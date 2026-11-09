//go:build linux

package agent

// Software on Debian and Proxmox VE: packages from the configured apt
// repositories, uploaded .deb files (apt resolves their dependencies) and
// removal with apt-get remove or purge.

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

type aptSoftware struct {
	run func(ctx context.Context, name string, args ...string) ([]byte, error)
}

func newPlatformSoftwareBackend() SoftwareBackend { return &aptSoftware{run: runApt} }

var aptInstallOpts = []string{"-y", "-q", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold"}

// Find reports whether a package is installed; Name is the package name.
func (b *aptSoftware) Find(ctx context.Context, d agentproto.Detect) (bool, string, error) {
	name := strings.ToLower(strings.TrimSpace(d.Name))
	if !aptNameRE.MatchString(name) {
		return false, "", nil
	}
	out, err := b.run(ctx, "dpkg-query", "-W", "-f", "${db:Status-Abbrev}\t${Version}", name)
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return false, "", nil // unknown package
		}
		return false, "", err
	}
	f := strings.SplitN(strings.TrimSpace(string(out)), "\t", 2)
	if len(f) != 2 || !strings.HasPrefix(f[0], "ii") {
		return false, "", nil
	}
	return true, f[1], nil
}

func (b *aptSoftware) Install(ctx context.Context, in agentproto.Installer, file string) (RunResult, error) {
	var target string
	switch in.Kind {
	case agentproto.KindApt:
		if selfPackages[in.Package] {
			return RunResult{}, errors.New("the FleetWarden packages are updated by FleetWarden itself")
		}
		target = in.Package
		// Fresh package lists, so a new package is found.
		if out, err := b.run(ctx, "apt-get", append([]string{"update", "-q"}, aptLock...)...); err != nil {
			return RunResult{Output: string(out)}, fmt.Errorf("apt-get update: %w", err)
		}
	case agentproto.KindDeb:
		if file == "" || strings.HasPrefix(file, "-") {
			return RunResult{}, errors.New("no package file")
		}
		target = file
		if !strings.HasPrefix(target, "/") {
			target = "./" + target
		}
	default:
		return RunResult{}, fmt.Errorf("installer kind %q is not supported on Linux", in.Kind)
	}
	args := append(append([]string{"install"}, aptInstallOpts...), aptLock...)
	args = append(args, extraAptArgs(in.Args)...)
	out, err := b.run(ctx, "apt-get", append(args, target)...)
	res := runResult(out, err)
	res.Reboot = newAptBackend().RebootPending(ctx)
	if err != nil {
		return res, fmt.Errorf("apt-get install failed (exit code %d)", res.ExitCode)
	}
	return res, nil
}

func (b *aptSoftware) Uninstall(ctx context.Context, u agentproto.Uninstall) (RunResult, bool, error) {
	name := strings.ToLower(strings.TrimSpace(u.Name))
	if !aptNameRE.MatchString(name) {
		return RunResult{}, false, fmt.Errorf("%q is not a package name", u.Name)
	}
	if selfPackages[name] {
		return RunResult{}, true, errors.New("remove the FleetWarden agent from the device page instead")
	}
	ok, v, err := b.Find(ctx, agentproto.Detect{Name: name})
	if err != nil {
		return RunResult{}, false, err
	}
	if !ok || (u.Version != "" && v != u.Version) {
		return RunResult{}, false, nil
	}
	verb := "remove"
	if u.Purge {
		verb = "purge"
	}
	args := append(append([]string{verb}, aptInstallOpts...), aptLock...)
	args = append(args, extraAptArgs(u.Args)...)
	out, err := b.run(ctx, "apt-get", append(args, name)...)
	res := runResult(out, err)
	if err != nil {
		return res, true, fmt.Errorf("apt-get %s failed (exit code %d)", verb, res.ExitCode)
	}
	return res, true, nil
}

// extraAptArgs allows only apt options (no extra package names).
func extraAptArgs(s string) []string {
	var out []string
	for _, f := range strings.Fields(s) {
		if strings.HasPrefix(f, "-") {
			out = append(out, f)
		}
	}
	return out
}

func runResult(out []byte, err error) RunResult {
	res := RunResult{Output: string(out), Ran: true}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
	} else if err != nil {
		res.Ran = false
	}
	return res
}
