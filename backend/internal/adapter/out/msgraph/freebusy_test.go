package msgraph

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

func TestClient_FreeBusy_RequestShapeAndStatusFiltering(t *testing.T) {
	var gotBody map[string]any
	var calls int

	_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1.0/me/calendar/getSchedule" {
			t.Errorf("path = %q, want /v1.0/me/calendar/getSchedule", r.URL.Path)
		}
		gotBody = decodeBody(t, r)
		io.WriteString(w, `{"value":[
			{"scheduleId":"alice@example.com","scheduleItems":[
				{"status":"busy","start":{"dateTime":"2026-07-08T09:00:00.0000000","timeZone":"UTC"},
				 "end":{"dateTime":"2026-07-08T09:30:00.0000000","timeZone":"UTC"}},
				{"status":"tentative","start":{"dateTime":"2026-07-08T10:00:00.0000000","timeZone":"UTC"},
				 "end":{"dateTime":"2026-07-08T10:30:00.0000000","timeZone":"UTC"}},
				{"status":"oof","start":{"dateTime":"2026-07-08T11:00:00.0000000","timeZone":"UTC"},
				 "end":{"dateTime":"2026-07-08T11:30:00.0000000","timeZone":"UTC"}},
				{"status":"free","start":{"dateTime":"2026-07-08T12:00:00.0000000","timeZone":"UTC"},
				 "end":{"dateTime":"2026-07-08T12:30:00.0000000","timeZone":"UTC"}},
				{"status":"workingElsewhere","start":{"dateTime":"2026-07-08T13:00:00.0000000","timeZone":"UTC"},
				 "end":{"dateTime":"2026-07-08T13:30:00.0000000","timeZone":"UTC"}}
			]}
		]}`)
	})

	from := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	got, err := c.FreeBusy(context.Background(), "tok", []string{"alice@example.com"}, from, to)
	if err != nil {
		t.Fatalf("FreeBusy: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (under the chunk size)", calls)
	}

	// Request body shape.
	schedules, ok := gotBody["schedules"].([]any)
	if !ok || len(schedules) != 1 || schedules[0] != "alice@example.com" {
		t.Errorf("schedules = %v, want [alice@example.com]", gotBody["schedules"])
	}
	if interval, ok := gotBody["availabilityViewInterval"].(float64); !ok || interval != 30 {
		t.Errorf("availabilityViewInterval = %v, want 30", gotBody["availabilityViewInterval"])
	}
	startTime, ok := gotBody["startTime"].(map[string]any)
	if !ok || startTime["dateTime"] != "2026-07-08T00:00:00" {
		t.Errorf("startTime = %v, want dateTime=2026-07-08T00:00:00", gotBody["startTime"])
	}

	// busy/tentative/oof become busy intervals; free and workingElsewhere are skipped.
	intervals := got["alice@example.com"]
	if len(intervals) != 3 {
		t.Fatalf("intervals = %+v, want 3 (busy, tentative, oof)", intervals)
	}
	wantStart := time.Date(2026, 7, 8, 9, 0, 0, 0, time.UTC)
	if !intervals[0].Start.Equal(wantStart) {
		t.Errorf("intervals[0].Start = %v, want %v", intervals[0].Start, wantStart)
	}
}

func TestClient_FreeBusy_StatusCaseInsensitive(t *testing.T) {
	// Regression test: Graph's freeBusyStatus enum is lowercase
	// (free/tentative/busy/oof/workingElsewhere). A caller must not
	// misreport a differently-cased "Free" as busy, nor a differently-cased
	// "Busy" as free.
	_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"value":[
			{"scheduleId":"alice@example.com","scheduleItems":[
				{"status":"Free","start":{"dateTime":"2026-07-08T09:00:00.0000000","timeZone":"UTC"},
				 "end":{"dateTime":"2026-07-08T09:30:00.0000000","timeZone":"UTC"}},
				{"status":"BUSY","start":{"dateTime":"2026-07-08T10:00:00.0000000","timeZone":"UTC"},
				 "end":{"dateTime":"2026-07-08T10:30:00.0000000","timeZone":"UTC"}},
				{"status":"WorkingElsewhere","start":{"dateTime":"2026-07-08T11:00:00.0000000","timeZone":"UTC"},
				 "end":{"dateTime":"2026-07-08T11:30:00.0000000","timeZone":"UTC"}}
			]}
		]}`)
	})

	from := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
	got, err := c.FreeBusy(context.Background(), "tok", []string{"alice@example.com"}, from, from.Add(time.Hour))
	if err != nil {
		t.Fatalf("FreeBusy: %v", err)
	}
	intervals := got["alice@example.com"]
	if len(intervals) != 1 {
		t.Fatalf("intervals = %+v, want exactly 1 (the BUSY item); Free/WorkingElsewhere must be skipped regardless of case", intervals)
	}
	wantStart := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	if !intervals[0].Start.Equal(wantStart) {
		t.Errorf("interval.Start = %v, want %v (the BUSY item, not Free)", intervals[0].Start, wantStart)
	}
}

func TestClient_FreeBusy_ScheduleIDLowercased(t *testing.T) {
	_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"value":[
			{"scheduleId":"Alice@Example.COM","scheduleItems":[]}
		]}`)
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

func TestClient_FreeBusy_UnresolvableMailboxOmitted(t *testing.T) {
	_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"value":[
			{"scheduleId":"known@example.com","scheduleItems":[]},
			{"scheduleId":"ghost@example.com","error":{"message":"mailbox not found"}}
		]}`)
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
		t.Errorf("ghost@example.com present in result, want omitted (error field set): %v", got)
	}
}

func TestClient_FreeBusy_ChunksAt20Emails(t *testing.T) {
	var calls int
	var sizes []int

	_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		body := decodeBody(t, r)
		schedules, _ := body["schedules"].([]any)
		sizes = append(sizes, len(schedules))
		values := make([]map[string]any, 0, len(schedules))
		for _, s := range schedules {
			values = append(values, map[string]any{"scheduleId": s, "scheduleItems": []any{}})
		}
		b, err := json.Marshal(map[string]any{"value": values})
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
			_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				io.WriteString(w, `{"error":{"code":"badRequest","message":"boom"}}`)
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
	_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"value":[
			{"scheduleId":"alice@example.com","scheduleItems":[
				{"status":"busy","start":{"dateTime":"not-a-time","timeZone":"UTC"},
				 "end":{"dateTime":"2026-07-08T09:30:00.0000000","timeZone":"UTC"}}
			]}
		]}`)
	})

	from := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
	_, err := c.FreeBusy(context.Background(), "tok", []string{"alice@example.com"}, from, from.Add(time.Hour))
	if err == nil {
		t.Fatal("err = nil, want a parse error for the malformed scheduleItem start")
	}
}

func TestClient_FreeBusy_ZeroEmailsMakesNoRequest(t *testing.T) {
	_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
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
