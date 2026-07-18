package msgraph

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// calendarView delta window: everything from one year back to two years out.
const (
	eventWindowPast   = 365 * 24 * time.Hour
	eventWindowFuture = 2 * 365 * 24 * time.Hour
)

// SyncCalendars lists the user's calendars.
func (c *Client) SyncCalendars(ctx context.Context, accessToken string) ([]domain.Calendar, error) {
	var out []domain.Calendar
	endpoint := graphBase + "/me/calendars?$top=100"
	for endpoint != "" {
		var res struct {
			Value []struct {
				ID                string `json:"id"`
				Name              string `json:"name"`
				HexColor          string `json:"hexColor"`
				CanEdit           bool   `json:"canEdit"`
				IsDefaultCalendar bool   `json:"isDefaultCalendar"`
			} `json:"value"`
			NextLink string `json:"@odata.nextLink"`
		}
		if err := c.doJSON(ctx, http.MethodGet, endpoint, accessToken, nil, &res); err != nil {
			return nil, err
		}
		for _, cal := range res.Value {
			out = append(out, domain.Calendar{
				ProviderCalendarID: cal.ID,
				Name:               cal.Name,
				Color:              cal.HexColor,
				IsPrimary:          cal.IsDefaultCalendar,
				IsVisible:          true,
				CanWrite:           cal.CanEdit,
			})
		}
		endpoint = res.NextLink
	}
	return out, nil
}

// SyncEvents runs a calendarView delta query. The cursor is the opaque
// @odata.nextLink / @odata.deltaLink URL; "" starts a fresh delta over the
// sync window. A 410 GONE (expired delta token) restarts from scratch.
func (c *Client) SyncEvents(ctx context.Context, accessToken, providerCalendarID, cursor string) (port.CalendarSyncPage, error) {
	endpoint := cursor
	if endpoint == "" {
		now := time.Now().UTC()
		q := url.Values{
			"startDateTime": {now.Add(-eventWindowPast).Format(time.RFC3339)},
			"endDateTime":   {now.Add(eventWindowFuture).Format(time.RFC3339)},
		}
		endpoint = graphBase + "/me/calendars/" + url.PathEscape(providerCalendarID) + "/calendarView/delta?" + q.Encode()
	}

	var res struct {
		Value     []graphEvent `json:"value"`
		NextLink  string       `json:"@odata.nextLink"`
		DeltaLink string       `json:"@odata.deltaLink"`
	}
	if err := c.doJSON(ctx, http.MethodGet, endpoint, accessToken, nil, &res); err != nil {
		var he *httpError
		if errors.As(err, &he) && he.StatusCode == http.StatusGone && cursor != "" {
			return c.SyncEvents(ctx, accessToken, providerCalendarID, "")
		}
		return port.CalendarSyncPage{}, err
	}

	var page port.CalendarSyncPage
	for _, item := range res.Value {
		if item.Removed != nil || item.IsCancelled {
			page.DeletedIDs = append(page.DeletedIDs, item.ID)
			continue
		}
		page.Events = append(page.Events, mapGraphEvent(item))
	}
	if res.NextLink != "" {
		page.NextCursor = res.NextLink
		page.HasMore = true
	} else {
		page.NextCursor = res.DeltaLink
	}
	return page, nil
}

// CreateEvent inserts an event; AddConferencing requests a Teams meeting.
func (c *Client) CreateEvent(ctx context.Context, accessToken, providerCalendarID string, in domain.EventInput) (domain.Event, error) {
	body := map[string]any{
		"subject":  in.Title,
		"start":    graphTime(in.Start, in.AllDay),
		"end":      graphTime(in.End, in.AllDay),
		"isAllDay": in.AllDay,
	}
	if in.Description != "" {
		body["body"] = map[string]string{"contentType": "HTML", "content": in.Description}
	}
	if in.Location != "" {
		body["location"] = map[string]string{"displayName": in.Location}
	}
	if len(in.AttendeeEmails) > 0 {
		body["attendees"] = graphAttendees(in.AttendeeEmails)
	}
	if in.RecurrenceRule != "" {
		rec, err := rruleToGraphRecurrence(in.RecurrenceRule, in.Start)
		if err != nil {
			return domain.Event{}, err
		}
		body["recurrence"] = rec
	}
	if len(in.ReminderMinutes) > 0 {
		body["isReminderOn"] = true
		body["reminderMinutesBeforeStart"] = in.ReminderMinutes[0]
	}
	if in.AddConferencing {
		body["isOnlineMeeting"] = true
		body["onlineMeetingProvider"] = "teamsForBusiness"
	}

	endpoint := graphBase + "/me/calendars/" + url.PathEscape(providerCalendarID) + "/events"
	var out graphEvent
	if err := c.doJSON(ctx, http.MethodPost, endpoint, accessToken, body, &out); err != nil {
		return domain.Event{}, err
	}
	return mapGraphEvent(out), nil
}

// UpdateEvent PATCHes only the fields set on the patch.
func (c *Client) UpdateEvent(ctx context.Context, accessToken, providerCalendarID, providerEventID string, patch domain.EventPatch) (domain.Event, error) {
	body := map[string]any{}
	if patch.Title != nil {
		body["subject"] = *patch.Title
	}
	if patch.Description != nil {
		body["body"] = map[string]string{"contentType": "HTML", "content": *patch.Description}
	}
	if patch.Location != nil {
		body["location"] = map[string]string{"displayName": *patch.Location}
	}
	allDay := patch.AllDay != nil && *patch.AllDay
	if patch.AllDay != nil {
		body["isAllDay"] = *patch.AllDay
	}
	if patch.Start != nil {
		body["start"] = graphTime(*patch.Start, allDay)
	}
	if patch.End != nil {
		body["end"] = graphTime(*patch.End, allDay)
	}
	if patch.AttendeeEmails != nil {
		body["attendees"] = graphAttendees(*patch.AttendeeEmails)
	}
	if patch.RecurrenceRule != nil {
		if *patch.RecurrenceRule == "" {
			body["recurrence"] = nil
		} else {
			start := time.Now().UTC()
			if patch.Start != nil {
				start = *patch.Start
			}
			rec, err := rruleToGraphRecurrence(*patch.RecurrenceRule, start)
			if err != nil {
				return domain.Event{}, err
			}
			body["recurrence"] = rec
		}
	}
	if patch.ReminderMinutes != nil {
		if len(*patch.ReminderMinutes) > 0 {
			body["isReminderOn"] = true
			body["reminderMinutesBeforeStart"] = (*patch.ReminderMinutes)[0]
		} else {
			body["isReminderOn"] = false
		}
	}

	endpoint := graphBase + "/me/calendars/" + url.PathEscape(providerCalendarID) + "/events/" + url.PathEscape(providerEventID)
	var out graphEvent
	if err := c.doJSON(ctx, http.MethodPatch, endpoint, accessToken, body, &out); err != nil {
		return domain.Event{}, err
	}
	return mapGraphEvent(out), nil
}

// DeleteEvent removes the event; already-gone events are treated as success
// so deletes stay idempotent.
func (c *Client) DeleteEvent(ctx context.Context, accessToken, providerCalendarID, providerEventID string) error {
	endpoint := graphBase + "/me/calendars/" + url.PathEscape(providerCalendarID) + "/events/" + url.PathEscape(providerEventID)
	err := c.doJSON(ctx, http.MethodDelete, endpoint, accessToken, nil, nil)
	var he *httpError
	if errors.As(err, &he) && (he.StatusCode == http.StatusNotFound || he.StatusCode == http.StatusGone) {
		return nil
	}
	return err
}

// RSVP calls the Graph respond endpoints (accept / decline /
// tentativelyAccept). Graph has no "reset to needsAction" endpoint.
func (c *Client) RSVP(ctx context.Context, accessToken, providerCalendarID, providerEventID string, response domain.RsvpStatus) error {
	var action string
	switch response {
	case domain.RsvpAccepted:
		action = "accept"
	case domain.RsvpDeclined:
		action = "decline"
	case domain.RsvpTentative:
		action = "tentativelyAccept"
	default:
		return fmt.Errorf("%w: microsoft calendars cannot reset an rsvp to %q", domain.ErrValidation, response)
	}
	endpoint := graphBase + "/me/events/" + url.PathEscape(providerEventID) + "/" + action
	return c.doJSON(ctx, http.MethodPost, endpoint, accessToken, map[string]any{"sendResponse": true}, nil)
}

// freeBusyChunkSize caps how many mailboxes go in a single getSchedule
// request — Graph documents a hard cap around 20-50 schedules per call
// depending on tenant; chunking at 20 keeps every call well under that and
// isolates one bad mailbox's error from the rest of the batch.
const freeBusyChunkSize = 20

// FreeBusy queries busy intervals for a set of attendee emails via the
// getSchedule endpoint, chunking requests at freeBusyChunkSize emails.
// Schedules the API reports an error for (unresolvable mailbox) are omitted
// from the result. Result keys are lowercased emails (scheduleId echoes the
// requested schedule verbatim, so casing is normalized here).
func (c *Client) FreeBusy(ctx context.Context, accessToken string, emails []string, from, to time.Time) (map[string][]domain.BusyInterval, error) {
	out := make(map[string][]domain.BusyInterval, len(emails))
	for _, chunk := range chunkStrings(emails, freeBusyChunkSize) {
		if err := c.freeBusyChunk(ctx, accessToken, chunk, from, to, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (c *Client) freeBusyChunk(ctx context.Context, accessToken string, emails []string, from, to time.Time, out map[string][]domain.BusyInterval) error {
	body := map[string]any{
		"schedules":                emails,
		"startTime":                map[string]string{"dateTime": from.UTC().Format("2006-01-02T15:04:05"), "timeZone": "UTC"},
		"endTime":                  map[string]string{"dateTime": to.UTC().Format("2006-01-02T15:04:05"), "timeZone": "UTC"},
		"availabilityViewInterval": 30,
	}
	var res struct {
		Value []struct {
			ScheduleID string `json:"scheduleId"`
			Error      *struct {
				Message string `json:"message"`
			} `json:"error"`
			ScheduleItems []struct {
				Status string        `json:"status"`
				Start  graphDateTime `json:"start"`
				End    graphDateTime `json:"end"`
			} `json:"scheduleItems"`
		} `json:"value"`
	}
	if err := c.doJSON(ctx, http.MethodPost, graphBase+"/me/calendar/getSchedule", accessToken, body, &res); err != nil {
		return err
	}
	for _, sched := range res.Value {
		if sched.Error != nil {
			continue // unresolvable mailbox — leave the schedule out of the result
		}
		intervals := make([]domain.BusyInterval, 0, len(sched.ScheduleItems))
		for _, item := range sched.ScheduleItems {
			// freeBusyStatus is a lowercase enum: free, tentative, busy, oof,
			// workingElsewhere. Compare case-insensitively so a differently
			// cased response never gets misreported as busy (or vice versa).
			if strings.EqualFold(item.Status, "free") || strings.EqualFold(item.Status, "workingElsewhere") {
				continue
			}
			start, err := parseGraphFreeBusyTime(item.Start)
			if err != nil {
				return fmt.Errorf("msgraph: parse freeBusy scheduleItem start %q: %w", item.Start.DateTime, err)
			}
			end, err := parseGraphFreeBusyTime(item.End)
			if err != nil {
				return fmt.Errorf("msgraph: parse freeBusy scheduleItem end %q: %w", item.End.DateTime, err)
			}
			intervals = append(intervals, domain.BusyInterval{Start: start, End: end})
		}
		out[strings.ToLower(sched.ScheduleID)] = intervals
	}
	return nil
}

// chunkStrings splits items into consecutive slices of at most size. A nil
// or empty items yields no chunks (so a zero-email FreeBusy call makes no
// HTTP requests).
func chunkStrings(items []string, size int) [][]string {
	if len(items) == 0 {
		return nil
	}
	chunks := make([][]string, 0, (len(items)+size-1)/size)
	for i := 0; i < len(items); i += size {
		end := i + size
		if end > len(items) {
			end = len(items)
		}
		chunks = append(chunks, items[i:end])
	}
	return chunks
}

// --- wire types + mapping ---

type graphDateTime struct {
	DateTime string `json:"dateTime"` // "2006-01-02T15:04:05.0000000" in TimeZone
	TimeZone string `json:"timeZone"`
}

type graphEvent struct {
	ID          string `json:"id"`
	Subject     string `json:"subject"`
	IsAllDay    bool   `json:"isAllDay"`
	IsCancelled bool   `json:"isCancelled"`
	Body        *struct {
		ContentType string `json:"contentType"`
		Content     string `json:"content"`
	} `json:"body"`
	Location *struct {
		DisplayName string `json:"displayName"`
	} `json:"location"`
	Start                      graphDateTime    `json:"start"`
	End                        graphDateTime    `json:"end"`
	ShowAs                     string           `json:"showAs"`
	Sensitivity                string           `json:"sensitivity"`
	IsReminderOn               bool             `json:"isReminderOn"`
	ReminderMinutesBeforeStart int              `json:"reminderMinutesBeforeStart"`
	Recurrence                 *graphRecurrence `json:"recurrence"`
	Attendees                  []struct {
		graphAddress
		Type   string `json:"type"`
		Status struct {
			Response string `json:"response"`
		} `json:"status"`
	} `json:"attendees"`
	Organizer     *graphAddress `json:"organizer"`
	OnlineMeeting *struct {
		JoinURL string `json:"joinUrl"`
	} `json:"onlineMeeting"`
	OnlineMeetingURL string `json:"onlineMeetingUrl"`
	Removed          *struct {
		Reason string `json:"reason"`
	} `json:"@removed"`
}

func mapGraphEvent(e graphEvent) domain.Event {
	out := domain.Event{
		ProviderEventID: e.ID,
		Title:           e.Subject,
		Start:           parseGraphTime(e.Start),
		End:             parseGraphTime(e.End),
		AllDay:          e.IsAllDay,
		Attendees:       []domain.Attendee{},
		Status:          mapGraphStatus(e),
		Visibility:      mapGraphSensitivity(e.Sensitivity),
		ReminderMinutes: []int{},
	}
	if e.Body != nil && e.Body.Content != "" {
		d := e.Body.Content
		out.Description = &d
	}
	if e.Location != nil && e.Location.DisplayName != "" {
		l := e.Location.DisplayName
		out.Location = &l
	}
	if rule := graphRecurrenceToRRule(e.Recurrence); rule != "" {
		out.RecurrenceRule = &rule
	}
	organizerEmail := ""
	if e.Organizer != nil {
		organizerEmail = e.Organizer.EmailAddress.Address
	}
	for _, a := range e.Attendees {
		att := domain.Attendee{
			Email:     a.EmailAddress.Address,
			Response:  mapGraphResponse(a.Status.Response),
			Organizer: strings.EqualFold(a.EmailAddress.Address, organizerEmail),
			Optional:  a.Type == "optional",
		}
		if a.EmailAddress.Name != "" {
			n := a.EmailAddress.Name
			att.Name = &n
		}
		out.Attendees = append(out.Attendees, att)
	}
	joinURL := e.OnlineMeetingURL
	if e.OnlineMeeting != nil && e.OnlineMeeting.JoinURL != "" {
		joinURL = e.OnlineMeeting.JoinURL
	}
	if joinURL != "" {
		out.Conferencing = &domain.Conferencing{Provider: domain.ConferencingTeams, URL: joinURL}
	}
	if e.IsReminderOn {
		out.ReminderMinutes = append(out.ReminderMinutes, e.ReminderMinutesBeforeStart)
	}
	return out
}

// parseGraphTime handles Graph's fractional-second local format; the Prefer
// header pins TimeZone to UTC on reads.
func parseGraphTime(t graphDateTime) time.Time {
	for _, layout := range []string{"2006-01-02T15:04:05.9999999", "2006-01-02T15:04:05", time.RFC3339} {
		if p, err := time.Parse(layout, t.DateTime); err == nil {
			return p.UTC()
		}
	}
	return time.Time{}
}

// parseGraphFreeBusyTime is parseGraphTime's error-surfacing counterpart:
// getSchedule bounds feed availability decisions directly, so a malformed
// timestamp must fail the call rather than silently degrade to the zero
// value (which would read as "busy from the Unix-time epoch").
func parseGraphFreeBusyTime(t graphDateTime) (time.Time, error) {
	for _, layout := range []string{"2006-01-02T15:04:05.9999999", "2006-01-02T15:04:05", time.RFC3339} {
		if p, err := time.Parse(layout, t.DateTime); err == nil {
			return p.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized time format %q", t.DateTime)
}

func graphTime(t time.Time, allDay bool) graphDateTime {
	if allDay {
		return graphDateTime{DateTime: t.UTC().Format("2006-01-02T00:00:00"), TimeZone: "UTC"}
	}
	return graphDateTime{DateTime: t.UTC().Format("2006-01-02T15:04:05"), TimeZone: "UTC"}
}

func graphAttendees(emails []string) []map[string]any {
	out := make([]map[string]any, 0, len(emails))
	for _, email := range emails {
		out = append(out, map[string]any{
			"emailAddress": map[string]string{"address": email},
			"type":         "required",
		})
	}
	return out
}

func mapGraphStatus(e graphEvent) domain.EventStatus {
	if e.IsCancelled {
		return domain.EventCancelled
	}
	if e.ShowAs == "tentative" {
		return domain.EventTentative
	}
	return domain.EventConfirmed
}

func mapGraphSensitivity(s string) domain.EventVisibility {
	switch s {
	case "private", "confidential":
		return domain.VisibilityPrivate
	case "normal":
		return domain.VisibilityDefault
	case "personal":
		return domain.VisibilityPublic
	default:
		return domain.VisibilityDefault
	}
}

func mapGraphResponse(s string) domain.RsvpStatus {
	switch s {
	case "accepted", "organizer":
		return domain.RsvpAccepted
	case "declined":
		return domain.RsvpDeclined
	case "tentativelyAccepted":
		return domain.RsvpTentative
	default:
		return domain.RsvpNeedsAction
	}
}
