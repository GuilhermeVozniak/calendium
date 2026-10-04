-- 0028_better_auth_rate_limit.sql — Better Auth rate limiting (piece 2).
--
-- apps/web/lib/auth.ts sets `rateLimit: { storage: "database" }`, which keeps
-- per-IP auth counters in Postgres so limits survive restarts and are shared
-- across web replicas. Column names are Better Auth's own (camel-cased,
-- quoted) and its atomic `incrementOne` relies on the unique `key`. Better
-- Auth prunes rows older than the largest window in the background; the
-- index keeps that delete and the per-key lookup cheap.
CREATE TABLE IF NOT EXISTS "rateLimit" (
    "id"          text    NOT NULL PRIMARY KEY,
    "key"         text    NOT NULL UNIQUE,
    "count"       integer NOT NULL,
    "lastRequest" bigint  NOT NULL
);
CREATE INDEX IF NOT EXISTS "rateLimit_lastRequest_idx" ON "rateLimit" ("lastRequest");
