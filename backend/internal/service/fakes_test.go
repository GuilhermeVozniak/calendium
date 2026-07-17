package service

import (
	"context"
	"sort"
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

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }
func (c *fakeClock) Set(t time.Time)         { c.now = t }

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

	// accounts, when set, scopes ListInboxBefore by resolving each
	// thread->account->user, mirroring how fakeCalendarRepo/fakeDraftRepo
	// scope by ownership. Left nil, ListInboxBefore ignores userID (existing
	// behavior for tests that don't wire it up).
	accounts *fakeAccountRepo

	// programmable
	listPage     domain.Page[domain.Thread]
	searchResult []domain.Thread
	searchErr    error
	snoozeDue    []domain.Thread
	remindersDue []domain.Thread

	// recording
	markOpened           int
	lastQuery            port.ThreadQuery
	clearedSnooze        []string
	clearedReminder      []string
	lastSetLabelsID      string
	lastSetLabelIDs      []string
	appendSentCalls      int
	lastAppendSentID     string
	lastAppendSentAt     time.Time
	listInboxBeforeCalls int
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

func (r *fakeThreadRepo) ListInboxBefore(_ context.Context, userID string, before time.Time, limit int) ([]domain.Thread, error) {
	r.listInboxBeforeCalls++
	var threads []domain.Thread
	for _, t := range r.byID {
		if r.accounts != nil {
			a, ok := r.accounts.byID[t.AccountID]
			if !ok || a.UserID != userID {
				continue
			}
		}
		// Filter: in_inbox, not snoozed, and last_message_at before cutoff
		if t.InInbox && t.SnoozedUntil == nil && t.LastMessageAt.Before(before) {
			threads = append(threads, t)
		}
	}
	// Sort by LastMessageAt ascending (oldest first)
	sort.Slice(threads, func(i, j int) bool {
		return threads[i].LastMessageAt.Before(threads[j].LastMessageAt)
	})
	// Cap at limit
	if limit > 0 && len(threads) > limit {
		threads = threads[:limit]
	}
	return threads, nil
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
// ID is assigned one via newID(). ListByUser scopes by ownership when the
// optional accounts pointer is set (Label carries no UserID); left nil, it
// returns every stored label (existing single-account-repo tests keep
// working without wiring accounts).
type fakeLabelRepo struct {
	byID     map[string]domain.Label
	order    []string
	accounts *fakeAccountRepo
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

func (r *fakeLabelRepo) GetByID(_ context.Context, id string) (domain.Label, error) {
	l, ok := r.byID[id]
	if !ok {
		return domain.Label{}, domain.ErrNotFound
	}
	return l, nil
}

func (r *fakeLabelRepo) ListByUser(_ context.Context, userID string) ([]domain.Label, error) {
	out := []domain.Label{}
	for _, id := range r.order {
		l := r.byID[id]
		if r.accounts != nil {
			a, ok := r.accounts.byID[l.AccountID]
			if !ok || a.UserID != userID {
				continue
			}
		}
		out = append(out, l)
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

func newCalendarRepo() *fakeCalendarRepo {
	return &fakeCalendarRepo{byID: map[string]domain.Calendar{}}
}

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
// DeleteByProviderID defaults to a silent no-op when nothing matches (existing
// behavior, unchanged); setting deleteByProviderErr makes every call return
// that error instead, so a test can drive both the domain.ErrNotFound swallow
// branch in syncEvents and a genuine propagated failure.
type fakeEventRepo struct {
	byID  map[string]domain.Event
	order []string

	searchResult        []domain.Event
	searchErr           error
	deleteByProviderErr error
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
	if r.deleteByProviderErr != nil {
		return r.deleteByProviderErr
	}
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
//
// SyncMail defaults to always returning the single programmable syncPage
// (existing behavior, unchanged). Programming syncPages instead switches it to
// serve one page per call, advancing an internal index and clamping to the
// last entry once exhausted -- the last page must set HasMore=false so the
// real sync loop terminates. Every cursor SyncMail was called with is
// recorded in syncMailCursors, in call order, so a test can assert the loop
// threads NextCursor into the next call.
type fakeMailProvider struct {
	// programmable
	syncPage        port.MailSyncPage
	syncPages       []port.MailSyncPage // when non-empty, overrides syncPage: one page served per call
	syncErr         error
	sentResult      port.SentMessage
	sendErr         error
	modifyLabelsErr error

	// recording
	sent               []port.OutgoingMessage
	syncMailCalls      int
	syncPageIdx        int      // next index into syncPages to serve (clamped to the last entry)
	syncMailCursors    []string // cursor arg on every SyncMail call, in call order
	modifyLabelsCalls  int
	lastModifyToken    string
	lastModifyThreadID string
	lastModifyAdd      []string
	lastModifyRemove   []string
}

func newMailProvider() *fakeMailProvider { return &fakeMailProvider{} }

func (p *fakeMailProvider) SyncMail(_ context.Context, accessToken, cursor string) (port.MailSyncPage, error) {
	p.syncMailCalls++
	p.syncMailCursors = append(p.syncMailCursors, cursor)
	if len(p.syncPages) > 0 {
		i := p.syncPageIdx
		if i >= len(p.syncPages) {
			i = len(p.syncPages) - 1
		}
		p.syncPageIdx++
		return p.syncPages[i], p.syncErr
	}
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

// --- unsubscribe gateway -----------------------------------------------------

// fakeUnsubscriber records every one-click POST target and serves a
// programmable error.
type fakeUnsubscriber struct {
	calls []string
	err   error
}

func (u *fakeUnsubscriber) PostOneClick(_ context.Context, url string) error {
	u.calls = append(u.calls, url)
	return u.err
}

var _ port.UnsubscribeGateway = (*fakeUnsubscriber)(nil)

// --- prefs repo --------------------------------------------------------------

type fakePrefsRepo struct{ byUser map[string]domain.UserPrefs }

func newPrefsRepo() *fakePrefsRepo { return &fakePrefsRepo{byUser: map[string]domain.UserPrefs{}} }

func (r *fakePrefsRepo) Get(_ context.Context, userID string) (domain.UserPrefs, error) {
	return r.byUser[userID], nil
}

func (r *fakePrefsRepo) Save(_ context.Context, userID string, p domain.UserPrefs) error {
	r.byUser[userID] = p
	return nil
}

var _ port.PrefsRepo = (*fakePrefsRepo)(nil)
