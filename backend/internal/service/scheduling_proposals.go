package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// ownedEventForProposals loads an event and enforces that it belongs to
// userID via its calendar's connected account. This mirrors
// CalendarService.ownedEvent exactly (foreign/missing events are both 404,
// never 403); it is duplicated here rather than shared because
// SchedulingService owns its own repos and ports are frozen for this task.
func (s *SchedulingService) ownedEventForProposals(ctx context.Context, userID, eventID string) (domain.Event, domain.Calendar, domain.ConnectedAccount, error) {
	ev, err := s.events.GetByID(ctx, eventID)
	if err != nil {
		return domain.Event{}, domain.Calendar{}, domain.ConnectedAccount{}, err
	}
	c, acct, err := ownedCalendarByID(ctx, s.calendars, s.accounts, userID, ev.CalendarID)
	if err != nil {
		return domain.Event{}, domain.Calendar{}, domain.ConnectedAccount{}, err
	}
	return ev, c, acct, nil
}

// organizerAttendeeEmail returns the email of ev's organizer attendee, if any.
func organizerAttendeeEmail(ev domain.Event) (string, bool) {
	for _, a := range ev.Attendees {
		if a.Organizer {
			return a.Email, true
		}
	}
	return "", false
}

// findProposingAccount locates the connected account (any provider) owned by
// userID whose email matches an attendee on ev. A match against the
// organizer attendee is domain.ErrValidation (the organizer cannot
// counter-propose their own event — they should just edit it); no match at
// all is domain.ErrNotFound (userID is not an invitee on this event).
func (s *SchedulingService) findProposingAccount(ctx context.Context, userID string, ev domain.Event) (domain.ConnectedAccount, error) {
	accts, err := s.accounts.ListByUser(ctx, userID)
	if err != nil {
		return domain.ConnectedAccount{}, err
	}
	byEmail := make(map[string]domain.ConnectedAccount, len(accts))
	for _, a := range accts {
		byEmail[strings.ToLower(a.Email)] = a
	}
	for _, at := range ev.Attendees {
		acct, ok := byEmail[strings.ToLower(at.Email)]
		if !ok {
			continue
		}
		if at.Organizer {
			return domain.ConnectedAccount{}, fmt.Errorf("%w: the organizer cannot propose a new time for their own event", domain.ErrValidation)
		}
		return acct, nil
	}
	return domain.ConnectedAccount{}, domain.ErrNotFound
}

// ProposeTime lets an event's attendee (never its organizer) suggest a
// different time for an event they do not own. The caller must own a
// connected account whose email appears as a non-organizer attendee on the
// mirrored event. The proposal is persisted pending first; only then —
// best-effort, same convention as ConfirmPoll's confirmation email — a
// notification goes to the organizer's address through the proposer's own
// account. A send failure here is never rolled back, just discarded.
func (s *SchedulingService) ProposeTime(ctx context.Context, userID, eventID string, in port.TimeProposalInput) (domain.TimeProposal, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.TimeProposal{}, err
	}
	if !in.End.After(in.Start) {
		return domain.TimeProposal{}, fmt.Errorf("%w: end must be after start", domain.ErrValidation)
	}

	ev, err := s.events.GetByID(ctx, eventID)
	if err != nil {
		return domain.TimeProposal{}, err
	}

	acct, err := s.findProposingAccount(ctx, userID, ev)
	if err != nil {
		return domain.TimeProposal{}, err
	}

	organizerEmail, ok := organizerAttendeeEmail(ev)
	if !ok {
		return domain.TimeProposal{}, fmt.Errorf("%w: event has no organizer attendee", domain.ErrValidation)
	}

	var note *string
	if trimmed := strings.TrimSpace(in.Note); trimmed != "" {
		note = ptr(trimmed)
	}

	p := domain.TimeProposal{
		ID:            newID(),
		EventID:       eventID,
		ProposerEmail: acct.Email,
		Start:         in.Start,
		End:           in.End,
		Note:          note,
		Status:        domain.ProposalPending,
		CreatedAt:     s.clock.Now(),
	}
	// Deliberately not ownerDisplayName here: that helper's neutral fallback
	// ("The organizer") is correct for the booking/poll OWNER's public name,
	// but this proposer is explicitly never the organizer (see the check
	// above) — falling back to that label would misrepresent who proposed the
	// new time in the organizer-facing notification. Leaving ProposerName
	// blank instead degrades buildProposalNotificationEmail's "who" line to
	// the (already-shown) ProposerEmail alone.
	if u, err := s.users.GetByID(ctx, userID); err == nil && u.Name != nil && strings.TrimSpace(*u.Name) != "" {
		p.ProposerName = *u.Name
	}

	created, err := s.proposals.Create(ctx, p)
	if err != nil {
		return domain.TimeProposal{}, err
	}

	if provider, ok := s.mail[acct.Provider]; ok {
		if token, err := s.tokens.accessToken(ctx, acct); err == nil {
			organizerTZ := s.calendarTimeZone(ctx, ev.CalendarID)
			proposerTZ := s.userTimeZone(ctx, userID)
			subject, body := buildProposalEmail(ev, created, organizerTZ, proposerTZ)
			_, _ = provider.Send(ctx, token, port.OutgoingMessage{
				From:     domain.EmailAddress{Email: acct.Email},
				To:       []domain.EmailAddress{{Email: organizerEmail}},
				Subject:  subject,
				BodyText: body,
			})
		}
	}

	return created, nil
}

// calendarTimeZone returns c.TimeZone for calendarID, falling back to UTC
// when the calendar cannot be loaded or leaves it blank.
func (s *SchedulingService) calendarTimeZone(ctx context.Context, calendarID string) string {
	if c, err := s.calendars.GetByID(ctx, calendarID); err == nil && c.TimeZone != "" {
		return c.TimeZone
	}
	return "UTC"
}

// userTimeZone returns userID's scheduling settings TimeZone, falling back to
// UTC when unset or unavailable.
func (s *SchedulingService) userTimeZone(ctx context.Context, userID string) string {
	if s.settings == nil {
		return "UTC"
	}
	if settings, err := s.settings.Get(ctx, userID); err == nil && settings.TimeZone != "" {
		return settings.TimeZone
	}
	return "UTC"
}

// buildProposalEmail renders the "new time proposed" notice sent to the
// organizer. Pure (no I/O) so it is unit-testable independent of the mail
// provider. Old and new times are shown in both the organizer's and the
// proposer's zones when those zones resolve; an unresolvable zone name (e.g.
// a corrupt UserSettings row) silently falls back to showing that side once,
// in UTC, rather than failing the whole notification.
func buildProposalEmail(ev domain.Event, p domain.TimeProposal, organizerTZ, proposerTZ string) (subject, body string) {
	subject = fmt.Sprintf("New time proposed: %s", ev.Title)

	who := p.ProposerEmail
	if p.ProposerName != "" {
		who = fmt.Sprintf("%s (%s)", p.ProposerName, p.ProposerEmail)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Hi,\n\n%s has proposed a new time for %q.\n\n", who, ev.Title)
	fmt.Fprintf(&b, "Current time:\n")
	writeTimeRangeBothZones(&b, ev.Start, ev.End, organizerTZ, proposerTZ)
	fmt.Fprintf(&b, "\nProposed time:\n")
	writeTimeRangeBothZones(&b, p.Start, p.End, organizerTZ, proposerTZ)
	if p.Note != nil && strings.TrimSpace(*p.Note) != "" {
		fmt.Fprintf(&b, "\nNote from %s:\n%s\n", p.ProposerEmail, *p.Note)
	}
	return subject, b.String()
}

// writeTimeRangeBothZones prints start-end once per distinct, resolvable
// zone name (organizerTZ and proposerTZ collapse to a single line when
// equal).
func writeTimeRangeBothZones(b *strings.Builder, start, end time.Time, organizerTZ, proposerTZ string) {
	zones := []string{organizerTZ}
	if proposerTZ != organizerTZ {
		zones = append(zones, proposerTZ)
	}
	for _, tz := range zones {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			loc = time.UTC
			tz = "UTC"
		}
		fmt.Fprintf(b, "  %s - %s (%s)\n", start.In(loc).Format(time.RFC1123), end.In(loc).Format(time.RFC1123), tz)
	}
}

// ListProposals returns every proposal on eventID; the caller must be the
// event's organizer (owner side).
func (s *SchedulingService) ListProposals(ctx context.Context, userID, eventID string) ([]domain.TimeProposal, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	if _, _, _, err := s.ownedEventForProposals(ctx, userID, eventID); err != nil {
		return nil, err
	}
	list, err := s.proposals.ListByEvent(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []domain.TimeProposal{}
	}
	return list, nil
}

// getPendingProposal loads proposalID, enforcing that it belongs to eventID
// and is currently pending. A foreign eventID is ErrNotFound; any other
// status (already accepted/declined/superseded, or a stale client
// double-submitting) is ErrConflict.
func (s *SchedulingService) getPendingProposal(ctx context.Context, eventID, proposalID string) (domain.TimeProposal, error) {
	p, err := s.proposals.GetByID(ctx, proposalID)
	if err != nil {
		return domain.TimeProposal{}, err
	}
	if p.EventID != eventID {
		return domain.TimeProposal{}, domain.ErrNotFound
	}
	if p.Status != domain.ProposalPending {
		return domain.TimeProposal{}, fmt.Errorf("%w: proposal is %s, not pending", domain.ErrConflict, p.Status)
	}
	return p, nil
}

// AcceptProposal is organizer-only. It patches the mirrored event to the
// proposal's Start/End via the calendar provider's write-through (Google/
// Graph deliver their own attendee notifications on that patch — Calendium
// sends no extra email here), marks the proposal accepted, and supersedes
// every other still-pending proposal on the same event.
func (s *SchedulingService) AcceptProposal(ctx context.Context, userID, eventID, proposalID string) (domain.Event, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Event{}, err
	}
	ev, c, acct, err := s.ownedEventForProposals(ctx, userID, eventID)
	if err != nil {
		return domain.Event{}, err
	}
	if !c.CanWrite {
		return domain.Event{}, fmt.Errorf("%w: calendar is read-only", domain.ErrValidation)
	}

	p, err := s.getPendingProposal(ctx, eventID, proposalID)
	if err != nil {
		return domain.Event{}, err
	}

	patch := domain.EventPatch{Start: ptr(p.Start), End: ptr(p.End)}
	applyEventPatch(&ev, patch)
	if provider, ok := s.cal[acct.Provider]; ok && ev.ProviderEventID != "" {
		token, err := s.tokens.accessToken(ctx, acct)
		if err != nil {
			return domain.Event{}, err
		}
		updated, err := provider.UpdateEvent(ctx, token, c.ProviderCalendarID, ev.ProviderEventID, patch)
		if err != nil {
			return domain.Event{}, fmt.Errorf("provider write-through failed: %w", err)
		}
		updated.ID = ev.ID
		updated.CalendarID = c.ID
		ev = updated
	}
	if ev, err = s.events.Upsert(ctx, ev); err != nil {
		return domain.Event{}, err
	}

	// The event patch (and its mirror) are already durable above, so from
	// here on the proposal MUST end up accepted no matter what; a failure
	// mid-supersede would otherwise leave siblings pending forever even
	// though the event has already moved.
	p.Status = domain.ProposalAccepted
	if err := s.proposals.Update(ctx, p); err != nil {
		return domain.Event{}, err
	}

	siblings, err := s.proposals.ListByEvent(ctx, eventID)
	if err != nil {
		return domain.Event{}, err
	}
	for _, sib := range siblings {
		if sib.ID == p.ID || sib.Status != domain.ProposalPending {
			continue
		}
		sib.Status = domain.ProposalSuperseded
		if err := s.proposals.Update(ctx, sib); err != nil {
			return domain.Event{}, err
		}
	}

	return ev, nil
}

// DeclineProposal is organizer-only: it marks the proposal declined without
// touching the event.
func (s *SchedulingService) DeclineProposal(ctx context.Context, userID, eventID, proposalID string) error {
	if err := s.ent.require(ctx, userID); err != nil {
		return err
	}
	if _, _, _, err := s.ownedEventForProposals(ctx, userID, eventID); err != nil {
		return err
	}
	p, err := s.getPendingProposal(ctx, eventID, proposalID)
	if err != nil {
		return err
	}
	p.Status = domain.ProposalDeclined
	return s.proposals.Update(ctx, p)
}
