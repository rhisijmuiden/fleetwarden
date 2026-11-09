-- Phase 10b: BIOS updates from the Dell, HP and Lenovo catalogs, and
-- driver packages administrators upload themselves.
--
-- The server reads the vendor catalogs (metadata only) for the models among
-- its devices and offers the newest BIOS a device can take, like any other
-- repository update: nothing is downloaded until an administrator adds it.
-- Uploaded packages are stored right away and offered to the devices their
-- target matches.

ALTER TABLE driver_updates DROP CONSTRAINT driver_updates_source_check;
ALTER TABLE driver_updates ADD CONSTRAINT driver_updates_source_check
    CHECK (source IN ('windows-update', 'fwupd', 'dell', 'hp', 'lenovo', 'upload'));

ALTER TABLE driver_updates
    -- How the agent installs it: inf, fwupd, dell, hp, lenovo, exe.
    ADD COLUMN installer   text NOT NULL DEFAULT '',
    -- The vendor's silent install command (HP, Lenovo) or the arguments of
    -- an uploaded program, and the exit codes that mean success.
    ADD COLUMN command     text NOT NULL DEFAULT '',
    ADD COLUMN exit_codes  int[] NOT NULL DEFAULT '{}',
    ADD COLUMN critical    boolean NOT NULL DEFAULT false,
    -- Uploads: which devices get it ({manufacturer, model}, matched
    -- case-insensitively, empty matches all of the platform).
    ADD COLUMN target      jsonb NOT NULL DEFAULT '{}';

-- The catalogs read per vendor and model.
CREATE TABLE vendor_catalogs (
    vendor      text NOT NULL,
    key         text NOT NULL, -- Dell system ID, HP platform ID, Lenovo machine type
    fetched_at  timestamptz,
    error       text NOT NULL DEFAULT '',
    items       jsonb NOT NULL DEFAULT '[]',
    PRIMARY KEY (vendor, key)
);
