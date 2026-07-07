package msgraph

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"calendium/backend/internal/domain"
)

// Graph models recurrence as a structured patternedRecurrence while the
// domain (and Google) speak RFC 5545 RRULE. This file converts the common
// shapes both ways; exotic rules (BYSETPOS, multiple BYMONTHDAY, ...) are
// out of scope for a mail/calendar mirror.

type graphRecurrence struct {
	Pattern struct {
		Type           string   `json:"type"` // daily|weekly|absoluteMonthly|relativeMonthly|absoluteYearly|relativeYearly
		Interval       int      `json:"interval"`
		DaysOfWeek     []string `json:"daysOfWeek"`
		DayOfMonth     int      `json:"dayOfMonth"`
		Month          int      `json:"month"`
		FirstDayOfWeek string   `json:"firstDayOfWeek"`
	} `json:"pattern"`
	Range struct {
		Type                string `json:"type"` // endDate|noEnd|numbered
		StartDate           string `json:"startDate"`
		EndDate             string `json:"endDate"`
		NumberOfOccurrences int    `json:"numberOfOccurrences"`
	} `json:"range"`
}

var (
	rruleDayToGraph = map[string]string{
		"MO": "monday", "TU": "tuesday", "WE": "wednesday", "TH": "thursday",
		"FR": "friday", "SA": "saturday", "SU": "sunday",
	}
	graphDayToRRule = map[string]string{
		"monday": "MO", "tuesday": "TU", "wednesday": "WE", "thursday": "TH",
		"friday": "FR", "saturday": "SA", "sunday": "SU",
	}
)

// graphRecurrenceToRRule renders a patternedRecurrence as an RRULE value
// (without the "RRULE:" prefix); "" when absent or unsupported.
func graphRecurrenceToRRule(r *graphRecurrence) string {
	if r == nil {
		return ""
	}
	var parts []string
	switch r.Pattern.Type {
	case "daily":
		parts = append(parts, "FREQ=DAILY")
	case "weekly":
		parts = append(parts, "FREQ=WEEKLY")
		if days := graphDays(r.Pattern.DaysOfWeek); days != "" {
			parts = append(parts, "BYDAY="+days)
		}
	case "absoluteMonthly":
		parts = append(parts, "FREQ=MONTHLY")
		if r.Pattern.DayOfMonth > 0 {
			parts = append(parts, "BYMONTHDAY="+strconv.Itoa(r.Pattern.DayOfMonth))
		}
	case "absoluteYearly":
		parts = append(parts, "FREQ=YEARLY")
		if r.Pattern.Month > 0 {
			parts = append(parts, "BYMONTH="+strconv.Itoa(r.Pattern.Month))
		}
		if r.Pattern.DayOfMonth > 0 {
			parts = append(parts, "BYMONTHDAY="+strconv.Itoa(r.Pattern.DayOfMonth))
		}
	default:
		return "" // relativeMonthly/relativeYearly not represented
	}
	if r.Pattern.Interval > 1 {
		parts = append(parts, "INTERVAL="+strconv.Itoa(r.Pattern.Interval))
	}
	switch r.Range.Type {
	case "endDate":
		if t, err := time.Parse("2006-01-02", r.Range.EndDate); err == nil {
			parts = append(parts, "UNTIL="+t.Format("20060102T235959Z"))
		}
	case "numbered":
		if r.Range.NumberOfOccurrences > 0 {
			parts = append(parts, "COUNT="+strconv.Itoa(r.Range.NumberOfOccurrences))
		}
	}
	return strings.Join(parts, ";")
}

// rruleToGraphRecurrence parses an RRULE (with or without the "RRULE:"
// prefix) into a patternedRecurrence anchored at start.
func rruleToGraphRecurrence(rule string, start time.Time) (map[string]any, error) {
	rule = strings.TrimPrefix(strings.TrimSpace(rule), "RRULE:")
	fields := map[string]string{}
	for _, kv := range strings.Split(rule, ";") {
		if kv == "" {
			continue
		}
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("%w: malformed RRULE part %q", domain.ErrValidation, kv)
		}
		fields[strings.ToUpper(k)] = strings.ToUpper(v)
	}

	interval := 1
	if v := fields["INTERVAL"]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("%w: bad RRULE INTERVAL %q", domain.ErrValidation, v)
		}
		interval = n
	}

	pattern := map[string]any{"interval": interval}
	switch fields["FREQ"] {
	case "DAILY":
		pattern["type"] = "daily"
	case "WEEKLY":
		pattern["type"] = "weekly"
		days := rruleDays(fields["BYDAY"])
		if len(days) == 0 {
			days = []string{strings.ToLower(start.Weekday().String())}
		}
		pattern["daysOfWeek"] = days
	case "MONTHLY":
		pattern["type"] = "absoluteMonthly"
		pattern["dayOfMonth"] = byMonthDayOr(fields["BYMONTHDAY"], start.Day())
	case "YEARLY":
		pattern["type"] = "absoluteYearly"
		pattern["dayOfMonth"] = byMonthDayOr(fields["BYMONTHDAY"], start.Day())
		pattern["month"] = int(start.Month())
	default:
		return nil, fmt.Errorf("%w: unsupported RRULE FREQ %q", domain.ErrValidation, fields["FREQ"])
	}

	rng := map[string]any{"type": "noEnd", "startDate": start.UTC().Format("2006-01-02")}
	if v := fields["COUNT"]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("%w: bad RRULE COUNT %q", domain.ErrValidation, v)
		}
		rng["type"] = "numbered"
		rng["numberOfOccurrences"] = n
	} else if v := fields["UNTIL"]; v != "" {
		t, err := parseRRuleUntil(v)
		if err != nil {
			return nil, err
		}
		rng["type"] = "endDate"
		rng["endDate"] = t.Format("2006-01-02")
	}

	return map[string]any{"pattern": pattern, "range": rng}, nil
}

func graphDays(days []string) string {
	var out []string
	for _, d := range days {
		if abbr, ok := graphDayToRRule[strings.ToLower(d)]; ok {
			out = append(out, abbr)
		}
	}
	return strings.Join(out, ",")
}

func rruleDays(byday string) []string {
	if byday == "" {
		return nil
	}
	var out []string
	for _, d := range strings.Split(byday, ",") {
		if name, ok := rruleDayToGraph[strings.TrimSpace(d)]; ok {
			out = append(out, name)
		}
	}
	return out
}

func byMonthDayOr(v string, fallback int) int {
	if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 31 {
		return n
	}
	return fallback
}

func parseRRuleUntil(v string) (time.Time, error) {
	for _, layout := range []string{"20060102T150405Z", "20060102"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%w: bad RRULE UNTIL %q", domain.ErrValidation, v)
}
