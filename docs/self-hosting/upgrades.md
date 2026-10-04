# Upgrading

Upgrading Calendium is: **snapshot the database → pull new code/images → restart**.
Database migrations apply themselves at boot, so there's no separate migrate
step. Read this page once before your first upgrade — the migration model has a
couple of properties worth understanding.

---

## Versioning

The backend reports its version via the public `GET /v1/instance` endpoint
(`httpapi.Version`, currently `0.1.0`). Clients read it to gate features and show
an "update available" hint. Use it as a quick sanity check that a new build is
live:

```bash
curl -s https://your-domain/v1/instance | jq .version
```

The reference `docker-compose.yml` builds local images tagged
`calendium-backend:latest` and `calendium-web:latest`. For reproducible
upgrades and easy rollback, **pin an immutable tag** (a git SHA or release
version) rather than rebuilding `latest` in place — see
[Rollback](#rollback) below.

---

## The upgrade flow

```bash
cd /opt/calendium            # wherever you deployed

# 1. ALWAYS snapshot first.
make db-backup

# 2. Get the new code (if you build locally) ...
git pull

# 3. ... and/or pull prebuilt images (if you use a registry).
docker compose pull

# 4. Rebuild + restart. `make self-host-up` runs `up -d --build`
#    (with the caddy profile by default).
make self-host-up
#    or, without the bundled proxy:
#    make self-host-up PROFILE=

# 5. Watch the API apply migrations and come healthy.
docker compose logs -f api
curl -fsS https://your-domain/healthz     # -> 200
```

That's it. `NEXT_PUBLIC_API_URL` is baked into the web bundle at build time, so
`--build` (which `make self-host-up` does) picks up a changed API URL
automatically. Better Auth's own vars (`BETTER_AUTH_SECRET`, `BETTER_AUTH_URL`,
`GOOGLE_*`, `APPLE_*`) are read at **runtime**, so changing them only needs a
restart, not a rebuild.

---

## How migrations run

Database migrations are **embedded** in the binary
([`backend/migrations/*.sql`](../../backend/migrations) via `embed.FS`) and
applied at startup by [`internal/migrate`](../../backend/internal/migrate/migrate.go).
Key properties:

- **Applied at boot, automatically.** Both `cmd/api` **and** `cmd/worker` call
  the migrator on startup. You never run a migrate command by hand.
- **Safe when api + worker start together.** The migrator takes a Postgres
  **advisory lock** before running, so whichever container wins applies the
  migrations while the other waits — no race, no duplicate application. The
  worker will never run against a half-migrated schema.
- **Idempotent & tracked.** Applied versions are recorded in a
  `schema_migrations` table; already-applied files are skipped. Each migration
  runs in its own transaction together with its bookkeeping row, so a failed
  migration rolls back cleanly.
- **Ordered lexicographically.** Files are applied in filename order
  (`0001_init.sql`, `0002_better_auth.sql`, `0003_subscription_event_order.sql`,
  …). The Better Auth schema (`0002_better_auth.sql`) is applied by the same
  boot migrator — there is no separate JS migration step at deploy.
- **Forward-only.** There are no down-migrations. Rolling *back* schema means
  restoring a dump (below).

If a migration fails, the container exits non-zero and logs
`migrate: apply <file>: ...`. Fix the cause (usually a manual DB edit that
conflicts) or restore your pre-upgrade snapshot, then restart.

---

## Better Auth schema

Better Auth's tables (`user`, `session`, `account`, `verification`, `jwks`) are
owned by the Go backend's boot migrations, committed as
[`backend/migrations/0002_better_auth.sql`](../../backend/migrations). There is
**no separate JS migration step** at deploy — the Go migrator applies it like
any other file, so `backend/migrations/*.sql` owns the entire schema.

When you bump the `better-auth` version, its required schema can change. Most
upgrades are code-only, but if the changelog calls for a schema change,
regenerate the migration and re-adapt it to plain idempotent Postgres DDL before
deploying:

```bash
# from apps/web, pointed at the Better Auth config (apps/web/lib/auth.ts)
bunx @better-auth/cli generate
# review the diff, fold it into backend/migrations/0002_better_auth.sql as
# idempotent DDL (CREATE TABLE IF NOT EXISTS / ADD COLUMN IF NOT EXISTS),
# commit, then upgrade as usual — the backend applies it at boot.
```

---

## Turning on email (SMTP)

- **Adding `SMTP_HOST`/`SMTP_FROM` turns email verification on** for
  email+password accounts. Existing users are not grandfathered: their next
  sign-in answers "Verify your email first — we sent a new link." and mails the
  link; after one click they sign in as before. Google/Apple sign-ins are
  unaffected. Tell your users before you flip it. Removing SMTP turns
  verification off again; nothing in the database changes.
- This release adds migration `0028_better_auth_rate_limit.sql` (the `rateLimit`
  table behind the sign-in rate limits); the API applies it at boot like any other.
- **Cloud (`SELF_HOSTED=false`)**: `api`, `worker` and `web` now refuse to start
  without `SMTP_HOST` and `SMTP_FROM` — set them before upgrading.
- **`web` now reads `SELF_HOSTED`.** The compose stack already passes `.env` to
  `web`. If you run the web app another way (for example `bun run dev:web` with
  `apps/web/.env`), add `SELF_HOSTED=true` for a self-hosted instance, or it
  boots in cloud mode and stops with the SMTP error.
- **localhost origins now need `ALLOW_DEV_ORIGINS=true`.** The API allows
  `http://localhost:*` / `http://127.0.0.1:*` origins only with it, in every
  environment. `web` also trusts them under `next dev`. If a browser client
  served from `http://localhost:<port>` talks to a production server, add that
  origin to `CORS_ALLOWED_ORIGINS`, or set `ALLOW_DEV_ORIGINS=true` (which logs a
  warning). The desktop app is unaffected.
- **`TRUST_PROXY` defaults to `true` under Compose.** The `web` service sets
  `TRUST_PROXY: ${TRUST_PROXY:-true}`, so behind the bundled Caddy each client
  gets its own auth rate-limit bucket without touching `.env`. Keep the proxy's
  address inside `TRUSTED_PROXY_CIDRS` (the default covers loopback and private
  ranges; see
  [Security → Client IP](./security.md#client-ip-trust_proxy-and-trusted_proxy_cidrs)).
  If you run `web` outside Compose behind a proxy, set `TRUST_PROXY=true`
  yourself: with `false` only the immediate peer, the proxy, is used, so every
  client shares one bucket on all auth endpoints, including JWT minting. Set
  `TRUST_PROXY=false` only if `web:3000` is exposed directly to a LAN with no
  proxy in front.

## Platform hardening release

- **`api` and `web` now publish on `127.0.0.1` only.** A proxy on the same
  host (the bundled Caddy profile, nginx, Traefik) keeps working unchanged. If
  clients reach `:3000` / `:8080` directly over the LAN, add
  `API_BIND=0.0.0.0`, `WEB_BIND=0.0.0.0` and `TRUST_PROXY=false` to `.env`. If
  you followed the old advice of `API_PORT=127.0.0.1:8080` /
  `WEB_PORT=127.0.0.1:3000`, change them back to plain port numbers.
- **The API now honours `TRUST_PROXY` too.** Compose sets
  `${TRUST_PROXY:-true}` on `api` as it already did on `web`, with the same
  `TRUSTED_PROXY_CIDRS`, so per-IP limits on public pages and the request logs
  see the real client
  ([Security → Client IP](./security.md#client-ip-trust_proxy-and-trusted_proxy_cidrs)).
- **Signed-in API calls are rate limited per user** (`RATE_LIMIT_*`, see
  [Configuration → Platform hardening](./configuration.md#platform-hardening));
  the defaults are well above interactive use.
- **`caddy` waits for `api` and `web` to be healthy** (`/readyz` and
  `/api/health`), so the first `up` after a rebuild takes a few seconds longer.
- **Boot checks are stricter.** `BETTER_AUTH_SECRET` shorter than 32 bytes
  stops `web`; a default `DATABASE_URL` password (`change-me-please` /
  `calendium`) stops the API in cloud mode and logs a warning when
  self-hosted.

---

## Keep migrations backward-compatible

Because the API applies new migrations the moment a new image starts, design
schema changes to be **compatible with the previous app version** across one
release (additive columns, nullable/defaulted, no destructive renames in the
same release). That way an app rollback doesn't hit a schema it can't read, and
a brief overlap during a rolling restart is harmless. This is standard practice;
it mostly matters if you author your own migrations on a fork.

---

## Rollback

**App rollback is easy; schema rollback is not** (migrations are forward-only).

- **If you pinned immutable tags:** set the tag back to the previous value and
  redeploy.

  ```bash
  # e.g. with a TAG var wired into your compose image references
  TAG=<previous-sha> docker compose up -d
  ```

- **If the new release added an incompatible migration:** the clean rollback is
  to **restore the pre-upgrade dump** you took in step 1, then start the old
  images:

  ```bash
  make db-restore FILE=backups/calendium-<pre-upgrade-timestamp>.sql.gz
  # then bring up the previous image tag
  ```

This is exactly why step 1 (`make db-backup`) is non-negotiable.

---

## Single-replica / near-zero-downtime notes

- **Keep `api` at a single replica during the rollout.** Migrations are
  serialized by the advisory lock, but a single migrating replica keeps behavior
  simplest.
- **Rate limits and shutdown assume that single `api` replica.** Limits are
  per process (`RATE_LIMIT_*`, see [Configuration](./configuration.md#platform-hardening)),
  so two replicas double every budget. On stop the API answers `503` on
  `/readyz`, waits `SHUTDOWN_DRAIN_DELAY`, then gives in-flight requests up to
  `SHUTDOWN_TIMEOUT` (default 30 s) before closing; SSE clients reconnect on
  their own.
- **Watch the drain in the logs** during `make self-host-up`:
  `api: shutdown requested; draining` … `api: shut down cleanly`. A
  `worker: shutdown timed out` line means a loop ignored its cancelled
  context past `SHUTDOWN_TIMEOUT` — the worker exits 1 and compose restarts it.
- **The `worker` must stay at exactly one replica** regardless of upgrades —
  it's a continuous poller (Gmail `historyId` / Graph delta / scheduled send).
  Two workers would double-sync.
- **Restart pause is brief.** With `restart: unless-stopped` and healthchecks,
  the proxy sees `api`/`web` go unhealthy for a few seconds during the swap.
  For true zero-downtime on managed platforms (Fargate / Cloud Run / Container
  Apps), run migrations as a one-off task of the new API image, then do a
  rolling deploy of `api` and `web`, keeping `worker` at one.

---

## Updating the bundled Postgres / base images

The stack pins `postgres:16-alpine`. Postgres minor upgrades within a major
(16.x) are drop-in — `docker compose pull db && docker compose up -d db`. A
**major** Postgres upgrade (e.g. 16 → 17) requires a dump/restore or
`pg_upgrade`, not just a tag bump — `pg_dump` on the old version, then restore
into a fresh 17 volume. Snapshot first. See
[Security → keep base images current](./security.md#7-keep-base-images-current)
for the other images.

---

## Related pages

- [Backups & Restore](./backups.md) — the snapshot you take before every upgrade
- [Security Hardening](./security.md) — updating base images
- [Troubleshooting](./troubleshooting.md) — migration failures at boot
- [Architecture](../architecture.md) — the api/worker split and sync model
