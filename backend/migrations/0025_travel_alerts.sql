-- M2.8 Task 12: leave-now travel alerts. One pending "time to leave" push
-- per event (PK event_id), computed by the travel pass as
-- leave_at = event start - travel time - 5 minutes. sent_at NULL means
-- undelivered; it is stamped only AFTER an observed successful push
-- (honesty policy) and cleared when leave_at moves (event rescheduled).
-- ON DELETE CASCADE from events: a deleted mirror takes its alert with it.
CREATE TABLE travel_alerts (
    event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    leave_at TIMESTAMPTZ NOT NULL,
    sent_at TIMESTAMPTZ
);

-- Serves ProcessDueWork's "unsent and due" scan (sent_at IS NULL AND leave_at <= now).
CREATE INDEX travel_alerts_due_idx ON travel_alerts (sent_at, leave_at);
