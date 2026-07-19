package postgres

import (
	"context"
	"fmt"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- port.ThreadShareRepo ----------------------------------------------------

var _ port.ThreadShareRepo = (*ThreadShareRepo)(nil)

// ThreadShareRepo persists tokenized live thread shares (table
// thread_shares, token stored hashed).
type ThreadShareRepo struct{ s *Store }

// NewThreadShareRepo returns the postgres port.ThreadShareRepo backed by s.
func NewThreadShareRepo(s *Store) *ThreadShareRepo { return &ThreadShareRepo{s: s} }

const threadShareCols = `id, thread_id, created_by, audience, team_id, token_hash, revoked_at, expires_at, created_at`

func scanThreadShare(r rowScanner) (domain.ThreadShare, error) {
	var sh domain.ThreadShare
	var audience string
	if err := r.Scan(&sh.ID, &sh.ThreadID, &sh.CreatedBy, &audience, &sh.TeamID,
		&sh.TokenHash, &sh.RevokedAt, &sh.ExpiresAt, &sh.CreatedAt); err != nil {
		return domain.ThreadShare{}, notFound(err)
	}
	sh.Audience = domain.ShareAudience(audience)
	return sh, nil
}

// Create inserts the share. A token_hash collision (astronomically unlikely
// for crypto/rand tokens) maps to domain.ErrConflict.
func (r *ThreadShareRepo) Create(ctx context.Context, sh domain.ThreadShare) (domain.ThreadShare, error) {
	if sh.ID == "" {
		sh.ID = newID()
	}
	err := r.s.q(ctx).QueryRowContext(ctx, `
		INSERT INTO thread_shares (id, thread_id, created_by, audience, team_id, token_hash, revoked_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING created_at`,
		sh.ID, sh.ThreadID, sh.CreatedBy, string(sh.Audience), sh.TeamID,
		sh.TokenHash, sh.RevokedAt, sh.ExpiresAt).Scan(&sh.CreatedAt)
	if err != nil {
		if isConflictSQLState(err) {
			return domain.ThreadShare{}, fmt.Errorf("%w: share token collision", domain.ErrConflict)
		}
		return domain.ThreadShare{}, fmt.Errorf("postgres: create thread share: %w", err)
	}
	return sh, nil
}

func (r *ThreadShareRepo) GetByID(ctx context.Context, id string) (domain.ThreadShare, error) {
	row := r.s.q(ctx).QueryRowContext(ctx,
		`SELECT `+threadShareCols+` FROM thread_shares WHERE id = $1`, id)
	return scanThreadShare(row)
}

func (r *ThreadShareRepo) GetByTokenHash(ctx context.Context, tokenHash string) (domain.ThreadShare, error) {
	row := r.s.q(ctx).QueryRowContext(ctx,
		`SELECT `+threadShareCols+` FROM thread_shares WHERE token_hash = $1`, tokenHash)
	return scanThreadShare(row)
}

func (r *ThreadShareRepo) ListByThread(ctx context.Context, threadID string) ([]domain.ThreadShare, error) {
	rows, err := r.s.q(ctx).QueryContext(ctx,
		`SELECT `+threadShareCols+` FROM thread_shares WHERE thread_id = $1 ORDER BY created_at, id`,
		threadID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	shares := []domain.ThreadShare{}
	for rows.Next() {
		sh, err := scanThreadShare(rows)
		if err != nil {
			return nil, err
		}
		shares = append(shares, sh)
	}
	return shares, rows.Err()
}

// Revoke stamps revoked_at; domain.ErrNotFound when the share is missing.
// Re-revoking keeps the ORIGINAL revocation time (idempotent).
func (r *ThreadShareRepo) Revoke(ctx context.Context, id string, at time.Time) error {
	return mustAffect(r.s.q(ctx).ExecContext(ctx,
		`UPDATE thread_shares SET revoked_at = COALESCE(revoked_at, $2) WHERE id = $1`, id, at))
}
