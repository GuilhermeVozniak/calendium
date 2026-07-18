// Package msgraph implements the Microsoft gateway adapters — Microsoft
// identity platform OAuth2 (v2.0 endpoints), Graph mail (inbox delta,
// sendMail, move/flag), and Graph calendar (calendarView delta, event CRUD,
// respond endpoints) — with raw net/http and stdlib only
// (docs/architecture.md). One *Client satisfies port.OAuthGateway,
// port.MailProvider, and port.CalendarProvider for provider "microsoft".
package msgraph

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

const graphBase = "https://graph.microsoft.com/v1.0"

// Client is the Microsoft Graph gateway.
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

// NewClient builds the Microsoft Graph gateway. hc may be nil, in which
// case http.DefaultClient is used.
func NewClient(clientID, clientSecret string, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{clientID: clientID, clientSecret: clientSecret, hc: hc}
}

// httpError carries the Graph response status while unwrapping to domain
// sentinels.
type httpError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("msgraph: http %d (%s): %s", e.StatusCode, e.Code, e.Message)
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

// doJSON performs an authenticated Graph request. All event/message
// timestamps are requested in UTC via the Prefer header so parsing is
// location-free. body and out may be nil.
func (c *Client) doJSON(ctx context.Context, method, url, accessToken string, body, out any) error {
	var rd io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("msgraph: encode request: %w", err)
		}
		rd = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return fmt.Errorf("msgraph: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Prefer", `outlook.timezone="UTC", odata.maxpagesize=50`)

	res, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("msgraph: %s %s: %w", method, url, err)
	}
	defer func() { _ = res.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("msgraph: read response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		var ge struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &ge)
		msg := ge.Error.Message
		if msg == "" && len(raw) > 0 {
			msg = truncate(string(raw), 200)
		}
		return &httpError{StatusCode: res.StatusCode, Code: ge.Error.Code, Message: msg}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("msgraph: decode response: %w", err)
		}
	}
	return nil
}

// maxAttachmentBytes caps raw attachment downloads defensively — well above
// any legitimate email attachment (Outlook/Graph attachments top out around
// 150MB via upload sessions, but ordinary mail attachments are far smaller)
// while still bounding memory use against a misbehaving or malicious
// response.
const maxAttachmentBytes = 64 << 20

// doRaw performs an authenticated Graph request expecting a non-JSON body
// (attachment content) and returns the raw bytes plus the response
// Content-Type header. Non-2xx responses still arrive as the standard
// Graph JSON error envelope and are mapped via the same *httpError as
// doJSON.
func (c *Client) doRaw(ctx context.Context, method, url, accessToken string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("msgraph: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	res, err := c.hc.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("msgraph: %s %s: %w", method, url, err)
	}
	defer func() { _ = res.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(res.Body, maxAttachmentBytes))
	if err != nil {
		return nil, "", fmt.Errorf("msgraph: read response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		var ge struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &ge)
		msg := ge.Error.Message
		if msg == "" && len(raw) > 0 {
			msg = truncate(string(raw), 200)
		}
		return nil, "", &httpError{StatusCode: res.StatusCode, Code: ge.Error.Code, Message: msg}
	}
	return raw, res.Header.Get("Content-Type"), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
