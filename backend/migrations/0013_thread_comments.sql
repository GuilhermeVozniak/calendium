-- 0013_thread_comments.sql — Team comments on mail threads (M2.7 Task 9).
-- Comments piggyback on an explicit team share (or thread ownership); the
-- authorization rule lives in the service layer. deleted_at implements
-- soft delete: soft-deleted rows are invisible to every read.

CREATE TABLE thread_comments (
    id         text PRIMARY KEY,
    thread_id  text NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    team_id    text NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    author_id  text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    body       text NOT NULL CHECK (length(body) <= 10000),
    -- Mentioned team-member user IDs, resolved at write time.
    mentions   text[] NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE INDEX thread_comments_thread_team_idx
    ON thread_comments (thread_id, team_id, created_at);
