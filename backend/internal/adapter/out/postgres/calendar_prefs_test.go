package postgres

import (
	"context"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// TestCalendarPrefsGetDefaultsOnMissingRow: no row => DefaultCalendarPrefs,
// never ErrNotFound.
func TestCalendarPrefsGetDefaultsOnMissingRow(t *testing.T) {
	st, _ := newTestStore(t)
	seedUser(t, st, "u1")

	got, err := st.CalendarPrefs().Get(context.Background(), "u1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := domain.DefaultCalendarPrefs("u1")
	if got.UserID != "u1" || got.TimeZone != want.TimeZone ||
		got.WorkdayStartMinutes != want.WorkdayStartMinutes ||
		got.WorkdayEndMinutes != want.WorkdayEndMinutes ||
		got.TravelMode != want.TravelMode ||
		len(got.WorkDays) != len(want.WorkDays) {
		t.Fatalf("Get on missing row = %+v, want defaults %+v", got, want)
	}
}

// TestCalendarPrefsUpsertRoundTrip covers create + full-document overwrite,
// including pointer coordinates and the weekday slice.
func TestCalendarPrefsUpsertRoundTrip(t *testing.T) {
	st, _ := newTestStore(t)
	seedUser(t, st, "u1")
	ctx := context.Background()

	lat, lon := 52.37, 4.89
	p := domain.DefaultCalendarPrefs("u1")
	p.TimeZone = "Europe/Amsterdam"
	p.WorkDays = []time.Weekday{time.Tuesday, time.Thursday}
	p.WorkdayStartMinutes, p.WorkdayEndMinutes = 8 * 60, 16 * 60
	p.FocusGoalMinutesPerWeek = 10 * 60
	p.FocusAutoDecline = true
	p.FocusDeclineMessage = "Deep work — back at 4."
	p.AutoBufferMinutes = 10
	p.OOOAutoDecline = true
	p.OOODeclineMessage = "Out of office."
	p.TravelBuffers = true
	p.TravelMode = domain.TravelTransit
	p.LeaveAlerts = true
	p.HomeLat, p.HomeLon = &lat, &lon
	p.WeatherEnabled = true

	if err := st.CalendarPrefs().Upsert(ctx, p); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := st.CalendarPrefs().Get(ctx, "u1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.TimeZone != "Europe/Amsterdam" || got.FocusGoalMinutesPerWeek != 600 ||
		!got.FocusAutoDecline || got.FocusDeclineMessage != "Deep work — back at 4." ||
		got.AutoBufferMinutes != 10 || !got.OOOAutoDecline || got.OOODeclineMessage != "Out of office." ||
		!got.TravelBuffers || got.TravelMode != domain.TravelTransit || !got.LeaveAlerts || !got.WeatherEnabled {
		t.Fatalf("round trip = %+v", got)
	}
	if got.HomeLat == nil || *got.HomeLat != lat || got.HomeLon == nil || *got.HomeLon != lon {
		t.Fatalf("home coordinates = %v/%v, want %v/%v", got.HomeLat, got.HomeLon, lat, lon)
	}
	if len(got.WorkDays) != 2 || got.WorkDays[0] != time.Tuesday || got.WorkDays[1] != time.Thursday {
		t.Fatalf("WorkDays = %v", got.WorkDays)
	}

	// Overwrite: the second upsert replaces the whole document.
	p.FocusGoalMinutesPerWeek = 0
	p.HomeLat, p.HomeLon = nil, nil
	if err := st.CalendarPrefs().Upsert(ctx, p); err != nil {
		t.Fatalf("Upsert(overwrite): %v", err)
	}
	got, err = st.CalendarPrefs().Get(ctx, "u1")
	if err != nil {
		t.Fatalf("Get after overwrite: %v", err)
	}
	if got.FocusGoalMinutesPerWeek != 0 || got.HomeLat != nil || got.HomeLon != nil {
		t.Fatalf("overwrite not applied: %+v", got)
	}
}

// TestCalendarPrefsListAutomated: only rows with an automation on are
// returned — all-defaults rows and absent rows never appear — and every
// automation flag independently qualifies a row.
func TestCalendarPrefsListAutomated(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()

	flip := map[string]func(*domain.CalendarPrefs){
		"u_focus":   func(p *domain.CalendarPrefs) { p.FocusGoalMinutesPerWeek = 300 },
		"u_buffer":  func(p *domain.CalendarPrefs) { p.AutoBufferMinutes = 15 },
		"u_ooo":     func(p *domain.CalendarPrefs) { p.OOOAutoDecline = true },
		"u_travel":  func(p *domain.CalendarPrefs) { p.TravelBuffers = true },
		"u_leave":   func(p *domain.CalendarPrefs) { p.LeaveAlerts = true },
		"u_weather": func(p *domain.CalendarPrefs) { p.WeatherEnabled = true },
	}
	for uid, mutate := range flip {
		seedUser(t, st, uid)
		p := domain.DefaultCalendarPrefs(uid)
		mutate(&p)
		if err := st.CalendarPrefs().Upsert(ctx, p); err != nil {
			t.Fatalf("Upsert(%s): %v", uid, err)
		}
	}
	// A saved row with every automation off must NOT be returned.
	seedUser(t, st, "u_defaults")
	if err := st.CalendarPrefs().Upsert(ctx, domain.DefaultCalendarPrefs("u_defaults")); err != nil {
		t.Fatalf("Upsert(defaults): %v", err)
	}
	// A user with no row at all must not be returned either.
	seedUser(t, st, "u_absent")

	got, err := st.CalendarPrefs().ListAutomated(ctx)
	if err != nil {
		t.Fatalf("ListAutomated: %v", err)
	}
	if len(got) != len(flip) {
		t.Fatalf("ListAutomated returned %d rows, want %d: %+v", len(got), len(flip), got)
	}
	for _, p := range got {
		if _, ok := flip[p.UserID]; !ok {
			t.Fatalf("unexpected row for %q", p.UserID)
		}
		if !p.AutomationEnabled() {
			t.Fatalf("row %q has no automation enabled: %+v", p.UserID, p)
		}
	}
}
