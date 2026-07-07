// Package googleapi implements the Google gateway adapters — OAuth2
// (offline access), Gmail REST, and Google Calendar REST — with raw
// net/http and stdlib only (docs/architecture.md). One *Client satisfies
// port.OAuthGateway, port.MailProvider, and port.CalendarProvider for
// provider "google".
package googleapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// Client is the Google API gateway.
type Client struct {
	clientID     string
	clientSecret string
	hc           *http.Client
}

var (
	_ port.OAuthGateway     = (*Client)(nil)
	_ port.MailProvider     = (*Client)(nil)
	_ port.CalendarProvider = (*Client)(nil)
)

// NewClient builds the Google gateway. hc may be nil, in which case
// http.DefaultClient is used.
func NewClient(clientID, clientSecret string, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{clientID: clientID, clientSecret: clientSecret, hc: hc}
}

// apiError is the standard Google JSON error envelope.
type apiError struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

// httpError carries the response status for control flow (e.g. 410 GONE on
// stale sync tokens) while unwrapping to domain sentinels.
type httpError struct {
	StatusCode int
	Message    string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("google api: http %d: %s", e.StatusCode, e.Message)
}

func (e *httpError) Unwrap() error {
	switch e.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return domain.ErrUnauthorized
	case http.StatusNotFound:
		return domain.ErrNotFound
	default:
		return nil
	}
}

// doJSON performs an authenticated JSON request. body and out may be nil;
// non-2xx responses are returned as *httpError.
func (c *Client) doJSON(ctx context.Context, method, url, accessToken string, body, out any) error {
	var rd io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("googleapi: encode request: %w", err)
		}
		rd = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return fmt.Errorf("googleapi: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	res, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("googleapi: %s %s: %w", method, url, err)
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("googleapi: read response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		var ae apiError
		_ = json.Unmarshal(raw, &ae)
		msg := ae.Error.Message
		if msg == "" {
			msg = truncate(string(raw), 200)
		}
		return &httpError{StatusCode: res.StatusCode, Message: msg}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("googleapi: decode response: %w", err)
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
