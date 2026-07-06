package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// CalendarServiceDeps wires a CalendarService.
type CalendarServiceDeps struct {
	Subscriptions     port.SubscriptionRepo
	Accounts          port.AccountRepo
	Calendars         port.CalendarRepo
	Events            port.EventRepo
	CalendarProviders map[domain.Provider]port.CalendarProvider
	OAuth             map[domain.Provider]port.OAuthGateway
	Clock             port.Clock
	// SelfHosted unlocks the paywall (open-core self-hosted mode).
	SelfHosted bool
}

// CalendarService implements port.CalendarService. Event mutations write
// through to the provider and update the local mirror optimistically.
type CalendarService struct {
	ent       entitlement
	accounts  port.AccountRepo
	calendars port.CalendarRepo
	events    port.EventRepo
	cal       map[domain.Provider]port.CalendarProvider
	tokens    tokenSource
	clock     port.Clock
}

var _ port.CalendarService = (*CalendarService)(nil)

func NewCalendarService(d CalendarServiceDeps) *CalendarService {
	return &CalendarService{
		ent:       entitlement{subs: d.Subscriptions, clock: d.Clock, selfHost: d.SelfHosted},
		accounts:  d.Accounts,
		calendars: d.Calendars,
		events:    d.Events,
		cal:       d.CalendarProviders,
		tokens:    tokenSource{accounts: d.Accounts, oauth: d.OAuth, clock: d.Clock},
		clock:     d.Clock,
	}
}

func (s *CalendarService) ListCalendars(ctx context.Context, userID string) ([]domain.Calendar, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	cals, err := s.calendars.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if cals == nil {
		cals = []domain.Calendar{}
	}
	return cals, nil
}

func (s *CalendarService) UpdateCalendar(ctx context.Context, userID, calendarID string, patch port.CalendarPatch) (domain.Calendar, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Calendar{}, err
	}
	c, _, err := s.ownedCalendar(ctx, userID, calendarID)
	if err != nil {
		return domain.Calendar{}, err
	}
	if patch.IsVisible != nil {
		c.IsVisible = *patch.IsVisible
	}
	if patch.Color != nil {
		c.Color = *patch.Color
	}
	if err := s.calendars.Update(ctx, c); err != nil {
		return domain.Calendar{}, err
	}
	return c, nil
}

// ListEvents serves from the local mirror; recurrences are expanded at sync
// time (providers are queried with single-instance expansion).
func (s *CalendarService) ListEvents(ctx context.Context, userID string, from, to time.Time, calendarIDs []string) ([]domain.Event, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	if !to.After(from) {
		return nil, fmt.Errorf("%w: `to` must be after `from`", domain.ErrValidation)
	}
	evs, err := s.events.ListInRange(ctx, userID, from, to, calendarIDs)
	if err != nil {
		return nil, err
	}
	if evs == nil {
		evs = []domain.Event{}
	}
	return evs, nil
}

func (s *CalendarService) CreateEvent(ctx context.Context, userID string, in domain.EventInput) (domain.Event, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Event{}, err
	}
	if in.CalendarID == "" {
		return domain.Event{}, fmt.Errorf("%w: calendarId is required", domain.ErrValidation)
	}
	if strings.TrimSpace(in.Title) == "" {
		return domain.Event{}, fmt.Errorf("%w: title is required", domain.ErrValidation)
	}
	if !in.End.After(in.Start) {
		return domain.Event{}, fmt.Errorf("%w: end must be after start", domain.ErrValidation)
	}
	c, acct, err := s.ownedCalendar(ctx, userID, in.CalendarID)
	if err != nil {
		return domain.Event{}, err
	}
	if !c.CanWrite {
		return domain.Event{}, fmt.Errorf("%w: calendar is read-only", domain.ErrValidation)
	}

	ev := eventFromInput(in, c.ID)
	if provider, ok := s.cal[acct.Provider]; ok {
		token, err := s.tokens.accessToken(ctx, acct)
		if err != nil {
			return domain.Event{}, err
		}
		created, err := provider.CreateEvent(ctx, token, c.ProviderCalendarID, in)
		if err != nil {
			return domain.Event{}, fmt.Errorf("provider write-through failed: %w", err)
		}
		created.ID = ev.ID
		created.CalendarID = c.ID
		ev = created
	}
	return s.events.Upsert(ctx, ev)
}

func (s *CalendarService) UpdateEvent(ctx context.Context, userID, eventID string, patch domain.EventPatch) (domain.Event, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Event{}, err
	}
	ev, c, acct, err := s.ownedEvent(ctx, userID, eventID)
	if err != nil {
		return domain.Event{}, err
	}
	if !c.CanWrite {
		return domain.Event{}, fmt.Errorf("%w: calendar is read-only", domain.ErrValidation)
	}
	if patch.Start != nil && patch.End != nil && !patch.End.After(*patch.Start) {
		return domain.Event{}, fmt.Errorf("%w: end must be after start", domain.ErrValidation)
	}

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
	return s.events.Upsert(ctx, ev)
}

func (s *CalendarService) DeleteEvent(ctx context.Context, userID, eventID string) error {
	if err := s.ent.require(ctx, userID); err != nil {
		return err
	}
	ev, c, acct, err := s.ownedEvent(ctx, userID, eventID)
	if err != nil {
		return err
	}
	if !c.CanWrite {
		return fmt.Errorf("%w: calendar is read-only", domain.ErrValidation)
	}
	if provider, ok := s.cal[acct.Provider]; ok && ev.ProviderEventID != "" {
		token, err := s.tokens.accessToken(ctx, acct)
		if err != nil {
			return err
		}
		if err := provider.DeleteEvent(ctx, token, c.ProviderCalendarID, ev.ProviderEventID); err != nil {
			return fmt.Errorf("provider write-through failed: %w", err)
		}
	}
	return s.events.Delete(ctx, ev.ID)
}

func (s *CalendarService) RSVP(ctx context.Context, userID, eventID string, response domain.RsvpStatus) (domain.Event, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Event{}, err
	}
	ev, c, acct, err := s.ownedEvent(ctx, userID, eventID)
	if err != nil {
		return domain.Event{}, err
	}
	if provider, ok := s.cal[acct.Provider]; ok && ev.ProviderEventID != "" {
		token, err := s.tokens.accessToken(ctx, acct)
		if err != nil {
			return domain.Event{}, err
		}
		if err := provider.RSVP(ctx, token, c.ProviderCalendarID, ev.ProviderEventID, response); err != nil {
			return domain.Event{}, fmt.Errorf("provider write-through failed: %w", err)
		}
	}
	for i := range ev.Attendees {
		if strings.EqualFold(ev.Attendees[i].Email, acct.Email) {
			ev.Attendees[i].Response = response
		}
	}
	return s.events.Upsert(ctx, ev)
}

// Availability computes free windows across every calendar of the user
// between from and to: busy intervals (non-cancelled, non-all-day events the
// user has not declined) are merged, and each remaining gap of at least
// slotDuration is returned as one slot for the share-availability flow.
func (s *CalendarService) Availability(ctx context.Context, userID string, from, to time.Time, slotDuration time.Duration) ([]domain.AvailabilitySlot, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	if !to.After(from) {
		return nil, fmt.Errorf("%w: `to` must be after `from`", domain.ErrValidation)
	}
	if slotDuration <= 0 {
		return nil, fmt.Errorf("%w: duration must be positive", domain.ErrValidation)
	}
	evs, err := s.events.ListInRange(ctx, userID, from, to, nil)
	if err != nil {
		return nil, err
	}
	accounts, err := s.accounts.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	ownEmails := make(map[string]struct{}, len(accounts))
	for _, a := range accounts {
		ownEmails[strings.ToLower(a.Email)] = struct{}{}
	}

	type interval struct{ start, end time.Time }
	busy := make([]interval, 0, len(evs))
	for _, ev := range evs {
		// All-day events are usually informational (birthdays, OOO banners) and
		// must not blanket the whole day as busy; cancelled or user-declined
		// events are not busy either.
		if ev.Status == domain.EventCancelled || ev.AllDay || declinedByUser(ev, ownEmails) {
			continue
		}
		start, end := ev.Start, ev.End
		if start.Before(from) {
			start = from
		}
		if end.After(to) {
			end = to
		}
		if end.After(start) {
			busy = append(busy, interval{start, end})
		}
	}
	sort.Slice(busy, func(i, j int) bool { return busy[i].start.Before(busy[j].start) })

	slots := []domain.AvailabilitySlot{}
	cursor := from
	for _, b := range busy {
		if b.start.Sub(cursor) >= slotDuration {
			slots = append(slots, domain.AvailabilitySlot{Start: cursor, End: b.start})
		}
		if b.end.After(cursor) {
			cursor = b.end
		}
	}
	if to.Sub(cursor) >= slotDuration {
		slots = append(slots, domain.AvailabilitySlot{Start: cursor, End: to})
	}
	return slots, nil
}

// --- helpers ---

func (s *CalendarService) ownedCalendar(ctx context.Context, userID, calendarID string) (domain.Calendar, domain.ConnectedAccount, error) {
	c, err := s.calendars.GetByID(ctx, calendarID)
	if err != nil {
		return domain.Calendar{}, domain.ConnectedAccount{}, err
	}
	acct, err := s.accounts.GetByID(ctx, c.AccountID)
	if err != nil {
		return domain.Calendar{}, domain.ConnectedAccount{}, err
	}
	if acct.UserID != userID {
		return domain.Calendar{}, domain.ConnectedAccount{}, domain.ErrNotFound
	}
	return c, acct, nil
}

func (s *CalendarService) ownedEvent(ctx context.Context, userID, eventID string) (domain.Event, domain.Calendar, domain.ConnectedAccount, error) {
	ev, err := s.events.GetByID(ctx, eventID)
	if err != nil {
		return domain.Event{}, domain.Calendar{}, domain.ConnectedAccount{}, err
	}
	c, acct, err := s.ownedCalendar(ctx, userID, ev.CalendarID)
	if err != nil {
		return domain.Event{}, domain.Calendar{}, domain.ConnectedAccount{}, err
	}
	return ev, c, acct, nil
}

func eventFromInput(in domain.EventInput, calendarID string) domain.Event {
	ev := domain.Event{
		ID:              newID(),
		CalendarID:      calendarID,
		Title:           in.Title,
		Start:           in.Start,
		End:             in.End,
		AllDay:          in.AllDay,
		Attendees:       []domain.Attendee{},
		Status:          domain.EventConfirmed,
		Visibility:      domain.VisibilityDefault,
		ReminderMinutes: in.ReminderMinutes,
	}
	if ev.ReminderMinutes == nil {
		ev.ReminderMinutes = []int{}
	}
	if in.Description != "" {
		ev.Description = ptr(in.Description)
	}
	if in.Location != "" {
		ev.Location = ptr(in.Location)
	}
	if in.RecurrenceRule != "" {
		ev.RecurrenceRule = ptr(in.RecurrenceRule)
	}
	for _, email := range in.AttendeeEmails {
		ev.Attendees = append(ev.Attendees, domain.Attendee{Email: email, Response: domain.RsvpNeedsAction})
	}
	return ev
}

func applyEventPatch(ev *domain.Event, patch domain.EventPatch) {
	if patch.Title != nil {
		ev.Title = *patch.Title
	}
	if patch.Description != nil {
		ev.Description = patch.Description
	}
	if patch.Location != nil {
		ev.Location = patch.Location
	}
	if patch.Start != nil {
		ev.Start = *patch.Start
	}
	if patch.End != nil {
		ev.End = *patch.End
	}
	if patch.AllDay != nil {
		ev.AllDay = *patch.AllDay
	}
	if patch.RecurrenceRule != nil {
		ev.RecurrenceRule = patch.RecurrenceRule
	}
	if patch.AttendeeEmails != nil {
		attendees := make([]domain.Attendee, 0, len(*patch.AttendeeEmails))
		for _, email := range *patch.AttendeeEmails {
			response := domain.RsvpNeedsAction
			for _, existing := range ev.Attendees {
				if strings.EqualFold(existing.Email, email) {
					response = existing.Response
					break
				}
			}
			attendees = append(attendees, domain.Attendee{Email: email, Response: response})
		}
		ev.Attendees = attendees
	}
	if patch.ReminderMinutes != nil {
		ev.ReminderMinutes = *patch.ReminderMinutes
	}
}

func declinedByUser(ev domain.Event, ownEmails map[string]struct{}) bool {
	for _, a := range ev.Attendees {
		if _, ok := ownEmails[strings.ToLower(a.Email)]; ok {
			return a.Response == domain.RsvpDeclined
		}
	}
	return false
}
