//go:build windows

package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"golang.org/x/sys/windows/svc/eventlog"
)

// eventLogWriter is a minimal slog handler for the Windows event log.
type eventLogWriter struct {
	el    *eventlog.Log
	attrs []slog.Attr
}

func (h *eventLogWriter) Enabled(_ context.Context, l slog.Level) bool { return l >= slog.LevelInfo }

func (h *eventLogWriter) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	write := func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value.Any())
		return true
	}
	for _, a := range h.attrs {
		write(a)
	}
	r.Attrs(write)
	switch {
	case r.Level >= slog.LevelError:
		return h.el.Error(3, b.String())
	case r.Level >= slog.LevelWarn:
		return h.el.Warning(2, b.String())
	}
	return h.el.Info(1, b.String())
}

func (h *eventLogWriter) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &eventLogWriter{el: h.el, attrs: append(append([]slog.Attr{}, h.attrs...), attrs...)}
}

func (h *eventLogWriter) WithGroup(string) slog.Handler { return h }
