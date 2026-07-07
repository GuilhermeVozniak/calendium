package msgraph

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func TestRRuleToGraphRecurrenceAndBack(t *testing.T) {
	start := time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC) // a Monday

	rec, err := rruleToGraphRecurrence("RRULE:FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,WE;COUNT=10", start)
	if err != nil {
		t.Fatalf("rruleToGraphRecurrence: %v", err)
	}
	raw, _ := json.Marshal(rec)
	var parsed graphRecurrence
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Pattern.Type != "weekly" || parsed.Pattern.Interval != 2 {
		t.Fatalf("bad pattern: %+v", parsed.Pattern)
	}
	if parsed.Range.Type != "numbered" || parsed.Range.NumberOfOccurrences != 10 {
		t.Fatalf("bad range: %+v", parsed.Range)
	}

	back := graphRecurrenceToRRule(&parsed)
	if back != "FREQ=WEEKLY;BYDAY=MO,WE;INTERVAL=2;COUNT=10" {
		t.Fatalf("round trip mismatch: %q", back)
	}
}

func TestRRuleWeeklyDefaultsToStartWeekday(t *testing.T) {
	start := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC) // a Tuesday
	rec, err := rruleToGraphRecurrence("FREQ=WEEKLY", start)
	if err != nil {
		t.Fatal(err)
	}
	days := rec["pattern"].(map[string]any)["daysOfWeek"].([]string)
	if len(days) != 1 || days[0] != "tuesday" {
		t.Fatalf("expected [tuesday], got %v", days)
	}
}

func TestRRuleUnsupportedFreq(t *testing.T) {
	if _, err := rruleToGraphRecurrence("FREQ=HOURLY", time.Now()); err == nil {
		t.Fatal("expected error for unsupported FREQ")
	}
}

func TestByMonthDayOr(t *testing.T) {
	cases := []struct {
		name     string
		v        string
		fallback int
		want     int
	}{
		{"valid day in range", "15", 1, 15},
		{"boundary low is valid", "1", 9, 1},
		{"boundary high is valid", "31", 9, 31},
		{"non-numeric falls back", "abc", 7, 7},
		{"zero is out of range, falls back", "0", 7, 7},
		{"32 is out of range, falls back", "32", 7, 7},
		{"empty string falls back", "", 3, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := byMonthDayOr(tc.v, tc.fallback); got != tc.want {
				t.Errorf("byMonthDayOr(%q, %d) = %d, want %d", tc.v, tc.fallback, got, tc.want)
			}
		})
	}
}

func TestParseRRuleUntil(t *testing.T) {
	t.Run("full datetime layout", func(t *testing.T) {
		got, err := parseRRuleUntil("20260706T235959Z")
		if err != nil {
			t.Fatalf("parseRRuleUntil: %v", err)
		}
		want := time.Date(2026, 7, 6, 23, 59, 59, 0, time.UTC)
		if !got.Equal(want) {
			t.Errorf("got = %v, want %v", got, want)
		}
	})

	t.Run("date-only layout", func(t *testing.T) {
		got, err := parseRRuleUntil("20260706")
		if err != nil {
			t.Fatalf("parseRRuleUntil: %v", err)
		}
		want := time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Errorf("got = %v, want %v", got, want)
		}
	})

	t.Run("malformed value errors with ErrValidation", func(t *testing.T) {
		_, err := parseRRuleUntil("not-a-date")
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})
}
