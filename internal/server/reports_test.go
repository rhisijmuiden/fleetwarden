package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/catalog"
	"github.com/rhisijmuiden/fleetwarden/internal/mail"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

func TestNextReportRun(t *testing.T) {
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	now := time.Date(2026, 11, 8, 10, 0, 0, 0, ams) // a Sunday
	cases := []struct {
		r    store.ReportSchedule
		want time.Time
	}{
		{store.ReportSchedule{Frequency: "daily", SendAt: 8 * 60, TimeZone: "Europe/Amsterdam"}, time.Date(2026, 11, 9, 8, 0, 0, 0, ams)},
		{store.ReportSchedule{Frequency: "daily", SendAt: 11 * 60, TimeZone: "Europe/Amsterdam"}, time.Date(2026, 11, 8, 11, 0, 0, 0, ams)},
		{store.ReportSchedule{Frequency: "weekly", Weekday: 1, SendAt: 8 * 60, TimeZone: "Europe/Amsterdam"}, time.Date(2026, 11, 9, 8, 0, 0, 0, ams)},
		{store.ReportSchedule{Frequency: "weekly", Weekday: 0, SendAt: 9 * 60, TimeZone: "Europe/Amsterdam"}, time.Date(2026, 11, 15, 9, 0, 0, 0, ams)},
		{store.ReportSchedule{Frequency: "weekly", Weekday: 0, SendAt: 12 * 60, TimeZone: "Europe/Amsterdam"}, time.Date(2026, 11, 8, 12, 0, 0, 0, ams)},
		{store.ReportSchedule{Frequency: "monthly", MonthDay: 1, SendAt: 7 * 60, TimeZone: "Europe/Amsterdam"}, time.Date(2026, 12, 1, 7, 0, 0, 0, ams)},
		{store.ReportSchedule{Frequency: "monthly", MonthDay: 28, SendAt: 7 * 60, TimeZone: "Europe/Amsterdam"}, time.Date(2026, 11, 28, 7, 0, 0, 0, ams)},
	}
	for i, c := range cases {
		if got := nextReportRun(c.r, now); !got.Equal(c.want) {
			t.Errorf("case %d: %v, want %v", i, got, c.want)
		}
	}
	// A run exactly at the send time moves to the next period.
	r := store.ReportSchedule{Frequency: "daily", SendAt: 8 * 60, TimeZone: "Europe/Amsterdam"}
	if got := nextReportRun(r, time.Date(2026, 11, 9, 8, 0, 0, 0, ams)); !got.Equal(time.Date(2026, 11, 10, 8, 0, 0, 0, ams)) {
		t.Errorf("at send time: %v", got)
	}
}

// seedDevice registers a device with inventory and a patch scan straight in the store.
func seedDevice(t *testing.T, st *store.Store, group int64, host, platform string, vols []agentproto.Volume, sw []store.SoftwareRow, patches []store.PatchRow) string {
	t.Helper()
	ctx := context.Background()
	id := newUUID()
	if err := st.CreateDevice(ctx, store.Device{ID: id, GroupID: group, PublicKey: []byte("k"), Hostname: host, Platform: platform}); err != nil {
		t.Fatal(err)
	}
	hw, _ := json.Marshal(agentproto.Hardware{Volumes: vols})
	if err := st.SaveInventory(ctx, id, store.InventoryUpdate{Hostname: host, Kind: "workstation", Hardware: hw, Software: sw}); err != nil {
		t.Fatal(err)
	}
	if patches != nil {
		if err := st.SaveScanResult(ctx, id, store.ScanResult{Source: "wua", Patches: patches}); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func TestReports(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()
	ctx := context.Background()

	cdn, err := catalog.FakeCDN(t.TempDir(), []catalog.FakePackage{
		{ID: "7zip.7zip", Name: "7-Zip", Version: "26.06", Publisher: "Igor Pavlov", ARPNames: []string{"7-Zip (x64)"}},
		{ID: "VideoLAN.VLC", Name: "VLC media player", Version: "3.0.24", Publisher: "VideoLAN"},
		{ID: "Google.Chrome", Name: "Google Chrome", Version: "157.0.1", Publisher: "Google LLC"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cdnTS := httptest.NewServer(cdn)
	defer cdnTS.Close()
	e.srv.winget = &catalog.Winget{Dir: t.TempDir(), BaseURL: cdnTS.URL}
	if err := e.srv.winget.Refresh(ctx); err != nil {
		t.Fatal(err)
	}

	var office store.DeviceGroup
	if code := admin.do("POST", "/api/v1/groups", map[string]any{"name": "Office", "updateRing": 1}, &office); code != 201 {
		t.Fatalf("create group: %d", code)
	}
	gb := int64(1 << 30)
	okDisk := []agentproto.Volume{{Mount: "C:", SizeBytes: 100 * gb, FreeBytes: 60 * gb}, {Mount: "EFI", SizeBytes: 1 << 20, FreeBytes: 0}}
	old := time.Now().Add(-40 * 24 * time.Hour)
	recent := time.Now()
	sec := func(id, title string, released time.Time) store.PatchRow {
		return store.PatchRow{UpdateID: id, Title: title, KB: "KB" + id, Category: "security", ReleasedAt: &released}
	}
	good := seedDevice(t, e.st, 1, "PC-GOOD", "windows", okDisk,
		[]store.SoftwareRow{{Name: "7-Zip 26.06 (x64)", Version: "26.06", Publisher: "Igor Pavlov"}}, []store.PatchRow{sec("1", "New update", recent)})
	seedDevice(t, e.st, 1, "PC-OLD7ZIP", "windows", okDisk,
		[]store.SoftwareRow{{Name: "7-Zip 23.01 (x64)", Version: "23.01", Publisher: "Igor Pavlov"}, {Name: "VLC media player", Version: "3.0.20", Publisher: "VideoLAN"},
			{Name: "Unknown Tool", Version: "1.0"}}, []store.PatchRow{})
	seedDevice(t, e.st, office.ID, "PC-PATCH", "windows", okDisk, nil,
		[]store.PatchRow{sec("2", "2026-10 Security Update", old), {UpdateID: "3", Title: "Driver", Category: "driver"}})
	seedDevice(t, e.st, office.ID, "SRV-FULL", "linux", []agentproto.Volume{{Mount: "/", SizeBytes: 50 * gb, FreeBytes: 2 * gb}}, nil, []store.PatchRow{})
	stale := seedDevice(t, e.st, office.ID, "PC-STALE", "windows", okDisk, nil, []store.PatchRow{})
	seedDevice(t, e.st, office.ID, "PC-NOSCAN", "windows", okDisk, nil, nil)
	pool, err := pgxpool.New(ctx, os.Getenv("FW_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, "UPDATE devices SET last_seen_at = now() - interval '30 days' WHERE id::text = $1", stale); err != nil {
		t.Fatal(err)
	}

	var comp complianceReport
	if code := admin.do("GET", "/api/v1/reports/compliance", nil, &comp); code != 200 {
		t.Fatalf("compliance: %d", code)
	}
	reasons := map[string]complianceDevice{}
	for _, d := range comp.List {
		reasons[d.Hostname] = d
	}
	if comp.Devices != 6 || comp.Compliant != 1 || !reasons["PC-GOOD"].Compliant || reasons["PC-GOOD"].ID != good {
		t.Errorf("compliance: %d of %d, %+v", comp.Compliant, comp.Devices, reasons["PC-GOOD"])
	}
	if d := reasons["PC-OLD7ZIP"]; d.Outdated != 2 || d.Compliant {
		t.Errorf("outdated software: %+v", d)
	}
	if d := reasons["PC-PATCH"]; d.Security != 1 || d.Compliant {
		t.Errorf("old security update: %+v", d)
	}
	if d := reasons["SRV-FULL"]; len(d.LowDisk) != 1 || d.LowDisk[0].FreePercent != 4 {
		t.Errorf("low disk: %+v", d)
	}
	if !reasons["PC-STALE"].Stale || !reasons["PC-NOSCAN"].NotScanned {
		t.Errorf("stale / not scanned: %+v %+v", reasons["PC-STALE"], reasons["PC-NOSCAN"])
	}
	if len(comp.Groups) != 2 || comp.Groups[0].Name != "Default" || comp.Groups[0].Score != 50 || comp.Groups[1].Score != 0 {
		t.Errorf("groups: %+v", comp.Groups)
	}

	// Group filter.
	if code := admin.do("GET", "/api/v1/reports/compliance?groups=1", nil, &comp); code != 200 || comp.Devices != 2 || comp.Scope != "Groups: Default" {
		t.Errorf("group filter: %d %d %q", code, comp.Devices, comp.Scope)
	}
	if code := admin.do("GET", "/api/v1/reports/compliance?groups=x", nil, nil); code != 400 {
		t.Errorf("bad group: %d", code)
	}

	var sw softwareReport
	if code := admin.do("GET", "/api/v1/reports/software", nil, &sw); code != 200 {
		t.Fatalf("software: %d", code)
	}
	if sw.Devices != 5 || sw.DevicesOutdated != 1 || len(sw.Packages) != 2 || sw.Matched != 3 {
		t.Errorf("software report: %+v", sw)
	}
	// Ignoring VLC leaves only 7-Zip.
	if code := admin.do("POST", "/api/v1/reports/software/ignore", map[string]any{"packageId": "VideoLAN.VLC", "ignore": true}, nil); code != 200 {
		t.Fatalf("ignore: %d", code)
	}
	admin.do("GET", "/api/v1/reports/software", nil, &sw)
	if len(sw.Packages) != 1 || sw.Packages[0].PackageID != "7zip.7zip" || sw.Packages[0].Versions[0] != "23.01" {
		t.Errorf("after ignore: %+v", sw.Packages)
	}

	var pr patchesReport
	if code := admin.do("GET", "/api/v1/reports/patches", nil, &pr); code != 200 {
		t.Fatalf("patches: %d", code)
	}
	if pr.DevicesMissing != 2 || pr.DevicesSecurity != 2 || len(pr.Updates) != 3 || pr.Updates[0].Category != "security" {
		t.Errorf("patches report: %+v", pr)
	}
	// A declined update is not missing.
	if code := admin.do("PUT", "/api/v1/patches/decision", map[string]any{"updateId": "3", "decision": "declined"}, nil); code != 204 {
		t.Fatalf("decline: %d", code)
	}
	admin.do("GET", "/api/v1/reports/patches", nil, &pr)
	if len(pr.Updates) != 2 {
		t.Errorf("declined update still listed: %+v", pr.Updates)
	}

	// Exports.
	get := func(path string) (int, http.Header, []byte) {
		req, _ := http.NewRequest("GET", e.ts.URL+path, nil)
		res, err := admin.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, res.Header, b
	}
	for _, kind := range reportKinds {
		code, h, b := get("/api/v1/reports/" + kind + "/export?format=pdf&lang=nl")
		if code != 200 || h.Get("Content-Type") != "application/pdf" || !bytes.HasPrefix(b, []byte("%PDF-")) ||
			!strings.Contains(h.Get("Content-Disposition"), "fleetwarden-"+kind+"-") {
			t.Errorf("%s pdf: %d %v %d bytes", kind, code, h, len(b))
		}
		if out := os.Getenv("FW_TEST_PDF_OUT"); out != "" {
			os.WriteFile(out+"-"+kind+".pdf", b, 0o644)
		}
	}
	code, _, csv := get("/api/v1/reports/software/export?format=csv&lang=nl")
	if code != 200 || !strings.Contains(string(csv), "PC-OLD7ZIP;Default;7-Zip 23.01 (x64);Igor Pavlov;23.01;26.06;7zip.7zip") {
		t.Errorf("software csv: %d\n%s", code, csv)
	}
	if code, _, _ := get("/api/v1/reports/nope/export?format=pdf"); code != 404 {
		t.Errorf("unknown report: %d", code)
	}

	// Read-only users see reports but cannot schedule them.
	ro := e.newUserClient(admin, "reader", "readonly")
	if code := ro.do("GET", "/api/v1/reports/compliance", nil, nil); code != 200 {
		t.Errorf("read-only report: %d", code)
	}
	if code := ro.do("GET", "/api/v1/reports/schedules", nil, nil); code != 403 {
		t.Errorf("read-only schedules: %d", code)
	}

	// Schedules.
	sched := map[string]any{"name": "Weekly", "enabled": true, "reports": []string{"software", "compliance", "bogus"}, "groupIds": []int64{},
		"frequency": "weekly", "weekday": 1, "monthDay": 1, "sendAt": 480, "timeZone": "Europe/Amsterdam",
		"recipients": []string{"it@example.com", " boss@example.com "}, "language": "nl", "formats": []string{"pdf", "csv"}}
	var created store.ReportSchedule
	if code := admin.do("POST", "/api/v1/reports/schedules", sched, &created); code != 201 {
		t.Fatalf("create schedule: %d", code)
	}
	if strings.Join(created.Reports, ",") != "compliance,software" || created.NextRunAt == nil || created.NextRunAt.Weekday() != time.Monday ||
		len(created.Recipients) != 2 || created.Recipients[1] != "boss@example.com" {
		t.Errorf("created: %+v", created)
	}
	sched["recipients"] = []string{"not-an-address"}
	if code := admin.do("POST", "/api/v1/reports/schedules", sched, nil); code != 400 {
		t.Errorf("bad recipient: %d", code)
	}
	sched["recipients"] = []string{"x@example.com"}
	if code := admin.do("POST", "/api/v1/reports/schedules", sched, nil); code != 409 {
		t.Errorf("duplicate name: %d", code)
	}
	// Sending needs a mail server.
	if code := admin.do("POST", "/api/v1/reports/schedules/"+itoa(created.ID)+"/send", nil, nil); code != 409 {
		t.Errorf("send without mail: %d", code)
	}
	e.configureMail(admin)
	if code := admin.do("POST", "/api/v1/reports/schedules/"+itoa(created.ID)+"/send", nil, nil); code != 200 {
		t.Fatalf("send now: %d", code)
	}
	e.srv.ProcessMailQueue(ctx)
	e.mailer.mu.Lock()
	sent := append([]mail.Message(nil), e.mailer.sent...)
	e.mailer.mu.Unlock()
	if len(sent) != 2 {
		t.Fatalf("sent %d messages", len(sent))
	}
	m := sent[0]
	if !strings.Contains(m.Subject, "Weekly") || len(m.Attachments) != 3 || m.Attachments[0].ContentType != "application/pdf" ||
		!bytes.HasPrefix(m.Attachments[0].Data, []byte("%PDF-")) || !strings.HasSuffix(m.Attachments[2].Name, ".csv") ||
		!strings.Contains(m.HTML, "Compliance") {
		t.Errorf("report mail: %s %d attachments", m.Subject, len(m.Attachments))
	}
	var log []store.Mail
	admin.do("GET", "/api/v1/mail/log", nil, &log)
	if len(log) == 0 || log[0].Kind != "report" || log[0].Status != "sent" {
		t.Errorf("mail log: %+v", log)
	}

	// A due schedule is sent by the worker once.
	if _, err := pool.Exec(ctx, "UPDATE report_schedules SET next_run_at = now() - interval '1 minute' WHERE id = $1", created.ID); err != nil {
		t.Fatal(err)
	}
	e.srv.reportTick(ctx)
	e.srv.reportTick(ctx)
	e.srv.bgWG.Wait()
	var n int
	pool.QueryRow(ctx, "SELECT count(*) FROM mail_queue WHERE kind = 'report' AND status = 'queued'").Scan(&n)
	if n != 2 {
		t.Errorf("scheduled run queued %d messages, want 2", n)
	}
	got, _ := e.st.ReportScheduleByID(ctx, created.ID)
	if got.LastRunAt == nil || got.NextRunAt == nil || !got.NextRunAt.After(time.Now()) || got.LastError != "" {
		t.Errorf("after run: %+v", got)
	}
	if code := admin.do("DELETE", "/api/v1/reports/schedules/"+itoa(created.ID), nil, nil); code != 204 {
		t.Errorf("delete: %d", code)
	}
}

// newUserClient creates a local user with a role and signs it in.
func (e *env) newUserClient(admin *client, name, role string) *client {
	e.t.Helper()
	var created userWithPassword
	if code := admin.do("POST", "/api/v1/users", createUserReq{Username: name, Role: role, Email: name + "@example.com"}, &created); code != 201 {
		e.t.Fatalf("create user: %d", code)
	}
	c := e.client()
	c.login(name, created.OneTimePassword)
	if code := c.do("POST", "/api/v1/me/password", map[string]string{"current": created.OneTimePassword, "new": "another-secret-pass"}, nil); code != 200 {
		e.t.Fatalf("password change: %d", code)
	}
	return c
}

// configureMail saves a working mail configuration (the fake mailer delivers).
func (e *env) configureMail(admin *client) {
	e.t.Helper()
	cfg := mail.Config{Enabled: true, Host: "smtp.example.com", Port: 587, Security: "starttls", Auth: "none",
		FromAddress: "fw@example.com", FromName: "FW"}
	if code := admin.do("PUT", "/api/v1/settings/mail", cfg, nil); code != 200 {
		e.t.Fatalf("mail settings: %d", code)
	}
}
