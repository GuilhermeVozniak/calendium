package postgres

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- port.ThreadRepo ---------------------------------------------------------

const threadCols = `t.id, t.account_id, t.provider_thread_id, t.subject, t.snippet,
	t.participants, t.split, t.message_count, t.unread, t.starred, t.in_inbox, t.last_message_at,
	t.opened_at, t.snoozed_until, t.remind_at, t.unsubscribe_mailto, t.unsubscribe_url, t.unsubscribe_one_click,
	t.summary, t.instant_replies, t.instant_replies_updated_at,
	coalesce((SELECT json_agg(tl.label_id ORDER BY tl.label_id)
	          FROM thread_labels tl WHERE tl.thread_id = t.id), '[]'::json)::text`

func scanThread(r rowScanner) (domain.Thread, error) {
	var t domain.Thread
	var participants, instantReplies []byte
	var labels string
	var opened, snoozed, remind, instantRepliesUpdatedAt sql.NullTime
	var unsubMailto, unsubURL sql.NullString
	if err := r.Scan(&t.ID, &t.AccountID, &t.ProviderThreadID, &t.Subject, &t.Snippet,
		&participants, &t.Split, &t.MessageCount, &t.Unread, &t.Starred, &t.InInbox, &t.LastMessageAt,
		&opened, &snoozed, &remind, &unsubMailto, &unsubURL, &t.UnsubscribeOneClick,
		&t.Summary, &instantReplies, &instantRepliesUpdatedAt, &labels); err != nil {
		return domain.Thread{}, notFound(err)
	}
	if err := unmarshalInto(participants, &t.Participants); err != nil {
		return domain.Thread{}, err
	}
	if err := unmarshalInto(instantReplies, &t.InstantReplies); err != nil {
		return domain.Thread{}, err
	}
	if err := unmarshalInto([]byte(labels), &t.LabelIDs); err != nil {
		return domain.Thread{}, err
	}
	if t.Participants == nil {
		t.Participants = []domain.EmailAddress{}
	}
	if t.InstantReplies == nil {
		t.InstantReplies = []string{}
	}
	if t.LabelIDs == nil {
		t.LabelIDs = []string{}
	}
	t.OpenedAt = timePtr(opened)
	t.SnoozedUntil = timePtr(snoozed)
	t.RemindAt = timePtr(remind)
	t.InstantRepliesUpdatedAt = timePtr(instantRepliesUpdatedAt)
	if unsubMailto.Valid {
		t.UnsubscribeMailto = &unsubMailto.String
	}
	if unsubURL.Valid {
		t.UnsubscribeURL = &unsubURL.String
	}
	return t, nil
}

func (r threadRepo) Upsert(ctx context.Context, t domain.Thread) (domain.Thread, error) {
	if t.ID == "" {
		t.ID = newID()
	}
	if t.Split == "" {
		t.Split = domain.SplitOther
	}
	if t.LastMessageAt.IsZero() {
		t.LastMessageAt = time.Now().UTC()
	}
	participants, err := jsonArray(t.Participants)
	if err != nil {
		return domain.Thread{}, err
	}
	// Threads mirrored from a provider converge on the provider identity;
	// purely local rows (no provider id) converge on their primary key.
	conflict := "(account_id, provider_thread_id)"
	if t.ProviderThreadID == "" {
		conflict = "(id)"
	}
	err = r.q(ctx).QueryRowContext(ctx, `
		INSERT INTO threads (id, account_id, provider_thread_id, subject, snippet, participants,
			split, message_count, unread, starred, in_inbox, last_message_at, opened_at, snoozed_until, remind_at,
			unsubscribe_mailto, unsubscribe_url, unsubscribe_one_click)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		ON CONFLICT `+conflict+` DO UPDATE SET
			subject         = EXCLUDED.subject,
			snippet         = EXCLUDED.snippet,
			participants    = EXCLUDED.participants,
			split           = EXCLUDED.split,
			message_count   = EXCLUDED.message_count,
			unread          = EXCLUDED.unread,
			starred         = EXCLUDED.starred,
			in_inbox        = EXCLUDED.in_inbox,
			last_message_at = EXCLUDED.last_message_at,
			opened_at       = COALESCE(threads.opened_at, EXCLUDED.opened_at),
			snoozed_until   = EXCLUDED.snoozed_until,
			remind_at       = EXCLUDED.remind_at,
			unsubscribe_mailto = EXCLUDED.unsubscribe_mailto,
			unsubscribe_url = EXCLUDED.unsubscribe_url,
			unsubscribe_one_click = EXCLUDED.unsubscribe_one_click,
			updated_at      = now()
		RETURNING id`,
		t.ID, t.AccountID, t.ProviderThreadID, t.Subject, t.Snippet, participants,
		string(t.Split), t.MessageCount, t.Unread, t.Starred, t.InInbox, t.LastMessageAt,
		nullTimePtr(t.OpenedAt), nullTimePtr(t.SnoozedUntil), nullTimePtr(t.RemindAt),
		t.UnsubscribeMailto, t.UnsubscribeURL, t.UnsubscribeOneClick).Scan(&t.ID)
	if err != nil {
		return domain.Thread{}, err
	}
	if t.Participants == nil {
		t.Participants = []domain.EmailAddress{}
	}
	if t.InstantReplies == nil {
		t.InstantReplies = []string{}
	}
	if t.LabelIDs == nil {
		t.LabelIDs = []string{}
	}
	return t, nil
}

func (r threadRepo) GetByID(ctx context.Context, id string) (domain.Thread, error) {
	row := r.q(ctx).QueryRowContext(ctx,
		`SELECT `+threadCols+` FROM threads t WHERE t.id = $1`, id)
	return scanThread(row)
}

func (r threadRepo) GetByProviderID(ctx context.Context, accountID, providerThreadID string) (domain.Thread, error) {
	row := r.q(ctx).QueryRowContext(ctx,
		`SELECT `+threadCols+` FROM threads t WHERE t.account_id = $1 AND t.provider_thread_id = $2`,
		accountID, providerThreadID)
	return scanThread(row)
}

func (r threadRepo) List(ctx context.Context, q port.ThreadQuery) (domain.Page[domain.Thread], error) {
	var page domain.Page[domain.Thread]
	if q.UserID == "" {
		return page, fmt.Errorf("%w: thread query requires a user id", domain.ErrValidation)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}

	where := []string{"ca.user_id = $1"}
	args := []any{q.UserID}
	if q.AccountID != "" {
		args = append(args, q.AccountID)
		where = append(where, fmt.Sprintf("t.account_id = $%d", len(args)))
	}
	if q.Split != "" {
		args = append(args, string(q.Split))
		where = append(where, fmt.Sprintf("t.split = $%d", len(args)))
	}
	if q.LabelID != "" {
		args = append(args, q.LabelID)
		where = append(where, fmt.Sprintf(
			"EXISTS (SELECT 1 FROM thread_labels tl WHERE tl.thread_id = t.id AND tl.label_id = $%d)", len(args)))
	}
	if q.Query != "" {
		args = append(args, q.Query)
		n := len(args)
		where = append(where, fmt.Sprintf(`(
			to_tsvector('simple', coalesce(t.subject, '') || ' ' || coalesce(t.snippet, ''))
				@@ plainto_tsquery('simple', $%d)
			OR t.subject ILIKE '%%' || $%d || '%%')`, n, n))
	}
	// Snooze handling: the snoozed pseudo-view shows only threads still
	// snoozed into the future; every other listing hides snoozed threads
	// unless IncludeSnoozed is set.
	if q.View == domain.ThreadViewSnoozed {
		where = append(where, "t.snoozed_until IS NOT NULL AND t.snoozed_until > now()")
	} else if !q.IncludeSnoozed {
		where = append(where, "t.snoozed_until IS NULL")
	}
	// Cross-split pseudo-views (starred/sent) span archived threads; the
	// default listing is inbox-only so triaged threads stop reappearing.
	switch q.View {
	case domain.ThreadViewStarred:
		where = append(where, "t.starred = true")
	case domain.ThreadViewSent:
		where = append(where,
			"EXISTS (SELECT 1 FROM messages m WHERE m.thread_id = t.id AND lower(m.from_addr->>'email') = lower(ca.email))")
	case "":
		where = append(where, "t.in_inbox = true")
	}
	if q.Cursor != "" {
		ts, id, err := decodeThreadCursor(q.Cursor)
		if err != nil {
			return page, err
		}
		args = append(args, ts, id)
		where = append(where, fmt.Sprintf("(t.last_message_at, t.id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	args = append(args, limit+1)

	query := `SELECT ` + threadCols + `
		FROM threads t
		JOIN connected_accounts ca ON ca.id = t.account_id
		WHERE ` + strings.Join(where, " AND ") +
		fmt.Sprintf(" ORDER BY t.last_message_at DESC, t.id DESC LIMIT $%d", len(args))

	rows, err := r.q(ctx).QueryContext(ctx, query, args...)
	if err != nil {
		return page, err
	}
	defer func() { _ = rows.Close() }()

	items := []domain.Thread{}
	for rows.Next() {
		t, err := scanThread(rows)
		if err != nil {
			return page, err
		}
		items = append(items, t)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(items) > limit {
		items = items[:limit]
		cursor := encodeThreadCursor(items[limit-1])
		page.NextCursor = &cursor
	}
	page.Items = items
	return page, nil
}

func (r threadRepo) Update(ctx context.Context, t domain.Thread) error {
	participants, err := jsonArray(t.Participants)
	if err != nil {
		return err
	}
	instantReplies, err := jsonArray(t.InstantReplies)
	if err != nil {
		return err
	}
	return mustAffect(r.q(ctx).ExecContext(ctx, `
		UPDATE threads SET
			subject         = $2,
			snippet         = $3,
			participants    = $4::jsonb,
			split           = $5,
			message_count   = $6,
			unread          = $7,
			starred         = $8,
			in_inbox        = $9,
			last_message_at = $10,
			opened_at       = $11,
			snoozed_until   = $12,
			remind_at       = $13,
			unsubscribe_mailto = $14,
			unsubscribe_url = $15,
			unsubscribe_one_click = $16,
			summary         = $17,
			instant_replies = $18::jsonb,
			updated_at      = now()
		WHERE id = $1`,
		t.ID, t.Subject, t.Snippet, participants, string(t.Split), t.MessageCount,
		t.Unread, t.Starred, t.InInbox, t.LastMessageAt,
		nullTimePtr(t.OpenedAt), nullTimePtr(t.SnoozedUntil), nullTimePtr(t.RemindAt),
		t.UnsubscribeMailto, t.UnsubscribeURL, t.UnsubscribeOneClick, t.Summary, instantReplies))
}

// SetSummary is a targeted write for the Auto Summarize AI job: it never
// touches columns a concurrent user mutation may have changed.
func (r threadRepo) SetSummary(ctx context.Context, threadID, summary string, at time.Time) error {
	return mustAffect(r.q(ctx).ExecContext(ctx, `
		UPDATE threads SET summary = $2, summary_updated_at = $3, updated_at = now()
		WHERE id = $1`, threadID, summary, at))
}

// SetInstantReplies is a targeted write for the Instant Reply AI job: it
// never touches columns a concurrent user mutation may have changed.
func (r threadRepo) SetInstantReplies(ctx context.Context, threadID string, replies []string, at time.Time) error {
	if replies == nil {
		replies = []string{}
	}
	encoded, err := jsonArray(replies)
	if err != nil {
		return err
	}
	return mustAffect(r.q(ctx).ExecContext(ctx, `
		UPDATE threads SET instant_replies = $2::jsonb, instant_replies_updated_at = $3, updated_at = now()
		WHERE id = $1`, threadID, encoded, at))
}

// SetReminderIfUnset arms remind_at only when it is currently null, so an AI
// auto-reminder never overwrites a user-chosen reminder. Silently no-ops
// (no error) when the thread is missing or the reminder is already set.
func (r threadRepo) SetReminderIfUnset(ctx context.Context, threadID string, remindAt time.Time) error {
	_, err := r.q(ctx).ExecContext(ctx, `
		UPDATE threads SET remind_at = $2, updated_at = now()
		WHERE id = $1 AND remind_at IS NULL`, threadID, remindAt)
	return err
}

// MarkOpened records the first open of a thread with a targeted write: it
// sets opened_at (only when still null) and clears unread, idempotently,
// without touching columns a concurrent user mutation may have changed.
func (r threadRepo) MarkOpened(ctx context.Context, id string) error {
	return mustAffect(r.q(ctx).ExecContext(ctx, `
		UPDATE threads SET opened_at = COALESCE(opened_at, now()), unread = false, updated_at = now()
		WHERE id = $1`, id))
}

func (r threadRepo) SetLabels(ctx context.Context, threadID string, labelIDs []string) error {
	if _, err := r.q(ctx).ExecContext(ctx,
		`DELETE FROM thread_labels WHERE thread_id = $1`, threadID); err != nil {
		return err
	}
	if len(labelIDs) == 0 {
		return nil
	}
	ids, err := jsonArray(labelIDs)
	if err != nil {
		return err
	}
	_, err = r.q(ctx).ExecContext(ctx, `
		INSERT INTO thread_labels (thread_id, label_id)
		SELECT $1, x FROM jsonb_array_elements_text($2::jsonb) x
		ON CONFLICT DO NOTHING`, threadID, ids)
	return err
}

func (r threadRepo) Search(ctx context.Context, userID, query string, limit int) ([]domain.Thread, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.q(ctx).QueryContext(ctx, `
		SELECT `+threadCols+`
		FROM threads t
		JOIN connected_accounts ca ON ca.id = t.account_id
		WHERE ca.user_id = $1 AND (
			to_tsvector('simple', coalesce(t.subject, '') || ' ' || coalesce(t.snippet, ''))
				@@ plainto_tsquery('simple', $2)
			OR t.subject ILIKE '%' || $2 || '%')
		ORDER BY t.last_message_at DESC
		LIMIT $3`, userID, query, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return collectThreads(rows)
}

func (r threadRepo) ListSnoozeDue(ctx context.Context, now time.Time, limit int) ([]domain.Thread, error) {
	rows, err := r.q(ctx).QueryContext(ctx, `
		SELECT `+threadCols+` FROM threads t
		WHERE t.snoozed_until IS NOT NULL AND t.snoozed_until <= $1
		ORDER BY t.snoozed_until, t.id LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return collectThreads(rows)
}

func (r threadRepo) ListRemindersDue(ctx context.Context, now time.Time, limit int) ([]domain.Thread, error) {
	rows, err := r.q(ctx).QueryContext(ctx, `
		SELECT `+threadCols+` FROM threads t
		WHERE t.remind_at IS NOT NULL AND t.remind_at <= $1
		ORDER BY t.remind_at, t.id LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return collectThreads(rows)
}

// ClearSnooze wakes a snoozed thread with a targeted write: it clears
// snoozed_until and marks the thread unread so it resurfaces, without
// touching columns a concurrent user mutation may have changed.
func (r threadRepo) ClearSnooze(ctx context.Context, id string) error {
	return mustAffect(r.q(ctx).ExecContext(ctx, `
		UPDATE threads SET snoozed_until = NULL, unread = true, updated_at = now()
		WHERE id = $1`, id))
}

// ClearReminder fires a follow-up reminder with a targeted write: it clears
// remind_at and marks the thread unread.
func (r threadRepo) ClearReminder(ctx context.Context, id string) error {
	return mustAffect(r.q(ctx).ExecContext(ctx, `
		UPDATE threads SET remind_at = NULL, unread = true, updated_at = now()
		WHERE id = $1`, id))
}

// AppendSentMessage records a newly delivered message on a thread: it bumps
// message_count and advances last_message_at atomically in the database,
// avoiding the lost update a read-modify-write of the whole row would risk.
func (r threadRepo) AppendSentMessage(ctx context.Context, id string, sentAt time.Time) error {
	return mustAffect(r.q(ctx).ExecContext(ctx, `
		UPDATE threads SET
			message_count   = message_count + 1,
			last_message_at = GREATEST(last_message_at, $2),
			updated_at      = now()
		WHERE id = $1`, id, sentAt))
}

func (r threadRepo) ListInboxBefore(ctx context.Context, userID string, before time.Time, limit int) ([]domain.Thread, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.q(ctx).QueryContext(ctx, `
		SELECT `+threadCols+`
		FROM threads t
		JOIN connected_accounts ca ON ca.id = t.account_id
		WHERE ca.user_id = $1 AND t.in_inbox AND t.snoozed_until IS NULL
		  AND t.last_message_at < $2
		ORDER BY t.last_message_at ASC, t.id LIMIT $3`, userID, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return collectThreads(rows)
}

func collectThreads(rows *sql.Rows) ([]domain.Thread, error) {
	threads := []domain.Thread{}
	for rows.Next() {
		t, err := scanThread(rows)
		if err != nil {
			return nil, err
		}
		threads = append(threads, t)
	}
	return threads, rows.Err()
}

// --- port.MessageRepo --------------------------------------------------------

const messageCols = `m.id, m.thread_id, m.account_id, m.provider_message_id, m.from_addr,
	m.to_addrs, m.cc_addrs, m.bcc_addrs, m.subject, m.body_html, m.body_text,
	m.sent_at, m.is_draft, m.opened_at, m.rfc_message_id`

func scanMessage(r rowScanner) (domain.Message, error) {
	var m domain.Message
	var from, to, cc, bcc []byte
	var opened sql.NullTime
	var rfcID sql.NullString
	if err := r.Scan(&m.ID, &m.ThreadID, &m.AccountID, &m.ProviderMessageID, &from,
		&to, &cc, &bcc, &m.Subject, &m.BodyHTML, &m.BodyText,
		&m.SentAt, &m.IsDraft, &opened, &rfcID); err != nil {
		return domain.Message{}, notFound(err)
	}
	m.RFCMessageID = rfcID.String
	for _, pair := range []struct {
		src []byte
		dst any
	}{{from, &m.From}, {to, &m.To}, {cc, &m.Cc}, {bcc, &m.Bcc}} {
		if err := unmarshalInto(pair.src, pair.dst); err != nil {
			return domain.Message{}, err
		}
	}
	if m.To == nil {
		m.To = []domain.EmailAddress{}
	}
	if m.Cc == nil {
		m.Cc = []domain.EmailAddress{}
	}
	if m.Bcc == nil {
		m.Bcc = []domain.EmailAddress{}
	}
	m.Attachments = []domain.Attachment{}
	m.OpenedAt = timePtr(opened)
	return m, nil
}

func (r messageRepo) Upsert(ctx context.Context, m domain.Message) (domain.Message, error) {
	if m.ID == "" {
		m.ID = newID()
	}
	from, err := jsonArray(m.From)
	if err != nil {
		return domain.Message{}, err
	}
	to, err := jsonArray(m.To)
	if err != nil {
		return domain.Message{}, err
	}
	cc, err := jsonArray(m.Cc)
	if err != nil {
		return domain.Message{}, err
	}
	bcc, err := jsonArray(m.Bcc)
	if err != nil {
		return domain.Message{}, err
	}
	conflict := "(account_id, provider_message_id)"
	if m.ProviderMessageID == "" {
		conflict = "(id)"
	}
	err = r.q(ctx).QueryRowContext(ctx, `
		INSERT INTO messages (id, thread_id, account_id, provider_message_id, from_addr,
			to_addrs, cc_addrs, bcc_addrs, subject, body_html, body_text, sent_at, is_draft, opened_at, rfc_message_id)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, $7::jsonb, $8::jsonb, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT `+conflict+` DO UPDATE SET
			thread_id = EXCLUDED.thread_id,
			from_addr = EXCLUDED.from_addr,
			to_addrs  = EXCLUDED.to_addrs,
			cc_addrs  = EXCLUDED.cc_addrs,
			bcc_addrs = EXCLUDED.bcc_addrs,
			subject   = EXCLUDED.subject,
			body_html = EXCLUDED.body_html,
			body_text = EXCLUDED.body_text,
			sent_at   = EXCLUDED.sent_at,
			is_draft  = EXCLUDED.is_draft,
			opened_at = COALESCE(EXCLUDED.opened_at, messages.opened_at),
			rfc_message_id = COALESCE(EXCLUDED.rfc_message_id, messages.rfc_message_id)
		RETURNING id`,
		m.ID, m.ThreadID, m.AccountID, m.ProviderMessageID, from, to, cc, bcc,
		m.Subject, m.BodyHTML, m.BodyText, m.SentAt, m.IsDraft, nullTimePtr(m.OpenedAt), nullStr(m.RFCMessageID)).Scan(&m.ID)
	if err != nil {
		return domain.Message{}, err
	}
	if err := r.replaceAttachments(ctx, m.ID, m.Attachments); err != nil {
		return domain.Message{}, err
	}
	if m.Attachments == nil {
		m.Attachments = []domain.Attachment{}
	}
	return m, nil
}

func (r messageRepo) replaceAttachments(ctx context.Context, messageID string, atts []domain.Attachment) error {
	if _, err := r.q(ctx).ExecContext(ctx,
		`DELETE FROM attachments WHERE message_id = $1`, messageID); err != nil {
		return err
	}
	for _, a := range atts {
		if a.ID == "" {
			a.ID = newID()
		}
		if _, err := r.q(ctx).ExecContext(ctx, `
			INSERT INTO attachments (id, message_id, filename, mime_type, size_bytes, provider_attachment_id)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			a.ID, messageID, a.Filename, a.MimeType, a.SizeBytes, a.ProviderAttachmentID); err != nil {
			return err
		}
	}
	return nil
}

func (r messageRepo) GetByID(ctx context.Context, id string) (domain.Message, error) {
	row := r.q(ctx).QueryRowContext(ctx,
		`SELECT `+messageCols+` FROM messages m WHERE m.id = $1`, id)
	m, err := scanMessage(row)
	if err != nil {
		return domain.Message{}, err
	}
	return r.withAttachments(ctx, m)
}

func (r messageRepo) GetByProviderID(ctx context.Context, accountID, providerMessageID string) (domain.Message, error) {
	row := r.q(ctx).QueryRowContext(ctx,
		`SELECT `+messageCols+` FROM messages m WHERE m.account_id = $1 AND m.provider_message_id = $2`,
		accountID, providerMessageID)
	m, err := scanMessage(row)
	if err != nil {
		return domain.Message{}, err
	}
	return r.withAttachments(ctx, m)
}

func (r messageRepo) withAttachments(ctx context.Context, m domain.Message) (domain.Message, error) {
	byMsg, err := r.attachmentsFor(ctx, []string{m.ID})
	if err != nil {
		return domain.Message{}, err
	}
	if atts, ok := byMsg[m.ID]; ok {
		m.Attachments = atts
	}
	return m, nil
}

func (r messageRepo) ListByThread(ctx context.Context, threadID string) ([]domain.Message, error) {
	rows, err := r.q(ctx).QueryContext(ctx,
		`SELECT `+messageCols+` FROM messages m WHERE m.thread_id = $1 ORDER BY m.sent_at, m.id`,
		threadID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	msgs := []domain.Message{}
	ids := []string{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
		ids = append(ids, m.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return msgs, nil
	}
	byMsg, err := r.attachmentsFor(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range msgs {
		if atts, ok := byMsg[msgs[i].ID]; ok {
			msgs[i].Attachments = atts
		}
	}
	return msgs, nil
}

// ListSentByAccount returns the newest messages sent from the account's own
// address (from_addr email match), newest first — the voice-learning corpus.
func (r messageRepo) ListSentByAccount(ctx context.Context, accountID, accountEmail string, limit int) ([]domain.Message, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.q(ctx).QueryContext(ctx, `
		SELECT `+messageCols+` FROM messages m
		WHERE m.account_id = $1 AND lower(m.from_addr->>'email') = lower($2)
		ORDER BY m.sent_at DESC, m.id DESC LIMIT $3`, accountID, accountEmail, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	msgs := []domain.Message{}
	ids := []string{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
		ids = append(ids, m.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return msgs, nil
	}
	byMsg, err := r.attachmentsFor(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range msgs {
		if atts, ok := byMsg[msgs[i].ID]; ok {
			msgs[i].Attachments = atts
		}
	}
	return msgs, nil
}

// --- M2.5 Compose & Contact --------------------------------------------------
//
// The methods below satisfy the widened port.MessageRepo interface so the
// backend compiles once ports/domain land (this task). Real SQL — Recent
// Opens keyset scan, open-hour histogram, attachment trigram search, and
// contact aggregation — is implemented in later M2.5 tasks (3, 4, 5 in
// docs/superpowers/plans/2026-07-17-m2-5-compose-contact.md).

// ListOpens returns opened sent messages (from the account's own address),
// newest open first, keyset-paginated on (opened_at, id) to stay stable
// under exact-timestamp ties.
func (r messageRepo) ListOpens(ctx context.Context, q port.OpensQuery) (domain.Page[domain.OpenEvent], error) {
	var page domain.Page[domain.OpenEvent]
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	args := []any{q.UserID}
	cursorPred := ""
	if q.Cursor != "" {
		at, id, err := decodeOpensCursor(q.Cursor)
		if err != nil {
			return page, err
		}
		args = append(args, at, id)
		cursorPred = "AND (m.opened_at, m.id) < ($2, $3)"
	}
	args = append(args, limit+1)
	rows, err := r.q(ctx).QueryContext(ctx, fmt.Sprintf(`
		SELECT m.id, m.thread_id, m.account_id, m.subject, m.to_addrs,
		       m.opened_at, m.sent_at
		FROM messages m
		JOIN connected_accounts ca ON ca.id = m.account_id
		WHERE ca.user_id = $1
		  AND m.opened_at IS NOT NULL
		  AND lower(m.from_addr->>'email') = lower(ca.email)
		  %s
		ORDER BY m.opened_at DESC, m.id DESC
		LIMIT $%d`, cursorPred, len(args)), args...)
	if err != nil {
		return page, err
	}
	defer func() { _ = rows.Close() }()

	items := []domain.OpenEvent{}
	for rows.Next() {
		var e domain.OpenEvent
		var recipients []byte
		if err := rows.Scan(&e.MessageID, &e.ThreadID, &e.AccountID,
			&e.Subject, &recipients, &e.OpenedAt, &e.SentAt); err != nil {
			return page, err
		}
		if err := unmarshalInto(recipients, &e.Recipients); err != nil {
			return page, err
		}
		if e.Recipients == nil {
			e.Recipients = []domain.EmailAddress{}
		}
		items = append(items, e)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(items) > limit {
		items = items[:limit]
		last := items[limit-1]
		cursor := encodeOpensCursor(last.OpenedAt, last.MessageID)
		page.NextCursor = &cursor
	}
	page.Items = items
	return page, nil
}

func (r messageRepo) OpenHourHistogram(ctx context.Context, userID, recipientEmail string) ([24]int, error) {
	var hist [24]int
	rows, err := r.q(ctx).QueryContext(ctx, `
		SELECT extract(hour FROM m.opened_at AT TIME ZONE 'UTC')::int AS h, count(*)
		FROM messages m
		JOIN connected_accounts ca ON ca.id = m.account_id
		WHERE ca.user_id = $1
		  AND m.opened_at IS NOT NULL
		  AND lower(m.from_addr->>'email') = lower(ca.email)
		  AND EXISTS (
			SELECT 1 FROM jsonb_array_elements(m.to_addrs) rcpt
			WHERE lower(rcpt->>'email') = lower($2))
		GROUP BY h`, userID, recipientEmail)
	if err != nil {
		return hist, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var h, c int
		if err := rows.Scan(&h, &c); err != nil {
			return hist, err
		}
		if h >= 0 && h < 24 {
			hist[h] = c
		}
	}
	return hist, rows.Err()
}

// encodeAttachmentCursor encodes the keyset position after (sentAt, attachmentID).
func encodeAttachmentCursor(sentAt time.Time, attachmentID string) string {
	raw := strconv.FormatInt(sentAt.UnixMicro(), 10) + ":" + attachmentID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeAttachmentCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: malformed cursor", domain.ErrValidation)
	}
	micros, id, ok := strings.Cut(string(raw), ":")
	if !ok {
		return time.Time{}, "", fmt.Errorf("%w: malformed cursor", domain.ErrValidation)
	}
	n, err := strconv.ParseInt(micros, 10, 64)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: malformed cursor", domain.ErrValidation)
	}
	return time.UnixMicro(n).UTC(), id, nil
}

func (r messageRepo) SearchAttachments(ctx context.Context, q port.AttachmentQuery) (domain.Page[domain.AttachmentHit], error) {
	var page domain.Page[domain.AttachmentHit]
	if q.UserID == "" {
		return page, fmt.Errorf("%w: attachment query requires a user id", domain.ErrValidation)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}

	where := []string{"ca.user_id = $1"}
	args := []any{q.UserID}
	if q.Query != "" {
		args = append(args, q.Query)
		where = append(where, fmt.Sprintf("a.filename ILIKE '%%' || $%d || '%%'", len(args)))
	}
	if q.ThreadID != "" {
		args = append(args, q.ThreadID)
		where = append(where, fmt.Sprintf("t.id = $%d", len(args)))
	}
	if q.Contact != "" {
		args = append(args, q.Contact)
		n := len(args)
		where = append(where, fmt.Sprintf(`(
			lower(m.from_addr->>'email') = lower($%d)
			OR EXISTS (SELECT 1 FROM jsonb_array_elements(m.to_addrs) rcpt WHERE lower(rcpt->>'email') = lower($%d))
			OR EXISTS (SELECT 1 FROM jsonb_array_elements(m.cc_addrs) rcpt WHERE lower(rcpt->>'email') = lower($%d)))`,
			n, n, n))
	}
	if q.Cursor != "" {
		ts, id, err := decodeAttachmentCursor(q.Cursor)
		if err != nil {
			return page, err
		}
		args = append(args, ts, id)
		where = append(where, fmt.Sprintf("(m.sent_at, a.id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	args = append(args, limit+1)

	query := `
		SELECT a.id, a.filename, a.mime_type, a.size_bytes, a.provider_attachment_id,
		       m.id, m.thread_id, t.subject, m.from_addr, m.sent_at
		FROM attachments a
		JOIN messages m ON m.id = a.message_id
		JOIN threads t ON t.id = m.thread_id
		JOIN connected_accounts ca ON ca.id = m.account_id
		WHERE ` + strings.Join(where, " AND ") +
		fmt.Sprintf(" ORDER BY m.sent_at DESC, a.id DESC LIMIT $%d", len(args))

	rows, err := r.q(ctx).QueryContext(ctx, query, args...)
	if err != nil {
		return page, err
	}
	defer func() { _ = rows.Close() }()

	items := []domain.AttachmentHit{}
	for rows.Next() {
		var hit domain.AttachmentHit
		var from []byte
		if err := rows.Scan(&hit.ID, &hit.Filename, &hit.MimeType, &hit.SizeBytes, &hit.ProviderAttachmentID,
			&hit.MessageID, &hit.ThreadID, &hit.ThreadSubject, &from, &hit.SentAt); err != nil {
			return page, err
		}
		if err := unmarshalInto(from, &hit.From); err != nil {
			return page, err
		}
		items = append(items, hit)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(items) > limit {
		items = items[:limit]
		last := items[limit-1]
		cursor := encodeAttachmentCursor(last.SentAt, last.ID)
		page.NextCursor = &cursor
	}
	page.Items = items
	return page, nil
}

func (r messageRepo) GetAttachment(ctx context.Context, attachmentID string) (domain.Attachment, string, error) {
	var a domain.Attachment
	var messageID string
	err := r.q(ctx).QueryRowContext(ctx, `
		SELECT id, message_id, filename, mime_type, size_bytes, provider_attachment_id
		FROM attachments WHERE id = $1`, attachmentID).
		Scan(&a.ID, &messageID, &a.Filename, &a.MimeType, &a.SizeBytes, &a.ProviderAttachmentID)
	if err != nil {
		return domain.Attachment{}, "", notFound(err)
	}
	return a, messageID, nil
}

func (r messageRepo) ContactSummary(ctx context.Context, userID, email string) (domain.ContactSummary, error) {
	c := domain.ContactSummary{Email: strings.ToLower(email), RecentThreads: []domain.Thread{}}
	if i := strings.LastIndex(c.Email, "@"); i >= 0 {
		c.Domain = c.Email[i+1:]
	}

	var name sql.NullString
	var lastMessageAt sql.NullTime
	err := r.q(ctx).QueryRowContext(ctx, `
		SELECT count(*),
		       max(m.sent_at),
		       (SELECT m2.from_addr->>'name'
		        FROM messages m2
		        JOIN connected_accounts ca2 ON ca2.id = m2.account_id
		        WHERE ca2.user_id = $1
		          AND lower(m2.from_addr->>'email') = lower($2)
		        ORDER BY m2.sent_at DESC LIMIT 1)
		FROM messages m
		JOIN connected_accounts ca ON ca.id = m.account_id
		WHERE ca.user_id = $1
		  AND (lower(m.from_addr->>'email') = lower($2)
		       OR EXISTS (SELECT 1 FROM jsonb_array_elements(m.to_addrs) rcpt
		                  WHERE lower(rcpt->>'email') = lower($2)))`,
		userID, email).Scan(&c.MessageCount, &lastMessageAt, &name)
	if err != nil {
		return c, err
	}
	if name.Valid {
		c.Name = &name.String
	}
	if lastMessageAt.Valid {
		t := lastMessageAt.Time
		c.LastMessageAt = &t
	}

	if err := r.q(ctx).QueryRowContext(ctx, `
		SELECT count(*)
		FROM threads t
		JOIN connected_accounts ca ON ca.id = t.account_id
		WHERE ca.user_id = $1
		  AND EXISTS (SELECT 1 FROM jsonb_array_elements(t.participants) p
		              WHERE lower(p->>'email') = lower($2))`,
		userID, email).Scan(&c.ThreadCount); err != nil {
		return c, err
	}

	// Recent threads via participants containment (thread repo columns reused).
	rows, err := r.q(ctx).QueryContext(ctx, `
		SELECT `+threadCols+`
		FROM threads t
		JOIN connected_accounts ca ON ca.id = t.account_id
		WHERE ca.user_id = $1
		  AND EXISTS (SELECT 1 FROM jsonb_array_elements(t.participants) p
		              WHERE lower(p->>'email') = lower($2))
		ORDER BY t.last_message_at DESC
		LIMIT 5`, userID, email)
	if err != nil {
		return c, err
	}
	defer func() { _ = rows.Close() }()
	threads, err := collectThreads(rows)
	if err != nil {
		return c, err
	}
	c.RecentThreads = threads
	return c, nil
}

func (r messageRepo) attachmentsFor(ctx context.Context, messageIDs []string) (map[string][]domain.Attachment, error) {
	ids, err := jsonArray(messageIDs)
	if err != nil {
		return nil, err
	}
	rows, err := r.q(ctx).QueryContext(ctx, `
		SELECT id, message_id, filename, mime_type, size_bytes, provider_attachment_id FROM attachments
		WHERE message_id IN (SELECT jsonb_array_elements_text($1::jsonb))
		ORDER BY id`, ids)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	byMsg := map[string][]domain.Attachment{}
	for rows.Next() {
		var a domain.Attachment
		var messageID string
		if err := rows.Scan(&a.ID, &messageID, &a.Filename, &a.MimeType, &a.SizeBytes, &a.ProviderAttachmentID); err != nil {
			return nil, err
		}
		byMsg[messageID] = append(byMsg[messageID], a)
	}
	return byMsg, rows.Err()
}

// --- port.LabelRepo ----------------------------------------------------------

const labelCols = `id, account_id, provider_label_id, name, kind, color`

func scanLabel(r rowScanner) (domain.Label, error) {
	var l domain.Label
	var color sql.NullString
	if err := r.Scan(&l.ID, &l.AccountID, &l.ProviderLabelID, &l.Name, &l.Kind, &color); err != nil {
		return domain.Label{}, notFound(err)
	}
	l.Color = strPtr(color)
	return l, nil
}

func (r labelRepo) Upsert(ctx context.Context, l domain.Label) (domain.Label, error) {
	if l.ID == "" {
		l.ID = newID()
	}
	if l.Kind == "" {
		l.Kind = domain.LabelKindUser
	}
	err := r.q(ctx).QueryRowContext(ctx, `
		INSERT INTO labels (id, account_id, provider_label_id, name, kind, color)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (account_id, provider_label_id) DO UPDATE SET
			name  = EXCLUDED.name,
			kind  = EXCLUDED.kind,
			color = EXCLUDED.color
		RETURNING id`,
		l.ID, l.AccountID, l.ProviderLabelID, l.Name, string(l.Kind), nullStrPtr(l.Color)).Scan(&l.ID)
	if err != nil {
		return domain.Label{}, err
	}
	return l, nil
}

func (r labelRepo) ListByAccount(ctx context.Context, accountID string) ([]domain.Label, error) {
	rows, err := r.q(ctx).QueryContext(ctx,
		`SELECT `+labelCols+` FROM labels WHERE account_id = $1 ORDER BY name`, accountID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	labels := []domain.Label{}
	for rows.Next() {
		l, err := scanLabel(rows)
		if err != nil {
			return nil, err
		}
		labels = append(labels, l)
	}
	return labels, rows.Err()
}

func (r labelRepo) GetByID(ctx context.Context, id string) (domain.Label, error) {
	row := r.q(ctx).QueryRowContext(ctx,
		`SELECT `+labelCols+` FROM labels WHERE id = $1`, id)
	return scanLabel(row)
}

func (r labelRepo) ListByUser(ctx context.Context, userID string) ([]domain.Label, error) {
	rows, err := r.q(ctx).QueryContext(ctx,
		`SELECT `+labelCols+` FROM labels
		 WHERE account_id IN (SELECT id FROM connected_accounts WHERE user_id = $1)
		 ORDER BY name`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	labels := []domain.Label{}
	for rows.Next() {
		l, err := scanLabel(rows)
		if err != nil {
			return nil, err
		}
		labels = append(labels, l)
	}
	return labels, rows.Err()
}
