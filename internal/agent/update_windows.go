//go:build windows

package agent

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func defaultStateDir() string { return filepath.Join(programData(), "FleetWarden") }

func platformInstall(ctx context.Context, pkg string) error {
	log := pkg + ".log"
	cmd := exec.CommandContext(ctx, "msiexec.exe", "/i", pkg, "/qn", "/norestart", "/l*v", log)
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 3010 { // success, reboot required
		return nil
	}
	if err != nil {
		return fmt.Errorf("msiexec: %v (log: %s)", err, log)
	}
	return nil
}

func platformStartService(context.Context) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect() //nolint:errcheck
	s, err := m.OpenService("FleetWardenAgent")
	if err != nil {
		return err
	}
	defer s.Close()
	if st, err := s.Query(); err == nil && st.State == svc.Running {
		return nil
	}
	if err := s.Start(); err != nil {
		return err
	}
	time.Sleep(2 * time.Second)
	return nil
}

// startHelper starts the helper as a detached process that outlives the
// service when the installer stops it.
func startHelper(exe, plan string) error {
	flags := uint32(windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP)
	for _, extra := range []uint32{windows.CREATE_BREAKAWAY_FROM_JOB, 0} {
		cmd := exec.Command(exe, "update-helper", "--plan", plan)
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags | extra, HideWindow: true}
		if err := cmd.Start(); err != nil {
			if extra != 0 {
				continue // not allowed to leave the job; try without
			}
			return err
		}
		return cmd.Process.Release()
	}
	return errors.New("could not start the update helper")
}
