//go:build !linux

package agent

import (
	"context"
	"errors"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

var errPatchUnsupported = errors.New("patching is not supported on this platform")

type unsupportedPatches struct{}

func newPlatformPatchBackend() PatchBackend { return unsupportedPatches{} }

func (unsupportedPatches) Scan(context.Context) (agentproto.PatchScanResult, error) {
	return agentproto.PatchScanResult{}, errPatchUnsupported
}

func (unsupportedPatches) Install(context.Context, []string, func(string)) ([]agentproto.PatchResult, bool, error) {
	return nil, false, errPatchUnsupported
}

func (unsupportedPatches) RebootPending(context.Context) bool     { return false }
func (unsupportedPatches) Reboot(context.Context, string) error   { return errPatchUnsupported }
func (unsupportedPatches) Configure(agentproto.PatchConfig) error { return nil }
