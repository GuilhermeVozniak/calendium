package postgres

import (
	"context"
	"database/sql"
	"time"

	"calendium/backend/internal/domain"
)

// --- port.DraftRepo ----------------------------------------------------------

const draftCols = `d.id, d.account_id, d.thread_id, d.to_addrs, d.cc_addrs, d.bcc_addrs,
	d.subject, d.body_html, d.scheduled_at, d.send_attempts, d.last_error, d.updated_at`

func scanDraft(r rowScanner) (domain.Draft, error) {
	var d domain.Draft
	var threadID sql.NullString
	var to, cc, bcc []byte
	var scheduled sql.NullTime
	var lastErr sql.NullString
	if err := r.Scan(&d.ID, &d.AccountID, &threadID, &to, &cc, &bcc,
		&d.Subject, &d.BodyHTML, &scheduled, &d.SendAttempts, &lastErr, &d.UpdatedAt); err != nil {
		return domain.Draft{}, notFound(err)
	}
	d.ThreadID = strPtr(threadID)
	d.ScheduledAt = timePtr(scheduled)
	d.LastError = strPtr(lastErr)
	for _, pair := range []struct {
		src []byte
		dst *[]domain.EmailAddress
	}{{to, &d.To}, {cc, &d.Cc}, {bcc, &d.Bcc}} {
		if err := unmarshalInto(pair.src, pair.dst); err != nil {
			return domain.Draft{}, err
		}
		if *pair.dst == nil {
			*pair.dst = []domain.EmailAddress{}
		}
	}
	return d, nil
}

func (r draftRepo) Create(ctx context.Context, d domain.Draft) (domain.Draft, error) {
	if d.ID == "" {
		d.ID = newID()
	}
	if d.UpdatedAt.IsZero() {
		d.UpdatedAt = time.Now().UTC()
	}
	to, cc, bcc, err := draftAddrs(d)
	if err != nil {
		return domain.Draft{}, err
	}
	_, err = r.q(ctx).ExecContext(ctx, `
		INSERT INTO drafts (id, account_id, thread_id, to_addrs, cc_addrs, bcc_addrs,
			subject, body_html, scheduled_at, updated_at)
		VALUES ($1, $2, $3, $4::jsonb, $5::jsonb, $6::jsonb, $7, $8, $9, $10)`,
		d.ID, d.AccountID, nullStrPtr(d.ThreadID), to, cc, bcc,
		d.Subject, d.BodyHTML, nullTimePtr(d.ScheduledAt), d.UpdatedAt)
	if err != nil {
		return domain.Draft{}, err
	}
	return d, nil
}

func (r draftRepo) GetByID(ctx context.Context, id string) (domain.Draft, error) {
	row := r.q(ctx).QueryRowContext(ctx,
		`SELECT `+draftCols+` FROM drafts d WHERE d.id = $1`, id)
	return scanDraft(row)
}

func (r draftRepo) ListByUser(ctx context.Context, userID string) ([]domain.Draft, error) {
	rows, err := r.q(ctx).QueryContext(ctx, `
		SELECT `+draftCols+`
		FROM drafts d
		JOIN connected_accounts ca ON ca.id = d.account_id
		WHERE ca.user_id = $1
		ORDER BY d.updated_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectDrafts(rows)
}

func (r draftRepo) Update(ctx context.Context, d domain.Draft) error {
	to, cc, bcc, err := draftAddrs(d)
	if err != nil {
		return err
	}
	// A user edit or reschedule resets the worker's retry budget: send_attempts
	// back to 0 and any prior delivery error cleared.
	return mustAffect(r.q(ctx).ExecContext(ctx, `
		UPDATE drafts SET
			thread_id     = $2,
			to_addrs      = $3::jsonb,
			cc_addrs      = $4::jsonb,
			bcc_addrs     = $5::jsonb,
			subject       = $6,
			body_html     = $7,
			scheduled_at  = $8,
			send_attempts = 0,
			last_error    = NULL,
			updated_at    = $9
		WHERE id = $1`,
		d.ID, nullStrPtr(d.ThreadID), to, cc, bcc, d.Subject, d.BodyHTML,
		nullTimePtr(d.ScheduledAt), d.UpdatedAt))
}

func (r draftRepo) Delete(ctx context.Context, id string) error {
	return mustAffect(r.q(ctx).ExecContext(ctx, `DELETE FROM drafts WHERE id = $1`, id))
}

func (r draftRepo) ListScheduledDue(ctx context.Context, now time.Time, limit int) ([]domain.Draft, error) {
	rows, err := r.q(ctx).QueryContext(ctx, `
		SELECT `+draftCols+` FROM drafts d
		WHERE d.scheduled_at IS NOT NULL AND d.scheduled_at <= $1
		ORDER BY d.scheduled_at, d.id LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectDrafts(rows)
}

func (r draftRepo) ClaimScheduled(ctx context.Context, id string) (bool, error) {
	res, err := r.q(ctx).ExecContext(ctx, `
		UPDATE drafts SET scheduled_at = NULL, updated_at = now()
		WHERE id = $1 AND scheduled_at IS NOT NULL`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func (r draftRepo) RecordSendFailure(ctx context.Context, id string, nextAttemptAt *time.Time, errMsg string) error {
	return mustAffect(r.q(ctx).ExecContext(ctx, `
		UPDATE drafts SET
			send_attempts = send_attempts + 1,
			last_error    = $2,
			scheduled_at  = $3,
			updated_at    = now()
		WHERE id = $1`,
		id, errMsg, nullTimePtr(nextAttemptAt)))
}

func collectDrafts(rows *sql.Rows) ([]domain.Draft, error) {
	drafts := []domain.Draft{}
	for rows.Next() {
		d, err := scanDraft(rows)
		if err != nil {
			return nil, err
		}
		drafts = append(drafts, d)
	}
	return drafts, rows.Err()
}

func draftAddrs(d domain.Draft) (to, cc, bcc string, err error) {
	if to, err = jsonArray(d.To); err != nil {
		return
	}
	if cc, err = jsonArray(d.Cc); err != nil {
		return
	}
	bcc, err = jsonArray(d.Bcc)
	return
}

// --- port.SnippetRepo --------------------------------------------------------

const snippetCols = `id, user_id, name, shortcut, body_html, usage_count`

func scanSnippet(r rowScanner) (domain.Snippet, error) {
	var s domain.Snippet
	var shortcut sql.NullString
	if err := r.Scan(&s.ID, &s.UserID, &s.Name, &shortcut, &s.BodyHTML, &s.UsageCount); err != nil {
		return domain.Snippet{}, notFound(err)
	}
	s.Shortcut = strPtr(shortcut)
	return s, nil
}

func (r snippetRepo) Create(ctx context.Context, s domain.Snippet) (domain.Snippet, error) {
	if s.ID == "" {
		s.ID = newID()
	}
	_, err := r.q(ctx).ExecContext(ctx, `
		INSERT INTO snippets (id, user_id, name, shortcut, body_html, usage_count)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		s.ID, s.UserID, s.Name, nullStrPtr(s.Shortcut), s.BodyHTML, s.UsageCount)
	if err != nil {
		return domain.Snippet{}, err
	}
	return s, nil
}

func (r snippetRepo) GetByID(ctx context.Context, id string) (domain.Snippet, error) {
	row := r.q(ctx).QueryRowContext(ctx,
		`SELECT `+snippetCols+` FROM snippets WHERE id = $1`, id)
	return scanSnippet(row)
}

func (r snippetRepo) ListByUser(ctx context.Context, userID string) ([]domain.Snippet, error) {
	rows, err := r.q(ctx).QueryContext(ctx,
		`SELECT `+snippetCols+` FROM snippets WHERE user_id = $1 ORDER BY name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	snippets := []domain.Snippet{}
	for rows.Next() {
		s, err := scanSnippet(rows)
		if err != nil {
			return nil, err
		}
		snippets = append(snippets, s)
	}
	return snippets, rows.Err()
}

func (r snippetRepo) Update(ctx context.Context, s domain.Snippet) error {
	return mustAffect(r.q(ctx).ExecContext(ctx, `
		UPDATE snippets SET
			name        = $2,
			shortcut    = $3,
			body_html   = $4,
			usage_count = $5,
			updated_at  = now()
		WHERE id = $1`,
		s.ID, s.Name, nullStrPtr(s.Shortcut), s.BodyHTML, s.UsageCount))
}

func (r snippetRepo) Delete(ctx context.Context, id string) error {
	return mustAffect(r.q(ctx).ExecContext(ctx, `DELETE FROM snippets WHERE id = $1`, id))
}
