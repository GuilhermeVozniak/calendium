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

// --- SchedulingService: booking pipeline (Book, ExpireHolds, ListBookings, --
// --- CancelBooking) fixture ---------------------------------------------------

// bookingFixture is a SchedulingService wired to one Google account (a1,
// user u1, email organizer@x.com) owning one writable calendar (cal1), with
// a valid non-expiring access token (tokenSource never refreshes) and one
// active booking link (slug "intro-call", 30-minute slots, every day
// 09:00-17:00 UTC).
type bookingFixture struct {
	svc       *SchedulingService
	links     *fakeBookingLinkRepo
	bookings  *fakeBookingRepo
	accounts  *fakeAccountRepo
	calendars *fakeCalendarRepo
	events    *fakeEventRepo
	users     *fakeUserRepo
	settings  *fakeUserSettingsRepo
	calProv   *fakeCalendarProvider
	mailProv  *fakeMailProvider
	tx        *fakeTxRunner
	clock     *fakeClock
	link      domain.BookingLink
}

func newBookingFixture(t *testing.T) *bookingFixture {
	t.Helper()
	ctx := context.Background()
	base := time.Date(2026, 8, 3, 8, 0, 0, 0, time.UTC) // a Monday
	clock := newClock(base)

	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "organizer@x.com",
	}); err != nil {
		t.Fatalf("seed account: %v", err)
	}
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

	users := newUserRepo()
	if _, err := users.Upsert(ctx, domain.User{ID: "u1", Email: "organizer@x.com", Name: ptr("Organizer")}); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	links := newBookingLinkRepo()
	windows := make([]domain.AvailabilityWindow, 7)
	for wd := 0; wd < 7; wd++ {
		windows[wd] = domain.AvailabilityWindow{Weekday: wd, Start: "09:00", End: "17:00"}
	}
	link, err := links.Create(ctx, domain.BookingLink{
		ID: "link1", UserID: "u1", Slug: "intro-call", Title: "Intro Call",
		CalendarID: "cal1", DurationMinutes: 30, TimeZone: "UTC",
		Windows: windows, Active: true,
	})
	if err != nil {
		t.Fatalf("seed link: %v", err)
	}

	bookings := newBookingRepo(links)
	events := newEventRepo()
	settingsRepo := newUserSettingsRepo()
	calProv := newCalendarProvider()
	calProv.createdEvent = domain.Event{ID: "provider-assigned", ProviderEventID: "prov-evt-1"}
	mailProv := newMailProvider()
	tx := newTxRunner()

	svc := NewSchedulingService(SchedulingServiceDeps{
		Users:             users,
		Accounts:          accounts,
		Calendars:         calendars,
		Events:            events,
		Links:             links,
		Bookings:          bookings,
		Settings:          settingsRepo,
		Tx:                tx,
		CalendarProviders: map[domain.Provider]port.CalendarProvider{domain.ProviderGoogle: calProv},
		MailProviders:     map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mailProv},
		OAuth:             map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:             clock,
		SelfHosted:        true,
	})

	return &bookingFixture{
		svc: svc, links: links, bookings: bookings, accounts: accounts,
		calendars: calendars, events: events, users: users, settings: settingsRepo,
		calProv: calProv, mailProv: mailProv, tx: tx, clock: clock, link: link,
	}
}

// validBookingRequest returns a request for the first slot PublicSlots
// currently offers on the fixture's link.
func (f *bookingFixture) validBookingRequest(t *testing.T) port.BookingRequest {
	t.Helper()
	ctx := context.Background()
	slots, err := f.svc.PublicSlots(ctx, f.link.Slug, f.clock.now, f.clock.now.AddDate(0, 0, 7))
	if err != nil {
		t.Fatalf("PublicSlots: %v", err)
	}
	if len(slots) == 0 {
		t.Fatal("no offered slots to book against")
	}
	return port.BookingRequest{
		Start:        slots[0].Start,
		InviteeName:  "Ivy Invitee",
		InviteeEmail: "ivy@example.com",
		InviteeTZ:    "America/New_York",
	}
}

func TestBookHappyPath(t *testing.T) {
	ctx := context.Background()
	f := newBookingFixture(t)
	req := f.validBookingRequest(t)

	got, err := f.svc.Book(ctx, "intro-call", req)
	if err != nil {
		t.Fatalf("Book: %v", err)
	}
	if got.Status != domain.BookingConfirmed {
		t.Fatalf("Status = %q, want confirmed", got.Status)
	}
	if got.EventID == nil || *got.EventID == "" {
		t.Fatalf("EventID not set on confirmed booking: %+v", got)
	}
	if got.HoldExpiresAt != nil {
		t.Fatalf("HoldExpiresAt = %v, want nil once confirmed", got.HoldExpiresAt)
	}

	// The provider event was created with both attendees.
	if len(f.calProv.lastCreateInput.AttendeeEmails) != 2 {
		t.Fatalf("provider CreateEvent attendees = %v, want owner+invitee", f.calProv.lastCreateInput.AttendeeEmails)
	}
	wantAttendees := map[string]bool{"organizer@x.com": true, "ivy@example.com": true}
	for _, e := range f.calProv.lastCreateInput.AttendeeEmails {
		if !wantAttendees[e] {
			t.Fatalf("unexpected attendee %q", e)
		}
	}

	// The local event mirror was upserted inside the tx.
	if _, err := f.events.GetByID(ctx, *got.EventID); err != nil {
		t.Fatalf("event not persisted: %v", err)
	}
	if f.tx.calls != 1 {
		t.Fatalf("tx.calls = %d, want 1", f.tx.calls)
	}

	// The confirmation email went to the invitee, cc'd the owner, and
	// renders the slot in the invitee's time zone.
	if len(f.mailProv.sent) != 1 {
		t.Fatalf("sent = %d messages, want 1", len(f.mailProv.sent))
	}
	msg := f.mailProv.sent[0]
	if len(msg.To) != 1 || msg.To[0].Email != "ivy@example.com" {
		t.Fatalf("To = %+v, want ivy@example.com", msg.To)
	}
	if len(msg.Cc) != 1 || msg.Cc[0].Email != "organizer@x.com" {
		t.Fatalf("Cc = %+v, want organizer@x.com", msg.Cc)
	}
	inviteeLoc, _ := time.LoadLocation("America/New_York")
	wantSubstr := req.Start.In(inviteeLoc).Format("Mon, Jan 2, 2006 3:04 PM MST")
	if !strings.Contains(msg.BodyText, wantSubstr) {
		t.Fatalf("body = %q, want it to contain invitee-TZ rendering %q", msg.BodyText, wantSubstr)
	}
}

func TestBookSlotTakenByExclusion(t *testing.T) {
	ctx := context.Background()
	f := newBookingFixture(t)
	req := f.validBookingRequest(t)

	// Force CreateHold to report the DB exclusion constraint firing (a
	// concurrent competitor won the race).
	f.bookings.forceCreateHoldErr = domain.ErrConflict

	_, err := f.svc.Book(ctx, "intro-call", req)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if f.calProv.lastCreateInput.CalendarID != "" || len(f.calProv.lastFreeBusyEmails) != 0 {
		t.Fatalf("provider was called after a hold rejection: create=%+v freeBusy=%v",
			f.calProv.lastCreateInput, f.calProv.lastFreeBusyEmails)
	}
}

func TestBookProviderBusyAtRecheck(t *testing.T) {
	ctx := context.Background()
	f := newBookingFixture(t)
	req := f.validBookingRequest(t)
	end := req.Start.Add(30 * time.Minute)

	f.calProv.freeBusyResult = map[string][]domain.BusyInterval{
		"organizer@x.com": {{Start: req.Start, End: end}},
	}

	_, err := f.svc.Book(ctx, "intro-call", req)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}

	holds, _ := f.bookings.ListActiveInRange(ctx, f.link.ID, req.Start, end)
	if len(holds) != 0 {
		t.Fatalf("active bookings after provider-busy rejection = %v, want none (hold cancelled)", holds)
	}
	if len(f.mailProv.sent) != 0 {
		t.Fatalf("email sent despite rejected booking: %v", f.mailProv.sent)
	}
}

func TestBookEventCreateFails(t *testing.T) {
	ctx := context.Background()
	f := newBookingFixture(t)
	req := f.validBookingRequest(t)
	end := req.Start.Add(30 * time.Minute)

	f.calProv.createErr = errors.New("provider unavailable")

	_, err := f.svc.Book(ctx, "intro-call", req)
	if err == nil {
		t.Fatal("Book: want error when provider CreateEvent fails")
	}

	holds, _ := f.bookings.ListActiveInRange(ctx, f.link.ID, req.Start, end)
	if len(holds) != 0 {
		t.Fatalf("active bookings after CreateEvent failure = %v, want none (hold cancelled)", holds)
	}
}

func TestBookEmailFailureStillConfirms(t *testing.T) {
	ctx := context.Background()
	f := newBookingFixture(t)
	req := f.validBookingRequest(t)

	f.mailProv.sendErr = errors.New("smtp unavailable")

	got, err := f.svc.Book(ctx, "intro-call", req)
	if err != nil {
		t.Fatalf("Book: want nil error despite email failure, got %v", err)
	}
	if got.Status != domain.BookingConfirmed {
		t.Fatalf("Status = %q, want confirmed even though the email failed", got.Status)
	}
	if got.EventID == nil {
		t.Fatal("EventID not set despite email failure")
	}
}

func TestBookRejectsUnofferedStart(t *testing.T) {
	ctx := context.Background()
	f := newBookingFixture(t)

	req := port.BookingRequest{
		// A start outside every window (03:00 UTC) is never offered.
		Start:        time.Date(2026, 8, 3, 3, 0, 0, 0, time.UTC),
		InviteeName:  "Ivy Invitee",
		InviteeEmail: "ivy@example.com",
		InviteeTZ:    "America/New_York",
	}
	_, err := f.svc.Book(ctx, "intro-call", req)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict for an unoffered start", err)
	}
	if f.calProv.lastCreateInput.CalendarID != "" {
		t.Fatalf("provider CreateEvent called for a rejected request: %+v", f.calProv.lastCreateInput)
	}
}

func TestBookUnknownOrInactiveLink(t *testing.T) {
	ctx := context.Background()

	t.Run("unknown slug", func(t *testing.T) {
		f := newBookingFixture(t)
		req := f.validBookingRequest(t)
		_, err := f.svc.Book(ctx, "nonexistent", req)
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("inactive link", func(t *testing.T) {
		f := newBookingFixture(t)
		l := f.link
		l.Active = false
		f.links.byID[l.ID] = l
		req := port.BookingRequest{
			Start: f.clock.now.AddDate(0, 0, 1), InviteeName: "Ivy", InviteeEmail: "ivy@example.com", InviteeTZ: "UTC",
		}
		_, err := f.svc.Book(ctx, "intro-call", req)
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("invalid invitee email", func(t *testing.T) {
		f := newBookingFixture(t)
		req := f.validBookingRequest(t)
		req.InviteeEmail = "not-an-email"
		_, err := f.svc.Book(ctx, "intro-call", req)
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})
}

func TestExpireHoldsSweep(t *testing.T) {
	ctx := context.Background()
	f := newBookingFixture(t)

	expired := f.clock.now.Add(-time.Minute)
	future := f.clock.now.Add(time.Hour)
	if _, err := f.bookings.CreateHold(ctx, domain.Booking{
		ID: "expired", LinkID: f.link.ID, Status: domain.BookingHold,
		Start: f.clock.now, End: f.clock.now.Add(30 * time.Minute), HoldExpiresAt: &expired,
	}); err != nil {
		t.Fatalf("seed expired hold: %v", err)
	}
	if _, err := f.bookings.CreateHold(ctx, domain.Booking{
		ID: "fresh", LinkID: f.link.ID, Status: domain.BookingHold,
		Start: f.clock.now.Add(time.Hour), End: f.clock.now.Add(90 * time.Minute), HoldExpiresAt: &future,
	}); err != nil {
		t.Fatalf("seed fresh hold: %v", err)
	}

	if err := f.svc.ExpireHolds(ctx); err != nil {
		t.Fatalf("ExpireHolds: %v", err)
	}

	expiredBooking, err := f.bookings.GetByID(ctx, "expired")
	if err != nil || expiredBooking.Status != domain.BookingCancelled {
		t.Fatalf("expired hold = %+v, %v; want cancelled", expiredBooking, err)
	}
	freshBooking, err := f.bookings.GetByID(ctx, "fresh")
	if err != nil || freshBooking.Status != domain.BookingHold {
		t.Fatalf("fresh hold = %+v, %v; want still hold (unaffected)", freshBooking, err)
	}
}

func TestListBookings(t *testing.T) {
	ctx := context.Background()
	f := newBookingFixture(t)
	req := f.validBookingRequest(t)
	if _, err := f.svc.Book(ctx, "intro-call", req); err != nil {
		t.Fatalf("Book: %v", err)
	}

	list, err := f.svc.ListBookings(ctx, "u1")
	if err != nil || len(list) != 1 {
		t.Fatalf("ListBookings = %v, %v; want 1 booking", list, err)
	}

	if _, err := f.svc.ListBookings(ctx, "someone-else"); err != nil {
		t.Fatalf("ListBookings(someone-else): %v", err)
	}
}

func TestCancelBooking(t *testing.T) {
	ctx := context.Background()

	t.Run("owner cancels a confirmed booking", func(t *testing.T) {
		f := newBookingFixture(t)
		req := f.validBookingRequest(t)
		booking, err := f.svc.Book(ctx, "intro-call", req)
		if err != nil {
			t.Fatalf("Book: %v", err)
		}
		f.mailProv.sent = nil // ignore the confirmation email sent above

		if err := f.svc.CancelBooking(ctx, "u1", booking.ID); err != nil {
			t.Fatalf("CancelBooking: %v", err)
		}

		got, err := f.bookings.GetByID(ctx, booking.ID)
		if err != nil || got.Status != domain.BookingCancelled {
			t.Fatalf("booking after cancel = %+v, %v; want cancelled", got, err)
		}
		if f.calProv.lastDeleteEventID == "" {
			t.Fatal("provider DeleteEvent was not called")
		}
		if len(f.mailProv.sent) != 1 || f.mailProv.sent[0].To[0].Email != "ivy@example.com" {
			t.Fatalf("cancellation notice = %+v, want one message to the invitee", f.mailProv.sent)
		}

		// Idempotent: cancelling again is a no-op, not an error.
		if err := f.svc.CancelBooking(ctx, "u1", booking.ID); err != nil {
			t.Fatalf("second CancelBooking: %v", err)
		}
	})

	t.Run("foreign booking is 404", func(t *testing.T) {
		f := newBookingFixture(t)
		req := f.validBookingRequest(t)
		booking, err := f.svc.Book(ctx, "intro-call", req)
		if err != nil {
			t.Fatalf("Book: %v", err)
		}
		if err := f.svc.CancelBooking(ctx, "intruder", booking.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("unknown booking id", func(t *testing.T) {
		f := newBookingFixture(t)
		if err := f.svc.CancelBooking(ctx, "u1", "nope"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

// --- buildConfirmationEmail / buildCancellationEmail (pure) ------------------

func TestBuildConfirmationEmail(t *testing.T) {
	link := domain.BookingLink{Title: "Intro Call", TimeZone: "America/Los_Angeles"}
	booking := domain.Booking{
		InviteeName: "Ivy Invitee", InviteeEmail: "ivy@example.com", InviteeTZ: "America/New_York",
		Start: time.Date(2026, 8, 3, 17, 0, 0, 0, time.UTC), // 13:00 ET / 10:00 PT
		End:   time.Date(2026, 8, 3, 17, 30, 0, 0, time.UTC),
	}
	ev := domain.Event{Conferencing: &domain.Conferencing{URL: "https://meet.example/abc"}}

	subject, html, text := buildConfirmationEmail(link, booking, ev, "Organizer Name")

	if !strings.Contains(subject, "Intro Call") || !strings.Contains(subject, "Organizer Name") {
		t.Fatalf("subject = %q, want it to mention the link title and organizer", subject)
	}
	inviteeLoc, _ := time.LoadLocation("America/New_York")
	wantInvitee := booking.Start.In(inviteeLoc).Format("Mon, Jan 2, 2006 3:04 PM MST")
	if !strings.Contains(text, wantInvitee) {
		t.Fatalf("text = %q, want invitee-TZ rendering %q", text, wantInvitee)
	}
	ownerLoc, _ := time.LoadLocation("America/Los_Angeles")
	wantOwner := booking.Start.In(ownerLoc).Format("Mon, Jan 2, 2006 3:04 PM MST")
	if !strings.Contains(text, wantOwner) {
		t.Fatalf("text = %q, want owner-TZ rendering %q", text, wantOwner)
	}
	if !strings.Contains(text, "https://meet.example/abc") {
		t.Fatalf("text = %q, want the conferencing URL", text)
	}
	if !strings.Contains(html, "<br>") {
		t.Fatalf("html = %q, want newlines rendered as <br>", html)
	}
}

func TestBuildCancellationEmail(t *testing.T) {
	link := domain.BookingLink{Title: "Intro Call"}
	booking := domain.Booking{
		InviteeName: "Ivy Invitee", InviteeTZ: "UTC",
		Start: time.Date(2026, 8, 3, 17, 0, 0, 0, time.UTC),
	}

	subject, body := buildCancellationEmail(link, booking, "Organizer Name")
	if !strings.Contains(subject, "Intro Call") {
		t.Fatalf("subject = %q, want it to mention the link title", subject)
	}
	if !strings.Contains(body, "Ivy Invitee") || !strings.Contains(body, "cancelled") {
		t.Fatalf("body = %q, want a cancellation notice addressed to the invitee", body)
	}
}
