package catalog

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// versionData of Google.Chrome as served by the CDN (MSZIP).
var chromeVersionData = []byte{0x0a, 0x51, 0xe5, 0xc0, 0x18, 0x00, 0xe0, 0x02, 0x2c, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x2c, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xbd, 0x00, 0x00, 0x00, 0x43, 0x4b, 0x8d, 0x8e, 0xc1, 0x4a, 0x04, 0x31, 0x10, 0x44, 0xef, 0xf3, 0x15, 0xf9, 0x01, 0x27, 0xe9, 0xee, 0x74, 0x77, 0x32, 0x57, 0x05, 0x3d, 0x7a, 0xf2, 0x9e, 0xa4, 0x7b, 0x56, 0x61, 0xd7, 0x81, 0x1d, 0xd9, 0xef, 0x77, 0x3c, 0x08, 0xea, 0x49, 0x0a, 0x0a, 0x0a, 0xaa, 0xa8, 0xb7, 0xbf, 0x2c, 0x01, 0xe6, 0x34, 0xdd, 0x1e, 0x96, 0xe9, 0x2e, 0xdc, 0x8e, 0xc0, 0x3a, 0xa7, 0xb9, 0x40, 0xa2, 0x99, 0xea, 0x14, 0xc2, 0xf5, 0x79, 0x09, 0x97, 0xf6, 0xfe, 0xb6, 0xfa, 0xfe, 0xb1, 0xc7, 0x53, 0x7c, 0xdc, 0xb6, 0xd3, 0xd9, 0xe3, 0xfd, 0xeb, 0x75, 0xbb, 0x78, 0xfc, 0xd5, 0x8e, 0x20, 0xc3, 0x8e, 0xc9, 0x8e, 0x2c, 0x4f, 0x4b, 0x18, 0x44, 0xd9, 0xfb, 0x60, 0x63, 0xe7, 0x15, 0x8b, 0x0a, 0x35, 0x59, 0x2d, 0xc1, 0xc0, 0x26, 0x98, 0xc0, 0x73, 0xb6, 0x2c, 0x58, 0x71, 0x90, 0x98, 0x81, 0x17, 0x4a, 0x56, 0xb9, 0x61, 0x4b, 0xd0, 0x48, 0xab, 0xf7, 0x6f, 0x1e, 0xf9, 0x7a, 0x48, 0x5a, 0x0e, 0xfb, 0x07, 0xcf, 0x8f, 0xf6, 0x1f, 0x1e, 0x3a, 0x4e, 0x49, 0x3b, 0x68, 0x57, 0x19, 0xa8, 0x92, 0x8a, 0x6b, 0xab, 0x5d, 0xb3, 0xb5, 0xe4, 0xde, 0x8b, 0xb3, 0x81, 0x75, 0x92, 0x43, 0xc8, 0x0c, 0x82, 0x56, 0x06, 0xd7, 0x15, 0x80, 0x58, 0x28, 0x1b, 0x4e, 0x9f}

func TestDecodeMSZIP(t *testing.T) {
	out, err := DecodeMSZIP(chromeVersionData)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "rP: manifests/g/Google/Chrome/157.0.8103.39/16cd") || len(out) != 300 {
		t.Fatalf("decoded: %q", out)
	}
	enc, _ := EncodeMSZIP(out)
	back, err := DecodeMSZIP(enc)
	if err != nil || string(back) != string(out) {
		t.Fatalf("round trip: %v", err)
	}
	if _, err := DecodeMSZIP(chromeVersionData[:60]); err == nil {
		t.Error("truncated data decoded")
	}
}

const chromeManifest = `AppsAndFeaturesEntries:
- UpgradeCode: '{C1DFDF69-5945-32F2-A35E-EE94C99C7CF4}'
InstallerType: wix
Installers:
- Architecture: x86
  InstallerSha256: D57347B99605CD231726FDD605A0F21C6BA9750F3B688697F8F008E2B96984FA
  InstallerUrl: https://dl.google.com/dl/chrome/install/googlechromestandaloneenterprise.msi
  ProductCode: '{00016FE3-603D-30A8-A18D-286C6CF8D90D}'
- Architecture: x64
  InstallerSha256: 55EDACD348B4C1C9B291DAB19E4665929C7A127C013D9625970BE924F893273F
  InstallerUrl: https://dl.google.com/dl/chrome/install/googlechromestandaloneenterprise64.msi
  ProductCode: '{807524DE-67B8-3922-BB35-773E5AFB6EBB}'
- Architecture: arm64
  InstallerSha256: C9D72C63776AA8A4B3212D413376CF1CE24CF007F9DBA9A048B73CB0F08F41B1
  InstallerUrl: https://dl.google.com/dl/chrome/install/googlechromestandaloneenterprise_arm64.msi
  ProductCode: '{8CAFEFE2-CDDF-3198-8DA4-9C19DD9F0714}'
ManifestType: merged
PackageIdentifier: Google.Chrome
PackageName: Google Chrome
PackageVersion: 157.0.8103.39
Publisher: Google LLC
Scope: machine
ShortDescription: The Fast & Secure Web Browser Built to be Yours
`

const firefoxManifest = `PackageIdentifier: Mozilla.Firefox
PackageVersion: "159.0"
PackageName: Mozilla Firefox
Publisher: Mozilla
InstallerType: nullsoft
InstallerSwitches:
  Silent: /S
  SilentWithProgress: /S
  Custom: /PreventRebootRequired=true
ExpectedReturnCodes:
- InstallerReturnCode: 1602
  ReturnResponse: cancelledByUser
- InstallerReturnCode: 3
  ReturnResponse: rebootRequiredToFinish
Installers:
- Architecture: x64
  InstallerLocale: de-DE
  InstallerUrl: https://download-installer.cdn.mozilla.net/pub/firefox/releases/159.0/win64/de/Firefox%20Setup%20159.0.exe
  InstallerSha256: 1111111111111111111111111111111111111111111111111111111111111111
- Architecture: x64
  InstallerLocale: en-US
  Scope: user
  InstallerUrl: https://example.invalid/user.exe
  InstallerSha256: 2222222222222222222222222222222222222222222222222222222222222222
- Architecture: x64
  InstallerLocale: en-US
  InstallerUrl: https://download-installer.cdn.mozilla.net/pub/firefox/releases/159.0/win64/en-US/Firefox%20Setup%20159.0.exe
  InstallerSha256: 8776887B79925ABBDD407FCDFE257D8382B54E24BE52A6466426B95A804D6790
  AppsAndFeaturesEntries:
  - DisplayName: Mozilla Firefox (x64 en-US)
- Architecture: x86
  InstallerLocale: en-US
  InstallerUrl: https://download-installer.cdn.mozilla.net/pub/firefox/releases/159.0/win32/en-US/Firefox%20Setup%20159.0.exe
  InstallerSha256: 3333333333333333333333333333333333333333333333333333333333333333
`

func TestResolve(t *testing.T) {
	m, err := ParseManifest([]byte(chromeManifest))
	if err != nil {
		t.Fatal(err)
	}
	c, err := m.Resolve("amd64")
	if err != nil {
		t.Fatal(err)
	}
	in := c.Installer
	if in.Kind != agentproto.KindMSI || !strings.HasSuffix(in.URL, "enterprise64.msi") || in.SHA256 != "55edacd348b4c1c9b291dab19e4665929c7a127c013d9625970be924f893273f" ||
		in.Args != "ALLUSERS=1" || in.FileName != "googlechromestandaloneenterprise64.msi" {
		t.Errorf("chrome x64: %+v", in)
	}
	if c.Detect.Name != "Google Chrome" || c.Detect.ProductCode != "{807524DE-67B8-3922-BB35-773E5AFB6EBB}" || c.Detect.Version != "157.0.8103.39" {
		t.Errorf("chrome detect: %+v", c.Detect)
	}
	if c, _ := m.Resolve("arm64"); !strings.HasSuffix(c.Installer.URL, "_arm64.msi") {
		t.Errorf("chrome arm64: %+v", c.Installer)
	}

	m, err = ParseManifest([]byte(firefoxManifest))
	if err != nil {
		t.Fatal(err)
	}
	c, err = m.Resolve("amd64")
	if err != nil {
		t.Fatal(err)
	}
	in = c.Installer
	if in.Kind != agentproto.KindNullsoft || !strings.Contains(in.URL, "/win64/en-US/") || in.Args != "/S /PreventRebootRequired=true" ||
		in.FileName != "Firefox Setup 159.0.exe" || len(in.RebootCodes) != 1 || in.RebootCodes[0] != 3 {
		t.Errorf("firefox: %+v", in)
	}
	if c.Detect.Name != "Mozilla Firefox" {
		t.Errorf("firefox detect: %+v", c.Detect)
	}

	// An exe without silent switches cannot be installed unattended.
	m, _ = ParseManifest([]byte("PackageIdentifier: X.Y\nPackageName: Y\nPackageVersion: '1'\nInstallers:\n- Architecture: x64\n  InstallerType: exe\n  InstallerUrl: https://x/y.exe\n  InstallerSha256: AA\n"))
	if _, err := m.Resolve("amd64"); err != ErrNoInstaller {
		t.Errorf("exe without switches: %v", err)
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"157.0.8103.39", "156.0.8078.80", 1},
		{"10.0", "9.5", 1},
		{"1.2", "1.2.0", 0},
		{"26.06", "26.6", 0},
		{"1.0", "1.0.beta", 1},
		{"v2.1", "2.1", 0},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("%s vs %s: %d", c.a, c.b, got)
		}
	}
}

func TestFakeCatalog(t *testing.T) {
	h, err := FakeCDN(t.TempDir(), []FakePackage{
		{ID: "Google.Chrome", Name: "Google Chrome", Moniker: "chrome", Version: "157.0.8103.39", Manifest: chromeManifest, Tags: []string{"browser"}},
		{ID: "Mozilla.Firefox", Name: "Mozilla Firefox", Moniker: "firefox", Version: "159.0", Manifest: firefoxManifest, Tags: []string{"browser"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()
	ctx := context.Background()
	w := &Winget{Dir: t.TempDir(), BaseURL: ts.URL}
	defer w.Close()
	if _, err := w.Search(ctx, "chrome", 5); err != ErrNotLoaded {
		t.Errorf("search before download: %v", err)
	}
	if !w.Due() {
		t.Error("an empty catalog is due")
	}
	if err := w.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if st := w.Status(); st.Packages != 2 || st.UpdatedAt == nil || st.Error != "" {
		t.Errorf("status: %+v", st)
	}
	if w.Due() {
		t.Error("a fresh catalog is not due")
	}
	found, err := w.Search(ctx, "browser", 5)
	if err != nil || len(found) != 2 {
		t.Fatalf("search by tag: %v %+v", err, found)
	}
	found, _ = w.Search(ctx, "fire", 5)
	if len(found) != 1 || found[0].ID != "Mozilla.Firefox" || found[0].Version != "159.0" {
		t.Errorf("search: %+v", found)
	}
	m, err := w.Manifest(ctx, "google.chrome", "")
	if err != nil || m.PackageVersion != "157.0.8103.39" {
		t.Fatalf("manifest: %v %+v", err, m)
	}
	if _, err := w.Manifest(ctx, "Google.Chrome", "1.0"); err == nil {
		t.Error("unknown version found")
	}
	if _, err := w.Get(ctx, "Nope.Nope"); err != ErrNotFound {
		t.Errorf("unknown package: %v", err)
	}
	// A second instance picks up the downloaded index.
	w2 := &Winget{Dir: w.Dir, BaseURL: ts.URL}
	defer w2.Close()
	if err := w2.Open(); err != nil || w2.Status().Packages != 2 {
		t.Errorf("open: %v %+v", err, w2.Status())
	}
}
