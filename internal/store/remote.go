package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// RemoteSession is one remote control session in the log.
type RemoteSession struct {
	ID          string     `json:"id"`
	DeviceID    string     `json:"deviceId"`
	Hostname    string     `json:"hostname"`
	GroupID     int64      `json:"groupId"`
	UserID      *int64     `json:"userId"`
	Operator    string     `json:"operator"`
	Kind        string     `json:"kind"`
	Consent     string     `json:"consent"`
	State       string     `json:"state"`
	Reason      string     `json:"reason"`
	RemoteIP    string     `json:"remoteIp"`
	CreatedAt   time.Time  `json:"createdAt"`
	ConnectedAt *time.Time `json:"connectedAt"`
	EndedAt     *time.Time `json:"endedAt"`
	BytesDown   int64      `json:"bytesDown"`
	BytesUp     int64      `json:"bytesUp"`
}

const remoteCols = `r.id, r.device_id, d.hostname, d.group_id, r.user_id, r.operator, r.kind, r.consent, r.state, r.reason, r.remote_ip,
	r.created_at, r.connected_at, r.ended_at, r.bytes_down, r.bytes_up`

func scanRemote(row pgx.Row) (RemoteSession, error) {
	var r RemoteSession
	err := row.Scan(&r.ID, &r.DeviceID, &r.Hostname, &r.GroupID, &r.UserID, &r.Operator, &r.Kind, &r.Consent, &r.State, &r.Reason,
		&r.RemoteIP, &r.CreatedAt, &r.ConnectedAt, &r.EndedAt, &r.BytesDown, &r.BytesUp)
	return r, mapErr(err)
}

// CreateRemoteSession logs a new session.
func (s *Store) CreateRemoteSession(ctx context.Context, r RemoteSession) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO remote_sessions (id, device_id, user_id, operator, kind, consent, remote_ip)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, r.ID, r.DeviceID, r.UserID, r.Operator, r.Kind, r.Consent, r.RemoteIP)
	return mapErr(err)
}

// RemoteSessionByID returns a session.
func (s *Store) RemoteSessionByID(ctx context.Context, id string) (RemoteSession, error) {
	return scanRemote(s.pool.QueryRow(ctx, "SELECT "+remoteCols+" FROM remote_sessions r JOIN devices d ON d.id = r.device_id WHERE r.id = $1", id))
}

// SetRemoteState records progress of an open session; "active" also sets
// the connection time.
func (s *Store) SetRemoteState(ctx context.Context, id, state string) error {
	_, err := s.pool.Exec(ctx, `UPDATE remote_sessions SET state = $2,
		connected_at = CASE WHEN $2 = 'active' AND connected_at IS NULL THEN now() ELSE connected_at END
		WHERE id = $1 AND ended_at IS NULL AND state <> 'active'`, id, state)
	return mapErr(err)
}

// EndRemoteSession closes a session with its final state. It reports false
// when the session had already ended.
func (s *Store) EndRemoteSession(ctx context.Context, id, state, reason string, down, up int64) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE remote_sessions SET state = $2, reason = $3, ended_at = now(), bytes_down = $4, bytes_up = $5
		WHERE id = $1 AND ended_at IS NULL`, id, state, reason, down, up)
	if err != nil {
		return false, mapErr(err)
	}
	return tag.RowsAffected() > 0, nil
}

// EndInterruptedRemoteSessions closes sessions that were open when the server
// stopped.
func (s *Store) EndInterruptedRemoteSessions(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `UPDATE remote_sessions SET state = CASE WHEN state = 'active' THEN 'ended' ELSE 'failed' END,
		reason = 'server_restart', ended_at = now() WHERE ended_at IS NULL`)
	return mapErr(err)
}

// RemoteQuery filters the session log.
type RemoteQuery struct {
	Scope    Scope
	DeviceID string
	Open     bool // only sessions that have not ended
	Limit    int
	Offset   int
}

// ListRemoteSessions returns sessions, newest first, and the total count.
func (s *Store) ListRemoteSessions(ctx context.Context, q RemoteQuery) ([]RemoteSession, int, error) {
	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 50
	}
	where := `($1 OR d.group_id = ANY($2)) AND ($3 = '' OR r.device_id::text = $3) AND (NOT $4 OR r.ended_at IS NULL)`
	args := []any{q.Scope.All, q.Scope.groups(), q.DeviceID, q.Open}
	var total int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM remote_sessions r JOIN devices d ON d.id = r.device_id WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, mapErr(err)
	}
	rows, err := s.pool.Query(ctx, "SELECT "+remoteCols+" FROM remote_sessions r JOIN devices d ON d.id = r.device_id WHERE "+where+
		" ORDER BY r.created_at DESC LIMIT $5 OFFSET $6", append(args, q.Limit, max(q.Offset, 0))...)
	if err != nil {
		return nil, 0, mapErr(err)
	}
	defer rows.Close()
	out := []RemoteSession{}
	for rows.Next() {
		r, err := scanRemote(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// PurgeRemoteSessions deletes log entries older than age.
func (s *Store) PurgeRemoteSessions(ctx context.Context, age time.Duration) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM remote_sessions WHERE created_at < $1", time.Now().Add(-age))
	return mapErr(err)
}
