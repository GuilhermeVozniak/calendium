package httpapi

// instant_replies_test.go covers GET /v1/mail/threads/{id}/instant-replies:
// the 200 response shape, a foreign/missing thread mapped to 404, and AI
// not configured mapped to 503. Ownership/freshness/budget logic all live
// service-side (ai_instant_replies_test.go); this only checks the handler
// forwards args and maps errors via the existing statusFor table.

import (
	"encoding/json"
	"net/http"
	"testing"

	"calendium/backend/internal/domain"
)

func TestHandleInstantReplies(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h := newHarness(t)
		h.ai.instantRepliesRet = []string{"Sounds good", "Can't make it", "What time works?"}
		rec := h.authed(http.MethodGet, "/v1/mail/threads/th1/instant-replies", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.ai.gotInstantRepliesThreadID != "th1" {
			t.Fatalf("gotInstantRepliesThreadID = %q, want th1", h.ai.gotInstantRepliesThreadID)
		}
		if h.ai.gotInstantRepliesUserID != defaultUserID {
			t.Fatalf("gotInstantRepliesUserID = %q, want %q", h.ai.gotInstantRepliesUserID, defaultUserID)
		}
		var got struct {
			Replies []string `json:"replies"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got.Replies) != 3 || got.Replies[0] != "Sounds good" {
			t.Fatalf("replies = %+v", got.Replies)
		}
	})

	t.Run("foreign thread is 404", func(t *testing.T) {
		h := newHarness(t)
		h.ai.instantRepliesErr = domain.ErrNotFound
		rec := h.authed(http.MethodGet, "/v1/mail/threads/not-mine/instant-replies", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "not_found" {
			t.Fatalf("code = %q, want not_found", got.Code)
		}
	})

	t.Run("no AI key configured is 503", func(t *testing.T) {
		h := newHarness(t)
		h.ai.instantRepliesErr = domain.ErrAIUnavailable
		rec := h.authed(http.MethodGet, "/v1/mail/threads/th1/instant-replies", nil)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "ai_unavailable" {
			t.Fatalf("code = %q, want ai_unavailable", got.Code)
		}
	})

	t.Run("budget exhausted is 429", func(t *testing.T) {
		h := newHarness(t)
		h.ai.instantRepliesErr = domain.ErrRateLimited
		rec := h.authed(http.MethodGet, "/v1/mail/threads/th1/instant-replies", nil)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "rate_limited" {
			t.Fatalf("code = %q, want rate_limited", got.Code)
		}
	})
}
