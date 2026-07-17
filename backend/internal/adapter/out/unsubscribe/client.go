// Package unsubscribe implements port.UnsubscribeGateway: an RFC 8058
// one-click list-unsubscribe POST using only the standard library.
package unsubscribe

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"calendium/backend/internal/port"
)

// Client POSTs "List-Unsubscribe=One-Click" to the sender's unsubscribe URL.
type Client struct {
	HTTP *http.Client
}

func New() *Client {
	return &Client{HTTP: &http.Client{Timeout: 10 * time.Second}}
}

func (c *Client) PostOneClick(ctx context.Context, target string) error {
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("invalid one-click unsubscribe url %q", target)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target,
		strings.NewReader("List-Unsubscribe=One-Click"))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("one-click unsubscribe returned status %d", res.StatusCode)
	}
	return nil
}

var _ port.UnsubscribeGateway = (*Client)(nil)
