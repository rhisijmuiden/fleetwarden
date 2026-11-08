package store

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Gateway is a secure gateway in the DMZ that relays agents to this server.
type Gateway struct {
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	Address         string     `json:"address"`
	PublicURL       string     `json:"publicUrl"`
	PublicKey       []byte     `json:"-"`
	Enabled         bool       `json:"enabled"`
	Version         string     `json:"version"`
	LastConnectedAt *time.Time `json:"lastConnectedAt"`
	LastError       string     `json:"lastError"`
	LastErrorAt     *time.Time `json:"lastErrorAt"`
	CreatedBy       string     `json:"createdBy"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

const gatewayCols = `id, name, address, public_url, public_key, enabled, version, last_connected_at, last_error, last_error_at,
	created_by, created_at, updated_at`

func scanGateway(row pgx.Row) (Gateway, error) {
	var g Gateway
	err := row.Scan(&g.ID, &g.Name, &g.Address, &g.PublicURL, &g.PublicKey, &g.Enabled, &g.Version, &g.LastConnectedAt,
		&g.LastError, &g.LastErrorAt, &g.CreatedBy, &g.CreatedAt, &g.UpdatedAt)
	return g, mapErr(err)
}

// ListGateways returns every gateway by name.
func (s *Store) ListGateways(ctx context.Context) ([]Gateway, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+gatewayCols+" FROM gateways ORDER BY lower(name)")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Gateway{}
	for rows.Next() {
		g, err := scanGateway(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GatewayByID returns a gateway.
func (s *Store) GatewayByID(ctx context.Context, id int64) (Gateway, error) {
	return scanGateway(s.pool.QueryRow(ctx, "SELECT "+gatewayCols+" FROM gateways WHERE id = $1", id))
}

// GatewayNameTaken reports whether another gateway has the name.
func (s *Store) GatewayNameTaken(ctx context.Context, name string, exceptID int64) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx, "SELECT count(*) FROM gateways WHERE lower(name) = lower($1) AND id <> $2", strings.TrimSpace(name), exceptID).Scan(&n)
	return n > 0, err
}

// CreateGateway stores a paired gateway.
func (s *Store) CreateGateway(ctx context.Context, g Gateway) (Gateway, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO gateways (name, address, public_url, public_key, enabled, version, last_connected_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, now(), $7) RETURNING id`, strings.TrimSpace(g.Name), g.Address, g.PublicURL, g.PublicKey, g.Enabled,
		g.Version, g.CreatedBy).Scan(&id)
	if err != nil {
		return g, mapErr(err)
	}
	return s.GatewayByID(ctx, id)
}

// UpdateGateway changes the editable fields.
func (s *Store) UpdateGateway(ctx context.Context, g Gateway) error {
	tag, err := s.pool.Exec(ctx, `UPDATE gateways SET name = $2, address = $3, public_url = $4, enabled = $5, updated_at = now()
		WHERE id = $1`, g.ID, strings.TrimSpace(g.Name), g.Address, g.PublicURL, g.Enabled)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RepairGateway stores a new gateway key after pairing again.
func (s *Store) RepairGateway(ctx context.Context, id int64, address string, key []byte, version string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE gateways SET address = $2, public_key = $3, version = $4, last_error = '', last_error_at = NULL,
		last_connected_at = now(), updated_at = now() WHERE id = $1`, id, address, key, version)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GatewayConnected records a successful tunnel.
func (s *Store) GatewayConnected(ctx context.Context, id int64, version string) error {
	_, err := s.pool.Exec(ctx, "UPDATE gateways SET version = $2, last_connected_at = now(), last_error = '', last_error_at = NULL WHERE id = $1", id, version)
	return err
}

// GatewayFailed records why the tunnel could not be opened.
func (s *Store) GatewayFailed(ctx context.Context, id int64, msg string) error {
	_, err := s.pool.Exec(ctx, "UPDATE gateways SET last_error = $2, last_error_at = now() WHERE id = $1", id, msg)
	return err
}

// DeleteGateway removes a gateway.
func (s *Store) DeleteGateway(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM gateways WHERE id = $1", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
