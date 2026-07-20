package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

const (
	// placesQueryMinChars is the minimum autocomplete query length.
	placesQueryMinChars = 3
	// placesLimit caps suggestions per query.
	placesLimit = 5
	// placesCacheTTL is how long one query's results are served from memory.
	// Nominatim's usage policy allows at most 1 req/s, so repeated
	// keystrokes/backtracks must not re-hit the vendor.
	placesCacheTTL = 10 * time.Minute
	// placesCacheMax bounds the cache; oldest entries are evicted first.
	placesCacheMax = 512
)

// PlacesServiceDeps wires PlacesService. Maps may be nil (maps not
// configured); Autocomplete then answers ErrNotImplemented.
type PlacesServiceDeps struct {
	Subscriptions port.SubscriptionRepo
	Maps          port.MapsProvider
	Clock         port.Clock
	SelfHosted    bool
}

// PlacesService implements port.PlacesService: entitlement-gated location
// autocomplete over a MapsProvider with a TTL'd, size-bounded in-memory
// cache (mutex-guarded map with timestamp eviction — stdlib only).
type PlacesService struct {
	ent   entitlement
	maps  port.MapsProvider
	clock port.Clock

	mu    sync.Mutex
	cache map[string]placesEntry
}

type placesEntry struct {
	places []domain.Place
	at     time.Time
}

var _ port.PlacesService = (*PlacesService)(nil)

func NewPlacesService(d PlacesServiceDeps) *PlacesService {
	return &PlacesService{
		ent:   entitlement{subs: d.Subscriptions, clock: d.Clock, selfHost: d.SelfHosted},
		maps:  d.Maps,
		clock: d.Clock,
		cache: map[string]placesEntry{},
	}
}

func (s *PlacesService) Autocomplete(ctx context.Context, userID, query string) ([]domain.Place, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	if s.maps == nil {
		return nil, fmt.Errorf("%w: no maps provider is configured", domain.ErrNotImplemented)
	}
	query = strings.TrimSpace(query)
	if utf8.RuneCountInString(query) < placesQueryMinChars {
		return nil, fmt.Errorf("%w: query must be at least %d characters", domain.ErrValidation, placesQueryMinChars)
	}

	key := strings.ToLower(query)
	now := s.clock.Now()
	if hit, ok := s.cached(key, now); ok {
		return hit, nil
	}
	places, err := s.maps.Autocomplete(ctx, query, placesLimit)
	if err != nil {
		return nil, err
	}
	if places == nil {
		places = []domain.Place{}
	}
	s.store(key, places, now)
	return places, nil
}

// cached returns a copy of a fresh cache entry (callers must never alias the
// cached slice).
func (s *PlacesService) cached(key string, now time.Time) ([]domain.Place, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.cache[key]
	if !ok || now.Sub(e.at) >= placesCacheTTL {
		return nil, false
	}
	out := make([]domain.Place, len(e.places))
	copy(out, e.places)
	return out, true
}

// store inserts the entry after dropping expired rows; when still over
// budget it evicts the oldest entries (timestamp eviction). It keeps a
// private copy so the slice handed to the miss-path caller is never aliased
// by the cache.
func (s *PlacesService) store(key string, places []domain.Place, now time.Time) {
	owned := make([]domain.Place, len(places))
	copy(owned, places)
	places = owned
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, e := range s.cache {
		if now.Sub(e.at) >= placesCacheTTL {
			delete(s.cache, k)
		}
	}
	for len(s.cache) >= placesCacheMax {
		oldestKey := ""
		var oldestAt time.Time
		for k, e := range s.cache {
			if oldestKey == "" || e.at.Before(oldestAt) {
				oldestKey, oldestAt = k, e.at
			}
		}
		delete(s.cache, oldestKey)
	}
	s.cache[key] = placesEntry{places: places, at: now}
}
