# M2.2 — Calendar Core Parity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
> **Migration numbering:** the eight M2 phase plans were authored in parallel, so migration filenames here are provisional — at execution time use the next free number in `backend/migrations/` and update references in the affected task.

> Phase 2 of the M2 roadmap (`docs/superpowers/specs/2026-07-17-m2-roadmap-design.md`). Written at task granularity now; per the roadmap execution model, a **final code-level pass at execution time** refreshes exact line-level details for the UI tasks (the calendar page and event dialog will have drifted slightly by then). Backend interfaces, grammar, and math in this plan are execution-ready as written.

## Goal

Make the Calendium calendar best-in-class at its core: natural-language event creation with live preview, the full Fantastical view range (day/week/month/quarter/year + DayTicker), multi-account overlay with cross-account conflict blocking, conference-link detection with a Join button plus one-click Meet/Teams attach, multi-timezone week-grid columns, a fully keyboard-driven calendar unified with ⌘K, a calendar peek beside the inbox, saved event templates, and calendar sets. Everything works on live provider data (M1 sync is complete) and in demo mode.

## Architecture

- **Backend (Go, hexagonal, stdlib-only)** — two new persisted concepts, *event templates* and *calendar sets*, follow the existing snippet pattern exactly: domain structs in `internal/domain/calendar.go`, repos in `internal/port/driven.go` + `internal/adapter/out/postgres/`, service methods on the existing `CalendarService` (`internal/service/calendar.go`, entitlement-gated), REST routes in `internal/adapter/in/httpapi/`. One new migration (`0005`). No provider interaction — both are purely local user data.
- **Shared (`packages/shared`)** — new pure-TS calendar utilities usable by web, desktop, and mobile: conference-link detection (`conferencing.ts`) and busy-interval/conflict/free-slot math (`conflicts.ts`), plus `EventTemplate`/`CalendarSet` types and eight new `ApiClient` methods. Shared stays dependency-free (plain `Date` math only).
- **Web (`apps/web`)** — the quick-add parser is extended in place (`lib/quick-add.ts`); a new `lib/calendar-views.ts` owns view-range/navigation math; the 30KB calendar page is decomposed into `components/app/calendar/*` so each view is a testable component; the event dialog gains conflict warnings, conferencing attach, and template support; the command palette gains a calendar section; the mail layout gains a calendar peek panel.
- **Desktop/mobile** — consume the new shared utilities and API methods; desktop `CalendarView.tsx` and the mobile calendar tab get parity features at lighter depth (views, Join button, keyboard on desktop).
- **Data flow for conflict blocking** — the local event mirror already spans every connected account (`GET /v1/events` with no `calendarIds` returns all calendars of all accounts). Cross-account blocking is therefore a *client-side* computation over an unfiltered fetch — hidden calendars still block. No backend change needed; the server-side `Availability` already merges all calendars.

## Tech Stack

- Go 1.22+ stdlib only (net/http ServeMux patterns, database/sql, embed migrations); testcontainers Postgres for repo tests.
- TypeScript everywhere else: Next.js 15 + React 19 + TanStack Query + date-fns v4 + shadcn new-york (web); Vite + React (desktop frontend); Expo (mobile).
- Vitest (+ jsdom + Testing Library) for shared/web/desktop/mobile unit tests; Playwright (demo mode) for web e2e.
- `Intl.DateTimeFormat` for all timezone math — no new dependencies.

## Global Constraints

- **Mocks only behind demo flags.** All mock fallbacks live behind `DEMO_MODE` (`apps/web/lib/demo.ts`) exactly like `calendar-data.ts` does today. Outside demo mode, failures propagate to real error states. New features (templates, sets) must extend `calendar-mock.ts` so the demo Playwright suite can exercise them.
- **Backend stays stdlib-only.** No new Go dependencies. New endpoints follow the existing handler/codec/writeError conventions.
- **Gates.** Every task ends green on the touched suites; the phase ends green on the full lefthook pre-push set: `bun run lint:js`, `bun run lint:go`, `bun run test:api`, `bun run test:shared`, `bun run test:web`, `bun run test:desktop`, `bun run test:mobile` (plus `bun run test:e2e` in CI).
- **Conventional commits**, one commit per task (e.g. `feat(backend): event template + calendar set storage`), ending with the session trailer per repo convention.
- **Contract sync.** Any change to `backend/internal/domain` must be mirrored in `packages/shared/src/types.ts` and noted in `docs/architecture.md` (three-way sync rule stated at the top of `types.ts`).
- **TDD.** Every task writes the failing test first. UI tasks test behavior via Testing Library, not snapshots.

---

### Task 1: Backend — migration + domain types for event templates and calendar sets

**Files:**
- Create: `backend/migrations/0005_event_templates_calendar_sets.sql` (picked up automatically by `//go:embed *.sql` in `backend/migrations/embed.go`)
- Modify: `backend/internal/domain/calendar.go` (append `EventTemplate`, `EventTemplateInput`, `CalendarSet`, `CalendarSetInput`)
- Test: `backend/internal/domain/domain_test.go` (validation helpers, if any added)

**Interfaces:**
- Produces (Go domain, mirrored later in types.ts):

```go
// EventTemplate is a saved event default set ("1:1", "Focus block") applied
// at creation time. CalendarID may be empty (= user's default calendar) and
// is nulled when the referenced calendar is deleted.
type EventTemplate struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Title           string   `json:"title"`
	Description     string   `json:"description"`
	Location        string   `json:"location"`
	DurationMinutes int      `json:"durationMinutes"`
	AllDay          bool     `json:"allDay"`
	CalendarID      *string  `json:"calendarId"`
	AttendeeEmails  []string `json:"attendeeEmails"`
	AddConferencing bool     `json:"addConferencing"`
	ReminderMinutes []int    `json:"reminderMinutes"`
	RecurrenceRule  *string  `json:"recurrenceRule"`
	UsageCount      int      `json:"usageCount"`
}

// EventTemplateInput is the create/update payload (full replace on update).
type EventTemplateInput struct {
	Name            string   `json:"name"`
	Title           string   `json:"title"`
	Description     string   `json:"description,omitempty"`
	Location        string   `json:"location,omitempty"`
	DurationMinutes int      `json:"durationMinutes"`
	AllDay          bool     `json:"allDay,omitempty"`
	CalendarID      *string  `json:"calendarId,omitempty"`
	AttendeeEmails  []string `json:"attendeeEmails,omitempty"`
	AddConferencing bool     `json:"addConferencing,omitempty"`
	ReminderMinutes []int    `json:"reminderMinutes,omitempty"`
	RecurrenceRule  *string  `json:"recurrenceRule,omitempty"`
}

// CalendarSet is a named group of calendars toggled together ("Work", "Home").
type CalendarSet struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	CalendarIDs []string `json:"calendarIds"`
	Position    int      `json:"position"`
}

// CalendarSetInput is the create/update payload (full replace on update).
type CalendarSetInput struct {
	Name        string   `json:"name"`
	CalendarIDs []string `json:"calendarIds"`
	Position    int      `json:"position"`
}
```

- Migration SQL (final form):

```sql
-- Saved event defaults (Fantastical event templates).
CREATE TABLE event_templates (
    id               text PRIMARY KEY,
    user_id          text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name             text NOT NULL,
    title            text NOT NULL DEFAULT '',
    description      text NOT NULL DEFAULT '',
    location         text NOT NULL DEFAULT '',
    duration_minutes integer NOT NULL DEFAULT 30 CHECK (duration_minutes > 0),
    all_day          boolean NOT NULL DEFAULT false,
    calendar_id      text REFERENCES calendars(id) ON DELETE SET NULL,
    attendee_emails  jsonb NOT NULL DEFAULT '[]',
    add_conferencing boolean NOT NULL DEFAULT false,
    reminder_minutes jsonb NOT NULL DEFAULT '[]',
    recurrence_rule  text,
    usage_count      integer NOT NULL DEFAULT 0,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX event_templates_user_idx ON event_templates (user_id);

-- Named calendar groups that toggle visibility together (Fantastical sets).
CREATE TABLE calendar_sets (
    id           text PRIMARY KEY,
    user_id      text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         text NOT NULL,
    calendar_ids jsonb NOT NULL DEFAULT '[]',
    position     integer NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX calendar_sets_user_idx ON calendar_sets (user_id);
```

**Steps:**
- [ ] Add the two structs + inputs to `backend/internal/domain/calendar.go`; run `cd backend && go build ./...` (compiles, nothing consumes them yet).
- [ ] Add `0005_event_templates_calendar_sets.sql`; verify the migration applies by running the existing migration-driven repo test boot: `cd backend && go test ./internal/adapter/out/postgres/ -run TestMain -count=1` (testcontainers applies all migrations at container start — any SQL error fails here).
- [ ] `cd backend && golangci-lint run ./...`

### Task 2: Backend — Postgres repos for templates and sets

**Files:**
- Create: `backend/internal/adapter/out/postgres/template_set.go`
- Modify: `backend/internal/adapter/out/postgres/store.go` (expose `EventTemplates()`/`CalendarSets()` accessors, matching how existing repos are wired)
- Modify: `backend/internal/port/driven.go` (two new repo interfaces)
- Test: `backend/internal/adapter/out/postgres/template_set_test.go` (testcontainers harness from `postgres_test.go`, same style as `snippet_label_test.go`)

**Interfaces:**
- Produces (in `port/driven.go`):

```go
// EventTemplateRepo persists per-user saved event defaults.
type EventTemplateRepo interface {
	Create(ctx context.Context, userID string, t domain.EventTemplate) (domain.EventTemplate, error)
	GetByID(ctx context.Context, id string) (domain.EventTemplate, string, error) // returns ownerUserID
	ListByUser(ctx context.Context, userID string) ([]domain.EventTemplate, error)
	Update(ctx context.Context, t domain.EventTemplate) error
	IncrementUsage(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
}

// CalendarSetRepo persists per-user named calendar groups.
type CalendarSetRepo interface {
	Create(ctx context.Context, userID string, s domain.CalendarSet) (domain.CalendarSet, error)
	GetByID(ctx context.Context, id string) (domain.CalendarSet, string, error) // returns ownerUserID
	ListByUser(ctx context.Context, userID string) ([]domain.CalendarSet, error)
	Update(ctx context.Context, s domain.CalendarSet) error
	Delete(ctx context.Context, id string) error
}
```

(`GetByID` returning the owner id mirrors how the service layer does ownership checks for snippets; JSONB columns marshal via the existing `jsonb` helpers in `helpers.go`.)

**Steps:**
- [ ] Write `template_set_test.go` first: CRUD round-trip for both repos, JSONB fields survive round-trip, `ListByUser` ordering (`name` asc for templates, `position, name` for sets), `calendar_id` nulls on calendar delete (FK `SET NULL`), `IncrementUsage` bumps the counter, cross-user isolation. Run: `cd backend && go test ./internal/adapter/out/postgres/ -run 'TemplateRepo|CalendarSetRepo' -count=1` → fails.
- [ ] Implement `template_set.go` + store accessors until green.
- [ ] `cd backend && go test ./internal/adapter/out/postgres/ -count=1 && golangci-lint run ./...`

### Task 3: Backend — CalendarService template/set methods

**Files:**
- Modify: `backend/internal/port/driving.go` (extend `CalendarService` interface)
- Modify: `backend/internal/service/calendar.go` (+`CalendarServiceDeps`: `Templates port.EventTemplateRepo`, `Sets port.CalendarSetRepo`)
- Modify: `backend/cmd/api/main.go` (wire the new repos into the service)
- Test: `backend/internal/service/calendar_test.go` (extend), `backend/internal/service/fakes_test.go` (in-memory fakes for both repos)

**Interfaces:**
- Produces (appended to `port.CalendarService`):

```go
	ListEventTemplates(ctx context.Context, userID string) ([]domain.EventTemplate, error)
	CreateEventTemplate(ctx context.Context, userID string, in domain.EventTemplateInput) (domain.EventTemplate, error)
	UpdateEventTemplate(ctx context.Context, userID, templateID string, in domain.EventTemplateInput) (domain.EventTemplate, error)
	DeleteEventTemplate(ctx context.Context, userID, templateID string) error

	ListCalendarSets(ctx context.Context, userID string) ([]domain.CalendarSet, error)
	CreateCalendarSet(ctx context.Context, userID string, in domain.CalendarSetInput) (domain.CalendarSet, error)
	UpdateCalendarSet(ctx context.Context, userID, setID string, in domain.CalendarSetInput) (domain.CalendarSet, error)
	DeleteCalendarSet(ctx context.Context, userID, setID string) error
```

- Business rules (each is a test case):
  - Every method calls `s.ent.require(ctx, userID)` first (entitlement gate, matches existing methods).
  - Template: `Name` required (trimmed non-empty); `DurationMinutes > 0` (default 30 when 0); `CalendarID`, when set, must resolve through `ownedCalendar` (else `domain.ErrNotFound`).
  - Set: `Name` required; every id in `CalendarIDs` must belong to the user (validate against `calendars.ListByUser`; unknown id → `domain.ErrValidation`); duplicates de-duped preserving order.
  - Update/Delete on another user's template/set → `domain.ErrNotFound` (via the ownerUserID returned by `GetByID`).
  - `CreateEvent` additionally: when `in.TemplateID != ""` — **no**; keep template application client-side (client expands a template into `EventInput`). The only server-side hook: new optional field is *not* added to `EventInput`. Instead the client calls `POST /v1/event-templates/{id}/use` → `IncrementUsage`. Add `UseEventTemplate(ctx, userID, templateID string) error` to the interface and rules above.

**Steps:**
- [ ] Add fakes for both repos to `fakes_test.go` (same map-backed style as `fakeSnippetRepo`).
- [ ] Write table-driven tests in `calendar_test.go` covering every rule above plus happy paths; run `cd backend && go test ./internal/service/ -run 'Template|CalendarSet' -count=1` → fails.
- [ ] Implement the methods in `service/calendar.go`; wire deps in `cmd/api/main.go`; green.
- [ ] `cd backend && go test ./... -count=1 && golangci-lint run ./...`

### Task 4: Backend — REST endpoints

**Files:**
- Modify: `backend/internal/adapter/in/httpapi/calendar.go` (nine handlers)
- Modify: `backend/internal/adapter/in/httpapi/httpapi.go` (routes)
- Test: `backend/internal/adapter/in/httpapi/calendar_handlers_test.go` (extend), `backend/internal/adapter/in/httpapi/harness_test.go` (extend the fake CalendarService)

**Interfaces:**
- Produces (routes, all authed):

```
GET    /v1/event-templates            → []EventTemplate
POST   /v1/event-templates            body EventTemplateInput → EventTemplate
PUT    /v1/event-templates/{id}       body EventTemplateInput → EventTemplate
DELETE /v1/event-templates/{id}       → 204
POST   /v1/event-templates/{id}/use   → 204   (usage counter)
GET    /v1/calendar-sets              → []CalendarSet
POST   /v1/calendar-sets              body CalendarSetInput → CalendarSet
PUT    /v1/calendar-sets/{id}         body CalendarSetInput → CalendarSet
DELETE /v1/calendar-sets/{id}         → 204
```

**Steps:**
- [ ] Extend the harness fake service with the nine methods; write handler tests (status codes, JSON round-trip, validation error → 400 envelope, not-found → 404, auth required → 401) mirroring `calendar_handlers_test.go` conventions. `cd backend && go test ./internal/adapter/in/httpapi/ -run 'Template|CalendarSet' -count=1` → fails.
- [ ] Implement handlers + routes; green.
- [ ] Document the endpoints in `docs/architecture.md` (REST v1 table).
- [ ] `cd backend && go test ./... -count=1 && golangci-lint run ./...`

### Task 5: Shared — types + ApiClient methods

**Files:**
- Modify: `packages/shared/src/types.ts` (add `EventTemplate`, `EventTemplateInput`, `CalendarSet`, `CalendarSetInput` in the Calendar section, mirroring Task 1 field-for-field with string dates n/a — no dates here)
- Modify: `packages/shared/src/client.ts` (nine methods)
- Test: `packages/shared/src/client.test.ts` (extend)

**Interfaces:**
- Produces (`ApiClient`):

```ts
listEventTemplates(): Promise<EventTemplate[]>                                   // GET  /v1/event-templates
createEventTemplate(input: EventTemplateInput): Promise<EventTemplate>           // POST /v1/event-templates
updateEventTemplate(id: string, input: EventTemplateInput): Promise<EventTemplate> // PUT /v1/event-templates/{id}
deleteEventTemplate(id: string): Promise<void>                                   // DELETE
useEventTemplate(id: string): Promise<void>                                      // POST /v1/event-templates/{id}/use
listCalendarSets(): Promise<CalendarSet[]>                                       // GET  /v1/calendar-sets
createCalendarSet(input: CalendarSetInput): Promise<CalendarSet>                 // POST /v1/calendar-sets
updateCalendarSet(id: string, input: CalendarSetInput): Promise<CalendarSet>     // PUT  /v1/calendar-sets/{id}
deleteCalendarSet(id: string): Promise<void>                                     // DELETE
```

**Steps:**
- [ ] Extend `client.test.ts` (fetch-mock style already used there): URL/method/body assertions + error envelope handling for each method → `bun run test:shared` fails.
- [ ] Implement types + methods; green: `bun run test:shared && bun run --cwd packages/shared typecheck && bun run lint:js`.

### Task 6: Shared — conference-link detection + conflict/free-slot math

**Files:**
- Create: `packages/shared/src/conferencing.ts`, `packages/shared/src/conflicts.ts`
- Modify: `packages/shared/src/index.ts` (re-export both)
- Test: `packages/shared/src/conferencing.test.ts`, `packages/shared/src/conflicts.test.ts`

**Interfaces:**
- Produces (`conferencing.ts`) — detection order: structured `conferencing` field wins, then location, then description:

```ts
export type ConferenceProvider = 'meet' | 'zoom' | 'teams' | 'webex' | 'other';
export interface DetectedConference { provider: ConferenceProvider; url: string }

const PATTERNS: ReadonlyArray<readonly [ConferenceProvider, RegExp]> = [
  ['zoom',  /https?:\/\/(?:[\w-]+\.)?zoom\.us\/(?:j|my|s|w)\/[\w?=&.\-]+/i],
  ['meet',  /https?:\/\/meet\.google\.com\/[a-z]{3}-[a-z]{4}-[a-z]{3}(?:\?[\w=&\-]*)?/i],
  ['teams', /https?:\/\/teams\.(?:microsoft|live)\.com\/(?:l\/meetup-join|meet)\/[\w%/=?.\-&]+/i],
  ['webex', /https?:\/\/(?:[\w-]+\.)?webex\.com\/(?:meet|join|wbxmjs|[\w-]+\/j\.php)[\w%/=?.\-&]*/i],
];

export function detectConference(
  ev: { conferencing: Conferencing | null; location: string | null; description: string | null }
): DetectedConference | null {
  if (ev.conferencing) return { provider: ev.conferencing.provider, url: ev.conferencing.url };
  for (const text of [ev.location, ev.description]) {
    if (!text) continue;
    for (const [provider, re] of PATTERNS) {
      const m = re.exec(text);
      if (m) return { provider, url: m[0] };
    }
  }
  return null;
}

/** Join is offered from `leadMinutes` before start until the event ends. */
export function isJoinable(now: Date, start: Date, end: Date, leadMinutes = 5): boolean {
  return now.getTime() >= start.getTime() - leadMinutes * 60_000 && now < end;
}
```

- Produces (`conflicts.ts`) — the cross-account blocking core. Inputs are the *unfiltered* event fetch (all calendars, all accounts, hidden included):

```ts
export interface BusyInterval {
  start: Date; end: Date;
  eventId: string; calendarId: string; accountId: string; title: string;
}

/** Half-open overlap test: [aS,aE) ∩ [bS,bE) ≠ ∅. */
export function overlaps(aS: Date, aE: Date, bS: Date, bE: Date): boolean {
  return aS < bE && bS < aE;
}

/**
 * Busy = non-cancelled, non-all-day, not declined by the user (ownEmails =
 * every connected account address). Mirrors the backend Availability rules.
 */
export function toBusyIntervals(
  events: Event[],
  calendars: Calendar[],
  ownEmails: string[],
  opts: { ignoreEventId?: string } = {}
): BusyInterval[]

/** Sorted-sweep merge of overlapping/adjacent intervals. */
export function mergeBusy(intervals: BusyInterval[]): Array<{ start: Date; end: Date }>

/** Existing events overlapping a candidate slot → double-booking warning list. */
export function findConflicts(start: Date, end: Date, busy: BusyInterval[]): BusyInterval[]

/**
 * Gaps of >= durationMinutes between merged busy intervals inside [from, to).
 * Used for quick-add slot suggestions; caller pre-clamps to working hours.
 */
export function suggestFreeSlots(
  from: Date, to: Date, durationMinutes: number, busy: BusyInterval[]
): Array<{ start: Date; end: Date }> {
  const merged = mergeBusy(busy.filter((b) => overlaps(b.start, b.end, from, to)));
  const out: Array<{ start: Date; end: Date }> = [];
  let cursor = from;
  for (const b of merged) {
    if (b.start.getTime() - cursor.getTime() >= durationMinutes * 60_000)
      out.push({ start: cursor, end: b.start });
    if (b.end > cursor) cursor = b.end;
  }
  if (to.getTime() - cursor.getTime() >= durationMinutes * 60_000)
    out.push({ start: cursor, end: to });
  return out;
}
```

**Steps:**
- [ ] Write `conferencing.test.ts`: each provider URL shape (zoom `/j/123?pwd=`, personal `/my/room`, meet code with/without query, teams `l/meetup-join` percent-encoded, webex `/meet/user` and `/j.php`), structured-field precedence, location-before-description precedence, plain-text location → null, `isJoinable` boundaries (start−5m inclusive, end exclusive). `bun run test:shared` → fails.
- [ ] Write `conflicts.test.ts`: overlap boundary cases (touching intervals do NOT conflict), cancelled/all-day/self-declined excluded, `ignoreEventId` excludes the event being edited, merge of nested + chained intervals, free slots at range head/middle/tail, empty busy → whole range.
- [ ] Implement both modules; green: `bun run test:shared && bun run lint:js`.

### Task 7: Web — natural-language parser extensions (durations, recurrence, locations, alerts, attendees)

**Files:**
- Modify: `apps/web/lib/quick-add.ts`
- Test: `apps/web/lib/quick-add.test.ts` (extend; keep every existing test passing — the current grammar is regression-locked)

**Interfaces:**
- Produces (extended, backward-compatible — existing fields unchanged):

```ts
export interface QuickAddParse {
  title: string;
  start: Date;
  end: Date;
  allDay: boolean;
  matched: boolean;
  // M2.2 additions:
  location: string | null;
  recurrenceRule: string | null;   // RFC 5545 RRULE body, e.g. "FREQ=WEEKLY;BYDAY=MO,WE"
  reminderMinutes: number[];       // alerts, minutes before start
  attendeeEmails: string[];        // consumed from the text
  attendeeNames: string[];         // surfaced as suggestions, NOT consumed from title
  durationMinutes: number | null;  // explicit duration when given
}
```

- Grammar and consumption order (order is load-bearing — each stage consumes its match out of the text before the next runs, so "every monday" must be eaten by recurrence before the weekday-date rule sees "monday", and "at 3" must be eaten by time rules before the location rule sees "at ..."):

```
1. all-day     ALL_DAY_RE   = /\ball[\s-]?day\b/i                       → allDay = true
2. recurrence  (below)                                                   → recurrenceRule
3. alerts      ALERT_RE     = /\b(?:alert|remind(?:er)?(?:\s+me)?|notify(?:\s+me)?)\s+
                              (\d+)\s*(minutes?|mins?|min|m|hours?|hrs?|hr|h|days?|d)\s*
                              (?:before|prior|early)?\b/ix               → reminderMinutes[]
               (repeat while matching; unit→minutes: h*60, d*1440)
4. time range / single time / duration / date — EXISTING RULES, unchanged,
   plus DURATION_WORD_RE = /\bfor\s+(?:an?\s+)?(half\s+an?\s+hour|hour(?:\s+and\s+a\s+half)?)\b/i
               → 30 / 60 / 90 minutes
5. attendees   WITH_RE      = /\bwith\s+((?:[\w.+-]+@[\w-]+\.[\w.-]+|[A-Z][\w'’-]*)
                              (?:\s*(?:,|and|&|\+)\s*
                              (?:[\w.+-]+@[\w-]+\.[\w.-]+|[A-Z][\w'’-]*))*)/
               Split the capture on ,/and/&/+. Tokens containing '@' →
               attendeeEmails and ARE consumed (rewrite text without them).
               Capitalized name tokens → attendeeNames but the "with Ana"
               phrase stays in the title (Fantastical behavior: title
               "Lunch with Ana", attendee suggestion "Ana").
6. location    LOCATION_RE  = /\b(?:at|in|@)\s+(.+?)(?=\s+(?:with|every|for|alert|remind|notify|until|from)\b|\s*$)/i
               Runs after ALL time/date rules, so "at" here is never a time.
               Reject captures that are purely numeric/empty. Consumed.
7. title = remaining text (existing cleanup rules)
```

- Recurrence sub-grammar → RRULE (implement as `parseRecurrence(consume): string | null`):

```
"daily" | "every day"                     → FREQ=DAILY
"weekly" | "every week"                   → FREQ=WEEKLY
"biweekly" | "every other week"           → FREQ=WEEKLY;INTERVAL=2
"monthly" | "every month"                 → FREQ=MONTHLY
"yearly" | "annually" | "every year"      → FREQ=YEARLY
"every N days|weeks|months|years"         → FREQ=<unit>;INTERVAL=N
"every weekday"                           → FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR
"every <wd>[(s)][ (,|and) <wd>...]"       → FREQ=WEEKLY;BYDAY=<list>   (e.g. "every mon and wed" → BYDAY=MO,WE)
"every other <wd>"                        → FREQ=WEEKLY;INTERVAL=2;BYDAY=<wd>
trailing "until <existing date grammar>"  → append ;UNTIL=<yyyymmdd>T000000Z (re-use the date rules on the captured tail)
WEEKDAY_TOKEN = { sun:'SU', mon:'MO', tue:'TU', wed:'WE', thu:'TH', fri:'FR', sat:'SA' }
```

  When a BYDAY recurrence is parsed and *no explicit date* was given, anchor `start` to the next occurrence of the first BYDAY weekday (reuse the existing weekday-advance math).

**Steps:**
- [ ] Extend `quick-add.test.ts` with a table per stage (≥40 new cases), including combinations: `"standup every weekday 9:30 alert 5 min before"`, `"lunch with ana@acme.com tomorrow 12:30-1:30 at Blue Bottle"`, `"1:1 with Sarah every other tuesday at 2pm for 45 min"`, `"rent reminder monthly on the 1st"` (numeric-date + monthly), `"offsite jul 24 all day"`, `"call at 3 at Office"` (time then location, both "at"), ambiguity locks: `"dinner at 7"` still parses as time-only, `"every monday"` does not also set a one-off Monday date. `bun run --cwd apps/web test lib/quick-add.test.ts` → fails.
- [ ] Implement stage by stage in the order above, keeping each existing test green after each stage.
- [ ] Full: `bun run test:web && bun run lint:js`.

### Task 8: Web — quick-add live-preview UI + event dialog integration

**Files:**
- Create: `apps/web/components/app/calendar/quick-add-bar.tsx` (extracted from the inline quick-add input in `apps/web/app/(app)/calendar/page.tsx`)
- Modify: `apps/web/app/(app)/calendar/page.tsx` (use the component), `apps/web/components/app/event-dialog.tsx` (accept the new `defaults` fields: `location`, `recurrenceRule`, `reminderMinutes`, `attendeeEmails`)
- Test: `apps/web/components/app/calendar/quick-add-bar.test.tsx`, extend `apps/web/components/app/event-dialog.test.tsx`

**Interfaces:**
- Consumes: `parseQuickAdd` (Task 7), `suggestFreeSlots` (Task 6).
- Produces: `<QuickAddBar calendars={...} events={...} onCreate={(defaults: Partial<EventInput>) => void} />` — as the user types, a live preview strip under the input renders parse chips (date/time, duration, recurrence, location, alert, attendees — each chip labeled with a lucide icon) plus up to 3 free-slot suggestions when no explicit time was typed (from `suggestFreeSlots` over today's busy intervals, clamped to 09:00–18:00). Enter opens the event dialog prefilled; clicking a suggestion prefills that slot.

**Steps:**
- [ ] Component test first: typing `"lunch with ana@acme.com tomorrow 1pm at Cafe every friday alert 10 min before"` renders chips for each parsed facet; Enter calls `onCreate` with the full `Partial<EventInput>` (assert `recurrenceRule`, `location`, `reminderMinutes: [10]`, `attendeeEmails`); typing a title with no time renders slot suggestions derived from a busy fixture. `bun run --cwd apps/web test components/app/calendar/quick-add-bar.test.tsx` → fails.
- [ ] Implement; extend `EventDialog` prefill handling (test: `defaults.recurrenceRule` shows in the recurrence field, attendees pre-chipped).
- [ ] `bun run test:web && bun run lint:js`. *(Final code-level pass at execution time: exact chip styling to shadcn tokens.)*

### Task 9: Web — view-range math + calendar page decomposition

**Files:**
- Create: `apps/web/lib/calendar-views.ts`
- Create: `apps/web/components/app/calendar/time-grid.tsx`, `apps/web/components/app/calendar/agenda-view.tsx`, `apps/web/components/app/calendar/mini-month.tsx` (verbatim extraction of `TimeGrid`/`DayColumn`, `AgendaView`, `MiniMonth` + helpers `layoutDayEvents`, `hourLabel`, `withAlpha`, `eventTouchesDay` out of `page.tsx` — no behavior change)
- Modify: `apps/web/app/(app)/calendar/page.tsx` (import the extractions; replace its local `viewRange` and `CalendarView` type with the lib's)
- Test: `apps/web/lib/calendar-views.test.ts`

**Interfaces:**
- Produces (`calendar-views.ts`):

```ts
export type CalendarView = 'day' | 'week' | 'month' | 'quarter' | 'year' | 'ticker';
export const WEEK_OPTS = { weekStartsOn: 0 as const };

export interface ViewRange {
  from: Date;           // inclusive
  to: Date;             // exclusive — feeds GET /v1/events from/to directly
  days: Date[];         // grid days for day/week/month; [] for quarter/year/ticker
}

export function viewRange(view: CalendarView, anchor: Date): ViewRange {
  switch (view) {
    case 'day': {
      const from = startOfDay(anchor);
      return { from, to: addDays(from, 1), days: [from] };
    }
    case 'week': {
      const from = startOfWeek(anchor, WEEK_OPTS);
      return { from, to: addDays(from, 7), days: [...Array(7)].map((_, i) => addDays(from, i)) };
    }
    case 'month': {
      // Full leading/trailing weeks: 4–6 rows × 7, always whole weeks.
      const from = startOfWeek(startOfMonth(anchor), WEEK_OPTS);
      const to = addDays(startOfWeek(endOfMonth(anchor), WEEK_OPTS), 7);
      return { from, to, days: eachDayOfInterval({ start: from, end: addDays(to, -1) }) };
    }
    case 'quarter': {
      const from = startOfQuarter(anchor);
      return { from, to: addQuarters(from, 1), days: [] }; // renders 3 MiniMonth grids
    }
    case 'year': {
      const from = startOfYear(anchor);
      return { from, to: addYears(from, 1), days: [] };    // renders 12 MiniMonth grids
    }
    case 'ticker': {
      const from = startOfDay(anchor);
      return { from, to: addDays(from, TICKER_DAYS), days: [] }; // TICKER_DAYS = 30
    }
  }
}

/** J/K step per view: day/ticker ±1d, week ±7d, month ±1mo, quarter ±3mo, year ±1y. */
export function stepAnchor(view: CalendarView, anchor: Date, dir: 1 | -1): Date

/** Header label per view: "Jul 17, 2026", "Jul 13 – 19, 2026", "July 2026", "Q3 2026", "2026". */
export function rangeLabel(view: CalendarView, anchor: Date): string

/** Single-key view switch map used by shortcuts + palette. */
export const VIEW_KEYS = { d: 'day', w: 'week', m: 'month', q: 'quarter', y: 'year', a: 'ticker' } as const;
```

**Steps:**
- [ ] Write `calendar-views.test.ts`: month grid always spans whole weeks (Feb-2026 = 4 rows exactly when Feb 1 is Sunday; months straddling 6 rows), `to` is exclusive and equals the next period's natural start, `stepAnchor` round-trips (`step(step(a,1),-1) === a` at day granularity), quarter boundaries (Jul 17 → Q3: Jul 1–Oct 1), `rangeLabel` per view, DST-crossing week keeps 7 days. `bun run --cwd apps/web test lib/calendar-views.test.ts` → fails.
- [ ] Implement the lib; extract the components; port `page.tsx` to consume them (agenda view becomes the `ticker` view's engine — see Task 10). Existing calendar behavior unchanged: `bun run test:web` green.
- [ ] `bun run lint:js && bun run --cwd apps/web typecheck`.

### Task 10: Web — Month view + DayTicker hybrid list

**Files:**
- Create: `apps/web/components/app/calendar/month-view.tsx`, `apps/web/components/app/calendar/day-ticker.tsx`
- Modify: `apps/web/app/(app)/calendar/page.tsx` (view switcher gains Month + Ticker; `CalendarView` union now from `calendar-views.ts`), `apps/web/lib/calendar-mock.ts` (ensure the demo dataset spans a full month so month view is non-empty)
- Test: `apps/web/components/app/calendar/month-view.test.tsx`, `apps/web/components/app/calendar/day-ticker.test.tsx`

**Interfaces:**
- Produces:

```ts
// month-view.tsx — 7-col CSS grid of viewRange('month').days; each cell lists up
// to 3 event pills (all-day first, then by start) + "+N more" overflowing into a
// popover; today ring; days outside the anchor month dimmed; cell click →
// onDayClick(day) (jump to day view), pill click → onEventClick(event).
export interface MonthViewProps {
  anchor: Date; days: Date[]; events: Event[];
  calendarById: Map<string, CalendarModel>;
  onDayClick: (day: Date) => void; onEventClick: (event: Event) => void;
}

// day-ticker.tsx — Fantastical hybrid: a horizontally scrollable day strip
// (14 days centered on anchor; click/arrow selects) above a grouped event list
// (the existing AgendaView engine) that auto-scrolls to the selected day.
export interface DayTickerProps {
  anchor: Date; events: Event[];
  calendarById: Map<string, CalendarModel>;
  onAnchorChange: (day: Date) => void; onEventClick: (event: Event) => void;
}
```

**Steps:**
- [ ] Month tests: renders 35/42 cells for known anchors; overflow day shows "+N more"; multi-day event pill appears on each touched day (reuse `eventTouchesDay`); dimmed out-of-month cells; clicks fire callbacks. → fails, then implement.
- [ ] Ticker tests: strip renders 14 dated cells with weekday initials; selecting a strip day calls `onAnchorChange`; list groups by day and hides empty days; today badge. → fails, then implement.
- [ ] Wire into the page's view switcher (Tabs) + `viewRange` fetch keying. `bun run test:web && bun run lint:js`. *(Final code-level pass: pill/strip polish.)*

### Task 11: Web — Quarter + Year views

**Files:**
- Create: `apps/web/components/app/calendar/quarter-view.tsx`, `apps/web/components/app/calendar/year-view.tsx`
- Modify: `apps/web/components/app/calendar/mini-month.tsx` (add optional `density?: Map<string, number>` prop — event count per `yyyy-MM-dd` — rendered as 0–3 intensity dots under each day; keep the existing picker behavior when absent), `apps/web/app/(app)/calendar/page.tsx`
- Test: `apps/web/components/app/calendar/quarter-view.test.tsx`, `apps/web/components/app/calendar/year-view.test.tsx`

**Interfaces:**
- Consumes: `viewRange('quarter'|'year')`, one unfiltered `fetchEvents(from, to)` per range (the local mirror makes a year fetch acceptable; events are reduced to a density map with `useMemo`, individual events are never rendered at these zoom levels).
- Produces: `QuarterView` = 3 `MiniMonth`s side by side; `YearView` = 12 in a responsive 3–4 col grid; clicking any day → `onDayClick(day)` (switches to day view at that date); current month highlighted.

**Steps:**
- [ ] Tests: density map computed correctly from a fixture (multi-day events count on each touched day), 3 vs 12 grids for known anchors, day click drills down. → fails.
- [ ] Implement both views + `MiniMonth` density prop; wire into the switcher. `bun run test:web && bun run lint:js`.

### Task 12: Web — multi-account overlay + cross-account conflict blocking

**Files:**
- Modify: `apps/web/app/(app)/calendar/page.tsx` (sidebar calendar list grouped by account — heading per `ConnectedAccount.email`; visibility toggles stay per-calendar via `patchCalendar`)
- Modify: `apps/web/components/app/event-dialog.tsx` (double-booking warning)
- Modify: `apps/web/lib/calendar-data.ts` (add `fetchBusyEvents(from: Date, to: Date): Promise<Event[]>` — an *unfiltered* `listEvents` used for conflict math, independent of visibility filtering; demo fallback included)
- Test: extend `apps/web/components/app/event-dialog.test.tsx`; `apps/web/lib/calendar-data.ts` behavior covered via existing data-layer patterns

**Interfaces:**
- Consumes: `toBusyIntervals`, `findConflicts` (Task 6), `useSelfEmails` (`lib/use-identity.ts`) for the declined-by-me rule, `fetchCalendars` + `listAccounts` for account grouping.
- Produces, in `EventDialog`: whenever start/end change (edit or create), a query fetches busy events for that day across ALL calendars (hidden ones included — that is the cross-account blocking) and `findConflicts(start, end, busy)` (with `ignoreEventId` = the edited event) drives an inline amber warning: `"Conflicts with «Standup» (9:30–9:45, work@acme.com)"`, listing up to 3 conflicts. Non-blocking — save stays enabled (deliberate double-booking is allowed, warned loudly).
- Produces, in `QuickAddBar` (already wired in Task 8): slot suggestions come from the same unfiltered busy set, so a personal-account dentist appointment blocks a work-account suggestion.

**Steps:**
- [ ] Event-dialog test: with a busy fixture on another account's hidden calendar overlapping the chosen time, the warning renders with the event title + account email; moving the time clears it; editing an event never conflicts with itself. → fails.
- [ ] Implement `fetchBusyEvents` + the warning; group the sidebar by account.
- [ ] `bun run test:web && bun run lint:js`.

### Task 13: Web — conference Join button + one-click add conferencing

**Files:**
- Create: `apps/web/components/app/calendar/join-button.tsx`
- Modify: `apps/web/components/app/event-dialog.tsx` (Join row when a conference is detected; "Add video conferencing" switch on create), `apps/web/components/app/calendar/time-grid.tsx`, `day-ticker.tsx`, `month-view.tsx` (Join affordance on event blocks/rows at meeting time)
- Test: `apps/web/components/app/calendar/join-button.test.tsx`, extend `event-dialog.test.tsx`

**Interfaces:**
- Consumes: `detectConference` + `isJoinable` (Task 6); `EventInput.addConferencing` (backend already wired: Google → `conferenceData.createRequest` (Meet), Microsoft → `isOnlineMeeting`/`teamsForBusiness` (Teams)); `Calendar.accountId` → account provider for labeling.
- Produces:

```ts
// join-button.tsx
export interface JoinButtonProps { event: Event; now?: Date; size?: 'sm' | 'default' }
// Renders provider-labeled button ("Join Zoom" / "Join Meet" / "Join Teams" /
// "Join Webex") opening detectConference(event).url in a new tab. Solid/primary
// while isJoinable(now, start, end); ghost/link otherwise; null when no
// conference detected.
```

  In `EventDialog` create mode: an "Add video conferencing" `Switch` sets `addConferencing: true` on the `EventInput`; helper text derives from the target calendar's account provider — "Google Meet link will be added" vs "Teams meeting will be added". Not shown in edit mode (`EventPatch` has no conferencing field — provider adapters only support attach-on-create; note this limitation in the UI copy and in `docs/feature-map.md`).

**Steps:**
- [ ] `join-button.test.tsx`: fake timers around start−5m/start/end boundaries drive the prominent/ghost/null states; provider label per URL fixture (all four vendors + structured-field event). → fails, implement.
- [ ] `event-dialog.test.tsx`: create with the switch on submits `addConferencing: true`; provider helper text follows the selected calendar's account. → fails, implement.
- [ ] Surface `JoinButton` in time-grid event blocks (only when block height ≥ 2 rows), ticker rows, and month popovers. `bun run test:web && bun run lint:js`.

### Task 14: Web — multi-timezone grid columns

**Files:**
- Create: `apps/web/lib/timezones.ts`
- Modify: `apps/web/components/app/calendar/time-grid.tsx` (N extra hour-label gutters left of the primary one), `apps/web/app/(app)/calendar/page.tsx` (a "+ TZ" popover on the week header: IANA zone search over `Intl.supportedValuesOf('timeZone')`, max 3 pinned)
- Test: `apps/web/lib/timezones.test.ts`, extend grid coverage in `apps/web/components/app/calendar/` tests

**Interfaces:**
- Produces (`timezones.ts`) — no dependencies, DST-correct via `Intl`:

```ts
const PINNED_KEY = 'calendium.pinnedTimeZones';
export function getPinnedTimeZones(): string[]            // localStorage, validated IANA ids
export function setPinnedTimeZones(zones: string[]): void // max 3, dedup, persists

/** Offset of `timeZone` from UTC in minutes at instant `at` (DST-aware). */
export function tzOffsetMinutes(timeZone: string, at: Date): number {
  const dtf = new Intl.DateTimeFormat('en-US', {
    timeZone, hour12: false,
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit',
  });
  const p = Object.fromEntries(dtf.formatToParts(at).map((x) => [x.type, x.value]));
  const asUTC = Date.UTC(+p.year, +p.month - 1, +p.day, +p.hour % 24, +p.minute, +p.second);
  return Math.round((asUTC - at.getTime()) / 60_000);
}

/**
 * Gutter label for the primary-zone hour `hour` of `day` rendered in `zone`:
 * label ("3 PM") + dayShift (-1|0|1) so the gutter can mark "prev/next day".
 */
export function hourLabelInZone(day: Date, hour: number, zone: string): { label: string; dayShift: -1 | 0 | 1 }

/** Short zone caption for the gutter header, e.g. "NYC GMT-4" → cityLabel + gmtLabel. */
export function zoneCaption(zone: string, at: Date): { city: string; gmt: string }
```

**Steps:**
- [ ] `timezones.test.ts`: `tzOffsetMinutes('America/New_York', summerDate) === -240` and winter `-300` (DST both sides); half-hour zone (`Asia/Kolkata` +330); `hourLabelInZone` day-shift across the dateline (`Pacific/Auckland` vs `America/Los_Angeles`); localStorage round-trip + cap at 3. `bun run --cwd apps/web test lib/timezones.test.ts` → fails, implement.
- [ ] Grid: render one extra gutter per pinned zone (labels computed per displayed day so DST transitions mid-week stay correct); persist via `setPinnedTimeZones`; component test asserts the extra gutter labels for a pinned fixture zone.
- [ ] `bun run test:web && bun run lint:js`. *(Final code-level pass: gutter width/typography.)*

### Task 15: Web — keyboard-first calendar + unified ⌘K actions

**Files:**
- Modify: `apps/web/app/(app)/calendar/page.tsx` (shortcut bindings), `apps/web/components/app/command-palette.tsx` (calendar action group), `apps/web/lib/mail-utils.ts` (only if the `MailCommand` dispatch pattern is generalized — prefer a parallel `dispatchCalendarCommand` in a new `apps/web/lib/calendar-commands.ts`)
- Create: `apps/web/lib/calendar-commands.ts` (typed command bus mirroring `mail-utils.ts`'s `dispatchMailCommand`/`queueMailCommand` so the palette can act on the calendar page from anywhere, including cross-route: queue → navigate → drain)
- Test: `apps/web/lib/calendar-commands.test.ts`, extend `apps/web/components/app/command-palette.test.tsx`

**Interfaces:**
- Produces:

```ts
// calendar-commands.ts
export type CalendarCommand =
  | { type: 'today' }
  | { type: 'step'; dir: 1 | -1 }
  | { type: 'view'; view: CalendarView }
  | { type: 'new-event'; defaults?: Partial<EventInput> }
  | { type: 'new-from-template'; templateId: string }
  | { type: 'share-availability' }
  | { type: 'toggle-set'; setId: string };
export function dispatchCalendarCommand(cmd: CalendarCommand): void
export function queueCalendarCommand(cmd: CalendarCommand): void  // for cross-route palette actions
export function useCalendarCommands(handler: (cmd: CalendarCommand) => void): void
```

- Bindings on the calendar page (`useShortcuts`, suppressed while inputs are focused per the hook's default):
  - `t` → today; `j` / `k` → next / previous period (`stepAnchor`); `d w m q y` → view switch via `VIEW_KEYS`; `a` → ticker; `s` → share-availability dialog; `c` → new event; `/` → focus quick-add; existing `mod+k` opens the palette.
- Palette additions (always listed; when not on `/calendar` they `queueCalendarCommand` + `router.push('/calendar')`): "Go to today (T)", "Day/Week/Month/Quarter/Year/Ticker view (D/W/M/Q/Y/A)", "New event (C)", "Share availability (S)", "New event from template: <name>" (from `listEventTemplates`), "Calendar set: <name>" (from `listCalendarSets`). Each row shows its `Kbd` hint (shortcut-teaching UX).

**Steps:**
- [ ] `calendar-commands.test.ts`: dispatch reaches a mounted handler; queue survives until a handler mounts, then drains once. → fails, implement (copy the proven event-bus pattern from `mail-utils.ts`).
- [ ] Palette test: calendar group renders with Kbd hints; selecting "Week view" on `/mail` queues + navigates; template rows appear from a mocked template list. → fails, implement.
- [ ] Page test (or e2e in Task 21): pressing `m` switches to month view, `t` re-anchors today, `j`/`k` move by the view's step.
- [ ] `bun run test:web && bun run lint:js`.

### Task 16: Web — calendar peek beside the inbox

**Files:**
- Create: `apps/web/components/app/calendar-peek.tsx`
- Modify: `apps/web/app/(app)/mail/page.tsx` (right-side collapsible panel + shortcut), `apps/web/app/(app)/layout.tsx` only if the panel must live in the shared shell (prefer the mail page)
- Test: `apps/web/components/app/calendar-peek.test.tsx`

**Interfaces:**
- Consumes: `fetchCalendars` + `fetchEvents` (React Query, same keys as the calendar page so the cache is shared), `TimeGrid` (single-day mode) and the ticker's day strip, `JoinButton`, `useShortcuts`.
- Produces: `<CalendarPeek open onOpenChange />` — a ~320px right panel on the mail route showing today's single-day `TimeGrid` with a Day/Week toggle (week renders the 7-day strip + selected-day grid), next-event card at top with `JoinButton`, and a "New event" button that opens `EventDialog`. Toggled by `mod+shift+k` and a palette entry "Toggle calendar peek"; open state persisted in `localStorage('calendium.calendarPeek')`.

**Steps:**
- [ ] Component test: renders today's events from a fixture, next-event card picks the first non-past event, Join button appears for an in-window conference event, toggle persists. → fails, implement.
- [ ] Wire into the mail page grid layout + shortcut + palette entry.
- [ ] `bun run test:web && bun run lint:js`. *(Final code-level pass: exact layout split with the thread list at execution time.)*

### Task 17: Web — event templates UI

**Files:**
- Create: `apps/web/lib/template-data.ts` (data wrappers with `DEMO_MODE` fallback), `apps/web/components/app/calendar/template-manager.tsx` (list/create/edit/delete dialog)
- Modify: `apps/web/lib/calendar-mock.ts` (mock templates + mutations), `apps/web/components/app/event-dialog.tsx` ("Start from template" select in create mode + "Save as template" action), `apps/web/app/(app)/calendar/page.tsx` (manager entry point in the sidebar), `apps/web/components/app/command-palette.tsx` (already wired in Task 15 — verify against live data)
- Test: `apps/web/components/app/calendar/template-manager.test.tsx`, extend `event-dialog.test.tsx`

**Interfaces:**
- Consumes: `ApiClient.listEventTemplates/create/update/delete/useEventTemplate` (Task 5).
- Produces: `applyTemplate(t: EventTemplate, at: Date): Partial<EventInput>` in `template-data.ts` — title/description/location/attendees/reminders/recurrence/addConferencing copied, `start = at`, `end = at + durationMinutes`, calendar defaulted to `t.calendarId ??` user's primary writable calendar; selecting a template in the dialog or palette also fires `useEventTemplate(t.id)` (fire-and-forget).

**Steps:**
- [ ] `template-manager.test.tsx`: lists mocked templates, create form validates name, delete confirms, edit round-trips. → fails, implement.
- [ ] `event-dialog.test.tsx`: choosing a template prefills all fields at the pending slot time; "Save as template" posts the current form as `EventTemplateInput` with `durationMinutes` derived from start/end. → fails, implement.
- [ ] Extend `calendar-mock.ts` so demo mode has 2 seed templates. `bun run test:web && bun run lint:js`.

### Task 18: Web — calendar sets UI

**Files:**
- Create: `apps/web/lib/set-data.ts` (data wrappers + `DEMO_MODE` fallback), `apps/web/components/app/calendar/set-switcher.tsx`
- Modify: `apps/web/lib/calendar-mock.ts` (mock sets), `apps/web/app/(app)/calendar/page.tsx` (switcher above the sidebar calendar list)
- Test: `apps/web/components/app/calendar/set-switcher.test.tsx`

**Interfaces:**
- Consumes: `ApiClient.listCalendarSets/create/update/delete` (Task 5), `patchCalendar` (existing).
- Produces: `activateSet(set: CalendarSet, calendars: Calendar[]): Promise<void>` in `set-data.ts` — batch-PATCHes `isVisible: true` for the set's calendars and `isVisible: false` for all others (visibility is the server-persisted mechanism, so sets apply across devices); "All calendars" pseudo-set restores everything visible. Active set id is a client preference: `localStorage('calendium.activeCalendarSet')`, cleared whenever a manual per-calendar toggle diverges from the active set. The switcher offers create-from-current-visibility ("Save current selection as set…"), rename, delete, and each set row is reachable from the palette (Task 15).

**Steps:**
- [ ] `set-switcher.test.tsx`: activating a set issues exactly the expected `patchCalendar` calls (visible + hidden partition, no redundant PATCHes for calendars already in the right state); manual toggle clears the active badge; save-current creates a set from currently visible ids. → fails, implement.
- [ ] Seed demo mocks; `bun run test:web && bun run lint:js`.

### Task 19: Desktop — calendar parity in CalendarView

**Files:**
- Modify: `apps/desktop/frontend/src/views/CalendarView.tsx`, `apps/desktop/frontend/src/lib/mock.ts` (demo data for month/conference fixtures)
- Test: `apps/desktop/frontend/src/views/CalendarView.test.tsx` (new, jsdom + Testing Library per the existing desktop test setup)

**Interfaces:**
- Consumes: `@calendium/shared` — `detectConference`, `isJoinable`, `toBusyIntervals`, `findConflicts`, plus the template/set client methods (desktop already uses the shared `ApiClient`).
- Produces (lighter code detail, straightforward UI — patterns proven on web): month view grid (port of the web month math — the `viewRange` month arithmetic is 15 lines of date-fns and is duplicated intentionally, desktop does not import from `apps/web`); `d/w/m` view keys + `t/j/k` navigation using the desktop's existing keyboard handling; `JoinButton`-equivalent on event blocks and the event detail pane; double-booking warning in the desktop event form via `findConflicts`; "Add video conferencing" toggle passing `addConferencing` on create.

**Steps:**
- [ ] Tests first for: conference detection renders a Join control for a Zoom-in-location fixture; month grid cell count; conflict warning for an overlapping fixture. `bun run test:desktop` → fails.
- [ ] Implement; `bun run test:desktop && bun run lint:js && cd apps/desktop && golangci-lint run ./...` (Go side untouched but the gate must stay green).

### Task 20: Mobile — calendar tab parity

**Files:**
- Modify: `apps/mobile/app/(tabs)/calendar.tsx`
- Test: `apps/mobile/` test suite location per its existing setup (`bun run test:mobile`)

**Interfaces:**
- Consumes: `@calendium/shared` — `detectConference`, `isJoinable`, template client methods.
- Produces (lighter detail, straightforward RN UI): DayTicker-style layout (horizontal day strip + day event list — the natural mobile default, matching Task 10's semantics); Join button (`Linking.openURL`) on event rows/detail when `isJoinable`; template picker in the create-event sheet using `applyTemplate` semantics from Task 17 (re-implemented locally over shared types); month mini-grid for date jumping.

**Steps:**
- [ ] Tests: join control visibility around meeting time (fake timers), template application prefills the sheet, ticker strip day selection. `bun run test:mobile` → fails, implement.
- [ ] `bun run test:mobile && bun run lint:js`.

### Task 21: E2E, docs, and phase gate

**Files:**
- Modify: `apps/web/e2e/calendar.spec.ts` (extend), `apps/web/e2e/command-palette.spec.ts` (calendar actions), `docs/feature-map.md` (flip the M2.2 rows to `scaffolded`/shipped status), `docs/architecture.md` (endpoints, verified in Task 4)
- Test: this task IS the test.

**Interfaces:**
- Consumes: demo mode (`DEMO_MODE`) — the Playwright suite runs against mocks per `apps/web/e2e/fixtures.ts`.

**Steps:**
- [ ] New e2e flows (demo mode): quick-add `"lunch with ana@acme.com tomorrow 1pm at Cafe"` → preview chips → Enter → dialog prefilled → create → event visible; `m`/`q`/`y`/`a` view switching + `t`/`j`/`k` navigation; double-booking warning appears when creating over a seeded busy slot; Join button on the seeded conference event; create/apply an event template via ⌘K; switch a calendar set and assert sidebar visibility changes; toggle calendar peek from the mail view.
- [ ] `bun run test:e2e` green locally (built web app + Chromium, as CI does).
- [ ] Full gate: `bun run lint:js && bun run lint:go && bun run test` — all suites green.
- [ ] Update `docs/feature-map.md` statuses; commit; the phase exits per the roadmap success criteria (live-data works, tests at every touched layer, gates pass, interactions within the sub-100ms budget measured by the M2.1 perf harness).
