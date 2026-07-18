package service

import (
	"context"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// scheduling_fakes_test.go exercises the scheduling in-memory fakes directly
// (fakeBookingLinkRepo, fakeBookingRepo, fakePollRepo, fakeProposalRepo,
// fakeUserSettingsRepo, and fakeCalendarProvider.FreeBusy) ahead of the
// scheduling service landing in a later task, proving the tricky bits mirror
// the real postgres adapter's documented semantics.

func TestFakeBookingLinkRepo_SlugCollisionCaseInsensitive(t *testing.T) {
	ctx := context.Background()
	repo := newBookingLinkRepo()

	if _, err := repo.Create(ctx, domain.BookingLink{ID: "l1", UserID: "u1", Slug: "intro-call"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := repo.Create(ctx, domain.BookingLink{ID: "l2", UserID: "u2", Slug: "Intro-Call"}); err == nil {
		t.Fatal("expected ErrConflict on case-insensitive slug collision, got nil")
	}

	got, err := repo.GetBySlug(ctx, "INTRO-CALL")
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if got.ID != "l1" {
		t.Fatalf("GetBySlug returned %q, want l1", got.ID)
	}

	if _, err := repo.GetBySlug(ctx, "missing"); err != domain.ErrNotFound {
		t.Fatalf("GetBySlug(missing) = %v, want ErrNotFound", err)
	}

	links, err := repo.ListByUser(ctx, "u1")
	if err != nil || len(links) != 1 {
		t.Fatalf("ListByUser = %v, %v; want 1 link", links, err)
	}

	if err := repo.Delete(ctx, "l1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.GetByID(ctx, "l1"); err != domain.ErrNotFound {
		t.Fatalf("GetByID after delete = %v, want ErrNotFound", err)
	}
}

func TestFakeBookingRepo_CreateHoldOverlapConflict(t *testing.T) {
	ctx := context.Background()
	links := newBookingLinkRepo()
	repo := newBookingRepo(links)

	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	first := domain.Booking{
		ID: "b1", LinkID: "link1", Status: domain.BookingHold,
		Start: base, End: base.Add(30 * time.Minute),
	}
	if _, err := repo.CreateHold(ctx, first); err != nil {
		t.Fatalf("CreateHold(first): %v", err)
	}

	overlapping := domain.Booking{
		ID: "b2", LinkID: "link1", Status: domain.BookingHold,
		Start: base.Add(15 * time.Minute), End: base.Add(45 * time.Minute),
	}
	if _, err := repo.CreateHold(ctx, overlapping); err != domain.ErrConflict {
		t.Fatalf("CreateHold(overlapping) = %v, want ErrConflict", err)
	}

	// A different link is unaffected by the overlap.
	otherLink := domain.Booking{
		ID: "b3", LinkID: "link2", Status: domain.BookingHold,
		Start: base, End: base.Add(30 * time.Minute),
	}
	if _, err := repo.CreateHold(ctx, otherLink); err != nil {
		t.Fatalf("CreateHold(other link): %v", err)
	}

	// Back-to-back (touching, not overlapping) slots are allowed.
	adjacent := domain.Booking{
		ID: "b4", LinkID: "link1", Status: domain.BookingHold,
		Start: base.Add(30 * time.Minute), End: base.Add(60 * time.Minute),
	}
	if _, err := repo.CreateHold(ctx, adjacent); err != nil {
		t.Fatalf("CreateHold(adjacent): %v", err)
	}

	active, err := repo.ListActiveInRange(ctx, "link1", base, base.Add(2*time.Hour))
	if err != nil || len(active) != 2 {
		t.Fatalf("ListActiveInRange = %v, %v; want 2 active bookings", active, err)
	}

	if err := repo.Confirm(ctx, "b1", "event-1"); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	confirmed, err := repo.GetByID(ctx, "b1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if confirmed.Status != domain.BookingConfirmed || confirmed.EventID == nil || *confirmed.EventID != "event-1" || confirmed.HoldExpiresAt != nil {
		t.Fatalf("Confirm did not update booking as expected: %+v", confirmed)
	}

	if err := repo.Cancel(ctx, "b4"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	cancelled, _ := repo.GetByID(ctx, "b4")
	if cancelled.Status != domain.BookingCancelled {
		t.Fatalf("Cancel did not update status, got %q", cancelled.Status)
	}

	// A cancelled slot no longer blocks new holds over the same window.
	if _, err := repo.CreateHold(ctx, domain.Booking{
		ID: "b5", LinkID: "link1", Status: domain.BookingHold,
		Start: adjacent.Start, End: adjacent.End,
	}); err != nil {
		t.Fatalf("CreateHold over cancelled slot: %v", err)
	}
}

func TestFakeBookingRepo_ExpireHoldsAndListByUser(t *testing.T) {
	ctx := context.Background()
	links := newBookingLinkRepo()
	if _, err := links.Create(ctx, domain.BookingLink{ID: "link1", UserID: "owner1", Slug: "a"}); err != nil {
		t.Fatalf("seed link: %v", err)
	}
	repo := newBookingRepo(links)

	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Minute)
	future := now.Add(time.Hour)

	if _, err := repo.CreateHold(ctx, domain.Booking{
		ID: "expired", LinkID: "link1", Status: domain.BookingHold,
		Start: now, End: now.Add(30 * time.Minute), HoldExpiresAt: &expired,
	}); err != nil {
		t.Fatalf("CreateHold(expired): %v", err)
	}
	if _, err := repo.CreateHold(ctx, domain.Booking{
		ID: "fresh", LinkID: "link1", Status: domain.BookingHold,
		Start: now.Add(time.Hour), End: now.Add(90 * time.Minute), HoldExpiresAt: &future,
	}); err != nil {
		t.Fatalf("CreateHold(fresh): %v", err)
	}

	count, err := repo.ExpireHolds(ctx, now)
	if err != nil || count != 1 {
		t.Fatalf("ExpireHolds = %d, %v; want 1, nil", count, err)
	}
	got, _ := repo.GetByID(ctx, "expired")
	if got.Status != domain.BookingCancelled {
		t.Fatalf("expired hold status = %q, want cancelled", got.Status)
	}
	stillFresh, _ := repo.GetByID(ctx, "fresh")
	if stillFresh.Status != domain.BookingHold {
		t.Fatalf("fresh hold status = %q, want hold (unaffected)", stillFresh.Status)
	}

	byUser, err := repo.ListByUser(ctx, "owner1", 0)
	if err != nil || len(byUser) != 2 {
		t.Fatalf("ListByUser = %v, %v; want 2 bookings scoped via link owner", byUser, err)
	}
	if _, err := repo.ListByUser(ctx, "someone-else", 0); err != nil {
		t.Fatalf("ListByUser(someone-else): %v", err)
	}
}

func TestFakePollRepo_CreateTokenVotesAndDelete(t *testing.T) {
	ctx := context.Background()
	repo := newPollRepo()

	p, err := repo.Create(ctx, domain.MeetingPoll{
		UserID: "u1", Title: "Sync",
		Options: []domain.PollOption{{ID: "opt1"}, {ID: "opt2"}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.ID == "" || p.Token == "" {
		t.Fatalf("Create did not assign ID/Token: %+v", p)
	}

	byToken, err := repo.GetByToken(ctx, p.Token)
	if err != nil || byToken.ID != p.ID {
		t.Fatalf("GetByToken = %+v, %v", byToken, err)
	}

	if _, err := repo.GetByToken(ctx, "nonexistent"); err != domain.ErrNotFound {
		t.Fatalf("GetByToken(missing) = %v, want ErrNotFound", err)
	}

	votes := []domain.PollVote{
		{PollID: p.ID, OptionID: "opt1", VoterEmail: "Alice@Example.com", Choice: domain.VoteYes},
	}
	if err := repo.UpsertVotes(ctx, votes); err != nil {
		t.Fatalf("UpsertVotes: %v", err)
	}
	// Replace the same voter's ballot for the same option (case-insensitive
	// email key) — the second write must win, not append.
	if err := repo.UpsertVotes(ctx, []domain.PollVote{
		{PollID: p.ID, OptionID: "opt1", VoterEmail: "alice@example.com", Choice: domain.VoteNo},
	}); err != nil {
		t.Fatalf("UpsertVotes(replace): %v", err)
	}

	got, err := repo.ListVotes(ctx, p.ID)
	if err != nil || len(got) != 1 || got[0].Choice != domain.VoteNo {
		t.Fatalf("ListVotes = %+v, %v; want single replaced ballot", got, err)
	}

	polls, err := repo.ListByUser(ctx, "u1")
	if err != nil || len(polls) != 1 {
		t.Fatalf("ListByUser = %v, %v; want 1 poll", polls, err)
	}

	if err := repo.Delete(ctx, p.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.GetByID(ctx, p.ID); err != domain.ErrNotFound {
		t.Fatalf("GetByID after delete = %v, want ErrNotFound", err)
	}
	if remaining, _ := repo.ListVotes(ctx, p.ID); len(remaining) != 0 {
		t.Fatalf("votes not cascaded on Delete: %v", remaining)
	}
}

func TestFakeProposalRepo_CreateListUpdate(t *testing.T) {
	ctx := context.Background()
	repo := newProposalRepo()

	p, err := repo.Create(ctx, domain.TimeProposal{
		EventID: "evt1", ProposerEmail: "bob@example.com", Status: domain.ProposalPending,
	})
	if err != nil || p.ID == "" {
		t.Fatalf("Create = %+v, %v", p, err)
	}

	list, err := repo.ListByEvent(ctx, "evt1")
	if err != nil || len(list) != 1 {
		t.Fatalf("ListByEvent = %v, %v; want 1", list, err)
	}

	p.Status = domain.ProposalAccepted
	if err := repo.Update(ctx, p); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := repo.GetByID(ctx, p.ID)
	if err != nil || got.Status != domain.ProposalAccepted {
		t.Fatalf("GetByID after update = %+v, %v", got, err)
	}

	if err := repo.Update(ctx, domain.TimeProposal{ID: "missing"}); err != domain.ErrNotFound {
		t.Fatalf("Update(missing) = %v, want ErrNotFound", err)
	}
}

func TestFakeUserSettingsRepo_DefaultsAndUpsert(t *testing.T) {
	ctx := context.Background()
	repo := newUserSettingsRepo()

	got, err := repo.Get(ctx, "u1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.TimeZone != "UTC" || len(got.WorkingHours) != 0 {
		t.Fatalf("Get default = %+v, want zero-value with TimeZone UTC", got)
	}

	want := domain.UserSettings{UserID: "u1", TimeZone: "America/New_York", WorkingLocation: "home"}
	if err := repo.Upsert(ctx, want); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err = repo.Get(ctx, "u1")
	if err != nil || got.TimeZone != "America/New_York" || got.WorkingLocation != "home" {
		t.Fatalf("Get after Upsert = %+v, %v", got, err)
	}
}

func TestFakeCalendarProvider_FreeBusy(t *testing.T) {
	ctx := context.Background()
	p := newCalendarProvider()

	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	p.freeBusyResult = map[string][]domain.BusyInterval{
		"alice@example.com": {{Start: from.Add(9 * time.Hour), End: from.Add(10 * time.Hour)}},
	}

	got, err := p.FreeBusy(ctx, "token", []string{"alice@example.com", "unresolvable@example.com"}, from, to)
	if err != nil {
		t.Fatalf("FreeBusy: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("FreeBusy result = %v, want only the resolvable email", got)
	}
	if len(p.lastFreeBusyEmails) != 2 || p.lastFreeBusyFrom != from || p.lastFreeBusyTo != to {
		t.Fatalf("FreeBusy did not record args: emails=%v from=%v to=%v", p.lastFreeBusyEmails, p.lastFreeBusyFrom, p.lastFreeBusyTo)
	}

	p.freeBusyErr = domain.ErrUnauthorized
	if _, err := p.FreeBusy(ctx, "token", nil, from, to); err != domain.ErrUnauthorized {
		t.Fatalf("FreeBusy error injection = %v, want ErrUnauthorized", err)
	}
}
