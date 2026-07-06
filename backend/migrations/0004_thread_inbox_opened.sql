-- Inbox membership + real read state for threads.
--
-- in_inbox mirrors provider inbox membership so archive/trash/spam remove a
-- thread from the inbox list instead of it reappearing on every refetch; the
-- default listing filters on it while starred/sent/snoozed pseudo-views span
-- archived threads. opened_at records the first time the owner opened the
-- thread (POST /v1/mail/threads/{id}/open) — genuine read state, not mock.
ALTER TABLE threads ADD COLUMN in_inbox  boolean NOT NULL DEFAULT true;
ALTER TABLE threads ADD COLUMN opened_at timestamptz;

-- Partial index backing the default inbox-only listing per split.
CREATE INDEX threads_inbox_idx ON threads (account_id, split, last_message_at DESC)
    WHERE in_inbox;
