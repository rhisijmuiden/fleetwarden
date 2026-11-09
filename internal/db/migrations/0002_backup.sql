-- Phase 2: backup locations and the backup history.

CREATE TABLE backup_locations (
    id         bigserial PRIMARY KEY,
    name       text NOT NULL UNIQUE,
    type       text NOT NULL,
    enabled    boolean NOT NULL DEFAULT true,
    -- Location settings; secrets are encrypted with the server's secret key.
    config     jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE backup_runs (
    id             bigserial PRIMARY KEY,
    location_id    bigint REFERENCES backup_locations (id) ON DELETE SET NULL,
    location_name  text NOT NULL,
    trigger        text NOT NULL CHECK (trigger IN ('schedule', 'manual')),
    actor          text NOT NULL DEFAULT '',
    kind           text NOT NULL DEFAULT '',
    chain          text NOT NULL DEFAULT '',
    snapshot       text NOT NULL DEFAULT '',
    status         text NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
    started_at     timestamptz NOT NULL DEFAULT now(),
    finished_at    timestamptz,
    size_bytes     bigint NOT NULL DEFAULT 0,
    uploaded_bytes bigint NOT NULL DEFAULT 0,
    error          text NOT NULL DEFAULT ''
);
CREATE INDEX backup_runs_started ON backup_runs (started_at DESC);
CREATE INDEX backup_runs_location ON backup_runs (location_id, started_at DESC);
