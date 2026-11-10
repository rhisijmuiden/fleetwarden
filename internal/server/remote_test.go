package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/webrtc/v4"

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
	mu       sync.Mutex
	allow    bool
	noAnswer bool // nobody answers (the user is away)
	asked    []agent.Prompt
	banners  map[string]func()
}

func (f *fakeConsent) Ask(_ context.Context, p agent.Prompt) (agent.Answer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, p)
	if f.noAnswer {
		return agent.Answer{}, nil
	}
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
	// The fake agent answers at once, often before a viewer could connect,
	// so the session log is checked instead of the viewer.
	admin.do("POST", "/api/v1/devices/"+id+"/remote", map[string]string{"kind": "desktop"}, &rs)
	denied := waitRemote(t, admin, rs.ID, func(r store.RemoteSession) bool { return r.EndedAt != nil })
	if denied.State != "denied" || denied.Reason != "user_denied" {
		t.Errorf("denied: %+v", denied)
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

	// The audit log has the starts and ends. The end entry is written just
	// after the session row is closed, so give the last one a moment.
	starts, ends := 0, 0
	for deadline := time.Now().Add(5 * time.Second); ; {
		entries, _ := e.st.ListAudit(context.Background(), store.AuditQuery{Search: "remote."})
		starts, ends = 0, 0
		for _, en := range entries {
			switch en.Action {
			case "remote.start":
				starts++
			case "remote.end":
				ends++
			}
		}
		if ends >= 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
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

func TestRemoteTerminalSession(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses a real Linux pseudo terminal")
	}
	e := newEnv(t)
	admin, _ := e.signedInAdmin()
	_, info := e.createToken(admin, 1)
	consent := &fakeConsent{allow: false}
	a := newTestAgent(t, e, info.Token, "SRV-01", false)
	a.Notify = consent
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
	if code := admin.do("POST", "/api/v1/devices/"+id+"/remote", map[string]string{"kind": "terminal", "shell": "bash"}, nil); code != 400 {
		t.Errorf("unknown shell: %d", code)
	}
	var rs store.RemoteSession
	if code := admin.do("POST", "/api/v1/devices/"+id+"/remote", map[string]any{"kind": "terminal", "cols": 90, "rows": 25}, &rs); code != 201 {
		t.Fatalf("start: %d", code)
	}
	// A terminal never asks the signed-in user, whatever the group says.
	if rs.Kind != "terminal" || rs.Consent != "direct" {
		t.Errorf("session: %+v", rs)
	}
	v := admin.openViewer(rs.ID)
	v.waitState("active")
	var ti remote.TermInfo
	if err := json.Unmarshal(v.waitBinary(remote.MsgTermInfo)[1:], &ti); err != nil || ti.Platform != "linux" || ti.Shell == "" || ti.User == "" {
		t.Fatalf("terminal info: %+v %v", ti, err)
	}
	waitOutput := func(want string) {
		t.Helper()
		var got strings.Builder
		for !strings.Contains(got.String(), want) {
			got.Write(v.waitBinary(remote.MsgTermOutput)[1:])
		}
	}
	v.send(append([]byte{remote.MsgTermInput}, "echo fw-$((6*7)) $(tput cols)x$(tput lines)\n"...))
	waitOutput("fw-42 90x25")
	v.send(remote.EncodeTermResize(120, 40))
	v.send(append([]byte{remote.MsgTermInput}, "echo size=$(tput cols)x$(tput lines)\n"...))
	waitOutput("size=120x40")
	v.send(append([]byte{remote.MsgTermInput}, "exit 3\n"...))
	var ex remote.TermExit
	if err := json.Unmarshal(v.waitBinary(remote.MsgTermExit)[1:], &ex); err != nil || ex.Code != 3 {
		t.Errorf("exit: %+v %v", ex, err)
	}
	done := waitRemote(t, admin, rs.ID, func(r store.RemoteSession) bool { return r.EndedAt != nil })
	if done.State != "ended" || done.Reason != "shell_exited" || done.BytesUp == 0 {
		t.Errorf("log: %+v", done)
	}
	consent.mu.Lock()
	asked := len(consent.asked)
	consent.mu.Unlock()
	if asked != 0 {
		t.Errorf("the user was asked %d times", asked)
	}

	// The operator ends a running shell.
	admin.do("POST", "/api/v1/devices/"+id+"/remote", map[string]any{"kind": "terminal"}, &rs)
	v = admin.openViewer(rs.ID)
	v.waitState("active")
	v.waitBinary(remote.MsgTermInfo)
	v.send(remote.EncodeTermResize(80, 24))
	if code := admin.do("POST", "/api/v1/remote/sessions/"+rs.ID+"/end", nil, nil); code != 204 {
		t.Fatalf("end: %d", code)
	}
	done = waitRemote(t, admin, rs.ID, func(r store.RemoteSession) bool { return r.EndedAt != nil })
	if done.Reason != "operator_ended" {
		t.Errorf("ended: %+v", done)
	}
	// The desktop is still Windows only.
	if code := admin.do("POST", "/api/v1/devices/"+id+"/remote", map[string]string{"kind": "desktop"}, nil); code != 400 {
		t.Errorf("desktop on Linux: %d", code)
	}
}

// rtcViewer is the browser's side of the direct path, with pion.
type rtcViewer struct {
	pc   *webrtc.PeerConnection
	msgs chan []byte
	dcC  chan *webrtc.DataChannel
}

func newRTCViewer(t *testing.T, offer string) (*rtcViewer, string) {
	t.Helper()
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	v := &rtcViewer{pc: pc, msgs: make(chan []byte, 256), dcC: make(chan *webrtc.DataChannel, 1)}
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		dc.OnOpen(func() { v.dcC <- dc })
		var buf []byte
		dc.OnMessage(func(m webrtc.DataChannelMessage) {
			buf = append(buf, m.Data[1:]...)
			if m.Data[0] == remote.ChunkLast {
				v.msgs <- buf
				buf = nil
			}
		})
	})
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer}); err != nil {
		t.Fatal(err)
	}
	ans, err := pc.CreateAnswer(nil)
	if err != nil {
		t.Fatal(err)
	}
	done := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(ans); err != nil {
		t.Fatal(err)
	}
	<-done
	return v, pc.LocalDescription().SDP
}

func (v *rtcViewer) next(t *testing.T, typ byte) []byte {
	t.Helper()
	timeout := time.After(15 * time.Second)
	for {
		select {
		case m := <-v.msgs:
			if len(m) > 0 && m[0] == typ {
				return m
			}
		case <-timeout:
			t.Fatalf("direct path: no message %x", typ)
		}
	}
}

func TestRemoteDirectPath(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()
	_, info := e.createToken(admin, 1)
	desk := &fakeDesktop{p: remote.NewFakePlatform()}
	a := newTestAgent(t, e, info.Token, "PC-02", false)
	a.Remote = desk
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
	setPlatform(t, id, "windows")
	admin.do("PUT", "/api/v1/groups/1", groupReq{Name: "Default", UpdateRing: 1, RemoteConsent: "direct"}, nil)

	// Settings: on by default; STUN servers are checked.
	var rset remoteSettingsView
	if admin.do("GET", "/api/v1/settings/remote", nil, &rset); !rset.Direct || len(rset.STUNServers) != 0 {
		t.Errorf("default settings: %+v", rset)
	}
	if code := admin.do("PUT", "/api/v1/settings/remote", remoteSettingsView{Direct: true, STUNServers: []string{"http://x"}}, nil); code != 400 {
		t.Errorf("bad STUN server: %d", code)
	}

	var rs startRemoteResp
	if code := admin.do("POST", "/api/v1/devices/"+id+"/remote", map[string]string{"kind": "desktop"}, &rs); code != 201 || rs.RTC == nil {
		t.Fatalf("start: %d %+v", code, rs)
	}
	v := admin.openViewer(rs.ID)
	v.waitState("active")
	seq, _, _ := remote.DecodeFrame(v.waitBinary(remote.MsgFrame))
	v.send(remote.EncodeAck(seq))
	v.send(remote.JSONMessage(remote.MsgRTCAns, remote.RTCSignal{Type: "request"}))
	var offer remote.RTCSignal
	if err := json.Unmarshal(v.waitBinary(remote.MsgRTC)[1:], &offer); err != nil || offer.Type != "offer" || !strings.Contains(offer.SDP, "a=fingerprint") {
		t.Fatalf("offer: %+v %v", offer, err)
	}
	rv, answer := newRTCViewer(t, offer.SDP)
	v.send(remote.JSONMessage(remote.MsgRTCAns, remote.RTCSignal{Type: "answer", SDP: answer}))

	var ev remote.Event
	if err := json.Unmarshal(rv.next(t, remote.MsgEvent)[1:], &ev); err != nil || ev.Type != "path" || ev.Code != remote.PathDirect {
		t.Fatalf("path event: %+v %v", ev, err)
	}
	// The refresh after the switch sends the whole screen on the direct path.
	seq, tiles, err := remote.DecodeFrame(rv.next(t, remote.MsgFrame))
	if err != nil || len(tiles) == 0 {
		t.Fatalf("frame on the direct path: %v", err)
	}
	// Acknowledgements and input go over the direct path too.
	dc := <-rv.dcC
	dc.Send(append([]byte{remote.ChunkLast}, remote.EncodeAck(seq)...))
	dc.Send(append([]byte{remote.ChunkLast}, remote.EncodeKey(remote.KeyInput{Down: true, Scan: 0x1e})...))
	for range 100 {
		if _, keys, _, _ := desk.p.Snapshot(); len(keys) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, keys, _, _ := desk.p.Snapshot(); len(keys) != 1 || keys[0].Scan != 0x1e {
		t.Errorf("keys over the direct path: %+v", keys)
	}
	// When the direct path goes away the screen comes over the relay again.
	rv.pc.Close()
	for {
		st, m := v.next()
		if st.State != "" && st.State != "active" {
			t.Fatalf("session %s (%s)", st.State, st.Reason)
		}
		if len(m) > 0 && m[0] == remote.MsgEvent {
			var ev remote.Event
			if json.Unmarshal(m[1:], &ev) == nil && ev.Type == "path" && ev.Code == remote.PathRelay {
				break
			}
		}
	}
	v.waitBinary(remote.MsgFrame)
	if done := waitRemote(t, admin, rs.ID, func(r store.RemoteSession) bool { return r.State == "active" }); done.EndedAt != nil {
		t.Fatal("session ended")
	}

	// Direct path off: the agent answers "failed" and the relay keeps working.
	admin.do("POST", "/api/v1/remote/sessions/"+rs.ID+"/end", nil, nil)
	if code := admin.do("PUT", "/api/v1/settings/remote", remoteSettingsView{Direct: false, STUNServers: []string{"stun:stun.example.org:3478"}}, &rset); code != 200 || rset.Direct {
		t.Fatalf("settings: %d %+v", code, rset)
	}
	rs = startRemoteResp{}
	admin.do("POST", "/api/v1/devices/"+id+"/remote", map[string]string{"kind": "desktop"}, &rs)
	if rs.RTC != nil {
		t.Errorf("rtc offered while off: %+v", rs.RTC)
	}
	v = admin.openViewer(rs.ID)
	v.waitState("active")
	v.send(remote.JSONMessage(remote.MsgRTCAns, remote.RTCSignal{Type: "request"}))
	var sig remote.RTCSignal
	if err := json.Unmarshal(v.waitBinary(remote.MsgRTC)[1:], &sig); err != nil || sig.Type != "failed" {
		t.Errorf("signal while off: %+v %v", sig, err)
	}
}
