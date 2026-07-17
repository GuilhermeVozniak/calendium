// Package unsubscribe implements port.UnsubscribeGateway: an RFC 8058
// one-click list-unsubscribe POST using only the standard library.
package unsubscribe

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"calendium/backend/internal/port"
)

// Client POSTs "List-Unsubscribe=One-Click" to the sender's unsubscribe URL.
type Client struct {
	HTTP *http.Client
}

// dialGuard vets every address net/http is about to connect to, after DNS
// resolution but before the connect syscall, so a hostname that resolves to
// a private/loopback/link-local address (whether at lookup time or via a
// DNS-rebind between checks) can never be reached. It is a package-level var
// so tests can substitute a permissive guard when exercising HTTP-level
// behavior (redirects, status codes) against httptest servers, which always
// listen on loopback addresses production traffic must never reach.
var dialGuard = blockPrivateNetworks

func blockPrivateNetworks(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("one-click unsubscribe: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("one-click unsubscribe: could not parse resolved address %q", host)
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return fmt.Errorf("one-click unsubscribe: refusing to dial disallowed address %s", ip)
	}
	return nil
}

func New() *Client {
	return &Client{HTTP: &http.Client{
		Timeout: 10 * time.Second,
		// RFC 8058 one-click targets are POSTed directly; a 3xx response is
		// treated as a failure below rather than followed.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			DialContext: (&net.Dialer{Control: dialGuard}).DialContext,
		},
	}}
}

func (c *Client) PostOneClick(ctx context.Context, target string) error {
	u, err := url.Parse(target)
	if err != nil {
		return fmt.Errorf("invalid one-click unsubscribe url %q: %w", target, err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("one-click unsubscribe requires https scheme, got %q", u.Scheme)
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
