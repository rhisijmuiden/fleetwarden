//go:build !windows

package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// unixConfig applies configurations on Linux: browser policies as JSON files
// in the folders the browsers read, scripts as root.
type unixConfig struct {
	log *slog.Logger
	// Root is prepended to every system path (tests).
	Root string
	// ScriptDir holds scripts while they run.
	ScriptDir string
}

func newPlatformConfigBackend(log *slog.Logger) ConfigBackend {
	return &unixConfig{log: log, ScriptDir: filepath.Join(defaultStateDir(), "scripts")}
}

// NewUnixConfigBackend returns the Linux backend below root (tests).
func NewUnixConfigBackend(root, scriptDir string) ConfigBackend {
	return &unixConfig{Root: root, ScriptDir: scriptDir}
}

func (u *unixConfig) p(path string) string { return filepath.Join(u.Root, path) }

func (u *unixConfig) exists(paths ...string) bool {
	for _, p := range paths {
		if _, err := os.Stat(u.p(p)); err == nil {
			return true
		}
	}
	return false
}

// The file FleetWarden writes for Chrome, Chromium and Edge. They read every
// JSON file in their "managed" folder.
const chromiumPolicyFile = "fleetwarden.json"

func (u *unixConfig) BrowserStore(b string) (PolicyStore, error) {
	switch b {
	case agentproto.BrowserChrome:
		paths := []string{u.p("/etc/opt/chrome/policies/managed/" + chromiumPolicyFile)}
		if u.exists("/etc/chromium", "/usr/bin/chromium", "/usr/bin/chromium-browser", "/snap/bin/chromium") {
			paths = append(paths, u.p("/etc/chromium/policies/managed/"+chromiumPolicyFile))
		}
		return newJSONPolicyStore(false, paths...)
	case agentproto.BrowserEdge:
		return newJSONPolicyStore(false, u.p("/etc/opt/edge/policies/managed/"+chromiumPolicyFile))
	case agentproto.BrowserFirefox:
		// Firefox, Firefox ESR and the snap all read this file.
		return newJSONPolicyStore(true, u.p("/etc/firefox/policies/policies.json"))
	}
	return nil, nil
}

func (u *unixConfig) BrowserInstalled(b string) bool {
	switch b {
	case agentproto.BrowserChrome:
		return u.exists("/opt/google/chrome/chrome", "/usr/bin/google-chrome", "/usr/bin/chromium", "/usr/bin/chromium-browser", "/snap/bin/chromium")
	case agentproto.BrowserEdge:
		return u.exists("/opt/microsoft/msedge/msedge", "/usr/bin/microsoft-edge")
	case agentproto.BrowserFirefox:
		return u.exists("/usr/bin/firefox", "/usr/bin/firefox-esr", "/usr/lib/firefox/firefox", "/usr/lib/firefox-esr/firefox-esr", "/snap/bin/firefox")
	}
	return false
}

func (u *unixConfig) RegistryStore(string, string) (PolicyStore, error) { return nil, nil }

// Network drives and printers are Windows only for now.
func (u *unixConfig) DriveStore(ConfigUser) (PolicyStore, error) { return nil, nil }
func (u *unixConfig) SystemStore(string) (PolicyStore, error)    { return nil, nil }

func (u *unixConfig) PrinterStore(*ConfigUser) (PolicyStore, error) { return nil, nil }

// Users is empty: scripts as the user and user settings are Windows only.
func (u *unixConfig) Users() []ConfigUser { return nil }

func (u *unixConfig) RunScript(ctx context.Context, s agentproto.ScriptConfig, user *ConfigUser) agentproto.ScriptRun {
	run := agentproto.ScriptRun{At: time.Now().UTC()}
	if user != nil {
		run.Error = "scripts as the user are only supported on Windows"
		return run
	}
	shell := "/bin/sh"
	if s.Shell == agentproto.ShellBash {
		shell = "/bin/bash"
	}
	if err := os.MkdirAll(u.ScriptDir, 0o700); err != nil {
		run.Error = err.Error()
		return run
	}
	var rnd [8]byte
	_, _ = rand.Read(rnd[:])
	file := filepath.Join(u.ScriptDir, "config-"+hex.EncodeToString(rnd[:])+".sh")
	if err := os.WriteFile(file, []byte(s.Content), 0o700); err != nil {
		run.Error = err.Error()
		return run
	}
	defer os.Remove(file)
	start := time.Now()
	cmd := exec.CommandContext(ctx, shell, file)
	cmd.Dir = "/"
	cmd.Env = append(os.Environ(), "FLEETWARDEN_CONFIG=1")
	cmd.WaitDelay = 10 * time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	run.DurationMs = time.Since(start).Milliseconds()
	run.Output = out.String()
	var ee *exec.ExitError
	switch {
	case ctx.Err() != nil:
		run.Error = "the script did not finish in time"
		run.ExitCode = -1
	case errors.As(err, &ee):
		run.ExitCode = ee.ExitCode()
	case err != nil:
		run.Error = err.Error()
		run.ExitCode = -1
	}
	return run
}
