package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// RoleMapping maps an Entra group object ID or app role value to a role.
type RoleMapping struct {
	ID        int64     `json:"id"`
	Claim     string    `json:"claim"` // "group" or "role"
	Value     string    `json:"value"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
	Scoped    bool      `json:"scoped"`
	Groups    []int64   `json:"groups"`
}

// ListRoleMappings returns all mappings.
func (s *Store) ListRoleMappings(ctx context.Context) ([]RoleMapping, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, claim, value, role, created_at, scoped,
		COALESCE((SELECT array_agg(group_id ORDER BY group_id) FROM role_mapping_scopes s WHERE s.mapping_id = role_mappings.id), '{}')
		FROM role_mappings ORDER BY claim, value`)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[RoleMapping])
	if out == nil {
		out = []RoleMapping{}
	}
	return out, err
}

// CreateRoleMapping adds a mapping.
func (s *Store) CreateRoleMapping(ctx context.Context, m RoleMapping) (RoleMapping, error) {
	err := s.pool.QueryRow(ctx, "INSERT INTO role_mappings (claim, value, role) VALUES ($1, $2, $3) RETURNING id, created_at",
		m.Claim, m.Value, m.Role).Scan(&m.ID, &m.CreatedAt)
	return m, mapErr(err)
}

// DeleteRoleMapping removes a mapping.
func (s *Store) DeleteRoleMapping(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM role_mappings WHERE id = $1", id)
	return err
}
