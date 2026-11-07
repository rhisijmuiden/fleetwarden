//go:build windows

package agent

// Real installs on Windows (CI runner): packages from the live winget
// catalog are downloaded, installed and removed again. Set
// FW_TEST_SOFTWARE=1 to run them; they change the machine.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/catalog"
)

func liveChoice(t *testing.T, w *catalog.Winget, id string, pick func(catalog.Installer) bool) (agentproto.Installer, agentproto.Detect) {
	t.Helper()
	ctx := context.Background()
	m, err := w.Manifest(ctx, id, "")
	if err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	c, err := m.Resolve("amd64")
	if err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	if pick != nil {
		for _, in := range m.Installers {
			if pick(in) {
				pc := in.ProductCode
				c.Installer = agentproto.Installer{Kind: agentproto.KindMSI, URL: in.InstallerURL, SHA256: strings.ToLower(in.InstallerSha256), Args: "ALLUSERS=1"}
				c.Detect.ProductCode = pc
			}
		}
	}
	return c.Installer, c.Detect
}

func installAndRemove(t *testing.T, a *Agent, in agentproto.Installer, det agentproto.Detect, uninstallArgs string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	be := winSoftware{}
	dir := t.TempDir()
	file := filepath.Join(dir, installerFileName(in))
	if err := a.downloadPublic(ctx, in, file); err != nil {
		t.Fatalf("download %s: %v", in.URL, err)
	}
	res, err := be.Install(ctx, in, file)
	t.Logf("install %s: exit %d reboot %v err %v\n%s", in.URL, res.ExitCode, res.Reboot, err, tailString(res.Output, 1500))
	if err != nil {
		t.Fatal(err)
	}
	ok, v, _ := be.Find(ctx, det)
	if !ok {
		t.Fatalf("%+v not found after the install", det)
	}
	t.Logf("installed %s %s", det.Name, v)
	var name string
	for _, e := range matchEntries(arpEntries(), det) {
		name = e.DisplayName
		t.Logf("entry: %+v", e)
		break
	}
	res, found, err := be.Uninstall(ctx, agentproto.Uninstall{Name: name, Args: uninstallArgs})
	t.Logf("uninstall %q: exit %d found %v err %v\n%s", name, res.ExitCode, found, err, tailString(res.Output, 1500))
	if err != nil || !found {
		t.Fatalf("uninstall: %v %v", found, err)
	}
	if ok, _, _ := be.Find(ctx, agentproto.Detect{Name: name}); ok {
		t.Errorf("%s is still installed", name)
	}
	// A second uninstall finds nothing.
	if _, found, _ := be.Uninstall(ctx, agentproto.Uninstall{Name: name}); found {
		t.Error("found after the uninstall")
	}
}

func TestSoftwareWindowsReal(t *testing.T) {
	if os.Getenv("FW_TEST_SOFTWARE") == "" {
		t.Skip("FW_TEST_SOFTWARE not set")
	}
	w := &catalog.Winget{Dir: t.TempDir()}
	defer w.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := w.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	a := &Agent{}
	a.init()

	t.Run("NSIS installer, detected uninstaller", func(t *testing.T) {
		in, det := liveChoice(t, w, "Notepad++.Notepad++", nil)
		if in.Kind != agentproto.KindNullsoft {
			t.Fatalf("kind %s", in.Kind)
		}
		installAndRemove(t, a, in, det, "")
	})
	t.Run("MSI with msiexec /x", func(t *testing.T) {
		in, det := liveChoice(t, w, "7zip.7zip", func(in catalog.Installer) bool {
			return in.Architecture == "x64" && strings.EqualFold(in.InstallerType, "wix")
		})
		if !strings.HasSuffix(in.URL, ".msi") || det.ProductCode == "" {
			t.Fatalf("no MSI: %+v %+v", in, det)
		}
		installAndRemove(t, a, in, det, "")
	})
	t.Run("EXE with given uninstall arguments", func(t *testing.T) {
		in, det := liveChoice(t, w, "7zip.7zip", nil)
		if in.Kind != agentproto.KindEXE || in.Args != "/S" {
			t.Fatalf("7-Zip exe: %+v", in)
		}
		det.ProductCode = ""
		installAndRemove(t, a, in, det, "/S")
	})
}

// TestBrowserUpdateWindowsReal installs an older Firefox, starts it, and
// lets a browser job close it and bring it to the newest version.
func TestBrowserUpdateWindowsReal(t *testing.T) {
	if os.Getenv("FW_TEST_SOFTWARE") == "" {
		t.Skip("FW_TEST_SOFTWARE not set")
	}
	w := &catalog.Winget{Dir: t.TempDir()}
	defer w.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if err := w.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	const id = "Mozilla.Firefox"
	vs, err := w.Versions(ctx, id)
	if err != nil || len(vs) < 4 {
		t.Fatalf("versions: %v %d", err, len(vs))
	}
	resolve := func(version string) (*catalog.Manifest, catalog.Choice) {
		m, err := w.Manifest(ctx, id, version)
		if err != nil {
			t.Fatal(err)
		}
		c, err := m.Resolve("amd64")
		if err != nil {
			t.Fatal(err)
		}
		return m, c
	}
	_, oldC := resolve(vs[3].Version)
	newM, newC := resolve("")
	det := agentproto.Detect{Name: "Mozilla Firefox (", MachineOnly: true}

	a := &Agent{StateDir: t.TempDir()}
	a.init()
	be := winSoftware{}
	// The runner image comes with the newest Firefox: remove it first.
	for _, e := range matchEntries(arpEntries(), det) {
		res, _, err := be.Uninstall(ctx, agentproto.Uninstall{Name: e.DisplayName})
		t.Logf("removed %s: exit %d err %v", e.DisplayName, res.ExitCode, err)
	}
	logProcesses(t, "after the removal")
	file := filepath.Join(t.TempDir(), installerFileName(oldC.Installer))
	if err := a.downloadPublic(ctx, oldC.Installer, file); err != nil {
		t.Fatal(err)
	}
	ictx, icancel := context.WithTimeout(ctx, 8*time.Minute)
	res, err := installBrowser(ictx, be, winProcesses{}, agentproto.BrowserUpdate{Firefox: true, Installer: oldC.Installer}, file)
	icancel()
	if err != nil {
		logProcesses(t, "after the failed install")
		t.Fatalf("install %s (%s %s): %v\n%s", vs[3].Version, oldC.Installer.URL, oldC.Installer.Args, err, tailString(res.Output, 1500))
	}
	ok, before, _ := be.Find(ctx, det)
	if !ok {
		t.Fatal("old Firefox not found")
	}
	t.Logf("installed Firefox %s, newest %s", before, newM.PackageVersion)
	defer func() {
		for _, e := range matchEntries(arpEntries(), det) {
			_, _, _ = be.Uninstall(context.Background(), agentproto.Uninstall{Name: e.DisplayName})
		}
	}()

	// A running Firefox (headless: the runner has no desktop).
	exe := `C:\Program Files\Mozilla Firefox\firefox.exe`
	cmd := exec.Command(exe, "-headless", "--profile", t.TempDir(), "about:blank")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	procs := winProcesses{}
	for i := 0; i < 20 && len(procs.Running([]string{"firefox.exe"})) == 0; i++ {
		time.Sleep(500 * time.Millisecond)
	}
	if len(procs.Running([]string{"firefox.exe"})) == 0 {
		t.Fatal("Firefox does not run")
	}

	job := agentproto.SoftwareJob{JobID: 99, Action: agentproto.ActionBrowsers, Name: "Browser updates", TimeoutMinutes: 30,
		Browsers: []agentproto.BrowserUpdate{{Key: "firefox", Name: "Mozilla Firefox", Version: newM.PackageVersion,
			Installer: newC.Installer, Detect: det, Process: "firefox.exe", Firefox: true}}}
	if msg := checkSoftwareJob(job); msg != "" {
		t.Fatal(msg)
	}
	var last agentproto.SoftwareStatus
	a.patch.sendFn = func(typ string, v any) bool {
		if st, ok := v.(agentproto.SoftwareStatus); ok && typ == agentproto.TypeSoftwareStatus {
			last = st
			t.Logf("status %s: %s", st.State, st.Message)
		}
		return true
	}
	a.runSoftwareJob(ctx, job)
	if last.State != agentproto.SoftwareCompleted {
		t.Fatalf("job: %+v\n%s", last, tailString(last.Output, 1500))
	}
	_, after, _ := be.Find(ctx, det)
	if catalog.CompareVersions(after, newM.PackageVersion) < 0 || catalog.CompareVersions(after, before) <= 0 {
		t.Errorf("Firefox %s after the update, want %s", after, newM.PackageVersion)
	}
	t.Logf("result: %s (version %s)", last.Message, after)
}

// logProcesses lists the running processes with their command lines, to see
// what an installer waits for.
func logProcesses(t *testing.T, when string) {
	t.Helper()
	out, _ := exec.Command("powershell", "-NoProfile", "-Command",
		"Get-CimInstance Win32_Process | Where-Object { $_.Name -match 'firefox|setup|helper|maintenance|browser|crash' } | Format-Table ProcessId,ParentProcessId,Name,CommandLine -AutoSize -Wrap | Out-String -Width 300").CombinedOutput()
	t.Logf("processes %s:\n%s", when, out)
}
