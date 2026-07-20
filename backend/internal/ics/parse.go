package ics

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Calendar is one parsed ICS feed.
type Calendar struct {
	Name   string // X-WR-CALNAME, else ""
	Events []Event
}

// Event is one VEVENT.
type Event struct {
	UID          string
	Summary      string
	Description  string
	Location     string
	Start        time.Time
	End          time.Time // DTEND, else DTSTART+DURATION, else Start (+1d when AllDay)
	AllDay       bool
	RRule        string // raw RRULE line, "" when absent
	Status       string // CONFIRMED|TENTATIVE|CANCELLED (upper-cased)
	LastModified time.Time
}

// Parse reads a full ICS document. It is tolerant: a malformed VEVENT is
// dropped (with its UID in the returned []error slice via errors.Join),
// never the whole feed.
func Parse(r io.Reader) (Calendar, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return Calendar{}, fmt.Errorf("ics: read: %w", err)
	}

	var (
		cal   Calendar
		errs  []error
		stack []string               // open component names, upper-cased
		props map[string]contentLine // nil unless inside a VEVENT
	)
	for _, line := range unfold(data) {
		cl, ok := parseContentLine(line)
		if !ok {
			continue // unparseable line: skip, tolerant
		}
		switch cl.name {
		case "BEGIN":
			comp := strings.ToUpper(strings.TrimSpace(cl.value))
			if comp == "" {
				continue
			}
			stack = append(stack, comp)
			if comp == "VEVENT" && props == nil {
				props = map[string]contentLine{}
			}
		case "END":
			comp := strings.ToUpper(strings.TrimSpace(cl.value))
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i] == comp {
					stack = stack[:i]
					break
				}
			}
			if comp == "VEVENT" && props != nil {
				ev, evErr := buildEvent(props)
				if evErr != nil {
					errs = append(errs, evErr)
				} else {
					cal.Events = append(cal.Events, ev)
				}
				props = nil
			}
		default:
			top := ""
			if len(stack) > 0 {
				top = stack[len(stack)-1]
			}
			switch {
			case top == "VEVENT" && props != nil:
				props[cl.name] = cl
			case (top == "VCALENDAR" || top == "") && cl.name == "X-WR-CALNAME":
				cal.Name = unescapeText(cl.value)
			}
		}
	}
	if props != nil {
		errs = append(errs, fmt.Errorf("ics: unterminated VEVENT (uid %q) dropped", props["UID"].value))
	}
	return cal, errors.Join(errs...)
}

// buildEvent assembles one Event from collected VEVENT properties.
// A missing or unparseable DTSTART makes the event malformed; everything
// else degrades gracefully.
func buildEvent(props map[string]contentLine) (Event, error) {
	uid := unescapeText(props["UID"].value)
	ds, ok := props["DTSTART"]
	if !ok {
		return Event{}, fmt.Errorf("ics: VEVENT (uid %q) has no DTSTART, dropped", uid)
	}
	start, allDay, ok := parseDateTime(ds.value, ds.params)
	if !ok {
		return Event{}, fmt.Errorf("ics: VEVENT (uid %q) has invalid DTSTART %q, dropped", uid, ds.value)
	}

	ev := Event{
		UID:         uid,
		Summary:     unescapeText(props["SUMMARY"].value),
		Description: unescapeText(props["DESCRIPTION"].value),
		Location:    unescapeText(props["LOCATION"].value),
		Start:       start,
		AllDay:      allDay,
		RRule:       props["RRULE"].value,
		Status:      strings.ToUpper(strings.TrimSpace(props["STATUS"].value)),
	}
	if lm, ok := props["LAST-MODIFIED"]; ok {
		if t, _, ok := parseDateTime(lm.value, lm.params); ok {
			ev.LastModified = t
		}
	}

	ev.End = ev.Start
	switch {
	case hasParsableEnd(props, &ev):
		// End set by helper.
	case hasParsableDuration(props, &ev):
		// End set by helper.
	case allDay:
		ev.End = ev.Start.AddDate(0, 0, 1)
	}
	if ev.End.Before(ev.Start) {
		ev.End = ev.Start
	}
	return ev, nil
}

func hasParsableEnd(props map[string]contentLine, ev *Event) bool {
	de, ok := props["DTEND"]
	if !ok {
		return false
	}
	end, _, ok := parseDateTime(de.value, de.params)
	if !ok {
		return false
	}
	ev.End = end
	return true
}

func hasParsableDuration(props map[string]contentLine, ev *Event) bool {
	du, ok := props["DURATION"]
	if !ok {
		return false
	}
	d, ok := parseDuration(du.value)
	if !ok {
		return false
	}
	ev.End = ev.Start.Add(d)
	return true
}

// parseDateTime parses a DATE (§3.3.4) or DATE-TIME (§3.3.5) value.
// VALUE=DATE (or a bare 8-digit value) yields midnight UTC with allDay
// true. A trailing Z means UTC; otherwise a TZID parameter is resolved via
// time.LoadLocation with unknown zones falling back to UTC; floating times
// are treated as UTC.
func parseDateTime(value string, params map[string]string) (t time.Time, allDay, ok bool) {
	value = strings.TrimSpace(value)
	if params["VALUE"] == "DATE" || isBareDate(value) {
		t, err := time.Parse("20060102", value)
		if err != nil {
			return time.Time{}, false, false
		}
		return t, true, true
	}

	v := value
	utc := strings.HasSuffix(v, "Z")
	if utc {
		v = strings.TrimSuffix(v, "Z")
	}
	parsed, err := time.Parse("20060102T150405", v)
	if err != nil {
		return time.Time{}, false, false
	}
	loc := time.UTC
	if !utc {
		if tzid := params["TZID"]; tzid != "" {
			if l, err := time.LoadLocation(tzid); err == nil {
				loc = l
			}
		}
	}
	y, m, d := parsed.Date()
	hh, mm, ss := parsed.Clock()
	return time.Date(y, m, d, hh, mm, ss, 0, loc), false, true
}

func isBareDate(v string) bool {
	if len(v) != 8 {
		return false
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return false
		}
	}
	return true
}

// parseDuration parses the RFC 5545 DURATION subset [+-]P(nW | [nD][T[nH][nM][nS]]).
// At least one component is required; trailing garbage is rejected.
func parseDuration(s string) (time.Duration, bool) {
	neg := false
	switch {
	case strings.HasPrefix(s, "-"):
		neg = true
		s = s[1:]
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	}
	if !strings.HasPrefix(s, "P") {
		return 0, false
	}
	s = s[1:]

	var (
		total      time.Duration
		components int
		inTime     bool
	)
	for len(s) > 0 {
		if s[0] == 'T' {
			if inTime {
				return 0, false
			}
			inTime = true
			s = s[1:]
			continue
		}
		i := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == 0 || i >= len(s) {
			return 0, false // no digits, or digits without a unit
		}
		var n int64
		for _, c := range s[:i] {
			n = n*10 + int64(c-'0')
			if n > 1<<40 { // absurd duration: reject rather than overflow
				return 0, false
			}
		}
		unit := s[i]
		s = s[i+1:]
		var mult time.Duration
		switch {
		case unit == 'W' && !inTime:
			mult = 7 * 24 * time.Hour
		case unit == 'D' && !inTime:
			mult = 24 * time.Hour
		case unit == 'H' && inTime:
			mult = time.Hour
		case unit == 'M' && inTime:
			mult = time.Minute
		case unit == 'S' && inTime:
			mult = time.Second
		default:
			return 0, false
		}
		total += time.Duration(n) * mult
		components++
	}
	if components == 0 {
		return 0, false
	}
	if neg {
		total = -total
	}
	return total, true
}
