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

See also: [Quickstart](./quickstart.md) · [Clients](./clients.md) ·
[Overview](./README.md).

---

## Core & database

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `DATABASE_URL` | **Yes** | — | Postgres DSN for `api` + `worker`. In compose the host is the service name `db`: `postgres://calendium:<pw>@db:5432/calendium?sslmode=disable`. Use `sslmode=require` for managed Postgres. |
| `TOKEN_ENCRYPTION_KEY` | **Yes** | — | 32-byte AES-256-GCM key as **exactly 64 hex chars**. Generate with `make gen-secret` / `openssl rand -hex 32`. Encrypts provider refresh tokens at rest. Wrong length fails config validation at boot. **Back it up separately.** |
| `POSTGRES_USER` | No | `calendium` | Bundled `db` service user. |
| `POSTGRES_PASSWORD` | **Yes** | `calendium` | Bundled `db` password — **change it** (the example ships `change-me-please`). Must match the password in `DATABASE_URL`. |
| `POSTGRES_DB` | No | `calendium` | Bundled `db` database name. |
| `HTTP_ADDR` | No | `:8080` | Listen address for the API. Compose sets `:8080` on the `api` service; only relevant when running the binary directly. `PORT` is honored as an alternative. |
| `WEB_PORT` | No | `3000` | Host port mapped to the `web` container's `3000`. |
| `API_PORT` | No | `8080` | Host port mapped to the `api` container's `8080`. |

## Instance identity & self-host mode

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `SELF_HOSTED` | No | `false` | `true` puts the instance in self-host mode: entitlement gating becomes a no-op (**all features unlocked**), the billing endpoints return `501 self_hosted`, and `GET /v1/instance` reports `mode: self_host`. The root `.env.example` defaults it to `true`. |
| `INSTANCE_NAME` | No | `Calendium` | Display name shown to clients on the connect screen and via `GET /v1/instance`. |
| `APP_URL` | No | — | Public web origin, used to build absolute links (no trailing slash). |
| `PUBLIC_WEB_URL` | No | falls back to `APP_URL` | Public web origin advertised to clients. |
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
| `BETTER_AUTH_SECRET` | **Yes** (web) | — | Better Auth signing/encryption secret. Generate with `openssl rand -base64 32`. **Secret — never expose.** The web app won't start without it. |
| `BETTER_AUTH_URL` | **Yes** | — | Public web origin hosting Better Auth (your domain, **no trailing slash**), e.g. `https://mail.example.com`. Read by the web app, and by the Go API to derive JWKS/issuer. |
| `AUTH_JWKS_URL` | No | `${BETTER_AUTH_URL}/api/auth/jwks` | JWKS endpoint the backend fetches Better Auth's public keys from. Override only if served from a different origin. |
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

## Provider OAuth apps (mail + calendar) & social login

Register your **own** Google Cloud and Microsoft Entra OAuth apps — per
deployment, not shared with Cloud. The **Google** app does double duty: Better
Auth "Continue with Google" login **and** Gmail/Calendar mailbox-connect (register
both redirect URIs on the one client). See [providers](./providers.md) for
redirect-URI setup.

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `OAUTH_ALLOWED_REDIRECT_URIS` | No | — | Comma-separated extra allowed OAuth redirect targets, **appended** to the built-in `localhost` + `calendium://` defaults. Add your web origin here or provider connect flows are rejected. |
| `GOOGLE_CLIENT_ID` | No | — | Enables Google **login** (`authProviders` gains `google`) **and** Gmail + Google Calendar connect (`features.google`). Shared between both. |
| `GOOGLE_CLIENT_SECRET` | No | — | Google OAuth client secret. |
| `APPLE_CLIENT_ID` | No | — | Enables **Sign in with Apple** (`authProviders` gains `apple`). Apple Services ID. |
| `APPLE_CLIENT_SECRET` | No | — | Apple OAuth client secret. |
| `MS_CLIENT_ID` | No | — | Enables Microsoft Graph mail + calendar. Sets `features.microsoft` when present. |
| `MS_CLIENT_SECRET` | No | — | Microsoft OAuth client secret. |

## AI (OpenRouter)

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `OPENROUTER_API_KEY` | No | — | Enables `POST /v1/ai/compose` (compose/reply/summarize/ask). Sets `features.ai` when present. |
| `OPENROUTER_MODEL` | No | `openrouter/auto` | Model slug for AI requests. |

## Push notifications

`features.push` becomes `true` when **any** push channel is configured: APNs
(`APNS_KEY_P8`), FCM (`FCM_SERVICE_ACCOUNT_JSON`), or Web Push (both
`VAPID_PUBLIC_KEY` and `VAPID_PRIVATE_KEY`).

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `APNS_KEY_ID` | No | — | APNs key ID (iOS/macOS, HTTP/2 + ES256 JWT). |
| `APNS_TEAM_ID` | No | — | Apple developer team ID. |
| `APNS_KEY_P8` | No | — | PEM contents of the `.p8` key (newlines escaped as `\n`), **not** a file path. Presence enables the push feature flag. |
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
| `STRIPE_SECRET_KEY` | No (cloud) | — | Stripe secret key. Managed-cloud only. |
| `STRIPE_WEBHOOK_SECRET` | No (cloud) | — | Stripe webhook signing secret. |
| `STRIPE_PRICE_ID_ANNUAL` | No (cloud) | — | Price ID for the $50/yr annual plan (see [`../payments.md`](../payments.md)). |

---

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

Add `GOOGLE_*` / `APPLE_*` for social sign-in (the Google creds also connect
Gmail/Calendar), `MS_*` for Outlook, `OPENROUTER_API_KEY` for AI, and push keys as
needed — each unlocks its adapter without touching the rest.

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
  `POST /v1/webhooks/stripe`** return **HTTP 501** with the stable envelope:

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
  "features": {
    "billing": false,
    "google": true,
    "microsoft": false,
    "ai": true,
    "push": false
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
| `features.billing` | `!SELF_HOSTED` — `false` on self-host. |
| `features.google` | `true` when `GOOGLE_CLIENT_ID` is set. |
| `features.microsoft` | `true` when `MS_CLIENT_ID` is set. |
| `features.ai` | `true` when `OPENROUTER_API_KEY` is set. |
| `features.push` | `true` when APNs (`APNS_KEY_P8`), FCM (`FCM_SERVICE_ACCOUNT_JSON`), or Web Push (both VAPID keys) is configured. |

The TypeScript mirror (`InstanceInfo`, `InstanceMode`, `InstanceFeatures`) and
the `fetchInstance(baseUrl)` / `ApiClient.getInstance()` helpers live in
`@calendium/shared` — the clients call this endpoint automatically on the connect
screen (see [clients](./clients.md)).

---

Next: **[Pointing the apps at your server →](./clients.md)**
