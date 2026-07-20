package googleapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func TestClient_CreateEvent_RequestAndMapping(t *testing.T) {
	var gotBody map[string]any
	var gotQuery string

	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		gotBody = decodeBody(t, r)
		// Response carries the Meet link the mapper should surface.
		io.WriteString(w, `{
			"id":"evt1","status":"confirmed","summary":"Standup",
			"start":{"dateTime":"2026-07-08T09:00:00Z"},
			"end":{"dateTime":"2026-07-08T09:30:00Z"},
			"hangoutLink":"https://meet.google.com/abc-defg-hij"
		}`)
	})

	start := time.Date(2026, 7, 8, 9, 0, 0, 0, time.UTC)
	in := domain.EventInput{
		Title:           "Standup",
		Start:           start,
		End:             start.Add(30 * time.Minute),
		RecurrenceRule:  "FREQ=DAILY",
		AttendeeEmails:  []string{"team@example.com"},
		ReminderMinutes: []int{10},
		AddConferencing: true,
	}
	ev, err := c.CreateEvent(context.Background(), "tok", "primary", in)
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}

	// conferenceDataVersion=1 must be on the query.
	if gotQuery != "conferenceDataVersion=1" {
		t.Errorf("query = %q, want conferenceDataVersion=1", gotQuery)
	}
	if gotBody["summary"] != "Standup" {
		t.Errorf("summary = %v", gotBody["summary"])
	}
	// RRULE gets the required "RRULE:" prefix, wrapped in a slice.
	if rec, ok := gotBody["recurrence"].([]any); !ok || len(rec) != 1 || rec[0] != "RRULE:FREQ=DAILY" {
		t.Errorf("recurrence = %v, want [RRULE:FREQ=DAILY]", gotBody["recurrence"])
	}
	if _, ok := gotBody["conferenceData"]; !ok {
		t.Errorf("conferenceData missing from body: %v", gotBody)
	}
	if _, ok := gotBody["reminders"]; !ok {
		t.Errorf("reminders missing from body: %v", gotBody)
	}

	// Response mapping.
	if ev.ProviderEventID != "evt1" || ev.Title != "Standup" {
		t.Errorf("event id/title wrong: %+v", ev)
	}
	if ev.Status != domain.EventConfirmed {
		t.Errorf("Status = %v, want confirmed", ev.Status)
	}
	if !ev.Start.Equal(start) {
		t.Errorf("Start = %v, want %v", ev.Start, start)
	}
	if ev.Conferencing == nil ||
		ev.Conferencing.Provider != domain.ConferencingMeet ||
		ev.Conferencing.URL != "https://meet.google.com/abc-defg-hij" {
		t.Errorf("Conferencing = %+v, want Meet link", ev.Conferencing)
	}
}

func TestClient_SyncCalendars_Pagination(t *testing.T) {
	calls := 0
	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if !strings.HasSuffix(r.URL.Path, "/users/me/calendarList") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.URL.Query().Get("pageToken") == "" {
			io.WriteString(w, `{"items":[
				{"id":"cal1","summary":"Primary","primary":true,"accessRole":"owner"},
				{"id":"cal2","summary":"Shared","accessRole":"reader"}
			],"nextPageToken":"P2"}`)
			return
		}
		if r.URL.Query().Get("pageToken") != "P2" {
			t.Errorf("pageToken = %q, want P2", r.URL.Query().Get("pageToken"))
		}
		io.WriteString(w, `{"items":[
			{"id":"cal3","summary":"Team","accessRole":"writer"}
		]}`)
	})

	cals, err := c.SyncCalendars(context.Background(), "tok")
	if err != nil {
		t.Fatalf("SyncCalendars: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (both pages fetched)", calls)
	}
	if len(cals) != 3 {
		t.Fatalf("Calendars len = %d, want 3", len(cals))
	}
	byID := map[string]domain.Calendar{}
	for _, cal := range cals {
		byID[cal.ProviderCalendarID] = cal
	}
	if !byID["cal1"].CanWrite || !byID["cal1"].IsPrimary {
		t.Errorf("cal1 = %+v, want CanWrite+IsPrimary", byID["cal1"])
	}
	if byID["cal2"].CanWrite {
		t.Errorf("cal2 = %+v, want CanWrite=false (reader)", byID["cal2"])
	}
	if byID["cal2"].IsPrimary {
		t.Errorf("cal2 IsPrimary = true, want false")
	}
	if !byID["cal3"].CanWrite {
		t.Errorf("cal3 = %+v, want CanWrite=true (writer)", byID["cal3"])
	}
	for id, cal := range byID {
		if !cal.IsVisible {
			t.Errorf("%s IsVisible = false, want true", id)
		}
	}
}

func TestClient_SyncEvents_InitialSyncWithCancelled(t *testing.T) {
	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"items":[
			{"id":"e-cancel","status":"cancelled"},
			{"id":"e1","status":"confirmed","summary":"Kickoff",
			 "start":{"dateTime":"2026-07-08T09:00:00Z"},
			 "end":{"dateTime":"2026-07-08T09:30:00Z"}}
		],"nextSyncToken":"S1"}`)
	})

	page, err := c.SyncEvents(context.Background(), "tok", "primary", "")
	if err != nil {
		t.Fatalf("SyncEvents: %v", err)
	}
	if len(page.DeletedIDs) != 1 || page.DeletedIDs[0] != "e-cancel" {
		t.Errorf("DeletedIDs = %v, want [e-cancel]", page.DeletedIDs)
	}
	if len(page.Events) != 1 || page.Events[0].ProviderEventID != "e1" {
		t.Errorf("Events = %+v, want [e1]", page.Events)
	}
	if page.NextCursor != "sync:S1" {
		t.Errorf("NextCursor = %q, want sync:S1", page.NextCursor)
	}
	if page.HasMore {
		t.Errorf("HasMore = true, want false")
	}
}

func TestClient_SyncEvents_NextPageToken(t *testing.T) {
	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"items":[],"nextPageToken":"P1"}`)
	})

	page, err := c.SyncEvents(context.Background(), "tok", "primary", "")
	if err != nil {
		t.Fatalf("SyncEvents: %v", err)
	}
	if page.NextCursor != "page:P1" || !page.HasMore {
		t.Errorf("cursor=%q hasMore=%v, want page:P1/true", page.NextCursor, page.HasMore)
	}
}

func TestClient_SyncEvents_ExpiredSyncTokenRestarts(t *testing.T) {
	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("syncToken") == "old" {
			w.WriteHeader(http.StatusGone)
			io.WriteString(w, `{"error":{"code":410,"message":"sync token expired"}}`)
			return
		}
		io.WriteString(w, `{"items":[
			{"id":"e1","status":"confirmed","summary":"Fresh",
			 "start":{"dateTime":"2026-07-08T09:00:00Z"},
			 "end":{"dateTime":"2026-07-08T09:30:00Z"}}
		],"nextSyncToken":"S9"}`)
	})

	page, err := c.SyncEvents(context.Background(), "tok", "primary", "sync:old")
	if err != nil {
		t.Fatalf("SyncEvents: %v", err)
	}
	if len(page.Events) != 1 || page.Events[0].ProviderEventID != "e1" {
		t.Errorf("Events = %+v, want [e1] from full resync", page.Events)
	}
	if page.NextCursor != "sync:S9" {
		t.Errorf("NextCursor = %q, want sync:S9", page.NextCursor)
	}
}

func TestClient_SyncEvents_BogusCursor(t *testing.T) {
	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP call for bogus cursor: %s", r.URL)
	})

	_, err := c.SyncEvents(context.Background(), "tok", "primary", "bogus")
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}

func TestClient_CreateEvent_AllDay(t *testing.T) {
	var gotBody map[string]any

	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody = decodeBody(t, r)
		io.WriteString(w, `{"id":"evt2","status":"confirmed","summary":"Off",
			"start":{"date":"2026-07-08"},"end":{"date":"2026-07-09"}}`)
	})

	start := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
	in := domain.EventInput{Title: "Off", Start: start, End: start.AddDate(0, 0, 1), AllDay: true}
	if _, err := c.CreateEvent(context.Background(), "tok", "primary", in); err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}

	startMap, ok := gotBody["start"].(map[string]any)
	if !ok {
		t.Fatalf("start = %v, want object", gotBody["start"])
	}
	if _, ok := startMap["date"]; !ok {
		t.Errorf("start missing 'date' key: %v", startMap)
	}
	if _, ok := startMap["dateTime"]; ok {
		t.Errorf("start has 'dateTime' key, want date-only: %v", startMap)
	}
}

func TestClient_UpdateEvent_PartialPatch(t *testing.T) {
	var gotBody map[string]any

	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody = decodeBody(t, r)
		io.WriteString(w, `{"id":"evt1","status":"confirmed","summary":"New title",
			"start":{"dateTime":"2026-07-08T09:00:00Z"},"end":{"dateTime":"2026-07-08T09:30:00Z"}}`)
	})

	title := "New title"
	start := time.Date(2026, 7, 8, 9, 0, 0, 0, time.UTC)
	patch := domain.EventPatch{Title: &title, Start: &start}
	if _, err := c.UpdateEvent(context.Background(), "tok", "primary", "evt1", patch); err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if _, ok := gotBody["summary"]; !ok {
		t.Errorf("summary missing from patch body: %v", gotBody)
	}
	if _, ok := gotBody["start"]; !ok {
		t.Errorf("start missing from patch body: %v", gotBody)
	}
	if _, ok := gotBody["description"]; ok {
		t.Errorf("description present in patch body, want absent: %v", gotBody)
	}
	if _, ok := gotBody["location"]; ok {
		t.Errorf("location present in patch body, want absent: %v", gotBody)
	}

	t.Run("clear recurrence", func(t *testing.T) {
		var gotBody2 map[string]any
		_, c2 := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
			gotBody2 = decodeBody(t, r)
			io.WriteString(w, `{"id":"evt1","status":"confirmed"}`)
		})
		empty := ""
		patch := domain.EventPatch{RecurrenceRule: &empty}
		if _, err := c2.UpdateEvent(context.Background(), "tok", "primary", "evt1", patch); err != nil {
			t.Fatalf("UpdateEvent: %v", err)
		}
		rec, ok := gotBody2["recurrence"].([]any)
		if !ok || len(rec) != 0 {
			t.Errorf("recurrence = %v, want empty array", gotBody2["recurrence"])
		}
	})
}

func TestClient_DeleteEvent(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		wantErr    bool
	}{
		{"not found is idempotent", http.StatusNotFound, false},
		{"gone is idempotent", http.StatusGone, false},
		{"server error propagates", http.StatusInternalServerError, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
			})
			err := c.DeleteEvent(context.Background(), "tok", "primary", "evt1")
			if tc.wantErr && err == nil {
				t.Errorf("err = nil, want error for status %d", tc.statusCode)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("err = %v, want nil for status %d", err, tc.statusCode)
			}
		})
	}
}

func TestClient_RSVP_Accepted(t *testing.T) {
	var gotPatchBody map[string]any
	var sawPatch bool

	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			io.WriteString(w, `{"id":"evt1","attendees":[
				{"email":"other@x.com"},
				{"email":"me@x.com","self":true,"responseStatus":"needsAction"}
			]}`)
		case http.MethodPatch:
			sawPatch = true
			gotPatchBody = decodeBody(t, r)
		default:
			t.Errorf("unexpected method %q", r.Method)
		}
	})

	if err := c.RSVP(context.Background(), "tok", "primary", "evt1", domain.RsvpAccepted, ""); err != nil {
		t.Fatalf("RSVP: %v", err)
	}
	if !sawPatch {
		t.Fatal("expected a PATCH request")
	}
	attendees, ok := gotPatchBody["attendees"].([]any)
	if !ok || len(attendees) != 2 {
		t.Fatalf("attendees = %v, want 2 entries", gotPatchBody["attendees"])
	}
	self, ok := attendees[1].(map[string]any)
	if !ok || self["email"] != "me@x.com" || self["responseStatus"] != "accepted" {
		t.Errorf("self attendee = %v, want accepted", attendees[1])
	}
	if _, has := self["comment"]; has {
		t.Errorf("comment key present on an empty-comment RSVP: %v", self)
	}
}

func TestRSVPSendsComment(t *testing.T) {
	var gotPatchBody map[string]any
	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			io.WriteString(w, `{"id":"evt1","attendees":[
				{"email":"other@x.com"},
				{"email":"me@x.com","self":true,"responseStatus":"needsAction"}
			]}`)
		case http.MethodPatch:
			gotPatchBody = decodeBody(t, r)
		default:
			t.Errorf("unexpected method %q", r.Method)
		}
	})

	if err := c.RSVP(context.Background(), "tok", "primary", "evt1", domain.RsvpDeclined, "On PTO until Monday."); err != nil {
		t.Fatalf("RSVP: %v", err)
	}
	attendees, ok := gotPatchBody["attendees"].([]any)
	if !ok || len(attendees) != 2 {
		t.Fatalf("attendees = %v, want 2 entries", gotPatchBody["attendees"])
	}
	self, ok := attendees[1].(map[string]any)
	if !ok || self["responseStatus"] != "declined" || self["comment"] != "On PTO until Monday." {
		t.Errorf("self attendee = %v, want declined with comment", attendees[1])
	}
	other, ok := attendees[0].(map[string]any)
	if !ok {
		t.Fatalf("other attendee = %v", attendees[0])
	}
	if _, has := other["comment"]; has {
		t.Errorf("comment leaked onto another attendee: %v", other)
	}
}

func TestClient_RSVP_NoSelfAttendee(t *testing.T) {
	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			t.Errorf("unexpected PATCH request; caller is not an attendee")
		}
		io.WriteString(w, `{"id":"evt1","attendees":[{"email":"other@x.com"}]}`)
	})

	err := c.RSVP(context.Background(), "tok", "primary", "evt1", domain.RsvpAccepted, "")
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}

func TestMapGcalResponse(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want domain.RsvpStatus
	}{
		{"accepted", "accepted", domain.RsvpAccepted},
		{"declined", "declined", domain.RsvpDeclined},
		{"tentative", "tentative", domain.RsvpTentative},
		{"needsAction maps directly", "needsAction", domain.RsvpNeedsAction},
		{"empty string falls back to needsAction", "", domain.RsvpNeedsAction},
		{"unrecognized value falls back to needsAction", "bogus", domain.RsvpNeedsAction},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapGcalResponse(tc.in); got != tc.want {
				t.Errorf("mapGcalResponse(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestClient_SyncEvents_AllDayParsing(t *testing.T) {
	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"items":[
			{"id":"e-allday","status":"confirmed","summary":"Holiday",
			 "start":{"date":"2026-03-01"},"end":{"date":"2026-03-02"}}
		],"nextSyncToken":"S1"}`)
	})

	page, err := c.SyncEvents(context.Background(), "tok", "primary", "")
	if err != nil {
		t.Fatalf("SyncEvents: %v", err)
	}
	if len(page.Events) != 1 {
		t.Fatalf("Events len = %d, want 1", len(page.Events))
	}
	ev := page.Events[0]
	if !ev.AllDay {
		t.Errorf("AllDay = false, want true")
	}
	want := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if !ev.Start.Equal(want) {
		t.Errorf("Start = %v, want %v", ev.Start, want)
	}
}
