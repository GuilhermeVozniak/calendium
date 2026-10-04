// Package paddle implements port.Payments against the Paddle Billing REST
// API (docs/payments.md): customers, transactions (overlay checkout),
// customer-portal sessions, subscription reads/cancels, and Paddle-Signature
// webhook verification. stdlib net/http only; the http.Client is injected.
package paddle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// Environment names (PADDLE_ENV).
const (
	EnvSandbox = "sandbox"
	EnvLive    = "live"

	sandboxBaseURL = "https://sandbox-api.paddle.com"
	liveBaseURL    = "https://api.paddle.com"
)

// Config configures the gateway. BaseURL overrides the environment-derived
// origin (tests point it at an httptest server); leave it empty in production.
type Config struct {
	Env           string // EnvSandbox | EnvLive
	APIKey        string
	WebhookSecret string
	AnnualPriceID string
	BaseURL       string
}

// Client is the Paddle billing gateway.
type Client struct {
	cfg     Config
	baseURL string
	hc      *http.Client
}

var _ port.Payments = (*Client)(nil)

// BaseURLFor returns the API origin for a PADDLE_ENV value; anything but
// "live" is sandbox so a typo can never hit production billing.
func BaseURLFor(env string) string {
	if env == EnvLive {
		return liveBaseURL
	}
	return sandboxBaseURL
}

// NewClient builds the gateway. hc may be nil (http.DefaultClient).
func NewClient(cfg Config, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	base := cfg.BaseURL
	if base == "" {
		base = BaseURLFor(cfg.Env)
	}
	return &Client{cfg: cfg, baseURL: strings.TrimRight(base, "/"), hc: hc}
}

// APIError is a non-2xx Paddle response ({error: {type, code, detail}}).
type APIError struct {
	Status int
	Type   string
	Code   string
	Detail string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("paddle: http %d (%s/%s): %s", e.Status, e.Type, e.Code, e.Detail)
}

// do performs one JSON call. Paddle wraps every success body in {data, meta};
// out (when non-nil) receives the decoded `data` member. 401/403 wrap
// domain.ErrUnauthorized and 404 wraps domain.ErrNotFound; every non-2xx
// carries an *APIError in its chain.
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("paddle: encode request: %w", err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("paddle: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("paddle: %s %s: %w", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("paddle: read response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		var env struct {
			Error struct {
				Type   string `json:"type"`
				Code   string `json:"code"`
				Detail string `json:"detail"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &env)
		apiErr := &APIError{Status: res.StatusCode, Type: env.Error.Type, Code: env.Error.Code, Detail: env.Error.Detail}
		switch res.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Errorf("%w: %w", domain.ErrUnauthorized, apiErr)
		case http.StatusNotFound:
			return fmt.Errorf("%w: %w", domain.ErrNotFound, apiErr)
		}
		return apiErr
	}
	if out == nil {
		return nil
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("paddle: decode envelope: %w", err)
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("paddle: decode data: %w", err)
	}
	return nil
}
