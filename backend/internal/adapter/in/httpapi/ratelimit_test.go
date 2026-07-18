package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiterBurstThenDeny(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	l := newRateLimiter(5, 3, clock)

	for i := 0; i < 3; i++ {
		if !l.allow("k") {
			t.Fatalf("expected call %d within burst to be allowed", i+1)
		}
	}
	if l.allow("k") {
		t.Fatal("expected call beyond burst to be denied")
	}
}

func TestRateLimiterRefillAfterTime(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	l := newRateLimiter(60, 2, clock) // 60/min = 1/sec

	if !l.allow("k") || !l.allow("k") {
		t.Fatal("expected burst of 2 to be allowed")
	}
	if l.allow("k") {
		t.Fatal("expected bucket to be empty")
	}

	// Advance simulated time by 1 minute: should refill up to burst (2).
	now = now.Add(time.Minute)
	if !l.allow("k") {
		t.Fatal("expected token to be available after refill")
	}
	if !l.allow("k") {
		t.Fatal("expected second refilled token to be available")
	}
	if l.allow("k") {
		t.Fatal("expected bucket to be empty again after consuming refill")
	}
}

func TestRateLimiterKeyIsolation(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	l := newRateLimiter(5, 1, clock)

	if !l.allow("ip-a") {
		t.Fatal("expected ip-a's first call to be allowed")
	}
	if l.allow("ip-a") {
		t.Fatal("expected ip-a's second call to be denied (burst exhausted)")
	}
	if !l.allow("ip-b") {
		t.Fatal("expected ip-b to have its own independent bucket")
	}
}

func TestRateLimiterGCPrunesIdleBuckets(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	l := newRateLimiter(5, 1, clock)

	l.allow("stale-key")
	if _, ok := l.buckets["stale-key"]; !ok {
		t.Fatal("expected bucket to exist after first call")
	}

	// Idle > 10 minutes and > 1 minute since lastGC triggers a prune on the
	// next call (for any key).
	now = now.Add(11 * time.Minute)
	l.allow("other-key")

	if _, ok := l.buckets["stale-key"]; ok {
		t.Fatal("expected stale-key's bucket to be pruned by GC")
	}
	if _, ok := l.buckets["other-key"]; !ok {
		t.Fatal("expected other-key's bucket to survive (it was just touched)")
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		want       string
	}{
		{name: "with port", remoteAddr: "203.0.113.5:54321", want: "203.0.113.5"},
		{name: "ipv6 with port", remoteAddr: "[2001:db8::1]:443", want: "2001:db8::1"},
		{name: "no port", remoteAddr: "203.0.113.5", want: "203.0.113.5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remoteAddr
			if got := clientIP(r); got != tt.want {
				t.Fatalf("clientIP(%q) = %q, want %q", tt.remoteAddr, got, tt.want)
			}
		})
	}
}

func TestRateLimitedHandler(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	l := newRateLimiter(5, 1, clock)
	s := &server{}

	calls := 0
	h := s.rateLimited(l, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.1:1"

	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK || calls != 1 {
		t.Fatalf("expected first call to pass through, got status %d calls %d", rec.Code, calls)
	}

	rec = httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "60" {
		t.Fatalf("expected Retry-After: 60, got %q", got)
	}
	if calls != 1 {
		t.Fatalf("expected handler not to be called when rate limited, calls=%d", calls)
	}
	e := decodeErr(t, rec)
	if e.Code != "rate_limited" {
		t.Fatalf("expected code rate_limited, got %q", e.Code)
	}
}
