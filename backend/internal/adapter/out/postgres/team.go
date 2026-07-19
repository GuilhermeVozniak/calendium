package postgres

import (
	"context"
	"fmt"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- port.TeamRepo -----------------------------------------------------------

var _ port.TeamRepo = (*TeamRepo)(nil)

// TeamRepo persists teams and memberships (tables teams / team_members).
type TeamRepo struct{ s *Store }

// NewTeamRepo returns the postgres port.TeamRepo backed by s.
func NewTeamRepo(s *Store) *TeamRepo { return &TeamRepo{s: s} }

const teamCols = `id, name, created_by, created_at`

func scanTeam(r rowScanner) (domain.Team, error) {
	var t domain.Team
	if err := r.Scan(&t.ID, &t.Name, &t.CreatedBy, &t.CreatedAt); err != nil {
		return domain.Team{}, notFound(err)
	}
	return t, nil
}

const teamMemberCols = `team_id, user_id, role, share_read_statuses, joined_at`

func scanTeamMember(r rowScanner) (domain.TeamMember, error) {
	var m domain.TeamMember
	var role string
	if err := r.Scan(&m.TeamID, &m.UserID, &role, &m.ShareReadStatuses, &m.JoinedAt); err != nil {
		return domain.TeamMember{}, notFound(err)
	}
	m.Role = domain.TeamRole(role)
	return m, nil
}

// Create inserts the team and its owner membership in one transaction: a
// team must never exist without an owner row.
func (r *TeamRepo) Create(ctx context.Context, t domain.Team, owner domain.TeamMember) (domain.Team, error) {
	if t.ID == "" {
		t.ID = newID()
	}
	if owner.Role == "" {
		owner.Role = domain.TeamRoleOwner
	}
	err := r.s.RunInTx(ctx, func(ctx context.Context) error {
		if err := r.s.q(ctx).QueryRowContext(ctx, `
			INSERT INTO teams (id, name, created_by)
			VALUES ($1, $2, $3)
			RETURNING created_at`,
			t.ID, t.Name, t.CreatedBy).Scan(&t.CreatedAt); err != nil {
			return fmt.Errorf("postgres: create team: %w", err)
		}
		if _, err := r.s.q(ctx).ExecContext(ctx, `
			INSERT INTO team_members (team_id, user_id, role, share_read_statuses)
			VALUES ($1, $2, $3, $4)`,
			t.ID, owner.UserID, string(owner.Role), owner.ShareReadStatuses); err != nil {
			return fmt.Errorf("postgres: create owner membership: %w", err)
		}
		return nil
	})
	if err != nil {
		return domain.Team{}, err
	}
	return t, nil
}

func (r *TeamRepo) GetByID(ctx context.Context, id string) (domain.Team, error) {
	row := r.s.q(ctx).QueryRowContext(ctx,
		`SELECT `+teamCols+` FROM teams WHERE id = $1`, id)
	return scanTeam(row)
}

// ListByUser is membership-scoped in SQL: only teams the user has a
// team_members row for are visible.
func (r *TeamRepo) ListByUser(ctx context.Context, userID string) ([]domain.Team, error) {
	rows, err := r.s.q(ctx).QueryContext(ctx, `
		SELECT t.id, t.name, t.created_by, t.created_at
		FROM teams t
		JOIN team_members m ON m.team_id = t.id AND m.user_id = $1
		ORDER BY t.created_at, t.id`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	teams := []domain.Team{}
	for rows.Next() {
		t, err := scanTeam(rows)
		if err != nil {
			return nil, err
		}
		teams = append(teams, t)
	}
	return teams, rows.Err()
}

func (r *TeamRepo) Update(ctx context.Context, t domain.Team) error {
	return mustAffect(r.s.q(ctx).ExecContext(ctx,
		`UPDATE teams SET name = $2 WHERE id = $1`, t.ID, t.Name))
}

func (r *TeamRepo) Delete(ctx context.Context, id string) error {
	return mustAffect(r.s.q(ctx).ExecContext(ctx,
		`DELETE FROM teams WHERE id = $1`, id))
}

// GetMember returns domain.ErrNotFound for non-members — the authz
// primitive behind service-layer membership checks.
func (r *TeamRepo) GetMember(ctx context.Context, teamID, userID string) (domain.TeamMember, error) {
	row := r.s.q(ctx).QueryRowContext(ctx,
		`SELECT `+teamMemberCols+` FROM team_members WHERE team_id = $1 AND user_id = $2`,
		teamID, userID)
	return scanTeamMember(row)
}

func (r *TeamRepo) ListMembers(ctx context.Context, teamID string) ([]domain.TeamMember, error) {
	rows, err := r.s.q(ctx).QueryContext(ctx,
		`SELECT `+teamMemberCols+` FROM team_members WHERE team_id = $1 ORDER BY joined_at, user_id`,
		teamID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	members := []domain.TeamMember{}
	for rows.Next() {
		m, err := scanTeamMember(rows)
		if err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

// UpsertMember inserts or updates role/share_read_statuses; joined_at is
// preserved on update.
func (r *TeamRepo) UpsertMember(ctx context.Context, m domain.TeamMember) error {
	_, err := r.s.q(ctx).ExecContext(ctx, `
		INSERT INTO team_members (team_id, user_id, role, share_read_statuses)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (team_id, user_id) DO UPDATE SET
			role                = EXCLUDED.role,
			share_read_statuses = EXCLUDED.share_read_statuses`,
		m.TeamID, m.UserID, string(m.Role), m.ShareReadStatuses)
	return err
}

func (r *TeamRepo) RemoveMember(ctx context.Context, teamID, userID string) error {
	return mustAffect(r.s.q(ctx).ExecContext(ctx,
		`DELETE FROM team_members WHERE team_id = $1 AND user_id = $2`, teamID, userID))
}

// CountByRole supports the last-owner invariant.
func (r *TeamRepo) CountByRole(ctx context.Context, teamID string, role domain.TeamRole) (int, error) {
	var n int
	err := r.s.q(ctx).QueryRowContext(ctx,
		`SELECT count(*) FROM team_members WHERE team_id = $1 AND role = $2`,
		teamID, string(role)).Scan(&n)
	return n, err
}

// --- port.TeamInvitationRepo -------------------------------------------------

var _ port.TeamInvitationRepo = (*TeamInvitationRepo)(nil)

// TeamInvitationRepo persists email invitations (table team_invitations,
// token stored hashed).
type TeamInvitationRepo struct{ s *Store }

// NewTeamInvitationRepo returns the postgres port.TeamInvitationRepo backed by s.
func NewTeamInvitationRepo(s *Store) *TeamInvitationRepo { return &TeamInvitationRepo{s: s} }

const teamInvitationCols = `id, team_id, email, role, invited_by, status, token_hash, expires_at, created_at`

func scanTeamInvitation(r rowScanner) (domain.TeamInvitation, error) {
	var inv domain.TeamInvitation
	var role, status string
	if err := r.Scan(&inv.ID, &inv.TeamID, &inv.Email, &role, &inv.InvitedBy,
		&status, &inv.TokenHash, &inv.ExpiresAt, &inv.CreatedAt); err != nil {
		return domain.TeamInvitation{}, notFound(err)
	}
	inv.Role = domain.TeamRole(role)
	inv.Status = domain.InvitationStatus(status)
	return inv, nil
}

// Create inserts a (by default pending) invitation. A second pending invite
// for the same (team, lower(email)) — team_invitations_pending_idx — or a
// token_hash collision maps to domain.ErrConflict.
func (r *TeamInvitationRepo) Create(ctx context.Context, inv domain.TeamInvitation) (domain.TeamInvitation, error) {
	if inv.ID == "" {
		inv.ID = newID()
	}
	if inv.Role == "" {
		inv.Role = domain.TeamRoleMember
	}
	if inv.Status == "" {
		inv.Status = domain.InvitePending
	}
	err := r.s.q(ctx).QueryRowContext(ctx, `
		INSERT INTO team_invitations (id, team_id, email, role, invited_by, status, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING created_at`,
		inv.ID, inv.TeamID, inv.Email, string(inv.Role), inv.InvitedBy,
		string(inv.Status), inv.TokenHash, inv.ExpiresAt).Scan(&inv.CreatedAt)
	if err != nil {
		if isConflictSQLState(err) {
			return domain.TeamInvitation{}, fmt.Errorf("%w: a pending invitation for this address already exists", domain.ErrConflict)
		}
		return domain.TeamInvitation{}, fmt.Errorf("postgres: create team invitation: %w", err)
	}
	return inv, nil
}

func (r *TeamInvitationRepo) GetByID(ctx context.Context, id string) (domain.TeamInvitation, error) {
	row := r.s.q(ctx).QueryRowContext(ctx,
		`SELECT `+teamInvitationCols+` FROM team_invitations WHERE id = $1`, id)
	return scanTeamInvitation(row)
}

func (r *TeamInvitationRepo) GetByTokenHash(ctx context.Context, tokenHash string) (domain.TeamInvitation, error) {
	row := r.s.q(ctx).QueryRowContext(ctx,
		`SELECT `+teamInvitationCols+` FROM team_invitations WHERE token_hash = $1`, tokenHash)
	return scanTeamInvitation(row)
}

func (r *TeamInvitationRepo) ListByTeam(ctx context.Context, teamID string) ([]domain.TeamInvitation, error) {
	rows, err := r.s.q(ctx).QueryContext(ctx,
		`SELECT `+teamInvitationCols+` FROM team_invitations WHERE team_id = $1 ORDER BY created_at, id`,
		teamID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	invitations := []domain.TeamInvitation{}
	for rows.Next() {
		inv, err := scanTeamInvitation(rows)
		if err != nil {
			return nil, err
		}
		invitations = append(invitations, inv)
	}
	return invitations, rows.Err()
}

// Update rewrites the mutable lifecycle fields (role, status, expires_at).
// Re-opening an invitation to pending can trip team_invitations_pending_idx;
// that maps to domain.ErrConflict.
func (r *TeamInvitationRepo) Update(ctx context.Context, inv domain.TeamInvitation) error {
	res, err := r.s.q(ctx).ExecContext(ctx, `
		UPDATE team_invitations SET role = $2, status = $3, expires_at = $4
		WHERE id = $1`,
		inv.ID, string(inv.Role), string(inv.Status), inv.ExpiresAt)
	if err != nil {
		if isConflictSQLState(err) {
			return fmt.Errorf("%w: a pending invitation for this address already exists", domain.ErrConflict)
		}
		return err
	}
	return mustAffect(res, nil)
}
