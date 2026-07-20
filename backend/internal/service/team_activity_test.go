package service

// Tests for M2.7 Task 10 (team read statuses / reply indicators): the
// MarkThreadOpened opened_at hook, the delivered-send replied_at hook, RFC
// Message-ID capture at ingest, and the TeamActivityService query surface.
// PRIVACY invariants get explicit negatives: an opted-out member records
// nothing, and foreign teams are never queried.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- doubles ----------------------------------------------------------------

type fakeActivityRepo struct {
	rfcByThread map[string]string
	rfcErr      error

	sharingTeams []string
	sharingErr   error
	sharingCalls int

	upserts   []domain.TeamThreadActivity
	upsertErr error

	listByConv   map[string][]domain.TeamThreadActivity // teamID + "|" + key
	listErr      error
	queriedTeams []string
}

func newActivityRepo() *fakeActivityRepo {
	return &fakeActivityRepo{
		rfcByThread: map[string]string{},
		listByConv:  map[string][]domain.TeamThreadActivity{},
	}
}

func (r *fakeActivityRepo) Upsert(_ context.Context, a domain.TeamThreadActivity) error {
	if r.upsertErr != nil {
		return r.upsertErr
	}
	r.upserts = append(r.upserts, a)
	return nil
}

func (r *fakeActivityRepo) ListByConversation(_ context.Context, teamID, key string) ([]domain.TeamThreadActivity, error) {
	r.queriedTeams = append(r.queriedTeams, teamID)
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.listByConv[teamID+"|"+key], nil
}

func (r *fakeActivityRepo) EarliestRFCMessageID(_ context.Context, threadID string) (string, error) {
	return r.rfcByThread[threadID], r.rfcErr
}

func (r *fakeActivityRepo) ListSharingTeamIDs(_ context.Context, userID string) ([]string, error) {
	r.sharingCalls++
	return r.sharingTeams, r.sharingErr
}

var _ port.TeamThreadActivityRepo = (*fakeActivityRepo)(nil)

// captureBus records published collab events (Subscribe is unused here).
type captureBus struct{ events []port.CollabEvent }

func (b *captureBus) Publish(ev port.CollabEvent) { b.events = append(b.events, ev) }
func (b *captureBus) Subscribe(topics []string) (<-chan port.CollabEvent, func()) {
	ch := make(chan port.CollabEvent)
	return ch, func() {}
}

var _ port.EventBus = (*captureBus)(nil)

// --- MarkThreadOpened opened_at hook ----------------------------------------

// activityMailFixture is a MailService wired with the activity repo and bus;
// no provider is registered, so MarkThreadOpened skips the write-through.
func activityMailFixture(t *testing.T, activity *fakeActivityRepo, bus *captureBus) (*MailService, *fakeClock) {
	t.Helper()
	ctx := context.Background()
	clk := newClock(time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC))
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@acme.com",
	}); err != nil {
		t.Fatal(err)
	}
	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", AccountID: "a1", ProviderThreadID: "pt1", Unread: true}); err != nil {
		t.Fatal(err)
	}
	svc := NewMailService(MailServiceDeps{
		Accounts:   accounts,
		Threads:    threads,
		Messages:   newMessageRepo(),
		Activity:   activity,
		Bus:        bus,
		Clock:      clk,
		SelfHosted: true,
	})
	return svc, clk
}

func TestMarkThreadOpenedRecordsActivityForSharingTeams(t *testing.T) {
	activity := newActivityRepo()
	activity.rfcByThread["t1"] = "<conv-1@acme.com>"
	activity.sharingTeams = []string{"teamA"} // opted in on teamA only
	bus := &captureBus{}
	svc, clk := activityMailFixture(t, activity, bus)

	if err := svc.MarkThreadOpened(context.Background(), "u1", "t1"); err != nil {
		t.Fatalf("MarkThreadOpened: %v", err)
	}
	if len(activity.upserts) != 1 {
		t.Fatalf("upserts = %d, want 1", len(activity.upserts))
	}
	got := activity.upserts[0]
	if got.TeamID != "teamA" || got.UserID != "u1" || got.ConversationKey != "<conv-1@acme.com>" {
		t.Fatalf("upsert = %+v, want teamA/u1/<conv-1@acme.com>", got)
	}
	if got.OpenedAt == nil || !got.OpenedAt.Equal(clk.Now()) {
		t.Fatalf("OpenedAt = %v, want %v", got.OpenedAt, clk.Now())
	}
	if got.RepliedAt != nil {
		t.Fatalf("RepliedAt = %v, want nil on an open", got.RepliedAt)
	}
	if len(bus.events) != 1 {
		t.Fatalf("published events = %d, want 1", len(bus.events))
	}
	ev := bus.events[0]
	if ev.Topic != "team:teamA" || ev.Type != "activity.updated" {
		t.Fatalf("event = %+v, want team:teamA activity.updated", ev)
	}
	// Ids-only doctrine: the payload names the activity (team/user/key) and
	// nothing else — no timestamps, no row — clients refetch for content.
	var payload map[string]any
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload["conversationKey"] != "<conv-1@acme.com>" || payload["teamId"] != "teamA" || payload["userId"] != "u1" {
		t.Fatalf("payload = %v, want ids only (teamA/u1/<conv-1@acme.com>)", payload)
	}
	if len(payload) != 3 {
		t.Fatalf("payload carries %d fields %v, want exactly teamId/userId/conversationKey", len(payload), payload)
	}
}

// PRIVACY negative: a member who has NOT opted in (share_read_statuses
// false everywhere) opens a thread and nothing is recorded or published.
func TestMarkThreadOpenedOptedOutRecordsNothing(t *testing.T) {
	activity := newActivityRepo()
	activity.rfcByThread["t1"] = "<conv-1@acme.com>"
	activity.sharingTeams = nil // member of teams, opted in to none
	bus := &captureBus{}
	svc, _ := activityMailFixture(t, activity, bus)

	if err := svc.MarkThreadOpened(context.Background(), "u1", "t1"); err != nil {
		t.Fatalf("MarkThreadOpened: %v", err)
	}
	if activity.sharingCalls == 0 {
		t.Fatal("ListSharingTeamIDs was never consulted (privacy gate not exercised)")
	}
	if len(activity.upserts) != 0 {
		t.Fatalf("upserts = %d, want 0 for an opted-out member", len(activity.upserts))
	}
	if len(bus.events) != 0 {
		t.Fatalf("events = %d, want 0 for an opted-out member", len(bus.events))
	}
}

func TestMarkThreadOpenedNoConversationKeyRecordsNothing(t *testing.T) {
	activity := newActivityRepo() // no rfc id for t1
	activity.sharingTeams = []string{"teamA"}
	bus := &captureBus{}
	svc, _ := activityMailFixture(t, activity, bus)

	if err := svc.MarkThreadOpened(context.Background(), "u1", "t1"); err != nil {
		t.Fatalf("MarkThreadOpened: %v", err)
	}
	if len(activity.upserts) != 0 || len(bus.events) != 0 {
		t.Fatalf("upserts=%d events=%d, want 0/0 without a Message-ID", len(activity.upserts), len(bus.events))
	}
}

// Recording is fire-and-forget: a repo failure never fails the open.
func TestMarkThreadOpenedActivityFailureNeverFailsOpen(t *testing.T) {
	activity := newActivityRepo()
	activity.rfcByThread["t1"] = "<conv-1@acme.com>"
	activity.sharingTeams = []string{"teamA"}
	activity.upsertErr = errors.New("db down")
	bus := &captureBus{}
	svc, _ := activityMailFixture(t, activity, bus)

	if err := svc.MarkThreadOpened(context.Background(), "u1", "t1"); err != nil {
		t.Fatalf("MarkThreadOpened = %v, want nil (activity is best-effort)", err)
	}
	if len(bus.events) != 0 {
		t.Fatalf("events = %d, want 0 after a failed upsert", len(bus.events))
	}
}

// --- delivered-send replied_at hook -----------------------------------------

// deliverFixture wires a SyncService with one due scheduled draft on thread
// t1 ready to deliver.
func deliverFixture(t *testing.T, activity *fakeActivityRepo, bus *captureBus, sentAt time.Time) (*SyncService, *fakeDraftRepo) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@acme.com",
	}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "tok", RefreshToken: "r", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", AccountID: "a1", ProviderThreadID: "pt1"}); err != nil {
		t.Fatal(err)
	}
	provider := newMailProvider()
	provider.sentResult = port.SentMessage{ProviderMessageID: "pm1", ProviderThreadID: "pt1", SentAt: sentAt}

	threadID := "t1"
	due := now.Add(-time.Minute)
	drafts := newDraftRepo(accounts)
	draft := domain.Draft{ID: "d1", AccountID: "a1", ThreadID: &threadID,
		To: []domain.EmailAddress{{Email: "bob@x.com"}}, Subject: "re", ScheduledAt: &due}
	if _, err := drafts.Create(ctx, draft); err != nil {
		t.Fatal(err)
	}
	drafts.scheduledDue = []domain.Draft{draft}
	drafts.claimOutcome["d1"] = true

	svc := NewSyncService(SyncServiceDeps{
		Accounts:      accounts,
		Threads:       threads,
		Messages:      newMessageRepo(),
		Drafts:        drafts,
		MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: provider},
		OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Activity:      activity,
		Bus:           bus,
		Clock:         newClock(now),
	})
	return svc, drafts
}

func TestDeliverDraftRecordsRepliedAtForOptedInTeams(t *testing.T) {
	sentAt := time.Date(2026, 7, 18, 12, 0, 30, 0, time.UTC)
	activity := newActivityRepo()
	activity.rfcByThread["t1"] = "<conv-1@acme.com>"
	activity.sharingTeams = []string{"teamA", "teamB"}
	bus := &captureBus{}
	svc, _ := deliverFixture(t, activity, bus, sentAt)

	if err := svc.ProcessDueWork(context.Background()); err != nil {
		t.Fatalf("ProcessDueWork: %v", err)
	}
	if len(activity.upserts) != 2 {
		t.Fatalf("upserts = %d, want 2 (one per opted-in team)", len(activity.upserts))
	}
	for i, want := range []string{"teamA", "teamB"} {
		got := activity.upserts[i]
		if got.TeamID != want || got.UserID != "u1" || got.ConversationKey != "<conv-1@acme.com>" {
			t.Fatalf("upsert[%d] = %+v", i, got)
		}
		if got.RepliedAt == nil || !got.RepliedAt.Equal(sentAt) {
			t.Fatalf("upsert[%d].RepliedAt = %v, want %v", i, got.RepliedAt, sentAt)
		}
		if got.OpenedAt != nil {
			t.Fatalf("upsert[%d].OpenedAt = %v, want nil on a reply", i, got.OpenedAt)
		}
	}
	if len(bus.events) != 2 || bus.events[0].Topic != "team:teamA" || bus.events[1].Topic != "team:teamB" {
		t.Fatalf("events = %+v, want activity.updated on team:teamA and team:teamB", bus.events)
	}
}

// PRIVACY negative: the sender opted in to no team — a delivered send
// records no replied_at anywhere.
func TestDeliverDraftOptedOutRecordsNothing(t *testing.T) {
	sentAt := time.Date(2026, 7, 18, 12, 0, 30, 0, time.UTC)
	activity := newActivityRepo()
	activity.rfcByThread["t1"] = "<conv-1@acme.com>"
	activity.sharingTeams = nil
	bus := &captureBus{}
	svc, drafts := deliverFixture(t, activity, bus, sentAt)

	if err := svc.ProcessDueWork(context.Background()); err != nil {
		t.Fatalf("ProcessDueWork: %v", err)
	}
	if drafts.claimedID != "d1" {
		t.Fatalf("draft was not delivered (claimedID=%q)", drafts.claimedID)
	}
	if len(activity.upserts) != 0 || len(bus.events) != 0 {
		t.Fatalf("upserts=%d events=%d, want 0/0 for an opted-out sender", len(activity.upserts), len(bus.events))
	}
}

// --- RFC Message-ID capture at ingest ---------------------------------------

func TestSyncIngestStoresRFCMessageID(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@acme.com",
	}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "tok", RefreshToken: "r", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	provider := newMailProvider()
	provider.syncPage = port.MailSyncPage{
		Threads: []domain.Thread{{ProviderThreadID: "pt1", Subject: "hello", LastMessageAt: now}},
		Messages: []port.IncomingMessage{
			{
				Message: domain.Message{ProviderMessageID: "pm1", ThreadID: "pt1",
					From: domain.EmailAddress{Email: "alice@x.com"}, SentAt: now.Add(-time.Hour)},
				Headers: map[string]string{"Message-Id": " <first@x.com> "},
			},
			{
				Message: domain.Message{ProviderMessageID: "pm2", ThreadID: "pt1",
					From: domain.EmailAddress{Email: "bob@x.com"}, SentAt: now},
				Headers: map[string]string{}, // no Message-ID header
			},
		},
	}
	messages := newMessageRepo()
	threads := newThreadRepo()
	svc := NewSyncService(SyncServiceDeps{
		Accounts:      accounts,
		Labels:        newLabelRepo(),
		Threads:       threads,
		Messages:      messages,
		SyncState:     newSyncStateRepo(),
		MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: provider},
		OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:         newClock(now),
	})
	if err := svc.SyncAccount(ctx, "a1"); err != nil {
		t.Fatalf("SyncAccount: %v", err)
	}

	th, err := threads.GetByProviderID(ctx, "a1", "pt1")
	if err != nil {
		t.Fatalf("thread: %v", err)
	}
	msgs, err := messages.ListByThread(ctx, th.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2", len(msgs))
	}
	byProvider := map[string]domain.Message{}
	for _, m := range msgs {
		byProvider[m.ProviderMessageID] = m
	}
	if got := byProvider["pm1"].RFCMessageID; got != "<first@x.com>" {
		t.Fatalf("pm1 RFCMessageID = %q, want trimmed <first@x.com>", got)
	}
	if got := byProvider["pm2"].RFCMessageID; got != "" {
		t.Fatalf("pm2 RFCMessageID = %q, want empty when the header is absent", got)
	}
}

// --- TeamActivityService query ----------------------------------------------

func newTeamActivityFixture(t *testing.T, activity *fakeActivityRepo) (*TeamActivityService, *fakeTeamRepo) {
	t.Helper()
	ctx := context.Background()
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@acme.com",
	}); err != nil {
		t.Fatal(err)
	}
	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", AccountID: "a1", ProviderThreadID: "pt1"}); err != nil {
		t.Fatal(err)
	}
	teams := newTeamRepo()
	svc := NewTeamActivityService(TeamActivityServiceDeps{
		Accounts:   accounts,
		Threads:    threads,
		Teams:      teams,
		Activity:   activity,
		Clock:      newClock(time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)),
		SelfHosted: true,
	})
	return svc, teams
}

// Cross-tenant doctrine: another user's thread is a 404, never data.
func TestTeamThreadActivityForeignThreadNotFound(t *testing.T) {
	svc, _ := newTeamActivityFixture(t, newActivityRepo())
	if _, err := svc.TeamThreadActivity(context.Background(), "intruder", "t1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound for a foreign thread", err)
	}
}

func TestTeamThreadActivityNoMessageIDIsEmpty(t *testing.T) {
	svc, _ := newTeamActivityFixture(t, newActivityRepo())
	got, err := svc.TeamThreadActivity(context.Background(), "u1", "t1")
	if err != nil {
		t.Fatalf("TeamThreadActivity: %v", err)
	}
	if len(got) != 0 || got == nil {
		t.Fatalf("got = %#v, want empty non-nil slice", got)
	}
}

func TestTeamThreadActivityNoTeamsIsEmptyNotError(t *testing.T) {
	activity := newActivityRepo()
	activity.rfcByThread["t1"] = "<k@x>"
	svc, _ := newTeamActivityFixture(t, activity)
	got, err := svc.TeamThreadActivity(context.Background(), "u1", "t1")
	if err != nil {
		t.Fatalf("TeamThreadActivity: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got = %#v, want empty for a caller in no team", got)
	}
}

// Rows come only from the caller's own teams; a foreign team holding
// activity for the same conversation is never queried (cross-tenant).
func TestTeamThreadActivityScopedToCallersTeams(t *testing.T) {
	ctx := context.Background()
	activity := newActivityRepo()
	activity.rfcByThread["t1"] = "<k@x>"
	opened := time.Date(2026, 7, 18, 11, 0, 0, 0, time.UTC)
	want := []domain.TeamThreadActivity{
		{TeamID: "teamA", UserID: "mate", ConversationKey: "<k@x>", OpenedAt: &opened},
	}
	activity.listByConv["teamA|<k@x>"] = want
	activity.listByConv["teamX|<k@x>"] = []domain.TeamThreadActivity{
		{TeamID: "teamX", UserID: "stranger", ConversationKey: "<k@x>", OpenedAt: &opened},
	}
	svc, teams := newTeamActivityFixture(t, activity)
	if _, err := teams.Create(ctx, domain.Team{ID: "teamA", Name: "Ops"}, domain.TeamMember{TeamID: "teamA", UserID: "u1", Role: domain.TeamRoleOwner}); err != nil {
		t.Fatal(err)
	}
	if _, err := teams.Create(ctx, domain.Team{ID: "teamX", Name: "Foreign"}, domain.TeamMember{TeamID: "teamX", UserID: "stranger", Role: domain.TeamRoleOwner}); err != nil {
		t.Fatal(err)
	}

	got, err := svc.TeamThreadActivity(ctx, "u1", "t1")
	if err != nil {
		t.Fatalf("TeamThreadActivity: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got = %+v, want %+v", got, want)
	}
	for _, teamID := range activity.queriedTeams {
		if teamID == "teamX" {
			t.Fatal("queried a team the caller does not belong to")
		}
	}
}

func TestRFCMessageIDFromHeaders(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{"canonical", map[string]string{"Message-Id": "<a@x>"}, "<a@x>"},
		{"uppercase ID", map[string]string{"Message-ID": " <b@x>\n"}, "<b@x>"},
		{"lowercase", map[string]string{"message-id": "<c@x>"}, "<c@x>"},
		{"absent", map[string]string{"Subject": "hi"}, ""},
		{"nil map", nil, ""},
	}
	for _, tc := range cases {
		if got := rfcMessageIDFromHeaders(tc.headers); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
