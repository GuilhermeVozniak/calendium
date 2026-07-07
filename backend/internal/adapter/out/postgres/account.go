package postgres

import (
	"context"
	"database/sql"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- port.AccountRepo --------------------------------------------------------

const accountCols = `id, user_id, provider, email, status,
	array_to_json(scopes)::text, array_to_json(vip_senders)::text, last_synced_at, created_at`

func scanAccount(r rowScanner) (domain.ConnectedAccount, error) {
	var a domain.ConnectedAccount
	var scopes, vipSenders string
	var lastSynced sql.NullTime
	if err := r.Scan(&a.ID, &a.UserID, &a.Provider, &a.Email, &a.Status,
		&scopes, &vipSenders, &lastSynced, &a.CreatedAt); err != nil {
		return domain.ConnectedAccount{}, notFound(err)
	}
	if err := unmarshalInto([]byte(scopes), &a.Scopes); err != nil {
		return domain.ConnectedAccount{}, err
	}
	if err := unmarshalInto([]byte(vipSenders), &a.VIPSenders); err != nil {
		return domain.ConnectedAccount{}, err
	}
	if a.Scopes == nil {
		a.Scopes = []string{}
	}
	if a.VIPSenders == nil {
		a.VIPSenders = []string{}
	}
	a.LastSyncedAt = timePtr(lastSynced)
	return a, nil
}

// scopesExpr converts a JSON string array parameter into text[].
const scopesExpr = `(SELECT coalesce(array_agg(x), '{}'::text[]) FROM jsonb_array_elements_text($6::jsonb) x)`

func (r accountRepo) Create(ctx context.Context, a domain.ConnectedAccount) (domain.ConnectedAccount, error) {
	if a.ID == "" {
		a.ID = newID()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	scopes, err := jsonArray(a.Scopes)
	if err != nil {
		return domain.ConnectedAccount{}, err
	}
	_, err = r.q(ctx).ExecContext(ctx, `
		INSERT INTO connected_accounts (id, user_id, provider, email, status, scopes, last_synced_at, created_at)
		VALUES ($1, $2, $3, $4, $5, `+scopesExpr+`, $7, $8)`,
		a.ID, a.UserID, string(a.Provider), a.Email, string(a.Status), scopes,
		nullTimePtr(a.LastSyncedAt), a.CreatedAt)
	if err != nil {
		return domain.ConnectedAccount{}, err
	}
	if a.Scopes == nil {
		a.Scopes = []string{}
	}
	if a.VIPSenders == nil {
		a.VIPSenders = []string{}
	}
	return a, nil
}

func (r accountRepo) GetByID(ctx context.Context, id string) (domain.ConnectedAccount, error) {
	row := r.q(ctx).QueryRowContext(ctx,
		`SELECT `+accountCols+` FROM connected_accounts WHERE id = $1`, id)
	return scanAccount(row)
}

func (r accountRepo) ListByUser(ctx context.Context, userID string) ([]domain.ConnectedAccount, error) {
	return r.listAccounts(ctx,
		`SELECT `+accountCols+` FROM connected_accounts WHERE user_id = $1 ORDER BY created_at`, userID)
}

func (r accountRepo) ListSyncable(ctx context.Context) ([]domain.ConnectedAccount, error) {
	return r.listAccounts(ctx,
		`SELECT `+accountCols+` FROM connected_accounts WHERE status IN ('active', 'syncing') ORDER BY created_at`)
}

func (r accountRepo) listAccounts(ctx context.Context, query string, args ...any) ([]domain.ConnectedAccount, error) {
	rows, err := r.q(ctx).QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := []domain.ConnectedAccount{}
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

func (r accountRepo) Update(ctx context.Context, a domain.ConnectedAccount) error {
	scopes, err := jsonArray(a.Scopes)
	if err != nil {
		return err
	}
	vip, err := jsonArray(a.VIPSenders)
	if err != nil {
		return err
	}
	return mustAffect(r.q(ctx).ExecContext(ctx, `
		UPDATE connected_accounts SET
			email          = $2,
			status         = $3,
			scopes         = (SELECT coalesce(array_agg(x), '{}'::text[]) FROM jsonb_array_elements_text($4::jsonb) x),
			last_synced_at = $5,
			vip_senders    = (SELECT coalesce(array_agg(x), '{}'::text[]) FROM jsonb_array_elements_text($6::jsonb) x)
		WHERE id = $1`,
		a.ID, a.Email, string(a.Status), scopes, nullTimePtr(a.LastSyncedAt), vip))
}

func (r accountRepo) Delete(ctx context.Context, id string) error {
	return mustAffect(r.q(ctx).ExecContext(ctx,
		`DELETE FROM connected_accounts WHERE id = $1`, id))
}

func (r accountRepo) SaveTokens(ctx context.Context, accountID string, t port.TokenSet) error {
	access, err := r.sealToken(t.AccessToken)
	if err != nil {
		return err
	}
	refresh, err := r.sealToken(t.RefreshToken)
	if err != nil {
		return err
	}
	var expires sql.NullTime
	if !t.ExpiresAt.IsZero() {
		expires = sql.NullTime{Time: t.ExpiresAt, Valid: true}
	}
	return mustAffect(r.q(ctx).ExecContext(ctx, `
		UPDATE connected_accounts
		SET access_token_enc = $2, refresh_token_enc = $3, token_expires_at = $4
		WHERE id = $1`,
		accountID, access, refresh, expires))
}

func (r accountRepo) GetTokens(ctx context.Context, accountID string) (port.TokenSet, error) {
	var access, refresh []byte
	var expires sql.NullTime
	err := r.q(ctx).QueryRowContext(ctx, `
		SELECT access_token_enc, refresh_token_enc, token_expires_at
		FROM connected_accounts WHERE id = $1`, accountID).
		Scan(&access, &refresh, &expires)
	if err != nil {
		return port.TokenSet{}, notFound(err)
	}
	var t port.TokenSet
	if t.AccessToken, err = r.openToken(access); err != nil {
		return port.TokenSet{}, err
	}
	if t.RefreshToken, err = r.openToken(refresh); err != nil {
		return port.TokenSet{}, err
	}
	if expires.Valid {
		t.ExpiresAt = expires.Time
	}
	return t, nil
}

// --- port.OAuthStateRepo -----------------------------------------------------

func (r oauthStateRepo) Create(ctx context.Context, s port.OAuthState) error {
	_, err := r.q(ctx).ExecContext(ctx, `
		INSERT INTO oauth_states (state, user_id, provider, redirect_url, code_verifier, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		s.State, s.UserID, string(s.Provider), s.RedirectURL, nullStr(s.CodeVerifier), s.ExpiresAt)
	return err
}

func (r oauthStateRepo) Consume(ctx context.Context, state string) (port.OAuthState, error) {
	s := port.OAuthState{State: state}
	var verifier sql.NullString
	err := r.q(ctx).QueryRowContext(ctx, `
		DELETE FROM oauth_states WHERE state = $1
		RETURNING user_id, provider, redirect_url, code_verifier, expires_at`, state).
		Scan(&s.UserID, &s.Provider, &s.RedirectURL, &verifier, &s.ExpiresAt)
	if err != nil {
		return port.OAuthState{}, notFound(err)
	}
	s.CodeVerifier = verifier.String
	return s, nil
}

// --- port.SyncStateRepo ------------------------------------------------------

func (r syncStateRepo) Get(ctx context.Context, accountID, resource string) (port.SyncState, error) {
	s := port.SyncState{AccountID: accountID, Resource: resource}
	err := r.q(ctx).QueryRowContext(ctx, `
		SELECT sync_cursor, updated_at FROM sync_state
		WHERE account_id = $1 AND resource = $2`, accountID, resource).
		Scan(&s.Cursor, &s.UpdatedAt)
	if err != nil {
		return port.SyncState{}, notFound(err)
	}
	return s, nil
}

func (r syncStateRepo) Save(ctx context.Context, s port.SyncState) error {
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = time.Now().UTC()
	}
	_, err := r.q(ctx).ExecContext(ctx, `
		INSERT INTO sync_state (account_id, resource, sync_cursor, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (account_id, resource) DO UPDATE SET
			sync_cursor = EXCLUDED.sync_cursor,
			updated_at  = EXCLUDED.updated_at`,
		s.AccountID, s.Resource, s.Cursor, s.UpdatedAt)
	return err
}

func (r syncStateRepo) DeleteByAccount(ctx context.Context, accountID string) error {
	_, err := r.q(ctx).ExecContext(ctx,
		`DELETE FROM sync_state WHERE account_id = $1`, accountID)
	return err
}

// --- port.DeviceRepo ---------------------------------------------------------

const deviceCols = `id, user_id, platform, token, created_at`

func scanDevice(r rowScanner) (domain.NotificationDevice, error) {
	var d domain.NotificationDevice
	if err := r.Scan(&d.ID, &d.UserID, &d.Platform, &d.Token, &d.CreatedAt); err != nil {
		return domain.NotificationDevice{}, notFound(err)
	}
	return d, nil
}

func (r deviceRepo) Upsert(ctx context.Context, d domain.NotificationDevice) (domain.NotificationDevice, error) {
	if d.ID == "" {
		d.ID = newID()
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	row := r.q(ctx).QueryRowContext(ctx, `
		INSERT INTO devices (id, user_id, platform, token, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, token) DO UPDATE SET platform = EXCLUDED.platform
		RETURNING id, created_at`,
		d.ID, d.UserID, string(d.Platform), d.Token, d.CreatedAt)
	if err := row.Scan(&d.ID, &d.CreatedAt); err != nil {
		return domain.NotificationDevice{}, err
	}
	return d, nil
}

func (r deviceRepo) GetByID(ctx context.Context, id string) (domain.NotificationDevice, error) {
	row := r.q(ctx).QueryRowContext(ctx,
		`SELECT `+deviceCols+` FROM devices WHERE id = $1`, id)
	return scanDevice(row)
}

func (r deviceRepo) ListByUser(ctx context.Context, userID string) ([]domain.NotificationDevice, error) {
	rows, err := r.q(ctx).QueryContext(ctx,
		`SELECT `+deviceCols+` FROM devices WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	devices := []domain.NotificationDevice{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

func (r deviceRepo) Delete(ctx context.Context, id string) error {
	return mustAffect(r.q(ctx).ExecContext(ctx, `DELETE FROM devices WHERE id = $1`, id))
}
