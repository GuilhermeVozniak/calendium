package httpapi

import (
	"container/list"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RateLimits is the per-class budget (requests per minute); burst equals
// the per-minute value except publicRead (half). A zero for a class
// disables that class, so the zero value disables API rate limiting (the
// config package resolves unset RATE_LIMIT_* to the defaults). Limits are
// per process: a multi-replica deployment multiplies them by replica count
// (upgrades.md assumes one api replica).
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
//
// Buckets live in a map plus an LRU list (front = most recently used): idle
// buckets (> 10 min) are pruned from the back, and the map is hard-capped
// at maxKeys — beyond it the least recently used bucket is evicted, so a
// client rotating source addresses (an IPv6 /64) cannot grow memory
// without bound.
type rateLimiter struct {
	mu      sync.Mutex
	perMin  float64
	burst   float64
	now     func() time.Time
	maxKeys int
	buckets map[string]*list.Element // Value is *bucket
	lru     *list.List
}

// maxRateLimiterKeys caps each limiter's bucket map.
const maxRateLimiterKeys = 100_000

// idleBucketTTL is how long an untouched bucket is kept; by then it has
// refilled to burst anyway, so dropping it changes nothing.
const idleBucketTTL = 10 * time.Minute

type bucket struct {
	key    string
	tokens float64
	last   time.Time
}

func newRateLimiter(perMin, burst int, now func() time.Time) *rateLimiter {
	return &rateLimiter{
		perMin: float64(perMin), burst: float64(burst), now: now,
		maxKeys: maxRateLimiterKeys, buckets: make(map[string]*list.Element), lru: list.New(),
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
	// GC from the LRU end: the back is the longest idle bucket.
	for e := l.lru.Back(); e != nil && t.Sub(e.Value.(*bucket).last) > idleBucketTTL; e = l.lru.Back() {
		l.remove(e)
	}
	var b *bucket
	if e, ok := l.buckets[key]; ok {
		l.lru.MoveToFront(e)
		b = e.Value.(*bucket)
	} else {
		b = &bucket{key: key, tokens: l.burst, last: t}
		l.buckets[key] = l.lru.PushFront(b)
		for l.lru.Len() > l.maxKeys {
			l.remove(l.lru.Back())
		}
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

func (l *rateLimiter) remove(e *list.Element) {
	delete(l.buckets, e.Value.(*bucket).key)
	l.lru.Remove(e)
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

// routeOptions tunes one authed route; see limitClass, deadline and
// noDeadline.
type routeOptions struct {
	class      string
	deadline   time.Duration
	noDeadline bool
}

type routeOption func(*routeOptions)

// limitClass charges the route to the named per-user class instead of
// the general user bucket.
func limitClass(name string) routeOption {
	return func(o *routeOptions) { o.class = name }
}

// deadline overrides the per-handler context deadline for one route.
func deadline(d time.Duration) routeOption {
	return func(o *routeOptions) { o.deadline = d }
}

// noDeadline marks a streaming route (SSE) that must run until the client
// disconnects or the server drains.
func noDeadline() routeOption {
	return func(o *routeOptions) { o.noDeadline = true }
}

// routeClasses reports the limiter class of every authed pattern registered
// by New (pinned by tests against the spec table).
func (s *server) routeClasses() map[string]string { return s.classOf }
