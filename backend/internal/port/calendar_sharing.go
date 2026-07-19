package port

import (
	"context"

	"calendium/backend/internal/domain"
)

// ---------------------------------------------------------------------------
// Shared calendars with granular permissions (M2.7 Task 12)
//
// A local sharing layer over the mirrored calendars. Reads come from the
// mirror; editor writes go through the OWNER's provider tokens and are
// audit-logged. The permission matrix (free_busy | reader | editor) is
// enforced in the service layer on every read+write path it governs:
// free_busy viewers never receive titles/descriptions/attendees.
// ---------------------------------------------------------------------------

// CalendarShareInput is the POST /v1/calendars/{id}/shares payload. Exactly
// one of GranteeUserID / GranteeTeamID must be set; Permission defaults to
// free_busy (the privacy-preserving minimum) when empty.
type CalendarShareInput struct {
	GranteeUserID string `json:"granteeUserId,omitempty"`
	GranteeTeamID string `json:"granteeTeamId,omitempty"`
	Permission    string `json:"permission,omitempty"`
}

// CalendarSharingService is the driving port for managing calendar shares.
// Only the calendar's owning user (via the calendar → account → user chain)
// may manage shares; everyone else gets ErrNotFound (no existence oracle).
// Shared-calendar READ/WRITE behavior itself surfaces through the existing
// CalendarService methods (ListCalendars / ListEvents / Create-Update-
// DeleteEvent), permission-gated in the service implementation.
type CalendarSharingService interface {
	ShareCalendar(ctx context.Context, userID, calendarID string, in CalendarShareInput) (domain.CalendarShare, error)
	ListCalendarShares(ctx context.Context, userID, calendarID string) ([]domain.CalendarShare, error)
	UpdateCalendarShare(ctx context.Context, userID, calendarID, shareID, permission string) (domain.CalendarShare, error)
	RevokeCalendarShare(ctx context.Context, userID, calendarID, shareID string) error
}

// CalendarShareRepo persists calendar sharing grants. Create returns
// domain.ErrConflict when the (calendar, grantee) pair is already shared.
type CalendarShareRepo interface {
	Create(ctx context.Context, s domain.CalendarShare) (domain.CalendarShare, error)
	ListByCalendar(ctx context.Context, calendarID string) ([]domain.CalendarShare, error)
	// ListForGrantee returns every share granted to the user directly or to
	// any of the given teams (the caller resolves the viewer's memberships).
	ListForGrantee(ctx context.Context, userID string, teamIDs []string) ([]domain.CalendarShare, error)
	Update(ctx context.Context, s domain.CalendarShare) error
	Delete(ctx context.Context, id string) error
}

// AuditRepo persists the audit log of cross-principal mutations (grantee
// writes on shared calendars; reused by the workspace audit surface).
type AuditRepo interface {
	Record(ctx context.Context, e domain.AuditEntry) error
	// ListByPrincipal returns the principal's newest entries first.
	ListByPrincipal(ctx context.Context, principalID string, limit int) ([]domain.AuditEntry, error)
}
