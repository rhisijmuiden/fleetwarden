// Package terminal runs an interactive shell on a pseudo terminal for the
// remote terminal (a PTY with the root user's login shell on Linux, a
// ConPTY with PowerShell or Command Prompt as SYSTEM on Windows).
package terminal

import (
	"errors"
	"io"
)

// Shells that can be asked for. Linux always runs the user's login shell.
const (
	ShellDefault    = ""
	ShellPowerShell = "powershell"
	ShellCmd        = "cmd"
)

// ErrUnsupported is returned where there is no pseudo terminal.
var ErrUnsupported = errors.New("terminal: not supported on this platform")

// Options configure a new session.
type Options struct {
	Cols, Rows int
	Shell      string // ShellPowerShell or ShellCmd on Windows
}

// Session is a running shell. Read returns its output and io.EOF once it
// has exited; Write types into it.
type Session interface {
	io.ReadWriter
	// Resize changes the terminal size.
	Resize(cols, rows int) error
	// Wait waits for the shell to exit and returns its exit code.
	Wait() (int, error)
	// Close ends the shell (and what it started) and frees the terminal.
	Close() error
	// Info describes the session.
	Info() Info
}

// Info describes a session for the viewer.
type Info struct {
	Shell string `json:"shell"` // path or program name
	User  string `json:"user"`  // the account the shell runs as
}

func clampSize(cols, rows int) (int, int) {
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 30
	}
	return min(max(cols, 10), 1000), min(max(rows, 2), 500)
}
