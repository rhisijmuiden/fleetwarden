package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

type fakeTray struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Scanner
}

func dialTray(t *testing.T, addr string) *fakeTray {
	t.Helper()
	c, err := net.Dial("unix", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	ft := &fakeTray{t: t, conn: c, r: bufio.NewScanner(c)}
	ft.send(trayMsg{Type: "hello", Lang: "nl-NL"})
	return ft
}

func (f *fakeTray) send(m trayMsg) {
	b, _ := json.Marshal(m)
	if _, err := f.conn.Write(append(b, '\n')); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fakeTray) next() trayMsg {
	f.t.Helper()
	_ = f.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if !f.r.Scan() {
		f.t.Fatalf("tray read: %v", f.r.Err())
	}
	var m trayMsg
	if err := json.Unmarshal(f.r.Bytes(), &m); err != nil {
		f.t.Fatal(err)
	}
	return m
}

func startHub(t *testing.T) (*trayHub, string) {
	t.Helper()
	addr := filepath.Join(t.TempDir(), "tray.sock")
	l, err := net.Listen("unix", addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h := newTrayHub(nil)
	h.joinWait = 2 * time.Second
	h.grace = 100 * time.Millisecond
	go h.Serve(ctx, l)
	return h, addr
}

func TestTrayHubPromptAndAnswer(t *testing.T) {
	h, addr := startHub(t)
	a := dialTray(t, addr)
	b := dialTray(t, addr)
	waitClients(t, h, 2)

	p := Prompt{ID: "7-1", Kind: PromptInstall, Updates: 3, CanPostpone: true, PostponesLeft: 2, PostponeMinutes: 30, CountdownSeconds: 60}
	got := make(chan Answer, 1)
	go func() {
		ans, err := h.Ask(context.Background(), p)
		if err != nil {
			t.Error(err)
		}
		got <- ans
	}()
	for _, tr := range []*fakeTray{a, b} {
		m := tr.next()
		if m.Type != "prompt" || m.Prompt == nil || m.Prompt.ID != "7-1" || m.Prompt.Updates != 3 {
			t.Fatalf("prompt = %+v", m)
		}
	}
	b.send(trayMsg{Type: "answer", ID: "old", Postpone: false}) // ignored
	b.send(trayMsg{Type: "answer", ID: "7-1", Postpone: true})
	if ans := <-got; !ans.Postpone {
		t.Fatalf("answer = %+v, want postpone", ans)
	}
	for _, tr := range []*fakeTray{a, b} {
		if m := tr.next(); m.Type != "close" || m.ID != "7-1" {
			t.Fatalf("close = %+v", m)
		}
	}

	// A postpone the prompt does not allow is no postpone.
	p2 := Prompt{ID: "7-2", Kind: PromptReboot, CountdownSeconds: 60}
	go func() {
		ans, _ := h.Ask(context.Background(), p2)
		got <- ans
	}()
	a.next()
	b.next()
	a.send(trayMsg{Type: "answer", ID: "7-2", Postpone: true})
	if ans := <-got; ans.Postpone {
		t.Fatal("postponed a prompt that cannot be postponed")
	}
}

func TestTrayHubLateTrayAndLaunch(t *testing.T) {
	h, addr := startHub(t)
	launched := make(chan uint32, 4)
	h.sessions = func() []uint32 { return []uint32{2} }
	h.launch = func(s uint32) error {
		launched <- s
		go func() { // the "started" tray connects
			c, err := net.Dial("unix", addr)
			if err != nil {
				t.Error(err)
				return
			}
			t.Cleanup(func() { c.Close() })
			_, _ = c.Write([]byte(`{"type":"hello"}` + "\n"))
		}()
		return nil
	}
	got := make(chan Answer, 1)
	go func() {
		ans, err := h.Ask(context.Background(), Prompt{ID: "9-1", Kind: PromptInstall, CanPostpone: true, CountdownSeconds: 1})
		if err != nil {
			t.Error(err)
		}
		got <- ans
	}()
	if s := <-launched; s != 2 {
		t.Fatalf("launched session %d", s)
	}
	// Nobody answers: after the countdown and grace the job goes ahead.
	select {
	case ans := <-got:
		if ans.Postpone {
			t.Fatal("unexpected postpone")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Ask did not time out")
	}
}

func TestTrayHubNoUser(t *testing.T) {
	h, _ := startHub(t)
	h.sessions = func() []uint32 { return nil }
	if _, err := h.Ask(context.Background(), Prompt{ID: "1-1", CountdownSeconds: 10}); !errors.Is(err, ErrNoUser) {
		t.Fatalf("err = %v, want ErrNoUser", err)
	}
	// A user, but the tray never connects.
	h.sessions = func() []uint32 { return []uint32{1} }
	h.joinWait = 50 * time.Millisecond
	if _, err := h.Ask(context.Background(), Prompt{ID: "1-2", CountdownSeconds: 10}); !errors.Is(err, ErrNoUser) {
		t.Fatalf("err = %v, want ErrNoUser", err)
	}
}

func waitClients(t *testing.T, h *trayHub, n int) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if c, _ := h.snapshot(); len(c) == n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("trays did not connect")
}

func TestWUACategory(t *testing.T) {
	const (
		security = "{0FA1201D-4330-4FA8-8AE9-B877473B6441}"
		rollup   = "28bc880e-0592-4cbf-8f95-c79b17911d5f"
		defs     = "e0789628-ce08-4437-be74-2495b842f43b"
		upgrade  = "3689bdc8-b205-4af4-8d4a-a63924c5e9d5"
		critical = "e6cf1350-c01b-414d-a61f-263d14d133b4"
	)
	cases := []struct {
		ids    []string
		title  string
		driver bool
		want   string
	}{
		{[]string{security}, "2026-10 Cumulative Update for Windows 11 Version 24H2 (KB5066835)", false, agentproto.CategoryCumulative},
		{[]string{security}, "Security Update for Microsoft Office (KB5002700)", false, agentproto.CategorySecurity},
		{[]string{defs}, "Security Intelligence Update for Microsoft Defender Antivirus", false, agentproto.CategoryDefinition},
		{[]string{upgrade}, "Windows 11, version 25H2", false, agentproto.CategoryFeature},
		{[]string{rollup}, ".NET Framework rollup", false, agentproto.CategoryCumulative},
		{[]string{critical}, "Critical fix", false, agentproto.CategoryCritical},
		{nil, "Intel - Display - 31.0.101.5590", true, agentproto.CategoryDriver},
		{[]string{"unknown"}, "Something", false, agentproto.CategoryOther},
	}
	for _, c := range cases {
		if got := wuaCategory(c.ids, c.title, c.driver); got != c.want {
			t.Errorf("wuaCategory(%v, %q) = %s, want %s", c.ids, c.title, got, c.want)
		}
	}
	if wuaResultText(4, -2145124329) != "failed (0x80240017)" {
		t.Errorf("wuaResultText = %q", wuaResultText(4, -2145124329))
	}
}

func TestTrayHubCloseAndLaunchApps(t *testing.T) {
	h, addr := startHub(t)
	a := dialTray(t, addr)
	b := dialTray(t, addr)
	waitClients(t, h, 2)
	apps := []CloseApp{{Key: "chrome", Name: "Google Chrome", Process: "chrome", RestoreArgs: "--restore-last-session"}, {Key: "firefox", Name: "Mozilla Firefox", Process: "firefox", Firefox: true}}
	got := make(chan []string, 1)
	go func() { got <- h.CloseApps(context.Background(), "browsers-3", apps, time.Second) }()
	for i, tr := range []*fakeTray{a, b} {
		m := tr.next()
		if m.Type != "close-apps" || m.ID != "browsers-3" || len(m.Apps) != 2 || m.Apps[1].Firefox != true || m.TimeoutSeconds != 1 {
			t.Fatalf("tray %d got %+v", i, m)
		}
	}
	a.send(trayMsg{Type: "apps-closed", ID: "old", Closed: []string{"edge"}}) // another job: ignored
	a.send(trayMsg{Type: "apps-closed", ID: "browsers-3", Closed: []string{"chrome"}})
	b.send(trayMsg{Type: "apps-closed", ID: "browsers-3", Closed: []string{"chrome", "firefox"}})
	select {
	case keys := <-got:
		if len(keys) != 2 || keys[0] != "chrome" || keys[1] != "firefox" {
			t.Errorf("closed %v", keys)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CloseApps did not return")
	}
	h.LaunchApps(context.Background(), "browsers-3")
	if m := a.next(); m.Type != "launch-apps" || m.ID != "browsers-3" {
		t.Errorf("launch: %+v", m)
	}

	// A tray that does not answer does not block the update.
	start := time.Now()
	if keys := h.CloseApps(context.Background(), "browsers-4", apps, 10*time.Millisecond); len(keys) != 0 {
		t.Errorf("closed %v", keys)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("waited too long")
	}
}

func TestTrayHubChat(t *testing.T) {
	h, addr := startHub(t)
	var got agentproto.SelfServiceRequest
	h.SetSelfService(func(_ context.Context, req agentproto.SelfServiceRequest) agentproto.SelfServiceReply {
		got = req
		return agentproto.SelfServiceReply{}
	})
	if n := h.Chat(agentproto.ChatState{ID: 1, Open: true}); n != 0 {
		t.Errorf("no tray: %d", n)
	}
	a := dialTray(t, addr)
	// A tray that connects during an open chat gets it.
	if m := a.next(); m.Type != "chat" || m.Chat == nil || m.Chat.ID != 1 || !m.Chat.Open {
		t.Fatalf("late tray: %+v", m)
	}
	waitClients(t, h, 1)
	st := agentproto.ChatState{ID: 1, Open: true, Lines: []agentproto.ChatLine{{ID: 5, From: agentproto.ChatFromTech, Name: "Piet", Text: "Hi"}}}
	if n := h.Chat(st); n != 1 {
		t.Errorf("trays: %d", n)
	}
	if m := a.next(); m.Type != "chat" || len(m.Chat.Lines) != 1 || m.Chat.Lines[0].Text != "Hi" {
		t.Fatalf("chat: %+v", m)
	}
	// The user's answer goes to the server as a self-service request.
	a.send(trayMsg{Type: "ss", ID: "9", Op: agentproto.SelfServiceOpChat, Text: "Hello"})
	if m := a.next(); m.Type != "ss-reply" || m.ID != "9" {
		t.Fatalf("reply: %+v", m)
	}
	if got.Op != agentproto.SelfServiceOpChat || got.Text != "Hello" {
		t.Errorf("request: %+v", got)
	}
	// An ended chat is not sent to trays that connect later.
	h.Chat(agentproto.ChatState{ID: 1, Open: false})
	if m := a.next(); m.Type != "chat" || m.Chat.Open {
		t.Fatalf("end: %+v", m)
	}
	b := dialTray(t, addr)
	waitClients(t, h, 2)
	_ = b.conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if b.r.Scan() {
		t.Errorf("ended chat sent to a new tray: %s", b.r.Text())
	}
}
