package service

import (
	"sort"
	"time"

	"calendium/backend/internal/domain"
)

const (
	focusMinBlock   = time.Hour
	focusMaxBlock   = 3 * time.Hour
	focusEventTitle = "Focus time"
	// focusLeadTime: never plan a block starting sooner than this, so the
	// engine doesn't drop a focus block on top of "right now".
	focusLeadTime = 30 * time.Minute
)

// span is a half-open [start, end) interval on the timeline.
type span struct{ start, end time.Time }

func (s span) duration() time.Duration { return s.end.Sub(s.start) }

// focusPlan is the delta RunAutomation applies for one user-week.
type focusPlan struct {
	create []domain.EventInput // new focus blocks (CalendarID filled by caller)
	remove []string            // managed focus event ids now colliding with real events
}

// planFocusWeek plans focus blocks for the week starting weekStart (Monday
// 00:00 in prefs.TimeZone). events is every mirrored event overlapping the
// week; managedFocusIDs identifies which of them the engine owns.
//
// Invariants:
//   - existing user events are never moved or shortened;
//   - owned focus blocks that now collide with a real event are removed
//     (the user booked over them — the meeting wins, the plan refills);
//   - surviving focus time counts toward the goal;
//   - new blocks fill the largest remaining working-hour gaps first, are
//     clamped to [focusMinBlock, focusMaxBlock], and stop once the goal
//     is met or the week has no gaps left.
func planFocusWeek(
	now time.Time,
	weekStart time.Time,
	prefs domain.CalendarPrefs,
	events []domain.Event,
	managedFocusIDs map[string]bool,
) focusPlan {
	goal := time.Duration(prefs.FocusGoalMinutesPerWeek) * time.Minute
	if goal <= 0 {
		return focusPlan{}
	}
	loc, err := time.LoadLocation(prefs.TimeZone)
	if err != nil {
		loc = time.UTC
	}

	// Partition: real busy intervals vs owned focus blocks.
	var busy []span
	var owned []domain.Event
	for _, ev := range events {
		if ev.Status == domain.EventCancelled || ev.AllDay {
			continue
		}
		if managedFocusIDs[ev.ID] {
			owned = append(owned, ev)
			continue
		}
		busy = append(busy, span{ev.Start, ev.End})
	}
	sort.Slice(busy, func(i, j int) bool { return busy[i].start.Before(busy[j].start) })

	var plan focusPlan
	var credit time.Duration
	for _, f := range owned {
		if overlapsAnySpan(f.Start, f.End, busy) {
			plan.remove = append(plan.remove, f.ID) // user booked over it
			continue
		}
		credit += f.End.Sub(f.Start)
		busy = insertSpan(busy, span{f.Start, f.End}) // keeps gap math honest
	}
	if credit >= goal {
		return plan
	}

	// Candidate gaps: per remaining workday, working hours minus busy.
	earliest := now.Add(focusLeadTime)
	var gaps []span
	for d := 0; d < 7; d++ {
		day := weekStart.AddDate(0, 0, d).In(loc)
		if !isWorkDay(day.Weekday(), prefs.WorkDays) {
			continue
		}
		open := dayWindow(day, prefs.WorkdayStartMinutes, prefs.WorkdayEndMinutes, loc)
		if open.end.Before(earliest) {
			continue // day already past
		}
		if open.start.Before(earliest) {
			open.start = earliest
		}
		gaps = append(gaps, subtractBusy(open, busy)...)
	}
	// Largest gap first: fewer, longer deep-work blocks.
	sort.Slice(gaps, func(i, j int) bool {
		return gaps[i].duration() > gaps[j].duration()
	})

	for _, g := range gaps {
		if credit >= goal {
			break
		}
		length := g.duration()
		if length < focusMinBlock {
			continue
		}
		if length > focusMaxBlock {
			length = focusMaxBlock
		}
		if remaining := goal - credit; length > remaining && remaining >= focusMinBlock {
			length = remaining.Round(15 * time.Minute)
			// Rounding up must not spill past the gap or the block cap.
			if length > g.duration() {
				length = g.duration()
			}
			if length > focusMaxBlock {
				length = focusMaxBlock
			}
		}
		plan.create = append(plan.create, domain.EventInput{
			Title: focusEventTitle,
			Start: g.start,
			End:   g.start.Add(length),
		})
		credit += length
	}
	return plan
}

// overlapsAnySpan reports whether the half-open interval [start, end)
// intersects any span. Touching intervals ([9,10) vs [10,11)) do not overlap.
func overlapsAnySpan(start, end time.Time, spans []span) bool {
	for _, s := range spans {
		if start.Before(s.end) && s.start.Before(end) {
			return true
		}
	}
	return false
}

// insertSpan inserts s into spans keeping the slice sorted by start.
func insertSpan(spans []span, s span) []span {
	i := sort.Search(len(spans), func(i int) bool { return spans[i].start.After(s.start) })
	spans = append(spans, span{})
	copy(spans[i+1:], spans[i:])
	spans[i] = s
	return spans
}

// subtractBusy returns the sub-intervals of open not covered by any busy
// span. busy must be sorted by start; busy spans may overlap each other.
func subtractBusy(open span, busy []span) []span {
	out := []span{}
	cur := open.start
	for _, b := range busy {
		if !b.end.After(cur) {
			continue // entirely before the cursor
		}
		if !b.start.Before(open.end) {
			break // sorted: nothing further can overlap
		}
		if b.start.After(cur) {
			end := b.start
			if end.After(open.end) {
				end = open.end
			}
			out = append(out, span{cur, end})
		}
		if b.end.After(cur) {
			cur = b.end
		}
		if !cur.Before(open.end) {
			return out
		}
	}
	if cur.Before(open.end) {
		out = append(out, span{cur, open.end})
	}
	return out
}

// dayWindow returns the working-hours window of the calendar day containing
// day. DST-safe: the minutes are wall-clock offsets applied via time.Date,
// so 09:00 local stays 09:00 local on both sides of a transition.
func dayWindow(day time.Time, startMinutes, endMinutes int, loc *time.Location) span {
	y, m, d := day.In(loc).Date()
	return span{
		start: time.Date(y, m, d, 0, startMinutes, 0, 0, loc),
		end:   time.Date(y, m, d, 0, endMinutes, 0, 0, loc),
	}
}

func isWorkDay(d time.Weekday, workDays []time.Weekday) bool {
	for _, w := range workDays {
		if w == d {
			return true
		}
	}
	return false
}
