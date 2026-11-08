//go:build windows

package agent

import (
	"context"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// TestConfigWindowsRegistry applies browser policies and registry values to
// a test key below HKCU and checks the layout the browsers expect.
func TestConfigWindowsRegistry(t *testing.T) {
	root := `Software\FleetWardenTest`
	t.Cleanup(func() { _ = deleteKeyTree(registry.CURRENT_USER, root) })
	_ = deleteKeyTree(registry.CURRENT_USER, root)
	be := NewWindowsTestConfigBackend(root, t.TempDir())
	a := testConfigAgent(t, be)
	pol := agentproto.ConfigPolicy{Items: []agentproto.ConfigItem{
		{ID: 1, Revision: 1, Type: agentproto.ConfigBrowser, Browser: &agentproto.BrowserPolicy{
			Browsers: []string{"chrome", "edge", "firefox"}, Homepage: "https://intranet.example.com", ShowHomeButton: ptr(true),
			StartupMode: "urls", StartupURLs: []string{"https://intranet.example.com", "https://mail.example.com"}, PasswordManager: ptr(false),
			EdgeExtensions: []string{"odfafepnkmbhccpbejgmiehpchacaeak"},
		}},
		{ID: 2, Revision: 1, Type: agentproto.ConfigRegistry, Registry: &agentproto.RegistrySettings{Values: []agentproto.RegistryValue{
			{Hive: "HKLM", Key: `SOFTWARE\Contoso`, Name: "Server", Type: "string", Value: "app.contoso.local"},
			{Hive: "HKLM", Key: `SOFTWARE\Contoso`, Name: "Mode", Type: "dword", Value: "16"},
			{Hive: "HKLM", Key: `SOFTWARE\Contoso`, Name: "Path", Type: "expandstring", Value: `%ProgramData%\Contoso`},
			{Hive: "HKLM", Key: `SOFTWARE\Contoso`, Name: "Hosts", Type: "multistring", Value: "a\nb"},
			{Hive: "HKCU", Key: `Software\Contoso`, Name: "Theme", Type: "qword", Value: "5000000000"},
		}}},
	}}
	ctx := context.Background()
	a.applyConfig(ctx, pol, be.Users(), nil)
	st := lastStatus(t, a)
	for _, it := range st.Items {
		if it.State != "applied" {
			t.Errorf("item %d: %+v", it.ID, it)
		}
	}
	open := func(path string) registry.Key {
		t.Helper()
		k, err := registry.OpenKey(registry.CURRENT_USER, root+`\`+path, registry.READ)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		return k
	}
	chrome := open(`Policies\Google\Chrome`)
	if v, _, _ := chrome.GetStringValue("HomepageLocation"); v != "https://intranet.example.com" {
		t.Errorf("HomepageLocation: %q", v)
	}
	if v, _, _ := chrome.GetIntegerValue("RestoreOnStartup"); v != 4 {
		t.Errorf("RestoreOnStartup: %d", v)
	}
	if v, _, _ := chrome.GetIntegerValue("PasswordManagerEnabled"); v != 0 {
		t.Errorf("PasswordManagerEnabled: %d", v)
	}
	chrome.Close()
	urls := open(`Policies\Google\Chrome\RestoreOnStartupURLs`)
	if v, _, _ := urls.GetStringValue("2"); v != "https://mail.example.com" {
		t.Errorf("RestoreOnStartupURLs\\2: %q", v)
	}
	urls.Close()
	ext := open(`Policies\Microsoft\Edge\ExtensionInstallForcelist`)
	if v, _, _ := ext.GetStringValue("1"); !strings.HasPrefix(v, "odfafepnkmbhccpbejgmiehpchacaeak;https://edge.microsoft.com/") {
		t.Errorf("Edge extension: %q", v)
	}
	ext.Close()
	ff := open(`Policies\Mozilla\Firefox\Homepage`)
	if v, _, _ := ff.GetStringValue("URL"); v != "https://intranet.example.com" {
		t.Errorf("Firefox Homepage\\URL: %q", v)
	}
	ff.Close()
	machine := open(`Policies\Machine\SOFTWARE\Contoso`)
	if v, typ, _ := machine.GetStringValue("Path"); v != `%ProgramData%\Contoso` || typ != registry.EXPAND_SZ {
		t.Errorf("Path: %q %d", v, typ)
	}
	if v, _, _ := machine.GetStringsValue("Hosts"); strings.Join(v, ",") != "a,b" {
		t.Errorf("Hosts: %v", v)
	}
	machine.Close()
	user := open(`User\Software\Contoso`)
	if v, typ, _ := user.GetIntegerValue("Theme"); v != 5000000000 || typ != registry.QWORD {
		t.Errorf("Theme: %d %d", v, typ)
	}
	user.Close()

	// Someone changes a value and adds a startup page: both are put back.
	k, _, _ := registry.CreateKey(registry.CURRENT_USER, root+`\Policies\Google\Chrome\RestoreOnStartupURLs`, registry.SET_VALUE)
	_ = k.SetStringValue("3", "https://evil.example")
	k.Close()
	k, _, _ = registry.CreateKey(registry.CURRENT_USER, root+`\Policies\Machine\SOFTWARE\Contoso`, registry.SET_VALUE)
	_ = k.SetDWordValue("Mode", 1)
	k.Close()
	a.applyConfig(ctx, pol, be.Users(), nil)
	st = lastStatus(t, a)
	if s := itemState(st, 1); s.Repaired != 1 {
		t.Errorf("browser repair: %+v", s)
	}
	if s := itemState(st, 2); s.Repaired != 1 {
		t.Errorf("registry repair: %+v", s)
	}
	urls = open(`Policies\Google\Chrome\RestoreOnStartupURLs`)
	if names, _ := urls.ReadValueNames(-1); len(names) != 2 {
		t.Errorf("startup pages after repair: %v", names)
	}
	urls.Close()

	// Removed: the agent deletes what it set.
	a.applyConfig(ctx, agentproto.ConfigPolicy{}, be.Users(), nil)
	if _, err := registry.OpenKey(registry.CURRENT_USER, root+`\Policies\Google\Chrome\RestoreOnStartupURLs`, registry.READ); err == nil {
		t.Error("RestoreOnStartupURLs still there")
	}
	machine = open(`Policies\Machine\SOFTWARE\Contoso`)
	if names, _ := machine.ReadValueNames(-1); len(names) != 0 {
		t.Errorf("machine values left: %v", names)
	}
	machine.Close()
}

func TestConfigWindowsScripts(t *testing.T) {
	be := NewWindowsTestConfigBackend(`Software\FleetWardenTest`, t.TempDir())
	ctx := context.Background()
	run := be.RunScript(ctx, agentproto.ScriptConfig{Shell: "powershell", Content: "Write-Output \"héllo $env:USERNAME\"\nexit 7"}, nil)
	if run.ExitCode != 7 || !strings.Contains(run.Output, "héllo") || run.Error != "" {
		t.Errorf("powershell: %+v", run)
	}
	run = be.RunScript(ctx, agentproto.ScriptConfig{Shell: "cmd", Content: "echo from cmd\r\nexit /b 3"}, nil)
	if run.ExitCode != 3 || !strings.Contains(run.Output, "from cmd") {
		t.Errorf("cmd: %+v", run)
	}
	users := be.Users()
	if len(users) != 1 || users[0].SID == "" {
		t.Fatalf("users: %+v", users)
	}
	run = be.RunScript(ctx, agentproto.ScriptConfig{Shell: "powershell", Content: "Write-Output ok"}, &users[0])
	if run.ExitCode != 0 || strings.TrimSpace(run.Output) != "ok" || run.User != users[0].Name {
		t.Errorf("as user: %+v", run)
	}
	tctx, cancel := context.WithTimeout(ctx, 3e9)
	defer cancel()
	run = be.RunScript(tctx, agentproto.ScriptConfig{Shell: "powershell", Content: "Start-Sleep 30"}, nil)
	if run.Error == "" {
		t.Errorf("timeout: %+v", run)
	}
}
