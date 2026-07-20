package domain

import (
	"errors"
	"testing"
	"time"
)

// TestCalendarPrefsDefaults pins the documented defaults: UTC, Mon–Fri,
// 09:00–17:00, driving, every automation off.
func TestCalendarPrefsDefaults(t *testing.T) {
	p := DefaultCalendarPrefs("u1")
	if p.UserID != "u1" {
		t.Fatalf("UserID = %q, want u1", p.UserID)
	}
	if p.TimeZone != "UTC" {
		t.Fatalf("TimeZone = %q, want UTC", p.TimeZone)
	}
	wantDays := []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}
	if len(p.WorkDays) != len(wantDays) {
		t.Fatalf("WorkDays = %v, want %v", p.WorkDays, wantDays)
	}
	for i, d := range wantDays {
		if p.WorkDays[i] != d {
			t.Fatalf("WorkDays = %v, want %v", p.WorkDays, wantDays)
		}
	}
	if p.WorkdayStartMinutes != 9*60 || p.WorkdayEndMinutes != 17*60 {
		t.Fatalf("workday = %d..%d, want 540..1020", p.WorkdayStartMinutes, p.WorkdayEndMinutes)
	}
	if p.TravelMode != TravelDriving {
		t.Fatalf("TravelMode = %q, want driving", p.TravelMode)
	}
	if p.AutomationEnabled() {
		t.Fatalf("defaults must have every automation off")
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("defaults must validate, got %v", err)
	}
}

// TestCalendarPrefsValidate table-drives every Validate rule from the brief:
// tz loads, minutes ordered & in-day, buffer 0 or 5..30, mode known — plus
// work-day range/duplicate and home lat/lon pair checks.
func TestCalendarPrefsValidate(t *testing.T) {
	valid := func() CalendarPrefs { return DefaultCalendarPrefs("u1") }
	f := func(v float64) *float64 { return &v }

	cases := []struct {
		name   string
		mutate func(*CalendarPrefs)
		wantOK bool
	}{
		{"defaults", func(p *CalendarPrefs) {}, true},
		{"real zone", func(p *CalendarPrefs) { p.TimeZone = "America/New_York" }, true},
		{"empty tz", func(p *CalendarPrefs) { p.TimeZone = "" }, false},
		{"Local tz", func(p *CalendarPrefs) { p.TimeZone = "Local" }, false},
		{"bogus tz", func(p *CalendarPrefs) { p.TimeZone = "Not/AZone" }, false},
		{"empty work days ok", func(p *CalendarPrefs) { p.WorkDays = nil }, true},
		{"work day out of range", func(p *CalendarPrefs) { p.WorkDays = []time.Weekday{7} }, false},
		{"duplicate work day", func(p *CalendarPrefs) { p.WorkDays = []time.Weekday{1, 1} }, false},
		{"start after end", func(p *CalendarPrefs) { p.WorkdayStartMinutes, p.WorkdayEndMinutes = 1020, 540 }, false},
		{"start equals end", func(p *CalendarPrefs) { p.WorkdayStartMinutes, p.WorkdayEndMinutes = 540, 540 }, false},
		{"negative start", func(p *CalendarPrefs) { p.WorkdayStartMinutes = -1 }, false},
		{"end past midnight", func(p *CalendarPrefs) { p.WorkdayEndMinutes = 24*60 + 1 }, false},
		{"end at midnight ok", func(p *CalendarPrefs) { p.WorkdayEndMinutes = 24 * 60 }, true},
		{"negative focus goal", func(p *CalendarPrefs) { p.FocusGoalMinutesPerWeek = -1 }, false},
		{"focus goal over a week", func(p *CalendarPrefs) { p.FocusGoalMinutesPerWeek = 7*24*60 + 1 }, false},
		{"focus goal ok", func(p *CalendarPrefs) { p.FocusGoalMinutesPerWeek = 8 * 60 }, true},
		{"buffer zero ok", func(p *CalendarPrefs) { p.AutoBufferMinutes = 0 }, true},
		{"buffer below five", func(p *CalendarPrefs) { p.AutoBufferMinutes = 3 }, false},
		{"buffer five ok", func(p *CalendarPrefs) { p.AutoBufferMinutes = 5 }, true},
		{"buffer thirty ok", func(p *CalendarPrefs) { p.AutoBufferMinutes = 30 }, true},
		{"buffer over thirty", func(p *CalendarPrefs) { p.AutoBufferMinutes = 31 }, false},
		{"unknown travel mode", func(p *CalendarPrefs) { p.TravelMode = "teleport" }, false},
		{"walking ok", func(p *CalendarPrefs) { p.TravelMode = TravelWalking }, true},
		{"transit ok", func(p *CalendarPrefs) { p.TravelMode = TravelTransit }, true},
		{"home pair ok", func(p *CalendarPrefs) { p.HomeLat, p.HomeLon = f(52.37), f(4.89) }, true},
		{"lat without lon", func(p *CalendarPrefs) { p.HomeLat = f(52.37) }, false},
		{"lon without lat", func(p *CalendarPrefs) { p.HomeLon = f(4.89) }, false},
		{"lat out of range", func(p *CalendarPrefs) { p.HomeLat, p.HomeLon = f(90.5), f(0) }, false},
		{"lon out of range", func(p *CalendarPrefs) { p.HomeLat, p.HomeLon = f(0), f(-180.5) }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := valid()
			tc.mutate(&p)
			err := p.Validate()
			if tc.wantOK && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
			if !tc.wantOK {
				if !errors.Is(err, ErrValidation) {
					t.Fatalf("Validate() = %v, want ErrValidation", err)
				}
			}
		})
	}
}

// TestCalendarPrefsPatchApply covers nil-means-unchanged plus value/slice
// copies (no aliasing of the patch's memory).
func TestCalendarPrefsPatchApply(t *testing.T) {
	base := DefaultCalendarPrefs("u1")

	if got := (CalendarPrefsPatch{}).Apply(base); got.TimeZone != "UTC" || got.WorkdayStartMinutes != 540 {
		t.Fatalf("empty patch changed the document: %+v", got)
	}

	tz := "Europe/Amsterdam"
	goal := 10 * 60
	buffer := 10
	decline := true
	msg := "In focus — back later."
	mode := TravelTransit
	lat, lon := 52.37, 4.89
	days := []time.Weekday{time.Tuesday, time.Thursday}
	weather := true
	patch := CalendarPrefsPatch{
		TimeZone:                &tz,
		WorkDays:                &days,
		FocusGoalMinutesPerWeek: &goal,
		FocusAutoDecline:        &decline,
		FocusDeclineMessage:     &msg,
		AutoBufferMinutes:       &buffer,
		OOOAutoDecline:          &decline,
		OOODeclineMessage:       &msg,
		TravelBuffers:           &decline,
		TravelMode:              &mode,
		LeaveAlerts:             &decline,
		HomeLat:                 &lat,
		HomeLon:                 &lon,
		WeatherEnabled:          &weather,
	}
	got := patch.Apply(base)

	if got.TimeZone != tz || got.FocusGoalMinutesPerWeek != goal || got.AutoBufferMinutes != buffer {
		t.Fatalf("patched doc = %+v", got)
	}
	if !got.FocusAutoDecline || got.FocusDeclineMessage != msg || !got.OOOAutoDecline || got.OOODeclineMessage != msg {
		t.Fatalf("decline fields not applied: %+v", got)
	}
	if !got.TravelBuffers || got.TravelMode != TravelTransit || !got.LeaveAlerts || !got.WeatherEnabled {
		t.Fatalf("travel/weather fields not applied: %+v", got)
	}
	if got.HomeLat == nil || *got.HomeLat != lat || got.HomeLon == nil || *got.HomeLon != lon {
		t.Fatalf("home coordinates not applied: %+v", got)
	}
	if got.HomeLat == &lat || got.HomeLon == &lon {
		t.Fatalf("patch pointers must be copied, not aliased")
	}
	if len(got.WorkDays) != 2 || got.WorkDays[0] != time.Tuesday {
		t.Fatalf("WorkDays = %v", got.WorkDays)
	}
	days[0] = time.Sunday // mutating the patch slice must not touch the result
	if got.WorkDays[0] != time.Tuesday {
		t.Fatalf("WorkDays aliases the patch slice")
	}
	// Untouched fields keep base values; base itself is unchanged (value receiver).
	if got.WorkdayStartMinutes != 540 || got.WorkdayEndMinutes != 1020 {
		t.Fatalf("untouched fields changed: %+v", got)
	}
	if base.TimeZone != "UTC" || base.HomeLat != nil {
		t.Fatalf("base mutated: %+v", base)
	}
}

// TestCalendarPrefsAutomationEnabled pins the fan-out predicate the repo's
// ListAutomated mirrors in SQL: any one automation flips it on.
func TestCalendarPrefsAutomationEnabled(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*CalendarPrefs)
		want   bool
	}{
		{"defaults off", func(p *CalendarPrefs) {}, false},
		{"focus goal", func(p *CalendarPrefs) { p.FocusGoalMinutesPerWeek = 300 }, true},
		{"auto buffer", func(p *CalendarPrefs) { p.AutoBufferMinutes = 10 }, true},
		{"ooo auto-decline", func(p *CalendarPrefs) { p.OOOAutoDecline = true }, true},
		{"travel buffers", func(p *CalendarPrefs) { p.TravelBuffers = true }, true},
		{"leave alerts", func(p *CalendarPrefs) { p.LeaveAlerts = true }, true},
		{"weather", func(p *CalendarPrefs) { p.WeatherEnabled = true }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := DefaultCalendarPrefs("u1")
			tc.mutate(&p)
			if got := p.AutomationEnabled(); got != tc.want {
				t.Fatalf("AutomationEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}
