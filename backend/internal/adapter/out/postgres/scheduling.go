package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"calendium/backend/internal/domain"
)

// isConflictSQLState reports whether err is the bookings_no_overlap
// exclusion constraint firing (SQLSTATE 23P01) or a unique violation
// (23505, e.g. booking_links_slug_idx / meeting_polls token).
func isConflictSQLState(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23P01" || pgErr.Code == "23505"
	}
	return false
}

// --- port.BookingLinkRepo -----------------------------------------------------

const bookingLinkCols = `id, user_id, slug, title, description, calendar_id, duration_minutes,
	time_zone, windows, buffer_before_min, buffer_after_min, daily_limit, min_notice_min,
	max_advance_days, respect_working_hours, add_conferencing, active, created_at`

func scanBookingLink(r rowScanner) (domain.BookingLink, error) {
	var l domain.BookingLink
	var description sql.NullString
	var windows []byte
	if err := r.Scan(&l.ID, &l.UserID, &l.Slug, &l.Title, &description, &l.CalendarID,
		&l.DurationMinutes, &l.TimeZone, &windows, &l.BufferBeforeMin, &l.BufferAfterMin,
		&l.DailyLimit, &l.MinNoticeMin, &l.MaxAdvanceDays, &l.RespectWorkingHours,
		&l.AddConferencing, &l.Active, &l.CreatedAt); err != nil {
		return domain.BookingLink{}, notFound(err)
	}
	l.Description = strPtr(description)
	if err := unmarshalInto(windows, &l.Windows); err != nil {
		return domain.BookingLink{}, err
	}
	if l.Windows == nil {
		l.Windows = []domain.AvailabilityWindow{}
	}
	return l, nil
}

func (r bookingLinkRepo) Create(ctx context.Context, l domain.BookingLink) (domain.BookingLink, error) {
	if l.ID == "" {
		l.ID = newID()
	}
	windows, err := jsonArray(l.Windows)
	if err != nil {
		return domain.BookingLink{}, err
	}
	err = r.q(ctx).QueryRowContext(ctx, `
		INSERT INTO booking_links (id, user_id, slug, title, description, calendar_id,
			duration_minutes, time_zone, windows, buffer_before_min, buffer_after_min,
			daily_limit, min_notice_min, max_advance_days, respect_working_hours,
			add_conferencing, active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10, $11, $12, $13, $14, $15, $16, $17)
		RETURNING created_at`,
		l.ID, l.UserID, l.Slug, l.Title, nullStrPtr(l.Description), l.CalendarID,
		l.DurationMinutes, l.TimeZone, windows, l.BufferBeforeMin, l.BufferAfterMin,
		l.DailyLimit, l.MinNoticeMin, l.MaxAdvanceDays, l.RespectWorkingHours,
		l.AddConferencing, l.Active).Scan(&l.CreatedAt)
	if err != nil {
		if isConflictSQLState(err) {
			return domain.BookingLink{}, fmt.Errorf("%w: slug %q is already taken", domain.ErrConflict, l.Slug)
		}
		return domain.BookingLink{}, fmt.Errorf("postgres: create booking link: %w", err)
	}
	return l, nil
}

func (r bookingLinkRepo) GetByID(ctx context.Context, id string) (domain.BookingLink, error) {
	row := r.q(ctx).QueryRowContext(ctx,
		`SELECT `+bookingLinkCols+` FROM booking_links WHERE id = $1`, id)
	return scanBookingLink(row)
}

func (r bookingLinkRepo) GetBySlug(ctx context.Context, slug string) (domain.BookingLink, error) {
	row := r.q(ctx).QueryRowContext(ctx,
		`SELECT `+bookingLinkCols+` FROM booking_links WHERE lower(slug) = lower($1)`, slug)
	return scanBookingLink(row)
}

func (r bookingLinkRepo) ListByUser(ctx context.Context, userID string) ([]domain.BookingLink, error) {
	rows, err := r.q(ctx).QueryContext(ctx,
		`SELECT `+bookingLinkCols+` FROM booking_links WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	links := []domain.BookingLink{}
	for rows.Next() {
		l, err := scanBookingLink(rows)
		if err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	return links, rows.Err()
}

func (r bookingLinkRepo) Update(ctx context.Context, l domain.BookingLink) error {
	windows, err := jsonArray(l.Windows)
	if err != nil {
		return err
	}
	res, err := r.q(ctx).ExecContext(ctx, `
		UPDATE booking_links SET
			slug                  = $2,
			title                 = $3,
			description           = $4,
			calendar_id           = $5,
			duration_minutes      = $6,
			time_zone             = $7,
			windows               = $8::jsonb,
			buffer_before_min     = $9,
			buffer_after_min      = $10,
			daily_limit           = $11,
			min_notice_min        = $12,
			max_advance_days      = $13,
			respect_working_hours = $14,
			add_conferencing      = $15,
			active                = $16
		WHERE id = $1`,
		l.ID, l.Slug, l.Title, nullStrPtr(l.Description), l.CalendarID, l.DurationMinutes,
		l.TimeZone, windows, l.BufferBeforeMin, l.BufferAfterMin, l.DailyLimit,
		l.MinNoticeMin, l.MaxAdvanceDays, l.RespectWorkingHours, l.AddConferencing, l.Active)
	if err != nil {
		if isConflictSQLState(err) {
			return fmt.Errorf("%w: slug %q is already taken", domain.ErrConflict, l.Slug)
		}
		return err
	}
	return mustAffect(res, nil)
}

func (r bookingLinkRepo) Delete(ctx context.Context, id string) error {
	return mustAffect(r.q(ctx).ExecContext(ctx, `DELETE FROM booking_links WHERE id = $1`, id))
}

// --- port.BookingRepo ----------------------------------------------------------

const bookingCols = `id, link_id, status, start_at, end_at, invitee_name, invitee_email,
	invitee_tz, note, event_id, hold_expires_at, created_at`

func scanBooking(r rowScanner) (domain.Booking, error) {
	var b domain.Booking
	var status string
	var note, eventID sql.NullString
	var holdExpiresAt sql.NullTime
	if err := r.Scan(&b.ID, &b.LinkID, &status, &b.Start, &b.End, &b.InviteeName,
		&b.InviteeEmail, &b.InviteeTZ, &note, &eventID, &holdExpiresAt, &b.CreatedAt); err != nil {
		return domain.Booking{}, notFound(err)
	}
	b.Status = domain.BookingStatus(status)
	b.Note = strPtr(note)
	b.EventID = strPtr(eventID)
	b.HoldExpiresAt = timePtr(holdExpiresAt)
	return b, nil
}

// CreateHold inserts a status="hold" row. The bookings_no_overlap exclusion
// constraint (SQLSTATE 23P01) rejects an overlapping active (hold or
// confirmed) booking on the same link; that maps to domain.ErrConflict so
// callers can't tell an exclusion loss from a duplicate-key loss, which is
// fine — both mean "the slot is taken."
func (r bookingRepo) CreateHold(ctx context.Context, b domain.Booking) (domain.Booking, error) {
	if b.ID == "" {
		b.ID = newID()
	}
	err := r.q(ctx).QueryRowContext(ctx, `
		INSERT INTO bookings (id, link_id, status, start_at, end_at, invitee_name,
			invitee_email, invitee_tz, note, hold_expires_at)
		VALUES ($1, $2, 'hold', $3, $4, $5, $6, $7, $8, $9)
		RETURNING created_at`,
		b.ID, b.LinkID, b.Start, b.End, b.InviteeName,
		b.InviteeEmail, b.InviteeTZ, nullStrPtr(b.Note), nullTimePtr(b.HoldExpiresAt)).Scan(&b.CreatedAt)
	if err != nil {
		if isConflictSQLState(err) {
			return domain.Booking{}, fmt.Errorf("%w: slot is no longer available", domain.ErrConflict)
		}
		return domain.Booking{}, fmt.Errorf("postgres: create hold: %w", err)
	}
	b.Status = domain.BookingHold
	return b, nil
}

func (r bookingRepo) GetByID(ctx context.Context, id string) (domain.Booking, error) {
	row := r.q(ctx).QueryRowContext(ctx,
		`SELECT `+bookingCols+` FROM bookings WHERE id = $1`, id)
	return scanBooking(row)
}

// ListActiveInRange returns hold+confirmed bookings on linkID overlapping
// [from,to).
func (r bookingRepo) ListActiveInRange(ctx context.Context, linkID string, from, to time.Time) ([]domain.Booking, error) {
	rows, err := r.q(ctx).QueryContext(ctx, `
		SELECT `+bookingCols+` FROM bookings
		WHERE link_id = $1 AND status IN ('hold', 'confirmed') AND start_at < $3 AND end_at > $2
		ORDER BY start_at`, linkID, from, to)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return collectBookings(rows)
}

// ListByUser joins through booking_links since bookings carries no user_id.
func (r bookingRepo) ListByUser(ctx context.Context, userID string, limit int) ([]domain.Booking, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.q(ctx).QueryContext(ctx, `
		SELECT b.id, b.link_id, b.status, b.start_at, b.end_at, b.invitee_name, b.invitee_email,
			b.invitee_tz, b.note, b.event_id, b.hold_expires_at, b.created_at
		FROM bookings b
		JOIN booking_links bl ON bl.id = b.link_id
		WHERE bl.user_id = $1
		ORDER BY b.created_at DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return collectBookings(rows)
}

func collectBookings(rows *sql.Rows) ([]domain.Booking, error) {
	bookings := []domain.Booking{}
	for rows.Next() {
		b, err := scanBooking(rows)
		if err != nil {
			return nil, err
		}
		bookings = append(bookings, b)
	}
	return bookings, rows.Err()
}

// Confirm promotes a hold with a guarded UPDATE: zero rows matched means the
// hold already expired or was cancelled out from under the caller, which is
// a conflict, not a not-found (the id did exist, just not as a live hold).
func (r bookingRepo) Confirm(ctx context.Context, id, eventID string) error {
	res, err := r.q(ctx).ExecContext(ctx, `
		UPDATE bookings SET status = 'confirmed', event_id = $2, hold_expires_at = NULL
		WHERE id = $1 AND status = 'hold'`, id, eventID)
	if err != nil {
		return fmt.Errorf("postgres: confirm booking: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: hold is no longer available", domain.ErrConflict)
	}
	return nil
}

func (r bookingRepo) Cancel(ctx context.Context, id string) error {
	return mustAffect(r.q(ctx).ExecContext(ctx, `UPDATE bookings SET status = 'cancelled' WHERE id = $1`, id))
}

// ExpireHolds cancels holds whose hold_expires_at <= now and returns the count.
func (r bookingRepo) ExpireHolds(ctx context.Context, now time.Time) (int64, error) {
	res, err := r.q(ctx).ExecContext(ctx, `
		UPDATE bookings SET status = 'cancelled'
		WHERE status = 'hold' AND hold_expires_at <= $1`, now)
	if err != nil {
		return 0, fmt.Errorf("postgres: expire holds: %w", err)
	}
	return res.RowsAffected()
}

// --- port.PollRepo ---------------------------------------------------------

const pollCols = `id, user_id, token, title, description, calendar_id, duration_minutes,
	options, status, winner_option_id, event_id, created_at`

func scanPoll(r rowScanner) (domain.MeetingPoll, error) {
	var p domain.MeetingPoll
	var description, winnerOptionID, eventID sql.NullString
	var status string
	var options []byte
	if err := r.Scan(&p.ID, &p.UserID, &p.Token, &p.Title, &description, &p.CalendarID,
		&p.DurationMinutes, &options, &status, &winnerOptionID, &eventID, &p.CreatedAt); err != nil {
		return domain.MeetingPoll{}, notFound(err)
	}
	p.Description = strPtr(description)
	p.Status = domain.PollStatus(status)
	p.WinnerOptionID = strPtr(winnerOptionID)
	p.EventID = strPtr(eventID)
	if err := unmarshalInto(options, &p.Options); err != nil {
		return domain.MeetingPoll{}, err
	}
	if p.Options == nil {
		p.Options = []domain.PollOption{}
	}
	return p, nil
}

func (r pollRepo) Create(ctx context.Context, p domain.MeetingPoll) (domain.MeetingPoll, error) {
	if p.ID == "" {
		p.ID = newID()
	}
	if p.Token == "" {
		p.Token = newID() // crypto/rand 16 bytes hex, same shape as newID's other uses
	}
	if p.Status == "" {
		p.Status = domain.PollOpen
	}
	options, err := jsonArray(p.Options)
	if err != nil {
		return domain.MeetingPoll{}, err
	}
	err = r.q(ctx).QueryRowContext(ctx, `
		INSERT INTO meeting_polls (id, user_id, token, title, description, calendar_id,
			duration_minutes, options, status, winner_option_id, event_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10, $11)
		RETURNING created_at`,
		p.ID, p.UserID, p.Token, p.Title, nullStrPtr(p.Description), p.CalendarID,
		p.DurationMinutes, options, string(p.Status), nullStrPtr(p.WinnerOptionID),
		nullStrPtr(p.EventID)).Scan(&p.CreatedAt)
	if err != nil {
		if isConflictSQLState(err) {
			return domain.MeetingPoll{}, fmt.Errorf("%w: poll token collision", domain.ErrConflict)
		}
		return domain.MeetingPoll{}, fmt.Errorf("postgres: create poll: %w", err)
	}
	if p.Options == nil {
		p.Options = []domain.PollOption{}
	}
	return p, nil
}

func (r pollRepo) GetByID(ctx context.Context, id string) (domain.MeetingPoll, error) {
	row := r.q(ctx).QueryRowContext(ctx, `SELECT `+pollCols+` FROM meeting_polls WHERE id = $1`, id)
	return scanPoll(row)
}

func (r pollRepo) GetByToken(ctx context.Context, token string) (domain.MeetingPoll, error) {
	row := r.q(ctx).QueryRowContext(ctx, `SELECT `+pollCols+` FROM meeting_polls WHERE token = $1`, token)
	return scanPoll(row)
}

func (r pollRepo) ListByUser(ctx context.Context, userID string) ([]domain.MeetingPoll, error) {
	rows, err := r.q(ctx).QueryContext(ctx,
		`SELECT `+pollCols+` FROM meeting_polls WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	polls := []domain.MeetingPoll{}
	for rows.Next() {
		p, err := scanPoll(rows)
		if err != nil {
			return nil, err
		}
		polls = append(polls, p)
	}
	return polls, rows.Err()
}

func (r pollRepo) Update(ctx context.Context, p domain.MeetingPoll) error {
	options, err := jsonArray(p.Options)
	if err != nil {
		return err
	}
	return mustAffect(r.q(ctx).ExecContext(ctx, `
		UPDATE meeting_polls SET
			title            = $2,
			description      = $3,
			calendar_id      = $4,
			duration_minutes = $5,
			options          = $6::jsonb,
			status           = $7,
			winner_option_id = $8,
			event_id         = $9
		WHERE id = $1`,
		p.ID, p.Title, nullStrPtr(p.Description), p.CalendarID, p.DurationMinutes,
		options, string(p.Status), nullStrPtr(p.WinnerOptionID), nullStrPtr(p.EventID)))
}

func (r pollRepo) Delete(ctx context.Context, id string) error {
	return mustAffect(r.q(ctx).ExecContext(ctx, `DELETE FROM meeting_polls WHERE id = $1`, id))
}

// UpsertVotes replaces each voter's ballot, keyed poll_id+option_id+lower(email)
// via poll_votes_unique_idx — the ON CONFLICT target expression must match
// that index exactly for Postgres to use it as the arbiter.
func (r pollRepo) UpsertVotes(ctx context.Context, votes []domain.PollVote) error {
	for _, v := range votes {
		_, err := r.q(ctx).ExecContext(ctx, `
			INSERT INTO poll_votes (poll_id, option_id, voter_email, voter_name, choice)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (poll_id, option_id, lower(voter_email)) DO UPDATE SET
				voter_name = EXCLUDED.voter_name,
				choice     = EXCLUDED.choice,
				created_at = now()`,
			v.PollID, v.OptionID, v.VoterEmail, v.VoterName, string(v.Choice))
		if err != nil {
			return fmt.Errorf("postgres: upsert poll vote: %w", err)
		}
	}
	return nil
}

func (r pollRepo) ListVotes(ctx context.Context, pollID string) ([]domain.PollVote, error) {
	rows, err := r.q(ctx).QueryContext(ctx, `
		SELECT poll_id, option_id, voter_email, voter_name, choice, created_at
		FROM poll_votes WHERE poll_id = $1 ORDER BY created_at`, pollID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	votes := []domain.PollVote{}
	for rows.Next() {
		var v domain.PollVote
		var choice string
		if err := rows.Scan(&v.PollID, &v.OptionID, &v.VoterEmail, &v.VoterName, &choice, &v.CreatedAt); err != nil {
			return nil, err
		}
		v.Choice = domain.PollVoteChoice(choice)
		votes = append(votes, v)
	}
	return votes, rows.Err()
}

// --- port.TimeProposalRepo ---------------------------------------------------

const timeProposalCols = `id, event_id, proposer_email, proposer_name, start_at, end_at,
	note, status, created_at`

func scanTimeProposal(r rowScanner) (domain.TimeProposal, error) {
	var p domain.TimeProposal
	var note sql.NullString
	var status string
	if err := r.Scan(&p.ID, &p.EventID, &p.ProposerEmail, &p.ProposerName, &p.Start, &p.End,
		&note, &status, &p.CreatedAt); err != nil {
		return domain.TimeProposal{}, notFound(err)
	}
	p.Note = strPtr(note)
	p.Status = domain.ProposalStatus(status)
	return p, nil
}

func (r timeProposalRepo) Create(ctx context.Context, p domain.TimeProposal) (domain.TimeProposal, error) {
	if p.ID == "" {
		p.ID = newID()
	}
	if p.Status == "" {
		p.Status = domain.ProposalPending
	}
	err := r.q(ctx).QueryRowContext(ctx, `
		INSERT INTO time_proposals (id, event_id, proposer_email, proposer_name, start_at, end_at, note, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING created_at`,
		p.ID, p.EventID, p.ProposerEmail, p.ProposerName, p.Start, p.End,
		nullStrPtr(p.Note), string(p.Status)).Scan(&p.CreatedAt)
	if err != nil {
		return domain.TimeProposal{}, fmt.Errorf("postgres: create time proposal: %w", err)
	}
	return p, nil
}

func (r timeProposalRepo) GetByID(ctx context.Context, id string) (domain.TimeProposal, error) {
	row := r.q(ctx).QueryRowContext(ctx, `SELECT `+timeProposalCols+` FROM time_proposals WHERE id = $1`, id)
	return scanTimeProposal(row)
}

func (r timeProposalRepo) ListByEvent(ctx context.Context, eventID string) ([]domain.TimeProposal, error) {
	rows, err := r.q(ctx).QueryContext(ctx,
		`SELECT `+timeProposalCols+` FROM time_proposals WHERE event_id = $1 ORDER BY created_at DESC`, eventID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	proposals := []domain.TimeProposal{}
	for rows.Next() {
		p, err := scanTimeProposal(rows)
		if err != nil {
			return nil, err
		}
		proposals = append(proposals, p)
	}
	return proposals, rows.Err()
}

func (r timeProposalRepo) Update(ctx context.Context, p domain.TimeProposal) error {
	return mustAffect(r.q(ctx).ExecContext(ctx, `
		UPDATE time_proposals SET
			proposer_email = $2,
			proposer_name  = $3,
			start_at       = $4,
			end_at         = $5,
			note           = $6,
			status         = $7
		WHERE id = $1`,
		p.ID, p.ProposerEmail, p.ProposerName, p.Start, p.End, nullStrPtr(p.Note), string(p.Status)))
}

// --- port.UserSettingsRepo ---------------------------------------------------

// Get returns a zero-value UserSettings (TimeZone "UTC") when no row exists,
// mirroring prefsRepo.Get's absent-row default rather than surfacing
// domain.ErrNotFound — scheduling settings always have sane defaults.
//
// user_settings.updated_at is write-only from this repo's perspective: it is
// stamped via SQL now() on Upsert but never scanned back, since
// domain.UserSettings carries no UpdatedAt field (Task 1 review note).
func (r userSettingsRepo) Get(ctx context.Context, userID string) (domain.UserSettings, error) {
	var s domain.UserSettings
	var workingHours []byte
	err := r.q(ctx).QueryRowContext(ctx, `
		SELECT user_id, time_zone, working_hours, working_location
		FROM user_settings WHERE user_id = $1`, userID).Scan(
		&s.UserID, &s.TimeZone, &workingHours, &s.WorkingLocation)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.UserSettings{UserID: userID, TimeZone: "UTC"}, nil
	}
	if err != nil {
		return domain.UserSettings{}, err
	}
	if err := unmarshalInto(workingHours, &s.WorkingHours); err != nil {
		return domain.UserSettings{}, err
	}
	if s.WorkingHours == nil {
		s.WorkingHours = []domain.AvailabilityWindow{}
	}
	return s, nil
}

func (r userSettingsRepo) Upsert(ctx context.Context, s domain.UserSettings) error {
	workingHours, err := jsonArray(s.WorkingHours)
	if err != nil {
		return err
	}
	_, err = r.q(ctx).ExecContext(ctx, `
		INSERT INTO user_settings (user_id, time_zone, working_hours, working_location, updated_at)
		VALUES ($1, $2, $3::jsonb, $4, now())
		ON CONFLICT (user_id) DO UPDATE SET
			time_zone        = EXCLUDED.time_zone,
			working_hours    = EXCLUDED.working_hours,
			working_location = EXCLUDED.working_location,
			updated_at       = now()`,
		s.UserID, s.TimeZone, workingHours, s.WorkingLocation)
	return err
}
