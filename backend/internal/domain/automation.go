package domain

import "time"

// ManagedKind classifies an automation-owned event: a FocusGuard focus
// block, an auto meeting buffer, or a travel-time buffer.
type ManagedKind string

const (
	ManagedFocus  ManagedKind = "focus"
	ManagedBuffer ManagedKind = "buffer"
	ManagedTravel ManagedKind = "travel"
)

// ManagedEvent tags a mirrored event as automation-owned. The event itself
// is a real provider event created through the normal write-through path;
// the tag is what makes the engine idempotent — re-runs only ever touch
// events listed here, never user-created ones. SourceEventID links
// buffers/travel blocks to the meeting they protect; WeekStart keys focus
// blocks to their planning week (Monday, prefs timezone).
type ManagedEvent struct {
	EventID       string
	UserID        string
	Kind          ManagedKind
	SourceEventID *string
	WeekStart     *time.Time
	CreatedAt     time.Time
}
