-- 0012_thread_shares.sql — M2.7 Task 7: tokenized live thread shares.
-- A share is an explicit per-thread action by the thread's owner: a live
-- link with audience 'team' (authed members of team_id) or 'external'
-- (anyone with the link). The raw token appears once in the API response;
-- only its SHA-256 hex is stored. Revocation stamps revoked_at; expiry is
-- optional. Shares die with their thread and (for team shares) their team.
CREATE TABLE thread_shares (
    id         text PRIMARY KEY,
    thread_id  text NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    created_by text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    audience   text NOT NULL, -- 'team' | 'external'
    -- Required when audience='team'; NULL for external shares.
    team_id    text REFERENCES teams(id) ON DELETE CASCADE,
    -- SHA-256 hex of the raw link token; raw token never stored.
    token_hash text NOT NULL UNIQUE,
    revoked_at timestamptz,
    expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
-- Backs ListByThread (share management + the sync share.updated fan-out).
CREATE INDEX thread_shares_thread_idx ON thread_shares (thread_id);
