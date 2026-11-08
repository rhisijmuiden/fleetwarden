package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// memStore is a PolicyStore in memory.
type memStore struct {
	mu   *sync.Mutex
	data map[string]string
}

func (m memStore) Matches(name string, v any) (bool, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.data[name]
	return ok && cur == canonical(v), ok, nil
}

func (m memStore) Set(name string, v any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[name] = canonical(v)
	return nil
}

func (m memStore) Delete(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, name)
	return nil
}

func (m memStore) Close() error { return nil }

// fakeConfig is a ConfigBackend that keeps everything in memory.
type fakeConfig struct {
	mu     sync.Mutex
	stores map[string]map[string]string
	users  []ConfigUser
	runs   []string // script content + user
	exit   int
}

func newFakeConfig() *fakeConfig { return &fakeConfig{stores: map[string]map[string]string{}} }

func (f *fakeConfig) store(key string) memStore {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stores[key] == nil {
		f.stores[key] = map[string]string{}
	}
	return memStore{mu: &f.mu, data: f.stores[key]}
}

func (f *fakeConfig) get(key, name string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.stores[key][name]
	return v, ok
}

func (f *fakeConfig) BrowserStore(b string) (PolicyStore, error) { return f.store("browser:" + b), nil }
func (f *fakeConfig) BrowserInstalled(b string) bool             { return b != agentproto.BrowserFirefox }
func (f *fakeConfig) RegistryStore(hive, sid string) (PolicyStore, error) {
	return f.store(hive + ":" + sid), nil
}
func (f *fakeConfig) Users() []ConfigUser {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ConfigUser(nil), f.users...)
}
func (f *fakeConfig) RunScript(_ context.Context, s agentproto.ScriptConfig, u *ConfigUser) agentproto.ScriptRun {
	f.mu.Lock()
	defer f.mu.Unlock()
	who := "system"
	if u != nil {
		who = u.Name
	}
	f.runs = append(f.runs, s.Content+"@"+who)
	return agentproto.ScriptRun{ExitCode: f.exit, Output: "ran " + s.Content, User: map[bool]string{true: who}[u != nil]}
}
func (f *fakeConfig) runCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.runs)
}

func ptr[T any](v T) *T { return &v }

func testConfigAgent(t *testing.T, be ConfigBackend) *Agent {
	t.Helper()
	a := &Agent{StateDir: t.TempDir(), Config: be, BootTime: func() time.Time { return time.Date(2026, 11, 8, 8, 0, 0, 0, time.UTC) }}
	a.init()
	return a
}

func lastStatus(t *testing.T, a *Agent) agentproto.ConfigStatus {
	t.Helper()
	a.patch.mu.Lock()
	defer a.patch.mu.Unlock()
	for i := len(a.patch.pending) - 1; i >= 0; i-- {
		if m := a.patch.pending[i]; m.typ == agentproto.TypeConfigStatus {
			return m.v.(agentproto.ConfigStatus)
		}
	}
	t.Fatal("no configuration status")
	return agentproto.ConfigStatus{}
}

func itemState(st agentproto.ConfigStatus, id int64) agentproto.ConfigItemStatus {
	for _, it := range st.Items {
		if it.ID == id {
			return it
		}
	}
	return agentproto.ConfigItemStatus{}
}

func TestBrowserPolicies(t *testing.T) {
	items := []agentproto.ConfigItem{
		{ID: 1, Type: agentproto.ConfigBrowser, Browser: &agentproto.BrowserPolicy{
			Browsers: []string{"chrome", "edge", "firefox"}, Homepage: "https://intranet.example.com", ShowHomeButton: ptr(true),
			StartupMode: "urls", StartupURLs: []string{"https://intranet.example.com", "https://mail.example.com"}, PasswordManager: ptr(false),
			ChromeExtensions: []string{"cjpalhdlnbpafiamejdnhcphjbkeiagm"}, FirefoxExtensions: []string{"https://addons.mozilla.org/firefox/downloads/latest/ublock-origin/latest.xpi"},
		}},
		{ID: 2, Type: agentproto.ConfigBrowser, Browser: &agentproto.BrowserPolicy{
			Browsers: []string{"chrome"}, PasswordManager: ptr(true), Extra: map[string]map[string]any{"chrome": {"BookmarkBarEnabled": true}},
		}},
	}
	p, from := browserPolicies(items)
	c := p["chrome"]
	if c["HomepageLocation"] != "https://intranet.example.com" || c["RestoreOnStartup"] != 4 || c["PasswordManagerEnabled"] != true ||
		c["BookmarkBarEnabled"] != true || canonical(c["ExtensionInstallForcelist"]) != `["cjpalhdlnbpafiamejdnhcphjbkeiagm;`+chromeStoreUpdateURL+`"]` {
		t.Errorf("chrome: %v", c)
	}
	if _, ok := p["edge"]["ExtensionInstallForcelist"]; ok || p["edge"]["PasswordManagerEnabled"] != false {
		t.Errorf("edge: %v", p["edge"])
	}
	if got := canonical(p["firefox"]["Homepage"]); got != `{"Additional":["https://mail.example.com"],"StartPage":"homepage","URL":"https://intranet.example.com"}` {
		t.Errorf("firefox homepage: %s", got)
	}
	if len(from["chrome"]) != 2 || len(from["firefox"]) != 1 {
		t.Errorf("from: %v", from)
	}
}

func TestFlattenPolicy(t *testing.T) {
	got := regEntries(flattenPolicy("RestoreOnStartupURLs", []any{"https://a", "https://b"}, false))
	if got != "restoreonstartupurls|1|sz|https://a\nrestoreonstartupurls|2|sz|https://b" {
		t.Errorf("list: %q", got)
	}
	if got := regEntries(flattenPolicy("ShowHomeButton", true, false)); got != "|showhomebutton|dword|1" {
		t.Errorf("bool: %q", got)
	}
	if got := regEntries(flattenPolicy("ManagedBookmarks", []any{map[string]any{"name": "x", "url": "https://x"}}, false)); got != `|managedbookmarks|sz|[{"name":"x","url":"https://x"}]` {
		t.Errorf("chrome list of objects: %q", got)
	}
	got = regEntries(flattenPolicy("Homepage", map[string]any{"URL": "https://a", "Additional": []any{"https://b"}, "Locked": false}, true))
	if got != "homepage\\additional|1|sz|https://b\nhomepage|locked|dword|0\nhomepage|url|sz|https://a" {
		t.Errorf("firefox nested: %q", got)
	}
}

func TestConfigApplyRepairAndRemove(t *testing.T) {
	be := newFakeConfig()
	a := testConfigAgent(t, be)
	pol := agentproto.ConfigPolicy{Items: []agentproto.ConfigItem{
		{ID: 1, Revision: 1, Type: agentproto.ConfigBrowser, Browser: &agentproto.BrowserPolicy{Browsers: []string{"chrome", "firefox"}, Homepage: "https://intranet"}},
		{ID: 2, Revision: 1, Type: agentproto.ConfigRegistry, Registry: &agentproto.RegistrySettings{Values: []agentproto.RegistryValue{
			{Hive: "HKLM", Key: `SOFTWARE\Contoso`, Name: "Mode", Type: "dword", Value: "2"},
			{Hive: "HKCU", Key: `Software\Contoso`, Name: "Theme", Type: "string", Value: "dark"},
		}}},
		{ID: 3, Revision: 1, Type: agentproto.ConfigRegistry, Registry: &agentproto.RegistrySettings{Keep: true, Values: []agentproto.RegistryValue{
			{Hive: "HKLM", Key: `SOFTWARE\Contoso`, Name: "Installed", Type: "string", Value: "yes"},
		}}},
	}}
	a.applyConfig(context.Background(), pol, nil, nil)
	st := lastStatus(t, a)
	if v, _ := be.get("browser:chrome", "HomepageLocation"); v != `"https://intranet"` {
		t.Fatalf("chrome homepage: %q", v)
	}
	if s := itemState(st, 1); s.State != "applied" || !strings.Contains(s.Message, "Firefox is not installed") {
		t.Errorf("browser status: %+v", s)
	}
	if s := itemState(st, 2); s.State != "applied" || !strings.Contains(s.Message, "when a user logs on") {
		t.Errorf("registry status: %+v", s)
	}
	if _, ok := be.get("HKLM:", `SOFTWARE\Contoso\\Mode`); !ok {
		t.Errorf("machine value not set: %v", be.stores)
	}

	// A user logs on: their settings are set too. Someone changes the homepage.
	be.users = []ConfigUser{{Session: 2, Name: `PC\anna`, SID: "S-1-5-21-1-2-3-1001"}}
	_ = be.store("browser:chrome").Set("HomepageLocation", "https://evil")
	a.applyConfig(context.Background(), pol, be.Users(), be.Users())
	st = lastStatus(t, a)
	if v, _ := be.get("browser:chrome", "HomepageLocation"); v != `"https://intranet"` {
		t.Errorf("homepage not put back: %q", v)
	}
	if s := itemState(st, 1); s.Repaired != 1 {
		t.Errorf("repaired: %+v", s)
	}
	if v, _ := be.get("HKCU:S-1-5-21-1-2-3-1001", `Software\Contoso\\Theme`); !strings.Contains(v, "dark") {
		t.Errorf("user value: %q", v)
	}

	// The configurations no longer apply: what the agent set is removed,
	// except the value of the configuration that keeps its values.
	a.applyConfig(context.Background(), agentproto.ConfigPolicy{}, be.Users(), nil)
	if _, ok := be.get("browser:chrome", "HomepageLocation"); ok {
		t.Error("chrome policy was not removed")
	}
	if _, ok := be.get("HKLM:", `SOFTWARE\Contoso\\Mode`); ok {
		t.Error("machine value was not removed")
	}
	if _, ok := be.get("HKCU:S-1-5-21-1-2-3-1001", `Software\Contoso\\Theme`); ok {
		t.Error("user value was not removed")
	}
	if _, ok := be.get("HKLM:", `SOFTWARE\Contoso\\Installed`); !ok {
		t.Error("the kept value was removed")
	}
	if a.configHasManaged() {
		t.Errorf("state still holds managed settings: %+v", a.loadConfigState())
	}
}

func TestConfigScriptTriggers(t *testing.T) {
	be := newFakeConfig()
	a := testConfigAgent(t, be)
	a.Minute = time.Millisecond
	script := func(id int64, rev int, trigger, runAs string) agentproto.ConfigItem {
		return agentproto.ConfigItem{ID: id, Revision: rev, Type: agentproto.ConfigScript, Script: &agentproto.ScriptConfig{
			Shell: "powershell", Content: trigger + "-" + runAs, RunAs: runAs, Trigger: trigger, IntervalMinutes: 5, TimeoutMinutes: 1}}
	}
	pol := agentproto.ConfigPolicy{Items: []agentproto.ConfigItem{
		script(1, 1, "once", "system"), script(2, 1, "startup", "system"), script(3, 1, "logon", "user"), script(4, 1, "interval", "system"),
	}}
	ctx := context.Background()
	a.applyConfig(ctx, pol, nil, nil)
	if got := strings.Join(be.runs, ","); got != "once-system@system,startup-system@system,interval-system@system" {
		t.Fatalf("first run: %s", got)
	}
	st := lastStatus(t, a)
	if s := itemState(st, 3); s.State != "waiting" {
		t.Errorf("logon script without a user: %+v", s)
	}
	if s := itemState(st, 1); len(s.Runs) != 1 || s.Runs[0].Output != "ran once-system" {
		t.Errorf("runs reported: %+v", s)
	}
	// Nothing is due right away; the interval script after its interval.
	be.runs = nil
	a.applyConfig(ctx, pol, nil, nil)
	if len(be.runs) != 0 {
		t.Errorf("second run: %v", be.runs)
	}
	time.Sleep(10 * time.Millisecond)
	a.applyConfig(ctx, pol, nil, nil)
	if strings.Join(be.runs, ",") != "interval-system@system" {
		t.Errorf("interval: %v", be.runs)
	}
	// A user logs on: the logon script runs as that user.
	be.runs = nil
	u := []ConfigUser{{Session: 3, Name: `PC\bob`, SID: "S-1-5-21-9"}}
	a.applyConfig(ctx, pol, u, u)
	if strings.Join(be.runs, ",") != "logon-user@PC\\bob" {
		t.Errorf("logon: %v", be.runs)
	}
	// After a restart of the device the startup script runs again; a changed
	// once script runs again.
	be.runs = nil
	a.BootTime = func() time.Time { return time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC) }
	pol.Items[0].Revision = 2
	pol.Items[3].Script.IntervalMinutes = 600
	pol.Items[3].Revision = 1
	a.applyConfig(ctx, pol, u, nil)
	if got := strings.Join(be.runs, ","); got != "once-system@system,startup-system@system" {
		t.Errorf("after restart: %s", got)
	}
	// A failing once script is tried again, at most three times in all.
	be.runs, be.exit = nil, 1
	pol.Items = []agentproto.ConfigItem{script(5, 1, "once", "system")}
	for range 5 {
		a.applyConfig(ctx, pol, u, nil)
	}
	if len(be.runs) != scriptRetries {
		t.Errorf("retries: %v", be.runs)
	}
	if s := itemState(lastStatus(t, a), 5); s.State != "failed" {
		t.Errorf("failed script: %+v", s)
	}
}

func TestConfigPolicyIsKept(t *testing.T) {
	be := newFakeConfig()
	a := testConfigAgent(t, be)
	p := agentproto.ConfigPolicy{Items: []agentproto.ConfigItem{{ID: 7, Revision: 1, Type: agentproto.ConfigBrowser,
		Browser: &agentproto.BrowserPolicy{Browsers: []string{"edge"}, Homepage: "https://x"}}}}
	e, _ := agentproto.NewEnvelope(agentproto.TypeConfigPolicy, "", 1, p)
	a.handleConfigPolicy(e)
	raw, err := os.ReadFile(filepath.Join(a.StateDir, configPolicyFile))
	if err != nil {
		t.Fatal(err)
	}
	var back agentproto.ConfigPolicy
	if json.Unmarshal(raw, &back) != nil || back.Hash() != p.Hash() {
		t.Errorf("stored policy: %s", raw)
	}
	b := testConfigAgent(t, be)
	b.StateDir = a.StateDir
	if got := b.loadConfigPolicy(); got.Hash() != p.Hash() {
		t.Errorf("loaded policy: %+v", got)
	}
}
