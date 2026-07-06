# Calendium Architecture

Calendium is an email + calendar manager (Superhuman-class email UX, deeply integrated calendar) shipping on web, desktop, and mobile, backed by a single Go API.

```
┌── apps/web (Next.js) ──┐  ┌─ apps/desktop (Wails) ─┐  ┌─ apps/mobile (Expo RN) ─┐
│ landing + web app     │  │ React/Vite frontend    │  │ iOS / Android          │
└──────────┬───────────┘  └──────────┬────────────┘  └───────────┬────────────┘
           │                        │                          │
           └─────── @calendium/shared (types + ApiClient) ───┘
                                    │  HTTPS + Better Auth JWT (EdDSA)
                        ┌──────────▼──────────┐
                        │   backend/ (Go API)   │  hexagonal, stdlib-only
                        └─┬─────┬─────┬─────┬─┘
            Postgres ────┘     │     │     └──── Stripe (billing)
            Google APIs ───────┘     └── OpenRouter (AI), APNs/FCM/WebPush
            Microsoft Graph
```

## Auth model

- **Better Auth** (npm `better-auth`) is the identity provider for web, desktop, and mobile. It is **hosted by the Next.js web app** at `${BETTER_AUTH_URL}/api/auth/*` (a catch-all route) and backed by the **same Postgres** as the Go API, in its own tables (`user`, `session`, `account`, `verification`, `jwks`). That schema is committed as `backend/migrations/0002_better_auth.sql` and applied by the Go migrate runner at boot — there is **no separate JS migration step** at deploy. Auth methods: email + password and social **Google / Apple**. This is a win for self-hosting: no external auth service to run, auth is built into the web app.
- The `jwt()` plugin mints short-lived (default 15m) **asymmetric EdDSA (Ed25519) JWTs** — payload `sub=userId`, `iss=BETTER_AUTH_URL`, plus `email` / `name` / `image` — and publishes the public keys as JWKS at `/api/auth/jwks`. Native clients (desktop, mobile) use the `bearer()` plugin; mobile also uses the `@better-auth/expo` server plugin. Every client fetches a fresh token from `GET ${BETTER_AUTH_URL}/api/auth/token` (minted per request, not cached) and sends it as `Authorization: Bearer <jwt>`.
- The Go backend is a **pure resource server** (`adapter/out/authjwt`): it fetches JWKS from `AUTH_JWKS_URL`, verifies the EdDSA (Ed25519) signature with stdlib `crypto/ed25519` (RS256/ES256 still supported), pins `iss == AUTH_ISSUER`, requires `sub` + `exp`, and upserts a `users` row keyed by the `sub` claim (mapping `email` / `name` / `image`) on first request. No Supabase, no shared HS256 secret for API auth.
- Connecting mail/calendar providers is separate from login: the backend runs its own **Google / Microsoft OAuth flows** (offline access) and stores refresh tokens encrypted (AES-GCM) in Postgres. The Google **login** credentials and the mailbox-**connect** credentials share `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET` (both redirect URIs registered on one OAuth client).

## Backend — hexagonal, no framework

Go stdlib only: `net/http` (1.22 pattern routing), `database/sql`, `crypto/*`, `encoding/json`. The single allowed external dependency is the Postgres driver (`jackc/pgx/v5/stdlib`, used strictly as a `database/sql` driver).

```
backend/
├─ cmd/api/main.go            # HTTP API entrypoint (composition root: wire adapters → services)
├─ cmd/worker/main.go         # background loops: provider sync, scheduled send, snooze/reminder wakeups, push dispatch
├─ internal/domain/           # pure entities + invariants; zero imports outside stdlib
│    user.go subscription.go account.go mail.go calendar.go notification.go errors.go
├─ internal/port/             # interfaces only — the hexagon's edges
│    driving.go               # use-case interfaces consumed by adapters/in (MailService, CalendarService, ...)
│    driven.go                # repos + gateways implemented by adapters/out (ThreadRepo, MailProvider, Payments, Push, AI, ...)
├─ internal/service/          # use-case implementations (business logic; depends on domain + port only)
├─ internal/adapter/
│    ├─ in/httpapi/           # net/http handlers, router, auth middleware, JSON codecs
│    ├─ out/postgres/         # database/sql repositories
│    ├─ out/googleapi/        # Gmail + Google Calendar REST (raw net/http)
│    ├─ out/msgraph/          # Microsoft Graph mail + calendar REST
│    ├─ out/stripeapi/        # Stripe REST client + webhook HMAC verification (stdlib)
│    ├─ out/push/             # APNs (HTTP/2 + ES256 JWT), FCM (OAuth2 SA JWT), Web Push (VAPID)
│    ├─ out/openrouter/       # chat-completions client
│    └─ out/authjwt/          # Better Auth JWT verification (EdDSA/Ed25519 + RS256/ES256 via JWKS)
├─ internal/config/           # env-based config
└─ migrations/                # plain SQL, applied by cmd/api at boot (embedded via embed.FS)
```

Dependency rule: `domain ← port ← service` and `adapter → port` only. Nothing in `domain`, `port`, or `service` may import an adapter.

## REST API contract (v1)

All endpoints JSON, Bearer-authenticated unless noted. Errors: `{ "error": { "code", "message" } }`. Lists paginate with `{ items, nextCursor }`. The TypeScript mirror lives in `packages/shared`.

| Method & path | Purpose |
| --- | --- |
| `GET /v1/instance` | Public instance discovery (unauthenticated): `{name, mode: self_host\|cloud, version, authBaseUrl, authProviders, features}` for client self-configuration |
| `GET /v1/me` | Current user (upserts on first call) |
| `GET /v1/billing/subscription` | Subscription status ($50/yr annual plan) |
| `POST /v1/billing/checkout` | Create Stripe Checkout session `{successUrl, cancelUrl} → {url}` (501 `self_hosted` when `SELF_HOSTED`) |
| `POST /v1/billing/portal` | Stripe billing portal `{returnUrl} → {url}` (501 `self_hosted` when `SELF_HOSTED`) |
| `POST /v1/webhooks/stripe` | Stripe webhook (signature-verified, unauthenticated) |
| `GET /v1/accounts` | List connected Google/Microsoft accounts |
| `POST /v1/accounts/connect/{provider}` | Begin provider OAuth `{redirectUrl} → {url}` |
| `GET /v1/accounts/callback/{provider}` | OAuth redirect target (state-validated) |
| `DELETE /v1/accounts/{id}` | Disconnect account |
| `GET /v1/mail/threads?split&labelId&q&cursor&limit` | Inbox lists (split inboxes) |
| `GET /v1/mail/threads/{id}` | Thread + messages |
| `POST /v1/mail/threads/{id}/actions` | `{action}` archive/trash/star/read/… |
| `POST /v1/mail/threads/{id}/snooze` | `{until}` |
| `POST /v1/mail/threads/{id}/reminder` | `{remindAt}` follow-up reminder |
| `POST /v1/mail/drafts` / `PUT /v1/mail/drafts/{id}` | Create / update draft (autosave) |
| `POST /v1/mail/drafts/{id}/send` | Send now or at `scheduledAt` (Send Later) |
| `GET/POST /v1/mail/snippets` / `PUT/DELETE /v1/mail/snippets/{id}` | Snippets CRUD |
| `GET /v1/calendars` / `PATCH /v1/calendars/{id}` | Calendars, visibility/color |
| `GET /v1/events?from&to&calendarIds` | Events in range (recurrences expanded) |
| `POST /v1/events` / `PATCH /v1/events/{id}` / `DELETE` | Event CRUD (writes through to provider; PATCH body is `EventPatch` — no `calendarId`/`addConferencing`) |
| `POST /v1/events/{id}/rsvp` | `{response}` |
| `GET /v1/availability?from&to&duration` | Free slots for share-availability |
| `GET /v1/search?q` | Unified search over threads + events |
| `POST /v1/ai/compose` | OpenRouter-backed compose/reply/summarize/ask |
| `POST /v1/devices` / `DELETE /v1/devices/{id}` | Push token registration |
| `GET /healthz` | Liveness (unauthenticated) |

## Sync model

- `cmd/worker` polls Gmail (`historyId` incremental sync) and Microsoft Graph (delta queries) per connected account; Google push channels / Graph subscriptions can be layered on later, the port (`MailProvider`, `CalendarProvider`) already models both.
- Threads, messages (headers + bodies), labels, calendars, and events are mirrored into Postgres so list/search endpoints are served locally (Superhuman-grade latency); mutations write through to the provider and update the mirror optimistically.
- Split-inbox classification (`important|vip|team|calendar|news|social|other`) runs at ingest in `service/mailsync` using header heuristics (list-unsubscribe, sender domain, calendar invites), with an optional AI pass via OpenRouter.

## Push notifications

`port.PushSender` fans out to APNs (iOS/macOS), FCM (Android), and Web Push (VAPID) adapters, all stdlib-implemented. Triggers: new important mail, event reminders, snooze/reminder wake-ups.

## Environment

Backend env vars (see `backend/.env.example`): `DATABASE_URL` (shared with the web app, which hosts Better Auth against the same Postgres), `SELF_HOSTED` (open-core: `true` unlocks all features and disables Stripe billing; default `false`), `INSTANCE_NAME` (shown to clients, default `Calendium`), `APP_URL`/`PUBLIC_WEB_URL` (public web origin), `BETTER_AUTH_URL` (public web origin hosting Better Auth, no trailing slash), `AUTH_JWKS_URL` (Better Auth JWKS endpoint; default `${BETTER_AUTH_URL}/api/auth/jwks`), `AUTH_ISSUER` (expected JWT `iss`; default `${BETTER_AUTH_URL}`), `OAUTH_ALLOWED_REDIRECT_URIS` (redirect allowlist, added to localhost + `calendium://` defaults), `GOOGLE_CLIENT_ID/SECRET` (shared between login + mailbox connect), `APPLE_CLIENT_ID/SECRET` (Apple login), `MS_CLIENT_ID/SECRET`, `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`, `STRIPE_PRICE_ID_ANNUAL`, `OPENROUTER_API_KEY`, `APNS_KEY_ID/TEAM_ID/KEY_P8`, `FCM_SERVICE_ACCOUNT_JSON`, `VAPID_PUBLIC/PRIVATE_KEY`, `TOKEN_ENCRYPTION_KEY` (32-byte hex for AES-GCM).

The Next.js web app additionally reads `BETTER_AUTH_SECRET` (`openssl rand -base64 32`), `BETTER_AUTH_URL`, `DATABASE_URL`, and `GOOGLE_*`/`APPLE_*` at **runtime** (not `NEXT_PUBLIC_*`) to run Better Auth. It talks to Better Auth **same-origin** (`/api/auth`), so it needs no `NEXT_PUBLIC` auth var; `NEXT_PUBLIC_API_URL` (default same-origin) still points clients at the Go API. All `SUPABASE_*` / `NEXT_PUBLIC_SUPABASE_*` vars are removed.

### Deployment modes (cloud vs self-hosted)

Calendium is **open core**: the same binaries run in two modes, selected by the backend
`SELF_HOSTED` env flag (default `false`).

- **Cloud (`SELF_HOSTED=false`)** — our managed hosting; Stripe billing is live and the
  paywall gates on subscription state (see [payments.md](./payments.md)).
- **Self-hosted (`SELF_HOSTED=true`)** — the user runs the whole stack; entitlement gating
  becomes a no-op (all features unlocked) and Stripe is switched off. `GET /v1/billing/subscription`
  reports a synthetic active annual plan (nil period) so clients treat the user as fully
  entitled, and the checkout/portal/webhook endpoints return `501 self_hosted`.

The business/entitlement rationale lives in [pricing-model.md](./pricing-model.md); the
operator guide (Docker Compose, HTTPS, providers, upgrades) lives in
[self-hosting/README.md](./self-hosting/README.md).

**Instance discovery.** `GET /v1/instance` is unauthenticated so a client that only knows
the server base URL can self-configure. It returns `{ name, mode, version, authBaseUrl,
authProviders, features: { billing, google, microsoft, ai, push } }` where `mode` is
`self_host` when `SELF_HOSTED=true` else `cloud`, `authBaseUrl` is `${PUBLIC_WEB_URL||APP_URL}/api/auth`
(where Better Auth is hosted), `authProviders` lists enabled sign-in methods (`["email"]`, plus
`"google"`/`"apple"` when their credentials are configured), `features.billing = !SELF_HOSTED`,
and the remaining feature flags reflect which gateways/credentials are configured. Clients build
their Better Auth client against `authBaseUrl` and read `features.billing` to decide whether to
show any billing/paywall UI at all.
