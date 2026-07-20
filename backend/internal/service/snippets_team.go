package service

// Team-scoped snippets (M2.7 Task 11). A snippet whose TeamID is set is
// shared with every member of that team: any member can contribute one
// (consistent-reply value beats gatekeeping), while editing or deleting
// someone else's team snippet takes admin+. Authorization follows the
// collab doctrine (collab_helpers.go): non-members get ErrNotFound — team
// existence never leaks — and under-privileged members get ErrForbidden.

import (
	"context"
	"fmt"

	"calendium/backend/internal/domain"
)

// snippetTeamMember is the membership gate for snippet operations. A
// MailService wired without a TeamRepo has team snippets disabled: every
// team reference is then an unknown team (ErrNotFound), same as for a
// non-member.
func (s *MailService) snippetTeamMember(ctx context.Context, userID, teamID string) (domain.TeamMember, error) {
	if s.teams == nil {
		return domain.TeamMember{}, fmt.Errorf("%w: unknown team", domain.ErrNotFound)
	}
	return membership(ctx, s.teams, userID, teamID)
}

// teamSnippets returns the snippets of every team the user belongs to.
func (s *MailService) teamSnippets(ctx context.Context, userID string) ([]domain.Snippet, error) {
	if s.teams == nil {
		return nil, nil
	}
	teams, err := s.teams.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(teams) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(teams))
	for _, t := range teams {
		ids = append(ids, t.ID)
	}
	return s.snippets.ListByTeams(ctx, ids)
}

// mergeSnippets concatenates lists, dropping duplicate IDs, and always
// returns a non-nil slice (JSON [] rather than null).
func mergeSnippets(lists ...[]domain.Snippet) []domain.Snippet {
	out := []domain.Snippet{}
	seen := make(map[string]bool)
	for _, list := range lists {
		for _, sn := range list {
			if seen[sn.ID] {
				continue
			}
			seen[sn.ID] = true
			out = append(out, sn)
		}
	}
	return out
}

// annotateSnippetPermissions computes CanDelete for userID on every
// snippet in place — EXACT parity with editableSnippet: personal snippets
// belong to the caller (ListByUser is user-scoped), team snippets take
// author or admin+. Roles are resolved once per team; a failed membership
// lookup fails closed (CanDelete=false, the server still decides).
func (s *MailService) annotateSnippetPermissions(ctx context.Context, userID string, snips []domain.Snippet) {
	roles := map[string]domain.TeamRole{}
	for i := range snips {
		sn := &snips[i]
		if sn.TeamID == nil || sn.UserID == userID {
			sn.CanDelete = true
			continue
		}
		role, ok := roles[*sn.TeamID]
		if !ok {
			if m, err := s.snippetTeamMember(ctx, userID, *sn.TeamID); err == nil {
				role = m.Role
			}
			roles[*sn.TeamID] = role
		}
		sn.CanDelete = role.AtLeast(domain.TeamRoleAdmin)
	}
}

// editableSnippet loads snippetID and authorizes userID to mutate it.
// Personal snippet: owner only — anyone else sees ErrNotFound (M2.1
// behavior unchanged). Team snippet: caller must be a member (non-members
// see ErrNotFound), and must be the author or an admin+ (else
// ErrForbidden).
func (s *MailService) editableSnippet(ctx context.Context, userID, snippetID string) (domain.Snippet, error) {
	snip, err := s.snippets.GetByID(ctx, snippetID)
	if err != nil {
		return domain.Snippet{}, err
	}
	if snip.TeamID == nil {
		if snip.UserID != userID {
			return domain.Snippet{}, domain.ErrNotFound
		}
		return snip, nil
	}
	m, err := s.snippetTeamMember(ctx, userID, *snip.TeamID)
	if err != nil {
		return domain.Snippet{}, err
	}
	if snip.UserID == userID {
		return snip, nil // the author manages their own contribution
	}
	if err := requireRole(m, domain.TeamRoleAdmin); err != nil {
		return domain.Snippet{}, err
	}
	return snip, nil
}
