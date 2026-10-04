package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// seedBookingLink creates a booking link owned by userID against a freshly
// seeded account+calendar, so bookings tests don't need to care about the
// FK chain.
func seedBookingLink(t *testing.T, st *Store, userID, slug string) domain.BookingLink {
	t.Helper()
	acct := seedAccount(t, st, userID)
	cal := seedCalendar(t, st, acct.ID)
	l, err := st.BookingLinks().Create(context.Background(), domain.BookingLink{
		UserID: userID, Slug: slug, Title: "Intro Call",
		CalendarID: cal.ID, DurationMinutes: 30, TimeZone: "UTC",
	})
	if err != nil {
		t.Fatalf("seed booking link: %v", err)
	}
	return l
}

func TestBookingLinkCRUDAndSlugUniqueness(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	link := seedBookingLink(t, st, "u1", "intro-call")

	if link.ID == "" || link.CreatedAt.IsZero() {
		t.Fatalf("Create did not populate ID/CreatedAt: %+v", link)
	}

	byID, err := st.BookingLinks().GetByID(ctx, link.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if byID.Slug != "intro-call" || len(byID.Windows) != 0 {
		t.Fatalf("GetByID = %+v", byID)
	}

	// Case-insensitive slug lookup.
	bySlug, err := st.BookingLinks().GetBySlug(ctx, "INTRO-CALL")
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if bySlug.ID != link.ID {
		t.Fatalf("GetBySlug = %+v, want id %q", bySlug, link.ID)
	}

	if _, err := st.BookingLinks().GetBySlug(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetBySlug(missing) = %v, want ErrNotFound", err)
	}

	// Create("Foo") after create("foo") collides case-insensitively.
	seedUser(t, st, "u2")
	if _, err := st.BookingLinks().Create(ctx, domain.BookingLink{
		UserID: "u2", Slug: "INTRO-CALL", Title: "Dup", CalendarID: link.CalendarID,
		DurationMinutes: 15, TimeZone: "UTC",
	}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Create(duplicate slug) = %v, want ErrConflict", err)
	}

	links, err := st.BookingLinks().ListByUser(ctx, "u1")
	if err != nil || len(links) != 1 {
		t.Fatalf("ListByUser = %v, %v; want 1 link", links, err)
	}

	link.Title = "Renamed"
	link.Windows = []domain.AvailabilityWindow{{Weekday: 1, Start: "09:00", End: "17:00"}}
	if err := st.BookingLinks().Update(ctx, link); err != nil {
		t.Fatalf("Update: %v", err)
	}
	updated, err := st.BookingLinks().GetByID(ctx, link.ID)
	if err != nil || updated.Title != "Renamed" || len(updated.Windows) != 1 {
		t.Fatalf("GetByID after update = %+v, %v", updated, err)
	}

	if err := st.BookingLinks().Delete(ctx, link.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := st.BookingLinks().GetByID(ctx, link.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByID after delete = %v, want ErrNotFound", err)
	}
}

func TestBookingHoldOverlapConflict(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	link := seedBookingLink(t, st, "u1", "intro-call")

	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	first := domain.Booking{
		LinkID: link.ID, Start: base, End: base.Add(30 * time.Minute),
		InviteeName: "Alice", InviteeEmail: "alice@example.com", InviteeTZ: "UTC",
	}
	created, err := st.Bookings().CreateHold(ctx, first)
	if err != nil {
		t.Fatalf("CreateHold(first): %v", err)
	}
	if created.Status != domain.BookingHold {
		t.Fatalf("Status = %q, want hold", created.Status)
	}

	// Overlapping hold on the same link is rejected by the DB exclusion
	// constraint, mapped to domain.ErrConflict.
	overlapping := domain.Booking{
		LinkID: link.ID, Start: base.Add(15 * time.Minute), End: base.Add(45 * time.Minute),
		InviteeName: "Bob", InviteeEmail: "bob@example.com", InviteeTZ: "UTC",
	}
	if _, err := st.Bookings().CreateHold(ctx, overlapping); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("CreateHold(overlapping) = %v, want ErrConflict", err)
	}

	// Non-overlapping (back-to-back) hold succeeds.
	adjacent := domain.Booking{
		LinkID: link.ID, Start: base.Add(30 * time.Minute), End: base.Add(60 * time.Minute),
		InviteeName: "Carol", InviteeEmail: "carol@example.com", InviteeTZ: "UTC",
	}
	if _, err := st.Bookings().CreateHold(ctx, adjacent); err != nil {
		t.Fatalf("CreateHold(adjacent): %v", err)
	}

	// Cancel the first hold, then the same window is bookable again — the
	// exclusion constraint only scopes hold|confirmed rows.
	if err := st.Bookings().Cancel(ctx, created.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	reused := domain.Booking{
		LinkID: link.ID, Start: base, End: base.Add(30 * time.Minute),
		InviteeName: "Dave", InviteeEmail: "dave@example.com", InviteeTZ: "UTC",
	}
	if _, err := st.Bookings().CreateHold(ctx, reused); err != nil {
		t.Fatalf("CreateHold over cancelled slot: %v", err)
	}

	active, err := st.Bookings().ListActiveInRange(ctx, link.ID, base, base.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("ListActiveInRange: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("ListActiveInRange = %d bookings, want 2 (adjacent + reused, cancelled excluded)", len(active))
	}
}

func TestBookingHoldConcurrent(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	link := seedBookingLink(t, st, "u1", "intro-call")

	base := time.Date(2026, 8, 2, 9, 0, 0, 0, time.UTC)
	const attempts = 10

	var wg sync.WaitGroup
	var mu sync.Mutex
	var successes, conflicts int

	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := st.Bookings().CreateHold(ctx, domain.Booking{
				LinkID: link.ID, Start: base, End: base.Add(30 * time.Minute),
				InviteeName: "Racer", InviteeEmail: "racer@example.com", InviteeTZ: "UTC",
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				successes++
			case errors.Is(err, domain.ErrConflict):
				conflicts++
			default:
				t.Errorf("goroutine %d: unexpected error: %v", n, err)
			}
		}(i)
	}
	wg.Wait()

	if successes != 1 {
		t.Fatalf("successes = %d, want exactly 1", successes)
	}
	if conflicts != attempts-1 {
		t.Fatalf("conflicts = %d, want %d", conflicts, attempts-1)
	}
}

func TestConfirmExpiredHoldConflict(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	link := seedBookingLink(t, st, "u1", "intro-call")

	base := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	b, err := st.Bookings().CreateHold(ctx, domain.Booking{
		LinkID: link.ID, Start: base, End: base.Add(30 * time.Minute),
		InviteeName: "Alice", InviteeEmail: "alice@example.com", InviteeTZ: "UTC",
	})
	if err != nil {
		t.Fatalf("CreateHold: %v", err)
	}

	// Simulate the hold expiring (worker cancels it) before Confirm runs.
	if err := st.Bookings().Cancel(ctx, b.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	if err := st.Bookings().Confirm(ctx, b.ID, "event-1"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Confirm(cancelled hold) = %v, want ErrConflict", err)
	}

	// A live hold confirms cleanly. event_id is FK'd to events, so the
	// mirrored event must actually exist.
	evt, err := st.Events().Upsert(ctx, domain.Event{
		CalendarID: link.CalendarID, ProviderEventID: newID(), Title: "Intro Call",
		Start: base.Add(time.Hour), End: base.Add(90 * time.Minute),
	})
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}
	live, err := st.Bookings().CreateHold(ctx, domain.Booking{
		LinkID: link.ID, Start: base.Add(time.Hour), End: base.Add(90 * time.Minute),
		InviteeName: "Bob", InviteeEmail: "bob@example.com", InviteeTZ: "UTC",
	})
	if err != nil {
		t.Fatalf("CreateHold(live): %v", err)
	}
	if err := st.Bookings().Confirm(ctx, live.ID, evt.ID); err != nil {
		t.Fatalf("Confirm(live): %v", err)
	}
	confirmed, err := st.Bookings().GetByID(ctx, live.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if confirmed.Status != domain.BookingConfirmed || confirmed.EventID == nil || *confirmed.EventID != evt.ID {
		t.Fatalf("Confirm did not update booking: %+v", confirmed)
	}
	if confirmed.HoldExpiresAt != nil {
		t.Fatalf("HoldExpiresAt = %v, want nil after confirm", confirmed.HoldExpiresAt)
	}

	// Confirming an unknown id is also a conflict, not a crash.
	if err := st.Bookings().Confirm(ctx, "nonexistent", "event-3"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Confirm(missing) = %v, want ErrConflict", err)
	}
}

func TestExpireHolds(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	link := seedBookingLink(t, st, "u1", "intro-call")

	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Minute)
	future := now.Add(time.Hour)

	expiredBooking, err := st.Bookings().CreateHold(ctx, domain.Booking{
		LinkID: link.ID, Start: now, End: now.Add(30 * time.Minute),
		InviteeName: "Alice", InviteeEmail: "alice@example.com", InviteeTZ: "UTC",
		HoldExpiresAt: &expired,
	})
	if err != nil {
		t.Fatalf("CreateHold(expired): %v", err)
	}
	freshBooking, err := st.Bookings().CreateHold(ctx, domain.Booking{
		LinkID: link.ID, Start: now.Add(time.Hour), End: now.Add(90 * time.Minute),
		InviteeName: "Bob", InviteeEmail: "bob@example.com", InviteeTZ: "UTC",
		HoldExpiresAt: &future,
	})
	if err != nil {
		t.Fatalf("CreateHold(fresh): %v", err)
	}

	count, err := st.Bookings().ExpireHolds(ctx, now)
	if err != nil {
		t.Fatalf("ExpireHolds: %v", err)
	}
	if count != 1 {
		t.Fatalf("ExpireHolds count = %d, want 1", count)
	}

	got, err := st.Bookings().GetByID(ctx, expiredBooking.ID)
	if err != nil || got.Status != domain.BookingCancelled {
		t.Fatalf("expired booking status = %+v, %v; want cancelled", got, err)
	}
	stillFresh, err := st.Bookings().GetByID(ctx, freshBooking.ID)
	if err != nil || stillFresh.Status != domain.BookingHold {
		t.Fatalf("fresh booking status = %+v, %v; want hold (unaffected)", stillFresh, err)
	}

	byUser, err := st.Bookings().ListByUser(ctx, "u1", 0)
	if err != nil || len(byUser) != 2 {
		t.Fatalf("ListByUser = %v, %v; want 2 bookings scoped via link owner", byUser, err)
	}
}

func TestPollCRUDAndVoteUpsertReplacesBallot(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	cal := seedCalendar(t, st, acct.ID)

	base := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	p, err := st.Polls().Create(ctx, domain.MeetingPoll{
		UserID: "u1", Title: "Sync", CalendarID: cal.ID, DurationMinutes: 30,
		Options: []domain.PollOption{
			{ID: "opt1", Start: base, End: base.Add(30 * time.Minute)},
			{ID: "opt2", Start: base.Add(time.Hour), End: base.Add(90 * time.Minute)},
		},
	})
	if err != nil {
		t.Fatalf("Create poll: %v", err)
	}
	if p.ID == "" || p.Token == "" || p.Status != domain.PollOpen || p.CreatedAt.IsZero() {
		t.Fatalf("Create did not populate defaults: %+v", p)
	}

	byToken, err := st.Polls().GetByToken(ctx, p.Token)
	if err != nil || byToken.ID != p.ID || len(byToken.Options) != 2 {
		t.Fatalf("GetByToken = %+v, %v", byToken, err)
	}

	if _, err := st.Polls().GetByToken(ctx, "nonexistent"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByToken(missing) = %v, want ErrNotFound", err)
	}

	// Vote, then replace the same voter's ballot for the same option — the
	// second write must win, keyed case-insensitively on email.
	if err := st.Polls().UpsertVotes(ctx, []domain.PollVote{
		{PollID: p.ID, OptionID: "opt1", VoterEmail: "Alice@Example.com", VoterName: "Alice", Choice: domain.VoteYes},
	}); err != nil {
		t.Fatalf("UpsertVotes: %v", err)
	}
	if err := st.Polls().UpsertVotes(ctx, []domain.PollVote{
		{PollID: p.ID, OptionID: "opt1", VoterEmail: "alice@example.com", VoterName: "Alice", Choice: domain.VoteNo},
	}); err != nil {
		t.Fatalf("UpsertVotes(replace): %v", err)
	}

	votes, err := st.Polls().ListVotes(ctx, p.ID)
	if err != nil || len(votes) != 1 || votes[0].Choice != domain.VoteNo {
		t.Fatalf("ListVotes = %+v, %v; want single replaced ballot with choice=no", votes, err)
	}

	// Update the poll (confirm the winner). event_id is FK'd to events, so
	// the mirrored event must actually exist.
	evt, err := st.Events().Upsert(ctx, domain.Event{
		CalendarID: cal.ID, ProviderEventID: newID(), Title: "Sync", Start: base, End: base.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}
	p.Status = domain.PollConfirmed
	p.WinnerOptionID = ptr("opt1")
	p.EventID = ptr(evt.ID)
	if err := st.Polls().Update(ctx, p); err != nil {
		t.Fatalf("Update: %v", err)
	}
	updated, err := st.Polls().GetByID(ctx, p.ID)
	if err != nil || updated.Status != domain.PollConfirmed || updated.WinnerOptionID == nil || *updated.WinnerOptionID != "opt1" {
		t.Fatalf("GetByID after update = %+v, %v", updated, err)
	}

	polls, err := st.Polls().ListByUser(ctx, "u1")
	if err != nil || len(polls) != 1 {
		t.Fatalf("ListByUser = %v, %v; want 1 poll", polls, err)
	}

	if err := st.Polls().Delete(ctx, p.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := st.Polls().GetByID(ctx, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByID after delete = %v, want ErrNotFound", err)
	}
	// Votes cascade with the poll.
	if remaining, err := st.Polls().ListVotes(ctx, p.ID); err != nil || len(remaining) != 0 {
		t.Fatalf("votes not cascaded on delete: %v, %v", remaining, err)
	}
}

func TestTimeProposalCRUD(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	cal := seedCalendar(t, st, acct.ID)
	start := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	evt, err := st.Events().Upsert(ctx, domain.Event{
		CalendarID: cal.ID, ProviderEventID: newID(), Title: "Sync", Start: start, End: start.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}

	p, err := st.TimeProposals().Create(ctx, domain.TimeProposal{
		EventID: evt.ID, ProposerEmail: "bob@example.com", ProposerName: "Bob",
		Start: start.Add(24 * time.Hour), End: start.Add(25 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.ID == "" || p.Status != domain.ProposalPending || p.CreatedAt.IsZero() {
		t.Fatalf("Create did not populate defaults: %+v", p)
	}

	list, err := st.TimeProposals().ListByEvent(ctx, evt.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListByEvent = %v, %v; want 1", list, err)
	}

	p.Status = domain.ProposalAccepted
	if err := st.TimeProposals().Update(ctx, p); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := st.TimeProposals().GetByID(ctx, p.ID)
	if err != nil || got.Status != domain.ProposalAccepted {
		t.Fatalf("GetByID after update = %+v, %v", got, err)
	}

	if err := st.TimeProposals().Update(ctx, domain.TimeProposal{ID: "missing"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Update(missing) = %v, want ErrNotFound", err)
	}
}

func TestUserSettingsGetReturnsDefaultAndUpsert(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	got, err := st.UserSettings().Get(ctx, "u1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.UserID != "u1" || got.TimeZone != "UTC" || len(got.WorkingHours) != 0 {
		t.Fatalf("Get default = %+v, want zero-value with TimeZone UTC", got)
	}

	want := domain.UserSettings{
		UserID: "u1", TimeZone: "America/New_York", WorkingLocation: "home",
		WorkingHours: []domain.AvailabilityWindow{{Weekday: 1, Start: "09:00", End: "17:00"}},
	}
	if err := st.UserSettings().Upsert(ctx, want); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err = st.UserSettings().Get(ctx, "u1")
	if err != nil {
		t.Fatalf("Get after upsert: %v", err)
	}
	if got.TimeZone != "America/New_York" || got.WorkingLocation != "home" || len(got.WorkingHours) != 1 {
		t.Fatalf("Get after Upsert = %+v", got)
	}

	// Upsert again overwrites rather than duplicating the row.
	want2 := want
	want2.WorkingLocation = "office"
	if err := st.UserSettings().Upsert(ctx, want2); err != nil {
		t.Fatalf("Upsert overwrite: %v", err)
	}
	got2, err := st.UserSettings().Get(ctx, "u1")
	if err != nil || got2.WorkingLocation != "office" {
		t.Fatalf("Get after overwrite = %+v, %v", got2, err)
	}
}

func TestUpsertVotesAtomicity(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	cal := seedCalendar(t, st, acct.ID)

	base := time.Date(2026, 8, 7, 9, 0, 0, 0, time.UTC)
	p, err := st.Polls().Create(ctx, domain.MeetingPoll{
		UserID: "u1", Title: "Sync", CalendarID: cal.ID, DurationMinutes: 30,
		Options: []domain.PollOption{
			{ID: "opt1", Start: base, End: base.Add(30 * time.Minute)},
			{ID: "opt2", Start: base.Add(time.Hour), End: base.Add(90 * time.Minute)},
		},
	})
	if err != nil {
		t.Fatalf("Create poll: %v", err)
	}

	// Attempt to upsert votes where the second vote references a nonexistent poll.
	// This should fail mid-loop, and with transactionality, all votes should be rolled back.
	err = st.Polls().UpsertVotes(ctx, []domain.PollVote{
		{PollID: p.ID, OptionID: "opt1", VoterEmail: "alice@example.com", VoterName: "Alice", Choice: domain.VoteYes},
		{PollID: "nonexistent-poll", OptionID: "opt1", VoterEmail: "bob@example.com", VoterName: "Bob", Choice: domain.VoteYes},
	})
	if err == nil {
		t.Fatalf("UpsertVotes should have failed due to FK constraint")
	}

	// Verify that the first vote was NOT committed (proving atomicity).
	votes, err := st.Polls().ListVotes(ctx, p.ID)
	if err != nil {
		t.Fatalf("ListVotes: %v", err)
	}
	if len(votes) != 0 {
		t.Fatalf("ListVotes = %d votes, want 0 (all should be rolled back); got %+v", len(votes), votes)
	}
}

func ptr(s string) *string { return &s }

func TestUserSettingsAIBackgroundDefaultsAndSet(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	got, err := st.UserSettings().Get(ctx, "u1")
	if err != nil || !got.AIBackground {
		t.Fatalf("Get without a row: AIBackground=%v err=%v, want true", got.AIBackground, err)
	}
	if err := st.UserSettings().SetAIBackground(ctx, "u1", false); err != nil {
		t.Fatalf("SetAIBackground on a user without a row: %v", err)
	}
	got, err = st.UserSettings().Get(ctx, "u1")
	if err != nil || got.AIBackground || got.TimeZone != "UTC" {
		t.Fatalf("after SetAIBackground(false): %+v err=%v, want AIBackground=false TimeZone=UTC", got, err)
	}
	// A full-document Upsert from an older client must not flip the switch back.
	if err := st.UserSettings().Upsert(ctx, domain.UserSettings{UserID: "u1", TimeZone: "Europe/Lisbon", WorkingHours: []domain.AvailabilityWindow{}}); err != nil {
		t.Fatal(err)
	}
	got, err = st.UserSettings().Get(ctx, "u1")
	if err != nil || got.AIBackground || got.TimeZone != "Europe/Lisbon" {
		t.Fatalf("after Upsert: %+v err=%v, want AIBackground still false", got, err)
	}
	if err := st.UserSettings().SetAIBackground(ctx, "u1", true); err != nil {
		t.Fatal(err)
	}
	if got, _ = st.UserSettings().Get(ctx, "u1"); !got.AIBackground {
		t.Fatal("SetAIBackground(true) did not persist")
	}
}
