package vulndb

import (
	"os"
	"path/filepath"
	"testing"
)

func open(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestParseNVD(t *testing.T) {
	got := map[string]CVE{}
	if err := ParseNVD(open(t, "nvd.json"), WantProduct, func(c CVE) error { got[c.ID] = c; return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 10 {
		t.Fatalf("%d CVEs", len(got))
	}
	c := got["CVE-2024-0519"]
	if c.Severity != SeverityHigh || c.Score != 8.8 || c.Summary == "" || c.Published == nil {
		t.Errorf("%+v", c)
	}
	if len(c.Ranges) != 1 || c.Ranges[0] != (Range{Product: "google:chrome", EndExcl: "120.0.6099.224"}) {
		t.Errorf("ranges %+v", c.Ranges)
	}
	if r := got["CVE-2024-11612"].Ranges; len(r) != 1 || r[0].StartIncl != "24.06" || r[0].EndExcl != "24.08" {
		t.Errorf("7-zip %+v", r)
	}
	if len(got["CVE-2024-0727"].Ranges) != 0 {
		t.Error("openssl is not a Windows product of the list")
	}
	if !got["CVE-2024-0069"].Rejected {
		t.Error("rejected")
	}
	ff := got["CVE-2024-0741"].Ranges
	products := map[string]bool{}
	for _, r := range ff {
		products[r.Product] = true
	}
	if !products["mozilla:firefox"] || !products["mozilla:firefox_esr"] || !products["mozilla:thunderbird"] {
		t.Errorf("firefox %+v", ff)
	}
}

func TestRangeAffects(t *testing.T) {
	cmp := CompareProgram
	r := Range{StartIncl: "24.06", EndExcl: "24.08"}
	for v, want := range map[string]bool{"24.05": false, "24.06": true, "24.07": true, "24.08": false, "24.09": false, "": false} {
		if r.Affects(v, cmp) != want {
			t.Errorf("%s: want %v", v, want)
		}
	}
	if (Range{}).Affects("1.0", cmp) {
		t.Error("open range")
	}
	if !(Range{Version: "0.80"}).Affects("0.80.0.0", cmp) {
		t.Error("exact version with trailing zeros")
	}
	if !(Range{EndIncl: "120.0.6099.224"}).Affects("120.0.6099.224", cmp) {
		t.Error("end including")
	}
}

func TestParseKEV(t *testing.T) {
	k, err := ParseKEV(open(t, "kev.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(k) != 3 {
		t.Fatalf("%+v", k)
	}
	for _, e := range k {
		if e.CVE == "CVE-2021-44228" && (!e.Ransomware || e.Added == nil || e.Due == nil) {
			t.Errorf("log4shell %+v", e)
		}
	}
}

func TestParseDebian(t *testing.T) {
	var got []DebianEntry
	err := ParseDebian(open(t, "debian.json"), map[string]bool{"trixie": true}, func(e DebianEntry) error {
		got = append(got, e)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]DebianEntry{}
	for _, e := range got {
		if e.Release != "trixie" {
			t.Errorf("release %s", e.Release)
		}
		by[e.Package+" "+e.CVE] = e
	}
	if e := by["openssh CVE-2024-6387"]; e.FixedVersion != "1:9.7p1-7" || e.Status != "resolved" {
		t.Errorf("openssh %+v", e)
	}
	if _, ok := by["openssl CVE-2026-84783"]; ok {
		t.Error("unimportant entry kept")
	}
	if e, ok := by["linux CVE-2026-99283"]; !ok || e.Status != "open" || e.FixedVersion != "" {
		t.Errorf("open entry %+v", e)
	}
	for k := range by {
		if len(k) > 5 && k[len(k)-6:] == "1E2093" {
			t.Error("TEMP entry kept")
		}
	}
}

func TestCompareDebian(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"1.0", "1.0", 0}, {"1.0-1", "1.0-2", -1}, {"1:1.0", "2.0", 1}, {"1.0~rc1", "1.0", -1}, {"1.0", "1.0+deb12u1", -1},
		{"3.0.13-1~deb12u1", "3.0.11-1~deb12u2", 1}, {"3.5.7-1~deb13u3", "3.5.7-1~deb13u2", 1}, {"1:9.7p1-7", "1:9.2p1-2+deb12u3", 1},
		{"6.12.113-1", "6.12.115-1", -1}, {"5.6.1+really5.4.5-1", "5.4.5-1", 1}, {"1.2.3a", "1.2.3", 1}, {"2.0", "10.0", -1},
	} {
		if got := CompareDebian(c.a, c.b); sign(got) != c.want {
			t.Errorf("%s vs %s: %d, want %d", c.a, c.b, got, c.want)
		}
	}
	if SourceVersion("1.2-3+b1") != "1.2-3" || SourceVersion("1.2+bad") != "1.2+bad" {
		t.Error("binNMU")
	}
}

func sign(v int) int {
	switch {
	case v < 0:
		return -1
	case v > 0:
		return 1
	}
	return 0
}

func TestDebianRelease(t *testing.T) {
	for name, want := range map[string]string{
		"Debian GNU/Linux 13 (trixie)":                    "trixie",
		"Proxmox VE 9.0 (Debian GNU/Linux 13 (trixie))":   "trixie",
		"Proxmox VE 8.4 (Debian GNU/Linux 12 (bookworm))": "bookworm",
		"Debian GNU/Linux 12":                             "bookworm",
		"Ubuntu 24.04 LTS":                                "",
	} {
		if got := DebianRelease(name, ""); got != want {
			t.Errorf("%s: %q", name, got)
		}
	}
}

func TestParseCVRF(t *testing.T) {
	docs, err := ParseMSRCUpdates(open(t, "msrc-updates.json"))
	if err != nil || len(docs) != 3 {
		t.Fatalf("%v %+v", err, docs)
	}
	got := map[string]MSRCVuln{}
	if err := ParseCVRF(open(t, "cvrf.json"), func(v MSRCVuln) error { got[v.CVE] = v; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["CVE-2019-25781"]; ok {
		t.Error("Azure Linux release notes are no update")
	}
	v := got["CVE-2026-100200"]
	if v.Severity != SeverityHigh || v.Score != 7 {
		t.Errorf("%+v", v)
	}
	found := false
	for _, f := range v.Fixes {
		if f.Product == "Windows 11 Version 24H2 for x64-based Systems" {
			found = f.KB == "5127648" && f.FixedBuild == "10.0.26100.9711" && f.Supersedes == "5124008"
		}
	}
	if !found {
		t.Errorf("fixes %+v", v.Fixes)
	}
	if got["CVE-2026-100202"].Severity != SeverityCritical {
		t.Error("critical rating")
	}
	net := got["CVE-2026-102869"]
	generic := false
	for _, f := range net.Fixes {
		generic = generic || (f.Product == "" && f.KB == "5126868")
	}
	if !generic {
		t.Errorf(".NET %+v", net.Fixes)
	}
}

func TestWindowsProduct(t *testing.T) {
	for _, c := range []struct{ name, ver, arch, want string }{
		{"Windows 11 Pro", "24H2", "amd64", "Windows 11 Version 24H2 for x64-based Systems"},
		{"Windows 11 Enterprise", "24h2", "arm64", "Windows 11 Version 24H2 for ARM64-based Systems"},
		{"Windows 10 Pro", "22H2", "386", "Windows 10 Version 22H2 for 32-bit Systems"},
		{"Windows Server 2022 Standard", "21H2", "amd64", "Windows Server 2022"},
		{"Windows Server 2012 R2 Datacenter", "", "amd64", "Windows Server 2012 R2"},
		{"Windows 11 Pro", "", "amd64", ""},
		{"Debian GNU/Linux 13 (trixie)", "13", "amd64", ""},
	} {
		if got := WindowsProduct(c.name, c.ver, c.arch); got != c.want {
			t.Errorf("%+v: %q", c, got)
		}
		if !IsWindowsProduct(c.want) && c.want != "" {
			t.Errorf("%q is no Windows product", c.want)
		}
	}
	if b, u, ok := ParseOSBuild("26100.2314"); !ok || b != 26100 || u != 2314 {
		t.Error("build")
	}
	if b, u, ok := ParseFixedBuild("10.0.26100.9711"); !ok || b != 26100 || u != 9711 {
		t.Error("fixed build")
	}
}

func TestMatchProgram(t *testing.T) {
	for name, want := range map[string]string{
		"Google Chrome":                     "google:chrome",
		"Mozilla Firefox (x64 nl)":          "mozilla:firefox",
		"Mozilla Firefox ESR (x64 en-US)":   "mozilla:firefox_esr",
		"7-Zip 24.08 (x64)":                 "7-zip:7-zip",
		"PuTTY release 0.80 (64-bit)":       "putty:putty",
		"Notepad++ (64-bit x64)":            "notepad-plus-plus:notepad++",
		"Microsoft Edge":                    "microsoft:edge_chromium",
		"Microsoft Edge WebView2 Runtime":   "",
		"Google Chrome Remote Desktop Host": "",
	} {
		p, ok := MatchProgram(name)
		if (want == "") == ok || (ok && p.CPE != want) {
			t.Errorf("%s: %q %v", name, p.CPE, ok)
		}
	}
	if ProgramVersion("anydesk:anydesk", "ad 8.1.0") != "8.1.0" || ProgramVersion("python:python", "3.12.4150.0") != "3.12.4" {
		t.Error("version")
	}
}
