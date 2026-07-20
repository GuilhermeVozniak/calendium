-- 0026: interesting-calendar ICS feed subscriptions (M2.8 Task 15).
-- calendar_subscriptions: one row per user-added https ICS feed.
-- subscription_events: the expanded, read-only occurrence mirror the feed
-- owns outright (ReplaceEvents swaps the whole set atomically).

CREATE TABLE calendar_subscriptions (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    url             TEXT NOT NULL,
    name            TEXT NOT NULL,
    color           TEXT NOT NULL DEFAULT '#8b5cf6',
    is_visible      BOOLEAN NOT NULL DEFAULT TRUE,
    etag            TEXT NOT NULL DEFAULT '',
    last_fetched_at TIMESTAMPTZ,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, url)
);

CREATE INDEX calendar_subscriptions_user_idx ON calendar_subscriptions (user_id);

CREATE TABLE subscription_events (
    id              TEXT PRIMARY KEY,
    subscription_id TEXT NOT NULL REFERENCES calendar_subscriptions(id) ON DELETE CASCADE,
    uid             TEXT NOT NULL,
    title           TEXT NOT NULL,
    description     TEXT,
    location        TEXT,
    starts_at       TIMESTAMPTZ NOT NULL,
    ends_at         TIMESTAMPTZ NOT NULL,
    all_day         BOOLEAN NOT NULL DEFAULT FALSE
);

-- Range scans: "events of subscription S overlapping [from, to)".
CREATE INDEX subscription_events_range_idx
    ON subscription_events (subscription_id, starts_at, ends_at);
