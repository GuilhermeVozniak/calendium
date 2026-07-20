package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- port.CalendarSubscriptionRepo (M2.8 Task 15) ---------------------------

// CalendarSubscriptions returns the ICS feed subscription repo. The accessor
// lives here rather than in store.go to keep the M2.8 wave purely additive
// per file.
func (s *Store) CalendarSubscriptions() port.CalendarSubscriptionRepo {
	return calendarSubscriptionRepo{s}
}

type calendarSubscriptionRepo struct{ *Store }

var _ port.CalendarSubscriptionRepo = calendarSubscriptionRepo{}

const calSubCols = `id, user_id, url, name, color, is_visible, etag, last_fetched_at, last_error, created_at`

func scanCalSub(r rowScanner) (domain.CalendarSubscription, error) {
	var s domain.CalendarSubscription
	var lastFetched sql.NullTime
	var lastError sql.NullString
	if err := r.Scan(&s.ID, &s.UserID, &s.URL, &s.Name, &s.Color, &s.IsVisible,
		&s.Etag, &lastFetched, &lastError, &s.CreatedAt); err != nil {
		return domain.CalendarSubscription{}, notFound(err)
	}
	s.LastFetchedAt = timePtr(lastFetched)
	s.LastError = strPtr(lastError)
	return s, nil
}

// Create inserts the subscription. A second row for the same (user, url) —
// the UNIQUE(user_id, url) constraint — maps to domain.ErrConflict.
func (r calendarSubscriptionRepo) Create(ctx context.Context, s domain.CalendarSubscription) (domain.CalendarSubscription, error) {
	if s.ID == "" {
		s.ID = newID()
	}
	row := r.q(ctx).QueryRowContext(ctx, `
		INSERT INTO calendar_subscriptions (id, user_id, url, name, color, is_visible, etag, last_fetched_at, last_error)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+calSubCols,
		s.ID, s.UserID, s.URL, s.Name, s.Color, s.IsVisible, s.Etag,
		nullTimePtr(s.LastFetchedAt), nullStrPtr(s.LastError))
	created, err := scanCalSub(row)
	if err != nil {
		if isConflictSQLState(err) {
			return domain.CalendarSubscription{}, fmt.Errorf("%w: this calendar feed is already subscribed", domain.ErrConflict)
		}
		return domain.CalendarSubscription{}, fmt.Errorf("postgres: create calendar subscription: %w", err)
	}
	return created, nil
}

func (r calendarSubscriptionRepo) GetByID(ctx context.Context, id string) (domain.CalendarSubscription, error) {
	row := r.q(ctx).QueryRowContext(ctx,
		`SELECT `+calSubCols+` FROM calendar_subscriptions WHERE id = $1`, id)
	return scanCalSub(row)
}

func (r calendarSubscriptionRepo) ListByUser(ctx context.Context, userID string) ([]domain.CalendarSubscription, error) {
	rows, err := r.q(ctx).QueryContext(ctx,
		`SELECT `+calSubCols+` FROM calendar_subscriptions WHERE user_id = $1 ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, err
	}
	return collectCalSubs(rows)
}

// ListDue returns subscriptions whose last fetch is older than `since` (or
// that were never fetched), oldest first — the hourly refresh work queue.
func (r calendarSubscriptionRepo) ListDue(ctx context.Context, since time.Time) ([]domain.CalendarSubscription, error) {
	rows, err := r.q(ctx).QueryContext(ctx,
		`SELECT `+calSubCols+` FROM calendar_subscriptions
		 WHERE last_fetched_at IS NULL OR last_fetched_at <= $1
		 ORDER BY last_fetched_at ASC NULLS FIRST, id`, since)
	if err != nil {
		return nil, err
	}
	return collectCalSubs(rows)
}

func collectCalSubs(rows *sql.Rows) ([]domain.CalendarSubscription, error) {
	defer func() { _ = rows.Close() }()
	subs := []domain.CalendarSubscription{}
	for rows.Next() {
		s, err := scanCalSub(rows)
		if err != nil {
			return nil, err
		}
		subs = append(subs, s)
	}
	return subs, rows.Err()
}

// Update rewrites the mutable fields. Identity (user_id, url) is immutable
// after Create — changing the URL is a delete + re-subscribe.
func (r calendarSubscriptionRepo) Update(ctx context.Context, s domain.CalendarSubscription) error {
	return mustAffect(r.q(ctx).ExecContext(ctx, `
		UPDATE calendar_subscriptions SET
			name            = $2,
			color           = $3,
			is_visible      = $4,
			etag            = $5,
			last_fetched_at = $6,
			last_error      = $7
		WHERE id = $1`,
		s.ID, s.Name, s.Color, s.IsVisible, s.Etag,
		nullTimePtr(s.LastFetchedAt), nullStrPtr(s.LastError)))
}

func (r calendarSubscriptionRepo) Delete(ctx context.Context, id string) error {
	return mustAffect(r.q(ctx).ExecContext(ctx,
		`DELETE FROM calendar_subscriptions WHERE id = $1`, id))
}

// ReplaceEvents atomically swaps the expanded occurrence set for a
// subscription — feeds own truth, nothing is preserved. Event UIDs travel
// in domain.Event.ProviderEventID.
func (r calendarSubscriptionRepo) ReplaceEvents(ctx context.Context, subscriptionID string, events []domain.Event) error {
	return r.RunInTx(ctx, func(ctx context.Context) error {
		if _, err := r.q(ctx).ExecContext(ctx,
			`DELETE FROM subscription_events WHERE subscription_id = $1`, subscriptionID); err != nil {
			return fmt.Errorf("postgres: clear subscription events: %w", err)
		}
		for _, ev := range events {
			id := ev.ID
			if id == "" {
				id = newID()
			}
			if _, err := r.q(ctx).ExecContext(ctx, `
				INSERT INTO subscription_events (id, subscription_id, uid, title, description, location, starts_at, ends_at, all_day)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
				id, subscriptionID, ev.ProviderEventID, ev.Title,
				nullStrPtr(ev.Description), nullStrPtr(ev.Location),
				ev.Start, ev.End, ev.AllDay); err != nil {
				return fmt.Errorf("postgres: insert subscription event: %w", err)
			}
		}
		return nil
	})
}

// ListEventsInRange returns occurrences overlapping [from, to) across the
// user's VISIBLE subscriptions, as read-only domain.Events (SubscriptionID
// set, Status confirmed). User scoping happens in SQL via the owning
// subscription row — cross-tenant reads are structurally impossible.
func (r calendarSubscriptionRepo) ListEventsInRange(ctx context.Context, userID string, from, to time.Time) ([]domain.Event, error) {
	rows, err := r.q(ctx).QueryContext(ctx, `
		SELECT e.id, e.subscription_id, e.uid, e.title, e.description, e.location, e.starts_at, e.ends_at, e.all_day
		FROM subscription_events e
		JOIN calendar_subscriptions s ON s.id = e.subscription_id
		WHERE s.user_id = $1 AND s.is_visible AND e.starts_at < $3 AND e.ends_at > $2
		ORDER BY e.starts_at, e.id`, userID, from, to)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	events := []domain.Event{}
	for rows.Next() {
		var ev domain.Event
		var subID string
		var description, location sql.NullString
		if err := rows.Scan(&ev.ID, &subID, &ev.ProviderEventID, &ev.Title,
			&description, &location, &ev.Start, &ev.End, &ev.AllDay); err != nil {
			return nil, err
		}
		ev.SubscriptionID = &subID
		ev.Description = strPtr(description)
		ev.Location = strPtr(location)
		ev.Status = domain.EventConfirmed
		ev.Visibility = domain.VisibilityDefault
		ev.Attendees = []domain.Attendee{}
		ev.ReminderMinutes = []int{}
		events = append(events, ev)
	}
	return events, rows.Err()
}
