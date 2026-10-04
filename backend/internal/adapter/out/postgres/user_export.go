package postgres

import (
	"context"
	"time"
)

// --- port.UserExportRepo -----------------------------------------------------

// Claim is one atomic upsert: the row is (re)stamped with now only when the
// previous export started at least window ago. When the conditional update
// touches nothing, the stored started_at tells the caller when to retry.
func (r userExportRepo) Claim(ctx context.Context, userID string, now time.Time, window time.Duration) (bool, time.Time, error) {
	res, err := r.q(ctx).ExecContext(ctx, `
		INSERT INTO user_exports (user_id, started_at) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET started_at = EXCLUDED.started_at
		WHERE user_exports.started_at <= $2::timestamptz - make_interval(secs => $3)`,
		userID, now.UTC(), window.Seconds())
	if err != nil {
		return false, time.Time{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, time.Time{}, err
	}
	if n == 1 {
		return true, time.Time{}, nil
	}
	var started time.Time
	if err := r.q(ctx).QueryRowContext(ctx,
		`SELECT started_at FROM user_exports WHERE user_id = $1`, userID).Scan(&started); err != nil {
		return false, time.Time{}, notFound(err)
	}
	return false, started.UTC().Add(window), nil
}
