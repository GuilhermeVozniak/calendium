package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// --- M2.8 Task 12: EventRepo.ClearGeo ----------------------------------------
//
// Upsert COALESCE-preserves coordinates on NULL input (so coordinate-less
// provider syncs can't wipe a user's pick — see event_geo_test.go). That
// makes a deliberate clear impossible through Upsert alone; ClearGeo is the
// targeted write the events PATCH path uses when a location is edited
// without a fresh autocomplete pick.

func TestEventClearGeo(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	cal := seedCalendar(t, st, acct.ID)
	now := time.Now().UTC().Truncate(time.Microsecond)

	lat, lon := 52.5200066, 13.404954
	ev, err := st.Events().Upsert(ctx, domain.Event{
		CalendarID: cal.ID, ProviderEventID: "pe-geo", Title: "Offsite",
		Start: now, End: now.Add(time.Hour),
		LocationLat: &lat, LocationLon: &lon,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Sanity: a coordinate-less upsert does NOT clear (the COALESCE contract
	// ClearGeo exists to punch through).
	if _, err := st.Events().Upsert(ctx, domain.Event{
		CalendarID: cal.ID, ProviderEventID: "pe-geo", Title: "Offsite",
		Start: now, End: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("coordinate-less upsert: %v", err)
	}
	got, err := st.Events().GetByID(ctx, ev.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.LocationLat == nil {
		t.Fatal("COALESCE contract changed: coordinate-less upsert cleared coords")
	}

	if err := st.Events().ClearGeo(ctx, ev.ID); err != nil {
		t.Fatalf("ClearGeo: %v", err)
	}
	got, err = st.Events().GetByID(ctx, ev.ID)
	if err != nil {
		t.Fatalf("get after clear: %v", err)
	}
	if got.LocationLat != nil || got.LocationLon != nil {
		t.Fatalf("coords = %v/%v, want nil/nil after ClearGeo", got.LocationLat, got.LocationLon)
	}

	if err := st.Events().ClearGeo(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("ClearGeo(missing) = %v, want ErrNotFound", err)
	}
}
