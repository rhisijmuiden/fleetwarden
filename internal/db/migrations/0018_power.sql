-- Phase 14: power management. Wake-on-LAN through an online agent in the
-- same network, restarts and shutdowns (by hand or on a schedule), and
-- waking devices for the patch window and shutting them down afterwards.

-- Weekly schedules that wake, restart or shut down the devices of groups.
CREATE TABLE power_schedules (
    id                     bigserial PRIMARY KEY,
    name                   text NOT NULL,
    description            text NOT NULL DEFAULT '',
    enabled                boolean NOT NULL DEFAULT true,
    action                 text NOT NULL CHECK (action IN ('wake', 'restart', 'shutdown')),
    -- Days of the week (bit 0 = Sunday) and time in minutes after midnight,
    -- in time_zone ('' = server time).
    days                   smallint NOT NULL DEFAULT 31 CHECK (days BETWEEN 0 AND 127),
    at_minute              smallint NOT NULL DEFAULT 0 CHECK (at_minute BETWEEN 0 AND 1439),
    time_zone              text NOT NULL DEFAULT '',
    -- Restart and shut down: ask the logged-on user first.
    notify_users           boolean NOT NULL DEFAULT true,
    max_postpones          smallint NOT NULL DEFAULT 2 CHECK (max_postpones BETWEEN 0 AND 20),
    postpone_minutes       smallint NOT NULL DEFAULT 30 CHECK (postpone_minutes BETWEEN 5 AND 1440),
    countdown_seconds      smallint NOT NULL DEFAULT 300 CHECK (countdown_seconds BETWEEN 30 AND 3600),
    skip_if_user_logged_on boolean NOT NULL DEFAULT false,
    only_if_reboot_pending boolean NOT NULL DEFAULT false,
    -- The last time slot that was carried out.
    last_run_at            timestamptz,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX power_schedules_name ON power_schedules (lower(name));

CREATE TABLE power_schedule_groups (
    schedule_id bigint NOT NULL REFERENCES power_schedules (id) ON DELETE CASCADE,
    group_id    bigint NOT NULL REFERENCES device_groups (id) ON DELETE CASCADE,
    PRIMARY KEY (schedule_id, group_id)
);
CREATE INDEX power_schedule_groups_group ON power_schedule_groups (group_id);

-- Every wake, restart and shutdown that was sent, with its outcome.
CREATE TABLE power_actions (
    id              bigserial PRIMARY KEY,
    device_id       uuid NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    action          text NOT NULL CHECK (action IN ('wake', 'restart', 'shutdown')),
    trigger         text NOT NULL CHECK (trigger IN ('manual', 'schedule', 'patch-window', 'after-patch')),
    schedule_id     bigint REFERENCES power_schedules (id) ON DELETE SET NULL,
    state           text NOT NULL DEFAULT 'sent',
    message         text NOT NULL DEFAULT '',
    -- Wake: the devices that sent the magic packet (host names), and
    -- whether the server sent it too.
    relays          text[] NOT NULL DEFAULT '{}',
    postpones       int NOT NULL DEFAULT 0,
    postponed_until timestamptz,
    requested_by    text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    finished_at     timestamptz
);
CREATE INDEX power_actions_device ON power_actions (device_id, created_at DESC);
CREATE INDEX power_actions_open ON power_actions (device_id) WHERE finished_at IS NULL;
CREATE INDEX power_actions_created ON power_actions (created_at DESC);

-- Patch policies: wake the devices that are off when the window opens, and
-- shut down the devices that were woken once they are done.
ALTER TABLE patch_policies
    ADD COLUMN wake_for_window   boolean NOT NULL DEFAULT false,
    ADD COLUMN shutdown_after    boolean NOT NULL DEFAULT false;
