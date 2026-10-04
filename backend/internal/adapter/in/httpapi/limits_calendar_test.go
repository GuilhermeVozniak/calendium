package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestFieldLimitsViaHandlersCalendarScheduling(t *testing.T) {
	long := func(n int) string { return strings.Repeat("x", n) }
	strs := func(n int) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = `"a@example.com"`
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	window := `{"weekday":1,"start":"09:00","end":"17:00"}`
	windows := func(n int) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = window
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	tests := []struct {
		name      string
		method    string
		target    string
		body      string
		wantField string
		wantLimit float64
	}{
		{"event title 501", http.MethodPost, "/v1/events", `{"calendarId":"c1","title":"` + long(501) + `","start":"2026-01-01T10:00:00Z","end":"2026-01-01T11:00:00Z"}`, "title", 500},
		{"event description 64KiB+1", http.MethodPost, "/v1/events", `{"calendarId":"c1","title":"t","description":"` + long((64<<10)+1) + `","start":"2026-01-01T10:00:00Z","end":"2026-01-01T11:00:00Z"}`, "description", 65536},
		{"event attendees 501", http.MethodPost, "/v1/events", `{"calendarId":"c1","title":"t","attendeeEmails":` + strs(501) + `,"start":"2026-01-01T10:00:00Z","end":"2026-01-01T11:00:00Z"}`, "attendeeEmails", 500},
		{"event patch location 501", http.MethodPatch, "/v1/events/e1", `{"location":"` + long(501) + `"}`, "location", 500},
		{"calendar color 501", http.MethodPatch, "/v1/calendars/c1", `{"color":"` + long(501) + `"}`, "color", 500},
		{"rsvp comment 64KiB+1", http.MethodPost, "/v1/events/e1/rsvp", `{"response":"accepted","comment":"` + long((64<<10)+1) + `"}`, "comment", 65536},
		{"event note links 501", http.MethodPut, "/v1/events/e1/note", `{"bodyMd":"x","links":` + strs(501) + `}`, "links", 500},
		{"event note link 2049", http.MethodPut, "/v1/events/e1/note", `{"bodyMd":"x","links":["https://` + long(2049) + `"]}`, "links", 2048},
		{"template name 501", http.MethodPost, "/v1/event-templates", `{"name":"` + long(501) + `","title":"t","durationMinutes":30}`, "name", 500},
		{"calendar set name 501", http.MethodPost, "/v1/calendar-sets", `{"name":"` + long(501) + `","calendarIds":[]}`, "name", 500},
		{"calendar set ids 501", http.MethodPut, "/v1/calendar-sets/s1", `{"name":"n","calendarIds":` + strs(501) + `}`, "calendarIds", 500},
		{"booking link title 501", http.MethodPost, "/v1/booking-links", `{"slug":"s","title":"` + long(501) + `","calendarId":"c1","durationMinutes":30,"timeZone":"UTC","windows":[]}`, "title", 500},
		{"booking link windows 501", http.MethodPut, "/v1/booking-links/l1", `{"slug":"s","title":"t","calendarId":"c1","durationMinutes":30,"timeZone":"UTC","windows":` + windows(501) + `}`, "windows", 500},
		{"poll description 64KiB+1", http.MethodPost, "/v1/polls", `{"title":"t","description":"` + long((64<<10)+1) + `","calendarId":"c1","durationMinutes":30,"options":[]}`, "description", 65536},
		{"poll confirm optionId 501", http.MethodPost, "/v1/polls/p1/confirm", `{"optionId":"` + long(501) + `"}`, "optionId", 500},
		{"proposal note 64KiB+1", http.MethodPost, "/v1/events/e1/propose-time", `{"start":"2026-01-01T10:00:00Z","end":"2026-01-01T11:00:00Z","note":"` + long((64<<10)+1) + `"}`, "note", 65536},
		{"freebusy email 321", http.MethodPost, "/v1/freebusy", `{"emails":["` + long(321) + `"],"from":"2026-01-01T00:00:00Z","to":"2026-01-02T00:00:00Z"}`, "emails", 320},
		{"settings workingLocation 501", http.MethodPut, "/v1/settings", `{"timeZone":"UTC","workingHours":[],"workingLocation":"` + long(501) + `"}`, "workingLocation", 500},
		{"team name 501", http.MethodPost, "/v1/teams", `{"name":"` + long(501) + `"}`, "name", 500},
		{"team rename 501", http.MethodPatch, "/v1/teams/team_1", `{"name":"` + long(501) + `"}`, "name", 500},
		{"invite email 321", http.MethodPost, "/v1/teams/team_1/invitations", `{"email":"` + long(321) + `","role":"member"}`, "email", 320},
		{"accept invitation token 501", http.MethodPost, "/v1/invitations/accept", `{"token":"` + long(501) + `"}`, "token", 500},
		{"calendar prefs decline message 64KiB+1", http.MethodPatch, "/v1/prefs/calendar", `{"focusDeclineMessage":"` + long((64<<10)+1) + `"}`, "focusDeclineMessage", 65536},
		{"calendar share permission 501", http.MethodPost, "/v1/calendars/c1/shares", `{"granteeUserId":"u2","permission":"` + long(501) + `"}`, "permission", 500},
		{"calendar share update permission 501", http.MethodPatch, "/v1/calendars/c1/shares/sh1", `{"permission":"` + long(501) + `"}`, "permission", 500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.deps.CalendarShares = &fakeCalendarSharingService{}
			rec := h.authed(tt.method, tt.target, strings.NewReader(tt.body))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
			}
			e := decodeErr(t, rec)
			d := limitDetails(e)
			if e.Code != "validation_failed" || d["field"] != tt.wantField || d["limit"] != tt.wantLimit {
				t.Fatalf("envelope = %+v, want validation_failed field=%s limit=%v", e, tt.wantField, tt.wantLimit)
			}
		})
	}
}

func TestPublicFieldLimits(t *testing.T) {
	long := func(n int) string { return strings.Repeat("x", n) }
	tests := []struct {
		name      string
		target    string
		body      string
		wantField string
	}{
		{"booking inviteeEmail 321", "/v1/public/booking/demo/bookings", `{"start":"2026-01-01T10:00:00Z","inviteeName":"n","inviteeEmail":"` + long(321) + `","inviteeTimeZone":"UTC"}`, "inviteeEmail"},
		{"booking inviteeName 501", "/v1/public/booking/demo/bookings", `{"start":"2026-01-01T10:00:00Z","inviteeName":"` + long(501) + `","inviteeEmail":"a@example.com","inviteeTimeZone":"UTC"}`, "inviteeName"},
		{"ballot voterName 501", "/v1/public/polls/tok/votes", `{"voterEmail":"a@example.com","voterName":"` + long(501) + `","choices":{}}`, "voterName"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			rec := h.anon(http.MethodPost, tt.target, strings.NewReader(tt.body))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
			}
			if d := limitDetails(decodeErr(t, rec)); d["field"] != tt.wantField {
				t.Fatalf("details = %v, want field %s", d, tt.wantField)
			}
		})
	}
}
