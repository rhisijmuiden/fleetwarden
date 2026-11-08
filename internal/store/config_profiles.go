package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// ConfigProfile bundles configurations. The devices of its groups get all of
// them.
type ConfigProfile struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Enabled     bool      `json:"enabled"`
	GroupIDs    []int64   `json:"groupIds"`
	ConfigIDs   []int64   `json:"configIds"`
	CreatedBy   string    `json:"createdBy"`
	UpdatedBy   string    `json:"updatedBy"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	// Devices in its groups, within the caller's scope.
	Devices int `json:"devices"`
}

const profileCols = `p.id, p.name, p.description, p.enabled, p.group_ids,
	COALESCE((SELECT array_agg(pi.configuration_id ORDER BY pi.configuration_id) FROM configuration_profile_items pi WHERE pi.profile_id = p.id), '{}'),
	p.created_by, p.updated_by, p.created_at, p.updated_at,
	(SELECT count(*) FROM devices d WHERE d.group_id = ANY(p.group_ids) AND ($1 OR d.group_id = ANY($2)))::int`

func scanProfile(row pgx.Row) (ConfigProfile, error) {
	var p ConfigProfile
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.Enabled, &p.GroupIDs, &p.ConfigIDs, &p.CreatedBy, &p.UpdatedBy,
		&p.CreatedAt, &p.UpdatedAt, &p.Devices)
	p.GroupIDs, p.ConfigIDs = groupIDs(p.GroupIDs), groupIDs(p.ConfigIDs)
	return p, mapErr(err)
}

// ListConfigProfiles returns every profile.
func (s *Store) ListConfigProfiles(ctx context.Context, sc Scope) ([]ConfigProfile, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+profileCols+" FROM configuration_profiles p ORDER BY lower(p.name)", sc.All, sc.groups())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ConfigProfile{}
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ConfigProfileByID returns one profile.
func (s *Store) ConfigProfileByID(ctx context.Context, id int64, sc Scope) (ConfigProfile, error) {
	return scanProfile(s.pool.QueryRow(ctx, "SELECT "+profileCols+" FROM configuration_profiles p WHERE p.id = $3", sc.All, sc.groups(), id))
}

// CreateConfigProfile inserts a profile with its configurations.
func (s *Store) CreateConfigProfile(ctx context.Context, p ConfigProfile) (ConfigProfile, error) {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO configuration_profiles (name, description, enabled, group_ids, created_by, updated_by)
			VALUES ($1, $2, $3, $4, $5, $5) RETURNING id`, p.Name, p.Description, p.Enabled, groupIDs(p.GroupIDs), p.CreatedBy).Scan(&p.ID); err != nil {
			return mapErr(err)
		}
		return setProfileConfigs(ctx, tx, p.ID, p.ConfigIDs)
	})
	if err != nil {
		return p, err
	}
	return s.ConfigProfileByID(ctx, p.ID, Scope{All: true})
}

// UpdateConfigProfile changes a profile and its configurations.
func (s *Store) UpdateConfigProfile(ctx context.Context, p ConfigProfile) (ConfigProfile, error) {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE configuration_profiles SET name = $2, description = $3, enabled = $4, group_ids = $5, updated_by = $6,
			updated_at = now() WHERE id = $1`, p.ID, p.Name, p.Description, p.Enabled, groupIDs(p.GroupIDs), p.UpdatedBy)
		if err != nil {
			return mapErr(err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return setProfileConfigs(ctx, tx, p.ID, p.ConfigIDs)
	})
	if err != nil {
		return p, err
	}
	return s.ConfigProfileByID(ctx, p.ID, Scope{All: true})
}

// DeleteConfigProfile removes a profile. Its configurations stay.
func (s *Store) DeleteConfigProfile(ctx context.Context, id int64) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "DELETE FROM configuration_profiles WHERE id = $1", id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return refreshConfigGroups(ctx, tx)
	})
}

func setProfileConfigs(ctx context.Context, tx pgx.Tx, id int64, configs []int64) error {
	if _, err := tx.Exec(ctx, "DELETE FROM configuration_profile_items WHERE profile_id = $1 AND NOT configuration_id = ANY($2)", id, groupIDs(configs)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO configuration_profile_items (profile_id, configuration_id)
		SELECT $1, c.id FROM configurations c WHERE c.id = ANY($2) ON CONFLICT DO NOTHING`, id, groupIDs(configs)); err != nil {
		return err
	}
	return refreshConfigGroups(ctx, tx)
}
