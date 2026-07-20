package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// AutomationServiceDeps wires the calendar automation engine (M2.8 Task 6;
// grows in Tasks 7, 8, 12, 14).
type AutomationServiceDeps struct {
	Prefs       port.CalendarPrefsRepo
	Accounts    port.AccountRepo
	Calendars   port.CalendarRepo
	Events      port.EventRepo
	Managed     port.ManagedEventRepo
	CalendarSvc port.CalendarService
	Clock       port.Clock
	Logger      *slog.Logger
}

// AutomationService implements port.AutomationService. Managed blocks are
// real provider events created through CalendarService's write-through path
// (provider-first; on provider failure nothing local changes) and tagged in
// managed_events so re-runs only ever touch the engine's own blocks.
type AutomationService struct {
	prefs       port.CalendarPrefsRepo
	accounts    port.AccountRepo
	calendars   port.CalendarRepo
	events      port.EventRepo
	managed     port.ManagedEventRepo
	calendarSvc port.CalendarService
	clock       port.Clock
	logger      *slog.Logger
}

// NewAutomationService wires an AutomationService.
func NewAutomationService(d AutomationServiceDeps) *AutomationService {
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &AutomationService{
		prefs:       d.Prefs,
		accounts:    d.Accounts,
		calendars:   d.Calendars,
		events:      d.Events,
		managed:     d.Managed,
		calendarSvc: d.CalendarSvc,
		clock:       d.Clock,
		logger:      logger,
	}
}

var _ port.AutomationService = (*AutomationService)(nil)

// RunAutomation runs one pass for every user with automation enabled
// (CalendarPrefsRepo.ListAutomated — never a full-table scan). Users fail
// independently: one broken user must not stall the fleet, matching the
// syncAllAccounts discipline.
func (s *AutomationService) RunAutomation(ctx context.Context) error {
	users, err := s.prefs.ListAutomated(ctx)
	if err != nil {
		return fmt.Errorf("automation: list automated users: %w", err)
	}
	for _, prefs := range users {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.runUser(ctx, prefs); err != nil {
			s.logger.Error("automation: user pass failed",
				"user_id", prefs.UserID, "error", err)
		}
	}
	return nil
}

func (s *AutomationService) runUser(ctx context.Context, prefs domain.CalendarPrefs) error {
	if prefs.FocusGoalMinutesPerWeek > 0 {
		if err := s.runFocusGuard(ctx, prefs); err != nil {
			return fmt.Errorf("focusguard: %w", err)
		}
	}
	if prefs.FocusAutoDecline || prefs.OOOAutoDecline {
		if err := s.runAutoDecline(ctx, prefs); err != nil {
			return fmt.Errorf("autodecline: %w", err)
		}
	}
	if prefs.AutoBufferMinutes > 0 {
		if err := s.runBuffers(ctx, prefs); err != nil {
			return fmt.Errorf("buffers: %w", err)
		}
	}
	// Tasks 12 and 14 add travel buffers and ICS
	// subscription refresh here.
	return nil
}

// focusPlanWeeks plans the current and next week, so Monday-morning runs
// still protect the week under way and Friday runs pre-fill the next.
const focusPlanWeeks = 2

func (s *AutomationService) runFocusGuard(ctx context.Context, prefs domain.CalendarPrefs) error {
	loc, err := time.LoadLocation(prefs.TimeZone)
	if err != nil {
		loc = time.UTC
	}
	now := s.clock.Now()

	cals, err := s.calendars.ListByUser(ctx, prefs.UserID)
	if err != nil {
		return err
	}
	var target *domain.Calendar
	calendarIDs := make([]string, 0, len(cals))
	for i, c := range cals {
		calendarIDs = append(calendarIDs, c.ID)
		if target == nil && c.IsPrimary && c.CanWrite {
			target = &cals[i]
		}
	}
	if target == nil {
		return errors.New("no writable primary calendar")
	}
	emails, err := s.calendarEmails(ctx, cals)
	if err != nil {
		return err
	}

	managed, err := s.managed.ListByUser(ctx, prefs.UserID, domain.ManagedFocus)
	if err != nil {
		return err
	}
	managedIDs := make(map[string]bool, len(managed))
	for _, m := range managed {
		managedIDs[m.EventID] = true
	}

	weekStart := startOfWeek(now, loc)
	for w := 0; w < focusPlanWeeks; w++ {
		ws := weekStart.AddDate(0, 0, 7*w) // wall-clock add: DST-safe
		we := ws.AddDate(0, 0, 7)
		// Every calendar explicitly: the is_visible display preference must
		// not hide busy time from the planner (ListInRange defaults to
		// visible-only when no ids are given).
		events, err := s.events.ListInRange(ctx, prefs.UserID, ws, we, calendarIDs)
		if err != nil {
			return err
		}
		// Declined invites hold no time (Availability's rule) — and must not
		// evict focus blocks, or the auto-decline pass would be undone by the
		// very next planning run.
		events = filterDeclined(events, emails)
		var pending map[string]bool
		if prefs.FocusAutoDecline {
			pending = pendingInvites(events, emails)
		}
		plan := planFocusWeek(now, ws, prefs, events, managedIDs, pending)
		for _, id := range plan.remove {
			// Provider-first (honesty policy): the tag is only forgotten
			// once the real event is gone. ErrNotFound means it already is.
			if err := s.calendarSvc.DeleteEvent(ctx, prefs.UserID, id); err != nil && !errors.Is(err, domain.ErrNotFound) {
				return fmt.Errorf("delete focus block %s: %w", id, err)
			}
			if err := s.managed.Delete(ctx, id); err != nil {
				return err
			}
		}
		for _, in := range plan.create {
			in.CalendarID = target.ID
			ev, err := s.calendarSvc.CreateEvent(ctx, prefs.UserID, in)
			if err != nil {
				// Provider write failed: nothing local changed, nothing to tag.
				return fmt.Errorf("create focus block: %w", err)
			}
			week := ws
			if err := s.managed.Create(ctx, domain.ManagedEvent{
				EventID:   ev.ID,
				UserID:    prefs.UserID,
				Kind:      domain.ManagedFocus,
				WeekStart: &week,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// startOfWeek returns Monday 00:00 of the week containing t, in loc.
func startOfWeek(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	back := (int(t.Weekday()) + 6) % 7 // Monday-based weekday index
	y, m, d := t.AddDate(0, 0, -back).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

// --- Auto-decline (M2.8 Task 7) ----------------------------------------------

// defaultFocusDeclineMessage is sent with focus declines when the user has
// not written their own message.
const defaultFocusDeclineMessage = "Declined automatically: this time is held for focus work. Please pick another slot."

// declineScanDays bounds the decline pass's look-ahead. Focus blocks only
// exist for the current and next week; OOO periods further out are handled
// by later runs as they enter the window.
const declineScanDays = 28

// oooDeclineMinLead: an already-accepted meeting inside an OOO period is only
// auto-declined when it starts at least this far in the future — the engine
// never silently no-shows a same-day commitment.
const oooDeclineMinLead = 24 * time.Hour

// runAutoDecline is the decline pass (runs after focus planning): pending
// invites overlapping a managed focus block (FocusAutoDecline) or an OOO
// period (OOOAutoDecline) are declined through CalendarService.RSVP with the
// user's custom message — provider-first, so on provider failure the mirror
// keeps saying needs_action and the next pass retries. Events the user
// organizes or has already responded to are never touched; idempotency comes
// from the RSVP state itself (declined attendees are never re-processed).
func (s *AutomationService) runAutoDecline(ctx context.Context, prefs domain.CalendarPrefs) error {
	now := s.clock.Now()
	cals, err := s.calendars.ListByUser(ctx, prefs.UserID)
	if err != nil {
		return err
	}
	emails, err := s.calendarEmails(ctx, cals)
	if err != nil {
		return err
	}
	calendarIDs := make([]string, 0, len(cals))
	for _, c := range cals {
		calendarIDs = append(calendarIDs, c.ID)
	}
	events, err := s.events.ListInRange(ctx, prefs.UserID, now, now.AddDate(0, 0, declineScanDays), calendarIDs)
	if err != nil {
		return err
	}
	managedFocus, err := s.managed.ListByUser(ctx, prefs.UserID, domain.ManagedFocus)
	if err != nil {
		return err
	}
	managedIDs := make(map[string]bool, len(managedFocus))
	for _, m := range managedFocus {
		managedIDs[m.EventID] = true
	}

	// Protected periods. OOO periods are the user's own announcements — an
	// incoming invite that merely mentions vacation in its title is not one.
	var focusSpans, oooSpans []span
	for _, ev := range events {
		if ev.Status == domain.EventCancelled {
			continue
		}
		if prefs.FocusAutoDecline && managedIDs[ev.ID] {
			focusSpans = append(focusSpans, span{ev.Start, ev.End})
		}
		if prefs.OOOAutoDecline && domain.IsOOOEvent(ev) {
			if self, ok := selfAttendee(ev, emails[ev.CalendarID]); !ok || self.Organizer {
				oooSpans = append(oooSpans, span{ev.Start, ev.End})
			}
		}
	}
	if len(focusSpans) == 0 && len(oooSpans) == 0 {
		return nil
	}

	var errs []error
	for _, ev := range events {
		if ev.Status == domain.EventCancelled || managedIDs[ev.ID] {
			continue
		}
		self, ok := selfAttendee(ev, emails[ev.CalendarID])
		if !ok || self.Organizer {
			continue // not an incoming invite: never decline the user's own events
		}
		var message string
		switch {
		case self.Response == domain.RsvpNeedsAction &&
			overlapsAnySpan(ev.Start, ev.End, focusSpans):
			message = prefs.FocusDeclineMessage
			if message == "" {
				message = defaultFocusDeclineMessage
			}
		case self.Response == domain.RsvpNeedsAction &&
			overlapsAnySpan(ev.Start, ev.End, oooSpans):
			message = prefs.OOODeclineMessage
		case self.Response == domain.RsvpAccepted && prefs.OOODeclineMessage != "" &&
			!ev.Start.Before(now.Add(oooDeclineMinLead)) &&
			overlapsAnySpan(ev.Start, ev.End, oooSpans):
			// An accepted meeting inside a (new) OOO period: declined only
			// with an explicit message and ≥24h of notice.
			message = prefs.OOODeclineMessage
		default:
			continue
		}
		// Provider-first honesty: on failure nothing local changes; log,
		// continue with the other events, and let the next pass retry.
		if _, err := s.calendarSvc.RSVP(ctx, prefs.UserID, ev.ID, domain.RsvpDeclined, message); err != nil {
			errs = append(errs, fmt.Errorf("decline event %s: %w", ev.ID, err))
		}
	}
	return errors.Join(errs...)
}

// calendarEmails resolves each calendar to its connected account's email —
// the identity the user appears under in that calendar's attendee lists.
func (s *AutomationService) calendarEmails(ctx context.Context, cals []domain.Calendar) (map[string]string, error) {
	byAccount := map[string]string{}
	out := make(map[string]string, len(cals))
	for _, c := range cals {
		email, ok := byAccount[c.AccountID]
		if !ok {
			acct, err := s.accounts.GetByID(ctx, c.AccountID)
			if err != nil {
				return nil, fmt.Errorf("resolve account %s: %w", c.AccountID, err)
			}
			email = acct.Email
			byAccount[c.AccountID] = email
		}
		out[c.ID] = email
	}
	return out, nil
}

// selfAttendee returns the user's own attendee entry on ev, matched by the
// owning account's email.
func selfAttendee(ev domain.Event, email string) (domain.Attendee, bool) {
	if email == "" {
		return domain.Attendee{}, false
	}
	for _, a := range ev.Attendees {
		if strings.EqualFold(a.Email, email) {
			return a, true
		}
	}
	return domain.Attendee{}, false
}

// filterDeclined drops invites the user has declined.
func filterDeclined(events []domain.Event, emails map[string]string) []domain.Event {
	out := events[:0]
	for _, ev := range events {
		if self, ok := selfAttendee(ev, emails[ev.CalendarID]); ok && !self.Organizer && self.Response == domain.RsvpDeclined {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// pendingInvites identifies unanswered incoming invites — events where the
// user's own entry is needs_action and someone else organizes.
func pendingInvites(events []domain.Event, emails map[string]string) map[string]bool {
	out := map[string]bool{}
	for _, ev := range events {
		if self, ok := selfAttendee(ev, emails[ev.CalendarID]); ok && !self.Organizer && self.Response == domain.RsvpNeedsAction {
			out[ev.ID] = true
		}
	}
	return out
}
