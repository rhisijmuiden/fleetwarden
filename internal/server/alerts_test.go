package server

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/mail"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

func TestAlerts(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("FW_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}

	// The two rules from the migration.
	var rules struct{ Rules []store.AlertRule }
	if code := admin.do("GET", "/api/v1/alerts/rules", nil, &rules); code != 200 || len(rules.Rules) != 3 {
		t.Fatalf("rules: %d %+v", code, rules)
	}
	var disk, offline store.AlertRule
	for _, r := range rules.Rules {
		switch r.Kind {
		case "disk":
			disk = r
		case "offline":
			offline = r
		}
	}
	if offline.Threshold != 15 || len(offline.DeviceKinds) != 2 || disk.Threshold != 10 {
		t.Fatalf("default rules: %+v %+v", offline, disk)
	}
	offline.Recipients, offline.Language = []string{"ops@example.com"}, "nl"
	if code := admin.do("PUT", "/api/v1/alerts/rules/"+itoa(offline.ID), offline, &offline); code != 200 || offline.Recipients[0] != "ops@example.com" {
		t.Fatalf("update rule: %d", code)
	}
	disk.Recipients = []string{"ops@example.com", " ops@example.com "}
	if code := admin.do("PUT", "/api/v1/alerts/rules/"+itoa(disk.ID), disk, &disk); code != 200 || len(disk.Recipients) != 1 {
		t.Fatalf("update disk rule: %d %+v", code, disk)
	}

	// Validation.
	bad := []store.AlertRule{
		{Name: "", Kind: "disk", Threshold: 10},
		{Name: "x", Kind: "cpu", Threshold: 10},
		{Name: "x", Kind: "offline", Threshold: 1},
		{Name: "x", Kind: "disk", Threshold: 95},
		{Name: "x", Kind: "disk", Threshold: 10, DeviceKinds: []string{"phone"}},
		{Name: "x", Kind: "disk", Threshold: 10, Recipients: []string{"not-an-address"}},
		{Name: "x", Kind: "disk", Threshold: 10, GroupIDs: []int64{999}},
	}
	for i, r := range bad {
		if code := admin.do("POST", "/api/v1/alerts/rules", r, nil); code != 400 {
			t.Errorf("bad rule %d: %d", i, code)
		}
	}
	if code := admin.do("POST", "/api/v1/alerts/rules", store.AlertRule{Name: "low disk space", Kind: "disk", Threshold: 5}, nil); code != 409 {
		t.Errorf("duplicate name: %d", code)
	}
	var extra store.AlertRule
	if code := admin.do("POST", "/api/v1/alerts/rules", store.AlertRule{Name: "Workstations", Kind: "offline", Threshold: 60,
		DeviceKinds: []string{"workstation"}}, &extra); code != 201 || extra.Enabled {
		t.Fatalf("create rule: %d %+v", code, extra) // disabled unless asked
	}

	// Permissions: read-only users see alerts but not the rules; a
	// technician may manage them.
	ro := e.newUserClient(admin, "reader", "readonly")
	if code := ro.do("GET", "/api/v1/alerts", nil, nil); code != 200 {
		t.Errorf("read-only alerts: %d", code)
	}
	if code := ro.do("GET", "/api/v1/alerts/rules", nil, nil); code != 403 {
		t.Errorf("read-only rules: %d", code)
	}
	tech := e.newUserClient(admin, "tech", "technician")
	if code := tech.do("GET", "/api/v1/alerts/rules", nil, nil); code != 200 {
		t.Errorf("technician rules: %d", code)
	}

	gb := int64(1 << 30)
	okDisk := []agentproto.Volume{{Mount: "C:", SizeBytes: 100 * gb, FreeBytes: 60 * gb}}
	srv1 := seedDevice(t, e.st, 1, "SRV-1", "windows", okDisk, nil, nil)
	srv2 := seedDevice(t, e.st, 1, "SRV-2", "linux", []agentproto.Volume{{Mount: "/", SizeBytes: 50 * gb, FreeBytes: 2 * gb},
		{Mount: "/boot", SizeBytes: 1 * gb, FreeBytes: 0}}, nil, nil)
	pc := seedDevice(t, e.st, 1, "PC-1", "windows", okDisk, nil, nil)
	exec("UPDATE devices SET kind = 'server' WHERE id::text = ANY($1)", []string{srv1, srv2})
	exec("UPDATE devices SET last_seen_at = now() - interval '30 minutes' WHERE id::text = ANY($1)", []string{srv1, pc})

	// Right after a restart nobody is offline yet.
	e.srv.workersStarted = time.Now()
	e.srv.alertTick(ctx)
	var list struct{ Alerts []alertView }
	admin.do("GET", "/api/v1/alerts", nil, &list)
	if len(list.Alerts) != 1 || list.Alerts[0].Kind != "disk" || list.Alerts[0].Subject != "/" {
		t.Fatalf("after restart: %+v", list.Alerts)
	}
	e.srv.workersStarted = time.Time{}

	e.configureMail(admin)
	e.srv.alertTick(ctx)
	e.srv.alertTick(ctx) // nothing new the second time
	admin.do("GET", "/api/v1/alerts", nil, &list)
	if len(list.Alerts) != 2 {
		t.Fatalf("alerts: %+v", list.Alerts)
	}
	var offAlert alertView
	for _, a := range list.Alerts {
		if a.Kind == "offline" {
			offAlert = a
		}
	}
	if offAlert.DeviceID != srv1 || !strings.Contains(offAlert.Text, "No contact for 30 min") {
		t.Errorf("offline alert: %+v", offAlert)
	}
	var count map[string]int
	ro.do("GET", "/api/v1/alerts/count", nil, &count)
	if count["open"] != 2 {
		t.Errorf("count: %v", count)
	}

	// Only the offline alert is mailed: the disk alert opened before the
	// mail server was set up.
	e.srv.ProcessMailQueue(ctx)
	e.mailer.mu.Lock()
	sent := append([]mail.Message(nil), e.mailer.sent...)
	e.mailer.sent = nil
	e.mailer.mu.Unlock()
	if len(sent) != 1 || !strings.Contains(sent[0].Subject, "melding") || !strings.Contains(sent[0].Subject, "SRV-1") ||
		!strings.Contains(sent[0].Text, "Geen contact gedurende 30 min") {
		t.Fatalf("alert mails: %+v", sent)
	}

	// Acknowledge.
	if code := ro.do("POST", "/api/v1/alerts/"+itoa(offAlert.ID)+"/acknowledge", nil, nil); code != 403 {
		t.Errorf("read-only acknowledge: %d", code)
	}
	var acked store.Alert
	if code := tech.do("POST", "/api/v1/alerts/"+itoa(offAlert.ID)+"/acknowledge", nil, &acked); code != 200 || acked.AcknowledgedBy != "tech" {
		t.Errorf("acknowledge: %d %+v", code, acked)
	}
	admin.do("GET", "/api/v1/alerts/count", nil, &count)
	if count["open"] != 1 {
		t.Errorf("count after acknowledge: %v", count)
	}

	// The heartbeat brings fresh volumes: just above the threshold the disk
	// alert stays open, with room to spare it closes.
	if err := e.srv.saveVolumes(ctx, srv2, []agentproto.Volume{{Mount: "/", SizeBytes: 100 * gb, FreeBytes: 10 * gb}}); err != nil {
		t.Fatal(err)
	}
	e.srv.alertTick(ctx)
	admin.do("GET", "/api/v1/alerts", nil, &list)
	if len(list.Alerts) != 2 {
		t.Errorf("hysteresis: %+v", list.Alerts)
	}
	if err := e.srv.saveVolumes(ctx, srv2, []agentproto.Volume{{Mount: "/", SizeBytes: 100 * gb, FreeBytes: 30 * gb}}); err != nil {
		t.Fatal(err)
	}
	exec("UPDATE devices SET last_seen_at = now() WHERE id::text = $1", srv1)
	e.srv.alertTick(ctx)
	admin.do("GET", "/api/v1/alerts", nil, &list)
	if len(list.Alerts) != 0 {
		t.Errorf("open after recovery: %+v", list.Alerts)
	}
	admin.do("GET", "/api/v1/alerts?state=all", nil, &list)
	if len(list.Alerts) != 2 || list.Alerts[0].ResolvedAt == nil {
		t.Errorf("history: %+v", list.Alerts)
	}
	e.srv.ProcessMailQueue(ctx)
	e.mailer.mu.Lock()
	sent = append([]mail.Message(nil), e.mailer.sent...)
	e.mailer.mu.Unlock()
	if len(sent) != 2 {
		t.Fatalf("resolved mails: %d", len(sent))
	}
	for _, m := range sent {
		if !strings.Contains(m.Subject, "opgelost") && !strings.Contains(m.Subject, "resolved") {
			t.Errorf("resolved mail: %s", m.Subject)
		}
		if strings.Contains(m.Subject, "SRV-2") && !strings.Contains(m.Text, "30% free again") {
			t.Errorf("disk resolved text: %s", m.Text)
		}
	}

	// The report uses the heartbeat volumes too.
	var rep complianceReport
	admin.do("GET", "/api/v1/reports/compliance", nil, &rep)
	for _, d := range rep.List {
		if d.ID == srv2 && len(d.LowDisk) > 0 {
			t.Errorf("report still sees the old volumes: %+v", d)
		}
	}

	// A disabled rule closes its alerts without mail.
	exec("UPDATE devices SET last_seen_at = now() - interval '1 hour' WHERE id::text = $1", srv1)
	e.srv.alertTick(ctx)
	offline.Enabled = false
	if code := admin.do("PUT", "/api/v1/alerts/rules/"+itoa(offline.ID), offline, nil); code != 200 {
		t.Fatalf("disable: %d", code)
	}
	e.srv.alertTick(ctx)
	admin.do("GET", "/api/v1/alerts", nil, &list)
	if len(list.Alerts) != 0 {
		t.Errorf("open after disabling: %+v", list.Alerts)
	}
	if code := admin.do("DELETE", "/api/v1/alerts/rules/"+itoa(extra.ID), nil, nil); code != 204 {
		t.Errorf("delete rule: %d", code)
	}
}
