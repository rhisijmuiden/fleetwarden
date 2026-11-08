-- Phase 9: configuration management. A configuration (browser policies,
-- registry values, a script, ...) is assigned to device groups. Agents apply
-- the configurations of their group, check them every few minutes and put
-- back what was changed. They report the state per configuration.

CREATE TABLE configurations (
    id          bigserial PRIMARY KEY,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    type        text NOT NULL,
    platform    text NOT NULL CHECK (platform IN ('windows', 'linux')),
    -- The settings of the type, as the agent receives them.
    settings    jsonb NOT NULL DEFAULT '{}',
    enabled     boolean NOT NULL DEFAULT true,
    group_ids   bigint[] NOT NULL DEFAULT '{}',
    -- Lower numbers apply first; a later configuration wins when two set the same thing.
    priority    integer NOT NULL DEFAULT 100 CHECK (priority BETWEEN 0 AND 1000),
    -- Goes up with every change of the settings, so agents run a changed script again.
    revision    integer NOT NULL DEFAULT 1,
    created_by  text NOT NULL DEFAULT '',
    updated_by  text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX configurations_name ON configurations (lower(name));
CREATE INDEX configurations_groups ON configurations USING gin (group_ids);

-- The state of each configuration on each device, from the agent's reports.
CREATE TABLE device_configurations (
    device_id        uuid NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    configuration_id bigint NOT NULL REFERENCES configurations (id) ON DELETE CASCADE,
    revision         integer NOT NULL DEFAULT 0,
    state            text NOT NULL DEFAULT 'pending',
    message          text NOT NULL DEFAULT '',
    -- Settings that were changed outside FleetWarden and put back.
    repaired_total   integer NOT NULL DEFAULT 0,
    repaired_at      timestamptz,
    applied_at       timestamptz,
    reported_at      timestamptz NOT NULL DEFAULT now(),
    -- The last script run.
    run_at           timestamptz,
    run_exit_code    integer,
    run_output       text NOT NULL DEFAULT '',
    run_error        text NOT NULL DEFAULT '',
    PRIMARY KEY (device_id, configuration_id)
);
CREATE INDEX device_configurations_config ON device_configurations (configuration_id);

-- Every script run, for the job log.
CREATE TABLE configuration_runs (
    id               bigserial PRIMARY KEY,
    device_id        uuid NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    configuration_id bigint NOT NULL REFERENCES configurations (id) ON DELETE CASCADE,
    revision         integer NOT NULL DEFAULT 0,
    run_at           timestamptz NOT NULL,
    username         text NOT NULL DEFAULT '',
    exit_code        integer NOT NULL DEFAULT 0,
    output           text NOT NULL DEFAULT '',
    error            text NOT NULL DEFAULT '',
    duration_ms      bigint NOT NULL DEFAULT 0
);
CREATE INDEX configuration_runs_config ON configuration_runs (configuration_id, id DESC);
CREATE INDEX configuration_runs_device ON configuration_runs (device_id, id DESC);
