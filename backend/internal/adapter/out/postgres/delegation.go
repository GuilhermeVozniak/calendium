package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- port.DelegationRepo -----------------------------------------------------

var _ port.DelegationRepo = (*DelegationRepo)(nil)

// DelegationRepo persists EA delegation grants (table delegations).
type DelegationRepo struct{ s *Store }

// NewDelegationRepo returns the postgres port.DelegationRepo backed by s.
func NewDelegationRepo(s *Store) *DelegationRepo { return &DelegationRepo{s: s} }

const delegationCols = `id, principal_id, assistant_id, array_to_json(scopes)::text, status, created_at, accepted_at, revoked_at`

// delegationScopesExpr converts a JSON string-array parameter into text[].
const delegationScopesExpr = `(SELECT coalesce(array_agg(x), '{}'::text[]) FROM jsonb_array_elements_text($4::jsonb) x)`

func scanDelegation(r rowScanner) (domain.Delegation, error) {
	var d domain.Delegation
	var scopes, status string
	var accepted, revoked sql.NullTime
	if err := r.Scan(&d.ID, &d.PrincipalID, &d.AssistantID, &scopes, &status,
		&d.CreatedAt, &accepted, &revoked); err != nil {
		return domain.Delegation{}, notFound(err)
	}
	d.Status = domain.DelegationStatus(status)
	d.AcceptedAt = timePtr(accepted)
	d.RevokedAt = timePtr(revoked)
	if err := unmarshalInto([]byte(scopes), &d.Scopes); err != nil {
		return domain.Delegation{}, err
	}
	return d, nil
}

// Create inserts a grant. A second LIVE (pending or active) grant for the
// same (principal, assistant) pair violates the partial unique index and
// maps to domain.ErrConflict; revoked rows never block a re-grant.
func (r *DelegationRepo) Create(ctx context.Context, d domain.Delegation) (domain.Delegation, error) {
	if d.ID == "" {
		d.ID = newID()
	}
	if d.Status == "" {
		d.Status = domain.DelegationPending
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	scopes, err := jsonArray(d.Scopes)
	if err != nil {
		return domain.Delegation{}, err
	}
	_, err = r.s.q(ctx).ExecContext(ctx, `
		INSERT INTO delegations (id, principal_id, assistant_id, scopes, status, created_at, accepted_at, revoked_at)
		VALUES ($1, $2, $3, `+delegationScopesExpr+`, $5, $6, $7, $8)`,
		d.ID, d.PrincipalID, d.AssistantID, scopes, string(d.Status),
		d.CreatedAt, nullTimePtr(d.AcceptedAt), nullTimePtr(d.RevokedAt))
	if err != nil {
		if isConflictSQLState(err) {
			return domain.Delegation{}, fmt.Errorf("%w: a delegation between these users already exists", domain.ErrConflict)
		}
		return domain.Delegation{}, fmt.Errorf("postgres: create delegation: %w", err)
	}
	return d, nil
}

func (r *DelegationRepo) GetByID(ctx context.Context, id string) (domain.Delegation, error) {
	row := r.s.q(ctx).QueryRowContext(ctx,
		`SELECT `+delegationCols+` FROM delegations WHERE id = $1`, id)
	return scanDelegation(row)
}

// GetActive returns the (principal, assistant) grant only while its status
// is 'active'. The status filter lives in SQL so every authorization
// re-reads the current row — a revocation is visible to the very next call.
func (r *DelegationRepo) GetActive(ctx context.Context, principalID, assistantID string) (domain.Delegation, error) {
	row := r.s.q(ctx).QueryRowContext(ctx,
		`SELECT `+delegationCols+` FROM delegations
		 WHERE principal_id = $1 AND assistant_id = $2 AND status = 'active'`,
		principalID, assistantID)
	return scanDelegation(row)
}

func (r *DelegationRepo) ListByUser(ctx context.Context, userID string) ([]domain.Delegation, error) {
	rows, err := r.s.q(ctx).QueryContext(ctx,
		`SELECT `+delegationCols+` FROM delegations
		 WHERE principal_id = $1 OR assistant_id = $1
		 ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []domain.Delegation{}
	for rows.Next() {
		d, err := scanDelegation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Update rewrites the mutable lifecycle fields (scopes, status,
// accepted_at, revoked_at).
func (r *DelegationRepo) Update(ctx context.Context, d domain.Delegation) error {
	scopes, err := jsonArray(d.Scopes)
	if err != nil {
		return err
	}
	return mustAffect(r.s.q(ctx).ExecContext(ctx, `
		UPDATE delegations SET
			status      = $2,
			accepted_at = $3,
			scopes      = (SELECT coalesce(array_agg(x), '{}'::text[]) FROM jsonb_array_elements_text($4::jsonb) x),
			revoked_at  = $5
		WHERE id = $1`,
		d.ID, string(d.Status), nullTimePtr(d.AcceptedAt), scopes, nullTimePtr(d.RevokedAt)))
}

// Delegated-mutation audit entries persist through the shared AuditRepo
// (sharing.go) into the unified audit_entries table (migration 0016).

// --- port.DelegationUserDirectory --------------------------------------------

var _ port.DelegationUserDirectory = (*UserDirectory)(nil)

// UserDirectory resolves users by email (case-insensitive) for delegation
// grant targets.
type UserDirectory struct{ s *Store }

// NewUserDirectory returns the postgres port.DelegationUserDirectory backed by s.
func NewUserDirectory(s *Store) *UserDirectory { return &UserDirectory{s: s} }

func (r *UserDirectory) GetByEmail(ctx context.Context, email string) (domain.User, error) {
	row := r.s.q(ctx).QueryRowContext(ctx,
		`SELECT `+userCols+` FROM users WHERE lower(email) = lower($1) ORDER BY created_at, id LIMIT 1`,
		email)
	return scanUser(row)
}

// GetByID resolves a grant party for display-identity enrichment. Only the
// DelegationService calls it, and only with the ids already on a grant the
// caller is a party to — it is not a global user-lookup surface.
func (r *UserDirectory) GetByID(ctx context.Context, id string) (domain.User, error) {
	row := r.s.q(ctx).QueryRowContext(ctx,
		`SELECT `+userCols+` FROM users WHERE id = $1`, id)
	return scanUser(row)
}
