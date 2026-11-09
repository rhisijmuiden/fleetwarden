package agent

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// securityState holds the sender of the current session, so an action that
// outlives a session (a full scan takes hours) still reports to the next one.
type securityState struct {
	mu      sync.Mutex
	gen     int
	send    func(string, any) bool
	running map[int64]bool
	// collecting keeps reports from overlapping.
	collecting sync.Mutex
}

func (s *securityState) attach(send func(string, any) bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen++
	s.send = send
	return s.gen
}

func (s *securityState) detach(gen int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gen == gen {
		s.send = nil
	}
}

func (s *securityState) deliver(typ string, v any) bool {
	s.mu.Lock()
	send := s.send
	s.mu.Unlock()
	return send != nil && send(typ, v)
}

// collectSecurityReport gathers the report, with the platform collector or
// the one a test set.
func (a *Agent) collectSecurityReport(ctx context.Context) agentproto.SecurityReport {
	a.security.collecting.Lock()
	defer a.security.collecting.Unlock()
	collect := a.Security
	if collect == nil {
		collect = collectSecurity
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	rep, err := collect(cctx)
	if err != nil {
		rep.Errors = append(rep.Errors, err.Error())
	}
	rep.CollectedAt = time.Now().UTC()
	normalizeSecurity(&rep)
	return rep
}

func (a *Agent) sendSecurity(ctx context.Context, send func(string, any)) {
	rep := a.collectSecurityReport(ctx)
	send(agentproto.TypeSecurityReport, rep)
	a.Log.Info("security status sent", "volumes", len(rep.Volumes), "errors", len(rep.Errors))
}

// handleSecurityRequest sends a fresh report in the background.
func (a *Agent) handleSecurityRequest() {
	go func() {
		rep := a.collectSecurityReport(a.runCtx())
		a.security.deliver(agentproto.TypeSecurityReport, rep)
	}()
}

var driveLetterRE = regexp.MustCompile(`^[A-Za-z]:$`)

// handleSecurityAction runs a Defender scan, a signature update or a
// BitLocker key rotation, and reports the result and a fresh status.
func (a *Agent) handleSecurityAction(e *agentproto.Envelope) {
	var act agentproto.SecurityAction
	if err := e.Decode(&act); err != nil || act.ID <= 0 {
		return
	}
	status := func(state, msg string) {
		a.security.deliver(agentproto.TypeSecurityActionStatus, agentproto.SecurityActionStatus{ID: act.ID, State: state, Message: msg})
	}
	switch act.Action {
	case agentproto.SecurityQuickScan, agentproto.SecurityFullScan, agentproto.SecuritySignatures:
	case agentproto.SecurityRotateKey:
		if !driveLetterRE.MatchString(act.Mount) {
			status(agentproto.SecurityActionFailed, "invalid volume")
			return
		}
		act.Mount = strings.ToUpper(act.Mount)
	default:
		status(agentproto.SecurityActionFailed, "unknown action "+act.Action)
		return
	}
	a.security.mu.Lock()
	if a.security.running == nil {
		a.security.running = map[int64]bool{}
	}
	if a.security.running[act.ID] {
		a.security.mu.Unlock()
		return // sent again after a reconnect
	}
	a.security.running[act.ID] = true
	a.security.mu.Unlock()
	go func() {
		defer func() {
			a.security.mu.Lock()
			delete(a.security.running, act.ID)
			a.security.mu.Unlock()
		}()
		status(agentproto.SecurityActionRunning, "")
		run := a.SecurityRun
		if run == nil {
			run = runSecurityAction
		}
		timeout := time.Hour
		if act.Action == agentproto.SecurityFullScan {
			timeout = 12 * time.Hour
		}
		ctx, cancel := context.WithTimeout(a.runCtx(), timeout)
		msg, err := run(ctx, act)
		cancel()
		if err != nil {
			a.Log.Warn("security action failed", "action", act.Action, "err", err)
			status(agentproto.SecurityActionFailed, truncate(err.Error(), 500))
		} else {
			a.Log.Info("security action done", "action", act.Action)
			status(agentproto.SecurityActionCompleted, truncate(msg, 500))
		}
		rep := a.collectSecurityReport(a.runCtx())
		a.security.deliver(agentproto.TypeSecurityReport, rep)
	}()
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

// normalizeSecurity sorts the lists so reports compare and show stably.
func normalizeSecurity(r *agentproto.SecurityReport) {
	sort.SliceStable(r.Volumes, func(i, j int) bool {
		if r.Volumes[i].System != r.Volumes[j].System {
			return r.Volumes[i].System
		}
		return r.Volumes[i].Mount < r.Volumes[j].Mount
	})
	sort.SliceStable(r.Admins, func(i, j int) bool { return strings.ToLower(r.Admins[i].Name) < strings.ToLower(r.Admins[j].Name) })
	if r.Volumes == nil {
		r.Volumes = []agentproto.SecureVolume{}
	}
	if r.Antivirus == nil {
		r.Antivirus = []agentproto.Antivirus{}
	}
	if r.Firewall == nil {
		r.Firewall = []agentproto.FirewallState{}
	}
	if r.Admins == nil {
		r.Admins = []agentproto.LocalAdmin{}
	}
}

// avProductState decodes the productState of a Windows Security Center
// product: the second byte is the scanner state (0x10 or 0x11 is on), the
// last byte the signatures (0x00 is up to date).
func avProductState(state uint32) (enabled, upToDate bool) {
	scanner := (state >> 8) & 0xff
	return scanner == 0x10 || scanner == 0x11, state&0xff == 0
}
