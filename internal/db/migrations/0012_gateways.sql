-- Phase 8: secure gateways in the DMZ. The management server dials out to
-- each gateway and pins the key it learned at pairing.

CREATE TABLE gateways (
    id                bigserial PRIMARY KEY,
    name              text NOT NULL,
    address           text NOT NULL,           -- host:port of the tunnel listener
    public_url        text NOT NULL,           -- the URL agents use, e.g. https://agents.example.com
    public_key        bytea NOT NULL UNIQUE,   -- Ed25519, pinned at pairing
    enabled           boolean NOT NULL DEFAULT true,
    version           text NOT NULL DEFAULT '',
    last_connected_at timestamptz,
    last_error        text NOT NULL DEFAULT '',
    last_error_at     timestamptz,
    created_by        text NOT NULL DEFAULT '',
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX gateways_name ON gateways (lower(name));
