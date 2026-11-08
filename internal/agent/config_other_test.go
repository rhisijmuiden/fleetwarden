//go:build !windows

package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// TestConfigLinuxBrowserFiles applies browser policies to the JSON files the
// browsers read on Linux.
func TestConfigLinuxBrowserFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux paths")
	}
	root := t.TempDir()
	// Another tool's Firefox policy stays.
	ff := filepath.Join(root, "etc/firefox/policies/policies.json")
	os.MkdirAll(filepath.Dir(ff), 0o755)
	os.WriteFile(ff, []byte(`{"policies":{"DisableTelemetry":true}}`), 0o644)
	os.MkdirAll(filepath.Join(root, "usr/bin"), 0o755)
	os.WriteFile(filepath.Join(root, "usr/bin/chromium"), nil, 0o755)
	be := NewUnixConfigBackend(root, t.TempDir())
	a := testConfigAgent(t, be)
	pol := agentproto.ConfigPolicy{Items: []agentproto.ConfigItem{{ID: 1, Revision: 1, Type: agentproto.ConfigBrowser,
		Browser: &agentproto.BrowserPolicy{Browsers: []string{"chrome", "firefox"}, Homepage: "https://intranet", StartupMode: "restore"}}}}
	a.applyConfig(context.Background(), pol, nil, nil)
	for _, p := range []string{"etc/opt/chrome/policies/managed/fleetwarden.json", "etc/chromium/policies/managed/fleetwarden.json"} {
		raw, err := os.ReadFile(filepath.Join(root, p))
		if err != nil || !strings.Contains(string(raw), `"HomepageLocation": "https://intranet"`) || !strings.Contains(string(raw), `"RestoreOnStartup": 1`) {
			t.Errorf("%s: %s %v", p, raw, err)
		}
	}
	raw, _ := os.ReadFile(ff)
	if !strings.Contains(string(raw), `"DisableTelemetry": true`) || !strings.Contains(string(raw), `"StartPage": "previous-session"`) {
		t.Errorf("firefox: %s", raw)
	}
	if s := itemState(lastStatus(t, a), 1); s.State != "applied" {
		t.Errorf("status: %+v", s)
	}
	// Someone edits the file; the next run puts it back.
	chrome := filepath.Join(root, "etc/opt/chrome/policies/managed/fleetwarden.json")
	os.WriteFile(chrome, []byte(`{"HomepageLocation":"https://other","HomepageIsNewTabPage":false,"RestoreOnStartup":1}`), 0o644)
	a.applyConfig(context.Background(), pol, nil, nil)
	if s := itemState(lastStatus(t, a), 1); s.Repaired != 1 {
		t.Errorf("repair: %+v", s)
	}
	// Removed: the Chrome file goes, the Firefox file keeps the other policy.
	a.applyConfig(context.Background(), agentproto.ConfigPolicy{}, nil, nil)
	if _, err := os.Stat(chrome); !os.IsNotExist(err) {
		t.Errorf("chrome file still there: %v", err)
	}
	raw, _ = os.ReadFile(ff)
	if string(raw) != "{\n  \"policies\": {\n    \"DisableTelemetry\": true\n  }\n}\n" {
		t.Errorf("firefox after removal: %q", raw)
	}
}

func TestConfigLinuxScript(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux shells")
	}
	be := NewUnixConfigBackend(t.TempDir(), t.TempDir())
	run := be.RunScript(context.Background(), agentproto.ScriptConfig{Shell: "bash", Content: "echo hello $FLEETWARDEN_CONFIG; exit 3"}, nil)
	if run.ExitCode != 3 || strings.TrimSpace(run.Output) != "hello 1" || run.Error != "" {
		t.Errorf("run: %+v", run)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	run = be.RunScript(ctx, agentproto.ScriptConfig{Shell: "sh", Content: "sleep 5"}, nil)
	if run.Error == "" {
		t.Errorf("timeout: %+v", run)
	}
}
