package postgres

import (
	"context"

	"calendium/backend/internal/domain"
)

// --- EventNoteRepo (M2.8 Task 4) ---------------------------------------------
//
// Notes are local-only: one row per event, ON DELETE CASCADE from events, so
// a provider-side delete mirrored locally takes the note with it.

func (r eventNoteRepo) Upsert(ctx context.Context, n domain.EventNote) (domain.EventNote, error) {
	if n.BodyMD == "" && len(n.Links) == 0 {
		// Empty note = delete; a missing row is fine (idempotent clear).
		if _, err := r.q(ctx).ExecContext(ctx,
			`DELETE FROM event_notes WHERE event_id = $1`, n.EventID); err != nil {
			return domain.EventNote{}, err
		}
		return domain.EventNote{EventID: n.EventID, UserID: n.UserID, Links: []string{}}, nil
	}

	linksJSON, err := jsonArray(n.Links)
	if err != nil {
		return domain.EventNote{}, err
	}
	row := r.q(ctx).QueryRowContext(ctx, `
		INSERT INTO event_notes (event_id, user_id, body_md, links, updated_at)
		VALUES ($1, $2, $3, $4::jsonb, now())
		ON CONFLICT (event_id) DO UPDATE
		SET user_id = EXCLUDED.user_id, body_md = EXCLUDED.body_md,
		    links = EXCLUDED.links, updated_at = now()
		RETURNING updated_at
	`, n.EventID, n.UserID, n.BodyMD, linksJSON)
	if err := row.Scan(&n.UpdatedAt); err != nil {
		return domain.EventNote{}, err
	}
	if n.Links == nil {
		n.Links = []string{}
	}
	return n, nil
}

func (r eventNoteRepo) GetByEventID(ctx context.Context, eventID string) (domain.EventNote, error) {
	var n domain.EventNote
	var linksJSON []byte
	row := r.q(ctx).QueryRowContext(ctx, `
		SELECT event_id, user_id, body_md, links, updated_at
		FROM event_notes
		WHERE event_id = $1
	`, eventID)
	if err := row.Scan(&n.EventID, &n.UserID, &n.BodyMD, &linksJSON, &n.UpdatedAt); err != nil {
		return domain.EventNote{}, notFound(err)
	}
	if err := unmarshalInto(linksJSON, &n.Links); err != nil {
		return domain.EventNote{}, err
	}
	if n.Links == nil {
		n.Links = []string{}
	}
	return n, nil
}

// ListByUser returns every note the user wrote, ordered by event id (the
// data export's event-notes.json).
func (r eventNoteRepo) ListByUser(ctx context.Context, userID string) ([]domain.EventNote, error) {
	rows, err := r.q(ctx).QueryContext(ctx, `
		SELECT event_id, user_id, body_md, links, updated_at
		FROM event_notes WHERE user_id = $1 ORDER BY event_id`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	notes := []domain.EventNote{}
	for rows.Next() {
		var n domain.EventNote
		var linksJSON []byte
		if err := rows.Scan(&n.EventID, &n.UserID, &n.BodyMD, &linksJSON, &n.UpdatedAt); err != nil {
			return nil, err
		}
		if err := unmarshalInto(linksJSON, &n.Links); err != nil {
			return nil, err
		}
		if n.Links == nil {
			n.Links = []string{}
		}
		notes = append(notes, n)
	}
	return notes, rows.Err()
}
