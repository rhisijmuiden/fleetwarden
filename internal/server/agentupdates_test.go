package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/debpatch"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
	"github.com/rhisijmuiden/fleetwarden/internal/updates"
	"github.com/rhisijmuiden/fleetwarden/internal/version"
)

func intp(v int) *int { return &v }

func TestInUpdateWindow(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 11, 7, h, 30, 0, 0, time.Local) }
	g := store.DeviceGroup{}
	if !inUpdateWindow(g, at(3)) {
		t.Error("no window must allow any hour")
	}
	g.WindowStart, g.WindowEnd = intp(22), intp(6)
	for h, want := range map[int]bool{23: true, 2: true, 6: false, 12: false, 22: true} {
		if inUpdateWindow(g, at(h)) != want {
			t.Errorf("22-6 at %d: want %v", h, want)
		}
	}
	g.WindowStart, g.WindowEnd = intp(9), intp(17)
	if inUpdateWindow(g, at(8)) || !inUpdateWindow(g, at(9)) || inUpdateWindow(g, at(17)) {
		t.Error("9-17 window")
	}
}

func TestTargetRolloutAndAdvance(t *testing.T) {
	test, prod := "test", "production"
	groups := map[int64]groupInfo{
		1: {DeviceGroup: store.DeviceGroup{ID: 1, Name: "Pilot", UpdateRing: 0}, channel: test},
		2: {DeviceGroup: store.DeviceGroup{ID: 2, Name: "Office", UpdateRing: 1}, channel: prod},
		3: {DeviceGroup: store.DeviceGroup{ID: 3, Name: "Servers", UpdateRing: 3}, channel: prod},
	}
	rollouts := []store.Rollout{
		{ID: 3, Version: "0.3.0-test.7", Channel: test, Status: store.RolloutActive, Ring: 0},
		{ID: 2, Version: "0.2.0", Channel: prod, Status: store.RolloutActive, Ring: 1},
		{ID: 1, Version: "0.1.0", Channel: prod, Status: store.RolloutCompleted, Ring: 3},
	}
	for gid, want := range map[int64]string{1: "0.3.0-test.7", 2: "0.2.0", 3: "0.1.0"} {
		if r := targetRollout(rollouts, groups[gid]); r == nil || r.Version != want {
			t.Errorf("group %d: target %+v, want %s", gid, r, want)
		}
	}

	dev := func(id string, group int64, v string) store.DeviceUpdate {
		return store.DeviceUpdate{DeviceID: id, GroupID: group, AgentVersion: v}
	}
	now := time.Now()
	r := store.Rollout{ID: 9, Version: "0.4.0", Channel: prod, Status: store.RolloutActive, Ring: 0, RingStartedAt: now, SoakHours: 24, MaxFailures: 1}
	devs := []store.DeviceUpdate{dev("a", 2, "0.2.0"), dev("b", 2, "0.2.0"), dev("c", 3, "0.2.0"), dev("d", 1, "0.0.0-dev")}
	// Ring 0 only holds the test-channel pilot (and a hand-made build): empty for 0.4.0? No:
	// production versions go to test groups too, but the pilot device has no comparable version.
	rings := rolloutRings(r, groups, devs)
	if rings[0].Devices != 0 || rings[1].Devices != 2 || rings[1].Pending != 2 || rings[3].Devices != 1 {
		t.Fatalf("rings: %+v", rings)
	}
	if !advance(&r, rings, now) || r.Ring != 1 || r.Status != store.RolloutActive {
		t.Fatalf("empty ring 0 must be skipped: %+v", r)
	}
	if advance(&r, rolloutRings(r, groups, devs), now.Add(time.Hour)) {
		t.Fatal("ring 1 advanced before the soak time")
	}
	nine := int64(9)
	devs[0].AgentVersion = "0.4.0"
	devs[1].Rollout, devs[1].Version, devs[1].State = &nine, "0.4.0", agentproto.UpdateRolledBack
	rings = rolloutRings(r, groups, devs)
	if rings[1].Updated != 1 || rings[1].Failed != 1 {
		t.Fatalf("ring 1: %+v", rings[1])
	}
	// One failure is within the limit; after the soak time ring 3 is next (ring 2 is empty).
	if !advance(&r, rings, now.Add(25*time.Hour)) || r.Ring != 3 {
		t.Fatalf("after soak: %+v", r)
	}
	devs[2].Rollout, devs[2].Version, devs[2].State = &nine, "0.4.0", agentproto.UpdateFailed
	devs = append(devs, store.DeviceUpdate{DeviceID: "e", GroupID: 3, AgentVersion: "0.2.0", Rollout: &nine, Version: "0.4.0", State: agentproto.UpdateFailed})
	if !advance(&r, rolloutRings(r, groups, devs), now.Add(26*time.Hour)) || r.Status != store.RolloutPaused {
		t.Fatalf("two failures must pause: %+v", r)
	}
}

func TestAgentUpdateRollout(t *testing.T) {
	oldVersion := version.Version
	version.Version = "0.1.0"
	t.Cleanup(func() { version.Version = oldVersion })
	e := newEnv(t)
	e.srv.cfg.AgentDir = t.TempDir()
	arch := runtime.GOARCH
	write := func(dir, v string) (string, string) {
		deb, err := debpatch.BuildMinimal("fleetwarden-agent", v)
		if err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("fleetwarden-agent_%s_%s.deb", v, arch)
		os.MkdirAll(dir, 0o755)
		if err := os.WriteFile(filepath.Join(dir, name), deb, 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(deb)
		return name, hex.EncodeToString(sum[:])
	}
	oldName, _ := write(e.srv.cfg.AgentDir, "0.1.0")
	newName, newSum := write(filepath.Join(e.srv.cfg.AgentReleasesDir(), "0.2.0"), "0.2.0")

	admin, _ := e.signedInAdmin()
	_, info := e.createToken(admin, 1)
	a := newTestAgent(t, e, info.Token, "PC-UPD", false)
	helper := make(chan updatePlanView, 1)
	a.StartHelper = func(exe, planPath string) error {
		var p updatePlanView
		raw, _ := os.ReadFile(planPath)
		_ = json.Unmarshal(raw, &p)
		helper <- p
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go a.Run(ctx) //nolint:errcheck

	var list deviceList
	for i := 0; i < 100 && (list.Total != 1 || !list.Items[0].Online); i++ {
		admin.do("GET", "/api/v1/devices", nil, &list)
		time.Sleep(50 * time.Millisecond)
	}
	if list.Total != 1 || list.Items[0].AgentVersion != "0.1.0" {
		t.Fatalf("device: %+v", list)
	}
	id := list.Items[0].ID

	// Unsigned package downloads are refused.
	if res, err := http.Get(e.ts.URL + agentproto.PathDownload + newName); err != nil || res.StatusCode != 401 {
		t.Fatalf("unsigned download: %v %v", err, res)
	}

	var view agentUpdatesView
	if code := admin.do("POST", "/api/v1/agent/rollouts", map[string]any{"version": "9.9.9"}, nil); code != 400 {
		t.Errorf("rollout of an unknown version: %d", code)
	}
	if code := admin.do("POST", "/api/v1/agent/rollouts", map[string]any{"version": "0.2.0", "soakHours": 0}, &view); code != 200 || len(view.Rollouts) != 1 {
		t.Fatalf("create rollout: %d %+v", code, view)
	}
	e.srv.agentUpdateTick(ctx)
	var p updatePlanView
	select {
	case p = <-helper:
	case <-time.After(15 * time.Second):
		t.Fatal("the agent did not start the update helper")
	}
	if p.Version != "0.2.0" || filepath.Base(p.Package) != newName || filepath.Base(p.Previous) != oldName {
		t.Errorf("plan: %+v", p)
	}
	got, _ := os.ReadFile(p.Package)
	if sum := sha256.Sum256(got); hex.EncodeToString(sum[:]) != newSum {
		t.Error("downloaded package differs")
	}
	var d store.Device
	for i := 0; i < 400; i++ {
		admin.do("GET", "/api/v1/devices/"+id, nil, &d)
		if d.UpdateState == agentproto.UpdateInstalling {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if d.UpdateVersion != "0.2.0" || d.UpdateState != agentproto.UpdateInstalling {
		t.Fatalf("device update state: %q %q", d.UpdateVersion, d.UpdateState)
	}
	// A second tick does not send the update again while it is in progress.
	e.srv.agentUpdateTick(ctx)
	select {
	case <-helper:
		t.Fatal("update sent twice")
	case <-time.After(300 * time.Millisecond):
	}
	admin.do("GET", "/api/v1/agent/updates", nil, &view)
	if view.Rollouts[0].Status != store.RolloutCompleted {
		t.Errorf("rollout with soak 0 and one ring: %+v", view.Rollouts[0])
	}
	if len(view.Releases) != 2 || view.Releases[0].Version != "0.2.0" || !view.Releases[0].Deletable || view.Releases[1].Deletable {
		t.Errorf("releases: %+v", view.Releases)
	}

	// Upload: the name and file type are checked.
	upload := func(name string, data []byte) int {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		fw, _ := mw.CreateFormFile("file", name)
		fw.Write(data)
		mw.Close()
		req, _ := http.NewRequest("POST", e.ts.URL+"/api/v1/agent/releases", &b)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("X-Requested-With", "fleetwarden")
		res, err := admin.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	deb, _ := debpatch.BuildMinimal("fleetwarden-agent", "0.3.0")
	if code := upload("fleetwarden-agent_0.3.0_"+arch+".deb", deb); code != 200 {
		t.Errorf("upload: %d", code)
	}
	if code := upload("fleetwarden-agent_0.3.1_"+arch+".deb", []byte("not a package")); code != 400 {
		t.Errorf("upload of a non-package: %d", code)
	}
	if code := upload("setup.exe", deb); code != 400 {
		t.Errorf("upload with a wrong name: %d", code)
	}
	admin.do("GET", "/api/v1/agent/updates", nil, &view)
	if len(view.Releases) != 3 || view.Releases[0].Source != "upload" || view.Releases[0].Packages[0].SHA256 == "" {
		t.Errorf("after upload: %+v", view.Releases)
	}
	if code := admin.do("DELETE", "/api/v1/agent/releases/0.3.0", nil, nil); code != 200 {
		t.Errorf("delete release: %d", code)
	}
	if code := admin.do("PUT", "/api/v1/agent/updates/settings", map[string]any{"autoDownload": false, "autoRollout": false, "soakHours": 48, "maxFailures": 3, "keepReleases": 4}, &view); code != 200 || view.Settings.SoakHours != 48 {
		t.Errorf("settings: %d %+v", code, view.Settings)
	}
}

// updatePlanView mirrors the agent's plan file.
type updatePlanView struct {
	Version  string `json:"version"`
	Package  string `json:"package"`
	Previous string `json:"previous"`
}

func TestFetchAgentReleasesFromGitHub(t *testing.T) {
	e := newEnv(t)
	deb, _ := debpatch.BuildMinimal("fleetwarden-agent", "0.4.0")
	sum := sha256.Sum256(deb)
	sums := hex.EncodeToString(sum[:]) + "  fleetwarden-agent_0.4.0_amd64.deb\n"
	var base string
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/releases":
			fmt.Fprintf(w, `[{"tag_name":"v0.4.0","prerelease":false,"assets":[
				{"name":"fleetwarden-agent_0.4.0_amd64.deb","browser_download_url":"%[1]s/dl/deb","size":%[2]d},
				{"name":"SHA256SUMS","browser_download_url":"%[1]s/dl/sums"},
				{"name":"fleetwarden-server_0.4.0_amd64.deb","browser_download_url":"%[1]s/dl/server"}]},
				{"tag_name":"v0.3.0","assets":[{"name":"fleetwarden-agent_0.3.0_amd64.deb","browser_download_url":"%[1]s/dl/old"}]}]`, base, len(deb))
		case "/dl/deb":
			w.Write(deb)
		case "/dl/sums":
			w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	}))
	defer gh.Close()
	base = gh.URL
	c := updates.NewChecker("o/r")
	c.APIBase = gh.URL
	e.srv.updates = c
	ctx := context.Background()
	if err := e.srv.fetchAgentReleases(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.srv.agentPackages().version("linux-amd64.deb", "0.4.0"); !ok {
		t.Fatal("release was not downloaded")
	}
	rs, _ := e.st.ListRollouts(ctx, 10)
	if len(rs) != 1 || rs[0].Version != "0.4.0" || !rs[0].Automatic || rs[0].Status != store.RolloutActive {
		t.Fatalf("automatic rollout: %+v", rs)
	}
	// A second check neither downloads nor starts anything again.
	if err := e.srv.fetchAgentReleases(ctx, true); err != nil {
		t.Fatal(err)
	}
	if rs, _ := e.st.ListRollouts(ctx, 10); len(rs) != 1 {
		t.Errorf("rollouts after a second check: %d", len(rs))
	}

	// A package that does not match SHA256SUMS is refused.
	os.RemoveAll(e.srv.releaseDir("0.4.0"))
	sums = strings.Repeat("0", 64) + "  fleetwarden-agent_0.4.0_amd64.deb\n"
	if err := e.srv.fetchAgentReleases(ctx, true); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("bad checksum: %v", err)
	}
	if _, ok := e.srv.agentPackages().version("linux-amd64.deb", "0.4.0"); ok {
		t.Error("a package with a bad checksum was kept")
	}
	if set := e.srv.loadAgentUpdateSettings(ctx); set.LastError == "" {
		t.Error("the error was not recorded")
	}
}
