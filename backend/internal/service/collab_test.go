package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- comment repo fake -------------------------------------------------------

type fakeCommentRepo struct {
	byID  map[string]domain.Comment
	order []string
}

func newFakeCommentRepo() *fakeCommentRepo {
	return &fakeCommentRepo{byID: map[string]domain.Comment{}}
}

func (r *fakeCommentRepo) Create(_ context.Context, c domain.Comment) (domain.Comment, error) {
	r.byID[c.ID] = c
	r.order = append(r.order, c.ID)
	return c, nil
}

func (r *fakeCommentRepo) GetByID(_ context.Context, id string) (domain.Comment, error) {
	c, ok := r.byID[id]
	if !ok || c.DeletedAt != nil {
		return domain.Comment{}, domain.ErrNotFound
	}
	return c, nil
}

func (r *fakeCommentRepo) ListByThreadTeam(_ context.Context, threadID, teamID string) ([]domain.Comment, error) {
	out := []domain.Comment{}
	for _, id := range r.order {
		c := r.byID[id]
		if c.ThreadID == threadID && c.TeamID == teamID && c.DeletedAt == nil {
			out = append(out, c)
		}
	}
	return out, nil
}

func (r *fakeCommentRepo) Update(_ context.Context, c domain.Comment) error {
	existing, ok := r.byID[c.ID]
	if !ok || existing.DeletedAt != nil {
		return domain.ErrNotFound
	}
	r.byID[c.ID] = c
	return nil
}

func (r *fakeCommentRepo) SoftDelete(_ context.Context, id string, at time.Time) error {
	c, ok := r.byID[id]
	if !ok || c.DeletedAt != nil {
		return domain.ErrNotFound
	}
	c.DeletedAt = &at
	r.byID[id] = c
	return nil
}

var _ port.CommentRepo = (*fakeCommentRepo)(nil)

// --- bus + share fakes -------------------------------------------------------

// collabBusRecorder records every published CollabEvent.
type collabBusRecorder struct{ events []port.CollabEvent }

func (b *collabBusRecorder) Publish(ev port.CollabEvent) { b.events = append(b.events, ev) }

func (b *collabBusRecorder) Subscribe([]string) (<-chan port.CollabEvent, func()) {
	ch := make(chan port.CollabEvent)
	close(ch)
	return ch, func() {}
}

func (b *collabBusRecorder) ofType(evType string) []port.CollabEvent {
	out := []port.CollabEvent{}
	for _, ev := range b.events {
		if ev.Type == evType {
			out = append(out, ev)
		}
	}
	return out
}

var _ port.EventBus = (*collabBusRecorder)(nil)

// collabShareStub answers ThreadSharedWithTeam from a static map.
type collabShareStub struct{ shared map[string]map[string]bool }

func (f *collabShareStub) ThreadSharedWithTeam(_ context.Context, threadID, teamID string) (bool, error) {
	return f.shared[threadID][teamID], nil
}

var _ ThreadShareChecker = (*collabShareStub)(nil)

// --- fixture -----------------------------------------------------------------

type collabFixture struct {
	svc      *CollabService
	comments *fakeCommentRepo
	teams    *fakeTeamRepo
	threads  *fakeThreadRepo
	accounts *fakeAccountRepo
	users    *fakeUserRepo
	devices  *fakeDeviceRepo
	push     *fakePush
	bus      *collabBusRecorder
	shares   *collabShareStub
	clock    *fakeClock
}

func newCollabFixture(t *testing.T) *collabFixture {
	t.Helper()
	f := &collabFixture{
		comments: newFakeCommentRepo(),
		teams:    newTeamRepo(),
		threads:  newThreadRepo(),
		accounts: newAccountRepo(),
		users:    newUserRepo(),
		devices:  newDeviceRepo(),
		push:     newPush(),
		bus:      &collabBusRecorder{},
		shares:   &collabShareStub{shared: map[string]map[string]bool{}},
		clock:    newClock(time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)),
	}
	f.svc = NewCollabService(CollabServiceDeps{
		Comments: f.comments,
		Teams:    f.teams,
		Threads:  f.threads,
		Accounts: f.accounts,
		Users:    f.users,
		Devices:  f.devices,
		Push:     f.push,
		Bus:      f.bus,
		Shares:   f.shares,
		Clock:    f.clock,
		SelfHost: true, // entitlement bypass; gating is covered elsewhere
	})
	return f
}

// seedMemberUser registers a user (email uid@example.com) and their team
// membership.
func (f *collabFixture) seedMemberUser(t *testing.T, teamID, uid string, role domain.TeamRole) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.users.Upsert(ctx, domain.User{ID: uid, Email: uid + "@example.com"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := f.teams.UpsertMember(ctx, domain.TeamMember{
		TeamID: teamID, UserID: uid, Role: role, JoinedAt: f.clock.Now(),
	}); err != nil {
		t.Fatalf("seed member: %v", err)
	}
}

// seedCollabTeam creates team teamID owned by ownerID (with a user record).
func (f *collabFixture) seedCollabTeam(t *testing.T, teamID, ownerID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.teams.Create(ctx,
		domain.Team{ID: teamID, Name: "Team " + teamID, CreatedBy: ownerID, CreatedAt: f.clock.Now()},
		domain.TeamMember{TeamID: teamID, UserID: ownerID, Role: domain.TeamRoleOwner, JoinedAt: f.clock.Now()},
	); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	if _, err := f.users.Upsert(ctx, domain.User{ID: ownerID, Email: ownerID + "@example.com"}); err != nil {
		t.Fatalf("seed owner user: %v", err)
	}
}

// seedOwnedThread creates a thread owned (via its account) by userID.
func (f *collabFixture) seedOwnedThread(t *testing.T, threadID, userID string) {
	t.Helper()
	ctx := context.Background()
	acctID := "acct-" + userID
	if _, err := f.accounts.Create(ctx, domain.ConnectedAccount{ID: acctID, UserID: userID}); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := f.threads.Upsert(ctx, domain.Thread{ID: threadID, AccountID: acctID}); err != nil {
		t.Fatalf("seed thread: %v", err)
	}
}

func (f *collabFixture) shareThread(threadID, teamID string) {
	if f.shares.shared[threadID] == nil {
		f.shares.shared[threadID] = map[string]bool{}
	}
	f.shares.shared[threadID][teamID] = true
}

// --- AddComment --------------------------------------------------------------

func TestCommentAddNonMemberNotFound(t *testing.T) {
	ctx := context.Background()
	f := newCollabFixture(t)
	f.seedCollabTeam(t, "t1", "owner")
	f.seedOwnedThread(t, "th1", "owner")
	f.seedMemberUser(t, "t1", "owner", domain.TeamRoleOwner)

	// stranger is a real user but not a member of t1: 404, never 403.
	f.users.Upsert(ctx, domain.User{ID: "stranger", Email: "stranger@example.com"})
	_, err := f.svc.AddComment(ctx, "stranger", "th1", port.CommentInput{TeamID: "t1", Body: "hi"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound (non-member indistinguishable from missing team)", err)
	}
	if len(f.comments.order) != 0 {
		t.Fatal("comment persisted for a non-member")
	}
}

func TestCommentAddMemberThreadNotVisibleNotFound(t *testing.T) {
	ctx := context.Background()
	f := newCollabFixture(t)
	f.seedCollabTeam(t, "t1", "owner")
	f.seedMemberUser(t, "t1", "bob", domain.TeamRoleMember)
	f.seedOwnedThread(t, "th1", "owner") // owned by owner, never shared to t1

	_, err := f.svc.AddComment(ctx, "bob", "th1", port.CommentInput{TeamID: "t1", Body: "hi"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound (no share, not owner)", err)
	}
	if len(f.comments.order) != 0 {
		t.Fatal("comment persisted without thread visibility")
	}
}

func TestCommentAddOwnerWithoutShareOK(t *testing.T) {
	ctx := context.Background()
	f := newCollabFixture(t)
	f.seedCollabTeam(t, "t1", "owner")
	f.seedOwnedThread(t, "th1", "owner")

	c, err := f.svc.AddComment(ctx, "owner", "th1", port.CommentInput{TeamID: "t1", Body: "note to team"})
	if err != nil {
		t.Fatalf("owner comments own unshared thread: %v", err)
	}
	if c.ID == "" || c.ThreadID != "th1" || c.TeamID != "t1" || c.AuthorID != "owner" {
		t.Fatalf("comment = %+v", c)
	}
	if !c.CreatedAt.Equal(f.clock.Now()) || !c.UpdatedAt.Equal(f.clock.Now()) {
		t.Fatalf("timestamps = %v / %v, want clock now", c.CreatedAt, c.UpdatedAt)
	}
	if len(c.Mentions) != 0 {
		t.Fatalf("mentions = %v, want none", c.Mentions)
	}
}

func TestCommentAddSharedThreadMemberOK(t *testing.T) {
	ctx := context.Background()
	f := newCollabFixture(t)
	f.seedCollabTeam(t, "t1", "owner")
	f.seedMemberUser(t, "t1", "bob", domain.TeamRoleMember)
	f.seedOwnedThread(t, "th1", "owner")
	f.shareThread("th1", "t1")

	if _, err := f.svc.AddComment(ctx, "bob", "th1", port.CommentInput{TeamID: "t1", Body: "hi"}); err != nil {
		t.Fatalf("member comments shared thread: %v", err)
	}
}

func TestCommentAddCrossTeamShareNotVisible(t *testing.T) {
	// The share is for ANOTHER team: t1 members still get 404.
	ctx := context.Background()
	f := newCollabFixture(t)
	f.seedCollabTeam(t, "t1", "owner")
	f.seedCollabTeam(t, "t2", "other")
	f.seedMemberUser(t, "t1", "bob", domain.TeamRoleMember)
	f.seedOwnedThread(t, "th1", "other")
	f.shareThread("th1", "t2")

	if _, err := f.svc.AddComment(ctx, "bob", "th1", port.CommentInput{TeamID: "t1", Body: "hi"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound (share belongs to a different team)", err)
	}
}

func TestCommentAddNonMemberMentionDroppedSilently(t *testing.T) {
	ctx := context.Background()
	f := newCollabFixture(t)
	f.seedCollabTeam(t, "t1", "owner")
	f.seedOwnedThread(t, "th1", "owner")
	// ghost exists as a USER but is not a team member: no cross-tenant
	// enumeration through mentions.
	f.users.Upsert(ctx, domain.User{ID: "ghost", Email: "ghost@example.com"})

	c, err := f.svc.AddComment(ctx, "owner", "th1", port.CommentInput{TeamID: "t1", Body: "cc @ghost@example.com"})
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	if len(c.Mentions) != 0 {
		t.Fatalf("mentions = %v, want none (non-member dropped)", c.Mentions)
	}
	if len(f.push.sent) != 0 {
		t.Fatalf("pushes = %d, want 0", len(f.push.sent))
	}
	if got := f.bus.ofType("mention"); len(got) != 0 {
		t.Fatalf("mention events = %v, want none", got)
	}
}

func TestCommentAddMentionPushPerDeviceAndBusEvents(t *testing.T) {
	ctx := context.Background()
	f := newCollabFixture(t)
	f.seedCollabTeam(t, "t1", "owner")
	f.seedMemberUser(t, "t1", "bob", domain.TeamRoleMember)
	f.seedOwnedThread(t, "th1", "owner")
	name := "Olivia Owner"
	f.users.Upsert(ctx, domain.User{ID: "owner", Email: "owner@example.com", Name: &name})
	for _, tok := range []string{"tok1", "tok2"} {
		if _, err := f.devices.Upsert(ctx, domain.NotificationDevice{UserID: "bob", Token: tok}); err != nil {
			t.Fatalf("seed device: %v", err)
		}
	}

	c, err := f.svc.AddComment(ctx, "owner", "th1", port.CommentInput{TeamID: "t1", Body: "fyi @bob@example.com and me @owner@example.com"})
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	if len(c.Mentions) != 2 || c.Mentions[0] != "bob" || c.Mentions[1] != "owner" {
		t.Fatalf("mentions = %v, want [bob owner]", c.Mentions)
	}

	// One push per registered device of the mentionee; none for the
	// author's self-mention.
	if len(f.push.sent) != 2 {
		t.Fatalf("pushes = %d, want 2 (bob's two devices, none for self-mention)", len(f.push.sent))
	}
	for _, p := range f.push.sent {
		if p.Device.UserID != "bob" {
			t.Fatalf("push went to %s, want bob", p.Device.UserID)
		}
		if p.Title != "Olivia Owner mentioned you" {
			t.Fatalf("push title = %q", p.Title)
		}
		if p.Data["threadId"] != "th1" || p.Data["commentId"] != c.ID || p.Data["teamId"] != "t1" {
			t.Fatalf("push data = %v", p.Data)
		}
	}

	created := f.bus.ofType("comment.created")
	if len(created) != 1 || created[0].Topic != "team:t1" {
		t.Fatalf("comment.created events = %+v, want one on team:t1", created)
	}
	if strings.Contains(string(created[0].Payload), "fyi @bob") {
		t.Fatal("bus payload carries the comment body; ids only")
	}
	mentions := f.bus.ofType("mention")
	if len(mentions) != 1 || mentions[0].Topic != "user:bob" {
		t.Fatalf("mention events = %+v, want one on user:bob", mentions)
	}
}

func TestCommentAddBodyValidation(t *testing.T) {
	ctx := context.Background()
	f := newCollabFixture(t)
	f.seedCollabTeam(t, "t1", "owner")
	f.seedOwnedThread(t, "th1", "owner")

	for _, body := range []string{"", "   ", strings.Repeat("x", domain.MaxCommentBodyChars+1)} {
		if _, err := f.svc.AddComment(ctx, "owner", "th1", port.CommentInput{TeamID: "t1", Body: body}); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("body %d chars err = %v, want ErrValidation", len(body), err)
		}
	}
	if len(f.comments.order) != 0 {
		t.Fatal("invalid comment persisted")
	}
}

// --- UpdateComment -----------------------------------------------------------

func TestCommentUpdateAuthorReResolvesMentionsAndNotifiesNewOnly(t *testing.T) {
	ctx := context.Background()
	f := newCollabFixture(t)
	f.seedCollabTeam(t, "t1", "owner")
	f.seedMemberUser(t, "t1", "bob", domain.TeamRoleMember)
	f.seedMemberUser(t, "t1", "eve", domain.TeamRoleMember)
	f.seedOwnedThread(t, "th1", "owner")
	f.devices.Upsert(ctx, domain.NotificationDevice{UserID: "bob", Token: "tb"})
	f.devices.Upsert(ctx, domain.NotificationDevice{UserID: "eve", Token: "te"})

	c, err := f.svc.AddComment(ctx, "owner", "th1", port.CommentInput{TeamID: "t1", Body: "hi @bob@example.com"})
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	f.push.sent = nil
	f.bus.events = nil
	f.clock.Advance(time.Minute)

	upd, err := f.svc.UpdateComment(ctx, "owner", c.ID, "hi @bob@example.com and @eve@example.com")
	if err != nil {
		t.Fatalf("UpdateComment: %v", err)
	}
	if len(upd.Mentions) != 2 {
		t.Fatalf("mentions = %v, want bob+eve", upd.Mentions)
	}
	if !upd.UpdatedAt.After(upd.CreatedAt) {
		t.Fatalf("UpdatedAt %v not after CreatedAt %v", upd.UpdatedAt, upd.CreatedAt)
	}
	if got := f.bus.ofType("comment.updated"); len(got) != 1 || got[0].Topic != "team:t1" {
		t.Fatalf("comment.updated events = %+v", got)
	}
	// Only eve is newly mentioned: bob must not be re-pushed.
	if len(f.push.sent) != 1 || f.push.sent[0].Device.UserID != "eve" {
		t.Fatalf("pushes = %+v, want exactly one to eve", f.push.sent)
	}
}

func TestCommentUpdateNonAuthorForbidden(t *testing.T) {
	ctx := context.Background()
	f := newCollabFixture(t)
	f.seedCollabTeam(t, "t1", "owner")
	f.seedMemberUser(t, "t1", "bob", domain.TeamRoleMember)
	f.seedMemberUser(t, "t1", "admin", domain.TeamRoleAdmin)
	f.seedOwnedThread(t, "th1", "owner")
	f.shareThread("th1", "t1")

	c, err := f.svc.AddComment(ctx, "bob", "th1", port.CommentInput{TeamID: "t1", Body: "mine"})
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	// Even an admin may not EDIT someone else's comment (delete only).
	if _, err := f.svc.UpdateComment(ctx, "admin", c.ID, "hijack"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("admin edit err = %v, want ErrForbidden", err)
	}
}

func TestCommentUpdateNonMemberNotFound(t *testing.T) {
	ctx := context.Background()
	f := newCollabFixture(t)
	f.seedCollabTeam(t, "t1", "owner")
	f.seedOwnedThread(t, "th1", "owner")
	c, err := f.svc.AddComment(ctx, "owner", "th1", port.CommentInput{TeamID: "t1", Body: "hello"})
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	f.users.Upsert(ctx, domain.User{ID: "outsider", Email: "outsider@example.com"})
	if _, err := f.svc.UpdateComment(ctx, "outsider", c.ID, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-tenant update err = %v, want ErrNotFound", err)
	}
	if err := f.svc.DeleteComment(ctx, "outsider", c.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-tenant delete err = %v, want ErrNotFound", err)
	}
}

func TestCommentUpdateMissingNotFound(t *testing.T) {
	f := newCollabFixture(t)
	if _, err := f.svc.UpdateComment(context.Background(), "anyone", "ghost", "x"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// --- DeleteComment -----------------------------------------------------------

func TestCommentDeleteAuthorAndAdminRules(t *testing.T) {
	ctx := context.Background()
	f := newCollabFixture(t)
	f.seedCollabTeam(t, "t1", "owner")
	f.seedMemberUser(t, "t1", "bob", domain.TeamRoleMember)
	f.seedMemberUser(t, "t1", "carol", domain.TeamRoleMember)
	f.seedMemberUser(t, "t1", "admin", domain.TeamRoleAdmin)
	f.seedOwnedThread(t, "th1", "owner")
	f.shareThread("th1", "t1")

	add := func(author string) domain.Comment {
		c, err := f.svc.AddComment(ctx, author, "th1", port.CommentInput{TeamID: "t1", Body: "by " + author})
		if err != nil {
			t.Fatalf("AddComment(%s): %v", author, err)
		}
		return c
	}

	// Plain member deleting someone else's comment: forbidden.
	c1 := add("bob")
	if err := f.svc.DeleteComment(ctx, "carol", c1.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("member deletes other's err = %v, want ErrForbidden", err)
	}
	// Admin deleting someone else's: allowed.
	if err := f.svc.DeleteComment(ctx, "admin", c1.ID); err != nil {
		t.Fatalf("admin deletes other's: %v", err)
	}
	// Author deleting their own: allowed.
	c2 := add("carol")
	if err := f.svc.DeleteComment(ctx, "carol", c2.ID); err != nil {
		t.Fatalf("author deletes own: %v", err)
	}
	// Deleting an already soft-deleted comment: 404.
	if err := f.svc.DeleteComment(ctx, "carol", c2.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("double delete err = %v, want ErrNotFound", err)
	}

	if got := f.bus.ofType("comment.deleted"); len(got) != 2 {
		t.Fatalf("comment.deleted events = %d, want 2", len(got))
	}
	// Soft-deleted comments vanish from the team's list.
	cs, err := f.svc.ListComments(ctx, "bob", "th1", "t1")
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(cs) != 0 {
		t.Fatalf("list after deletes = %+v, want empty", cs)
	}
}

// --- ListComments ------------------------------------------------------------

func TestCommentListScoping(t *testing.T) {
	ctx := context.Background()
	f := newCollabFixture(t)
	f.seedCollabTeam(t, "t1", "owner")
	f.seedMemberUser(t, "t1", "bob", domain.TeamRoleMember)
	f.seedOwnedThread(t, "th1", "owner")
	f.shareThread("th1", "t1")

	for _, body := range []string{"first", "second"} {
		if _, err := f.svc.AddComment(ctx, "owner", "th1", port.CommentInput{TeamID: "t1", Body: body}); err != nil {
			t.Fatalf("AddComment: %v", err)
		}
	}

	cs, err := f.svc.ListComments(ctx, "bob", "th1", "t1")
	if err != nil {
		t.Fatalf("ListComments as member: %v", err)
	}
	if len(cs) != 2 || cs[0].Body != "first" || cs[1].Body != "second" {
		t.Fatalf("list = %+v, want [first second] oldest-first", cs)
	}

	// Non-member: 404.
	f.users.Upsert(ctx, domain.User{ID: "stranger", Email: "stranger@example.com"})
	if _, err := f.svc.ListComments(ctx, "stranger", "th1", "t1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("non-member list err = %v, want ErrNotFound", err)
	}

	// Member of a team the thread is NOT visible to: 404.
	f.seedCollabTeam(t, "t2", "zoe")
	if _, err := f.svc.ListComments(ctx, "zoe", "th1", "t2"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("invisible-thread list err = %v, want ErrNotFound", err)
	}

	// Unknown thread: 404 (not an oracle).
	if _, err := f.svc.ListComments(ctx, "bob", "ghost", "t1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown thread err = %v, want ErrNotFound", err)
	}
}

func TestCommentEntitlementRequired(t *testing.T) {
	ctx := context.Background()
	f := newCollabFixture(t)
	// Rebuild the service without the self-host bypass and with no
	// subscription on file: every entry point must be 402.
	subs := newSubscriptionRepo()
	f.svc = NewCollabService(CollabServiceDeps{
		Comments: f.comments, Teams: f.teams, Threads: f.threads,
		Accounts: f.accounts, Users: f.users, Devices: f.devices,
		Push: f.push, Bus: f.bus, Shares: f.shares, Subs: subs, Clock: f.clock,
	})
	if _, err := f.svc.AddComment(ctx, "u1", "th1", port.CommentInput{TeamID: "t1", Body: "x"}); !errors.Is(err, domain.ErrPaymentRequired) {
		t.Fatalf("err = %v, want ErrPaymentRequired", err)
	}
}
