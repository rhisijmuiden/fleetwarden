//go:build windows

package agent

// Software on Windows: installers run as SYSTEM without UI (msiexec for MSI
// packages, the installer's own silent switches for the others); programs
// are found and removed through the Uninstall registry keys.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows/registry"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

type winSoftware struct{}

func newPlatformSoftwareBackend() SoftwareBackend { return winSoftware{} }

func msiexecPath() string {
	return filepath.Join(os.Getenv("SystemRoot"), "System32", "msiexec.exe")
}

// Hooks for tests.
var (
	installBusyWait  = time.Minute
	uninstallGoneFor = 3 * time.Minute
)

// arpEntries reads the Uninstall registry keys of the machine and of the
// loaded user profiles.
func arpEntries() []arpEntry {
	var out []arpEntry
	const uninstall = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`
	read := func(root registry.Key, path string, access uint32, perUser bool) {
		k, err := registry.OpenKey(root, path, registry.ENUMERATE_SUB_KEYS|access)
		if err != nil {
			return
		}
		defer k.Close()
		names, _ := k.ReadSubKeyNames(-1)
		for _, n := range names {
			sk, err := registry.OpenKey(k, n, registry.QUERY_VALUE|access)
			if err != nil {
				continue
			}
			e := arpEntry{Key: n, DisplayName: regString(sk, "DisplayName"), DisplayVersion: regString(sk, "DisplayVersion"),
				Publisher: regString(sk, "Publisher"), UninstallString: regString(sk, "UninstallString"),
				QuietUninstallString: regString(sk, "QuietUninstallString"), PerUser: perUser}
			if wi, _, err := sk.GetIntegerValue("WindowsInstaller"); err == nil && wi == 1 {
				e.WindowsInstaller = true
			}
			sk.Close()
			if e.DisplayName != "" {
				out = append(out, e)
			}
		}
	}
	read(registry.LOCAL_MACHINE, uninstall, registry.WOW64_64KEY, false)
	read(registry.LOCAL_MACHINE, uninstall, registry.WOW64_32KEY, false)
	if users, err := registry.OpenKey(registry.USERS, "", registry.ENUMERATE_SUB_KEYS); err == nil {
		sids, _ := users.ReadSubKeyNames(-1)
		users.Close()
		for _, sid := range sids {
			if strings.HasPrefix(sid, "S-1-5-21-") && !strings.HasSuffix(sid, "_Classes") {
				read(registry.USERS, sid+`\`+uninstall, 0, true)
			}
		}
	}
	return out
}

func (winSoftware) Find(_ context.Context, d agentproto.Detect) (bool, string, error) {
	ok, v := findInstalled(arpEntries(), d)
	return ok, v, nil
}

func (winSoftware) Install(ctx context.Context, in agentproto.Installer, file string) (RunResult, error) {
	logFile := filepath.Join(filepath.Dir(file), "install.log")
	exe, cl, err := installCommand(in, file, logFile, msiexecPath())
	if err != nil {
		return RunResult{}, err
	}
	var res RunResult
	for attempt := 0; ; attempt++ {
		res, err = runWindowsCommand(ctx, exe, cl, logFile)
		if err != nil || res.ExitCode != ExitInstallBusy || attempt >= 4 {
			break
		}
		if !sleep(ctx, installBusyWait) { // another installation runs: wait for it
			break
		}
	}
	if err != nil {
		return res, err
	}
	ok, reboot, msg := classifyExit(res.ExitCode, in)
	res.Reboot = reboot
	if !ok {
		return res, errors.New(msg)
	}
	return res, nil
}

func (winSoftware) Uninstall(ctx context.Context, u agentproto.Uninstall) (RunResult, bool, error) {
	targets := uninstallTargets(arpEntries(), u)
	if len(targets) == 0 {
		return RunResult{}, false, nil
	}
	var res RunResult
	for _, e := range targets {
		exe, cl, err := uninstallCommand(e, u.Args, msiexecPath(), fileExists, installerFamily)
		if err != nil {
			return res, true, err
		}
		logFile := ""
		if strings.EqualFold(exe, msiexecPath()) {
			dir, _ := os.MkdirTemp("", "fw-uninstall-")
			if dir != "" {
				defer os.RemoveAll(dir)
				logFile = filepath.Join(dir, "uninstall.log")
				cl += " /l*v " + quoteArg(logFile)
			}
		}
		res, err = runWindowsCommand(ctx, exe, cl, logFile)
		if err != nil {
			return res, true, err
		}
		ok, reboot, msg := classifyExit(res.ExitCode, agentproto.Installer{})
		res.Reboot = res.Reboot || reboot
		if !ok && res.ExitCode != 1605 { // 1605: already gone
			return res, true, errors.New(msg)
		}
	}
	// Some uninstallers (NSIS) hand over to a copy of themselves and return
	// at once: wait until the program is gone.
	deadline := time.Now().Add(uninstallGoneFor)
	for {
		if len(uninstallTargets(arpEntries(), u)) == 0 {
			return res, true, nil
		}
		if time.Now().After(deadline) {
			if res.Reboot {
				return res, true, nil // removed at the next restart
			}
			return res, true, errors.New("the uninstaller finished, but the program is still installed")
		}
		if !sleep(ctx, 3*time.Second) {
			return res, true, ctx.Err()
		}
	}
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// installerFamily recognises Inno Setup and NSIS uninstallers by the
// markers their programs carry.
func installerFamily(exe string) string {
	f, err := os.Open(exe)
	if err != nil {
		return familyUnknown
	}
	defer f.Close()
	buf := make([]byte, 16<<20)
	n, _ := f.Read(buf)
	b := buf[:n]
	switch {
	case bytes.Contains(b, []byte("Inno Setup")):
		return familyInno
	case bytes.Contains(b, []byte("Nullsoft")), bytes.Contains(b, []byte("NullsoftInst")):
		return familyNullsoft
	}
	return familyUnknown
}

// runWindowsCommand runs a command line exactly as given, without a window,
// and returns its exit code with the end of its output or log file.
func runWindowsCommand(ctx context.Context, exe, cmdline, logFile string) (RunResult, error) {
	cmd := exec.CommandContext(ctx, exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: cmdline, HideWindow: true}
	cmd.Dir = filepath.Dir(exe)
	cmd.WaitDelay = 30 * time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	res := RunResult{Output: out.String()}
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		res.Ran, res.ExitCode = true, ee.ExitCode()
	case err != nil:
		if ctx.Err() != nil {
			return res, fmt.Errorf("the installer did not finish in time")
		}
		return res, err
	default:
		res.Ran = true
	}
	if ctx.Err() != nil {
		return res, fmt.Errorf("the installer did not finish in time")
	}
	if logFile != "" {
		if raw, err := os.ReadFile(logFile); err == nil {
			res.Output = strings.TrimSpace(res.Output + "\n" + tailString(decodeLog(raw), softwareOutputMax))
		}
	}
	return res, nil
}
