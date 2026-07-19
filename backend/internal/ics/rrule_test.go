package ics

import (
	"testing"
	"time"
)

func timedEvent(start time.Time, dur time.Duration, rrule string) Event {
	return Event{UID: "u", Summary: "s", Start: start, End: start.Add(dur), RRule: rrule}
}

func TestExpandNonRecurring(t *testing.T) {
	start := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	ev := timedEvent(start, time.Hour, "")

	got := Expand(ev, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC))
	if len(got) != 1 {
		t.Fatalf("in-window: got %d, want 1", len(got))
	}
	if !got[0].Start.Equal(start) || !got[0].End.Equal(start.Add(time.Hour)) {
		t.Errorf("occurrence = %v–%v", got[0].Start, got[0].End)
	}

	got = Expand(ev, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if len(got) != 0 {
		t.Fatalf("out-of-window: got %d, want 0", len(got))
	}

	// Straddling the window start still counts.
	got = Expand(ev, start.Add(30*time.Minute), start.Add(2*time.Hour))
	if len(got) != 1 {
		t.Fatalf("straddling: got %d, want 1", len(got))
	}
}

func TestExpandDailyCount(t *testing.T) {
	start := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	ev := timedEvent(start, time.Hour, "FREQ=DAILY;COUNT=5")

	got := Expand(ev, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	if len(got) != 5 {
		t.Fatalf("got %d occurrences, want 5", len(got))
	}
	if !got[4].Start.Equal(time.Date(2026, 7, 5, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("last start = %v, want July 5 09:00", got[4].Start)
	}
}

func TestExpandDailyCountFromDTSTARTInclusive(t *testing.T) {
	// COUNT is consumed from DTSTART even when the window starts later.
	start := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	ev := timedEvent(start, time.Hour, "FREQ=DAILY;COUNT=5")

	got := Expand(ev, time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	if len(got) != 3 {
		t.Fatalf("got %d occurrences, want 3 (Jul 3, 4, 5)", len(got))
	}
	if !got[0].Start.Equal(time.Date(2026, 7, 3, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("first start = %v, want July 3", got[0].Start)
	}
}

func TestExpandWeeklyByDayUntil(t *testing.T) {
	lisbon := mustLoadLocation(t, "Europe/Lisbon")
	start := time.Date(2026, 9, 1, 8, 0, 0, 0, lisbon) // a Tuesday
	ev := timedEvent(start, time.Hour, "FREQ=WEEKLY;BYDAY=TU,TH;UNTIL=20260930T230000Z")

	got := Expand(ev, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC))
	if len(got) != 9 {
		t.Fatalf("got %d occurrences, want 9 (TU/TH through Sep 29)", len(got))
	}
	if !got[0].Start.Equal(start) {
		t.Errorf("first = %v, want DTSTART", got[0].Start)
	}
	if !got[1].Start.Equal(time.Date(2026, 9, 3, 8, 0, 0, 0, lisbon)) {
		t.Errorf("second = %v, want Thu Sep 3 08:00 Lisbon", got[1].Start)
	}
	if !got[8].Start.Equal(time.Date(2026, 9, 29, 8, 0, 0, 0, lisbon)) {
		t.Errorf("last = %v, want Tue Sep 29 08:00 Lisbon", got[8].Start)
	}
}

func TestExpandWeeklyFixture(t *testing.T) {
	cal, err := parseFixture(t, "weekly.ics")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cal.Events) != 1 {
		t.Fatalf("got %d events, want 1", len(cal.Events))
	}
	got := Expand(cal.Events[0], time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	if len(got) != 9 {
		t.Fatalf("got %d occurrences, want 9", len(got))
	}
}

func TestExpandWeeklyNoByDay(t *testing.T) {
	start := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC) // Monday
	ev := timedEvent(start, 30*time.Minute, "FREQ=WEEKLY;COUNT=3")
	got := Expand(ev, start, start.AddDate(0, 2, 0))
	if len(got) != 3 {
		t.Fatalf("got %d, want 3", len(got))
	}
	if !got[2].Start.Equal(start.AddDate(0, 0, 14)) {
		t.Errorf("third = %v, want +14d", got[2].Start)
	}
}

func TestExpandMonthlyByStartDay(t *testing.T) {
	// Day 31: months without a 31st are skipped, never normalized into
	// the next month.
	start := time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC)
	ev := timedEvent(start, time.Hour, "FREQ=MONTHLY")

	got := Expand(ev, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))
	wantMonths := []time.Month{time.January, time.March, time.May, time.July, time.August, time.October, time.December}
	if len(got) != len(wantMonths) {
		t.Fatalf("got %d occurrences, want %d", len(got), len(wantMonths))
	}
	for i, m := range wantMonths {
		if got[i].Start.Month() != m || got[i].Start.Day() != 31 {
			t.Errorf("occurrence %d = %v, want day 31 of %v", i, got[i].Start, m)
		}
	}
}

func TestExpandYearlyAllDay(t *testing.T) {
	start := time.Date(2025, 12, 25, 0, 0, 0, 0, time.UTC)
	ev := Event{
		UID: "xmas", Summary: "Natal", AllDay: true,
		Start: start, End: start.AddDate(0, 0, 1),
		RRule: "FREQ=YEARLY",
	}
	got := Expand(ev, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC))
	if len(got) != 6 {
		t.Fatalf("got %d occurrences, want 6 (2025–2030)", len(got))
	}
	for i, oc := range got {
		if !oc.AllDay {
			t.Errorf("occurrence %d lost AllDay", i)
		}
		want := time.Date(2025+i, 12, 25, 0, 0, 0, 0, time.UTC)
		if !oc.Start.Equal(want) {
			t.Errorf("occurrence %d start = %v, want %v", i, oc.Start, want)
		}
		if !oc.End.Equal(want.AddDate(0, 0, 1)) {
			t.Errorf("occurrence %d end = %v, want +1d", i, oc.End)
		}
	}
}

func TestExpandInterval(t *testing.T) {
	start := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	ev := timedEvent(start, time.Hour, "FREQ=DAILY;INTERVAL=3")
	got := Expand(ev, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC))
	if len(got) != 4 {
		t.Fatalf("got %d, want 4 (Jul 1, 4, 7, 10)", len(got))
	}
	if !got[3].Start.Equal(time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("last = %v, want Jul 10", got[3].Start)
	}
}

func TestExpandCap1000(t *testing.T) {
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	ev := timedEvent(start, time.Hour, "FREQ=DAILY")
	got := Expand(ev, start, start.AddDate(10, 0, 0))
	if len(got) != 1000 {
		t.Fatalf("got %d occurrences, want exactly 1000 (safety valve)", len(got))
	}
}

func TestExpandWindowClipping(t *testing.T) {
	start := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	ev := timedEvent(start, time.Hour, "FREQ=DAILY")

	// Occurrence straddling `from` is included.
	got := Expand(ev, time.Date(2026, 7, 3, 9, 30, 0, 0, time.UTC), time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC))
	if len(got) != 1 {
		t.Fatalf("straddle-from: got %d, want 1", len(got))
	}
	if !got[0].Start.Equal(time.Date(2026, 7, 3, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("start = %v", got[0].Start)
	}

	// Occurrence starting exactly at `to` is excluded (half-open window).
	got = Expand(ev, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 3, 9, 0, 0, 0, time.UTC))
	if len(got) != 2 {
		t.Fatalf("half-open: got %d, want 2 (Jul 1, Jul 2)", len(got))
	}
}

func TestExpandUntilBeforeWindow(t *testing.T) {
	start := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	ev := timedEvent(start, time.Hour, "FREQ=DAILY;UNTIL=20260703T090000Z")
	got := Expand(ev, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if len(got) != 0 {
		t.Fatalf("got %d, want 0 (UNTIL before window)", len(got))
	}
	// UNTIL equal to an occurrence start includes that occurrence.
	got = Expand(ev, start, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	if len(got) != 3 {
		t.Fatalf("got %d, want 3 (Jul 1–3, UNTIL inclusive)", len(got))
	}
}

func TestExpandEmptyWindow(t *testing.T) {
	start := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	ev := timedEvent(start, time.Hour, "FREQ=DAILY")
	if got := Expand(ev, start, start); got != nil {
		t.Fatalf("to==from: got %v, want nil", got)
	}
	if got := Expand(ev, start, start.Add(-time.Hour)); got != nil {
		t.Fatalf("to<from: got %v, want nil", got)
	}
}

// TestExpandHostileRRules: malformed RRULE parts are skipped or the rule is
// treated as non-recurring — never a panic, never an infinite loop.
func TestExpandHostileRRules(t *testing.T) {
	start := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name  string
		rrule string
		want  int
	}{
		{"unsupported freq", "FREQ=SECONDLY;COUNT=1000000", 1},
		{"garbage", "%%%GARBAGE%%%", 1},
		{"empty freq", "FREQ=", 1},
		{"just semicolons", ";;;", 1},
		{"interval zero clamped", "FREQ=DAILY;INTERVAL=0;COUNT=3", 3},
		{"negative interval clamped", "FREQ=DAILY;INTERVAL=-5;COUNT=2", 2},
		{"bad count ignored", "FREQ=DAILY;COUNT=abc", 7},
		{"bad until ignored", "UNTIL=banana;FREQ=DAILY;COUNT=2", 2},
		{"unknown byday token skipped", "FREQ=WEEKLY;BYDAY=XX,WE", 1},
		{"huge interval no panic", "FREQ=DAILY;INTERVAL=1000000000", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := timedEvent(start, time.Hour, tc.rrule)
			got := Expand(ev, from, to)
			if len(got) != tc.want {
				t.Errorf("rrule %q: got %d occurrences, want %d", tc.rrule, len(got), tc.want)
			}
		})
	}
}
