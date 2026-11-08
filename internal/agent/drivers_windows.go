//go:build windows

package agent

// Windows driver and firmware updates. The scan asks Windows Update (WUA)
// which driver updates it offers, with their download addresses, but does
// not download them. A repository update is installed from the files the
// server stores: the cabinet is unpacked and the driver added with pnputil,
// which installs it on the matching devices. Firmware (UEFI capsules) is
// staged the same way and applied by Windows on the next restart; BitLocker
// is suspended for that one restart so it does not ask for the recovery key.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	ole "github.com/go-ole/go-ole"
	"golang.org/x/sys/windows/registry"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

type windowsDrivers struct{}

func newPlatformDriverBackend() DriverBackend { return windowsDrivers{} }

func (windowsDrivers) Scan(ctx context.Context) ([]agentproto.DriverUpdate, error) {
	out := []agentproto.DriverUpdate{}
	err := withCOM(ctx, func() error {
		session, err := newWUASession()
		if err != nil {
			return err
		}
		defer session.Release()
		ups, err := searchFor(session, "IsInstalled=0 and IsHidden=0 and Type='Driver'")
		if err != nil {
			return err
		}
		defer ups.Release()
		for i := 0; i < count(ups); i++ {
			u, err := dispProp(ups, "Item", i)
			if err != nil {
				continue
			}
			if d := describeDriver(u); d.SourceID != "" && len(d.Files) > 0 {
				out = append(out, d)
			}
			u.Release()
		}
		return nil
	})
	if err != nil {
		return out, fmt.Errorf("windows update driver scan: %w", err)
	}
	if len(out) > 0 {
		devs := pnpDevices(ctx)
		for i := range out {
			if d, ok := devs[strings.ToLower(out[i].HardwareID)]; ok {
				out[i].Device, out[i].CurrentVersion = d.Name, d.Version
			}
		}
	}
	return out, nil
}

func describeDriver(u *ole.IDispatch) agentproto.DriverUpdate {
	d := agentproto.DriverUpdate{Source: agentproto.DriverSourceWindowsUpdate, Kind: agentproto.DriverKindDriver}
	if ident, err := dispProp(u, "Identity"); err == nil {
		d.SourceID = strings.ToLower(strProp(ident, "UpdateID"))
		ident.Release()
	}
	d.Title = strProp(u, "Title")
	d.Description = strProp(u, "Description")
	d.Class = strProp(u, "DriverClass")
	d.Vendor = firstNonEmpty(strProp(u, "DriverManufacturer"), strProp(u, "DriverProvider"))
	d.HardwareID = strProp(u, "DriverHardwareID")
	d.Version = versionInTitle(d.Title)
	if strings.EqualFold(d.Class, "Firmware") {
		d.Kind = agentproto.DriverKindFirmware
	}
	if t, ok := valProp(u, "DriverVerDate").(time.Time); ok && t.Year() > 1990 {
		d.ReleasedAt = t.UTC()
	} else if t, ok := valProp(u, "LastDeploymentChangeTime").(time.Time); ok && t.Year() > 2000 {
		d.ReleasedAt = t.UTC()
	}
	switch v := valProp(u, "MaxDownloadSize").(type) {
	case int64:
		d.SizeBytes = v
	case float64:
		d.SizeBytes = int64(v)
	}
	if ib, err := dispProp(u, "InstallationBehavior"); err == nil {
		d.RebootRequired = intProp(ib, "RebootBehavior") != 0
		ib.Release()
	}
	d.RebootRequired = d.RebootRequired || d.Kind == agentproto.DriverKindFirmware
	d.Files = downloadContents(u)
	if bundled, err := dispProp(u, "BundledUpdates"); err == nil {
		for i := 0; i < count(bundled); i++ {
			if b, err := dispProp(bundled, "Item", i); err == nil {
				d.Files = append(d.Files, downloadContents(b)...)
				b.Release()
			}
		}
		bundled.Release()
	}
	return d
}

// downloadContents lists the download addresses of an update.
func downloadContents(u *ole.IDispatch) []agentproto.DriverFile {
	var out []agentproto.DriverFile
	dc, err := dispProp(u, "DownloadContents")
	if err != nil {
		return nil
	}
	defer dc.Release()
	for i := 0; i < count(dc); i++ {
		c, err := dispProp(dc, "Item", i)
		if err != nil {
			continue
		}
		link := strProp(c, "DownloadUrl")
		c.Release()
		if link == "" {
			continue
		}
		name := ""
		if p, err := url.Parse(link); err == nil {
			name = path.Base(p.Path)
		}
		out = append(out, agentproto.DriverFile{Name: name, URL: link})
	}
	return out
}

type pnpDevice struct {
	Name    string
	Version string
}

const pnpScript = `$ErrorActionPreference='SilentlyContinue'
$v=@{}; Get-CimInstance Win32_PnPSignedDriver | ForEach-Object { if($_.DeviceID){ $v[[string]$_.DeviceID]=[string]$_.DriverVersion } }
@(Get-CimInstance Win32_PnPEntity | ForEach-Object { [pscustomobject]@{n=[string]$_.Name; v=$v[[string]$_.DeviceID]; ids=@(@($_.HardwareID)+@($_.CompatibleID)|Where-Object {$_})} }) | ConvertTo-Json -Compress -Depth 3`

// pnpDevices maps hardware and compatible IDs (lower case) to the device
// name and its current driver version. Hardware IDs win over compatible IDs.
func pnpDevices(ctx context.Context) map[string]pnpDevice {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	out := map[string]pnpDevice{}
	raw, err := runPowerShell(ctx, pnpScript)
	if err != nil {
		return out
	}
	var list []struct {
		Name    string   `json:"n"`
		Version string   `json:"v"`
		IDs     []string `json:"ids"`
	}
	if json.Unmarshal(raw, &list) != nil {
		return out
	}
	for _, d := range list {
		for _, id := range d.IDs {
			k := strings.ToLower(id)
			if _, ok := out[k]; !ok {
				out[k] = pnpDevice{Name: d.Name, Version: d.Version}
			}
		}
	}
	return out
}

func system32(name string) string { return filepath.Join(os.Getenv("SystemRoot"), "System32", name) }

func hiddenCommand(ctx context.Context, exe string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}

// unpackCab extracts a cabinet with its folders (tar reads cabinets since
// Windows 10 1803), falling back to expand.exe.
func unpackCab(ctx context.Context, cab, dst string) error {
	if tar := system32("tar.exe"); fileExists(tar) {
		if hiddenCommand(ctx, tar, "-xf", cab, "-C", dst).Run() == nil {
			return nil
		}
	}
	out, err := hiddenCommand(ctx, system32("expand.exe"), "-F:*", cab, dst).CombinedOutput()
	if err != nil {
		return fmt.Errorf("unpack %s: %w: %s", filepath.Base(cab), err, lastLine(string(out)))
	}
	return nil
}

func (windowsDrivers) Install(ctx context.Context, job agentproto.DriverInstall, files []string) (bool, string, error) {
	if len(files) == 0 {
		return false, "", errors.New("no files")
	}
	switch job.Installer {
	case "", agentproto.InstallerINF:
		return installINF(ctx, job, files)
	case agentproto.InstallerDell, agentproto.InstallerHP, agentproto.InstallerLenovo, agentproto.InstallerExe:
		return installVendor(ctx, job, files[0])
	}
	return false, "", fmt.Errorf("unknown installer %q", job.Installer)
}

// installVendor runs a vendor package silently: a Dell Update Package
// directly, an HP SoftPaq or Lenovo package after extracting it, an
// uploaded program with its arguments.
func installVendor(ctx context.Context, job agentproto.DriverInstall, file string) (bool, string, error) {
	firmware := job.Kind == agentproto.DriverKindFirmware
	if firmware {
		suspendBitLocker(ctx)
	}
	work, err := os.MkdirTemp(filepath.Dir(filepath.Dir(file)), "package-")
	if err != nil {
		return false, "", err
	}
	defer os.RemoveAll(work)
	var reboot bool
	var note string
	switch job.Installer {
	case agentproto.InstallerDell:
		code, out, err := runExit(ctx, file, "/s", "/l="+filepath.Join(work, "dup.log"))
		if err != nil {
			return false, "", err
		}
		reboot, note, err = dellOutcome(code)
		if err != nil {
			return false, "", fmt.Errorf("%w %s", err, lastLine(out))
		}
	case agentproto.InstallerExe:
		code, out, err := runExit(ctx, file, agentproto.SplitCommand(job.Command)...)
		if err != nil {
			return false, "", err
		}
		if reboot, note, err = exitOutcome(code, job.ExitCodes); err != nil {
			return false, "", fmt.Errorf("%w %s", err, lastLine(out))
		}
	default:
		dir := filepath.Join(work, "x")
		var args []string
		if job.Installer == agentproto.InstallerHP {
			args = []string{"/s", "/e", "/f", dir}
		} else {
			args = []string{"/VERYSILENT", "/DIR=" + dir, "/EXTRACT=YES"}
		}
		if code, out, err := runExit(ctx, file, args...); err != nil || code != 0 {
			return false, "", fmt.Errorf("extract the package (exit code %d): %v %s", code, err, lastLine(out))
		}
		exe, cargs, err := packageCommand(dir, job.Command)
		if err != nil {
			return false, "", err
		}
		if !fileExists(exe) {
			return false, "", fmt.Errorf("the package has no %s", filepath.Base(exe))
		}
		code, out, err := runExitIn(ctx, dir, exe, cargs...)
		if err != nil {
			return false, "", err
		}
		if reboot, note, err = exitOutcome(code, job.ExitCodes); err != nil {
			return false, "", fmt.Errorf("%w %s", err, lastLine(out))
		}
	}
	return reboot || firmware, note, nil
}

func runExit(ctx context.Context, exe string, args ...string) (int, string, error) {
	return runExitIn(ctx, filepath.Dir(exe), exe, args...)
}

// runExitIn runs a program hidden and returns its exit code.
func runExitIn(ctx context.Context, dir, exe string, args ...string) (int, string, error) {
	cmd := hiddenCommand(ctx, exe, args...)
	cmd.Dir = dir
	raw, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), string(raw), nil
	}
	if err != nil {
		return -1, string(raw), fmt.Errorf("run %s: %w", filepath.Base(exe), err)
	}
	return 0, string(raw), nil
}

// installINF unpacks the files and adds the drivers with pnputil.
func installINF(ctx context.Context, job agentproto.DriverInstall, files []string) (bool, string, error) {
	dir, err := os.MkdirTemp(filepath.Dir(filepath.Dir(files[0])), "unpacked-")
	if err != nil {
		return false, "", err
	}
	defer os.RemoveAll(dir)
	for i, f := range files {
		sub := filepath.Join(dir, fmt.Sprintf("%02d", i))
		if err := os.MkdirAll(sub, 0o700); err != nil {
			return false, "", err
		}
		switch strings.ToLower(filepath.Ext(f)) {
		case ".cab", ".zip":
			if err := unpackCab(ctx, f, sub); err != nil {
				return false, "", err
			}
		default:
			if err := os.Rename(f, filepath.Join(sub, filepath.Base(f))); err != nil {
				return false, "", err
			}
		}
	}
	if !hasINF(dir) {
		return false, "", errors.New("the package has no driver (.inf) file")
	}
	firmware := job.Kind == agentproto.DriverKindFirmware
	if firmware {
		suspendBitLocker(ctx)
	}
	out, code, err := pnputilAdd(ctx, dir)
	if err != nil {
		return false, "", err
	}
	reboot, note, err := pnputilOutcome(code, out)
	return reboot || firmware, note, err
}

func hasINF(dir string) bool {
	found := false
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".inf") {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}

func pnputilAdd(ctx context.Context, dir string) (string, int, error) {
	cmd := hiddenCommand(ctx, system32("pnputil.exe"), "/add-driver", filepath.Join(dir, "*.inf"), "/subdirs", "/install")
	raw, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return string(raw), ee.ExitCode(), nil
	}
	if err != nil {
		return string(raw), -1, fmt.Errorf("pnputil: %w", err)
	}
	return string(raw), 0, nil
}

// suspendBitLocker suspends BitLocker on the system drive for one restart,
// so a firmware update does not trigger the recovery screen. Drives without
// BitLocker answer with an error, which is fine.
func suspendBitLocker(ctx context.Context) {
	drive := os.Getenv("SystemDrive")
	if drive == "" {
		drive = "C:"
	}
	_ = hiddenCommand(ctx, system32("manage-bde.exe"), "-protectors", "-disable", drive, "-RebootCount", "1").Run()
}

const (
	searchOrderKey    = `SOFTWARE\Microsoft\Windows\CurrentVersion\DriverSearching`
	searchOrderValue  = "SearchOrderConfig"
	searchOrderSaved  = "FleetWardenSearchOrder" // the value before FleetWarden changed it
	searchOrderAbsent = 0xFFFFFFFF
)

// Configure stops Windows from fetching drivers from Windows Update when a
// device is plugged in (manage) or restores what was there before.
func (windowsDrivers) Configure(manage bool) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, searchOrderKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	saved, _, savedErr := k.GetIntegerValue(searchOrderSaved)
	if manage {
		if savedErr != nil {
			cur, _, err := k.GetIntegerValue(searchOrderValue)
			if err != nil {
				cur = searchOrderAbsent
			}
			if err := k.SetDWordValue(searchOrderSaved, uint32(cur)); err != nil {
				return err
			}
		}
		return k.SetDWordValue(searchOrderValue, 0)
	}
	if savedErr != nil {
		return nil // never changed by FleetWarden
	}
	if saved == searchOrderAbsent {
		_ = k.DeleteValue(searchOrderValue)
	} else if err := k.SetDWordValue(searchOrderValue, uint32(saved)); err != nil {
		return err
	}
	return k.DeleteValue(searchOrderSaved)
}
