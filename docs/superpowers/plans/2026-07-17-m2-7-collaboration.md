# M2.7 — Collaboration & Teams Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
> **Migration numbering:** the eight M2 phase plans were authored in parallel, so migration filenames here are provisional — at execution time use the next free number in `backend/migrations/` and update references in the affected task.

**Goal:** Add multi-user collaboration to a codebase that is single-user everywhere today (verified: `backend/internal/domain/` has no team/org concept; migrations 0001–0004 model users, connected_accounts, and per-user mail/calendar mirrors only). Deliver: teams + membership + email invitations; shared conversations via tokenized live links; team comments with @mentions; team read statuses / reply indicators; team snippets; shared calendars with granular permissions; team availability + Find Time inline; team scheduling links (on M2.4 booking links); and EA delegation mode with an audit log.

**Architecture:** Hexagonal, stdlib-only Go backend. New domain types in `internal/domain/{team,collab,delegation}.go`; new driven ports (`TeamRepo`, `TeamInvitationRepo`, `ThreadShareRepo`, `CommentRepo`, `TeamThreadActivityRepo`, `CalendarShareRepo`, `DelegationRepo`, `AuditRepo`, `EventBus`) in `internal/port/driven.go`; new driving ports (`TeamService`, `CollabService`, `DelegationService`) in `internal/port/driving.go`; services in `internal/service/`; Postgres adapters in `internal/adapter/out/postgres/`; routes on the existing `httpapi` ServeMux. Clients consume everything through `@calendium/shared` `ApiClient`.

Three architecture decisions are **encoded up front** and referenced by tasks:

1. **Authorization lives in the service layer**, exactly where `ownedAccount`/`ownedThread` live today (`internal/service/service.go`). New helpers in `internal/service/collab_helpers.go`: `membership(ctx, teams, userID, teamID)` returns the caller's `domain.TeamMember` or `domain.ErrNotFound` (non-members cannot distinguish a team from a missing one — same invariant as foreign accounts today), and `requireRole(m domain.TeamMember, min domain.TeamRole)` returns the **new sentinel `domain.ErrForbidden` → 403** when the caller is a member but lacks the role. HTTP handlers and repos never make authorization decisions.
2. **Realtime is SSE, not polling.** A new in-process `port.EventBus` (adapter `internal/adapter/out/eventbus`) fans `CollabEvent`s (comments, read-status changes, shared-view message updates) out to `GET /v1/collab/stream` and `GET /v1/shared/threads/{token}/stream`, implemented with stdlib `http.Flusher` + `text/event-stream`. This fits the stdlib constraint (no websocket dep), works through proxies, and beats polling on latency and load. Web clients consume it with `fetch` + `ReadableStream` (not `EventSource`) so the `Authorization` header works and no token ever appears in a URL. Single-API-process deployment is assumed today; the documented scale-out path is Postgres `LISTEN/NOTIFY` behind the same `EventBus` port (pgx is already the driver) — out of scope here.
3. **Privacy default: nothing is shared without an explicit action.** Creating or joining a team shares zero data. Every surface has its own opt-in: thread shares are created per thread; comments are visible only to the team they were posted to; read-status/reply sharing is a per-member-per-team toggle (`team_members.share_read_statuses`, default **false**); calendars are shared per calendar with an explicit permission grant; team availability only includes members who granted at least free/busy to that team; delegation requires the principal to create the grant and the assistant to accept it.

**Team billing is flagged and out of scope:** billing stays per-user $50/yr (docs/payments.md). Teams do not change entitlement — every member needs their own active subscription, and all new gated use-cases call the existing `entitlement.require` on the *acting* user. A future team plan (seat-based Stripe subscription, `quantity` on the price, owner-pays) would touch `subscriptions`, checkout, and webhooks; none of that is in this phase.

**Tech Stack:** Go 1.26 stdlib (net/http ServeMux 1.22 routing, crypto/rand, crypto/sha256) + pgx; Postgres migrations via `internal/migrate` (next free number: **0005**); testcontainers-go for repo integration tests (existing pattern in `postgres_test.go`); Next.js 15 + React 19 + TanStack Query + shadcn new-york on web; `@calendium/shared` types + `ApiClient`; Vitest.

## Global Constraints

- Mock/sample data may be served ONLY behind explicit demo flags (`NEXT_PUBLIC_DEMO_MODE === 'true'` on web via `apps/web/lib/demo.ts`, `isDemoMode()` on mobile). Outside demo mode errors propagate — never fabricate success.
- Backend is stdlib-only (+pgx, +testcontainers test-scope). No new runtime dependencies.
- Explicit-share-only privacy (decision 3 above) is an invariant every task's tests must assert: the "member of team but no grant" case must always return empty/404, never data.
- Ownership/authz invariants: foreign or non-member resources → `domain.ErrNotFound` (never 403); member-with-insufficient-role → `domain.ErrForbidden` (403). Compare with `errors.Is`, never string-match.
- Time is injected via `port.Clock`; tokens via the existing `randomToken` helper; IDs via `newID()`.
- Test gates per task: `cd backend && go test ./...` green; web tasks: `bun run --cwd apps/web test` green; lint gates `bun run lint:js` (Biome) and `cd backend && golangci-lint run ./...` before every commit (lefthook enforces on push).
- Conventional commits: `feat(backend): …`, `feat(web): …`, `test(backend): …`, `docs: …`.
- Keep `backend/internal/domain` ↔ `packages/shared/src/types.ts` ↔ REST contract in sync (the tri-sync rule documented in `domain/errors.go`).
- **M2.4 dependency:** Task 14 (team scheduling links) extends the `booking_links` table and `BookingService` delivered by plan `2026-07-17-m2-4-scheduling-booking.md`. If M2.4 has not merged when this phase executes, Task 14 is blocked and must be re-sequenced last or deferred — every other task here is independent of M2.4.
- **Provider-level sharing stays out of scope** (Tasks 12–13): Google Calendar ACLs and Microsoft Graph calendar permissions have divergent semantics (roles, defaults, propagation) and would grant access *outside* Calendium that we cannot revoke or audit uniformly. The sharing layer is local: reads are served from the Postgres mirror; edit-permission writes go through the **owner's** provider tokens and are audit-logged. This is noted in code comments and docs.

---

### Task 1: Team domain model (foundational — real code)

The core new backend infrastructure. Pure domain types + invariants, stdlib imports only, mirroring the style of `domain/mail.go`.

**Files:**
- Create: `backend/internal/domain/team.go`
- Create: `backend/internal/domain/team_test.go`
- Modify: `backend/internal/domain/errors.go` (add `ErrForbidden`)
- Modify: `backend/internal/adapter/in/httpapi/codec.go` (map `ErrForbidden` → 403)
- Modify: `backend/internal/adapter/in/httpapi/codec_test.go`

**Interfaces:** `backend/internal/domain/team.go` is written verbatim as:

```go
package domain

import (
	"fmt"
	"strings"
	"time"
)

// TeamRole orders member privileges: owner > admin > member.
type TeamRole string

const (
	TeamRoleOwner  TeamRole = "owner"
	TeamRoleAdmin  TeamRole = "admin"
	TeamRoleMember TeamRole = "member"
)

// ParseTeamRole validates a role path/body parameter.
func ParseTeamRole(s string) (TeamRole, error) {
	switch TeamRole(s) {
	case TeamRoleOwner, TeamRoleAdmin, TeamRoleMember:
		return TeamRole(s), nil
	}
	return "", fmt.Errorf("%w: unknown team role %q", ErrValidation, s)
}

// rank returns the privilege ordering used by AtLeast.
func (r TeamRole) rank() int {
	switch r {
	case TeamRoleOwner:
		return 3
	case TeamRoleAdmin:
		return 2
	case TeamRoleMember:
		return 1
	}
	return 0
}

// AtLeast reports whether r grants the privileges of min.
func (r TeamRole) AtLeast(min TeamRole) bool { return r.rank() >= min.rank() }

// Team is a collaboration group. Membership grants NOTHING by itself: every
// collaborative surface (shares, comments, read statuses, calendars,
// availability) requires its own explicit opt-in (privacy default).
type Team struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedBy string    `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
}

// ValidateTeamName enforces the team-name invariant shared by create/rename.
func ValidateTeamName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 120 {
		return "", fmt.Errorf("%w: team name must be 1-120 characters", ErrValidation)
	}
	return name, nil
}

// TeamMember is a user's membership in a team.
type TeamMember struct {
	TeamID string   `json:"teamId"`
	UserID string   `json:"userId"`
	Role   TeamRole `json:"role"`
	// ShareReadStatuses opts this member's thread open/reply activity into
	// the team's read-status indicators. Privacy default: false — joining a
	// team never exposes activity without this explicit toggle.
	ShareReadStatuses bool      `json:"shareReadStatuses"`
	JoinedAt          time.Time `json:"joinedAt"`
}

// InvitationStatus is the lifecycle of an email invitation.
type InvitationStatus string

const (
	InvitePending  InvitationStatus = "pending"
	InviteAccepted InvitationStatus = "accepted"
	InviteRevoked  InvitationStatus = "revoked"
	InviteExpired  InvitationStatus = "expired"
)

// TeamInvitation is an email invitation to join a team. The raw token is
// shown once in the invite link; only its SHA-256 hash is stored.
type TeamInvitation struct {
	ID        string           `json:"id"`
	TeamID    string           `json:"teamId"`
	Email     string           `json:"email"`
	Role      TeamRole         `json:"role"`
	InvitedBy string           `json:"invitedBy"`
	Status    InvitationStatus `json:"status"`
	TokenHash string           `json:"-"`
	ExpiresAt time.Time        `json:"expiresAt"`
	CreatedAt time.Time        `json:"createdAt"`
}

// Usable reports whether the invitation can still be accepted at now.
func (i TeamInvitation) Usable(now time.Time) bool {
	return i.Status == InvitePending && now.Before(i.ExpiresAt)
}
```

And `errors.go` gains (with the doc comment updated to list the mapping):

```go
// ErrForbidden marks a caller who is authenticated and known (e.g. a team
// member) but lacks the required role/permission. Mapped to 403. Non-members
// keep getting ErrNotFound so resource existence is never leaked.
ErrForbidden = errors.New("forbidden")
```

- [ ] **Step 1: Write the failing tests** — `backend/internal/domain/team_test.go` (`package domain`, table-driven like `domain_test.go`): `ParseTeamRole` accepts owner/admin/member and rejects `""`/`"superadmin"` with `errors.Is(err, ErrValidation)`; `AtLeast` full matrix (owner≥owner/admin/member, admin≥admin/member but !≥owner, member only ≥member, unknown role rank 0 fails all); `ValidateTeamName` trims, rejects empty/whitespace-only/121-char names; `Usable` true only for pending+unexpired (subtests: pending future=true, pending past=false, accepted future=false, revoked future=false).
- [ ] **Step 2: Run to verify failure** — `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/domain/ -run 'TeamRole|TeamName|Invitation'`. Expected: FAIL (compile error, types undefined).
- [ ] **Step 3: Create `backend/internal/domain/team.go`** with the code above; add `ErrForbidden` to `errors.go`.
- [ ] **Step 4: Map 403** — in `httpapi/codec.go`, add `case errors.Is(err, domain.ErrForbidden): status = http.StatusForbidden` alongside the existing sentinel mapping; extend `codec_test.go` with a 403 case.
- [ ] **Step 5: Run tests** — `cd backend && go test ./internal/domain/ ./internal/adapter/in/httpapi/`. Expected: PASS.
- [ ] **Step 6: Lint + commit** — `cd backend && golangci-lint run ./...`; `git add -A && git commit -m "feat(backend): team domain model with roles, invitations, ErrForbidden"`.

---

### Task 2: Teams migration + repo ports (foundational — real code)

**Files:**
- Create: `backend/migrations/0005_teams.sql`
- Modify: `backend/internal/port/driven.go` (append `TeamRepo`, `TeamInvitationRepo`)

**Interfaces:** Migration `0005_teams.sql` verbatim:

```sql
-- 0005_teams.sql — Teams, membership, email invitations (M2.7).
-- Membership grants nothing by itself; collaborative surfaces each have
-- explicit opt-ins (privacy default: share nothing).

CREATE TABLE teams (
    id         text PRIMARY KEY,
    name       text NOT NULL,
    created_by text NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE team_members (
    team_id             text NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id             text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role                text NOT NULL DEFAULT 'member', -- 'owner' | 'admin' | 'member'
    -- Explicit opt-in for team read-status / reply indicators.
    share_read_statuses boolean NOT NULL DEFAULT false,
    joined_at           timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, user_id)
);
CREATE INDEX team_members_user_idx ON team_members (user_id);

CREATE TABLE team_invitations (
    id         text PRIMARY KEY,
    team_id    text NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    email      text NOT NULL,
    role       text NOT NULL DEFAULT 'member',
    invited_by text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status     text NOT NULL DEFAULT 'pending', -- pending|accepted|revoked|expired
    -- SHA-256 hex of the one-time invite-link token; raw token never stored.
    token_hash text NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX team_invitations_team_idx ON team_invitations (team_id);
-- One live invitation per address per team.
CREATE UNIQUE INDEX team_invitations_pending_idx
    ON team_invitations (team_id, lower(email)) WHERE status = 'pending';
```

Ports appended to `driven.go` verbatim:

```go
// TeamRepo persists teams and memberships.
type TeamRepo interface {
	// Create inserts the team and its owner membership atomically.
	Create(ctx context.Context, t domain.Team, owner domain.TeamMember) (domain.Team, error)
	GetByID(ctx context.Context, id string) (domain.Team, error)
	ListByUser(ctx context.Context, userID string) ([]domain.Team, error)
	Update(ctx context.Context, t domain.Team) error
	Delete(ctx context.Context, id string) error
	// GetMember returns domain.ErrNotFound for non-members — the authz
	// primitive behind service-layer membership checks.
	GetMember(ctx context.Context, teamID, userID string) (domain.TeamMember, error)
	ListMembers(ctx context.Context, teamID string) ([]domain.TeamMember, error)
	// UpsertMember inserts or updates role/share_read_statuses.
	UpsertMember(ctx context.Context, m domain.TeamMember) error
	RemoveMember(ctx context.Context, teamID, userID string) error
	// CountByRole supports the last-owner invariant.
	CountByRole(ctx context.Context, teamID string, role domain.TeamRole) (int, error)
}

// TeamInvitationRepo persists email invitations (token stored hashed).
type TeamInvitationRepo interface {
	Create(ctx context.Context, inv domain.TeamInvitation) (domain.TeamInvitation, error)
	GetByID(ctx context.Context, id string) (domain.TeamInvitation, error)
	GetByTokenHash(ctx context.Context, tokenHash string) (domain.TeamInvitation, error)
	ListByTeam(ctx context.Context, teamID string) ([]domain.TeamInvitation, error)
	Update(ctx context.Context, inv domain.TeamInvitation) error
}
```

- [ ] **Step 1: Add the migration file** exactly as above (embedded automatically via `migrations/embed.go`).
- [ ] **Step 2: Append the two port interfaces** to `driven.go` under a new `// --- Collaboration (M2.7)` banner.
- [ ] **Step 3: Compile** — `cd backend && go build ./...`. Expected: PASS (interfaces have no implementors yet — that's fine).
- [ ] **Step 4: Commit** — `git commit -am "feat(backend): teams schema and repo ports"`.

---

### Task 3: Postgres team repos + integration tests

**Files:**
- Create: `backend/internal/adapter/out/postgres/team.go` (both repos; follow `snippets`/`account.go` style: `Store`-attached structs, `$n` params, `domain.ErrNotFound` on no rows)
- Create: `backend/internal/adapter/out/postgres/team_test.go` (testcontainers harness from `postgres_test.go`)

**Interfaces:** Produces `func NewTeamRepo(s *Store) *TeamRepo` and `func NewTeamInvitationRepo(s *Store) *TeamInvitationRepo` with compile-time assertions `var _ port.TeamRepo = (*TeamRepo)(nil)` / `var _ port.TeamInvitationRepo = (*TeamInvitationRepo)(nil)`. `TeamRepo.Create` wraps team-insert + owner-membership-insert in one transaction via the store's existing tx helper.

- [ ] **Step 1: Write failing integration tests** (require Docker, same skip-guard as existing repo tests). Required subtests: create-team-inserts-owner-membership atomically; `GetMember` non-member → `domain.ErrNotFound`; `ListByUser` returns only the user's teams; `UpsertMember` updates role and `share_read_statuses` idempotently; `RemoveMember` then `GetMember` → not found; `CountByRole` reflects promotions/demotions; invitation `GetByTokenHash` round-trip and unknown hash → not found; the `team_invitations_pending_idx` partial unique index rejects a second pending invite for the same (team, email) — assert the driver error surfaces (repo maps unique-violation to `domain.ErrConflict`, matching existing repo conventions); cascade: deleting a team removes members + invitations.
- [ ] **Step 2: Run to verify failure** — `cd backend && go test ./internal/adapter/out/postgres/ -run TestTeam`. Expected: FAIL (types undefined).
- [ ] **Step 3: Implement `team.go`.**
- [ ] **Step 4: Run** — same command. Expected: PASS. Then full suite `go test ./...`.
- [ ] **Step 5: Lint + commit** — `feat(backend): postgres team and invitation repos`.

---

### Task 4: TeamService (foundational — real interface code)

Membership lifecycle + invitation email through the inviter's own send pipeline. There is **no transactional-email vendor in the stack** (stdlib constraint); the invitation email is sent as an `OutgoingMessage` through `port.MailProvider.Send` using the inviter's first active `ConnectedAccount` and the existing `tokenSource` — the same pipeline drafts use. No connected account → `ErrValidation` with a clear message.

**Files:**
- Modify: `backend/internal/port/driving.go` (append `TeamService`)
- Create: `backend/internal/service/team.go`
- Create: `backend/internal/service/collab_helpers.go` (`membership`, `requireRole`)
- Create: `backend/internal/service/team_test.go`
- Modify: `backend/internal/service/fakes_test.go` (add `fakeTeamRepo`, `fakeTeamInvitationRepo`)

**Interfaces:** Appended to `driving.go` verbatim:

```go
// TeamInput is the create/rename team payload.
type TeamInput struct {
	Name string `json:"name"`
}

// TeamService manages teams, membership, and email invitations. All
// role/authorization checks live here (service layer), never in adapters:
// non-members get ErrNotFound, under-privileged members get ErrForbidden.
type TeamService interface {
	Create(ctx context.Context, userID string, in TeamInput) (domain.Team, error)
	List(ctx context.Context, userID string) ([]domain.Team, error)
	// Get returns the team and its members; callers must be members.
	Get(ctx context.Context, userID, teamID string) (domain.Team, []domain.TeamMember, error)
	Rename(ctx context.Context, userID, teamID, name string) (domain.Team, error)
	// Delete requires the owner role and removes the team and all
	// memberships, shares, and comments (DB cascades).
	Delete(ctx context.Context, userID, teamID string) error
	// SetMemberRole requires admin+; only owners may grant/revoke owner.
	// Demoting or removing the last owner returns ErrConflict.
	SetMemberRole(ctx context.Context, userID, teamID, memberUserID string, role domain.TeamRole) (domain.TeamMember, error)
	// SetShareReadStatuses toggles the CALLER's own read-status opt-in.
	SetShareReadStatuses(ctx context.Context, userID, teamID string, share bool) (domain.TeamMember, error)
	// RemoveMember: admins remove members, owners remove anyone; any member
	// may remove themselves (leave), except the last owner (ErrConflict).
	RemoveMember(ctx context.Context, userID, teamID, memberUserID string) error
	// Invite (admin+) creates a pending invitation and emails the invite
	// link via the inviter's own connected account send pipeline.
	Invite(ctx context.Context, userID, teamID, email string, role domain.TeamRole) (domain.TeamInvitation, error)
	ListInvitations(ctx context.Context, userID, teamID string) ([]domain.TeamInvitation, error)
	RevokeInvitation(ctx context.Context, userID, teamID, invitationID string) error
	// AcceptInvitation redeems a raw invite token for the AUTHENTICATED
	// user. The token is hashed and looked up; expired/revoked/used tokens
	// return ErrNotFound (no oracle).
	AcceptInvitation(ctx context.Context, userID, token string) (domain.Team, error)
}
```

`collab_helpers.go` core (verbatim):

```go
// membership loads the caller's membership; non-members are
// indistinguishable from a missing team (404, never 403).
func membership(ctx context.Context, teams port.TeamRepo, userID, teamID string) (domain.TeamMember, error) {
	m, err := teams.GetMember(ctx, teamID, userID)
	if err != nil {
		return domain.TeamMember{}, domain.ErrNotFound
	}
	return m, nil
}

// requireRole gates an action on a minimum role for a known member.
func requireRole(m domain.TeamMember, min domain.TeamRole) error {
	if !m.Role.AtLeast(min) {
		return fmt.Errorf("%w: requires %s role", domain.ErrForbidden, min)
	}
	return nil
}
```

`TeamService` deps struct: `Teams port.TeamRepo`, `Invitations port.TeamInvitationRepo`, `Users port.UserRepo`, `Accounts port.AccountRepo`, `Mail map[domain.Provider]port.MailProvider`, `OAuth map[domain.Provider]port.OAuthGateway`, `Subs port.SubscriptionRepo`, `Tx port.TxRunner`, `Clock port.Clock`, `SelfHost bool`, `AppBaseURL string` (for the invite link `<AppBaseURL>/invite/<token>`). Invitation token: `randomToken(32)`, stored as `sha256` hex; TTL 14 days. All mutating methods call `entitlement.require` first (same gating as mail/calendar services).

- [ ] **Step 1: Extend fakes** — `fakeTeamRepo` / `fakeTeamInvitationRepo` in `fakes_test.go`, map-backed, honoring `GetMember`→`ErrNotFound`, pending-unique→`ErrConflict`, with `var _ port.X` assertions.
- [ ] **Step 2: Write failing service tests** — required subtests: create makes caller owner; `Get` as non-member → `ErrNotFound`; rename as member → `ErrForbidden`, as admin → ok; `SetMemberRole` admin-grants-admin ok, admin-grants-owner → `ErrForbidden`, demote-last-owner → `ErrConflict`; leave as last owner → `ErrConflict`; `Invite` as member → `ErrForbidden`; `Invite` with no connected account → `ErrValidation`; `Invite` happy path records a `MailProvider.Send` call whose body contains `/invite/<rawtoken>` and whose To is the invitee; duplicate pending invite → `ErrConflict`; `AcceptInvitation` happy path adds membership with the invited role and flips status to accepted; expired token → `ErrNotFound`; token reuse after accept → `ErrNotFound`; accepting user's email need NOT match the invited address (invites are bearer links — decision: the emailed address is a delivery hint, the token is the credential); unsubscribed caller → `ErrPaymentRequired`; self-host bypasses billing.
- [ ] **Step 3: Run to verify failure** — `cd backend && go test ./internal/service/ -run TestTeam`. Expected: FAIL.
- [ ] **Step 4: Implement `service/team.go` + `collab_helpers.go`.**
- [ ] **Step 5: Run** — `go test ./internal/service/`. Expected: PASS.
- [ ] **Step 6: Lint + commit** — `feat(backend): team service with role authz and email invitations`.

---

### Task 5: Team HTTP endpoints + shared types/client

**Files:**
- Create: `backend/internal/adapter/in/httpapi/teams.go`
- Create: `backend/internal/adapter/in/httpapi/teams_handlers_test.go`
- Modify: `backend/internal/adapter/in/httpapi/httpapi.go` (Deps gains `Teams port.TeamService`; routes below)
- Modify: `backend/internal/adapter/in/httpapi/harness_test.go` (fake TeamService)
- Modify: `backend/cmd/api/main.go` (wire repos + service)
- Modify: `packages/shared/src/types.ts` (`Team`, `TeamMember`, `TeamRole`, `TeamInvitation`)
- Modify: `packages/shared/src/client.ts` + `client.test.ts` (`listTeams`, `createTeam`, `getTeam`, `renameTeam`, `deleteTeam`, `setMemberRole`, `setShareReadStatuses`, `removeMember`, `invite`, `listInvitations`, `revokeInvitation`, `acceptInvitation`)

**Interfaces:** Routes registered in `New` (all authed):

```
POST   /v1/teams                                  → handleCreateTeam
GET    /v1/teams                                  → handleListTeams
GET    /v1/teams/{id}                             → handleGetTeam        (team + members)
PATCH  /v1/teams/{id}                             → handleRenameTeam
DELETE /v1/teams/{id}                             → handleDeleteTeam
PATCH  /v1/teams/{id}/members/{userId}            → handleSetMemberRole  (body {role})
PUT    /v1/teams/{id}/read-status-sharing         → handleSetShareReadStatuses (body {share})
DELETE /v1/teams/{id}/members/{userId}            → handleRemoveMember
POST   /v1/teams/{id}/invitations                 → handleInvite         (body {email, role})
GET    /v1/teams/{id}/invitations                 → handleListInvitations
DELETE /v1/teams/{id}/invitations/{invitationId}  → handleRevokeInvitation
POST   /v1/invitations/accept                     → handleAcceptInvitation (body {token})
```

- [ ] **Step 1: Write failing handler tests** in the `harness_test.go` style (fake driving port, assert status + JSON + fake-call args): 201 create, 200 lists, 400 bad role, 403 propagated from `ErrForbidden`, 404 non-member, 409 last-owner, 401 without bearer.
- [ ] **Step 2: Run to verify failure** — `cd backend && go test ./internal/adapter/in/httpapi/ -run TestTeams`. Expected: FAIL.
- [ ] **Step 3: Implement `teams.go`, register routes, wire `cmd/api/main.go`.**
- [ ] **Step 4: Shared contract** — add TS types mirroring the Go JSON, `ApiClient` methods, and client tests (fetch-mock pattern from `client.test.ts`).
- [ ] **Step 5: Run all gates** — `cd backend && go test ./...`; `bun run --cwd packages/shared test`; `bun run lint:js`.
- [ ] **Step 6: Commit** — `feat(backend): team REST endpoints and shared client surface`.

---

### Task 6: Realtime SSE — EventBus port, broker adapter, stream endpoint

**Files:**
- Modify: `backend/internal/port/driven.go` (append `CollabEvent`, `EventBus`)
- Create: `backend/internal/adapter/out/eventbus/bus.go` + `bus_test.go`
- Create: `backend/internal/adapter/in/httpapi/stream.go` + `stream_test.go`
- Modify: `backend/internal/adapter/in/httpapi/httpapi.go` (Deps gains `Events port.EventBus`; route `GET /v1/collab/stream`)
- Modify: `backend/cmd/api/main.go`
- Create: `apps/web/lib/collab-stream.ts` + `collab-stream.test.ts`

**Interfaces:**

```go
// CollabEvent is a realtime collaboration notification fanned out over SSE.
type CollabEvent struct {
	// Topic scopes delivery: "team:<teamID>" | "share:<shareID>" | "user:<userID>".
	Topic   string          `json:"topic"`
	Type    string          `json:"type"` // "comment.created" | "comment.deleted" | "activity.updated" | "share.updated" | "mention"
	Payload json.RawMessage `json:"payload"`
}

// EventBus fans CollabEvents out to in-process subscribers. Publish never
// blocks (slow subscribers drop events — SSE clients re-sync on reconnect).
// Single-process today; the multi-instance path is a Postgres LISTEN/NOTIFY
// implementation behind this same port.
type EventBus interface {
	Publish(ev CollabEvent)
	// Subscribe returns a channel of events for the given topics and a
	// cancel func. The channel closes on cancel.
	Subscribe(topics []string) (<-chan CollabEvent, func())
}
```

`stream.go`: `GET /v1/collab/stream` — authed; subscribes to `user:<uid>` plus `team:<id>` for every team the caller belongs to (resolved via `Teams.List`); writes `text/event-stream` with `event:`/`data:` frames and a 25s `: keepalive` comment tick; flushes via `http.Flusher`; exits on `r.Context().Done()`. Web `collab-stream.ts`: `openCollabStream(onEvent, { signal })` using `fetch` + `ReadableStream` line-parser with exponential-backoff reconnect (1s→30s cap) — `Authorization` header works with fetch, so no token in URL.

- [ ] **Step 1: Write failing bus tests** — publish/subscribe round-trip, topic isolation, non-blocking publish with a full subscriber buffer (event dropped, no deadlock), cancel closes channel, concurrent publishers (`go test -race`).
- [ ] **Step 2: Implement `eventbus` (mutex + per-subscriber buffered chans, cap 64).** Run `go test -race ./internal/adapter/out/eventbus/`. Expected: PASS.
- [ ] **Step 3: Write failing stream handler test** — `httptest` + a context-cancelled request: assert `Content-Type: text/event-stream`, a published `CollabEvent` arrives as a `data:` frame, 401 unauthenticated.
- [ ] **Step 4: Implement `stream.go`, wire, run** `go test ./internal/adapter/in/httpapi/`.
- [ ] **Step 5: Web client** — TDD `collab-stream.ts` with a mocked `fetch` returning a scripted `ReadableStream`; assert parsed events and reconnect on stream end. `bun run --cwd apps/web test collab-stream`.
- [ ] **Step 6: Lint + commit** — `feat(backend): SSE collaboration stream with in-process event bus`.

---

### Task 7: Shared conversations — backend

Tokenized live links to a thread, two audiences: `team` (viewer must be an authed member of the share's team) and `external` (anyone with the link; unauthenticated). Explicit per-thread action; revocable; token hashed at rest.

**Files:**
- Create: `backend/migrations/0006_thread_shares.sql` — `thread_shares(id PK, thread_id FK threads ON DELETE CASCADE, created_by FK users, audience text 'team'|'external', team_id FK teams NULL (required when audience='team', ON DELETE CASCADE), token_hash text UNIQUE, revoked_at timestamptz, expires_at timestamptz NULL, created_at)`; index on `thread_id`.
- Modify: `backend/internal/domain/` — create `collab.go` with `ShareAudience`, `ThreadShare`, plus (for Task 9/10) `Comment`, `TeamThreadActivity`.
- Modify: `backend/internal/port/driven.go` (`ThreadShareRepo`: `Create/GetByID/GetByTokenHash/ListByThread/Revoke`)
- Modify: `backend/internal/port/driving.go` (start `CollabService` — share methods below)
- Create: `backend/internal/service/collab.go` + `collab_test.go`
- Create: `backend/internal/adapter/out/postgres/collab.go` + `collab_test.go`
- Create: `backend/internal/adapter/in/httpapi/collab.go` + `collab_handlers_test.go`
- Modify: `httpapi.go`, `cmd/api/main.go`, `packages/shared/src/types.ts` + `client.ts`

**Interfaces:**

```go
// ShareThreadInput creates a live share link for a thread.
type ShareThreadInput struct {
	Audience domain.ShareAudience `json:"audience"` // "team" | "external"
	TeamID   string               `json:"teamId,omitempty"`
	// ExpiresAt optionally bounds the link's life; nil = until revoked.
	ExpiresAt *time.Time `json:"expiresAt"`
}

// SharedThreadView is the read-only projection served to share viewers:
// thread metadata + messages, with recipients' Bcc stripped and no
// labels/split/snooze state (owner-private triage data never leaves).
type SharedThreadView struct {
	Subject   string           `json:"subject"`
	Audience  domain.ShareAudience `json:"audience"`
	Messages  []domain.Message `json:"messages"`
	UpdatedAt time.Time        `json:"updatedAt"`
}

// CollabService — share surface (grown in Tasks 9-10).
type CollabService interface {
	ShareThread(ctx context.Context, userID, threadID string, in ShareThreadInput) (share domain.ThreadShare, rawToken string, err error)
	ListThreadShares(ctx context.Context, userID, threadID string) ([]domain.ThreadShare, error)
	RevokeThreadShare(ctx context.Context, userID, threadID, shareID string) error
	// GetSharedThread serves a share view. viewerUserID is nil for
	// unauthenticated (external) viewers; team-audience shares require a
	// viewer who is a member of the share's team.
	GetSharedThread(ctx context.Context, rawToken string, viewerUserID *string) (SharedThreadView, error)
}
```

Rules (all in the service): sharing requires thread ownership (`ownedThread`) + entitlement; `audience=team` requires the sharer to be a member of `TeamID`; revoked/expired/unknown tokens → `ErrNotFound` uniformly (no oracle); external views never include `Bcc` (strip in projection); the sync service publishes `{Topic: "share:<id>", Type: "share.updated"}` when a shared thread receives a new message — hook: after `SyncService` upserts messages for a thread, it asks `ThreadShareRepo.ListByThread` and publishes for live shares (cheap: one indexed query per updated thread, only when the bus is non-nil).

Routes:

```
POST   /v1/mail/threads/{id}/share             (authed)
GET    /v1/mail/threads/{id}/shares            (authed)
DELETE /v1/mail/threads/{id}/shares/{shareId}  (authed)
GET    /v1/shared/threads/{token}              (UNAUTHENTICATED route; team-audience re-checks bearer if present, else 404)
GET    /v1/shared/threads/{token}/stream       (SSE; same auth semantics; subscribes topic "share:<id>")
```

- [ ] **Step 1: Failing service tests** — share own thread ok + raw token returned once; share foreign thread → `ErrNotFound`; team share by non-member of that team → `ErrNotFound`; external view strips Bcc and triage fields; team view without viewer → `ErrNotFound`; team view by non-member viewer → `ErrNotFound`; revoked and expired → `ErrNotFound`; revoke by non-creator non-owner → `ErrNotFound`.
- [ ] **Step 2: Implement domain + ports + service; run** `go test ./internal/service/ -run TestCollabShare`.
- [ ] **Step 3: Repo integration tests + implementation** (token-hash lookup, revocation, cascade on thread delete): `go test ./internal/adapter/out/postgres/ -run TestThreadShare`.
- [ ] **Step 4: Handler tests + implementation** (incl. unauthenticated route registered OUTSIDE `authed(...)`, mirroring the Stripe-webhook precedent; optional-bearer parsing for team shares).
- [ ] **Step 5: Shared client** — `shareThread`, `listThreadShares`, `revokeThreadShare`, `getSharedThread`; types; tests.
- [ ] **Step 6: Full gates + commit** — `feat(backend): tokenized live thread shares (team + external)`.

---### Task 8: Shared conversation web page (read-only live view)

**Files:**
- Create: `apps/web/app/(share)/shared/[token]/page.tsx` (public route — no `(app)` auth shell)
- Create: `apps/web/components/share/shared-thread-view.tsx` + `shared-thread-view.test.tsx`
- Modify: `apps/web/components/app/*` thread view — add a "Share thread" action to the thread toolbar + ⌘K palette (dialog: audience picker, team picker, copy-link; list + revoke existing shares)
- Create: `apps/web/components/app/share-dialog.tsx` + `share-dialog.test.tsx`

**Interfaces:** Consumes `getSharedThread(token)` and the Task 6 `openCollabStream` scoped to `/v1/shared/threads/{token}/stream`; on `share.updated` events, invalidates the TanStack Query key `['shared-thread', token]` for a live refresh (SSE-driven, no polling). Demo mode: a canned `SharedThreadView` behind `DEMO_MODE` only.

- [ ] **Step 1: Failing component tests** — renders subject + message list read-only (no compose/triage affordances present); revoked link renders the "This link is no longer active" empty state on 404; share-dialog: creating a share surfaces the one-time link and calls the client with the chosen audience/team.
- [ ] **Step 2: Implement page + components** (shadcn new-york, light+dark; reuse the message renderer from the mail thread view, stripped of actions).
- [ ] **Step 3: Run** — `bun run --cwd apps/web test share`. Expected: PASS.
- [ ] **Step 4: Lint + commit** — `feat(web): live read-only shared conversation page and share dialog`.

---

### Task 9: Team comments with @mentions

**Files:**
- Create: `backend/migrations/0007_thread_comments.sql` — `thread_comments(id PK, thread_id FK threads ON DELETE CASCADE, team_id FK teams ON DELETE CASCADE, author_id FK users, body text NOT NULL CHECK (length(body) <= 10000), mentions text[] NOT NULL DEFAULT '{}', created_at, updated_at, deleted_at timestamptz)`; index `(thread_id, team_id, created_at)`.
- Modify: `backend/internal/domain/collab.go` (`Comment{ID, ThreadID, TeamID, AuthorID, Body, Mentions []string, CreatedAt, UpdatedAt, DeletedAt *time.Time}` + `ParseMentions(body string, members []TeamMember) []string` resolving `@email` tokens against membership)
- Modify: `driven.go` (`CommentRepo: Create/GetByID/ListByThreadTeam/Update/SoftDelete`), `driving.go` (extend `CollabService`)
- Modify: `backend/internal/service/collab.go` + tests; `postgres/collab.go` + tests; `httpapi/collab.go` + tests; shared types/client
- Modify: web thread view — comments sidebar panel `apps/web/components/app/thread-comments.tsx` + test

**Interfaces:** `CollabService` gains:

```go
CommentInput struct {
	TeamID string `json:"teamId"`
	Body   string `json:"body"`
}
ListComments(ctx context.Context, userID, threadID, teamID string) ([]domain.Comment, error)
AddComment(ctx context.Context, userID, threadID string, in CommentInput) (domain.Comment, error)
UpdateComment(ctx context.Context, userID, commentID, body string) (domain.Comment, error)
DeleteComment(ctx context.Context, userID, commentID string) error
```

Rules: commenting requires (a) team membership and (b) the thread being *visible to that team* — visible means: the caller owns the thread, or an unrevoked team-audience share for that team exists on it (comments piggyback on the explicit share, preserving the privacy default — a comment can never be the first thing that exposes a thread). Mentions resolve to member user IDs at write time; each mentioned member gets a push through the existing `port.PushSender` fan-out (`DeviceRepo.ListByUser` per mentionee, same pattern the worker uses for snooze wake-ups) with title `"<author> mentioned you"`, and a `{Topic:"user:<id>", Type:"mention"}` bus event. Every comment mutation publishes `{Topic:"team:<teamID>", Type:"comment.created|updated|deleted"}`. Edit/delete: author always; team admin+ may delete (not edit); others → `ErrForbidden` (author-known case) — non-members still `ErrNotFound`.

- [ ] **Step 1: Failing service tests** — non-member comment → `ErrNotFound`; member but thread not shared to team and not owned → `ErrNotFound`; owner comments without a share → OK (owner is always allowed on own thread); mention of non-member email resolves to no mention (dropped silently); mention push recorded per device via fake `PushSender`; bus event published; admin deletes other's comment OK, member deletes other's → `ErrForbidden`; body length cap → `ErrValidation`.
- [ ] **Step 2: Implement domain helper + service; run** `go test ./internal/service/ -run TestComment`.
- [ ] **Step 3: Migration + repo (soft delete filtered from lists) + integration tests.**
- [ ] **Step 4: Routes** — `GET/POST /v1/mail/threads/{id}/comments`, `PATCH/DELETE /v1/comments/{id}`; handler tests; shared client (`listComments`, `addComment`, `updateComment`, `deleteComment`).
- [ ] **Step 5: Web comments panel** — thread sidebar with author avatars, @-autocomplete from team members, live updates via bus `comment.*` events invalidating `['comments', threadId, teamId]`; component tests (add, render, mention-autocomplete).
- [ ] **Step 6: Full gates + commit** — `feat: team comments with mentions and push notifications`.

---

### Task 10: Team read statuses / reply indicators

Teammates each mirror the *same* conversation as different local thread rows (per-account provider thread IDs differ). Correlation decision: the **RFC 5322 `Message-ID` of the thread's earliest message** is the cross-account conversation key. Sync already receives headers (`port.IncomingMessage.Headers`); we persist the id.

**Files:**
- Create: `backend/migrations/0008_team_thread_activity.sql` — `ALTER TABLE messages ADD COLUMN rfc_message_id text; CREATE INDEX messages_rfc_idx ON messages (rfc_message_id) WHERE rfc_message_id IS NOT NULL;` plus `team_thread_activity(team_id FK ON DELETE CASCADE, user_id FK ON DELETE CASCADE, conversation_key text NOT NULL, opened_at timestamptz, replied_at timestamptz, updated_at, PRIMARY KEY (team_id, user_id, conversation_key))`; index `(team_id, conversation_key)`.
- Modify: `backend/internal/domain/mail.go` (`Message.RFCMessageID string \`json:"-"\``), `collab.go` (`TeamThreadActivity{TeamID, UserID, ConversationKey string, OpenedAt, RepliedAt *time.Time}`)
- Modify: `driven.go` (`TeamThreadActivityRepo: Upsert(ctx, a domain.TeamThreadActivity) error; ListByConversation(ctx, teamID, conversationKey string) ([]domain.TeamThreadActivity, error)`; `MessageRepo` gains `EarliestRFCMessageID(ctx, threadID string) (string, error)`)
- Modify: `backend/internal/service/sync.go` (persist `rfc_message_id` from the `Message-ID` header at ingest; on delivered sends, record `replied_at`)
- Modify: `backend/internal/service/mail.go` (`MarkThreadOpened` additionally records `opened_at` activity for every team where the caller has `ShareReadStatuses` — fire-and-forget, never fails the open)
- Modify: `backend/internal/service/collab.go` (`TeamThreadActivity(ctx, userID, threadID string) ([]domain.TeamThreadActivity, error)` on `CollabService`: resolves the caller's thread → conversation key → activity rows for teams the caller belongs to, filtered to members with sharing on)
- Modify: postgres adapters + integration tests; `httpapi` route `GET /v1/mail/threads/{id}/team-activity`; shared types/client (`teamThreadActivity(threadId)`)
- Modify: web thread view — teammate read/replied chips (`apps/web/components/app/team-activity-chips.tsx` + test)

Activity writes publish `{Topic:"team:<id>", Type:"activity.updated"}`.

- [ ] **Step 1: Failing sync tests** — ingest stores `rfc_message_id` from the `Message-ID` header (present/absent cases); worker send path upserts `replied_at` for opted-in teams only.
- [ ] **Step 2: Failing mail-service test** — `MarkThreadOpened` records activity only for teams with `ShareReadStatuses=true`; opted-out member's open records nothing (privacy default).
- [ ] **Step 3: Failing collab tests** — query returns only rows from members who opted in; caller not in any team → empty, not error; conversation with no `Message-ID` → empty (graceful).
- [ ] **Step 4: Implement migration, repos, service hooks; run** `cd backend && go test ./...`.
- [ ] **Step 5: Route + client + web chips (with live `activity.updated` invalidation); component test.**
- [ ] **Step 6: Full gates + commit** — `feat: team read statuses and reply indicators over RFC Message-ID correlation`.

---

### Task 11: Team snippets

Extend the existing snippet model with an optional team scope — no new resource.

**Files:**
- Create: `backend/migrations/0009_team_snippets.sql` — `ALTER TABLE snippets ADD COLUMN team_id text REFERENCES teams(id) ON DELETE CASCADE; CREATE INDEX snippets_team_idx ON snippets (team_id) WHERE team_id IS NOT NULL;`
- Modify: `backend/internal/domain/mail.go` (`Snippet.TeamID *string \`json:"teamId"\``)
- Modify: `driving.go` (`SnippetInput.TeamID *string`), `driven.go` (`SnippetRepo` gains `ListByTeams(ctx context.Context, teamIDs []string) ([]domain.Snippet, error)`)
- Modify: `backend/internal/service/mail.go` snippet methods: `ListSnippets` returns personal + all-my-teams' snippets; create with `TeamID` requires membership (any role — decision: every member can contribute; consistent-reply value beats gatekeeping); update/delete of a team snippet: author or team admin+, else `ErrForbidden`; personal snippets unchanged.
- Modify: postgres `mail.go` + tests; shared types/client; web snippet manager in settings + compose `;`-insert surface team snippets with a team badge (`apps/web/app/(app)/settings/page.tsx`, compose components + tests)

- [ ] **Step 1: Failing service tests** — create team snippet as non-member → `ErrNotFound`; list merges personal + team, no duplicates; member edits other's team snippet → `ErrForbidden`; admin edits OK; deleting team cascades its snippets (integration).
- [ ] **Step 2: Implement migration + service + repo; run** `go test ./internal/service/ -run TestSnippet ./internal/adapter/out/postgres/ -run TestSnippet`.
- [ ] **Step 3: Contract + web (badge + grouped listing); tests; gates.**
- [ ] **Step 4: Commit** — `feat: team-scoped snippets`.

---

### Task 12: Shared calendars with granular permissions

Local sharing layer over the mirrored calendars. **Provider-level sharing is explicitly out of scope** (see Global Constraints — divergent Google/Graph ACL semantics, unrevocable external access). Reads come from the mirror; `editor` writes go through the **owner's** tokens and are audit-logged (audit table lands here, reused by Task 15).

**Files:**
- Create: `backend/migrations/0010_calendar_shares.sql` —

```sql
CREATE TABLE calendar_shares (
    id          text PRIMARY KEY,
    calendar_id text NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    -- Exactly one grantee: a specific user or a whole team.
    grantee_user_id text REFERENCES users(id) ON DELETE CASCADE,
    grantee_team_id text REFERENCES teams(id) ON DELETE CASCADE,
    permission  text NOT NULL DEFAULT 'free_busy', -- 'free_busy' | 'reader' | 'editor'
    created_by  text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CHECK (num_nonnulls(grantee_user_id, grantee_team_id) = 1)
);
CREATE UNIQUE INDEX calendar_shares_user_idx ON calendar_shares (calendar_id, grantee_user_id) WHERE grantee_user_id IS NOT NULL;
CREATE UNIQUE INDEX calendar_shares_team_idx ON calendar_shares (calendar_id, grantee_team_id) WHERE grantee_team_id IS NOT NULL;
CREATE INDEX calendar_shares_grantee_idx ON calendar_shares (grantee_user_id, grantee_team_id);

CREATE TABLE audit_entries (
    id            text PRIMARY KEY,
    actor_id      text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    principal_id  text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    action        text NOT NULL,          -- 'event.create' | 'event.update' | ...
    resource_type text NOT NULL,
    resource_id   text NOT NULL,
    metadata      jsonb NOT NULL DEFAULT '{}',
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_entries_principal_idx ON audit_entries (principal_id, created_at DESC);
```

- Modify: `backend/internal/domain/calendar.go` (`CalendarPermission` enum + `ParseCalendarPermission`; `CalendarShare` struct; `Event.FreeBusyOnly bool \`json:"freeBusyOnly,omitempty"\``), new `domain/audit.go` (`AuditEntry`)
- Modify: `driven.go` (`CalendarShareRepo: Create/ListByCalendar/ListForGrantee(userID, teamIDs)/Update/Delete`; `AuditRepo: Record(ctx, e domain.AuditEntry) error; ListByPrincipal(ctx, principalID string, limit int) ([]domain.AuditEntry, error)`)
- Modify: `driving.go` `CalendarService`: `ShareCalendar(ctx, userID, calendarID string, in CalendarShareInput) (domain.CalendarShare, error)`, `ListCalendarShares`, `UpdateCalendarShare`, `RevokeCalendarShare`; `ListCalendars` result gains shared-with-me calendars (annotated `SharedPermission *domain.CalendarPermission` on `domain.Calendar`, `json:"sharedPermission,omitempty"`); `ListEvents` serves shared calendars: `free_busy` → events reduced to `{Start, End, FreeBusyOnly: true}` (title "Busy", all other fields zeroed); `reader` → full read; `editor` → full read + `CreateEvent/UpdateEvent/DeleteEvent` accepted on the shared calendar, executed with the owner's `tokenSource`, `AuditRepo.Record` on success (actor=grantee, principal=owner).
- Modify: `backend/internal/service/calendar.go` + tests; postgres + tests; httpapi routes `POST/GET /v1/calendars/{id}/shares`, `PATCH/DELETE /v1/calendars/{id}/shares/{shareId}`; shared types/client; web calendar settings sharing UI + free/busy rendering (`apps/web/app/(app)/calendar/page.tsx`, `apps/web/components/calendar/*` + tests)

Rules: only the calendar's owning user may manage shares (via account ownership chain `calendar → account → user`); sharing requires entitlement; grantee resolution: direct user grant or any team the viewer shares with the owner; the most permissive applicable grant wins; **no share → shared calendar invisible** (privacy default).

- [ ] **Step 1: Failing service tests** — share own calendar to user and to team; non-owner shares → `ErrNotFound`; viewer sees shared calendar in `ListCalendars` with the annotation; `free_busy` viewer gets redacted events (no title/attendees/location leak — assert zeroed fields); `reader` cannot write (`ErrForbidden`); `editor` write goes through owner tokens (fake `CalendarProvider` records the owner's access token) and records an audit entry; revoke hides the calendar; team grant applies to all members; most-permissive-wins with overlapping user+team grants.
- [ ] **Step 2: Implement migration, domain, repos (+integration tests), service; run** `cd backend && go test ./...`.
- [ ] **Step 3: Routes + client + web UI (share dialog on a calendar row; "Busy" block rendering); tests.**
- [ ] **Step 4: Gates + commit** — `feat: shared calendars with free-busy/reader/editor permissions and audit log`.

---

### Task 13: Team availability overview + Find Time inline

**Files:**
- Modify: `driving.go` `CalendarService`: `TeamAvailability(ctx context.Context, userID, teamID string, from, to time.Time) ([]MemberAvailability, error)` with `MemberAvailability{UserID string \`json:"userId"\`; Busy []domain.AvailabilitySlot \`json:"busy"\`; Shared bool \`json:"shared"\`}`
- Modify: `backend/internal/service/calendar.go` + tests — caller must be a team member; a member appears with `Shared:true` + busy blocks **only if** they granted ≥ `free_busy` on ≥1 calendar to that team (Task 12 grants are the opt-in; no separate mechanism); non-granting members return `Shared:false` and an empty list (visible as "not sharing" in UI, no data leak). Busy blocks computed from mirrored events on granted calendars (reuse the existing availability computation inverted to busy intervals).
- Modify: httpapi route `GET /v1/teams/{id}/availability?from&to`; shared types/client (`teamAvailability`)
- Create: `apps/web/components/calendar/team-availability.tsx` + test (overview grid: member rows × time columns, timezone-aware)
- Create: `apps/web/components/app/find-time.tsx` + test — inline in compose (⌘⇧A extension): pick a team, overlay members' busy on the slot picker, only slots free for all selected members insertable; deliberate-override affordance (insert anyway, marked)

- [ ] **Step 1: Failing service tests** — non-member → `ErrNotFound`; member without grants → `Shared:false`, no busy data; grant on one calendar exposes only that calendar's events as busy; range validation (`to` ≤ `from` → `ErrValidation`, span > 35 days → `ErrValidation`).
- [ ] **Step 2: Implement + run** `go test ./internal/service/ -run TestTeamAvailability`.
- [ ] **Step 3: Route + client; handler tests.**
- [ ] **Step 4: Web overview + Find Time inline; component tests (slot disabled when any selected member busy; override path).**
- [ ] **Step 5: Gates + commit** — `feat: team availability overview and Find Time inline`.

---

### Task 14: Team scheduling links (DEPENDS ON M2.4)

**Explicit dependency:** builds on `booking_links` + `BookingService` from plan `2026-07-17-m2-4-scheduling-booking.md`. Do not start until M2.4 is merged; if executing this plan before M2.4 lands, skip and return.

**Files:**
- Create: `backend/migrations/0011_team_booking_links.sql` — `ALTER TABLE booking_links ADD COLUMN team_id text REFERENCES teams(id) ON DELETE CASCADE;` plus `booking_link_members(booking_link_id FK ON DELETE CASCADE, user_id FK ON DELETE CASCADE, PRIMARY KEY (booking_link_id, user_id))`.
- Modify: M2.4's booking domain/port/service files (exact names per that plan): `BookingLinkInput` gains `TeamID *string` + `MemberUserIDs []string`; slot computation for a team link intersects **all** listed members' free/busy (collective mode — every member must be free; round-robin is future work, noted in code); each member must have granted ≥ `free_busy` to the link's team (Task 12/13 opt-in) or be the link creator, else `ErrValidation` at link creation names the non-sharing member; booking confirmation creates the event on the creator's calendar and invites all members as attendees (single provider write via the creator's tokens — no cross-account event fan-out).
- Modify: booking endpoints (M2.4's) to accept/return the team fields; shared types/client; web booking-link editor gains a team/member picker; public booking page shows collective availability. Tests at every touched layer mirror M2.4's existing suites.

- [ ] **Step 1: Verify M2.4 merged** — `cd backend && ls migrations | grep booking`. If absent: STOP, mark task blocked.
- [ ] **Step 2: Failing service tests** — collective slots = intersection of member availabilities; member without team grant at creation → `ErrValidation`; removing a member from the team invalidates the link's slots for them (recomputed per request, so it just intersects fewer/none — assert behavior); confirmation invites all members.
- [ ] **Step 3: Implement migration + service + endpoints; run** `cd backend && go test ./...`.
- [ ] **Step 4: Client + web editor/public page; tests; gates.**
- [ ] **Step 5: Commit** — `feat: team scheduling links with collective availability`.

---

### Task 15: EA delegation mode + audit log

A principal grants an assistant scoped access to their mail/calendar; the assistant acts as the principal over the normal API with an explicit header; every delegated **mutation** is audit-logged (reads are not, by decision — volume vs. value; the grant itself bounds read exposure).

**Files:**
- Create: `backend/migrations/0012_delegations.sql` — `delegations(id PK, principal_id FK users ON DELETE CASCADE, assistant_id FK users ON DELETE CASCADE, scopes text[] NOT NULL, status text NOT NULL DEFAULT 'pending' /* pending|active|revoked */, created_at, accepted_at timestamptz, revoked_at timestamptz, UNIQUE (principal_id, assistant_id))` (audit_entries already exists from Task 12).
- Create: `backend/internal/domain/delegation.go` — `DelegationScope` enum (`mail_read`, `mail_write`, `calendar_read`, `calendar_write`) + `ParseDelegationScope`; `Delegation{ID, PrincipalID, AssistantID, Scopes []DelegationScope, Status DelegationStatus, CreatedAt, AcceptedAt, RevokedAt *time.Time}`; `DelegationStatus` enum.
- Modify: `driven.go` (`DelegationRepo: Create/GetByID/GetActive(ctx, principalID, assistantID string)/ListByUser(either side)/Update`)
- Modify: `driving.go`:

```go
// DelegationService manages EA grants and authorizes delegated requests.
type DelegationService interface {
	Create(ctx context.Context, principalID, assistantEmail string, scopes []domain.DelegationScope) (domain.Delegation, error)
	List(ctx context.Context, userID string) (asPrincipal, asAssistant []domain.Delegation, err error)
	Accept(ctx context.Context, assistantID, delegationID string) (domain.Delegation, error)
	Revoke(ctx context.Context, userID, delegationID string) error // either party
	// Authorize validates that assistant may act as principal with the
	// given scope; returns ErrForbidden when the grant is missing/inactive
	// or lacks the scope. Called by the HTTP middleware.
	Authorize(ctx context.Context, assistantID, principalID string, scope domain.DelegationScope) error
	// RecordAudit persists an audit entry for a delegated mutation.
	RecordAudit(ctx context.Context, e domain.AuditEntry) error
	Audit(ctx context.Context, principalID string, limit int) ([]domain.AuditEntry, error)
}
```

- Create: `backend/internal/service/delegation.go` + `delegation_test.go`; postgres repo + integration tests
- Modify: `backend/internal/adapter/in/httpapi/middleware.go` — after bearer verification, if `X-Calendium-Act-As: <principalUserID>` is present: map the route to a scope (`GET /v1/mail*`, `GET /v1/search` → `mail_read`; mutating `/v1/mail*` → `mail_write`; `GET /v1/calendars|/v1/events|/v1/availability` → `calendar_read`; mutating → `calendar_write`; **teams/billing/accounts/devices/delegations routes reject act-as outright with `ErrForbidden`** — delegation covers mail/calendar actions only), call `Delegations.Authorize`, then place the *principal's* userID in the request context (handlers are untouched — they already read userID from context) while retaining the assistant id; after a successful mutating delegated request the middleware calls `RecordAudit` (actor=assistant, principal, action=`<METHOD> <route>`, resource id from path).
- Modify: routes — `POST/GET /v1/delegations`, `POST /v1/delegations/{id}/accept`, `DELETE /v1/delegations/{id}`, `GET /v1/delegations/audit`; `httpapi.go` Deps gains `Delegations port.DelegationService`; `cmd/api/main.go` wiring; shared types/client (`ApiClient` gains an `actAs(principalUserID)` option that sets the header on every call)
- Modify: web — settings "Delegation" section (grant/accept/revoke, scope checkboxes, audit table) + an act-as switcher in the account menu that badges the whole UI "Acting for <name>"; components + tests

- [ ] **Step 1: Failing service tests** — create targets an existing user by email (unknown email → `ErrNotFound`); self-delegation → `ErrValidation`; `Authorize` on pending grant → `ErrForbidden`; scope mismatch → `ErrForbidden`; revoked → `ErrForbidden`; accept by wrong user → `ErrNotFound`; audit list is principal-only (assistant reading principal's audit → `ErrNotFound`).
- [ ] **Step 2: Failing middleware tests** (extend `middleware_test.go`) — act-as with valid grant reaches the handler with the principal's userID in context; missing grant → 403; act-as on `POST /v1/teams` → 403; delegated `POST /v1/mail/threads/{id}/actions` records exactly one audit entry with actor/principal set; non-delegated requests record nothing.
- [ ] **Step 3: Implement domain/ports/service/repo/middleware/routes; run** `cd backend && go test ./...`.
- [ ] **Step 4: Shared client `actAs` + web delegation settings/switcher; tests.**
- [ ] **Step 5: Gates + commit** — `feat: EA delegation mode with scoped act-as and audit log`.

---

### Task 16: Docs, feature map, and final gates

**Files:**
- Modify: `docs/feature-map.md` — flip the Collaboration rows in both tables (Shared Conversations, Team Comments, Team read statuses, Team Snippets, Shared calendars w/ permissions, Find a Time grid, Find Time inline, Team availability overview, Team scheduling links, EA delegation mode, Team scheduling) from `planned` to `scaffolded`/live per what shipped.
- Create: `docs/collaboration.md` — the four encoded decisions (service-layer authz + ErrForbidden; SSE over EventBus with LISTEN/NOTIFY scale path; explicit-share-only privacy; RFC Message-ID conversation correlation), the provider-sharing out-of-scope rationale, and the team-billing flag (per-user $50/yr unchanged; seat-based team plan = future Stripe work).
- Modify: `docs/state-and-gaps.md` — record M2.7 completion status.

- [ ] **Step 1: Write the docs.**
- [ ] **Step 2: Full verification** — `cd backend && go test ./... && golangci-lint run ./...`; `bun run test:web`; `bun run --cwd packages/shared test`; `bun run lint:js`; e2e demo-mode Playwright suite still green (`bun run --cwd apps/web e2e` if configured per M-earlier setup).
- [ ] **Step 3: Commit** — `docs: collaboration architecture decisions and feature-map status`.

---

## Out of scope (deliberately)

- **Team/seat billing** — flagged above; per-user $50/yr Stripe unchanged. Future work: seat-quantity subscription, owner-pays, proration.
- **Provider-level calendar/mail sharing (Google ACLs, Graph permissions)** — local layer only; rationale in Global Constraints and `docs/collaboration.md`.
- **Round-robin team scheduling links** — collective (all-free) mode only; round-robin noted in code for a follow-up.
- **Multi-instance SSE fan-out** — Postgres LISTEN/NOTIFY behind the existing `EventBus` port when the API scales past one process.
- **Mobile/desktop collaboration UI beyond shared-client plumbing** — the `ApiClient` surface ships everywhere; web is the reference UI this phase, mobile/desktop screens follow in platform-polish passes.
