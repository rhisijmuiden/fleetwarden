package agent

// Configuration management: the server sends the configurations that apply
// to this device (agentproto.ConfigPolicy). The agent keeps them in the state
// folder, so they also apply while the server cannot be reached, applies them
// and checks them again every few minutes: a setting that was changed is put
// back. What the agent set for a configuration that no longer applies is
// removed. Scripts run on their trigger.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

const (
	configPolicyFile = "config-policy.json"
	configStateFile  = "config-state.json"
	// configUserPoll is how often the agent looks for users who logged on.
	configUserPoll = 30 * time.Second
	// scriptRetries is how often a failed "once" script is tried again.
	scriptRetries     = 3
	configOutputMax   = 8000
	maxRunsPerReport  = 20
	defaultScriptTime = 10 * time.Minute
)

// ConfigUser is a user who is logged on.
type ConfigUser struct {
	Session uint32 `json:"session"`
	Name    string `json:"name"` // DOMAIN\user or user
	SID     string `json:"sid"`
}

func (u ConfigUser) key() string {
	return fmt.Sprintf("%d|%s", u.Session, strings.ToLower(u.SID+"|"+u.Name))
}

// PolicyStore holds the policies of one browser (or registry values).
type PolicyStore interface {
	// Matches reports whether the setting has the value, and whether the
	// setting exists at all.
	Matches(name string, v any) (match, present bool, err error)
	Set(name string, v any) error
	Delete(name string) error
	// Close writes pending changes.
	Close() error
}

// ConfigBackend applies settings on the platform. Tests replace it.
type ConfigBackend interface {
	// BrowserStore opens the policy store of a browser; nil when the
	// platform has none.
	BrowserStore(browser string) (PolicyStore, error)
	// BrowserInstalled reports whether the browser is installed.
	BrowserInstalled(browser string) bool
	// RegistryStore opens the registry of the machine (hive HKLM) or of a
	// user (HKCU with their SID); nil when the platform has none.
	RegistryStore(hive, sid string) (PolicyStore, error)
	// DriveStore opens the network drives of a logged-on user: setting
	// names are drive letters, values DriveValue; nil when the platform has
	// none.
	DriveStore(user ConfigUser) (PolicyStore, error)
	// PrinterStore opens the printers of the device (user nil) or of a
	// logged-on user: setting names are printerName, values Printer, and for
	// a user "default" with the name of the default printer; nil when the
	// platform has none.
	PrinterStore(user *ConfigUser) (PolicyStore, error)
	// Users lists the logged-on users.
	Users() []ConfigUser
	// RunScript runs a script as SYSTEM/root, or as the user when user is set.
	RunScript(ctx context.Context, s agentproto.ScriptConfig, user *ConfigUser) agentproto.ScriptRun
}

// configState is what the agent remembers between runs.
type configState struct {
	// Managed holds what the agent set: store key -> setting -> value hash.
	Managed map[string]map[string]string `json:"managed"`
	// Scripts holds per configuration and target (machine or user) when a
	// script last ran.
	Scripts map[string]scriptState `json:"scripts"`
	// Sessions are the user sessions seen, for logon scripts.
	Sessions []string `json:"sessions"`
}

type scriptState struct {
	Revision int       `json:"revision"`
	At       time.Time `json:"at"`
	Boot     time.Time `json:"boot"`
	OK       bool      `json:"ok"`
	Tries    int       `json:"tries"`
}

// configRunner is the agent's configuration bookkeeping.
type configRunner struct {
	mu      sync.Mutex
	policy  agentproto.ConfigPolicy
	loaded  bool
	wake    chan struct{}
	runs    map[int64][]agentproto.ScriptRun // not reported yet
	last    *agentproto.ConfigStatus
	backend ConfigBackend
}

func (a *Agent) configBackend() ConfigBackend {
	a.config.mu.Lock()
	defer a.config.mu.Unlock()
	if a.Config != nil {
		return a.Config
	}
	if a.config.backend == nil {
		a.config.backend = newPlatformConfigBackend(a.Log)
	}
	return a.config.backend
}

func (a *Agent) configWake() chan struct{} {
	a.config.mu.Lock()
	defer a.config.mu.Unlock()
	if a.config.wake == nil {
		a.config.wake = make(chan struct{}, 1)
	}
	return a.config.wake
}

func (a *Agent) wakeConfig() {
	select {
	case a.configWake() <- struct{}{}:
	default:
	}
}

func (a *Agent) loadConfigPolicy() agentproto.ConfigPolicy {
	a.config.mu.Lock()
	defer a.config.mu.Unlock()
	if !a.config.loaded {
		a.config.loaded = true
		if raw, err := os.ReadFile(filepath.Join(a.stateDir(), configPolicyFile)); err == nil {
			_ = json.Unmarshal(raw, &a.config.policy)
		}
	}
	return a.config.policy
}

// handleConfigPolicy stores a new policy and applies it.
func (a *Agent) handleConfigPolicy(e *agentproto.Envelope) {
	var p agentproto.ConfigPolicy
	if err := e.Decode(&p); err != nil {
		a.Log.Warn("configuration policy", "err", err)
		return
	}
	a.loadConfigPolicy()
	a.config.mu.Lock()
	changed := p.Hash() != a.config.policy.Hash()
	a.config.policy = p
	a.config.mu.Unlock()
	if changed {
		if err := a.saveConfigFile(configPolicyFile, p); err != nil {
			a.Log.Warn("saving the configuration policy", "err", err)
		}
		a.Log.Info("configuration policy received", "configurations", len(p.Items))
	}
	a.wakeConfig()
}

// saveConfigFile writes a state file, creating the state folder.
func (a *Agent) saveConfigFile(name string, v any) error {
	if err := os.MkdirAll(a.stateDir(), 0o700); err != nil {
		return err
	}
	return writeJSONFile(filepath.Join(a.stateDir(), name), v)
}

func (a *Agent) loadConfigState() configState {
	var st configState
	if raw, err := os.ReadFile(filepath.Join(a.stateDir(), configStateFile)); err == nil {
		_ = json.Unmarshal(raw, &st)
	}
	if st.Managed == nil {
		st.Managed = map[string]map[string]string{}
	}
	if st.Scripts == nil {
		st.Scripts = map[string]scriptState{}
	}
	return st
}

// runConfigLoop applies the policy at start, when it changes, when the
// server asks, every ReapplyMinutes and when a user logs on.
func (a *Agent) runConfigLoop(ctx context.Context) {
	wake := a.configWake()
	poll := time.NewTicker(configUserPoll)
	defer poll.Stop()
	var next time.Time
	seen := map[string]bool{}
	first := true
	for {
		p := a.loadConfigPolicy()
		users := a.configBackend().Users()
		var fresh []ConfigUser
		cur := map[string]bool{}
		for _, u := range users {
			cur[u.key()] = true
			if !seen[u.key()] {
				fresh = append(fresh, u)
			}
		}
		seen = cur
		if first || !time.Now().Before(next) || len(fresh) > 0 {
			if len(p.Items) > 0 || first || a.configHasManaged() {
				a.applyConfig(ctx, p, users, fresh)
			}
			next = time.Now().Add(time.Duration(reapply(p.ReapplyMinutes)) * a.minute())
			first = false
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
			next = time.Time{}
		case <-poll.C:
		}
	}
}

func reapply(m int) int {
	if m < 5 {
		return agentproto.DefaultReapplyMinutes
	}
	return m
}

func (a *Agent) minute() time.Duration {
	if a.Minute > 0 {
		return a.Minute
	}
	return time.Minute
}

func (a *Agent) configHasManaged() bool {
	st := a.loadConfigState()
	for _, m := range st.Managed {
		if len(m) > 0 {
			return true
		}
	}
	return false
}

// handleConfigApply runs the configurations now.
func (a *Agent) handleConfigApply() { a.wakeConfig() }

func valueHash(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

// itemResult collects the outcome of one configuration in a run.
type itemResult struct {
	state    string
	messages []string
	repaired int
}

func (r *itemResult) fail(msg string) {
	r.state = agentproto.ConfigFailed
	r.messages = append(r.messages, msg)
}

func (r *itemResult) note(msg string) { r.messages = append(r.messages, msg) }

// syncStore makes a store hold want for the settings this agent manages
// under key: it sets what differs and removes what it set before and is no
// longer wanted, except settings in keep, which it only forgets. It returns
// the number of settings it set before that had been changed since.
func syncStore(st *configState, key string, s PolicyStore, want map[string]any, keep map[string]bool) (int, error) {
	managed := st.Managed[key]
	next := map[string]string{}
	repaired := 0
	var errs []string
	names := make([]string, 0, len(want))
	for n := range want {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		v := want[name]
		h := valueHash(v)
		match, _, err := s.Matches(name, v)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			if managed[name] != "" {
				next[name] = managed[name]
			}
			continue
		}
		if !match {
			if managed[name] == h {
				repaired++ // the agent set this value, and someone changed it
			}
			if err := s.Set(name, v); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", name, err))
				if managed[name] != "" {
					next[name] = managed[name]
				}
				continue
			}
		}
		next[name] = h
	}
	for name := range managed {
		if _, ok := want[name]; ok || keep[name] {
			continue
		}
		if err := s.Delete(name); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			next[name] = managed[name]
		}
	}
	if err := s.Close(); err != nil {
		errs = append(errs, err.Error())
	}
	if len(next) == 0 {
		delete(st.Managed, key)
	} else {
		st.Managed[key] = next
	}
	if len(errs) > 0 {
		return repaired, errors.New(strings.Join(errs, "; "))
	}
	return repaired, nil
}

// applyConfig runs every configuration once and reports the result.
func (a *Agent) applyConfig(ctx context.Context, p agentproto.ConfigPolicy, users, fresh []ConfigUser) {
	be := a.configBackend()
	st := a.loadConfigState()
	res := map[int64]*itemResult{}
	for _, it := range p.Items {
		res[it.ID] = &itemResult{state: agentproto.ConfigApplied}
	}

	a.applyBrowsers(be, &st, p.Items, res)
	a.applyRegistry(be, &st, p.Items, users, res)
	a.applyDrives(be, &st, p.Items, users, res)
	a.applyPrinters(be, &st, p.Items, users, res)
	a.runScripts(ctx, be, &st, p.Items, users, fresh, res)

	if err := a.saveConfigFile(configStateFile, st); err != nil {
		a.Log.Warn("saving the configuration state", "err", err)
	}
	now := time.Now().UTC()
	status := agentproto.ConfigStatus{Policy: p.Hash(), At: now, Items: []agentproto.ConfigItemStatus{}}
	a.config.mu.Lock()
	for _, it := range p.Items {
		r := res[it.ID]
		is := agentproto.ConfigItemStatus{ID: it.ID, Revision: it.Revision, State: r.state, Repaired: r.repaired,
			Message: tailString(strings.Join(r.messages, "\n"), 1000), Runs: a.config.runs[it.ID]}
		if r.state == agentproto.ConfigApplied {
			is.AppliedAt = now
		}
		if len(is.Runs) > maxRunsPerReport {
			is.Runs = is.Runs[len(is.Runs)-maxRunsPerReport:]
		}
		status.Items = append(status.Items, is)
	}
	a.config.runs = nil
	a.config.last = &status
	a.config.mu.Unlock()
	a.emit(agentproto.TypeConfigStatus, status)
	a.Log.Info("configurations applied", "configurations", len(p.Items))
}

func (a *Agent) applyBrowsers(be ConfigBackend, st *configState, items []agentproto.ConfigItem, res map[int64]*itemResult) {
	want, from := browserPolicies(items)
	for _, b := range []string{agentproto.BrowserChrome, agentproto.BrowserEdge, agentproto.BrowserFirefox} {
		key := "browser:" + b
		if len(want[b]) == 0 && len(st.Managed[key]) == 0 {
			continue
		}
		s, err := be.BrowserStore(b)
		if s == nil || err != nil {
			for _, id := range from[b] {
				if err != nil {
					res[id].fail(fmt.Sprintf("%s: %v", browserLabel(b), err))
				} else {
					res[id].state = agentproto.ConfigUnsupported
					res[id].note(browserLabel(b) + " policies are not supported on this system")
				}
			}
			continue
		}
		repaired, err := syncStore(st, key, s, want[b], nil)
		installed := be.BrowserInstalled(b)
		for _, id := range from[b] {
			r := res[id]
			r.repaired += repaired
			if err != nil {
				r.fail(fmt.Sprintf("%s: %v", browserLabel(b), err))
			} else if !installed {
				r.note(browserLabel(b) + " is not installed; the policies apply when it is")
			}
		}
	}
}

func browserLabel(b string) string {
	switch b {
	case agentproto.BrowserChrome:
		return "Chrome"
	case agentproto.BrowserEdge:
		return "Edge"
	case agentproto.BrowserFirefox:
		return "Firefox"
	}
	return b
}

// regName is the setting name of a registry value in a store.
func regName(v agentproto.RegistryValue) string { return strings.TrimLeft(v.Key, `\`) + `\\` + v.Name }

// RegValue is how a registry setting travels to a PolicyStore.
type RegValue struct {
	Key   string
	Name  string
	Type  string
	Value string
}

// regValueOf splits a store setting back into key and value name.
func regValueOf(name string) (key, value string) {
	i := strings.Index(name, `\\`)
	if i < 0 {
		return name, ""
	}
	return name[:i], name[i+2:]
}

func (a *Agent) applyRegistry(be ConfigBackend, st *configState, items []agentproto.ConfigItem, users []ConfigUser, res map[int64]*itemResult) {
	machine := map[string]any{}
	user := map[string]any{}
	// Values of configurations with Keep stay when they are no longer
	// configured. The set is remembered from the previous run, when the
	// configuration that is gone now was still there.
	keepNext := map[string]string{}
	var machineIDs, userIDs []int64
	for _, it := range items {
		if it.Type != agentproto.ConfigRegistry || it.Registry == nil {
			continue
		}
		for _, v := range it.Registry.Values {
			name := regName(v)
			val := RegValue{Key: v.Key, Name: v.Name, Type: v.Type, Value: v.Value}
			if v.Hive == agentproto.HiveUser {
				user[name] = val
				userIDs = appendOnce(userIDs, it.ID)
			} else {
				machine[name] = val
				machineIDs = appendOnce(machineIDs, it.ID)
			}
			if it.Registry.Keep {
				keepNext[v.Hive+"|"+name] = "1"
			}
		}
	}
	keepPrev := st.Managed[keepKey]
	keepFor := func(hive string) map[string]bool {
		out := map[string]bool{}
		for _, m := range []map[string]string{keepPrev, keepNext} {
			for k := range m {
				if h, name, _ := strings.Cut(k, "|"); h == hive {
					out[name] = true
				}
			}
		}
		return out
	}
	apply := func(key, hive, sid string, want map[string]any, ids []int64) {
		if len(want) == 0 && len(st.Managed[key]) == 0 {
			return
		}
		s, err := be.RegistryStore(hive, sid)
		if s == nil || err != nil {
			for _, id := range ids {
				if err != nil {
					res[id].fail(err.Error())
				} else {
					res[id].state = agentproto.ConfigUnsupported
					res[id].note("the registry is not supported on this system")
				}
			}
			return
		}
		repaired, err := syncStore(st, key, s, want, keepFor(hive))
		for _, id := range ids {
			res[id].repaired += repaired
			if err != nil {
				res[id].fail(err.Error())
			}
		}
	}
	apply("registry:HKLM", agentproto.HiveMachine, "", machine, machineIDs)
	seen := map[string]bool{}
	for _, u := range users {
		if u.SID == "" || seen[u.SID] {
			continue
		}
		seen[u.SID] = true
		apply("registry:HKCU:"+u.SID, agentproto.HiveUser, u.SID, user, userIDs)
	}
	if len(userIDs) > 0 && len(seen) == 0 {
		for _, id := range userIDs {
			res[id].note("user settings apply when a user logs on")
		}
	}
	if len(keepNext) > 0 {
		st.Managed[keepKey] = keepNext
	} else {
		delete(st.Managed, keepKey)
	}
}

const keepKey = "keep:registry"

func appendOnce(ids []int64, id int64) []int64 {
	if slices.Contains(ids, id) {
		return ids
	}
	return append(ids, id)
}

func (a *Agent) runScripts(ctx context.Context, be ConfigBackend, st *configState, items []agentproto.ConfigItem, users, fresh []ConfigUser, res map[int64]*itemResult) {
	boot := a.bootTime()
	now := time.Now()
	// Sessions the agent saw before a restart are not new logons.
	known := map[string]bool{}
	for _, k := range st.Sessions {
		known[k] = true
	}
	newLogon := map[string]bool{}
	for _, u := range fresh {
		if !known[u.key()] {
			newLogon[u.key()] = true
		}
	}
	st.Sessions = st.Sessions[:0]
	for _, u := range users {
		st.Sessions = append(st.Sessions, u.key())
	}
	for _, it := range items {
		if it.Type != agentproto.ConfigScript || it.Script == nil {
			continue
		}
		sc := *it.Script
		r := res[it.ID]
		targets := []*ConfigUser{nil}
		if sc.RunAs == agentproto.RunAsUser {
			targets = targets[:0]
			for i := range users {
				targets = append(targets, &users[i])
			}
			if len(targets) == 0 {
				r.state = agentproto.ConfigWaiting
				r.note("waits for a user to log on")
				continue
			}
		}
		failed := 0
		for _, u := range targets {
			key := scriptKey(it.ID, u)
			prev, had := st.Scripts[key]
			changed := !had || prev.Revision != it.Revision
			retry := !changed && !prev.OK && prev.Tries < scriptRetries
			due := false
			switch sc.Trigger {
			case agentproto.TriggerOnce:
				due = changed || retry
			case agentproto.TriggerStartup:
				due = changed || !sameBoot(prev.Boot, boot) || retry
			case agentproto.TriggerLogon:
				if u != nil {
					due = changed || newLogon[u.key()]
				} else {
					due = len(newLogon) > 0 || (changed && len(users) > 0)
					if !due && changed {
						// Wait for the next logon.
						st.Scripts[key] = scriptState{Revision: it.Revision, Boot: boot, OK: true}
					}
				}
			case agentproto.TriggerInterval:
				every := time.Duration(max(sc.IntervalMinutes, 5)) * a.minute()
				due = changed || now.Sub(prev.At) >= every
			}
			if !due {
				if !changed && !prev.OK {
					failed++
				}
				continue
			}
			tries := 1
			if !changed && !prev.OK {
				tries = prev.Tries + 1
			}
			timeout := time.Duration(sc.TimeoutMinutes) * time.Minute
			if timeout <= 0 {
				timeout = defaultScriptTime
			}
			sctx, cancel := context.WithTimeout(ctx, timeout)
			run := be.RunScript(sctx, sc, u)
			cancel()
			if run.At.IsZero() {
				run.At = time.Now().UTC()
			}
			run.Output = tailString(run.Output, configOutputMax)
			ok := run.Error == "" && run.ExitCode == 0
			st.Scripts[key] = scriptState{Revision: it.Revision, At: now, Boot: boot, OK: ok, Tries: tries}
			a.config.mu.Lock()
			if a.config.runs == nil {
				a.config.runs = map[int64][]agentproto.ScriptRun{}
			}
			a.config.runs[it.ID] = append(a.config.runs[it.ID], run)
			a.config.mu.Unlock()
			if !ok {
				failed++
			}
			a.Log.Info("configuration script ran", "configuration", it.Name, "user", run.User, "exit", run.ExitCode, "err", run.Error)
		}
		if failed > 0 {
			r.fail(fmt.Sprintf("the script failed (%d of %d)", failed, len(targets)))
		}
	}
	// Forget scripts that are no longer configured. Records of users who
	// logged off stay, so a "once" script does not run twice for them.
	for k := range st.Scripts {
		id, _, _ := strings.Cut(k, "|")
		if !scriptConfigured(items, id) {
			delete(st.Scripts, k)
		}
	}
}

func scriptKey(id int64, u *ConfigUser) string {
	key := strconv.FormatInt(id, 10)
	if u != nil {
		key += "|" + strings.ToLower(u.SID+"|"+u.Name)
	}
	return key
}

func scriptConfigured(items []agentproto.ConfigItem, id string) bool {
	for _, it := range items {
		if it.Type == agentproto.ConfigScript && strconv.FormatInt(it.ID, 10) == id {
			return true
		}
	}
	return false
}

func sameBoot(a, b time.Time) bool {
	d := a.Sub(b)
	return d > -2*time.Minute && d < 2*time.Minute
}

// syncFor applies want to one store and records the outcome on the
// configurations in ids. A nil store means the platform has none.
func syncFor(st *configState, key string, open func() (PolicyStore, error), want map[string]any, ids []int64, res map[int64]*itemResult, what string) {
	if len(want) == 0 && len(st.Managed[key]) == 0 {
		return
	}
	s, err := open()
	if s == nil || err != nil {
		for _, id := range ids {
			if err != nil {
				res[id].fail(err.Error())
			} else {
				res[id].state = agentproto.ConfigUnsupported
				res[id].note(what + " are not supported on this system")
			}
		}
		return
	}
	repaired, err := syncStore(st, key, s, want, nil)
	for _, id := range ids {
		res[id].repaired += repaired
		if err != nil {
			res[id].fail(err.Error())
		}
	}
}

// DriveValue is how a network drive travels to a PolicyStore.
type DriveValue struct {
	Path  string `json:"path"` // with the user's name filled in
	Label string `json:"label,omitempty"`
}

// expandUser fills in %USERNAME% and %USERDOMAIN% for a user.
func expandUser(s string, u ConfigUser) string {
	dom, name, ok := strings.Cut(u.Name, `\`)
	if !ok {
		dom, name = "", u.Name
	}
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '%')
		if i < 0 {
			break
		}
		j := strings.IndexByte(s[i+1:], '%')
		if j < 0 {
			break
		}
		v := s[i+1 : i+1+j]
		switch strings.ToUpper(v) {
		case "USERNAME":
			b.WriteString(s[:i] + name)
		case "USERDOMAIN":
			b.WriteString(s[:i] + dom)
		default:
			b.WriteString(s[:i+1+j])
			s = s[i+1+j:]
			continue
		}
		s = s[i+2+j:]
	}
	b.WriteString(s)
	return b.String()
}

func (a *Agent) applyDrives(be ConfigBackend, st *configState, items []agentproto.ConfigItem, users []ConfigUser, res map[int64]*itemResult) {
	var ids []int64
	var drives []agentproto.DriveMapping
	for _, it := range items {
		if it.Type != agentproto.ConfigDrives || it.Drives == nil {
			continue
		}
		ids = append(ids, it.ID)
		drives = append(drives, it.Drives.Drives...) // a later one wins
	}
	seen := map[string]bool{}
	for _, u := range users {
		if u.SID == "" || seen[u.SID] {
			continue
		}
		seen[u.SID] = true
		want := map[string]any{}
		for _, d := range drives {
			want[strings.ToUpper(d.Letter)] = DriveValue{Path: expandUser(d.Path, u), Label: d.Label}
		}
		syncFor(st, "drives:"+u.SID, func() (PolicyStore, error) { return be.DriveStore(u) }, want, ids, res, "network drives")
	}
	if len(ids) > 0 && len(seen) == 0 {
		for _, id := range ids {
			res[id].state = agentproto.ConfigWaiting
			res[id].note("network drives are mapped when a user logs on")
		}
	}
}

// defaultPrinterName is the setting of a user's default printer. It sorts
// after the printers, so they are there when it is set.
const defaultPrinterName = "~default"

// printerName is the setting name of a printer in a store.
func printerName(p agentproto.Printer) string {
	if p.Kind == agentproto.PrinterShared {
		return "shared:" + strings.ToLower(p.Path)
	}
	return "ip:" + strings.ToLower(p.Name)
}

// PrinterLabel is the name Windows shows for a printer.
func PrinterLabel(p agentproto.Printer) string {
	if p.Kind == agentproto.PrinterShared {
		return p.Path
	}
	return p.Name
}

func (a *Agent) applyPrinters(be ConfigBackend, st *configState, items []agentproto.ConfigItem, users []ConfigUser, res map[int64]*itemResult) {
	machine := map[string]any{}
	perUser := map[string]any{}
	var machineIDs, userIDs []int64
	for _, it := range items {
		if it.Type != agentproto.ConfigPrinters || it.Printers == nil {
			continue
		}
		for _, p := range it.Printers.Printers {
			if p.Kind == agentproto.PrinterShared && p.Scope == agentproto.PrinterUser {
				perUser[printerName(p)] = p
				userIDs = appendOnce(userIDs, it.ID)
			} else {
				machine[printerName(p)] = p
				machineIDs = appendOnce(machineIDs, it.ID)
			}
			if p.Default {
				perUser[defaultPrinterName] = PrinterLabel(p)
				userIDs = appendOnce(userIDs, it.ID)
			}
		}
	}
	syncFor(st, "printers:machine", func() (PolicyStore, error) { return be.PrinterStore(nil) }, machine, machineIDs, res, "printers")
	seen := map[string]bool{}
	for _, u := range users {
		if u.SID == "" || seen[u.SID] {
			continue
		}
		seen[u.SID] = true
		syncFor(st, "printers:"+u.SID, func() (PolicyStore, error) { return be.PrinterStore(&u) }, perUser, userIDs, res, "printers")
	}
	if len(userIDs) > 0 && len(seen) == 0 {
		for _, id := range userIDs {
			res[id].note("printers of the users are added when a user logs on")
		}
	}
	if sw, ok := be.(interface{ SweepPrinterPorts() }); ok {
		sw.SweepPrinterPorts()
	}
}
