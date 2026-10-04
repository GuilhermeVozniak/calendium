package paddle

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// webhookTolerance bounds |now - ts|. 5 minutes is wider than Paddle's 5 s
// SDK default to absorb clock skew; retries carry fresh timestamps so replay
// exposure stays bounded.
const webhookTolerance = 5 * time.Minute

// ParseWebhook verifies the Paddle-Signature header and normalizes the
// envelope {event_id, event_type, occurred_at, notification_id, data}.
// Signature failures return before the body is parsed. Non-subscription
// event types come back Ignored=true with the envelope ids only.
func (c *Client) ParseWebhook(payload []byte, sigHeader string, now time.Time) (port.SubscriptionEvent, error) {
	if err := verifySignature(payload, sigHeader, c.cfg.WebhookSecret, now); err != nil {
		return port.SubscriptionEvent{}, err
	}
	var env struct {
		EventID        string          `json:"event_id"`
		EventType      string          `json:"event_type"`
		OccurredAt     time.Time       `json:"occurred_at"`
		NotificationID string          `json:"notification_id"`
		Data           json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return port.SubscriptionEvent{}, fmt.Errorf("%w: paddle: decode webhook envelope: %v", domain.ErrValidation, err)
	}
	if env.EventID == "" || env.NotificationID == "" || env.EventType == "" {
		return port.SubscriptionEvent{}, fmt.Errorf("%w: paddle: webhook envelope missing event_id, notification_id or event_type", domain.ErrValidation)
	}
	out := port.SubscriptionEvent{
		NotificationID: env.NotificationID,
		EventID:        env.EventID,
		Type:           env.EventType,
		OccurredAt:     env.OccurredAt.UTC(),
	}
	if !strings.HasPrefix(env.EventType, "subscription.") {
		out.Ignored = true
		return out, nil
	}
	var sub subscription
	if err := json.Unmarshal(env.Data, &sub); err != nil {
		return port.SubscriptionEvent{}, fmt.Errorf("%w: paddle: decode subscription data: %v", domain.ErrValidation, err)
	}
	if sub.ID == "" {
		// data:null or {} decodes cleanly; never hand the service an empty event.
		return port.SubscriptionEvent{}, fmt.Errorf("%w: paddle: %s webhook without subscription data", domain.ErrValidation, env.EventType)
	}
	return normalizeSubscription(sub, out), nil
}

// verifySignature implements Paddle-Signature: `ts=<unix>;h1=<hex>[;h1=<hex>]`,
// HMAC-SHA256(secret, "<ts>:<raw body>") hex, constant-time compare, any h1
// may match (key rotation). An empty secret is refused outright so a
// misconfigured deployment can never accept forged events.
func verifySignature(payload []byte, header, secret string, now time.Time) error {
	if secret == "" {
		return errors.New("paddle: webhook secret is empty; refusing to verify")
	}
	if header == "" {
		return errors.New("paddle: missing Paddle-Signature header")
	}
	var ts int64 = -1
	var candidates [][]byte
	for _, part := range strings.Split(header, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "ts":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return fmt.Errorf("paddle: bad signature timestamp %q", v)
			}
			ts = n
		case "h1":
			sig, err := hex.DecodeString(v)
			if err == nil && len(sig) > 0 {
				candidates = append(candidates, sig)
			}
		}
	}
	if ts < 0 || len(candidates) == 0 {
		return errors.New("paddle: malformed Paddle-Signature header")
	}
	if drift := now.Sub(time.Unix(ts, 0)); drift > webhookTolerance || drift < -webhookTolerance {
		return fmt.Errorf("paddle: webhook timestamp outside %s tolerance", webhookTolerance)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10)))
	mac.Write([]byte(":"))
	mac.Write(payload)
	expected := mac.Sum(nil)
	for _, sig := range candidates {
		if hmac.Equal(expected, sig) {
			return nil
		}
	}
	return errors.New("paddle: no matching h1 signature")
}
