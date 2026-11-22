-- MCP server: AI assistants such as Claude reach FleetWarden through tokens
-- that act as the user who made them. Personal tokens are made in the web
-- interface; OAuth connections are made when a user approves an assistant.

CREATE TABLE mcp_clients (
    id            text PRIMARY KEY,
    name          text NOT NULL,
    redirect_uris text[] NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    ip            text NOT NULL DEFAULT ''
);

CREATE TABLE mcp_tokens (
    id                 bigserial PRIMARY KEY,
    user_id            bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name               text NOT NULL,
    kind               text NOT NULL CHECK (kind IN ('personal', 'oauth')),
    client_id          text REFERENCES mcp_clients (id) ON DELETE CASCADE,
    access             text NOT NULL CHECK (access IN ('read', 'write', 'full')),
    prefix             text NOT NULL DEFAULT '',
    token_hash         bytea NOT NULL UNIQUE,
    expires_at         timestamptz,
    refresh_hash       bytea UNIQUE,
    refresh_expires_at timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    last_used_at       timestamptz,
    last_ip            text NOT NULL DEFAULT ''
);
CREATE INDEX mcp_tokens_user_id ON mcp_tokens (user_id);
