package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---- patch policies ----

// Approval modes per update category.
const (
	ApproveAuto   = "auto"   // approved automatically after DelayDays
	ApproveManual = "manual" // only when approved by hand
	ApproveNever  = "never"  // never installed (unless approved by hand)
)

// Approval is the rule for one update category.
type Approval struct {
	Mode      string `json:"mode"`
	DelayDays int    `json:"delayDays"`
}

// PatchPolicy says when and how updates are installed on the devices of the
// groups it is assigned to.
type PatchPolicy struct {
	ID               int64               `json:"id"`
	Name             string              `json:"name"`
	Description      string              `json:"description"`
	Enabled          bool                `json:"enabled"`
	WindowDays       int                 `json:"windowDays"` // bit 0 = Sunday
	WindowStart      int                 `json:"windowStart"`
	WindowMinutes    int                 `json:"windowMinutes"`
	TimeZone         string              `json:"timeZone"`
	Approvals        map[string]Approval `json:"approvals"`
	Reboot           string              `json:"reboot"`
	NotifyUsers      bool                `json:"notifyUsers"`
	MaxPostpones     int                 `json:"maxPostpones"`
	PostponeMinutes  int                 `json:"postponeMinutes"`
	CountdownSeconds int                 `json:"countdownSeconds"`
	TakeOver         bool                `json:"takeOver"`
	Drivers          bool                `json:"drivers"`  // install repository drivers in the window
	Firmware         bool                `json:"firmware"` // install repository firmware (BIOS/UEFI) in the window
	ScanHours        int                 `json:"scanHours"`
	CreatedAt        time.Time           `json:"createdAt"`
	UpdatedAt        time.Time           `json:"updatedAt"`
	Groups           []GroupRef          `json:"groups"`
}

// GroupRef names a device group.
type GroupRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

const policyCols = `p.id, p.name, p.description, p.enabled, p.window_days, p.window_start, p.window_minutes, p.time_zone,
	p.approvals, p.reboot, p.notify_users, p.max_postpones, p.postpone_minutes, p.countdown_seconds, p.take_over,
	p.scan_hours, p.drivers, p.firmware, p.created_at, p.updated_at,
	COALESCE((SELECT json_agg(json_build_object('id', g.id, 'name', g.name) ORDER BY lower(g.name))
		FROM device_groups g WHERE g.patch_policy_id = p.id), '[]')`

func scanPolicy(row pgx.Row) (PatchPolicy, error) {
	var p PatchPolicy
	var days, start, mins, postpones, postponeMins, countdown, scan int16
	var approvals, groups []byte
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.Enabled, &days, &start, &mins, &p.TimeZone, &approvals, &p.Reboot,
		&p.NotifyUsers, &postpones, &postponeMins, &countdown, &p.TakeOver, &scan, &p.Drivers, &p.Firmware, &p.CreatedAt, &p.UpdatedAt, &groups)
	if err != nil {
		return p, mapErr(err)
	}
	p.WindowDays, p.WindowStart, p.WindowMinutes = int(days), int(start), int(mins)
	p.MaxPostpones, p.PostponeMinutes, p.CountdownSeconds, p.ScanHours = int(postpones), int(postponeMins), int(countdown), int(scan)
	p.Approvals = map[string]Approval{}
	_ = json.Unmarshal(approvals, &p.Approvals)
	p.Groups = []GroupRef{}
	_ = json.Unmarshal(groups, &p.Groups)
	return p, nil
}

// ListPatchPolicies returns every policy, ordered by name.
func (s *Store) ListPatchPolicies(ctx context.Context) ([]PatchPolicy, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+policyCols+" FROM patch_policies p ORDER BY lower(p.name)")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PatchPolicy{}
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PatchPolicyByID returns a policy.
func (s *Store) PatchPolicyByID(ctx context.Context, id int64) (PatchPolicy, error) {
	return scanPolicy(s.pool.QueryRow(ctx, "SELECT "+policyCols+" FROM patch_policies p WHERE p.id = $1", id))
}

func policyArgs(p PatchPolicy) []any {
	approvals, _ := json.Marshal(p.Approvals)
	return []any{p.Name, p.Description, p.Enabled, int16(p.WindowDays), int16(p.WindowStart), int16(p.WindowMinutes), p.TimeZone,
		approvals, p.Reboot, p.NotifyUsers, int16(p.MaxPostpones), int16(p.PostponeMinutes), int16(p.CountdownSeconds), p.TakeOver,
		int16(p.ScanHours), p.Drivers, p.Firmware}
}

// CreatePatchPolicy inserts a policy.
func (s *Store) CreatePatchPolicy(ctx context.Context, p PatchPolicy) (PatchPolicy, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO patch_policies (name, description, enabled, window_days, window_start, window_minutes,
		time_zone, approvals, reboot, notify_users, max_postpones, postpone_minutes, countdown_seconds, take_over, scan_hours,
		drivers, firmware) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17) RETURNING id`, policyArgs(p)...).Scan(&id)
	if err != nil {
		return p, mapErr(err)
	}
	return s.PatchPolicyByID(ctx, id)
}

// UpdatePatchPolicy changes a policy.
func (s *Store) UpdatePatchPolicy(ctx context.Context, p PatchPolicy) error {
	tag, err := s.pool.Exec(ctx, `UPDATE patch_policies SET name = $2, description = $3, enabled = $4, window_days = $5,
		window_start = $6, window_minutes = $7, time_zone = $8, approvals = $9, reboot = $10, notify_users = $11,
		max_postpones = $12, postpone_minutes = $13, countdown_seconds = $14, take_over = $15, scan_hours = $16,
		drivers = $17, firmware = $18, updated_at = now() WHERE id = $1`, append([]any{p.ID}, policyArgs(p)...)...)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return mapErr(err)
}

// DeletePatchPolicy removes a policy; its groups are no longer patched.
func (s *Store) DeletePatchPolicy(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM patch_policies WHERE id = $1", id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ---- devices and scans ----

// PatchDevice is what the patch scheduler needs to know about a device.
type PatchDevice struct {
	ID               string
	Hostname         string
	Platform         string
	GroupID          int64
	PolicyID         *int64
	InventoryMinutes *int // group overrides; nil uses the default
	PatchScanMinutes *int
	ScanAt           *time.Time
	ScanRequestedAt  *time.Time
	Cluster          string
	RebootPending    bool
	LastWindowRunAt  *time.Time
	OpenRun          *int64
	OpenRunUpdatedAt *time.Time
}

// PatchDevices returns the scheduler view of the listed devices.
func (s *Store) PatchDevices(ctx context.Context, ids []string) ([]PatchDevice, error) {
	rows, err := s.pool.Query(ctx, `SELECT d.id::text, d.hostname, d.platform, d.group_id, g.patch_policy_id,
		g.inventory_minutes, g.patch_scan_minutes, d.patch_scan_at, d.patch_scan_requested_at, d.patch_cluster, d.patch_reboot_pending,
		(SELECT max(r.started_at) FROM patch_runs r WHERE r.device_id = d.id AND r.trigger = 'window'),
		o.id, o.updated_at
		FROM devices d JOIN device_groups g ON g.id = d.group_id
		LEFT JOIN LATERAL (SELECT r.id, r.updated_at FROM patch_runs r WHERE r.device_id = d.id AND r.finished_at IS NULL
			ORDER BY r.id DESC LIMIT 1) o ON true
		WHERE d.id::text = ANY($1) ORDER BY d.id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PatchDevice{}
	for rows.Next() {
		var d PatchDevice
		if err := rows.Scan(&d.ID, &d.Hostname, &d.Platform, &d.GroupID, &d.PolicyID, &d.InventoryMinutes, &d.PatchScanMinutes, &d.ScanAt, &d.ScanRequestedAt, &d.Cluster,
			&d.RebootPending, &d.LastWindowRunAt, &d.OpenRun, &d.OpenRunUpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// MarkPatchScanRequested records that a scan was asked for.
func (s *Store) MarkPatchScanRequested(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, "UPDATE devices SET patch_scan_requested_at = now() WHERE id::text = $1", id)
	return err
}

// PatchRow is one missing update of a device.
type PatchRow struct {
	UpdateID       string     `json:"updateId"`
	Title          string     `json:"title"`
	KB             string     `json:"kb"`
	Category       string     `json:"category"`
	Severity       string     `json:"severity"`
	SizeBytes      int64      `json:"sizeBytes"`
	ReleasedAt     *time.Time `json:"releasedAt"`
	CurrentVersion string     `json:"currentVersion"`
	NewVersion     string     `json:"newVersion"`
	Reboot         bool       `json:"reboot"`
	FirstSeenAt    time.Time  `json:"firstSeenAt"`
	Decision       string     `json:"decision"` // approved, declined or ""
	Approved       bool       `json:"approved"` // filled in by the server
	ApprovedFrom   *time.Time `json:"approvedFrom"`
}

// ScanResult is a stored scan.
type ScanResult struct {
	Error         string
	Source        string
	Cluster       string
	RebootPending bool
	Patches       []PatchRow
}

// SaveScanResult replaces the missing updates of a device. A failed scan
// without results keeps the previous list.
func (s *Store) SaveScanResult(ctx context.Context, id string, r ScanResult) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if r.Error == "" || len(r.Patches) > 0 {
		keep := make([]string, 0, len(r.Patches))
		for _, p := range r.Patches {
			keep = append(keep, p.UpdateID)
		}
		if _, err := tx.Exec(ctx, "DELETE FROM device_patches WHERE device_id::text = $1 AND NOT (update_id = ANY($2))", id, keep); err != nil {
			return err
		}
		b := &pgx.Batch{}
		for _, p := range r.Patches {
			b.Queue(`INSERT INTO device_patches (device_id, update_id, title, kb, category, severity, size_bytes, released_at,
				current_version, new_version, reboot) VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
				ON CONFLICT (device_id, update_id) DO UPDATE SET title = $3, kb = $4, category = $5, severity = $6, size_bytes = $7,
				released_at = $8, current_version = $9, new_version = $10, reboot = $11`,
				id, p.UpdateID, p.Title, p.KB, p.Category, p.Severity, p.SizeBytes, p.ReleasedAt, p.CurrentVersion, p.NewVersion, p.Reboot)
		}
		if err := tx.SendBatch(ctx, b).Close(); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE devices SET patch_scan_at = now(), patch_scan_requested_at = NULL, patch_scan_error = $2,
		patch_source = $3, patch_cluster = $4, patch_reboot_pending = $5 WHERE id::text = $1`,
		id, r.Error, r.Source, r.Cluster, r.RebootPending); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DevicePatches returns the missing updates of a device with manual decisions.
func (s *Store) DevicePatches(ctx context.Context, id string) ([]PatchRow, error) {
	rows, err := s.pool.Query(ctx, `SELECT p.update_id, p.title, p.kb, p.category, p.severity, p.size_bytes, p.released_at,
		p.current_version, p.new_version, p.reboot, p.first_seen_at, COALESCE(x.decision, '')
		FROM device_patches p LEFT JOIN patch_decisions x ON x.update_id = p.update_id
		WHERE p.device_id::text = $1 ORDER BY array_position(ARRAY['security', 'critical', 'cumulative', 'definition', 'driver',
		'feature', 'other'], p.category), lower(p.title)`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PatchRow{}
	for rows.Next() {
		var p PatchRow
		if err := rows.Scan(&p.UpdateID, &p.Title, &p.KB, &p.Category, &p.Severity, &p.SizeBytes, &p.ReleasedAt, &p.CurrentVersion,
			&p.NewVersion, &p.Reboot, &p.FirstSeenAt, &p.Decision); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ClusterBusy reports whether another node of a Proxmox cluster has an open patch job.
func (s *Store) ClusterBusy(ctx context.Context, cluster, exceptDevice string) (bool, error) {
	var busy bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM patch_runs r JOIN devices d ON d.id = r.device_id
		WHERE r.finished_at IS NULL AND d.patch_cluster = $1 AND d.id::text <> $2)`, cluster, exceptDevice).Scan(&busy)
	return busy, err
}

// ---- decisions ----

// SetPatchDecision approves or declines an update for every device; an
// empty decision removes it.
func (s *Store) SetPatchDecision(ctx context.Context, updateID, decision, by string) error {
	if decision == "" {
		_, err := s.pool.Exec(ctx, "DELETE FROM patch_decisions WHERE update_id = $1", updateID)
		return err
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO patch_decisions (update_id, decision, decided_by) VALUES ($1, $2, $3)
		ON CONFLICT (update_id) DO UPDATE SET decision = $2, decided_by = $3, decided_at = now()`, updateID, decision, by)
	return err
}

// ---- runs ----

// PatchRun is one install job on a device.
type PatchRun struct {
	ID             int64           `json:"id"`
	DeviceID       string          `json:"deviceId"`
	Hostname       string          `json:"hostname"`
	PolicyID       *int64          `json:"policyId"`
	Trigger        string          `json:"trigger"`
	State          string          `json:"state"`
	Message        string          `json:"message"`
	Updates        []string        `json:"updates"`
	Results        json.RawMessage `json:"results"`
	Installed      int             `json:"installed"`
	Failed         int             `json:"failed"`
	RebootRequired bool            `json:"rebootRequired"`
	Postpones      int             `json:"postpones"`
	PostponedUntil *time.Time      `json:"postponedUntil"`
	RequestedBy    string          `json:"requestedBy"`
	StartedAt      time.Time       `json:"startedAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	FinishedAt     *time.Time      `json:"finishedAt"`
}

const patchRunCols = `r.id, r.device_id::text, d.hostname, r.policy_id, r.trigger, r.state, r.message, r.updates, r.results, r.installed,
	r.failed, r.reboot_required, r.postpones, r.postponed_until, r.requested_by, r.started_at, r.updated_at, r.finished_at`

func scanPatchRun(row pgx.Row) (PatchRun, error) {
	var r PatchRun
	err := row.Scan(&r.ID, &r.DeviceID, &r.Hostname, &r.PolicyID, &r.Trigger, &r.State, &r.Message, &r.Updates, &r.Results,
		&r.Installed, &r.Failed, &r.RebootRequired, &r.Postpones, &r.PostponedUntil, &r.RequestedBy, &r.StartedAt, &r.UpdatedAt, &r.FinishedAt)
	return r, mapErr(err)
}

// CreatePatchRun starts a job; it returns ErrConflict when the device already has an open job.
func (s *Store) CreatePatchRun(ctx context.Context, r PatchRun) (PatchRun, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return r, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	// Serialise per device so two ticks or clicks cannot start two jobs.
	if _, err := tx.Exec(ctx, "SELECT 1 FROM devices WHERE id::text = $1 FOR UPDATE", r.DeviceID); err != nil {
		return r, err
	}
	var open bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM patch_runs WHERE device_id::text = $1 AND finished_at IS NULL)", r.DeviceID).Scan(&open); err != nil {
		return r, err
	}
	if open {
		return r, ErrConflict
	}
	if r.Updates == nil {
		r.Updates = []string{}
	}
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO patch_runs (device_id, policy_id, trigger, updates, requested_by)
		VALUES ($1::uuid, $2, $3, $4, $5) RETURNING id`, r.DeviceID, r.PolicyID, r.Trigger, r.Updates, r.RequestedBy).Scan(&id); err != nil {
		return r, err
	}
	if err := tx.Commit(ctx); err != nil {
		return r, err
	}
	return s.PatchRunByID(ctx, id)
}

// PatchRunByID returns a job.
func (s *Store) PatchRunByID(ctx context.Context, id int64) (PatchRun, error) {
	return scanPatchRun(s.pool.QueryRow(ctx, "SELECT "+patchRunCols+" FROM patch_runs r JOIN devices d ON d.id = r.device_id WHERE r.id = $1", id))
}

// PatchRunUpdate is a status report of a job.
type PatchRunUpdate struct {
	State          string
	Message        string
	Results        []byte
	Installed      int
	Failed         int
	RebootRequired bool
	Postpones      int
	PostponedUntil *time.Time
	Finished       bool
}

// UpdatePatchRun records a status of an open job of the device. Reports for
// finished jobs are ignored (ErrNotFound).
func (s *Store) UpdatePatchRun(ctx context.Context, id int64, deviceID string, u PatchRunUpdate) error {
	if u.Results == nil {
		u.Results = []byte("[]")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE patch_runs SET state = $3, message = $4,
		results = CASE WHEN $5::jsonb = '[]'::jsonb THEN results ELSE $5::jsonb END,
		installed = CASE WHEN $5::jsonb = '[]'::jsonb THEN installed ELSE $6 END,
		failed = CASE WHEN $5::jsonb = '[]'::jsonb THEN failed ELSE $7 END,
		reboot_required = $8, postpones = GREATEST(postpones, $9), postponed_until = $10, updated_at = now(),
		finished_at = CASE WHEN $11 THEN now() END
		WHERE id = $1 AND device_id::text = $2 AND finished_at IS NULL`,
		id, deviceID, u.State, u.Message, u.Results, u.Installed, u.Failed, u.RebootRequired, u.Postpones, u.PostponedUntil, u.Finished)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if u.Finished {
		_, err = s.pool.Exec(ctx, "UPDATE devices SET patch_reboot_pending = $2 WHERE id::text = $1", deviceID, u.RebootRequired)
	}
	return err
}

// FailPatchRun closes an open job as failed.
func (s *Store) FailPatchRun(ctx context.Context, id int64, message string) error {
	_, err := s.pool.Exec(ctx, `UPDATE patch_runs SET state = 'failed', message = $2, updated_at = now(), finished_at = now()
		WHERE id = $1 AND finished_at IS NULL`, id, message)
	return err
}

// RunQuery filters the job history.
type RunQuery struct {
	Scope    Scope
	DeviceID string
	Limit    int
}

// ListPatchRuns returns jobs, newest first.
func (s *Store) ListPatchRuns(ctx context.Context, q RunQuery) ([]PatchRun, error) {
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, "SELECT "+patchRunCols+` FROM patch_runs r JOIN devices d ON d.id = r.device_id
		WHERE ($1 OR d.group_id = ANY($2)) AND ($3 = '' OR d.id::text = $3) ORDER BY r.id DESC LIMIT $4`,
		q.Scope.All, q.Scope.groups(), q.DeviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PatchRun{}
	for rows.Next() {
		r, err := scanPatchRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PurgePatchRuns removes finished jobs older than the cut-off.
func (s *Store) PurgePatchRuns(ctx context.Context, olderThan time.Duration) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM patch_runs WHERE finished_at < $1", time.Now().Add(-olderThan))
	return err
}

// ---- overview ----

// UpdateSummary is one update across devices.
type UpdateSummary struct {
	UpdateID   string     `json:"updateId"`
	Title      string     `json:"title"`
	KB         string     `json:"kb"`
	Category   string     `json:"category"`
	Severity   string     `json:"severity"`
	Platform   string     `json:"platform"`
	ReleasedAt *time.Time `json:"releasedAt"`
	FirstSeen  time.Time  `json:"firstSeenAt"`
	Devices    int        `json:"devices"`
	Decision   string     `json:"decision"`
	DecidedBy  string     `json:"decidedBy"`
}

// UpdateQuery filters the update overview.
type UpdateQuery struct {
	Scope    Scope
	Search   string
	Category string
	Platform string
	Decision string // approved, declined, none
}

// ListUpdates aggregates the missing updates of devices within a scope.
func (s *Store) ListUpdates(ctx context.Context, q UpdateQuery) ([]UpdateSummary, error) {
	where := []string{"($1 OR d.group_id = ANY($2))"}
	args := []any{q.Scope.All, q.Scope.groups()}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+itoa(len(args))))
	}
	if q.Search != "" {
		add("(p.title ILIKE ? OR p.kb ILIKE ? OR p.update_id ILIKE ?)", "%"+likeEscape(q.Search)+"%")
	}
	if q.Category != "" {
		add("p.category = ?", q.Category)
	}
	if q.Platform != "" {
		add("d.platform = ?", q.Platform)
	}
	switch q.Decision {
	case "approved", "declined":
		add("x.decision = ?", q.Decision)
	case "none":
		where = append(where, "x.decision IS NULL")
	}
	rows, err := s.pool.Query(ctx, `SELECT p.update_id, max(p.title), max(p.kb), max(p.category), max(p.severity), min(d.platform),
		max(p.released_at), min(p.first_seen_at), count(*)::int, COALESCE(max(x.decision), ''), COALESCE(max(x.decided_by), '')
		FROM device_patches p JOIN devices d ON d.id = p.device_id LEFT JOIN patch_decisions x ON x.update_id = p.update_id
		WHERE `+strings.Join(where, " AND ")+` GROUP BY p.update_id ORDER BY count(*) DESC, max(p.title) LIMIT 2000`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UpdateSummary{}
	for rows.Next() {
		var u UpdateSummary
		if err := rows.Scan(&u.UpdateID, &u.Title, &u.KB, &u.Category, &u.Severity, &u.Platform, &u.ReleasedAt, &u.FirstSeen,
			&u.Devices, &u.Decision, &u.DecidedBy); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// PatchStats summarises patch status within a scope.
type PatchStats struct {
	Devices       int `json:"devices"`       // devices in the scope
	Managed       int `json:"managed"`       // in a group with an enabled policy
	Scanned       int `json:"scanned"`       // with a scan result
	UpToDate      int `json:"upToDate"`      // scanned, nothing missing
	Missing       int `json:"missing"`       // devices with missing updates
	Security      int `json:"security"`      // devices missing security updates
	RebootPending int `json:"rebootPending"` // devices waiting for a restart
	ScanErrors    int `json:"scanErrors"`
	RunsOpen      int `json:"runsOpen"`
	RunsFailed    int `json:"runsFailed"` // failed jobs in the last 7 days
}

// PatchStats counts patch status within a scope.
func (s *Store) PatchStats(ctx context.Context, sc Scope) (PatchStats, error) {
	var st PatchStats
	err := s.pool.QueryRow(ctx, `SELECT count(*)::int,
		count(*) FILTER (WHERE pp.enabled)::int,
		count(*) FILTER (WHERE d.patch_scan_at IS NOT NULL)::int,
		count(*) FILTER (WHERE d.patch_scan_at IS NOT NULL AND NOT EXISTS (SELECT 1 FROM device_patches p WHERE p.device_id = d.id))::int,
		count(*) FILTER (WHERE EXISTS (SELECT 1 FROM device_patches p WHERE p.device_id = d.id))::int,
		count(*) FILTER (WHERE EXISTS (SELECT 1 FROM device_patches p WHERE p.device_id = d.id AND p.category = 'security'))::int,
		count(*) FILTER (WHERE d.patch_reboot_pending)::int,
		count(*) FILTER (WHERE d.patch_scan_error <> '')::int,
		(SELECT count(*) FROM patch_runs r JOIN devices e ON e.id = r.device_id WHERE r.finished_at IS NULL AND ($1 OR e.group_id = ANY($2)))::int,
		(SELECT count(*) FROM patch_runs r JOIN devices e ON e.id = r.device_id WHERE r.state = 'failed'
			AND r.finished_at > now() - interval '7 days' AND ($1 OR e.group_id = ANY($2)))::int
		FROM devices d JOIN device_groups g ON g.id = d.group_id LEFT JOIN patch_policies pp ON pp.id = g.patch_policy_id
		WHERE $1 OR d.group_id = ANY($2)`, sc.All, sc.groups()).Scan(&st.Devices, &st.Managed, &st.Scanned, &st.UpToDate, &st.Missing,
		&st.Security, &st.RebootPending, &st.ScanErrors, &st.RunsOpen, &st.RunsFailed)
	return st, err
}

// PatchDecisions returns every manual decision by update ID.
func (s *Store) PatchDecisions(ctx context.Context) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, "SELECT update_id, decision FROM patch_decisions")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, d string
		if err := rows.Scan(&id, &d); err != nil {
			return nil, err
		}
		out[id] = d
	}
	return out, rows.Err()
}
