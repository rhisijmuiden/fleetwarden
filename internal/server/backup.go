package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/rhisijmuiden/fleetwarden/internal/backup"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

const keyBackup = "backup"

// backupSchedule says when scheduled backups run. Backups run every
// IntervalHours, at StartTime and at the times that follow from it. A new
// full backup (and so a new chain) starts every FullEveryDays; in between
// backups are incremental. KeepFull chains are kept.
type backupSchedule struct {
	Enabled       bool   `json:"enabled"`
	IntervalHours int    `json:"intervalHours"`
	StartTime     string `json:"startTime"` // HH:MM, server time zone
	FullEveryDays int    `json:"fullEveryDays"`
	KeepFull      int    `json:"keepFull"`
}

type backupSettings struct {
	Passphrase          string         `json:"passphrase"` // sealed
	InstanceID          string         `json:"instanceId"`
	Schedule            backupSchedule `json:"schedule"`
	LastSlot            time.Time      `json:"lastSlot"`
	LastRestoreReported string         `json:"lastRestoreReported"`
}

var validIntervals = map[int]bool{1: true, 2: true, 3: true, 4: true, 6: true, 8: true, 12: true, 24: true}

var hhmm = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

func defaultSchedule() backupSchedule {
	return backupSchedule{IntervalHours: 24, StartTime: "02:00", FullEveryDays: 7, KeepFull: 4}
}

func (s backupSchedule) valid() bool {
	return validIntervals[s.IntervalHours] && hhmm.MatchString(s.StartTime) &&
		s.FullEveryDays >= 1 && s.FullEveryDays <= 90 && s.KeepFull >= 1 && s.KeepFull <= 52
}

// loadBackupSettings returns the settings, creating the instance ID on first use.
func (s *Server) loadBackupSettings(ctx context.Context) (backupSettings, error) {
	var b backupSettings
	err := s.st.GetSetting(ctx, keyBackup, &b)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return b, err
	}
	if !b.Schedule.valid() {
		enabled := b.Schedule.Enabled
		b.Schedule = defaultSchedule()
		b.Schedule.Enabled = enabled && err == nil
	}
	if b.InstanceID == "" {
		var raw [4]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return b, err
		}
		b.InstanceID = hex.EncodeToString(raw[:])
		if err := s.st.PutSetting(ctx, keyBackup, b); err != nil {
			return b, err
		}
	}
	return b, nil
}

func (s *Server) backupPassphrase(b backupSettings) (string, error) {
	return s.box.Open(b.Passphrase)
}

// backupOptions are the server-side options for opening locations.
func (s *Server) backupOptions() backup.Options {
	o := s.backupOpts
	o.LocalBase = s.cfg.BackupDir()
	return o
}

// ---- locations ----

// locationView is what the interface sees: no secrets, only whether they are set.
type locationView struct {
	ID                 int64            `json:"id"`
	Name               string           `json:"name"`
	Enabled            bool             `json:"enabled"`
	Type               string           `json:"type"`
	Host               string           `json:"host"`
	Port               int              `json:"port"`
	Username           string           `json:"username"`
	ImplicitTLS        bool             `json:"implicitTls"`
	TLSFingerprint     string           `json:"tlsFingerprint"`
	HostKey            string           `json:"hostKey"`
	HostKeyFingerprint string           `json:"hostKeyFingerprint"`
	SSHAuth            string           `json:"sshAuth"`
	PublicKey          string           `json:"publicKey"`
	TenantID           string           `json:"tenantId"`
	ClientID           string           `json:"clientId"`
	DriveUser          string           `json:"driveUser"`
	Path               string           `json:"path"`
	PasswordSet        bool             `json:"passwordSet"`
	ClientSecretSet    bool             `json:"clientSecretSet"`
	LastRun            *store.BackupRun `json:"lastRun"`
	CreatedAt          time.Time        `json:"createdAt"`
}

// locationRequest is the form: the location plus write-only secrets. Empty
// secrets keep the stored value.
type locationRequest struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	SSHAuth string `json:"sshAuth"` // password or key
	backup.Location
}

func (s *Server) unsealLocation(l store.BackupLocation) (backup.Location, error) {
	var c backup.Location
	if err := json.Unmarshal(l.Config, &c); err != nil {
		return c, err
	}
	var err error
	if c.Password, err = s.box.Open(c.Password); err != nil {
		return c, err
	}
	if c.PrivateKey, err = s.box.Open(c.PrivateKey); err != nil {
		return c, err
	}
	c.ClientSecret, err = s.box.Open(c.ClientSecret)
	return c, err
}

func (s *Server) sealLocation(c backup.Location) (json.RawMessage, error) {
	var err error
	if c.Password, err = s.box.Seal(c.Password); err != nil {
		return nil, err
	}
	if c.PrivateKey, err = s.box.Seal(c.PrivateKey); err != nil {
		return nil, err
	}
	if c.ClientSecret, err = s.box.Seal(c.ClientSecret); err != nil {
		return nil, err
	}
	return json.Marshal(c)
}

func sshFingerprint(authorizedKey string) string {
	k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(authorizedKey))
	if err != nil {
		return ""
	}
	return ssh.FingerprintSHA256(k)
}

func viewOfLocation(l store.BackupLocation, c backup.Location) locationView {
	v := locationView{
		ID: l.ID, Name: l.Name, Enabled: l.Enabled, Type: c.Type, Host: c.Host, Port: c.Port, Username: c.Username,
		ImplicitTLS: c.ImplicitTLS, TLSFingerprint: c.TLSFingerprint, HostKey: c.HostKey, TenantID: c.TenantID,
		ClientID: c.ClientID, DriveUser: c.DriveUser, Path: c.Path, PasswordSet: c.Password != "",
		ClientSecretSet: c.ClientSecret != "", CreatedAt: l.CreatedAt, SSHAuth: "password",
	}
	if c.TLSFingerprint != "" {
		v.TLSFingerprint = backup.FormatFingerprint(c.TLSFingerprint)
	}
	if c.HostKey != "" {
		v.HostKeyFingerprint = sshFingerprint(c.HostKey)
	}
	if c.PrivateKey != "" {
		v.SSHAuth = "key"
		v.PublicKey, _ = backup.PublicKeyOf(c.PrivateKey)
	}
	return v
}

// mergeLocation applies a form to the stored settings of the same location.
func mergeLocation(cur backup.Location, req locationRequest) backup.Location {
	next := req.Location
	sameType := cur.Type == next.Type
	if next.Password == "" && sameType {
		next.Password = cur.Password
	}
	if next.ClientSecret == "" && sameType {
		next.ClientSecret = cur.ClientSecret
	}
	if next.PrivateKey == "" && sameType {
		next.PrivateKey = cur.PrivateKey
	}
	if next.Type == backup.TypeSFTP {
		if req.SSHAuth == "key" {
			next.Password = ""
		} else {
			next.PrivateKey = ""
		}
	}
	// A changed server invalidates the trusted key or certificate.
	if !sameType || cur.Host != strings.TrimSpace(next.Host) || cur.Port != next.Port {
		if next.HostKey == cur.HostKey {
			next.HostKey = ""
		}
		if next.TLSFingerprint == cur.TLSFingerprint {
			next.TLSFingerprint = ""
		}
	}
	next.Normalize()
	return next
}

func (s *Server) locationViews(ctx context.Context) ([]locationView, error) {
	list, err := s.st.ListBackupLocations(ctx)
	if err != nil {
		return nil, err
	}
	last, err := s.st.LastBackupRuns(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]locationView, 0, len(list))
	for _, l := range list {
		c, err := s.unsealLocation(l)
		if err != nil {
			return nil, err
		}
		v := viewOfLocation(l, c)
		if r, ok := last[l.ID]; ok {
			v.LastRun = &r
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Server) handleListLocations(w http.ResponseWriter, r *http.Request) {
	list, err := s.locationViews(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// formLocation resolves a form to a full location, merging stored secrets
// when it names an existing location.
func (s *Server) formLocation(ctx context.Context, req locationRequest) (backup.Location, *store.BackupLocation, error) {
	if req.ID == 0 {
		return mergeLocation(backup.Location{Type: req.Type}, req), nil, nil
	}
	l, err := s.st.BackupLocation(ctx, req.ID)
	if err != nil {
		return backup.Location{}, nil, err
	}
	cur, err := s.unsealLocation(l)
	if err != nil {
		return backup.Location{}, nil, err
	}
	return mergeLocation(cur, req), &l, nil
}

func (s *Server) saveLocation(w http.ResponseWriter, r *http.Request, id int64) {
	var req locationRequest
	if !decode(w, r, &req) {
		return
	}
	req.ID = id
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 80 {
		apiError(w, http.StatusBadRequest, "invalid_name")
		return
	}
	c, existing, err := s.formLocation(r.Context(), req)
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := c.Validate(); err != nil {
		apiErrorDetail(w, http.StatusBadRequest, "invalid_location", err.Error())
		return
	}
	if c.Type == backup.TypeSFTP && c.PrivateKey != "" {
		if _, err := backup.PublicKeyOf(c.PrivateKey); err != nil {
			apiErrorDetail(w, http.StatusBadRequest, "invalid_location", "private key: "+err.Error())
			return
		}
	}
	raw, err := s.sealLocation(c)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	row := store.BackupLocation{ID: id, Name: req.Name, Type: c.Type, Enabled: req.Enabled, Config: raw}
	var saved store.BackupLocation
	if existing == nil {
		saved, err = s.st.CreateBackupLocation(r.Context(), row)
	} else {
		saved, err = s.st.UpdateBackupLocation(r.Context(), row)
	}
	if errors.Is(err, store.ErrConflict) {
		apiError(w, http.StatusConflict, "name_exists")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	action := "backup.location.create"
	if existing != nil {
		action = "backup.location.update"
	}
	s.audit(r, action, saved.Name, true, map[string]any{"type": c.Type, "host": c.Host, "path": c.Path, "enabled": saved.Enabled,
		"secretChanged": req.Password != "" || req.PrivateKey != "" || req.ClientSecret != ""})
	code := http.StatusOK
	if existing == nil {
		code = http.StatusCreated
	}
	writeJSON(w, code, viewOfLocation(saved, c))
}

func (s *Server) handleCreateLocation(w http.ResponseWriter, r *http.Request) {
	s.saveLocation(w, r, 0)
}

func (s *Server) handleUpdateLocation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	s.saveLocation(w, r, id)
}

func (s *Server) handleDeleteLocation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	l, err := s.st.BackupLocation(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.st.DeleteBackupLocation(r.Context(), id); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "backup.location.delete", l.Name, true, nil)
	w.WriteHeader(http.StatusNoContent)
}

type trustView struct {
	Kind        string `json:"kind"`
	Fingerprint string `json:"fingerprint"`
	Value       string `json:"value"`
	Mismatch    bool   `json:"mismatch"`
}

type testResult struct {
	OK         bool       `json:"ok"`
	Error      string     `json:"error,omitempty"`
	Trust      *trustView `json:"trust,omitempty"`
	Repository string     `json:"repository"` // none, ok, wrong_passphrase, unknown
	Snapshots  int        `json:"snapshots"`
}

// testLocation connects, writes and deletes a test file, and looks for
// existing backups.
func (s *Server) testLocation(ctx context.Context, c backup.Location, passphrase string) testResult {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	be, err := backup.Open(ctx, c, s.backupOptions())
	if err != nil {
		return failedTest(err)
	}
	defer be.Close()
	name := ".fleetwarden-write-test"
	if err := be.Put(ctx, name, []byte("FleetWarden write test\n")); err != nil {
		return testResult{Error: "write test failed: " + err.Error(), Repository: "unknown"}
	}
	if err := be.Delete(ctx, name); err != nil {
		return testResult{Error: "delete test failed: " + err.Error(), Repository: "unknown"}
	}
	res := testResult{OK: true, Repository: "unknown"}
	if passphrase == "" {
		if _, err := be.Get(ctx, "fleetwarden-backup.json"); errors.Is(err, backup.ErrNotExist) {
			res.Repository = "none"
		}
		return res
	}
	repo, err := backup.OpenRepo(ctx, be, passphrase, false)
	switch {
	case errors.Is(err, backup.ErrNoRepository):
		res.Repository = "none"
	case errors.Is(err, backup.ErrWrongPassphrase):
		res.Repository = "wrong_passphrase"
	case err != nil:
		res.Error = err.Error()
	default:
		res.Repository = "ok"
		if snaps, err := repo.Snapshots(ctx); err == nil {
			res.Snapshots = len(snaps)
		}
	}
	return res
}

func failedTest(err error) testResult {
	res := testResult{Error: err.Error(), Repository: "unknown"}
	var te *backup.TrustError
	if errors.As(err, &te) {
		res.Trust = &trustView{Kind: te.Kind, Fingerprint: te.Fingerprint, Value: te.Value, Mismatch: te.Mismatch}
	}
	return res
}

func (s *Server) handleTestLocation(w http.ResponseWriter, r *http.Request) {
	var req locationRequest
	if !decode(w, r, &req) {
		return
	}
	c, _, err := s.formLocation(r.Context(), req)
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := c.Validate(); err != nil {
		apiErrorDetail(w, http.StatusBadRequest, "invalid_location", err.Error())
		return
	}
	b, err := s.loadBackupSettings(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	pass, _ := s.backupPassphrase(b)
	res := s.testLocation(r.Context(), c, pass)
	s.audit(r, "backup.location.test", strings.TrimSpace(req.Name), res.OK, map[string]any{"type": c.Type, "host": c.Host, "error": res.Error})
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleSSHKey(w http.ResponseWriter, r *http.Request) {
	priv, pub, err := backup.GenerateSSHKey("fleetwarden-backup")
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"privateKey": priv, "publicKey": pub})
}

// ---- passphrase and schedule ----

type backupConfigView struct {
	PassphraseSet bool           `json:"passphraseSet"`
	Schedule      backupSchedule `json:"schedule"`
	NextRun       *time.Time     `json:"nextRun"`
	TimeZone      string         `json:"timeZone"`
	InstanceID    string         `json:"instanceId"`
}

func (s *Server) backupConfigView(b backupSettings) backupConfigView {
	v := backupConfigView{PassphraseSet: b.Passphrase != "", Schedule: b.Schedule, TimeZone: s.now().Location().String(), InstanceID: b.InstanceID}
	if b.Schedule.Enabled {
		n := nextSlot(b.Schedule, s.now())
		v.NextRun = &n
	}
	if v.TimeZone == "Local" {
		v.TimeZone = time.Now().Format("MST")
	}
	return v
}

func (s *Server) handleGetBackupConfig(w http.ResponseWriter, r *http.Request) {
	b, err := s.loadBackupSettings(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.backupConfigView(b))
}

func (s *Server) handlePutSchedule(w http.ResponseWriter, r *http.Request) {
	var req backupSchedule
	if !decode(w, r, &req) {
		return
	}
	if !req.valid() {
		apiError(w, http.StatusBadRequest, "invalid_schedule")
		return
	}
	b, err := s.loadBackupSettings(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if req.Enabled && b.Passphrase == "" {
		apiError(w, http.StatusBadRequest, "passphrase_required")
		return
	}
	if req.Enabled && (!b.Schedule.Enabled || req.StartTime != b.Schedule.StartTime || req.IntervalHours != b.Schedule.IntervalHours) {
		// Start counting from now: do not run a backup for a time slot that has already passed.
		b.LastSlot = prevSlot(req, s.now())
	}
	b.Schedule = req
	if err := s.st.PutSetting(r.Context(), keyBackup, b); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "backup.schedule", "", true, map[string]any{"enabled": req.Enabled, "intervalHours": req.IntervalHours,
		"startTime": req.StartTime, "fullEveryDays": req.FullEveryDays, "keepFull": req.KeepFull})
	writeJSON(w, http.StatusOK, s.backupConfigView(b))
}

const minPassphrase = 12

// handlePutPassphrase sets or changes the backup passphrase. A change
// re-wraps the key of every location that already holds backups, so all
// existing backups stay restorable with the new passphrase.
func (s *Server) handlePutPassphrase(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len([]rune(req.New)) < minPassphrase {
		apiError(w, http.StatusBadRequest, "passphrase_too_short")
		return
	}
	if !s.backupMu.TryLock() {
		apiError(w, http.StatusConflict, "backup_running")
		return
	}
	defer s.backupMu.Unlock()
	b, err := s.loadBackupSettings(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	cur, err := s.backupPassphrase(b)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if cur != "" && req.Current != cur {
		s.audit(r, "backup.passphrase", "", false, map[string]any{"reason": "wrong current passphrase"})
		apiError(w, http.StatusBadRequest, "wrong_passphrase")
		return
	}
	if cur != "" && cur != req.New {
		if loc, err := s.rewrapAll(r.Context(), cur, req.New); err != nil {
			s.audit(r, "backup.passphrase", loc, false, map[string]any{"error": err.Error()})
			apiErrorDetail(w, http.StatusBadGateway, "passphrase_change_failed", loc+": "+err.Error())
			return
		}
	}
	if b.Passphrase, err = s.box.Seal(req.New); err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.st.PutSetting(r.Context(), keyBackup, b); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "backup.passphrase", "", true, map[string]any{"changed": cur != ""})
	writeJSON(w, http.StatusOK, s.backupConfigView(b))
}

// rewrapAll changes the passphrase at every location with backups. When one
// fails, the locations already changed are changed back.
func (s *Server) rewrapAll(ctx context.Context, from, to string) (string, error) {
	locs, err := s.st.ListBackupLocations(ctx)
	if err != nil {
		return "", err
	}
	var changed []rewrapped
	defer func() {
		for _, d := range changed {
			d.be.Close()
		}
	}()
	for _, l := range locs {
		c, err := s.unsealLocation(l)
		if err != nil {
			return l.Name, err
		}
		be, err := backup.Open(ctx, c, s.backupOptions())
		if err != nil {
			s.revert(ctx, changed, from)
			return l.Name, err
		}
		repo, err := backup.OpenRepo(ctx, be, from, false)
		if errors.Is(err, backup.ErrNoRepository) || errors.Is(err, backup.ErrWrongPassphrase) {
			be.Close() // nothing of ours, or another server's backups
			continue
		}
		if err == nil {
			err = repo.ChangePassphrase(ctx, to)
		}
		if err != nil {
			be.Close()
			s.revert(ctx, changed, from)
			return l.Name, err
		}
		changed = append(changed, rewrapped{repo, be})
	}
	return "", nil
}

type rewrapped struct {
	repo *backup.Repo
	be   backup.Backend
}

func (s *Server) revert(ctx context.Context, changed []rewrapped, from string) {
	for _, d := range changed {
		if err := d.repo.ChangePassphrase(ctx, from); err != nil {
			s.log.Error("backup passphrase: could not change a location back", "err", err)
		}
	}
}
