package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

func runHelperTest(t *testing.T, install func(context.Context, string) error, connect bool) (agentproto.UpdateStatus, []string) {
	t.Helper()
	dir := t.TempDir()
	plan := updatePlan{Version: "0.2.0", FromVersion: "0.1.0", Package: filepath.Join(dir, "new.deb"), Previous: filepath.Join(dir, "old.deb"),
		StateDir: dir, StartedAt: time.Now().UTC()}
	os.WriteFile(plan.Package, []byte("new"), 0o600)
	os.WriteFile(plan.Previous, []byte("old"), 0o600)
	planPath := filepath.Join(dir, planFile)
	if err := writeJSONFile(planPath, plan); err != nil {
		t.Fatal(err)
	}
	var installed []string
	oldInstall, oldStart, oldTimeout := installPackage, startService, HelperHealthTimeout
	t.Cleanup(func() { installPackage, startService, HelperHealthTimeout = oldInstall, oldStart, oldTimeout })
	HelperHealthTimeout = 3 * time.Second
	startService = func(context.Context) error { return nil }
	installPackage = func(ctx context.Context, pkg string) error {
		installed = append(installed, filepath.Base(pkg))
		if err := install(ctx, pkg); err != nil {
			return err
		}
		if connect && filepath.Base(pkg) == "new.deb" {
			// The new agent starts and reaches the server.
			_ = writeJSONFile(filepath.Join(dir, connectedFile), connectedMarker{Version: "0.2.0", At: time.Now().UTC()})
		}
		return nil
	}
	if err := RunUpdateHelper(context.Background(), planPath, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	var res agentproto.UpdateStatus
	raw, err := os.ReadFile(filepath.Join(dir, resultFile))
	if err != nil || json.Unmarshal(raw, &res) != nil {
		t.Fatalf("result: %v %s", err, raw)
	}
	if _, err := os.Stat(plan.Package); err == nil {
		t.Error("package was not cleaned up")
	}
	return res, installed
}

func TestUpdateHelperSuccess(t *testing.T) {
	res, installed := runHelperTest(t, func(context.Context, string) error { return nil }, true)
	if res.State != agentproto.UpdateCompleted || len(installed) != 1 {
		t.Errorf("result %+v, installed %v", res, installed)
	}
}

func TestUpdateHelperRollsBackWhenNewVersionStaysOffline(t *testing.T) {
	res, installed := runHelperTest(t, func(context.Context, string) error { return nil }, false)
	if res.State != agentproto.UpdateRolledBack || len(installed) != 2 || installed[1] != "old.deb" {
		t.Errorf("result %+v, installed %v", res, installed)
	}
}

func TestUpdateHelperRollsBackWhenInstallFails(t *testing.T) {
	res, installed := runHelperTest(t, func(_ context.Context, pkg string) error {
		if filepath.Base(pkg) == "new.deb" {
			return errors.New("dpkg: dependency problems")
		}
		return nil
	}, true)
	if res.State != agentproto.UpdateRolledBack || len(installed) != 2 || res.Message == "" {
		t.Errorf("result %+v, installed %v", res, installed)
	}
}

func TestReportUpdateResult(t *testing.T) {
	a := &Agent{StateDir: t.TempDir()}
	a.init()
	_ = writeJSONFile(filepath.Join(a.StateDir, resultFile), agentproto.UpdateStatus{Version: "0.2.0", State: agentproto.UpdateRolledBack, Message: "x"})
	var got []agentproto.UpdateStatus
	a.reportUpdateResult(func(typ string, v any) {
		if typ == agentproto.TypeUpdateStatus {
			got = append(got, v.(agentproto.UpdateStatus))
		}
	})
	a.reportUpdateResult(func(string, any) { t.Error("result reported twice") })
	if len(got) != 1 || got[0].State != agentproto.UpdateRolledBack {
		t.Errorf("reported %+v", got)
	}
}
