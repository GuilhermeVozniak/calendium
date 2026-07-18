package googleapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func TestClient_FreeBusy_RequestShapeAndBusyParsing(t *testing.T) {
	var gotBody map[string]any
	var calls int

	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/calendar/v3/freeBusy" {
			t.Errorf("path = %q, want /calendar/v3/freeBusy", r.URL.Path)
		}
		gotBody = decodeBody(t, r)
		io.WriteString(w, `{"calendars":{
			"alice@example.com":{"busy":[
				{"start":"2026-07-08T09:00:00Z","end":"2026-07-08T09:30:00Z"}
			]},
			"bob@example.com":{"busy":[]}
		}}`)
	})

	from := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	got, err := c.FreeBusy(context.Background(), "tok", []string{"alice@example.com", "bob@example.com"}, from, to)
	if err != nil {
		t.Fatalf("FreeBusy: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (under the chunk size)", calls)
	}

	// Request body shape: timeMin/timeMax RFC3339, items list of {id}.
	if gotBody["timeMin"] != "2026-07-08T00:00:00Z" {
		t.Errorf("timeMin = %v", gotBody["timeMin"])
	}
	if gotBody["timeMax"] != "2026-07-09T00:00:00Z" {
		t.Errorf("timeMax = %v", gotBody["timeMax"])
	}
	items, ok := gotBody["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("items = %v, want 2 entries", gotBody["items"])
	}
	first, ok := items[0].(map[string]any)
	if !ok || first["id"] != "alice@example.com" {
		t.Errorf("items[0] = %v, want id=alice@example.com", items[0])
	}

	// Busy parsing.
	if len(got["alice@example.com"]) != 1 {
		t.Fatalf("alice busy = %v, want 1 interval", got["alice@example.com"])
	}
	wantStart := time.Date(2026, 7, 8, 9, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 7, 8, 9, 30, 0, 0, time.UTC)
	if iv := got["alice@example.com"][0]; !iv.Start.Equal(wantStart) || !iv.End.Equal(wantEnd) {
		t.Errorf("alice interval = %+v, want [%v, %v]", iv, wantStart, wantEnd)
	}
	if len(got["bob@example.com"]) != 0 {
		t.Errorf("bob busy = %v, want empty (free all day)", got["bob@example.com"])
	}
}

func TestClient_FreeBusy_ResultKeysLowercased(t *testing.T) {
	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"calendars":{
			"Alice@Example.COM":{"busy":[]}
		}}`)
	})

	from := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
	got, err := c.FreeBusy(context.Background(), "tok", []string{"Alice@Example.COM"}, from, from.Add(time.Hour))
	if err != nil {
		t.Fatalf("FreeBusy: %v", err)
	}
	if _, ok := got["alice@example.com"]; !ok {
		t.Errorf("got keys %v, want lowercased alice@example.com", got)
	}
	if _, ok := got["Alice@Example.COM"]; ok {
		t.Errorf("got original-case key present, want only lowercased")
	}
}

func TestClient_FreeBusy_UnresolvableEmailOmitted(t *testing.T) {
	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"calendars":{
			"known@example.com":{"busy":[]},
			"ghost@example.com":{"errors":[{"reason":"notFound"}]}
		}}`)
	})

	from := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
	got, err := c.FreeBusy(context.Background(), "tok", []string{"known@example.com", "ghost@example.com"}, from, from.Add(time.Hour))
	if err != nil {
		t.Fatalf("FreeBusy: %v", err)
	}
	if _, ok := got["known@example.com"]; !ok {
		t.Errorf("known@example.com missing from result: %v", got)
	}
	if _, ok := got["ghost@example.com"]; ok {
		t.Errorf("ghost@example.com present in result, want omitted (errors array): %v", got)
	}
}

func TestClient_FreeBusy_ChunksAt20Emails(t *testing.T) {
	var calls int
	var sizes []int

	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		body := decodeBody(t, r)
		items, _ := body["items"].([]any)
		sizes = append(sizes, len(items))
		cals := map[string]any{}
		for _, it := range items {
			m := it.(map[string]any)
			cals[m["id"].(string)] = map[string]any{"busy": []any{}}
		}
		enc := map[string]any{"calendars": cals}
		b, err := json.Marshal(enc)
		if err != nil {
			t.Fatalf("marshal test response: %v", err)
		}
		if _, err := w.Write(b); err != nil {
			t.Errorf("write test response: %v", err)
		}
	})

	emails := make([]string, 45)
	for i := range emails {
		emails[i] = fmt.Sprintf("user%02d@example.com", i)
	}
	from := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
	got, err := c.FreeBusy(context.Background(), "tok", emails, from, from.Add(time.Hour))
	if err != nil {
		t.Fatalf("FreeBusy: %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3 (45 emails chunked at 20)", calls)
	}
	if len(sizes) != 3 || sizes[0] != 20 || sizes[1] != 20 || sizes[2] != 5 {
		t.Errorf("chunk sizes = %v, want [20 20 5]", sizes)
	}
	if len(got) != 45 {
		t.Errorf("merged result len = %d, want 45", len(got))
	}
}

func TestClient_FreeBusy_ProviderErrorMapsToSentinels(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		want       error
	}{
		{"unauthorized maps to ErrUnauthorized", http.StatusUnauthorized, domain.ErrUnauthorized},
		{"forbidden maps to ErrUnauthorized", http.StatusForbidden, domain.ErrUnauthorized},
		{"server error has no sentinel", http.StatusInternalServerError, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				io.WriteString(w, `{"error":{"code":400,"message":"boom"}}`)
			})
			from := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
			_, err := c.FreeBusy(context.Background(), "tok", []string{"a@example.com"}, from, from.Add(time.Hour))
			if err == nil {
				t.Fatal("err = nil, want error")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want it to wrap %v", err, tc.want)
			}
		})
	}
}

func TestClient_FreeBusy_MalformedTimeSurfacesError(t *testing.T) {
	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"calendars":{
			"alice@example.com":{"busy":[{"start":"not-a-time","end":"2026-07-08T09:30:00Z"}]}
		}}`)
	})

	from := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
	_, err := c.FreeBusy(context.Background(), "tok", []string{"alice@example.com"}, from, from.Add(time.Hour))
	if err == nil {
		t.Fatal("err = nil, want a parse error for the malformed busy-interval start")
	}
}

func TestClient_FreeBusy_ZeroEmailsMakesNoRequest(t *testing.T) {
	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP call for zero emails: %s", r.URL)
	})

	from := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
	got, err := c.FreeBusy(context.Background(), "tok", nil, from, from.Add(time.Hour))
	if err != nil {
		t.Fatalf("FreeBusy: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got = %v, want empty map", got)
	}
}
