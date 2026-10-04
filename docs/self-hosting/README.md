# Self-Hosting Calendium

Calendium is **open-core**: the entire app — the Go API, background worker, web
client, desktop, and mobile apps — is free and open source (AGPL-3.0). There are
two ways to run it:

- **Self-hosted (free).** You run the backend + Postgres + web app on your own
  hardware (a VPS, home server, or a cloud VM) and point the desktop and mobile
  apps at *your* server. No Paddle, no paywall, **every feature unlocked**.
- **Calendium Cloud ($50/year).** We run and operate everything for you — the
  Go API, Postgres, the web app (Better Auth built in), push infrastructure — and
  bill through Paddle. See
  [`../payments.md`](../payments.md).

Self-hosting is free the way a puppy is free: you own the backups, upgrades, and
uptime. If you'd rather not think about servers, Cloud is the same product with
the operations handled for you. **Nothing is feature-gated** — the two tiers
differ only in *who runs it*.

---

## Cloud vs. Self-Host

| | **Self-Hosted** | **Calendium Cloud** |
| --- | --- | --- |
| Price | Free (you pay for your own VPS) | $50 / year |
| Who runs it | You | We do |
| Mail, calendar, AI, push, snooze, Send Later, split inbox | **All included** | **All included** |
| Updates | You run `docker compose pull && up -d` | Automatic |
| Backups | You (`make db-backup`, off-box copies) | Automatic + point-in-time |
| Uptime / SLA | Yours to keep | Managed |
| Support | Community (issues, docs) | Email support |
| Data location | Wherever you host it | Our managed region |
| Setup effort | ~15 minutes + DNS | One tap |
| Billing | Disabled — everything unlocked | Paddle ($50/yr) |

You are **not locked in**. Both tiers speak the same REST API and data model, so
you can start on Cloud and move to self-host later (or the reverse) — the same
desktop/mobile apps just point at a different server URL.

---

## What you actually run

A Calendium deployment is five containers on one Docker network (`calendium`),
all defined in the repo-root [`docker-compose.yml`](../../docker-compose.yml):

```
                          ┌──────────────────────────┐
   browser / clients ────▶│  caddy  (:80 / :443)      │  automatic HTTPS
                          │  profile: "caddy"         │  (optional proxy)
                          └────────┬─────────┬────────┘
                 /v1/*, /healthz   │         │   everything else
                          ┌────────▼───┐ ┌───▼──────────┐
                          │  api :8080 │ │  web :3000    │  Next.js standalone
                          │  cmd/api   │ │  apps/web     │
                          └──────┬─────┘ └──────────────┘
         runs SQL migrations     │
         at boot (embed.FS)      │
                          ┌──────▼──────┐   ┌───────────────┐
                          │  db :5432   │◀──│  worker       │  provider sync,
                          │ postgres:16 │   │  cmd/worker   │  scheduled send,
                          │ (internal)  │   │ (no HTTP port)│  snooze/push loops
                          └─────────────┘   └───────────────┘
```

| Service | Image / build | Port | Role |
| --- | --- | --- | --- |
| `db` | `postgres:16-alpine` | `5432` (internal only) | Postgres data on the `db_data` volume. Never published to the host. |
| `api` | `calendium-backend:latest` (`backend/Dockerfile`) | `${API_BIND:-127.0.0.1}:${API_PORT:-8080}` → 8080 | Stdlib `net/http` API. **Applies embedded SQL migrations at boot.** Serves `GET /healthz` and `GET /v1/instance`. |
| `worker` | same image, `command: [worker]` | none | Long-running poller: Gmail `historyId` / Graph delta sync, scheduled send, snooze/reminder wakeups, push dispatch. |
| `web` | `calendium-web:latest` (`apps/web/Dockerfile`) | `${WEB_BIND:-127.0.0.1}:${WEB_PORT:-3000}` → 3000 | Next.js 15 standalone server (`node apps/web/server.js`). `NEXT_PUBLIC_*` are baked at build time. |
| `caddy` | `caddy:2-alpine`, compose profile `caddy` | `80`, `443`, `443/udp` | Optional reverse proxy with automatic HTTPS. Routes `/v1/*` + `/healthz` → `api:8080`, everything else → `web:3000`. |

This mirrors the backend layout in [`../architecture.md`](../architecture.md):
`cmd/api` and `cmd/worker` are two entrypoints built from the *same* image.

---

## Requirements

**A host**
- Linux (Ubuntu 24.04 LTS or similar). ARM64 works (Raspberry Pi 4/5, Ampere,
  Graviton) — the images are multi-arch.
- **Minimum: 2 vCPU / 4 GB RAM / 20 GB disk.** RAM scales with the number of
  connected mail accounts (the worker holds a sync loop per account), so size up
  if you connect many mailboxes. The Next.js build is memory-hungry — on a 2 GB
  box, build the `web` image elsewhere and pull it, or add swap.

**Software**
- **Docker 29** (Engine) with the **Compose v5 plugin** (`docker compose`, not
  the legacy `docker-compose`).
- `make` and `openssl` (for the helper targets and secret generation).

**A domain (for HTTPS)**
- A domain or subdomain with a DNS **A/AAAA record** pointing at your host, and
  inbound ports **80 + 443** open. Caddy needs these reachable *before* first
  start so the ACME challenge can issue a certificate. (You can skip this for a
  local, no-TLS trial — see the Quickstart.)

**Authentication (built in — no external service)**
- Auth is **[Better Auth](https://better-auth.com)**, hosted by the web app on the
  same Postgres. Email + password works out of the box; you only set
  `BETTER_AUTH_SECRET` (`openssl rand -base64 32`) and `BETTER_AUTH_URL` (your
  domain), plus `INTERNAL_API_SECRET` (`openssl rand -hex 32`, the same value on
  `api`, `worker` and `web`) for the web app's server-to-server calls.
  `make gen-secrets` prints every required secret. No Supabase, no external identity provider to run. Social Google/Apple
  login is optional — see the Quickstart's auth step.

**Optional credentials (unlock their features when set)**
- **Google** (`GOOGLE_CLIENT_ID/SECRET`) and/or **Apple** (`APPLE_CLIENT_ID/SECRET`)
  for social sign-in. The Google app doubles as the Gmail/Calendar mailbox
  connector.
- **Google** and/or **Microsoft** (`MS_CLIENT_ID/SECRET`) OAuth apps to connect
  Gmail/Calendar and Outlook mailboxes. You register your **own** apps — Cloud's
  cannot be shared.
- **OpenRouter** (`OPENROUTER_API_KEY`) for AI compose/reply/summarize.
- **Push**: APNs, FCM, and/or Web Push (VAPID) keys.

Everything except `DATABASE_URL`, `TOKEN_ENCRYPTION_KEY`, `INTERNAL_API_SECRET`
(api, worker and web), and the web app's `BETTER_AUTH_SECRET`/`BETTER_AUTH_URL`
is optional — unwired adapters are simply
left out, so partial deployments boot fine.

---

## Choose your path

1. **[Quickstart →](./quickstart.md)** — the recommended Docker Compose path,
   end to end: clone, configure, generate secrets, point DNS, `docker compose
   --profile caddy up -d`, verify, connect a client. Includes a no-TLS
   "just run it locally" variant.
2. **[Configuration & environment reference →](./configuration.md)** — every
   environment variable (name / required? / default / description), grouped by
   concern, plus the `SELF_HOSTED` behavior and the `GET /v1/instance`
   discovery contract.
3. **[Pointing the apps at your server →](./clients.md)** — how the Web,
   Desktop, and Mobile clients connect to a self-hosted server: the "enter your
   server URL" flow, instance discovery, the Cloud preset, and building the
   apps from source to distribute internally.
4. **[Provider & integration setup →](./providers.md)** — authentication
   (Better Auth, built in), Google Cloud + Microsoft Entra OAuth apps (exact
   scopes & redirect URIs), OpenRouter AI, and APNs/FCM/Web-Push keys; what's
   required vs. optional.
5. **[Reverse proxy & HTTPS →](./reverse-proxy-tls.md)** — the bundled Caddy
   auto-TLS path, plus nginx+certbot and Traefik alternates.
6. **[Backups & restore →](./backups.md)** — `pg_dump`/`pg_restore`, cron,
   volume snapshots, and a restore drill.
7. **[Upgrading →](./upgrades.md)** — pull-and-restart, boot-time migrations, and
   rollback.
8. **[Security hardening →](./security.md)** — secrets, firewalling, keeping
   Postgres private, and TLS.
9. **[Troubleshooting →](./troubleshooting.md)** — symptom → cause → fix for the
   common self-host failures.

Reference deploy files already live in the repo: the
[`docker-compose.yml`](../../docker-compose.yml), [`.env.example`](../../.env.example),
[`Makefile`](../../Makefile), the Caddy config in
[`deploy/caddy/Caddyfile`](../../deploy/caddy/Caddyfile), an nginx sample in
[`deploy/nginx/calendium.conf`](../../deploy/nginx/calendium.conf), and a systemd
unit in [`deploy/systemd/calendium-compose.service`](../../deploy/systemd/calendium-compose.service).

---

## Operating your instance

The [`Makefile`](../../Makefile) wraps the common tasks (run `make help` to list
them). By default it runs the stack **with the Caddy profile**; append
`PROFILE=` to skip the bundled proxy (bring your own).

```bash
make self-host-up      # build & start db + api + worker + web (+ caddy)
make self-host-logs    # tail all service logs
make self-host-down    # stop the stack (keeps the db_data volume)
make self-host-up PROFILE=   # start WITHOUT the bundled Caddy proxy
```

### Backups

Provider refresh tokens are encrypted with `TOKEN_ENCRYPTION_KEY`, and all state
lives in Postgres (`db_data` volume). Two things to protect:

```bash
make db-backup                 # → backups/calendium-<timestamp>.sql.gz
make db-restore FILE=backups/calendium-20260706-020000.sql.gz
```

- **Copy the dumps off-box** (rclone/rsync to S3/R2/B2). A backup on the same
  disk is not a backup.
- **Back up `TOKEN_ENCRYPTION_KEY` separately and permanently.** It is *not* in
  the database. If you lose it, every stored provider refresh token becomes
  undecryptable and all users must reconnect their accounts. Keep it stable
  across deploys.
- Also worth snapshotting: the `caddy_data` volume (issued TLS certificates) so
  you don't re-request certs and risk Let's Encrypt rate limits.

### Upgrading

Migrations run **forward-only at api boot**, so upgrades are pull-and-restart:

```bash
make db-backup                 # 1. snapshot first
git pull                       # 2. new code (or: docker compose pull for images)
make self-host-up              # 3. rebuild + restart; api applies new migrations
make self-host-logs            # 4. confirm migrations applied + /healthz green
```

Keep the `api` at a single replica during a rollout (it owns migrations). App
rollback is trivial; **schema rollback is not** — if a new migration breaks the
old binary, restore the pre-upgrade dump. Prefer additive, backward-compatible
migrations across one release.

### Security hardening

- **Change every default secret.** Never boot with the `.env.example`
  placeholders (`POSTGRES_PASSWORD=change-me-please`, empty
  `TOKEN_ENCRYPTION_KEY` / `INTERNAL_API_SECRET` / `BETTER_AUTH_SECRET`).
  Generate real values (`make gen-secrets`).
- **Keep Postgres off the public network.** The bundled compose never publishes
  `5432` — leave it that way. Never add a `5432:5432` mapping on a public host.
- **Firewall the box.** Allow only `22` (SSH, from your IP), `80`, and `443`.
  Note: Docker's published ports bypass UFW's `INPUT` chain, so the compose's
  `api` (`8080`) and `web` (`3000`) host mappings are reachable even with UFW
  "deny". On a public server behind Caddy, either bind those to loopback (edit
  the mappings to `127.0.0.1:8080:8080` / `127.0.0.1:3000:3000`) or don't
  publish them at all — Caddy reaches them over the internal network by service
  name regardless.
- **Keep secrets out of git.** `.env` is git-ignored; keep it that way.

---

## Troubleshooting

| Symptom | Likely cause & fix |
| --- | --- |
| Caddy never gets a certificate | DNS A/AAAA record isn't pointing at the host yet, or ports 80/443 aren't reachable. Point DNS and open the ports **before** `up`. Set `ACME_EMAIL` for a real domain. |
| Redirect loop / mixed-content behind your own proxy | Your nginx/Traefik must set `X-Forwarded-Proto $scheme` (plus `X-Real-IP`, `X-Forwarded-For`). The sample [`deploy/nginx/calendium.conf`](../../deploy/nginx/calendium.conf) already does. |
| `connection refused` to the database | `DATABASE_URL` host must be the compose service name `db` (`...@db:5432/...`), not `localhost`. Check `db` is healthy: `docker compose ps`. |
| API exits on boot complaining about the key | `TOKEN_ENCRYPTION_KEY` must be **exactly 64 hex chars** (32 bytes). Regenerate with `make gen-secret`. |
| Provider connect fails / "redirect not allowed" | Add your web origin to `OAUTH_ALLOWED_REDIRECT_URIS`, **and** register the same redirect URIs in your Google Cloud / Microsoft Entra OAuth app. |
| Client shows a dead "subscribe" screen | It's talking to a cloud-mode server. On a self-host instance `features.billing` is `false` and the billing UI hides itself; confirm `SELF_HOSTED=true` and check `GET /v1/instance`. |
| Web app can't reach the API after changing the URL | `NEXT_PUBLIC_*` are baked at **build** time — rebuild the `web` image (`make self-host-up` rebuilds), don't just edit the container env. |

---

## FAQ

**Is anything feature-gated in self-host?** No. Leaving `PADDLE_*` unset (and
`SELF_HOSTED=true`) disables the paywall and unlocks everything. The billing
endpoints return `501 self_hosted` and `GET /v1/billing/subscription` reports an
active annual plan so clients treat you as fully entitled.

**What license is Calendium under?** The open core is **AGPL-3.0** (see the
repo-root [`LICENSE`](../../LICENSE)) — the same choice as Plausible, Grafana,
and others using a free-self-host + paid-cloud model. You can self-host, modify,
and redistribute; if you run a modified version as a network service you must
offer users its source. Cloud-only operational code (Paddle wiring, provisioning)
is not part of what you must distribute.

**Can I use my own domain?** Yes — set `DOMAIN` (and `ACME_EMAIL`) in `.env` and
Caddy handles HTTPS. See the Quickstart.

**Do I need Supabase or any external auth service?** No — that dependency is gone.
Authentication is **Better Auth**, built into the web app and backed by your own
Postgres. Email + password works out of the box; set `BETTER_AUTH_SECRET` and
`BETTER_AUTH_URL`, and optionally add Google/Apple OAuth creds for social login.

**Can I migrate between Cloud and self-host?** Yes — same API and data model.
Repoint the desktop/mobile apps at the other server URL (see
[clients](./clients.md)).

**Is there telemetry?** The web image sets `NEXT_TELEMETRY_DISABLED=1` (Next.js
telemetry off). The Go backend and worker phone home to nobody.

---

Next: **[Quickstart →](./quickstart.md)**
