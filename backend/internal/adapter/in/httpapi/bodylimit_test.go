package httpapi

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// jsonOfSize returns a syntactically valid JSON object whose encoded size is
// exactly n bytes: {"pad":"xxxx..."}.
func jsonOfSize(n int) string {
	const frame = `{"pad":""}`
	return `{"pad":"` + strings.Repeat("x", n-len(frame)) + `"}`
}

func TestBodyLimits(t *testing.T) {
	t.Run("1 MiB + 1 on a default route is 413", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPut, "/v1/prefs", strings.NewReader(jsonOfSize(maxBodyBytes+1)))
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413 (body=%s)", rec.Code, rec.Body.String())
		}
		if e := decodeErr(t, rec); e.Code != "payload_too_large" {
			t.Fatalf("code = %q", e.Code)
		}
	})
	t.Run("exactly 1 MiB on a default route is accepted by the decoder", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPut, "/v1/prefs", strings.NewReader(jsonOfSize(maxBodyBytes)))
		if rec.Code == http.StatusRequestEntityTooLarge {
			t.Fatalf("a body of exactly maxBodyBytes must not be 413 (body=%s)", rec.Body.String())
		}
	})
	t.Run("draft create accepts 2 MiB", func(t *testing.T) {
		h := newHarness(t)
		body := fmt.Sprintf(`{"accountId":"a1","subject":"hi","bodyHtml":%q}`, strings.Repeat("y", 2<<20))
		rec := h.authed(http.MethodPost, "/v1/mail/drafts", strings.NewReader(body))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
	})
	t.Run("draft update rejects 10 MiB + 1", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPut, "/v1/mail/drafts/d1", strings.NewReader(jsonOfSize(maxDraftBodyBytes+1)))
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413", rec.Code)
		}
	})
	t.Run("public booking over 16 KiB is 413 payload_too_large", func(t *testing.T) {
		h := newHarness(t)
		rec := h.anon(http.MethodPost, "/v1/public/booking/demo/bookings", strings.NewReader(jsonOfSize(publicBodyLimit+1)))
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413", rec.Code)
		}
		if e := decodeErr(t, rec); e.Code != "payload_too_large" || e.RequestID == "" {
			t.Fatalf("envelope = %+v, want payload_too_large with requestId", e)
		}
	})
	t.Run("paddle webhook over 1 MiB is 413", func(t *testing.T) {
		h := newHarness(t)
		req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/paddle", bytes.NewReader(bytes.Repeat([]byte("z"), (1<<20)+1)))
		req.Header.Set("Paddle-Signature", "ts=1;h1=ab")
		rec := httptest.NewRecorder()
		h.handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413 (body=%s)", rec.Code, rec.Body.String())
		}
		if e := decodeErr(t, rec); e.Code != "payload_too_large" {
			t.Fatalf("code = %q", e.Code)
		}
		if h.billing.webhookCalls != 0 {
			t.Fatal("oversized webhook must not reach Billing")
		}
	})
	t.Run("paddle webhook of exactly 1 MiB reaches Billing", func(t *testing.T) {
		h := newHarness(t)
		req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/paddle", bytes.NewReader(bytes.Repeat([]byte("z"), 1<<20)))
		req.Header.Set("Paddle-Signature", "ts=1;h1=ab")
		rec := httptest.NewRecorder()
		h.handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || h.billing.webhookCalls != 1 {
			t.Fatalf("status = %d calls = %d, want 200 / 1", rec.Code, h.billing.webhookCalls)
		}
	})
}
