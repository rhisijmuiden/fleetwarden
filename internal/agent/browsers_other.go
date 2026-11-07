//go:build !windows

package agent

// Browser updates run on Windows only; elsewhere no browser processes are
// managed.
func newPlatformProcessControl() ProcessControl { return nil }
