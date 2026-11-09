package agent

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// Self-service: the tray app's requests go to the server over the current
// session and the reply comes back to the tray. While the agent is offline
// (or the server does not support self-service) the tray gets an error
// right away.

// selfServiceTimeout bounds the wait for the server's reply.
const selfServiceTimeout = 30 * time.Second

type selfServiceState struct {
	mu      sync.Mutex
	send    func(string, any) bool // nil while offline or unsupported
	gen     uint64
	online  bool // a session is open
	seq     uint64
	waiting map[string]chan agentproto.SelfServiceReply
}

func (a *Agent) attachSelfService(send func(string, any) bool, supported bool) uint64 {
	s := &a.selfService
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen++
	s.send, s.online = nil, true
	if supported {
		s.send = send
	}
	return s.gen
}

func (a *Agent) detachSelfService(gen uint64) {
	s := &a.selfService
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gen != gen {
		return
	}
	s.send, s.online = nil, false
	for id, ch := range s.waiting {
		close(ch) // the session is gone: the request fails
		delete(s.waiting, id)
	}
}

// SelfService sends a tray request to the server and waits for its reply.
// The caller sets User; RequestID is set here.
func (a *Agent) SelfService(ctx context.Context, req agentproto.SelfServiceRequest) agentproto.SelfServiceReply {
	s := &a.selfService
	s.mu.Lock()
	send, online := s.send, s.online
	if send == nil {
		s.mu.Unlock()
		if online {
			return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrNoService}
		}
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrOffline}
	}
	s.seq++
	req.RequestID = strconv.FormatUint(s.seq, 10)
	ch := make(chan agentproto.SelfServiceReply, 1)
	if s.waiting == nil {
		s.waiting = map[string]chan agentproto.SelfServiceReply{}
	}
	s.waiting[req.RequestID] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.waiting, req.RequestID)
		s.mu.Unlock()
	}()
	if !send(agentproto.TypeSelfServiceRequest, req) {
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrOffline}
	}
	t := time.NewTimer(selfServiceTimeout)
	defer t.Stop()
	select {
	case r, ok := <-ch:
		if !ok {
			return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrOffline}
		}
		return r
	case <-t.C:
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrOffline}
	case <-ctx.Done():
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrOffline}
	}
}

func (a *Agent) handleSelfServiceReply(e *agentproto.Envelope) {
	var r agentproto.SelfServiceReply
	if e.Decode(&r) != nil {
		return
	}
	s := &a.selfService
	s.mu.Lock()
	ch := s.waiting[r.RequestID]
	delete(s.waiting, r.RequestID)
	s.mu.Unlock()
	if ch != nil {
		ch <- r
	}
}

// SelfServiceHost is a Notifier that takes self-service requests from the
// tray (the tray hub).
type SelfServiceHost interface {
	SetSelfService(fn func(ctx context.Context, req agentproto.SelfServiceRequest) agentproto.SelfServiceReply)
}
