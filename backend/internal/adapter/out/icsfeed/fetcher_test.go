package icsfeed

import (
	"context"
	"net/http"
	"net/http/httptest"
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

	cal, etag, notModified, err := New(nil).Fetch(context.Background(), srv.URL, "")
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

	cal, etag, notModified, err := New(nil).Fetch(context.Background(), srv.URL, `"v1"`)
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

	_, _, _, err := New(nil).Fetch(context.Background(), srv.URL, "")
	if err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("err = %v, want 1MiB cap error", err)
	}
}

func TestFetchRejectsNonIcsContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>Sign in required</body></html>"))
	}))
	defer srv.Close()

	_, _, _, err := New(nil).Fetch(context.Background(), srv.URL, "")
	if err == nil || !strings.Contains(err.Error(), "not an ICS calendar") {
		t.Fatalf("err = %v, want not-an-ICS error", err)
	}
}

func TestFetchRejectsErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()

	_, _, _, err := New(nil).Fetch(context.Background(), srv.URL, "")
	if err == nil || !strings.Contains(err.Error(), "status 404") {
		t.Fatalf("err = %v, want status error", err)
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
