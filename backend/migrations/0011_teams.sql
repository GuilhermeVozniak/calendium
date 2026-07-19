-- 0011_teams.sql — Teams, membership, email invitations (M2.7).
-- Membership grants nothing by itself; collaborative surfaces each have
-- explicit opt-ins (privacy default: share nothing).

CREATE TABLE teams (
    id         text PRIMARY KEY,
    name       text NOT NULL,
    created_by text NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE team_members (
    team_id             text NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id             text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role                text NOT NULL DEFAULT 'member', -- 'owner' | 'admin' | 'member'
    -- Explicit opt-in for team read-status / reply indicators.
    share_read_statuses boolean NOT NULL DEFAULT false,
    joined_at           timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, user_id)
);
CREATE INDEX team_members_user_idx ON team_members (user_id);

CREATE TABLE team_invitations (
    id         text PRIMARY KEY,
    team_id    text NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    email      text NOT NULL,
    role       text NOT NULL DEFAULT 'member',
    invited_by text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status     text NOT NULL DEFAULT 'pending', -- pending|accepted|revoked|expired
    -- SHA-256 hex of the one-time invite-link token; raw token never stored.
    token_hash text NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX team_invitations_team_idx ON team_invitations (team_id);
-- One live invitation per address per team.
CREATE UNIQUE INDEX team_invitations_pending_idx
    ON team_invitations (team_id, lower(email)) WHERE status = 'pending';
