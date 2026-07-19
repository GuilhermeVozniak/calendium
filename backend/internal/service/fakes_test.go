package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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

	// recording (Search)
	searchGotUserID string
	searchGotQuery  string
	searchGotLimit  int

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
	r.searchGotUserID, r.searchGotQuery, r.searchGotLimit = userID, query, limit
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

func (r *fakeThreadRepo) SetSummary(_ context.Context, threadID, summary string, at time.Time) error {
	t, ok := r.byID[threadID]
	if !ok {
		return domain.ErrNotFound
	}
	t.Summary = summary
	r.byID[threadID] = t
	return nil
}

func (r *fakeThreadRepo) SetInstantReplies(_ context.Context, threadID string, replies []string, at time.Time) error {
	t, ok := r.byID[threadID]
	if !ok {
		return domain.ErrNotFound
	}
	t.InstantReplies = replies
	atCopy := at
	t.InstantRepliesUpdatedAt = &atCopy
	r.byID[threadID] = t
	return nil
}

func (r *fakeThreadRepo) SetReminderIfUnset(_ context.Context, threadID string, remindAt time.Time) error {
	t, ok := r.byID[threadID]
	if !ok || t.RemindAt != nil {
		return nil
	}
	t.RemindAt = &remindAt
	r.byID[threadID] = t
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

	// opensPage/opensErr configure ListOpens; opensCalls captures every
	// query for assertions (entitlement/clamping/passthrough).
	opensPage  domain.Page[domain.OpenEvent]
	opensErr   error
	opensCalls []port.OpensQuery

	// histograms configures OpenHourHistogram keyed by recipient email;
	// histogramErr forces an error return.
	histograms   map[string][24]int
	histogramErr error

	attachmentByID          map[string]fakeAttachmentRecord
	searchAttachmentsResult domain.Page[domain.AttachmentHit]
	lastSearchQuery         port.AttachmentQuery
	contactSummaryResult    domain.ContactSummary
	contactSummaryErr       error
	lastContactEmail        string
}

func newMessageRepo() *fakeMessageRepo {
	return &fakeMessageRepo{byID: map[string]domain.Message{}, attachmentByID: map[string]fakeAttachmentRecord{}, histograms: map[string][24]int{}}
}

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

// ListSentByAccount returns messages from accountID whose From address
// matches accountEmail (case-insensitive), newest-first, capped at limit.
func (r *fakeMessageRepo) ListSentByAccount(_ context.Context, accountID, accountEmail string, limit int) ([]domain.Message, error) {
	out := []domain.Message{}
	for i := len(r.order) - 1; i >= 0; i-- {
		m, ok := r.byID[r.order[i]]
		if !ok || m.AccountID != accountID || !strings.EqualFold(m.From.Email, accountEmail) {
			continue
		}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].SentAt.After(out[j].SentAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ListOpens and OpenHourHistogram are configurable via opensPage/opensErr
// and histograms/histogramErr (task 8). SearchAttachments, GetAttachment,
// and ContactSummary remain M2.5 stubs (real Postgres queries land in later
// tasks); they return zero values so the package compiles.
func (r *fakeMessageRepo) ListOpens(_ context.Context, q port.OpensQuery) (domain.Page[domain.OpenEvent], error) {
	r.opensCalls = append(r.opensCalls, q)
	if r.opensErr != nil {
		return domain.Page[domain.OpenEvent]{}, r.opensErr
	}
	if r.opensPage.Items == nil {
		return domain.Page[domain.OpenEvent]{Items: []domain.OpenEvent{}}, nil
	}
	return r.opensPage, nil
}

func (r *fakeMessageRepo) OpenHourHistogram(_ context.Context, _, recipientEmail string) ([24]int, error) {
	if r.histogramErr != nil {
		return [24]int{}, r.histogramErr
	}
	return r.histograms[recipientEmail], nil
}

func (r *fakeMessageRepo) SearchAttachments(_ context.Context, q port.AttachmentQuery) (domain.Page[domain.AttachmentHit], error) {
	r.lastSearchQuery = q
	return r.searchAttachmentsResult, nil
}

func (r *fakeMessageRepo) GetAttachment(_ context.Context, attachmentID string) (domain.Attachment, string, error) {
	rec, ok := r.attachmentByID[attachmentID]
	if !ok {
		return domain.Attachment{}, "", domain.ErrNotFound
	}
	return rec.att, rec.messageID, nil
}

func (r *fakeMessageRepo) ContactSummary(_ context.Context, userID, email string) (domain.ContactSummary, error) {
	r.lastContactEmail = email
	if r.contactSummaryErr != nil {
		return domain.ContactSummary{}, r.contactSummaryErr
	}
	return r.contactSummaryResult, nil
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

// GetAiGeneratedByThread returns the newest AI-generated draft on threadID
// (insertion order, last wins), mirroring the postgres adapter's "newest
// wins" semantics.
func (r *fakeDraftRepo) GetAiGeneratedByThread(_ context.Context, threadID string) (domain.Draft, error) {
	for i := len(r.order) - 1; i >= 0; i-- {
		d, ok := r.byID[r.order[i]]
		if ok && d.AiGenerated && d.ThreadID != nil && *d.ThreadID == threadID {
			return d, nil
		}
	}
	return domain.Draft{}, domain.ErrNotFound
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
		// Personal snippets only — mirrors the SQL repo's team_id IS NULL.
		if s, ok := r.byID[id]; ok && s.UserID == userID && s.TeamID == nil {
			out = append(out, s)
		}
	}
	return out, nil
}

func (r *fakeSnippetRepo) ListByTeams(_ context.Context, teamIDs []string) ([]domain.Snippet, error) {
	out := []domain.Snippet{}
	for _, id := range r.order {
		s, ok := r.byID[id]
		if !ok || s.TeamID == nil {
			continue
		}
		for _, tid := range teamIDs {
			if *s.TeamID == tid {
				out = append(out, s)
				break
			}
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

	// calendars/accounts, when BOTH set, scope ListInRange by resolving each
	// event → calendar → account → user (mirrors the SQL join; needed by the
	// Task 14 team-busy tests where two users' mirrors coexist). Left nil,
	// ListInRange ignores userID (existing behavior for older tests).
	calendars *fakeCalendarRepo
	accounts  *fakeAccountRepo

	searchResult        []domain.Event
	searchErr           error
	deleteByProviderErr error
	// upsertErr, when set, is returned by Upsert instead of storing the
	// event — simulates a persistence failure between provider event
	// creation and the local mirror write inside Book/ConfirmPoll's tx.
	upsertErr error
}

func newEventRepo() *fakeEventRepo { return &fakeEventRepo{byID: map[string]domain.Event{}} }

func (r *fakeEventRepo) Upsert(_ context.Context, e domain.Event) (domain.Event, error) {
	if r.upsertErr != nil {
		return domain.Event{}, r.upsertErr
	}
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
		if r.calendars != nil && r.accounts != nil {
			c, ok := r.calendars.byID[e.CalendarID]
			if !ok {
				continue
			}
			a, ok := r.accounts.byID[c.AccountID]
			if !ok || a.UserID != userID {
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
	// fetchAttachmentErr overrides FetchAttachment's default
	// domain.ErrNotImplemented return; nil keeps the default.
	fetchAttachmentErr error

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

	fetchAttachmentData      []byte
	fetchAttachmentMimeType  string
	fetchAttachmentCalls     int
	lastFetchAttachmentToken string
	lastFetchMessageID       string // providerMessageID arg on the last FetchAttachment call
	lastFetchAttachmentID    string // providerAttachmentID arg on the last FetchAttachment call
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

// FetchAttachment is an M2.5 stub (real provider fetch lands in a later
// task); it returns domain.ErrNotImplemented by default, consistent with the
// package-level errNotImplemented used by the other M2.5 stubs, so tests can
// errors.Is against a shared sentinel rather than a bare nil-nil result.
// fetchAttachmentErr lets a test override the returned error.
func (p *fakeMailProvider) FetchAttachment(_ context.Context, accessToken, providerMessageID, providerAttachmentID string) ([]byte, string, error) {
	p.fetchAttachmentCalls++
	p.lastFetchAttachmentToken = accessToken
	p.lastFetchMessageID = providerMessageID
	p.lastFetchAttachmentID = providerAttachmentID
	if p.fetchAttachmentErr != nil {
		return nil, "", p.fetchAttachmentErr
	}
	if p.fetchAttachmentData != nil || p.fetchAttachmentMimeType != "" {
		return p.fetchAttachmentData, p.fetchAttachmentMimeType, nil
	}
	return nil, "", domain.ErrNotImplemented
}

var _ port.MailProvider = (*fakeMailProvider)(nil)

// --- calendar provider -------------------------------------------------------

// fakeCalendarProvider serves programmable calendars/sync-page/created/updated
// events, records each RSVP response and the last create/update/delete args.
// FreeBusy serves a programmable per-email busy map (freeBusyErr injects a
// gateway failure); emails absent from freeBusyResult mirror a provider that
// could not resolve that address.
type fakeCalendarProvider struct {
	// programmable
	calendars      []domain.Calendar
	syncPage       port.CalendarSyncPage
	createdEvent   domain.Event
	updatedEvent   domain.Event
	freeBusyResult map[string][]domain.BusyInterval

	syncCalendarsErr error
	syncEventsErr    error
	createErr        error
	updateErr        error
	deleteErr        error
	rsvpErr          error
	freeBusyErr      error

	// recording
	rsvpCalls            []domain.RsvpStatus
	lastCreateCalendarID string
	lastCreateInput      domain.EventInput
	lastUpdateEventID    string
	lastUpdatePatch      domain.EventPatch
	lastDeleteEventID    string
	lastFreeBusyEmails   []string
	lastFreeBusyFrom     time.Time
	lastFreeBusyTo       time.Time
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

func (p *fakeCalendarProvider) FreeBusy(_ context.Context, accessToken string, emails []string, from, to time.Time) (map[string][]domain.BusyInterval, error) {
	p.lastFreeBusyEmails = emails
	p.lastFreeBusyFrom, p.lastFreeBusyTo = from, to
	return p.freeBusyResult, p.freeBusyErr
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
// CompleteJSON unmarshals the scripted jsonOut string into the caller's out;
// jsonErr, when set, short-circuits before decoding (parity with a gateway
// error from the real adapter).
type fakeAI struct {
	text       string
	model      string
	err        error
	jsonOut    string
	jsonModel  string
	jsonErr    error
	lastSystem string
	lastUser   string
}

func newAI() *fakeAI { return &fakeAI{} }

func (a *fakeAI) Complete(_ context.Context, system, user string) (string, string, error) {
	a.lastSystem, a.lastUser = system, user
	return a.text, a.model, a.err
}

func (a *fakeAI) CompleteJSON(_ context.Context, system, user string, out any) (string, error) {
	a.lastSystem, a.lastUser = system, user
	if a.jsonErr != nil {
		return "", a.jsonErr
	}
	if err := json.Unmarshal([]byte(a.jsonOut), out); err != nil {
		return "", fmt.Errorf("%w: fakeAI: decode structured output: %w", domain.ErrAIOutput, err)
	}
	return a.jsonModel, nil
}

var _ port.AI = (*fakeAI)(nil)

// --- calendar service (driving) ----------------------------------------------

// fakeCalendarService is a minimal port.CalendarService double for
// AIJobService tests exercising runAutoDraft's scheduling path: only
// ListCalendars and Availability carry meaningful behavior (programmable via
// calendars/slots/availErr); every other method is unused by this task's
// handlers and returns a zero value.
type fakeCalendarService struct {
	calendars []domain.Calendar
	slots     []domain.AvailabilitySlot
	availErr  error

	lastAvailFrom time.Time
	lastAvailTo   time.Time
	lastAvailDur  time.Duration
	availCalls    int
}

func newCalendarService() *fakeCalendarService { return &fakeCalendarService{} }

func (c *fakeCalendarService) ListCalendars(_ context.Context, _ string) ([]domain.Calendar, error) {
	return c.calendars, nil
}

func (c *fakeCalendarService) UpdateCalendar(_ context.Context, _, _ string, _ port.CalendarPatch) (domain.Calendar, error) {
	return domain.Calendar{}, nil
}

func (c *fakeCalendarService) ListEvents(_ context.Context, _ string, _, _ time.Time, _ []string) ([]domain.Event, error) {
	return nil, nil
}

func (c *fakeCalendarService) CreateEvent(_ context.Context, _ string, _ domain.EventInput) (domain.Event, error) {
	return domain.Event{}, nil
}

func (c *fakeCalendarService) UpdateEvent(_ context.Context, _, _ string, _ domain.EventPatch) (domain.Event, error) {
	return domain.Event{}, nil
}

func (c *fakeCalendarService) DeleteEvent(_ context.Context, _, _ string) error { return nil }

func (c *fakeCalendarService) RSVP(_ context.Context, _, _ string, _ domain.RsvpStatus) (domain.Event, error) {
	return domain.Event{}, nil
}

func (c *fakeCalendarService) Availability(_ context.Context, _ string, from, to time.Time, dur time.Duration) ([]domain.AvailabilitySlot, error) {
	c.availCalls++
	c.lastAvailFrom, c.lastAvailTo, c.lastAvailDur = from, to, dur
	return c.slots, c.availErr
}

func (c *fakeCalendarService) TeamAvailability(_ context.Context, _, _ string, _, _ time.Time) ([]port.MemberAvailability, error) {
	return nil, nil
}

func (c *fakeCalendarService) ListEventTemplates(_ context.Context, _ string) ([]domain.EventTemplate, error) {
	return nil, nil
}

func (c *fakeCalendarService) CreateEventTemplate(_ context.Context, _ string, _ domain.EventTemplateInput) (domain.EventTemplate, error) {
	return domain.EventTemplate{}, nil
}

func (c *fakeCalendarService) UpdateEventTemplate(_ context.Context, _, _ string, _ domain.EventTemplateInput) (domain.EventTemplate, error) {
	return domain.EventTemplate{}, nil
}

func (c *fakeCalendarService) DeleteEventTemplate(_ context.Context, _, _ string) error { return nil }

func (c *fakeCalendarService) UseEventTemplate(_ context.Context, _, _ string) error { return nil }

func (c *fakeCalendarService) ListCalendarSets(_ context.Context, _ string) ([]domain.CalendarSet, error) {
	return nil, nil
}

func (c *fakeCalendarService) CreateCalendarSet(_ context.Context, _ string, _ domain.CalendarSetInput) (domain.CalendarSet, error) {
	return domain.CalendarSet{}, nil
}

func (c *fakeCalendarService) UpdateCalendarSet(_ context.Context, _, _ string, _ domain.CalendarSetInput) (domain.CalendarSet, error) {
	return domain.CalendarSet{}, nil
}

func (c *fakeCalendarService) DeleteCalendarSet(_ context.Context, _, _ string) error { return nil }

var _ port.CalendarService = (*fakeCalendarService)(nil)

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

type fakeUserPreferencesRepo struct {
	byUser map[string]port.UserPreferences
}

func newUserPreferencesRepo() *fakeUserPreferencesRepo {
	return &fakeUserPreferencesRepo{byUser: map[string]port.UserPreferences{}}
}

func (r *fakeUserPreferencesRepo) Get(_ context.Context, userID string) (port.UserPreferences, error) {
	if p, ok := r.byUser[userID]; ok {
		return p, nil
	}
	return port.UserPreferences{Theme: port.DefaultTheme}, nil
}

func (r *fakeUserPreferencesRepo) Put(_ context.Context, userID string, p port.UserPreferences) error {
	r.byUser[userID] = p
	return nil
}

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

// --- event template repo ----------------------------------------------------

type fakeEventTemplateRepo struct {
	byID   map[string]domain.EventTemplate
	order  []string
	owners map[string]string // id -> userID
}

func newEventTemplateRepo() *fakeEventTemplateRepo {
	return &fakeEventTemplateRepo{
		byID:   map[string]domain.EventTemplate{},
		owners: map[string]string{},
	}
}

func (r *fakeEventTemplateRepo) Create(_ context.Context, userID string, t domain.EventTemplate) (domain.EventTemplate, error) {
	if t.ID == "" {
		t.ID = newID()
	}
	r.byID[t.ID] = t
	r.owners[t.ID] = userID
	r.order = append(r.order, t.ID)
	return t, nil
}

func (r *fakeEventTemplateRepo) GetByID(_ context.Context, id string) (domain.EventTemplate, string, error) {
	t, ok := r.byID[id]
	if !ok {
		return domain.EventTemplate{}, "", domain.ErrNotFound
	}
	userID := r.owners[id]
	return t, userID, nil
}

func (r *fakeEventTemplateRepo) ListByUser(_ context.Context, userID string) ([]domain.EventTemplate, error) {
	out := []domain.EventTemplate{}
	for _, id := range r.order {
		if t, ok := r.byID[id]; ok && r.owners[id] == userID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (r *fakeEventTemplateRepo) Update(_ context.Context, t domain.EventTemplate) error {
	r.byID[t.ID] = t
	return nil
}

func (r *fakeEventTemplateRepo) IncrementUsage(_ context.Context, id string) error {
	if t, ok := r.byID[id]; ok {
		t.UsageCount++
		r.byID[id] = t
	}
	return nil
}

func (r *fakeEventTemplateRepo) Delete(_ context.Context, id string) error {
	delete(r.byID, id)
	delete(r.owners, id)
	return nil
}

var _ port.EventTemplateRepo = (*fakeEventTemplateRepo)(nil)

// --- calendar set repo -------------------------------------------------------

type fakeCalendarSetRepo struct {
	byID   map[string]domain.CalendarSet
	order  []string
	owners map[string]string // id -> userID
}

func newCalendarSetRepo() *fakeCalendarSetRepo {
	return &fakeCalendarSetRepo{
		byID:   map[string]domain.CalendarSet{},
		owners: map[string]string{},
	}
}

func (r *fakeCalendarSetRepo) Create(_ context.Context, userID string, s domain.CalendarSet) (domain.CalendarSet, error) {
	if s.ID == "" {
		s.ID = newID()
	}
	r.byID[s.ID] = s
	r.owners[s.ID] = userID
	r.order = append(r.order, s.ID)
	return s, nil
}

func (r *fakeCalendarSetRepo) GetByID(_ context.Context, id string) (domain.CalendarSet, string, error) {
	s, ok := r.byID[id]
	if !ok {
		return domain.CalendarSet{}, "", domain.ErrNotFound
	}
	userID := r.owners[id]
	return s, userID, nil
}

func (r *fakeCalendarSetRepo) ListByUser(_ context.Context, userID string) ([]domain.CalendarSet, error) {
	out := []domain.CalendarSet{}
	for _, id := range r.order {
		if s, ok := r.byID[id]; ok && r.owners[id] == userID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (r *fakeCalendarSetRepo) Update(_ context.Context, s domain.CalendarSet) error {
	r.byID[s.ID] = s
	return nil
}

func (r *fakeCalendarSetRepo) Delete(_ context.Context, id string) error {
	delete(r.byID, id)
	delete(r.owners, id)
	return nil
}

var _ port.CalendarSetRepo = (*fakeCalendarSetRepo)(nil)

// --- ai job repo ---------------------------------------------------------

// fakeAiJobRepo serves a scripted queue from ClaimDue and records every
// Complete/Fail call so AIJobService tests can assert dispatch, budget, and
// backoff/dead-letter outcomes. Enqueue is a bare append (no dedup — the
// real dedup-on-conflict semantics are covered by Task 3's postgres suite,
// not re-tested here).
type fakeAiJobRepo struct {
	queue      []domain.AiJob
	claimErr   error
	claimCalls int

	completed   []string
	completeErr error

	failed []struct {
		ID      string
		RetryAt *time.Time
		ErrMsg  string
	}
	failErr error
}

func newAiJobRepo() *fakeAiJobRepo { return &fakeAiJobRepo{} }

func (r *fakeAiJobRepo) Enqueue(_ context.Context, j domain.AiJob) error {
	r.queue = append(r.queue, j)
	return nil
}

func (r *fakeAiJobRepo) ClaimDue(_ context.Context, _ time.Time, limit int) ([]domain.AiJob, error) {
	r.claimCalls++
	if r.claimErr != nil {
		return nil, r.claimErr
	}
	batch := r.queue
	if limit > 0 && len(batch) > limit {
		batch = batch[:limit]
	}
	r.queue = r.queue[len(batch):]
	return batch, nil
}

func (r *fakeAiJobRepo) Complete(_ context.Context, id string) error {
	r.completed = append(r.completed, id)
	return r.completeErr
}

func (r *fakeAiJobRepo) Fail(_ context.Context, id string, retryAt *time.Time, errMsg string) error {
	r.failed = append(r.failed, struct {
		ID      string
		RetryAt *time.Time
		ErrMsg  string
	}{ID: id, RetryAt: retryAt, ErrMsg: errMsg})
	return r.failErr
}

var _ port.AiJobRepo = (*fakeAiJobRepo)(nil)

// --- ai usage repo ---------------------------------------------------------

// fakeAiUsageRepo tracks per-user call counts in memory (day-agnostic: tests
// don't need multi-day rollover, that's covered by Task 3's postgres suite).
// A bump that would exceed limit leaves the counter unchanged and reports
// allowed=false, mirroring the real race-free SQL semantics.
type fakeAiUsageRepo struct {
	calls map[string]int
	err   error
}

func newAiUsageRepo() *fakeAiUsageRepo { return &fakeAiUsageRepo{calls: map[string]int{}} }

func (r *fakeAiUsageRepo) IncrementAndCheck(_ context.Context, userID string, _ time.Time, limit int) (bool, error) {
	if r.err != nil {
		return false, r.err
	}
	if r.calls[userID]+1 > limit {
		return false, nil
	}
	r.calls[userID]++
	return true, nil
}

var _ port.AiUsageRepo = (*fakeAiUsageRepo)(nil)

// --- classifier repo ---------------------------------------------------------

type fakeClassifierRepo struct {
	byID   map[string]domain.AiClassifier
	owners map[string]string
}

func newClassifierRepo() *fakeClassifierRepo {
	return &fakeClassifierRepo{byID: map[string]domain.AiClassifier{}, owners: map[string]string{}}
}

func (r *fakeClassifierRepo) Create(_ context.Context, c domain.AiClassifier) (domain.AiClassifier, error) {
	if c.ID == "" {
		c.ID = newID()
	}
	r.byID[c.ID] = c
	r.owners[c.ID] = c.UserID
	return c, nil
}

func (r *fakeClassifierRepo) GetByID(_ context.Context, id string) (domain.AiClassifier, error) {
	c, ok := r.byID[id]
	if !ok {
		return domain.AiClassifier{}, domain.ErrNotFound
	}
	return c, nil
}

func (r *fakeClassifierRepo) ListByUser(_ context.Context, userID string) ([]domain.AiClassifier, error) {
	out := []domain.AiClassifier{}
	for id, c := range r.byID {
		if r.owners[id] == userID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (r *fakeClassifierRepo) ListEnabledByUser(_ context.Context, userID string) ([]domain.AiClassifier, error) {
	out := []domain.AiClassifier{}
	for id, c := range r.byID {
		if r.owners[id] == userID && c.Enabled {
			out = append(out, c)
		}
	}
	return out, nil
}

func (r *fakeClassifierRepo) Update(_ context.Context, c domain.AiClassifier) error {
	r.byID[c.ID] = c
	return nil
}

func (r *fakeClassifierRepo) Delete(_ context.Context, id string) error {
	delete(r.byID, id)
	delete(r.owners, id)
	return nil
}

var _ port.ClassifierRepo = (*fakeClassifierRepo)(nil)

// --- voice profile repo -------------------------------------------------------

type fakeVoiceProfileRepo struct {
	byUser map[string]domain.VoiceProfile
}

func newVoiceProfileRepo() *fakeVoiceProfileRepo {
	return &fakeVoiceProfileRepo{byUser: map[string]domain.VoiceProfile{}}
}

func (r *fakeVoiceProfileRepo) Get(_ context.Context, userID string) (domain.VoiceProfile, error) {
	p, ok := r.byUser[userID]
	if !ok {
		return domain.VoiceProfile{}, domain.ErrNotFound
	}
	return p, nil
}

func (r *fakeVoiceProfileRepo) Upsert(_ context.Context, p domain.VoiceProfile) error {
	r.byUser[p.UserID] = p
	return nil
}

var _ port.VoiceProfileRepo = (*fakeVoiceProfileRepo)(nil)

// --- booking link repo --------------------------------------------------------

// fakeBookingLinkRepo enforces the same case-insensitive slug uniqueness as
// the real postgres adapter: Create returns domain.ErrConflict when the slug
// (case-insensitive) is already taken by another link.
type fakeBookingLinkRepo struct {
	byID map[string]domain.BookingLink
}

func newBookingLinkRepo() *fakeBookingLinkRepo {
	return &fakeBookingLinkRepo{byID: map[string]domain.BookingLink{}}
}

func (r *fakeBookingLinkRepo) Create(_ context.Context, l domain.BookingLink) (domain.BookingLink, error) {
	for _, existing := range r.byID {
		if strings.EqualFold(existing.Slug, l.Slug) {
			return domain.BookingLink{}, domain.ErrConflict
		}
	}
	if l.ID == "" {
		l.ID = newID()
	}
	r.byID[l.ID] = l
	return l, nil
}

func (r *fakeBookingLinkRepo) GetByID(_ context.Context, id string) (domain.BookingLink, error) {
	l, ok := r.byID[id]
	if !ok {
		return domain.BookingLink{}, domain.ErrNotFound
	}
	return l, nil
}

func (r *fakeBookingLinkRepo) GetBySlug(_ context.Context, slug string) (domain.BookingLink, error) {
	for _, l := range r.byID {
		if strings.EqualFold(l.Slug, slug) {
			return l, nil
		}
	}
	return domain.BookingLink{}, domain.ErrNotFound
}

func (r *fakeBookingLinkRepo) ListByUser(_ context.Context, userID string) ([]domain.BookingLink, error) {
	out := []domain.BookingLink{}
	for _, l := range r.byID {
		if l.UserID == userID {
			out = append(out, l)
		}
	}
	return out, nil
}

// Update mirrors the real postgres adapter: a case-insensitive slug
// collision with a DIFFERENT link returns domain.ErrConflict (the link's own
// row keeping its current slug is not a collision with itself).
func (r *fakeBookingLinkRepo) Update(_ context.Context, l domain.BookingLink) error {
	if _, ok := r.byID[l.ID]; !ok {
		return domain.ErrNotFound
	}
	for id, existing := range r.byID {
		if id != l.ID && strings.EqualFold(existing.Slug, l.Slug) {
			return domain.ErrConflict
		}
	}
	r.byID[l.ID] = l
	return nil
}

func (r *fakeBookingLinkRepo) Delete(_ context.Context, id string) error {
	delete(r.byID, id)
	return nil
}

var _ port.BookingLinkRepo = (*fakeBookingLinkRepo)(nil)

// --- booking repo --------------------------------------------------------------

// fakeBookingRepo mirrors the DB exclusion constraint in memory: CreateHold
// rejects a hold/confirmed booking whose [Start,End) overlaps another active
// (hold or confirmed) booking on the same link, returning domain.ErrConflict.
// ListByUser scopes by owning link when the optional links pointer is set
// (Booking carries no UserID); left nil, it returns every stored booking.
type fakeBookingRepo struct {
	byID  map[string]domain.Booking
	links *fakeBookingLinkRepo
	// forceCreateHoldErr, when set, is returned by CreateHold instead of the
	// usual overlap check — simulates the DB exclusion constraint firing on
	// a concurrent competitor the in-memory overlap scan wouldn't otherwise
	// catch.
	forceCreateHoldErr error
}

func newBookingRepo(links *fakeBookingLinkRepo) *fakeBookingRepo {
	return &fakeBookingRepo{byID: map[string]domain.Booking{}, links: links}
}

func bookingsOverlap(a, b domain.Booking) bool {
	return a.Start.Before(b.End) && b.Start.Before(a.End)
}

func (r *fakeBookingRepo) CreateHold(_ context.Context, b domain.Booking) (domain.Booking, error) {
	if r.forceCreateHoldErr != nil {
		return domain.Booking{}, r.forceCreateHoldErr
	}
	for _, existing := range r.byID {
		if existing.LinkID != b.LinkID {
			continue
		}
		if existing.Status == domain.BookingCancelled {
			continue
		}
		if bookingsOverlap(existing, b) {
			return domain.Booking{}, domain.ErrConflict
		}
	}
	if b.ID == "" {
		b.ID = newID()
	}
	r.byID[b.ID] = b
	return b, nil
}

func (r *fakeBookingRepo) GetByID(_ context.Context, id string) (domain.Booking, error) {
	b, ok := r.byID[id]
	if !ok {
		return domain.Booking{}, domain.ErrNotFound
	}
	return b, nil
}

func (r *fakeBookingRepo) ListActiveInRange(_ context.Context, linkID string, from, to time.Time) ([]domain.Booking, error) {
	out := []domain.Booking{}
	for _, b := range r.byID {
		if b.LinkID != linkID || b.Status == domain.BookingCancelled {
			continue
		}
		if b.End.After(from) && b.Start.Before(to) {
			out = append(out, b)
		}
	}
	return out, nil
}

func (r *fakeBookingRepo) ListByUser(_ context.Context, userID string, limit int) ([]domain.Booking, error) {
	out := []domain.Booking{}
	for _, b := range r.byID {
		if r.links != nil {
			link, ok := r.links.byID[b.LinkID]
			if !ok || link.UserID != userID {
				continue
			}
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *fakeBookingRepo) Confirm(_ context.Context, id, eventID string) error {
	b, ok := r.byID[id]
	if !ok {
		return domain.ErrNotFound
	}
	b.Status = domain.BookingConfirmed
	eid := eventID
	b.EventID = &eid
	b.HoldExpiresAt = nil
	r.byID[id] = b
	return nil
}

func (r *fakeBookingRepo) Cancel(_ context.Context, id string) error {
	b, ok := r.byID[id]
	if !ok {
		return domain.ErrNotFound
	}
	b.Status = domain.BookingCancelled
	r.byID[id] = b
	return nil
}

func (r *fakeBookingRepo) ExpireHolds(_ context.Context, now time.Time) (int64, error) {
	var count int64
	for id, b := range r.byID {
		if b.Status != domain.BookingHold || b.HoldExpiresAt == nil {
			continue
		}
		if !b.HoldExpiresAt.After(now) {
			b.Status = domain.BookingCancelled
			r.byID[id] = b
			count++
		}
	}
	return count, nil
}

var _ port.BookingRepo = (*fakeBookingRepo)(nil)

// --- poll repo -----------------------------------------------------------------

// fakePollRepo persists polls plus a votes table keyed by
// pollID+optionID+lower(voterEmail), mirroring the DB's replace-ballot upsert.
type fakePollRepo struct {
	byID  map[string]domain.MeetingPoll
	votes map[string]domain.PollVote

	// createErrs, when non-empty, is consumed FIFO by Create (one error per
	// call, poll not stored) instead of the normal insert path — lets a test
	// simulate a token collision (domain.ErrConflict) that the service must
	// retry past with a freshly generated token.
	createErrs []error
}

func newPollRepo() *fakePollRepo {
	return &fakePollRepo{byID: map[string]domain.MeetingPoll{}, votes: map[string]domain.PollVote{}}
}

func pollVoteKey(pollID, optionID, voterEmail string) string {
	return pollID + "\x00" + optionID + "\x00" + strings.ToLower(voterEmail)
}

func (r *fakePollRepo) Create(_ context.Context, p domain.MeetingPoll) (domain.MeetingPoll, error) {
	if len(r.createErrs) > 0 {
		err := r.createErrs[0]
		r.createErrs = r.createErrs[1:]
		return domain.MeetingPoll{}, err
	}
	if p.ID == "" {
		p.ID = newID()
	}
	if p.Token == "" {
		p.Token = randomToken(16)
	}
	r.byID[p.ID] = p
	return p, nil
}

func (r *fakePollRepo) GetByID(_ context.Context, id string) (domain.MeetingPoll, error) {
	p, ok := r.byID[id]
	if !ok {
		return domain.MeetingPoll{}, domain.ErrNotFound
	}
	return p, nil
}

func (r *fakePollRepo) GetByToken(_ context.Context, token string) (domain.MeetingPoll, error) {
	for _, p := range r.byID {
		if p.Token == token {
			return p, nil
		}
	}
	return domain.MeetingPoll{}, domain.ErrNotFound
}

func (r *fakePollRepo) ListByUser(_ context.Context, userID string) ([]domain.MeetingPoll, error) {
	out := []domain.MeetingPoll{}
	for _, p := range r.byID {
		if p.UserID == userID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (r *fakePollRepo) Update(_ context.Context, p domain.MeetingPoll) error {
	if _, ok := r.byID[p.ID]; !ok {
		return domain.ErrNotFound
	}
	r.byID[p.ID] = p
	return nil
}

func (r *fakePollRepo) Delete(_ context.Context, id string) error {
	delete(r.byID, id)
	for k, v := range r.votes {
		if v.PollID == id {
			delete(r.votes, k)
		}
	}
	return nil
}

func (r *fakePollRepo) UpsertVotes(_ context.Context, votes []domain.PollVote) error {
	for _, v := range votes {
		r.votes[pollVoteKey(v.PollID, v.OptionID, v.VoterEmail)] = v
	}
	return nil
}

func (r *fakePollRepo) ListVotes(_ context.Context, pollID string) ([]domain.PollVote, error) {
	out := []domain.PollVote{}
	for _, v := range r.votes {
		if v.PollID == pollID {
			out = append(out, v)
		}
	}
	return out, nil
}

var _ port.PollRepo = (*fakePollRepo)(nil)

// --- time proposal repo ---------------------------------------------------------

type fakeProposalRepo struct {
	byID map[string]domain.TimeProposal
}

func newProposalRepo() *fakeProposalRepo {
	return &fakeProposalRepo{byID: map[string]domain.TimeProposal{}}
}

func (r *fakeProposalRepo) Create(_ context.Context, p domain.TimeProposal) (domain.TimeProposal, error) {
	if p.ID == "" {
		p.ID = newID()
	}
	r.byID[p.ID] = p
	return p, nil
}

func (r *fakeProposalRepo) GetByID(_ context.Context, id string) (domain.TimeProposal, error) {
	p, ok := r.byID[id]
	if !ok {
		return domain.TimeProposal{}, domain.ErrNotFound
	}
	return p, nil
}

func (r *fakeProposalRepo) ListByEvent(_ context.Context, eventID string) ([]domain.TimeProposal, error) {
	out := []domain.TimeProposal{}
	for _, p := range r.byID {
		if p.EventID == eventID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (r *fakeProposalRepo) Update(_ context.Context, p domain.TimeProposal) error {
	if _, ok := r.byID[p.ID]; !ok {
		return domain.ErrNotFound
	}
	r.byID[p.ID] = p
	return nil
}

var _ port.TimeProposalRepo = (*fakeProposalRepo)(nil)

// --- user settings repo ---------------------------------------------------------

// fakeUserSettingsRepo mirrors the real adapter's absent-row default: Get
// returns a zero-value UserSettings with TimeZone "UTC" when no row exists.
type fakeUserSettingsRepo struct {
	byUser map[string]domain.UserSettings
}

func newUserSettingsRepo() *fakeUserSettingsRepo {
	return &fakeUserSettingsRepo{byUser: map[string]domain.UserSettings{}}
}

func (r *fakeUserSettingsRepo) Get(_ context.Context, userID string) (domain.UserSettings, error) {
	s, ok := r.byUser[userID]
	if !ok {
		return domain.UserSettings{UserID: userID, TimeZone: "UTC"}, nil
	}
	return s, nil
}

func (r *fakeUserSettingsRepo) Upsert(_ context.Context, s domain.UserSettings) error {
	r.byUser[s.UserID] = s
	return nil
}

var _ port.UserSettingsRepo = (*fakeUserSettingsRepo)(nil)

// --- reaction repo -----------------------------------------------------------

// fakeReactionRepo mirrors the real postgres adapter's semantics: Create is
// idempotent on (message_id, user_id, emoji) — re-reacting returns the
// existing row's stored Delivery/CreatedAt rather than the caller's new
// values (matching the ON CONFLICT DO UPDATE SET emoji = emoji no-op in
// reaction.go, which never rewrites Delivery on a duplicate Create).
// DeleteByEmoji reports domain.ErrNotFound when nothing matched.
type fakeReactionRepo struct {
	byID  map[string]domain.Reaction
	order []string
}

func newReactionRepo() *fakeReactionRepo {
	return &fakeReactionRepo{byID: map[string]domain.Reaction{}}
}

func (r *fakeReactionRepo) Create(_ context.Context, react domain.Reaction) (domain.Reaction, error) {
	for _, id := range r.order {
		existing := r.byID[id]
		if existing.MessageID == react.MessageID && existing.UserID == react.UserID && existing.Emoji == react.Emoji {
			return existing, nil
		}
	}
	if react.ID == "" {
		react.ID = newID()
	}
	if react.Delivery == "" {
		react.Delivery = "local"
	}
	r.byID[react.ID] = react
	r.order = append(r.order, react.ID)
	return react, nil
}

func (r *fakeReactionRepo) ListByMessages(_ context.Context, messageIDs []string) (map[string][]domain.Reaction, error) {
	want := map[string]struct{}{}
	for _, id := range messageIDs {
		want[id] = struct{}{}
	}
	out := map[string][]domain.Reaction{}
	for _, id := range r.order {
		react := r.byID[id]
		if _, ok := want[react.MessageID]; ok {
			out[react.MessageID] = append(out[react.MessageID], react)
		}
	}
	return out, nil
}

func (r *fakeReactionRepo) DeleteByEmoji(_ context.Context, messageID, userID, emoji string) error {
	for i, id := range r.order {
		react := r.byID[id]
		if react.MessageID == messageID && react.UserID == userID && react.Emoji == emoji {
			delete(r.byID, id)
			r.order = append(r.order[:i:i], r.order[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

var _ port.ReactionRepo = (*fakeReactionRepo)(nil)

type fakeAttachmentRecord struct {
	att       domain.Attachment
	messageID string
}

func (r *fakeMessageRepo) seedAttachment(messageID string, att domain.Attachment) {
	r.attachmentByID[att.ID] = fakeAttachmentRecord{att: att, messageID: messageID}
}

// --- team repo ---------------------------------------------------------------

// fakeTeamRepo is map-backed: teams by id plus per-team member maps. It honors
// the port contract that GetMember returns ErrNotFound for non-members (the
// service-layer authz primitive) and that Create inserts the team and its
// owner membership atomically, like the SQL repo.
type fakeTeamRepo struct {
	byID    map[string]domain.Team
	members map[string]map[string]domain.TeamMember // teamID -> userID -> member
}

func newTeamRepo() *fakeTeamRepo {
	return &fakeTeamRepo{
		byID:    map[string]domain.Team{},
		members: map[string]map[string]domain.TeamMember{},
	}
}

func (r *fakeTeamRepo) Create(_ context.Context, t domain.Team, owner domain.TeamMember) (domain.Team, error) {
	if _, dup := r.byID[t.ID]; dup {
		return domain.Team{}, domain.ErrConflict
	}
	r.byID[t.ID] = t
	r.members[t.ID] = map[string]domain.TeamMember{owner.UserID: owner}
	return t, nil
}

func (r *fakeTeamRepo) GetByID(_ context.Context, id string) (domain.Team, error) {
	t, ok := r.byID[id]
	if !ok {
		return domain.Team{}, domain.ErrNotFound
	}
	return t, nil
}

func (r *fakeTeamRepo) ListByUser(_ context.Context, userID string) ([]domain.Team, error) {
	out := []domain.Team{}
	for teamID, members := range r.members {
		if _, ok := members[userID]; ok {
			out = append(out, r.byID[teamID])
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r *fakeTeamRepo) Update(_ context.Context, t domain.Team) error {
	if _, ok := r.byID[t.ID]; !ok {
		return domain.ErrNotFound
	}
	r.byID[t.ID] = t
	return nil
}

func (r *fakeTeamRepo) Delete(_ context.Context, id string) error {
	if _, ok := r.byID[id]; !ok {
		return domain.ErrNotFound
	}
	delete(r.byID, id)
	delete(r.members, id)
	return nil
}

func (r *fakeTeamRepo) GetMember(_ context.Context, teamID, userID string) (domain.TeamMember, error) {
	m, ok := r.members[teamID][userID]
	if !ok {
		return domain.TeamMember{}, domain.ErrNotFound
	}
	return m, nil
}

func (r *fakeTeamRepo) ListMembers(_ context.Context, teamID string) ([]domain.TeamMember, error) {
	out := []domain.TeamMember{}
	for _, m := range r.members[teamID] {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UserID < out[j].UserID })
	return out, nil
}

func (r *fakeTeamRepo) UpsertMember(_ context.Context, m domain.TeamMember) error {
	if r.members[m.TeamID] == nil {
		r.members[m.TeamID] = map[string]domain.TeamMember{}
	}
	r.members[m.TeamID][m.UserID] = m
	return nil
}

func (r *fakeTeamRepo) RemoveMember(_ context.Context, teamID, userID string) error {
	if _, ok := r.members[teamID][userID]; !ok {
		return domain.ErrNotFound
	}
	delete(r.members[teamID], userID)
	return nil
}

func (r *fakeTeamRepo) CountByRole(_ context.Context, teamID string, role domain.TeamRole) (int, error) {
	n := 0
	for _, m := range r.members[teamID] {
		if m.Role == role {
			n++
		}
	}
	return n, nil
}

var _ port.TeamRepo = (*fakeTeamRepo)(nil)

// --- team invitation repo -----------------------------------------------------

// fakeTeamInvitationRepo mirrors the SQL repo's uniqueness semantics: a second
// pending invitation for the same (team, lower(email)) — or a token-hash
// collision — maps to ErrConflict, on Create and on an Update that re-opens an
// invitation to pending.
type fakeTeamInvitationRepo struct {
	byID  map[string]domain.TeamInvitation
	order []string
}

func newTeamInvitationRepo() *fakeTeamInvitationRepo {
	return &fakeTeamInvitationRepo{byID: map[string]domain.TeamInvitation{}}
}

func (r *fakeTeamInvitationRepo) conflicts(inv domain.TeamInvitation) bool {
	for _, other := range r.byID {
		if other.ID == inv.ID {
			continue
		}
		if other.TokenHash == inv.TokenHash {
			return true
		}
		if inv.Status == domain.InvitePending && other.Status == domain.InvitePending &&
			other.TeamID == inv.TeamID && strings.EqualFold(other.Email, inv.Email) {
			return true
		}
	}
	return false
}

func (r *fakeTeamInvitationRepo) Create(_ context.Context, inv domain.TeamInvitation) (domain.TeamInvitation, error) {
	if _, dup := r.byID[inv.ID]; dup || r.conflicts(inv) {
		return domain.TeamInvitation{}, fmt.Errorf("%w: a pending invitation for this address already exists", domain.ErrConflict)
	}
	r.byID[inv.ID] = inv
	r.order = append(r.order, inv.ID)
	return inv, nil
}

func (r *fakeTeamInvitationRepo) GetByID(_ context.Context, id string) (domain.TeamInvitation, error) {
	inv, ok := r.byID[id]
	if !ok {
		return domain.TeamInvitation{}, domain.ErrNotFound
	}
	return inv, nil
}

func (r *fakeTeamInvitationRepo) GetByTokenHash(_ context.Context, tokenHash string) (domain.TeamInvitation, error) {
	for _, id := range r.order {
		if inv := r.byID[id]; inv.TokenHash == tokenHash {
			return inv, nil
		}
	}
	return domain.TeamInvitation{}, domain.ErrNotFound
}

func (r *fakeTeamInvitationRepo) ListByTeam(_ context.Context, teamID string) ([]domain.TeamInvitation, error) {
	out := []domain.TeamInvitation{}
	for _, id := range r.order {
		if inv := r.byID[id]; inv.TeamID == teamID {
			out = append(out, inv)
		}
	}
	return out, nil
}

func (r *fakeTeamInvitationRepo) Update(_ context.Context, inv domain.TeamInvitation) error {
	if _, ok := r.byID[inv.ID]; !ok {
		return domain.ErrNotFound
	}
	if r.conflicts(inv) {
		return fmt.Errorf("%w: a pending invitation for this address already exists", domain.ErrConflict)
	}
	r.byID[inv.ID] = inv
	return nil
}

var _ port.TeamInvitationRepo = (*fakeTeamInvitationRepo)(nil)

// --- task repo (M2.8) --------------------------------------------------------

// fakeTaskRepo is map-backed and mirrors the postgres repo's List semantics:
// completed excluded unless IncludeCompleted, scheduled overlap with
// [ScheduledFrom, ScheduledTo), due range [DueFrom, DueTo), ordered by
// (position, createdAt, id).
type fakeTaskRepo struct {
	byID map[string]domain.Task
	seq  int

	// programmable
	createErr error
	listErr   error
	updateErr error

	// recording
	lastQuery   port.TaskQuery
	updateCalls int
}

func newTaskRepo() *fakeTaskRepo { return &fakeTaskRepo{byID: map[string]domain.Task{}} }

func (r *fakeTaskRepo) Create(_ context.Context, t domain.Task) (domain.Task, error) {
	if r.createErr != nil {
		return domain.Task{}, r.createErr
	}
	if t.ID == "" {
		r.seq++
		t.ID = fmt.Sprintf("task_%d", r.seq)
	}
	if t.Source == "" {
		t.Source = domain.TaskSourceLocal
	}
	r.byID[t.ID] = t
	return t, nil
}

func (r *fakeTaskRepo) GetByID(_ context.Context, id string) (domain.Task, error) {
	t, ok := r.byID[id]
	if !ok {
		return domain.Task{}, domain.ErrNotFound
	}
	return t, nil
}

func (r *fakeTaskRepo) GetByExternalID(_ context.Context, userID string, source domain.TaskSource, externalID string) (domain.Task, error) {
	for _, t := range r.byID {
		if t.UserID == userID && t.Source == source && t.ExternalID == externalID {
			return t, nil
		}
	}
	return domain.Task{}, domain.ErrNotFound
}

func (r *fakeTaskRepo) List(_ context.Context, q port.TaskQuery) ([]domain.Task, error) {
	r.lastQuery = q
	if r.listErr != nil {
		return nil, r.listErr
	}
	out := []domain.Task{}
	for _, t := range r.byID {
		switch {
		case t.UserID != q.UserID:
		case q.Source != "" && t.Source != q.Source:
		case !q.IncludeCompleted && t.Completed():
		case !q.ScheduledFrom.IsZero() && (t.ScheduledEnd == nil || !t.ScheduledEnd.After(q.ScheduledFrom)):
		case !q.ScheduledTo.IsZero() && (t.ScheduledStart == nil || !t.ScheduledStart.Before(q.ScheduledTo)):
		case !q.DueFrom.IsZero() && (t.Due == nil || t.Due.Before(q.DueFrom)):
		case !q.DueTo.IsZero() && (t.Due == nil || !t.Due.Before(q.DueTo)):
		case q.UnscheduledOnly && t.ScheduledStart != nil:
		default:
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Position != out[j].Position {
			return out[i].Position < out[j].Position
		}
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func (r *fakeTaskRepo) Update(_ context.Context, t domain.Task) error {
	r.updateCalls++
	if r.updateErr != nil {
		return r.updateErr
	}
	if _, ok := r.byID[t.ID]; !ok {
		return domain.ErrNotFound
	}
	r.byID[t.ID] = t
	return nil
}

func (r *fakeTaskRepo) Delete(_ context.Context, id string) error {
	if _, ok := r.byID[id]; !ok {
		return domain.ErrNotFound
	}
	delete(r.byID, id)
	return nil
}

func (r *fakeTaskRepo) DeleteBySource(_ context.Context, userID string, source domain.TaskSource) error {
	for id, t := range r.byID {
		if t.UserID == userID && t.Source == source {
			delete(r.byID, id)
		}
	}
	return nil
}

var _ port.TaskRepo = (*fakeTaskRepo)(nil)
