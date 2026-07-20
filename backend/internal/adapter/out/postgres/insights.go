package postgres

import (
	"context"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- Time insights (M2.8 Task 17) --------------------------------------------
//
// The insights aggregation needs the managed-events ledger WITHOUT a kind
// filter (managed_event.go's ListByUser is per-kind): every managed event of
// a user, so any kind — including kinds added by future automation tasks —
// is categorized generically.

type insightsManagedEventRepo struct{ *Store }

var _ port.InsightsManagedEventRepo = insightsManagedEventRepo{}

// InsightsManagedEvents returns the all-kinds managed-event reader.
func (s *Store) InsightsManagedEvents() port.InsightsManagedEventRepo {
	return insightsManagedEventRepo{s}
}

func (r insightsManagedEventRepo) ListAllByUser(ctx context.Context, userID string) ([]domain.ManagedEvent, error) {
	rows, err := r.q(ctx).QueryContext(ctx,
		`SELECT `+managedEventCols+` FROM managed_events
		 WHERE user_id = $1 ORDER BY created_at, event_id`, userID)
	if err != nil {
		return nil, err
	}
	return collectManagedEvents(rows)
}
