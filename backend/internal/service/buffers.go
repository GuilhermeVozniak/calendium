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
	bufferEventTitle = "Buffer"
	// bufferHorizonDays: the buffer pass plans over a rolling two-week window.
	bufferHorizonDays = 14
)

// bufferPlan is the delta runBuffers applies for one user — same shape as
// focusPlan.
type bufferPlan struct {
	create []bufferCreate // buffer events to insert
	remove []string       // managed buffer event ids whose source gap vanished
}

type bufferCreate struct {
	input         domain.EventInput // Title "Buffer", fills the tight gap after the meeting
	sourceEventID string            // the meeting the buffer trails
}

// planBuffers inserts a breather after every meeting (>= 2 listed humans,
// not cancelled, not all-day) that is followed by another meeting starting
// before its end + prefs.AutoBufferMinutes. Invariants:
//
//   - a buffer trails its source meeting at [meetingEnd, meetingEnd+N) but
//     never exceeds the true gap: it is clamped to the next meeting's start,
//     skipped entirely when the gap is already >= N, and — since a buffer
//     cannot occupy time that does not exist — skipped when meetings touch
//     or overlap (zero gap);
//   - buffers never stack: a buffer is not a meeting (owned blocks are
//     excluded up front, and buffers list no attendees anyway), so a buffer
//     never seeds a buffer of its own;
//   - buffers never cover time held by any real user event;
//   - owned buffers whose source meeting moved, whose following meeting
//     vanished, or whose gap no longer needs protecting are removed — the
//     only events the planner ever removes are the ones in owned;
//   - AutoBufferMinutes == 0 (off) yields the empty plan.
//
// Gap math runs on instants, never wall clocks, so DST transitions and day
// edges cannot stretch or shrink a buffer.
func planBuffers(prefs domain.CalendarPrefs, events []domain.Event, owned map[string]domain.ManagedEvent) bufferPlan {
	n := time.Duration(prefs.AutoBufferMinutes) * time.Minute
	if n <= 0 {
		return bufferPlan{}
	}

	// Partition: meetings, engine-owned buffer blocks, and the busy timeline
	// (every real user event, meeting or not — owned blocks excluded so the
	// planner's own output never perturbs the next pass).
	var meetings []domain.Event
	var ownedBuffers []domain.Event
	var busy []span
	for _, ev := range events {
		if ev.Status == domain.EventCancelled || ev.AllDay {
			continue
		}
		if _, ok := owned[ev.ID]; ok {
			ownedBuffers = append(ownedBuffers, ev)
			continue
		}
		busy = append(busy, span{ev.Start, ev.End})
		if len(ev.Attendees) >= 2 {
			meetings = append(meetings, ev)
		}
	}
	sort.Slice(meetings, func(i, j int) bool {
		if !meetings[i].Start.Equal(meetings[j].Start) {
			return meetings[i].Start.Before(meetings[j].Start)
		}
		return meetings[i].ID < meetings[j].ID
	})
	sort.Slice(busy, func(i, j int) bool { return busy[i].start.Before(busy[j].start) })

	// Desired buffers, one per source meeting, in chronological order.
	desired := map[string]span{}
	var sources []string
	var taken []span
	for i, m := range meetings {
		// First meeting starting at or after m's end (sorted by start).
		var next *domain.Event
		for j := i + 1; j < len(meetings); j++ {
			if !meetings[j].Start.Before(m.End) {
				next = &meetings[j]
				break
			}
		}
		if next == nil {
			continue // no following meeting
		}
		gap := next.Start.Sub(m.End)
		if gap <= 0 || gap >= n {
			continue // no room to insert, or no protection needed
		}
		b := span{m.End, next.Start} // clamped: never exceed the true gap
		if overlapsAnySpan(b.start, b.end, busy) {
			continue // the gap is already held by a real user event
		}
		if overlapsAnySpan(b.start, b.end, taken) {
			continue // co-terminal meetings want the same slot exactly once
		}
		desired[m.ID] = b
		sources = append(sources, m.ID)
		taken = append(taken, b)
	}

	// Reconcile owned buffers: keep exact matches, remove everything stale.
	sort.Slice(ownedBuffers, func(i, j int) bool {
		if !ownedBuffers[i].Start.Equal(ownedBuffers[j].Start) {
			return ownedBuffers[i].Start.Before(ownedBuffers[j].Start)
		}
		return ownedBuffers[i].ID < ownedBuffers[j].ID
	})
	var plan bufferPlan
	matched := map[string]bool{}
	for _, b := range ownedBuffers {
		tag := owned[b.ID]
		if tag.SourceEventID != nil && !matched[*tag.SourceEventID] {
			if want, ok := desired[*tag.SourceEventID]; ok && b.Start.Equal(want.start) && b.End.Equal(want.end) {
				matched[*tag.SourceEventID] = true
				continue
			}
		}
		plan.remove = append(plan.remove, b.ID)
	}
	for _, src := range sources {
		if matched[src] {
			continue
		}
		b := desired[src]
		plan.create = append(plan.create, bufferCreate{
			input: domain.EventInput{
				Title:           bufferEventTitle,
				Start:           b.start,
				End:             b.end,
				ReminderMinutes: []int{},
			},
			sourceEventID: src,
		})
	}
	return plan
}

// runBuffers plans and applies auto meeting buffers for one user over a
// rolling bufferHorizonDays window, writing to the primary writable
// calendar. Same discipline as runFocusGuard: provider-first writes, tags
// only after the real event exists, deletes forget the tag only once the
// real event is gone.
func (s *AutomationService) runBuffers(ctx context.Context, prefs domain.CalendarPrefs) error {
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

	managed, err := s.managed.ListByUser(ctx, prefs.UserID, domain.ManagedBuffer)
	if err != nil {
		return err
	}
	owned := make(map[string]domain.ManagedEvent, len(managed))
	for _, m := range managed {
		owned[m.EventID] = m
	}

	// Every calendar explicitly: the is_visible display preference must not
	// hide meetings from the planner (see runFocusGuard).
	events, err := s.events.ListInRange(ctx, prefs.UserID, now, now.AddDate(0, 0, bufferHorizonDays), calendarIDs)
	if err != nil {
		return err
	}

	plan := planBuffers(prefs, events, owned)
	for _, id := range plan.remove {
		// Provider-first (honesty policy): the tag is only forgotten once the
		// real event is gone. ErrNotFound means it already is.
		if err := s.calendarSvc.DeleteEvent(ctx, prefs.UserID, id); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("delete buffer %s: %w", id, err)
		}
		if err := s.managed.Delete(ctx, id); err != nil {
			return err
		}
	}
	for _, bc := range plan.create {
		in := bc.input
		in.CalendarID = target.ID
		ev, err := s.calendarSvc.CreateEvent(ctx, prefs.UserID, in)
		if err != nil {
			// Provider write failed: nothing local changed, nothing to tag.
			return fmt.Errorf("create buffer: %w", err)
		}
		src := bc.sourceEventID
		if err := s.managed.Create(ctx, domain.ManagedEvent{
			EventID:       ev.ID,
			UserID:        prefs.UserID,
			Kind:          domain.ManagedBuffer,
			SourceEventID: &src,
		}); err != nil {
			return err
		}
	}
	return nil
}
