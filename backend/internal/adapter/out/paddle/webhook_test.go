package paddle

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// sign computes HMAC-SHA256(secret, "<ts>:<payload>") hex — the Paddle scheme.
func sign(secret string, ts int64, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d:%s", ts, payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func header(ts int64, h1 ...string) string {
	parts := []string{fmt.Sprintf("ts=%d", ts)}
	for _, h := range h1 {
		parts = append(parts, "h1="+h)
	}
	return strings.Join(parts, ";")
}

func TestVerifySignature(t *testing.T) {
	secret := "pdl_ntfset_secret"
	payload := []byte(`{"event_id":"evt_1"}`)
	now := time.Unix(1_700_000_000, 0)
	ts := now.Unix()

	t.Run("valid", func(t *testing.T) {
		if err := verifySignature(payload, header(ts, sign(secret, ts, payload)), secret, now); err != nil {
			t.Fatalf("valid signature rejected: %v", err)
		}
	})
	t.Run("wrong secret", func(t *testing.T) {
		if err := verifySignature(payload, header(ts, sign(secret, ts, payload)), "other", now); err == nil {
			t.Fatal("wrong secret accepted")
		}
	})
	t.Run("empty configured secret is refused even with a matching mac", func(t *testing.T) {
		if err := verifySignature(payload, header(ts, sign("", ts, payload)), "", now); err == nil {
			t.Fatal("empty secret must be refused")
		}
	})
	t.Run("tampered body", func(t *testing.T) {
		if err := verifySignature([]byte(`{"event_id":"evt_2"}`), header(ts, sign(secret, ts, payload)), secret, now); err == nil {
			t.Fatal("tampered body accepted")
		}
	})
	t.Run("multiple h1: any match passes, including when the first is stale", func(t *testing.T) {
		h := header(ts, sign("old-secret", ts, payload), sign(secret, ts, payload))
		if err := verifySignature(payload, h, secret, now); err != nil {
			t.Fatalf("rotated header rejected: %v", err)
		}
	})
	t.Run("tolerance window", func(t *testing.T) {
		h := header(ts, sign(secret, ts, payload))
		for _, tc := range []struct {
			name  string
			clock time.Time
			ok    bool
		}{
			{"4m late", now.Add(4 * time.Minute), true},
			{"4m early (skewed sender clock)", now.Add(-4 * time.Minute), true},
			{"6m late", now.Add(6 * time.Minute), false},
			{"6m early", now.Add(-6 * time.Minute), false},
		} {
			err := verifySignature(payload, h, secret, tc.clock)
			if tc.ok && err != nil {
				t.Fatalf("%s: rejected: %v", tc.name, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("%s: accepted outside the 5m tolerance", tc.name)
			}
		}
	})
	t.Run("malformed headers", func(t *testing.T) {
		for _, h := range []string{"", "ts=abc;h1=00", "h1=00", fmt.Sprintf("ts=%d", ts), fmt.Sprintf("ts=%d;h1=zz", ts)} {
			if err := verifySignature(payload, h, secret, now); err == nil {
				t.Fatalf("header %q accepted", h)
			}
		}
	})
}

func TestParseWebhook(t *testing.T) {
	c := NewClient(Config{Env: EnvSandbox, WebhookSecret: "pdl_ntfset_secret"}, nil)
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	signed := func(payload string) ([]byte, string) {
		b := []byte(payload)
		return b, header(now.Unix(), sign("pdl_ntfset_secret", now.Unix(), b))
	}

	t.Run("subscription.updated is normalized", func(t *testing.T) {
		payload, h := signed(`{
			"event_id":"evt_1","event_type":"subscription.updated","occurred_at":"2026-10-04T11:59:00Z","notification_id":"ntf_1",
			"data":{"id":"sub_1","status":"past_due","customer_id":"ctm_1","custom_data":{"user_id":"user-9"},
				"current_billing_period":{"starts_at":"2025-10-01T00:00:00Z","ends_at":"2026-10-01T00:00:00Z"},
				"scheduled_change":null}}`)
		ev, err := c.ParseWebhook(payload, h, now)
		if err != nil {
			t.Fatalf("ParseWebhook: %v", err)
		}
		if ev.NotificationID != "ntf_1" || ev.EventID != "evt_1" || ev.Type != "subscription.updated" {
			t.Fatalf("envelope = %+v", ev)
		}
		if !ev.OccurredAt.Equal(time.Date(2026, 10, 4, 11, 59, 0, 0, time.UTC)) {
			t.Fatalf("OccurredAt = %v", ev.OccurredAt)
		}
		if ev.SubscriptionID != "sub_1" || ev.CustomerID != "ctm_1" || ev.UserID != "user-9" {
			t.Fatalf("ids = %+v", ev)
		}
		if ev.Status != domain.SubscriptionPastDue || ev.CancelAtPeriodEnd || ev.Ignored {
			t.Fatalf("state = %+v", ev)
		}
		if ev.CurrentPeriodEnd == nil || !ev.CurrentPeriodEnd.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
			t.Fatalf("CurrentPeriodEnd = %v", ev.CurrentPeriodEnd)
		}
	})

	t.Run("scheduled cancel sets CancelAtPeriodEnd with status active", func(t *testing.T) {
		payload, h := signed(`{"event_id":"evt_2","event_type":"subscription.updated","occurred_at":"2026-10-04T11:59:00Z","notification_id":"ntf_2",
			"data":{"id":"sub_1","status":"active","customer_id":"ctm_1","custom_data":{"user_id":"user-9"},
				"current_billing_period":{"starts_at":"2026-10-01T00:00:00Z","ends_at":"2027-10-01T00:00:00Z"},
				"scheduled_change":{"action":"cancel","effective_at":"2027-10-01T00:00:00Z"}}}`)
		ev, err := c.ParseWebhook(payload, h, now)
		if err != nil {
			t.Fatal(err)
		}
		if ev.Status != domain.SubscriptionActive || !ev.CancelAtPeriodEnd {
			t.Fatalf("state = %+v", ev)
		}
	})

	// Review Focus: canceled payloads carry null custom_data and null period.
	t.Run("subscription.canceled with null custom_data and null period", func(t *testing.T) {
		payload, h := signed(`{"event_id":"evt_3","event_type":"subscription.canceled","occurred_at":"2026-10-04T11:59:00Z","notification_id":"ntf_3",
			"data":{"id":"sub_1","status":"canceled","customer_id":"ctm_1","custom_data":null,"current_billing_period":null,"scheduled_change":null}}`)
		ev, err := c.ParseWebhook(payload, h, now)
		if err != nil {
			t.Fatal(err)
		}
		if ev.Status != domain.SubscriptionCanceled || ev.UserID != "" || ev.CurrentPeriodEnd != nil || ev.CancelAtPeriodEnd {
			t.Fatalf("state = %+v", ev)
		}
	})

	t.Run("trialing maps to active", func(t *testing.T) {
		payload, h := signed(`{"event_id":"evt_4","event_type":"subscription.trialing","occurred_at":"2026-10-04T11:59:00Z","notification_id":"ntf_4",
			"data":{"id":"sub_1","status":"trialing","customer_id":"ctm_1"}}`)
		ev, err := c.ParseWebhook(payload, h, now)
		if err != nil {
			t.Fatal(err)
		}
		if ev.Status != domain.SubscriptionActive {
			t.Fatalf("Status = %q, want active", ev.Status)
		}
	})

	t.Run("transaction.* and unknown types are Ignored but keep ids", func(t *testing.T) {
		for _, typ := range []string{"transaction.completed", "customer.updated", "something.new"} {
			payload, h := signed(`{"event_id":"evt_5","event_type":"` + typ + `","occurred_at":"2026-10-04T11:59:00Z","notification_id":"ntf_5","data":{"id":"txn_1"}}`)
			ev, err := c.ParseWebhook(payload, h, now)
			if err != nil {
				t.Fatalf("%s: %v", typ, err)
			}
			if !ev.Ignored || ev.NotificationID != "ntf_5" || ev.Status != "" {
				t.Fatalf("%s: ev = %+v, want Ignored with envelope ids only", typ, ev)
			}
		}
	})

	t.Run("bad signature never parses the body", func(t *testing.T) {
		payload := []byte(`not json at all`)
		_, err := c.ParseWebhook(payload, header(now.Unix(), "00"), now)
		if err == nil || errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want a signature error (not a validation/parse error)", err)
		}
	})

	t.Run("malformed envelope after a valid signature is ErrValidation", func(t *testing.T) {
		payload, h := signed(`{"event_type":"subscription.updated"}`)
		_, err := c.ParseWebhook(payload, h, now)
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})
}
