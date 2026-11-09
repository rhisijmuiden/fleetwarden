package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

// Remote control: the operator's browser and the agent each open a
// WebSocket to the server, which relays the session between them. The
// agent's leg is encrypted with keys from an X25519 exchange (see
// agentproto/remote.go); the browser's leg is the browser's own HTTPS.

const (
	remoteConsentSeconds = 30
	remoteViewerWait     = 60 * time.Second // to open the viewer after starting a session
	remoteAgentWait      = 60 * time.Second // for the agent to connect, on top of the consent time
	remoteViewerLimit    = 2 << 20          // clipboard text and typed text
	remoteAgentLimit     = 40 << 20         // a full-screen frame
	remoteLogDays        = 400
)

type remoteMsg struct {
	typ  websocket.MessageType
	data []byte
}

// remoteSession is a session between creation and its end.
type remoteSession struct {
	store.RemoteSession
	key   *agentproto.RemoteKey
	viewC chan remoteMsg // to the viewer
	done  chan struct{}

	mu     sync.Mutex
	viewer bool
	agent  *websocket.Conn
	ciph   *agentproto.RemoteCipher
	ended  bool

	down, up atomic.Int64
}

type remoteHub struct {
	mu       sync.Mutex
	sessions map[string]*remoteSession
}

func (h *remoteHub) get(id string) *remoteSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessions[id]
}

func (h *remoteHub) put(r *remoteSession) {
	h.mu.Lock()
	if h.sessions == nil {
		h.sessions = map[string]*remoteSession{}
	}
	h.sessions[r.ID] = r
	h.mu.Unlock()
}

func (h *remoteHub) drop(id string) {
	h.mu.Lock()
	delete(h.sessions, id)
	h.mu.Unlock()
}

func (h *remoteHub) count(deviceID, kind string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, r := range h.sessions {
		if r.DeviceID == deviceID && r.Kind == kind {
			n++
		}
	}
	return n
}

func (s *Server) remoteRoutes(mux *http.ServeMux) {
	ctl := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermRemoteControl, 0, h) }
	mux.Handle("POST /api/v1/devices/{id}/remote", ctl(s.handleStartRemote))
	mux.Handle("GET /api/v1/remote/sessions", ctl(s.handleListRemote))
	mux.Handle("GET /api/v1/remote/sessions/{sid}", ctl(s.handleGetRemote))
	mux.Handle("POST /api/v1/remote/sessions/{sid}/end", ctl(s.handleEndRemote))
	mux.Handle("GET /api/v1/remote/sessions/{sid}/ws", ctl(s.handleRemoteViewer))
	mux.HandleFunc("GET "+agentproto.PathRemote+"{sid}/{key}", s.handleRemoteAgent)
	mux.Handle("GET /api/v1/settings/remote", s.authed(auth.PermSettingsManage, 0, s.handleGetRemoteSettings))
	mux.Handle("PUT /api/v1/settings/remote", s.authed(auth.PermSettingsManage, 0, s.handlePutRemoteSettings))
}

// consentFor resolves a group's setting for a device: "auto" takes servers
// over directly and asks on workstations.
func consentFor(groupSetting, kind string) string {
	switch groupSetting {
	case agentproto.ConsentDirect, agentproto.ConsentNotify, agentproto.ConsentAsk:
		return groupSetting
	}
	if kind == "server" || kind == "hypervisor" {
		return agentproto.ConsentDirect
	}
	return agentproto.ConsentAsk
}

func validConsentSetting(v string) bool {
	switch v {
	case "", "auto", agentproto.ConsentDirect, agentproto.ConsentNotify, agentproto.ConsentAsk:
		return true
	}
	return false
}

type startRemoteReq struct {
	Kind  string `json:"kind"`
	Shell string `json:"shell"` // terminal on Windows: powershell or cmd
	Cols  int    `json:"cols"`
	Rows  int    `json:"rows"`
}

// Sessions per device, by kind.
var remoteLimit = map[string]int{agentproto.RemoteDesktop: 2, agentproto.RemoteTerminal: 3}

// remoteSupported reports whether a device's platform has a session kind.
func remoteSupported(kind, platform string) bool {
	switch kind {
	case agentproto.RemoteDesktop:
		return platform == "windows"
	case agentproto.RemoteTerminal:
		return platform == "windows" || platform == "linux"
	}
	return false
}

func (s *Server) handleStartRemote(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	var req startRemoteReq
	if !decode(w, r, &req) {
		return
	}
	if req.Kind == "" {
		req.Kind = agentproto.RemoteDesktop
	}
	if !remoteSupported(req.Kind, dev.Platform) {
		apiError(w, http.StatusBadRequest, "remote_unsupported")
		return
	}
	u, _ := currentUser(r)
	terminal := req.Kind == agentproto.RemoteTerminal
	if terminal && !auth.Role(u.Role).Has(auth.PermRemoteTerminal) {
		apiError(w, http.StatusForbidden, "forbidden")
		return
	}
	switch req.Shell {
	case "", "powershell", "cmd":
	default:
		apiError(w, http.StatusBadRequest, "invalid_terminal_shell")
		return
	}
	if !terminal || dev.Platform != "windows" {
		req.Shell = ""
	}
	if !dev.Online {
		apiError(w, http.StatusConflict, "device_offline")
		return
	}
	if s.remote.count(dev.ID, req.Kind) >= remoteLimit[req.Kind] {
		apiError(w, http.StatusConflict, "remote_busy")
		return
	}
	g, err := s.st.GroupByID(r.Context(), dev.GroupID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	key, err := agentproto.NewRemoteKey()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	operator := u.DisplayName
	if operator == "" {
		operator = u.Username
	}
	// A terminal shows nothing of the user's screen (like SSH), so nobody
	// is asked.
	consent := agentproto.ConsentDirect
	if !terminal {
		consent = consentFor(g.RemoteConsent, dev.Kind)
	}
	rs := &remoteSession{key: key, viewC: make(chan remoteMsg, 64), done: make(chan struct{})}
	rs.RemoteSession = store.RemoteSession{ID: newUUID(), DeviceID: dev.ID, Hostname: dev.Hostname, GroupID: dev.GroupID, UserID: &u.ID,
		Operator: u.Username, Kind: req.Kind, Consent: consent, State: "pending", RemoteIP: ClientIP(r),
		CreatedAt: s.now()}
	if err := s.st.CreateRemoteSession(r.Context(), rs.RemoteSession); err != nil {
		s.internalError(w, r, err)
		return
	}
	b := s.loadBranding(r.Context())
	var rtc *rtcConfig
	if !terminal {
		if rset := s.loadRemoteSettings(r.Context()); !rset.DirectOff {
			rtc = &rtcConfig{ICEServers: rset.STUNServers}
		}
	}
	s.remote.put(rs)
	if !s.hub.send(dev.ID, agentproto.TypeRemoteStart, agentproto.RemoteStart{
		SessionID: rs.ID, Kind: rs.Kind, Operator: operator, Consent: rs.Consent, ConsentSeconds: remoteConsentSeconds,
		ServerKey: key.Public(), ProductName: b.ProductName, AccentColor: b.PrimaryColor,
		Direct: rtc != nil, ICEServers: rtcServers(rtc),
		Shell: req.Shell, Cols: min(max(req.Cols, 0), 1000), Rows: min(max(req.Rows, 0), 500),
	}) {
		s.endRemote(rs, agentproto.RemoteStateFailed, "device_offline")
		apiError(w, http.StatusConflict, "device_offline")
		return
	}
	details := map[string]any{"session": rs.ID, "deviceId": dev.ID, "kind": rs.Kind, "consent": rs.Consent}
	if req.Shell != "" {
		details["shell"] = req.Shell
	}
	s.audit(r, "remote.start", dev.Hostname, true, details)
	go s.watchRemote(rs)
	writeJSON(w, http.StatusCreated, startRemoteResp{RemoteSession: rs.RemoteSession, RTC: rtc})
}

// rtcConfig tells the viewer it may set up the direct path.
type rtcConfig struct {
	ICEServers []string `json:"iceServers"`
}

type startRemoteResp struct {
	store.RemoteSession
	RTC *rtcConfig `json:"rtc,omitempty"`
}

func rtcServers(c *rtcConfig) []string {
	if c == nil {
		return nil
	}
	return c.ICEServers
}

// watchRemote ends sessions whose viewer or agent never shows up.
func (s *Server) watchRemote(rs *remoteSession) {
	viewer := time.NewTimer(remoteViewerWait)
	agent := time.NewTimer(remoteConsentSeconds*time.Second + remoteAgentWait)
	defer viewer.Stop()
	defer agent.Stop()
	for {
		select {
		case <-rs.done:
			return
		case <-s.bgCtx.Done():
			s.endRemote(rs, agentproto.RemoteStateEnded, "server_stopping")
			return
		case <-viewer.C:
			rs.mu.Lock()
			ok := rs.viewer
			rs.mu.Unlock()
			if !ok {
				s.endRemote(rs, agentproto.RemoteStateFailed, "viewer_timeout")
				return
			}
		case <-agent.C:
			rs.mu.Lock()
			ok := rs.agent != nil
			rs.mu.Unlock()
			if !ok {
				s.endRemote(rs, agentproto.RemoteStateFailed, "agent_timeout")
				return
			}
		}
	}
}

type remoteStatusMsg struct {
	Type    string `json:"type"`
	State   string `json:"state"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
	Consent string `json:"consent,omitempty"`
}

// toViewer queues a message for the viewer; it waits while the viewer is
// slow, so the agent slows down too.
func (rs *remoteSession) toViewer(m remoteMsg) bool {
	select {
	case rs.viewC <- m:
		return true
	case <-rs.done:
		return false
	}
}

func (rs *remoteSession) status(state, reason, message string) {
	raw, _ := json.Marshal(remoteStatusMsg{Type: "status", State: state, Reason: reason, Message: message, Consent: rs.Consent})
	select {
	case rs.viewC <- remoteMsg{websocket.MessageText, raw}:
	default: // the viewer reads the state again when it connects
	}
}

var stateRank = map[string]int{"pending": 0, "consent": 1, "connecting": 2, "active": 3}

// setRemoteState records progress and tells the viewer.
func (s *Server) setRemoteState(rs *remoteSession, state, message string) {
	rs.mu.Lock()
	// The agent's status reports and its session socket arrive on different
	// connections, so "connecting" can come after "active": never go back.
	if rs.ended || stateRank[state] <= stateRank[rs.State] {
		rs.mu.Unlock()
		return
	}
	rs.State = state
	rs.status(state, "", message) // under the lock, so the viewer gets the states in order
	rs.mu.Unlock()
	ctx, cancel := context.WithTimeout(s.bgCtx, 5*time.Second)
	defer cancel()
	_ = s.st.SetRemoteState(ctx, rs.ID, state)
}

// endRemote finishes a session once: log, audit, tell both sides.
func (s *Server) endRemote(rs *remoteSession, state, reason string) {
	rs.mu.Lock()
	if rs.ended {
		rs.mu.Unlock()
		return
	}
	rs.ended = true
	rs.State, rs.Reason = state, reason
	agent := rs.agent
	rs.mu.Unlock()

	raw, _ := json.Marshal(remoteStatusMsg{Type: "status", State: state, Reason: reason, Consent: rs.Consent})
	select { // the last word for the viewer, before done closes its writer
	case rs.viewC <- remoteMsg{websocket.MessageText, raw}:
	default:
	}
	close(rs.done)
	s.remote.drop(rs.ID)
	if agent != nil {
		agent.Close(websocket.StatusNormalClosure, reason) //nolint:errcheck
	} else {
		s.hub.send(rs.DeviceID, agentproto.TypeRemoteStop, agentproto.RemoteStop{SessionID: rs.ID, Reason: reason})
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.bgCtx), 5*time.Second)
	defer cancel()
	if ok, err := s.st.EndRemoteSession(ctx, rs.ID, state, reason, rs.down.Load(), rs.up.Load()); err != nil || !ok {
		return
	}
	details := map[string]any{"session": rs.ID, "deviceId": rs.DeviceID, "state": state, "reason": reason,
		"seconds": int(s.now().Sub(rs.CreatedAt).Seconds()), "bytes": rs.down.Load()}
	e := store.AuditEntry{IP: rs.RemoteIP, Action: "remote.end", Target: rs.Hostname, Success: state == agentproto.RemoteStateEnded,
		Details: details, Actor: rs.Operator, ActorID: rs.UserID}
	if err := s.st.Audit(ctx, e); err != nil {
		s.log.Error("audit log write failed", "action", e.Action, "err", err)
	}
	s.log.Info("remote session ended", "session", rs.ID, "device", rs.DeviceID, "operator", rs.Operator, "state", state, "reason", reason)
}

// handleRemoteStatusMsg takes the agent's progress reports.
func (s *Server) handleRemoteStatusMsg(c *agentConn, e *agentproto.Envelope) error {
	var st agentproto.RemoteStatus
	if err := e.Decode(&st); err != nil {
		return err
	}
	rs := s.remote.get(st.SessionID)
	if rs == nil || rs.DeviceID != c.deviceID {
		return nil // ended already
	}
	reason := cleanText(st.Reason, 64)
	switch st.State {
	case agentproto.RemoteStateConsent, agentproto.RemoteStateConnecting:
		s.setRemoteState(rs, st.State, "")
	case agentproto.RemoteStateDenied, agentproto.RemoteStateFailed, agentproto.RemoteStateEnded:
		s.endRemote(rs, st.State, reason)
	}
	return nil
}

func (s *Server) remoteInScope(w http.ResponseWriter, r *http.Request) (store.RemoteSession, bool) {
	rs, err := s.st.RemoteSessionByID(r.Context(), r.PathValue("sid"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && !s.scope(r).Allows(rs.GroupID)) {
		apiError(w, http.StatusNotFound, "not_found")
		return rs, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return rs, false
	}
	return rs, true
}

func (s *Server) handleGetRemote(w http.ResponseWriter, r *http.Request) {
	if rs, ok := s.remoteInScope(w, r); ok {
		writeJSON(w, http.StatusOK, rs)
	}
}

type remoteList struct {
	Items []store.RemoteSession `json:"items"`
	Total int                   `json:"total"`
}

func (s *Server) handleListRemote(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rq := store.RemoteQuery{Scope: s.scope(r), DeviceID: q.Get("device"), Open: q.Get("open") == "true"}
	rq.Limit, _ = strconv.Atoi(q.Get("limit"))
	rq.Offset, _ = strconv.Atoi(q.Get("offset"))
	items, total, err := s.st.ListRemoteSessions(r.Context(), rq)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, remoteList{Items: items, Total: total})
}

func (s *Server) handleEndRemote(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.remoteInScope(w, r)
	if !ok {
		return
	}
	if rs := s.remote.get(rec.ID); rs != nil {
		u, _ := currentUser(r)
		reason := "operator_ended"
		if rs.UserID == nil || *rs.UserID != u.ID {
			reason = "ended_by_admin"
		}
		s.audit(r, "remote.terminate", rec.Hostname, true, map[string]any{"session": rec.ID, "operator": rec.Operator})
		s.endRemote(rs, agentproto.RemoteStateEnded, reason)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRemoteViewer is the operator's side of a session.
func (s *Server) handleRemoteViewer(w http.ResponseWriter, r *http.Request) {
	rs := s.remote.get(r.PathValue("sid"))
	u, _ := currentUser(r)
	if rs == nil || rs.UserID == nil || *rs.UserID != u.ID || !s.scope(r).Allows(rs.GroupID) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	rs.mu.Lock()
	taken := rs.viewer || rs.ended
	rs.viewer = true
	rs.mu.Unlock()
	if taken {
		apiError(w, http.StatusConflict, "remote_viewer_connected")
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		s.endRemote(rs, agentproto.RemoteStateFailed, "viewer_failed")
		return
	}
	defer ws.CloseNow() //nolint:errcheck
	ws.SetReadLimit(remoteViewerLimit)
	ctx, cancel := context.WithCancel(s.bgCtx)
	defer cancel()
	rs.mu.Lock()
	rs.status(rs.State, "", "")
	rs.mu.Unlock()

	go func() { // reader: viewer -> agent
		defer cancel()
		for {
			typ, data, err := ws.Read(ctx)
			if err != nil {
				s.endRemote(rs, agentproto.RemoteStateEnded, "viewer_closed")
				return
			}
			if typ != websocket.MessageBinary || len(data) == 0 {
				continue
			}
			rs.mu.Lock()
			agent, ciph := rs.agent, rs.ciph
			rs.mu.Unlock()
			if agent == nil {
				continue // not connected yet: input is dropped
			}
			rs.up.Add(int64(len(data)))
			wctx, wcancel := context.WithTimeout(ctx, agentWriteTimeout)
			err = agent.Write(wctx, websocket.MessageBinary, ciph.Seal(data))
			wcancel()
			if err != nil {
				// The agent leg is going away; its handler ends the session
				// with the reason the agent reports. Input is dropped.
				continue
			}
		}
	}()
	ping := time.NewTicker(agentPingInterval)
	defer ping.Stop()
	write := func(m remoteMsg) error {
		wctx, wcancel := context.WithTimeout(ctx, agentWriteTimeout)
		defer wcancel()
		return ws.Write(wctx, m.typ, m.data)
	}
	for {
		select {
		case m := <-rs.viewC:
			if err := write(m); err != nil {
				s.endRemote(rs, agentproto.RemoteStateEnded, "viewer_closed")
				return
			}
		case <-ping.C:
			pctx, pcancel := context.WithTimeout(ctx, agentWriteTimeout)
			_ = ws.Ping(pctx)
			pcancel()
		case <-rs.done:
		flush: // the final status
			for {
				select {
				case m := <-rs.viewC:
					if m.typ == websocket.MessageText {
						_ = write(m)
					}
				default:
					break flush
				}
			}
			ws.Close(websocket.StatusNormalClosure, "") //nolint:errcheck
			return
		case <-ctx.Done():
			return
		}
	}
}

// handleRemoteAgent is the agent's side of a session. The request is signed
// with the device key and names the agent's session key.
func (s *Server) handleRemoteAgent(w http.ResponseWriter, r *http.Request) {
	ip := ClientIP(r)
	if s.agentFails.Blocked(ip) {
		apiError(w, http.StatusTooManyRequests, "rate_limited")
		return
	}
	rs := s.remote.get(r.PathValue("sid"))
	peer, kerr := agentproto.DecodeRemoteKey(r.PathValue("key"))
	devID, ts, sig, err := agentproto.ParseRequestAuth(r.Header.Get(agentproto.HeaderAuth))
	if rs == nil || kerr != nil || err != nil || devID != rs.DeviceID {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	dev, err := s.st.DeviceByID(r.Context(), devID)
	if err != nil || !agentproto.VerifyRequest(dev.PublicKey, dev.ID, r.Method, r.URL.Path, ts, sig, s.now(), 5*time.Minute) {
		s.agentFails.Fail(ip)
		apiError(w, http.StatusUnauthorized, "bad_signature")
		return
	}
	ciph, err := rs.key.Cipher(peer, rs.ID, false)
	if err != nil {
		apiError(w, http.StatusBadRequest, "bad_key")
		return
	}
	rs.mu.Lock()
	taken := rs.agent != nil || rs.ended
	rs.mu.Unlock()
	if taken {
		apiError(w, http.StatusConflict, "already_connected")
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer ws.CloseNow() //nolint:errcheck
	ws.SetReadLimit(remoteAgentLimit)
	rs.mu.Lock()
	if rs.agent != nil || rs.ended {
		rs.mu.Unlock()
		ws.Close(websocket.StatusPolicyViolation, "already connected") //nolint:errcheck
		return
	}
	rs.agent, rs.ciph = ws, ciph
	rs.mu.Unlock()
	s.setRemoteState(rs, "active", "")

	ctx, cancel := context.WithCancel(s.bgCtx)
	defer cancel()
	go func() {
		ping := time.NewTicker(agentPingInterval)
		defer ping.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-rs.done:
				cancel()
				return
			case <-ping.C:
				pctx, pcancel := context.WithTimeout(ctx, agentWriteTimeout)
				_ = ws.Ping(pctx)
				pcancel()
			}
		}
	}()
	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			// The agent reports why it ended on its control channel; give
			// that a moment to arrive.
			select {
			case <-rs.done:
			case <-time.After(2 * time.Second):
				s.endRemote(rs, agentproto.RemoteStateEnded, "agent_closed")
			}
			return
		}
		if typ != websocket.MessageBinary {
			continue
		}
		plain, err := ciph.Open(data)
		if err != nil {
			s.endRemote(rs, agentproto.RemoteStateFailed, "bad_message")
			return
		}
		rs.down.Add(int64(len(plain)))
		if !rs.toViewer(remoteMsg{websocket.MessageBinary, plain}) {
			return
		}
	}
}
