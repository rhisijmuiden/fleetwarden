package agent

// Patch jobs: the server sends one install job at a time. The agent asks the
// logged-on user first when the policy says so, installs, and restarts the
// device according to the reboot policy. The job is written to the state
// folder, so the agent can report the outcome after the restart (or after it
// was stopped in the middle of a job).

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// PatchBackend talks to the platform's update system (Windows Update, apt).
type PatchBackend interface {
	Scan(ctx context.Context) (agentproto.PatchScanResult, error)
	// Install installs the updates with these IDs and reports per update
	// whether it succeeded and whether a restart is needed.
	Install(ctx context.Context, ids []string, progress func(msg string)) ([]agentproto.PatchResult, bool, error)
	RebootPending(ctx context.Context) bool
	Reboot(ctx context.Context, message string) error
	// Configure applies the policy settings the agent enforces itself.
	Configure(cfg agentproto.PatchConfig) error
}

// Prompt kinds.
const (
	PromptInstall = "install"
	PromptReboot  = "reboot"
)

// Prompt is a question for the logged-on user (the pop-up on Windows).
type Prompt struct {
	ID               string `json:"id"`
	Kind             string `json:"kind"`
	Updates          int    `json:"updates"`
	CanPostpone      bool   `json:"canPostpone"`
	PostponesLeft    int    `json:"postponesLeft"`
	PostponeMinutes  int    `json:"postponeMinutes"`
	CountdownSeconds int    `json:"countdownSeconds"`
	ProductName      string `json:"productName"`
	AccentColor      string `json:"accentColor"`
}

// Answer is the user's choice. Without a choice before the countdown ends
// the job goes ahead.
type Answer struct {
	Postpone bool `json:"postpone"`
}

// Notifier shows prompts to the logged-on users.
type Notifier interface {
	// Ask shows the prompt and waits for an answer or the end of the
	// countdown. It returns ErrNoUser when nobody can be asked.
	Ask(ctx context.Context, p Prompt) (Answer, error)
}

// ErrNoUser means nobody is logged on (or no pop-up app runs) to ask.
var ErrNoUser = errors.New("no logged-on user to ask")

const (
	patchJobFile     = "patch-job.json"
	patchScanTimeout = 45 * time.Minute
	patchJobTimeout  = 6 * time.Hour
	// A restart that did not happen within this time failed.
	patchRebootGrace = 30 * time.Minute
)

// patchJob is the job as written to the state folder.
type patchJob struct {
	Job       agentproto.PatchInstall `json:"job"`
	Status    agentproto.PatchStatus  `json:"status"`
	BootTime  time.Time               `json:"bootTime"`
	UpdatedAt time.Time               `json:"updatedAt"`
}

// patchState is the agent's patch bookkeeping.
type patchState struct {
	mu       sync.Mutex
	sendFn   func(string, any) bool // send on the current session; nil while offline
	gen      uint64                 // session generation of sendFn
	running  bool
	last     *agentproto.PatchStatus // last status of the running job
	pending  []pendingMsg            // messages that could not be sent yet
	scanning bool
	cfg      agentproto.PatchConfig
}

type pendingMsg struct {
	typ string
	v   any
}

// ScanPatches lists the missing updates of this machine (the patch-scan command).
func ScanPatches(ctx context.Context) (agentproto.PatchScanResult, error) {
	return newPlatformPatchBackend().Scan(ctx)
}

func (a *Agent) patchBackend() PatchBackend {
	if a.Patches == nil {
		a.Patches = newPlatformPatchBackend()
	}
	return a.Patches
}

// attachPatchSession is called when a session starts: queued messages and
// the status of a running job are sent on it.
func (a *Agent) attachPatchSession(send func(string, any) bool) uint64 {
	p := &a.patch
	p.mu.Lock()
	p.gen++
	gen := p.gen
	p.sendFn = send
	queued := p.pending
	p.pending = nil
	last := p.last
	running := p.running
	p.mu.Unlock()
	for _, m := range queued {
		send(m.typ, m.v)
	}
	if running && last != nil {
		send(agentproto.TypePatchStatus, *last)
	}
	return gen
}

func (a *Agent) detachPatchSession(gen uint64) {
	a.patch.mu.Lock()
	if a.patch.gen == gen {
		a.patch.sendFn = nil
	}
	a.patch.mu.Unlock()
}

// emit sends a message on the current session or queues it for the next one.
// Only the newest scan result and job status are kept while offline.
func (a *Agent) emit(typ string, v any) {
	p := &a.patch
	p.mu.Lock()
	fn := p.sendFn
	p.mu.Unlock()
	if fn != nil && fn(typ, v) {
		return
	}
	p.mu.Lock()
	kept := p.pending[:0]
	for _, m := range p.pending {
		if m.typ != typ {
			kept = append(kept, m)
		}
	}
	p.pending = append(kept, pendingMsg{typ, v})
	p.mu.Unlock()
}

func (a *Agent) handlePatchConfig(e *agentproto.Envelope) {
	var c agentproto.PatchConfig
	if err := e.Decode(&c); err != nil {
		return
	}
	a.patch.mu.Lock()
	a.patch.cfg = c
	a.patch.mu.Unlock()
	if err := a.patchBackend().Configure(c); err != nil {
		a.Log.Warn("patch settings", "err", err)
	}
}

func (a *Agent) patchConfig() agentproto.PatchConfig {
	a.patch.mu.Lock()
	defer a.patch.mu.Unlock()
	return a.patch.cfg
}

// handlePatchScan scans in the background; a scan during a job is skipped
// because the job ends with a scan of its own.
func (a *Agent) handlePatchScan(ctx context.Context) {
	a.patch.mu.Lock()
	busy := a.patch.scanning || a.patch.running
	if !busy {
		a.patch.scanning = true
	}
	a.patch.mu.Unlock()
	if busy {
		return
	}
	go func() {
		defer func() {
			a.patch.mu.Lock()
			a.patch.scanning = false
			a.patch.mu.Unlock()
		}()
		a.scanAndSend(ctx)
	}()
}

func (a *Agent) scanAndSend(ctx context.Context) {
	sctx, cancel := context.WithTimeout(ctx, patchScanTimeout)
	defer cancel()
	res, err := a.patchBackend().Scan(sctx)
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		res.Error = err.Error()
		a.Log.Warn("patch scan", "err", err)
	}
	if res.ScannedAt.IsZero() {
		res.ScannedAt = time.Now().UTC()
	}
	if res.Updates == nil {
		res.Updates = []agentproto.PatchUpdate{}
	}
	a.emit(agentproto.TypePatchScanResult, res)
	a.Log.Info("patch scan", "missing", len(res.Updates), "rebootPending", res.RebootPending)
}

var patchIDRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+.:_\-]{0,127}$`)

func (a *Agent) handlePatchInstall(e *agentproto.Envelope) {
	var job agentproto.PatchInstall
	if err := e.Decode(&job); err != nil || job.RunID <= 0 || !agentproto.ValidReboot(job.Reboot) {
		a.Log.Warn("ignoring malformed patch job")
		return
	}
	for _, id := range job.Updates {
		if !patchIDRE.MatchString(id) {
			a.emit(agentproto.TypePatchStatus, agentproto.PatchStatus{RunID: job.RunID, State: agentproto.PatchFailed, Message: "invalid update id"})
			return
		}
	}
	a.patch.mu.Lock()
	if a.patch.running {
		same := a.patch.last != nil && a.patch.last.RunID == job.RunID
		a.patch.mu.Unlock()
		if !same {
			a.emit(agentproto.TypePatchStatus, agentproto.PatchStatus{RunID: job.RunID, State: agentproto.PatchFailed, Message: "another patch job is running"})
		}
		return
	}
	a.patch.running = true
	a.patch.mu.Unlock()
	go a.runPatchJob(a.runCtx(), job)
}

type patchRun struct {
	a     *Agent
	job   agentproto.PatchInstall
	st    agentproto.PatchStatus
	boot  time.Time
	asked int // prompts shown, for prompt IDs
}

func (r *patchRun) report(state, msg string) {
	r.st.State, r.st.Message = state, msg
	st := r.st
	st.Results = append([]agentproto.PatchResult(nil), r.st.Results...)
	r.a.patch.mu.Lock()
	r.a.patch.last = &st
	r.a.patch.mu.Unlock()
	if !agentproto.PatchFinished(state) {
		_ = writeJSONFile(filepath.Join(r.a.stateDir(), patchJobFile), patchJob{Job: r.job, Status: st, BootTime: r.boot, UpdatedAt: time.Now().UTC()})
	}
	r.a.emit(agentproto.TypePatchStatus, st)
}

func (a *Agent) runPatchJob(ctx context.Context, job agentproto.PatchInstall) {
	ctx, cancel := context.WithTimeout(ctx, patchJobTimeout)
	defer cancel()
	r := &patchRun{a: a, job: job, st: agentproto.PatchStatus{RunID: job.RunID}, boot: a.bootTime()}
	finished := false
	defer func() {
		if finished {
			_ = os.Remove(filepath.Join(a.stateDir(), patchJobFile))
		}
		a.patch.mu.Lock()
		a.patch.running = false
		a.patch.last = nil
		a.patch.mu.Unlock()
	}()
	finish := func(state, msg string) {
		r.report(state, msg)
		finished = true
		a.Log.Info("patch job finished", "run", job.RunID, "state", state, "message", msg)
		a.scanAndSend(ctx)
	}
	be := a.patchBackend()
	a.Log.Info("patch job", "run", job.RunID, "updates", len(job.Updates), "reboot", job.Reboot)

	if len(job.Updates) > 0 {
		if !r.askUser(ctx, PromptInstall) {
			return // the agent stops; the next start reports the job as interrupted
		}
		r.report(agentproto.PatchInstalling, "")
		var lastProgress time.Time
		a.installMu.Lock()
		results, reboot, err := be.Install(ctx, job.Updates, func(msg string) {
			if time.Since(lastProgress) > 5*time.Second {
				lastProgress = time.Now()
				r.report(agentproto.PatchInstalling, msg)
			}
		})
		a.installMu.Unlock()
		if ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
			return
		}
		r.st.Results = results
		r.st.RebootRequired = reboot || be.RebootPending(ctx)
		if err != nil {
			finish(agentproto.PatchFailed, err.Error())
			return
		}
	} else {
		r.st.RebootRequired = be.RebootPending(ctx)
	}
	failed := 0
	for _, res := range r.st.Results {
		if !res.OK {
			failed++
		}
	}
	outcome, msg := agentproto.PatchCompleted, ""
	if failed > 0 {
		outcome, msg = agentproto.PatchFailed, strconv.Itoa(failed)+" update(s) failed"
	}
	if !r.st.RebootRequired || job.Reboot == agentproto.RebootNever {
		finish(outcome, msg)
		return
	}
	if !r.askUser(ctx, PromptReboot) {
		return
	}
	r.report(agentproto.PatchRebooting, msg)
	if err := be.Reboot(ctx, "Restarting to finish installing updates"); err != nil {
		finish(agentproto.PatchFailed, "restart failed: "+err.Error())
		return
	}
	// The device restarts; the next start reports the outcome.
}

// askUser shows a prompt when someone is logged on: before installing when
// the policy asks for that, and always before a restart (a question with
// "ask the user", a countdown with "restart automatically"). It returns
// false when the agent is stopping.
func (r *patchRun) askUser(ctx context.Context, kind string) bool {
	n := r.a.Notify
	if n == nil || (kind == PromptInstall && !r.job.Notify) {
		return true
	}
	canPostpone := kind == PromptInstall || r.job.Reboot == agentproto.RebootPrompt
	cfg := r.a.patchConfig()
	used := 0
	for {
		r.asked++
		left := max(r.job.MaxPostpones-used, 0)
		p := Prompt{
			ID: strconv.FormatInt(r.job.RunID, 10) + "-" + strconv.Itoa(r.asked), Kind: kind, Updates: len(r.job.Updates),
			CanPostpone: canPostpone && left > 0, PostponesLeft: left, PostponeMinutes: max(r.job.PostponeMinutes, 1),
			CountdownSeconds: max(r.job.CountdownSeconds, 10), ProductName: cfg.ProductName, AccentColor: cfg.AccentColor,
		}
		r.st.PostponedUntil = time.Time{}
		r.report(agentproto.PatchWaitingUser, kind)
		ans, err := n.Ask(ctx, p)
		if ctx.Err() != nil {
			return false
		}
		if err != nil {
			if !errors.Is(err, ErrNoUser) {
				r.a.Log.Warn("user prompt", "err", err)
			}
			return true // nobody to ask: go ahead
		}
		if !ans.Postpone || !p.CanPostpone {
			return true
		}
		used++
		r.st.Postpones++
		minute := r.a.Minute
		if minute <= 0 {
			minute = time.Minute
		}
		wait := time.Duration(p.PostponeMinutes) * minute
		r.st.PostponedUntil = time.Now().Add(wait).UTC()
		r.report(agentproto.PatchPostponed, kind)
		if !sleep(ctx, wait) {
			return false
		}
	}
}

// resumePatchJob looks at a job left in the state folder by an earlier run:
// after the restart it asked for, the job is complete; otherwise the agent
// was stopped in the middle of it.
func (a *Agent) resumePatchJob(ctx context.Context) {
	path := filepath.Join(a.stateDir(), patchJobFile)
	var j patchJob
	if !readJSONFile(path, &j) || j.Job.RunID == 0 {
		_ = os.Remove(path)
		return
	}
	st := j.Status
	st.RunID = j.Job.RunID
	st.PostponedUntil = time.Time{}
	booted := a.bootTime()
	restarted := !booted.IsZero() && !j.BootTime.IsZero() && booted.After(j.BootTime.Add(time.Minute))
	switch {
	case st.State == agentproto.PatchRebooting && restarted:
		st.RebootRequired = a.patchBackend().RebootPending(ctx)
		failed := 0
		for _, r := range st.Results {
			if !r.OK {
				failed++
			}
		}
		st.State, st.Message = agentproto.PatchCompleted, "restarted"
		if failed > 0 {
			st.State, st.Message = agentproto.PatchFailed, strconv.Itoa(failed)+" update(s) failed; restarted"
		}
	case st.State == agentproto.PatchRebooting && time.Since(j.UpdatedAt) < patchRebootGrace:
		return // the restart is still on its way
	case st.State == agentproto.PatchRebooting:
		st.State, st.Message = agentproto.PatchFailed, "the device did not restart"
	default:
		st.State, st.Message = agentproto.PatchFailed, "the agent stopped during the patch job"
	}
	_ = os.Remove(path)
	a.emit(agentproto.TypePatchStatus, st)
	a.Log.Info("patch job outcome after restart", "run", st.RunID, "state", st.State)
	go a.scanAndSend(ctx)
}

func (a *Agent) bootTime() time.Time {
	if a.BootTime != nil {
		return a.BootTime()
	}
	return bootTime()
}

func readJSONFile(path string, v any) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return json.Unmarshal(raw, v) == nil
}
