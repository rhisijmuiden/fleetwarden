//go:build windows

package agent

import (
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// TrayPipe is the named pipe the tray app (FleetWardenTray.exe) connects to.
const TrayPipe = `\\.\pipe\FleetWardenAgent.Tray`

// TrayExe is the tray app, installed next to the agent.
const TrayExe = "FleetWardenTray.exe"

// The pipe is for SYSTEM and administrators, and logged-on users may read
// and write it. 0x12019b is read/write without FILE_CREATE_PIPE_INSTANCE,
// so a user cannot set up a pipe instance of their own.
const trayPipeSDDL = "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;0x12019b;;;IU)"

// platformNotifier listens for tray apps and starts them in the sessions of
// logged-on users when there is something to ask.
func platformNotifier(ctx context.Context, log *slog.Logger) Notifier {
	l, err := listenTray(TrayPipe)
	if err != nil {
		if log != nil {
			log.Warn("tray pipe", "err", err)
		}
		return nil
	}
	h := newTrayHub(log)
	h.sessions = activeSessions
	h.session = pipeClientSession
	h.user = pipeClientUser
	if exe, err := os.Executable(); err == nil {
		tray := filepath.Join(filepath.Dir(exe), TrayExe)
		if _, err := os.Stat(tray); err == nil {
			h.launch = func(s uint32) error { return launchInSession(s, tray) }
		}
	}
	go h.Serve(ctx, l)
	return h
}

func listenTray(name string) (net.Listener, error) {
	return winio.ListenPipe(name, &winio.PipeConfig{SecurityDescriptor: trayPipeSDDL, InputBufferSize: 64 << 10, OutputBufferSize: 64 << 10})
}

// activeSessions lists the sessions with a logged-on user at the console or
// over remote desktop.
func activeSessions() []uint32 {
	var info *windows.WTS_SESSION_INFO
	var n uint32
	if err := windows.WTSEnumerateSessions(0, 0, 1, &info, &n); err != nil {
		return nil
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(info)))
	var out []uint32
	for _, s := range unsafe.Slice(info, n) {
		if s.State != windows.WTSActive || s.SessionID == 0 {
			continue
		}
		var tok windows.Token
		if windows.WTSQueryUserToken(s.SessionID, &tok) == nil {
			tok.Close()
			out = append(out, s.SessionID)
		}
	}
	return out
}

// pipeClientSession returns the session of the process on the other end.
func pipeClientSession(c net.Conn) uint32 {
	f, ok := c.(interface{ Fd() uintptr })
	if !ok {
		return 0
	}
	var pid, session uint32
	if windows.GetNamedPipeClientProcessId(windows.Handle(f.Fd()), &pid) != nil {
		return 0
	}
	if windows.ProcessIdToSessionId(pid, &session) != nil {
		return 0
	}
	return session
}

// pipeClientUser returns the account (DOMAIN\name) of the process on the
// other end, from its token: the tray cannot claim another user.
func pipeClientUser(c net.Conn) string {
	f, ok := c.(interface{ Fd() uintptr })
	if !ok {
		return ""
	}
	var pid uint32
	if windows.GetNamedPipeClientProcessId(windows.Handle(f.Fd()), &pid) != nil {
		return ""
	}
	p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(p)
	var tok windows.Token
	if windows.OpenProcessToken(p, windows.TOKEN_QUERY, &tok) != nil {
		return ""
	}
	defer tok.Close()
	u, err := tok.GetTokenUser()
	if err != nil {
		return ""
	}
	account, domain, _, err := u.User.Sid.LookupAccount("")
	if err != nil {
		return u.User.Sid.String()
	}
	if domain == "" {
		return account
	}
	return domain + `\` + account
}

// launchInSession starts exe as the user of the session, on their desktop.
func launchInSession(session uint32, exe string) error {
	var tok windows.Token
	if err := windows.WTSQueryUserToken(session, &tok); err != nil {
		return err
	}
	defer tok.Close()
	var env *uint16
	if err := windows.CreateEnvironmentBlock(&env, tok, false); err == nil {
		defer windows.DestroyEnvironmentBlock(env)
	}
	app, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	cmd, _ := windows.UTF16PtrFromString(`"` + exe + `"`)
	dir, _ := windows.UTF16PtrFromString(filepath.Dir(exe))
	desktop, _ := windows.UTF16PtrFromString(`winsta0\default`)
	si := windows.StartupInfo{Desktop: desktop}
	si.Cb = uint32(unsafe.Sizeof(si))
	var pi windows.ProcessInformation
	if err := windows.CreateProcessAsUser(tok, app, cmd, nil, nil, false, windows.CREATE_UNICODE_ENVIRONMENT, env, dir, &si, &pi); err != nil {
		return err
	}
	windows.CloseHandle(pi.Thread)
	windows.CloseHandle(pi.Process)
	return nil
}
