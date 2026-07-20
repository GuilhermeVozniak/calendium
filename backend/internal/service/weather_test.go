package service

// Tests for the WeatherService (M2.8 Task 13): 30-minute per-location cache
// with a fake clock, day clamping, entitlement gating, and validation.

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

type fakeWeatherProvider struct {
	calls   int
	gotLat  float64
	gotLon  float64
	gotTZ   string
	gotDays int
	ret     []domain.DayForecast
	err     error
}

func (f *fakeWeatherProvider) DailyForecast(_ context.Context, lat, lon float64, tz string, days int) ([]domain.DayForecast, error) {
	f.calls++
	f.gotLat, f.gotLon, f.gotTZ, f.gotDays = lat, lon, tz, days
	if f.err != nil {
		return nil, f.err
	}
	return f.ret, nil
}

var _ port.WeatherProvider = (*fakeWeatherProvider)(nil)

func fullForecast() []domain.DayForecast {
	days := make([]domain.DayForecast, 14)
	for i := range days {
		days[i] = domain.DayForecast{
			Date:         fmt.Sprintf("2026-07-%02d", 19+i),
			Code:         i,
			HighCelsius:  20 + float64(i),
			LowCelsius:   10 + float64(i),
			PrecipChance: i * 5,
		}
	}
	return days
}

func newWeatherSvc(provider *fakeWeatherProvider, clock *fakeClock) *WeatherSvc {
	return NewWeatherService(WeatherServiceDeps{
		Provider:      provider,
		Subscriptions: newSubscriptionRepo(),
		Clock:         clock,
		SelfHosted:    true, // entitlement bypass; gating is covered below
	})
}

func TestForecastCachesForThirtyMinutesPerRoundedLocation(t *testing.T) {
	provider := &fakeWeatherProvider{ret: fullForecast()}
	clock := newClock(time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC))
	svc := newWeatherSvc(provider, clock)
	ctx := context.Background()

	if _, err := svc.Forecast(ctx, "u1", 52.5201, 13.4049, "Europe/Berlin", 7); err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	if provider.gotDays != 14 {
		t.Fatalf("provider asked for %d days, want the full 14-day window", provider.gotDays)
	}
	if provider.gotLat != 52.52 || provider.gotLon != 13.4 {
		t.Fatalf("provider got (%v, %v), want coordinates rounded to 2 decimals (52.52, 13.4)", provider.gotLat, provider.gotLon)
	}

	// Nearby coordinates (same rounded key) within the TTL: cache hit.
	clock.Advance(29 * time.Minute)
	got, err := svc.Forecast(ctx, "u1", 52.5249, 13.3951, "Europe/Berlin", 3)
	if err != nil {
		t.Fatalf("Forecast (cached): %v", err)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d after cached read, want 1", provider.calls)
	}
	if len(got) != 3 || got[0].Date != "2026-07-19" {
		t.Fatalf("cached slice = %d rows (first %+v), want 3 from the cached window", len(got), got[0])
	}

	// A different location misses the cache.
	if _, err := svc.Forecast(ctx, "u1", 40.71, -74.01, "America/New_York", 7); err != nil {
		t.Fatalf("Forecast (other location): %v", err)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 after a different location", provider.calls)
	}

	// Past the TTL the original location refetches (never served stale).
	clock.Advance(2 * time.Minute) // 31 minutes after the first fetch
	if _, err := svc.Forecast(ctx, "u1", 52.52, 13.40, "Europe/Berlin", 7); err != nil {
		t.Fatalf("Forecast (expired): %v", err)
	}
	if provider.calls != 3 {
		t.Fatalf("provider calls = %d, want 3 after TTL expiry", provider.calls)
	}
}

func TestForecastClampsDays(t *testing.T) {
	provider := &fakeWeatherProvider{ret: fullForecast()}
	svc := newWeatherSvc(provider, newClock(time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC)))
	ctx := context.Background()

	got, err := svc.Forecast(ctx, "u1", 1, 1, "UTC", 99)
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if len(got) != 14 {
		t.Fatalf("days=99 returned %d rows, want clamp to 14", len(got))
	}

	got, err = svc.Forecast(ctx, "u1", 1, 1, "UTC", 0)
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("days=0 returned %d rows, want clamp to 1", len(got))
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1 (both day counts share one cache entry)", provider.calls)
	}
}

func TestForecastRequiresEntitlement(t *testing.T) {
	provider := &fakeWeatherProvider{ret: fullForecast()}
	svc := NewWeatherService(WeatherServiceDeps{
		Provider:      provider,
		Subscriptions: newSubscriptionRepo(), // no subscription seeded => 402
		Clock:         newClock(time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC)),
		SelfHosted:    false,
	})

	_, err := svc.Forecast(context.Background(), "u1", 1, 1, "UTC", 7)
	if !errors.Is(err, domain.ErrPaymentRequired) {
		t.Fatalf("err = %v, want ErrPaymentRequired", err)
	}
	if provider.calls != 0 {
		t.Fatalf("provider called %d times for an unentitled user, want 0", provider.calls)
	}
}

func TestForecastValidatesCoordinatesAndTimeZone(t *testing.T) {
	provider := &fakeWeatherProvider{ret: fullForecast()}
	svc := newWeatherSvc(provider, newClock(time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC)))
	ctx := context.Background()

	for name, call := range map[string]func() error{
		"lat over 90":    func() error { _, err := svc.Forecast(ctx, "u1", 90.01, 0, "UTC", 7); return err },
		"lon under -180": func() error { _, err := svc.Forecast(ctx, "u1", 0, -180.5, "UTC", 7); return err },
		"bogus tz":       func() error { _, err := svc.Forecast(ctx, "u1", 0, 0, "Mars/Olympus", 7); return err },
		"empty tz":       func() error { _, err := svc.Forecast(ctx, "u1", 0, 0, "", 7); return err },
	} {
		if err := call(); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", name, err)
		}
	}
	if provider.calls != 0 {
		t.Fatalf("provider called %d times on invalid input, want 0", provider.calls)
	}
}

func TestForecastProviderErrorPropagatesWithoutPoisoningCache(t *testing.T) {
	provider := &fakeWeatherProvider{err: errors.New("vendor down")}
	clock := newClock(time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC))
	svc := newWeatherSvc(provider, clock)
	ctx := context.Background()

	if _, err := svc.Forecast(ctx, "u1", 1, 1, "UTC", 7); err == nil {
		t.Fatal("provider error swallowed, want propagation")
	}

	// Vendor recovers: the failure was not cached as an empty forecast.
	provider.err = nil
	provider.ret = fullForecast()
	got, err := svc.Forecast(ctx, "u1", 1, 1, "UTC", 7)
	if err != nil {
		t.Fatalf("Forecast after recovery: %v", err)
	}
	if len(got) != 7 {
		t.Fatalf("rows = %d, want 7 after recovery", len(got))
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (failure must not populate the cache)", provider.calls)
	}
}
