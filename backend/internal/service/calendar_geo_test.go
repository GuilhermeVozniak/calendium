package service

import (
	"context"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// --- M2.8 Task 11: event geo plumbing (create/update persistence) ------------

func TestCreateEventPersistsPickedCoordinates(t *testing.T) {
	ctx := context.Background()
	f := newCalFixture(t)
	base := f.clock.Now()
	lat, lon := 52.5219, 13.4132

	// The provider's canned response never carries coordinates (vendors
	// know nothing about them) — the mirror write must keep the picked
	// ones anyway, without changing what is sent to the provider.
	f.provider.createdEvent = domain.Event{
		ProviderEventID: "pe-new", Title: "Offsite",
		Start: base.Add(time.Hour), End: base.Add(2 * time.Hour),
	}

	got, err := f.svc.CreateEvent(ctx, "u1", domain.EventInput{
		CalendarID: "cal1", Title: "Offsite",
		Start: base.Add(time.Hour), End: base.Add(2 * time.Hour),
		Location:    "Alexanderplatz, Berlin",
		LocationLat: &lat, LocationLon: &lon,
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if got.LocationLat == nil || *got.LocationLat != lat || got.LocationLon == nil || *got.LocationLon != lon {
		t.Fatalf("returned coords = %v/%v, want %v/%v", got.LocationLat, got.LocationLon, lat, lon)
	}
	stored := f.events.byID[got.ID]
	if stored.LocationLat == nil || *stored.LocationLat != lat || stored.LocationLon == nil || *stored.LocationLon != lon {
		t.Fatalf("mirror coords = %v/%v, want %v/%v", stored.LocationLat, stored.LocationLon, lat, lon)
	}
}

func TestCreateEventFreeTypedLocationKeepsCoordinatesNil(t *testing.T) {
	ctx := context.Background()
	f := newCalFixture(t)
	base := f.clock.Now()
	f.provider.createdEvent = domain.Event{
		ProviderEventID: "pe-new", Title: "Coffee",
		Start: base.Add(time.Hour), End: base.Add(2 * time.Hour),
	}
	got, err := f.svc.CreateEvent(ctx, "u1", domain.EventInput{
		CalendarID: "cal1", Title: "Coffee",
		Start: base.Add(time.Hour), End: base.Add(2 * time.Hour),
		Location: "that nice place around the corner",
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if got.LocationLat != nil || got.LocationLon != nil {
		t.Fatalf("coords = %v/%v, want nil/nil for free-typed locations", got.LocationLat, got.LocationLon)
	}
}

func TestUpdateEventPatchesCoordinates(t *testing.T) {
	ctx := context.Background()
	f := newCalFixture(t)
	lat, lon := 48.8566, 2.3522

	seedEvent(f, "ev1", nil)
	f.provider.updatedEvent = domain.Event{
		ProviderEventID: "pe-1", Title: "Standup",
		Start: f.clock.Now().Add(time.Hour), End: f.clock.Now().Add(90 * time.Minute),
	}

	loc := "Champ de Mars, Paris"
	got, err := f.svc.UpdateEvent(ctx, "u1", "ev1", domain.EventPatch{
		Location:    &loc,
		LocationLat: &lat, LocationLon: &lon,
	})
	if err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if got.LocationLat == nil || *got.LocationLat != lat || got.LocationLon == nil || *got.LocationLon != lon {
		t.Fatalf("returned coords = %v/%v, want %v/%v", got.LocationLat, got.LocationLon, lat, lon)
	}
	stored := f.events.byID["ev1"]
	if stored.LocationLat == nil || *stored.LocationLat != lat {
		t.Fatalf("mirror coords = %v/%v, want persisted", stored.LocationLat, stored.LocationLon)
	}
}

func TestUpdateEventNilCoordinatePatchLeavesStoredCoordinates(t *testing.T) {
	ctx := context.Background()
	f := newCalFixture(t)
	lat, lon := 52.5219, 13.4132

	seedEvent(f, "ev1", func(e *domain.Event) {
		e.LocationLat, e.LocationLon = &lat, &lon
	})
	f.provider.updatedEvent = domain.Event{
		ProviderEventID: "pe-1", Title: "Renamed standup",
		Start: f.clock.Now().Add(time.Hour), End: f.clock.Now().Add(90 * time.Minute),
	}

	title := "Renamed standup"
	got, err := f.svc.UpdateEvent(ctx, "u1", "ev1", domain.EventPatch{Title: &title})
	if err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if got.LocationLat == nil || *got.LocationLat != lat || got.LocationLon == nil || *got.LocationLon != lon {
		t.Fatalf("coords = %v/%v, want preserved through a coordinate-less patch", got.LocationLat, got.LocationLon)
	}
}
