package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/backup"
	"github.com/rhisijmuiden/fleetwarden/internal/config"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
	"github.com/rhisijmuiden/fleetwarden/internal/version"
)

// ---- schedule ----

// slotOffsets are the times of day (as offsets from midnight) of a schedule.
func slotOffsets(sc backupSchedule) []time.Duration {
	h, _ := strconv.Atoi(sc.StartTime[:2])
	m, _ := strconv.Atoi(sc.StartTime[3:])
	start := time.Duration(h)*time.Hour + time.Duration(m)*time.Minute
	var out []time.Duration
	for k := 0; k < 24/sc.IntervalHours; k++ {
		out = append(out, (start+time.Duration(k*sc.IntervalHours)*time.Hour)%(24*time.Hour))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func midnight(t time.Time) time.Time {
	y, mo, d := t.Date()
	return time.Date(y, mo, d, 0, 0, 0, 0, t.Location())
}

// prevSlot is the latest scheduled time at or before now.
func prevSlot(sc backupSchedule, now time.Time) time.Time {
	var best time.Time
	for _, day := range []time.Time{midnight(now).AddDate(0, 0, -1), midnight(now)} {
		for _, off := range slotOffsets(sc) {
			if t := day.Add(off); !t.After(now) && t.After(best) {
				best = t
			}
		}
	}
	return best
}

// nextSlot is the first scheduled time after now.
func nextSlot(sc backupSchedule, now time.Time) time.Time {
	for _, day := range []time.Time{midnight(now), midnight(now).AddDate(0, 0, 1)} {
		for _, off := range slotOffsets(sc) {
			if t := day.Add(off); t.After(now) {
				return t
			}
		}
	}
	return now.Add(time.Duration(sc.IntervalHours) * time.Hour)
}

// backupTick starts a scheduled backup when a time slot has passed. A slot
// missed while the server was down runs once when it is back.
func (s *Server) backupTick(ctx context.Context) {
	b, err := s.loadBackupSettings(ctx)
	if err != nil || !b.Schedule.Enabled || b.Passphrase == "" {
		return
	}
	slot := prevSlot(b.Schedule, s.now())
	if !slot.After(b.LastSlot) {
		return
	}
	if err := s.startBackup(ctx, "schedule", "", "auto", 0); err != nil {
		if !errors.Is(err, errBackupBusy) {
			s.log.Warn("scheduled backup not started", "err", err)
		}
		return // try again next tick
	}
	b.LastSlot = slot
	if err := s.st.PutSetting(ctx, keyBackup, b); err != nil {
		s.log.Error("backup schedule", "err", err)
	}
}

// ---- runs ----

var (
	errBackupBusy  = errors.New("a backup or restore is already running")
	errNoLocations = errors.New("no enabled backup locations")
	errNoPass      = errors.New("no backup passphrase set")
)

// backupProgress is the live state of the running backup.
type backupProgress struct {
	Running   bool                       `json:"running"`
	Trigger   string                     `json:"trigger"`
	StartedAt time.Time                  `json:"startedAt"`
	Phase     string                     `json:"phase"` // dump, upload, done
	Locations map[int64]*locationProgess `json:"locations"`
}

type locationProgess struct {
	Name  string `json:"name"`
	State string `json:"state"` // waiting, running, succeeded, failed
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
	Kind  string `json:"kind"`
	Error string `json:"error,omitempty"`
}

type progressState struct {
	mu sync.Mutex
	p  backupProgress
}

func (ps *progressState) update(fn func(p *backupProgress)) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	fn(&ps.p)
}

func (ps *progressState) snapshot() backupProgress {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	out := ps.p
	out.Locations = map[int64]*locationProgess{}
	for k, v := range ps.p.Locations {
		c := *v
		out.Locations[k] = &c
	}
	return out
}

// prepareBackup checks that a backup can run and returns what it needs.
func (s *Server) prepareBackup(ctx context.Context, only int64) (backupSettings, string, []store.BackupLocation, error) {
	if s.restoreBusy() {
		return backupSettings{}, "", nil, errBackupBusy
	}
	b, err := s.loadBackupSettings(ctx)
	if err != nil {
		return b, "", nil, err
	}
	pass, err := s.backupPassphrase(b)
	if err != nil {
		return b, "", nil, err
	}
	if pass == "" {
		return b, "", nil, errNoPass
	}
	all, err := s.st.ListBackupLocations(ctx)
	if err != nil {
		return b, "", nil, err
	}
	var locs []store.BackupLocation
	for _, l := range all {
		if (only == 0 && l.Enabled) || l.ID == only {
			locs = append(locs, l)
		}
	}
	if len(locs) == 0 {
		return b, "", nil, errNoLocations
	}
	return b, pass, locs, nil
}

func (s *Server) resetProgress(trigger string, locs []store.BackupLocation) {
	s.progress.update(func(p *backupProgress) {
		*p = backupProgress{Running: true, Trigger: trigger, StartedAt: s.now(), Phase: "dump", Locations: map[int64]*locationProgess{}}
		for _, l := range locs {
			p.Locations[l.ID] = &locationProgess{Name: l.Name, State: "waiting"}
		}
	})
}

// startBackup starts a backup in the background. kind is auto (the
// schedule decides), full or incremental. only limits it to one location.
func (s *Server) startBackup(ctx context.Context, trigger, actor, kind string, only int64) error {
	b, pass, locs, err := s.prepareBackup(ctx, only)
	if err != nil {
		return err
	}
	if !s.backupMu.TryLock() {
		return errBackupBusy
	}
	s.resetProgress(trigger, locs)
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		defer s.backupMu.Unlock()
		s.runBackup(s.bgContext(), trigger, actor, kind, b, pass, locs)
	}()
	return nil
}

// BackupNow runs a backup to all enabled locations and waits for it
// (`fleetwarden-server backup`).
func (s *Server) BackupNow(ctx context.Context, kind string) error {
	b, pass, locs, err := s.prepareBackup(ctx, 0)
	if err != nil {
		return err
	}
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	s.resetProgress("manual", locs)
	if failures := s.runBackup(ctx, "manual", "console", kind, b, pass, locs); len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func (s *Server) bgContext() context.Context { return s.bgCtx }

func (s *Server) archiveSpec(dump string, b backupSettings) backup.ArchiveSpec {
	host, _ := os.Hostname()
	return backup.ArchiveSpec{
		Meta:     backup.ArchiveMeta{Version: version.Version, Host: host, Instance: b.InstanceID, Created: s.now()},
		DumpPath: dump, DataDir: s.cfg.DataDir, Exclude: config.BackupExclude, EnvFile: s.cfg.EnvFile,
	}
}

func (s *Server) runBackup(ctx context.Context, trigger, actor, kind string, b backupSettings, pass string, locs []store.BackupLocation) []string {
	runs := map[int64]store.BackupRun{}
	for _, l := range locs {
		id := l.ID
		run, err := s.st.StartBackupRun(ctx, store.BackupRun{LocationID: &id, LocationName: l.Name, Trigger: trigger, Actor: actor, Kind: kind})
		if err != nil {
			s.log.Error("backup history", "err", err)
		}
		runs[l.ID] = run
	}
	var failures []string
	finish := func(l store.BackupLocation, run store.BackupRun, err error) {
		run.Status = "succeeded"
		if err != nil {
			run.Status, run.Error = "failed", err.Error()
			failures = append(failures, l.Name+": "+err.Error())
		}
		if run.ID != 0 {
			if ferr := s.st.FinishBackupRun(context.WithoutCancel(ctx), run); ferr != nil {
				s.log.Error("backup history", "err", ferr)
			}
		}
		s.progress.update(func(p *backupProgress) {
			lp := p.Locations[l.ID]
			lp.State, lp.Kind = run.Status, run.Kind
			if err != nil {
				lp.Error = err.Error()
			}
		})
		_ = s.st.Audit(context.WithoutCancel(ctx), store.AuditEntry{Actor: actorOr(actor), Action: "backup.run", Target: l.Name,
			Success: err == nil, Details: map[string]any{"trigger": trigger, "kind": run.Kind, "snapshot": run.Snapshot,
				"sizeBytes": run.SizeBytes, "uploadedBytes": run.UploadedBytes, "error": run.Error}})
	}

	tmp := filepath.Join(s.cfg.TmpDir(), "backup-"+s.now().UTC().Format("20060102T150405Z"))
	defer os.RemoveAll(tmp)
	dump := filepath.Join(tmp, "database.dump")
	err := os.MkdirAll(tmp, 0o700)
	if err == nil {
		err = backup.DumpDatabase(ctx, s.cfg.DatabaseURL, dump)
	}
	if err != nil {
		err = fmt.Errorf("database dump: %w", err)
		for _, l := range locs {
			finish(l, runs[l.ID], err)
		}
		s.backupFailed(ctx, failures)
		s.progress.update(func(p *backupProgress) { p.Running, p.Phase = false, "done" })
		return failures
	}
	s.progress.update(func(p *backupProgress) { p.Phase = "upload" })
	spec := s.archiveSpec(dump, b)
	total := spec.EstimateSize()
	for _, l := range locs {
		run := runs[l.ID]
		s.progress.update(func(p *backupProgress) {
			p.Locations[l.ID].State, p.Locations[l.ID].Total = "running", total
		})
		m, kindDone, err := s.backupTo(ctx, l, spec, kind, b, pass)
		run.Kind = kindDone
		if m != nil {
			run.Chain, run.Snapshot, run.SizeBytes, run.UploadedBytes = m.Chain, m.ID, m.Size, m.UploadedBytes
		}
		finish(l, run, err)
	}
	if len(failures) > 0 {
		s.backupFailed(ctx, failures)
	}
	s.progress.update(func(p *backupProgress) { p.Running, p.Phase = false, "done" })
	return failures
}

func actorOr(a string) string {
	if a == "" {
		return "system"
	}
	return a
}

// backupTo writes one snapshot to one location and applies retention.
func (s *Server) backupTo(ctx context.Context, l store.BackupLocation, spec backup.ArchiveSpec, kind string, b backupSettings, pass string) (*backup.Manifest, string, error) {
	c, err := s.unsealLocation(l)
	if err != nil {
		return nil, kind, err
	}
	be, err := backup.Open(ctx, c, s.backupOptions())
	if err != nil {
		return nil, kind, err
	}
	defer be.Close()
	repo, err := backup.OpenRepo(ctx, be, pass, true)
	if errors.Is(err, backup.ErrWrongPassphrase) {
		return nil, kind, errors.New("this location holds backups made with a different passphrase")
	}
	if err != nil {
		return nil, kind, err
	}
	chains, err := repo.Chains(ctx)
	if err != nil {
		return nil, kind, err
	}
	var latest *backup.Chain
	for i := len(chains) - 1; i >= 0; i-- {
		if chains[i].Instance == b.InstanceID && chains[i].Valid() {
			latest = &chains[i]
			break
		}
	}
	now := s.now()
	switch {
	case kind == backup.KindFull || latest == nil:
		kind = backup.KindFull
	case kind == "auto" && now.Sub(latest.Snapshots[0].Time) >= time.Duration(b.Schedule.FullEveryDays)*24*time.Hour-time.Hour:
		kind = backup.KindFull
	default:
		kind = backup.KindIncremental
	}
	chain := ""
	if kind == backup.KindFull {
		chain = backup.NewChainID(b.InstanceID, now)
	} else {
		chain = latest.ID
	}
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(backup.WriteArchive(pw, spec)) }()
	meta := backup.Manifest{Host: spec.Meta.Host, Version: version.Version}
	m, err := repo.Write(ctx, chain, kind, now, pr, meta, func(done int64) {
		s.progress.update(func(p *backupProgress) {
			lp := p.Locations[l.ID]
			lp.Done, lp.Kind = done, kind
			if done > lp.Total {
				lp.Total = done
			}
		})
	})
	pr.CloseWithError(errors.New("backup stopped"))
	if err != nil {
		return nil, kind, err
	}
	if removed, err := repo.Prune(ctx, b.InstanceID, chain, b.Schedule.KeepFull, now); err != nil {
		s.log.Warn("backup retention", "location", l.Name, "err", err)
	} else if len(removed) > 0 {
		_ = s.st.Audit(ctx, auditSystem("backup.retention", l.Name, true, map[string]any{"removed": removed}))
	}
	return m, kind, nil
}

func (s *Server) backupFailed(ctx context.Context, failures []string) {
	s.notifyAdmins(context.WithoutCancel(ctx), "backup_failed", map[string]string{"detail": strings.Join(failures, "\n")})
}

// notifyAdmins mails every administrator in their own language.
func (s *Server) notifyAdmins(ctx context.Context, kind string, vars map[string]string) {
	list, err := s.st.AdminRecipients(ctx)
	if err != nil {
		s.log.Error("admin recipients", "err", err)
		return
	}
	b := s.loadBranding(ctx)
	var g generalSettings
	_ = s.st.GetSetting(ctx, keyGeneral, &g)
	for _, r := range list {
		s.enqueue(ctx, kind, r.Language, r.Email, mailBrandOf(b, g), vars)
	}
}

// ---- handlers ----

func (s *Server) handleRunBackup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind       string `json:"kind"`
		LocationID int64  `json:"locationId"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Kind == "" {
		req.Kind = "auto"
	}
	if req.Kind != "auto" && req.Kind != backup.KindFull && req.Kind != backup.KindIncremental {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	u, _ := currentUser(r)
	err := s.startBackup(r.Context(), "manual", u.Username, req.Kind, req.LocationID)
	switch {
	case errors.Is(err, errBackupBusy):
		apiError(w, http.StatusConflict, "backup_running")
	case errors.Is(err, errNoPass):
		apiError(w, http.StatusBadRequest, "passphrase_required")
	case errors.Is(err, errNoLocations):
		apiError(w, http.StatusBadRequest, "no_locations")
	case err != nil:
		s.internalError(w, r, err)
	default:
		writeJSON(w, http.StatusAccepted, s.progress.snapshot())
	}
}

func (s *Server) handleBackupProgress(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.progress.snapshot())
}

func (s *Server) handleBackupRuns(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListBackupRuns(r.Context(), 200)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// openRepoFor opens the repository of a stored location or of a location
// from a form (restore on a new server).
func (s *Server) openRepoFor(ctx context.Context, id int64, form *backup.Location, passphrase string) (*backup.Repo, func(), error) {
	var c backup.Location
	if id != 0 {
		l, err := s.st.BackupLocation(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		if c, err = s.unsealLocation(l); err != nil {
			return nil, nil, err
		}
	} else if form != nil {
		c = *form
		c.Normalize()
	} else {
		return nil, nil, store.ErrNotFound
	}
	if passphrase == "" {
		b, err := s.loadBackupSettings(ctx)
		if err != nil {
			return nil, nil, err
		}
		if passphrase, err = s.backupPassphrase(b); err != nil {
			return nil, nil, err
		}
		if passphrase == "" {
			return nil, nil, errNoPass
		}
	}
	be, err := backup.Open(ctx, c, s.backupOptions())
	if err != nil {
		return nil, nil, err
	}
	repo, err := backup.OpenRepo(ctx, be, passphrase, false)
	if err != nil {
		be.Close()
		return nil, nil, err
	}
	return repo, func() { be.Close() }, nil
}

// repoError answers the common repository errors with stable codes.
func (s *Server) repoError(w http.ResponseWriter, r *http.Request, err error) {
	var te *backup.TrustError
	switch {
	case errors.Is(err, store.ErrNotFound):
		apiError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, errNoPass):
		apiError(w, http.StatusBadRequest, "passphrase_required")
	case errors.Is(err, backup.ErrNoRepository):
		apiError(w, http.StatusNotFound, "no_backups")
	case errors.Is(err, backup.ErrWrongPassphrase):
		apiError(w, http.StatusBadRequest, "wrong_passphrase")
	case errors.As(err, &te):
		apiErrorDetail(w, http.StatusBadGateway, "untrusted_server", err.Error())
	case errors.Is(err, backup.ErrCorrupt):
		apiErrorDetail(w, http.StatusBadGateway, "backup_corrupt", err.Error())
	default:
		apiErrorDetail(w, http.StatusBadGateway, "location_failed", err.Error())
	}
}

// snapshotSource names a location: a stored one by ID, or one from a form
// with its passphrase (a new server that has no settings yet).
type snapshotSource struct {
	LocationID int64            `json:"locationId"`
	Location   *backup.Location `json:"location"`
	Passphrase string           `json:"passphrase"`
	Chain      string           `json:"chain"`
	Snapshot   string           `json:"snapshot"`
}

func (s *Server) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	var req snapshotSource
	if !decode(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	repo, done, err := s.openRepoFor(ctx, req.LocationID, req.Location, req.Passphrase)
	if err != nil {
		s.repoError(w, r, err)
		return
	}
	defer done()
	snaps, err := repo.Snapshots(ctx)
	if err != nil {
		s.repoError(w, r, err)
		return
	}
	if snaps == nil {
		snaps = []backup.Snapshot{}
	}
	writeJSON(w, http.StatusOK, snaps)
}

type manifestView struct {
	backup.Snapshot
	Host      string `json:"host"`
	Version   string `json:"version"`
	Size      int64  `json:"size"`
	Chunks    int    `json:"chunks"`
	NewChunks int    `json:"newChunks"`
	// Restorable is false when the backup is from a newer FleetWarden version.
	Restorable bool `json:"restorable"`
}

func (s *Server) handleSnapshotDetails(w http.ResponseWriter, r *http.Request) {
	var req snapshotSource
	if !decode(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	repo, done, err := s.openRepoFor(ctx, req.LocationID, req.Location, req.Passphrase)
	if err != nil {
		s.repoError(w, r, err)
		return
	}
	defer done()
	m, err := repo.Manifest(ctx, req.Chain, req.Snapshot)
	if err != nil {
		s.repoError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, manifestView{Snapshot: m.Snapshot, Host: m.Host, Version: m.Version, Size: m.Size,
		Chunks: len(m.Chunks), NewChunks: m.NewChunks, Restorable: restorableVersion(m.Version)})
}
