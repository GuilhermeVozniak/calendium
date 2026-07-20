// Package icsfeed is the outbound HTTP adapter behind port.IcsFetcher: it
// retrieves user-subscribed ICS feeds with a bounded body (1 MiB), ETag
// revalidation, a redirect policy that never downgrades https → http, and
// an SSRF dial guard — subscription URLs are untrusted user input, so every
// connection is refused unless the address actually being dialed is a
// public (global-unicast, non-private) IP. Because the check runs against
// the resolved, dialed address, DNS rebinding cannot smuggle a fetch into
// the internal network. (The https-only rule itself is enforced at the
// domain layer; package tests bypass the guard to reach httptest servers.)
//
// Errors returned by Fetch are deliberately COARSE categories ("feed
// unreachable", "feed answered an HTTP error", …): they surface to users in
// 422 bodies and are stored in last_error, and detailed errors (status
// codes, dial targets, guard refusals) would let an authenticated user
// port-scan the deployment's network by subscribing to internal addresses
// and diffing the messages.
package icsfeed

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"time"

	"calendium/backend/internal/ics"
	"calendium/backend/internal/port"
)

// maxFeedBytes caps a feed body at 1 MiB — plenty for real calendars,
// small enough that a hostile feed cannot balloon memory.
const maxFeedBytes = 1 << 20

// maxRedirects bounds the redirect chain.
const maxRedirects = 5

// Fetcher implements port.IcsFetcher over net/http.
type Fetcher struct {
	hc *http.Client
}

var _ port.IcsFetcher = (*Fetcher)(nil)

// errFeedUnreachable is the uniform user-visible failure for anything
// network-shaped — DNS failure, refused/timed-out dials, TLS errors,
// refused redirects, AND destinations blocked by the SSRF guard. One
// indistinguishable message means feed errors carry zero reconnaissance
// value about the internal network.
var errFeedUnreachable = errors.New("icsfeed: feed unreachable")

// New wraps hc (nil for a 30s-timeout default client). The client's
// redirect policy is replaced with redirectPolicy and its transport with
// one whose dials are SSRF-guarded (see guardedDialContext).
func New(hc *http.Client) *Fetcher {
	return newFetcher(hc, defaultLookup)
}

// lookupFunc resolves a hostname to IPs; injectable so tests can simulate a
// hostname resolving to a private address without real DNS.
type lookupFunc func(ctx context.Context, host string) ([]netip.Addr, error)

func defaultLookup(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// newFetcher wires the dial guard when lookup is non-nil; package tests
// pass nil to reach local httptest servers.
func newFetcher(hc *http.Client, lookup lookupFunc) *Fetcher {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	} else {
		c := *hc // shallow copy — never mutate the caller's client
		hc = &c
	}
	hc.CheckRedirect = redirectPolicy
	if lookup != nil {
		hc.Transport = &http.Transport{
			// No Proxy func: an environment proxy would carry the request
			// past the dial guard.
			DialContext:           guardedDialContext(lookup),
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
		}
	}
	return &Fetcher{hc: hc}
}

// guardedDialContext returns a DialContext that only ever connects to
// vetted public IP literals. Hostnames are resolved ONCE through lookup,
// every resolved address must pass isPublicAddr, and the connection is then
// made to the vetted literal itself — a rebinding DNS server never gets a
// second query to answer differently.
func guardedDialContext(lookup lookupFunc) func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if ip, perr := netip.ParseAddr(host); perr == nil {
			if !isPublicAddr(ip) {
				return nil, errFeedUnreachable
			}
			return dialer.DialContext(ctx, network, addr)
		}
		ips, err := lookup(ctx, host)
		if err != nil || len(ips) == 0 {
			return nil, errFeedUnreachable
		}
		for _, ip := range ips {
			if !isPublicAddr(ip) {
				return nil, errFeedUnreachable
			}
		}
		var dialErr error
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
			if err == nil {
				return conn, nil
			}
			dialErr = err
		}
		return nil, dialErr
	}
}

// isPublicAddr reports whether ip is a global-unicast, non-private address.
// Refused: loopback (127/8, ::1), RFC1918 (10/8, 172.16/12, 192.168/16),
// link-local (169.254/16, fe80::/10), ULA (fc00::/7 via RFC 4193),
// unspecified (0.0.0.0, ::), and multicast — including their IPv4-mapped
// IPv6 forms (Unmap).
func isPublicAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsGlobalUnicast() && !ip.IsPrivate()
}

// redirectPolicy allows up to maxRedirects hops and refuses any https →
// http downgrade: a feed subscribed over TLS must never be silently
// re-fetched over cleartext (SSRF/downgrade hygiene).
func redirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return errors.New("icsfeed: stopped after too many redirects")
	}
	if len(via) > 0 && via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
		return errors.New("icsfeed: refusing redirect from https to non-https")
	}
	return nil
}

// Fetch retrieves and parses the feed. When etag is non-empty it is sent as
// If-None-Match; a 304 answer returns notModified=true (events must be
// kept). Individually malformed VEVENTs are tolerated (the parser drops
// them); a body without BEGIN:VCALENDAR is an error — an HTML error page
// must never be mistaken for an empty calendar.
func (f *Fetcher) Fetch(ctx context.Context, url, etag string) (ics.Calendar, string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ics.Calendar{}, "", false, errors.New("icsfeed: invalid feed URL")
	}
	req.Header.Set("Accept", "text/calendar, */*;q=0.5")
	req.Header.Set("User-Agent", "Calendium/1.0 (+ics-subscription)")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	// Coarse error categories from here down (see the package comment): no
	// status codes, no dial detail, no guard-vs-dead-host distinction.
	resp, err := f.hc.Do(req)
	if err != nil {
		return ics.Calendar{}, "", false, errFeedUnreachable
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified {
		return ics.Calendar{}, etag, true, nil
	}
	if resp.StatusCode != http.StatusOK {
		return ics.Calendar{}, "", false, errors.New("icsfeed: feed answered an HTTP error")
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedBytes+1))
	if err != nil {
		return ics.Calendar{}, "", false, errFeedUnreachable
	}
	if len(body) > maxFeedBytes {
		return ics.Calendar{}, "", false, errors.New("icsfeed: feed too large")
	}
	if !bytes.Contains(bytes.ToUpper(body), []byte("BEGIN:VCALENDAR")) {
		return ics.Calendar{}, "", false, errors.New("icsfeed: response is not an ICS calendar")
	}

	// Parse is tolerant: individually malformed VEVENTs are dropped with a
	// joined error while good ones survive — that is not a fetch failure.
	cal, _ := ics.Parse(bytes.NewReader(body))
	return cal, resp.Header.Get("ETag"), false, nil
}
