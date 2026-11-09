package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type linuxPower struct{}

func newPlatformPowerBackend() PowerBackend { return linuxPower{} }

func systemctl(ctx context.Context, verb string) error {
	out, err := exec.CommandContext(ctx, "systemctl", verb).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %v: %s", verb, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (linuxPower) Restart(ctx context.Context, _ string) error  { return systemctl(ctx, "reboot") }
func (linuxPower) Shutdown(ctx context.Context, _ string) error { return systemctl(ctx, "poweroff") }

// PrepareWake sets "wol g" (magic packet) with ethtool on the wired
// adapters. The setting does not survive a restart on every distribution,
// so the agent applies it again each time it starts. Off leaves the
// adapters as they are.
func (linuxPower) PrepareWake(on bool) error {
	if !on {
		return nil
	}
	ethtool, err := exec.LookPath("ethtool")
	if err != nil {
		return errors.New("ethtool is not installed")
	}
	ifs, _ := filepath.Glob("/sys/class/net/*")
	var errs []string
	for _, p := range ifs {
		name := filepath.Base(p)
		if _, err := os.Stat(filepath.Join(p, "device")); err != nil {
			continue // virtual: bridge, bond, tap, loopback
		}
		if _, err := os.Stat(filepath.Join(p, "wireless")); err == nil {
			continue
		}
		info, err := exec.Command(ethtool, name).Output()
		if err != nil || !strings.Contains(string(info), "Supports Wake-on:") {
			continue
		}
		if !wakeSupportsMagic(string(info)) {
			continue
		}
		if out, err := exec.Command(ethtool, "-s", name, "wol", "g").CombinedOutput(); err != nil {
			errs = append(errs, name+": "+strings.TrimSpace(string(out)))
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// wakeSupportsMagic reads "Supports Wake-on: pumbg" from ethtool output.
func wakeSupportsMagic(info string) bool {
	for _, l := range strings.Split(info, "\n") {
		l = strings.TrimSpace(l)
		if v, ok := strings.CutPrefix(l, "Supports Wake-on:"); ok {
			return strings.Contains(strings.TrimSpace(v), "g")
		}
	}
	return false
}
