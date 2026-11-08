-- Phase 7a: reports. Scheduled reports are mailed as PDF and/or CSV
-- attachments; the mail queue keeps attachments until the message is sent.

ALTER TABLE mail_queue ADD COLUMN attachments jsonb NOT NULL DEFAULT '[]';

CREATE TABLE report_schedules (
    id           bigserial PRIMARY KEY,
    name         text NOT NULL,
    enabled      boolean NOT NULL DEFAULT true,
    -- Which reports: compliance, patches, software.
    reports      text[] NOT NULL DEFAULT '{compliance}',
    -- Device groups to report on; empty is every group.
    group_ids    bigint[] NOT NULL DEFAULT '{}',
    frequency    text NOT NULL DEFAULT 'weekly' CHECK (frequency IN ('daily', 'weekly', 'monthly')),
    weekday      smallint NOT NULL DEFAULT 1 CHECK (weekday BETWEEN 0 AND 6),     -- weekly: 0 = Sunday
    month_day    smallint NOT NULL DEFAULT 1 CHECK (month_day BETWEEN 1 AND 28),  -- monthly
    send_at      smallint NOT NULL DEFAULT 480 CHECK (send_at BETWEEN 0 AND 1439), -- minutes after midnight
    time_zone    text NOT NULL DEFAULT '',
    recipients   text[] NOT NULL DEFAULT '{}',
    language     text NOT NULL DEFAULT 'en' CHECK (language IN ('en', 'nl')),
    formats      text[] NOT NULL DEFAULT '{pdf}',
    next_run_at  timestamptz,
    last_run_at  timestamptz,
    last_error   text NOT NULL DEFAULT '',
    created_by   text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX report_schedules_name ON report_schedules (lower(name));
CREATE INDEX report_schedules_due ON report_schedules (next_run_at) WHERE enabled;
