//go:build !linux && !windows

package agent

import (
	"context"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

type unsupportedSoftware struct{}

func newPlatformSoftwareBackend() SoftwareBackend { return unsupportedSoftware{} }

func (unsupportedSoftware) Find(context.Context, agentproto.Detect) (bool, string, error) {
	return false, "", nil
}

func (unsupportedSoftware) Install(context.Context, agentproto.Installer, string) (RunResult, error) {
	return RunResult{}, ErrSoftwareUnsupported
}

func (unsupportedSoftware) Uninstall(context.Context, agentproto.Uninstall) (RunResult, bool, error) {
	return RunResult{}, false, ErrSoftwareUnsupported
}
