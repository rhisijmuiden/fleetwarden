-- Agent updates: releases are rolled out ring by ring (device_groups.update_ring).

CREATE TABLE agent_rollouts (
    id              bigserial PRIMARY KEY,
    version         text NOT NULL,
    channel         text NOT NULL CHECK (channel IN ('production', 'test')),
    status          text NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active', 'paused', 'completed', 'cancelled', 'superseded')),
    ring            smallint NOT NULL DEFAULT 0 CHECK (ring BETWEEN 0 AND 3),
    ring_started_at timestamptz NOT NULL DEFAULT now(),
    soak_hours      int NOT NULL DEFAULT 24 CHECK (soak_hours BETWEEN 0 AND 720),
    max_failures    int NOT NULL DEFAULT 2 CHECK (max_failures >= 0),
    automatic       boolean NOT NULL DEFAULT false,
    message         text NOT NULL DEFAULT '',
    created_by      text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX agent_rollouts_open ON agent_rollouts (channel) WHERE status IN ('active', 'paused');

ALTER TABLE devices
    ADD COLUMN update_rollout  bigint REFERENCES agent_rollouts (id) ON DELETE SET NULL,
    ADD COLUMN update_version  text NOT NULL DEFAULT '',
    -- sent, downloading, installing, completed, failed, rolled-back
    ADD COLUMN update_state    text NOT NULL DEFAULT '',
    ADD COLUMN update_message  text NOT NULL DEFAULT '',
    ADD COLUMN update_at       timestamptz,
    ADD COLUMN update_attempts int NOT NULL DEFAULT 0;
