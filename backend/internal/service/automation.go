package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// AutomationServiceDeps wires the calendar automation engine (M2.8 Task 6;
// grows in Tasks 7, 8, 12, 14).
type AutomationServiceDeps struct {
	Prefs       port.CalendarPrefsRepo
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
	if prefs.AutoBufferMinutes > 0 {
		if err := s.runBuffers(ctx, prefs); err != nil {
			return fmt.Errorf("buffers: %w", err)
		}
	}
	// Tasks 7, 12, 14 add auto-decline, travel buffers, and ICS
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
		plan := planFocusWeek(now, ws, prefs, events, managedIDs)
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
