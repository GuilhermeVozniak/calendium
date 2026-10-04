package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func readyzStatus(t *testing.T, h *harness) (int, string) {
	t.Helper()
	rec := h.anon(http.MethodGet, "/readyz", nil)
	var body struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return rec.Code, body.Status
}

func TestReadyz(t *testing.T) {
	t.Run("ping ok", func(t *testing.T) {
		h := newHarness(t)
		h.deps.Ready = func(context.Context) error { return nil }
		if code, status := readyzStatus(t, h); code != http.StatusOK || status != "ok" {
			t.Fatalf("%d %s, want 200 ok", code, status)
		}
	})
	t.Run("nil Ready is ok (tests, no DB)", func(t *testing.T) {
		h := newHarness(t)
		if code, _ := readyzStatus(t, h); code != http.StatusOK {
			t.Fatalf("code = %d", code)
		}
	})
	t.Run("ping error is 503 db_unavailable", func(t *testing.T) {
		h := newHarness(t)
		h.deps.Ready = func(context.Context) error { return errors.New("dial tcp: refused") }
		if code, status := readyzStatus(t, h); code != http.StatusServiceUnavailable || status != "db_unavailable" {
			t.Fatalf("%d %s, want 503 db_unavailable", code, status)
		}
	})
	t.Run("draining is 503 before the ping", func(t *testing.T) {
		h := newHarness(t)
		drain := make(chan struct{})
		close(drain)
		h.deps.Drain = drain
		var pinged atomic.Bool
		h.deps.Ready = func(context.Context) error { pinged.Store(true); return nil }
		code, status := readyzStatus(t, h)
		if code != http.StatusServiceUnavailable || status != "draining" || pinged.Load() {
			t.Fatalf("%d %s pinged=%v, want 503 draining without a ping", code, status, pinged.Load())
		}
	})
	t.Run("open drain channel is still ready", func(t *testing.T) {
		h := newHarness(t)
		h.deps.Drain = make(chan struct{})
		if code, status := readyzStatus(t, h); code != http.StatusOK || status != "ok" {
			t.Fatalf("%d %s, want 200 ok", code, status)
		}
	})
	t.Run("ping receives a bounded context", func(t *testing.T) {
		h := newHarness(t)
		var hasDeadline atomic.Bool
		h.deps.Ready = func(ctx context.Context) error {
			_, ok := ctx.Deadline()
			hasDeadline.Store(ok)
			return nil
		}
		readyzStatus(t, h)
		if !hasDeadline.Load() {
			t.Fatal("Ready must be called with a deadline")
		}
	})
	// Review Focus 5: a ping that ignores its context still answers in time.
	t.Run("hung ping times out to 503", func(t *testing.T) {
		prev := readyzPingTimeout
		readyzPingTimeout = 50 * time.Millisecond
		t.Cleanup(func() { readyzPingTimeout = prev })
		h := newHarness(t)
		h.deps.Ready = func(context.Context) error { time.Sleep(500 * time.Millisecond); return nil }
		start := time.Now()
		code, status := readyzStatus(t, h)
		if code != http.StatusServiceUnavailable || status != "db_unavailable" {
			t.Fatalf("%d %s, want 503 db_unavailable", code, status)
		}
		if time.Since(start) > 300*time.Millisecond {
			t.Fatalf("readyz took %v; the ping timeout was not honoured", time.Since(start))
		}
	})
}
