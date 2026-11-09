package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/remote"
	"github.com/rhisijmuiden/fleetwarden/internal/version"
)

// RemoteBackend opens remote desktop sessions on this machine.
type RemoteBackend interface {
	// OpenDesktop starts the desktop helper in the console session and
	// returns the connection to it.
	OpenDesktop(ctx context.Context) (DesktopConn, error)
	// SecureAttention sends Ctrl+Alt+Del.
	SecureAttention() error
}

// DesktopConn is the connection to a desktop helper.
type DesktopConn interface {
	remote.Conn
	Close() error
}

// RemoteNotifier asks the logged-on user before a remote session and shows a
// banner while one runs. The tray hub implements it on Windows.
type RemoteNotifier interface {
	Notifier
	RemoteActive(id string, p Prompt, stop func())
	RemoteEnded(id string)
}

// remoteState tracks the running sessions so remote.stop can end them.
type remoteState struct {
	mu       sync.Mutex
	sessions map[string]context.CancelCauseFunc
}

func (r *remoteState) add(id string, cancel context.CancelCauseFunc) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sessions == nil {
		r.sessions = map[string]context.CancelCauseFunc{}
	}
	if _, dup := r.sessions[id]; dup || len(r.sessions) >= remoteMaxSessions {
		return false
	}
	r.sessions[id] = cancel
	return true
}

func (r *remoteState) remove(id string) {
	r.mu.Lock()
	delete(r.sessions, id)
	r.mu.Unlock()
}

func (r *remoteState) stop(id string, cause error) {
	r.mu.Lock()
	c := r.sessions[id]
	r.mu.Unlock()
	if c != nil {
		c(cause)
	}
}

// remoteEnd is a reason a session ended, reported to the server.
type remoteEnd struct{ state, reason string }

func (e remoteEnd) Error() string { return e.state + ": " + e.reason }

var (
	endByUser     = remoteEnd{agentproto.RemoteStateEnded, "user_ended"}
	endByOperator = remoteEnd{agentproto.RemoteStateEnded, "operator_ended"}
)

const (
	remoteConsentSeconds = 30
	remoteDialTimeout    = 30 * time.Second
	remoteWriteTimeout   = 30 * time.Second
	remoteReadLimit      = 4 << 20
	remoteHelperRetries  = 20
	remoteMaxSessions    = 6 // desktops and terminals together
)

func (a *Agent) handleRemoteStart(e *agentproto.Envelope, send func(string, any)) {
	var st agentproto.RemoteStart
	if err := e.Decode(&st); err != nil || st.SessionID == "" || len(st.ServerKey) != 32 {
		return
	}
	ctx, cancel := context.WithCancelCause(a.runCtx())
	if !a.remote.add(st.SessionID, cancel) {
		cancel(nil)
		send(agentproto.TypeRemoteStatus, agentproto.RemoteStatus{SessionID: st.SessionID, State: agentproto.RemoteStateFailed, Reason: "busy"})
		return
	}
	go func() {
		defer a.remote.remove(st.SessionID)
		defer cancel(nil)
		end := a.runRemote(ctx, st, send)
		status := agentproto.RemoteStatus{SessionID: st.SessionID, State: end.state, Reason: end.reason}
		a.Log.Info("remote session ended", "session", st.SessionID, "operator", st.Operator, "state", end.state, "reason", end.reason)
		send(agentproto.TypeRemoteStatus, status)
	}()
}

func (a *Agent) handleRemoteStop(e *agentproto.Envelope) {
	var st agentproto.RemoteStop
	if e.Decode(&st) == nil {
		a.remote.stop(st.SessionID, endByOperator)
	}
}

func failed(reason string) remoteEnd { return remoteEnd{agentproto.RemoteStateFailed, reason} }

// runRemote runs one session to its end.
func (a *Agent) runRemote(ctx context.Context, st agentproto.RemoteStart, send func(string, any)) remoteEnd {
	if st.Kind == agentproto.RemoteTerminal {
		return a.runTerminal(ctx, st, send)
	}
	if st.Kind != agentproto.RemoteDesktop {
		return failed("unsupported")
	}
	backend := a.Remote
	if backend == nil {
		return failed("unsupported")
	}
	notifier, _ := a.Notify.(RemoteNotifier)
	prompt := Prompt{ID: "remote-" + st.SessionID, Kind: PromptRemote, Operator: st.Operator, ProductName: st.ProductName,
		AccentColor: st.AccentColor, CountdownSeconds: max(st.ConsentSeconds, 10)}
	if st.ConsentSeconds == 0 {
		prompt.CountdownSeconds = remoteConsentSeconds
	}
	banner := false
	switch st.Consent {
	case agentproto.ConsentAsk:
		if notifier == nil {
			return remoteEnd{agentproto.RemoteStateDenied, "consent_unavailable"}
		}
		send(agentproto.TypeRemoteStatus, agentproto.RemoteStatus{SessionID: st.SessionID, State: agentproto.RemoteStateConsent})
		ans, err := notifier.Ask(ctx, prompt)
		switch {
		case errors.Is(err, ErrNoTray):
			return remoteEnd{agentproto.RemoteStateDenied, "consent_unavailable"}
		case errors.Is(err, ErrNoUser):
			// Nobody is signed in: there is nobody to ask (a server, the
			// sign-in screen).
		case err != nil:
			return endFromContext(ctx)
		case !ans.Answered:
			return remoteEnd{agentproto.RemoteStateDenied, "no_answer"}
		case !ans.Allow:
			return remoteEnd{agentproto.RemoteStateDenied, "user_denied"}
		default:
			banner = true
		}
	case agentproto.ConsentNotify:
		banner = notifier != nil
	}
	if ctx.Err() != nil {
		return endFromContext(ctx)
	}
	ws, ciph, fail := a.connectRemote(ctx, st, send)
	if ws == nil {
		return fail
	}
	defer ws.CloseNow() //nolint:errcheck

	if banner {
		notifier.RemoteActive(st.SessionID, prompt, func() {
			a.remote.stop(st.SessionID, endByUser)
		})
		defer notifier.RemoteEnded(st.SessionID)
	}
	a.Log.Info("remote session started", "session", st.SessionID, "operator", st.Operator, "consent", st.Consent)
	end := a.relayDesktop(ctx, ws, ciph, backend)
	send(agentproto.TypeRemoteStatus, agentproto.RemoteStatus{SessionID: st.SessionID, State: end.state, Reason: end.reason})
	ws.Close(websocket.StatusNormalClosure, end.reason) //nolint:errcheck
	return end
}

// connectRemote reports "connecting" and opens the encrypted session
// socket. On failure ws is nil and the remoteEnd says why.
func (a *Agent) connectRemote(ctx context.Context, st agentproto.RemoteStart, send func(string, any)) (*websocket.Conn, *agentproto.RemoteCipher, remoteEnd) {
	send(agentproto.TypeRemoteStatus, agentproto.RemoteStatus{SessionID: st.SessionID, State: agentproto.RemoteStateConnecting})
	key, err := agentproto.NewRemoteKey()
	if err != nil {
		return nil, nil, failed("internal")
	}
	ciph, err := key.Cipher(st.ServerKey, st.SessionID, true)
	if err != nil {
		return nil, nil, failed("bad_key")
	}
	ws, err := a.dialRemote(ctx, st.SessionID, key)
	if err != nil {
		a.Log.Warn("remote session: connect", "session", st.SessionID, "err", err)
		if ctx.Err() != nil {
			return nil, nil, endFromContext(ctx)
		}
		return nil, nil, failed("connect_failed")
	}
	ws.SetReadLimit(remoteReadLimit)
	return ws, ciph, remoteEnd{}
}

func endFromContext(ctx context.Context) remoteEnd {
	var e remoteEnd
	if errors.As(context.Cause(ctx), &e) {
		return e
	}
	return remoteEnd{agentproto.RemoteStateEnded, "agent_stopped"}
}

func (a *Agent) dialRemote(ctx context.Context, sessionID string, key *agentproto.RemoteKey) (*websocket.Conn, error) {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	if !cfg.Enrolled() {
		return nil, errors.New("not enrolled")
	}
	path := agentproto.PathRemote + sessionID + "/" + agentproto.EncodeRemoteKey(key.Public())
	dctx, cancel := context.WithTimeout(ctx, remoteDialTimeout)
	defer cancel()
	ws, res, err := websocket.Dial(dctx, wsURL(cfg.Server)+path, &websocket.DialOptions{
		HTTPClient: a.HTTP,
		HTTPHeader: http.Header{
			"User-Agent":          {"fleetwarden-agent/" + version.Version},
			agentproto.HeaderAuth: {agentproto.SignRequest(cfg.Key(), cfg.DeviceID, http.MethodGet, path, time.Now())},
		},
		CompressionMode: websocket.CompressionDisabled,
	})
	if res != nil && res.Body != nil {
		res.Body.Close()
	}
	return ws, err
}

// relayDesktop passes messages between the session socket and the desktop
// helper. When the helper goes away (the user signs out, the console session
// changes) a new one is started in the current console session.
func (a *Agent) relayDesktop(ctx context.Context, ws *websocket.Conn, ciph *agentproto.RemoteCipher, backend RemoteBackend) remoteEnd {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var mu sync.Mutex
	var helper DesktopConn
	setHelper := func(h DesktopConn) {
		mu.Lock()
		helper = h
		mu.Unlock()
	}
	getHelper := func() DesktopConn {
		mu.Lock()
		defer mu.Unlock()
		return helper
	}
	write := func(plain []byte) error {
		wctx, wcancel := context.WithTimeout(ctx, remoteWriteTimeout)
		defer wcancel()
		return ws.Write(wctx, websocket.MessageBinary, ciph.Seal(plain))
	}
	notice := func(code string) {
		_ = write(remote.JSONMessage(remote.MsgEvent, remote.Event{Type: "notice", Code: code}))
	}

	// viewer -> helper
	go func() {
		for {
			typ, raw, err := ws.Read(ctx)
			if err != nil {
				cancel(remoteEnd{agentproto.RemoteStateEnded, "closed"})
				return
			}
			if typ != websocket.MessageBinary {
				continue
			}
			m, err := ciph.Open(raw)
			if err != nil {
				cancel(failed("bad_message"))
				return
			}
			if ctl, ok := remote.DecodeControl(m); ok && ctl.Op == "cad" {
				if err := backend.SecureAttention(); err != nil {
					a.Log.Warn("remote session: Ctrl+Alt+Del", "err", err)
					notice("cad_failed")
				}
				continue
			}
			if h := getHelper(); h != nil {
				_ = h.WriteMsg(m) // a dead helper is replaced below
			}
		}
	}()

	// helper -> viewer, restarting the helper when it ends
	for attempt := 0; ctx.Err() == nil; {
		h, err := backend.OpenDesktop(ctx)
		if err != nil {
			attempt++
			if ctx.Err() != nil {
				break
			}
			a.Log.Warn("remote session: desktop helper", "err", err, "attempt", attempt)
			if attempt == 1 {
				notice("helper_restarting")
			}
			if attempt >= remoteHelperRetries {
				return failed("helper_failed")
			}
			if !sleep(ctx, time.Second) {
				break
			}
			continue
		}
		setHelper(h)
		stopClose := context.AfterFunc(ctx, func() { h.Close() })
		started := time.Now()
		for {
			m, err := h.ReadMsg()
			if err != nil {
				break
			}
			if err := write(m); err != nil {
				cancel(remoteEnd{agentproto.RemoteStateEnded, "closed"})
				break
			}
		}
		stopClose()
		h.Close()
		setHelper(nil)
		if time.Since(started) > 10*time.Second {
			attempt = 0
		}
		attempt++
		if ctx.Err() == nil {
			notice("helper_restarting")
			if attempt >= remoteHelperRetries || !sleep(ctx, time.Second) {
				break
			}
		}
	}
	var e remoteEnd
	if errors.As(context.Cause(ctx), &e) {
		return e
	}
	if ctx.Err() == nil {
		return failed("helper_failed")
	}
	return endFromContext(ctx)
}

// String implements fmt.Stringer for logs.
func (e remoteEnd) String() string { return fmt.Sprintf("%s (%s)", e.state, e.reason) }

// RemoteSnapshot opens the desktop helper like a remote session does and
// saves the first frame as a PNG ("fleetwarden-agent remote-test").
func RemoteSnapshot(ctx context.Context, log *slog.Logger, pngPath string) (remote.Info, error) {
	backend := platformRemote(log)
	if backend == nil {
		return remote.Info{}, remote.ErrUnsupported
	}
	h, err := backend.OpenDesktop(ctx)
	if err != nil {
		return remote.Info{}, err
	}
	defer h.Close()
	stop := context.AfterFunc(ctx, func() { h.Close() })
	defer stop()
	var info remote.Info
	var img *image.RGBA
	for {
		m, err := h.ReadMsg()
		if err != nil {
			return info, err
		}
		switch m[0] {
		case remote.MsgInfo:
			if err := json.Unmarshal(m[1:], &info); err != nil {
				return info, err
			}
			img = image.NewRGBA(image.Rect(0, 0, info.Width, info.Height))
		case remote.MsgFrame:
			seq, tiles, err := remote.DecodeFrame(m)
			if err != nil || img == nil {
				return info, errors.New("bad frame")
			}
			for _, t := range tiles {
				ti, err := jpeg.Decode(bytes.NewReader(t.Data))
				if err != nil {
					return info, err
				}
				draw.Draw(img, image.Rect(t.X, t.Y, t.X+t.W, t.Y+t.H), ti, image.Point{}, draw.Src)
			}
			_ = h.WriteMsg(remote.EncodeAck(seq))
			f, err := os.Create(pngPath)
			if err != nil {
				return info, err
			}
			defer f.Close()
			return info, png.Encode(f, img)
		}
	}
}
