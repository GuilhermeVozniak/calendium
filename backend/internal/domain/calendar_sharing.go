package domain

import (
	"fmt"
	"time"
)

// CalendarPermission orders shared-calendar access levels:
// free_busy < reader < editor. free_busy viewers may only ever see
// start/end busy blocks — titles, descriptions, locations, and attendees
// are redacted server-side before an event crosses the API boundary.
type CalendarPermission string

const (
	PermissionFreeBusy CalendarPermission = "free_busy"
	PermissionReader   CalendarPermission = "reader"
	PermissionEditor   CalendarPermission = "editor"
)

// ParseCalendarPermission validates a permission body parameter.
func ParseCalendarPermission(s string) (CalendarPermission, error) {
	switch CalendarPermission(s) {
	case PermissionFreeBusy, PermissionReader, PermissionEditor:
		return CalendarPermission(s), nil
	}
	return "", fmt.Errorf("%w: unknown calendar permission %q", ErrValidation, s)
}

// rank returns the privilege ordering used by AtLeast; unknown values rank
// below every valid permission.
func (p CalendarPermission) rank() int {
	switch p {
	case PermissionEditor:
		return 3
	case PermissionReader:
		return 2
	case PermissionFreeBusy:
		return 1
	}
	return 0
}

// AtLeast reports whether p grants the privileges of min.
func (p CalendarPermission) AtLeast(min CalendarPermission) bool { return p.rank() >= min.rank() }

// MorePermissive reports whether p strictly outranks other (used to apply
// the most-permissive-grant-wins rule across overlapping user/team shares).
func (p CalendarPermission) MorePermissive(other CalendarPermission) bool {
	return p.rank() > other.rank()
}

// CalendarShare grants one user or one whole team access to a calendar.
// Exactly one grantee field is set. Sharing is a local layer over the
// mirrored calendars — provider-level ACLs are never touched.
type CalendarShare struct {
	ID            string             `json:"id"`
	CalendarID    string             `json:"calendarId"`
	GranteeUserID *string            `json:"granteeUserId,omitempty"`
	GranteeTeamID *string            `json:"granteeTeamId,omitempty"`
	Permission    CalendarPermission `json:"permission"`
	CreatedBy     string             `json:"createdBy"`
	CreatedAt     time.Time          `json:"createdAt"`
}
