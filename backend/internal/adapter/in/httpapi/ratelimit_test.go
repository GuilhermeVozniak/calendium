package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiterBurstThenDenyWithRetryAfter(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	l := newRateLimiter(5, 3, clock) // 5/min = one token every 12s

	for i := 0; i < 3; i++ {
		if ok, _ := l.allow("k"); !ok {
			t.Fatalf("expected call %d within burst to be allowed", i+1)
		}
	}
	ok, retry := l.allow("k")
	if ok {
		t.Fatal("expected call beyond burst to be denied")
	}
	if retry != 12*time.Second {
		t.Fatalf("retryAfter = %v, want 12s (ceil((1-0)/5 min))", retry)
	}
	now = now.Add(6 * time.Second) // half a token refilled → 6s left, still denied
	if ok, retry := l.allow("k"); ok || retry != 6*time.Second {
		t.Fatalf("after 6s: ok=%v retry=%v, want denied / 6s", ok, retry)
	}
}

func TestRateLimiterRetryAfterMinimumOneSecond(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	l := newRateLimiter(600, 1, clock) // 10 tokens/s
	l.allow("k")
	if ok, retry := l.allow("k"); ok || retry != time.Second {
		t.Fatalf("ok=%v retry=%v, want denied / 1s floor", ok, retry)
	}
}

func TestRateLimiterRefillAfterTime(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	l := newRateLimiter(60, 2, clock) // 60/min = 1/sec

	if ok, _ := l.allow("k"); !ok {
		t.Fatal("expected first burst token")
	}
	if ok, _ := l.allow("k"); !ok {
		t.Fatal("expected second burst token")
	}
	if ok, _ := l.allow("k"); ok {
		t.Fatal("expected bucket to be empty")
	}
	now = now.Add(time.Minute)
	if ok, _ := l.allow("k"); !ok {
		t.Fatal("expected token after refill")
	}
	if ok, _ := l.allow("k"); !ok {
		t.Fatal("expected second refilled token")
	}
	if ok, _ := l.allow("k"); ok {
		t.Fatal("expected bucket empty again")
	}
}

func TestRateLimiterKeyIsolation(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	l := newRateLimiter(5, 1, clock)

	if ok, _ := l.allow("ip-a"); !ok {
		t.Fatal("expected ip-a's first call to be allowed")
	}
	if ok, _ := l.allow("ip-a"); ok {
		t.Fatal("expected ip-a's second call to be denied (burst exhausted)")
	}
	if ok, _ := l.allow("ip-b"); !ok {
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

func TestRateLimiterKeyCapEvictsLeastRecentlyUsed(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	l := newRateLimiter(5, 1, clock)
	if l.maxKeys != maxRateLimiterKeys || maxRateLimiterKeys != 100_000 {
		t.Fatalf("maxKeys = %d (const %d), want 100000", l.maxKeys, maxRateLimiterKeys)
	}
	l.maxKeys = 3

	for _, k := range []string{"a", "b", "c"} {
		l.allow(k)
		now = now.Add(time.Second)
	}
	l.allow("a") // a is now the most recently used; b is the oldest
	now = now.Add(time.Second)
	l.allow("d")

	if len(l.buckets) != 3 || l.lru.Len() != 3 {
		t.Fatalf("buckets = %d, lru = %d, want both capped at 3", len(l.buckets), l.lru.Len())
	}
	if _, ok := l.buckets["b"]; ok {
		t.Fatal("b (least recently used) should have been evicted")
	}
	for _, k := range []string{"a", "c", "d"} {
		if _, ok := l.buckets[k]; !ok {
			t.Fatalf("%s should survive the eviction", k)
		}
	}
	// a kept its (empty) bucket: still refused.
	if ok, _ := l.allow("a"); ok {
		t.Fatal("a's bucket was reset; only the LRU key may be evicted")
	}

	// A flood of fresh keys never grows the map past the cap.
	for i := 0; i < 1000; i++ {
		l.allow("flood-" + time.Duration(i).String())
	}
	if len(l.buckets) != 3 || l.lru.Len() != 3 {
		t.Fatalf("after flood: buckets = %d, lru = %d", len(l.buckets), l.lru.Len())
	}
}

func TestRateLimitedHandler(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	l := newRateLimiter(5, 1, clock)
	s := newHarness(t).server()

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
	if got := rec.Header().Get("Retry-After"); got != "12" {
		t.Fatalf("expected Retry-After: 12 (5/min, empty bucket), got %q", got)
	}
	if calls != 1 {
		t.Fatalf("expected handler not to be called when rate limited, calls=%d", calls)
	}
	e := decodeErr(t, rec)
	if e.Code != "rate_limited" {
		t.Fatalf("expected code rate_limited, got %q", e.Code)
	}
}
