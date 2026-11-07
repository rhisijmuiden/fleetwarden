package store

import (
	"context"
	"time"
)

// Rollout states.
const (
	RolloutActive     = "active"
	RolloutPaused     = "paused"
	RolloutCompleted  = "completed"
	RolloutCancelled  = "cancelled"
	RolloutSuperseded = "superseded"
)

// Rollout sends one agent version to the device groups ring by ring.
type Rollout struct {
	ID            int64     `json:"id"`
	Version       string    `json:"version"`
	Channel       string    `json:"channel"`
	Status        string    `json:"status"`
	Ring          int       `json:"ring"`
	RingStartedAt time.Time `json:"ringStartedAt"`
	SoakHours     int       `json:"soakHours"`
	MaxFailures   int       `json:"maxFailures"`
	Automatic     bool      `json:"automatic"`
	Message       string    `json:"message"`
	CreatedBy     string    `json:"createdBy"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// Open reports whether the rollout still sends updates or may resume.
func (r Rollout) Open() bool { return r.Status == RolloutActive || r.Status == RolloutPaused }

const rolloutCols = `id, version, channel, status, ring, ring_started_at, soak_hours, max_failures, automatic, message,
	created_by, created_at, updated_at`

func scanRollout(row interface{ Scan(...any) error }) (Rollout, error) {
	var r Rollout
	err := row.Scan(&r.ID, &r.Version, &r.Channel, &r.Status, &r.Ring, &r.RingStartedAt, &r.SoakHours, &r.MaxFailures,
		&r.Automatic, &r.Message, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt)
	return r, mapErr(err)
}

// CreateRollout starts a rollout; open rollouts of the same channel are superseded.
func (s *Store) CreateRollout(ctx context.Context, r Rollout) (Rollout, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return r, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `UPDATE agent_rollouts SET status = 'superseded', message = 'replaced by ' || $2, updated_at = now()
		WHERE channel = $1 AND status IN ('active', 'paused')`, r.Channel, r.Version); err != nil {
		return r, err
	}
	out, err := scanRollout(tx.QueryRow(ctx, `INSERT INTO agent_rollouts (version, channel, soak_hours, max_failures, automatic, created_by)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+rolloutCols, r.Version, r.Channel, r.SoakHours, r.MaxFailures, r.Automatic, r.CreatedBy))
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

// RolloutByID returns a rollout.
func (s *Store) RolloutByID(ctx context.Context, id int64) (Rollout, error) {
	return scanRollout(s.pool.QueryRow(ctx, "SELECT "+rolloutCols+" FROM agent_rollouts WHERE id = $1", id))
}

// ListRollouts returns the newest rollouts first.
func (s *Store) ListRollouts(ctx context.Context, limit int) ([]Rollout, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+rolloutCols+" FROM agent_rollouts ORDER BY id DESC LIMIT $1", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rollout{}
	for rows.Next() {
		r, err := scanRollout(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveRolloutState stores the status, ring and message of a rollout.
func (s *Store) SaveRolloutState(ctx context.Context, r Rollout) error {
	_, err := s.pool.Exec(ctx, `UPDATE agent_rollouts SET status = $2, ring = $3, ring_started_at = $4, message = $5, updated_at = now()
		WHERE id = $1`, r.ID, r.Status, r.Ring, r.RingStartedAt, r.Message)
	return err
}

// DeviceUpdate is the update state of one device.
type DeviceUpdate struct {
	DeviceID     string     `json:"deviceId"`
	Hostname     string     `json:"hostname"`
	GroupID      int64      `json:"groupId"`
	Platform     string     `json:"platform"`
	Arch         string     `json:"arch"`
	AgentVersion string     `json:"agentVersion"`
	Rollout      *int64     `json:"rollout"`
	Version      string     `json:"version"`
	State        string     `json:"state"`
	Message      string     `json:"message"`
	At           *time.Time `json:"at"`
	Attempts     int        `json:"attempts"`
	LastSeenAt   time.Time  `json:"lastSeenAt"`
}

// DeviceUpdates returns the update state of every device.
func (s *Store) DeviceUpdates(ctx context.Context) ([]DeviceUpdate, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text, hostname, group_id, platform, arch, agent_version, update_rollout, update_version,
		update_state, update_message, update_at, update_attempts, last_seen_at FROM devices`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeviceUpdate{}
	for rows.Next() {
		var d DeviceUpdate
		if err := rows.Scan(&d.DeviceID, &d.Hostname, &d.GroupID, &d.Platform, &d.Arch, &d.AgentVersion, &d.Rollout, &d.Version,
			&d.State, &d.Message, &d.At, &d.Attempts, &d.LastSeenAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeviceUpdateByID returns the update state of one device.
func (s *Store) DeviceUpdateByID(ctx context.Context, id string) (DeviceUpdate, error) {
	var d DeviceUpdate
	err := s.pool.QueryRow(ctx, `SELECT id::text, hostname, group_id, platform, arch, agent_version, update_rollout, update_version,
		update_state, update_message, update_at, update_attempts, last_seen_at FROM devices WHERE id::text = $1`, id).Scan(
		&d.DeviceID, &d.Hostname, &d.GroupID, &d.Platform, &d.Arch, &d.AgentVersion, &d.Rollout, &d.Version,
		&d.State, &d.Message, &d.At, &d.Attempts, &d.LastSeenAt)
	return d, mapErr(err)
}

// StartDeviceUpdate records that an update was sent. Attempts count up while
// the same version is retried.
func (s *Store) StartDeviceUpdate(ctx context.Context, id string, rollout int64, version string) error {
	_, err := s.pool.Exec(ctx, `UPDATE devices SET update_rollout = $2, update_state = 'sent', update_message = '', update_at = now(),
		update_attempts = CASE WHEN update_version = $3 THEN update_attempts + 1 ELSE 1 END, update_version = $3
		WHERE id::text = $1`, id, rollout, version)
	return err
}

// SetDeviceUpdateState records progress reported by the agent for version.
func (s *Store) SetDeviceUpdateState(ctx context.Context, id, version, state, message string) error {
	_, err := s.pool.Exec(ctx, `UPDATE devices SET update_state = $3, update_message = $4, update_at = now()
		WHERE id::text = $1 AND update_version = $2`, id, version, state, message)
	return err
}

// ResetFailedUpdates lets failed devices of a rollout try again.
func (s *Store) ResetFailedUpdates(ctx context.Context, rollout int64) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE devices SET update_state = '', update_message = '', update_attempts = 0
		WHERE update_rollout = $1 AND update_state IN ('failed', 'rolled-back')`, rollout)
	return tag.RowsAffected(), err
}

// TimeOutUpdates marks updates that made no progress since before as failed.
func (s *Store) TimeOutUpdates(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE devices SET update_state = 'failed', update_message = 'no answer from the agent', update_at = now()
		WHERE update_state IN ('sent', 'downloading', 'installing') AND update_at < $1`, before)
	return tag.RowsAffected(), err
}

// CompleteDeviceUpdate marks the update done once the device runs its version.
func (s *Store) CompleteDeviceUpdate(ctx context.Context, id, agentVersion string) error {
	_, err := s.pool.Exec(ctx, `UPDATE devices SET update_state = 'completed', update_message = '', update_at = now()
		WHERE id::text = $1 AND update_version = $2 AND update_version <> '' AND update_state <> 'completed'`, id, agentVersion)
	return err
}
