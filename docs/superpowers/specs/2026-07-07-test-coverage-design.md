# Calendium — Full Test Coverage: Design Spec

**Date:** 2026-07-07
**Status:** Approved design → decomposed into 6 sub-projects, built phased unit-by-unit.
**Goal:** Comprehensive automated test coverage of *all functionality* across the Calendium monorepo — the full test pyramid (unit → integration → e2e) for every unit, backend through the three client apps.

---

## 1. Context

Calendium is a bun monorepo: a Go 1.26 hexagonal backend (stdlib `net/http`, only production dependency is pgx), a shared TS package (`ApiClient` + domain types), and three clients (Next.js web, Wails desktop, Expo mobile). See [[calendium-monorepo-plan]] for architecture.

**Starting state (2026-07-07):**
- **backend/** — 9 Go test files, ~10% line coverage. Test infra exists (`go test`).
- **packages/shared, apps/web, apps/desktop, apps/mobile** — zero tests, zero test tooling. Every runner is stood up from scratch.

## 2. Scope decisions (locked)

| Decision | Choice |
|----------|--------|
| Breadth | **Everything** — every unit, full pyramid incl. UI component tests + e2e on all three clients. |
| Delivery | **Phased, unit by unit** — each sub-project runs its own design → plan → implement → verify → commit cycle. |
| Postgres repos | **Real Postgres via Docker**, provisioned by **testcontainers-go** (test-scope dependency only). |
| E2E reach | **All three clients** — Playwright (web + desktop renderer), Maestro (mobile). |
| CI | **Yes** — GitHub Actions runs every suite on push/PR. |

**Non-goals:** snapshot tests of presentational UI (the ~20 shadcn primitives, marketing landing sections) beyond a smoke render; testing generated/vendored code; gaming a coverage number.

## 3. Cross-cutting standards (the umbrella)

### 3.1 One runner per runtime

| Unit | Framework | Rationale |
|------|-----------|-----------|
| backend/, desktop Go bridge | Go stdlib `testing` + `httptest` | Already in use; no new deps in production scope. |
| packages/shared, apps/web, apps/desktop/frontend | **Vitest** (jsdom) + @testing-library/react | Native to Vite (desktop); handles Next/React + pure TS uniformly — one model for 3 units. |
| apps/mobile | **jest-expo** + @testing-library/react-native | RN Metro/Babel transforms require it; Vitest RN support is immature. |
| web e2e | **Playwright** (app runs in demo mode) | No real backend/OAuth needed. |
| desktop e2e | **Playwright** vs the Vite-served renderer, Go bridge stubbed | Reuses Playwright. |
| mobile e2e | **Maestro** on a simulator | Simpler, less flaky than Detox. |

### 3.2 Testing conventions

- **Behavior-focused**, table-driven where natural (matching the existing `classify_test.go` / `ops_test.go` style).
- **Deterministic time** — inject a fake clock (`port.Clock` in Go; a controllable `now()` in TS). Never assert against wall-clock.
- **Network at the edge only** — provider adapters (Google, MS Graph, Stripe, OpenRouter, APNs/FCM) are tested against local `httptest` servers (Go) / MSW or injected `fetch` (TS) returning canned JSON. No real network in tests.
- **No UI snapshots** for presentational components; component tests assert behavior (validation, state transitions, event dispatch), not markup.
- **Ownership/authorization** semantics are explicitly tested (foreign rows → 404, never 403 — an existing invariant).

### 3.3 Established Go patterns to build on (do not reinvent)

From the existing backend tests:
- **Partial fakes** — embed the port interface in a stub struct, override only the methods exercised; unimplemented methods panic (nil embedded interface), which doubles as a "must not be reached" assertion. Example: `stubAccountRepo struct { port.AccountRepo; ... }`.
- **Table-driven `t.Run(tt.name, ...)`** subtests.
- **`httptest` + `New(Deps{...})`** for HTTP handlers.
- **`NewXService(deps)`** construction with an injectable `port.Clock`; `SystemClock{}` in prod, a fake in tests.

### 3.4 Coverage floors (diagnostic, not gamed)

| Layer | Floor |
|-------|-------|
| backend domain, service | ≥90% |
| backend adapters (logic) | ≥80% |
| shared ApiClient | ≥95% |
| frontend lib modules | ≥90% |
| frontend components | meaningful-path only (no % floor) |

### 3.5 Orchestration & CI

- Root `package.json` scripts: `test` (all), `test:api`, `test:shared`, `test:web`, `test:mobile`, `test:desktop`, `test:e2e`, `test:e2e:mobile`.
- Makefile target `test-db` for the local Postgres integration path (also usable outside CI).
- **`.github/workflows/test.yml`**: a Go job (with Docker available for testcontainers-go), a TS matrix job (shared/web/desktop Vitest + mobile jest-expo), and a separate/nightly e2e job (Playwright; Maestro on macOS runner). Coverage floors enforced per job.

## 4. Sub-project sequence

Built in this order; each is its own spec → plan → implement → verify → commit cycle. Sub-project 1 is designed in full below; 2–6 are sketched and detailed when reached.

1. Backend (Go)
2. Shared `ApiClient` (TS)
3. Web (Next.js)
4. Desktop (Wails)
5. Mobile (Expo)
6. E2E (all three)

---

## 5. Sub-project 1 — Backend (detailed)

Five layers, built in order. Adds one shared `service/fakes_test.go` providing in-memory fakes for **every driven port** + a `fakeClock`, so services stop re-stubbing ad hoc.

### 5.1 `domain/` — pure unit tests (~95%)
- Every `Parse*` enum validator: `ParseThreadView` (exists), `ParseRsvpStatus`, `ParseProvider`, `ParseAccountStatus`, `ParseInboxSplit`, `ParseThreadAction`, `ParseDevicePlatform`, `ParseAiAction`, `ParseSubscriptionStatus` — valid values round-trip, invalid → `ErrValidation`.
- `Subscription.HasAccess(now)` across boundaries: trialing, active, past_due within 7-day grace, past_due expired, canceled, none.
- `Page[T]` envelope behavior; any `EventInput`/`DraftInput` validation helpers.

### 5.2 `service/` — use-case tests (~90%)
Shared fakes for repos, gateways (OAuth/mail/calendar providers), `Payments`, `AI`, `PushSender`, `TxRunner`, plus `fakeClock`.
- **billing** — GetSubscription (paid: none/active/not-found→none); CreateCheckoutSession (14-day trial for first-timers only, EnsureCustomer path, validation); CreatePortalSession (no-profile error); HandleWebhook (idempotent Record, tx rollback, out-of-order `customer.subscription.*` drop, invoice/checkout field preservation, customer→user resolution); RequireActive (grace boundaries). *(self-host paths exist in `selfhost_test.go`.)*
- **account** — Disconnect, List, token refresh via `tokenSource` (fresh / expired→refresh / refresh-fail→`reauth_required`). *(BeginConnect/CompleteConnect/SetVipSenders exist in `ops_test.go`.)*
- **mail** — ListThreads filter/pagination forwarding; GetThread (ownership, message order); ActOnThread all 8 actions (→ provider ModifyLabels + repo); SnoozeThread/SetReminder (set+clear); Draft CRUD; SendDraft (undo-send grace scheduling, Send-Later). *(MarkThreadOpened, UnsendDraft, ListThreads-view exist.)*
- **calendar** — ListCalendars, UpdateCalendar, ListEvents (range); CreateEvent/UpdateEvent/DeleteEvent write-through to provider + repo; RSVP; **Availability** free-window computation (boundary-heavy: overlapping events, all-day, timezone, empty ranges).
- **search** — query validation, paywall, threads+events merge, limit 20.
- **ai** — Compose/reply/summarize/ask context assembly, paywall.
- **device** — Register (platform validation, idempotent upsert), Unregister (ownership).
- **sync** (largest) — SyncAccount (mail+calendar incremental, token refresh, cursor save); ProcessDueWork (claim+send scheduled drafts, wake snoozed, fire reminders → push).
- **service.go helpers** — newID/randomToken (format/uniqueness), truncate, firstNonEmpty, `tokenSource`, `ownedAccount/Thread/Draft` (404 ownership semantics). *(entitlement exists.)*

### 5.3 HTTP `adapter/in/httpapi` — handler tests (~85%)
`httptest` + `New(Deps{...})` with **fake driving-port services** and a fake `TokenVerifier`. Focus on HTTP concerns (services already covered in 5.2):
- `requireAuth`: valid / missing / malformed bearer, JWT-verify failure, user upsert on success.
- CORS allow-list, panic recovery (500), `decodeJSON` 10MB bound, `writeJSON`, `statusFor` error→status mapping, `safeMessage` (no internal leakage).
- Per-handler param parsing + status codes for all 55 routes (query params: cursor/limit/split/view/labelId/q, time ranges, path ids). *(instance exists.)*

### 5.4 `adapter/out/` — integration against local mocks
- **authjwt** — extend: RS256/ES256 verification, JWKS fetch + caching, issuer mismatch, expired, kid selection. *(EdDSA exists.)*
- **push** — apns (JWT gen + request), fcm (payload + request), webpush (exists) — via `httptest` mock endpoints.
- **stripeapi** — payments (EnsureCustomer/CreateCheckout/CreatePortal) via mock Stripe server; client. *(webhook exists.)*
- **openrouter** — Complete: request shape, response parse, error mapping (mock server).
- **googleapi / msgraph** — mail sync/send/label, calendar sync + event CRUD + RSVP against mock servers returning canned Gmail/Graph JSON. *(msgraph recurrence exists.)*
- **postgres/crypto.go** — AES-256-GCM encrypt/decrypt round-trip as a pure unit (no DB).

### 5.5 `postgres/` repos — real Postgres via testcontainers-go
A `postgres_test.go` harness starts a throwaway Postgres container, applies `migrations`, and exercises **every repo method**: CRUD for all 14 repos, thread-list pagination cursors + filters, atomic `ClaimScheduled`, encrypted-token save/get round-trip, `Search`, snooze/reminder due-queries, Stripe-event dedup (`Record` firstTime), OAuth-state atomic `Consume`, sync-state cursor persistence. Container reused across the package's tests for speed. `testcontainers-go` is a **test-scope** module dep only — the production binary's dependency set is unchanged.

### 5.6 `config/`, `migrate/`, `cmd/`
- `config.FromEnv` — parsing, defaults, validation errors (env-driven, table-based).
- `migrate.Apply` — covered by the 5.5 harness (applies real migrations).
- `cmd/api`, `cmd/worker` — low-ROI composition roots; light smoke at most, otherwise skipped.

### 5.7 Definition of done (backend)
`go test ./...` green; coverage floors met (§3.4); testcontainers path runs under `make test-db` and in CI; no wall-clock flakiness.

---

## 6. Sub-projects 2–6 (sketched)

### 6.1 Shared `ApiClient` (Vitest)
Injected `fetch`; all 40+ methods — path/verb correctness, auth-header injection via `getAccessToken`, query-string building (listThreads/listEvents), `ApiRequestError` status/code/message mapping, `fetchInstance` (no-auth), `unsendDraft` conflict revert. Public contract for both frontends.

### 6.2 Web (Vitest + Testing Library)
- **lib units**: `quick-add.ts` NL parser (the star — dozens of phrases), `mail-utils` (time formatting, participants line, snooze/reminder/send-later option generators), `shortcuts` (key/chord matching, editable-target guard), `auth.ts` CORS allow-list (`isAllowedOrigin`/`trustedOrigins`/localhost-dev), `web-push` (base64→Uint8Array, subscribe/unsubscribe), `theme-provider` (persistence, system detection).
- **component logic**: compose (EMAIL_RE, htmlToText, draft state), event-dialog (parse/format datetime, reminder labels), command-palette (command dispatch + queue).
- **route handler**: Better Auth `[...all]` origin/CORS behavior.
- Mocks: `mail-mock`/`calendar-mock`/`settings-mock` as fixtures; `getApiClient` stubbed.

### 6.3 Desktop (Vitest + Go)
- Frontend lib: `toast` store (add/dismiss/subscribe, timer via fake timers), `auth` (token storage, refresh, sign-in/up/verifyOtt, external store subscribers), `server-config` (normalize/discover/persist), `compose` bus, `wails` bridge browser fallback.
- Go: `app.go` deep-link buffering (buffered pre-startup, flushed on startup) + `handleURL` parsing.

### 6.4 Mobile (jest-expo + RNTL)
`format.ts` (relativeTime/formatDate/startOfWeek/etc.), `server-config` normalize/discover, `mock` fallback gating, `auth-client` token mint, `use-push-registration` hook (Expo APIs mocked), key screens render + interaction.

### 6.5 E2E
- **Web (Playwright, demo mode):** sign-in gate → inbox triage (archive/snooze/star) → compose + send → calendar create (incl. quick-add) → ⌘K command palette → settings.
- **Desktop (Playwright):** same core flows vs Vite renderer, Go bridge stubbed.
- **Mobile (Maestro):** happy-path — connect/demo → inbox → open thread → compose → calendar.

---

## 7. Risks & mitigations

- **RN/Expo test env friction** → isolate to jest-expo; keep mobile unit scope to pure lib + hooks; defer flaky screen tests if needed.
- **testcontainers-go needs Docker in CI** → GitHub Actions Ubuntu runners provide Docker; container reused per-package to bound runtime.
- **Mobile e2e flakiness/slowness** → Maestro over Detox; run on a dedicated/nightly job, not the PR blocker.
- **Provider mock drift** → canned fixtures live beside the adapter; update when the adapter changes.
- **Coverage-as-target temptation** → floors are gates, not goals; presentational UI explicitly exempt.

## 8. Definition of done (overall)

All six sub-projects merged; `bun run test` (+ `test:e2e`) green locally and in CI; coverage floors enforced per §3.4; every functional area in the map has behavior-level tests.
