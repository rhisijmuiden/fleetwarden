-- Phase 10: driver and firmware updates through FleetWarden's own
-- repository. Agents report which driver and firmware updates their platform
-- offers (Windows Update, fwupd). Nothing is downloaded until an
-- administrator adds an update to the repository; the server then fetches
-- the files from the vendor and keeps them under DataDir/repository/<sha256>.
-- Patch jobs install repository updates; agents fetch them from the server.

CREATE TABLE driver_updates (
    id             bigserial PRIMARY KEY,
    source         text NOT NULL CHECK (source IN ('windows-update', 'fwupd')),
    source_id      text NOT NULL,
    kind           text NOT NULL DEFAULT 'driver' CHECK (kind IN ('driver', 'firmware')),
    platform       text NOT NULL DEFAULT '',
    title          text NOT NULL DEFAULT '',
    vendor         text NOT NULL DEFAULT '',
    class          text NOT NULL DEFAULT '',
    version        text NOT NULL DEFAULT '',
    released_at    timestamptz,
    size_bytes     bigint NOT NULL DEFAULT 0,
    hardware_id    text NOT NULL DEFAULT '',
    description    text NOT NULL DEFAULT '',
    reboot         boolean NOT NULL DEFAULT false,
    -- Where the vendor offers the files: [{name, url, size, sha256, sha1}].
    -- Once stored, each file also has "stored": its SHA-256 in the repository.
    files          jsonb NOT NULL DEFAULT '[]',
    -- available: offered, not in the repository; queued: added, waiting for
    -- the download; downloading; ready: in the repository; failed: the
    -- download failed (error says why); rejected: hidden from the list.
    state          text NOT NULL DEFAULT 'available'
                   CHECK (state IN ('available', 'queued', 'downloading', 'ready', 'failed', 'rejected')),
    error          text NOT NULL DEFAULT '',
    stored_bytes   bigint NOT NULL DEFAULT 0,
    attempts       int NOT NULL DEFAULT 0,
    decided_by     text NOT NULL DEFAULT '',
    decided_at     timestamptz,
    stored_at      timestamptz,
    first_seen_at  timestamptz NOT NULL DEFAULT now(),
    last_seen_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source, source_id)
);
CREATE INDEX driver_updates_state ON driver_updates (state);

-- Which device can install which update, from the latest scan.
CREATE TABLE device_drivers (
    device_id         uuid NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    driver_update_id  bigint NOT NULL REFERENCES driver_updates (id) ON DELETE CASCADE,
    device_name       text NOT NULL DEFAULT '',
    fw_device_id      text NOT NULL DEFAULT '',
    current_version   text NOT NULL DEFAULT '',
    first_seen_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (device_id, driver_update_id)
);
CREATE INDEX device_drivers_update ON device_drivers (driver_update_id);

ALTER TABLE devices
    ADD COLUMN driver_scan_at    timestamptz,
    ADD COLUMN driver_scan_error text NOT NULL DEFAULT '';

-- Per patch policy: install repository drivers and/or firmware in the window.
ALTER TABLE patch_policies
    ADD COLUMN drivers  boolean NOT NULL DEFAULT true,
    ADD COLUMN firmware boolean NOT NULL DEFAULT false;

-- Drivers are no longer patches: Windows Update drivers only reach devices
-- through the repository now.
DELETE FROM device_patches WHERE category = 'driver';
