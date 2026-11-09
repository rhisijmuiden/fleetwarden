-- Phase 15: self-service through the tray app. Administrators publish
-- programs from the software library in a catalog (for every group or for
-- some groups, with or without approval); users install them, start the
-- approved updates and ask for help from the tray.

CREATE TABLE selfservice_items (
    id           bigserial PRIMARY KEY,
    package_id   bigint NOT NULL UNIQUE REFERENCES software_packages (id) ON DELETE CASCADE,
    -- What the user sees; '' uses the package's name and description.
    display_name text NOT NULL DEFAULT '',
    description  text NOT NULL DEFAULT '',
    category     text NOT NULL DEFAULT '',
    -- Groups that see the item; empty is every group.
    group_ids    bigint[] NOT NULL DEFAULT '{}',
    -- Each request waits for a technician first.
    approval     boolean NOT NULL DEFAULT false,
    enabled      boolean NOT NULL DEFAULT true,
    created_by   text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- Install requests of users. An approved request has the deployment (with
-- one job) that installs the program.
CREATE TABLE selfservice_requests (
    id            bigserial PRIMARY KEY,
    item_id       bigint REFERENCES selfservice_items (id) ON DELETE SET NULL,
    package_id    bigint REFERENCES software_packages (id) ON DELETE SET NULL,
    name          text NOT NULL,
    device_id     uuid NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    username      text NOT NULL DEFAULT '',
    reason        text NOT NULL DEFAULT '',
    state         text NOT NULL CHECK (state IN ('pending', 'approved', 'rejected', 'cancelled')),
    deployment_id bigint REFERENCES software_deployments (id) ON DELETE SET NULL,
    decided_by    text NOT NULL DEFAULT '',
    decided_at    timestamptz,
    note          text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX selfservice_requests_device ON selfservice_requests (device_id, item_id, id DESC);
CREATE INDEX selfservice_requests_pending ON selfservice_requests (id) WHERE state = 'pending';

-- Help requests from the tray.
CREATE TABLE help_requests (
    id          bigserial PRIMARY KEY,
    device_id   uuid NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    username    text NOT NULL DEFAULT '',
    subject     text NOT NULL,
    message     text NOT NULL DEFAULT '',
    contact     text NOT NULL DEFAULT '',
    state       text NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'in_progress', 'closed')),
    -- The technician who took it, and the note the user sees in the tray.
    assigned_to text NOT NULL DEFAULT '',
    reply       text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    closed_at   timestamptz
);
CREATE INDEX help_requests_device ON help_requests (device_id, id DESC);
CREATE INDEX help_requests_open ON help_requests (id) WHERE state <> 'closed';

-- Users may start the approved updates themselves.
ALTER TABLE patch_runs DROP CONSTRAINT patch_runs_trigger_check;
ALTER TABLE patch_runs ADD CONSTRAINT patch_runs_trigger_check CHECK (trigger IN ('window', 'manual', 'selfservice'));
