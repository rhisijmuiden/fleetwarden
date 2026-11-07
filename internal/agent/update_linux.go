//go:build linux

package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func defaultStateDir() string { return "/var/lib/fleetwarden-agent" }

func platformInstall(ctx context.Context, pkg string) error {
	cmd := exec.CommandContext(ctx, "dpkg", "-i", pkg)
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("dpkg: %v: %s", err, tail(out, 400))
	}
	return nil
}

func platformStartService(ctx context.Context) error {
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return nil
	}
	if out, err := exec.CommandContext(ctx, "systemctl", "start", "fleetwarden-agent.service").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl: %v: %s", err, tail(out, 200))
	}
	return nil
}

// startHelper runs the helper in its own transient systemd unit, so stopping
// the agent service during the package upgrade does not stop it.
func startHelper(exe, plan string) error {
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		if path, err := exec.LookPath("systemd-run"); err == nil {
			unit := fmt.Sprintf("fleetwarden-agent-update-%d", time.Now().Unix())
			out, err := exec.Command(path, "--unit", unit, "--collect", "--quiet", exe, "update-helper", "--plan", plan).CombinedOutput()
			if err != nil {
				return fmt.Errorf("systemd-run: %v: %s", err, tail(out, 200))
			}
			return nil
		}
	}
	cmd := exec.Command(exe, "update-helper", "--plan", plan)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
