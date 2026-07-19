-- 0017_delegations.sql — M2.7 Task 15: EA delegation mode.
--
-- delegations: explicit grantor→delegate grants with scoped permissions.
-- A grant is pending until the assistant accepts it and stops authorizing
-- the moment it is revoked (services always re-read the row — no caching).
CREATE TABLE delegations (
    id           text PRIMARY KEY,
    principal_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    assistant_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scopes       text[] NOT NULL,
    status       text NOT NULL DEFAULT 'pending', -- pending|active|revoked
    created_at   timestamptz NOT NULL DEFAULT now(),
    accepted_at  timestamptz,
    revoked_at   timestamptz,
    UNIQUE (principal_id, assistant_id)
);

CREATE INDEX delegations_assistant_idx ON delegations (assistant_id);

-- Delegated-mutation audit entries live in the unified audit_entries table
-- created by 0016_calendar_shares.sql (append-only; indexed on
-- (principal_id, created_at DESC)). No audit DDL here.
