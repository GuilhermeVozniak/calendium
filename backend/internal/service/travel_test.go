package service

// travel_test.go covers M2.8 Task 12: the pure travel planner (planTravel)
// and the TravelService pass orchestration (prefs fan-out, entitlement,
// user-scoped event reads, silent degrade when maps is unconfigured, alert
// arming/re-arming, and the Task 6 managed-blocks seam).

import (
	"context"
	"errors"
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

// fakeTravelBlockStore stands in for the Task 6 managed-events seam.
type fakeTravelBlockStore struct {
	managed   map[string]struct{}
	blocks    map[string]TravelWindow
	ensured   []TravelPlan
	ensureErr error
}

var _ TravelBlockStore = (*fakeTravelBlockStore)(nil)

func (f *fakeTravelBlockStore) ManagedEventIDs(context.Context, string) (map[string]struct{}, error) {
	if f.managed == nil {
		return map[string]struct{}{}, nil
	}
	return f.managed, nil
}

func (f *fakeTravelBlockStore) OwnedTravelBlocks(context.Context, string) (map[string]TravelWindow, error) {
	if f.blocks == nil {
		return map[string]TravelWindow{}, nil
	}
	return f.blocks, nil
}

func (f *fakeTravelBlockStore) EnsureTravelBlock(_ context.Context, _ string, plan TravelPlan) error {
	if f.ensureErr != nil {
		return f.ensureErr
	}
	f.ensured = append(f.ensured, plan)
	return nil
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

// --- TravelService.RunTravelPass ---------------------------------------------

type travelFixture struct {
	svc       *TravelService
	prefs     *fakeCalendarPrefsRepo
	events    *fakeEventRepo
	alerts    *fakeTravelAlertRepo
	maps      *fakeTravelMaps
	clock     *fakeClock
	accounts  *fakeAccountRepo
	calendars *fakeCalendarRepo
}

// newTravelFixture wires a TravelService over user-scoped fakes: events
// resolve event→calendar→account→user like the SQL join, so cross-tenant
// isolation is real in these tests. seedTravelUser adds a user with one
// calendar ("c-<user>").
func newTravelFixture(t *testing.T, maps *fakeTravelMaps, blocks TravelBlockStore) *travelFixture {
	t.Helper()
	accounts := newAccountRepo()
	calendars := newCalendarRepo()
	events := newEventRepo()
	events.calendars = calendars
	events.accounts = accounts
	prefs := newCalendarPrefsRepo()
	alerts := newTravelAlertRepo()
	clock := newClock(travelBase)
	var mapsPort port.MapsProvider
	if maps != nil {
		mapsPort = maps
	}
	svc := NewTravelService(TravelServiceDeps{
		Subscriptions: newSubscriptionRepo(),
		Prefs:         prefs,
		Events:        events,
		Alerts:        alerts,
		Maps:          mapsPort,
		Blocks:        blocks,
		Clock:         clock,
		SelfHosted:    true,
	})
	return &travelFixture{svc, prefs, events, alerts, maps, clock, accounts, calendars}
}

func (f *travelFixture) seedTravelUser(t *testing.T, userID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a-" + userID, UserID: userID, Provider: domain.ProviderGoogle, Email: userID + "@x.com",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.calendars.Upsert(ctx, domain.Calendar{
		ID: "c-" + userID, AccountID: "a-" + userID, ProviderCalendarID: "pc-" + userID, CanWrite: true,
	}); err != nil {
		t.Fatal(err)
	}
	f.prefs.byUser[userID] = travelPrefs(userID)
}

func (f *travelFixture) seedTravelEvent(t *testing.T, userID, id string, start time.Time) domain.Event {
	t.Helper()
	ev := locEvent(id, start, time.Hour, 48.86, 2.35)
	ev.CalendarID = "c-" + userID
	if _, err := f.events.Upsert(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	return ev
}

// TestRunTravelPassArmsAlerts: the happy path upserts one alert per planned
// event at leaveAt = start - travel - 5min, scoped to the owning user.
func TestRunTravelPassArmsAlerts(t *testing.T) {
	maps := &fakeTravelMaps{travel: 30 * time.Minute}
	f := newTravelFixture(t, maps, nil)
	f.seedTravelUser(t, "u1")
	ev := f.seedTravelEvent(t, "u1", "ev1", travelBase.Add(3*time.Hour))

	if err := f.svc.RunTravelPass(context.Background()); err != nil {
		t.Fatalf("RunTravelPass: %v", err)
	}
	a, ok := f.alerts.byEvent["ev1"]
	if !ok {
		t.Fatal("alert not armed")
	}
	if a.UserID != "u1" || !a.LeaveAt.Equal(ev.Start.Add(-35*time.Minute)) || a.SentAt != nil {
		t.Fatalf("alert = %+v, want u1 @ start-35m unsent", a)
	}
}

// TestRunTravelPassUnconfiguredMapsDegradesSilently: no maps provider means
// no vendor calls, no buffers, no alerts, no error.
func TestRunTravelPassUnconfiguredMapsDegradesSilently(t *testing.T) {
	f := newTravelFixture(t, nil, nil)
	f.seedTravelUser(t, "u1")
	f.seedTravelEvent(t, "u1", "ev1", travelBase.Add(3*time.Hour))

	if err := f.svc.RunTravelPass(context.Background()); err != nil {
		t.Fatalf("RunTravelPass: %v", err)
	}
	if len(f.alerts.byEvent) != 0 {
		t.Fatalf("alerts = %+v, want none with maps unconfigured", f.alerts.byEvent)
	}
}

// TestRunTravelPassRespectsToggles: TravelBuffers off ends the user's pass
// before any vendor call; LeaveAlerts off plans (blocks seam) but never
// arms alerts.
func TestRunTravelPassRespectsToggles(t *testing.T) {
	maps := &fakeTravelMaps{travel: 30 * time.Minute}
	f := newTravelFixture(t, maps, nil)
	f.seedTravelUser(t, "u1")
	f.seedTravelEvent(t, "u1", "ev1", travelBase.Add(3*time.Hour))

	p := f.prefs.byUser["u1"]
	p.TravelBuffers = false
	f.prefs.byUser["u1"] = p
	if err := f.svc.RunTravelPass(context.Background()); err != nil {
		t.Fatalf("RunTravelPass: %v", err)
	}
	if len(maps.calls) != 0 || len(f.alerts.byEvent) != 0 {
		t.Fatalf("calls=%d alerts=%d, want 0/0 with TravelBuffers off", len(maps.calls), len(f.alerts.byEvent))
	}

	p.TravelBuffers, p.LeaveAlerts = true, false
	f.prefs.byUser["u1"] = p
	if err := f.svc.RunTravelPass(context.Background()); err != nil {
		t.Fatalf("RunTravelPass: %v", err)
	}
	if len(maps.calls) == 0 {
		t.Fatal("expected planning vendor calls with TravelBuffers on")
	}
	if len(f.alerts.byEvent) != 0 {
		t.Fatalf("alerts = %+v, want none with LeaveAlerts off", f.alerts.byEvent)
	}
}

// TestRunTravelPassEntitlement: on a cloud instance an unsubscribed user is
// skipped silently — no vendor calls, no alerts, no error.
func TestRunTravelPassEntitlement(t *testing.T) {
	maps := &fakeTravelMaps{travel: 30 * time.Minute}
	accounts := newAccountRepo()
	calendars := newCalendarRepo()
	events := newEventRepo()
	events.calendars, events.accounts = calendars, accounts
	prefs := newCalendarPrefsRepo()
	alerts := newTravelAlertRepo()
	subs := newSubscriptionRepo()
	svc := NewTravelService(TravelServiceDeps{
		Subscriptions: subs, Prefs: prefs, Events: events, Alerts: alerts,
		Maps: maps, Clock: newClock(travelBase), SelfHosted: false,
	})
	ctx := context.Background()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle}); err != nil {
		t.Fatal(err)
	}
	if _, err := calendars.Upsert(ctx, domain.Calendar{ID: "c-u1", AccountID: "a1", ProviderCalendarID: "pc1"}); err != nil {
		t.Fatal(err)
	}
	prefs.byUser["u1"] = travelPrefs("u1")
	ev := locEvent("ev1", travelBase.Add(3*time.Hour), time.Hour, 48.86, 2.35)
	ev.CalendarID = "c-u1"
	if _, err := events.Upsert(ctx, ev); err != nil {
		t.Fatal(err)
	}

	if err := svc.RunTravelPass(ctx); err != nil {
		t.Fatalf("RunTravelPass: %v", err)
	}
	if len(maps.calls) != 0 || len(alerts.byEvent) != 0 {
		t.Fatalf("calls=%d alerts=%d, want 0/0 for unentitled user", len(maps.calls), len(alerts.byEvent))
	}

	// Subscribing unlocks the pass.
	if err := subs.Upsert(ctx, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive}); err != nil {
		t.Fatal(err)
	}
	if err := svc.RunTravelPass(ctx); err != nil {
		t.Fatalf("RunTravelPass (subscribed): %v", err)
	}
	if _, ok := alerts.byEvent["ev1"]; !ok {
		t.Fatal("alert not armed for subscribed user")
	}
}

// TestRunTravelPassPerUserFailureContinues: one user's vendor failure is
// reported but never stalls the other users' passes.
func TestRunTravelPassPerUserFailureContinues(t *testing.T) {
	maps := &fakeTravelMaps{}
	maps.fn = func(_, _, toLat, _ float64, _ domain.TravelMode) (time.Duration, error) {
		if toLat == 99.0 { // u1's event routes to the poisoned coordinate
			return 0, errors.New("osrm 500")
		}
		return 30 * time.Minute, nil
	}
	f := newTravelFixture(t, maps, nil)
	f.seedTravelUser(t, "u1")
	f.seedTravelUser(t, "u2")
	bad := locEvent("ev-bad", travelBase.Add(2*time.Hour), time.Hour, 99.0, 2.35)
	bad.CalendarID = "c-u1"
	if _, err := f.events.Upsert(context.Background(), bad); err != nil {
		t.Fatal(err)
	}
	f.seedTravelEvent(t, "u2", "ev-ok", travelBase.Add(3*time.Hour))

	err := f.svc.RunTravelPass(context.Background())
	if err == nil {
		t.Fatal("want joined error for u1's vendor failure")
	}
	if _, ok := f.alerts.byEvent["ev-ok"]; !ok {
		t.Fatal("u2's alert missing: one user's failure stalled the fleet")
	}
	if _, ok := f.alerts.byEvent["ev-bad"]; ok {
		t.Fatal("u1's failed event must not get an alert")
	}
}

// TestRunTravelPassCrossTenantIsolation: users only ever plan over their own
// events; alerts carry the owning user id.
func TestRunTravelPassCrossTenantIsolation(t *testing.T) {
	maps := &fakeTravelMaps{travel: 30 * time.Minute}
	f := newTravelFixture(t, maps, nil)
	f.seedTravelUser(t, "u1")
	f.seedTravelUser(t, "u2")
	f.seedTravelEvent(t, "u1", "ev-u1", travelBase.Add(3*time.Hour))
	f.seedTravelEvent(t, "u2", "ev-u2", travelBase.Add(4*time.Hour))

	if err := f.svc.RunTravelPass(context.Background()); err != nil {
		t.Fatalf("RunTravelPass: %v", err)
	}
	if a := f.alerts.byEvent["ev-u1"]; a.UserID != "u1" {
		t.Fatalf("ev-u1 alert user = %q, want u1", a.UserID)
	}
	if a := f.alerts.byEvent["ev-u2"]; a.UserID != "u2" {
		t.Fatalf("ev-u2 alert user = %q, want u2", a.UserID)
	}
}

// TestRunTravelPassMovedEventReArmsAlert: a delivered alert whose event
// moves gets a fresh leave time with sent_at cleared; an unmoved event's
// delivered alert stays delivered (no re-send).
func TestRunTravelPassMovedEventReArmsAlert(t *testing.T) {
	maps := &fakeTravelMaps{travel: 30 * time.Minute}
	f := newTravelFixture(t, maps, nil)
	f.seedTravelUser(t, "u1")
	ev := f.seedTravelEvent(t, "u1", "ev1", travelBase.Add(3*time.Hour))
	ctx := context.Background()

	if err := f.svc.RunTravelPass(ctx); err != nil {
		t.Fatalf("RunTravelPass: %v", err)
	}
	sentAt := travelBase.Add(time.Minute)
	if err := f.alerts.MarkSent(ctx, "ev1", sentAt); err != nil {
		t.Fatal(err)
	}

	// Idempotent pass: unchanged leave time keeps the delivery stamp.
	if err := f.svc.RunTravelPass(ctx); err != nil {
		t.Fatalf("RunTravelPass (idempotent): %v", err)
	}
	if a := f.alerts.byEvent["ev1"]; a.SentAt == nil {
		t.Fatal("idempotent pass cleared sent_at (would re-send)")
	}

	// Event moves an hour later: leave time changes, alert re-arms.
	ev.Start = ev.Start.Add(time.Hour)
	ev.End = ev.End.Add(time.Hour)
	if _, err := f.events.Upsert(ctx, ev); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RunTravelPass(ctx); err != nil {
		t.Fatalf("RunTravelPass (moved): %v", err)
	}
	a := f.alerts.byEvent["ev1"]
	if a.SentAt != nil {
		t.Fatal("moved event must re-arm its alert (sent_at cleared)")
	}
	if !a.LeaveAt.Equal(ev.Start.Add(-35 * time.Minute)) {
		t.Fatalf("LeaveAt = %v, want new start-35m", a.LeaveAt)
	}
}

// TestRunTravelPassBlocksSeam: with the Task 6 seam wired, create/refresh
// plans reach EnsureTravelBlock and keep plans do not.
func TestRunTravelPassBlocksSeam(t *testing.T) {
	maps := &fakeTravelMaps{travel: 30 * time.Minute}
	blocks := &fakeTravelBlockStore{}
	f := newTravelFixture(t, maps, blocks)
	f.seedTravelUser(t, "u1")
	evNew := f.seedTravelEvent(t, "u1", "ev-new", travelBase.Add(3*time.Hour))
	evKept := f.seedTravelEvent(t, "u1", "ev-kept", travelBase.Add(6*time.Hour))
	blocks.blocks = map[string]TravelWindow{
		"ev-kept": {Start: evKept.Start.Add(-30 * time.Minute), End: evKept.Start},
	}

	if err := f.svc.RunTravelPass(context.Background()); err != nil {
		t.Fatalf("RunTravelPass: %v", err)
	}
	if len(blocks.ensured) != 1 {
		t.Fatalf("ensured = %+v, want only the new block", blocks.ensured)
	}
	got := blocks.ensured[0]
	if got.EventID != "ev-new" || got.Action != BlockCreate {
		t.Fatalf("ensured plan = %+v, want ev-new create", got)
	}
	if !got.Start.Equal(evNew.Start.Add(-30*time.Minute)) || !got.End.Equal(evNew.Start) {
		t.Fatalf("ensured window = [%v, %v)", got.Start, got.End)
	}
}
