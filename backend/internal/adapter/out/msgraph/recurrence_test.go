package msgraph

import (
	"encoding/json"
	"testing"
	"time"
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
