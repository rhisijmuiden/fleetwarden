package agent

// Driver and firmware updates. The agent reports what the platform offers
// (Windows Update drivers and firmware, fwupd on Linux) with each patch scan,
// but never downloads these updates from the vendor: a patch job carries the
// updates an administrator added to FleetWarden's repository, and the agent
// fetches their files from the server (signed, checked against the SHA-256).

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// DriverBackend scans for and installs driver and firmware updates.
type DriverBackend interface {
	// Scan lists the driver and firmware updates the platform offers.
	Scan(ctx context.Context) ([]agentproto.DriverUpdate, error)
	// Install installs one update from its downloaded files and reports
	// whether a restart is needed and a note for the job log.
	Install(ctx context.Context, job agentproto.DriverInstall, files []string) (reboot bool, note string, err error)
	// Configure stops (manage) or allows the platform fetching drivers by itself.
	Configure(manage bool) error
}

// errNoDrivers means the platform has no driver updates (macOS, BSD).
var errNoDrivers = errors.New("driver updates are not supported on this platform")

const (
	driverScanTimeout    = 15 * time.Minute
	driverInstallTimeout = 45 * time.Minute
)

func (a *Agent) driverBackend() DriverBackend {
	if a.Drivers == nil {
		a.Drivers = newPlatformDriverBackend()
	}
	return a.Drivers
}

// scanDrivers adds the driver part to a patch scan result.
func (a *Agent) scanDrivers(ctx context.Context, res *agentproto.PatchScanResult) {
	be := a.driverBackend()
	if be == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, driverScanTimeout)
	defer cancel()
	list, err := be.Scan(ctx)
	if errors.Is(err, errNoDrivers) {
		return
	}
	res.DriversScanned = true
	res.Drivers = list
	if err != nil {
		res.DriverError = err.Error()
		a.Log.Warn("driver scan", "err", err)
	}
	if res.Drivers == nil {
		res.Drivers = []agentproto.DriverUpdate{}
	}
}

var repoFileNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._()+~-]{0,150}$`)

func validDriverJob(d agentproto.DriverInstall) bool {
	if !patchIDRE.MatchString(d.ID) || len(d.Files) == 0 || len(d.Files) > 20 || len(d.Command) > 1000 {
		return false
	}
	switch d.Installer {
	case "", agentproto.InstallerINF, agentproto.InstallerFwupd, agentproto.InstallerExe:
	case agentproto.InstallerDell, agentproto.InstallerHP, agentproto.InstallerLenovo:
		if len(d.Files) != 1 {
			return false
		}
	default:
		return false
	}
	for _, f := range d.Files {
		if !sha256HexRE.MatchString(strings.ToLower(f.SHA256)) || !repoFileNameRE.MatchString(f.Name) || f.Size < 0 {
			return false
		}
	}
	return true
}

var sha256HexRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// installDrivers fetches and installs the repository updates of a job.
func (a *Agent) installDrivers(ctx context.Context, jobs []agentproto.DriverInstall, progress func(string)) ([]agentproto.PatchResult, bool) {
	be := a.driverBackend()
	var results []agentproto.PatchResult
	reboot := false
	for _, d := range jobs {
		if ctx.Err() != nil {
			break
		}
		res := agentproto.PatchResult{ID: d.ID}
		if be == nil {
			res.Error = errNoDrivers.Error()
			results = append(results, res)
			continue
		}
		progress("installing " + d.Title)
		r, note, err := a.installDriver(ctx, be, d)
		reboot = reboot || r
		res.OK = err == nil
		if err != nil {
			res.Error = err.Error()
			a.Log.Warn("driver install", "driver", d.Title, "err", err)
		} else {
			a.Log.Info("driver installed", "driver", d.Title, "reboot", r, "note", note)
		}
		results = append(results, res)
	}
	return results, reboot
}

func (a *Agent) installDriver(ctx context.Context, be DriverBackend, d agentproto.DriverInstall) (bool, string, error) {
	ctx, cancel := context.WithTimeout(ctx, driverInstallTimeout)
	defer cancel()
	dir, err := os.MkdirTemp(a.stateDir(), "driver-")
	if err != nil {
		return false, "", err
	}
	defer os.RemoveAll(dir)
	paths := make([]string, 0, len(d.Files))
	for i, f := range d.Files {
		// Each file in its own folder: two files may share a name.
		sub := filepath.Join(dir, fmt.Sprintf("%02d", i))
		if err := os.Mkdir(sub, 0o700); err != nil {
			return false, "", err
		}
		dst := filepath.Join(sub, f.Name)
		if err := a.downloadSigned(ctx, agentproto.PathRepository+strings.ToLower(f.SHA256), f.SHA256, f.Size, dst); err != nil {
			return false, "", fmt.Errorf("download %s: %w", f.Name, err)
		}
		paths = append(paths, dst)
	}
	return be.Install(ctx, d, paths)
}

// versionInTitle finds the version at the end of a Windows Update driver
// title, e.g. "Intel - System - 10.1.1.44" -> "10.1.1.44".
func versionInTitle(title string) string {
	m := titleVersionRE.FindStringSubmatch(title)
	if m == nil {
		return ""
	}
	return m[1]
}

var titleVersionRE = regexp.MustCompile(`(?:^|[\s\-])v?(\d+(?:\.\d+){1,4})\s*\)?\s*$`)

// pnputilOutcome reads pnputil's exit code: 0 installed, 3010 installed and
// a restart is needed, 259 added to the driver store but no device took it
// (Windows already has an equal or better driver).
func pnputilOutcome(code int, out string) (bool, string, error) {
	reboot := strings.Contains(strings.ToLower(out), "reboot is needed") || strings.Contains(strings.ToLower(out), "restart is needed")
	switch code {
	case 0:
		return reboot, "installed", nil
	case 3010:
		return true, "installed; a restart is needed", nil
	case 259:
		return reboot, "added to the driver store; the devices keep their current driver", nil
	}
	return false, "", fmt.Errorf("pnputil failed (exit code %d): %s", code, lastLine(out))
}

// packageCommand turns a vendor's install command ("HpFirmwareUpdRec.exe
// -b -s", "%PACKAGEPATH%\winuptp.exe -s") into the program inside the
// extracted package dir and its arguments. The program must lie inside dir.
func packageCommand(dir, command string) (string, []string, error) {
	command = strings.NewReplacer("%PACKAGEPATH%", dir, "%packagepath%", dir).Replace(command)
	f := agentproto.SplitCommand(command)
	if len(f) == 0 {
		return "", nil, errors.New("the package has no install command")
	}
	exe := f[0]
	if !filepath.IsAbs(exe) {
		exe = filepath.Join(dir, exe)
	}
	exe = filepath.Clean(exe)
	rel, err := filepath.Rel(dir, exe)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", nil, fmt.Errorf("the install command runs a program outside the package: %s", f[0])
	}
	return exe, f[1:], nil
}

// exitOutcome reads the exit code of a vendor installer. Besides the codes
// the vendor lists, 3010 and 1641 mean installed with a restart needed.
func exitOutcome(code int, ok []int) (bool, string, error) {
	switch {
	case code == 3010 || code == 1641:
		return true, "installed; a restart is needed", nil
	case len(ok) == 0 && code == 0, slices.Contains(ok, code):
		return false, "installed", nil
	}
	return false, "", fmt.Errorf("the installer failed (exit code %d)", code)
}

// dellOutcome reads the exit code of a Dell Update Package.
func dellOutcome(code int) (bool, string, error) {
	switch code {
	case 0:
		return false, "installed", nil
	case 2, 6, 9:
		return true, "installed; a restart is needed", nil
	case 3:
		return false, "nothing to do: the same or a newer version is installed", nil
	case 7:
		return false, "", errors.New("Dell Update Package: the BIOS has a setup password (exit code 7)")
	case 8:
		return false, "", errors.New("Dell Update Package: downgrades are not allowed (exit code 8)")
	}
	return false, "", fmt.Errorf("Dell Update Package failed (exit code %d)", code)
}
