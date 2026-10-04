package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestOwnsTeamsErrorWrapsSentinel(t *testing.T) {
	var err error = &OwnsTeamsError{Teams: []TeamRef{{ID: "t1", Name: "Design"}}}
	if !errors.Is(err, ErrOwnsTeams) {
		t.Fatal("OwnsTeamsError must unwrap to ErrOwnsTeams")
	}
	var typed *OwnsTeamsError
	if !errors.As(err, &typed) || len(typed.Teams) != 1 || typed.Teams[0].Name != "Design" {
		t.Fatalf("errors.As lost the team list: %+v", typed)
	}
	if got := err.Error(); got != "owns teams: 1 team(s) need another owner first" {
		t.Fatalf("Error() = %q", got)
	}
}

func TestExportThrottledErrorWrapsSentinel(t *testing.T) {
	var err error = &ExportThrottledError{RetryAfter: 90 * time.Second}
	if !errors.Is(err, ErrExportThrottled) {
		t.Fatal("ExportThrottledError must unwrap to ErrExportThrottled")
	}
	var typed *ExportThrottledError
	if !errors.As(err, &typed) || typed.RetryAfter != 90*time.Second {
		t.Fatalf("errors.As lost RetryAfter: %+v", typed)
	}
}

func TestUserSettingsAIBackgroundJSONName(t *testing.T) {
	b, err := json.Marshal(UserSettings{TimeZone: "UTC", WorkingHours: []AvailabilityWindow{}, AIBackground: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"aiBackground":true`) {
		t.Fatalf("UserSettings JSON = %s, want an aiBackground field", b)
	}
}
