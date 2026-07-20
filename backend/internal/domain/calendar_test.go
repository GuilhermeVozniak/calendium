package domain

import "testing"

func TestIsOOOEvent(t *testing.T) {
	cases := []struct {
		name  string
		event Event
		want  bool
	}{
		{"bare OOO", Event{Title: "OOO", Status: EventConfirmed}, true},
		{"out of office with suffix", Event{Title: "Out of office — Lisbon", Status: EventConfirmed}, true},
		{"vacation", Event{Title: "Vacation", Status: EventConfirmed}, true},
		{"annual leave", Event{Title: "Annual leave", Status: EventConfirmed}, true},
		{"pto lowercase", Event{Title: "pto friday", Status: EventConfirmed}, true},
		{"all-day flag irrelevant", Event{Title: "OOO", AllDay: true, Status: EventConfirmed}, true},
		{"unrelated title", Event{Title: "foo", Status: EventConfirmed}, false},
		{"substring does not match without word boundary", Event{Title: "Laptop handover", Status: EventConfirmed}, false},
		{"cancelled OOO is not a period", Event{Title: "OOO", Status: EventCancelled}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsOOOEvent(tc.event); got != tc.want {
				t.Fatalf("IsOOOEvent(%q) = %v, want %v", tc.event.Title, got, tc.want)
			}
		})
	}
}
