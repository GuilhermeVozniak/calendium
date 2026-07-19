-- 0016_calendar_shares.sql — M2.7 Task 12: shared calendars with granular
-- permissions plus the audit log for editor write-throughs (reused by the
-- workspace audit surface).

CREATE TABLE calendar_shares (
    id          text PRIMARY KEY,
    calendar_id text NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    -- Exactly one grantee: a specific user or a whole team.
    grantee_user_id text REFERENCES users(id) ON DELETE CASCADE,
    grantee_team_id text REFERENCES teams(id) ON DELETE CASCADE,
    permission  text NOT NULL DEFAULT 'free_busy', -- 'free_busy' | 'reader' | 'editor'
    created_by  text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CHECK (num_nonnulls(grantee_user_id, grantee_team_id) = 1)
);
CREATE UNIQUE INDEX calendar_shares_user_idx ON calendar_shares (calendar_id, grantee_user_id) WHERE grantee_user_id IS NOT NULL;
CREATE UNIQUE INDEX calendar_shares_team_idx ON calendar_shares (calendar_id, grantee_team_id) WHERE grantee_team_id IS NOT NULL;
CREATE INDEX calendar_shares_grantee_idx ON calendar_shares (grantee_user_id, grantee_team_id);

CREATE TABLE audit_entries (
    id            text PRIMARY KEY,
    actor_id      text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    principal_id  text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    action        text NOT NULL,          -- 'event.create' | 'event.update' | ...
    resource_type text NOT NULL,
    resource_id   text NOT NULL,
    metadata      jsonb NOT NULL DEFAULT '{}',
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_entries_principal_idx ON audit_entries (principal_id, created_at DESC);
