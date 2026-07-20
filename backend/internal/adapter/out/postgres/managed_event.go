package postgres

import (
	"context"
	"database/sql"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- Managed events (M2.8 Task 6) -------------------------------------------
//
// managed_events tags provider-real mirrored events as automation-owned so
// the engine only ever shrinks, extends, or deletes its own blocks. Rows
// cascade away with the mirrored event row.

type managedEventRepo struct{ *Store }

var _ port.ManagedEventRepo = managedEventRepo{}

// ManagedEvents returns the managed-events repo.
func (s *Store) ManagedEvents() port.ManagedEventRepo { return managedEventRepo{s} }

const managedEventCols = `event_id, user_id, kind, source_event_id, week_start, created_at`

func (r managedEventRepo) Create(ctx context.Context, m domain.ManagedEvent) error {
	_, err := r.q(ctx).ExecContext(ctx, `
		INSERT INTO managed_events (event_id, user_id, kind, source_event_id, week_start)
		VALUES ($1, $2, $3, $4, $5)`,
		m.EventID, m.UserID, string(m.Kind), m.SourceEventID, m.WeekStart)
	return err
}

func (r managedEventRepo) GetByEventID(ctx context.Context, eventID string) (domain.ManagedEvent, error) {
	row := r.q(ctx).QueryRowContext(ctx,
		`SELECT `+managedEventCols+` FROM managed_events WHERE event_id = $1`, eventID)
	return scanManagedEvent(row)
}

func (r managedEventRepo) ListByUser(ctx context.Context, userID string, kind domain.ManagedKind) ([]domain.ManagedEvent, error) {
	rows, err := r.q(ctx).QueryContext(ctx,
		`SELECT `+managedEventCols+` FROM managed_events
		 WHERE user_id = $1 AND kind = $2 ORDER BY created_at, event_id`,
		userID, string(kind))
	if err != nil {
		return nil, err
	}
	return collectManagedEvents(rows)
}

func (r managedEventRepo) ListBySourceEvent(ctx context.Context, sourceEventID string) ([]domain.ManagedEvent, error) {
	rows, err := r.q(ctx).QueryContext(ctx,
		`SELECT `+managedEventCols+` FROM managed_events
		 WHERE source_event_id = $1 ORDER BY created_at, event_id`, sourceEventID)
	if err != nil {
		return nil, err
	}
	return collectManagedEvents(rows)
}

func (r managedEventRepo) Delete(ctx context.Context, eventID string) error {
	_, err := r.q(ctx).ExecContext(ctx,
		`DELETE FROM managed_events WHERE event_id = $1`, eventID)
	return err
}

func scanManagedEvent(row rowScanner) (domain.ManagedEvent, error) {
	var m domain.ManagedEvent
	var source sql.NullString
	var weekStart sql.NullTime
	if err := row.Scan(&m.EventID, &m.UserID, &m.Kind, &source, &weekStart, &m.CreatedAt); err != nil {
		return domain.ManagedEvent{}, notFound(err)
	}
	m.SourceEventID = strPtr(source)
	if weekStart.Valid {
		ws := weekStart.Time
		m.WeekStart = &ws
	}
	return m, nil
}

func collectManagedEvents(rows *sql.Rows) ([]domain.ManagedEvent, error) {
	defer func() { _ = rows.Close() }()
	out := []domain.ManagedEvent{}
	for rows.Next() {
		m, err := scanManagedEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
