-- Remote control: who may see a device's screen how, and a log of every
-- session.
ALTER TABLE device_groups ADD COLUMN remote_consent text NOT NULL DEFAULT 'auto'
    CHECK (remote_consent IN ('auto', 'direct', 'notify', 'ask'));

CREATE TABLE remote_sessions (
    id           uuid PRIMARY KEY,
    device_id    uuid NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    user_id      bigint REFERENCES users (id) ON DELETE SET NULL,
    operator     text NOT NULL,           -- user name at the time
    kind         text NOT NULL CHECK (kind IN ('desktop', 'terminal')),
    consent      text NOT NULL CHECK (consent IN ('direct', 'notify', 'ask')),
    state        text NOT NULL DEFAULT 'pending'
                 CHECK (state IN ('pending', 'consent', 'connecting', 'active', 'ended', 'denied', 'failed')),
    reason       text NOT NULL DEFAULT '',
    remote_ip    text NOT NULL DEFAULT '', -- the operator's address
    created_at   timestamptz NOT NULL DEFAULT now(),
    connected_at timestamptz,
    ended_at     timestamptz,
    bytes_down   bigint NOT NULL DEFAULT 0, -- screen data sent to the operator
    bytes_up     bigint NOT NULL DEFAULT 0
);
CREATE INDEX remote_sessions_device ON remote_sessions (device_id, created_at DESC);
CREATE INDEX remote_sessions_created ON remote_sessions (created_at DESC);
CREATE INDEX remote_sessions_open ON remote_sessions (id) WHERE ended_at IS NULL;
