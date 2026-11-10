package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// DistributionServer is a cache in a branch office that serves approved
// files to the agents near it.
type DistributionServer struct {
	ID             int64           `json:"id"`
	Name           string          `json:"name"`
	Enabled        bool            `json:"enabled"`
	URL            string          `json:"url"`
	Networks       []string        `json:"networks"`
	GroupIDs       []int64         `json:"groupIds"`
	BandwidthKbps  int             `json:"bandwidthKbps"`
	PublicKey      []byte          `json:"-"`
	Enrolled       bool            `json:"enrolled"`
	TokenExpiresAt *time.Time      `json:"tokenExpiresAt"`
	CertSHA256     string          `json:"certSha256"`
	Hostname       string          `json:"hostname"`
	Version        string          `json:"version"`
	Status         json.RawMessage `json:"-"`
	UpdateVersion  string          `json:"updateVersion"`
	EnrolledAt     *time.Time      `json:"enrolledAt"`
	LastSeenAt     *time.Time      `json:"lastSeenAt"`
	CreatedBy      string          `json:"createdBy"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
}

const distCols = `d.id, d.name, d.enabled, d.url, d.networks, COALESCE((SELECT array_agg(g.group_id ORDER BY g.group_id)
	FROM distribution_server_groups g WHERE g.server_id = d.id), '{}'), d.bandwidth_kbps, d.public_key, d.token_expires_at,
	d.cert_sha256, d.hostname, d.version, d.status, d.update_version, d.enrolled_at, d.last_seen_at, d.created_by, d.created_at, d.updated_at`

func scanDist(row pgx.Row) (DistributionServer, error) {
	var d DistributionServer
	err := row.Scan(&d.ID, &d.Name, &d.Enabled, &d.URL, &d.Networks, &d.GroupIDs, &d.BandwidthKbps, &d.PublicKey, &d.TokenExpiresAt,
		&d.CertSHA256, &d.Hostname, &d.Version, &d.Status, &d.UpdateVersion, &d.EnrolledAt, &d.LastSeenAt, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt)
	d.Enrolled = len(d.PublicKey) > 0
	if d.Networks == nil {
		d.Networks = []string{}
	}
	if d.GroupIDs == nil {
		d.GroupIDs = []int64{}
	}
	return d, mapErr(err)
}

// ListDistributionServers returns every distribution server by name.
func (s *Store) ListDistributionServers(ctx context.Context) ([]DistributionServer, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+distCols+" FROM distribution_servers d ORDER BY lower(d.name)")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DistributionServer{}
	for rows.Next() {
		d, err := scanDist(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DistributionServerByID returns one distribution server.
func (s *Store) DistributionServerByID(ctx context.Context, id int64) (DistributionServer, error) {
	return scanDist(s.pool.QueryRow(ctx, "SELECT "+distCols+" FROM distribution_servers d WHERE d.id = $1", id))
}

// DistributionNameTaken reports whether another distribution server has the name.
func (s *Store) DistributionNameTaken(ctx context.Context, name string, exceptID int64) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx, "SELECT count(*) FROM distribution_servers WHERE lower(name) = lower($1) AND id <> $2",
		strings.TrimSpace(name), exceptID).Scan(&n)
	return n > 0, err
}

func setDistGroups(ctx context.Context, tx pgx.Tx, id int64, groups []int64) error {
	if _, err := tx.Exec(ctx, "DELETE FROM distribution_server_groups WHERE server_id = $1", id); err != nil {
		return err
	}
	if len(groups) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO distribution_server_groups (server_id, group_id)
		SELECT $1, g.id FROM device_groups g WHERE g.id = ANY($2) ON CONFLICT DO NOTHING`, id, groups)
	return err
}

// CreateDistributionServer stores a new distribution server with its
// enrollment token hash.
func (s *Store) CreateDistributionServer(ctx context.Context, d DistributionServer, tokenHash []byte, expires time.Time) (DistributionServer, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return d, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO distribution_servers (name, enabled, url, networks, bandwidth_kbps, token_hash, token_expires_at, created_by)
		VALUES ($1, true, $2, $3, $4, $5, $6, $7) RETURNING id`, strings.TrimSpace(d.Name), d.URL, d.Networks, d.BandwidthKbps,
		tokenHash, expires, d.CreatedBy).Scan(&id); err != nil {
		return d, mapErr(err)
	}
	if err := setDistGroups(ctx, tx, id, d.GroupIDs); err != nil {
		return d, err
	}
	if err := tx.Commit(ctx); err != nil {
		return d, err
	}
	return s.DistributionServerByID(ctx, id)
}

// UpdateDistributionServer changes the editable fields.
func (s *Store) UpdateDistributionServer(ctx context.Context, d DistributionServer) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	tag, err := tx.Exec(ctx, `UPDATE distribution_servers SET name = $2, enabled = $3, url = $4, networks = $5, bandwidth_kbps = $6,
		updated_at = now() WHERE id = $1`, d.ID, strings.TrimSpace(d.Name), d.Enabled, d.URL, d.Networks, d.BandwidthKbps)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := setDistGroups(ctx, tx, d.ID, d.GroupIDs); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// NewDistributionToken replaces the enrollment token. The server keeps its
// key until it enrolls again with the new token.
func (s *Store) NewDistributionToken(ctx context.Context, id int64, tokenHash []byte, expires time.Time) error {
	tag, err := s.pool.Exec(ctx, "UPDATE distribution_servers SET token_hash = $2, token_expires_at = $3, updated_at = now() WHERE id = $1",
		id, tokenHash, expires)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DistributionEnroll describes an enrolling distribution server.
type DistributionEnroll struct {
	PublicKey  []byte
	Hostname   string
	Version    string
	CertSHA256 string
	URL        string // used when no address was set
}

// EnrollDistributionServer uses a valid token once: it stores the key and
// clears the token. It returns ErrNotFound for an unknown or expired token.
func (s *Store) EnrollDistributionServer(ctx context.Context, tokenHash []byte, e DistributionEnroll) (DistributionServer, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `UPDATE distribution_servers SET public_key = $2, hostname = $3, version = $4, cert_sha256 = $5,
		url = CASE WHEN url = '' THEN $6 ELSE url END, token_hash = NULL, token_expires_at = NULL, enrolled_at = now(), last_seen_at = now(),
		status = NULL, update_version = '', updated_at = now()
		WHERE token_hash = $1 AND token_expires_at > now() RETURNING id`, tokenHash, e.PublicKey, e.Hostname, e.Version, e.CertSHA256, e.URL).Scan(&id)
	if err != nil {
		return DistributionServer{}, mapErr(err)
	}
	return s.DistributionServerByID(ctx, id)
}

// DistributionSeen stores a status report.
func (s *Store) DistributionSeen(ctx context.Context, id int64, version, hostname, certSHA string, status json.RawMessage) error {
	_, err := s.pool.Exec(ctx, `UPDATE distribution_servers SET version = $2, hostname = $3, cert_sha256 = $4, status = $5, last_seen_at = now(),
		update_version = CASE WHEN update_version = $2 THEN '' ELSE update_version END WHERE id = $1`, id, version, hostname, certSHA, status)
	return err
}

// RequestDistributionUpdate asks the server to install a release on its next report.
func (s *Store) RequestDistributionUpdate(ctx context.Context, id int64, version string) error {
	_, err := s.pool.Exec(ctx, "UPDATE distribution_servers SET update_version = $2 WHERE id = $1", id, version)
	return err
}

// DeleteDistributionServer removes a distribution server.
func (s *Store) DeleteDistributionServer(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM distribution_servers WHERE id = $1", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DistributedFile is a file a distribution server should keep.
type DistributedFile struct {
	SHA256 string
	Kind   string // software, driver
}

// DistributableFiles returns the uploaded installers and the driver files in
// the repository: what distribution servers keep besides agent packages.
func (s *Store) DistributableFiles(ctx context.Context) ([]DistributedFile, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT lower(file_sha256), 'software' FROM software_packages WHERE file_sha256 <> ''
		UNION SELECT DISTINCT lower(f->>'stored'), 'driver' FROM driver_updates u, jsonb_array_elements(u.files) f
			WHERE u.state = 'ready' AND COALESCE(f->>'stored', '') <> ''
		ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DistributedFile{}
	for rows.Next() {
		var f DistributedFile
		if err := rows.Scan(&f.SHA256, &f.Kind); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
