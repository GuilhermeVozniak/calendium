package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- port.CalendarShareRepo --------------------------------------------------

var _ port.CalendarShareRepo = (*CalendarShareRepo)(nil)

// CalendarShareRepo persists calendar sharing grants (table calendar_shares).
type CalendarShareRepo struct{ s *Store }

// NewCalendarShareRepo returns the postgres port.CalendarShareRepo backed by s.
func NewCalendarShareRepo(s *Store) *CalendarShareRepo { return &CalendarShareRepo{s: s} }

const calendarShareCols = `id, calendar_id, grantee_user_id, grantee_team_id, permission, created_by, created_at`

func scanCalendarShare(r rowScanner) (domain.CalendarShare, error) {
	var sh domain.CalendarShare
	var granteeUser, granteeTeam sql.NullString
	var perm string
	if err := r.Scan(&sh.ID, &sh.CalendarID, &granteeUser, &granteeTeam, &perm,
		&sh.CreatedBy, &sh.CreatedAt); err != nil {
		return domain.CalendarShare{}, notFound(err)
	}
	sh.GranteeUserID = strPtr(granteeUser)
	sh.GranteeTeamID = strPtr(granteeTeam)
	sh.Permission = domain.CalendarPermission(perm)
	return sh, nil
}

// Create inserts the grant. A second grant for the same (calendar, grantee)
// pair — calendar_shares_user_idx / calendar_shares_team_idx — maps to
// domain.ErrConflict.
func (r *CalendarShareRepo) Create(ctx context.Context, s domain.CalendarShare) (domain.CalendarShare, error) {
	if s.ID == "" {
		s.ID = newID()
	}
	if s.Permission == "" {
		s.Permission = domain.PermissionFreeBusy
	}
	err := r.s.q(ctx).QueryRowContext(ctx, `
		INSERT INTO calendar_shares (id, calendar_id, grantee_user_id, grantee_team_id, permission, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at`,
		s.ID, s.CalendarID, nullStrPtr(s.GranteeUserID), nullStrPtr(s.GranteeTeamID),
		string(s.Permission), s.CreatedBy).Scan(&s.CreatedAt)
	if err != nil {
		if isConflictSQLState(err) {
			return domain.CalendarShare{}, fmt.Errorf("%w: calendar already shared with this grantee", domain.ErrConflict)
		}
		return domain.CalendarShare{}, fmt.Errorf("postgres: create calendar share: %w", err)
	}
	return s, nil
}

func (r *CalendarShareRepo) ListByCalendar(ctx context.Context, calendarID string) ([]domain.CalendarShare, error) {
	return r.listShares(ctx,
		`SELECT `+calendarShareCols+` FROM calendar_shares WHERE calendar_id = $1 ORDER BY created_at, id`,
		calendarID)
}

// ListForGrantee returns every share granted to the user directly or to any
// of the given teams.
func (r *CalendarShareRepo) ListForGrantee(ctx context.Context, userID string, teamIDs []string) ([]domain.CalendarShare, error) {
	ids, err := jsonArray(teamIDs)
	if err != nil {
		return nil, err
	}
	return r.listShares(ctx, `
		SELECT `+calendarShareCols+` FROM calendar_shares
		WHERE grantee_user_id = $1
		   OR grantee_team_id IN (SELECT jsonb_array_elements_text($2::jsonb))
		ORDER BY created_at, id`, userID, ids)
}

func (r *CalendarShareRepo) listShares(ctx context.Context, query string, args ...any) ([]domain.CalendarShare, error) {
	rows, err := r.s.q(ctx).QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	shares := []domain.CalendarShare{}
	for rows.Next() {
		sh, err := scanCalendarShare(rows)
		if err != nil {
			return nil, err
		}
		shares = append(shares, sh)
	}
	return shares, rows.Err()
}

// Update rewrites the grant's permission (the only mutable field).
func (r *CalendarShareRepo) Update(ctx context.Context, s domain.CalendarShare) error {
	return mustAffect(r.s.q(ctx).ExecContext(ctx,
		`UPDATE calendar_shares SET permission = $2 WHERE id = $1`,
		s.ID, string(s.Permission)))
}

func (r *CalendarShareRepo) Delete(ctx context.Context, id string) error {
	return mustAffect(r.s.q(ctx).ExecContext(ctx,
		`DELETE FROM calendar_shares WHERE id = $1`, id))
}

// --- port.AuditRepo ----------------------------------------------------------

var _ port.AuditRepo = (*AuditRepo)(nil)

// AuditRepo persists the cross-principal mutation trail (table audit_entries).
type AuditRepo struct{ s *Store }

// NewAuditRepo returns the postgres port.AuditRepo backed by s.
func NewAuditRepo(s *Store) *AuditRepo { return &AuditRepo{s: s} }

func (r *AuditRepo) Record(ctx context.Context, e domain.AuditEntry) error {
	if e.ID == "" {
		e.ID = newID()
	}
	meta, err := jsonObject(e.Metadata)
	if err != nil {
		return err
	}
	var createdAt any
	if !e.CreatedAt.IsZero() {
		createdAt = e.CreatedAt
	}
	_, err = r.s.q(ctx).ExecContext(ctx, `
		INSERT INTO audit_entries (id, actor_id, principal_id, action, resource_type, resource_id, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, coalesce($8::timestamptz, now()))`,
		e.ID, e.ActorID, e.PrincipalID, e.Action, e.ResourceType, e.ResourceID, meta, createdAt)
	if err != nil {
		return fmt.Errorf("postgres: record audit entry: %w", err)
	}
	return nil
}

// ListByPrincipal returns the principal's newest entries first.
func (r *AuditRepo) ListByPrincipal(ctx context.Context, principalID string, limit int) ([]domain.AuditEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.s.q(ctx).QueryContext(ctx, `
		SELECT id, actor_id, principal_id, action, resource_type, resource_id, metadata, created_at
		FROM audit_entries
		WHERE principal_id = $1
		ORDER BY created_at DESC, id
		LIMIT $2`, principalID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	entries := []domain.AuditEntry{}
	for rows.Next() {
		var e domain.AuditEntry
		var meta []byte
		if err := rows.Scan(&e.ID, &e.ActorID, &e.PrincipalID, &e.Action,
			&e.ResourceType, &e.ResourceID, &meta, &e.CreatedAt); err != nil {
			return nil, err
		}
		if err := unmarshalInto(meta, &e.Metadata); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
