package service

import (
	"context"
	"fmt"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// membership loads the caller's membership; non-members are
// indistinguishable from a missing team (404, never 403).
func membership(ctx context.Context, teams port.TeamRepo, userID, teamID string) (domain.TeamMember, error) {
	m, err := teams.GetMember(ctx, teamID, userID)
	if err != nil {
		return domain.TeamMember{}, domain.ErrNotFound
	}
	return m, nil
}

// requireRole gates an action on a minimum role for a known member.
func requireRole(m domain.TeamMember, min domain.TeamRole) error {
	if !m.Role.AtLeast(min) {
		return fmt.Errorf("%w: requires %s role", domain.ErrForbidden, min)
	}
	return nil
}
