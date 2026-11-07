// Package store is the PostgreSQL data access layer.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned on a unique constraint violation.
var ErrConflict = errors.New("conflict")

// Store wraps the connection pool.
type Store struct{ pool *pgxpool.Pool }

// New creates a store.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Ping checks the database connection.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func mapErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return ErrConflict
	}
	return err
}

// ---- settings ----

// GetSetting decodes the JSON setting key into v. It returns ErrNotFound when unset.
func (s *Store) GetSetting(ctx context.Context, key string, v any) error {
	var raw []byte
	if err := s.pool.QueryRow(ctx, "SELECT value FROM settings WHERE key = $1", key).Scan(&raw); err != nil {
		return mapErr(err)
	}
	return json.Unmarshal(raw, v)
}

// PutSetting stores v as the JSON setting key.
func (s *Store) PutSetting(ctx context.Context, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, key, raw)
	return err
}

// ---- assets ----

// Asset is a stored binary such as the white-label logo.
type Asset struct {
	ContentType string
	Data        []byte
	UpdatedAt   time.Time
}

// GetAsset returns an asset by name.
func (s *Store) GetAsset(ctx context.Context, name string) (Asset, error) {
	var a Asset
	err := s.pool.QueryRow(ctx, "SELECT content_type, data, updated_at FROM assets WHERE name = $1", name).
		Scan(&a.ContentType, &a.Data, &a.UpdatedAt)
	return a, mapErr(err)
}

// PutAsset stores an asset.
func (s *Store) PutAsset(ctx context.Context, name, contentType string, data []byte) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO assets (name, content_type, data) VALUES ($1, $2, $3)
		ON CONFLICT (name) DO UPDATE SET content_type = EXCLUDED.content_type, data = EXCLUDED.data, updated_at = now()`,
		name, contentType, data)
	return err
}

// DeleteAsset removes an asset.
func (s *Store) DeleteAsset(ctx context.Context, name string) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM assets WHERE name = $1", name)
	return err
}

// HashToken hashes a session token for storage, so a database leak does not
// expose usable session cookies.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
