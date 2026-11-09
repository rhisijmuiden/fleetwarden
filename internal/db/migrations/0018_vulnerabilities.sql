-- Phase 13: vulnerabilities (CVE) per device.
--
-- The server reads public sources once a day: the NVD feeds (CVSS scores
-- and the affected versions of known Windows programs), CISA's Known
-- Exploited Vulnerabilities catalog, the Debian security tracker and
-- Microsoft's security update guide. It matches the inventory, the Windows
-- build and the missing updates of each device against them. The source
-- data is not backed up: it is read again after a restore.

-- Agents report the source package of Debian packages (security fixes are
-- tracked per source package).
ALTER TABLE device_software
    ADD COLUMN src_package text NOT NULL DEFAULT '',
    ADD COLUMN src_version text NOT NULL DEFAULT '';

ALTER TABLE devices ADD COLUMN vuln_dirty boolean NOT NULL DEFAULT true;
ALTER TABLE devices ADD COLUMN vuln_checked_at timestamptz;

-- One row per CVE known from any source.
CREATE TABLE vulnerabilities (
    cve_id         text PRIMARY KEY,
    title          text NOT NULL DEFAULT '',
    summary        text NOT NULL DEFAULT '',
    score          real NOT NULL DEFAULT 0,
    severity       text NOT NULL DEFAULT 'none',
    vector         text NOT NULL DEFAULT '',
    published_at   timestamptz,
    modified_at    timestamptz,
    rejected       boolean NOT NULL DEFAULT false,
    -- Microsoft's rating and score, when NVD has none yet.
    msrc_severity  text NOT NULL DEFAULT '',
    msrc_score     real NOT NULL DEFAULT 0,
    exploited      boolean NOT NULL DEFAULT false, -- Microsoft: exploitation detected
    kev            boolean NOT NULL DEFAULT false,
    kev_name       text NOT NULL DEFAULT '',
    kev_added      date,
    kev_due        date,
    kev_ransomware boolean NOT NULL DEFAULT false
);
CREATE INDEX vulnerabilities_kev ON vulnerabilities (cve_id) WHERE kev;

-- Affected versions of the Windows programs FleetWarden recognises.
CREATE TABLE vuln_ranges (
    cve_id     text NOT NULL,
    product    text NOT NULL, -- CPE vendor:product
    version    text NOT NULL DEFAULT '',
    start_incl text NOT NULL DEFAULT '',
    start_excl text NOT NULL DEFAULT '',
    end_incl   text NOT NULL DEFAULT '',
    end_excl   text NOT NULL DEFAULT ''
);
CREATE INDEX vuln_ranges_product ON vuln_ranges (product);
CREATE INDEX vuln_ranges_cve ON vuln_ranges (cve_id);

-- Debian security tracker: per release and source package.
CREATE TABLE vuln_debian (
    release       text NOT NULL,
    package       text NOT NULL,
    cve_id        text NOT NULL,
    status        text NOT NULL, -- open, resolved
    fixed_version text NOT NULL DEFAULT '',
    urgency       text NOT NULL DEFAULT '',
    nodsa         boolean NOT NULL DEFAULT false,
    PRIMARY KEY (release, package, cve_id)
);

-- Microsoft security updates: the update (KB) and, for Windows releases,
-- the build that fixes a CVE. product is '' for other products, which are
-- matched by the update a device is missing.
CREATE TABLE vuln_msrc (
    cve_id      text NOT NULL,
    product     text NOT NULL DEFAULT '',
    kb          text NOT NULL,
    fixed_build text NOT NULL DEFAULT '',
    supersedes  text NOT NULL DEFAULT '',
    month       text NOT NULL
);
CREATE INDEX vuln_msrc_product ON vuln_msrc (product) WHERE product <> '';
CREATE INDEX vuln_msrc_kb ON vuln_msrc (kb);
CREATE INDEX vuln_msrc_month ON vuln_msrc (month);

-- What each source was read from and when.
CREATE TABLE vuln_sources (
    source     text PRIMARY KEY, -- nvd, kev, debian, msrc
    synced_at  timestamptz,
    checked_at timestamptz,
    error      text NOT NULL DEFAULT '',
    items      int NOT NULL DEFAULT 0,
    state      jsonb NOT NULL DEFAULT '{}'
);

-- The vulnerabilities found per device. kind: os (Windows build), update
-- (a missing Microsoft update), package (Debian), program (Windows program).
-- fix: patch (missing updates in fix_ref), software (winget package or
-- browser:<key> in fix_ref), update (fixed version exists, no update offered
-- yet), none (no fix yet).
CREATE TABLE device_vulnerabilities (
    device_id         uuid NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    cve_id            text NOT NULL,
    kind              text NOT NULL,
    product           text NOT NULL,
    installed_version text NOT NULL DEFAULT '',
    fixed_version     text NOT NULL DEFAULT '',
    fix               text NOT NULL DEFAULT 'none',
    fix_ref           text[] NOT NULL DEFAULT '{}',
    first_seen_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (device_id, cve_id, product)
);
CREATE INDEX device_vulnerabilities_cve ON device_vulnerabilities (cve_id);

-- Accepted risks: a CVE that is not counted (on all devices, or one),
-- optionally until a date.
CREATE TABLE vuln_exceptions (
    id         bigserial PRIMARY KEY,
    cve_id     text NOT NULL,
    device_id  uuid REFERENCES devices (id) ON DELETE CASCADE,
    reason     text NOT NULL DEFAULT '',
    expires_at timestamptz,
    created_by text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX vuln_exceptions_one ON vuln_exceptions (cve_id, COALESCE(device_id, '00000000-0000-0000-0000-000000000000'::uuid));
