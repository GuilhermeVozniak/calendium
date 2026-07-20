package msgraph

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func TestClient_SyncEvents_Delta(t *testing.T) {
	var deltaLink string

	const events = `{"value":[
		{"id":"e1","subject":"Review","showAs":"busy","sensitivity":"normal",
		 "start":{"dateTime":"2026-07-08T09:00:00.0000000","timeZone":"UTC"},
		 "end":{"dateTime":"2026-07-08T10:00:00.0000000","timeZone":"UTC"},
		 "isReminderOn":true,"reminderMinutesBeforeStart":15,
		 "onlineMeeting":{"joinUrl":"https://teams.microsoft.com/l/meetup/xyz"}},
		{"id":"e2","isCancelled":true},
		{"id":"e3","@removed":{"reason":"deleted"}}
	]`

	srv, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/calendarView/delta") {
			io.WriteString(w, fmt.Sprintf(`%s,"@odata.deltaLink":%q}`, events, deltaLink))
			return
		}
		t.Errorf("unexpected path %q", r.URL.Path)
		http.NotFound(w, r)
	})
	deltaLink = srv.URL + "/v1.0/me/calendars/cal1/calendarView/delta?$deltatoken=zzz"

	page, err := c.SyncEvents(context.Background(), "tok", "cal1", "")
	if err != nil {
		t.Fatalf("SyncEvents: %v", err)
	}

	if page.NextCursor != deltaLink || page.HasMore {
		t.Errorf("cursor=%q hasMore=%v, want deltaLink + false", page.NextCursor, page.HasMore)
	}
	// e2 (isCancelled) and e3 (@removed) become tombstones.
	if len(page.DeletedIDs) != 2 {
		t.Errorf("DeletedIDs = %v, want [e2 e3]", page.DeletedIDs)
	}
	if len(page.Events) != 1 {
		t.Fatalf("Events len = %d, want 1", len(page.Events))
	}
	ev := page.Events[0]
	if ev.ProviderEventID != "e1" || ev.Title != "Review" {
		t.Errorf("event id/title wrong: %+v", ev)
	}
	if want := time.Date(2026, 7, 8, 9, 0, 0, 0, time.UTC); !ev.Start.Equal(want) {
		t.Errorf("Start = %v, want %v (fractional-second layout)", ev.Start, want)
	}
	if ev.Status != domain.EventConfirmed {
		t.Errorf("Status = %v, want confirmed", ev.Status)
	}
	if len(ev.ReminderMinutes) != 1 || ev.ReminderMinutes[0] != 15 {
		t.Errorf("ReminderMinutes = %v, want [15]", ev.ReminderMinutes)
	}
	if ev.Conferencing == nil ||
		ev.Conferencing.Provider != domain.ConferencingTeams ||
		ev.Conferencing.URL != "https://teams.microsoft.com/l/meetup/xyz" {
		t.Errorf("Conferencing = %+v, want Teams join url", ev.Conferencing)
	}
}

func TestClient_SyncCalendars_Pagination(t *testing.T) {
	calls := 0
	_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if !strings.HasSuffix(r.URL.Path, "/me/calendars") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if calls == 1 {
			if got := r.URL.Query().Get("$top"); got != "100" {
				t.Errorf("$top = %q, want 100", got)
			}
			fmt.Fprintf(w, `{"value":[
				{"id":"cal1","name":"Calendar","isDefaultCalendar":true,"canEdit":true,"hexColor":"#112233"},
				{"id":"cal2","name":"Shared","canEdit":false}
			],"@odata.nextLink":"http://%s/v1.0/me/calendars?$top=100&$skip=2"}`, r.Host)
			return
		}
		io.WriteString(w, `{"value":[{"id":"cal3","name":"Team","canEdit":true}]}`)
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
	if byID["cal1"].Color != "#112233" {
		t.Errorf("cal1.Color = %q, want #112233", byID["cal1"].Color)
	}
	if byID["cal2"].CanWrite {
		t.Errorf("cal2.CanWrite = true, want false")
	}
	if byID["cal2"].IsPrimary {
		t.Errorf("cal2.IsPrimary = true, want false")
	}
	if !byID["cal3"].CanWrite {
		t.Errorf("cal3.CanWrite = false, want true")
	}
	for id, cal := range byID {
		if !cal.IsVisible {
			t.Errorf("%s IsVisible = false, want true", id)
		}
	}
}

func TestClient_SyncEvents_ContinuationCursor(t *testing.T) {
	var nextLink string

	srv, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/calendarView/delta") {
			t.Errorf("unexpected path %q", r.URL.Path)
			return
		}
		fmt.Fprintf(w, `{"value":[],"@odata.nextLink":%q}`, nextLink)
	})
	cursor := srv.URL + "/v1.0/me/calendars/cal1/calendarView/delta?$deltatoken=abc"
	nextLink = srv.URL + "/v1.0/me/calendars/cal1/calendarView/delta?$skiptoken=p2"

	page, err := c.SyncEvents(context.Background(), "tok", "cal1", cursor)
	if err != nil {
		t.Fatalf("SyncEvents: %v", err)
	}
	if !page.HasMore {
		t.Errorf("HasMore = false, want true")
	}
	if page.NextCursor != nextLink {
		t.Errorf("NextCursor = %q, want %q", page.NextCursor, nextLink)
	}
}

func TestClient_SyncEvents_ExpiredDeltaRestarts(t *testing.T) {
	_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/calendarView/delta") {
			t.Errorf("unexpected path %q", r.URL.Path)
			return
		}
		if r.URL.Query().Get("$deltatoken") == "stale" {
			w.WriteHeader(http.StatusGone)
			io.WriteString(w, `{"error":{"code":"ResyncRequired","message":"token expired"}}`)
			return
		}
		// Fresh restart: startDateTime/endDateTime query, no deltatoken.
		if r.URL.Query().Get("startDateTime") == "" || r.URL.Query().Get("endDateTime") == "" {
			t.Errorf("restart request missing startDateTime/endDateTime: %s", r.URL.RawQuery)
		}
		io.WriteString(w, `{"value":[],"@odata.deltaLink":"https://graph.microsoft.com/v1.0/fresh"}`)
	})

	cursor := "https://graph.microsoft.com/v1.0/me/calendars/cal1/calendarView/delta?$deltatoken=stale"
	_, err := c.SyncEvents(context.Background(), "tok", "cal1", cursor)
	if err != nil {
		t.Fatalf("SyncEvents: %v", err)
	}
}

func TestClient_CreateEvent_FullRequestAndMapping(t *testing.T) {
	var gotBody map[string]any

	_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody = decodeBody(t, r)
		io.WriteString(w, `{
			"id":"evt1","subject":"Planning","showAs":"busy",
			"start":{"dateTime":"2026-07-08T09:00:00.0000000","timeZone":"UTC"},
			"end":{"dateTime":"2026-07-08T10:00:00.0000000","timeZone":"UTC"},
			"onlineMeetingUrl":"https://teams.microsoft.com/l/meetup/abc"
		}`)
	})

	start := time.Date(2026, 7, 8, 9, 0, 0, 0, time.UTC)
	in := domain.EventInput{
		Title:           "Planning",
		Start:           start,
		End:             start.Add(time.Hour),
		Description:     "Agenda",
		Location:        "Room 1",
		AttendeeEmails:  []string{"a@x.com"},
		RecurrenceRule:  "FREQ=WEEKLY;BYDAY=MO",
		ReminderMinutes: []int{15},
		AddConferencing: true,
	}
	ev, err := c.CreateEvent(context.Background(), "tok", "cal1", in)
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}

	if gotBody["subject"] != "Planning" {
		t.Errorf("subject = %v, want Planning", gotBody["subject"])
	}
	startMap, ok := gotBody["start"].(map[string]any)
	if !ok || startMap["dateTime"] != "2026-07-08T09:00:00" {
		t.Errorf("start = %v, want dateTime 2026-07-08T09:00:00", gotBody["start"])
	}
	endMap, ok := gotBody["end"].(map[string]any)
	if !ok || endMap["dateTime"] != "2026-07-08T10:00:00" {
		t.Errorf("end = %v, want dateTime 2026-07-08T10:00:00", gotBody["end"])
	}
	if gotBody["isAllDay"] != false {
		t.Errorf("isAllDay = %v, want false", gotBody["isAllDay"])
	}
	bodyMap, ok := gotBody["body"].(map[string]any)
	if !ok || bodyMap["contentType"] != "HTML" {
		t.Errorf("body = %v, want contentType HTML", gotBody["body"])
	}
	locMap, ok := gotBody["location"].(map[string]any)
	if !ok || locMap["displayName"] != "Room 1" {
		t.Errorf("location = %v, want displayName Room 1", gotBody["location"])
	}
	attendees, ok := gotBody["attendees"].([]any)
	if !ok || len(attendees) != 1 {
		t.Fatalf("attendees = %v, want 1 entry", gotBody["attendees"])
	}
	att, ok := attendees[0].(map[string]any)
	if !ok || att["type"] != "required" {
		t.Errorf("attendees[0].type = %v, want required", att["type"])
	}
	if gotBody["recurrence"] == nil {
		t.Errorf("recurrence missing from body")
	}
	if gotBody["isReminderOn"] != true || gotBody["reminderMinutesBeforeStart"] != float64(15) {
		t.Errorf("reminder fields = isReminderOn=%v reminderMinutesBeforeStart=%v, want true/15",
			gotBody["isReminderOn"], gotBody["reminderMinutesBeforeStart"])
	}
	if gotBody["isOnlineMeeting"] != true || gotBody["onlineMeetingProvider"] != "teamsForBusiness" {
		t.Errorf("online meeting fields = isOnlineMeeting=%v onlineMeetingProvider=%v, want true/teamsForBusiness",
			gotBody["isOnlineMeeting"], gotBody["onlineMeetingProvider"])
	}

	if ev.Conferencing == nil || ev.Conferencing.Provider != domain.ConferencingTeams {
		t.Errorf("Conferencing = %+v, want Teams", ev.Conferencing)
	}
}

func TestClient_CreateEvent_AllDay(t *testing.T) {
	var gotBody map[string]any

	_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody = decodeBody(t, r)
		io.WriteString(w, `{"id":"evt2","subject":"Off",
			"start":{"dateTime":"2026-07-08T00:00:00.0000000","timeZone":"UTC"},
			"end":{"dateTime":"2026-07-09T00:00:00.0000000","timeZone":"UTC"}}`)
	})

	start := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
	in := domain.EventInput{Title: "Off", Start: start, End: start.AddDate(0, 0, 1), AllDay: true}
	if _, err := c.CreateEvent(context.Background(), "tok", "cal1", in); err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}

	if gotBody["isAllDay"] != true {
		t.Errorf("isAllDay = %v, want true", gotBody["isAllDay"])
	}
	startMap, ok := gotBody["start"].(map[string]any)
	if !ok || startMap["dateTime"] != "2026-07-08T00:00:00" {
		t.Errorf("start.dateTime = %v, want 2026-07-08T00:00:00 (midnight layout)", gotBody["start"])
	}
}

func TestClient_UpdateEvent_PartialPatch(t *testing.T) {
	var gotBody map[string]any

	_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody = decodeBody(t, r)
		io.WriteString(w, `{"id":"evt1","subject":"New title"}`)
	})

	title := "New title"
	loc := "New room"
	patch := domain.EventPatch{Title: &title, Location: &loc}
	if _, err := c.UpdateEvent(context.Background(), "tok", "cal1", "evt1", patch); err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if _, ok := gotBody["subject"]; !ok {
		t.Errorf("subject missing from patch body: %v", gotBody)
	}
	if _, ok := gotBody["location"]; !ok {
		t.Errorf("location missing from patch body: %v", gotBody)
	}
	if _, ok := gotBody["start"]; ok {
		t.Errorf("start present in patch body, want absent: %v", gotBody)
	}
	if _, ok := gotBody["end"]; ok {
		t.Errorf("end present in patch body, want absent: %v", gotBody)
	}

	t.Run("clear recurrence", func(t *testing.T) {
		var gotBody2 map[string]any
		_, c2 := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
			gotBody2 = decodeBody(t, r)
			io.WriteString(w, `{"id":"evt1"}`)
		})
		empty := ""
		patch := domain.EventPatch{RecurrenceRule: &empty}
		if _, err := c2.UpdateEvent(context.Background(), "tok", "cal1", "evt1", patch); err != nil {
			t.Fatalf("UpdateEvent: %v", err)
		}
		if v, ok := gotBody2["recurrence"]; !ok || v != nil {
			t.Errorf("recurrence = %v (present=%v), want JSON null", v, ok)
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
			_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
			})
			err := c.DeleteEvent(context.Background(), "tok", "cal1", "evt1")
			if tc.wantErr && err == nil {
				t.Errorf("err = nil, want error for status %d", tc.statusCode)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("err = %v, want nil for status %d", err, tc.statusCode)
			}
		})
	}
}

func TestClient_RSVP(t *testing.T) {
	cases := []struct {
		name     string
		response domain.RsvpStatus
		comment  string
		action   string
	}{
		{"accepted", domain.RsvpAccepted, "", "accept"},
		{"declined", domain.RsvpDeclined, "", "decline"},
		{"tentative", domain.RsvpTentative, "", "tentativelyAccept"},
		{"declined with comment", domain.RsvpDeclined, "Out of office this week.", "decline"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			var gotBody map[string]any
			_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotBody = decodeBody(t, r)
			})
			if err := c.RSVP(context.Background(), "tok", "cal1", "evt1", tc.response, tc.comment); err != nil {
				t.Fatalf("RSVP: %v", err)
			}
			if !strings.HasSuffix(gotPath, "/me/events/evt1/"+tc.action) {
				t.Errorf("path = %q, want suffix /me/events/evt1/%s", gotPath, tc.action)
			}
			if gotBody["sendResponse"] != true {
				t.Errorf("sendResponse = %v, want true", gotBody["sendResponse"])
			}
			if tc.comment == "" {
				if _, has := gotBody["comment"]; has {
					t.Errorf("comment key present on an empty-comment RSVP: %v", gotBody)
				}
			} else if gotBody["comment"] != tc.comment {
				t.Errorf("comment = %v, want %q", gotBody["comment"], tc.comment)
			}
		})
	}

	t.Run("needs action rejected", func(t *testing.T) {
		_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected HTTP call for RsvpNeedsAction: %s", r.URL)
		})
		err := c.RSVP(context.Background(), "tok", "cal1", "evt1", domain.RsvpNeedsAction, "")
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})
}

func TestMapGraphEvent_SensitivityStatusResponse(t *testing.T) {
	_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, fmt.Sprintf(`{"value":[
			{"id":"e1","subject":"Confidential","sensitivity":"private","showAs":"tentative",
			 "start":{"dateTime":"2026-07-08T09:00:00.0000000","timeZone":"UTC"},
			 "end":{"dateTime":"2026-07-08T10:00:00.0000000","timeZone":"UTC"},
			 "organizer":{"emailAddress":{"address":"boss@x.com"}},
			 "attendees":[
			   {"emailAddress":{"address":"boss@x.com"},"type":"required","status":{"response":"organizer"}},
			   {"emailAddress":{"address":"me@x.com"},"type":"required","status":{"response":"declined"}}
			 ]}
		],"@odata.deltaLink":%q}`, "https://graph.microsoft.com/v1.0/done"))
	})

	page, err := c.SyncEvents(context.Background(), "tok", "cal1", "")
	if err != nil {
		t.Fatalf("SyncEvents: %v", err)
	}
	if len(page.Events) != 1 {
		t.Fatalf("Events len = %d, want 1", len(page.Events))
	}
	ev := page.Events[0]
	if ev.Visibility != domain.VisibilityPrivate {
		t.Errorf("Visibility = %v, want private", ev.Visibility)
	}
	if ev.Status != domain.EventTentative {
		t.Errorf("Status = %v, want tentative", ev.Status)
	}
	if len(ev.Attendees) != 2 {
		t.Fatalf("Attendees len = %d, want 2", len(ev.Attendees))
	}
	var organizer, declined *domain.Attendee
	for i := range ev.Attendees {
		a := &ev.Attendees[i]
		if a.Organizer {
			organizer = a
		}
		if a.Response == domain.RsvpDeclined {
			declined = a
		}
	}
	if organizer == nil || organizer.Email != "boss@x.com" {
		t.Errorf("organizer attendee = %+v, want boss@x.com", organizer)
	}
	if declined == nil || declined.Email != "me@x.com" {
		t.Errorf("declined attendee = %+v, want me@x.com", declined)
	}
}
