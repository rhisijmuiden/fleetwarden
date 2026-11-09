package agent

import (
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/coder/websocket"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/remote"
	"github.com/rhisijmuiden/fleetwarden/internal/terminal"
)

// Terminal starts a shell for a remote terminal session; it defaults to
// terminal.Start (tests replace it).
type TerminalStarter func(terminal.Options) (terminal.Session, error)

const (
	terminalSizeWait = 2 * time.Second // for the viewer's first size before the shell starts
	terminalChunk    = 32 << 10
)

var endShellExited = remoteEnd{agentproto.RemoteStateEnded, "shell_exited"}

// runTerminal runs a terminal session: a shell as root (Linux) or SYSTEM
// (Windows). It does not ask the signed-in user: nothing of their screen is
// shown, like an SSH session.
func (a *Agent) runTerminal(ctx context.Context, st agentproto.RemoteStart, send func(string, any)) remoteEnd {
	start := a.Terminal
	if start == nil {
		start = terminal.Start
	}
	ws, ciph, fail := a.connectRemote(ctx, st, send)
	if ws == nil {
		return fail
	}
	defer ws.CloseNow() //nolint:errcheck
	end := a.relayTerminal(ctx, ws, ciph, st, start)
	// Report why before closing the socket: the server takes a closed
	// socket without a reason as a lost connection.
	send(agentproto.TypeRemoteStatus, agentproto.RemoteStatus{SessionID: st.SessionID, State: end.state, Reason: end.reason})
	ws.Close(websocket.StatusNormalClosure, end.reason) //nolint:errcheck
	return end
}

func (a *Agent) relayTerminal(ctx context.Context, ws *websocket.Conn, ciph *agentproto.RemoteCipher, st agentproto.RemoteStart, start TerminalStarter) remoteEnd {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	write := func(plain []byte) error {
		wctx, wcancel := context.WithTimeout(ctx, remoteWriteTimeout)
		defer wcancel()
		return ws.Write(wctx, websocket.MessageBinary, ciph.Seal(plain))
	}
	// One reader for the whole session: coder/websocket closes the socket
	// when a Read is cancelled, so reads never get their own timeout.
	inC := make(chan []byte, 64)
	go func() {
		defer close(inC)
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
			select {
			case inC <- m:
			case <-ctx.Done():
				return
			}
		}
	}()

	// The viewer sends its size as soon as it connects; start the shell
	// with it, so the first prompt is laid out right.
	cols, rows := st.Cols, st.Rows
	var early [][]byte
	timer := time.NewTimer(terminalSizeWait)
	for cols == 0 {
		var m []byte
		var ok bool
		select {
		case m, ok = <-inC:
		case <-timer.C:
			ok, cols = true, -1 // no size: the default
		}
		if !ok {
			timer.Stop()
			return endFromContext(ctx)
		}
		if c, r, isSize := remote.DecodeTermResize(m); isSize {
			cols, rows = c, r
		} else if m != nil {
			early = append(early, m)
		}
	}
	timer.Stop()
	cols = max(cols, 0)

	sh, err := start(terminal.Options{Cols: cols, Rows: rows, Shell: st.Shell})
	if err != nil {
		a.Log.Warn("remote terminal: start shell", "session", st.SessionID, "err", err)
		_ = write(remote.JSONMessage(remote.MsgEvent, remote.Event{Type: "error", Code: "shell_failed", Message: err.Error()}))
		if errors.Is(err, terminal.ErrUnsupported) {
			return failed("unsupported")
		}
		return failed("shell_failed")
	}
	defer sh.Close()
	stopClose := context.AfterFunc(ctx, func() { sh.Close() })
	defer stopClose()

	host, _ := os.Hostname()
	info := sh.Info()
	if err := write(remote.JSONMessage(remote.MsgTermInfo, remote.TermInfo{Hostname: host, Platform: runtime.GOOS, Shell: info.Shell, User: info.User})); err != nil {
		return remoteEnd{agentproto.RemoteStateEnded, "closed"}
	}
	input := func(m []byte) {
		switch {
		case len(m) == 0:
		case m[0] == remote.MsgTermInput:
			_, _ = sh.Write(m[1:])
		case m[0] == remote.MsgTermResize:
			if c, r, ok := remote.DecodeTermResize(m); ok {
				_ = sh.Resize(c, r)
			}
		}
	}
	for _, m := range early {
		input(m)
	}

	// viewer -> shell
	go func() {
		for m := range inC {
			input(m)
		}
	}()

	// When the shell exits, programs it left running can keep the terminal
	// open; stop reading shortly after.
	go func() {
		_, _ = sh.Wait()
		select {
		case <-time.After(time.Second):
			sh.Close()
		case <-ctx.Done():
		}
	}()

	// shell -> viewer
	buf := make([]byte, terminalChunk)
	for {
		n, err := sh.Read(buf)
		if n > 0 {
			msg := append([]byte{remote.MsgTermOutput}, buf[:n]...)
			if werr := write(msg); werr != nil {
				cancel(remoteEnd{agentproto.RemoteStateEnded, "closed"})
				break
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && ctx.Err() == nil {
				a.Log.Warn("remote terminal: read", "session", st.SessionID, "err", err)
			}
			break
		}
	}
	if ctx.Err() != nil {
		return endFromContext(ctx)
	}
	code, _ := sh.Wait()
	_ = write(remote.JSONMessage(remote.MsgTermExit, remote.TermExit{Code: code}))
	return endShellExited
}
