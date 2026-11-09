package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---- power schedules ----

// PowerSchedule wakes, restarts or shuts down the devices of groups on set
// days of the week.
type PowerSchedule struct {
	ID                  int64      `json:"id"`
	Name                string     `json:"name"`
	Description         string     `json:"description"`
	Enabled             bool       `json:"enabled"`
	Action              string     `json:"action"` // wake, restart, shutdown
	Days                int        `json:"days"`   // bit 0 = Sunday
	AtMinute            int        `json:"atMinute"`
	TimeZone            string     `json:"timeZone"`
	NotifyUsers         bool       `json:"notifyUsers"`
	MaxPostpones        int        `json:"maxPostpones"`
	PostponeMinutes     int        `json:"postponeMinutes"`
	CountdownSeconds    int        `json:"countdownSeconds"`
	SkipIfUserLoggedOn  bool       `json:"skipIfUserLoggedOn"`
	OnlyIfRebootPending bool       `json:"onlyIfRebootPending"`
	LastRunAt           *time.Time `json:"lastRunAt"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
	Groups              []GroupRef `json:"groups"`
	// GroupIDs is what the web interface sends.
	GroupIDs []int64 `json:"groupIds,omitempty"`
}

const powerScheduleCols = `p.id, p.name, p.description, p.enabled, p.action, p.days, p.at_minute, p.time_zone, p.notify_users,
	p.max_postpones, p.postpone_minutes, p.countdown_seconds, p.skip_if_user_logged_on, p.only_if_reboot_pending, p.last_run_at,
	p.created_at, p.updated_at,
	COALESCE((SELECT json_agg(json_build_object('id', g.id, 'name', g.name) ORDER BY lower(g.name))
		FROM power_schedule_groups sg JOIN device_groups g ON g.id = sg.group_id WHERE sg.schedule_id = p.id), '[]')`

func scanPowerSchedule(row pgx.Row) (PowerSchedule, error) {
	var p PowerSchedule
	var days, at, postpones, postponeMins, countdown int16
	var groups []byte
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.Enabled, &p.Action, &days, &at, &p.TimeZone, &p.NotifyUsers,
		&postpones, &postponeMins, &countdown, &p.SkipIfUserLoggedOn, &p.OnlyIfRebootPending, &p.LastRunAt, &p.CreatedAt, &p.UpdatedAt, &groups)
	if err != nil {
		return p, mapErr(err)
	}
	p.Days, p.AtMinute = int(days), int(at)
	p.MaxPostpones, p.PostponeMinutes, p.CountdownSeconds = int(postpones), int(postponeMins), int(countdown)
	p.Groups = []GroupRef{}
	_ = json.Unmarshal(groups, &p.Groups)
	p.GroupIDs = make([]int64, 0, len(p.Groups))
	for _, g := range p.Groups {
		p.GroupIDs = append(p.GroupIDs, g.ID)
	}
	return p, nil
}

// ListPowerSchedules returns every schedule, ordered by name.
func (s *Store) ListPowerSchedules(ctx context.Context) ([]PowerSchedule, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+powerScheduleCols+" FROM power_schedules p ORDER BY lower(p.name)")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PowerSchedule{}
	for rows.Next() {
		p, err := scanPowerSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PowerScheduleByID returns a schedule.
func (s *Store) PowerScheduleByID(ctx context.Context, id int64) (PowerSchedule, error) {
	return scanPowerSchedule(s.pool.QueryRow(ctx, "SELECT "+powerScheduleCols+" FROM power_schedules p WHERE p.id = $1", id))
}

func powerScheduleArgs(p PowerSchedule) []any {
	return []any{p.Name, p.Description, p.Enabled, p.Action, int16(p.Days), int16(p.AtMinute), p.TimeZone, p.NotifyUsers,
		int16(p.MaxPostpones), int16(p.PostponeMinutes), int16(p.CountdownSeconds), p.SkipIfUserLoggedOn, p.OnlyIfRebootPending}
}

func setScheduleGroups(ctx context.Context, tx pgx.Tx, id int64, groups []int64) error {
	if _, err := tx.Exec(ctx, "DELETE FROM power_schedule_groups WHERE schedule_id = $1", id); err != nil {
		return err
	}
	if len(groups) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO power_schedule_groups (schedule_id, group_id)
		SELECT $1, g.id FROM device_groups g WHERE g.id = ANY($2) ON CONFLICT DO NOTHING`, id, groups)
	return err
}

// CreatePowerSchedule inserts a schedule with its groups. A new schedule
// starts with the current time as its last run, so it does not catch up on
// a slot that just passed.
func (s *Store) CreatePowerSchedule(ctx context.Context, p PowerSchedule) (PowerSchedule, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return p, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO power_schedules (name, description, enabled, action, days, at_minute, time_zone, notify_users,
		max_postpones, postpone_minutes, countdown_seconds, skip_if_user_logged_on, only_if_reboot_pending, last_run_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, now()) RETURNING id`, powerScheduleArgs(p)...).Scan(&id)
	if err != nil {
		return p, mapErr(err)
	}
	if err := setScheduleGroups(ctx, tx, id, p.GroupIDs); err != nil {
		return p, err
	}
	if err := tx.Commit(ctx); err != nil {
		return p, err
	}
	return s.PowerScheduleByID(ctx, id)
}

// UpdatePowerSchedule changes a schedule and its groups.
func (s *Store) UpdatePowerSchedule(ctx context.Context, p PowerSchedule) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	tag, err := tx.Exec(ctx, `UPDATE power_schedules SET name = $2, description = $3, enabled = $4, action = $5, days = $6,
		at_minute = $7, time_zone = $8, notify_users = $9, max_postpones = $10, postpone_minutes = $11, countdown_seconds = $12,
		skip_if_user_logged_on = $13, only_if_reboot_pending = $14,
		last_run_at = CASE WHEN days <> $6 OR at_minute <> $7 OR time_zone <> $8 OR NOT enabled THEN now() ELSE last_run_at END,
		updated_at = now() WHERE id = $1`, append([]any{p.ID}, powerScheduleArgs(p)...)...)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := setScheduleGroups(ctx, tx, p.ID, p.GroupIDs); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeletePowerSchedule removes a schedule.
func (s *Store) DeletePowerSchedule(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM power_schedules WHERE id = $1", id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// MarkPowerScheduleRun records the slot that was carried out. It reports
// false when another server process did it already.
func (s *Store) MarkPowerScheduleRun(ctx context.Context, id int64, slot time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE power_schedules SET last_run_at = $2
		WHERE id = $1 AND (last_run_at IS NULL OR last_run_at < $2)`, id, slot)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ---- devices ----

// PowerDevice is what power management needs to know about a device.
type PowerDevice struct {
	ID           string
	Hostname     string
	GroupID      int64
	Platform     string
	Kind         string
	RemoteIP     string
	LocalIPs     []string
	LoggedInUser string
	LastSeenAt   *time.Time
	// Network is the adapters from the last inventory.
	Network json.RawMessage
}

// PowerDeviceQuery selects devices: by ID, by group or by scope.
type PowerDeviceQuery struct {
	IDs    []string
	Groups []int64
	Scope  *Scope
}

// PowerDevices returns the network view of devices.
func (s *Store) PowerDevices(ctx context.Context, q PowerDeviceQuery) ([]PowerDevice, error) {
	all, groups := false, []int64{}
	if q.Scope != nil {
		all, groups = q.Scope.All, q.Scope.groups()
	} else if q.Groups != nil {
		groups = q.Groups
	}
	ids := q.IDs
	if ids == nil {
		ids = []string{}
	}
	rows, err := s.pool.Query(ctx, `SELECT d.id::text, d.hostname, d.group_id, d.platform, d.kind, d.remote_ip, d.local_ips,
		d.logged_in_user, d.last_seen_at, COALESCE(d.hardware->'network', 'null')
		FROM devices d
		WHERE ($1 OR d.group_id = ANY($2) OR d.id::text = ANY($3))
		ORDER BY lower(d.hostname)`, all, groups, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PowerDevice{}
	for rows.Next() {
		var d PowerDevice
		if err := rows.Scan(&d.ID, &d.Hostname, &d.GroupID, &d.Platform, &d.Kind, &d.RemoteIP, &d.LocalIPs, &d.LoggedInUser,
			&d.LastSeenAt, &d.Network); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ---- actions ----

// PowerAction is one wake, restart or shutdown.
type PowerAction struct {
	ID             int64      `json:"id"`
	DeviceID       string     `json:"deviceId"`
	Hostname       string     `json:"hostname"`
	Action         string     `json:"action"`
	Trigger        string     `json:"trigger"`
	ScheduleID     *int64     `json:"scheduleId"`
	ScheduleName   string     `json:"scheduleName"`
	State          string     `json:"state"`
	Message        string     `json:"message"`
	Relays         []string   `json:"relays"`
	Postpones      int        `json:"postpones"`
	PostponedUntil *time.Time `json:"postponedUntil"`
	RequestedBy    string     `json:"requestedBy"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
	FinishedAt     *time.Time `json:"finishedAt"`
}

const powerActionCols = `a.id, a.device_id::text, d.hostname, a.action, a.trigger, a.schedule_id, COALESCE(ps.name, ''), a.state,
	a.message, a.relays, a.postpones, a.postponed_until, a.requested_by, a.created_at, a.updated_at, a.finished_at`

const powerActionFrom = ` FROM power_actions a JOIN devices d ON d.id = a.device_id
	LEFT JOIN power_schedules ps ON ps.id = a.schedule_id`

func scanPowerAction(row pgx.Row) (PowerAction, error) {
	var a PowerAction
	err := row.Scan(&a.ID, &a.DeviceID, &a.Hostname, &a.Action, &a.Trigger, &a.ScheduleID, &a.ScheduleName, &a.State, &a.Message,
		&a.Relays, &a.Postpones, &a.PostponedUntil, &a.RequestedBy, &a.CreatedAt, &a.UpdatedAt, &a.FinishedAt)
	return a, mapErr(err)
}

// CreatePowerAction records an action. An action that is already final
// (a wake that could not be sent) passes its state and message.
func (s *Store) CreatePowerAction(ctx context.Context, a PowerAction, finished bool) (PowerAction, error) {
	if a.Relays == nil {
		a.Relays = []string{}
	}
	if a.State == "" {
		a.State = "sent"
	}
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO power_actions (device_id, action, trigger, schedule_id, state, message, relays, requested_by,
		finished_at) VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, CASE WHEN $9 THEN now() END) RETURNING id`,
		a.DeviceID, a.Action, a.Trigger, a.ScheduleID, a.State, a.Message, a.Relays, a.RequestedBy, finished).Scan(&id)
	if err != nil {
		return a, mapErr(err)
	}
	return s.PowerActionByID(ctx, id)
}

// PowerActionByID returns an action.
func (s *Store) PowerActionByID(ctx context.Context, id int64) (PowerAction, error) {
	return scanPowerAction(s.pool.QueryRow(ctx, "SELECT "+powerActionCols+powerActionFrom+" WHERE a.id = $1", id))
}

// PowerActionUpdate is a status report of a restart or shutdown.
type PowerActionUpdate struct {
	State          string
	Message        string
	Postpones      int
	PostponedUntil *time.Time
	Finished       bool
}

// UpdatePowerAction records the status of an open action of the device.
func (s *Store) UpdatePowerAction(ctx context.Context, id int64, deviceID string, u PowerActionUpdate) error {
	tag, err := s.pool.Exec(ctx, `UPDATE power_actions SET state = $3, message = $4, postpones = GREATEST(postpones, $5),
		postponed_until = $6, updated_at = now(), finished_at = CASE WHEN $7 THEN now() END
		WHERE id = $1 AND device_id::text = $2 AND finished_at IS NULL AND action <> 'wake'`,
		id, deviceID, u.State, u.Message, u.Postpones, u.PostponedUntil, u.Finished)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// NoteWakeRelay records what a relay reported about an open wake.
func (s *Store) NoteWakeRelay(ctx context.Context, id int64, message string) error {
	_, err := s.pool.Exec(ctx, `UPDATE power_actions SET message = CASE WHEN message = '' THEN $2 ELSE message END, updated_at = now()
		WHERE id = $1 AND action = 'wake' AND finished_at IS NULL`, id, message)
	return err
}

// CompleteWakes closes the open wakes of a device that came online.
func (s *Store) CompleteWakes(ctx context.Context, deviceID string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE power_actions SET state = 'completed', updated_at = now(),
		finished_at = now() WHERE device_id::text = $1 AND action = 'wake' AND finished_at IS NULL`, deviceID)
	return tag.RowsAffected(), err
}

// FailStalePowerActions closes wakes that did not bring the device online
// and restarts or shutdowns that did not finish in time.
func (s *Store) FailStalePowerActions(ctx context.Context, wakeAfter, actionAfter time.Duration) error {
	now := time.Now()
	_, err := s.pool.Exec(ctx, `UPDATE power_actions SET state = 'failed', updated_at = now(), finished_at = now(),
		message = CASE WHEN action = 'wake' THEN 'the device did not come online' ELSE 'no answer from the agent' END
		WHERE finished_at IS NULL AND ((action = 'wake' AND created_at < $1) OR (action <> 'wake' AND updated_at < $2))`,
		now.Add(-wakeAfter), now.Add(-actionAfter))
	return err
}

// FailPowerAction closes an open action as failed.
func (s *Store) FailPowerAction(ctx context.Context, id int64, message string) error {
	_, err := s.pool.Exec(ctx, `UPDATE power_actions SET state = 'failed', message = $2, updated_at = now(), finished_at = now()
		WHERE id = $1 AND finished_at IS NULL`, id, message)
	return err
}

// OpenPowerAction reports whether a device has an open action of a kind.
func (s *Store) OpenPowerAction(ctx context.Context, deviceID, action string) (bool, error) {
	var open bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM power_actions WHERE device_id::text = $1 AND action = $2
		AND finished_at IS NULL)`, deviceID, action).Scan(&open)
	return open, err
}

// PowerActionQuery filters the action history.
type PowerActionQuery struct {
	Scope    Scope
	DeviceID string
	Limit    int
}

// ListPowerActions returns actions, newest first.
func (s *Store) ListPowerActions(ctx context.Context, q PowerActionQuery) ([]PowerAction, error) {
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, "SELECT "+powerActionCols+powerActionFrom+`
		WHERE ($1 OR d.group_id = ANY($2)) AND ($3 = '' OR d.id::text = $3) ORDER BY a.id DESC LIMIT $4`,
		q.Scope.All, q.Scope.groups(), q.DeviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PowerAction{}
	for rows.Next() {
		a, err := scanPowerAction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// PurgePowerActions removes finished actions older than the cut-off.
func (s *Store) PurgePowerActions(ctx context.Context, olderThan time.Duration) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM power_actions WHERE finished_at < $1", time.Now().Add(-olderThan))
	return err
}

// ---- patch window ----

// WindowDevice is a device of a patch policy's groups, with what happened
// to it in the current window.
type WindowDevice struct {
	ID           string
	Hostname     string
	Wakes        int        // wakes sent for this window
	WakeOpen     bool       // a wake is still waiting for the device
	WokeAt       *time.Time // when a wake for this window brought it online
	ShutdownSent bool       // a shutdown after the window was sent
	RunFinished  bool       // a window patch job finished since the start
	RunOpen      bool       // a patch job is open
}

// PatchWindowDevices returns the devices of a patch policy's groups and
// their wakes, shutdowns and patch jobs since a window start.
func (s *Store) PatchWindowDevices(ctx context.Context, policyID int64, since time.Time) ([]WindowDevice, error) {
	rows, err := s.pool.Query(ctx, `SELECT d.id::text, d.hostname,
		(SELECT count(*) FROM power_actions a WHERE a.device_id = d.id AND a.trigger = 'patch-window' AND a.created_at >= $2),
		EXISTS (SELECT 1 FROM power_actions a WHERE a.device_id = d.id AND a.trigger = 'patch-window' AND a.finished_at IS NULL),
		(SELECT max(a.finished_at) FROM power_actions a WHERE a.device_id = d.id AND a.trigger = 'patch-window' AND a.state = 'completed'
			AND a.created_at >= $2),
		EXISTS (SELECT 1 FROM power_actions a WHERE a.device_id = d.id AND a.trigger = 'after-patch' AND a.created_at >= $2),
		EXISTS (SELECT 1 FROM patch_runs r WHERE r.device_id = d.id AND r.trigger = 'window' AND r.started_at >= $2 AND r.finished_at IS NOT NULL),
		EXISTS (SELECT 1 FROM patch_runs r WHERE r.device_id = d.id AND r.finished_at IS NULL)
		FROM devices d JOIN device_groups g ON g.id = d.group_id
		WHERE g.patch_policy_id = $1 ORDER BY d.id`, policyID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WindowDevice{}
	for rows.Next() {
		var d WindowDevice
		if err := rows.Scan(&d.ID, &d.Hostname, &d.Wakes, &d.WakeOpen, &d.WokeAt, &d.ShutdownSent, &d.RunFinished, &d.RunOpen); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
