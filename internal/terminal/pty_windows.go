package terminal

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

type conPTY struct {
	hpc     windows.Handle
	process windows.Handle
	in      *os.File // our end of the shell's input
	out     *os.File // our end of the shell's output
	info    Info

	closeOnce sync.Once // the pseudo console
	endOnce   sync.Once // everything
	exited    chan struct{}
	code      int
	waitErr   error
}

func shellCommand(shell string) (path, cmdline, name string) {
	sys := os.Getenv("SystemRoot")
	if sys == "" {
		sys = `C:\Windows`
	}
	if shell == ShellCmd {
		p := filepath.Join(sys, "System32", "cmd.exe")
		return p, `"` + p + `"`, "cmd.exe"
	}
	p := filepath.Join(sys, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	return p, `"` + p + `" -NoLogo -NoExit -ExecutionPolicy Bypass`, "powershell.exe"
}

// Start starts PowerShell (or Command Prompt) on a pseudo console, as the
// account the agent service runs as (SYSTEM).
func Start(o Options) (Session, error) {
	cols, rows := clampSize(o.Cols, o.Rows)
	path, cmdline, name := shellCommand(o.Shell)
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("terminal: %s not found", name)
	}
	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		return nil, err
	}
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		windows.CloseHandle(inR)
		windows.CloseHandle(inW)
		return nil, err
	}
	var hpc windows.Handle
	err := windows.CreatePseudoConsole(windows.Coord{X: int16(cols), Y: int16(rows)}, inR, outW, 0, &hpc)
	// The pseudo console keeps its own copies of these ends.
	windows.CloseHandle(inR)
	windows.CloseHandle(outW)
	if err != nil {
		windows.CloseHandle(inW)
		windows.CloseHandle(outR)
		return nil, fmt.Errorf("terminal: CreatePseudoConsole: %w", err)
	}
	t := &conPTY{hpc: hpc, in: os.NewFile(uintptr(inW), "conpty-in"), out: os.NewFile(uintptr(outR), "conpty-out"), exited: make(chan struct{})}
	fail := func(err error) (Session, error) {
		windows.ClosePseudoConsole(hpc)
		t.in.Close()
		t.out.Close()
		return nil, err
	}

	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return fail(err)
	}
	defer attrs.Delete()
	// The attribute's value is the HPCON itself, not a pointer to it.
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&hpc)), unsafe.Sizeof(hpc)); err != nil {
		return fail(err)
	}
	si := windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(si))
	// No inherited standard handles: the shell must use the pseudo console.
	si.Flags = windows.STARTF_USESTDHANDLES

	env, err := envBlock()
	if err != nil {
		return fail(err)
	}
	dir := os.Getenv("SystemDrive") + `\`
	if dir == `\` {
		dir = `C:\`
	}
	cl, _ := windows.UTF16PtrFromString(cmdline)
	dp, _ := windows.UTF16PtrFromString(dir)
	var pi windows.ProcessInformation
	if err := windows.CreateProcess(nil, cl, nil, nil, false, windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT,
		&env[0], dp, &si.StartupInfo, &pi); err != nil {
		return fail(fmt.Errorf("terminal: start %s: %w", name, err))
	}
	windows.CloseHandle(pi.Thread)
	t.process = pi.Process
	t.info = Info{Shell: name, User: currentUser()}
	go t.wait()
	return t, nil
}

func envBlock() ([]uint16, error) {
	var b []uint16
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "=") {
			continue
		}
		u, err := windows.UTF16FromString(kv)
		if err != nil {
			return nil, err
		}
		b = append(b, u...)
	}
	return append(b, 0), nil
}

func currentUser() string {
	tok := windows.GetCurrentProcessToken()
	u, err := tok.GetTokenUser()
	if err != nil {
		return ""
	}
	acct, dom, _, err := u.User.Sid.LookupAccount("")
	if err != nil {
		return u.User.Sid.String()
	}
	if dom != "" {
		return dom + `\` + acct
	}
	return acct
}

func (t *conPTY) wait() {
	_, err := windows.WaitForSingleObject(t.process, windows.INFINITE)
	var code uint32
	if err == nil {
		err = windows.GetExitCodeProcess(t.process, &code)
	}
	t.code, t.waitErr = int(code), err
	close(t.exited)
	// The output pipe only ends when the pseudo console closes.
	go t.closeConsole()
}

func (t *conPTY) closeConsole() {
	t.closeOnce.Do(func() {
		// ClosePseudoConsole can wait for the output to be read, so the
		// reader keeps going until it gets EOF.
		windows.ClosePseudoConsole(t.hpc)
	})
}

func (t *conPTY) Read(p []byte) (int, error) {
	n, err := t.out.Read(p)
	if err != nil && (errors.Is(err, windows.ERROR_BROKEN_PIPE) || errors.Is(err, os.ErrClosed)) {
		return n, io.EOF
	}
	return n, err
}

func (t *conPTY) Write(p []byte) (int, error) { return t.in.Write(p) }

func (t *conPTY) Resize(cols, rows int) error {
	cols, rows = clampSize(cols, rows)
	return windows.ResizePseudoConsole(t.hpc, windows.Coord{X: int16(cols), Y: int16(rows)})
}

func (t *conPTY) Wait() (int, error) {
	<-t.exited
	return t.code, t.waitErr
}

func (t *conPTY) Info() Info { return t.info }

// Close ends the shell and the programs on its console.
func (t *conPTY) Close() error {
	var err error
	t.endOnce.Do(func() { err = t.close() })
	return err
}

func (t *conPTY) close() error {
	select {
	case <-t.exited:
	default:
		_ = windows.TerminateProcess(t.process, 1)
		<-t.exited
	}
	// Closing our end of the output first means ClosePseudoConsole never
	// waits for output nobody reads any more.
	err := t.out.Close()
	t.closeConsole()
	t.in.Close()
	windows.CloseHandle(t.process)
	return err
}
