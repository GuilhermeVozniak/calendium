package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"calendium/backend/internal/domain"
)

const (
	// travelLookahead bounds the travel pass to upcoming events.
	travelLookahead = 48 * time.Hour
	// minTravelBuffer: shorter trips get no buffer and no alert.
	minTravelBuffer = 10 * time.Minute
	// maxTravelBuffer caps a single travel block.
	maxTravelBuffer = 3 * time.Hour
	// leaveAlertLead is how long before the travel block starts the
	// leave-now alert fires: leaveAt = eventStart - travel - 5min.
	leaveAlertLead = 5 * time.Minute
)

// BlockAction is what the planner wants done with an event's travel block.
type BlockAction string

const (
	BlockCreate  BlockAction = "create"  // no owned travel block exists yet
	BlockRefresh BlockAction = "refresh" // owned block exists at a stale window (event moved)
	BlockKeep    BlockAction = "keep"    // owned block already matches; no write needed
)

// TravelWindow is an existing managed travel block's [Start, End) span.
type TravelWindow struct {
	Start time.Time
	End   time.Time
}

// TravelPlan is one computed travel buffer for an upcoming located event:
// a "Travel to <Location>" block spanning [Start, End) = [eventStart-travel,
// eventStart), plus the leave-now alert time.
type TravelPlan struct {
	EventID  string
	Location string
	Start    time.Time
	End      time.Time
	LeaveAt  time.Time
	Action   BlockAction
}

// =============================================================================
// Managed travel blocks (M2.8 Task 12, wired through the Task 6 engine)
// =============================================================================
//
// Travel buffer blocks are MANAGED provider events ("Travel to <location>"):
// real calendar events created through CalendarService's write-through path
// (provider-first — on provider failure nothing local changes) and tagged in
// managed_events with kind 'travel', keyed to the event they lead to via
// SourceEventID. The travel pass runs INSIDE AutomationService.runUser — the
// single loop that owns every managed-events write — so the focus and buffer
// planners always see a settled managed set (no cross-loop write races).
// Leave-now alert DELIVERY stays on the worker's 5s due-work loop
// (SyncService.fireTravelAlerts); this pass only plans blocks + arms alerts.

// travelTimeFunc estimates door-to-door travel duration (MapsProvider.
// TravelTime with ctx bound by the caller — keeps planTravel pure/testable).
type travelTimeFunc func(fromLat, fromLon, toLat, toLon float64, mode domain.TravelMode) (time.Duration, error)

// planTravel is the pure travel planner. For each upcoming non-managed,
// non-cancelled, timed event with coordinates it computes the travel buffer
// [eventStart-travel, eventStart): origin is the end-location of the
// previous located event that same local day (prefs.TimeZone), else home.
// Trips under 10 minutes are skipped entirely (no block, no alert); travel
// is capped at 3 hours. Vendor errors skip that event and are joined into
// the returned error — remaining events still plan.
func planTravel(prefs domain.CalendarPrefs, events []domain.Event, managed map[string]struct{}, blocks map[string]TravelWindow, travelTime travelTimeFunc) ([]TravelPlan, error) {
	if prefs.HomeLat == nil || prefs.HomeLon == nil {
		return nil, nil // travel needs a home base; prefs validation keeps the pair together
	}
	loc, err := time.LoadLocation(prefs.TimeZone)
	if err != nil {
		loc = time.UTC
	}

	// Candidates: located, non-managed, non-cancelled, timed events by start.
	cands := make([]domain.Event, 0, len(events))
	for _, ev := range events {
		if _, isManaged := managed[ev.ID]; isManaged {
			continue
		}
		if ev.LocationLat == nil || ev.LocationLon == nil {
			continue // free-typed or cleared location: no trustworthy geo
		}
		if ev.AllDay || ev.Status == domain.EventCancelled {
			continue
		}
		cands = append(cands, ev)
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].Start.Before(cands[j].Start) })

	var plans []TravelPlan
	var errs []error
	for i, ev := range cands {
		// Origin: end-location of the latest earlier located event that ends
		// by this event's start on the same local day; home otherwise.
		fromLat, fromLon := *prefs.HomeLat, *prefs.HomeLon
		for j := i - 1; j >= 0; j-- {
			prev := cands[j]
			if prev.End.After(ev.Start) {
				continue // overlapping event can't be a departure point
			}
			if !sameLocalDay(prev.Start, ev.Start, loc) {
				break // candidates are start-sorted: nothing earlier is same-day either
			}
			fromLat, fromLon = *prev.LocationLat, *prev.LocationLon
			break
		}

		travel, err := travelTime(fromLat, fromLon, *ev.LocationLat, *ev.LocationLon, prefs.TravelMode)
		if err != nil {
			errs = append(errs, fmt.Errorf("travel time for event %s: %w", ev.ID, err))
			continue
		}
		if travel < minTravelBuffer {
			continue
		}
		if travel > maxTravelBuffer {
			travel = maxTravelBuffer
		}

		start := ev.Start.Add(-travel)
		plan := TravelPlan{
			EventID:  ev.ID,
			Location: eventLocationLabel(ev),
			Start:    start,
			End:      ev.Start,
			LeaveAt:  start.Add(-leaveAlertLead),
			Action:   BlockCreate,
		}
		if w, ok := blocks[ev.ID]; ok {
			if w.Start.Equal(plan.Start) && w.End.Equal(plan.End) {
				plan.Action = BlockKeep
			} else {
				plan.Action = BlockRefresh // event moved: refresh block + re-arm alert
			}
		}
		plans = append(plans, plan)
	}
	return plans, errors.Join(errs...)
}

func sameLocalDay(a, b time.Time, loc *time.Location) bool {
	ay, am, ad := a.In(loc).Date()
	by, bm, bd := b.In(loc).Date()
	return ay == by && am == bm && ad == bd
}

func eventLocationLabel(ev domain.Event) string {
	if ev.Location != nil && *ev.Location != "" {
		return *ev.Location
	}
	return ev.Title
}

// travelBlockTitle names the managed travel block for a plan.
func travelBlockTitle(location string) string { return "Travel to " + location }

// runTravel is the travel step of AutomationService.runUser (M2.8 Task 12):
// it plans travel buffers for the user's upcoming located events, creates or
// moves the managed "Travel to <location>" blocks provider-first, removes
// blocks whose source event no longer plans (deleted, cancelled, coordinates
// cleared, or moved out of the window), and arms leave-now alerts. It only
// ever writes events tagged in managed_events with kind 'travel' — never a
// user's own event.
func (s *AutomationService) runTravel(ctx context.Context, prefs domain.CalendarPrefs) error {
	if s.maps == nil || !prefs.TravelBuffers || prefs.HomeLat == nil || prefs.HomeLon == nil {
		return nil // maps unconfigured or travel off: degrade silently
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
	// Without a writable primary calendar there is nowhere to put blocks; the
	// pass still plans and arms leave alerts (alerts never needed a calendar).
	var listIDs []string
	if len(calendarIDs) > 0 {
		listIDs = calendarIDs // every calendar explicitly: visibility prefs must not hide busy time
	}
	events, err := s.events.ListInRange(ctx, prefs.UserID, now, now.Add(travelLookahead), listIDs)
	if err != nil {
		return err
	}
	byID := make(map[string]domain.Event, len(events))
	for _, ev := range events {
		byID[ev.ID] = ev
	}

	// Managed-skip set: EVERY managed kind, so no automation-owned event ever
	// gets a travel buffer of its own (no travel-to-travel, no buffering a
	// focus block) and none is used as a chain origin.
	managedSet := map[string]struct{}{}
	for _, kind := range []domain.ManagedKind{domain.ManagedFocus, domain.ManagedBuffer, domain.ManagedTravel} {
		list, err := s.managed.ListByUser(ctx, prefs.UserID, kind)
		if err != nil {
			return err
		}
		for _, m := range list {
			managedSet[m.EventID] = struct{}{}
		}
	}

	// Owned travel blocks keyed by the event they lead to, windows joined
	// from the mirrored block events (move detection for planTravel).
	ownedTravel, err := s.managed.ListByUser(ctx, prefs.UserID, domain.ManagedTravel)
	if err != nil {
		return err
	}
	blocks := map[string]TravelWindow{} // source event id -> block window
	blockIDs := map[string]string{}     // source event id -> block event id
	for _, m := range ownedTravel {
		if m.SourceEventID == nil {
			continue // defensive: a travel tag without a target is inert
		}
		bev, ok := byID[m.EventID]
		if !ok {
			var gerr error
			bev, gerr = s.events.GetByID(ctx, m.EventID)
			if errors.Is(gerr, domain.ErrNotFound) {
				// Mirror row gone (the tag normally cascades with it): forget.
				if derr := s.managed.Delete(ctx, m.EventID); derr != nil {
					return derr
				}
				continue
			}
			if gerr != nil {
				return gerr
			}
		}
		blocks[*m.SourceEventID] = TravelWindow{Start: bev.Start, End: bev.End}
		blockIDs[*m.SourceEventID] = m.EventID
	}

	plans, planErr := planTravel(prefs, events, managedSet, blocks, func(fromLat, fromLon, toLat, toLon float64, mode domain.TravelMode) (time.Duration, error) {
		return s.maps.TravelTime(ctx, fromLat, fromLon, toLat, toLon, mode)
	})
	errs := []error{planErr}
	desired := make(map[string]struct{}, len(plans))
	for _, plan := range plans {
		desired[plan.EventID] = struct{}{}
		switch {
		case plan.Action == BlockCreate && target != nil:
			// Provider-first (honesty policy): the tag is written only after
			// the real event exists; on provider failure nothing local changes.
			in := domain.EventInput{
				CalendarID:      target.ID,
				Title:           travelBlockTitle(plan.Location),
				Start:           plan.Start,
				End:             plan.End,
				ReminderMinutes: []int{},
			}
			ev, err := s.calendarSvc.CreateEvent(ctx, prefs.UserID, in)
			if err != nil {
				errs = append(errs, fmt.Errorf("create travel block for event %s: %w", plan.EventID, err))
				continue // the leave alert still arms below — alerts never depended on blocks
			}
			src := plan.EventID
			if err := s.managed.Create(ctx, domain.ManagedEvent{
				EventID:       ev.ID,
				UserID:        prefs.UserID,
				Kind:          domain.ManagedTravel,
				SourceEventID: &src,
			}); err != nil {
				errs = append(errs, err)
			}
		case plan.Action == BlockRefresh:
			// The event moved: move the engine's OWN block to the recomputed
			// window (never the user's event).
			title := travelBlockTitle(plan.Location)
			start, end := plan.Start, plan.End
			if _, err := s.calendarSvc.UpdateEvent(ctx, prefs.UserID, blockIDs[plan.EventID], domain.EventPatch{
				Title: &title,
				Start: &start,
				End:   &end,
			}); err != nil {
				errs = append(errs, fmt.Errorf("move travel block for event %s: %w", plan.EventID, err))
			}
		}
	}

	// Arm leave-now alerts; delivery rides the worker's 5s due-work loop. A
	// failed provider block write must not lose the "time to leave" push.
	if prefs.LeaveAlerts && s.alerts != nil {
		for _, plan := range plans {
			if err := s.alerts.Upsert(ctx, plan.EventID, prefs.UserID, plan.LeaveAt); err != nil {
				errs = append(errs, fmt.Errorf("upsert travel alert for event %s: %w", plan.EventID, err))
			}
		}
	}

	// Reconcile away stale blocks: an owned FUTURE travel block whose source
	// event no longer plans is removed provider-first (the tag is forgotten
	// only once the real event is gone; ErrNotFound means it already is).
	// Past blocks are history and stay.
	for srcID, blockID := range blockIDs {
		if _, ok := desired[srcID]; ok {
			continue
		}
		if w := blocks[srcID]; !w.End.After(now) {
			continue
		}
		if err := s.calendarSvc.DeleteEvent(ctx, prefs.UserID, blockID); err != nil && !errors.Is(err, domain.ErrNotFound) {
			errs = append(errs, fmt.Errorf("delete travel block %s: %w", blockID, err))
			continue
		}
		if err := s.managed.Delete(ctx, blockID); err != nil {
			errs = append(errs, err)
			continue
		}
		if s.alerts != nil {
			if err := s.alerts.Delete(ctx, srcID); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
