-- First-class tasks (M2.8): local todos and mirrored external provider
-- todos (source/external_id). Due carries deadline semantics; the
-- scheduled_* pair carries timeblock semantics for the calendar grid.
CREATE TABLE tasks (
    id               TEXT PRIMARY KEY,
    user_id          TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title            TEXT NOT NULL,
    notes            TEXT,
    due              TIMESTAMPTZ,
    all_day_due      BOOLEAN NOT NULL DEFAULT FALSE,
    scheduled_start  TIMESTAMPTZ,
    scheduled_end    TIMESTAMPTZ,
    completed_at     TIMESTAMPTZ,
    source           TEXT NOT NULL DEFAULT 'local',
    external_id      TEXT NOT NULL DEFAULT '',
    source_url       TEXT,
    position         DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT tasks_scheduled_pair CHECK ((scheduled_start IS NULL) = (scheduled_end IS NULL))
);

-- One mirrored row per provider todo. Partial: local tasks all carry
-- ('local', '') and must never collide with each other.
CREATE UNIQUE INDEX tasks_external_idx ON tasks (user_id, source, external_id) WHERE source <> 'local';
CREATE INDEX tasks_user_scheduled_idx ON tasks (user_id, scheduled_start) WHERE scheduled_start IS NOT NULL;
CREATE INDEX tasks_user_due_idx ON tasks (user_id, due) WHERE due IS NOT NULL;
