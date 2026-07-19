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

// --- port.CalendarPrefsRepo (M2.8 Task 5) ------------------------------------
//
// calendar_prefs stores the whole domain.CalendarPrefs document in one JSONB
// column. Upsert follows the user_settings precedent: a full-document
// last-write-wins upsert stamped with now() — concurrency is handled by the
// service layer doing read-merge-write of the entire row, never by partial
// column updates.

func (r calendarPrefsRepo) Get(ctx context.Context, userID string) (domain.CalendarPrefs, error) {
	var raw []byte
	err := r.q(ctx).QueryRowContext(ctx,
		`SELECT prefs FROM calendar_prefs WHERE user_id = $1`, userID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DefaultCalendarPrefs(userID), nil
	}
	if err != nil {
		return domain.CalendarPrefs{}, err
	}
	return decodeCalendarPrefs(userID, raw)
}

func (r calendarPrefsRepo) Upsert(ctx context.Context, p domain.CalendarPrefs) error {
	raw, err := json.Marshal(p) // UserID is json:"-": the PK column is authoritative
	if err != nil {
		return err
	}
	_, err = r.q(ctx).ExecContext(ctx, `
		INSERT INTO calendar_prefs (user_id, prefs, updated_at)
		VALUES ($1, $2::jsonb, now())
		ON CONFLICT (user_id) DO UPDATE SET prefs = EXCLUDED.prefs, updated_at = now()`,
		p.UserID, raw)
	return err
}

// ListAutomated mirrors domain.CalendarPrefs.AutomationEnabled as a JSONB
// predicate so the worker's fan-out never scans users with defaults.
func (r calendarPrefsRepo) ListAutomated(ctx context.Context) ([]domain.CalendarPrefs, error) {
	rows, err := r.q(ctx).QueryContext(ctx, `
		SELECT user_id, prefs FROM calendar_prefs
		WHERE (prefs->>'focusGoalMinutesPerWeek')::int > 0
		   OR (prefs->>'autoBufferMinutes')::int > 0
		   OR (prefs->>'oooAutoDecline')::boolean
		   OR (prefs->>'travelBuffers')::boolean
		   OR (prefs->>'leaveAlerts')::boolean
		   OR (prefs->>'weatherEnabled')::boolean
		ORDER BY user_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []domain.CalendarPrefs{}
	for rows.Next() {
		var userID string
		var raw []byte
		if err := rows.Scan(&userID, &raw); err != nil {
			return nil, err
		}
		p, err := decodeCalendarPrefs(userID, raw)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// decodeCalendarPrefs unmarshals a stored document over the defaults, so
// fields added after a row was written come back with their default (not
// zero) values.
func decodeCalendarPrefs(userID string, raw []byte) (domain.CalendarPrefs, error) {
	p := domain.DefaultCalendarPrefs(userID)
	if err := json.Unmarshal(raw, &p); err != nil {
		return domain.CalendarPrefs{}, err
	}
	p.UserID = userID
	return p, nil
}
