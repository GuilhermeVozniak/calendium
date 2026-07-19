-- 0020_event_notes.sql — M2.8 Task 4: docs/notes attached to events.
-- Notes are local-only user content keyed by our event id: they never reach
-- the provider, so they survive provider syncs, and ON DELETE CASCADE follows
-- mirror deletes (a provider-side event removal takes its note with it).
CREATE TABLE event_notes (
    event_id   text PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    user_id    text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    body_md    text NOT NULL,
    links      jsonb NOT NULL DEFAULT '[]',
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX event_notes_user_idx ON event_notes (user_id);
