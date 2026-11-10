-- Chat between a technician and the user of a device, and unattended
-- takeover (connect when nobody answers the consent prompt).

ALTER TABLE device_groups ADD COLUMN remote_unattended boolean NOT NULL DEFAULT false;

CREATE TABLE chats (
    id              bigserial PRIMARY KEY,
    device_id       uuid NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    help_request_id bigint REFERENCES help_requests (id) ON DELETE SET NULL,
    started_by      text NOT NULL,          -- username of the technician
    operator        text NOT NULL,          -- display name shown to the user
    username        text NOT NULL DEFAULT '', -- the Windows account that answered
    started_at      timestamptz NOT NULL DEFAULT now(),
    ended_at        timestamptz,
    ended_by        text NOT NULL DEFAULT '',
    delivered_at    timestamptz,            -- a tray showed the chat
    trays           int NOT NULL DEFAULT 0
);

-- At most one open chat per device.
CREATE UNIQUE INDEX chats_open ON chats (device_id) WHERE ended_at IS NULL;
CREATE INDEX chats_device ON chats (device_id, started_at DESC);

CREATE TABLE chat_messages (
    id         bigserial PRIMARY KEY,
    chat_id    bigint NOT NULL REFERENCES chats (id) ON DELETE CASCADE,
    sender     text NOT NULL CHECK (sender IN ('tech', 'user')),
    name       text NOT NULL,
    body       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX chat_messages_chat ON chat_messages (chat_id, id);
