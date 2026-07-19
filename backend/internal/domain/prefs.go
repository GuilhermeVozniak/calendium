package domain

import (
	"fmt"
	"time"
)

type UserPrefs struct {
	SplitOrder []InboxSplit `json:"splitOrder"`
}

// Validate rejects unknown or duplicate splits; an empty order is valid
// ("use the default").
func (p UserPrefs) Validate() error {
	seen := make(map[InboxSplit]struct{}, len(p.SplitOrder))
	for _, s := range p.SplitOrder {
		if _, err := ParseInboxSplit(string(s)); err != nil {
			return err
		}
		if _, dup := seen[s]; dup {
			return fmt.Errorf("%w: duplicate split %q in splitOrder", ErrValidation, s)
		}
		seen[s] = struct{}{}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Calendar automation preferences (M2.8 Task 5)
// ---------------------------------------------------------------------------

// TravelMode is how the user travels between event locations; it selects the
// routing profile for travel buffers and leave-by alerts.
type TravelMode string

const (
	TravelDriving TravelMode = "driving"
	TravelWalking TravelMode = "walking"
	TravelTransit TravelMode = "transit"
)

// ParseTravelMode validates a travel mode string.
func ParseTravelMode(s string) (TravelMode, error) {
	switch m := TravelMode(s); m {
	case TravelDriving, TravelWalking, TravelTransit:
		return m, nil
	default:
		return "", fmt.Errorf("%w: unknown travel mode %q (want driving|walking|transit)", ErrValidation, s)
	}
}

// CalendarPrefs is the per-user calendar automation preference document —
// one row per user; DefaultCalendarPrefs is returned when absent. It gates
// the M2.8 automation engine (FocusGuard, auto buffers, OOO auto-decline,
// travel buffers / leave alerts, weather).
type CalendarPrefs struct {
	UserID string `json:"-"`
	// Working hours bound every automation (focus, buffers, travel).
	TimeZone            string         `json:"timeZone"`            // IANA; default "UTC"
	WorkDays            []time.Weekday `json:"workDays"`            // default Mon–Fri
	WorkdayStartMinutes int            `json:"workdayStartMinutes"` // default 9*60
	WorkdayEndMinutes   int            `json:"workdayEndMinutes"`   // default 17*60

	FocusGoalMinutesPerWeek int    `json:"focusGoalMinutesPerWeek"` // 0 = FocusGuard off
	FocusAutoDecline        bool   `json:"focusAutoDecline"`
	FocusDeclineMessage     string `json:"focusDeclineMessage"`

	AutoBufferMinutes int `json:"autoBufferMinutes"` // 0 = off; 5..30 valid

	OOOAutoDecline    bool   `json:"oooAutoDecline"`
	OOODeclineMessage string `json:"oooDeclineMessage"`

	TravelBuffers bool       `json:"travelBuffers"`
	TravelMode    TravelMode `json:"travelMode"` // driving|walking|transit
	LeaveAlerts   bool       `json:"leaveAlerts"`
	HomeLat       *float64   `json:"homeLat"`
	HomeLon       *float64   `json:"homeLon"`

	WeatherEnabled bool `json:"weatherEnabled"`
}

// DefaultCalendarPrefs is the document synthesized when the user has never
// saved calendar prefs: UTC, Mon–Fri 09:00–17:00, every automation off.
func DefaultCalendarPrefs(userID string) CalendarPrefs {
	return CalendarPrefs{
		UserID:              userID,
		TimeZone:            "UTC",
		WorkDays:            []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday},
		WorkdayStartMinutes: 9 * 60,
		WorkdayEndMinutes:   17 * 60,
		TravelMode:          TravelDriving,
	}
}

// maxFocusGoalMinutesPerWeek caps the focus goal at a full week.
const maxFocusGoalMinutesPerWeek = 7 * 24 * 60

// Validate checks the whole document: the time zone loads ("" and "Local"
// are rejected like the user_settings precedent), workday minutes are
// ordered and inside one day, the buffer is 0 (off) or 5..30, the travel
// mode is known, work days are valid and unique, and home coordinates —
// when present — come as a full lat/lon pair in range.
func (p CalendarPrefs) Validate() error {
	if p.TimeZone == "" || p.TimeZone == "Local" {
		return fmt.Errorf("%w: invalid time zone %q", ErrValidation, p.TimeZone)
	}
	if _, err := time.LoadLocation(p.TimeZone); err != nil {
		return fmt.Errorf("%w: invalid time zone %q", ErrValidation, p.TimeZone)
	}
	seen := make(map[time.Weekday]struct{}, len(p.WorkDays))
	for _, d := range p.WorkDays {
		if d < time.Sunday || d > time.Saturday {
			return fmt.Errorf("%w: work day must be 0-6, got %d", ErrValidation, d)
		}
		if _, dup := seen[d]; dup {
			return fmt.Errorf("%w: duplicate work day %d", ErrValidation, d)
		}
		seen[d] = struct{}{}
	}
	if p.WorkdayStartMinutes < 0 || p.WorkdayEndMinutes > 24*60 || p.WorkdayStartMinutes >= p.WorkdayEndMinutes {
		return fmt.Errorf("%w: workday minutes must satisfy 0 <= start < end <= 1440, got %d..%d",
			ErrValidation, p.WorkdayStartMinutes, p.WorkdayEndMinutes)
	}
	if p.FocusGoalMinutesPerWeek < 0 || p.FocusGoalMinutesPerWeek > maxFocusGoalMinutesPerWeek {
		return fmt.Errorf("%w: focusGoalMinutesPerWeek must be 0..%d, got %d",
			ErrValidation, maxFocusGoalMinutesPerWeek, p.FocusGoalMinutesPerWeek)
	}
	if p.AutoBufferMinutes != 0 && (p.AutoBufferMinutes < 5 || p.AutoBufferMinutes > 30) {
		return fmt.Errorf("%w: autoBufferMinutes must be 0 (off) or 5..30, got %d", ErrValidation, p.AutoBufferMinutes)
	}
	if _, err := ParseTravelMode(string(p.TravelMode)); err != nil {
		return err
	}
	if (p.HomeLat == nil) != (p.HomeLon == nil) {
		return fmt.Errorf("%w: homeLat and homeLon must be set together", ErrValidation)
	}
	if p.HomeLat != nil && (*p.HomeLat < -90 || *p.HomeLat > 90) {
		return fmt.Errorf("%w: homeLat must be -90..90, got %g", ErrValidation, *p.HomeLat)
	}
	if p.HomeLon != nil && (*p.HomeLon < -180 || *p.HomeLon > 180) {
		return fmt.Errorf("%w: homeLon must be -180..180, got %g", ErrValidation, *p.HomeLon)
	}
	return nil
}

// CalendarPrefsPatch is the PATCH /v1/prefs/calendar payload; nil means
// unchanged (the EventPatch convention). Home coordinates can be moved but
// not cleared through a patch — clearing travel automation is done by
// turning travelBuffers/leaveAlerts off.
type CalendarPrefsPatch struct {
	TimeZone                *string         `json:"timeZone"`
	WorkDays                *[]time.Weekday `json:"workDays"`
	WorkdayStartMinutes     *int            `json:"workdayStartMinutes"`
	WorkdayEndMinutes       *int            `json:"workdayEndMinutes"`
	FocusGoalMinutesPerWeek *int            `json:"focusGoalMinutesPerWeek"`
	FocusAutoDecline        *bool           `json:"focusAutoDecline"`
	FocusDeclineMessage     *string         `json:"focusDeclineMessage"`
	AutoBufferMinutes       *int            `json:"autoBufferMinutes"`
	OOOAutoDecline          *bool           `json:"oooAutoDecline"`
	OOODeclineMessage       *string         `json:"oooDeclineMessage"`
	TravelBuffers           *bool           `json:"travelBuffers"`
	TravelMode              *TravelMode     `json:"travelMode"`
	LeaveAlerts             *bool           `json:"leaveAlerts"`
	HomeLat                 *float64        `json:"homeLat"`
	HomeLon                 *float64        `json:"homeLon"`
	WeatherEnabled          *bool           `json:"weatherEnabled"`
}

// Apply returns base with every non-nil patch field applied. Slices and
// pointers are copied so the result never aliases the patch.
func (p CalendarPrefsPatch) Apply(base CalendarPrefs) CalendarPrefs {
	if p.TimeZone != nil {
		base.TimeZone = *p.TimeZone
	}
	if p.WorkDays != nil {
		base.WorkDays = append([]time.Weekday{}, (*p.WorkDays)...)
	}
	if p.WorkdayStartMinutes != nil {
		base.WorkdayStartMinutes = *p.WorkdayStartMinutes
	}
	if p.WorkdayEndMinutes != nil {
		base.WorkdayEndMinutes = *p.WorkdayEndMinutes
	}
	if p.FocusGoalMinutesPerWeek != nil {
		base.FocusGoalMinutesPerWeek = *p.FocusGoalMinutesPerWeek
	}
	if p.FocusAutoDecline != nil {
		base.FocusAutoDecline = *p.FocusAutoDecline
	}
	if p.FocusDeclineMessage != nil {
		base.FocusDeclineMessage = *p.FocusDeclineMessage
	}
	if p.AutoBufferMinutes != nil {
		base.AutoBufferMinutes = *p.AutoBufferMinutes
	}
	if p.OOOAutoDecline != nil {
		base.OOOAutoDecline = *p.OOOAutoDecline
	}
	if p.OOODeclineMessage != nil {
		base.OOODeclineMessage = *p.OOODeclineMessage
	}
	if p.TravelBuffers != nil {
		base.TravelBuffers = *p.TravelBuffers
	}
	if p.TravelMode != nil {
		base.TravelMode = *p.TravelMode
	}
	if p.LeaveAlerts != nil {
		base.LeaveAlerts = *p.LeaveAlerts
	}
	if p.HomeLat != nil {
		v := *p.HomeLat
		base.HomeLat = &v
	}
	if p.HomeLon != nil {
		v := *p.HomeLon
		base.HomeLon = &v
	}
	if p.WeatherEnabled != nil {
		base.WeatherEnabled = *p.WeatherEnabled
	}
	return base
}

// AutomationEnabled reports whether any calendar automation is on — the
// predicate CalendarPrefsRepo.ListAutomated mirrors in SQL.
func (p CalendarPrefs) AutomationEnabled() bool {
	return p.FocusGoalMinutesPerWeek > 0 ||
		p.AutoBufferMinutes > 0 ||
		p.OOOAutoDecline ||
		p.TravelBuffers ||
		p.LeaveAlerts ||
		p.WeatherEnabled
}
