//go:build !linux && !windows

package agent

import (
	"context"
	"errors"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

func collectSecurity(context.Context) (agentproto.SecurityReport, error) {
	return agentproto.SecurityReport{}, errors.New("the security status is not available on this platform")
}

func runSecurityAction(_ context.Context, act agentproto.SecurityAction) (string, error) {
	return "", errors.New(act.Action + " is not available on this platform")
}
