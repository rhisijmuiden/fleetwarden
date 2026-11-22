package store

import (
	"context"
	"time"
)

// MCPToken is a token an AI assistant uses to reach the MCP server. It acts
// as the user who made it, limited to its access level.
type MCPToken struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"userId"`
	Username   string     `json:"username"`
	Name       string     `json:"name"`
	Kind       string     `json:"kind"` // personal or oauth
	ClientID   *string    `json:"clientId"`
	Access     string     `json:"access"` // read, write or full
	Prefix     string     `json:"prefix"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	LastIP     string     `json:"lastIp"`
	// RefreshExpiresAt is when an OAuth connection ends unless the assistant
	// refreshes it.
	RefreshExpiresAt *time.Time `json:"refreshExpiresAt"`
}

// MCPClient is an AI assistant that registered itself for OAuth.
type MCPClient struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	RedirectURIs []string  `json:"redirectUris"`
	CreatedAt    time.Time `json:"createdAt"`
}

const mcpTokenCols = `t.id, t.user_id, u.username, t.name, t.kind, t.client_id, t.access, t.prefix, t.expires_at,
	t.created_at, t.last_used_at, t.last_ip, t.refresh_expires_at`

func scanMCPToken(row interface{ Scan(...any) error }) (MCPToken, error) {
	var t MCPToken
	err := row.Scan(&t.ID, &t.UserID, &t.Username, &t.Name, &t.Kind, &t.ClientID, &t.Access, &t.Prefix, &t.ExpiresAt,
		&t.CreatedAt, &t.LastUsedAt, &t.LastIP, &t.RefreshExpiresAt)
	return t, mapErr(err)
}

// CreateMCPToken stores a token; only hashes of the secrets are kept.
func (s *Store) CreateMCPToken(ctx context.Context, t MCPToken, token, refresh string) (MCPToken, error) {
	var refreshHash []byte
	if refresh != "" {
		refreshHash = HashToken(refresh)
	}
	err := s.pool.QueryRow(ctx, `INSERT INTO mcp_tokens (user_id, name, kind, client_id, access, prefix, token_hash, expires_at, refresh_hash, refresh_expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id, created_at`,
		t.UserID, t.Name, t.Kind, t.ClientID, t.Access, t.Prefix, HashToken(token), t.ExpiresAt, refreshHash, t.RefreshExpiresAt).
		Scan(&t.ID, &t.CreatedAt)
	return t, mapErr(err)
}

// MCPTokenBySecret returns the token with this secret when it has not expired.
func (s *Store) MCPTokenBySecret(ctx context.Context, token string) (MCPToken, error) {
	return scanMCPToken(s.pool.QueryRow(ctx, `SELECT `+mcpTokenCols+` FROM mcp_tokens t JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = $1 AND (t.expires_at IS NULL OR t.expires_at > now())`, HashToken(token)))
}

// RefreshMCPToken swaps the secrets of the OAuth connection that holds this
// refresh token (for this client), so an old refresh token works only once.
func (s *Store) RefreshMCPToken(ctx context.Context, refresh, clientID, newToken, newRefresh string, expires, refreshExpires time.Time) (MCPToken, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `UPDATE mcp_tokens SET token_hash = $3, refresh_hash = $4, expires_at = $5, refresh_expires_at = $6
		WHERE refresh_hash = $1 AND client_id = $2 AND refresh_expires_at > now() RETURNING id`,
		HashToken(refresh), clientID, HashToken(newToken), HashToken(newRefresh), expires, refreshExpires).Scan(&id)
	if err != nil {
		return MCPToken{}, mapErr(err)
	}
	return s.MCPTokenByID(ctx, id)
}

// MCPTokenByID returns one token.
func (s *Store) MCPTokenByID(ctx context.Context, id int64) (MCPToken, error) {
	return scanMCPToken(s.pool.QueryRow(ctx, `SELECT `+mcpTokenCols+` FROM mcp_tokens t JOIN users u ON u.id = t.user_id WHERE t.id = $1`, id))
}

// ListMCPTokens returns the tokens of one user, or of everyone for userID 0.
// Expired OAuth connections are left out.
func (s *Store) ListMCPTokens(ctx context.Context, userID int64) ([]MCPToken, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+mcpTokenCols+` FROM mcp_tokens t JOIN users u ON u.id = t.user_id
		WHERE ($1 = 0 OR t.user_id = $1) AND (t.kind = 'personal' OR t.refresh_expires_at > now())
		ORDER BY t.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MCPToken{}
	for rows.Next() {
		t, err := scanMCPToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteMCPToken revokes a token. With userID set, only that user's token.
func (s *Store) DeleteMCPToken(ctx context.Context, id, userID int64) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM mcp_tokens WHERE id = $1 AND ($2 = 0 OR user_id = $2)", id, userID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// RevokeMCPRefresh ends the OAuth connection holding this refresh token.
func (s *Store) RevokeMCPRefresh(ctx context.Context, refresh string) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM mcp_tokens WHERE refresh_hash = $1 OR token_hash = $1", HashToken(refresh))
	return err
}

// TouchMCPToken records when and from where a token was last used.
func (s *Store) TouchMCPToken(ctx context.Context, id int64, ip string) error {
	_, err := s.pool.Exec(ctx, "UPDATE mcp_tokens SET last_used_at = now(), last_ip = $2 WHERE id = $1", id, ip)
	return err
}

// PruneMCP removes ended OAuth connections and registered clients that never
// got a connection within a day.
func (s *Store) PruneMCP(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, "DELETE FROM mcp_tokens WHERE kind = 'oauth' AND refresh_expires_at < now()"); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM mcp_clients c WHERE c.created_at < now() - interval '1 day'
		AND NOT EXISTS (SELECT 1 FROM mcp_tokens t WHERE t.client_id = c.id)`)
	return err
}

// CreateMCPClient registers an assistant for OAuth.
func (s *Store) CreateMCPClient(ctx context.Context, c MCPClient, ip string) (MCPClient, error) {
	err := s.pool.QueryRow(ctx, "INSERT INTO mcp_clients (id, name, redirect_uris, ip) VALUES ($1, $2, $3, $4) RETURNING created_at",
		c.ID, c.Name, c.RedirectURIs, ip).Scan(&c.CreatedAt)
	return c, mapErr(err)
}

// MCPClientByID returns a registered assistant.
func (s *Store) MCPClientByID(ctx context.Context, id string) (MCPClient, error) {
	var c MCPClient
	err := s.pool.QueryRow(ctx, "SELECT id, name, redirect_uris, created_at FROM mcp_clients WHERE id = $1", id).
		Scan(&c.ID, &c.Name, &c.RedirectURIs, &c.CreatedAt)
	return c, mapErr(err)
}

// CountMCPClients counts the registered assistants.
func (s *Store) CountMCPClients(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, "SELECT count(*) FROM mcp_clients").Scan(&n)
	return n, err
}
