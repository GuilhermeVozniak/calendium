package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- fakes -------------------------------------------------------------------

// fakeInsightsManagedRepo is the all-kinds managed-event reader; rows are
// filtered by UserID like the SQL implementation.
type fakeInsightsManagedRepo struct {
	rows []domain.ManagedEvent
	err  error
}

func (f *fakeInsightsManagedRepo) ListAllByUser(_ context.Context, userID string) ([]domain.ManagedEvent, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := []domain.ManagedEvent{}
	for _, m := range f.rows {
		if m.UserID == userID {
			out = append(out, m)
		}
	}
	return out, nil
}

var _ port.InsightsManagedEventRepo = (*fakeInsightsManagedRepo)(nil)

// --- fixture -----------------------------------------------------------------

type insightsFixture struct {
	svc     *InsightsSvc
	events  *fakeEventRepo
	managed *fakeInsightsManagedRepo
	tasks   *fakeTaskRepo
	prefs   *fakeCalendarPrefsRepo
	clock   *fakeClock
}

// insightsWeek is Mon 2026-07-06 00:00 UTC .. Mon 2026-07-13 00:00 UTC.
var (
	weekFrom = time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC)
	weekTo   = time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
)

func day(d int, hour, minute int) time.Time {
	return time.Date(2026, 7, d, hour, minute, 0, 0, time.UTC)
}

func att(email string, name string, resp domain.RsvpStatus) domain.Attendee {
	a := domain.Attendee{Email: email, Response: resp}
	if name != "" {
		a.Name = &name
	}
	return a
}

// newInsightsFixture seeds the brief's fixture week for user u1:
//   - m1  Mon 09:00–10:00 with alice+bob            → 60 meeting minutes
//   - m2  Tue 14:00–15:30 with alice                → 90 meeting minutes
//   - f1  Wed 09:00–12:00 managed kind "focus"      → 180 focus minutes
//   - b1  Tue 15:30–15:40 managed kind "buffer"     → excluded (even with attendees)
//   - d1  Thu 10:00–11:00 declined by u1            → excluded
//   - t1  Fri 08:00–09:00 scheduled task block      → 60 task minutes
//
// Cross-tenant: intruder's event on their own calendar coexists in the repo
// and must never leak into u1's numbers (events repo wired with the
// calendar→account→user join like the SQL).
func newInsightsFixture(t *testing.T) *insightsFixture {
	t.Helper()
	ctx := context.Background()

	users := newUserRepo()
	if _, err := users.Upsert(ctx, domain.User{ID: "u1", Email: "u1@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := users.Upsert(ctx, domain.User{ID: "u2", Email: "u2@example.com"}); err != nil {
		t.Fatal(err)
	}

	accounts := newAccountRepo()
	for _, a := range []domain.ConnectedAccount{
		{ID: "a1", UserID: "u1", Email: "u1+work@example.com"},
		{ID: "a2", UserID: "u2", Email: "u2@example.com"},
	} {
		if _, err := accounts.Create(ctx, a); err != nil {
			t.Fatal(err)
		}
	}

	cals := newCalendarRepo()
	cals.byID["c1"] = domain.Calendar{ID: "c1", AccountID: "a1", IsVisible: true}
	cals.byID["c2"] = domain.Calendar{ID: "c2", AccountID: "a2", IsVisible: true}

	events := newEventRepo()
	events.calendars = cals
	events.accounts = accounts
	seed := []domain.Event{
		{ID: "m1", CalendarID: "c1", Title: "Roadmap review", Start: day(6, 9, 0), End: day(6, 10, 0),
			Status: domain.EventConfirmed,
			Attendees: []domain.Attendee{
				att("u1@example.com", "Me", domain.RsvpAccepted),
				att("alice@example.com", "Alice", domain.RsvpAccepted),
				att("bob@example.com", "Bob", domain.RsvpAccepted),
			}},
		{ID: "m2", CalendarID: "c1", Title: "1:1", Start: day(7, 14, 0), End: day(7, 15, 30),
			Status: domain.EventConfirmed,
			Attendees: []domain.Attendee{
				att("u1+work@example.com", "Me", domain.RsvpAccepted),
				att("alice@example.com", "Alice", domain.RsvpAccepted),
			}},
		{ID: "f1", CalendarID: "c1", Title: "Focus", Start: day(8, 9, 0), End: day(8, 12, 0),
			Status: domain.EventConfirmed},
		{ID: "b1", CalendarID: "c1", Title: "Buffer", Start: day(7, 15, 30), End: day(7, 15, 40),
			Status: domain.EventConfirmed,
			Attendees: []domain.Attendee{
				att("u1@example.com", "", domain.RsvpAccepted),
				att("alice@example.com", "", domain.RsvpAccepted),
			}},
		{ID: "d1", CalendarID: "c1", Title: "Vendor pitch", Start: day(9, 10, 0), End: day(9, 11, 0),
			Status: domain.EventConfirmed,
			Attendees: []domain.Attendee{
				att("u1@example.com", "", domain.RsvpDeclined),
				att("carol@example.com", "Carol", domain.RsvpAccepted),
				att("dave@example.com", "Dave", domain.RsvpAccepted),
			}},
		// Foreign event: same repo, another tenant's calendar.
		{ID: "x1", CalendarID: "c2", Title: "Intruder meeting", Start: day(6, 9, 0), End: day(6, 17, 0),
			Status: domain.EventConfirmed,
			Attendees: []domain.Attendee{
				att("u2@example.com", "", domain.RsvpAccepted),
				att("eve@example.com", "Eve", domain.RsvpAccepted),
			}},
	}
	for _, e := range seed {
		if _, err := events.Upsert(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	managed := &fakeInsightsManagedRepo{rows: []domain.ManagedEvent{
		{EventID: "f1", UserID: "u1", Kind: domain.ManagedFocus},
		{EventID: "b1", UserID: "u1", Kind: domain.ManagedBuffer},
	}}

	tasks := newTaskRepo()
	start, end := day(10, 8, 0), day(10, 9, 0)
	if _, err := tasks.Create(ctx, domain.Task{
		ID: "t1", UserID: "u1", Title: "Write launch notes",
		ScheduledStart: &start, ScheduledEnd: &end,
	}); err != nil {
		t.Fatal(err)
	}

	prefs := newCalendarPrefsRepo()
	clock := newClock(day(12, 12, 0))
	svc := NewInsightsService(InsightsServiceDeps{
		Users:      users,
		Accounts:   accounts,
		Events:     events,
		Managed:    managed,
		Tasks:      tasks,
		Prefs:      prefs,
		Clock:      clock,
		SelfHosted: true,
	})
	return &insightsFixture{svc: svc, events: events, managed: managed, tasks: tasks, prefs: prefs, clock: clock}
}

// --- tests -------------------------------------------------------------------

func TestTimeInsightsFixtureWeekTotals(t *testing.T) {
	ctx := context.Background()
	fx := newInsightsFixture(t)

	got, err := fx.svc.TimeInsights(ctx, "u1", weekFrom, weekTo)
	if err != nil {
		t.Fatalf("TimeInsights: %v", err)
	}
	if got.MeetingMinutes != 150 {
		t.Errorf("MeetingMinutes = %d, want 150 (60 + 90)", got.MeetingMinutes)
	}
	if got.MeetingCount != 2 {
		t.Errorf("MeetingCount = %d, want 2", got.MeetingCount)
	}
	if got.FocusMinutes != 180 {
		t.Errorf("FocusMinutes = %d, want 180 (managed focus block)", got.FocusMinutes)
	}
	if got.TaskMinutes != 60 {
		t.Errorf("TaskMinutes = %d, want 60 (scheduled block)", got.TaskMinutes)
	}
	if !got.From.Equal(weekFrom) || !got.To.Equal(weekTo) {
		t.Errorf("range echoed = %v..%v, want %v..%v", got.From, got.To, weekFrom, weekTo)
	}
}

func TestTimeInsightsTopPeopleOrderingAndSelfExclusion(t *testing.T) {
	ctx := context.Background()
	fx := newInsightsFixture(t)

	got, err := fx.svc.TimeInsights(ctx, "u1", weekFrom, weekTo)
	if err != nil {
		t.Fatalf("TimeInsights: %v", err)
	}
	if len(got.TopPeople) != 2 {
		t.Fatalf("TopPeople = %+v, want exactly alice and bob", got.TopPeople)
	}
	alice, bob := got.TopPeople[0], got.TopPeople[1]
	if alice.Email != "alice@example.com" || alice.Minutes != 150 || alice.Meetings != 2 || alice.Name != "Alice" {
		t.Errorf("TopPeople[0] = %+v, want alice 150m across 2 meetings", alice)
	}
	if bob.Email != "bob@example.com" || bob.Minutes != 60 || bob.Meetings != 1 {
		t.Errorf("TopPeople[1] = %+v, want bob 60m across 1 meeting", bob)
	}
	for _, p := range got.TopPeople {
		if p.Email == "u1@example.com" || p.Email == "u1+work@example.com" {
			t.Errorf("self address %q leaked into TopPeople", p.Email)
		}
	}
}

func TestTimeInsightsTopPeopleCappedAtFive(t *testing.T) {
	ctx := context.Background()
	fx := newInsightsFixture(t)
	attendees := []domain.Attendee{att("u1@example.com", "", domain.RsvpAccepted)}
	for _, e := range []string{"p1@x.com", "p2@x.com", "p3@x.com", "p4@x.com", "p5@x.com", "p6@x.com"} {
		attendees = append(attendees, att(e, "", domain.RsvpAccepted))
	}
	if _, err := fx.events.Upsert(ctx, domain.Event{
		ID: "big", CalendarID: "c1", Title: "All hands", Status: domain.EventConfirmed,
		Start: day(10, 15, 0), End: day(10, 16, 0), Attendees: attendees,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := fx.svc.TimeInsights(ctx, "u1", weekFrom, weekTo)
	if err != nil {
		t.Fatalf("TimeInsights: %v", err)
	}
	if len(got.TopPeople) != 5 {
		t.Fatalf("len(TopPeople) = %d, want capped at 5", len(got.TopPeople))
	}
	// Alice (150m) must still rank first.
	if got.TopPeople[0].Email != "alice@example.com" {
		t.Errorf("TopPeople[0] = %+v, want alice", got.TopPeople[0])
	}
}

func TestTimeInsightsClipsOverlapToRange(t *testing.T) {
	ctx := context.Background()
	fx := newInsightsFixture(t)

	// Sub-range Mon 09:30 .. Tue 15:00 clips m1 to 30m and m2 to 60m.
	got, err := fx.svc.TimeInsights(ctx, "u1", day(6, 9, 30), day(7, 15, 0))
	if err != nil {
		t.Fatalf("TimeInsights: %v", err)
	}
	if got.MeetingMinutes != 90 {
		t.Errorf("MeetingMinutes = %d, want 90 (30 clipped + 60 clipped)", got.MeetingMinutes)
	}
	if got.MeetingCount != 2 {
		t.Errorf("MeetingCount = %d, want 2", got.MeetingCount)
	}
	// Clipped minutes flow into TopPeople too.
	if got.TopPeople[0].Email != "alice@example.com" || got.TopPeople[0].Minutes != 90 {
		t.Errorf("TopPeople[0] = %+v, want alice with 90 clipped minutes", got.TopPeople[0])
	}
}

func TestTimeInsightsByDaySplit(t *testing.T) {
	ctx := context.Background()
	fx := newInsightsFixture(t)

	got, err := fx.svc.TimeInsights(ctx, "u1", weekFrom, weekTo)
	if err != nil {
		t.Fatalf("TimeInsights: %v", err)
	}
	if len(got.ByDay) != 7 {
		t.Fatalf("len(ByDay) = %d, want 7", len(got.ByDay))
	}
	want := map[string]domain.DayStat{
		"2026-07-06": {Date: "2026-07-06", MeetingMinutes: 60},
		"2026-07-07": {Date: "2026-07-07", MeetingMinutes: 90},
		"2026-07-08": {Date: "2026-07-08", FocusMinutes: 180},
	}
	for i, d := range got.ByDay {
		if w, ok := want[d.Date]; ok {
			if d != w {
				t.Errorf("ByDay[%d] = %+v, want %+v", i, d, w)
			}
		} else if d.MeetingMinutes != 0 || d.FocusMinutes != 0 {
			t.Errorf("ByDay[%d] = %+v, want zeroes", i, d)
		}
	}
}

func TestTimeInsightsClassificationTable(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		event       domain.Event
		managedKind domain.ManagedKind // "" = not managed
		wantMeeting int
		wantFocus   int
	}{
		{
			name: "cancelled meeting excluded",
			event: domain.Event{ID: "e", CalendarID: "c1", Title: "Cancelled sync", Status: domain.EventCancelled,
				Start: day(6, 9, 0), End: day(6, 10, 0),
				Attendees: []domain.Attendee{att("u1@example.com", "", domain.RsvpAccepted), att("x@x.com", "", domain.RsvpAccepted)}},
		},
		{
			name: "all-day event excluded",
			event: domain.Event{ID: "e", CalendarID: "c1", Title: "Conference", Status: domain.EventConfirmed, AllDay: true,
				Start: day(6, 0, 0), End: day(7, 0, 0),
				Attendees: []domain.Attendee{att("u1@example.com", "", domain.RsvpAccepted), att("x@x.com", "", domain.RsvpAccepted)}},
		},
		{
			name: "managed buffer excluded from meeting time",
			event: domain.Event{ID: "e", CalendarID: "c1", Title: "Buffer", Status: domain.EventConfirmed,
				Start: day(6, 9, 0), End: day(6, 9, 10)},
			managedKind: domain.ManagedBuffer,
		},
		{
			name: "managed travel excluded from meeting time",
			event: domain.Event{ID: "e", CalendarID: "c1", Title: "Travel to HQ", Status: domain.EventConfirmed,
				Start: day(6, 9, 0), End: day(6, 9, 45)},
			managedKind: domain.ManagedTravel,
		},
		{
			name: "unknown future managed kind excluded generically",
			event: domain.Event{ID: "e", CalendarID: "c1", Title: "Hold", Status: domain.EventConfirmed,
				Start: day(6, 9, 0), End: day(6, 10, 0),
				Attendees: []domain.Attendee{att("u1@example.com", "", domain.RsvpAccepted), att("x@x.com", "", domain.RsvpAccepted)}},
			managedKind: domain.ManagedKind("ooo-hold"),
		},
		{
			name: "managed focus counts as focus",
			event: domain.Event{ID: "e", CalendarID: "c1", Title: "Focus", Status: domain.EventConfirmed,
				Start: day(6, 9, 0), End: day(6, 11, 0)},
			managedKind: domain.ManagedFocus,
			wantFocus:   120,
		},
		{
			name: "focus-titled user event counts as focus",
			event: domain.Event{ID: "e", CalendarID: "c1", Title: "Deep FOCUS writing", Status: domain.EventConfirmed,
				Start: day(6, 13, 0), End: day(6, 14, 30)},
			wantFocus: 90,
		},
		{
			// Title precedence: IsFocusTitle is checked BEFORE the attendee
			// count, so a focus-titled event with meeting-shaped attendees is
			// focus time, never meeting time.
			name: "focus-titled event with two attendees stays focus (title precedence)",
			event: domain.Event{ID: "e", CalendarID: "c1", Title: "Focus: roadmap deep-dive", Status: domain.EventConfirmed,
				Start: day(6, 9, 0), End: day(6, 10, 0),
				Attendees: []domain.Attendee{att("u1@example.com", "", domain.RsvpAccepted), att("x@x.com", "", domain.RsvpAccepted)}},
			wantFocus: 60,
		},
		{
			name: "declined-by-user meeting excluded",
			event: domain.Event{ID: "e", CalendarID: "c1", Title: "Pitch", Status: domain.EventConfirmed,
				Start: day(6, 9, 0), End: day(6, 10, 0),
				Attendees: []domain.Attendee{att("u1+work@example.com", "", domain.RsvpDeclined), att("x@x.com", "", domain.RsvpAccepted)}},
		},
		{
			name: "solo untitled block is neither",
			event: domain.Event{ID: "e", CalendarID: "c1", Title: "Errand", Status: domain.EventConfirmed,
				Start: day(6, 9, 0), End: day(6, 10, 0)},
		},
		{
			name: "two-attendee event is a meeting",
			event: domain.Event{ID: "e", CalendarID: "c1", Title: "Sync", Status: domain.EventConfirmed,
				Start: day(6, 9, 0), End: day(6, 10, 0),
				Attendees: []domain.Attendee{att("u1@example.com", "", domain.RsvpAccepted), att("x@x.com", "", domain.RsvpAccepted)}},
			wantMeeting: 60,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newInsightsFixture(t)
			// Start from a clean slate: drop the fixture's seeded events so the
			// table's single event is the only classified input.
			fx.events.byID = map[string]domain.Event{}
			fx.events.order = nil
			fx.managed.rows = nil
			fx.tasks.byID = map[string]domain.Task{}
			if _, err := fx.events.Upsert(ctx, tt.event); err != nil {
				t.Fatal(err)
			}
			if tt.managedKind != "" {
				fx.managed.rows = []domain.ManagedEvent{{EventID: tt.event.ID, UserID: "u1", Kind: tt.managedKind}}
			}

			got, err := fx.svc.TimeInsights(ctx, "u1", weekFrom, weekTo)
			if err != nil {
				t.Fatalf("TimeInsights: %v", err)
			}
			if got.MeetingMinutes != tt.wantMeeting {
				t.Errorf("MeetingMinutes = %d, want %d", got.MeetingMinutes, tt.wantMeeting)
			}
			if got.FocusMinutes != tt.wantFocus {
				t.Errorf("FocusMinutes = %d, want %d", got.FocusMinutes, tt.wantFocus)
			}
		})
	}
}

func TestTimeInsightsCrossTenantIsolation(t *testing.T) {
	ctx := context.Background()
	fx := newInsightsFixture(t)

	got, err := fx.svc.TimeInsights(ctx, "u1", weekFrom, weekTo)
	if err != nil {
		t.Fatalf("TimeInsights: %v", err)
	}
	// The intruder's 8-hour meeting (event x1 on calendar c2) must not move
	// any number.
	if got.MeetingMinutes != 150 {
		t.Errorf("MeetingMinutes = %d, want 150 — foreign tenant's event leaked in", got.MeetingMinutes)
	}
	for _, p := range got.TopPeople {
		if p.Email == "eve@example.com" || p.Email == "u2@example.com" {
			t.Errorf("foreign attendee %q leaked into TopPeople", p.Email)
		}
	}
}

func TestTimeInsightsFocusGoalScaledToRange(t *testing.T) {
	ctx := context.Background()
	fx := newInsightsFixture(t)
	prefs := domain.DefaultCalendarPrefs("u1")
	prefs.FocusGoalMinutesPerWeek = 600
	if err := fx.prefs.Upsert(ctx, prefs); err != nil {
		t.Fatal(err)
	}

	week, err := fx.svc.TimeInsights(ctx, "u1", weekFrom, weekTo)
	if err != nil {
		t.Fatalf("TimeInsights: %v", err)
	}
	if week.FocusGoalMinutes != 600 {
		t.Errorf("7-day FocusGoalMinutes = %d, want 600 (one full week)", week.FocusGoalMinutes)
	}

	half, err := fx.svc.TimeInsights(ctx, "u1", weekFrom, weekFrom.AddDate(0, 0, 14))
	if err != nil {
		t.Fatalf("TimeInsights: %v", err)
	}
	if half.FocusGoalMinutes != 1200 {
		t.Errorf("14-day FocusGoalMinutes = %d, want 1200 (two weeks)", half.FocusGoalMinutes)
	}
}

func TestTimeInsightsRangeValidation(t *testing.T) {
	ctx := context.Background()
	fx := newInsightsFixture(t)

	// 92 days is allowed; 92 days + 1 minute is not.
	if _, err := fx.svc.TimeInsights(ctx, "u1", weekFrom, weekFrom.AddDate(0, 0, 92)); err != nil {
		t.Fatalf("92-day range must be accepted, got %v", err)
	}
	if _, err := fx.svc.TimeInsights(ctx, "u1", weekFrom, weekFrom.AddDate(0, 0, 92).Add(time.Minute)); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("over-cap err = %v, want ErrValidation", err)
	}
	if _, err := fx.svc.TimeInsights(ctx, "u1", weekFrom, weekFrom); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("empty range err = %v, want ErrValidation", err)
	}
	if _, err := fx.svc.TimeInsights(ctx, "u1", weekTo, weekFrom); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("inverted range err = %v, want ErrValidation", err)
	}
	if _, err := fx.svc.TimeInsights(ctx, "u1", time.Time{}, weekTo); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("zero from err = %v, want ErrValidation", err)
	}
}

func TestTimeInsightsRequiresEntitlement(t *testing.T) {
	svc := NewInsightsService(InsightsServiceDeps{
		Subscriptions: newSubscriptionRepo(), // empty → ErrNotFound → 402
		Users:         newUserRepo(),
		Accounts:      newAccountRepo(),
		Events:        newEventRepo(),
		Managed:       &fakeInsightsManagedRepo{},
		Tasks:         newTaskRepo(),
		Prefs:         newCalendarPrefsRepo(),
		Clock:         newClock(day(12, 12, 0)),
		SelfHosted:    false,
	})
	if _, err := svc.TimeInsights(context.Background(), "u1", weekFrom, weekTo); !errors.Is(err, domain.ErrPaymentRequired) {
		t.Fatalf("err = %v, want ErrPaymentRequired", err)
	}
}

func TestTimeInsightsEmptyRangeHasNonNilSlices(t *testing.T) {
	fx := newInsightsFixture(t)
	fx.events.byID = map[string]domain.Event{}
	fx.events.order = nil
	fx.tasks.byID = map[string]domain.Task{}

	got, err := fx.svc.TimeInsights(context.Background(), "u1", weekFrom, weekTo)
	if err != nil {
		t.Fatalf("TimeInsights: %v", err)
	}
	if got.TopPeople == nil || len(got.TopPeople) != 0 {
		t.Errorf("TopPeople = %#v, want non-nil empty", got.TopPeople)
	}
	if got.ByDay == nil || len(got.ByDay) != 7 {
		t.Errorf("ByDay = %#v, want 7 zeroed days", got.ByDay)
	}
}
