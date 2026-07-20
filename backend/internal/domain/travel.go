package domain

import "time"

// TravelAlert is a pending "time to leave" push for one event (M2.8 Task
// 12), created by the travel pass when the user has leave alerts enabled.
// LeaveAt = event start - travel time - 5 minutes. SentAt is nil until a
// push was observed to succeed (honesty policy: sent_at is stamped after
// delivery, never before); it is cleared when LeaveAt changes so a moved
// event re-arms its alert.
type TravelAlert struct {
	EventID string
	UserID  string
	LeaveAt time.Time
	SentAt  *time.Time
}
