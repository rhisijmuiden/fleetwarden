-- Phase 4: patch management. A patch policy (maintenance window, approval
-- rules per category, reboot and user notification settings) is assigned to
-- device groups. Agents report missing updates; the server starts one
-- install job per device in the window.

CREATE TABLE patch_policies (
    id                bigserial PRIMARY KEY,
    name              text NOT NULL,
    description       text NOT NULL DEFAULT '',
    enabled           boolean NOT NULL DEFAULT true,
    -- Maintenance window: days of the week (bit 0 = Sunday), start in minutes
    -- after midnight and length in minutes, in time_zone ('' = server time).
    window_days       smallint NOT NULL DEFAULT 127 CHECK (window_days BETWEEN 0 AND 127),
    window_start      smallint NOT NULL DEFAULT 1200 CHECK (window_start BETWEEN 0 AND 1439),
    window_minutes    smallint NOT NULL DEFAULT 240 CHECK (window_minutes BETWEEN 30 AND 1440),
    time_zone         text NOT NULL DEFAULT '',
    -- {"security": {"mode": "auto", "delayDays": 0}, ...}; mode auto, manual or never.
    approvals         jsonb NOT NULL DEFAULT '{}',
    reboot            text NOT NULL DEFAULT 'prompt' CHECK (reboot IN ('never', 'automatic', 'prompt')),
    notify_users      boolean NOT NULL DEFAULT true,
    max_postpones     smallint NOT NULL DEFAULT 3 CHECK (max_postpones BETWEEN 0 AND 20),
    postpone_minutes  smallint NOT NULL DEFAULT 60 CHECK (postpone_minutes BETWEEN 5 AND 1440),
    countdown_seconds smallint NOT NULL DEFAULT 300 CHECK (countdown_seconds BETWEEN 30 AND 3600),
    take_over         boolean NOT NULL DEFAULT true, -- turn off Windows' own automatic updates
    scan_hours        smallint NOT NULL DEFAULT 6 CHECK (scan_hours BETWEEN 1 AND 168),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX patch_policies_name ON patch_policies (lower(name));

ALTER TABLE device_groups ADD COLUMN patch_policy_id bigint REFERENCES patch_policies (id) ON DELETE SET NULL;

ALTER TABLE devices
    ADD COLUMN patch_scan_at           timestamptz,
    ADD COLUMN patch_scan_requested_at timestamptz,
    ADD COLUMN patch_scan_error        text NOT NULL DEFAULT '',
    ADD COLUMN patch_source            text NOT NULL DEFAULT '',
    ADD COLUMN patch_cluster           text NOT NULL DEFAULT '',
    ADD COLUMN patch_reboot_pending    boolean NOT NULL DEFAULT false;
CREATE INDEX devices_patch_cluster ON devices (patch_cluster) WHERE patch_cluster <> '';

-- Missing updates per device, from the latest scan.
CREATE TABLE device_patches (
    device_id       uuid NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    update_id       text NOT NULL,
    title           text NOT NULL DEFAULT '',
    kb              text NOT NULL DEFAULT '',
    category        text NOT NULL DEFAULT 'other',
    severity        text NOT NULL DEFAULT '',
    size_bytes      bigint NOT NULL DEFAULT 0,
    released_at     timestamptz,
    current_version text NOT NULL DEFAULT '',
    new_version     text NOT NULL DEFAULT '',
    reboot          boolean NOT NULL DEFAULT false,
    first_seen_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (device_id, update_id)
);
CREATE INDEX device_patches_update ON device_patches (update_id);

-- Manual decisions that override the policy rules, for every device.
CREATE TABLE patch_decisions (
    update_id  text PRIMARY KEY,
    decision   text NOT NULL CHECK (decision IN ('approved', 'declined')),
    decided_by text NOT NULL DEFAULT '',
    decided_at timestamptz NOT NULL DEFAULT now()
);

-- Install jobs.
CREATE TABLE patch_runs (
    id              bigserial PRIMARY KEY,
    device_id       uuid NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    policy_id       bigint REFERENCES patch_policies (id) ON DELETE SET NULL,
    trigger         text NOT NULL CHECK (trigger IN ('window', 'manual')),
    state           text NOT NULL DEFAULT 'sent',
    message         text NOT NULL DEFAULT '',
    updates         text[] NOT NULL DEFAULT '{}',
    results         jsonb NOT NULL DEFAULT '[]',
    installed       int NOT NULL DEFAULT 0,
    failed          int NOT NULL DEFAULT 0,
    reboot_required boolean NOT NULL DEFAULT false,
    postpones       int NOT NULL DEFAULT 0,
    postponed_until timestamptz,
    requested_by    text NOT NULL DEFAULT '',
    started_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    finished_at     timestamptz
);
CREATE INDEX patch_runs_device ON patch_runs (device_id, started_at DESC);
CREATE INDEX patch_runs_open ON patch_runs (device_id) WHERE finished_at IS NULL;
