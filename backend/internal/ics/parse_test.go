package ics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func parseFixture(t *testing.T, name string) (Calendar, error) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("open fixture %s: %v", name, err)
	}
	defer f.Close()
	return Parse(f)
}

func mustLoadLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%s): %v", name, err)
	}
	return loc
}

func TestParseGoogleFixture(t *testing.T) {
	cal, err := parseFixture(t, "google.ics")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cal.Name != "Phases of the Moon" {
		t.Errorf("Name = %q, want %q", cal.Name, "Phases of the Moon")
	}
	if len(cal.Events) != 3 {
		t.Fatalf("got %d events, want 3", len(cal.Events))
	}

	moon := cal.Events[0]
	if moon.UID != "moon-full-20260702@google.com" {
		t.Errorf("UID = %q", moon.UID)
	}
	if moon.Summary != "Full Moon" {
		t.Errorf("Summary = %q", moon.Summary)
	}
	if moon.Description != "Full moon, visible all night." {
		t.Errorf("Description = %q", moon.Description)
	}
	if moon.Status != "CONFIRMED" {
		t.Errorf("Status = %q, want CONFIRMED (upper-cased)", moon.Status)
	}
	wantStart := time.Date(2026, 7, 2, 3, 15, 0, 0, time.UTC)
	if !moon.Start.Equal(wantStart) {
		t.Errorf("Start = %v, want %v", moon.Start, wantStart)
	}
	if !moon.End.Equal(wantStart) {
		t.Errorf("End = %v, want %v", moon.End, wantStart)
	}
	if moon.AllDay {
		t.Error("AllDay = true, want false")
	}
	wantMod := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if !moon.LastModified.Equal(wantMod) {
		t.Errorf("LastModified = %v, want %v", moon.LastModified, wantMod)
	}

	lisbon := mustLoadLocation(t, "Europe/Lisbon")
	brk := cal.Events[1]
	if !brk.Start.Equal(time.Date(2026, 8, 10, 9, 30, 0, 0, lisbon)) {
		t.Errorf("Start = %v, want 2026-08-10 09:30 Europe/Lisbon", brk.Start)
	}
	if !brk.End.Equal(time.Date(2026, 8, 10, 10, 30, 0, 0, lisbon)) {
		t.Errorf("End = %v, want 2026-08-10 10:30 Europe/Lisbon", brk.End)
	}
	if brk.Location != "Café A Brasileira, Chiado" {
		t.Errorf("Location = %q", brk.Location)
	}

	standup := cal.Events[2]
	wantStandup := time.Date(2026, 7, 15, 18, 0, 0, 0, time.UTC)
	if !standup.Start.Equal(wantStandup) {
		t.Errorf("Start = %v, want %v", standup.Start, wantStandup)
	}
	if !standup.End.Equal(wantStandup.Add(45 * time.Minute)) {
		t.Errorf("End = %v, want DTSTART+PT45M", standup.End)
	}
}

func TestParseOutlookFixture(t *testing.T) {
	cal, err := parseFixture(t, "outlook.ics")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cal.Name != "Ops Team" {
		t.Errorf("Name = %q, want %q", cal.Name, "Ops Team")
	}
	if len(cal.Events) != 1 {
		t.Fatalf("got %d events, want 1 (VTIMEZONE must not produce events)", len(cal.Events))
	}
	ev := cal.Events[0]
	// Unknown Windows-style TZID falls back to UTC.
	if !ev.Start.Equal(time.Date(2026, 9, 1, 14, 0, 0, 0, time.UTC)) {
		t.Errorf("Start = %v, want 2026-09-01 14:00 UTC (unknown TZID falls back to UTC)", ev.Start)
	}
	if !ev.End.Equal(time.Date(2026, 9, 1, 15, 0, 0, 0, time.UTC)) {
		t.Errorf("End = %v", ev.End)
	}
	// The nested VALARM's DESCRIPTION must not leak into the event.
	if ev.Description != "" {
		t.Errorf("Description = %q, want empty (VALARM property must be ignored)", ev.Description)
	}
	if ev.Summary != "Sprint review" {
		t.Errorf("Summary = %q", ev.Summary)
	}
}

func TestParseHolidaysFixture(t *testing.T) {
	cal, err := parseFixture(t, "holidays.ics")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cal.Events) != 2 {
		t.Fatalf("got %d events, want 2", len(cal.Events))
	}

	natal := cal.Events[0]
	if !natal.AllDay {
		t.Error("AllDay = false, want true")
	}
	if !natal.Start.Equal(time.Date(2025, 12, 25, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Start = %v", natal.Start)
	}
	if !natal.End.Equal(time.Date(2025, 12, 26, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("End = %v", natal.End)
	}
	if natal.RRule != "FREQ=YEARLY" {
		t.Errorf("RRule = %q, want FREQ=YEARLY", natal.RRule)
	}

	imp := cal.Events[1]
	if imp.Summary != "Implantação da República" {
		t.Errorf("Summary = %q", imp.Summary)
	}
	if !imp.AllDay {
		t.Error("AllDay = false, want true")
	}
	// No DTEND on an all-day event: End = Start + 1 day.
	if !imp.End.Equal(imp.Start.AddDate(0, 0, 1)) {
		t.Errorf("End = %v, want Start+1d (%v)", imp.End, imp.Start.AddDate(0, 0, 1))
	}
}

func TestParseFoldedFixture(t *testing.T) {
	cal, err := parseFixture(t, "folded.ics")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cal.Name != "Agenda Cultural" {
		t.Errorf("Name = %q, want %q (folded X-WR-CALNAME)", cal.Name, "Agenda Cultural")
	}
	if len(cal.Events) != 1 {
		t.Fatalf("got %d events, want 1", len(cal.Events))
	}
	ev := cal.Events[0]
	if want := "Concerto de fado — Amália: uma homenagem"; ev.Summary != want {
		t.Errorf("Summary = %q, want %q", ev.Summary, want)
	}
	if want := "Linha um\nLinha dois com vírgula, e espaço preservado"; ev.Description != want {
		t.Errorf("Description = %q, want %q", ev.Description, want)
	}
}

func TestParseMalformedFixture(t *testing.T) {
	cal, err := parseFixture(t, "malformed.ics")
	if len(cal.Events) != 1 {
		t.Fatalf("got %d events, want 1 (only good-1 survives)", len(cal.Events))
	}
	if cal.Events[0].UID != "good-1" {
		t.Errorf("surviving UID = %q, want good-1", cal.Events[0].UID)
	}
	if err == nil {
		t.Fatal("err = nil, want joined errors for dropped events")
	}
	msg := err.Error()
	for _, uid := range []string{"bad-dtstart", "no-dtstart", "never-closed"} {
		if !strings.Contains(msg, uid) {
			t.Errorf("error %q does not mention dropped UID %q", msg, uid)
		}
	}
}

func TestParseCRLF(t *testing.T) {
	lf, err := os.ReadFile(filepath.Join("testdata", "google.ics"))
	if err != nil {
		t.Fatal(err)
	}
	crlf := strings.ReplaceAll(string(lf), "\n", "\r\n")
	cal, err := Parse(strings.NewReader(crlf))
	if err != nil {
		t.Fatalf("Parse CRLF: %v", err)
	}
	if len(cal.Events) != 3 {
		t.Fatalf("got %d events, want 3", len(cal.Events))
	}
}

// TestParseHostileInputs feeds malformed, truncated, and adversarial input.
// The parser must return (never panic, never hang) on all of them.
func TestParseHostileInputs(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantEvents int // -1 = don't care, just must not panic
	}{
		{"empty", "", 0},
		{"only blank lines", "\r\n\r\n\n", 0},
		{"binary garbage", "\x00\x01\xff\xfe garbage \x7f", 0},
		{"colonless lines", "no colon\nstill none\n:::\n", 0},
		{"end without begin", "END:VEVENT\nEND:VCALENDAR\n", 0},
		{"begin flood", strings.Repeat("BEGIN:VEVENT\n", 1000), -1},
		{"huge single line", "SUMMARY:" + strings.Repeat("A", 1<<20), 0},
		{"orphan folds", " \n \n \n", 0},
		{"unterminated quote", "X;P=\"unterminated:value\nUID:x\n", 0},
		{
			"bare vevent without vcalendar",
			"BEGIN:VEVENT\nUID:x\nDTSTART:20260101T000000Z\nEND:VEVENT\n",
			1,
		},
		{
			"unknown tzid falls back to utc",
			"BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:x\nDTSTART;TZID=Nope/Zone:20260101T100000\nEND:VEVENT\nEND:VCALENDAR\n",
			1,
		},
		{
			"bad month dropped",
			"BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:x\nDTSTART:20261301T000000Z\nEND:VEVENT\nEND:VCALENDAR\n",
			0,
		},
		{
			"nested vevent tolerated",
			"BEGIN:VEVENT\nUID:outer\nDTSTART:20260101T000000Z\nBEGIN:VEVENT\nUID:inner\nEND:VEVENT\nEND:VEVENT\n",
			-1,
		},
		{
			"bad escapes survive",
			"BEGIN:VEVENT\nUID:x\nDTSTART:20260101T000000Z\nSUMMARY:bad \\x esc \\\nEND:VEVENT\n",
			1,
		},
		{
			"truncated mid line",
			"BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:x\nDTSTART:2026",
			0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cal, _ := Parse(strings.NewReader(tc.in))
			if tc.wantEvents >= 0 && len(cal.Events) != tc.wantEvents {
				t.Errorf("got %d events, want %d", len(cal.Events), tc.wantEvents)
			}
		})
	}
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		in     string
		want   time.Duration
		wantOK bool
	}{
		{"PT1H", time.Hour, true},
		{"P1D", 24 * time.Hour, true},
		{"P2W", 14 * 24 * time.Hour, true},
		{"PT90M", 90 * time.Minute, true},
		{"P1DT12H", 36 * time.Hour, true},
		{"PT1H30M15S", time.Hour + 30*time.Minute + 15*time.Second, true},
		{"-PT15M", -15 * time.Minute, true},
		{"+PT15M", 15 * time.Minute, true},
		{"PT0S", 0, true},
		{"", 0, false},
		{"P", 0, false},
		{"PT", 0, false},
		{"1H", 0, false},
		{"P1X", 0, false},
		{"PT1H tail", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, ok := parseDuration(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("parseDuration(%q) ok = %v, want %v", tc.in, ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Fatalf("parseDuration(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
