//go:build !windows

package agent

import "log/slog"

// platformRemote has no remote desktop outside Windows yet; Linux gets a
// terminal instead.
func platformRemote(*slog.Logger) RemoteBackend { return nil }
