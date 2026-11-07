package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rhisijmuiden/fleetwarden/internal/backup"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

func init() { backup.KDFParams.Memory, backup.KDFParams.Time, backup.KDFParams.Threads = 1024, 1, 1 }

func TestSchedule(t *testing.T) {
	loc := time.FixedZone("CET", 3600)
	sc := backupSchedule{IntervalHours: 6, StartTime: "02:30", FullEveryDays: 7, KeepFull: 4}
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, loc)
	if got := prevSlot(sc, now); !got.Equal(time.Date(2026, 11, 7, 8, 30, 0, 0, loc)) {
		t.Errorf("prev: %v", got)
	}
	if got := nextSlot(sc, now); !got.Equal(time.Date(2026, 11, 7, 14, 30, 0, 0, loc)) {
		t.Errorf("next: %v", got)
	}
	sc.IntervalHours = 24
	if got := prevSlot(sc, now); !got.Equal(time.Date(2026, 11, 7, 2, 30, 0, 0, loc)) {
		t.Errorf("daily prev: %v", got)
	}
	early := time.Date(2026, 11, 7, 1, 0, 0, 0, loc)
	if got := prevSlot(sc, early); !got.Equal(time.Date(2026, 11, 6, 2, 30, 0, 0, loc)) {
		t.Errorf("prev before first slot: %v", got)
	}
	if got := nextSlot(sc, time.Date(2026, 11, 7, 23, 0, 0, 0, loc)); !got.Equal(time.Date(2026, 11, 8, 2, 30, 0, 0, loc)) {
		t.Errorf("next tomorrow: %v", got)
	}
	if (backupSchedule{IntervalHours: 5, StartTime: "02:00", FullEveryDays: 7, KeepFull: 4}).valid() {
		t.Error("interval 5 accepted")
	}
}

func (c *client) waitBackup() backupProgress {
	c.e.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		var p backupProgress
		c.do("GET", "/api/v1/backup/progress", nil, &p)
		if !p.Running {
			return p
		}
		time.Sleep(50 * time.Millisecond)
	}
	c.e.t.Fatal("backup did not finish")
	return backupProgress{}
}

func (c *client) waitRestore(state string) restoreView {
	c.e.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	var v restoreView
	for time.Now().Before(deadline) {
		c.do("GET", "/api/v1/backup/restore", nil, &v)
		if v.State == state || v.State == "failed" {
			return v
		}
		time.Sleep(50 * time.Millisecond)
	}
	c.e.t.Fatalf("restore did not reach %s: %+v", state, v)
	return v
}

func TestBackupAndRestore(t *testing.T) {
	e := newEnv(t)
	c, _ := e.signedInAdmin()
	ctx := context.Background()

	// A backup needs a passphrase and a location.
	var ae apiErr
	if code := c.do("POST", "/api/v1/backup/run", map[string]string{}, &ae); code != 400 || ae.Error != "passphrase_required" {
		t.Fatalf("run without passphrase: %d %v", code, ae)
	}
	if code := c.do("PUT", "/api/v1/backup/passphrase", map[string]string{"new": "short"}, &ae); code != 400 || ae.Error != "passphrase_too_short" {
		t.Fatalf("short passphrase: %d %v", code, ae)
	}
	pass := "a long backup passphrase"
	if code := c.do("PUT", "/api/v1/backup/passphrase", map[string]string{"new": pass}, nil); code != 200 {
		t.Fatalf("set passphrase: %d", code)
	}
	if code := c.do("POST", "/api/v1/backup/run", map[string]string{}, &ae); code != 400 || ae.Error != "no_locations" {
		t.Fatalf("run without locations: %d %v", code, ae)
	}
	var test testResult
	if code := c.do("POST", "/api/v1/backup/locations/test", map[string]any{"name": "NAS", "type": "local", "path": "nas"}, &test); code != 200 || !test.OK || test.Repository != "none" {
		t.Fatalf("test location: %d %+v", code, test)
	}
	var loc locationView
	if code := c.do("POST", "/api/v1/backup/locations", map[string]any{"name": "NAS", "type": "local", "path": "nas", "enabled": true}, &loc); code != 201 {
		t.Fatalf("create location: %d", code)
	}
	if code := c.do("POST", "/api/v1/backup/locations", map[string]any{"name": "NAS", "type": "local", "path": "x", "enabled": true}, &ae); code != 409 {
		t.Fatalf("duplicate name: %d", code)
	}

	// Data files that must come back.
	os.MkdirAll(filepath.Join(e.cfg.DataDir, "uploads"), 0o750)
	msi := make([]byte, 3<<20) // incompressible, several chunks
	rand.Read(msi)
	os.WriteFile(filepath.Join(e.cfg.DataDir, "uploads", "agent.msi"), msi, 0o640)
	keyBefore, _ := os.ReadFile(e.cfg.SecretKeyPath())

	if code := c.do("POST", "/api/v1/backup/run", map[string]string{"kind": "auto"}, nil); code != 202 {
		t.Fatalf("run: %d", code)
	}
	p := c.waitBackup()
	if lp := p.Locations[loc.ID]; lp == nil || lp.State != "succeeded" || lp.Kind != "full" {
		t.Fatalf("first backup: %+v", lp)
	}
	if code := c.do("POST", "/api/v1/backup/run", map[string]string{"kind": "auto"}, nil); code != 202 {
		t.Fatalf("run 2: %d", code)
	}
	if lp := c.waitBackup().Locations[loc.ID]; lp.State != "succeeded" || lp.Kind != "incremental" {
		t.Fatalf("second backup: %+v", lp)
	}
	var runs []store.BackupRun
	c.do("GET", "/api/v1/backup/runs", nil, &runs)
	if len(runs) != 2 || runs[0].Kind != "incremental" || runs[1].Kind != "full" || runs[0].UploadedBytes*2 >= runs[1].UploadedBytes {
		t.Fatalf("runs: %+v", runs)
	}

	// Changes after the backup.
	if code := c.do("POST", "/api/v1/users", createUserReq{Username: "bob", Role: "technician", Email: "bob@example.com"}, nil); code != 201 {
		t.Fatalf("create user: %d", code)
	}
	os.Remove(filepath.Join(e.cfg.DataDir, "uploads", "agent.msi"))
	os.WriteFile(filepath.Join(e.cfg.DataDir, "added-later.txt"), []byte("x"), 0o600)

	var snaps []backup.Snapshot
	if code := c.do("POST", "/api/v1/backup/snapshots", map[string]any{"locationId": loc.ID}, &snaps); code != 200 || len(snaps) != 2 {
		t.Fatalf("snapshots: %d %+v", code, snaps)
	}
	var details manifestView
	if code := c.do("POST", "/api/v1/backup/snapshots/details", map[string]any{"locationId": loc.ID, "chain": snaps[0].Chain, "snapshot": snaps[0].ID}, &details); code != 200 || !details.Restorable || details.Size == 0 {
		t.Fatalf("details: %d %+v", code, details)
	}
	// Browsing like a new server: location and passphrase from the form.
	form := map[string]any{"type": "local", "path": "nas"}
	if code := c.do("POST", "/api/v1/backup/snapshots", map[string]any{"location": form, "passphrase": "wrong passphrase"}, &ae); code != 400 || ae.Error != "wrong_passphrase" {
		t.Fatalf("wrong passphrase: %d %v", code, ae)
	}
	if code := c.do("POST", "/api/v1/backup/snapshots", map[string]any{"location": map[string]any{"type": "local", "path": "empty"}, "passphrase": pass}, &ae); code != 404 || ae.Error != "no_backups" {
		t.Fatalf("empty location: %d %v", code, ae)
	}

	// Restore the incremental (newest) snapshot.
	if code := c.do("POST", "/api/v1/backup/restore", map[string]any{"locationId": loc.ID, "chain": snaps[0].Chain, "snapshot": snaps[0].ID}, &ae); code != 400 || ae.Error != "confirm_required" {
		t.Fatalf("restore without confirm: %d", code)
	}
	if code := c.do("POST", "/api/v1/backup/restore", map[string]any{"location": form, "passphrase": pass, "chain": snaps[0].Chain,
		"snapshot": snaps[0].ID, "confirm": "RESTORE"}, nil); code != 202 {
		t.Fatalf("restore: %d", code)
	}
	if v := c.waitRestore("requested"); v.State != "requested" || !v.Pending {
		t.Fatalf("restore prepare: %+v", v)
	}
	if code := c.do("POST", "/api/v1/backup/run", map[string]string{}, &ae); code != 409 {
		t.Fatalf("backup during a pending restore: %d", code)
	}
	req, err := ReadRestoreRequest(e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := ApplyRestore(ctx, e.cfg, req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "Restored") {
		t.Errorf("message %q", msg)
	}
	WriteRestoreStatus(e.cfg.UpdateStatusDir, RestoreStatus{ID: "r1", State: "completed", Message: msg, Snapshot: req.Snapshot, RequestedBy: "admin"})

	// The server restarts after a restore; use a fresh connection pool.
	pool, err := pgxpool.New(ctx, os.Getenv("FW_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	st := store.New(pool)
	if _, err := st.UserByUsername(ctx, "bob"); err != store.ErrNotFound {
		t.Errorf("user created after the backup still exists: %v", err)
	}
	if _, err := st.UserByUsername(ctx, "admin"); err != nil {
		t.Errorf("admin missing after restore: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(e.cfg.DataDir, "uploads", "agent.msi")); !bytes.Equal(got, msi) {
		t.Error("data file not restored")
	}
	if _, err := os.Stat(filepath.Join(e.cfg.DataDir, "added-later.txt")); err == nil {
		t.Error("file added after the backup survived the restore")
	}
	if got, _ := os.ReadFile(e.cfg.SecretKeyPath()); !bytes.Equal(got, keyBefore) {
		t.Error("secret key differs")
	}
	if _, err := os.Stat(filepath.Join(e.cfg.DataDir, "server.env.from-backup")); err != nil {
		t.Error("configuration copy missing")
	}
	locs, _ := st.ListBackupLocations(ctx)
	if len(locs) != 1 {
		t.Errorf("locations after restore: %d", len(locs))
	}
	// The restored server reports the result once.
	srv := New(e.cfg, Deps{Store: st, DB: st, Secrets: e.srv.box, Mailer: e.mailer})
	srv.reportRestoreOutcome(ctx)
	srv.reportRestoreOutcome(ctx)
	audit, _ := st.ListAudit(ctx, store.AuditQuery{Search: "backup.restore_result"})
	if len(audit) != 1 || !audit[0].Success {
		t.Errorf("restore result audit: %+v", audit)
	}
}

func TestBackupFailureMailAndPassphraseChange(t *testing.T) {
	e := newEnv(t)
	c, _ := e.signedInAdmin()
	ctx := context.Background()
	c.do("PATCH", "/api/v1/users/1", map[string]any{"email": "admin@example.com", "displayName": "Admin", "role": "administrator"}, nil)
	old := "first passphrase 123"
	c.do("PUT", "/api/v1/backup/passphrase", map[string]string{"new": old}, nil)
	var loc locationView
	c.do("POST", "/api/v1/backup/locations", map[string]any{"name": "Local", "type": "local", "path": "l", "enabled": true}, &loc)
	var broken locationView
	if code := c.do("POST", "/api/v1/backup/locations", map[string]any{"name": "Broken", "type": "sftp", "host": "127.0.0.1", "port": 1,
		"username": "x", "password": "y", "enabled": true}, &broken); code != 201 {
		t.Fatalf("create sftp location: %d", code)
	}
	c.do("POST", "/api/v1/backup/run", map[string]string{"kind": "full"}, nil)
	p := c.waitBackup()
	if p.Locations[loc.ID].State != "succeeded" || p.Locations[broken.ID].State != "failed" {
		t.Fatalf("progress: %+v %+v", p.Locations[loc.ID], p.Locations[broken.ID])
	}
	mails, _ := e.st.ListMail(ctx, 10)
	found := false
	for _, m := range mails {
		if m.Kind == "backup_failed" && m.To == "admin@example.com" {
			found = true
		}
	}
	if !found {
		t.Errorf("no failure mail: %+v", mails)
	}
	var locs []locationView
	c.do("GET", "/api/v1/backup/locations", nil, &locs)
	if len(locs) != 2 || locs[0].LastRun == nil || locs[0].LastRun.Status != "failed" || locs[0].PasswordSet != true {
		t.Fatalf("locations: %+v", locs)
	}
	c.do("DELETE", "/api/v1/backup/locations/"+itoa(broken.ID), nil, nil)

	// Changing the passphrase needs the current one and re-keys existing backups.
	var ae apiErr
	if code := c.do("PUT", "/api/v1/backup/passphrase", map[string]string{"current": "nope", "new": "second passphrase 456"}, &ae); code != 400 || ae.Error != "wrong_passphrase" {
		t.Fatalf("wrong current: %d %v", code, ae)
	}
	if code := c.do("PUT", "/api/v1/backup/passphrase", map[string]string{"current": old, "new": "second passphrase 456"}, nil); code != 200 {
		t.Fatalf("change: %d", code)
	}
	var snaps []backup.Snapshot
	if code := c.do("POST", "/api/v1/backup/snapshots", map[string]any{"locationId": loc.ID}, &snaps); code != 200 || len(snaps) != 1 {
		t.Fatalf("snapshots after change: %d %v", code, snaps)
	}
	if code := c.do("POST", "/api/v1/backup/snapshots", map[string]any{"location": map[string]any{"type": "local", "path": "l"}, "passphrase": old}, &ae); code != 400 {
		t.Fatalf("old passphrase still opens the backups: %d", code)
	}

	// Scheduled backups: enabling does not run a slot that already passed.
	sched := map[string]any{"enabled": true, "intervalHours": 1, "startTime": "00:00", "fullEveryDays": 7, "keepFull": 2}
	var cv backupConfigView
	if code := c.do("PUT", "/api/v1/backup/schedule", sched, &cv); code != 200 || cv.NextRun == nil {
		t.Fatalf("schedule: %d %+v", code, cv)
	}
	e.srv.backupTick(ctx)
	if p := c.waitBackup(); p.Trigger == "schedule" {
		t.Fatal("a past slot ran right after enabling the schedule")
	}
	e.srv.now = func() time.Time { return time.Now().Add(time.Hour) }
	e.srv.backupTick(ctx)
	if p := c.waitBackup(); p.Trigger != "schedule" || p.Locations[loc.ID].State != "succeeded" || p.Locations[loc.ID].Kind != "incremental" {
		t.Fatalf("scheduled run: %+v", p)
	}
	e.srv.backupTick(ctx) // same slot: nothing new
	var runs []store.BackupRun
	c.do("GET", "/api/v1/backup/runs", nil, &runs)
	if len(runs) != 3 {
		t.Fatalf("runs: %d", len(runs))
	}

	// Technicians cannot reach backups.
	var created userWithPassword
	c.do("POST", "/api/v1/users", createUserReq{Username: "tech", Role: "technician"}, &created)
	tc := e.client()
	tc.login("tech", created.OneTimePassword)
	tc.do("POST", "/api/v1/me/password", map[string]string{"current": created.OneTimePassword, "new": "tech-password-123"}, nil)
	if code := tc.do("GET", "/api/v1/backup/locations", nil, nil); code != 403 {
		t.Errorf("technician: %d", code)
	}
}
