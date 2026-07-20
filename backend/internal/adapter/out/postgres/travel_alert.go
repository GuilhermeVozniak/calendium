package postgres

import (
	"context"
	"database/sql"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- port.TravelAlertRepo (M2.8 Task 12) -------------------------------------
//
// One leave-now alert per event (PK event_id), ON DELETE CASCADE from events
// so a deleted mirror takes its pending alert with it. sent_at NULL means
// undelivered; the (sent_at, leave_at) index serves ListDue's "unsent and
// due" scan.

func (s *Store) TravelAlerts() port.TravelAlertRepo { return travelAlertRepo{s} }

type travelAlertRepo struct{ *Store }

var _ port.TravelAlertRepo = (travelAlertRepo{})

func (r travelAlertRepo) Upsert(ctx context.Context, eventID, userID string, leaveAt time.Time) error {
	_, err := r.q(ctx).ExecContext(ctx, `
		INSERT INTO travel_alerts (event_id, user_id, leave_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (event_id) DO UPDATE SET
			user_id  = EXCLUDED.user_id,
			leave_at = EXCLUDED.leave_at,
			-- A moved leave time re-arms the alert; an unchanged one must NOT
			-- clear sent_at (idempotent passes would re-send every 5s).
			sent_at  = CASE WHEN travel_alerts.leave_at IS DISTINCT FROM EXCLUDED.leave_at
			                THEN NULL ELSE travel_alerts.sent_at END
	`, eventID, userID, leaveAt)
	return err
}

func (r travelAlertRepo) ListDue(ctx context.Context, now time.Time, limit int) ([]domain.TravelAlert, error) {
	rows, err := r.q(ctx).QueryContext(ctx, `
		SELECT event_id, user_id, leave_at, sent_at
		FROM travel_alerts
		WHERE sent_at IS NULL AND leave_at <= $1
		ORDER BY leave_at ASC
		LIMIT $2
	`, now, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []domain.TravelAlert{}
	for rows.Next() {
		var a domain.TravelAlert
		var sent sql.NullTime
		if err := rows.Scan(&a.EventID, &a.UserID, &a.LeaveAt, &sent); err != nil {
			return nil, err
		}
		a.SentAt = timePtr(sent)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r travelAlertRepo) MarkSent(ctx context.Context, eventID string, at time.Time) error {
	return mustAffect(r.q(ctx).ExecContext(ctx,
		`UPDATE travel_alerts SET sent_at = $2 WHERE event_id = $1`, eventID, at))
}

func (r travelAlertRepo) Delete(ctx context.Context, eventID string) error {
	_, err := r.q(ctx).ExecContext(ctx, `DELETE FROM travel_alerts WHERE event_id = $1`, eventID)
	return err
}
