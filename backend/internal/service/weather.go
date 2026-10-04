package service

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// weatherCacheTTL is how long one location's forecast is served from memory
// before the vendor is asked again (M2.8 Task 13: ~30 minutes). Entries past
// the TTL are never served — honest freshness — so a vendor outage makes the
// chips disappear rather than silently go stale.
const weatherCacheTTL = 30 * time.Minute

// weatherMaxDays caps a forecast request; the product surface is two weeks.
const weatherMaxDays = 14

// WeatherServiceDeps wires the weather use-cases.
type WeatherServiceDeps struct {
	Provider      port.WeatherProvider
	Subscriptions port.SubscriptionRepo
	Users         port.UserRepo // anchors the entitlement gate's lazy trial grant
	Clock         port.Clock
	// SelfHosted unlocks the paywall (open-core self-hosted mode).
	SelfHosted bool
}

// WeatherSvc implements port.WeatherService: entitlement-gated, best-effort
// day forecasts with a 30-minute in-memory cache keyed by rounded lat/lon
// (+ time zone), so a whole team in one city shares one vendor call.
type WeatherSvc struct {
	provider port.WeatherProvider
	ent      entitlement
	clock    port.Clock

	mu    sync.Mutex
	cache map[weatherKey]weatherEntry
}

type weatherKey struct {
	lat, lon float64 // rounded to 2 decimals (~1 km) so nearby callers share
	timeZone string
}

type weatherEntry struct {
	fetchedAt time.Time
	days      []domain.DayForecast
}

func NewWeatherService(d WeatherServiceDeps) *WeatherSvc {
	return &WeatherSvc{
		provider: d.Provider,
		ent:      entitlement{subs: d.Subscriptions, users: d.Users, clock: d.Clock, selfHost: d.SelfHosted},
		clock:    d.Clock,
		cache:    map[weatherKey]weatherEntry{},
	}
}

var _ port.WeatherService = (*WeatherSvc)(nil)

func roundCoord(v float64) float64 { return math.Round(v*100) / 100 }

func (s *WeatherSvc) Forecast(ctx context.Context, userID string, lat, lon float64, timeZone string, days int) ([]domain.DayForecast, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return nil, fmt.Errorf("%w: coordinates out of range", domain.ErrValidation)
	}
	if !validIANATimeZone(timeZone) {
		return nil, fmt.Errorf("%w: invalid time zone %q", domain.ErrValidation, timeZone)
	}
	// Clamp, don't reject: any positive day count is a reasonable ask and the
	// cache always holds the full window anyway.
	if days < 1 {
		days = 1
	}
	if days > weatherMaxDays {
		days = weatherMaxDays
	}

	key := weatherKey{lat: roundCoord(lat), lon: roundCoord(lon), timeZone: timeZone}
	now := s.clock.Now()

	s.mu.Lock()
	entry, ok := s.cache[key]
	s.mu.Unlock()
	if !ok || now.Sub(entry.fetchedAt) >= weatherCacheTTL {
		// Always fetch the full window so day/week/agenda callers with
		// different day counts share one cache entry per location.
		fresh, err := s.provider.DailyForecast(ctx, key.lat, key.lon, timeZone, weatherMaxDays)
		if err != nil {
			// Best-effort surface: no stale fallback past the TTL (honest
			// UI) — the error propagates and clients hide the chips.
			return nil, fmt.Errorf("weather provider: %w", err)
		}
		entry = weatherEntry{fetchedAt: now, days: fresh}
		s.mu.Lock()
		s.cache[key] = entry
		s.mu.Unlock()
	}

	if days > len(entry.days) {
		days = len(entry.days)
	}
	out := make([]domain.DayForecast, days)
	copy(out, entry.days[:days])
	return out, nil
}
