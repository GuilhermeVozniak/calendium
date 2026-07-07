package stripeapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func sign(secret string, ts int64, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.%s", ts, payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerifySignature(t *testing.T) {
	secret := "whsec_test"
	payload := []byte(`{"id":"evt_1"}`)
	now := time.Unix(1_700_000_000, 0)
	ts := now.Unix()

	header := fmt.Sprintf("t=%d,v1=%s", ts, sign(secret, ts, payload))
	if err := verifySignature(payload, header, secret, now); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if err := verifySignature(payload, header, "whsec_other", now); err == nil {
		t.Fatal("signature with wrong secret accepted")
	}
	if err := verifySignature(payload, header, secret, now.Add(6*time.Minute)); err == nil {
		t.Fatal("stale signature accepted")
	}
	// Extra v1 candidates: any match passes (key-rotation behavior).
	rotated := fmt.Sprintf("t=%d,v1=%s,v1=%s", ts, sign("old", ts, payload), sign(secret, ts, payload))
	if err := verifySignature(payload, rotated, secret, now); err != nil {
		t.Fatalf("rotated header rejected: %v", err)
	}
}

func TestParseWebhookCheckoutSessionCompleted(t *testing.T) {
	c := NewClient("sk_test", "whsec_test", "price_annual", nil)

	t.Run("uses metadata.user_id when present", func(t *testing.T) {
		payload := []byte(`{
			"id": "evt_cs1", "type": "checkout.session.completed",
			"data": {"object": {
				"customer": "cus_1", "subscription": "sub_1",
				"client_reference_id": "user-ref",
				"metadata": {"user_id": "user-meta"}
			}}
		}`)
		now := time.Now()
		header := fmt.Sprintf("t=%d,v1=%s", now.Unix(), sign("whsec_test", now.Unix(), payload))

		ev, err := c.ParseWebhook(payload, header)
		if err != nil {
			t.Fatalf("ParseWebhook: %v", err)
		}
		if ev.ID != "evt_cs1" || ev.Type != "checkout.session.completed" {
			t.Fatalf("id/type not normalized: %+v", ev)
		}
		if ev.CustomerID != "cus_1" || ev.SubscriptionID != "sub_1" {
			t.Fatalf("ids not normalized: %+v", ev)
		}
		if ev.UserID != "user-meta" {
			t.Fatalf("UserID = %q, want metadata.user_id value", ev.UserID)
		}
		if ev.Status != "" {
			t.Fatalf("Status = %q, want empty (left to the paired subscription.created event)", ev.Status)
		}
	})

	t.Run("falls back to client_reference_id when metadata.user_id is empty", func(t *testing.T) {
		payload := []byte(`{
			"id": "evt_cs2", "type": "checkout.session.completed",
			"data": {"object": {
				"customer": "cus_2", "subscription": "sub_2",
				"client_reference_id": "user-ref"
			}}
		}`)
		now := time.Now()
		header := fmt.Sprintf("t=%d,v1=%s", now.Unix(), sign("whsec_test", now.Unix(), payload))

		ev, err := c.ParseWebhook(payload, header)
		if err != nil {
			t.Fatalf("ParseWebhook: %v", err)
		}
		if ev.UserID != "user-ref" {
			t.Fatalf("UserID = %q, want client_reference_id fallback", ev.UserID)
		}
	})
}

// TestParseWebhookInvoiceEvents pins the invoice.paid → active and
// invoice.payment_failed → past_due normalization; current_period_end is
// intentionally left to the paired subscription.* event.
func TestParseWebhookInvoiceEvents(t *testing.T) {
	c := NewClient("sk_test", "whsec_test", "price_annual", nil)

	tests := []struct {
		name       string
		eventType  string
		wantStatus domain.SubscriptionStatus
	}{
		{"invoice.paid settles to active", "invoice.paid", domain.SubscriptionActive},
		{"invoice.payment_failed settles to past_due", "invoice.payment_failed", domain.SubscriptionPastDue},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload := []byte(fmt.Sprintf(`{
				"id": "evt_inv", "type": %q,
				"data": {"object": {
					"customer": "cus_3", "subscription": "sub_3", "period_end": 1700100000
				}}
			}`, tc.eventType))
			now := time.Now()
			header := fmt.Sprintf("t=%d,v1=%s", now.Unix(), sign("whsec_test", now.Unix(), payload))

			ev, err := c.ParseWebhook(payload, header)
			if err != nil {
				t.Fatalf("ParseWebhook: %v", err)
			}
			if ev.CustomerID != "cus_3" || ev.SubscriptionID != "sub_3" {
				t.Fatalf("ids not normalized: %+v", ev)
			}
			if ev.Status != tc.wantStatus {
				t.Fatalf("Status = %q, want %q", ev.Status, tc.wantStatus)
			}
			if ev.CurrentPeriodEnd != nil {
				t.Fatalf("CurrentPeriodEnd = %v, want nil (left to subscription.* events)", ev.CurrentPeriodEnd)
			}
		})
	}
}

func TestParseWebhookNormalizesSubscription(t *testing.T) {
	c := NewClient("sk_test", "whsec_test", "price_annual", nil)
	payload := []byte(`{
		"id": "evt_42", "type": "customer.subscription.updated",
		"data": {"object": {
			"id": "sub_1", "customer": "cus_1", "status": "past_due",
			"current_period_end": 1700000000, "cancel_at_period_end": true,
			"trial_end": 0, "metadata": {"user_id": "user-9"}
		}}
	}`)
	now := time.Now()
	header := fmt.Sprintf("t=%d,v1=%s", now.Unix(), sign("whsec_test", now.Unix(), payload))

	ev, err := c.ParseWebhook(payload, header)
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if ev.ID != "evt_42" || ev.SubscriptionID != "sub_1" || ev.CustomerID != "cus_1" || ev.UserID != "user-9" {
		t.Fatalf("ids not normalized: %+v", ev)
	}
	if ev.Status != domain.SubscriptionPastDue || !ev.CancelAtPeriodEnd {
		t.Fatalf("state not normalized: %+v", ev)
	}
	if ev.CurrentPeriodEnd == nil || ev.CurrentPeriodEnd.Unix() != 1700000000 || ev.TrialEndsAt != nil {
		t.Fatalf("timestamps not normalized: %+v", ev)
	}
}
