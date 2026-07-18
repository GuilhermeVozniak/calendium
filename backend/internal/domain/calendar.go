package domain

import (
	"fmt"
	"time"
)

// Calendar is a provider calendar mirrored locally. IsVisible and Color are
// local user preferences and survive provider syncs.
type Calendar struct {
	ID                 string `json:"id"`
	AccountID          string `json:"accountId"`
	ProviderCalendarID string `json:"-"`
	Name               string `json:"name"`
	Color              string `json:"color"`
	TimeZone           string `json:"timeZone"`
	IsPrimary          bool   `json:"isPrimary"`
	IsVisible          bool   `json:"isVisible"`
	CanWrite           bool   `json:"canWrite"`
}

// RsvpStatus is an attendee's response to an invitation.
type RsvpStatus string

const (
	RsvpAccepted    RsvpStatus = "accepted"
	RsvpDeclined    RsvpStatus = "declined"
	RsvpTentative   RsvpStatus = "tentative"
	RsvpNeedsAction RsvpStatus = "needs_action"
)

// ParseRsvpStatus validates an rsvp body parameter.
func ParseRsvpStatus(s string) (RsvpStatus, error) {
	switch RsvpStatus(s) {
	case RsvpAccepted, RsvpDeclined, RsvpTentative, RsvpNeedsAction:
		return RsvpStatus(s), nil
	}
	return "", fmt.Errorf("%w: unknown rsvp response %q", ErrValidation, s)
}

// Attendee is a participant on an event.
type Attendee struct {
	Email     string     `json:"email"`
	Name      *string    `json:"name"`
	Response  RsvpStatus `json:"response"`
	Organizer bool       `json:"organizer"`
	Optional  bool       `json:"optional"`
}

// ConferencingProvider identifies the video-call vendor on an event.
type ConferencingProvider string

const (
	ConferencingMeet  ConferencingProvider = "meet"
	ConferencingZoom  ConferencingProvider = "zoom"
	ConferencingTeams ConferencingProvider = "teams"
	ConferencingOther ConferencingProvider = "other"
)

// Conferencing is a video-call link attached to an event.
type Conferencing struct {
	Provider ConferencingProvider `json:"provider"`
	URL      string               `json:"url"`
}

// EventStatus mirrors the provider event status.
type EventStatus string

const (
	EventConfirmed EventStatus = "confirmed"
	EventTentative EventStatus = "tentative"
	EventCancelled EventStatus = "cancelled"
)

// EventVisibility mirrors the provider visibility setting.
type EventVisibility string

const (
	VisibilityDefault EventVisibility = "default"
	VisibilityPublic  EventVisibility = "public"
	VisibilityPrivate EventVisibility = "private"
)

// Event is a calendar event mirrored locally; writes go through to the
// provider and update the mirror optimistically.
type Event struct {
	ID              string    `json:"id"`
	CalendarID      string    `json:"calendarId"`
	ProviderEventID string    `json:"-"`
	Title           string    `json:"title"`
	Description     *string   `json:"description"`
	Location        *string   `json:"location"`
	Start           time.Time `json:"start"`
	End             time.Time `json:"end"`
	AllDay          bool      `json:"allDay"`
	// RecurrenceRule is an RFC 5545 RRULE, when recurring.
	RecurrenceRule  *string         `json:"recurrenceRule"`
	Attendees       []Attendee      `json:"attendees"`
	Conferencing    *Conferencing   `json:"conferencing"`
	Status          EventStatus     `json:"status"`
	Visibility      EventVisibility `json:"visibility"`
	ReminderMinutes []int           `json:"reminderMinutes"`
}

// EventInput is the create-event payload (mirrors EventInput in types.ts).
type EventInput struct {
	CalendarID      string    `json:"calendarId"`
	Title           string    `json:"title"`
	Description     string    `json:"description,omitempty"`
	Location        string    `json:"location,omitempty"`
	Start           time.Time `json:"start"`
	End             time.Time `json:"end"`
	AllDay          bool      `json:"allDay,omitempty"`
	RecurrenceRule  string    `json:"recurrenceRule,omitempty"`
	AttendeeEmails  []string  `json:"attendeeEmails,omitempty"`
	AddConferencing bool      `json:"addConferencing,omitempty"`
	ReminderMinutes []int     `json:"reminderMinutes,omitempty"`
}

// EventPatch is a partial event update; nil fields are left unchanged.
type EventPatch struct {
	Title           *string    `json:"title"`
	Description     *string    `json:"description"`
	Location        *string    `json:"location"`
	Start           *time.Time `json:"start"`
	End             *time.Time `json:"end"`
	AllDay          *bool      `json:"allDay"`
	RecurrenceRule  *string    `json:"recurrenceRule"`
	AttendeeEmails  *[]string  `json:"attendeeEmails"`
	ReminderMinutes *[]int     `json:"reminderMinutes"`
}

// AvailabilitySlot is a free window for the share-availability flow.
type AvailabilitySlot struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// EventTemplate is a saved event default set ("1:1", "Focus block") applied
// at creation time. CalendarID may be empty (= user's default calendar) and
// is nulled when the referenced calendar is deleted.
type EventTemplate struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Title           string   `json:"title"`
	Description     string   `json:"description"`
	Location        string   `json:"location"`
	DurationMinutes int      `json:"durationMinutes"`
	AllDay          bool     `json:"allDay"`
	CalendarID      *string  `json:"calendarId"`
	AttendeeEmails  []string `json:"attendeeEmails"`
	AddConferencing bool     `json:"addConferencing"`
	ReminderMinutes []int    `json:"reminderMinutes"`
	RecurrenceRule  *string  `json:"recurrenceRule"`
	UsageCount      int      `json:"usageCount"`
}

// EventTemplateInput is the create/update payload (full replace on update).
type EventTemplateInput struct {
	Name            string   `json:"name"`
	Title           string   `json:"title"`
	Description     string   `json:"description,omitempty"`
	Location        string   `json:"location,omitempty"`
	DurationMinutes int      `json:"durationMinutes"`
	AllDay          bool     `json:"allDay,omitempty"`
	CalendarID      *string  `json:"calendarId,omitempty"`
	AttendeeEmails  []string `json:"attendeeEmails,omitempty"`
	AddConferencing bool     `json:"addConferencing,omitempty"`
	ReminderMinutes []int    `json:"reminderMinutes,omitempty"`
	RecurrenceRule  *string  `json:"recurrenceRule,omitempty"`
}

// CalendarSet is a named group of calendars toggled together ("Work", "Home").
type CalendarSet struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	CalendarIDs []string `json:"calendarIds"`
	Position    int      `json:"position"`
}

// CalendarSetInput is the create/update payload (full replace on update).
type CalendarSetInput struct {
	Name        string   `json:"name"`
	CalendarIDs []string `json:"calendarIds"`
	Position    int      `json:"position"`
}
