# M2.4 — Scheduling & Booking Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
> **Migration numbering:** the eight M2 phase plans were authored in parallel, so migration filenames here are provisional — at execution time use the next free number in `backend/migrations/` and update references in the affected task.

## Goal

Ship the M2.4 phase of the M2 roadmap (`docs/superpowers/specs/2026-07-17-m2-roadmap-design.md`): personal booking links with public booking pages, appointment schedules (windows/buffers/daily limits), meeting polls, propose-new-time RSVP, drag-and-copy availability in the recipient's time zone, recipient-TZ preview, the Time Travel timezone overlay, working hours/location settings, and the Find-a-Time guest availability grid. When done: a visitor with no account opens `https://<web>/book/<slug>`, sees live availability rendered in their own time zone, books a slot, an event lands on the owner's Google/Microsoft calendar, and both sides get a confirmation email sent through the owner's connected account — with double-booking made impossible by a database-level slot hold plus a live provider free/busy re-check.

## Architecture

Everything follows the existing hexagon (`backend/internal/{domain,port,service,adapter}`):

- **Domain** (`domain/scheduling.go`): `BookingLink`, `Booking`, `AvailabilityWindow`, `MeetingPoll`, `PollOption`, `PollVote`, `TimeProposal`, `UserSettings`, `BusyInterval`. Pure data + validation, stdlib only.
- **Ports**: new driving port `port.SchedulingService` + `port.SettingsService`; new driven repos `BookingLinkRepo`, `BookingRepo`, `PollRepo`, `TimeProposalRepo`, `UserSettingsRepo`; one new method on the existing `port.CalendarProvider` gateway: `FreeBusy` (Google `POST /calendar/v3/freeBusy`, Graph `POST /me/calendar/getSchedule`).
- **Service** (`service/scheduling.go`, `service/settings.go`): slot computation (link windows ∩ owner free gaps − buffers − existing bookings − daily limit, discretized to the link duration), the hold→re-check→confirm booking pipeline, poll lifecycle, proposal accept (provider write-through via the same pattern `CalendarService` uses), confirmation email via the owner's `MailProvider.Send`.
- **HTTP adapter**: public (unauthenticated) routes join `/v1/instance` in the unauthenticated section of `httpapi.New`, but wrapped in a new in-memory token-bucket rate limiter (`httpapi/ratelimit.go`). Authenticated owner CRUD routes use the existing `authed(...)` helper.
- **Persistence**: migration `0005_scheduling.sql`. Double-booking is prevented *in the database* by a `tstzrange` GiST exclusion constraint over active (hold/confirmed) bookings per link — the service layer treats SQLSTATE `23P01` as `domain.ErrConflict`. Live provider free/busy is re-checked after the hold and before the provider event is created.
- **Worker** (`cmd/worker`): the existing `ProcessDueWork` loop gains `SchedulingService.ExpireHolds` (cancels holds older than 5 minutes) so abandoned holds free their slots.
- **Web**: public pages live in the existing public marketing route group — `apps/web/app/(marketing)/book/[slug]/page.tsx` and `(marketing)/poll/[token]/page.tsx` — talking to the public API endpoints with plain `fetch` (no auth client). In-app surfaces (booking-link manager, Find-a-Time, Time Travel, working hours) live in the `(app)` group and go through `@calendium/shared` `ApiClient` + the `lib/*-data.ts` wrapper pattern with `DEMO_MODE`-gated mocks.
- **Shared contract** (`packages/shared`): types + `ApiClient` methods for every new endpoint, plus standalone unauthenticated fetchers (`fetchPublicBookingLink`, `fetchPublicSlots`, `createPublicBooking`, `fetchPublicPoll`, `votePublicPoll`) mirroring the existing `fetchInstance` pattern.

### Key decisions (locked)

1. **Public-route auth exemption**: public routes are registered directly on the mux (never through `requireAuth`), exactly like `GET /v1/instance` today, under a dedicated `/v1/public/...` prefix so the exemption is visible in the path itself. Abuse protection = per-IP token-bucket rate limiting (stdlib, in-memory, per-process), uniform 404s for unknown slugs/tokens (no enumeration oracle), payload size caps via `http.MaxBytesReader`, and no owner PII beyond display name on public responses.
2. **Double-booking prevention**: (a) DB exclusion constraint `EXCLUDE USING gist (link_id WITH =, tstzrange(start_at,end_at,'[)') WITH &&) WHERE (status IN ('hold','confirmed'))` makes two concurrent bookings for overlapping slots impossible — one INSERT wins, the other gets `23P01` → 409; (b) after the hold row commits, the service re-checks the owner's **live** provider free/busy for the slot (catching events created outside Calendium since the last sync) before creating the provider event; on busy, the hold is cancelled and the visitor gets 409. Holds expire after 5 minutes (worker sweep) so failures/abandons cannot wedge a slot.
3. **Slug uniqueness**: case-insensitive unique index `ON booking_links (lower(slug))`; slugs validated against `^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$` and a reserved list (`api`, `www`, `book`, `admin`, `app`, `settings`, `pricing`, `docs`); create returns `domain.ErrConflict` → 409 on collision.
4. **Booking emails**: sent through the owner's connected account via the existing `port.MailProvider.Send` + `tokenSource` (same pipeline the draft worker delivery uses) — no new SMTP infrastructure. To: invitee, Cc: owner. Email construction is a pure function so it is unit-testable; a send failure never rolls back a confirmed booking (booking + event are the source of truth; the failure is logged).
5. **Propose-new-time scope**: proposals are created by *authenticated* invitees on mirrored events they attend (in-app flow); the organizer accepts with one click, which patches the provider event through the existing `CalendarService.UpdateEvent` write-through and marks all other proposals superseded. A public-token proposal surface for non-Calendium invitees is explicitly out of scope for M2.4 (it rides the poll infrastructure in a later phase).
6. **Working hours** constrain *both* the private `GET /v1/availability` computation and public booking slots (link windows are intersected with working hours when `booking_links.respect_working_hours` is true, default true). Working location is display-only metadata (shown to teammates in M2.7); it is stored now so the settings surface ships once.

## Tech Stack

- **Backend**: Go 1.26, stdlib only (`net/http` mux routing, `database/sql` on pgx stdlib driver, `sync` for the rate limiter). No new Go dependencies. Postgres extension `btree_gist` (ships with Postgres, enabled in the migration).
- **Tests**: Go `testing` + the existing fakes (`service/fakes_test.go`), httpapi harness (`httpapi/harness_test.go`), testcontainers Postgres for repo tests, `httptest` for provider adapters.
- **Shared**: TypeScript, vitest (`packages/shared`).
- **Web**: Next.js 15 App Router, shadcn new-york components, TanStack Query, date-fns, `Intl` APIs for all time-zone math (no new TZ library — `Intl.DateTimeFormat` with `timeZone` + `Intl.supportedValuesOf('timeZone')`). Vitest + Testing Library; Playwright e2e in demo mode.

## Global Constraints

- **Mocks only behind demo flags**: every web data wrapper hits the real API and falls back to mocks **only** when `DEMO_MODE` (`NEXT_PUBLIC_DEMO_MODE=true`, `apps/web/lib/demo.ts`) is on. Never fabricate success outside demo mode.
- **stdlib-only backend**: no third-party Go packages beyond what `go.mod` already carries (pgx driver, testcontainers for tests).
- **Lint/test gates**: every task ends green on the gates it touches — `cd backend && go test ./...`, `bun run test:shared`, `bun run test:web`, `bun run lint:js`, `bun run lint:go`, `bun run typecheck`. Full pre-push suite (`bun run lint && bun run test`) before merging.
- **Conventional commits**: one commit per task, `feat(scope): ...` / `test(scope): ...` / `chore(scope): ...` (e.g. `feat(backend): booking links domain + migration`).
- **TDD**: every task writes the failing test first, watches it fail, then implements.
- **Contract sync**: `packages/shared/src/types.ts` mirrors `backend/internal/domain` field-for-field (camelCase JSON), as today.

---

### Task 1: Domain model + migration 0005

**Files:**
- `backend/internal/domain/scheduling.go` (new)
- `backend/internal/domain/domain_test.go` (extend)
- `backend/migrations/0005_scheduling.sql` (new)

**Interfaces:** (exact Go — this is the core model, written out in full)

```go
package domain

import (
	"fmt"
	"regexp"
	"time"
)

// AvailabilityWindow is one weekly recurring open window on a booking link
// or in a user's working hours, expressed in the owning entity's time zone.
type AvailabilityWindow struct {
	Weekday int    `json:"weekday"` // 0=Sunday … 6=Saturday
	Start   string `json:"start"`   // "09:00" (24h HH:MM)
	End     string `json:"end"`     // "17:00", must be > Start
}

// BookingLink is a personal Calendly-style scheduling page:
// /book/{slug} → live slots → visitor books → event on CalendarID.
type BookingLink struct {
	ID              string               `json:"id"`
	UserID          string               `json:"-"`
	Slug            string               `json:"slug"`
	Title           string               `json:"title"`
	Description     *string              `json:"description"`
	CalendarID      string               `json:"calendarId"` // target (writable) calendar
	DurationMinutes int                  `json:"durationMinutes"`
	TimeZone        string               `json:"timeZone"` // IANA name; windows interpreted here
	Windows         []AvailabilityWindow `json:"windows"`
	BufferBeforeMin int                  `json:"bufferBeforeMin"`
	BufferAfterMin  int                  `json:"bufferAfterMin"`
	DailyLimit      int                  `json:"dailyLimit"` // 0 = unlimited confirmed bookings/day
	MinNoticeMin    int                  `json:"minNoticeMin"`
	MaxAdvanceDays  int                  `json:"maxAdvanceDays"` // 0 = default 60
	RespectWorkingHours bool             `json:"respectWorkingHours"`
	AddConferencing bool                 `json:"addConferencing"`
	Active          bool                 `json:"active"`
	CreatedAt       time.Time            `json:"createdAt"`
}

// BookingStatus is the slot-hold lifecycle: hold → confirmed | cancelled.
type BookingStatus string

const (
	BookingHold      BookingStatus = "hold"
	BookingConfirmed BookingStatus = "confirmed"
	BookingCancelled BookingStatus = "cancelled"
)

// Booking is one visitor reservation against a BookingLink. While Status is
// "hold" the row blocks the slot (DB exclusion constraint) but no provider
// event exists yet; HoldExpiresAt bounds how long an unconfirmed hold lives.
type Booking struct {
	ID            string        `json:"id"`
	LinkID        string        `json:"linkId"`
	Status        BookingStatus `json:"status"`
	Start         time.Time     `json:"start"`
	End           time.Time     `json:"end"`
	InviteeName   string        `json:"inviteeName"`
	InviteeEmail  string        `json:"inviteeEmail"`
	InviteeTZ     string        `json:"inviteeTimeZone"`
	Note          *string       `json:"note"`
	EventID       *string       `json:"eventId"` // mirrored event once confirmed
	HoldExpiresAt *time.Time    `json:"-"`
	CreatedAt     time.Time     `json:"createdAt"`
}

// PollStatus is the meeting-poll lifecycle.
type PollStatus string

const (
	PollOpen      PollStatus = "open"
	PollConfirmed PollStatus = "confirmed"
	PollCancelled PollStatus = "cancelled"
)

// MeetingPoll proposes candidate slots invitees vote on via a public link;
// the organizer confirms the winner, creating a real event.
type MeetingPoll struct {
	ID              string       `json:"id"`
	UserID          string       `json:"-"`
	Token           string       `json:"token"` // unguessable public URL token (32 hex chars)
	Title           string       `json:"title"`
	Description     *string      `json:"description"`
	CalendarID      string       `json:"calendarId"`
	DurationMinutes int          `json:"durationMinutes"`
	Options         []PollOption `json:"options"`
	Status          PollStatus   `json:"status"`
	WinnerOptionID  *string      `json:"winnerOptionId"`
	EventID         *string      `json:"eventId"`
	CreatedAt       time.Time    `json:"createdAt"`
}

// PollOption is one candidate slot on a poll.
type PollOption struct {
	ID    string    `json:"id"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// PollVoteChoice is one voter's answer for one option.
type PollVoteChoice string

const (
	VoteYes      PollVoteChoice = "yes"
	VoteNo       PollVoteChoice = "no"
	VoteIfNeeded PollVoteChoice = "if_needed"
)

// PollVote is one voter's full ballot (one row per voter per option).
type PollVote struct {
	PollID     string         `json:"-"`
	OptionID   string         `json:"optionId"`
	VoterEmail string         `json:"voterEmail"`
	VoterName  string         `json:"voterName"`
	Choice     PollVoteChoice `json:"choice"`
	CreatedAt  time.Time      `json:"createdAt"`
}

// ProposalStatus is the propose-new-time lifecycle.
type ProposalStatus string

const (
	ProposalPending    ProposalStatus = "pending"
	ProposalAccepted   ProposalStatus = "accepted"
	ProposalDeclined   ProposalStatus = "declined"
	ProposalSuperseded ProposalStatus = "superseded"
)

// TimeProposal is an invitee's counter-proposed time for an existing event.
type TimeProposal struct {
	ID            string         `json:"id"`
	EventID       string         `json:"eventId"`
	ProposerEmail string         `json:"proposerEmail"`
	ProposerName  string         `json:"proposerName"`
	Start         time.Time      `json:"start"`
	End           time.Time      `json:"end"`
	Note          *string        `json:"note"`
	Status        ProposalStatus `json:"status"`
	CreatedAt     time.Time      `json:"createdAt"`
}

// UserSettings carries per-user scheduling preferences (working hours in
// TimeZone, displayed location). Zero-value WorkingHours = no constraint.
type UserSettings struct {
	UserID          string               `json:"-"`
	TimeZone        string               `json:"timeZone"`
	WorkingHours    []AvailabilityWindow `json:"workingHours"`
	WorkingLocation string               `json:"workingLocation"` // "", "office", "home", or free text
}

// BusyInterval is one busy span from a provider free/busy query.
type BusyInterval struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

var slugRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

var reservedSlugs = map[string]struct{}{
	"api": {}, "www": {}, "book": {}, "admin": {}, "app": {},
	"settings": {}, "pricing": {}, "docs": {}, "signin": {}, "poll": {},
}

// ValidateSlug enforces the booking-link slug shape and reserved list.
func ValidateSlug(slug string) error {
	if !slugRe.MatchString(slug) {
		return fmt.Errorf("%w: slug must be 1-64 lowercase letters, digits, or hyphens (no leading/trailing hyphen)", ErrValidation)
	}
	if _, reserved := reservedSlugs[slug]; reserved {
		return fmt.Errorf("%w: slug %q is reserved", ErrValidation, slug)
	}
	return nil
}

// Validate checks an AvailabilityWindow's shape ("HH:MM", End > Start, weekday 0-6).
func (w AvailabilityWindow) Validate() error { /* HH:MM parse via time.Parse("15:04", …) */ ... }
```

Migration `0005_scheduling.sql` (core, written out — the exclusion constraint is the concurrency linchpin):

```sql
-- 0005_scheduling.sql — booking links, bookings, meeting polls, time
-- proposals, user scheduling settings. btree_gist backs the anti-double-
-- booking exclusion constraint (text equality inside a GiST index).
CREATE EXTENSION IF NOT EXISTS btree_gist;

CREATE TABLE booking_links (
    id                    text PRIMARY KEY,
    user_id               text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    slug                  text NOT NULL,
    title                 text NOT NULL,
    description           text,
    calendar_id           text NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    duration_minutes      integer NOT NULL,
    time_zone             text NOT NULL DEFAULT 'UTC',
    windows               jsonb NOT NULL DEFAULT '[]',
    buffer_before_min     integer NOT NULL DEFAULT 0,
    buffer_after_min      integer NOT NULL DEFAULT 0,
    daily_limit           integer NOT NULL DEFAULT 0,
    min_notice_min        integer NOT NULL DEFAULT 60,
    max_advance_days      integer NOT NULL DEFAULT 60,
    respect_working_hours boolean NOT NULL DEFAULT true,
    add_conferencing      boolean NOT NULL DEFAULT false,
    active                boolean NOT NULL DEFAULT true,
    created_at            timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX booking_links_slug_idx ON booking_links (lower(slug));
CREATE INDEX booking_links_user_idx ON booking_links (user_id);

CREATE TABLE bookings (
    id              text PRIMARY KEY,
    link_id         text NOT NULL REFERENCES booking_links(id) ON DELETE CASCADE,
    status          text NOT NULL DEFAULT 'hold', -- hold | confirmed | cancelled
    start_at        timestamptz NOT NULL,
    end_at          timestamptz NOT NULL,
    invitee_name    text NOT NULL,
    invitee_email   text NOT NULL,
    invitee_tz      text NOT NULL DEFAULT 'UTC',
    note            text,
    event_id        text REFERENCES events(id) ON DELETE SET NULL,
    hold_expires_at timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CHECK (end_at > start_at),
    -- The anti-double-booking constraint: two active (hold or confirmed)
    -- bookings on the same link can never overlap in time. Concurrent
    -- inserts race safely: exactly one commits, the loser gets SQLSTATE
    -- 23P01, which the repo maps to domain.ErrConflict.
    CONSTRAINT bookings_no_overlap EXCLUDE USING gist (
        link_id WITH =,
        tstzrange(start_at, end_at, '[)') WITH &&
    ) WHERE (status IN ('hold', 'confirmed'))
);
CREATE INDEX bookings_link_idx ON bookings (link_id, start_at DESC);
CREATE INDEX bookings_hold_expiry_idx ON bookings (hold_expires_at)
    WHERE status = 'hold';

CREATE TABLE meeting_polls (
    id               text PRIMARY KEY,
    user_id          text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token            text NOT NULL UNIQUE, -- crypto/rand 16 bytes hex
    title            text NOT NULL,
    description      text,
    calendar_id      text NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    duration_minutes integer NOT NULL,
    options          jsonb NOT NULL DEFAULT '[]', -- [{id,start,end}]
    status           text NOT NULL DEFAULT 'open',
    winner_option_id text,
    event_id         text REFERENCES events(id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX meeting_polls_user_idx ON meeting_polls (user_id);

CREATE TABLE poll_votes (
    poll_id     text NOT NULL REFERENCES meeting_polls(id) ON DELETE CASCADE,
    option_id   text NOT NULL,
    voter_email text NOT NULL,
    voter_name  text NOT NULL DEFAULT '',
    choice      text NOT NULL, -- yes | no | if_needed
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (poll_id, option_id, lower(voter_email))
);

CREATE TABLE time_proposals (
    id             text PRIMARY KEY,
    event_id       text NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    proposer_email text NOT NULL,
    proposer_name  text NOT NULL DEFAULT '',
    start_at       timestamptz NOT NULL,
    end_at         timestamptz NOT NULL,
    note           text,
    status         text NOT NULL DEFAULT 'pending',
    created_at     timestamptz NOT NULL DEFAULT now(),
    CHECK (end_at > start_at)
);
CREATE INDEX time_proposals_event_idx ON time_proposals (event_id, created_at DESC);

CREATE TABLE user_settings (
    user_id          text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    time_zone        text NOT NULL DEFAULT 'UTC',
    working_hours    jsonb NOT NULL DEFAULT '[]',
    working_location text NOT NULL DEFAULT '',
    updated_at       timestamptz NOT NULL DEFAULT now()
);
```

(Note: `PRIMARY KEY (poll_id, option_id, lower(voter_email))` is invalid Postgres — use a `UNIQUE INDEX poll_votes_unique_idx ON poll_votes (poll_id, option_id, lower(voter_email))` plus no PK, matching how `subscriptions_stripe_customer_idx` uses expression indexes. Implement it that way.)

**Steps:**
- [ ] Write failing tests in `backend/internal/domain/domain_test.go`: `TestValidateSlug` (valid slugs, uppercase rejected, leading/trailing hyphen rejected, reserved `api`/`book` rejected, 65-char rejected), `TestAvailabilityWindowValidate` (bad weekday, bad HH:MM, End ≤ Start): `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/domain/ -run 'TestValidateSlug|TestAvailabilityWindow' -v` — confirm failure
- [ ] Create `backend/internal/domain/scheduling.go` with the types above; tests pass
- [ ] Create `backend/migrations/0005_scheduling.sql` (with the poll_votes unique-index fix); verify the embedded migrator picks it up by running any postgres repo test (testcontainers applies all migrations): `cd backend && go test ./internal/adapter/out/postgres/ -run TestStore -v` (requires Docker)
- [ ] `cd backend && go test ./... && golangci-lint run ./...`
- [ ] Commit: `feat(backend): scheduling domain model + migration 0005 (booking links, bookings, polls, proposals, user settings)`

---

### Task 2: Ports — repos, services, provider FreeBusy

**Files:**
- `backend/internal/port/driven.go` (extend)
- `backend/internal/port/driving.go` (extend)
- `backend/internal/service/fakes_test.go` (extend with in-memory fakes)

**Interfaces:** (exact Go)

```go
// --- driven.go additions ---

// BookingLinkRepo persists booking links. Create returns domain.ErrConflict
// on a (case-insensitive) slug collision.
type BookingLinkRepo interface {
	Create(ctx context.Context, l domain.BookingLink) (domain.BookingLink, error)
	GetByID(ctx context.Context, id string) (domain.BookingLink, error)
	// GetBySlug matches case-insensitively; domain.ErrNotFound when absent.
	GetBySlug(ctx context.Context, slug string) (domain.BookingLink, error)
	ListByUser(ctx context.Context, userID string) ([]domain.BookingLink, error)
	Update(ctx context.Context, l domain.BookingLink) error
	Delete(ctx context.Context, id string) error
}

// BookingRepo persists bookings. CreateHold inserts a status="hold" row and
// returns domain.ErrConflict when the DB exclusion constraint rejects an
// overlapping active booking (SQLSTATE 23P01).
type BookingRepo interface {
	CreateHold(ctx context.Context, b domain.Booking) (domain.Booking, error)
	GetByID(ctx context.Context, id string) (domain.Booking, error)
	// ListActiveInRange returns hold+confirmed bookings overlapping [from,to).
	ListActiveInRange(ctx context.Context, linkID string, from, to time.Time) ([]domain.Booking, error)
	ListByUser(ctx context.Context, userID string, limit int) ([]domain.Booking, error)
	// Confirm promotes a hold: status="confirmed", event_id set, hold_expires_at cleared.
	Confirm(ctx context.Context, id, eventID string) error
	Cancel(ctx context.Context, id string) error
	// ExpireHolds cancels holds whose hold_expires_at <= now; returns count.
	ExpireHolds(ctx context.Context, now time.Time) (int64, error)
}

// PollRepo persists meeting polls and votes.
type PollRepo interface {
	Create(ctx context.Context, p domain.MeetingPoll) (domain.MeetingPoll, error)
	GetByID(ctx context.Context, id string) (domain.MeetingPoll, error)
	GetByToken(ctx context.Context, token string) (domain.MeetingPoll, error)
	ListByUser(ctx context.Context, userID string) ([]domain.MeetingPoll, error)
	Update(ctx context.Context, p domain.MeetingPoll) error
	Delete(ctx context.Context, id string) error
	// UpsertVotes replaces the voter's ballot (keyed poll_id+option_id+lower(email)).
	UpsertVotes(ctx context.Context, votes []domain.PollVote) error
	ListVotes(ctx context.Context, pollID string) ([]domain.PollVote, error)
}

// TimeProposalRepo persists propose-new-time counter-proposals.
type TimeProposalRepo interface {
	Create(ctx context.Context, p domain.TimeProposal) (domain.TimeProposal, error)
	GetByID(ctx context.Context, id string) (domain.TimeProposal, error)
	ListByEvent(ctx context.Context, eventID string) ([]domain.TimeProposal, error)
	Update(ctx context.Context, p domain.TimeProposal) error
}

// UserSettingsRepo persists per-user scheduling settings; Get returns a
// zero-value UserSettings (TimeZone "UTC") when no row exists.
type UserSettingsRepo interface {
	Get(ctx context.Context, userID string) (domain.UserSettings, error)
	Upsert(ctx context.Context, s domain.UserSettings) error
}

// --- CalendarProvider extension (driven.go) ---
type CalendarProvider interface {
	// ... existing methods unchanged ...
	// FreeBusy returns busy intervals per requested attendee email between
	// from and to (Google POST /freeBusy; Graph POST /me/calendar/getSchedule).
	// Emails absent from the result were not resolvable by the provider.
	FreeBusy(ctx context.Context, accessToken string, emails []string, from, to time.Time) (map[string][]domain.BusyInterval, error)
}
```

```go
// --- driving.go additions ---

// BookingLinkInput is the create/update booking-link payload.
type BookingLinkInput struct {
	Slug                string                      `json:"slug"`
	Title               string                      `json:"title"`
	Description         string                      `json:"description,omitempty"`
	CalendarID          string                      `json:"calendarId"`
	DurationMinutes     int                         `json:"durationMinutes"`
	TimeZone            string                      `json:"timeZone"`
	Windows             []domain.AvailabilityWindow `json:"windows"`
	BufferBeforeMin     int                         `json:"bufferBeforeMin"`
	BufferAfterMin      int                         `json:"bufferAfterMin"`
	DailyLimit          int                         `json:"dailyLimit"`
	MinNoticeMin        int                         `json:"minNoticeMin"`
	MaxAdvanceDays      int                         `json:"maxAdvanceDays"`
	RespectWorkingHours bool                        `json:"respectWorkingHours"`
	AddConferencing     bool                        `json:"addConferencing"`
	Active              bool                        `json:"active"`
}

// PublicBookingPage is the public GET /v1/public/booking/{slug} document —
// no owner PII beyond display name.
type PublicBookingPage struct {
	Slug            string  `json:"slug"`
	Title           string  `json:"title"`
	Description     *string `json:"description"`
	OwnerName       string  `json:"ownerName"`
	DurationMinutes int     `json:"durationMinutes"`
	TimeZone        string  `json:"timeZone"` // owner's link TZ (for "times shown in…" hints)
}

// BookingRequest is the public booking payload.
type BookingRequest struct {
	Start        time.Time `json:"start"`
	InviteeName  string    `json:"inviteeName"`
	InviteeEmail string    `json:"inviteeEmail"`
	InviteeTZ    string    `json:"inviteeTimeZone"`
	Note         string    `json:"note,omitempty"`
}

// PollInput is the create-poll payload.
type PollInput struct {
	Title           string                    `json:"title"`
	Description     string                    `json:"description,omitempty"`
	CalendarID      string                    `json:"calendarId"`
	DurationMinutes int                       `json:"durationMinutes"`
	Options         []domain.PollOption       `json:"options"` // IDs assigned server-side
}

// PublicPoll is the public poll document incl. anonymized tallies.
type PublicPoll struct {
	Token           string              `json:"token"`
	Title           string              `json:"title"`
	Description     *string             `json:"description"`
	OrganizerName   string              `json:"organizerName"`
	DurationMinutes int                 `json:"durationMinutes"`
	Status          domain.PollStatus   `json:"status"`
	Options         []domain.PollOption `json:"options"`
	Tallies         map[string]PollTally `json:"tallies"` // optionID → tally
	WinnerOptionID  *string             `json:"winnerOptionId"`
}

// PollTally aggregates votes for one option.
type PollTally struct {
	Yes      int `json:"yes"`
	No       int `json:"no"`
	IfNeeded int `json:"ifNeeded"`
}

// PollBallot is one public voter's submission.
type PollBallot struct {
	VoterEmail string                              `json:"voterEmail"`
	VoterName  string                              `json:"voterName"`
	Choices    map[string]domain.PollVoteChoice    `json:"choices"` // optionID → choice
}

// TimeProposalInput is the propose-new-time payload.
type TimeProposalInput struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Note  string    `json:"note,omitempty"`
}

// FreeBusyRequest asks for guest busy intervals (Find-a-Time grid).
type FreeBusyRequest struct {
	Emails []string  `json:"emails"`
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
}

// SchedulingService covers booking links, public booking, meeting polls,
// propose-new-time, and guest free/busy.
type SchedulingService interface {
	// Owner surface (authenticated).
	CreateLink(ctx context.Context, userID string, in BookingLinkInput) (domain.BookingLink, error)
	UpdateLink(ctx context.Context, userID, linkID string, in BookingLinkInput) (domain.BookingLink, error)
	ListLinks(ctx context.Context, userID string) ([]domain.BookingLink, error)
	DeleteLink(ctx context.Context, userID, linkID string) error
	ListBookings(ctx context.Context, userID string) ([]domain.Booking, error)
	CancelBooking(ctx context.Context, userID, bookingID string) error

	// Public surface (unauthenticated, rate limited).
	PublicPage(ctx context.Context, slug string) (PublicBookingPage, error)
	// PublicSlots returns bookable start times between from and to.
	PublicSlots(ctx context.Context, slug string, from, to time.Time) ([]domain.AvailabilitySlot, error)
	// Book runs the hold → provider free/busy re-check → event create →
	// confirm → email pipeline. domain.ErrConflict when the slot is taken.
	Book(ctx context.Context, slug string, req BookingRequest) (domain.Booking, error)

	// Meeting polls.
	CreatePoll(ctx context.Context, userID string, in PollInput) (domain.MeetingPoll, error)
	ListPolls(ctx context.Context, userID string) ([]domain.MeetingPoll, error)
	// ConfirmPoll picks the winner, creates the event (write-through), and
	// emails every yes/if_needed voter an invitation-style notice.
	ConfirmPoll(ctx context.Context, userID, pollID, optionID string) (domain.MeetingPoll, error)
	DeletePoll(ctx context.Context, userID, pollID string) error
	PublicPollByToken(ctx context.Context, token string) (PublicPoll, error)
	VotePoll(ctx context.Context, token string, ballot PollBallot) (PublicPoll, error)

	// Propose-new-time (authenticated invitee → organizer accept).
	ProposeTime(ctx context.Context, userID, eventID string, in TimeProposalInput) (domain.TimeProposal, error)
	ListProposals(ctx context.Context, userID, eventID string) ([]domain.TimeProposal, error)
	// AcceptProposal patches the event to the proposed time via provider
	// write-through and supersedes sibling proposals.
	AcceptProposal(ctx context.Context, userID, eventID, proposalID string) (domain.Event, error)
	DeclineProposal(ctx context.Context, userID, eventID, proposalID string) error

	// GuestFreeBusy powers the Find-a-Time grid via provider APIs.
	GuestFreeBusy(ctx context.Context, userID string, req FreeBusyRequest) (map[string][]domain.BusyInterval, error)

	// ExpireHolds is called by cmd/worker; cancels overdue holds.
	ExpireHolds(ctx context.Context) error
}

// SettingsService reads/writes per-user scheduling settings.
type SettingsService interface {
	Get(ctx context.Context, userID string) (domain.UserSettings, error)
	Update(ctx context.Context, userID string, s domain.UserSettings) (domain.UserSettings, error)
}
```

Also extend `CalendarServiceDeps`/`CalendarService.Availability` to consult `UserSettingsRepo` (working-hours constraint — implemented in Task 4).

**Steps:**
- [ ] Add the interfaces above to `port/driven.go` / `port/driving.go`; `cd backend && go build ./...` — expect compile failures in `service/fakes_test.go` (fakeCalendarProvider missing `FreeBusy`) and any `var _ port.X` assertions
- [ ] Extend `service/fakes_test.go`: `fakeCalendarProvider.FreeBusy` (configurable per-email busy map + error injection), plus in-memory `fakeBookingLinkRepo`, `fakeBookingRepo` (CreateHold enforces overlap→`domain.ErrConflict` in memory, mirroring the exclusion constraint), `fakePollRepo`, `fakeProposalRepo`, `fakeUserSettingsRepo` following the existing fake style
- [ ] `cd backend && go test ./... && golangci-lint run ./...` — everything compiles, existing suites green
- [ ] Commit: `feat(backend): scheduling ports + provider FreeBusy method + test fakes`

---

### Task 3: Postgres repositories (incl. exclusion-constraint conflict mapping)

**Files:**
- `backend/internal/adapter/out/postgres/scheduling.go` (new — bookingLinkRepo, bookingRepo, pollRepo, timeProposalRepo, userSettingsRepo)
- `backend/internal/adapter/out/postgres/store.go` (extend accessors + assertions)
- `backend/internal/adapter/out/postgres/scheduling_test.go` (new, testcontainers)

**Interfaces:** repos implement the Task-2 ports on the `struct{ *Store }` embedding pattern. The conflict mapping is the load-bearing piece:

```go
// isExclusionViolation reports whether err is the bookings_no_overlap
// exclusion constraint firing (SQLSTATE 23P01) or a unique violation (23505,
// e.g. booking_links_slug_idx / meeting_polls token).
func isConflictSQLState(err error) bool {
	var pgErr *pgconn.PgError // pgx is already a direct dependency
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23P01" || pgErr.Code == "23505"
	}
	return false
}

func (r bookingRepo) CreateHold(ctx context.Context, b domain.Booking) (domain.Booking, error) {
	_, err := r.q(ctx).ExecContext(ctx, `
		INSERT INTO bookings (id, link_id, status, start_at, end_at, invitee_name,
			invitee_email, invitee_tz, note, hold_expires_at)
		VALUES ($1, $2, 'hold', $3, $4, $5, $6, $7, $8, $9)`,
		b.ID, b.LinkID, b.Start, b.End, b.InviteeName,
		b.InviteeEmail, b.InviteeTZ, b.Note, b.HoldExpiresAt)
	if err != nil {
		if isConflictSQLState(err) {
			return domain.Booking{}, fmt.Errorf("%w: slot is no longer available", domain.ErrConflict)
		}
		return domain.Booking{}, fmt.Errorf("postgres: create hold: %w", err)
	}
	b.Status = domain.BookingHold
	return b, nil
}
```

`Confirm` uses a guarded UPDATE (`... SET status='confirmed', event_id=$2, hold_expires_at=NULL WHERE id=$1 AND status='hold'`) returning `domain.ErrConflict` when zero rows matched (hold already expired/cancelled). `ExpireHolds` is one UPDATE with `RETURNING`-free `RowsAffected`.

**Steps:**
- [ ] Write failing testcontainers tests in `scheduling_test.go`: slug CRUD + case-insensitive uniqueness (`Create("Foo") after create("foo")` → ErrConflict via lower index), `TestBookingHoldOverlapConflict` (two overlapping CreateHold on one link → second gets `domain.ErrConflict`; non-overlapping succeeds; overlap with a *cancelled* booking succeeds), `TestBookingHoldConcurrent` (10 goroutines CreateHold the same slot → exactly 1 success, 9 ErrConflict), `TestConfirmExpiredHoldConflict`, `TestExpireHolds`, poll vote upsert-replaces-ballot, settings Get-returns-default: `cd backend && go test ./internal/adapter/out/postgres/ -run TestScheduling -v` (Docker required) — confirm failures
- [ ] Implement `scheduling.go` repos; wire `Store` accessors (`func (s *Store) BookingLinks() port.BookingLinkRepo` etc.) + `var _` assertions
- [ ] `cd backend && go test ./internal/adapter/out/postgres/ -v && go test ./... && golangci-lint run ./...`
- [ ] Commit: `feat(backend): postgres scheduling repos with exclusion-constraint conflict mapping`

---

### Task 4: Slot computation + working-hours availability

**Files:**
- `backend/internal/service/scheduling.go` (new — service struct, deps, link CRUD, `PublicPage`, `PublicSlots`, pure `computeSlots`)
- `backend/internal/service/scheduling_test.go` (new)
- `backend/internal/service/calendar.go` + `calendar_test.go` (extend `Availability` with working hours)
- `backend/internal/service/service.go` (wire construction)

**Interfaces:**

```go
// SchedulingServiceDeps wires a SchedulingService.
type SchedulingServiceDeps struct {
	Subscriptions     port.SubscriptionRepo
	Users             port.UserRepo
	Accounts          port.AccountRepo
	Calendars         port.CalendarRepo
	Events            port.EventRepo
	Links             port.BookingLinkRepo
	Bookings          port.BookingRepo
	Polls             port.PollRepo
	Proposals         port.TimeProposalRepo
	Settings          port.UserSettingsRepo
	Tx                port.TxRunner
	CalendarProviders map[domain.Provider]port.CalendarProvider
	MailProviders     map[domain.Provider]port.MailProvider
	OAuth             map[domain.Provider]port.OAuthGateway
	Clock             port.Clock
	SelfHosted        bool
	// PublicWebURL builds booking/poll URLs in emails (config.PublicWebURL).
	PublicWebURL string
}

func NewSchedulingService(d SchedulingServiceDeps) *SchedulingService

// computeSlots is the pure slot engine, unit-tested exhaustively:
// discretize link windows (link TZ, DST-correct via time.LoadLocation) to
// duration-sized starts stepping every duration; drop slots violating
// minNotice/maxAdvance; subtract owner busy events padded by buffers;
// subtract active bookings; enforce dailyLimit (confirmed count per link-TZ
// day); optionally intersect with working hours (their own TZ).
func computeSlots(link domain.BookingLink, settings domain.UserSettings,
	busy []domain.BusyInterval, active []domain.Booking,
	from, to, now time.Time) []domain.AvailabilitySlot
```

`CalendarService.Availability` change: `CalendarServiceDeps` gains `Settings port.UserSettingsRepo`; after building free gaps it intersects them with the user's working hours (in `settings.TimeZone`) when `len(settings.WorkingHours) > 0`. Extract the shared window-intersection helper into `service/scheduling.go` (`intersectWindows(slots []domain.AvailabilitySlot, windows []domain.AvailabilityWindow, tz *time.Location) []domain.AvailabilitySlot`).

**Steps:**
- [ ] Write failing table-driven tests for `computeSlots` in `scheduling_test.go`: window discretization across days, DST spring-forward day in `America/New_York` (a 09:00-17:00 window on the transition day yields correct UTC instants), buffers padding a busy event, dailyLimit reached hides that day, minNotice hides near slots, active hold blocks its slot, working-hours intersection in a *different* TZ than the link TZ: `cd backend && go test ./internal/service/ -run TestComputeSlots -v`
- [ ] Write failing tests for `CreateLink`/`UpdateLink` (slug validation, calendar must be owned+writable, ErrConflict propagation), `PublicPage` (inactive link → ErrNotFound; owner name from UserRepo), `PublicSlots` (range clamped to maxAdvanceDays; ≤ 31-day query window enforced): `go test ./internal/service/ -run TestSchedulingLinks -v`
- [ ] Write failing test `TestAvailabilityRespectsWorkingHours` in `calendar_test.go`
- [ ] Implement `service/scheduling.go` (CRUD + slots) and the `Availability` working-hours intersection; all new tests green
- [ ] `cd backend && go test ./... && golangci-lint run ./...`
- [ ] Commit: `feat(backend): booking-link CRUD + slot engine + working-hours availability`

---

### Task 5: Booking pipeline — hold, provider re-check, confirm, email

**Files:**
- `backend/internal/service/scheduling.go` (extend — `Book`, `ExpireHolds`, `CancelBooking`, `ListBookings`, email builders)
- `backend/internal/service/scheduling_test.go` (extend)
- `backend/cmd/worker/main.go` (call `ExpireHolds` in the poll loop)

**Interfaces:** the core pipeline, written out (real code — adapt names to compile):

```go
const holdTTL = 5 * time.Minute

// Book implements the double-booking-safe pipeline:
//  1. hold  — INSERT status='hold'; the DB exclusion constraint serializes
//     concurrent competitors (loser → domain.ErrConflict).
//  2. re-check — live provider free/busy for the owner over [start,end);
//     catches events created outside Calendium since the last sync.
//  3. event — provider write-through CreateEvent on the link's calendar
//     (owner + invitee as attendees, conferencing per link setting).
//  4. confirm — promote the hold (guarded UPDATE) inside a tx with the
//     event upsert.
//  5. email — confirmation via the owner's MailProvider; failure is logged,
//     never rolls back the booking.
// Any failure in 2-4 cancels the hold before returning.
func (s *SchedulingService) Book(ctx context.Context, slug string, req BookingRequest) (domain.Booking, error) {
	link, err := s.links.GetBySlug(ctx, slug)
	if err != nil || !link.Active {
		return domain.Booking{}, domain.ErrNotFound
	}
	if err := validateBookingRequest(req); err != nil { // email shape, name, TZ loadable
		return domain.Booking{}, err
	}
	end := req.Start.Add(time.Duration(link.DurationMinutes) * time.Minute)

	// The requested start must be one of the currently offered slots
	// (window/buffer/limit rules re-evaluated server-side, never trusted
	// from the client).
	offered, err := s.PublicSlots(ctx, slug, req.Start, end)
	if err != nil {
		return domain.Booking{}, err
	}
	if !containsStart(offered, req.Start) {
		return domain.Booking{}, fmt.Errorf("%w: slot is no longer available", domain.ErrConflict)
	}

	now := s.clock.Now()
	expires := now.Add(holdTTL)
	hold, err := s.bookings.CreateHold(ctx, domain.Booking{
		ID: newID(), LinkID: link.ID, Start: req.Start, End: end,
		InviteeName: req.InviteeName, InviteeEmail: strings.ToLower(req.InviteeEmail),
		InviteeTZ: req.InviteeTZ, Note: optional(req.Note), HoldExpiresAt: &expires,
	})
	if err != nil {
		return domain.Booking{}, err // ErrConflict → 409 upstream
	}

	cancelHold := func() { _ = s.bookings.Cancel(context.WithoutCancel(ctx), hold.ID) }

	// Live re-check against the provider (step 2).
	cal, acct, err := s.ownedCalendarForLink(ctx, link)
	if err != nil {
		cancelHold()
		return domain.Booking{}, err
	}
	if provider, ok := s.cal[acct.Provider]; ok {
		token, err := s.tokens.accessToken(ctx, acct)
		if err != nil {
			cancelHold()
			return domain.Booking{}, err
		}
		busy, err := provider.FreeBusy(ctx, token, []string{acct.Email}, hold.Start, hold.End)
		if err != nil {
			cancelHold()
			return domain.Booking{}, fmt.Errorf("provider free/busy check failed: %w", err)
		}
		if overlapsAny(busy[strings.ToLower(acct.Email)], hold.Start, hold.End) {
			cancelHold()
			return domain.Booking{}, fmt.Errorf("%w: slot was just taken on the owner's calendar", domain.ErrConflict)
		}
	}

	// Provider event (step 3) — reuses the CalendarService write-through shape.
	ev, err := s.createBookingEvent(ctx, link, cal, acct, hold)
	if err != nil {
		cancelHold()
		return domain.Booking{}, err
	}

	// Confirm hold + persist event atomically (step 4).
	err = s.tx.RunInTx(ctx, func(ctx context.Context) error {
		if _, err := s.events.Upsert(ctx, ev); err != nil {
			return err
		}
		return s.bookings.Confirm(ctx, hold.ID, ev.ID)
	})
	if err != nil {
		cancelHold()
		return domain.Booking{}, err
	}
	hold.Status, hold.EventID, hold.HoldExpiresAt = domain.BookingConfirmed, &ev.ID, nil

	// Confirmation email (step 5) — best-effort.
	if err := s.sendBookingConfirmation(ctx, link, acct, hold, ev); err != nil {
		s.logger.Warn("booking confirmation email failed", "booking", hold.ID, "error", err)
	}
	return hold, nil
}
```

`sendBookingConfirmation` builds a `port.OutgoingMessage{From: acct.Email, To: invitee, Cc: owner}` whose body renders the slot in **the invitee's time zone** (`time.LoadLocation(hold.InviteeTZ)`) with the owner's zone in parentheses, plus a cancellation note; body building is a pure function `buildConfirmationEmail(link, booking, ev, ownerName) (subject, html, text string)` with its own test. `ExpireHolds` calls `s.bookings.ExpireHolds(ctx, s.clock.Now())` and logs the count. Worker: add `schedSvc.ExpireHolds(ctx)` alongside `ProcessDueWork` in the existing loop.

**Steps:**
- [ ] Write failing tests: `TestBookHappyPath` (hold created → fake provider FreeBusy empty → event created with both attendees → booking confirmed with eventID → fake MailProvider captured To=invitee/Cc=owner and body contains invitee-TZ rendering), `TestBookSlotTakenByExclusion` (fakeBookingRepo returns ErrConflict → 0 provider calls), `TestBookProviderBusyAtRecheck` (fake FreeBusy busy → hold cancelled, ErrConflict), `TestBookEventCreateFails` (hold cancelled), `TestBookEmailFailureStillConfirms`, `TestBookRejectsUnofferedStart` (start not in computed slots → ErrConflict), `TestExpireHoldsSweep`: `cd backend && go test ./internal/service/ -run TestBook -v`
- [ ] Implement `Book`, `sendBookingConfirmation`, `buildConfirmationEmail`, `ExpireHolds`, `ListBookings`, `CancelBooking` (owner cancel → provider DeleteEvent + status cancelled + notice email)
- [ ] Wire `ExpireHolds` into `cmd/worker/main.go`; `cd backend && go build ./cmd/worker`
- [ ] `cd backend && go test ./... && golangci-lint run ./...`
- [ ] Commit: `feat(backend): public booking pipeline with transactional slot hold + provider re-check + confirmation email`

---

### Task 6: Provider FreeBusy adapters (Google + Graph)

**Files:**
- `backend/internal/adapter/out/googleapi/calendar.go` + `calendar_test.go` (extend)
- `backend/internal/adapter/out/msgraph/calendar.go` + `calendar_test.go` (extend)

**Interfaces:**

```go
// googleapi: POST {base}/calendars/v3/freeBusy with
// {"timeMin":..,"timeMax":..,"items":[{"id":email},...]}; response
// calendars.<email>.busy[].{start,end} → map[email][]domain.BusyInterval.
// Errors array per calendar (notFound) → email omitted from the result.
func (c *CalendarAPI) FreeBusy(ctx context.Context, accessToken string, emails []string, from, to time.Time) (map[string][]domain.BusyInterval, error)

// msgraph: POST {base}/me/calendar/getSchedule with
// {"schedules":[emails...],"startTime":{...},"endTime":{...},
//  "availabilityViewInterval":30}; response value[].scheduleItems where
// status != "free" → busy intervals. scheduleId casing normalized to lower.
func (c *CalendarAPI) FreeBusy(ctx context.Context, accessToken string, emails []string, from, to time.Time) (map[string][]domain.BusyInterval, error)
```

Both follow the adapters' existing `httptest`-tested request/response style (see `googleapi/calendar_test.go`), chunk requests at 20 emails, and return `map[string][]domain.BusyInterval` keyed by lowercased email.

**Steps:**
- [ ] Write failing `httptest` tests in both adapters: request body shape (timeMin/timeMax RFC3339, items list), busy parsing, unknown-attendee omission, non-2xx → error, Graph `scheduleItems` status mapping (`busy`/`tentative`/`oof` → busy, `free` skipped): `cd backend && go test ./internal/adapter/out/googleapi/ ./internal/adapter/out/msgraph/ -run TestFreeBusy -v`
- [ ] Implement both `FreeBusy` methods
- [ ] `cd backend && go test ./... && golangci-lint run ./...`
- [ ] Commit: `feat(backend): Google freeBusy + Graph getSchedule provider adapters`

---

### Task 7: Public HTTP surface — rate limiter + routes

**Files:**
- `backend/internal/adapter/in/httpapi/ratelimit.go` (new)
- `backend/internal/adapter/in/httpapi/ratelimit_test.go` (new)
- `backend/internal/adapter/in/httpapi/scheduling.go` (new — public handlers)
- `backend/internal/adapter/in/httpapi/httpapi.go` (register routes; `Deps` gains `Scheduling port.SchedulingService`, `Settings port.SettingsService`)
- `backend/internal/adapter/in/httpapi/scheduling_handlers_test.go` (new)

**Interfaces:** the rate limiter is real code (stdlib token bucket, per-IP, lazily pruned):

```go
package httpapi

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// rateLimiter is an in-memory per-key token bucket: capacity `burst`,
// refilled at `perMin` tokens/minute. Zero-dependency abuse protection for
// the public (unauthenticated) endpoints; per-process by design — a
// horizontal deployment multiplies the effective limit by replica count,
// which is acceptable for M2.4.
type rateLimiter struct {
	mu      sync.Mutex
	perMin  float64
	burst   float64
	now     func() time.Time
	buckets map[string]*bucket
	lastGC  time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(perMin, burst int, now func() time.Time) *rateLimiter {
	return &rateLimiter{
		perMin: float64(perMin), burst: float64(burst),
		now: now, buckets: make(map[string]*bucket),
	}
}

// allow consumes one token for key, reporting whether the call may proceed.
func (l *rateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.now()
	// Opportunistic GC: drop buckets idle > 10 minutes, at most once a minute.
	if t.Sub(l.lastGC) > time.Minute {
		for k, b := range l.buckets {
			if t.Sub(b.last) > 10*time.Minute {
				delete(l.buckets, k)
			}
		}
		l.lastGC = t
	}
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: t}
		l.buckets[key] = b
	}
	b.tokens = min(l.burst, b.tokens+t.Sub(b.last).Minutes()*l.perMin)
	b.last = t
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// clientIP extracts the caller address. RemoteAddr only — proxy headers are
// spoofable and this API terminates TLS itself in the reference deployment.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// rateLimited wraps a public handler: 429 {"error":{"code":"rate_limited"}}
// with Retry-After: 60 when the caller's bucket is empty.
func (s *server) rateLimited(l *rateLimiter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(clientIP(r)) {
			w.Header().Set("Retry-After", "60")
			writeJSON(w, http.StatusTooManyRequests, errorBody{Error: errorDetail{
				Code: "rate_limited", Message: "too many requests, slow down",
			}})
			return
		}
		next(w, r)
	}
}
```

Route registration in `New` (unauthenticated section, beside `/v1/instance`) — two buckets, reads generous, writes tight:

```go
publicRead := newRateLimiter(60, 30, time.Now)  // GETs: 60/min, burst 30
publicWrite := newRateLimiter(5, 5, time.Now)   // POSTs: 5/min, burst 5

mux.HandleFunc("GET /v1/public/booking/{slug}", s.rateLimited(publicRead, s.handlePublicBookingPage))
mux.HandleFunc("GET /v1/public/booking/{slug}/slots", s.rateLimited(publicRead, s.handlePublicSlots))
mux.HandleFunc("POST /v1/public/booking/{slug}/bookings", s.rateLimited(publicWrite, s.handlePublicBook))
mux.HandleFunc("GET /v1/public/polls/{token}", s.rateLimited(publicRead, s.handlePublicPoll))
mux.HandleFunc("POST /v1/public/polls/{token}/votes", s.rateLimited(publicWrite, s.handlePublicPollVote))
```

Public POST bodies pass through `http.MaxBytesReader(w, r.Body, 16<<10)` before decode. `handlePublicSlots` takes `from`/`to` via the existing `timeRange` helper and caps the span at 31 days. All slug/token misses return the uniform 404 error body.

**Steps:**
- [ ] Write failing `ratelimit_test.go`: burst then deny, refill after simulated time (inject `now`), key isolation (two IPs independent), GC prunes idle buckets, `clientIP` with/without port; and handler tests in `scheduling_handlers_test.go` using the existing harness: public page 200 with **no** Authorization header, unknown slug → 404, 429 after burst exhaustion with `Retry-After`, slots happy path, book 201/409/422 mapping (`domain.ErrConflict` → 409 via existing `writeError`), body-size 413: `cd backend && go test ./internal/adapter/in/httpapi/ -run 'TestRateLimit|TestPublic' -v`
- [ ] Implement `ratelimit.go` + public handlers in `scheduling.go`; extend `Deps` and route table
- [ ] Verify `middleware_test.go` CORS still passes (public routes sit inside the CORS wrapper — booking pages are served from the web origin)
- [ ] `cd backend && go test ./... && golangci-lint run ./...`
- [ ] Commit: `feat(backend): public booking/poll endpoints with per-IP token-bucket rate limiting`

---

### Task 8: Authenticated HTTP surface + composition root

**Files:**
- `backend/internal/adapter/in/httpapi/scheduling.go` (extend — owner handlers)
- `backend/internal/adapter/in/httpapi/httpapi.go` (authed routes)
- `backend/internal/adapter/in/httpapi/scheduling_handlers_test.go` (extend)
- `backend/internal/service/settings.go` (new — trivial `SettingsService` impl with TZ validation via `time.LoadLocation`)
- `backend/internal/service/misc_services_test.go` (extend)
- `backend/cmd/api/main.go` (construct + inject `SchedulingService`/`SettingsService`)

**Interfaces:** routes (all via `authed(...)`):

```
GET    /v1/booking-links                 → ListLinks
POST   /v1/booking-links                 → CreateLink        (BookingLinkInput)
PUT    /v1/booking-links/{id}            → UpdateLink
DELETE /v1/booking-links/{id}            → DeleteLink
GET    /v1/bookings                      → ListBookings
POST   /v1/bookings/{id}/cancel          → CancelBooking
GET    /v1/polls                         → ListPolls
POST   /v1/polls                         → CreatePoll        (PollInput)
POST   /v1/polls/{id}/confirm            → ConfirmPoll       ({"optionId": "..."})
DELETE /v1/polls/{id}                    → DeletePoll
POST   /v1/events/{id}/propose-time      → ProposeTime       (TimeProposalInput)
GET    /v1/events/{id}/proposals         → ListProposals
POST   /v1/events/{id}/proposals/{pid}/accept  → AcceptProposal
POST   /v1/events/{id}/proposals/{pid}/decline → DeclineProposal
POST   /v1/freebusy                      → GuestFreeBusy     (FreeBusyRequest, ≤ 20 emails, ≤ 14-day span)
GET    /v1/settings                      → SettingsService.Get
PUT    /v1/settings                      → SettingsService.Update
```

**Steps:**
- [ ] Write failing handler tests (harness): link CRUD auth-scoped (user B cannot see/patch user A's link → 404), slug conflict → 409, freebusy validation (0 or >20 emails → 422), settings round-trip + invalid TZ → 422, proposals routes wired: `cd backend && go test ./internal/adapter/in/httpapi/ -run TestScheduling -v`
- [ ] Write failing `SettingsService` tests (default on missing row; TZ + window validation)
- [ ] Implement handlers, `service/settings.go`, route registration; wire both services in `cmd/api/main.go` (repos from `Store`, providers/OAuth/mail maps and `Clock` as `CalendarService` does; `PublicWebURL` from config)
- [ ] `cd backend && go build ./cmd/api && go test ./... && golangci-lint run ./...`
- [ ] Commit: `feat(backend): owner scheduling endpoints + settings service + composition wiring`

---

### Task 9: Meeting polls service

**Files:**
- `backend/internal/service/scheduling.go` (extend — `CreatePoll`, `ListPolls`, `PublicPollByToken`, `VotePoll`, `ConfirmPoll`, `DeletePoll`)
- `backend/internal/service/scheduling_test.go` (extend)

**Interfaces:** already declared in Task 2. Behaviors to encode:
- `CreatePoll`: 2–10 options required; option IDs + 32-hex-char token from `crypto/rand`; calendar must be owned + writable; every option end = start + duration.
- `VotePoll`: poll must be `open`; ballot must address only known option IDs; voter email validated; `UpsertVotes` replaces the ballot (re-vote allowed); returns the refreshed `PublicPoll` with tallies (voter emails are **never** exposed publicly — tallies only).
- `ConfirmPoll`: organizer-only; poll `open` → `confirmed`; creates the event via provider write-through with all yes/if_needed voters as attendees; stores `winnerOptionId` + `eventID`; sends a "time confirmed" email to every voter through the organizer's account (`buildPollConfirmationEmail`, pure). Idempotent: confirming an already-confirmed poll with the same option returns it unchanged, different option → `domain.ErrConflict`.

**Steps:**
- [ ] Write failing tests: create validation (1 option → 422-shaped ErrValidation, 11 options → same), token uniqueness (fake repo collision retried), public poll hides voter emails, re-vote replaces, vote on confirmed poll → ErrConflict, confirm creates event with voter attendees + emails all voters + is idempotent: `cd backend && go test ./internal/service/ -run TestPoll -v`
- [ ] Implement; `cd backend && go test ./... && golangci-lint run ./...`
- [ ] Commit: `feat(backend): meeting polls — create, public vote, confirm winner to event`

---

### Task 10: Propose-new-time flow

**Files:**
- `backend/internal/service/scheduling.go` (extend — `ProposeTime`, `ListProposals`, `AcceptProposal`, `DeclineProposal`)
- `backend/internal/service/scheduling_test.go` (extend)

**Interfaces:** behaviors:
- `ProposeTime(userID, eventID, in)`: the caller must own a connected account whose email is an **attendee** (non-organizer) of the mirrored event; proposal stored `pending`; a notification email goes to the organizer's address through the proposer's account (subject "New time proposed: <title>", body shows old → new in both parties' zones when known).
- `ListProposals(userID, eventID)`: caller must be the event owner (organizer side, via the `ownedEvent` pattern shared with `CalendarService` — extract `ownedEvent` into a shared unexported helper or duplicate the 3-line lookup with the scheduling service's own repos).
- `AcceptProposal(userID, eventID, proposalID)`: organizer-only; patches the event via the calendar provider (`UpdateEvent` write-through with `EventPatch{Start,End}`), marks the proposal `accepted`, marks all sibling `pending` proposals `superseded`, upserts the mirror. The provider's own attendee notifications carry the change to guests (Google/Graph send updates) — no extra email.
- `DeclineProposal`: organizer-only; marks `declined`.

**Steps:**
- [ ] Write failing tests: non-attendee proposer → ErrNotFound, organizer cannot counter-propose own event → ErrValidation, accept patches provider event (fake CalendarProvider captures `EventPatch{Start,End}`) + supersedes siblings, decline leaves event untouched, accept on non-pending → ErrConflict: `cd backend && go test ./internal/service/ -run TestProposal -v`
- [ ] Implement; `cd backend && go test ./... && golangci-lint run ./...`
- [ ] Commit: `feat(backend): propose-new-time flow with one-click organizer accept`

---

### Task 11: Shared contract — types, ApiClient, public fetchers

**Files:**
- `packages/shared/src/types.ts` (extend)
- `packages/shared/src/client.ts` (extend)
- `packages/shared/src/client.test.ts` (extend)

**Interfaces:** (exact TS)

```ts
// types.ts additions (mirror backend/internal/domain/scheduling.go)
export interface AvailabilityWindow { weekday: number; start: string; end: string }
export interface BookingLink {
  id: string; slug: string; title: string; description: string | null;
  calendarId: string; durationMinutes: number; timeZone: string;
  windows: AvailabilityWindow[]; bufferBeforeMin: number; bufferAfterMin: number;
  dailyLimit: number; minNoticeMin: number; maxAdvanceDays: number;
  respectWorkingHours: boolean; addConferencing: boolean; active: boolean;
  createdAt: string;
}
export type BookingLinkInput = Omit<BookingLink, 'id' | 'createdAt' | 'description'> & { description?: string };
export type BookingStatus = 'hold' | 'confirmed' | 'cancelled';
export interface Booking {
  id: string; linkId: string; status: BookingStatus; start: string; end: string;
  inviteeName: string; inviteeEmail: string; inviteeTimeZone: string;
  note: string | null; eventId: string | null; createdAt: string;
}
export interface PublicBookingPage {
  slug: string; title: string; description: string | null;
  ownerName: string; durationMinutes: number; timeZone: string;
}
export interface BookingRequest {
  start: string; inviteeName: string; inviteeEmail: string;
  inviteeTimeZone: string; note?: string;
}
export type PollStatus = 'open' | 'confirmed' | 'cancelled';
export type PollVoteChoice = 'yes' | 'no' | 'if_needed';
export interface PollOption { id: string; start: string; end: string }
export interface PollTally { yes: number; no: number; ifNeeded: number }
export interface MeetingPoll {
  id: string; token: string; title: string; description: string | null;
  calendarId: string; durationMinutes: number; options: PollOption[];
  status: PollStatus; winnerOptionId: string | null; eventId: string | null;
  createdAt: string;
}
export interface PollInput {
  title: string; description?: string; calendarId: string;
  durationMinutes: number; options: Array<{ start: string; end: string }>;
}
export interface PublicPoll {
  token: string; title: string; description: string | null; organizerName: string;
  durationMinutes: number; status: PollStatus; options: PollOption[];
  tallies: Record<string, PollTally>; winnerOptionId: string | null;
}
export interface PollBallot {
  voterEmail: string; voterName: string;
  choices: Record<string, PollVoteChoice>;
}
export type ProposalStatus = 'pending' | 'accepted' | 'declined' | 'superseded';
export interface TimeProposal {
  id: string; eventId: string; proposerEmail: string; proposerName: string;
  start: string; end: string; note: string | null; status: ProposalStatus;
  createdAt: string;
}
export interface TimeProposalInput { start: string; end: string; note?: string }
export interface BusyInterval { start: string; end: string }
export interface UserSettings {
  timeZone: string; workingHours: AvailabilityWindow[]; workingLocation: string;
}
```

```ts
// client.ts — ApiClient methods (authed)
listBookingLinks(): Promise<BookingLink[]>
createBookingLink(input: BookingLinkInput): Promise<BookingLink>
updateBookingLink(id: string, input: BookingLinkInput): Promise<BookingLink>
deleteBookingLink(id: string): Promise<void>
listBookings(): Promise<Booking[]>
cancelBooking(id: string): Promise<void>
listPolls(): Promise<MeetingPoll[]>
createPoll(input: PollInput): Promise<MeetingPoll>
confirmPoll(id: string, optionId: string): Promise<MeetingPoll>
deletePoll(id: string): Promise<void>
proposeTime(eventId: string, input: TimeProposalInput): Promise<TimeProposal>
listProposals(eventId: string): Promise<TimeProposal[]>
acceptProposal(eventId: string, proposalId: string): Promise<Event>
declineProposal(eventId: string, proposalId: string): Promise<void>
getFreeBusy(emails: string[], from: string, to: string): Promise<Record<string, BusyInterval[]>>
getSettings(): Promise<UserSettings>
updateSettings(s: UserSettings): Promise<UserSettings>

// client.ts — standalone unauthenticated fetchers (fetchInstance pattern)
export function fetchPublicBookingPage(baseUrl: string, slug: string, fetchImpl?: typeof fetch): Promise<PublicBookingPage>
export function fetchPublicSlots(baseUrl: string, slug: string, from: string, to: string, fetchImpl?: typeof fetch): Promise<AvailabilitySlot[]>
export function createPublicBooking(baseUrl: string, slug: string, req: BookingRequest, fetchImpl?: typeof fetch): Promise<Booking>
export function fetchPublicPoll(baseUrl: string, token: string, fetchImpl?: typeof fetch): Promise<PublicPoll>
export function votePublicPoll(baseUrl: string, token: string, ballot: PollBallot, fetchImpl?: typeof fetch): Promise<PublicPoll>
```

**Steps:**
- [ ] Write failing client tests (mock-fetch pattern from `client.test.ts`): URL/method/body per new method, no Authorization header on the five public fetchers, `ApiRequestError` mapping incl. 429 `rate_limited`: `bun run test:shared` — confirm failures
- [ ] Add types + methods; `bun run test:shared && bun run --cwd packages/shared typecheck && bun run lint:js`
- [ ] Commit: `feat(shared): scheduling types, ApiClient methods, public booking/poll fetchers`

---

### Task 12: Public booking page (web)

**Files:**
- `apps/web/app/(marketing)/book/[slug]/page.tsx` (new — server component shell + metadata)
- `apps/web/components/public/booking-page.tsx` (new — client component)
- `apps/web/components/public/booking-page.test.tsx` (new)
- `apps/web/lib/timezone.ts` (new — shared TZ utilities, used again in Tasks 14–16)
- `apps/web/lib/timezone.test.ts` (new)

**Interfaces:**

```ts
// lib/timezone.ts — Intl-only helpers, no new deps
export function formatInTZ(iso: string, timeZone: string, fmt: 'time' | 'weekday-date' | 'datetime'): string
export function tzOffsetLabel(timeZone: string, at?: Date): string        // "GMT-4"
export function browserTimeZone(): string                                  // Intl resolvedOptions fallback 'UTC'
export function listTimeZones(): string[]                                  // Intl.supportedValuesOf('timeZone') fallback list
export function tzAbbrev(timeZone: string, at?: Date): string              // "EDT"

// components/public/booking-page.tsx
export function PublicBookingPage({ slug }: { slug: string }): JSX.Element
```

Flow: `fetchPublicBookingPage` → header (owner name, title, duration, description) → week strip + slot list from `fetchPublicSlots` (7-day pages) rendered in a **visitor-TZ selector** defaulting to `browserTimeZone()` (this is the recipient-TZ preview) → slot click → name/email/note form → `createPublicBooking` → confirmation screen showing the booked time in the visitor's zone with the owner's zone beneath. 409 → toast "That slot was just taken" + slot list refetch; 429 → "Too many requests" state. API base URL from the existing `lib/env.ts` public API URL. No auth client, no `ApiClient` — this page must work signed out. Uses shadcn primitives + the marketing layout.

**Steps:**
- [ ] Write failing `timezone.test.ts` (formatInTZ across DST boundary, offset labels, fallbacks) and `booking-page.test.tsx` (mock the shared fetchers: renders slots in selected TZ, TZ switch re-renders labels without refetch, book happy path shows confirmation, 409 refetches, empty week shows "no times available"): `bun run test:web` — confirm failures
- [ ] Implement `lib/timezone.ts`, the client component, and the route (server component passes `params.slug`; `generateMetadata` fetches the public page for the title, `notFound()` on 404)
- [ ] `bun run test:web && bun run --cwd apps/web typecheck && bun run lint:js`
- [ ] Commit: `feat(web): public booking page with recipient-timezone preview`

---

### Task 13: Public poll page (web)

**Files:**
- `apps/web/app/(marketing)/poll/[token]/page.tsx` (new)
- `apps/web/components/public/poll-page.tsx` (new)
- `apps/web/components/public/poll-page.test.tsx` (new)

**Interfaces:**

```ts
export function PublicPollPage({ token }: { token: string }): JSX.Element
```

Flow: `fetchPublicPoll` → options rendered in visitor TZ (reuses `lib/timezone.ts` + the same TZ selector) with per-option tallies → voter enters name + email, picks yes/no/if-needed per option → `votePublicPoll` → thanks state with live tallies. Confirmed poll renders the winning time prominently and disables voting.

**Steps:**
- [ ] Write failing `poll-page.test.tsx`: options in visitor TZ, ballot submission body shape, re-vote allowed (fields prefilled from localStorage `calendium.poll.voter`), confirmed poll shows winner + disabled controls: `bun run test:web`
- [ ] Implement component + route
- [ ] `bun run test:web && bun run --cwd apps/web typecheck && bun run lint:js`
- [ ] Commit: `feat(web): public meeting-poll voting page`

---

### Task 14: In-app scheduling UI — booking links, bookings, polls, working hours

**Files:**
- `apps/web/lib/scheduling-data.ts` (new — wrappers with `DEMO_MODE` fallback)
- `apps/web/lib/scheduling-mock.ts` (new — in-memory mock, demo flag only)
- `apps/web/components/app/booking-links.tsx` (new — manager: list, create/edit dialog with windows editor, buffers, daily limit, copy-public-URL)
- `apps/web/components/app/meeting-polls.tsx` (new — poll list + create dialog picking candidate slots from availability + confirm winner)
- `apps/web/components/app/booking-links.test.tsx`, `meeting-polls.test.tsx` (new)
- `apps/web/app/(app)/settings/page.tsx` (extend — "Scheduling" section: working hours editor (per-weekday windows), time zone select, working location; renders `BookingLinks` + `MeetingPolls`)
- `apps/web/components/app/command-palette.tsx` (extend — "Create booking link", "New meeting poll" actions)

**Interfaces:**

```ts
// lib/scheduling-data.ts (calendar-data.ts pattern; every fn try/real-API,
// catch → DEMO_MODE ? mock : rethrow)
export function fetchBookingLinks(): Promise<BookingLink[]>
export function createBookingLinkApi(input: BookingLinkInput): Promise<BookingLink>
export function updateBookingLinkApi(id: string, input: BookingLinkInput): Promise<BookingLink>
export function deleteBookingLinkApi(id: string): Promise<void>
export function fetchBookings(): Promise<Booking[]>
export function cancelBookingApi(id: string): Promise<void>
export function fetchPolls(): Promise<MeetingPoll[]>
export function createPollApi(input: PollInput): Promise<MeetingPoll>
export function confirmPollApi(id: string, optionId: string): Promise<MeetingPoll>
export function fetchSettings(): Promise<UserSettings>
export function updateSettingsApi(s: UserSettings): Promise<UserSettings>
export function fetchFreeBusy(emails: string[], from: string, to: string): Promise<Record<string, BusyInterval[]>>
```

Public URL construction: `${window.location.origin}/book/${slug}` (web serves the public page; the API base differs). Slug field live-validates with the same regex as the backend and surfaces 409 as an inline "slug taken" error.

**Steps:**
- [ ] Write failing component tests: link list renders + copy URL writes clipboard, create dialog validates slug locally, windows editor adds/removes rows, poll create requires ≥2 options, confirm winner calls API and shows event badge, working-hours editor round-trips settings: `bun run test:web`
- [ ] Implement `scheduling-data.ts` + `scheduling-mock.ts` (mock data clearly marked, served only under `DEMO_MODE`), components, settings section, palette actions
- [ ] `bun run test:web && bun run --cwd apps/web typecheck && bun run lint:js`
- [ ] Commit: `feat(web): booking-link manager, meeting polls, working-hours settings`

---### Task 15: Find-a-Time grid + propose-new-time UI

**Files:**
- `apps/web/components/app/find-a-time.tsx` (new)
- `apps/web/components/app/find-a-time.test.tsx` (new)
- `apps/web/components/app/event-dialog.tsx` (extend — "Find a time" tab when ≥1 attendee; propose-new-time affordance + proposals list)
- `apps/web/components/app/event-dialog.test.tsx` (extend)

**Interfaces:**

```ts
// find-a-time.tsx — horizontal day grid: one row per attendee email plus
// "You"; busy intervals shaded from fetchFreeBusy; clicking a free column
// sets the event start/end via onPick.
export function FindATimeGrid(props: {
  attendeeEmails: string[];
  durationMinutes: number;
  initialDate: Date;
  onPick: (start: Date, end: Date) => void;
}): JSX.Element
```

Event dialog additions: (a) creating/editing an event with attendees shows the Find-a-Time tab (grid drives start/end); (b) viewing an event the user does **not** organize shows "Propose a new time" (start/end pickers + note → `proposeTime`); (c) viewing an event the user organizes shows pending proposals with one-click **Accept** (`acceptProposal`, then refetch) and Decline. Free/busy failures degrade gracefully: rows for unresolvable attendees render "availability unknown" (provider omission), the grid never blocks manual time entry.

**Steps:**
- [ ] Write failing tests: grid shades mocked busy intervals per attendee, pick callback returns duration-sized range, unknown attendee row labeled, organizer sees proposals + accept triggers API and closes, non-organizer sees propose form: `bun run test:web`
- [ ] Implement grid + dialog integration
- [ ] `bun run test:web && bun run --cwd apps/web typecheck && bun run lint:js`
- [ ] Commit: `feat(web): find-a-time guest availability grid + propose-new-time flow`

---

### Task 16: Availability text in recipient TZ + Time Travel overlay + e2e

**Files:**
- `apps/web/components/app/availability.tsx` (extend — recipient-TZ selector; `buildShareText(slots, ownerTZ, recipientTZ)`; booking-link footer line)
- `apps/web/components/app/availability.test.tsx` (new)
- `apps/web/components/app/time-travel.tsx` (new — overlay controller)
- `apps/web/components/app/time-travel.test.tsx` (new)
- `apps/web/app/(app)/calendar/page.tsx` (extend — render secondary TZ gutter when Time Travel active; palette + `shift+z` shortcut)
- `apps/web/lib/shortcuts.ts` (extend)
- `apps/web/e2e/scheduling.spec.ts` (new — Playwright, demo mode)

**Interfaces:**

```ts
// availability.tsx — signature change (old callers pass recipientTZ = ownerTZ)
function buildShareText(slots: AvailabilitySlot[], ownerTZ: string, recipientTZ: string): string
// Output when zones differ:
// "Here are a few times that work for me (all times Eastern Time — EDT):" +
// per-slot lines rendered in recipientTZ via formatInTZ; when a booking link
// is selected, appends "Or pick a time: https://<web>/book/<slug>".

// time-travel.tsx
export function TimeTravelPicker(props: {
  active: string | null;                 // IANA TZ or null
  onChange: (tz: string | null) => void; // persisted in localStorage 'calendium.timetravel'
}): JSX.Element
// Calendar grid: when active, a second hour gutter renders hour labels in the
// overlay TZ (computed with formatInTZ per grid hour — DST-correct), with the
// city name + tzAbbrev in the gutter header and an "exit" chip. Esc clears.
```

The availability dialog gains: a recipient-TZ `Select` (searchable, `listTimeZones()`, default = own TZ) so copied text is pre-converted for the recipient (drag-and-copy availability as text, Vimcal-style), and an optional booking-link picker appending the public URL.

**Steps:**
- [ ] Write failing tests: `buildShareText` converts slot lines to the recipient zone and labels the zone, appends booking URL when a link is chosen, same-zone output matches today's format; Time Travel picker filters city list, overlay gutter shows converted hour labels across a DST boundary, Esc/exit clears + localStorage persists: `bun run test:web`
- [ ] Implement availability extensions, `time-travel.tsx`, calendar-page gutter, `shift+z` shortcut + palette entry ("Time Travel: overlay a city's time zone")
- [ ] Add `e2e/scheduling.spec.ts` (demo mode): open availability dialog → switch recipient TZ → copied text contains converted times; open `/book/demo` route renders the public page shell (mock service worker or demo API); Time Travel toggles the second gutter: `bun run test:e2e` (CI job)
- [ ] Full gates: `bun run lint && bun run typecheck && bun run test && cd backend && go test ./...`
- [ ] Commit: `feat(web): recipient-TZ availability text, Time Travel overlay, scheduling e2e`

---

## Test strategy summary

| Layer | Harness | Critical cases |
| --- | --- | --- |
| domain | `go test ./internal/domain/` | slug + window validation |
| service | fakes (`fakes_test.go`) | slot engine w/ DST + buffers + limits; hold→re-check→confirm pipeline incl. every failure leg; poll lifecycle; proposal accept write-through |
| postgres | testcontainers | exclusion-constraint overlap (incl. 10-goroutine race), slug case-insensitive uniqueness, hold expiry sweep |
| httpapi | in-process harness | public routes bypass auth, rate-limit 429 + Retry-After, uniform 404s, 409 conflict mapping, body-size caps |
| provider adapters | `httptest` | freeBusy/getSchedule request+response shape, non-2xx |
| shared | vitest mock fetch | every new method's URL/verb/body; public fetchers send no auth |
| web | vitest + RTL | TZ conversion across DST, booking flow states (409/429/empty), grids, editors |
| e2e | Playwright (demo mode, CI) | availability copy in recipient TZ, public page shell, Time Travel gutter |

## Task order recap

Backend model → ports/fakes → repos → slot engine → booking pipeline → provider free/busy → public HTTP + rate limiting → authed HTTP + wiring → polls → proposals → shared contract → public booking page → public poll page → in-app managers/settings → Find-a-Time/propose UI → TZ features + e2e. Tasks 12–13 depend on 11; 14–16 depend on 11 and are mutually independent (parallelizable by subagents).
