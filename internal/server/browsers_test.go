package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agent"
	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/catalog"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

func browserManifest(id, name, version, typ, url, sum string) string {
	return fmt.Sprintf(`PackageIdentifier: %s
PackageVersion: "%s"
PackageName: %s
Publisher: Vendor
InstallerType: %s
Scope: machine
Installers:
- Architecture: x64
  InstallerUrl: %s
  InstallerSha256: %s
`, id, version, name, typ, url, sum)
}

// browserHost is the user's side on a test device: Chrome is open and the
// user postpones once.
type browserHost struct {
	mu      sync.Mutex
	running map[string]bool
	prompts []agent.Prompt
	closed  []agent.CloseApp
	reopen  int
}

func (b *browserHost) Running(names []string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for _, n := range names {
		if b.running[n] {
			out = append(out, n)
		}
	}
	return out
}

func (b *browserHost) Kill(names []string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, n := range names {
		b.running[n] = false
	}
	return 0
}

func (b *browserHost) Ask(_ context.Context, p agent.Prompt) (agent.Answer, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prompts = append(b.prompts, p)
	return agent.Answer{Postpone: len(b.prompts) == 1}, nil
}

func (b *browserHost) CloseApps(_ context.Context, _ string, apps []agent.CloseApp, _ time.Duration) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = append(b.closed, apps...)
	var keys []string
	for _, a := range apps {
		keys = append(keys, a.Key)
		b.running[a.Process+".exe"] = false
	}
	return keys
}

func (b *browserHost) LaunchApps(context.Context, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reopen++
}

func TestBrowserUpdates(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()

	files := map[string][]byte{}
	sums := map[string]string{}
	for _, f := range []string{"chrome.msi", "edge.msi", "firefox-nl.exe", "firefox.exe"} {
		files[f] = []byte("installer " + f)
		s := sha256.Sum256(files[f])
		sums[f] = hex.EncodeToString(s[:])
	}
	vendor := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(files[strings.TrimPrefix(r.URL.Path, "/")])
	}))
	defer vendor.Close()
	cdn, err := catalog.FakeCDN(t.TempDir(), []catalog.FakePackage{
		{ID: "Google.Chrome", Name: "Google Chrome", Version: "157.0.1", Manifest: browserManifest("Google.Chrome", "Google Chrome", "157.0.1", "wix", vendor.URL+"/chrome.msi", sums["chrome.msi"])},
		{ID: "Microsoft.Edge", Name: "Microsoft Edge", Version: "156.0", Manifest: browserManifest("Microsoft.Edge", "Microsoft Edge", "156.0", "wix", vendor.URL+"/edge.msi", sums["edge.msi"])},
		{ID: "Mozilla.Firefox", Name: "Mozilla Firefox (en-US)", Version: "159.0", Manifest: browserManifest("Mozilla.Firefox", "Mozilla Firefox (en-US)", "159.0", "nullsoft", vendor.URL+"/firefox.exe", sums["firefox.exe"])},
		{ID: "Mozilla.Firefox.nl", Name: "Mozilla Firefox (nl)", Version: "159.0", Manifest: browserManifest("Mozilla.Firefox.nl", "Mozilla Firefox (nl)", "159.0", "nullsoft", vendor.URL+"/firefox-nl.exe", sums["firefox-nl.exe"])},
		{ID: "Mozilla.Firefox.ESR", Name: "Mozilla Firefox ESR (en-US)", Version: "153.6.0", Manifest: browserManifest("Mozilla.Firefox.ESR", "Mozilla Firefox ESR", "153.6.0", "nullsoft", vendor.URL+"/firefox.exe", sums["firefox.exe"])},
	})
	if err != nil {
		t.Fatal(err)
	}
	cdnTS := httptest.NewServer(cdn)
	defer cdnTS.Close()
	e.srv.winget = &catalog.Winget{Dir: t.TempDir(), BaseURL: cdnTS.URL}
	if code := admin.do("POST", "/api/v1/deployments", map[string]any{"action": "browsers", "browsers": []string{"chrome"}, "groupIds": []int64{1}}, nil); code != 400 {
		t.Errorf("browser update before the catalog is loaded: %d", code)
	}
	if code := admin.do("POST", "/api/v1/software/catalog/refresh", nil, nil); code != 200 {
		t.Fatal("catalog refresh")
	}

	// A Windows device with an old Chrome (open) and a Dutch Firefox.
	fs := newFakeSoftware()
	fs.installed = map[string]string{"Google Chrome": "150.0", "Mozilla Firefox (x64 nl)": "158.0"}
	fs.byFile["chrome.msi"] = [2]string{"Google Chrome", "157.0.1"}
	fs.byFile["firefox-nl.exe"] = [2]string{"Mozilla Firefox (x64 nl)", "159.0"}
	host := &browserHost{running: map[string]bool{"chrome.exe": true}}
	_, info := e.createToken(admin, 1)
	a := newTestAgent(t, e, info.Token, "PC-BR1", false)
	a.Software, a.HTTP, a.Processes, a.Notify, a.Minute = fs, &http.Client{Transport: vendor.Client().Transport}, host, host, time.Millisecond
	inv := a.Collect
	a.Collect = func(ctx context.Context) (agentproto.Inventory, error) {
		v, err := inv(ctx)
		fs.mu.Lock()
		v.Software = nil
		for n, ver := range fs.installed {
			v.Software = append(v.Software, agentproto.Software{Name: n, Version: ver, Source: "registry"})
		}
		fs.mu.Unlock()
		return v, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	go a.Run(ctx) //nolint:errcheck
	d := e.onlineDevice(admin, "PC-BR1")
	setPlatform(t, d.ID, "windows")

	var rep browserReport
	if code := admin.do("GET", "/api/v1/software/browsers", nil, &rep); code != 200 || len(rep.Browsers) != 3 || len(rep.Devices) != 1 {
		t.Fatalf("report: %d %+v", code, rep)
	}
	if b := rep.Browsers[0]; b.Key != "chrome" || b.Latest != "157.0.1" || b.Devices != 1 || b.UpToDate != 0 {
		t.Errorf("chrome: %+v", b)
	}
	if row := rep.Devices[0]; !row.Outdated || row.Browsers["firefox"].Version != "158.0" || row.Browsers["firefox"].UpToDate || len(row.Browsers) != 2 {
		t.Errorf("device row: %+v", row)
	}

	if code := admin.do("POST", "/api/v1/deployments", map[string]any{"action": "browsers", "browsers": []string{"opera"}, "deviceIds": []string{d.ID}}, nil); code != 400 {
		t.Errorf("unknown browser: %d", code)
	}
	var dep deploymentCreated
	if code := admin.do("POST", "/api/v1/deployments", map[string]any{"action": "browsers", "browsers": []string{"firefox", "chrome", "edge"}, "deviceIds": []string{d.ID}}, &dep); code != 201 ||
		dep.Jobs != 1 || dep.Platform != "windows" || dep.Name != "Browser updates: Google Chrome, Microsoft Edge, Mozilla Firefox" || len(dep.Browsers) != 3 {
		t.Fatalf("deploy: %d %+v", code, dep)
	}
	var job store.SoftwareJob
	waitFor(t, "browser job", func() bool {
		e.srv.softwareTick(ctx)
		var v deploymentView
		admin.do("GET", fmt.Sprintf("/api/v1/deployments/%d", dep.ID), nil, &v)
		if len(v.Jobs) == 1 {
			job = v.Jobs[0]
		}
		return agentproto.SoftwareFinished(job.State)
	})
	if job.State != "completed" || !strings.Contains(job.Message, "Google Chrome 150.0 → 157.0.1") || !strings.Contains(job.Message, "Mozilla Firefox 158.0 → 159.0") ||
		!strings.Contains(job.Message, "Microsoft Edge is not installed") {
		t.Fatalf("job: %+v", job)
	}
	fs.mu.Lock()
	var urls []string
	for _, in := range fs.installs {
		urls = append(urls, in.URL)
	}
	fs.mu.Unlock()
	if len(urls) != 2 || !strings.HasSuffix(urls[0], "/chrome.msi") || !strings.HasSuffix(urls[1], "/firefox-nl.exe") {
		t.Errorf("installers: %v (the Dutch Firefox must stay Dutch)", urls)
	}
	host.mu.Lock()
	if len(host.prompts) != 2 || host.prompts[0].Apps[0] != "Google Chrome" || host.prompts[0].PostponesLeft != 3 || host.prompts[0].CountdownSeconds != 60 ||
		len(host.closed) != 1 || host.closed[0].RestoreArgs != "--restore-last-session" || host.reopen != 1 {
		t.Errorf("user side: prompts %+v closed %+v reopened %d", host.prompts, host.closed, host.reopen)
	}
	host.mu.Unlock()

	// The report follows the new inventory.
	waitFor(t, "fresh inventory", func() bool {
		admin.do("GET", "/api/v1/software/browsers", nil, &rep)
		return len(rep.Devices) == 1 && !rep.Devices[0].Outdated
	})

	// The postpone settings come from the group's patch policy.
	cancel()
	var pv policyView
	if code := admin.do("POST", "/api/v1/patches/policies", allDayPolicy("Office"), &pv); code != 201 {
		t.Fatal("policy")
	}
	admin.do("PUT", "/api/v1/groups/1", map[string]any{"name": "Default", "updateRing": 1, "patchPolicyId": pv.ID}, nil)
	full, err := e.st.DeploymentByID(context.Background(), dep.ID, store.Scope{All: true})
	if err != nil {
		t.Fatal(err)
	}
	built, err := e.srv.buildSoftwareJob(context.Background(), store.DueJob{JobID: 1, DeviceID: d.ID, Arch: "amd64", Deployment: full})
	if err != nil || built.Prompt == nil || built.Prompt.MaxPostpones != 1 || built.Prompt.PostponeMinutes != 5 || built.TimeoutMinutes != 65 || len(built.Browsers) != 3 {
		t.Errorf("job with policy: %v %+v", err, built)
	}
	if b := built.Browsers[1]; b.Key != "edge" || !b.Detect.Exact || !b.Detect.MachineOnly || b.Version != "156.0" || b.Installer.Kind != "msi" {
		t.Errorf("edge: %+v", b)
	}
}

func TestFirefoxVariant(t *testing.T) {
	for name, want := range map[string]string{
		"Mozilla Firefox (x64 nl)":        "Mozilla.Firefox.nl",
		"Mozilla Firefox (x64 en-US)":     "Mozilla.Firefox",
		"Mozilla Firefox ESR (x64 en-GB)": "Mozilla.Firefox.ESR.en-GB",
		"Mozilla Firefox ESR (x86 en-US)": "Mozilla.Firefox.ESR",
		"Mozilla Maintenance Service":     "Mozilla.Firefox",
	} {
		if got := firefoxVariantOf(name).WingetID; got != want {
			t.Errorf("%s: %s, want %s", name, got, want)
		}
	}
	if p := firefoxVariantOf("Mozilla Firefox ESR (x64 de)").Prefix; p != "Mozilla Firefox ESR (" {
		t.Errorf("prefix %q", p)
	}
}
