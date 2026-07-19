-- 0017_delegations.sql — M2.7 Task 15: EA delegation mode + audit log.
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

-- audit_entries: append-only record of delegated mutations. Each row names
-- the REAL actor (the assistant) and the principal acted for; there is no
-- update or delete surface anywhere in the application. Guarded with
-- IF NOT EXISTS because the Task 12 brief also expected to own this table
-- and parallel branches may merge in either order.
CREATE TABLE IF NOT EXISTS audit_entries (
    id           text PRIMARY KEY,
    principal_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    actor_id     text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    action       text NOT NULL,
    resource_id  text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS audit_entries_principal_idx
    ON audit_entries (principal_id, created_at DESC, id DESC);
