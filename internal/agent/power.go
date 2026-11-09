package agent

// Power management: waking other devices in this network (Wake-on-LAN for
// the server, which cannot reach a switched-off computer), restarting and
// shutting down this device with a countdown for the logged-on user, and
// preparing the network adapters to be woken.

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/wol"
)

// PowerBackend restarts and shuts down the device.
type PowerBackend interface {
	Restart(ctx context.Context, message string) error
	Shutdown(ctx context.Context, message string) error
	// PrepareWake turns "wake on magic packet" on (and, on Windows, Fast
	// Startup off); off restores what it changed where it can.
	PrepareWake(on bool) error
}

// Prompt kinds of power actions.
const (
	PromptRestart  = "restart"
	PromptShutdown = "shutdown"
)

// powerState is the agent's power bookkeeping.
type powerState struct {
	mu      sync.Mutex
	running int64 // the job being carried out, 0 for none
	prepare *bool // last applied PrepareWake
}

func (a *Agent) powerBackend() PowerBackend {
	if a.Power == nil {
		a.Power = newPlatformPowerBackend()
	}
	return a.Power
}

// powerDelay is how long the agent waits after reporting that it starts a
// restart or shutdown, so the report reaches the server first.
const powerDelay = 3 * time.Second

func (a *Agent) handlePowerWake(e *agentproto.Envelope) {
	var job agentproto.PowerWakeJob
	if err := e.Decode(&job); err != nil || job.ID <= 0 {
		a.Log.Warn("ignoring malformed wake job")
		return
	}
	go func() {
		st := agentproto.PowerStatus{ID: job.ID, Wake: true, State: agentproto.PowerCompleted}
		n, err := wol.Send(job.Targets, job.Port)
		st.Sent = n
		if err != nil {
			st.State, st.Message = agentproto.PowerFailed, err.Error()
		}
		a.Log.Info("wake packets sent", "job", job.ID, "targets", len(job.Targets), "sent", n, "err", err)
		a.emit(agentproto.TypePowerStatus, st)
	}()
}

func (a *Agent) handlePowerAction(e *agentproto.Envelope) {
	var job agentproto.PowerActionJob
	if err := e.Decode(&job); err != nil || job.ID <= 0 || (job.Action != agentproto.PowerRestart && job.Action != agentproto.PowerShutdown) {
		a.Log.Warn("ignoring malformed power job")
		return
	}
	a.power.mu.Lock()
	busy := a.power.running
	if busy == 0 {
		a.power.running = job.ID
	}
	a.power.mu.Unlock()
	if busy != 0 {
		if busy != job.ID {
			a.emit(agentproto.TypePowerStatus, agentproto.PowerStatus{ID: job.ID, State: agentproto.PowerFailed, Message: "another restart or shutdown is waiting"})
		}
		return
	}
	go a.runPowerJob(a.runCtx(), job)
}

func (a *Agent) runPowerJob(ctx context.Context, job agentproto.PowerActionJob) {
	defer func() {
		a.power.mu.Lock()
		a.power.running = 0
		a.power.mu.Unlock()
	}()
	st := agentproto.PowerStatus{ID: job.ID}
	report := func(state, msg string) {
		st.State, st.Message = state, msg
		a.emit(agentproto.TypePowerStatus, st)
	}
	if job.SkipIfUserLoggedOn {
		if u := a.loggedOnUser(); u != "" {
			report(agentproto.PowerSkipped, "a user is logged on: "+u)
			return
		}
	}
	if job.Action == agentproto.PowerRestart && job.OnlyIfRebootPending && !a.patchBackend().RebootPending(ctx) {
		report(agentproto.PowerSkipped, "no restart pending")
		return
	}
	if job.Notify && a.Notify != nil {
		kind := PromptRestart
		if job.Action == agentproto.PowerShutdown {
			kind = PromptShutdown
		}
		cfg := a.patchConfig()
		used, asked := 0, 0
		for {
			asked++
			left := max(job.MaxPostpones-used, 0)
			p := Prompt{
				ID: "power-" + strconv.FormatInt(job.ID, 10) + "-" + strconv.Itoa(asked), Kind: kind,
				CanPostpone: left > 0, PostponesLeft: left, PostponeMinutes: max(job.PostponeMinutes, 1),
				CountdownSeconds: max(job.CountdownSeconds, 10), ProductName: cfg.ProductName, AccentColor: cfg.AccentColor,
			}
			st.PostponedUntil = time.Time{}
			report(agentproto.PowerWaitingUser, "")
			ans, err := a.Notify.Ask(ctx, p)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				if !errors.Is(err, ErrNoUser) {
					a.Log.Warn("user prompt", "err", err)
				}
				break // nobody to ask: go ahead
			}
			if !ans.Postpone || !p.CanPostpone {
				break
			}
			used++
			st.Postpones++
			minute := a.Minute
			if minute <= 0 {
				minute = time.Minute
			}
			wait := time.Duration(p.PostponeMinutes) * minute
			st.PostponedUntil = time.Now().Add(wait).UTC()
			report(agentproto.PowerPostponed, "")
			if !sleep(ctx, wait) {
				return
			}
		}
	}
	st.PostponedUntil = time.Time{}
	report(agentproto.PowerExecuting, "")
	a.Log.Info("power action", "job", job.ID, "action", job.Action)
	delay := powerDelay
	if a.Minute > 0 && a.Minute < time.Second {
		delay = 10 * time.Millisecond // tests
	}
	if !sleep(ctx, delay) {
		return
	}
	be := a.powerBackend()
	var err error
	if job.Action == agentproto.PowerRestart {
		err = be.Restart(ctx, "Your IT department restarts this computer")
	} else {
		err = be.Shutdown(ctx, "Your IT department shuts down this computer")
	}
	if err != nil {
		report(agentproto.PowerFailed, err.Error())
	}
}

func (a *Agent) loggedOnUser() string {
	if a.LoggedOnUser != nil {
		return a.LoggedOnUser()
	}
	return strings.TrimSpace(loggedInUser())
}

func (a *Agent) handlePowerConfig(e *agentproto.Envelope) {
	var c agentproto.PowerConfig
	if err := e.Decode(&c); err != nil {
		return
	}
	a.power.mu.Lock()
	same := a.power.prepare != nil && *a.power.prepare == c.PrepareWake
	if !same {
		v := c.PrepareWake
		a.power.prepare = &v
	}
	a.power.mu.Unlock()
	if same {
		return
	}
	go func() {
		if err := a.powerBackend().PrepareWake(c.PrepareWake); err != nil {
			a.Log.Warn("wake-on-LAN settings", "err", err)
			return
		}
		a.Log.Info("wake-on-LAN settings applied", "prepare", c.PrepareWake)
	}()
}
