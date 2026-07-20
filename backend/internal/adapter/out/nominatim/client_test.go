package nominatim

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// unpaced disables request spacing so tests never really sleep.
func unpaced(c *Client) *Client {
	c.sleep = func(time.Duration) {}
	return c
}

func TestAutocompleteMapsAndEncodes(t *testing.T) {
	var gotPath, gotRawQuery, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRawQuery = r.URL.RawQuery
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[
			{"name":"Alexanderplatz","display_name":"Alexanderplatz, Mitte, Berlin, Germany","lat":"52.5219","lon":"13.4132"},
			{"name":"","display_name":"Café & Bar, Paris, France","lat":"48.85","lon":"2.35"},
			{"name":"Broken","display_name":"Broken row","lat":"not-a-float","lon":"0"}
		]`)
	}))
	defer srv.Close()

	c := unpaced(New(srv.URL+"/", "", srv.Client()))
	got, err := c.Autocomplete(context.Background(), "café & bar berlin", 5)
	if err != nil {
		t.Fatalf("Autocomplete: %v", err)
	}

	if gotPath != "/search" {
		t.Fatalf("path = %q, want /search", gotPath)
	}
	if gotUA != "calendium/1.0" {
		t.Fatalf("User-Agent = %q, want calendium/1.0", gotUA)
	}
	q, err := url.ParseQuery(gotRawQuery)
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	if q.Get("format") != "jsonv2" || q.Get("limit") != "5" {
		t.Fatalf("query = %q, want format=jsonv2 limit=5", gotRawQuery)
	}
	if q.Get("q") != "café & bar berlin" {
		t.Fatalf("q = %q, want the round-tripped user query", q.Get("q"))
	}
	// The raw wire form must be percent-encoded (M2.5 lesson): no raw
	// multibyte chars or bare ampersands from user input.
	if !strings.Contains(gotRawQuery, "caf%C3%A9") || !strings.Contains(gotRawQuery, "%26") {
		t.Fatalf("user query not percent-encoded on the wire: %q", gotRawQuery)
	}

	// Mapping: the malformed row is dropped; the empty-name row falls back
	// to the first display_name segment.
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (malformed row dropped): %+v", len(got), got)
	}
	want0 := domain.Place{Name: "Alexanderplatz", Address: "Alexanderplatz, Mitte, Berlin, Germany", Lat: 52.5219, Lon: 13.4132}
	if got[0] != want0 {
		t.Fatalf("got[0] = %+v, want %+v", got[0], want0)
	}
	if got[1].Name != "Café & Bar" || got[1].Lat != 48.85 || got[1].Lon != 2.35 {
		t.Fatalf("got[1] = %+v", got[1])
	}
}

func TestAutocompleteRespectsLimit(t *testing.T) {
	var gotLimit string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLimit = r.URL.Query().Get("limit")
		// A misbehaving vendor returning more rows than asked for.
		fmt.Fprint(w, `[
			{"name":"A","display_name":"A","lat":"1","lon":"1"},
			{"name":"B","display_name":"B","lat":"2","lon":"2"},
			{"name":"C","display_name":"C","lat":"3","lon":"3"}
		]`)
	}))
	defer srv.Close()

	c := unpaced(New(srv.URL, "", srv.Client()))
	got, err := c.Autocomplete(context.Background(), "abc", 2)
	if err != nil {
		t.Fatalf("Autocomplete: %v", err)
	}
	if gotLimit != "2" {
		t.Fatalf("limit param = %q, want 2", gotLimit)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (client-side truncation)", len(got))
	}
}

func TestAutocompleteErrorStatuses(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		c := unpaced(New(srv.URL, "", srv.Client()))
		if _, err := c.Autocomplete(context.Background(), "berlin", 5); err == nil {
			t.Fatalf("status %d: err = nil, want error", status)
		}
		srv.Close()
	}
}

func TestAutocompleteRejectsInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"not": "an array"`)
	}))
	defer srv.Close()
	c := unpaced(New(srv.URL, "", srv.Client()))
	if _, err := c.Autocomplete(context.Background(), "berlin", 5); err == nil {
		t.Fatal("err = nil, want decode error")
	}
}

func TestAutocompleteSerializesAndPacesRequests(t *testing.T) {
	var inFlight, overlaps int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&inFlight, 1) > 1 {
			atomic.AddInt32(&overlaps, 1)
		}
		time.Sleep(2 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		fmt.Fprint(w, `[]`)
	}))
	defer srv.Close()

	c := New(srv.URL, "", srv.Client())
	var mu sync.Mutex
	var sleeps []time.Duration
	c.sleep = func(d time.Duration) {
		mu.Lock()
		sleeps = append(sleeps, d)
		mu.Unlock()
	}
	// Frozen clock: with zero elapsed time between requests, every call
	// after the first must pace a full minSpacing.
	base := time.Unix(1_000_000, 0)
	c.now = func() time.Time { return base }

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Autocomplete(context.Background(), "berlin", 5); err != nil {
				t.Errorf("Autocomplete: %v", err)
			}
		}()
	}
	wg.Wait()

	if overlaps != 0 {
		t.Fatalf("observed %d overlapping vendor requests, want fully serialized", overlaps)
	}
	if len(sleeps) != 3 {
		t.Fatalf("sleep calls = %d, want 3 (every call after the first paces)", len(sleeps))
	}
	for _, d := range sleeps {
		if d != minSpacing {
			t.Fatalf("paced %v, want %v", d, minSpacing)
		}
	}
}

func TestTravelTimeDrivingWalkingTransit(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		fmt.Fprint(w, `{"code":"Ok","routes":[{"duration":3600}]}`)
	}))
	defer srv.Close()
	c := New("", srv.URL+"/", srv.Client())
	ctx := context.Background()

	d, err := c.TravelTime(ctx, 52.5, 13.4, 48.85, 2.35, domain.TravelDriving)
	if err != nil {
		t.Fatalf("TravelTime driving: %v", err)
	}
	if d != time.Hour {
		t.Fatalf("driving duration = %v, want 1h", d)
	}
	// OSRM coordinate order is {lon},{lat}.
	if gotPath != "/route/v1/driving/13.4,52.5;2.35,48.85" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotQuery != "overview=false" {
		t.Fatalf("query = %q, want overview=false", gotQuery)
	}

	d, err = c.TravelTime(ctx, 52.5, 13.4, 48.85, 2.35, domain.TravelWalking)
	if err != nil {
		t.Fatalf("TravelTime walking: %v", err)
	}
	if d != time.Hour {
		t.Fatalf("walking duration = %v, want 1h", d)
	}
	if !strings.HasPrefix(gotPath, "/route/v1/walking/") {
		t.Fatalf("walking path = %q, want /route/v1/walking/…", gotPath)
	}

	// Transit approximates driving * 1.5 (no transit vendor yet).
	d, err = c.TravelTime(ctx, 52.5, 13.4, 48.85, 2.35, domain.TravelTransit)
	if err != nil {
		t.Fatalf("TravelTime transit: %v", err)
	}
	if d != 90*time.Minute {
		t.Fatalf("transit duration = %v, want 1h30m", d)
	}
	if !strings.HasPrefix(gotPath, "/route/v1/driving/") {
		t.Fatalf("transit path = %q, want the driving profile", gotPath)
	}
}

func TestTravelTimeErrors(t *testing.T) {
	t.Run("non-Ok code", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `{"code":"NoRoute","routes":[]}`)
		}))
		defer srv.Close()
		c := New("", srv.URL, srv.Client())
		if _, err := c.TravelTime(context.Background(), 1, 1, 2, 2, domain.TravelDriving); err == nil {
			t.Fatal("err = nil, want no-route error")
		}
	})
	t.Run("http error status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))
		defer srv.Close()
		c := New("", srv.URL, srv.Client())
		if _, err := c.TravelTime(context.Background(), 1, 1, 2, 2, domain.TravelDriving); err == nil {
			t.Fatal("err = nil, want error")
		}
	})
	t.Run("unknown mode", func(t *testing.T) {
		c := New("", "http://example.invalid", nil)
		if _, err := c.TravelTime(context.Background(), 1, 1, 2, 2, domain.TravelMode("teleport")); err == nil {
			t.Fatal("err = nil, want validation error")
		}
	})
	t.Run("unconfigured base URL", func(t *testing.T) {
		c := New("http://example.invalid", "", nil)
		if _, err := c.TravelTime(context.Background(), 1, 1, 2, 2, domain.TravelDriving); err == nil {
			t.Fatal("err = nil, want unconfigured error")
		}
	})
}
