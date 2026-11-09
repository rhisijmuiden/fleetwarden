package agent

import (
	"context"
	"net"
	"path/filepath"
	"sync"
	"testing"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// startUserHub is startHub with a fixed user for every tray.
func startUserHub(t *testing.T, user string) (*trayHub, string) {
	t.Helper()
	addr := filepath.Join(t.TempDir(), "tray.sock")
	l, err := net.Listen("unix", addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h := newTrayHub(nil)
	h.user = func(net.Conn) string { return user }
	go h.Serve(ctx, l)
	return h, addr
}

func TestTrayHubSelfService(t *testing.T) {
	h, addr := startUserHub(t, `CORP\jan`)
	var mu sync.Mutex
	var got []agentproto.SelfServiceRequest
	fn := func(_ context.Context, req agentproto.SelfServiceRequest) agentproto.SelfServiceReply {
		mu.Lock()
		got = append(got, req)
		mu.Unlock()
		return agentproto.SelfServiceReply{RequestID: "server-side", Message: "ok", Catalog: &agentproto.SelfServiceCatalog{ProductName: "FW", Help: true}}
	}
	h.SetSelfService(fn)
	tray := dialTray(t, addr)
	tray.send(trayMsg{Type: "ss", ID: "ss1", Op: agentproto.SelfServiceOpInstall, ItemID: 7, Reason: "for work"})
	m := tray.next()
	if m.Type != "ss-reply" || m.ID != "ss1" || m.Reply == nil || m.Reply.Message != "ok" || m.Reply.Catalog == nil || m.Reply.RequestID != "" {
		t.Fatalf("reply: %+v", m)
	}
	mu.Lock()
	req := got[0]
	mu.Unlock()
	// The agent names the user and the language comes from the tray's hello.
	if req.User != `CORP\jan` || req.Lang != "nl-NL" || req.Op != "install" || req.ItemID != 7 || req.Reason != "for work" {
		t.Errorf("request: %+v", req)
	}

	// Without a known user nothing reaches the server.
	h2, addr2 := startUserHub(t, "")
	h2.SetSelfService(fn)
	tray2 := dialTray(t, addr2)
	tray2.send(trayMsg{Type: "ss", ID: "ss2", Op: agentproto.SelfServiceOpCatalog})
	if m := tray2.next(); m.Type != "ss-reply" || m.Reply == nil || m.Reply.Error != agentproto.SelfServiceErrNoUser {
		t.Errorf("no user: %+v", m)
	}
	mu.Lock()
	n := len(got)
	mu.Unlock()
	if n != 1 {
		t.Errorf("forwarded %d requests", n)
	}
}

func TestSelfServiceOffline(t *testing.T) {
	a := &Agent{}
	if r := a.SelfService(context.Background(), agentproto.SelfServiceRequest{Op: "catalog"}); r.Error != agentproto.SelfServiceErrOffline {
		t.Errorf("offline: %+v", r)
	}
	gen := a.attachSelfService(func(string, any) bool { return true }, false)
	if r := a.SelfService(context.Background(), agentproto.SelfServiceRequest{Op: "catalog"}); r.Error != agentproto.SelfServiceErrNoService {
		t.Errorf("old server: %+v", r)
	}
	a.detachSelfService(gen)

	// A session that ends while a request waits fails it.
	sent := make(chan struct{})
	gen = a.attachSelfService(func(string, any) bool { close(sent); return true }, true)
	done := make(chan agentproto.SelfServiceReply)
	go func() { done <- a.SelfService(context.Background(), agentproto.SelfServiceRequest{Op: "catalog"}) }()
	<-sent
	a.detachSelfService(gen)
	if r := <-done; r.Error != agentproto.SelfServiceErrOffline {
		t.Errorf("lost session: %+v", r)
	}
}
