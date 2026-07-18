-- 0007_ai_suite.sql — M2.3 AI suite storage.

-- Live Auto Summarize + Instant Reply cache ride the thread row.
ALTER TABLE threads
    ADD COLUMN summary                    text NOT NULL DEFAULT '',
    ADD COLUMN summary_updated_at         timestamptz,
    ADD COLUMN instant_replies            jsonb NOT NULL DEFAULT '[]',
    ADD COLUMN instant_replies_updated_at timestamptz;

-- Auto Drafts are ordinary drafts flagged as provisional AI output.
ALTER TABLE drafts
    ADD COLUMN ai_generated boolean NOT NULL DEFAULT false;
CREATE INDEX drafts_ai_thread_idx ON drafts (thread_id) WHERE ai_generated;

-- Background AI job queue (drained by cmd/worker).
CREATE TABLE ai_jobs (
    id         text PRIMARY KEY,
    user_id    text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    account_id text NOT NULL REFERENCES connected_accounts(id) ON DELETE CASCADE,
    kind       text NOT NULL,
    thread_id  text REFERENCES threads(id) ON DELETE CASCADE,
    payload    jsonb NOT NULL DEFAULT '{}',
    attempts   integer NOT NULL DEFAULT 0,
    run_after  timestamptz NOT NULL DEFAULT now(),
    locked_at  timestamptz,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now()
);
-- One pending job per (kind, thread): message bursts collapse.
CREATE UNIQUE INDEX ai_jobs_dedup_idx ON ai_jobs (kind, thread_id) WHERE thread_id IS NOT NULL;
CREATE INDEX ai_jobs_due_idx ON ai_jobs (run_after);

-- User-defined natural-language classifiers (Auto Labels / custom splits).
CREATE TABLE ai_classifiers (
    id           text PRIMARY KEY,
    user_id      text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         text NOT NULL,
    prompt       text NOT NULL,
    target_split text NOT NULL DEFAULT '',
    label_name   text NOT NULL DEFAULT '',
    enabled      boolean NOT NULL DEFAULT true,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ai_classifiers_user_idx ON ai_classifiers (user_id);

-- Personal voice learning: one style profile per user, derived from sent mail.
CREATE TABLE voice_profiles (
    user_id      text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    profile      text NOT NULL,
    sample_count integer NOT NULL DEFAULT 0,
    model        text NOT NULL DEFAULT '',
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- Per-user daily AI budget (interactive + background share it).
CREATE TABLE ai_usage (
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    day     date NOT NULL,
    calls   integer NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, day)
);
