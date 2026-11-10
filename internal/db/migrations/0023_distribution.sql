-- Distribution servers: caches in branch offices that keep approved
-- software, drivers and agent updates close to the agents. Each one
-- connects out to this server with its own Ed25519 key, learned at
-- enrollment with a one-time token.

CREATE TABLE distribution_servers (
    id                bigserial PRIMARY KEY,
    name              text NOT NULL,
    enabled           boolean NOT NULL DEFAULT true,
    -- The address agents use, e.g. https://10.1.0.5:8445; filled in at
    -- enrollment when left empty.
    url               text NOT NULL DEFAULT '',
    -- Agents with an address in one of these networks (local or the public
    -- address the server sees) use this server.
    networks          text[] NOT NULL DEFAULT '{}',
    bandwidth_kbps    int NOT NULL DEFAULT 0,      -- download limit from this server, 0 = none
    public_key        bytea UNIQUE,                -- Ed25519, NULL until enrolled
    token_hash        bytea,                       -- one-time enrollment token
    token_expires_at  timestamptz,
    cert_sha256       text NOT NULL DEFAULT '',    -- pinned by agents
    hostname          text NOT NULL DEFAULT '',
    version           text NOT NULL DEFAULT '',
    status            jsonb,
    update_version    text NOT NULL DEFAULT '',    -- requested release, cleared once installed
    enrolled_at       timestamptz,
    last_seen_at      timestamptz,
    created_by        text NOT NULL DEFAULT '',
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX distribution_servers_name ON distribution_servers (lower(name));
CREATE UNIQUE INDEX distribution_servers_token ON distribution_servers (token_hash) WHERE token_hash IS NOT NULL;

-- Devices in these groups use the server too, wherever they are.
CREATE TABLE distribution_server_groups (
    server_id  bigint NOT NULL REFERENCES distribution_servers (id) ON DELETE CASCADE,
    group_id   bigint NOT NULL REFERENCES device_groups (id) ON DELETE CASCADE,
    PRIMARY KEY (server_id, group_id)
);
