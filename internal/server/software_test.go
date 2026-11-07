package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rhisijmuiden/fleetwarden/internal/agent"
	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/catalog"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

// fakeSoftware is a SoftwareBackend that keeps a list of installed programs.
type fakeSoftware struct {
	mu         sync.Mutex
	installed  map[string]string // display name -> version
	byFile     map[string][2]string
	installs   []agentproto.Installer
	contents   [][]byte
	uninstalls []agentproto.Uninstall
	failNext   int
}

func newFakeSoftware() *fakeSoftware {
	return &fakeSoftware{installed: map[string]string{"Mozilla Firefox": "133.0"}, byFile: map[string][2]string{}}
}

func (f *fakeSoftware) Find(_ context.Context, d agentproto.Detect) (bool, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for n, v := range f.installed {
		if d.Name != "" && strings.HasPrefix(strings.ToLower(n), strings.ToLower(d.Name)) {
			return true, v, nil
		}
	}
	return false, "", nil
}

func (f *fakeSoftware) Install(_ context.Context, in agentproto.Installer, file string) (agent.RunResult, error) {
	var raw []byte
	if file != "" {
		var err error
		if raw, err = os.ReadFile(file); err != nil {
			return agent.RunResult{}, err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installs = append(f.installs, in)
	f.contents = append(f.contents, raw)
	if f.failNext > 0 {
		f.failNext--
		return agent.RunResult{Ran: true, ExitCode: 1603, Output: "MSI (s) Product: Test -- Installation failed."}, errors.New("exit code 1603: fatal error during installation")
	}
	if nv, ok := f.byFile[in.FileName]; ok {
		f.installed[nv[0]] = nv[1]
	}
	return agent.RunResult{Ran: true, ExitCode: 3010, Reboot: true}, nil
}

func (f *fakeSoftware) Uninstall(_ context.Context, u agentproto.Uninstall) (agent.RunResult, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uninstalls = append(f.uninstalls, u)
	if _, ok := f.installed[u.Name]; !ok {
		return agent.RunResult{}, false, nil
	}
	delete(f.installed, u.Name)
	return agent.RunResult{Ran: true}, true, nil
}

func (f *fakeSoftware) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.installs)
}

// setPlatform makes an enrolled test device a Windows machine.
func setPlatform(t *testing.T, id, platform string) {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), os.Getenv("FW_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(context.Background(), "UPDATE devices SET platform = $2, arch = 'amd64' WHERE id::text = $1", id, platform); err != nil {
		t.Fatal(err)
	}
}

func (c *client) upload(name string, body []byte, out any) int {
	c.e.t.Helper()
	req, _ := http.NewRequest("POST", c.e.ts.URL+"/api/v1/software/uploads", bytes.NewReader(body))
	req.Header.Set("X-Requested-With", "fleetwarden")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-File-Name", name)
	res, err := c.c.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if out != nil {
		_ = json.Unmarshal(raw, out)
	}
	return res.StatusCode
}

func toolManifest(url, sum string) string {
	return fmt.Sprintf(`PackageIdentifier: Test.Tool
PackageVersion: "2.0"
PackageName: Test Tool
Publisher: Test Corp
InstallerType: nullsoft
Scope: machine
Installers:
- Architecture: x64
  InstallerUrl: %s
  InstallerSha256: %s
`, url, sum)
}

func TestSoftwareDeployment(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()

	// A vendor download site and a winget catalog that points to it.
	setup := []byte("MZ pretend installer")
	sum := sha256.Sum256(setup)
	vendor := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(setup) }))
	defer vendor.Close()
	cdn, err := catalog.FakeCDN(t.TempDir(), []catalog.FakePackage{
		{ID: "Test.Tool", Name: "Test Tool", Moniker: "tool", Version: "2.0", Manifest: toolManifest(vendor.URL+"/tool-setup.exe", hex.EncodeToString(sum[:]))},
		{ID: "Test.Bad", Name: "Bad Hash", Version: "1.0", Manifest: strings.NewReplacer("Test.Tool", "Test.Bad", "Test Tool", "Bad Hash").Replace(toolManifest(vendor.URL+"/bad.exe", strings.Repeat("ab", 32)))},
	})
	if err != nil {
		t.Fatal(err)
	}
	cdnTS := httptest.NewServer(cdn)
	defer cdnTS.Close()
	e.srv.winget = &catalog.Winget{Dir: t.TempDir(), BaseURL: cdnTS.URL}

	var pv packagesView
	if code := admin.do("GET", "/api/v1/software/packages", nil, &pv); code != 200 || pv.Catalog.Packages != 0 || len(pv.Kinds["windows"]) != 5 {
		t.Fatalf("packages: %d %+v", code, pv)
	}
	if code := admin.do("POST", "/api/v1/software/packages", map[string]any{"name": "Test Tool", "source": "winget", "wingetId": "Test.Tool"}, nil); code != 400 {
		t.Errorf("winget package before the catalog is loaded: %d", code)
	}
	var st catalog.Status
	if code := admin.do("POST", "/api/v1/software/catalog/refresh", nil, &st); code != 200 || st.Packages != 2 {
		t.Fatalf("catalog refresh: %d %+v", code, st)
	}
	var found catalogSearch
	if code := admin.do("GET", "/api/v1/software/catalog?q=tool", nil, &found); code != 200 || len(found.Results) != 1 || found.Results[0].ID != "Test.Tool" {
		t.Fatalf("catalog search: %d %+v", code, found)
	}
	var cp catalogPackageView
	if code := admin.do("GET", "/api/v1/software/catalog/package?id=Test.Tool", nil, &cp); code != 200 || cp.Choice == nil ||
		cp.Choice.Installer.Kind != "nullsoft" || cp.Publisher != "Test Corp" || !slices.Equal(cp.Versions, []string{"2.0"}) {
		t.Fatalf("catalog package: %d %+v", code, cp)
	}

	// The library: a winget package, an uploaded MSI and an apt package.
	var tool, bad, msi, apt store.SoftwarePackage
	if code := admin.do("POST", "/api/v1/software/packages", map[string]any{"name": "Test Tool", "source": "winget", "wingetId": "test.tool"}, &tool); code != 201 || tool.Platform != "windows" {
		t.Fatalf("winget package: %d %+v", code, tool)
	}
	if code := admin.do("POST", "/api/v1/software/packages", map[string]any{"name": "test tool", "source": "winget", "wingetId": "Test.Tool"}, nil); code != 409 {
		t.Errorf("duplicate package: %d", code)
	}
	admin.do("POST", "/api/v1/software/packages", map[string]any{"name": "Bad Hash", "source": "winget", "wingetId": "Test.Bad"}, &bad)
	var up uploadResult
	msiBody := []byte("pretend MSI database")
	if code := admin.upload("Line-of-business App 1.0.msi", msiBody, &up); code != 201 || up.Kind != "msi" || up.Size != int64(len(msiBody)) {
		t.Fatalf("upload: %d %+v", code, up)
	}
	if code := admin.upload("notes.txt", []byte("x"), nil); code != 400 {
		t.Errorf("upload of a text file: %d", code)
	}
	if code := admin.do("POST", "/api/v1/software/packages", map[string]any{"name": "LOB", "source": "upload", "fileSha256": up.SHA256,
		"fileName": up.Name, "installerKind": "exe"}, nil); code != 400 {
		t.Errorf("exe without silent arguments: %d", code)
	}
	if code := admin.do("POST", "/api/v1/software/packages", map[string]any{"name": "LOB App", "source": "upload", "fileSha256": up.SHA256,
		"fileName": up.Name, "installerKind": "msi", "installArgs": "SERVER=lob.corp.example", "detectName": "LOB App", "version": "1.0"}, &msi); code != 201 ||
		msi.FileSize != int64(len(msiBody)) || msi.Platform != "windows" {
		t.Fatalf("upload package: %d %+v", code, msi)
	}
	if code := admin.do("POST", "/api/v1/software/packages", map[string]any{"name": "htop", "source": "apt", "aptPackage": "htop"}, &apt); code != 201 || apt.Platform != "linux" {
		t.Fatalf("apt package: %d %+v", code, apt)
	}
	if code := admin.do("POST", "/api/v1/software/packages", map[string]any{"name": "evil", "source": "apt", "aptPackage": "htop; rm -rf /"}, nil); code != 400 {
		t.Errorf("bad apt name: %d", code)
	}

	// Two Windows devices in a group.
	var grp store.DeviceGroup
	admin.do("POST", "/api/v1/groups", groupReq{Name: "Office", UpdateRing: 1}, &grp)
	_, info := e.createToken(admin, grp.ID)
	fs1 := newFakeSoftware()
	fs1.byFile["tool-setup.exe"] = [2]string{"Test Tool 2.0", "2.0"}
	fs1.byFile[up.Name] = [2]string{"LOB App", "1.0"}
	a1 := newTestAgent(t, e, info.Token, "PC-SW1", false)
	a1.Software, a1.HTTP = fs1, &http.Client{Transport: vendor.Client().Transport}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	go a1.Run(ctx) //nolint:errcheck
	d1 := e.onlineDevice(admin, "PC-SW1")
	setPlatform(t, d1.ID, "windows")

	// Install the winget package on the group, also on devices that join later.
	var dep deploymentCreated
	if code := admin.do("POST", "/api/v1/deployments", map[string]any{"action": "install", "packageId": tool.ID, "groupIds": []int64{grp.ID}, "ongoing": true}, &dep); code != 201 ||
		dep.Jobs != 1 || dep.Platform != "windows" || dep.Name != "Test Tool" {
		t.Fatalf("deploy: %d %+v", code, dep)
	}
	jobsOf := func(id int64) []store.SoftwareJob {
		var v deploymentView
		if code := admin.do("GET", fmt.Sprintf("/api/v1/deployments/%d", id), nil, &v); code != 200 {
			t.Fatalf("deployment %d: %d", id, code)
		}
		return v.Jobs
	}
	waitDone := func(id int64, n int) []store.SoftwareJob {
		t.Helper()
		var jobs []store.SoftwareJob
		waitFor(t, fmt.Sprintf("deployment %d", id), func() bool {
			e.srv.softwareTick(ctx)
			jobs = jobsOf(id)
			done := 0
			for _, j := range jobs {
				if agentproto.SoftwareFinished(j.State) {
					done++
				}
			}
			return len(jobs) == n && done == n
		})
		return jobs
	}
	jobs := waitDone(dep.ID, 1)
	if j := jobs[0]; j.State != "completed" || !j.RebootRequired || j.ExitCode == nil || *j.ExitCode != 3010 || j.Version != "2.0" || j.Attempts != 1 {
		t.Errorf("job: %+v", j)
	}
	fs1.mu.Lock()
	if len(fs1.installs) != 1 || fs1.installs[0].Kind != "nullsoft" || fs1.installs[0].Args != "/S" || !bytes.Equal(fs1.contents[0], setup) {
		t.Errorf("installed: %+v", fs1.installs)
	}
	fs1.mu.Unlock()

	// A second device joins the group and gets the ongoing deployment.
	fs2 := newFakeSoftware()
	fs2.byFile["tool-setup.exe"] = [2]string{"Test Tool", "2.0"}
	a2 := newTestAgent(t, e, info.Token, "PC-SW2", false)
	a2.Software, a2.HTTP = fs2, &http.Client{Transport: vendor.Client().Transport}
	go a2.Run(ctx) //nolint:errcheck
	d2 := e.onlineDevice(admin, "PC-SW2")
	setPlatform(t, d2.ID, "windows")
	waitDone(dep.ID, 2)
	if fs2.count() != 1 {
		t.Errorf("second device installs: %d", fs2.count())
	}

	// Already installed: the job is skipped.
	var dep2 deploymentCreated
	admin.do("POST", "/api/v1/deployments", map[string]any{"action": "install", "packageId": tool.ID, "deviceIds": []string{d1.ID}}, &dep2)
	if jobs := waitDone(dep2.ID, 1); jobs[0].State != "skipped" || !strings.Contains(jobs[0].Message, "already installed") {
		t.Errorf("skip: %+v", jobs[0])
	}

	// An uploaded MSI comes from the server; only devices with a job may fetch it.
	fs1.mu.Lock()
	fs1.failNext = 1
	fs1.mu.Unlock()
	var dep3 deploymentCreated
	admin.do("POST", "/api/v1/deployments", map[string]any{"action": "install", "packageId": msi.ID, "deviceIds": []string{d1.ID}}, &dep3)
	jobs = waitDone(dep3.ID, 1)
	if j := jobs[0]; j.State != "failed" || j.ExitCode == nil || *j.ExitCode != 1603 || !strings.Contains(j.Output, "Installation failed") {
		t.Errorf("failed job: %+v", j)
	}
	if ok, _ := e.st.DeviceMayDownload(ctx, d2.ID, up.SHA256); ok {
		t.Error("a device without a job may download the file")
	}
	var retried map[string]int
	if code := admin.do("POST", fmt.Sprintf("/api/v1/deployments/%d/retry", dep3.ID), nil, &retried); code != 200 || retried["jobs"] != 1 {
		t.Fatalf("retry: %d %+v", code, retried)
	}
	jobs = waitDone(dep3.ID, 1)
	if j := jobs[0]; j.State != "completed" || j.Attempts != 2 || j.Version != "1.0" {
		t.Errorf("retried job: %+v", j)
	}
	fs1.mu.Lock()
	last := fs1.installs[len(fs1.installs)-1]
	lastBody := fs1.contents[len(fs1.contents)-1]
	fs1.mu.Unlock()
	if last.Kind != "msi" || last.Args != "SERVER=lob.corp.example" || last.File != up.SHA256 || !bytes.Equal(lastBody, msiBody) {
		t.Errorf("msi install: %+v", last)
	}
	resp, _ := http.Get(e.ts.URL + agentproto.PathSoftware + up.SHA256)
	if resp.StatusCode != 401 {
		t.Errorf("unsigned download: %d", resp.StatusCode)
	}
	resp.Body.Close()

	// A changed file at the vendor fails the hash check.
	var dep4 deploymentCreated
	admin.do("POST", "/api/v1/deployments", map[string]any{"action": "install", "packageId": bad.ID, "deviceIds": []string{d2.ID}}, &dep4)
	if jobs := waitDone(dep4.ID, 1); jobs[0].State != "failed" || !strings.Contains(jobs[0].Message, "SHA-256") {
		t.Errorf("hash mismatch: %+v", jobs[0])
	}

	// Uninstall a program from the inventory on both devices.
	var installed []store.InstalledSoftware
	if code := admin.do("GET", "/api/v1/software/installed?search=firefox", nil, &installed); code != 200 || len(installed) != 1 || installed[0].Devices != 2 {
		t.Fatalf("installed: %d %+v", code, installed)
	}
	var holders []store.InstalledDevice
	admin.do("GET", "/api/v1/software/installed/devices?name=Mozilla+Firefox", nil, &holders)
	if len(holders) != 2 {
		t.Fatalf("devices with firefox: %+v", holders)
	}
	fs2.mu.Lock()
	delete(fs2.installed, "Mozilla Firefox") // removed by hand in the meantime
	fs2.mu.Unlock()
	var dep5 deploymentCreated
	if code := admin.do("POST", "/api/v1/deployments", map[string]any{"action": "uninstall", "deviceIds": []string{d1.ID, d2.ID},
		"uninstall": map[string]any{"name": "Mozilla Firefox", "platform": "windows", "args": "/S"}}, &dep5); code != 201 || dep5.Jobs != 2 {
		t.Fatalf("uninstall: %d %+v", code, dep5)
	}
	states := map[string]string{}
	for _, j := range waitDone(dep5.ID, 2) {
		states[j.Hostname] = j.State
	}
	if states["PC-SW1"] != "completed" || states["PC-SW2"] != "skipped" {
		t.Errorf("uninstall states: %+v", states)
	}
	fs1.mu.Lock()
	if len(fs1.uninstalls) != 1 || fs1.uninstalls[0].Args != "/S" {
		t.Errorf("uninstalls: %+v", fs1.uninstalls)
	}
	fs1.mu.Unlock()
	if code := admin.do("POST", "/api/v1/deployments", map[string]any{"action": "uninstall", "deviceIds": []string{d1.ID},
		"uninstall": map[string]any{"name": "FleetWarden Agent", "platform": "windows"}}, nil); code != 400 {
		t.Errorf("uninstall the agent: %d", code)
	}

	// A planned deployment waits, and can be cancelled.
	var dep6 deploymentCreated
	admin.do("POST", "/api/v1/deployments", map[string]any{"action": "install", "packageId": tool.ID, "deviceIds": []string{d2.ID},
		"startAt": time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)}, &dep6)
	e.srv.softwareTick(ctx)
	if jobs := jobsOf(dep6.ID); len(jobs) != 1 || jobs[0].State != "pending" {
		t.Errorf("planned: %+v", jobs)
	}
	if code := admin.do("POST", fmt.Sprintf("/api/v1/deployments/%d/cancel", dep6.ID), nil, nil); code != 204 {
		t.Errorf("cancel: %d", code)
	}
	if jobs := jobsOf(dep6.ID); jobs[0].State != "cancelled" {
		t.Errorf("cancelled: %+v", jobs)
	}

	// The apt package does not target Windows devices.
	if code := admin.do("POST", "/api/v1/deployments", map[string]any{"action": "install", "packageId": apt.ID, "groupIds": []int64{grp.ID}}, &dep); code != 201 || dep.Jobs != 0 {
		t.Errorf("apt on windows devices: %d %+v", code, dep)
	}

	// Device history and the list.
	var devJobs []store.SoftwareJob
	if code := admin.do("GET", "/api/v1/devices/"+d1.ID+"/software-jobs", nil, &devJobs); code != 200 || len(devJobs) < 4 {
		t.Errorf("device jobs: %d %d", code, len(devJobs))
	}
	var deps []store.Deployment
	if code := admin.do("GET", "/api/v1/deployments", nil, &deps); code != 200 || len(deps) != 7 || deps[len(deps)-1].Counts.Completed != 2 {
		t.Errorf("deployments: %d %+v", code, deps)
	}
	if code := admin.do("GET", "/api/v1/software/packages", nil, &pv); code != 200 || len(pv.Packages) != 4 {
		t.Fatalf("packages: %d", code)
	}
	for _, p := range pv.Packages {
		if p.Name == "Test Tool" && p.Installed != 0 {
			// The fake inventory does not list the tool.
			t.Errorf("installed count: %+v", p)
		}
	}

	// Deleting a package keeps the history and stops what did not start.
	if code := admin.do("DELETE", fmt.Sprintf("/api/v1/software/packages/%d", msi.ID), nil, nil); code != 204 {
		t.Errorf("delete package: %d", code)
	}
	if jobs := jobsOf(dep3.ID); len(jobs) != 1 || jobs[0].State != "completed" {
		t.Errorf("history after delete: %+v", jobs)
	}

	// Read-only users see, but cannot deploy.
	var created userWithPassword
	admin.do("POST", "/api/v1/users", createUserReq{Username: "viewer", Role: "readonly"}, &created)
	ro := e.client()
	ro.login("viewer", created.OneTimePassword)
	if code := ro.do("POST", "/api/v1/me/password", map[string]string{"current": created.OneTimePassword, "new": "another-secret-pass"}, nil); code != 200 {
		t.Fatalf("viewer password: %d %+v", code, created)
	}
	if code := ro.do("GET", "/api/v1/deployments", nil, nil); code != 200 {
		t.Errorf("read-only list: %d", code)
	}
	if code := ro.do("POST", "/api/v1/deployments", map[string]any{"action": "install", "packageId": tool.ID, "deviceIds": []string{d1.ID}}, nil); code != 403 {
		t.Errorf("read-only deploy: %d", code)
	}
}
