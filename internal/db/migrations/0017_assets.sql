-- Phase 16: asset management. Per device who has it, where it is and until
-- when the warranty runs; software licences with the number of seats bought,
-- compared with the installations the agents report.

CREATE TABLE device_assets (
    device_id     uuid PRIMARY KEY REFERENCES devices (id) ON DELETE CASCADE,
    asset_tag     text NOT NULL DEFAULT '',
    owner         text NOT NULL DEFAULT '',
    location      text NOT NULL DEFAULT '',
    department    text NOT NULL DEFAULT '',
    supplier      text NOT NULL DEFAULT '',
    order_number  text NOT NULL DEFAULT '',
    purchase_date date,
    warranty_end  date,
    updated_by    text NOT NULL DEFAULT '',
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX device_assets_warranty ON device_assets (warranty_end) WHERE warranty_end IS NOT NULL;
CREATE INDEX device_assets_tag ON device_assets (lower(asset_tag)) WHERE asset_tag <> '';

CREATE TABLE software_licenses (
    id              bigserial PRIMARY KEY,
    name            text NOT NULL,
    vendor          text NOT NULL DEFAULT '',
    -- Installed programs that use a seat: their name, and optionally their
    -- publisher, matched case-insensitively; * is a wildcard.
    match_name      text NOT NULL,
    match_publisher text NOT NULL DEFAULT '',
    seats           integer NOT NULL CHECK (seats >= 0),
    -- The licence key, encrypted with the server secret key.
    key_enc         text NOT NULL DEFAULT '',
    expires_on      date,
    cost            numeric(12, 2),
    notes           text NOT NULL DEFAULT '',
    created_by      text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX software_licenses_name ON software_licenses (lower(name));

-- Warranty alerts: threshold is the number of days before the end.
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_kind_check;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_kind_check CHECK (kind IN ('offline', 'disk', 'warranty'));

INSERT INTO alert_rules (name, kind, threshold, device_kinds) VALUES ('Warranty ending', 'warranty', 60, '{}');
