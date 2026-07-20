package postgres

import (
	"context"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// --- M2.8 Task 11: events.location_lat/location_lon round trip ---------------

func TestEventGeoColumnsRoundTrip(t *testing.T) {
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

	got, err := st.Events().GetByID(ctx, ev.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.LocationLat == nil || *got.LocationLat != lat {
		t.Fatalf("LocationLat = %v, want %v", got.LocationLat, lat)
	}
	if got.LocationLon == nil || *got.LocationLon != lon {
		t.Fatalf("LocationLon = %v, want %v", got.LocationLon, lon)
	}
}

func TestEventGeoNilCoordinatesScanAsNil(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	cal := seedCalendar(t, st, acct.ID)
	now := time.Now().UTC().Truncate(time.Microsecond)

	ev, err := st.Events().Upsert(ctx, domain.Event{
		CalendarID: cal.ID, ProviderEventID: "pe-plain", Title: "Coffee",
		Start: now, End: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := st.Events().GetByID(ctx, ev.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.LocationLat != nil || got.LocationLon != nil {
		t.Fatalf("coords = %v/%v, want nil/nil", got.LocationLat, got.LocationLon)
	}
}

func TestEventGeoSurvivesCoordinateLessSyncUpsert(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	cal := seedCalendar(t, st, acct.ID)
	now := time.Now().UTC().Truncate(time.Microsecond)

	lat, lon := 52.5200066, 13.404954
	if _, err := st.Events().Upsert(ctx, domain.Event{
		CalendarID: cal.ID, ProviderEventID: "pe-geo", Title: "Offsite",
		Start: now, End: now.Add(time.Hour),
		LocationLat: &lat, LocationLon: &lon,
	}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	// A provider-sync upsert of the same event never carries coordinates;
	// it must not wipe the locally chosen ones (COALESCE on conflict).
	synced, err := st.Events().Upsert(ctx, domain.Event{
		CalendarID: cal.ID, ProviderEventID: "pe-geo", Title: "Offsite (renamed upstream)",
		Start: now, End: now.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("sync upsert: %v", err)
	}
	if synced.LocationLat == nil || *synced.LocationLat != lat {
		t.Fatalf("returned LocationLat = %v, want preserved %v", synced.LocationLat, lat)
	}
	got, err := st.Events().GetByID(ctx, synced.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != "Offsite (renamed upstream)" {
		t.Fatalf("Title = %q, want the synced rename applied", got.Title)
	}
	if got.LocationLat == nil || *got.LocationLat != lat || got.LocationLon == nil || *got.LocationLon != lon {
		t.Fatalf("stored coords = %v/%v, want preserved %v/%v", got.LocationLat, got.LocationLon, lat, lon)
	}

	// An upsert that DOES carry coordinates replaces them.
	lat2, lon2 := 48.8566, 2.3522
	if _, err := st.Events().Upsert(ctx, domain.Event{
		CalendarID: cal.ID, ProviderEventID: "pe-geo", Title: "Offsite",
		Start: now, End: now.Add(time.Hour),
		LocationLat: &lat2, LocationLon: &lon2,
	}); err != nil {
		t.Fatalf("re-pick upsert: %v", err)
	}
	got, err = st.Events().GetByID(ctx, got.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.LocationLat == nil || *got.LocationLat != lat2 || got.LocationLon == nil || *got.LocationLon != lon2 {
		t.Fatalf("stored coords = %v/%v, want replaced %v/%v", got.LocationLat, got.LocationLon, lat2, lon2)
	}
}
