package postgres

import (
	"context"
	"database/sql"

	"calendium/backend/internal/domain"
)

// --- EventTemplateRepo ---------------------------------------------------------

func (r eventTemplateRepo) Create(ctx context.Context, userID string, t domain.EventTemplate) (domain.EventTemplate, error) {
	id := newID()
	if t.DurationMinutes <= 0 {
		t.DurationMinutes = 30 // Default per migration
	}
	attendeeJSON, err := jsonArray(t.AttendeeEmails)
	if err != nil {
		return domain.EventTemplate{}, err
	}
	reminderJSON, err := jsonArray(t.ReminderMinutes)
	if err != nil {
		return domain.EventTemplate{}, err
	}

	_, err = r.q(ctx).ExecContext(ctx, `
		INSERT INTO event_templates
		(id, user_id, name, title, description, location, duration_minutes, all_day,
		 calendar_id, attendee_emails, add_conferencing, reminder_minutes, recurrence_rule)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11, $12::jsonb, $13)
	`, id, userID, t.Name, t.Title, t.Description, t.Location, t.DurationMinutes,
		t.AllDay, nullStrPtr(t.CalendarID), attendeeJSON, t.AddConferencing, reminderJSON,
		nullStrPtr(t.RecurrenceRule))
	if err != nil {
		return domain.EventTemplate{}, err
	}

	t.ID = id
	t.UsageCount = 0
	return t, nil
}

func (r eventTemplateRepo) GetByID(ctx context.Context, id string) (domain.EventTemplate, string, error) {
	var t domain.EventTemplate
	var userID string
	var attendeeJSON, reminderJSON []byte
	var calendarID, recurrenceRule sql.NullString

	row := r.q(ctx).QueryRowContext(ctx, `
		SELECT id, user_id, name, title, description, location, duration_minutes, all_day,
		       calendar_id, attendee_emails, add_conferencing, reminder_minutes, recurrence_rule, usage_count
		FROM event_templates
		WHERE id = $1
	`, id)

	err := row.Scan(&t.ID, &userID, &t.Name, &t.Title, &t.Description, &t.Location,
		&t.DurationMinutes, &t.AllDay,
		&calendarID, &attendeeJSON, &t.AddConferencing, &reminderJSON,
		&recurrenceRule, &t.UsageCount)
	if err != nil {
		return domain.EventTemplate{}, "", notFound(err)
	}

	t.CalendarID = strPtr(calendarID)
	t.RecurrenceRule = strPtr(recurrenceRule)

	if err := unmarshalInto(attendeeJSON, &t.AttendeeEmails); err != nil {
		return domain.EventTemplate{}, "", err
	}
	if err := unmarshalInto(reminderJSON, &t.ReminderMinutes); err != nil {
		return domain.EventTemplate{}, "", err
	}

	return t, userID, nil
}

func (r eventTemplateRepo) ListByUser(ctx context.Context, userID string) ([]domain.EventTemplate, error) {
	rows, err := r.q(ctx).QueryContext(ctx, `
		SELECT id, user_id, name, title, description, location, duration_minutes, all_day,
		       calendar_id, attendee_emails, add_conferencing, reminder_minutes, recurrence_rule, usage_count
		FROM event_templates
		WHERE user_id = $1
		ORDER BY name ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var list []domain.EventTemplate
	for rows.Next() {
		var t domain.EventTemplate
		var userID string
		var attendeeJSON, reminderJSON []byte
		var calendarID, recurrenceRule sql.NullString

		if err := rows.Scan(&t.ID, &userID, &t.Name, &t.Title, &t.Description, &t.Location,
			&t.DurationMinutes, &t.AllDay,
			&calendarID, &attendeeJSON, &t.AddConferencing, &reminderJSON,
			&recurrenceRule, &t.UsageCount); err != nil {
			return nil, err
		}

		t.CalendarID = strPtr(calendarID)
		t.RecurrenceRule = strPtr(recurrenceRule)

		if err := unmarshalInto(attendeeJSON, &t.AttendeeEmails); err != nil {
			return nil, err
		}
		if err := unmarshalInto(reminderJSON, &t.ReminderMinutes); err != nil {
			return nil, err
		}

		list = append(list, t)
	}
	return list, rows.Err()
}

func (r eventTemplateRepo) Update(ctx context.Context, t domain.EventTemplate) error {
	attendeeJSON, err := jsonArray(t.AttendeeEmails)
	if err != nil {
		return err
	}
	reminderJSON, err := jsonArray(t.ReminderMinutes)
	if err != nil {
		return err
	}

	res, err := r.q(ctx).ExecContext(ctx, `
		UPDATE event_templates
		SET name = $1, title = $2, description = $3, location = $4,
		    duration_minutes = $5, all_day = $6, calendar_id = $7,
		    attendee_emails = $8::jsonb, add_conferencing = $9,
		    reminder_minutes = $10::jsonb, recurrence_rule = $11,
		    updated_at = now()
		WHERE id = $12
	`, t.Name, t.Title, t.Description, t.Location, t.DurationMinutes, t.AllDay,
		nullStrPtr(t.CalendarID), attendeeJSON, t.AddConferencing,
		reminderJSON, nullStrPtr(t.RecurrenceRule), t.ID)

	return mustAffect(res, err)
}

func (r eventTemplateRepo) IncrementUsage(ctx context.Context, id string) error {
	res, err := r.q(ctx).ExecContext(ctx, `
		UPDATE event_templates
		SET usage_count = usage_count + 1, updated_at = now()
		WHERE id = $1
	`, id)
	return mustAffect(res, err)
}

func (r eventTemplateRepo) Delete(ctx context.Context, id string) error {
	res, err := r.q(ctx).ExecContext(ctx, `DELETE FROM event_templates WHERE id = $1`, id)
	return mustAffect(res, err)
}

// --- CalendarSetRepo ----------------------------------------------------------

func (r calendarSetRepo) Create(ctx context.Context, userID string, s domain.CalendarSet) (domain.CalendarSet, error) {
	id := newID()
	calendarIDsJSON, err := jsonArray(s.CalendarIDs)
	if err != nil {
		return domain.CalendarSet{}, err
	}

	_, err = r.q(ctx).ExecContext(ctx, `
		INSERT INTO calendar_sets (id, user_id, name, calendar_ids, position)
		VALUES ($1, $2, $3, $4::jsonb, $5)
	`, id, userID, s.Name, calendarIDsJSON, s.Position)
	if err != nil {
		return domain.CalendarSet{}, err
	}

	s.ID = id
	return s, nil
}

func (r calendarSetRepo) GetByID(ctx context.Context, id string) (domain.CalendarSet, string, error) {
	var s domain.CalendarSet
	var userID string
	var calendarIDsJSON []byte

	row := r.q(ctx).QueryRowContext(ctx, `
		SELECT id, user_id, name, calendar_ids, position
		FROM calendar_sets
		WHERE id = $1
	`, id)

	err := row.Scan(&s.ID, &userID, &s.Name, &calendarIDsJSON, &s.Position)
	if err != nil {
		return domain.CalendarSet{}, "", notFound(err)
	}

	if err := unmarshalInto(calendarIDsJSON, &s.CalendarIDs); err != nil {
		return domain.CalendarSet{}, "", err
	}

	return s, userID, nil
}

func (r calendarSetRepo) ListByUser(ctx context.Context, userID string) ([]domain.CalendarSet, error) {
	rows, err := r.q(ctx).QueryContext(ctx, `
		SELECT id, user_id, name, calendar_ids, position
		FROM calendar_sets
		WHERE user_id = $1
		ORDER BY position ASC, name ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var list []domain.CalendarSet
	for rows.Next() {
		var s domain.CalendarSet
		var userID string
		var calendarIDsJSON []byte

		if err := rows.Scan(&s.ID, &userID, &s.Name, &calendarIDsJSON, &s.Position); err != nil {
			return nil, err
		}

		if err := unmarshalInto(calendarIDsJSON, &s.CalendarIDs); err != nil {
			return nil, err
		}

		list = append(list, s)
	}
	return list, rows.Err()
}

func (r calendarSetRepo) Update(ctx context.Context, s domain.CalendarSet) error {
	calendarIDsJSON, err := jsonArray(s.CalendarIDs)
	if err != nil {
		return err
	}

	res, err := r.q(ctx).ExecContext(ctx, `
		UPDATE calendar_sets
		SET name = $1, calendar_ids = $2::jsonb, position = $3, updated_at = now()
		WHERE id = $4
	`, s.Name, calendarIDsJSON, s.Position, s.ID)

	return mustAffect(res, err)
}

func (r calendarSetRepo) Delete(ctx context.Context, id string) error {
	res, err := r.q(ctx).ExecContext(ctx, `DELETE FROM calendar_sets WHERE id = $1`, id)
	return mustAffect(res, err)
}
