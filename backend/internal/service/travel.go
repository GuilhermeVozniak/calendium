package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
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
// ============ TASK 6 INTEGRATION SEAM — MANAGED TRAVEL BLOCKS ================
// =============================================================================
//
// Travel buffer blocks are MANAGED provider events ("Travel to <location>"),
// owned by the Task 6 managed-events infrastructure, which is NOT on this
// branch yet. Everything provider-facing therefore goes through this seam and
// nothing else: with TravelServiceDeps.Blocks nil (today's wiring),
// RunTravelPass plans with an empty managed-set / no existing blocks, arms
// leave-now alerts (fully functional), and performs NO provider writes.
//
// Exact wiring points for the Task 6 integrator:
//  1. Implement TravelBlockStore on the managed-events service and pass it as
//     TravelServiceDeps.Blocks in cmd/worker/main.go (currently nil there).
//  2. ManagedEventIDs feeds planTravel's managed-skip set — back it with the
//     managed_events table (kind "travel" at minimum; include every managed
//     kind so no managed event ever gets a travel buffer of its own).
//  3. OwnedTravelBlocks feeds move-detection, keyed by TARGET event id: the
//     planner compares each block to [eventStart-travel, eventStart) and
//     emits Action refresh when the event moved, keep when it matches.
//  4. EnsureTravelBlock receives every plan whose Action is create or
//     refresh and must create/move the managed provider event at
//     [plan.Start, plan.End) titled "Travel to <plan.Location>", and reflect
//     it in managed_events so (2) and (3) see it on the next pass.
type TravelBlockStore interface {
	// ManagedEventIDs returns the ids of the user's managed events — the
	// planner never buffers a managed event (no travel-to-travel).
	ManagedEventIDs(ctx context.Context, userID string) (map[string]struct{}, error)
	// OwnedTravelBlocks returns the user's existing managed travel blocks
	// keyed by the id of the event they lead to.
	OwnedTravelBlocks(ctx context.Context, userID string) (map[string]TravelWindow, error)
	// EnsureTravelBlock creates or moves the managed "Travel to <location>"
	// provider event for the plan.
	EnsureTravelBlock(ctx context.Context, userID string, plan TravelPlan) error
}

// =============================================================================
// End of Task 6 seam.
// =============================================================================

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

// TravelServiceDeps wires a TravelService. Maps may be nil (maps not
// configured): the whole pass then degrades silently — no vendor calls, no
// buffers, no alerts. Blocks is the Task 6 managed-events seam (see the
// banner above); nil until Task 6 merges.
type TravelServiceDeps struct {
	Subscriptions port.SubscriptionRepo
	Prefs         port.CalendarPrefsRepo
	Events        port.EventRepo
	Alerts        port.TravelAlertRepo
	Maps          port.MapsProvider
	Blocks        TravelBlockStore
	Clock         port.Clock
	Logger        *slog.Logger
	SelfHosted    bool
}

// TravelService implements port.TravelService: the recurring travel-buffer
// pass over every user with travel automation enabled.
type TravelService struct {
	ent    entitlement
	prefs  port.CalendarPrefsRepo
	events port.EventRepo
	alerts port.TravelAlertRepo
	maps   port.MapsProvider
	blocks TravelBlockStore
	clock  port.Clock
	logger *slog.Logger
}

var _ port.TravelService = (*TravelService)(nil)

func NewTravelService(d TravelServiceDeps) *TravelService {
	return &TravelService{
		ent:    entitlement{subs: d.Subscriptions, clock: d.Clock, selfHost: d.SelfHosted},
		prefs:  d.Prefs,
		events: d.Events,
		alerts: d.Alerts,
		maps:   d.Maps,
		blocks: d.Blocks,
		clock:  d.Clock,
		logger: d.Logger,
	}
}

// RunTravelPass plans travel buffers and arms leave-now alerts for every
// user with TravelBuffers enabled. Users fail independently (log +
// continue); a lapsed subscription skips the user silently.
func (s *TravelService) RunTravelPass(ctx context.Context) error {
	if s.maps == nil {
		return nil // maps unconfigured: travel features degrade silently
	}
	prefsList, err := s.prefs.ListAutomated(ctx)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	var errs []error
	for _, p := range prefsList {
		if err := s.runUserPass(ctx, p, now); err != nil {
			s.logError("travel pass", p.UserID, err)
			errs = append(errs, fmt.Errorf("travel pass user %s: %w", p.UserID, err))
		}
	}
	return errors.Join(errs...)
}

func (s *TravelService) runUserPass(ctx context.Context, p domain.CalendarPrefs, now time.Time) error {
	if !p.TravelBuffers || p.HomeLat == nil || p.HomeLon == nil {
		return nil
	}
	if err := s.ent.require(ctx, p.UserID); err != nil {
		if errors.Is(err, domain.ErrPaymentRequired) {
			return nil // lapsed subscription: automation simply pauses
		}
		return err
	}
	events, err := s.events.ListInRange(ctx, p.UserID, now, now.Add(travelLookahead), nil)
	if err != nil {
		return err
	}

	managed := map[string]struct{}{}
	blocks := map[string]TravelWindow{}
	if s.blocks != nil { // Task 6 seam — nil until managed events merge
		if managed, err = s.blocks.ManagedEventIDs(ctx, p.UserID); err != nil {
			return err
		}
		if blocks, err = s.blocks.OwnedTravelBlocks(ctx, p.UserID); err != nil {
			return err
		}
	}

	plans, planErr := planTravel(p, events, managed, blocks, func(fromLat, fromLon, toLat, toLon float64, mode domain.TravelMode) (time.Duration, error) {
		return s.maps.TravelTime(ctx, fromLat, fromLon, toLat, toLon, mode)
	})
	errs := []error{planErr}
	for _, plan := range plans {
		if s.blocks != nil && plan.Action != BlockKeep {
			if err := s.blocks.EnsureTravelBlock(ctx, p.UserID, plan); err != nil {
				errs = append(errs, fmt.Errorf("ensure travel block for event %s: %w", plan.EventID, err))
			}
		}
		if p.LeaveAlerts {
			if err := s.alerts.Upsert(ctx, plan.EventID, p.UserID, plan.LeaveAt); err != nil {
				errs = append(errs, fmt.Errorf("upsert travel alert for event %s: %w", plan.EventID, err))
			}
		}
	}
	return errors.Join(errs...)
}

func (s *TravelService) logError(op, userID string, err error) {
	if s.logger == nil {
		return
	}
	s.logger.Error(op, "user_id", userID, "error", err)
}
