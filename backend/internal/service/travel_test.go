package service

// travel_test.go covers M2.8 Task 12: the pure travel planner (planTravel)
// and the travel pass that runs inside AutomationService.RunAutomation —
// managed "Travel to <location>" blocks written provider-first, idempotent
// re-runs, block moves/deletes tracking the source event, entitlement,
// silent degrade when maps is unconfigured, and alert arming/re-arming.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sort"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- travel fakes ------------------------------------------------------------

// fakeTravelAlertRepo mirrors the postgres repo's contract: Upsert clears
// SentAt only when leaveAt changed; ListDue returns unsent due alerts oldest
// first; Delete is idempotent.
type fakeTravelAlertRepo struct {
	byEvent   map[string]domain.TravelAlert
	upsertErr error
	listErr   error
}

var _ port.TravelAlertRepo = (*fakeTravelAlertRepo)(nil)

func newTravelAlertRepo() *fakeTravelAlertRepo {
	return &fakeTravelAlertRepo{byEvent: map[string]domain.TravelAlert{}}
}

func (r *fakeTravelAlertRepo) Upsert(_ context.Context, eventID, userID string, leaveAt time.Time) error {
	if r.upsertErr != nil {
		return r.upsertErr
	}
	a := domain.TravelAlert{EventID: eventID, UserID: userID, LeaveAt: leaveAt}
	if existing, ok := r.byEvent[eventID]; ok && existing.LeaveAt.Equal(leaveAt) {
		a.SentAt = existing.SentAt // unchanged leave time keeps delivery state
	}
	r.byEvent[eventID] = a
	return nil
}

func (r *fakeTravelAlertRepo) ListDue(_ context.Context, now time.Time, limit int) ([]domain.TravelAlert, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	out := []domain.TravelAlert{}
	for _, a := range r.byEvent {
		if a.SentAt == nil && !a.LeaveAt.After(now) {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LeaveAt.Before(out[j].LeaveAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *fakeTravelAlertRepo) MarkSent(_ context.Context, eventID string, at time.Time) error {
	a, ok := r.byEvent[eventID]
	if !ok {
		return domain.ErrNotFound
	}
	a.SentAt = &at
	r.byEvent[eventID] = a
	return nil
}

func (r *fakeTravelAlertRepo) Delete(_ context.Context, eventID string) error {
	delete(r.byEvent, eventID)
	return nil
}

// fakeTravelMaps is a port.MapsProvider for travel tests: TravelTime records
// every call and answers a fixed duration or a programmable func.
type travelCall struct {
	FromLat, FromLon, ToLat, ToLon float64
	Mode                           domain.TravelMode
}

type fakeTravelMaps struct {
	travel time.Duration
	err    error
	fn     func(fromLat, fromLon, toLat, toLon float64, mode domain.TravelMode) (time.Duration, error)
	calls  []travelCall
}

var _ port.MapsProvider = (*fakeTravelMaps)(nil)

func (m *fakeTravelMaps) Autocomplete(context.Context, string, int) ([]domain.Place, error) {
	return nil, errors.New("not used in travel tests")
}

func (m *fakeTravelMaps) TravelTime(_ context.Context, fromLat, fromLon, toLat, toLon float64, mode domain.TravelMode) (time.Duration, error) {
	m.calls = append(m.calls, travelCall{fromLat, fromLon, toLat, toLon, mode})
	if m.fn != nil {
		return m.fn(fromLat, fromLon, toLat, toLon, mode)
	}
	return m.travel, m.err
}

// --- planner helpers ---------------------------------------------------------

var travelBase = time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)

func travelPrefs(userID string) domain.CalendarPrefs {
	p := domain.DefaultCalendarPrefs(userID)
	homeLat, homeLon := 52.37, 4.89
	p.TravelBuffers = true
	p.LeaveAlerts = true
	p.HomeLat, p.HomeLon = &homeLat, &homeLon
	return p
}

func locEvent(id string, start time.Time, dur time.Duration, lat, lon float64) domain.Event {
	loc := "Location " + id
	return domain.Event{
		ID: id, CalendarID: "c1", Title: "Meeting " + id, Location: &loc,
		Start: start, End: start.Add(dur), Status: domain.EventConfirmed,
		LocationLat: &lat, LocationLon: &lon,
	}
}

func fixedTravel(d time.Duration) travelTimeFunc {
	return func(_, _, _, _ float64, _ domain.TravelMode) (time.Duration, error) { return d, nil }
}

// --- planTravel --------------------------------------------------------------

// TestPlanTravelHomeOrigin: a lone located event routes from home, producing
// a [start-travel, start) block and leaveAt = block start - 5min.
func TestPlanTravelHomeOrigin(t *testing.T) {
	prefs := travelPrefs("u1")
	ev := locEvent("ev1", travelBase.Add(2*time.Hour), time.Hour, 48.86, 2.35)

	var got travelCall
	plans, err := planTravel(prefs, []domain.Event{ev}, nil, nil,
		func(fromLat, fromLon, toLat, toLon float64, mode domain.TravelMode) (time.Duration, error) {
			got = travelCall{fromLat, fromLon, toLat, toLon, mode}
			return 30 * time.Minute, nil
		})
	if err != nil {
		t.Fatalf("planTravel: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans = %d, want 1", len(plans))
	}
	p := plans[0]
	if p.EventID != "ev1" || p.Action != BlockCreate {
		t.Fatalf("plan = %+v, want ev1 create", p)
	}
	if !p.Start.Equal(ev.Start.Add(-30*time.Minute)) || !p.End.Equal(ev.Start) {
		t.Fatalf("block = [%v, %v), want [start-30m, start)", p.Start, p.End)
	}
	if !p.LeaveAt.Equal(ev.Start.Add(-35 * time.Minute)) {
		t.Fatalf("LeaveAt = %v, want start-35m", p.LeaveAt)
	}
	if p.Location != "Location ev1" {
		t.Fatalf("Location = %q", p.Location)
	}
	if got.FromLat != *prefs.HomeLat || got.FromLon != *prefs.HomeLon {
		t.Fatalf("origin = %v/%v, want home", got.FromLat, got.FromLon)
	}
	if got.ToLat != 48.86 || got.ToLon != 2.35 || got.Mode != domain.TravelDriving {
		t.Fatalf("dest/mode = %+v, want event coords + driving", got)
	}
}

// TestPlanTravelChainedOrigin: the second event the same local day routes
// from the first event's location, not home; an event on a PREVIOUS day
// never chains (home origin again).
func TestPlanTravelChainedOrigin(t *testing.T) {
	prefs := travelPrefs("u1")
	first := locEvent("ev1", travelBase.Add(1*time.Hour), time.Hour, 48.86, 2.35)
	second := locEvent("ev2", travelBase.Add(4*time.Hour), time.Hour, 41.39, 2.17)

	var origins [][2]float64
	plans, err := planTravel(prefs, []domain.Event{second, first}, nil, nil,
		func(fromLat, fromLon, _, _ float64, _ domain.TravelMode) (time.Duration, error) {
			origins = append(origins, [2]float64{fromLat, fromLon})
			return 20 * time.Minute, nil
		})
	if err != nil || len(plans) != 2 {
		t.Fatalf("plans = %d (%v), want 2", len(plans), err)
	}
	if origins[0] != [2]float64{*prefs.HomeLat, *prefs.HomeLon} {
		t.Fatalf("first origin = %v, want home", origins[0])
	}
	if origins[1] != [2]float64{48.86, 2.35} {
		t.Fatalf("second origin = %v, want first event's location", origins[1])
	}

	// Same two events split across days: no chaining, both route from home.
	prevDay := locEvent("ev0", travelBase.Add(-20*time.Hour), time.Hour, 48.86, 2.35)
	origins = nil
	if _, err := planTravel(prefs, []domain.Event{prevDay, second}, nil, nil,
		func(fromLat, fromLon, _, _ float64, _ domain.TravelMode) (time.Duration, error) {
			origins = append(origins, [2]float64{fromLat, fromLon})
			return 20 * time.Minute, nil
		}); err != nil {
		t.Fatalf("planTravel: %v", err)
	}
	if origins[1] != [2]float64{*prefs.HomeLat, *prefs.HomeLon} {
		t.Fatalf("cross-day origin = %v, want home", origins[1])
	}
}

// TestPlanTravelShortTripSkipped: trips under 10 minutes produce neither a
// block nor an alert.
func TestPlanTravelShortTripSkipped(t *testing.T) {
	prefs := travelPrefs("u1")
	ev := locEvent("ev1", travelBase.Add(2*time.Hour), time.Hour, 48.86, 2.35)
	plans, err := planTravel(prefs, []domain.Event{ev}, nil, nil, fixedTravel(9*time.Minute))
	if err != nil {
		t.Fatalf("planTravel: %v", err)
	}
	if len(plans) != 0 {
		t.Fatalf("plans = %+v, want none for a 9-minute trip", plans)
	}
}

// TestPlanTravelCapsAtThreeHours: a five-hour estimate is capped to a 3h block.
func TestPlanTravelCapsAtThreeHours(t *testing.T) {
	prefs := travelPrefs("u1")
	ev := locEvent("ev1", travelBase.Add(6*time.Hour), time.Hour, 48.86, 2.35)
	plans, err := planTravel(prefs, []domain.Event{ev}, nil, nil, fixedTravel(5*time.Hour))
	if err != nil || len(plans) != 1 {
		t.Fatalf("plans = %d (%v), want 1", len(plans), err)
	}
	if !plans[0].Start.Equal(ev.Start.Add(-3 * time.Hour)) {
		t.Fatalf("Start = %v, want start-3h (capped)", plans[0].Start)
	}
}

// TestPlanTravelSkipsUnroutableEvents: events without coordinates (free-typed
// or cleared locations) and managed events are skipped without vendor calls;
// managed events are not used as chain origins either.
func TestPlanTravelSkipsUnroutableEvents(t *testing.T) {
	prefs := travelPrefs("u1")
	free := domain.Event{ID: "free", Title: "No geo", Start: travelBase.Add(time.Hour), End: travelBase.Add(2 * time.Hour), Status: domain.EventConfirmed}
	managedEv := locEvent("tb1", travelBase.Add(90*time.Minute), 30*time.Minute, 50.0, 8.0)
	target := locEvent("ev1", travelBase.Add(4*time.Hour), time.Hour, 48.86, 2.35)

	var calls []travelCall
	plans, err := planTravel(prefs, []domain.Event{free, managedEv, target},
		map[string]struct{}{"tb1": {}}, nil,
		func(fromLat, fromLon, toLat, toLon float64, mode domain.TravelMode) (time.Duration, error) {
			calls = append(calls, travelCall{fromLat, fromLon, toLat, toLon, mode})
			return 30 * time.Minute, nil
		})
	if err != nil {
		t.Fatalf("planTravel: %v", err)
	}
	if len(plans) != 1 || plans[0].EventID != "ev1" {
		t.Fatalf("plans = %+v, want only ev1", plans)
	}
	if len(calls) != 1 {
		t.Fatalf("vendor calls = %d, want 1 (skips must not hit the vendor)", len(calls))
	}
	if calls[0].FromLat != *prefs.HomeLat {
		t.Fatalf("origin = %v, want home (managed event must not chain)", calls[0].FromLat)
	}
}

// TestPlanTravelMovedEventRefreshes: an owned travel block at a stale window
// yields Action refresh with the new window + leave time; a matching block
// yields keep.
func TestPlanTravelMovedEventRefreshes(t *testing.T) {
	prefs := travelPrefs("u1")
	ev := locEvent("ev1", travelBase.Add(3*time.Hour), time.Hour, 48.86, 2.35)

	stale := map[string]TravelWindow{"ev1": {
		Start: ev.Start.Add(-90 * time.Minute), // block computed before the event moved
		End:   ev.Start.Add(-time.Hour),
	}}
	plans, err := planTravel(prefs, []domain.Event{ev}, nil, stale, fixedTravel(30*time.Minute))
	if err != nil || len(plans) != 1 {
		t.Fatalf("plans = %d (%v), want 1", len(plans), err)
	}
	if plans[0].Action != BlockRefresh {
		t.Fatalf("Action = %q, want refresh", plans[0].Action)
	}
	if !plans[0].Start.Equal(ev.Start.Add(-30*time.Minute)) || !plans[0].LeaveAt.Equal(ev.Start.Add(-35*time.Minute)) {
		t.Fatalf("refreshed plan = %+v, want recomputed window + leave time", plans[0])
	}

	current := map[string]TravelWindow{"ev1": {Start: ev.Start.Add(-30 * time.Minute), End: ev.Start}}
	plans, err = planTravel(prefs, []domain.Event{ev}, nil, current, fixedTravel(30*time.Minute))
	if err != nil || len(plans) != 1 || plans[0].Action != BlockKeep {
		t.Fatalf("plans = %+v (%v), want one keep", plans, err)
	}
}

// TestPlanTravelNoHomeCoordinates: without a home base the planner refuses
// to guess and plans nothing.
func TestPlanTravelNoHomeCoordinates(t *testing.T) {
	prefs := travelPrefs("u1")
	prefs.HomeLat, prefs.HomeLon = nil, nil
	ev := locEvent("ev1", travelBase.Add(2*time.Hour), time.Hour, 48.86, 2.35)
	plans, err := planTravel(prefs, []domain.Event{ev}, nil, nil, fixedTravel(time.Hour))
	if err != nil || plans != nil {
		t.Fatalf("plans = %v (%v), want none without home coordinates", plans, err)
	}
}

// TestPlanTravelModePassthrough: the user's travel mode reaches the vendor
// call (the transit≈driving×1.5 approximation lives in the OSRM adapter).
func TestPlanTravelModePassthrough(t *testing.T) {
	prefs := travelPrefs("u1")
	prefs.TravelMode = domain.TravelTransit
	ev := locEvent("ev1", travelBase.Add(2*time.Hour), time.Hour, 48.86, 2.35)
	var gotMode domain.TravelMode
	if _, err := planTravel(prefs, []domain.Event{ev}, nil, nil,
		func(_, _, _, _ float64, mode domain.TravelMode) (time.Duration, error) {
			gotMode = mode
			return 30 * time.Minute, nil
		}); err != nil {
		t.Fatalf("planTravel: %v", err)
	}
	if gotMode != domain.TravelTransit {
		t.Fatalf("mode = %q, want transit", gotMode)
	}
}

// --- travel pass inside RunAutomation ----------------------------------------

// travelAutomationUser seeds a user whose automation prefs enable travel
// buffers + leave alerts (home in Amsterdam) with a primary writable
// calendar; returns that calendar.
func travelAutomationUser(t *testing.T, f *automationFixture, userID string) domain.Calendar {
	t.Helper()
	return f.addUser(t, userID, func(p *domain.CalendarPrefs) { *p = travelPrefs(userID) })
}

func seedTravelEventOn(t *testing.T, f *automationFixture, calID, id string, start time.Time) domain.Event {
	t.Helper()
	ev := locEvent(id, start, time.Hour, 48.86, 2.35)
	ev.CalendarID = calID
	if _, err := f.events.Upsert(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	return ev
}

func travelTags(t *testing.T, f *automationFixture, userID string) []domain.ManagedEvent {
	t.Helper()
	out, err := f.managed.ListByUser(context.Background(), userID, domain.ManagedTravel)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestAutomationTravelCreatesManagedBlock: the travel pass creates a real
// "Travel to <location>" event through the write-through path, tags it in
// managed_events (kind travel, SourceEventID = the meeting), and arms the
// leave alert — all inside RunAutomation.
func TestAutomationTravelCreatesManagedBlock(t *testing.T) {
	maps := &fakeTravelMaps{travel: 30 * time.Minute}
	f := newAutomationFixtureWith(maps)
	cal := travelAutomationUser(t, f, "u1")
	ev := seedTravelEventOn(t, f, cal.ID, "ev1", travelBase.Add(3*time.Hour))

	if err := f.svc.RunAutomation(context.Background()); err != nil {
		t.Fatalf("RunAutomation: %v", err)
	}
	tags := travelTags(t, f, "u1")
	if len(tags) != 1 {
		t.Fatalf("travel tags = %d, want 1", len(tags))
	}
	if tags[0].SourceEventID == nil || *tags[0].SourceEventID != "ev1" {
		t.Fatalf("tag = %+v, want SourceEventID ev1", tags[0])
	}
	block, err := f.events.GetByID(context.Background(), tags[0].EventID)
	if err != nil {
		t.Fatalf("block mirror: %v", err)
	}
	if block.Title != "Travel to Location ev1" {
		t.Fatalf("block title = %q", block.Title)
	}
	if !block.Start.Equal(ev.Start.Add(-30*time.Minute)) || !block.End.Equal(ev.Start) {
		t.Fatalf("block window = [%v, %v), want [start-30m, start)", block.Start, block.End)
	}
	if block.CalendarID != cal.ID {
		t.Fatalf("block calendar = %q, want primary writable %q", block.CalendarID, cal.ID)
	}
	a, ok := f.alerts.byEvent["ev1"]
	if !ok || a.UserID != "u1" || !a.LeaveAt.Equal(ev.Start.Add(-35*time.Minute)) {
		t.Fatalf("alert = %+v (ok=%v), want u1 @ start-35m", a, ok)
	}
}

// TestAutomationTravelProviderFirst: a failed provider write leaves NOTHING
// local — no mirrored block, no managed tag — while the leave alert (which
// never depended on blocks) still arms; the next healthy pass creates it.
func TestAutomationTravelProviderFirst(t *testing.T) {
	maps := &fakeTravelMaps{travel: 30 * time.Minute}
	f := newAutomationFixtureWith(maps)
	cal := travelAutomationUser(t, f, "u1")
	seedTravelEventOn(t, f, cal.ID, "ev1", travelBase.Add(3*time.Hour))
	f.calSvc.failUsers["u1"] = true

	if err := f.svc.RunAutomation(context.Background()); err != nil {
		t.Fatalf("RunAutomation: %v (per-user failures must be swallowed)", err)
	}
	if tags := travelTags(t, f, "u1"); len(tags) != 0 {
		t.Fatalf("tags = %+v, want none after provider failure", tags)
	}
	if len(f.events.byID) != 1 {
		t.Fatalf("events = %d, want only the seeded meeting (nothing local on failure)", len(f.events.byID))
	}
	if _, ok := f.alerts.byEvent["ev1"]; !ok {
		t.Fatal("leave alert must still arm when the block write fails")
	}

	// Provider recovers: the block appears on the next pass.
	f.calSvc.failUsers["u1"] = false
	if err := f.svc.RunAutomation(context.Background()); err != nil {
		t.Fatalf("RunAutomation (recovered): %v", err)
	}
	if tags := travelTags(t, f, "u1"); len(tags) != 1 {
		t.Fatalf("tags = %d, want 1 after recovery", len(tags))
	}
}

// TestAutomationTravelIdempotentRerun: re-running the pass with nothing
// changed writes nothing and never clears a delivered alert.
func TestAutomationTravelIdempotentRerun(t *testing.T) {
	maps := &fakeTravelMaps{travel: 30 * time.Minute}
	f := newAutomationFixtureWith(maps)
	cal := travelAutomationUser(t, f, "u1")
	seedTravelEventOn(t, f, cal.ID, "ev1", travelBase.Add(3*time.Hour))
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if err := f.svc.RunAutomation(ctx); err != nil {
			t.Fatalf("RunAutomation #%d: %v", i+1, err)
		}
	}
	if f.calSvc.created != 1 {
		t.Fatalf("created = %d, want exactly 1 (re-run must be a no-op)", f.calSvc.created)
	}
	if len(f.calSvc.updatedIDs) != 0 || len(f.calSvc.deletedIDs) != 0 {
		t.Fatalf("updates=%v deletes=%v, want none on idempotent re-run", f.calSvc.updatedIDs, f.calSvc.deletedIDs)
	}
	// A delivered alert stays delivered across idempotent passes.
	if err := f.alerts.MarkSent(ctx, "ev1", travelBase.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RunAutomation(ctx); err != nil {
		t.Fatalf("RunAutomation (post-delivery): %v", err)
	}
	if a := f.alerts.byEvent["ev1"]; a.SentAt == nil {
		t.Fatal("idempotent pass cleared sent_at (would re-send)")
	}
}

// TestAutomationTravelMovedEventMovesBlock: when the meeting moves, the
// engine MOVES its own block (same managed event id — no create/delete
// churn) and re-arms the alert at the new leave time.
func TestAutomationTravelMovedEventMovesBlock(t *testing.T) {
	maps := &fakeTravelMaps{travel: 30 * time.Minute}
	f := newAutomationFixtureWith(maps)
	cal := travelAutomationUser(t, f, "u1")
	ev := seedTravelEventOn(t, f, cal.ID, "ev1", travelBase.Add(3*time.Hour))
	ctx := context.Background()

	if err := f.svc.RunAutomation(ctx); err != nil {
		t.Fatalf("RunAutomation: %v", err)
	}
	blockID := travelTags(t, f, "u1")[0].EventID
	if err := f.alerts.MarkSent(ctx, "ev1", travelBase.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	ev.Start = ev.Start.Add(time.Hour)
	ev.End = ev.End.Add(time.Hour)
	if _, err := f.events.Upsert(ctx, ev); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RunAutomation(ctx); err != nil {
		t.Fatalf("RunAutomation (moved): %v", err)
	}

	tags := travelTags(t, f, "u1")
	if len(tags) != 1 || tags[0].EventID != blockID {
		t.Fatalf("tags = %+v, want the SAME block %s", tags, blockID)
	}
	block, err := f.events.GetByID(ctx, blockID)
	if err != nil {
		t.Fatal(err)
	}
	if !block.Start.Equal(ev.Start.Add(-30*time.Minute)) || !block.End.Equal(ev.Start) {
		t.Fatalf("block window = [%v, %v), want [newStart-30m, newStart)", block.Start, block.End)
	}
	if f.calSvc.created != 1 || len(f.calSvc.deletedIDs) != 0 {
		t.Fatalf("created=%d deleted=%v, want a move, not churn", f.calSvc.created, f.calSvc.deletedIDs)
	}
	a := f.alerts.byEvent["ev1"]
	if a.SentAt != nil || !a.LeaveAt.Equal(ev.Start.Add(-35*time.Minute)) {
		t.Fatalf("alert = %+v, want re-armed at newStart-35m", a)
	}
}

// TestAutomationTravelCoordsClearedDeletesBlock: an event whose coordinates
// were cleared (location edited without a fresh autocomplete pick) loses its
// travel block and pending alert; the user's own event is never touched.
func TestAutomationTravelCoordsClearedDeletesBlock(t *testing.T) {
	maps := &fakeTravelMaps{travel: 30 * time.Minute}
	f := newAutomationFixtureWith(maps)
	cal := travelAutomationUser(t, f, "u1")
	ev := seedTravelEventOn(t, f, cal.ID, "ev1", travelBase.Add(3*time.Hour))
	ctx := context.Background()

	if err := f.svc.RunAutomation(ctx); err != nil {
		t.Fatalf("RunAutomation: %v", err)
	}
	blockID := travelTags(t, f, "u1")[0].EventID

	ev.LocationLat, ev.LocationLon = nil, nil
	if _, err := f.events.Upsert(ctx, ev); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RunAutomation(ctx); err != nil {
		t.Fatalf("RunAutomation (coords cleared): %v", err)
	}

	if _, err := f.events.GetByID(ctx, blockID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("block still mirrored (err=%v), want deleted", err)
	}
	if tags := travelTags(t, f, "u1"); len(tags) != 0 {
		t.Fatalf("tags = %+v, want none", tags)
	}
	if _, ok := f.alerts.byEvent["ev1"]; ok {
		t.Fatal("pending alert must be deleted with its block")
	}
	// The engine only ever deleted its OWN block; the user's event survives.
	if len(f.calSvc.deletedIDs) != 1 || f.calSvc.deletedIDs[0] != blockID {
		t.Fatalf("deletedIDs = %v, want exactly [%s]", f.calSvc.deletedIDs, blockID)
	}
	if got, err := f.events.GetByID(ctx, "ev1"); err != nil || got.Title != "Meeting ev1" {
		t.Fatalf("user event touched: %+v (%v)", got, err)
	}
}

// TestAutomationTravelRespectsToggles: TravelBuffers off never calls the
// vendor; LeaveAlerts off writes blocks but never alerts.
func TestAutomationTravelRespectsToggles(t *testing.T) {
	maps := &fakeTravelMaps{travel: 30 * time.Minute}
	f := newAutomationFixtureWith(maps)
	cal := f.addUser(t, "u1", func(p *domain.CalendarPrefs) {
		*p = travelPrefs("u1")
		p.TravelBuffers = false // LeaveAlerts alone keeps the user listed
	})
	seedTravelEventOn(t, f, cal.ID, "ev1", travelBase.Add(3*time.Hour))
	ctx := context.Background()

	if err := f.svc.RunAutomation(ctx); err != nil {
		t.Fatalf("RunAutomation: %v", err)
	}
	if len(maps.calls) != 0 || len(f.alerts.byEvent) != 0 || f.calSvc.created != 0 {
		t.Fatalf("TravelBuffers off wrote: calls=%d alerts=%d created=%d", len(maps.calls), len(f.alerts.byEvent), f.calSvc.created)
	}

	p := travelPrefs("u1")
	p.LeaveAlerts = false
	if err := f.prefs.Upsert(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RunAutomation(ctx); err != nil {
		t.Fatalf("RunAutomation (alerts off): %v", err)
	}
	if f.calSvc.created != 1 {
		t.Fatalf("created = %d, want the travel block with alerts off", f.calSvc.created)
	}
	if len(f.alerts.byEvent) != 0 {
		t.Fatalf("alerts = %+v, want none with LeaveAlerts off", f.alerts.byEvent)
	}
}

// TestAutomationTravelPerUserFailureContinues: one user's vendor outage is
// logged; the other users' passes still run.
func TestAutomationTravelPerUserFailureContinues(t *testing.T) {
	maps := &fakeTravelMaps{}
	maps.fn = func(_, _, toLat, _ float64, _ domain.TravelMode) (time.Duration, error) {
		if toLat == 99.0 { // u1's event routes to the poisoned coordinate
			return 0, errors.New("osrm 500")
		}
		return 30 * time.Minute, nil
	}
	f := newAutomationFixtureWith(maps)
	cal1 := travelAutomationUser(t, f, "u1")
	cal2 := travelAutomationUser(t, f, "u2")
	bad := locEvent("ev-bad", travelBase.Add(2*time.Hour), time.Hour, 99.0, 2.35)
	bad.CalendarID = cal1.ID
	if _, err := f.events.Upsert(context.Background(), bad); err != nil {
		t.Fatal(err)
	}
	seedTravelEventOn(t, f, cal2.ID, "ev-ok", travelBase.Add(3*time.Hour))

	if err := f.svc.RunAutomation(context.Background()); err != nil {
		t.Fatalf("RunAutomation: %v (user failures are logged, not fatal)", err)
	}
	if _, ok := f.alerts.byEvent["ev-ok"]; !ok {
		t.Fatal("u2's alert missing: one user's failure stalled the fleet")
	}
	if _, ok := f.alerts.byEvent["ev-bad"]; ok {
		t.Fatal("u1's failed event must not get an alert")
	}
}

// TestAutomationTravelCrossTenantIsolation: blocks and alerts carry the
// owning user; users never plan over each other's events.
func TestAutomationTravelCrossTenantIsolation(t *testing.T) {
	maps := &fakeTravelMaps{travel: 30 * time.Minute}
	f := newAutomationFixtureWith(maps)
	cal1 := travelAutomationUser(t, f, "u1")
	cal2 := travelAutomationUser(t, f, "u2")
	seedTravelEventOn(t, f, cal1.ID, "ev-u1", travelBase.Add(3*time.Hour))
	seedTravelEventOn(t, f, cal2.ID, "ev-u2", travelBase.Add(4*time.Hour))

	if err := f.svc.RunAutomation(context.Background()); err != nil {
		t.Fatalf("RunAutomation: %v", err)
	}
	if a := f.alerts.byEvent["ev-u1"]; a.UserID != "u1" {
		t.Fatalf("ev-u1 alert user = %q, want u1", a.UserID)
	}
	if a := f.alerts.byEvent["ev-u2"]; a.UserID != "u2" {
		t.Fatalf("ev-u2 alert user = %q, want u2", a.UserID)
	}
	if tags := travelTags(t, f, "u1"); len(tags) != 1 {
		t.Fatalf("u1 tags = %d, want 1", len(tags))
	}
	if tags := travelTags(t, f, "u2"); len(tags) != 1 {
		t.Fatalf("u2 tags = %d, want 1", len(tags))
	}
}

// TestAutomationEntitlementSkipsLapsedUser: on a cloud instance a lapsed
// user's whole automation pass (focus AND travel) is skipped silently — no
// vendor calls, no provider writes, no error spam — and resumes on
// resubscribe.
func TestAutomationEntitlementSkipsLapsedUser(t *testing.T) {
	maps := &fakeTravelMaps{travel: 30 * time.Minute}
	prefs := newCalendarPrefsRepo()
	accts := newAccountRepo()
	cals := newCalendarRepo()
	cals.accounts = accts
	events := newEventRepo()
	events.calendars, events.accounts = cals, accts
	managed := newManagedEventRepo()
	alerts := newTravelAlertRepo()
	subs := newSubscriptionRepo()
	calSvc := &automationCalendarStub{events: events, failUsers: map[string]bool{}}
	svc := NewAutomationService(AutomationServiceDeps{
		Prefs: prefs, Accounts: accts, Calendars: cals, Events: events,
		Managed: managed, CalendarSvc: calSvc,
		Subscriptions: subs, SelfHosted: false,
		Maps: maps, Alerts: alerts,
		Clock:  newClock(travelBase),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ctx := context.Background()
	p := travelPrefs("u1")
	p.FocusGoalMinutesPerWeek = 300
	if err := prefs.Upsert(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := accts.Create(ctx, domain.ConnectedAccount{ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "u1@x.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cals.Upsert(ctx, domain.Calendar{ID: "c-u1", AccountID: "a1", ProviderCalendarID: "pc1", IsPrimary: true, IsVisible: true, CanWrite: true}); err != nil {
		t.Fatal(err)
	}
	ev := locEvent("ev1", travelBase.Add(3*time.Hour), time.Hour, 48.86, 2.35)
	ev.CalendarID = "c-u1"
	if _, err := events.Upsert(ctx, ev); err != nil {
		t.Fatal(err)
	}

	if err := svc.RunAutomation(ctx); err != nil {
		t.Fatalf("RunAutomation: %v", err)
	}
	if len(maps.calls) != 0 || len(alerts.byEvent) != 0 || calSvc.created != 0 {
		t.Fatalf("lapsed user wrote: maps=%d alerts=%d created=%d, want all 0", len(maps.calls), len(alerts.byEvent), calSvc.created)
	}

	// Subscribing resumes automation.
	if err := subs.Upsert(ctx, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive}); err != nil {
		t.Fatal(err)
	}
	if err := svc.RunAutomation(ctx); err != nil {
		t.Fatalf("RunAutomation (subscribed): %v", err)
	}
	if _, ok := alerts.byEvent["ev1"]; !ok {
		t.Fatal("alert not armed after resubscribe")
	}
}
