# Backend Test Coverage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring the Go backend (`backend/`) to comprehensive automated test coverage — domain rules, all 11 services, HTTP handlers, outbound adapters, and the Postgres repositories — building on the existing 9 test files.

**Architecture:** Hexagonal. Tests target each layer through its port. A new shared `service/fakes_test.go` provides in-memory fakes for every driven port plus a controllable `fakeClock`; service tests wire real services to these fakes. HTTP tests use `httptest` with fake driving-port services. Provider adapters are tested against local `httptest` mock servers. Postgres repos run against a real, throwaway Postgres started by testcontainers-go.

**Tech Stack:** Go 1.26 stdlib `testing` + `net/http/httptest`; `github.com/testcontainers/testcontainers-go` (test-scope only); GitHub Actions CI.

## Nature of this work (read first)

Production code **already exists and is expected to pass**. These are coverage/characterization tests, not TDD of new features. Per-task cycle:

1. Write the test.
2. Run it. **Expected: PASS** against existing code.
3. **If it FAILS:** decide — wrong test or real bug? Wrong test → fix the test. Real bug → STOP, invoke `superpowers:systematic-debugging`, fix production code in a *separate* commit, note it in the task.
4. Commit.

Every Interfaces block below cites the **real** signatures (copied from `internal/port` and `internal/domain`); do not invent names. Where a step says "cover these cases," each listed case is a required `t.Run` subtest following the exemplar shown in that task.

## Global Constraints

- Module path: `calendium/backend`. Packages import as `calendium/backend/internal/...`.
- Test files live in the **same package** as the code under test (white-box), matching existing convention (`package service`, `package domain`, `package httpapi`). Postgres integration uses `package postgres`.
- Sentinel errors are compared with `errors.Is` against `domain.ErrValidation | ErrUnauthorized | ErrPaymentRequired | ErrNotFound | ErrConflict | ErrSelfHosted` — never string-match.
- Time is injected. Never call `time.Now()` in an assertion; drive time through `fakeClock`.
- Ownership rule (invariant): a resource owned by another user is indistinguishable from missing → `domain.ErrNotFound`, never 403.
- `testcontainers-go` is added to `require` in `backend/go.mod` as a **test-scope** dependency; the production binaries (`cmd/api`, `cmd/worker`) must still compile and depend only on pgx at runtime.
- Coverage floors (measured via `go test -coverprofile`): domain + service ≥90%; adapters logic ≥80%.
- Every task ends green: `cd backend && go test ./...` (integration tasks: with Docker available).

## File structure (created or modified)

**New test files:**
- `backend/internal/service/fakes_test.go` — shared in-memory port fakes + `fakeClock` (Task 1).
- `backend/internal/domain/domain_test.go` — enum parsers, `HasAccess`, `Page` (Task 2).
- `backend/internal/service/billing_test.go` (Task 3), `account_test.go` (Task 4), `mail_test.go`* (Task 5), `calendar_test.go` (Task 6), `misc_services_test.go` (Task 7: search/ai/device/user), `sync_test.go` (Task 8), `helpers_test.go` (Task 9).
- `backend/internal/adapter/in/httpapi/harness_test.go` (Task 10), `handlers_test.go` (Task 11).
- `backend/internal/adapter/out/postgres/crypto_test.go` (Task 12a); `authjwt/verifier_test.go`* extend (Task 12b).
- `backend/internal/adapter/out/push/apns_test.go`, `fcm_test.go`; `openrouter/client_test.go`; `stripeapi/payments_test.go` (Task 13).
- `backend/internal/adapter/out/googleapi/{mail,calendar}_test.go`; `msgraph/{mail,calendar}_test.go` (Task 14).
- `backend/internal/adapter/out/postgres/postgres_test.go` (harness) + `*_repo_test.go` (Task 15).
- `backend/internal/config/config_test.go` (Task 16).

**Modified:**
- `backend/internal/service/ops_test.go` — drop its inline stubs; use the shared fakes (Task 1).
- `backend/go.mod` / `go.sum` — add testcontainers-go (Task 15).
- root `package.json`, `Makefile`, new `.github/workflows/test.yml` (Task 17).

(*`mail_test.go` in service and `verifier_test.go` in authjwt already exist and are extended in place; `domain/mail_test.go` stays as-is.)

---

### Task 1: Shared service test fakes

Build the map-backed in-memory fakes that every downstream service test depends on, then retire the ad-hoc inline stubs currently living in `ops_test.go`. The fakes implement the **full** driven/driving port interfaces (verified against `backend/internal/port/driven.go`), carry the exact programmable/recording fields other tasks reference, and are self-verified by `var _ port.X = (*fakeX)(nil)` compile-time assertions.

**Files:**
- `backend/internal/service/fakes_test.go` — new; the entire fake suite (package `service`, white-box).
- `backend/internal/service/ops_test.go` — modify; delete inline stubs, rewire call sites to the shared constructors.

**Interfaces (Produces):**
- Repo constructors: `newUserRepo`, `newSubscriptionRepo`, `newAccountRepo`, `newThreadRepo`, `newMessageRepo`, `newDraftRepo(*fakeAccountRepo)`, `newSnippetRepo`, `newLabelRepo`, `newCalendarRepo`, `newEventRepo`, `newDeviceRepo`, `newSyncStateRepo`, `newStripeEventRepo`, `newOAuthStateRepo`.
- Gateway constructors: `newOAuthGateway`, `newMailProvider`, `newCalendarProvider`, `newPayments`, `newAI`, `newPush`, `newTxRunner`.
- Clock: `newClock(t time.Time) *fakeClock` with `Now()/Advance(d)/Set(t)`.

Real-code invariants these fakes must honor (confirmed while reading the services):
- `entitlement.require` returns `ErrPaymentRequired` wrapping when `SubscriptionRepo.GetByUserID` yields `ErrNotFound`, so the subscription repo MUST return `domain.ErrNotFound` (not a zero value) when absent.
- `tokenSource.accessToken` calls `GetTokens` then, on expiry, `Refresh` and re-`SaveTokens`; a `Refresh` error flips the account to `AccountReauthRequired` via `Update`. The account fake's `GetTokens`/`SaveTokens` must round-trip and `Update` must persist.
- `BillingService.HandleWebhook` runs inside `TxRunner.RunInTx` and calls `StripeEventRepo.Record` for idempotency — so `RunInTx` MUST execute `fn(ctx)` and `Record` MUST be genuinely set-backed (first insert `true`, replays `false`).
- Ownership is enforced in the service via `owned*` helpers reading `AccountRepo.GetByID().UserID`; foreign rows surface as `domain.ErrNotFound`. Fakes just need faithful `GetByID`/absent semantics.

---

- [ ] **Step 1: Create `backend/internal/service/fakes_test.go`.**

```go
package service

import (
	"context"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// fakes_test.go holds the shared, map-backed in-memory fakes used across the
// service test suite. Every fake implements the full port interface it stands
// in for (asserted below with `var _ port.X = (*fakeX)(nil)`), constructed by a
// newXxx() helper. No goroutines are used, so no mutex is needed.

// --- clock -------------------------------------------------------------------

// fakeClock is a deterministic port.Clock. Never assert against time.Now();
// drive time with Advance/Set instead.
type fakeClock struct{ now time.Time }

func newClock(t time.Time) *fakeClock { return &fakeClock{now: t} }

func (c *fakeClock) Now() time.Time            { return c.now }
func (c *fakeClock) Advance(d time.Duration)   { c.now = c.now.Add(d) }
func (c *fakeClock) Set(t time.Time)           { c.now = t }

var _ port.Clock = (*fakeClock)(nil)

// --- tx runner ---------------------------------------------------------------

// fakeTxRunner runs fn inline (no real transaction) and counts invocations.
type fakeTxRunner struct{ calls int }

func newTxRunner() *fakeTxRunner { return &fakeTxRunner{} }

func (r *fakeTxRunner) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	r.calls++
	return fn(ctx)
}

var _ port.TxRunner = (*fakeTxRunner)(nil)

// --- user repo ---------------------------------------------------------------

type fakeUserRepo struct {
	byID map[string]domain.User
}

func newUserRepo() *fakeUserRepo { return &fakeUserRepo{byID: map[string]domain.User{}} }

func (r *fakeUserRepo) Upsert(_ context.Context, u domain.User) (domain.User, error) {
	r.byID[u.ID] = u
	return u, nil
}

func (r *fakeUserRepo) GetByID(_ context.Context, id string) (domain.User, error) {
	u, ok := r.byID[id]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return u, nil
}

var _ port.UserRepo = (*fakeUserRepo)(nil)

// --- subscription repo -------------------------------------------------------

// fakeSubscriptionRepo indexes every upserted row by BOTH UserID and
// StripeCustomerID so GetByUserID / GetByStripeCustomerID stay consistent.
type fakeSubscriptionRepo struct {
	byUser     map[string]domain.Subscription
	byCustomer map[string]domain.Subscription
}

func newSubscriptionRepo() *fakeSubscriptionRepo {
	return &fakeSubscriptionRepo{
		byUser:     map[string]domain.Subscription{},
		byCustomer: map[string]domain.Subscription{},
	}
}

func (r *fakeSubscriptionRepo) GetByUserID(_ context.Context, userID string) (domain.Subscription, error) {
	s, ok := r.byUser[userID]
	if !ok {
		return domain.Subscription{}, domain.ErrNotFound
	}
	return s, nil
}

func (r *fakeSubscriptionRepo) GetByStripeCustomerID(_ context.Context, customerID string) (domain.Subscription, error) {
	s, ok := r.byCustomer[customerID]
	if !ok {
		return domain.Subscription{}, domain.ErrNotFound
	}
	return s, nil
}

func (r *fakeSubscriptionRepo) Upsert(_ context.Context, s domain.Subscription) error {
	r.byUser[s.UserID] = s
	if s.StripeCustomerID != "" {
		r.byCustomer[s.StripeCustomerID] = s
	}
	return nil
}

var _ port.SubscriptionRepo = (*fakeSubscriptionRepo)(nil)

// --- account repo ------------------------------------------------------------

// fakeAccountRepo is map-backed and consistent: after Delete GetByID reports
// ErrNotFound; SaveTokens/GetTokens round-trip a port.TokenSet; Update mutates
// the stored account. created/updated snapshot the last Create/Update arg.
type fakeAccountRepo struct {
	byID    map[string]domain.ConnectedAccount
	tokens  map[string]port.TokenSet
	created *domain.ConnectedAccount
	updated *domain.ConnectedAccount
	deleted []string
}

func newAccountRepo() *fakeAccountRepo {
	return &fakeAccountRepo{
		byID:   map[string]domain.ConnectedAccount{},
		tokens: map[string]port.TokenSet{},
	}
}

func (r *fakeAccountRepo) Create(_ context.Context, a domain.ConnectedAccount) (domain.ConnectedAccount, error) {
	r.byID[a.ID] = a
	cp := a
	r.created = &cp
	return a, nil
}

func (r *fakeAccountRepo) GetByID(_ context.Context, id string) (domain.ConnectedAccount, error) {
	a, ok := r.byID[id]
	if !ok {
		return domain.ConnectedAccount{}, domain.ErrNotFound
	}
	return a, nil
}

func (r *fakeAccountRepo) ListByUser(_ context.Context, userID string) ([]domain.ConnectedAccount, error) {
	out := []domain.ConnectedAccount{}
	for _, a := range r.byID {
		if a.UserID == userID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (r *fakeAccountRepo) ListSyncable(_ context.Context) ([]domain.ConnectedAccount, error) {
	out := []domain.ConnectedAccount{}
	for _, a := range r.byID {
		if a.Status == domain.AccountActive || a.Status == domain.AccountSyncing {
			out = append(out, a)
		}
	}
	return out, nil
}

func (r *fakeAccountRepo) Update(_ context.Context, a domain.ConnectedAccount) error {
	r.byID[a.ID] = a
	cp := a
	r.updated = &cp
	return nil
}

func (r *fakeAccountRepo) Delete(_ context.Context, id string) error {
	delete(r.byID, id)
	delete(r.tokens, id)
	r.deleted = append(r.deleted, id)
	return nil
}

func (r *fakeAccountRepo) SaveTokens(_ context.Context, accountID string, t port.TokenSet) error {
	r.tokens[accountID] = t
	return nil
}

func (r *fakeAccountRepo) GetTokens(_ context.Context, accountID string) (port.TokenSet, error) {
	t, ok := r.tokens[accountID]
	if !ok {
		return port.TokenSet{}, domain.ErrNotFound
	}
	return t, nil
}

var _ port.AccountRepo = (*fakeAccountRepo)(nil)

// --- thread repo -------------------------------------------------------------

// fakeThreadRepo persists threads keyed by ID. List records lastQuery and
// returns the programmable listPage (empty by default). Search / snooze-due /
// reminders-due are programmable; the mutating targeted-writes are recorded.
type fakeThreadRepo struct {
	byID map[string]domain.Thread

	// programmable
	listPage     domain.Page[domain.Thread]
	searchResult []domain.Thread
	searchErr    error
	snoozeDue    []domain.Thread
	remindersDue []domain.Thread

	// recording
	markOpened       int
	lastQuery        port.ThreadQuery
	clearedSnooze    []string
	clearedReminder  []string
	lastSetLabelsID  string
	lastSetLabelIDs  []string
	appendSentCalls  int
	lastAppendSentID string
	lastAppendSentAt time.Time
}

func newThreadRepo() *fakeThreadRepo { return &fakeThreadRepo{byID: map[string]domain.Thread{}} }

func (r *fakeThreadRepo) Upsert(_ context.Context, t domain.Thread) (domain.Thread, error) {
	r.byID[t.ID] = t
	return t, nil
}

func (r *fakeThreadRepo) GetByID(_ context.Context, id string) (domain.Thread, error) {
	t, ok := r.byID[id]
	if !ok {
		return domain.Thread{}, domain.ErrNotFound
	}
	return t, nil
}

func (r *fakeThreadRepo) GetByProviderID(_ context.Context, accountID, providerThreadID string) (domain.Thread, error) {
	for _, t := range r.byID {
		if t.AccountID == accountID && t.ProviderThreadID == providerThreadID {
			return t, nil
		}
	}
	return domain.Thread{}, domain.ErrNotFound
}

func (r *fakeThreadRepo) List(_ context.Context, q port.ThreadQuery) (domain.Page[domain.Thread], error) {
	r.lastQuery = q
	if r.listPage.Items == nil {
		return domain.Page[domain.Thread]{Items: []domain.Thread{}}, nil
	}
	return r.listPage, nil
}

func (r *fakeThreadRepo) Update(_ context.Context, t domain.Thread) error {
	r.byID[t.ID] = t
	return nil
}

func (r *fakeThreadRepo) MarkOpened(_ context.Context, id string) error {
	r.markOpened++
	if t, ok := r.byID[id]; ok {
		t.Unread = false
		r.byID[id] = t
	}
	return nil
}

func (r *fakeThreadRepo) SetLabels(_ context.Context, threadID string, labelIDs []string) error {
	r.lastSetLabelsID = threadID
	r.lastSetLabelIDs = labelIDs
	if t, ok := r.byID[threadID]; ok {
		t.LabelIDs = labelIDs
		r.byID[threadID] = t
	}
	return nil
}

func (r *fakeThreadRepo) Search(_ context.Context, userID, query string, limit int) ([]domain.Thread, error) {
	return r.searchResult, r.searchErr
}

func (r *fakeThreadRepo) ListSnoozeDue(_ context.Context, now time.Time, limit int) ([]domain.Thread, error) {
	return r.snoozeDue, nil
}

func (r *fakeThreadRepo) ListRemindersDue(_ context.Context, now time.Time, limit int) ([]domain.Thread, error) {
	return r.remindersDue, nil
}

func (r *fakeThreadRepo) ClearSnooze(_ context.Context, id string) error {
	r.clearedSnooze = append(r.clearedSnooze, id)
	if t, ok := r.byID[id]; ok {
		t.SnoozedUntil = nil
		t.Unread = true
		r.byID[id] = t
	}
	return nil
}

func (r *fakeThreadRepo) ClearReminder(_ context.Context, id string) error {
	r.clearedReminder = append(r.clearedReminder, id)
	if t, ok := r.byID[id]; ok {
		t.RemindAt = nil
		t.Unread = true
		r.byID[id] = t
	}
	return nil
}

func (r *fakeThreadRepo) AppendSentMessage(_ context.Context, id string, sentAt time.Time) error {
	r.appendSentCalls++
	r.lastAppendSentID = id
	r.lastAppendSentAt = sentAt
	if t, ok := r.byID[id]; ok {
		t.MessageCount++
		t.LastMessageAt = sentAt
		r.byID[id] = t
	}
	return nil
}

var _ port.ThreadRepo = (*fakeThreadRepo)(nil)

// --- message repo ------------------------------------------------------------

// fakeMessageRepo preserves Upsert insertion order for ListByThread.
type fakeMessageRepo struct {
	byID  map[string]domain.Message
	order []string
}

func newMessageRepo() *fakeMessageRepo { return &fakeMessageRepo{byID: map[string]domain.Message{}} }

func (r *fakeMessageRepo) Upsert(_ context.Context, m domain.Message) (domain.Message, error) {
	if _, ok := r.byID[m.ID]; !ok {
		r.order = append(r.order, m.ID)
	}
	r.byID[m.ID] = m
	return m, nil
}

func (r *fakeMessageRepo) GetByID(_ context.Context, id string) (domain.Message, error) {
	m, ok := r.byID[id]
	if !ok {
		return domain.Message{}, domain.ErrNotFound
	}
	return m, nil
}

func (r *fakeMessageRepo) GetByProviderID(_ context.Context, accountID, providerMessageID string) (domain.Message, error) {
	for _, id := range r.order {
		if m, ok := r.byID[id]; ok && m.AccountID == accountID && m.ProviderMessageID == providerMessageID {
			return m, nil
		}
	}
	return domain.Message{}, domain.ErrNotFound
}

func (r *fakeMessageRepo) ListByThread(_ context.Context, threadID string) ([]domain.Message, error) {
	out := []domain.Message{}
	for _, id := range r.order {
		if m, ok := r.byID[id]; ok && m.ThreadID == threadID {
			out = append(out, m)
		}
	}
	return out, nil
}

var _ port.MessageRepo = (*fakeMessageRepo)(nil)

// --- draft repo --------------------------------------------------------------

// fakeDraftRepo resolves draft->account->user ownership for ListByUser via the
// injected fakeAccountRepo (Draft carries only AccountID). ClaimScheduled is
// programmable per id; the last claimed id and send-failure args are recorded.
type fakeDraftRepo struct {
	byID     map[string]domain.Draft
	order    []string
	accounts *fakeAccountRepo

	// programmable
	claimOutcome map[string]bool
	scheduledDue []domain.Draft

	// recording
	claimedID       string
	failureCalls    int
	lastFailureID   string
	lastNextAttempt *time.Time
	lastFailureErr  string
}

func newDraftRepo(accounts *fakeAccountRepo) *fakeDraftRepo {
	return &fakeDraftRepo{
		byID:         map[string]domain.Draft{},
		accounts:     accounts,
		claimOutcome: map[string]bool{},
	}
}

func (r *fakeDraftRepo) Create(_ context.Context, d domain.Draft) (domain.Draft, error) {
	if _, ok := r.byID[d.ID]; !ok {
		r.order = append(r.order, d.ID)
	}
	r.byID[d.ID] = d
	return d, nil
}

func (r *fakeDraftRepo) GetByID(_ context.Context, id string) (domain.Draft, error) {
	d, ok := r.byID[id]
	if !ok {
		return domain.Draft{}, domain.ErrNotFound
	}
	return d, nil
}

func (r *fakeDraftRepo) ListByUser(_ context.Context, userID string) ([]domain.Draft, error) {
	out := []domain.Draft{}
	for _, id := range r.order {
		d, ok := r.byID[id]
		if !ok {
			continue
		}
		if r.accounts == nil {
			out = append(out, d)
			continue
		}
		if a, ok := r.accounts.byID[d.AccountID]; ok && a.UserID == userID {
			out = append(out, d)
		}
	}
	return out, nil
}

func (r *fakeDraftRepo) Update(_ context.Context, d domain.Draft) error {
	r.byID[d.ID] = d
	return nil
}

func (r *fakeDraftRepo) Delete(_ context.Context, id string) error {
	delete(r.byID, id)
	return nil
}

func (r *fakeDraftRepo) ListScheduledDue(_ context.Context, now time.Time, limit int) ([]domain.Draft, error) {
	return r.scheduledDue, nil
}

func (r *fakeDraftRepo) ClaimScheduled(_ context.Context, id string) (bool, error) {
	r.claimedID = id
	return r.claimOutcome[id], nil
}

func (r *fakeDraftRepo) RecordSendFailure(_ context.Context, id string, nextAttemptAt *time.Time, errMsg string) error {
	r.failureCalls++
	r.lastFailureID = id
	r.lastNextAttempt = nextAttemptAt
	r.lastFailureErr = errMsg
	if d, ok := r.byID[id]; ok {
		d.SendAttempts++
		d.ScheduledAt = nextAttemptAt
		msg := errMsg
		d.LastError = &msg
		r.byID[id] = d
	}
	return nil
}

var _ port.DraftRepo = (*fakeDraftRepo)(nil)

// --- snippet repo ------------------------------------------------------------

type fakeSnippetRepo struct {
	byID  map[string]domain.Snippet
	order []string
}

func newSnippetRepo() *fakeSnippetRepo { return &fakeSnippetRepo{byID: map[string]domain.Snippet{}} }

func (r *fakeSnippetRepo) Create(_ context.Context, s domain.Snippet) (domain.Snippet, error) {
	if _, ok := r.byID[s.ID]; !ok {
		r.order = append(r.order, s.ID)
	}
	r.byID[s.ID] = s
	return s, nil
}

func (r *fakeSnippetRepo) GetByID(_ context.Context, id string) (domain.Snippet, error) {
	s, ok := r.byID[id]
	if !ok {
		return domain.Snippet{}, domain.ErrNotFound
	}
	return s, nil
}

func (r *fakeSnippetRepo) ListByUser(_ context.Context, userID string) ([]domain.Snippet, error) {
	out := []domain.Snippet{}
	for _, id := range r.order {
		if s, ok := r.byID[id]; ok && s.UserID == userID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (r *fakeSnippetRepo) Update(_ context.Context, s domain.Snippet) error {
	r.byID[s.ID] = s
	return nil
}

func (r *fakeSnippetRepo) Delete(_ context.Context, id string) error {
	delete(r.byID, id)
	return nil
}

var _ port.SnippetRepo = (*fakeSnippetRepo)(nil)

// --- label repo --------------------------------------------------------------

// fakeLabelRepo upserts on (AccountID, ProviderLabelID); a new label without an
// ID is assigned one via newID().
type fakeLabelRepo struct {
	byID  map[string]domain.Label
	order []string
}

func newLabelRepo() *fakeLabelRepo { return &fakeLabelRepo{byID: map[string]domain.Label{}} }

func (r *fakeLabelRepo) Upsert(_ context.Context, l domain.Label) (domain.Label, error) {
	for _, id := range r.order {
		if existing := r.byID[id]; existing.AccountID == l.AccountID && existing.ProviderLabelID == l.ProviderLabelID {
			l.ID = id
			r.byID[id] = l
			return l, nil
		}
	}
	if l.ID == "" {
		l.ID = newID()
	}
	r.byID[l.ID] = l
	r.order = append(r.order, l.ID)
	return l, nil
}

func (r *fakeLabelRepo) ListByAccount(_ context.Context, accountID string) ([]domain.Label, error) {
	out := []domain.Label{}
	for _, id := range r.order {
		if l := r.byID[id]; l.AccountID == accountID {
			out = append(out, l)
		}
	}
	return out, nil
}

var _ port.LabelRepo = (*fakeLabelRepo)(nil)

// --- calendar repo -----------------------------------------------------------

// fakeCalendarRepo upserts on (AccountID, ProviderCalendarID) and preserves the
// local prefs IsVisible/Color on conflict. ListByUser scopes by ownership when
// the optional accounts pointer is set (Calendar carries no UserID); otherwise
// it returns every stored calendar (single-user tests).
type fakeCalendarRepo struct {
	byID     map[string]domain.Calendar
	order    []string
	accounts *fakeAccountRepo
}

func newCalendarRepo() *fakeCalendarRepo { return &fakeCalendarRepo{byID: map[string]domain.Calendar{}} }

func (r *fakeCalendarRepo) Upsert(_ context.Context, c domain.Calendar) (domain.Calendar, error) {
	for _, id := range r.order {
		if existing := r.byID[id]; existing.AccountID == c.AccountID && existing.ProviderCalendarID == c.ProviderCalendarID {
			c.ID = id
			c.IsVisible = existing.IsVisible
			c.Color = existing.Color
			r.byID[id] = c
			return c, nil
		}
	}
	if c.ID == "" {
		c.ID = newID()
	}
	r.byID[c.ID] = c
	r.order = append(r.order, c.ID)
	return c, nil
}

func (r *fakeCalendarRepo) GetByID(_ context.Context, id string) (domain.Calendar, error) {
	c, ok := r.byID[id]
	if !ok {
		return domain.Calendar{}, domain.ErrNotFound
	}
	return c, nil
}

func (r *fakeCalendarRepo) ListByUser(_ context.Context, userID string) ([]domain.Calendar, error) {
	out := []domain.Calendar{}
	for _, id := range r.order {
		c := r.byID[id]
		if r.accounts != nil {
			a, ok := r.accounts.byID[c.AccountID]
			if !ok || a.UserID != userID {
				continue
			}
		}
		out = append(out, c)
	}
	return out, nil
}

func (r *fakeCalendarRepo) ListByAccount(_ context.Context, accountID string) ([]domain.Calendar, error) {
	out := []domain.Calendar{}
	for _, id := range r.order {
		if c := r.byID[id]; c.AccountID == accountID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (r *fakeCalendarRepo) Update(_ context.Context, c domain.Calendar) error {
	r.byID[c.ID] = c
	return nil
}

var _ port.CalendarRepo = (*fakeCalendarRepo)(nil)

// --- event repo --------------------------------------------------------------

// fakeEventRepo stores events by ID in insertion order. ListInRange filters by
// [from,to) overlap and, when calendarIDs is non-empty, by calendar membership
// (user scoping is assumed handled by test seeding). Search is programmable.
type fakeEventRepo struct {
	byID  map[string]domain.Event
	order []string

	searchResult []domain.Event
	searchErr    error
}

func newEventRepo() *fakeEventRepo { return &fakeEventRepo{byID: map[string]domain.Event{}} }

func (r *fakeEventRepo) Upsert(_ context.Context, e domain.Event) (domain.Event, error) {
	if _, ok := r.byID[e.ID]; !ok {
		r.order = append(r.order, e.ID)
	}
	r.byID[e.ID] = e
	return e, nil
}

func (r *fakeEventRepo) GetByID(_ context.Context, id string) (domain.Event, error) {
	e, ok := r.byID[id]
	if !ok {
		return domain.Event{}, domain.ErrNotFound
	}
	return e, nil
}

func (r *fakeEventRepo) GetByProviderID(_ context.Context, calendarID, providerEventID string) (domain.Event, error) {
	for _, id := range r.order {
		if e, ok := r.byID[id]; ok && e.CalendarID == calendarID && e.ProviderEventID == providerEventID {
			return e, nil
		}
	}
	return domain.Event{}, domain.ErrNotFound
}

func (r *fakeEventRepo) ListInRange(_ context.Context, userID string, from, to time.Time, calendarIDs []string) ([]domain.Event, error) {
	filter := map[string]struct{}{}
	for _, id := range calendarIDs {
		filter[id] = struct{}{}
	}
	out := []domain.Event{}
	for _, id := range r.order {
		e, ok := r.byID[id]
		if !ok {
			continue
		}
		if len(filter) > 0 {
			if _, in := filter[e.CalendarID]; !in {
				continue
			}
		}
		if e.End.After(from) && e.Start.Before(to) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (r *fakeEventRepo) Delete(_ context.Context, id string) error {
	delete(r.byID, id)
	return nil
}

func (r *fakeEventRepo) DeleteByProviderID(_ context.Context, calendarID, providerEventID string) error {
	for _, id := range r.order {
		if e, ok := r.byID[id]; ok && e.CalendarID == calendarID && e.ProviderEventID == providerEventID {
			delete(r.byID, id)
		}
	}
	return nil
}

func (r *fakeEventRepo) Search(_ context.Context, userID, query string, limit int) ([]domain.Event, error) {
	return r.searchResult, r.searchErr
}

var _ port.EventRepo = (*fakeEventRepo)(nil)

// --- device repo -------------------------------------------------------------

// fakeDeviceRepo upserts on (UserID, Token) so re-registrations are idempotent.
type fakeDeviceRepo struct {
	byID  map[string]domain.NotificationDevice
	order []string
}

func newDeviceRepo() *fakeDeviceRepo {
	return &fakeDeviceRepo{byID: map[string]domain.NotificationDevice{}}
}

func (r *fakeDeviceRepo) Upsert(_ context.Context, d domain.NotificationDevice) (domain.NotificationDevice, error) {
	for _, id := range r.order {
		if existing, ok := r.byID[id]; ok && existing.UserID == d.UserID && existing.Token == d.Token {
			d.ID = id
			r.byID[id] = d
			return d, nil
		}
	}
	if d.ID == "" {
		d.ID = newID()
	}
	r.byID[d.ID] = d
	r.order = append(r.order, d.ID)
	return d, nil
}

func (r *fakeDeviceRepo) GetByID(_ context.Context, id string) (domain.NotificationDevice, error) {
	d, ok := r.byID[id]
	if !ok {
		return domain.NotificationDevice{}, domain.ErrNotFound
	}
	return d, nil
}

func (r *fakeDeviceRepo) ListByUser(_ context.Context, userID string) ([]domain.NotificationDevice, error) {
	out := []domain.NotificationDevice{}
	for _, id := range r.order {
		if d, ok := r.byID[id]; ok && d.UserID == userID {
			out = append(out, d)
		}
	}
	return out, nil
}

func (r *fakeDeviceRepo) Delete(_ context.Context, id string) error {
	delete(r.byID, id)
	return nil
}

var _ port.DeviceRepo = (*fakeDeviceRepo)(nil)

// --- sync-state repo ---------------------------------------------------------

// fakeSyncStateRepo keys cursors by (accountID, resource) and records every
// account cleared via DeleteByAccount.
type fakeSyncStateRepo struct {
	byKey           map[string]port.SyncState
	deletedAccounts []string
}

func newSyncStateRepo() *fakeSyncStateRepo {
	return &fakeSyncStateRepo{byKey: map[string]port.SyncState{}}
}

func (r *fakeSyncStateRepo) Get(_ context.Context, accountID, resource string) (port.SyncState, error) {
	s, ok := r.byKey[accountID+"\x00"+resource]
	if !ok {
		return port.SyncState{}, domain.ErrNotFound
	}
	return s, nil
}

func (r *fakeSyncStateRepo) Save(_ context.Context, s port.SyncState) error {
	r.byKey[s.AccountID+"\x00"+s.Resource] = s
	return nil
}

func (r *fakeSyncStateRepo) DeleteByAccount(_ context.Context, accountID string) error {
	r.deletedAccounts = append(r.deletedAccounts, accountID)
	for k, s := range r.byKey {
		if s.AccountID == accountID {
			delete(r.byKey, k)
		}
	}
	return nil
}

var _ port.SyncStateRepo = (*fakeSyncStateRepo)(nil)

// --- stripe-event repo -------------------------------------------------------

// fakeStripeEventRepo is faithfully set-backed: Record returns firstTime=true
// the first time an id is seen, false on every replay.
type fakeStripeEventRepo struct {
	seen map[string]string // id -> type
}

func newStripeEventRepo() *fakeStripeEventRepo {
	return &fakeStripeEventRepo{seen: map[string]string{}}
}

func (r *fakeStripeEventRepo) Record(_ context.Context, eventID, eventType string) (bool, error) {
	if _, ok := r.seen[eventID]; ok {
		return false, nil
	}
	r.seen[eventID] = eventType
	return true, nil
}

var _ port.StripeEventRepo = (*fakeStripeEventRepo)(nil)

// --- oauth-state repo --------------------------------------------------------

// fakeOAuthStateRepo persists pending states; Consume atomically fetches and
// deletes (ErrNotFound when missing). created snapshots the last Create arg.
type fakeOAuthStateRepo struct {
	byState map[string]port.OAuthState
	created *port.OAuthState
}

func newOAuthStateRepo() *fakeOAuthStateRepo {
	return &fakeOAuthStateRepo{byState: map[string]port.OAuthState{}}
}

func (r *fakeOAuthStateRepo) Create(_ context.Context, s port.OAuthState) error {
	r.byState[s.State] = s
	cp := s
	r.created = &cp
	return nil
}

func (r *fakeOAuthStateRepo) Consume(_ context.Context, state string) (port.OAuthState, error) {
	s, ok := r.byState[state]
	if !ok {
		return port.OAuthState{}, domain.ErrNotFound
	}
	delete(r.byState, state)
	return s, nil
}

var _ port.OAuthStateRepo = (*fakeOAuthStateRepo)(nil)

// --- oauth gateway -----------------------------------------------------------

// fakeOAuthGateway records the AuthURL/Exchange args and serves a programmable
// exchange token and refresh result. AuthURL returns a URL carrying the state
// unless authURL is set to a verbatim override.
type fakeOAuthGateway struct {
	// programmable
	authURL      string
	token        port.OAuthToken // Exchange result
	exchangeErr  error
	refreshToken port.OAuthToken // Refresh result
	refreshErr   error

	// recording
	authState, authRedirect, authChallenge string
	exchCode, exchRedirect, exchVerifier   string
	refreshCalls                           int
	lastRefreshToken                       string
}

func newOAuthGateway() *fakeOAuthGateway { return &fakeOAuthGateway{} }

func (g *fakeOAuthGateway) AuthURL(state, redirectURI, codeChallenge string) string {
	g.authState, g.authRedirect, g.authChallenge = state, redirectURI, codeChallenge
	if g.authURL != "" {
		return g.authURL
	}
	return "https://provider.example/auth?state=" + state
}

func (g *fakeOAuthGateway) Exchange(_ context.Context, code, redirectURI, codeVerifier string) (port.OAuthToken, error) {
	g.exchCode, g.exchRedirect, g.exchVerifier = code, redirectURI, codeVerifier
	return g.token, g.exchangeErr
}

func (g *fakeOAuthGateway) Refresh(_ context.Context, refreshToken string) (port.OAuthToken, error) {
	g.refreshCalls++
	g.lastRefreshToken = refreshToken
	return g.refreshToken, g.refreshErr
}

var _ port.OAuthGateway = (*fakeOAuthGateway)(nil)

// --- mail provider -----------------------------------------------------------

// fakeMailProvider serves a programmable sync page and send result, records
// every Send, and captures ModifyLabels args exactly as received (nil stays nil).
type fakeMailProvider struct {
	// programmable
	syncPage        port.MailSyncPage
	syncErr         error
	sentResult      port.SentMessage
	sendErr         error
	modifyLabelsErr error

	// recording
	sent               []port.OutgoingMessage
	modifyLabelsCalls  int
	lastModifyToken    string
	lastModifyThreadID string
	lastModifyAdd      []string
	lastModifyRemove   []string
}

func newMailProvider() *fakeMailProvider { return &fakeMailProvider{} }

func (p *fakeMailProvider) SyncMail(_ context.Context, accessToken, cursor string) (port.MailSyncPage, error) {
	return p.syncPage, p.syncErr
}

func (p *fakeMailProvider) Send(_ context.Context, accessToken string, msg port.OutgoingMessage) (port.SentMessage, error) {
	p.sent = append(p.sent, msg)
	return p.sentResult, p.sendErr
}

func (p *fakeMailProvider) ModifyLabels(_ context.Context, accessToken, providerThreadID string, add, remove []string) error {
	p.modifyLabelsCalls++
	p.lastModifyToken = accessToken
	p.lastModifyThreadID = providerThreadID
	p.lastModifyAdd = add
	p.lastModifyRemove = remove
	return p.modifyLabelsErr
}

var _ port.MailProvider = (*fakeMailProvider)(nil)

// --- calendar provider -------------------------------------------------------

// fakeCalendarProvider serves programmable calendars/sync-page/created/updated
// events, records each RSVP response and the last create/update/delete args.
type fakeCalendarProvider struct {
	// programmable
	calendars    []domain.Calendar
	syncPage     port.CalendarSyncPage
	createdEvent domain.Event
	updatedEvent domain.Event

	syncCalendarsErr error
	syncEventsErr    error
	createErr        error
	updateErr        error
	deleteErr        error
	rsvpErr          error

	// recording
	rsvpCalls            []domain.RsvpStatus
	lastCreateCalendarID string
	lastCreateInput      domain.EventInput
	lastUpdateEventID    string
	lastUpdatePatch      domain.EventPatch
	lastDeleteEventID    string
}

func newCalendarProvider() *fakeCalendarProvider { return &fakeCalendarProvider{} }

func (p *fakeCalendarProvider) SyncCalendars(_ context.Context, accessToken string) ([]domain.Calendar, error) {
	return p.calendars, p.syncCalendarsErr
}

func (p *fakeCalendarProvider) SyncEvents(_ context.Context, accessToken, providerCalendarID, cursor string) (port.CalendarSyncPage, error) {
	return p.syncPage, p.syncEventsErr
}

func (p *fakeCalendarProvider) CreateEvent(_ context.Context, accessToken, providerCalendarID string, in domain.EventInput) (domain.Event, error) {
	p.lastCreateCalendarID = providerCalendarID
	p.lastCreateInput = in
	return p.createdEvent, p.createErr
}

func (p *fakeCalendarProvider) UpdateEvent(_ context.Context, accessToken, providerCalendarID, providerEventID string, patch domain.EventPatch) (domain.Event, error) {
	p.lastUpdateEventID = providerEventID
	p.lastUpdatePatch = patch
	return p.updatedEvent, p.updateErr
}

func (p *fakeCalendarProvider) DeleteEvent(_ context.Context, accessToken, providerCalendarID, providerEventID string) error {
	p.lastDeleteEventID = providerEventID
	return p.deleteErr
}

func (p *fakeCalendarProvider) RSVP(_ context.Context, accessToken, providerCalendarID, providerEventID string, response domain.RsvpStatus) error {
	p.rsvpCalls = append(p.rsvpCalls, response)
	return p.rsvpErr
}

var _ port.CalendarProvider = (*fakeCalendarProvider)(nil)

// --- payments ----------------------------------------------------------------

// fakePayments serves a programmable customer id, checkout/portal URLs and a
// parsed webhook event, recording the calls billing.go makes.
type fakePayments struct {
	// programmable
	customerID      string
	checkoutURL     string
	portalURL       string
	webhookEvent    port.WebhookEvent
	parseWebhookErr error
	ensureErr       error
	checkoutErr     error
	portalErr       error

	// recording
	ensureCustomerCalls  int
	lastEnsureUser       domain.User
	lastCheckoutParams   port.CheckoutParams
	lastPortalCustomerID string
	lastPortalReturnURL  string
}

func newPayments() *fakePayments { return &fakePayments{} }

func (p *fakePayments) EnsureCustomer(_ context.Context, user domain.User) (string, error) {
	p.ensureCustomerCalls++
	p.lastEnsureUser = user
	return p.customerID, p.ensureErr
}

func (p *fakePayments) CreateCheckoutSession(_ context.Context, params port.CheckoutParams) (string, error) {
	p.lastCheckoutParams = params
	return p.checkoutURL, p.checkoutErr
}

func (p *fakePayments) CreatePortalSession(_ context.Context, customerID, returnURL string) (string, error) {
	p.lastPortalCustomerID = customerID
	p.lastPortalReturnURL = returnURL
	return p.portalURL, p.portalErr
}

func (p *fakePayments) ParseWebhook(payload []byte, sigHeader string) (port.WebhookEvent, error) {
	return p.webhookEvent, p.parseWebhookErr
}

var _ port.Payments = (*fakePayments)(nil)

// --- ai ----------------------------------------------------------------------

// fakeAI serves programmable text/model/err and records the last prompts.
type fakeAI struct {
	text       string
	model      string
	err        error
	lastSystem string
	lastUser   string
}

func newAI() *fakeAI { return &fakeAI{} }

func (a *fakeAI) Complete(_ context.Context, system, user string) (string, string, error) {
	a.lastSystem, a.lastUser = system, user
	return a.text, a.model, a.err
}

var _ port.AI = (*fakeAI)(nil)

// --- push --------------------------------------------------------------------

// fakePush records every fan-out and can be programmed to fail.
type fakePush struct {
	err  error
	sent []struct {
		Device domain.NotificationDevice
		Title  string
		Body   string
		Data   map[string]string
	}
}

func newPush() *fakePush { return &fakePush{} }

func (p *fakePush) Send(_ context.Context, device domain.NotificationDevice, title, body string, data map[string]string) error {
	p.sent = append(p.sent, struct {
		Device domain.NotificationDevice
		Title  string
		Body   string
		Data   map[string]string
	}{Device: device, Title: title, Body: body, Data: data})
	return p.err
}

var _ port.PushSender = (*fakePush)(nil)
```

- [ ] **Step 2: Delete the inline stubs in `ops_test.go`.** They span from the `// --- fakes ...` banner (line 15) through the end of `stubOAuthGateway.Refresh` (line 152). Remove the whole block with this edit (old → new):

  Old (the entire stub region):
```go
// --- fakes -------------------------------------------------------------------

type stubAccountRepo struct {
	port.AccountRepo
	byID           map[string]domain.ConnectedAccount
	updated        *domain.ConnectedAccount
	created        *domain.ConnectedAccount
	savedTokensFor string
}

func (r *stubAccountRepo) GetByID(_ context.Context, id string) (domain.ConnectedAccount, error) {
	a, ok := r.byID[id]
	if !ok {
		return domain.ConnectedAccount{}, domain.ErrNotFound
	}
	return a, nil
}

func (r *stubAccountRepo) ListByUser(_ context.Context, userID string) ([]domain.ConnectedAccount, error) {
	out := []domain.ConnectedAccount{}
	for _, a := range r.byID {
		if a.UserID == userID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (r *stubAccountRepo) Create(_ context.Context, a domain.ConnectedAccount) (domain.ConnectedAccount, error) {
	if r.byID == nil {
		r.byID = map[string]domain.ConnectedAccount{}
	}
	r.byID[a.ID] = a
	r.created = &a
	return a, nil
}

func (r *stubAccountRepo) Update(_ context.Context, a domain.ConnectedAccount) error {
	if r.byID == nil {
		r.byID = map[string]domain.ConnectedAccount{}
	}
	r.byID[a.ID] = a
	r.updated = &a
	return nil
}

func (r *stubAccountRepo) SaveTokens(_ context.Context, accountID string, _ port.TokenSet) error {
	r.savedTokensFor = accountID
	return nil
}

type stubThreadRepo struct {
	port.ThreadRepo
	byID       map[string]domain.Thread
	markOpened int
	lastQuery  port.ThreadQuery
}

func (r *stubThreadRepo) GetByID(_ context.Context, id string) (domain.Thread, error) {
	t, ok := r.byID[id]
	if !ok {
		return domain.Thread{}, domain.ErrNotFound
	}
	return t, nil
}

func (r *stubThreadRepo) MarkOpened(_ context.Context, _ string) error {
	r.markOpened++
	return nil
}

func (r *stubThreadRepo) List(_ context.Context, q port.ThreadQuery) (domain.Page[domain.Thread], error) {
	r.lastQuery = q
	return domain.Page[domain.Thread]{Items: []domain.Thread{}}, nil
}

type stubDraftRepo struct {
	port.DraftRepo
	byID        map[string]domain.Draft
	claimResult bool
	claimedID   string
}

func (r *stubDraftRepo) GetByID(_ context.Context, id string) (domain.Draft, error) {
	d, ok := r.byID[id]
	if !ok {
		return domain.Draft{}, domain.ErrNotFound
	}
	return d, nil
}

func (r *stubDraftRepo) ClaimScheduled(_ context.Context, id string) (bool, error) {
	r.claimedID = id
	return r.claimResult, nil
}

type stubOAuthStateRepo struct {
	created *port.OAuthState
	byState map[string]port.OAuthState
}

func (r *stubOAuthStateRepo) Create(_ context.Context, s port.OAuthState) error {
	if r.byState == nil {
		r.byState = map[string]port.OAuthState{}
	}
	r.byState[s.State] = s
	r.created = &s
	return nil
}

func (r *stubOAuthStateRepo) Consume(_ context.Context, state string) (port.OAuthState, error) {
	s, ok := r.byState[state]
	if !ok {
		return port.OAuthState{}, domain.ErrNotFound
	}
	delete(r.byState, state)
	return s, nil
}

type stubOAuthGateway struct {
	authRedirect, authChallenge string
	exchRedirect, exchVerifier  string
	token                       port.OAuthToken
}

func (g *stubOAuthGateway) AuthURL(state, redirectURI, codeChallenge string) string {
	g.authRedirect, g.authChallenge = redirectURI, codeChallenge
	return "https://provider.example/auth?state=" + state
}

func (g *stubOAuthGateway) Exchange(_ context.Context, _, redirectURI, codeVerifier string) (port.OAuthToken, error) {
	g.exchRedirect, g.exchVerifier = redirectURI, codeVerifier
	return g.token, nil
}

func (g *stubOAuthGateway) Refresh(context.Context, string) (port.OAuthToken, error) {
	return port.OAuthToken{}, nil
}
```

  New (replace with just the section banner that follows):
```go
// --- MailService.UnsendDraft -------------------------------------------------
```

  The imports block at the top of `ops_test.go` is unchanged — `context`, `errors`, `reflect`, `strings`, `testing`, `time`, `domain`, `port` all stay in use by the tests below.

- [ ] **Step 3: Rewire the five test call sites to the shared fakes.** Field names the assertions read (`accounts.created`, `accounts.updated`, `threads.markOpened`, `threads.lastQuery`, `drafts.claimedID`, `states.created`, `gw.authRedirect`, `gw.authChallenge`, `gw.exchRedirect`, `gw.exchVerifier`, `gw.token`) are all preserved by the fakes, so only the constructor calls change. The one behavioral rename: `stubDraftRepo.claimResult bool` becomes `fakeDraftRepo.claimOutcome map[string]bool` keyed by draft id.

  3a. `TestUnsendDraft` — swap stub literals for constructors and set the per-id claim outcome:
```go
			accounts := newAccountRepo()
			accounts.byID["a1"] = acct
			drafts := newDraftRepo(accounts)
			if tt.draft != nil {
				drafts.byID["d1"] = *tt.draft
			}
			drafts.claimOutcome["d1"] = tt.claim
			svc := NewMailService(MailServiceDeps{Accounts: accounts, Drafts: drafts, Clock: SystemClock{}, SelfHosted: true})
```
  (replaces the old three lines that built `&stubAccountRepo{...}` / `&stubDraftRepo{..., claimResult: tt.claim}` and seeded `drafts.byID["d1"]`.)

  3b. `TestMarkThreadOpenedIdempotentAndOwned` — build the fakes and seed their maps:
```go
	accounts := newAccountRepo()
	accounts.byID["a1"] = domain.ConnectedAccount{ID: "a1", UserID: owner}
	threads := newThreadRepo()
	threads.byID["t1"] = domain.Thread{ID: "t1", AccountID: "a1"}
	svc := NewMailService(MailServiceDeps{Accounts: accounts, Threads: threads, Clock: SystemClock{}, SelfHosted: true})
```

  3c. `TestListThreadsForwardsView` — replace `threads := &stubThreadRepo{}`:
```go
	threads := newThreadRepo()
```

  3d. `TestSetVipSenders` — replace the `&stubAccountRepo{...}` literal:
```go
	accounts := newAccountRepo()
	accounts.byID["a1"] = domain.ConnectedAccount{ID: "a1", UserID: owner, Email: "me@x.com"}
```

  3e. `TestBeginConnectUsesCallbackAndPKCE`, `TestBeginConnectDerivesCallbackFromRequest`, `TestCompleteConnectReplaysVerifierAndReturnsRedirect` — swap the three gateway/state/account constructions. For each of the first two:
```go
	gw := newOAuthGateway()
	states := newOAuthStateRepo()
	oauth := map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: gw} // ProviderMicrosoft in the second test
	svc := NewAccountService(newAccountRepo(), states, nil, oauth,
		[]string{"https://app.example.com"}, "https://api.example.com", SystemClock{})
```
  And for `TestCompleteConnectReplaysVerifierAndReturnsRedirect`, set the programmable exchange token after construction:
```go
	gw := newOAuthGateway()
	gw.token = port.OAuthToken{Email: "me@x.com", Scopes: []string{"scope"}}
	accounts := newAccountRepo()
	states := newOAuthStateRepo()
	oauth := map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: gw}
	svc := NewAccountService(accounts, states, nil, oauth,
		[]string{"https://app.example.com"}, "https://api.example.com", SystemClock{})
```
  Every assertion body below these constructions is unchanged.

- [ ] **Step 4: Compile-check, run, and commit.** The suite must stay GREEN.
```bash
cd backend && gofmt -w internal/service/fakes_test.go internal/service/ops_test.go
cd backend && go vet ./internal/service/...
cd backend && go test ./internal/service/... -run . -count=1
```
```bash
cd backend && git add internal/service/fakes_test.go internal/service/ops_test.go
git commit -m "test(service): add shared in-memory port fakes; retire ops_test inline stubs

Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 2: Domain layer pure validator + invariant tests

**Files:**
- Create/Test: `backend/internal/domain/domain_test.go` (`package domain`, white-box).
- Do NOT modify `backend/internal/domain/mail_test.go` — it already owns `TestParseThreadView`; do not duplicate it here.

**Interfaces:**
- Consumes: nothing from other tasks. This is the pure domain package and it **cannot** import the service-package shared fakes (`fakeClock`, repos, gateways) — that would be an import cycle / wrong package. Time is injected via explicit fixed `time.Time` values built with `time.Date(...)`; no `time.Now()` appears in any assertion.
- Produces (test identifiers, all `package domain`, unqualified sentinel/func names): helper `checkValidator[T ~string]`; tests `TestParseProvider`, `TestParseAiAction`, `TestParseRsvpStatus`, `TestParseInboxSplit`, `TestParseThreadAction`, `TestParseDevicePlatform`, `TestSubscriptionHasAccess`, `TestPageZeroValue`, `TestPagePopulated`.

Notes verified against the real source before writing:
- All `Parse*` validators live in different files but the same `package domain`: `ParseProvider` (`account.go`), `ParseAiAction` (`ai.go`), `ParseRsvpStatus` (`calendar.go`), `ParseInboxSplit` + `ParseThreadAction` (`mail.go`), `ParseDevicePlatform` (`notification.go`). Each returns `("", fmt.Errorf("%w: ...", ErrValidation))` on an unknown value, so on error the returned enum is the empty string and `errors.Is(err, ErrValidation)` holds. Matching is case-sensitive exact-string.
- `Subscription.HasAccess(now)` (`subscription.go`): `trialing`/`active` → true unconditionally; `past_due` → false when `CurrentPeriodEnd == nil`, else `now.Before(*CurrentPeriodEnd + PastDueGrace)` (strict `Before`); every other status → false. `PastDueGrace == 7 * 24 * time.Hour`.
- `Page[T]` (`page.go`) has exactly two fields: `Items []T` and `NextCursor *string`.

---

- [ ] **Step 1: Write the validator tests** — create `backend/internal/domain/domain_test.go` with the generic validator helper and one table-driven test per `Parse*` function. Each valid value must round-trip (`string(got) == input`); each invalid value must return `ErrValidation` (via `errors.Is`) and the empty enum.

```go
package domain

import (
	"errors"
	"testing"
	"time"
)

// checkValidator exercises a Parse* validator: every value in valid must
// round-trip (parse ok and stringify back to the input), and every value in
// invalid must fail with ErrValidation and yield the zero enum value.
func checkValidator[T ~string](t *testing.T, name string, parse func(string) (T, error), valid, invalid []string) {
	t.Helper()
	for _, s := range valid {
		s := s
		t.Run(name+"/valid/"+s, func(t *testing.T) {
			got, err := parse(s)
			if err != nil {
				t.Fatalf("%s(%q) unexpected err: %v", name, s, err)
			}
			if string(got) != s {
				t.Fatalf("%s(%q) = %q, want round-trip %q", name, s, string(got), s)
			}
		})
	}
	for _, s := range invalid {
		s := s
		label := s
		if label == "" {
			label = "<empty>"
		}
		t.Run(name+"/invalid/"+label, func(t *testing.T) {
			got, err := parse(s)
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("%s(%q) err = %v, want ErrValidation", name, s, err)
			}
			if string(got) != "" {
				t.Fatalf("%s(%q) = %q, want zero value on error", name, s, string(got))
			}
		})
	}
}

func TestParseProvider(t *testing.T) {
	checkValidator(t, "ParseProvider", ParseProvider,
		[]string{"google", "microsoft"},
		[]string{"", "Google", "GOOGLE", "yahoo", "gmail", " google"})
}

func TestParseAiAction(t *testing.T) {
	checkValidator(t, "ParseAiAction", ParseAiAction,
		[]string{"compose", "reply", "summarize", "ask"},
		[]string{"", "Compose", "translate", "summarise", "answer"})
}

func TestParseRsvpStatus(t *testing.T) {
	checkValidator(t, "ParseRsvpStatus", ParseRsvpStatus,
		[]string{"accepted", "declined", "tentative", "needs_action"},
		[]string{"", "maybe", "needsAction", "needs-action", "accept"})
}

func TestParseInboxSplit(t *testing.T) {
	checkValidator(t, "ParseInboxSplit", ParseInboxSplit,
		[]string{"important", "vip", "team", "calendar", "news", "social", "other"},
		[]string{"", "VIP", "inbox", "spam", "starred", "promotions"})
}

func TestParseThreadAction(t *testing.T) {
	checkValidator(t, "ParseThreadAction", ParseThreadAction,
		[]string{"archive", "trash", "star", "unstar", "read", "unread", "spam", "move_to_inbox"},
		[]string{"", "delete", "moveToInbox", "move-to-inbox", "snooze", "Archive"})
}

func TestParseDevicePlatform(t *testing.T) {
	checkValidator(t, "ParseDevicePlatform", ParseDevicePlatform,
		[]string{"ios", "android", "web", "macos", "windows", "linux"},
		[]string{"", "iOS", "macOS", "blackberry", "tvos", "desktop"})
}
```

- [ ] **Step 2: Run** the validator tests — Expected PASS.

```bash
cd backend && go test ./internal/domain/ -run 'TestParse' -v
```

Expected PASS (this `-run` also re-runs the pre-existing `TestParseThreadView`, which must keep passing). If any invalid case unexpectedly parses OK, that is a real widened-validator bug — record it and fix in a separate commit; if a valid value fails, re-check the exact string constant in the source.

**Required cases** (every one is mandatory coverage; all encoded in Step 1):
- `ParseProvider` valid: `google`, `microsoft` → round-trip. Invalid: `""`, `Google`, `GOOGLE`, `yahoo`, `gmail`, `" google"` → `domain.ErrValidation`.
- `ParseAiAction` valid: `compose`, `reply`, `summarize`, `ask` → round-trip. Invalid: `""`, `Compose`, `translate`, `summarise`, `answer` → `domain.ErrValidation`.
- `ParseRsvpStatus` valid: `accepted`, `declined`, `tentative`, `needs_action` → round-trip. Invalid: `""`, `maybe`, `needsAction`, `needs-action`, `accept` → `domain.ErrValidation`.
- `ParseInboxSplit` valid: `important`, `vip`, `team`, `calendar`, `news`, `social`, `other` → round-trip. Invalid: `""`, `VIP`, `inbox`, `spam`, `starred`, `promotions` → `domain.ErrValidation`.
- `ParseThreadAction` valid: `archive`, `trash`, `star`, `unstar`, `read`, `unread`, `spam`, `move_to_inbox` → round-trip. Invalid: `""`, `delete`, `moveToInbox`, `move-to-inbox`, `snooze`, `Archive` → `domain.ErrValidation`.
- `ParseDevicePlatform` valid: `ios`, `android`, `web`, `macos`, `windows`, `linux` → round-trip. Invalid: `""`, `iOS`, `macOS`, `blackberry`, `tvos`, `desktop` → `domain.ErrValidation`.

---

- [ ] **Step 3: Write the `Subscription.HasAccess` invariant test** — append to `domain_test.go`. Exhaustively cover the paywall boundary with explicit fixed times (7-day `PastDueGrace` boundary is exercised down to the nanosecond).

```go
func TestSubscriptionHasAccess(t *testing.T) {
	// Fixed reference instant; no time.Now() anywhere in the assertions.
	periodEnd := time.Date(2026, time.July, 7, 12, 0, 0, 0, time.UTC)
	graceEnd := periodEnd.Add(PastDueGrace) // periodEnd + 7*24h

	ptr := func(tm time.Time) *time.Time { return &tm }

	tests := []struct {
		name string
		sub  Subscription
		now  time.Time
		want bool
	}{
		{
			name: "trialing always has access",
			sub:  Subscription{Status: SubscriptionTrialing},
			now:  periodEnd,
			want: true,
		},
		{
			name: "active always has access",
			sub:  Subscription{Status: SubscriptionActive},
			now:  periodEnd,
			want: true,
		},
		{
			name: "past_due with nil period end has no access",
			sub:  Subscription{Status: SubscriptionPastDue, CurrentPeriodEnd: nil},
			now:  periodEnd,
			want: false,
		},
		{
			name: "past_due just before end+grace keeps access",
			sub:  Subscription{Status: SubscriptionPastDue, CurrentPeriodEnd: ptr(periodEnd)},
			now:  graceEnd.Add(-time.Nanosecond),
			want: true,
		},
		{
			name: "past_due exactly at end+grace loses access (Before is strict)",
			sub:  Subscription{Status: SubscriptionPastDue, CurrentPeriodEnd: ptr(periodEnd)},
			now:  graceEnd,
			want: false,
		},
		{
			name: "past_due after end+grace loses access",
			sub:  Subscription{Status: SubscriptionPastDue, CurrentPeriodEnd: ptr(periodEnd)},
			now:  graceEnd.Add(time.Hour),
			want: false,
		},
		{
			// Future period end is irrelevant once canceled — status wins.
			name: "canceled has no access even with future period end",
			sub:  Subscription{Status: SubscriptionCanceled, CurrentPeriodEnd: ptr(graceEnd.Add(365 * 24 * time.Hour))},
			now:  periodEnd,
			want: false,
		},
		{
			name: "expired has no access",
			sub:  Subscription{Status: SubscriptionExpired},
			now:  periodEnd,
			want: false,
		},
		{
			name: "none has no access",
			sub:  Subscription{Status: SubscriptionNone},
			now:  periodEnd,
			want: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.sub.HasAccess(tt.now); got != tt.want {
				t.Fatalf("HasAccess(%v) with status %q = %v, want %v", tt.now, tt.sub.Status, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 4: Run** the access test — Expected PASS.

```bash
cd backend && go test ./internal/domain/ -run TestSubscriptionHasAccess -v
```

Expected PASS. If the `graceEnd` (exactly end+7d) case returns `true`, the implementation used `!After`/`<=` instead of strict `Before` — that is a real off-by-one bug: record it, keep the test as the spec, and fix in a separate commit.

**Required cases** (all encoded in Step 3):
- `Subscription.HasAccess` status `trialing` → `true`.
- status `active` → `true`.
- status `past_due`, `CurrentPeriodEnd == nil` → `false`.
- status `past_due`, `now == end+7d − 1ns` → `true`.
- status `past_due`, `now == end+7d` exactly → `false` (strict `Before`).
- status `past_due`, `now == end+7d + 1h` → `false`.
- status `canceled` (even with a far-future `CurrentPeriodEnd`) → `false`.
- status `expired` → `false`.
- status `none` → `false`.

---

- [ ] **Step 5: Write the `Page[T]` envelope tests** — append to `domain_test.go`. Cover the zero value (nil `Items`, nil `NextCursor`) and a populated value (non-empty `Items`, non-nil `NextCursor` pointer that dereferences to the set token).

```go
func TestPageZeroValue(t *testing.T) {
	var p Page[Thread]
	if p.Items != nil {
		t.Fatalf("zero Page.Items = %v, want nil", p.Items)
	}
	if p.NextCursor != nil {
		t.Fatalf("zero Page.NextCursor = %v, want nil", p.NextCursor)
	}
}

func TestPagePopulated(t *testing.T) {
	cursor := "next-page-token"
	p := Page[int]{
		Items:      []int{1, 2, 3},
		NextCursor: &cursor,
	}
	if len(p.Items) != 3 || p.Items[0] != 1 || p.Items[2] != 3 {
		t.Fatalf("Page.Items = %v, want [1 2 3]", p.Items)
	}
	if p.NextCursor == nil {
		t.Fatalf("Page.NextCursor = nil, want non-nil")
	}
	if *p.NextCursor != cursor {
		t.Fatalf("*Page.NextCursor = %q, want %q", *p.NextCursor, cursor)
	}
}
```

- [ ] **Step 6: Run** the Page tests — Expected PASS.

```bash
cd backend && go test ./internal/domain/ -run TestPage -v
```

Expected PASS.

**Required cases** (all encoded in Step 5):
- `Page[Thread]` zero value → `Items == nil` and `NextCursor == nil`.
- `Page[int]{Items: []int{1,2,3}, NextCursor: &"next-page-token"}` → `len(Items) == 3`, first/last elements `1`/`3`, `NextCursor != nil`, `*NextCursor == "next-page-token"`.

---

- [ ] **Step 7: Run the full domain package, then commit** — confirm the whole `domain` package (including the pre-existing `mail_test.go`) is green, then commit the new file only.

```bash
cd backend && go test ./internal/domain/
git add backend/internal/domain/domain_test.go
git commit -m "test(domain): characterize Parse* validators, HasAccess grace boundary, Page envelope"
```

Expected: `ok  	calendium/backend/internal/domain`. The commit adds only `domain_test.go`; if a real-bug fix was needed at Step 2 or Step 4, that production-code change goes in its own separate commit (never squashed into this test commit).

---

### Task 3: Billing service — paid-path characterization (`BillingService`)

Coverage tests for the paid (`selfHosted=false`) Stripe flow in
`backend/internal/service/billing.go`. Self-host bypass / `ErrSelfHosted` are
already covered by `selfhost_test.go` (`TestBillingSelfHost`,
`TestEntitlementSelfHostBypass`) — do NOT duplicate those; every service here is
built with `selfHosted=false`.

**Files:**
- Create/Test: `backend/internal/service/billing_test.go` (package `service`, white-box).
- Consumes: `backend/internal/service/fakes_test.go` (Task 1).

**Interfaces:**
- Consumes fakes (Task 1): `newUserRepo() *fakeUserRepo`, `newSubscriptionRepo() *fakeSubscriptionRepo`, `newStripeEventRepo() *fakeStripeEventRepo`, `newPayments() *fakePayments`, `newTxRunner() *fakeTxRunner`, `newClock(t time.Time) *fakeClock`.
- Wiring: `NewBillingService(users port.UserRepo, subs port.SubscriptionRepo, events port.StripeEventRepo, payments port.Payments, clock port.Clock, tx port.TxRunner, selfHosted bool) *BillingService`.
- Produces: `TestGetSubscriptionPaidPath`, `TestCreateCheckoutSessionTrialLogic`, `TestCreateCheckoutSessionValidation`, `TestCreatePortalSession`, `TestHandleWebhookIdempotentReplay`, `TestHandleWebhookApplies`, `TestHandleWebhookParseError`, `TestHandleWebhookOutOfOrder`, `TestHandleWebhookPreservesInvoiceFields`, `TestRequireActivePaidPath`.

Seed repos through their port methods (`subs.Upsert`, `users.Upsert`, `events.Record`) so the fakes persist and later reads return the seeded rows — no assumption about private seed helpers.

- [ ] **Step 1: Write the failing test** — representative #1, `CreateCheckoutSession` trial logic (first-time vs returning subscriber).

```go
package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// TestCreateCheckoutSessionTrialLogic pins the paid-path trial rule
// (billing.go): a first-time subscriber (no StripeSubscriptionID) gets a
// 14-day trial and a freshly-ensured Stripe customer persisted via Upsert; a
// returning subscriber gets TrialDays=0 and reuses the stored customer.
func TestCreateCheckoutSessionTrialLogic(t *testing.T) {
	const userID = "u1"
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	tests := []struct {
		name           string
		seed           *domain.Subscription // existing row; nil => none yet
		wantTrialDays  int
		wantEnsure     bool // EnsureCustomer must be invoked (no customer yet)
		wantCustomerID string
	}{
		{
			name:           "first-time subscriber gets 14-day trial and customer persisted",
			seed:           nil,
			wantTrialDays:  trialDays, // 14
			wantEnsure:     true,
			wantCustomerID: "cus_new",
		},
		{
			name: "returning subscriber gets no trial and reuses customer",
			seed: &domain.Subscription{
				UserID:               userID,
				Status:               domain.SubscriptionActive,
				StripeCustomerID:     "cus_old",
				StripeSubscriptionID: "sub_old",
			},
			wantTrialDays:  0,
			wantEnsure:     false,
			wantCustomerID: "cus_old",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			clock := newClock(now)
			users := newUserRepo()
			if _, err := users.Upsert(ctx, domain.User{ID: userID, Email: "me@x.com"}); err != nil {
				t.Fatal(err)
			}
			subs := newSubscriptionRepo()
			if tt.seed != nil {
				if err := subs.Upsert(ctx, *tt.seed); err != nil {
					t.Fatal(err)
				}
			}
			payments := newPayments()
			payments.customerID = "cus_new"           // EnsureCustomer return
			payments.checkoutURL = "https://co.test/1" // CreateCheckoutSession return

			b := NewBillingService(users, subs, newStripeEventRepo(), payments, clock, newTxRunner(), false)

			url, err := b.CreateCheckoutSession(ctx, userID, "https://app/success", "https://app/cancel")
			if err != nil {
				t.Fatalf("CreateCheckoutSession: %v", err)
			}
			if url != "https://co.test/1" {
				t.Fatalf("url = %q, want the payments checkout url", url)
			}
			p := payments.lastCheckoutParams
			if p.TrialDays != tt.wantTrialDays {
				t.Fatalf("TrialDays = %d, want %d", p.TrialDays, tt.wantTrialDays)
			}
			if p.CustomerID != tt.wantCustomerID {
				t.Fatalf("CheckoutParams.CustomerID = %q, want %q", p.CustomerID, tt.wantCustomerID)
			}
			if p.UserID != userID {
				t.Fatalf("CheckoutParams.UserID = %q, want %q", p.UserID, userID)
			}
			if p.SuccessURL != "https://app/success" || p.CancelURL != "https://app/cancel" {
				t.Fatalf("checkout URLs not forwarded: %+v", p)
			}
			if got := payments.ensureCustomerCalls > 0; got != tt.wantEnsure {
				t.Fatalf("EnsureCustomer called = %v, want %v", got, tt.wantEnsure)
			}
			// The (existing or freshly-ensured) customer id is mirrored on the row.
			persisted, err := subs.GetByUserID(ctx, userID)
			if err != nil {
				t.Fatalf("subscription not persisted: %v", err)
			}
			if persisted.StripeCustomerID != tt.wantCustomerID {
				t.Fatalf("persisted StripeCustomerID = %q, want %q", persisted.StripeCustomerID, tt.wantCustomerID)
			}
		})
	}
}
```

- [ ] **Step 2: Run** — `cd backend && go test ./internal/service/ -run TestCreateCheckoutSessionTrialLogic -v` — **Expected PASS**.
- [ ] **Step 3: Commit** — `cd backend && git add internal/service/billing_test.go && git commit -m $'test(service): pin billing checkout trial logic\n\nCo-Authored-By: WOZCODE <contact@withwoz.com>'`

- [ ] **Step 1 (representative #2): Write the failing test** — `HandleWebhook` idempotent replay (record-after-success + tx wrapping).

```go
// TestHandleWebhookIdempotentReplay proves the replay guard: an event id
// already recorded is a no-op — applyWebhookEvent is skipped, the existing
// active status is NOT downgraded — and handling still runs inside a tx.
func TestHandleWebhookIdempotentReplay(t *testing.T) {
	ctx := context.Background()
	const userID = "u1"
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	subs := newSubscriptionRepo()
	if err := subs.Upsert(ctx, domain.Subscription{
		UserID:           userID,
		Status:           domain.SubscriptionActive,
		StripeCustomerID: "cus_1",
	}); err != nil {
		t.Fatal(err)
	}

	events := newStripeEventRepo()
	// Pre-record so the fake reports firstTime=false for this id on replay.
	if _, err := events.Record(ctx, "evt_1", "customer.subscription.updated"); err != nil {
		t.Fatal(err)
	}

	created := now
	payments := newPayments()
	payments.webhookEvent = port.WebhookEvent{
		ID:         "evt_1",
		Type:       "customer.subscription.updated",
		UserID:     userID,
		CustomerID: "cus_1",
		Status:     domain.SubscriptionCanceled, // would downgrade IF applied
		Created:    &created,
	}
	tx := newTxRunner()

	b := NewBillingService(newUserRepo(), subs, events, payments, newClock(now), tx, false)

	if err := b.HandleWebhook(ctx, []byte("{}"), "sig"); err != nil {
		t.Fatalf("HandleWebhook: %v", err)
	}
	got, err := subs.GetByUserID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.SubscriptionActive {
		t.Fatalf("status = %q, want %q (replay must not re-apply)", got.Status, domain.SubscriptionActive)
	}
	if tx.calls == 0 {
		t.Fatal("expected RunInTx to wrap webhook handling")
	}
}
```

- [ ] **Step 2: Run** — `cd backend && go test ./internal/service/ -run TestHandleWebhookIdempotentReplay -v` — **Expected PASS**.
- [ ] **Step 3: Commit** — `cd backend && git add internal/service/billing_test.go && git commit -m $'test(service): pin billing webhook idempotent replay\n\nCo-Authored-By: WOZCODE <contact@withwoz.com>'`

- [ ] **Step 4: Write the remaining Task 3 tests** (enumerated below), then **Run** `cd backend && go test ./internal/service/ -run 'TestGetSubscriptionPaidPath|TestCreateCheckoutSessionValidation|TestCreatePortalSession|TestHandleWebhookApplies|TestHandleWebhookParseError|TestHandleWebhookOutOfOrder|TestHandleWebhookPreservesInvoiceFields|TestRequireActivePaidPath' -v` — **Expected PASS** — then **Commit** `cd backend && git add internal/service/billing_test.go && git commit -m $'test(service): cover billing subscription, portal, webhook ordering, entitlement\n\nCo-Authored-By: WOZCODE <contact@withwoz.com>'`.

**Required cases (each mandatory; `selfHosted=false`, sentinels compared with `errors.Is`):**

- `TestGetSubscriptionPaidPath` — `BillingService.GetSubscription`, repo miss: `subs` empty (`GetByUserID` returns `domain.ErrNotFound`) => returns a placeholder with `Status==domain.SubscriptionNone`, `Plan==domain.PlanAnnual`, `PriceUSD==domain.PriceUSDAnnual`, `UserID==userID`, and **nil error**.
- `TestGetSubscriptionPaidPath` — `GetSubscription`, active: seed via `subs.Upsert` a sub `{UserID, Status: domain.SubscriptionActive, StripeCustomerID:"cus_1"}` => returned value equals the seeded row (`Status==domain.SubscriptionActive`, `StripeCustomerID=="cus_1"`), nil error.
- `TestCreateCheckoutSessionValidation` — `CreateCheckoutSession` with `successURL==""` => `domain.ErrValidation`; with `cancelURL==""` => `domain.ErrValidation` (table-driven, both empty-string variants); assert `payments.ensureCustomerCalls==0` and `payments.lastCheckoutParams` is the zero value (short-circuit before any Stripe call).
- `TestCreatePortalSession` — no billing profile: `subs` empty (`GetByUserID` => `domain.ErrNotFound`) => `domain.ErrValidation`; assert `payments` portal fields untouched.
- `TestCreatePortalSession` — profile without customer: seed sub `{UserID, StripeCustomerID:""}` => `domain.ErrValidation`.
- `TestCreatePortalSession` — empty returnURL: `returnURL==""` => `domain.ErrValidation` (checked before the repo read).
- `TestCreatePortalSession` — happy path: seed sub `{UserID, StripeCustomerID:"cus_1"}`, set `payments.portalURL="https://portal.test/x"` => returned url `=="https://portal.test/x"`, `payments.lastPortalCustomerID=="cus_1"`, `payments.lastPortalReturnURL==returnURL`, nil error.
- `TestHandleWebhookParseError` — set `payments.parseWebhookErr = errors.New("bad sig")` => `HandleWebhook` returns `domain.ErrUnauthorized`; assert `tx.calls==0` (parse fails before the tx) and `subs` unchanged.
- `TestHandleWebhookApplies` — first delivery (id NOT pre-recorded), `payments.webhookEvent` a `customer.subscription.created` with `UserID`, `Status: domain.SubscriptionActive`, `SubscriptionID:"sub_1"`, `CustomerID:"cus_1"`, `CurrentPeriodEnd: &future`, `Created:&now` => after handling, `subs.GetByUserID(userID)` returns `Status==domain.SubscriptionActive`, `StripeSubscriptionID=="sub_1"`, `CurrentPeriodEnd` equal to `future`, `LastEventAt` equal to `now`; assert `tx.calls>0`.
- `TestHandleWebhookOutOfOrder` — seed existing sub `{UserID, Status: domain.SubscriptionActive, StripeCustomerID:"cus_1", LastEventAt:&t2}`; deliver `customer.subscription.updated` with `Status: domain.SubscriptionCanceled`, `Created:&t1` where `t1.Before(t2)` => dropped: `subs.GetByUserID(userID).Status` stays `domain.SubscriptionActive` and `LastEventAt` stays `t2` (older lifecycle event never reverts state). Id must NOT be pre-recorded (proves the drop is ordering, not idempotency).
- `TestHandleWebhookPreservesInvoiceFields` — seed existing sub `{UserID, Status: domain.SubscriptionActive, StripeCustomerID:"cus_1", StripeSubscriptionID:"sub_1", CurrentPeriodEnd:&pe, CancelAtPeriodEnd:true, TrialEndsAt:&te, LastEventAt:&t1}`; deliver an `invoice.paid` event (`Type:"invoice.paid"`, `Status: domain.SubscriptionActive`, `SubscriptionID:"sub_1"`, `CustomerID:"cus_1"`, `Created:&t2`, all period/cancel/trial fields nil/false — matches the real `webhook.go` normalization) => persisted sub keeps `CurrentPeriodEnd==pe`, `CancelAtPeriodEnd==true`, `TrialEndsAt==te`, `StripeSubscriptionID=="sub_1"`, `LastEventAt==t1` (non-`customer.subscription.*` events never clobber those), while `Status==domain.SubscriptionActive`.
- `TestRequireActivePaidPath` — no sub: `subs` empty => `RequireActive` returns `domain.ErrPaymentRequired`.
- `TestRequireActivePaidPath` — past_due within grace: `clock := newClock(now)`, seed `{UserID, Status: domain.SubscriptionPastDue, CurrentPeriodEnd:&now}` (so `now.Before(now+domain.PastDueGrace)`) => `RequireActive` returns **nil**.
- `TestRequireActivePaidPath` — past_due beyond grace: seed `CurrentPeriodEnd = now.Add(-8*24*time.Hour)` => `domain.ErrPaymentRequired`.
- `TestRequireActivePaidPath` — expired: seed `{UserID, Status: domain.SubscriptionExpired}` => `domain.ErrPaymentRequired`.

---

### Task 4: Account service — list / disconnect / token refresh

Coverage tests for `backend/internal/service/account.go` (`List`, `Disconnect`)
and the `tokenSource` refresh helper in `backend/internal/service/service.go`.
`ops_test.go` already covers `BeginConnect` PKCE/callback, `CompleteConnect`,
and `SetVipSenders` — extend, do NOT duplicate those.

**Files:**
- Create/Test: `backend/internal/service/account_test.go` (package `service`, white-box).
- Consumes: `backend/internal/service/fakes_test.go` (Task 1).

**Interfaces:**
- Consumes fakes (Task 1): `newAccountRepo() *fakeAccountRepo`, `newOAuthStateRepo() *fakeOAuthStateRepo`, `newOAuthGateway() *fakeOAuthGateway`, `newSyncStateRepo() *fakeSyncStateRepo`, `newClock(t time.Time) *fakeClock`.
- Wiring: `NewAccountService(accounts port.AccountRepo, states port.OAuthStateRepo, syncState port.SyncStateRepo, oauth map[domain.Provider]port.OAuthGateway, allowedRedirects []string, callbackBaseURL string, clock port.Clock) *AccountService`. `tokenSource` is package-private and constructed directly in-test: `tokenSource{accounts:…, oauth:…, clock:…}`.
- Produces: `TestAccountListScopedToUser`, `TestDisconnectAccount`, `TestTokenSourceRefresh`.

- [ ] **Step 1 (representative #3): Write the failing test** — `tokenSource.accessToken` valid / expired-refresh / refresh-failure. Directly exercises the same helper every provider write-through in `MailService`/`CalendarService`/`SyncService` calls.

```go
package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// TestTokenSourceRefresh pins service.go tokenSource.accessToken: a live token
// is returned untouched; an expired one is refreshed, re-persisted via
// SaveTokens, and a failed refresh flags the account reauth_required and
// surfaces domain.ErrUnauthorized.
func TestTokenSourceRefresh(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	acct := domain.ConnectedAccount{ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Status: domain.AccountActive}

	// build wires an account + seeded token set into a tokenSource.
	build := func(clock *fakeClock, gw *fakeOAuthGateway, saved port.TokenSet) (*fakeAccountRepo, tokenSource) {
		accounts := newAccountRepo()
		if _, err := accounts.Create(ctx, acct); err != nil {
			t.Fatal(err)
		}
		if err := accounts.SaveTokens(ctx, acct.ID, saved); err != nil {
			t.Fatal(err)
		}
		ts := tokenSource{
			accounts: accounts,
			oauth:    map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: gw},
			clock:    clock,
		}
		return accounts, ts
	}

	t.Run("valid unexpired token returned as-is", func(t *testing.T) {
		gw := newOAuthGateway()
		_, ts := build(newClock(base), gw, port.TokenSet{
			AccessToken:  "access-good",
			RefreshToken: "refresh-1",
			ExpiresAt:    base.Add(time.Hour), // beyond the 1-minute slack
		})
		got, err := ts.accessToken(ctx, acct)
		if err != nil {
			t.Fatalf("accessToken: %v", err)
		}
		if got != "access-good" {
			t.Fatalf("token = %q, want access-good", got)
		}
		if gw.refreshCalls != 0 {
			t.Fatalf("Refresh called %d times, want 0 (token still valid)", gw.refreshCalls)
		}
	})

	t.Run("expired token triggers refresh and re-persist", func(t *testing.T) {
		gw := newOAuthGateway()
		gw.refreshToken = port.OAuthToken{TokenSet: port.TokenSet{
			AccessToken:  "access-fresh",
			RefreshToken: "refresh-2",
			ExpiresAt:    base.Add(time.Hour),
		}}
		accounts, ts := build(newClock(base), gw, port.TokenSet{
			AccessToken:  "access-stale",
			RefreshToken: "refresh-1",
			ExpiresAt:    base.Add(-time.Minute), // already expired
		})
		got, err := ts.accessToken(ctx, acct)
		if err != nil {
			t.Fatalf("accessToken: %v", err)
		}
		if got != "access-fresh" {
			t.Fatalf("token = %q, want access-fresh", got)
		}
		if gw.refreshCalls != 1 {
			t.Fatalf("Refresh calls = %d, want 1", gw.refreshCalls)
		}
		if gw.lastRefreshToken != "refresh-1" {
			t.Fatalf("Refresh got refresh token %q, want refresh-1", gw.lastRefreshToken)
		}
		persisted, err := accounts.GetTokens(ctx, acct.ID)
		if err != nil {
			t.Fatal(err)
		}
		if persisted.AccessToken != "access-fresh" || persisted.RefreshToken != "refresh-2" {
			t.Fatalf("persisted tokens = %+v, want the refreshed pair", persisted)
		}
	})

	t.Run("refresh failure flags reauth_required and returns ErrUnauthorized", func(t *testing.T) {
		gw := newOAuthGateway()
		gw.refreshErr = errors.New("invalid_grant")
		accounts, ts := build(newClock(base), gw, port.TokenSet{
			AccessToken:  "access-stale",
			RefreshToken: "refresh-1",
			ExpiresAt:    base.Add(-time.Minute),
		})
		if _, err := ts.accessToken(ctx, acct); !errors.Is(err, domain.ErrUnauthorized) {
			t.Fatalf("err = %v, want domain.ErrUnauthorized", err)
		}
		reloaded, err := accounts.GetByID(ctx, acct.ID)
		if err != nil {
			t.Fatal(err)
		}
		if reloaded.Status != domain.AccountReauthRequired {
			t.Fatalf("account status = %q, want %q", reloaded.Status, domain.AccountReauthRequired)
		}
	})
}
```

- [ ] **Step 2: Run** — `cd backend && go test ./internal/service/ -run TestTokenSourceRefresh -v` — **Expected PASS**.
- [ ] **Step 3: Commit** — `cd backend && git add internal/service/account_test.go && git commit -m $'test(service): pin tokenSource refresh + reauth-required flagging\n\nCo-Authored-By: WOZCODE <contact@withwoz.com>'`

- [ ] **Step 4: Write the remaining Task 4 tests** (enumerated below), then **Run** `cd backend && go test ./internal/service/ -run 'TestAccountListScopedToUser|TestDisconnectAccount' -v` — **Expected PASS** — then **Commit** `cd backend && git add internal/service/account_test.go && git commit -m $'test(service): cover account list scoping and disconnect cleanup\n\nCo-Authored-By: WOZCODE <contact@withwoz.com>'`.

**Required cases (each mandatory; ownership invariant: a foreign resource yields `domain.ErrNotFound`, never 403/`ErrUnauthorized`):**

- `TestAccountListScopedToUser` — `AccountService.List`: `accounts.Create` two accounts for `"u1"` (`a1`, `a2`) and one for `"u2"` (`a3`); `List(ctx,"u1")` returns exactly the two `u1` ids (assert `len==2` and both `UserID=="u1"`), never `a3`. Wire with `NewAccountService(accounts, nil, nil, nil, nil, "", newClock(base))`.
- `TestAccountListScopedToUser` — `List` empty: for a user with no accounts, the result is **non-nil** and `len==0` (service normalizes the `nil` slice to `[]domain.ConnectedAccount{}`).
- `TestDisconnectAccount` — owned: `accounts.Create` `{ID:"a1", UserID:"u1"}`, `syncState := newSyncStateRepo()`; `Disconnect(ctx,"u1","a1")` returns nil, then `accounts.GetByID(ctx,"a1")` returns `domain.ErrNotFound` (row deleted) and `syncState.deletedAccounts` contains `"a1"` (`SyncStateRepo.DeleteByAccount` invoked).
- `TestDisconnectAccount` — foreign: `accounts.Create` `{ID:"a1", UserID:"u2"}`; `Disconnect(ctx,"u1","a1")` returns `domain.ErrNotFound`; assert the row still exists (`accounts.GetByID(ctx,"a1")` succeeds) and `syncState.deletedAccounts` is empty (no cleanup on a non-owned account).
- `TestDisconnectAccount` — missing: `Disconnect(ctx,"u1","ghost")` (no such account) returns `domain.ErrNotFound` from `ownedAccount`'s `GetByID`.
- `TestTokenSourceRefresh` — refresh omits refresh_token: extend the expired-refresh subtest (or add a sibling) where `gw.refreshToken` has `RefreshToken:""`; assert the persisted `RefreshToken` falls back to the prior `"refresh-1"` (service.go keeps the old refresh token when the provider returns none) and the returned access token is the fresh one.
- `TestTokenSourceRefresh` — no gateway configured: build a `tokenSource` whose `oauth` map lacks the account's provider and a seeded expired token; `accessToken` returns a non-nil error whose message names the missing provider (not a sentinel — plain `fmt.Errorf`), and `SaveTokens`/`Update` are not called (assert `accounts` token set and status unchanged).

---

### Task 5: MailService — GetThread, ActOnThread, snooze/reminder, draft & snippet CRUD, send scheduling

**Files:**
- Modify/Test: `backend/internal/service/mail_test.go` (package `service`; white-box). This file already exists from an earlier task and covers `UnsendDraft`, `MarkThreadOpened`, and `ListThreads`-forwards-view — **append** the new tests; do **not** re-add those three.
- Consumes (read-only): `backend/internal/service/fakes_test.go` (Task 1), `backend/internal/service/mail.go`, `backend/internal/service/service.go`, `backend/internal/domain/mail.go`, `backend/internal/port/driven.go`, `backend/internal/port/driving.go`.

**Interfaces:**
- **Consumes** (shared fakes + constructors from Task 1, exact signatures):
  - `newAccountRepo() *fakeAccountRepo`, `newThreadRepo() *fakeThreadRepo`, `newMessageRepo() *fakeMessageRepo`, `newDraftRepo() *fakeDraftRepo`, `newSnippetRepo() *fakeSnippetRepo`, `newMailProvider() *fakeMailProvider`, `newOAuthGateway() *fakeOAuthGateway`, `newClock(t time.Time) *fakeClock` (methods `Now() time.Time`, `Advance(time.Duration)`, `Set(time.Time)`).
  - `service.NewMailService(service.MailServiceDeps{...}) *MailService` (white-box, so just `NewMailService`).
  - Seeding is done through the real port methods the fakes implement (`accounts.Create`, `accounts.SaveTokens`, `threads.Upsert`, `messages.Upsert`, `drafts.Create`, `snippets.Create`) so the tests never touch fake internals.
- **Produces** (test funcs + local helpers, all package `service`): `mailFixture` (type), `newMailFixture`, `(*mailFixture).seedAccount`, `(*mailFixture).seedThread`, `TestActOnThreadMirrorsAndWritesThrough`, `TestSendDraftSchedulesAtGrace`, `TestGetThread`, `TestSnoozeThread`, `TestSetReminder`, `TestCreateDraft`, `TestUpdateDraft`, `TestGetDraft`, `TestListDrafts`, `TestDeleteDraft`, `TestSendDraftValidationAndOwnership`, `TestSendDraftSendLaterKeepsScheduledTime`, `TestCreateSnippet`, `TestUpdateSnippet`, `TestDeleteSnippet`, `TestListSnippets`.

---

- [ ] **Step 1: Write the fixture helper + the two fully-coded characterization tests**

  Append to `backend/internal/service/mail_test.go`. First a shared fixture (ensure `reflect` is in the file's import block):

  ```go
  // --- MailService fixture (Task 5) --------------------------------------------

  // mailFixture wires a MailService to the shared package-service fakes with a
  // Google mail provider + oauth gateway registered and a frozen clock. It is
  // SelfHosted so the paywall is bypassed (paywall is exercised elsewhere).
  type mailFixture struct {
  	svc      *MailService
  	accounts *fakeAccountRepo
  	threads  *fakeThreadRepo
  	messages *fakeMessageRepo
  	drafts   *fakeDraftRepo
  	snippets *fakeSnippetRepo
  	provider *fakeMailProvider
  	oauth    *fakeOAuthGateway
  	clock    *fakeClock
  }

  func newMailFixture(t *testing.T) *mailFixture {
  	t.Helper()
  	clk := newClock(time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC))
  	f := &mailFixture{
  		accounts: newAccountRepo(),
  		threads:  newThreadRepo(),
  		messages: newMessageRepo(),
  		drafts:   newDraftRepo(),
  		snippets: newSnippetRepo(),
  		provider: newMailProvider(),
  		oauth:    newOAuthGateway(),
  		clock:    clk,
  	}
  	f.svc = NewMailService(MailServiceDeps{
  		Accounts:      f.accounts,
  		Threads:       f.threads,
  		Messages:      f.messages,
  		Drafts:        f.drafts,
  		Snippets:      f.snippets,
  		MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: f.provider},
  		OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: f.oauth},
  		Clock:         clk,
  		SelfHosted:    true,
  		UndoSendGrace: 15 * time.Second,
  	})
  	return f
  }

  // seedAccount persists an owned Google account with a fresh (non-expired)
  // token so provider write-through resolves an access token without a refresh.
  func (f *mailFixture) seedAccount(t *testing.T, id, userID string) domain.ConnectedAccount {
  	t.Helper()
  	ctx := context.Background()
  	a := domain.ConnectedAccount{
  		ID:       id,
  		UserID:   userID,
  		Email:    userID + "@acme.com",
  		Provider: domain.ProviderGoogle,
  		Status:   domain.AccountActive,
  	}
  	if _, err := f.accounts.Create(ctx, a); err != nil {
  		t.Fatalf("seed account: %v", err)
  	}
  	if err := f.accounts.SaveTokens(ctx, id, port.TokenSet{
  		AccessToken:  "tok-" + id,
  		RefreshToken: "refresh-" + id,
  		ExpiresAt:    f.clock.Now().Add(time.Hour),
  	}); err != nil {
  		t.Fatalf("seed tokens: %v", err)
  	}
  	return a
  }

  // seedThread persists a thread (ProviderThreadID = "p-"+id) with optional mutation.
  func (f *mailFixture) seedThread(t *testing.T, id, accountID string, mut func(*domain.Thread)) domain.Thread {
  	t.Helper()
  	th := domain.Thread{ID: id, AccountID: accountID, ProviderThreadID: "p-" + id, InInbox: true}
  	if mut != nil {
  		mut(&th)
  	}
  	if _, err := f.threads.Upsert(context.Background(), th); err != nil {
  		t.Fatalf("seed thread: %v", err)
  	}
  	return th
  }
  ```

  Fully-coded test #1 — `ActOnThread` over all 8 `domain.ThreadAction` values, asserting both the local mirror mutation and the exact provider `ModifyLabels` add/remove keys (mapping read from `mail.go` lines 126-153):

  ```go
  // --- MailService.ActOnThread (all 8 actions) ---------------------------------

  func TestActOnThreadMirrorsAndWritesThrough(t *testing.T) {
  	const owner = "u1"
  	tests := []struct {
  		action     domain.ThreadAction
  		wantAdd    []string
  		wantRemove []string
  		check      func(t *testing.T, got domain.Thread)
  	}{
  		{
  			action: domain.ThreadActionArchive, wantAdd: nil, wantRemove: []string{port.LabelKeyInbox},
  			check: func(t *testing.T, g domain.Thread) {
  				if g.InInbox {
  					t.Fatal("archive: InInbox = true, want false")
  				}
  			},
  		},
  		{
  			action: domain.ThreadActionTrash, wantAdd: []string{port.LabelKeyTrash}, wantRemove: []string{port.LabelKeyInbox},
  			check: func(t *testing.T, g domain.Thread) {
  				if g.InInbox {
  					t.Fatal("trash: InInbox = true, want false")
  				}
  			},
  		},
  		{
  			action: domain.ThreadActionStar, wantAdd: []string{port.LabelKeyStarred}, wantRemove: nil,
  			check: func(t *testing.T, g domain.Thread) {
  				if !g.Starred {
  					t.Fatal("star: Starred = false, want true")
  				}
  			},
  		},
  		{
  			action: domain.ThreadActionUnstar, wantAdd: nil, wantRemove: []string{port.LabelKeyStarred},
  			check: func(t *testing.T, g domain.Thread) {
  				if g.Starred {
  					t.Fatal("unstar: Starred = true, want false")
  				}
  			},
  		},
  		{
  			action: domain.ThreadActionRead, wantAdd: nil, wantRemove: []string{port.LabelKeyUnread},
  			check: func(t *testing.T, g domain.Thread) {
  				if g.Unread {
  					t.Fatal("read: Unread = true, want false")
  				}
  			},
  		},
  		{
  			action: domain.ThreadActionUnread, wantAdd: []string{port.LabelKeyUnread}, wantRemove: nil,
  			check: func(t *testing.T, g domain.Thread) {
  				if !g.Unread {
  					t.Fatal("unread: Unread = false, want true")
  				}
  			},
  		},
  		{
  			action: domain.ThreadActionSpam, wantAdd: []string{port.LabelKeySpam}, wantRemove: []string{port.LabelKeyInbox},
  			check: func(t *testing.T, g domain.Thread) {
  				if g.InInbox {
  					t.Fatal("spam: InInbox = true, want false")
  				}
  			},
  		},
  		{
  			action:  domain.ThreadActionMoveToInbox,
  			wantAdd: []string{port.LabelKeyInbox}, wantRemove: []string{port.LabelKeyTrash, port.LabelKeySpam},
  			check: func(t *testing.T, g domain.Thread) {
  				if !g.InInbox {
  					t.Fatal("move_to_inbox: InInbox = false, want true")
  				}
  			},
  		},
  	}

  	for _, tt := range tests {
  		t.Run(string(tt.action), func(t *testing.T) {
  			f := newMailFixture(t)
  			f.seedAccount(t, "a1", owner)
  			// Seed the opposite of the mutation so each change is observable.
  			f.seedThread(t, "t1", "a1", func(th *domain.Thread) {
  				th.InInbox = true
  				th.Starred = tt.action == domain.ThreadActionUnstar
  				th.Unread = tt.action == domain.ThreadActionRead
  			})

  			got, err := f.svc.ActOnThread(context.Background(), owner, "t1", tt.action)
  			if err != nil {
  				t.Fatalf("ActOnThread(%s): %v", tt.action, err)
  			}
  			tt.check(t, got)

  			// Local mirror persisted through ThreadRepo.Update.
  			stored, err := f.threads.GetByID(context.Background(), "t1")
  			if err != nil {
  				t.Fatalf("reload thread: %v", err)
  			}
  			tt.check(t, stored)

  			// Exactly one provider write-through with canonical keys, addressed
  			// by the provider thread id and carrying the resolved access token.
  			if f.provider.modifyLabelsCalls != 1 {
  				t.Fatalf("ModifyLabels calls = %d, want 1", f.provider.modifyLabelsCalls)
  			}
  			if f.provider.lastModifyThreadID != "p-t1" {
  				t.Fatalf("provider thread id = %q, want p-t1", f.provider.lastModifyThreadID)
  			}
  			if f.provider.lastModifyToken != "tok-a1" {
  				t.Fatalf("access token = %q, want tok-a1", f.provider.lastModifyToken)
  			}
  			if !reflect.DeepEqual(f.provider.lastModifyAdd, tt.wantAdd) {
  				t.Fatalf("add = %v, want %v", f.provider.lastModifyAdd, tt.wantAdd)
  			}
  			if !reflect.DeepEqual(f.provider.lastModifyRemove, tt.wantRemove) {
  				t.Fatalf("remove = %v, want %v", f.provider.lastModifyRemove, tt.wantRemove)
  			}
  		})
  	}
  }
  ```

  Fully-coded test #2 — `SendDraft` "send now" grace scheduling (`mail.go` lines 324-364):

  ```go
  // --- MailService.SendDraft: send-now schedules at now + undo-send grace -------

  func TestSendDraftSchedulesAtGrace(t *testing.T) {
  	const owner = "u1"
  	f := newMailFixture(t)
  	f.seedAccount(t, "a1", owner)
  	now := f.clock.Now()

  	if _, err := f.drafts.Create(context.Background(), domain.Draft{
  		ID:        "d1",
  		AccountID: "a1",
  		To:        []domain.EmailAddress{{Email: "bob@x.com"}},
  		Subject:   "hi",
  		BodyHTML:  "<p>hi</p>",
  	}); err != nil {
  		t.Fatalf("seed draft: %v", err)
  	}

  	msg, err := f.svc.SendDraft(context.Background(), owner, "d1")
  	if err != nil {
  		t.Fatalf("SendDraft: %v", err)
  	}

  	wantSendAt := now.Add(15 * time.Second) // fixture UndoSendGrace
  	// Provisional message returned to the client.
  	if !msg.SentAt.Equal(wantSendAt) {
  		t.Fatalf("provisional SentAt = %v, want now+grace %v", msg.SentAt, wantSendAt)
  	}
  	if !msg.IsDraft {
  		t.Fatal("provisional message IsDraft = false, want true")
  	}
  	if msg.From.Email != "u1@acme.com" {
  		t.Fatalf("provisional From = %q, want the account email", msg.From.Email)
  	}
  	if msg.ID == "" {
  		t.Fatal("provisional message has no id")
  	}
  	if len(msg.Attachments) != 0 || msg.Attachments == nil {
  		t.Fatalf("provisional Attachments = %v, want non-nil empty slice", msg.Attachments)
  	}

  	// Draft persisted with ScheduledAt = now+grace (the worker's claim token).
  	stored, err := f.drafts.GetByID(context.Background(), "d1")
  	if err != nil {
  		t.Fatalf("reload draft: %v", err)
  	}
  	if stored.ScheduledAt == nil || !stored.ScheduledAt.Equal(wantSendAt) {
  		t.Fatalf("stored ScheduledAt = %v, want %v", stored.ScheduledAt, wantSendAt)
  	}
  	if !stored.UpdatedAt.Equal(now) {
  		t.Fatalf("stored UpdatedAt = %v, want %v", stored.UpdatedAt, now)
  	}
  }
  ```

- [ ] **Step 2: Run the two representative tests — Expected PASS**

  ```bash
  cd backend && go test ./internal/service/ -run 'TestActOnThreadMirrorsAndWritesThrough|TestSendDraftSchedulesAtGrace' -v
  ```

  Expected: PASS (8 `ActOnThread` subtests + the grace test). If `ActOnThread` fails on the add/remove keys, re-read the `switch` in `mail.go` (lines 126-153) — the test is the wrong one, not a bug. If `lastModifyToken`/`lastModifyThreadID` is empty, the fake did not record `ModifyLabels` args (fix Task 1's `fakeMailProvider`, see realBehaviorNotes).

- [ ] **Step 3: Commit**

  ```bash
  cd /Users/guilherme/Dev/pessoal/calendium && git add backend/internal/service/mail_test.go && \
  git commit -m "test(service): cover MailService ActOnThread label write-through + send-now grace scheduling

  Co-Authored-By: WOZCODE <contact@withwoz.com>"
  ```

- [ ] **Step 4: Write the enumerated remaining tests**

  Add the following test functions to `mail_test.go`, each table-driven with `t.Run` and reusing `newMailFixture`/`seedAccount`/`seedThread`. Every bullet is mandatory coverage; assert with `errors.Is` against the named sentinel and inject time via `f.clock`.

  **`TestGetThread`** — `MailService.GetThread`:
  - Owned thread with two seeded messages (`f.messages.Upsert` for `m1` then `m2`, both `ThreadID:"t1"`): returns the thread and `[]domain.Message{m1, m2}` **in insertion (provider) order** — assert `msgs[0].ID == "m1"` and `msgs[1].ID == "m2"`, and returned thread `ID == "t1"`.
  - Owned thread with **no** messages: returns a non-nil empty `[]domain.Message` (`len == 0`, `msgs != nil`).
  - Thread owned by another user (seed account `a1` for `owner`, thread `t1` under `a1`; call as `"intruder"`) => `domain.ErrNotFound`, nil thread, nil messages.
  - Unknown thread id => `domain.ErrNotFound` (repo miss propagates).

  **`TestSnoozeThread`** — `MailService.SnoozeThread` (no provider side-effects; assert `f.provider.modifyLabelsCalls == 0`):
  - `until` in the future (`f.clock.Now().Add(time.Hour)`): returns thread with `SnoozedUntil` set to `until` (`got.SnoozedUntil.Equal(until)`), and the persisted thread (`f.threads.GetByID`) also has it set.
  - `until` == `f.clock.Now()` (not after now) => `domain.ErrValidation`.
  - `until` in the past => `domain.ErrValidation` (validation precedes ownership — holds even for a foreign/missing thread).
  - Foreign thread with a **future** `until` (call as `"intruder"`) => `domain.ErrNotFound`.

  **`TestSetReminder`** — `MailService.SetReminder` (no provider side-effects):
  - `remindAt` in the future: returns thread with `RemindAt` set (`got.RemindAt.Equal(*remindAt)`), persisted through `ThreadRepo.Update`.
  - `remindAt == nil` clears an existing reminder: seed thread via `seedThread` with `mut` setting `th.RemindAt = &someFuture`, call with `nil` => `got.RemindAt == nil` and persisted thread `RemindAt == nil`.
  - `remindAt` in the past (non-nil, not after now) => `domain.ErrValidation`.
  - Foreign thread with a future `remindAt` => `domain.ErrNotFound`.

  **`TestCreateDraft`** — `MailService.CreateDraft` (seed account `a1` for `owner`):
  - Missing `AccountID` (`port.DraftInput{}`) => `domain.ErrValidation`.
  - `AccountID` of an account owned by another user => `domain.ErrNotFound` (ownership; seed `a2` under `"other"`).
  - Happy path (`port.DraftInput{AccountID:"a1", To:[]domain.EmailAddress{{Email:"x@y.com"}}, Subject:"s", BodyHTML:"b"}`): returns a draft with a **non-empty generated `ID`**, `AccountID=="a1"`, `To` preserved, `Cc`/`Bcc` non-nil empty slices (via `emptyIfNil`), `UpdatedAt.Equal(f.clock.Now())`; and `f.drafts.GetByID(ctx, got.ID)` returns the persisted draft.
  - `ScheduledAt` passed through: `DraftInput.ScheduledAt = &future` is stored on the created draft.

  **`TestUpdateDraft`** — `MailService.UpdateDraft` (seed account `a1`, create draft `d1` under `a1`):
  - Foreign draft (draft under an account owned by `"other"`) => `domain.ErrNotFound`.
  - `in.AccountID` set to a **different** account id (`"a2"`) => `domain.ErrValidation` ("a draft cannot move between accounts").
  - Happy path (advance clock via `f.clock.Advance(time.Minute)` first): updates `Subject`/`BodyHTML`/`To`/`ScheduledAt`, sets `UpdatedAt.Equal(f.clock.Now())` (the advanced time), persisted through `DraftRepo.Update`.
  - `in.AccountID == ""` is accepted (keeps the existing account) — no error.

  **`TestGetDraft`** — `MailService.GetDraft`:
  - Owned draft returns it (`got.ID == "d1"`).
  - Foreign draft => `domain.ErrNotFound`.
  - Unknown id => `domain.ErrNotFound`.

  **`TestListDrafts`** — `MailService.ListDrafts`:
  - Two drafts created under `owner`'s account `a1`: returns both (`len == 2`).
  - Drafts under another user's account are **excluded** (seed `a2` under `"other"` with a draft; listing for `owner` does not include it). *(Depends on `fakeDraftRepo.ListByUser` resolving draft→account→user ownership — see realBehaviorNotes.)*
  - No drafts for the user: returns a non-nil empty slice (`got != nil && len(got) == 0`).

  **`TestDeleteDraft`** — `MailService.DeleteDraft`:
  - Owned draft: `DeleteDraft` returns nil; a subsequent `f.drafts.GetByID(ctx, "d1")` returns `domain.ErrNotFound`.
  - Foreign draft => `domain.ErrNotFound` (and the draft is **not** deleted — `GetByID` still returns it).

  **`TestSendDraftValidationAndOwnership`** — `MailService.SendDraft` guards:
  - Draft with zero recipients (`To`/`Cc`/`Bcc` all empty) => `domain.ErrValidation` ("no recipients").
  - Foreign draft (account owned by `"other"`) => `domain.ErrNotFound`.
  - Unknown draft id => `domain.ErrNotFound`.

  **`TestSendDraftSendLaterKeepsScheduledTime`** — `MailService.SendDraft` Send-Later branch:
  - Draft seeded with `ScheduledAt` = `f.clock.Now().Add(2*time.Hour)` (a future time beyond the grace) and a recipient: the returned message `SentAt` **equals that scheduled time** (not `now+grace`), and the persisted draft's `ScheduledAt` stays equal to it. Concretely `wantAt := f.clock.Now().Add(2*time.Hour)`; assert `msg.SentAt.Equal(wantAt)` and `stored.ScheduledAt.Equal(wantAt)`.
  - Edge: a `ScheduledAt` in the **past** (before `now`) is treated as send-now => `SentAt.Equal(now.Add(15*time.Second))` (the `d.ScheduledAt.After(now)` guard is false).

  **`TestCreateSnippet`** — `MailService.CreateSnippet`:
  - Blank name (`SnippetInput{Name:"  ", BodyHTML:"b"}`) => `domain.ErrValidation`.
  - Blank body (`SnippetInput{Name:"n", BodyHTML:" "}`) => `domain.ErrValidation`.
  - Happy path: returns snippet with non-empty `ID`, `UserID == owner`, `Name`/`Shortcut`/`BodyHTML` set; `f.snippets.GetByID(ctx, got.ID)` returns it.

  **`TestUpdateSnippet`** — `MailService.UpdateSnippet`:
  - Foreign snippet (seed a snippet with `UserID:"other"` via `f.snippets.Create`) => `domain.ErrNotFound`.
  - Blank name => `domain.ErrValidation` (validation runs before ownership).
  - Happy path: updates `Name`/`Shortcut`/`BodyHTML`, persisted through `SnippetRepo.Update`.

  **`TestDeleteSnippet`** — `MailService.DeleteSnippet`:
  - Owned snippet: returns nil; subsequent `f.snippets.GetByID` returns `domain.ErrNotFound`.
  - Foreign snippet => `domain.ErrNotFound` (not deleted).

  **`TestListSnippets`** — `MailService.ListSnippets`:
  - Two snippets with `UserID == owner`: returns both.
  - Another user's snippet excluded (filter by `snip.UserID`).
  - None for the user: non-nil empty slice.

- [ ] **Step 5: Run the full MailService suite — Expected PASS**

  ```bash
  cd backend && go test ./internal/service/ -run 'TestGetThread|TestSnoozeThread|TestSetReminder|Test.*Draft|Test.*Snippet|TestActOnThread' -v
  cd backend && go test ./internal/service/ -count=1
  ```

  Expected: all PASS. For any FAIL, classify wrong-test vs real-bug against `mail.go`: e.g. if `SnoozeThread` returns `ErrNotFound` where the case expects `ErrValidation`, the fixture passed a past `until` to a foreign thread — re-check ordering (validation precedes ownership at `mail.go` lines 202-208). Genuine bugs are noted and fixed in a separate commit, never folded into the test commit.

- [ ] **Step 6: Commit**

  ```bash
  cd /Users/guilherme/Dev/pessoal/calendium && git add backend/internal/service/mail_test.go && \
  git commit -m "test(service): cover MailService GetThread, snooze/reminder, draft & snippet CRUD, send-later

  Co-Authored-By: WOZCODE <contact@withwoz.com>"
  ```

---

### Task 6: CalendarService — calendars, event write-through, RSVP, and availability

**Files:**
- Create/Test: `backend/internal/service/calendar_test.go` (white-box, `package service`).
- Consumes (do NOT create here): `backend/internal/service/fakes_test.go` from **Task 1** (shared fakes + `fakeClock`).
- Code under test (read-only reference): `backend/internal/service/calendar.go`, `backend/internal/service/service.go` (`entitlement`, `tokenSource`, `eventFromInput`, `applyEventPatch`, `declinedByUser`).

**Interfaces:**

_Consumes from Task 1 (`fakes_test.go`, same package):_
- `newClock(t time.Time) *fakeClock` → `Now()/Advance(d)/Set(t)`.
- `newAccountRepo() *fakeAccountRepo` — `Create(ctx, domain.ConnectedAccount)`, `SaveTokens(ctx, id, port.TokenSet)`, `GetTokens`, `GetByID`, `ListByUser`, `Update`. Seed accounts via `Create`; seed provider tokens via `SaveTokens` (read back by `GetTokens`).
- `newCalendarRepo() *fakeCalendarRepo` — backing map `byID map[string]domain.Calendar` (seed exact ids directly); `GetByID`/`Update` read/write `byID`; `ListByUser` returns every seeded calendar and records `lastListUserID`.
- `newEventRepo() *fakeEventRepo` — backing map `byID map[string]domain.Event`; `Upsert` stores by `e.ID` and returns it; `Delete` removes from `byID` and records `lastDeletedID`; `ListInRange` returns seeded events overlapping `[from,to)` (`Start.Before(to) && End.After(from)`), restricted to `calendarIDs` when non-nil, and records `lastListUserID/lastListFrom/lastListTo/lastListCalendarIDs`.
- `newCalendarProvider() *fakeCalendarProvider` — programmable `createResult/updateResult domain.Event`; recording `createCalls/updateCalls/deleteCalls/rsvpCalls int`, `lastAccessToken string`, `lastProviderCalendarID string`, `lastCreateInput domain.EventInput`, `lastUpdatePatch domain.EventPatch`, `lastUpdateProviderEventID string`, `lastDeleteProviderEventID string`, `lastRSVPStatus domain.RsvpStatus`, `lastRSVPProviderEventID string`.
- `newOAuthGateway() *fakeOAuthGateway` (wired into deps; not exercised on the valid-token path).
- `newSubscriptionRepo() *fakeSubscriptionRepo` — empty ⇒ `GetByUserID` returns `domain.ErrNotFound` (drives the paywall).

_Produces (test funcs + local helpers other tasks may ignore):_ `newCalFixture`, `newAvailFixture`, `TestCreateEventWritesThroughToProviderAndMirror`, `TestAvailabilityFreeWindows`, `TestListCalendars`, `TestUpdateCalendar`, `TestListEvents`, `TestCreateEventValidation`, `TestCreateEventNoProvider`, `TestUpdateEventWriteThrough`, `TestDeleteEventWriteThrough`, `TestRSVPWriteThrough`, `TestCalendarPaywall`, `TestAvailabilityValidation`.

Wire the service via the real constructor (`SelfHosted: true` unless a case tests the paywall):
```go
svc := NewCalendarService(CalendarServiceDeps{
    Subscriptions:     subs,      // fakeSubscriptionRepo (only needed when SelfHosted=false)
    Accounts:          accounts,  // fakeAccountRepo
    Calendars:         calendars, // fakeCalendarRepo
    Events:            events,    // fakeEventRepo
    CalendarProviders: map[domain.Provider]port.CalendarProvider{domain.ProviderGoogle: provider},
    OAuth:             map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
    Clock:             clock,
    SelfHosted:        true,
})
```

---

#### Cycle A — event create write-through (representative, fully coded)

- [ ] **Step 1: Write the test.** Add to `backend/internal/service/calendar_test.go`. A locally created event must (a) call the provider's `CreateEvent` with the resolved access token and the calendar's *provider* id, and (b) upsert the returned event into the local mirror with a locally-generated `ID` and the *local* `CalendarID`.

```go
package service

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// calFixture is a CalendarService wired to one Google account (a1, user u1,
// email me@x.com) owning one writable calendar (cal1 -> provider prov-cal-1),
// with a valid non-expiring access token so tokenSource never refreshes.
type calFixture struct {
	svc       *CalendarService
	accounts  *fakeAccountRepo
	calendars *fakeCalendarRepo
	events    *fakeEventRepo
	provider  *fakeCalendarProvider
	clock     *fakeClock
}

func newCalFixture(t *testing.T) *calFixture {
	t.Helper()
	ctx := context.Background()
	base := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	clock := newClock(base)

	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@x.com",
	}); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	// Valid token: AccessToken set and ExpiresAt an hour out, so
	// tokenSource.accessToken returns it without hitting the OAuth gateway.
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{
		AccessToken: "valid-access", RefreshToken: "r", ExpiresAt: base.Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed tokens: %v", err)
	}

	calendars := newCalendarRepo()
	calendars.byID["cal1"] = domain.Calendar{
		ID: "cal1", AccountID: "a1", ProviderCalendarID: "prov-cal-1",
		Name: "Work", CanWrite: true, IsVisible: true,
	}

	events := newEventRepo()
	provider := newCalendarProvider()

	svc := NewCalendarService(CalendarServiceDeps{
		Accounts:          accounts,
		Calendars:         calendars,
		Events:            events,
		CalendarProviders: map[domain.Provider]port.CalendarProvider{domain.ProviderGoogle: provider},
		OAuth:             map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:             clock,
		SelfHosted:        true,
	})
	return &calFixture{svc, accounts, calendars, events, provider, clock}
}

func TestCreateEventWritesThroughToProviderAndMirror(t *testing.T) {
	ctx := context.Background()
	f := newCalFixture(t)
	base := f.clock.Now()

	// Canned provider result: what the upstream calendar returns post-create.
	f.provider.createResult = domain.Event{
		ProviderEventID: "pe-1",
		Title:           "Sync",
		Start:           base.Add(time.Hour),
		End:             base.Add(2 * time.Hour),
		Status:          domain.EventConfirmed,
	}

	in := domain.EventInput{
		CalendarID: "cal1",
		Title:      "Sync",
		Start:      base.Add(time.Hour),
		End:        base.Add(2 * time.Hour),
	}
	got, err := f.svc.CreateEvent(ctx, "u1", in)
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}

	// (a) provider write-through: exactly one call, with the resolved token and
	// the calendar's PROVIDER id (never the local id).
	if f.provider.createCalls != 1 {
		t.Fatalf("provider CreateEvent calls = %d, want 1", f.provider.createCalls)
	}
	if f.provider.lastAccessToken != "valid-access" {
		t.Fatalf("provider token = %q, want valid-access", f.provider.lastAccessToken)
	}
	if f.provider.lastProviderCalendarID != "prov-cal-1" {
		t.Fatalf("provider calendarID = %q, want prov-cal-1", f.provider.lastProviderCalendarID)
	}
	if f.provider.lastCreateInput.Title != "Sync" {
		t.Fatalf("provider input title = %q, want Sync", f.provider.lastCreateInput.Title)
	}

	// (b) returned event: provider fields, but locally-generated ID and LOCAL
	// calendar id.
	if got.ID == "" {
		t.Fatal("event id not generated")
	}
	if got.ProviderEventID != "pe-1" {
		t.Fatalf("ProviderEventID = %q, want pe-1", got.ProviderEventID)
	}
	if got.CalendarID != "cal1" {
		t.Fatalf("CalendarID = %q, want cal1 (local id)", got.CalendarID)
	}

	// (c) local mirror upserted with the same row.
	stored, ok := f.events.byID[got.ID]
	if !ok {
		t.Fatal("event not persisted to mirror")
	}
	if !reflect.DeepEqual(stored, got) {
		t.Fatalf("mirror = %+v, want %+v", stored, got)
	}
}
```

- [ ] **Step 2: Run** — Expected **PASS**.
```bash
cd backend && go test ./internal/service/ -run TestCreateEventWritesThroughToProviderAndMirror -v
```

- [ ] **Step 3: Commit.**
```bash
cd backend && git add internal/service/calendar_test.go
git commit -m "test(service): cover CalendarService.CreateEvent provider write-through + mirror"
```

---

#### Cycle B — Availability free-window computation (thorough table, fully coded)

- [ ] **Step 1: Write the test.** This is the load-bearing test for the section: exercise `Availability`'s busy-merge / gap-filter logic. `Availability` ignores the clock in its computation, so times are constructed literally (all `time.UTC`, no monotonic component — `reflect.DeepEqual` on the returned slots is exact).

```go
func newAvailFixture(t *testing.T, accountEmail string, evs []domain.Event) *CalendarService {
	t.Helper()
	ctx := context.Background()
	accounts := newAccountRepo()
	if accountEmail != "" {
		// One account so accounts.ListByUser yields the owner email set used by
		// declinedByUser.
		if _, err := accounts.Create(ctx, domain.ConnectedAccount{
			ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: accountEmail,
		}); err != nil {
			t.Fatalf("seed account: %v", err)
		}
	}
	events := newEventRepo()
	for i, ev := range evs {
		if ev.ID == "" {
			ev.ID = fmt.Sprintf("e%d", i)
		}
		if ev.CalendarID == "" {
			ev.CalendarID = "cal1"
		}
		events.byID[ev.ID] = ev
	}
	return NewCalendarService(CalendarServiceDeps{
		Accounts:   accounts,
		Calendars:  newCalendarRepo(),
		Events:     events,
		Clock:      newClock(time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)),
		SelfHosted: true,
	})
}

func TestAvailabilityFreeWindows(t *testing.T) {
	ctx := context.Background()
	hm := func(h, m int) time.Time { return time.Date(2026, 7, 7, h, m, 0, 0, time.UTC) }
	confirmed := func(start, end time.Time) domain.Event {
		return domain.Event{Start: start, End: end, Status: domain.EventConfirmed}
	}
	slot := 30 * time.Minute

	tests := []struct {
		name         string
		from, to     time.Time
		slot         time.Duration
		accountEmail string
		events       []domain.Event
		want         []domain.AvailabilitySlot
	}{
		{
			name: "no events: whole window is one free slot",
			from: hm(9, 0), to: hm(17, 0), slot: slot,
			events: nil,
			want:   []domain.AvailabilitySlot{{Start: hm(9, 0), End: hm(17, 0)}},
		},
		{
			name: "single midday event splits the window",
			from: hm(9, 0), to: hm(17, 0), slot: slot,
			events: []domain.Event{confirmed(hm(12, 0), hm(13, 0))},
			want: []domain.AvailabilitySlot{
				{Start: hm(9, 0), End: hm(12, 0)},
				{Start: hm(13, 0), End: hm(17, 0)},
			},
		},
		{
			name: "overlapping events merge into one busy block",
			from: hm(9, 0), to: hm(17, 0), slot: slot,
			events: []domain.Event{
				confirmed(hm(10, 0), hm(11, 30)),
				confirmed(hm(11, 0), hm(12, 0)),
			},
			want: []domain.AvailabilitySlot{
				{Start: hm(9, 0), End: hm(10, 0)},
				{Start: hm(12, 0), End: hm(17, 0)},
			},
		},
		{
			name: "adjacent (touching) events merge with no zero-length slot",
			from: hm(9, 0), to: hm(17, 0), slot: slot,
			events: []domain.Event{
				confirmed(hm(10, 0), hm(11, 0)),
				confirmed(hm(11, 0), hm(12, 0)),
			},
			want: []domain.AvailabilitySlot{
				{Start: hm(9, 0), End: hm(10, 0)},
				{Start: hm(12, 0), End: hm(17, 0)},
			},
		},
		{
			name: "all-day event never marks the day busy",
			from: hm(9, 0), to: hm(17, 0), slot: slot,
			events: []domain.Event{{Start: hm(0, 0), End: hm(23, 59), AllDay: true, Status: domain.EventConfirmed}},
			want:   []domain.AvailabilitySlot{{Start: hm(9, 0), End: hm(17, 0)}},
		},
		{
			name: "gap shorter than slot filtered, gap equal to slot kept",
			from: hm(9, 0), to: hm(17, 0), slot: slot,
			events: []domain.Event{
				confirmed(hm(9, 30), hm(10, 0)),  // leaves a 30m gap [9:00,9:30] == slot: KEPT
				confirmed(hm(10, 20), hm(11, 0)), // leaves a 20m gap [10:00,10:20] < slot: DROPPED
			},
			want: []domain.AvailabilitySlot{
				{Start: hm(9, 0), End: hm(9, 30)},
				{Start: hm(11, 0), End: hm(17, 0)},
			},
		},
		{
			name: "cancelled event is treated as free",
			from: hm(9, 0), to: hm(17, 0), slot: slot,
			events: []domain.Event{{Start: hm(12, 0), End: hm(13, 0), Status: domain.EventCancelled}},
			want:   []domain.AvailabilitySlot{{Start: hm(9, 0), End: hm(17, 0)}},
		},
		{
			name:         "event the user declined is treated as free",
			from:         hm(9, 0), to: hm(17, 0), slot: slot,
			accountEmail: "me@x.com",
			events: []domain.Event{{
				Start: hm(12, 0), End: hm(13, 0), Status: domain.EventConfirmed,
				Attendees: []domain.Attendee{{Email: "me@x.com", Response: domain.RsvpDeclined}},
			}},
			want: []domain.AvailabilitySlot{{Start: hm(9, 0), End: hm(17, 0)}},
		},
		{
			name:         "event the user accepted stays busy",
			from:         hm(9, 0), to: hm(17, 0), slot: slot,
			accountEmail: "me@x.com",
			events: []domain.Event{{
				Start: hm(12, 0), End: hm(13, 0), Status: domain.EventConfirmed,
				Attendees: []domain.Attendee{{Email: "me@x.com", Response: domain.RsvpAccepted}},
			}},
			want: []domain.AvailabilitySlot{
				{Start: hm(9, 0), End: hm(12, 0)},
				{Start: hm(13, 0), End: hm(17, 0)},
			},
		},
		{
			name: "event starting before the window is clamped to from",
			from: hm(9, 0), to: hm(17, 0), slot: slot,
			events: []domain.Event{confirmed(hm(8, 0), hm(9, 30))},
			want:   []domain.AvailabilitySlot{{Start: hm(9, 30), End: hm(17, 0)}},
		},
		{
			name: "event ending after the window is clamped to to",
			from: hm(9, 0), to: hm(17, 0), slot: slot,
			events: []domain.Event{confirmed(hm(16, 0), hm(18, 0))},
			want:   []domain.AvailabilitySlot{{Start: hm(9, 0), End: hm(16, 0)}},
		},
		{
			name: "event covering the whole window leaves no free time",
			from: hm(9, 0), to: hm(17, 0), slot: slot,
			events: []domain.Event{confirmed(hm(9, 0), hm(17, 0))},
			want:   []domain.AvailabilitySlot{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newAvailFixture(t, tt.accountEmail, tt.events)
			got, err := svc.Availability(ctx, "u1", tt.from, tt.to, tt.slot)
			if err != nil {
				t.Fatalf("Availability: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("slots = %v, want %v", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run** — Expected **PASS**. If any row fails, first re-derive the expected slots by hand against `calendar.go` lines 273-285 (cursor never moves backwards; a gap is emitted only when `b.start - cursor >= slotDuration`; the trailing slot only when `to - cursor >= slotDuration`). A genuine off-by-one in the production merge is a real bug — record it and fix in a separate commit; do not weaken the table.
```bash
cd backend && go test ./internal/service/ -run TestAvailabilityFreeWindows -v
```

- [ ] **Step 3: Commit.**
```bash
cd backend && git add internal/service/calendar_test.go
git commit -m "test(service): table-drive CalendarService.Availability free-window computation"
```

---

#### Cycle C — remaining read/patch/write-through/paywall coverage (enumerated)

- [ ] **Step 1: Write the tests.** Add the functions below to `calendar_test.go`, reusing `newCalFixture(t)` (Cycle A) as the base wiring. For the write-through mutators (`UpdateEvent`/`DeleteEvent`/`RSVP`) seed the target event directly into `f.events.byID`, e.g.:
```go
f.events.byID["ev1"] = domain.Event{
    ID: "ev1", CalendarID: "cal1", ProviderEventID: "pe-1",
    Title: "Standup", Start: base.Add(time.Hour), End: base.Add(90 * time.Minute),
    Status: domain.EventConfirmed, Attendees: []domain.Attendee{},
}
```
For the paywall test build a second service with `SelfHosted: false`, `Subscriptions: newSubscriptionRepo()` (empty ⇒ no subscription). Compare every error with `errors.Is` against the sentinel named; add `"errors"` to the import block.

**Required cases** (each bullet is one mandatory subtest; group by the listed test func):

`TestListCalendars`
- `ListCalendars(ctx,"u1")` with two calendars seeded in `calendars.byID` ⇒ returns both (len 2) and `calendars.lastListUserID == "u1"`.
- `ListCalendars(ctx,"u1")` with an empty repo ⇒ returns non-nil empty `[]domain.Calendar{}` (nil-normalization at calendar.go:61-63), `err == nil`.

`TestUpdateCalendar`
- Patch `IsVisible=ptr(false)` + `Color=ptr("#ff0000")` on `cal1` ⇒ returned `domain.Calendar` has `IsVisible==false` and `Color=="#ff0000"`, and `calendars.byID["cal1"]` reflects both (persisted via `Update`).
- Patch with only `Color=ptr("#00ff00")` (`IsVisible=nil`) ⇒ `Color` updated, `IsVisible` unchanged from the seeded `true`.
- Foreign calendar: seed `cal1` under account `a1`/user `u1`, call with `userID="intruder"` ⇒ `errors.Is(err, domain.ErrNotFound)` (ownership ⇒ NotFound, never Unauthorized).
- Unknown calendar id (`"nope"`, absent from `byID`) ⇒ `errors.Is(err, domain.ErrNotFound)`.

`TestListEvents`
- `ListEvents(ctx,"u1", from, to, []string{"cal1","cal2"})` ⇒ delegates to `EventRepo.ListInRange`: assert `events.lastListUserID=="u1"`, `events.lastListFrom.Equal(from)`, `events.lastListTo.Equal(to)`, `reflect.DeepEqual(events.lastListCalendarIDs, []string{"cal1","cal2"})`.
- Empty result ⇒ non-nil empty `[]domain.Event{}` (calendar.go:100-102), `err == nil`.
- `to == from` (and `to.Before(from)`) ⇒ `errors.Is(err, domain.ErrValidation)`.

`TestCreateEventValidation` (table; each ⇒ `errors.Is(err, want)` and provider `createCalls == 0`)
- `CalendarID: ""` ⇒ `domain.ErrValidation`.
- `Title: "   "` (whitespace) ⇒ `domain.ErrValidation`.
- `End == Start` (not after) ⇒ `domain.ErrValidation`.
- Calendar with `CanWrite=false` ⇒ `domain.ErrValidation` (read-only).
- Foreign calendar (owned by `u1`, called as `"intruder"`) ⇒ `domain.ErrNotFound`.
- Unknown calendar id ⇒ `domain.ErrNotFound`.

`TestCreateEventNoProvider`
- Rebuild the service with an **empty** `CalendarProviders` map (no gateway for `google`); `CreateEvent` a valid event ⇒ provider skipped, returned event has a generated `ID`, `CalendarID=="cal1"`, `ProviderEventID==""`, `Status==domain.EventConfirmed` (from `eventFromInput`), and it is present in `events.byID`.

`TestUpdateEventWriteThrough`
- Seed `ev1` (`ProviderEventID="pe-1"`); set `f.provider.updateResult = domain.Event{ProviderEventID:"pe-1", Title:"Renamed", Status: domain.EventConfirmed}`; `UpdateEvent(ctx,"u1","ev1", domain.EventPatch{Title: ptr("Renamed")})` ⇒ `provider.updateCalls==1`, `provider.lastUpdateProviderEventID=="pe-1"`, `provider.lastProviderCalendarID=="prov-cal-1"`, `*provider.lastUpdatePatch.Title=="Renamed"`; returned event has `ID=="ev1"`, `CalendarID=="cal1"`, `Title=="Renamed"`; persisted in `events.byID["ev1"]`.
- Local-only event (`ProviderEventID==""`) with provider configured ⇒ `provider.updateCalls==0`; local mirror still patched (`Title` applied by `applyEventPatch`) and persisted.
- `patch.Start=ptr(t2)` + `patch.End=ptr(t1)` with `!t1.After(t2)` ⇒ `errors.Is(err, domain.ErrValidation)` (guard needs both Start and End non-nil).
- Read-only calendar (`CanWrite=false`) ⇒ `errors.Is(err, domain.ErrValidation)`.
- Foreign event (owned by `u1`, called as `"intruder"`) ⇒ `errors.Is(err, domain.ErrNotFound)`.
- Unknown event id ⇒ `errors.Is(err, domain.ErrNotFound)`.

`TestDeleteEventWriteThrough`
- Seed `ev1` (`ProviderEventID="pe-1"`); `DeleteEvent(ctx,"u1","ev1")` ⇒ `provider.deleteCalls==1`, `provider.lastDeleteProviderEventID=="pe-1"`, `provider.lastProviderCalendarID=="prov-cal-1"`; then `events.lastDeletedID=="ev1"` and `ev1` absent from `events.byID`; `err == nil`.
- Local-only event (`ProviderEventID==""`) ⇒ `provider.deleteCalls==0`, but `EventRepo.Delete` still called and the row removed.
- Read-only calendar ⇒ `errors.Is(err, domain.ErrValidation)` and `provider.deleteCalls==0`.
- Foreign event (`"intruder"`) ⇒ `errors.Is(err, domain.ErrNotFound)`.

`TestRSVPWriteThrough`
- Seed `ev1` (`ProviderEventID="pe-1"`, `Attendees: [{Email:"me@x.com", Response: domain.RsvpNeedsAction}]`; account `a1` email is `me@x.com`); `RSVP(ctx,"u1","ev1", domain.RsvpAccepted)` ⇒ `provider.rsvpCalls==1`, `provider.lastRSVPStatus==domain.RsvpAccepted`, `provider.lastRSVPProviderEventID=="pe-1"`; returned event's matching attendee `Response==domain.RsvpAccepted`; `events.byID["ev1"].Attendees[0].Response==domain.RsvpAccepted`.
- Local-only event (`ProviderEventID==""`) ⇒ `provider.rsvpCalls==0`; attendee still updated locally and persisted.
- Attendee list has no address matching the account email ⇒ provider still called (when `ProviderEventID` set), but no attendee mutated; event upserted with attendees unchanged.
- Foreign event (`"intruder"`) ⇒ `errors.Is(err, domain.ErrNotFound)`.

`TestCalendarPaywall` (service built with `SelfHosted: false`, `Subscriptions: newSubscriptionRepo()` empty ⇒ `ErrNotFound` from the repo ⇒ `ErrPaymentRequired`). Each of the following ⇒ `errors.Is(err, domain.ErrPaymentRequired)` and NO provider/repo write:
- `ListCalendars(ctx,"u1")`
- `ListEvents(ctx,"u1", from, to, nil)`
- `UpdateCalendar(ctx,"u1","cal1", port.CalendarPatch{})`
- `CreateEvent(ctx,"u1", validInput)`
- `UpdateEvent(ctx,"u1","ev1", domain.EventPatch{})`
- `DeleteEvent(ctx,"u1","ev1")`
- `RSVP(ctx,"u1","ev1", domain.RsvpAccepted)`
- `Availability(ctx,"u1", from, to, 30*time.Minute)`

`TestAvailabilityValidation`
- `to == from` (and `to.Before(from)`) ⇒ `errors.Is(err, domain.ErrValidation)`.
- `slotDuration == 0` and `slotDuration == -time.Minute` ⇒ `errors.Is(err, domain.ErrValidation)`.

- [ ] **Step 2: Run** — Expected **PASS** (whole package, to catch any fake-contract drift from Task 1).
```bash
cd backend && go test ./internal/service/ -run 'TestListCalendars|TestUpdateCalendar|TestListEvents|TestCreateEvent|TestUpdateEventWriteThrough|TestDeleteEventWriteThrough|TestRSVPWriteThrough|TestCalendarPaywall|TestAvailability' -v
cd backend && go test ./internal/service/
```

- [ ] **Step 3: Commit.**
```bash
cd backend && git add internal/service/calendar_test.go
git commit -m "test(service): cover CalendarService list/update/rsvp/paywall + validation"
```

---

### Task 7: SearchService, AIService, DeviceService, UserService coverage

Characterization tests for the four "misc" services. All four are thin: `SearchService`
and `AIService` are paywalled (via the shared `entitlement`), `DeviceService` and
`UserService` are **not** paywalled. Read before writing:
`backend/internal/service/search.go`, `ai.go`, `device.go`, `user.go`, and the shared
helpers in `service.go` (`entitlement.require`, `ownedThread`, `ownedDraft`, `truncate`,
`ptr`, `newID`, `systemPromptFor`, const `searchLimit = 20`).

**Files:**
- Create/Test: `backend/internal/service/misc_services_test.go` (package `service`).
- Consumes (do NOT create here): `backend/internal/service/fakes_test.go` from **Task 1**.

**Interfaces:**
- **Consumes** (fakes + constructors from Task 1, exact signatures):
  - `newSubscriptionRepo() *fakeSubscriptionRepo` (persists via `Upsert(ctx, domain.Subscription) error`, keyed on `UserID`; `GetByUserID` returns `domain.ErrNotFound` when absent).
  - `newAccountRepo() *fakeAccountRepo` (exposes `byID map[string]domain.ConnectedAccount`; `GetByID`).
  - `newThreadRepo() *fakeThreadRepo` (exposes `byID map[string]domain.Thread`; programmable `searchResults []domain.Thread`, `searchErr error`; records `lastSearchUserID, lastSearchQuery string`, `lastSearchLimit int`).
  - `newEventRepo() *fakeEventRepo` (programmable `searchResults []domain.Event`, `searchErr error`; records `lastSearchUserID, lastSearchQuery string`, `lastSearchLimit int`).
  - `newMessageRepo() *fakeMessageRepo` (persists via `Upsert`; `ListByThread` returns messages whose `ThreadID` matches, in insertion order).
  - `newDraftRepo() *fakeDraftRepo` (exposes `byID map[string]domain.Draft`; `GetByID`).
  - `newAI() *fakeAI` (programmable `text, model string`, `err error`; records `lastSystem, lastUser string`, `calls int`; `Complete` returns `(text, model, err)`).
  - `newDeviceRepo() *fakeDeviceRepo` (exposes `byID map[string]domain.NotificationDevice`; counts `upserts int`, `deletes int`; `Upsert` stores by `ID`, `Delete` removes, `GetByID` returns `domain.ErrNotFound` when absent).
  - `newUserRepo() *fakeUserRepo` (exposes `byID map[string]domain.User`; `Upsert` stores by `ID`; `GetByID` returns `domain.ErrNotFound` when absent).
  - `newClock(t time.Time) *fakeClock` (`Now()`, `Advance(d)`, `Set(t)`).
- **Consumes** (real production, package-local — callable directly from the test):
  - `NewSearchService(subs port.SubscriptionRepo, threads port.ThreadRepo, events port.EventRepo, clock port.Clock, selfHosted bool) *SearchService`
  - `NewAIService(AIServiceDeps{...}) *AIService`
  - `NewDeviceService(devices port.DeviceRepo, clock port.Clock) *DeviceService`
  - `NewUserService(users port.UserRepo, clock port.Clock) *UserService`
  - unexported `systemPromptFor(domain.AiAction) string` and const `searchLimit` (same package — assert against them directly instead of hard-coding strings/numbers).
- **Produces** (test funcs): `TestSearchServiceSearch`, `TestSearchServiceDelegatesToRepos`, `TestAIServiceComposeAssemblesPrompt`, `TestAIServiceComposeValidation`, `TestDeviceServiceRegister`, `TestDeviceServiceUnregister`, `TestUserServiceEnsureUser`, `TestUserServiceGetUser`.

---

- [ ] **Step 1: Write the failing test** — create `backend/internal/service/misc_services_test.go` with the fully-coded `AIService.Compose` and `DeviceService.Register` tests, plus a representative `SearchService` delegate test to anchor the fake field names. (Full-package compile also needs Task 1's `fakes_test.go`.)

```go
package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- AIService.Compose -------------------------------------------------------

// TestAIServiceComposeAssemblesPrompt pins the exact system+user prompt handed
// to port.AI.Complete when a thread, a draft, and an instruction are all
// present, and that the response echoes the gateway's text+model. Read ai.go:
// the user prompt is "Conversation subject: <s>\n\n" + per-message
// "From <email> at <RFC3339>:\n<body>\n\n" (newest aiContextMessages, BodyText
// preferred over BodyHTML) + `Current draft (subject %q):\n<html>\n\n` +
// "Instruction: <prompt>", then strings.TrimSpace.
func TestAIServiceComposeAssemblesPrompt(t *testing.T) {
	const owner = "u1"
	base := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	sentAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	accounts := newAccountRepo()
	accounts.byID["a1"] = domain.ConnectedAccount{ID: "a1", UserID: owner}

	threads := newThreadRepo()
	threads.byID["t1"] = domain.Thread{ID: "t1", AccountID: "a1", Subject: "Q3 planning"}

	messages := newMessageRepo()
	if _, err := messages.Upsert(context.Background(), domain.Message{
		ID:       "m1",
		ThreadID: "t1",
		From:     domain.EmailAddress{Email: "boss@acme.com"},
		BodyText: "Let's sync Thursday.",
		SentAt:   sentAt,
	}); err != nil {
		t.Fatal(err)
	}

	drafts := newDraftRepo()
	drafts.byID["d1"] = domain.Draft{ID: "d1", AccountID: "a1", Subject: "Re: Q3 planning", BodyHTML: "<p>Sure</p>"}

	ai := newAI()
	ai.text = "Generated reply"
	ai.model = "openrouter/auto"

	svc := NewAIService(AIServiceDeps{
		Subscriptions: newSubscriptionRepo(),
		Accounts:      accounts,
		Threads:       threads,
		Messages:      messages,
		Drafts:        drafts,
		AI:            ai,
		Clock:         newClock(base),
		SelfHosted:    true, // bypass the paywall; entitlement tested separately
	})

	resp, err := svc.Compose(context.Background(), owner, domain.AiComposeRequest{
		Action:   domain.AiCompose,
		Prompt:   "make it warmer",
		ThreadID: "t1",
		DraftID:  "d1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "Generated reply" || resp.Model != "openrouter/auto" {
		t.Fatalf("response = %+v, want Text/Model from the AI gateway", resp)
	}
	if ai.lastSystem != systemPromptFor(domain.AiCompose) {
		t.Fatalf("system prompt = %q, want %q", ai.lastSystem, systemPromptFor(domain.AiCompose))
	}
	wantUser := "Conversation subject: Q3 planning\n\n" +
		"From boss@acme.com at 2026-01-02T03:04:05Z:\nLet's sync Thursday.\n\n" +
		"Current draft (subject \"Re: Q3 planning\"):\n<p>Sure</p>\n\n" +
		"Instruction: make it warmer"
	if ai.lastUser != wantUser {
		t.Fatalf("user prompt =\n%q\nwant\n%q", ai.lastUser, wantUser)
	}
}

// TestAIServiceComposeValidation covers ordering + sentinel paths in Compose:
// entitlement is checked BEFORE ParseAiAction, ParseAiAction before the
// prompt-required rule, and the prompt-required rule is skipped for AiSummarize.
func TestAIServiceComposeValidation(t *testing.T) {
	const owner = "u1"
	base := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)

	newSvc := func(selfHosted, entitled bool) (*AIService, *fakeAI) {
		accounts := newAccountRepo()
		accounts.byID["a1"] = domain.ConnectedAccount{ID: "a1", UserID: owner}
		accounts.byID["a2"] = domain.ConnectedAccount{ID: "a2", UserID: "someone-else"}
		threads := newThreadRepo()
		threads.byID["t1"] = domain.Thread{ID: "t1", AccountID: "a1", Subject: "Sub"}
		threads.byID["t2"] = domain.Thread{ID: "t2", AccountID: "a2", Subject: "Foreign"}
		subs := newSubscriptionRepo()
		if entitled {
			if err := subs.Upsert(context.Background(), domain.Subscription{UserID: owner, Status: domain.SubscriptionActive}); err != nil {
				t.Fatal(err)
			}
		}
		ai := newAI()
		ai.text, ai.model = "ok", "m"
		svc := NewAIService(AIServiceDeps{
			Subscriptions: subs,
			Accounts:      accounts,
			Threads:       threads,
			Messages:      newMessageRepo(),
			Drafts:        newDraftRepo(),
			AI:            ai,
			Clock:         newClock(base),
			SelfHosted:    selfHosted,
		})
		return svc, ai
	}

	tests := []struct {
		name       string
		selfHosted bool
		entitled   bool
		req        domain.AiComposeRequest
		wantErr    error // nil = success
		wantCalled bool  // whether port.AI.Complete should be invoked
	}{
		{"unknown action", true, false, domain.AiComposeRequest{Action: "translate", Prompt: "hi"}, domain.ErrValidation, false},
		{"empty action", true, false, domain.AiComposeRequest{Action: "", Prompt: "hi"}, domain.ErrValidation, false},
		{"compose without prompt", true, false, domain.AiComposeRequest{Action: domain.AiCompose}, domain.ErrValidation, false},
		{"reply without prompt", true, false, domain.AiComposeRequest{Action: domain.AiReply}, domain.ErrValidation, false},
		{"ask without prompt", true, false, domain.AiComposeRequest{Action: domain.AiAsk}, domain.ErrValidation, false},
		{"summarize without prompt is allowed", true, false, domain.AiComposeRequest{Action: domain.AiSummarize, ThreadID: "t1"}, nil, true},
		{"foreign thread is not found", true, false, domain.AiComposeRequest{Action: domain.AiAsk, Prompt: "q", ThreadID: "t2"}, domain.ErrNotFound, false},
		{"paywall wins over invalid action", false, false, domain.AiComposeRequest{Action: "", Prompt: ""}, domain.ErrPaymentRequired, false},
		{"entitled via active subscription", false, true, domain.AiComposeRequest{Action: domain.AiCompose, Prompt: "write it"}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, ai := newSvc(tt.selfHosted, tt.entitled)
			_, err := svc.Compose(context.Background(), owner, tt.req)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := ai.calls > 0; got != tt.wantCalled {
				t.Fatalf("AI invoked = %v, want %v", got, tt.wantCalled)
			}
		})
	}
}

// --- DeviceService.Register --------------------------------------------------

// TestDeviceServiceRegister covers platform/token validation and the happy
// path: a new ID is minted, CreatedAt comes from the clock, and the row is
// persisted through DeviceRepo.Upsert. Registration is deliberately NOT
// paywalled, so no subscription is wired.
func TestDeviceServiceRegister(t *testing.T) {
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		platform domain.DevicePlatform
		token    string
		wantErr  error // nil = success
	}{
		{"valid ios registration", domain.PlatformIOS, "apns-token-123", nil},
		{"valid web push registration", domain.PlatformWeb, `{"endpoint":"https://push.example/x"}`, nil},
		{"unknown platform", domain.DevicePlatform("blackberry"), "tok", domain.ErrValidation},
		{"empty platform", domain.DevicePlatform(""), "tok", domain.ErrValidation},
		{"empty token", domain.PlatformAndroid, "", domain.ErrValidation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			devices := newDeviceRepo()
			svc := NewDeviceService(devices, newClock(base))

			got, err := svc.Register(context.Background(), "u1", tt.platform, tt.token)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				if devices.upserts != 0 {
					t.Fatalf("Upsert called %d times on invalid input, want 0", devices.upserts)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.ID == "" {
				t.Fatal("Register did not mint an ID")
			}
			if got.UserID != "u1" {
				t.Fatalf("UserID = %q, want u1", got.UserID)
			}
			if got.Platform != tt.platform {
				t.Fatalf("Platform = %q, want %q", got.Platform, tt.platform)
			}
			if got.Token != tt.token {
				t.Fatalf("Token = %q, want %q", got.Token, tt.token)
			}
			if !got.CreatedAt.Equal(base) {
				t.Fatalf("CreatedAt = %v, want %v (clock.Now)", got.CreatedAt, base)
			}
			// Persisted through Upsert and readable by the minted ID.
			stored, err := devices.GetByID(context.Background(), got.ID)
			if err != nil {
				t.Fatalf("device not persisted: %v", err)
			}
			if stored.Token != tt.token {
				t.Fatalf("stored token = %q, want %q", stored.Token, tt.token)
			}
		})
	}
}

// --- SearchService (representative delegate test) ----------------------------

// TestSearchServiceDelegatesToRepos pins that Search fans out to both repos
// with the shared searchLimit and returns their rows unchanged (paywall
// bypassed via SelfHosted).
func TestSearchServiceDelegatesToRepos(t *testing.T) {
	threads := newThreadRepo()
	threads.searchResults = []domain.Thread{{ID: "t1", Subject: "budget review"}}
	events := newEventRepo()
	events.searchResults = []domain.Event{{ID: "e1", Title: "budget sync"}}

	svc := NewSearchService(newSubscriptionRepo(), threads, events, newClock(time.Now()), true)

	res, err := svc.Search(context.Background(), "u1", "  budget  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if threads.lastSearchLimit != searchLimit || events.lastSearchLimit != searchLimit {
		t.Fatalf("limits = thread:%d event:%d, want %d for both", threads.lastSearchLimit, events.lastSearchLimit, searchLimit)
	}
	if threads.lastSearchQuery != "budget" || events.lastSearchQuery != "budget" {
		t.Fatalf("query not trimmed before delegation: thread=%q event=%q", threads.lastSearchQuery, events.lastSearchQuery)
	}
	if threads.lastSearchUserID != "u1" || events.lastSearchUserID != "u1" {
		t.Fatal("userID not forwarded to repo Search")
	}
	if len(res.Threads) != 1 || res.Threads[0].ID != "t1" {
		t.Fatalf("threads = %+v, want the repo's rows", res.Threads)
	}
	if len(res.Events) != 1 || res.Events[0].ID != "e1" {
		t.Fatalf("events = %+v, want the repo's rows", res.Events)
	}
	var _ port.SearchResult = res // result type is port.SearchResult
}
```

- [ ] **Step 2: Run** the fully-coded tests. They exercise EXISTING code and are **Expected PASS**.

```bash
cd backend && go test ./internal/service/ \
  -run 'TestAIServiceComposeAssemblesPrompt|TestAIServiceComposeValidation|TestDeviceServiceRegister|TestSearchServiceDelegatesToRepos' \
  -count=1 -v
```

Expected PASS. If any FAILS: decide wrong-test vs real-bug. Likely wrong-test causes: prompt
whitespace/format drift (re-diff against `ai.go` lines 77/87/95/98 and the trailing
`strings.TrimSpace`), or wrong ordering assumption (entitlement runs before `ParseAiAction`).
A genuine format/ordering mismatch that contradicts `ai.go` is a real bug — note it and fix in
a SEPARATE commit, do not weaken the assertion.

- [ ] **Step 3: Commit.**

```bash
cd backend && git add internal/service/misc_services_test.go
git commit -m "test(service): cover AIService.Compose and DeviceService.Register

Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

- [ ] **Step 4: Add the enumerated remaining tests** to the same file. Each bullet below is
mandatory coverage; every case names the function under test, the scenario, and the exact
expected sentinel/result.

**`TestSearchServiceSearch` — `SearchService.Search`** (build with `NewSearchService(subs, threads, events, newClock(now), selfHosted)`):
- SelfHosted=true, query `""` ⇒ `errors.Is(err, domain.ErrValidation)`; `threads.lastSearchLimit == 0` (never delegated — validation precedes delegation).
- SelfHosted=true, query `"   \t "` (whitespace only) ⇒ `domain.ErrValidation` (trimmed to empty).
- SelfHosted=true, `threads.searchResults = nil`, `events.searchResults = nil`, query `"x"` ⇒ err nil AND `res.Threads != nil && len(res.Threads) == 0` AND `res.Events != nil && len(res.Events) == 0` (nil→empty normalization, search.go lines 48-53).
- SelfHosted=true, `threads.searchErr = errBoom` (a local `errors.New`) ⇒ returned err `errors.Is(err, errBoom)`; events repo NOT consulted (`events.lastSearchLimit == 0`), since threads.Search runs first.
- SelfHosted=true, threads OK, `events.searchErr = errBoom` ⇒ `errors.Is(err, errBoom)`.
- Paywall: SelfHosted=false, `subs` empty (no row for `"u1"`), query `""` ⇒ `errors.Is(err, domain.ErrPaymentRequired)` — entitlement short-circuits BEFORE the empty-query `ErrValidation`, proving ordering.
- Paywall passes: SelfHosted=false, seed `subs.Upsert(ctx, domain.Subscription{UserID:"u1", Status: domain.SubscriptionActive})`, query `"team"` ⇒ err nil, both repos delegated with `lastSearchLimit == searchLimit`.

**`TestDeviceServiceUnregister` — `DeviceService.Unregister`** (build with `NewDeviceService(devices, newClock(base))`; seed `devices.byID["d1"] = domain.NotificationDevice{ID:"d1", UserID:"u1", Platform: domain.PlatformIOS, Token:"t"}`):
- Owner unregisters own device: `Unregister(ctx, "u1", "d1")` ⇒ err nil; `devices.deletes == 1`; `devices.byID["d1"]` gone (subsequent `GetByID("d1")` ⇒ `domain.ErrNotFound`).
- Ownership invariant — foreign device: `Unregister(ctx, "intruder", "d1")` ⇒ `errors.Is(err, domain.ErrNotFound)` (NEVER `ErrUnauthorized`); `devices.deletes == 0` (Delete not reached).
- Missing device: `Unregister(ctx, "u1", "ghost")` ⇒ `errors.Is(err, domain.ErrNotFound)` (propagated from `devices.GetByID`); `devices.deletes == 0`.

**`TestUserServiceEnsureUser` — `UserService.EnsureUser`** (build with `NewUserService(newUserRepo(), newClock(base))`, `base := time.Date(2026,6,1,0,0,0,0,time.UTC)`):
- Empty subject: `EnsureUser(ctx, port.Identity{Subject: "", Email: "x@y.com"})` ⇒ `errors.Is(err, domain.ErrUnauthorized)`; `len(users.byID) == 0` (no Upsert attempted).
- Full identity: `port.Identity{Subject:"sub-1", Email:"a@b.com", Name:"Ada", AvatarURL:"https://img/a.png"}` ⇒ err nil; returned `u.ID == "sub-1"`, `u.Email == "a@b.com"`, `u.Name != nil && *u.Name == "Ada"`, `u.AvatarURL != nil && *u.AvatarURL == "https://img/a.png"`, `u.CreatedAt.Equal(base)`.
- Sparse identity (no name/avatar): `port.Identity{Subject:"sub-2", Email:"c@d.com"}` ⇒ err nil; `u.Name == nil` AND `u.AvatarURL == nil` (pointers only set when the source string is non-empty, user.go lines 32-37).
- Persistence: after a successful `EnsureUser`, `users.GetByID(ctx, "sub-1")` returns the same row (`ID`/`Email` match) — the service returns whatever `Upsert` echoes.

**`TestUserServiceGetUser` — `UserService.GetUser`** (passthrough to `UserRepo.GetByID`):
- Existing user: seed `users.byID["sub-1"] = domain.User{ID:"sub-1", Email:"a@b.com"}`; `GetUser(ctx, "sub-1")` ⇒ err nil, `got.Email == "a@b.com"` (unchanged passthrough).
- Unknown user: `GetUser(ctx, "nope")` ⇒ `errors.Is(err, domain.ErrNotFound)` (propagated unchanged from the repo).

- [ ] **Step 5: Run** the whole file. **Expected PASS**.

```bash
cd backend && go test ./internal/service/ \
  -run 'TestSearchServiceSearch|TestSearchServiceDelegatesToRepos|TestAIServiceCompose|TestDeviceService|TestUserService' \
  -count=1 -v
```

Expected PASS. Then a full-package sanity run:

```bash
cd backend && go test ./internal/service/ -count=1
```

- [ ] **Step 6: Commit.**

```bash
cd backend && git add internal/service/misc_services_test.go
git commit -m "test(service): cover Search/Device/User services (validation, paywall, ownership)

Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 8: SyncService — provider sync (SyncAccount) + scheduled work (ProcessDueWork)

**Files:**
- Create/Test: `backend/internal/service/sync_test.go` (package `service`, white-box).
- Reads (do not modify): `backend/internal/service/sync.go`, `backend/internal/service/service.go`, `backend/internal/service/classify.go`.
- Depends on Task 1: `backend/internal/service/fakes_test.go` (shared fakes) must exist and compile first.

**Interfaces:**
- **Consumes** (from Task 1 `fakes_test.go`, all package `service`):
  - `newAccountRepo() *fakeAccountRepo` — persists via `Create`/`Update`/`GetByID`; token store via `SaveTokens(ctx, id, port.TokenSet)` / `GetTokens(ctx, id) (port.TokenSet, error)`.
  - `newThreadRepo() *fakeThreadRepo`, `newMessageRepo() *fakeMessageRepo`, `newDraftRepo() *fakeDraftRepo`, `newLabelRepo() *fakeLabelRepo`, `newCalendarRepo() *fakeCalendarRepo`, `newEventRepo() *fakeEventRepo`, `newDeviceRepo() *fakeDeviceRepo`, `newSyncStateRepo() *fakeSyncStateRepo`.
  - `newMailProvider() *fakeMailProvider`, `newCalendarProvider() *fakeCalendarProvider`, `newOAuthGateway() *fakeOAuthGateway`, `newPush() *fakePush`.
  - `newClock(t time.Time) *fakeClock` with `Now() time.Time`, `Advance(d time.Duration)`, `Set(t time.Time)`.
  - Recording/programmable fields I depend on are listed in this task's realBehaviorNotes so Task 1 adds them.
- **Produces** (test funcs other tasks may reference by name, but none should): `TestProcessDueWorkDeliversClaimedDraft`, `TestProcessDueWorkSkipsUnclaimedDraft`, `TestSyncAccountHappyPath`, plus the enumerated funcs below.
- Real constructor under test: `service.NewSyncService(service.SyncServiceDeps{...})` (see repo `sync.go`). `MailProviders`/`CalendarProviders`/`OAuth` are `map[domain.Provider]port.X` keyed by `domain.ProviderGoogle` / `domain.ProviderMicrosoft`.

---

- [ ] **Step 1: Write the failing test — ProcessDueWork draft-claim, both outcomes**

  Two fully-coded characterization tests. Claim=true delivers exactly once and consumes the draft; claim=false (lost the race to a concurrent worker) never sends and leaves the draft intact.

  ```go
  package service

  import (
  	"context"
  	"errors"
  	"testing"
  	"time"

  	"calendium/backend/internal/domain"
  	"calendium/backend/internal/port"
  )

  // A claimed, due, threaded draft is delivered through the provider exactly
  // once, mirrored onto its local thread, and then deleted.
  func TestProcessDueWorkDeliversClaimedDraft(t *testing.T) {
  	ctx := context.Background()
  	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
  	clock := newClock(now)

  	accounts := newAccountRepo()
  	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
  		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@acme.com",
  	}); err != nil {
  		t.Fatal(err)
  	}
  	// Non-expired token so tokenSource returns it without a refresh.
  	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "at", ExpiresAt: now.Add(time.Hour)}); err != nil {
  		t.Fatal(err)
  	}

  	threads := newThreadRepo()
  	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", AccountID: "a1", ProviderThreadID: "pt1"}); err != nil {
  		t.Fatal(err)
  	}

  	drafts := newDraftRepo()
  	past := now.Add(-time.Minute) // due
  	tid := "t1"
  	if _, err := drafts.Create(ctx, domain.Draft{
  		ID: "d1", AccountID: "a1", ThreadID: &tid,
  		To:      []domain.EmailAddress{{Email: "friend@example.org"}},
  		Subject: "Lunch?", BodyHTML: "<p>hi</p>", ScheduledAt: &past,
  	}); err != nil {
  		t.Fatal(err)
  	}
  	drafts.claimResult = true

  	mail := newMailProvider()
  	mail.sendResult = port.SentMessage{ProviderMessageID: "pm1", ProviderThreadID: "pt1", SentAt: now}
  	messages := newMessageRepo()

  	svc := NewSyncService(SyncServiceDeps{
  		Accounts: accounts, Threads: threads, Messages: messages, Drafts: drafts,
  		MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mail},
  		OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
  		Clock:         clock,
  	})

  	if err := svc.ProcessDueWork(ctx); err != nil {
  		t.Fatalf("ProcessDueWork: %v", err)
  	}

  	// Claimed => delivered exactly once, threaded as a reply.
  	if mail.sendCalls != 1 {
  		t.Fatalf("Send calls = %d, want 1", mail.sendCalls)
  	}
  	if mail.lastSend.ProviderThreadID != "pt1" {
  		t.Fatalf("reply ProviderThreadID = %q, want pt1", mail.lastSend.ProviderThreadID)
  	}
  	// Sent message mirrored onto the local thread (non-draft).
  	msgs, err := messages.ListByThread(ctx, "t1")
  	if err != nil {
  		t.Fatal(err)
  	}
  	if len(msgs) != 1 || msgs[0].IsDraft {
  		t.Fatalf("thread messages = %+v, want one non-draft", msgs)
  	}
  	// AppendSentMessage bumped the thread counters.
  	th, err := threads.GetByID(ctx, "t1")
  	if err != nil {
  		t.Fatal(err)
  	}
  	if th.MessageCount != 1 || !th.LastMessageAt.Equal(now) {
  		t.Fatalf("thread after append = {count:%d last:%v}, want {1 %v}", th.MessageCount, th.LastMessageAt, now)
  	}
  	// Draft consumed after delivery.
  	if _, err := drafts.GetByID(ctx, "d1"); !errors.Is(err, domain.ErrNotFound) {
  		t.Fatalf("draft after send err = %v, want ErrNotFound (deleted)", err)
  	}
  }

  // Losing the claim race (ClaimScheduled=false) must not re-send the draft to
  // recipients and must leave the draft untouched.
  func TestProcessDueWorkSkipsUnclaimedDraft(t *testing.T) {
  	ctx := context.Background()
  	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
  	clock := newClock(now)

  	accounts := newAccountRepo()
  	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
  		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@acme.com",
  	}); err != nil {
  		t.Fatal(err)
  	}
  	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "at", ExpiresAt: now.Add(time.Hour)}); err != nil {
  		t.Fatal(err)
  	}

  	drafts := newDraftRepo()
  	past := now.Add(-time.Minute)
  	if _, err := drafts.Create(ctx, domain.Draft{
  		ID: "d1", AccountID: "a1",
  		To:      []domain.EmailAddress{{Email: "friend@example.org"}},
  		Subject: "hi", ScheduledAt: &past,
  	}); err != nil {
  		t.Fatal(err)
  	}
  	drafts.claimResult = false // already claimed by a concurrent worker

  	mail := newMailProvider()

  	svc := NewSyncService(SyncServiceDeps{
  		Accounts: accounts, Drafts: drafts,
  		MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mail},
  		OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
  		Clock:         clock,
  	})

  	if err := svc.ProcessDueWork(ctx); err != nil {
  		t.Fatalf("ProcessDueWork: %v", err)
  	}
  	if mail.sendCalls != 0 {
  		t.Fatalf("Send calls = %d, want 0 (no double-send after lost claim)", mail.sendCalls)
  	}
  	if _, err := drafts.GetByID(ctx, "d1"); err != nil {
  		t.Fatalf("unclaimed draft must survive, got err %v", err)
  	}
  }
  ```

- [ ] **Step 2: Run** — `cd backend && go test ./internal/service/ -run 'TestProcessDueWork' -count=1 -v` → **Expected PASS**.

- [ ] **Step 3: Commit** —
  ```bash
  cd backend && git add internal/service/sync_test.go
  git commit -m "test(service): cover ProcessDueWork scheduled-draft claim (send + no double-send)"
  ```

- [ ] **Step 4: Write the failing test — SyncAccount happy path (mail + calendar)**

  Fully-coded incremental pass: one mail page (label + new thread + newest message classified `important` at ingest) and one calendar page (one upserted event, one upstream deletion). Verifies cursors persisted per resource and the account finalized `active` with a clock-stamped `LastSyncedAt`.

  ```go
  // SyncAccount runs one incremental mail sync (from the stored cursor, threads
  // + messages upserted with split classification at ingest, NextCursor saved)
  // and one calendar sync (calendars + events upserted, DeletedIDs removed), then
  // stamps the account active.
  func TestSyncAccountHappyPath(t *testing.T) {
  	ctx := context.Background()
  	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
  	clock := newClock(now)

  	accounts := newAccountRepo()
  	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
  		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle,
  		Email: "me@acme.com", Status: domain.AccountSyncing,
  	}); err != nil {
  		t.Fatal(err)
  	}
  	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "valid", ExpiresAt: now.Add(time.Hour)}); err != nil {
  		t.Fatal(err)
  	}

  	// One mail page: a label, a brand-new thread, and its newest message
  	// (direct personal mail => classified important). ThreadID on the message
  	// carries the PROVIDER thread id at this boundary.
  	mail := newMailProvider()
  	mail.syncPage = port.MailSyncPage{
  		Labels:  []domain.Label{{ProviderLabelID: "L1", Name: "Work"}},
  		Threads: []domain.Thread{{ProviderThreadID: "pt1", Subject: "Lunch?", InInbox: true, LastMessageAt: now}},
  		Messages: []port.IncomingMessage{{
  			Message: domain.Message{
  				ProviderMessageID: "pm1", ThreadID: "pt1",
  				From:   domain.EmailAddress{Email: "friend@example.org"},
  				To:     []domain.EmailAddress{{Email: "me@acme.com"}},
  				SentAt: now,
  			},
  		}},
  		NextCursor: "mail-cursor-2",
  		HasMore:    false,
  	}

  	// One calendar with one upserted event and one upstream deletion.
  	cal := newCalendarProvider()
  	cal.calendars = []domain.Calendar{{ProviderCalendarID: "pc1", Name: "Personal"}}
  	cal.eventsPage = port.CalendarSyncPage{
  		Events:     []domain.Event{{ProviderEventID: "pe1", Title: "Standup", Start: now, End: now.Add(time.Hour)}},
  		DeletedIDs: []string{"pe-gone"},
  		NextCursor: "events-cursor-2",
  		HasMore:    false,
  	}

  	threads := newThreadRepo()
  	messages := newMessageRepo()
  	labels := newLabelRepo()
  	calendars := newCalendarRepo()
  	events := newEventRepo()
  	syncState := newSyncStateRepo()
  	// Pre-seed the calendar (matched on account+providerCalendarID so the local
  	// id survives) and the event the page deletes, so removal is observable.
  	if _, err := calendars.Upsert(ctx, domain.Calendar{ID: "c1", AccountID: "a1", ProviderCalendarID: "pc1"}); err != nil {
  		t.Fatal(err)
  	}
  	if _, err := events.Upsert(ctx, domain.Event{ID: "old", CalendarID: "c1", ProviderEventID: "pe-gone"}); err != nil {
  		t.Fatal(err)
  	}

  	svc := NewSyncService(SyncServiceDeps{
  		Accounts: accounts, Labels: labels, Threads: threads, Messages: messages,
  		Calendars: calendars, Events: events, SyncState: syncState,
  		MailProviders:     map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mail},
  		CalendarProviders: map[domain.Provider]port.CalendarProvider{domain.ProviderGoogle: cal},
  		OAuth:             map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
  		Clock:             clock,
  	})

  	if err := svc.SyncAccount(ctx, "a1"); err != nil {
  		t.Fatalf("SyncAccount: %v", err)
  	}

  	// Mail sync started from the stored cursor ("" on the first pass).
  	if mail.lastSyncCursor != "" {
  		t.Fatalf("initial mail cursor = %q, want empty", mail.lastSyncCursor)
  	}
  	// Thread ingested, classified important, with a local id.
  	th, err := threads.GetByProviderID(ctx, "a1", "pt1")
  	if err != nil {
  		t.Fatalf("thread not upserted: %v", err)
  	}
  	if th.ID == "" {
  		t.Fatal("thread stored without a local id")
  	}
  	if th.Split != domain.SplitImportant {
  		t.Fatalf("split = %q, want %q (direct personal mail)", th.Split, domain.SplitImportant)
  	}
  	// Message mirrored onto the LOCAL thread id.
  	msgs, err := messages.ListByThread(ctx, th.ID)
  	if err != nil {
  		t.Fatal(err)
  	}
  	if len(msgs) != 1 {
  		t.Fatalf("messages on thread = %d, want 1", len(msgs))
  	}
  	// Exactly one calendar (matched, not duplicated); resolve its local id.
  	cals, err := calendars.ListByAccount(ctx, "a1")
  	if err != nil {
  		t.Fatal(err)
  	}
  	if len(cals) != 1 {
  		t.Fatalf("calendars = %d, want 1 (matched on providerCalendarID)", len(cals))
  	}
  	calID := cals[0].ID
  	// Event upserted; deleted id removed.
  	if _, err := events.GetByProviderID(ctx, calID, "pe1"); err != nil {
  		t.Fatalf("synced event missing: %v", err)
  	}
  	if _, err := events.GetByProviderID(ctx, calID, "pe-gone"); !errors.Is(err, domain.ErrNotFound) {
  		t.Fatalf("deleted event still present, err = %v", err)
  	}
  	// Cursors persisted for both resources.
  	if st, _ := syncState.Get(ctx, "a1", "mail"); st.Cursor != "mail-cursor-2" {
  		t.Fatalf("mail cursor = %q, want mail-cursor-2", st.Cursor)
  	}
  	if st, _ := syncState.Get(ctx, "a1", "events:pc1"); st.Cursor != "events-cursor-2" {
  		t.Fatalf("events cursor = %q, want events-cursor-2", st.Cursor)
  	}
  	// Account finalized: active + LastSyncedAt stamped from the clock.
  	fin, _ := accounts.GetByID(ctx, "a1")
  	if fin.Status != domain.AccountActive {
  		t.Fatalf("status = %q, want %q", fin.Status, domain.AccountActive)
  	}
  	if fin.LastSyncedAt == nil || !fin.LastSyncedAt.Equal(now) {
  		t.Fatalf("LastSyncedAt = %v, want %v", fin.LastSyncedAt, now)
  	}
  }
  ```

- [ ] **Step 5: Run** — `cd backend && go test ./internal/service/ -run 'TestSyncAccountHappyPath' -count=1 -v` → **Expected PASS**.

- [ ] **Step 6: Commit** —
  ```bash
  cd backend && git add internal/service/sync_test.go
  git commit -m "test(service): cover SyncAccount incremental mail+calendar happy path"
  ```

- [ ] **Step 7: Write the remaining enumerated cases** (add to `sync_test.go`; table-driven with `t.Run` where scenarios share a fixture, per `ops_test.go` style). Every bullet is mandatory coverage. Seed valid tokens via `accounts.SaveTokens` (ExpiresAt `now.Add(time.Hour)`) unless the case exercises refresh. Compare all errors with `errors.Is`.

  **SyncAccount:**
  - `SyncService.SyncAccount` — unknown accountID `"ghost"` (nothing seeded) ⇒ `errors.Is(err, domain.ErrNotFound)` (propagated unwrapped from `accounts.GetByID`); no provider calls (`mail.sendCalls == 0`).
  - `SyncService.SyncAccount` — expired token triggers refresh: seed `GetTokens` with `port.TokenSet{AccessToken:"old", RefreshToken:"rt", ExpiresAt: now.Add(-time.Hour)}`; `oauth.refreshResult = port.OAuthToken{TokenSet: port.TokenSet{AccessToken:"new", ExpiresAt: now.Add(time.Hour)}}`. Assert `oauth.refreshCalls == 1`, `oauth.lastRefreshToken == "rt"`, re-persisted (`accounts.GetTokens` returns AccessToken `"new"`), and `mail.lastSyncToken == "new"` (refreshed token used for sync). Result: nil error, account `active`.
  - `SyncService.SyncAccount` — refresh failure: `oauth.refreshErr = errors.New("boom")` with expired token ⇒ `errors.Is(err, domain.ErrUnauthorized)`; account left `domain.AccountReauthRequired` (assert via `accounts.GetByID`); no `accounts.Update` to `active`.
  - `SyncService.SyncAccount` — refresh with empty returned RefreshToken keeps the old one: `oauth.refreshResult` has `RefreshToken:""` ⇒ persisted token retains `RefreshToken:"rt"` (assert via `accounts.GetTokens`).
  - `SyncService.SyncAccount` — no mail provider configured (`MailProviders` empty map) ⇒ `syncMail` no-ops, calendar still runs, result nil, account `active`, no `mail`/thread writes; mail cursor never saved (`syncState.Get(ctx,"a1","mail")` ⇒ `domain.ErrNotFound`).
  - `SyncService.SyncAccount` — no calendar provider configured (`CalendarProviders` empty) ⇒ `syncCalendars` no-ops, mail still runs, result nil, account `active`.
  - `SyncService.SyncAccount` — label remap onto local ids: page `Threads[0].LabelIDs = []string{"L1"}` and page `Labels = [{ProviderLabelID:"L1"}]`; assert the saved thread's `LabelIDs` equals `[]string{ <local id of L1 from labels.ListByAccount> }` (provider label id replaced by local id; persisted through `SetLabels`).
  - `SyncService.SyncAccount` — existing thread preserves local-only state: pre-seed thread `{ID:"t1",AccountID:"a1",ProviderThreadID:"pt1",OpenedAt:&past,SnoozedUntil:&future,RemindAt:&future,Split:SplitTeam,LastMessageAt: now}`; page re-syncs `pt1` with a message whose `From` is the owner (`me@acme.com`, SentAt `now`) so there is no new inbound reply. Assert saved thread keeps `OpenedAt` non-nil, `SnoozedUntil`/`RemindAt` unchanged (not cleared), and `Split == SplitTeam` (existing split retained — no classifying inbound message overrode it).
  - `SyncService.SyncAccount` — inbound reply resurfaces thread: pre-seed thread `pt1` with `SnoozedUntil:&future`, `RemindAt:&future`, `LastMessageAt: now.Add(-time.Hour)`; page carries a newer message from `stranger@x.com` (SentAt `now`). Assert saved thread `SnoozedUntil == nil` **and** `RemindAt == nil` (auto-unsnooze + reminder cancelled on reply).
  - `SyncService.SyncAccount` — push on new important mail: wire `Push: newPush()` and seed a device via `devices.Upsert(domain.NotificationDevice{ID:"dev1",UserID:"u1",Platform:domain.PlatformIOS,Token:"tok"})`; new inbox thread + newest message classified `important`. Assert `push.sendCalls == 1`, `push.lastTitle == "New email"`, `push.lastData["threadId"] == <local thread id>`.
  - `SyncService.SyncAccount` — push VIP title: account `VIPSenders: []string{"boss@corp.com"}`, message `From: boss@corp.com` ⇒ classified `vip`; assert `push.lastTitle == "New VIP email"`.
  - `SyncService.SyncAccount` — no push on non-important split: message classified `news` (e.g. `List-Unsubscribe` header) ⇒ `push.sendCalls == 0` even though push is wired.
  - `SyncService.SyncAccount` — orphan message skipped: page has a `Messages` entry with `ThreadID:"pt-unknown"` not present in `page.Threads` and not in the repo ⇒ message silently skipped (`continue`), `SyncAccount` returns nil, `messages.ListByThread` for any known thread excludes it (no orphan persisted).
  - `SyncService.SyncAccount` — calendar DeletedIDs tolerant of already-absent id: page `DeletedIDs = ["never-existed"]` with nothing seeded ⇒ `events.DeleteByProviderID` returns `domain.ErrNotFound` internally but `syncEvents` swallows it; `SyncAccount` returns nil.
  - `SyncService.SyncAccount` — multi-page mail drains until `HasMore=false`: `mail.syncPages = [{NextCursor:"c1",HasMore:true}, {NextCursor:"c2",HasMore:false}]`; assert `mail.syncCalls == 2`, the second call received cursor `"c1"` (`mail.lastSyncCursor == "c1"`), and the final saved mail cursor is `"c2"`.
  - `SyncService.SyncAccount` — provider SyncMail error aborts and wraps: `mail.syncErr = errors.New("gmail 500")` ⇒ `SyncAccount` returns a non-nil error containing `"sync mail for account a1"`; account NOT stamped `active` (still `syncing`).

  **ProcessDueWork:**
  - `SyncService.ProcessDueWork` — standalone draft (`ThreadID: nil`) claimed ⇒ delivered once, draft deleted, but NO message mirrored and NO `AppendSentMessage` (guarded by `msg.ThreadID != ""`): assert `messages.ListByThread` empty for every thread and `mail.sendCalls == 1`.
  - `SyncService.ProcessDueWork` — provider send error re-arms for retry: `mail.sendErr = errors.New("smtp 421")`, draft `SendAttempts: 0`; assert `drafts.recordSendFailureCalls == 1`, `drafts.lastFailureID == "d1"`, `drafts.lastFailureNext != nil` and equals `now.Add(30*time.Second)` (first backoff), `drafts.lastFailureMsg` contains `"smtp 421"`; `ProcessDueWork` returns non-nil (joined) error; draft NOT deleted.
  - `SyncService.ProcessDueWork` — dead-letter after cap: draft `SendAttempts: maxSendAttempts-1` (=4) with `mail.sendErr` set ⇒ attempts reach 5 ≥ cap; assert `drafts.lastFailureNext == nil` (dead-lettered, scheduled_at left clear), error returned, draft not deleted.
  - `SyncService.ProcessDueWork` — wakes snoozed threads: `threads.snoozeDue = [{ID:"t1",AccountID:"a1"}]`; assert `threads.clearedSnoozeIDs` contains `"t1"`; with push wired + a device, `push.lastTitle == "Snoozed conversation is back"`.
  - `SyncService.ProcessDueWork` — fires reminders: `threads.remindersDue = [{ID:"t2",AccountID:"a1",Subject:"ping"}]`; assert `threads.clearedReminderIDs` contains `"t2"`; `push.lastTitle == "Follow-up reminder"` and `push.lastBody == "ping"`.
  - `SyncService.ProcessDueWork` — Push nil is safe: `Push` omitted (nil) with due snoozes and reminders queued ⇒ `ClearSnooze`/`ClearReminder` still invoked, `notifyThread` returns early, no panic, `ProcessDueWork` returns nil.
  - `SyncService.ProcessDueWork` — ClearSnooze error is collected but non-fatal: `threads.clearSnoozeErr = errors.New("db")` with one due snooze plus one due reminder; assert `ProcessDueWork` returns non-nil (joined) error yet the reminder path still ran (`threads.clearedReminderIDs` contains the reminder id).
  - `SyncService.ProcessDueWork` — nothing due: all `ListScheduledDue`/`ListSnoozeDue`/`ListRemindersDue` empty ⇒ returns nil, `mail.sendCalls == 0`, `push.sendCalls == 0`.

- [ ] **Step 8: Run** — `cd backend && go test ./internal/service/ -run 'TestSyncAccount|TestProcessDueWork' -count=1 -v` → **Expected PASS**. If any case FAILS, decide wrong-test vs real bug: for a real bug, record it in the commit body and fix production code in a **separate** commit (do not weaken the assertion).

- [ ] **Step 9: Commit** —
  ```bash
  cd backend && git add internal/service/sync_test.go
  git commit -m "test(service): cover SyncService refresh, classification, push, retry/dead-letter, snooze/reminder"
  ```

---

### Task 9: service.go helpers — newID / randomToken / truncate / firstNonEmpty + ownership 404 semantics

**Files:**
- Create/Test: `backend/internal/service/helpers_test.go` (package `service`, white-box).
- Reads (do not modify): `backend/internal/service/service.go`.
- Depends on Task 1 fakes for the ownership test (`newAccountRepo`, `newThreadRepo`, `newDraftRepo`).

**Interfaces:**
- **Consumes:** package-level `newID()`, `randomToken(n int)`, `truncate(s string, n int)`, `firstNonEmpty(vals ...string)`, `ownedAccount`, `ownedThread`, `ownedDraft` (all in `service.go`); Task 1 `newAccountRepo()`, `newThreadRepo()`, `newDraftRepo()`.
- **Produces:** `TestNewID`, `TestRandomToken`, `TestTruncate`, `TestFirstNonEmpty`, `TestOwnershipHelpers404OnForeign`.
- Real signatures: `ownedAccount(ctx, accounts port.AccountRepo, userID, accountID string) (domain.ConnectedAccount, error)`; `ownedThread(ctx, threads port.ThreadRepo, accounts port.AccountRepo, userID, threadID string) (domain.Thread, domain.ConnectedAccount, error)`; `ownedDraft(ctx, drafts port.DraftRepo, accounts port.AccountRepo, userID, draftID string) (domain.Draft, domain.ConnectedAccount, error)`.

---

- [ ] **Step 1: Write the failing test — helpers + ownership**

  Fully coded. Note `truncate` slices by **bytes** (`s[:n]`) and appends the multi-byte ellipsis rune `"…"` (U+2026) — inputs are ASCII so byte length equals rune length.

  ```go
  package service

  import (
  	"context"
  	"encoding/hex"
  	"errors"
  	"testing"

  	"calendium/backend/internal/domain"
  )

  func TestNewID(t *testing.T) {
  	seen := make(map[string]struct{}, 1000)
  	for i := 0; i < 1000; i++ {
  		id := newID()
  		if len(id) != 32 {
  			t.Fatalf("len(newID()) = %d, want 32", len(id))
  		}
  		if _, err := hex.DecodeString(id); err != nil {
  			t.Fatalf("newID() = %q is not hex: %v", id, err)
  		}
  		if _, dup := seen[id]; dup {
  			t.Fatalf("newID() collision on %q", id)
  		}
  		seen[id] = struct{}{}
  	}
  }

  func TestRandomToken(t *testing.T) {
  	for _, n := range []int{0, 1, 16, 32} {
  		tok := randomToken(n)
  		if len(tok) != 2*n { // hex-encoded n bytes
  			t.Fatalf("len(randomToken(%d)) = %d, want %d", n, len(tok), 2*n)
  		}
  		if _, err := hex.DecodeString(tok); err != nil {
  			t.Fatalf("randomToken(%d) = %q is not hex: %v", n, tok, err)
  		}
  	}
  	if randomToken(16) == randomToken(16) {
  		t.Fatal("randomToken is not random across calls")
  	}
  }

  func TestTruncate(t *testing.T) {
  	tests := []struct {
  		name, in string
  		n        int
  		want     string
  	}{
  		{"shorter than n unchanged", "hi", 5, "hi"},
  		{"equal to n unchanged", "hello", 5, "hello"},
  		{"longer truncated with ellipsis", "hello world", 5, "hello…"},
  		{"n zero keeps only ellipsis", "abc", 0, "…"},
  	}
  	for _, tt := range tests {
  		t.Run(tt.name, func(t *testing.T) {
  			if got := truncate(tt.in, tt.n); got != tt.want {
  				t.Fatalf("truncate(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
  			}
  		})
  	}
  }

  func TestFirstNonEmpty(t *testing.T) {
  	tests := []struct {
  		name string
  		in   []string
  		want string
  	}{
  		{"no args", nil, ""},
  		{"all empty", []string{"", ""}, ""},
  		{"first wins", []string{"a", "b"}, "a"},
  		{"skips leading empties", []string{"", "", "c"}, "c"},
  	}
  	for _, tt := range tests {
  		t.Run(tt.name, func(t *testing.T) {
  			if got := firstNonEmpty(tt.in...); got != tt.want {
  				t.Fatalf("firstNonEmpty(%v) = %q, want %q", tt.in, got, tt.want)
  			}
  		})
  	}
  }

  // Ownership invariant: a resource owned by another user is indistinguishable
  // from a missing one (404, never 403/ErrUnauthorized).
  func TestOwnershipHelpers404OnForeign(t *testing.T) {
  	ctx := context.Background()
  	accounts := newAccountRepo()
  	threads := newThreadRepo()
  	drafts := newDraftRepo()

  	if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", UserID: "owner"}); err != nil {
  		t.Fatal(err)
  	}
  	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", AccountID: "a1"}); err != nil {
  		t.Fatal(err)
  	}
  	if _, err := drafts.Create(ctx, domain.Draft{ID: "d1", AccountID: "a1"}); err != nil {
  		t.Fatal(err)
  	}

  	// Owner resolves all three.
  	if _, err := ownedAccount(ctx, accounts, "owner", "a1"); err != nil {
  		t.Fatalf("owner account: %v", err)
  	}
  	if _, _, err := ownedThread(ctx, threads, accounts, "owner", "t1"); err != nil {
  		t.Fatalf("owner thread: %v", err)
  	}
  	if _, _, err := ownedDraft(ctx, drafts, accounts, "owner", "d1"); err != nil {
  		t.Fatalf("owner draft: %v", err)
  	}

  	// A stranger gets ErrNotFound, never ErrUnauthorized.
  	if _, err := ownedAccount(ctx, accounts, "intruder", "a1"); !errors.Is(err, domain.ErrNotFound) {
  		t.Fatalf("foreign account err = %v, want ErrNotFound", err)
  	}
  	if _, _, err := ownedThread(ctx, threads, accounts, "intruder", "t1"); !errors.Is(err, domain.ErrNotFound) {
  		t.Fatalf("foreign thread err = %v, want ErrNotFound", err)
  	}
  	if _, _, err := ownedDraft(ctx, drafts, accounts, "intruder", "d1"); !errors.Is(err, domain.ErrNotFound) {
  		t.Fatalf("foreign draft err = %v, want ErrNotFound", err)
  	}

  	// Genuinely missing ids also 404.
  	if _, err := ownedAccount(ctx, accounts, "owner", "nope"); !errors.Is(err, domain.ErrNotFound) {
  		t.Fatalf("missing account err = %v, want ErrNotFound", err)
  	}
  }
  ```

  **Required cases** (all covered by the funcs above; every bullet mandatory):
  - `newID` — returns 32 hex chars, valid hex, unique across 1000 calls.
  - `randomToken` — returns exactly `2*n` hex chars for n ∈ {0,1,16,32}, valid hex, differs across calls.
  - `truncate` — `len(s) <= n` returned unchanged (incl. `len==n`); `len(s) > n` returns `s[:n] + "…"` (byte-sliced prefix + U+2026); `n == 0` yields just `"…"`.
  - `firstNonEmpty` — no args ⇒ `""`; all empty ⇒ `""`; returns the first non-empty; skips leading empties.
  - `ownedAccount` / `ownedThread` / `ownedDraft` — owner resolves; foreign owner ⇒ `domain.ErrNotFound`; missing id ⇒ `domain.ErrNotFound`.

- [ ] **Step 2: Run** — `cd backend && go test ./internal/service/ -run 'TestNewID|TestRandomToken|TestTruncate|TestFirstNonEmpty|TestOwnershipHelpers404OnForeign' -count=1 -v` → **Expected PASS**.

- [ ] **Step 3: Commit** —
  ```bash
  cd backend && git add internal/service/helpers_test.go
  git commit -m "test(service): cover id/token/truncate/firstNonEmpty helpers and ownership 404 semantics"
  ```

---

### Task 10: HTTP test harness (package httpapi)

**Files:**
- Create/Test: `backend/internal/adapter/in/httpapi/harness_test.go`

This harness is the shared fixture for every Task 11 handler/middleware test. It provides:
- Configurable **doubles** for the eight driving ports (`port.UserService`, `port.BillingService`, `port.AccountService`, `port.MailService`, `port.CalendarService`, `port.SearchService`, `port.AIService`, `port.DeviceService`) — each records the args it receives and returns programmable values/errors.
- A `fakeVerifier` implementing `port.TokenVerifier` (map `token => port.Identity`, or a canned error).
- `newHarness(t)` wiring all doubles into `Deps` with a discard logger.
- `h.handler()` → the full stack via `New(h.deps)`; `h.server()` → a bare `*server` for unit-testing individual middleware.
- `h.authed(method, target, body)` — issues a request with `Authorization: Bearer <defaultToken>`; `h.anon(...)` — no auth header.
- `decodeErr(t, rec)` helper for the `{ "error": { code, message } }` envelope.

The doubles use white-box package `httpapi` (same package as the code under test), so they can construct `&server{deps: ...}` and reference unexported helpers (`userFrom`, `bearerToken`, `corsMiddleware`, `statusFor`, `safeMessage`, `decodeJSON`, `errorBody`).

**Interfaces:**
- Consumes: `New(Deps) http.Handler`, `Deps{...}`, `server`, and every driving port from `calendium/backend/internal/port`.
- Produces: `newHarness`, `harness`, `fakeVerifier`, `fakeUserService`, `fakeBillingService`, `fakeAccountService`, `fakeMailService`, `fakeCalendarService`, `fakeSearchService`, `fakeAIService`, `fakeDeviceService`, `defaultToken`, `defaultUserID`, `decodeErr`, `jsonBody`.

- [ ] **Step 1: Write the harness file**

```go
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// Compile-time proof every double satisfies its port.
var (
	_ port.TokenVerifier   = (*fakeVerifier)(nil)
	_ port.UserService     = (*fakeUserService)(nil)
	_ port.BillingService  = (*fakeBillingService)(nil)
	_ port.AccountService  = (*fakeAccountService)(nil)
	_ port.MailService     = (*fakeMailService)(nil)
	_ port.CalendarService = (*fakeCalendarService)(nil)
	_ port.SearchService   = (*fakeSearchService)(nil)
	_ port.AIService       = (*fakeAIService)(nil)
	_ port.DeviceService   = (*fakeDeviceService)(nil)
)

const (
	defaultToken  = "valid-token"
	defaultUserID = "user_1"
)

// --- token verifier ----------------------------------------------------------

type fakeVerifier struct {
	tokens map[string]port.Identity // token => identity
	err    error                    // when non-nil, Verify always fails
	calls  int
	gotJWT string
}

func (f *fakeVerifier) Verify(ctx context.Context, jwt string) (port.Identity, error) {
	f.calls++
	f.gotJWT = jwt
	if f.err != nil {
		return port.Identity{}, f.err
	}
	id, ok := f.tokens[jwt]
	if !ok {
		return port.Identity{}, domain.ErrUnauthorized
	}
	return id, nil
}

// --- UserService -------------------------------------------------------------

type fakeUserService struct {
	ensureRet   domain.User
	ensureErr   error
	ensureCalls int
	gotIdentity port.Identity

	getRet domain.User
	getErr error
}

func (f *fakeUserService) EnsureUser(ctx context.Context, id port.Identity) (domain.User, error) {
	f.ensureCalls++
	f.gotIdentity = id
	return f.ensureRet, f.ensureErr
}
func (f *fakeUserService) GetUser(ctx context.Context, userID string) (domain.User, error) {
	return f.getRet, f.getErr
}

// --- BillingService ----------------------------------------------------------

type fakeBillingService struct {
	subRet domain.Subscription
	subErr error

	checkoutURL           string
	checkoutErr           error
	gotCheckoutSuccessURL string
	gotCheckoutCancelURL  string

	portalURL       string
	portalErr       error
	gotPortalReturn string

	webhookErr        error
	webhookCalls      int
	gotWebhookPayload []byte
	gotWebhookSig     string

	requireActiveErr error
}

func (f *fakeBillingService) GetSubscription(ctx context.Context, userID string) (domain.Subscription, error) {
	return f.subRet, f.subErr
}
func (f *fakeBillingService) CreateCheckoutSession(ctx context.Context, userID, successURL, cancelURL string) (string, error) {
	f.gotCheckoutSuccessURL, f.gotCheckoutCancelURL = successURL, cancelURL
	return f.checkoutURL, f.checkoutErr
}
func (f *fakeBillingService) CreatePortalSession(ctx context.Context, userID, returnURL string) (string, error) {
	f.gotPortalReturn = returnURL
	return f.portalURL, f.portalErr
}
func (f *fakeBillingService) HandleWebhook(ctx context.Context, payload []byte, sigHeader string) error {
	f.webhookCalls++
	f.gotWebhookPayload = payload
	f.gotWebhookSig = sigHeader
	return f.webhookErr
}
func (f *fakeBillingService) RequireActive(ctx context.Context, userID string) error {
	return f.requireActiveErr
}

// --- AccountService ----------------------------------------------------------

type fakeAccountService struct {
	listRet []domain.ConnectedAccount
	listErr error

	beginURL         string
	beginErr         error
	gotProvider      domain.Provider
	gotRedirectURL   string
	gotBeginBaseURL  string

	completeAccount  domain.ConnectedAccount
	completeRedirect string
	completeErr      error
	gotState         string
	gotCode          string

	vipRet    domain.ConnectedAccount
	vipErr    error
	gotVIPID  string
	gotVIP    []string

	disconnectErr    error
	gotDisconnectID  string
}

func (f *fakeAccountService) List(ctx context.Context, userID string) ([]domain.ConnectedAccount, error) {
	return f.listRet, f.listErr
}
func (f *fakeAccountService) BeginConnect(ctx context.Context, userID string, provider domain.Provider, redirectURL, requestBaseURL string) (string, error) {
	f.gotProvider, f.gotRedirectURL, f.gotBeginBaseURL = provider, redirectURL, requestBaseURL
	return f.beginURL, f.beginErr
}
func (f *fakeAccountService) CompleteConnect(ctx context.Context, provider domain.Provider, state, code, requestBaseURL string) (domain.ConnectedAccount, string, error) {
	f.gotProvider, f.gotState, f.gotCode = provider, state, code
	return f.completeAccount, f.completeRedirect, f.completeErr
}
func (f *fakeAccountService) SetVipSenders(ctx context.Context, userID, accountID string, vipSenders []string) (domain.ConnectedAccount, error) {
	f.gotVIPID, f.gotVIP = accountID, vipSenders
	return f.vipRet, f.vipErr
}
func (f *fakeAccountService) Disconnect(ctx context.Context, userID, accountID string) error {
	f.gotDisconnectID = accountID
	return f.disconnectErr
}

// --- MailService -------------------------------------------------------------

type fakeMailService struct {
	// ListThreads
	listPage      domain.Page[domain.Thread]
	listErr       error
	listCalls     int
	gotListUserID string
	gotListQuery  port.ThreadQuery

	// GetThread
	getThreadThread domain.Thread
	getThreadMsgs   []domain.Message
	getThreadErr    error
	gotGetThreadID  string

	// ActOnThread
	actRet    domain.Thread
	actErr    error
	gotAction domain.ThreadAction
	gotActID  string

	markOpenedErr error
	gotMarkID     string

	snoozeRet   domain.Thread
	snoozeErr   error
	gotSnoozeID string
	gotUntil    time.Time

	reminderRet   domain.Thread
	reminderErr   error
	gotReminderID string
	gotRemindAt   *time.Time

	// drafts
	createDraftRet domain.Draft
	createDraftErr error
	gotCreateDraft port.DraftInput

	updateDraftRet domain.Draft
	updateDraftErr error
	gotUpdateDraft port.DraftInput

	getDraftRet domain.Draft
	getDraftErr error

	listDraftsRet []domain.Draft
	listDraftsErr error

	deleteDraftErr error
	gotDeleteDraft string

	sendDraftRet domain.Message
	sendDraftErr error

	unsendDraftRet domain.Draft
	unsendDraftErr error

	// snippets
	listSnippetsRet []domain.Snippet
	listSnippetsErr error
	createSnippet   domain.Snippet
	createSnipErr   error
	updateSnippet   domain.Snippet
	updateSnipErr   error
	deleteSnipErr   error
}

func (f *fakeMailService) ListThreads(ctx context.Context, userID string, q port.ThreadQuery) (domain.Page[domain.Thread], error) {
	f.listCalls++
	f.gotListUserID = userID
	f.gotListQuery = q
	return f.listPage, f.listErr
}
func (f *fakeMailService) GetThread(ctx context.Context, userID, threadID string) (domain.Thread, []domain.Message, error) {
	f.gotGetThreadID = threadID
	return f.getThreadThread, f.getThreadMsgs, f.getThreadErr
}
func (f *fakeMailService) ActOnThread(ctx context.Context, userID, threadID string, action domain.ThreadAction) (domain.Thread, error) {
	f.gotActID, f.gotAction = threadID, action
	return f.actRet, f.actErr
}
func (f *fakeMailService) MarkThreadOpened(ctx context.Context, userID, threadID string) error {
	f.gotMarkID = threadID
	return f.markOpenedErr
}
func (f *fakeMailService) SnoozeThread(ctx context.Context, userID, threadID string, until time.Time) (domain.Thread, error) {
	f.gotSnoozeID, f.gotUntil = threadID, until
	return f.snoozeRet, f.snoozeErr
}
func (f *fakeMailService) SetReminder(ctx context.Context, userID, threadID string, remindAt *time.Time) (domain.Thread, error) {
	f.gotReminderID, f.gotRemindAt = threadID, remindAt
	return f.reminderRet, f.reminderErr
}
func (f *fakeMailService) CreateDraft(ctx context.Context, userID string, in port.DraftInput) (domain.Draft, error) {
	f.gotCreateDraft = in
	return f.createDraftRet, f.createDraftErr
}
func (f *fakeMailService) UpdateDraft(ctx context.Context, userID, draftID string, in port.DraftInput) (domain.Draft, error) {
	f.gotUpdateDraft = in
	return f.updateDraftRet, f.updateDraftErr
}
func (f *fakeMailService) GetDraft(ctx context.Context, userID, draftID string) (domain.Draft, error) {
	return f.getDraftRet, f.getDraftErr
}
func (f *fakeMailService) ListDrafts(ctx context.Context, userID string) ([]domain.Draft, error) {
	return f.listDraftsRet, f.listDraftsErr
}
func (f *fakeMailService) DeleteDraft(ctx context.Context, userID, draftID string) error {
	f.gotDeleteDraft = draftID
	return f.deleteDraftErr
}
func (f *fakeMailService) SendDraft(ctx context.Context, userID, draftID string) (domain.Message, error) {
	return f.sendDraftRet, f.sendDraftErr
}
func (f *fakeMailService) UnsendDraft(ctx context.Context, userID, draftID string) (domain.Draft, error) {
	return f.unsendDraftRet, f.unsendDraftErr
}
func (f *fakeMailService) ListSnippets(ctx context.Context, userID string) ([]domain.Snippet, error) {
	return f.listSnippetsRet, f.listSnippetsErr
}
func (f *fakeMailService) CreateSnippet(ctx context.Context, userID string, in port.SnippetInput) (domain.Snippet, error) {
	return f.createSnippet, f.createSnipErr
}
func (f *fakeMailService) UpdateSnippet(ctx context.Context, userID, snippetID string, in port.SnippetInput) (domain.Snippet, error) {
	return f.updateSnippet, f.updateSnipErr
}
func (f *fakeMailService) DeleteSnippet(ctx context.Context, userID, snippetID string) error {
	return f.deleteSnipErr
}

// --- CalendarService ---------------------------------------------------------

type fakeCalendarService struct {
	listCalsRet []domain.Calendar
	listCalsErr error

	updateCalRet domain.Calendar
	updateCalErr error
	gotUpdateCal port.CalendarPatch

	listEventsRet   []domain.Event
	listEventsErr   error
	listEventsCalls int
	gotEventsFrom   time.Time
	gotEventsTo     time.Time
	gotEventsCalIDs []string

	createEventRet domain.Event
	createEventErr error

	updateEventRet domain.Event
	updateEventErr error

	deleteEventErr error
	gotDeleteEvt   string

	rsvpRet   domain.Event
	rsvpErr   error
	gotRsvp   domain.RsvpStatus
	gotRsvpID string

	availRet   []domain.AvailabilitySlot
	availErr   error
	availCalls int
	gotAvailDur time.Duration
}

func (f *fakeCalendarService) ListCalendars(ctx context.Context, userID string) ([]domain.Calendar, error) {
	return f.listCalsRet, f.listCalsErr
}
func (f *fakeCalendarService) UpdateCalendar(ctx context.Context, userID, calendarID string, patch port.CalendarPatch) (domain.Calendar, error) {
	f.gotUpdateCal = patch
	return f.updateCalRet, f.updateCalErr
}
func (f *fakeCalendarService) ListEvents(ctx context.Context, userID string, from, to time.Time, calendarIDs []string) ([]domain.Event, error) {
	f.listEventsCalls++
	f.gotEventsFrom, f.gotEventsTo, f.gotEventsCalIDs = from, to, calendarIDs
	return f.listEventsRet, f.listEventsErr
}
func (f *fakeCalendarService) CreateEvent(ctx context.Context, userID string, in domain.EventInput) (domain.Event, error) {
	return f.createEventRet, f.createEventErr
}
func (f *fakeCalendarService) UpdateEvent(ctx context.Context, userID, eventID string, patch domain.EventPatch) (domain.Event, error) {
	return f.updateEventRet, f.updateEventErr
}
func (f *fakeCalendarService) DeleteEvent(ctx context.Context, userID, eventID string) error {
	f.gotDeleteEvt = eventID
	return f.deleteEventErr
}
func (f *fakeCalendarService) RSVP(ctx context.Context, userID, eventID string, response domain.RsvpStatus) (domain.Event, error) {
	f.gotRsvpID, f.gotRsvp = eventID, response
	return f.rsvpRet, f.rsvpErr
}
func (f *fakeCalendarService) Availability(ctx context.Context, userID string, from, to time.Time, slotDuration time.Duration) ([]domain.AvailabilitySlot, error) {
	f.availCalls++
	f.gotEventsFrom, f.gotEventsTo, f.gotAvailDur = from, to, slotDuration
	return f.availRet, f.availErr
}

// --- SearchService -----------------------------------------------------------

type fakeSearchService struct {
	ret    port.SearchResult
	err    error
	gotQ   string
	calls  int
}

func (f *fakeSearchService) Search(ctx context.Context, userID, query string) (port.SearchResult, error) {
	f.calls++
	f.gotQ = query
	return f.ret, f.err
}

// --- AIService ---------------------------------------------------------------

type fakeAIService struct {
	ret   domain.AiComposeResponse
	err   error
	gotReq domain.AiComposeRequest
}

func (f *fakeAIService) Compose(ctx context.Context, userID string, req domain.AiComposeRequest) (domain.AiComposeResponse, error) {
	f.gotReq = req
	return f.ret, f.err
}

// --- DeviceService -----------------------------------------------------------

type fakeDeviceService struct {
	registerRet domain.NotificationDevice
	registerErr error
	gotPlatform domain.DevicePlatform
	gotToken    string

	unregisterErr error
	gotUnregID    string
}

func (f *fakeDeviceService) Register(ctx context.Context, userID string, platform domain.DevicePlatform, token string) (domain.NotificationDevice, error) {
	f.gotPlatform, f.gotToken = platform, token
	return f.registerRet, f.registerErr
}
func (f *fakeDeviceService) Unregister(ctx context.Context, userID, deviceID string) error {
	f.gotUnregID = deviceID
	return f.unregisterErr
}

// --- harness -----------------------------------------------------------------

type harness struct {
	t         *testing.T
	deps      Deps
	verifier  *fakeVerifier
	users     *fakeUserService
	billing   *fakeBillingService
	accounts  *fakeAccountService
	mail      *fakeMailService
	calendars *fakeCalendarService
	search    *fakeSearchService
	ai        *fakeAIService
	devices   *fakeDeviceService
}

// newHarness wires every double into Deps with a discard logger and one
// pre-registered valid token (defaultToken => user defaultUserID).
func newHarness(t *testing.T) *harness {
	t.Helper()
	ver := &fakeVerifier{tokens: map[string]port.Identity{
		defaultToken: {Subject: defaultUserID, Email: "owner@example.com", Name: "Owner"},
	}}
	users := &fakeUserService{ensureRet: domain.User{ID: defaultUserID, Email: "owner@example.com"}}
	h := &harness{
		t:         t,
		verifier:  ver,
		users:     users,
		billing:   &fakeBillingService{},
		accounts:  &fakeAccountService{},
		mail:      &fakeMailService{},
		calendars: &fakeCalendarService{},
		search:    &fakeSearchService{},
		ai:        &fakeAIService{},
		devices:   &fakeDeviceService{},
	}
	h.deps = Deps{
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Verifier:  ver,
		Users:     users,
		Billing:   h.billing,
		Accounts:  h.accounts,
		Mail:      h.mail,
		Calendars: h.calendars,
		Search:    h.search,
		AI:        h.ai,
		Devices:   h.devices,
	}
	return h
}

// server returns a bare *server for unit-testing individual middleware
// (requireAuth/recoverPanics) without the full New() stack.
func (h *harness) server() *server { return &server{deps: h.deps} }

// handler returns the full v1 stack (recover + log + CORS + auth + routes).
func (h *harness) handler() http.Handler { return New(h.deps) }

// authed issues a request through the full stack with a valid bearer token.
func (h *harness) authed(method, target string, body io.Reader) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("Authorization", "Bearer "+defaultToken)
	rec := httptest.NewRecorder()
	h.handler().ServeHTTP(rec, req)
	return rec
}

// anon issues a request through the full stack with no Authorization header.
func (h *harness) anon(method, target string, body io.Reader) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(method, target, body)
	rec := httptest.NewRecorder()
	h.handler().ServeHTTP(rec, req)
	return rec
}

// jsonBody marshals v to an io.Reader for request bodies.
func jsonBody(t *testing.T, v any) io.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return bytes.NewReader(b)
}

// decodeErr unmarshals the { "error": { code, message } } envelope.
func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) errorDetail {
	t.Helper()
	var b errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode error envelope: %v (body=%s)", err, rec.Body.String())
	}
	return b.Error
}
```

- [ ] **Step 2: Run** — the harness compiles and existing tests still pass.

```
cd backend && go test ./internal/adapter/in/httpapi/... 2>&1 | tail -20
```

Expected PASS (the harness has no tests of its own yet; `TestHandleInstance` from `instance_test.go` still passes and the file compiles).

- [ ] **Step 3: Commit**

```
cd backend && git add internal/adapter/in/httpapi/harness_test.go
git commit -m "test(httpapi): add configurable driving-port + verifier harness

Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 11: httpapi handler & middleware tests

**Files:**
- Create/Test: `backend/internal/adapter/in/httpapi/middleware_test.go` (requireAuth, CORS, recoverPanics, decodeJSON limits)
- Create/Test: `backend/internal/adapter/in/httpapi/codec_test.go` (statusFor, safeMessage, writeError no-leak)
- Create/Test: `backend/internal/adapter/in/httpapi/mail_handlers_test.go` (list parsing + thread/draft/snippet routes)
- Create/Test: `backend/internal/adapter/in/httpapi/calendar_handlers_test.go` (event time-range + calendar/event/rsvp/availability routes)
- Create/Test: `backend/internal/adapter/in/httpapi/accounts_handlers_test.go` (connect/callback/vip/disconnect routes)
- Create/Test: `backend/internal/adapter/in/httpapi/billing_handlers_test.go` (subscription/checkout/portal/webhook routes)
- Create/Test: `backend/internal/adapter/in/httpapi/misc_handlers_test.go` (search/ai/device routes)

**Interfaces:**
- Consumes (Task 10): `newHarness(t) *harness`, `h.authed/anon/server/handler`, `jsonBody`, `decodeErr`, `defaultToken`, `defaultUserID`, and every `fake*Service`/`fakeVerifier` recording field.
- Consumes (production, package-level, white-box): `statusFor(error) (int, string)`, `safeMessage(string) string`, `(*server).requireAuth`, `corsMiddleware(http.Handler, []string) http.Handler`, `userFrom(*http.Request) domain.User`.
- Produces: `TestRequireAuth`, `TestCORS`, `TestRecoverPanics`, `TestDecodeJSONLimits`, `TestStatusFor`, `TestSafeMessage`, `TestWriteErrorNoLeak`, `TestListThreadsParsing`, and the per-route tests enumerated below.

> All tests are white-box (`package httpapi`). Compare errors only via the response envelope `code`/HTTP status (which the production `statusFor` derives with `errors.Is`). Use fixed RFC 3339 literal timestamps in request URLs/bodies — never `time.Now()`.

- [ ] **Step 1a: Write the auth-middleware table (FULLY CODED)** — `middleware_test.go`

`requireAuth` has two distinct 401 shapes that the table must distinguish:
- missing/malformed bearer → `s.writeError(..., domain.ErrUnauthorized)` → code `"unauthorized"`, message from `safeMessage` = `"Authentication is required or has failed."`
- verifier rejects the token → inline `writeJSON(401, ...)` → code `"unauthorized"`, message `"invalid or expired access token"`

```go
package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

func TestRequireAuth(t *testing.T) {
	const (
		msgReject = "invalid or expired access token"                // verifier-error path
		msgMissing = "Authentication is required or has failed."      // writeError(ErrUnauthorized) path
	)
	tests := []struct {
		name        string
		setAuth     bool
		authHeader  string
		verifierErr error   // forces Verify to fail
		unknownTok  bool    // send a token the verifier map does not know
		ensureErr   error   // EnsureUser failure
		wantStatus  int
		wantCode    string
		wantMessage string
		wantNext    bool // did the wrapped handler run?
		wantVerify  bool // was the verifier consulted?
	}{
		{
			name:       "missing Authorization header",
			setAuth:    false,
			wantStatus: http.StatusUnauthorized,
			wantCode:   "unauthorized",
			wantMessage: msgMissing,
			wantNext:   false,
			wantVerify: false,
		},
		{
			name:       "wrong scheme is malformed",
			setAuth:    true,
			authHeader: "Token abc123",
			wantStatus: http.StatusUnauthorized,
			wantCode:   "unauthorized",
			wantMessage: msgMissing,
			wantNext:   false,
			wantVerify: false,
		},
		{
			name:       "bearer with empty token is malformed",
			setAuth:    true,
			authHeader: "Bearer    ", // only whitespace after the scheme
			wantStatus: http.StatusUnauthorized,
			wantCode:   "unauthorized",
			wantMessage: msgMissing,
			wantNext:   false,
			wantVerify: false,
		},
		{
			name:        "verifier rejects the token",
			setAuth:     true,
			authHeader:  "Bearer some-token",
			verifierErr: errors.New("bad signature"),
			wantStatus:  http.StatusUnauthorized,
			wantCode:    "unauthorized",
			wantMessage: msgReject,
			wantNext:    false,
			wantVerify:  true,
		},
		{
			name:       "unknown token rejected",
			setAuth:    true,
			authHeader: "Bearer nope",
			unknownTok: true,
			wantStatus: http.StatusUnauthorized,
			wantCode:   "unauthorized",
			wantMessage: msgReject,
			wantNext:   false,
			wantVerify: true,
		},
		{
			name:       "EnsureUser failure surfaces as 500",
			setAuth:    true,
			authHeader: "Bearer " + defaultToken,
			ensureErr:  errors.New("db unavailable"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "internal",
			wantMessage: "Internal server error.",
			wantNext:   false,
			wantVerify: true,
		},
		{
			name:       "success runs next with injected user",
			setAuth:    true,
			authHeader: "Bearer " + defaultToken,
			wantStatus: http.StatusOK,
			wantNext:   true,
			wantVerify: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.verifier.err = tt.verifierErr
			h.users.ensureErr = tt.ensureErr
			if tt.unknownTok {
				h.verifier.tokens = map[string]port.Identity{} // known map, token absent
			}

			var ran bool
			var gotUser domain.User
			next := func(w http.ResponseWriter, r *http.Request) {
				ran = true
				gotUser = userFrom(r)
				w.WriteHeader(http.StatusOK)
			}
			handler := h.server().requireAuth(next)

			req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
			if tt.setAuth {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if ran != tt.wantNext {
				t.Fatalf("next ran = %v, want %v", ran, tt.wantNext)
			}
			if (h.verifier.calls > 0) != tt.wantVerify {
				t.Fatalf("verifier consulted = %v, want %v", h.verifier.calls > 0, tt.wantVerify)
			}
			if tt.wantStatus != http.StatusOK {
				got := decodeErr(t, rec)
				if got.Code != tt.wantCode {
					t.Fatalf("code = %q, want %q", got.Code, tt.wantCode)
				}
				if got.Message != tt.wantMessage {
					t.Fatalf("message = %q, want %q", got.Message, tt.wantMessage)
				}
			}
			if tt.wantNext {
				if gotUser.ID != defaultUserID {
					t.Fatalf("injected user id = %q, want %q", gotUser.ID, defaultUserID)
				}
				if h.users.gotIdentity.Subject != defaultUserID {
					t.Fatalf("EnsureUser identity subject = %q, want %q", h.users.gotIdentity.Subject, defaultUserID)
				}
			}
		})
	}
}
```

- [ ] **Step 1b: Write the mail list-parse test (FULLY CODED)** — `mail_handlers_test.go`

```go
package httpapi

import (
	"net/http"
	"testing"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

func TestListThreadsParsing(t *testing.T) {
	tests := []struct {
		name       string
		query      string // raw query string appended to /v1/mail/threads
		wantStatus int
		wantCall   bool // did Mail.ListThreads get invoked?
		check      func(t *testing.T, q port.ThreadQuery)
	}{
		{
			name:       "all filters parse",
			query:      "?labelId=lbl1&q=hello&cursor=cur1&split=important&view=starred&limit=25",
			wantStatus: http.StatusOK,
			wantCall:   true,
			check: func(t *testing.T, q port.ThreadQuery) {
				if q.LabelID != "lbl1" || q.Query != "hello" || q.Cursor != "cur1" {
					t.Fatalf("string filters = %+v", q)
				}
				if q.Split != domain.SplitImportant {
					t.Fatalf("split = %q, want %q", q.Split, domain.SplitImportant)
				}
				if q.View != domain.ThreadViewStarred {
					t.Fatalf("view = %q, want %q", q.View, domain.ThreadViewStarred)
				}
				if q.Limit != 25 {
					t.Fatalf("limit = %d, want 25", q.Limit)
				}
			},
		},
		{
			name:       "no optional params leaves zero values",
			query:      "",
			wantStatus: http.StatusOK,
			wantCall:   true,
			check: func(t *testing.T, q port.ThreadQuery) {
				if q.Split != "" || q.View != "" || q.Limit != 0 || q.LabelID != "" {
					t.Fatalf("expected zero-valued query, got %+v", q)
				}
			},
		},
		{name: "invalid split rejected", query: "?split=bogus", wantStatus: http.StatusBadRequest, wantCall: false},
		{name: "invalid view rejected", query: "?view=drafts", wantStatus: http.StatusBadRequest, wantCall: false},
		{name: "non-numeric limit rejected", query: "?limit=abc", wantStatus: http.StatusBadRequest, wantCall: false},
		{name: "zero limit rejected", query: "?limit=0", wantStatus: http.StatusBadRequest, wantCall: false},
		{name: "negative limit rejected", query: "?limit=-5", wantStatus: http.StatusBadRequest, wantCall: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			rec := h.authed(http.MethodGet, "/v1/mail/threads"+tt.query, nil)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if (h.mail.listCalls > 0) != tt.wantCall {
				t.Fatalf("ListThreads called = %v, want %v", h.mail.listCalls > 0, tt.wantCall)
			}
			if tt.wantStatus == http.StatusBadRequest {
				if got := decodeErr(t, rec); got.Code != "validation_failed" {
					t.Fatalf("code = %q, want validation_failed", got.Code)
				}
			}
			if tt.wantCall {
				if h.mail.gotListUserID != defaultUserID {
					t.Fatalf("userID = %q, want %q", h.mail.gotListUserID, defaultUserID)
				}
				if tt.check != nil {
					tt.check(t, h.mail.gotListQuery)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run** — after each file, run the package tests.

```
cd backend && go test ./internal/adapter/in/httpapi/... 2>&1 | tail -30
```

Expected PASS.

- [ ] **Step 3: Commit** (one commit per file, or grouped).

```
cd backend && git add internal/adapter/in/httpapi/*_test.go
git commit -m "test(httpapi): cover auth, CORS, codec, and per-route handlers

Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

#### Required cases — middleware (`middleware_test.go`)

Each is mandatory. Drive `corsMiddleware`/`recoverPanics` directly with a probe handler; drive `decodeJSON` limits through a real decoding route.

**`TestCORS`** — call `corsMiddleware(probe, []string{"https://app.example.com"})` where `probe` sets `w.WriteHeader(200)` and records that it ran:
- Origin `http://localhost:5173` (GET) → `Access-Control-Allow-Origin == "http://localhost:5173"`, `Access-Control-Allow-Credentials == "true"`, `Vary` contains `Origin`, probe ran, 200. (local-dev origin allowed by `isLocalDevOrigin`).
- Origin `http://127.0.0.1:3000` (GET) → reflected, 200.
- Origin `wails://wails.localhost` (GET) → reflected (allowed by `isWailsOrigin`), 200.
- Origin `https://app.example.com` (GET) → reflected (explicit allowlist match), 200.
- Origin `https://app.example.com/` (trailing slash, GET) → reflected (allowlist trims trailing `/`), 200.
- Origin `https://evil.example.com` (GET) → **no** `Access-Control-Allow-Origin` header, probe ran (request still served), 200.
- No `Origin` header (GET) → no CORS headers, probe ran, 200.
- Preflight `OPTIONS` with `Origin: http://localhost:5173` **and** `Access-Control-Request-Method: POST` → status 204, `Access-Control-Allow-Origin` reflected, `Access-Control-Allow-Methods` present, probe **not** run.
- `OPTIONS` with allowed Origin but **no** `Access-Control-Request-Method` → falls through to `next` (probe ran), not 204.

**`TestRecoverPanics`** — call `h.server().recoverPanics(panicky)`:
- Handler panics with `"boom"` → status 500, envelope `code == "internal"`, `message == "internal server error"`, response body written (connection not aborted, `rec.Body` non-empty).
- Handler that writes 200 and does **not** panic → passes through unchanged (200), proving recovery only fires on panic.
- (Optional, documents intent) Handler panics with `http.ErrAbortHandler` → `recoverPanics` re-panics; assert with `defer func(){ recover() }()` that the panic propagates (it is intentionally **not** converted to 500).

**`TestDecodeJSONLimits`** — drive through `POST /v1/mail/drafts` (calls `decodeJSON` into `port.DraftInput`) with a valid bearer token:
- Malformed body `"{"` → status 400, `code == "validation_failed"`, and `Mail.CreateDraft` **not** called (`h.mail.gotCreateDraft` stays zero / add a `createDraftCalls` counter if asserting invocation).
- Empty body `""` → status 400 (`json.Decode` of empty stream errors), `code == "validation_failed"`.
- Oversized body > `maxBodyBytes` (10 MB): build a JSON string whose length exceeds `10<<20` (e.g. `` `{"subject":"` + strings.Repeat("a", 11<<20) + `"}` ``) → status 400 (`http.MaxBytesReader` trips), `code == "validation_failed"`, `CreateDraft` not called.
- Valid body `port.DraftInput{AccountID:"acc1", Subject:"hi"}` (via `jsonBody`) → status 200; assert `h.mail.gotCreateDraft.AccountID == "acc1"` and `Subject == "hi"`.

#### Required cases — codec (`codec_test.go`)

**`TestStatusFor`** — table calling `statusFor(err)` directly; wrap each sentinel with `fmt.Errorf("x: %w", sentinel)` to prove `errors.Is` unwrapping:
- `domain.ErrValidation` → `(400, "validation_failed")`
- `domain.ErrUnauthorized` → `(401, "unauthorized")`
- `domain.ErrPaymentRequired` → `(402, "payment_required")`
- `domain.ErrNotFound` → `(404, "not_found")`
- `domain.ErrConflict` → `(409, "conflict")`
- `domain.ErrSelfHosted` → `(501, "self_hosted")`
- `errors.New("anything else")` → `(500, "internal")`

**`TestSafeMessage`** — assert `safeMessage(code)` returns the stable client string for each code (`validation_failed`→`"The request was invalid."`, `unauthorized`→`"Authentication is required or has failed."`, `payment_required`→`"An active subscription is required."`, `not_found`→`"The requested resource was not found."`, `conflict`→`"The request conflicts with the current state of the resource."`, `self_hosted`→`"Billing is disabled on self-hosted instances."`, unknown/`"internal"`→`"Internal server error."`).

**`TestStatusForViaHandler`** — the required "handler returning that error" path. For each sentinel above, set `h.billing.subErr = fmt.Errorf("wrapped: %w", sentinel)` and issue `h.authed(GET, "/v1/billing/subscription", nil)`; assert the HTTP status equals the mapped code and `decodeErr(...).Code` equals the machine code. For the `internal` row use `h.billing.subErr = errors.New("boom")` → 500.

**`TestWriteErrorNoLeak`** — set `h.billing.subErr = fmt.Errorf("db dial tcp 10.0.0.5: password=hunter2")`; `GET /v1/billing/subscription` → status 500, body `message == "Internal server error."`, and assert `!strings.Contains(rec.Body.String(), "hunter2")` and `!strings.Contains(rec.Body.String(), "10.0.0.5")` (internal detail never echoed).

#### Required cases — mail routes (`mail_handlers_test.go`, in addition to `TestListThreadsParsing`)

- `handleGetThread` — `GET /v1/mail/threads/th1` success: seed `h.mail.getThreadThread`/`getThreadMsgs`, assert 200, `h.mail.gotGetThreadID == "th1"`, and body has top-level keys `"thread"` and `"messages"`.
- `handleGetThread` — other user's thread: `h.mail.getThreadErr = domain.ErrNotFound` → 404, code `not_found` (ownership invariant).
- `handleThreadAction` — `POST /v1/mail/threads/th1/actions` body `{"action":"archive"}` → 200, `h.mail.gotAction == domain.ThreadActionArchive`, `gotActID == "th1"`.
- `handleThreadAction` — body `{"action":"frobnicate"}` → 400 `validation_failed` (`ParseThreadAction`), `ActOnThread` not called.
- `handleThreadAction` — malformed JSON body → 400 `validation_failed`.
- `handleMarkThreadOpened` — `POST /v1/mail/threads/th1/open` success → 204 (`w.WriteHeader(StatusNoContent)`, empty body), `gotMarkID == "th1"`.
- `handleSnoozeThread` — body `{"until":"2030-01-01T00:00:00Z"}` → 200, `!h.mail.gotUntil.IsZero()`.
- `handleSnoozeThread` — body `{}` (zero/missing `until`) → 400 `validation_failed` ("until is required"), `SnoozeThread` not called.
- `handleThreadReminder` — body `{"remindAt":"2030-01-01T00:00:00Z"}` → 200, `h.mail.gotRemindAt != nil`; body `{"remindAt":null}` → 200, `h.mail.gotRemindAt == nil` (clears reminder).
- `handleListDrafts` — `GET /v1/mail/drafts` → 200; seed `h.mail.listDraftsRet`, assert body is the JSON array of drafts.
- `handleCreateDraft` — valid `port.DraftInput` → 200, `gotCreateDraft.AccountID` forwarded (also covered by `TestDecodeJSONLimits`).
- `handleUpdateDraft` — `PUT /v1/mail/drafts/d1` valid body → 200; `h.mail.updateDraftErr = domain.ErrNotFound` → 404.
- `handleDeleteDraft` — `DELETE /v1/mail/drafts/d1` success → 204, `gotDeleteDraft == "d1"`; `deleteDraftErr = domain.ErrNotFound` → 404.
- `handleSendDraft` — `POST /v1/mail/drafts/d1/send` → 200 body is the provisional `domain.Message`.
- `handleUnsendDraft` — `POST /v1/mail/drafts/d1/unsend` with `h.mail.unsendDraftErr = domain.ErrConflict` → 409 `conflict` (grace window elapsed).
- `handleListSnippets` / `handleCreateSnippet` / `handleUpdateSnippet` / `handleDeleteSnippet` — happy path status (200 for the first three, 204 for delete); malformed JSON on create/update → 400; `deleteSnipErr = domain.ErrNotFound` → 404.
- Paywall passthrough: `h.mail.listErr = domain.ErrPaymentRequired` on `GET /v1/mail/threads` → 402 `payment_required` (handler forwards the service sentinel unchanged).

#### Required cases — calendar routes (`calendar_handlers_test.go`)

- `handleListEvents` (time-range parse, FULLY analogous to the mail test) — `GET /v1/events?from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z&calendarIds=c1,c2&calendarIds=c3` → 200, `h.calendars.gotEventsCalIDs == ["c1","c2","c3"]` (comma-split + repeated params, blanks dropped), `gotEventsFrom`/`gotEventsTo` equal the parsed RFC 3339 instants (compare with `.Equal`).
- `handleListEvents` — `?from=not-a-time&to=2026-01-02T00:00:00Z` → 400 `validation_failed`, `ListEvents` not called (`listEventsCalls == 0`).
- `handleListEvents` — valid `from` but `?to=` empty/invalid → 400 `validation_failed`.
- `handleListEvents` — no `calendarIds` param → 200, `gotEventsCalIDs == nil`.
- `handleAvailability` — `GET /v1/availability?from=...&to=...&duration=30` → 200, `h.calendars.gotAvailDur == 30*time.Minute`.
- `handleAvailability` — `duration` missing / `duration=0` / `duration=-5` / `duration=abc` → 400 `validation_failed` each (`Atoi` err or `<= 0`), `Availability` not called (`availCalls == 0`).
- `handleAvailability` — invalid `from`/`to` → 400 `validation_failed`.
- `handleListCalendars` — `GET /v1/calendars` → 200, body is the seeded `[]domain.Calendar`.
- `handleUpdateCalendar` — `PATCH /v1/calendars/cal1` body `{"isVisible":false,"color":"#fff"}` → 200, `h.calendars.gotUpdateCal.IsVisible` non-nil `== false`, `*gotUpdateCal.Color == "#fff"`; `updateCalErr = domain.ErrNotFound` → 404.
- `handleCreateEvent` — `POST /v1/events` valid `domain.EventInput` → 200; malformed JSON → 400 `validation_failed`.
- `handleUpdateEvent` — `PATCH /v1/events/ev1` valid `domain.EventPatch` → 200; `updateEventErr = domain.ErrNotFound` → 404.
- `handleDeleteEvent` — `DELETE /v1/events/ev1` → 204, `gotDeleteEvt == "ev1"`; `deleteEventErr = domain.ErrNotFound` → 404.
- `handleRsvp` — `POST /v1/events/ev1/rsvp` body `{"response":"accepted"}` → 200, `h.calendars.gotRsvp == domain.RsvpAccepted`, `gotRsvpID == "ev1"`.
- `handleRsvp` — body `{"response":"maybe"}` → 400 `validation_failed` (`ParseRsvpStatus`), `RSVP` not called; malformed JSON → 400.

#### Required cases — accounts routes (`accounts_handlers_test.go`)

- `handleListAccounts` — `GET /v1/accounts` → 200, body is seeded `[]domain.ConnectedAccount`; `listErr = domain.ErrPaymentRequired` → 402 (if service gates; otherwise 200 — assert against whatever `h.accounts.listErr` is set to, one case per).
- `handleConnectAccount` — `POST /v1/accounts/connect/google` body `{"redirectUrl":"myapp://cb"}` with `h.accounts.beginURL = "https://accounts.google.com/o/oauth2/auth?..."` → 200, response JSON `{"url": ...}` equals `beginURL`, `h.accounts.gotProvider == domain.ProviderGoogle`, `gotRedirectURL == "myapp://cb"`, `gotBeginBaseURL == "http://example.com"` (httptest default `r.Host`, via `requestBaseURL`).
- `handleConnectAccount` — `POST /v1/accounts/connect/yahoo` → 400 `validation_failed` (`ParseProvider`), `BeginConnect` not called.
- `handleConnectAccount` — malformed JSON body on a valid provider → 400 `validation_failed`.
- `handleConnectAccount` — `h.accounts.beginErr = domain.ErrNotFound` → 404 (service sentinel forwarded).
- `handleSetVipSenders` — `PUT /v1/accounts/acc1/vip-senders` body `{"vipSenders":["a@x.com","b@y.com"]}` → 200, `h.accounts.gotVIPID == "acc1"`, `gotVIP == ["a@x.com","b@y.com"]`.
- `handleSetVipSenders` — other user's account: `h.accounts.vipErr = domain.ErrNotFound` → 404 (ownership invariant); malformed JSON → 400.
- `handleDisconnectAccount` — `DELETE /v1/accounts/acc1` → 204, `gotDisconnectID == "acc1"`; `disconnectErr = domain.ErrNotFound` → 404.
- `handleAccountCallback` (unauthenticated — use `h.anon`, no bearer) — `GET /v1/accounts/callback/google?state=st&code=cd` with `h.accounts.completeRedirect = "myapp://done"`, `completeErr = nil` → status 302, `Location` header contains `myapp://done` and `status=connected`; assert `h.accounts.gotState == "st"`, `gotCode == "cd"`.
- `handleAccountCallback` — `completeErr = domain.ErrValidation`, `completeRedirect = "myapp://done"` → 302 to `myapp://done?status=error` (known client redirect, non-500).
- `handleAccountCallback` — `completeErr = domain.ErrValidation`, `completeRedirect = ""` (invalid state, no known redirect) → renders the static HTML page: status 400, `Content-Type` `text/html; charset=utf-8`, body contains `"Connection failed"`.
- `handleAccountCallback` — unknown provider in path (`/v1/accounts/callback/yahoo`) → status 400, HTML page `"Unknown provider."`, `CompleteConnect` not called.
- `handleAccountCallback` — `completeErr` mapping to 500 (`errors.New("boom")`) with `completeRedirect = ""` → status 500, HTML body contains the generic "Something went wrong while connecting the account." string (not the raw error).

#### Required cases — billing routes (`billing_handlers_test.go`)

- `handleMe` — `GET /v1/me` → 200, body decodes to `domain.User` with `id == defaultUserID` (proves `requireAuth` upserted + `userFrom` injected; `EnsureUser` ran).
- `handleGetSubscription` — `GET /v1/billing/subscription` → 200, body is seeded `domain.Subscription` (e.g. `Status: domain.SubscriptionActive`). (Also the vehicle for `TestStatusForViaHandler` / `TestWriteErrorNoLeak`.)
- `handleCreateCheckout` — `POST /v1/billing/checkout` body `{"successUrl":"https://a","cancelUrl":"https://b"}`, `h.billing.checkoutURL = "https://checkout.stripe/x"` → 200, response `{"url": ...}` equals it, `gotCheckoutSuccessURL == "https://a"`, `gotCheckoutCancelURL == "https://b"`.
- `handleCreateCheckout` — `h.billing.checkoutErr = domain.ErrSelfHosted` → 501 `self_hosted`; malformed JSON → 400.
- `handleCreatePortal` — `POST /v1/billing/portal` body `{"returnUrl":"https://back"}` → 200 `{"url": portalURL}`, `gotPortalReturn == "https://back"`; `portalErr = domain.ErrSelfHosted` → 501.
- `handleStripeWebhook` (unauthenticated — use `h.anon`) — `POST /v1/webhooks/stripe` with header `Stripe-Signature: sig` and raw body `[]byte("{...}")`, `h.billing.webhookErr = nil` → 200 body `{"received":true}`, `h.billing.gotWebhookSig == "sig"`, `gotWebhookPayload` equals the sent bytes.
- `handleStripeWebhook` — `h.billing.webhookErr = domain.ErrValidation` (bad signature) → 400 `validation_failed`.
- `handleStripeWebhook` — oversized body > `1<<20` (1 MB, the webhook's own `MaxBytesReader` limit) → 400 `validation_failed` (read fails before `HandleWebhook`; assert `h.billing.webhookCalls == 0`).

#### Required cases — misc routes (`misc_handlers_test.go`)

- `handleSearch` — `GET /v1/search?q=quarterly%20report` → 200, `h.search.gotQ == "quarterly report"`, body is the seeded `port.SearchResult`.
- `handleSearch` — "q required": handler forwards the raw `q` (validation is service-side), so set `h.search.err = domain.ErrValidation` and request `GET /v1/search` (empty `q`) → 400 `validation_failed`; assert `h.search.gotQ == ""` (empty string forwarded). *(realBehaviorNotes: the handler does not itself reject empty `q`.)*
- `handleAiCompose` — `POST /v1/ai/compose` body `{"action":"compose","prompt":"draft a reply"}` → 200, `h.ai.gotReq.Action == domain.AiCompose`, `gotReq.Prompt == "draft a reply"`, body is seeded `domain.AiComposeResponse`; malformed JSON → 400.
- `handleRegisterDevice` — `POST /v1/devices` body `{"platform":"ios","token":"abc"}` → 200, `h.devices.gotPlatform == domain.PlatformIOS`, `gotToken == "abc"`, body is seeded `domain.NotificationDevice`.
- `handleRegisterDevice` — "platform enum": handler casts the raw string to `domain.DevicePlatform` and forwards it (validation is service-side). Set `h.devices.registerErr = domain.ErrValidation` and send `{"platform":"blackberry","token":"t"}` → 400 `validation_failed`; assert `h.devices.gotPlatform == domain.DevicePlatform("blackberry")` (raw value forwarded). *(realBehaviorNotes: the handler does not call `domain.ParseDevicePlatform`.)*
- `handleRegisterDevice` — malformed JSON → 400 `validation_failed`.
- `handleUnregisterDevice` — `DELETE /v1/devices/dev1` → 204, `h.devices.gotUnregID == "dev1"`; `unregisterErr = domain.ErrNotFound` → 404.

#### Cross-cutting sanity (any file)

- Public routes reachable without a token via `h.anon`: `GET /healthz` → 200 text `ok`; `GET /v1/instance` → 200 (already covered by `instance_test.go`); `POST /v1/webhooks/stripe` and `GET /v1/accounts/callback/{provider}` do **not** require a bearer token.
- Authed routes with no token via `h.anon` (e.g. `GET /v1/me`, `GET /v1/mail/threads`) → 401 `unauthorized`, and the corresponding fake service method is **not** invoked (e.g. `h.mail.listCalls == 0`), proving `requireAuth` short-circuits before the handler.

---

### Task 12: Crypto at rest + JWKS asymmetric verification (`postgres`, `authjwt`)

Two white-box test files. Both are pure-crypto / local-httptest and need **no production changes**.

**Files:**
- `backend/internal/adapter/out/postgres/crypto_test.go` (new, package `postgres`)
- `backend/internal/adapter/out/authjwt/verifier_test.go` (extend existing, package `authjwt`)

**Interfaces / real symbols under test:**
- `postgres`: `Store{db *sql.DB; aead cipher.AEAD}`; `(*Store).SetTokenEncryptionKey(key []byte) error`; unexported `(*Store).sealToken(plain string) ([]byte, error)`; `(*Store).openToken(blob []byte) (string, error)`; sentinel `errNoTokenKey`. Ciphertext layout is `nonce || gcmSeal`, `NonceSize()==12`.
- `authjwt`: `NewVerifier(jwksURL, issuer string, hc *http.Client) *Verifier`; `(*Verifier).Verify(ctx, token) (port.Identity, error)`. Caching constants: `jwksTTL = 15m`, `jwksRefetchMin = 30s`. A cached+fresh kid is served without any refetch; empty cache forces a fetch. JWK shapes accepted: `kty:"RSA"` (`n`,`e` base64url), `kty:"EC"`,`crv:"P-256"` (`x`,`y` base64url), `kty:"OKP"`,`crv:"Ed25519"` (`x`).

No seams required — `SetTokenEncryptionKey` installs the AEAD on a bare `&Store{}` (only `aead` is touched by seal/open), and `NewVerifier` already takes an injectable `jwksURL` + `*http.Client` pointed at an `httptest` server.

---

#### Task 12a — `postgres/crypto_test.go`

- [ ] Create `backend/internal/adapter/out/postgres/crypto_test.go`:

```go
package postgres

import (
	"bytes"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
)

// newSealedStore builds a bare Store with only the AEAD installed — seal/open
// touch nothing else on Store, so a nil db is fine.
func newSealedStore(t *testing.T, key []byte) *Store {
	t.Helper()
	s := &Store{}
	if err := s.SetTokenEncryptionKey(key); err != nil {
		t.Fatalf("SetTokenEncryptionKey: %v", err)
	}
	return s
}

func randKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return k
}

func TestSetTokenEncryptionKeyLength(t *testing.T) {
	s := &Store{}
	for _, n := range []int{0, 16, 31, 33, 64} {
		if err := s.SetTokenEncryptionKey(make([]byte, n)); err == nil {
			t.Errorf("key length %d accepted, want error", n)
		}
	}
	if err := s.SetTokenEncryptionKey(make([]byte, 32)); err != nil {
		t.Errorf("32-byte key rejected: %v", err)
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	s := newSealedStore(t, randKey(t))
	for _, plain := range []string{"", "ya29.short-token", strings.Repeat("secret-", 1000)} {
		t.Run(plain[:min(len(plain), 12)], func(t *testing.T) {
			blob, err := s.sealToken(plain)
			if err != nil {
				t.Fatalf("sealToken: %v", err)
			}
			if plain == "" {
				if blob != nil {
					t.Fatalf("empty plaintext should seal to nil, got %v", blob)
				}
			} else if bytes.Equal(blob, []byte(plain)) {
				t.Fatal("ciphertext equals plaintext")
			}
			got, err := s.openToken(blob)
			if err != nil {
				t.Fatalf("openToken: %v", err)
			}
			if got != plain {
				t.Fatalf("round trip = %q, want %q", got, plain)
			}
		})
	}
}

// TestSealTokenNonceIsRandom: two seals of the same plaintext must differ
// (random 12-byte GCM nonce prefix).
func TestSealTokenNonceIsRandom(t *testing.T) {
	s := newSealedStore(t, randKey(t))
	a, err := s.sealToken("same-token")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.sealToken("same-token")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions of same plaintext identical; nonce not random")
	}
}

// TestOpenTokenWrongKeyFails: a ciphertext sealed under k1 must not decrypt
// under a different key (GCM auth tag mismatch).
func TestOpenTokenWrongKeyFails(t *testing.T) {
	blob, err := newSealedStore(t, randKey(t)).sealToken("provider-refresh-token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newSealedStore(t, randKey(t)).openToken(blob); err == nil {
		t.Fatal("decrypt under wrong key succeeded, want auth failure")
	}
}

func TestSealOpenNoKey(t *testing.T) {
	s := &Store{} // no AEAD installed
	if _, err := s.sealToken("x"); !errors.Is(err, errNoTokenKey) {
		t.Fatalf("sealToken err = %v, want errNoTokenKey", err)
	}
	if _, err := s.openToken([]byte("nonempty")); !errors.Is(err, errNoTokenKey) {
		t.Fatalf("openToken err = %v, want errNoTokenKey", err)
	}
	// Empty inputs are the NULL mapping and never touch the AEAD.
	if b, err := s.sealToken(""); err != nil || b != nil {
		t.Fatalf("sealToken(\"\") = %v, %v", b, err)
	}
	if v, err := s.openToken(nil); err != nil || v != "" {
		t.Fatalf("openToken(nil) = %q, %v", v, err)
	}
}

func TestOpenTokenTooShort(t *testing.T) {
	s := newSealedStore(t, randKey(t))
	if _, err := s.openToken([]byte{1, 2, 3}); err == nil {
		t.Fatal("ciphertext shorter than nonce accepted")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
```

- [ ] Run:
```
cd backend && go test ./internal/adapter/out/postgres/ -run 'TestSealOpen|TestSealToken|TestOpenToken|TestSetTokenEncryptionKey' -v
```
Expected PASS: `TestSetTokenEncryptionKeyLength`, `TestSealOpenRoundTrip` (3 subtests), `TestSealTokenNonceIsRandom`, `TestOpenTokenWrongKeyFails`, `TestSealOpenNoKey`, `TestOpenTokenTooShort`.

> Note: if the `postgres` package already declares a test-local `min`, drop the helper here and use Go 1.21 builtin `min`. Confirm with `cd backend && go doc ./internal/adapter/out/postgres 2>/dev/null` or just build — a redeclaration error is the only failure mode and is a one-line delete.

**Required cases (all coded above):** wrong key-length rejected + 32 accepted; round-trip for empty/short/large; empty→nil (NULL) mapping; ciphertext≠plaintext; random-nonce divergence; wrong-key auth failure; no-key sentinel (`errors.Is(errNoTokenKey)`) for both seal and open; too-short ciphertext.

- [ ] Commit: `git add backend/internal/adapter/out/postgres/crypto_test.go && git commit -m "test(postgres): AES-256-GCM token vault round-trip + nonce/key negatives"`

---

#### Task 12b — extend `authjwt/verifier_test.go`

Append the helpers + tests below to the existing file and **merge the new imports** into its import block. New imports needed: `crypto`, `crypto/ecdsa`, `crypto/elliptic`, `crypto/rand`, `crypto/rsa`, `crypto/sha256`, `math/big`, `sync/atomic`. (`context`, `crypto/ed25519`, `encoding/base64`, `encoding/json`, `net/http`, `net/http/httptest`, `strings`, `testing`, `time` are already imported.)

- [ ] Add the JWKS + signing helpers and a request-counting server:

```go
// countingJWKS serves a fixed JWKS body and counts every GET, so tests can
// assert the verifier caches keys across Verify calls.
type countingJWKS struct {
	*httptest.Server
	hits int32
}

func (c *countingJWKS) count() int { return int(atomic.LoadInt32(&c.hits)) }

func newCountingJWKS(t *testing.T, keys ...map[string]any) *countingJWKS {
	t.Helper()
	body := map[string]any{"keys": keys}
	c := &countingJWKS{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&c.hits, 1)
		_ = json.NewEncoder(w).Encode(body)
	}))
	return c
}

func rsaJWK(kid string, pub *rsa.PublicKey) map[string]any {
	return map[string]any{
		"kty": "RSA", "kid": kid,
		"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

func ecJWK(kid string, pub *ecdsa.PublicKey) map[string]any {
	x, y := make([]byte, 32), make([]byte, 32)
	pub.X.FillBytes(x)
	pub.Y.FillBytes(y)
	return map[string]any{
		"kty": "EC", "crv": "P-256", "kid": kid,
		"x": base64.RawURLEncoding.EncodeToString(x),
		"y": base64.RawURLEncoding.EncodeToString(y),
	}
}

func encSeg(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func signRS256(t *testing.T, priv *rsa.PrivateKey, header, claims map[string]any) string {
	t.Helper()
	input := encSeg(t, header) + "." + encSeg(t, claims)
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("rsa sign: %v", err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func signES256(t *testing.T, priv *ecdsa.PrivateKey, header, claims map[string]any) string {
	t.Helper()
	input := encSeg(t, header) + "." + encSeg(t, claims)
	sum := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, priv, sum[:])
	if err != nil {
		t.Fatalf("ecdsa sign: %v", err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}
```

> Prerequisite check: the existing `verifier_test.go` already defines `signEdDSA`. The new helper is named `signES256` — confirm it does not collide with any existing test symbol (it does not, in the file read). If a future edit adds one, rename to `signES256Test`.

- [ ] RS256 happy-path **and** caching (fully coded):

```go
func TestVerifyRS256AndCaches(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const kid, issuer = "rsa-kid", "https://app.calendium.com"

	srv := newCountingJWKS(t, rsaJWK(kid, &priv.PublicKey))
	defer srv.Close()

	v := NewVerifier(srv.URL, issuer, srv.Client())

	mint := func() string {
		return signRS256(t, priv,
			map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid},
			map[string]any{"sub": "user_rsa", "email": "ada@calendium.com",
				"iss": issuer, "exp": time.Now().Add(15 * time.Minute).Unix()},
		)
	}

	id, err := v.Verify(context.Background(), mint())
	if err != nil {
		t.Fatalf("first Verify: %v", err)
	}
	if id.Subject != "user_rsa" || id.Email != "ada@calendium.com" {
		t.Fatalf("identity = %+v", id)
	}

	// Second Verify with a fresh token but same kid must hit the cache.
	if _, err := v.Verify(context.Background(), mint()); err != nil {
		t.Fatalf("second Verify: %v", err)
	}
	if got := srv.count(); got != 1 {
		t.Fatalf("JWKS fetched %d times, want exactly 1 (second Verify must not refetch)", got)
	}
}
```

- [ ] ES256 happy-path (fully coded):

```go
func TestVerifyES256(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const kid, issuer = "ec-kid", "https://app.calendium.com"
	srv := newCountingJWKS(t, ecJWK(kid, &priv.PublicKey))
	defer srv.Close()

	v := NewVerifier(srv.URL, issuer, srv.Client())
	token := signES256(t, priv,
		map[string]any{"alg": "ES256", "kid": kid},
		map[string]any{"sub": "user_ec", "iss": issuer, "exp": time.Now().Add(time.Minute).Unix()})

	id, err := v.Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if id.Subject != "user_ec" {
		t.Errorf("Subject = %q, want user_ec", id.Subject)
	}
}
```

- [ ] kid selection across a two-key JWKS (fully coded):

```go
func TestVerifyKidSelection(t *testing.T) {
	rsaPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const issuer = "https://app.calendium.com"
	srv := newCountingJWKS(t, rsaJWK("k-rsa", &rsaPriv.PublicKey), ecJWK("k-ec", &ecPriv.PublicKey))
	defer srv.Close()

	v := NewVerifier(srv.URL, issuer, srv.Client())
	exp := time.Now().Add(time.Minute).Unix()

	rs := signRS256(t, rsaPriv, map[string]any{"alg": "RS256", "kid": "k-rsa"},
		map[string]any{"sub": "u1", "iss": issuer, "exp": exp})
	es := signES256(t, ecPriv, map[string]any{"alg": "ES256", "kid": "k-ec"},
		map[string]any{"sub": "u2", "iss": issuer, "exp": exp})

	if id, err := v.Verify(context.Background(), rs); err != nil || id.Subject != "u1" {
		t.Fatalf("k-rsa/RS256: id=%+v err=%v", id, err)
	}
	if id, err := v.Verify(context.Background(), es); err != nil || id.Subject != "u2" {
		t.Fatalf("k-ec/ES256: id=%+v err=%v", id, err)
	}
	if got := srv.count(); got != 1 {
		t.Fatalf("JWKS fetched %d times, want 1 (both kids in one key set)", got)
	}
}
```

- [ ] Rejections over the RSA JWKS path — expiry / issuer / unknown-kid (table-driven, fully coded):

```go
func TestVerifyRS256Rejects(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const kid, issuer = "rsa-kid", "https://app.calendium.com"
	srv := newCountingJWKS(t, rsaJWK(kid, &priv.PublicKey))
	defer srv.Close()
	v := NewVerifier(srv.URL, issuer, srv.Client())
	now := time.Now()

	tests := []struct {
		name         string
		header       map[string]any
		claims       map[string]any
		wantContains string
	}{
		{"expired", map[string]any{"alg": "RS256", "kid": kid},
			map[string]any{"sub": "u", "iss": issuer, "exp": now.Add(-10 * time.Minute).Unix()}, "expired"},
		{"wrong issuer", map[string]any{"alg": "RS256", "kid": kid},
			map[string]any{"sub": "u", "iss": "https://evil.example", "exp": now.Add(time.Minute).Unix()}, "issuer"},
		{"unknown kid", map[string]any{"alg": "RS256", "kid": "no-such-kid"},
			map[string]any{"sub": "u", "iss": issuer, "exp": now.Add(time.Minute).Unix()}, "no JWK with kid"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			token := signRS256(t, priv, tc.header, tc.claims)
			_, err := v.Verify(context.Background(), token)
			if err == nil || !strings.Contains(err.Error(), tc.wantContains) {
				t.Fatalf("err = %v, want contains %q", err, tc.wantContains)
			}
		})
	}
}
```

> Caching note that makes the assertions deterministic: `jwksRefetchMin = 30s`, so within a single fast test the verifier fetches the JWKS **once** and then serves cached keys; the `unknown kid` subtest therefore returns `no JWK with kid "no-such-kid"` from cache without a refetch, regardless of subtest order. All subtests run well inside the 30s throttle and the 15m TTL. Error strings are asserted with `strings.Contains` because these are non-sentinel `fmt.Errorf` messages, not `domain.Err*` values.

- [ ] Run:
```
cd backend && go test ./internal/adapter/out/authjwt/ -run 'TestVerify' -v
```
Expected PASS: existing `TestVerifyEd25519`, `TestVerifyRejectsWrongKey`, `TestVerifyRejectsExpired`, `TestVerifyRejectsWrongIssuer` **plus** new `TestVerifyRS256AndCaches`, `TestVerifyES256`, `TestVerifyKidSelection`, `TestVerifyRS256Rejects` (3 subtests).

**Required cases:** RS256 verify via JWKS; ES256 verify via JWKS; kid selection across a multi-key set; JWKS fetch caching (request-counter == 1 on second Verify); issuer mismatch → error; expired token → error; unknown kid → error. (Tampered-signature rejection for the asymmetric path is already covered structurally by the existing EdDSA `TestVerifyRejectsWrongKey`; add an RS256 analogue only if desired — sign with a second RSA key not in the JWKS and expect `invalid RS256 signature`.)

- [ ] Commit: `git add backend/internal/adapter/out/authjwt/verifier_test.go && git commit -m "test(authjwt): RS256/ES256 JWKS verification, kid selection, fetch caching"`

---

### Task 13: Push, OpenRouter, Stripe payments network adapters

All three adapters accept an injectable `*http.Client` (`NewDispatcher(cfg, hc)`, `NewClient(..., hc)`), but their upstream hosts are hardcoded **consts** (`push.apnsHost`, the FCM `fcm.googleapis.com` URL, `openrouter.completionsURL`, `stripeapi.apiBase`). Rather than change production to `var`, the tests install a **transport-rewrite seam on the injected client**: a `RoundTripper` that redirects any outbound request to a local `httptest` server while preserving method / path / query / headers / body. This keeps production code unchanged (it must PASS as-is) and still exercises the real request-building and response-parsing code end-to-end against canned JSON — no real network.

**Seam decision (stated, not silent):** the base URLs are hardcoded consts and **not** overridable by a package var. The chosen seam is the already-injectable `*http.Client`’s `Transport`. An equally valid but *more invasive* alternative would be changing each `const baseURL` to `var baseURL` — not taken, because the transport seam requires zero production edits. No production changes are required for any Task 13 file.

**Files:**
- `backend/internal/adapter/out/push/apns_test.go` (new, package `push`)
- `backend/internal/adapter/out/push/fcm_test.go` (new, package `push`)
- `backend/internal/adapter/out/openrouter/client_test.go` (new, package `openrouter`)
- `backend/internal/adapter/out/stripeapi/payments_test.go` (new, package `stripeapi`)

**Interfaces / real symbols under test:**
- `push`: `NewDispatcher(cfg config.Push, hc *http.Client) *Dispatcher`; `(*Dispatcher).Send(ctx, domain.NotificationDevice, title, body string, data map[string]string) error`. APNs wired only when `cfg.APNs.{KeyID,TeamID,KeyP8}` all non-empty; FCM wired when `cfg.FCM.ServiceAccountJSON` non-empty. Consts: `apnsHost = "https://api.push.apple.com"`, `defaultAPNsTopic = "app.calendium"` (APNs path `/3/device/<token>`; headers `apns-topic`,`apns-push-type:alert`,`apns-priority:10`; ES256 provider-token JWT `{alg:ES256,kid:KeyID}` / `{iss:TeamID,iat}`). FCM: token exchange (`grant_type=urn:ietf:params:oauth:grant-type:jwt-bearer`, RS256 assertion) at `token_uri`, then POST `…/v1/projects/<project_id>/messages:send`.
- `openrouter`: `NewClient(apiKey, model string, hc) *Client`; `(*Client).Complete(ctx, system, user) (text, model string, err error)`. Const `completionsURL` path `/api/v1/chat/completions`; 401/403 → wrapped `domain.ErrUnauthorized`.
- `stripeapi`: `NewClient(secretKey, webhookSecret, annualPriceID string, hc) *Client`; `EnsureCustomer(ctx, domain.User) (string, error)`, `CreateCheckoutSession(ctx, port.CheckoutParams) (string, error)`, `CreatePortalSession(ctx, customerID, returnURL string) (string, error)`. Const `apiBase = "https://api.stripe.com/v1"` (so rewritten paths keep the `/v1` prefix). `do()` maps 401/403→`domain.ErrUnauthorized`, 404→`domain.ErrNotFound`.

---

#### Task 13a — `push/apns_test.go` (transport-rewrite seam + shared helper)

The rewrite helper lives here and is reused by `fcm_test.go` (same package `push`; `webpush_test.go` defines no clashing symbols).

- [ ] Create `backend/internal/adapter/out/push/apns_test.go`:

```go
package push

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"calendium/backend/internal/config"
	"calendium/backend/internal/domain"
)

// rewriteRoundTripper redirects the hardcoded APNs/FCM hosts to the test
// server. The *http.Client passed to NewDispatcher is the injection seam;
// production source is unchanged.
type rewriteRoundTripper struct {
	target *url.URL
	base   http.RoundTripper
}

func (rt rewriteRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = rt.target.Scheme
	req.URL.Host = rt.target.Host
	req.Host = rt.target.Host
	return rt.base.RoundTrip(req)
}

func rewriteClient(t *testing.T, serverURL string) *http.Client {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	return &http.Client{Transport: rewriteRoundTripper{target: u, base: http.DefaultTransport}}
}

// ecKeyP8PEM generates a P-256 key and its PKCS#8 PEM (an Apple .p8 shape).
func ecKeyP8PEM(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func TestAPNsSend(t *testing.T) {
	key, keyPEM := ecKeyP8PEM(t)

	var gotPath, gotAuth, gotTopic, gotType, gotPriority, gotCT string
	var gotPayload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotTopic = r.Header.Get("apns-topic")
		gotType = r.Header.Get("apns-push-type")
		gotPriority = r.Header.Get("apns-priority")
		gotCT = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotPayload)
		w.WriteHeader(http.StatusOK) // APNs signals success with 200 + empty body
	}))
	defer srv.Close()

	cfg := config.Push{APNs: config.APNs{KeyID: "KID123", TeamID: "TEAM99", KeyP8: keyPEM}}
	d := NewDispatcher(cfg, rewriteClient(t, srv.URL))

	dev := domain.NotificationDevice{Platform: domain.PlatformIOS, Token: "abc123devtoken"}
	if err := d.Send(context.Background(), dev, "New mail", "from Ada", map[string]string{"threadId": "t-1"}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if gotPath != "/3/device/abc123devtoken" {
		t.Errorf("path = %q", gotPath)
	}
	if gotTopic != defaultAPNsTopic {
		t.Errorf("apns-topic = %q, want %q", gotTopic, defaultAPNsTopic)
	}
	if gotType != "alert" || gotPriority != "10" {
		t.Errorf("apns-push-type=%q apns-priority=%q", gotType, gotPriority)
	}
	if gotCT != "application/json" {
		t.Errorf("content-type = %q", gotCT)
	}

	aps, _ := gotPayload["aps"].(map[string]any)
	alert, _ := aps["alert"].(map[string]any)
	if alert["title"] != "New mail" || alert["body"] != "from Ada" {
		t.Errorf("aps.alert = %v", alert)
	}
	if gotPayload["threadId"] != "t-1" { // custom data keys hoisted to top level
		t.Errorf("custom data key = %v", gotPayload["threadId"])
	}

	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	verifyES256JWT(t, strings.TrimPrefix(gotAuth, "Bearer "), &key.PublicKey, "KID123", "TEAM99")
}

// verifyES256JWT decodes a compact ES256 JWS, checks header/claims, and
// verifies the raw r||s (64-byte) signature with pub.
func verifyES256JWT(t *testing.T, token string, pub *ecdsa.PublicKey, wantKid, wantIss string) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt has %d parts", len(parts))
	}
	var hdr struct{ Alg, Kid string }
	hb, _ := base64.RawURLEncoding.DecodeString(parts[0])
	if err := json.Unmarshal(hb, &hdr); err != nil {
		t.Fatalf("decode header: %v", err)
	}
	if hdr.Alg != "ES256" || hdr.Kid != wantKid {
		t.Errorf("header = %+v, want alg ES256 kid %q", hdr, wantKid)
	}
	var claims struct {
		Iss string `json:"iss"`
		Iat int64  `json:"iat"`
	}
	cb, _ := base64.RawURLEncoding.DecodeString(parts[1])
	if err := json.Unmarshal(cb, &claims); err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	if claims.Iss != wantIss || claims.Iat == 0 {
		t.Errorf("claims = %+v, want iss %q + nonzero iat", claims, wantIss)
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	if len(sig) != 64 {
		t.Fatalf("signature = %d bytes, want 64", len(sig))
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(pub, digest[:], r, s) {
		t.Fatal("ES256 provider-token signature invalid")
	}
}
```

**Required cases:** APNs request routed to `/3/device/<token>`; topic/push-type/priority/content-type headers; `aps.alert` + hoisted custom data key; valid ES256 provider-token JWT (header alg/kid, claims iss/iat, signature verified with the generated key). (Optional extra: an APNs non-200 — return `410 {"reason":"BadDeviceToken"}` and assert `Send` errors with `apns http 410: BadDeviceToken`.)

---

#### Task 13b — `push/fcm_test.go`

Reuses `rewriteClient` from `apns_test.go`.

- [ ] Create `backend/internal/adapter/out/push/fcm_test.go`:

```go
package push

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"calendium/backend/internal/config"
	"calendium/backend/internal/domain"
)

// serviceAccountJSON builds a Google service-account JSON with a fresh RSA
// key. token_uri stays the real Google host — the rewrite transport redirects
// it (path "/token") to the test server; messages:send is redirected the same
// way.
func serviceAccountJSON(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	b, err := json.Marshal(map[string]string{
		"project_id":   "proj-42",
		"client_email": "sa@proj-42.iam.gserviceaccount.com",
		"private_key":  keyPEM,
		"token_uri":    "https://oauth2.googleapis.com/token",
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFCMSend(t *testing.T) {
	var tokenForm url.Values
	var sendPath, sendAuth, sendCT string
	var sendBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			raw, _ := io.ReadAll(r.Body)
			tokenForm, _ = url.ParseQuery(string(raw))
			_, _ = w.Write([]byte(`{"access_token":"ya29.test","expires_in":3600}`))
		case strings.HasSuffix(r.URL.Path, "messages:send"):
			sendPath = r.URL.Path
			sendAuth = r.Header.Get("Authorization")
			sendCT = r.Header.Get("Content-Type")
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &sendBody)
			_, _ = w.Write([]byte(`{"name":"projects/proj-42/messages/0:1"}`))
		default:
			t.Errorf("unexpected request path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	cfg := config.Push{FCM: config.FCM{ServiceAccountJSON: serviceAccountJSON(t)}}
	d := NewDispatcher(cfg, rewriteClient(t, srv.URL))

	dev := domain.NotificationDevice{Platform: domain.PlatformAndroid, Token: "fcm-reg-token"}
	if err := d.Send(context.Background(), dev, "New mail", "from Ada", map[string]string{"threadId": "t-1"}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Token exchange: OAuth2 JWT-bearer grant with an RS256 assertion.
	if gt := tokenForm.Get("grant_type"); gt != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		t.Errorf("grant_type = %q", gt)
	}
	if parts := strings.Split(tokenForm.Get("assertion"), "."); len(parts) != 3 {
		t.Errorf("assertion is not a compact JWS: %q", tokenForm.Get("assertion"))
	}

	// messages:send: bearer the minted access token + FCM v1 message shape.
	if sendPath != "/v1/projects/proj-42/messages:send" {
		t.Errorf("send path = %q", sendPath)
	}
	if sendAuth != "Bearer ya29.test" {
		t.Errorf("send Authorization = %q", sendAuth)
	}
	if sendCT != "application/json" {
		t.Errorf("send content-type = %q", sendCT)
	}
	msg, _ := sendBody["message"].(map[string]any)
	if msg["token"] != "fcm-reg-token" {
		t.Errorf("message.token = %v", msg["token"])
	}
	notif, _ := msg["notification"].(map[string]any)
	if notif["title"] != "New mail" || notif["body"] != "from Ada" {
		t.Errorf("message.notification = %v", notif)
	}
	data, _ := msg["data"].(map[string]any)
	if data["threadId"] != "t-1" {
		t.Errorf("message.data = %v", data)
	}
}
```

**Required cases:** OAuth2 token exchange (`grant_type` + well-formed RS256 assertion) routed to `token_uri`; `messages:send` routed to `/v1/projects/<project_id>/messages:send` with `Bearer <access_token>`, JSON content-type, and the FCM v1 body (`message.token` / `message.notification.{title,body}` / `message.data`). (Optional extra: assertion RS256 signature fully verified via `rsa.VerifyPKCS1v15` with the SA public key; and an error path where `/token` returns non-200 → `Send` errors with `fcm token exchange http …`.)

- [ ] Run push tests:
```
cd backend && go test ./internal/adapter/out/push/ -run 'TestAPNsSend|TestFCMSend' -v
```
Expected PASS: `TestAPNsSend`, `TestFCMSend` (existing `webpush_test.go` tests remain green).

- [ ] Commit: `git add backend/internal/adapter/out/push/apns_test.go backend/internal/adapter/out/push/fcm_test.go && git commit -m "test(push): APNs ES256-JWT + FCM v1 send against httptest via transport-rewrite seam"`

---

#### Task 13c — `openrouter/client_test.go`

Base-URL seam: `completionsURL` is a hardcoded const → rewrite via the injected client's transport (same pattern; helper redefined locally for package `openrouter`).

- [ ] Create `backend/internal/adapter/out/openrouter/client_test.go`:

```go
package openrouter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"calendium/backend/internal/domain"
)

type rewriteRoundTripper struct {
	target *url.URL
	base   http.RoundTripper
}

func (rt rewriteRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = rt.target.Scheme
	req.URL.Host = rt.target.Host
	req.Host = rt.target.Host
	return rt.base.RoundTrip(req)
}

func rewriteClient(t *testing.T, serverURL string) *http.Client {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	return &http.Client{Transport: rewriteRoundTripper{target: u, base: http.DefaultTransport}}
}

func TestCompletePostsChatAndParses(t *testing.T) {
	var gotPath, gotAuth, gotCT string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"model":"anthropic/claude-3.5","choices":[{"message":{"role":"assistant","content":"Hi there"}}]}`))
	}))
	defer srv.Close()

	c := NewClient("sk-or-key", "openrouter/auto", rewriteClient(t, srv.URL))
	text, model, err := c.Complete(context.Background(), "be brief", "hello")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if text != "Hi there" {
		t.Errorf("text = %q", text)
	}
	if model != "anthropic/claude-3.5" { // upstream-reported model wins over configured
		t.Errorf("model = %q", model)
	}
	if gotPath != "/api/v1/chat/completions" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer sk-or-key" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotCT != "application/json" {
		t.Errorf("content-type = %q", gotCT)
	}
	if gotBody["model"] != "openrouter/auto" {
		t.Errorf("body model = %v", gotBody["model"])
	}
	msgs, ok := gotBody["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("messages = %v", gotBody["messages"])
	}
	sys := msgs[0].(map[string]any)
	usr := msgs[1].(map[string]any)
	if sys["role"] != "system" || sys["content"] != "be brief" {
		t.Errorf("system msg = %v", sys)
	}
	if usr["role"] != "user" || usr["content"] != "hello" {
		t.Errorf("user msg = %v", usr)
	}
}

// TestCompleteOmitsEmptySystem: no system message when system == "".
func TestCompleteOmitsEmptySystem(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"model":"m","choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()

	c := NewClient("k", "m", rewriteClient(t, srv.URL))
	if _, _, err := c.Complete(context.Background(), "", "just user"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	msgs := gotBody["messages"].([]any)
	if len(msgs) != 1 || msgs[0].(map[string]any)["role"] != "user" {
		t.Fatalf("messages = %v, want single user message", msgs)
	}
}

func TestCompleteErrors(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantUnauth bool
	}{
		{"server error", http.StatusInternalServerError, `{"error":{"message":"boom"}}`, false},
		{"unauthorized", http.StatusUnauthorized, `{"error":{"message":"bad key"}}`, true},
		{"no choices", http.StatusOK, `{"model":"m","choices":[]}`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c := NewClient("k", "", rewriteClient(t, srv.URL))
			_, _, err := c.Complete(context.Background(), "", "hi")
			if err == nil {
				t.Fatal("want error")
			}
			if tc.wantUnauth && !errors.Is(err, domain.ErrUnauthorized) {
				t.Fatalf("err = %v, want wrap of domain.ErrUnauthorized", err)
			}
		})
	}
}
```

**Required cases:** POST to `/api/v1/chat/completions` with `Bearer <key>` + JSON body (`model` + ordered system/user `messages`); parse `(text, model)` with upstream model taking precedence; empty system omitted; non-2xx → error; 401 → `errors.Is(domain.ErrUnauthorized)`; empty `choices` → error.

- [ ] Run:
```
cd backend && go test ./internal/adapter/out/openrouter/ -v
```
Expected PASS: `TestCompletePostsChatAndParses`, `TestCompleteOmitsEmptySystem`, `TestCompleteErrors` (3 subtests).

- [ ] Commit: `git add backend/internal/adapter/out/openrouter/client_test.go && git commit -m "test(openrouter): Complete posts chat-completions JSON and parses text/model"`

---

#### Task 13d — `stripeapi/payments_test.go`

Base-URL seam: `apiBase = "https://api.stripe.com/v1"` is a hardcoded const → rewrite via the injected client's transport; rewritten request paths therefore keep the `/v1` prefix (e.g. `/v1/customers/search`).

- [ ] Create `backend/internal/adapter/out/stripeapi/payments_test.go`:

```go
package stripeapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

type rewriteRoundTripper struct {
	target *url.URL
	base   http.RoundTripper
}

func (rt rewriteRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = rt.target.Scheme
	req.URL.Host = rt.target.Host
	req.Host = rt.target.Host
	return rt.base.RoundTrip(req)
}

func rewriteClient(t *testing.T, serverURL string) *http.Client {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	return &http.Client{Transport: rewriteRoundTripper{target: u, base: http.DefaultTransport}}
}

func strptr(s string) *string { return &s }

func TestEnsureCustomerCreatesWhenMissing(t *testing.T) {
	var searchQuery, custBody, custCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/customers/search":
			searchQuery = r.URL.Query().Get("query")
			_, _ = w.Write([]byte(`{"data":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/customers":
			raw, _ := io.ReadAll(r.Body)
			custBody = string(raw)
			custCT = r.Header.Get("Content-Type")
			_, _ = w.Write([]byte(`{"id":"cus_new"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	c := NewClient("sk_test", "wh", "price_x", rewriteClient(t, srv.URL))
	id, err := c.EnsureCustomer(context.Background(),
		domain.User{ID: "user-9", Email: "ada@x.com", Name: strptr("Ada")})
	if err != nil {
		t.Fatalf("EnsureCustomer: %v", err)
	}
	if id != "cus_new" {
		t.Errorf("id = %q", id)
	}
	if !strings.Contains(searchQuery, "metadata['user_id']:'user-9'") {
		t.Errorf("search query = %q", searchQuery)
	}
	if custCT != "application/x-www-form-urlencoded" {
		t.Errorf("create content-type = %q", custCT)
	}
	form, _ := url.ParseQuery(custBody)
	if form.Get("email") != "ada@x.com" || form.Get("metadata[user_id]") != "user-9" || form.Get("name") != "Ada" {
		t.Errorf("create form = %q", custBody)
	}
}

func TestEnsureCustomerReturnsExisting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/customers/search" {
			t.Errorf("unexpected create call: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"cus_existing"}]}`))
	}))
	defer srv.Close()

	c := NewClient("sk_test", "wh", "price_x", rewriteClient(t, srv.URL))
	id, err := c.EnsureCustomer(context.Background(), domain.User{ID: "user-9", Email: "a@b.com"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "cus_existing" {
		t.Errorf("id = %q", id)
	}
}

func TestCreateCheckoutSession(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/checkout/sessions" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		form, _ = url.ParseQuery(string(raw))
		_, _ = w.Write([]byte(`{"id":"cs_1","url":"https://checkout.stripe.com/c/pay/cs_1"}`))
	}))
	defer srv.Close()

	c := NewClient("sk_test", "wh", "price_annual", rewriteClient(t, srv.URL))
	got, err := c.CreateCheckoutSession(context.Background(), port.CheckoutParams{
		UserID: "user-9", CustomerID: "cus_1",
		SuccessURL: "https://a/ok", CancelURL: "https://a/no", TrialDays: 14,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://checkout.stripe.com/c/pay/cs_1" {
		t.Errorf("url = %q", got)
	}
	want := map[string]string{
		"mode":                                 "subscription",
		"line_items[0][price]":                 "price_annual",
		"line_items[0][quantity]":              "1",
		"success_url":                          "https://a/ok",
		"cancel_url":                           "https://a/no",
		"client_reference_id":                  "user-9",
		"metadata[user_id]":                    "user-9",
		"subscription_data[metadata][user_id]": "user-9",
		"customer":                             "cus_1",
		"subscription_data[trial_period_days]": "14",
	}
	for k, v := range want {
		if form.Get(k) != v {
			t.Errorf("form[%q] = %q, want %q", k, form.Get(k), v)
		}
	}
}

func TestCreatePortalSession(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/billing_portal/sessions" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		form, _ = url.ParseQuery(string(raw))
		_, _ = w.Write([]byte(`{"id":"bps_1","url":"https://billing.stripe.com/p/session/bps_1"}`))
	}))
	defer srv.Close()

	c := NewClient("sk_test", "wh", "price_x", rewriteClient(t, srv.URL))
	got, err := c.CreatePortalSession(context.Background(), "cus_1", "https://a/return")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://billing.stripe.com/p/session/bps_1" {
		t.Errorf("url = %q", got)
	}
	if form.Get("customer") != "cus_1" || form.Get("return_url") != "https://a/return" {
		t.Errorf("form = %v", form)
	}
}

// TestDoMapsErrorStatuses checks the shared do() sentinel mapping.
func TestDoMapsErrorStatuses(t *testing.T) {
	tests := []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, domain.ErrUnauthorized},
		{http.StatusNotFound, domain.ErrNotFound},
	}
	for _, tc := range tests {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"nope"}}`))
		}))
		c := NewClient("sk_test", "wh", "price_x", rewriteClient(t, srv.URL))
		_, err := c.CreatePortalSession(context.Background(), "cus_1", "https://a/r")
		if !errors.Is(err, tc.want) {
			t.Errorf("status %d: err = %v, want wrap of %v", tc.status, err, tc.want)
		}
		srv.Close()
	}
}
```

**Required cases:** `EnsureCustomer` GET search with the `metadata['user_id']:'<id>'` query → returns existing id when found (no create call), else POST `/v1/customers` (form: email / metadata[user_id] / name) and returns created id; `CreateCheckoutSession` POST `/v1/checkout/sessions` asserting mode / price / quantity / urls / client_reference_id / metadata / subscription_data + optional customer & trial, returns `url`; `CreatePortalSession` POST `/v1/billing_portal/sessions` (customer / return_url) returns `url`; `do()` sentinel mapping 401→`ErrUnauthorized`, 404→`ErrNotFound` via `errors.Is`.

- [ ] Run:
```
cd backend && go test ./internal/adapter/out/stripeapi/ -run 'TestEnsureCustomer|TestCreateCheckoutSession|TestCreatePortalSession|TestDoMaps' -v
```
Expected PASS: `TestEnsureCustomerCreatesWhenMissing`, `TestEnsureCustomerReturnsExisting`, `TestCreateCheckoutSession`, `TestCreatePortalSession`, `TestDoMapsErrorStatuses` (existing `webhook_test.go` stays green).

- [ ] Full sweep for the section:
```
cd backend && go test ./internal/adapter/out/... -v
```
Expected PASS across `postgres`, `authjwt`, `push`, `openrouter`, `stripeapi`.

- [ ] Commit: `git add backend/internal/adapter/out/stripeapi/payments_test.go && git commit -m "test(stripeapi): EnsureCustomer/Checkout/Portal form params + error sentinels via httptest"`

---

### Task 14.0: Add the mock-server base-URL seam to the Google + Microsoft adapters (prep)

**Files:**
- Modify `backend/internal/adapter/out/googleapi/mail.go` — `const gmailBase` → `var gmailBase`.
- Modify `backend/internal/adapter/out/googleapi/calendar.go` — `const calendarBase` → `var calendarBase`.
- Modify `backend/internal/adapter/out/msgraph/client.go` — `const graphBase` → `var graphBase`.

**Why:** All three adapters build every request URL from a hardcoded base constant (`gmailBase = "https://gmail.googleapis.com/gmail/v1/users/me"`, `calendarBase = "https://www.googleapis.com/calendar/v3"`, `graphBase = "https://graph.microsoft.com/v1.0"`). `NewClient(clientID, clientSecret, hc *http.Client)` lets us inject an `*http.Client` but **not** a base URL, so pointing a client at an `httptest.Server` requires a package-level `var` the white-box test can reassign (and restore via `t.Cleanup`). No behavioral change — the default values are identical; only the declaration keyword changes. This is the single production edit in Task 14; it is mechanical and must land in its own commit before any test file is added.

**Interfaces:**
- Consumes: nothing.
- Produces: writable package vars `gmailBase`, `calendarBase` (package `googleapi`) and `graphBase` (package `msgraph`).

- [ ] **Step 1: Convert the three constants to vars**
  ```go
  // backend/internal/adapter/out/googleapi/mail.go
  // was: const gmailBase = "https://gmail.googleapis.com/gmail/v1/users/me"
  var gmailBase = "https://gmail.googleapis.com/gmail/v1/users/me"

  // backend/internal/adapter/out/googleapi/calendar.go
  // was: const calendarBase = "https://www.googleapis.com/calendar/v3"
  var calendarBase = "https://www.googleapis.com/calendar/v3"

  // backend/internal/adapter/out/msgraph/client.go
  // was: const graphBase = "https://graph.microsoft.com/v1.0"
  var graphBase = "https://graph.microsoft.com/v1.0"
  ```
  Leave the OAuth `authEndpoint`/`tokenEndpoint` constants alone — Task 14 does not test the OAuth flow, so no seam is needed there.

- [ ] **Step 2: Run** — the whole module must still build and the existing recurrence test must still pass.
  ```
  cd backend && go build ./... && go test ./internal/adapter/out/... 
  ```
  Expected PASS (no test yet references the vars; this only proves the production edit compiles and changes nothing).

- [ ] **Step 3: Commit**
  ```
  git add backend/internal/adapter/out/googleapi/mail.go \
          backend/internal/adapter/out/googleapi/calendar.go \
          backend/internal/adapter/out/msgraph/client.go
  git commit -m "test(adapters): make provider base URLs overridable for httptest seams"
  ```

---

### Task 14.1: googleapi Gmail adapter — mail_test.go

**Files:**
- Create/Test `backend/internal/adapter/out/googleapi/mail_test.go` (package `googleapi`, white-box).

**Interfaces:**
- Consumes (real signatures, package `googleapi`):
  - `NewClient(clientID, clientSecret string, hc *http.Client) *Client`
  - `(*Client).SyncMail(ctx context.Context, accessToken, cursor string) (port.MailSyncPage, error)`
  - `(*Client).Send(ctx context.Context, accessToken string, msg port.OutgoingMessage) (port.SentMessage, error)`
  - `(*Client).ModifyLabels(ctx context.Context, accessToken, providerThreadID string, add, remove []string) error`
  - seam vars `gmailBase`, `calendarBase` (Task 14.0).
- Produces (referenced by Task 14.2 in the same package): `newGoogleServer(t *testing.T, h http.HandlerFunc) *httptest.Server`, `b64url(s string) string`.

- [ ] **Step 1: Write the failing test** — the shared helper + the mandated representative (`SyncMail` initial full sync mapping).
  ```go
  package googleapi

  import (
  	"context"
  	"encoding/base64"
  	"fmt"
  	"io"
  	"net/http"
  	"net/http/httptest"
  	"reflect"
  	"strings"
  	"testing"
  	"time"

  	"calendium/backend/internal/domain"
  )

  // b64url encodes a body the way Gmail returns message part data.
  func b64url(s string) string { return base64.URLEncoding.EncodeToString([]byte(s)) }

  // newGoogleServer starts a mock Google API and points the package base-URL
  // seams (gmailBase, calendarBase) at it for the life of the test.
  // Requires the Task 14.0 seam: gmailBase/calendarBase are package vars.
  func newGoogleServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
  	t.Helper()
  	srv := httptest.NewServer(h)
  	t.Cleanup(srv.Close)
  	oldGmail, oldCal := gmailBase, calendarBase
  	gmailBase = srv.URL + "/gmail/v1/users/me"
  	calendarBase = srv.URL + "/calendar/v3"
  	t.Cleanup(func() { gmailBase = oldGmail; calendarBase = oldCal })
  	return srv
  }

  func TestClient_SyncMail_InitialFullSync(t *testing.T) {
  	const wantToken = "access-tok-1"

  	threadJSON := fmt.Sprintf(`{
  		"id":"t1",
  		"messages":[
  			{"id":"m1","threadId":"t1","labelIds":["INBOX","UNREAD"],
  			 "snippet":"first snippet","internalDate":"1704067200000",
  			 "payload":{"mimeType":"text/plain",
  			   "headers":[{"name":"From","value":"Alice <alice@example.com>"},
  			              {"name":"To","value":"me@example.com"},
  			              {"name":"Subject","value":"Hello thread"}],
  			   "body":{"data":%q}}},
  			{"id":"m2","threadId":"t1","labelIds":["INBOX","STARRED"],
  			 "snippet":"second snippet","internalDate":"1704153600000",
  			 "payload":{"mimeType":"text/html",
  			   "headers":[{"name":"From","value":"Bob <bob@example.com>"},
  			              {"name":"Subject","value":"Re: Hello thread"}],
  			   "body":{"data":%q}}}
  		]}`, b64url("Hi there"), b64url("<p>Reply</p>"))

  	newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
  		if got := r.Header.Get("Authorization"); got != "Bearer "+wantToken {
  			t.Errorf("Authorization = %q, want Bearer %s", got, wantToken)
  		}
  		switch p := r.URL.Path; {
  		case strings.HasSuffix(p, "/profile"):
  			io.WriteString(w, `{"historyId":"98765"}`)
  		case strings.HasSuffix(p, "/labels"):
  			io.WriteString(w, `{"labels":[
  				{"id":"INBOX","name":"INBOX","type":"system"},
  				{"id":"Label_1","name":"Work","type":"user","color":{"backgroundColor":"#ff0000"}}
  			]}`)
  		case strings.HasSuffix(p, "/threads"): // thread list (no nextPageToken => single page)
  			io.WriteString(w, `{"threads":[{"id":"t1"}]}`)
  		case strings.Contains(p, "/threads/"): // one full thread
  			io.WriteString(w, threadJSON)
  		default:
  			t.Errorf("unexpected path %q", p)
  			http.NotFound(w, r)
  		}
  	})

  	c := NewClient("cid", "secret", nil)
  	page, err := c.SyncMail(context.Background(), wantToken, "")
  	if err != nil {
  		t.Fatalf("SyncMail: %v", err)
  	}

  	// Steady-state history cursor built from the profile snapshot.
  	if page.NextCursor != "hist:98765" {
  		t.Errorf("NextCursor = %q, want hist:98765", page.NextCursor)
  	}
  	if page.HasMore {
  		t.Errorf("HasMore = true, want false")
  	}

  	// Labels: system has no color; user label carries its color.
  	if len(page.Labels) != 2 {
  		t.Fatalf("Labels len = %d, want 2", len(page.Labels))
  	}
  	if page.Labels[0].ProviderLabelID != "INBOX" ||
  		page.Labels[0].Kind != domain.LabelKindSystem || page.Labels[0].Color != nil {
  		t.Errorf("system label mapped wrong: %+v", page.Labels[0])
  	}
  	if page.Labels[1].Kind != domain.LabelKindUser ||
  		page.Labels[1].Color == nil || *page.Labels[1].Color != "#ff0000" {
  		t.Errorf("user label mapped wrong: %+v", page.Labels[1])
  	}

  	// Thread aggregation across both messages.
  	if len(page.Threads) != 1 {
  		t.Fatalf("Threads len = %d, want 1", len(page.Threads))
  	}
  	th := page.Threads[0]
  	if th.ProviderThreadID != "t1" || th.Subject != "Hello thread" {
  		t.Errorf("thread id/subject wrong: %+v", th)
  	}
  	if th.Snippet != "second snippet" { // snippet follows the latest message
  		t.Errorf("Snippet = %q, want %q", th.Snippet, "second snippet")
  	}
  	if !th.Unread || !th.Starred || !th.InInbox {
  		t.Errorf("flags unread=%v starred=%v inInbox=%v, want all true", th.Unread, th.Starred, th.InInbox)
  	}
  	if th.MessageCount != 2 {
  		t.Errorf("MessageCount = %d, want 2", th.MessageCount)
  	}
  	if want := time.UnixMilli(1704153600000).UTC(); !th.LastMessageAt.Equal(want) {
  		t.Errorf("LastMessageAt = %v, want %v", th.LastMessageAt, want)
  	}
  	if want := []string{"INBOX", "UNREAD", "STARRED"}; !reflect.DeepEqual(th.LabelIDs, want) {
  		t.Errorf("LabelIDs = %v, want %v", th.LabelIDs, want)
  	}
  	if len(th.Participants) != 2 ||
  		th.Participants[0].Email != "alice@example.com" ||
  		th.Participants[1].Email != "bob@example.com" {
  		t.Errorf("Participants = %+v, want [alice, bob]", th.Participants)
  	}

  	// Messages: bodies decoded per MIME type, raw headers preserved.
  	if len(page.Messages) != 2 {
  		t.Fatalf("Messages len = %d, want 2", len(page.Messages))
  	}
  	m1 := page.Messages[0].Message
  	if m1.ProviderMessageID != "m1" || m1.From.Email != "alice@example.com" || m1.Subject != "Hello thread" {
  		t.Errorf("m1 mapped wrong: %+v", m1)
  	}
  	if m1.BodyText != "Hi there" {
  		t.Errorf("m1.BodyText = %q, want %q", m1.BodyText, "Hi there")
  	}
  	if !m1.SentAt.Equal(time.UnixMilli(1704067200000).UTC()) {
  		t.Errorf("m1.SentAt = %v", m1.SentAt)
  	}
  	if page.Messages[0].Headers["From"] != "Alice <alice@example.com>" {
  		t.Errorf("raw From header not preserved: %v", page.Messages[0].Headers)
  	}
  	if m2 := page.Messages[1].Message; m2.BodyHTML != "<p>Reply</p>" {
  		t.Errorf("m2.BodyHTML = %q, want %q", m2.BodyHTML, "<p>Reply</p>")
  	}
  }
  ```

- [ ] **Step 2: Run**
  ```
  cd backend && go test ./internal/adapter/out/googleapi/ -run TestClient_SyncMail_InitialFullSync -v
  ```
  Expected PASS.

- [ ] **Step 3: Commit**
  ```
  git add backend/internal/adapter/out/googleapi/mail_test.go
  git commit -m "test(googleapi): cover Gmail SyncMail full-sync mapping"
  ```

**Required cases** (each a `t.Run` subtest / test func in `mail_test.go`; all mandatory). For request-payload assertions, read `r.Body` in the handler and `json.Unmarshal` it into a struct/map, then assert fields:

- `(*Client).SyncMail`, cursor `"hist:55"` (incremental) — mock `GET …/history?startHistoryId=55` returns `{"history":[{"messages":[{"threadId":"t9"}]}],"historyId":"77"}`, `GET …/threads/t9?format=full` returns one thread. Expect `len(page.Threads)==1` (t9), `page.NextCursor=="hist:77"`.
- `(*Client).SyncMail`, cursor `"hist:55"` with a **deleted** thread — `/history` yields threadId `t9`, but `GET …/threads/t9` returns 404. Expect no error and that thread silently skipped (`errors.Is(ErrNotFound)` → `continue`); `page.NextCursor=="hist:77"`.
- `(*Client).SyncMail`, cursor `"hist:oldid"` with **expired** historyId — `/history` returns HTTP 404; adapter must transparently restart a full sync (mock also serves `/profile`,`/labels`,`/threads`). Expect `err==nil` and `page.NextCursor=="hist:<newProfileHist>"`.
- `(*Client).SyncMail`, cursor `"bogus"` — no HTTP call; `errors.Is(err, domain.ErrValidation)` and `page` zero-valued.
- `(*Client).SyncMail` transport error path — `/threads` returns HTTP 403; `errors.Is(err, domain.ErrUnauthorized)` (via `httpError.Unwrap`).
- `(*Client).Send`, text-only — `OutgoingMessage{From:{Email:"me@x.com"}, To:[{Email:"you@x.com"}], Subject:"Hi", BodyText:"Body"}`. Capture `POST …/messages/send` body `{"raw":...}`; base64url-decode `raw` and assert it contains `To: you@x.com`, `Subject: Hi`, `Content-Type: text/plain`, and `Body`; response `{"id":"s1","threadId":"th1"}` ⇒ `SentMessage{ProviderMessageID:"s1", ProviderThreadID:"th1"}`.
- `(*Client).Send`, threaded reply — `msg.ProviderThreadID="th1"`. Assert captured JSON body has `"threadId":"th1"`.
- `(*Client).Send`, multipart — both `BodyHTML` and `BodyText` set. Decoded `raw` contains `multipart/alternative; boundary=`, a `text/plain` part and a `text/html` part carrying the HTML.
- `(*Client).ModifyLabels`, `add=["STARRED"]`, `remove=["UNREAD"]` — assert `POST …/threads/{id}/modify` body equals `{"addLabelIds":["STARRED"],"removeLabelIds":["UNREAD"]}`; returns `nil`.
- `(*Client).ModifyLabels`, `add=nil`, `remove=nil` — returns `nil` and makes **no** HTTP request (fail the test if the handler is hit).
- `(*Client).ModifyLabels`, server 401 — `errors.Is(err, domain.ErrUnauthorized)`.

---

### Task 14.2: googleapi Google Calendar adapter — calendar_test.go

**Files:**
- Create/Test `backend/internal/adapter/out/googleapi/calendar_test.go` (package `googleapi`).

**Interfaces:**
- Consumes: `newGoogleServer`, `b64url` (Task 14.1, same package); `NewClient`; and
  - `(*Client).SyncCalendars(ctx, accessToken string) ([]domain.Calendar, error)`
  - `(*Client).SyncEvents(ctx, accessToken, providerCalendarID, cursor string) (port.CalendarSyncPage, error)`
  - `(*Client).CreateEvent(ctx, accessToken, providerCalendarID string, in domain.EventInput) (domain.Event, error)`
  - `(*Client).UpdateEvent(ctx, accessToken, providerCalendarID, providerEventID string, patch domain.EventPatch) (domain.Event, error)`
  - `(*Client).DeleteEvent(ctx, accessToken, providerCalendarID, providerEventID string) error`
  - `(*Client).RSVP(ctx, accessToken, providerCalendarID, providerEventID string, response domain.RsvpStatus) error`
- Produces: the test funcs below.

- [ ] **Step 1: Write the failing test** — representative write-path mapping (`CreateEvent`), which exercises request-body construction and `mapGcalEvent` on the response.
  ```go
  package googleapi

  import (
  	"context"
  	"encoding/json"
  	"io"
  	"net/http"
  	"testing"
  	"time"

  	"calendium/backend/internal/domain"
  )

  func TestClient_CreateEvent_RequestAndMapping(t *testing.T) {
  	var gotBody map[string]any
  	var gotQuery string

  	newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
  		gotQuery = r.URL.RawQuery
  		raw, _ := io.ReadAll(r.Body)
  		_ = json.Unmarshal(raw, &gotBody)
  		// Response carries the Meet link the mapper should surface.
  		io.WriteString(w, `{
  			"id":"evt1","status":"confirmed","summary":"Standup",
  			"start":{"dateTime":"2026-07-08T09:00:00Z"},
  			"end":{"dateTime":"2026-07-08T09:30:00Z"},
  			"hangoutLink":"https://meet.google.com/abc-defg-hij"
  		}`)
  	})

  	start := time.Date(2026, 7, 8, 9, 0, 0, 0, time.UTC)
  	in := domain.EventInput{
  		Title:           "Standup",
  		Start:           start,
  		End:             start.Add(30 * time.Minute),
  		RecurrenceRule:  "FREQ=DAILY",
  		AttendeeEmails:  []string{"team@example.com"},
  		ReminderMinutes: []int{10},
  		AddConferencing: true,
  	}
  	c := NewClient("cid", "secret", nil)
  	ev, err := c.CreateEvent(context.Background(), "tok", "primary", in)
  	if err != nil {
  		t.Fatalf("CreateEvent: %v", err)
  	}

  	// conferenceDataVersion=1 must be on the query.
  	if gotQuery != "conferenceDataVersion=1" {
  		t.Errorf("query = %q, want conferenceDataVersion=1", gotQuery)
  	}
  	if gotBody["summary"] != "Standup" {
  		t.Errorf("summary = %v", gotBody["summary"])
  	}
  	// RRULE gets the required "RRULE:" prefix, wrapped in a slice.
  	if rec, ok := gotBody["recurrence"].([]any); !ok || len(rec) != 1 || rec[0] != "RRULE:FREQ=DAILY" {
  		t.Errorf("recurrence = %v, want [RRULE:FREQ=DAILY]", gotBody["recurrence"])
  	}
  	if _, ok := gotBody["conferenceData"]; !ok {
  		t.Errorf("conferenceData missing from body: %v", gotBody)
  	}
  	if _, ok := gotBody["reminders"]; !ok {
  		t.Errorf("reminders missing from body: %v", gotBody)
  	}

  	// Response mapping.
  	if ev.ProviderEventID != "evt1" || ev.Title != "Standup" {
  		t.Errorf("event id/title wrong: %+v", ev)
  	}
  	if ev.Status != domain.EventConfirmed {
  		t.Errorf("Status = %v, want confirmed", ev.Status)
  	}
  	if !ev.Start.Equal(start) {
  		t.Errorf("Start = %v, want %v", ev.Start, start)
  	}
  	if ev.Conferencing == nil ||
  		ev.Conferencing.Provider != domain.ConferencingMeet ||
  		ev.Conferencing.URL != "https://meet.google.com/abc-defg-hij" {
  		t.Errorf("Conferencing = %+v, want Meet link", ev.Conferencing)
  	}
  }
  ```

- [ ] **Step 2: Run**
  ```
  cd backend && go test ./internal/adapter/out/googleapi/ -run TestClient_CreateEvent_RequestAndMapping -v
  ```
  Expected PASS.

- [ ] **Step 3: Commit**
  ```
  git add backend/internal/adapter/out/googleapi/calendar_test.go
  git commit -m "test(googleapi): cover Calendar create/sync/event mapping"
  ```

**Required cases** (all mandatory):

- `(*Client).SyncCalendars` — `GET …/users/me/calendarList` page 1 returns two items (`accessRole:"owner"` primary, `accessRole:"reader"`) plus `nextPageToken:"P2"`; page 2 returns one `writer` item, no token. Expect 3 `domain.Calendar`; `CanWrite` true for owner/writer and false for reader; `IsPrimary` true only for the primary; every `IsVisible==true`; both pages fetched.
- `(*Client).SyncEvents`, cursor `""` — items include one `status:"cancelled"` (⇒ `page.DeletedIDs` holds its id, not `Events`) and one confirmed event (⇒ mapped into `page.Events`); response `nextSyncToken:"S1"`. Expect `page.NextCursor=="sync:S1"`, `HasMore==false`.
- `(*Client).SyncEvents`, cursor `""` with `nextPageToken:"P1"` — expect `page.NextCursor=="page:P1"`, `HasMore==true`.
- `(*Client).SyncEvents`, cursor `"sync:old"` — first response HTTP 410; on the adapter's automatic retry (`cursor==""`) the mock returns a normal page. Expect `err==nil` and a page from the full listing (proves the 410-restart branch).
- `(*Client).SyncEvents`, cursor `"bogus"` — `errors.Is(err, domain.ErrValidation)`.
- `(*Client).CreateEvent`, all-day — `in.AllDay=true`. Assert `gotBody["start"]` carries a `date` key (not `dateTime`).
- `(*Client).UpdateEvent`, partial patch — only `Title` and `Start` set. Assert PATCH body contains `summary` and `start` but **not** `description`/`location`. Add a subtest where `patch.RecurrenceRule` points to `""` ⇒ body `recurrence` is an empty array (rule cleared).
- `(*Client).DeleteEvent` — three subtests: server 404 ⇒ `err==nil`; server 410 ⇒ `err==nil`; server 500 ⇒ `err!=nil` (idempotent delete only swallows not-found/gone).
- `(*Client).RSVP`, `domain.RsvpAccepted` — `GET` event returns attendees including one with `"self":true`; assert the follow-up `PATCH` body's attendee list sets that attendee's `responseStatus:"accepted"`.
- `(*Client).RSVP` with no self attendee — `GET` returns attendees none of which are `self`; expect `errors.Is(err, domain.ErrValidation)` and no PATCH.
- `mapGcalEvent` all-day parse (via `SyncEvents`) — an item with `start.date:"2026-03-01"` maps to `AllDay==true` and `Start` equal to `2026-03-01T00:00:00Z`.

---

### Task 14.3: msgraph Graph mail adapter — mail_test.go

**Files:**
- Create/Test `backend/internal/adapter/out/msgraph/mail_test.go` (package `msgraph`, white-box; sits alongside the existing `recurrence_test.go`).

**Interfaces:**
- Consumes (package `msgraph`): `NewClient(clientID, clientSecret string, hc *http.Client) *Client`; `(*Client).SyncMail`, `(*Client).Send`, `(*Client).ModifyLabels` (same port signatures as googleapi); seam var `graphBase` (Task 14.0); label-key constants `port.LabelKeyInbox/Starred/Unread/Trash/Spam`.
- Produces (referenced by Task 14.4): `newGraphServer(t *testing.T, h http.HandlerFunc) *httptest.Server`.

- [ ] **Step 1: Write the failing test** — the shared helper + representative (`SyncMail` delta mapping, deltaLink cursor).
  ```go
  package msgraph

  import (
  	"context"
  	"fmt"
  	"io"
  	"net/http"
  	"net/http/httptest"
  	"reflect"
  	"strings"
  	"testing"
  	"time"

  	"calendium/backend/internal/domain"
  )

  // newGraphServer starts a mock Graph API and points graphBase at it.
  // Requires the Task 14.0 seam: graphBase is a package var.
  func newGraphServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
  	t.Helper()
  	srv := httptest.NewServer(h)
  	t.Cleanup(srv.Close)
  	old := graphBase
  	graphBase = srv.URL + "/v1.0"
  	t.Cleanup(func() { graphBase = old })
  	return srv
  }

  func TestClient_SyncMail_Delta(t *testing.T) {
  	var deltaLink string // assigned after the server is up

  	const messages = `{"value":[
  		{"id":"g1","conversationId":"c1","subject":"Hi","bodyPreview":"preview one",
  		 "from":{"emailAddress":{"name":"Alice","address":"alice@example.com"}},
  		 "toRecipients":[{"emailAddress":{"address":"me@example.com"}}],
  		 "receivedDateTime":"2024-01-01T00:00:00Z","isRead":false,"parentFolderId":"folderA",
  		 "body":{"contentType":"html","content":"<p>Hi</p>"}},
  		{"id":"g2","conversationId":"c1","subject":"Re: Hi","bodyPreview":"preview two",
  		 "from":{"emailAddress":{"address":"bob@example.com"}},
  		 "receivedDateTime":"2024-01-02T00:00:00Z","isRead":true,
  		 "flag":{"flagStatus":"flagged"},"parentFolderId":"folderA"},
  		{"id":"g3","@removed":{"reason":"deleted"}}
  	]`

  	srv := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
  		switch p := r.URL.Path; {
  		case strings.HasSuffix(p, "/messages/delta"):
  			io.WriteString(w, fmt.Sprintf(`%s,"@odata.deltaLink":%q}`, messages, deltaLink))
  		case strings.HasSuffix(p, "/mailFolders"):
  			io.WriteString(w, `{"value":[
  				{"id":"folderA","displayName":"Inbox"},
  				{"id":"folderB","displayName":"Archive"}
  			]}`)
  		default:
  			t.Errorf("unexpected path %q", p)
  			http.NotFound(w, r)
  		}
  	})
  	deltaLink = srv.URL + "/v1.0/me/mailFolders/inbox/messages/delta?$deltatoken=xyz"

  	c := NewClient("cid", "secret", nil)
  	page, err := c.SyncMail(context.Background(), "tok", "")
  	if err != nil {
  		t.Fatalf("SyncMail: %v", err)
  	}

  	if page.NextCursor != deltaLink {
  		t.Errorf("NextCursor = %q, want deltaLink %q", page.NextCursor, deltaLink)
  	}
  	if page.HasMore {
  		t.Errorf("HasMore = true, want false")
  	}

  	// Folders mirror to labels on the initial delta only.
  	if len(page.Labels) != 2 || page.Labels[0].ProviderLabelID != "folderA" ||
  		page.Labels[0].Kind != domain.LabelKindSystem {
  		t.Fatalf("Labels = %+v, want folderA/folderB system labels", page.Labels)
  	}

  	// Tombstone (@removed) is dropped: only g1,g2 survive, one conversation.
  	if len(page.Messages) != 2 {
  		t.Fatalf("Messages len = %d, want 2 (tombstone dropped)", len(page.Messages))
  	}
  	if len(page.Threads) != 1 {
  		t.Fatalf("Threads len = %d, want 1", len(page.Threads))
  	}
  	th := page.Threads[0]
  	if th.ProviderThreadID != "c1" || th.Subject != "Re: Hi" { // subject follows latest
  		t.Errorf("thread id/subject wrong: %+v", th)
  	}
  	if th.Snippet != "preview two" {
  		t.Errorf("Snippet = %q, want preview two", th.Snippet)
  	}
  	if !th.Unread || !th.Starred || !th.InInbox {
  		t.Errorf("flags unread=%v starred=%v inInbox=%v, want all true", th.Unread, th.Starred, th.InInbox)
  	}
  	if th.MessageCount != 2 {
  		t.Errorf("MessageCount = %d, want 2", th.MessageCount)
  	}
  	if want := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC); !th.LastMessageAt.Equal(want) {
  		t.Errorf("LastMessageAt = %v, want %v", th.LastMessageAt, want)
  	}
  	if want := []string{"folderA"}; !reflect.DeepEqual(th.LabelIDs, want) {
  		t.Errorf("LabelIDs = %v, want %v", th.LabelIDs, want)
  	}
  	if len(th.Participants) != 2 ||
  		th.Participants[0].Email != "alice@example.com" ||
  		th.Participants[1].Email != "bob@example.com" {
  		t.Errorf("Participants = %+v, want [alice, bob]", th.Participants)
  	}

  	// g1 body: html -> BodyHTML; BodyText falls back to bodyPreview.
  	m1 := page.Messages[0].Message
  	if m1.ProviderMessageID != "g1" || m1.From.Email != "alice@example.com" {
  		t.Errorf("m1 mapped wrong: %+v", m1)
  	}
  	if m1.BodyHTML != "<p>Hi</p>" || m1.BodyText != "preview one" {
  		t.Errorf("m1 body wrong: html=%q text=%q", m1.BodyHTML, m1.BodyText)
  	}
  }
  ```

- [ ] **Step 2: Run**
  ```
  cd backend && go test ./internal/adapter/out/msgraph/ -run TestClient_SyncMail_Delta -v
  ```
  Expected PASS.

- [ ] **Step 3: Commit**
  ```
  git add backend/internal/adapter/out/msgraph/mail_test.go
  git commit -m "test(msgraph): cover Graph mail delta sync + folder-label mapping"
  ```

**Required cases** (all mandatory):

- `(*Client).SyncMail`, continuation cursor — pass a deltaLink-shaped URL (`srv.URL + "/v1.0/me/mailFolders/inbox/messages/delta?$deltatoken=…"`) as `cursor`; the mock serves that path directly. Assert `page.Labels==nil` (folders **not** re-fetched when `cursor!=""`) and `page.NextCursor` equals the new deltaLink.
- `(*Client).SyncMail`, `@odata.nextLink` pagination — delta response returns `@odata.nextLink` (no deltaLink). Expect `page.HasMore==true` and `page.NextCursor==nextLink`.
- `(*Client).SyncMail`, expired delta token — `cursor!=""` and the response is HTTP 410; adapter restarts with `""` (re-fetching folders + fresh delta). Expect `err==nil`, non-empty `page.Labels`.
- `(*Client).Send`, HTML body — `POST …/me/messages` (draft) captured body has `subject`, `body.contentType=="HTML"`, `body.content==BodyHTML`, and mapped `toRecipients`; mock returns `{"id":"d1","conversationId":"conv1"}`; adapter then `POST …/me/messages/d1/send` (assert the send call fired). Result `SentMessage{ProviderMessageID:"d1", ProviderThreadID:"conv1"}` and `SentAt` non-zero. **Note:** `Send` stamps `SentAt=time.Now().UTC()` (no injectable clock in this adapter) — assert `!SentAt.IsZero()`, never an exact value.
- `(*Client).Send`, text body — `BodyHTML==""`, `BodyText` set ⇒ draft `body.contentType=="Text"`, `content==BodyText`.
- `(*Client).Send`, existing thread — `msg.ProviderThreadID="conv-orig"` ⇒ `SentMessage.ProviderThreadID=="conv-orig"` (`firstNonEmpty` prefers the caller's id over `created.conversationId`).
- `(*Client).ModifyLabels`, `add=[STARRED]`, `remove=[UNREAD]` — `GET …/me/messages?$select=id,categories&$filter=conversationId eq 'c1'` returns `[{"id":"m1","categories":[]}]`; expect a `PATCH …/me/messages/m1` body carrying `flag.flagStatus=="flagged"` and `isRead==true`.
- `(*Client).ModifyLabels`, `add=[TRASH]` — expect `POST …/me/messages/m1/move` body `{"destinationId":"deleteditems"}`.
- `(*Client).ModifyLabels`, `remove=[INBOX]` (archive) — expect a `/move` with `{"destinationId":"archive"}`.
- `(*Client).ModifyLabels`, custom key `"Project"` (not a canonical `LabelKey*`) — expect the message PATCH sets `categories` to `["Project"]` (via `mergeCategories`).
- `(*Client).ModifyLabels`, conversation with no messages — `/me/messages` filter returns `{"value":[]}`; expect `errors.Is(err, domain.ErrNotFound)`.

---

### Task 14.4: msgraph Graph calendar adapter — calendar_test.go

**Files:**
- Create/Test `backend/internal/adapter/out/msgraph/calendar_test.go` (package `msgraph`).

**Interfaces:**
- Consumes: `newGraphServer` (Task 14.3, same package); `NewClient`; and the calendar port methods `SyncCalendars`, `SyncEvents`, `CreateEvent`, `UpdateEvent`, `DeleteEvent`, `RSVP` (same signatures listed in Task 14.2, on `msgraph.*Client`).
- Produces: the test funcs below.

- [ ] **Step 1: Write the failing test** — representative read-path (`SyncEvents` delta: cancelled/@removed ⇒ `DeletedIDs`, deltaLink cursor, `mapGraphEvent`).
  ```go
  package msgraph

  import (
  	"context"
  	"fmt"
  	"io"
  	"net/http"
  	"strings"
  	"testing"
  	"time"

  	"calendium/backend/internal/domain"
  )

  func TestClient_SyncEvents_Delta(t *testing.T) {
  	var deltaLink string

  	const events = `{"value":[
  		{"id":"e1","subject":"Review","showAs":"busy","sensitivity":"normal",
  		 "start":{"dateTime":"2026-07-08T09:00:00.0000000","timeZone":"UTC"},
  		 "end":{"dateTime":"2026-07-08T10:00:00.0000000","timeZone":"UTC"},
  		 "isReminderOn":true,"reminderMinutesBeforeStart":15,
  		 "onlineMeeting":{"joinUrl":"https://teams.microsoft.com/l/meetup/xyz"}},
  		{"id":"e2","isCancelled":true},
  		{"id":"e3","@removed":{"reason":"deleted"}}
  	]`

  	srv := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
  		if strings.Contains(r.URL.Path, "/calendarView/delta") {
  			io.WriteString(w, fmt.Sprintf(`%s,"@odata.deltaLink":%q}`, events, deltaLink))
  			return
  		}
  		t.Errorf("unexpected path %q", r.URL.Path)
  		http.NotFound(w, r)
  	})
  	deltaLink = srv.URL + "/v1.0/me/calendars/cal1/calendarView/delta?$deltatoken=zzz"

  	c := NewClient("cid", "secret", nil)
  	page, err := c.SyncEvents(context.Background(), "tok", "cal1", "")
  	if err != nil {
  		t.Fatalf("SyncEvents: %v", err)
  	}

  	if page.NextCursor != deltaLink || page.HasMore {
  		t.Errorf("cursor=%q hasMore=%v, want deltaLink + false", page.NextCursor, page.HasMore)
  	}
  	// e2 (isCancelled) and e3 (@removed) become tombstones.
  	if len(page.DeletedIDs) != 2 {
  		t.Errorf("DeletedIDs = %v, want [e2 e3]", page.DeletedIDs)
  	}
  	if len(page.Events) != 1 {
  		t.Fatalf("Events len = %d, want 1", len(page.Events))
  	}
  	ev := page.Events[0]
  	if ev.ProviderEventID != "e1" || ev.Title != "Review" {
  		t.Errorf("event id/title wrong: %+v", ev)
  	}
  	if want := time.Date(2026, 7, 8, 9, 0, 0, 0, time.UTC); !ev.Start.Equal(want) {
  		t.Errorf("Start = %v, want %v (fractional-second layout)", ev.Start, want)
  	}
  	if ev.Status != domain.EventConfirmed {
  		t.Errorf("Status = %v, want confirmed", ev.Status)
  	}
  	if len(ev.ReminderMinutes) != 1 || ev.ReminderMinutes[0] != 15 {
  		t.Errorf("ReminderMinutes = %v, want [15]", ev.ReminderMinutes)
  	}
  	if ev.Conferencing == nil ||
  		ev.Conferencing.Provider != domain.ConferencingTeams ||
  		ev.Conferencing.URL != "https://teams.microsoft.com/l/meetup/xyz" {
  		t.Errorf("Conferencing = %+v, want Teams join url", ev.Conferencing)
  	}
  }
  ```

- [ ] **Step 2: Run**
  ```
  cd backend && go test ./internal/adapter/out/msgraph/ -run TestClient_SyncEvents_Delta -v
  ```
  Expected PASS.

- [ ] **Step 3: Commit**
  ```
  git add backend/internal/adapter/out/msgraph/calendar_test.go
  git commit -m "test(msgraph): cover Graph calendar delta sync + event mapping"
  ```

**Required cases** (all mandatory):

- `(*Client).SyncCalendars` — `GET …/me/calendars?$top=100` returns two items (`isDefaultCalendar:true, canEdit:true, hexColor:"#112233"` and `canEdit:false`) plus `@odata.nextLink`; the second page returns one more. Expect 3 `domain.Calendar`; `CanWrite==canEdit`; `IsPrimary` true only for the default; `Color=="#112233"` on the first; `IsVisible==true`; both pages fetched.
- `(*Client).SyncEvents`, continuation cursor — pass a deltaLink URL as `cursor`; mock serves it directly with an `@odata.nextLink` ⇒ `page.HasMore==true`, `page.NextCursor==nextLink`.
- `(*Client).SyncEvents`, expired delta — `cursor!=""`, response HTTP 410 ⇒ adapter restarts with `""` (builds a fresh `calendarView/delta?startDateTime=…&endDateTime=…`); expect `err==nil`.
- `(*Client).CreateEvent` — `EventInput{Title, Start, End, Description, Location, AttendeeEmails:["a@x.com"], RecurrenceRule:"FREQ=WEEKLY;BYDAY=MO", ReminderMinutes:[15], AddConferencing:true}`. Capture `POST …/me/calendars/cal1/events` body; assert `subject`, `start`/`end` as `graphDateTime`, `isAllDay==false`, `body.contentType=="HTML"`, `location.displayName`, `attendees[0].type=="required"`, a non-nil `recurrence` (from `rruleToGraphRecurrence`), `isReminderOn==true` + `reminderMinutesBeforeStart==15`, `isOnlineMeeting==true` + `onlineMeetingProvider=="teamsForBusiness"`. Response with `onlineMeetingUrl` ⇒ mapped `Conferencing.Provider==ConferencingTeams`.
- `(*Client).CreateEvent`, all-day — `in.AllDay=true` ⇒ body `isAllDay==true` and `start.dateTime` uses the midnight layout `2006-01-02T00:00:00`.
- `(*Client).UpdateEvent`, partial patch — only `Title`+`Location` set ⇒ PATCH body has `subject`+`location` but not `start`/`end`. Add a subtest: `patch.RecurrenceRule` points to `""` ⇒ body `recurrence` is JSON `null`.
- `(*Client).DeleteEvent` — subtests: 404 ⇒ `nil`; 410 ⇒ `nil`; 500 ⇒ `err!=nil`.
- `(*Client).RSVP` — table over `{RsvpAccepted→"accept", RsvpDeclined→"decline", RsvpTentative→"tentativelyAccept"}`: assert `POST …/me/events/{id}/{action}` with body `{"sendResponse":true}`. Plus `RsvpNeedsAction ⇒ errors.Is(err, domain.ErrValidation)` and no HTTP call (Graph has no reset endpoint).
- `mapGraphEvent` sensitivity/status/response — an event with `sensitivity:"private"` ⇒ `Visibility==VisibilityPrivate`; `showAs:"tentative"` ⇒ `Status==EventTentative`; an attendee with `status.response:"declined"` ⇒ `Response==RsvpDeclined`, and the organizer's attendee entry ⇒ `Organizer==true`.

---

**Whole-area green gate** (run after all four test files land):
```
cd backend && go test ./internal/adapter/out/googleapi/... ./internal/adapter/out/msgraph/... -count=1
```
Expected PASS. Optionally record adapter coverage toward the ≥80% floor:
```
cd backend && go test ./internal/adapter/out/googleapi/... ./internal/adapter/out/msgraph/... -cover
```

---

### Task 15: Postgres repositories via testcontainers-go (package `postgres`)

Characterization tests for the real `database/sql` adapters in
`backend/internal/adapter/out/postgres/*.go`, exercised against a **real
Postgres** started with `testcontainers-go`. These are white-box tests
(`package postgres`) so they may call the unexported `newID`, `scanX`, and the
`Store.sealToken`/`openToken` helpers directly, and they build the store the
same way `cmd/api/main.go` does: `postgres.NewStore(db)` +
`store.SetTokenEncryptionKey(key)` + `migrate.Apply(ctx, db, migrations.FS)`.

The production code already exists and is expected to **PASS**. Per test:
write → `go test` (EXPECTED PASS) → if it fails, decide wrong-test vs real-bug
(note real bugs, fix in a separate commit) → commit.

**Files:**
- Modify: `backend/go.mod`, `backend/go.sum` (add the test-scope container deps).
- Create: `backend/internal/adapter/out/postgres/postgres_test.go` (TestMain harness + seed helpers, shared across the whole package).
- Create: `backend/internal/adapter/out/postgres/account_test.go`, `thread_test.go`, `message_test.go`, `draft_test.go`, `snippet_label_test.go`, `calendar_event_test.go`, `user_billing_test.go`, `misc_repo_test.go`.

**Interfaces:**
- **Consumes (production, real signatures):** `postgres.NewStore(db *sql.DB) *Store`; `(*Store).SetTokenEncryptionKey(key []byte) error`; the accessors `(*Store).Users()/Subscriptions()/StripeEvents()/Accounts()/OAuthStates()/SyncStates()/Devices()/Threads()/Messages()/Labels()/Drafts()/Snippets()/Calendars()/Events()` (each returns the matching `port.*Repo`); `(*Store).RunInTx(ctx, fn)`; `migrate.Apply(ctx context.Context, db *sql.DB, fsys fs.FS) error`; `migrations.FS`; the unexported `newID() string`. Port value types: `port.TokenSet{AccessToken,RefreshToken,ExpiresAt}`, `port.ThreadQuery{...}`, `port.SyncState{AccountID,Resource,Cursor,UpdatedAt}`, `port.OAuthState{State,UserID,Provider,RedirectURL,CodeVerifier,ExpiresAt}`.
- **Produces (harness helpers, referenced by every other test file in this task):** `newTestStore(t) (*Store, *sql.DB)`, `truncateAll(t)`, `seedUser(t, *Store, id string) domain.User`, `seedAccount(t, *Store, userID string) domain.ConnectedAccount`, `seedThread(t, *Store, accountID string, lastMsg time.Time) domain.Thread`, `seedCalendar(t, *Store, accountID string) domain.Calendar`.

> NOTE: package `postgres` cannot use the service-package fakes (Task 1). It uses the harness above against a live container.

---

- [ ] **Step 1: Add the test-scope container dependencies**

  Go has no separate test-only dependency scope; the modules land in `go.mod`
  but are referenced **only** from `_test.go`, so the `cmd/api` and `cmd/worker`
  binaries never link them. Run:

  ```bash
  cd backend && go get github.com/testcontainers/testcontainers-go@latest
  cd backend && go get github.com/testcontainers/testcontainers-go/modules/postgres@latest
  cd backend && go mod tidy
  ```

  Then prove the production commands still build and the package still vets
  (this also compiles nothing yet since the harness is not written — that is
  expected; the build gate is the point):

  ```bash
  cd backend && go build ./cmd/... && go vet ./internal/adapter/out/postgres/
  ```

  Expected: `go build ./cmd/...` succeeds (binaries do not import
  testcontainers). Commit:

  ```bash
  git add backend/go.mod backend/go.sum
  git commit -m "test(postgres): add testcontainers-go for repo integration tests"
  ```

- [ ] **Step 2: Write the shared container harness** — `backend/internal/adapter/out/postgres/postgres_test.go`

  One container per package run (`TestMain`), reused by every test; each test
  truncates the app tables so it starts clean. If the container cannot start
  (Docker missing), the start error is recorded and **every** test `t.Skip`s
  rather than failing — this keeps the suite green on machines without Docker.

  ```go
  package postgres

  import (
  	"bytes"
  	"context"
  	"database/sql"
  	"errors"
  	"log"
  	"os"
  	"testing"
  	"time"

  	_ "github.com/jackc/pgx/v5/stdlib"
  	"github.com/testcontainers/testcontainers-go"
  	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
  	"github.com/testcontainers/testcontainers-go/wait"

  	"calendium/backend/internal/domain"
  	"calendium/backend/internal/migrate"
  	"calendium/backend/internal/port"
  	"calendium/backend/migrations"
  )

  // testKey is a deterministic 32-byte AES-256-GCM key for the token vault.
  var testKey = []byte("0123456789abcdef0123456789abcdef") // len == 32

  var (
  	sharedStore *Store
  	sharedDB    *sql.DB
  	// dockerErr != nil means the container could not start (Docker
  	// unavailable); every test t.Skip()s on it.
  	dockerErr error
  )

  func TestMain(m *testing.M) {
  	os.Exit(runSuite(m))
  }

  func runSuite(m *testing.M) int {
  	ctx := context.Background()

  	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
  		tcpostgres.WithDatabase("calendium_test"),
  		tcpostgres.WithUsername("test"),
  		tcpostgres.WithPassword("test"),
  		testcontainers.WithWaitStrategy(
  			wait.ForLog("database system is ready to accept connections").
  				WithOccurrence(2).
  				WithStartupTimeout(60*time.Second)),
  	)
  	if err != nil {
  		// Container start failure ~always means Docker is unavailable. Record
  		// it and run: the tests will skip themselves.
  		dockerErr = err
  		return m.Run()
  	}
  	defer func() { _ = ctr.Terminate(context.Background()) }()

  	// Past this point the container is up, so any error is a real defect in
  	// our migration/store wiring — fail loudly, do not skip.
  	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
  	if err != nil {
  		log.Fatalf("postgres test: connection string: %v", err)
  	}
  	db, err := sql.Open("pgx", dsn)
  	if err != nil {
  		log.Fatalf("postgres test: open db: %v", err)
  	}
  	defer db.Close()
  	if err := migrate.Apply(ctx, db, migrations.FS); err != nil {
  		log.Fatalf("postgres test: migrate: %v", err)
  	}
  	st := NewStore(db)
  	if err := st.SetTokenEncryptionKey(testKey); err != nil {
  		log.Fatalf("postgres test: token key: %v", err)
  	}
  	sharedStore, sharedDB = st, db
  	return m.Run()
  }

  // newTestStore returns the shared store + raw db, skipping when Docker is
  // unavailable and truncating every application table for isolation.
  // Migrations are applied once in TestMain and are NOT re-run here.
  func newTestStore(t *testing.T) (*Store, *sql.DB) {
  	t.Helper()
  	if dockerErr != nil {
  		t.Skipf("skipping postgres repo tests: Docker unavailable: %v", dockerErr)
  	}
  	truncateAll(t)
  	return sharedStore, sharedDB
  }

  // truncateAll wipes app data (CASCADE makes FK order irrelevant). The Better
  // Auth tables and schema_migrations are intentionally preserved.
  func truncateAll(t *testing.T) {
  	t.Helper()
  	_, err := sharedDB.Exec(`TRUNCATE
  		users, subscriptions, stripe_events, connected_accounts, oauth_states,
  		labels, threads, thread_labels, messages, attachments, drafts, snippets,
  		calendars, events, devices, sync_state RESTART IDENTITY CASCADE`)
  	if err != nil {
  		t.Fatalf("truncate: %v", err)
  	}
  }

  // --- seed helpers (satisfy the FK chain users → accounts → threads/calendars) ---

  func seedUser(t *testing.T, st *Store, id string) domain.User {
  	t.Helper()
  	u, err := st.Users().Upsert(context.Background(), domain.User{ID: id, Email: id + "@example.com"})
  	if err != nil {
  		t.Fatalf("seed user: %v", err)
  	}
  	return u
  }

  func seedAccount(t *testing.T, st *Store, userID string) domain.ConnectedAccount {
  	t.Helper()
  	a, err := st.Accounts().Create(context.Background(), domain.ConnectedAccount{
  		UserID:   userID,
  		Provider: domain.ProviderGoogle,
  		Email:    userID + "+google@example.com",
  		Status:   domain.AccountActive,
  		Scopes:   []string{"mail.read"},
  	})
  	if err != nil {
  		t.Fatalf("seed account: %v", err)
  	}
  	return a
  }

  func seedThread(t *testing.T, st *Store, accountID string, lastMsg time.Time) domain.Thread {
  	t.Helper()
  	th, err := st.Threads().Upsert(context.Background(), domain.Thread{
  		AccountID:        accountID,
  		ProviderThreadID: newID(),
  		Subject:          "hello",
  		Split:            domain.SplitImportant,
  		InInbox:          true,
  		Unread:           true,
  		MessageCount:     1,
  		LastMessageAt:    lastMsg.UTC().Truncate(time.Microsecond),
  	})
  	if err != nil {
  		t.Fatalf("seed thread: %v", err)
  	}
  	return th
  }

  func seedCalendar(t *testing.T, st *Store, accountID string) domain.Calendar {
  	t.Helper()
  	c, err := st.Calendars().Upsert(context.Background(), domain.Calendar{
  		AccountID:          accountID,
  		ProviderCalendarID: newID(),
  		Name:               "Primary",
  		IsPrimary:          true,
  		CanWrite:           true,
  	})
  	if err != nil {
  		t.Fatalf("seed calendar: %v", err)
  	}
  	return c
  }

  // ensure the imports above are all used even before every test file lands.
  var _ = bytes.Contains
  var _ = errors.Is
  var _ = port.TokenSet{}
  ```

  Run (confirms the harness compiles and either skips or connects):

  ```bash
  cd backend && go test ./internal/adapter/out/postgres/ -run TestMain -count=1 -v
  ```

  Expected: builds; with Docker running the container starts and the package’s
  (currently zero) tests pass; without Docker the run is a clean no-op/skip.
  Commit:

  ```bash
  git add backend/internal/adapter/out/postgres/postgres_test.go
  git commit -m "test(postgres): add testcontainers harness + seed helpers"
  ```

  > Remove the three `var _ =` guards once `account_test.go` (Step 3) lands and
  > references `bytes`, `errors`, and `port` for real.

- [ ] **Step 3: AccountRepo — Create+GetByID + encrypted token round-trip (representative, fully coded)** — `backend/internal/adapter/out/postgres/account_test.go`

  This is the flagship case: it proves the AES-256-GCM token vault never
  persists plaintext by reading the raw `bytea` columns.

  ```go
  package postgres

  import (
  	"bytes"
  	"context"
  	"errors"
  	"testing"
  	"time"

  	"calendium/backend/internal/domain"
  	"calendium/backend/internal/port"
  )

  func TestAccountRepoCreateGetAndEncryptedTokens(t *testing.T) {
  	st, db := newTestStore(t)
  	ctx := context.Background()
  	seedUser(t, st, "u1")

  	created, err := st.Accounts().Create(ctx, domain.ConnectedAccount{
  		UserID:   "u1",
  		Provider: domain.ProviderGoogle,
  		Email:    "me@gmail.com",
  		Status:   domain.AccountActive,
  		Scopes:   []string{"https://mail.google.com/"},
  	})
  	if err != nil {
  		t.Fatalf("Create: %v", err)
  	}
  	if created.ID == "" {
  		t.Fatal("Create did not assign an id")
  	}

  	got, err := st.Accounts().GetByID(ctx, created.ID)
  	if err != nil {
  		t.Fatalf("GetByID: %v", err)
  	}
  	if got.Email != "me@gmail.com" || got.Provider != domain.ProviderGoogle {
  		t.Fatalf("GetByID = %+v", got)
  	}
  	// scanAccount normalizes nil slices to empty (never JSON null over the wire).
  	if got.Scopes == nil || got.VIPSenders == nil {
  		t.Fatalf("nil slices must be normalized to empty: %+v", got)
  	}

  	// --- token vault round-trip ---
  	const access, refresh = "ya29.super-secret-access", "1//refresh-secret"
  	exp := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
  	if err := st.Accounts().SaveTokens(ctx, created.ID, port.TokenSet{
  		AccessToken:  access,
  		RefreshToken: refresh,
  		ExpiresAt:    exp,
  	}); err != nil {
  		t.Fatalf("SaveTokens: %v", err)
  	}
  	tok, err := st.Accounts().GetTokens(ctx, created.ID)
  	if err != nil {
  		t.Fatalf("GetTokens: %v", err)
  	}
  	if tok.AccessToken != access || tok.RefreshToken != refresh {
  		t.Fatalf("token round-trip mismatch: %+v", tok)
  	}
  	if !tok.ExpiresAt.Equal(exp) {
  		t.Fatalf("ExpiresAt = %v, want %v", tok.ExpiresAt, exp)
  	}

  	// --- ciphertext at rest: read the raw columns, assert no plaintext ---
  	var accessEnc, refreshEnc []byte
  	if err := db.QueryRowContext(ctx,
  		`SELECT access_token_enc, refresh_token_enc FROM connected_accounts WHERE id = $1`,
  		created.ID).Scan(&accessEnc, &refreshEnc); err != nil {
  		t.Fatalf("read raw columns: %v", err)
  	}
  	if len(accessEnc) == 0 || bytes.Contains(accessEnc, []byte(access)) {
  		t.Fatalf("access token stored in plaintext: %q", accessEnc)
  	}
  	if len(refreshEnc) == 0 || bytes.Contains(refreshEnc, []byte(refresh)) {
  		t.Fatalf("refresh token stored in plaintext: %q", refreshEnc)
  	}
  	// nonce-prefixed GCM ciphertext is strictly longer than the plaintext.
  	if len(accessEnc) <= len(access) {
  		t.Fatalf("ciphertext unexpectedly short: %d bytes", len(accessEnc))
  	}
  }

  func TestAccountRepoGetByIDMissing(t *testing.T) {
  	st, _ := newTestStore(t)
  	if _, err := st.Accounts().GetByID(context.Background(), "nope"); !errors.Is(err, domain.ErrNotFound) {
  		t.Fatalf("err = %v, want ErrNotFound", err)
  	}
  }
  ```

  Run:

  ```bash
  cd backend && go test ./internal/adapter/out/postgres/ -run 'TestAccountRepo' -count=1 -v
  ```

  Expected: PASS (with Docker) / SKIP (without). Then delete the three `var _ =`
  guards from `postgres_test.go` (now genuinely used), rebuild, and commit:

  ```bash
  cd backend && go test ./internal/adapter/out/postgres/ -count=1
  git add backend/internal/adapter/out/postgres/account_test.go backend/internal/adapter/out/postgres/postgres_test.go
  git commit -m "test(postgres): AccountRepo create/get + encrypted token round-trip"
  ```

  **Required cases (AccountRepo, in `account_test.go`):**
  - `accountRepo.Create` then `GetByID` — returns the row; `Scopes`/`VIPSenders` non-nil empty slices when unset. (Covered above.)
  - `accountRepo.SaveTokens`/`GetTokens` — plaintext round-trip; raw `access_token_enc`/`refresh_token_enc` columns contain no plaintext and are longer than the input. (Covered above.)
  - `accountRepo.GetByID` unknown id → `errors.Is(err, domain.ErrNotFound)`. (Covered above.)
  - `accountRepo.ListByUser` — seed two accounts for `u1` (distinct emails) + one for `u2`; returns exactly the two for `u1`, ordered by `created_at`.
  - `accountRepo.ListSyncable` — seed accounts with `Status` `active`, `syncing`, `reauth_required`, `disconnected`; returns only the `active` and `syncing` ones (the `connected_accounts_syncable_idx` predicate).
  - `accountRepo.Update` — change `Email`, `Status`→`domain.AccountReauthRequired`, `Scopes`, and `VIPSenders`; `GetByID` reflects all four (Update is the only writer of `vip_senders`; `Create` leaves it `'{}'`).
  - `accountRepo.Update` unknown id → `errors.Is(err, domain.ErrNotFound)` (via `mustAffect`).
  - `accountRepo.Delete` existing id → nil, then `GetByID` → `domain.ErrNotFound`; `Delete` unknown id → `domain.ErrNotFound`.
  - `accountRepo.SaveTokens` unknown id → `domain.ErrNotFound`.
  - `accountRepo.SaveTokens` with empty `AccessToken`/`RefreshToken` (`sealToken` maps `""`→NULL) then `GetTokens` → both fields `""`, zero `ExpiresAt` when not set.

- [ ] **Step 4: ThreadRepo pagination/cursor + CalendarRepo visibility (representative, fully coded)** — `backend/internal/adapter/out/postgres/thread_test.go` and `calendar_event_test.go`

  `thread_test.go`:

  ```go
  package postgres

  import (
  	"context"
  	"testing"
  	"time"

  	"calendium/backend/internal/domain"
  	"calendium/backend/internal/port"
  )

  func TestThreadRepoListPaginationCursor(t *testing.T) {
  	st, _ := newTestStore(t)
  	ctx := context.Background()
  	seedUser(t, st, "u1")
  	acct := seedAccount(t, st, "u1")

  	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
  	for i := 0; i < 5; i++ { // strictly increasing last_message_at
  		seedThread(t, st, acct.ID, base.Add(time.Duration(i)*time.Hour))
  	}

  	page1, err := st.Threads().List(ctx, port.ThreadQuery{UserID: "u1", Limit: 2})
  	if err != nil {
  		t.Fatalf("List page1: %v", err)
  	}
  	if len(page1.Items) != 2 {
  		t.Fatalf("page1 items = %d, want 2", len(page1.Items))
  	}
  	if page1.NextCursor == nil {
  		t.Fatal("page1 NextCursor = nil, want a cursor")
  	}
  	// keyset order is last_message_at DESC.
  	if !page1.Items[0].LastMessageAt.After(page1.Items[1].LastMessageAt) {
  		t.Fatal("page not ordered by last_message_at DESC")
  	}

  	page2, err := st.Threads().List(ctx, port.ThreadQuery{UserID: "u1", Limit: 2, Cursor: *page1.NextCursor})
  	if err != nil {
  		t.Fatalf("List page2: %v", err)
  	}
  	if len(page2.Items) != 2 {
  		t.Fatalf("page2 items = %d, want 2", len(page2.Items))
  	}
  	if page2.Items[0].ID == page1.Items[1].ID {
  		t.Fatal("cursor overlap: page2 repeats the last row of page1")
  	}

  	// final page: 1 remaining, no further cursor.
  	page3, err := st.Threads().List(ctx, port.ThreadQuery{UserID: "u1", Limit: 2, Cursor: *page2.NextCursor})
  	if err != nil {
  		t.Fatalf("List page3: %v", err)
  	}
  	if len(page3.Items) != 1 || page3.NextCursor != nil {
  		t.Fatalf("page3 = %d items, cursor=%v; want 1 item, nil cursor", len(page3.Items), page3.NextCursor)
  	}
  }

  func TestThreadRepoListRequiresUser(t *testing.T) {
  	st, _ := newTestStore(t)
  	if _, err := st.Threads().List(context.Background(), port.ThreadQuery{}); !errorsIsValidation(err) {
  		t.Fatalf("err = %v, want ErrValidation", err)
  	}
  }

  // errorsIsValidation is a tiny local shim so this file needn't import errors
  // twice; equivalent to errors.Is(err, domain.ErrValidation).
  func errorsIsValidation(err error) bool { return err != nil && isSentinel(err, domain.ErrValidation) }
  ```

  > Prefer `errors.Is` directly (import `errors`); the shim above is only to keep
  > the snippet self-contained. Use `if !errors.Is(err, domain.ErrValidation)` in
  > the real file and drop `errorsIsValidation`/`isSentinel`.

  `calendar_event_test.go` (visibility/color preservation — the CalendarRepo
  contract explicitly promises this):

  ```go
  package postgres

  import (
  	"context"
  	"testing"

  	"calendium/backend/internal/domain"
  )

  func TestCalendarRepoUpsertPreservesVisibilityAndColor(t *testing.T) {
  	st, _ := newTestStore(t)
  	ctx := context.Background()
  	seedUser(t, st, "u1")
  	acct := seedAccount(t, st, "u1")

  	const pid = "cal-provider-1"
  	first, err := st.Calendars().Upsert(ctx, domain.Calendar{
  		AccountID:          acct.ID,
  		ProviderCalendarID: pid,
  		Name:               "Work",
  		Color:              "#111111",
  		CanWrite:           true,
  	})
  	if err != nil {
  		t.Fatalf("Upsert first: %v", err)
  	}
  	if !first.IsVisible {
  		t.Fatal("new calendar must default to visible")
  	}

  	// User hides it and repaints it locally.
  	first.IsVisible = false
  	first.Color = "#abcdef"
  	if err := st.Calendars().Update(ctx, first); err != nil {
  		t.Fatalf("Update: %v", err)
  	}

  	// Provider re-sync upserts the same calendar with a new name and its own color.
  	resynced, err := st.Calendars().Upsert(ctx, domain.Calendar{
  		AccountID:          acct.ID,
  		ProviderCalendarID: pid,
  		Name:               "Work (renamed)",
  		Color:              "#000000", // provider color must NOT overwrite local pref
  		CanWrite:           true,
  	})
  	if err != nil {
  		t.Fatalf("Upsert resync: %v", err)
  	}
  	if resynced.Name != "Work (renamed)" {
  		t.Fatalf("name not updated on resync: %q", resynced.Name)
  	}
  	if resynced.IsVisible {
  		t.Fatal("resync clobbered the local is_visible preference")
  	}
  	if resynced.Color != "#abcdef" {
  		t.Fatalf("resync clobbered local color: got %q, want #abcdef", resynced.Color)
  	}
  }
  ```

  Run + commit:

  ```bash
  cd backend && go test ./internal/adapter/out/postgres/ -run 'TestThreadRepo|TestCalendarRepo' -count=1 -v
  git add backend/internal/adapter/out/postgres/thread_test.go backend/internal/adapter/out/postgres/calendar_event_test.go
  git commit -m "test(postgres): thread pagination/cursor + calendar visibility preservation"
  ```

- [ ] **Step 5: Enumerated required cases across ALL remaining repos**

  Group into the files listed under **Files**. Every bullet names the exact
  method under test + scenario + expected result/sentinel; all are mandatory.
  Use `context.Background()`, seed the FK parents with the helpers, inject times
  as UTC values truncated to microseconds (`t.UTC().Truncate(time.Microsecond)`
  — Postgres `timestamptz` is microsecond precision), and compare times with
  `.Equal`. Compare sentinels with `errors.Is` against the `domain.Err*` values.

  **UserRepo** (`user_billing_test.go`) — no seeding needed:
  - `userRepo.Upsert` insert → returns row with the given `ID`/`Email`; `GetByID` returns the same.
  - `userRepo.Upsert` on an existing id with a **new email but nil Name/AvatarURL** → email updated, previously-stored `Name`/`AvatarURL` preserved (the `COALESCE(EXCLUDED.name, users.name)` clause). Seed the first upsert with a non-nil `Name`.
  - `userRepo.Upsert` on an existing id with a non-nil `Name` → `Name` overwritten.
  - `userRepo.GetByID` unknown id → `domain.ErrNotFound`.

  **SubscriptionRepo** (`user_billing_test.go`) — seed the user first (FK `subscriptions.user_id → users.id`):
  - `subscriptionRepo.Upsert` with a zero-value `Status`/`Plan`/`PriceUSD` → persisted defaults `domain.SubscriptionNone`, `domain.PlanAnnual`, `domain.PriceUSDAnnual` (50); verify via `GetByUserID`.
  - `subscriptionRepo.Upsert` with `Status=domain.SubscriptionActive`, a `StripeCustomerID`, `CurrentPeriodEnd`, `LastEventAt` → `GetByUserID` returns them; `CurrentPeriodEnd`/`LastEventAt` round-trip with `.Equal`.
  - `subscriptionRepo.GetByStripeCustomerID` with the customer id set above → returns the row; unknown customer id → `domain.ErrNotFound`.
  - `subscriptionRepo.Upsert` twice on the same user with a second call passing empty `StripeCustomerID` → the previously-stored customer id is preserved (`COALESCE(EXCLUDED.stripe_customer_id, ...)`).
  - `subscriptionRepo.GetByUserID` unknown user → `domain.ErrNotFound`.

  **StripeEventRepo** (`misc_repo_test.go`) — no seeding:
  - `stripeEventRepo.Record("evt_1","invoice.paid")` → `firstTime == true`.
  - second `Record("evt_1", ...)` (any type) → `firstTime == false`, no error (dedup via `ON CONFLICT (id) DO NOTHING`).

  **OAuthStateRepo** (`misc_repo_test.go`) — seed user (FK `oauth_states.user_id → users.id`):
  - `oauthStateRepo.Create` then `Consume(state)` → returns the `port.OAuthState` with matching `UserID`, `Provider`, `RedirectURL`, and the round-tripped `CodeVerifier`.
  - second `Consume(state)` → `domain.ErrNotFound` (atomic `DELETE ... RETURNING` — replay/forgery guard).
  - `Consume` on a never-created state → `domain.ErrNotFound`.
  - `oauthStateRepo.Create` with empty `CodeVerifier` (maps to NULL) then `Consume` → `CodeVerifier == ""`.

  **SyncStateRepo** (`misc_repo_test.go`) — seed user + account:
  - `syncStateRepo.Save(port.SyncState{AccountID, Resource:"mail", Cursor:"c1"})` then `Get(accountID,"mail")` → `Cursor == "c1"`, non-zero `UpdatedAt`.
  - `Save` again for the same `(accountID,"mail")` with `Cursor:"c2"` → `Get` returns `"c2"` (upsert `ON CONFLICT (account_id, resource)`).
  - `syncStateRepo.Get` for an unknown `(accountID,resource)` → `domain.ErrNotFound`.
  - `syncStateRepo.DeleteByAccount(accountID)` after saving two resources (`"mail"`, `"calendars"`) → both `Get`s then return `domain.ErrNotFound`; `DeleteByAccount` on an account with no rows → nil error.

  **DeviceRepo** (`misc_repo_test.go`) — seed user (FK `devices.user_id → users.id`):
  - `deviceRepo.Upsert` new device (`Platform=domain.PlatformIOS`, `Token:"tok-A"`) → returns assigned `ID`, non-zero `CreatedAt`; `GetByID` returns it.
  - `deviceRepo.Upsert` again for the **same `(userID,"tok-A")`** but a fresh `d.ID=""` and `Platform=domain.PlatformMacOS` → returns the **original** id (`RETURNING id` on `ON CONFLICT (user_id, token)`), platform updated to `macos`; `ListByUser` has exactly one row.
  - `deviceRepo.ListByUser` with two distinct tokens → two rows ordered by `created_at`.
  - `deviceRepo.Delete` existing → nil then `GetByID` → `domain.ErrNotFound`; `Delete` unknown id → `domain.ErrNotFound`; `GetByID` unknown → `domain.ErrNotFound`.

  **ThreadRepo** (remainder, `thread_test.go`) — seed user + account:
  - `threadRepo.Upsert` new (with `ProviderThreadID`) then `GetByProviderID(accountID, pid)` and `GetByID(id)` → same row; `Participants`/`LabelIDs` non-nil empty when unset.
  - `threadRepo.Upsert` twice on the same `(accountID, providerThreadID)` after `MarkOpened` set `opened_at` → the second upsert preserves `opened_at` (`COALESCE(threads.opened_at, EXCLUDED.opened_at)`).
  - `threadRepo.MarkOpened(id)` — call once, read `opened_at` (non-nil) + `unread==false`; call again → `opened_at` **unchanged** (idempotent `COALESCE(opened_at, now())`), still `unread==false`. `MarkOpened` unknown id → `domain.ErrNotFound`.
  - `threadRepo.List` default view — seed one `InInbox:true` and one `InInbox:false` thread → default query (`View:""`) returns only the in-inbox one (`t.in_inbox = true`).
  - `threadRepo.List` `View=domain.ThreadViewStarred` → returns only `Starred:true` threads regardless of inbox membership; `View=domain.ThreadViewSnoozed` → returns only threads with `SnoozedUntil` in the **future**.
  - `threadRepo.List` `AccountID`/`Split` filters — seed threads across two accounts and two splits; filtered lists return only matching rows.
  - `threadRepo.List` `Query` filter — thread subject `"Quarterly report"`; `Query:"quarterly"` returns it, `Query:"zzz"` returns none.
  - `threadRepo.List` with a malformed `Cursor:"!!not-base64!!"` → `domain.ErrValidation` (via `decodeThreadCursor`).
  - `threadRepo.SetLabels` — seed two labels via `labelRepo.Upsert`; `SetLabels(threadID, [l1,l2])` then `GetByID` → `LabelIDs` contains both (json_agg ordered by `label_id`); `SetLabels(threadID, nil)` → `LabelIDs` empty.
  - `threadRepo.Search(userID, "quarterly", 0)` → returns the matching thread (default limit 20); non-matching query → empty slice (never nil).
  - `threadRepo.ListSnoozeDue(now, 10)` — thread with `SnoozedUntil = now-1h` is returned; a `now+1h` one is not. Then `ClearSnooze(id)` → `GetByID` has `SnoozedUntil == nil` and `Unread == true`. `ClearSnooze` unknown id → `domain.ErrNotFound`.
  - `threadRepo.ListRemindersDue(now, 10)` — thread with `RemindAt = now-1h` returned; then `ClearReminder(id)` → `RemindAt == nil`, `Unread == true`. `ClearReminder` unknown id → `domain.ErrNotFound`.
  - `threadRepo.AppendSentMessage(id, sentAt)` — seed thread `MessageCount:1`, `LastMessageAt:t0`; `AppendSentMessage(id, t1>t0)` → `GetByID` has `MessageCount==2`, `LastMessageAt==t1`. A subsequent `AppendSentMessage(id, tEarlier<t1)` → `MessageCount==3` but `LastMessageAt` stays `t1` (`GREATEST`). Unknown id → `domain.ErrNotFound`.

  **MessageRepo** (`message_test.go`) — seed user + account + thread:
  - `messageRepo.Upsert` new (with `ProviderMessageID`, a `From`, `To`, one `Attachment`) → `GetByID` returns it with `Attachments` populated; `To`/`Cc`/`Bcc` non-nil empty when unset.
  - `messageRepo.GetByProviderID(accountID, pmid)` → same row as `GetByID`.
  - `messageRepo.Upsert` twice on the same `(accountID, providerMessageID)` with the second call carrying **fewer** attachments → `replaceAttachments` drops the removed ones (re-read attachment count matches the second write).
  - `messageRepo.ListByThread(threadID)` — two messages with `SentAt` `t0<t1` → returned ordered by `sent_at, id`; each carries its own attachments (batched `attachmentsFor`).
  - `messageRepo.GetByID` unknown id → `domain.ErrNotFound`.

  **DraftRepo** (`draft_test.go`) — seed user + account:
  - `draftRepo.Create` then `GetByID` → same row; `To`/`Cc`/`Bcc` non-nil empty when unset; `SendAttempts==0`, `LastError==nil`.
  - `draftRepo.ListByUser(userID)` — two drafts on the user's account → returned ordered by `updated_at DESC` (join through `connected_accounts`).
  - `draftRepo.ListScheduledDue(now, 10)` — draft with `ScheduledAt = now-1m` returned; a `now+1h` one and a `nil`-scheduled one are not.
  - `draftRepo.ClaimScheduled(id)` on a due draft → `claimed==true` (clears `scheduled_at`); a **second** `ClaimScheduled(id)` → `claimed==false` (already claimed — the double-send guard). `ClaimScheduled` on an unscheduled draft → `false`, nil error.
  - `draftRepo.RecordSendFailure(id, &next, "smtp 550")` → `GetByID` has `SendAttempts==1`, `LastError=="smtp 550"`, `ScheduledAt==next`; a second `RecordSendFailure(id, nil, "again")` → `SendAttempts==2`, `ScheduledAt==nil` (dead-letter). Unknown id → `domain.ErrNotFound`.
  - `draftRepo.Update` on a draft that previously failed (`SendAttempts>0`, `LastError` set) → `send_attempts` reset to 0 and `last_error` cleared (a user edit resets the retry budget); verify via `GetByID`. `Update`/`Delete` unknown id → `domain.ErrNotFound`.

  **SnippetRepo** (`snippet_label_test.go`) — seed user:
  - `snippetRepo.Create` then `GetByID` → same row; `Shortcut==nil` when unset.
  - `snippetRepo.ListByUser(userID)` — two snippets → ordered by `name`.
  - `snippetRepo.Update` — change `Name`, `Shortcut` (non-nil), `BodyHTML`, `UsageCount` → `GetByID` reflects all; `Update` unknown id → `domain.ErrNotFound`.
  - `snippetRepo.Delete` existing → nil then `GetByID` → `domain.ErrNotFound`; `Delete` unknown → `domain.ErrNotFound`.

  **LabelRepo** (`snippet_label_test.go`) — seed user + account:
  - `labelRepo.Upsert` new (with `ProviderLabelID`, `Kind` unset) → returns assigned `ID`; persisted `Kind==domain.LabelKindUser` (default). `ListByAccount` returns it.
  - `labelRepo.Upsert` again on the same `(accountID, providerLabelID)` with a new `Name`/`Color`/`Kind=domain.LabelKindSystem` → same id, fields updated (`ON CONFLICT (account_id, provider_label_id)`); `ListByAccount` still one row.
  - `labelRepo.ListByAccount` — two labels with names `"Zeta"`,`"Alpha"` → returned ordered by `name` (`Alpha` first).

  **CalendarRepo** (remainder, `calendar_event_test.go`) — seed user + account:
  - `calendarRepo.Upsert` new with empty `Color`/`TimeZone` → defaults `"#6366f1"` and `"UTC"` applied; `IsVisible==true`.
  - `calendarRepo.GetByID` unknown → `domain.ErrNotFound`.
  - `calendarRepo.ListByUser(userID)` and `ListByAccount(accountID)` — two calendars (one `IsPrimary`) → both list them ordered `is_primary DESC, name` (primary first).
  - `calendarRepo.Update` unknown id → `domain.ErrNotFound`.
  - (visibility/color preservation covered in Step 4.)

  **EventRepo** (`calendar_event_test.go`) — seed user + account + calendar:
  - `eventRepo.Upsert` new (with `ProviderEventID`, `Attendees`, `Conferencing`, `ReminderMinutes`) then `GetByID`/`GetByProviderID(calendarID, peid)` → same row; empty defaults `Status==domain.EventConfirmed`, `Visibility==domain.VisibilityDefault`; `Attendees`/`ReminderMinutes` non-nil empty when unset; `Conferencing` round-trips (non-nil pointer) and is `nil` when omitted.
  - `eventRepo.ListInRange(userID, from, to, nil)` — overlap semantics: seed events (A fully inside `[from,to)`, B straddling `from`, C entirely before, D entirely after) → returns A and B only (`start_at < to AND end_at > from`). C and D excluded.
  - `eventRepo.ListInRange` visibility gating — seed a second calendar with `IsVisible=false` (via `Update`) holding an in-range event; with `calendarIDs=nil` it is **excluded** (`c.is_visible`); passing explicit `calendarIDs=[hiddenCalID]` **includes** it (explicit selection overrides visibility).
  - `eventRepo.Search(userID, "standup", 0)` — event titled `"Team standup"` → returned (default limit 20); non-matching → empty slice.
  - `eventRepo.Delete(id)` existing → nil then `GetByID` → `domain.ErrNotFound`; `Delete` unknown → `domain.ErrNotFound`.
  - `eventRepo.DeleteByProviderID(calendarID, peid)` existing → nil then `GetByProviderID` → `domain.ErrNotFound`; unknown pair → `domain.ErrNotFound`.

  After each file compiles and passes, commit it individually, e.g.:

  ```bash
  cd backend && go test ./internal/adapter/out/postgres/ -run 'TestMessageRepo' -count=1 -v
  git add backend/internal/adapter/out/postgres/message_test.go
  git commit -m "test(postgres): MessageRepo upsert/get/list + attachment replace"
  ```

  Repeat the run/commit cycle for `draft_test.go`, `snippet_label_test.go`,
  `calendar_event_test.go`, `user_billing_test.go`, and `misc_repo_test.go`.
  Final gate for the whole task:

  ```bash
  cd backend && go test ./internal/adapter/out/postgres/ -count=1
  ```

  Expected: PASS with Docker available; the whole package cleanly SKIPs when
  Docker is absent.

**Real-behavior notes for implementers (verified against the source):**
- `connected_accounts` has `UNIQUE (user_id, provider, email)` — give each seeded account for the same user a distinct email, or `Create` errors.
- `accountRepo.Create` does **not** write `vip_senders` (defaults to `'{}'`); only `Update` sets it. So the "list/tokens" cases assert `[]` after `Create`, and the VIP round-trip goes through `Update`.
- `threadRepo.List` with an empty `View` appends `t.in_inbox = true` **and** (unless `IncludeSnoozed`) `t.snoozed_until IS NULL`; seed `InInbox:true` and no snooze for pagination fixtures or they vanish from the default listing.
- `calendarRepo.Upsert` `RETURNING id, color, is_visible` and its `ON CONFLICT` clause deliberately omit `color`/`is_visible`, so provider re-sync preserves the local preference — the Step 4 test locks this in.
- Tokens are nonce-prefixed AES-256-GCM ciphertext; the raw `bytea` column is strictly longer than the plaintext and contains none of it.
- Postgres `timestamptz` is microsecond precision: build fixture times with `.UTC().Truncate(time.Microsecond)` and compare with `time.Time.Equal`, never `==`. pgx returns times in UTC.
- `mustAffect` turns a zero-row `UPDATE`/`DELETE` into `domain.ErrNotFound`; every "unknown id" mutation case relies on this.

---

### Task 16: config.FromEnv characterization tests

**Files:**
- Create/Test: `backend/internal/config/config_test.go` (package `config`, white-box).
- Under test (read-only): `backend/internal/config/config.go` — `func FromEnv() (Config, error)`.

**Interfaces:**
- Consumes: nothing from other tasks. Pure stdlib (`testing`, `t.Setenv`, `encoding/hex`, `bytes`, `errors`, `time`). Does NOT use the service-package fakes — `config` is a leaf package.
- Produces: `TestFromEnv` (table-driven, the bulk), `TestFromEnvJoinsMultipleErrors`, plus the unexported helpers `clearEnv(t)`, `withBase(map[string]string) map[string]string`, and the consts `validKeyHex`. These helper/const names are local to this file only.

Behavior facts pinned from the real `FromEnv` (assert these, not error strings):
- Required: `DATABASE_URL` non-empty; `TOKEN_ENCRYPTION_KEY` must `hex.DecodeString` to exactly 32 bytes. On ANY collected error, `FromEnv` returns the **zero** `Config{}` and a non-nil `errors.Join(...)`.
- `HTTP.Addr`: `HTTP_ADDR` if set, else `:$PORT` if `PORT` set, else `:8080`.
- `Auth.JWKSURL` defaults to `${BETTER_AUTH_URL trimmed of trailing "/"}/api/auth/jwks` only when `AUTH_JWKS_URL` empty AND `BETTER_AUTH_URL` non-empty; `Auth.Issuer` defaults to the trimmed `BETTER_AUTH_URL` when `AUTH_ISSUER` empty (so `Issuer==""` when neither set).
- `OpenRouter.Model` defaults to `"openrouter/auto"`.
- `Mail.UndoSendGrace` defaults to `15*time.Second`; `UNDO_SEND_SECONDS` must parse as a **non-negative** int (`0` allowed, negative or non-int errors).
- `Instance.SelfHosted` via `strconv.ParseBool` (accepts `1/t/T/TRUE/true/True/0/f/false/...`, rejects e.g. `"yes"`); default `false`.
- `Instance.Name` default `"Calendium"`; `Instance.PublicWebURL` = `PUBLIC_WEB_URL` else `APP_URL`.
- `OAuth.AllowedRedirectURIs` = the 4 built-in defaults (`http://localhost`, `https://localhost`, `http://127.0.0.1`, `calendium://`) with the comma-split, `TrimSpace`d, non-empty entries of `OAUTH_ALLOWED_REDIRECT_URIS` appended.
- `HTTP.CORSAllowedOrigins` = only the comma-split/`TrimSpace`d/non-empty entries of `CORS_ALLOWED_ORIGINS` (nil when unset — the localhost/Wails browser defaults live in the HTTP layer, NOT in `Config`).

Hermetic note: `FromEnv` reads `os.Getenv` directly, so the host/CI environment could already have `DATABASE_URL` etc. set. Every subtest first calls `clearEnv(t)` which `t.Setenv`s **every** key `FromEnv` reads to `""` (for these checks `""` is indistinguishable from unset), then layers the case's overrides. `t.Setenv` restores prior values at subtest end, so runs are hermetic and must NOT call `t.Parallel()`.

- [ ] **Step 1: Write the failing test** — create `backend/internal/config/config_test.go`:

```go
package config

import (
	"bytes"
	"encoding/hex"
	"testing"
	"time"
)

// validKeyHex is 64 hex chars => 32 bytes, a valid AES-256-GCM key.
const validKeyHex = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

// configEnvKeys is every environment variable FromEnv reads. clearEnv blanks
// them all so a test starts from a known-empty environment regardless of the
// host/CI env. For FromEnv's os.Getenv("")==unset checks, "" == unset.
var configEnvKeys = []string{
	"DATABASE_URL", "BETTER_AUTH_URL", "AUTH_JWKS_URL", "AUTH_ISSUER",
	"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET",
	"APPLE_CLIENT_ID", "APPLE_CLIENT_SECRET",
	"MS_CLIENT_ID", "MS_CLIENT_SECRET",
	"STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET", "STRIPE_PRICE_ID_ANNUAL",
	"APNS_KEY_ID", "APNS_TEAM_ID", "APNS_KEY_P8",
	"FCM_SERVICE_ACCOUNT_JSON", "VAPID_PUBLIC_KEY", "VAPID_PRIVATE_KEY",
	"OPENROUTER_API_KEY", "OPENROUTER_MODEL",
	"HTTP_ADDR", "PORT", "TOKEN_ENCRYPTION_KEY", "UNDO_SEND_SECONDS",
	"INSTANCE_NAME", "PUBLIC_WEB_URL", "APP_URL", "PUBLIC_API_URL",
	"SELF_HOSTED", "OAUTH_ALLOWED_REDIRECT_URIS", "CORS_ALLOWED_ORIGINS",
}

// clearEnv blanks every config env var for the duration of the test; t.Setenv
// restores prior values at test end. Must not be combined with t.Parallel.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range configEnvKeys {
		t.Setenv(k, "")
	}
}

// withBase returns a copy of the two required-valid vars merged with extra, so
// a success-path case can override a single knob and still boot.
func withBase(extra map[string]string) map[string]string {
	m := map[string]string{
		"DATABASE_URL":         "postgres://localhost:5432/calendium",
		"TOKEN_ENCRYPTION_KEY": validKeyHex,
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func containsStr(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}

func TestFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
		check   func(t *testing.T, c Config)
	}{
		// ---- required-var errors ----
		{
			name:    "missing DATABASE_URL errors",
			env:     map[string]string{"TOKEN_ENCRYPTION_KEY": validKeyHex},
			wantErr: true,
		},
		{
			name:    "missing TOKEN_ENCRYPTION_KEY errors",
			env:     map[string]string{"DATABASE_URL": "postgres://localhost/db"},
			wantErr: true,
		},
		{
			name:    "TOKEN_ENCRYPTION_KEY not hex errors",
			env:     withBase(map[string]string{"TOKEN_ENCRYPTION_KEY": "zzzz-not-hex-zzzz"}),
			wantErr: true,
		},
		{
			name: "TOKEN_ENCRYPTION_KEY valid hex but wrong length errors",
			// 32 hex chars = 16 bytes, not 32.
			env:     withBase(map[string]string{"TOKEN_ENCRYPTION_KEY": "00112233445566778899aabbccddeeff"}),
			wantErr: true,
		},
		// ---- crypto happy path ----
		{
			name: "valid key decodes to 32 bytes",
			env:  withBase(nil),
			check: func(t *testing.T, c Config) {
				want, _ := hex.DecodeString(validKeyHex)
				if len(c.Crypto.TokenEncryptionKey) != 32 {
					t.Fatalf("key len = %d, want 32", len(c.Crypto.TokenEncryptionKey))
				}
				if !bytes.Equal(c.Crypto.TokenEncryptionKey, want) {
					t.Fatalf("key bytes = %x, want %x", c.Crypto.TokenEncryptionKey, want)
				}
			},
		},
		// ---- HTTP.Addr ----
		{
			name:  "HTTP_ADDR default :8080",
			env:   withBase(nil),
			check: func(t *testing.T, c Config) { assertEq(t, "HTTP.Addr", c.HTTP.Addr, ":8080") },
		},
		{
			name:  "PORT fallback when HTTP_ADDR unset",
			env:   withBase(map[string]string{"PORT": "9090"}),
			check: func(t *testing.T, c Config) { assertEq(t, "HTTP.Addr", c.HTTP.Addr, ":9090") },
		},
		{
			name:  "HTTP_ADDR wins over PORT",
			env:   withBase(map[string]string{"HTTP_ADDR": "127.0.0.1:1234", "PORT": "9090"}),
			check: func(t *testing.T, c Config) { assertEq(t, "HTTP.Addr", c.HTTP.Addr, "127.0.0.1:1234") },
		},
		// ---- UNDO_SEND_SECONDS ----
		{
			name:  "UNDO_SEND_SECONDS default 15s",
			env:   withBase(nil),
			check: func(t *testing.T, c Config) { assertDur(t, "UndoSendGrace", c.Mail.UndoSendGrace, 15*time.Second) },
		},
		{
			name:  "UNDO_SEND_SECONDS custom",
			env:   withBase(map[string]string{"UNDO_SEND_SECONDS": "30"}),
			check: func(t *testing.T, c Config) { assertDur(t, "UndoSendGrace", c.Mail.UndoSendGrace, 30*time.Second) },
		},
		{
			name:  "UNDO_SEND_SECONDS zero allowed",
			env:   withBase(map[string]string{"UNDO_SEND_SECONDS": "0"}),
			check: func(t *testing.T, c Config) { assertDur(t, "UndoSendGrace", c.Mail.UndoSendGrace, 0) },
		},
		{
			name:    "UNDO_SEND_SECONDS negative errors",
			env:     withBase(map[string]string{"UNDO_SEND_SECONDS": "-1"}),
			wantErr: true,
		},
		{
			name:    "UNDO_SEND_SECONDS non-int errors",
			env:     withBase(map[string]string{"UNDO_SEND_SECONDS": "abc"}),
			wantErr: true,
		},
		// ---- SELF_HOSTED ----
		{
			name:  "SELF_HOSTED default false",
			env:   withBase(nil),
			check: func(t *testing.T, c Config) { assertBool(t, "SelfHosted", c.Instance.SelfHosted, false) },
		},
		{
			name:  "SELF_HOSTED true",
			env:   withBase(map[string]string{"SELF_HOSTED": "true"}),
			check: func(t *testing.T, c Config) { assertBool(t, "SelfHosted", c.Instance.SelfHosted, true) },
		},
		{
			name:  "SELF_HOSTED false",
			env:   withBase(map[string]string{"SELF_HOSTED": "false"}),
			check: func(t *testing.T, c Config) { assertBool(t, "SelfHosted", c.Instance.SelfHosted, false) },
		},
		{
			name:    "SELF_HOSTED invalid errors",
			env:     withBase(map[string]string{"SELF_HOSTED": "yes"}),
			wantErr: true,
		},
		// ---- OpenRouter model ----
		{
			name:  "OpenRouter model default",
			env:   withBase(nil),
			check: func(t *testing.T, c Config) { assertEq(t, "OpenRouter.Model", c.OpenRouter.Model, "openrouter/auto") },
		},
		{
			name:  "OpenRouter model override",
			env:   withBase(map[string]string{"OPENROUTER_MODEL": "anthropic/claude-sonnet"}),
			check: func(t *testing.T, c Config) { assertEq(t, "OpenRouter.Model", c.OpenRouter.Model, "anthropic/claude-sonnet") },
		},
		// ---- Auth derivation ----
		{
			name: "JWKS + Issuer derived from BETTER_AUTH_URL",
			env:  withBase(map[string]string{"BETTER_AUTH_URL": "https://app.calendium.com"}),
			check: func(t *testing.T, c Config) {
				assertEq(t, "JWKSURL", c.Auth.JWKSURL, "https://app.calendium.com/api/auth/jwks")
				assertEq(t, "Issuer", c.Auth.Issuer, "https://app.calendium.com")
			},
		},
		{
			name: "BETTER_AUTH_URL trailing slash trimmed before derivation",
			env:  withBase(map[string]string{"BETTER_AUTH_URL": "https://app.calendium.com/"}),
			check: func(t *testing.T, c Config) {
				assertEq(t, "JWKSURL", c.Auth.JWKSURL, "https://app.calendium.com/api/auth/jwks")
				assertEq(t, "Issuer", c.Auth.Issuer, "https://app.calendium.com")
			},
		},
		{
			name: "explicit JWKS + Issuer win over derivation",
			env: withBase(map[string]string{
				"BETTER_AUTH_URL": "https://app.calendium.com",
				"AUTH_JWKS_URL":   "https://auth.example.com/jwks",
				"AUTH_ISSUER":     "https://issuer.example.com",
			}),
			check: func(t *testing.T, c Config) {
				assertEq(t, "JWKSURL", c.Auth.JWKSURL, "https://auth.example.com/jwks")
				assertEq(t, "Issuer", c.Auth.Issuer, "https://issuer.example.com")
			},
		},
		{
			name: "empty BETTER_AUTH_URL leaves Issuer empty and JWKS underived",
			env:  withBase(nil),
			check: func(t *testing.T, c Config) {
				assertEq(t, "JWKSURL", c.Auth.JWKSURL, "")
				assertEq(t, "Issuer", c.Auth.Issuer, "")
			},
		},
		// ---- OAuth redirect allowlist ----
		{
			name: "OAUTH_ALLOWED_REDIRECT_URIS appended to the 4 defaults",
			env:  withBase(map[string]string{"OAUTH_ALLOWED_REDIRECT_URIS": "https://app.example.com, https://web.example.com"}),
			check: func(t *testing.T, c Config) {
				got := c.OAuth.AllowedRedirectURIs
				if len(got) != 6 {
					t.Fatalf("AllowedRedirectURIs len = %d, want 6 (%v)", len(got), got)
				}
				for _, want := range []string{
					"http://localhost", "https://localhost", "http://127.0.0.1", "calendium://",
					"https://app.example.com", "https://web.example.com",
				} {
					if !containsStr(got, want) {
						t.Errorf("AllowedRedirectURIs missing %q (%v)", want, got)
					}
				}
			},
		},
		{
			name: "OAUTH_ALLOWED_REDIRECT_URIS empty leaves only defaults",
			env:  withBase(nil),
			check: func(t *testing.T, c Config) {
				if len(c.OAuth.AllowedRedirectURIs) != 4 {
					t.Fatalf("AllowedRedirectURIs len = %d, want 4 (%v)", len(c.OAuth.AllowedRedirectURIs), c.OAuth.AllowedRedirectURIs)
				}
			},
		},
		// ---- CORS ----
		{
			name: "CORS_ALLOWED_ORIGINS comma-split and trimmed",
			env:  withBase(map[string]string{"CORS_ALLOWED_ORIGINS": "https://a.com, https://b.com ,, https://c.com"}),
			check: func(t *testing.T, c Config) {
				got := c.HTTP.CORSAllowedOrigins
				if len(got) != 3 {
					t.Fatalf("CORSAllowedOrigins = %v, want 3 non-empty entries", got)
				}
				for i, want := range []string{"https://a.com", "https://b.com", "https://c.com"} {
					assertEq(t, "CORSAllowedOrigins["+want+"]", got[i], want)
				}
			},
		},
		{
			name: "CORS_ALLOWED_ORIGINS unset yields nil",
			env:  withBase(nil),
			check: func(t *testing.T, c Config) {
				if c.HTTP.CORSAllowedOrigins != nil {
					t.Fatalf("CORSAllowedOrigins = %v, want nil", c.HTTP.CORSAllowedOrigins)
				}
			},
		},
		// ---- Instance name + public web url ----
		{
			name:  "INSTANCE_NAME default Calendium",
			env:   withBase(nil),
			check: func(t *testing.T, c Config) { assertEq(t, "Instance.Name", c.Instance.Name, "Calendium") },
		},
		{
			name:  "INSTANCE_NAME override",
			env:   withBase(map[string]string{"INSTANCE_NAME": "Acme Mail"}),
			check: func(t *testing.T, c Config) { assertEq(t, "Instance.Name", c.Instance.Name, "Acme Mail") },
		},
		{
			name: "PUBLIC_WEB_URL wins over APP_URL",
			env: withBase(map[string]string{
				"PUBLIC_WEB_URL": "https://web.example.com",
				"APP_URL":        "https://app.example.com",
			}),
			check: func(t *testing.T, c Config) { assertEq(t, "PublicWebURL", c.Instance.PublicWebURL, "https://web.example.com") },
		},
		{
			name:  "APP_URL is the fallback when PUBLIC_WEB_URL unset",
			env:   withBase(map[string]string{"APP_URL": "https://app.example.com"}),
			check: func(t *testing.T, c Config) { assertEq(t, "PublicWebURL", c.Instance.PublicWebURL, "https://app.example.com") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			c, err := FromEnv()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("FromEnv() error = nil, want non-nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("FromEnv() unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, c)
			}
		})
	}
}

// TestFromEnvJoinsMultipleErrors asserts that when several required vars are
// bad at once FromEnv still returns a single non-nil error (errors.Join) and a
// zero-value Config (no partial config leaks out on the error path). We assert
// presence, not the joined string.
func TestFromEnvJoinsMultipleErrors(t *testing.T) {
	clearEnv(t)
	// DATABASE_URL missing AND TOKEN_ENCRYPTION_KEY invalid AND
	// UNDO_SEND_SECONDS non-int => three collected errors.
	t.Setenv("TOKEN_ENCRYPTION_KEY", "nothex")
	t.Setenv("UNDO_SEND_SECONDS", "abc")

	c, err := FromEnv()
	if err == nil {
		t.Fatalf("FromEnv() error = nil, want non-nil (joined)")
	}
	if !bytes.Equal(c.Crypto.TokenEncryptionKey, nil) || c.HTTP.Addr != "" || c.Instance.Name != "" {
		t.Fatalf("FromEnv() returned a non-zero Config on error path: %+v", c)
	}
}

func assertEq(t *testing.T, field, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", field, got, want)
	}
}

func assertDur(t *testing.T, field string, got, want time.Duration) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", field, got, want)
	}
}

func assertBool(t *testing.T, field string, got, want bool) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", field, got, want)
	}
}
```

- [ ] **Step 2: Run** — `cd backend && go test ./internal/config/ -run TestFromEnv -v` → **Expected PASS** (all subtests). Full package: `cd backend && go test ./internal/config/`. If a `wantErr` case unexpectedly passes, or a `check` fails, decide wrong-test vs. real bug per the cycle: (a) re-read the exact branch in `config.go`; (b) if the test asserts something `config.go` genuinely does not do, fix the test; (c) if `config.go` misbehaves (e.g. accepts a negative `UNDO_SEND_SECONDS`), record it and fix production in a **separate** commit.

- [ ] **Step 3: Commit**
```
cd /Users/guilherme/Dev/pessoal/calendium
git add backend/internal/config/config_test.go
git commit -m "test(config): characterize FromEnv env parsing and validation

Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

**Required cases** (every bullet is mandatory coverage of `config.FromEnv`; all included in `TestFromEnv` above unless noted):
- `FromEnv` + `DATABASE_URL` unset → non-nil error.
- `FromEnv` + `TOKEN_ENCRYPTION_KEY` unset → non-nil error.
- `FromEnv` + `TOKEN_ENCRYPTION_KEY` non-hex → non-nil error.
- `FromEnv` + `TOKEN_ENCRYPTION_KEY` valid hex but 16 bytes (32 chars) → non-nil error.
- `FromEnv` + valid 64-hex key → `Crypto.TokenEncryptionKey` len 32 and byte-equal to `hex.DecodeString(validKeyHex)`.
- `FromEnv` + neither `HTTP_ADDR` nor `PORT` → `HTTP.Addr == ":8080"`.
- `FromEnv` + `PORT=9090`, no `HTTP_ADDR` → `HTTP.Addr == ":9090"`.
- `FromEnv` + `HTTP_ADDR=127.0.0.1:1234` and `PORT=9090` → `HTTP.Addr == "127.0.0.1:1234"`.
- `FromEnv` + no `UNDO_SEND_SECONDS` → `Mail.UndoSendGrace == 15*time.Second`.
- `FromEnv` + `UNDO_SEND_SECONDS=30` → `Mail.UndoSendGrace == 30*time.Second`.
- `FromEnv` + `UNDO_SEND_SECONDS=0` → `Mail.UndoSendGrace == 0`.
- `FromEnv` + `UNDO_SEND_SECONDS=-1` → non-nil error.
- `FromEnv` + `UNDO_SEND_SECONDS=abc` → non-nil error.
- `FromEnv` + no `SELF_HOSTED` → `Instance.SelfHosted == false`.
- `FromEnv` + `SELF_HOSTED=true` → `Instance.SelfHosted == true`.
- `FromEnv` + `SELF_HOSTED=false` → `Instance.SelfHosted == false`.
- `FromEnv` + `SELF_HOSTED=yes` → non-nil error.
- `FromEnv` + no `OPENROUTER_MODEL` → `OpenRouter.Model == "openrouter/auto"`.
- `FromEnv` + `OPENROUTER_MODEL=anthropic/claude-sonnet` → that exact value.
- `FromEnv` + `BETTER_AUTH_URL=https://app.calendium.com` → `Auth.JWKSURL == ".../api/auth/jwks"`, `Auth.Issuer == "https://app.calendium.com"`.
- `FromEnv` + `BETTER_AUTH_URL` with trailing `/` → same trimmed derivation.
- `FromEnv` + explicit `AUTH_JWKS_URL` and `AUTH_ISSUER` → those win over derivation.
- `FromEnv` + no `BETTER_AUTH_URL` → `Auth.JWKSURL == ""` and `Auth.Issuer == ""`.
- `FromEnv` + `OAUTH_ALLOWED_REDIRECT_URIS="https://app.example.com, https://web.example.com"` → `OAuth.AllowedRedirectURIs` len 6, contains the 4 defaults plus both trimmed entries.
- `FromEnv` + no `OAUTH_ALLOWED_REDIRECT_URIS` → `OAuth.AllowedRedirectURIs` len 4 (defaults only).
- `FromEnv` + `CORS_ALLOWED_ORIGINS="https://a.com, https://b.com ,, https://c.com"` → 3 non-empty trimmed entries (empty splits dropped).
- `FromEnv` + no `CORS_ALLOWED_ORIGINS` → `HTTP.CORSAllowedOrigins == nil`.
- `FromEnv` + no `INSTANCE_NAME` → `Instance.Name == "Calendium"`.
- `FromEnv` + `INSTANCE_NAME="Acme Mail"` → that exact value.
- `FromEnv` + `PUBLIC_WEB_URL` and `APP_URL` both set → `Instance.PublicWebURL == PUBLIC_WEB_URL`.
- `FromEnv` + only `APP_URL` set → `Instance.PublicWebURL == APP_URL`.
- `TestFromEnvJoinsMultipleErrors`: three simultaneous bad vars → single non-nil joined error AND zero-value `Config` returned (no partial leak).

---

### Task 17: Root test scripts, Makefile target, and CI workflow

**Files:**
- Modify: `/Users/guilherme/Dev/pessoal/calendium/package.json` — add the `test`, `test:shared`, `test:web`, `test:mobile`, `test:desktop`, `test:e2e` scripts (keep the existing `test:api`).
- Modify: `/Users/guilherme/Dev/pessoal/calendium/Makefile` — add a `test-api` target (and a coverage variant); extend `.PHONY`.
- Create: `/Users/guilherme/Dev/pessoal/calendium/.github/workflows/test.yml` (the `.github/workflows/` directory does not exist yet — create it).

**Interfaces:**
- Consumes: Task 16's `backend/internal/config/config_test.go` (and every other backend test task) via `go test ./...`. No code interfaces.
- Produces: runnable `bun run test`, `bun run test:api`, `make test-api`, and a GitHub Actions `test` workflow. These are the aggregation entry points every other task's tests flow through.

Facts pinned: `backend/go.mod` is module `calendium/backend`, `go 1.26`, with `backend/go.sum` present (real dep `github.com/jackc/pgx/v5`). Root `package.json` is a Bun workspace; the only test script today is `"test:api": "cd backend && go test ./..."`. The Makefile currently has no test target (its `.PHONY` lists only the self-host/db helpers). CI runners (`ubuntu-latest`) ship Docker, so postgres testcontainers integration tests run in CI.

- [ ] **Step 1: Add root `package.json` test scripts.** Replace the current `scripts` block so it reads exactly (only the test-related keys are new; the TS ones are passing stubs until each sub-project grows a real suite):

```json
  "scripts": {
    "dev:mobile": "bun run --cwd apps/mobile dev",
    "dev:web": "bun run --cwd apps/web dev",
    "dev:desktop": "cd apps/desktop && wails dev",
    "dev:api": "cd backend && go run ./cmd/api",
    "build:web": "bun run --cwd apps/web build",
    "build:desktop": "cd apps/desktop && wails build",
    "build:api": "cd backend && go build ./...",
    "test": "bun run test:api && bun run test:shared && bun run test:web && bun run test:mobile && bun run test:desktop",
    "test:api": "cd backend && go test ./...",
    "test:shared": "echo \"[test:shared] no unit tests yet — TODO in packages/shared sub-project\" && exit 0",
    "test:web": "echo \"[test:web] no unit tests yet — TODO in apps/web sub-project\" && exit 0",
    "test:mobile": "echo \"[test:mobile] no unit tests yet — TODO in apps/mobile sub-project\" && exit 0",
    "test:desktop": "echo \"[test:desktop] no unit tests yet — TODO in apps/desktop sub-project\" && exit 0",
    "test:e2e": "echo \"[test:e2e] no e2e suite yet — TODO once web+api compose is wired\" && exit 0",
    "typecheck": "bun run --cwd apps/web typecheck && bun run --cwd packages/shared typecheck",
    "format": "prettier --write \"**/*.{ts,tsx,js,jsx,json,md,css}\""
  },
```

(`test:e2e` is intentionally excluded from the aggregate `test` script — it is a heavier target run on demand, and it stays a passing stub until the compose-driven suite exists.)

- [ ] **Step 2: Add the Makefile `test-api` target.** Append this block to the Makefile and add the two target names to the `.PHONY` line:

```make
test-api: ## Run the Go backend test suite (needs Docker for testcontainers)
	cd backend && go test ./...

test-api-cover: ## Run the backend suite printing per-package coverage
	cd backend && go test -cover ./...
```

Edit the existing `.PHONY` line from:
```make
.PHONY: help self-host-up self-host-down self-host-logs gen-secret db-backup db-restore
```
to:
```make
.PHONY: help self-host-up self-host-down self-host-logs gen-secret db-backup db-restore test-api test-api-cover
```
Note: the old `test-db` helper is superseded — the postgres layer's integration tests spin up their own throwaway Postgres via testcontainers (Docker), so `test-api` (which runs `go test ./...` with Docker available) fully covers what a separate DB target used to. No standalone `test-db` target is added.

- [ ] **Step 3: Create the CI workflow.** Write `/Users/guilherme/Dev/pessoal/calendium/.github/workflows/test.yml`:

```yaml
name: test

on:
  push:
    branches: [main]
  pull_request:

# Cancel superseded runs on the same ref to save CI minutes.
concurrency:
  group: test-${{ github.ref }}
  cancel-in-progress: true

jobs:
  backend:
    name: Go backend (build + test)
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: backend
    steps:
      - name: Checkout
        uses: actions/checkout@v4

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: "1.26"
          cache-dependency-path: backend/go.sum

      - name: Build
        run: go build ./...

      - name: Vet
        run: go vet ./...

      - name: Test (race + coverage)
        # ubuntu-latest ships Docker, so the postgres testcontainers
        # integration tests run here too. -cover prints per-package coverage;
        # a hard threshold gate (e.g. fail under N%) can be layered on later.
        run: go test -race -covermode=atomic -coverprofile=coverage.out ./...

      - name: Coverage summary
        run: go tool cover -func=coverage.out | tail -n 1

  # ---------------------------------------------------------------------------
  # Placeholder for the TypeScript sub-projects (packages/shared, apps/web,
  # apps/mobile, apps/desktop/frontend). Uncomment and fill in per-workspace as
  # each grows a real unit-test suite. Kept here so the matrix shape is agreed
  # up-front and reviewers know where TS coverage will land.
  #
  # frontend:
  #   name: TS ${{ matrix.workspace }}
  #   runs-on: ubuntu-latest
  #   strategy:
  #     fail-fast: false
  #     matrix:
  #       workspace: [shared, web, mobile, desktop]
  #   steps:
  #     - uses: actions/checkout@v4
  #     - uses: oven-sh/setup-bun@v2
  #       with:
  #         bun-version: latest
  #     - run: bun install --frozen-lockfile
  #     - run: bun run test:${{ matrix.workspace }}
```

- [ ] **Step 4: Verify locally** (CI itself only runs on push/PR):
  - `cd /Users/guilherme/Dev/pessoal/calendium && bun run test:api` → **Expected PASS** (drives `go test ./...` for the whole backend).
  - `cd /Users/guilherme/Dev/pessoal/calendium && bun run test` → **Expected PASS** (api real tests + four passing TS stubs).
  - `cd /Users/guilherme/Dev/pessoal/calendium && make test-api` → **Expected PASS** (identical `go test ./...`).
  - `cd /Users/guilherme/Dev/pessoal/calendium && make test-api-cover` → prints a per-package coverage column, exit 0.
  - Lint the workflow YAML (optional): `cd /Users/guilherme/Dev/pessoal/calendium && python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/test.yml'))"` → no output, exit 0.

- [ ] **Step 5: Commit**
```
cd /Users/guilherme/Dev/pessoal/calendium
git add package.json Makefile .github/workflows/test.yml
git commit -m "ci: add root test scripts, make test-api target, and Go CI workflow

Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

**Required outcomes** (mandatory):
- `package.json` exposes `test`, `test:api`, `test:shared`, `test:web`, `test:mobile`, `test:desktop`, `test:e2e`; `test:api` is unchanged (`cd backend && go test ./...`); `test` aggregates api + the four unit stubs; every TS stub exits 0.
- `Makefile` has `test-api` (`cd backend && go test ./...`) and `test-api-cover` targets, both listed in `.PHONY`; no `test-db` target (superseded by testcontainers).
- `.github/workflows/test.yml` exists with a `backend` job on `ubuntu-latest` running `go build ./...`, `go vet ./...`, and `go test -race -covermode=atomic -coverprofile=coverage.out ./...` inside `backend/`, plus a coverage-summary step, and a commented TS matrix job for future sub-projects. Coverage is printed (no hard threshold gate — deferred to a follow-up).

---
