package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// Integration is a stored service desk connector or custom API call.
// Secrets holds sealed values; the server opens them.
type Integration struct {
	ID           int64             `json:"id"`
	Name         string            `json:"name"`
	Kind         string            `json:"kind"`
	Enabled      bool              `json:"enabled"`
	Settings     map[string]string `json:"settings"`
	Custom       json.RawMessage   `json:"custom"`
	Secrets      map[string]string `json:"-"`
	Events       []string          `json:"events"`
	AlertRuleIDs []int64           `json:"alertRuleIds"`
	CreatedBy    string            `json:"createdBy"`
	CreatedAt    time.Time         `json:"createdAt"`
	UpdatedAt    time.Time         `json:"updatedAt"`
	LastOK       *bool             `json:"lastOk"`
	LastError    string            `json:"lastError"`
	LastAt       *time.Time        `json:"lastAt"`
}

// IntegrationCall is one logged request of an integration.
type IntegrationCall struct {
	ID            int64     `json:"id"`
	IntegrationID int64     `json:"integrationId"`
	CreatedAt     time.Time `json:"createdAt"`
	Event         string    `json:"event"`
	Method        string    `json:"method"`
	URL           string    `json:"url"`
	Status        int       `json:"status"`
	OK            bool      `json:"ok"`
	DurationMS    int       `json:"durationMs"`
	Error         string    `json:"error"`
	TicketNumber  string    `json:"ticketNumber"`
	TicketURL     string    `json:"ticketUrl"`
}

const integrationCols = `id, name, kind, enabled, settings, custom, secrets, events, alert_rule_ids, created_by, created_at, updated_at,
	last_ok, last_error, last_at`

func scanIntegration(row pgx.Row) (Integration, error) {
	var i Integration
	err := row.Scan(&i.ID, &i.Name, &i.Kind, &i.Enabled, &i.Settings, &i.Custom, &i.Secrets, &i.Events, &i.AlertRuleIDs,
		&i.CreatedBy, &i.CreatedAt, &i.UpdatedAt, &i.LastOK, &i.LastError, &i.LastAt)
	if i.Settings == nil {
		i.Settings = map[string]string{}
	}
	if i.Secrets == nil {
		i.Secrets = map[string]string{}
	}
	if i.Events == nil {
		i.Events = []string{}
	}
	if i.AlertRuleIDs == nil {
		i.AlertRuleIDs = []int64{}
	}
	return i, mapErr(err)
}

// ListIntegrations returns every integration by name.
func (s *Store) ListIntegrations(ctx context.Context) ([]Integration, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+integrationCols+" FROM integrations ORDER BY lower(name)")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Integration{}
	for rows.Next() {
		i, err := scanIntegration(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// IntegrationByID returns one integration.
func (s *Store) IntegrationByID(ctx context.Context, id int64) (Integration, error) {
	return scanIntegration(s.pool.QueryRow(ctx, "SELECT "+integrationCols+" FROM integrations WHERE id = $1", id))
}

func nullJSON(v json.RawMessage) any {
	if len(v) == 0 || string(v) == "null" {
		return nil
	}
	return v
}

// CreateIntegration stores a new integration and returns its id.
func (s *Store) CreateIntegration(ctx context.Context, i Integration) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO integrations (name, kind, enabled, settings, custom, secrets, events, alert_rule_ids, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`, i.Name, i.Kind, i.Enabled, i.Settings, nullJSON(i.Custom), i.Secrets,
		i.Events, i.AlertRuleIDs, i.CreatedBy).Scan(&id)
	return id, mapErr(err)
}

// UpdateIntegration changes an integration (not its kind).
func (s *Store) UpdateIntegration(ctx context.Context, i Integration) error {
	tag, err := s.pool.Exec(ctx, `UPDATE integrations SET name = $2, enabled = $3, settings = $4, custom = $5, secrets = $6, events = $7,
		alert_rule_ids = $8, updated_at = now() WHERE id = $1`, i.ID, i.Name, i.Enabled, i.Settings, nullJSON(i.Custom), i.Secrets, i.Events, i.AlertRuleIDs)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteIntegration removes an integration and its call log.
func (s *Store) DeleteIntegration(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM integrations WHERE id = $1", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordIntegrationCall logs a request and updates the integration's last
// result.
func (s *Store) RecordIntegrationCall(ctx context.Context, c IntegrationCall) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `INSERT INTO integration_calls (integration_id, event, method, url, status, ok, duration_ms, error, ticket_number, ticket_url)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, c.IntegrationID, c.Event, c.Method, c.URL, c.Status, c.OK, c.DurationMS,
		c.Error, c.TicketNumber, c.TicketURL); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE integrations SET last_ok = $2, last_error = $3, last_at = now() WHERE id = $1`,
		c.IntegrationID, c.OK, c.Error); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ListIntegrationCalls returns the latest calls of an integration.
func (s *Store) ListIntegrationCalls(ctx context.Context, id int64, limit int) ([]IntegrationCall, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, integration_id, created_at, event, method, url, status, ok, duration_ms, error, ticket_number, ticket_url
		FROM integration_calls WHERE integration_id = $1 ORDER BY id DESC LIMIT $2`, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IntegrationCall{}
	for rows.Next() {
		var c IntegrationCall
		if err := rows.Scan(&c.ID, &c.IntegrationID, &c.CreatedAt, &c.Event, &c.Method, &c.URL, &c.Status, &c.OK, &c.DurationMS,
			&c.Error, &c.TicketNumber, &c.TicketURL); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// PruneIntegrationCalls drops calls older than the cutoff.
func (s *Store) PruneIntegrationCalls(ctx context.Context, before time.Time) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM integration_calls WHERE created_at < $1", before)
	return err
}
