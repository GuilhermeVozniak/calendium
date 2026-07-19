package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// Team booking links (M2.7 Task 14).
//
// A booking link scoped to a team offers COLLECTIVE availability: the
// public slot computation intersects the creator's and every listed
// member's free/busy (every member must be free), and a confirmed booking
// invites every member as an attendee of a single event created on the
// CREATOR's calendar through the creator's provider tokens — never a
// cross-account event fan-out.
//
// Privacy/authz doctrine:
//   - the creator must be a member of the link's team (TeamRepo.GetMember is
//     the primitive: non-members get ErrNotFound → 404, no existence oracle);
//   - every listed member must have opted in by sharing at least one of
//     their calendars with the team at ≥ free_busy (Task 12), or be the
//     creator; violations are ErrValidation naming the non-sharing member;
//   - the public surface leaks nothing about the team: the public page
//     payload is unchanged and slots are opaque times;
//   - membership AND the calendar-share grant are re-checked per
//     slots/booking request, so removing a member from the team — or the
//     member revoking their team calendar share (the documented opt-in) —
//     immediately drops them from the intersection (fewer members
//     intersected — possibly none) and from the invite list; a revoked
//     opt-in never keeps leaking free/busy through public slots.
//
// Round-robin (fair rotation of a single host per booking) is future work:
// it needs per-link rotation state advanced inside the booking transaction
// (port.TxRunner) to stay race-safe, and is intentionally not implemented
// here — collective mode only.

// validateTeamLink authorizes and normalizes the team fields of a booking
// link input for creator userID: it returns the link's team scope and the
// deduplicated member list (creator dropped — it is implicit).
func (s *SchedulingService) validateTeamLink(ctx context.Context, userID string, in port.BookingLinkInput) (*string, []string, error) {
	if in.TeamID == nil || *in.TeamID == "" {
		if len(in.MemberUserIDs) > 0 {
			return nil, nil, fmt.Errorf("%w: memberUserIds requires teamId", domain.ErrValidation)
		}
		return nil, []string{}, nil
	}
	if s.teams == nil {
		return nil, nil, domain.ErrNotImplemented
	}
	teamID := *in.TeamID
	// Membership first: a creator outside the team cannot even learn the
	// team exists (404, never 403).
	if _, err := s.teams.GetMember(ctx, teamID, userID); err != nil {
		return nil, nil, err
	}
	members := make([]string, 0, len(in.MemberUserIDs))
	seen := make(map[string]struct{}, len(in.MemberUserIDs))
	for _, id := range in.MemberUserIDs {
		id = strings.TrimSpace(id)
		if id == "" || id == userID { // creator is implicit
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		members = append(members, id)
	}
	if len(members) > 0 {
		granted, err := s.teamGrantOwners(ctx, teamID)
		if err != nil {
			return nil, nil, err
		}
		for _, id := range members {
			if _, err := s.teams.GetMember(ctx, teamID, id); err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					return nil, nil, fmt.Errorf("%w: user %q is not a member of this team", domain.ErrValidation, id)
				}
				return nil, nil, err
			}
			if _, ok := granted[id]; !ok {
				return nil, nil, fmt.Errorf("%w: member %q has not shared free/busy availability with this team", domain.ErrValidation, id)
			}
		}
	}
	return ptr(teamID), members, nil
}

// teamGrantOwners returns the set of user IDs that have shared at least one
// of their own calendars with teamID at free_busy or better (the Task 12
// opt-in behind collective availability). Fail closed: a nil Shares repo
// means no grants are visible.
func (s *SchedulingService) teamGrantOwners(ctx context.Context, teamID string) (map[string]struct{}, error) {
	owners := map[string]struct{}{}
	if s.shares == nil {
		return owners, nil
	}
	shares, err := s.shares.ListForGrantee(ctx, "", []string{teamID})
	if err != nil {
		return nil, err
	}
	for _, sh := range shares {
		if sh.GranteeTeamID == nil || *sh.GranteeTeamID != teamID {
			continue
		}
		if !sh.Permission.AtLeast(domain.PermissionFreeBusy) {
			continue
		}
		c, err := s.calendars.GetByID(ctx, sh.CalendarID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				continue
			}
			return nil, err
		}
		acct, err := s.accounts.GetByID(ctx, c.AccountID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				continue
			}
			return nil, err
		}
		owners[acct.UserID] = struct{}{}
	}
	return owners, nil
}

// teamMembersBusy unions the busy intervals (local event mirror, same
// source as ownerBusy) of every listed member that is STILL a member of the
// link's team AND still shares free/busy with it (the Task 12 opt-in).
// Removed members — and members who revoked their team calendar grant —
// simply drop out of the intersection: availability is recomputed per
// request, so the link intersects fewer members (possibly none beyond the
// creator) without any stored-state invalidation, and a revoked opt-in
// stops leaking the member's free/busy into public slot computation.
func (s *SchedulingService) teamMembersBusy(ctx context.Context, link domain.BookingLink, from, to time.Time) ([]domain.BusyInterval, error) {
	if link.TeamID == nil || len(link.MemberUserIDs) == 0 {
		return nil, nil
	}
	if s.teams == nil {
		return nil, domain.ErrNotImplemented
	}
	granted, err := s.teamGrantOwners(ctx, *link.TeamID)
	if err != nil {
		return nil, err
	}
	var busy []domain.BusyInterval
	for _, memberID := range link.MemberUserIDs {
		if memberID == link.UserID {
			continue // creator's busy already comes from ownerBusy
		}
		if _, err := s.teams.GetMember(ctx, *link.TeamID, memberID); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				continue
			}
			return nil, err
		}
		if _, ok := granted[memberID]; !ok {
			continue // opt-in revoked — fail-closed skip, like team removal
		}
		mb, err := s.ownerBusy(ctx, memberID, from, to)
		if err != nil {
			return nil, err
		}
		busy = append(busy, mb...)
	}
	return busy, nil
}

// teamAttendeeEmails resolves the extra invitees for a confirmed booking on
// a team link: the email of every listed member still on the team AND still
// sharing free/busy with it. Errors other than a vanished member/user
// propagate; vanished ones — and members who revoked their opt-in — are
// skipped (matching teamMembersBusy's fewer/none semantics: a member who
// withdrew their availability must not keep being auto-invited).
func (s *SchedulingService) teamAttendeeEmails(ctx context.Context, link domain.BookingLink) ([]string, error) {
	if link.TeamID == nil {
		return nil, nil
	}
	var granted map[string]struct{}
	if len(link.MemberUserIDs) > 0 {
		var err error
		if granted, err = s.teamGrantOwners(ctx, *link.TeamID); err != nil {
			return nil, err
		}
	}
	var emails []string
	for _, memberID := range link.MemberUserIDs {
		if memberID == link.UserID {
			continue
		}
		if s.teams != nil {
			if _, err := s.teams.GetMember(ctx, *link.TeamID, memberID); err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					continue
				}
				return nil, err
			}
		}
		if _, ok := granted[memberID]; !ok {
			continue // opt-in revoked — no longer auto-invited
		}
		u, err := s.users.GetByID(ctx, memberID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				continue
			}
			return nil, err
		}
		if u.Email != "" {
			emails = append(emails, u.Email)
		}
	}
	return emails, nil
}

// appendMissingEmails appends each email in add not already present in dst
// (case-insensitive), preserving order.
func appendMissingEmails(dst, add []string) []string {
	seen := make(map[string]struct{}, len(dst))
	for _, e := range dst {
		seen[strings.ToLower(e)] = struct{}{}
	}
	for _, e := range add {
		k := strings.ToLower(e)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		dst = append(dst, e)
	}
	return dst
}
