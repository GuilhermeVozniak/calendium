# M2.8 — Calendar Life Integrations & Long Tail Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
> **Migration numbering:** the eight M2 phase plans were authored in parallel, so migration filenames here are provisional — at execution time use the next free number in `backend/migrations/` and update references in the affected task.

**Goal**

Ship the final M2 phase: make the calendar a *life* surface, not just a meetings mirror. First-class tasks/todos render beside events, drag onto the grid as timeblocks, and check off in place — locally and mirrored from Todoist. Events carry notes, real locations (Maps autocomplete), travel-time buffers with mobile leave-alerts, and inline weather. An automation engine (FocusGuard weekly focus blocking, focus/OOO auto-decline with custom messages, auto buffers between back-to-back meetings) runs in `cmd/worker` against the mirrored calendar and writes through to providers. ICS subscription calendars (stdlib RFC 5545 parser), a time-insights panel, email-to-event drag, HubSpot CRM context in the contact pane, and an in-app concierge tour close out the feature map's Integrations + remaining Scheduling/Views rows and the Platform long tail (`docs/feature-map.md`; spec: `docs/superpowers/specs/2026-07-17-m2-roadmap-design.md` §M2.8).

**Architecture**

- **Hexagonal, unchanged shape.** New capability = new domain types in `backend/internal/domain`, new driven ports in `backend/internal/port/driven.go`, new driving ports in `backend/internal/port/driving.go`, service logic in `backend/internal/service`, HTTP surface in `backend/internal/adapter/in/httpapi` (stdlib ServeMux, `New()` route table in `httpapi.go`), persistence in `backend/internal/adapter/out/postgres`, and **every external HTTP integration as its own outbound adapter package** under `backend/internal/adapter/out/` (`todoist`, `nominatim`, `openmeteo`, `hubspot`) implementing exactly one port. Services never import adapters; adapters never import services.
- **Ports for third parties.** `TodoProvider` (Todoist first, Things/Notion/Linear later), `MapsProvider` (Nominatim+OSRM first, Google/Apple later), `WeatherProvider` (Open-Meteo first), `CrmProvider` (HubSpot first, Salesforce/Pipedrive later). Adapters are keyed in maps (like today's `map[domain.Provider]port.CalendarProvider`) so a second vendor is only a new package + one wiring line.
- **Worker.** `backend/cmd/worker/main.go` gains a third loop: `automationInterval = 15 * time.Minute` → `port.AutomationService.RunAutomation` (FocusGuard, auto-buffers, travel buffers, OOO/focus auto-decline, ICS subscription refresh, todo sync). Leave-alerts and other time-critical pushes ride the existing 5-second `ProcessDueWork` loop.
- **Managed events.** Automation-created events (focus/buffer/travel/OOO) are real provider events (write-through via the existing `CalendarService` path) tagged in a local `managed_events` table so the engine is idempotent, can shrink/delete its own blocks, and never touches user events.
- **Per-user integration OAuth** (Todoist, HubSpot) mirrors the existing account-connect flow: `OAuthStateRepo` for CSRF state, AES-256-GCM token encryption in the postgres store, connect/callback endpoints, `GET /v1/instance` advertises which vendors are configured.
- **Frontend.** Web (`apps/web`) is the flagship: task rail + grid drag on `app/(app)/calendar/page.tsx`, notes/location in `components/app/event-dialog.tsx`, insights panel, weather chips, subscription manager in settings, CRM context in the contact pane, thread-drag from `app/(app)/mail/page.tsx`, onboarding tour. Desktop (Wails, `apps/desktop/frontend`) reuses the same component patterns; mobile (Expo, `apps/mobile`) gets task list + check-off and leave-alert notifications. `packages/shared` carries every new type + `ApiClient` method so all clients stay contract-locked.

**Tech Stack**

- Backend: Go stdlib only (`net/http`, `database/sql` via existing pgx driver dep, `encoding/json`, `time`); Postgres migrations under `backend/migrations/NNNN_*.sql` (embedded FS); table-driven tests + fakes (`internal/service/fakes_test.go` pattern), `httptest` for HTTP adapters and vendor API fakes, testcontainers Postgres for repos (`internal/adapter/out/postgres/postgres_test.go` pattern).
- External services (all free-tier, keyless where possible): Todoist REST v2 (OAuth2), Nominatim search + OSRM routing (configurable base URLs, no key), Open-Meteo forecast (no key), HubSpot CRM v3 (OAuth2).
- Web: Next.js 15, React 19, shadcn new-york components in `apps/web/components/ui`, TanStack Query, `date-fns`; vitest + Testing Library for components, Playwright (demo mode) for e2e.
- Shared: `packages/shared/src/types.ts` + `client.ts` (typed fetch client), vitest.
- Gates: Biome (`bun run lint:js`), golangci-lint (`bun run lint:go`), full test matrix (`bun run test`), lefthook pre-push + CI (`.github/workflows`).

**Global Constraints**

1. **Stdlib-only backend — no new Go module dependencies.** Every new integration is a port + hand-rolled `net/http` adapter. JSON via `encoding/json`; OAuth via the existing PKCE/state machinery; retries/timeouts via `context` + `http.Client{Timeout: ...}`.
2. **Mocks only behind demo flags.** Web demo fixtures live in `apps/web/lib/*-mock.ts` and are reachable only through the `lib/demo.ts` gate (same for desktop/mobile). No mock data on live paths; no demo branches inside backend code.
3. **Graceful degradation when an integration is unconfigured.** If a vendor's config/env is absent, its adapter is not wired; `GET /v1/instance` reports capability flags; endpoints backed by a missing gateway return `501` with a clear error; UI hides or disables the affected affordance. Automation features that need no vendor (FocusGuard, buffers, OOO) always work.
4. **TDD every task**: write the failing test, watch it fail, implement, watch it pass. No implementation commit without its tests.
5. **Gates before every commit**: `bun run lint` (Biome + golangci-lint both Go modules) and the touched suites (`bun run test:api`, `bun run test:shared`, `bun run test:web`, `bun run test:mobile`, `bun run test:desktop`) must pass. Pre-push runs them all via lefthook; do not `--no-verify`.
6. **Conventional commits**, one commit per task (or tighter): `feat(backend): ...`, `feat(web): ...`, `test(...): ...`, `chore(...): ...`.
7. **Migration numbering**: this plan assigns `0005`–`0012`. Earlier M2 phases may have consumed numbers; at execution time renumber to the next free `NNNN` in `backend/migrations/` keeping this plan's relative order. Same rule for any route or file that an earlier phase moved: keep the interface, adapt the path, note it in the commit body.
8. **Provider write-through invariant** (matches `internal/service/calendar.go`): every event mutation attempts the provider first; on provider failure the local mirror is untouched and the error surfaces. Automation jobs must tolerate per-user failure without stalling the fleet (log + continue, like `syncAllAccounts`).
9. **Sub-100ms local interactions**: task check-off, drag-to-timeblock, and notes edits are optimistic (TanStack Query mutation + rollback), matching the existing thread-action pattern in `apps/web/lib/use-mail.ts`.

---

### Task 1: Task domain model, migration, and TaskRepo

**Files:**
- `backend/internal/domain/task.go` (new)
- `backend/internal/domain/task_test.go` (new)
- `backend/migrations/0005_tasks.sql` (new)
- `backend/internal/port/driven.go` (add `TaskQuery`, `TaskRepo`)
- `backend/internal/adapter/out/postgres/task.go` (new)
- `backend/internal/adapter/out/postgres/task_test.go` (new)
- `backend/internal/adapter/out/postgres/store.go` (add `Tasks()` accessor)

**Interfaces:**

```go
// port/driven.go

// TaskQuery filters task lists. UserID is mandatory; zero values mean "no
// filter". Completed tasks are excluded unless IncludeCompleted is set.
// ScheduledFrom/ScheduledTo select tasks whose scheduled block overlaps
// [ScheduledFrom, ScheduledTo) — the calendar-grid query. DueFrom/DueTo
// select by due date — the rail's "due today" grouping.
type TaskQuery struct {
	UserID           string
	Source           domain.TaskSource
	IncludeCompleted bool
	ScheduledFrom    time.Time
	ScheduledTo      time.Time
	DueFrom          time.Time
	DueTo            time.Time
	UnscheduledOnly  bool
	Limit            int
}

// TaskRepo persists first-class tasks (local and mirrored external todos).
type TaskRepo interface {
	Create(ctx context.Context, t domain.Task) (domain.Task, error)
	GetByID(ctx context.Context, id string) (domain.Task, error)
	// GetByExternalID resolves a mirrored provider todo; domain.ErrNotFound
	// when the task was never synced.
	GetByExternalID(ctx context.Context, userID string, source domain.TaskSource, externalID string) (domain.Task, error)
	List(ctx context.Context, q TaskQuery) ([]domain.Task, error)
	Update(ctx context.Context, t domain.Task) error
	Delete(ctx context.Context, id string) error
	// DeleteBySource removes every mirrored task of one source for a user
	// (integration disconnect).
	DeleteBySource(ctx context.Context, userID string, source domain.TaskSource) error
}
```

**Domain sketch** (`backend/internal/domain/task.go`) — the reference model for the whole phase:

```go
package domain

import (
	"fmt"
	"strings"
	"time"
)

// TaskSource identifies where a task originates.
type TaskSource string

const (
	TaskSourceLocal   TaskSource = "local"
	TaskSourceTodoist TaskSource = "todoist"
)

// ParseTaskSource validates a task source parameter.
func ParseTaskSource(s string) (TaskSource, error) {
	switch TaskSource(s) {
	case TaskSourceLocal, TaskSourceTodoist:
		return TaskSource(s), nil
	}
	return "", fmt.Errorf("%w: unknown task source %q", ErrValidation, s)
}

// Task is a first-class todo. Due carries deadline semantics; the
// Scheduled* pair carries timeblock semantics — when both are set the task
// renders on the calendar grid between ScheduledStart and ScheduledEnd and
// in the rail's due grouping. External tasks mirror a provider todo
// (Source/ExternalID) and write completion through to the provider.
type Task struct {
	ID             string     `json:"id"`
	UserID         string     `json:"-"`
	Title          string     `json:"title"`
	Notes          *string    `json:"notes"`
	Due            *time.Time `json:"due"`
	// AllDayDue marks a date-only due (render in the all-day lane, no hour).
	AllDayDue      bool       `json:"allDayDue"`
	ScheduledStart *time.Time `json:"scheduledStart"`
	ScheduledEnd   *time.Time `json:"scheduledEnd"`
	CompletedAt    *time.Time `json:"completedAt"`
	Source         TaskSource `json:"source"`
	ExternalID     string     `json:"-"`
	SourceURL      *string    `json:"sourceUrl"`
	// Position orders tasks inside the unscheduled rail (lower first;
	// fractional so drag-reorder never rewrites neighbors).
	Position  float64   `json:"position"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Completed reports whether the task is checked off.
func (t Task) Completed() bool { return t.CompletedAt != nil }

// Scheduled reports whether the task occupies a timeblock on the grid.
func (t Task) Scheduled() bool { return t.ScheduledStart != nil && t.ScheduledEnd != nil }

// Validate enforces invariants shared by the create and update paths.
func (t Task) Validate() error {
	if strings.TrimSpace(t.Title) == "" {
		return fmt.Errorf("%w: title is required", ErrValidation)
	}
	if (t.ScheduledStart == nil) != (t.ScheduledEnd == nil) {
		return fmt.Errorf("%w: scheduledStart and scheduledEnd must be set together", ErrValidation)
	}
	if t.Scheduled() && !t.ScheduledEnd.After(*t.ScheduledStart) {
		return fmt.Errorf("%w: scheduledEnd must be after scheduledStart", ErrValidation)
	}
	if t.AllDayDue && t.Due == nil {
		return fmt.Errorf("%w: allDayDue requires due", ErrValidation)
	}
	return nil
}

// TaskInput is the create-task payload (mirrors TaskInput in types.ts).
type TaskInput struct {
	Title          string     `json:"title"`
	Notes          string     `json:"notes,omitempty"`
	Due            *time.Time `json:"due,omitempty"`
	AllDayDue      bool       `json:"allDayDue,omitempty"`
	ScheduledStart *time.Time `json:"scheduledStart,omitempty"`
	ScheduledEnd   *time.Time `json:"scheduledEnd,omitempty"`
	Position       *float64   `json:"position,omitempty"`
}

// TaskPatch is a partial update; nil fields are left unchanged. Explicit
// JSON null clears a clearable field (Due, Scheduled*, Notes) — the
// double-pointer decode helper in httpapi/codec.go handles the distinction.
type TaskPatch struct {
	Title          *string     `json:"title"`
	Notes          **string    `json:"notes"`
	Due            **time.Time `json:"due"`
	AllDayDue      *bool       `json:"allDayDue"`
	ScheduledStart **time.Time `json:"scheduledStart"`
	ScheduledEnd   **time.Time `json:"scheduledEnd"`
	Position       *float64    `json:"position"`
}
```

**Migration** (`backend/migrations/0005_tasks.sql`):

```sql
CREATE TABLE tasks (
    id               TEXT PRIMARY KEY,
    user_id          TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title            TEXT NOT NULL,
    notes            TEXT,
    due              TIMESTAMPTZ,
    all_day_due      BOOLEAN NOT NULL DEFAULT FALSE,
    scheduled_start  TIMESTAMPTZ,
    scheduled_end    TIMESTAMPTZ,
    completed_at     TIMESTAMPTZ,
    source           TEXT NOT NULL DEFAULT 'local',
    external_id      TEXT NOT NULL DEFAULT '',
    source_url       TEXT,
    position         DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT tasks_scheduled_pair CHECK ((scheduled_start IS NULL) = (scheduled_end IS NULL)),
    CONSTRAINT tasks_external_unique UNIQUE (user_id, source, external_id)
);
CREATE INDEX tasks_user_scheduled_idx ON tasks (user_id, scheduled_start) WHERE scheduled_start IS NOT NULL;
CREATE INDEX tasks_user_due_idx ON tasks (user_id, due) WHERE due IS NOT NULL;
```

(Note: `tasks_external_unique` treats local tasks as `('local','')` duplicates — enforce uniqueness only for external rows with a partial unique index instead: `CREATE UNIQUE INDEX tasks_external_idx ON tasks (user_id, source, external_id) WHERE source <> 'local';` — drop the table-level constraint.)

**Steps:**

- [ ] Write `backend/internal/domain/task_test.go`: table-driven `TestTaskValidate` (empty title, lone scheduledStart, end before start, allDayDue without due, valid local, valid scheduled), `TestParseTaskSource`, `TestTaskCompleted`.
- [ ] Run `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/domain/` — confirm compile failure/red.
- [ ] Implement `backend/internal/domain/task.go` per the sketch; re-run — green.
- [ ] Add `TaskQuery` + `TaskRepo` to `backend/internal/port/driven.go` (repositories section, after `EventRepo`).
- [ ] Write `backend/internal/adapter/out/postgres/task_test.go` on the testcontainers harness (`postgres_test.go` pattern): create/get/list round-trip, `GetByExternalID` hit+miss, scheduled-range overlap query, `IncludeCompleted` filtering, `UnscheduledOnly`, external-id upsert-conflict behavior, `DeleteBySource`.
- [ ] Run `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/adapter/out/postgres/ -run TestTask` — red.
- [ ] Add `backend/migrations/0005_tasks.sql` (with the partial unique index) and implement `postgres/task.go` (`taskRepo` struct, scan helpers per `postgres/calendar.go` style) + `Tasks() port.TaskRepo` on `Store`; re-run — green.
- [ ] Run `bun run lint:go && bun run test:api` from the repo root — green.
- [ ] Commit: `feat(backend): first-class task domain, tasks table, and TaskRepo`.

---

### Task 2: TaskService, HTTP surface, and shared ApiClient

**Files:**
- `backend/internal/port/driving.go` (add `TaskService`)
- `backend/internal/service/task.go` (new)
- `backend/internal/service/task_test.go` (new)
- `backend/internal/service/fakes_test.go` (add `fakeTaskRepo`)
- `backend/internal/adapter/in/httpapi/tasks.go` (new)
- `backend/internal/adapter/in/httpapi/tasks_handlers_test.go` (new)
- `backend/internal/adapter/in/httpapi/httpapi.go` (Deps + routes)
- `backend/internal/adapter/in/httpapi/codec.go` (add `OptionalField` double-pointer JSON helper if not present)
- `backend/cmd/api/main.go` (wire service)
- `packages/shared/src/types.ts` (`Task`, `TaskInput`, `TaskPatch`, `TaskSource`)
- `packages/shared/src/client.ts` (`listTasks`, `createTask`, `updateTask`, `completeTask`, `reopenTask`, `deleteTask`)
- `packages/shared/src/client.test.ts` (new cases)

**Interfaces:**

```go
// port/driving.go

// TaskService covers first-class tasks: CRUD, timeblock scheduling, and
// in-place completion. Completion of an external task writes through to
// its TodoProvider (Task 10) and rolls back the local mirror on failure.
type TaskService interface {
	ListTasks(ctx context.Context, userID string, q TaskQuery) ([]domain.Task, error)
	CreateTask(ctx context.Context, userID string, in domain.TaskInput) (domain.Task, error)
	UpdateTask(ctx context.Context, userID, taskID string, patch domain.TaskPatch) (domain.Task, error)
	// CompleteTask checks the task off (idempotent); ReopenTask clears it.
	CompleteTask(ctx context.Context, userID, taskID string) (domain.Task, error)
	ReopenTask(ctx context.Context, userID, taskID string) (domain.Task, error)
	DeleteTask(ctx context.Context, userID, taskID string) error
}
```

Routes (in `httpapi.New`, after the events block):

```
authed("GET /v1/tasks", s.handleListTasks)          // ?from&to (scheduled overlap) | ?dueFrom&dueTo | ?unscheduled=1 | ?includeCompleted=1
authed("POST /v1/tasks", s.handleCreateTask)
authed("PATCH /v1/tasks/{id}", s.handleUpdateTask)
authed("POST /v1/tasks/{id}/complete", s.handleCompleteTask)
authed("POST /v1/tasks/{id}/reopen", s.handleReopenTask)
authed("DELETE /v1/tasks/{id}", s.handleDeleteTask)
```

Service rules: entitlement check first (`entitlement.require`, as in `CalendarService`); ownership by `UserID` match → `domain.ErrNotFound` on mismatch; `CreateTask` defaults `Source=local`, `Position` = max+1024 when unset; `UpdateTask` re-runs `Validate()` after patch; `CompleteTask` on a `Source != local` task calls the wired `TodoProvider` first (Task 10 — until then the provider map is empty and completion is local-only).

**Steps:**

- [ ] Add `fakeTaskRepo` (in-memory map, same style as existing fakes) to `backend/internal/service/fakes_test.go`.
- [ ] Write `backend/internal/service/task_test.go`: `TestTaskServiceCreate` (defaults, validation errors, entitlement gate), `TestTaskServiceUpdate` (patch semantics incl. clearing due/schedule via double-pointer, cross-user 404), `TestTaskServiceCompleteReopen` (idempotency, timestamps from fake clock), `TestTaskServiceList` (query passthrough + empty slice not nil).
- [ ] Run `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/service/ -run TestTaskService` — red.
- [ ] Implement `backend/internal/service/task.go` (`TaskServiceDeps`, `NewTaskService`, `var _ port.TaskService = (*TaskService)(nil)`); re-run — green.
- [ ] Write `backend/internal/adapter/in/httpapi/tasks_handlers_test.go` on the harness (`harness_test.go` pattern): each route happy path, query-param parsing (`from`/`to` RFC 3339 via `timeRange`), validation 400s, auth 401, JSON null clears due (`{"due": null}`).
- [ ] Run `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/adapter/in/httpapi/ -run TestTasks` — red; implement `tasks.go` + `Deps.Tasks port.TaskService` + routes; green.
- [ ] Wire `service.NewTaskService` in `backend/cmd/api/main.go` (`Tasks: store.Tasks()`).
- [ ] Add `Task`/`TaskInput`/`TaskPatch`/`TaskSource` to `packages/shared/src/types.ts` and the six client methods to `client.ts`; write the matching fetch-mock tests in `client.test.ts` (URL, method, body, query encoding).
- [ ] Run `bun run test:shared && bun run typecheck` — green.
- [ ] Run `bun run lint && bun run test:api` — green.
- [ ] Commit: `feat(backend,shared): task CRUD service, /v1/tasks surface, and ApiClient methods`.

---

### Task 3: Web tasks UI — rail beside the grid, drag to timeblock, check-off in place

**Files:**
- `apps/web/components/app/task-rail.tsx` (new — rail + `TaskItem` + inline quick-add)
- `apps/web/components/app/task-rail.test.tsx` (new)
- `apps/web/lib/use-tasks.ts` (new — TanStack Query hooks with optimistic mutations)
- `apps/web/lib/tasks-mock.ts` (new — demo fixtures, gated by `lib/demo.ts`)
- `apps/web/lib/calendar-data.ts` (merge scheduled tasks into grid data)
- `apps/web/app/(app)/calendar/page.tsx` (rail mount, grid task blocks, drag/drop, `T` toggle)
- `apps/web/components/app/command-palette.tsx` (add "New task", "Toggle task rail")
- `apps/web/lib/shortcuts.ts` (register `⇧T` new task, `T` handled per-page)
- `apps/web/e2e/tasks.spec.ts` (new — demo mode)
- `apps/mobile` (screen: task list with check-off — `apps/mobile/app/(tabs)/tasks.tsx` or the equivalent route group in the current tree)
- `apps/desktop/frontend` (mirror the rail component into the Wails frontend per its existing component conventions)

**Interfaces:** `useTasks(range)` returns `{ unscheduled, scheduled, dueByDay, createTask, updateTask, complete, reopen, remove }`; every mutation is optimistic with rollback (mirror `use-mail.ts`). Drag contract: rail items set `dataTransfer.setData("application/x-calendium-task", taskId)`; the grid's day columns compute drop time from `clientY` (reuse the existing event drag-position math in `calendar/page.tsx`) and call `updateTask(taskId, { scheduledStart, scheduledEnd: +30min })`; dragging a grid task block back to the rail clears the pair.

**Steps:**

- [ ] Write `apps/web/components/app/task-rail.test.tsx` (vitest + Testing Library): renders unscheduled + due-today groups, checkbox click calls `complete` optimistically (item gets strikethrough before the promise resolves), quick-add submits on Enter, drag start sets the dataTransfer payload.
- [ ] Run `bun run --cwd apps/web test task-rail` — red.
- [ ] Implement `use-tasks.ts` (demo branch reads `tasks-mock.ts` when `isDemo()`), `tasks-mock.ts`, and `task-rail.tsx` (shadcn `Card`/`Checkbox`-style styling consistent with new-york tokens; completed tasks collapse under a toggle); green.
- [ ] Extend `calendar/page.tsx`: right-side rail (collapsible, `T`), scheduled tasks render as grid blocks (distinct rounded style + checkbox; check-off in place without opening a dialog), drop handlers on day columns and on the rail; extend `calendar-data.ts` to interleave task blocks with events in day/week layouts.
- [ ] Add palette commands + shortcut registrations; update `command-palette.test.tsx` expectations.
- [ ] Write `apps/web/e2e/tasks.spec.ts` (Playwright, demo mode): open calendar → rail visible → check a task off → drag a task onto Wednesday 10:00 → block appears → reload keeps state (demo store) — follow the structure of the existing specs in `apps/web/e2e/`.
- [ ] Run `bun run test:web` — green; run `bunx playwright test tasks --config apps/web/playwright.config.ts` locally if Chromium is available (CI enforces otherwise).
- [ ] Port the task list to mobile (`@calendium/shared` client; jest component test for check-off) and mirror the rail into the desktop frontend; run `bun run test:mobile && bun run test:desktop`.
- [ ] Run `bun run lint` — green.
- [ ] Commit: `feat(web,mobile,desktop): task rail on the calendar grid with drag-to-timeblock and in-place check-off`.

---

### Task 4: Docs/notes attached to events

**Files:**
- `backend/migrations/0006_event_notes.sql` (new)
- `backend/internal/domain/calendar.go` (add `EventNote`)
- `backend/internal/port/driven.go` (add `EventNoteRepo`)
- `backend/internal/port/driving.go` (extend `CalendarService`)
- `backend/internal/adapter/out/postgres/event_note.go` (+ test) (new)
- `backend/internal/service/calendar.go` (+ `calendar_test.go`)
- `backend/internal/adapter/in/httpapi/calendar.go` (+ handlers test)
- `backend/internal/adapter/in/httpapi/httpapi.go` (routes)
- `packages/shared/src/types.ts` + `client.ts` (+ tests)
- `apps/web/components/app/event-dialog.tsx` (+ `event-dialog.test.tsx`): Notes tab with autosave

**Interfaces:**

```go
// domain — notes survive provider syncs because they live only locally,
// keyed by our event id (ON DELETE CASCADE follows mirror deletes).
type EventNote struct {
	EventID   string    `json:"eventId"`
	UserID    string    `json:"-"`
	BodyMD    string    `json:"bodyMd"` // markdown; rendered read-only outside edit
	Links     []string  `json:"links"`  // attached doc URLs (Notion, GDoc, ...)
	UpdatedAt time.Time `json:"updatedAt"`
}

// port/driven.go
type EventNoteRepo interface {
	// Upsert replaces the note for (eventID); empty BodyMD+Links deletes it.
	Upsert(ctx context.Context, n domain.EventNote) (domain.EventNote, error)
	GetByEventID(ctx context.Context, eventID string) (domain.EventNote, error)
}

// port/driving.go — CalendarService additions
GetEventNote(ctx context.Context, userID, eventID string) (domain.EventNote, error)
PutEventNote(ctx context.Context, userID, eventID string, bodyMD string, links []string) (domain.EventNote, error)
```

Routes: `authed("GET /v1/events/{id}/note", ...)`, `authed("PUT /v1/events/{id}/note", ...)`. Missing note returns `200` with an empty note (not 404) so the UI needs no special case. Migration: `event_notes(event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, body_md TEXT NOT NULL, links JSONB NOT NULL DEFAULT '[]', updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`.

**Steps:**

- [ ] Repo test (testcontainers): upsert/get round-trip, empty-body upsert deletes, cascade on event delete. Run `go test ./internal/adapter/out/postgres/ -run TestEventNote` — red → implement migration + repo → green.
- [ ] Service test: ownership via `ownedEvent`, entitlement gate, empty-note read. Red → implement → green (`go test ./internal/service/ -run TestEventNote`).
- [ ] Handler test on the harness: GET empty, PUT + re-GET, cross-user 404. Red → implement handlers + routes → green.
- [ ] Shared: `EventNote` type + `getEventNote`/`putEventNote` client methods + tests; `bun run test:shared`.
- [ ] Web: Notes tab in `event-dialog.tsx` (textarea, 800ms debounce autosave, link chips with add/remove, "open" affordance per link); extend `event-dialog.test.tsx` (tab renders, autosave fires PUT, link add). `bun run test:web` — green.
- [ ] `bun run lint && bun run test:api` — green.
- [ ] Commit: `feat(backend,web): notes and doc links attached to events`.

---

### Task 5: Calendar automation preferences (FocusGuard/buffers/OOO/travel settings)

**Files:**
- `backend/migrations/0007_calendar_prefs.sql` (new)
- `backend/internal/domain/prefs.go` (+ `prefs_test.go`) (new)
- `backend/internal/port/driven.go` (add `CalendarPrefsRepo`)
- `backend/internal/port/driving.go` (add `PrefsService`)
- `backend/internal/service/prefs.go` (+ test) (new)
- `backend/internal/adapter/out/postgres/prefs.go` (+ test) (new)
- `backend/internal/adapter/in/httpapi/prefs.go` (+ test), `httpapi.go` routes, `cmd/api/main.go` wiring
- `packages/shared/src/types.ts` + `client.ts` (+ tests)
- `apps/web/app/(app)/settings/page.tsx` (new "Calendar automation" section)

**Interfaces:**

```go
// domain/prefs.go — one row per user; defaults returned when absent.
type CalendarPrefs struct {
	UserID string `json:"-"`
	// Working hours bound every automation (focus, buffers, travel).
	TimeZone            string         `json:"timeZone"`            // IANA; default "UTC"
	WorkDays            []time.Weekday `json:"workDays"`            // default Mon–Fri
	WorkdayStartMinutes int            `json:"workdayStartMinutes"` // default 9*60
	WorkdayEndMinutes   int            `json:"workdayEndMinutes"`   // default 17*60

	FocusGoalMinutesPerWeek int    `json:"focusGoalMinutesPerWeek"` // 0 = FocusGuard off
	FocusAutoDecline        bool   `json:"focusAutoDecline"`
	FocusDeclineMessage     string `json:"focusDeclineMessage"`

	AutoBufferMinutes int `json:"autoBufferMinutes"` // 0 = off; 5..30 valid

	OOOAutoDecline    bool   `json:"oooAutoDecline"`
	OOODeclineMessage string `json:"oooDeclineMessage"`

	TravelBuffers bool              `json:"travelBuffers"`
	TravelMode    TravelMode        `json:"travelMode"` // driving|walking|transit
	LeaveAlerts   bool              `json:"leaveAlerts"`
	HomeLat       *float64          `json:"homeLat"`
	HomeLon       *float64          `json:"homeLon"`

	WeatherEnabled bool `json:"weatherEnabled"`
}

func DefaultCalendarPrefs(userID string) CalendarPrefs
func (p CalendarPrefs) Validate() error // tz loads, minutes ordered & in-day, buffer 0..30, mode known

// port/driven.go
type CalendarPrefsRepo interface {
	// Get returns DefaultCalendarPrefs(userID) when no row exists.
	Get(ctx context.Context, userID string) (domain.CalendarPrefs, error)
	Upsert(ctx context.Context, p domain.CalendarPrefs) error
	// ListAutomated returns prefs rows with any automation enabled — the
	// worker's fan-out set (no full-user table scan of defaults).
	ListAutomated(ctx context.Context) ([]domain.CalendarPrefs, error)
}

// port/driving.go
type PrefsService interface {
	GetCalendarPrefs(ctx context.Context, userID string) (domain.CalendarPrefs, error)
	UpdateCalendarPrefs(ctx context.Context, userID string, patch domain.CalendarPrefsPatch) (domain.CalendarPrefs, error)
}
```

Routes: `authed("GET /v1/prefs/calendar", ...)`, `authed("PATCH /v1/prefs/calendar", ...)`. `CalendarPrefsPatch` follows the nil-means-unchanged convention (pointer fields, defined next to `CalendarPrefs`). Migration `0007_calendar_prefs.sql`: `calendar_prefs(user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, prefs JSONB NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT now())` — a JSONB doc column: the shape is young, all reads are by PK, and `ListAutomated` filters with a JSONB predicate (`prefs->>'focusGoalMinutesPerWeek' != '0' OR ...`).

**Steps:**

- [ ] Domain tests for `Validate` + `DefaultCalendarPrefs`; red → implement → green (`go test ./internal/domain/ -run TestCalendarPrefs`).
- [ ] Repo tests (default-on-missing, upsert round-trip, `ListAutomated` includes only automated rows); red → migration + `postgres/prefs.go` → green.
- [ ] Service + handler tests (patch merge, validation 400, entitlement); red → implement → green.
- [ ] Shared types/client (`getCalendarPrefs`, `updateCalendarPrefs`) + tests; `bun run test:shared`.
- [ ] Settings UI section: working hours pickers, focus goal slider (h/week), auto-decline toggles + message inputs, buffer minutes select, travel mode, weather toggle; extend the settings page test if one exists, else add targeted vitest coverage for the new section component.
- [ ] `bun run lint && bun run test:api && bun run test:web` — green.
- [ ] Commit: `feat(backend,web): calendar automation preferences (focus goal, buffers, OOO, travel, weather)`.

### Task 6: Managed-events infrastructure + FocusGuard weekly auto-blocking

**Files:**
- `backend/migrations/0008_managed_events.sql` (new)
- `backend/internal/domain/automation.go` (new — `ManagedKind`, `ManagedEvent`)
- `backend/internal/port/driven.go` (add `ManagedEventRepo`)
- `backend/internal/port/driving.go` (add `AutomationService`)
- `backend/internal/service/focusguard.go` (new — pure planning engine)
- `backend/internal/service/focusguard_test.go` (new)
- `backend/internal/service/automation.go` (new — orchestration; grows in Tasks 7, 8, 12, 14)
- `backend/internal/service/automation_test.go` (new)
- `backend/internal/adapter/out/postgres/managed_event.go` (+ test) (new)
- `backend/cmd/worker/main.go` (third loop)

**Interfaces:**

```go
// domain/automation.go
type ManagedKind string

const (
	ManagedFocus  ManagedKind = "focus"
	ManagedBuffer ManagedKind = "buffer"
	ManagedTravel ManagedKind = "travel"
)

// ManagedEvent tags a mirrored event as automation-owned. SourceEventID
// links buffers/travel blocks to the meeting they protect; WeekStart keys
// focus blocks to their planning week (Monday, prefs timezone).
type ManagedEvent struct {
	EventID       string
	UserID        string
	Kind          ManagedKind
	SourceEventID *string
	WeekStart     *time.Time
	CreatedAt     time.Time
}

// port/driven.go
type ManagedEventRepo interface {
	Create(ctx context.Context, m domain.ManagedEvent) error
	GetByEventID(ctx context.Context, eventID string) (domain.ManagedEvent, error)
	ListByUser(ctx context.Context, userID string, kind domain.ManagedKind) ([]domain.ManagedEvent, error)
	ListBySourceEvent(ctx context.Context, sourceEventID string) ([]domain.ManagedEvent, error)
	Delete(ctx context.Context, eventID string) error
}

// port/driving.go — consumed by cmd/worker beside SyncService.
type AutomationService interface {
	// RunAutomation runs one pass of every calendar automation for every
	// user with automation enabled: FocusGuard planning, auto buffers,
	// travel buffers, focus/OOO auto-decline, and ICS subscription refresh.
	// Users fail independently.
	RunAutomation(ctx context.Context) error
}
```

Migration `0008_managed_events.sql`:

```sql
CREATE TABLE managed_events (
    event_id        TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL CHECK (kind IN ('focus','buffer','travel')),
    source_event_id TEXT REFERENCES events(id) ON DELETE CASCADE,
    week_start      DATE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX managed_events_user_kind_idx ON managed_events (user_id, kind);
CREATE INDEX managed_events_source_idx ON managed_events (source_event_id) WHERE source_event_id IS NOT NULL;
```

**FocusGuard engine sketch** (`backend/internal/service/focusguard.go`) — a pure function so the planner is exhaustively table-testable without fakes:

```go
package service

import (
	"sort"
	"time"

	"calendium/backend/internal/domain"
)

const (
	focusMinBlock   = time.Hour
	focusMaxBlock   = 3 * time.Hour
	focusEventTitle = "Focus time"
	// focusLeadTime: never plan a block starting sooner than this, so the
	// engine doesn't drop a focus block on top of "right now".
	focusLeadTime = 30 * time.Minute
)

// focusPlan is the delta RunAutomation applies for one user-week.
type focusPlan struct {
	create []domain.EventInput // new focus blocks (CalendarID filled by caller)
	remove []string            // managed focus event ids now colliding with real events
}

// planFocusWeek plans focus blocks for the week starting weekStart (Monday
// 00:00 in prefs.TimeZone). events is every mirrored event overlapping the
// week; managedFocusIDs identifies which of them the engine owns.
//
// Invariants:
//   - existing user events are never moved or shortened;
//   - owned focus blocks that now collide with a real event are removed
//     (the user booked over them — the meeting wins, the plan refills);
//   - surviving focus time counts toward the goal;
//   - new blocks fill the largest remaining working-hour gaps first, are
//     clamped to [focusMinBlock, focusMaxBlock], and stop once the goal
//     is met or the week has no gaps left.
func planFocusWeek(
	now time.Time,
	weekStart time.Time,
	prefs domain.CalendarPrefs,
	events []domain.Event,
	managedFocusIDs map[string]bool,
) focusPlan {
	goal := time.Duration(prefs.FocusGoalMinutesPerWeek) * time.Minute
	if goal <= 0 {
		return focusPlan{}
	}
	loc, err := time.LoadLocation(prefs.TimeZone)
	if err != nil {
		loc = time.UTC
	}

	// Partition: real busy intervals vs owned focus blocks.
	type span struct{ start, end time.Time }
	var busy []span
	var owned []domain.Event
	for _, ev := range events {
		if ev.Status == domain.EventCancelled || ev.AllDay {
			continue
		}
		if managedFocusIDs[ev.ID] {
			owned = append(owned, ev)
			continue
		}
		busy = append(busy, span{ev.Start, ev.End})
	}
	sort.Slice(busy, func(i, j int) bool { return busy[i].start.Before(busy[j].start) })

	var plan focusPlan
	var credit time.Duration
	for _, f := range owned {
		if overlapsAny(f.Start, f.End, busy) {
			plan.remove = append(plan.remove, f.ID) // user booked over it
			continue
		}
		credit += f.End.Sub(f.Start)
		busy = insertSpan(busy, span{f.Start, f.End}) // keeps gap math honest
	}
	if credit >= goal {
		return plan
	}

	// Candidate gaps: per remaining workday, working hours minus busy.
	earliest := now.Add(focusLeadTime)
	var gaps []span
	for d := 0; d < 7; d++ {
		day := weekStart.AddDate(0, 0, d).In(loc)
		if !isWorkDay(day.Weekday(), prefs.WorkDays) {
			continue
		}
		open := dayWindow(day, prefs.WorkdayStartMinutes, prefs.WorkdayEndMinutes, loc)
		if open.end.Before(earliest) {
			continue // day already past
		}
		if open.start.Before(earliest) {
			open.start = earliest
		}
		gaps = append(gaps, subtractBusy(open, busy)...)
	}
	// Largest gap first: fewer, longer deep-work blocks.
	sort.Slice(gaps, func(i, j int) bool {
		return gaps[i].end.Sub(gaps[i].start) > gaps[j].end.Sub(gaps[j].start)
	})

	for _, g := range gaps {
		if credit >= goal {
			break
		}
		length := g.end.Sub(g.start)
		if length < focusMinBlock {
			continue
		}
		if length > focusMaxBlock {
			length = focusMaxBlock
		}
		if remaining := goal - credit; length > remaining && remaining >= focusMinBlock {
			length = remaining.Round(15 * time.Minute)
		}
		plan.create = append(plan.create, domain.EventInput{
			Title: focusEventTitle,
			Start: g.start,
			End:   g.start.Add(length),
		})
		credit += length
	}
	return plan
}
```

Helper primitives `overlapsAny`, `insertSpan`, `subtractBusy`, `dayWindow`, `isWorkDay` live in the same file and get their own unit tests. `automation.go` then does the impure orchestration per user: resolve the user's primary writable calendar (first `IsPrimary && CanWrite`), load this week's + next week's events via `EventRepo.ListInRange`, call `planFocusWeek` for both weeks, apply `plan.remove` via `CalendarService.DeleteEvent` + `ManagedEventRepo.Delete`, and `plan.create` via `CalendarService.CreateEvent` + `ManagedEventRepo.Create` (kind `focus`, `WeekStart` set). Per-user errors are logged and skipped (`syncAllAccounts` discipline).

**Steps:**

- [ ] Write `focusguard_test.go`: table-driven `TestPlanFocusWeek` — empty week fills goal with max-size blocks; busy week places around meetings; goal already met by surviving blocks → no creates; owned block overlapped by new meeting → removed and refilled elsewhere; mid-week `now` never plans in the past; non-workdays skipped; gaps under 1h ignored; remaining goal rounds to 15min. Plus unit tests for `subtractBusy`/`insertSpan` edge cases (touching intervals, containment).
- [ ] Run `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/service/ -run 'TestPlanFocusWeek|TestSubtractBusy|TestInsertSpan'` — red → implement `focusguard.go` → green.
- [ ] Repo test for `ManagedEventRepo` (testcontainers: create/get/list, cascade when the event row is deleted); red → migration + `postgres/managed_event.go` + `store.ManagedEvents()` → green.
- [ ] Write `automation_test.go` with fakes (`fakeManagedEventRepo`, reuse `fakeEventRepo`/`fakeCalendarService`-style fakes in `fakes_test.go`): `TestRunAutomationFocus` — creates provider write-through events for a user with a goal, tags them managed, is idempotent on the second pass, skips users with goal 0, one failing user doesn't stop the next. Red → implement `automation.go` (`AutomationServiceDeps{Prefs, Calendars, Events, Managed, CalendarSvc port.CalendarService, Clock, Logger}`) → green.
- [ ] Wire the third worker loop in `backend/cmd/worker/main.go`: `automationInterval = 15 * time.Minute`, `wg.Add(3)`, `runLoop(ctx, automationInterval, func(ctx){ if err := autoSvc.RunAutomation(ctx); err != nil { logger.Error(...) } })`.
- [ ] `bun run lint:go && bun run test:api` — green.
- [ ] Commit: `feat(backend): FocusGuard weekly focus-time auto-blocking with managed provider events`.

---

### Task 7: Focus auto-decline + OOO auto-decline with custom message

**Files:**
- `backend/internal/port/driven.go` (**signature change**: `CalendarProvider.RSVP` gains a `comment string`)
- `backend/internal/adapter/out/googleapi/calendar.go` (+ test) — comment → `attendees[].comment` on the RSVP patch
- `backend/internal/adapter/out/msgraph/calendar.go` (+ test) — comment → `decline` action body `{"comment": ...}`
- `backend/internal/service/calendar.go` (pass `""` at the existing user-RSVP call site)
- `backend/internal/service/automation.go` (+ `automation_test.go`) — decline pass
- `backend/internal/domain/calendar.go` (add `IsOOOEvent` helper)
- `backend/internal/service/fakes_test.go` (update fake provider signature)

**Interfaces:**

```go
// port/driven.go — changed method (all implementations + call sites updated
// in this task; comment may be empty):
RSVP(ctx context.Context, accessToken, providerCalendarID, providerEventID string, response domain.RsvpStatus, comment string) error
```

Decline pass semantics (inside `RunAutomation`, after focus planning):

1. **Focus auto-decline** (`prefs.FocusAutoDecline`): for each managed `focus` block, find non-cancelled events overlapping it where the user's own attendee entry is `needs_action` and the user is not the organizer → `CalendarService.RSVP(..., RsvpDeclined)` with `prefs.FocusDeclineMessage` (default: "Declined automatically: this time is held for focus work. Please pick another slot.").
2. **OOO auto-decline** (`prefs.OOOAutoDecline`): OOO periods are detected from mirrored events via `domain.IsOOOEvent(ev)` — all-day or timed events whose title matches (case-insensitive) `out of office|ooo|vacation|annual leave|pto` on a calendar the user owns. Every pending (`needs_action`) invite overlapping an OOO period is declined with `prefs.OOODeclineMessage`; **existing accepted** meetings inside a *newly created* OOO period (created after the OOO event's `CreatedAt` cannot be known from the mirror — use: accepted invites overlapping OOO where the user is not organizer) are declined only when `prefs.OOODeclineMessage` is set and the event starts ≥ 24h in the future, so the engine never no-shows same-day meetings silently.
3. Both passes record nothing new in `managed_events` — idempotency comes from the RSVP state itself (`declined` attendees are never re-processed).

Threading the comment through user-facing RSVP: `POST /v1/events/{id}/rsvp` body gains optional `comment`; `CalendarService.RSVP` signature gains `comment string` (driving port + handler + shared client updated here too, since the wire change is one atomic refactor).

**Steps:**

- [ ] Update `fakes_test.go` fake calendar provider + `googleapi`/`msgraph` adapter tests first: `TestRSVPSendsComment` asserting the JSON body carries the comment for both vendors (httptest fake servers, existing adapter-test pattern). Red → change the port signature, adapters, and `service/calendar.go` call site → green (`go test ./internal/adapter/out/... ./internal/service/`).
- [ ] Add `domain.IsOOOEvent` + table test (`titles: "OOO", "Out of office — Lisbon", "Vacation", "foo"`); red → implement → green.
- [ ] Extend `automation_test.go`: `TestFocusAutoDecline` (pending invite overlapping focus block declined with message; organizer-self and already-declined skipped; toggle off = no-op), `TestOOOAutoDecline` (pending overlapping OOO declined; accepted future meeting declined only with message set and ≥24h lead; same-day accepted untouched). Red → implement the decline pass → green.
- [ ] Update driving port + handler + `packages/shared` (`rsvpEvent(eventId, response, comment?)`) + client tests; `bun run test:shared`.
- [ ] `bun run lint && bun run test:api` — green.
- [ ] Commit: `feat(backend): focus and OOO auto-decline with custom RSVP messages` (body notes the `CalendarProvider.RSVP` signature change).

---

### Task 8: Auto buffers between back-to-back meetings

**Files:**
- `backend/internal/service/buffers.go` (new — pure planner)
- `backend/internal/service/buffers_test.go` (new)
- `backend/internal/service/automation.go` (+ test) — buffer pass

**Interfaces:**

```go
// buffers.go — pure planner, same shape as planFocusWeek.
type bufferPlan struct {
	create []bufferCreate // buffer events to insert
	remove []string       // managed buffer event ids whose source gap vanished
}

type bufferCreate struct {
	input         domain.EventInput // Title "Buffer", [meetingEnd, meetingEnd+N)
	sourceEventID string            // the meeting the buffer trails
}

// planBuffers inserts a prefs.AutoBufferMinutes gap after every meeting
// with attendees (>= 2 humans) that is followed by another meeting starting
// exactly at or before its end + buffer. Owned buffers whose source meeting
// moved or whose following meeting vanished are removed. Buffers never
// stack (a buffer is not a meeting), never exceed the true gap, and are
// skipped when the gap is already >= buffer.
func planBuffers(prefs domain.CalendarPrefs, events []domain.Event, owned map[string]domain.ManagedEvent) bufferPlan
```

Orchestration in `RunAutomation`: runs over a rolling 14-day horizon on the primary writable calendar; creates go through `CalendarService.CreateEvent` (title "Buffer", no attendees, `ReminderMinutes: []`), tagged `ManagedBuffer` with `SourceEventID`; removals delete event + tag. Buffer events are excluded from `Availability` busy computation? **No** — buffers exist precisely to hold the time; they stay busy. They are excluded from *insights* meeting-time (Task 16) and from FocusGuard credit.

**Steps:**

- [ ] Write `buffers_test.go` tables: back-to-back pair gets one buffer; triple chain gets two; existing sufficient gap → none; meeting moved → stale buffer removed + new one created; 1:1 with one attendee list empty (solo block) → skipped; buffer-after-buffer never happens; `AutoBufferMinutes=0` → empty plan.
- [ ] Run `go test ./internal/service/ -run TestPlanBuffers` — red → implement → green.
- [ ] Extend `automation_test.go` with the orchestration case (create + tag + idempotent second pass). Red → wire the pass → green.
- [ ] `bun run lint:go && bun run test:api` — green.
- [ ] Commit: `feat(backend): automatic buffers between back-to-back meetings`.

---

### Task 9: Integration-connection infrastructure (per-user OAuth for Todoist/HubSpot)

**Files:**
- `backend/migrations/0009_integration_connections.sql` (new)
- `backend/internal/domain/integration.go` (+ test) (new — `IntegrationVendor`, `IntegrationConnection`)
- `backend/internal/port/driven.go` (add `IntegrationRepo`; reuse `OAuthGateway` for vendor OAuth)
- `backend/internal/port/driving.go` (add `IntegrationService`)
- `backend/internal/service/integration.go` (+ test) (new)
- `backend/internal/adapter/out/postgres/integration.go` (+ test) (new — AES-GCM token storage, same helpers as `account.go`)
- `backend/internal/adapter/in/httpapi/integrations.go` (+ test), `httpapi.go` routes, `instance.go` capability flags
- `backend/internal/config/config.go` (add `Todoist`, `HubSpot` structs: ClientID/ClientSecret from `TODOIST_CLIENT_ID`/`TODOIST_CLIENT_SECRET`/`HUBSPOT_CLIENT_ID`/`HUBSPOT_CLIENT_SECRET`) + `backend/.env.example`
- `backend/cmd/api/main.go` wiring
- `packages/shared/src/types.ts` + `client.ts` (+ tests)
- `apps/web/app/(app)/settings/page.tsx` (Integrations section: connect/disconnect cards)

**Interfaces:**

```go
// domain/integration.go
type IntegrationVendor string

const (
	IntegrationTodoist IntegrationVendor = "todoist"
	IntegrationHubSpot IntegrationVendor = "hubspot"
)

func ParseIntegrationVendor(s string) (IntegrationVendor, error)

// IntegrationConnection is a per-user vendor OAuth grant (one per vendor).
type IntegrationConnection struct {
	ID              string            `json:"id"`
	UserID          string            `json:"-"`
	Vendor          IntegrationVendor `json:"vendor"`
	ExternalAccount string            `json:"externalAccount"` // vendor login/portal label
	Status          string            `json:"status"`          // active|error
	LastError       *string           `json:"lastError"`
	CreatedAt       time.Time         `json:"createdAt"`
}

// port/driven.go — tokens encrypted at rest exactly like AccountRepo.
type IntegrationRepo interface {
	Create(ctx context.Context, c domain.IntegrationConnection) (domain.IntegrationConnection, error)
	GetByID(ctx context.Context, id string) (domain.IntegrationConnection, error)
	GetByVendor(ctx context.Context, userID string, vendor domain.IntegrationVendor) (domain.IntegrationConnection, error)
	ListByUser(ctx context.Context, userID string) ([]domain.IntegrationConnection, error)
	ListByVendor(ctx context.Context, vendor domain.IntegrationVendor) ([]domain.IntegrationConnection, error)
	Update(ctx context.Context, c domain.IntegrationConnection) error
	Delete(ctx context.Context, id string) error
	SaveTokens(ctx context.Context, connectionID string, t TokenSet) error
	GetTokens(ctx context.Context, connectionID string) (TokenSet, error)
}

// port/driving.go — mirrors AccountService's connect choreography.
type IntegrationService interface {
	List(ctx context.Context, userID string) ([]domain.IntegrationConnection, error)
	BeginConnect(ctx context.Context, userID string, vendor domain.IntegrationVendor, redirectURL, requestBaseURL string) (authURL string, err error)
	CompleteConnect(ctx context.Context, vendor domain.IntegrationVendor, state, code, requestBaseURL string) (conn domain.IntegrationConnection, clientRedirect string, err error)
	Disconnect(ctx context.Context, userID, connectionID string) error
}
```

Routes: `authed("GET /v1/integrations", ...)`, `authed("POST /v1/integrations/connect/{vendor}", ...)`, unauthenticated `mux.HandleFunc("GET /v1/integrations/callback/{vendor}", ...)` (state-validated, like the account callback), `authed("DELETE /v1/integrations/{id}", ...)`. Vendor OAuth adapters implement the existing `port.OAuthGateway` (Todoist: `https://todoist.com/oauth/authorize|access_token`, no PKCE — pass empty challenge; HubSpot: `https://app.hubspot.com/oauth/authorize` + `api.hubapi.com/oauth/v1/token`, refresh-token flow). `OAuthStateRepo` is reused with a vendor-prefixed state. `GET /v1/instance` gains `capabilities: {todoist, hubspot, maps, weather bool}` filled from wiring. Disconnect for Todoist also calls `TaskRepo.DeleteBySource`. Migration: `integration_connections(id TEXT PK, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, vendor TEXT NOT NULL, external_account TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'active', last_error TEXT, access_token_enc BYTEA, refresh_token_enc BYTEA, token_expires_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE (user_id, vendor))`.

**Steps:**

- [ ] Domain test (`ParseIntegrationVendor`); red → implement → green.
- [ ] Repo test (testcontainers): round-trip, unique (user, vendor), token encrypt/decrypt (assert ciphertext differs from plaintext in the raw row), delete cascade. Red → migration + `postgres/integration.go` → green.
- [ ] Service test with fakes (`fakeIntegrationRepo`, reuse `fakeOAuthStateRepo`/fake gateway): begin→URL contains state; complete consumes state, stores encrypted tokens, upserts on reconnect; forged state → `ErrNotFound`; disconnect purges Todoist tasks. Red → implement → green (`go test ./internal/service/ -run TestIntegration`).
- [ ] Handler tests (connect 200 with URL, callback 302 to client redirect, list, delete, unknown vendor 400, unconfigured vendor 501). Red → implement + routes + instance capabilities → green.
- [ ] Config: parse the four env vars; extend `config_test.go`; update `backend/.env.example`.
- [ ] Shared types/client (`listIntegrations`, `connectIntegration`, `disconnectIntegration`) + tests; settings Integrations cards (connect opens `authURL`, disconnect confirms) behind capability flags from `useInstance`.
- [ ] `bun run lint && bun run test:api && bun run test:shared && bun run test:web` — green.
- [ ] Commit: `feat(backend,web): per-user integration OAuth infrastructure (Todoist/HubSpot connections)`.

---

### Task 10: TodoProvider port + Todoist adapter + worker todo sync

**Files:**
- `backend/internal/port/driven.go` (add `TodoSyncPage`, `TodoProvider`)
- `backend/internal/adapter/out/todoist/client.go` (new)
- `backend/internal/adapter/out/todoist/client_test.go` (new — httptest fake Todoist)
- `backend/internal/service/automation.go` (+ test) — todo-sync pass
- `backend/internal/service/task.go` (+ test) — write-through completion
- `backend/cmd/worker/main.go` + `backend/cmd/api/main.go` (wire adapter map)

**Interfaces:**

```go
// port/driven.go

// TodoSyncPage is one page of incremental todo sync.
type TodoSyncPage struct {
	Tasks      []domain.Task // ExternalID+Source set; ID/UserID left empty
	DeletedIDs []string      // provider task ids removed or completed upstream
	NextCursor string
	HasMore    bool
}

// TodoProvider is the external todo-tool surface (Todoist first; Things,
// Notion, Linear later). SyncTasks performs incremental sync from cursor
// ("" = full sync; Todoist uses the Sync v9 sync_token).
type TodoProvider interface {
	Source() domain.TaskSource
	SyncTasks(ctx context.Context, accessToken, cursor string) (TodoSyncPage, error)
	CompleteTask(ctx context.Context, accessToken, externalID string) error
	ReopenTask(ctx context.Context, accessToken, externalID string) error
}
```

Adapter: `todoist.NewClient(hc *http.Client) *Client` (token is per-call — stateless client, matching `googleapi`), Sync API v9 `https://api.todoist.com/sync/v9/sync` with `resource_types=["items"]` + `sync_token`, mapping `content→Title`, `description→Notes`, `due.date/datetime→Due/AllDayDue`, `checked→DeletedIDs` (completed upstream disappears from the rail; local mirror rows for them are deleted), item URL → `SourceURL`. Base URL is a struct field so tests point at httptest. Sync pass in `RunAutomation`: for each `ListByVendor(todoist)` connection → `GetTokens` → `SyncTasks(cursor from SyncStateRepo key "todoist")` → upsert by `GetByExternalID` (create or update Title/Notes/Due, **preserving local `Scheduled*` and `Position`**) → delete removed → save cursor. `TaskService.CompleteTask` on a todoist task: provider `CompleteTask` first, local mark second (write-through discipline); provider error surfaces and leaves the task unchecked.

**Steps:**

- [ ] Write `todoist/client_test.go` against an httptest server: full-sync page maps fields correctly (datetime + date-only dues), sync_token round-trip, checked item → DeletedIDs, complete/reopen POST bodies (`item_complete`/`item_uncomplete` commands), non-200 → error. Red → implement `client.go` → green (`go test ./internal/adapter/out/todoist/`).
- [ ] Extend `automation_test.go`: `TestTodoSync` with a fake `TodoProvider` — first pass creates mirrored tasks, second pass with updates preserves local schedule/position, deleted upstream removes rows, cursor persisted, broken connection marks `Status=error` + `LastError` without stalling others. Red → implement the pass → green.
- [ ] Extend `task_test.go`: `TestCompleteTaskWritesThrough` (provider called with token+externalID; provider failure leaves task open). Red → wire `TodoProviders map[domain.TaskSource]port.TodoProvider` + `Integrations port.IntegrationRepo` into `TaskServiceDeps` → green.
- [ ] Wire the adapter in both binaries (only when `cfg.Todoist.ClientID != ""`).
- [ ] `bun run lint:go && bun run test:api` — green.
- [ ] Commit: `feat(backend): Todoist TodoProvider adapter with incremental sync and completion write-through`.

### Task 11: MapsProvider port + adapter + location autocomplete on events

**Files:**
- `backend/migrations/0010_event_geo.sql` (new — `ALTER TABLE events ADD COLUMN location_lat DOUBLE PRECISION, ADD COLUMN location_lon DOUBLE PRECISION;`)
- `backend/internal/domain/calendar.go` (add `LocationLat`/`LocationLon *float64` to `Event`, `EventInput`, `EventPatch`; add `Place`, `TravelMode`)
- `backend/internal/port/driven.go` (add `MapsProvider`)
- `backend/internal/port/driving.go` (add `PlacesService`)
- `backend/internal/adapter/out/nominatim/client.go` (+ test) (new — Nominatim search + OSRM route, both stdlib HTTP, configurable base URLs)
- `backend/internal/service/places.go` (+ test) (new)
- `backend/internal/adapter/in/httpapi/places.go` (+ test), `httpapi.go` route, `instance.go` `capabilities.maps`
- `backend/internal/config/config.go` (add `Maps{NominatimBaseURL, OSRMBaseURL string}` from `MAPS_NOMINATIM_URL`/`MAPS_OSRM_URL`; empty = feature off) + `.env.example`
- `backend/internal/adapter/out/postgres/calendar.go` (+ test) — persist the two columns
- `packages/shared/src/types.ts` + `client.ts` (+ tests)
- `apps/web/components/app/location-field.tsx` (+ test) (new — debounced autocomplete combobox)
- `apps/web/components/app/event-dialog.tsx` (swap plain location input for `LocationField`)

**Interfaces:**

```go
// domain
type Place struct {
	Name    string  `json:"name"`
	Address string  `json:"address"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
}

type TravelMode string

const (
	TravelDriving TravelMode = "driving"
	TravelWalking TravelMode = "walking"
	TravelTransit TravelMode = "transit" // OSRM adapter approximates as driving*1.5 until a transit vendor lands
)

// port/driven.go
type MapsProvider interface {
	// Autocomplete returns up to limit place suggestions for a partial query.
	Autocomplete(ctx context.Context, query string, limit int) ([]domain.Place, error)
	// TravelTime estimates door-to-door duration between two points.
	TravelTime(ctx context.Context, fromLat, fromLon, toLat, toLon float64, mode domain.TravelMode) (time.Duration, error)
}

// port/driving.go
type PlacesService interface {
	Autocomplete(ctx context.Context, userID, query string) ([]domain.Place, error)
}
```

Route: `authed("GET /v1/places/autocomplete", ...)` (`?q=`, min 3 chars, limit 5, `501` when no `MapsProvider` wired). Service adds a 10-minute in-memory LRU (query→results, mutex-guarded map with timestamp eviction — stdlib only) to respect Nominatim's 1 req/s policy; the adapter also sets a proper `User-Agent` ("calendium/1.0") and serializes requests with a `sync.Mutex` + minimum 1s spacing. Picking a suggestion in the UI stores `location` (display string) + `locationLat/locationLon` on the event input; free-typed text keeps coordinates nil (graceful — travel features simply skip such events).

**Steps:**

- [ ] Adapter test (httptest fakes for both Nominatim `/search?format=jsonv2` and OSRM `/route/v1/driving/{coords}`): mapping, limit, error statuses, request spacing under mocked clock skipped (assert serialized calls only). Red → implement `nominatim/client.go` → green (`go test ./internal/adapter/out/nominatim/`).
- [ ] Migration + repo columns test (round-trip lat/lon through `postgres/calendar.go` scan/insert); red → implement → green.
- [ ] Service test (min-length validation, cache hit skips gateway — fake counts calls, entitlement, nil-provider 501 via `domain.ErrUnavailable`-style error mapped in `writeError`); handler test. Red → implement → green.
- [ ] Domain/EventInput/EventPatch plumbing + `eventFromInput`/`applyEventPatch` updates + service tests for persistence through create/update.
- [ ] Shared types/client (`autocompletePlaces`) + tests; web `LocationField` (combobox on `cmdk`, 300ms debounce, keyboard navigation) + vitest (suggestions render, selection fills coords, offline/501 degrades to plain input); wire into `event-dialog.tsx`.
- [ ] `bun run lint && bun run test:api && bun run test:web && bun run test:shared` — green.
- [ ] Commit: `feat(backend,web): MapsProvider port with Nominatim/OSRM adapter and event location autocomplete`.

---

### Task 12: Travel-time buffer events + mobile leave-alerts

**Files:**
- `backend/migrations/0011_travel_alerts.sql` (new — `travel_alerts(event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE, user_id TEXT NOT NULL, leave_at TIMESTAMPTZ NOT NULL, sent_at TIMESTAMPTZ)` + index on `(sent_at, leave_at)`)
- `backend/internal/port/driven.go` (add `TravelAlertRepo`)
- `backend/internal/adapter/out/postgres/travel_alert.go` (+ test) (new)
- `backend/internal/service/travel.go` (+ test) (new — pure planner `planTravel`)
- `backend/internal/service/automation.go` (+ test) — travel pass
- `backend/internal/service/sync.go` (+ test) — leave-alert delivery inside `ProcessDueWork`
- `apps/mobile` (notification category/display already handled by `expo-notifications`; verify payload rendering)

**Interfaces:**

```go
// port/driven.go
type TravelAlertRepo interface {
	Upsert(ctx context.Context, eventID, userID string, leaveAt time.Time) error
	// ListDue returns unsent alerts with leave_at <= now.
	ListDue(ctx context.Context, now time.Time, limit int) ([]domain.TravelAlert, error)
	MarkSent(ctx context.Context, eventID string, at time.Time) error
	Delete(ctx context.Context, eventID string) error
}

// domain
type TravelAlert struct {
	EventID string
	UserID  string
	LeaveAt time.Time
	SentAt  *time.Time
}
```

Travel pass (in `RunAutomation`, `prefs.TravelBuffers`, requires a wired `MapsProvider` and `prefs.HomeLat/Lon`): for each upcoming (next 48h) non-managed event with coordinates, origin = end-location of the previous located event that day, else home → `MapsProvider.TravelTime` → create/refresh a `ManagedTravel` event `[eventStart-travel, eventStart)` titled `"Travel to <location>"` (skip when travel < 10 min; cap 3h; refresh when event moved — detected by comparing owned travel block to `eventStart-travel`), and `TravelAlertRepo.Upsert(eventID, leaveAt = eventStart - travel - 5min)` when `prefs.LeaveAlerts`. Delivery: `ProcessDueWork` (5s loop) pushes due alerts — title `"Time to leave"`, body `"Leave now to make <event title> at <HH:mm>"`, `data: {"type":"leave_alert","eventId":...}` — to every registered device via the existing `port.PushSender`, then `MarkSent`.

**Steps:**

- [ ] `travel_test.go` tables for `planTravel`: home origin, chained-locations origin, <10min skipped, moved event refreshes block + alert, no-coordinate events skipped, managed events skipped. Red → implement → green.
- [ ] Repo test (upsert refresh clears sent_at only when leave_at changed, ListDue excludes sent). Red → migration + repo → green.
- [ ] `automation_test.go` travel-pass orchestration case; `sync_test.go` leave-alert delivery case (fake PushSender records payload; second pass sends nothing). Red → implement both passes → green.
- [ ] Manual mobile check note: Expo dev client shows the push with the leave-alert copy (payload uses the same shape as existing event-reminder pushes, so no mobile code change is expected — add a jest test only if a payload switch lands in mobile code).
- [ ] `bun run lint && bun run test:api` — green.
- [ ] Commit: `feat(backend): travel-time buffer events and leave-now push alerts`.

---

### Task 13: WeatherProvider port + Open-Meteo adapter + inline weather on calendar days

**Files:**
- `backend/internal/domain/weather.go` (new — `DayForecast`)
- `backend/internal/port/driven.go` (add `WeatherProvider`)
- `backend/internal/port/driving.go` (add `WeatherService`)
- `backend/internal/adapter/out/openmeteo/client.go` (+ test) (new)
- `backend/internal/service/weather.go` (+ test) (new — 30-min in-memory cache keyed by rounded lat/lon)
- `backend/internal/adapter/in/httpapi/weather.go` (+ test), `httpapi.go` route, `instance.go` `capabilities.weather`
- `backend/internal/config/config.go` (`Weather{BaseURL string}` from `OPEN_METEO_URL`, default `https://api.open-meteo.com`; set empty to disable) + `.env.example`
- `packages/shared/src/types.ts` + `client.ts` (+ tests)
- `apps/web/app/(app)/calendar/page.tsx` + `apps/web/lib/use-weather.ts` (new) + `apps/web/lib/calendar-mock.ts` (demo forecast)

**Interfaces:**

```go
// domain/weather.go
type DayForecast struct {
	Date         string  `json:"date"` // YYYY-MM-DD in the requested zone
	Code         int     `json:"code"` // WMO weather interpretation code
	HighCelsius  float64 `json:"highCelsius"`
	LowCelsius   float64 `json:"lowCelsius"`
	PrecipChance int     `json:"precipChance"` // 0..100
}

// port/driven.go
type WeatherProvider interface {
	DailyForecast(ctx context.Context, lat, lon float64, timeZone string, days int) ([]domain.DayForecast, error)
}

// port/driving.go
type WeatherService interface {
	// Forecast serves up to 14 days; cached ~30 minutes per location.
	Forecast(ctx context.Context, userID string, lat, lon float64, timeZone string, days int) ([]domain.DayForecast, error)
}
```

Route: `authed("GET /v1/weather", ...)` (`?lat&lon&days&tz`, validated; `501` unconfigured). Open-Meteo call: `/v1/forecast?latitude=&longitude=&daily=weather_code,temperature_2m_max,temperature_2m_min,precipitation_probability_max&timezone=`. Web: `use-weather.ts` resolves location — prefs `HomeLat/Lon` when set, else `navigator.geolocation` (permission-gated, cached in localStorage) — and the day/week/agenda headers render a small icon+high/low chip (lucide icons mapped from WMO code buckets; hidden when pref off, endpoint 501, or no location).

**Steps:**

- [ ] Adapter test (httptest: query params, field mapping, error status); red → implement → green (`go test ./internal/adapter/out/openmeteo/`).
- [ ] Service test (cache hit counts gateway calls with fake clock, days clamp 1..14, entitlement); handler test (param validation, 501). Red → implement → green.
- [ ] Shared client (`getWeather`) + tests; web `use-weather.ts` + chip rendering in the three views + vitest (chip renders from mock, hidden when disabled); demo forecast in `calendar-mock.ts` behind `isDemo()`.
- [ ] `bun run lint && bun run test:api && bun run test:web && bun run test:shared` — green.
- [ ] Commit: `feat(backend,web): Open-Meteo WeatherProvider and inline day forecasts on the calendar`.

---

### Task 14: `backend/internal/ics` — minimal RFC 5545 parser (stdlib-only)

**Files:**
- `backend/internal/ics/ics.go` (new — lexer: unfolding, property parsing)
- `backend/internal/ics/parse.go` (new — VCALENDAR/VEVENT tree → typed events)
- `backend/internal/ics/rrule.go` (new — minimal RRULE expansion)
- `backend/internal/ics/ics_test.go`, `parse_test.go`, `rrule_test.go` (new)
- `backend/internal/ics/testdata/*.ics` (new — fixtures: Google "interesting calendar" export, Outlook export, all-day holidays feed, folded lines + UTF-8, weekly RRULE with BYDAY)

**Interfaces:**

```go
// Package ics implements the subset of RFC 5545 needed to consume public
// ICS subscription feeds: line unfolding (§3.1), text escaping (§3.3.11),
// DATE and DATE-TIME values with TZID/UTC (§3.3.4–5), VEVENT components,
// and a bounded RRULE expander for FREQ=DAILY/WEEKLY/MONTHLY/YEARLY with
// INTERVAL, COUNT, UNTIL, and (weekly) BYDAY. Unknown properties,
// components, and RRULE parts are skipped, never fatal.
package ics

type Calendar struct {
	Name   string // X-WR-CALNAME, else ""
	Events []Event
}

type Event struct {
	UID          string
	Summary      string
	Description  string
	Location     string
	Start        time.Time
	End          time.Time // DTEND, else DTSTART+DURATION, else Start (+1d when AllDay)
	AllDay       bool
	RRule        string // raw RRULE line, "" when absent
	Status       string // CONFIRMED|TENTATIVE|CANCELLED (upper-cased)
	LastModified time.Time
}

// Parse reads a full ICS document. It is tolerant: a malformed VEVENT is
// dropped (with its UID in the returned []error slice via errors.Join),
// never the whole feed.
func Parse(r io.Reader) (Calendar, error)

// Expand returns concrete occurrences of ev overlapping [from, to),
// expanding RRule when present (max 1000 occurrences as a safety valve).
// TZID zones resolve via time.LoadLocation; unknown zones fall back to UTC.
func Expand(ev Event, from, to time.Time) []Event
```

Parsing rules to encode in tests: CRLF and bare-LF both accepted; folded lines (leading space/tab) joined before parsing; `TEXT` unescaping (`\n`, `\,`, `\;`, `\\`); `DTSTART;VALUE=DATE:20260801` → all-day midnight UTC-date semantics; `DTSTART;TZID=Europe/Lisbon:...` via `time.LoadLocation`; trailing `Z` → UTC; `DURATION` ISO-8601 subset (`PnDTnHnMnS`, `PnW`); RRULE `UNTIL` in UTC compared against occurrence start; BYDAY only for `FREQ=WEEKLY` (e.g. `MO,WE,FR`); `COUNT` counts from DTSTART inclusive.

**Steps:**

- [ ] Write `ics_test.go` (unfolding, escaping, property/parameter split), `parse_test.go` (fixtures round-trip: event counts, exact times per zone, all-day flags, dropped-malformed behavior), `rrule_test.go` (daily+COUNT, weekly+BYDAY+UNTIL, monthly by start-day, yearly holiday, interval >1, 1000-cap, window clipping) — all red first: `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/ics/`.
- [ ] Implement `ics.go` → `parse.go` → `rrule.go` in that order, going green suite by suite.
- [ ] `bun run lint:go && bun run test:api` — green.
- [ ] Commit: `feat(backend): minimal stdlib RFC 5545 ICS parser with bounded RRULE expansion`.

---

### Task 15: Interesting-calendar subscriptions (ICS feeds)

**Files:**
- `backend/migrations/0012_calendar_subscriptions.sql` (new)
- `backend/internal/domain/subscription_calendar.go` (+ test) (new — `CalendarSubscription`; add `SubscriptionID *string` json field to `domain.Event`)
- `backend/internal/port/driven.go` (add `CalendarSubscriptionRepo`, `IcsFetcher`)
- `backend/internal/port/driving.go` (extend `CalendarService` with subscription CRUD)
- `backend/internal/adapter/out/postgres/subscription.go` (+ test) (new — subscriptions + `subscription_events` table)
- `backend/internal/adapter/out/icsfeed/fetcher.go` (+ test) (new — HTTP GET + `ics.Parse`, 1MB body cap, ETag/If-Modified-Since)
- `backend/internal/service/calendar.go` (+ test) — merge subscription events into `ListEvents` (read-only; never busy for `Availability`)
- `backend/internal/service/automation.go` (+ test) — hourly refresh pass
- `backend/internal/adapter/in/httpapi/subscriptions.go` (+ test), routes
- `packages/shared/src/types.ts` + `client.ts` (+ tests)
- `apps/web/app/(app)/settings/page.tsx` (Subscriptions manager: add URL, name, color, remove) + calendar page renders subscription events with their color and a read-only event dialog state

**Interfaces:**

```go
// domain
type CalendarSubscription struct {
	ID            string     `json:"id"`
	UserID        string     `json:"-"`
	URL           string     `json:"url"`  // https only
	Name          string     `json:"name"` // user label, else feed X-WR-CALNAME
	Color         string     `json:"color"`
	IsVisible     bool       `json:"isVisible"`
	LastFetchedAt *time.Time `json:"lastFetchedAt"`
	LastError     *string    `json:"lastError"`
	CreatedAt     time.Time  `json:"createdAt"`
}

// port/driven.go
type CalendarSubscriptionRepo interface {
	Create(ctx context.Context, s domain.CalendarSubscription) (domain.CalendarSubscription, error)
	GetByID(ctx context.Context, id string) (domain.CalendarSubscription, error)
	ListByUser(ctx context.Context, userID string) ([]domain.CalendarSubscription, error)
	// ListDue returns subscriptions not fetched since `since` (hourly cadence).
	ListDue(ctx context.Context, since time.Time) ([]domain.CalendarSubscription, error)
	Update(ctx context.Context, s domain.CalendarSubscription) error
	Delete(ctx context.Context, id string) error
	// ReplaceEvents atomically swaps the expanded occurrence set for a
	// subscription (12-month horizon), preserving nothing — feeds own truth.
	ReplaceEvents(ctx context.Context, subscriptionID string, events []domain.Event) error
	// ListEventsInRange mirrors EventRepo.ListInRange for subscription events.
	ListEventsInRange(ctx context.Context, userID string, from, to time.Time) ([]domain.Event, error)
}

// IcsFetcher retrieves and parses a feed. notModified is true when the
// server honored the cached validator (etag) and events must be kept.
type IcsFetcher interface {
	Fetch(ctx context.Context, url, etag string) (cal ics.Calendar, newEtag string, notModified bool, err error)
}
```

Migration: `calendar_subscriptions(id TEXT PK, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, url TEXT NOT NULL, name TEXT NOT NULL, color TEXT NOT NULL DEFAULT '#8b5cf6', is_visible BOOLEAN NOT NULL DEFAULT TRUE, etag TEXT NOT NULL DEFAULT '', last_fetched_at TIMESTAMPTZ, last_error TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(user_id, url))` + `subscription_events(id TEXT PK, subscription_id TEXT NOT NULL REFERENCES calendar_subscriptions(id) ON DELETE CASCADE, uid TEXT NOT NULL, title TEXT NOT NULL, description TEXT, location TEXT, starts_at TIMESTAMPTZ NOT NULL, ends_at TIMESTAMPTZ NOT NULL, all_day BOOLEAN NOT NULL DEFAULT FALSE)` + range index. Routes: `authed("GET|POST /v1/calendar-subscriptions", ...)`, `authed("PATCH|DELETE /v1/calendar-subscriptions/{id}", ...)`; POST validates https + fetches once synchronously so the user sees immediate events or a clear error. Refresh pass in `RunAutomation`: `ListDue(now-1h)` → fetch → `ics.Expand` each event over `[now-1mo, now+12mo)` → `ReplaceEvents`; failures set `LastError` without wiping the previous good set. `CalendarService.ListEvents` merges visible subscription events (mapped to `domain.Event` with `SubscriptionID` set, `Status: confirmed`); `Availability` ignores them (informational feeds must not block booking).

**Steps:**

- [ ] Domain validation test (https-only, name defaulting); red → implement → green.
- [ ] Repo test (CRUD, unique user+url, ReplaceEvents swap, range query, cascade); red → migration + repo → green.
- [ ] Fetcher test (httptest: 200 parses, 304 → notModified, 1MB cap, non-ics content error); red → `icsfeed/fetcher.go` → green.
- [ ] Service tests: create-fetches-immediately (fake fetcher), ListEvents merge respects `IsVisible` and range, availability unaffected (extend existing `TestAvailability`), refresh pass expands + replaces + records errors. Red → implement → green.
- [ ] Handler tests (POST happy/invalid URL/fetch-fail 422 with message, PATCH visibility/color, DELETE); red → implement + routes → green.
- [ ] Shared types/client + tests; web settings manager + grid rendering (distinct dashed border, read-only dialog) + vitest; demo fixture (a "Holidays" feed) behind `isDemo()`.
- [ ] `bun run lint && bun run test:api && bun run test:web && bun run test:shared` — green.
- [ ] Commit: `feat(backend,web): ICS interesting-calendar subscriptions with hourly refresh`.

---

### Task 16: CrmProvider port + HubSpot adapter — contact context + email logging

**Files:**
- `backend/internal/domain/crm.go` (new — `CrmContact`, `CrmDeal`, `CrmContext`, `CrmEmailLog`)
- `backend/internal/port/driven.go` (add `CrmProvider`)
- `backend/internal/port/driving.go` (add `CrmService`)
- `backend/internal/adapter/out/hubspot/client.go` (+ test) (new)
- `backend/internal/service/crm.go` (+ test) (new)
- `backend/internal/adapter/in/httpapi/crm.go` (+ test), routes
- `backend/cmd/api/main.go` wiring (behind `cfg.HubSpot.ClientID != ""`; OAuth connect already ships from Task 9)
- `packages/shared/src/types.ts` + `client.ts` (+ tests)
- `apps/web/components/app/contact-pane.tsx` (landed in M2.5 — add a CRM section; if the M2.5 path differs, follow it) + test
- `apps/web/components/app/thread-view.tsx` ("Log to HubSpot" action on a message overflow menu)

**Interfaces:**

```go
// domain/crm.go
type CrmContact struct {
	ID       string  `json:"id"`
	Email    string  `json:"email"`
	Name     string  `json:"name"`
	Company  string  `json:"company"`
	Title    string  `json:"title"`
	Phone    string  `json:"phone"`
	Owner    string  `json:"owner"`
	VendorURL string `json:"vendorUrl"` // deep link into the CRM record
}

type CrmDeal struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Stage    string     `json:"stage"`
	Amount   *float64   `json:"amount"`
	CloseDate *time.Time `json:"closeDate"`
	VendorURL string    `json:"vendorUrl"`
}

// CrmContext is everything the contact pane shows for one email address.
type CrmContext struct {
	Vendor  IntegrationVendor `json:"vendor"`
	Contact *CrmContact       `json:"contact"` // nil = not in CRM
	Deals   []CrmDeal         `json:"deals"`
}

type CrmEmailLog struct {
	ContactEmail string    `json:"contactEmail"`
	Subject      string    `json:"subject"`
	BodyText     string    `json:"bodyText"`
	SentAt       time.Time `json:"sentAt"`
	Direction    string    `json:"direction"` // inbound|outbound
}

// port/driven.go
type CrmProvider interface {
	Vendor() domain.IntegrationVendor
	// ContactContext resolves a contact by email with associated open deals;
	// a missing contact returns CrmContext{Contact: nil}, not an error.
	ContactContext(ctx context.Context, accessToken, email string) (domain.CrmContext, error)
	// LogEmail records an email engagement on the contact's timeline.
	LogEmail(ctx context.Context, accessToken string, log domain.CrmEmailLog) error
}

// port/driving.go
type CrmService interface {
	// ContactContext returns context from the user's connected CRM vendors
	// (empty slice when none connected — the pane hides the section).
	ContactContext(ctx context.Context, userID, email string) ([]domain.CrmContext, error)
	LogEmail(ctx context.Context, userID string, log domain.CrmEmailLog) error
}
```

Routes: `authed("GET /v1/crm/context", ...)` (`?email=`), `authed("POST /v1/crm/log", ...)`. HubSpot adapter: contacts search `POST /crm/v3/objects/contacts/search` (filter `email EQ`), associated deals via `GET /crm/v3/objects/contacts/{id}/associations/deals` + batch read; email logging via `POST /crm/v3/objects/emails` + association to the contact; token refresh through the Task 9 `OAuthGateway` when a 401 arrives (service refreshes via `IntegrationRepo` tokens, one retry). Service caches context per (user,email) 5 min in-memory. Web: CRM card in the contact pane (name/company/title, deal list with stage badges, "Open in HubSpot" links) rendered only when `listIntegrations` shows a connected CRM; "Log to HubSpot" toast-confirmed action.

**Steps:**

- [ ] Adapter test (httptest fake HubSpot: search hit/miss mapping, deals join, log-email request body + association, 401 error passthrough); red → implement → green (`go test ./internal/adapter/out/hubspot/`).
- [ ] Service test (no connections → empty, token refresh on 401 retries once, cache hit, entitlement); handler tests (email param validation, 501 when vendor unconfigured but connection exists is impossible — assert 200 empty). Red → implement → green.
- [ ] Shared client (`getCrmContext`, `logCrmEmail`) + tests; web pane section + log action + vitest (renders contact/deals from mock, hidden without connection); demo CRM fixture behind `isDemo()`.
- [ ] `bun run lint && bun run test:api && bun run test:web && bun run test:shared` — green.
- [ ] Commit: `feat(backend,web): HubSpot CrmProvider — contact context in the pane and email logging`.

---

### Task 17: Time analytics / insights panel

**Files:**
- `backend/internal/domain/insights.go` (new — `TimeInsights`, `PersonStat`, `DayStat`)
- `backend/internal/port/driving.go` (add `InsightsService`)
- `backend/internal/service/insights.go` (+ test) (new — pure aggregation over `EventRepo.ListInRange` + `ManagedEventRepo` + `TaskRepo`)
- `backend/internal/adapter/in/httpapi/insights.go` (+ test), route `authed("GET /v1/insights/time", ...)` (`?from&to`)
- `backend/cmd/api/main.go` wiring
- `packages/shared/src/types.ts` + `client.ts` (+ tests)
- `apps/web/components/app/insights-panel.tsx` (+ test) (new), mounted from `calendar/page.tsx` (palette command "Time insights", shortcut `⇧I`)

**Interfaces:**

```go
// domain/insights.go
type PersonStat struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Meetings int    `json:"meetings"`
	Minutes  int    `json:"minutes"`
}

type DayStat struct {
	Date           string `json:"date"` // YYYY-MM-DD
	MeetingMinutes int    `json:"meetingMinutes"`
	FocusMinutes   int    `json:"focusMinutes"`
}

type TimeInsights struct {
	From            time.Time    `json:"from"`
	To              time.Time    `json:"to"`
	MeetingMinutes  int          `json:"meetingMinutes"`  // events with >=2 attendees, not declined/cancelled
	FocusMinutes    int          `json:"focusMinutes"`    // managed focus + IsFocusTitle events
	TaskMinutes     int          `json:"taskMinutes"`     // scheduled task blocks
	MeetingCount    int          `json:"meetingCount"`
	FocusGoalMinutes int         `json:"focusGoalMinutes"`// weekly goal scaled to the range
	TopPeople       []PersonStat `json:"topPeople"`       // top 5 by minutes, self excluded
	ByDay           []DayStat    `json:"byDay"`
}

// port/driving.go
type InsightsService interface {
	TimeInsights(ctx context.Context, userID string, from, to time.Time) (domain.TimeInsights, error)
}
```

Classification rules (all table-tested): declined-by-user, cancelled, all-day, buffers (`ManagedBuffer`), and travel blocks are excluded from meeting time; `ManagedFocus` events and user events titled like focus (`focus` substring, case-insensitive) count as focus; overlap clipped to `[from,to)`; range capped at 92 days. Web panel: right-side sheet with meeting-vs-focus split bar, per-day mini bars, top-people list with avatars — built per the dataviz guidance already used in the app, plain divs/SVG, no chart lib.

**Steps:**

- [ ] Service tests: fixture week (2 meetings, 1 focus, 1 buffer, 1 task block, 1 declined) → exact minute totals, top-people ordering + self-exclusion, range clipping, 92-day cap error. Red → implement → green (`go test ./internal/service/ -run TestTimeInsights`).
- [ ] Handler test + route; shared client (`getTimeInsights`) + tests.
- [ ] Web panel + vitest (renders totals from demo insights, keyboard open/close); palette + shortcut registration; demo data behind `isDemo()`.
- [ ] `bun run lint && bun run test:api && bun run test:web && bun run test:shared` — green.
- [ ] Commit: `feat(backend,web): time insights — meeting hours, top people, meetings-vs-focus`.

---

### Task 18: Email-to-event drag (web)

**Files:**
- `apps/web/app/(app)/mail/page.tsx` (thread rows `draggable`, payload `application/x-calendium-thread` = `{threadId, subject, participants[]}`)
- `apps/web/app/(app)/calendar/page.tsx` (grid day columns accept the thread payload → compute drop time → open `EventDialog` prefilled)
- `apps/web/components/app/event-dialog.tsx` (accept `prefill` prop: title, attendees, description with a `mailto`-style thread reference line)
- `apps/web/components/app/event-dialog.test.tsx`, new `apps/web/lib/thread-drag.ts` (+ test — payload encode/decode, drop-time math shared with task drag from Task 3)
- `apps/web/e2e/email-to-event.spec.ts` (new, demo mode)

**Interfaces:** `encodeThreadDrag(t: {threadId; subject; participants: {email; name?}[]}): string` / `decodeThreadDrag(dt: DataTransfer): ThreadDragPayload | null` in `lib/thread-drag.ts`; `EventDialog` gains `prefill?: Partial<EventInput> & { sourceThreadId?: string }`. The calendar peek (M2.2's beside-inbox mini calendar) accepts the same drop, so a drag never requires leaving mail; drop on an all-day header prefils an all-day event.

**Steps:**

- [ ] Write `thread-drag.test.ts` (encode/decode round-trip, malformed payload → null, drop-time snapping to 15min) and extend `event-dialog.test.tsx` (prefill renders title/attendees/description). Red → implement `thread-drag.ts` + dialog prop → green (`bun run --cwd apps/web test thread-drag event-dialog`).
- [ ] Wire drag source on thread rows (subject + participants from the already-loaded thread list data) and drop targets on grid + peek; visual drop indicator reuses the task-drag affordance from Task 3.
- [ ] Write `e2e/email-to-event.spec.ts`: demo mode — drag first thread onto Thursday 14:00 → dialog opens prefilled → save → event appears on the grid.
- [ ] `bun run lint:js && bun run test:web` — green.
- [ ] Commit: `feat(web): drag an email thread onto the calendar to create a prefilled event`.

---

### Task 19: Concierge onboarding — in-app guided tour + shortcut coach

**Files:**
- `apps/web/components/app/onboarding-tour.tsx` (+ test) (new — step engine: anchored popovers over `data-tour="..."` targets)
- `apps/web/lib/tour-steps.ts` (new — declarative step list) + `apps/web/lib/tour-state.ts` (+ test) (localStorage persistence, `calendium.tour.v1`)
- `apps/web/app/(app)/layout.tsx` (mount tour; auto-start on first authenticated visit)
- `apps/web/app/(app)/mail/page.tsx`, `calendar/page.tsx`, `components/app/command-palette.tsx` (add `data-tour` anchors; palette command "Restart tour")
- `apps/web/lib/shortcuts.ts` (shortcut-coach hook: after a mouse-performed action that has a shortcut, show a one-line toast "Next time press E" — max once per action per session, mute setting)
- `apps/web/e2e/onboarding.spec.ts` (new)

**Interfaces:** `tourSteps: {id; target: string; title; body; page: "mail"|"calendar"|"any"; shortcut?: string}[]` — 8 steps: split inbox → j/k/e triage → palette ⌘K → snooze → calendar T → task rail → share availability → settings/integrations. `useTourState()` returns `{active, stepIndex, start, next, skip, done, restart}`; state is client-local only (no backend — the human 1:1 concierge session is a business process explicitly out of scope; this task ships only the in-app tour and coach).

**Steps:**

- [ ] Write `tour-state.test.ts` (fresh user auto-eligible, done persists across reload, restart resets) and `onboarding-tour.test.tsx` (renders step anchored to target, next/skip advance, missing anchor skips step). Red → implement engine + steps → green.
- [ ] Add anchors + palette command + shortcut-coach toast wiring (extend `shortcuts.test.ts` for the coach's once-per-session rule).
- [ ] `e2e/onboarding.spec.ts`: first demo visit shows step 1 → walk two steps → skip → reload shows nothing → palette "Restart tour" brings it back.
- [ ] `bun run lint:js && bun run test:web` — green.
- [ ] Commit: `feat(web): in-app concierge tour and shortcut coach`.

---

## Deferred delight extras (explicitly out of M2.8)

| Deferred item | Why |
| --- | --- |
| Spotify listening history on the timeline (Amie) | Pure delight, zero scheduling value; needs another OAuth vendor + polling quota for a decorative feature. Revisit post-M2 if retention data asks for it. |
| Built-in Pomodoro timer (Amie) | Client-only timer is trivial but competes with FocusGuard's model of focus; ship after we learn how focus blocks are actually used. |
| Scheduled FaceTime calls (Apple Calendar) | Requires Apple-private link generation; not reachable from a web/Go stack. Mobile-only consideration for a later Apple-platform pass. |
| Deep OS surfaces: widgets, watch, lock screen (Apple) | Expo widget/watch tooling is heavy native work per platform; belongs to a dedicated mobile-platform milestone, not the calendar long tail. |
| Salesforce + Pipedrive CRM adapters | `CrmProvider` port ships with HubSpot proving it; each extra vendor is a mechanical adapter + OAuth config. Add on customer demand — no design risk retired by doing them now. |
| Things 3 / Notion / Linear todo adapters | Same reasoning via `TodoProvider`; Things 3 additionally has no public HTTP API (AppleScript/URL-scheme only), so it may need a desktop-side bridge — out of scope here. |
| Auto-events from Gmail content (flights/hotels) | Superseded by M2.3's Instant Event AI path; a heuristic parser here would duplicate that pipeline. |
| Google/Apple Maps adapters with live traffic + transit | `MapsProvider` port ships with the keyless Nominatim/OSRM adapter; paid-key adapters are a config + adapter drop-in later. |
| AI task slotting (Amie "best slot for this todo") | Depends on the M2.3 AI suite's scheduling models; the task domain + `planFocusWeek` gap machinery built here is exactly what it will call. |

## Execution order & dependency notes

1→2→3 (tasks vertical) → 4 (notes) → 5 (prefs — required by 6–8, 12, 13) → 6→7→8 (automation engine; 7 contains the `RSVP` signature migration) → 9 (integration OAuth — required by 10, 16) → 10 (Todoist) → 11 (maps — required by 12) → 12 (travel) → 13 (weather) → 14 (ics — required by 15) → 15 (subscriptions) → 16 (CRM) → 17 (insights — reads managed events from 6/8) → 18 (email drag — reuses 3's drag math) → 19 (tour — anchors features from everything above). Tasks 13, 14, 16 are independent of their neighbors and may run in parallel worktrees if desired; everything else is ordered by real dependencies.

Phase-done criteria (spec §Success criteria): every feature works on live data on its feature-map platforms, all suites green (`bun run test`), lint gates green (`bun run lint`), e2e green in CI, sub-100ms optimistic interactions on task check-off/drag verified against the M2.1 perf harness.

