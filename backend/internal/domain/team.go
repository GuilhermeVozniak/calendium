package domain

import (
	"fmt"
	"strings"
	"time"
)

// TeamRole orders member privileges: owner > admin > member.
type TeamRole string

const (
	TeamRoleOwner  TeamRole = "owner"
	TeamRoleAdmin  TeamRole = "admin"
	TeamRoleMember TeamRole = "member"
)

// ParseTeamRole validates a role path/body parameter.
func ParseTeamRole(s string) (TeamRole, error) {
	switch TeamRole(s) {
	case TeamRoleOwner, TeamRoleAdmin, TeamRoleMember:
		return TeamRole(s), nil
	}
	return "", fmt.Errorf("%w: unknown team role %q", ErrValidation, s)
}

// rank returns the privilege ordering used by AtLeast.
func (r TeamRole) rank() int {
	switch r {
	case TeamRoleOwner:
		return 3
	case TeamRoleAdmin:
		return 2
	case TeamRoleMember:
		return 1
	}
	return 0
}

// AtLeast reports whether r grants the privileges of min.
func (r TeamRole) AtLeast(min TeamRole) bool { return r.rank() >= min.rank() }

// Team is a collaboration group. Membership grants NOTHING by itself: every
// collaborative surface (shares, comments, read statuses, calendars,
// availability) requires its own explicit opt-in (privacy default).
type Team struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// CreatedBy is the creator's user id; empty when the creator's account
	// was deleted (teams.created_by is SET NULL on user deletion, scanned
	// back as COALESCE(created_by, '')).
	CreatedBy string    `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
}

// ValidateTeamName enforces the team-name invariant shared by create/rename.
func ValidateTeamName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 120 {
		return "", fmt.Errorf("%w: team name must be 1-120 characters", ErrValidation)
	}
	return name, nil
}

// TeamMember is a user's membership in a team.
type TeamMember struct {
	TeamID string   `json:"teamId"`
	UserID string   `json:"userId"`
	Role   TeamRole `json:"role"`
	// ShareReadStatuses opts this member's thread open/reply activity into
	// the team's read-status indicators. Privacy default: false — joining a
	// team never exposes activity without this explicit toggle.
	ShareReadStatuses bool      `json:"shareReadStatuses"`
	JoinedAt          time.Time `json:"joinedAt"`
	// Name and Email are display-identity enrichment resolved from the
	// users table when a member roster is read (TeamRepo.ListMembers
	// join). Least-leak: they are populated ONLY on rosters the caller
	// can already see by being a member — never via any global user
	// lookup. Empty when unresolvable; clients fall back to the id
	// (nothing is fabricated).
	Name  string `json:"name"`
	Email string `json:"email"`
}

// InvitationStatus is the lifecycle of an email invitation.
type InvitationStatus string

const (
	InvitePending  InvitationStatus = "pending"
	InviteAccepted InvitationStatus = "accepted"
	InviteRevoked  InvitationStatus = "revoked"
	InviteExpired  InvitationStatus = "expired"
)

// TeamInvitation is an email invitation to join a team. The raw token is
// shown once in the invite link; only its SHA-256 hash is stored.
// InvitationDelivery reports how an invitation was delivered: through the
// inviter's connected mailbox, the instance's SMTP sender, or — when neither
// exists — a link the inviter shares by hand. Response-only; never stored.
type InvitationDelivery string

const (
	DeliveryMailbox InvitationDelivery = "mailbox"
	DeliverySMTP    InvitationDelivery = "smtp"
	DeliveryLink    InvitationDelivery = "link"
)

type TeamInvitation struct {
	ID        string           `json:"id"`
	TeamID    string           `json:"teamId"`
	Email     string           `json:"email"`
	Role      TeamRole         `json:"role"`
	InvitedBy string           `json:"invitedBy"`
	Status    InvitationStatus `json:"status"`
	TokenHash string           `json:"-"`
	ExpiresAt time.Time        `json:"expiresAt"`
	CreatedAt time.Time        `json:"createdAt"`
	// Delivery and InviteURL are response-only (set by TeamService.Invite,
	// never persisted). InviteURL is populated only for DeliveryLink, where
	// the inviter must share the accept link themselves.
	Delivery  InvitationDelivery `json:"delivery,omitempty"`
	InviteURL string             `json:"inviteUrl,omitempty"`
}

// Usable reports whether the invitation can still be accepted at now.
func (i TeamInvitation) Usable(now time.Time) bool {
	return i.Status == InvitePending && now.Before(i.ExpiresAt)
}
