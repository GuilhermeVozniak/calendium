// Package icsfeed is the outbound HTTP adapter behind port.IcsFetcher: it
// retrieves user-subscribed ICS feeds with a bounded body (1 MiB), ETag
// revalidation, and a redirect policy that never downgrades https → http
// (subscription URLs are untrusted user input — the https-only rule itself
// is enforced at the domain layer, so tests can point the fetcher at plain
// http httptest servers).
package icsfeed

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
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

// New wraps hc (nil for a 30s-timeout default client). The client's
// redirect policy is replaced with redirectPolicy in either case.
func New(hc *http.Client) *Fetcher {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	} else {
		c := *hc // shallow copy — never mutate the caller's client
		hc = &c
	}
	hc.CheckRedirect = redirectPolicy
	return &Fetcher{hc: hc}
}

// redirectPolicy allows up to maxRedirects hops and refuses any https →
// http downgrade: a feed subscribed over TLS must never be silently
// re-fetched over cleartext (SSRF/downgrade hygiene).
func redirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("icsfeed: stopped after %d redirects", maxRedirects)
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
		return ics.Calendar{}, "", false, fmt.Errorf("icsfeed: build request: %w", err)
	}
	req.Header.Set("Accept", "text/calendar, */*;q=0.5")
	req.Header.Set("User-Agent", "Calendium/1.0 (+ics-subscription)")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	resp, err := f.hc.Do(req)
	if err != nil {
		return ics.Calendar{}, "", false, fmt.Errorf("icsfeed: fetch feed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified {
		return ics.Calendar{}, etag, true, nil
	}
	if resp.StatusCode != http.StatusOK {
		return ics.Calendar{}, "", false, fmt.Errorf("icsfeed: feed answered status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedBytes+1))
	if err != nil {
		return ics.Calendar{}, "", false, fmt.Errorf("icsfeed: read body: %w", err)
	}
	if len(body) > maxFeedBytes {
		return ics.Calendar{}, "", false, fmt.Errorf("icsfeed: feed exceeds the %d byte limit", maxFeedBytes)
	}
	if !bytes.Contains(bytes.ToUpper(body), []byte("BEGIN:VCALENDAR")) {
		return ics.Calendar{}, "", false, errors.New("icsfeed: response is not an ICS calendar")
	}

	// Parse is tolerant: individually malformed VEVENTs are dropped with a
	// joined error while good ones survive — that is not a fetch failure.
	cal, _ := ics.Parse(bytes.NewReader(body))
	return cal, resp.Header.Get("ETag"), false, nil
}
