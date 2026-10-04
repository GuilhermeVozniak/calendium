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
            Postgres ────┘     │     │     └──── Paddle (billing)
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
├─ cmd/worker/main.go         # background loops: provider sync, scheduled send, snooze/reminder wakeups, push dispatch, ai_jobs drain
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
│    ├─ out/paddle/           # Paddle Billing REST client + Paddle-Signature HMAC verification (stdlib)
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
| `GET /v1/instance` | Public instance discovery (unauthenticated): `{name, mode: self_host\|cloud, version, authBaseUrl, authProviders, webUrl, features, capabilities}` for client self-configuration (`capabilities` = `{todoist, hubspot, maps, weather}` — only vendors whose config is present are advertised) |
| `GET /v1/me` | Current user (upserts on first call) |
| `GET /v1/billing/subscription` | Subscription status ($50/yr annual plan); grants the 14-day signup trial on first call; `402 {details:{reason}}` elsewhere when lapsed |
| `POST /v1/billing/checkout` | Create a Paddle overlay checkout (no body) `→ {url}` (409 `already_subscribed`, 502 `billing_unavailable`, 501 `self_hosted` when `SELF_HOSTED`) |
| `POST /v1/billing/portal` | Paddle customer-portal links (no body) `→ {overviewUrl, cancelUrl, updatePaymentUrl}` (400 `no_billing_profile`, 501 `self_hosted`) |
| `POST /v1/webhooks/paddle` | Paddle webhook (Paddle-Signature verified, unauthenticated, 1 MB cap) |
| `GET /v1/accounts` | List connected Google/Microsoft accounts |
| `POST /v1/accounts/connect/{provider}` | Begin provider OAuth `{redirectUrl} → {url}` |
| `GET /v1/accounts/callback/{provider}` | OAuth redirect target (state-validated) |
| `DELETE /v1/accounts/{id}` | Disconnect account |
| `GET /v1/integrations` | List per-user vendor integrations (Todoist/HubSpot) |
| `POST /v1/integrations/connect/{vendor}` | Begin vendor OAuth `{redirectUrl} → {url}` (501 when the vendor is unconfigured) |
| `GET /v1/integrations/callback/{vendor}` | Vendor OAuth redirect target (state-validated, unauthenticated) |
| `DELETE /v1/integrations/{id}` | Disconnect integration (Todoist also purges mirrored tasks) |
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
| `POST /v1/mail/threads/bulk-actions` | Bulk archive/read/label with per-id `failedIds` reporting (cap 200) |
| `POST /v1/mail/threads/zero` | Get Me To Zero: archive all inbox mail older than `olderThan` |
| `GET /v1/mail/labels` / `POST /v1/mail/threads/{id}/labels` | Label listing and single-thread label add/remove |
| `POST /v1/mail/threads/{id}/unsubscribe` | Execute unsubscribe: `{method: one_click\|mailto\|link, url?}` (RFC 8058 one-click POST) |
| `DELETE /v1/mail/threads/{id}/snooze` | Manual unsnooze (client-side undo; does not mark unread) |
| `GET/PUT /v1/prefs` | User preferences: `{splitOrder}` (reorderable splits) |
| `GET/POST /v1/event-templates` / `PUT/DELETE /v1/event-templates/{id}` | Event template CRUD (full-replace PUT) |
| `POST /v1/event-templates/{id}/use` | Bump a template's usage counter (204) |
| `GET/POST /v1/calendar-sets` / `PUT/DELETE /v1/calendar-sets/{id}` | Calendar set CRUD (named visibility groups, full-replace PUT) |
| `GET /v1/search?q` | Unified search over threads + events |
| `POST /v1/ai/compose` | OpenRouter-backed compose/reply/summarize plus `improve\|shorten\|simplify\|fix_grammar\|change_tone` editing actions |
| `POST /v1/ai/ask` | Cited-source Q&A over inbox/calendar (budget-gated) |
| `POST /v1/ai/event-proposal` | AI reads a thread and proposes a ready-to-send event (title/attendees/time) |
| `GET /v1/mail/threads/{id}/instant-replies` | Up to 3 precomputed reply drafts, cached on the thread |
| `GET/POST /v1/classifiers` / `PATCH/DELETE /v1/classifiers/{id}` | User-defined natural-language mail classifiers (Auto Labels) |
| `POST /v1/devices` / `DELETE /v1/devices/{id}` | Push token registration |
| `GET /healthz` | Liveness (unauthenticated) |

## Sync model

- `cmd/worker` polls Gmail (`historyId` incremental sync) and Microsoft Graph (delta queries) per connected account; Google push channels / Graph subscriptions can be layered on later, the port (`MailProvider`, `CalendarProvider`) already models both.
- Threads, messages (headers + bodies), labels, calendars, and events are mirrored into Postgres so list/search endpoints are served locally (Superhuman-grade latency); mutations write through to the provider and update the mirror optimistically.
- Split-inbox classification (`important|vip|team|calendar|news|social|other`) runs at ingest in `service/mailsync` using header heuristics (list-unsubscribe, sender domain, calendar invites), with an optional AI pass via OpenRouter.

## Push notifications

`port.PushSender` fans out to APNs (iOS/macOS), FCM (Android), and Web Push (VAPID) adapters, all stdlib-implemented. Triggers: new important mail, event reminders, snooze/reminder wake-ups.

## AI suite

- `port.AI` (`internal/port/driven.go`) is the OpenRouter chat-completions surface: `Complete` returns freeform text; `CompleteJSON` requests a JSON-object response (OpenRouter `response_format: json_object`) and decodes it into a caller-supplied struct, wrapping decode failures in `domain.ErrAIOutput`-wrapped errors. `internal/adapter/out/openrouter/client.go` implements both against the OpenRouter chat-completions API.
- Background AI work is queued in Postgres `ai_jobs` (`backend/migrations/0007_ai_suite.sql`) and drained by the worker's ai-jobs loop, gated entirely on `OPENROUTER_API_KEY` being set — unset, the loop never starts, `GET /v1/instance` reports `features.ai: false`, and the interactive `/v1/ai/*` endpoints return `503`. Job kinds: `thread_summary`, `instant_replies`, `auto_draft`, `classify`, `reminder_detect`, `voice_profile` (30-day self-refresh of the user's writing-style profile).
- **Claim.** `AiJobRepo.ClaimDue` uses `SELECT ... FOR UPDATE SKIP LOCKED` to atomically claim up to N due jobs (`run_after <= now`, unlocked or lock expired after 10 minutes) — safe under concurrent workers. Thread-scoped kinds (`thread_summary`, `instant_replies`, `auto_draft`, `reminder_detect`) dedup to one pending job per `(kind, thread_id)` via a partial unique index, so a burst of inbound messages collapses into a single fresh job.
- **Retry/backoff** (`internal/service/ai_jobs.go`). Generic failures back off `1m → 4m → 16m` (capped at 30m) and dead-letter (row dropped, error logged) once `attempts` reaches 4. `domain.ErrRateLimited` and `domain.ErrAIUnavailable` (and anything wrapping them via `errors.Is`) instead rearm at a flat 15 minutes regardless of attempt count and never dead-letter, since provider throttling/outages are expected to recover. `domain.ErrNotFound` (e.g. the thread was deleted) drops the job silently.
- **Budget.** `AiUsageRepo.IncrementAndCheck` atomically bumps a per-user, per-UTC-day counter and refuses the call once it would exceed `AI_DAILY_LIMIT` (default 300); interactive endpoints and background jobs share the same budget, and it rearms at the next UTC midnight.
- **Interactive endpoints** (all budget-gated): `POST /v1/ai/compose` (compose/reply/summarize plus editing actions), `POST /v1/ai/ask` (cited-source Q&A), `POST /v1/ai/event-proposal`, `GET /v1/mail/threads/{id}/instant-replies`, and `/v1/classifiers` CRUD.

## Worker loops

`cmd/worker` runs up to three independent loops against the same composition root: **sync** (1 minute) polls every syncable account for incremental mail + calendar changes; **due-work** (5 seconds) processes scheduled sends, undo-send holds, and snooze/reminder wake-ups — kept short because it bounds how late an undo-send delivery can fire; **ai-jobs** (15 seconds) drains the `ai_jobs` queue and starts only when `OPENROUTER_API_KEY` is configured.

## Environment

Backend env vars (see `backend/.env.example`): `DATABASE_URL` (shared with the web app, which hosts Better Auth against the same Postgres), `SELF_HOSTED` (open-core: `true` unlocks all features and disables Paddle billing; default `false`), `INSTANCE_NAME` (shown to clients, default `Calendium`), `APP_URL`/`PUBLIC_WEB_URL` (public web origin), `BETTER_AUTH_URL` (public web origin hosting Better Auth, no trailing slash), `AUTH_JWKS_URL` (Better Auth JWKS endpoint; default `${BETTER_AUTH_URL}/api/auth/jwks`), `AUTH_ISSUER` (expected JWT `iss`; default `${BETTER_AUTH_URL}`), `OAUTH_ALLOWED_REDIRECT_URIS` (redirect allowlist, added to localhost + `calendium://` defaults), `GOOGLE_CLIENT_ID/SECRET` (shared between login + mailbox connect), `APPLE_CLIENT_ID/SECRET` (Apple login), `MS_CLIENT_ID/SECRET`, `PADDLE_ENV`, `PADDLE_API_KEY`, `PADDLE_WEBHOOK_SECRET`, `PADDLE_PRICE_ID_ANNUAL`, `BILLING_RECONCILE_INTERVAL`, `OPENROUTER_API_KEY`, `APNS_KEY_ID/TEAM_ID/KEY_P8`, `FCM_SERVICE_ACCOUNT_JSON`, `VAPID_PUBLIC/PRIVATE_KEY`, `TOKEN_ENCRYPTION_KEY` (32-byte hex for AES-GCM).

The Next.js web app additionally reads `BETTER_AUTH_SECRET` (`openssl rand -base64 32`), `BETTER_AUTH_URL`, `DATABASE_URL`, and `GOOGLE_*`/`APPLE_*` at **runtime** (not `NEXT_PUBLIC_*`) to run Better Auth. It talks to Better Auth **same-origin** (`/api/auth`), so it needs no `NEXT_PUBLIC` auth var; `NEXT_PUBLIC_API_URL` (default same-origin) still points clients at the Go API. All `SUPABASE_*` / `NEXT_PUBLIC_SUPABASE_*` vars are removed.

### Deployment modes (cloud vs self-hosted)

Calendium is **open core**: the same binaries run in two modes, selected by the backend
`SELF_HOSTED` env flag (default `false`).

- **Cloud (`SELF_HOSTED=false`)** — our managed hosting; Paddle billing is live (the api and worker refuse to boot without the Paddle keys) and the
  paywall gates on subscription state (see [payments.md](./payments.md)).
- **Self-hosted (`SELF_HOSTED=true`)** — the user runs the whole stack; entitlement gating
  becomes a no-op (all features unlocked) and Paddle is switched off. `GET /v1/billing/subscription`
  reports a synthetic active annual plan (nil period) so clients treat the user as fully
  entitled, and the checkout/portal/webhook endpoints return `501 self_hosted`.

The business/entitlement rationale lives in [pricing-model.md](./pricing-model.md); the
operator guide (Docker Compose, HTTPS, providers, upgrades) lives in
[self-hosting/README.md](./self-hosting/README.md).

**Instance discovery.** `GET /v1/instance` is unauthenticated so a client that only knows
the server base URL can self-configure. It returns `{ name, mode, version, authBaseUrl,
authProviders, webUrl, undoSendSeconds, vapidPublicKey?, features: { billing, google, microsoft, ai, push } }`
where `mode` is
`self_host` when `SELF_HOSTED=true` else `cloud`, `authBaseUrl` is `${PUBLIC_WEB_URL||APP_URL}/api/auth`
(with `PUBLIC_WEB_URL` winning over `APP_URL` when both are set)
(where Better Auth is hosted), `authProviders` lists enabled sign-in methods (`["email"]`, plus
`"google"`/`"apple"` when their credentials are configured), `undoSendSeconds` is the
undo-send grace window (`UNDO_SEND_SECONDS`, default 15), `vapidPublicKey` is present only
when web push is configured, `features.billing = !SELF_HOSTED`, and `webUrl` is `PUBLIC_WEB_URL` (desktop builds its billing link `<webUrl>/settings?tab=billing` from it; the mobile apps show no billing links);
the remaining feature flags reflect which gateways/credentials are configured. Clients build
their Better Auth client against `authBaseUrl` and read `features.billing` to decide whether to
show any billing/paywall UI at all.
