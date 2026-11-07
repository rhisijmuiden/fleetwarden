//go:build windows

package agent

// Real installs on Windows (CI runner): packages from the live winget
// catalog are downloaded, installed and removed again. Set
// FW_TEST_SOFTWARE=1 to run them; they change the machine.

import (
	"context"
	"os"
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
