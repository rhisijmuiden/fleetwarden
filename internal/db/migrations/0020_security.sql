-- Phase 12: security status.
--
-- Agents report disk encryption (BitLocker, LUKS), antivirus (Defender and
-- the products Windows Security Center knows), the firewall, Secure Boot,
-- the TPM and the local administrators after each inventory. The summary
-- columns drive the overview and its filters; the full report (without
-- recovery keys) is kept as JSON.

CREATE TABLE device_security (
    device_id    uuid PRIMARY KEY REFERENCES devices (id) ON DELETE CASCADE,
    collected_at timestamptz NOT NULL,
    received_at  timestamptz NOT NULL DEFAULT now(),
    -- encrypted, partial (other fixed volumes are not), suspended,
    -- encrypting, none, unknown
    encryption   text NOT NULL,
    -- ok, outdated, disabled, none, unknown
    antivirus    text NOT NULL,
    -- on, partial, off, unknown
    firewall     text NOT NULL,
    threats      int NOT NULL DEFAULT 0, -- active threats
    admins       int NOT NULL DEFAULT 0,
    secure_boot  boolean,
    tpm          boolean,
    report       jsonb NOT NULL
);

CREATE INDEX device_security_encryption ON device_security (encryption);

-- BitLocker recovery passwords, encrypted with the server's secret key. A
-- key stays when its device is deleted (a reinstalled PC may need it), and
-- when it disappears from the device (rotated), so an old printout still
-- resolves. The protector ID is unique per key.
CREATE TABLE recovery_keys (
    id            bigserial PRIMARY KEY,
    key_id        text NOT NULL UNIQUE,
    device_id     uuid REFERENCES devices (id) ON DELETE SET NULL,
    hostname      text NOT NULL,
    serial        text NOT NULL DEFAULT '',
    volume        text NOT NULL,
    password      text NOT NULL, -- sealed
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at  timestamptz NOT NULL DEFAULT now(),
    removed_at    timestamptz, -- no longer on the device
    revealed_at   timestamptz,
    revealed_by   text NOT NULL DEFAULT ''
);

CREATE INDEX recovery_keys_device ON recovery_keys (device_id);
CREATE INDEX recovery_keys_hostname ON recovery_keys (lower(hostname));

-- Defender scans, signature updates and key rotations started from the UI.
CREATE TABLE security_actions (
    id           bigserial PRIMARY KEY,
    device_id    uuid NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    action       text NOT NULL CHECK (action IN ('defender.quickscan', 'defender.fullscan', 'defender.signatures', 'bitlocker.rotate')),
    mount        text NOT NULL DEFAULT '',
    state        text NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'running', 'completed', 'failed')),
    message      text NOT NULL DEFAULT '',
    requested_by text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    finished_at  timestamptz
);

CREATE INDEX security_actions_device ON security_actions (device_id, created_at DESC);

-- Alert rules for the security status.
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_kind_check;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_kind_check
    CHECK (kind IN ('offline', 'disk', 'warranty', 'threat', 'antivirus', 'firewall', 'encryption'));

INSERT INTO alert_rules (name, enabled, kind, threshold, device_kinds) VALUES
    ('Threat detected', true, 'threat', 0, '{}');
