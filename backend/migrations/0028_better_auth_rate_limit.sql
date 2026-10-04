-- 0028_better_auth_rate_limit.sql — Better Auth rate limiting (piece 2).
--
-- apps/web/lib/rate-limit-storage.ts keeps per-IP auth counters in Postgres
-- (through Better Auth's adapter) so limits survive restarts and are shared
-- across web replicas. Column names are Better Auth's own (camel-cased,
-- quoted) and its atomic `incrementOne` relies on the unique `key`.
-- "lastRequest" is epoch milliseconds; node-postgres returns int8 as a
-- string, so the storage coerces it to a number on read. The storage prunes
-- rows older than the LONGEST rule window (600 s) in the background; the
-- index keeps that delete and the per-key lookup cheap.
CREATE TABLE IF NOT EXISTS "rateLimit" (
    "id"          text    NOT NULL PRIMARY KEY,
    "key"         text    NOT NULL UNIQUE,
    "count"       integer NOT NULL,
    "lastRequest" bigint  NOT NULL
);
CREATE INDEX IF NOT EXISTS "rateLimit_lastRequest_idx" ON "rateLimit" ("lastRequest");
