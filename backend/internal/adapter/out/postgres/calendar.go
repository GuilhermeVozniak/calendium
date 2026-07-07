package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"calendium/backend/internal/domain"
)

// --- port.CalendarRepo -------------------------------------------------------

const calendarCols = `id, account_id, provider_calendar_id, name, color, time_zone,
	is_primary, is_visible, can_write`

func scanCalendar(r rowScanner) (domain.Calendar, error) {
	var c domain.Calendar
	if err := r.Scan(&c.ID, &c.AccountID, &c.ProviderCalendarID, &c.Name, &c.Color,
		&c.TimeZone, &c.IsPrimary, &c.IsVisible, &c.CanWrite); err != nil {
		return domain.Calendar{}, notFound(err)
	}
	return c, nil
}

func (r calendarRepo) Upsert(ctx context.Context, c domain.Calendar) (domain.Calendar, error) {
	if c.ID == "" {
		c.ID = newID()
	}
	if c.Color == "" {
		c.Color = "#6366f1"
	}
	if c.TimeZone == "" {
		c.TimeZone = "UTC"
	}
	// New calendars start visible; on conflict the local preferences
	// (is_visible, color) are preserved and read back.
	err := r.q(ctx).QueryRowContext(ctx, `
		INSERT INTO calendars (id, account_id, provider_calendar_id, name, color, time_zone,
			is_primary, is_visible, can_write)
		VALUES ($1, $2, $3, $4, $5, $6, $7, true, $8)
		ON CONFLICT (account_id, provider_calendar_id) DO UPDATE SET
			name       = EXCLUDED.name,
			time_zone  = EXCLUDED.time_zone,
			is_primary = EXCLUDED.is_primary,
			can_write  = EXCLUDED.can_write
		RETURNING id, color, is_visible`,
		c.ID, c.AccountID, c.ProviderCalendarID, c.Name, c.Color, c.TimeZone,
		c.IsPrimary, c.CanWrite).Scan(&c.ID, &c.Color, &c.IsVisible)
	if err != nil {
		return domain.Calendar{}, err
	}
	return c, nil
}

func (r calendarRepo) GetByID(ctx context.Context, id string) (domain.Calendar, error) {
	row := r.q(ctx).QueryRowContext(ctx,
		`SELECT `+calendarCols+` FROM calendars WHERE id = $1`, id)
	return scanCalendar(row)
}

func (r calendarRepo) ListByUser(ctx context.Context, userID string) ([]domain.Calendar, error) {
	return r.listCalendars(ctx, `
		SELECT c.id, c.account_id, c.provider_calendar_id, c.name, c.color, c.time_zone,
			c.is_primary, c.is_visible, c.can_write
		FROM calendars c
		JOIN connected_accounts ca ON ca.id = c.account_id
		WHERE ca.user_id = $1
		ORDER BY c.is_primary DESC, c.name`, userID)
}

func (r calendarRepo) ListByAccount(ctx context.Context, accountID string) ([]domain.Calendar, error) {
	return r.listCalendars(ctx,
		`SELECT `+calendarCols+` FROM calendars WHERE account_id = $1 ORDER BY is_primary DESC, name`,
		accountID)
}

func (r calendarRepo) listCalendars(ctx context.Context, query string, args ...any) ([]domain.Calendar, error) {
	rows, err := r.q(ctx).QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cals := []domain.Calendar{}
	for rows.Next() {
		c, err := scanCalendar(rows)
		if err != nil {
			return nil, err
		}
		cals = append(cals, c)
	}
	return cals, rows.Err()
}

func (r calendarRepo) Update(ctx context.Context, c domain.Calendar) error {
	return mustAffect(r.q(ctx).ExecContext(ctx, `
		UPDATE calendars SET
			name       = $2,
			color      = $3,
			time_zone  = $4,
			is_primary = $5,
			is_visible = $6,
			can_write  = $7
		WHERE id = $1`,
		c.ID, c.Name, c.Color, c.TimeZone, c.IsPrimary, c.IsVisible, c.CanWrite))
}

// --- port.EventRepo ----------------------------------------------------------

const eventCols = `e.id, e.calendar_id, e.provider_event_id, e.title, e.description,
	e.location, e.start_at, e.end_at, e.all_day, e.recurrence_rule, e.attendees,
	e.conferencing, e.status, e.visibility, array_to_json(e.reminder_minutes)::text`

func scanEvent(r rowScanner) (domain.Event, error) {
	var e domain.Event
	var description, location, rrule sql.NullString
	var attendees, conferencing []byte
	var reminders string
	if err := r.Scan(&e.ID, &e.CalendarID, &e.ProviderEventID, &e.Title, &description,
		&location, &e.Start, &e.End, &e.AllDay, &rrule, &attendees,
		&conferencing, &e.Status, &e.Visibility, &reminders); err != nil {
		return domain.Event{}, notFound(err)
	}
	e.Description = strPtr(description)
	e.Location = strPtr(location)
	e.RecurrenceRule = strPtr(rrule)
	if err := unmarshalInto(attendees, &e.Attendees); err != nil {
		return domain.Event{}, err
	}
	if e.Attendees == nil {
		e.Attendees = []domain.Attendee{}
	}
	if len(conferencing) > 0 {
		var conf domain.Conferencing
		if err := unmarshalInto(conferencing, &conf); err != nil {
			return domain.Event{}, err
		}
		e.Conferencing = &conf
	}
	if err := unmarshalInto([]byte(reminders), &e.ReminderMinutes); err != nil {
		return domain.Event{}, err
	}
	if e.ReminderMinutes == nil {
		e.ReminderMinutes = []int{}
	}
	return e, nil
}

func (r eventRepo) Upsert(ctx context.Context, e domain.Event) (domain.Event, error) {
	if e.ID == "" {
		e.ID = newID()
	}
	if e.Status == "" {
		e.Status = domain.EventConfirmed
	}
	if e.Visibility == "" {
		e.Visibility = domain.VisibilityDefault
	}
	attendees, err := jsonArray(e.Attendees)
	if err != nil {
		return domain.Event{}, err
	}
	var conferencing any
	if e.Conferencing != nil {
		b, err := json.Marshal(e.Conferencing)
		if err != nil {
			return domain.Event{}, fmt.Errorf("postgres: marshal conferencing: %w", err)
		}
		conferencing = string(b)
	}
	reminders, err := jsonArray(e.ReminderMinutes)
	if err != nil {
		return domain.Event{}, err
	}
	conflict := "(calendar_id, provider_event_id)"
	if e.ProviderEventID == "" {
		conflict = "(id)"
	}
	err = r.q(ctx).QueryRowContext(ctx, `
		INSERT INTO events (id, calendar_id, provider_event_id, title, description, location,
			start_at, end_at, all_day, recurrence_rule, attendees, conferencing, status,
			visibility, reminder_minutes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb, $12::jsonb, $13, $14,
			(SELECT coalesce(array_agg(x::integer), '{}'::integer[]) FROM jsonb_array_elements_text($15::jsonb) x))
		ON CONFLICT `+conflict+` DO UPDATE SET
			title            = EXCLUDED.title,
			description      = EXCLUDED.description,
			location         = EXCLUDED.location,
			start_at         = EXCLUDED.start_at,
			end_at           = EXCLUDED.end_at,
			all_day          = EXCLUDED.all_day,
			recurrence_rule  = EXCLUDED.recurrence_rule,
			attendees        = EXCLUDED.attendees,
			conferencing     = EXCLUDED.conferencing,
			status           = EXCLUDED.status,
			visibility       = EXCLUDED.visibility,
			reminder_minutes = EXCLUDED.reminder_minutes,
			updated_at       = now()
		RETURNING id`,
		e.ID, e.CalendarID, e.ProviderEventID, e.Title, nullStrPtr(e.Description),
		nullStrPtr(e.Location), e.Start, e.End, e.AllDay, nullStrPtr(e.RecurrenceRule),
		attendees, conferencing, string(e.Status), string(e.Visibility), reminders).Scan(&e.ID)
	if err != nil {
		return domain.Event{}, err
	}
	if e.Attendees == nil {
		e.Attendees = []domain.Attendee{}
	}
	if e.ReminderMinutes == nil {
		e.ReminderMinutes = []int{}
	}
	return e, nil
}

func (r eventRepo) GetByID(ctx context.Context, id string) (domain.Event, error) {
	row := r.q(ctx).QueryRowContext(ctx,
		`SELECT `+eventCols+` FROM events e WHERE e.id = $1`, id)
	return scanEvent(row)
}

func (r eventRepo) GetByProviderID(ctx context.Context, calendarID, providerEventID string) (domain.Event, error) {
	row := r.q(ctx).QueryRowContext(ctx,
		`SELECT `+eventCols+` FROM events e WHERE e.calendar_id = $1 AND e.provider_event_id = $2`,
		calendarID, providerEventID)
	return scanEvent(row)
}

func (r eventRepo) ListInRange(ctx context.Context, userID string, from, to time.Time, calendarIDs []string) ([]domain.Event, error) {
	query := `
		SELECT ` + eventCols + `
		FROM events e
		JOIN calendars c ON c.id = e.calendar_id
		JOIN connected_accounts ca ON ca.id = c.account_id
		WHERE ca.user_id = $1 AND e.start_at < $3 AND e.end_at > $2`
	args := []any{userID, from, to}
	if len(calendarIDs) > 0 {
		// Explicit calendar selection overrides the visibility preference.
		ids, err := jsonArray(calendarIDs)
		if err != nil {
			return nil, err
		}
		args = append(args, ids)
		query += ` AND e.calendar_id IN (SELECT jsonb_array_elements_text($4::jsonb))`
	} else {
		// Default: only calendars the user has kept visible (the is_visible
		// preference gates both event listing and availability).
		query += ` AND c.is_visible`
	}
	query += ` ORDER BY e.start_at, e.id`

	rows, err := r.q(ctx).QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectEvents(rows)
}

func (r eventRepo) Delete(ctx context.Context, id string) error {
	return mustAffect(r.q(ctx).ExecContext(ctx, `DELETE FROM events WHERE id = $1`, id))
}

func (r eventRepo) DeleteByProviderID(ctx context.Context, calendarID, providerEventID string) error {
	return mustAffect(r.q(ctx).ExecContext(ctx,
		`DELETE FROM events WHERE calendar_id = $1 AND provider_event_id = $2`,
		calendarID, providerEventID))
}

func (r eventRepo) Search(ctx context.Context, userID, query string, limit int) ([]domain.Event, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.q(ctx).QueryContext(ctx, `
		SELECT `+eventCols+`
		FROM events e
		JOIN calendars c ON c.id = e.calendar_id
		JOIN connected_accounts ca ON ca.id = c.account_id
		WHERE ca.user_id = $1 AND (
			to_tsvector('simple', coalesce(e.title, '') || ' ' || coalesce(e.description, '') || ' ' || coalesce(e.location, ''))
				@@ plainto_tsquery('simple', $2)
			OR e.title ILIKE '%' || $2 || '%')
		ORDER BY e.start_at DESC
		LIMIT $3`, userID, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectEvents(rows)
}

func collectEvents(rows *sql.Rows) ([]domain.Event, error) {
	events := []domain.Event{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
