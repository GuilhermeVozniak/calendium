package domain

import (
	"strings"
	"time"
)

// --- Time insights (M2.8 Task 17) --------------------------------------------
//
// TimeInsights is the aggregated time-analytics document served at
// GET /v1/insights/time. It is computed entirely from the LOCAL mirror
// (events, managed_events, tasks) — never from provider calls.

// PersonStat is one "top person" row: meetings shared with them and the
// minutes spent in those meetings inside the requested range.
type PersonStat struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Meetings int    `json:"meetings"`
	Minutes  int    `json:"minutes"`
}

// DayStat is one day's meeting-vs-focus split for the per-day mini bars.
type DayStat struct {
	Date           string `json:"date"` // YYYY-MM-DD
	MeetingMinutes int    `json:"meetingMinutes"`
	FocusMinutes   int    `json:"focusMinutes"`
}

// TimeInsights aggregates how the user's time in [From, To) was spent.
type TimeInsights struct {
	From             time.Time    `json:"from"`
	To               time.Time    `json:"to"`
	MeetingMinutes   int          `json:"meetingMinutes"`   // events with >=2 attendees, not declined/cancelled
	FocusMinutes     int          `json:"focusMinutes"`     // managed focus + IsFocusTitle events
	TaskMinutes      int          `json:"taskMinutes"`      // scheduled task blocks
	MeetingCount     int          `json:"meetingCount"`
	FocusGoalMinutes int          `json:"focusGoalMinutes"` // weekly goal scaled to the range
	TopPeople        []PersonStat `json:"topPeople"`        // top 5 by minutes, self excluded
	ByDay            []DayStat    `json:"byDay"`
}

// IsFocusTitle reports whether a user-created event's title marks it as
// focus time ("focus" substring, case-insensitive) for insights
// classification.
func IsFocusTitle(title string) bool {
	return strings.Contains(strings.ToLower(title), "focus")
}
