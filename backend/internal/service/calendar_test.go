package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- CalendarService fixture (Task 6) ----------------------------------------

// calFixture is a CalendarService wired to one Google account (a1, user u1,
// email me@x.com) owning one writable calendar (cal1 -> provider prov-cal-1),
// with a valid non-expiring access token so tokenSource never refreshes.
//
// NOTE: cal1 is seeded directly into fakeCalendarRepo.byID (bypassing its
// insertion-order-tracked Upsert). That is fine for every lookup that goes
// through GetByID (ownedCalendar/ownedEvent), which all of the write-through
// tests below use. It is NOT visible to ListByUser (which iterates the
// fake's insertion order, not byID) -- TestListCalendars registers cal1 via
// Upsert explicitly where that matters.
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

// seedEvent stores an event directly into the fixture's event mirror (via
// GetByID-visible byID, same caveat as cal1 above -- fine for every test here
// since none of them list events by range). Defaults to a writable cal1 event
// with a provider-backed id; mut can override any field.
func seedEvent(f *calFixture, id string, mut func(*domain.Event)) domain.Event {
	base := f.clock.Now()
	ev := domain.Event{
		ID: id, CalendarID: "cal1", ProviderEventID: "pe-1",
		Title: "Standup", Start: base.Add(time.Hour), End: base.Add(90 * time.Minute),
		Status: domain.EventConfirmed, Attendees: []domain.Attendee{},
	}
	if mut != nil {
		mut(&ev)
	}
	f.events.byID[ev.ID] = ev
	return ev
}

// --- CalendarService.CreateEvent write-through -------------------------------

func TestCreateEventWritesThroughToProviderAndMirror(t *testing.T) {
	ctx := context.Background()
	f := newCalFixture(t)
	base := f.clock.Now()

	// Canned provider result: what the upstream calendar returns post-create.
	f.provider.createdEvent = domain.Event{
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

	// Provider write-through: called with the calendar's PROVIDER id (never
	// the local id) and the raw create input.
	if f.provider.lastCreateCalendarID != "prov-cal-1" {
		t.Fatalf("provider calendarID = %q, want prov-cal-1", f.provider.lastCreateCalendarID)
	}
	if f.provider.lastCreateInput.Title != "Sync" {
		t.Fatalf("provider input title = %q, want Sync", f.provider.lastCreateInput.Title)
	}

	// Returned event: provider fields, but locally-generated ID and LOCAL
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

	// Local mirror upserted with the same row.
	stored, ok := f.events.byID[got.ID]
	if !ok {
		t.Fatal("event not persisted to mirror")
	}
	if !reflect.DeepEqual(stored, got) {
		t.Fatalf("mirror = %+v, want %+v", stored, got)
	}
}

// --- CalendarService.Availability free-window computation --------------------

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
		// Seed via Upsert (not a direct byID assignment): fakeEventRepo.ListInRange
		// -- which Availability calls -- iterates the fake's insertion-order
		// slice, not the byID map, so a direct byID write would be invisible to it.
		if _, err := events.Upsert(ctx, ev); err != nil {
			t.Fatalf("seed event %s: %v", ev.ID, err)
		}
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
			name: "event the user declined is treated as free",
			from: hm(9, 0), to: hm(17, 0), slot: slot,
			accountEmail: "me@x.com",
			events: []domain.Event{{
				Start: hm(12, 0), End: hm(13, 0), Status: domain.EventConfirmed,
				Attendees: []domain.Attendee{{Email: "me@x.com", Response: domain.RsvpDeclined}},
			}},
			want: []domain.AvailabilitySlot{{Start: hm(9, 0), End: hm(17, 0)}},
		},
		{
			name: "event the user accepted stays busy",
			from: hm(9, 0), to: hm(17, 0), slot: slot,
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

func TestAvailabilityValidation(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	svc := newAvailFixture(t, "", nil)

	tests := []struct {
		name     string
		from, to time.Time
		slot     time.Duration
	}{
		{"to equal from", base, base, 30 * time.Minute},
		{"to before from", base, base.Add(-time.Hour), 30 * time.Minute},
		{"zero slot duration", base, base.Add(time.Hour), 0},
		{"negative slot duration", base, base.Add(time.Hour), -time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.Availability(ctx, "u1", tt.from, tt.to, tt.slot)
			if !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("err = %v, want ErrValidation", err)
			}
		})
	}
}

// --- CalendarService.ListCalendars / UpdateCalendar --------------------------

func TestListCalendars(t *testing.T) {
	ctx := context.Background()

	t.Run("returns every calendar for the user", func(t *testing.T) {
		f := newCalFixture(t)
		// cal1 was seeded directly into byID (see newCalFixture's note), which
		// the fake's order-tracked ListByUser does not see; register it (and a
		// second calendar) via Upsert so both are visible.
		if _, err := f.calendars.Upsert(ctx, f.calendars.byID["cal1"]); err != nil {
			t.Fatalf("register cal1: %v", err)
		}
		if _, err := f.calendars.Upsert(ctx, domain.Calendar{
			ID: "cal2", AccountID: "a1", ProviderCalendarID: "prov-cal-2",
			Name: "Personal", CanWrite: true, IsVisible: true,
		}); err != nil {
			t.Fatalf("seed cal2: %v", err)
		}

		got, err := f.svc.ListCalendars(ctx, "u1")
		if err != nil {
			t.Fatalf("ListCalendars: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}
	})

	t.Run("empty repo returns a non-nil empty slice", func(t *testing.T) {
		svc := NewCalendarService(CalendarServiceDeps{
			Calendars:  newCalendarRepo(),
			Clock:      newClock(time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)),
			SelfHosted: true,
		})
		got, err := svc.ListCalendars(ctx, "u1")
		if err != nil {
			t.Fatalf("ListCalendars: %v", err)
		}
		if got == nil {
			t.Fatal("got nil, want non-nil empty slice")
		}
		if len(got) != 0 {
			t.Fatalf("len = %d, want 0", len(got))
		}
	})
}

func TestUpdateCalendar(t *testing.T) {
	ctx := context.Background()

	t.Run("patches IsVisible and Color and persists both", func(t *testing.T) {
		f := newCalFixture(t)
		got, err := f.svc.UpdateCalendar(ctx, "u1", "cal1", port.CalendarPatch{
			IsVisible: ptr(false),
			Color:     ptr("#ff0000"),
		})
		if err != nil {
			t.Fatalf("UpdateCalendar: %v", err)
		}
		if got.IsVisible || got.Color != "#ff0000" {
			t.Fatalf("got = %+v, want IsVisible=false Color=#ff0000", got)
		}
		stored := f.calendars.byID["cal1"]
		if stored.IsVisible || stored.Color != "#ff0000" {
			t.Fatalf("persisted = %+v, want IsVisible=false Color=#ff0000", stored)
		}
	})

	t.Run("patching only Color leaves IsVisible unchanged", func(t *testing.T) {
		f := newCalFixture(t)
		got, err := f.svc.UpdateCalendar(ctx, "u1", "cal1", port.CalendarPatch{
			Color: ptr("#00ff00"),
		})
		if err != nil {
			t.Fatalf("UpdateCalendar: %v", err)
		}
		if got.Color != "#00ff00" {
			t.Fatalf("Color = %q, want #00ff00", got.Color)
		}
		if !got.IsVisible {
			t.Fatal("IsVisible changed, want unchanged (true)")
		}
	})

	t.Run("foreign calendar is not found", func(t *testing.T) {
		f := newCalFixture(t)
		_, err := f.svc.UpdateCalendar(ctx, "intruder", "cal1", port.CalendarPatch{Color: ptr("#000000")})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("unknown calendar id is not found", func(t *testing.T) {
		f := newCalFixture(t)
		_, err := f.svc.UpdateCalendar(ctx, "u1", "nope", port.CalendarPatch{Color: ptr("#000000")})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

// --- CalendarService.ListEvents -----------------------------------------------

func TestListEvents(t *testing.T) {
	ctx := context.Background()

	t.Run("delegates to EventRepo.ListInRange with from/to/calendarIDs", func(t *testing.T) {
		f := newCalFixture(t)
		base := f.clock.Now()
		from, to := base, base.Add(4*time.Hour)

		seed := func(id, calendarID string, start, end time.Time) {
			if _, err := f.events.Upsert(ctx, domain.Event{
				ID: id, CalendarID: calendarID, Start: start, End: end,
				Status: domain.EventConfirmed, Attendees: []domain.Attendee{},
			}); err != nil {
				t.Fatalf("seed event %s: %v", id, err)
			}
		}
		seed("in-range-cal1", "cal1", base.Add(time.Hour), base.Add(2*time.Hour))    // in range, requested
		seed("in-range-cal2", "cal2", base.Add(time.Hour), base.Add(2*time.Hour))    // in range, requested
		seed("in-range-cal3", "cal3", base.Add(time.Hour), base.Add(2*time.Hour))    // in range, NOT requested
		seed("out-of-range", "cal1", base.Add(10*time.Hour), base.Add(11*time.Hour)) // outside [from,to)

		got, err := f.svc.ListEvents(ctx, "u1", from, to, []string{"cal1", "cal2"})
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		gotIDs := map[string]bool{}
		for _, e := range got {
			gotIDs[e.ID] = true
		}
		want := map[string]bool{"in-range-cal1": true, "in-range-cal2": true}
		if !reflect.DeepEqual(gotIDs, want) {
			t.Fatalf("event ids = %v, want %v", gotIDs, want)
		}
	})

	t.Run("empty result is a non-nil empty slice", func(t *testing.T) {
		f := newCalFixture(t)
		base := f.clock.Now()
		got, err := f.svc.ListEvents(ctx, "u1", base, base.Add(time.Hour), nil)
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if got == nil {
			t.Fatal("got nil slice, want non-nil empty slice")
		}
		if len(got) != 0 {
			t.Fatalf("len = %d, want 0", len(got))
		}
	})

	t.Run("to == from is a validation error", func(t *testing.T) {
		f := newCalFixture(t)
		base := f.clock.Now()
		_, err := f.svc.ListEvents(ctx, "u1", base, base, nil)
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("to before from is a validation error", func(t *testing.T) {
		f := newCalFixture(t)
		base := f.clock.Now()
		_, err := f.svc.ListEvents(ctx, "u1", base, base.Add(-time.Hour), nil)
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})
}

// --- CalendarService.CreateEvent validation / no-provider ---------------------

func TestCreateEventValidation(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name   string
		userID string
		mutate func(f *calFixture, in *domain.EventInput)
		want   error
	}{
		{
			name:   "empty calendar id",
			mutate: func(f *calFixture, in *domain.EventInput) { in.CalendarID = "" },
			want:   domain.ErrValidation,
		},
		{
			name:   "whitespace title",
			mutate: func(f *calFixture, in *domain.EventInput) { in.Title = "   " },
			want:   domain.ErrValidation,
		},
		{
			name:   "end not after start",
			mutate: func(f *calFixture, in *domain.EventInput) { in.End = in.Start },
			want:   domain.ErrValidation,
		},
		{
			name: "read-only calendar",
			mutate: func(f *calFixture, in *domain.EventInput) {
				c := f.calendars.byID["cal1"]
				c.CanWrite = false
				f.calendars.byID["cal1"] = c
			},
			want: domain.ErrValidation,
		},
		{
			name:   "foreign calendar",
			userID: "intruder",
			want:   domain.ErrNotFound,
		},
		{
			name:   "unknown calendar id",
			mutate: func(f *calFixture, in *domain.EventInput) { in.CalendarID = "nope" },
			want:   domain.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newCalFixture(t)
			base := f.clock.Now()
			in := domain.EventInput{
				CalendarID: "cal1",
				Title:      "Sync",
				Start:      base.Add(time.Hour),
				End:        base.Add(2 * time.Hour),
			}
			if tt.mutate != nil {
				tt.mutate(f, &in)
			}
			userID := tt.userID
			if userID == "" {
				userID = "u1"
			}
			_, err := f.svc.CreateEvent(ctx, userID, in)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if f.provider.lastCreateCalendarID != "" {
				t.Fatalf("provider was called (lastCreateCalendarID = %q), want not called", f.provider.lastCreateCalendarID)
			}
		})
	}
}

func TestCreateEventNoProvider(t *testing.T) {
	ctx := context.Background()
	f := newCalFixture(t)
	base := f.clock.Now()

	svc := NewCalendarService(CalendarServiceDeps{
		Accounts:          f.accounts,
		Calendars:         f.calendars,
		Events:            f.events,
		CalendarProviders: map[domain.Provider]port.CalendarProvider{}, // no gateway for google
		OAuth:             map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:             f.clock,
		SelfHosted:        true,
	})

	got, err := svc.CreateEvent(ctx, "u1", domain.EventInput{
		CalendarID: "cal1",
		Title:      "Offline sync",
		Start:      base.Add(time.Hour),
		End:        base.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if got.ID == "" {
		t.Fatal("event id not generated")
	}
	if got.CalendarID != "cal1" {
		t.Fatalf("CalendarID = %q, want cal1", got.CalendarID)
	}
	if got.ProviderEventID != "" {
		t.Fatalf("ProviderEventID = %q, want empty (provider skipped)", got.ProviderEventID)
	}
	if got.Status != domain.EventConfirmed {
		t.Fatalf("Status = %q, want confirmed", got.Status)
	}
	if _, ok := f.events.byID[got.ID]; !ok {
		t.Fatal("event not persisted to mirror")
	}
	if f.provider.lastCreateCalendarID != "" {
		t.Fatal("provider was called despite empty CalendarProviders map")
	}
}

// --- CalendarService.UpdateEvent / DeleteEvent write-through ------------------

func TestUpdateEventWriteThrough(t *testing.T) {
	ctx := context.Background()

	t.Run("write-through to provider and mirror", func(t *testing.T) {
		f := newCalFixture(t)
		seedEvent(f, "ev1", nil)
		f.provider.updatedEvent = domain.Event{ProviderEventID: "pe-1", Title: "Renamed", Status: domain.EventConfirmed}

		got, err := f.svc.UpdateEvent(ctx, "u1", "ev1", domain.EventPatch{Title: ptr("Renamed")})
		if err != nil {
			t.Fatalf("UpdateEvent: %v", err)
		}
		if f.provider.lastUpdateEventID != "pe-1" {
			t.Fatalf("provider updated event id = %q, want pe-1", f.provider.lastUpdateEventID)
		}
		if f.provider.lastUpdatePatch.Title == nil || *f.provider.lastUpdatePatch.Title != "Renamed" {
			t.Fatalf("provider patch title = %v, want Renamed", f.provider.lastUpdatePatch.Title)
		}
		if got.ID != "ev1" || got.CalendarID != "cal1" || got.Title != "Renamed" {
			t.Fatalf("got = %+v, want ID=ev1 CalendarID=cal1 Title=Renamed", got)
		}
		stored, ok := f.events.byID["ev1"]
		if !ok {
			t.Fatal("event not persisted to mirror")
		}
		if !reflect.DeepEqual(stored, got) {
			t.Fatalf("mirror = %+v, want %+v", stored, got)
		}
	})

	t.Run("local-only event skips the provider", func(t *testing.T) {
		f := newCalFixture(t)
		seedEvent(f, "ev1", func(e *domain.Event) { e.ProviderEventID = "" })

		got, err := f.svc.UpdateEvent(ctx, "u1", "ev1", domain.EventPatch{Title: ptr("Local rename")})
		if err != nil {
			t.Fatalf("UpdateEvent: %v", err)
		}
		if f.provider.lastUpdateEventID != "" {
			t.Fatalf("provider was called (lastUpdateEventID = %q), want not called", f.provider.lastUpdateEventID)
		}
		if got.Title != "Local rename" {
			t.Fatalf("Title = %q, want Local rename", got.Title)
		}
		if f.events.byID["ev1"].Title != "Local rename" {
			t.Fatal("mirror not patched")
		}
	})

	t.Run("start/end guard rejects end not after start", func(t *testing.T) {
		f := newCalFixture(t)
		seedEvent(f, "ev1", nil)
		base := f.clock.Now()
		_, err := f.svc.UpdateEvent(ctx, "u1", "ev1", domain.EventPatch{
			Start: ptr(base.Add(2 * time.Hour)),
			End:   ptr(base.Add(time.Hour)),
		})
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("read-only calendar is a validation error", func(t *testing.T) {
		f := newCalFixture(t)
		c := f.calendars.byID["cal1"]
		c.CanWrite = false
		f.calendars.byID["cal1"] = c
		seedEvent(f, "ev1", nil)

		_, err := f.svc.UpdateEvent(ctx, "u1", "ev1", domain.EventPatch{Title: ptr("x")})
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("foreign event is not found", func(t *testing.T) {
		f := newCalFixture(t)
		seedEvent(f, "ev1", nil)
		_, err := f.svc.UpdateEvent(ctx, "intruder", "ev1", domain.EventPatch{Title: ptr("x")})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("unknown event id is not found", func(t *testing.T) {
		f := newCalFixture(t)
		_, err := f.svc.UpdateEvent(ctx, "u1", "nope", domain.EventPatch{Title: ptr("x")})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

func TestDeleteEventWriteThrough(t *testing.T) {
	ctx := context.Background()

	t.Run("write-through to provider and mirror", func(t *testing.T) {
		f := newCalFixture(t)
		seedEvent(f, "ev1", nil)

		if err := f.svc.DeleteEvent(ctx, "u1", "ev1"); err != nil {
			t.Fatalf("DeleteEvent: %v", err)
		}
		if f.provider.lastDeleteEventID != "pe-1" {
			t.Fatalf("provider deleted event id = %q, want pe-1", f.provider.lastDeleteEventID)
		}
		if _, ok := f.events.byID["ev1"]; ok {
			t.Fatal("event still present in mirror")
		}
	})

	t.Run("local-only event skips the provider but still deletes locally", func(t *testing.T) {
		f := newCalFixture(t)
		seedEvent(f, "ev1", func(e *domain.Event) { e.ProviderEventID = "" })

		if err := f.svc.DeleteEvent(ctx, "u1", "ev1"); err != nil {
			t.Fatalf("DeleteEvent: %v", err)
		}
		if f.provider.lastDeleteEventID != "" {
			t.Fatalf("provider was called (lastDeleteEventID = %q), want not called", f.provider.lastDeleteEventID)
		}
		if _, ok := f.events.byID["ev1"]; ok {
			t.Fatal("event still present in mirror")
		}
	})

	t.Run("read-only calendar is a validation error", func(t *testing.T) {
		f := newCalFixture(t)
		c := f.calendars.byID["cal1"]
		c.CanWrite = false
		f.calendars.byID["cal1"] = c
		seedEvent(f, "ev1", nil)

		err := f.svc.DeleteEvent(ctx, "u1", "ev1")
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
		if f.provider.lastDeleteEventID != "" {
			t.Fatal("provider was called despite read-only calendar")
		}
	})

	t.Run("foreign event is not found", func(t *testing.T) {
		f := newCalFixture(t)
		seedEvent(f, "ev1", nil)
		err := f.svc.DeleteEvent(ctx, "intruder", "ev1")
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

// --- CalendarService.RSVP write-through ---------------------------------------

func TestRSVPWriteThrough(t *testing.T) {
	ctx := context.Background()

	t.Run("write-through to provider and mirror", func(t *testing.T) {
		f := newCalFixture(t)
		seedEvent(f, "ev1", func(e *domain.Event) {
			e.Attendees = []domain.Attendee{{Email: "me@x.com", Response: domain.RsvpNeedsAction}}
		})

		got, err := f.svc.RSVP(ctx, "u1", "ev1", domain.RsvpAccepted)
		if err != nil {
			t.Fatalf("RSVP: %v", err)
		}
		if len(f.provider.rsvpCalls) != 1 || f.provider.rsvpCalls[0] != domain.RsvpAccepted {
			t.Fatalf("provider rsvpCalls = %v, want [accepted]", f.provider.rsvpCalls)
		}
		if len(got.Attendees) != 1 || got.Attendees[0].Response != domain.RsvpAccepted {
			t.Fatalf("attendee response = %+v, want accepted", got.Attendees)
		}
		if f.events.byID["ev1"].Attendees[0].Response != domain.RsvpAccepted {
			t.Fatal("mirror attendee not updated")
		}
	})

	t.Run("local-only event skips the provider", func(t *testing.T) {
		f := newCalFixture(t)
		seedEvent(f, "ev1", func(e *domain.Event) {
			e.ProviderEventID = ""
			e.Attendees = []domain.Attendee{{Email: "me@x.com", Response: domain.RsvpNeedsAction}}
		})

		got, err := f.svc.RSVP(ctx, "u1", "ev1", domain.RsvpDeclined)
		if err != nil {
			t.Fatalf("RSVP: %v", err)
		}
		if len(f.provider.rsvpCalls) != 0 {
			t.Fatalf("provider was called: %v, want not called", f.provider.rsvpCalls)
		}
		if got.Attendees[0].Response != domain.RsvpDeclined {
			t.Fatal("attendee response not updated locally")
		}
		if f.events.byID["ev1"].Attendees[0].Response != domain.RsvpDeclined {
			t.Fatal("mirror attendee not updated")
		}
	})

	t.Run("no matching attendee leaves attendees unchanged", func(t *testing.T) {
		f := newCalFixture(t)
		seedEvent(f, "ev1", func(e *domain.Event) {
			e.Attendees = []domain.Attendee{{Email: "someone-else@x.com", Response: domain.RsvpNeedsAction}}
		})

		got, err := f.svc.RSVP(ctx, "u1", "ev1", domain.RsvpAccepted)
		if err != nil {
			t.Fatalf("RSVP: %v", err)
		}
		if len(f.provider.rsvpCalls) != 1 {
			t.Fatalf("provider rsvpCalls = %v, want called once", f.provider.rsvpCalls)
		}
		if got.Attendees[0].Response != domain.RsvpNeedsAction {
			t.Fatalf("attendee response = %v, want unchanged (needs_action)", got.Attendees[0].Response)
		}
		if f.events.byID["ev1"].Attendees[0].Response != domain.RsvpNeedsAction {
			t.Fatal("mirror attendee unexpectedly changed")
		}
	})

	t.Run("foreign event is not found", func(t *testing.T) {
		f := newCalFixture(t)
		seedEvent(f, "ev1", nil)
		_, err := f.svc.RSVP(ctx, "intruder", "ev1", domain.RsvpAccepted)
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

// --- CalendarService paywall ---------------------------------------------------

func TestCalendarPaywall(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	accounts := newAccountRepo()
	calendars := newCalendarRepo()
	events := newEventRepo()
	svc := NewCalendarService(CalendarServiceDeps{
		Subscriptions: newSubscriptionRepo(), // empty -> GetByUserID ErrNotFound -> ErrPaymentRequired
		Accounts:      accounts,
		Calendars:     calendars,
		Events:        events,
		Clock:         newClock(base),
		SelfHosted:    false,
	})
	wantPaymentRequired := func(t *testing.T, err error) {
		t.Helper()
		if !errors.Is(err, domain.ErrPaymentRequired) {
			t.Fatalf("err = %v, want ErrPaymentRequired", err)
		}
	}

	t.Run("ListCalendars", func(t *testing.T) {
		_, err := svc.ListCalendars(ctx, "u1")
		wantPaymentRequired(t, err)
	})
	t.Run("ListEvents", func(t *testing.T) {
		_, err := svc.ListEvents(ctx, "u1", base, base.Add(time.Hour), nil)
		wantPaymentRequired(t, err)
	})
	t.Run("UpdateCalendar", func(t *testing.T) {
		_, err := svc.UpdateCalendar(ctx, "u1", "cal1", port.CalendarPatch{})
		wantPaymentRequired(t, err)
	})
	t.Run("CreateEvent", func(t *testing.T) {
		_, err := svc.CreateEvent(ctx, "u1", domain.EventInput{
			CalendarID: "cal1", Title: "x", Start: base, End: base.Add(time.Hour),
		})
		wantPaymentRequired(t, err)
	})
	t.Run("UpdateEvent", func(t *testing.T) {
		_, err := svc.UpdateEvent(ctx, "u1", "ev1", domain.EventPatch{})
		wantPaymentRequired(t, err)
	})
	t.Run("DeleteEvent", func(t *testing.T) {
		err := svc.DeleteEvent(ctx, "u1", "ev1")
		wantPaymentRequired(t, err)
	})
	t.Run("RSVP", func(t *testing.T) {
		_, err := svc.RSVP(ctx, "u1", "ev1", domain.RsvpAccepted)
		wantPaymentRequired(t, err)
	})
	t.Run("Availability", func(t *testing.T) {
		_, err := svc.Availability(ctx, "u1", base, base.Add(time.Hour), 30*time.Minute)
		wantPaymentRequired(t, err)
	})

	// None of the above should have reached a repo write.
	if len(calendars.byID) != 0 || len(events.byID) != 0 {
		t.Fatal("paywall allowed a repo write")
	}
}
