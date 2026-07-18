# Calendium — Repository State & Gap Analysis

> Snapshot corrected Jul 17 2026 after a full code audit (backend + all three clients) and a green run of the entire test suite. The previous revision of this document claimed M1 was "~entirely unstarted"; the code contradicts that on every item, so this revision records the verified state.

## Goal

Build **Calendium**: a Superhuman-class, keyboard-first **email client** deeply integrated with a **best-in-class calendar** (borrowing from Fantastical, Vimcal, Amie, Rise, Notion Calendar). Ships on **web, desktop, and mobile** from one Go backend, one `@calendium/shared` contract, and one shadcn-based design system. Open-core (AGPLv3 self-host free; $50/yr managed Cloud).

## Current State — M0 + M1 complete in code

**Test baseline (verified Jul 17 2026):** backend Go suite (incl. testcontainers Postgres), 200 web, 102 mobile, 97 desktop, and shared-client tests all green; Biome + golangci-lint gates wired via lefthook pre-push and CI.

### M1 — Live Provider Sync: IMPLEMENTED (all 9 items, verified with file-level evidence)

1. **OAuth connect flows** — `service/account.go` BeginConnect/CompleteConnect with PKCE (S256), one-time state (10-min TTL, atomic consume), AES-256-GCM token storage (`adapter/out/postgres/crypto.go`), HTTP routes + client 302 return (`adapter/in/httpapi/accounts.go`).
2. **Worker incremental sync** — `cmd/worker` runs a 1-min sync loop + 5-s due-work loop; `service/sync.go` chains mail → calendars → events with cursor persistence (`SyncStateRepo`) and automatic token refresh (reauth_required on failure).
3. **Real provider adapters** — Gmail historyId full+incremental sync, RFC-2822 send, thread label modify (`adapter/out/googleapi/mail.go`); Microsoft Graph delta queries, draft-create+send, folder/category mapping (`adapter/out/msgraph/mail.go`). Real endpoints throughout; zero stubs.
4. **Split-inbox classification at ingest** — `service/classify.go` (VIP → Calendar → News → Social → Team → Important → Other) invoked from the sync path with real headers.
5. **Send pipeline** — SendDraft schedules via undo-send grace (UNDO_SEND_SECONDS); UnsendDraft races atomically via ClaimScheduled; worker delivers due drafts with failure recording.
6. **Read-status tracking** — MarkThreadOpened sets opened_at locally and writes read state through to the provider.
7. **Calendar CRUD/RSVP write-through** — Create/Update/Delete/RSVP hit Google Calendar v3 / Graph and mirror locally; availability computed from visible calendars.
8. **Local instant search** — tsvector + trigram GIN indexes (migration 0001), dual-path query (FTS + ILIKE) for threads and events.
9. **Push notifications** — APNs (ES256/HTTP2), FCM v1 (service-account), Web Push (VAPID + aes128gcm) dispatchers, triggered on important/VIP mail arrival, snooze wake-ups, and reminders.
10. **Billing enforcement** — entitlement gate on every paid service call (trial/active/past-due grace), webhook idempotency + event ordering, self-host bypass.

**The backend has no mock mode at all** — fakes exist only in `_test.go` files.

### M2.1 — Triage Power (COMPLETE)

Shipped nine triage-power features (Get Me To Zero, Bulk triage actions, One-click and bulk unsubscribe, Inbox zero celebration design, Auto-advance, Reorderable splits, Stars and labels via shortcuts, Undo anything, Shortcut teaching UX) backed by the real REST v1 endpoints: `POST /v1/mail/threads/bulk-actions`, `POST /v1/mail/threads/zero`, `GET /v1/mail/labels`, `POST /v1/mail/threads/{id}/labels`, `POST /v1/mail/threads/{id}/unsubscribe`, `DELETE /v1/mail/threads/{id}/snooze`, and `GET`/`PUT /v1/prefs`. The Playwright p95 perf budget (measured 36-46ms) is web-only, not a cross-platform desktop/mobile budget.

### Clients — wired to the live API

All three clients call the Go API through `@calendium/shared` `ApiClient` (40+ typed methods covering the full REST v1 surface). Mock data is served **only** behind an explicit demo flag (`NEXT_PUBLIC_DEMO_MODE`, desktop/mobile "Try the demo"); outside demo mode errors surface honestly.

- **Web (Next.js 15):** all screens live (mail, calendar, compose, settings, billing, web push); instance feature detection gates AI/billing/push/social-signin; ⌘K palette shows live unified search results (threads + events, added Jul 17).
- **Desktop (Wails v2):** server discovery via `/v1/instance`, bearer-token auth with `set-auth-token` capture, deep-link OTT sign-in (`calendium://auth`) and mailbox-connect return (`calendium://accounts`), live ⌘K search.
- **Mobile (Expo RN):** split-inbox chips (all 7 splits), infinite scroll, pull-to-refresh, swipe-to-triage (archive/snooze, added Jul 17) plus long-press action bar, full-thread reader with inline reply, compose with AI assist, server discovery, secure-store auth.

## Go-live checklist (operator inputs — no code remaining)

Configuration reference: `docs/self-hosting/configuration.md`. To take a real instance live:

1. **Required:** `DATABASE_URL` (Postgres), `TOKEN_ENCRYPTION_KEY` (32-byte hex), `BETTER_AUTH_URL` + `PUBLIC_WEB_URL`.
2. **Google:** OAuth client in Google Cloud Console (Gmail + Calendar scopes) → `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET`; redirect URI `<PUBLIC_API_URL>/v1/accounts/callback/google`.
3. **Microsoft:** Azure app registration (Mail.ReadWrite, Mail.Send, Calendars.ReadWrite, offline_access) → `MS_CLIENT_ID` / `MS_CLIENT_SECRET`.
4. **Billing (Cloud mode only):** `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`, `STRIPE_PRICE_ID_ANNUAL`.
5. **Push (optional):** `APNS_KEY_ID`/`APNS_TEAM_ID`/`APNS_KEY_P8`, `FCM_SERVICE_ACCOUNT_JSON`, `VAPID_PUBLIC_KEY`/`VAPID_PRIVATE_KEY`.
6. **AI (optional):** `OPENROUTER_API_KEY` (+ `OPENROUTER_MODEL`).
7. Run `cmd/api` and `cmd/worker` (both apply migrations), connect a real mailbox from Settings, and watch the first incremental sync land in Postgres.

## M2.1 — Triage Power (COMPLETE)

Shipped nine triage-power features (Get Me To Zero, Bulk triage actions, One-click and bulk unsubscribe, Inbox zero celebration design, Auto-advance, Reorderable splits, Stars and labels via shortcuts, Undo anything, Shortcut teaching UX) backed by the real REST v1 endpoints.

## M2.2 — Calendar Core (COMPLETE)

Shipped keyboard-first calendar with full view range (day/week/month/quarter/year/agenda via d/w/m/q/y/a keys), natural-language event parsing in quick-add bar (parseQuickAdd; live preview chips for location/time/attendees/recurrence/alerts), event templates (CRUD endpoints: `/v1/event-templates`, create/list/update/delete/use), calendar sets (CRUD endpoints: `/v1/calendar-sets`, toggle visibility per set), multi-timezone pinning on time-grid gutters, multi-account overlay with visible/hidden cross-account conflict detection, join buttons on conference events (Conferencing.provider + url detection), command palette integration (calendar commands via calendar-commands.ts bus), and calendar peek toggle from mail page (mod+shift+k). Backend migration 0006 for templates and sets tables. Keyboard shortcuts: t/j/k for navigation, d/w/m/q/y/a for view switching, c/s for create/availability, /  to focus quick-add. All e2e flows tested.

## What's Missing to Achieve the Goal — M2 Full Parity

**Email — M2.1 complete, M2.3+ planned:** Contact pane w/ social insights, Smart Send, Instant Intro, Recent Opens feed, Auto Bcc, emoji reactions, per-account signatures, attachment quick access.

**AI — all planned:** Auto Drafts, Instant Reply, Auto Labels + custom classifiers, personal voice learning, AI editing commands, Auto Reminders, AI scheduling drafts, Ask AI sidebar (cited sources), external AI-agent integrations, Instant Event AI.

**Calendar — M2.2 complete** (natural-language parsing, templates, sets, views, multi-TZ, keyboard-first, peek, conferencing join); **M2.3+ planned:** NLP command bar, booking pages/links, appointment schedules, meeting polls, Time Travel TZ comparison, conferencing auto-attach (already partial), tasks/todos on grid, docs on events, travel time, focus-time auto-decline + FocusGuard, AI scheduling engine, working hours/location, propose-new-time RSVP, OOO auto-decline, EA delegation, time analytics, weather, auto buffers.

**Collaboration — all planned:** shared conversations, team comments w/ @mentions, team read statuses, team snippets, Find-a-Time grids, team scheduling links, team availability.

**Speed/Platform — mostly planned:** offline mode, preloading, undo-anything, 100+ shortcut coverage, global desktop shortcuts, menu-bar calendar, auto-join, attachment previews, multi-account switching shortcuts, CRM integrations, concierge onboarding, named themes. A measured/enforced sub-100 ms search budget also belongs here (search is indexed but not benchmarked).

## Summary

**M0, M1, M2.1 (Triage Power), and M2.2 (Calendar Core) are complete in code and fully tested.** Going live requires only operator credentials (checklist above). The remaining product work is **M2 full parity** (M2.3+ features + AI suite + collaboration), which needs product prioritization before planning.
