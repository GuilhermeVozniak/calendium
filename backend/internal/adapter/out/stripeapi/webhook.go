package stripeapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// webhookTolerance rejects signatures older (or newer) than this, blocking
// replay attacks (docs/payments.md).
const webhookTolerance = 5 * time.Minute

// ParseWebhook verifies the Stripe-Signature header (t/v1 scheme:
// HMAC-SHA256 over "<t>.<payload>", constant-time compare, 5-minute
// tolerance) and normalizes the event into a port.WebhookEvent.
func (c *Client) ParseWebhook(payload []byte, sigHeader string) (port.WebhookEvent, error) {
	if err := verifySignature(payload, sigHeader, c.webhookSecret, time.Now()); err != nil {
		return port.WebhookEvent{}, err
	}

	var event struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Created int64  `json:"created"`
		Data    struct {
			Object json.RawMessage `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		return port.WebhookEvent{}, fmt.Errorf("stripeapi: decode webhook event: %w", err)
	}

	out := port.WebhookEvent{ID: event.ID, Type: event.Type, Created: unixPtr(event.Created)}
	switch {
	case event.Type == "checkout.session.completed":
		var s struct {
			Customer          string `json:"customer"`
			Subscription      string `json:"subscription"`
			ClientReferenceID string `json:"client_reference_id"`
			Metadata          struct {
				UserID string `json:"user_id"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(event.Data.Object, &s); err != nil {
			return port.WebhookEvent{}, fmt.Errorf("stripeapi: decode checkout session: %w", err)
		}
		out.CustomerID = s.Customer
		out.SubscriptionID = s.Subscription
		out.UserID = firstNonEmpty(s.Metadata.UserID, s.ClientReferenceID)
		// Status intentionally left empty: the paired
		// customer.subscription.created event is authoritative.

	case strings.HasPrefix(event.Type, "customer.subscription."):
		var sub stripeSubscription
		if err := json.Unmarshal(event.Data.Object, &sub); err != nil {
			return port.WebhookEvent{}, fmt.Errorf("stripeapi: decode subscription: %w", err)
		}
		out.CustomerID = sub.Customer
		out.SubscriptionID = sub.ID
		out.UserID = sub.Metadata.UserID
		out.Status = mapSubscriptionStatus(sub.Status)
		out.CancelAtPeriodEnd = sub.CancelAtPeriodEnd
		out.CurrentPeriodEnd = unixPtr(sub.CurrentPeriodEnd)
		out.TrialEndsAt = unixPtr(sub.TrialEnd)

	case event.Type == "invoice.paid" || event.Type == "invoice.payment_failed":
		var inv struct {
			Customer     string `json:"customer"`
			Subscription string `json:"subscription"`
			PeriodEnd    int64  `json:"period_end"`
		}
		if err := json.Unmarshal(event.Data.Object, &inv); err != nil {
			return port.WebhookEvent{}, fmt.Errorf("stripeapi: decode invoice: %w", err)
		}
		out.CustomerID = inv.Customer
		out.SubscriptionID = inv.Subscription
		// A renewal payment settles to active; a failed one to past_due
		// (docs/payments.md lifecycle). current_period_end is left to the
		// subscription.* events, which carry the authoritative value.
		if event.Type == "invoice.paid" {
			out.Status = domain.SubscriptionActive
		} else {
			out.Status = domain.SubscriptionPastDue
		}
	}
	return out, nil
}

// verifySignature implements the Stripe t/v1 signing scheme.
func verifySignature(payload []byte, sigHeader, secret string, now time.Time) error {
	if sigHeader == "" {
		return fmt.Errorf("stripeapi: missing Stripe-Signature header")
	}
	var ts int64 = -1
	var candidates [][]byte
	for _, part := range strings.Split(sigHeader, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return fmt.Errorf("stripeapi: bad signature timestamp %q", v)
			}
			ts = n
		case "v1":
			sig, err := hex.DecodeString(v)
			if err == nil {
				candidates = append(candidates, sig)
			}
		}
	}
	if ts < 0 || len(candidates) == 0 {
		return fmt.Errorf("stripeapi: malformed Stripe-Signature header")
	}

	if drift := now.Sub(time.Unix(ts, 0)); drift > webhookTolerance || drift < -webhookTolerance {
		return fmt.Errorf("stripeapi: webhook timestamp outside %s tolerance", webhookTolerance)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10)))
	mac.Write([]byte("."))
	mac.Write(payload)
	expected := mac.Sum(nil)

	for _, sig := range candidates {
		if hmac.Equal(expected, sig) {
			return nil
		}
	}
	return fmt.Errorf("stripeapi: no matching v1 signature")
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
