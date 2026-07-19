package domain

import (
	"fmt"
	"strings"
	"time"
)

// TaskSource identifies where a task originates.
type TaskSource string

const (
	TaskSourceLocal   TaskSource = "local"
	TaskSourceTodoist TaskSource = "todoist"
)

// ParseTaskSource validates a task source parameter.
func ParseTaskSource(s string) (TaskSource, error) {
	switch TaskSource(s) {
	case TaskSourceLocal, TaskSourceTodoist:
		return TaskSource(s), nil
	}
	return "", fmt.Errorf("%w: unknown task source %q", ErrValidation, s)
}

// Task is a first-class todo. Due carries deadline semantics; the
// Scheduled* pair carries timeblock semantics — when both are set the task
// renders on the calendar grid between ScheduledStart and ScheduledEnd and
// in the rail's due grouping. External tasks mirror a provider todo
// (Source/ExternalID) and write completion through to the provider.
type Task struct {
	ID     string     `json:"id"`
	UserID string     `json:"-"`
	Title  string     `json:"title"`
	Notes  *string    `json:"notes"`
	Due    *time.Time `json:"due"`
	// AllDayDue marks a date-only due (render in the all-day lane, no hour).
	AllDayDue      bool       `json:"allDayDue"`
	ScheduledStart *time.Time `json:"scheduledStart"`
	ScheduledEnd   *time.Time `json:"scheduledEnd"`
	CompletedAt    *time.Time `json:"completedAt"`
	Source         TaskSource `json:"source"`
	ExternalID     string     `json:"-"`
	SourceURL      *string    `json:"sourceUrl"`
	// Position orders tasks inside the unscheduled rail (lower first;
	// fractional so drag-reorder never rewrites neighbors).
	Position  float64   `json:"position"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Completed reports whether the task is checked off.
func (t Task) Completed() bool { return t.CompletedAt != nil }

// Scheduled reports whether the task occupies a timeblock on the grid.
func (t Task) Scheduled() bool { return t.ScheduledStart != nil && t.ScheduledEnd != nil }

// Validate enforces invariants shared by the create and update paths.
func (t Task) Validate() error {
	if strings.TrimSpace(t.Title) == "" {
		return fmt.Errorf("%w: title is required", ErrValidation)
	}
	if (t.ScheduledStart == nil) != (t.ScheduledEnd == nil) {
		return fmt.Errorf("%w: scheduledStart and scheduledEnd must be set together", ErrValidation)
	}
	if t.Scheduled() && !t.ScheduledEnd.After(*t.ScheduledStart) {
		return fmt.Errorf("%w: scheduledEnd must be after scheduledStart", ErrValidation)
	}
	if t.AllDayDue && t.Due == nil {
		return fmt.Errorf("%w: allDayDue requires due", ErrValidation)
	}
	return nil
}

// TaskInput is the create-task payload (mirrors TaskInput in types.ts).
type TaskInput struct {
	Title          string     `json:"title"`
	Notes          string     `json:"notes,omitempty"`
	Due            *time.Time `json:"due,omitempty"`
	AllDayDue      bool       `json:"allDayDue,omitempty"`
	ScheduledStart *time.Time `json:"scheduledStart,omitempty"`
	ScheduledEnd   *time.Time `json:"scheduledEnd,omitempty"`
	Position       *float64   `json:"position,omitempty"`
}

// TaskPatch is a partial update; nil fields are left unchanged. Explicit
// JSON null clears a clearable field (Due, Scheduled*, Notes) — the
// double-pointer decode helper in httpapi/codec.go handles the distinction.
type TaskPatch struct {
	Title          *string     `json:"title"`
	Notes          **string    `json:"notes"`
	Due            **time.Time `json:"due"`
	AllDayDue      *bool       `json:"allDayDue"`
	ScheduledStart **time.Time `json:"scheduledStart"`
	ScheduledEnd   **time.Time `json:"scheduledEnd"`
	Position       *float64    `json:"position"`
}
