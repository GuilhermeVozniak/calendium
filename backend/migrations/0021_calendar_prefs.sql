-- M2.8 Task 5: calendar automation preferences (FocusGuard, auto buffers,
-- OOO auto-decline, travel buffers/leave alerts, weather).
-- One JSONB document per user: the shape is young, all reads are by PK, and
-- ListAutomated filters with a JSONB predicate over the doc.
CREATE TABLE calendar_prefs (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    prefs JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
