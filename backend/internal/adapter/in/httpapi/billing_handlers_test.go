package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
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
	end := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	h.billing.subRet = domain.Subscription{Status: domain.SubscriptionPaused, Plan: domain.PlanAnnual, PriceUSD: 50, CurrentPeriodEnd: &end, BillingCustomerID: "ctm_secret", BillingSubscriptionID: "sub_secret"}
	rec := h.authed(http.MethodGet, "/v1/billing/subscription", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(raw["status"]) != `"paused"` {
		t.Fatalf("status = %s, want paused", raw["status"])
	}
	for _, k := range []string{"billingCustomerId", "billingSubscriptionId", "BillingCustomerID", "userId"} {
		if _, ok := raw[k]; ok {
			t.Fatalf("provider ids must never be serialized: found %q", k)
		}
	}
}

func TestHandleCreateCheckout(t *testing.T) {
	t.Run("no request body, returns url", func(t *testing.T) {
		h := newHarness(t)
		h.billing.checkoutURL = "https://app/checkout?_ptxn=txn_1"
		rec := h.authed(http.MethodPost, "/v1/billing/checkout", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["url"] != h.billing.checkoutURL {
			t.Fatalf("url = %q", body["url"])
		}
		if h.billing.gotCheckoutUserID != defaultUserID {
			t.Fatalf("userID = %q, want %q", h.billing.gotCheckoutUserID, defaultUserID)
		}
	})
	t.Run("client-supplied URLs are ignored, not an error", func(t *testing.T) {
		h := newHarness(t)
		h.billing.checkoutURL = "https://app/checkout"
		rec := h.authed(http.MethodPost, "/v1/billing/checkout", jsonBody(t, map[string]string{"successUrl": "https://evil"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})
	t.Run("already subscribed is 409", func(t *testing.T) {
		h := newHarness(t)
		h.billing.checkoutErr = domain.ErrAlreadySubscribed
		rec := h.authed(http.MethodPost, "/v1/billing/checkout", nil)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "already_subscribed" {
			t.Fatalf("code = %q, want already_subscribed", got.Code)
		}
	})
	t.Run("provider failure is 502", func(t *testing.T) {
		h := newHarness(t)
		h.billing.checkoutErr = domain.ErrBillingUnavailable
		rec := h.authed(http.MethodPost, "/v1/billing/checkout", nil)
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "billing_unavailable" {
			t.Fatalf("code = %q, want billing_unavailable", got.Code)
		}
	})
	t.Run("self-hosted is 501", func(t *testing.T) {
		h := newHarness(t)
		h.billing.checkoutErr = domain.ErrSelfHosted
		rec := h.authed(http.MethodPost, "/v1/billing/checkout", nil)
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want 501", rec.Code)
		}
	})
}

func TestHandleCreatePortal(t *testing.T) {
	t.Run("returns the three urls", func(t *testing.T) {
		h := newHarness(t)
		h.billing.portalURLs = port.PortalURLs{Overview: "https://p/o", Cancel: "https://p/c", UpdatePayment: "https://p/u"}
		rec := h.authed(http.MethodPost, "/v1/billing/portal", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["overviewUrl"] != "https://p/o" || body["cancelUrl"] != "https://p/c" || body["updatePaymentUrl"] != "https://p/u" {
			t.Fatalf("body = %v", body)
		}
		if h.billing.gotPortalUserID != defaultUserID {
			t.Fatalf("userID = %q", h.billing.gotPortalUserID)
		}
	})
	t.Run("empty cancel/update are serialized as empty strings", func(t *testing.T) {
		h := newHarness(t)
		h.billing.portalURLs = port.PortalURLs{Overview: "https://p/o"}
		rec := h.authed(http.MethodPost, "/v1/billing/portal", nil)
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if v, ok := body["cancelUrl"]; !ok || v != "" {
			t.Fatalf("cancelUrl = %q (present=%v), want present and empty", v, ok)
		}
	})
	t.Run("no billing profile is 400", func(t *testing.T) {
		h := newHarness(t)
		h.billing.portalErr = domain.ErrNoBillingProfile
		rec := h.authed(http.MethodPost, "/v1/billing/portal", nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "no_billing_profile" {
			t.Fatalf("code = %q, want no_billing_profile", got.Code)
		}
	})
}

func TestHandlePaddleWebhook(t *testing.T) {
	post := func(h *harness, body []byte, sig string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/paddle", bytes.NewReader(body))
		if sig != "" {
			req.Header.Set("Paddle-Signature", sig)
		}
		rec := httptest.NewRecorder()
		h.handler().ServeHTTP(rec, req)
		return rec
	}

	t.Run("forwards raw body and Paddle-Signature, answers received", func(t *testing.T) {
		h := newHarness(t)
		body := []byte(`{"event_type":"subscription.updated"}`)
		rec := post(h, body, "ts=1;h1=ab")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		var got map[string]bool
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || !got["received"] {
			t.Fatalf("body = %s", rec.Body.String())
		}
		if h.billing.gotWebhookSig != "ts=1;h1=ab" || !bytes.Equal(h.billing.gotWebhookPayload, body) {
			t.Fatalf("forwarded sig=%q payload=%s", h.billing.gotWebhookSig, h.billing.gotWebhookPayload)
		}
	})
	t.Run("bad signature is 401", func(t *testing.T) {
		h := newHarness(t)
		h.billing.webhookErr = domain.ErrUnauthorized
		rec := post(h, []byte(`{}`), "bad")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})
	t.Run("malformed envelope is 400", func(t *testing.T) {
		h := newHarness(t)
		h.billing.webhookErr = domain.ErrValidation
		rec := post(h, []byte(`{}`), "ts=1;h1=ab")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
	t.Run("oversized body rejected before HandleWebhook runs", func(t *testing.T) {
		h := newHarness(t)
		big := bytes.Repeat([]byte("a"), (1<<20)+1024)
		rec := post(h, big, "ts=1;h1=ab")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		if h.billing.webhookCalls != 0 {
			t.Fatalf("HandleWebhook called = %d, want 0", h.billing.webhookCalls)
		}
	})
	t.Run("old stripe route is gone", func(t *testing.T) {
		h := newHarness(t)
		req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/stripe", bytes.NewReader([]byte(`{}`)))
		rec := httptest.NewRecorder()
		h.handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 for the removed Stripe route", rec.Code)
		}
	})
}
