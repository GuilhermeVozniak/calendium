package icsfeed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

const feedBody = "BEGIN:VCALENDAR\r\n" +
	"X-WR-CALNAME:US Holidays\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:ev1@example.com\r\n" +
	"SUMMARY:Independence Day\r\n" +
	"DTSTART;VALUE=DATE:20260704\r\n" +
	"END:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func TestFetchParsesFeedAndCapturesEtag(t *testing.T) {
	var gotIfNoneMatch string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIfNoneMatch = r.Header.Get("If-None-Match")
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Type", "text/calendar")
		_, _ = w.Write([]byte(feedBody))
	}))
	defer srv.Close()

	cal, etag, notModified, err := newFetcher(nil, nil).Fetch(context.Background(), srv.URL, "")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if notModified {
		t.Fatal("notModified = true, want false")
	}
	if gotIfNoneMatch != "" {
		t.Fatalf("If-None-Match sent without a cached etag: %q", gotIfNoneMatch)
	}
	if etag != `"v1"` {
		t.Fatalf("etag = %q, want %q", etag, `"v1"`)
	}
	if cal.Name != "US Holidays" {
		t.Fatalf("cal.Name = %q, want US Holidays", cal.Name)
	}
	if len(cal.Events) != 1 || cal.Events[0].UID != "ev1@example.com" {
		t.Fatalf("events = %+v, want one event ev1@example.com", cal.Events)
	}
}

func TestFetchSendsEtagAndHonors304(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		t.Errorf("If-None-Match = %q, want %q", r.Header.Get("If-None-Match"), `"v1"`)
	}))
	defer srv.Close()

	cal, etag, notModified, err := newFetcher(nil, nil).Fetch(context.Background(), srv.URL, `"v1"`)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !notModified {
		t.Fatal("notModified = false, want true")
	}
	if etag != `"v1"` {
		t.Fatalf("etag = %q, want the cached validator kept", etag)
	}
	if len(cal.Events) != 0 {
		t.Fatalf("events = %+v, want none on 304", cal.Events)
	}
}

func TestFetchRejectsOversizedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// A valid preamble followed by > 1 MiB of padding.
		_, _ = w.Write([]byte("BEGIN:VCALENDAR\r\n"))
		_, _ = w.Write([]byte(strings.Repeat("X", maxFeedBytes+10)))
	}))
	defer srv.Close()

	_, _, _, err := newFetcher(nil, nil).Fetch(context.Background(), srv.URL, "")
	if err == nil || err.Error() != "icsfeed: feed too large" {
		t.Fatalf("err = %v, want the coarse 1MiB cap error", err)
	}
}

func TestFetchRejectsNonIcsContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>Sign in required</body></html>"))
	}))
	defer srv.Close()

	_, _, _, err := newFetcher(nil, nil).Fetch(context.Background(), srv.URL, "")
	if err == nil || !strings.Contains(err.Error(), "not an ICS calendar") {
		t.Fatalf("err = %v, want not-an-ICS error", err)
	}
}

// TestFetchRejectsErrorStatus: HTTP failures are reported as a coarse
// category — the status code itself must never leak into the user-visible
// error (it is stored in last_error and echoed in 422 bodies, and
// per-status differences would let a subscriber port-scan internal hosts).
func TestFetchRejectsErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()

	_, _, _, err := newFetcher(nil, nil).Fetch(context.Background(), srv.URL, "")
	if err == nil || err.Error() != "icsfeed: feed answered an HTTP error" {
		t.Fatalf("err = %v, want the coarse HTTP-error category", err)
	}
	if err != nil && strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v leaks the status code", err)
	}
}

// --- SSRF dial guard ---------------------------------------------------------

// TestDialGuardBlocksPrivateLiterals: the production constructor (New)
// refuses to connect to loopback/RFC1918/link-local/ULA IP literals. The
// loopback case runs against a real listening httptest server and asserts
// the handler is NEVER reached; the others are guard-refused before any
// dial. Every refusal surfaces as the same coarse "feed unreachable".
func TestDialGuardBlocksPrivateLiterals(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(feedBody))
	}))
	defer srv.Close()

	f := New(nil)
	if _, _, _, err := f.Fetch(context.Background(), srv.URL, ""); err == nil || err.Error() != "icsfeed: feed unreachable" {
		t.Fatalf("loopback fetch err = %v, want coarse feed-unreachable", err)
	}
	if hits != 0 {
		t.Fatalf("loopback server got %d requests, want 0 (guard must refuse before connecting)", hits)
	}

	for _, url := range []string{
		"http://10.0.0.5/feed.ics",
		"http://192.168.1.10/feed.ics",
		"http://169.254.169.254/latest/meta-data", // cloud metadata endpoint
		"http://[fc00::1]/feed.ics",
		"http://[::1]/feed.ics",
		"http://0.0.0.0/feed.ics",
	} {
		if _, _, _, err := f.Fetch(context.Background(), url, ""); err == nil || err.Error() != "icsfeed: feed unreachable" {
			t.Fatalf("%s: err = %v, want coarse feed-unreachable", url, err)
		}
	}
}

// TestDialGuardBlocksPrivateResolution: a hostname RESOLVING to a private
// address is refused — the guard vets the post-resolution addresses, so a
// rebinding-style hostname cannot reach the internal network. The fake
// resolver stands in for hostile DNS.
func TestDialGuardBlocksPrivateResolution(t *testing.T) {
	lookups := 0
	private := func(_ context.Context, host string) ([]netip.Addr, error) {
		lookups++
		if host != "feeds.example.com" {
			t.Fatalf("lookup host = %q", host)
		}
		return []netip.Addr{netip.MustParseAddr("10.0.0.5")}, nil
	}
	f := newFetcher(nil, private)
	if _, _, _, err := f.Fetch(context.Background(), "http://feeds.example.com/cal.ics", ""); err == nil || err.Error() != "icsfeed: feed unreachable" {
		t.Fatalf("err = %v, want coarse feed-unreachable", err)
	}
	if lookups == 0 {
		t.Fatal("guard never consulted the resolver")
	}

	// Mixed answers fail closed: ONE private address poisons the whole set
	// (a rebinding attacker controls which record a later dial would use).
	mixed := func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("192.168.0.7")}, nil
	}
	if _, _, _, err := newFetcher(nil, mixed).Fetch(context.Background(), "http://feeds.example.com/cal.ics", ""); err == nil || err.Error() != "icsfeed: feed unreachable" {
		t.Fatalf("mixed resolution err = %v, want coarse feed-unreachable", err)
	}
}

// TestIsPublicAddr: the guard predicate delegates to netguard (whose test
// pins every refused range), so CGNAT and NAT64 are refused here too.
func TestIsPublicAddr(t *testing.T) {
	cases := map[string]bool{
		"93.184.216.34":   true,
		"10.0.0.5":        false,
		"::ffff:10.0.0.5": false,
		"100.64.0.1":      false, // CGNAT
		"64:ff9b::7f00:1": false, // NAT64
	}
	for addr, want := range cases {
		if got := isPublicAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("isPublicAddr(%s) = %v, want %v", addr, got, want)
		}
	}
}

// TestFetchNetworkErrorIsCoarse: a dead port yields exactly the coarse
// category with no dial detail (address/port/errno) attached.
func TestFetchNetworkErrorIsCoarse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // now guaranteed refused

	_, _, _, err := newFetcher(nil, nil).Fetch(context.Background(), url, "")
	if err == nil || err.Error() != "icsfeed: feed unreachable" {
		t.Fatalf("err = %v, want exactly the coarse feed-unreachable message", err)
	}
}

func TestRedirectPolicy(t *testing.T) {
	mkReq := func(raw string) *http.Request {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		return &http.Request{URL: u}
	}

	t.Run("https to http downgrade refused", func(t *testing.T) {
		err := redirectPolicy(mkReq("http://internal.local/feed"), []*http.Request{mkReq("https://example.com/a.ics")})
		if err == nil {
			t.Fatal("downgrade allowed, want refusal")
		}
	})
	t.Run("https to https allowed", func(t *testing.T) {
		if err := redirectPolicy(mkReq("https://other.example.com/b.ics"), []*http.Request{mkReq("https://example.com/a.ics")}); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	})
	t.Run("redirect chain bounded", func(t *testing.T) {
		via := make([]*http.Request, maxRedirects)
		for i := range via {
			via[i] = mkReq("https://example.com/a.ics")
		}
		if err := redirectPolicy(mkReq("https://example.com/z.ics"), via); err == nil {
			t.Fatal("6th redirect allowed, want refusal")
		}
	})
}
