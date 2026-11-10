package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

// Chat between a technician (web interface) and the user signed on to a
// device (tray app). The technician starts it, the user can only answer an
// open chat. The web interface polls; the tray gets every change pushed.

const (
	chatIdle       = time.Hour // an open chat without messages ends after this
	chatMaxText    = 2000
	chatMaxLines   = 200 // lines kept in the state sent to the tray
	chatMaxPerChat = 1000
)

func (s *Server) chatRoutes(mux *http.ServeMux) {
	handle := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermSelfServiceHandle, 0, h) }
	mux.Handle("GET /api/v1/devices/{id}/chat", handle(s.handleGetChat))
	mux.Handle("POST /api/v1/devices/{id}/chat", handle(s.handleSendChat))
	mux.Handle("POST /api/v1/devices/{id}/chat/end", handle(s.handleEndChat))
}

type chatView struct {
	Chat   *store.Chat `json:"chat"` // the open chat, else the last one, else null
	Online bool        `json:"online"`
}

func (s *Server) handleGetChat(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	c, err := s.st.LastChat(r.Context(), dev.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, chatView{Online: dev.Online})
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, chatView{Chat: &c, Online: dev.Online})
}

type chatSendReq struct {
	Text string `json:"text"`
	// HelpRequestID links a new chat to a help request: the transcript is
	// added to its service desk ticket when the chat ends.
	HelpRequestID int64 `json:"helpRequestId"`
}

func (s *Server) handleSendChat(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	var req chatSendReq
	if !decode(w, r, &req) {
		return
	}
	text := cleanOutput(req.Text, chatMaxText)
	if strings.TrimSpace(text) == "" {
		apiError(w, http.StatusBadRequest, "invalid_message")
		return
	}
	u, _ := currentUser(r)
	name := u.DisplayName
	if name == "" {
		name = u.Username
	}
	ctx := r.Context()
	c, err := s.st.OpenChat(ctx, dev.ID)
	if errors.Is(err, store.ErrNotFound) {
		if !dev.Online {
			apiError(w, http.StatusConflict, "device_offline")
			return
		}
		nc := store.Chat{DeviceID: dev.ID, StartedBy: u.Username, Operator: name}
		if req.HelpRequestID > 0 {
			if h, err := s.st.HelpRequestByID(ctx, req.HelpRequestID); err == nil && h.DeviceID == dev.ID {
				id := h.ID
				nc.HelpRequestID = &id
			}
		}
		c, err = s.st.StartChat(ctx, nc)
		if errors.Is(err, store.ErrChatOpen) {
			c, err = s.st.OpenChat(ctx, dev.ID)
		} else if err == nil {
			s.audit(r, "chat.start", dev.Hostname, true, map[string]any{"chat": c.ID, "deviceId": dev.ID, "helpRequest": c.HelpRequestID})
		}
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if len(c.Messages) >= chatMaxPerChat {
		apiError(w, http.StatusConflict, "chat_full")
		return
	}
	if _, err := s.st.AddChatMessage(ctx, c.ID, store.ChatMessage{Sender: agentproto.ChatFromTech, Name: name, Body: text}); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			apiError(w, http.StatusConflict, "chat_ended")
			return
		}
		s.internalError(w, r, err)
		return
	}
	out, err := s.st.ChatByID(ctx, c.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.pushChat(ctx, out)
	writeJSON(w, http.StatusOK, chatView{Chat: &out, Online: s.hub.online(dev.ID)})
}

func (s *Server) handleEndChat(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	c, err := s.st.OpenChat(ctx, dev.ID)
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusConflict, "chat_ended")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	u, _ := currentUser(r)
	if err := s.st.EndChat(ctx, c.ID, u.Username); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, r, err)
		return
	}
	out, err := s.st.ChatByID(ctx, c.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.chatEnded(ctx, out)
	s.audit(r, "chat.end", dev.Hostname, true, map[string]any{"chat": c.ID, "deviceId": dev.ID, "messages": len(out.Messages)})
	writeJSON(w, http.StatusOK, chatView{Chat: &out, Online: dev.Online})
}

// chatEnded tells the tray and adds the transcript to the service desk
// ticket of the help request.
func (s *Server) chatEnded(ctx context.Context, c store.Chat) {
	s.pushChat(ctx, c)
	if c.HelpRequestID == nil || len(c.Messages) == 0 {
		return
	}
	h, err := s.st.HelpRequestByID(ctx, *c.HelpRequestID)
	if err != nil {
		return
	}
	s.ticketNote(ctx, h, chatTranscript(c, s.loadSelfService(ctx).Language))
}

// chatTranscript is the chat as plain text for a ticket note.
func chatTranscript(c store.Chat, lang string) string {
	var b strings.Builder
	b.WriteString(ssText(lang, "chatTranscript", c.Operator))
	b.WriteString("\n\n")
	for _, m := range c.Messages {
		b.WriteString(m.CreatedAt.Local().Format("15:04"))
		b.WriteString(" ")
		b.WriteString(m.Name)
		b.WriteString(": ")
		b.WriteString(m.Body)
		b.WriteString("\n")
	}
	return b.String()
}

// chatState is the chat as the tray shows it.
func (s *Server) chatState(ctx context.Context, c store.Chat) agentproto.ChatState {
	b := s.loadBranding(ctx)
	st := agentproto.ChatState{ID: c.ID, Operator: c.Operator, Open: c.EndedAt == nil, Lines: []agentproto.ChatLine{},
		ProductName: b.ProductName, AccentColor: b.PrimaryColor}
	msgs := c.Messages
	if len(msgs) > chatMaxLines {
		msgs = msgs[len(msgs)-chatMaxLines:]
	}
	for _, m := range msgs {
		name := m.Name
		if m.Sender == agentproto.ChatFromUser {
			name = shortAccount(name)
		}
		st.Lines = append(st.Lines, agentproto.ChatLine{ID: m.ID, From: m.Sender, Name: name, Text: m.Body, At: m.CreatedAt})
	}
	return st
}

// shortAccount drops the domain of DOMAIN\name.
func shortAccount(a string) string {
	if i := strings.LastIndex(a, `\`); i >= 0 {
		return a[i+1:]
	}
	return a
}

// pushChat sends the chat to the device's tray.
func (s *Server) pushChat(ctx context.Context, c store.Chat) {
	s.hub.send(c.DeviceID, agentproto.TypeChat, s.chatState(ctx, c))
}

// handleChatStatusMsg records whether a tray shows the chat.
func (s *Server) handleChatStatusMsg(ctx context.Context, c *agentConn, e *agentproto.Envelope) error {
	var st agentproto.ChatStatus
	if err := e.Decode(&st); err != nil {
		return err
	}
	ch, err := s.st.ChatByID(ctx, st.ID)
	if err != nil || ch.DeviceID != c.deviceID {
		return nil
	}
	return s.st.SetChatDelivery(ctx, st.ID, max(st.Trays, 0))
}

// selfServiceChat is the user's answer in the open chat.
func (s *Server) selfServiceChat(ctx context.Context, dev store.Device, user, lang, text string) agentproto.SelfServiceReply {
	text = cleanOutput(text, chatMaxText)
	if strings.TrimSpace(text) == "" {
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrInvalid}
	}
	if user == "" {
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrNoUser}
	}
	c, err := s.st.OpenChat(ctx, dev.ID)
	if errors.Is(err, store.ErrNotFound) {
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrNotFound, Message: ssText(lang, "chatEnded")}
	}
	if err != nil {
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrInternal}
	}
	if len(c.Messages) >= chatMaxPerChat {
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrLimit}
	}
	if _, err := s.st.AddChatMessage(ctx, c.ID, store.ChatMessage{Sender: agentproto.ChatFromUser, Name: user, Body: text}); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrNotFound, Message: ssText(lang, "chatEnded")}
		}
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrInternal}
	}
	if out, err := s.st.ChatByID(ctx, c.ID); err == nil {
		// Other trays on the device (fast user switching) see it too.
		s.pushChat(ctx, out)
	}
	return agentproto.SelfServiceReply{}
}

// openChatState is the open chat of a device for the catalog, or nil.
func (s *Server) openChatState(ctx context.Context, deviceID string) *agentproto.ChatState {
	c, err := s.st.OpenChat(ctx, deviceID)
	if err != nil {
		return nil
	}
	st := s.chatState(ctx, c)
	return &st
}

// chatTick ends chats nobody wrote in for an hour.
func (s *Server) chatTick(ctx context.Context) {
	list, err := s.st.EndIdleChats(ctx, s.now().Add(-chatIdle))
	if err != nil {
		s.log.Warn("idle chats", "err", err)
		return
	}
	for _, c := range list {
		if full, err := s.st.ChatByID(ctx, c.ID); err == nil {
			s.chatEnded(ctx, full)
		}
	}
}
