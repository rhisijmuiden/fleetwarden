package agent

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows/registry"
)

type windowsPower struct{}

func newPlatformPowerBackend() PowerBackend { return windowsPower{} }

// shutdownExe starts a restart or shutdown in a few seconds; logged-on
// users see the message. A timeout implies /f, so open programs do not stop
// it (the user had the countdown of the pop-up). /d p:0:0 records it as
// planned.
func shutdownExe(ctx context.Context, flag, message string) error {
	out, err := exec.CommandContext(ctx, "shutdown.exe", flag, "/t", "5", "/d", "p:0:0", "/c", message).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (windowsPower) Restart(ctx context.Context, message string) error {
	return shutdownExe(ctx, "/r", message)
}

func (windowsPower) Shutdown(ctx context.Context, message string) error {
	return shutdownExe(ctx, "/s", message)
}

const (
	sessionPowerKey = `SYSTEM\CurrentControlSet\Control\Session Manager\Power`
	hiberbootValue  = "HiberbootEnabled"
	// The agent notes the Fast Startup value it found, to put it back.
	hiberbootSaved = "FleetWardenHiberbootEnabled"
)

// prepareWakeScript turns on "wake on magic packet" for the wired physical
// adapters: the power management switch and the driver's own property.
// Adapters that do not have them are skipped.
const prepareWakeScript = `$ErrorActionPreference='SilentlyContinue'
$n=0
foreach ($a in Get-NetAdapter -Physical | Where-Object { $_.MediaType -eq '802.3' -and $_.PhysicalMediaType -notmatch 'Wireless|Native 802.11' }) {
  Set-NetAdapterPowerManagement -Name $a.Name -WakeOnMagicPacket Enabled -NoRestart
  Set-NetAdapterAdvancedProperty -Name $a.Name -RegistryKeyword '*WakeOnMagicPacket' -RegistryValue 1 -NoRestart
  $n++
}
"adapters=$n"`

// PrepareWake turns on wake on magic packet and turns off Fast Startup: a
// computer shut down with Fast Startup is half hibernated and many network
// adapters do not wake it. Off restores Fast Startup as it was; the adapter
// settings stay (they do no harm).
func (windowsPower) PrepareWake(on bool) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, sessionPowerKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		saved, _, err := k.GetIntegerValue(hiberbootSaved)
		if err != nil {
			return nil // nothing changed by the agent
		}
		if err := k.SetDWordValue(hiberbootValue, uint32(saved)); err != nil {
			return err
		}
		return k.DeleteValue(hiberbootSaved)
	}
	if _, _, err := k.GetIntegerValue(hiberbootSaved); errors.Is(err, registry.ErrNotExist) {
		cur, _, err := k.GetIntegerValue(hiberbootValue)
		if err != nil {
			cur = 1 // missing means on
		}
		if err := k.SetDWordValue(hiberbootSaved, uint32(cur)); err != nil {
			return err
		}
	}
	if err := k.SetDWordValue(hiberbootValue, 0); err != nil {
		return err
	}
	out, err := exec.Command(system32(`WindowsPowerShell\v1.0\powershell.exe`), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", prepareWakeScript).CombinedOutput()
	if err != nil {
		return fmt.Errorf("network adapters: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
