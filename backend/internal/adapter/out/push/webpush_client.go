package push

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"

	"calendium/backend/internal/adapter/out/netguard"
)

// maxWebPushRedirects bounds the redirect hops a push service may send.
const maxWebPushRedirects = 3

// dialControl is a net.Dialer.Control hook (netguard.Control in production).
type dialControl func(network, address string, c syscall.RawConn) error

// newWebPushClient is the Web Push delivery client. The endpoint comes from
// a user-registered PushSubscription, so it is an SSRF surface: every dial
// (the endpoint and every redirect hop) goes through control after DNS
// resolution, no environment proxy can carry the request past it, and the
// redirect policy re-checks each hop before it is dialled.
func newWebPushClient(control dialControl) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: control}
	return &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: webPushRedirectPolicy,
		Transport: &http.Transport{
			// No Proxy: an environment proxy would dial on our behalf.
			DialContext:           dialer.DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          20,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
	}
}

// newGuardedWebPushClient is the production client: netguard on every dial.
func newGuardedWebPushClient() *http.Client { return newWebPushClient(netguard.Control) }

// webPushRedirectPolicy re-checks every redirect hop: at most
// maxWebPushRedirects, no https → http downgrade, and an IP-literal target
// must be public. Hostname targets are vetted again at dial time.
func webPushRedirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) >= maxWebPushRedirects {
		return fmt.Errorf("push: web push stopped after %d redirects", len(via))
	}
	if len(via) > 0 && via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
		return errors.New("push: web push redirect downgrades https to http")
	}
	if req.URL.Scheme != "https" && req.URL.Scheme != "http" {
		return fmt.Errorf("push: web push redirect to unsupported scheme %q", req.URL.Scheme)
	}
	if ip, err := netip.ParseAddr(req.URL.Hostname()); err == nil && !netguard.IsPublic(ip) {
		return fmt.Errorf("push: web push redirect refused: netguard: disallowed address %s", ip)
	}
	return nil
}
