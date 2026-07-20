package postgres

import (
	"context"
	"database/sql"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// IntegrationRepo implements port.IntegrationRepo over integration_connections
// (M2.8 Task 9). Like the team repos it is a standalone constructor over the
// shared *Store (same pool, same tx plumbing, same AES-256-GCM token vault as
// connected_accounts).
type IntegrationRepo struct{ s *Store }

func NewIntegrationRepo(s *Store) *IntegrationRepo { return &IntegrationRepo{s: s} }

var _ port.IntegrationRepo = (*IntegrationRepo)(nil)

const integrationCols = `id, user_id, vendor, external_account, status, last_error, created_at`

func scanIntegration(r rowScanner) (domain.IntegrationConnection, error) {
	var c domain.IntegrationConnection
	var lastError sql.NullString
	if err := r.Scan(&c.ID, &c.UserID, &c.Vendor, &c.ExternalAccount, &c.Status, &lastError, &c.CreatedAt); err != nil {
		return domain.IntegrationConnection{}, notFound(err)
	}
	c.LastError = strPtr(lastError)
	return c, nil
}

func (r *IntegrationRepo) Create(ctx context.Context, c domain.IntegrationConnection) (domain.IntegrationConnection, error) {
	if c.ID == "" {
		c.ID = newID()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	_, err := r.s.q(ctx).ExecContext(ctx, `
		INSERT INTO integration_connections (id, user_id, vendor, external_account, status, last_error, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		c.ID, c.UserID, string(c.Vendor), c.ExternalAccount, c.Status, nullStrPtr(c.LastError), c.CreatedAt)
	if err != nil {
		if isConflictSQLState(err) {
			return domain.IntegrationConnection{}, domain.ErrConflict
		}
		return domain.IntegrationConnection{}, err
	}
	return c, nil
}

func (r *IntegrationRepo) GetByID(ctx context.Context, id string) (domain.IntegrationConnection, error) {
	row := r.s.q(ctx).QueryRowContext(ctx,
		`SELECT `+integrationCols+` FROM integration_connections WHERE id = $1`, id)
	return scanIntegration(row)
}

func (r *IntegrationRepo) GetByVendor(ctx context.Context, userID string, vendor domain.IntegrationVendor) (domain.IntegrationConnection, error) {
	row := r.s.q(ctx).QueryRowContext(ctx,
		`SELECT `+integrationCols+` FROM integration_connections WHERE user_id = $1 AND vendor = $2`,
		userID, string(vendor))
	return scanIntegration(row)
}

func (r *IntegrationRepo) ListByUser(ctx context.Context, userID string) ([]domain.IntegrationConnection, error) {
	return r.list(ctx,
		`SELECT `+integrationCols+` FROM integration_connections WHERE user_id = $1 ORDER BY created_at, id`, userID)
}

func (r *IntegrationRepo) ListByVendor(ctx context.Context, vendor domain.IntegrationVendor) ([]domain.IntegrationConnection, error) {
	return r.list(ctx,
		`SELECT `+integrationCols+` FROM integration_connections WHERE vendor = $1 ORDER BY created_at, id`, string(vendor))
}

func (r *IntegrationRepo) list(ctx context.Context, query string, args ...any) ([]domain.IntegrationConnection, error) {
	rows, err := r.s.q(ctx).QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	conns := []domain.IntegrationConnection{}
	for rows.Next() {
		c, err := scanIntegration(rows)
		if err != nil {
			return nil, err
		}
		conns = append(conns, c)
	}
	return conns, rows.Err()
}

func (r *IntegrationRepo) Update(ctx context.Context, c domain.IntegrationConnection) error {
	return mustAffect(r.s.q(ctx).ExecContext(ctx, `
		UPDATE integration_connections SET
			external_account = $2,
			status           = $3,
			last_error       = $4
		WHERE id = $1`,
		c.ID, c.ExternalAccount, c.Status, nullStrPtr(c.LastError)))
}

func (r *IntegrationRepo) Delete(ctx context.Context, id string) error {
	return mustAffect(r.s.q(ctx).ExecContext(ctx,
		`DELETE FROM integration_connections WHERE id = $1`, id))
}

func (r *IntegrationRepo) SaveTokens(ctx context.Context, connectionID string, t port.TokenSet) error {
	access, err := r.s.sealToken(t.AccessToken)
	if err != nil {
		return err
	}
	refresh, err := r.s.sealToken(t.RefreshToken)
	if err != nil {
		return err
	}
	var expires sql.NullTime
	if !t.ExpiresAt.IsZero() {
		expires = sql.NullTime{Time: t.ExpiresAt, Valid: true}
	}
	return mustAffect(r.s.q(ctx).ExecContext(ctx, `
		UPDATE integration_connections
		SET access_token_enc = $2, refresh_token_enc = $3, token_expires_at = $4
		WHERE id = $1`,
		connectionID, access, refresh, expires))
}

func (r *IntegrationRepo) GetTokens(ctx context.Context, connectionID string) (port.TokenSet, error) {
	var access, refresh []byte
	var expires sql.NullTime
	err := r.s.q(ctx).QueryRowContext(ctx, `
		SELECT access_token_enc, refresh_token_enc, token_expires_at
		FROM integration_connections WHERE id = $1`, connectionID).
		Scan(&access, &refresh, &expires)
	if err != nil {
		return port.TokenSet{}, notFound(err)
	}
	var t port.TokenSet
	if t.AccessToken, err = r.s.openToken(access); err != nil {
		return port.TokenSet{}, err
	}
	if t.RefreshToken, err = r.s.openToken(refresh); err != nil {
		return port.TokenSet{}, err
	}
	if expires.Valid {
		t.ExpiresAt = expires.Time
	}
	return t, nil
}
