package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- computeSlots (pure engine) -----------------------------------------------

func TestComputeSlotsWindowDiscretizationAcrossDays(t *testing.T) {
	// Anchor on a Tuesday: the window recurs on both Tuesdays inside [from,to).
	day0 := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	link := domain.BookingLink{
		TimeZone:        "UTC",
		DurationMinutes: 30,
		Windows: []domain.AvailabilityWindow{
			{Weekday: int(day0.Weekday()), Start: "09:00", End: "11:00"},
		},
	}
	from := day0
	to := day0.AddDate(0, 0, 8) // spans day0's Tuesday and the following Tuesday
	now := day0.Add(-48 * time.Hour)

	got := computeSlots(link, domain.UserSettings{}, nil, nil, from, to, now)

	day1 := day0.AddDate(0, 0, 7)
	want := []domain.AvailabilitySlot{
		{Start: day0.Add(9 * time.Hour), End: day0.Add(9*time.Hour + 30*time.Minute)},
		{Start: day0.Add(9*time.Hour + 30*time.Minute), End: day0.Add(10 * time.Hour)},
		{Start: day0.Add(10 * time.Hour), End: day0.Add(10*time.Hour + 30*time.Minute)},
		{Start: day0.Add(10*time.Hour + 30*time.Minute), End: day0.Add(11 * time.Hour)},
		{Start: day1.Add(9 * time.Hour), End: day1.Add(9*time.Hour + 30*time.Minute)},
		{Start: day1.Add(9*time.Hour + 30*time.Minute), End: day1.Add(10 * time.Hour)},
		{Start: day1.Add(10 * time.Hour), End: day1.Add(10*time.Hour + 30*time.Minute)},
		{Start: day1.Add(10*time.Hour + 30*time.Minute), End: day1.Add(11 * time.Hour)},
	}
	assertSlotsEqual(t, got, want)
}

// TestComputeSlotsDSTSpringForward proves the discretization loop is
// DST-correct: a 09:00-17:00 window on America/New_York's 2026 spring-forward
// day (March 8) resolves to the true UTC instants for that day's actual
// offset (EDT, UTC-4, since the 2am->3am jump happens before 09:00), not the
// pre-transition EST (UTC-5) offset.
func TestComputeSlotsDSTSpringForward(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	transitionDay := time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC)
	link := domain.BookingLink{
		TimeZone:        "America/New_York",
		DurationMinutes: 60,
		Windows: []domain.AvailabilityWindow{
			{Weekday: int(transitionDay.Weekday()), Start: "09:00", End: "17:00"},
		},
	}
	from := time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	got := computeSlots(link, domain.UserSettings{}, nil, nil, from, to, now)
	if len(got) != 8 {
		t.Fatalf("len(got) = %d, want 8 (09:00-17:00 in 1h steps)", len(got))
	}

	wantFirstStart := time.Date(2026, 3, 8, 9, 0, 0, 0, loc)
	if !got[0].Start.Equal(wantFirstStart) {
		t.Fatalf("first slot start = %v, want %v (local 09:00 America/New_York)", got[0].Start, wantFirstStart)
	}
	// EDT is UTC-4; by 09:00 local the 2am->3am spring-forward has already
	// happened, so 09:00 local must be 13:00 UTC, NOT 14:00 UTC (which would
	// be the stale pre-DST EST/UTC-5 offset).
	wantFirstUTC := time.Date(2026, 3, 8, 13, 0, 0, 0, time.UTC)
	if !got[0].Start.Equal(wantFirstUTC) {
		t.Fatalf("first slot start (UTC) = %v, want %v", got[0].Start.UTC(), wantFirstUTC)
	}
}

func TestComputeSlotsBuffersPadBusyEvent(t *testing.T) {
	day0 := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	link := domain.BookingLink{
		TimeZone:        "UTC",
		DurationMinutes: 30,
		BufferBeforeMin: 15,
		BufferAfterMin:  15,
		Windows: []domain.AvailabilityWindow{
			{Weekday: int(day0.Weekday()), Start: "09:00", End: "12:00"},
		},
	}
	busy := []domain.BusyInterval{
		{Start: day0.Add(10 * time.Hour), End: day0.Add(10*time.Hour + 30*time.Minute)},
	}
	from, to := day0, day0.AddDate(0, 0, 1)
	now := day0.Add(-48 * time.Hour)

	got := computeSlots(link, domain.UserSettings{}, busy, nil, from, to, now)

	want := []domain.AvailabilitySlot{
		{Start: day0.Add(9 * time.Hour), End: day0.Add(9*time.Hour + 30*time.Minute)},
		{Start: day0.Add(11 * time.Hour), End: day0.Add(11*time.Hour + 30*time.Minute)},
		{Start: day0.Add(11*time.Hour + 30*time.Minute), End: day0.Add(12 * time.Hour)},
	}
	assertSlotsEqual(t, got, want)
}

func TestComputeSlotsDailyLimitHidesWholeDay(t *testing.T) {
	day0 := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	day1 := day0.AddDate(0, 0, 7)
	link := domain.BookingLink{
		TimeZone:        "UTC",
		DurationMinutes: 30,
		DailyLimit:      1,
		Windows: []domain.AvailabilityWindow{
			{Weekday: int(day0.Weekday()), Start: "09:00", End: "11:00"},
		},
	}
	// One confirmed booking on day0 only; day0 should vanish entirely (not
	// just the slot it overlaps), day1 stays untouched.
	active := []domain.Booking{
		{Status: domain.BookingConfirmed, Start: day0.Add(9 * time.Hour), End: day0.Add(9*time.Hour + 30*time.Minute)},
	}
	from, to := day0, day0.AddDate(0, 0, 8)
	now := day0.Add(-48 * time.Hour)

	got := computeSlots(link, domain.UserSettings{}, nil, active, from, to, now)

	for _, s := range got {
		if !s.Start.Before(day1) {
			continue
		}
		t.Fatalf("day0 slot %v survived dailyLimit, want the whole day hidden", s)
	}
	if len(got) != 4 {
		t.Fatalf("len(got) = %d, want 4 (day1's untouched slots only)", len(got))
	}
}

func TestComputeSlotsMinNoticeHidesNearSlots(t *testing.T) {
	day0 := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	link := domain.BookingLink{
		TimeZone:        "UTC",
		DurationMinutes: 60,
		MinNoticeMin:    120, // 2h
		Windows: []domain.AvailabilityWindow{
			{Weekday: int(day0.Weekday()), Start: "09:00", End: "17:00"},
		},
	}
	from, to := day0, day0.AddDate(0, 0, 1)
	now := day0.Add(9*time.Hour + 30*time.Minute) // 09:30 -> notice limit 11:30

	got := computeSlots(link, domain.UserSettings{}, nil, nil, from, to, now)

	want := []domain.AvailabilitySlot{
		{Start: day0.Add(12 * time.Hour), End: day0.Add(13 * time.Hour)},
		{Start: day0.Add(13 * time.Hour), End: day0.Add(14 * time.Hour)},
		{Start: day0.Add(14 * time.Hour), End: day0.Add(15 * time.Hour)},
		{Start: day0.Add(15 * time.Hour), End: day0.Add(16 * time.Hour)},
		{Start: day0.Add(16 * time.Hour), End: day0.Add(17 * time.Hour)},
	}
	assertSlotsEqual(t, got, want)
}

func TestComputeSlotsActiveHoldBlocksItsSlot(t *testing.T) {
	day0 := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	link := domain.BookingLink{
		TimeZone:        "UTC",
		DurationMinutes: 60,
		Windows: []domain.AvailabilityWindow{
			{Weekday: int(day0.Weekday()), Start: "09:00", End: "12:00"},
		},
	}
	active := []domain.Booking{
		{Status: domain.BookingHold, Start: day0.Add(10 * time.Hour), End: day0.Add(11 * time.Hour)},
	}
	from, to := day0, day0.AddDate(0, 0, 1)
	now := day0.Add(-48 * time.Hour)

	got := computeSlots(link, domain.UserSettings{}, nil, active, from, to, now)

	want := []domain.AvailabilitySlot{
		{Start: day0.Add(9 * time.Hour), End: day0.Add(10 * time.Hour)},
		{Start: day0.Add(11 * time.Hour), End: day0.Add(12 * time.Hour)},
	}
	assertSlotsEqual(t, got, want)
}

// TestComputeSlotsWorkingHoursDifferentTimeZone proves working-hours
// intersection is evaluated in settings.TimeZone even when it differs from
// the link's own TimeZone: a link window in America/New_York (EST, UTC-5 in
// January) is intersected against working hours declared in Asia/Tokyo
// (JST, UTC+9). Only the NY slot fully contained in the JST working window
// survives; the following hour, partially outside it, is dropped.
func TestComputeSlotsWorkingHoursDifferentTimeZone(t *testing.T) {
	nyLoc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation(NY): %v", err)
	}
	jstLoc, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatalf("LoadLocation(JST): %v", err)
	}

	nyWindowDay := time.Date(2026, 1, 6, 0, 0, 0, 0, nyLoc)
	link := domain.BookingLink{
		TimeZone:            "America/New_York",
		DurationMinutes:     60,
		RespectWorkingHours: true,
		Windows: []domain.AvailabilityWindow{
			{Weekday: int(nyWindowDay.Weekday()), Start: "06:00", End: "08:00"},
		},
	}

	// NY 06:00 Jan 6 == JST 20:00 Jan 6 (UTC-5 -> UTC+9 is +14h, same JST
	// calendar date); NY 07:00-08:00 == JST 21:00-22:00 Jan 6.
	firstSlotStart := time.Date(2026, 1, 6, 6, 0, 0, 0, nyLoc)
	jstDate := firstSlotStart.In(jstLoc)
	settings := domain.UserSettings{
		TimeZone: "Asia/Tokyo",
		WorkingHours: []domain.AvailabilityWindow{
			{Weekday: int(jstDate.Weekday()), Start: "19:00", End: "21:30"},
		},
	}

	from := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 8, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	got := computeSlots(link, settings, nil, nil, from, to, now)

	want := []domain.AvailabilitySlot{
		{Start: firstSlotStart, End: firstSlotStart.Add(time.Hour)},
	}
	assertSlotsEqual(t, got, want)
}

func assertSlotsEqual(t *testing.T, got, want []domain.AvailabilitySlot) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d\ngot:  %+v\nwant: %+v", len(got), len(want), got, want)
	}
	for i := range want {
		if !got[i].Start.Equal(want[i].Start) || !got[i].End.Equal(want[i].End) {
			t.Fatalf("slot %d = %+v, want %+v\nfull got:  %+v\nfull want: %+v", i, got[i], want[i], got, want)
		}
	}
}

// --- SchedulingService: booking-link CRUD, PublicPage, PublicSlots -----------

type schedFixture struct {
	svc       *SchedulingService
	links     *fakeBookingLinkRepo
	bookings  *fakeBookingRepo
	users     *fakeUserRepo
	accounts  *fakeAccountRepo
	calendars *fakeCalendarRepo
	events    *fakeEventRepo
	settings  *fakeUserSettingsRepo
	clock     *fakeClock
}

func newSchedFixture(t *testing.T) *schedFixture {
	t.Helper()
	links := newBookingLinkRepo()
	bookings := newBookingRepo(links)
	users := newUserRepo()
	accounts := newAccountRepo()
	calendars := newCalendarRepo()
	events := newEventRepo()
	settingsRepo := newUserSettingsRepo()
	clock := newClock(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))

	svc := NewSchedulingService(SchedulingServiceDeps{
		Users:      users,
		Accounts:   accounts,
		Calendars:  calendars,
		Events:     events,
		Links:      links,
		Bookings:   bookings,
		Settings:   settingsRepo,
		Clock:      clock,
		SelfHosted: true,
	})
	return &schedFixture{
		svc: svc, links: links, bookings: bookings, users: users,
		accounts: accounts, calendars: calendars, events: events,
		settings: settingsRepo, clock: clock,
	}
}

// seedOwnedCalendar creates an account + writable calendar owned by userID
// and returns the calendar id.
func (f *schedFixture) seedOwnedCalendar(t *testing.T, userID string, canWrite bool) string {
	t.Helper()
	ctx := context.Background()
	accountID := userID + "-acct"
	if _, err := f.accounts.Create(ctx, domain.ConnectedAccount{
		ID: accountID, UserID: userID, Provider: domain.ProviderGoogle, Email: userID + "@example.com",
	}); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	cal, err := f.calendars.Upsert(ctx, domain.Calendar{
		AccountID: accountID, ProviderCalendarID: "prov-" + userID, CanWrite: canWrite,
	})
	if err != nil {
		t.Fatalf("seed calendar: %v", err)
	}
	return cal.ID
}

func validLinkInput(calendarID string) port.BookingLinkInput {
	return port.BookingLinkInput{
		Slug:            "intro-call",
		Title:           "Intro Call",
		CalendarID:      calendarID,
		DurationMinutes: 30,
		TimeZone:        "UTC",
		Windows: []domain.AvailabilityWindow{
			{Weekday: 1, Start: "09:00", End: "17:00"},
		},
		Active: true,
	}
}

func TestSchedulingLinksCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("valid input creates the link", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", true)

		got, err := f.svc.CreateLink(ctx, "u1", validLinkInput(calID))
		if err != nil {
			t.Fatalf("CreateLink: %v", err)
		}
		if got.ID == "" || got.UserID != "u1" || got.Slug != "intro-call" {
			t.Fatalf("CreateLink result = %+v", got)
		}
	})

	t.Run("invalid slug rejected", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", true)
		in := validLinkInput(calID)
		in.Slug = "Not_Valid!"

		_, err := f.svc.CreateLink(ctx, "u1", in)
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("reserved slug rejected", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", true)
		in := validLinkInput(calID)
		in.Slug = "admin"

		_, err := f.svc.CreateLink(ctx, "u1", in)
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("calendar owned by someone else 404s", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "owner", true)

		_, err := f.svc.CreateLink(ctx, "intruder", validLinkInput(calID))
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("read-only calendar rejected", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", false)

		_, err := f.svc.CreateLink(ctx, "u1", validLinkInput(calID))
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("slug collision propagates ErrConflict", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", true)
		if _, err := f.svc.CreateLink(ctx, "u1", validLinkInput(calID)); err != nil {
			t.Fatalf("seed first link: %v", err)
		}

		_, err := f.svc.CreateLink(ctx, "u1", validLinkInput(calID))
		if !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
	})
}

func TestSchedulingLinksUpdate(t *testing.T) {
	ctx := context.Background()

	t.Run("owner can update", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", true)
		created, err := f.svc.CreateLink(ctx, "u1", validLinkInput(calID))
		if err != nil {
			t.Fatalf("seed link: %v", err)
		}

		in := validLinkInput(calID)
		in.Title = "Renamed"
		got, err := f.svc.UpdateLink(ctx, "u1", created.ID, in)
		if err != nil {
			t.Fatalf("UpdateLink: %v", err)
		}
		if got.Title != "Renamed" || got.ID != created.ID || !got.CreatedAt.Equal(created.CreatedAt) {
			t.Fatalf("UpdateLink result = %+v", got)
		}
	})

	t.Run("foreign link 404s", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "owner", true)
		created, err := f.svc.CreateLink(ctx, "owner", validLinkInput(calID))
		if err != nil {
			t.Fatalf("seed link: %v", err)
		}

		_, err = f.svc.UpdateLink(ctx, "intruder", created.ID, validLinkInput(calID))
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("invalid slug rejected", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", true)
		created, err := f.svc.CreateLink(ctx, "u1", validLinkInput(calID))
		if err != nil {
			t.Fatalf("seed link: %v", err)
		}

		in := validLinkInput(calID)
		in.Slug = "!!bad!!"
		_, err = f.svc.UpdateLink(ctx, "u1", created.ID, in)
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("calendar must still be owned and writable", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", true)
		created, err := f.svc.CreateLink(ctx, "u1", validLinkInput(calID))
		if err != nil {
			t.Fatalf("seed link: %v", err)
		}
		foreignCalID := f.seedOwnedCalendar(t, "someone-else", true)

		in := validLinkInput(foreignCalID)
		_, err = f.svc.UpdateLink(ctx, "u1", created.ID, in)
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("slug collision with another link propagates ErrConflict", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", true)
		first := validLinkInput(calID)
		first.Slug = "first-slug"
		if _, err := f.svc.CreateLink(ctx, "u1", first); err != nil {
			t.Fatalf("seed first: %v", err)
		}
		second := validLinkInput(calID)
		second.Slug = "second-slug"
		createdSecond, err := f.svc.CreateLink(ctx, "u1", second)
		if err != nil {
			t.Fatalf("seed second: %v", err)
		}

		in := validLinkInput(calID)
		in.Slug = "first-slug" // collides with the first link
		_, err = f.svc.UpdateLink(ctx, "u1", createdSecond.ID, in)
		if !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
	})
}

func TestSchedulingLinksListAndDelete(t *testing.T) {
	ctx := context.Background()
	f := newSchedFixture(t)
	calID := f.seedOwnedCalendar(t, "u1", true)
	created, err := f.svc.CreateLink(ctx, "u1", validLinkInput(calID))
	if err != nil {
		t.Fatalf("seed link: %v", err)
	}

	links, err := f.svc.ListLinks(ctx, "u1")
	if err != nil || len(links) != 1 {
		t.Fatalf("ListLinks = %v, %v; want 1 link", links, err)
	}

	if err := f.svc.DeleteLink(ctx, "intruder", created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("DeleteLink(foreign) = %v, want ErrNotFound", err)
	}
	if err := f.svc.DeleteLink(ctx, "u1", created.ID); err != nil {
		t.Fatalf("DeleteLink: %v", err)
	}
	links, err = f.svc.ListLinks(ctx, "u1")
	if err != nil || len(links) != 0 {
		t.Fatalf("ListLinks after delete = %v, %v; want empty", links, err)
	}
}

func TestSchedulingLinksPublicPage(t *testing.T) {
	ctx := context.Background()

	t.Run("active link returns page with owner display name", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", true)
		if _, err := f.svc.CreateLink(ctx, "u1", validLinkInput(calID)); err != nil {
			t.Fatalf("seed link: %v", err)
		}
		name := "Ada Lovelace"
		if _, err := f.users.Upsert(ctx, domain.User{ID: "u1", Email: "ada@example.com", Name: &name}); err != nil {
			t.Fatalf("seed user: %v", err)
		}

		page, err := f.svc.PublicPage(ctx, "intro-call")
		if err != nil {
			t.Fatalf("PublicPage: %v", err)
		}
		if page.OwnerName != "Ada Lovelace" || page.Slug != "intro-call" || page.DurationMinutes != 30 {
			t.Fatalf("PublicPage = %+v", page)
		}
	})

	t.Run("owner without a display name falls back to email", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", true)
		if _, err := f.svc.CreateLink(ctx, "u1", validLinkInput(calID)); err != nil {
			t.Fatalf("seed link: %v", err)
		}
		if _, err := f.users.Upsert(ctx, domain.User{ID: "u1", Email: "ada@example.com"}); err != nil {
			t.Fatalf("seed user: %v", err)
		}

		page, err := f.svc.PublicPage(ctx, "intro-call")
		if err != nil {
			t.Fatalf("PublicPage: %v", err)
		}
		if page.OwnerName != "ada@example.com" {
			t.Fatalf("OwnerName = %q, want fallback email", page.OwnerName)
		}
	})

	t.Run("inactive link 404s", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", true)
		in := validLinkInput(calID)
		in.Active = false
		if _, err := f.svc.CreateLink(ctx, "u1", in); err != nil {
			t.Fatalf("seed link: %v", err)
		}

		_, err := f.svc.PublicPage(ctx, "intro-call")
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("unknown slug 404s", func(t *testing.T) {
		f := newSchedFixture(t)
		if _, err := f.svc.PublicPage(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

func TestSchedulingLinksPublicSlots(t *testing.T) {
	ctx := context.Background()

	newActiveLinkFixture := func(t *testing.T, maxAdvanceDays int) (*schedFixture, domain.BookingLink) {
		t.Helper()
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", true)
		in := validLinkInput(calID)
		in.MaxAdvanceDays = maxAdvanceDays
		// Every weekday open 09:00-17:00 so the query window always has slots
		// to clamp/reject against, regardless of which day `now` falls on.
		in.Windows = make([]domain.AvailabilityWindow, 7)
		for wd := 0; wd < 7; wd++ {
			in.Windows[wd] = domain.AvailabilityWindow{Weekday: wd, Start: "09:00", End: "17:00"}
		}
		created, err := f.svc.CreateLink(ctx, "u1", in)
		if err != nil {
			t.Fatalf("seed link: %v", err)
		}
		return f, created
	}

	t.Run("to must be after from", func(t *testing.T) {
		f, _ := newActiveLinkFixture(t, 0)
		now := f.clock.now
		_, err := f.svc.PublicSlots(ctx, "intro-call", now, now)
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("query window over 31 days rejected", func(t *testing.T) {
		f, _ := newActiveLinkFixture(t, 0)
		now := f.clock.now
		_, err := f.svc.PublicSlots(ctx, "intro-call", now, now.AddDate(0, 0, 32))
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("range clamped to the link's maxAdvanceDays", func(t *testing.T) {
		f, _ := newActiveLinkFixture(t, 5)
		now := f.clock.now
		slots, err := f.svc.PublicSlots(ctx, "intro-call", now, now.AddDate(0, 0, 10))
		if err != nil {
			t.Fatalf("PublicSlots: %v", err)
		}
		limit := now.AddDate(0, 0, 5)
		for _, s := range slots {
			if s.Start.After(limit) {
				t.Fatalf("slot %v starts after the 5-day advance limit %v", s, limit)
			}
		}
	})

	t.Run("inactive link 404s", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", true)
		in := validLinkInput(calID)
		in.Active = false
		if _, err := f.svc.CreateLink(ctx, "u1", in); err != nil {
			t.Fatalf("seed link: %v", err)
		}
		now := f.clock.now
		_, err := f.svc.PublicSlots(ctx, "intro-call", now, now.AddDate(0, 0, 1))
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}
