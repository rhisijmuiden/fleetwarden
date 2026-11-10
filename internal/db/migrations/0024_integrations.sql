-- Integrations: service desk connectors (TOPdesk, Freshservice, ...) and
-- custom API calls the administrator defines. Secrets are sealed with the
-- server's secret key, like the mail and SSO secrets.

CREATE TABLE integrations (
    id             bigserial PRIMARY KEY,
    name           text NOT NULL,
    kind           text NOT NULL,
    enabled        boolean NOT NULL DEFAULT true,
    -- Connector settings (not secret) and, for a custom API, its definition.
    settings       jsonb NOT NULL DEFAULT '{}',
    custom         jsonb,
    -- Sealed secrets by field key.
    secrets        jsonb NOT NULL DEFAULT '{}',
    -- Events that trigger the integration (alert.opened, device.enrolled),
    -- and for alerts the rules (empty is every rule).
    events         text[] NOT NULL DEFAULT '{}',
    alert_rule_ids bigint[] NOT NULL DEFAULT '{}',
    created_by     text NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    -- Result of the last call, for the overview.
    last_ok        boolean,
    last_error     text NOT NULL DEFAULT '',
    last_at        timestamptz
);
CREATE UNIQUE INDEX integrations_name ON integrations (lower(name));

-- Every HTTP request an integration made, kept 30 days.
CREATE TABLE integration_calls (
    id             bigserial PRIMARY KEY,
    integration_id bigint NOT NULL REFERENCES integrations (id) ON DELETE CASCADE,
    created_at     timestamptz NOT NULL DEFAULT now(),
    -- What caused it: test, test_ticket, alert.opened, device.enrolled, ticket ...
    event          text NOT NULL,
    method         text NOT NULL DEFAULT '',
    url            text NOT NULL DEFAULT '',
    status         integer NOT NULL DEFAULT 0,
    ok             boolean NOT NULL,
    duration_ms    integer NOT NULL DEFAULT 0,
    error          text NOT NULL DEFAULT '',
    -- The ticket the call created, if any.
    ticket_number  text NOT NULL DEFAULT '',
    ticket_url     text NOT NULL DEFAULT ''
);
CREATE INDEX integration_calls_integration ON integration_calls (integration_id, id DESC);
CREATE INDEX integration_calls_created ON integration_calls (created_at);
