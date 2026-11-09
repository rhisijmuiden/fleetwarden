package terminal

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type ptySession struct {
	master *os.File
	cmd    *exec.Cmd
	info   Info

	waitOnce sync.Once
	code     int
	waitErr  error
	exited   chan struct{}
	endOnce  sync.Once
}

// account is the part of /etc/passwd the shell needs.
type account struct{ name, home, shell string }

func lookupAccount(uid int) account {
	a := account{name: "root", home: "/root", shell: ""}
	if uid != 0 {
		a = account{name: strconv.Itoa(uid), home: "/"}
	}
	f, err := os.Open("/etc/passwd")
	if err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			p := strings.Split(sc.Text(), ":")
			if len(p) >= 7 && p[2] == strconv.Itoa(uid) {
				a = account{name: p[0], home: p[5], shell: p[6]}
				break
			}
		}
	}
	if !usableShell(a.shell) {
		a.shell = ""
		for _, s := range []string{"/bin/bash", "/usr/bin/bash", "/bin/sh"} {
			if usableShell(s) {
				a.shell = s
				break
			}
		}
	}
	if st, err := os.Stat(a.home); err != nil || !st.IsDir() {
		a.home = "/"
	}
	return a
}

func usableShell(s string) bool {
	if s == "" || strings.HasSuffix(s, "/nologin") || strings.HasSuffix(s, "/false") {
		return false
	}
	st, err := os.Stat(s)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

// systemLang reads the system locale, so the shell gets the same one as a
// console login.
func systemLang() string {
	for _, p := range []string{"/etc/default/locale", "/etc/locale.conf"} {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(raw), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(l), "LANG="); ok {
				if v = strings.Trim(v, `"'`); v != "" {
					return v
				}
			}
		}
	}
	return "C.UTF-8"
}

// openPTY opens a new pseudo terminal pair. The master is non-blocking, so
// Close interrupts a Read.
func openPTY() (master, slave *os.File, err error) {
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open /dev/ptmx: %w", err)
	}
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		unix.Close(fd)
		return nil, nil, fmt.Errorf("unlock pty: %w", err)
	}
	n, err := unix.IoctlGetUint32(fd, unix.TIOCGPTN)
	if err != nil {
		unix.Close(fd)
		return nil, nil, fmt.Errorf("pty number: %w", err)
	}
	master = os.NewFile(uintptr(fd), "/dev/ptmx")
	slave, err = os.OpenFile("/dev/pts/"+strconv.FormatUint(uint64(n), 10), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}

// Start starts the login shell of the user the agent runs as (root).
func Start(o Options) (Session, error) {
	cols, rows := clampSize(o.Cols, o.Rows)
	acct := lookupAccount(os.Getuid())
	if acct.shell == "" {
		return nil, errors.New("terminal: no shell found")
	}
	master, slave, err := openPTY()
	if err != nil {
		return nil, err
	}
	s := &ptySession{master: master, exited: make(chan struct{}), info: Info{Shell: acct.shell, User: acct.name}}
	if err := s.Resize(cols, rows); err != nil {
		master.Close()
		slave.Close()
		return nil, err
	}
	cmd := &exec.Cmd{
		Path: acct.shell,
		Args: []string{"-" + filepath.Base(acct.shell)}, // a login shell
		Dir:  acct.home,
		Env: []string{
			"TERM=xterm-256color", "COLORTERM=truecolor", "HOME=" + acct.home, "USER=" + acct.name, "LOGNAME=" + acct.name,
			"SHELL=" + acct.shell, "LANG=" + systemLang(), "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		},
		Stdin: slave, Stdout: slave, Stderr: slave,
		SysProcAttr: &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0},
	}
	if err := cmd.Start(); err != nil {
		master.Close()
		slave.Close()
		return nil, err
	}
	slave.Close()
	s.cmd = cmd
	go s.wait()
	return s, nil
}

func (s *ptySession) wait() {
	err := s.cmd.Wait()
	s.code = s.cmd.ProcessState.ExitCode()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		err = nil
	}
	s.waitErr = err
	close(s.exited)
}

func (s *ptySession) Read(p []byte) (int, error) {
	n, err := s.master.Read(p)
	if err != nil && (errors.Is(err, syscall.EIO) || errors.Is(err, os.ErrClosed)) {
		return n, io.EOF // the shell and everything on the terminal exited
	}
	return n, err
}

func (s *ptySession) Write(p []byte) (int, error) { return s.master.Write(p) }

func (s *ptySession) Resize(cols, rows int) error {
	cols, rows = clampSize(cols, rows)
	rc, err := s.master.SyscallConn()
	if err != nil {
		return err
	}
	var ierr error
	err = rc.Control(func(fd uintptr) {
		ierr = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &unix.Winsize{Row: uint16(rows), Col: uint16(cols)})
	})
	if err != nil {
		return err
	}
	return ierr
}

func (s *ptySession) Wait() (int, error) {
	<-s.exited
	return s.code, s.waitErr
}

func (s *ptySession) Info() Info { return s.info }

// Close hangs up the terminal like a closed SSH connection: SIGHUP to the
// shell's process group, SIGKILL when it is still there after 3 seconds.
func (s *ptySession) Close() error {
	var err error
	s.endOnce.Do(func() { err = s.close() })
	return err
}

func (s *ptySession) close() error {
	if s.cmd != nil && s.cmd.Process != nil {
		pid := s.cmd.Process.Pid
		select {
		case <-s.exited:
		default:
			_ = syscall.Kill(-pid, syscall.SIGHUP)
			select {
			case <-s.exited:
			case <-time.After(3 * time.Second):
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		}
	}
	return s.master.Close()
}
