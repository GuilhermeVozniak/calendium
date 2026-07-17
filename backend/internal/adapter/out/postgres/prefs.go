package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

var _ port.PrefsRepo = prefsRepo{}

func (r prefsRepo) Get(ctx context.Context, userID string) (domain.UserPrefs, error) {
	var raw []byte
	err := r.q(ctx).QueryRowContext(ctx,
		`SELECT split_order FROM user_prefs WHERE user_id = $1`, userID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.UserPrefs{}, nil
	}
	if err != nil {
		return domain.UserPrefs{}, err
	}
	var p domain.UserPrefs
	if err := json.Unmarshal(raw, &p.SplitOrder); err != nil {
		return domain.UserPrefs{}, err
	}
	return p, nil
}

func (r prefsRepo) Save(ctx context.Context, userID string, p domain.UserPrefs) error {
	raw, err := json.Marshal(p.SplitOrder)
	if err != nil {
		return err
	}
	_, err = r.q(ctx).ExecContext(ctx, `
		INSERT INTO user_prefs (user_id, split_order, updated_at)
		VALUES ($1, $2::jsonb, now())
		ON CONFLICT (user_id) DO UPDATE SET split_order = EXCLUDED.split_order, updated_at = now()`,
		userID, raw)
	return err
}
