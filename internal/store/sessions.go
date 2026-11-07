package store

import (
	"context"
	"time"
)

// Session is a browser login.
type Session struct {
	UserID     int64
	MFAPending bool
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// CreateSession stores a session for the token.
func (s *Store) CreateSession(ctx context.Context, token string, userID int64, mfaPending bool, expires time.Time, ip, ua string) error {
	if len(ua) > 512 {
		ua = ua[:512]
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, mfa_pending, expires_at, ip, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6)`, HashToken(token), userID, mfaPending, expires, ip, ua)
	return err
}

// SessionByToken returns a session that has not expired.
func (s *Store) SessionByToken(ctx context.Context, token string) (Session, error) {
	var ss Session
	err := s.pool.QueryRow(ctx, `SELECT user_id, mfa_pending, created_at, last_seen_at, expires_at FROM sessions
		WHERE token_hash = $1 AND expires_at > now()`, HashToken(token)).
		Scan(&ss.UserID, &ss.MFAPending, &ss.CreatedAt, &ss.LastSeenAt, &ss.ExpiresAt)
	return ss, mapErr(err)
}

// TouchSession extends the idle timeout (never past the absolute expiry set at creation).
func (s *Store) TouchSession(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx, "UPDATE sessions SET last_seen_at = now() WHERE token_hash = $1", HashToken(token))
	return err
}

// CompleteMFA marks the second factor as done.
func (s *Store) CompleteMFA(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx, "UPDATE sessions SET mfa_pending = false, last_seen_at = now() WHERE token_hash = $1", HashToken(token))
	return err
}

// DeleteSession logs a session out.
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM sessions WHERE token_hash = $1", HashToken(token))
	return err
}

// DeleteUserSessions logs a user out everywhere, except the session with keepToken.
func (s *Store) DeleteUserSessions(ctx context.Context, userID int64, keepToken string) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM sessions WHERE user_id = $1 AND token_hash <> $2", userID, HashToken(keepToken))
	return err
}

// PurgeSessions removes expired and idle sessions.
func (s *Store) PurgeSessions(ctx context.Context, idle time.Duration) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM sessions WHERE expires_at < now() OR last_seen_at < now() - make_interval(secs => $1)", idle.Seconds())
	return err
}
