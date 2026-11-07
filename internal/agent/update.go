package agent

import (
	"context"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// handleUpdate installs another agent version on request of the server.
func (a *Agent) handleUpdate(_ context.Context, e *agentproto.Envelope, _ func(string, any)) {
	a.Log.Info("agent update requested; self-update is not available in this build", "type", e.Type)
}
