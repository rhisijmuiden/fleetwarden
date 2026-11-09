package agent

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

func TestParseEventXML(t *testing.T) {
	const ev = `<Event xmlns='http://schemas.microsoft.com/win/2004/08/events/event'><System><Provider Name='Service Control Manager' Guid='{555908d1-a6d7-4695-8e1e-26931d2012f4}' EventSourceName='Service Control Manager'/><EventID Qualifiers='49152'>7031</EventID><Version>0</Version><Level>2</Level><TimeCreated SystemTime='2026-11-08T21:14:03.1234567Z'/><Execution ProcessID='812' ThreadID='9000'/><Channel>System</Channel><Computer>PC-01</Computer><Security UserID='S-1-5-18'/></System><EventData><Data Name='param1'>Print Spooler</Data><Data Name='param2'>1</Data></EventData></Event>`
	e, err := parseEventXML(ev)
	if err != nil {
		t.Fatal(err)
	}
	if e.Provider != "Service Control Manager" || e.EventID != 7031 || e.Level != 2 || e.PID != 812 || e.User != "S-1-5-18" ||
		!e.Time.Equal(time.Date(2026, 11, 8, 21, 14, 3, 123456700, time.UTC)) {
		t.Errorf("event: %+v", e)
	}
	if e.Message != "param1: Print Spooler\nparam2: 1" {
		t.Errorf("message from data: %q", e.Message)
	}
	// LogAlways (level 0) shows as information; a rendered message wins.
	e, _ = parseEventXML(`<Event><System><Provider Name='x'/><EventID>1</EventID><Level>0</Level></System><RenderingInfo><Message>Hallo wereld é</Message></RenderingInfo></Event>`)
	if e.Level != agentproto.LevelInfo || e.Message != "Hallo wereld é" {
		t.Errorf("event: %+v", e)
	}
}

func TestEventXPath(t *testing.T) {
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	if q := eventXPath(agentproto.LogQuery{}, now); q != "*" {
		t.Errorf("all: %s", q)
	}
	q := eventXPath(agentproto.LogQuery{MaxLevel: 2, Since: now.Add(-time.Hour), Provider: "Service Control Manager", EventID: 7031}, now)
	want := "*[System[(Level=1 or Level=2) and TimeCreated[timediff(@SystemTime) <= 3600000] and Provider[@Name='Service Control Manager'] and EventID=7031]]"
	if q != want {
		t.Errorf("query:\n%s\n%s", q, want)
	}
	if q := eventXPath(agentproto.LogQuery{MaxLevel: 4}, now); !strings.Contains(q, "Level=0") {
		t.Errorf("information includes LogAlways: %s", q)
	}
	for _, n := range []string{"System", "Microsoft-Windows-Windows Defender/Operational"} {
		if !validLogName(n) {
			t.Errorf("%q refused", n)
		}
	}
	for _, n := range []string{"", "a'b", "x]"} {
		if validLogName(n) {
			t.Errorf("%q accepted", n)
		}
	}
}

func TestServiceNames(t *testing.T) {
	for _, n := range []string{"Spooler", "ssh.service", "getty@tty1.service", "MSSQL$SQLEXPRESS", "Windows Update"} {
		if !validServiceName(n) {
			t.Errorf("%q refused", n)
		}
	}
	for _, n := range []string{"", "-x", "a;b", "a/b"} {
		if validServiceName(n) {
			t.Errorf("%q accepted", n)
		}
	}
	if !isAgentService("FleetWardenAgent") || !isAgentService("fleetwarden-agent.service") || isAgentService("ssh") {
		t.Error("agent service")
	}
}

func TestManageFiles(t *testing.T) {
	a := &Agent{}
	ctx := context.Background()
	dir := t.TempDir()
	r := a.runManage(ctx, agentproto.ManageRequest{Op: agentproto.ManageFilesMkdir, Path: filepath.Join(dir, "sub")})
	if r.Error != "" {
		t.Fatal(r)
	}
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("bb"), 0o644)
	os.WriteFile(filepath.Join(dir, "A.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(dir, ".hidden"), nil, 0o644)
	r = a.runManage(ctx, agentproto.ManageRequest{Op: agentproto.ManageFilesList, Path: dir})
	var names []string
	for _, f := range r.Files {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "sub,.hidden,A.txt,b.txt" || !r.Files[0].Dir || r.Files[3].Size != 2 {
		t.Errorf("list: %v %+v", names, r)
	}
	if runtime.GOOS != "windows" && !r.Files[1].Hidden {
		t.Errorf("dot file not hidden: %+v", r.Files[1])
	}
	if r := a.runManage(ctx, agentproto.ManageRequest{Op: agentproto.ManageFilesDelete, Path: filepath.Dir(filepath.VolumeName(dir) + string(filepath.Separator))}); r.Error != agentproto.ManageErrProtected {
		t.Errorf("delete root: %+v", r)
	}
	if r := a.runManage(ctx, agentproto.ManageRequest{Op: agentproto.ManageFilesList, Path: filepath.Join(dir, "missing")}); r.Error != agentproto.ManageErrNotFound {
		t.Errorf("missing: %+v", r)
	}
	if r := a.runManage(ctx, agentproto.ManageRequest{Op: agentproto.ManageFilesList, Path: "rel"}); r.Error != agentproto.ManageErrInvalid {
		t.Errorf("relative: %+v", r)
	}
	if r := a.runManage(ctx, agentproto.ManageRequest{Op: agentproto.ManageKillProcess, PID: os.Getpid()}); r.Error != agentproto.ManageErrProtected {
		t.Errorf("kill self: %+v", r)
	}

	var buf bytes.Buffer
	if err := zipDir(ctx, dir, &buf); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Base(dir)
	got := map[string]bool{}
	for _, f := range zr.File {
		got[f.Name] = true
	}
	if !got[base+"/A.txt"] || !got[base+"/sub/"] {
		t.Errorf("zip: %v", got)
	}
}

// TestManagePlatform runs the real backend: on Linux in every test run, on
// Windows in the Windows CI job.
func TestManagePlatform(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("no backend")
	}
	b := platformManage()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	ps, err := b.Processes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var self *agentproto.Process
	for i := range ps {
		if ps[i].PID == os.Getpid() {
			self = &ps[i]
		}
	}
	if self == nil || self.Memory == 0 || self.Path == "" || self.User == "" || self.StartedAt == nil {
		t.Fatalf("own process missing or incomplete among %d: %+v", len(ps), self)
	}
	// Stop a program.
	name, args := "sleep", []string{"60"}
	if runtime.GOOS == "windows" {
		name, args = "ping", []string{"-n", "60", "127.0.0.1"}
	}
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := b.KillProcess(cmd.Process.Pid); err != nil {
		t.Fatalf("kill: %v", err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case <-waited:
	case <-time.After(10 * time.Second):
		t.Error("the process still runs")
	}
	roots, err := b.Roots()
	if err != nil || len(roots) == 0 || !roots[0].Dir {
		t.Errorf("roots: %+v %v", roots, err)
	}
	if runtime.GOOS == "linux" {
		if _, err := os.Stat("/run/systemd/system"); err != nil {
			t.Log("no systemd here; skipping services and journal")
			return
		}
	}
	ss, err := b.Services(ctx)
	if err != nil || len(ss) == 0 {
		t.Fatalf("services: %d %v", len(ss), err)
	}
	running := 0
	for _, s := range ss {
		if s.State == "running" {
			running++
		}
	}
	if running == 0 {
		t.Errorf("no running service among %d", len(ss))
	}
	src, err := b.LogSources(ctx)
	if err != nil || len(src) == 0 {
		t.Fatalf("log sources: %+v %v", src, err)
	}
	ev, _, err := b.QueryLog(ctx, agentproto.LogQuery{Source: src[0].Name, Max: 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d processes, %d services, %d events from %s", len(ps), len(ss), len(ev), src[0].Name)
	if runtime.GOOS == "windows" {
		if len(ev) == 0 || ev[0].Provider == "" || ev[0].Time.IsZero() {
			t.Errorf("events: %+v", ev)
		}
		// Restart a real service and change its startup type back and forth.
		if err := b.ServiceAction(ctx, "Spooler", agentproto.ServiceRestart); err != nil {
			t.Errorf("restart Spooler: %v", err)
		}
		if err := b.ServiceAction(ctx, "Spooler", agentproto.ServiceManual); err != nil {
			t.Errorf("Spooler manual: %v", err)
		}
		if err := b.ServiceAction(ctx, "Spooler", agentproto.ServiceAuto); err != nil {
			t.Errorf("Spooler automatic: %v", err)
		}
		ss, _ = b.Services(ctx)
		for _, s := range ss {
			if s.Name == "Spooler" && (s.State != "running" || s.StartType != agentproto.ServiceAuto) {
				t.Errorf("Spooler after the actions: %+v", s)
			}
		}
		if ev, _, err := b.QueryLog(ctx, agentproto.LogQuery{Source: "System", MaxLevel: 4, Since: time.Now().Add(-30 * 24 * time.Hour), Max: 5}); err != nil || len(ev) == 0 || ev[0].Message == "" {
			t.Errorf("filtered events: %+v %v", ev, err)
		}
	}
}
