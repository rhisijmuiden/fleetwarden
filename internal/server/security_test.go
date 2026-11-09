package server

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

const (
	testKeyID1 = "{1F0E5C7A-3B2D-4C1E-9A8B-7D6C5B4A3F21}"
	testKeyPW1 = "123456-234567-345678-456789-567890-678901-789012-890123"
	testKeyID2 = "{AB12CD34-0000-4000-8000-0000000000AA}"
	testKeyPW2 = "111111-222222-333333-444444-555555-666666-777777-888888"
)

// fakeSecurity is a security collector whose report a test changes, and an
// action runner that records what it ran.
type fakeSecurity struct {
	mu   sync.Mutex
	rep  agentproto.SecurityReport
	runs []agentproto.SecurityAction
}

func (f *fakeSecurity) collect(context.Context) (agentproto.SecurityReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, _ := json.Marshal(f.rep)
	var out agentproto.SecurityReport
	_ = json.Unmarshal(raw, &out)
	return out, nil
}

func (f *fakeSecurity) run(_ context.Context, a agentproto.SecurityAction) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs = append(f.runs, a)
	if a.Action == agentproto.SecurityRotateKey {
		// BitLocker replaces the recovery password.
		f.rep.Volumes[0].Keys = []agentproto.RecoveryKey{{ID: testKeyID2, Password: testKeyPW2}}
		return "New recovery key " + testKeyID2, nil
	}
	if f.rep.Defender != nil {
		f.rep.Defender.Threats = nil // the scan cleaned it up
	}
	return "Quick scan finished", nil
}

func (f *fakeSecurity) ran() []agentproto.SecurityAction {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]agentproto.SecurityAction(nil), f.runs...)
}

func laptopSecurity() agentproto.SecurityReport {
	on := true
	now := time.Now().UTC()
	return agentproto.SecurityReport{
		Volumes: []agentproto.SecureVolume{
			{Mount: "C:", System: true, Method: "bitlocker", Protection: "on", Status: "encrypted", Percent: 100, Cipher: "XtsAes128",
				Protectors: []string{"Tpm", "RecoveryPassword"}, Keys: []agentproto.RecoveryKey{{ID: testKeyID1, Password: testKeyPW1},
					{ID: "{bogus}", Password: "not-a-key"}}},
			{Mount: "D:", Status: "decrypted", Protection: "off"},
		},
		Antivirus: []agentproto.Antivirus{{Name: "Windows Defender", Enabled: true, UpToDate: true, Defender: true}},
		Defender: &agentproto.DefenderStatus{Mode: "Normal", ServiceEnabled: true, AntivirusEnabled: true, RealTime: true, SignatureVersion: "1.421.95.0",
			SignatureUpdatedAt: &now, Threats: []agentproto.Threat{{Name: "Trojan:Win32/Wacatac.B!ml", Severity: "Severe", DetectedAt: now.Add(-time.Hour),
				Active: true, Action: "Detected"}}},
		Firewall: []agentproto.FirewallState{{Name: "Domain", Enabled: true, Inbound: "block"}, {Name: "Private", Enabled: true, Inbound: "block", Active: true},
			{Name: "Public", Enabled: false, Inbound: "allow"}},
		Admins:     []agentproto.LocalAdmin{{Name: `PC-SEC\Administrator`, Kind: "user", Builtin: true}, {Name: `CORP\jan`, Kind: "user"}},
		SecureBoot: &on,
		TPM:        &agentproto.TPMStatus{Present: true, Ready: true, Version: "2.0"},
	}
}

func TestSecurityStatus(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()
	_, info := e.createToken(admin, 1)
	a := newTestAgent(t, e, info.Token, "PC-SEC", false)
	fs := &fakeSecurity{rep: laptopSecurity()}
	a.Security, a.SecurityRun = fs.collect, fs.run
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	go a.Run(ctx) //nolint:errcheck
	d := e.onlineDevice(admin, "PC-SEC")

	var view deviceSecurityView
	waitFor(t, "security report", func() bool {
		admin.do("GET", "/api/v1/devices/"+d.ID+"/security", nil, &view)
		return view.Reported
	})
	sm := view.Summary
	if sm.Encryption != "partial" || sm.Antivirus != "ok" || sm.Firewall != "partial" || sm.Threats != 1 || sm.Admins != 2 ||
		sm.SecureBoot == nil || !*sm.SecureBoot || sm.TPM == nil || !*sm.TPM {
		t.Errorf("summary: %+v", sm)
	}
	if strings.Contains(string(view.Report), testKeyPW1) || strings.Contains(string(view.Report), "password") {
		t.Fatalf("the report holds the recovery password: %s", view.Report)
	}
	if !view.CanKeys || len(view.Keys) != 1 || view.Keys[0].KeyID != testKeyID1 || view.Keys[0].Volume != "C:" || view.Keys[0].Hostname != "PC-SEC" ||
		view.Keys[0].Serial != "ABC123" {
		t.Fatalf("keys: %+v", view.Keys)
	}
	// Sealed at rest.
	stored, err := e.st.RecoveryKeyByID(ctx, view.Keys[0].ID)
	if err != nil || stored.Password() == testKeyPW1 || stored.Password() == "" {
		t.Fatalf("stored key: %v %q", err, stored.Password())
	}

	var sum store.SecurityStats
	if code := admin.do("GET", "/api/v1/security/summary", nil, &sum); code != 200 || sum.Devices != 1 || sum.Reported != 1 ||
		sum.Encryption["partial"] != 1 || sum.Threats != 1 || sum.Keys != 1 {
		t.Errorf("summary: %d %+v", code, sum)
	}
	for filter, want := range map[string]int{"": 1, "threats": 1, "firewall": 1, "unencrypted": 1, "antivirus": 0, "secureboot": 0, "unreported": 0} {
		var rows []store.SecurityRow
		if code := admin.do("GET", "/api/v1/security/devices?filter="+filter, nil, &rows); code != 200 || len(rows) != want {
			t.Errorf("filter %q: %d %d rows", filter, code, len(rows))
		}
	}
	if code := admin.do("GET", "/api/v1/security/devices?filter=bogus", nil, nil); code != 400 {
		t.Errorf("bad filter: %d", code)
	}

	// Looking a key up by what the recovery screen shows.
	var keys []store.RecoveryKey
	if code := admin.do("GET", "/api/v1/security/keys?search=1f0e5c7a", nil, &keys); code != 200 || len(keys) != 1 {
		t.Fatalf("search by key ID: %d %+v", code, keys)
	}
	if code := admin.do("GET", "/api/v1/security/keys?search=pc-sec", nil, &keys); code != 200 || len(keys) != 1 {
		t.Fatalf("search by host: %d %+v", code, keys)
	}
	var shown map[string]string
	if code := admin.do("POST", "/api/v1/security/keys/"+itoa(keys[0].ID)+"/reveal", map[string]string{"reason": "user locked out"}, &shown); code != 200 ||
		shown["password"] != testKeyPW1 {
		t.Fatalf("reveal: %d %+v", code, shown)
	}
	entries, _ := e.st.ListAudit(ctx, store.AuditQuery{Search: "security.key.reveal"})
	if len(entries) != 1 || entries[0].Target != "PC-SEC" || entries[0].Actor != "admin" {
		t.Errorf("audit: %+v", entries)
	}
	if code := admin.do("POST", "/api/v1/security/keys/999999/reveal", nil, nil); code != 404 {
		t.Errorf("unknown key: %d", code)
	}

	// A read-only user sees the status, not the keys.
	var created userWithPassword
	if code := admin.do("POST", "/api/v1/users", createUserReq{Username: "viewer", Role: "readonly"}, &created); code != 201 {
		t.Fatalf("create viewer: %d", code)
	}
	viewer := e.client()
	viewer.login("viewer", created.OneTimePassword)
	if code := viewer.do("POST", "/api/v1/me/password", map[string]string{"current": created.OneTimePassword, "new": "another-secret-pass"}, nil); code != 200 {
		t.Fatalf("viewer password: %d", code)
	}
	var vv deviceSecurityView
	if code := viewer.do("GET", "/api/v1/devices/"+d.ID+"/security", nil, &vv); code != 200 || vv.CanKeys || len(vv.Keys) != 0 || !vv.Reported {
		t.Errorf("viewer device security: %d %+v", code, vv)
	}
	if code := viewer.do("GET", "/api/v1/security/keys", nil, nil); code != 403 {
		t.Errorf("viewer keys: %d", code)
	}
	if code := viewer.do("POST", "/api/v1/security/keys/"+itoa(keys[0].ID)+"/reveal", nil, nil); code != 403 {
		t.Errorf("viewer reveal: %d", code)
	}
	if code := viewer.do("POST", "/api/v1/devices/"+d.ID+"/security/actions", map[string]string{"action": "defender.quickscan"}, nil); code != 403 {
		t.Errorf("viewer action: %d", code)
	}

	// The alert rule from the migration opens an alert for the threat.
	e.srv.alertTick(ctx)
	var alerts struct{ Alerts []alertView }
	admin.do("GET", "/api/v1/alerts?state=open", nil, &alerts)
	threat := false
	for _, al := range alerts.Alerts {
		threat = threat || (al.Kind == "threat" && al.DeviceID == d.ID && strings.Contains(al.Text, "Wacatac"))
	}
	if !threat {
		t.Errorf("no threat alert: %+v", alerts.Alerts)
	}

	// Actions are for Windows devices.
	if code := admin.do("POST", "/api/v1/devices/"+d.ID+"/security/actions", map[string]string{"action": "defender.quickscan"}, nil); code != 400 {
		t.Errorf("action on Linux: %d", code)
	}
	setPlatform(t, d.ID, "windows")
	if code := admin.do("POST", "/api/v1/devices/"+d.ID+"/security/actions", map[string]string{"action": "format.c"}, nil); code != 400 {
		t.Errorf("unknown action: %d", code)
	}
	if code := admin.do("POST", "/api/v1/devices/"+d.ID+"/security/actions", map[string]string{"action": "bitlocker.rotate", "mount": "C:\\"}, nil); code != 400 {
		t.Errorf("bad volume: %d", code)
	}
	var act store.SecurityAction
	if code := admin.do("POST", "/api/v1/devices/"+d.ID+"/security/actions", map[string]string{"action": "defender.quickscan"}, &act); code != 202 {
		t.Fatalf("quick scan: %d", code)
	}
	waitFor(t, "scan done", func() bool {
		admin.do("GET", "/api/v1/devices/"+d.ID+"/security", nil, &view)
		return len(view.Actions) == 1 && view.Actions[0].State == "completed" && view.Summary.Threats == 0
	})
	if view.Actions[0].Message != "Quick scan finished" || view.Actions[0].RequestedBy != "admin" || view.Actions[0].FinishedAt == nil {
		t.Errorf("action: %+v", view.Actions[0])
	}
	e.srv.alertTick(ctx)
	admin.do("GET", "/api/v1/alerts?state=open", nil, &alerts)
	for _, al := range alerts.Alerts {
		if al.Kind == "threat" {
			t.Errorf("threat alert still open: %+v", al)
		}
	}

	// Rotating the key: the new one is escrowed, the old one stays findable.
	if code := admin.do("POST", "/api/v1/devices/"+d.ID+"/security/actions", map[string]string{"action": "bitlocker.rotate", "mount": "c:"}, &act); code != 202 ||
		act.Mount != "C:" {
		t.Fatalf("rotate: %d %+v", code, act)
	}
	waitFor(t, "rotation", func() bool {
		admin.do("GET", "/api/v1/devices/"+d.ID+"/security", nil, &view)
		return len(view.Keys) == 2 && view.Keys[0].KeyID == testKeyID2 && view.Keys[1].RemovedAt != nil
	})
	if r := fs.ran(); len(r) != 2 || r[1].Mount != "C:" {
		t.Errorf("ran: %+v", r)
	}
	admin.do("GET", "/api/v1/security/keys?search="+strings.Trim(testKeyID1, "{}")[:8], nil, &keys)
	if len(keys) != 1 || keys[0].RemovedAt == nil {
		t.Errorf("old key: %+v", keys)
	}

	// Keys outlive their device.
	if code := admin.do("DELETE", "/api/v1/devices/"+d.ID, nil, nil); code != 204 && code != 200 {
		t.Fatalf("delete device: %d", code)
	}
	admin.do("GET", "/api/v1/security/keys?search=pc-sec", nil, &keys)
	if len(keys) != 2 || keys[0].DeviceID != nil {
		t.Errorf("keys after delete: %+v", keys)
	}
	if code := admin.do("POST", "/api/v1/security/keys/"+itoa(keys[0].ID)+"/reveal", nil, &shown); code != 200 || shown["password"] != testKeyPW2 {
		t.Errorf("reveal after delete: %d %+v", code, shown)
	}
}

func TestSummarizeSecurity(t *testing.T) {
	enc := agentproto.SecureVolume{Mount: "C:", System: true, Method: "bitlocker", Protection: "on", Status: "encrypted"}
	cases := []struct {
		name     string
		platform string
		rep      agentproto.SecurityReport
		want     store.SecuritySummary
	}{
		{"nothing", "windows", agentproto.SecurityReport{}, store.SecuritySummary{Encryption: "unknown", Antivirus: "unknown", Firewall: "unknown"}},
		{"encrypted", "windows", agentproto.SecurityReport{Volumes: []agentproto.SecureVolume{enc},
			Antivirus: []agentproto.Antivirus{{Name: "A", Enabled: false}, {Name: "B", Enabled: true, UpToDate: false}},
			Firewall:  []agentproto.FirewallState{{Name: "Domain"}, {Name: "Public"}}},
			store.SecuritySummary{Encryption: "encrypted", Antivirus: "outdated", Firewall: "off"}},
		{"suspended", "windows", agentproto.SecurityReport{Volumes: []agentproto.SecureVolume{{Mount: "C:", System: true, Method: "bitlocker",
			Protection: "suspended", Status: "encrypted"}}, Defender: &agentproto.DefenderStatus{}},
			store.SecuritySummary{Encryption: "suspended", Antivirus: "none", Firewall: "unknown"}},
		{"encrypting", "windows", agentproto.SecurityReport{Volumes: []agentproto.SecureVolume{{Mount: "C:", System: true, Method: "bitlocker",
			Protection: "off", Status: "encrypting", Percent: 40}}}, store.SecuritySummary{Encryption: "encrypting", Antivirus: "unknown", Firewall: "unknown"}},
		{"linux luks", "linux", agentproto.SecurityReport{Volumes: []agentproto.SecureVolume{{Mount: "/", System: true, Method: "luks", Protection: "on",
			Status: "encrypted"}, {Mount: "/boot"}, {Mount: "/srv"}}, Firewall: []agentproto.FirewallState{{Name: "ufw", Enabled: true}}},
			store.SecuritySummary{Encryption: "encrypted", Antivirus: "none", Firewall: "on"}},
		{"linux home", "linux", agentproto.SecurityReport{Volumes: []agentproto.SecureVolume{{Mount: "/", System: true, Method: "luks", Protection: "on",
			Status: "encrypted"}, {Mount: "/home"}}}, store.SecuritySummary{Encryption: "partial", Antivirus: "none", Firewall: "unknown"}},
		{"plain", "linux", agentproto.SecurityReport{Volumes: []agentproto.SecureVolume{{Mount: "/", System: true}}},
			store.SecuritySummary{Encryption: "none", Antivirus: "none", Firewall: "unknown"}},
	}
	for _, c := range cases {
		got := summarizeSecurity(c.platform, c.rep)
		got.SecureBoot, got.TPM = nil, nil
		if got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestSecurityAlertRules(t *testing.T) {
	dev := func(platform string, sm store.SecuritySummary) store.SecurityAlertDevice {
		return store.SecurityAlertDevice{Platform: platform, Summary: sm}
	}
	if _, bad := securityProblem("antivirus", dev("linux", store.SecuritySummary{Antivirus: "none"})); bad {
		t.Error("Linux without antivirus alerts")
	}
	if d, bad := securityProblem("antivirus", dev("windows", store.SecuritySummary{Antivirus: "none"})); !bad || d.Text != "security.av.none" {
		t.Errorf("Windows without antivirus: %v %+v", bad, d)
	}
	if _, bad := securityProblem("firewall", dev("windows", store.SecuritySummary{Firewall: "on"})); bad {
		t.Error("firewall on alerts")
	}
	if _, bad := securityProblem("encryption", dev("windows", store.SecuritySummary{Encryption: "encrypting"})); bad {
		t.Error("encrypting alerts")
	}
	if d, bad := securityProblem("threat", dev("windows", store.SecuritySummary{Threats: 2})); !bad || len(d.Threats) != 1 {
		t.Errorf("threat without names: %+v", d)
	}
}
