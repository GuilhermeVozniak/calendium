-- M2.1 triage power: unsubscribe targets on threads + per-user preferences.
--
-- unsubscribe_* mirror the newest message's List-Unsubscribe (RFC 2369) and
-- List-Unsubscribe-Post (RFC 8058) headers, parsed at sync ingest, so the
-- clients can offer one-click / mailto / link unsubscribe without refetching
-- raw headers. All empty/false when the sender offers no unsubscribe.
ALTER TABLE threads ADD COLUMN unsubscribe_mailto    text;
ALTER TABLE threads ADD COLUMN unsubscribe_url       text;
ALTER TABLE threads ADD COLUMN unsubscribe_one_click boolean NOT NULL DEFAULT false;

-- Per-user client preferences shared across devices. split_order is a jsonb
-- array of inbox-split names ('important', 'vip', ...); '[]' = default order.
CREATE TABLE user_prefs (
    user_id     text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    split_order jsonb NOT NULL DEFAULT '[]',
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- Backs GET-ME-TO-ZERO's "oldest inbox threads before cutoff" scan.
CREATE INDEX threads_zero_idx ON threads (account_id, last_message_at ASC)
    WHERE in_inbox;
