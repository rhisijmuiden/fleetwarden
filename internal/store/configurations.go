package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// Configuration is a set of settings (browser policies, registry values, a
// script, ...) that agents of the assigned groups apply and keep in place.
type Configuration struct {
	ID          int64           `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Type        string          `json:"type"`
	Platform    string          `json:"platform"`
	Settings    json.RawMessage `json:"settings"`
	Enabled     bool            `json:"enabled"`
	// GroupIDs are the groups the configuration reaches through its enabled
	// profiles; it is kept up to date by the store.
	GroupIDs []int64 `json:"groupIds"`
	// ProfileIDs are the profiles the configuration is in.
	ProfileIDs []int64   `json:"profileIds"`
	Priority   int       `json:"priority"`
	Revision   int       `json:"revision"`
	CreatedBy  string    `json:"createdBy"`
	UpdatedBy  string    `json:"updatedBy"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	// Counts over the devices of the assigned groups, in the caller's scope.
	Devices  int `json:"devices"`
	Applied  int `json:"applied"`
	Failed   int `json:"failed"`
	Repaired int `json:"repaired"` // devices where settings were changed outside FleetWarden
}

const configCols = `c.id, c.name, c.description, c.type, c.platform, c.settings, c.enabled, c.group_ids,
	COALESCE((SELECT array_agg(pi.profile_id ORDER BY pi.profile_id) FROM configuration_profile_items pi WHERE pi.configuration_id = c.id), '{}'),
	c.priority, c.revision, c.created_by, c.updated_by, c.created_at, c.updated_at`

// configCounts counts the target devices within a scope ($1 all, $2 groups).
const configCounts = `,
	(SELECT count(*) FROM devices d WHERE d.group_id = ANY(c.group_ids) AND d.platform = c.platform AND ($1 OR d.group_id = ANY($2)))::int,
	(SELECT count(*) FROM device_configurations dc JOIN devices d ON d.id = dc.device_id
		WHERE dc.configuration_id = c.id AND dc.state = 'applied' AND dc.revision = c.revision AND ($1 OR d.group_id = ANY($2)))::int,
	(SELECT count(*) FROM device_configurations dc JOIN devices d ON d.id = dc.device_id
		WHERE dc.configuration_id = c.id AND dc.state = 'failed' AND ($1 OR d.group_id = ANY($2)))::int,
	(SELECT count(*) FROM device_configurations dc JOIN devices d ON d.id = dc.device_id
		WHERE dc.configuration_id = c.id AND dc.repaired_total > 0 AND ($1 OR d.group_id = ANY($2)))::int`

func scanConfig(row pgx.Row, counts bool) (Configuration, error) {
	var c Configuration
	dst := []any{&c.ID, &c.Name, &c.Description, &c.Type, &c.Platform, &c.Settings, &c.Enabled, &c.GroupIDs, &c.ProfileIDs, &c.Priority, &c.Revision,
		&c.CreatedBy, &c.UpdatedBy, &c.CreatedAt, &c.UpdatedAt}
	if counts {
		dst = append(dst, &c.Devices, &c.Applied, &c.Failed, &c.Repaired)
	}
	err := row.Scan(dst...)
	c.GroupIDs, c.ProfileIDs = groupIDs(c.GroupIDs), groupIDs(c.ProfileIDs)
	return c, mapErr(err)
}

// ListConfigurations returns every configuration with counts for the scope.
func (s *Store) ListConfigurations(ctx context.Context, sc Scope) ([]Configuration, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+configCols+configCounts+" FROM configurations c ORDER BY c.priority, lower(c.name)", sc.All, sc.groups())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Configuration{}
	for rows.Next() {
		c, err := scanConfig(rows, true)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ConfigurationByID returns a configuration with counts for the scope.
func (s *Store) ConfigurationByID(ctx context.Context, id int64, sc Scope) (Configuration, error) {
	return scanConfig(s.pool.QueryRow(ctx, "SELECT "+configCols+configCounts+" FROM configurations c WHERE c.id = $3", sc.All, sc.groups(), id), true)
}

// EnabledConfigurations returns the enabled configurations in the order
// agents apply them.
func (s *Store) EnabledConfigurations(ctx context.Context) ([]Configuration, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+configCols+" FROM configurations c WHERE c.enabled AND cardinality(c.group_ids) > 0 ORDER BY c.priority, c.id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Configuration{}
	for rows.Next() {
		c, err := scanConfig(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func groupIDs(v []int64) []int64 {
	if v == nil {
		return []int64{}
	}
	return v
}

// CreateConfiguration inserts a configuration and puts it in the profiles
// of c.ProfileIDs.
func (s *Store) CreateConfiguration(ctx context.Context, c Configuration) (Configuration, error) {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO configurations (name, description, type, platform, settings, enabled, priority, created_by, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8) RETURNING id`, c.Name, c.Description, c.Type, c.Platform, c.Settings, c.Enabled,
			c.Priority, c.CreatedBy).Scan(&c.ID)
		if err != nil {
			return mapErr(err)
		}
		return setConfigProfiles(ctx, tx, c.ID, c.ProfileIDs)
	})
	if err != nil {
		return c, err
	}
	return s.ConfigurationByID(ctx, c.ID, Scope{All: true})
}

// UpdateConfiguration changes a configuration and the profiles it is in. The
// revision goes up when the settings change. Type and platform stay.
func (s *Store) UpdateConfiguration(ctx context.Context, c Configuration) (Configuration, error) {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE configurations SET name = $2, description = $3, settings = $4, enabled = $5, priority = $6,
			updated_by = $7, updated_at = now(), revision = revision + CASE WHEN settings IS DISTINCT FROM $4::jsonb THEN 1 ELSE 0 END
			WHERE id = $1`, c.ID, c.Name, c.Description, c.Settings, c.Enabled, c.Priority, c.UpdatedBy)
		if err != nil {
			return mapErr(err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return setConfigProfiles(ctx, tx, c.ID, c.ProfileIDs)
	})
	if err != nil {
		return c, err
	}
	return s.ConfigurationByID(ctx, c.ID, Scope{All: true})
}

// setConfigProfiles makes a configuration a member of exactly the listed
// profiles; unknown profiles are ignored.
func setConfigProfiles(ctx context.Context, tx pgx.Tx, id int64, profiles []int64) error {
	if _, err := tx.Exec(ctx, "DELETE FROM configuration_profile_items WHERE configuration_id = $1 AND NOT profile_id = ANY($2)", id, groupIDs(profiles)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO configuration_profile_items (profile_id, configuration_id)
		SELECT p.id, $1 FROM configuration_profiles p WHERE p.id = ANY($2) ON CONFLICT DO NOTHING`, id, groupIDs(profiles)); err != nil {
		return err
	}
	return refreshConfigGroups(ctx, tx)
}

// refreshConfigGroups sets the groups of every configuration to those of its
// enabled profiles.
func refreshConfigGroups(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `UPDATE configurations c SET group_ids = x.groups FROM (
		SELECT c2.id, COALESCE((SELECT array_agg(DISTINCT g ORDER BY g) FROM configuration_profile_items pi
			JOIN configuration_profiles p ON p.id = pi.profile_id AND p.enabled, unnest(p.group_ids) g
			WHERE pi.configuration_id = c2.id), '{}') AS groups FROM configurations c2) x
		WHERE x.id = c.id AND c.group_ids IS DISTINCT FROM x.groups`)
	return err
}

// DeleteConfiguration removes a configuration. Agents remove its settings
// when they get their next policy.
func (s *Store) DeleteConfiguration(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM configurations WHERE id = $1", id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ConfigTarget is what decides which configurations a device gets.
type ConfigTarget struct {
	DeviceID string
	GroupID  int64
	Platform string
}

// ConfigTargets returns group and platform of the listed devices.
func (s *Store) ConfigTargets(ctx context.Context, ids []string) ([]ConfigTarget, error) {
	rows, err := s.pool.Query(ctx, "SELECT id::text, group_id, platform FROM devices WHERE id::text = ANY($1)", ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConfigTarget
	for rows.Next() {
		var t ConfigTarget
		if err := rows.Scan(&t.DeviceID, &t.GroupID, &t.Platform); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// PruneDeviceConfigurations removes states of configurations that no longer
// apply to their device (deleted, disabled, unassigned, or the device moved).
func (s *Store) PruneDeviceConfigurations(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM device_configurations dc USING devices d WHERE d.id = dc.device_id AND NOT EXISTS (
		SELECT 1 FROM configurations c WHERE c.id = dc.configuration_id AND c.enabled AND d.group_id = ANY(c.group_ids) AND c.platform = d.platform)`)
	return err
}

// ConfigItemReport is the state of one configuration in an agent report.
type ConfigItemReport struct {
	ConfigurationID int64
	Revision        int
	State           string
	Message         string
	Repaired        int
	AppliedAt       *time.Time
	Runs            []ConfigRun
}

// ConfigRun is one script run.
type ConfigRun struct {
	ID              int64     `json:"id"`
	DeviceID        string    `json:"deviceId"`
	Hostname        string    `json:"hostname"`
	ConfigurationID int64     `json:"configurationId"`
	Revision        int       `json:"revision"`
	At              time.Time `json:"at"`
	User            string    `json:"user"`
	ExitCode        int       `json:"exitCode"`
	Output          string    `json:"output"`
	Error           string    `json:"error"`
	DurationMs      int64     `json:"durationMs"`
}

// SaveConfigReport stores an agent's report. Reports about configurations
// that do not exist (any more) are ignored.
func (s *Store) SaveConfigReport(ctx context.Context, deviceID string, items []ConfigItemReport, at time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	for _, it := range items {
		var last *ConfigRun
		if n := len(it.Runs); n > 0 {
			last = &it.Runs[n-1]
		}
		var runAt *time.Time
		var runExit *int
		runOut, runErr := "", ""
		if last != nil {
			runAt, runExit, runOut, runErr = &last.At, &last.ExitCode, last.Output, last.Error
		}
		var repairedAt *time.Time
		if it.Repaired > 0 {
			repairedAt = &at
		}
		tag, err := tx.Exec(ctx, `INSERT INTO device_configurations (device_id, configuration_id, revision, state, message, repaired_total, repaired_at,
			applied_at, reported_at, run_at, run_exit_code, run_output, run_error)
			SELECT $1, c.id, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13 FROM configurations c WHERE c.id = $2
			ON CONFLICT (device_id, configuration_id) DO UPDATE SET revision = EXCLUDED.revision, state = EXCLUDED.state, message = EXCLUDED.message,
				repaired_total = CASE WHEN device_configurations.revision = EXCLUDED.revision THEN device_configurations.repaired_total ELSE 0 END + EXCLUDED.repaired_total,
				repaired_at = COALESCE(EXCLUDED.repaired_at, CASE WHEN device_configurations.revision = EXCLUDED.revision THEN device_configurations.repaired_at END),
				applied_at = COALESCE(EXCLUDED.applied_at, device_configurations.applied_at), reported_at = EXCLUDED.reported_at,
				run_at = COALESCE(EXCLUDED.run_at, device_configurations.run_at),
				run_exit_code = CASE WHEN EXCLUDED.run_at IS NULL THEN device_configurations.run_exit_code ELSE EXCLUDED.run_exit_code END,
				run_output = CASE WHEN EXCLUDED.run_at IS NULL THEN device_configurations.run_output ELSE EXCLUDED.run_output END,
				run_error = CASE WHEN EXCLUDED.run_at IS NULL THEN device_configurations.run_error ELSE EXCLUDED.run_error END`,
			deviceID, it.ConfigurationID, it.Revision, it.State, it.Message, it.Repaired, repairedAt, it.AppliedAt, at, runAt, runExit, runOut, runErr)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		for _, r := range it.Runs {
			if _, err := tx.Exec(ctx, `INSERT INTO configuration_runs (device_id, configuration_id, revision, run_at, username, exit_code, output, error, duration_ms)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, deviceID, it.ConfigurationID, it.Revision, r.At, r.User, r.ExitCode, r.Output, r.Error, r.DurationMs); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

// DeviceConfigState is the state of a configuration on a device.
type DeviceConfigState struct {
	DeviceID          string     `json:"deviceId"`
	Hostname          string     `json:"hostname"`
	GroupID           int64      `json:"groupId"`
	DomainJoined      bool       `json:"domainJoined"`
	Online            bool       `json:"online"` // filled in by the server
	ConfigurationID   int64      `json:"configurationId"`
	ConfigurationName string     `json:"configurationName"`
	Type              string     `json:"type"`
	CurrentRevision   int        `json:"currentRevision"`
	Revision          int        `json:"revision"`
	State             string     `json:"state"` // pending when the device has not reported yet
	Message           string     `json:"message"`
	RepairedTotal     int        `json:"repairedTotal"`
	RepairedAt        *time.Time `json:"repairedAt"`
	AppliedAt         *time.Time `json:"appliedAt"`
	ReportedAt        *time.Time `json:"reportedAt"`
	RunAt             *time.Time `json:"runAt"`
	RunExitCode       *int       `json:"runExitCode"`
	RunOutput         string     `json:"runOutput"`
	RunError          string     `json:"runError"`
}

const deviceConfigCols = `d.id::text, d.hostname, d.group_id, d.domain_joined, c.id, c.name, c.type, c.revision,
	COALESCE(dc.revision, 0), COALESCE(dc.state, 'pending'), COALESCE(dc.message, ''), COALESCE(dc.repaired_total, 0), dc.repaired_at,
	dc.applied_at, dc.reported_at, dc.run_at, dc.run_exit_code, COALESCE(dc.run_output, ''), COALESCE(dc.run_error, '')`

func scanDeviceConfigs(rows pgx.Rows) ([]DeviceConfigState, error) {
	defer rows.Close()
	out := []DeviceConfigState{}
	for rows.Next() {
		var x DeviceConfigState
		if err := rows.Scan(&x.DeviceID, &x.Hostname, &x.GroupID, &x.DomainJoined, &x.ConfigurationID, &x.ConfigurationName, &x.Type, &x.CurrentRevision,
			&x.Revision, &x.State, &x.Message, &x.RepairedTotal, &x.RepairedAt, &x.AppliedAt, &x.ReportedAt, &x.RunAt, &x.RunExitCode,
			&x.RunOutput, &x.RunError); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// ConfigurationDevices lists the target devices of a configuration within
// the scope, with their state.
func (s *Store) ConfigurationDevices(ctx context.Context, id int64, sc Scope) ([]DeviceConfigState, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+deviceConfigCols+` FROM configurations c
		JOIN devices d ON d.group_id = ANY(c.group_ids) AND d.platform = c.platform
		LEFT JOIN device_configurations dc ON dc.device_id = d.id AND dc.configuration_id = c.id
		WHERE c.id = $1 AND ($2 OR d.group_id = ANY($3)) ORDER BY lower(d.hostname) LIMIT 5000`, id, sc.All, sc.groups())
	if err != nil {
		return nil, err
	}
	return scanDeviceConfigs(rows)
}

// DeviceConfigurations lists the enabled configurations that apply to a
// device, in the order they apply, with their state.
func (s *Store) DeviceConfigurations(ctx context.Context, deviceID string) ([]DeviceConfigState, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+deviceConfigCols+` FROM devices d
		JOIN configurations c ON d.group_id = ANY(c.group_ids) AND d.platform = c.platform AND c.enabled
		LEFT JOIN device_configurations dc ON dc.device_id = d.id AND dc.configuration_id = c.id
		WHERE d.id::text = $1 ORDER BY c.priority, c.id`, deviceID)
	if err != nil {
		return nil, err
	}
	return scanDeviceConfigs(rows)
}

// ConfigRunQuery selects script runs.
type ConfigRunQuery struct {
	Scope           Scope
	ConfigurationID int64
	DeviceID        string
	Limit           int
}

// ConfigurationRuns returns script runs, newest first.
func (s *Store) ConfigurationRuns(ctx context.Context, q ConfigRunQuery) ([]ConfigRun, error) {
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT r.id, r.device_id::text, d.hostname, r.configuration_id, r.revision, r.run_at, r.username, r.exit_code,
		r.output, r.error, r.duration_ms FROM configuration_runs r JOIN devices d ON d.id = r.device_id
		WHERE ($1 = 0 OR r.configuration_id = $1) AND ($2 = '' OR r.device_id::text = $2) AND ($3 OR d.group_id = ANY($4))
		ORDER BY r.id DESC LIMIT $5`, q.ConfigurationID, q.DeviceID, q.Scope.All, q.Scope.groups(), q.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ConfigRun{}
	for rows.Next() {
		var r ConfigRun
		if err := rows.Scan(&r.ID, &r.DeviceID, &r.Hostname, &r.ConfigurationID, &r.Revision, &r.At, &r.User, &r.ExitCode, &r.Output,
			&r.Error, &r.DurationMs); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PurgeConfigurationRuns removes old script runs.
func (s *Store) PurgeConfigurationRuns(ctx context.Context, olderThan time.Duration) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM configuration_runs WHERE run_at < $1", time.Now().Add(-olderThan))
	return err
}
