//go:build !linux && !windows

package agent

import (
	"context"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

type noDrivers struct{}

func newPlatformDriverBackend() DriverBackend { return noDrivers{} }

func (noDrivers) Scan(context.Context) ([]agentproto.DriverUpdate, error) { return nil, errNoDrivers }

func (noDrivers) Install(context.Context, agentproto.DriverInstall, []string) (bool, string, error) {
	return false, "", errNoDrivers
}

func (noDrivers) Configure(bool) error { return nil }
