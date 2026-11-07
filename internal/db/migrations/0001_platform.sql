-- Phase 1: accounts, sessions, settings, audit log, mail queue and branding assets.

CREATE TABLE users (
    id                   bigserial PRIMARY KEY,
    username             text NOT NULL UNIQUE,
    display_name         text NOT NULL DEFAULT '',
    email                text NOT NULL DEFAULT '',
    source               text NOT NULL CHECK (source IN ('local', 'entra')),
    external_id          text UNIQUE,
    password_hash        text,
    must_change_password boolean NOT NULL DEFAULT false,
    totp_secret          text,
    totp_enabled         boolean NOT NULL DEFAULT false,
    totp_last_step       bigint NOT NULL DEFAULT 0,
    role                 text NOT NULL,
    language             text NOT NULL DEFAULT 'en',
    disabled             boolean NOT NULL DEFAULT false,
    break_glass          boolean NOT NULL DEFAULT false,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    last_login_at        timestamptz
);

CREATE TABLE sessions (
    token_hash   bytea PRIMARY KEY,
    user_id      bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    mfa_pending  boolean NOT NULL DEFAULT false,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    ip           text NOT NULL DEFAULT '',
    user_agent   text NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user_id ON sessions (user_id);
CREATE INDEX sessions_expires_at ON sessions (expires_at);

CREATE TABLE settings (
    key        text PRIMARY KEY,
    value      jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Maps an Entra group object ID or app role value to a FleetWarden role.
CREATE TABLE role_mappings (
    id         bigserial PRIMARY KEY,
    claim      text NOT NULL CHECK (claim IN ('group', 'role')),
    value      text NOT NULL,
    role       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (claim, value)
);

CREATE TABLE audit_log (
    id       bigserial PRIMARY KEY,
    at       timestamptz NOT NULL DEFAULT now(),
    actor_id bigint,
    actor    text NOT NULL DEFAULT '',
    ip       text NOT NULL DEFAULT '',
    action   text NOT NULL,
    target   text NOT NULL DEFAULT '',
    success  boolean NOT NULL DEFAULT true,
    details  jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX audit_log_at ON audit_log (at DESC);

CREATE TABLE mail_queue (
    id              bigserial PRIMARY KEY,
    created_at      timestamptz NOT NULL DEFAULT now(),
    kind            text NOT NULL,
    to_addr         text NOT NULL,
    subject         text NOT NULL,
    body_text       text NOT NULL,
    body_html       text NOT NULL,
    status          text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'sent', 'failed')),
    attempts        integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error      text NOT NULL DEFAULT '',
    sent_at         timestamptz
);
CREATE INDEX mail_queue_pending ON mail_queue (next_attempt_at) WHERE status = 'queued';
CREATE INDEX mail_queue_created_at ON mail_queue (created_at DESC);

CREATE TABLE assets (
    name         text PRIMARY KEY,
    content_type text NOT NULL,
    data         bytea NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now()
);
