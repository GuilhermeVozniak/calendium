package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// --- port.AiUsageRepo -----------------------------------------------------------

// IncrementAndCheck atomically bumps the user's counter for day and reports
// whether this call was within limit: the UPDATE branch's WHERE clause
// refuses the bump once calls reaches limit, so zero rows return means
// allowed=false and the counter is left unchanged.
func (r aiUsageRepo) IncrementAndCheck(ctx context.Context, userID string, day time.Time, limit int) (bool, error) {
	var calls int
	err := r.q(ctx).QueryRowContext(ctx, `
		INSERT INTO ai_usage (user_id, day, calls) VALUES ($1, $2, 1)
		ON CONFLICT (user_id, day)
		DO UPDATE SET calls = ai_usage.calls + 1 WHERE ai_usage.calls < $3
		RETURNING calls`,
		userID, day, limit).Scan(&calls)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
