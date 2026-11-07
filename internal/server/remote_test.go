package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/rhisijmuiden/fleetwarden/internal/agent"
	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/remote"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

// fakeDesktop runs the real desktop session over an in-memory pipe.
type fakeDesktop struct {
	p    *remote.FakePlatform
	mu   sync.Mutex
	sas  int
	open int
}

type pipeConn struct {
	*remote.StreamConn
	c net.Conn
}

func (p pipeConn) Close() error { return p.c.Close() }

func (f *fakeDesktop) OpenDesktop(ctx context.Context) (agent.DesktopConn, error) {
	a, b := net.Pipe()
	f.mu.Lock()
	f.open++
	f.mu.Unlock()
	go func() {
		d := &remote.Desktop{P: f.p, Features: []string{"cad"}}
		_ = d.Run(ctx, remote.NewStreamConn(b))
		b.Close()
	}()
	return pipeConn{remote.NewStreamConn(a), a}, nil
}

func (f *fakeDesktop) SecureAttention() error {
	f.mu.Lock()
	f.sas++
	f.mu.Unlock()
	return nil
}

// fakeConsent answers remote prompts.
type fakeConsent struct {
	mu      sync.Mutex
	allow   bool
	asked   []agent.Prompt
	banners map[string]func()
}

func (f *fakeConsent) Ask(_ context.Context, p agent.Prompt) (agent.Answer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, p)
	return agent.Answer{Answered: true, Allow: f.allow}, nil
}

func (f *fakeConsent) RemoteActive(id string, _ agent.Prompt, stop func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.banners == nil {
		f.banners = map[string]func(){}
	}
	f.banners[id] = stop
}

func (f *fakeConsent) RemoteEnded(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.banners, id)
}

func (f *fakeConsent) banner(id string) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.banners[id]
}

type viewerConn struct {
	t  *testing.T
	ws *websocket.Conn
}

func (c *client) openViewer(sid string) viewerConn {
	c.e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(c.e.ts.URL, "http") + "/api/v1/remote/sessions/" + sid + "/ws"
	ws, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: c.c})
	if err != nil {
		c.e.t.Fatalf("viewer: %v", err)
	}
	ws.SetReadLimit(64 << 20)
	return viewerConn{c.e.t, ws}
}

// next returns the next message; status messages come back decoded.
func (v viewerConn) next() (remoteStatusMsg, []byte) {
	v.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	typ, data, err := v.ws.Read(ctx)
	if err != nil {
		v.t.Fatalf("viewer read: %v", err)
	}
	if typ == websocket.MessageText {
		var st remoteStatusMsg
		json.Unmarshal(data, &st)
		return st, nil
	}
	return remoteStatusMsg{}, data
}

func (v viewerConn) waitState(state string) remoteStatusMsg {
	v.t.Helper()
	for {
		st, _ := v.next()
		if st.State == state {
			return st
		}
		if st.State == "ended" || st.State == "failed" || st.State == "denied" {
			v.t.Fatalf("waiting for %s: session %s (%s)", state, st.State, st.Reason)
		}
	}
}

func (v viewerConn) waitBinary(typ byte) []byte {
	v.t.Helper()
	for {
		st, m := v.next()
		if st.State == "ended" || st.State == "failed" || st.State == "denied" {
			v.t.Fatalf("waiting for message %x: session %s (%s)", typ, st.State, st.Reason)
		}
		if len(m) > 0 && m[0] == typ {
			return m
		}
	}
}

func (v viewerConn) send(m []byte) {
	v.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := v.ws.Write(ctx, websocket.MessageBinary, m); err != nil {
		v.t.Fatal(err)
	}
}

func waitRemote(t *testing.T, c *client, sid string, done func(store.RemoteSession) bool) store.RemoteSession {
	t.Helper()
	var rs store.RemoteSession
	for range 100 {
		c.do("GET", "/api/v1/remote/sessions/"+sid, nil, &rs)
		if done(rs) {
			return rs
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("session %s: %+v", sid, rs)
	return rs
}

func TestRemoteDesktopSession(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()
	_, info := e.createToken(admin, 1)
	desk := &fakeDesktop{p: remote.NewFakePlatform()}
	consent := &fakeConsent{allow: true}
	a := newTestAgent(t, e, info.Token, "PC-01", false)
	a.Remote, a.Notify = desk, consent
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	go a.Run(ctx)

	var list deviceList
	for range 100 {
		if admin.do("GET", "/api/v1/devices?status=online", nil, &list); list.Total == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if list.Total != 1 {
		t.Fatal("agent did not come online")
	}
	id := list.Items[0].ID
	if code := admin.do("POST", "/api/v1/devices/"+id+"/remote", map[string]string{"kind": "desktop"}, nil); code != 400 {
		t.Errorf("remote control of a Linux device: %d", code)
	}
	setPlatform(t, id, "windows")

	// A workstation in a group on "auto": the user is asked.
	var rs store.RemoteSession
	if code := admin.do("POST", "/api/v1/devices/"+id+"/remote", map[string]string{"kind": "desktop"}, &rs); code != 201 || rs.Consent != "ask" {
		t.Fatalf("start: %d %+v", code, rs)
	}
	v := admin.openViewer(rs.ID)
	v.waitState("active")
	var inf remote.Info
	if err := json.Unmarshal(v.waitBinary(remote.MsgInfo)[1:], &inf); err != nil || inf.Width != 320 || len(inf.Monitors) != 2 {
		t.Fatalf("info: %+v %v", inf, err)
	}
	seq, tiles, err := remote.DecodeFrame(v.waitBinary(remote.MsgFrame))
	if err != nil || len(tiles) == 0 {
		t.Fatalf("frame: %v", err)
	}
	v.send(remote.EncodeAck(seq))
	v.send(remote.EncodeKey(remote.KeyInput{Down: true, Scan: 0x1c}))
	v.send(remote.JSONMessage(remote.MsgControl, remote.Control{Op: "cad"}))
	for range 100 {
		_, keys, _, _ := desk.p.Snapshot()
		desk.mu.Lock()
		sas := desk.sas
		desk.mu.Unlock()
		if len(keys) == 1 && sas == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, keys, _, _ := desk.p.Snapshot(); len(keys) != 1 || keys[0].Scan != 0x1c {
		t.Errorf("keys at the agent: %+v", keys)
	}
	if desk.sas != 1 {
		t.Errorf("Ctrl+Alt+Del calls: %d", desk.sas)
	}
	if consent.asked[0].Operator != "admin" && consent.asked[0].Operator != "Administrator" {
		t.Errorf("prompt: %+v", consent.asked[0])
	}
	var open remoteList
	if admin.do("GET", "/api/v1/remote/sessions?open=true", nil, &open); open.Total != 1 || open.Items[0].State != "active" || open.Items[0].ConnectedAt == nil {
		t.Errorf("open sessions: %+v", open)
	}
	// Only the operator who started it can watch.
	if code := admin.do("GET", "/api/v1/remote/sessions/"+rs.ID+"/ws", nil, nil); code != 409 {
		t.Errorf("second viewer: %d", code)
	}
	// The user ends it from the banner.
	stop := consent.banner(rs.ID)
	if stop == nil {
		t.Fatal("no banner shown to the user")
	}
	stop()
	st := v.waitState("ended")
	if st.Reason != "user_ended" {
		t.Errorf("ended: %+v", st)
	}
	done := waitRemote(t, admin, rs.ID, func(r store.RemoteSession) bool { return r.EndedAt != nil })
	if done.State != "ended" || done.Reason != "user_ended" || done.BytesDown == 0 {
		t.Errorf("log: %+v", done)
	}

	// The user says no.
	consent.mu.Lock()
	consent.allow = false
	consent.mu.Unlock()
	admin.do("POST", "/api/v1/devices/"+id+"/remote", map[string]string{"kind": "desktop"}, &rs)
	v = admin.openViewer(rs.ID)
	for {
		st, _ := v.next()
		if st.State == "denied" {
			if st.Reason != "user_denied" {
				t.Errorf("denied: %+v", st)
			}
			break
		}
		if st.State == "active" || st.State == "ended" {
			t.Fatalf("denied session went %s", st.State)
		}
	}

	// A group set to direct: nobody is asked, and the operator closes the viewer.
	var g store.DeviceGroup
	if code := admin.do("PUT", "/api/v1/groups/1", groupReq{Name: "Default", UpdateRing: 1, RemoteConsent: "direct"}, &g); code != 200 || g.RemoteConsent != "direct" {
		t.Fatalf("group: %d %+v", code, g)
	}
	if code := admin.do("PUT", "/api/v1/groups/1", groupReq{Name: "Default", UpdateRing: 1, RemoteConsent: "sometimes"}, nil); code != 400 {
		t.Errorf("invalid consent: %d", code)
	}
	asked := len(consent.asked)
	admin.do("POST", "/api/v1/devices/"+id+"/remote", map[string]string{"kind": "desktop"}, &rs)
	if rs.Consent != "direct" {
		t.Errorf("consent: %s", rs.Consent)
	}
	v = admin.openViewer(rs.ID)
	v.waitState("active")
	v.waitBinary(remote.MsgFrame)
	v.ws.Close(websocket.StatusNormalClosure, "")
	done = waitRemote(t, admin, rs.ID, func(r store.RemoteSession) bool { return r.EndedAt != nil })
	if done.Reason != "viewer_closed" || len(consent.asked) != asked || consent.banner(rs.ID) != nil {
		t.Errorf("direct session: %+v, asked %d", done, len(consent.asked))
	}

	// The audit log has the starts and ends.
	entries, _ := e.st.ListAudit(context.Background(), store.AuditQuery{Search: "remote."})
	starts, ends := 0, 0
	for _, en := range entries {
		switch en.Action {
		case "remote.start":
			starts++
		case "remote.end":
			ends++
		}
	}
	if starts != 3 || ends != 3 {
		t.Errorf("audit: %d starts, %d ends", starts, ends)
	}
	var hist remoteList
	if admin.do("GET", "/api/v1/remote/sessions?device="+id, nil, &hist); hist.Total != 3 {
		t.Errorf("history: %+v", hist)
	}
}

func TestRemoteAgentLegNeedsSignature(t *testing.T) {
	e := newEnv(t)
	key, _ := agentproto.NewRemoteKey()
	url := e.ts.URL + agentproto.PathRemote + newUUID() + "/" + agentproto.EncodeRemoteKey(key.Public())
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Errorf("unknown session: %d", res.StatusCode)
	}
	if consentFor("auto", "server") != "direct" || consentFor("auto", "workstation") != "ask" || consentFor("notify", "server") != "notify" {
		t.Error("consentFor")
	}
}
