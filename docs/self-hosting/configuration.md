# Configuration & environment reference

Every Calendium container reads its settings from a single `.env` file. Start
from [`.env.example`](../../.env.example) (the compose/self-host template) and
fill it in. Only **`DATABASE_URL`** and **`TOKEN_ENCRYPTION_KEY`** are required
to boot; authentication (Better Auth) is built into the web app and needs only `BETTER_AUTH_SECRET`; everything else unlocks its own
adapter when set and is otherwise ignored.

> **Two `.env.example` files.** The **repo-root**
> [`.env.example`](../../.env.example) is the one the Docker Compose stack uses
> (defaults to `SELF_HOSTED=true`, `DATABASE_URL` host `db`). The
> **[`backend/.env.example`](../../backend/.env.example)** is for running the Go
> backend binary *directly* (defaults to `SELF_HOSTED=false`, `DATABASE_URL`
> host `localhost`, and adds `HTTP_ADDR`/`PORT`). Self-hosters use the root one.

> **Booleans.** `SELF_HOSTED`, `SMTP_SECURE`, `ALLOW_DEV_ORIGINS` and
> `TRUST_PROXY` accept `true`/`1`/`yes` and `false`/`0`/`no`
> (case-insensitive); blank means `false` (except `TRUST_PROXY` under
> Compose, which defaults to `true`). Every service that reads one parses it
> the same way, and any other value stops that service at boot with the
> variable named.

See also: [Quickstart](./quickstart.md) · [Clients](./clients.md) ·
[Overview](./README.md).

---

## Core & database

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `DATABASE_URL` | **Yes** | — | Postgres DSN for `api` + `worker`. In compose the host is the service name `db`: `postgres://calendium:<pw>@db:5432/calendium?sslmode=disable`. Use `sslmode=require` for managed Postgres. |
| `TOKEN_ENCRYPTION_KEY` | **Yes** | — | 32-byte AES-256-GCM key as **exactly 64 hex chars**. Generate with `make gen-secret` / `openssl rand -hex 32`. Encrypts provider refresh tokens at rest. Wrong length fails config validation at boot. **Back it up separately.** |
| `POSTGRES_USER` | No | `calendium` | Bundled `db` service user. |
| `POSTGRES_PASSWORD` | **Yes** | `calendium` | Bundled `db` password — **change it**. The API refuses to boot in cloud mode (and warns loudly when `SELF_HOSTED=true`) when `DATABASE_URL` still carries `change-me-please` or `calendium`. Must match the password in `DATABASE_URL`. |
| `POSTGRES_DB` | No | `calendium` | Bundled `db` database name. |
| `HTTP_ADDR` | No | `:8080` | Listen address for the API. Compose sets `:8080` on the `api` service; only relevant when running the binary directly. `PORT` is honored as an alternative. |
| `WEB_PORT` | No | `3000` | Host port mapped to the `web` container's `3000`. |
| `API_PORT` | No | `8080` | Host port mapped to the `api` container's `8080`. |
| `API_BIND` / `WEB_BIND` | No | `127.0.0.1` | Host interface the `api`/`web` ports bind to. Loopback keeps them reachable only through a proxy on the same host; `0.0.0.0` exposes them on the LAN (then set `TRUST_PROXY=false`, or narrow `TRUSTED_PROXY_CIDRS` to your proxy: the default trusts Docker's `172.16.0.0/12` gateway). |

## Instance identity & self-host mode

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `SELF_HOSTED` | No | `false` | `true` puts the instance in self-host mode: entitlement gating becomes a no-op (**all features unlocked**), the billing endpoints return `501 self_hosted`, and `GET /v1/instance` reports `mode: self_host`. The root `.env.example` defaults it to `true`. Read by `api`, `worker` **and `web`**: with `false` (cloud mode) all three refuse to start unless `SMTP_HOST` and `SMTP_FROM` are set ([Transactional email](#transactional-email-smtp)). |
| `INSTANCE_NAME` | No | `Calendium` | Display name shown to clients on the connect screen and via `GET /v1/instance`. |
| `APP_URL` | No | — | Public web origin, used to build absolute links (no trailing slash). Fallback source for `PUBLIC_WEB_URL`. |
| `PUBLIC_WEB_URL` | No | falls back to `APP_URL` | Public web origin advertised to clients (the `authBaseUrl` in `GET /v1/instance`). **When both are set, `PUBLIC_WEB_URL` wins over `APP_URL`;** blank falls back to `APP_URL`. The web app also adds it to Better Auth's trusted origins. Required by `api` in both modes; the `worker` errors without it in cloud mode and warns when self-hosted. |
| `DOMAIN` | No | `localhost` | Domain the bundled Caddy proxy serves (automatic HTTPS). With `localhost`, Caddy serves a locally-trusted internal cert. |
| `ACME_EMAIL` | No | — | Email Let's Encrypt uses for expiry notices (Caddy profile). Set it for a real domain. |

## Authentication (Better Auth)

Auth is **[Better Auth](https://better-auth.com)**, hosted by the web app at
`${BETTER_AUTH_URL}/api/auth/*` on the same Postgres; the Go backend is a resource
server that verifies its EdDSA (Ed25519) JWTs against the published JWKS (RS256/
ES256 also supported). Email + password works out of the box. Full setup:
[Providers → Authentication](./providers.md#1-authentication-better-auth--built-in).

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `BETTER_AUTH_SECRET` | **Yes** (web) | — | Better Auth signing/encryption secret, **at least 32 bytes** (`openssl rand -base64 32`). The web server refuses to start with a shorter value — in production, in `bun run dev`, everywhere. **Secret — never expose.** |
| `BETTER_AUTH_URL` | **Yes** | — | Public web origin hosting Better Auth (your domain, **no trailing slash**), e.g. `https://mail.example.com`. Read by the web app, and by the Go API to derive JWKS/issuer. |
| `AUTH_JWKS_URL` | No | in-network `http://web:3000/api/auth/jwks` (compose) | JWKS endpoint the backend fetches Better Auth's public keys from, **server-to-server**. The bundled `docker-compose.yml` defaults it to the in-network `web` service (the public `BETTER_AUTH_URL` usually isn't reachable from the `api` container), so **leave it blank**. Running the API binary directly it derives from `BETTER_AUTH_URL` (`${BETTER_AUTH_URL}/api/auth/jwks`). Override only to a URL the API can actually reach. |
| `AUTH_ISSUER` | No | `${BETTER_AUTH_URL}` | Expected token `iss`, pinned by the backend. Empty disables issuer pinning. |

## Web app (Next.js)

Better Auth's settings (`BETTER_AUTH_SECRET`, `BETTER_AUTH_URL`, `DATABASE_URL`,
`GOOGLE_*`, `APPLE_*`) are read at **runtime** by the web server — not baked into
the browser bundle — so there are **no `NEXT_PUBLIC_*` auth variables**. The web
app talks to Better Auth **same-origin** at `/api/auth`. The only build-time value
is `NEXT_PUBLIC_API_URL`.

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `NEXT_PUBLIC_API_URL` | No | `http://localhost:8080` | Where the browser reaches the Go API. **Inlined at build time** (Docker build arg) — rebuild the `web` image to change it. Behind the bundled proxy set it to your domain, or **leave blank** to use same-origin relative `/v1/…` requests. |
| `CORS_ALLOWED_ORIGINS` | No | — | Comma-separated extra browser origins trusted by **both** the Go API (CORS) and Better Auth (trusted origins). Always trusted without listing them: `BETTER_AUTH_URL`, `PUBLIC_WEB_URL`, the desktop app's WebView origins (`wails://wails`, `wails://wails.localhost`, `http(s)://wails.localhost`), `calendium://` and `https://appleid.apple.com` (Apple's sign-in `form_post`; trusted, never reflected in CORS). |
| `ALLOW_DEV_ORIGINS` | No | `false` | Also allow `http://localhost:*` / `http://127.0.0.1:*` (and `[::1]` in CORS). The Go API allows these origins **only** when this is `true`, in every environment. Better Auth (`web`) also trusts them under `next dev`. The desktop WebView origins, `CORS_ALLOWED_ORIGINS`, `PUBLIC_WEB_URL` and `BETTER_AUTH_URL` are always allowed, whatever this is set to. In production keep it blank: `true` makes `web` log a startup warning. Read by `web` and `api`. |
| `TRUST_PROXY` | No | `true` under Compose (`false` when `web`/`api` run outside it) | How the web app and the Go API find the client IP — the web app for its auth rate limits (every `/api/auth/*` endpoint, including session reads and JWT minting), the API for its public-endpoint rate limits, request logs and (with `X-Forwarded-Proto`/`Host`) the OAuth callback origin: the right-most `X-Forwarded-For` entry that is not a trusted proxy. When every hop is trusted (a LAN/VPN client behind the proxy) both take the left-most valid entry; when the socket peer is not trusted, both use the peer. Forwarded headers count only when the socket peer is itself inside `TRUSTED_PROXY_CIDRS`. `true`: entries in `TRUSTED_PROXY_CIDRS` are skipped from the right, so behind the bundled Caddy each client gets its own bucket. `false`: no proxy is trusted, so only the immediate peer counts and a client-supplied `X-Forwarded-For` is never used. Behind a proxy every client then shares the proxy's bucket on all auth endpoints, and production logs a warning. Set `false` only when `web:3000` / `api:8080` are exposed directly to a LAN with no proxy in front (`WEB_BIND`/`API_BIND=0.0.0.0`). Accepts `true`/`1`/`yes` and `false`/`0`/`no` (case-insensitive); anything else stops the service at boot. `docker-compose.yml` sets `${TRUST_PROXY:-true}` on both `web` and `api`. See [Security → Client IP](./security.md#client-ip-trust_proxy-and-trusted_proxy_cidrs). |
| `TRUSTED_PROXY_CIDRS` | No | `127.0.0.0/8,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,::1/128,fc00::/7` | Comma-separated proxy ranges skipped from the right of `X-Forwarded-For` when `TRUST_PROXY=true`. Each entry is a CIDR or a bare IP (that one host, `/32` or `/128`); whitespace is trimmed and empty entries are ignored. Validated at boot even when `TRUST_PROXY=false`. The default covers loopback and private networks, including the bundled Caddy on the Compose network. Add your CDN or load balancer ranges if they are public. Narrow it to your proxy's own address if untrusted clients can reach `web` or `api` from a private range. The default `TRUSTED_PROXY_CIDRS` includes `172.16.0.0/12`, which is Docker's own bridge gateway: with `API_BIND`/`WEB_BIND=0.0.0.0`, docker-proxy, rootless Docker and Docker Desktop can source-NAT direct clients to that gateway, so with `TRUST_PROXY=true` every LAN client would count as a trusted proxy and could forge `X-Forwarded-For` (and, on the API, `X-Forwarded-Host` / `X-Request-Id`). Set `TRUST_PROXY=false`, or narrow `TRUSTED_PROXY_CIDRS` to your proxy's own address. An invalid entry stops the service at boot. Read by `web` and `api`. |

## Transactional email (SMTP)

One instance-wide sender. The **web** app sends email-verification and
password-reset mail; the **api** sends team invitations when the inviter has
no connected mailbox. All three containers read the same variables.
Provider setup and DNS: [Providers → Transactional email](./providers.md#1d-transactional-email-smtp).

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `SMTP_HOST` | Cloud: **yes**. Self-host: no | — | SMTP server hostname. Required together with `SMTP_FROM` as soon as any of `SMTP_HOST`, `SMTP_FROM`, `SMTP_USER` or `SMTP_PASS` is set. `SMTP_PORT` and `SMTP_SECURE` alone never trigger it, and the env templates pre-fill both. Blank on self-host = email off. |
| `SMTP_PORT` | No | `587` | Integer 1–65535 (`465` for implicit TLS). |
| `SMTP_USER` / `SMTP_PASS` | No | — | Login; set both or neither. **`SMTP_PASS` is a secret.** |
| `SMTP_FROM` | With `SMTP_HOST` | — | `addr` or `Name <addr>`, e.g. `Calendium <no-reply@mail.example.com>`. |
| `SMTP_SECURE` | No | `false` | `true` = implicit TLS; `false` = STARTTLS, **required** unless `SMTP_HOST` is loopback (`127.0.0.1`, `localhost`, `::1`). The server certificate is always verified. |

| | SMTP configured | No SMTP (self-host only) |
| --- | --- | --- |
| Email+password sign-up | Must verify the address (link valid 24 h); same response whether or not the address exists | Signed in immediately; a duplicate address is reported |
| Forgot password | Reset link by email (valid 1 h, signs out every device) | Page explains the administrator resets passwords ([procedure](./security.md#resetting-a-password-without-email-self-host)) |
| Team invitation, inviter has no connected mailbox | Sent from `SMTP_FROM`, `Reply-To` = inviter | The inviter gets a link to share (`delivery: "link"`, expires in 14 days) |
| `GET /v1/instance` `features.email` | `true` | `false` |

Startup: with `SELF_HOSTED=false` and no SMTP, `api`, `worker` and `web` exit
with `SMTP_HOST and SMTP_FROM are required when SELF_HOSTED=false (cloud mode); set them or run with SELF_HOSTED=true`;
a half-configured block exits with `SMTP_* is partially configured: <missing>`. A block is half-configured when any of `SMTP_HOST`, `SMTP_FROM`, `SMTP_USER` or `SMTP_PASS` is set but `SMTP_HOST` or `SMTP_FROM` is missing, or when only one of `SMTP_USER` and `SMTP_PASS` is set.
Self-host without SMTP boots and logs
`email: disabled (no SMTP_HOST); verification off, invitations fall back to links`.

## Provider OAuth apps (mail + calendar) & social login

Register your **own** Google Cloud and Microsoft Entra OAuth apps — per
deployment, not shared with Cloud. The **Google** app does double duty: Better
Auth "Continue with Google" login **and** Gmail/Calendar mailbox-connect (register
both redirect URIs on the one client). See [providers](./providers.md) for
redirect-URI setup.

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `PUBLIC_API_URL` | No | derived from request | Public origin of the Go API itself — the base for the mailbox-connect OAuth callback. Register `${PUBLIC_API_URL}/v1/accounts/callback/{google\|microsoft}` as the redirect URI in the provider console. Blank derives `scheme://host` from the incoming connect request (honoring `X-Forwarded-Proto`/`Host`), which is correct behind the bundled Caddy proxy. |
| `OAUTH_ALLOWED_REDIRECT_URIS` | No | — | Comma-separated extra allowed OAuth redirect targets, **appended** to the built-in `localhost` + `calendium://` defaults. This is the client return URL the browser lands on *after* the callback completes (your web app) — **not** the provider redirect URI above. Add your web origin here or provider connect flows are rejected. |
| `GOOGLE_CLIENT_ID` | No | — | Enables Google **login** (`authProviders` gains `google`) **and** Gmail + Google Calendar connect (`features.google`). Shared between both. |
| `GOOGLE_CLIENT_SECRET` | No | — | Google OAuth client secret. |
| `APPLE_CLIENT_ID` | No | — | Enables **Sign in with Apple** (`authProviders` gains `apple`). Apple Services ID. |
| `APPLE_CLIENT_SECRET` | No | — | Apple OAuth client secret. |
| `MS_CLIENT_ID` | No | — | Enables Microsoft Graph mail + calendar. Sets `features.microsoft` when present. |
| `MS_CLIENT_SECRET` | No | — | Microsoft OAuth client secret. |

## AI (OpenRouter)

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `OPENROUTER_API_KEY` | No | — | Enables the AI suite: interactive `/v1/ai/compose`, `/v1/ai/ask`, `/v1/ai/event-proposal`, `/v1/mail/threads/{id}/instant-replies`, `/v1/classifiers`, and the worker's background `ai_jobs` loop (thread summaries, instant replies, auto drafts, auto labels, reminder detection, voice learning). Sets `features.ai` when present; unset, the ai-jobs loop never starts and the interactive endpoints return `503`. |
| `OPENROUTER_MODEL` | No | `openrouter/auto` | Model slug for AI requests. |
| `AI_DAILY_LIMIT` | No | `300` | Per-user daily AI call budget, shared by the interactive endpoints above and the background `ai_jobs` queue. Rearms at the next UTC midnight. Must be a positive integer. |

## Push notifications

`features.push` becomes `true` when **any** push channel is configured: APNs
(`APNS_KEY_P8`), FCM (`FCM_SERVICE_ACCOUNT_JSON`), or Web Push (both
`VAPID_PUBLIC_KEY` and `VAPID_PRIVATE_KEY`). When Web Push is configured,
`GET /v1/instance` also advertises the public key as `vapidPublicKey` so the web
client can register its service worker (`public/sw.js`) and subscribe; the worker
then delivers a push to the user's devices on newly-synced important/VIP messages.

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `APNS_KEY_ID` | No | — | APNs key ID (iOS/macOS, HTTP/2 + ES256 JWT). |
| `APNS_TEAM_ID` | No | — | Apple developer team ID. |
| `APNS_KEY_P8` | No | — | PEM contents of the `.p8` key (newlines escaped as `\n`), **not** a file path. Presence enables the push feature flag. |
| `APNS_TOPIC` | No | `app.calendium` | APNs topic — must equal your iOS/macOS app's **bundle id**. Change it to match the bundle id of the build you distribute (see [Clients → build from source](./clients.md)), or APNs rejects the pushes. |
| `FCM_SERVICE_ACCOUNT_JSON` | No | — | FCM v1 service-account JSON on a single line (Android). |
| `VAPID_PUBLIC_KEY` | No | — | Web Push VAPID public key. |
| `VAPID_PRIVATE_KEY` | No | — | Web Push VAPID private key. Both VAPID keys must be set to enable Web Push. |

## Mail behavior

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `UNDO_SEND_SECONDS` | No | `15` | Undo-send grace window (seconds) for "send now". |

## Billing (Cloud only — leave blank when self-hosting)

Self-hosted instances leave these empty. With `SELF_HOSTED=true` the billing
adapter stays unwired and the checkout/portal/webhook endpoints return
`501 self_hosted`.

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `PADDLE_ENV` | No | `sandbox` | `sandbox` or `live` — selects the Paddle API origin. |
| `PADDLE_API_KEY` | **Yes (cloud)** | — | Paddle API key. Cloud mode refuses to start without it. |
| `PADDLE_WEBHOOK_SECRET` | **Yes (cloud)** | — | Notification-destination secret for `Paddle-Signature` verification. Cloud mode refuses to start without it. |
| `PADDLE_PRICE_ID_ANNUAL` | **Yes (cloud)** | — | Paddle price id (`pri_…`) for the $50/yr plan (see [`../payments.md`](../payments.md)). |
| `BILLING_RECONCILE_INTERVAL` | No | `6h` | Worker loop that re-reads stale subscriptions from Paddle. |
| `NEXT_PUBLIC_PADDLE_CLIENT_TOKEN` | **Yes (cloud, web build)** | — | Paddle.js client token, inlined at web build time. |
| `NEXT_PUBLIC_PADDLE_ENV` | No | `sandbox` | `sandbox` or `production`, inlined at web build time; `sandbox` while `PADDLE_ENV=sandbox`, `production` when `PADDLE_ENV=live` (Paddle.js uses `production`, the API uses `live`). |

---

## Platform hardening

Reverse-proxy trust, rate limits, shutdown and logging. Every value has a safe
default; the compose file only overrides `TRUST_PROXY`.

| Variable | Where | Default | Description |
| --- | --- | --- | --- |
| `TRUST_PROXY` | api, web | `false` (`true` under Compose) | Honour `X-Forwarded-For` (API: also `X-Forwarded-Proto/Host`) from peers inside `TRUSTED_PROXY_CIDRS`. `docker-compose.yml` sets `${TRUST_PROXY:-true}` on `api` and `web`. Keep `false` when clients reach the API directly. Full rule in the [Web app table](#web-app-nextjs). |
| `TRUSTED_PROXY_CIDRS` | api, web | `127.0.0.0/8,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,::1/128,fc00::/7` | Comma-separated CIDRs or bare IPs (one host); blanks ignored, an invalid entry fails boot. Narrow it to your proxy's address when clients share the private network or `API_BIND`/`WEB_BIND` is `0.0.0.0` (the default trusts Docker's `172.16.0.0/12` gateway). |
| `RATE_LIMIT_PUBLIC_READ_PER_MIN` | api | `60` (burst 30) | Per client IP: public booking/poll/share GETs. |
| `RATE_LIMIT_PUBLIC_WRITE_PER_MIN` | api | `5` (burst 5) | Per client IP: public booking/poll POSTs. |
| `RATE_LIMIT_USER_PER_MIN` | api | `600` | Per user: every authenticated route not in a class below. `0` disables the class. |
| `RATE_LIMIT_MUTATE_HEAVY_PER_MIN` | api | `30` | Per user: sends, bulk actions, inbox zero, subscriptions, invitations, booking links, polls, thread shares, AI calls. |
| `RATE_LIMIT_SEARCH_PER_MIN` | api | `120` | Per user: search, attachment search, place autocomplete. |
| `SHUTDOWN_TIMEOUT` | api, worker | `30s` | Go duration. In-flight requests / loop passes get this long after SIGTERM; the worker exits 1 if a loop overruns it. Keep `SHUTDOWN_DRAIN_DELAY + SHUTDOWN_TIMEOUT` + 5 s below the compose `stop_grace_period` (40 s). |
| `SHUTDOWN_DRAIN_DELAY` | api | `0s` | How long `/readyz` answers `503 draining` before listeners close (set `2s`–`5s` behind a load balancer that polls readiness). |
| `LOG_FORMAT` | api, worker | `json` | `json` for log shippers, `text` for a terminal. Request lines carry `request_id` (also sent as `X-Request-Id`), method, route pattern, status, duration, bytes, client IP, user id and actor id; tokens and query strings are never logged. |
| `LOG_LEVEL` | api, worker | `info` | `debug`, `info`, `warn` or `error`. |
| `CSP_REPORT_ONLY` | web (runtime) | `true` | `true` sends the web app's Content-Security-Policy as report-only (violations are logged as `csp_violation` lines via `/api/csp-report`, URLs reduced to origin + path with share/poll/booking/reset tokens redacted, at most 5 per request and 60 per client IP a minute); `false` enforces it. Runtime, no rebuild. |

Rate limits are **per process**; the reference stack runs one `api` replica.
A limited request answers `429` with a `Retry-After` header; an oversized body
answers `413 payload_too_large` (1 MiB default, 10 MiB for drafts, 16 KiB on
public routes); a field over its limit answers `400 validation_failed` with
`details: {"field", "limit"}`.

## Minimum viable configuration

The smallest `.env` that boots a *usable* self-host instance:

```dotenv
SELF_HOSTED=true
POSTGRES_PASSWORD=<strong password>
DATABASE_URL=postgres://calendium:<same password>@db:5432/calendium?sslmode=disable
TOKEN_ENCRYPTION_KEY=<openssl rand -hex 32>

# Authentication (Better Auth — built into the web app; email+password out of the box):
BETTER_AUTH_SECRET=<openssl rand -base64 32>
BETTER_AUTH_URL=https://mail.example.com   # your public web origin, no trailing slash
```

Add `SMTP_*` to turn on email verification and self-service password reset
(required in cloud mode), `GOOGLE_*` / `APPLE_*` for social sign-in (the Google
creds also connect Gmail/Calendar), `MS_*` for Outlook, `OPENROUTER_API_KEY` for
AI, and push keys as needed — each unlocks its adapter without touching the rest.

---

## How `SELF_HOSTED` changes behavior

Setting `SELF_HOSTED=true` flips the instance into open-core self-host mode:

- **Entitlement gating is a no-op.** Every feature is unlocked; there is no
  paywall. (Internally the entitlement check short-circuits, so the hexagonal
  boundaries stay intact — no billing adapter is wired.)
- **`GET /v1/billing/subscription`** reports an **active annual plan** (`plan:
  annual`, `priceUsd: 50`, all period fields `null`) so clients treat the user
  as fully entitled. The `SubscriptionStatus` type is unchanged from Cloud.
- **`POST /v1/billing/checkout`, `POST /v1/billing/portal`, and
  `POST /v1/webhooks/paddle`** return **HTTP 501** with the stable envelope:

  ```json
  { "error": { "code": "self_hosted", "message": "Billing is disabled on self-hosted instances." } }
  ```
- **`GET /v1/instance`** reports `mode: self_host` and `features.billing: false`,
  which tells desktop/mobile clients to hide the billing UI entirely.

---

## The `/v1/instance` discovery contract

`GET /v1/instance` is **unauthenticated** (registered outside the auth
middleware) so a client that only knows the server's base URL can self-configure
— discover the Better Auth base URL and feature flags before anyone logs in. Exact
shape:

```json
{
  "name": "Calendium",
  "mode": "self_host",
  "version": "0.1.0",
  "authBaseUrl": "https://mail.example.com/api/auth",
  "authProviders": ["email", "google", "apple"],
  "webUrl": "https://mail.example.com",
  "undoSendSeconds": 15,
  "features": {
    "billing": false,
    "google": true,
    "microsoft": false,
    "ai": true,
    "push": false,
    "email": true
  }
}
```

| Field | Source |
| --- | --- |
| `name` | `INSTANCE_NAME` (default `Calendium`). |
| `mode` | `self_host` when `SELF_HOSTED=true`, else `cloud`. |
| `version` | The running API build constant (currently `0.1.0`). |
| `authBaseUrl` | `${PUBLIC_WEB_URL\|\|APP_URL}/api/auth` — the Better Auth base clients build their auth client against. |
| `authProviders` | Sign-in methods: `["email"]`, plus `"google"` when `GOOGLE_CLIENT_ID` is set and `"apple"` when `APPLE_CLIENT_ID` is set. |
| `webUrl` | `PUBLIC_WEB_URL` — mobile and desktop build billing links from it. |
| `undoSendSeconds` | Undo-send grace window in seconds (`UNDO_SEND_SECONDS`, default `15`); clients show a post-send **Undo** toast for this long. |
| `vapidPublicKey` | Web Push VAPID public key (`VAPID_PUBLIC_KEY`), **present only when web push is configured** (the web client subscribes with it). Omitted otherwise. |
| `features.billing` | `!SELF_HOSTED` — `false` on self-host. |
| `features.google` | `true` when `GOOGLE_CLIENT_ID` is set. |
| `features.microsoft` | `true` when `MS_CLIENT_ID` is set. |
| `features.ai` | `true` when `OPENROUTER_API_KEY` is set. |
| `features.push` | `true` when APNs (`APNS_KEY_P8`), FCM (`FCM_SERVICE_ACCOUNT_JSON`), or Web Push (both VAPID keys) is configured. |
| `features.email` | `true` when `SMTP_HOST` is set (an SMTP sender is configured). The web forgot-password page shows the administrator instructions only when this is explicitly `false`. |

The TypeScript mirror (`InstanceInfo`, `InstanceMode`, `InstanceFeatures`) and
the `fetchInstance(baseUrl)` / `ApiClient.getInstance()` helpers live in
`@calendium/shared` — the clients call this endpoint automatically on the connect
screen (see [clients](./clients.md)).

---

Next: **[Pointing the apps at your server →](./clients.md)**
