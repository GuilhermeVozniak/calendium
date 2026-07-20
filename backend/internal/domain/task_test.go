package domain

import (
	"errors"
	"testing"
	"time"
)

func TestParseTaskSource(t *testing.T) {
	for _, valid := range []string{"local", "todoist"} {
		src, err := ParseTaskSource(valid)
		if err != nil {
			t.Fatalf("ParseTaskSource(%q): unexpected error: %v", valid, err)
		}
		if string(src) != valid {
			t.Fatalf("ParseTaskSource(%q) = %q", valid, src)
		}
	}
	for _, invalid := range []string{"", "linear", "LOCAL", "todoist "} {
		if _, err := ParseTaskSource(invalid); !errors.Is(err, ErrValidation) {
			t.Fatalf("ParseTaskSource(%q): want ErrValidation, got %v", invalid, err)
		}
	}
}

func TestTaskValidate(t *testing.T) {
	start := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	due := start.Add(24 * time.Hour)

	tests := []struct {
		name    string
		task    Task
		wantErr bool
	}{
		{"valid local", Task{Title: "buy milk", Source: TaskSourceLocal}, false},
		{"valid scheduled", Task{Title: "deep work", ScheduledStart: &start, ScheduledEnd: &end}, false},
		{"valid all-day due", Task{Title: "taxes", Due: &due, AllDayDue: true}, false},
		{"empty title", Task{Title: ""}, true},
		{"whitespace title", Task{Title: "   "}, true},
		{"lone scheduledStart", Task{Title: "x", ScheduledStart: &start}, true},
		{"lone scheduledEnd", Task{Title: "x", ScheduledEnd: &end}, true},
		{"end before start", Task{Title: "x", ScheduledStart: &end, ScheduledEnd: &start}, true},
		{"end equals start", Task{Title: "x", ScheduledStart: &start, ScheduledEnd: &start}, true},
		{"allDayDue without due", Task{Title: "x", AllDayDue: true}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.task.Validate()
			if tt.wantErr {
				if !errors.Is(err, ErrValidation) {
					t.Fatalf("Validate() = %v, want ErrValidation", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() unexpected error: %v", err)
			}
		})
	}
}

func TestTaskCompleted(t *testing.T) {
	now := time.Now()
	if (Task{}).Completed() {
		t.Fatal("zero task must not be completed")
	}
	if !(Task{CompletedAt: &now}).Completed() {
		t.Fatal("task with CompletedAt must be completed")
	}
}

func TestTaskScheduled(t *testing.T) {
	now := time.Now()
	later := now.Add(time.Hour)
	if (Task{}).Scheduled() {
		t.Fatal("zero task must not be scheduled")
	}
	if (Task{ScheduledStart: &now}).Scheduled() {
		t.Fatal("half-set pair must not report scheduled")
	}
	if !(Task{ScheduledStart: &now, ScheduledEnd: &later}).Scheduled() {
		t.Fatal("full pair must report scheduled")
	}
}
