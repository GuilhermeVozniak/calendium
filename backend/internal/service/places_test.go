package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

type fakeMapsProvider struct {
	places   []domain.Place
	err      error
	calls    int
	gotQuery string
	gotLimit int
}

func (f *fakeMapsProvider) Autocomplete(_ context.Context, query string, limit int) ([]domain.Place, error) {
	f.calls++
	f.gotQuery = query
	f.gotLimit = limit
	return f.places, f.err
}

func (f *fakeMapsProvider) TravelTime(context.Context, float64, float64, float64, float64, domain.TravelMode) (time.Duration, error) {
	return 0, errors.New("not used in places tests")
}

var _ port.MapsProvider = (*fakeMapsProvider)(nil)

func newPlacesFixture() (*PlacesService, *fakeMapsProvider, *fakeClock) {
	maps := &fakeMapsProvider{places: []domain.Place{
		{Name: "Berlin", Address: "Berlin, Germany", Lat: 52.52, Lon: 13.405},
	}}
	clock := newClock(time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC))
	svc := NewPlacesService(PlacesServiceDeps{Maps: maps, Clock: clock, SelfHosted: true})
	return svc, maps, clock
}

func TestPlacesAutocompleteMinLength(t *testing.T) {
	ctx := context.Background()
	svc, maps, _ := newPlacesFixture()
	for _, q := range []string{"", "ab", "  ab  "} {
		if _, err := svc.Autocomplete(ctx, "u1", q); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("q=%q: err = %v, want ErrValidation", q, err)
		}
	}
	if maps.calls != 0 {
		t.Fatalf("gateway calls = %d, want 0 for under-length queries", maps.calls)
	}
}

func TestPlacesAutocompleteCacheHitSkipsGateway(t *testing.T) {
	ctx := context.Background()
	svc, maps, clock := newPlacesFixture()

	got, err := svc.Autocomplete(ctx, "u1", "Berlin")
	if err != nil {
		t.Fatalf("Autocomplete: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Berlin" {
		t.Fatalf("got = %+v", got)
	}
	if maps.calls != 1 || maps.gotQuery != "Berlin" || maps.gotLimit != 5 {
		t.Fatalf("gateway = calls %d query %q limit %d, want 1/Berlin/5", maps.calls, maps.gotQuery, maps.gotLimit)
	}

	// Same query — case-insensitively, whitespace-trimmed — is a cache hit.
	for _, q := range []string{"Berlin", "berlin", "  BERLIN  "} {
		if _, err := svc.Autocomplete(ctx, "u1", q); err != nil {
			t.Fatalf("cached %q: %v", q, err)
		}
	}
	if maps.calls != 1 {
		t.Fatalf("gateway calls = %d, want 1 (cache must absorb repeats)", maps.calls)
	}

	// Past the 10-minute TTL the vendor is consulted again.
	clock.Advance(10 * time.Minute)
	if _, err := svc.Autocomplete(ctx, "u1", "Berlin"); err != nil {
		t.Fatalf("post-TTL: %v", err)
	}
	if maps.calls != 2 {
		t.Fatalf("gateway calls = %d, want 2 after TTL expiry", maps.calls)
	}
}

func TestPlacesAutocompleteCachedSliceIsACopy(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newPlacesFixture()
	first, err := svc.Autocomplete(ctx, "u1", "Berlin")
	if err != nil {
		t.Fatalf("Autocomplete: %v", err)
	}
	first[0].Name = "mutated"
	second, err := svc.Autocomplete(ctx, "u1", "Berlin")
	if err != nil {
		t.Fatalf("Autocomplete: %v", err)
	}
	if second[0].Name != "Berlin" {
		t.Fatalf("cache entry was aliased by a caller mutation: %+v", second[0])
	}
}

func TestPlacesAutocompleteNilProviderIsNotImplemented(t *testing.T) {
	svc := NewPlacesService(PlacesServiceDeps{
		Clock:      newClock(time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)),
		SelfHosted: true,
	})
	if _, err := svc.Autocomplete(context.Background(), "u1", "Berlin"); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("err = %v, want ErrNotImplemented", err)
	}
}

func TestPlacesAutocompleteRequiresEntitlement(t *testing.T) {
	svc := NewPlacesService(PlacesServiceDeps{
		Subscriptions: newSubscriptionRepo(), // empty → ErrNotFound → ErrPaymentRequired
		Maps:          &fakeMapsProvider{},
		Clock:         newClock(time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)),
		SelfHosted:    false,
	})
	if _, err := svc.Autocomplete(context.Background(), "u1", "Berlin"); !errors.Is(err, domain.ErrPaymentRequired) {
		t.Fatalf("err = %v, want ErrPaymentRequired", err)
	}
}

func TestPlacesAutocompleteGatewayErrorPropagates(t *testing.T) {
	svc, maps, _ := newPlacesFixture()
	maps.err = errors.New("vendor down")
	if _, err := svc.Autocomplete(context.Background(), "u1", "Berlin"); err == nil {
		t.Fatal("err = nil, want gateway error")
	}
	// A failed lookup must not be cached.
	maps.err = nil
	if _, err := svc.Autocomplete(context.Background(), "u1", "Berlin"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if maps.calls != 2 {
		t.Fatalf("gateway calls = %d, want 2 (errors are not cached)", maps.calls)
	}
}

func TestPlacesAutocompleteNilGatewayResultBecomesEmptySlice(t *testing.T) {
	svc, maps, _ := newPlacesFixture()
	maps.places = nil
	got, err := svc.Autocomplete(context.Background(), "u1", "Nowhere")
	if err != nil {
		t.Fatalf("Autocomplete: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("got = %#v, want non-nil empty slice", got)
	}
}
