package service

import (
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// Monday 2026-07-20 00:00 UTC — the planning week used by most cases.
var fgWeek = time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)

func fgPrefs(tz string, goalMinutes int) domain.CalendarPrefs {
	p := domain.DefaultCalendarPrefs("u1")
	p.TimeZone = tz
	p.FocusGoalMinutesPerWeek = goalMinutes
	return p
}

// fgEvent builds a confirmed timed event.
func fgEvent(id string, start, end time.Time) domain.Event {
	return domain.Event{ID: id, Start: start, End: end, Status: domain.EventConfirmed}
}

func fgDay(dayOffset int, hour, minute int) time.Time {
	return fgWeek.AddDate(0, 0, dayOffset).Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

func totalCreated(p focusPlan) time.Duration {
	var total time.Duration
	for _, in := range p.create {
		total += in.End.Sub(in.Start)
	}
	return total
}

func assertWithinWorkingHours(t *testing.T, p focusPlan, prefs domain.CalendarPrefs) {
	t.Helper()
	loc, err := time.LoadLocation(prefs.TimeZone)
	if err != nil {
		t.Fatalf("tz: %v", err)
	}
	for _, in := range p.create {
		if in.Title != focusEventTitle {
			t.Fatalf("block title = %q, want %q", in.Title, focusEventTitle)
		}
		if !in.End.After(in.Start) {
			t.Fatalf("empty block %v", in)
		}
		if d := in.End.Sub(in.Start); d < focusMinBlock || d > focusMaxBlock {
			t.Fatalf("block length %v outside [%v, %v]", d, focusMinBlock, focusMaxBlock)
		}
		day := dayWindow(in.Start, prefs.WorkdayStartMinutes, prefs.WorkdayEndMinutes, loc)
		if in.Start.Before(day.start) || in.End.After(day.end) {
			t.Fatalf("block %v-%v escapes working hours %v-%v", in.Start, in.End, day.start, day.end)
		}
		if !isWorkDay(in.Start.In(loc).Weekday(), prefs.WorkDays) {
			t.Fatalf("block on non-workday %v", in.Start)
		}
	}
}

func assertNoOverlapWithBusy(t *testing.T, p focusPlan, events []domain.Event, managed map[string]bool) {
	t.Helper()
	for _, in := range p.create {
		for _, ev := range events {
			if managed[ev.ID] || ev.Status == domain.EventCancelled || ev.AllDay {
				continue
			}
			if in.Start.Before(ev.End) && ev.Start.Before(in.End) {
				t.Fatalf("created block %v-%v overlaps user event %s %v-%v",
					in.Start, in.End, ev.ID, ev.Start, ev.End)
			}
		}
	}
}

func TestPlanFocusWeekEmptyWeekFillsGoalWithMaxBlocks(t *testing.T) {
	prefs := fgPrefs("UTC", 8*60)
	now := fgWeek.Add(-time.Hour)

	plan := planFocusWeek(now, fgWeek, prefs, nil, nil, nil)

	if len(plan.remove) != 0 {
		t.Fatalf("remove = %v, want none", plan.remove)
	}
	if got := totalCreated(plan); got != 8*time.Hour {
		t.Fatalf("total created = %v, want 8h", got)
	}
	if len(plan.create) != 3 { // 3h + 3h + 2h: largest-gap-first, max-size blocks
		t.Fatalf("created %d blocks, want 3 (3h+3h+2h): %+v", len(plan.create), plan.create)
	}
	assertWithinWorkingHours(t, plan, prefs)
}

func TestPlanFocusWeekPlacesAroundMeetings(t *testing.T) {
	prefs := fgPrefs("UTC", 120)
	now := fgWeek.Add(-time.Hour)
	events := []domain.Event{
		fgEvent("m1", fgDay(0, 9, 0), fgDay(0, 16, 30)), // Mon: only a 30m tail
		fgEvent("m2", fgDay(1, 9, 0), fgDay(1, 15, 0)),  // Tue: 2h tail
		fgEvent("m3", fgDay(2, 9, 0), fgDay(2, 17, 0)),
		fgEvent("m4", fgDay(3, 9, 0), fgDay(3, 17, 0)),
		fgEvent("m5", fgDay(4, 9, 0), fgDay(4, 17, 0)),
	}

	plan := planFocusWeek(now, fgWeek, prefs, events, nil, nil)

	if len(plan.create) != 1 {
		t.Fatalf("created %d blocks, want 1: %+v", len(plan.create), plan.create)
	}
	if got := plan.create[0]; !got.Start.Equal(fgDay(1, 15, 0)) || !got.End.Equal(fgDay(1, 17, 0)) {
		t.Fatalf("block = %v-%v, want Tue 15:00-17:00", got.Start, got.End)
	}
	assertNoOverlapWithBusy(t, plan, events, nil)
}

func TestPlanFocusWeekGoalMetBySurvivingBlocks(t *testing.T) {
	prefs := fgPrefs("UTC", 120)
	now := fgWeek.Add(-time.Hour)
	events := []domain.Event{fgEvent("f1", fgDay(2, 9, 0), fgDay(2, 11, 0))}
	managed := map[string]bool{"f1": true}

	plan := planFocusWeek(now, fgWeek, prefs, events, managed, nil)

	if len(plan.create) != 0 || len(plan.remove) != 0 {
		t.Fatalf("plan = %+v, want empty (goal already met)", plan)
	}
}

func TestPlanFocusWeekOwnedBlockOverbookedIsRemovedAndRefilled(t *testing.T) {
	prefs := fgPrefs("UTC", 60)
	now := fgWeek.Add(-time.Hour)
	events := []domain.Event{
		fgEvent("f1", fgDay(0, 9, 0), fgDay(0, 11, 0)),       // owned focus block
		fgEvent("meeting", fgDay(0, 10, 0), fgDay(0, 12, 0)), // user booked over it
	}
	managed := map[string]bool{"f1": true}

	plan := planFocusWeek(now, fgWeek, prefs, events, managed, nil)

	if len(plan.remove) != 1 || plan.remove[0] != "f1" {
		t.Fatalf("remove = %v, want [f1] — the meeting wins, never the reverse", plan.remove)
	}
	if len(plan.create) != 1 || totalCreated(plan) != time.Hour {
		t.Fatalf("create = %+v, want one 1h refill", plan.create)
	}
	assertNoOverlapWithBusy(t, plan, events, managed)
	assertWithinWorkingHours(t, plan, prefs)
}

func TestPlanFocusWeekNeverRemovesUserEvents(t *testing.T) {
	prefs := fgPrefs("UTC", 8*60)
	now := fgWeek.Add(-time.Hour)
	// A user event sitting exactly where a would-be focus block would start.
	events := []domain.Event{fgEvent("user1", fgDay(0, 9, 0), fgDay(0, 12, 0))}

	plan := planFocusWeek(now, fgWeek, prefs, events, nil, nil)

	if len(plan.remove) != 0 {
		t.Fatalf("remove = %v — the planner must never touch user events", plan.remove)
	}
	assertNoOverlapWithBusy(t, plan, events, nil)
}

func TestPlanFocusWeekMidWeekNowNeverPlansInThePast(t *testing.T) {
	prefs := fgPrefs("UTC", 8*60)
	now := fgDay(2, 16, 0) // Wednesday 16:00
	earliest := now.Add(focusLeadTime)

	plan := planFocusWeek(now, fgWeek, prefs, nil, nil, nil)

	if len(plan.create) != 2 || totalCreated(plan) != 6*time.Hour {
		t.Fatalf("plan = %+v, want two 3h blocks (Thu+Fri)", plan.create)
	}
	for _, in := range plan.create {
		if in.Start.Before(earliest) {
			t.Fatalf("block starts %v, before earliest plannable %v", in.Start, earliest)
		}
	}
}

func TestPlanFocusWeekSkipsNonWorkdays(t *testing.T) {
	prefs := fgPrefs("UTC", 10*60)
	prefs.WorkDays = []time.Weekday{time.Tuesday}
	now := fgWeek.Add(-time.Hour)

	plan := planFocusWeek(now, fgWeek, prefs, nil, nil, nil)

	if len(plan.create) != 1 {
		t.Fatalf("created %d blocks, want 1 (one gap, one block)", len(plan.create))
	}
	if wd := plan.create[0].Start.Weekday(); wd != time.Tuesday {
		t.Fatalf("block on %v, want Tuesday", wd)
	}
	assertWithinWorkingHours(t, plan, prefs)
}

func TestPlanFocusWeekIgnoresGapsUnderMinBlock(t *testing.T) {
	prefs := fgPrefs("UTC", 120)
	now := fgWeek.Add(-time.Hour)
	var events []domain.Event
	for d := 0; d < 5; d++ { // every workday leaves only a 45m tail
		events = append(events, fgEvent("m"+string(rune('a'+d)), fgDay(d, 9, 0), fgDay(d, 16, 15)))
	}

	plan := planFocusWeek(now, fgWeek, prefs, events, nil, nil)

	if len(plan.create) != 0 {
		t.Fatalf("create = %+v, want none (all gaps under 1h)", plan.create)
	}
}

func TestPlanFocusWeekRemainingGoalRoundsToQuarterHour(t *testing.T) {
	prefs := fgPrefs("UTC", 70)
	now := fgWeek.Add(-time.Hour)

	plan := planFocusWeek(now, fgWeek, prefs, nil, nil, nil)

	if len(plan.create) != 1 {
		t.Fatalf("created %d blocks, want 1", len(plan.create))
	}
	if got := totalCreated(plan); got != 75*time.Minute {
		t.Fatalf("block length = %v, want 75m (70m rounded to 15m)", got)
	}
}

func TestPlanFocusWeekIgnoresCancelledAndAllDayEvents(t *testing.T) {
	prefs := fgPrefs("UTC", 60)
	now := fgWeek.Add(-time.Hour)
	cancelled := fgEvent("c1", fgDay(0, 9, 0), fgDay(0, 17, 0))
	cancelled.Status = domain.EventCancelled
	allDay := fgEvent("a1", fgDay(1, 0, 0), fgDay(2, 0, 0))
	allDay.AllDay = true
	events := []domain.Event{cancelled, allDay}

	plan := planFocusWeek(now, fgWeek, prefs, events, nil, nil)

	if len(plan.create) != 1 || totalCreated(plan) != time.Hour {
		t.Fatalf("plan = %+v, want one 1h block (cancelled/all-day must not block)", plan.create)
	}
}

// TestPlanFocusWeekDSTTransition: Europe/Amsterdam springs forward Sunday
// 2026-03-29. Blocks must start at 09:00 *local wall clock* on both sides of
// the transition — 08:00 UTC before (CET, +01), 07:00 UTC after (CEST, +02).
func TestPlanFocusWeekDSTTransition(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatalf("tz: %v", err)
	}
	prefs := fgPrefs("Europe/Amsterdam", 60)

	for _, tc := range []struct {
		name      string
		weekStart time.Time
		wantUTC   time.Time
	}{
		{
			name:      "week before transition (CET +01)",
			weekStart: time.Date(2026, 3, 23, 0, 0, 0, 0, loc),
			wantUTC:   time.Date(2026, 3, 23, 8, 0, 0, 0, time.UTC),
		},
		{
			name:      "week after transition (CEST +02)",
			weekStart: time.Date(2026, 3, 30, 0, 0, 0, 0, loc),
			wantUTC:   time.Date(2026, 3, 30, 7, 0, 0, 0, time.UTC),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := tc.weekStart.Add(-time.Hour)
			plan := planFocusWeek(now, tc.weekStart, prefs, nil, nil, nil)
			if len(plan.create) != 1 {
				t.Fatalf("created %d blocks, want 1", len(plan.create))
			}
			got := plan.create[0].Start
			wantLocal := time.Date(tc.weekStart.Year(), tc.weekStart.Month(), tc.weekStart.Day(), 9, 0, 0, 0, loc)
			if !got.Equal(wantLocal) {
				t.Fatalf("block starts %v, want 09:00 local (%v)", got, wantLocal)
			}
			if !got.Equal(tc.wantUTC) {
				t.Fatalf("block instant = %v, want %v — DST offset not honored", got.UTC(), tc.wantUTC)
			}
		})
	}
}

// --- helper primitives -------------------------------------------------------

func TestSubtractBusy(t *testing.T) {
	h := func(hh, mm int) time.Time { return fgDay(0, hh, mm) }
	open := span{h(9, 0), h(17, 0)}

	cases := []struct {
		name string
		busy []span
		want []span
	}{
		{"no busy", nil, []span{open}},
		{"touching intervals leave no sliver", []span{{h(9, 0), h(10, 0)}, {h(10, 0), h(11, 0)}}, []span{{h(11, 0), h(17, 0)}}},
		{"busy touching end", []span{{h(16, 0), h(17, 0)}}, []span{{h(9, 0), h(16, 0)}}},
		{"containment swallows the window", []span{{h(8, 0), h(18, 0)}}, []span{}},
		{"busy inside splits the window", []span{{h(12, 0), h(13, 0)}}, []span{{h(9, 0), h(12, 0)}, {h(13, 0), h(17, 0)}}},
		{"busy overlapping start", []span{{h(8, 0), h(10, 0)}}, []span{{h(10, 0), h(17, 0)}}},
		{"busy overlapping end", []span{{h(16, 0), h(18, 0)}}, []span{{h(9, 0), h(16, 0)}}},
		{"overlapping busy spans merge", []span{{h(9, 0), h(12, 0)}, {h(11, 0), h(14, 0)}}, []span{{h(14, 0), h(17, 0)}}},
		{"busy entirely outside", []span{{h(6, 0), h(7, 0)}, {h(18, 0), h(19, 0)}}, []span{open}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := subtractBusy(open, tc.busy)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if !got[i].start.Equal(tc.want[i].start) || !got[i].end.Equal(tc.want[i].end) {
					t.Fatalf("gap %d = %v-%v, want %v-%v", i, got[i].start, got[i].end, tc.want[i].start, tc.want[i].end)
				}
			}
		})
	}
}

func TestInsertSpan(t *testing.T) {
	h := func(hh int) time.Time { return fgDay(0, hh, 0) }
	spans := []span{{h(9), h(10)}, {h(13), h(14)}}

	for _, tc := range []struct {
		name string
		in   span
		pos  int
	}{
		{"front", span{h(7), h(8)}, 0},
		{"middle", span{h(11), h(12)}, 1},
		{"end", span{h(15), h(16)}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := insertSpan(append([]span{}, spans...), tc.in)
			if len(got) != 3 {
				t.Fatalf("len = %d, want 3", len(got))
			}
			if !got[tc.pos].start.Equal(tc.in.start) {
				t.Fatalf("inserted at wrong position: %v", got)
			}
			for i := 1; i < len(got); i++ {
				if got[i].start.Before(got[i-1].start) {
					t.Fatalf("not sorted after insert: %v", got)
				}
			}
		})
	}
}

func TestOverlapsAnySpanTouchingIsNotOverlap(t *testing.T) {
	h := func(hh int) time.Time { return fgDay(0, hh, 0) }
	spans := []span{{h(10), h(11)}}
	if overlapsAnySpan(h(9), h(10), spans) {
		t.Fatal("[9,10) must not overlap [10,11)")
	}
	if overlapsAnySpan(h(11), h(12), spans) {
		t.Fatal("[11,12) must not overlap [10,11)")
	}
	if !overlapsAnySpan(h(10), h(12), spans) {
		t.Fatal("[10,12) must overlap [10,11)")
	}
}
