package catalog

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNormalizeName(t *testing.T) {
	cases := []struct{ in, norm, arch string }{
		{"7-Zip 23.01 (x64)", "7zip", "X64"},
		{"Notepad++ (64-bit x64)", "notepad", "X64"},
		{"VLC media player", "vlcmediaplayer", ""},
		{"Mozilla Firefox (x64 nl)", "mozillafirefox", "X64"},
		{"Mozilla Firefox ESR (x64 en-US)", "mozillafirefoxesr", "X64"},
		{"Python 3.12.4 (64-bit)", "python", "X64"},
		{"Microsoft Visual Studio Code", "microsoftvisualstudiocode", ""},
		{"Java 8 Update 431 (64-bit)", "java8update431", "X64"},
		{"Zoom Workplace (32-bit)", "zoomworkplace", "X86"},
		{"Adobe Acrobat (64-bit)", "adobeacrobat", "X64"},
		{"PuTTY release 0.81 (arm64)", "puttyrelease", "Arm64"},
		{"Google Chrome", "googlechrome", ""},
	}
	for _, c := range cases {
		norm, arch := NormalizeName(c.in)
		if norm != c.norm || arch != c.arch {
			t.Errorf("NormalizeName(%q) = %q %q, want %q %q", c.in, norm, arch, c.norm, c.arch)
		}
	}
	for in, want := range map[string]string{"Google LLC": "google", "Microsoft Corporation": "microsoft", "Igor Pavlov": "igorpavlov",
		"Python Software Foundation": "pythonsoftwarefoundation", "TeamViewer GmbH": "teamviewer", "Notepad++ Team": "notepadteam"} {
		if got := NormalizePublisher(in); got != want {
			t.Errorf("NormalizePublisher(%q) = %q, want %q", in, got, want)
		}
	}
}

func fakeWinget(t *testing.T, pkgs []FakePackage) *Winget {
	t.Helper()
	h, err := FakeCDN(t.TempDir(), pkgs)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	w := &Winget{Dir: t.TempDir(), BaseURL: ts.URL}
	t.Cleanup(w.Close)
	if err := w.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	return w
}

func TestMatchInstalled(t *testing.T) {
	w := fakeWinget(t, []FakePackage{
		{ID: "7zip.7zip", Name: "7-Zip", Version: "26.06", Publisher: "Igor Pavlov", ARPNames: []string{"7-Zip", "7-Zip (x64)"}},
		{ID: "Mozilla.Firefox", Name: "Mozilla Firefox (en-US)", Version: "159.0", Publisher: "Mozilla", ARPNames: []string{"Mozilla Firefox (x64)"}},
		{ID: "Mozilla.Firefox.nl", Name: "Mozilla Firefox (nl)", Version: "159.0", Publisher: "Mozilla", ARPNames: []string{"Mozilla Firefox (x64)"}},
		{ID: "Python.Python.3.11", Name: "Python 3.11", Version: "3.11.9", ARPMax: "3.11.9150.0", Publisher: "Python Software Foundation", ARPNames: []string{"Python (64-bit)"}},
		{ID: "Python.Python.3.12", Name: "Python 3.12", Version: "3.12.10", ARPMax: "3.12.10150.0", Publisher: "Python Software Foundation", ARPNames: []string{"Python (64-bit)"}},
		{ID: "Other.Notepad", Name: "Notepad", Version: "1.0", Publisher: "Someone Else"},
		{ID: "Notepad++.Notepad++", Name: "Notepad++", Version: "8.9.9", Publisher: "Notepad++ Team"},
	})
	ctx := context.Background()
	check := func(name, pub, ver, wantID string, outdated bool) {
		t.Helper()
		m, ok := w.MatchInstalled(ctx, name, pub, ver)
		if wantID == "" {
			if ok {
				t.Errorf("%s: matched %s, want none", name, m.ID)
			}
			return
		}
		if !ok || m.ID != wantID {
			t.Errorf("%s: matched %q (%v), want %s", name, m.ID, ok, wantID)
			return
		}
		if m.Outdated(ver) != outdated {
			t.Errorf("%s %s: outdated = %v", name, ver, !outdated)
		}
	}
	check("7-Zip 23.01 (x64)", "Igor Pavlov", "23.01", "7zip.7zip", true)
	check("7-Zip 26.06 (x64)", "Igor Pavlov", "26.06", "7zip.7zip", false)
	check("Mozilla Firefox (x64 nl)", "Mozilla", "158.0.1", "Mozilla.Firefox", true)
	check("Python 3.12.4 (64-bit)", "Python Software Foundation", "3.12.4150.0", "Python.Python.3.12", true)
	check("Python 3.11.9 (64-bit)", "Python Software Foundation", "3.11.9150.0", "Python.Python.3.11", false)
	check("Python 3.9.1 (64-bit)", "Python Software Foundation", "3.9.1150.0", "", false) // no 3.9 line
	check("Notepad++ (64-bit x64)", "Notepad++ Team", "8.6.2", "Notepad++.Notepad++", true)
	check("Unknown Tool 1.0", "Nobody", "1.0", "", false)
}

// TestMatchRealIndex runs against a downloaded index (FW_TEST_WINGET_INDEX=path/to/index.db).
func TestMatchRealIndex(t *testing.T) {
	path := os.Getenv("FW_TEST_WINGET_INDEX")
	if path == "" {
		t.Skip("FW_TEST_WINGET_INDEX not set")
	}
	w := &Winget{Dir: filepath.Dir(path)}
	if err := w.load(path, time.Now()); err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for _, c := range []struct{ name, pub, ver, id string }{
		{"7-Zip 24.08 (x64)", "Igor Pavlov", "24.08", "7zip.7zip"},
		{"Notepad++ (64-bit x64)", "Notepad++ Team", "8.6.9", "Notepad++.Notepad++"},
		{"VLC media player", "VideoLAN", "3.0.20", "VideoLAN.VLC"},
		{"Mozilla Firefox (x64 nl)", "Mozilla", "131.0", ""},
		{"Google Chrome", "Google LLC", "130.0.6723.92", "Google.Chrome"},
		{"Microsoft Visual Studio Code", "Microsoft Corporation", "1.95.1", "Microsoft.VisualStudioCode"},
		{"Python 3.12.4 (64-bit)", "Python Software Foundation", "3.12.4150.0", "Python.Python.3.12"},
	} {
		m, ok := w.MatchInstalled(context.Background(), c.name, c.pub, c.ver)
		t.Logf("%-32s -> %v %s latest %s arp %s outdated %v", c.name, ok, m.ID, m.Latest, m.ARPMax, m.Outdated(c.ver))
		if c.id != "" && m.ID != c.id {
			t.Errorf("%s: %s, want %s", c.name, m.ID, c.id)
		}
		if !ok {
			t.Errorf("%s: no match", c.name)
		}
	}
}
