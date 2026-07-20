package service

// travel_alerts_test.go covers M2.8 Task 12 leave-alert delivery inside
// SyncService.ProcessDueWork: payload shape, honesty policy (sent_at only
// after an observed push success), silent degrade when unconfigured, stale
// alert cleanup, and cross-tenant device routing.

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

type leaveAlertFixture struct {
	svc     *SyncService
	alerts  *fakeTravelAlertRepo
	events  *fakeEventRepo
	devices *fakeDeviceRepo
	push    *fakePush
	prefs   *fakeCalendarPrefsRepo
	clock   *fakeClock
}

func newLeaveAlertFixture(t *testing.T) *leaveAlertFixture {
	t.Helper()
	accounts := newAccountRepo()
	f := &leaveAlertFixture{
		alerts:  newTravelAlertRepo(),
		events:  newEventRepo(),
		devices: newDeviceRepo(),
		push:    newPush(),
		prefs:   newCalendarPrefsRepo(),
		clock:   newClock(travelBase),
	}
	f.svc = NewSyncService(SyncServiceDeps{
		Accounts:      accounts,
		Threads:       newThreadRepo(),
		Drafts:        newDraftRepo(accounts),
		Events:        f.events,
		Devices:       f.devices,
		Push:          f.push,
		TravelAlerts:  f.alerts,
		CalendarPrefs: f.prefs,
		Clock:         f.clock,
	})
	return f
}

func (f *leaveAlertFixture) seedDevice(t *testing.T, userID, token string) {
	t.Helper()
	if _, err := f.devices.Upsert(context.Background(), domain.NotificationDevice{
		UserID: userID, Platform: domain.PlatformIOS, Token: token,
	}); err != nil {
		t.Fatal(err)
	}
}

// seedDueAlert stores an event starting soon plus its already-due alert.
// The owner gets travel-enabled prefs unless the test already seeded some —
// delivery re-checks prefs, and an armed alert implies they were on.
func (f *leaveAlertFixture) seedDueAlert(t *testing.T, userID, eventID string) domain.Event {
	t.Helper()
	ctx := context.Background()
	if _, ok := f.prefs.byUser[userID]; !ok {
		f.prefs.byUser[userID] = travelPrefs(userID)
	}
	ev := locEvent(eventID, f.clock.Now().Add(40*time.Minute), time.Hour, 48.86, 2.35)
	ev.Title = "Standup " + eventID
	if _, err := f.events.Upsert(ctx, ev); err != nil {
		t.Fatal(err)
	}
	if err := f.alerts.Upsert(ctx, eventID, userID, f.clock.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	return ev
}

// TestLeaveAlertDelivery: a due alert pushes to every device of its owner
// with the brief's payload, stamps sent_at, and a second pass sends nothing.
func TestLeaveAlertDelivery(t *testing.T) {
	f := newLeaveAlertFixture(t)
	ctx := context.Background()
	f.seedDevice(t, "u1", "tok-1")
	f.seedDevice(t, "u1", "tok-2")
	tz := "Europe/Amsterdam"
	p := travelPrefs("u1")
	p.TimeZone = tz
	f.prefs.byUser["u1"] = p
	ev := f.seedDueAlert(t, "u1", "ev1")

	if err := f.svc.ProcessDueWork(ctx); err != nil {
		t.Fatalf("ProcessDueWork: %v", err)
	}
	if len(f.push.sent) != 2 {
		t.Fatalf("pushes = %d, want one per device", len(f.push.sent))
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		t.Fatal(err)
	}
	wantBody := "Leave now to make Standup ev1 at " + ev.Start.In(loc).Format("15:04")
	for _, s := range f.push.sent {
		if s.Title != "Time to leave" {
			t.Fatalf("title = %q", s.Title)
		}
		if s.Body != wantBody {
			t.Fatalf("body = %q, want %q", s.Body, wantBody)
		}
		if s.Data["type"] != "leave_alert" || s.Data["eventId"] != "ev1" {
			t.Fatalf("data = %v", s.Data)
		}
	}
	if a := f.alerts.byEvent["ev1"]; a.SentAt == nil {
		t.Fatal("sent_at not stamped after successful pushes")
	}

	// Honesty in the other direction too: delivered means delivered once.
	if err := f.svc.ProcessDueWork(ctx); err != nil {
		t.Fatalf("ProcessDueWork (second): %v", err)
	}
	if len(f.push.sent) != 2 {
		t.Fatalf("pushes after second pass = %d, want still 2", len(f.push.sent))
	}
}

// TestLeaveAlertPushFailureLeavesUnsent: a total push failure must NOT stamp
// sent_at (the alert stays due for the next pass — honesty policy).
func TestLeaveAlertPushFailureLeavesUnsent(t *testing.T) {
	f := newLeaveAlertFixture(t)
	f.seedDevice(t, "u1", "tok-1")
	f.seedDueAlert(t, "u1", "ev1")
	f.push.err = errors.New("apns down")

	if err := f.svc.ProcessDueWork(context.Background()); err == nil {
		t.Fatal("want push failure reported")
	}
	if a := f.alerts.byEvent["ev1"]; a.SentAt != nil {
		t.Fatal("sent_at stamped despite total push failure")
	}

	// Sender recovers: the very next pass delivers and stamps.
	f.push.err = nil
	if err := f.svc.ProcessDueWork(context.Background()); err != nil {
		t.Fatalf("ProcessDueWork (recovered): %v", err)
	}
	if a := f.alerts.byEvent["ev1"]; a.SentAt == nil {
		t.Fatal("sent_at not stamped after recovery")
	}
}

// TestLeaveAlertPrefsOptOutDeletes: alerts armed while travel automation
// was on must NOT fire after the user opts out — delivery re-checks prefs
// and deletes the alert (either toggle off silences it). A prefs-load
// failure, by contrast, retries instead of deleting.
func TestLeaveAlertPrefsOptOutDeletes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*domain.CalendarPrefs)
	}{
		{"leave alerts off", func(p *domain.CalendarPrefs) { p.LeaveAlerts = false }},
		{"travel buffers off", func(p *domain.CalendarPrefs) { p.TravelBuffers = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLeaveAlertFixture(t)
			f.seedDevice(t, "u1", "tok-1")
			f.seedDueAlert(t, "u1", "ev1")
			p := f.prefs.byUser["u1"]
			tc.mutate(&p)
			f.prefs.byUser["u1"] = p

			if err := f.svc.ProcessDueWork(context.Background()); err != nil {
				t.Fatalf("ProcessDueWork: %v", err)
			}
			if len(f.push.sent) != 0 {
				t.Fatalf("pushes = %d, want 0 after opt-out", len(f.push.sent))
			}
			if _, ok := f.alerts.byEvent["ev1"]; ok {
				t.Fatal("opted-out alert must be deleted, not left to retry")
			}
		})
	}
}

// TestLeaveAlertNoDevicesMarksDone: nothing to deliver — the alert is marked
// handled instead of retrying every 5 seconds forever.
func TestLeaveAlertNoDevicesMarksDone(t *testing.T) {
	f := newLeaveAlertFixture(t)
	f.seedDueAlert(t, "u1", "ev1")

	if err := f.svc.ProcessDueWork(context.Background()); err != nil {
		t.Fatalf("ProcessDueWork: %v", err)
	}
	if len(f.push.sent) != 0 {
		t.Fatalf("pushes = %d, want 0", len(f.push.sent))
	}
	if a := f.alerts.byEvent["ev1"]; a.SentAt == nil {
		t.Fatal("deviceless alert must be marked done (no 5s retry storm)")
	}
}

// TestLeaveAlertUnconfiguredDegradesSilently: without a TravelAlertRepo or
// without push, ProcessDueWork ignores travel alerts entirely.
func TestLeaveAlertUnconfiguredDegradesSilently(t *testing.T) {
	// No push sender wired: the due alert stays untouched.
	accounts := newAccountRepo()
	alerts := newTravelAlertRepo()
	events := newEventRepo()
	clock := newClock(travelBase)
	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts, Threads: newThreadRepo(), Drafts: newDraftRepo(accounts),
		Events: events, Devices: newDeviceRepo(), TravelAlerts: alerts, Clock: clock,
	})
	if _, err := events.Upsert(context.Background(), locEvent("ev1", travelBase.Add(time.Hour), time.Hour, 1, 2)); err != nil {
		t.Fatal(err)
	}
	if err := alerts.Upsert(context.Background(), "ev1", "u1", travelBase.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := svc.ProcessDueWork(context.Background()); err != nil {
		t.Fatalf("ProcessDueWork: %v", err)
	}
	if a := alerts.byEvent["ev1"]; a.SentAt != nil {
		t.Fatal("alert touched with push unconfigured")
	}

	// No travel-alert repo wired at all: same silence (nil-safe).
	svc2 := NewSyncService(SyncServiceDeps{
		Accounts: accounts, Threads: newThreadRepo(), Drafts: newDraftRepo(accounts),
		Events: events, Devices: newDeviceRepo(), Push: newPush(), Clock: clock,
	})
	if err := svc2.ProcessDueWork(context.Background()); err != nil {
		t.Fatalf("ProcessDueWork (no repo): %v", err)
	}
}

// TestLeaveAlertStaleCleanup: alerts whose event vanished, already started,
// or lost its coordinates (location edited without a fresh pick — the Task
// 11 stale-coords carry-forward) are deleted, never pushed.
func TestLeaveAlertStaleCleanup(t *testing.T) {
	f := newLeaveAlertFixture(t)
	ctx := context.Background()
	f.seedDevice(t, "u1", "tok-1")

	// Event deleted after the alert was armed.
	if err := f.alerts.Upsert(ctx, "ev-gone", "u1", f.clock.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Event already started.
	started := locEvent("ev-started", f.clock.Now().Add(-10*time.Minute), time.Hour, 48.86, 2.35)
	if _, err := f.events.Upsert(ctx, started); err != nil {
		t.Fatal(err)
	}
	if err := f.alerts.Upsert(ctx, "ev-started", "u1", f.clock.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Coordinates cleared by a location edit without a new autocomplete pick:
	// pushing would confidently route to the OLD location — drop instead.
	nogeo := locEvent("ev-nogeo", f.clock.Now().Add(40*time.Minute), time.Hour, 48.86, 2.35)
	nogeo.LocationLat, nogeo.LocationLon = nil, nil
	if _, err := f.events.Upsert(ctx, nogeo); err != nil {
		t.Fatal(err)
	}
	if err := f.alerts.Upsert(ctx, "ev-nogeo", "u1", f.clock.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	if err := f.svc.ProcessDueWork(ctx); err != nil {
		t.Fatalf("ProcessDueWork: %v", err)
	}
	if len(f.push.sent) != 0 {
		t.Fatalf("pushes = %d, want 0 for stale alerts", len(f.push.sent))
	}
	for _, id := range []string{"ev-gone", "ev-started", "ev-nogeo"} {
		if _, ok := f.alerts.byEvent[id]; ok {
			t.Fatalf("stale alert %s not cleaned up", id)
		}
	}
}

// TestLeaveAlertCrossTenantRouting: an alert pushes only to its owner's
// devices, never to another tenant's.
func TestLeaveAlertCrossTenantRouting(t *testing.T) {
	f := newLeaveAlertFixture(t)
	f.seedDevice(t, "u1", "tok-u1")
	f.seedDevice(t, "u2", "tok-u2")
	f.seedDueAlert(t, "u2", "ev-u2")

	if err := f.svc.ProcessDueWork(context.Background()); err != nil {
		t.Fatalf("ProcessDueWork: %v", err)
	}
	if len(f.push.sent) != 1 {
		t.Fatalf("pushes = %d, want 1", len(f.push.sent))
	}
	if got := f.push.sent[0].Device; got.UserID != "u2" || got.Token != "tok-u2" {
		t.Fatalf("pushed to %+v, want u2's device only", got)
	}
}
