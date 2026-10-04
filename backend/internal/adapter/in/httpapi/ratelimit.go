package httpapi

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RateLimits is the per-class budget (requests per minute); burst equals
// the per-minute value except publicRead (half). A zero value for the whole
// struct means DefaultRateLimits; a zero for ONE class disables that class
// (tests only). Limits are per process: a multi-replica deployment
// multiplies them by replica count (upgrades.md assumes one api replica).
type RateLimits struct {
	PublicReadPerMin  int // per IP: public GETs + share routes
	PublicWritePerMin int // per IP: public POSTs
	UserPerMin        int // per user: every authed route not in a class
	MutateHeavyPerMin int // per user: token-minting / fan-out mutations
	SearchPerMin      int // per user: search-shaped reads
}

// DefaultRateLimits mirrors the spec table (and the RATE_LIMIT_* defaults).
func DefaultRateLimits() RateLimits {
	return RateLimits{PublicReadPerMin: 60, PublicWritePerMin: 5, UserPerMin: 600, MutateHeavyPerMin: 30, SearchPerMin: 120}
}

// Limiter class names used by limitClass on the route table.
const (
	classUser        = "user"
	classMutateHeavy = "mutate_heavy"
	classSearch      = "search"
)

// rateLimiter is an in-memory per-key token bucket: capacity `burst`,
// refilled at `perMin` tokens/minute. Keys are "ip:<s.clientIP>" for the
// public routes (proxy-aware, see proxy.go) and "user:<actor id>" for the
// authed routes.
type rateLimiter struct {
	mu      sync.Mutex
	perMin  float64
	burst   float64
	now     func() time.Time
	buckets map[string]*bucket
	lastGC  time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(perMin, burst int, now func() time.Time) *rateLimiter {
	return &rateLimiter{
		perMin: float64(perMin), burst: float64(burst),
		now: now, buckets: make(map[string]*bucket),
	}
}

// newClassLimiter builds a production limiter, or nil (class disabled)
// when perMin is zero.
func newClassLimiter(perMin, burst int) *rateLimiter {
	if perMin <= 0 {
		return nil
	}
	if burst < 1 {
		burst = 1
	}
	return newRateLimiter(perMin, burst, time.Now)
}

// allow consumes one token for key. When refused it also returns how long
// until one token is available: ceil((1 − tokens) / perMin minutes), at
// least one second — the Retry-After value.
func (l *rateLimiter) allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.now()
	// Opportunistic GC: drop buckets idle > 10 minutes, at most once a minute.
	if t.Sub(l.lastGC) > time.Minute {
		for k, b := range l.buckets {
			if t.Sub(b.last) > 10*time.Minute {
				delete(l.buckets, k)
			}
		}
		l.lastGC = t
	}
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: t}
		l.buckets[key] = b
	}
	b.tokens = min(l.burst, b.tokens+t.Sub(b.last).Minutes()*l.perMin)
	b.last = t
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	seconds := max(math.Ceil((1-b.tokens)/l.perMin*60), 1)
	return false, time.Duration(seconds) * time.Second
}

// writeRateLimited renders the 429 envelope with Retry-After in whole seconds.
func (s *server) writeRateLimited(w http.ResponseWriter, r *http.Request, retry time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retry.Seconds()))))
	writeJSON(w, http.StatusTooManyRequests, errorBody{Error: errorDetail{
		Code: "rate_limited", Message: safeMessage("rate_limited"), RequestID: requestIDFrom(r.Context()),
	}})
}

// rateLimited wraps a public handler with a per-client-IP bucket; a nil
// limiter (class disabled) passes through.
func (s *server) rateLimited(l *rateLimiter, next http.HandlerFunc) http.HandlerFunc {
	if l == nil {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if ok, retry := l.allow("ip:" + s.clientIP(r)); !ok {
			s.writeRateLimited(w, r, retry)
			return
		}
		next(w, r)
	}
}

// userLimited wraps an authed handler with a per-user bucket keyed on the
// AUTHENTICATED actor (it runs before withActAs swaps in the principal).
func (s *server) userLimited(l *rateLimiter, next http.HandlerFunc) http.HandlerFunc {
	if l == nil {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if ok, retry := l.allow("user:" + userFrom(r).ID); !ok {
			s.writeRateLimited(w, r, retry)
			return
		}
		next(w, r)
	}
}

// routeOptions tunes one authed route; see limitClass.
type routeOptions struct {
	class string
}

type routeOption func(*routeOptions)

// limitClass charges the route to the named per-user class instead of
// the general user bucket.
func limitClass(name string) routeOption {
	return func(o *routeOptions) { o.class = name }
}

// routeClasses reports the limiter class of every authed pattern registered
// by New (pinned by tests against the spec table).
func (s *server) routeClasses() map[string]string { return s.classOf }
