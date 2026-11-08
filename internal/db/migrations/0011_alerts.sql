-- Phase 7b: alerts. Agents send their volumes with every heartbeat; alert
-- rules open an alert per device (and per disk) and close it again when the
-- condition is gone.

ALTER TABLE devices ADD COLUMN volumes jsonb;          -- from the last heartbeat
ALTER TABLE devices ADD COLUMN volumes_at timestamptz;

CREATE TABLE alert_rules (
    id              bigserial PRIMARY KEY,
    name            text NOT NULL,
    enabled         boolean NOT NULL DEFAULT true,
    kind            text NOT NULL CHECK (kind IN ('offline', 'disk')),
    -- offline: minutes without contact; disk: percentage free space.
    threshold       integer NOT NULL,
    -- Device groups and kinds (workstation, server, hypervisor); empty is all.
    group_ids       bigint[] NOT NULL DEFAULT '{}',
    device_kinds    text[] NOT NULL DEFAULT '{}',
    recipients      text[] NOT NULL DEFAULT '{}',
    language        text NOT NULL DEFAULT 'en' CHECK (language IN ('en', 'nl')),
    notify_resolved boolean NOT NULL DEFAULT true,
    created_by      text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX alert_rules_name ON alert_rules (lower(name));

CREATE TABLE alerts (
    id              bigserial PRIMARY KEY,
    rule_id         bigint NOT NULL REFERENCES alert_rules (id) ON DELETE CASCADE,
    device_id       uuid NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    kind            text NOT NULL,
    subject         text NOT NULL DEFAULT '', -- the disk, for disk alerts
    details         jsonb NOT NULL DEFAULT '{}',
    opened_at       timestamptz NOT NULL DEFAULT now(),
    resolved_at     timestamptz,
    acknowledged_at timestamptz,
    acknowledged_by text NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX alerts_open ON alerts (rule_id, device_id, subject) WHERE resolved_at IS NULL;
CREATE INDEX alerts_device ON alerts (device_id);
CREATE INDEX alerts_opened ON alerts (opened_at DESC);

-- Two rules to start with; they only show in the web interface until
-- someone adds recipients.
INSERT INTO alert_rules (name, kind, threshold, device_kinds) VALUES
    ('Server offline', 'offline', 15, '{server,hypervisor}'),
    ('Low disk space', 'disk', 10, '{}');
