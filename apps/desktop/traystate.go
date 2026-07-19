package main

// Pure menu-bar tray state (Task 10): which event is next, the countdown
// string rendered beside the tray icon, and the text menu rows. No systray or
// Wails imports here so everything is unit-testable.

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
	"unicode/utf8"
)

const (
	// trayLookahead bounds "upcoming": events starting further out than this
	// never appear in the tray ("nothing in the next 12h" -> empty title).
	trayLookahead = 12 * time.Hour
	// trayNowWindow: how long after its start an event still counts as
	// "<title> now". TrayEvent carries no end time (the frontend already
	// filters out ended events and re-pushes every 60s); this is the host-side
	// fallback so a stale feed can't pin "now" forever.
	trayNowWindow = 10 * time.Minute
	// trayTitleMaxRunes caps the menu-bar title (macOS renders it verbatim).
	trayTitleMaxRunes = 24
	// trayMenuMaxEvents: how many event rows the tray menu shows.
	trayMenuMaxEvents = 5
)

// TrayEvent is one upcoming event as pushed by the frontend via the
// SetUpcomingEvents bound method (see frontend/src/lib/tray.ts).
type TrayEvent struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	StartAt time.Time `json:"startAt"`
	JoinURL string    `json:"joinUrl"` // empty when no conferencing
}

// decodeTrayEvents parses the JSON payload of SetUpcomingEvents.
func decodeTrayEvents(payload string) ([]TrayEvent, error) {
	var evs []TrayEvent
	if err := json.Unmarshal([]byte(payload), &evs); err != nil {
		return nil, fmt.Errorf("decode tray events: %w", err)
	}
	return evs, nil
}

// upcoming returns the tray-relevant events — not ended (grace window past
// start) and within the lookahead — sorted by start time.
func upcoming(events []TrayEvent, now time.Time) []TrayEvent {
	out := make([]TrayEvent, 0, len(events))
	for _, ev := range events {
		if now.Sub(ev.StartAt) > trayNowWindow {
			continue // ended
		}
		if ev.StartAt.Sub(now) > trayLookahead {
			continue // too far out
		}
		out = append(out, ev)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartAt.Before(out[j].StartAt) })
	return out
}

// nextEvent picks the earliest upcoming event, skipping ended ones.
func nextEvent(events []TrayEvent, now time.Time) (TrayEvent, bool) {
	up := upcoming(events, now)
	if len(up) == 0 {
		return TrayEvent{}, false
	}
	return up[0], true
}

// nextJoinable picks the earliest upcoming event that has a conferencing URL.
func nextJoinable(events []TrayEvent, now time.Time) (TrayEvent, bool) {
	for _, ev := range upcoming(events, now) {
		if ev.JoinURL != "" {
			return ev, true
		}
	}
	return TrayEvent{}, false
}

// trayTitle renders the menu-bar text: "Standup in 12m", "Standup now", or ""
// when nothing starts in the next 12h. Capped at trayTitleMaxRunes runes.
func trayTitle(events []TrayEvent, now time.Time) string {
	ev, ok := nextEvent(events, now)
	if !ok {
		return ""
	}
	suffix := "now"
	if ev.StartAt.After(now) {
		suffix = "in " + formatCountdown(ev.StartAt.Sub(now))
	}
	avail := trayTitleMaxRunes - utf8.RuneCountInString(suffix) - 1
	return truncateRunes(ev.Title, avail) + " " + suffix
}

// formatCountdown renders a duration as "12m", "1h", or "1h30m", rounding
// minutes UP so the countdown never understates how soon an event starts.
func formatCountdown(d time.Duration) string {
	mins := int((d + time.Minute - time.Nanosecond) / time.Minute)
	if mins < 1 {
		mins = 1
	}
	if mins < 60 {
		return fmt.Sprintf("%dm", mins)
	}
	h, m := mins/60, mins%60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%02dm", h, m)
}

// truncateRunes shortens s to at most max runes, ellipsizing when truncated.
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}

// menuRows formats up to max upcoming events as "10:30  Standup" rows.
func menuRows(events []TrayEvent, now time.Time, max int) []string {
	up := upcoming(events, now)
	if len(up) > max {
		up = up[:max]
	}
	rows := make([]string, 0, len(up))
	for _, ev := range up {
		rows = append(rows, ev.StartAt.Local().Format("15:04")+"  "+ev.Title)
	}
	return rows
}
