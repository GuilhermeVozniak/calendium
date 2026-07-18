-- 0008_scheduling.sql — booking links, bookings, meeting polls, time
-- proposals, user scheduling settings. btree_gist backs the anti-double-
-- booking exclusion constraint (text equality inside a GiST index).
CREATE EXTENSION IF NOT EXISTS btree_gist;

CREATE TABLE booking_links (
    id                    text PRIMARY KEY,
    user_id               text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    slug                  text NOT NULL,
    title                 text NOT NULL,
    description           text,
    calendar_id           text NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    duration_minutes      integer NOT NULL,
    time_zone             text NOT NULL DEFAULT 'UTC',
    windows               jsonb NOT NULL DEFAULT '[]',
    buffer_before_min     integer NOT NULL DEFAULT 0,
    buffer_after_min      integer NOT NULL DEFAULT 0,
    daily_limit           integer NOT NULL DEFAULT 0,
    min_notice_min        integer NOT NULL DEFAULT 60,
    max_advance_days      integer NOT NULL DEFAULT 60,
    respect_working_hours boolean NOT NULL DEFAULT true,
    add_conferencing      boolean NOT NULL DEFAULT false,
    active                boolean NOT NULL DEFAULT true,
    created_at            timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX booking_links_slug_idx ON booking_links (lower(slug));
CREATE INDEX booking_links_user_idx ON booking_links (user_id);

CREATE TABLE bookings (
    id              text PRIMARY KEY,
    link_id         text NOT NULL REFERENCES booking_links(id) ON DELETE CASCADE,
    status          text NOT NULL DEFAULT 'hold', -- hold | confirmed | cancelled
    start_at        timestamptz NOT NULL,
    end_at          timestamptz NOT NULL,
    invitee_name    text NOT NULL,
    invitee_email   text NOT NULL,
    invitee_tz      text NOT NULL DEFAULT 'UTC',
    note            text,
    event_id        text REFERENCES events(id) ON DELETE SET NULL,
    hold_expires_at timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CHECK (end_at > start_at),
    -- The anti-double-booking constraint: two active (hold or confirmed)
    -- bookings on the same link can never overlap in time. Concurrent
    -- inserts race safely: exactly one commits, the loser gets SQLSTATE
    -- 23P01, which the repo maps to domain.ErrConflict.
    CONSTRAINT bookings_no_overlap EXCLUDE USING gist (
        link_id WITH =,
        tstzrange(start_at, end_at, '[)') WITH &&
    ) WHERE (status IN ('hold', 'confirmed'))
);
CREATE INDEX bookings_link_idx ON bookings (link_id, start_at DESC);
CREATE INDEX bookings_hold_expiry_idx ON bookings (hold_expires_at)
    WHERE status = 'hold';

CREATE TABLE meeting_polls (
    id               text PRIMARY KEY,
    user_id          text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token            text NOT NULL UNIQUE, -- crypto/rand 16 bytes hex
    title            text NOT NULL,
    description      text,
    calendar_id      text NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    duration_minutes integer NOT NULL,
    options          jsonb NOT NULL DEFAULT '[]', -- [{id,start,end}]
    status           text NOT NULL DEFAULT 'open',
    winner_option_id text,
    event_id         text REFERENCES events(id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX meeting_polls_user_idx ON meeting_polls (user_id);

CREATE TABLE poll_votes (
    poll_id     text NOT NULL REFERENCES meeting_polls(id) ON DELETE CASCADE,
    option_id   text NOT NULL,
    voter_email text NOT NULL,
    voter_name  text NOT NULL DEFAULT '',
    choice      text NOT NULL, -- yes | no | if_needed
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX poll_votes_unique_idx ON poll_votes (poll_id, option_id, lower(voter_email));

CREATE TABLE time_proposals (
    id             text PRIMARY KEY,
    event_id       text NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    proposer_email text NOT NULL,
    proposer_name  text NOT NULL DEFAULT '',
    start_at       timestamptz NOT NULL,
    end_at         timestamptz NOT NULL,
    note           text,
    status         text NOT NULL DEFAULT 'pending',
    created_at     timestamptz NOT NULL DEFAULT now(),
    CHECK (end_at > start_at)
);
CREATE INDEX time_proposals_event_idx ON time_proposals (event_id, created_at DESC);

CREATE TABLE user_settings (
    user_id          text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    time_zone        text NOT NULL DEFAULT 'UTC',
    working_hours    jsonb NOT NULL DEFAULT '[]',
    working_location text NOT NULL DEFAULT '',
    updated_at       timestamptz NOT NULL DEFAULT now()
);
