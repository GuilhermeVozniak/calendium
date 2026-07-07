// Package stripeapi implements port.Payments with a raw form-encoded stdlib
// REST client for api.stripe.com/v1 plus Stripe-Signature webhook
// verification (docs/payments.md — the $50/yr Spotify-model flow).
package stripeapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

const apiBase = "https://api.stripe.com/v1"

// Client is the Stripe billing gateway.
type Client struct {
	secretKey     string
	webhookSecret string
	annualPriceID string
	hc            *http.Client
}

var _ port.Payments = (*Client)(nil)

// NewClient builds the Stripe gateway. hc may be nil, in which case
// http.DefaultClient is used.
func NewClient(secretKey, webhookSecret, annualPriceID string, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{secretKey: secretKey, webhookSecret: webhookSecret, annualPriceID: annualPriceID, hc: hc}
}

// do performs one Stripe call: form-encoded POST or querystring GET,
// Bearer-authenticated, JSON out.
func (c *Client) do(ctx context.Context, method, path string, form url.Values, out any) error {
	endpoint := apiBase + path
	var body io.Reader
	if method == http.MethodGet {
		if len(form) > 0 {
			endpoint += "?" + form.Encode()
		}
	} else if form != nil {
		body = strings.NewReader(form.Encode())
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("stripeapi: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.secretKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	res, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("stripeapi: %s %s: %w", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("stripeapi: read response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		var se struct {
			Error struct {
				Type    string `json:"type"`
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &se)
		err := fmt.Errorf("stripeapi: http %d (%s): %s", res.StatusCode, se.Error.Type, se.Error.Message)
		switch res.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Errorf("%w: %w", domain.ErrUnauthorized, err)
		case http.StatusNotFound:
			return fmt.Errorf("%w: %w", domain.ErrNotFound, err)
		}
		return err
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("stripeapi: decode response: %w", err)
		}
	}
	return nil
}
