package server

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/remote"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

// fakeChatTray shows chats like the tray app.
type fakeChatTray struct {
	fakeNotifier
	cmu    sync.Mutex
	states []agentproto.ChatState
	trays  int
}

func (f *fakeChatTray) Chat(c agentproto.ChatState) int {
	f.cmu.Lock()
	defer f.cmu.Unlock()
	f.states = append(f.states, c)
	return f.trays
}

func (f *fakeChatTray) last() (agentproto.ChatState, int) {
	f.cmu.Lock()
	defer f.cmu.Unlock()
	if len(f.states) == 0 {
		return agentproto.ChatState{}, 0
	}
	return f.states[len(f.states)-1], len(f.states)
}

// TestChat runs a chat from the web interface to the tray and back, linked
// to a help request whose service desk ticket gets the transcript.
func TestChat(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()
	bg := context.Background()
	_, info := e.createToken(admin, 1)
	tray := &fakeChatTray{trays: 1}
	a := newTestAgent(t, e, info.Token, "PC-CHAT", false)
	a.Notify, a.Minute = tray, 10*time.Millisecond
	ctx, cancel := context.WithTimeout(bg, 60*time.Second)
	defer cancel()
	go a.Run(ctx) //nolint:errcheck
	d := e.onlineDevice(admin, "PC-CHAT")

	var v chatView
	if code := admin.do("GET", "/api/v1/devices/"+d.ID+"/chat", nil, &v); code != 200 || v.Chat != nil || !v.Online {
		t.Fatalf("no chat yet: %d %+v", code, v)
	}
	// The user cannot start a chat.
	const user = `CORP\jan`
	ask := func(text string) agentproto.SelfServiceReply {
		var r agentproto.SelfServiceReply
		waitFor(t, "self-service reply", func() bool {
			r = a.SelfService(ctx, agentproto.SelfServiceRequest{Op: agentproto.SelfServiceOpChat, User: user, Lang: "en", Text: text})
			return r.Error != agentproto.SelfServiceErrOffline && r.Error != agentproto.SelfServiceErrNoService
		})
		return r
	}
	if r := ask("hello?"); r.Error != agentproto.SelfServiceErrNotFound {
		t.Errorf("chat without an open chat: %+v", r)
	}

	h, _ := e.st.CreateHelpRequest(bg, store.HelpRequest{DeviceID: d.ID, Username: user, Subject: "Printer"})
	if code := admin.do("POST", "/api/v1/devices/"+d.ID+"/chat", map[string]any{"text": " "}, nil); code != 400 {
		t.Errorf("empty message: %d", code)
	}
	if code := admin.do("POST", "/api/v1/devices/"+d.ID+"/chat", map[string]any{"text": "Hi Jan, I am looking at your printer.", "helpRequestId": h}, &v); code != 200 ||
		v.Chat == nil || len(v.Chat.Messages) != 1 || v.Chat.HelpRequestID == nil || *v.Chat.HelpRequestID != h || v.Chat.EndedAt != nil {
		t.Fatalf("start chat: %d %+v", code, v)
	}
	chatID := v.Chat.ID
	waitFor(t, "chat in the tray", func() bool {
		st, _ := tray.last()
		return st.ID == chatID && st.Open && len(st.Lines) == 1 && st.Lines[0].From == agentproto.ChatFromTech
	})
	waitFor(t, "delivery", func() bool {
		c, _ := e.st.ChatByID(bg, chatID)
		return c.DeliveredAt != nil && c.Trays == 1
	})

	// The user answers; the catalog shows the open chat.
	if r := ask("Thanks! It shows error 49."); r.Error != "" || r.Catalog == nil || r.Catalog.Chat == nil || len(r.Catalog.Chat.Lines) != 2 ||
		r.Catalog.Chat.Lines[1].Name != "jan" {
		t.Fatalf("user answer: %+v", r)
	}
	admin.do("GET", "/api/v1/devices/"+d.ID+"/chat", nil, &v)
	if len(v.Chat.Messages) != 2 || v.Chat.Messages[1].Sender != "user" || v.Chat.Username != user {
		t.Fatalf("chat after answer: %+v", v.Chat)
	}
	// A second message keeps the same chat.
	admin.do("POST", "/api/v1/devices/"+d.ID+"/chat", map[string]any{"text": "Fixed, try again."}, &v)
	if v.Chat.ID != chatID || len(v.Chat.Messages) != 3 {
		t.Errorf("same chat: %+v", v.Chat)
	}

	// Ending tells the tray; the user can no longer write.
	if code := admin.do("POST", "/api/v1/devices/"+d.ID+"/chat/end", nil, &v); code != 200 || v.Chat.EndedAt == nil || v.Chat.EndedBy != "admin" {
		t.Fatalf("end: %d %+v", code, v.Chat)
	}
	waitFor(t, "ended in the tray", func() bool { st, _ := tray.last(); return st.ID == chatID && !st.Open })
	if r := ask("one more thing"); r.Error != agentproto.SelfServiceErrNotFound {
		t.Errorf("answer after the end: %+v", r)
	}
	if code := admin.do("POST", "/api/v1/devices/"+d.ID+"/chat/end", nil, nil); code != 409 {
		t.Errorf("end twice: %d", code)
	}
	tr := chatTranscript(func() store.Chat { c, _ := e.st.ChatByID(bg, chatID); return c }(), "en")
	if !strings.Contains(tr, "jan") && !strings.Contains(tr, `CORP\jan`) || !strings.Contains(tr, "error 49") {
		t.Errorf("transcript: %s", tr)
	}

	// Idle chats end by themselves.
	admin.do("POST", "/api/v1/devices/"+d.ID+"/chat", map[string]any{"text": "Are you there?"}, &v)
	realNow := e.srv.now
	e.srv.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	e.srv.chatTick(bg)
	e.srv.now = realNow
	if c, _ := e.st.ChatByID(bg, v.Chat.ID); c.EndedAt == nil || c.EndedBy != "timeout" {
		t.Errorf("idle chat: %+v", c)
	}
	var audit []store.AuditEntry
	admin.do("GET", "/api/v1/audit", nil, &audit)
	n := 0
	for _, it := range audit {
		if it.Action == "chat.start" || it.Action == "chat.end" {
			n++
		}
	}
	if n < 3 {
		t.Errorf("audit entries for chats: %d", n)
	}
}

// TestRemoteUnattended takes over a workstation whose user does not answer,
// only when the group allows it and the technician chooses it.
func TestRemoteUnattended(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()
	_, info := e.createToken(admin, 1)
	desk := &fakeDesktop{p: remote.NewFakePlatform()}
	consent := &fakeConsent{noAnswer: true}
	a := newTestAgent(t, e, info.Token, "PC-AWAY", false)
	a.Remote, a.Notify = desk, consent
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	go a.Run(ctx) //nolint:errcheck
	d := e.onlineDevice(admin, "PC-AWAY")
	setPlatform(t, d.ID, "windows")

	start := func(unattended bool) (int, store.RemoteSession) {
		var rs store.RemoteSession
		code := admin.do("POST", "/api/v1/devices/"+d.ID+"/remote", map[string]any{"kind": "desktop", "unattended": unattended}, &rs)
		return code, rs
	}
	if code, _ := start(true); code != 403 {
		t.Errorf("unattended without the group setting: %d", code)
	}
	// Without unattended, no answer means no session.
	_, rs := start(false)
	if done := waitRemote(t, admin, rs.ID, func(r store.RemoteSession) bool { return r.EndedAt != nil }); done.Reason != "no_answer" {
		t.Fatalf("attended, no answer: %+v", done)
	}

	var g store.DeviceGroup
	if code := admin.do("PUT", "/api/v1/groups/1", groupReq{Name: "Default", UpdateRing: 1, RemoteUnattended: true}, &g); code != 200 || !g.RemoteUnattended {
		t.Fatalf("group: %d %+v", code, g)
	}
	code, rs := start(true)
	if code != 201 || rs.Consent != "ask" {
		t.Fatalf("unattended start: %d %+v", code, rs)
	}
	v := admin.openViewer(rs.ID)
	v.waitState("active")
	waitFor(t, "banner for a returning user", func() bool { return consent.banner(rs.ID) != nil })
	var audit []store.AuditEntry
	admin.do("GET", "/api/v1/audit", nil, &audit)
	found := false
	for _, it := range audit {
		if it.Action == "remote.start" && fmt.Sprint(it.Details["unattended"]) == "true" {
			found = true
		}
	}
	if !found {
		t.Errorf("audit lacks unattended: %+v", audit)
	}
	// A user who denies still wins.
	consent.mu.Lock()
	consent.noAnswer, consent.allow = false, false
	consent.mu.Unlock()
	_, rs2 := start(true)
	if done := waitRemote(t, admin, rs2.ID, func(r store.RemoteSession) bool { return r.EndedAt != nil }); done.Reason != "user_denied" {
		t.Errorf("denied: %+v", done)
	}
}
