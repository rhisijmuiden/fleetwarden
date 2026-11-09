//go:build !linux && !windows

package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// Development builds on other systems (macOS support is planned for phase 10).

func defaultConfigPath() string {
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "fleetwarden-agent", "agent.json")
}

func restrictDir(dir string) error      { return os.Chmod(dir, 0o700) }
func machineID() string                 { return "" }
func bootTime() time.Time               { return time.Time{} }
func loggedInUser() string              { return os.Getenv("USER") }
func fixedVolumes() []agentproto.Volume { return nil }

func collectInventory(_ context.Context) (agentproto.Inventory, error) {
	inv := agentproto.Inventory{OS: agentproto.OSInfo{Name: runtime.GOOS, Kind: "workstation"}}
	inv.Hostname, _ = os.Hostname()
	inv.Hardware.Network = networkInterfaces()
	return inv, nil
}
