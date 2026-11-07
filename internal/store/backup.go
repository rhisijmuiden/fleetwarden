package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// BackupLocation is a configured backup destination. Config holds the
// location settings as JSON with secrets encrypted.
type BackupLocation struct {
	ID        int64           `json:"id"`
	Name      string          `json:"name"`
	Type      string          `json:"type"`
	Enabled   bool            `json:"enabled"`
	Config    json.RawMessage `json:"-"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

const locationCols = "id, name, type, enabled, config, created_at, updated_at"

func scanLocation(row pgx.Row) (BackupLocation, error) {
	var l BackupLocation
	err := row.Scan(&l.ID, &l.Name, &l.Type, &l.Enabled, &l.Config, &l.CreatedAt, &l.UpdatedAt)
	return l, mapErr(err)
}

// ListBackupLocations returns all locations by name.
func (s *Store) ListBackupLocations(ctx context.Context) ([]BackupLocation, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+locationCols+" FROM backup_locations ORDER BY lower(name)")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BackupLocation{}
	for rows.Next() {
		l, err := scanLocation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// BackupLocation returns one location.
func (s *Store) BackupLocation(ctx context.Context, id int64) (BackupLocation, error) {
	return scanLocation(s.pool.QueryRow(ctx, "SELECT "+locationCols+" FROM backup_locations WHERE id = $1", id))
}

// CreateBackupLocation inserts a location.
func (s *Store) CreateBackupLocation(ctx context.Context, l BackupLocation) (BackupLocation, error) {
	return scanLocation(s.pool.QueryRow(ctx, `INSERT INTO backup_locations (name, type, enabled, config)
		VALUES ($1, $2, $3, $4) RETURNING `+locationCols, l.Name, l.Type, l.Enabled, l.Config))
}

// UpdateBackupLocation replaces a location's settings.
func (s *Store) UpdateBackupLocation(ctx context.Context, l BackupLocation) (BackupLocation, error) {
	return scanLocation(s.pool.QueryRow(ctx, `UPDATE backup_locations SET name = $2, type = $3, enabled = $4, config = $5,
		updated_at = now() WHERE id = $1 RETURNING `+locationCols, l.ID, l.Name, l.Type, l.Enabled, l.Config))
}

// DeleteBackupLocation removes a location; its history stays.
func (s *Store) DeleteBackupLocation(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM backup_locations WHERE id = $1", id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// BackupRun is one backup to one location.
type BackupRun struct {
	ID            int64      `json:"id"`
	LocationID    *int64     `json:"locationId"`
	LocationName  string     `json:"locationName"`
	Trigger       string     `json:"trigger"`
	Actor         string     `json:"actor"`
	Kind          string     `json:"kind"`
	Chain         string     `json:"chain"`
	Snapshot      string     `json:"snapshot"`
	Status        string     `json:"status"`
	StartedAt     time.Time  `json:"startedAt"`
	FinishedAt    *time.Time `json:"finishedAt"`
	SizeBytes     int64      `json:"sizeBytes"`
	UploadedBytes int64      `json:"uploadedBytes"`
	Error         string     `json:"error"`
}

const runCols = "id, location_id, location_name, trigger, actor, kind, chain, snapshot, status, started_at, finished_at, size_bytes, uploaded_bytes, error"

func scanRun(row pgx.Row) (BackupRun, error) {
	var r BackupRun
	err := row.Scan(&r.ID, &r.LocationID, &r.LocationName, &r.Trigger, &r.Actor, &r.Kind, &r.Chain, &r.Snapshot, &r.Status,
		&r.StartedAt, &r.FinishedAt, &r.SizeBytes, &r.UploadedBytes, &r.Error)
	return r, mapErr(err)
}

// StartBackupRun records a running backup.
func (s *Store) StartBackupRun(ctx context.Context, r BackupRun) (BackupRun, error) {
	return scanRun(s.pool.QueryRow(ctx, `INSERT INTO backup_runs (location_id, location_name, trigger, actor, kind, status)
		VALUES ($1, $2, $3, $4, $5, 'running') RETURNING `+runCols, r.LocationID, r.LocationName, r.Trigger, r.Actor, r.Kind))
}

// FinishBackupRun stores the outcome of a run.
func (s *Store) FinishBackupRun(ctx context.Context, r BackupRun) error {
	_, err := s.pool.Exec(ctx, `UPDATE backup_runs SET status = $2, kind = $3, chain = $4, snapshot = $5, size_bytes = $6,
		uploaded_bytes = $7, error = $8, finished_at = now() WHERE id = $1`,
		r.ID, r.Status, r.Kind, r.Chain, r.Snapshot, r.SizeBytes, r.UploadedBytes, r.Error)
	return err
}

// ListBackupRuns returns the newest runs.
func (s *Store) ListBackupRuns(ctx context.Context, limit int) ([]BackupRun, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+runCols+" FROM backup_runs ORDER BY started_at DESC, id DESC LIMIT $1", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BackupRun{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LastBackupRuns returns the newest finished run per location.
func (s *Store) LastBackupRuns(ctx context.Context) (map[int64]BackupRun, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT ON (location_id) `+runCols+` FROM backup_runs
		WHERE location_id IS NOT NULL AND status <> 'running' ORDER BY location_id, started_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]BackupRun{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out[*r.LocationID] = r
	}
	return out, rows.Err()
}

// FailInterruptedBackupRuns marks runs that were running when the server
// stopped as failed.
func (s *Store) FailInterruptedBackupRuns(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `UPDATE backup_runs SET status = 'failed', error = 'interrupted: the server stopped during the backup',
		finished_at = now() WHERE status = 'running'`)
	return err
}

// PurgeBackupRuns removes history older than age.
func (s *Store) PurgeBackupRuns(ctx context.Context, age time.Duration) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM backup_runs WHERE started_at < $1", time.Now().Add(-age))
	return err
}

// Recipient is an administrator who receives system e-mail.
type Recipient struct {
	Email    string
	Language string
}

// AdminRecipients returns enabled administrators with an e-mail address.
func (s *Store) AdminRecipients(ctx context.Context) ([]Recipient, error) {
	rows, err := s.pool.Query(ctx, "SELECT email, language FROM users WHERE role = 'administrator' AND NOT disabled AND email <> ''")
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Recipient, error) {
		var r Recipient
		err := row.Scan(&r.Email, &r.Language)
		return r, err
	})
}
