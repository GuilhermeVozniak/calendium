package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
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
	templates *fakeEventTemplateRepo
	sets      *fakeCalendarSetRepo
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
	templates := newEventTemplateRepo()
	sets := newCalendarSetRepo()
	provider := newCalendarProvider()

	svc := NewCalendarService(CalendarServiceDeps{
		Subscriptions:     newSubscriptionRepo(),
		Accounts:          accounts,
		Calendars:         calendars,
		Events:            events,
		Templates:         templates,
		Sets:              sets,
		CalendarProviders: map[domain.Provider]port.CalendarProvider{domain.ProviderGoogle: provider},
		OAuth:             map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:             clock,
		SelfHosted:        true,
	})
	return &calFixture{svc, accounts, calendars, events, templates, sets, provider, clock}
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

// --- CalendarService event notes (M2.8 Task 4) -------------------------------

type fakeEventNoteRepo struct {
	byEvent map[string]domain.EventNote
}

var _ port.EventNoteRepo = (*fakeEventNoteRepo)(nil)

func newFakeEventNoteRepo() *fakeEventNoteRepo {
	return &fakeEventNoteRepo{byEvent: map[string]domain.EventNote{}}
}

func (r *fakeEventNoteRepo) Upsert(_ context.Context, n domain.EventNote) (domain.EventNote, error) {
	if n.BodyMD == "" && len(n.Links) == 0 {
		delete(r.byEvent, n.EventID)
		return domain.EventNote{EventID: n.EventID, UserID: n.UserID, Links: []string{}}, nil
	}
	if n.Links == nil {
		n.Links = []string{}
	}
	n.UpdatedAt = time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	r.byEvent[n.EventID] = n
	return n, nil
}

func (r *fakeEventNoteRepo) GetByEventID(_ context.Context, eventID string) (domain.EventNote, error) {
	n, ok := r.byEvent[eventID]
	if !ok {
		return domain.EventNote{}, domain.ErrNotFound
	}
	return n, nil
}

func (r *fakeEventNoteRepo) ListByUser(_ context.Context, userID string) ([]domain.EventNote, error) {
	out := []domain.EventNote{}
	for _, n := range r.byEvent {
		if n.UserID == userID {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EventID < out[j].EventID })
	return out, nil
}

// noteSvc rewires the fixture's repos into a CalendarService that also has
// the note repo (newCalFixture predates Notes and leaves it nil).
func noteSvc(f *calFixture, notes port.EventNoteRepo) *CalendarService {
	return NewCalendarService(CalendarServiceDeps{
		Accounts:   f.accounts,
		Calendars:  f.calendars,
		Events:     f.events,
		Clock:      f.clock,
		SelfHosted: true,
		Notes:      notes,
	})
}

func TestEventNoteGetReturnsEmptyWhenMissing(t *testing.T) {
	ctx := context.Background()
	f := newCalFixture(t)
	seedEvent(f, "ev1", nil)
	svc := noteSvc(f, newFakeEventNoteRepo())

	got, err := svc.GetEventNote(ctx, "u1", "ev1")
	if err != nil {
		t.Fatalf("GetEventNote: %v", err)
	}
	if got.EventID != "ev1" || got.BodyMD != "" {
		t.Fatalf("got = %+v, want empty note for ev1", got)
	}
	if got.Links == nil || len(got.Links) != 0 {
		t.Fatalf("links = %#v, want non-nil empty slice", got.Links)
	}
}

func TestEventNotePutThenGetRoundTrips(t *testing.T) {
	ctx := context.Background()
	f := newCalFixture(t)
	seedEvent(f, "ev1", nil)
	notes := newFakeEventNoteRepo()
	svc := noteSvc(f, notes)

	saved, err := svc.PutEventNote(ctx, "u1", "ev1", "# Prep", []string{" https://notion.so/doc ", ""})
	if err != nil {
		t.Fatalf("PutEventNote: %v", err)
	}
	if saved.BodyMD != "# Prep" || saved.UserID != "u1" {
		t.Fatalf("saved = %+v", saved)
	}
	// Links are trimmed and empties dropped before persisting.
	if want := []string{"https://notion.so/doc"}; !reflect.DeepEqual(saved.Links, want) {
		t.Fatalf("links = %v, want %v", saved.Links, want)
	}

	got, err := svc.GetEventNote(ctx, "u1", "ev1")
	if err != nil {
		t.Fatalf("GetEventNote: %v", err)
	}
	if got.BodyMD != "# Prep" || !reflect.DeepEqual(got.Links, []string{"https://notion.so/doc"}) {
		t.Fatalf("got = %+v, want stored note", got)
	}
}

func TestEventNotePutRejectsNonHTTPLinks(t *testing.T) {
	ctx := context.Background()
	f := newCalFixture(t)
	seedEvent(f, "ev1", nil)
	svc := noteSvc(f, newFakeEventNoteRepo())

	for _, link := range []string{"javascript:alert(1)", "notion.so/doc", "ftp://x.test/f"} {
		if _, err := svc.PutEventNote(ctx, "u1", "ev1", "body", []string{link}); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("link %q: err = %v, want ErrValidation", link, err)
		}
	}
}

func TestEventNoteEmptyPutClears(t *testing.T) {
	ctx := context.Background()
	f := newCalFixture(t)
	seedEvent(f, "ev1", nil)
	notes := newFakeEventNoteRepo()
	svc := noteSvc(f, notes)

	if _, err := svc.PutEventNote(ctx, "u1", "ev1", "scratch", nil); err != nil {
		t.Fatalf("seed put: %v", err)
	}
	if _, err := svc.PutEventNote(ctx, "u1", "ev1", "", nil); err != nil {
		t.Fatalf("clearing put: %v", err)
	}
	if _, ok := notes.byEvent["ev1"]; ok {
		t.Fatal("note still stored after empty put")
	}
	got, err := svc.GetEventNote(ctx, "u1", "ev1")
	if err != nil || got.BodyMD != "" {
		t.Fatalf("get after clear = %+v, %v; want empty note, nil", got, err)
	}
}

func TestEventNoteOwnershipViaOwnedEvent(t *testing.T) {
	ctx := context.Background()
	f := newCalFixture(t)
	seedEvent(f, "ev1", nil)
	notes := newFakeEventNoteRepo()
	svc := noteSvc(f, notes)

	// u2 does not own ev1's calendar chain: both verbs 404, and nothing is
	// ever written for the foreign user.
	if _, err := svc.GetEventNote(ctx, "u2", "ev1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get: err = %v, want ErrNotFound", err)
	}
	if _, err := svc.PutEventNote(ctx, "u2", "ev1", "intruder", nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("put: err = %v, want ErrNotFound", err)
	}
	if len(notes.byEvent) != 0 {
		t.Fatal("note stored despite foreign user")
	}

	// Unknown event id 404s for the owner too.
	if _, err := svc.GetEventNote(ctx, "u1", "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown event: err = %v, want ErrNotFound", err)
	}
}

func TestEventNoteEntitlementGate(t *testing.T) {
	ctx := context.Background()
	f := newCalFixture(t)
	seedEvent(f, "ev1", nil)
	// Cloud mode with no subscription: every note verb is paywalled.
	svc := NewCalendarService(CalendarServiceDeps{
		Subscriptions: newSubscriptionRepo(),
		Accounts:      f.accounts,
		Calendars:     f.calendars,
		Events:        f.events,
		Clock:         f.clock,
		Notes:         newFakeEventNoteRepo(),
	})

	if _, err := svc.GetEventNote(ctx, "u1", "ev1"); !errors.Is(err, domain.ErrPaymentRequired) {
		t.Fatalf("get: err = %v, want ErrPaymentRequired", err)
	}
	if _, err := svc.PutEventNote(ctx, "u1", "ev1", "x", nil); !errors.Is(err, domain.ErrPaymentRequired) {
		t.Fatalf("put: err = %v, want ErrPaymentRequired", err)
	}
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

// TestAvailabilityRespectsWorkingHours proves Availability intersects its
// computed free gaps with the user's working hours (in settings.TimeZone)
// once CalendarServiceDeps.Settings is wired and WorkingHours is non-empty;
// a nil Settings repo or empty WorkingHours (the zero value) must leave the
// prior free-gap-only behavior untouched.
func TestAvailabilityRespectsWorkingHours(t *testing.T) {
	ctx := context.Background()
	hm := func(h, m int) time.Time { return time.Date(2026, 7, 7, h, m, 0, 0, time.UTC) }
	weekday := int(hm(0, 0).Weekday())

	newSvc := func(t *testing.T, settings *fakeUserSettingsRepo) *CalendarService {
		t.Helper()
		return NewCalendarService(CalendarServiceDeps{
			Accounts:   newAccountRepo(),
			Calendars:  newCalendarRepo(),
			Events:     newEventRepo(),
			Settings:   settings,
			Clock:      newClock(hm(0, 0)),
			SelfHosted: true,
		})
	}

	t.Run("no working hours configured: free gaps pass through unchanged", func(t *testing.T) {
		settingsRepo := newUserSettingsRepo()
		svc := newSvc(t, settingsRepo)

		got, err := svc.Availability(ctx, "u1", hm(9, 0), hm(17, 0), 30*time.Minute)
		if err != nil {
			t.Fatalf("Availability: %v", err)
		}
		want := []domain.AvailabilitySlot{{Start: hm(9, 0), End: hm(17, 0)}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("slots = %v, want %v", got, want)
		}
	})

	t.Run("working hours narrower than the free gap clip it", func(t *testing.T) {
		settingsRepo := newUserSettingsRepo()
		if err := settingsRepo.Upsert(ctx, domain.UserSettings{
			UserID:   "u1",
			TimeZone: "UTC",
			WorkingHours: []domain.AvailabilityWindow{
				{Weekday: weekday, Start: "10:00", End: "15:00"},
			},
		}); err != nil {
			t.Fatalf("seed settings: %v", err)
		}
		svc := newSvc(t, settingsRepo)

		got, err := svc.Availability(ctx, "u1", hm(9, 0), hm(17, 0), 30*time.Minute)
		if err != nil {
			t.Fatalf("Availability: %v", err)
		}
		want := []domain.AvailabilitySlot{{Start: hm(10, 0), End: hm(15, 0)}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("slots = %v, want %v", got, want)
		}
	})

	t.Run("working hours declared in a different time zone than the calendar events", func(t *testing.T) {
		// Working hours 09:00-17:00 America/New_York on the same calendar
		// date; the free gap is the full UTC day. Only the portion inside
		// the NY working window (converted to UTC, EDT = UTC-4 in July)
		// should survive.
		settingsRepo := newUserSettingsRepo()
		if err := settingsRepo.Upsert(ctx, domain.UserSettings{
			UserID:   "u1",
			TimeZone: "America/New_York",
			WorkingHours: []domain.AvailabilityWindow{
				{Weekday: weekday, Start: "09:00", End: "17:00"},
			},
		}); err != nil {
			t.Fatalf("seed settings: %v", err)
		}
		svc := newSvc(t, settingsRepo)

		loc, err := time.LoadLocation("America/New_York")
		if err != nil {
			t.Fatalf("LoadLocation: %v", err)
		}
		from := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
		to := from.AddDate(0, 0, 1)

		got, err := svc.Availability(ctx, "u1", from, to, 30*time.Minute)
		if err != nil {
			t.Fatalf("Availability: %v", err)
		}
		want := []domain.AvailabilitySlot{{
			Start: time.Date(2026, 7, 7, 9, 0, 0, 0, loc),
			End:   time.Date(2026, 7, 7, 17, 0, 0, 0, loc),
		}}
		if len(got) != 1 || !got[0].Start.Equal(want[0].Start) || !got[0].End.Equal(want[0].End) {
			t.Fatalf("slots = %v, want %v", got, want)
		}
	})

	t.Run("nil Settings repo leaves behavior unchanged", func(t *testing.T) {
		svc := NewCalendarService(CalendarServiceDeps{
			Accounts:   newAccountRepo(),
			Calendars:  newCalendarRepo(),
			Events:     newEventRepo(),
			Clock:      newClock(hm(0, 0)),
			SelfHosted: true,
		})
		got, err := svc.Availability(ctx, "u1", hm(9, 0), hm(17, 0), 30*time.Minute)
		if err != nil {
			t.Fatalf("Availability: %v", err)
		}
		want := []domain.AvailabilitySlot{{Start: hm(9, 0), End: hm(17, 0)}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("slots = %v, want %v", got, want)
		}
	})
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

// --- applyEventPatch field-level merge -----------------------------------
//
// These drive UpdateEvent against a LOCAL-ONLY event (ProviderEventID = "",
// same technique as "local-only event skips the provider" above) so the
// provider never overwrites the merged result with a canned response --
// the final mirror row is exactly applyEventPatch's output.

func TestApplyEventPatchFieldMerge(t *testing.T) {
	ctx := context.Background()

	// seedFullEvent seeds a local-only event with every field populated so
	// both the "patched" and "left alone" branches have something concrete
	// to assert against.
	seedFullEvent := func(f *calFixture) domain.Event {
		base := f.clock.Now()
		return seedEvent(f, "ev1", func(e *domain.Event) {
			e.ProviderEventID = ""
			e.Title = "Standup"
			e.Description = ptr("Daily sync")
			e.Location = ptr("Room A")
			e.Start = base.Add(time.Hour)
			e.End = base.Add(90 * time.Minute)
			e.AllDay = false
			e.RecurrenceRule = ptr("FREQ=DAILY")
			e.Attendees = []domain.Attendee{
				{Email: "alice@x.com", Response: domain.RsvpAccepted},
				{Email: "bob@x.com", Response: domain.RsvpDeclined},
			}
			e.ReminderMinutes = []int{10}
		})
	}

	t.Run("every set field is applied and attendee merge preserves existing RSVPs", func(t *testing.T) {
		f := newCalFixture(t)
		seedFullEvent(f)
		base := f.clock.Now()

		newStart := base.Add(3 * time.Hour)
		newEnd := base.Add(4 * time.Hour)
		patch := domain.EventPatch{
			Title:           ptr("Renamed Standup"),
			Description:     ptr("Updated agenda"),
			Location:        ptr("Room B"),
			Start:           &newStart,
			End:             &newEnd,
			AllDay:          ptr(true),
			RecurrenceRule:  ptr("FREQ=WEEKLY"),
			AttendeeEmails:  ptr([]string{"Alice@X.com", "carol@x.com"}), // bob dropped, alice re-added (different case), carol new
			ReminderMinutes: ptr([]int{5, 30}),
		}

		got, err := f.svc.UpdateEvent(ctx, "u1", "ev1", patch)
		if err != nil {
			t.Fatalf("UpdateEvent: %v", err)
		}

		if got.Title != "Renamed Standup" {
			t.Fatalf("Title = %q, want Renamed Standup", got.Title)
		}
		if got.Description == nil || *got.Description != "Updated agenda" {
			t.Fatalf("Description = %v, want Updated agenda", got.Description)
		}
		if got.Location == nil || *got.Location != "Room B" {
			t.Fatalf("Location = %v, want Room B", got.Location)
		}
		if !got.Start.Equal(newStart) {
			t.Fatalf("Start = %v, want %v", got.Start, newStart)
		}
		if !got.End.Equal(newEnd) {
			t.Fatalf("End = %v, want %v", got.End, newEnd)
		}
		if !got.AllDay {
			t.Fatal("AllDay = false, want true")
		}
		if got.RecurrenceRule == nil || *got.RecurrenceRule != "FREQ=WEEKLY" {
			t.Fatalf("RecurrenceRule = %v, want FREQ=WEEKLY", got.RecurrenceRule)
		}
		if !reflect.DeepEqual(got.ReminderMinutes, []int{5, 30}) {
			t.Fatalf("ReminderMinutes = %v, want [5 30]", got.ReminderMinutes)
		}

		// Attendee merge: order follows the patch's AttendeeEmails order.
		if len(got.Attendees) != 2 {
			t.Fatalf("Attendees = %+v, want 2 entries", got.Attendees)
		}
		if got.Attendees[0].Email != "Alice@X.com" || got.Attendees[0].Response != domain.RsvpAccepted {
			t.Fatalf("Attendees[0] = %+v, want Alice@X.com/accepted (existing RSVP preserved case-insensitively)", got.Attendees[0])
		}
		if got.Attendees[1].Email != "carol@x.com" || got.Attendees[1].Response != domain.RsvpNeedsAction {
			t.Fatalf("Attendees[1] = %+v, want carol@x.com/needs_action (new attendee defaults)", got.Attendees[1])
		}
		// bob@x.com was dropped from the patch's attendee list entirely.
		for _, a := range got.Attendees {
			if strings.EqualFold(a.Email, "bob@x.com") {
				t.Fatalf("bob@x.com still present after being dropped from the patch: %+v", got.Attendees)
			}
		}

		// Status carries no patch field: untouched regardless of what else changed.
		if got.Status != domain.EventConfirmed {
			t.Fatalf("Status = %q, want confirmed (unpatchable field left alone)", got.Status)
		}

		stored, ok := f.events.byID["ev1"]
		if !ok {
			t.Fatal("event not persisted to mirror")
		}
		if !reflect.DeepEqual(stored, got) {
			t.Fatalf("mirror = %+v, want %+v", stored, got)
		}
	})

	t.Run("nil patch fields leave every corresponding value untouched", func(t *testing.T) {
		f := newCalFixture(t)
		original := seedFullEvent(f)

		// Only Title is set; every other EventPatch field is nil, exercising
		// the nil branch for each field in applyEventPatch.
		got, err := f.svc.UpdateEvent(ctx, "u1", "ev1", domain.EventPatch{Title: ptr("Only title changed")})
		if err != nil {
			t.Fatalf("UpdateEvent: %v", err)
		}

		if got.Title != "Only title changed" {
			t.Fatalf("Title = %q, want Only title changed", got.Title)
		}
		if got.Description == nil || *got.Description != *original.Description {
			t.Fatalf("Description = %v, want unchanged %v", got.Description, original.Description)
		}
		if got.Location == nil || *got.Location != *original.Location {
			t.Fatalf("Location = %v, want unchanged %v", got.Location, original.Location)
		}
		if !got.Start.Equal(original.Start) {
			t.Fatalf("Start = %v, want unchanged %v", got.Start, original.Start)
		}
		if !got.End.Equal(original.End) {
			t.Fatalf("End = %v, want unchanged %v", got.End, original.End)
		}
		if got.AllDay != original.AllDay {
			t.Fatalf("AllDay = %v, want unchanged %v", got.AllDay, original.AllDay)
		}
		if got.RecurrenceRule == nil || *got.RecurrenceRule != *original.RecurrenceRule {
			t.Fatalf("RecurrenceRule = %v, want unchanged %v", got.RecurrenceRule, original.RecurrenceRule)
		}
		if !reflect.DeepEqual(got.Attendees, original.Attendees) {
			t.Fatalf("Attendees = %+v, want unchanged %+v (RSVP responses preserved)", got.Attendees, original.Attendees)
		}
		if !reflect.DeepEqual(got.ReminderMinutes, original.ReminderMinutes) {
			t.Fatalf("ReminderMinutes = %v, want unchanged %v", got.ReminderMinutes, original.ReminderMinutes)
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

		got, err := f.svc.RSVP(ctx, "u1", "ev1", domain.RsvpAccepted, "see you there")
		if err != nil {
			t.Fatalf("RSVP: %v", err)
		}
		if len(f.provider.rsvpCalls) != 1 || f.provider.rsvpCalls[0] != domain.RsvpAccepted {
			t.Fatalf("provider rsvpCalls = %v, want [accepted]", f.provider.rsvpCalls)
		}
		if len(f.provider.rsvpComments) != 1 || f.provider.rsvpComments[0] != "see you there" {
			t.Fatalf("provider rsvpComments = %v, want the comment passed through", f.provider.rsvpComments)
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

		got, err := f.svc.RSVP(ctx, "u1", "ev1", domain.RsvpDeclined, "")
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

		got, err := f.svc.RSVP(ctx, "u1", "ev1", domain.RsvpAccepted, "")
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
		_, err := f.svc.RSVP(ctx, "intruder", "ev1", domain.RsvpAccepted, "")
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
		_, err := svc.RSVP(ctx, "u1", "ev1", domain.RsvpAccepted, "")
		wantPaymentRequired(t, err)
	})
	t.Run("Availability", func(t *testing.T) {
		_, err := svc.Availability(ctx, "u1", base, base.Add(time.Hour), 30*time.Minute)
		wantPaymentRequired(t, err)
	})

	// Template methods
	t.Run("ListEventTemplates", func(t *testing.T) {
		_, err := svc.ListEventTemplates(ctx, "u1")
		wantPaymentRequired(t, err)
	})
	t.Run("CreateEventTemplate", func(t *testing.T) {
		_, err := svc.CreateEventTemplate(ctx, "u1", domain.EventTemplateInput{
			Name: "Meeting", Title: "Team Meeting", DurationMinutes: 60,
		})
		wantPaymentRequired(t, err)
	})
	t.Run("UpdateEventTemplate", func(t *testing.T) {
		_, err := svc.UpdateEventTemplate(ctx, "u1", "t1", domain.EventTemplateInput{
			Name: "Meeting", Title: "Team Meeting", DurationMinutes: 60,
		})
		wantPaymentRequired(t, err)
	})
	t.Run("DeleteEventTemplate", func(t *testing.T) {
		err := svc.DeleteEventTemplate(ctx, "u1", "t1")
		wantPaymentRequired(t, err)
	})
	t.Run("UseEventTemplate", func(t *testing.T) {
		err := svc.UseEventTemplate(ctx, "u1", "t1")
		wantPaymentRequired(t, err)
	})

	// Calendar Set methods
	t.Run("ListCalendarSets", func(t *testing.T) {
		_, err := svc.ListCalendarSets(ctx, "u1")
		wantPaymentRequired(t, err)
	})
	t.Run("CreateCalendarSet", func(t *testing.T) {
		_, err := svc.CreateCalendarSet(ctx, "u1", domain.CalendarSetInput{
			Name: "Work", CalendarIDs: []string{"cal1"},
		})
		wantPaymentRequired(t, err)
	})
	t.Run("UpdateCalendarSet", func(t *testing.T) {
		_, err := svc.UpdateCalendarSet(ctx, "u1", "s1", domain.CalendarSetInput{
			Name: "Work", CalendarIDs: []string{"cal1"},
		})
		wantPaymentRequired(t, err)
	})
	t.Run("DeleteCalendarSet", func(t *testing.T) {
		err := svc.DeleteCalendarSet(ctx, "u1", "s1")
		wantPaymentRequired(t, err)
	})

	// None of the above should have reached a repo write.
	if len(calendars.byID) != 0 || len(events.byID) != 0 {
		t.Fatal("paywall allowed a repo write")
	}
}

// --- Event Template tests ----

func TestEventTemplateList(t *testing.T) {
	ctx := context.Background()
	f := newCalFixture(t)

	// Create templates for different users
	t1, _ := f.svc.CreateEventTemplate(ctx, "u1", domain.EventTemplateInput{
		Name: "Meeting", Title: "Team Meeting", DurationMinutes: 60,
	})
	t2, _ := f.svc.CreateEventTemplate(ctx, "u1", domain.EventTemplateInput{
		Name: "Standup", Title: "Daily Standup", DurationMinutes: 15,
	})
	f.svc.CreateEventTemplate(ctx, "u2", domain.EventTemplateInput{
		Name: "Other", Title: "Other User's", DurationMinutes: 30,
	})

	result, err := f.svc.ListEventTemplates(ctx, "u1")
	if err != nil {
		t.Fatalf("ListEventTemplates failed: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 templates for u1, got %d", len(result))
	}
	if result[0].ID != t1.ID || result[1].ID != t2.ID {
		t.Fatalf("templates mismatch")
	}
}

func TestEventTemplateCreate(t *testing.T) {
	type testcase struct {
		name      string
		userID    string
		input     domain.EventTemplateInput
		wantErr   bool
		wantErrIs error
	}
	tests := []testcase{
		{
			name:   "valid template with defaults",
			userID: "u1",
			input: domain.EventTemplateInput{
				Name: "Meeting", Title: "Team Meeting",
			},
			wantErr: false,
		},
		{
			name:   "default duration to 30",
			userID: "u1",
			input: domain.EventTemplateInput{
				Name: "Quick", Title: "Quick Call", DurationMinutes: 0,
			},
			wantErr: false,
		},
		{
			name:   "empty name validation",
			userID: "u1",
			input: domain.EventTemplateInput{
				Name: "  ", Title: "Test",
			},
			wantErr:   true,
			wantErrIs: domain.ErrValidation,
		},
		{
			name:   "invalid calendar id",
			userID: "u1",
			input: domain.EventTemplateInput{
				Name: "Test", Title: "Test", CalendarID: ptr("unknown-cal"),
			},
			wantErr:   true,
			wantErrIs: domain.ErrNotFound,
		},
		{
			name:   "negative duration validation",
			userID: "u1",
			input: domain.EventTemplateInput{
				Name: "Bad", Title: "Bad", DurationMinutes: -15,
			},
			wantErr:   true,
			wantErrIs: domain.ErrValidation,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f := newCalFixture(t)

			result, err := f.svc.CreateEventTemplate(ctx, tc.userID, tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tc.wantErrIs != nil && !errors.Is(err, tc.wantErrIs) {
					t.Fatalf("err = %v, want %v", err, tc.wantErrIs)
				}
			} else {
				if err != nil {
					t.Fatalf("CreateEventTemplate failed: %v", err)
				}
				if result.Name != strings.TrimSpace(tc.input.Name) {
					t.Fatalf("name mismatch: got %q, want %q", result.Name, strings.TrimSpace(tc.input.Name))
				}
				if tc.input.DurationMinutes == 0 && result.DurationMinutes != 30 {
					t.Fatalf("expected default duration 30, got %d", result.DurationMinutes)
				}
				if result.UsageCount != 0 {
					t.Fatalf("expected UsageCount=0, got %d", result.UsageCount)
				}
			}
		})
	}
}

func TestEventTemplateUpdate(t *testing.T) {
	ctx := context.Background()
	f := newCalFixture(t)

	// Create a template
	created, _ := f.svc.CreateEventTemplate(ctx, "u1", domain.EventTemplateInput{
		Name: "Original", Title: "Original Title", DurationMinutes: 30,
	})

	// Update it
	updated, err := f.svc.UpdateEventTemplate(ctx, "u1", created.ID, domain.EventTemplateInput{
		Name: "Updated", Title: "Updated Title", DurationMinutes: 60,
	})
	if err != nil {
		t.Fatalf("UpdateEventTemplate failed: %v", err)
	}
	if updated.Name != "Updated" || updated.Title != "Updated Title" || updated.DurationMinutes != 60 {
		t.Fatalf("update failed")
	}
	if updated.UsageCount != 0 {
		t.Fatalf("expected UsageCount preserved as 0")
	}

	// Try to update another user's template
	_, err = f.svc.UpdateEventTemplate(ctx, "u2", created.ID, domain.EventTemplateInput{
		Name: "Hijacked", Title: "Hijacked", DurationMinutes: 15,
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for other user's template, got %v", err)
	}

	// Try to update with negative duration
	_, err = f.svc.UpdateEventTemplate(ctx, "u1", created.ID, domain.EventTemplateInput{
		Name: "Bad", Title: "Bad", DurationMinutes: -15,
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expected ErrValidation for negative duration, got %v", err)
	}
}

func TestEventTemplateCreateEmptyCalendarIDNormalizesToNil(t *testing.T) {
	ctx := context.Background()
	f := newCalFixture(t)

	// A pointer to "" (rather than a nil pointer) can arrive from JSON bodies
	// that send `"calendarId": ""` instead of omitting the field. It must be
	// treated as "no calendar override": normalized to nil before validation
	// and persistence, not passed to ownedCalendar (which would error) or
	// stored as a non-nil empty-string pointer.
	result, err := f.svc.CreateEventTemplate(ctx, "u1", domain.EventTemplateInput{
		Name: "Test", Title: "Test", CalendarID: ptr(""),
	})
	if err != nil {
		t.Fatalf("CreateEventTemplate with empty CalendarID failed: %v", err)
	}
	if result.CalendarID != nil {
		t.Fatalf("CalendarID = %v, want nil", result.CalendarID)
	}
}

func TestEventTemplateUpdateEmptyCalendarIDNormalizesToNil(t *testing.T) {
	ctx := context.Background()
	f := newCalFixture(t)

	created, err := f.svc.CreateEventTemplate(ctx, "u1", domain.EventTemplateInput{
		Name: "Original", Title: "Original Title", DurationMinutes: 30, CalendarID: ptr("cal1"),
	})
	if err != nil {
		t.Fatalf("CreateEventTemplate failed: %v", err)
	}

	updated, err := f.svc.UpdateEventTemplate(ctx, "u1", created.ID, domain.EventTemplateInput{
		Name: "Updated", Title: "Updated Title", DurationMinutes: 30, CalendarID: ptr(""),
	})
	if err != nil {
		t.Fatalf("UpdateEventTemplate with empty CalendarID failed: %v", err)
	}
	if updated.CalendarID != nil {
		t.Fatalf("CalendarID = %v, want nil", updated.CalendarID)
	}
}

func TestEventTemplateDelete(t *testing.T) {
	ctx := context.Background()
	f := newCalFixture(t)

	created, _ := f.svc.CreateEventTemplate(ctx, "u1", domain.EventTemplateInput{
		Name: "ToDelete", Title: "Delete Me", DurationMinutes: 30,
	})

	// Delete it
	err := f.svc.DeleteEventTemplate(ctx, "u1", created.ID)
	if err != nil {
		t.Fatalf("DeleteEventTemplate failed: %v", err)
	}

	// Verify it's gone
	_, err = f.svc.ListEventTemplates(ctx, "u1")
	if err != nil {
		t.Fatalf("ListEventTemplates failed: %v", err)
	}

	// Try to delete another user's template
	created2, _ := f.svc.CreateEventTemplate(ctx, "u1", domain.EventTemplateInput{
		Name: "Another", Title: "Another", DurationMinutes: 30,
	})
	err = f.svc.DeleteEventTemplate(ctx, "u2", created2.ID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestUseEventTemplate(t *testing.T) {
	ctx := context.Background()
	f := newCalFixture(t)

	created, _ := f.svc.CreateEventTemplate(ctx, "u1", domain.EventTemplateInput{
		Name: "Template", Title: "Test", DurationMinutes: 30,
	})

	// Use it
	err := f.svc.UseEventTemplate(ctx, "u1", created.ID)
	if err != nil {
		t.Fatalf("UseEventTemplate failed: %v", err)
	}

	// Verify usage count incremented
	templates, _ := f.svc.ListEventTemplates(ctx, "u1")
	if len(templates) != 1 || templates[0].UsageCount != 1 {
		t.Fatalf("expected UsageCount=1 after use")
	}

	// Use it again
	f.svc.UseEventTemplate(ctx, "u1", created.ID)
	templates, _ = f.svc.ListEventTemplates(ctx, "u1")
	if templates[0].UsageCount != 2 {
		t.Fatalf("expected UsageCount=2 after second use")
	}

	// Try to use another user's template
	err = f.svc.UseEventTemplate(ctx, "u2", created.ID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for other user's template, got %v", err)
	}
}

// --- Calendar Set tests ----

func TestCalendarSetList(t *testing.T) {
	ctx := context.Background()
	f := newCalFixture(t)

	// Ensure cal1 is in user's calendars
	f.calendars.order = []string{"cal1"}

	// Create sets for different users
	s1, _ := f.svc.CreateCalendarSet(ctx, "u1", domain.CalendarSetInput{
		Name: "Work", CalendarIDs: []string{"cal1"},
	})
	s2, _ := f.svc.CreateCalendarSet(ctx, "u1", domain.CalendarSetInput{
		Name: "Personal", CalendarIDs: []string{},
	})
	f.svc.CreateCalendarSet(ctx, "u2", domain.CalendarSetInput{
		Name: "Other", CalendarIDs: []string{},
	})

	result, err := f.svc.ListCalendarSets(ctx, "u1")
	if err != nil {
		t.Fatalf("ListCalendarSets failed: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 sets for u1, got %d", len(result))
	}
	if result[0].ID != s1.ID || result[1].ID != s2.ID {
		t.Fatalf("sets mismatch")
	}
}

func TestCalendarSetCreate(t *testing.T) {
	type testcase struct {
		name      string
		userID    string
		input     domain.CalendarSetInput
		wantErr   bool
		wantErrIs error
	}
	tests := []testcase{
		{
			name:   "valid set with calendar",
			userID: "u1",
			input: domain.CalendarSetInput{
				Name: "Work", CalendarIDs: []string{"cal1"},
			},
			wantErr: false,
		},
		{
			name:   "empty name validation",
			userID: "u1",
			input: domain.CalendarSetInput{
				Name: "  ", CalendarIDs: []string{},
			},
			wantErr:   true,
			wantErrIs: domain.ErrValidation,
		},
		{
			name:   "unknown calendar id",
			userID: "u1",
			input: domain.CalendarSetInput{
				Name: "Bad", CalendarIDs: []string{"unknown"},
			},
			wantErr:   true,
			wantErrIs: domain.ErrValidation,
		},
		{
			name:   "duplicate calendar ids de-duped",
			userID: "u1",
			input: domain.CalendarSetInput{
				Name: "Deduped", CalendarIDs: []string{"cal1", "cal1", "cal1"},
			},
			wantErr: false,
		},
		{
			name:   "empty string calendar id is rejected",
			userID: "u1",
			input: domain.CalendarSetInput{
				Name: "BadEmpty", CalendarIDs: []string{"cal1", "", "cal1"},
			},
			wantErr:   true,
			wantErrIs: domain.ErrValidation,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f := newCalFixture(t)
			f.calendars.order = []string{"cal1"}

			result, err := f.svc.CreateCalendarSet(ctx, tc.userID, tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tc.wantErrIs != nil && !errors.Is(err, tc.wantErrIs) {
					t.Fatalf("err = %v, want %v", err, tc.wantErrIs)
				}
			} else {
				if err != nil {
					t.Fatalf("CreateCalendarSet failed: %v", err)
				}
				if result.Name != strings.TrimSpace(tc.input.Name) {
					t.Fatalf("name mismatch")
				}
				if strings.Contains(tc.name, "Deduped") {
					if len(result.CalendarIDs) != 1 {
						t.Fatalf("expected deduped to have 1 calendar, got %d", len(result.CalendarIDs))
					}
				}
			}
		})
	}
}

func TestCalendarSetCreateDedupOrderDistinct(t *testing.T) {
	ctx := context.Background()
	f := newCalFixture(t)

	// Seed cal2 so we have two calendars
	if _, err := f.calendars.Upsert(ctx, f.calendars.byID["cal1"]); err != nil {
		t.Fatalf("register cal1: %v", err)
	}
	if _, err := f.calendars.Upsert(ctx, domain.Calendar{
		ID: "cal2", AccountID: "a1", ProviderCalendarID: "prov-cal-2",
		Name: "Personal", CanWrite: true, IsVisible: true,
	}); err != nil {
		t.Fatalf("seed cal2: %v", err)
	}

	// Create set with [cal2, cal1, cal2] → should dedup to [cal2, cal1]
	result, err := f.svc.CreateCalendarSet(ctx, "u1", domain.CalendarSetInput{
		Name:        "Mixed",
		CalendarIDs: []string{"cal2", "cal1", "cal2"},
	})
	if err != nil {
		t.Fatalf("CreateCalendarSet: %v", err)
	}
	if len(result.CalendarIDs) != 2 {
		t.Fatalf("expected 2 calendar IDs after dedup, got %d", len(result.CalendarIDs))
	}
	if result.CalendarIDs[0] != "cal2" || result.CalendarIDs[1] != "cal1" {
		t.Fatalf("dedup order = %v, want [cal2 cal1]", result.CalendarIDs)
	}
}

func TestCalendarSetUpdate(t *testing.T) {
	ctx := context.Background()
	f := newCalFixture(t)
	f.calendars.order = []string{"cal1"}

	created, _ := f.svc.CreateCalendarSet(ctx, "u1", domain.CalendarSetInput{
		Name: "Original", CalendarIDs: []string{"cal1"},
	})

	updated, err := f.svc.UpdateCalendarSet(ctx, "u1", created.ID, domain.CalendarSetInput{
		Name:        "Updated",
		CalendarIDs: []string{},
	})
	if err != nil {
		t.Fatalf("UpdateCalendarSet failed: %v", err)
	}
	if updated.Name != "Updated" || len(updated.CalendarIDs) != 0 {
		t.Fatalf("update failed")
	}

	// Try to update another user's set
	_, err = f.svc.UpdateCalendarSet(ctx, "u2", created.ID, domain.CalendarSetInput{
		Name: "Hijacked", CalendarIDs: []string{},
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestCalendarSetDelete(t *testing.T) {
	ctx := context.Background()
	f := newCalFixture(t)
	f.calendars.order = []string{"cal1"}

	created, _ := f.svc.CreateCalendarSet(ctx, "u1", domain.CalendarSetInput{
		Name: "ToDelete", CalendarIDs: []string{"cal1"},
	})

	err := f.svc.DeleteCalendarSet(ctx, "u1", created.ID)
	if err != nil {
		t.Fatalf("DeleteCalendarSet failed: %v", err)
	}

	// Try to delete another user's set
	created2, _ := f.svc.CreateCalendarSet(ctx, "u1", domain.CalendarSetInput{
		Name: "Another", CalendarIDs: []string{},
	})
	err = f.svc.DeleteCalendarSet(ctx, "u2", created2.ID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
