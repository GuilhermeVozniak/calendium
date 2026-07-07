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
