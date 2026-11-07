-- Phase 5: software deployment. The library holds packages (from the winget
-- catalog, uploaded installers, apt packages). A deployment installs a
-- package, or uninstalls a program from the inventory, on devices and
-- groups; it has one job per device.

CREATE TABLE software_packages (
    id             bigserial PRIMARY KEY,
    name           text NOT NULL,
    publisher      text NOT NULL DEFAULT '',
    description    text NOT NULL DEFAULT '',
    platform       text NOT NULL CHECK (platform IN ('windows', 'linux')),
    source         text NOT NULL CHECK (source IN ('winget', 'upload', 'apt')),
    winget_id      text NOT NULL DEFAULT '',
    -- Pinned version; '' installs the newest version of the catalog.
    version        text NOT NULL DEFAULT '',
    file_sha256    text NOT NULL DEFAULT '',
    file_name      text NOT NULL DEFAULT '',
    file_size      bigint NOT NULL DEFAULT 0,
    installer_kind text NOT NULL DEFAULT '',
    install_args   text NOT NULL DEFAULT '',
    apt_package    text NOT NULL DEFAULT '',
    -- How the program shows up in the inventory, to skip devices that have it.
    detect_name    text NOT NULL DEFAULT '',
    product_code   text NOT NULL DEFAULT '',
    -- Accept a newer file at the same download link than the catalog lists.
    any_hash       boolean NOT NULL DEFAULT false,
    timeout_minutes smallint NOT NULL DEFAULT 60 CHECK (timeout_minutes BETWEEN 5 AND 600),
    created_by     text NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX software_packages_name ON software_packages (platform, lower(name));
CREATE INDEX software_packages_file ON software_packages (file_sha256) WHERE file_sha256 <> '';

CREATE TABLE software_deployments (
    id                bigserial PRIMARY KEY,
    name              text NOT NULL,
    action            text NOT NULL CHECK (action IN ('install', 'uninstall')),
    package_id        bigint REFERENCES software_packages (id) ON DELETE SET NULL,
    platform          text NOT NULL CHECK (platform IN ('windows', 'linux')),
    -- Uninstall of a program from the inventory.
    uninstall_name    text NOT NULL DEFAULT '',
    uninstall_version text NOT NULL DEFAULT '',
    uninstall_args    text NOT NULL DEFAULT '',
    uninstall_purge   boolean NOT NULL DEFAULT false,
    group_ids         bigint[] NOT NULL DEFAULT '{}',
    -- Devices that join the groups later get a job too.
    ongoing           boolean NOT NULL DEFAULT false,
    start_at          timestamptz NOT NULL DEFAULT now(),
    -- A job that could not start within this many days expires.
    expire_days       smallint NOT NULL DEFAULT 7 CHECK (expire_days BETWEEN 1 AND 90),
    requested_by      text NOT NULL DEFAULT '',
    created_at        timestamptz NOT NULL DEFAULT now(),
    cancelled_at      timestamptz
);
CREATE INDEX software_deployments_active ON software_deployments (id) WHERE cancelled_at IS NULL;

CREATE TABLE software_jobs (
    id              bigserial PRIMARY KEY,
    deployment_id   bigint NOT NULL REFERENCES software_deployments (id) ON DELETE CASCADE,
    device_id       uuid NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    state           text NOT NULL DEFAULT 'pending',
    message         text NOT NULL DEFAULT '',
    exit_code       int,
    output          text NOT NULL DEFAULT '',
    reboot_required boolean NOT NULL DEFAULT false,
    version         text NOT NULL DEFAULT '',
    attempts        int NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now(),
    sent_at         timestamptz,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    finished_at     timestamptz,
    UNIQUE (deployment_id, device_id)
);
CREATE INDEX software_jobs_device ON software_jobs (device_id, id DESC);
CREATE INDEX software_jobs_open ON software_jobs (device_id) WHERE finished_at IS NULL;
