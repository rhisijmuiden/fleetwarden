package terminal

import (
	"bytes"
	"errors"
	"io"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// collector reads a session's output in the background.
type collector struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	done chan struct{}
}

func collect(s Session) *collector {
	c := &collector{done: make(chan struct{})}
	go func() {
		defer close(c.done)
		b := make([]byte, 4096)
		for {
			n, err := s.Read(b)
			c.mu.Lock()
			c.buf.Write(b[:n])
			c.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return c
}

func (c *collector) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		got := c.buf.String()
		c.mu.Unlock()
		if strings.Contains(got, want) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	t.Fatalf("output never contained %q; got:\n%s", want, c.buf.String())
}

func commands() (echo, size, exit string) {
	if runtime.GOOS == "windows" {
		return "Write-Output ('fw-' + (40+2))\r", "Write-Output ('size-' + $Host.UI.RawUI.WindowSize.Width + 'x' + $Host.UI.RawUI.WindowSize.Height)\r", "exit 7\r"
	}
	return "echo fw-$((40+2))\n", "echo size-$(tput cols)x$(tput lines)\n", "exit 7\n"
}

func TestShell(t *testing.T) {
	s, err := Start(Options{Cols: 100, Rows: 30})
	if errors.Is(err, ErrUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Info().Shell == "" || s.Info().User == "" {
		t.Errorf("info = %+v", s.Info())
	}
	t.Logf("shell %+v", s.Info())
	out := collect(s)
	echo, size, exit := commands()
	if _, err := io.WriteString(s, echo); err != nil {
		t.Fatal(err)
	}
	out.waitFor(t, "fw-42")

	if err := s.Resize(132, 40); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := io.WriteString(s, size); err != nil {
		t.Fatal(err)
	}
	out.waitFor(t, "size-132x40")

	if _, err := io.WriteString(s, exit); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var code int
	go func() {
		code, err = s.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("shell did not exit")
	}
	if err != nil || code != 7 {
		t.Fatalf("exit = %d, %v", code, err)
	}
	s.Close()
	select {
	case <-out.done:
	case <-time.After(10 * time.Second):
		t.Fatal("output did not end after the shell exited")
	}
}

func TestCloseEndsRunningShell(t *testing.T) {
	s, err := Start(Options{})
	if errors.Is(err, ErrUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	out := collect(s)
	echo, _, _ := commands()
	io.WriteString(s, echo)
	out.waitFor(t, "fw-42")
	done := make(chan struct{})
	go func() { s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Close hung")
	}
	select {
	case <-out.done:
	case <-time.After(10 * time.Second):
		t.Fatal("output did not end after Close")
	}
}

func TestCommandPrompt(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows only")
	}
	s, err := Start(Options{Shell: ShellCmd})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Info().Shell != "cmd.exe" {
		t.Errorf("shell = %q", s.Info().Shell)
	}
	out := collect(s)
	io.WriteString(s, "set /a 40+2 & echo  fw-cmd\r")
	out.waitFor(t, "fw-cmd")
}
