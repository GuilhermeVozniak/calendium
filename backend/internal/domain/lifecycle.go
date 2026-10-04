package domain

import (
	"fmt"
	"time"
)

// TeamRef identifies a team in an owns_teams refusal: id plus display name
// only (both are already visible to the refused user as a member).
type TeamRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// OwnsTeamsError wraps ErrOwnsTeams with the teams that still need another
// owner before the account can be deleted.
type OwnsTeamsError struct {
	Teams []TeamRef
}

func (e *OwnsTeamsError) Error() string {
	return fmt.Sprintf("owns teams: %d team(s) need another owner first", len(e.Teams))
}

func (e *OwnsTeamsError) Unwrap() error { return ErrOwnsTeams }

// ExportThrottledError wraps ErrExportThrottled with how long the caller
// must wait before the next export slot opens.
type ExportThrottledError struct {
	RetryAfter time.Duration
}

func (e *ExportThrottledError) Error() string {
	return fmt.Sprintf("export throttled: retry after %s", e.RetryAfter)
}

func (e *ExportThrottledError) Unwrap() error { return ErrExportThrottled }
