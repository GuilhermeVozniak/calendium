-- 0001_init.sql — Calendium initial schema.
-- IDs are app-generated random hex strings (see internal/service newID),
-- except users.id which is the Supabase JWT `sub`.

-- Trigram matching backs the thread-search subject ILIKE predicate.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- --- Identity & billing -----------------------------------------------------

CREATE TABLE users (
    id         text PRIMARY KEY, -- Supabase JWT sub
    email      text NOT NULL,
    name       text,
    avatar_url text,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX users_email_idx ON users (lower(email));

CREATE TABLE subscriptions (
    user_id                text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    status                 text NOT NULL DEFAULT 'none',
    plan                   text NOT NULL DEFAULT 'annual',
    price_usd              integer NOT NULL DEFAULT 50,
    stripe_customer_id     text,
    stripe_subscription_id text,
    current_period_end     timestamptz,
    cancel_at_period_end   boolean NOT NULL DEFAULT false,
    trial_ends_at          timestamptz,
    updated_at             timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX subscriptions_stripe_customer_idx
    ON subscriptions (stripe_customer_id) WHERE stripe_customer_id IS NOT NULL;
CREATE INDEX subscriptions_stripe_subscription_idx ON subscriptions (stripe_subscription_id);

-- Webhook idempotency ledger (unique event id recorded before processing).
CREATE TABLE stripe_events (
    id          text PRIMARY KEY,
    type        text NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now()
);

-- --- Connected provider accounts -------------------------------------------

CREATE TABLE connected_accounts (
    id                text PRIMARY KEY,
    user_id           text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider          text NOT NULL, -- 'google' | 'microsoft'
    email             text NOT NULL,
    status            text NOT NULL DEFAULT 'active',
    scopes            text[] NOT NULL DEFAULT '{}',
    -- Sender addresses whose mail is classified into the "vip" split.
    vip_senders       text[] NOT NULL DEFAULT '{}',
    -- AES-256-GCM ciphertext, nonce-prefixed (TOKEN_ENCRYPTION_KEY).
    access_token_enc  bytea,
    refresh_token_enc bytea,
    token_expires_at  timestamptz,
    last_synced_at    timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, provider, email)
);
CREATE INDEX connected_accounts_user_idx ON connected_accounts (user_id);
-- Serves the worker's hot ListSyncable poll (status filter + created_at order).
CREATE INDEX connected_accounts_syncable_idx ON connected_accounts (created_at)
    WHERE status IN ('active', 'syncing');

-- One-time CSRF states (+ PKCE verifier) for provider-connect flows.
CREATE TABLE oauth_states (
    state         text PRIMARY KEY,
    user_id       text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider      text NOT NULL,
    redirect_url  text NOT NULL,
    code_verifier text,
    expires_at    timestamptz NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX oauth_states_expires_idx ON oauth_states (expires_at);

-- --- Mail mirror -------------------------------------------------------------

CREATE TABLE labels (
    id                text PRIMARY KEY,
    account_id        text NOT NULL REFERENCES connected_accounts(id) ON DELETE CASCADE,
    provider_label_id text NOT NULL,
    name              text NOT NULL,
    kind              text NOT NULL DEFAULT 'user', -- 'system' | 'user'
    color             text,
    UNIQUE (account_id, provider_label_id)
);

CREATE TABLE threads (
    id                 text PRIMARY KEY,
    account_id         text NOT NULL REFERENCES connected_accounts(id) ON DELETE CASCADE,
    provider_thread_id text NOT NULL,
    subject            text NOT NULL DEFAULT '',
    snippet            text NOT NULL DEFAULT '',
    participants       jsonb NOT NULL DEFAULT '[]',
    split              text NOT NULL DEFAULT 'other',
    message_count      integer NOT NULL DEFAULT 0,
    unread             boolean NOT NULL DEFAULT false,
    starred            boolean NOT NULL DEFAULT false,
    last_message_at    timestamptz NOT NULL,
    snoozed_until      timestamptz,
    remind_at          timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id, provider_thread_id)
);
CREATE INDEX threads_list_idx    ON threads (account_id, last_message_at DESC);
CREATE INDEX threads_split_idx   ON threads (account_id, split, last_message_at DESC);
CREATE INDEX threads_snoozed_idx ON threads (snoozed_until) WHERE snoozed_until IS NOT NULL;
CREATE INDEX threads_remind_idx  ON threads (remind_at) WHERE remind_at IS NOT NULL;
CREATE INDEX threads_search_idx  ON threads
    USING gin (to_tsvector('simple', coalesce(subject, '') || ' ' || coalesce(snippet, '')));
-- Trigram index for the subject ILIKE '%q%' branch of thread search.
CREATE INDEX threads_subject_trgm_idx ON threads USING gin (subject gin_trgm_ops);

CREATE TABLE thread_labels (
    thread_id text NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    label_id  text NOT NULL REFERENCES labels(id) ON DELETE CASCADE,
    PRIMARY KEY (thread_id, label_id)
);
CREATE INDEX thread_labels_label_idx ON thread_labels (label_id);

CREATE TABLE messages (
    id                  text PRIMARY KEY,
    thread_id           text NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    account_id          text NOT NULL REFERENCES connected_accounts(id) ON DELETE CASCADE,
    provider_message_id text NOT NULL,
    from_addr           jsonb NOT NULL,
    to_addrs            jsonb NOT NULL DEFAULT '[]',
    cc_addrs            jsonb NOT NULL DEFAULT '[]',
    bcc_addrs           jsonb NOT NULL DEFAULT '[]',
    subject             text NOT NULL DEFAULT '',
    body_html           text NOT NULL DEFAULT '',
    body_text           text NOT NULL DEFAULT '',
    sent_at             timestamptz NOT NULL,
    is_draft            boolean NOT NULL DEFAULT false,
    opened_at           timestamptz, -- read receipts
    UNIQUE (account_id, provider_message_id)
);
CREATE INDEX messages_thread_idx ON messages (thread_id, sent_at);

CREATE TABLE attachments (
    id         text PRIMARY KEY,
    message_id text NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    filename   text NOT NULL,
    mime_type  text NOT NULL,
    size_bytes bigint NOT NULL DEFAULT 0
);
CREATE INDEX attachments_message_idx ON attachments (message_id);

CREATE TABLE drafts (
    id           text PRIMARY KEY,
    account_id   text NOT NULL REFERENCES connected_accounts(id) ON DELETE CASCADE,
    thread_id    text REFERENCES threads(id) ON DELETE SET NULL,
    to_addrs     jsonb NOT NULL DEFAULT '[]',
    cc_addrs     jsonb NOT NULL DEFAULT '[]',
    bcc_addrs    jsonb NOT NULL DEFAULT '[]',
    subject      text NOT NULL DEFAULT '',
    body_html    text NOT NULL DEFAULT '',
    scheduled_at timestamptz, -- Send Later / undo-send delivery queue
    -- Worker delivery bookkeeping: retry/backoff counter and last failure.
    send_attempts integer NOT NULL DEFAULT 0,
    last_error   text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX drafts_account_idx   ON drafts (account_id);
CREATE INDEX drafts_scheduled_idx ON drafts (scheduled_at) WHERE scheduled_at IS NOT NULL;

CREATE TABLE snippets (
    id          text PRIMARY KEY,
    user_id     text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        text NOT NULL,
    shortcut    text,
    body_html   text NOT NULL DEFAULT '',
    usage_count integer NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX snippets_user_idx ON snippets (user_id);

-- --- Calendar mirror ---------------------------------------------------------

CREATE TABLE calendars (
    id                   text PRIMARY KEY,
    account_id           text NOT NULL REFERENCES connected_accounts(id) ON DELETE CASCADE,
    provider_calendar_id text NOT NULL,
    name                 text NOT NULL,
    color                text NOT NULL DEFAULT '#6366f1',
    time_zone            text NOT NULL DEFAULT 'UTC',
    is_primary           boolean NOT NULL DEFAULT false,
    is_visible           boolean NOT NULL DEFAULT true, -- local preference
    can_write            boolean NOT NULL DEFAULT false,
    UNIQUE (account_id, provider_calendar_id)
);
CREATE INDEX calendars_account_idx ON calendars (account_id);

CREATE TABLE events (
    id                text PRIMARY KEY,
    calendar_id       text NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    provider_event_id text NOT NULL,
    title             text NOT NULL DEFAULT '',
    description       text,
    location          text,
    start_at          timestamptz NOT NULL,
    end_at            timestamptz NOT NULL,
    all_day           boolean NOT NULL DEFAULT false,
    recurrence_rule   text, -- RFC 5545 RRULE
    attendees         jsonb NOT NULL DEFAULT '[]',
    conferencing      jsonb,
    status            text NOT NULL DEFAULT 'confirmed',
    visibility        text NOT NULL DEFAULT 'default',
    reminder_minutes  integer[] NOT NULL DEFAULT '{}',
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (calendar_id, provider_event_id)
);
CREATE INDEX events_range_idx  ON events (calendar_id, start_at, end_at);
CREATE INDEX events_search_idx ON events
    USING gin (to_tsvector('simple',
        coalesce(title, '') || ' ' || coalesce(description, '') || ' ' || coalesce(location, '')));

-- --- Devices & sync ----------------------------------------------------------

CREATE TABLE devices (
    id         text PRIMARY KEY,
    user_id    text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    platform   text NOT NULL, -- ios|android|web|macos|windows|linux
    token      text NOT NULL, -- APNs token / FCM token / Web Push subscription JSON
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, token)
);

-- Incremental sync cursors (Gmail historyId, Graph delta links, ...).
CREATE TABLE sync_state (
    account_id  text NOT NULL REFERENCES connected_accounts(id) ON DELETE CASCADE,
    resource    text NOT NULL, -- 'mail' | 'calendars' | 'events:<providerCalendarID>'
    sync_cursor text NOT NULL DEFAULT '',
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, resource)
);
