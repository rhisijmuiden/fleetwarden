package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agent"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

func TestConfigurations(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses the Linux configuration backend")
	}
	e := newEnv(t)
	admin, _ := e.signedInAdmin()
	var grp, other store.DeviceGroup
	admin.do("POST", "/api/v1/groups", groupReq{Name: "Office", UpdateRing: 1}, &grp)
	admin.do("POST", "/api/v1/groups", groupReq{Name: "Lab", UpdateRing: 1}, &other)

	browser := map[string]any{"name": "Intranet start page", "type": "browser", "platform": "linux", "groupIds": []int64{grp.ID},
		"settings": map[string]any{"browsers": []string{"chrome", "firefox"}, "homepage": "https://intranet.example.com", "startupMode": "restore"}}
	bad := []struct {
		name string
		body map[string]any
		code string
	}{
		{"no name", map[string]any{"type": "browser", "platform": "linux", "settings": map[string]any{"browsers": []string{"chrome"}, "homepage": "https://x.example"}}, "invalid_name"},
		{"registry on linux", map[string]any{"name": "r", "type": "registry", "platform": "linux", "settings": map[string]any{"values": []any{}}}, "invalid_config_type"},
		{"javascript url", map[string]any{"name": "b", "type": "browser", "platform": "linux", "settings": map[string]any{"browsers": []string{"chrome"}, "homepage": "javascript:alert(1)"}}, "invalid_url"},
		{"nothing set", map[string]any{"name": "b", "type": "browser", "platform": "windows", "settings": map[string]any{"browsers": []string{"edge"}}}, "nothing_configured"},
		{"unknown field", map[string]any{"name": "b", "type": "browser", "platform": "windows", "settings": map[string]any{"browsers": []string{"edge"}, "homepage": "https://x.example", "evil": 1}}, "invalid_settings"},
		{"bad extension", map[string]any{"name": "b", "type": "browser", "platform": "windows", "settings": map[string]any{"browsers": []string{"chrome"}, "chromeExtensions": []string{"notanid"}}}, "invalid_extension"},
		{"logon on linux", map[string]any{"name": "s", "type": "script", "platform": "linux", "settings": map[string]any{"shell": "bash", "content": "true", "runAs": "system", "trigger": "logon"}}, "invalid_trigger"},
		{"user on linux", map[string]any{"name": "s", "type": "script", "platform": "linux", "settings": map[string]any{"shell": "bash", "content": "true", "runAs": "user", "trigger": "once"}}, "invalid_run_as"},
		{"powershell on linux", map[string]any{"name": "s", "type": "script", "platform": "linux", "settings": map[string]any{"shell": "powershell", "content": "x", "runAs": "system", "trigger": "once"}}, "invalid_shell"},
		{"bad dword", map[string]any{"name": "r", "type": "registry", "platform": "windows", "settings": map[string]any{"values": []any{
			map[string]any{"hive": "HKLM", "key": `SOFTWARE\Contoso`, "name": "X", "type": "dword", "value": "lots"}}}}, "invalid_registry_value"},
		{"HKU", map[string]any{"name": "r", "type": "registry", "platform": "windows", "settings": map[string]any{"values": []any{
			map[string]any{"hive": "HKLM", "key": `HKEY_USERS\.DEFAULT\X`, "name": "X", "type": "string", "value": "1"}}}}, "invalid_registry_key"},
		{"unknown group", map[string]any{"name": "g", "type": "script", "platform": "linux", "groupIds": []int64{9999}, "settings": map[string]any{"shell": "sh", "content": "true", "runAs": "system", "trigger": "once"}}, "unknown_group"},
	}
	for _, b := range bad {
		var er struct{ Error string }
		if code := admin.do("POST", "/api/v1/configurations", b.body, &er); code != 400 || er.Error != b.code {
			t.Errorf("%s: %d %q", b.name, code, er.Error)
		}
	}

	// A registry configuration accepts keys with the hive in front and hex numbers.
	var reg store.Configuration
	if code := admin.do("POST", "/api/v1/configurations", map[string]any{"name": "Contoso settings", "type": "registry", "platform": "windows",
		"settings": map[string]any{"values": []any{
			map[string]any{"hive": "HKLM", "key": `HKEY_CURRENT_USER\Software/Contoso\`, "name": "Mode", "type": "dword", "value": "0x10"}}}}, &reg); code != 201 ||
		!strings.Contains(string(reg.Settings), `"hive":"HKCU"`) || !strings.Contains(string(reg.Settings), `"key":"Software\\Contoso"`) ||
		!strings.Contains(string(reg.Settings), `"value":"16"`) {
		t.Fatalf("registry: %d %s", code, reg.Settings)
	}

	var cfg, script store.Configuration
	if code := admin.do("POST", "/api/v1/configurations", browser, &cfg); code != 201 || cfg.Revision != 1 || !cfg.Enabled {
		t.Fatalf("create: %d %+v", code, cfg)
	}
	if code := admin.do("POST", "/api/v1/configurations", browser, nil); code != 409 {
		t.Errorf("duplicate name: %d", code)
	}
	if code := admin.do("POST", "/api/v1/configurations", map[string]any{"name": "Hello", "type": "script", "platform": "linux", "groupIds": []int64{grp.ID},
		"priority": 200, "settings": map[string]any{"shell": "bash", "content": "echo hello from $(basename $0 .sh | cut -c1-6)\nexit 0", "runAs": "system", "trigger": "once"}}, &script); code != 201 {
		t.Fatalf("script: %d", code)
	}

	// A device in the group gets both and applies them.
	_, info := e.createToken(admin, grp.ID)
	root := t.TempDir()
	a := newTestAgent(t, e, info.Token, "LNX-CFG1", false)
	a.Config = agent.NewUnixConfigBackend(root, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	go a.Run(ctx) //nolint:errcheck
	d := e.onlineDevice(admin, "LNX-CFG1")

	view := func(id int64) configView {
		var v configView
		if code := admin.do("GET", fmt.Sprintf("/api/v1/configurations/%d", id), nil, &v); code != 200 {
			t.Fatalf("configuration %d: %d", id, code)
		}
		return v
	}
	waitState := func(id int64, state string, rev int) store.DeviceConfigState {
		t.Helper()
		var got store.DeviceConfigState
		waitFor(t, fmt.Sprintf("configuration %d %s", id, state), func() bool {
			v := view(id)
			if len(v.Devices) != 1 {
				return false
			}
			got = v.Devices[0]
			return got.State == state && got.Revision == rev
		})
		return got
	}
	st := waitState(cfg.ID, "applied", 1)
	if st.DeviceID != d.ID || st.AppliedAt == nil || !st.Online || !strings.Contains(st.Message, "not installed") {
		t.Errorf("state: %+v", st)
	}
	chrome := filepath.Join(root, "etc/opt/chrome/policies/managed/fleetwarden.json")
	if raw, err := os.ReadFile(chrome); err != nil || !strings.Contains(string(raw), "https://intranet.example.com") {
		t.Fatalf("chrome policy: %s %v", raw, err)
	}
	ss := waitState(script.ID, "applied", 1)
	if ss.RunAt == nil || ss.RunExitCode == nil || *ss.RunExitCode != 0 || !strings.Contains(ss.RunOutput, "hello from config") {
		t.Errorf("script state: %+v", ss)
	}
	var runs []store.ConfigRun
	if code := admin.do("GET", fmt.Sprintf("/api/v1/configurations/%d/runs", script.ID), nil, &runs); code != 200 || len(runs) != 1 || runs[0].Hostname != "LNX-CFG1" {
		t.Errorf("runs: %d %+v", code, runs)
	}
	var dv deviceConfigsView
	if code := admin.do("GET", "/api/v1/devices/"+d.ID+"/configurations", nil, &dv); code != 200 || len(dv.Items) != 2 ||
		dv.Items[0].ConfigurationName != "Intranet start page" || len(dv.Runs) != 1 {
		t.Errorf("device configurations: %d %+v", code, dv)
	}
	var list configList
	if code := admin.do("GET", "/api/v1/configurations", nil, &list); code != 200 || len(list.Items) != 3 || list.Items[0].Name != "Contoso settings" {
		t.Fatalf("list: %d %+v", code, list)
	}
	for _, c := range list.Items {
		if c.ID == cfg.ID && (c.Devices != 1 || c.Applied != 1) {
			t.Errorf("counts: %+v", c)
		}
	}

	// Someone changes the file; "Apply now" puts it back and reports it.
	os.WriteFile(chrome, []byte(`{"HomepageLocation":"https://evil.example","HomepageIsNewTabPage":false,"RestoreOnStartup":1}`), 0o644)
	var ar applyResult
	if code := admin.do("POST", fmt.Sprintf("/api/v1/configurations/%d/apply", cfg.ID), nil, &ar); code != 200 || ar.Sent != 1 {
		t.Errorf("apply: %d %+v", code, ar)
	}
	waitFor(t, "repair reported", func() bool { return view(cfg.ID).Devices[0].RepairedTotal == 1 })
	if raw, _ := os.ReadFile(chrome); !strings.Contains(string(raw), "intranet.example.com") {
		t.Errorf("not repaired: %s", raw)
	}

	// Read-only users and technicians see configurations without settings and cannot change them.
	ro := e.newUserClient(admin, "reader", "readonly")
	tech := e.newUserClient(admin, "tech", "technician")
	var roList configList
	if code := ro.do("GET", "/api/v1/configurations", nil, &roList); code != 200 || len(roList.Items) != 3 || string(roList.Items[1].Settings) != "null" {
		t.Errorf("readonly list: %d %+v", code, roList)
	}
	if code := tech.do("POST", "/api/v1/configurations", browser, nil); code != 403 {
		t.Errorf("technician create: %d", code)
	}
	if code := ro.do("POST", "/api/v1/devices/"+d.ID+"/configurations/apply", nil, nil); code != 403 {
		t.Errorf("readonly apply: %d", code)
	}
	if code := tech.do("POST", "/api/v1/devices/"+d.ID+"/configurations/apply", nil, nil); code != 200 {
		t.Errorf("technician apply on a device: %d", code)
	}

	// A change of the settings raises the revision; the agent applies it.
	browser["settings"] = map[string]any{"browsers": []string{"chrome"}, "homepage": "https://portal.example.com"}
	var upd store.Configuration
	if code := admin.do("PUT", fmt.Sprintf("/api/v1/configurations/%d", cfg.ID), browser, &upd); code != 200 || upd.Revision != 2 {
		t.Fatalf("update: %d %+v", code, upd)
	}
	waitState(cfg.ID, "applied", 2)
	if raw, _ := os.ReadFile(chrome); !strings.Contains(string(raw), "portal.example.com") {
		t.Errorf("updated policy: %s", raw)
	}
	if code := admin.do("PUT", fmt.Sprintf("/api/v1/configurations/%d", cfg.ID), map[string]any{"name": "x", "type": "script", "settings": browser["settings"]}, nil); code != 400 {
		t.Errorf("type change: %d", code)
	}
	// Only the name changes: same revision.
	browser["name"] = "Portal start page"
	if code := admin.do("PUT", fmt.Sprintf("/api/v1/configurations/%d", cfg.ID), browser, &upd); code != 200 || upd.Revision != 2 {
		t.Errorf("rename: %d %+v", code, upd)
	}

	// Assigned to another group: the agent removes the policy file and the state goes.
	browser["groupIds"] = []int64{other.ID}
	if code := admin.do("PUT", fmt.Sprintf("/api/v1/configurations/%d", cfg.ID), browser, nil); code != 200 {
		t.Fatalf("reassign: %d", code)
	}
	waitFor(t, "policy file removed", func() bool {
		_, err := os.Stat(chrome)
		return os.IsNotExist(err)
	})
	waitFor(t, "state pruned", func() bool {
		var dv deviceConfigsView
		admin.do("GET", "/api/v1/devices/"+d.ID+"/configurations", nil, &dv)
		return len(dv.Items) == 1 && dv.Items[0].ConfigurationID == script.ID
	})
	if code := admin.do("DELETE", fmt.Sprintf("/api/v1/configurations/%d", script.ID), nil, nil); code != 204 {
		t.Errorf("delete: %d", code)
	}
	if code := admin.do("GET", fmt.Sprintf("/api/v1/configurations/%d", script.ID), nil, nil); code != 404 {
		t.Errorf("deleted: %d", code)
	}
}
