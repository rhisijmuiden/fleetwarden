package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---- report data ----

// ReportDevice is what the reports need to know about a device.
type ReportDevice struct {
	ID            string
	Hostname      string
	GroupID       int64
	GroupName     string
	Platform      string
	Kind          string
	OSName        string
	LastSeenAt    time.Time
	PatchScanAt   *time.Time
	RebootPending bool
	Volumes       []ReportVolume
	Missing       int // missing updates
	Security      int // missing security or critical updates
	SecurityOld   int // of those, missing for longer than the grace period
}

// ReportVolume is a file system with its free space.
type ReportVolume struct {
	Mount     string `json:"mount"`
	SizeBytes int64  `json:"sizeBytes"`
	FreeBytes int64  `json:"freeBytes"`
}

// ReportDevices returns the devices within a scope (and, when groups is
// not empty, in those groups), ordered by host name. Security updates count
// as old when they were released or first seen before graceBefore.
func (s *Store) ReportDevices(ctx context.Context, sc Scope, groups []int64, graceBefore time.Time) ([]ReportDevice, error) {
	if groups == nil {
		groups = []int64{}
	}
	rows, err := s.pool.Query(ctx, `SELECT d.id::text, d.hostname, d.group_id, g.name, d.platform, d.kind, d.os_name, d.last_seen_at,
		d.patch_scan_at, d.patch_reboot_pending, COALESCE(d.hardware->'volumes', '[]'::jsonb),
		COALESCE(p.missing, 0), COALESCE(p.security, 0), COALESCE(p.security_old, 0)
		FROM devices d JOIN device_groups g ON g.id = d.group_id
		LEFT JOIN LATERAL (SELECT count(*)::int AS missing,
			count(*) FILTER (WHERE x.category IN ('security', 'critical'))::int AS security,
			count(*) FILTER (WHERE x.category IN ('security', 'critical')
				AND LEAST(x.first_seen_at, COALESCE(x.released_at, x.first_seen_at)) < $4)::int AS security_old
			FROM device_patches x LEFT JOIN patch_decisions dec ON dec.update_id = x.update_id
			WHERE x.device_id = d.id AND COALESCE(dec.decision, '') <> 'declined') p ON true
		WHERE ($1 OR d.group_id = ANY($2)) AND (cardinality($3::bigint[]) = 0 OR d.group_id = ANY($3))
		ORDER BY lower(d.hostname), d.id`, sc.All, sc.groups(), groups, graceBefore)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReportDevice{}
	for rows.Next() {
		var d ReportDevice
		var vol []byte
		if err := rows.Scan(&d.ID, &d.Hostname, &d.GroupID, &d.GroupName, &d.Platform, &d.Kind, &d.OSName, &d.LastSeenAt,
			&d.PatchScanAt, &d.RebootPending, &vol, &d.Missing, &d.Security, &d.SecurityOld); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(vol, &d.Volumes)
		out = append(out, d)
	}
	return out, rows.Err()
}

// InstalledProgram is one Windows program on a device.
type InstalledProgram struct {
	DeviceID  string
	Name      string
	Version   string
	Publisher string
}

// WindowsPrograms returns the Windows desktop programs (not Store apps) of
// the listed devices.
func (s *Store) WindowsPrograms(ctx context.Context, ids []string) ([]InstalledProgram, error) {
	rows, err := s.pool.Query(ctx, `SELECT s.device_id::text, s.name, s.version, s.publisher FROM device_software s
		JOIN devices d ON d.id = s.device_id
		WHERE d.platform = 'windows' AND s.source <> 'appx' AND s.device_id::text = ANY($1) ORDER BY lower(s.name)`, ids)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[InstalledProgram])
}

// MissingPatch is one missing update of one device.
type MissingPatch struct {
	DeviceID   string
	UpdateID   string
	Title      string
	KB         string
	Category   string
	Severity   string
	ReleasedAt *time.Time
	FirstSeen  time.Time
}

// MissingPatches returns the missing updates of the listed devices,
// without updates that were declined.
func (s *Store) MissingPatches(ctx context.Context, ids []string) ([]MissingPatch, error) {
	rows, err := s.pool.Query(ctx, `SELECT p.device_id::text, p.update_id, p.title, p.kb, p.category, p.severity, p.released_at, p.first_seen_at
		FROM device_patches p LEFT JOIN patch_decisions x ON x.update_id = p.update_id
		WHERE p.device_id::text = ANY($1) AND COALESCE(x.decision, '') <> 'declined'
		ORDER BY array_position(ARRAY['security', 'critical', 'cumulative', 'definition', 'driver', 'feature', 'other'], p.category),
			lower(p.title)`, ids)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[MissingPatch])
}

// ---- schedules ----

// ReportSchedule mails reports regularly.
type ReportSchedule struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Enabled    bool       `json:"enabled"`
	Reports    []string   `json:"reports"`
	GroupIDs   []int64    `json:"groupIds"`
	Frequency  string     `json:"frequency"`
	Weekday    int        `json:"weekday"`
	MonthDay   int        `json:"monthDay"`
	SendAt     int        `json:"sendAt"` // minutes after midnight
	TimeZone   string     `json:"timeZone"`
	Recipients []string   `json:"recipients"`
	Language   string     `json:"language"`
	Formats    []string   `json:"formats"`
	NextRunAt  *time.Time `json:"nextRunAt"`
	LastRunAt  *time.Time `json:"lastRunAt"`
	LastError  string     `json:"lastError"`
	CreatedBy  string     `json:"createdBy"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

const scheduleCols = `id, name, enabled, reports, group_ids, frequency, weekday, month_day, send_at, time_zone, recipients, language,
	formats, next_run_at, last_run_at, last_error, created_by, created_at, updated_at`

func scanSchedule(row pgx.Row) (ReportSchedule, error) {
	var r ReportSchedule
	var wd, md, at int16
	err := row.Scan(&r.ID, &r.Name, &r.Enabled, &r.Reports, &r.GroupIDs, &r.Frequency, &wd, &md, &at, &r.TimeZone, &r.Recipients,
		&r.Language, &r.Formats, &r.NextRunAt, &r.LastRunAt, &r.LastError, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt)
	r.Weekday, r.MonthDay, r.SendAt = int(wd), int(md), int(at)
	return r, mapErr(err)
}

// ListReportSchedules returns every schedule by name.
func (s *Store) ListReportSchedules(ctx context.Context) ([]ReportSchedule, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+scheduleCols+" FROM report_schedules ORDER BY lower(name)")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReportSchedule{}
	for rows.Next() {
		r, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReportScheduleByID returns a schedule.
func (s *Store) ReportScheduleByID(ctx context.Context, id int64) (ReportSchedule, error) {
	return scanSchedule(s.pool.QueryRow(ctx, "SELECT "+scheduleCols+" FROM report_schedules WHERE id = $1", id))
}

func nonNil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

// CreateReportSchedule stores a new schedule.
func (s *Store) CreateReportSchedule(ctx context.Context, r ReportSchedule) (ReportSchedule, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO report_schedules (name, enabled, reports, group_ids, frequency, weekday, month_day, send_at,
		time_zone, recipients, language, formats, next_run_at, created_by) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id`, strings.TrimSpace(r.Name), r.Enabled, nonNil(r.Reports), nonNil(r.GroupIDs), r.Frequency, int16(r.Weekday),
		int16(r.MonthDay), int16(r.SendAt), r.TimeZone, nonNil(r.Recipients), r.Language, nonNil(r.Formats), r.NextRunAt, r.CreatedBy).Scan(&id)
	if err != nil {
		return r, mapErr(err)
	}
	return s.ReportScheduleByID(ctx, id)
}

// UpdateReportSchedule changes a schedule.
func (s *Store) UpdateReportSchedule(ctx context.Context, r ReportSchedule) error {
	tag, err := s.pool.Exec(ctx, `UPDATE report_schedules SET name = $2, enabled = $3, reports = $4, group_ids = $5, frequency = $6,
		weekday = $7, month_day = $8, send_at = $9, time_zone = $10, recipients = $11, language = $12, formats = $13, next_run_at = $14,
		updated_at = now() WHERE id = $1`, r.ID, strings.TrimSpace(r.Name), r.Enabled, nonNil(r.Reports), nonNil(r.GroupIDs), r.Frequency,
		int16(r.Weekday), int16(r.MonthDay), int16(r.SendAt), r.TimeZone, nonNil(r.Recipients), r.Language, nonNil(r.Formats), r.NextRunAt)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return mapErr(err)
}

// DeleteReportSchedule removes a schedule.
func (s *Store) DeleteReportSchedule(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM report_schedules WHERE id = $1", id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DueReportSchedules returns enabled schedules whose time has come.
func (s *Store) DueReportSchedules(ctx context.Context, now time.Time) ([]ReportSchedule, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+scheduleCols+" FROM report_schedules WHERE enabled AND next_run_at <= $1 ORDER BY next_run_at", now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReportSchedule
	for rows.Next() {
		r, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ClaimReportRun moves a due schedule to its next run, so that only one
// worker sends it. It answers false when another worker was first.
func (s *Store) ClaimReportRun(ctx context.Context, id int64, due, next time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, "UPDATE report_schedules SET next_run_at = $3 WHERE id = $1 AND next_run_at = $2", id, due, next)
	return tag.RowsAffected() == 1, err
}

// FinishReportRun records the outcome of a run.
func (s *Store) FinishReportRun(ctx context.Context, id int64, at time.Time, errMsg string) error {
	_, err := s.pool.Exec(ctx, "UPDATE report_schedules SET last_run_at = $2, last_error = $3 WHERE id = $1", id, at, errMsg)
	return err
}
