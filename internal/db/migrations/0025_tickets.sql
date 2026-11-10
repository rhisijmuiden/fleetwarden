-- Tickets from the tray: a help request can go to a service desk (through an
-- integration), to the user's Outlook or to an e-mail address. The request
-- keeps what the user chose, the device report that went along and the
-- ticket in the service desk.

ALTER TABLE help_requests
    ADD COLUMN urgency        text NOT NULL DEFAULT '',
    ADD COLUMN category       text NOT NULL DEFAULT '',
    ADD COLUMN email          text NOT NULL DEFAULT '',
    ADD COLUMN destination    text NOT NULL DEFAULT 'fleetwarden',
    ADD COLUMN integration_id bigint REFERENCES integrations (id) ON DELETE SET NULL,
    ADD COLUMN ticket_id      text NOT NULL DEFAULT '',
    ADD COLUMN ticket_number  text NOT NULL DEFAULT '',
    ADD COLUMN ticket_url     text NOT NULL DEFAULT '',
    ADD COLUMN ticket_state   text NOT NULL DEFAULT '',
    ADD COLUMN ticket_error   text NOT NULL DEFAULT '',
    ADD COLUMN report         jsonb,
    ADD COLUMN synced_at      timestamptz;

-- The open tickets whose state is read back from the service desk.
CREATE INDEX help_requests_sync ON help_requests (synced_at NULLS FIRST)
    WHERE state <> 'closed' AND ticket_id <> '' AND integration_id IS NOT NULL;
