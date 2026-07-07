package googleapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

const calendarBase = "https://www.googleapis.com/calendar/v3"

// SyncCalendars lists the user's calendarList and maps it to domain
// calendars (ProviderCalendarID set; local prefs are preserved by the repo).
func (c *Client) SyncCalendars(ctx context.Context, accessToken string) ([]domain.Calendar, error) {
	var out []domain.Calendar
	pageTok := ""
	for {
		q := url.Values{"maxResults": {"100"}}
		if pageTok != "" {
			q.Set("pageToken", pageTok)
		}
		var list struct {
			Items []struct {
				ID              string `json:"id"`
				Summary         string `json:"summary"`
				BackgroundColor string `json:"backgroundColor"`
				TimeZone        string `json:"timeZone"`
				Primary         bool   `json:"primary"`
				AccessRole      string `json:"accessRole"`
			} `json:"items"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := c.doJSON(ctx, http.MethodGet, calendarBase+"/users/me/calendarList?"+q.Encode(), accessToken, nil, &list); err != nil {
			return nil, err
		}
		for _, item := range list.Items {
			out = append(out, domain.Calendar{
				ProviderCalendarID: item.ID,
				Name:               item.Summary,
				Color:              item.BackgroundColor,
				TimeZone:           item.TimeZone,
				IsPrimary:          item.Primary,
				IsVisible:          true,
				CanWrite:           item.AccessRole == "owner" || item.AccessRole == "writer",
			})
		}
		if list.NextPageToken == "" {
			return out, nil
		}
		pageTok = list.NextPageToken
	}
}

// SyncEvents cursor scheme:
//
//	""              initial full window — first page
//	"page:<tok>"    full listing in progress
//	"sync:<tok>"    steady state — Google syncToken incremental
//
// A 410 GONE (expired syncToken) transparently restarts the full listing.
func (c *Client) SyncEvents(ctx context.Context, accessToken, providerCalendarID, cursor string) (port.CalendarSyncPage, error) {
	q := url.Values{"maxResults": {"100"}, "singleEvents": {"false"}}
	switch {
	case cursor == "":
	case strings.HasPrefix(cursor, "page:"):
		q.Set("pageToken", strings.TrimPrefix(cursor, "page:"))
	case strings.HasPrefix(cursor, "sync:"):
		q.Set("syncToken", strings.TrimPrefix(cursor, "sync:"))
	default:
		return port.CalendarSyncPage{}, fmt.Errorf("%w: unrecognized calendar sync cursor %q", domain.ErrValidation, cursor)
	}

	endpoint := calendarBase + "/calendars/" + url.PathEscape(providerCalendarID) + "/events?" + q.Encode()
	var list struct {
		Items         []gcalEvent `json:"items"`
		NextPageToken string      `json:"nextPageToken"`
		NextSyncToken string      `json:"nextSyncToken"`
	}
	if err := c.doJSON(ctx, http.MethodGet, endpoint, accessToken, nil, &list); err != nil {
		var he *httpError
		if errors.As(err, &he) && he.StatusCode == http.StatusGone {
			return c.SyncEvents(ctx, accessToken, providerCalendarID, "") // token expired — full resync
		}
		return port.CalendarSyncPage{}, err
	}

	var page port.CalendarSyncPage
	for _, item := range list.Items {
		if item.Status == "cancelled" {
			page.DeletedIDs = append(page.DeletedIDs, item.ID)
			continue
		}
		page.Events = append(page.Events, mapGcalEvent(item))
	}
	switch {
	case list.NextPageToken != "":
		page.NextCursor = "page:" + list.NextPageToken
		page.HasMore = true
	case list.NextSyncToken != "":
		page.NextCursor = "sync:" + list.NextSyncToken
	default:
		page.NextCursor = cursor
	}
	return page, nil
}

// CreateEvent inserts an event; AddConferencing requests a Google Meet link
// via conferenceData.createRequest (conferenceDataVersion=1).
func (c *Client) CreateEvent(ctx context.Context, accessToken, providerCalendarID string, in domain.EventInput) (domain.Event, error) {
	body := map[string]any{
		"summary": in.Title,
		"start":   gcalTime(in.Start, in.AllDay),
		"end":     gcalTime(in.End, in.AllDay),
	}
	if in.Description != "" {
		body["description"] = in.Description
	}
	if in.Location != "" {
		body["location"] = in.Location
	}
	if in.RecurrenceRule != "" {
		body["recurrence"] = []string{rrulePrefixed(in.RecurrenceRule)}
	}
	if len(in.AttendeeEmails) > 0 {
		attendees := make([]map[string]any, 0, len(in.AttendeeEmails))
		for _, email := range in.AttendeeEmails {
			attendees = append(attendees, map[string]any{"email": email})
		}
		body["attendees"] = attendees
	}
	if len(in.ReminderMinutes) > 0 {
		body["reminders"] = remindersBody(in.ReminderMinutes)
	}
	if in.AddConferencing {
		body["conferenceData"] = map[string]any{
			"createRequest": map[string]any{
				"requestId":             randomID(),
				"conferenceSolutionKey": map[string]string{"type": "hangoutsMeet"},
			},
		}
	}

	endpoint := calendarBase + "/calendars/" + url.PathEscape(providerCalendarID) + "/events?conferenceDataVersion=1"
	var out gcalEvent
	if err := c.doJSON(ctx, http.MethodPost, endpoint, accessToken, body, &out); err != nil {
		return domain.Event{}, err
	}
	return mapGcalEvent(out), nil
}

// UpdateEvent PATCHes only the fields set on the patch.
func (c *Client) UpdateEvent(ctx context.Context, accessToken, providerCalendarID, providerEventID string, patch domain.EventPatch) (domain.Event, error) {
	body := map[string]any{}
	if patch.Title != nil {
		body["summary"] = *patch.Title
	}
	if patch.Description != nil {
		body["description"] = *patch.Description
	}
	if patch.Location != nil {
		body["location"] = *patch.Location
	}
	allDay := patch.AllDay != nil && *patch.AllDay
	if patch.Start != nil {
		body["start"] = gcalTime(*patch.Start, allDay)
	}
	if patch.End != nil {
		body["end"] = gcalTime(*patch.End, allDay)
	}
	if patch.RecurrenceRule != nil {
		if *patch.RecurrenceRule == "" {
			body["recurrence"] = []string{}
		} else {
			body["recurrence"] = []string{rrulePrefixed(*patch.RecurrenceRule)}
		}
	}
	if patch.AttendeeEmails != nil {
		attendees := make([]map[string]any, 0, len(*patch.AttendeeEmails))
		for _, email := range *patch.AttendeeEmails {
			attendees = append(attendees, map[string]any{"email": email})
		}
		body["attendees"] = attendees
	}
	if patch.ReminderMinutes != nil {
		body["reminders"] = remindersBody(*patch.ReminderMinutes)
	}

	endpoint := calendarBase + "/calendars/" + url.PathEscape(providerCalendarID) + "/events/" + url.PathEscape(providerEventID) + "?conferenceDataVersion=1"
	var out gcalEvent
	if err := c.doJSON(ctx, http.MethodPatch, endpoint, accessToken, body, &out); err != nil {
		return domain.Event{}, err
	}
	return mapGcalEvent(out), nil
}

// DeleteEvent removes the event; already-gone events are treated as success
// so deletes stay idempotent.
func (c *Client) DeleteEvent(ctx context.Context, accessToken, providerCalendarID, providerEventID string) error {
	endpoint := calendarBase + "/calendars/" + url.PathEscape(providerCalendarID) + "/events/" + url.PathEscape(providerEventID)
	err := c.doJSON(ctx, http.MethodDelete, endpoint, accessToken, nil, nil)
	var he *httpError
	if errors.As(err, &he) && (he.StatusCode == http.StatusNotFound || he.StatusCode == http.StatusGone) {
		return nil
	}
	return err
}

// RSVP patches the caller's attendee responseStatus (Google models RSVP as
// an attendee-list update, so the current list is fetched first).
func (c *Client) RSVP(ctx context.Context, accessToken, providerCalendarID, providerEventID string, response domain.RsvpStatus) error {
	endpoint := calendarBase + "/calendars/" + url.PathEscape(providerCalendarID) + "/events/" + url.PathEscape(providerEventID)
	var ev gcalEvent
	if err := c.doJSON(ctx, http.MethodGet, endpoint, accessToken, nil, &ev); err != nil {
		return err
	}
	found := false
	for i := range ev.Attendees {
		if ev.Attendees[i].Self {
			ev.Attendees[i].ResponseStatus = gcalResponseStatus(response)
			found = true
		}
	}
	if !found {
		return fmt.Errorf("%w: caller is not an attendee of event %s", domain.ErrValidation, providerEventID)
	}
	return c.doJSON(ctx, http.MethodPatch, endpoint, accessToken, map[string]any{"attendees": ev.Attendees}, nil)
}

// --- wire types + mapping ---

type gcalDateTime struct {
	Date     string `json:"date,omitempty"`     // all-day, "2006-01-02"
	DateTime string `json:"dateTime,omitempty"` // RFC 3339
	TimeZone string `json:"timeZone,omitempty"`
}

type gcalAttendee struct {
	Email          string `json:"email"`
	DisplayName    string `json:"displayName,omitempty"`
	ResponseStatus string `json:"responseStatus,omitempty"`
	Organizer      bool   `json:"organizer,omitempty"`
	Optional       bool   `json:"optional,omitempty"`
	Self           bool   `json:"self,omitempty"`
}

type gcalEvent struct {
	ID          string         `json:"id"`
	Status      string         `json:"status"`
	Summary     string         `json:"summary"`
	Description string         `json:"description"`
	Location    string         `json:"location"`
	Start       gcalDateTime   `json:"start"`
	End         gcalDateTime   `json:"end"`
	Recurrence  []string       `json:"recurrence"`
	Attendees   []gcalAttendee `json:"attendees"`
	HangoutLink string         `json:"hangoutLink"`
	Visibility  string         `json:"visibility"`
	Reminders   *struct {
		UseDefault bool `json:"useDefault"`
		Overrides  []struct {
			Minutes int `json:"minutes"`
		} `json:"overrides"`
	} `json:"reminders"`
	ConferenceData *struct {
		EntryPoints []struct {
			EntryPointType string `json:"entryPointType"`
			URI            string `json:"uri"`
		} `json:"entryPoints"`
	} `json:"conferenceData"`
}

func mapGcalEvent(e gcalEvent) domain.Event {
	start, allDay := parseGcalTime(e.Start)
	end, _ := parseGcalTime(e.End)
	out := domain.Event{
		ProviderEventID: e.ID,
		Title:           e.Summary,
		Start:           start,
		End:             end,
		AllDay:          allDay,
		Attendees:       []domain.Attendee{},
		Status:          mapGcalStatus(e.Status),
		Visibility:      mapGcalVisibility(e.Visibility),
		ReminderMinutes: []int{},
	}
	if e.Description != "" {
		d := e.Description
		out.Description = &d
	}
	if e.Location != "" {
		l := e.Location
		out.Location = &l
	}
	for _, r := range e.Recurrence {
		if strings.HasPrefix(r, "RRULE:") {
			rule := strings.TrimPrefix(r, "RRULE:")
			out.RecurrenceRule = &rule
			break
		}
	}
	for _, a := range e.Attendees {
		att := domain.Attendee{
			Email:     a.Email,
			Response:  mapGcalResponse(a.ResponseStatus),
			Organizer: a.Organizer,
			Optional:  a.Optional,
		}
		if a.DisplayName != "" {
			n := a.DisplayName
			att.Name = &n
		}
		out.Attendees = append(out.Attendees, att)
	}
	if url := conferenceURL(e); url != "" {
		out.Conferencing = &domain.Conferencing{Provider: domain.ConferencingMeet, URL: url}
	}
	if e.Reminders != nil {
		for _, o := range e.Reminders.Overrides {
			out.ReminderMinutes = append(out.ReminderMinutes, o.Minutes)
		}
	}
	return out
}

func conferenceURL(e gcalEvent) string {
	if e.HangoutLink != "" {
		return e.HangoutLink
	}
	if e.ConferenceData != nil {
		for _, ep := range e.ConferenceData.EntryPoints {
			if ep.EntryPointType == "video" {
				return ep.URI
			}
		}
	}
	return ""
}

func parseGcalTime(t gcalDateTime) (parsed time.Time, allDay bool) {
	if t.Date != "" {
		p, _ := time.Parse("2006-01-02", t.Date)
		return p, true
	}
	p, _ := time.Parse(time.RFC3339, t.DateTime)
	return p, false
}

func gcalTime(t time.Time, allDay bool) gcalDateTime {
	if allDay {
		return gcalDateTime{Date: t.Format("2006-01-02")}
	}
	return gcalDateTime{DateTime: t.Format(time.RFC3339), TimeZone: "UTC"}
}

func mapGcalStatus(s string) domain.EventStatus {
	switch s {
	case "tentative":
		return domain.EventTentative
	case "cancelled":
		return domain.EventCancelled
	default:
		return domain.EventConfirmed
	}
}

func mapGcalVisibility(s string) domain.EventVisibility {
	switch s {
	case "public":
		return domain.VisibilityPublic
	case "private", "confidential":
		return domain.VisibilityPrivate
	default:
		return domain.VisibilityDefault
	}
}

func mapGcalResponse(s string) domain.RsvpStatus {
	switch s {
	case "accepted":
		return domain.RsvpAccepted
	case "declined":
		return domain.RsvpDeclined
	case "tentative":
		return domain.RsvpTentative
	default:
		return domain.RsvpNeedsAction
	}
}

func gcalResponseStatus(r domain.RsvpStatus) string {
	switch r {
	case domain.RsvpAccepted:
		return "accepted"
	case domain.RsvpDeclined:
		return "declined"
	case domain.RsvpTentative:
		return "tentative"
	default:
		return "needsAction"
	}
}

func remindersBody(minutes []int) map[string]any {
	overrides := make([]map[string]any, 0, len(minutes))
	for _, m := range minutes {
		overrides = append(overrides, map[string]any{"method": "popup", "minutes": m})
	}
	return map[string]any{"useDefault": false, "overrides": overrides}
}

func rrulePrefixed(rule string) string {
	if strings.HasPrefix(rule, "RRULE:") {
		return rule
	}
	return "RRULE:" + rule
}

func randomID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
