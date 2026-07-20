-- M2.8 Task 6: managed events — the automation engine's ownership ledger.
-- Managed events are REAL provider events (created write-through via the
-- normal CalendarService path); this table only tags the local mirror rows
-- the engine owns, so re-runs shrink/extend/delete their own blocks and
-- never touch user events. source_event_id links buffers/travel blocks to
-- the meeting they protect; week_start keys focus blocks to their planning
-- week (Monday, prefs timezone). Mirror deletes cascade the tag away.
CREATE TABLE managed_events (
    event_id        TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL CHECK (kind IN ('focus','buffer','travel')),
    source_event_id TEXT REFERENCES events(id) ON DELETE CASCADE,
    week_start      DATE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX managed_events_user_kind_idx ON managed_events (user_id, kind);
CREATE INDEX managed_events_source_idx ON managed_events (source_event_id) WHERE source_event_id IS NOT NULL;
