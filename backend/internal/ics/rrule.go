package ics

import (
	"strconv"
	"strings"
	"time"
)

const (
	// maxOccurrences bounds how many occurrences Expand returns.
	maxOccurrences = 1000
	// maxIterations bounds candidate generation so hostile rules can
	// never loop unbounded.
	maxIterations = 200_000
)

// rrule is the parsed subset of an RRULE value. Unknown parts are skipped.
type rrule struct {
	freq     string
	interval int
	count    int // 0 = unset
	until    time.Time
	hasUntil bool
	byday    []time.Weekday
}

var weekdayNames = map[string]time.Weekday{
	"SU": time.Sunday, "MO": time.Monday, "TU": time.Tuesday, "WE": time.Wednesday,
	"TH": time.Thursday, "FR": time.Friday, "SA": time.Saturday,
}

// parseRRule parses an RRULE value tolerantly: malformed parts are skipped,
// INTERVAL is clamped to >= 1, and unknown BYDAY tokens are dropped.
func parseRRule(s string) rrule {
	r := rrule{interval: 1}
	for _, part := range strings.Split(s, ";") {
		k, v, found := strings.Cut(part, "=")
		if !found {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.ToUpper(strings.TrimSpace(k)) {
		case "FREQ":
			r.freq = strings.ToUpper(v)
		case "INTERVAL":
			if n, err := strconv.Atoi(v); err == nil && n >= 1 {
				r.interval = n
			}
		case "COUNT":
			if n, err := strconv.Atoi(v); err == nil && n >= 1 {
				r.count = n
			}
		case "UNTIL":
			if t, ok := parseUntil(v); ok {
				r.until = t
				r.hasUntil = true
			}
		case "BYDAY":
			for _, tok := range strings.Split(v, ",") {
				if wd, ok := weekdayNames[strings.ToUpper(strings.TrimSpace(tok))]; ok {
					r.byday = append(r.byday, wd)
				}
			}
		}
	}
	return r
}

// parseUntil parses an UNTIL value as UTC: 20060102T150405Z (or without the
// Z, still treated as UTC) or a bare 20060102 date.
func parseUntil(v string) (time.Time, bool) {
	if isBareDate(v) {
		t, err := time.Parse("20060102", v)
		return t, err == nil
	}
	t, err := time.Parse("20060102T150405", strings.TrimSuffix(v, "Z"))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// Expand returns concrete occurrences of ev overlapping [from, to),
// expanding RRule when present (max 1000 occurrences as a safety valve).
// TZID zones resolve via time.LoadLocation; unknown zones fall back to UTC.
// COUNT is consumed from DTSTART inclusive, even for occurrences before
// from; UNTIL is compared in UTC against each occurrence start.
func Expand(ev Event, from, to time.Time) []Event {
	if !to.After(from) {
		return nil
	}
	r := parseRRule(ev.RRule)
	supported := r.freq == "DAILY" || r.freq == "WEEKLY" || r.freq == "MONTHLY" || r.freq == "YEARLY"
	if ev.RRule == "" || !supported {
		if overlaps(ev.Start, ev.End, from, to) {
			return []Event{ev}
		}
		return nil
	}

	dur := ev.End.Sub(ev.Start)
	var out []Event
	emitted := 0 // occurrences counted against COUNT (from DTSTART inclusive)

	// add records one occurrence candidate in chronological order and
	// reports whether expansion should continue.
	add := func(start time.Time) bool {
		if r.hasUntil && start.UTC().After(r.until) {
			return false
		}
		if !start.Before(to) {
			return false
		}
		emitted++
		if r.count > 0 && emitted > r.count {
			return false
		}
		end := start.Add(dur)
		if ev.AllDay {
			end = start.AddDate(0, 0, 1)
		}
		if overlaps(start, end, from, to) {
			oc := ev
			oc.Start = start
			oc.End = end
			out = append(out, oc)
			if len(out) >= maxOccurrences {
				return false
			}
		}
		return true
	}

	if r.freq == "WEEKLY" && len(r.byday) > 0 {
		expandWeeklyByDay(ev.Start, r, add)
		return out
	}

	for n := 0; n < maxIterations; n++ {
		var start time.Time
		switch r.freq {
		case "DAILY":
			start = ev.Start.AddDate(0, 0, n*r.interval)
		case "WEEKLY":
			start = ev.Start.AddDate(0, 0, 7*n*r.interval)
		case "MONTHLY":
			start = ev.Start.AddDate(0, n*r.interval, 0)
			if start.Day() != ev.Start.Day() {
				continue // month has no such day (e.g. Feb 31): skip, don't normalize
			}
		case "YEARLY":
			start = ev.Start.AddDate(n*r.interval, 0, 0)
			if start.Day() != ev.Start.Day() || start.Month() != ev.Start.Month() {
				continue // Feb 29 in a non-leap year: skip
			}
		}
		if !add(start) {
			break
		}
	}
	return out
}

// expandWeeklyByDay walks day by day from the start of DTSTART's week
// (WKST=MO default), emitting days whose weekday is in BYDAY and whose week
// index matches INTERVAL. DTSTART's own wall-clock time is preserved.
func expandWeeklyByDay(dtstart time.Time, r rrule, add func(time.Time) bool) {
	inSet := map[time.Weekday]bool{}
	for _, wd := range r.byday {
		inSet[wd] = true
	}
	mondayOffset := (int(dtstart.Weekday()) + 6) % 7 // Monday = 0
	anchor := dtstart.AddDate(0, 0, -mondayOffset)   // Monday of DTSTART's week
	for day := 0; day < maxIterations; day++ {
		if (day/7)%r.interval != 0 {
			continue
		}
		d := anchor.AddDate(0, 0, day)
		if d.Before(dtstart) || !inSet[d.Weekday()] {
			continue
		}
		if !add(d) {
			return
		}
	}
}

// overlaps reports whether [start, end) intersects [from, to). A
// zero-length event is treated as an instant that overlaps when it lies
// inside the window.
func overlaps(start, end, from, to time.Time) bool {
	if !end.After(start) {
		return !start.Before(from) && start.Before(to)
	}
	return start.Before(to) && end.After(from)
}
