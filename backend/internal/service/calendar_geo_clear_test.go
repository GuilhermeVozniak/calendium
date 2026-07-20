package service

// calendar_geo_clear_test.go pins the M2.8 Task 12 stale-coords carry-forward
// from Task 11's review: editing an event's location string WITHOUT picking a
// fresh autocomplete suggestion must null the stored coordinates (server-side
// clear via EventRepo.ClearGeo, since Upsert COALESCE-preserves them), so the
// travel planner skips the event instead of routing to the OLD location.

import (
	"context"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// TestUpdateEventLocationEditWithoutPickClearsCoordinates: a PATCH that
// changes location with no coordinates clears the stored pair — in the
// response, in the mirror, and via the targeted ClearGeo write (the
// COALESCE-proof path).
func TestUpdateEventLocationEditWithoutPickClearsCoordinates(t *testing.T) {
	ctx := context.Background()
	f := newCalFixture(t)
	lat, lon := 52.5219, 13.4132

	seedEvent(f, "ev1", func(e *domain.Event) {
		e.LocationLat, e.LocationLon = &lat, &lon
	})
	f.provider.updatedEvent = domain.Event{
		ProviderEventID: "pe-1", Title: "Standup",
		Start: f.clock.Now().Add(time.Hour), End: f.clock.Now().Add(90 * time.Minute),
	}

	loc := "some other café I'll look up later"
	got, err := f.svc.UpdateEvent(ctx, "u1", "ev1", domain.EventPatch{Location: &loc})
	if err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if got.LocationLat != nil || got.LocationLon != nil {
		t.Fatalf("returned coords = %v/%v, want cleared on a pick-less location edit", got.LocationLat, got.LocationLon)
	}
	stored := f.events.byID["ev1"]
	if stored.LocationLat != nil || stored.LocationLon != nil {
		t.Fatalf("mirror coords = %v/%v, want cleared", stored.LocationLat, stored.LocationLon)
	}
	if len(f.events.clearedGeo) != 1 || f.events.clearedGeo[0] != "ev1" {
		t.Fatalf("ClearGeo calls = %v, want the targeted write (Upsert alone COALESCE-preserves)", f.events.clearedGeo)
	}
}

// TestUpdateEventLocationEditWithPickKeepsFreshCoordinates: supplying new
// coordinates alongside the location is a real pick — nothing is cleared.
func TestUpdateEventLocationEditWithPickKeepsFreshCoordinates(t *testing.T) {
	ctx := context.Background()
	f := newCalFixture(t)
	oldLat, oldLon := 52.5219, 13.4132
	newLat, newLon := 48.8566, 2.3522

	seedEvent(f, "ev1", func(e *domain.Event) {
		e.LocationLat, e.LocationLon = &oldLat, &oldLon
	})
	f.provider.updatedEvent = domain.Event{
		ProviderEventID: "pe-1", Title: "Standup",
		Start: f.clock.Now().Add(time.Hour), End: f.clock.Now().Add(90 * time.Minute),
	}

	loc := "Champ de Mars, Paris"
	got, err := f.svc.UpdateEvent(ctx, "u1", "ev1", domain.EventPatch{
		Location: &loc, LocationLat: &newLat, LocationLon: &newLon,
	})
	if err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if got.LocationLat == nil || *got.LocationLat != newLat || got.LocationLon == nil || *got.LocationLon != newLon {
		t.Fatalf("coords = %v/%v, want the fresh pick", got.LocationLat, got.LocationLon)
	}
	if len(f.events.clearedGeo) != 0 {
		t.Fatalf("ClearGeo calls = %v, want none on a real pick", f.events.clearedGeo)
	}
}

// TestClearedCoordinatesSkipTravelPlanning closes the carry-forward loop:
// once the pick-less edit cleared the coordinates, the travel planner skips
// the event — a missing buffer beats a confidently wrong one.
func TestClearedCoordinatesSkipTravelPlanning(t *testing.T) {
	ctx := context.Background()
	f := newCalFixture(t)
	lat, lon := 52.5219, 13.4132
	start := f.clock.Now().Add(3 * time.Hour)

	seedEvent(f, "ev1", func(e *domain.Event) {
		e.Start, e.End = start, start.Add(time.Hour)
		e.LocationLat, e.LocationLon = &lat, &lon
	})
	f.provider.updatedEvent = domain.Event{
		ProviderEventID: "pe-1", Title: "Standup", Start: start, End: start.Add(time.Hour),
	}
	loc := "new spot, address TBD"
	if _, err := f.svc.UpdateEvent(ctx, "u1", "ev1", domain.EventPatch{Location: &loc}); err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}

	plans, err := planTravel(travelPrefs("u1"), []domain.Event{f.events.byID["ev1"]}, nil, nil,
		fixedTravel(30*time.Minute))
	if err != nil {
		t.Fatalf("planTravel: %v", err)
	}
	if len(plans) != 0 {
		t.Fatalf("plans = %+v, want none for an event whose coords were cleared", plans)
	}
}
