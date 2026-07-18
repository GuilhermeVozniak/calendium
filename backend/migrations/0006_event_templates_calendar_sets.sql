-- Saved event defaults (Fantastical event templates).
CREATE TABLE event_templates (
    id               text PRIMARY KEY,
    user_id          text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name             text NOT NULL,
    title            text NOT NULL DEFAULT '',
    description      text NOT NULL DEFAULT '',
    location         text NOT NULL DEFAULT '',
    duration_minutes integer NOT NULL DEFAULT 30 CHECK (duration_minutes > 0),
    all_day          boolean NOT NULL DEFAULT false,
    calendar_id      text REFERENCES calendars(id) ON DELETE SET NULL,
    attendee_emails  jsonb NOT NULL DEFAULT '[]',
    add_conferencing boolean NOT NULL DEFAULT false,
    reminder_minutes jsonb NOT NULL DEFAULT '[]',
    recurrence_rule  text,
    usage_count      integer NOT NULL DEFAULT 0,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX event_templates_user_idx ON event_templates (user_id);

-- Named calendar groups that toggle visibility together (Fantastical sets).
CREATE TABLE calendar_sets (
    id           text PRIMARY KEY,
    user_id      text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         text NOT NULL,
    calendar_ids jsonb NOT NULL DEFAULT '[]',
    position     integer NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX calendar_sets_user_idx ON calendar_sets (user_id);