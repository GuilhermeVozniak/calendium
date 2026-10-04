# Account Lifecycle and Privacy Alignment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the legal pages' account promises true — delete the account from every client in one action, download a copy of the data, turn background AI off, and have privacy/terms describe what the system does and whom it talks to.

**Architecture:** Deletion is Better Auth `deleteUser` whose `beforeDelete` hook calls a secret-authenticated `DELETE /v1/internal/users/{id}` on the Go API; `service.UserLifecycleService.Purge` checks team ownership, cancels a live Paddle subscription through piece 1's `port.Payments.CancelSubscription`, then in one transaction revokes delegations, deletes sole-member teams and the `users` row (migration 0029 closes the two cascade gaps). `GET /v1/me/export` streams a zip through `archive/zip` while repos are read page by page (new keyset pagers); `user_settings.ai_background` gates every `ai_jobs` enqueue in `sync.go`. Web gets a Settings → Account tab (download, delete dialog, `/goodbye`), mobile and desktop get Account controls, and privacy/terms are rewritten.

**Tech Stack:** Go 1.26 stdlib (`net/http`, `archive/zip`, `crypto/subtle`, `database/sql` + pgx driver, `log/slog`), SQL migrations in `backend/migrations`, testcontainers Postgres tests, Next.js 15 + Better Auth 1.6 (`user.deleteUser`, `session.freshAge`), React 19 + TanStack Query + sonner, Expo SDK 54 (jest-expo), Wails v2 / Vite React (Vitest), `packages/shared` TS contract (Vitest), Playwright demo-mode e2e, Biome, golangci-lint.

**Spec:** docs/superpowers/specs/2026-10-04-account-lifecycle-design.md

**Program position:** piece 3 of 6. Executes AFTER piece 1 (`docs/superpowers/plans/2026-10-04-paddle-billing.md`, migration 0027) and is independent of piece 2 (migration 0028). **Migration number used here: `0029` (provisional — if piece 2 lands more than one migration, renumber to the next free number at execution; `ls backend/migrations` first).**

## Global Constraints

- Hexagonal rule: `domain ← port ← service`, adapters at the edges, no frameworks, stdlib only (pgx only as the `database/sql` driver); no business logic in `httpapi`.
- Consumed from piece 1, never redefined: `port.Payments.CancelSubscription(ctx, subscriptionID string, immediately bool) error`; `domain.Subscription.BillingCustomerID` / `BillingSubscriptionID`; statuses `domain.SubscriptionTrialing|SubscriptionActive|SubscriptionPastDue|SubscriptionPaused|SubscriptionCanceled|SubscriptionNone`; `domain.ErrBillingUnavailable` (502 `billing_unavailable`); `port.SubscriptionRepo.GetByUserID`.
- Piece 2 (email) is NOT a dependency: no confirmation mail, no reset flow, no provider-side token revocation, `sendDeleteAccountVerification` unset.
- Internal route: `DELETE /v1/internal/users/{id}`, header `X-Internal-Secret` = `INTERNAL_API_SECRET` (64 hex chars / 32 bytes, required in BOTH modes, parsed like `TOKEN_ENCRYPTION_KEY`), compared as `subtle.ConstantTimeCompare(sha256(header bytes), sha256(secret))`; registered OUTSIDE `authed(...)`, excluded from CORS reflection; missing secret or wrong/missing header → exactly the mux's own 404 body (`http.NotFound`), so "wrong secret" is indistinguishable from "no route". Success → 204 (also when no `users` row). Errors: 409 `owns_teams` with `details.teams[{id,name}]`, 502 `billing_unavailable`, 500 `internal`.
- Purge order: (1) `users.GetByID` — `ErrNotFound` → 204 no-op; (2) team check — sole `owner` of a team with other members → `ErrOwnsTeams`; (3) `CancelSubscription(id, true)` when `BillingSubscriptionID != ""` and status ∈ `{active, past_due, paused, trialing}`, failure → `ErrBillingUnavailable`, nothing deleted; (4) one `RunInTx`: re-run (2), revoke delegations both ways (`status=revoked`, `RevokedAt=now`), delete sole-member teams, `users.Delete` (cascades); (5) one `slog.Info("user purged", "userId", id, "teamsDeleted", n, "subscriptionCanceled", b)` — never email/name/team names.
- Export: `GET /v1/me/export`, Bearer-authed, NOT entitlement-gated, 403 `forbidden` under `X-Calendium-Act-As` (existing fail-closed route table), one per user per hour claimed atomically in `user_exports` else 409 `export_throttled` + `Retry-After: <seconds>`; 10-minute budget (`context.WithTimeout` + `http.NewResponseController(w).SetWriteDeadline`); `Content-Type: application/zip`, `Content-Disposition: attachment; filename="calendium-export-<YYYY-MM-DD>.zip"`; flush after every file; error before the first file → JSON envelope; error after → connection dropped (no central directory), logged, slot stays claimed; `AccountRepo.GetTokens` never called; thread page 200 (keyset on `id` per account), event page 500 (keyset on `id`).
- Zip contents (pretty JSON, UTC, domain JSON shapes), exactly these 16 names: `profile.json`, `accounts.json` (`[{id, provider, email, status, createdAt}]`), `calendars.json`, `events.json`, `threads/<threadId>.json` (`{thread, messages}`), `drafts.json`, `snippets.json`, `templates.json`, `calendar-sets.json`, `tasks.json`, `booking-links.json`, `bookings.json`, `polls.json` (`[{poll, votes}]`), `settings.json` (`{prefs, preferences, calendarPrefs, settings}`), `labels.json`, `event-notes.json`.
- Background AI: `UserSettings.aiBackground` (bool, default `true`); `sync.go` skips every `ai_jobs` enqueue (voice_profile, thread_summary, instant_replies, auto_draft, classify, reminder_detect) when off; queued jobs still complete (`ai_jobs.go` untouched); on-demand `/v1/ai/*` unaffected; a settings repo error reads as `true` (mirrors the `hasClassifiers` fallback). Older clients `PUT` the document without the field → stored value kept (tri-state decode via `optionalField`).
- Migration `0029_account_lifecycle.sql` exactly as the spec: orphan cleanup + `user_preferences_user_id_fkey ... ON DELETE CASCADE`; `teams.created_by` nullable + `teams_created_by_fkey ... ON DELETE SET NULL`; `user_settings.ai_background boolean NOT NULL DEFAULT true`; `user_exports(user_id PK → users CASCADE, started_at)`; `truncateAll` gains `user_exports`.
- Error envelope stays `{error:{code,message}}`; `errorDetail` gains `Details any \`json:"details,omitempty"\``; new sentinels `domain.ErrOwnsTeams` (409), `domain.ErrExportThrottled` (409).
- Better Auth: `user.deleteUser.enabled = true`, `beforeDelete` → `purgeOnApi(user.id)` (throws `APIError`), `afterDelete` → `DELETE FROM "verification" WHERE identifier = email` (best effort), `session.freshAge = 300`; `apps/web/lib/internal-api.ts` reads `INTERNAL_API_URL` (default `http://localhost:8080`) and `INTERNAL_API_SECRET`, `AbortSignal.timeout(15_000)`, 409 → `APIError('CONFLICT', body.error)`, any other non-204 → `APIError('SERVICE_UNAVAILABLE')`; web startup (production, non-demo) fails without `INTERNAL_API_SECRET`; `docker-compose.yml` gives `web` `INTERNAL_API_URL=http://api:8080`; `deploy/caddy/Caddyfile` answers `/v1/internal/*` with 404.
- Shared contract: `UserSettings.aiBackground: boolean`; `ApiClient.downloadExport(): Promise<Blob>` (no JSON parsing; 409 → `ApiRequestError` code `export_throttled` with `retryAfterSeconds` from `Retry-After`); `ApiRequestError.details?: unknown` (piece 1 may already have added it — first to land owns it, do not duplicate).
- Clients: web Settings → Account tab (Change-password slot reserved for piece 2, "Download my data", Danger zone → Delete account dialog: what is deleted, type your email, password only for credential accounts → navigate to `/goodbye` then sign out/clear local state); mobile Settings → Account → Delete account (`authClient.deleteUser`, password field or social re-auth) + "Download my data" link to `<webOrigin>/settings?tab=account`; desktop same via its `better-auth/react` client, null client → `desktop.OpenExternal(`${origin}/settings?tab=account`)`.
- Demo mode and e2e: buttons render; clicks toast "Not available in demo"; no request to `/api/auth/delete-user` or `/v1/me/export`.
- Logs carry `userId` only; `beforeDelete` logs the status code only.

## Review Focus

1. Export paging boundary — an events list that is an exact multiple of the page size must terminate without an extra duplicate page or a dropped tail; every event appears once, in id order (test added to Task 7: `TestExportPagesEventsAcrossBoundary`).
2. `X-Internal-Secret` header with uppercase hex or surrounding whitespace must still match (hex decoding is case-insensitive, header trimmed); a wrong-length but otherwise valid hex string must NOT match (test added to Task 10: `TestInternalPurgeHeaderNormalization`).
3. `PUT /v1/settings {"aiBackground": null}` (explicit null from a client) must leave the stored value unchanged — not flip it to false (covered in Task 10: `TestUpdateSettingsAIBackgroundTriState` null case).
4. Delete dialog: an email typed with different case or surrounding whitespace (`"  Me@Example.com "`) must enable Confirm — normalization, not byte equality (test added to Task 14: `enables confirm when the typed email matches ignoring case and whitespace`).
5. Purge when the user is the sole member of one team AND the sole owner of another team that has other members: the refusal must list only the blocking team and must leave the sole-member team intact (test added to Task 6: `TestPurgeOwnsTeamsRefusedKeepsSoleMemberTeams`).

## Execution tracks

Four parallel tracks with STRICT file ownership. Pieces 1 and 2 have already merged to the base branch before any track starts.

- **Track A — domain/port + migration + Postgres (Tasks 1–4, sequential).** Owns `backend/migrations/0029_account_lifecycle.sql`, `backend/internal/domain/{errors.go,lifecycle.go,lifecycle_test.go,team.go,scheduling.go}`, `backend/internal/port/{driven.go,driving.go,lifecycle_port_test.go}`, `backend/internal/adapter/out/postgres/{store.go,user.go,user_export.go,team.go,mail.go,calendar.go,event_note.go,scheduling.go,postgres_test.go,lifecycle_test.go,scheduling_test.go}`. Starts immediately. NOTE: between Task 1 and Task 3 `go build ./...` is red (the Postgres repos do not yet satisfy the widened ports) — every step in Tasks 1–3 runs package-scoped commands; Task 3 restores a green `go build ./...`.
- **Track B — Go service, config, HTTP, composition (Tasks 5–11, sequential).** Owns `backend/internal/service/{lifecycle.go,lifecycle_test.go,export.go,export_test.go,settings.go,settings_test.go,sync.go,sync_test.go,fakes_test.go,calendar_test.go}`, `backend/internal/config/{config.go,config_test.go}`, `backend/internal/adapter/in/httpapi/{lifecycle.go,lifecycle_handlers_test.go,codec.go,codec_test.go,httpapi.go,harness_test.go,scheduling.go,scheduling_handlers_test.go,middleware.go,middleware_test.go}`, `backend/cmd/api/main.go`, `backend/cmd/worker/main.go`. Tasks 5–10 start after **Task 1** is committed (they compile against the new ports; `service` and `httpapi` do not import `postgres`). Task 11 (composition) starts after **Track A is complete**.
- **Track C — shared contract + web (Tasks 12–17, sequential).** Owns `packages/shared/src/{types.ts,client.ts,client.test.ts,lifecycle.test.ts}`, `apps/web/lib/{auth.ts,auth-lifecycle.test.ts,internal-api.ts,internal-api.test.ts,runtime-env.ts,runtime-env.test.ts,account-data.ts,account-data.test.ts,scheduling-mock.ts}`, `apps/web/instrumentation.ts`, `apps/web/components/app/{delete-account-dialog.tsx,delete-account-dialog.test.tsx,account-section.tsx,account-section.test.tsx,background-ai-card.tsx}`, `apps/web/app/(app)/settings/settings-page.tsx`, `apps/web/app/goodbye/page.tsx`, `apps/web/app/(marketing)/privacy/page.tsx`, `apps/web/app/(marketing)/terms/page.tsx`, `apps/web/e2e/settings.spec.ts`. Starts immediately (the contract is fixed by this plan, not by the Go code).
- **Track D — mobile, desktop, docs, env (Tasks 18–20).** Owns `apps/mobile/app/(tabs)/{settings.tsx,settings.test.tsx}`, `apps/mobile/lib/{server-config.ts,mock.ts}`, `apps/desktop/frontend/src/views/{SettingsView.tsx,SettingsView.test.tsx}`, `apps/desktop/frontend/src/lib/mock.ts`, `docs/self-hosting/{configuration.md,security.md}`, `docs/architecture.md`, `.env.example`, `backend/.env.example`, `docker-compose.yml`, `deploy/caddy/Caddyfile`. Tasks 18 and 19 start after **Task 12** (shared contract) is committed; Task 20 starts immediately. Tasks 18, 19, 20 touch disjoint files and may be split across agents.
- **Tasks 21–22** (full-suite gate, verification runbook) run after every track has merged.

---

### Task 1: Domain + ports — lifecycle errors, `AIBackground`, lifecycle/export driving ports, widened driven ports (Track A)

**Files:**
- Modify: `backend/internal/domain/errors.go` (mapping comment block L9–21; var block L22–55)
- Create: `backend/internal/domain/lifecycle.go`
- Create: `backend/internal/domain/lifecycle_test.go`
- Modify: `backend/internal/domain/team.go` (the `Team.CreatedBy` field, L43–48)
- Modify: `backend/internal/domain/scheduling.go` (the `UserSettings` struct, L152–157)
- Modify: `backend/internal/port/driving.go` (imports L6–11; `SettingsService` L519–523; append new types at the end of the file)
- Modify: `backend/internal/port/driven.go` (`UserRepo` L47–52; `ThreadRepo` L102–137; `EventRepo` L246–262; `EventNoteRepo` L298–303; `UserSettingsRepo` L503–508; `TeamRepo` L512–529; append `UserExportRepo` after `UserSettingsRepo`)
- Create: `backend/internal/port/lifecycle_port_test.go`

**Interfaces:**
- Consumes: nothing new (piece 1's `domain.Subscription`/`port.Payments` are already on the base branch).
- Produces: `domain.ErrOwnsTeams`, `domain.ErrExportThrottled`; `domain.TeamRef{ID, Name string}`; `*domain.OwnsTeamsError{Teams []TeamRef}` (Unwrap → `ErrOwnsTeams`); `*domain.ExportThrottledError{RetryAfter time.Duration}` (Unwrap → `ErrExportThrottled`); `domain.UserSettings.AIBackground bool \`json:"aiBackground"\``; `port.PurgeReport{TeamsDeleted int; SubscriptionCanceled bool}`; `port.UserLifecycleService{Purge(ctx, userID string) (PurgeReport, error)}`; `port.ExportSink{Create(name string) (io.Writer, error)}`; `port.ExportService{Export(ctx, userID string, sink ExportSink) error}`; `port.SettingsService.SetAIBackground(ctx, userID string, on bool) (domain.UserSettings, error)`; `port.UserRepo.Delete(ctx, id string) error`; `port.TeamRepo.ListMemberships(ctx, userID string) ([]domain.TeamMember, error)`; `port.ThreadRepo.ListByAccountPage(ctx, accountID, afterID string, limit int) ([]domain.Thread, error)`; `port.EventRepo.ListByUserPage(ctx, userID, afterID string, limit int) ([]domain.Event, error)`; `port.EventNoteRepo.ListByUser(ctx, userID string) ([]domain.EventNote, error)`; `port.UserSettingsRepo.SetAIBackground(ctx, userID string, on bool) error`; `port.UserExportRepo{Claim(ctx, userID string, now time.Time, window time.Duration) (ok bool, retryAt time.Time, err error)}`.

- [ ] **Step 1: Write the failing domain test** at `backend/internal/domain/lifecycle_test.go`:

```go
package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestOwnsTeamsErrorWrapsSentinel(t *testing.T) {
	var err error = &OwnsTeamsError{Teams: []TeamRef{{ID: "t1", Name: "Design"}}}
	if !errors.Is(err, ErrOwnsTeams) {
		t.Fatal("OwnsTeamsError must unwrap to ErrOwnsTeams")
	}
	var typed *OwnsTeamsError
	if !errors.As(err, &typed) || len(typed.Teams) != 1 || typed.Teams[0].Name != "Design" {
		t.Fatalf("errors.As lost the team list: %+v", typed)
	}
	if got := err.Error(); got != "owns teams: 1 team(s) need another owner first" {
		t.Fatalf("Error() = %q", got)
	}
}

func TestExportThrottledErrorWrapsSentinel(t *testing.T) {
	var err error = &ExportThrottledError{RetryAfter: 90 * time.Second}
	if !errors.Is(err, ErrExportThrottled) {
		t.Fatal("ExportThrottledError must unwrap to ErrExportThrottled")
	}
	var typed *ExportThrottledError
	if !errors.As(err, &typed) || typed.RetryAfter != 90*time.Second {
		t.Fatalf("errors.As lost RetryAfter: %+v", typed)
	}
}

func TestUserSettingsAIBackgroundJSONName(t *testing.T) {
	b, err := json.Marshal(UserSettings{TimeZone: "UTC", WorkingHours: []AvailabilityWindow{}, AIBackground: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"aiBackground":true`) {
		t.Fatalf("UserSettings JSON = %s, want an aiBackground field", b)
	}
}
```

- [ ] **Step 2: Write the port compile-contract test** at `backend/internal/port/lifecycle_port_test.go`:

```go
package port

import (
	"archive/zip"
	"context"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// *zip.Writer is the production ExportSink (httpapi wraps the response in
// one); this pins the Create(name) (io.Writer, error) shape.
func TestZipWriterIsExportSink(t *testing.T) {
	var _ ExportSink = (*zip.Writer)(nil)
}

type stubExports struct{}

func (stubExports) Claim(context.Context, string, time.Time, time.Duration) (bool, time.Time, error) {
	return true, time.Time{}, nil
}

type stubLifecycle struct{}

func (stubLifecycle) Purge(context.Context, string) (PurgeReport, error) { return PurgeReport{}, nil }

func TestLifecyclePortShapes(t *testing.T) {
	var _ UserExportRepo = stubExports{}
	var _ UserLifecycleService = stubLifecycle{}
	var _ = domain.ErrOwnsTeams
}
```

- [ ] **Step 3: Run both and confirm the failure.** `cd backend && go test ./internal/domain/ ./internal/port/` — expected: `undefined: OwnsTeamsError`, `undefined: ExportThrottledError`, `unknown field AIBackground`, `undefined: ExportSink`, `undefined: UserExportRepo`, `undefined: PurgeReport`.

- [ ] **Step 4: Add the sentinels to `backend/internal/domain/errors.go`.** Extend the mapping comment block with two lines after `ErrRateLimited     → 429`:

```go
//	ErrOwnsTeams       → 409 (code owns_teams, details.teams)
//	ErrExportThrottled → 409 (code export_throttled + Retry-After)
```

and append to the `var (...)` block, after `ErrRateLimited`:

```go
	// ErrOwnsTeams marks an account deletion refused because the user is the
	// sole owner of a team that still has other members (transfer ownership
	// first). Carried by *OwnsTeamsError; the HTTP adapter maps it to 409
	// "owns_teams" with the team list in details.
	ErrOwnsTeams = errors.New("owns teams")
	// ErrExportThrottled marks a data export requested within the hourly
	// window. Carried by *ExportThrottledError; mapped to 409
	// "export_throttled" plus a Retry-After header.
	ErrExportThrottled = errors.New("export throttled")
```

- [ ] **Step 5: Create `backend/internal/domain/lifecycle.go`:**

```go
package domain

import (
	"fmt"
	"time"
)

// TeamRef identifies a team in an owns_teams refusal: id plus display name
// only (both are already visible to the refused user as a member).
type TeamRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// OwnsTeamsError wraps ErrOwnsTeams with the teams that still need another
// owner before the account can be deleted.
type OwnsTeamsError struct {
	Teams []TeamRef
}

func (e *OwnsTeamsError) Error() string {
	return fmt.Sprintf("owns teams: %d team(s) need another owner first", len(e.Teams))
}

func (e *OwnsTeamsError) Unwrap() error { return ErrOwnsTeams }

// ExportThrottledError wraps ErrExportThrottled with how long the caller
// must wait before the next export slot opens.
type ExportThrottledError struct {
	RetryAfter time.Duration
}

func (e *ExportThrottledError) Error() string {
	return fmt.Sprintf("export throttled: retry after %s", e.RetryAfter)
}

func (e *ExportThrottledError) Unwrap() error { return ErrExportThrottled }
```

- [ ] **Step 6: Document `Team.CreatedBy` in `backend/internal/domain/team.go`.** Replace the `Team` struct with:

```go
type Team struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// CreatedBy is the creator's user id; empty when the creator's account
	// was deleted (teams.created_by is SET NULL on user deletion, scanned
	// back as COALESCE(created_by, '')).
	CreatedBy string    `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
}
```

- [ ] **Step 7: Add `AIBackground` to `UserSettings` in `backend/internal/domain/scheduling.go`:**

```go
// UserSettings carries per-user scheduling preferences (working hours in
// TimeZone, displayed location) and the background-AI switch. Zero-value
// WorkingHours = no constraint.
type UserSettings struct {
	UserID          string               `json:"-"`
	TimeZone        string               `json:"timeZone"`
	WorkingHours    []AvailabilityWindow `json:"workingHours"`
	WorkingLocation string               `json:"workingLocation"` // "", "office", "home", or free text
	// AIBackground gates every background AI enqueue for this user (voice
	// profile, thread summaries, instant replies, auto drafts, classifiers,
	// reminder detection). Default true. On-demand /v1/ai/* calls are never
	// gated by it. Written only through UserSettingsRepo.SetAIBackground.
	AIBackground bool `json:"aiBackground"`
}
```

- [ ] **Step 8: Widen `backend/internal/port/driving.go`.** Add `"io"` to the import block (between `"context"` and `"time"`). Replace the `SettingsService` interface with:

```go
// SettingsService reads/writes per-user scheduling settings.
type SettingsService interface {
	Get(ctx context.Context, userID string) (domain.UserSettings, error)
	// Update replaces time zone, working hours and location. It never
	// touches AIBackground (older clients PUT the document without the
	// field); the returned document carries the stored value.
	Update(ctx context.Context, userID string, s domain.UserSettings) (domain.UserSettings, error)
	// SetAIBackground flips the background-AI switch and returns the
	// resulting document.
	SetAIBackground(ctx context.Context, userID string, on bool) (domain.UserSettings, error)
}
```

Append at the end of the file:

```go
// --- Account lifecycle (production-readiness piece 3) ------------------------

// PurgeReport summarises what Purge removed beyond the users-row cascade.
type PurgeReport struct {
	TeamsDeleted         int
	SubscriptionCanceled bool
}

// UserLifecycleService deletes accounts (DELETE /v1/internal/users/{id},
// called by Better Auth's beforeDelete hook).
type UserLifecycleService interface {
	// Purge removes every row owned by userID. Idempotent: an unknown user
	// is not an error. Returns domain.ErrOwnsTeams (as *domain.OwnsTeamsError),
	// domain.ErrBillingUnavailable or a repo error; the DB work is
	// transactional, the payment-provider cancel is not.
	Purge(ctx context.Context, userID string) (PurgeReport, error)
}

// ExportSink receives the files of a data export, one writer per file.
// *zip.Writer satisfies it.
type ExportSink interface {
	Create(name string) (io.Writer, error)
}

// ExportService streams the user's data (GET /v1/me/export).
type ExportService interface {
	// Export claims the hourly export slot (domain.ErrExportThrottled, as
	// *domain.ExportThrottledError, when taken), then writes every export
	// file into sink in a fixed order. It never reads provider tokens.
	Export(ctx context.Context, userID string, sink ExportSink) error
}
```

- [ ] **Step 9: Widen `backend/internal/port/driven.go`.** Make these exact edits:

`UserRepo` becomes:

```go
// UserRepo persists users keyed by the Better Auth subject id.
type UserRepo interface {
	// Upsert inserts the user or refreshes email/name/avatar on conflict.
	Upsert(ctx context.Context, u domain.User) (domain.User, error)
	GetByID(ctx context.Context, id string) (domain.User, error)
	// Delete removes the users row; every owned table cascades (migration
	// 0029 audit). domain.ErrNotFound when absent.
	Delete(ctx context.Context, id string) error
}
```

Append to `ThreadRepo` (after `SetReminderIfUnset`):

```go
	// ListByAccountPage pages one account's threads by id (keyset: id >
	// afterID, ascending, at most limit) for the data export.
	ListByAccountPage(ctx context.Context, accountID, afterID string, limit int) ([]domain.Thread, error)
```

Append to `EventRepo` (after `ClearGeo`):

```go
	// ListByUserPage pages every event on the user's calendars by id
	// (keyset: id > afterID, ascending, at most limit) for the data export.
	ListByUserPage(ctx context.Context, userID, afterID string, limit int) ([]domain.Event, error)
```

Append to `EventNoteRepo` (after `GetByEventID`):

```go
	// ListByUser returns every note the user wrote, ordered by event id.
	ListByUser(ctx context.Context, userID string) ([]domain.EventNote, error)
```

Replace `UserSettingsRepo` with, and add `UserExportRepo` right after it:

```go
// UserSettingsRepo persists per-user scheduling settings; Get returns a
// zero-value UserSettings (TimeZone "UTC", AIBackground true) when no row
// exists.
type UserSettingsRepo interface {
	Get(ctx context.Context, userID string) (domain.UserSettings, error)
	// Upsert writes time zone, working hours and location. It NEVER writes
	// ai_background (new rows take the column default true; existing rows
	// keep their value) so a client that omits the field cannot flip it.
	Upsert(ctx context.Context, s domain.UserSettings) error
	// SetAIBackground writes only ai_background, creating the row with
	// defaults when absent.
	SetAIBackground(ctx context.Context, userID string, on bool) error
}

// UserExportRepo enforces the one-export-per-hour throttle (table
// user_exports).
type UserExportRepo interface {
	// Claim is one atomic upsert: ok=true (re)stamps the slot with now;
	// ok=false reports the earliest retry time when the user exported less
	// than window ago.
	Claim(ctx context.Context, userID string, now time.Time, window time.Duration) (ok bool, retryAt time.Time, err error)
}
```

Append to `TeamRepo` (after `CountByRole`):

```go
	// ListMemberships returns every team_members row for userID (the
	// account-deletion team-ownership check).
	ListMemberships(ctx context.Context, userID string) ([]domain.TeamMember, error)
```

- [ ] **Step 10: Run the package tests.** `cd backend && go test ./internal/domain/ ./internal/port/` — expected: `ok` for both.

- [ ] **Step 11: Commit.**

```bash
git add backend/internal/domain backend/internal/port
git commit -m "feat(domain,port): account-lifecycle errors, AIBackground flag, lifecycle/export ports and widened repos" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 2: Migration 0029 + `user_exports` repo + `ai_background` persistence (Track A)

**Files:**
- Create: `backend/migrations/0029_account_lifecycle.sql`
- Create: `backend/internal/adapter/out/postgres/user_export.go`
- Modify: `backend/internal/adapter/out/postgres/store.go` (accessor list L28–57; assertion block L59–91; type block L93–123)
- Modify: `backend/internal/adapter/out/postgres/scheduling.go` (`userSettingsRepo.Get`/`Upsert` L640–676)
- Modify: `backend/internal/adapter/out/postgres/postgres_test.go` (`truncateAll` L95–110)
- Create: `backend/internal/adapter/out/postgres/lifecycle_test.go`
- Modify: `backend/internal/adapter/out/postgres/scheduling_test.go` (append)

**Interfaces:**
- Consumes: `port.UserExportRepo`, `port.UserSettingsRepo.SetAIBackground`, `domain.UserSettings.AIBackground` (Task 1).
- Produces: `(*Store) UserExports() port.UserExportRepo`; `userExportRepo.Claim`; `userSettingsRepo.Get` reads `ai_background` (default `true` without a row); `userSettingsRepo.SetAIBackground`; tables `user_exports`, column `user_settings.ai_background`, FK `user_preferences_user_id_fkey`, FK `teams_created_by_fkey` (SET NULL).

- [ ] **Step 1: Write the failing migration/repo tests** at `backend/internal/adapter/out/postgres/lifecycle_test.go`:

```go
package postgres

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

func TestUserExportClaim(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

	ok, _, err := st.UserExports().Claim(ctx, "u1", now, time.Hour)
	if err != nil || !ok {
		t.Fatalf("first claim: ok=%v err=%v, want ok", ok, err)
	}
	ok, retryAt, err := st.UserExports().Claim(ctx, "u1", now.Add(10*time.Minute), time.Hour)
	if err != nil || ok {
		t.Fatalf("second claim inside the window: ok=%v err=%v, want refused", ok, err)
	}
	if !retryAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("retryAt = %v, want %v", retryAt, now.Add(time.Hour))
	}
	var started time.Time
	if err := db.QueryRowContext(ctx, `SELECT started_at FROM user_exports WHERE user_id = 'u1'`).Scan(&started); err != nil {
		t.Fatal(err)
	}
	if !started.Equal(now) {
		t.Fatalf("a refused claim must not move started_at: got %v, want %v", started, now)
	}
	ok, _, err = st.UserExports().Claim(ctx, "u1", now.Add(time.Hour), time.Hour)
	if err != nil || !ok {
		t.Fatalf("claim exactly one window later: ok=%v err=%v, want ok", ok, err)
	}
}

func TestUserExportsCascadeFromUsers(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	if ok, _, err := st.UserExports().Claim(ctx, "u1", time.Now(), time.Hour); err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = 'u1'`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM user_exports WHERE user_id = 'u1'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("user_exports rows after user delete = %d err=%v, want 0", n, err)
	}
}

func TestUserPreferencesCascade(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	if err := st.UserPreferences().Put(ctx, "u1", port.UserPreferences{Theme: "ocean"}); err != nil {
		t.Fatal(err)
	}
	var fk int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM pg_constraint WHERE conname = 'user_preferences_user_id_fkey' AND confdeltype = 'c'`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("user_preferences_user_id_fkey ON DELETE CASCADE present = %d err=%v, want 1", fk, err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = 'u1'`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM user_preferences WHERE user_id = 'u1'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("user_preferences rows after user delete = %d err=%v, want 0", n, err)
	}
}

func TestTeamsCreatedBySetNull(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	teams := NewTeamRepo(st)
	team, err := teams.Create(ctx, domain.Team{Name: "Design", CreatedBy: "u1"},
		domain.TeamMember{UserID: "u1", Role: domain.TeamRoleOwner})
	if err != nil {
		t.Fatal(err)
	}
	if err := teams.UpsertMember(ctx, domain.TeamMember{TeamID: team.ID, UserID: "u2", Role: domain.TeamRoleOwner}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = 'u1'`); err != nil {
		t.Fatalf("deleting a team creator must no longer be RESTRICTed: %v", err)
	}
	var createdBy sql.NullString
	var members int
	if err := db.QueryRowContext(ctx, `SELECT created_by FROM teams WHERE id = $1`, team.ID).Scan(&createdBy); err != nil {
		t.Fatalf("team must survive its creator: %v", err)
	}
	if createdBy.Valid {
		t.Fatalf("created_by = %q, want NULL", createdBy.String)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM team_members WHERE team_id = $1`, team.ID).Scan(&members); err != nil || members != 1 {
		t.Fatalf("surviving members = %d err=%v, want 1 (u2)", members, err)
	}
}
```

And append to `backend/internal/adapter/out/postgres/scheduling_test.go`:

```go
func TestUserSettingsAIBackgroundDefaultsAndSet(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	got, err := st.UserSettings().Get(ctx, "u1")
	if err != nil || !got.AIBackground {
		t.Fatalf("Get without a row: AIBackground=%v err=%v, want true", got.AIBackground, err)
	}
	if err := st.UserSettings().SetAIBackground(ctx, "u1", false); err != nil {
		t.Fatalf("SetAIBackground on a user without a row: %v", err)
	}
	got, err = st.UserSettings().Get(ctx, "u1")
	if err != nil || got.AIBackground || got.TimeZone != "UTC" {
		t.Fatalf("after SetAIBackground(false): %+v err=%v, want AIBackground=false TimeZone=UTC", got, err)
	}
	// A full-document Upsert from an older client must not flip the switch back.
	if err := st.UserSettings().Upsert(ctx, domain.UserSettings{UserID: "u1", TimeZone: "Europe/Lisbon", WorkingHours: []domain.AvailabilityWindow{}}); err != nil {
		t.Fatal(err)
	}
	got, err = st.UserSettings().Get(ctx, "u1")
	if err != nil || got.AIBackground || got.TimeZone != "Europe/Lisbon" {
		t.Fatalf("after Upsert: %+v err=%v, want AIBackground still false", got, err)
	}
	if err := st.UserSettings().SetAIBackground(ctx, "u1", true); err != nil {
		t.Fatal(err)
	}
	if got, _ = st.UserSettings().Get(ctx, "u1"); !got.AIBackground {
		t.Fatal("SetAIBackground(true) did not persist")
	}
}
```

- [ ] **Step 2: Run and confirm the failure.** `cd backend && REQUIRE_DOCKER=1 go test ./internal/adapter/out/postgres/ -run 'TestUserExport|TestUserPreferencesCascade|TestTeamsCreatedBySetNull|TestUserSettingsAIBackground'` — expected: compile errors `st.UserExports undefined`, `st.UserSettings().SetAIBackground undefined` (plus the pre-existing `var _ port.X` assertion failures from Task 1 — those clear in Task 3).

- [ ] **Step 3: Create `backend/migrations/0029_account_lifecycle.sql`:**

```sql
-- 0029_account_lifecycle.sql — production-readiness piece 3: account
-- deletion, data export, background-AI switch.
--
-- Cascade audit (docs/superpowers/specs/2026-10-04-account-lifecycle-design.md):
-- every table reachable from users(id) already cascades except
--   user_preferences (0010): user_id PRIMARY KEY with NO foreign key → orphans
--   teams (0011): created_by ... ON DELETE RESTRICT → deletion refused for
--                 anyone who created a surviving team.

-- user_preferences: drop orphans, then make the row follow its user.
DELETE FROM user_preferences WHERE user_id NOT IN (SELECT id FROM users);
ALTER TABLE user_preferences ADD CONSTRAINT user_preferences_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

-- teams: the team outlives its creator; created_by becomes NULL (scanned
-- back as '' by postgres/team.go).
ALTER TABLE teams ALTER COLUMN created_by DROP NOT NULL;
ALTER TABLE teams DROP CONSTRAINT teams_created_by_fkey;
ALTER TABLE teams ADD CONSTRAINT teams_created_by_fkey
    FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL;

-- Settings → AI → "Background AI processing". Default on; written only by
-- UserSettingsRepo.SetAIBackground so clients omitting the field keep it.
ALTER TABLE user_settings ADD COLUMN ai_background boolean NOT NULL DEFAULT true;

-- One data export per user per hour, claimed atomically (UserExportRepo.Claim).
CREATE TABLE user_exports (
    user_id    text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    started_at timestamptz NOT NULL
);
```

- [ ] **Step 4: Create `backend/internal/adapter/out/postgres/user_export.go`:**

```go
package postgres

import (
	"context"
	"time"
)

// --- port.UserExportRepo -----------------------------------------------------

// Claim is one atomic upsert: the row is (re)stamped with now only when the
// previous export started at least window ago. When the conditional update
// touches nothing, the stored started_at tells the caller when to retry.
func (r userExportRepo) Claim(ctx context.Context, userID string, now time.Time, window time.Duration) (bool, time.Time, error) {
	res, err := r.q(ctx).ExecContext(ctx, `
		INSERT INTO user_exports (user_id, started_at) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET started_at = EXCLUDED.started_at
		WHERE user_exports.started_at <= $2::timestamptz - make_interval(secs => $3)`,
		userID, now.UTC(), window.Seconds())
	if err != nil {
		return false, time.Time{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, time.Time{}, err
	}
	if n == 1 {
		return true, time.Time{}, nil
	}
	var started time.Time
	if err := r.q(ctx).QueryRowContext(ctx,
		`SELECT started_at FROM user_exports WHERE user_id = $1`, userID).Scan(&started); err != nil {
		return false, time.Time{}, notFound(err)
	}
	return false, started.UTC().Add(window), nil
}
```

- [ ] **Step 5: Register the repo in `backend/internal/adapter/out/postgres/store.go`.** After the line `func (s *Store) UserSettings() port.UserSettingsRepo  { return userSettingsRepo{s} }` add:

```go
func (s *Store) UserExports() port.UserExportRepo    { return userExportRepo{s} }
```

In the `var (...)` assertion block, after `_ port.UserSettingsRepo    = userSettingsRepo{}` add `_ port.UserExportRepo      = userExportRepo{}`. In the `type (...)` block, after `userSettingsRepo    struct{ *Store }` add `userExportRepo      struct{ *Store }`. Run `gofmt -w backend/internal/adapter/out/postgres/store.go` so the alignment is canonical.

- [ ] **Step 6: Persist `ai_background` in `backend/internal/adapter/out/postgres/scheduling.go`.** Replace `userSettingsRepo.Get` and `Upsert` with, and add `SetAIBackground`:

```go
// Get returns a zero-value UserSettings (TimeZone "UTC", AIBackground true)
// when no row exists, mirroring prefsRepo.Get's absent-row default rather
// than surfacing domain.ErrNotFound — scheduling settings always have sane
// defaults.
//
// user_settings.updated_at is write-only from this repo's perspective: it is
// stamped via SQL now() on every write but never scanned back, since
// domain.UserSettings carries no UpdatedAt field (Task 1 review note).
func (r userSettingsRepo) Get(ctx context.Context, userID string) (domain.UserSettings, error) {
	var s domain.UserSettings
	var workingHours []byte
	err := r.q(ctx).QueryRowContext(ctx, `
		SELECT user_id, time_zone, working_hours, working_location, ai_background
		FROM user_settings WHERE user_id = $1`, userID).Scan(
		&s.UserID, &s.TimeZone, &workingHours, &s.WorkingLocation, &s.AIBackground)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.UserSettings{UserID: userID, TimeZone: "UTC", AIBackground: true}, nil
	}
	if err != nil {
		return domain.UserSettings{}, err
	}
	if err := unmarshalInto(workingHours, &s.WorkingHours); err != nil {
		return domain.UserSettings{}, err
	}
	if s.WorkingHours == nil {
		s.WorkingHours = []domain.AvailabilityWindow{}
	}
	return s, nil
}

// Upsert deliberately leaves ai_background out of both the INSERT column
// list (the column default applies) and the DO UPDATE SET list, so a client
// that PUTs the document without the field can never flip the switch.
func (r userSettingsRepo) Upsert(ctx context.Context, s domain.UserSettings) error {
	workingHours, err := jsonArray(s.WorkingHours)
	if err != nil {
		return err
	}
	_, err = r.q(ctx).ExecContext(ctx, `
		INSERT INTO user_settings (user_id, time_zone, working_hours, working_location, updated_at)
		VALUES ($1, $2, $3::jsonb, $4, now())
		ON CONFLICT (user_id) DO UPDATE SET
			time_zone        = EXCLUDED.time_zone,
			working_hours    = EXCLUDED.working_hours,
			working_location = EXCLUDED.working_location,
			updated_at       = now()`,
		s.UserID, s.TimeZone, workingHours, s.WorkingLocation)
	return err
}

// SetAIBackground writes only the switch; a missing row is created with the
// other columns' defaults (UTC, no windows, no location).
func (r userSettingsRepo) SetAIBackground(ctx context.Context, userID string, on bool) error {
	_, err := r.q(ctx).ExecContext(ctx, `
		INSERT INTO user_settings (user_id, ai_background, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (user_id) DO UPDATE SET
			ai_background = EXCLUDED.ai_background,
			updated_at    = now()`, userID, on)
	return err
}
```

- [ ] **Step 7: Add `user_exports` to `truncateAll` in `backend/internal/adapter/out/postgres/postgres_test.go`.** Change the last table line of the `TRUNCATE` statement from `calendar_subscriptions, subscription_events, travel_alerts` to `calendar_subscriptions, subscription_events, travel_alerts, user_exports`.

- [ ] **Step 8: Run the targeted tests.** `cd backend && REQUIRE_DOCKER=1 go test ./internal/adapter/out/postgres/ -run 'TestUserExport|TestUserPreferencesCascade|TestTeamsCreatedBySetNull|TestUserSettingsAIBackground'` — expected: still a compile failure from the Task-1 port widening (`*TeamRepo does not implement port.TeamRepo (missing method ListMemberships)` etc.). That is Task 3's job; proceed.

- [ ] **Step 9: Commit (compile is restored by Task 3 in the same track).**

```bash
git add backend/migrations/0029_account_lifecycle.sql backend/internal/adapter/out/postgres
git commit -m "feat(postgres): migration 0029 (cascade fixes, ai_background, user_exports) + UserExportRepo.Claim + SetAIBackground" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 3: Postgres repo methods — `Users.Delete`, `Teams.ListMemberships` + NULL creator, thread/event keyset pagers, `EventNotes.ListByUser` (Track A)

**Files:**
- Modify: `backend/internal/adapter/out/postgres/user.go` (append after `GetByID`, L37–40)
- Modify: `backend/internal/adapter/out/postgres/team.go` (`teamCols` L22; `ListByUser` SELECT L80–82; append `ListMemberships` after `CountByRole` L172–178)
- Modify: `backend/internal/adapter/out/postgres/mail.go` (append after `ListInboxBefore`, ~L407–423)
- Modify: `backend/internal/adapter/out/postgres/calendar.go` (append after `ListInRange`, ~L253–282)
- Modify: `backend/internal/adapter/out/postgres/event_note.go` (append)
- Modify: `backend/internal/adapter/out/postgres/lifecycle_test.go` (append)

**Interfaces:**
- Consumes: Task 1 ports.
- Produces: `userRepo.Delete`; `(*TeamRepo) ListMemberships`; `teamCols` = `id, name, COALESCE(created_by, ''), created_at`; `threadRepo.ListByAccountPage`; `eventRepo.ListByUserPage`; `eventNoteRepo.ListByUser`. After this task `go build ./...` is green again.

- [ ] **Step 1: Append the failing repo tests to `backend/internal/adapter/out/postgres/lifecycle_test.go`:**

```go
func TestUserRepoDelete(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	if err := st.Users().Delete(ctx, "u1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := st.Users().GetByID(ctx, "u1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByID after Delete err = %v, want ErrNotFound", err)
	}
	if err := st.Users().Delete(ctx, "u1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second Delete err = %v, want ErrNotFound", err)
	}
}

func TestTeamRepoListMembershipsAndNullCreator(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	teams := NewTeamRepo(st)
	a, err := teams.Create(ctx, domain.Team{Name: "A", CreatedBy: "u2"}, domain.TeamMember{UserID: "u2", Role: domain.TeamRoleOwner})
	if err != nil {
		t.Fatal(err)
	}
	b, err := teams.Create(ctx, domain.Team{Name: "B", CreatedBy: "u1"}, domain.TeamMember{UserID: "u1", Role: domain.TeamRoleOwner})
	if err != nil {
		t.Fatal(err)
	}
	if err := teams.UpsertMember(ctx, domain.TeamMember{TeamID: a.ID, UserID: "u1", Role: domain.TeamRoleMember}); err != nil {
		t.Fatal(err)
	}
	got, err := teams.ListMemberships(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("memberships = %d, want 2", len(got))
	}
	roles := map[string]domain.TeamRole{}
	for _, m := range got {
		if m.UserID != "u1" {
			t.Fatalf("membership for %q leaked into u1's list", m.UserID)
		}
		roles[m.TeamID] = m.Role
	}
	if roles[a.ID] != domain.TeamRoleMember || roles[b.ID] != domain.TeamRoleOwner {
		t.Fatalf("roles = %v", roles)
	}
	if _, err := db.ExecContext(ctx, `UPDATE teams SET created_by = NULL WHERE id = $1`, b.ID); err != nil {
		t.Fatal(err)
	}
	team, err := teams.GetByID(ctx, b.ID)
	if err != nil || team.CreatedBy != "" {
		t.Fatalf("GetByID with NULL created_by = (%+v, %v), want CreatedBy \"\"", team, err)
	}
	list, err := teams.ListByUser(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	for _, tm := range list {
		if tm.ID == b.ID && tm.CreatedBy != "" {
			t.Fatalf("ListByUser scanned NULL created_by as %q", tm.CreatedBy)
		}
	}
}

func TestThreadRepoListByAccountPage(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	mine := seedAccount(t, st, "u1")
	theirs := seedAccount(t, st, "u2")
	now := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	ids := []string{}
	for i := 0; i < 3; i++ {
		ids = append(ids, seedThread(t, st, mine.ID, now.Add(time.Duration(i)*time.Minute)).ID)
	}
	seedThread(t, st, theirs.ID, now)
	sort.Strings(ids)

	page1, err := st.Threads().ListByAccountPage(ctx, mine.ID, "", 2)
	if err != nil || len(page1) != 2 || page1[0].ID != ids[0] || page1[1].ID != ids[1] {
		t.Fatalf("page1 = %v err=%v, want ids %v", page1, err, ids[:2])
	}
	page2, err := st.Threads().ListByAccountPage(ctx, mine.ID, page1[1].ID, 2)
	if err != nil || len(page2) != 1 || page2[0].ID != ids[2] {
		t.Fatalf("page2 = %v err=%v, want [%s]", page2, err, ids[2])
	}
	page3, err := st.Threads().ListByAccountPage(ctx, mine.ID, page2[0].ID, 2)
	if err != nil || len(page3) != 0 {
		t.Fatalf("page3 = %v err=%v, want empty", page3, err)
	}
}

func TestEventRepoListByUserPage(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	mine := seedAccount(t, st, "u1")
	theirs := seedAccount(t, st, "u2")
	myCal := seedCalendar(t, st, mine.ID)
	theirCal := seedCalendar(t, st, theirs.ID)
	now := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	ids := []string{}
	for i := 0; i < 3; i++ {
		e, err := st.Events().Upsert(ctx, domain.Event{
			CalendarID: myCal.ID, ProviderEventID: fmt.Sprintf("pe%d", i), Title: "mine",
			Start: now, End: now.Add(time.Hour), Status: domain.EventConfirmed,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, e.ID)
	}
	if _, err := st.Events().Upsert(ctx, domain.Event{
		CalendarID: theirCal.ID, ProviderEventID: "pe-other", Title: "theirs",
		Start: now, End: now.Add(time.Hour), Status: domain.EventConfirmed,
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(ids)

	page1, err := st.Events().ListByUserPage(ctx, "u1", "", 2)
	if err != nil || len(page1) != 2 || page1[0].ID != ids[0] || page1[1].ID != ids[1] {
		t.Fatalf("page1 = %v err=%v", page1, err)
	}
	page2, err := st.Events().ListByUserPage(ctx, "u1", page1[1].ID, 2)
	if err != nil || len(page2) != 1 || page2[0].ID != ids[2] {
		t.Fatalf("page2 = %v err=%v", page2, err)
	}
	for _, e := range append(page1, page2...) {
		if e.Title != "mine" {
			t.Fatalf("another user's event leaked: %+v", e)
		}
	}
}

func TestEventNoteRepoListByUser(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	mine := seedAccount(t, st, "u1")
	theirs := seedAccount(t, st, "u2")
	myCal := seedCalendar(t, st, mine.ID)
	theirCal := seedCalendar(t, st, theirs.ID)
	now := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	e1, _ := st.Events().Upsert(ctx, domain.Event{CalendarID: myCal.ID, ProviderEventID: "pe1", Title: "a", Start: now, End: now.Add(time.Hour), Status: domain.EventConfirmed})
	e2, _ := st.Events().Upsert(ctx, domain.Event{CalendarID: theirCal.ID, ProviderEventID: "pe2", Title: "b", Start: now, End: now.Add(time.Hour), Status: domain.EventConfirmed})
	if _, err := st.EventNotes().Upsert(ctx, domain.EventNote{EventID: e1.ID, UserID: "u1", BodyMD: "mine", Links: []string{"https://x.test"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EventNotes().Upsert(ctx, domain.EventNote{EventID: e2.ID, UserID: "u2", BodyMD: "theirs"}); err != nil {
		t.Fatal(err)
	}
	notes, err := st.EventNotes().ListByUser(ctx, "u1")
	if err != nil || len(notes) != 1 || notes[0].BodyMD != "mine" || len(notes[0].Links) != 1 {
		t.Fatalf("ListByUser = %+v err=%v, want the one u1 note with its link", notes, err)
	}
}
```

Add `"errors"`, `"fmt"` and `"sort"` to the file's import block.

- [ ] **Step 2: Run and confirm the failure.** `cd backend && REQUIRE_DOCKER=1 go test ./internal/adapter/out/postgres/ -run 'TestUserRepoDelete|TestTeamRepoListMemberships|TestThreadRepoListByAccountPage|TestEventRepoListByUserPage|TestEventNoteRepoListByUser'` — expected: compile errors `st.Users().Delete undefined`, `teams.ListMemberships undefined`, `ListByAccountPage undefined`, `ListByUserPage undefined`, `st.EventNotes().ListByUser undefined`.

- [ ] **Step 3: Append to `backend/internal/adapter/out/postgres/user.go` (after `GetByID`):**

```go
// Delete removes the users row; every owned table follows by FK cascade
// (migration 0029 closes the two historical gaps). domain.ErrNotFound when
// the row is already gone.
func (r userRepo) Delete(ctx context.Context, id string) error {
	return mustAffect(r.q(ctx).ExecContext(ctx, `DELETE FROM users WHERE id = $1`, id))
}
```

- [ ] **Step 4: Update `backend/internal/adapter/out/postgres/team.go`.** Change `teamCols` to:

```go
// created_by is NULL once the creator's account was deleted (0029); scan it
// as '' so domain.Team.CreatedBy stays a plain string.
const teamCols = `id, name, COALESCE(created_by, ''), created_at`
```

In `ListByUser`, change the select list `SELECT t.id, t.name, t.created_by, t.created_at` to `SELECT t.id, t.name, COALESCE(t.created_by, ''), t.created_at`. Append after `CountByRole`:

```go
// ListMemberships returns every team_members row for userID — the
// account-deletion ownership check walks these and ListMembers per team.
func (r *TeamRepo) ListMemberships(ctx context.Context, userID string) ([]domain.TeamMember, error) {
	rows, err := r.s.q(ctx).QueryContext(ctx,
		`SELECT `+teamMemberCols+` FROM team_members WHERE user_id = $1 ORDER BY joined_at, team_id`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	members := []domain.TeamMember{}
	for rows.Next() {
		m, err := scanTeamMember(rows)
		if err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}
```

- [ ] **Step 5: Append to `backend/internal/adapter/out/postgres/mail.go` (after `ListInboxBefore`, before `collectThreads`):**

```go
// ListByAccountPage is the export pager: one account's threads in id order,
// keyset on id so a 10-minute export never re-reads or skips a row.
func (r threadRepo) ListByAccountPage(ctx context.Context, accountID, afterID string, limit int) ([]domain.Thread, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.q(ctx).QueryContext(ctx, `
		SELECT `+threadCols+`
		FROM threads t
		WHERE t.account_id = $1 AND t.id > $2
		ORDER BY t.id LIMIT $3`, accountID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return collectThreads(rows)
}
```

- [ ] **Step 6: Append to `backend/internal/adapter/out/postgres/calendar.go` (after `ListInRange`):**

```go
// ListByUserPage is the export pager: every event on the user's calendars
// (visible or not) in id order, keyset on id.
func (r eventRepo) ListByUserPage(ctx context.Context, userID, afterID string, limit int) ([]domain.Event, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := r.q(ctx).QueryContext(ctx, `
		SELECT `+eventCols+`
		FROM events e
		JOIN calendars c ON c.id = e.calendar_id
		JOIN connected_accounts ca ON ca.id = c.account_id
		WHERE ca.user_id = $1 AND e.id > $2
		ORDER BY e.id LIMIT $3`, userID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return collectEvents(rows)
}
```

- [ ] **Step 7: Append to `backend/internal/adapter/out/postgres/event_note.go`:**

```go
// ListByUser returns every note the user wrote, ordered by event id (the
// data export's event-notes.json).
func (r eventNoteRepo) ListByUser(ctx context.Context, userID string) ([]domain.EventNote, error) {
	rows, err := r.q(ctx).QueryContext(ctx, `
		SELECT event_id, user_id, body_md, links, updated_at
		FROM event_notes WHERE user_id = $1 ORDER BY event_id`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	notes := []domain.EventNote{}
	for rows.Next() {
		var n domain.EventNote
		var linksJSON []byte
		if err := rows.Scan(&n.EventID, &n.UserID, &n.BodyMD, &linksJSON, &n.UpdatedAt); err != nil {
			return nil, err
		}
		if err := unmarshalInto(linksJSON, &n.Links); err != nil {
			return nil, err
		}
		if n.Links == nil {
			n.Links = []string{}
		}
		notes = append(notes, n)
	}
	return notes, rows.Err()
}
```

- [ ] **Step 8: Run the whole Postgres suite and the build.** `cd backend && go build ./... && REQUIRE_DOCKER=1 go test ./internal/adapter/out/postgres/...` — expected: build `ok`; tests `ok` (every Task 2 and Task 3 test passes, the migration applies on a fresh container).

- [ ] **Step 9: Commit.**

```bash
git add backend/internal/adapter/out/postgres
git commit -m "feat(postgres): Users.Delete, Teams.ListMemberships + NULL creator, thread/event keyset pagers, EventNotes.ListByUser" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 4: Cascade coverage — structural FK policy test + seeded purge test (Track A)

**Files:**
- Modify: `backend/internal/adapter/out/postgres/lifecycle_test.go` (append)

**Interfaces:**
- Consumes: every repo accessor on `*Store`, `NewTeamRepo`, `NewDelegationRepo`.
- Produces: `TestUsersFKDeletePolicy` (fails for any future migration that references `users` without `CASCADE`/`SET NULL`, or adds a `user_id`-style column with no FK) and `TestPurgeCascadeCoverage`.

- [ ] **Step 1: Append the tests to `backend/internal/adapter/out/postgres/lifecycle_test.go`:**

```go
// TestUsersFKDeletePolicy pins the cascade audit structurally: every foreign
// key that references users(id) must delete-cascade or set-null, and every
// user-pointing column name from the audit must actually carry such a FK. A
// future migration that forgets ON DELETE CASCADE fails here, not in prod.
func TestUsersFKDeletePolicy(t *testing.T) {
	_, db := newTestStore(t)
	ctx := context.Background()

	rows, err := db.QueryContext(ctx, `
		SELECT c.conrelid::regclass::text, a.attname, c.confdeltype
		FROM pg_constraint c
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
		WHERE c.contype = 'f' AND c.confrelid = 'users'::regclass`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	policy := map[string]string{} // "table.column" -> confdeltype
	for rows.Next() {
		var table, column, deltype string
		if err := rows.Scan(&table, &column, &deltype); err != nil {
			t.Fatal(err)
		}
		policy[table+"."+column] = deltype
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for ref, deltype := range policy {
		if deltype != "c" && deltype != "n" {
			t.Errorf("%s references users with delete action %q, want CASCADE (c) or SET NULL (n)", ref, deltype)
		}
	}
	if policy["teams.created_by"] != "n" {
		t.Errorf("teams.created_by delete action = %q, want SET NULL (n)", policy["teams.created_by"])
	}
	if policy["user_preferences.user_id"] != "c" {
		t.Errorf("user_preferences.user_id delete action = %q, want CASCADE (c)", policy["user_preferences.user_id"])
	}

	// Every user-pointing column name used anywhere in the schema must be
	// covered by one of those FKs (the orphan class user_preferences was in).
	cols, err := db.QueryContext(ctx, `
		SELECT table_name, column_name FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND column_name IN ('user_id', 'principal_id', 'assistant_id', 'grantee_user_id',
		                      'actor_id', 'author_id', 'created_by', 'invited_by')`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cols.Close() }()
	for cols.Next() {
		var table, column string
		if err := cols.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		if _, ok := policy[table+"."+column]; !ok {
			t.Errorf("%s.%s names a user but has no foreign key to users(id)", table, column)
		}
	}
	if err := cols.Err(); err != nil {
		t.Fatal(err)
	}
}

// TestPurgeCascadeCoverage seeds u1 across the owned tables (plus u2 sharing
// a team and a delegation), deletes u1 through the repo, and asserts u1's
// rows are gone everywhere, the shared team survives with created_by NULL,
// and u2's rows are intact.
func TestPurgeCascadeCoverage(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	// --- u1's rows ---
	must(st.Subscriptions().Upsert(ctx, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive}))
	acct := seedAccount(t, st, "u1")
	must(st.OAuthStates().Create(ctx, port.OAuthState{State: "st1", UserID: "u1", Provider: domain.ProviderGoogle, ExpiresAt: now.Add(time.Hour)}))
	_, err := st.Labels().Upsert(ctx, domain.Label{AccountID: acct.ID, ProviderLabelID: "L1", Name: "Work", Kind: domain.LabelKindUser})
	must(err)
	th := seedThread(t, st, acct.ID, now)
	_, err = st.Messages().Upsert(ctx, domain.Message{
		ThreadID: th.ID, AccountID: acct.ID, ProviderMessageID: "pm1",
		From: domain.EmailAddress{Email: "a@example.com"}, Subject: "hi", SentAt: now,
	})
	must(err)
	_, err = st.Drafts().Create(ctx, domain.Draft{AccountID: acct.ID, Subject: "draft"})
	must(err)
	_, err = st.Snippets().Create(ctx, domain.Snippet{UserID: "u1", Name: "s", BodyHTML: "<p>x</p>"})
	must(err)
	cal := seedCalendar(t, st, acct.ID)
	ev, err := st.Events().Upsert(ctx, domain.Event{CalendarID: cal.ID, ProviderEventID: "pe1", Title: "e", Start: now, End: now.Add(time.Hour), Status: domain.EventConfirmed})
	must(err)
	_, err = st.Devices().Upsert(ctx, domain.NotificationDevice{UserID: "u1", Platform: domain.PlatformIOS, Token: "tok-u1"})
	must(err)
	must(st.Prefs().Save(ctx, "u1", domain.UserPrefs{}))
	must(st.UserPreferences().Put(ctx, "u1", port.UserPreferences{Theme: "ocean"}))
	must(st.UserSettings().Upsert(ctx, domain.UserSettings{UserID: "u1", TimeZone: "UTC", WorkingHours: []domain.AvailabilityWindow{}}))
	_, err = st.EventNotes().Upsert(ctx, domain.EventNote{EventID: ev.ID, UserID: "u1", BodyMD: "note"})
	must(err)
	_, err = st.Tasks().Create(ctx, domain.Task{UserID: "u1", Title: "task", Source: domain.TaskSourceLocal})
	must(err)
	_, err = st.Classifiers().Create(ctx, domain.AiClassifier{UserID: "u1", Name: "rule", Prompt: "p", LabelName: "L", Enabled: true})
	must(err)
	_, err = st.AiUsage().IncrementAndCheck(ctx, "u1", now, 10)
	must(err)
	_, err = st.BookingLinks().Create(ctx, domain.BookingLink{UserID: "u1", Slug: "u1-call", Title: "Call", CalendarID: cal.ID, DurationMinutes: 30, TimeZone: "UTC"})
	must(err)
	must(st.CalendarPrefs().Upsert(ctx, domain.DefaultCalendarPrefs("u1")))
	_, err = st.EventTemplates().Create(ctx, "u1", domain.EventTemplate{Name: "t", Title: "t", DurationMinutes: 30})
	must(err)
	_, err = st.CalendarSets().Create(ctx, "u1", domain.CalendarSet{Name: "set"})
	must(err)
	must(st.SyncStates().Save(ctx, port.SyncState{AccountID: acct.ID, Resource: "mail", Cursor: "c1"}))
	ok, _, err := st.UserExports().Claim(ctx, "u1", now, time.Hour)
	must(err)
	if !ok {
		t.Fatal("export claim refused on a fresh user")
	}

	// --- shared with u2 ---
	teams := NewTeamRepo(st)
	shared, err := teams.Create(ctx, domain.Team{Name: "Shared", CreatedBy: "u1"}, domain.TeamMember{UserID: "u1", Role: domain.TeamRoleOwner})
	must(err)
	must(teams.UpsertMember(ctx, domain.TeamMember{TeamID: shared.ID, UserID: "u2", Role: domain.TeamRoleOwner}))
	_, err = NewDelegationRepo(st).Create(ctx, domain.Delegation{PrincipalID: "u1", AssistantID: "u2", Scopes: []domain.DelegationScope{domain.ScopeMailRead}, Status: domain.DelegationActive})
	must(err)
	// --- u2's own rows (must survive) ---
	theirAcct := seedAccount(t, st, "u2")
	_, err = st.Snippets().Create(ctx, domain.Snippet{UserID: "u2", Name: "theirs", BodyHTML: "<p>y</p>"})
	must(err)

	if err := st.Users().Delete(ctx, "u1"); err != nil {
		t.Fatalf("Users.Delete: %v", err)
	}

	owned := []struct{ table, column, value string }{
		{"users", "id", "u1"}, {"subscriptions", "user_id", "u1"}, {"connected_accounts", "user_id", "u1"},
		{"oauth_states", "user_id", "u1"}, {"labels", "account_id", acct.ID}, {"threads", "account_id", acct.ID},
		{"messages", "account_id", acct.ID}, {"drafts", "account_id", acct.ID}, {"snippets", "user_id", "u1"},
		{"calendars", "account_id", acct.ID}, {"events", "calendar_id", cal.ID}, {"devices", "user_id", "u1"},
		{"user_prefs", "user_id", "u1"}, {"user_preferences", "user_id", "u1"}, {"user_settings", "user_id", "u1"},
		{"event_notes", "user_id", "u1"}, {"tasks", "user_id", "u1"}, {"ai_classifiers", "user_id", "u1"},
		{"ai_usage", "user_id", "u1"}, {"booking_links", "user_id", "u1"}, {"calendar_prefs", "user_id", "u1"},
		{"event_templates", "user_id", "u1"}, {"calendar_sets", "user_id", "u1"}, {"sync_state", "account_id", acct.ID},
		{"user_exports", "user_id", "u1"}, {"team_members", "user_id", "u1"}, {"delegations", "principal_id", "u1"},
	}
	for _, o := range owned {
		var n int
		if err := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s = $1`, o.table, o.column), o.value).Scan(&n); err != nil {
			t.Fatalf("count %s.%s: %v", o.table, o.column, err)
		}
		if n != 0 {
			t.Errorf("%s still has %d row(s) for the deleted user", o.table, n)
		}
	}
	var createdBy sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT created_by FROM teams WHERE id = $1`, shared.ID).Scan(&createdBy); err != nil {
		t.Fatalf("shared team must survive: %v", err)
	}
	if createdBy.Valid {
		t.Fatalf("shared team created_by = %q, want NULL", createdBy.String)
	}
	if _, err := teams.GetMember(ctx, shared.ID, "u2"); err != nil {
		t.Fatalf("u2's membership must survive: %v", err)
	}
	if _, err := st.Accounts().GetByID(ctx, theirAcct.ID); err != nil {
		t.Fatalf("u2's account must survive: %v", err)
	}
	theirs, err := st.Snippets().ListByUser(ctx, "u2")
	if err != nil || len(theirs) != 1 {
		t.Fatalf("u2's snippets = %v err=%v, want 1", theirs, err)
	}
}
```

- [ ] **Step 2: Run them.** `cd backend && REQUIRE_DOCKER=1 go test ./internal/adapter/out/postgres/ -run 'TestUsersFKDeletePolicy|TestPurgeCascadeCoverage' -v` — expected: PASS for both (if `TestUsersFKDeletePolicy` reports a column, that is a real schema gap: fix it in migration 0029 before continuing — the audit in the spec says none remain).

- [ ] **Step 3: Run the full Postgres suite once more.** `cd backend && REQUIRE_DOCKER=1 go test ./internal/adapter/out/postgres/...` — expected: `ok`.

- [ ] **Step 4: Commit.**

```bash
git add backend/internal/adapter/out/postgres/lifecycle_test.go
git commit -m "test(postgres): pin users FK delete policy and seeded purge cascade coverage" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 5: Service fakes — widen the shared in-memory fakes for the new ports (Track B)

**Files:**
- Modify: `backend/internal/service/fakes_test.go` (`fakeUserRepo` L49–74; `fakeThreadRepo` L201–240; `fakeEventRepo` L851–900; `fakeUserSettingsRepo` L2358–2387; `fakeTeamRepo` L2460–2568; append `fakeUserExportRepo` after `fakeUserSettingsRepo`)
- Modify: `backend/internal/service/calendar_test.go` (`fakeEventNoteRepo` L104–124)

**Interfaces:**
- Consumes: Task 1 ports.
- Produces: `(*fakeUserRepo) Delete`; `(*fakeTeamRepo) ListMemberships`; `(*fakeThreadRepo) ListByAccountPage`; `(*fakeEventRepo) ListByUserPage` (scoped through `calendars`/`accounts` when both set, like `ListInRange`); `(*fakeUserSettingsRepo) SetAIBackground` (+ `Get` defaults `AIBackground: true`, `Upsert` preserves it); `(*fakeEventNoteRepo) ListByUser`; `fakeUserExportRepo{startedAt map[string]time.Time; err error}` + `newUserExportRepo()`.

- [ ] **Step 1: Confirm the package no longer compiles its tests.** `cd backend && go vet ./internal/service/` — expected: `*fakeUserRepo does not implement port.UserRepo (missing method Delete)`, same for `fakeTeamRepo` (`ListMemberships`), `fakeThreadRepo` (`ListByAccountPage`), `fakeEventRepo` (`ListByUserPage`), `fakeUserSettingsRepo` (`SetAIBackground`), `fakeEventNoteRepo` (`ListByUser`).

- [ ] **Step 2: Add the user/team/thread/event methods to `backend/internal/service/fakes_test.go`.** After `(*fakeUserRepo) GetByID`:

```go
func (r *fakeUserRepo) Delete(_ context.Context, id string) error {
	if _, ok := r.byID[id]; !ok {
		return domain.ErrNotFound
	}
	delete(r.byID, id)
	return nil
}
```

After `(*fakeTeamRepo) CountByRole`:

```go
func (r *fakeTeamRepo) ListMemberships(_ context.Context, userID string) ([]domain.TeamMember, error) {
	out := []domain.TeamMember{}
	for _, members := range r.members {
		if m, ok := members[userID]; ok {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TeamID < out[j].TeamID })
	return out, nil
}
```

After `(*fakeThreadRepo) Upsert`:

```go
// ListByAccountPage mirrors the SQL keyset pager: id > afterID, ascending,
// capped at limit.
func (r *fakeThreadRepo) ListByAccountPage(_ context.Context, accountID, afterID string, limit int) ([]domain.Thread, error) {
	out := []domain.Thread{}
	for _, t := range r.byID {
		if t.AccountID == accountID && t.ID > afterID {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
```

After `(*fakeEventRepo) GetByProviderID`:

```go
// ListByUserPage mirrors the SQL keyset pager. Ownership is resolved event
// → calendar → account → user when both calendars and accounts are wired
// (same convention as ListInRange); otherwise every event is the user's.
func (r *fakeEventRepo) ListByUserPage(_ context.Context, userID, afterID string, limit int) ([]domain.Event, error) {
	out := []domain.Event{}
	for _, id := range r.order {
		e, ok := r.byID[id]
		if !ok || e.ID <= afterID {
			continue
		}
		if r.calendars != nil && r.accounts != nil {
			cal, ok := r.calendars.byID[e.CalendarID]
			if !ok {
				continue
			}
			acct, ok := r.accounts.byID[cal.AccountID]
			if !ok || acct.UserID != userID {
				continue
			}
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
```

- [ ] **Step 3: Replace the `fakeUserSettingsRepo` block (type, constructor, `Get`, `Upsert`, assertion) with:**

```go
// fakeUserSettingsRepo mirrors the real adapter: Get returns a zero-value
// UserSettings with TimeZone "UTC" and AIBackground true when no row exists;
// Upsert never writes AIBackground (new rows default true, existing rows keep
// theirs); SetAIBackground writes only the switch.
type fakeUserSettingsRepo struct {
	byUser map[string]domain.UserSettings
	getErr error
}

func newUserSettingsRepo() *fakeUserSettingsRepo {
	return &fakeUserSettingsRepo{byUser: map[string]domain.UserSettings{}}
}

func (r *fakeUserSettingsRepo) Get(_ context.Context, userID string) (domain.UserSettings, error) {
	if r.getErr != nil {
		return domain.UserSettings{}, r.getErr
	}
	s, ok := r.byUser[userID]
	if !ok {
		return domain.UserSettings{UserID: userID, TimeZone: "UTC", AIBackground: true}, nil
	}
	return s, nil
}

func (r *fakeUserSettingsRepo) Upsert(_ context.Context, s domain.UserSettings) error {
	if prev, ok := r.byUser[s.UserID]; ok {
		s.AIBackground = prev.AIBackground
	} else {
		s.AIBackground = true
	}
	r.byUser[s.UserID] = s
	return nil
}

func (r *fakeUserSettingsRepo) SetAIBackground(_ context.Context, userID string, on bool) error {
	s, ok := r.byUser[userID]
	if !ok {
		s = domain.UserSettings{UserID: userID, TimeZone: "UTC", WorkingHours: []domain.AvailabilityWindow{}}
	}
	s.AIBackground = on
	r.byUser[userID] = s
	return nil
}

var _ port.UserSettingsRepo = (*fakeUserSettingsRepo)(nil)

// --- user export repo --------------------------------------------------------

// fakeUserExportRepo mirrors the SQL claim: refused inside window with the
// earliest retry time, otherwise stamped with now.
type fakeUserExportRepo struct {
	startedAt map[string]time.Time
	err       error
}

func newUserExportRepo() *fakeUserExportRepo {
	return &fakeUserExportRepo{startedAt: map[string]time.Time{}}
}

func (r *fakeUserExportRepo) Claim(_ context.Context, userID string, now time.Time, window time.Duration) (bool, time.Time, error) {
	if r.err != nil {
		return false, time.Time{}, r.err
	}
	if prev, ok := r.startedAt[userID]; ok && now.Before(prev.Add(window)) {
		return false, prev.Add(window), nil
	}
	r.startedAt[userID] = now
	return true, time.Time{}, nil
}

var _ port.UserExportRepo = (*fakeUserExportRepo)(nil)
```

- [ ] **Step 4: Add `ListByUser` to `fakeEventNoteRepo` in `backend/internal/service/calendar_test.go`** (after its `GetByEventID`):

```go
func (r *fakeEventNoteRepo) ListByUser(_ context.Context, userID string) ([]domain.EventNote, error) {
	out := []domain.EventNote{}
	for _, n := range r.byEvent {
		if n.UserID == userID {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EventID < out[j].EventID })
	return out, nil
}
```

(add `"sort"` to that file's imports if absent).

- [ ] **Step 5: Run the service suite.** `cd backend && go vet ./internal/service/ && go test ./internal/service/` — expected: `ok` (existing settings tests still pass: `Get` without a row now also reports `AIBackground: true`, which no existing assertion compares).

- [ ] **Step 6: Commit.**

```bash
git add backend/internal/service/fakes_test.go backend/internal/service/calendar_test.go
git commit -m "test(service): widen in-memory fakes for lifecycle/export ports" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 6: `UserLifecycleService.Purge` (Track B)

**Files:**
- Create: `backend/internal/service/lifecycle.go`
- Create: `backend/internal/service/lifecycle_test.go`

**Interfaces:**
- Consumes: `port.UserRepo.Delete`, `port.TeamRepo.{ListMemberships,ListMembers,GetByID,Delete}`, `port.DelegationRepo.{ListByUser,Update}`, `port.SubscriptionRepo.GetByUserID`, `port.Payments.CancelSubscription(ctx, id string, immediately bool) error` (piece 1), `port.TxRunner`, `port.Clock`, `domain.Subscription.BillingSubscriptionID` + status consts (piece 1), `domain.ErrBillingUnavailable` (piece 1), `*domain.OwnsTeamsError`, `port.PurgeReport`.
- Produces: `service.UserLifecycleDeps{Users, Teams, Delegations, Subscriptions, Payments, Tx, Clock, Logger}`; `service.NewUserLifecycleService(d UserLifecycleDeps) *UserLifecycleService`; `(*UserLifecycleService) Purge(ctx, userID string) (port.PurgeReport, error)`; unexported `subscriptionNeedsCancel(domain.Subscription) bool`.

- [ ] **Step 1: Write the failing tests** at `backend/internal/service/lifecycle_test.go`:

```go
package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- local fakes (lifecycle-prefixed; the shared fakes carry no call log) ---

type lifecycleCancel struct {
	ID          string
	Immediately bool
}

// lifecyclePayments embeds the port so only CancelSubscription is
// implemented; Purge never calls anything else (a nil embed would panic).
type lifecyclePayments struct {
	port.Payments
	cancelCalls []lifecycleCancel
	cancelErr   error
	log         *[]string
}

func (p *lifecyclePayments) CancelSubscription(_ context.Context, id string, immediately bool) error {
	p.cancelCalls = append(p.cancelCalls, lifecycleCancel{ID: id, Immediately: immediately})
	*p.log = append(*p.log, "cancel:"+id)
	return p.cancelErr
}

type loggingUserRepo struct {
	*fakeUserRepo
	log *[]string
}

func (r *loggingUserRepo) Delete(ctx context.Context, id string) error {
	*r.log = append(*r.log, "users.delete:"+id)
	return r.fakeUserRepo.Delete(ctx, id)
}

type loggingTeamRepo struct {
	*fakeTeamRepo
	log *[]string
}

func (r *loggingTeamRepo) ListMembers(ctx context.Context, teamID string) ([]domain.TeamMember, error) {
	*r.log = append(*r.log, "teams.listMembers:"+teamID)
	return r.fakeTeamRepo.ListMembers(ctx, teamID)
}

func (r *loggingTeamRepo) Delete(ctx context.Context, id string) error {
	*r.log = append(*r.log, "teams.delete:"+id)
	return r.fakeTeamRepo.Delete(ctx, id)
}

type lifecycleFixture struct {
	svc      *UserLifecycleService
	users    *loggingUserRepo
	teams    *loggingTeamRepo
	delegs   *delegRepoFake
	subs     *fakeSubscriptionRepo
	payments *lifecyclePayments
	tx       *fakeTxRunner
	log      []string
}

var lifecycleNow = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

func newLifecycleFixture(t *testing.T) *lifecycleFixture {
	t.Helper()
	f := &lifecycleFixture{}
	f.users = &loggingUserRepo{fakeUserRepo: newUserRepo(), log: &f.log}
	f.teams = &loggingTeamRepo{fakeTeamRepo: newTeamRepo(), log: &f.log}
	f.delegs = newDelegRepoFake()
	f.subs = newSubscriptionRepo()
	f.payments = &lifecyclePayments{log: &f.log}
	f.tx = newTxRunner()
	f.svc = NewUserLifecycleService(UserLifecycleDeps{
		Users: f.users, Teams: f.teams, Delegations: f.delegs, Subscriptions: f.subs,
		Payments: f.payments, Tx: f.tx, Clock: newClock(lifecycleNow),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	for _, id := range []string{"u1", "u2", "u3"} {
		if _, err := f.users.Upsert(context.Background(), domain.User{ID: id, Email: id + "@example.com"}); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// addTeam creates id/name with members[0] as the creating owner and upserts
// the rest.
func (f *lifecycleFixture) addTeam(t *testing.T, id, name string, members ...domain.TeamMember) {
	t.Helper()
	ctx := context.Background()
	first := members[0]
	first.TeamID = id
	if _, err := f.teams.Create(ctx, domain.Team{ID: id, Name: name, CreatedBy: first.UserID}, first); err != nil {
		t.Fatal(err)
	}
	for _, m := range members[1:] {
		m.TeamID = id
		if err := f.teams.UpsertMember(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
}

func owner(userID string) domain.TeamMember {
	return domain.TeamMember{UserID: userID, Role: domain.TeamRoleOwner}
}

func member(userID string) domain.TeamMember {
	return domain.TeamMember{UserID: userID, Role: domain.TeamRoleMember}
}

func indexOf(log []string, entry string) int {
	for i, e := range log {
		if e == entry {
			return i
		}
	}
	return -1
}

func firstIndexWithPrefix(log []string, prefix string) int {
	for i, e := range log {
		if strings.HasPrefix(e, prefix) {
			return i
		}
	}
	return -1
}

func TestPurgeOrder(t *testing.T) {
	f := newLifecycleFixture(t)
	ctx := context.Background()
	f.addTeam(t, "shared", "Shared", owner("u1"), owner("u2"))
	if err := f.subs.Upsert(ctx, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive, BillingSubscriptionID: "sub_1"}); err != nil {
		t.Fatal(err)
	}

	report, err := f.svc.Purge(ctx, "u1")
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if len(f.payments.cancelCalls) != 1 || f.payments.cancelCalls[0] != (lifecycleCancel{ID: "sub_1", Immediately: true}) {
		t.Fatalf("cancel calls = %+v, want exactly one (sub_1, immediately=true)", f.payments.cancelCalls)
	}
	check := indexOf(f.log, "teams.listMembers:shared")
	cancel := indexOf(f.log, "cancel:sub_1")
	del := indexOf(f.log, "users.delete:u1")
	if check < 0 || cancel < 0 || del < 0 || !(check < cancel && cancel < del) {
		t.Fatalf("order = %v, want team check < cancel < delete", f.log)
	}
	if f.tx.calls != 1 {
		t.Fatalf("tx calls = %d, want 1", f.tx.calls)
	}
	if !report.SubscriptionCanceled || report.TeamsDeleted != 0 {
		t.Fatalf("report = %+v", report)
	}
	if _, err := f.users.GetByID(ctx, "u1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("user still present after purge: %v", err)
	}
	if _, err := f.teams.GetByID(ctx, "shared"); err != nil {
		t.Fatalf("co-owned team must survive: %v", err)
	}
}

func TestPurgePaddleFailureAborts(t *testing.T) {
	f := newLifecycleFixture(t)
	ctx := context.Background()
	f.payments.cancelErr = errors.New("paddle: 503")
	if err := f.subs.Upsert(ctx, domain.Subscription{UserID: "u1", Status: domain.SubscriptionPastDue, BillingSubscriptionID: "sub_1"}); err != nil {
		t.Fatal(err)
	}
	d, err := f.delegs.Create(ctx, domain.Delegation{PrincipalID: "u1", AssistantID: "u2", Scopes: []domain.DelegationScope{domain.ScopeMailRead}, Status: domain.DelegationActive})
	if err != nil {
		t.Fatal(err)
	}

	_, err = f.svc.Purge(ctx, "u1")
	if !errors.Is(err, domain.ErrBillingUnavailable) {
		t.Fatalf("err = %v, want ErrBillingUnavailable", err)
	}
	if f.tx.calls != 0 {
		t.Fatal("no transaction may start when the cancel fails")
	}
	if i := firstIndexWithPrefix(f.log, "users.delete"); i >= 0 {
		t.Fatalf("rows were deleted after a cancel failure: %v", f.log)
	}
	if _, err := f.users.GetByID(ctx, "u1"); err != nil {
		t.Fatalf("user must remain: %v", err)
	}
	got, _ := f.delegs.GetByID(ctx, d.ID)
	if got.Status != domain.DelegationActive {
		t.Fatalf("delegation status = %q, want active (untouched)", got.Status)
	}
}

func TestPurgeSkipsCancelWithoutLiveSubscription(t *testing.T) {
	cases := []struct {
		name string
		sub  *domain.Subscription
	}{
		{"no row", nil},
		{"status none with id", &domain.Subscription{Status: domain.SubscriptionNone, BillingSubscriptionID: "sub_x"}},
		{"canceled with id", &domain.Subscription{Status: domain.SubscriptionCanceled, BillingSubscriptionID: "sub_x"}},
		{"active without id", &domain.Subscription{Status: domain.SubscriptionActive}},
		{"trialing without id", &domain.Subscription{Status: domain.SubscriptionTrialing}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newLifecycleFixture(t)
			ctx := context.Background()
			if tc.sub != nil {
				s := *tc.sub
				s.UserID = "u1"
				if err := f.subs.Upsert(ctx, s); err != nil {
					t.Fatal(err)
				}
			}
			report, err := f.svc.Purge(ctx, "u1")
			if err != nil {
				t.Fatalf("Purge: %v", err)
			}
			if len(f.payments.cancelCalls) != 0 || report.SubscriptionCanceled {
				t.Fatalf("cancel must be skipped: calls=%v report=%+v", f.payments.cancelCalls, report)
			}
			if _, err := f.users.GetByID(ctx, "u1"); !errors.Is(err, domain.ErrNotFound) {
				t.Fatal("user must still be purged")
			}
		})
	}
}

func TestPurgeCancelsTrialingWithSubscriptionID(t *testing.T) {
	f := newLifecycleFixture(t)
	ctx := context.Background()
	if err := f.subs.Upsert(ctx, domain.Subscription{UserID: "u1", Status: domain.SubscriptionTrialing, BillingSubscriptionID: "sub_t"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Purge(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if len(f.payments.cancelCalls) != 1 || f.payments.cancelCalls[0].ID != "sub_t" {
		t.Fatalf("cancel calls = %+v, want sub_t", f.payments.cancelCalls)
	}
}

func TestPurgeOwnsTeamsRefused(t *testing.T) {
	f := newLifecycleFixture(t)
	ctx := context.Background()
	f.addTeam(t, "design", "Design", owner("u1"), member("u2"))
	if err := f.subs.Upsert(ctx, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive, BillingSubscriptionID: "sub_1"}); err != nil {
		t.Fatal(err)
	}

	_, err := f.svc.Purge(ctx, "u1")
	if !errors.Is(err, domain.ErrOwnsTeams) {
		t.Fatalf("err = %v, want ErrOwnsTeams", err)
	}
	var typed *domain.OwnsTeamsError
	if !errors.As(err, &typed) || len(typed.Teams) != 1 || typed.Teams[0] != (domain.TeamRef{ID: "design", Name: "Design"}) {
		t.Fatalf("owns-teams payload = %+v", typed)
	}
	if len(f.payments.cancelCalls) != 0 {
		t.Fatal("the team check must run before the subscription cancel")
	}
	if f.tx.calls != 0 {
		t.Fatal("nothing may be deleted")
	}
	if _, err := f.users.GetByID(ctx, "u1"); err != nil {
		t.Fatalf("user must remain: %v", err)
	}
}

func TestPurgeCoOwnerAndPlainMemberProceed(t *testing.T) {
	for _, role := range []domain.TeamMember{owner("u1"), member("u1")} {
		t.Run(string(role.Role), func(t *testing.T) {
			f := newLifecycleFixture(t)
			ctx := context.Background()
			f.addTeam(t, "shared", "Shared", owner("u2"), role)
			if _, err := f.svc.Purge(ctx, "u1"); err != nil {
				t.Fatalf("Purge as %s: %v", role.Role, err)
			}
			if _, err := f.teams.GetByID(ctx, "shared"); err != nil {
				t.Fatalf("team must survive: %v", err)
			}
			if _, err := f.users.GetByID(ctx, "u1"); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("user must be purged: %v", err)
			}
			// The membership row itself goes by SQL cascade from users.Delete —
			// proven in postgres TestPurgeCascadeCoverage, not by the fake.
		})
	}
}

// Review Focus 5: a sole-member team plus a blocking team → refusal lists only
// the blocking team and the sole-member team is untouched.
func TestPurgeOwnsTeamsRefusedKeepsSoleMemberTeams(t *testing.T) {
	f := newLifecycleFixture(t)
	ctx := context.Background()
	f.addTeam(t, "solo", "Solo", owner("u1"))
	f.addTeam(t, "design", "Design", owner("u1"), member("u2"))

	_, err := f.svc.Purge(ctx, "u1")
	var typed *domain.OwnsTeamsError
	if !errors.As(err, &typed) || len(typed.Teams) != 1 || typed.Teams[0].ID != "design" {
		t.Fatalf("err = %v, want owns_teams listing only design", err)
	}
	if _, err := f.teams.GetByID(ctx, "solo"); err != nil {
		t.Fatalf("sole-member team must survive a refused purge: %v", err)
	}
	if i := firstIndexWithPrefix(f.log, "teams.delete"); i >= 0 {
		t.Fatalf("no team may be deleted on refusal: %v", f.log)
	}
}

func TestPurgeDeletesSoleMemberTeams(t *testing.T) {
	f := newLifecycleFixture(t)
	ctx := context.Background()
	f.addTeam(t, "solo", "Solo", owner("u1"))
	f.addTeam(t, "shared", "Shared", owner("u2"), member("u1"))

	report, err := f.svc.Purge(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if report.TeamsDeleted != 1 {
		t.Fatalf("TeamsDeleted = %d, want 1", report.TeamsDeleted)
	}
	if _, err := f.teams.GetByID(ctx, "solo"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("solo team must be deleted: %v", err)
	}
	if _, err := f.teams.GetMember(ctx, "shared", "u2"); err != nil {
		t.Fatalf("u2's membership of the shared team must survive: %v", err)
	}
	if del, user := indexOf(f.log, "teams.delete:solo"), indexOf(f.log, "users.delete:u1"); del < 0 || user < 0 || del > user {
		t.Fatalf("teams must be deleted before the users row: %v", f.log)
	}
}

func TestPurgeRevokesDelegationsBothWays(t *testing.T) {
	f := newLifecycleFixture(t)
	ctx := context.Background()
	asPrincipal, err := f.delegs.Create(ctx, domain.Delegation{PrincipalID: "u1", AssistantID: "u2", Scopes: []domain.DelegationScope{domain.ScopeMailRead}, Status: domain.DelegationActive})
	if err != nil {
		t.Fatal(err)
	}
	asAssistant, err := f.delegs.Create(ctx, domain.Delegation{PrincipalID: "u3", AssistantID: "u1", Scopes: []domain.DelegationScope{domain.ScopeCalendarRead}, Status: domain.DelegationPending})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Purge(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{asPrincipal.ID, asAssistant.ID} {
		got, err := f.delegs.GetByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != domain.DelegationRevoked || got.RevokedAt == nil || !got.RevokedAt.Equal(lifecycleNow) {
			t.Fatalf("delegation %s = %+v, want revoked at %v", id, got, lifecycleNow)
		}
	}
}

func TestPurgeUnknownUserIsNoop(t *testing.T) {
	f := newLifecycleFixture(t)
	report, err := f.svc.Purge(context.Background(), "ghost")
	if err != nil {
		t.Fatalf("Purge(ghost) = %v, want nil (idempotent)", err)
	}
	if report != (port.PurgeReport{}) || f.tx.calls != 0 || len(f.payments.cancelCalls) != 0 || len(f.log) != 0 {
		t.Fatalf("unknown user must touch nothing: report=%+v tx=%d log=%v", report, f.tx.calls, f.log)
	}
}

func TestPurgeSelfHostedWithoutPayments(t *testing.T) {
	f := newLifecycleFixture(t)
	ctx := context.Background()
	f.svc = NewUserLifecycleService(UserLifecycleDeps{
		Users: f.users, Teams: f.teams, Delegations: f.delegs, Subscriptions: f.subs,
		Payments: nil, Tx: f.tx, Clock: newClock(lifecycleNow),
	})
	if err := f.subs.Upsert(ctx, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive, BillingSubscriptionID: "sub_1"}); err != nil {
		t.Fatal(err)
	}
	report, err := f.svc.Purge(ctx, "u1")
	if err != nil || report.SubscriptionCanceled {
		t.Fatalf("self-hosted purge = (%+v, %v), want success with no cancel", report, err)
	}
}

// raceTx promotes u1 to sole owner right before the transaction body runs,
// simulating a co-owner leaving between the pre-check and the tx.
type raceTx struct {
	teams  *loggingTeamRepo
	called int
}

func (r *raceTx) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	r.called++
	_ = r.teams.UpsertMember(ctx, domain.TeamMember{TeamID: "shared", UserID: "u2", Role: domain.TeamRoleMember})
	return fn(ctx)
}

func TestPurgeRaceGuardRefusesInsideTx(t *testing.T) {
	f := newLifecycleFixture(t)
	ctx := context.Background()
	f.addTeam(t, "shared", "Shared", owner("u1"), owner("u2"))
	tx := &raceTx{teams: f.teams}
	f.svc = NewUserLifecycleService(UserLifecycleDeps{
		Users: f.users, Teams: f.teams, Delegations: f.delegs, Subscriptions: f.subs,
		Payments: f.payments, Tx: tx, Clock: newClock(lifecycleNow),
	})
	_, err := f.svc.Purge(ctx, "u1")
	if !errors.Is(err, domain.ErrOwnsTeams) {
		t.Fatalf("err = %v, want ErrOwnsTeams from the in-tx re-check", err)
	}
	if tx.called != 1 {
		t.Fatalf("tx called %d times, want 1", tx.called)
	}
	if _, err := f.users.GetByID(ctx, "u1"); err != nil {
		t.Fatalf("user must remain after the tx rolled back: %v", err)
	}
}
```

- [ ] **Step 2: Run and confirm the failure.** `cd backend && go test ./internal/service/ -run 'TestPurge'` — expected: `undefined: UserLifecycleDeps`, `undefined: NewUserLifecycleService`.

- [ ] **Step 3: Create `backend/internal/service/lifecycle.go`:**

```go
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// UserLifecycleDeps wires the account-deletion service.
type UserLifecycleDeps struct {
	Users         port.UserRepo
	Teams         port.TeamRepo
	Delegations   port.DelegationRepo
	Subscriptions port.SubscriptionRepo
	// Payments cancels a live billing subscription before the rows go; nil
	// on self-hosted instances (no biller), which skips the cancel step.
	Payments port.Payments
	Tx       port.TxRunner
	Clock    port.Clock
	Logger   *slog.Logger // optional; defaults to slog.Default()
}

// UserLifecycleService implements port.UserLifecycleService: account
// deletion behind DELETE /v1/internal/users/{id}. Order: team-ownership
// check → payment-provider cancel (not transactional; a failure aborts
// before anything is deleted; the provider's customer record is retained by
// the merchant of record) → one transaction that re-checks teams, revokes
// delegations both ways, deletes sole-member teams and the users row —
// every other owned table cascades (migration 0029 + TestPurgeCascadeCoverage).
// Provider OAuth tokens die with their rows; there is no provider-side
// revocation.
type UserLifecycleService struct {
	d UserLifecycleDeps
}

var _ port.UserLifecycleService = (*UserLifecycleService)(nil)

func NewUserLifecycleService(d UserLifecycleDeps) *UserLifecycleService {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &UserLifecycleService{d: d}
}

// teamPlan is the outcome of classifying one user's memberships.
type teamPlan struct {
	soleMember []string         // teams the user is the only member of → deleted with the account
	blocking   []domain.TeamRef // teams where the user is the only owner but others remain → refuse
}

func (s *UserLifecycleService) planTeams(ctx context.Context, userID string) (teamPlan, error) {
	var plan teamPlan
	memberships, err := s.d.Teams.ListMemberships(ctx, userID)
	if err != nil {
		return plan, err
	}
	for _, m := range memberships {
		members, err := s.d.Teams.ListMembers(ctx, m.TeamID)
		if err != nil {
			return plan, err
		}
		if len(members) <= 1 {
			plan.soleMember = append(plan.soleMember, m.TeamID)
			continue
		}
		if m.Role != domain.TeamRoleOwner {
			continue
		}
		otherOwners := 0
		for _, other := range members {
			if other.UserID != userID && other.Role == domain.TeamRoleOwner {
				otherOwners++
			}
		}
		if otherOwners > 0 {
			continue
		}
		team, err := s.d.Teams.GetByID(ctx, m.TeamID)
		if err != nil {
			return plan, err
		}
		plan.blocking = append(plan.blocking, domain.TeamRef{ID: team.ID, Name: team.Name})
	}
	return plan, nil
}

// subscriptionNeedsCancel: a provider subscription exists (id present) and
// is in a state the provider would keep billing or could resume.
func subscriptionNeedsCancel(sub domain.Subscription) bool {
	if sub.BillingSubscriptionID == "" {
		return false
	}
	switch sub.Status {
	case domain.SubscriptionActive, domain.SubscriptionPastDue, domain.SubscriptionPaused, domain.SubscriptionTrialing:
		return true
	}
	return false
}

// Purge implements port.UserLifecycleService.
func (s *UserLifecycleService) Purge(ctx context.Context, userID string) (port.PurgeReport, error) {
	var report port.PurgeReport
	if _, err := s.d.Users.GetByID(ctx, userID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return report, nil // already purged (or never provisioned): idempotent
		}
		return report, err
	}
	plan, err := s.planTeams(ctx, userID)
	if err != nil {
		return report, err
	}
	if len(plan.blocking) > 0 {
		return report, &domain.OwnsTeamsError{Teams: plan.blocking}
	}
	if s.d.Payments != nil && s.d.Subscriptions != nil {
		sub, err := s.d.Subscriptions.GetByUserID(ctx, userID)
		switch {
		case errors.Is(err, domain.ErrNotFound):
			// never subscribed
		case err != nil:
			return report, err
		case subscriptionNeedsCancel(sub):
			if err := s.d.Payments.CancelSubscription(ctx, sub.BillingSubscriptionID, true); err != nil {
				return report, fmt.Errorf("%w: cancel subscription: %v", domain.ErrBillingUnavailable, err)
			}
			report.SubscriptionCanceled = true
		}
	}
	err = s.d.Tx.RunInTx(ctx, func(ctx context.Context) error {
		// Race guard: a co-owner may have left between the check and the tx.
		plan, err := s.planTeams(ctx, userID)
		if err != nil {
			return err
		}
		if len(plan.blocking) > 0 {
			return &domain.OwnsTeamsError{Teams: plan.blocking}
		}
		grants, err := s.d.Delegations.ListByUser(ctx, userID)
		if err != nil {
			return err
		}
		now := s.d.Clock.Now().UTC()
		for _, g := range grants {
			if g.Status == domain.DelegationRevoked {
				continue
			}
			g.Status = domain.DelegationRevoked
			g.RevokedAt = &now
			if err := s.d.Delegations.Update(ctx, g); err != nil {
				return err
			}
		}
		for _, teamID := range plan.soleMember {
			if err := s.d.Teams.Delete(ctx, teamID); err != nil && !errors.Is(err, domain.ErrNotFound) {
				return err
			}
			report.TeamsDeleted++
		}
		return s.d.Users.Delete(ctx, userID)
	})
	if err != nil {
		return port.PurgeReport{}, err
	}
	s.d.Logger.Info("user purged",
		"userId", userID, "teamsDeleted", report.TeamsDeleted, "subscriptionCanceled", report.SubscriptionCanceled)
	return report, nil
}
```

- [ ] **Step 4: Run the tests.** `cd backend && go test ./internal/service/ -run 'TestPurge' -v` — expected: every `TestPurge*` PASS.

- [ ] **Step 5: Run the whole service suite.** `cd backend && go test ./internal/service/` — expected: `ok`.

- [ ] **Step 6: Commit.**

```bash
git add backend/internal/service/lifecycle.go backend/internal/service/lifecycle_test.go
git commit -m "feat(service): UserLifecycleService.Purge — team check, Paddle cancel, transactional revoke/delete" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 7: `ExportService` — throttle claim + 16-file zip stream with paged threads/events (Track B)

**Files:**
- Create: `backend/internal/service/export.go`
- Create: `backend/internal/service/export_test.go`

**Interfaces:**
- Consumes: Task 1 ports (`ExportSink`, `UserExportRepo`, pagers, `EventNoteRepo.ListByUser`), every `ListByUser`/`Get` on the existing repos, `port.TaskQuery`.
- Produces: `service.ExportDeps{Users, Accounts, Calendars, Events, Threads, Messages, Drafts, Snippets, Templates, Sets, Tasks, Links, Bookings, Polls, Labels, EventNotes, Prefs, Preferences, CalendarPrefs, Settings, Exports, Clock}`; `service.NewExportService(d ExportDeps) *ExportService`; `(*ExportService) Export(ctx, userID string, sink port.ExportSink) error`; `service.ExportWindow = time.Hour`; unexported fields `threadPage` (200) / `eventPage` (500) overridable in tests.

- [ ] **Step 1: Write the failing tests** at `backend/internal/service/export_test.go`:

```go
package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

type exportFixture struct {
	svc      *ExportService
	users    *fakeUserRepo
	accounts *fakeAccountRepo
	cals     *fakeCalendarRepo
	events   *fakeEventRepo
	threads  *fakeThreadRepo
	messages *fakeMessageRepo
	snippets *fakeSnippetRepo
	exports  *fakeUserExportRepo
	clock    *fakeClock
}

var exportNow = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

func newExportFixture(t *testing.T) *exportFixture {
	t.Helper()
	f := &exportFixture{
		users: newUserRepo(), accounts: newAccountRepo(), cals: newCalendarRepo(), events: newEventRepo(),
		threads: newThreadRepo(), messages: newMessageRepo(), snippets: newSnippetRepo(),
		exports: newUserExportRepo(), clock: newClock(exportNow),
	}
	f.cals.accounts = f.accounts
	f.events.calendars = f.cals
	f.events.accounts = f.accounts
	links := newBookingLinkRepo()
	f.svc = NewExportService(ExportDeps{
		Users: f.users, Accounts: f.accounts, Calendars: f.cals, Events: f.events, Threads: f.threads,
		Messages: f.messages, Drafts: newDraftRepo(f.accounts), Snippets: f.snippets,
		Templates: newEventTemplateRepo(), Sets: newCalendarSetRepo(), Tasks: newTaskRepo(),
		Links: links, Bookings: newBookingRepo(links), Polls: newPollRepo(), Labels: newLabelRepo(),
		EventNotes: newFakeEventNoteRepo(), Prefs: newPrefsRepo(), Preferences: newUserPreferencesRepo(),
		CalendarPrefs: newCalendarPrefsRepo(), Settings: newUserSettingsRepo(), Exports: f.exports, Clock: f.clock,
	})
	ctx := context.Background()
	if _, err := f.users.Upsert(ctx, domain.User{ID: "u1", Email: "u1@example.com", CreatedAt: exportNow}); err != nil {
		t.Fatal(err)
	}
	return f
}

// seedEverything puts one row in each collection the export reads.
func (f *exportFixture) seedEverything(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@example.com", Status: domain.AccountActive, CreatedAt: exportNow}); err != nil {
		t.Fatal(err)
	}
	if err := f.accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "SECRET-ACCESS", RefreshToken: "SECRET-REFRESH", ExpiresAt: exportNow.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.cals.Upsert(ctx, domain.Calendar{ID: "c1", AccountID: "a1", ProviderCalendarID: "pc1", Name: "Primary"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.events.Upsert(ctx, domain.Event{ID: "e1", CalendarID: "c1", Title: "Standup", Start: exportNow, End: exportNow.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.threads.Upsert(ctx, domain.Thread{ID: "t1", AccountID: "a1", Subject: "Hello", LastMessageAt: exportNow}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.messages.Upsert(ctx, domain.Message{
		ID: "m1", ThreadID: "t1", AccountID: "a1", Subject: "Hello", BodyHTML: "<p>body text</p>",
		Attachments: []domain.Attachment{{ID: "att1", Filename: "deck.pdf", MimeType: "application/pdf", SizeBytes: 3, ProviderAttachmentID: "PROVIDER-ATT"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.snippets.Create(ctx, domain.Snippet{ID: "s1", UserID: "u1", Name: "Thanks", BodyHTML: "<p>thanks</p>"}); err != nil {
		t.Fatal(err)
	}
}

func exportToZip(t *testing.T, f *exportFixture) (map[string][]byte, error) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := f.svc.Export(context.Background(), "u1", zw)
	if cerr := zw.Close(); cerr != nil {
		t.Fatal(cerr)
	}
	if err != nil {
		return nil, err
	}
	zr, zerr := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if zerr != nil {
		t.Fatalf("zip.NewReader: %v", zerr)
	}
	files := map[string][]byte{}
	for _, zf := range zr.File {
		rc, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[zf.Name] = b
	}
	return files, nil
}

func TestExportWritesEveryFile(t *testing.T) {
	f := newExportFixture(t)
	f.seedEverything(t)
	files, err := exportToZip(t, f)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	want := []string{
		"profile.json", "accounts.json", "calendars.json", "events.json", "threads/t1.json",
		"drafts.json", "snippets.json", "templates.json", "calendar-sets.json", "tasks.json",
		"booking-links.json", "bookings.json", "polls.json", "settings.json", "labels.json", "event-notes.json",
	}
	got := make([]string, 0, len(files))
	for name := range files {
		got = append(got, name)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("zip entries = %v\nwant        %v", got, want)
	}
	for name, body := range files {
		if !json.Valid(body) {
			t.Fatalf("%s is not valid JSON: %s", name, body)
		}
	}
	accounts := string(files["accounts.json"])
	for _, forbidden := range []string{"accessToken", "refreshToken", "SECRET-ACCESS", "SECRET-REFRESH", "scopes", "vipSenders"} {
		if strings.Contains(accounts, forbidden) {
			t.Fatalf("accounts.json leaks %q: %s", forbidden, accounts)
		}
	}
	for _, required := range []string{`"id": "a1"`, `"provider": "google"`, `"email": "me@example.com"`, `"status": "active"`, `"createdAt"`} {
		if !strings.Contains(accounts, required) {
			t.Fatalf("accounts.json missing %s: %s", required, accounts)
		}
	}
	thread := string(files["threads/t1.json"])
	for _, required := range []string{`"thread"`, `"messages"`, `"bodyHtml": "<p>body text</p>"`, `"filename": "deck.pdf"`, `"sizeBytes": 3`} {
		if !strings.Contains(thread, required) {
			t.Fatalf("threads/t1.json missing %s: %s", required, thread)
		}
	}
	if strings.Contains(thread, "PROVIDER-ATT") || strings.Contains(thread, "providerAttachmentId") {
		t.Fatalf("threads/t1.json leaks provider attachment ids: %s", thread)
	}
	if !strings.Contains(string(files["snippets.json"]), `"name": "Thanks"`) {
		t.Fatalf("snippets.json = %s", files["snippets.json"])
	}
	if !strings.Contains(string(files["profile.json"]), `"email": "u1@example.com"`) {
		t.Fatalf("profile.json = %s", files["profile.json"])
	}
	settings := string(files["settings.json"])
	for _, key := range []string{`"prefs"`, `"preferences"`, `"calendarPrefs"`, `"settings"`, `"aiBackground": true`} {
		if !strings.Contains(settings, key) {
			t.Fatalf("settings.json missing %s: %s", key, settings)
		}
	}
	if !strings.Contains(string(files["events.json"]), `"title": "Standup"`) {
		t.Fatalf("events.json = %s", files["events.json"])
	}
}

func TestExportThrottle(t *testing.T) {
	f := newExportFixture(t)
	if _, err := exportToZip(t, f); err != nil {
		t.Fatalf("first export: %v", err)
	}
	f.clock.Advance(10 * time.Minute)
	_, err := exportToZip(t, f)
	if !errors.Is(err, domain.ErrExportThrottled) {
		t.Fatalf("second export err = %v, want ErrExportThrottled", err)
	}
	var typed *domain.ExportThrottledError
	if !errors.As(err, &typed) || typed.RetryAfter != 50*time.Minute {
		t.Fatalf("RetryAfter = %+v, want 50m", typed)
	}
	f.clock.Advance(50 * time.Minute)
	if _, err := exportToZip(t, f); err != nil {
		t.Fatalf("export after the window: %v", err)
	}
}

func TestExportNeverReadsProviderTokens(t *testing.T) {
	f := newExportFixture(t)
	f.seedEverything(t)
	before := f.accounts.getTokensCalls
	if _, err := exportToZip(t, f); err != nil {
		t.Fatal(err)
	}
	if f.accounts.getTokensCalls != before {
		t.Fatalf("GetTokens was called %d time(s) during export, want 0", f.accounts.getTokensCalls-before)
	}
}

// Review Focus 1: an event count that is an exact multiple of the page size
// terminates cleanly — every event once, in id order, no phantom page.
func TestExportPagesEventsAcrossBoundary(t *testing.T) {
	f := newExportFixture(t)
	f.seedEverything(t)
	ctx := context.Background()
	for i := 2; i <= 4; i++ { // e1 already exists → 4 events, page size 2
		if _, err := f.events.Upsert(ctx, domain.Event{ID: fmt.Sprintf("e%d", i), CalendarID: "c1", Title: fmt.Sprintf("Event %d", i), Start: exportNow, End: exportNow.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	f.svc.eventPage = 2
	files, err := exportToZip(t, f)
	if err != nil {
		t.Fatal(err)
	}
	var events []domain.Event
	if err := json.Unmarshal(files["events.json"], &events); err != nil {
		t.Fatalf("events.json: %v\n%s", err, files["events.json"])
	}
	ids := []string{}
	for _, e := range events {
		ids = append(ids, e.ID)
	}
	if strings.Join(ids, ",") != "e1,e2,e3,e4" {
		t.Fatalf("events.json ids = %v, want e1..e4 once each in order", ids)
	}
}

func TestExportPagesThreadsPerAccount(t *testing.T) {
	f := newExportFixture(t)
	f.seedEverything(t)
	ctx := context.Background()
	for _, id := range []string{"t2", "t3"} {
		if _, err := f.threads.Upsert(ctx, domain.Thread{ID: id, AccountID: "a1", Subject: id, LastMessageAt: exportNow}); err != nil {
			t.Fatal(err)
		}
	}
	f.svc.threadPage = 2
	files, err := exportToZip(t, f)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"t1", "t2", "t3"} {
		if _, ok := files["threads/"+id+".json"]; !ok {
			t.Fatalf("threads/%s.json missing from %v", id, files)
		}
	}
}

func TestExportRepoErrorPropagatesAndKeepsSlot(t *testing.T) {
	f := newExportFixture(t)
	delete(f.users.byID, "u1") // profile lookup fails
	_, err := exportToZip(t, f)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want the repo error", err)
	}
	if _, err := exportToZip(t, f); !errors.Is(err, domain.ErrExportThrottled) {
		t.Fatalf("slot must stay claimed after a failed export, got %v", err)
	}
}
```

If `fakeAccountRepo` has no `getTokensCalls` counter, add `getTokensCalls int` to its struct and `r.getTokensCalls++` as the first line of its `GetTokens` method in `fakes_test.go` (Track B owns that file).

- [ ] **Step 2: Run and confirm the failure.** `cd backend && go test ./internal/service/ -run 'TestExport'` — expected: `undefined: ExportDeps`, `undefined: NewExportService`.

- [ ] **Step 3: Create `backend/internal/service/export.go`:**

```go
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// ExportWindow is the per-user data-export throttle.
const ExportWindow = time.Hour

const (
	exportThreadPage = 200
	exportEventPage  = 500
	// exportBookingCap bounds bookings.json (BookingRepo.ListByUser is
	// limit-based; a personal booking page never approaches this).
	exportBookingCap = 10000
)

// ExportDeps wires one repo per export file plus the throttle and clock.
type ExportDeps struct {
	Users         port.UserRepo
	Accounts      port.AccountRepo
	Calendars     port.CalendarRepo
	Events        port.EventRepo
	Threads       port.ThreadRepo
	Messages      port.MessageRepo
	Drafts        port.DraftRepo
	Snippets      port.SnippetRepo
	Templates     port.EventTemplateRepo
	Sets          port.CalendarSetRepo
	Tasks         port.TaskRepo
	Links         port.BookingLinkRepo
	Bookings      port.BookingRepo
	Polls         port.PollRepo
	Labels        port.LabelRepo
	EventNotes    port.EventNoteRepo
	Prefs         port.PrefsRepo
	Preferences   port.UserPreferencesRepo
	CalendarPrefs port.CalendarPrefsRepo
	Settings      port.UserSettingsRepo
	Exports       port.UserExportRepo
	Clock         port.Clock
}

// ExportService implements port.ExportService: claims the hourly slot, then
// writes the 16 export files into the sink in a fixed order, paging threads
// (per account, keyset on id) and events (keyset on id) so memory stays
// flat. It never touches AccountRepo.GetTokens.
type ExportService struct {
	d          ExportDeps
	threadPage int
	eventPage  int
}

var _ port.ExportService = (*ExportService)(nil)

func NewExportService(d ExportDeps) *ExportService {
	return &ExportService{d: d, threadPage: exportThreadPage, eventPage: exportEventPage}
}

// exportAccount is accounts.json's row: identity only, never tokens/scopes.
type exportAccount struct {
	ID        string          `json:"id"`
	Provider  domain.Provider `json:"provider"`
	Email     string          `json:"email"`
	Status    string          `json:"status"`
	CreatedAt time.Time       `json:"createdAt"`
}

type exportThread struct {
	Thread   domain.Thread    `json:"thread"`
	Messages []domain.Message `json:"messages"`
}

type exportPoll struct {
	Poll  domain.MeetingPoll `json:"poll"`
	Votes []domain.PollVote  `json:"votes"`
}

type exportSettings struct {
	Prefs         domain.UserPrefs     `json:"prefs"`
	Preferences   port.UserPreferences `json:"preferences"`
	CalendarPrefs domain.CalendarPrefs `json:"calendarPrefs"`
	Settings      domain.UserSettings  `json:"settings"`
}

// writeExportFile encodes v as one pretty-printed JSON document.
func writeExportFile(sink port.ExportSink, name string, v any) error {
	w, err := sink.Create(name)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("export %s: %w", name, err)
	}
	return nil
}

// exportArray streams a JSON array element by element (paged collections).
type exportArray struct {
	w io.Writer
	n int
}

func newExportArray(sink port.ExportSink, name string) (*exportArray, error) {
	w, err := sink.Create(name)
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(w, "[\n"); err != nil {
		return nil, err
	}
	return &exportArray{w: w}, nil
}

func (a *exportArray) item(v any) error {
	b, err := json.MarshalIndent(v, "  ", "  ")
	if err != nil {
		return err
	}
	sep := ",\n"
	if a.n == 0 {
		sep = ""
	}
	a.n++
	_, err = fmt.Fprintf(a.w, "%s  %s", sep, b)
	return err
}

func (a *exportArray) close() error {
	_, err := io.WriteString(a.w, "\n]\n")
	return err
}

// Export implements port.ExportService.
func (s *ExportService) Export(ctx context.Context, userID string, sink port.ExportSink) error {
	now := s.d.Clock.Now().UTC()
	ok, retryAt, err := s.d.Exports.Claim(ctx, userID, now, ExportWindow)
	if err != nil {
		return err
	}
	if !ok {
		wait := retryAt.Sub(now)
		if wait < time.Second {
			wait = time.Second
		}
		return &domain.ExportThrottledError{RetryAfter: wait}
	}

	user, err := s.d.Users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if err := writeExportFile(sink, "profile.json", user); err != nil {
		return err
	}

	accounts, err := s.d.Accounts.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	exported := make([]exportAccount, 0, len(accounts))
	for _, a := range accounts {
		exported = append(exported, exportAccount{ID: a.ID, Provider: a.Provider, Email: a.Email, Status: string(a.Status), CreatedAt: a.CreatedAt.UTC()})
	}
	if err := writeExportFile(sink, "accounts.json", exported); err != nil {
		return err
	}

	calendars, err := s.d.Calendars.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	if err := writeExportFile(sink, "calendars.json", calendars); err != nil {
		return err
	}

	events, err := newExportArray(sink, "events.json")
	if err != nil {
		return err
	}
	for after := ""; ; {
		page, err := s.d.Events.ListByUserPage(ctx, userID, after, s.eventPage)
		if err != nil {
			return err
		}
		for _, e := range page {
			if err := events.item(e); err != nil {
				return err
			}
		}
		if len(page) < s.eventPage {
			break
		}
		after = page[len(page)-1].ID
	}
	if err := events.close(); err != nil {
		return err
	}

	for _, a := range accounts {
		for after := ""; ; {
			page, err := s.d.Threads.ListByAccountPage(ctx, a.ID, after, s.threadPage)
			if err != nil {
				return err
			}
			for _, t := range page {
				msgs, err := s.d.Messages.ListByThread(ctx, t.ID)
				if err != nil {
					return err
				}
				if err := writeExportFile(sink, "threads/"+t.ID+".json", exportThread{Thread: t, Messages: msgs}); err != nil {
					return err
				}
			}
			if len(page) < s.threadPage {
				break
			}
			after = page[len(page)-1].ID
		}
	}

	drafts, err := s.d.Drafts.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	if err := writeExportFile(sink, "drafts.json", drafts); err != nil {
		return err
	}
	snippets, err := s.d.Snippets.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	if err := writeExportFile(sink, "snippets.json", snippets); err != nil {
		return err
	}
	templates, err := s.d.Templates.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	if err := writeExportFile(sink, "templates.json", templates); err != nil {
		return err
	}
	sets, err := s.d.Sets.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	if err := writeExportFile(sink, "calendar-sets.json", sets); err != nil {
		return err
	}
	tasks, err := s.d.Tasks.List(ctx, port.TaskQuery{UserID: userID, IncludeCompleted: true})
	if err != nil {
		return err
	}
	if err := writeExportFile(sink, "tasks.json", tasks); err != nil {
		return err
	}
	links, err := s.d.Links.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	if err := writeExportFile(sink, "booking-links.json", links); err != nil {
		return err
	}
	bookings, err := s.d.Bookings.ListByUser(ctx, userID, exportBookingCap)
	if err != nil {
		return err
	}
	if err := writeExportFile(sink, "bookings.json", bookings); err != nil {
		return err
	}
	polls, err := s.d.Polls.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	exportedPolls := make([]exportPoll, 0, len(polls))
	for _, p := range polls {
		votes, err := s.d.Polls.ListVotes(ctx, p.ID)
		if err != nil {
			return err
		}
		exportedPolls = append(exportedPolls, exportPoll{Poll: p, Votes: votes})
	}
	if err := writeExportFile(sink, "polls.json", exportedPolls); err != nil {
		return err
	}
	prefs, err := s.d.Prefs.Get(ctx, userID)
	if err != nil {
		return err
	}
	preferences, err := s.d.Preferences.Get(ctx, userID)
	if err != nil {
		return err
	}
	calendarPrefs, err := s.d.CalendarPrefs.Get(ctx, userID)
	if err != nil {
		return err
	}
	settings, err := s.d.Settings.Get(ctx, userID)
	if err != nil {
		return err
	}
	if err := writeExportFile(sink, "settings.json", exportSettings{Prefs: prefs, Preferences: preferences, CalendarPrefs: calendarPrefs, Settings: settings}); err != nil {
		return err
	}
	labels, err := s.d.Labels.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	if err := writeExportFile(sink, "labels.json", labels); err != nil {
		return err
	}
	notes, err := s.d.EventNotes.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	return writeExportFile(sink, "event-notes.json", notes)
}
```

- [ ] **Step 4: Run the tests.** `cd backend && go test ./internal/service/ -run 'TestExport' -v` — expected: all PASS. If `TestExportWritesEveryFile` reports `settings.json missing "aiBackground": true`, the `fakeUserSettingsRepo.Get` default from Task 5 is missing — fix the fake, not the test.

- [ ] **Step 5: Run the whole service suite.** `cd backend && go test ./internal/service/` — expected: `ok`.

- [ ] **Step 6: Commit.**

```bash
git add backend/internal/service/export.go backend/internal/service/export_test.go backend/internal/service/fakes_test.go
git commit -m "feat(service): ExportService — hourly claim, 16-file zip stream, paged threads/events, no tokens" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 8: Settings `SetAIBackground` + `sync.go` background-AI gate (Track B)

**Files:**
- Modify: `backend/internal/service/settings.go` (`Update` L37–55; append `SetAIBackground`)
- Create: `backend/internal/service/settings_test.go`
- Modify: `backend/internal/service/sync.go` (`SyncServiceDeps` L21–61; `SyncService` L66–91; `NewSyncService` L95–122; `SyncAccount` L125–149; `syncMail` L151–171; `applyMailPage` L174 signature + the `enqueueIngestAiJobs` call ~L302; `enqueueIngestAiJobs` L357–404; `deliverDraft` reminder enqueue L628–637)
- Modify: `backend/internal/service/sync_test.go` (append)

**Interfaces:**
- Consumes: `port.UserSettingsRepo.{Get,SetAIBackground}`, `domain.UserSettings.AIBackground`.
- Produces: `(*SettingsService) SetAIBackground(ctx, userID string, on bool) (domain.UserSettings, error)`; `SyncServiceDeps.UserSettings port.UserSettingsRepo` (optional); unexported `(*SyncService) backgroundAIAllowed(ctx, userID string) bool`; `enqueueIngestAiJobs(ctx, acct, t, hasClassifiers, aiAllowed bool)`.

- [ ] **Step 1: Write the failing settings tests** at `backend/internal/service/settings_test.go`:

```go
package service

import (
	"context"
	"testing"

	"calendium/backend/internal/domain"
)

func TestSettingsUpdatePreservesAIBackgroundWhenAbsent(t *testing.T) {
	ctx := context.Background()
	repo := newUserSettingsRepo()
	svc := NewSettingsService(repo)

	fresh, err := svc.Update(ctx, "u1", domain.UserSettings{TimeZone: "UTC"})
	if err != nil || !fresh.AIBackground {
		t.Fatalf("first Update = (%+v, %v), want AIBackground default true", fresh, err)
	}
	if _, err := svc.SetAIBackground(ctx, "u1", false); err != nil {
		t.Fatal(err)
	}
	// An older client PUTs the whole document without the field (decodes as false).
	after, err := svc.Update(ctx, "u1", domain.UserSettings{TimeZone: "Europe/Lisbon", AIBackground: false})
	if err != nil {
		t.Fatal(err)
	}
	if after.AIBackground {
		t.Fatal("Update must not flip AIBackground back on")
	}
	if after.TimeZone != "Europe/Lisbon" {
		t.Fatalf("TimeZone = %q", after.TimeZone)
	}
	got, _ := svc.Get(ctx, "u1")
	if got.AIBackground {
		t.Fatal("stored AIBackground must stay false")
	}
}

func TestSettingsSetAIBackgroundReturnsDocument(t *testing.T) {
	ctx := context.Background()
	svc := NewSettingsService(newUserSettingsRepo())
	got, err := svc.SetAIBackground(ctx, "u1", false)
	if err != nil {
		t.Fatal(err)
	}
	if got.AIBackground || got.TimeZone != "UTC" || got.WorkingHours == nil {
		t.Fatalf("SetAIBackground on a fresh user = %+v, want aiBackground=false with defaults and a non-nil WorkingHours", got)
	}
	got, _ = svc.SetAIBackground(ctx, "u1", true)
	if !got.AIBackground {
		t.Fatal("SetAIBackground(true) not reflected")
	}
}
```

- [ ] **Step 2: Write the failing sync tests.** Append to `backend/internal/service/sync_test.go`:

```go
// TestBackgroundAIOffSkipsEnqueue: Settings → AI → "Background AI
// processing" off skips every ingest enqueue (summary, instant replies,
// auto draft, classify) and the first-sync voice profile; on (default) and
// a settings repo error both enqueue as before.
func TestBackgroundAIOffSkipsEnqueue(t *testing.T) {
	cases := []struct {
		name       string
		settings   *fakeUserSettingsRepo // nil = dep not wired
		aiOff      bool
		repoErr    bool
		wantEnqueue bool
	}{
		{name: "default on", settings: newUserSettingsRepo(), wantEnqueue: true},
		{name: "switched off", settings: newUserSettingsRepo(), aiOff: true, wantEnqueue: false},
		{name: "repo error reads as on", settings: newUserSettingsRepo(), repoErr: true, wantEnqueue: true},
		{name: "dep not wired", settings: nil, wantEnqueue: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
			accounts := newAccountRepo()
			if _, err := accounts.Create(ctx, domain.ConnectedAccount{
				ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@acme.com", LastSyncedAt: nil, // first sync
			}); err != nil {
				t.Fatal(err)
			}
			if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "valid", ExpiresAt: now.Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			if tc.settings != nil {
				if tc.aiOff {
					if err := tc.settings.SetAIBackground(ctx, "u1", false); err != nil {
						t.Fatal(err)
					}
				}
				if tc.repoErr {
					tc.settings.getErr = errors.New("db down")
				}
			}
			classifiers := newClassifierRepo()
			if _, err := classifiers.Create(ctx, domain.AiClassifier{UserID: "u1", Name: "rule", Prompt: "p", LabelName: "L", Enabled: true}); err != nil {
				t.Fatal(err)
			}
			mail := newMailProvider()
			mail.syncPage = port.MailSyncPage{
				Threads: []domain.Thread{{ProviderThreadID: "pt1", InInbox: true, LastMessageAt: now}},
				Messages: []port.IncomingMessage{{Message: domain.Message{
					ProviderMessageID: "pm1", ThreadID: "pt1",
					From: domain.EmailAddress{Email: "sender@example.org"},
					To:   []domain.EmailAddress{{Email: "me@acme.com"}}, SentAt: now,
				}}},
				NextCursor: "c1",
			}
			aiJobs := newAiJobRepo()
			deps := SyncServiceDeps{
				Accounts: accounts, Labels: newLabelRepo(), Threads: newThreadRepo(), Messages: newMessageRepo(),
				SyncState: newSyncStateRepo(), AiJobs: aiJobs, Classifiers: classifiers,
				MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mail},
				OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
				Clock:         newClock(now),
			}
			if tc.settings != nil {
				deps.UserSettings = tc.settings
			}
			svc := NewSyncService(deps)
			if err := svc.SyncAccount(ctx, "a1"); err != nil {
				t.Fatalf("SyncAccount: %v", err)
			}
			kinds := map[domain.AiJobKind]bool{}
			for _, j := range aiJobs.queue {
				kinds[j.Kind] = true
			}
			wantKinds := []domain.AiJobKind{domain.AiJobVoiceProfile, domain.AiJobThreadSummary, domain.AiJobInstantReplies, domain.AiJobAutoDraft, domain.AiJobClassify}
			if tc.wantEnqueue {
				for _, k := range wantKinds {
					if !kinds[k] {
						t.Fatalf("kind %q not enqueued; queue=%v", k, kinds)
					}
				}
			} else if len(aiJobs.queue) != 0 {
				t.Fatalf("background AI off must enqueue nothing, got %v", kinds)
			}
		})
	}
}

// reminder_detect rides the delivery path (ProcessDueWork), so it reads the
// switch per delivered draft.
func TestBackgroundAIOffSkipsReminderDetect(t *testing.T) {
	for _, aiOff := range []bool{false, true} {
		t.Run(fmt.Sprintf("off=%v", aiOff), func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
			accounts := newAccountRepo()
			if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@acme.com"}); err != nil {
				t.Fatal(err)
			}
			if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "at", ExpiresAt: now.Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			threads := newThreadRepo()
			if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", AccountID: "a1", ProviderThreadID: "pt1"}); err != nil {
				t.Fatal(err)
			}
			drafts := newDraftRepo(accounts)
			past := now.Add(-time.Minute)
			tid := "t1"
			draft := domain.Draft{ID: "d1", AccountID: "a1", ThreadID: &tid, To: []domain.EmailAddress{{Email: "friend@example.org"}}, Subject: "Lunch?", BodyHTML: "<p>hi</p>", ScheduledAt: &past}
			if _, err := drafts.Create(ctx, draft); err != nil {
				t.Fatal(err)
			}
			drafts.claimOutcome["d1"] = true
			drafts.scheduledDue = []domain.Draft{draft}
			mail := newMailProvider()
			mail.sentResult = port.SentMessage{ProviderMessageID: "pm1", ProviderThreadID: "pt1", SentAt: now}
			settings := newUserSettingsRepo()
			if aiOff {
				if err := settings.SetAIBackground(ctx, "u1", false); err != nil {
					t.Fatal(err)
				}
			}
			aiJobs := newAiJobRepo()
			svc := NewSyncService(SyncServiceDeps{
				Accounts: accounts, Threads: threads, Messages: newMessageRepo(), Drafts: drafts,
				AiJobs: aiJobs, UserSettings: settings,
				MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mail},
				OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
				Clock:         newClock(now),
			})
			if err := svc.ProcessDueWork(ctx); err != nil {
				t.Fatalf("ProcessDueWork: %v", err)
			}
			got := 0
			for _, j := range aiJobs.queue {
				if j.Kind == domain.AiJobReminderDetect {
					got++
				}
			}
			want := 1
			if aiOff {
				want = 0
			}
			if got != want {
				t.Fatalf("reminder_detect jobs = %d, want %d (aiOff=%v)", got, want, aiOff)
			}
			if len(mail.sent) != 1 {
				t.Fatalf("the switch must never block delivery: sent=%d", len(mail.sent))
			}
		})
	}
}
```

(`sync_test.go` already imports `context`, `errors`, `fmt`, `testing`, `time`, `domain`, `port`; add any that `go vet` reports missing.)

- [ ] **Step 3: Run and confirm the failure.** `cd backend && go test ./internal/service/ -run 'TestSettings|TestBackgroundAI'` — expected: `svc.SetAIBackground undefined`, `unknown field UserSettings in struct literal of type SyncServiceDeps`.

- [ ] **Step 4: Update `backend/internal/service/settings.go`.** Replace `Update` and add `SetAIBackground`:

```go
// Update validates TimeZone (must be a loadable IANA zone) and every
// WorkingHours window, then upserts. The repo's Upsert never writes
// ai_background, so the returned document is re-read to carry the stored
// switch (older clients PUT the document without the field).
func (s *SettingsService) Update(ctx context.Context, userID string, in domain.UserSettings) (domain.UserSettings, error) {
	if !validIANATimeZone(in.TimeZone) {
		return domain.UserSettings{}, fmt.Errorf("%w: invalid time zone %q", domain.ErrValidation, in.TimeZone)
	}
	for _, w := range in.WorkingHours {
		if err := w.Validate(); err != nil {
			return domain.UserSettings{}, err
		}
	}
	in.UserID = userID
	if in.WorkingHours == nil {
		in.WorkingHours = []domain.AvailabilityWindow{}
	}
	if err := s.settings.Upsert(ctx, in); err != nil {
		return domain.UserSettings{}, err
	}
	return s.Get(ctx, userID)
}

// SetAIBackground flips Settings → AI → "Background AI processing" and
// returns the resulting document. No paywall — it only ever turns work off.
func (s *SettingsService) SetAIBackground(ctx context.Context, userID string, on bool) (domain.UserSettings, error) {
	if err := s.settings.SetAIBackground(ctx, userID, on); err != nil {
		return domain.UserSettings{}, err
	}
	return s.Get(ctx, userID)
}
```

- [ ] **Step 5: Gate the enqueues in `backend/internal/service/sync.go`.** Make these exact edits:

(a) In `SyncServiceDeps`, after the `CalendarPrefs port.CalendarPrefsRepo` line add:

```go
	// UserSettings is optional: when set, the owner's Settings → AI →
	// "Background AI processing" switch (UserSettings.AIBackground) gates
	// every ai_jobs enqueue here — sync.go is the only producer, so queued
	// jobs still complete and on-demand /v1/ai/* is unaffected. Nil (or a
	// read error) reads as on, mirroring the hasClassifiers fallback.
	UserSettings port.UserSettingsRepo
```

(b) In the `SyncService` struct, after `calendarPrefs port.CalendarPrefsRepo // optional (leave-alert time zone)` add `userSettings port.UserSettingsRepo // optional (background-AI switch)`; in `NewSyncService` after `calendarPrefs: d.CalendarPrefs,` add `userSettings: d.UserSettings,`.

(c) Add the helper after `NewSyncService`:

```go
// backgroundAIAllowed reports the owner's background-AI switch. Fail-open
// on a missing repo or a repo error: a transient settings read must not
// change behaviour for everyone (same stance as hasClassifiers).
func (s *SyncService) backgroundAIAllowed(ctx context.Context, userID string) bool {
	if s.userSettings == nil {
		return true
	}
	settings, err := s.userSettings.Get(ctx, userID)
	if err != nil {
		return true
	}
	return settings.AIBackground
}
```

(d) In `SyncAccount`, replace the voice-profile block and the `syncMail` call:

```go
	// Read the owner's background-AI switch once per pass; it gates the
	// voice-profile enqueue below and every ingest enqueue in applyMailPage.
	aiAllowed := s.backgroundAIAllowed(ctx, acct.UserID)
	// Enqueue voice_profile on first sync of the account.
	if s.aiJobs != nil && aiAllowed && acct.LastSyncedAt == nil {
		_ = s.aiJobs.Enqueue(ctx, domain.AiJob{
			ID:        newID(),
			UserID:    acct.UserID,
			AccountID: acct.ID,
			Kind:      domain.AiJobVoiceProfile,
			ThreadID:  nil,
			Payload:   map[string]string{},
			RunAfter:  s.clock.Now(),
		})
	}
	token, err := s.tokens.accessToken(ctx, acct)
	if err != nil {
		return err
	}
	if err := s.syncMail(ctx, acct, token, aiAllowed); err != nil {
		return fmt.Errorf("sync mail for account %s: %w", acct.ID, err)
	}
```

(e) Change `syncMail`'s signature to `func (s *SyncService) syncMail(ctx context.Context, acct domain.ConnectedAccount, token string, aiAllowed bool) error` and its `applyMailPage` call to `s.applyMailPage(ctx, acct, page, aiAllowed)`; change `applyMailPage`'s signature to `func (s *SyncService) applyMailPage(ctx context.Context, acct domain.ConnectedAccount, page port.MailSyncPage, aiAllowed bool) error` and the enqueue call to `s.enqueueIngestAiJobs(ctx, acct, saved, hasClassifiers, aiAllowed)`. Run `grep -n 'applyMailPage(\|syncMail(' backend/internal/service/*_test.go` — any direct test caller gets the extra `true` argument.

(f) Change `enqueueIngestAiJobs` to:

```go
// enqueueIngestAiJobs queues background AI work for a thread that just
// received a genuinely new inbound message. Enqueue failures are intentionally
// discarded — AI enqueueing must never fail sync. aiAllowed is the owner's
// background-AI switch (read once per pass by SyncAccount).
func (s *SyncService) enqueueIngestAiJobs(ctx context.Context, acct domain.ConnectedAccount, t domain.Thread, hasClassifiers, aiAllowed bool) {
	if s.aiJobs == nil || !aiAllowed {
		return
	}
```

(the rest of the body unchanged).

(g) In `deliverDraft`, change `if s.aiJobs != nil {` guarding the `AiJobReminderDetect` enqueue to `if s.aiJobs != nil && s.backgroundAIAllowed(ctx, acct.UserID) {`.

- [ ] **Step 6: Run the tests.** `cd backend && go test ./internal/service/ -run 'TestSettings|TestBackgroundAI|TestSyncAccount|TestProcessDueWork' -v` — expected: all PASS, including every pre-existing sync test (they leave `UserSettings` nil → fail-open).

- [ ] **Step 7: Run the whole service suite and vet.** `cd backend && go vet ./internal/service/ && go test ./internal/service/` — expected: `ok`.

- [ ] **Step 8: Commit.**

```bash
git add backend/internal/service/settings.go backend/internal/service/settings_test.go backend/internal/service/sync.go backend/internal/service/sync_test.go
git commit -m "feat(service): SetAIBackground + background-AI gate on every ai_jobs enqueue" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 9: Config — `INTERNAL_API_SECRET` (required in both modes, parsed like `TOKEN_ENCRYPTION_KEY`) (Track B)

**Files:**
- Modify: `backend/internal/config/config.go` (`Crypto` struct L131–135; the `TOKEN_ENCRYPTION_KEY` switch L291–301)
- Modify: `backend/internal/config/config_test.go` (`configEnvKeys` L17–29; `withBase` L40–48; `TestFromEnv` cases; every other test that sets `TOKEN_ENCRYPTION_KEY` directly)

**Interfaces:**
- Produces: `config.Crypto.InternalAPISecret []byte` (32 bytes decoded from `INTERNAL_API_SECRET`).

- [ ] **Step 1: Write the failing tests.** In `backend/internal/config/config_test.go`: add `"INTERNAL_API_SECRET"` to `configEnvKeys` (after `"TOKEN_ENCRYPTION_KEY"`), add `"INTERNAL_API_SECRET": validKeyHex,` to the map in `withBase`, and add these cases to the `TestFromEnv` table right after the `"TOKEN_ENCRYPTION_KEY valid hex but wrong length errors"` case:

```go
		{
			name: "missing INTERNAL_API_SECRET errors in cloud mode",
			env: map[string]string{
				"DATABASE_URL": "postgres://localhost/db", "TOKEN_ENCRYPTION_KEY": validKeyHex, "SELF_HOSTED": "false",
			},
			wantErr: true,
		},
		{
			name: "missing INTERNAL_API_SECRET errors in self-host mode too",
			env: map[string]string{
				"DATABASE_URL": "postgres://localhost/db", "TOKEN_ENCRYPTION_KEY": validKeyHex, "SELF_HOSTED": "true",
			},
			wantErr: true,
		},
		{
			name:    "INTERNAL_API_SECRET not hex errors",
			env:     withBase(map[string]string{"INTERNAL_API_SECRET": "zzzz-not-hex-zzzz"}),
			wantErr: true,
		},
		{
			name:    "INTERNAL_API_SECRET wrong length errors",
			env:     withBase(map[string]string{"INTERNAL_API_SECRET": "00112233445566778899aabbccddeeff"}),
			wantErr: true,
		},
		{
			name: "INTERNAL_API_SECRET decodes to 32 bytes",
			env:  withBase(nil),
			check: func(t *testing.T, c Config) {
				if len(c.Crypto.InternalAPISecret) != 32 {
					t.Fatalf("InternalAPISecret len = %d, want 32", len(c.Crypto.InternalAPISecret))
				}
			},
		},
```

If the `TestFromEnv` table has no `check func(t *testing.T, c Config)` field, add it to the struct and call it after a successful `FromEnv()` (`if tt.check != nil { tt.check(t, c) }`). Then run `grep -n 'TOKEN_ENCRYPTION_KEY", validKeyHex)' backend/internal/config/config_test.go` and, next to EVERY `t.Setenv("TOKEN_ENCRYPTION_KEY", validKeyHex)` outside `withBase` (e.g. `TestFromEnvIntegrationVendors`, the `setBase` helper in `TestWeatherFromEnv`), add `t.Setenv("INTERNAL_API_SECRET", validKeyHex)`; in `TestFromEnvJoinsMultipleErrors` nothing changes (it expects errors).

- [ ] **Step 2: Run and confirm the failure.** `cd backend && go test ./internal/config/` — expected: `c.Crypto.InternalAPISecret undefined` and, once that compiles, the "missing INTERNAL_API_SECRET" cases fail with `wantErr`.

- [ ] **Step 3: Update `backend/internal/config/config.go`.** Replace the `Crypto` struct:

```go
type Crypto struct {
	// TokenEncryptionKey is the 32-byte AES-256-GCM key for provider
	// refresh tokens (TOKEN_ENCRYPTION_KEY, 64 hex chars).
	TokenEncryptionKey []byte
	// InternalAPISecret authenticates the web app's server-to-server calls
	// to /v1/internal/* (INTERNAL_API_SECRET, 64 hex chars; the same value
	// is configured on the web service). Required in both modes.
	InternalAPISecret []byte
}
```

and add, directly after the `TOKEN_ENCRYPTION_KEY` switch:

```go
	switch key := os.Getenv("INTERNAL_API_SECRET"); key {
	case "":
		errs = append(errs, errors.New("INTERNAL_API_SECRET is required (64 hex chars / 32 bytes; shared by api and web — openssl rand -hex 32)"))
	default:
		raw, err := hex.DecodeString(key)
		if err != nil || len(raw) != 32 {
			errs = append(errs, errors.New("INTERNAL_API_SECRET must be exactly 64 hex chars (32 bytes)"))
		} else {
			cfg.Crypto.InternalAPISecret = raw
		}
	}
```

- [ ] **Step 4: Run the config tests.** `cd backend && go test ./internal/config/` — expected: `ok`.

- [ ] **Step 5: Commit.**

```bash
git add backend/internal/config
git commit -m "feat(config): INTERNAL_API_SECRET required in both modes, parsed like TOKEN_ENCRYPTION_KEY" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 10: HTTP — error details/Retry-After, internal purge route, export stream, settings tri-state, CORS exclusion (Track B)

**Files:**
- Modify: `backend/internal/adapter/in/httpapi/codec.go` (`errorDetail` L19–22; `statusFor` L57–85; `safeMessage` L92–120; `writeError` L125–135)
- Modify: `backend/internal/adapter/in/httpapi/codec_test.go` (append rows + new test)
- Modify: `backend/internal/adapter/in/httpapi/httpapi.go` (`Deps` L16–90; route table — add one unauthenticated route after the integrations callback L121 and one authed route after `authed("PUT /v1/me/preferences", ...)` L144)
- Create: `backend/internal/adapter/in/httpapi/lifecycle.go`
- Create: `backend/internal/adapter/in/httpapi/lifecycle_handlers_test.go`
- Modify: `backend/internal/adapter/in/httpapi/scheduling.go` (`handleUpdateSettings` L326–338; imports)
- Modify: `backend/internal/adapter/in/httpapi/scheduling_handlers_test.go` (append)
- Modify: `backend/internal/adapter/in/httpapi/middleware.go` (`corsMiddleware` L129–154)
- Modify: `backend/internal/adapter/in/httpapi/middleware_test.go` (append)
- Modify: `backend/internal/adapter/in/httpapi/harness_test.go` (`fakeSettingsService` L1041–1057; `harness` struct L1273–1293; `newHarness` L1295–1343)

**Interfaces:**
- Consumes: `port.UserLifecycleService`, `port.ExportService`, `port.SettingsService.SetAIBackground`, `*domain.OwnsTeamsError`, `*domain.ExportThrottledError`, `domain.ErrBillingUnavailable`.
- Produces: `Deps.Lifecycle port.UserLifecycleService`, `Deps.Export port.ExportService`, `Deps.InternalSecret []byte`; routes `DELETE /v1/internal/users/{id}` (unauthenticated, secret) and `GET /v1/me/export` (authed); `errorDetail.Details any`; `statusFor` → `owns_teams`/`export_throttled` (409), `billing_unavailable` (502, add only if piece 1 has not); `writeError` sets `Retry-After` for throttles and `details.teams` for owns_teams; `internalSecretMatches(header string, secret []byte) bool`; `exportSink`; `corsMiddleware` skips `/v1/internal/`.

- [ ] **Step 1: Write the failing codec tests.** In `backend/internal/adapter/in/httpapi/codec_test.go` add to the `TestStatusFor` table:

```go
		{"owns teams", &domain.OwnsTeamsError{Teams: []domain.TeamRef{{ID: "t1", Name: "Design"}}}, http.StatusConflict, "owns_teams"},
		{"export throttled", &domain.ExportThrottledError{RetryAfter: time.Minute}, http.StatusConflict, "export_throttled"},
		{"billing unavailable", fmt.Errorf("x: %w", domain.ErrBillingUnavailable), http.StatusBadGateway, "billing_unavailable"},
```

to the `TestSafeMessage` table:

```go
		{"owns_teams", "Transfer ownership of your teams before deleting your account."},
		{"export_throttled", "You exported your data recently. Try again later."},
		{"billing_unavailable", "Billing is temporarily unavailable. Try again later."},
```

and append:

```go
func TestWriteErrorCarriesDetailsAndRetryAfter(t *testing.T) {
	h := newHarness(t)
	srv := h.server()

	rec := httptest.NewRecorder()
	srv.writeError(rec, httptest.NewRequest(http.MethodDelete, "/x", nil),
		&domain.OwnsTeamsError{Teams: []domain.TeamRef{{ID: "t1", Name: "Design"}}})
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Teams []domain.TeamRef `json:"teams"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusConflict || body.Error.Code != "owns_teams" || len(body.Error.Details.Teams) != 1 || body.Error.Details.Teams[0].Name != "Design" {
		t.Fatalf("owns_teams envelope = %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	srv.writeError(rec, httptest.NewRequest(http.MethodGet, "/x", nil), &domain.ExportThrottledError{RetryAfter: 59200 * time.Millisecond})
	if rec.Code != http.StatusConflict || rec.Header().Get("Retry-After") != "60" {
		t.Fatalf("throttle: status=%d Retry-After=%q, want 409 and ceil(59.2s)=60", rec.Code, rec.Header().Get("Retry-After"))
	}
	if strings.Contains(rec.Body.String(), `"details"`) {
		t.Fatalf("a plain error must omit details: %s", rec.Body.String())
	}
}
```

(add `"encoding/json"`, `"net/http/httptest"`, `"time"` to the imports as needed; `fmt`, `strings`, `http` are already imported).

- [ ] **Step 2: Write the failing handler tests** at `backend/internal/adapter/in/httpapi/lifecycle_handlers_test.go`:

```go
package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

var testInternalSecret = bytes.Repeat([]byte{0xab}, 32)

func withSecret(h *harness) *harness {
	h.deps.InternalSecret = testInternalSecret
	return h
}

func internalDelete(h *harness, id, header string) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/v1/internal/users/"+id, nil)
	if header != "" {
		req.Header.Set("X-Internal-Secret", header)
	}
	rec := httptest.NewRecorder()
	h.handler().ServeHTTP(rec, req)
	return rec
}

func TestInternalPurgeWithoutConfiguredSecretIsUnknownRoute(t *testing.T) {
	h := newHarness(t) // InternalSecret empty
	unknown := h.anon(http.MethodDelete, "/v1/internal/nope", nil)
	rec := internalDelete(h, "u1", hex.EncodeToString(testInternalSecret))
	if rec.Code != http.StatusNotFound || rec.Body.String() != unknown.Body.String() {
		t.Fatalf("status=%d body=%q, want the mux's own 404 (%q)", rec.Code, rec.Body.String(), unknown.Body.String())
	}
	if h.lifecycle.purgeCalls != 0 {
		t.Fatal("Purge must not run without a configured secret")
	}
}

func TestInternalPurgeWrongOrMissingHeaderIsUnknownRoute(t *testing.T) {
	h := withSecret(newHarness(t))
	unknown := h.anon(http.MethodDelete, "/v1/internal/nope", nil)
	for name, header := range map[string]string{
		"missing":     "",
		"wrong bytes": strings.Repeat("cd", 32),
		"not hex":     strings.Repeat("zz", 32),
		"prefix only": strings.Repeat("ab", 31),
	} {
		t.Run(name, func(t *testing.T) {
			rec := internalDelete(h, "u1", header)
			if rec.Code != http.StatusNotFound || rec.Body.String() != unknown.Body.String() {
				t.Fatalf("status=%d body=%q, want the mux's own 404", rec.Code, rec.Body.String())
			}
		})
	}
	if h.lifecycle.purgeCalls != 0 {
		t.Fatal("Purge must not run on a bad header")
	}
}

// Review Focus 2: uppercase hex and surrounding whitespace still match; a
// wrong-length value never does.
func TestInternalPurgeHeaderNormalization(t *testing.T) {
	h := withSecret(newHarness(t))
	upper := strings.ToUpper(hex.EncodeToString(testInternalSecret))
	if rec := internalDelete(h, "u1", upper); rec.Code != http.StatusNoContent {
		t.Fatalf("uppercase hex: status=%d body=%s, want 204", rec.Code, rec.Body.String())
	}
	if rec := internalDelete(h, "u1", "  "+hex.EncodeToString(testInternalSecret)+"\t"); rec.Code != http.StatusNoContent {
		t.Fatalf("padded hex: status=%d, want 204", rec.Code)
	}
	if rec := internalDelete(h, "u1", hex.EncodeToString(testInternalSecret)+"ab"); rec.Code != http.StatusNotFound {
		t.Fatalf("over-long hex: status=%d, want 404", rec.Code)
	}
}

func TestInternalPurgeSuccess204(t *testing.T) {
	h := withSecret(newHarness(t))
	rec := internalDelete(h, "user_42", hex.EncodeToString(testInternalSecret))
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("status=%d body=%q, want 204 empty", rec.Code, rec.Body.String())
	}
	if h.lifecycle.purgeCalls != 1 || h.lifecycle.gotUserID != "user_42" {
		t.Fatalf("Purge calls=%d id=%q", h.lifecycle.purgeCalls, h.lifecycle.gotUserID)
	}
	if h.verifier.calls != 0 {
		t.Fatal("the internal route must never consult the JWT verifier")
	}
}

func TestInternalPurgeOwnsTeams409WithDetails(t *testing.T) {
	h := withSecret(newHarness(t))
	h.lifecycle.purgeErr = &domain.OwnsTeamsError{Teams: []domain.TeamRef{{ID: "t1", Name: "Design"}, {ID: "t2", Name: "Ops"}}}
	rec := internalDelete(h, "u1", hex.EncodeToString(testInternalSecret))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s, want 409", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"code":"owns_teams"`, `"details":{"teams":[`, `"name":"Design"`, `"id":"t2"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body %s missing %s", body, want)
		}
	}
}

func TestInternalPurgeBillingUnavailable502(t *testing.T) {
	h := withSecret(newHarness(t))
	h.lifecycle.purgeErr = fmt.Errorf("%w: cancel subscription: paddle 503", domain.ErrBillingUnavailable)
	rec := internalDelete(h, "u1", hex.EncodeToString(testInternalSecret))
	if rec.Code != http.StatusBadGateway || decodeErr(t, rec).Code != "billing_unavailable" {
		t.Fatalf("status=%d body=%s, want 502 billing_unavailable", rec.Code, rec.Body.String())
	}
}

func TestInternalPurgeIsNotCORSReflected(t *testing.T) {
	h := withSecret(newHarness(t))
	req := httptest.NewRequest(http.MethodOptions, "/v1/internal/users/u1", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "DELETE")
	rec := httptest.NewRecorder()
	h.handler().ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("internal routes must not reflect CORS origins: %v", rec.Header())
	}
}

// --- export -----------------------------------------------------------------

func TestExportStreamsZipWithHeaders(t *testing.T) {
	h := newHarness(t)
	h.export.files = map[string]string{"profile.json": `{"id":"user_1"}`}
	rec := h.authed(http.MethodGet, "/v1/me/export", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("Content-Type = %q", ct)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, `attachment; filename="calendium-export-`) || !strings.HasSuffix(cd, `.zip"`) {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("response is not a readable zip: %v", err)
	}
	if len(zr.File) != 1 || zr.File[0].Name != "profile.json" {
		t.Fatalf("zip entries = %v", zr.File)
	}
	if h.export.gotUserID != defaultUserID {
		t.Fatalf("Export userID = %q", h.export.gotUserID)
	}
}

func TestExportThrottled409RetryAfter(t *testing.T) {
	h := newHarness(t)
	h.export.err = &domain.ExportThrottledError{RetryAfter: 1800 * time.Second}
	rec := h.authed(http.MethodGet, "/v1/me/export", nil)
	if rec.Code != http.StatusConflict || decodeErr(t, rec).Code != "export_throttled" {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") != "1800" {
		t.Fatalf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("an error before the first file must be a JSON envelope, got %q", ct)
	}
}

func TestExportUnauthenticatedIs401(t *testing.T) {
	h := newHarness(t)
	if rec := h.anon(http.MethodGet, "/v1/me/export", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestExportUnderActAsIsForbidden(t *testing.T) {
	h, fd := delegHarness(t)
	rec := actAs(h, "principal_1", http.MethodGet, "/v1/me/export", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want 403 (export is never delegable)", rec.Code)
	}
	if fd.authorizeCalls != 0 || h.export.exportCalls != 0 {
		t.Fatal("rejected before any grant lookup or export")
	}
}

func TestExportMidStreamFailureDropsConnection(t *testing.T) {
	h := newHarness(t)
	h.export.files = map[string]string{"profile.json": `{"id":"user_1"}`}
	h.export.errAfterFirstFile = errors.New("db went away")
	srv := httptest.NewServer(h.handler())
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/me/export", nil)
	req.Header.Set("Authorization", "Bearer "+defaultToken)
	res, err := srv.Client().Do(req)
	if err != nil {
		return // connection reset before headers: acceptable
	}
	defer res.Body.Close()
	body, readErr := io.ReadAll(res.Body)
	if readErr == nil {
		if _, zerr := zip.NewReader(bytes.NewReader(body), int64(len(body))); zerr == nil {
			t.Fatal("a mid-stream failure must not produce a complete zip")
		}
	}
}

func TestExportNotWiredIs501(t *testing.T) {
	h := newHarness(t)
	h.deps.Export = nil
	if rec := h.authed(http.MethodGet, "/v1/me/export", nil); rec.Code != http.StatusNotImplemented {
		t.Fatalf("status=%d, want 501", rec.Code)
	}
}

// --- fakes ------------------------------------------------------------------

type fakeLifecycleService struct {
	purgeCalls int
	gotUserID  string
	purgeErr   error
}

func (f *fakeLifecycleService) Purge(_ context.Context, userID string) (port.PurgeReport, error) {
	f.purgeCalls++
	f.gotUserID = userID
	return port.PurgeReport{}, f.purgeErr
}

// fakeExportService writes `files` (in map order; tests use one file) and
// may fail before the first file (err) or after it (errAfterFirstFile).
type fakeExportService struct {
	files             map[string]string
	err               error
	errAfterFirstFile error
	exportCalls       int
	gotUserID         string
}

func (f *fakeExportService) Export(_ context.Context, userID string, sink port.ExportSink) error {
	f.exportCalls++
	f.gotUserID = userID
	if f.err != nil {
		return f.err
	}
	for name, body := range f.files {
		w, err := sink.Create(name)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(w, body); err != nil {
			return err
		}
	}
	return f.errAfterFirstFile
}

var (
	_ port.UserLifecycleService = (*fakeLifecycleService)(nil)
	_ port.ExportService        = (*fakeExportService)(nil)
)
```

And append to `backend/internal/adapter/in/httpapi/scheduling_handlers_test.go` (Review Focus 3 included):

```go
func TestUpdateSettingsAIBackgroundTriState(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantCall bool
		wantOn   bool
	}{
		{"absent keeps stored value", `{"timeZone":"UTC","workingHours":[],"workingLocation":""}`, false, false},
		{"explicit null keeps stored value", `{"timeZone":"UTC","workingHours":[],"workingLocation":"","aiBackground":null}`, false, false},
		{"true", `{"timeZone":"UTC","workingHours":[],"workingLocation":"","aiBackground":true}`, true, true},
		{"false", `{"timeZone":"UTC","workingHours":[],"workingLocation":"","aiBackground":false}`, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.settings.updateRet = domain.UserSettings{TimeZone: "UTC", WorkingHours: []domain.AvailabilityWindow{}, AIBackground: true}
			h.settings.setAIRet = domain.UserSettings{TimeZone: "UTC", WorkingHours: []domain.AvailabilityWindow{}, AIBackground: tc.wantOn}
			rec := h.authed(http.MethodPut, "/v1/settings", strings.NewReader(tc.body))
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if h.settings.gotUpdateUserID != defaultUserID {
				t.Fatal("Update must always run")
			}
			if (h.settings.setAICalls == 1) != tc.wantCall {
				t.Fatalf("SetAIBackground calls = %d, wantCall=%v", h.settings.setAICalls, tc.wantCall)
			}
			if tc.wantCall && h.settings.gotSetAI != tc.wantOn {
				t.Fatalf("SetAIBackground(%v), want %v", h.settings.gotSetAI, tc.wantOn)
			}
			if tc.wantCall && !strings.Contains(rec.Body.String(), fmt.Sprintf(`"aiBackground":%v`, tc.wantOn)) {
				t.Fatalf("response must carry the switch: %s", rec.Body.String())
			}
		})
	}
}

func TestUpdateSettingsInvalidAIBackgroundIs400(t *testing.T) {
	h := newHarness(t)
	rec := h.authed(http.MethodPut, "/v1/settings", strings.NewReader(`{"timeZone":"UTC","aiBackground":"yes"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
}
```

And append to `backend/internal/adapter/in/httpapi/middleware_test.go`:

```go
func TestCORSSkipsInternalRoutes(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := corsMiddleware(next, nil)
	req := httptest.NewRequest(http.MethodDelete, "/v1/internal/users/u1", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("/v1/internal/* must never reflect an Origin")
	}
	req = httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Fatal("ordinary routes keep reflecting allowed origins")
	}
}
```

- [ ] **Step 3: Extend the harness.** In `backend/internal/adapter/in/httpapi/harness_test.go` replace `fakeSettingsService` with:

```go
type fakeSettingsService struct {
	getRet domain.UserSettings
	getErr error

	updateRet       domain.UserSettings
	updateErr       error
	gotUpdateUserID string
	gotUpdateIn     domain.UserSettings

	setAIRet   domain.UserSettings
	setAIErr   error
	setAICalls int
	gotSetAI   bool
}

func (f *fakeSettingsService) Get(ctx context.Context, userID string) (domain.UserSettings, error) {
	return f.getRet, f.getErr
}
func (f *fakeSettingsService) Update(ctx context.Context, userID string, s domain.UserSettings) (domain.UserSettings, error) {
	f.gotUpdateUserID, f.gotUpdateIn = userID, s
	return f.updateRet, f.updateErr
}
func (f *fakeSettingsService) SetAIBackground(ctx context.Context, userID string, on bool) (domain.UserSettings, error) {
	f.setAICalls++
	f.gotSetAI = on
	return f.setAIRet, f.setAIErr
}
```

Add to the `harness` struct `lifecycle *fakeLifecycleService` and `export *fakeExportService`; in `newHarness` initialise `lifecycle: &fakeLifecycleService{}, export: &fakeExportService{},` and add `Lifecycle: h.lifecycle, Export: h.export,` to the `Deps{...}` literal (leave `InternalSecret` unset — tests opt in with `withSecret`).

- [ ] **Step 4: Run and confirm the failure.** `cd backend && go vet ./internal/adapter/in/httpapi/` — expected: `unknown field Lifecycle in struct literal of type Deps`, `h.deps.InternalSecret undefined`, `undefined: domain.ErrBillingUnavailable` only if piece 1 is missing (it is not), `fakeSettingsService` fine.

- [ ] **Step 5: Update `backend/internal/adapter/in/httpapi/codec.go`.** `errorDetail` becomes:

```go
type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Details carries structured, client-safe context for a few codes
	// (owns_teams → {teams:[{id,name}]}; payment_required → piece 1's
	// reason payload). Omitted for every other error.
	Details any `json:"details,omitempty"`
}
```

(if piece 1 already added an identical `Details any` field, keep its version). In `statusFor`, add before the `default:` case:

```go
	case errors.Is(err, domain.ErrOwnsTeams):
		return http.StatusConflict, "owns_teams"
	case errors.Is(err, domain.ErrExportThrottled):
		return http.StatusConflict, "export_throttled"
	case errors.Is(err, domain.ErrBillingUnavailable):
		return http.StatusBadGateway, "billing_unavailable"
```

(skip the `ErrBillingUnavailable` case if piece 1's codec already maps it — `grep -n ErrBillingUnavailable backend/internal/adapter/in/httpapi/codec.go`). In `safeMessage`, add:

```go
	case "owns_teams":
		return "Transfer ownership of your teams before deleting your account."
	case "export_throttled":
		return "You exported your data recently. Try again later."
	case "billing_unavailable":
		return "Billing is temporarily unavailable. Try again later."
```

(again skip `billing_unavailable` if present). Replace `writeError` with:

```go
// writeError renders the `{ "error": { code, message[, details] } }`
// envelope with a stable per-code message. Every failure is logged
// server-side with full detail; the wrapped error text is never echoed to
// clients. owns_teams carries the blocking team list; export_throttled sets
// Retry-After (whole seconds, rounded up).
func (s *server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := statusFor(err)
	if status >= http.StatusInternalServerError {
		s.deps.Logger.Error("request failed",
			"method", r.Method, "path", r.URL.Path, "status", status, "error", err)
	} else {
		s.deps.Logger.Info("request rejected",
			"method", r.Method, "path", r.URL.Path, "status", status, "code", code, "error", err)
	}
	detail := errorDetail{Code: code, Message: safeMessage(code)}
	var ownsTeams *domain.OwnsTeamsError
	if errors.As(err, &ownsTeams) {
		detail.Details = map[string]any{"teams": ownsTeams.Teams}
	}
	var throttled *domain.ExportThrottledError
	if errors.As(err, &throttled) {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(throttled.RetryAfter.Seconds()))))
	}
	writeJSON(w, status, errorBody{Error: detail})
}
```

(add `"math"` and `"strconv"` to the imports). If piece 1's `writeError` already builds `detail` with a 402 `Details` payload, merge: keep its 402 branch and add the two `errors.As` branches above.

- [ ] **Step 6: Create `backend/internal/adapter/in/httpapi/lifecycle.go`:**

```go
package httpapi

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"time"

	"calendium/backend/internal/domain"
)

const (
	internalSecretHeader = "X-Internal-Secret"
	exportTimeout        = 10 * time.Minute
)

// internalSecretMatches compares the hex header to the configured secret in
// constant time over SHA-256 digests, so neither the length nor a prefix of
// the secret leaks through timing. Malformed or wrong-length hex never
// matches; case and surrounding whitespace are tolerated.
func internalSecretMatches(header string, secret []byte) bool {
	if len(secret) == 0 {
		return false
	}
	raw, err := hex.DecodeString(strings.TrimSpace(header))
	if err != nil {
		return false
	}
	got, want := sha256.Sum256(raw), sha256.Sum256(secret)
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1 && len(raw) == len(secret)
}

// handleInternalPurgeUser is DELETE /v1/internal/users/{id}: authenticated
// by the shared secret (never a JWT) and registered outside authed(...).
// Without a configured secret, a wired service, or a matching header it
// answers exactly like an unknown route, so probing cannot tell the two
// apart. Logs carry the user id only (it is the path).
func (s *server) handleInternalPurgeUser(w http.ResponseWriter, r *http.Request) {
	if s.deps.Lifecycle == nil || !internalSecretMatches(r.Header.Get(internalSecretHeader), s.deps.InternalSecret) {
		http.NotFound(w, r)
		return
	}
	if _, err := s.deps.Lifecycle.Purge(r.Context(), r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// exportSink adapts the HTTP response into a port.ExportSink: the headers
// and the 200 status go out on the first file, the zip is flushed to the
// client after every file, and a failure before the first file still gets a
// JSON error envelope because nothing has been written yet.
type exportSink struct {
	w        http.ResponseWriter
	rc       *http.ResponseController
	zw       *zip.Writer
	filename string
	started  bool
}

func (s *exportSink) Create(name string) (io.Writer, error) {
	if s.started {
		if err := s.zw.Flush(); err != nil {
			return nil, err
		}
		_ = s.rc.Flush()
	} else {
		h := s.w.Header()
		h.Set("Content-Type", "application/zip")
		h.Set("Content-Disposition", `attachment; filename="`+s.filename+`"`)
		h.Set("Cache-Control", "no-store")
		s.w.WriteHeader(http.StatusOK)
		s.started = true
	}
	return s.zw.Create(name)
}

// handleExport is GET /v1/me/export: streams calendium-export-<date>.zip
// under a 10-minute budget. Act-as is rejected before this runs (the route
// is absent from delegationScopeForRoute → 403). Not entitlement-gated.
func (s *server) handleExport(w http.ResponseWriter, r *http.Request) {
	if s.deps.Export == nil {
		s.writeError(w, r, domain.ErrNotImplemented)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), exportTimeout)
	defer cancel()
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(exportTimeout))
	userID := userFrom(r).ID
	sink := &exportSink{
		w: w, rc: rc, zw: zip.NewWriter(w),
		filename: "calendium-export-" + time.Now().UTC().Format("2006-01-02") + ".zip",
	}
	if err := s.deps.Export.Export(ctx, userID, sink); err != nil {
		if !sink.started {
			s.writeError(w, r, err)
			return
		}
		// Bytes are already on the wire: abort without the central
		// directory so the client sees a corrupt (incomplete) zip rather
		// than a plausible partial export. The throttle slot stays claimed.
		s.deps.Logger.Error("export aborted mid-stream", "userId", userID, "error", err)
		panic(http.ErrAbortHandler)
	}
	if err := sink.zw.Close(); err != nil {
		s.deps.Logger.Error("export close failed", "userId", userID, "error", err)
		return
	}
	_ = rc.Flush()
}
```

- [ ] **Step 7: Wire `Deps` and routes in `backend/internal/adapter/in/httpapi/httpapi.go`.** After the `Insights port.InsightsService` field add:

```go
	// Lifecycle purges accounts (DELETE /v1/internal/users/{id}, called by
	// Better Auth's beforeDelete hook). When nil, or when InternalSecret is
	// empty, the internal route answers like an unknown route.
	Lifecycle port.UserLifecycleService
	// Export streams a user's data (GET /v1/me/export). When nil → 501.
	Export port.ExportService
	// InternalSecret is INTERNAL_API_SECRET decoded (32 bytes). It is the
	// only credential the /v1/internal/* surface accepts.
	InternalSecret []byte
```

After the integrations-callback `mux.HandleFunc(...)` line add:

```go
	// Account deletion, server-to-server from the web app (shared secret,
	// never a JWT). Outside authed(...) like the webhook; the CORS layer
	// skips /v1/internal/* and the self-hosting proxy must block it.
	mux.HandleFunc("DELETE /v1/internal/users/{id}", s.handleInternalPurgeUser)
```

After `authed("PUT /v1/me/preferences", s.handleUpdatePreferences)` add:

```go
	authed("GET /v1/me/export", s.handleExport)
```

- [ ] **Step 8: Skip `/v1/internal/` in `corsMiddleware` (`backend/internal/adapter/in/httpapi/middleware.go`).** As the first statement inside the returned `http.HandlerFunc`:

```go
		if strings.HasPrefix(r.URL.Path, "/v1/internal/") {
			// Server-to-server only: never advertise or reflect browser origins.
			next.ServeHTTP(w, r)
			return
		}
```

- [ ] **Step 9: Tri-state `aiBackground` in `handleUpdateSettings` (`backend/internal/adapter/in/httpapi/scheduling.go`).** Replace the handler with:

```go
// handleUpdateSettings decodes the document twice: once into the domain
// struct (full replace of time zone / hours / location) and once as a raw
// map so aiBackground is tri-state — absent or null leaves the stored switch
// alone (older mobile/desktop builds PUT the whole document without it),
// true/false writes it through SetAIBackground.
func (s *server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		s.writeError(w, r, fmt.Errorf("%w: invalid JSON body: %v", domain.ErrValidation, err))
		return
	}
	var in domain.UserSettings
	if err := json.Unmarshal(body, &in); err != nil {
		s.writeError(w, r, fmt.Errorf("%w: invalid JSON body: %v", domain.ErrValidation, err))
		return
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		s.writeError(w, r, fmt.Errorf("%w: invalid JSON body: %v", domain.ErrValidation, err))
		return
	}
	aiBackground, err := optionalField[bool](raw, "aiBackground")
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	userID := userFrom(r).ID
	settings, err := s.deps.Settings.Update(r.Context(), userID, in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if aiBackground != nil && *aiBackground != nil {
		settings, err = s.deps.Settings.SetAIBackground(r.Context(), userID, **aiBackground)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, settings)
}
```

Add `"encoding/json"` and `"io"` to that file's imports.

- [ ] **Step 10: Run the package.** `cd backend && go vet ./internal/adapter/in/httpapi/ && go test ./internal/adapter/in/httpapi/` — expected: `ok` (new tests pass; `TestExportMidStreamFailureDropsConnection` passes either by connection error or by an unreadable zip).

- [ ] **Step 11: Commit.**

```bash
git add backend/internal/adapter/in/httpapi
git commit -m "feat(httpapi): internal purge route (secret-authed, 404-indistinguishable), streamed export, error details/Retry-After, aiBackground tri-state" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 11: Composition — wire lifecycle + export into `cmd/api`, settings into the worker's sync (Track B; after Track A)

**Files:**
- Modify: `backend/cmd/api/main.go` (service block after `settingsSvc := service.NewSettingsService(store.UserSettings())` ~L246; `httpapi.Deps{...}` literal ~L452–491)
- Modify: `backend/cmd/worker/main.go` (`SyncServiceDeps{...}` literal ~L151–175)

**Interfaces:**
- Consumes: `service.NewUserLifecycleService`, `service.NewExportService`, `postgres.NewTeamRepo`, `postgres.NewDelegationRepo`, `store.UserExports()`, `store.EventNotes()`, `store.Tasks()`, `cfg.Crypto.InternalAPISecret`, and piece 1's wired Paddle client — the `port.Payments` value `main.go` passes to `service.NewBillingService` (named `paddleClient` in piece 1's composition task; if that task used another identifier, use it — it is the only `port.Payments` constructed in `main.go`).
- Produces: a running API with `Lifecycle`, `Export`, `InternalSecret` set; a worker whose `SyncService` honours `aiBackground`.

- [ ] **Step 1: Wire the services in `backend/cmd/api/main.go`.** Directly after `settingsSvc := service.NewSettingsService(store.UserSettings())` add:

```go
	// Account deletion (piece 3). A live Paddle subscription is cancelled
	// first; self-hosted instances have no biller, so Payments stays nil and
	// the cancel step is skipped.
	var lifecyclePayments port.Payments
	if !cfg.Instance.SelfHosted {
		lifecyclePayments = paddleClient
	}
	lifecycle := service.NewUserLifecycleService(service.UserLifecycleDeps{
		Users:         store.Users(),
		Teams:         postgres.NewTeamRepo(store),
		Delegations:   postgres.NewDelegationRepo(store),
		Subscriptions: store.Subscriptions(),
		Payments:      lifecyclePayments,
		Tx:            store,
		Clock:         clock,
		Logger:        logger,
	})
	// Data export (GET /v1/me/export): one repo per zip file.
	exportSvc := service.NewExportService(service.ExportDeps{
		Users: store.Users(), Accounts: store.Accounts(), Calendars: store.Calendars(), Events: store.Events(),
		Threads: store.Threads(), Messages: store.Messages(), Drafts: store.Drafts(), Snippets: store.Snippets(),
		Templates: store.EventTemplates(), Sets: store.CalendarSets(), Tasks: store.Tasks(),
		Links: store.BookingLinks(), Bookings: store.Bookings(), Polls: store.Polls(), Labels: store.Labels(),
		EventNotes: store.EventNotes(), Prefs: store.Prefs(), Preferences: store.UserPreferences(),
		CalendarPrefs: store.CalendarPrefs(), Settings: store.UserSettings(), Exports: store.UserExports(),
		Clock: clock,
	})
```

In the `httpapi.Deps{...}` literal, after `Insights:           insightsSvc,` add:

```go
		// Piece 3: account deletion (secret-authed internal route) + export.
		Lifecycle:      lifecycle,
		Export:         exportSvc,
		InternalSecret: cfg.Crypto.InternalAPISecret,
```

Add `"lifecycle", lifecycle != nil, "export", exportSvc != nil,` to the `logger.Info("api: optional deps", ...)` call. Ensure `"calendium/backend/internal/port"` is imported (it already is for `pushSender`).

- [ ] **Step 2: Wire settings into the worker's sync (`backend/cmd/worker/main.go`).** In the `service.NewSyncService(service.SyncServiceDeps{...})` literal, after `CalendarPrefs: store.CalendarPrefs(),` add:

```go
		// Settings → AI → "Background AI processing" gates every enqueue.
		UserSettings: store.UserSettings(),
```

- [ ] **Step 3: Build and vet everything.** `cd backend && go build ./... && go vet ./... && go test ./...` — expected: all `ok` (the Postgres suite skips without Docker; with Docker it passes).

- [ ] **Step 4: Commit.**

```bash
git add backend/cmd/api/main.go backend/cmd/worker/main.go
git commit -m "feat(cmd): wire UserLifecycleService, ExportService, INTERNAL_API_SECRET and the sync background-AI gate" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 12: Shared contract — `aiBackground`, `ApiRequestError.details`/`retryAfterSeconds`, `downloadExport()` (Track C)

**Files:**
- Modify: `packages/shared/src/types.ts` (`UserSettings` L684–688)
- Modify: `packages/shared/src/client.ts` (`ApiRequestError` L124–133; `request()` L142–163; append `downloadExport` after `updateSettings` L608–610)
- Modify: `packages/shared/src/client.test.ts` (the `UserSettings` literal around L635: add `aiBackground: true,`)
- Create: `packages/shared/src/lifecycle.test.ts`

**Interfaces:**
- Produces: `UserSettings.aiBackground: boolean`; `ApiRequestError` constructor `(status, code, message, details?: unknown)` with `readonly details?: unknown` and mutable `retryAfterSeconds?: number`; `ApiClient.downloadExport(): Promise<Blob>`.

- [ ] **Step 1: Write the failing tests** at `packages/shared/src/lifecycle.test.ts`:

```ts
import { describe, expect, it, vi } from 'vitest';
import { ApiClient, ApiRequestError } from './client';
import type { UserSettings } from './types';

const BASE = 'https://api.test';

function makeClient(fetchFn: (...args: unknown[]) => Promise<Response>) {
  return new ApiClient({
    baseUrl: BASE,
    getAccessToken: async () => 'tok',
    fetch: fetchFn as unknown as typeof fetch,
  });
}

function jsonResponse(status: number, body: unknown, headers: Record<string, string> = {}) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  });
}

describe('downloadExport', () => {
  it('GETs /v1/me/export with the bearer token and returns the raw zip blob (no JSON parsing)', async () => {
    const bytes = new Uint8Array([0x50, 0x4b, 0x03, 0x04]);
    const fetchFn = vi.fn(
      async () => new Response(bytes, { status: 200, headers: { 'Content-Type': 'application/zip' } })
    );
    const blob = await makeClient(fetchFn).downloadExport();
    expect(blob).toBeInstanceOf(Blob);
    expect(new Uint8Array(await blob.arrayBuffer())).toEqual(bytes);
    const [url, init] = fetchFn.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe(`${BASE}/v1/me/export`);
    expect(init.method).toBe('GET');
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer tok');
    expect((init.headers as Record<string, string>).Accept).toBe('application/zip');
    expect(init.body).toBeUndefined();
  });

  it('maps 409 export_throttled to ApiRequestError with retryAfterSeconds from Retry-After', async () => {
    const fetchFn = vi.fn(async () =>
      jsonResponse(409, { error: { code: 'export_throttled', message: 'Try later' } }, { 'Retry-After': '1800' })
    );
    const err = await makeClient(fetchFn).downloadExport().catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiRequestError);
    const typed = err as ApiRequestError;
    expect(typed.status).toBe(409);
    expect(typed.code).toBe('export_throttled');
    expect(typed.retryAfterSeconds).toBe(1800);
  });

  it('leaves retryAfterSeconds undefined when the header is missing or unparsable', async () => {
    const fetchFn = vi.fn(async () =>
      jsonResponse(500, { error: { code: 'internal', message: 'boom' } }, { 'Retry-After': 'soon' })
    );
    const err = (await makeClient(fetchFn).downloadExport().catch((e: unknown) => e)) as ApiRequestError;
    expect(err.code).toBe('internal');
    expect(err.retryAfterSeconds).toBeUndefined();
  });
});

describe('ApiRequestError.details', () => {
  it('request() carries error.details from the envelope', async () => {
    const fetchFn = vi.fn(async () =>
      jsonResponse(409, { error: { code: 'conflict', message: 'x', details: { teams: [{ id: 't1', name: 'Design' }] } } })
    );
    const err = (await makeClient(fetchFn).getSettings().catch((e: unknown) => e)) as ApiRequestError;
    expect(err).toBeInstanceOf(ApiRequestError);
    expect(err.details).toEqual({ teams: [{ id: 't1', name: 'Design' }] });
  });

  it('is undefined when the envelope has no details', async () => {
    const fetchFn = vi.fn(async () => jsonResponse(404, { error: { code: 'not_found', message: 'x' } }));
    const err = (await makeClient(fetchFn).getSettings().catch((e: unknown) => e)) as ApiRequestError;
    expect(err.details).toBeUndefined();
  });
});

describe('UserSettings.aiBackground', () => {
  it('updateSettings PUTs the switch as part of the document', async () => {
    const doc: UserSettings = { timeZone: 'UTC', workingHours: [], workingLocation: '', aiBackground: false };
    const fetchFn = vi.fn(async () => jsonResponse(200, doc));
    const got = await makeClient(fetchFn).updateSettings(doc);
    expect(got.aiBackground).toBe(false);
    const [, init] = fetchFn.mock.calls[0] as unknown as [string, RequestInit];
    expect(JSON.parse(init.body as string)).toMatchObject({ aiBackground: false });
  });
});
```

- [ ] **Step 2: Run and confirm the failure.** `bun run test:shared -- lifecycle` — expected: `downloadExport is not a function`, `retryAfterSeconds`/`details` undefined, and a TS complaint on `aiBackground` in the literal.

- [ ] **Step 3: Update `packages/shared/src/types.ts`.** Replace the `UserSettings` interface:

```ts
/** Per-user scheduling preferences (working hours in timeZone, displayed location) plus the background-AI switch. */
export interface UserSettings {
  timeZone: string;
  workingHours: AvailabilityWindow[];
  workingLocation: string;
  /**
   * Settings → AI → "Background AI processing". Off skips every automatic
   * AI job at sync (summaries, quick replies, auto drafts, classifiers,
   * writing-style profile, reminder detection); on-demand actions are
   * unaffected. Default true. Omitting it on PUT keeps the stored value.
   */
  aiBackground: boolean;
}
```

- [ ] **Step 4: Update `packages/shared/src/client.ts`.** Replace `ApiRequestError`:

```ts
export class ApiRequestError extends Error {
  /** Seconds to wait before retrying, from a Retry-After header (export throttle). */
  retryAfterSeconds?: number;

  constructor(
    public readonly status: number,
    public readonly code: string,
    message: string,
    /** Structured, client-safe context from the error envelope (`error.details`). */
    public readonly details?: unknown
  ) {
    super(message);
    this.name = 'ApiRequestError';
  }
}
```

(If piece 1 already added `details` to the constructor, keep its declaration and only add the `retryAfterSeconds` field.) In `request()`, change the throw to `throw new ApiRequestError(res.status, code, message, json?.error?.details);` (again, skip if piece 1 already did). After `updateSettings(...)` add:

```ts
  /**
   * GET /v1/me/export — streams the account's data as a zip. Unlike every
   * other call this returns the raw Blob (no JSON parsing). A 409
   * export_throttled carries `retryAfterSeconds` from the Retry-After header.
   * Never delegable (403 under act-as). Mobile/desktop never call it: they
   * link to the web settings page instead.
   */
  async downloadExport(): Promise<Blob> {
    const token = await this.opts.getAccessToken();
    const doFetch = this.opts.fetch ?? fetch;
    const res = await doFetch(`${this.opts.baseUrl}/v1/me/export`, {
      method: 'GET',
      headers: {
        Accept: 'application/zip',
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
    });
    if (res.ok) return res.blob();
    const json = await res.json().catch(() => null);
    const code = json?.error?.code ?? 'unknown';
    const message = json?.error?.message ?? `Request failed with status ${res.status}`;
    const err = new ApiRequestError(res.status, code, message, json?.error?.details);
    const retryAfter = Number(res.headers.get('Retry-After'));
    if (Number.isFinite(retryAfter) && retryAfter > 0) err.retryAfterSeconds = retryAfter;
    throw err;
  }
```

- [ ] **Step 5: Fix the existing `UserSettings` literal.** In `packages/shared/src/client.test.ts`, the object containing `workingLocation: 'home',` (≈L635) gains `aiBackground: true,` on the next line. `grep -n "workingLocation:" packages/shared/src/*.ts` must show only that literal and the type.

- [ ] **Step 6: Run the shared suite.** `bun run test:shared` — expected: all pass including the six new tests.

- [ ] **Step 7: Commit.**

```bash
git add packages/shared/src/types.ts packages/shared/src/client.ts packages/shared/src/client.test.ts packages/shared/src/lifecycle.test.ts
git commit -m "feat(shared): UserSettings.aiBackground, ApiRequestError.details/retryAfterSeconds, downloadExport()" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 13: Web server side — `purgeOnApi`, Better Auth `deleteUser` hooks, `freshAge`, runtime env check (Track C)

**Files:**
- Create: `apps/web/lib/internal-api.ts`
- Create: `apps/web/lib/internal-api.test.ts`
- Modify: `apps/web/lib/auth.ts` (imports L1–5; the `betterAuth({...})` options L148–180)
- Create: `apps/web/lib/auth-lifecycle.test.ts`
- Create: `apps/web/lib/runtime-env.ts`
- Create: `apps/web/lib/runtime-env.test.ts`
- Create: `apps/web/instrumentation.ts`

**Interfaces:**
- Consumes: `APIError` from `better-auth/api`; `db()` (module-private pg pool in `auth.ts`).
- Produces: `purgeOnApi(userId: string, opts?: { fetchImpl?: typeof fetch; env?: NodeJS.ProcessEnv }): Promise<void>` (throws `APIError`); `deleteVerificationRows(store: { query(sql: string, params: unknown[]): Promise<unknown> }, email: string): Promise<void>`; `internalApiUrl(env?)`; `INTERNAL_API_TIMEOUT_MS = 15_000`; `checkRuntimeEnv(env: NodeJS.ProcessEnv): RuntimeEnvProblem[]`; `register()` in `instrumentation.ts`; `auth.options.user.deleteUser.{enabled, beforeDelete, afterDelete}`, `auth.options.session.freshAge === 300`.

- [ ] **Step 1: Write the failing tests.** `apps/web/lib/internal-api.test.ts`:

```ts
import { APIError } from 'better-auth/api';
import { describe, expect, it, vi } from 'vitest';

import { deleteVerificationRows, internalApiUrl, purgeOnApi } from '@/lib/internal-api';

const SECRET = 'ab'.repeat(32);
const ENV = { INTERNAL_API_URL: 'http://api:8080/', INTERNAL_API_SECRET: SECRET } as NodeJS.ProcessEnv;

function fetchReturning(status: number, body?: unknown) {
  return vi.fn(
    async () =>
      new Response(body === undefined ? null : JSON.stringify(body), {
        status,
        headers: { 'Content-Type': 'application/json' },
      })
  );
}

describe('internalApiUrl', () => {
  it('defaults to localhost:8080 and strips trailing slashes', () => {
    expect(internalApiUrl({} as NodeJS.ProcessEnv)).toBe('http://localhost:8080');
    expect(internalApiUrl(ENV)).toBe('http://api:8080');
  });
});

describe('purgeOnApi', () => {
  it('DELETEs /v1/internal/users/{id} with the secret header and an abort signal', async () => {
    const fetchImpl = fetchReturning(204);
    await purgeOnApi('user 1', { fetchImpl: fetchImpl as unknown as typeof fetch, env: ENV });
    const [url, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe('http://api:8080/v1/internal/users/user%201');
    expect(init.method).toBe('DELETE');
    expect((init.headers as Record<string, string>)['X-Internal-Secret']).toBe(SECRET);
    expect(init.signal).toBeInstanceOf(AbortSignal);
  });

  it('maps 409 to APIError CONFLICT carrying the envelope code, message and details', async () => {
    const fetchImpl = fetchReturning(409, {
      error: { code: 'owns_teams', message: 'Transfer first', details: { teams: [{ id: 't1', name: 'Design' }] } },
    });
    const err = (await purgeOnApi('u1', { fetchImpl: fetchImpl as unknown as typeof fetch, env: ENV }).catch(
      (e: unknown) => e
    )) as APIError;
    expect(err).toBeInstanceOf(APIError);
    expect(err.statusCode).toBe(409);
    expect(err.body?.code).toBe('owns_teams');
    expect((err.body as { details?: { teams?: { name: string }[] } }).details?.teams?.[0]?.name).toBe('Design');
  });

  it('maps any other non-204 status to SERVICE_UNAVAILABLE', async () => {
    for (const status of [500, 502, 404]) {
      const fetchImpl = fetchReturning(status, { error: { code: 'x', message: 'y' } });
      const err = (await purgeOnApi('u1', { fetchImpl: fetchImpl as unknown as typeof fetch, env: ENV }).catch(
        (e: unknown) => e
      )) as APIError;
      expect(err).toBeInstanceOf(APIError);
      expect(err.statusCode).toBe(503);
    }
  });

  it('maps a network failure / timeout to SERVICE_UNAVAILABLE', async () => {
    const fetchImpl = vi.fn(async () => {
      throw new DOMException('aborted', 'TimeoutError');
    });
    const err = (await purgeOnApi('u1', { fetchImpl: fetchImpl as unknown as typeof fetch, env: ENV }).catch(
      (e: unknown) => e
    )) as APIError;
    expect(err).toBeInstanceOf(APIError);
    expect(err.statusCode).toBe(503);
  });

  it('refuses without a configured secret and never calls fetch', async () => {
    const fetchImpl = fetchReturning(204);
    const err = (await purgeOnApi('u1', {
      fetchImpl: fetchImpl as unknown as typeof fetch,
      env: { INTERNAL_API_URL: 'http://api:8080' } as NodeJS.ProcessEnv,
    }).catch((e: unknown) => e)) as APIError;
    expect(err).toBeInstanceOf(APIError);
    expect(err.statusCode).toBe(503);
    expect(fetchImpl).not.toHaveBeenCalled();
  });
});

describe('deleteVerificationRows', () => {
  it('deletes by identifier', async () => {
    const query = vi.fn(async () => ({ rowCount: 1 }));
    await deleteVerificationRows({ query }, 'me@example.com');
    expect(query).toHaveBeenCalledWith('DELETE FROM "verification" WHERE identifier = $1', ['me@example.com']);
  });

  it('is best effort: a failing query never throws', async () => {
    const query = vi.fn(async () => {
      throw new Error('db down');
    });
    await expect(deleteVerificationRows({ query }, 'me@example.com')).resolves.toBeUndefined();
  });
});
```

`apps/web/lib/auth-lifecycle.test.ts`:

```ts
import { describe, expect, it } from 'vitest';

// Importing lib/auth constructs the Better Auth server; `db()` builds a pg
// Pool lazily (no connection), so this is safe without env vars — see
// auth.test.ts.
import { auth } from '@/lib/auth';

describe('Better Auth account deletion options', () => {
  it('enables deleteUser with before/after hooks and no email verification (piece 2 is not a dependency)', () => {
    const del = auth.options.user?.deleteUser;
    expect(del?.enabled).toBe(true);
    expect(typeof del?.beforeDelete).toBe('function');
    expect(typeof del?.afterDelete).toBe('function');
    expect(del?.sendDeleteAccountVerification).toBeUndefined();
  });

  it('requires a session no older than 300 s for deleteUser on social-only accounts', () => {
    expect(auth.options.session?.freshAge).toBe(300);
  });
});
```

`apps/web/lib/runtime-env.test.ts`:

```ts
import { describe, expect, it } from 'vitest';

import { checkRuntimeEnv } from '@/lib/runtime-env';

const OK = { BETTER_AUTH_SECRET: 'x'.repeat(32), INTERNAL_API_SECRET: 'ab'.repeat(32) } as NodeJS.ProcessEnv;

describe('checkRuntimeEnv', () => {
  it('passes with both secrets', () => {
    expect(checkRuntimeEnv(OK)).toEqual([]);
  });
  it('reports a missing INTERNAL_API_SECRET', () => {
    const problems = checkRuntimeEnv({ BETTER_AUTH_SECRET: 'x' } as NodeJS.ProcessEnv);
    expect(problems.map((p) => p.name)).toEqual(['INTERNAL_API_SECRET']);
  });
  it('reports a malformed INTERNAL_API_SECRET (not 64 hex chars)', () => {
    const problems = checkRuntimeEnv({ ...OK, INTERNAL_API_SECRET: 'not-hex' } as NodeJS.ProcessEnv);
    expect(problems[0]?.reason).toMatch(/64 hex/);
  });
  it('reports a missing BETTER_AUTH_SECRET too', () => {
    const problems = checkRuntimeEnv({ INTERNAL_API_SECRET: 'ab'.repeat(32) } as NodeJS.ProcessEnv);
    expect(problems.map((p) => p.name)).toEqual(['BETTER_AUTH_SECRET']);
  });
});
```

- [ ] **Step 2: Run and confirm the failure.** `bun run test:web -- internal-api auth-lifecycle runtime-env` — expected: `Cannot find module '@/lib/internal-api'`, `'@/lib/runtime-env'`; `auth.options.user?.deleteUser` undefined.

- [ ] **Step 3: Create `apps/web/lib/internal-api.ts`:**

```ts
import { APIError } from 'better-auth/api';

/**
 * Server-only bridge from Better Auth's account-deletion hook to the Go API's
 * secret-authenticated internal surface. Never import from client components:
 * it reads INTERNAL_API_SECRET.
 */

export const INTERNAL_API_TIMEOUT_MS = 15_000;

export interface PurgeTeam {
  id: string;
  name: string;
}

interface PurgeErrorBody {
  error?: { code?: string; message?: string; details?: { teams?: PurgeTeam[] } };
}

/** Base URL of the Go API as reachable from the web server (compose: http://api:8080). */
export function internalApiUrl(env: NodeJS.ProcessEnv = process.env): string {
  return (env.INTERNAL_API_URL || 'http://localhost:8080').replace(/\/+$/, '');
}

/**
 * DELETE /v1/internal/users/{id}. Throwing aborts Better Auth's deleteUser
 * and keeps the auth rows intact: 409 → CONFLICT with the API's envelope
 * (owns_teams + details.teams), anything else → SERVICE_UNAVAILABLE. Logs
 * carry the status code only — never the user's email or name.
 */
export async function purgeOnApi(
  userId: string,
  opts: { fetchImpl?: typeof fetch; env?: NodeJS.ProcessEnv } = {}
): Promise<void> {
  const env = opts.env ?? process.env;
  const doFetch = opts.fetchImpl ?? fetch;
  const secret = env.INTERNAL_API_SECRET;
  if (!secret) {
    console.error('[account-delete] INTERNAL_API_SECRET is not configured');
    throw new APIError('SERVICE_UNAVAILABLE', { message: 'Account deletion is not available right now.' });
  }
  let res: Response;
  try {
    res = await doFetch(`${internalApiUrl(env)}/v1/internal/users/${encodeURIComponent(userId)}`, {
      method: 'DELETE',
      headers: { 'X-Internal-Secret': secret },
      signal: AbortSignal.timeout(INTERNAL_API_TIMEOUT_MS),
    });
  } catch (err) {
    console.error('[account-delete] purge request failed:', err instanceof Error ? err.name : 'error');
    throw new APIError('SERVICE_UNAVAILABLE', { message: 'Could not delete your account. Try again.' });
  }
  if (res.status === 204) return;
  console.error('[account-delete] purge status', res.status);
  if (res.status === 409) {
    const body = (await res.json().catch(() => null)) as PurgeErrorBody | null;
    throw new APIError('CONFLICT', {
      code: body?.error?.code ?? 'conflict',
      message: body?.error?.message ?? 'Your account cannot be deleted yet.',
      details: body?.error?.details ?? {},
    });
  }
  throw new APIError('SERVICE_UNAVAILABLE', { message: 'Could not delete your account. Try again.' });
}

export interface VerificationStore {
  query(sql: string, params: unknown[]): Promise<unknown>;
}

/**
 * afterDelete cleanup: Better Auth keys "verification" rows by email
 * (identifier), with no FK to "user", so they would outlive the account.
 * Best effort — the account is already gone when this runs.
 */
export async function deleteVerificationRows(store: VerificationStore, email: string): Promise<void> {
  try {
    await store.query('DELETE FROM "verification" WHERE identifier = $1', [email]);
  } catch (err) {
    console.error('[account-delete] verification cleanup failed:', err instanceof Error ? err.name : 'error');
  }
}
```

- [ ] **Step 4: Create `apps/web/lib/runtime-env.ts`:**

```ts
const HEX64 = /^[0-9a-f]{64}$/i;

export interface RuntimeEnvProblem {
  name: string;
  reason: string;
}

/**
 * Validates the server-only env the web app needs at RUNTIME (never at build:
 * `next build` runs with no secrets). BETTER_AUTH_SECRET was already required
 * by Better Auth; INTERNAL_API_SECRET is what account deletion uses to reach
 * the Go API and must equal the API's own value.
 */
export function checkRuntimeEnv(env: NodeJS.ProcessEnv): RuntimeEnvProblem[] {
  const problems: RuntimeEnvProblem[] = [];
  if (!env.BETTER_AUTH_SECRET) {
    problems.push({ name: 'BETTER_AUTH_SECRET', reason: 'is required (openssl rand -base64 32)' });
  }
  if (!env.INTERNAL_API_SECRET) {
    problems.push({
      name: 'INTERNAL_API_SECRET',
      reason: 'is required (openssl rand -hex 32; must match the API service)',
    });
  } else if (!HEX64.test(env.INTERNAL_API_SECRET)) {
    problems.push({ name: 'INTERNAL_API_SECRET', reason: 'must be exactly 64 hex chars' });
  }
  return problems;
}
```

- [ ] **Step 5: Create `apps/web/instrumentation.ts`** — or, if piece 2 (email + auth hardening) already created it, do NOT overwrite it: add the `checkRuntimeEnv` block below inside its existing `register()` after piece 2's own checks, keeping piece 2's guards and its `instrumentation.test.ts` green. Fresh file:

```ts
/**
 * Next.js instrumentation hook: runs once when the server process starts,
 * never during `next build`. A production, non-demo web server refuses to
 * start without the secrets account deletion depends on (the e2e suite runs
 * in demo mode and never calls the internal route, so it is exempt).
 */
export async function register(): Promise<void> {
  if (process.env.NEXT_RUNTIME !== 'nodejs') return;
  if (process.env.NEXT_PHASE === 'phase-production-build') return;
  if (process.env.NODE_ENV !== 'production' || process.env.NEXT_PUBLIC_DEMO_MODE === 'true') return;
  const { checkRuntimeEnv } = await import('./lib/runtime-env');
  const problems = checkRuntimeEnv(process.env);
  if (problems.length > 0) {
    throw new Error(`Refusing to start: ${problems.map((p) => `${p.name} ${p.reason}`).join('; ')}`);
  }
}
```

- [ ] **Step 6: Enable `deleteUser` in `apps/web/lib/auth.ts`.** Add the import `import { deleteVerificationRows, purgeOnApi } from '@/lib/internal-api';` after the `pg` import. In the `betterAuth({...})` call, after `socialProviders: socialProviders(),` add:

```ts
  // Account deletion (production-readiness piece 3). beforeDelete purges the
  // Go side first (throwing aborts the deletion and keeps the auth rows);
  // afterDelete drops the email-keyed "verification" rows Better Auth would
  // otherwise leave behind. No email confirmation: credential accounts prove
  // possession with their password, social-only accounts with a fresh
  // (<= freshAge) session.
  user: {
    deleteUser: {
      enabled: true,
      beforeDelete: async (user) => {
        await purgeOnApi(user.id);
      },
      afterDelete: async (user) => {
        await deleteVerificationRows(db(), user.email);
      },
    },
  },
  session: { freshAge: 300 },
```

- [ ] **Step 7: Run the tests.** `bun run test:web -- internal-api auth-lifecycle runtime-env auth.test` — expected: all pass (the pre-existing `auth.test.ts` still passes).

- [ ] **Step 8: Commit.**

```bash
git add apps/web/lib/internal-api.ts apps/web/lib/internal-api.test.ts apps/web/lib/auth.ts apps/web/lib/auth-lifecycle.test.ts apps/web/lib/runtime-env.ts apps/web/lib/runtime-env.test.ts apps/web/instrumentation.ts
git commit -m "feat(web): Better Auth deleteUser → internal purge (secret, 15s timeout, 409 details), freshAge 300, runtime env check" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 14: Web — `account-data.ts` helpers + `DeleteAccountDialog` (Track C)

**Files:**
- Create: `apps/web/lib/account-data.ts`
- Create: `apps/web/lib/account-data.test.ts`
- Create: `apps/web/components/app/delete-account-dialog.tsx`
- Create: `apps/web/components/app/delete-account-dialog.test.tsx`

**Interfaces:**
- Consumes: `getApiClient().downloadExport()` (Task 12), `authClient.{listAccounts,deleteUser}` (`better-auth/react`), `performSignOut()` (`apps/web/lib/sign-out.ts`), `useRouter` (`next/navigation`), UI `Dialog*`, `Button`, `Input`, `Label`.
- Produces: `downloadExportApi(): Promise<Blob>`; `exportFilename(now?: Date): string`; `saveBlob(blob: Blob, filename: string): void`; `retryMessage(seconds: number): string`; `DeleteAccountDialog({ open, onOpenChange, email })`; `normalizeEmail(value: string): string`; `describeDeleteError(error): { message: string; teams: BlockingTeam[] }`.

- [ ] **Step 1: Write the failing helper tests** at `apps/web/lib/account-data.test.ts`:

```ts
import { describe, expect, it } from 'vitest';

import { exportFilename, retryMessage } from '@/lib/account-data';

describe('exportFilename', () => {
  it('uses the calendar date', () => {
    expect(exportFilename(new Date(2026, 9, 4, 15, 30))).toBe('calendium-export-2026-10-04.zip');
  });
});

describe('retryMessage', () => {
  it('rounds up to whole minutes and never says 0', () => {
    expect(retryMessage(59)).toBe('You exported your data recently. Try again in 1 minute.');
    expect(retryMessage(61)).toBe('You exported your data recently. Try again in 2 minutes.');
    expect(retryMessage(1800)).toBe('You exported your data recently. Try again in 30 minutes.');
  });
});
```

- [ ] **Step 2: Write the failing dialog tests** at `apps/web/components/app/delete-account-dialog.test.tsx`:

```tsx
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const listAccountsMock = vi.fn();
const deleteUserMock = vi.fn();
vi.mock('@/lib/auth-client', () => ({
  authClient: {
    listAccounts: (...args: unknown[]) => listAccountsMock(...args),
    deleteUser: (...args: unknown[]) => deleteUserMock(...args),
  },
}));

const replaceMock = vi.fn();
vi.mock('next/navigation', () => ({
  useRouter: () => ({ replace: replaceMock, push: vi.fn() }),
}));

const performSignOutMock = vi.fn();
vi.mock('@/lib/sign-out', () => ({
  performSignOut: (...args: unknown[]) => performSignOutMock(...args),
}));

import { DeleteAccountDialog, describeDeleteError, normalizeEmail } from './delete-account-dialog';

const EMAIL = 'me@example.com';

function renderDialog() {
  return render(<DeleteAccountDialog open onOpenChange={() => {}} email={EMAIL} />);
}

const credentialAccounts = { data: [{ id: 'acc1', provider: 'credential' }], error: null };
const socialAccounts = { data: [{ id: 'acc2', provider: 'google' }], error: null };

beforeEach(() => {
  vi.clearAllMocks();
  listAccountsMock.mockResolvedValue(credentialAccounts);
  deleteUserMock.mockResolvedValue({ data: { success: true }, error: null });
  performSignOutMock.mockResolvedValue(undefined);
});

describe('DeleteAccountDialog', () => {
  it('keeps confirm disabled until the typed email matches', async () => {
    const user = userEvent.setup();
    renderDialog();
    const confirm = await screen.findByRole('button', { name: 'Delete my account' });
    expect(confirm).toBeDisabled();
    await user.type(screen.getByLabelText(/type your email/i), 'wrong@example.com');
    await user.type(await screen.findByLabelText('Your password'), 'hunter2');
    expect(confirm).toBeDisabled();
    await user.clear(screen.getByLabelText(/type your email/i));
    await user.type(screen.getByLabelText(/type your email/i), EMAIL);
    expect(confirm).toBeEnabled();
  });

  // Review Focus 4: case and whitespace are normalized, not byte-compared.
  it('enables confirm when the typed email matches ignoring case and whitespace', async () => {
    const user = userEvent.setup();
    renderDialog();
    await user.type(await screen.findByLabelText(/type your email/i), '  Me@Example.com ');
    await user.type(await screen.findByLabelText('Your password'), 'hunter2');
    expect(screen.getByRole('button', { name: 'Delete my account' })).toBeEnabled();
  });

  it('shows the password field only when a credential account exists', async () => {
    listAccountsMock.mockResolvedValue(socialAccounts);
    const user = userEvent.setup();
    renderDialog();
    await waitFor(() => expect(listAccountsMock).toHaveBeenCalled());
    await user.type(await screen.findByLabelText(/type your email/i), EMAIL);
    expect(screen.queryByLabelText('Your password')).toBeNull();
    expect(screen.getByRole('button', { name: 'Delete my account' })).toBeEnabled();
  });

  it('calls deleteUser with the password for credential accounts', async () => {
    const user = userEvent.setup();
    renderDialog();
    await user.type(await screen.findByLabelText(/type your email/i), EMAIL);
    await user.type(await screen.findByLabelText('Your password'), 'hunter2');
    await user.click(screen.getByRole('button', { name: 'Delete my account' }));
    await waitFor(() => expect(deleteUserMock).toHaveBeenCalledWith({ password: 'hunter2' }));
  });

  it('calls deleteUser without a password for social-only accounts', async () => {
    listAccountsMock.mockResolvedValue(socialAccounts);
    const user = userEvent.setup();
    renderDialog();
    await waitFor(() => expect(listAccountsMock).toHaveBeenCalled());
    await user.type(await screen.findByLabelText(/type your email/i), EMAIL);
    await user.click(screen.getByRole('button', { name: 'Delete my account' }));
    await waitFor(() => expect(deleteUserMock).toHaveBeenCalledWith({}));
  });

  it('renders the blocking team list on owns_teams and does not navigate', async () => {
    deleteUserMock.mockResolvedValue({
      data: null,
      error: { status: 409, code: 'owns_teams', message: 'x', details: { teams: [{ id: 't1', name: 'Design' }, { id: 't2', name: 'Ops' }] } },
    });
    const user = userEvent.setup();
    renderDialog();
    await user.type(await screen.findByLabelText(/type your email/i), EMAIL);
    await user.type(await screen.findByLabelText('Your password'), 'hunter2');
    await user.click(screen.getByRole('button', { name: 'Delete my account' }));
    expect(await screen.findByText('Design')).toBeInTheDocument();
    expect(screen.getByText('Ops')).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent(/transfer ownership/i);
    expect(replaceMock).not.toHaveBeenCalled();
    expect(performSignOutMock).not.toHaveBeenCalled();
  });

  it('explains a stale social session', async () => {
    listAccountsMock.mockResolvedValue(socialAccounts);
    deleteUserMock.mockResolvedValue({ data: null, error: { status: 403, code: 'SESSION_NOT_FRESH', message: 'fresh' } });
    const user = userEvent.setup();
    renderDialog();
    await waitFor(() => expect(listAccountsMock).toHaveBeenCalled());
    await user.type(await screen.findByLabelText(/type your email/i), EMAIL);
    await user.click(screen.getByRole('button', { name: 'Delete my account' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(/sign in again/i);
  });

  it('on success routes to /goodbye first, then signs out and clears local state', async () => {
    const user = userEvent.setup();
    renderDialog();
    await user.type(await screen.findByLabelText(/type your email/i), EMAIL);
    await user.type(await screen.findByLabelText('Your password'), 'hunter2');
    await user.click(screen.getByRole('button', { name: 'Delete my account' }));
    await waitFor(() => expect(performSignOutMock).toHaveBeenCalled());
    expect(replaceMock).toHaveBeenCalledWith('/goodbye');
    expect(replaceMock.mock.invocationCallOrder[0]).toBeLessThan(performSignOutMock.mock.invocationCallOrder[0]);
  });
});

describe('helpers', () => {
  it('normalizeEmail trims and lowercases', () => {
    expect(normalizeEmail('  Me@Example.com ')).toBe('me@example.com');
  });

  it('describeDeleteError maps codes to copy', () => {
    expect(describeDeleteError({ status: 400, code: 'INVALID_PASSWORD' }).message).toMatch(/password/i);
    expect(describeDeleteError({ status: 503 }).message).toMatch(/try again/i);
    expect(describeDeleteError({ status: 409, code: 'owns_teams', details: { teams: [{ id: 't1', name: 'A' }] } }).teams).toHaveLength(1);
  });
});
```

- [ ] **Step 3: Run and confirm the failure.** `bun run test:web -- account-data delete-account-dialog` — expected: `Cannot find module` for both.

- [ ] **Step 4: Create `apps/web/lib/account-data.ts`:**

```ts
import { format } from 'date-fns';

import { getApiClient } from '@/lib/api';

/**
 * Data-access + browser helpers for Settings → Account. No demo fallback on
 * purpose: in demo mode the UI never calls these (it toasts instead), and
 * outside demo mode a failure must surface honestly.
 */

/** GET /v1/me/export as a Blob (throws ApiRequestError, 409 export_throttled with retryAfterSeconds). */
export function downloadExportApi(): Promise<Blob> {
  return getApiClient().downloadExport();
}

export function exportFilename(now: Date = new Date()): string {
  return `calendium-export-${format(now, 'yyyy-MM-dd')}.zip`;
}

/** Hands the blob to the browser's download manager via a transient object URL. */
export function saveBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

export function retryMessage(seconds: number): string {
  const minutes = Math.max(1, Math.ceil(seconds / 60));
  return `You exported your data recently. Try again in ${minutes} minute${minutes === 1 ? '' : 's'}.`;
}
```

- [ ] **Step 5: Create `apps/web/components/app/delete-account-dialog.tsx`:**

```tsx
'use client';

import * as React from 'react';
import { useRouter } from 'next/navigation';

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { authClient } from '@/lib/auth-client';
import { performSignOut } from '@/lib/sign-out';

export interface BlockingTeam {
  id: string;
  name: string;
}

export interface DeleteUserError {
  status?: number;
  code?: string;
  message?: string;
  details?: { teams?: BlockingTeam[] };
}

export function normalizeEmail(value: string): string {
  return value.trim().toLowerCase();
}

/** Maps Better Auth's deleteUser error (incl. the Go API's owns_teams envelope relayed by beforeDelete) to dialog copy. */
export function describeDeleteError(error: DeleteUserError): { message: string; teams: BlockingTeam[] } {
  const teams = error.details?.teams ?? [];
  if (error.code === 'owns_teams' || teams.length > 0) {
    return { message: 'Transfer ownership of these teams (or remove their other members) first:', teams };
  }
  if (error.code === 'INVALID_PASSWORD' || error.status === 400 || error.status === 401) {
    return { message: 'That password is incorrect.', teams: [] };
  }
  if (error.status === 403) {
    return { message: 'For your security, sign in again and then retry.', teams: [] };
  }
  return { message: 'Could not delete your account. Try again.', teams: [] };
}

/**
 * Danger-zone confirmation: explains what goes, requires the typed email and
 * (credential accounts only) the password, then calls Better Auth's
 * deleteUser. On success it leaves the app shell FIRST (/goodbye is outside
 * the session-gated layout, which would otherwise bounce to /signin) and
 * then runs the shared sign-out routine to scrub local state.
 */
export function DeleteAccountDialog({
  open,
  onOpenChange,
  email,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  email: string;
}) {
  const router = useRouter();
  const [typed, setTyped] = React.useState('');
  const [password, setPassword] = React.useState('');
  const [hasCredential, setHasCredential] = React.useState<boolean | null>(null);
  const [submitting, setSubmitting] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [teams, setTeams] = React.useState<BlockingTeam[]>([]);

  React.useEffect(() => {
    if (!open) return;
    let active = true;
    setTyped('');
    setPassword('');
    setError(null);
    setTeams([]);
    setHasCredential(null);
    authClient
      .listAccounts()
      .then(({ data }) => {
        if (active) setHasCredential(!!data?.some((a) => a.provider === 'credential'));
      })
      .catch(() => {
        if (active) setHasCredential(false);
      });
    return () => {
      active = false;
    };
  }, [open]);

  const emailMatches = normalizeEmail(typed) === normalizeEmail(email);
  const canConfirm =
    emailMatches && hasCredential !== null && (!hasCredential || password.length > 0) && !submitting;

  async function confirm() {
    setSubmitting(true);
    setError(null);
    setTeams([]);
    try {
      const { error: err } = await authClient.deleteUser(hasCredential ? { password } : {});
      if (err) {
        const described = describeDeleteError(err as DeleteUserError);
        setError(described.message);
        setTeams(described.teams);
        return;
      }
      router.replace('/goodbye');
      await performSignOut().catch(() => undefined);
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete your account</DialogTitle>
          <DialogDescription>
            This permanently deletes your Calendium account: connected mailboxes and their mirrored
            mail and calendars, drafts, snippets, templates, booking links, polls, tasks, notes,
            settings, and any team where you are the only member. Teams you share stay with their
            other members. Your mail stays with Google or Microsoft. There is no undo.
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="delete-account-email">Type your email ({email}) to confirm</Label>
            <Input
              id="delete-account-email"
              autoComplete="off"
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
            />
          </div>
          {hasCredential && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="delete-account-password">Your password</Label>
              <Input
                id="delete-account-password"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </div>
          )}
          {error && (
            <p role="alert" className="text-destructive text-sm">
              {error}
            </p>
          )}
          {teams.length > 0 && (
            <ul className="list-disc pl-5 text-sm">
              {teams.map((t) => (
                <li key={t.id}>{t.name}</li>
              ))}
            </ul>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
            Cancel
          </Button>
          <Button variant="destructive" onClick={confirm} disabled={!canConfirm}>
            Delete my account
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
```

- [ ] **Step 6: Run the tests.** `bun run test:web -- account-data delete-account-dialog` — expected: all pass.

- [ ] **Step 7: Commit.**

```bash
git add apps/web/lib/account-data.ts apps/web/lib/account-data.test.ts apps/web/components/app/delete-account-dialog.tsx apps/web/components/app/delete-account-dialog.test.tsx
git commit -m "feat(web): DeleteAccountDialog (typed email, password for credential accounts, owns_teams list) + export helpers" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 15: Web — Settings → Account tab, Background-AI toggle, `/goodbye` (Track C)

**Files:**
- Create: `apps/web/components/app/account-section.tsx`
- Create: `apps/web/components/app/background-ai-card.tsx`
- Create: `apps/web/components/app/account-section.test.tsx`
- Modify: `apps/web/app/(app)/settings/settings-page.tsx` (imports L44–52; `SettingsTab` + `KNOWN_TABS` L123–153; subtitle L217–221; `TabsList` L228–244 and `TabsContent`s L245–298; `AiSection` L1205–1224)
- Create: `apps/web/app/goodbye/page.tsx`
- Modify: `apps/web/lib/scheduling-mock.ts` (the seeded `settings` literal ≈L127–137)

**Interfaces:**
- Consumes: Task 14 (`DeleteAccountDialog`, `account-data`), `fetchSettings`/`updateSettingsApi` (`apps/web/lib/scheduling-data.ts`), `authClient.useSession()`, `DEMO_MODE`, `toast` (sonner), UI `Card*`, `Switch`, `Button`.
- Produces: `AccountSection()`; `BackgroundAiCard()`; settings tab `account` (first tab, deep-linkable via `/settings?tab=account`); `/goodbye` static page; demo settings carry `aiBackground: true`.

- [ ] **Step 1: Write the failing tests** at `apps/web/components/app/account-section.test.tsx`:

```tsx
import { ApiRequestError } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const downloadExportMock = vi.fn();
const getSettingsMock = vi.fn();
const updateSettingsMock = vi.fn();
vi.mock('@/lib/api', () => ({
  getApiClient: () => ({
    downloadExport: (...args: unknown[]) => downloadExportMock(...args),
    getSettings: (...args: unknown[]) => getSettingsMock(...args),
    updateSettings: (...args: unknown[]) => updateSettingsMock(...args),
  }),
}));

const saveBlobMock = vi.fn();
vi.mock('@/lib/account-data', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/account-data')>();
  return { ...actual, saveBlob: (...args: unknown[]) => saveBlobMock(...args) };
});

vi.mock('@/lib/auth-client', () => ({
  authClient: {
    useSession: () => ({ data: { user: { email: 'me@example.com', name: 'Me' } }, isPending: false }),
    listAccounts: vi.fn(async () => ({ data: [], error: null })),
    deleteUser: vi.fn(),
  },
}));

vi.mock('@/lib/demo', () => ({ DEMO_MODE: false }));

const toastSuccess = vi.fn();
const toastError = vi.fn();
const toastInfo = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
    info: (...args: unknown[]) => toastInfo(...args),
  },
}));

vi.mock('next/navigation', () => ({ useRouter: () => ({ replace: vi.fn(), push: vi.fn() }) }));
vi.mock('@/lib/sign-out', () => ({ performSignOut: vi.fn() }));

import { AccountSection } from './account-section';
import { BackgroundAiCard } from './background-ai-card';

function renderWithQuery(ui: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

const SETTINGS = { timeZone: 'UTC', workingHours: [], workingLocation: '', aiBackground: true };

beforeEach(() => {
  vi.clearAllMocks();
  getSettingsMock.mockResolvedValue(SETTINGS);
  updateSettingsMock.mockImplementation(async (s: typeof SETTINGS) => s);
});

describe('AccountSection', () => {
  it('shows the signed-in identity and both actions', async () => {
    renderWithQuery(<AccountSection />);
    expect(await screen.findByText(/me@example.com/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Download my data' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete account' })).toBeInTheDocument();
  });

  it('download calls downloadExport and hands the blob to the browser', async () => {
    const blob = new Blob(['PK'], { type: 'application/zip' });
    downloadExportMock.mockResolvedValue(blob);
    const user = userEvent.setup();
    renderWithQuery(<AccountSection />);
    await user.click(await screen.findByRole('button', { name: 'Download my data' }));
    await waitFor(() => expect(downloadExportMock).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(saveBlobMock).toHaveBeenCalledWith(blob, expect.stringMatching(/^calendium-export-\d{4}-\d{2}-\d{2}\.zip$/)));
    expect(toastSuccess).toHaveBeenCalled();
  });

  it('export_throttled shows the retry time', async () => {
    const err = new ApiRequestError(409, 'export_throttled', 'later');
    err.retryAfterSeconds = 1800;
    downloadExportMock.mockRejectedValue(err);
    const user = userEvent.setup();
    renderWithQuery(<AccountSection />);
    await user.click(await screen.findByRole('button', { name: 'Download my data' }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('You exported your data recently. Try again in 30 minutes.'));
    expect(saveBlobMock).not.toHaveBeenCalled();
  });

  it('opens the delete dialog', async () => {
    const user = userEvent.setup();
    renderWithQuery(<AccountSection />);
    await user.click(await screen.findByRole('button', { name: 'Delete account' }));
    expect(await screen.findByRole('dialog')).toHaveTextContent(/delete your account/i);
  });
});

describe('BackgroundAiCard', () => {
  it('reflects the stored switch and PUTs the whole document with aiBackground flipped', async () => {
    const user = userEvent.setup();
    renderWithQuery(<BackgroundAiCard />);
    const toggle = await screen.findByRole('switch', { name: 'Background AI processing' });
    await waitFor(() => expect(toggle).toBeChecked());
    await user.click(toggle);
    await waitFor(() =>
      expect(updateSettingsMock).toHaveBeenCalledWith({ ...SETTINGS, aiBackground: false })
    );
    await waitFor(() => expect(toggle).not.toBeChecked());
  });
});
```

- [ ] **Step 2: Run and confirm the failure.** `bun run test:web -- account-section` — expected: `Cannot find module './account-section'`.

- [ ] **Step 3: Create `apps/web/components/app/background-ai-card.tsx`:**

```tsx
'use client';

import type { UserSettings } from '@calendium/shared';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';

import { Card, CardAction, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Switch } from '@/components/ui/switch';
import { fetchSettings, updateSettingsApi } from '@/lib/scheduling-data';

/**
 * Settings → AI → "Background AI processing". Shares the ['scheduling-settings']
 * query with SchedulingSection: the switch is one field of the same document,
 * and PUT /v1/settings only writes it when the field is present.
 */
export function BackgroundAiCard() {
  const queryClient = useQueryClient();
  const settingsQuery = useQuery({ queryKey: ['scheduling-settings'], queryFn: fetchSettings });
  const toggle = useMutation({
    mutationFn: (aiBackground: boolean) =>
      updateSettingsApi({ ...(settingsQuery.data as UserSettings), aiBackground }),
    onSuccess: (s) => {
      queryClient.setQueryData<UserSettings>(['scheduling-settings'], s);
      toast.success(s.aiBackground ? 'Background AI processing is on' : 'Background AI processing is off');
    },
    onError: () => toast.error('Could not update background AI processing'),
  });
  const enabled = settingsQuery.data?.aiBackground ?? true;

  return (
    <Card>
      <CardHeader>
        <CardTitle>Background AI processing</CardTitle>
        <CardDescription>
          When new mail arrives, summarise it, draft a reply, suggest quick replies, run your
          classifiers and build your writing-style profile automatically. Turn this off and only the
          actions you trigger send a thread to the model.
        </CardDescription>
        <CardAction>
          <Switch
            aria-label="Background AI processing"
            checked={enabled}
            disabled={!settingsQuery.data || toggle.isPending}
            onCheckedChange={(checked) => toggle.mutate(checked)}
          />
        </CardAction>
      </CardHeader>
    </Card>
  );
}
```

- [ ] **Step 4: Create `apps/web/components/app/account-section.tsx`:**

```tsx
'use client';

import * as React from 'react';
import { ApiRequestError } from '@calendium/shared';
import { useMutation } from '@tanstack/react-query';
import { Download, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import { DeleteAccountDialog } from '@/components/app/delete-account-dialog';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { downloadExportApi, exportFilename, retryMessage, saveBlob } from '@/lib/account-data';
import { authClient } from '@/lib/auth-client';
import { DEMO_MODE } from '@/lib/demo';

const DEMO_TOAST = 'Not available in demo';

/**
 * Settings → Account: identity, the reserved Change-password slot (piece 2),
 * "Download my data" and the Danger zone. Demo mode renders every control
 * but never calls /v1/me/export or /api/auth/delete-user.
 */
export function AccountSection() {
  const { data: session } = authClient.useSession();
  const email = session?.user.email ?? '';
  const name = session?.user.name ?? '';
  const [deleteOpen, setDeleteOpen] = React.useState(false);

  const download = useMutation({
    mutationFn: async () => {
      const blob = await downloadExportApi();
      saveBlob(blob, exportFilename());
    },
    onSuccess: () => toast.success('Your export is downloading'),
    onError: (err) => {
      if (err instanceof ApiRequestError && err.code === 'export_throttled') {
        toast.error(retryMessage(err.retryAfterSeconds ?? 3600));
        return;
      }
      toast.error('Could not prepare your export. Try again.');
    },
  });

  function onDownload() {
    if (DEMO_MODE) {
      toast.info(DEMO_TOAST);
      return;
    }
    download.mutate();
  }

  function onDelete() {
    if (DEMO_MODE) {
      toast.info(DEMO_TOAST);
      return;
    }
    setDeleteOpen(true);
  }

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <CardHeader>
          <CardTitle>Account</CardTitle>
          <CardDescription>{name ? `${name} · ${email}` : email}</CardDescription>
        </CardHeader>
        {/* Piece 2 (email + auth hardening) mounts its Change-password control here. */}
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Your data</CardTitle>
          <CardDescription>
            Download a zip with your profile, mirrored mail and calendars, drafts, snippets,
            templates, tasks, booking links, polls and settings. One export per hour.
          </CardDescription>
          <CardAction>
            <Button size="sm" variant="outline" onClick={onDownload} disabled={download.isPending}>
              <Download />
              Download my data
            </Button>
          </CardAction>
        </CardHeader>
      </Card>
      <Card className="border-destructive/40">
        <CardHeader>
          <CardTitle>Danger zone</CardTitle>
          <CardDescription>
            Deleting your account removes your data from Calendium Cloud. Download a copy first if
            you want to keep it.
          </CardDescription>
          <CardAction>
            <Button size="sm" variant="destructive" onClick={onDelete}>
              <Trash2 />
              Delete account
            </Button>
          </CardAction>
        </CardHeader>
      </Card>
      <DeleteAccountDialog open={deleteOpen} onOpenChange={setDeleteOpen} email={email} />
    </div>
  );
}
```

- [ ] **Step 5: Wire the tab into `apps/web/app/(app)/settings/settings-page.tsx`.** Add imports after `import { BookingLinks, ... } from '@/components/app/booking-links';`:

```tsx
import { AccountSection } from '@/components/app/account-section';
import { BackgroundAiCard } from '@/components/app/background-ai-card';
```

Add `| 'account'` as the first member of `SettingsTab` and `'account',` as the first entry of `KNOWN_TABS`. Change the subtitle paragraph's first line from `Accounts, snippets, templates, sets, scheduling, delegation, appearance, mailbox` to `Account, connected accounts, snippets, templates, sets, scheduling, delegation, appearance, mailbox`. In `TabsList`, insert `<TabsTrigger value="account">Account</TabsTrigger>` before `<TabsTrigger value="accounts">Accounts</TabsTrigger>`. Before `<TabsContent value="accounts" className="mt-4">` insert:

```tsx
          <TabsContent value="account" className="mt-4">
            <AccountSection />
          </TabsContent>
```

Replace `AiSection` with:

```tsx
function AiSection() {
  return (
    <div className="flex flex-col gap-4">
      <BackgroundAiCard />
      <Card>
        <CardHeader>
          <CardTitle>AI classifiers</CardTitle>
          <CardDescription>
            Natural-language rules that route matching mail to a split and/or tag it with a label.
          </CardDescription>
          <CardAction>
            <Button size="sm" asChild>
              <Link href="/settings/classifiers">
                <Sparkles />
                Manage classifiers
              </Link>
            </Button>
          </CardAction>
        </CardHeader>
      </Card>
    </div>
  );
}
```

- [ ] **Step 6: Give the demo settings the new field.** In `apps/web/lib/scheduling-mock.ts`, the seeded `const settings: UserSettings = { ... workingLocation: '', }` literal gains `aiBackground: true,` after `workingLocation: '',`. `grep -rn "workingLocation:" apps/web --include=*.ts --include=*.tsx` must show no other `UserSettings` literal without `aiBackground` (test files included: add the field wherever one exists).

- [ ] **Step 7: Create `apps/web/app/goodbye/page.tsx`:**

```tsx
import type { Metadata } from 'next';
import Link from 'next/link';
import { CheckCircle2 } from 'lucide-react';

export const metadata: Metadata = {
  title: 'Account deleted',
};

// Public and static: it renders after the session is gone, outside the
// session-gated (app) layout.
export const dynamic = 'force-static';

export default function GoodbyePage() {
  return (
    <div className="bg-background flex min-h-svh flex-col items-center justify-center gap-4 px-6 text-center">
      <CheckCircle2 className="text-muted-foreground size-10" />
      <h1 className="text-xl font-semibold tracking-tight">Your account has been deleted</h1>
      <p className="text-muted-foreground max-w-md text-sm leading-relaxed">
        Your Calendium data is gone from our servers. Your mail and calendars stay with Google or
        Microsoft; you can revoke Calendium&apos;s access from their account settings. Thanks for
        trying Calendium.
      </p>
      <Link href="/" className="text-sm underline underline-offset-4">
        Back to the home page
      </Link>
    </div>
  );
}
```

- [ ] **Step 8: Run the web suite.** `bun run test:web` — expected: all pass (the new `account-section` tests plus every existing settings test).

- [ ] **Step 9: Commit.**

```bash
git add apps/web/components/app/account-section.tsx apps/web/components/app/background-ai-card.tsx apps/web/components/app/account-section.test.tsx "apps/web/app/(app)/settings/settings-page.tsx" apps/web/app/goodbye/page.tsx apps/web/lib/scheduling-mock.ts
git commit -m "feat(web): Settings → Account tab (download, danger zone), Background AI toggle, /goodbye" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 16: Web — privacy and terms copy (Track C)

**Files:**
- Modify: `apps/web/app/(marketing)/privacy/page.tsx` (`UPDATED` L13; "Information we collect" L36–59; "AI features" L71–79; "Third parties we rely on" L81–97; "Retention and deletion" L99–107)
- Modify: `apps/web/app/(marketing)/terms/page.tsx` (`UPDATED` L13; "Subscriptions and billing" L50–60; "Termination" L96–105)

**Interfaces:** none (copy only). The `LegalLayout`/`LegalSection`/`LegalList` components are unchanged.

- [ ] **Step 1: Write the failing copy test.** Create `apps/web/app/(marketing)/legal-copy.test.tsx` (Track C owns it; add it to the file list above):

```tsx
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import PrivacyPage from './privacy/page';
import TermsPage from './terms/page';

describe('privacy page', () => {
  it('names the background-AI toggle, every third party and the account controls', () => {
    render(<PrivacyPage />);
    expect(screen.getByText(/Last updated October 2026/)).toBeInTheDocument();
    expect(screen.getByText(/Background AI processing/)).toBeInTheDocument();
    for (const vendor of ['OpenRouter', 'Paddle', 'Todoist', 'HubSpot', 'Open-Meteo', 'Nominatim', 'OSRM', 'APNs', 'FCM']) {
      expect(screen.getAllByText(new RegExp(vendor)).length).toBeGreaterThan(0);
    }
    expect(screen.getByText(/Delete account/)).toBeInTheDocument();
    expect(screen.getByText(/Download my data/)).toBeInTheDocument();
    expect(screen.queryByText(/Stripe/)).toBeNull();
    expect(screen.queryByText(/nothing runs in the background/)).toBeNull();
  });
});

describe('terms page', () => {
  it('names Paddle as merchant of record with its refund policy and the export-before-delete sentence', () => {
    render(<TermsPage />);
    expect(screen.getByText(/Last updated October 2026/)).toBeInTheDocument();
    expect(screen.getAllByText(/Paddle/).length).toBeGreaterThan(0);
    expect(screen.getByText(/Refund requests are handled by Paddle under its refund policy/)).toBeInTheDocument();
    expect(screen.getByText(/download a copy of your data from Settings/)).toBeInTheDocument();
    expect(screen.queryByText(/Stripe/)).toBeNull();
    expect(screen.queryByText(/non-refundable/)).toBeNull();
  });
});
```

- [ ] **Step 2: Run and confirm the failure.** `bun run test:web -- legal-copy` — expected: assertions on `October 2026`, `Background AI processing`, `Paddle` fail. (If piece 1 already replaced Stripe with Paddle in these pages, only the piece-3 assertions fail — keep piece 1's wording where it overlaps.)

- [ ] **Step 3: Rewrite the privacy sections.** In `apps/web/app/(marketing)/privacy/page.tsx` set `const UPDATED = 'October 2026';` and replace the four sections:

```tsx
      <LegalSection title="Information we collect">
        <LegalList
          items={[
            <>
              <strong>Account information.</strong> Your name, email address, and authentication
              credentials, managed by Better Auth. Passwords are stored only as salted hashes.
            </>,
            <>
              <strong>Connected mailboxes.</strong> When you connect a Google or Microsoft account,
              we store OAuth refresh tokens (encrypted at rest with AES-256-GCM) and mirror your
              mail and calendar so the app can serve them quickly.
            </>,
            <>
              <strong>Integrations.</strong> If you connect Todoist or HubSpot, we store their
              OAuth tokens the same way and mirror the tasks or contact context you use in the app.
            </>,
            <>
              <strong>Billing information.</strong> Subscriptions are sold by Paddle, our merchant
              of record. We store your subscription status and Paddle customer and subscription
              ids; your payment details are held by Paddle, not us.
            </>,
            <>
              <strong>Device tokens.</strong> If you enable notifications, we store the push token
              for your device so we can deliver alerts.
            </>,
          ]}
        />
      </LegalSection>
```

```tsx
      <LegalSection title="AI features">
        <p>
          When new mail arrives we may summarise it, draft a reply, suggest quick replies, run your
          classifiers and build your writing-style profile in the background. Turn this off in
          Settings → AI → <strong>Background AI processing</strong>; on-demand actions (compose,
          reply, summarise, ask) still send only the thread you point them at. Requests go to
          OpenRouter, which routes them to model providers that vary by model. Your mail is never
          used to train models.
        </p>
      </LegalSection>
```

```tsx
      <LegalSection title="Third parties we rely on">
        <LegalList
          items={[
            <>
              <strong>Google &amp; Microsoft</strong> — to access the mailboxes and calendars you
              connect, under the scopes you approve.
            </>,
            <>
              <strong>Paddle</strong> — merchant of record for Cloud subscriptions: payment,
              invoices, receipts, taxes and refunds.
            </>,
            <>
              <strong>OpenRouter</strong> — the AI gateway for the AI features above; it routes
              requests to model providers that vary by model.
            </>,
            <>
              <strong>Todoist and HubSpot</strong> — only if you connect them, for the tasks and
              CRM context you choose to sync.
            </>,
            <>
              <strong>Open-Meteo</strong> — weather on calendar days; receives coordinates only.
            </>,
            <>
              <strong>Nominatim and OSRM</strong> — place search and routing for event locations;
              receive your query text and coordinates.
            </>,
            <>
              <strong>Apple APNs and Google FCM</strong> — deliver push notifications; receive
              your device token and the notification preview.
            </>,
          ]}
        />
      </LegalSection>
```

```tsx
      <LegalSection title="Retention and deletion">
        <p>
          We keep your data for as long as your account is active. You can disconnect a mailbox at
          any time, which removes its stored tokens and mirrored content. You can download a copy of
          everything from Settings → Account → <strong>Download my data</strong>, and you can delete
          your account from Settings → Account → <strong>Delete account</strong> — on the web, in
          the mobile app or on desktop. Deletion removes your personal data from Cloud immediately,
          subject to any records we must retain for legal or accounting reasons (Paddle keeps its
          own transaction records as merchant of record).
        </p>
      </LegalSection>
```

- [ ] **Step 4: Rewrite the terms sections.** In `apps/web/app/(marketing)/terms/page.tsx` set `const UPDATED = 'October 2026';` and replace:

```tsx
      <LegalSection title="Subscriptions and billing">
        <p>
          Calendium Cloud costs <strong>$50 per year</strong> after a 14-day free trial. Purchases
          are made through Paddle, our merchant of record, which handles payment, invoices, receipts
          and applicable taxes; when your trial ends you subscribe on the web and the plan renews
          annually until you cancel. You can cancel anytime from Settings → Billing, and your access
          continues through the end of the period you paid for. There are no in-app purchases — the
          mobile and desktop apps unlock automatically once you subscribe on the web. Refund requests
          are handled by Paddle under its refund policy.
        </p>
      </LegalSection>
```

```tsx
      <LegalSection title="Termination">
        <p>
          You may stop using Calendium and delete your account at any time. You can download a copy
          of your data from Settings → Account at any time before deleting your account. We may
          suspend or terminate an account that violates these terms. On termination, your right to
          use the Cloud service ends; the deletion of your data is described in our{' '}
          <Link href="/privacy">Privacy Policy</Link>.
        </p>
      </LegalSection>
```

- [ ] **Step 5: Run the test and Biome.** `bun run test:web -- legal-copy && bunx biome check apps/web/app/\(marketing\)` — expected: pass, no lint findings.

- [ ] **Step 6: Commit.**

```bash
git add "apps/web/app/(marketing)/privacy/page.tsx" "apps/web/app/(marketing)/terms/page.tsx" "apps/web/app/(marketing)/legal-copy.test.tsx"
git commit -m "docs(web): privacy names background AI + every third party + account controls; terms name Paddle refund policy and export-before-delete" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 17: Web e2e — Account tab in demo mode (Track C)

**Files:**
- Modify: `apps/web/e2e/settings.spec.ts` (append one test)

**Interfaces:**
- Consumes: Task 15 UI; e2e fixtures (`apps/web/e2e/fixtures.ts`).

- [ ] **Step 1: Append the test to `apps/web/e2e/settings.spec.ts`:**

```ts
  test('account tab renders both actions and demo mode never calls the real routes', async ({ page }) => {
    const forbidden: string[] = [];
    page.on('request', (req) => {
      if (/\/api\/auth\/delete-user|\/v1\/me\/export/.test(req.url())) forbidden.push(req.url());
    });
    await page.goto('/settings?tab=account');
    await expect(page.getByRole('tab', { name: 'Account', exact: true })).toHaveAttribute('aria-selected', 'true');
    await expect(page.getByText('e2e@calendium.app')).toBeVisible();

    await page.getByRole('button', { name: 'Download my data' }).click();
    await expect(page.getByText('Not available in demo').first()).toBeVisible();

    await page.getByRole('button', { name: 'Delete account' }).click();
    await expect(page.getByText('Not available in demo').first()).toBeVisible();
    await expect(page.getByRole('dialog')).toHaveCount(0);

    await page.getByRole('tab', { name: 'AI', exact: true }).click();
    await expect(page.getByRole('switch', { name: 'Background AI processing' })).toBeVisible();

    expect(forbidden).toEqual([]);
  });
```

- [ ] **Step 2: Run it.** `bun run test:e2e -- settings` — expected: the whole `settings.spec.ts` passes (the demo instance advertises `ai: true`, so the AI tab exists).

- [ ] **Step 3: Commit.**

```bash
git add apps/web/e2e/settings.spec.ts
git commit -m "test(e2e): account tab renders in demo mode without touching delete-user or export" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 18: Mobile — Settings → Account (delete, download link-out) + Background AI toggle (Track D; after Task 12)

**Files:**
- Modify: `apps/mobile/lib/server-config.ts` (append `webOrigin` after `normalizeServerUrl` ≈L63–68)
- Modify: `apps/mobile/lib/mock.ts` (append `mockSettings`/`updateMockSettings`; add `UserSettings` to the `@calendium/shared` type import)
- Modify: `apps/mobile/app/(tabs)/settings.tsx` (imports L1–44; hook destructuring L58–62; `aiEnabled`/`demoMode` L76–77; add state + queries after `saveMailPrefsMutation`; AI section L412–423; new Account section before the Sign-out `<Button>` L467)
- Modify: `apps/mobile/app/(tabs)/settings.test.tsx` (mocks L1–57; `beforeEach` L100–108; append tests)

**Interfaces:**
- Consumes: `UserSettings.aiBackground` (Task 12), `api.getSettings/updateSettings`, `useServerConfig().authClient` (`AuthClient.listAccounts/deleteUser`), `useAuth().{signOut, signInWithOAuth}`, `withMockFallback`, `Linking.openURL`, RN `Switch`.
- Produces: `webOrigin(config: ServerConfig | null): string | null` (mobile); `mockSettings(): UserSettings`, `updateMockSettings(next: UserSettings): UserSettings`; Settings → Account rows "Download my data" / "Delete account"; AI → "Background AI processing" switch.

- [ ] **Step 1: Extend the test mocks and write the failing tests.** In `apps/mobile/app/(tabs)/settings.test.tsx`:

Replace the `@/lib/server-config` mock with:

```ts
const mockUseServerConfig = jest.fn();
jest.mock('@/lib/server-config', () => ({
  useServerConfig: (...args: unknown[]) => mockUseServerConfig(...args),
  webOrigin: () => 'https://app.example.com',
}));
```

Add `getSettings`/`updateSettings` to the `@/lib/api` mock:

```ts
const mockGetSettings = jest.fn();
const mockUpdateSettings = jest.fn();
```

and inside `api: { ... }`: `getSettings: (...args: unknown[]) => mockGetSettings(...args), updateSettings: (...args: unknown[]) => mockUpdateSettings(...args),`.

Replace the `expo-linking` mock with:

```ts
const mockOpenURL = jest.fn();
jest.mock('expo-linking', () => ({
  createURL: jest.fn(() => 'calendium://settings'),
  openURL: (...args: unknown[]) => mockOpenURL(...args),
}));
```

Add after the other mock declarations:

```ts
const mockListAuthAccounts = jest.fn();
const mockDeleteUser = jest.fn();
const mockAuthClient = {
  listAccounts: (...args: unknown[]) => mockListAuthAccounts(...args),
  deleteUser: (...args: unknown[]) => mockDeleteUser(...args),
};
const mockSignOut = jest.fn();
const mockSignInWithOAuth = jest.fn();
```

Add `import { Alert } from 'react-native';` next to the other imports. Change `beforeEach` to:

```ts
beforeEach(() => {
  jest.clearAllMocks();
  jest.spyOn(Alert, 'alert').mockImplementation(() => undefined);
  mockUseAuth.mockReturnValue({
    user: { id: 'u1', name: 'You', email: 'you@example.com', image: null },
    signOut: mockSignOut,
    signInWithOAuth: mockSignInWithOAuth,
  });
  mockUseServerConfig.mockReturnValue({ config: AI_ENABLED_CONFIG, clear: jest.fn(), authClient: mockAuthClient });
  mockListAccounts.mockResolvedValue([]);
  mockGetSubscription.mockResolvedValue({ status: 'none', priceUsd: 50 });
  mockGetSettings.mockResolvedValue({ timeZone: 'UTC', workingHours: [], workingLocation: '', aiBackground: true });
  mockUpdateSettings.mockImplementation(async (s: unknown) => s);
  mockListAuthAccounts.mockResolvedValue({ data: [{ id: 'acc1', provider: 'credential' }], error: null });
  mockDeleteUser.mockResolvedValue({ data: { success: true }, error: null });
  mockSignOut.mockResolvedValue(undefined);
});
```

Append the tests:

```tsx
/** Presses the destructive button of the most recent Alert.alert call. */
async function pressAlertConfirm() {
  const calls = (Alert.alert as jest.Mock).mock.calls;
  const buttons = calls[calls.length - 1]?.[2] as { style?: string; onPress?: () => void }[] | undefined;
  const confirm = buttons?.find((b) => b.style === 'destructive');
  if (!confirm?.onPress) throw new Error('no destructive alert button');
  await act(async () => {
    await confirm.onPress?.();
  });
}

describe('SettingsScreen — account lifecycle', () => {
  it('"Download my data" opens the web account page in the browser', async () => {
    await renderScreen();
    await flush();
    await fireEvent.press(screen.getByText('Download my data'));
    expect(mockOpenURL).toHaveBeenCalledWith('https://app.example.com/settings?tab=account');
  });

  it('delete: password prompt → deleteUser({password}) → sign-out (clears offline state) → sign-in screen', async () => {
    await renderScreen();
    await flush();
    await fireEvent.press(screen.getByText('Delete account'));
    await flush();
    fireEvent.changeText(screen.getByPlaceholderText('Your password'), 'hunter2');
    await fireEvent.press(screen.getByText('Delete my account'));
    await pressAlertConfirm();
    await flush();
    expect(mockDeleteUser).toHaveBeenCalledWith({ password: 'hunter2' });
    expect(mockSignOut).toHaveBeenCalled();
    expect(mockReplace).toHaveBeenCalledWith('/');
  });

  it('social-only accounts get no password field and call deleteUser({})', async () => {
    mockListAuthAccounts.mockResolvedValue({ data: [{ id: 'acc2', provider: 'google' }], error: null });
    await renderScreen();
    await flush();
    await fireEvent.press(screen.getByText('Delete account'));
    await flush();
    expect(screen.queryByPlaceholderText('Your password')).toBeNull();
    await fireEvent.press(screen.getByText('Delete my account'));
    await pressAlertConfirm();
    await flush();
    expect(mockDeleteUser).toHaveBeenCalledWith({});
  });

  it('owns_teams lists the blocking teams and keeps the session', async () => {
    mockDeleteUser.mockResolvedValue({
      data: null,
      error: { status: 409, code: 'owns_teams', details: { teams: [{ id: 't1', name: 'Design' }] } },
    });
    await renderScreen();
    await flush();
    await fireEvent.press(screen.getByText('Delete account'));
    await flush();
    fireEvent.changeText(screen.getByPlaceholderText('Your password'), 'hunter2');
    await fireEvent.press(screen.getByText('Delete my account'));
    await pressAlertConfirm();
    await flush();
    expect(Alert.alert).toHaveBeenLastCalledWith('Transfer your teams first', expect.stringContaining('Design'));
    expect(mockSignOut).not.toHaveBeenCalled();
  });

  it('demo mode renders the rows but never calls deleteUser or opens a URL', async () => {
    mockUseServerConfig.mockReturnValue({
      config: { ...AI_ENABLED_CONFIG, demoMode: true },
      clear: jest.fn(),
      authClient: mockAuthClient,
    });
    await renderScreen();
    await flush();
    await fireEvent.press(screen.getByText('Delete account'));
    await fireEvent.press(screen.getByText('Download my data'));
    expect(Alert.alert).toHaveBeenCalledWith('Not available in demo');
    expect(mockDeleteUser).not.toHaveBeenCalled();
    expect(mockOpenURL).not.toHaveBeenCalled();
  });
});

describe('SettingsScreen — background AI switch', () => {
  it('reflects the stored value and PUTs the whole document with aiBackground flipped', async () => {
    await renderScreen();
    await flush();
    const toggle = screen.getByLabelText('Background AI processing');
    expect(toggle.props.value).toBe(true);
    await act(async () => {
      fireEvent(toggle, 'valueChange', false);
    });
    await flush();
    expect(mockUpdateSettings).toHaveBeenCalledWith({
      timeZone: 'UTC',
      workingHours: [],
      workingLocation: '',
      aiBackground: false,
    });
  });

  it('is hidden when the server disables AI', async () => {
    mockUseServerConfig.mockReturnValue({ config: AI_DISABLED_CONFIG, clear: jest.fn(), authClient: mockAuthClient });
    await renderScreen();
    await flush();
    expect(screen.queryByLabelText('Background AI processing')).toBeNull();
  });
});
```

- [ ] **Step 2: Run and confirm the failure.** `bun run test:mobile -- settings.test` — expected: `Unable to find an element with text: Download my data`, `webOrigin`/`mockSettings` missing.

- [ ] **Step 3: Add `webOrigin` to `apps/mobile/lib/server-config.ts`** (after `normalizeServerUrl`):

```ts
/** Web origin (scheme://host) of a server's Better Auth base URL, or null. */
export function webOrigin(config: ServerConfig | null): string | null {
  if (!config?.authBaseUrl) return null;
  try {
    return new URL(config.authBaseUrl).origin;
  } catch {
    return null;
  }
}
```

- [ ] **Step 4: Add the settings mock to `apps/mobile/lib/mock.ts`** (append; add `UserSettings` to the `@calendium/shared` type import at the top):

```ts
// ---------------------------------------------------------------------------
// Settings (background-AI switch; demo mode keeps the toggle working offline)
// ---------------------------------------------------------------------------

let mockSettingsState: UserSettings = {
  timeZone: 'UTC',
  workingHours: [],
  workingLocation: '',
  aiBackground: true,
};

export function mockSettings(): UserSettings {
  return { ...mockSettingsState };
}

export function updateMockSettings(next: UserSettings): UserSettings {
  mockSettingsState = { ...next };
  return mockSettings();
}
```

- [ ] **Step 5: Update `apps/mobile/app/(tabs)/settings.tsx`.** Imports: change `import { useServerConfig } from '@/lib/server-config';` to `import { useServerConfig, webOrigin } from '@/lib/server-config';`; add `mockSettings, updateMockSettings,` to the `@/lib/mock` import list; add `type UserSettings,` to the `@calendium/shared` import; add `DownloadIcon, Trash2Icon,` to the `lucide-react-native` import; add `Switch` to the `react-native` import (`import { ActivityIndicator, Alert, Image, Pressable, ScrollView, Switch, View } from 'react-native';`).

Change the two hook lines to:

```tsx
  const { user, signOut, signInWithOAuth } = useAuth();
  const { config, clear: clearServer, authClient } = useServerConfig();
```

After `saveMailPrefsMutation` (its closing `});`) add:

```tsx
  // --- Settings → AI → "Background AI processing" (piece 3) ---
  const settingsQuery = useQuery({
    queryKey: ['settings'],
    queryFn: () =>
      withMockFallback(
        () => api.getSettings(),
        () => mockSettings()
      ),
    enabled: aiEnabled,
  });
  const toggleBackgroundAi = useMutation({
    mutationFn: (aiBackground: boolean) => {
      const next: UserSettings = { ...(settingsQuery.data as UserSettings), aiBackground };
      return withMockFallback(
        () => api.updateSettings(next),
        () => updateMockSettings(next)
      );
    },
    onSuccess: (s) => queryClient.setQueryData(['settings'], s),
    onError: () => Alert.alert('Could not update', 'Background AI processing was not changed.'),
  });

  // --- Settings → Account: export link-out + in-app deletion (App Store 5.1.1(v)) ---
  const [deleting, setDeleting] = React.useState(false);
  const [deletePassword, setDeletePassword] = React.useState('');
  const [hasCredential, setHasCredential] = React.useState<boolean | null>(null);
  const [socialProvider, setSocialProvider] = React.useState<'google' | 'apple' | null>(null);
  const [deleteBusy, setDeleteBusy] = React.useState(false);

  const openDataExport = () => {
    if (demoMode) {
      Alert.alert('Not available in demo');
      return;
    }
    const origin = webOrigin(config);
    if (!origin) {
      Alert.alert('No web app', 'This server has no web address to open.');
      return;
    }
    void Linking.openURL(`${origin}/settings?tab=account`);
  };

  const startDelete = async () => {
    if (demoMode) {
      Alert.alert('Not available in demo');
      return;
    }
    if (!authClient) return;
    setDeleting(true);
    try {
      const { data } = await authClient.listAccounts();
      const accounts = data ?? [];
      setHasCredential(accounts.some((a) => a.provider === 'credential'));
      const social = accounts.find((a) => a.provider === 'google' || a.provider === 'apple');
      setSocialProvider((social?.provider as 'google' | 'apple' | undefined) ?? null);
    } catch {
      setHasCredential(false);
    }
  };

  const runDelete = async () => {
    if (!authClient) return;
    setDeleteBusy(true);
    try {
      const { error } = await authClient.deleteUser(hasCredential ? { password: deletePassword } : {});
      if (error) {
        const err = error as {
          status?: number;
          code?: string;
          details?: { teams?: { id: string; name: string }[] };
        };
        const teams = err.details?.teams ?? [];
        if (err.code === 'owns_teams' || teams.length > 0) {
          Alert.alert('Transfer your teams first', teams.map((t) => `• ${t.name}`).join('\n'));
        } else if (err.code === 'INVALID_PASSWORD' || err.status === 400 || err.status === 401) {
          Alert.alert('Incorrect password', 'Check your password and try again.');
        } else if (err.status === 403) {
          Alert.alert('Sign in again', 'For your security, sign in again and then retry.', [
            { text: 'Cancel', style: 'cancel' },
            {
              text: 'Sign in',
              onPress: () => {
                if (socialProvider) void signInWithOAuth(socialProvider).catch(() => undefined);
              },
            },
          ]);
        } else {
          Alert.alert('Could not delete your account', 'Try again.');
        }
        return;
      }
      // The session is gone server-side; signOut still clears this device's
      // push token, query cache and offline outbox (context/auth.tsx).
      await signOut().catch(() => undefined);
      router.replace('/');
    } finally {
      setDeleteBusy(false);
    }
  };

  const confirmDelete = () => {
    Alert.alert(
      'Delete your account?',
      'This permanently deletes your Calendium account and its data. There is no undo.',
      [
        { text: 'Cancel', style: 'cancel' },
        { text: 'Delete', style: 'destructive', onPress: () => void runDelete() },
      ]
    );
  };
```

Replace the AI section with:

```tsx
      {/* AI — background switch + classifier CRUD on its own screen (Task 17). */}
      {aiEnabled && (
        <Section title="AI">
          <View className="flex-row items-center justify-between p-4">
            <View className="flex-1 gap-0.5 pr-3">
              <Text className="text-sm font-medium">Background AI processing</Text>
              <Text className="text-xs text-muted-foreground">
                Summaries, quick replies, auto drafts, classifiers and your writing profile run
                automatically when mail arrives.
              </Text>
            </View>
            <Switch
              accessibilityLabel="Background AI processing"
              value={settingsQuery.data?.aiBackground ?? true}
              disabled={!settingsQuery.data || toggleBackgroundAi.isPending}
              onValueChange={(value) => toggleBackgroundAi.mutate(value)}
            />
          </View>
          <Pressable
            onPress={() => router.push('/classifiers')}
            className="flex-row items-center justify-between border-t border-border p-4 active:bg-accent">
            <View className="flex-row items-center gap-3">
              <Icon as={SparklesIcon} className="size-5 text-muted-foreground" />
              <Text className="text-sm font-medium">AI classifiers</Text>
            </View>
            <Icon as={ChevronRightIcon} className="size-4 text-muted-foreground" />
          </Pressable>
        </Section>
      )}
```

Insert the Account section between the Appearance `</Section>` and the Sign-out `<Button>`:

```tsx
      {/* Account (piece 3): export on the web, deletion in-app. */}
      <Section title="Account">
        <Pressable
          onPress={openDataExport}
          className="flex-row items-center justify-between p-4 active:bg-accent">
          <View className="flex-row items-center gap-3">
            <Icon as={DownloadIcon} className="size-5 text-muted-foreground" />
            <Text className="text-sm font-medium">Download my data</Text>
          </View>
          <Icon as={ExternalLinkIcon} className="size-4 text-muted-foreground" />
        </Pressable>
        {!deleting ? (
          <Pressable
            onPress={() => void startDelete()}
            className="flex-row items-center gap-3 border-t border-border p-4 active:bg-accent">
            <Icon as={Trash2Icon} className="size-5 text-destructive" />
            <Text className="text-sm font-medium text-destructive">Delete account</Text>
          </Pressable>
        ) : (
          <View className="gap-3 border-t border-border p-4">
            <Text className="text-sm text-muted-foreground">
              This permanently deletes your account: mirrored mail and calendars, drafts, snippets,
              tasks, settings and any team where you are the only member. There is no undo.
            </Text>
            {hasCredential && (
              <Input
                placeholder="Your password"
                secureTextEntry
                autoCapitalize="none"
                value={deletePassword}
                onChangeText={setDeletePassword}
              />
            )}
            <View className="flex-row gap-2">
              <Button
                variant="outline"
                className="flex-1"
                onPress={() => setDeleting(false)}
                disabled={deleteBusy}>
                <Text>Cancel</Text>
              </Button>
              <Button
                variant="destructive"
                className="flex-1"
                onPress={confirmDelete}
                disabled={
                  deleteBusy ||
                  hasCredential === null ||
                  (hasCredential && deletePassword.length === 0)
                }>
                {deleteBusy ? <ActivityIndicator /> : <Text>Delete my account</Text>}
              </Button>
            </View>
          </View>
        )}
      </Section>
```

- [ ] **Step 6: Run the mobile suite.** `bun run test:mobile` — expected: all pass (the new account/background-AI tests plus every existing one; the `AI classifiers` row visibility tests still hold).

- [ ] **Step 7: Commit.**

```bash
git add "apps/mobile/app/(tabs)/settings.tsx" "apps/mobile/app/(tabs)/settings.test.tsx" apps/mobile/lib/server-config.ts apps/mobile/lib/mock.ts
git commit -m "feat(mobile): Settings → Account (in-app delete with password/social re-auth, export link-out) + background AI switch" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 19: Desktop — Account controls + Background AI toggle (Track D; after Task 12)

**Files:**
- Modify: `apps/desktop/frontend/src/lib/mock.ts` (append `mockSettings`/`updateMockSettings`; add `UserSettings` to the shared type import)
- Modify: `apps/desktop/frontend/src/views/SettingsView.tsx` (imports L1–69; new `BackgroundAiSection` before `Section`; `SettingsView` state/handlers after `handleSwitchServer` ≈L608–617; Account section JSX L627–640; render `<BackgroundAiSection />` before `{config?.features?.ai && <ClassifiersSection />}` L704)
- Modify: `apps/desktop/frontend/src/views/SettingsView.test.tsx` (mocks L1–112; append tests)

**Interfaces:**
- Consumes: `UserSettings.aiBackground` (Task 12), `api.getSettings/updateSettings`, `orMock`, `getAuthClient()`, `clearStoredToken()`, `signOut()`, `clearOfflineState()`, `webOrigin(config)`, `desktop.OpenExternal`, `toast`/`errorMessage`.
- Produces: `mockSettings()`, `updateMockSettings(next)`; `BackgroundAiSection()`; Account section buttons "Download my data" / "Delete account" with an inline confirm (typed email + password).

- [ ] **Step 1: Extend the test mocks and write the failing tests.** In `apps/desktop/frontend/src/views/SettingsView.test.tsx`:

Replace the hoisted fixtures with:

```ts
const fixtures = vi.hoisted(() => ({
  accounts: [] as ConnectedAccount[],
  ai: false,
  origin: 'https://app.example.com' as string | null,
  authClient: null as null | {
    listAccounts: () => Promise<{ data: { id: string; provider: string }[] }>;
    deleteUser: (input: { password?: string }) => Promise<{ error: null | { status?: number; code?: string } }>;
  },
}));
const openExternalMock = vi.hoisted(() => vi.fn());
const clearStoredTokenMock = vi.hoisted(() => vi.fn());
const signOutMock = vi.hoisted(() => vi.fn(async () => undefined));
const getSettingsMock = vi.hoisted(() => vi.fn());
const updateSettingsMock = vi.hoisted(() => vi.fn());
```

Add `getSettings: (...args: unknown[]) => getSettingsMock(...args), updateSettings: (...args: unknown[]) => updateSettingsMock(...args),` to the `api` object in the `@/lib/api` mock (keep its `orMock` routing every call to the mock branch, as today) and add to the `@/lib/mock` mock:

```ts
  mockSettings: () => ({ timeZone: 'UTC', workingHours: [], workingLocation: '', aiBackground: true }),
  updateMockSettings: (s: unknown) => {
    updateSettingsMock(s);
    return s;
  },
```

Replace the `@/lib/server-config` mock with:

```ts
vi.mock('@/lib/server-config', () => ({
  useServerConfig: () => ({
    config: { features: { billing: false, google: true, microsoft: false, ai: fixtures.ai, push: false } },
    clear: vi.fn(),
    demoMode: false,
    exitDemo: vi.fn(),
  }),
  webOrigin: () => fixtures.origin,
}));
```

Replace the `@/lib/auth` mock with:

```ts
vi.mock('@/lib/auth', () => ({
  clearStoredToken: (...args: unknown[]) => clearStoredTokenMock(...args),
  signOut: (...args: unknown[]) => signOutMock(...args),
  getAuthClient: () => fixtures.authClient,
}));

vi.mock('@/lib/offline', () => ({ clearOfflineState: vi.fn(async () => undefined) }));
```

In the `@/lib/wails` mock change `OpenExternal: vi.fn()` to `OpenExternal: (...args: unknown[]) => openExternalMock(...args)`. Append the tests:

```tsx
describe('SettingsView — account lifecycle', () => {
  beforeEach(() => {
    fixtures.ai = false;
    fixtures.origin = 'https://app.example.com';
    fixtures.authClient = {
      listAccounts: async () => ({ data: [{ id: 'acc1', provider: 'credential' }] }),
      deleteUser: vi.fn(async () => ({ error: null })),
    };
    openExternalMock.mockClear();
    clearStoredTokenMock.mockClear();
    signOutMock.mockClear();
  });

  it('"Download my data" opens the web account page', async () => {
    renderSettings();
    await userEvent.click(await screen.findByRole('button', { name: /download my data/i }));
    expect(openExternalMock).toHaveBeenCalledWith('https://app.example.com/settings?tab=account');
  });

  it('deleteUser with the password, then clearStoredToken and signOut', async () => {
    renderSettings();
    await userEvent.click(await screen.findByRole('button', { name: /delete account/i }));
    await userEvent.type(await screen.findByLabelText('Type your email to confirm'), 'ada@calendium.app');
    await userEvent.type(await screen.findByLabelText('Your password'), 'hunter2');
    await userEvent.click(screen.getByRole('button', { name: /delete my account/i }));
    await waitFor(() => expect(fixtures.authClient?.deleteUser).toHaveBeenCalledWith({ password: 'hunter2' }));
    await waitFor(() => expect(clearStoredTokenMock).toHaveBeenCalled());
    expect(signOutMock).toHaveBeenCalled();
  });

  it('keeps "Delete my account" disabled until the typed email matches', async () => {
    renderSettings();
    await userEvent.click(await screen.findByRole('button', { name: /delete account/i }));
    await userEvent.type(await screen.findByLabelText('Type your email to confirm'), 'someone@else.test');
    await userEvent.type(await screen.findByLabelText('Your password'), 'hunter2');
    expect(screen.getByRole('button', { name: /delete my account/i })).toBeDisabled();
  });

  it('without a Better Auth client it opens the web settings page instead', async () => {
    fixtures.authClient = null;
    renderSettings();
    await userEvent.click(await screen.findByRole('button', { name: /delete account/i }));
    expect(openExternalMock).toHaveBeenCalledWith('https://app.example.com/settings?tab=account');
    expect(screen.queryByLabelText('Type your email to confirm')).toBeNull();
  });
});

describe('SettingsView — background AI switch', () => {
  it('renders when AI is on and PUTs the flipped document', async () => {
    fixtures.ai = true;
    renderSettings();
    const toggle = (await screen.findByLabelText('Background AI processing')) as HTMLInputElement;
    await waitFor(() => expect(toggle.checked).toBe(true));
    await userEvent.click(toggle);
    await waitFor(() =>
      expect(updateSettingsMock).toHaveBeenCalledWith({ timeZone: 'UTC', workingHours: [], workingLocation: '', aiBackground: false })
    );
  });

  it('is absent when the server disables AI', async () => {
    fixtures.ai = false;
    renderSettings();
    await screen.findByText('Appearance');
    expect(screen.queryByLabelText('Background AI processing')).toBeNull();
  });
});
```

- [ ] **Step 2: Run and confirm the failure.** `bun run test:desktop -- SettingsView` — expected: `Unable to find role="button" and name /download my data/i`, `mockSettings` missing.

- [ ] **Step 3: Add the settings mock to `apps/desktop/frontend/src/lib/mock.ts`** (append; add `UserSettings` to the `@calendium/shared` type import):

```ts
// --- Settings (background-AI switch) -----------------------------------------
let mockSettingsState: UserSettings = { timeZone: 'UTC', workingHours: [], workingLocation: '', aiBackground: true };

export function mockSettings(): UserSettings {
  return { ...mockSettingsState };
}

export function updateMockSettings(next: UserSettings): UserSettings {
  mockSettingsState = { ...next };
  return mockSettings();
}
```

- [ ] **Step 4: Update `apps/desktop/frontend/src/views/SettingsView.tsx`.** Imports: add `type UserSettings` to the `@calendium/shared` type import; change `import { clearStoredToken, signOut } from '@/lib/auth';` to `import { clearStoredToken, getAuthClient, signOut } from '@/lib/auth';`; add `Download, Trash2` to the `lucide-react` import; add `mockSettings, updateMockSettings` to the `@/lib/mock` import list.

Add before `function Section(...)`:

```tsx
/** Settings → AI → "Background AI processing" (piece 3): one field of /v1/settings. */
function BackgroundAiSection() {
  const queryClient = useQueryClient();
  const { data: settings } = useQuery({
    queryKey: ['settings'],
    queryFn: () =>
      orMock(
        () => api.getSettings(),
        () => mockSettings()
      ),
  });
  const toggle = useMutation({
    mutationFn: (aiBackground: boolean) => {
      const next: UserSettings = { ...(settings as UserSettings), aiBackground };
      return orMock(
        () => api.updateSettings(next),
        () => updateMockSettings(next)
      );
    },
    onSuccess: (s) => queryClient.setQueryData(['settings'], s),
    onError: (e) => toast({ title: 'Could not update background AI', description: errorMessage(e), variant: 'destructive' }),
  });
  return (
    <Section title="Background AI">
      <div className="flex items-center gap-3 p-3">
        <div className="min-w-0 flex-1">
          <label className="text-sm font-medium" htmlFor="background-ai-toggle">
            Background AI processing
          </label>
          <p className="text-xs text-muted-foreground">
            Summaries, quick replies, auto drafts, classifiers and your writing profile run automatically when mail arrives.
          </p>
        </div>
        <input
          id="background-ai-toggle"
          type="checkbox"
          checked={settings?.aiBackground ?? true}
          disabled={!settings || toggle.isPending}
          onChange={(e) => toggle.mutate(e.target.checked)}
          aria-label="Background AI processing"
        />
      </div>
    </Section>
  );
}
```

Inside `SettingsView`, after `handleSwitchServer` add:

```tsx
  // --- Account (piece 3): export on the web, deletion in-app ---
  const [deleting, setDeleting] = useState(false);
  const [typedEmail, setTypedEmail] = useState('');
  const [deletePassword, setDeletePassword] = useState('');
  const [hasCredential, setHasCredential] = useState<boolean | null>(null);
  const [deleteBusy, setDeleteBusy] = useState(false);

  function openWebAccount(): boolean {
    const origin = webOrigin(config);
    if (!origin) {
      toast({ title: 'No web URL for this server', variant: 'destructive' });
      return false;
    }
    desktop.OpenExternal(`${origin}/settings?tab=account`);
    return true;
  }

  function openDataExport() {
    if (demoMode) {
      toast({ title: 'Not available in demo' });
      return;
    }
    openWebAccount();
  }

  async function startDelete() {
    if (demoMode) {
      toast({ title: 'Not available in demo' });
      return;
    }
    const c = getAuthClient();
    if (!c) {
      // No Better Auth client (server not configured): the web app owns the flow.
      openWebAccount();
      return;
    }
    setDeleting(true);
    try {
      const { data } = await c.listAccounts();
      setHasCredential(!!data?.some((a) => a.provider === 'credential'));
    } catch {
      setHasCredential(false);
    }
  }

  async function runDelete() {
    const c = getAuthClient();
    if (!c) return;
    setDeleteBusy(true);
    try {
      const { error } = await c.deleteUser(hasCredential ? { password: deletePassword } : {});
      if (error) {
        const err = error as { status?: number; code?: string; details?: { teams?: { name: string }[] } };
        const teams = err.details?.teams ?? [];
        if (err.code === 'owns_teams' || teams.length > 0) {
          toast({ title: 'Transfer your teams first', description: teams.map((t) => t.name).join(', '), variant: 'destructive' });
        } else if (err.code === 'INVALID_PASSWORD' || err.status === 400 || err.status === 401) {
          toast({ title: 'Incorrect password', variant: 'destructive' });
        } else if (err.status === 403) {
          toast({ title: 'Sign in again', description: 'For your security, sign in again and then retry.', variant: 'destructive' });
        } else {
          toast({ title: 'Could not delete your account', description: 'Try again.', variant: 'destructive' });
        }
        return;
      }
      clearStoredToken();
      await signOut();
      void clearOfflineState();
      toast({ title: 'Account deleted' });
    } finally {
      setDeleteBusy(false);
    }
  }

  const emailMatches = typedEmail.trim().toLowerCase() === (user?.email ?? '').toLowerCase() && typedEmail.trim() !== '';
```

Replace the Account `<Section title="Account">...</Section>` with:

```tsx
        <Section title="Account">
          <div className="flex items-center gap-3 p-3">
            <div className="flex size-9 select-none items-center justify-center rounded-full bg-secondary text-sm font-semibold text-secondary-foreground">
              {(user?.name ?? user?.email ?? '?').slice(0, 1).toUpperCase()}
            </div>
            <div className="min-w-0">
              <div className="truncate text-sm font-medium">{user?.name ?? 'Signed out'}</div>
              <div className="truncate text-xs text-muted-foreground">{user?.email ?? '—'}</div>
            </div>
            <Button variant="outline" size="sm" className="ml-auto" onClick={handleSignOut}>
              <LogOut /> {demoMode ? 'Exit demo' : 'Sign out'}
            </Button>
          </div>
          <div className="flex flex-wrap items-center gap-2 border-t p-3">
            <Button variant="outline" size="sm" onClick={openDataExport}>
              <Download /> Download my data
            </Button>
            {!deleting && (
              <Button variant="outline" size="sm" className="text-destructive" onClick={() => void startDelete()}>
                <Trash2 /> Delete account
              </Button>
            )}
          </div>
          {deleting && (
            <div className="flex flex-col gap-2 border-t p-3">
              <p className="text-xs text-muted-foreground">
                This permanently deletes your account and its data. Type your email to confirm
                {hasCredential ? ' and enter your password' : ''}. There is no undo.
              </p>
              <Input
                aria-label="Type your email to confirm"
                placeholder={user?.email ?? ''}
                value={typedEmail}
                onChange={(e) => setTypedEmail(e.target.value)}
              />
              {hasCredential && (
                <Input
                  aria-label="Your password"
                  type="password"
                  placeholder="Password"
                  value={deletePassword}
                  onChange={(e) => setDeletePassword(e.target.value)}
                />
              )}
              <div className="flex gap-2">
                <Button variant="outline" size="sm" onClick={() => setDeleting(false)} disabled={deleteBusy}>
                  Cancel
                </Button>
                <Button
                  variant="destructive"
                  size="sm"
                  onClick={() => void runDelete()}
                  disabled={deleteBusy || hasCredential === null || !emailMatches || (hasCredential && deletePassword.length === 0)}
                >
                  Delete my account
                </Button>
              </div>
            </div>
          )}
        </Section>
```

Before `{config?.features?.ai && <ClassifiersSection />}` add `{config?.features?.ai && <BackgroundAiSection />}`.

- [ ] **Step 5: Run the desktop suite.** `bun run test:desktop` — expected: all pass.

- [ ] **Step 6: Commit.**

```bash
git add apps/desktop/frontend/src/views/SettingsView.tsx apps/desktop/frontend/src/views/SettingsView.test.tsx apps/desktop/frontend/src/lib/mock.ts
git commit -m "feat(desktop): Account controls (in-app delete via better-auth client, export link-out) + background AI switch" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 20: Operator docs, env templates, compose and Caddy (Track D; starts immediately)

**Files:**
- Modify: `docs/self-hosting/configuration.md` (intro L4–7; "Core & database" table L22–31; minimal `.env` block L132–143)
- Modify: `docs/self-hosting/security.md` (checklist L8–18; section 1 paragraph L23–26; section 3 table L59–69; append section "10. Internal API surface")
- Modify: `docs/architecture.md` (REST table L57–95; adapter tree comment is piece 1's)
- Modify: `.env.example` (after `TOKEN_ENCRYPTION_KEY=` L38)
- Modify: `backend/.env.example` (after `TOKEN_ENCRYPTION_KEY=` L22)
- Modify: `docker-compose.yml` (`web` service `environment:` L92–96)
- Modify: `deploy/caddy/Caddyfile` (the `handle @api` block L18–21)

**Interfaces:** none (operator-facing). Every value below is copied from the Global Constraints.

- [ ] **Step 1: Write the failing doc checks** as a shell assertion you will re-run after editing (no test file; these are the acceptance criteria):

```bash
grep -q 'INTERNAL_API_SECRET' docs/self-hosting/configuration.md && \
grep -q 'INTERNAL_API_URL' docs/self-hosting/configuration.md && \
grep -q 'INTERNAL_API_SECRET' docs/self-hosting/security.md && \
grep -q '10. Internal API surface' docs/self-hosting/security.md && \
grep -q '/v1/internal/users/{id}' docs/architecture.md && \
grep -q '/v1/me/export' docs/architecture.md && \
grep -q 'aiBackground' docs/architecture.md && \
grep -q '^INTERNAL_API_SECRET=' .env.example && \
grep -q '^INTERNAL_API_SECRET=' backend/.env.example && \
grep -q 'INTERNAL_API_URL' docker-compose.yml && \
grep -q '/v1/internal/\*' deploy/caddy/Caddyfile && echo DOCS-OK
```

Run it now — expected: no `DOCS-OK` (first grep fails).

- [ ] **Step 2: `docs/self-hosting/configuration.md`.** Change the intro sentence `Only **`DATABASE_URL`** and **`TOKEN_ENCRYPTION_KEY`** are required to boot;` to `Only **`DATABASE_URL`**, **`TOKEN_ENCRYPTION_KEY`** and **`INTERNAL_API_SECRET`** are required to boot;`. In the "Core & database" table add, directly after the `TOKEN_ENCRYPTION_KEY` row:

```markdown
| `INTERNAL_API_SECRET` | **Yes** | — | 32-byte secret as **exactly 64 hex chars** (`openssl rand -hex 32`), required in both modes. Authenticates the web app's server-to-server calls to `/v1/internal/*` (account deletion). **Set the same value on `api` and `web`.** Never public, never `NEXT_PUBLIC_*`. |
| `INTERNAL_API_URL` | No | compose: `http://api:8080`; bare: `http://localhost:8080` | **Web only.** Base URL of the Go API as reachable from the web server (in-network), used for account deletion. |
```

In the minimal `.env` block add `INTERNAL_API_SECRET=<openssl rand -hex 32>   # same value for api and web` right after the `TOKEN_ENCRYPTION_KEY=` line.

- [ ] **Step 3: `docs/self-hosting/security.md`.** In the checklist add after the `TOKEN_ENCRYPTION_KEY` item: `- [ ] `INTERNAL_API_SECRET` generated fresh, identical on `api` and `web`, and `/v1/internal/*` blocked at the proxy`. In section 1's first paragraph change `Stripe keys (cloud only), and the token-encryption key` to `Paddle keys (cloud only), the token-encryption key and the internal API secret`. In section 3's table add a row before the `GOOGLE_CLIENT_SECRET, ...` row:

```markdown
| `INTERNAL_API_SECRET` | **SECRET** | Shared by the `api` and `web` services only (server-to-server account deletion). Never served, never logged, never `NEXT_PUBLIC_*`. |
```

Append at the end of the file:

```markdown
---

## 10. Internal API surface

The Go API exposes `/v1/internal/*` for the web app's server-to-server calls
— today exactly one route, `DELETE /v1/internal/users/{id}`, which account
deletion uses (Better Auth's `beforeDelete` hook calls it before removing
the auth rows). It is authenticated **only** by the `X-Internal-Secret`
header carrying `INTERNAL_API_SECRET` (constant-time compared), never by a
user token, and it is excluded from CORS. With no secret configured, or a
wrong header, the API answers exactly like an unknown route (plain 404), so
the surface cannot be probed.

Keep it off the public internet anyway: the bundled Caddyfile answers
`/v1/internal/*` with 404 before anything reaches the API, and a custom
proxy must do the same. The web container reaches the API over the compose
network via `INTERNAL_API_URL=http://api:8080`, so blocking the path at the
edge costs nothing.
```

- [ ] **Step 4: `docs/architecture.md`.** In the REST table add, after the `GET /v1/me` row:

```markdown
| `GET /v1/me/export` | Streams `calendium-export-<date>.zip` with the user's data (one per hour; `409 export_throttled` + `Retry-After`; never delegable) |
| `DELETE /v1/internal/users/{id}` | Account purge for the web app's Better Auth `beforeDelete` hook (unauthenticated JWT-wise: `X-Internal-Secret` = `INTERNAL_API_SECRET`; 204; `409 owns_teams` with `details.teams`; `502 billing_unavailable`; wrong/missing secret → plain 404) |
```

and after the `GET/PUT /v1/prefs` row:

```markdown
| `GET/PUT /v1/settings` | Scheduling settings `{timeZone, workingHours, workingLocation, aiBackground}`; `aiBackground` (default `true`) gates every background AI job and is kept when a PUT omits it |
```

- [ ] **Step 5: env templates.** In `.env.example`, after the `TOKEN_ENCRYPTION_KEY=` line add:

```dotenv
# 32-byte hex secret shared by the api and web services for server-to-server
# calls (/v1/internal/*, account deletion). REQUIRED in both modes.
# Generate: openssl rand -hex 32
INTERNAL_API_SECRET=
# Web only: where the web server reaches the Go API in-network. Compose
# defaults it to http://api:8080; set only when the API lives elsewhere.
INTERNAL_API_URL=
```

In `backend/.env.example`, after its `TOKEN_ENCRYPTION_KEY=` line add:

```dotenv
# 32-byte hex secret for the web app's server-to-server calls to /v1/internal/*
# (account deletion). Must equal the web service's INTERNAL_API_SECRET.
# Generate: openssl rand -hex 32
INTERNAL_API_SECRET=
```

- [ ] **Step 6: `docker-compose.yml`.** In the `web` service's `environment:` block, after `NEXT_PUBLIC_API_URL: ${NEXT_PUBLIC_API_URL:-}` add:

```yaml
      # Server-to-server base URL of the Go API for account deletion
      # (/v1/internal/*). Like AUTH_JWKS_URL above, the public origin is not
      # reachable from inside the network, so default to the api service.
      INTERNAL_API_URL: ${INTERNAL_API_URL:-http://api:8080}
```

- [ ] **Step 7: `deploy/caddy/Caddyfile`.** Replace the `handle @api` block with:

```caddyfile
	# API + health checks → Go backend. /v1/internal/* is server-to-server
	# only (web → api over the compose network); never expose it at the edge.
	@api path /v1/* /healthz
	handle @api {
		@internal path /v1/internal/*
		respond @internal 404
		reverse_proxy api:8080
	}
```

- [ ] **Step 8: Re-run the acceptance greps from Step 1** — expected: `DOCS-OK`. Then `docker compose config -q` (read-only validation of the YAML; does not start anything) — expected: no output, exit 0.

- [ ] **Step 9: Commit.**

```bash
git add docs/self-hosting/configuration.md docs/self-hosting/security.md docs/architecture.md .env.example backend/.env.example docker-compose.yml deploy/caddy/Caddyfile
git commit -m "docs(self-hosting): INTERNAL_API_SECRET/INTERNAL_API_URL, internal API surface, export/purge routes; compose web env; Caddy blocks /v1/internal/*" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 21: Full-suite gate (after every track has merged)

**Files:** none created; fixes go into the owning files from the tasks above.

- [ ] **Step 1: Backend.** `cd backend && go build ./... && go vet ./... && REQUIRE_DOCKER=1 go test ./...` — expected: every package `ok` (Docker required for the Postgres suite).
- [ ] **Step 2: TypeScript suites.** `bun run test:shared test:web test:desktop test:mobile` — expected: all green.
- [ ] **Step 3: Lint.** `bunx biome check .` — expected: no diagnostics. `cd backend && golangci-lint run ./...` and `cd apps/desktop && golangci-lint run ./...` — expected: no findings.
- [ ] **Step 4: e2e.** `bun run test:e2e` — expected: all specs pass (demo mode; `settings.spec.ts` includes the Account tab test).
- [ ] **Step 5: Type-check the web app the way the Docker build does.** `cd apps/web && bun x next build` — expected: build succeeds with no type errors (this catches any `UserSettings` literal missing `aiBackground`). Delete the generated `.next` directory afterwards only if it did not exist before.
- [ ] **Step 6: Lefthook dry run.** `lefthook run pre-push` — expected: all jobs pass.
- [ ] **Step 7: Commit any fix-ups** with `git commit -m "chore: full-suite gate fix-ups for account lifecycle" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"` (only if something changed).

---

### Task 22: Verification runbook — local compose stack, throwaway users only (after Task 21)

**Files:** none. Marks itself **BLOCKED** (not failed) when a required operator-supplied value is absent.

Prerequisites (operator-supplied; if any is missing, record `BLOCKED: <name>` and stop):
- A local stack started by the operator (`docker compose up -d` — this plan never starts or stops it) with `.env` containing `INTERNAL_API_SECRET` (64 hex), `BETTER_AUTH_SECRET`, `TOKEN_ENCRYPTION_KEY`, `DATABASE_URL`, and `SELF_HOSTED=false` plus piece 1's `PADDLE_*` sandbox values if the Paddle-cancel path is to be exercised (otherwise `SELF_HOSTED=true` verifies everything except the cancel).
- `psql` access to the stack's database (`DATABASE_URL`).
- `WEB=http://localhost:3000`, `API=http://localhost:8080`.

- [ ] **Step 1: Sign up a throwaway user and mint a JWT.**

```bash
EMAIL="purge-$(date +%s)@example.test"
curl -s -c cj.txt -X POST "$WEB/api/auth/sign-up/email" -H 'Content-Type: application/json' \
  -d "{\"name\":\"Purge Test\",\"email\":\"$EMAIL\",\"password\":\"correct-horse-battery\"}" | head -c 200; echo
TOKEN=$(curl -s -b cj.txt "$WEB/api/auth/token" | sed -E 's/.*"token":"([^"]+)".*/\1/')
curl -s "$API/v1/me" -H "Authorization: Bearer $TOKEN"
```

Expected: a JSON user with the signup email. Record its `id` as `UID`.

- [ ] **Step 2: Seed data.**

```bash
curl -s -X POST "$API/v1/mail/snippets" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"name":"Thanks","shortcut":null,"bodyHtml":"<p>thanks</p>"}'
curl -s -X POST "$API/v1/tasks" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"title":"Export me"}'
curl -s -X POST "$API/v1/teams" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"name":"Purge Team"}'
curl -s -X PUT "$API/v1/settings" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"timeZone":"UTC","workingHours":[],"workingLocation":"","aiBackground":false}'
```

Expected: 200/201 each; the settings response shows `"aiBackground":false`. Record the team id as `TEAM`.

- [ ] **Step 3: Export twice.**

```bash
curl -s -D - -o export.zip "$API/v1/me/export" -H "Authorization: Bearer $TOKEN" | head -5
unzip -l export.zip
curl -s -D - -o /dev/null "$API/v1/me/export" -H "Authorization: Bearer $TOKEN" | head -8
```

Expected: first call `200`, `Content-Type: application/zip`, `Content-Disposition: attachment; filename="calendium-export-<today>.zip"`; `unzip -l` lists the 16 entries (`profile.json … event-notes.json`, no `threads/` since no mailbox) and `unzip -p export.zip snippets.json` contains `"Thanks"`; `unzip -p export.zip settings.json` contains `"aiBackground": false`; second call `409` with `Retry-After: <seconds>` and body code `export_throttled`.

- [ ] **Step 4: Team ownership refusal, then deletion.** Sign up a second throwaway user (repeat Step 1 with a different email → `TOKEN2`, `UID2`), invite + accept them into `TEAM` (`POST /v1/teams/$TEAM/invitations` with `{"email":"<second email>","role":"member"}` as user 1, then `POST /v1/invitations/accept` with the token from the invite email — the stack has no mailer, so read the token hash from `psql -c "select id from team_invitations"` is NOT possible; instead promote via the API by making user 2 a member through `PATCH /v1/teams/$TEAM/members/$UID2` only after acceptance — if the invite cannot be accepted without mail, mark this sub-step `BLOCKED: team invitation acceptance needs an email channel` and continue with Step 4b).
  - 4a. In the web app signed in as user 1, Settings → Account → Delete account → type the email + password → the dialog shows "Transfer ownership of these teams…" listing **Purge Team**; nothing is deleted (`GET /v1/me` still 200). Then `PATCH /v1/teams/$TEAM/members/$UID2 {"role":"owner"}`, retry the dialog → the browser lands on `/goodbye`.
  - 4b. (If 4a is blocked) delete the team first via `DELETE /v1/teams/$TEAM`, then run the dialog → `/goodbye`.

- [ ] **Step 5: Database and log assertions.**

```bash
psql "$DATABASE_URL" -tAc "select count(*) from users where id='$UID'; select count(*) from snippets where user_id='$UID'; select count(*) from tasks where user_id='$UID'; select count(*) from team_members where user_id='$UID'; select count(*) from user_settings where user_id='$UID'; select count(*) from user_exports where user_id='$UID'; select count(*) from \"user\" where id='$UID'; select count(*) from \"session\" where \"userId\"='$UID'; select count(*) from \"account\" where \"userId\"='$UID'; select count(*) from \"verification\" where identifier='$EMAIL';"
psql "$DATABASE_URL" -tAc "select id, created_by is null from teams where id='$TEAM'"
docker compose logs api 2>/dev/null | grep 'user purged' | tail -1
```

Expected: every count `0`; (4a) the team row exists with `created_by IS NULL` = `t`; the API log line contains `userId=<UID>` and does NOT contain the email.

- [ ] **Step 6: Cloud-only Paddle cancel (BLOCKED without sandbox keys).** With `SELF_HOSTED=false` and piece 1's sandbox checkout completed for a third throwaway user, delete that user and confirm in the Paddle sandbox dashboard that the subscription is `canceled` immediately and the API log shows `subscriptionCanceled=true`. Without `PADDLE_API_KEY`/`PADDLE_PRICE_ID_ANNUAL`, record `BLOCKED: Paddle sandbox keys` — the unit tests (`TestPurgeOrder`, `TestPurgePaddleFailureAborts`) cover the ordering.

- [ ] **Step 7: Internal route hardening.**

```bash
curl -s -o /dev/null -w '%{http_code}\n' -X DELETE "$API/v1/internal/users/nobody"
curl -s -o /dev/null -w '%{http_code}\n' -X DELETE "$API/v1/internal/users/nobody" -H "X-Internal-Secret: $(printf 'cd%.0s' $(seq 32))"
curl -s -o /dev/null -w '%{http_code}\n' -X DELETE "$API/v1/internal/users/nobody" -H "X-Internal-Secret: $(grep '^INTERNAL_API_SECRET=' .env | cut -d= -f2)"
curl -s -o /dev/null -w '%{http_code}\n' -X DELETE "https://$(grep '^DOMAIN=' .env | cut -d= -f2)/v1/internal/users/nobody" -H "X-Internal-Secret: $(grep '^INTERNAL_API_SECRET=' .env | cut -d= -f2)" -k
```

Expected: `404`, `404`, `204` (unknown user is a no-op), and — through Caddy (only when the `caddy` profile is running; otherwise `BLOCKED: caddy profile not running`) — `404`.

- [ ] **Step 8: Mobile and desktop smoke (BLOCKED without a simulator/desktop build).** On an iOS simulator signed in as a throwaway user: Settings → Account → Delete account → password → Alert → confirm → app returns to the sign-in screen; Settings → AI → the switch persists across a cold start. On the desktop app: Settings → Account → Delete account → typed email + password → "Account deleted" toast and the sign-in view. Record `BLOCKED: <platform> build unavailable` when not possible.

- [ ] **Step 9: Record the outcome** (PASS / BLOCKED items) in the PR description. Remove `cj.txt` and `export.zip`.
