-- Phase 3: device groups, enrollment tokens, devices with inventory, and
-- scoping of roles to device groups.

CREATE TABLE device_groups (
    id           bigserial PRIMARY KEY,
    name         text NOT NULL,
    description  text NOT NULL DEFAULT '',
    -- Agent update ring: ring 0 (pilot) gets a new agent version first.
    update_ring  smallint NOT NULL DEFAULT 1 CHECK (update_ring BETWEEN 0 AND 3),
    -- Release channel for agents in this group; NULL follows the server.
    agent_channel text CHECK (agent_channel IN ('production', 'test')),
    -- Hours (server local time) in which agents may update; NULL is any time.
    window_start smallint CHECK (window_start BETWEEN 0 AND 23),
    window_end   smallint CHECK (window_end BETWEEN 0 AND 23),
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX device_groups_name ON device_groups (lower(name));
INSERT INTO device_groups (name, description) VALUES ('Default', 'Devices without a specific group');

CREATE TABLE enrollment_tokens (
    id          bigserial PRIMARY KEY,
    token_hash  bytea NOT NULL UNIQUE,
    token_enc   text NOT NULL,            -- encrypted with the server secret key, so admins can show it again
    prefix      text NOT NULL,            -- first characters, to recognise a token in lists
    description text NOT NULL DEFAULT '',
    group_id    bigint NOT NULL REFERENCES device_groups (id) ON DELETE CASCADE,
    created_by  text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz,
    revoked_at  timestamptz,
    max_uses    integer CHECK (max_uses > 0),
    uses        integer NOT NULL DEFAULT 0,
    last_used_at timestamptz
);
CREATE INDEX enrollment_tokens_group ON enrollment_tokens (group_id);

CREATE TABLE devices (
    id             uuid PRIMARY KEY,
    group_id       bigint NOT NULL REFERENCES device_groups (id),
    token_id       bigint REFERENCES enrollment_tokens (id) ON DELETE SET NULL,
    public_key     bytea NOT NULL,
    machine_id     text NOT NULL DEFAULT '',
    hostname       text NOT NULL,
    platform       text NOT NULL CHECK (platform IN ('windows', 'linux')),
    arch           text NOT NULL DEFAULT '',
    agent_version  text NOT NULL DEFAULT '',
    os_name        text NOT NULL DEFAULT '',
    os_version     text NOT NULL DEFAULT '',
    os_build       text NOT NULL DEFAULT '',
    kind           text NOT NULL DEFAULT 'workstation' CHECK (kind IN ('workstation', 'server', 'hypervisor')),
    domain         text NOT NULL DEFAULT '',
    domain_joined  boolean NOT NULL DEFAULT false,
    ou             text NOT NULL DEFAULT '',
    manufacturer   text NOT NULL DEFAULT '',
    model          text NOT NULL DEFAULT '',
    serial         text NOT NULL DEFAULT '',
    cpu            text NOT NULL DEFAULT '',
    cpu_cores      integer NOT NULL DEFAULT 0,
    memory_bytes   bigint NOT NULL DEFAULT 0,
    logged_in_user text NOT NULL DEFAULT '',
    remote_ip      text NOT NULL DEFAULT '',
    local_ips      text[] NOT NULL DEFAULT '{}',
    boot_time      timestamptz,
    hardware       jsonb NOT NULL DEFAULT '{}', -- disks, network adapters, BIOS, ...
    enrolled_at    timestamptz NOT NULL DEFAULT now(),
    last_seen_at   timestamptz NOT NULL DEFAULT now(),
    inventory_at   timestamptz,
    notes          text NOT NULL DEFAULT ''
);
CREATE INDEX devices_group ON devices (group_id);
CREATE INDEX devices_hostname ON devices (lower(hostname));
CREATE INDEX devices_machine ON devices (machine_id) WHERE machine_id <> '';

CREATE TABLE device_software (
    device_id    uuid NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    name         text NOT NULL,
    version      text NOT NULL DEFAULT '',
    publisher    text NOT NULL DEFAULT '',
    installed_on date,
    source       text NOT NULL DEFAULT '', -- msi, registry, dpkg, appx
    size_bytes   bigint NOT NULL DEFAULT 0
);
CREATE INDEX device_software_device ON device_software (device_id);
CREATE INDEX device_software_name ON device_software (lower(name));

-- A scoped user only sees and manages devices in the listed groups.
-- Administrators are never scoped.
ALTER TABLE users ADD COLUMN scoped boolean NOT NULL DEFAULT false;
CREATE TABLE user_group_scopes (
    user_id  bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    group_id bigint NOT NULL REFERENCES device_groups (id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, group_id)
);

-- An Entra role mapping can limit the role to device groups too.
ALTER TABLE role_mappings ADD COLUMN scoped boolean NOT NULL DEFAULT false;
CREATE TABLE role_mapping_scopes (
    mapping_id bigint NOT NULL REFERENCES role_mappings (id) ON DELETE CASCADE,
    group_id   bigint NOT NULL REFERENCES device_groups (id) ON DELETE CASCADE,
    PRIMARY KEY (mapping_id, group_id)
);
