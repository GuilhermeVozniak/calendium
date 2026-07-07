package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"calendium/backend/internal/domain"
)

func TestHandleMe(t *testing.T) {
	h := newHarness(t)
	rec := h.authed(http.MethodGet, "/v1/me", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var got domain.User
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != defaultUserID {
		t.Fatalf("id = %q, want %q", got.ID, defaultUserID)
	}
	if h.users.ensureCalls == 0 {
		t.Fatalf("EnsureUser was not called")
	}
}

func TestHandleGetSubscription(t *testing.T) {
	h := newHarness(t)
	h.billing.subRet = domain.Subscription{Status: domain.SubscriptionActive}
	rec := h.authed(http.MethodGet, "/v1/billing/subscription", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var got domain.Subscription
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != domain.SubscriptionActive {
		t.Fatalf("status = %q, want active", got.Status)
	}
}

func TestHandleCreateCheckout(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h := newHarness(t)
		h.billing.checkoutURL = "https://checkout.stripe/x"
		rec := h.authed(http.MethodPost, "/v1/billing/checkout", jsonBody(t, map[string]string{"successUrl": "https://a", "cancelUrl": "https://b"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["url"] != h.billing.checkoutURL {
			t.Fatalf("url = %q, want %q", body["url"], h.billing.checkoutURL)
		}
		if h.billing.gotCheckoutSuccessURL != "https://a" || h.billing.gotCheckoutCancelURL != "https://b" {
			t.Fatalf("success/cancel = %q/%q, want https://a / https://b", h.billing.gotCheckoutSuccessURL, h.billing.gotCheckoutCancelURL)
		}
	})

	t.Run("self-hosted disables checkout", func(t *testing.T) {
		h := newHarness(t)
		h.billing.checkoutErr = domain.ErrSelfHosted
		rec := h.authed(http.MethodPost, "/v1/billing/checkout", jsonBody(t, map[string]string{"successUrl": "https://a", "cancelUrl": "https://b"}))
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want 501 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "self_hosted" {
			t.Fatalf("code = %q, want self_hosted", got.Code)
		}
	})

	t.Run("malformed JSON rejected", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/billing/checkout", strings.NewReader("{"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
}

func TestHandleCreatePortal(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h := newHarness(t)
		h.billing.portalURL = "https://billing.stripe/portal"
		rec := h.authed(http.MethodPost, "/v1/billing/portal", jsonBody(t, map[string]string{"returnUrl": "https://back"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["url"] != h.billing.portalURL {
			t.Fatalf("url = %q, want %q", body["url"], h.billing.portalURL)
		}
		if h.billing.gotPortalReturn != "https://back" {
			t.Fatalf("gotPortalReturn = %q, want https://back", h.billing.gotPortalReturn)
		}
	})

	t.Run("self-hosted", func(t *testing.T) {
		h := newHarness(t)
		h.billing.portalErr = domain.ErrSelfHosted
		rec := h.authed(http.MethodPost, "/v1/billing/portal", jsonBody(t, map[string]string{"returnUrl": "https://back"}))
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want 501 (body=%s)", rec.Code, rec.Body.String())
		}
	})
}

func TestHandleStripeWebhook(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h := newHarness(t)
		body := []byte(`{"type":"checkout.session.completed"}`)
		req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/stripe", bytes.NewReader(body))
		req.Header.Set("Stripe-Signature", "sig")
		rec := httptest.NewRecorder()
		h.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		var got map[string]bool
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !got["received"] {
			t.Fatalf("received = %v, want true", got)
		}
		if h.billing.gotWebhookSig != "sig" {
			t.Fatalf("gotWebhookSig = %q, want sig", h.billing.gotWebhookSig)
		}
		if !bytes.Equal(h.billing.gotWebhookPayload, body) {
			t.Fatalf("gotWebhookPayload = %s, want %s", h.billing.gotWebhookPayload, body)
		}
	})

	t.Run("bad signature rejected", func(t *testing.T) {
		h := newHarness(t)
		h.billing.webhookErr = domain.ErrValidation
		req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/stripe", bytes.NewReader([]byte(`{}`)))
		req.Header.Set("Stripe-Signature", "bad")
		rec := httptest.NewRecorder()
		h.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "validation_failed" {
			t.Fatalf("code = %q, want validation_failed", got.Code)
		}
	})

	t.Run("oversized body rejected before HandleWebhook runs", func(t *testing.T) {
		h := newHarness(t)
		big := bytes.Repeat([]byte("a"), (1<<20)+1024)
		req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/stripe", bytes.NewReader(big))
		req.Header.Set("Stripe-Signature", "sig")
		rec := httptest.NewRecorder()
		h.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "validation_failed" {
			t.Fatalf("code = %q, want validation_failed", got.Code)
		}
		if h.billing.webhookCalls != 0 {
			t.Fatalf("HandleWebhook called = %d, want 0", h.billing.webhookCalls)
		}
	})
}
