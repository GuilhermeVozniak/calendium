package service

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"sort"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// SchedulingServiceDeps wires a SchedulingService.
//
// NOTE: port.SchedulingService also covers booking links, propose-new-time,
// and guest free/busy; those are implemented by sibling tasks in separate
// files (e.g. scheduling_links.go) adding methods to this same struct and
// fields to this same Deps type. This task (meeting polls) only wires and
// implements the poll-related dependencies/methods.
type SchedulingServiceDeps struct {
	Subscriptions     port.SubscriptionRepo
	Accounts          port.AccountRepo
	Calendars         port.CalendarRepo
	Events            port.EventRepo
	Polls             port.PollRepo
	Users             port.UserRepo
	CalendarProviders map[domain.Provider]port.CalendarProvider
	MailProviders     map[domain.Provider]port.MailProvider
	OAuth             map[domain.Provider]port.OAuthGateway
	Clock             port.Clock
	// SelfHosted unlocks the paywall (open-core self-hosted mode).
	SelfHosted bool
}

// SchedulingService implements the meeting-polls surface of
// port.SchedulingService. Poll creation/confirmation write through to the
// provider (event creation) and mirror the result locally, matching
// CalendarService's write-through pattern.
type SchedulingService struct {
	ent       entitlement
	accounts  port.AccountRepo
	calendars port.CalendarRepo
	events    port.EventRepo
	polls     port.PollRepo
	users     port.UserRepo
	cal       map[domain.Provider]port.CalendarProvider
	mail      map[domain.Provider]port.MailProvider
	tokens    tokenSource
	clock     port.Clock
}

func NewSchedulingService(d SchedulingServiceDeps) *SchedulingService {
	return &SchedulingService{
		ent:       entitlement{subs: d.Subscriptions, clock: d.Clock, selfHost: d.SelfHosted},
		accounts:  d.Accounts,
		calendars: d.Calendars,
		events:    d.Events,
		polls:     d.Polls,
		users:     d.Users,
		cal:       d.CalendarProviders,
		mail:      d.MailProviders,
		tokens:    tokenSource{accounts: d.Accounts, oauth: d.OAuth, clock: d.Clock},
		clock:     d.Clock,
	}
}

const (
	minPollOptions   = 2
	maxPollOptions   = 10
	pollTokenBytes   = 16 // 32 hex chars
	maxTokenAttempts = 5
)

// ownedCalendarByID loads a calendar and enforces ownership via its account,
// mirroring CalendarService.ownedCalendar (kept package-level here so it can
// be shared without reaching into another task's file).
func ownedCalendarByID(ctx context.Context, calendars port.CalendarRepo, accounts port.AccountRepo, userID, calendarID string) (domain.Calendar, domain.ConnectedAccount, error) {
	c, err := calendars.GetByID(ctx, calendarID)
	if err != nil {
		return domain.Calendar{}, domain.ConnectedAccount{}, err
	}
	acct, err := accounts.GetByID(ctx, c.AccountID)
	if err != nil {
		return domain.Calendar{}, domain.ConnectedAccount{}, err
	}
	if acct.UserID != userID {
		return domain.Calendar{}, domain.ConnectedAccount{}, domain.ErrNotFound
	}
	return c, acct, nil
}

// ownedPoll loads a poll and enforces ownership; foreign rows are 404, never 403.
func (s *SchedulingService) ownedPoll(ctx context.Context, userID, pollID string) (domain.MeetingPoll, error) {
	p, err := s.polls.GetByID(ctx, pollID)
	if err != nil {
		return domain.MeetingPoll{}, err
	}
	if p.UserID != userID {
		return domain.MeetingPoll{}, domain.ErrNotFound
	}
	return p, nil
}

// CreatePoll validates the payload (2-10 options, every option end = start +
// duration), assigns server-side option ids and an unguessable 32-hex-char
// public token, and persists the poll in "open" status. A token collision at
// the repo (domain.ErrConflict) is retried with a freshly generated token.
func (s *SchedulingService) CreatePoll(ctx context.Context, userID string, in port.PollInput) (domain.MeetingPoll, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.MeetingPoll{}, err
	}
	if strings.TrimSpace(in.Title) == "" {
		return domain.MeetingPoll{}, fmt.Errorf("%w: title is required", domain.ErrValidation)
	}
	if in.DurationMinutes <= 0 {
		return domain.MeetingPoll{}, fmt.Errorf("%w: durationMinutes must be positive", domain.ErrValidation)
	}
	if len(in.Options) < minPollOptions || len(in.Options) > maxPollOptions {
		return domain.MeetingPoll{}, fmt.Errorf("%w: a poll needs between %d and %d options, got %d", domain.ErrValidation, minPollOptions, maxPollOptions, len(in.Options))
	}
	if in.CalendarID == "" {
		return domain.MeetingPoll{}, fmt.Errorf("%w: calendarId is required", domain.ErrValidation)
	}
	c, _, err := ownedCalendarByID(ctx, s.calendars, s.accounts, userID, in.CalendarID)
	if err != nil {
		return domain.MeetingPoll{}, err
	}
	if !c.CanWrite {
		return domain.MeetingPoll{}, fmt.Errorf("%w: calendar is read-only", domain.ErrValidation)
	}

	duration := time.Duration(in.DurationMinutes) * time.Minute
	options := make([]domain.PollOption, len(in.Options))
	for i, o := range in.Options {
		if !o.End.Equal(o.Start.Add(duration)) {
			return domain.MeetingPoll{}, fmt.Errorf("%w: option %d end must equal start + duration", domain.ErrValidation, i)
		}
		options[i] = domain.PollOption{ID: newID(), Start: o.Start, End: o.End}
	}

	var desc *string
	if in.Description != "" {
		desc = ptr(in.Description)
	}

	p := domain.MeetingPoll{
		UserID:          userID,
		Title:           in.Title,
		Description:     desc,
		CalendarID:      in.CalendarID,
		DurationMinutes: in.DurationMinutes,
		Options:         options,
		Status:          domain.PollOpen,
		CreatedAt:       s.clock.Now(),
	}

	var lastErr error
	for attempt := 0; attempt < maxTokenAttempts; attempt++ {
		p.Token = randomToken(pollTokenBytes)
		created, err := s.polls.Create(ctx, p)
		if err == nil {
			return created, nil
		}
		if !errors.Is(err, domain.ErrConflict) {
			return domain.MeetingPoll{}, err
		}
		lastErr = err
	}
	return domain.MeetingPoll{}, fmt.Errorf("service: could not generate a unique poll token after %d attempts: %w", maxTokenAttempts, lastErr)
}

// ListPolls returns every poll owned by userID.
func (s *SchedulingService) ListPolls(ctx context.Context, userID string) ([]domain.MeetingPoll, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	polls, err := s.polls.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if polls == nil {
		polls = []domain.MeetingPoll{}
	}
	return polls, nil
}

// DeletePoll removes a poll owned by userID (foreign/unknown ids are 404).
func (s *SchedulingService) DeletePoll(ctx context.Context, userID, pollID string) error {
	if err := s.ent.require(ctx, userID); err != nil {
		return err
	}
	p, err := s.ownedPoll(ctx, userID, pollID)
	if err != nil {
		return err
	}
	return s.polls.Delete(ctx, p.ID)
}

// publicPollView builds the anonymized public document for a poll: per-option
// vote tallies only — voter identities never cross this boundary.
func (s *SchedulingService) publicPollView(ctx context.Context, p domain.MeetingPoll) (port.PublicPoll, error) {
	votes, err := s.polls.ListVotes(ctx, p.ID)
	if err != nil {
		return port.PublicPoll{}, err
	}
	tallies := make(map[string]port.PollTally, len(p.Options))
	for _, o := range p.Options {
		tallies[o.ID] = port.PollTally{}
	}
	for _, v := range votes {
		t := tallies[v.OptionID]
		switch v.Choice {
		case domain.VoteYes:
			t.Yes++
		case domain.VoteNo:
			t.No++
		case domain.VoteIfNeeded:
			t.IfNeeded++
		}
		tallies[v.OptionID] = t
	}

	var organizerName string
	if s.users != nil {
		if u, err := s.users.GetByID(ctx, p.UserID); err == nil {
			if u.Name != nil && *u.Name != "" {
				organizerName = *u.Name
			} else {
				organizerName = u.Email
			}
		}
	}

	return port.PublicPoll{
		Token:           p.Token,
		Title:           p.Title,
		Description:     p.Description,
		OrganizerName:   organizerName,
		DurationMinutes: p.DurationMinutes,
		Status:          p.Status,
		Options:         p.Options,
		Tallies:         tallies,
		WinnerOptionID:  p.WinnerOptionID,
	}, nil
}

// PublicPollByToken serves the public poll page: title, options, and
// anonymized tallies. No authentication required.
func (s *SchedulingService) PublicPollByToken(ctx context.Context, token string) (port.PublicPoll, error) {
	p, err := s.polls.GetByToken(ctx, token)
	if err != nil {
		return port.PublicPoll{}, err
	}
	return s.publicPollView(ctx, p)
}

// VotePoll records (or replaces) one voter's ballot on an open poll and
// returns the refreshed public view. The poll must be open; every option id
// in the ballot must be a known option on the poll; the voter email must be
// syntactically valid. Re-voting (same email, same option) replaces the
// previous choice rather than appending a duplicate.
func (s *SchedulingService) VotePoll(ctx context.Context, token string, ballot port.PollBallot) (port.PublicPoll, error) {
	p, err := s.polls.GetByToken(ctx, token)
	if err != nil {
		return port.PublicPoll{}, err
	}
	if p.Status != domain.PollOpen {
		return port.PublicPoll{}, fmt.Errorf("%w: poll is not open for voting", domain.ErrConflict)
	}

	email := strings.TrimSpace(ballot.VoterEmail)
	if email == "" {
		return port.PublicPoll{}, fmt.Errorf("%w: voterEmail is required", domain.ErrValidation)
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return port.PublicPoll{}, fmt.Errorf("%w: invalid voterEmail %q", domain.ErrValidation, email)
	}
	if len(ballot.Choices) == 0 {
		return port.PublicPoll{}, fmt.Errorf("%w: ballot must include at least one choice", domain.ErrValidation)
	}

	known := make(map[string]bool, len(p.Options))
	for _, o := range p.Options {
		known[o.ID] = true
	}

	now := s.clock.Now()
	votes := make([]domain.PollVote, 0, len(ballot.Choices))
	for optionID, choice := range ballot.Choices {
		if !known[optionID] {
			return port.PublicPoll{}, fmt.Errorf("%w: unknown option id %q", domain.ErrValidation, optionID)
		}
		switch choice {
		case domain.VoteYes, domain.VoteNo, domain.VoteIfNeeded:
		default:
			return port.PublicPoll{}, fmt.Errorf("%w: unknown vote choice %q", domain.ErrValidation, choice)
		}
		votes = append(votes, domain.PollVote{
			PollID:     p.ID,
			OptionID:   optionID,
			VoterEmail: email,
			VoterName:  strings.TrimSpace(ballot.VoterName),
			Choice:     choice,
			CreatedAt:  now,
		})
	}

	if err := s.polls.UpsertVotes(ctx, votes); err != nil {
		return port.PublicPoll{}, err
	}
	return s.publicPollView(ctx, p)
}

// ConfirmPoll picks optionID as the winner: it creates the event via
// provider write-through (attendees = every yes/if_needed voter on that
// option), marks the poll confirmed, and emails every one of those voters a
// "time confirmed" notice through the organizer's own account. Confirming an
// already-confirmed poll with the same option is a no-op that returns the
// poll unchanged; a different option is domain.ErrConflict.
func (s *SchedulingService) ConfirmPoll(ctx context.Context, userID, pollID, optionID string) (domain.MeetingPoll, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.MeetingPoll{}, err
	}
	p, err := s.ownedPoll(ctx, userID, pollID)
	if err != nil {
		return domain.MeetingPoll{}, err
	}

	var winner *domain.PollOption
	for i := range p.Options {
		if p.Options[i].ID == optionID {
			winner = &p.Options[i]
			break
		}
	}
	if winner == nil {
		return domain.MeetingPoll{}, fmt.Errorf("%w: unknown option id %q", domain.ErrValidation, optionID)
	}

	if p.Status == domain.PollConfirmed {
		if p.WinnerOptionID != nil && *p.WinnerOptionID == optionID {
			return p, nil // idempotent: already confirmed with this option
		}
		return domain.MeetingPoll{}, fmt.Errorf("%w: poll already confirmed with a different option", domain.ErrConflict)
	}
	if p.Status != domain.PollOpen {
		return domain.MeetingPoll{}, fmt.Errorf("%w: poll is %s, not open", domain.ErrConflict, p.Status)
	}

	c, acct, err := ownedCalendarByID(ctx, s.calendars, s.accounts, userID, p.CalendarID)
	if err != nil {
		return domain.MeetingPoll{}, err
	}
	if !c.CanWrite {
		return domain.MeetingPoll{}, fmt.Errorf("%w: calendar is read-only", domain.ErrValidation)
	}

	votes, err := s.polls.ListVotes(ctx, p.ID)
	if err != nil {
		return domain.MeetingPoll{}, err
	}
	seen := map[string]bool{}
	var attendeeEmails []string
	for _, v := range votes {
		if v.OptionID != optionID {
			continue
		}
		if v.Choice != domain.VoteYes && v.Choice != domain.VoteIfNeeded {
			continue
		}
		key := strings.ToLower(v.VoterEmail)
		if seen[key] {
			continue
		}
		seen[key] = true
		attendeeEmails = append(attendeeEmails, v.VoterEmail)
	}
	sort.Strings(attendeeEmails)

	in := domain.EventInput{
		CalendarID:     p.CalendarID,
		Title:          p.Title,
		Start:          winner.Start,
		End:            winner.End,
		AttendeeEmails: attendeeEmails,
	}
	if p.Description != nil {
		in.Description = *p.Description
	}

	ev := eventFromInput(in, c.ID)
	if provider, ok := s.cal[acct.Provider]; ok {
		token, err := s.tokens.accessToken(ctx, acct)
		if err != nil {
			return domain.MeetingPoll{}, err
		}
		created, err := provider.CreateEvent(ctx, token, c.ProviderCalendarID, in)
		if err != nil {
			return domain.MeetingPoll{}, fmt.Errorf("provider write-through failed: %w", err)
		}
		created.ID = ev.ID
		created.CalendarID = c.ID
		ev = created
	}
	if _, err := s.events.Upsert(ctx, ev); err != nil {
		return domain.MeetingPoll{}, err
	}

	// Email every yes/if_needed voter before flipping the poll to confirmed,
	// so a send failure never leaves the poll falsely marked confirmed.
	if provider, ok := s.mail[acct.Provider]; ok && len(attendeeEmails) > 0 {
		token, err := s.tokens.accessToken(ctx, acct)
		if err != nil {
			return domain.MeetingPoll{}, err
		}
		for _, email := range attendeeEmails {
			subject, body := buildPollConfirmationEmail(p, *winner, email)
			if _, err := provider.Send(ctx, token, port.OutgoingMessage{
				From:     domain.EmailAddress{Email: acct.Email},
				To:       []domain.EmailAddress{{Email: email}},
				Subject:  subject,
				BodyText: body,
			}); err != nil {
				return domain.MeetingPoll{}, fmt.Errorf("poll confirmation email to %s failed: %w", email, err)
			}
		}
	}

	p.Status = domain.PollConfirmed
	winnerID := optionID
	p.WinnerOptionID = &winnerID
	eventID := ev.ID
	p.EventID = &eventID
	if err := s.polls.Update(ctx, p); err != nil {
		return domain.MeetingPoll{}, err
	}
	return p, nil
}

// buildPollConfirmationEmail renders the "time confirmed" notice sent to
// each yes/if_needed voter once the organizer picks a winning option. Pure:
// no I/O, so it is trivially unit-testable independent of the mail provider.
func buildPollConfirmationEmail(p domain.MeetingPoll, winner domain.PollOption, voterEmail string) (subject, body string) {
	subject = fmt.Sprintf("Time confirmed: %s", p.Title)

	var b strings.Builder
	fmt.Fprintf(&b, "Hi,\n\n%q has been confirmed for %s - %s.\n\n",
		p.Title, winner.Start.Format(time.RFC1123), winner.End.Format(time.RFC1123))
	if p.Description != nil && strings.TrimSpace(*p.Description) != "" {
		fmt.Fprintf(&b, "%s\n\n", *p.Description)
	}
	fmt.Fprintf(&b, "You're receiving this at %s because you voted on this meeting poll.\n", voterEmail)
	return subject, b.String()
}
