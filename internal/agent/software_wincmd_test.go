package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

const testMsiexec = `C:\Windows\System32\msiexec.exe`

func TestInstallCommand(t *testing.T) {
	cases := []struct {
		in   agentproto.Installer
		want string
	}{
		{agentproto.Installer{Kind: "msi"}, `"` + testMsiexec + `" /i "C:\t\app.msi" /qn /norestart /l*v "C:\t\install.log"`},
		{agentproto.Installer{Kind: "msi", Args: "ALLUSERS=1 SERVER=x"}, `"` + testMsiexec + `" /i "C:\t\app.msi" /qn /norestart ALLUSERS=1 SERVER=x /l*v "C:\t\install.log"`},
		{agentproto.Installer{Kind: "nullsoft"}, `"C:\t\app.msi" /S`},
		{agentproto.Installer{Kind: "inno", Args: "/VERYSILENT /ALLUSERS"}, `"C:\t\app.msi" /VERYSILENT /ALLUSERS /LOG="C:\t\install.log"`},
		{agentproto.Installer{Kind: "burn"}, `"C:\t\app.msi" /quiet /norestart /log "C:\t\install.log"`},
		{agentproto.Installer{Kind: "exe", Args: "--silent"}, `"C:\t\app.msi" --silent`},
	}
	for _, c := range cases {
		_, cl, err := installCommand(c.in, `C:\t\app.msi`, `C:\t\install.log`, testMsiexec)
		if err != nil || cl != c.want {
			t.Errorf("%+v:\n got %s (%v)\nwant %s", c.in, cl, err, c.want)
		}
	}
	if _, _, err := installCommand(agentproto.Installer{Kind: "exe"}, "x.exe", "l", testMsiexec); err == nil {
		t.Error("an exe without arguments")
	}
	if _, _, err := installCommand(agentproto.Installer{Kind: "apt"}, "x", "l", testMsiexec); err == nil {
		t.Error("apt on Windows")
	}
}

func TestSplitCommand(t *testing.T) {
	exists := func(p string) bool { return p == `C:\Program Files\Foo Bar\uninst.exe` }
	cases := []struct{ in, exe, rest string }{
		{`"C:\Program Files\X\unins000.exe" /SILENT`, `C:\Program Files\X\unins000.exe`, "/SILENT"},
		{`C:\Program Files\Foo Bar\uninst.exe /x /y`, `C:\Program Files\Foo Bar\uninst.exe`, "/x /y"},
		{`C:\tools\remove.exe --all`, `C:\tools\remove.exe`, "--all"},
		{`"C:\no end`, `C:\no end`, ""},
		{``, ``, ``},
	}
	for _, c := range cases {
		exe, rest := splitCommand(c.in, exists)
		if exe != c.exe || rest != c.rest {
			t.Errorf("%q: %q %q", c.in, exe, rest)
		}
	}
}

func TestUninstallCommand(t *testing.T) {
	exists := func(string) bool { return false }
	fam := func(exe string) string {
		if strings.Contains(exe, "npp") {
			return familyNullsoft
		}
		return familyUnknown
	}
	cases := []struct {
		e    arpEntry
		args string
		want string
		err  bool
	}{
		{arpEntry{Key: "{23170F69-40C1-2702-2606-000001000000}", WindowsInstaller: true, UninstallString: "MsiExec.exe /I{23170F69-40C1-2702-2606-000001000000}"}, "",
			`"` + testMsiexec + `" /x {23170F69-40C1-2702-2606-000001000000} /qn /norestart`, false},
		{arpEntry{Key: "Chrome", UninstallString: `"C:\Program Files\Google\Chrome\Application\setup.exe" --uninstall --system-level`}, "--force-uninstall",
			`"C:\Program Files\Google\Chrome\Application\setup.exe" --uninstall --system-level --force-uninstall`, false},
		{arpEntry{Key: "Old", UninstallString: `MsiExec.exe /X{11111111-2222-3333-4444-555555555555}`}, "",
			`"` + testMsiexec + `" /x {11111111-2222-3333-4444-555555555555} /qn /norestart`, false},
		{arpEntry{Key: "VS Code_is1", UninstallString: `"C:\Program Files\Microsoft VS Code\unins000.exe"`}, "",
			`"C:\Program Files\Microsoft VS Code\unins000.exe" /VERYSILENT /SUPPRESSMSGBOXES /NORESTART`, false},
		{arpEntry{Key: "Notepad++", UninstallString: `"C:\Program Files\Notepad++\uninstall.exe"`, QuietUninstallString: `"C:\Program Files\Notepad++\uninstall.exe" /S`}, "",
			`"C:\Program Files\Notepad++\uninstall.exe" /S`, false},
		{arpEntry{Key: "npp", UninstallString: `"C:\npp\uninstall.exe"`}, "", `"C:\npp\uninstall.exe" /S`, false},
		{arpEntry{Key: "Mystery", DisplayName: "Mystery", UninstallString: `"C:\m\remove.exe"`}, "", "", true},
		{arpEntry{Key: "Empty", DisplayName: "Empty"}, "", "", true},
	}
	for _, c := range cases {
		_, cl, err := uninstallCommand(c.e, c.args, testMsiexec, exists, fam)
		if (err != nil) != c.err || cl != c.want {
			t.Errorf("%+v:\n got %s (%v)\nwant %s", c.e, cl, err, c.want)
		}
	}
}

func TestClassifyExit(t *testing.T) {
	in := agentproto.Installer{SuccessCodes: []int{1}, RebootCodes: []int{3}}
	for code, want := range map[int][2]bool{0: {true, false}, 3010: {true, true}, 1641: {true, true}, 1: {true, false}, 3: {true, true}, 1603: {false, false}, 2: {false, false}} {
		ok, reboot, msg := classifyExit(code, in)
		if ok != want[0] || reboot != want[1] {
			t.Errorf("%d: %v %v %s", code, ok, reboot, msg)
		}
	}
	if _, _, msg := classifyExit(1618, in); !strings.Contains(msg, "another installation") {
		t.Errorf("1618: %s", msg)
	}
}

func TestMatchEntries(t *testing.T) {
	entries := []arpEntry{
		{Key: "a", DisplayName: "Google Chrome Beta", DisplayVersion: "158.0"},
		{Key: "{807524DE-67B8-3922-BB35-773E5AFB6EBB}", DisplayName: "Google Chrome", DisplayVersion: "157.0.8103.39"},
		{Key: "c", DisplayName: "Mozilla Firefox (x64 en-US)", DisplayVersion: "159.0"},
	}
	m := matchEntries(entries, agentproto.Detect{Name: "google chrome"})
	if len(m) != 2 || m[0].DisplayName != "Google Chrome" {
		t.Errorf("exact first: %+v", m)
	}
	if ok, v := findInstalled(entries, agentproto.Detect{Name: "Mozilla Firefox"}); !ok || v != "159.0" {
		t.Errorf("prefix: %v %s", ok, v)
	}
	if ok, v := findInstalled(entries, agentproto.Detect{ProductCode: "{807524de-67b8-3922-bb35-773e5afb6ebb}"}); !ok || v != "157.0.8103.39" {
		t.Errorf("product code: %v %s", ok, v)
	}
	if ok, _ := findInstalled(entries, agentproto.Detect{Name: "Opera"}); ok {
		t.Error("not installed")
	}
	if got := uninstallTargets(entries, agentproto.Uninstall{Name: "google chrome", Version: "157.0.8103.39"}); len(got) != 1 {
		t.Errorf("targets: %+v", got)
	}
	if got := uninstallTargets(entries, agentproto.Uninstall{Name: "Google Chrome", Version: "1.0"}); len(got) != 0 {
		t.Errorf("other version: %+v", got)
	}
	if ok, v := findInstalled(entries, agentproto.Detect{Name: "Google Chrome", Exact: true}); !ok || v != "157.0.8103.39" {
		t.Errorf("exact: %v %s (the beta is newer but another program)", ok, v)
	}
	user := append(entries, arpEntry{Key: "u", DisplayName: "Microsoft Edge", DisplayVersion: "150.0", PerUser: true})
	if ok, _ := findInstalled(user, agentproto.Detect{Name: "Microsoft Edge", MachineOnly: true}); ok {
		t.Error("a per-user install counts as installed for the machine")
	}
}

func TestDecodeLog(t *testing.T) {
	utf16 := []byte{0xff, 0xfe, 'O', 0, 'K', 0, 0x3d, 0xd8, 0x00, 0xde} // "OK😀"
	if got := decodeLog(utf16); got != "OK😀" {
		t.Errorf("utf-16: %q", got)
	}
	if got := decodeLog([]byte("plain")); got != "plain" {
		t.Errorf("ansi: %q", got)
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"157.0.8103.39", "156.0.8078.80", 1}, {"10.0", "9.5", 1}, {"1.2", "1.2.0", 0}, {"2.0", "2.0.1", -1},
		{"3.12.10150.0", "3.12.10", 1}, {"1:2.3-1", "1:2.3-2", -1}, {"1.0", "1.0rc1", 1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("%s vs %s: %d", c.a, c.b, got)
		}
	}
}

func TestCheckSoftwareJob(t *testing.T) {
	sum := strings.Repeat("a", 64)
	ok := []agentproto.SoftwareJob{
		{Action: "install", Installer: &agentproto.Installer{Kind: "msi", URL: "https://dl.example.com/x.msi", SHA256: sum}},
		{Action: "install", Installer: &agentproto.Installer{Kind: "msi", URL: "https://dl.example.com/x.msi", AnyHash: true}},
		{Action: "install", Installer: &agentproto.Installer{Kind: "msi", File: sum, SHA256: sum, FileName: "LOB App 1.0.msi"}},
		{Action: "install", Installer: &agentproto.Installer{Kind: "apt", Package: "htop"}},
		{Action: "uninstall", Uninstall: &agentproto.Uninstall{Name: "7-Zip"}},
	}
	for _, j := range ok {
		if msg := checkSoftwareJob(j); msg != "" {
			t.Errorf("%+v: %s", j.Installer, msg)
		}
	}
	bad := []agentproto.SoftwareJob{
		{Action: "install"},
		{Action: "install", Installer: &agentproto.Installer{Kind: "msi", URL: "http://dl.example.com/x.msi", SHA256: sum}},
		{Action: "install", Installer: &agentproto.Installer{Kind: "msi", URL: "https://dl.example.com/x.msi"}},
		{Action: "install", Installer: &agentproto.Installer{Kind: "msi", File: "../../etc/passwd", SHA256: "../../etc/passwd"}},
		{Action: "install", Installer: &agentproto.Installer{Kind: "msi", File: sum, SHA256: sum, FileName: `..\evil.exe`}},
		{Action: "install", Installer: &agentproto.Installer{Kind: "apt", Package: "htop -o APT::x=y"}},
		{Action: "uninstall", Uninstall: &agentproto.Uninstall{Name: " "}},
		{Action: "format"},
	}
	for _, j := range bad {
		if msg := checkSoftwareJob(j); msg == "" {
			t.Errorf("accepted %+v %+v", j.Action, j.Installer)
		}
	}
}

func TestSaveChecked(t *testing.T) {
	dir := t.TempDir()
	data := []byte("installer bytes")
	h := sha256.Sum256(data)
	sum := hex.EncodeToString(h[:])
	if err := saveChecked(bytes.NewReader(data), dir+"/a.exe", strings.ToUpper(sum), 0, false); err != nil {
		t.Errorf("good hash: %v", err)
	}
	if err := saveChecked(bytes.NewReader(data), dir+"/b.exe", strings.Repeat("0", 64), 0, false); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Errorf("bad hash: %v", err)
	}
	if err := saveChecked(bytes.NewReader(data), dir+"/c.exe", strings.Repeat("0", 64), 0, true); err != nil {
		t.Errorf("any hash: %v", err)
	}
	if err := saveChecked(bytes.NewReader(data), dir+"/d.exe", sum, 3, false); err == nil {
		t.Error("wrong size accepted")
	}
	if n := installerFileName(agentproto.Installer{Kind: "msi", URL: "https://x/download?id=4"}); n != "installer.msi" {
		t.Errorf("file name: %s", n)
	}
}
