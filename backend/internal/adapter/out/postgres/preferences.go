package postgres

import (
	"context"
	"database/sql"
	"errors"

	"calendium/backend/internal/port"
)

// Get returns the user's cross-device preferences (named theme), defaulting
// to port.DefaultTheme when the row is absent (never ErrNotFound).
func (r userPreferencesRepo) Get(ctx context.Context, userID string) (port.UserPreferences, error) {
	var p port.UserPreferences
	err := r.q(ctx).QueryRowContext(ctx,
		`SELECT theme FROM user_preferences WHERE user_id = $1`, userID).Scan(&p.Theme)
	if errors.Is(err, sql.ErrNoRows) {
		return port.UserPreferences{Theme: port.DefaultTheme}, nil
	}
	if err != nil {
		return port.UserPreferences{}, err
	}
	return p, nil
}

// Put upserts the preference document (second Put overwrites the first).
func (r userPreferencesRepo) Put(ctx context.Context, userID string, p port.UserPreferences) error {
	_, err := r.q(ctx).ExecContext(ctx, `
		INSERT INTO user_preferences (user_id, theme, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (user_id) DO UPDATE SET theme = EXCLUDED.theme, updated_at = now()`,
		userID, p.Theme)
	return err
}
