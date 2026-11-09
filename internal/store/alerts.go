package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// SaveVolumes stores the volumes from a heartbeat.
func (s *Store) SaveVolumes(ctx context.Context, deviceID string, volumes json.RawMessage) error {
	_, err := s.pool.Exec(ctx, "UPDATE devices SET volumes = $2, volumes_at = now() WHERE id::text = $1", deviceID, volumes)
	return err
}

// ---- rules ----

// AlertRule opens alerts for devices that are offline or low on disk space.
type AlertRule struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	Enabled        bool      `json:"enabled"`
	Kind           string    `json:"kind"`      // offline, disk
	Threshold      int       `json:"threshold"` // minutes, or percentage free
	GroupIDs       []int64   `json:"groupIds"`
	DeviceKinds    []string  `json:"deviceKinds"`
	Recipients     []string  `json:"recipients"`
	Language       string    `json:"language"`
	NotifyResolved bool      `json:"notifyResolved"`
	OpenAlerts     int       `json:"openAlerts"`
	CreatedBy      string    `json:"createdBy"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

const alertRuleCols = `r.id, r.name, r.enabled, r.kind, r.threshold, r.group_ids, r.device_kinds, r.recipients, r.language,
	r.notify_resolved, (SELECT count(*) FROM alerts a WHERE a.rule_id = r.id AND a.resolved_at IS NULL)::int,
	r.created_by, r.created_at, r.updated_at`

func scanAlertRule(row pgx.Row) (AlertRule, error) {
	var r AlertRule
	err := row.Scan(&r.ID, &r.Name, &r.Enabled, &r.Kind, &r.Threshold, &r.GroupIDs, &r.DeviceKinds, &r.Recipients, &r.Language,
		&r.NotifyResolved, &r.OpenAlerts, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt)
	return r, mapErr(err)
}

// ListAlertRules returns every rule by name.
func (s *Store) ListAlertRules(ctx context.Context) ([]AlertRule, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+alertRuleCols+" FROM alert_rules r ORDER BY lower(r.name)")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AlertRule{}
	for rows.Next() {
		r, err := scanAlertRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AlertRuleByID returns a rule.
func (s *Store) AlertRuleByID(ctx context.Context, id int64) (AlertRule, error) {
	return scanAlertRule(s.pool.QueryRow(ctx, "SELECT "+alertRuleCols+" FROM alert_rules r WHERE r.id = $1", id))
}

// CreateAlertRule stores a new rule.
func (s *Store) CreateAlertRule(ctx context.Context, r AlertRule) (AlertRule, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO alert_rules (name, enabled, kind, threshold, group_ids, device_kinds, recipients, language,
		notify_resolved, created_by) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`, strings.TrimSpace(r.Name), r.Enabled,
		r.Kind, r.Threshold, nonNil(r.GroupIDs), nonNil(r.DeviceKinds), nonNil(r.Recipients), r.Language, r.NotifyResolved, r.CreatedBy).Scan(&id)
	if err != nil {
		return r, mapErr(err)
	}
	return s.AlertRuleByID(ctx, id)
}

// UpdateAlertRule changes a rule. The open alerts of a rule whose kind
// changes are closed, since they no longer apply.
func (s *Store) UpdateAlertRule(ctx context.Context, r AlertRule) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var kind string
		if err := tx.QueryRow(ctx, "SELECT kind FROM alert_rules WHERE id = $1 FOR UPDATE", r.ID).Scan(&kind); err != nil {
			return mapErr(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE alert_rules SET name = $2, enabled = $3, kind = $4, threshold = $5, group_ids = $6,
			device_kinds = $7, recipients = $8, language = $9, notify_resolved = $10, updated_at = now() WHERE id = $1`, r.ID,
			strings.TrimSpace(r.Name), r.Enabled, r.Kind, r.Threshold, nonNil(r.GroupIDs), nonNil(r.DeviceKinds), nonNil(r.Recipients),
			r.Language, r.NotifyResolved); err != nil {
			return mapErr(err)
		}
		if kind != r.Kind {
			_, err := tx.Exec(ctx, "DELETE FROM alerts WHERE rule_id = $1", r.ID)
			return err
		}
		return nil
	})
}

// DeleteAlertRule removes a rule and its alerts.
func (s *Store) DeleteAlertRule(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM alert_rules WHERE id = $1", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- evaluation ----

// AlertDevice is what the alert rules look at.
type AlertDevice struct {
	ID         string
	Hostname   string
	GroupID    int64
	GroupName  string
	Kind       string
	LastSeenAt time.Time
	Volumes    []ReportVolume
	// WarrantyEnd is the end of the warranty from the asset register.
	WarrantyEnd *time.Time
}

// AlertDevices returns every device with its latest volumes.
func (s *Store) AlertDevices(ctx context.Context) ([]AlertDevice, error) {
	rows, err := s.pool.Query(ctx, `SELECT d.id::text, d.hostname, d.group_id, g.name, d.kind, d.last_seen_at,
		COALESCE(d.volumes, d.hardware->'volumes', '[]'::jsonb), a.warranty_end
		FROM devices d JOIN device_groups g ON g.id = d.group_id LEFT JOIN device_assets a ON a.device_id = d.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AlertDevice
	for rows.Next() {
		var d AlertDevice
		var vol []byte
		if err := rows.Scan(&d.ID, &d.Hostname, &d.GroupID, &d.GroupName, &d.Kind, &d.LastSeenAt, &vol, &d.WarrantyEnd); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(vol, &d.Volumes)
		out = append(out, d)
	}
	return out, rows.Err()
}

// Alert is one alert for a device.
type Alert struct {
	ID             int64           `json:"id"`
	RuleID         int64           `json:"ruleId"`
	RuleName       string          `json:"ruleName"`
	DeviceID       string          `json:"deviceId"`
	Hostname       string          `json:"hostname"`
	GroupID        int64           `json:"groupId"`
	GroupName      string          `json:"groupName"`
	Kind           string          `json:"kind"`
	Subject        string          `json:"subject"`
	Details        json.RawMessage `json:"details"`
	OpenedAt       time.Time       `json:"openedAt"`
	ResolvedAt     *time.Time      `json:"resolvedAt"`
	AcknowledgedAt *time.Time      `json:"acknowledgedAt"`
	AcknowledgedBy string          `json:"acknowledgedBy"`
}

const alertCols = `a.id, a.rule_id, r.name, a.device_id::text, d.hostname, d.group_id, g.name, a.kind, a.subject, a.details,
	a.opened_at, a.resolved_at, a.acknowledged_at, a.acknowledged_by`

const alertFrom = ` FROM alerts a JOIN alert_rules r ON r.id = a.rule_id JOIN devices d ON d.id = a.device_id
	JOIN device_groups g ON g.id = d.group_id`

func scanAlert(row pgx.Row) (Alert, error) {
	var a Alert
	err := row.Scan(&a.ID, &a.RuleID, &a.RuleName, &a.DeviceID, &a.Hostname, &a.GroupID, &a.GroupName, &a.Kind, &a.Subject, &a.Details,
		&a.OpenedAt, &a.ResolvedAt, &a.AcknowledgedAt, &a.AcknowledgedBy)
	return a, mapErr(err)
}

func (s *Store) queryAlerts(ctx context.Context, where string, args ...any) ([]Alert, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+alertCols+alertFrom+" WHERE "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Alert{}
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// OpenAlertsOf returns the open alerts of the given rules.
func (s *Store) OpenAlertsOf(ctx context.Context, ruleIDs []int64) ([]Alert, error) {
	return s.queryAlerts(ctx, "a.resolved_at IS NULL AND a.rule_id = ANY($1)", nonNil(ruleIDs))
}

// OpenAlert opens an alert unless the same one is open already. It
// returns the new alert's id, or 0.
func (s *Store) OpenAlert(ctx context.Context, a Alert) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO alerts (rule_id, device_id, kind, subject, details, opened_at) VALUES ($1, $2::uuid, $3, $4, $5, $6)
		ON CONFLICT (rule_id, device_id, subject) WHERE resolved_at IS NULL DO NOTHING RETURNING id`,
		a.RuleID, a.DeviceID, a.Kind, a.Subject, a.Details, a.OpenedAt).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return id, mapErr(err)
}

// UpdateAlertDetails refreshes what an open alert shows, such as the free space.
func (s *Store) UpdateAlertDetails(ctx context.Context, id int64, details json.RawMessage) error {
	_, err := s.pool.Exec(ctx, "UPDATE alerts SET details = $2 WHERE id = $1 AND resolved_at IS NULL AND details <> $2", id, details)
	return err
}

// ResolveAlert closes an open alert. It reports whether it was open.
func (s *Store) ResolveAlert(ctx context.Context, id int64, at time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, "UPDATE alerts SET resolved_at = $2 WHERE id = $1 AND resolved_at IS NULL", id, at)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// CloseAlertsOfDisabledRules resolves the open alerts of disabled rules.
func (s *Store) CloseAlertsOfDisabledRules(ctx context.Context, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE alerts SET resolved_at = $1 WHERE resolved_at IS NULL
		AND rule_id IN (SELECT id FROM alert_rules WHERE NOT enabled)`, at)
	return err
}

// AlertFilter selects alerts for the web interface.
type AlertFilter struct {
	Open     bool   // only open alerts, otherwise open and resolved
	DeviceID string // optional
	Limit    int
}

// ListAlerts returns alerts within a scope, newest first.
func (s *Store) ListAlerts(ctx context.Context, sc Scope, f AlertFilter) ([]Alert, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 200
	}
	return s.queryAlerts(ctx, `($1 OR d.group_id = ANY($2)) AND (NOT $3 OR a.resolved_at IS NULL) AND ($4 = '' OR a.device_id::text = $4)
		ORDER BY a.resolved_at IS NOT NULL, a.opened_at DESC, a.id DESC LIMIT $5`, sc.All, sc.groups(), f.Open, f.DeviceID, f.Limit)
}

// AlertByID returns one alert.
func (s *Store) AlertByID(ctx context.Context, id int64) (Alert, error) {
	return scanAlert(s.pool.QueryRow(ctx, "SELECT "+alertCols+alertFrom+" WHERE a.id = $1", id))
}

// AcknowledgeAlert marks an alert as seen by someone.
func (s *Store) AcknowledgeAlert(ctx context.Context, id int64, by string, at time.Time) error {
	tag, err := s.pool.Exec(ctx, "UPDATE alerts SET acknowledged_at = $3, acknowledged_by = $2 WHERE id = $1 AND acknowledged_at IS NULL",
		id, by, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountOpenAlerts counts the open, unacknowledged alerts within a scope.
func (s *Store) CountOpenAlerts(ctx context.Context, sc Scope) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*)::int FROM alerts a JOIN devices d ON d.id = a.device_id
		WHERE a.resolved_at IS NULL AND a.acknowledged_at IS NULL AND ($1 OR d.group_id = ANY($2))`, sc.All, sc.groups()).Scan(&n)
	return n, err
}
