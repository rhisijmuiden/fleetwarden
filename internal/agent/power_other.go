//go:build !linux && !windows

package agent

import (
	"context"
	"errors"
)

var errPowerUnsupported = errors.New("power actions are not supported on this platform")

type unsupportedPower struct{}

func newPlatformPowerBackend() PowerBackend { return unsupportedPower{} }

func (unsupportedPower) Restart(context.Context, string) error  { return errPowerUnsupported }
func (unsupportedPower) Shutdown(context.Context, string) error { return errPowerUnsupported }
func (unsupportedPower) PrepareWake(bool) error                 { return nil }
