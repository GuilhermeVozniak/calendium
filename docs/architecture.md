# Calendium Architecture

Calendium is an email + calendar manager (Superhuman-class email UX, deeply integrated calendar) shipping on web, desktop, and mobile, backed by a single Go API.

```
┌── apps/web (Next.js) ──┐  ┌─ apps/desktop (Wails) ─┐  ┌─ apps/mobile (Expo RN) ─┐
│ landing + web app     │  │ React/Vite frontend    │  │ iOS / Android          │
└──────────┬───────────┘  └──────────┬────────────┘  └───────────┬────────────┘
           │                        │                          │
           └─────── @calendium/shared (types + ApiClient) ───┘
                                    │  HTTPS + Supabase JWT
                        ┌──────────▼──────────┐
                        │   backend/ (Go API)   │  hexagonal, stdlib-only
                        └─┬─────┬─────┬─────┬─┘
            Postgres ────┘     │     │     └──── Stripe (billing)
            Google APIs ───────┘     └── OpenRouter (AI), APNs/FCM/WebPush
            Microsoft Graph
```

## Auth model

- **Supabase Auth** is the identity provider on every platform (Google / Apple OAuth, email). Clients hold a Supabase session and send the access token as `Authorization: Bearer <jwt>`.
- The backend **verifies the JWT locally** (stdlib crypto against the Supabase JWT secret / JWKS — no Supabase SDK) and upserts a `users` row keyed by the JWT `sub` claim on first request.
- Connecting mail/calendar providers is separate from login: the backend runs its own **Google / Microsoft OAuth flows** (offline access) and stores refresh tokens encrypted (AES-GCM) in Postgres.

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
│    └─ out/supabasejwt/      # JWT verification (HS256 + RS256/ES256 via JWKS)
├─ internal/config/           # env-based config
└─ migrations/                # plain SQL, applied by cmd/api at boot (embedded via embed.FS)
```

Dependency rule: `domain ← port ← service` and `adapter → port` only. Nothing in `domain`, `port`, or `service` may import an adapter.

## REST API contract (v1)

All endpoints JSON, Bearer-authenticated unless noted. Errors: `{ "error": { "code", "message" } }`. Lists paginate with `{ items, nextCursor }`. The TypeScript mirror lives in `packages/shared`.

| Method & path | Purpose |
| --- | --- |
| `GET /v1/instance` | Public instance discovery (unauthenticated): `{name, mode: self_host\|cloud, version, supabaseUrl, supabaseAnonKey, features}` for client self-configuration |
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

Backend env vars (see `backend/.env.example`): `DATABASE_URL`, `SELF_HOSTED` (open-core: `true` unlocks all features and disables Stripe billing; default `false`), `INSTANCE_NAME` (shown to clients, default `Calendium`), `APP_URL`/`PUBLIC_WEB_URL` (public web origin), `SUPABASE_JWT_SECRET`, `SUPABASE_JWKS_URL`, `SUPABASE_URL` (issuer pinned to `<URL>/auth/v1`), `SUPABASE_ANON_KEY` (PUBLIC anon key served via `GET /v1/instance`), `OAUTH_ALLOWED_REDIRECT_URIS` (redirect allowlist, added to localhost + `calendium://` defaults), `GOOGLE_CLIENT_ID/SECRET`, `MS_CLIENT_ID/SECRET`, `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`, `STRIPE_PRICE_ID_ANNUAL`, `OPENROUTER_API_KEY`, `APNS_KEY_ID/TEAM_ID/KEY_P8`, `FCM_SERVICE_ACCOUNT_JSON`, `VAPID_PUBLIC/PRIVATE_KEY`, `TOKEN_ENCRYPTION_KEY` (32-byte hex for AES-GCM).

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
the server base URL can self-configure. It returns `{ name, mode, version, supabaseUrl,
supabaseAnonKey, features: { billing, google, microsoft, ai, push } }` where `mode` is
`self_host` when `SELF_HOSTED=true` else `cloud`, `features.billing = !SELF_HOSTED`, and
the remaining feature flags reflect which gateways/credentials are configured. Clients read
`features.billing` to decide whether to show any billing/paywall UI at all.
