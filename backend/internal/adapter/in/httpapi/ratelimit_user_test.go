package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"calendium/backend/internal/port"
)

func TestDefaultRateLimits(t *testing.T) {
	d := DefaultRateLimits()
	if d != (RateLimits{PublicReadPerMin: 60, PublicWritePerMin: 5, UserPerMin: 600, MutateHeavyPerMin: 30, SearchPerMin: 120}) {
		t.Fatalf("DefaultRateLimits() = %+v", d)
	}
}

func authedReq(method, target, body string) *http.Request {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	req.Header.Set("Authorization", "Bearer "+defaultToken)
	return req
}

// TestMutateHeavy31stCallIs429 drives POST /v1/mail/threads/zero (class
// mutate_heavy, 30/min burst 30) through one handler instance.
func TestMutateHeavy31stCallIs429(t *testing.T) {
	h := newHarness(t)
	handler := h.handler()
	body := `{"olderThan":"2026-01-01T00:00:00Z"}`
	for i := 0; i < 30; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, authedReq(http.MethodPost, "/v1/mail/threads/zero", body))
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d: status = %d (body=%s)", i+1, rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authedReq(http.MethodPost, "/v1/mail/threads/zero", body))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("31st call: status = %d, want 429", rec.Code)
	}
	retry, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || retry < 1 {
		t.Fatalf("Retry-After = %q, want an integer >= 1", rec.Header().Get("Retry-After"))
	}
	e := decodeErr(t, rec)
	if e.Code != "rate_limited" || e.RequestID == "" {
		t.Fatalf("envelope = %+v", e)
	}
	// The general user class is untouched by the mutate_heavy bucket.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authedReq(http.MethodGet, "/v1/me", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/me after mutate_heavy exhaustion: %d", rec.Code)
	}
}

func TestUserLimitsAreIsolatedPerUser(t *testing.T) {
	h := newHarness(t)
	h.deps.RateLimits = RateLimits{PublicReadPerMin: 60, PublicWritePerMin: 5, UserPerMin: 2, MutateHeavyPerMin: 30, SearchPerMin: 120}
	h.verifier.tokens["other-token"] = port.Identity{Subject: "user_2", Email: "other@example.com"}
	handler := h.handler()
	call := func(token string) int {
		req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}
	call(defaultToken)
	call(defaultToken)
	if got := call(defaultToken); got != http.StatusTooManyRequests {
		t.Fatalf("3rd call for user_1 = %d, want 429", got)
	}
	// user_2 shares the EnsureUser fake, so swap the returned user for isolation.
	h.users.ensureRet.ID = "user_2"
	if got := call("other-token"); got != http.StatusOK {
		t.Fatalf("user_2's first call = %d, want 200 (own bucket)", got)
	}
}

func TestSearchClass121stCallIs429(t *testing.T) {
	h := newHarness(t)
	handler := h.handler()
	for i := 0; i < 120; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, authedReq(http.MethodGet, "/v1/search?q=x", ""))
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d: %d (body=%s)", i+1, rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authedReq(http.MethodGet, "/v1/search?q=x", ""))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("121st search = %d, want 429", rec.Code)
	}
}

// TestActorChargedUnderActAs: 30 delegated bulk-actions (mutate_heavy) by
// the assistant exhaust the ASSISTANT's bucket — its own 31st direct call
// is refused — proving the principal is never the limiter key.
func TestActorChargedUnderActAs(t *testing.T) {
	h, _ := delegHarness(t)
	handler := h.handler()
	body := `{"threadIds":["t1"],"action":"archive"}`
	for i := 0; i < 30; i++ {
		req := authedReq(http.MethodPost, "/v1/mail/threads/bulk-actions", body)
		req.Header.Set(actAsHeader, "principal_1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("delegated call %d: %d (body=%s)", i+1, rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authedReq(http.MethodPost, "/v1/mail/threads/bulk-actions", body))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("assistant's own 31st call = %d, want 429 (actor is charged)", rec.Code)
	}
}

func TestZeroDisablesClass(t *testing.T) {
	h := newHarness(t)
	h.deps.RateLimits = RateLimits{PublicReadPerMin: 60, PublicWritePerMin: 5, UserPerMin: 0, MutateHeavyPerMin: 0, SearchPerMin: 120}
	handler := h.handler()
	for i := 0; i < 700; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, authedReq(http.MethodGet, "/v1/me", ""))
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d with user class disabled: %d", i+1, rec.Code)
		}
	}
}

func TestPublicLimitsUnchanged(t *testing.T) {
	h := newHarness(t)
	handler := h.handler()
	for i := 0; i < 30; i++ {
		req := httptest.NewRequest(http.MethodGet, "/v1/public/booking/demo", nil)
		req.RemoteAddr = "198.51.100.1:1"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("public read call %d of burst 30 was limited", i+1)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/public/booking/demo", nil)
	req.RemoteAddr = "198.51.100.1:1"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("31st public read = %d, want 429", rec.Code)
	}
}

// TestAllClassedRoutesTagged pins the spec's class table against the route
// registrations so a renamed pattern cannot silently fall back to "user".
func TestAllClassedRoutesTagged(t *testing.T) {
	want := map[string]string{
		"POST /v1/mail/drafts/{id}/send":        classMutateHeavy,
		"POST /v1/mail/threads/bulk-actions":    classMutateHeavy,
		"POST /v1/mail/threads/zero":            classMutateHeavy,
		"POST /v1/calendar-subscriptions":       classMutateHeavy,
		"POST /v1/teams/{id}/invitations":       classMutateHeavy,
		"POST /v1/booking-links":                classMutateHeavy,
		"POST /v1/polls":                        classMutateHeavy,
		"POST /v1/mail/threads/{id}/share":      classMutateHeavy,
		"POST /v1/ai/compose":                   classMutateHeavy,
		"POST /v1/ai/ask":                       classMutateHeavy,
		"POST /v1/ai/event-proposal":            classMutateHeavy,
		"GET /v1/search":                        classSearch,
		"GET /v1/mail/attachments":              classSearch,
		"GET /v1/places/autocomplete":           classSearch,
		"GET /v1/me":                            classUser,
		"GET /v1/mail/attachments/{id}/content": classUser,
	}
	s, _ := build(newHarness(t).deps)
	got := s.routeClasses()
	for pattern, class := range want {
		if got[pattern] != class {
			t.Errorf("%s: class %q, want %q", pattern, got[pattern], class)
		}
	}
}
