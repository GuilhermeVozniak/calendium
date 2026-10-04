package push

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"calendium/backend/internal/config"
)

const (
	apnsHost = "https://api.push.apple.com"
	// apnsTokenLifetime keeps provider tokens well inside Apple's 20–60
	// minute validity window.
	apnsTokenLifetime = 50 * time.Minute
	// defaultAPNsTopic is the iOS app bundle id (apps/mobile/app.json
	// ios.bundleIdentifier); override with APNS_TOPIC for custom builds.
	defaultAPNsTopic = "app.calendium.mobile"
)

// apnsSender POSTs alerts to APNs over HTTP/2 (negotiated via ALPN by the
// stdlib transport) authenticated with an ES256 provider-token JWT.
type apnsSender struct {
	cfg   config.APNs
	topic string
	hc    *http.Client

	mu       sync.Mutex
	key      *ecdsa.PrivateKey
	keyErr   error
	keyOnce  sync.Once
	token    string
	issuedAt time.Time
}

func newAPNsSender(cfg config.APNs, hc *http.Client) *apnsSender {
	topic := os.Getenv("APNS_TOPIC")
	if topic == "" {
		topic = defaultAPNsTopic
	}
	return &apnsSender{cfg: cfg, topic: topic, hc: hc}
}

func (s *apnsSender) send(ctx context.Context, deviceToken, title, body string, data map[string]string) error {
	jwt, err := s.providerToken()
	if err != nil {
		return err
	}

	payload := map[string]any{
		"aps": map[string]any{
			"alert": map[string]string{"title": title, "body": body},
			"sound": "default",
		},
	}
	for k, v := range data {
		payload[k] = v
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("push: encode apns payload: %w", err)
	}

	endpoint := apnsHost + "/3/device/" + url.PathEscape(deviceToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("push: build apns request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apns-topic", s.topic)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-priority", "10")

	res, err := s.hc.Do(req)
	if err != nil {
		return fmt.Errorf("push: apns request: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == http.StatusOK {
		return nil
	}
	var apnsErr struct {
		Reason string `json:"reason"`
	}
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	_ = json.Unmarshal(raw, &apnsErr)
	return fmt.Errorf("push: apns http %d: %s", res.StatusCode, apnsErr.Reason)
}

// providerToken returns a cached ES256 provider token, re-minting when it
// approaches Apple's 60-minute cap.
func (s *apnsSender) providerToken() (string, error) {
	s.keyOnce.Do(func() {
		s.key, s.keyErr = parseECPrivateKeyPEM(s.cfg.KeyP8)
	})
	if s.keyErr != nil {
		return "", fmt.Errorf("push: parse APNS_KEY_P8: %w", s.keyErr)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.token != "" && now.Sub(s.issuedAt) < apnsTokenLifetime {
		return s.token, nil
	}
	header := map[string]string{"alg": "ES256", "kid": s.cfg.KeyID}
	claims := map[string]any{"iss": s.cfg.TeamID, "iat": now.Unix()}
	token, err := signES256(header, claims, s.key)
	if err != nil {
		return "", err
	}
	s.token, s.issuedAt = token, now
	return token, nil
}
