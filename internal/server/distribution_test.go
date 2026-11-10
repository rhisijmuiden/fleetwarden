package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agent"
	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/distproto"
	"github.com/rhisijmuiden/fleetwarden/internal/distribution"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

func (e *env) distState(c *client, id int64) distView {
	e.t.Helper()
	var v distListView
	if code := c.do("GET", "/api/v1/distribution", nil, &v); code != 200 {
		e.t.Fatalf("list distribution servers: %d", code)
	}
	for _, d := range v.Servers {
		if d.ID == id {
			return d
		}
	}
	return distView{}
}

type testDist struct {
	cfg distribution.Config
	srv *distribution.Server
	ts  *httptest.Server
}

// startDist enrolls a distribution server with the token and serves it.
func startDist(t *testing.T, e *env, token string) *testDist {
	t.Helper()
	dir := t.TempDir()
	cfg := distribution.Config{Listen: "127.0.0.1:0", DataDir: dir, ContentDir: filepath.Join(dir, "content")}
	if _, err := distribution.Enroll(context.Background(), cfg, e.ts.URL, "wrong-token", "9.9.9-dist"); err == nil {
		t.Fatal("enrolled with a wrong token")
	}
	id, err := distribution.Enroll(context.Background(), cfg, e.ts.URL, token, "9.9.9-dist")
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if _, err := distribution.Enroll(context.Background(), cfg, e.ts.URL, token, "9.9.9-dist"); err == nil {
		t.Fatal("the token worked twice")
	}
	if id.Server != e.ts.URL || !strings.HasPrefix(id.ID, distproto.IDPrefix) {
		t.Fatalf("identity: %+v", id)
	}
	s, err := distribution.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), "9.9.9-dist")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(s.Handler())
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{s.Certificate()}}
	ts.StartTLS()
	t.Cleanup(ts.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go s.Loop(ctx)
	return &testDist{cfg: cfg, srv: s, ts: ts}
}

func TestDistributionServer(t *testing.T) {
	e := newEnv(t)
	t.Cleanup(e.srv.bgCancel)
	admin, _ := e.signedInAdmin()

	var branch store.DeviceGroup
	admin.do("POST", "/api/v1/groups", groupReq{Name: "Branch Utrecht", UpdateRing: 1}, &branch)

	var apiErr apiErr
	if code := admin.do("POST", "/api/v1/distribution", distReq{Name: "Utrecht", Networks: []string{"10.1.0.0/33"}}, &apiErr); code != 400 || apiErr.Error != "invalid_network" {
		t.Fatalf("bad network: %d %+v", code, apiErr)
	}
	if code := admin.do("POST", "/api/v1/distribution", distReq{Name: "Utrecht", URL: "http://10.1.0.5"}, &apiErr); code != 400 || apiErr.Error != "invalid_url" {
		t.Fatalf("plain http: %d %+v", code, apiErr)
	}
	var created distTokenView
	if code := admin.do("POST", "/api/v1/distribution", distReq{Name: "Utrecht", Networks: []string{"10.1.0.0/16", " 10.1.2.3 "}, GroupIDs: []int64{branch.ID}, BandwidthKbps: 0}, &created); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if !strings.HasPrefix(created.Token, "fwd_") || !strings.Contains(created.Command, "--token "+created.Token) || !strings.Contains(created.Command, "--server "+e.ts.URL) ||
		created.Server.State != "pending" || len(created.Server.Networks) != 2 || created.Server.Networks[1] != "10.1.2.3/32" {
		t.Fatalf("created: %+v", created)
	}
	if code := admin.do("POST", "/api/v1/distribution", distReq{Name: "utrecht"}, &apiErr); code != 409 {
		t.Errorf("duplicate name: %d", code)
	}

	// Nothing to fetch without a signature.
	if res, err := http.Get(e.ts.URL + distproto.PathManifest); err != nil || res.StatusCode != 401 {
		t.Errorf("unsigned manifest: %v %v", res, err)
	}

	td := startDist(t, e, created.Token)
	id := created.Server.ID
	waitFor(t, "first report", func() bool { v := e.distState(admin, id); return v.State == "online" && v.Status != nil })
	v := e.distState(admin, id)
	if !v.Enrolled || v.Version != "9.9.9-dist" || !distproto.ValidSHA(v.CertSHA256) || v.Status == nil || v.Status.Hostname == "" {
		t.Fatalf("after enrollment: %+v", v)
	}
	// Agents reach it on the test listener.
	if code := admin.do("PUT", fmt.Sprintf("/api/v1/distribution/%d", id), distReq{Name: "Utrecht", URL: td.ts.URL, Networks: v.Networks, GroupIDs: v.GroupIDs}, &v); code != 200 || v.URL != td.ts.URL {
		t.Fatalf("set url: %d %+v", code, v)
	}

	// An uploaded installer ends up on the distribution server.
	payload := bytes.Repeat([]byte("pretend MSI database "), 5000)
	var up uploadResult
	if code := admin.upload("Branch App 1.0.msi", payload, &up); code != 201 {
		t.Fatalf("upload: %d", code)
	}
	var pkg store.SoftwarePackage
	if code := admin.do("POST", "/api/v1/software/packages", map[string]any{"name": "Branch App", "source": "upload", "fileSha256": up.SHA256,
		"fileName": up.Name, "installerKind": "msi", "detectName": "Branch App", "version": "1.0"}, &pkg); code != 201 {
		t.Fatalf("package: %d", code)
	}
	e.srv.invalidateDistManifest()
	waitFor(t, "file on the distribution server", func() bool {
		_ = td.srv.Report(context.Background())
		if _, err := os.Stat(filepath.Join(td.cfg.ContentDir, up.SHA256)); err != nil {
			return false
		}
		st := e.distState(admin, id).Status
		return st != nil && st.PendingFiles == 0 && st.Files >= 1
	})

	// A device in the branch group is told about the server and installs from it.
	_, info := e.createToken(admin, branch.ID)
	fs := newFakeSoftware()
	fs.byFile[up.Name] = [2]string{"Branch App", "1.0"}
	a := newTestAgent(t, e, info.Token, "PC-BRANCH", false)
	a.Software = fs
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	go a.Run(ctx) //nolint:errcheck
	dev := e.onlineDevice(admin, "PC-BRANCH")
	setPlatform(t, dev.ID, "windows")
	waitFor(t, "assignment", func() bool {
		e.srv.assignDistribution(ctx)
		return e.distState(admin, id).Agents == 1
	})

	// Without a job the distribution server refuses the file, as the server would.
	if code := agentGet(t, a, dev.ID, td.ts, agentproto.PathSoftware+up.SHA256); code != 404 {
		t.Errorf("file without a job: %d", code)
	}
	var dep deploymentCreated
	if code := admin.do("POST", "/api/v1/deployments", map[string]any{"action": "install", "packageId": pkg.ID, "deviceIds": []string{dev.ID}}, &dep); code != 201 {
		t.Fatalf("deploy: %d", code)
	}
	waitFor(t, "installed", func() bool {
		e.srv.softwareTick(ctx)
		return fs.count() == 1
	})
	fs.mu.Lock()
	if !bytes.Equal(fs.contents[0], payload) {
		t.Error("installed the wrong bytes")
	}
	fs.mu.Unlock()
	waitFor(t, "served count", func() bool {
		st := td.srv.Status()
		return st.ServedFiles == 1 && st.ServedBytes == int64(len(payload)) && st.Refused == 1
	})

	// Disabled: agents are told to stop using it and it fetches nothing.
	if code := admin.do("PUT", fmt.Sprintf("/api/v1/distribution/%d", id), map[string]any{"name": "Utrecht", "url": td.ts.URL, "networks": v.Networks,
		"groupIds": v.GroupIDs, "enabled": false}, &v); code != 200 || v.State != "disabled" {
		t.Fatalf("disable: %d %+v", code, v)
	}
	if v := e.distState(admin, id); v.Agents != 0 {
		t.Errorf("agents after disable: %d", v.Agents)
	}

	// A dev build cannot ask for a release.
	if code := admin.do("POST", fmt.Sprintf("/api/v1/distribution/%d/update", id), nil, &apiErr); code != 409 || apiErr.Error != "version_not_offered" {
		t.Errorf("update from a dev build: %d %+v", code, apiErr)
	}
	var tok distTokenView
	if code := admin.do("POST", fmt.Sprintf("/api/v1/distribution/%d/token", id), nil, &tok); code != 200 || tok.Token == created.Token || tok.Token == "" {
		t.Errorf("new token: %d %+v", code, tok)
	}
	if code := admin.do("DELETE", fmt.Sprintf("/api/v1/distribution/%d", id), nil, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
}

// agentGet fetches path from a distribution server as the agent would.
func agentGet(t *testing.T, a *agent.Agent, deviceID string, ts *httptest.Server, path string) int {
	t.Helper()
	cfg, err := agent.LoadConfig(a.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	key := cfg.Key()
	req, _ := http.NewRequest("GET", ts.URL+path, nil)
	req.Header.Set(agentproto.HeaderAuth, agentproto.SignRequest(key, deviceID, "GET", path, time.Now()))
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // test server
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	return res.StatusCode
}

func TestDistServersFor(t *testing.T) {
	net := func(ifs ...agentproto.NetIface) json.RawMessage { raw, _ := json.Marshal(ifs); return raw }
	cands := newDistCandidates([]store.DistributionServer{
		{ID: 1, Name: "Wide", URL: "https://a", CertSHA256: strings.Repeat("a", 64), Networks: []string{"10.0.0.0/8"}},
		{ID: 2, Name: "Utrecht", URL: "https://b", CertSHA256: strings.Repeat("b", 64), Networks: []string{"10.1.0.0/16"}},
		{ID: 3, Name: "Remote", URL: "https://c", CertSHA256: strings.Repeat("c", 64), Networks: []string{"203.0.113.7/32"}, GroupIDs: []int64{9}},
	})
	ids := func(l []agentproto.DistributionServer) []int64 {
		out := []int64{}
		for _, d := range l {
			out = append(out, d.ID)
		}
		return out
	}
	got := ids(distServersFor(store.PowerDevice{ID: "x", GroupID: 1, Network: net(agentproto.NetIface{IPs: []string{"10.1.4.20"}, CIDRs: []string{"10.1.4.20/24"}})}, cands))
	if fmt.Sprint(got) != "[2 1]" {
		t.Errorf("most specific first: %v", got)
	}
	got = ids(distServersFor(store.PowerDevice{ID: "y", GroupID: 1, RemoteIP: "203.0.113.7", LocalIPs: []string{"192.168.1.10"}}, cands))
	if fmt.Sprint(got) != "[3]" {
		t.Errorf("by public address: %v", got)
	}
	got = ids(distServersFor(store.PowerDevice{ID: "z", GroupID: 9, LocalIPs: []string{"172.16.0.1"}}, cands))
	if fmt.Sprint(got) != "[3]" {
		t.Errorf("by group: %v", got)
	}
	if got := distServersFor(store.PowerDevice{ID: "w", GroupID: 1, LocalIPs: []string{"172.16.0.1"}}, cands); len(got) != 0 {
		t.Errorf("no match: %v", got)
	}
}

func TestNormalizeDist(t *testing.T) {
	if u, ok := normalizeDistURL("10.1.0.5"); !ok || u != "https://10.1.0.5:8445" {
		t.Errorf("bare address: %q %v", u, ok)
	}
	if u, ok := normalizeDistURL("https://dp.branch.local:9000/"); !ok || u != "https://dp.branch.local:9000" {
		t.Errorf("with port: %q %v", u, ok)
	}
	if _, ok := normalizeDistURL("https://x/path"); ok {
		t.Error("path accepted")
	}
	if n, ok := normalizeNetworks([]string{"10.1.2.3/16", "10.1.0.0/16", "fd00::1"}); !ok || fmt.Sprint(n) != "[10.1.0.0/16 fd00::1/128]" {
		t.Errorf("networks: %v %v", n, ok)
	}
	if a := firstLANAddress([]string{"127.0.0.1", "fe80::1", "2001:db8::1", "192.168.5.4"}); a != "192.168.5.4" {
		t.Errorf("lan address: %q", a)
	}
}
