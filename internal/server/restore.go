package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/backup"
	"github.com/rhisijmuiden/fleetwarden/internal/config"
	"github.com/rhisijmuiden/fleetwarden/internal/updates"
	"github.com/rhisijmuiden/fleetwarden/internal/version"
)

// A restore runs in two steps:
//
//  1. The server downloads and verifies the snapshot into
//     <data>/restore/<staging id>/ and writes <data>/restore/request.json.
//  2. The fleetwarden-restore systemd path unit starts `fleetwarden-server
//     restore-apply` as root. It stops the server, replaces the database and
//     the data directory as the fleetwarden user (keeping a rollback copy),
//     and starts the server again. It reports in restore-status.json, which
//     the restarted server audits and mails once.

// RestoreRequest is the file the server writes for the restore helper.
type RestoreRequest struct {
	Staging     string    `json:"staging"`
	Snapshot    string    `json:"snapshot"`
	Version     string    `json:"version"`
	Host        string    `json:"host"`
	RequestedBy string    `json:"requestedBy"`
	RequestedAt time.Time `json:"requestedAt"`
}

// RestoreStatus is written by the restore helper.
type RestoreStatus struct {
	ID          string    `json:"id"`
	State       string    `json:"state"` // applying, completed, failed
	Message     string    `json:"message"`
	Snapshot    string    `json:"snapshot"`
	RequestedBy string    `json:"requestedBy"`
	StartedAt   time.Time `json:"startedAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

const restoreStatusFile = "restore-status.json"

var stagingRe = regexp.MustCompile(`^staging-\d{8}T\d{6}Z$`)

// ReadRestoreStatus reads the helper's status; missing gives an empty status.
func ReadRestoreStatus(dir string) (RestoreStatus, error) {
	var st RestoreStatus
	raw, err := os.ReadFile(filepath.Join(dir, restoreStatusFile))
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(raw, &st)
}

// WriteRestoreStatus writes the status atomically.
func WriteRestoreStatus(dir string, st RestoreStatus) error {
	st.UpdatedAt = time.Now().UTC()
	raw, _ := json.MarshalIndent(st, "", "  ")
	tmp := filepath.Join(dir, "."+restoreStatusFile+".tmp")
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, restoreStatusFile))
}

// restorePrep is the server-side state of step 1.
type restorePrep struct {
	mu       sync.Mutex
	State    string    `json:"state"` // "", preparing, requested, failed
	Message  string    `json:"message"`
	Snapshot string    `json:"snapshot"`
	Done     int64     `json:"done"`
	Total    int64     `json:"total"`
	Started  time.Time `json:"startedAt"`
}

type restoreView struct {
	State    string         `json:"state"`
	Message  string         `json:"message"`
	Snapshot string         `json:"snapshot"`
	Done     int64          `json:"done"`
	Total    int64          `json:"total"`
	Pending  bool           `json:"pending"`
	Last     *RestoreStatus `json:"last"`
}

func (s *Server) restoreBusy() bool {
	s.restore.mu.Lock()
	defer s.restore.mu.Unlock()
	return s.restore.State == "preparing" || s.restore.State == "requested"
}

func restorableVersion(v string) bool {
	return v == "" || !updates.ValidVersion(v) || !updates.ValidVersion(version.Version) || updates.Compare(v, version.Version) <= 0
}

func (s *Server) restoreView() restoreView {
	s.restore.mu.Lock()
	v := restoreView{State: s.restore.State, Message: s.restore.Message, Snapshot: s.restore.Snapshot, Done: s.restore.Done, Total: s.restore.Total}
	s.restore.mu.Unlock()
	if _, err := os.Stat(filepath.Join(s.cfg.RestoreDir(), "request.json")); err == nil {
		v.Pending = true
	}
	if st, err := ReadRestoreStatus(s.cfg.UpdateStatusDir); err == nil && st.ID != "" {
		v.Last = &st
	}
	return v
}

func (s *Server) handleRestoreStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.restoreView())
}

func (s *Server) handleStartRestore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		snapshotSource
		Confirm string `json:"confirm"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Confirm != "RESTORE" {
		apiError(w, http.StatusBadRequest, "confirm_required")
		return
	}
	if !s.backupMu.TryLock() {
		apiError(w, http.StatusConflict, "backup_running")
		return
	}
	s.restore.mu.Lock()
	if s.restore.State == "preparing" || s.restore.State == "requested" {
		s.restore.mu.Unlock()
		s.backupMu.Unlock()
		apiError(w, http.StatusConflict, "restore_running")
		return
	}
	s.restore.mu.Unlock()
	// Check the location, passphrase and version before going to the background.
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	repo, done, err := s.openRepoFor(ctx, req.LocationID, req.Location, req.Passphrase)
	var m *backup.Manifest
	if err == nil {
		m, err = repo.Manifest(ctx, req.Chain, req.Snapshot)
		if err != nil {
			done()
		}
	}
	cancel()
	if err != nil {
		s.backupMu.Unlock()
		s.repoError(w, r, err)
		return
	}
	if !restorableVersion(m.Version) {
		done()
		s.backupMu.Unlock()
		apiErrorDetail(w, http.StatusBadRequest, "backup_newer", m.Version)
		return
	}
	u, _ := currentUser(r)
	s.restore.mu.Lock()
	s.restore.State, s.restore.Message, s.restore.Snapshot = "preparing", "", m.Snapshot.String()
	s.restore.Done, s.restore.Total, s.restore.Started = 0, m.Size, s.now()
	s.restore.mu.Unlock()
	s.audit(r, "backup.restore_request", m.Snapshot.String(), true, map[string]any{"version": m.Version, "host": m.Host,
		"location": req.LocationID})
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		defer s.backupMu.Unlock()
		defer done()
		err := s.prepareRestore(s.bgContext(), repo, m, u.Username)
		s.restore.mu.Lock()
		if err != nil {
			s.restore.State, s.restore.Message = "failed", err.Error()
		} else {
			s.restore.State = "requested"
		}
		s.restore.mu.Unlock()
		if err != nil {
			s.log.Error("restore", "err", err)
			_ = s.st.Audit(context.Background(), auditSystem("backup.restore_prepare", m.Snapshot.String(), false, map[string]any{"error": err.Error()}))
		}
	}()
	writeJSON(w, http.StatusAccepted, s.restoreView())
}

// handleCancelRestore withdraws a staged restore that the helper has not
// picked up yet.
func (s *Server) handleCancelRestore(w http.ResponseWriter, r *http.Request) {
	s.restore.mu.Lock()
	defer s.restore.mu.Unlock()
	if s.restore.State == "preparing" {
		apiError(w, http.StatusConflict, "restore_running")
		return
	}
	dir := s.cfg.RestoreDir()
	if err := os.Remove(filepath.Join(dir, "request.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.internalError(w, r, err)
		return
	}
	old, _ := filepath.Glob(filepath.Join(dir, "staging-*"))
	for _, o := range old {
		os.RemoveAll(o)
	}
	s.restore.State, s.restore.Message = "", ""
	s.audit(r, "backup.restore_cancel", s.restore.Snapshot, true, nil)
	w.WriteHeader(http.StatusNoContent)
}

// prepareRestore downloads the snapshot into a staging folder and asks the
// restore helper to apply it.
func (s *Server) prepareRestore(ctx context.Context, repo *backup.Repo, m *backup.Manifest, actor string) error {
	dir := s.cfg.RestoreDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	// Only one staged restore at a time.
	old, _ := filepath.Glob(filepath.Join(dir, "staging-*"))
	for _, o := range old {
		os.RemoveAll(o)
	}
	id := "staging-" + s.now().UTC().Format("20060102T150405Z")
	staging := filepath.Join(dir, id)
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return err
	}
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(repo.Read(ctx, m, pw, func(done int64) {
			s.restore.mu.Lock()
			s.restore.Done = done
			s.restore.mu.Unlock()
		}))
	}()
	meta, err := backup.ExtractArchive(pr, staging)
	if err == nil {
		// Read to the end so the checksum of the whole stream is verified.
		_, err = io.Copy(io.Discard, pr)
	}
	pr.CloseWithError(errors.New("restore stopped"))
	if err != nil {
		os.RemoveAll(staging)
		return fmt.Errorf("download: %w", err)
	}
	req := RestoreRequest{Staging: id, Snapshot: m.Snapshot.String(), Version: meta.Version, Host: meta.Host, RequestedBy: actor, RequestedAt: s.now()}
	raw, _ := json.MarshalIndent(req, "", "  ")
	tmp := filepath.Join(dir, ".request.json.tmp")
	if err := os.WriteFile(tmp, raw, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "request.json"))
}

// reportRestoreOutcome audits and mails the result of a finished restore once.
func (s *Server) reportRestoreOutcome(ctx context.Context) {
	st, err := ReadRestoreStatus(s.cfg.UpdateStatusDir)
	if err != nil || st.ID == "" || st.State == "applying" {
		return
	}
	b, err := s.loadBackupSettings(ctx)
	if err != nil || b.LastRestoreReported == st.ID {
		return
	}
	b.LastRestoreReported = st.ID
	if err := s.st.PutSetting(ctx, keyBackup, b); err != nil {
		return
	}
	ok := st.State == "completed"
	_ = s.st.Audit(ctx, auditSystem("backup.restore_result", st.Snapshot, ok, map[string]any{
		"state": st.State, "message": st.Message, "requestedBy": st.RequestedBy}))
	kind := "restore_success"
	if !ok {
		kind = "restore_failed"
	}
	s.notifyAdmins(ctx, kind, map[string]string{"snapshot": st.Snapshot, "detail": st.Message})
}

// ---- step 2: apply (fleetwarden-server restore-apply) ----

// ReadRestoreRequest reads and validates the pending request.
func ReadRestoreRequest(cfg config.Server) (RestoreRequest, error) {
	var req RestoreRequest
	p := filepath.Join(cfg.RestoreDir(), "request.json")
	info, err := os.Lstat(p)
	if err != nil {
		return req, err
	}
	if !info.Mode().IsRegular() {
		return req, errors.New("request.json is not a regular file")
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return req, err
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return req, fmt.Errorf("request.json: %w", err)
	}
	if !stagingRe.MatchString(req.Staging) {
		return req, errors.New("invalid staging folder in request.json")
	}
	return req, nil
}

// ApplyRestore replaces the database and the data directory with a staged
// backup. The server must be stopped. On failure the previous state is put
// back. It runs as the service user.
func ApplyRestore(ctx context.Context, cfg config.Server, req RestoreRequest) (msg string, err error) {
	staging := filepath.Join(cfg.RestoreDir(), req.Staging)
	if info, err := os.Lstat(staging); err != nil || !info.IsDir() {
		return "", fmt.Errorf("staged backup %s not found", req.Staging)
	}
	dump := filepath.Join(staging, "database.dump")
	if _, err := os.Stat(dump); err != nil {
		return "", errors.New("the staged backup has no database dump")
	}
	rollback := filepath.Join(cfg.RestoreDir(), "rollback")
	if err := os.RemoveAll(rollback); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(rollback, "data"), 0o700); err != nil {
		return "", err
	}
	// 1. A copy of the current database, to roll back to.
	if err := backup.DumpDatabase(ctx, cfg.DatabaseURL, filepath.Join(rollback, "database.dump")); err != nil {
		return "", fmt.Errorf("could not save the current database, nothing was changed: %w", err)
	}
	// 2. The database, in one transaction.
	if err := backup.RestoreDatabase(ctx, cfg.DatabaseURL, dump); err != nil {
		return "", fmt.Errorf("database restore failed, nothing was changed: %w", err)
	}
	// 3. The data directory: move the current files aside, put the backup's in place.
	moved, err := replaceData(cfg.DataDir, filepath.Join(staging, "data"), filepath.Join(rollback, "data"))
	if err != nil {
		undoData(cfg.DataDir, filepath.Join(rollback, "data"), moved)
		if rerr := backup.RestoreDatabase(context.WithoutCancel(ctx), cfg.DatabaseURL, filepath.Join(rollback, "database.dump")); rerr != nil {
			return "", fmt.Errorf("restoring files failed (%v) and the database could not be rolled back (%v); the previous database is in %s", err, rerr, rollback)
		}
		return "", fmt.Errorf("restoring files failed, the previous state was put back: %w", err)
	}
	// 4. The configuration file of the backup, for reference only: network
	//    and database settings of this server stay as they are.
	if raw, err := os.ReadFile(filepath.Join(staging, "etc", "server.env")); err == nil {
		_ = os.WriteFile(filepath.Join(cfg.DataDir, "server.env.from-backup"), raw, 0o600)
	}
	// The installer's one-time password belongs to an account that no longer exists.
	_ = os.Remove(cfg.InitialPasswordPath())
	_ = os.RemoveAll(staging)
	_ = os.Remove(filepath.Join(cfg.RestoreDir(), "request.json"))
	msg = fmt.Sprintf("Restored %s (FleetWarden %s from %s)", req.Snapshot, req.Version, req.Host)
	return msg, nil
}

func excluded(name string) bool {
	for _, e := range config.BackupExclude {
		if e == name {
			return true
		}
	}
	return false
}

// replaceData moves the current top-level entries of dataDir (except the
// excluded ones) into aside, then copies src into dataDir.
func replaceData(dataDir, src, aside string) ([]string, error) {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil, err
	}
	var moved []string
	for _, e := range entries {
		if excluded(e.Name()) {
			continue
		}
		if err := os.Rename(filepath.Join(dataDir, e.Name()), filepath.Join(aside, e.Name())); err != nil {
			return moved, err
		}
		moved = append(moved, e.Name())
	}
	if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
		return moved, nil
	}
	err = filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if rel == "." {
			return nil
		}
		top, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
		if excluded(top) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		dst := filepath.Join(dataDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o750)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return copyFile(p, dst)
	})
	return moved, err
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// undoData removes what replaceData copied and moves the old entries back.
func undoData(dataDir, aside string, moved []string) {
	entries, _ := os.ReadDir(dataDir)
	for _, e := range entries {
		if !excluded(e.Name()) {
			os.RemoveAll(filepath.Join(dataDir, e.Name()))
		}
	}
	for _, name := range moved {
		_ = os.Rename(filepath.Join(aside, name), filepath.Join(dataDir, name))
	}
}
