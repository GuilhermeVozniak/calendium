package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestChecker builds a checker against url with a temp cache file, a fixed
// clock and an emit sink that records every UpdateInfo it receives.
func newTestChecker(t *testing.T, version, url string) (*updateChecker, *[]UpdateInfo) {
	t.Helper()
	var emitted []UpdateInfo
	c := &updateChecker{
		version:   version,
		url:       url,
		hc:        &http.Client{Timeout: updateCheckTimeout},
		cachePath: filepath.Join(t.TempDir(), "update-check.json"),
		now:       func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) },
		latest:    UpdateInfo{Current: version},
	}
	c.setEmit(func(info UpdateInfo) { emitted = append(emitted, info) })
	return c, &emitted
}

// releaseServer mimics GET /repos/.../releases/latest: ETag "etag-1", 304 on
// a matching If-None-Match, and asserts the request shape the spec fixes.
func releaseServer(t *testing.T, tag string, draft, prerelease bool, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept = %q", got)
		}
		if ua := r.Header.Get("User-Agent"); !strings.HasPrefix(ua, "Calendium-Desktop/") {
			t.Errorf("User-Agent = %q, want Calendium-Desktop/<version>", ua)
		}
		if r.URL.RawQuery != "" {
			t.Errorf("unexpected query string %q (nothing beyond a UA may reach GitHub)", r.URL.RawQuery)
		}
		w.Header().Set("ETag", `"etag-1"`)
		if r.Header.Get("If-None-Match") == `"etag-1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"tag_name":%q,"html_url":"https://github.com/GuilhermeVozniak/calendium/releases/tag/%s","draft":%t,"prerelease":%t}`, tag, tag, draft, prerelease)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestUpdateCheck_NewerReleaseEmitsOnce(t *testing.T) {
	var hits atomic.Int32
	srv := releaseServer(t, "v1.3.0", false, false, &hits)
	c, emitted := newTestChecker(t, "1.2.3", srv.URL)

	info, err := c.check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := UpdateInfo{Available: true, Current: "1.2.3", Latest: "1.3.0", URL: "https://github.com/GuilhermeVozniak/calendium/releases/tag/v1.3.0"}
	if info != want {
		t.Fatalf("check = %+v, want %+v", info, want)
	}
	if c.status() != want {
		t.Fatalf("status = %+v, want %+v", c.status(), want)
	}
	if len(*emitted) != 1 || (*emitted)[0] != want {
		t.Fatalf("emitted = %+v, want exactly [%+v]", *emitted, want)
	}
}

func TestUpdateCheck_EqualOlderPrereleaseDraftDoNotEmit(t *testing.T) {
	cases := []struct {
		name       string
		tag        string
		draft, pre bool
	}{
		{"equal", "v1.2.3", false, false},
		{"older", "v1.2.2", false, false},
		{"prerelease flag", "v2.0.0", false, true},
		{"draft flag", "v2.0.0", true, false},
		{"prerelease tag", "v1.2.4-rc.1", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			srv := releaseServer(t, tc.tag, tc.draft, tc.pre, &hits)
			c, emitted := newTestChecker(t, "1.2.3", srv.URL)
			info, err := c.check(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if info.Available {
				t.Fatalf("Available = true for %+v", info)
			}
			if len(*emitted) != 0 {
				t.Fatalf("emitted %+v, want nothing", *emitted)
			}
		})
	}
}

func TestUpdateCheck_ETagRoundTripServesCachedResult(t *testing.T) {
	var hits atomic.Int32
	srv := releaseServer(t, "v1.3.0", false, false, &hits)
	c, emitted := newTestChecker(t, "1.2.3", srv.URL)

	if _, err := c.check(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(c.cachePath)
	if err != nil {
		t.Fatalf("cache not written: %v", err)
	}
	for _, key := range []string{`"etag":"\"etag-1\""`, `"tagName":"v1.3.0"`, `"htmlUrl":`, `"checkedAt":"2026-10-04T12:00:00Z"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("cache %s missing %s", b, key)
		}
	}

	info, err := c.check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Fatalf("hits = %d, want 2 (second request must still go out, conditionally)", hits.Load())
	}
	if !info.Available || info.Latest != "1.3.0" {
		t.Fatalf("304 result = %+v, want the cached v1.3.0", info)
	}
	if len(*emitted) != 1 {
		t.Fatalf("emitted %d times, want 1 (same version must not re-emit)", len(*emitted))
	}
}

func TestUpdateCheck_TimeoutErrorsWithoutEmitting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	c, emitted := newTestChecker(t, "1.2.3", srv.URL)
	c.hc = &http.Client{Timeout: 50 * time.Millisecond}

	info, err := c.check(context.Background())
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if info.Available || len(*emitted) != 0 {
		t.Fatalf("timeout must not change status (%+v) or emit (%+v)", info, *emitted)
	}
}

func TestUpdateCheck_DevBuildsNeverRequest(t *testing.T) {
	for _, v := range []string{"dev", "0.0.0-dev.abc1234", "dev (browser)", ""} {
		var hits atomic.Int32
		srv := releaseServer(t, "v9.9.9", false, false, &hits)
		c, emitted := newTestChecker(t, v, srv.URL)
		if c.enabled() {
			t.Errorf("enabled() = true for version %q", v)
		}
		info, err := c.check(context.Background())
		if err != nil || info.Available || hits.Load() != 0 || len(*emitted) != 0 {
			t.Errorf("version %q: err=%v info=%+v hits=%d emitted=%+v", v, err, info, hits.Load(), *emitted)
		}
	}
}

func TestResolveUpdateURL(t *testing.T) {
	if got := resolveUpdateURL(""); got != defaultUpdateURL {
		t.Errorf("empty -> %q, want default", got)
	}
	if got := resolveUpdateURL("off"); got != "" {
		t.Errorf("off -> %q, want disabled", got)
	}
	if got := resolveUpdateURL(" https://mirror.example/latest "); got != "https://mirror.example/latest" {
		t.Errorf("override -> %q", got)
	}
	c, _ := newTestChecker(t, "1.2.3", "")
	if c.enabled() {
		t.Error("a release build with CALENDIUM_UPDATE_URL=off must be disabled")
	}
}

// Review Focus 2: a 200 that is not a usable release must not emit or poison the cache.
func TestUpdateCheck_NonSemverTagOrBadJSONErrorsWithoutEmitOrCache(t *testing.T) {
	for _, body := range []string{`{"tag_name":"nightly","html_url":"https://x","draft":false,"prerelease":false}`, `<html>rate limited</html>`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("ETag", `"bad"`)
			_, _ = fmt.Fprint(w, body)
		}))
		c, emitted := newTestChecker(t, "1.2.3", srv.URL)
		info, err := c.check(context.Background())
		srv.Close()
		if err == nil {
			t.Errorf("body %q: expected an error", body)
		}
		if info.Available || len(*emitted) != 0 {
			t.Errorf("body %q: info=%+v emitted=%+v", body, info, *emitted)
		}
		if _, statErr := os.Stat(c.cachePath); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("body %q: cache must not be written, stat err = %v", body, statErr)
		}
	}
}

// Review Focus 3: a corrupt cache is ignored, not fatal.
func TestUpdateCheck_CorruptCacheIsIgnoredAndRewritten(t *testing.T) {
	var hits atomic.Int32
	var sawConditional atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("If-None-Match") != "" {
			sawConditional.Store(true)
		}
		w.Header().Set("ETag", `"etag-2"`)
		_, _ = fmt.Fprint(w, `{"tag_name":"v1.3.0","html_url":"https://github.com/GuilhermeVozniak/calendium/releases/tag/v1.3.0","draft":false,"prerelease":false}`)
	}))
	t.Cleanup(srv.Close)
	c, emitted := newTestChecker(t, "1.2.3", srv.URL)
	if err := os.WriteFile(c.cachePath, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	info, err := c.check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sawConditional.Load() {
		t.Error("corrupt cache must not produce an If-None-Match header")
	}
	if !info.Available || len(*emitted) != 1 {
		t.Fatalf("info=%+v emitted=%+v", info, *emitted)
	}
	b, _ := os.ReadFile(c.cachePath)
	if !strings.Contains(string(b), `"etag":"\"etag-2\""`) {
		t.Fatalf("cache not rewritten with the fresh ETag: %s", b)
	}
}

func TestParseSemverAndNewer(t *testing.T) {
	parse := []struct {
		in   string
		ok   bool
		want semver
	}{
		{"1.2.3", true, semver{1, 2, 3, ""}},
		{"v1.2.3", true, semver{1, 2, 3, ""}},
		{"1.10.0", true, semver{1, 10, 0, ""}},
		{"1.2.3-rc.1", true, semver{1, 2, 3, "rc.1"}},
		{"0.0.0-dev.abc1234", true, semver{0, 0, 0, "dev.abc1234"}},
		{"1.2.3+build.5", true, semver{1, 2, 3, ""}},
		{"dev", false, semver{}},
		{"1.2", false, semver{}},
		{"1.02.3", false, semver{}},
		{"1.2.3-", false, semver{}},
		{"", false, semver{}},
	}
	for _, tc := range parse {
		got, ok := parseSemver(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("parseSemver(%q) = %+v,%t want %+v,%t", tc.in, got, ok, tc.want, tc.ok)
		}
	}
	cmp := []struct {
		latest, current string
		want            bool
	}{
		{"1.3.0", "1.2.9", true},
		{"1.10.0", "1.9.0", true}, // numeric, not lexical
		{"2.0.0", "1.99.99", true},
		{"1.2.3", "1.2.3", false},
		{"1.2.2", "1.2.3", false},
		{"1.2.3", "1.2.3-rc.1", true},  // release beats prerelease
		{"1.2.3-rc.1", "1.2.3", false}, // prerelease never beats the release
		{"1.2.3-rc.2", "1.2.3-rc.1", true},
	}
	for _, tc := range cmp {
		l, _ := parseSemver(tc.latest)
		c, _ := parseSemver(tc.current)
		if got := newer(l, c); got != tc.want {
			t.Errorf("newer(%s, %s) = %t, want %t", tc.latest, tc.current, got, tc.want)
		}
	}
	for v, want := range map[string]bool{"1.2.3": true, "1.2.3-rc.1": false, "dev": false, "0.0.0-dev.abc1234": false} {
		if got := isReleaseVersion(v); got != want {
			t.Errorf("isReleaseVersion(%q) = %t, want %t", v, got, want)
		}
	}
}
