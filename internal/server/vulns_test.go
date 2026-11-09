package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/catalog"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
	"github.com/rhisijmuiden/fleetwarden/internal/vulndb"
)

func vulnTestdata(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "vulndb", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func gzipped(t *testing.T, raw []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	zw.Write(raw)
	zw.Close()
	return b.Bytes()
}

// vulnSources serves the sources the way NVD, CISA, Debian and Microsoft do.
func vulnSources(t *testing.T) (*httptest.Server, *map[string]int) {
	nvd := gzipped(t, vulnTestdata(t, "nvd.json"))
	files := map[string][]byte{
		"/kev.json":                        vulnTestdata(t, "kev.json"),
		"/debian/json":                     vulnTestdata(t, "debian.json"),
		"/msrc/updates":                    vulnTestdata(t, "msrc-updates.json"),
		"/msrc/cvrf/2026-Oct":              vulnTestdata(t, "cvrf.json"),
		"/msrc/cvrf/2026-Nov":              []byte(`{"DocumentTitle":{"Value":"November"},"ProductTree":{"FullProductName":[]},"Vulnerability":[]}`),
		"/msrc/cvrf/2026-Sep":              []byte(`{"DocumentTitle":{"Value":"September"},"Vulnerability":[]}`),
		"/nvd/nvdcve-2.0-modified.json.gz": nvd,
	}
	for y := 2002; y <= time.Now().Year(); y++ {
		files["/nvd/nvdcve-2.0-"+strconv.Itoa(y)+".json.gz"] = gzipped(t, []byte(`{"format":"NVD_CVE","vulnerabilities":[]}`))
	}
	files["/nvd/nvdcve-2.0-2024.json.gz"] = nvd
	hits := map[string]int{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits[r.URL.Path]++
		raw, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(raw)
	}))
	t.Cleanup(ts.Close)
	return ts, &hits
}

// seedVulnDevice registers a device with its OS, programs and missing updates.
func seedVulnDevice(t *testing.T, st *store.Store, host, platform, osName, osVersion, osBuild string, sw []store.SoftwareRow, patches []store.PatchRow) string {
	t.Helper()
	ctx := context.Background()
	id := newUUID()
	if err := st.CreateDevice(ctx, store.Device{ID: id, GroupID: 1, PublicKey: []byte("k"), Hostname: host, Platform: platform, Arch: "amd64"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveInventory(ctx, id, store.InventoryUpdate{Hostname: host, OSName: osName, OSVersion: osVersion, OSBuild: osBuild,
		Kind: "workstation", Hardware: []byte("{}"), Software: sw}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveScanResult(ctx, id, store.ScanResult{Source: "test", Patches: patches}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestVulnerabilities(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()
	ctx := context.Background()

	ts, hits := vulnSources(t)
	e.srv.vulns.urls = &vulnURLs{NVD: ts.URL + "/nvd/", KEV: ts.URL + "/kev.json", Debian: ts.URL + "/debian/json", MSRC: ts.URL + "/msrc/"}
	e.srv.vulns.client = ts.Client()

	cdn, err := catalog.FakeCDN(t.TempDir(), []catalog.FakePackage{
		{ID: "7zip.7zip", Name: "7-Zip", Version: "26.06", Publisher: "Igor Pavlov", ARPNames: []string{"7-Zip (x64)"}},
		{ID: "PuTTY.PuTTY", Name: "PuTTY", Version: "0.83.0.0", Publisher: "Simon Tatham"},
		{ID: "Google.Chrome", Name: "Google Chrome", Version: "157.0.1", Publisher: "Google LLC"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cdnTS := httptest.NewServer(cdn)
	defer cdnTS.Close()
	e.srv.winget = &catalog.Winget{Dir: t.TempDir(), BaseURL: cdnTS.URL}
	if err := e.srv.winget.Refresh(ctx); err != nil {
		t.Fatal(err)
	}

	// A Windows 11 PC behind on its build, with old programs, and a Debian server.
	win := seedVulnDevice(t, e.st, "PC-VULN", "windows", "Windows 11 Pro", "24H2", "26100.2314", []store.SoftwareRow{
		{Name: "Google Chrome", Version: "120.0.6099.200", Publisher: "Google LLC"},
		{Name: "7-Zip 24.07 (x64)", Version: "24.07", Publisher: "Igor Pavlov"},
		{Name: "PuTTY release 0.80 (64-bit)", Version: "0.80.0.0", Publisher: "Simon Tatham"},
		{Name: "Unknown Tool", Version: "1.0"},
	}, []store.PatchRow{
		{UpdateID: "cu-2026-10", Title: "2026-10 Cumulative Update for Windows 11 Version 24H2 for x64-based Systems (KB5127648)", KB: "KB5127648", Category: "security"},
		{UpdateID: "net-2026-10", Title: "2026-10 Security Update for .NET Framework (KB5126868)", KB: "KB5126868", Category: "security"},
	})
	deb := seedVulnDevice(t, e.st, "SRV-DEB", "linux", "Debian GNU/Linux 13 (trixie)", "13", "6.12.107-amd64", []store.SoftwareRow{
		{Name: "openssh-server", Version: "1:9.7p1-6", Source: "dpkg", SrcPackage: "openssh", SrcVersion: "1:9.7p1-6"},
		{Name: "openssh-client", Version: "1:9.7p1-6", Source: "dpkg", SrcPackage: "openssh", SrcVersion: "1:9.7p1-6"},
		{Name: "linux-image-6.12.107-amd64", Version: "6.12.107-1", Source: "dpkg", SrcPackage: "linux", SrcVersion: "6.12.107-1"},
		{Name: "libssl3t64", Version: "3.5.7-1~deb13u3", Source: "dpkg", SrcPackage: "openssl", SrcVersion: "3.5.7-1~deb13u3"},
		{Name: "xz-utils", Version: "5.8.1-1", Source: "dpkg"},
	}, []store.PatchRow{
		{UpdateID: "openssh-server", Title: "openssh-server", NewVersion: "1:9.7p1-7", Category: "security"},
		{UpdateID: "linux-image-6.12.107-amd64", Title: "linux-image-6.12.107-amd64", NewVersion: "6.12.116-1", Category: "security"},
	})
	// Nothing is matched before a source was read.
	if err := e.srv.matchDirtyDevices(ctx); err != nil {
		t.Fatal(err)
	}
	var sum vulnSummaryView
	if code := admin.do("GET", "/api/v1/vulnerabilities/summary", nil, &sum); code != 200 || sum.CVEs != 0 || sum.Checked != 0 || !sum.Status.Enabled {
		t.Fatalf("summary before sync: %d %+v", code, sum)
	}

	// Read the sources (all years of the NVD on the first read).
	e.srv.vulns.firstYear = 2024
	if !e.srv.syncVulns(ctx, false) {
		t.Fatal("nothing read")
	}
	if (*hits)["/nvd/nvdcve-2.0-2024.json.gz"] != 1 || (*hits)["/nvd/nvdcve-2.0-modified.json.gz"] != 1 || (*hits)["/msrc/cvrf/2026-Oct"] != 1 {
		t.Errorf("hits %v", *hits)
	}
	srcs, _ := e.st.VulnSources(ctx)
	for _, name := range []string{vulnSourceNVD, vulnSourceKEV, vulnSourceDebian, vulnSourceMSRC} {
		if srcs[name].SyncedAt == nil || srcs[name].Error != "" || srcs[name].Items == 0 {
			t.Errorf("source %s: %+v", name, srcs[name])
		}
	}
	// A second read the same day reads nothing; a forced one reads the
	// changed sources only (the NVD's modified feed, unchanged Microsoft months not).
	if e.srv.syncVulns(ctx, false) {
		t.Error("read again within a day")
	}
	if !e.srv.syncVulns(ctx, true) || (*hits)["/nvd/nvdcve-2.0-2024.json.gz"] != 1 || (*hits)["/nvd/nvdcve-2.0-modified.json.gz"] != 2 ||
		(*hits)["/msrc/cvrf/2026-Oct"] != 1 {
		t.Errorf("forced read: %v", *hits)
	}
	if err := e.st.MarkAllVulnDirty(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.srv.matchDirtyDevices(ctx); err != nil {
		t.Fatal(err)
	}

	var dv deviceVulnsView
	if code := admin.do("GET", "/api/v1/devices/"+win+"/vulnerabilities", nil, &dv); code != 200 || dv.CheckedAt == nil || dv.Pending || !dv.Supported {
		t.Fatalf("device: %d %+v", code, dv)
	}
	byCVE := map[string]store.DeviceVulnRow{}
	for _, v := range dv.Vulnerabilities {
		byCVE[v.CVE+" "+v.Product] = v
	}
	chrome := byCVE["CVE-2024-0519 Google Chrome"]
	if !chrome.KEV || chrome.Severity != "high" || chrome.Fix != "software" || !slices.Equal(chrome.FixRef, []string{"browser:chrome"}) ||
		chrome.Fixed != "120.0.6099.224" {
		t.Errorf("chrome: %+v", chrome)
	}
	if _, ok := byCVE["CVE-2024-0222 Google Chrome"]; ok {
		t.Error("chrome 120.0.6099.200 has the fix of CVE-2024-0222")
	}
	if z := byCVE["CVE-2024-11612 7-Zip"]; z.Fix != "software" || !slices.Equal(z.FixRef, []string{"7zip.7zip"}) {
		t.Errorf("7-zip: %+v", z)
	}
	if _, ok := byCVE["CVE-2024-11477 7-Zip"]; ok {
		t.Error("7-Zip 24.07 has the fix of CVE-2024-11477")
	}
	if p := byCVE["CVE-2024-31497 PuTTY"]; p.Fix != "software" || p.Installed != "0.80.0.0" {
		t.Errorf("putty: %+v", p)
	}
	osv := byCVE["CVE-2026-100200 Windows 11 Version 24H2"]
	if osv.Kind != "os" || osv.Fixed != "10.0.26100.9711" || osv.Fix != "patch" || !slices.Equal(osv.FixRef, []string{"cu-2026-10"}) ||
		osv.Severity != "high" {
		t.Errorf("windows build: %+v", osv)
	}
	if n := byCVE["CVE-2026-102869 2026-10 Security Update for .NET Framework (KB5126868)"]; n.Kind != "update" || !slices.Equal(n.FixRef, []string{"net-2026-10"}) {
		t.Errorf(".NET update: %+v (%v)", n, keys(byCVE))
	}

	if code := admin.do("GET", "/api/v1/devices/"+deb+"/vulnerabilities", nil, &dv); code != 200 {
		t.Fatal(code)
	}
	byCVE = map[string]store.DeviceVulnRow{}
	for _, v := range dv.Vulnerabilities {
		byCVE[v.CVE+" "+v.Product] = v
	}
	if o := byCVE["CVE-2024-6387 openssh"]; o.Fix != "patch" || !slices.Equal(o.FixRef, []string{"openssh-server"}) || o.Fixed != "1:9.7p1-7" {
		t.Errorf("openssh: %+v", o)
	}
	if k := byCVE["CVE-2026-99283 linux"]; k.Fix != "none" {
		t.Errorf("open kernel CVE: %+v", k)
	}
	if k := byCVE["CVE-2026-99284 linux"]; k.Fix != "patch" {
		t.Errorf("kernel: %+v", k)
	}
	for k := range byCVE {
		if strings.HasSuffix(k, " openssl") || strings.HasSuffix(k, " xz-utils") {
			t.Errorf("fixed package reported: %s", k)
		}
	}

	if code := admin.do("GET", "/api/v1/vulnerabilities/summary", nil, &sum); code != 200 || sum.KEV != 1 || sum.Devices != 2 || sum.Checked != 2 ||
		sum.CriticalHosts != 1 || sum.CVEs < 8 {
		t.Errorf("summary: %d %+v", code, sum)
	}
	var list []store.VulnSummary
	if code := admin.do("GET", "/api/v1/vulnerabilities", nil, &list); code != 200 || len(list) == 0 || list[0].CVE != "CVE-2024-0519" || !list[0].KEV {
		t.Fatalf("list: %d %+v", code, list)
	}
	if admin.do("GET", "/api/v1/vulnerabilities?kev=1", nil, &list); len(list) != 1 {
		t.Errorf("kev filter %+v", list)
	}
	if admin.do("GET", "/api/v1/vulnerabilities?kind=package&search=openssh", nil, &list); len(list) != 1 || list[0].CVE != "CVE-2024-6387" {
		t.Errorf("search %+v", list)
	}
	if code := admin.do("GET", "/api/v1/vulnerabilities?severity=bad", nil, nil); code != 400 {
		t.Errorf("bad severity: %d", code)
	}
	var devs []store.VulnDeviceSummary
	if code := admin.do("GET", "/api/v1/vulnerabilities/devices", nil, &devs); code != 200 || len(devs) != 2 || devs[0].Hostname != "PC-VULN" || devs[0].KEV != 1 {
		t.Errorf("devices: %d %+v", code, devs)
	}
	var det vulnDetailView
	if code := admin.do("GET", "/api/v1/vulnerabilities/cve/cve-2024-0519", nil, &det); code != 200 || !det.KEV || len(det.Devices) != 1 ||
		len(det.Ranges) != 1 || det.Summary == "" {
		t.Errorf("detail: %d %+v", code, det)
	}
	if code := admin.do("GET", "/api/v1/vulnerabilities/cve/CVE-1999-0001", nil, nil); code != 404 {
		t.Errorf("unknown CVE: %d", code)
	}

	// Accepting a risk hides the CVE; the read-only role cannot.
	ro := e.newUserClient(admin, "viewer", "readonly")
	if code := ro.do("POST", "/api/v1/vulnerabilities/exceptions", map[string]any{"cve": "CVE-2024-31497", "reason": "x"}, nil); code != 403 {
		t.Errorf("read-only accepts: %d", code)
	}
	if code := ro.do("POST", "/api/v1/vulnerabilities/fix", map[string]any{"cves": []string{"CVE-2024-31497"}}, nil); code != 403 {
		t.Errorf("read-only fixes: %d", code)
	}
	if code := admin.do("POST", "/api/v1/vulnerabilities/exceptions", map[string]any{"cve": "CVE-2024-31497"}, nil); code != 400 {
		t.Errorf("no reason: %d", code)
	}
	var x store.VulnException
	if code := admin.do("POST", "/api/v1/vulnerabilities/exceptions", map[string]any{"cve": "CVE-2024-31497", "reason": "Jump host only",
		"expiresAt": time.Now().AddDate(0, 1, 0).Format("2006-01-02")}, &x); code != 201 || x.ID == 0 || x.ExpiresAt == nil {
		t.Fatalf("accept: %d %+v", code, x)
	}
	if code := admin.do("POST", "/api/v1/vulnerabilities/exceptions", map[string]any{"cve": "CVE-2024-31497", "reason": "again"}, nil); code != 409 {
		t.Errorf("duplicate: %d", code)
	}
	admin.do("GET", "/api/v1/vulnerabilities?search=31497", nil, &list)
	if len(list) != 0 {
		t.Errorf("accepted CVE listed: %+v", list)
	}
	admin.do("GET", "/api/v1/vulnerabilities?accepted=1", nil, &list)
	if len(list) != 1 || list[0].CVE != "CVE-2024-31497" {
		t.Errorf("accepted list: %+v", list)
	}

	// Rolling out fixes: the browser and 7-Zip are deployed (also to an
	// offline device, the job waits); the patch job needs the device online.
	var res vulnFixResult
	if code := admin.do("POST", "/api/v1/vulnerabilities/fix", map[string]any{"cves": []string{"CVE-2024-0519", "CVE-2024-11612",
		"CVE-2024-6387", "CVE-2026-99283", "CVE-2024-31497"}}, &res); code != 200 {
		t.Fatalf("fix: %d", code)
	}
	if res.PatchRuns != 0 || len(res.Deployments) != 2 {
		t.Errorf("fix result: %+v", res)
	}
	reasons := map[string]bool{}
	for _, sk := range res.Skipped {
		reasons[sk.Hostname+" "+sk.Reason] = true
	}
	if !reasons["SRV-DEB offline"] || !reasons["SRV-DEB no_fix"] || len(res.Skipped) != 2 {
		t.Errorf("skipped: %+v", res.Skipped)
	}
	var names []string
	for _, d := range res.Deployments {
		names = append(names, d.Name)
		if d.Devices != 1 {
			t.Errorf("deployment %+v", d)
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"Vulnerability fix: 7-Zip", "Vulnerability fix: Google Chrome"}) {
		t.Errorf("deployments %v", names)
	}
	if p, err := e.st.SoftwarePackageByWinget(ctx, "7zip.7zip"); err != nil || p.Name != "7-Zip" {
		t.Errorf("package added: %+v %v", p, err)
	}
	if code := admin.do("POST", "/api/v1/vulnerabilities/fix", map[string]any{"cves": []string{"not-a-cve"}}, nil); code != 400 {
		t.Errorf("invalid cve: %d", code)
	}

	// Settings.
	var set vulnStatus
	if code := admin.do("GET", "/api/v1/vulnerabilities/settings", nil, &set); code != 200 || set.Settings == nil || !set.Settings.MSRC ||
		len(set.Products) != len(vulndb.Products) || len(set.Sources) != 4 {
		t.Errorf("settings: %d %+v", code, set)
	}
	bad := defaultVulnSettings()
	bad.MSRCMonths = 1
	if code := admin.do("PUT", "/api/v1/vulnerabilities/settings", bad, nil); code != 400 {
		t.Errorf("bad months: %d", code)
	}
	off := defaultVulnSettings()
	off.Enabled = false
	if code := admin.do("PUT", "/api/v1/vulnerabilities/settings", off, &set); code != 200 || set.Enabled {
		t.Errorf("disable: %d %+v", code, set)
	}
	if code := admin.do("POST", "/api/v1/vulnerabilities/sync", nil, nil); code != 409 {
		t.Errorf("sync while disabled: %d", code)
	}

	// A new inventory marks the device for matching again.
	if err := e.st.SaveInventory(ctx, win, store.InventoryUpdate{Hostname: "PC-VULN", OSName: "Windows 11 Pro", OSVersion: "24H2",
		OSBuild: "26100.9711", Kind: "workstation", Hardware: []byte("{}")}); err != nil {
		t.Fatal(err)
	}
	if _, pending, _ := e.st.DeviceVulnCheck(ctx, win); !pending {
		t.Error("not marked")
	}
}

func keys[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
