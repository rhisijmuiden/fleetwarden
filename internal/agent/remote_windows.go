//go:build windows

package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/rhisijmuiden/fleetwarden/internal/remote"
)

// The desktop helper is the agent executable started as
// "fleetwarden-agent remote-helper --pipe NAME". The service (session 0,
// where no screen exists) starts it as SYSTEM in the console session, so it
// can follow the input desktop to the sign-in screen and UAC prompts. It
// connects back over a named pipe that only its own account may open.

type winRemote struct{ log *slog.Logger }

func platformRemote(log *slog.Logger) RemoteBackend { return &winRemote{log: log} }

var (
	sasDLL      = windows.NewLazySystemDLL("sas.dll")
	procSendSAS = sasDLL.NewProc("SendSAS")
)

// SecureAttention sends Ctrl+Alt+Del. Windows only accepts SendSAS from a
// service when the SoftwareSASGeneration policy allows it, so the agent
// switches that on for services (1) when it is off.
func (w *winRemote) SecureAttention() error {
	if err := procSendSAS.Find(); err != nil {
		return err
	}
	if err := allowSoftwareSAS(); err != nil {
		return fmt.Errorf("allow software Ctrl+Alt+Del: %w", err)
	}
	procSendSAS.Call(0) // FALSE: the caller is a service
	return nil
}

func allowSoftwareSAS() error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("SoftwareSASGeneration")
	if err == nil && (v == 1 || v == 3) {
		return nil
	}
	nv := uint32(1)
	if err == nil && v == 2 { // ease of access apps: keep them, add services
		nv = 3
	}
	return k.SetDWordValue("SoftwareSASGeneration", nv)
}

type helperConn struct {
	*remote.StreamConn
	pipe net.Conn
	proc windows.Handle
}

func (h *helperConn) Close() error {
	err := h.pipe.Close()
	if h.proc != 0 {
		// The helper exits when its pipe closes; make sure it does.
		if ev, _ := windows.WaitForSingleObject(h.proc, 3000); ev != windows.WAIT_OBJECT_0 {
			_ = windows.TerminateProcess(h.proc, 1)
		}
		windows.CloseHandle(h.proc)
		h.proc = 0
	}
	return err
}

func currentUserSID() (string, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String(), nil
}

func ownSession() uint32 {
	var s uint32
	_ = windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &s)
	return s
}

// OpenDesktop starts a helper and waits for it to connect.
func (w *winRemote) OpenDesktop(ctx context.Context) (DesktopConn, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	sid, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	var rnd [12]byte
	_, _ = rand.Read(rnd[:])
	name := `\\.\pipe\FleetWardenAgent.Remote.` + hex.EncodeToString(rnd[:])
	l, err := winio.ListenPipe(name, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;" + sid + ")", InputBufferSize: 1 << 20, OutputBufferSize: 1 << 20})
	if err != nil {
		return nil, err
	}
	defer l.Close()

	var proc windows.Handle
	var pid uint32
	if ownSession() == 0 {
		proc, pid, err = startInConsole(exe, name)
	} else {
		// Not a service (tests, a technician running it by hand): the
		// helper runs in this session.
		cmd := exec.Command(exe, "remote-helper", "--pipe", name)
		if err = cmd.Start(); err == nil {
			pid = uint32(cmd.Process.Pid)
			proc, err = windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, pid)
			_ = cmd.Process.Release()
		}
	}
	if err != nil {
		return nil, err
	}
	type accepted struct {
		c   net.Conn
		err error
	}
	ch := make(chan accepted, 1)
	go func() {
		c, err := l.Accept()
		ch <- accepted{c, err}
	}()
	t := time.NewTimer(20 * time.Second)
	defer t.Stop()
	var conn net.Conn
	select {
	case a := <-ch:
		conn, err = a.c, a.err
	case <-t.C:
		err = errors.New("the desktop helper did not connect")
	case <-ctx.Done():
		err = ctx.Err()
	}
	if err == nil {
		if got := pipeClientPID(conn); got != pid {
			conn.Close()
			err = fmt.Errorf("unexpected process %d on the helper pipe", got)
		}
	}
	if err != nil {
		_ = windows.TerminateProcess(proc, 1)
		windows.CloseHandle(proc)
		return nil, err
	}
	return &helperConn{StreamConn: remote.NewStreamConn(conn), pipe: conn, proc: proc}, nil
}

func pipeClientPID(c net.Conn) uint32 {
	f, ok := c.(interface{ Fd() uintptr })
	if !ok {
		return 0
	}
	var pid uint32
	_ = windows.GetNamedPipeClientProcessId(windows.Handle(f.Fd()), &pid)
	return pid
}

// startInConsole starts the helper as SYSTEM (a copy of the service's
// token) in the session attached to the physical console.
func startInConsole(exe, pipe string) (windows.Handle, uint32, error) {
	session := windows.WTSGetActiveConsoleSessionId()
	if session == 0xFFFFFFFF {
		return 0, 0, errors.New("no console session (it is switching)")
	}
	var own windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ALL_ACCESS, &own); err != nil {
		return 0, 0, err
	}
	defer own.Close()
	var tok windows.Token
	if err := windows.DuplicateTokenEx(own, windows.MAXIMUM_ALLOWED, nil, windows.SecurityImpersonation, windows.TokenPrimary, &tok); err != nil {
		return 0, 0, err
	}
	defer tok.Close()
	if err := windows.SetTokenInformation(tok, windows.TokenSessionId, (*byte)(unsafe.Pointer(&session)), uint32(unsafe.Sizeof(session))); err != nil {
		return 0, 0, fmt.Errorf("set session: %w", err)
	}
	app, _ := windows.UTF16PtrFromString(exe)
	cmd, _ := windows.UTF16PtrFromString(fmt.Sprintf(`"%s" remote-helper --pipe %s`, exe, pipe))
	dir, _ := windows.UTF16PtrFromString(filepath.Dir(exe))
	desktop, _ := windows.UTF16PtrFromString(`winsta0\default`)
	si := windows.StartupInfo{Desktop: desktop}
	si.Cb = uint32(unsafe.Sizeof(si))
	var pi windows.ProcessInformation
	const createNoWindow = 0x08000000
	if err := windows.CreateProcessAsUser(tok, app, cmd, nil, nil, false, createNoWindow, nil, dir, &si, &pi); err != nil {
		return 0, 0, err
	}
	windows.CloseHandle(pi.Thread)
	return pi.Process, pi.ProcessId, nil
}

// RunRemoteHelper is the desktop helper: it connects to the agent's pipe and
// runs the desktop session until the pipe closes.
func RunRemoteHelper(pipe string) error {
	remote.SetDPIAware()
	timeout := 10 * time.Second
	conn, err := winio.DialPipe(pipe, &timeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	d := &remote.Desktop{P: remote.NewWindowsPlatform(), Features: []string{"cad", "clipboard", "type", "monitors"}}
	err = d.Run(context.Background(), remote.NewStreamConn(conn))
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, windows.ERROR_BROKEN_PIPE) || errors.Is(err, windows.ERROR_PIPE_NOT_CONNECTED) {
		return nil
	}
	return err
}
