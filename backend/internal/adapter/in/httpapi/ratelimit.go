package httpapi

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// rateLimiter is an in-memory per-key token bucket: capacity `burst`,
// refilled at `perMin` tokens/minute. Zero-dependency abuse protection for
// the public (unauthenticated) endpoints; per-process by design — a
// horizontal deployment multiplies the effective limit by replica count,
// which is acceptable for M2.4.
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

// allow consumes one token for key, reporting whether the call may proceed.
func (l *rateLimiter) allow(key string) bool {
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
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// clientIP extracts the caller address. RemoteAddr only — proxy headers are
// spoofable and this API terminates TLS itself in the reference deployment.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// rateLimited wraps a public handler: 429 {"error":{"code":"rate_limited"}}
// with Retry-After: 60 when the caller's bucket is empty.
func (s *server) rateLimited(l *rateLimiter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(clientIP(r)) {
			w.Header().Set("Retry-After", "60")
			writeJSON(w, http.StatusTooManyRequests, errorBody{Error: errorDetail{
				Code: "rate_limited", Message: "too many requests, slow down",
			}})
			return
		}
		next(w, r)
	}
}
