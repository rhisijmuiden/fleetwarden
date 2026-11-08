package server

// Patch management. Each device group can have a patch policy: a weekly
// maintenance window, approval rules per update category, a reboot policy
// and user notification settings. Online agents scan regularly; in the
// window the server sends each device one install job with its approved
// updates. Proxmox VE nodes of one cluster are patched one at a time.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

const (
	patchRunsPerTick    = 50               // install jobs started per minute
	patchScansPerTick   = 100              // scan requests per minute
	patchScanRetry      = 30 * time.Minute // a requested scan that did not answer is asked again
	patchRunStale       = 8 * time.Hour    // an open job without news failed
	patchPostponeSlack  = 2 * time.Hour    // extra time for a postponed job
	maxPatchesPerDevice = 5000
)

// ---- policy rules (pure functions) ----

func defaultApprovals() map[string]store.Approval {
	return map[string]store.Approval{
		agentproto.CategorySecurity:   {Mode: store.ApproveAuto},
		agentproto.CategoryCritical:   {Mode: store.ApproveAuto},
		agentproto.CategoryCumulative: {Mode: store.ApproveAuto, DelayDays: 7},
		agentproto.CategoryDefinition: {Mode: store.ApproveAuto},
		agentproto.CategoryDriver:     {Mode: store.ApproveManual},
		agentproto.CategoryFeature:    {Mode: store.ApproveNever},
		agentproto.CategoryOther:      {Mode: store.ApproveAuto, DelayDays: 3},
	}
}

func defaultPatchPolicy() store.PatchPolicy {
	return store.PatchPolicy{
		Enabled: true, WindowDays: 127, WindowStart: 20 * 60, WindowMinutes: 240, Approvals: defaultApprovals(),
		Reboot: agentproto.RebootPrompt, NotifyUsers: true, MaxPostpones: 3, PostponeMinutes: 60, CountdownSeconds: 300,
		TakeOver: true, ScanHours: 6,
	}
}

func approvalRule(p store.PatchPolicy, category string) store.Approval {
	if a, ok := p.Approvals[category]; ok {
		return a
	}
	if a, ok := defaultApprovals()[category]; ok {
		return a
	}
	return store.Approval{Mode: store.ApproveManual}
}

// approval decides whether an update may be installed under a policy: a
// manual decision wins; otherwise the category rule applies, counting the
// delay from the release date (or from when it was first seen).
func approval(p store.PatchPolicy, row store.PatchRow, now time.Time) (bool, *time.Time) {
	switch row.Decision {
	case "approved":
		return true, nil
	case "declined":
		return false, nil
	}
	a := approvalRule(p, row.Category)
	if a.Mode != store.ApproveAuto {
		return false, nil
	}
	base := row.FirstSeenAt
	if row.ReleasedAt != nil && !row.ReleasedAt.IsZero() && row.ReleasedAt.Before(base) {
		base = *row.ReleasedAt
	}
	from := base.Add(time.Duration(a.DelayDays) * 24 * time.Hour)
	return !now.Before(from), &from
}

// serverZoneName names the server's time zone, e.g. Europe/Amsterdam.
func serverZoneName() string {
	if tz := os.Getenv("TZ"); tz != "" {
		return strings.TrimPrefix(tz, ":")
	}
	if l, err := os.Readlink("/etc/localtime"); err == nil {
		if i := strings.Index(l, "zoneinfo/"); i >= 0 {
			return l[i+len("zoneinfo/"):]
		}
	}
	if n := time.Local.String(); n != "Local" {
		return n
	}
	return "UTC"
}

func policyLocation(p store.PatchPolicy) *time.Location {
	if p.TimeZone != "" {
		if loc, err := time.LoadLocation(p.TimeZone); err == nil {
			return loc
		}
	}
	return time.Local
}

// windowOpen reports whether the maintenance window is open at now and when
// this window started. A window may run past midnight.
func windowOpen(p store.PatchPolicy, now time.Time) (time.Time, bool) {
	loc := policyLocation(p)
	t := now.In(loc)
	for back := 0; back <= 1; back++ {
		start := time.Date(t.Year(), t.Month(), t.Day()-back, p.WindowStart/60, p.WindowStart%60, 0, 0, loc)
		if p.WindowDays&(1<<int(start.Weekday())) == 0 {
			continue
		}
		end := start.Add(time.Duration(p.WindowMinutes) * time.Minute)
		if !t.Before(start) && t.Before(end) {
			return start, true
		}
	}
	return time.Time{}, false
}

// nextWindow returns the start of the next window (or of the open one).
func nextWindow(p store.PatchPolicy, now time.Time) *time.Time {
	if start, ok := windowOpen(p, now); ok {
		return &start
	}
	loc := policyLocation(p)
	t := now.In(loc)
	for d := 0; d <= 7; d++ {
		start := time.Date(t.Year(), t.Month(), t.Day()+d, p.WindowStart/60, p.WindowStart%60, 0, 0, loc)
		if p.WindowDays&(1<<int(start.Weekday())) != 0 && start.After(t) {
			return &start
		}
	}
	return nil
}

func validPolicy(p *store.PatchPolicy) string {
	p.Name = cleanText(p.Name, 100)
	p.Description = cleanText(p.Description, 500)
	p.TimeZone = cleanText(p.TimeZone, 64)
	switch {
	case p.Name == "":
		return "invalid_name"
	case p.WindowDays < 0 || p.WindowDays > 127 || p.WindowStart < 0 || p.WindowStart > 1439 || p.WindowMinutes < 30 || p.WindowMinutes > 1440:
		return "invalid_patch_window"
	case !agentproto.ValidReboot(p.Reboot):
		return "invalid_reboot"
	case p.MaxPostpones < 0 || p.MaxPostpones > 20 || p.PostponeMinutes < 5 || p.PostponeMinutes > 1440 ||
		p.CountdownSeconds < 30 || p.CountdownSeconds > 3600:
		return "invalid_notification"
	}
	// ScanHours is no longer used: the interval is set under Settings and per
	// group. It is kept so older API clients keep working.
	if p.ScanHours < 1 || p.ScanHours > 168 {
		p.ScanHours = 6
	}
	if p.TimeZone != "" {
		if _, err := time.LoadLocation(p.TimeZone); err != nil {
			return "invalid_time_zone"
		}
	}
	approvals := defaultApprovals()
	for k, a := range p.Approvals {
		if !agentproto.ValidCategory(k) || (a.Mode != store.ApproveAuto && a.Mode != store.ApproveManual && a.Mode != store.ApproveNever) ||
			a.DelayDays < 0 || a.DelayDays > 365 {
			return "invalid_approvals"
		}
		approvals[k] = a
	}
	p.Approvals = approvals
	return ""
}

// ---- scheduler ----

func (s *Server) loadPolicies(ctx context.Context) (map[int64]store.PatchPolicy, error) {
	list, err := s.st.ListPatchPolicies(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]store.PatchPolicy, len(list))
	for _, p := range list {
		out[p.ID] = p
	}
	return out, nil
}

func devicePolicy(d store.PatchDevice, policies map[int64]store.PatchPolicy) *store.PatchPolicy {
	if d.PolicyID == nil {
		return nil
	}
	p, ok := policies[*d.PolicyID]
	if !ok || !p.Enabled {
		return nil
	}
	return &p
}

// patchTick scans and patches online devices; it runs every minute.
func (s *Server) patchTick(ctx context.Context) {
	ids := s.hub.onlineIDs()
	if len(ids) == 0 {
		return
	}
	policies, err := s.loadPolicies(ctx)
	if err != nil {
		s.log.Warn("patch policies", "err", err)
		return
	}
	devs, err := s.st.PatchDevices(ctx, ids)
	if err != nil {
		s.log.Warn("patch devices", "err", err)
		return
	}
	now := s.now()
	branding := s.loadBranding(ctx)
	defaults := s.loadScanSettings(ctx)
	scans, started := 0, 0
	for _, d := range devs {
		pol := devicePolicy(d, policies)
		intervals := defaults.effective(d.InventoryMinutes, d.PatchScanMinutes)
		s.pushAgentConfig(d.ID, intervals)
		s.pushPatchConfig(d.ID, pol, branding.ProductName, branding.PrimaryColor)
		if d.OpenRun != nil {
			if d.OpenRunUpdatedAt != nil && now.Sub(*d.OpenRunUpdatedAt) > patchRunStale {
				_ = s.st.FailPatchRun(ctx, *d.OpenRun, "no answer from the agent")
			}
			continue
		}
		if pol == nil {
			continue
		}
		stale := d.ScanAt == nil || now.Sub(*d.ScanAt) > time.Duration(intervals.PatchScanMinutes)*time.Minute
		if stale && (d.ScanRequestedAt == nil || now.Sub(*d.ScanRequestedAt) > patchScanRetry) && scans < patchScansPerTick {
			if s.hub.send(d.ID, agentproto.TypePatchScan, nil) {
				_ = s.st.MarkPatchScanRequested(ctx, d.ID)
				scans++
			}
		}
		if d.ScanAt == nil || started >= patchRunsPerTick {
			continue
		}
		start, open := windowOpen(*pol, now)
		if !open || (d.LastWindowRunAt != nil && !d.LastWindowRunAt.Before(start)) {
			continue
		}
		rows, err := s.st.DevicePatches(ctx, d.ID)
		if err != nil {
			continue
		}
		var approved []string
		for _, r := range rows {
			if ok, _ := approval(*pol, r, now); ok {
				approved = append(approved, r.UpdateID)
			}
		}
		sort.Strings(approved)
		if len(approved) == 0 && !(d.RebootPending && pol.Reboot != agentproto.RebootNever) {
			continue
		}
		if d.Cluster != "" {
			if busy, err := s.st.ClusterBusy(ctx, d.Cluster, d.ID); err != nil || busy {
				continue // the next node waits until this one is done
			}
		}
		if _, err := s.startPatchRun(ctx, d.ID, pol, approved, "window", ""); err == nil {
			started++
		}
	}
}

// pushPatchConfig sends the policy settings when they changed for the session.
func (s *Server) pushPatchConfig(id string, pol *store.PatchPolicy, product, color string) {
	cfg := agentproto.PatchConfig{ProductName: product, AccentColor: color}
	if pol != nil {
		cfg.Managed, cfg.TakeOverWindowsUpdate = true, pol.TakeOver
	}
	s.hub.sendChanged(id, "patch.config", agentproto.TypePatchConfig, cfg)
}

// patchOnConnect sends the patch settings right away and asks for a scan
// when the last one is old.
func (s *Server) patchOnConnect(ctx context.Context, dev store.Device) {
	g, err := s.st.GroupByID(ctx, dev.GroupID)
	if err != nil {
		return
	}
	var pol *store.PatchPolicy
	if g.PatchPolicy != nil {
		if p, err := s.st.PatchPolicyByID(ctx, *g.PatchPolicy); err == nil && p.Enabled {
			pol = &p
		}
	}
	b := s.loadBranding(ctx)
	s.pushPatchConfig(dev.ID, pol, b.ProductName, b.PrimaryColor)
}

func (s *Server) startPatchRun(ctx context.Context, deviceID string, pol *store.PatchPolicy, updateIDs []string, trigger, by string) (store.PatchRun, error) {
	p := defaultPatchPolicy()
	var policyID *int64
	if pol != nil {
		p, policyID = *pol, &pol.ID
	}
	run, err := s.st.CreatePatchRun(ctx, store.PatchRun{DeviceID: deviceID, PolicyID: policyID, Trigger: trigger, Updates: updateIDs, RequestedBy: by})
	if err != nil {
		return run, err
	}
	job := agentproto.PatchInstall{
		RunID: run.ID, Updates: run.Updates, Reboot: p.Reboot, Notify: p.NotifyUsers, MaxPostpones: p.MaxPostpones,
		PostponeMinutes: p.PostponeMinutes, CountdownSeconds: p.CountdownSeconds,
	}
	if !s.hub.send(deviceID, agentproto.TypePatchInstall, job) {
		_ = s.st.FailPatchRun(ctx, run.ID, "the device went offline")
		return run, errDeviceOffline
	}
	s.log.Info("patch job started", "device", deviceID, "run", run.ID, "updates", len(updateIDs), "trigger", trigger)
	return run, nil
}

var errDeviceOffline = errors.New("device offline")

// ---- agent messages ----

func (s *Server) handlePatchScanMsg(ctx context.Context, c *agentConn, e *agentproto.Envelope) error {
	var res agentproto.PatchScanResult
	if err := e.Decode(&res); err != nil {
		return err
	}
	ups := res.Updates
	if len(ups) > maxPatchesPerDevice {
		ups = ups[:maxPatchesPerDevice]
	}
	seen := map[string]bool{}
	rows := make([]store.PatchRow, 0, len(ups))
	for _, u := range ups {
		id := cleanText(u.ID, 128)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		cat := u.Category
		if !agentproto.ValidCategory(cat) {
			cat = agentproto.CategoryOther
		}
		row := store.PatchRow{UpdateID: id, Title: cleanText(u.Title, 500), KB: cleanText(u.KB, 20), Category: cat,
			Severity: cleanText(u.Severity, 20), SizeBytes: max(u.SizeBytes, 0), CurrentVersion: cleanText(u.CurrentVersion, 100),
			NewVersion: cleanText(u.NewVersion, 100), Reboot: u.RebootRequired}
		if row.Title == "" {
			row.Title = id
		}
		if t := validTime(u.ReleasedAt); t != nil {
			row.ReleasedAt = t
		}
		rows = append(rows, row)
	}
	return s.st.SaveScanResult(ctx, c.deviceID, store.ScanResult{Error: cleanText(res.Error, 1000), Source: cleanText(res.Source, 30),
		Cluster: cleanText(res.Cluster, 100), RebootPending: res.RebootPending, Patches: rows})
}

func (s *Server) handlePatchStatusMsg(ctx context.Context, c *agentConn, e *agentproto.Envelope) error {
	var st agentproto.PatchStatus
	if err := e.Decode(&st); err != nil {
		return err
	}
	switch st.State {
	case agentproto.PatchWaitingUser, agentproto.PatchPostponed, agentproto.PatchInstalling, agentproto.PatchRebooting,
		agentproto.PatchCompleted, agentproto.PatchFailed:
	default:
		return errors.New("unknown patch state " + strconv.Quote(st.State))
	}
	u := store.PatchRunUpdate{State: st.State, Message: cleanText(st.Message, 1000), RebootRequired: st.RebootRequired,
		Postpones: max(st.Postpones, 0), Finished: agentproto.PatchFinished(st.State)}
	if len(st.Results) > 0 {
		results := st.Results
		if len(results) > maxPatchesPerDevice {
			results = results[:maxPatchesPerDevice]
		}
		for i := range results {
			results[i].ID, results[i].Error = cleanText(results[i].ID, 128), cleanText(results[i].Error, 500)
			if results[i].OK {
				u.Installed++
			} else {
				u.Failed++
			}
		}
		u.Results, _ = json.Marshal(results)
	}
	if pu := st.PostponedUntil; !pu.IsZero() && pu.After(s.now().Add(-time.Hour)) && pu.Before(s.now().Add(48*time.Hour)) {
		u.PostponedUntil = &pu
	}
	err := s.st.UpdatePatchRun(ctx, st.RunID, c.deviceID, u)
	if errors.Is(err, store.ErrNotFound) {
		return nil // a late report for a job that was closed already
	}
	if err == nil && u.Finished {
		s.log.Info("patch job finished", "device", c.deviceID, "run", st.RunID, "state", st.State, "installed", u.Installed, "failed", u.Failed)
	}
	return err
}

// ---- API ----

func (s *Server) patchRoutes(mux *http.ServeMux) {
	view := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermDevicesView, 0, h) }
	manage := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermDevicesManage, 0, h) }
	approve := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermPatchesApprove, 0, h) }
	policies := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermPatchesManage, 0, h) }

	mux.Handle("GET /api/v1/patches/summary", view(s.handlePatchSummary))
	mux.Handle("GET /api/v1/patches/updates", view(s.handleListUpdates))
	mux.Handle("PUT /api/v1/patches/decision", approve(s.handlePatchDecision))
	mux.Handle("GET /api/v1/patches/runs", view(s.handleListPatchRuns))
	mux.Handle("DELETE /api/v1/patches/runs/{id}", manage(s.handleCancelPatchRun))
	mux.Handle("GET /api/v1/patches/policies", view(s.handleListPolicies))
	mux.Handle("POST /api/v1/patches/policies", policies(s.handleCreatePolicy))
	mux.Handle("PUT /api/v1/patches/policies/{id}", policies(s.handleUpdatePolicy))
	mux.Handle("DELETE /api/v1/patches/policies/{id}", policies(s.handleDeletePolicy))
	mux.Handle("GET /api/v1/devices/{id}/patches", view(s.handleDevicePatches))
	mux.Handle("POST /api/v1/devices/{id}/patches/scan", manage(s.handlePatchScanNow))
	mux.Handle("POST /api/v1/devices/{id}/patches/install", manage(s.handlePatchInstallNow))
}

type policyView struct {
	store.PatchPolicy
	NextWindow *time.Time `json:"nextWindow"`
	WindowOpen bool       `json:"windowOpen"`
}

func (s *Server) policyView(p store.PatchPolicy) policyView {
	_, open := windowOpen(p, s.now())
	return policyView{PatchPolicy: p, NextWindow: nextWindow(p, s.now()), WindowOpen: open}
}

type policiesView struct {
	Policies   []policyView      `json:"policies"`
	Defaults   store.PatchPolicy `json:"defaults"`
	Categories []string          `json:"categories"`
	ServerZone string            `json:"serverTimeZone"`
}

func (s *Server) handleListPolicies(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListPatchPolicies(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := policiesView{Policies: make([]policyView, 0, len(list)), Defaults: defaultPatchPolicy(), Categories: agentproto.Categories,
		ServerZone: serverZoneName()}
	for _, p := range list {
		out.Policies = append(out.Policies, s.policyView(p))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreatePolicy(w http.ResponseWriter, r *http.Request) {
	var p store.PatchPolicy
	if !decode(w, r, &p) {
		return
	}
	if bad := validPolicy(&p); bad != "" {
		apiError(w, http.StatusBadRequest, bad)
		return
	}
	p, err := s.st.CreatePatchPolicy(r.Context(), p)
	if errors.Is(err, store.ErrConflict) {
		apiError(w, http.StatusConflict, "policy_exists")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "patch.policy.create", p.Name, true, nil)
	writeJSON(w, http.StatusCreated, s.policyView(p))
}

func (s *Server) handleUpdatePolicy(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	var p store.PatchPolicy
	if !decode(w, r, &p) {
		return
	}
	if bad := validPolicy(&p); bad != "" {
		apiError(w, http.StatusBadRequest, bad)
		return
	}
	p.ID = id
	err := s.st.UpdatePatchPolicy(r.Context(), p)
	switch {
	case errors.Is(err, store.ErrNotFound):
		apiError(w, http.StatusNotFound, "not_found")
		return
	case errors.Is(err, store.ErrConflict):
		apiError(w, http.StatusConflict, "policy_exists")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "patch.policy.update", p.Name, true, map[string]any{"enabled": p.Enabled, "reboot": p.Reboot})
	p, _ = s.st.PatchPolicyByID(r.Context(), id)
	writeJSON(w, http.StatusOK, s.policyView(p))
}

func (s *Server) handleDeletePolicy(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	p, err := s.st.PatchPolicyByID(r.Context(), id)
	if err == nil {
		err = s.st.DeletePatchPolicy(r.Context(), id)
	}
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "patch.policy.delete", p.Name, true, nil)
	w.WriteHeader(http.StatusNoContent)
}

type patchSummary struct {
	Stats    store.PatchStats `json:"stats"`
	Runs     []store.PatchRun `json:"runs"`
	Policies int              `json:"policies"`
}

func (s *Server) handlePatchSummary(w http.ResponseWriter, r *http.Request) {
	sc := s.scope(r)
	st, err := s.st.PatchStats(r.Context(), sc)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	runs, err := s.st.ListPatchRuns(r.Context(), store.RunQuery{Scope: sc, Limit: 15})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	pols, _ := s.st.ListPatchPolicies(r.Context())
	writeJSON(w, http.StatusOK, patchSummary{Stats: st, Runs: runs, Policies: len(pols)})
}

func (s *Server) handleListUpdates(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cat := q.Get("category")
	if cat != "" && !agentproto.ValidCategory(cat) {
		cat = ""
	}
	plat := q.Get("platform")
	if plat != "" && !validPlatform(plat) {
		plat = ""
	}
	list, err := s.st.ListUpdates(r.Context(), store.UpdateQuery{Scope: s.scope(r), Search: strings.TrimSpace(q.Get("search")),
		Category: cat, Platform: plat, Decision: q.Get("decision")})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type decisionReq struct {
	UpdateID string `json:"updateId"`
	Decision string `json:"decision"` // approved, declined, "" (follow the policy)
}

func (s *Server) handlePatchDecision(w http.ResponseWriter, r *http.Request) {
	var req decisionReq
	if !decode(w, r, &req) {
		return
	}
	req.UpdateID = cleanText(req.UpdateID, 128)
	if req.UpdateID == "" || (req.Decision != "" && req.Decision != "approved" && req.Decision != "declined") {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	u, _ := currentUser(r)
	if err := s.st.SetPatchDecision(r.Context(), req.UpdateID, req.Decision, u.Username); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "patch.decision", req.UpdateID, true, map[string]any{"decision": req.Decision})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListPatchRuns(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	runs, err := s.st.ListPatchRuns(r.Context(), store.RunQuery{Scope: s.scope(r), DeviceID: r.URL.Query().Get("device"), Limit: limit})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *Server) handleCancelPatchRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	run, err := s.st.PatchRunByID(r.Context(), id)
	if err == nil {
		var d store.Device
		d, err = s.st.DeviceByID(r.Context(), run.DeviceID)
		if err == nil && !s.scope(r).Allows(d.GroupID) {
			err = store.ErrNotFound
		}
	}
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	u, _ := currentUser(r)
	if err := s.st.FailPatchRun(r.Context(), id, "closed by "+u.Username); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "patch.run.close", run.Hostname, true, map[string]any{"run": id})
	w.WriteHeader(http.StatusNoContent)
}

type devicePatchesView struct {
	Policy        *policyView      `json:"policy"`
	ServerZone    string           `json:"serverTimeZone"`
	ScanAt        *time.Time       `json:"scanAt"`
	ScanError     string           `json:"scanError"`
	Source        string           `json:"source"`
	Cluster       string           `json:"cluster"`
	RebootPending bool             `json:"rebootPending"`
	Updates       []store.PatchRow `json:"updates"`
	Runs          []store.PatchRun `json:"runs"`
}

func (s *Server) handleDevicePatches(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	rows, err := s.st.DevicePatches(r.Context(), d.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := devicePatchesView{ServerZone: serverZoneName(), ScanAt: d.PatchScanAt, ScanError: d.PatchScanError, Source: d.PatchSource, Cluster: d.PatchCluster,
		RebootPending: d.PatchRebootPending, Updates: rows}
	g, err := s.st.GroupByID(r.Context(), d.GroupID)
	pol := defaultPatchPolicy()
	if err == nil && g.PatchPolicy != nil {
		if p, err := s.st.PatchPolicyByID(r.Context(), *g.PatchPolicy); err == nil {
			v := s.policyView(p)
			out.Policy, pol = &v, p
		}
	}
	now := s.now()
	for i := range out.Updates {
		out.Updates[i].Approved, out.Updates[i].ApprovedFrom = approval(pol, out.Updates[i], now)
	}
	if out.Runs, err = s.st.ListPatchRuns(r.Context(), store.RunQuery{Scope: store.Scope{All: true}, DeviceID: d.ID, Limit: 20}); err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePatchScanNow(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	if !s.hub.send(d.ID, agentproto.TypePatchScan, nil) {
		apiError(w, http.StatusConflict, "device_offline")
		return
	}
	_ = s.st.MarkPatchScanRequested(r.Context(), d.ID)
	s.audit(r, "patch.scan", d.Hostname, true, nil)
	w.WriteHeader(http.StatusAccepted)
}

type installNowReq struct {
	UpdateIDs []string `json:"updateIds"` // empty: the approved updates
}

func (s *Server) handlePatchInstallNow(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	var req installNowReq
	if !decode(w, r, &req) {
		return
	}
	if !d.Online {
		apiError(w, http.StatusConflict, "device_offline")
		return
	}
	var pol *store.PatchPolicy
	if g, err := s.st.GroupByID(r.Context(), d.GroupID); err == nil && g.PatchPolicy != nil {
		if p, err := s.st.PatchPolicyByID(r.Context(), *g.PatchPolicy); err == nil {
			pol = &p
		}
	}
	rows, err := s.st.DevicePatches(r.Context(), d.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	effective := defaultPatchPolicy()
	if pol != nil {
		effective = *pol
	}
	var ids []string
	if len(req.UpdateIDs) > 0 {
		known := map[string]bool{}
		for _, row := range rows {
			known[row.UpdateID] = true
		}
		for _, id := range req.UpdateIDs {
			if !known[id] {
				apiError(w, http.StatusBadRequest, "unknown_update")
				return
			}
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	} else {
		for _, row := range rows {
			if ok, _ := approval(effective, row, s.now()); ok {
				ids = append(ids, row.UpdateID)
			}
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 && !(d.PatchRebootPending && effective.Reboot != agentproto.RebootNever) {
		apiError(w, http.StatusBadRequest, "nothing_to_install")
		return
	}
	if d.PatchCluster != "" {
		if busy, err := s.st.ClusterBusy(r.Context(), d.PatchCluster, d.ID); err == nil && busy {
			apiError(w, http.StatusConflict, "cluster_busy")
			return
		}
	}
	u, _ := currentUser(r)
	run, err := s.startPatchRun(r.Context(), d.ID, pol, ids, "manual", u.Username)
	switch {
	case errors.Is(err, store.ErrConflict):
		apiError(w, http.StatusConflict, "run_open")
		return
	case errors.Is(err, errDeviceOffline):
		apiError(w, http.StatusConflict, "device_offline")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "patch.install", d.Hostname, true, map[string]any{"run": run.ID, "updates": len(ids)})
	writeJSON(w, http.StatusAccepted, run)
}
