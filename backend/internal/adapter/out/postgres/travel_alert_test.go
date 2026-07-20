package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// --- M2.8 Task 12: travel_alerts repo ----------------------------------------

func seedGeoEvent(t *testing.T, st *Store, calID, providerID string, start time.Time) domain.Event {
	t.Helper()
	lat, lon := 52.52, 13.405
	ev, err := st.Events().Upsert(context.Background(), domain.Event{
		CalendarID: calID, ProviderEventID: providerID, Title: "Offsite",
		Start: start, End: start.Add(time.Hour),
		LocationLat: &lat, LocationLon: &lon,
	})
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}
	return ev
}

func TestTravelAlertUpsertRefreshSemantics(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	cal := seedCalendar(t, st, acct.ID)
	now := time.Now().UTC().Truncate(time.Microsecond)
	ev := seedGeoEvent(t, st, cal.ID, "pe-1", now.Add(2*time.Hour))

	leaveAt := now.Add(time.Hour)
	if err := st.TravelAlerts().Upsert(ctx, ev.ID, "u1", leaveAt); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.TravelAlerts().MarkSent(ctx, ev.ID, now); err != nil {
		t.Fatalf("mark sent: %v", err)
	}

	// Same leave_at: sent_at survives (idempotent pass never re-sends).
	if err := st.TravelAlerts().Upsert(ctx, ev.ID, "u1", leaveAt); err != nil {
		t.Fatalf("idempotent upsert: %v", err)
	}
	due, err := st.TravelAlerts().ListDue(ctx, now.Add(90*time.Minute), 10)
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("due = %+v, want none (sent alert with unchanged leave_at)", due)
	}

	// Moved leave_at: sent_at clears, alert becomes due again.
	moved := leaveAt.Add(30 * time.Minute)
	if err := st.TravelAlerts().Upsert(ctx, ev.ID, "u1", moved); err != nil {
		t.Fatalf("moved upsert: %v", err)
	}
	due, err = st.TravelAlerts().ListDue(ctx, now.Add(2*time.Hour), 10)
	if err != nil {
		t.Fatalf("list due after move: %v", err)
	}
	if len(due) != 1 || due[0].EventID != ev.ID || due[0].UserID != "u1" {
		t.Fatalf("due = %+v, want the re-armed alert", due)
	}
	if !due[0].LeaveAt.Equal(moved) || due[0].SentAt != nil {
		t.Fatalf("alert = %+v, want moved leave_at with sent_at cleared", due[0])
	}
}

func TestTravelAlertListDueOrderingAndLimit(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	acct1 := seedAccount(t, st, "u1")
	acct2 := seedAccount(t, st, "u2")
	cal1 := seedCalendar(t, st, acct1.ID)
	cal2 := seedCalendar(t, st, acct2.ID)
	now := time.Now().UTC().Truncate(time.Microsecond)

	evLate := seedGeoEvent(t, st, cal1.ID, "pe-late", now.Add(3*time.Hour))
	evEarly := seedGeoEvent(t, st, cal2.ID, "pe-early", now.Add(2*time.Hour))
	evFuture := seedGeoEvent(t, st, cal1.ID, "pe-future", now.Add(30*time.Hour))

	if err := st.TravelAlerts().Upsert(ctx, evLate.ID, "u1", now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := st.TravelAlerts().Upsert(ctx, evEarly.ID, "u2", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.TravelAlerts().Upsert(ctx, evFuture.ID, "u1", now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	due, err := st.TravelAlerts().ListDue(ctx, now, 10)
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	if len(due) != 2 {
		t.Fatalf("due = %d, want 2 (future alert excluded)", len(due))
	}
	if due[0].EventID != evEarly.ID || due[1].EventID != evLate.ID {
		t.Fatalf("order = %s, %s; want oldest leave_at first", due[0].EventID, due[1].EventID)
	}
	// Alerts are worker-wide but carry their owning user for device routing.
	if due[0].UserID != "u2" || due[1].UserID != "u1" {
		t.Fatalf("user ids = %s/%s, want u2/u1", due[0].UserID, due[1].UserID)
	}

	limited, err := st.TravelAlerts().ListDue(ctx, now, 1)
	if err != nil {
		t.Fatalf("list due limited: %v", err)
	}
	if len(limited) != 1 || limited[0].EventID != evEarly.ID {
		t.Fatalf("limited = %+v, want just the earliest", limited)
	}
}

func TestTravelAlertMarkSentAndDelete(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	cal := seedCalendar(t, st, acct.ID)
	now := time.Now().UTC().Truncate(time.Microsecond)
	ev := seedGeoEvent(t, st, cal.ID, "pe-1", now.Add(2*time.Hour))

	if err := st.TravelAlerts().MarkSent(ctx, ev.ID, now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("MarkSent(absent) = %v, want ErrNotFound", err)
	}
	if err := st.TravelAlerts().Upsert(ctx, ev.ID, "u1", now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := st.TravelAlerts().MarkSent(ctx, ev.ID, now); err != nil {
		t.Fatalf("MarkSent: %v", err)
	}
	due, err := st.TravelAlerts().ListDue(ctx, now, 10)
	if err != nil || len(due) != 0 {
		t.Fatalf("due = %v (%v), want empty after MarkSent", due, err)
	}

	if err := st.TravelAlerts().Delete(ctx, ev.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := st.TravelAlerts().Delete(ctx, ev.ID); err != nil {
		t.Fatalf("Delete (idempotent): %v", err)
	}
}

// TestTravelAlertCascadesWithEvent: deleting the event mirror removes its
// pending alert (ON DELETE CASCADE) — no orphaned "leave now" pushes.
func TestTravelAlertCascadesWithEvent(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	cal := seedCalendar(t, st, acct.ID)
	now := time.Now().UTC().Truncate(time.Microsecond)
	ev := seedGeoEvent(t, st, cal.ID, "pe-1", now.Add(2*time.Hour))

	if err := st.TravelAlerts().Upsert(ctx, ev.ID, "u1", now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := st.Events().Delete(ctx, ev.ID); err != nil {
		t.Fatalf("delete event: %v", err)
	}
	due, err := st.TravelAlerts().ListDue(ctx, now, 10)
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("due = %+v, want cascade-deleted with the event", due)
	}
}
