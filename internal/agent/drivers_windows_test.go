//go:build windows

package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// TestDriverWindowsScan asks Windows Update for driver updates. A CI
// machine usually has none; the scan itself must work.
func TestDriverWindowsScan(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	list, err := windowsDrivers{}.Scan(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	t.Logf("%d driver update(s) on offer", len(list))
	for _, d := range list {
		t.Logf("%s | %s | %s | %s | hw %s | device %q %s | files %d", d.SourceID, d.Kind, d.Title, d.Version, d.HardwareID, d.Device,
			d.CurrentVersion, len(d.Files))
		if d.SourceID == "" || len(d.Files) == 0 {
			t.Errorf("incomplete: %+v", d)
		}
		for _, f := range d.Files {
			if !strings.Contains(strings.ToLower(f.URL), "windowsupdate.com") && !strings.Contains(strings.ToLower(f.URL), "microsoft.com") {
				t.Errorf("unexpected download host: %s", f.URL)
			}
		}
	}
}

// TestDriverWindowsInstall packs a driver that Windows already has into a
// cabinet (as Windows Update ships them) and installs it the way a
// repository update is installed: unpack, pnputil /add-driver /install.
func TestDriverWindowsInstall(t *testing.T) {
	src := smallDriverPackage(t)
	t.Logf("driver package: %s", src)
	work := t.TempDir()
	cab := makeCab(t, src, filepath.Join(work, "00"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	reboot, note, err := windowsDrivers{}.Install(ctx, agentproto.DriverInstall{ID: "driver:1", Kind: agentproto.DriverKindDriver, Title: "test"},
		[]string{cab})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	t.Logf("installed: reboot %v, %s", reboot, note)

	// A cabinet without a driver is refused.
	other := filepath.Join(work, "readme")
	os.MkdirAll(other, 0o700)                                                    //nolint:errcheck
	os.WriteFile(filepath.Join(other, "readme.txt"), []byte("no driver"), 0o600) //nolint:errcheck
	bad := makeCab(t, other, filepath.Join(work, "01"))
	if _, _, err := (windowsDrivers{}).Install(ctx, agentproto.DriverInstall{ID: "driver:2"}, []string{bad}); err == nil ||
		!strings.Contains(err.Error(), ".inf") {
		t.Errorf("cabinet without a driver: %v", err)
	}
}

// TestDriverWindowsConfigure turns Windows' own driver search off and back.
func TestDriverWindowsConfigure(t *testing.T) {
	read := func() (uint64, bool) {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, searchOrderKey, registry.QUERY_VALUE)
		if err != nil {
			return 0, false
		}
		defer k.Close()
		v, _, err := k.GetIntegerValue(searchOrderValue)
		return v, err == nil
	}
	before, had := read()
	b := windowsDrivers{}
	if err := b.Configure(true); err != nil {
		t.Fatalf("manage: %v", err)
	}
	if v, ok := read(); !ok || v != 0 {
		t.Errorf("SearchOrderConfig while managed: %d %v", v, ok)
	}
	if err := b.Configure(true); err != nil { // twice keeps the saved value
		t.Fatal(err)
	}
	if err := b.Configure(false); err != nil {
		t.Fatalf("release: %v", err)
	}
	if v, ok := read(); ok != had || v != before {
		t.Errorf("SearchOrderConfig after release: %d %v, want %d %v", v, ok, before, had)
	}
}

// smallDriverPackage finds a small flat driver package in the driver store.
func smallDriverPackage(t *testing.T) string {
	t.Helper()
	root := filepath.Join(os.Getenv("SystemRoot"), `System32\DriverStore\FileRepository`)
	dirs, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	type cand struct {
		dir  string
		size int64
	}
	var list []cand
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		dir := filepath.Join(root, d.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		var size int64
		infs, cats, flat := 0, 0, true
		for _, f := range files {
			if f.IsDir() {
				flat = false
				break
			}
			info, err := f.Info()
			if err != nil {
				flat = false
				break
			}
			size += info.Size()
			switch strings.ToLower(filepath.Ext(f.Name())) {
			case ".inf":
				infs++
			case ".cat":
				cats++
			}
		}
		if flat && infs == 1 && cats == 1 && size < 2<<20 {
			list = append(list, cand{dir, size})
		}
	}
	if len(list) == 0 {
		t.Skip("no small driver package in the driver store")
	}
	sort.Slice(list, func(i, j int) bool { return list[i].size < list[j].size })
	return list[0].dir
}

// makeCab packs the files of dir into dst\package.cab with makecab.
func makeCab(t *testing.T, dir, dst string) string {
	t.Helper()
	if err := os.MkdirAll(dst, 0o700); err != nil {
		t.Fatal(err)
	}
	files, _ := os.ReadDir(dir)
	ddf := []string{".OPTION EXPLICIT", ".Set CabinetNameTemplate=package.cab", ".Set DiskDirectoryTemplate=" + dst,
		".Set Cabinet=on", ".Set Compress=on", ".Set MaxDiskSize=0", ".Set RptFileName=nul", ".Set InfFileName=nul"}
	for _, f := range files {
		ddf = append(ddf, `"`+filepath.Join(dir, f.Name())+`"`)
	}
	ddfPath := filepath.Join(dst, "package.ddf")
	if err := os.WriteFile(ddfPath, []byte(strings.Join(ddf, "\r\n")+"\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(system32("makecab.exe"), "/F", ddfPath).CombinedOutput(); err != nil {
		t.Fatalf("makecab: %v: %s", err, out)
	}
	return filepath.Join(dst, "package.cab")
}

// TestDriverWindowsPlatform checks the hardware details the vendor BIOS
// catalogs are matched on.
func TestDriverWindowsPlatform(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	inv, err := collectInventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hw := inv.Hardware
	t.Logf("manufacturer %q model %q sku %q baseboard %q bios %q (%q)", hw.Manufacturer, hw.Model, hw.SystemSKU, hw.Baseboard,
		hw.BIOSVersion, hw.BIOS)
	if hw.BIOSVersion == "" || !strings.Contains(hw.BIOS, hw.BIOSVersion) {
		t.Errorf("BIOS version %q, BIOS %q", hw.BIOSVersion, hw.BIOS)
	}
}

// TestDriverWindowsVendorExe runs an uploaded program the way a vendor
// installer is run, with its arguments, and reads its exit code.
func TestDriverWindowsVendorExe(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "00")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "setup.exe")
	raw, err := os.ReadFile(system32("cmd.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, raw, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	job := agentproto.DriverInstall{ID: "driver:1", Kind: agentproto.DriverKindDriver, Installer: agentproto.InstallerExe}
	job.Command = "/c exit 3010"
	if reboot, _, err := (windowsDrivers{}).Install(ctx, job, []string{exe}); err != nil || !reboot {
		t.Errorf("3010: %v %v", reboot, err)
	}
	job.Command = "/c exit 0"
	if reboot, _, err := (windowsDrivers{}).Install(ctx, job, []string{exe}); err != nil || reboot {
		t.Errorf("0: %v %v", reboot, err)
	}
	job.Command = "/c exit 5"
	if _, _, err := (windowsDrivers{}).Install(ctx, job, []string{exe}); err == nil || !strings.Contains(err.Error(), "exit code 5") {
		t.Errorf("5: %v", err)
	}
}
