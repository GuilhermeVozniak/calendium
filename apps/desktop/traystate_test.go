package main

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// Fixed local base time keeps countdown/clock formatting deterministic.
var trayBase = time.Date(2026, 7, 19, 10, 0, 0, 0, time.Local)

func trayEv(id, title string, start time.Time, joinURL string) TrayEvent {
	return TrayEvent{ID: id, Title: title, StartAt: start, JoinURL: joinURL}
}

func TestTrayTitle_FutureEventShowsMinutes(t *testing.T) {
	events := []TrayEvent{trayEv("1", "Standup", trayBase.Add(12*time.Minute), "")}
	if got := trayTitle(events, trayBase); got != "Standup in 12m" {
		t.Fatalf("trayTitle = %q, want %q", got, "Standup in 12m")
	}
}

func TestTrayTitle_CeilsPartialMinutes(t *testing.T) {
	// 90s out is "in 2m", never a dishonest "in 1m" (or "in 0m" for 30s).
	events := []TrayEvent{trayEv("1", "Standup", trayBase.Add(90*time.Second), "")}
	if got := trayTitle(events, trayBase); got != "Standup in 2m" {
		t.Fatalf("trayTitle = %q, want %q", got, "Standup in 2m")
	}
}

func TestTrayTitle_HourFormats(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{2 * time.Hour, "Sync in 2h"},
		{90 * time.Minute, "Sync in 1h30m"},
		{time.Hour, "Sync in 1h"},
	}
	for _, tc := range cases {
		events := []TrayEvent{trayEv("1", "Sync", trayBase.Add(tc.in), "")}
		if got := trayTitle(events, trayBase); got != tc.want {
			t.Errorf("trayTitle(+%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTrayTitle_InProgressEventShowsNow(t *testing.T) {
	events := []TrayEvent{trayEv("1", "Standup", trayBase.Add(-time.Minute), "")}
	if got := trayTitle(events, trayBase); got != "Standup now" {
		t.Fatalf("trayTitle = %q, want %q", got, "Standup now")
	}
}

func TestTrayTitle_EmptyWhenNoEvents(t *testing.T) {
	if got := trayTitle(nil, trayBase); got != "" {
		t.Fatalf("trayTitle = %q, want empty", got)
	}
}

func TestTrayTitle_EmptyWhenOnlyEndedEvents(t *testing.T) {
	// Started longer than the "now" window ago — treated as over.
	events := []TrayEvent{trayEv("1", "Standup", trayBase.Add(-trayNowWindow-time.Minute), "")}
	if got := trayTitle(events, trayBase); got != "" {
		t.Fatalf("trayTitle = %q, want empty", got)
	}
}

func TestTrayTitle_EmptyBeyondLookahead(t *testing.T) {
	events := []TrayEvent{trayEv("1", "Standup", trayBase.Add(13*time.Hour), "")}
	if got := trayTitle(events, trayBase); got != "" {
		t.Fatalf("trayTitle = %q, want empty (event beyond 12h)", got)
	}
}

func TestTrayTitle_CappedAt24Runes(t *testing.T) {
	events := []TrayEvent{trayEv("1", "Quarterly planning déjà-vu marathon", trayBase.Add(5*time.Minute), "")}
	got := trayTitle(events, trayBase)
	if n := utf8.RuneCountInString(got); n > 24 {
		t.Fatalf("trayTitle = %q (%d runes), want <= 24", got, n)
	}
	if !strings.HasSuffix(got, "… in 5m") {
		t.Fatalf("trayTitle = %q, want truncated title ending in %q", got, "… in 5m")
	}
}

func TestNextEvent_PicksEarliestUpcomingAndSkipsEnded(t *testing.T) {
	events := []TrayEvent{
		trayEv("later", "Later", trayBase.Add(3*time.Hour), ""),
		trayEv("ended", "Ended", trayBase.Add(-30*time.Minute), ""),
		trayEv("next", "Next", trayBase.Add(20*time.Minute), ""),
	}
	ev, ok := nextEvent(events, trayBase)
	if !ok || ev.ID != "next" {
		t.Fatalf("nextEvent = (%v, %v), want the earliest non-ended event %q", ev, ok, "next")
	}
}

func TestNextEvent_NoneWhenAllEndedOrFar(t *testing.T) {
	events := []TrayEvent{
		trayEv("ended", "Ended", trayBase.Add(-time.Hour), ""),
		trayEv("far", "Far", trayBase.Add(20*time.Hour), ""),
	}
	if _, ok := nextEvent(events, trayBase); ok {
		t.Fatal("nextEvent ok = true, want false")
	}
}

func TestMenuRows_FormatsSortsAndCaps(t *testing.T) {
	events := []TrayEvent{
		trayEv("2", "Design review", trayBase.Add(2*time.Hour), ""),
		trayEv("1", "Standup", trayBase.Add(30*time.Minute), ""),
		trayEv("ended", "Ended", trayBase.Add(-time.Hour), ""),
		trayEv("3", "1:1", trayBase.Add(3*time.Hour), ""),
	}
	got := menuRows(events, trayBase, 2)
	want := []string{"10:30  Standup", "12:00  Design review"}
	if len(got) != len(want) {
		t.Fatalf("menuRows = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("menuRows[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestNextJoinable_SkipsEventsWithoutURL(t *testing.T) {
	events := []TrayEvent{
		trayEv("1", "No conf", trayBase.Add(10*time.Minute), ""),
		trayEv("2", "Zoom", trayBase.Add(20*time.Minute), "https://zoom.us/j/123"),
	}
	ev, ok := nextJoinable(events, trayBase)
	if !ok || ev.ID != "2" {
		t.Fatalf("nextJoinable = (%v, %v), want event 2", ev, ok)
	}
}

func TestDecodeTrayEvents_Valid(t *testing.T) {
	payload := `[{"id":"e1","title":"Standup","startAt":"2026-07-19T10:30:00Z","joinUrl":"https://meet.google.com/abc-defg-hij"}]`
	evs, err := decodeTrayEvents(payload)
	if err != nil {
		t.Fatalf("decodeTrayEvents: %v", err)
	}
	if len(evs) != 1 || evs[0].ID != "e1" || evs[0].JoinURL != "https://meet.google.com/abc-defg-hij" {
		t.Fatalf("decodeTrayEvents = %+v, want the decoded event", evs)
	}
	if !evs[0].StartAt.Equal(time.Date(2026, 7, 19, 10, 30, 0, 0, time.UTC)) {
		t.Fatalf("StartAt = %v, want 2026-07-19T10:30:00Z", evs[0].StartAt)
	}
}

func TestDecodeTrayEvents_BadJSON(t *testing.T) {
	if _, err := decodeTrayEvents("{nope"); err == nil {
		t.Fatal("decodeTrayEvents(bad json) error = nil, want error")
	}
}

func TestSetUpcomingEvents_BadJSONReturnsErrorAndKeepsState(t *testing.T) {
	a := NewApp()
	good := `[{"id":"e1","title":"Standup","startAt":"2026-07-19T10:30:00Z","joinUrl":""}]`
	if err := a.SetUpcomingEvents(good); err != nil {
		t.Fatalf("SetUpcomingEvents(valid): %v", err)
	}
	if err := a.SetUpcomingEvents("{nope"); err == nil {
		t.Fatal("SetUpcomingEvents(bad json) = nil, want error")
	}
	a.tray.mu.Lock()
	defer a.tray.mu.Unlock()
	if len(a.tray.events) != 1 || a.tray.events[0].ID != "e1" {
		t.Fatalf("tray events = %+v, want the previously accepted event untouched", a.tray.events)
	}
}

func TestTrayManager_SetEventsAndTickUpdateTitle(t *testing.T) {
	tm := newTrayManager()
	var titles []string
	tm.mu.Lock()
	tm.setTitle = func(s string) { titles = append(titles, s) }
	tm.mu.Unlock()

	tm.SetEvents([]TrayEvent{trayEv("1", "Standup", time.Now().Add(10*time.Minute), "")})
	if len(titles) == 0 {
		t.Fatal("SetEvents did not update the tray title")
	}
	if !strings.HasPrefix(titles[len(titles)-1], "Standup in ") {
		t.Fatalf("title after SetEvents = %q, want a Standup countdown", titles[len(titles)-1])
	}

	tm.tick(time.Now().Add(9 * time.Minute))
	last := titles[len(titles)-1]
	if last != "Standup in 1m" {
		t.Fatalf("title after tick = %q, want %q", last, "Standup in 1m")
	}
}
