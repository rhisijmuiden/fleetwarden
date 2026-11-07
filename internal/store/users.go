package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// User is an account. Local accounts have a password; Entra accounts are
// created on first SSO login and keep their role in sync with Entra.
type User struct {
	ID                 int64      `json:"id"`
	Username           string     `json:"username"`
	DisplayName        string     `json:"displayName"`
	Email              string     `json:"email"`
	Source             string     `json:"source"`
	ExternalID         string     `json:"-"`
	PasswordHash       string     `json:"-"`
	MustChangePassword bool       `json:"mustChangePassword"`
	TOTPSecret         string     `json:"-"`
	TOTPEnabled        bool       `json:"totpEnabled"`
	TOTPLastStep       int64      `json:"-"`
	Role               string     `json:"role"`
	Language           string     `json:"language"`
	Disabled           bool       `json:"disabled"`
	BreakGlass         bool       `json:"breakGlass"`
	CreatedAt          time.Time  `json:"createdAt"`
	LastLoginAt        *time.Time `json:"lastLoginAt"`
}

const userCols = `id, username, display_name, email, source, COALESCE(external_id, ''), COALESCE(password_hash, ''),
	must_change_password, COALESCE(totp_secret, ''), totp_enabled, totp_last_step, role, language, disabled,
	break_glass, created_at, last_login_at`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Email, &u.Source, &u.ExternalID, &u.PasswordHash,
		&u.MustChangePassword, &u.TOTPSecret, &u.TOTPEnabled, &u.TOTPLastStep, &u.Role, &u.Language, &u.Disabled,
		&u.BreakGlass, &u.CreatedAt, &u.LastLoginAt)
	return u, mapErr(err)
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// CreateUser inserts a user and returns it with its ID.
func (s *Store) CreateUser(ctx context.Context, u User) (User, error) {
	row := s.pool.QueryRow(ctx, `INSERT INTO users (username, display_name, email, source, external_id, password_hash,
		must_change_password, role, language, break_glass)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING `+userCols,
		u.Username, u.DisplayName, u.Email, u.Source, nullable(u.ExternalID), nullable(u.PasswordHash),
		u.MustChangePassword, u.Role, defaultLang(u.Language), u.BreakGlass)
	return scanUser(row)
}

func defaultLang(l string) string {
	if l == "" {
		return "en"
	}
	return l
}

// UserByID returns a user.
func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, "SELECT "+userCols+" FROM users WHERE id = $1", id))
}

// UserByUsername returns a user by (case-insensitive) username.
func (s *Store) UserByUsername(ctx context.Context, username string) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, "SELECT "+userCols+" FROM users WHERE lower(username) = lower($1)", username))
}

// UserByExternalID returns an SSO user.
func (s *Store) UserByExternalID(ctx context.Context, id string) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, "SELECT "+userCols+" FROM users WHERE external_id = $1", id))
}

// BreakGlassUser returns the installer-created local administrator.
func (s *Store) BreakGlassUser(ctx context.Context) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, "SELECT "+userCols+" FROM users WHERE break_glass ORDER BY id LIMIT 1"))
}

// ListUsers returns all users ordered by username.
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+userCols+" FROM users ORDER BY lower(username)")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CountActiveAdmins counts enabled administrators, optionally excluding one user.
func (s *Store) CountActiveAdmins(ctx context.Context, exclude int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE role = 'administrator' AND NOT disabled AND id <> $1", exclude).Scan(&n)
	return n, err
}

// UpdateProfile changes the editable fields of a user.
func (s *Store) UpdateProfile(ctx context.Context, id int64, displayName, email, role string, disabled bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET display_name = $2, email = $3, role = $4, disabled = $5, updated_at = now()
		WHERE id = $1`, id, displayName, email, role, disabled)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SetPassword sets a password hash and whether it must be changed at next login.
func (s *Store) SetPassword(ctx context.Context, id int64, hash string, mustChange bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET password_hash = $2, must_change_password = $3, updated_at = now() WHERE id = $1`,
		id, hash, mustChange)
	return err
}

// SetTOTP stores the (encrypted) TOTP secret and whether it is active.
func (s *Store) SetTOTP(ctx context.Context, id int64, secret string, enabled bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET totp_secret = $2, totp_enabled = $3, totp_last_step = 0, updated_at = now() WHERE id = $1`,
		id, nullable(secret), enabled)
	return err
}

// UseTOTPStep records the last accepted TOTP step. It fails when the step was
// already used (concurrent replay).
func (s *Store) UseTOTPStep(ctx context.Context, id, step int64) (bool, error) {
	tag, err := s.pool.Exec(ctx, "UPDATE users SET totp_last_step = $2 WHERE id = $1 AND totp_last_step < $2", id, step)
	return err == nil && tag.RowsAffected() == 1, err
}

// SetLanguage stores the user's interface language.
func (s *Store) SetLanguage(ctx context.Context, id int64, lang string) error {
	_, err := s.pool.Exec(ctx, "UPDATE users SET language = $2, updated_at = now() WHERE id = $1", id, lang)
	return err
}

// SyncSSOUser updates an Entra account's details and role at login.
func (s *Store) SyncSSOUser(ctx context.Context, id int64, username, displayName, email, role string) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET username = $2, display_name = $3, email = $4, role = $5, updated_at = now()
		WHERE id = $1`, id, username, displayName, email, role)
	return mapErr(err)
}

// TouchLogin records a successful login.
func (s *Store) TouchLogin(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, "UPDATE users SET last_login_at = now() WHERE id = $1", id)
	return err
}

// DeleteUser removes a user and their sessions.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM users WHERE id = $1", id)
	return err
}

// AdminEmails returns the e-mail addresses of enabled administrators.
func (s *Store) AdminEmails(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, "SELECT email FROM users WHERE role = 'administrator' AND NOT disabled AND email <> ''")
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
