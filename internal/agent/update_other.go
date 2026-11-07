//go:build !linux && !windows

package agent

import (
	"context"
	"errors"
	"path/filepath"
)

func defaultStateDir() string { return filepath.Dir(ConfigPath()) }

var errNoSelfUpdate = errors.New("self-update is not supported on this system")

func platformInstall(context.Context, string) error { return errNoSelfUpdate }
func platformStartService(context.Context) error    { return nil }
func startHelper(string, string) error              { return errNoSelfUpdate }
