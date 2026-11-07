package server

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agent"
	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

func TestWindowOpen(t *testing.T) {
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	// Tuesday and Thursday 22:00 - 02:00 in Amsterdam.
	p := store.PatchPolicy{WindowDays: 1<<2 | 1<<4, WindowStart: 22 * 60, WindowMinutes: 240, TimeZone: "Europe/Amsterdam"}
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, ams)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cases := []struct {
		now   string
		open  bool
		start string
	}{
		{"2026-11-10 21:59", false, ""}, // Tuesday
		{"2026-11-10 22:00", true, "2026-11-10 22:00"},
		{"2026-11-11 01:30", true, "2026-11-10 22:00"}, // Wednesday night, window of Tuesday
		{"2026-11-11 02:00", false, ""},
		{"2026-11-11 22:30", false, ""}, // Wednesday is not a window day
		{"2026-11-12 23:00", true, "2026-11-12 22:00"},
	}
	for _, c := range cases {
		start, open := windowOpen(p, at(c.now).UTC())
		if open != c.open || (open && !start.Equal(at(c.start))) {
			t.Errorf("%s: open=%v start=%v", c.now, open, start)
		}
	}
	if n := nextWindow(p, at("2026-11-11 03:00")); n == nil || !n.Equal(at("2026-11-12 22:00")) {
		t.Errorf("next window: %v", n)
	}
	if n := nextWindow(store.PatchPolicy{WindowDays: 0, WindowMinutes: 60}, time.Now()); n != nil {
		t.Errorf("next window without days: %v", n)
	}
}

func TestApproval(t *testing.T) {
	p := defaultPatchPolicy()
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	released := now.Add(-5 * day)
	cases := []struct {
		name string
		row  store.PatchRow
		want bool
	}{
		{"security now", store.PatchRow{Category: "security", FirstSeenAt: now}, true},
		{"cumulative too new", store.PatchRow{Category: "cumulative", FirstSeenAt: now, ReleasedAt: &released}, false},
		{"cumulative after a week", store.PatchRow{Category: "cumulative", FirstSeenAt: now.Add(-8 * day)}, true},
		{"driver needs approval", store.PatchRow{Category: "driver", FirstSeenAt: now.Add(-30 * day)}, false},
		{"driver approved", store.PatchRow{Category: "driver", FirstSeenAt: now, Decision: "approved"}, true},
		{"feature never", store.PatchRow{Category: "feature", FirstSeenAt: now.Add(-90 * day)}, false},
		{"security declined", store.PatchRow{Category: "security", FirstSeenAt: now, Decision: "declined"}, false},
	}
	for _, c := range cases {
		if got, _ := approval(p, c.row, now); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
	bad := store.PatchPolicy{Name: "x", WindowDays: 127, WindowMinutes: 60, Reboot: "prompt", PostponeMinutes: 60, CountdownSeconds: 60, ScanHours: 6,
		Approvals: map[string]store.Approval{"security": {Mode: "sometimes"}}}
	if v := validPolicy(&bad); v != "invalid_approvals" {
		t.Errorf("invalid approvals: %q", v)
	}
	bad.Approvals, bad.TimeZone = nil, "Mars/Olympus"
	if v := validPolicy(&bad); v != "invalid_time_zone" {
		t.Errorf("invalid zone: %q", v)
	}
	bad.TimeZone = ""
	if v := validPolicy(&bad); v != "" || bad.Approvals["feature"].Mode != store.ApproveNever {
		t.Errorf("valid policy: %q %+v", v, bad.Approvals)
	}
}

// fakePatches is a PatchBackend for tests.
type fakePatches struct {
	mu        sync.Mutex
	missing   []agentproto.PatchUpdate
	installed [][]string
	reboot    bool
	reboots   int
	cluster   string
	cfg       agentproto.PatchConfig
	block     chan struct{} // when set, Install waits for it
}

func (f *fakePatches) Scan(context.Context) (agentproto.PatchScanResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return agentproto.PatchScanResult{Updates: slices.Clone(f.missing), Source: "fake", Cluster: f.cluster, RebootPending: f.reboot}, nil
}

func (f *fakePatches) Install(ctx context.Context, ids []string, progress func(string)) ([]agentproto.PatchResult, bool, error) {
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installed = append(f.installed, ids)
	progress("installing")
	var res []agentproto.PatchResult
	kept := f.missing[:0]
	for _, u := range f.missing {
		if slices.Contains(ids, u.ID) {
			res = append(res, agentproto.PatchResult{ID: u.ID, OK: true})
			if u.RebootRequired {
				f.reboot = true
			}
		} else {
			kept = append(kept, u)
		}
	}
	f.missing = kept
	return res, f.reboot, nil
}

func (f *fakePatches) RebootPending(context.Context) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reboot
}

func (f *fakePatches) Reboot(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reboots++
	return nil
}

func (f *fakePatches) Configure(c agentproto.PatchConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cfg = c
	return nil
}

func (f *fakePatches) installs() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.installed)
}

// fakeNotifier answers prompts.
type fakeNotifier struct {
	mu       sync.Mutex
	prompts  []agent.Prompt
	postpone int // answer "postpone" this many times
}

func (n *fakeNotifier) Ask(_ context.Context, p agent.Prompt) (agent.Answer, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.prompts = append(n.prompts, p)
	if n.postpone > 0 {
		n.postpone--
		return agent.Answer{Postpone: true}, nil
	}
	return agent.Answer{}, nil
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (e *env) onlineDevice(c *client, host string) store.Device {
	e.t.Helper()
	var d store.Device
	waitFor(e.t, host+" online", func() bool {
		var list deviceList
		c.do("GET", "/api/v1/devices?search="+host, nil, &list)
		if list.Total == 1 && list.Items[0].Online {
			d = list.Items[0]
			return true
		}
		return false
	})
	return d
}

func allDayPolicy(name string) map[string]any {
	return map[string]any{
		"name": name, "enabled": true, "windowDays": 127, "windowStart": 0, "windowMinutes": 1440, "reboot": "prompt",
		"notifyUsers": true, "maxPostpones": 1, "postponeMinutes": 5, "countdownSeconds": 60, "takeOver": true, "scanHours": 6,
		"approvals": map[string]any{"driver": map[string]any{"mode": "manual"}},
	}
}

func TestPatchPolicyAndJobs(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()

	var pv policyView
	if code := admin.do("POST", "/api/v1/patches/policies", allDayPolicy("Workstations"), &pv); code != 201 || !pv.WindowOpen || pv.Approvals["security"].Mode != "auto" {
		t.Fatalf("create policy: %d %+v", code, pv)
	}
	if code := admin.do("POST", "/api/v1/patches/policies", allDayPolicy("workstations"), nil); code != 409 {
		t.Errorf("duplicate policy: %d", code)
	}
	if code := admin.do("PUT", "/api/v1/groups/1", map[string]any{"name": "Default", "updateRing": 1, "patchPolicyId": 9999}, nil); code != 400 {
		t.Errorf("unknown policy: %d", code)
	}
	if code := admin.do("PUT", "/api/v1/groups/1", map[string]any{"name": "Default", "updateRing": 1, "patchPolicyId": pv.ID}, nil); code != 200 {
		t.Fatalf("assign policy: %d", code)
	}

	_, info := e.createToken(admin, 1)
	a := newTestAgent(t, e, info.Token, "PC-PATCH", false)
	fp := &fakePatches{missing: []agentproto.PatchUpdate{
		{ID: "11111111-aaaa", Title: "2026-11 Security Update (KB5050001)", KB: "KB5050001", Category: "security", RebootRequired: true},
		{ID: "22222222-bbbb", Title: "Intel - Display - 31.0.101", Category: "driver"},
		{ID: "33333333-cccc", Title: "Windows 11, version 25H2", Category: "feature"},
	}}
	fn := &fakeNotifier{postpone: 1}
	a.Patches, a.Notify, a.Minute = fp, fn, 10*time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	go a.Run(ctx) //nolint:errcheck
	d := e.onlineDevice(admin, "PC-PATCH")
	waitFor(t, "patch settings", func() bool {
		fp.mu.Lock()
		defer fp.mu.Unlock()
		return fp.cfg.Managed && fp.cfg.TakeOverWindowsUpdate && fp.cfg.ProductName != ""
	})

	// First tick: the device was never scanned, so it is asked to scan.
	e.srv.patchTick(ctx)
	var dp devicePatchesView
	waitFor(t, "scan result", func() bool {
		admin.do("GET", "/api/v1/devices/"+d.ID+"/patches", nil, &dp)
		return dp.ScanAt != nil && len(dp.Updates) == 3
	})
	if dp.Policy == nil || dp.Policy.Name != "Workstations" || dp.Source != "fake" {
		t.Errorf("device patches: %+v", dp)
	}
	approved := map[string]bool{}
	for _, u := range dp.Updates {
		approved[u.UpdateID] = u.Approved
	}
	if !approved["11111111-aaaa"] || approved["22222222-bbbb"] || approved["33333333-cccc"] {
		t.Errorf("approvals: %+v", approved)
	}

	// The update overview and a manual approval of the driver.
	var ups []store.UpdateSummary
	if code := admin.do("GET", "/api/v1/patches/updates?category=driver", nil, &ups); code != 200 || len(ups) != 1 || ups[0].Devices != 1 {
		t.Fatalf("updates: %d %+v", code, ups)
	}
	if code := admin.do("PUT", "/api/v1/patches/decision", map[string]any{"updateId": "22222222-bbbb", "decision": "approved"}, nil); code != 204 {
		t.Fatalf("approve: %d", code)
	}

	// Second tick: the window is open, so the approved updates are installed.
	e.srv.patchTick(ctx)
	var runs []store.PatchRun
	waitFor(t, "patch job to finish", func() bool {
		admin.do("GET", "/api/v1/patches/runs?device="+d.ID, nil, &runs)
		return len(runs) == 1 && runs[0].State == agentproto.PatchRebooting
	})
	r := runs[0]
	if r.Trigger != "window" || !slices.Equal(r.Updates, []string{"11111111-aaaa", "22222222-bbbb"}) || r.Installed != 2 || r.Postpones != 1 || !r.RebootRequired {
		t.Errorf("run: %+v", r)
	}
	if got := fp.installs(); len(got) != 1 || len(got[0]) != 2 {
		t.Errorf("installed: %+v", got)
	}
	fn.mu.Lock()
	kinds := []string{}
	for _, p := range fn.prompts {
		kinds = append(kinds, fmt.Sprintf("%s:%v", p.Kind, p.CanPostpone))
	}
	fn.mu.Unlock()
	// install (postponed), install (last chance, no postpone left), reboot.
	if !slices.Equal(kinds, []string{"install:true", "install:false", "reboot:true"}) {
		t.Errorf("prompts: %v", kinds)
	}
	waitFor(t, "reboot", func() bool {
		fp.mu.Lock()
		defer fp.mu.Unlock()
		return fp.reboots == 1
	})

	// The job stays open during the restart, so no second job starts.
	e.srv.patchTick(ctx)
	if code := admin.do("POST", "/api/v1/devices/"+d.ID+"/patches/install", map[string]any{}, nil); code != 409 {
		t.Errorf("install during an open job: %d", code)
	}

	// Simulate the restart: stop the agent and start it again with a later boot time.
	cancel()
	time.Sleep(300 * time.Millisecond)
	fp.mu.Lock()
	fp.reboot = false
	fp.mu.Unlock()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel2()
	booted := time.Now().Add(time.Hour)
	a2 := &agent.Agent{ConfigPath: a.ConfigPath, StateDir: a.StateDir, Collect: a.Collect, Patches: fp, Notify: fn,
		BootTime: func() time.Time { return booted }}
	go a2.Run(ctx2) //nolint:errcheck
	waitFor(t, "job completed after the restart", func() bool {
		admin.do("GET", "/api/v1/patches/runs?device="+d.ID, nil, &runs)
		return len(runs) == 1 && runs[0].State == agentproto.PatchCompleted
	})
	if runs[0].RebootRequired || runs[0].FinishedAt == nil || runs[0].Installed != 2 {
		t.Errorf("finished run: %+v", runs[0])
	}
	waitFor(t, "rescan after the job", func() bool {
		admin.do("GET", "/api/v1/devices/"+d.ID+"/patches", nil, &dp)
		return len(dp.Updates) == 1 && !dp.RebootPending
	})

	// The window already had its job: no second window job.
	e.srv.patchTick(ctx2)
	time.Sleep(200 * time.Millisecond)
	admin.do("GET", "/api/v1/patches/runs?device="+d.ID, nil, &runs)
	if len(runs) != 1 {
		t.Errorf("second job in the same window: %d", len(runs))
	}

	// Manual install of a declined-by-policy update, chosen explicitly.
	var mr store.PatchRun
	if code := admin.do("POST", "/api/v1/devices/"+d.ID+"/patches/install", map[string]any{"updateIds": []string{"nope"}}, nil); code != 400 {
		t.Errorf("unknown update: %d", code)
	}
	if code := admin.do("POST", "/api/v1/devices/"+d.ID+"/patches/install", map[string]any{}, nil); code != 400 {
		t.Errorf("nothing approved to install: %d", code)
	}
	if code := admin.do("POST", "/api/v1/devices/"+d.ID+"/patches/install", map[string]any{"updateIds": []string{"33333333-cccc"}}, &mr); code != 202 || mr.Trigger != "manual" {
		t.Fatalf("manual install: %d %+v", code, mr)
	}
	waitFor(t, "manual job", func() bool {
		admin.do("GET", "/api/v1/patches/runs?device="+d.ID, nil, &runs)
		return len(runs) == 2 && runs[0].State == agentproto.PatchCompleted
	})

	var sum patchSummary
	if code := admin.do("GET", "/api/v1/patches/summary", nil, &sum); code != 200 || sum.Stats.Managed != 1 || sum.Stats.Scanned != 1 || sum.Policies != 1 {
		t.Errorf("summary: %d %+v", code, sum)
	}

	// Removing the policy releases Windows Update again.
	if code := admin.do("DELETE", fmt.Sprintf("/api/v1/patches/policies/%d", pv.ID), nil, nil); code != 204 {
		t.Fatalf("delete policy: %d", code)
	}
	e.srv.patchTick(ctx2)
	waitFor(t, "unmanaged settings", func() bool {
		fp.mu.Lock()
		defer fp.mu.Unlock()
		return !fp.cfg.Managed && !fp.cfg.TakeOverWindowsUpdate
	})
}

func TestProxmoxClusterOneNodeAtATime(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()
	pol := allDayPolicy("Proxmox")
	pol["notifyUsers"], pol["reboot"] = false, "automatic"
	var pv policyView
	admin.do("POST", "/api/v1/patches/policies", pol, &pv)
	admin.do("PUT", "/api/v1/groups/1", map[string]any{"name": "Default", "updateRing": 1, "patchPolicyId": pv.ID}, nil)
	_, info := e.createToken(admin, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	block := make(chan struct{})
	for _, host := range []string{"PVE-1", "PVE-2"} {
		a := newTestAgent(t, e, info.Token, host, false)
		a.MachineID = "machine-" + host
		fp := &fakePatches{cluster: "lab", block: block, missing: []agentproto.PatchUpdate{
			{ID: "pve-manager", Title: "pve-manager", Category: "other", CurrentVersion: "9.0.10", NewVersion: "9.0.11"},
			{ID: "libssl3t64", Title: "libssl3t64", Category: "security"},
		}}
		a.Patches = fp
		go a.Run(ctx) //nolint:errcheck
		e.onlineDevice(admin, host)
	}
	e.srv.patchTick(ctx) // scans
	waitFor(t, "both scans", func() bool {
		var list deviceList
		admin.do("GET", "/api/v1/devices", nil, &list)
		n := 0
		for _, d := range list.Items {
			if d.PatchScanAt != nil && d.PatchCluster == "lab" {
				n++
			}
		}
		return n == 2
	})
	e.srv.patchTick(ctx)
	var runs []store.PatchRun
	admin.do("GET", "/api/v1/patches/runs", nil, &runs)
	if len(runs) != 1 {
		t.Fatalf("jobs started in one cluster: %d", len(runs))
	}
	first := runs[0].Hostname
	close(block)
	waitFor(t, "first node done", func() bool {
		admin.do("GET", "/api/v1/patches/runs", nil, &runs)
		return len(runs) == 1 && agentproto.PatchFinished(runs[0].State)
	})
	if runs[0].State != agentproto.PatchCompleted || runs[0].Installed != 1 || runs[0].Updates[0] != "libssl3t64" {
		// "other" updates wait three days; only the security update is approved.
		t.Errorf("first run: %+v", runs[0])
	}
	e.srv.patchTick(ctx)
	waitFor(t, "second node", func() bool {
		admin.do("GET", "/api/v1/patches/runs", nil, &runs)
		return len(runs) == 2 && runs[0].Hostname != first
	})
}
