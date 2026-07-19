-- 0014_team_thread_activity.sql — Team read statuses / reply indicators (M2.7 Task 10).
-- Teammates mirror the same conversation as different local thread rows
-- (per-account provider thread ids differ); the RFC 5322 Message-ID of the
-- thread's earliest message is the cross-account conversation key.

-- Persist the RFC 5322 Message-ID captured from sync headers at ingest.
ALTER TABLE messages ADD COLUMN rfc_message_id text;
CREATE INDEX messages_rfc_idx ON messages (rfc_message_id) WHERE rfc_message_id IS NOT NULL;

-- One row per (team, member, conversation): when the member last opened the
-- conversation and when they last replied. Rows are written ONLY for members
-- with team_members.share_read_statuses set, and reads re-filter on the
-- flag (privacy default: opting out hides a member immediately).
CREATE TABLE team_thread_activity (
    team_id          text NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id          text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    conversation_key text NOT NULL,
    opened_at        timestamptz,
    replied_at       timestamptz,
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, user_id, conversation_key)
);
CREATE INDEX team_thread_activity_conv_idx ON team_thread_activity (team_id, conversation_key);
