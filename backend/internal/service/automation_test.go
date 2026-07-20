package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- fakes -------------------------------------------------------------------

// fakeManagedEventRepo is an in-memory port.ManagedEventRepo.
type fakeManagedEventRepo struct {
	byEvent map[string]domain.ManagedEvent
	order   []string
}

func newManagedEventRepo() *fakeManagedEventRepo {
	return &fakeManagedEventRepo{byEvent: map[string]domain.ManagedEvent{}}
}

var _ port.ManagedEventRepo = (*fakeManagedEventRepo)(nil)

func (r *fakeManagedEventRepo) Create(_ context.Context, m domain.ManagedEvent) error {
	if _, dup := r.byEvent[m.EventID]; dup {
		return errors.New("duplicate managed event")
	}
	m.CreatedAt = time.Now()
	r.byEvent[m.EventID] = m
	r.order = append(r.order, m.EventID)
	return nil
}

func (r *fakeManagedEventRepo) GetByEventID(_ context.Context, eventID string) (domain.ManagedEvent, error) {
	m, ok := r.byEvent[eventID]
	if !ok {
		return domain.ManagedEvent{}, domain.ErrNotFound
	}
	return m, nil
}

func (r *fakeManagedEventRepo) ListByUser(_ context.Context, userID string, kind domain.ManagedKind) ([]domain.ManagedEvent, error) {
	out := []domain.ManagedEvent{}
	for _, id := range r.order {
		if m, ok := r.byEvent[id]; ok && m.UserID == userID && m.Kind == kind {
			out = append(out, m)
		}
	}
	return out, nil
}

func (r *fakeManagedEventRepo) ListBySourceEvent(_ context.Context, sourceEventID string) ([]domain.ManagedEvent, error) {
	out := []domain.ManagedEvent{}
	for _, id := range r.order {
		if m, ok := r.byEvent[id]; ok && m.SourceEventID != nil && *m.SourceEventID == sourceEventID {
			out = append(out, m)
		}
	}
	return out, nil
}

func (r *fakeManagedEventRepo) Delete(_ context.Context, eventID string) error {
	delete(r.byEvent, eventID)
	return nil
}

// automationCalendarStub is a port.CalendarService double whose CreateEvent
// and DeleteEvent write through to the fake event repo (the mirror), the way
// the real service's provider-first path does. failUsers simulates a provider
// outage: the call fails and — honesty policy — nothing local changes. Every
// other method panics via the embedded nil interface if reached.
type automationCalendarStub struct {
	port.CalendarService
	events     *fakeEventRepo
	failUsers  map[string]bool
	created    int
	deletedIDs []string
}

func (c *automationCalendarStub) CreateEvent(ctx context.Context, userID string, in domain.EventInput) (domain.Event, error) {
	if c.failUsers[userID] {
		return domain.Event{}, errors.New("provider down")
	}
	ev, err := c.events.Upsert(ctx, domain.Event{
		ID:         newID(),
		CalendarID: in.CalendarID,
		Title:      in.Title,
		Start:      in.Start,
		End:        in.End,
		Status:     domain.EventConfirmed,
	})
	if err != nil {
		return domain.Event{}, err
	}
	c.created++
	return ev, nil
}

func (c *automationCalendarStub) DeleteEvent(ctx context.Context, userID, eventID string) error {
	if c.failUsers[userID] {
		return errors.New("provider down")
	}
	c.deletedIDs = append(c.deletedIDs, eventID)
	return c.events.Delete(ctx, eventID)
}

// --- fixture -----------------------------------------------------------------

// autoMonday is Monday 2026-07-20; the clock starts 07:00 UTC that morning.
var autoMonday = time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)

type automationFixture struct {
	prefs   *fakeCalendarPrefsRepo
	accts   *fakeAccountRepo
	cals    *fakeCalendarRepo
	events  *fakeEventRepo
	managed *fakeManagedEventRepo
	calSvc  *automationCalendarStub
	clock   *fakeClock
	svc     *AutomationService
}

func newAutomationFixture() *automationFixture {
	prefs := newCalendarPrefsRepo()
	accts := newAccountRepo()
	cals := newCalendarRepo()
	cals.accounts = accts
	events := newEventRepo()
	events.calendars = cals
	events.accounts = accts
	managed := newManagedEventRepo()
	calSvc := &automationCalendarStub{events: events, failUsers: map[string]bool{}}
	clock := newClock(autoMonday.Add(7 * time.Hour))
	svc := NewAutomationService(AutomationServiceDeps{
		Prefs:       prefs,
		Calendars:   cals,
		Events:      events,
		Managed:     managed,
		CalendarSvc: calSvc,
		Clock:       clock,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return &automationFixture{prefs: prefs, accts: accts, cals: cals, events: events, managed: managed, calSvc: calSvc, clock: clock, svc: svc}
}

// addUser seeds prefs plus a primary writable calendar; goalMinutes 0 leaves
// FocusGuard off for that user.
func (f *automationFixture) addUser(t *testing.T, userID string, mutate func(*domain.CalendarPrefs)) domain.Calendar {
	t.Helper()
	ctx := context.Background()
	p := domain.DefaultCalendarPrefs(userID)
	mutate(&p)
	if err := f.prefs.Upsert(ctx, p); err != nil {
		t.Fatalf("seed prefs: %v", err)
	}
	a, err := f.accts.Create(ctx, domain.ConnectedAccount{
		ID: userID + "-acct", UserID: userID, Provider: domain.ProviderGoogle, Email: userID + "@example.com",
	})
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	c, err := f.cals.Upsert(ctx, domain.Calendar{
		AccountID: a.ID, ProviderCalendarID: newID(), Name: "Primary",
		IsPrimary: true, IsVisible: true, CanWrite: true,
	})
	if err != nil {
		t.Fatalf("seed calendar: %v", err)
	}
	return c
}

func (f *automationFixture) managedForUser(userID string) []domain.ManagedEvent {
	out, _ := f.managed.ListByUser(context.Background(), userID, domain.ManagedFocus)
	return out
}

// --- tests -------------------------------------------------------------------

func TestRunAutomationFocusCreatesTaggedProviderEvents(t *testing.T) {
	f := newAutomationFixture()
	f.addUser(t, "u1", func(p *domain.CalendarPrefs) { p.FocusGoalMinutesPerWeek = 120 })
	ctx := context.Background()

	if err := f.svc.RunAutomation(ctx); err != nil {
		t.Fatalf("RunAutomation: %v", err)
	}

	blocks := f.managedForUser("u1")
	if len(blocks) != 2 { // one 2h block per planned week (current + next)
		t.Fatalf("managed blocks = %d, want 2 (current + next week): %+v", len(blocks), blocks)
	}
	weeks := map[string]bool{}
	for _, m := range blocks {
		if m.Kind != domain.ManagedFocus || m.UserID != "u1" {
			t.Fatalf("tag = %+v", m)
		}
		if m.WeekStart == nil {
			t.Fatalf("focus tag without WeekStart: %+v", m)
		}
		weeks[m.WeekStart.Format("2006-01-02")] = true
		ev, err := f.events.GetByID(ctx, m.EventID)
		if err != nil {
			t.Fatalf("managed block has no mirror event: %v", err)
		}
		if ev.Title != focusEventTitle || ev.End.Sub(ev.Start) != 2*time.Hour {
			t.Fatalf("mirror event = %+v, want 2h %q block", ev, focusEventTitle)
		}
	}
	if !weeks["2026-07-20"] || !weeks["2026-07-27"] {
		t.Fatalf("weeks = %v, want 2026-07-20 and 2026-07-27", weeks)
	}
}

func TestRunAutomationFocusIsIdempotent(t *testing.T) {
	f := newAutomationFixture()
	f.addUser(t, "u1", func(p *domain.CalendarPrefs) { p.FocusGoalMinutesPerWeek = 120 })
	ctx := context.Background()

	if err := f.svc.RunAutomation(ctx); err != nil {
		t.Fatalf("first run: %v", err)
	}
	createdAfterFirst := f.calSvc.created

	if err := f.svc.RunAutomation(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if f.calSvc.created != createdAfterFirst {
		t.Fatalf("second run created %d more events — not idempotent", f.calSvc.created-createdAfterFirst)
	}
	if len(f.calSvc.deletedIDs) != 0 {
		t.Fatalf("second run deleted %v — not idempotent", f.calSvc.deletedIDs)
	}
	if got := len(f.managedForUser("u1")); got != 2 {
		t.Fatalf("managed blocks = %d after second run, want 2", got)
	}
}

func TestRunAutomationFocusUserBookingWinsAndBlockIsReplanned(t *testing.T) {
	f := newAutomationFixture()
	cal := f.addUser(t, "u1", func(p *domain.CalendarPrefs) { p.FocusGoalMinutesPerWeek = 120 })
	ctx := context.Background()

	if err := f.svc.RunAutomation(ctx); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// The user books a meeting exactly over the current week's focus block.
	var victim domain.ManagedEvent
	for _, m := range f.managedForUser("u1") {
		if m.WeekStart.Format("2006-01-02") == "2026-07-20" {
			victim = m
		}
	}
	block, err := f.events.GetByID(ctx, victim.EventID)
	if err != nil {
		t.Fatalf("victim block: %v", err)
	}
	userEv, err := f.events.Upsert(ctx, domain.Event{
		ID: "user-meeting", CalendarID: cal.ID, ProviderEventID: "pm-1",
		Title: "Standup", Start: block.Start, End: block.End, Status: domain.EventConfirmed,
	})
	if err != nil {
		t.Fatalf("seed user meeting: %v", err)
	}

	if err := f.svc.RunAutomation(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}

	// The overbooked managed block is gone — provider delete + tag delete.
	if _, err := f.events.GetByID(ctx, victim.EventID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("overbooked focus block still mirrored: err = %v", err)
	}
	if _, err := f.managed.GetByEventID(ctx, victim.EventID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("overbooked focus tag survived")
	}

	// The user's meeting is untouched: never deleted, never moved.
	for _, id := range f.calSvc.deletedIDs {
		if id == userEv.ID {
			t.Fatal("automation deleted a user event — forbidden")
		}
	}
	got, err := f.events.GetByID(ctx, userEv.ID)
	if err != nil {
		t.Fatalf("user meeting gone: %v", err)
	}
	if !got.Start.Equal(userEv.Start) || !got.End.Equal(userEv.End) || got.Title != "Standup" {
		t.Fatalf("user meeting mutated: %+v", got)
	}

	// A replacement 2h block exists for the same week and avoids the meeting.
	var replacement *domain.ManagedEvent
	for _, m := range f.managedForUser("u1") {
		if m.WeekStart.Format("2006-01-02") == "2026-07-20" {
			m := m
			replacement = &m
		}
	}
	if replacement == nil {
		t.Fatal("no replacement focus block for the overbooked week")
	}
	repl, err := f.events.GetByID(ctx, replacement.EventID)
	if err != nil {
		t.Fatalf("replacement mirror: %v", err)
	}
	if repl.End.Sub(repl.Start) != 2*time.Hour {
		t.Fatalf("replacement length = %v, want 2h", repl.End.Sub(repl.Start))
	}
	if repl.Start.Before(userEv.End) && userEv.Start.Before(repl.End) {
		t.Fatalf("replacement %v-%v overlaps the user meeting %v-%v",
			repl.Start, repl.End, userEv.Start, userEv.End)
	}
}

func TestRunAutomationSkipsUsersWithoutFocusGoal(t *testing.T) {
	f := newAutomationFixture()
	// Automation enabled (weather) but FocusGuard off: in ListAutomated, no blocks.
	f.addUser(t, "u1", func(p *domain.CalendarPrefs) { p.WeatherEnabled = true })

	if err := f.svc.RunAutomation(context.Background()); err != nil {
		t.Fatalf("RunAutomation: %v", err)
	}
	if f.calSvc.created != 0 || len(f.managedForUser("u1")) != 0 {
		t.Fatalf("created %d events for a user with focus goal 0", f.calSvc.created)
	}
}

func TestRunAutomationOneFailingUserDoesNotStallTheFleet(t *testing.T) {
	f := newAutomationFixture()
	f.addUser(t, "u_fail", func(p *domain.CalendarPrefs) { p.FocusGoalMinutesPerWeek = 60 })
	f.addUser(t, "u_ok", func(p *domain.CalendarPrefs) { p.FocusGoalMinutesPerWeek = 60 })
	f.calSvc.failUsers["u_fail"] = true

	if err := f.svc.RunAutomation(context.Background()); err != nil {
		t.Fatalf("RunAutomation must not fail the fleet: %v", err)
	}

	// The healthy user is fully planned regardless of iteration order.
	if got := len(f.managedForUser("u_ok")); got != 2 {
		t.Fatalf("u_ok managed blocks = %d, want 2", got)
	}
	// Honesty policy: the failing user's provider write failed, so nothing
	// local exists — no tags, no mirror events.
	if got := len(f.managedForUser("u_fail")); got != 0 {
		t.Fatalf("u_fail has %d managed tags after a provider failure", got)
	}
	evs, _ := f.events.ListInRange(context.Background(), "u_fail", autoMonday, autoMonday.AddDate(0, 0, 14), nil)
	for _, ev := range evs {
		if ev.Title == focusEventTitle {
			t.Fatalf("u_fail has a mirrored focus block despite provider failure: %+v", ev)
		}
	}
}

func TestRunAutomationUserWithoutWritablePrimaryIsSkipped(t *testing.T) {
	f := newAutomationFixture()
	ctx := context.Background()
	// u_nocal has a goal but no calendars at all.
	p := domain.DefaultCalendarPrefs("u_nocal")
	p.FocusGoalMinutesPerWeek = 60
	if err := f.prefs.Upsert(ctx, p); err != nil {
		t.Fatalf("seed prefs: %v", err)
	}
	f.addUser(t, "u_ok", func(p *domain.CalendarPrefs) { p.FocusGoalMinutesPerWeek = 60 })

	if err := f.svc.RunAutomation(ctx); err != nil {
		t.Fatalf("RunAutomation: %v", err)
	}
	if got := len(f.managedForUser("u_nocal")); got != 0 {
		t.Fatalf("u_nocal managed blocks = %d, want 0", got)
	}
	if got := len(f.managedForUser("u_ok")); got != 2 {
		t.Fatalf("u_ok managed blocks = %d, want 2", got)
	}
}

// TestStartOfWeek covers the Monday snap, including across the Amsterdam
// spring-forward Sunday (2026-03-29).
func TestStartOfWeek(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatalf("tz: %v", err)
	}
	monday := time.Date(2026, 3, 23, 0, 0, 0, 0, loc)

	for _, tc := range []struct {
		name string
		in   time.Time
		want time.Time
	}{
		{"monday midnight is itself", monday, monday},
		{"midweek", time.Date(2026, 3, 25, 13, 30, 0, 0, loc), monday},
		{"sunday snaps back to monday", time.Date(2026, 3, 29, 23, 0, 0, 0, loc), monday},
		{"utc instant resolves in loc", time.Date(2026, 3, 29, 21, 30, 0, 0, time.UTC), monday}, // 23:30 CEST Sunday
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := startOfWeek(tc.in, loc); !got.Equal(tc.want) {
				t.Fatalf("startOfWeek(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
