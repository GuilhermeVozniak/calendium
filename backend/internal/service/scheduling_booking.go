package service

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// holdTTL bounds how long an unconfirmed slot hold blocks a booking-link
// slot before ExpireHolds sweeps it back open.
const holdTTL = 5 * time.Minute

// maxBookingsList caps ListBookings so an owner's history can't grow an
// unbounded response.
const maxBookingsList = 200

// htmlEscaper escapes the handful of HTML-significant characters in
// buildConfirmationEmail's plain-text body when producing its HTML sibling.
// The rendered text never itself contains markup, so escaping the whole
// string (rather than only the interpolated fields) is safe and simplest.
var htmlEscaper = strings.NewReplacer(
	"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;",
)

// validateBookingRequest checks the public booking payload: a non-blank
// name, a syntactically valid email, and a loadable IANA time zone.
func validateBookingRequest(req port.BookingRequest) error {
	if strings.TrimSpace(req.InviteeName) == "" {
		return fmt.Errorf("%w: inviteeName is required", domain.ErrValidation)
	}
	if _, err := mail.ParseAddress(req.InviteeEmail); err != nil {
		return fmt.Errorf("%w: invalid inviteeEmail %q", domain.ErrValidation, req.InviteeEmail)
	}
	if _, err := time.LoadLocation(req.InviteeTZ); err != nil {
		return fmt.Errorf("%w: invalid inviteeTimeZone %q", domain.ErrValidation, req.InviteeTZ)
	}
	return nil
}

// containsStart reports whether start matches one of the currently offered
// slots' start instants exactly.
func containsStart(slots []domain.AvailabilitySlot, start time.Time) bool {
	for _, s := range slots {
		if s.Start.Equal(start) {
			return true
		}
	}
	return false
}

// overlapsAny reports whether [start,end) overlaps any busy interval.
func overlapsAny(busy []domain.BusyInterval, start, end time.Time) bool {
	for _, b := range busy {
		if start.Before(b.End) && b.Start.Before(end) {
			return true
		}
	}
	return false
}

// ownedCalendarForLink loads the link's target calendar and its owning
// account. A booking link's CalendarID is only ever set to a calendar
// already verified writable+owned at CreateLink/UpdateLink time, so this is
// a plain lookup rather than a fresh ownership check against an untrusted
// caller-supplied userID (the public booking flow has none).
func (s *SchedulingService) ownedCalendarForLink(ctx context.Context, link domain.BookingLink) (domain.Calendar, domain.ConnectedAccount, error) {
	return ownedCalendarByID(ctx, s.calendars, s.accounts, link.UserID, link.CalendarID)
}

// createBookingEvent creates the provider event for a confirmed hold (owner
// + invitee as attendees, conferencing per the link's AddConferencing
// setting) and returns the local mirror shape; the caller upserts it.
func (s *SchedulingService) createBookingEvent(ctx context.Context, link domain.BookingLink, cal domain.Calendar, acct domain.ConnectedAccount, hold domain.Booking) (domain.Event, error) {
	in := domain.EventInput{
		CalendarID:      cal.ID,
		Title:           fmt.Sprintf("%s: %s", link.Title, hold.InviteeName),
		Start:           hold.Start,
		End:             hold.End,
		AttendeeEmails:  []string{acct.Email, hold.InviteeEmail},
		AddConferencing: link.AddConferencing,
	}
	if hold.Note != nil {
		in.Description = *hold.Note
	}
	ev := eventFromInput(in, cal.ID)
	if provider, ok := s.cal[acct.Provider]; ok {
		token, err := s.tokens.accessToken(ctx, acct)
		if err != nil {
			return domain.Event{}, err
		}
		created, err := provider.CreateEvent(ctx, token, cal.ProviderCalendarID, in)
		if err != nil {
			return domain.Event{}, fmt.Errorf("provider write-through failed: %w", err)
		}
		created.ID = ev.ID
		created.CalendarID = cal.ID
		ev = created
	}
	return ev, nil
}

// Book implements the double-booking-safe pipeline:
//  1. hold  — INSERT status="hold"; the DB exclusion constraint serializes
//     concurrent competitors (loser → domain.ErrConflict).
//  2. re-check — live provider free/busy for the owner over [start,end);
//     catches events created outside Calendium since the last sync.
//  3. event — provider write-through CreateEvent on the link's calendar
//     (owner + invitee as attendees, conferencing per link setting).
//  4. confirm — promote the hold (guarded UPDATE) inside a tx with the
//     event upsert.
//  5. email — confirmation via the owner's MailProvider; failure is logged,
//     never rolls back the booking.
//
// Any failure in steps 2-4 cancels the hold before returning.
func (s *SchedulingService) Book(ctx context.Context, slug string, req port.BookingRequest) (domain.Booking, error) {
	link, err := s.links.GetBySlug(ctx, slug)
	if err != nil {
		return domain.Booking{}, err
	}
	if !link.Active {
		return domain.Booking{}, domain.ErrNotFound
	}
	if err := validateBookingRequest(req); err != nil {
		return domain.Booking{}, err
	}
	end := req.Start.Add(time.Duration(link.DurationMinutes) * time.Minute)

	// The requested start must be one of the currently offered slots
	// (window/buffer/limit rules re-evaluated server-side, never trusted
	// from the client).
	offered, err := s.PublicSlots(ctx, slug, req.Start, end)
	if err != nil {
		return domain.Booking{}, err
	}
	if !containsStart(offered, req.Start) {
		return domain.Booking{}, fmt.Errorf("%w: slot is no longer available", domain.ErrConflict)
	}

	now := s.clock.Now()
	expires := now.Add(holdTTL)
	var note *string
	if strings.TrimSpace(req.Note) != "" {
		note = ptr(req.Note)
	}
	hold, err := s.bookings.CreateHold(ctx, domain.Booking{
		ID: newID(), LinkID: link.ID, Status: domain.BookingHold,
		Start: req.Start, End: end,
		InviteeName:   strings.TrimSpace(req.InviteeName),
		InviteeEmail:  strings.ToLower(req.InviteeEmail),
		InviteeTZ:     req.InviteeTZ,
		Note:          note,
		HoldExpiresAt: &expires,
		CreatedAt:     now,
	})
	if err != nil {
		return domain.Booking{}, err // domain.ErrConflict → 409 upstream
	}

	cancelHold := func() { _ = s.bookings.Cancel(context.WithoutCancel(ctx), hold.ID) }

	// Live re-check against the provider (step 2).
	cal, acct, err := s.ownedCalendarForLink(ctx, link)
	if err != nil {
		cancelHold()
		return domain.Booking{}, err
	}
	if provider, ok := s.cal[acct.Provider]; ok {
		token, err := s.tokens.accessToken(ctx, acct)
		if err != nil {
			cancelHold()
			return domain.Booking{}, err
		}
		busy, err := provider.FreeBusy(ctx, token, []string{acct.Email}, hold.Start, hold.End)
		if err != nil {
			cancelHold()
			return domain.Booking{}, fmt.Errorf("provider free/busy check failed: %w", err)
		}
		if overlapsAny(busy[strings.ToLower(acct.Email)], hold.Start, hold.End) {
			cancelHold()
			return domain.Booking{}, fmt.Errorf("%w: slot was just taken on the owner's calendar", domain.ErrConflict)
		}
	}

	// Provider event (step 3) — reuses the CalendarService write-through shape.
	ev, err := s.createBookingEvent(ctx, link, cal, acct, hold)
	if err != nil {
		cancelHold()
		return domain.Booking{}, err
	}

	// Confirm hold + persist event atomically (step 4).
	err = s.tx.RunInTx(ctx, func(ctx context.Context) error {
		if _, err := s.events.Upsert(ctx, ev); err != nil {
			return err
		}
		return s.bookings.Confirm(ctx, hold.ID, ev.ID)
	})
	if err != nil {
		cancelHold()
		return domain.Booking{}, err
	}
	hold.Status, hold.EventID, hold.HoldExpiresAt = domain.BookingConfirmed, &ev.ID, nil

	// Confirmation email (step 5) — best-effort: the booking is already
	// confirmed above, so a send failure here is logged and discarded,
	// never rolled back.
	if err := s.sendBookingConfirmation(ctx, link, acct, hold, ev); err != nil {
		s.logger.Warn("booking confirmation email failed", "booking", hold.ID, "error", err)
	}
	return hold, nil
}

// sendBookingConfirmation emails the invitee (Cc: owner) that their booking
// is confirmed, through the owner's own MailProvider. A missing provider for
// the owner's connected-account type is not an error — it just means no
// email is sent.
func (s *SchedulingService) sendBookingConfirmation(ctx context.Context, link domain.BookingLink, acct domain.ConnectedAccount, booking domain.Booking, ev domain.Event) error {
	provider, ok := s.mail[acct.Provider]
	if !ok {
		return nil
	}
	ownerName := acct.Email
	if u, err := s.users.GetByID(ctx, link.UserID); err == nil {
		ownerName = ownerDisplayName(u)
	}
	subject, html, text := buildConfirmationEmail(link, booking, ev, ownerName)
	token, err := s.tokens.accessToken(ctx, acct)
	if err != nil {
		return err
	}
	_, err = provider.Send(ctx, token, port.OutgoingMessage{
		From:     domain.EmailAddress{Email: acct.Email},
		To:       []domain.EmailAddress{{Email: booking.InviteeEmail}},
		Cc:       []domain.EmailAddress{{Email: acct.Email}},
		Subject:  subject,
		BodyHTML: html,
		BodyText: text,
	})
	return err
}

// buildConfirmationEmail renders the booking-confirmed notice sent to the
// invitee: the slot in the invitee's own time zone (booking.InviteeTZ) with
// the owner's link time zone alongside it, plus a plain-text note that
// there's no self-serve cancel — reply to the organizer instead. Pure: no
// I/O, independently unit-testable.
func buildConfirmationEmail(link domain.BookingLink, booking domain.Booking, ev domain.Event, ownerName string) (subject, html, text string) {
	subject = fmt.Sprintf("Confirmed: %s with %s", link.Title, ownerName)

	inviteeLoc, err := time.LoadLocation(booking.InviteeTZ)
	if err != nil {
		inviteeLoc = time.UTC
	}
	ownerLoc, err := time.LoadLocation(link.TimeZone)
	if err != nil {
		ownerLoc = time.UTC
	}
	const layout = "Mon, Jan 2, 2006 3:04 PM MST"
	startInvitee := booking.Start.In(inviteeLoc).Format(layout)
	startOwner := booking.Start.In(ownerLoc).Format(layout)

	var b strings.Builder
	fmt.Fprintf(&b, "Hi %s,\n\n", booking.InviteeName)
	fmt.Fprintf(&b, "%q with %s is confirmed for %s.\n", link.Title, ownerName, startInvitee)
	fmt.Fprintf(&b, "(%s's time zone: %s)\n\n", ownerName, startOwner)
	if ev.Conferencing != nil && ev.Conferencing.URL != "" {
		fmt.Fprintf(&b, "Join: %s\n\n", ev.Conferencing.URL)
	}
	fmt.Fprintf(&b, "Need to cancel or reschedule? Reply to this email and let %s know.\n", ownerName)
	text = b.String()
	html = "<p>" + strings.ReplaceAll(htmlEscaper.Replace(text), "\n", "<br>") + "</p>"
	return subject, html, text
}

// ExpireHolds cancels every hold whose HoldExpiresAt has elapsed. Called
// periodically by cmd/worker alongside ProcessDueWork.
func (s *SchedulingService) ExpireHolds(ctx context.Context) error {
	count, err := s.bookings.ExpireHolds(ctx, s.clock.Now())
	if err != nil {
		return err
	}
	if count > 0 {
		s.logger.Info("expired booking holds", "count", count)
	}
	return nil
}

// ListBookings returns userID's most recent bookings across every one of
// their booking links, newest first, capped at maxBookingsList.
func (s *SchedulingService) ListBookings(ctx context.Context, userID string) ([]domain.Booking, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	bookings, err := s.bookings.ListByUser(ctx, userID, maxBookingsList)
	if err != nil {
		return nil, err
	}
	if bookings == nil {
		bookings = []domain.Booking{}
	}
	return bookings, nil
}

// CancelBooking lets the owner cancel one of their bookings: it deletes the
// provider event (when one was created), marks the booking cancelled, and
// sends the invitee a best-effort cancellation notice. Cancelling an
// already-cancelled booking is a no-op. A foreign booking is 404, never 403.
func (s *SchedulingService) CancelBooking(ctx context.Context, userID, bookingID string) error {
	if err := s.ent.require(ctx, userID); err != nil {
		return err
	}
	booking, err := s.bookings.GetByID(ctx, bookingID)
	if err != nil {
		return err
	}
	link, err := s.links.GetByID(ctx, booking.LinkID)
	if err != nil {
		return err
	}
	if link.UserID != userID {
		return domain.ErrNotFound
	}
	if booking.Status == domain.BookingCancelled {
		return nil // idempotent
	}

	cal, acct, err := s.ownedCalendarForLink(ctx, link)
	if err != nil {
		return err
	}
	if booking.EventID != nil {
		ev, err := s.events.GetByID(ctx, *booking.EventID)
		if err != nil {
			if !errors.Is(err, domain.ErrNotFound) {
				return err
			}
		} else {
			if provider, ok := s.cal[acct.Provider]; ok && ev.ProviderEventID != "" {
				token, err := s.tokens.accessToken(ctx, acct)
				if err != nil {
					return err
				}
				if err := provider.DeleteEvent(ctx, token, cal.ProviderCalendarID, ev.ProviderEventID); err != nil {
					return fmt.Errorf("provider write-through failed: %w", err)
				}
			}
			_ = s.events.Delete(ctx, ev.ID)
		}
	}

	if err := s.bookings.Cancel(ctx, booking.ID); err != nil {
		return err
	}

	if err := s.sendCancellationNotice(ctx, link, acct, booking); err != nil {
		s.logger.Warn("booking cancellation email failed", "booking", booking.ID, "error", err)
	}
	return nil
}

// sendCancellationNotice emails the invitee that the owner cancelled their
// booking, through the owner's own MailProvider. Best-effort: called only
// after the booking is already marked cancelled.
func (s *SchedulingService) sendCancellationNotice(ctx context.Context, link domain.BookingLink, acct domain.ConnectedAccount, booking domain.Booking) error {
	provider, ok := s.mail[acct.Provider]
	if !ok {
		return nil
	}
	ownerName := acct.Email
	if u, err := s.users.GetByID(ctx, link.UserID); err == nil {
		ownerName = ownerDisplayName(u)
	}
	subject, body := buildCancellationEmail(link, booking, ownerName)
	token, err := s.tokens.accessToken(ctx, acct)
	if err != nil {
		return err
	}
	_, err = provider.Send(ctx, token, port.OutgoingMessage{
		From:     domain.EmailAddress{Email: acct.Email},
		To:       []domain.EmailAddress{{Email: booking.InviteeEmail}},
		Subject:  subject,
		BodyText: body,
	})
	return err
}

// buildCancellationEmail renders the booking-cancelled notice sent to the
// invitee, in their own time zone. Pure: no I/O, independently unit-testable.
func buildCancellationEmail(link domain.BookingLink, booking domain.Booking, ownerName string) (subject, body string) {
	subject = fmt.Sprintf("Cancelled: %s with %s", link.Title, ownerName)

	loc, err := time.LoadLocation(booking.InviteeTZ)
	if err != nil {
		loc = time.UTC
	}
	when := booking.Start.In(loc).Format("Mon, Jan 2, 2006 3:04 PM MST")

	var b strings.Builder
	fmt.Fprintf(&b, "Hi %s,\n\n", booking.InviteeName)
	fmt.Fprintf(&b, "Your %q booking with %s on %s has been cancelled.\n", link.Title, ownerName, when)
	return subject, b.String()
}
