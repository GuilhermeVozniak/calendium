# Account lifecycle and privacy alignment — design

Date: 2026-10-04. Status: approved in conversation, awaiting written review.
Program context: piece 3 of 6 in the production-readiness program. Depends on
piece 1 (`docs/superpowers/specs/2026-10-04-paddle-billing-design.md`) for
`port.Payments.CancelSubscription(ctx, subscriptionID, immediately bool)` and the
`billing_customer_id` / `billing_subscription_id` columns. Does NOT depend on
piece 2 (email): no confirmation mail, no reset flow, no provider-side token
revocation. Piece 2's "Change password" control gets a reserved slot in the new
Settings → Account section.

## Goal

Make the legal pages' account promises true: delete the account from every
client in one action, download a copy of the data, turn background AI off,
and have privacy/terms describe what the system does and whom it talks to.

## Findings being fixed

1. No account deletion. `backend/internal/adapter/in/httpapi/httpapi.go:136-138`
   registers only `GET /v1/me` and the preferences routes; no `DELETE`, no
   `/v1/internal`. `apps/web/lib/auth.ts:148-180` configures `betterAuth({...})`
   without `user.deleteUser`, so `/delete-user` is disabled. Yet
   `apps/web/app/(marketing)/privacy/page.tsx:94-97` promises "you can delete your
   account entirely" and `terms/page.tsx:91` "delete your account at any time".
   App Store guideline 5.1.1(v) requires in-app deletion;
   `apps/mobile/app/(tabs)/settings.tsx` and
   `apps/desktop/frontend/src/views/SettingsView.tsx:626-640` have no entry point.
2. No data export anywhere, while `privacy/page.tsx:110-113` lists "export".
3. Background AI misdescribed. `privacy/page.tsx:66-73` says AI "run[s] only when
   you invoke them … nothing runs in the background", but
   `backend/internal/service/sync.go:121-131` enqueues `voice_profile` on first
   sync, `sync.go:355-404` (`enqueueIngestAiJobs`) enqueues `thread_summary`,
   `instant_replies`, `auto_draft`, `classify` per new inbound thread, and
   `sync.go:628-637` enqueues `reminder_detect` after sends — all to OpenRouter.
4. Third-party list (`privacy/page.tsx:75-90`) omits Open-Meteo
   (`port.WeatherProvider`), Nominatim/OSRM (`port.MapsProvider`), Todoist and
   HubSpot (`integration_connections`, migration 0023), APNs/FCM (`devices`,
   `port.PushSender`) and Paddle.
5. Terms `terms/page.tsx:50-56`: "Billing runs through Stripe … Fees are
   non-refundable except where required by law" — wrong biller; Paddle as
   merchant of record applies its own refund policy.
6. Cascade gaps: `backend/migrations/0010_user_preferences.sql:4-8` has
   `user_id text PRIMARY KEY` with no FK (orphans); `0011_teams.sql:8` has
   `teams.created_by … ON DELETE RESTRICT` (delete refused for anyone who created
   a surviving team).

## Decisions

1. Deletion is Better Auth `deleteUser` (`user.deleteUser.enabled = true`) with a
   `beforeDelete` hook calling `DELETE /v1/internal/users/{id}` on the Go API,
   header `X-Internal-Secret` = `INTERNAL_API_SECRET` (64 hex chars, required in
   both modes, parsed like `TOKEN_ENCRYPTION_KEY` at
   `backend/internal/config/config.go:291-301`, compared with
   `subtle.ConstantTimeCompare`). The route is not JWT-authed and answers 404
   when no secret is configured. A failed purge makes the hook throw `APIError`;
   Better Auth aborts and the auth rows stay intact. Credential accounts send
   their password (`deleteUser({ password })`); social-only accounts need a
   session younger than `session.freshAge = 300` s, else the client re-runs
   social sign-in. No email confirmation (`sendDeleteAccountVerification` unset).
2. Go purge `UserLifecycleService.Purge(ctx, userID)`, ordered: (b) team check
   first — `409 owns_teams` (ids + names) when the user is the sole `owner` of a
   team with other members; (a) if a Paddle subscription is `active|past_due|paused`,
   or `trialing` with a `billing_subscription_id`, `CancelSubscription(id, true)`;
   a Paddle error aborts (retry later; the Paddle customer record is retained by
   the merchant of record); (c) revoke delegations both directions; (d) delete
   teams where the user is the only member, then the `users` row, relying on
   cascades (audit below, migration adds the missing ones); (e) provider token
   revocation is out of scope — tokens die with their rows; (f) one `slog`
   line with the user id only.
3. Export `GET /v1/me/export` (authed, not entitlement-gated, no act-as) streams
   `calendium-export-<YYYY-MM-DD>.zip` via `archive/zip` while repos are read
   page by page; one per user per hour claimed atomically in `user_exports`,
   else `409 export_throttled` + `Retry-After`; 10-minute budget. Web gets
   "Download my data"; mobile/desktop link to web settings.
4. Background AI: `UserSettings.aiBackground` (bool, default true); `sync.go`
   skips every enqueue when off; queued jobs complete. Privacy text rewritten,
   toggle named, third-party list completed, terms refund/deletion/export fixed.
5. Web Settings → Account: Change password (slot), Download my data, Danger zone
   → Delete account dialog (what is deleted, type your email, password for
   credential accounts) → sign out → `/goodbye`. Mobile: Settings → Account →
   Delete account (`authClient.deleteUser`, password prompt or social re-auth),
   "Download my data" link out. Desktop: same via its `better-auth/react`
   client; without `deleteUser`, open the web settings page in the browser.
6. Demo mode and e2e: buttons render; actions toast "Not available in demo".

## Non-goals

GDPR DSAR tooling; provider-side OAuth token revocation; soft-delete, grace
period or undo; admin UI; deleting Paddle customer records; deleting content
other people own (team threads the user commented on, teammates' snippets).

## Architecture

### Deletion sequence

```
client (web/mobile/desktop): authClient.deleteUser({ password? })
  ▼
Better Auth (/api/auth/delete-user): verifies password (credential) or
  session freshness (social, 300 s) → user.deleteUser.beforeDelete(user)
  ▼
beforeDelete: fetch(`${INTERNAL_API_URL}/v1/internal/users/${user.id}`,
  { method: 'DELETE', headers: { 'X-Internal-Secret': secret }, signal: 15 s })
  ▼
Go httpapi.handleInternalPurgeUser → UserLifecycleService.Purge(ctx, id)
  1 users.GetByID            ErrNotFound → already purged → 204
  2 teams.ListMemberships → ListMembers per team → sole owner w/ others → 409
  3 subscriptions.GetByUserID → payments.CancelSubscription(id, true) if live
  4 tx.RunInTx: re-run 2 (race guard) → delegations.Update(status=revoked) both
      ways → teams.Delete(sole-member teams) → users.Delete(id)  (cascades)
  5 slog.Info("user purged", "userId", id, "teamsDeleted", n, "subscriptionCanceled", b)
  ▼ 204
beforeDelete returns → Better Auth deletes "session", "account", "user"
  → afterDelete: DELETE FROM "verification" WHERE identifier = user.email
  ▼
client: clear offline cache/outbox/stored token → /goodbye (web) or sign-in screen
```

Failure modes: (i) bad password or stale social session → Better Auth 4xx, hook
never runs; (ii) Go unreachable/5xx or 15 s timeout → hook throws
`APIError('SERVICE_UNAVAILABLE')`, client: "Could not delete your account. Try
again."; (iii) 409 → `APIError('CONFLICT', {code:'owns_teams', teams})`, client
lists the teams and links to Settings → Teams; (iv) Paddle error → Go 502
`billing_unavailable`, same retry, nothing deleted; (v) Go purge succeeded but
Better Auth's own delete failed (or the timeout fired late) → auth rows remain,
Go rows gone; next sign-in re-provisions an empty `users` row via `requireAuth`
(`httpapi/middleware.go:19-50`) and the user deletes again — step 1 makes the
purge idempotent.

### Export streaming

`GET /v1/me/export` → `ExportService.Export(ctx, userID, sink)`. The handler
uses `context.WithTimeout(r.Context(), 10*time.Minute)` and
`http.NewResponseController(w).SetWriteDeadline(...)` (the server sets no global
`WriteTimeout`, `cmd/api/main.go:496-500`), writes `Content-Type: application/zip`
and `Content-Disposition: attachment; filename="calendium-export-2026-10-04.zip"`,
wraps `w` in `zip.NewWriter` and passes it as the sink (`zip.Writer.Create`
satisfies `port.ExportSink`). Each file is one `json.Encoder` stream; the
handler flushes after every file. Errors before the first byte (throttle, repo
failure on `profile.json`) are JSON envelopes; errors after it close the
connection without the central directory (corrupt zip), are logged, and the
throttle slot stays claimed. Paging: threads via new
`ThreadRepo.ListByAccountPage(ctx, accountID, afterID, 200)` (keyset on `id`),
messages via `MessageRepo.ListByThread`, events via new
`EventRepo.ListByUserPage(ctx, userID, afterID, 500)`; everything else uses its
existing `ListByUser`. `AccountRepo.GetTokens` is never called.

### Background AI toggle

`PUT /v1/settings {aiBackground:false}` → `SettingsService.Update` →
`user_settings.ai_background`. `SyncServiceDeps` gains optional
`UserSettings port.UserSettingsRepo`; `SyncAccount` reads the owner's settings
once per pass (`aiAllowed`, repo error → `true`, mirroring the `hasClassifiers`
fallback at `sync.go:196-200`) and threads it into the voice-profile enqueue
(`sync.go:121-131`), `enqueueIngestAiJobs` (`sync.go:355-404`) and the
reminder-detect enqueue (`sync.go:628-637`). `sync.go` is the only producer of
`ai_jobs`; the runner (`service/ai_jobs.go:72`) is untouched, so queued jobs
complete. On-demand AI (`/v1/ai/*`, instant replies on request) is unaffected.
Toggle lives in Settings → AI on web (`settings-page.tsx:1205` `AiSection`),
mobile (`settings.tsx:412-423`) and desktop (`SettingsView.tsx:164`).

## Data model changes

### Cascade audit (every table reachable from `users(id)`)

| Table (migration) | Path from users | Today | Action |
|---|---|---|---|
| subscriptions, connected_accounts, oauth_states, snippets, devices (0001) | user_id | CASCADE | none |
| labels, threads, messages, drafts, calendars, sync_state (0001) | account_id → connected_accounts | CASCADE | none |
| thread_labels, attachments, events (0001) | thread / message / calendar | CASCADE | none |
| user_prefs (0005); event_templates, calendar_sets (0006) | user_id | CASCADE | none |
| ai_jobs, ai_classifiers, voice_profiles, ai_usage (0007) | user_id (+account_id) | CASCADE | none |
| booking_links, meeting_polls, user_settings (0008) | user_id | CASCADE | none |
| bookings, poll_votes, time_proposals (0008) | link / poll / event | CASCADE | none |
| message_reactions (0009) | user_id | CASCADE | none |
| **user_preferences (0010)** | user_id, **no FK** | orphaned | add FK CASCADE |
| **teams (0011)** | created_by | **RESTRICT** | nullable + SET NULL |
| team_members, team_invitations (0011) | user_id / invited_by | CASCADE | none (the user's pending invites are dropped) |
| thread_shares (0012), thread_comments (0013), team_thread_activity (0014) | created_by / author_id / user_id | CASCADE | none (the user's team comments disappear) |
| calendar_shares, audit_entries (0016) | grantee_user_id, created_by / actor_id, principal_id | CASCADE | none |
| delegations (0017) | principal_id, assistant_id | CASCADE | none (revoked explicitly first) |
| booking_link_members (0018), tasks (0019), event_notes (0020), calendar_prefs (0021) | user_id | CASCADE | none |
| managed_events (0022), integration_connections (0023), travel_alerts (0025) | user_id | CASCADE | none |
| calendar_subscriptions → subscription_events (0026) | user_id → subscription_id | CASCADE | none |
| billing_events (piece 1) | none (notification ids only) | n/a | retained |
| "session", "account" (0002) | "userId" → "user" | CASCADE | deleted by Better Auth |
| "verification" (0002) | identifier = email | none | afterDelete cleanup |

Teams where the user is a co-owner or a non-owner member survive with the
membership cascaded and `created_by` set NULL; `postgres/team.go` scans
`COALESCE(created_by, '')` and `domain.Team.CreatedBy` is documented as "empty
when the creator's account was deleted".

### Migration `00NN_account_lifecycle.sql` (next free number at execution)

```sql
DELETE FROM user_preferences WHERE user_id NOT IN (SELECT id FROM users);
ALTER TABLE user_preferences ADD CONSTRAINT user_preferences_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE teams ALTER COLUMN created_by DROP NOT NULL;
ALTER TABLE teams DROP CONSTRAINT teams_created_by_fkey;
ALTER TABLE teams ADD CONSTRAINT teams_created_by_fkey
    FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE user_settings ADD COLUMN ai_background boolean NOT NULL DEFAULT true;
CREATE TABLE user_exports (
    user_id    text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    started_at timestamptz NOT NULL
);
```

`postgres_test.go:95-110 truncateAll` gains `user_exports`.

## API contract

### Go routes

| Route | Auth | Success | Errors |
|---|---|---|---|
| `DELETE /v1/internal/users/{id}` | `X-Internal-Secret` | 204 (also when no `users` row) | 404 `not_found` when the secret is unset or the header mismatches (same body as an unknown route); 409 `owns_teams`; 502 `billing_unavailable`; 500 `internal` |
| `GET /v1/me/export` | Bearer | 200 `application/zip` | 409 `export_throttled` + `Retry-After: <seconds>`; 403 `forbidden` under `X-Calendium-Act-As` (existing fail-closed path, `httpapi/delegation.go:127-150`) |
| `GET/PUT /v1/settings` | Bearer | 200 `UserSettings` incl. `aiBackground` | as today |

Envelope stays `{error:{code,message}}` (`httpapi/codec.go:14-21`); `errorDetail`
gains `Details any \`json:"details,omitempty"\`` so `owns_teams` can carry
`{"details":{"teams":[{"id":"t1","name":"Design"}]}}`. New sentinels
`domain.ErrOwnsTeams` (409, wraps the team list), `domain.ErrExportThrottled`
(409, `RetryAfter time.Duration`); `domain.ErrBillingUnavailable` (502) is
piece 1's and is reused. `statusFor` (`codec.go:57-85`) and `safeMessage`
(`codec.go:92-120`) learn the new codes.

Zip contents (pretty JSON, UTC, domain JSON shapes): `profile.json`
(`domain.User`); `accounts.json` (`[{id, provider, email, status, createdAt}]`,
never tokens); `calendars.json`; `events.json`; `threads/<threadId>.json`
(`{thread, messages}` with bodies and `attachments[]` metadata only);
`drafts.json`; `snippets.json`; `templates.json`; `calendar-sets.json`;
`tasks.json`; `booking-links.json`; `bookings.json`; `polls.json` (with votes);
`settings.json` (`{prefs, preferences, calendarPrefs, settings}`); `labels.json`;
`event-notes.json`.

### Better Auth options (`apps/web/lib/auth.ts`)

```ts
user: {
  deleteUser: {
    enabled: true,
    beforeDelete: async (user) => purgeOnApi(user.id),            // throws APIError
    afterDelete: async (user) => deleteVerificationRows(user.email), // best effort
  },
},
session: { freshAge: 300 },
```

`purgeOnApi` lives in server-only `apps/web/lib/internal-api.ts`: reads
`INTERNAL_API_URL` (default `http://localhost:8080`) and `INTERNAL_API_SECRET`,
`AbortSignal.timeout(15_000)`, maps 409 → `APIError('CONFLICT', body.error)`,
anything else non-204 → `APIError('SERVICE_UNAVAILABLE')`. `docker-compose.yml`
gives the web service `INTERNAL_API_URL=http://api:8080` next to the
`AUTH_JWKS_URL` precedent (lines 33-49). Web startup fails without
`INTERNAL_API_SECRET`, where `BETTER_AUTH_SECRET` is already required.

### Shared client and types (`packages/shared`)

- `UserSettings.aiBackground: boolean` (`types.ts:684-688`); `updateSettings`
  unchanged (`client.ts:605-610`).
- `ApiClient.downloadExport(): Promise<Blob>` — authenticated fetch, no JSON
  parsing, 409 → `ApiRequestError('export_throttled')` with `retryAfterSeconds`
  from the header. Web saves it through an object URL; mobile/desktop never call it.
- `ApiRequestError.details?: unknown` (piece 1 adds the same field; first to land owns it).

### Go service and port interfaces

```go
// port/driving.go
type UserLifecycleService interface {
    // Purge removes every row owned by userID. Idempotent: an unknown user is
    // not an error. Returns domain.ErrOwnsTeams, domain.ErrBillingUnavailable
    // or a repo error; DB work is transactional, the Paddle cancel is not.
    Purge(ctx context.Context, userID string) (PurgeReport, error)
}
type PurgeReport struct{ TeamsDeleted int; SubscriptionCanceled bool }

type ExportService interface {
    // Export claims the hourly slot, then writes every file into sink.
    Export(ctx context.Context, userID string, sink ExportSink) error
}
type ExportSink interface{ Create(name string) (io.Writer, error) } // *zip.Writer

// port/driven.go additions
type UserRepo interface { /* existing */; Delete(ctx context.Context, id string) error }
type TeamRepo interface { /* existing */; ListMemberships(ctx context.Context, userID string) ([]domain.TeamMember, error) }
type ThreadRepo interface { /* existing */; ListByAccountPage(ctx context.Context, accountID, afterID string, limit int) ([]domain.Thread, error) }
type EventRepo interface  { /* existing */; ListByUserPage(ctx context.Context, userID, afterID string, limit int) ([]domain.Event, error) }
type UserExportRepo interface {
    // Claim is one atomic upsert: ok=false and the earliest retry time when
    // the user exported less than window ago.
    Claim(ctx context.Context, userID string, now time.Time, window time.Duration) (ok bool, retryAt time.Time, err error)
}

// service
type UserLifecycleDeps struct {
    Users port.UserRepo; Teams port.TeamRepo; Delegations port.DelegationRepo
    Subscriptions port.SubscriptionRepo; Payments port.Payments // nil when SELF_HOSTED
    Tx port.TxRunner; Clock port.Clock; Logger *slog.Logger
}
func NewUserLifecycleService(d UserLifecycleDeps) *UserLifecycleService
func NewExportService(d ExportDeps) *ExportService // ExportDeps: one repo per zip file + UserExportRepo + Clock
```

`domain.UserSettings` gains `AIBackground bool \`json:"aiBackground"\``
(`domain/scheduling.go:152-157`); `postgres/scheduling.go:640-676` reads/writes
`ai_background`, default `true` without a row. Older mobile/desktop builds `PUT`
the whole document without the field, so `handleUpdateSettings`
(`httpapi/scheduling.go:316-336`) decodes `aiBackground` as `*bool` via
`optionalField` (`codec.go:31-49`) and `SettingsService.Update` keeps the stored
value when nil. `httpapi.Deps` gains `Lifecycle port.UserLifecycleService`,
`Export port.ExportService`, `InternalSecret []byte`; `cmd/api/main.go` wires
both beside `settingsSvc` (line 246) and passes the secret into `httpapi.New`
(line 452); `cmd/worker/main.go:151` adds `UserSettings: store.UserSettings()`.

## Security

- `INTERNAL_API_SECRET`: decoded at boot, boot refused when missing or malformed
  in either mode (same block as `TOKEN_ENCRYPTION_KEY`). The handler compares
  `sha256(header)` to `sha256(secret)` with `subtle.ConstantTimeCompare` so
  length never short-circuits. Missing or wrong header → the mux's own
  `404 not_found` body; "wrong secret" is indistinguishable from "no route". The
  route is registered outside `authed(...)` (`httpapi.go:130-133`) like the
  webhook, excluded from CORS reflection, and listens only on the API address;
  the self-hosting docs require the proxy to block `/v1/internal/*`
  (`deploy/caddy/Caddyfile` gets `respond /v1/internal/* 404`).
- Logs carry `userId` only — never email, name, team names or provider ids;
  `beforeDelete` logs the status code only.
- Export: Bearer-authed, no act-as, entitlement-ungated, 1/hour/user, never
  reads provider tokens, streamed (never on disk), 10-minute deadline.
- Deletion needs proof of possession (password or ≤300 s social session) plus
  a typed email on web; Better Auth's CSRF/origin checks cover `/delete-user`.
- Demo mode never calls either route.

## Testing & verification

Go `service`: `TestPurgeOrder` — recording fake `Payments` asserts
`CancelSubscription(id, true)` is called once, after the team check and before
any delete; `TestPurgePaddleFailureAborts` (fake errors → `ErrBillingUnavailable`,
no rows touched, no delegations revoked); `TestPurgeSkipsCancelWithoutLiveSubscription`
(`none`/`canceled`/no id); `TestPurgeOwnsTeamsRefused` (sole owner + others →
`ErrOwnsTeams` with ids/names; co-owner and member proceed);
`TestPurgeDeletesSoleMemberTeams`; `TestPurgeRevokesDelegationsBothWays`;
`TestPurgeUnknownUserIsNoop`; `sync_test.go` `TestBackgroundAIOffSkipsEnqueue`
for all five job kinds plus default-on and repo-error paths;
`TestSettingsUpdatePreservesAIBackgroundWhenAbsent`. Export:
`TestExportWritesEveryFile` seeds one row per collection in the in-memory
fakes, exports into a `zip.Writer` over a buffer, reopens with `zip.NewReader`,
asserts the exact file set, no `accessToken`/`refreshToken` keys in
`accounts.json`, and bodies + attachment metadata in `threads/<id>.json`;
`TestExportThrottle` (second call within the hour → `ErrExportThrottled` with
`RetryAfter > 0`; after the window → ok).

Go `postgres` (testcontainers suite): `TestPurgeCascadeCoverage` inserts one
row into every table in the audit for `u1` (plus `u2` sharing a team and a
delegation), runs `Users().Delete("u1")`, asserts zero rows for `u1` in every
table, `created_by IS NULL` on the surviving team, and `u2`'s rows intact; the
table list is cross-checked against `information_schema.columns` for any
`user_id`/`*_id` column referencing `users`, so a future migration without a
cascade fails here. `TestUserExportClaim`, `TestUserPreferencesCascade`,
`TestTeamsCreatedBySetNull` pin the migration. Go `httpapi`: internal route
404 when `InternalSecret` is empty, 404 on wrong header, 204 on success, 409
body with `details.teams`, 502 mapping; export 409 + `Retry-After`, headers,
act-as rejection; settings tri-state decode.

Web (Vitest) `delete-account-dialog.test.tsx`: confirm disabled until the typed
email matches; password field only when `authClient.listAccounts()` includes
`credential`; `deleteUser` called with the password; `owns_teams` renders the
team list; success calls `signOut` and routes to `/goodbye`.
`account-section.test.tsx`: download calls `downloadExport`, `export_throttled`
shows the retry time, AI toggle calls `updateSettings` with `aiBackground`.
Playwright `e2e/settings.spec.ts` (demo): both buttons render, each click shows
"Not available in demo" and no request to `/api/auth/delete-user` or
`/v1/me/export` is made. Mobile (Jest `settings.test.tsx`): password prompt →
`deleteUser` → offline state cleared (`context/auth.tsx:134-155` path). Desktop
(Vitest `SettingsView.test.tsx`): `getAuthClient().deleteUser` then
`clearStoredToken`; null client → `desktop.OpenExternal(`${origin}/settings?tab=account`)`.

Manual runbook (local compose stack, throwaway users only): (1) sign up via
`POST /api/auth/sign-up/email`, mint a JWT, `GET /v1/me`; (2) seed
`POST /v1/mail/snippets`, `POST /v1/tasks`, `POST /v1/teams`,
`PUT /v1/settings {aiBackground:false}`; (3) `GET /v1/me/export` → zip with all
16 files and the snippet; second call → 409 + `Retry-After`; (4) add a second
user to the team → delete attempt shows `owns_teams` with the team name;
transfer ownership; delete → `/goodbye`; (5) `psql`: `count(*)` is 0 in `users`,
`snippets`, `tasks`, `team_members`, `user_settings`, `user_exports`, `"user"`,
`"session"`, `"account"` for that id; the team survives with
`created_by IS NULL`; (6) the API log has `user purged` with the id and no email.

## Operator docs updates

- `docs/self-hosting/configuration.md:18-25` required table: add
  `INTERNAL_API_SECRET` (**Yes**, both modes, `openssl rand -hex 32`, shared by
  api and web) and `INTERNAL_API_URL` (web only, compose default
  `http://api:8080`); the minimal `.env` block (`configuration.md:132-143`),
  `.env.example:38` and `backend/.env.example:22` gain them beside
  `TOKEN_ENCRYPTION_KEY`.
- `docs/self-hosting/security.md`: checklist (lines 8-18) adds
  "`INTERNAL_API_SECRET` generated fresh"; section 3's table (lines 59-69) adds
  it as **SECRET** (api + web, never public); new section "10. Internal API
  surface" — `/v1/internal/*` is secret-authenticated, must stay behind the
  proxy, and is what account deletion uses.
- `docs/architecture.md:57-70` REST table: the two new routes and
  `aiBackground` on `/v1/settings`.
- Privacy page: "AI features" rewritten — "When new mail arrives we may
  summarise it, draft a reply, suggest quick replies, run your classifiers and
  build your writing-style profile in the background. Turn this off in Settings
  → AI → Background AI processing; on-demand actions still send only the thread
  you point them at. Requests go to OpenRouter, which routes them to model
  providers that vary by model." "Billing information" names Paddle (merchant of
  record) as the holder of payment details. "Third parties" lists OpenRouter,
  Paddle, Google, Microsoft, Todoist, HubSpot, Open-Meteo (weather; coordinates
  only), Nominatim/OSRM (place search and routing; query text and coordinates),
  Apple APNs and Google FCM (push tokens and notification previews).
  "Retention and deletion" names Settings → Account → Delete account and
  Download my data. `UPDATED` → `October 2026`.
- Terms page: billing paragraph names Paddle as merchant of record and says
  "Refund requests are handled by Paddle under its refund policy"; Termination
  keeps the deletion sentence and adds "You can download a copy of your data
  from Settings → Account at any time before deleting your account."; date bumped.
