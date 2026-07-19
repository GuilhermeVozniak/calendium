package domain

import (
	"fmt"
	"regexp"
	"time"
)

// AvailabilityWindow is one weekly recurring open window on a booking link
// or in a user's working hours, expressed in the owning entity's time zone.
type AvailabilityWindow struct {
	Weekday int    `json:"weekday"` // 0=Sunday … 6=Saturday
	Start   string `json:"start"`   // "09:00" (24h HH:MM)
	End     string `json:"end"`     // "17:00", must be > Start
}

// BookingLink is a personal Calendly-style scheduling page:
// /book/{slug} → live slots → visitor books → event on CalendarID.
type BookingLink struct {
	ID                  string               `json:"id"`
	UserID              string               `json:"-"`
	Slug                string               `json:"slug"`
	Title               string               `json:"title"`
	Description         *string              `json:"description"`
	CalendarID          string               `json:"calendarId"` // target (writable) calendar
	DurationMinutes     int                  `json:"durationMinutes"`
	TimeZone            string               `json:"timeZone"` // IANA name; windows interpreted here
	Windows             []AvailabilityWindow `json:"windows"`
	BufferBeforeMin     int                  `json:"bufferBeforeMin"`
	BufferAfterMin      int                  `json:"bufferAfterMin"`
	DailyLimit          int                  `json:"dailyLimit"` // 0 = unlimited confirmed bookings/day
	MinNoticeMin        int                  `json:"minNoticeMin"`
	MaxAdvanceDays      int                  `json:"maxAdvanceDays"` // 0 = default 60
	RespectWorkingHours bool                 `json:"respectWorkingHours"`
	AddConferencing     bool                 `json:"addConferencing"`
	Active              bool                 `json:"active"`
	CreatedAt           time.Time            `json:"createdAt"`
	// TeamID scopes this link to a team (M2.7 Task 14): slots become the
	// COLLECTIVE intersection of the creator's and every listed member's
	// availability, and confirmed bookings invite every member. nil =
	// personal link. Round-robin rotation is future work.
	TeamID *string `json:"teamId"`
	// MemberUserIDs are the team members whose free/busy is intersected and
	// who are invited on every confirmed booking. Normalized: the creator is
	// implicit and never stored here. Meaningful only when TeamID is set.
	MemberUserIDs []string `json:"memberUserIds"`
}

// BookingStatus is the slot-hold lifecycle: hold → confirmed | cancelled.
type BookingStatus string

const (
	BookingHold      BookingStatus = "hold"
	BookingConfirmed BookingStatus = "confirmed"
	BookingCancelled BookingStatus = "cancelled"
)

// Booking is one visitor reservation against a BookingLink. While Status is
// "hold" the row blocks the slot (DB exclusion constraint) but no provider
// event exists yet; HoldExpiresAt bounds how long an unconfirmed hold lives.
type Booking struct {
	ID            string        `json:"id"`
	LinkID        string        `json:"linkId"`
	Status        BookingStatus `json:"status"`
	Start         time.Time     `json:"start"`
	End           time.Time     `json:"end"`
	InviteeName   string        `json:"inviteeName"`
	InviteeEmail  string        `json:"inviteeEmail"`
	InviteeTZ     string        `json:"inviteeTimeZone"`
	Note          *string       `json:"note"`
	EventID       *string       `json:"eventId"` // mirrored event once confirmed
	HoldExpiresAt *time.Time    `json:"-"`
	CreatedAt     time.Time     `json:"createdAt"`
}

// PollStatus is the meeting-poll lifecycle.
type PollStatus string

const (
	PollOpen      PollStatus = "open"
	PollConfirmed PollStatus = "confirmed"
	PollCancelled PollStatus = "cancelled"
)

// MeetingPoll proposes candidate slots invitees vote on via a public link;
// the organizer confirms the winner, creating a real event.
type MeetingPoll struct {
	ID              string       `json:"id"`
	UserID          string       `json:"-"`
	Token           string       `json:"token"` // unguessable public URL token (32 hex chars)
	Title           string       `json:"title"`
	Description     *string      `json:"description"`
	CalendarID      string       `json:"calendarId"`
	DurationMinutes int          `json:"durationMinutes"`
	Options         []PollOption `json:"options"`
	Status          PollStatus   `json:"status"`
	WinnerOptionID  *string      `json:"winnerOptionId"`
	EventID         *string      `json:"eventId"`
	CreatedAt       time.Time    `json:"createdAt"`
}

// PollOption is one candidate slot on a poll.
type PollOption struct {
	ID    string    `json:"id"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// PollVoteChoice is one voter's answer for one option.
type PollVoteChoice string

const (
	VoteYes      PollVoteChoice = "yes"
	VoteNo       PollVoteChoice = "no"
	VoteIfNeeded PollVoteChoice = "if_needed"
)

// PollVote is one voter's full ballot (one row per voter per option).
type PollVote struct {
	PollID     string         `json:"-"`
	OptionID   string         `json:"optionId"`
	VoterEmail string         `json:"voterEmail"`
	VoterName  string         `json:"voterName"`
	Choice     PollVoteChoice `json:"choice"`
	CreatedAt  time.Time      `json:"createdAt"`
}

// ProposalStatus is the propose-new-time lifecycle.
type ProposalStatus string

const (
	ProposalPending    ProposalStatus = "pending"
	ProposalAccepted   ProposalStatus = "accepted"
	ProposalDeclined   ProposalStatus = "declined"
	ProposalSuperseded ProposalStatus = "superseded"
)

// TimeProposal is an invitee's counter-proposed time for an existing event.
type TimeProposal struct {
	ID            string         `json:"id"`
	EventID       string         `json:"eventId"`
	ProposerEmail string         `json:"proposerEmail"`
	ProposerName  string         `json:"proposerName"`
	Start         time.Time      `json:"start"`
	End           time.Time      `json:"end"`
	Note          *string        `json:"note"`
	Status        ProposalStatus `json:"status"`
	CreatedAt     time.Time      `json:"createdAt"`
}

// UserSettings carries per-user scheduling preferences (working hours in
// TimeZone, displayed location). Zero-value WorkingHours = no constraint.
type UserSettings struct {
	UserID          string               `json:"-"`
	TimeZone        string               `json:"timeZone"`
	WorkingHours    []AvailabilityWindow `json:"workingHours"`
	WorkingLocation string               `json:"workingLocation"` // "", "office", "home", or free text
}

// BusyInterval is one busy span from a provider free/busy query.
type BusyInterval struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

var slugRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

var reservedSlugs = map[string]struct{}{
	"api": {}, "www": {}, "book": {}, "admin": {}, "app": {},
	"settings": {}, "pricing": {}, "docs": {}, "signin": {}, "poll": {},
}

// ValidateSlug enforces the booking-link slug shape and reserved list.
func ValidateSlug(slug string) error {
	if !slugRe.MatchString(slug) {
		return fmt.Errorf("%w: slug must be 1-64 lowercase letters, digits, or hyphens (no leading/trailing hyphen)", ErrValidation)
	}
	if _, reserved := reservedSlugs[slug]; reserved {
		return fmt.Errorf("%w: slug %q is reserved", ErrValidation, slug)
	}
	return nil
}

// Validate checks an AvailabilityWindow's shape ("HH:MM", End > Start, weekday 0-6).
func (w AvailabilityWindow) Validate() error {
	if w.Weekday < 0 || w.Weekday > 6 {
		return fmt.Errorf("%w: weekday must be 0-6, got %d", ErrValidation, w.Weekday)
	}
	start, err := time.Parse("15:04", w.Start)
	if err != nil {
		return fmt.Errorf("%w: invalid start time %q, want HH:MM", ErrValidation, w.Start)
	}
	end, err := time.Parse("15:04", w.End)
	if err != nil {
		return fmt.Errorf("%w: invalid end time %q, want HH:MM", ErrValidation, w.End)
	}
	if !end.After(start) {
		return fmt.Errorf("%w: end %q must be after start %q", ErrValidation, w.End, w.Start)
	}
	return nil
}
