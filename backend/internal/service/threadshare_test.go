package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- fakes (Task 7: kept in this file, not fakes_test.go, so parallel
// --- feature branches don't collide on the shared fixture file) --------------

type fakeThreadShareRepo struct {
	byID  map[string]domain.ThreadShare
	order []string
}

var _ port.ThreadShareRepo = (*fakeThreadShareRepo)(nil)

func newThreadShareRepo() *fakeThreadShareRepo {
	return &fakeThreadShareRepo{byID: map[string]domain.ThreadShare{}}
}

func (r *fakeThreadShareRepo) Create(_ context.Context, sh domain.ThreadShare) (domain.ThreadShare, error) {
	for _, existing := range r.byID {
		if existing.TokenHash == sh.TokenHash {
			return domain.ThreadShare{}, domain.ErrConflict
		}
	}
	r.byID[sh.ID] = sh
	r.order = append(r.order, sh.ID)
	return sh, nil
}

func (r *fakeThreadShareRepo) GetByID(_ context.Context, id string) (domain.ThreadShare, error) {
	sh, ok := r.byID[id]
	if !ok {
		return domain.ThreadShare{}, domain.ErrNotFound
	}
	return sh, nil
}

func (r *fakeThreadShareRepo) GetByTokenHash(_ context.Context, tokenHash string) (domain.ThreadShare, error) {
	for _, sh := range r.byID {
		if sh.TokenHash == tokenHash {
			return sh, nil
		}
	}
	return domain.ThreadShare{}, domain.ErrNotFound
}

func (r *fakeThreadShareRepo) ListByThread(_ context.Context, threadID string) ([]domain.ThreadShare, error) {
	out := []domain.ThreadShare{}
	for _, id := range r.order {
		if sh := r.byID[id]; sh.ThreadID == threadID {
			out = append(out, sh)
		}
	}
	return out, nil
}

func (r *fakeThreadShareRepo) Revoke(_ context.Context, id string, at time.Time) error {
	sh, ok := r.byID[id]
	if !ok {
		return domain.ErrNotFound
	}
	if sh.RevokedAt == nil {
		sh.RevokedAt = &at
		r.byID[id] = sh
	}
	return nil
}

// fakeCollabBus captures published events (Subscribe is unused here).
type fakeCollabBus struct{ events []port.CollabEvent }

var _ port.EventBus = (*fakeCollabBus)(nil)

func (b *fakeCollabBus) Publish(ev port.CollabEvent) { b.events = append(b.events, ev) }
func (b *fakeCollabBus) Subscribe([]string) (<-chan port.CollabEvent, func()) {
	ch := make(chan port.CollabEvent)
	return ch, func() {}
}

// --- fixture ------------------------------------------------------------------

// collabFixture wires a CollabService over the shared package fakes:
// thread t1 (account a1, user u1, one normal + one Bcc-carrying + one draft
// message), foreign thread t2 (account a2, user u2), and team team1 whose
// members are u1 (owner) and u3 (member) — u2 is NOT a member.
type collabFixture struct {
	svc      *CollabService
	shares   *fakeThreadShareRepo
	teams    *fakeTeamRepo
	threads  *fakeThreadRepo
	messages *fakeMessageRepo
	accounts *fakeAccountRepo
	clock    *fakeClock
	base     time.Time
}

func newCollabFixture(t *testing.T) *collabFixture {
	t.Helper()
	ctx := context.Background()
	base := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	clock := newClock(base)

	accounts := newAccountRepo()
	for _, a := range []domain.ConnectedAccount{
		{ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "owner@x.com"},
		{ID: "a2", UserID: "u2", Provider: domain.ProviderGoogle, Email: "other@x.com"},
	} {
		if _, err := accounts.Create(ctx, a); err != nil {
			t.Fatalf("seed account: %v", err)
		}
	}

	threads := newThreadRepo()
	for _, th := range []domain.Thread{
		{ID: "t1", AccountID: "a1", Subject: "Launch plan", Split: domain.SplitImportant, LastMessageAt: base.Add(-time.Hour)},
		{ID: "t2", AccountID: "a2", Subject: "Foreign", LastMessageAt: base.Add(-time.Hour)},
	} {
		if _, err := threads.Upsert(ctx, th); err != nil {
			t.Fatalf("seed thread: %v", err)
		}
	}

	opened := base.Add(-30 * time.Minute)
	messages := newMessageRepo()
	for _, m := range []domain.Message{
		{ID: "m1", ThreadID: "t1", AccountID: "a1",
			From:    domain.EmailAddress{Email: "owner@x.com"},
			To:      []domain.EmailAddress{{Email: "pal@x.com"}},
			Bcc:     []domain.EmailAddress{{Email: "secret@x.com"}},
			Subject: "Launch plan", BodyText: "hello",
			SentAt: base.Add(-time.Hour), OpenedAt: &opened},
		{ID: "m2", ThreadID: "t1", AccountID: "a1",
			From:   domain.EmailAddress{Email: "pal@x.com"},
			SentAt: base.Add(-45 * time.Minute)},
		{ID: "m3", ThreadID: "t1", AccountID: "a1", IsDraft: true,
			BodyText: "unsent secret", SentAt: base.Add(-time.Minute)},
	} {
		if _, err := messages.Upsert(ctx, m); err != nil {
			t.Fatalf("seed message: %v", err)
		}
	}

	teams := newTeamRepo()
	if _, err := teams.Create(ctx, domain.Team{ID: "team1", Name: "Acme", CreatedBy: "u1"},
		domain.TeamMember{TeamID: "team1", UserID: "u1", Role: domain.TeamRoleOwner}); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	if err := teams.UpsertMember(ctx, domain.TeamMember{TeamID: "team1", UserID: "u3", Role: domain.TeamRoleMember}); err != nil {
		t.Fatalf("seed member: %v", err)
	}

	shares := newThreadShareRepo()
	svc := NewCollabService(CollabServiceDeps{
		Shares:   shares,
		Teams:    teams,
		Threads:  threads,
		Messages: messages,
		Accounts: accounts,
		Clock:    clock,
		SelfHost: true, // paywall exercised in its own test
	})
	return &collabFixture{svc: svc, shares: shares, teams: teams, threads: threads,
		messages: messages, accounts: accounts, clock: clock, base: base}
}

func (f *collabFixture) share(t *testing.T, userID, threadID string, in port.ShareThreadInput) (domain.ThreadShare, string) {
	t.Helper()
	sh, raw, err := f.svc.ShareThread(context.Background(), userID, threadID, in)
	if err != nil {
		t.Fatalf("ShareThread: %v", err)
	}
	return sh, raw
}

// --- tests -------------------------------------------------------------------

func TestCollabShareOwnThread(t *testing.T) {
	f := newCollabFixture(t)
	ctx := context.Background()

	sh, raw := f.share(t, "u1", "t1", port.ShareThreadInput{Audience: domain.ShareAudienceExternal})

	if len(raw) != 64 {
		t.Fatalf("raw token length = %d, want 64 hex chars", len(raw))
	}
	sum := sha256.Sum256([]byte(raw))
	if sh.TokenHash != hex.EncodeToString(sum[:]) {
		t.Fatalf("TokenHash = %q, want sha256(raw)", sh.TokenHash)
	}
	if _, err := f.shares.GetByTokenHash(ctx, raw); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("raw token stored verbatim; only the hash may be persisted")
	}
	if sh.ThreadID != "t1" || sh.CreatedBy != "u1" || sh.Audience != domain.ShareAudienceExternal ||
		sh.TeamID != nil || sh.RevokedAt != nil || sh.ExpiresAt != nil || !sh.CreatedAt.Equal(f.base) {
		t.Fatalf("share = %+v", sh)
	}

	// The hash never serializes (json:"-"): shares are safe to return as-is.
	blob, err := json.Marshal(sh)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), sh.TokenHash) || strings.Contains(strings.ToLower(string(blob)), "tokenhash") {
		t.Fatalf("TokenHash leaked into JSON: %s", blob)
	}
}

func TestCollabShareForeignOrMissingThreadNotFound(t *testing.T) {
	f := newCollabFixture(t)
	ctx := context.Background()
	in := port.ShareThreadInput{Audience: domain.ShareAudienceExternal}

	if _, _, err := f.svc.ShareThread(ctx, "u1", "t2", in); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign thread err = %v, want ErrNotFound", err)
	}
	if _, _, err := f.svc.ShareThread(ctx, "u1", "ghost", in); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing thread err = %v, want ErrNotFound", err)
	}
}

func TestCollabShareValidation(t *testing.T) {
	f := newCollabFixture(t)
	ctx := context.Background()

	if _, _, err := f.svc.ShareThread(ctx, "u1", "t1", port.ShareThreadInput{Audience: "everyone"}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("bad audience err = %v, want ErrValidation", err)
	}
	if _, _, err := f.svc.ShareThread(ctx, "u1", "t1", port.ShareThreadInput{Audience: domain.ShareAudienceTeam}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("team share without teamId err = %v, want ErrValidation", err)
	}
	if _, _, err := f.svc.ShareThread(ctx, "u1", "t1", port.ShareThreadInput{Audience: domain.ShareAudienceExternal, TeamID: "team1"}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("external share with teamId err = %v, want ErrValidation", err)
	}
	past := f.base.Add(-time.Minute)
	if _, _, err := f.svc.ShareThread(ctx, "u1", "t1", port.ShareThreadInput{Audience: domain.ShareAudienceExternal, ExpiresAt: &past}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("past expiry err = %v, want ErrValidation", err)
	}
}

func TestCollabShareTeamRequiresMembership(t *testing.T) {
	f := newCollabFixture(t)
	ctx := context.Background()

	// u2 owns t2 but is not a member of team1: existence never leaks.
	if _, _, err := f.svc.ShareThread(ctx, "u2", "t2", port.ShareThreadInput{
		Audience: domain.ShareAudienceTeam, TeamID: "team1",
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("non-member team share err = %v, want ErrNotFound", err)
	}

	sh, _ := f.share(t, "u1", "t1", port.ShareThreadInput{Audience: domain.ShareAudienceTeam, TeamID: "team1"})
	if sh.TeamID == nil || *sh.TeamID != "team1" {
		t.Fatalf("TeamID = %v, want team1", sh.TeamID)
	}
}

func TestCollabSharePaywalled(t *testing.T) {
	f := newCollabFixture(t)
	gated := NewCollabService(CollabServiceDeps{
		Shares: f.shares, Teams: f.teams, Threads: f.threads,
		Messages: f.messages, Accounts: f.accounts,
		Subs: notFoundSubs{}, Clock: f.clock, SelfHost: false,
	})
	if _, _, err := gated.ShareThread(context.Background(), "u1", "t1",
		port.ShareThreadInput{Audience: domain.ShareAudienceExternal}); !errors.Is(err, domain.ErrPaymentRequired) {
		t.Fatalf("err = %v, want ErrPaymentRequired", err)
	}
}

func TestCollabShareExternalViewStripsPrivateData(t *testing.T) {
	f := newCollabFixture(t)
	ctx := context.Background()
	_, raw := f.share(t, "u1", "t1", port.ShareThreadInput{Audience: domain.ShareAudienceExternal})

	view, err := f.svc.GetSharedThread(ctx, raw, nil)
	if err != nil {
		t.Fatalf("GetSharedThread: %v", err)
	}
	if view.Subject != "Launch plan" || view.Audience != domain.ShareAudienceExternal {
		t.Fatalf("view = %+v", view)
	}
	if !view.UpdatedAt.Equal(f.base.Add(-time.Hour)) {
		t.Fatalf("UpdatedAt = %v, want thread LastMessageAt", view.UpdatedAt)
	}
	if len(view.Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (draft m3 dropped)", len(view.Messages))
	}
	for _, m := range view.Messages {
		if m.IsDraft {
			t.Fatal("draft leaked into share view")
		}
		if len(m.Bcc) != 0 {
			t.Fatalf("Bcc leaked into share view: %+v", m.Bcc)
		}
		if m.Bcc == nil {
			t.Fatal("Bcc must be an empty slice, not nil (serializes as [])")
		}
		if m.OpenedAt != nil {
			t.Fatal("read receipt (OpenedAt) leaked into share view")
		}
		if len(m.Reactions) != 0 {
			t.Fatal("reactions leaked into share view")
		}
	}
}

func TestCollabShareTeamViewMembership(t *testing.T) {
	f := newCollabFixture(t)
	ctx := context.Background()
	_, raw := f.share(t, "u1", "t1", port.ShareThreadInput{Audience: domain.ShareAudienceTeam, TeamID: "team1"})

	if _, err := f.svc.GetSharedThread(ctx, raw, nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("anonymous viewer err = %v, want ErrNotFound", err)
	}
	if _, err := f.svc.GetSharedThread(ctx, raw, ptr("u2")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("non-member viewer err = %v, want ErrNotFound", err)
	}
	view, err := f.svc.GetSharedThread(ctx, raw, ptr("u3"))
	if err != nil {
		t.Fatalf("member viewer: %v", err)
	}
	if view.Audience != domain.ShareAudienceTeam {
		t.Fatalf("audience = %q", view.Audience)
	}
}

func TestCollabShareRevokedExpiredUnknownAreUniform404(t *testing.T) {
	f := newCollabFixture(t)
	ctx := context.Background()

	if _, err := f.svc.GetSharedThread(ctx, "no-such-token", nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown token err = %v, want ErrNotFound", err)
	}

	sh, raw := f.share(t, "u1", "t1", port.ShareThreadInput{Audience: domain.ShareAudienceExternal})
	if err := f.svc.RevokeThreadShare(ctx, "u1", "t1", sh.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := f.svc.GetSharedThread(ctx, raw, nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revoked token err = %v, want ErrNotFound", err)
	}

	exp := f.base.Add(time.Hour)
	_, raw2 := f.share(t, "u1", "t1", port.ShareThreadInput{Audience: domain.ShareAudienceExternal, ExpiresAt: &exp})
	if _, err := f.svc.GetSharedThread(ctx, raw2, nil); err != nil {
		t.Fatalf("pre-expiry view: %v", err)
	}
	f.clock.Advance(2 * time.Hour)
	if _, err := f.svc.GetSharedThread(ctx, raw2, nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expired token err = %v, want ErrNotFound", err)
	}
}

func TestCollabShareListAndRevokeRequireOwnership(t *testing.T) {
	f := newCollabFixture(t)
	ctx := context.Background()
	sh, _ := f.share(t, "u1", "t1", port.ShareThreadInput{Audience: domain.ShareAudienceExternal})

	// List: owner sees it, non-owner gets 404.
	shares, err := f.svc.ListThreadShares(ctx, "u1", "t1")
	if err != nil || len(shares) != 1 || shares[0].ID != sh.ID {
		t.Fatalf("ListThreadShares = %+v, %v", shares, err)
	}
	if _, err := f.svc.ListThreadShares(ctx, "u2", "t1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("non-owner list err = %v, want ErrNotFound", err)
	}

	// Revoke: non-owner 404; mismatched thread/share 404; owner succeeds.
	if err := f.svc.RevokeThreadShare(ctx, "u2", "t1", sh.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("non-owner revoke err = %v, want ErrNotFound", err)
	}
	if err := f.svc.RevokeThreadShare(ctx, "u2", "t2", sh.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-thread revoke err = %v, want ErrNotFound", err)
	}
	if err := f.svc.RevokeThreadShare(ctx, "u1", "t1", sh.ID); err != nil {
		t.Fatalf("owner revoke: %v", err)
	}
	got, _ := f.shares.GetByID(ctx, sh.ID)
	if got.RevokedAt == nil {
		t.Fatal("RevokedAt not stamped")
	}
}

func TestCollabShareResolveShare(t *testing.T) {
	f := newCollabFixture(t)
	ctx := context.Background()
	sh, raw := f.share(t, "u1", "t1", port.ShareThreadInput{Audience: domain.ShareAudienceTeam, TeamID: "team1"})

	if _, err := f.svc.ResolveShare(ctx, raw, nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("anonymous resolve err = %v, want ErrNotFound", err)
	}
	id, err := f.svc.ResolveShare(ctx, raw, ptr("u3"))
	if err != nil || id != sh.ID {
		t.Fatalf("ResolveShare = %q, %v; want %q", id, err, sh.ID)
	}
	if _, err := f.svc.ResolveShare(ctx, "bogus", ptr("u3")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown resolve err = %v, want ErrNotFound", err)
	}
}

func TestCollabSharePublishShareUpdates(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	shares := newThreadShareRepo()
	seed := func(id, threadID string, revoked, expired bool) {
		sh := domain.ThreadShare{ID: id, ThreadID: threadID, Audience: domain.ShareAudienceExternal, TokenHash: "h-" + id}
		if revoked {
			at := base.Add(-time.Minute)
			sh.RevokedAt = &at
		}
		if expired {
			at := base.Add(-time.Minute)
			sh.ExpiresAt = &at
		}
		if _, err := shares.Create(ctx, sh); err != nil {
			t.Fatal(err)
		}
	}
	seed("live", "t1", false, false)
	seed("revoked", "t1", true, false)
	seed("expired", "t1", false, true)
	seed("other", "t2", false, false)

	bus := &fakeCollabBus{}
	svc := NewSyncService(SyncServiceDeps{Clock: newClock(base), Shares: shares, Bus: bus})
	svc.publishShareUpdates(ctx, map[string]struct{}{"t1": {}})

	if len(bus.events) != 1 {
		t.Fatalf("published %d events, want 1 (live share only): %+v", len(bus.events), bus.events)
	}
	ev := bus.events[0]
	if ev.Topic != "share:live" || ev.Type != "share.updated" {
		t.Fatalf("event = %+v, want topic share:live type share.updated", ev)
	}

	// Nil bus/shares: the hook is a silent no-op (worker default wiring).
	NewSyncService(SyncServiceDeps{Clock: newClock(base)}).publishShareUpdates(ctx, map[string]struct{}{"t1": {}})
}
