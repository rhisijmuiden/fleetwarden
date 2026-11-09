//go:build !windows

package agent

import (
	"context"
	"log/slog"
)

// platformNotifier: only Windows has a tray app; elsewhere patch jobs run
// without asking.
func platformNotifier(context.Context, *slog.Logger) Notifier { return nil }
