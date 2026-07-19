package main

// Auto-join scheduler (Task 11): opens the conferencing link of the next
// upcoming event at StartAt-lead, exactly once per event, only while the user
// has auto-join enabled. Pure core — clock and timer are injected so tests
// drive it deterministically; production wiring lives in tray.go
// (initDesktopExtras / startDesktopExtras).

import (
	"sync"
	"time"
)

// autoJoinedEvent is emitted to the frontend after a successful auto-join
// (payload: the TrayEvent) so the UI can show an honest "Joined <title>" toast.
const autoJoinedEvent = "auto-joined"

// autoJoinStaleAfter: events whose start already passed by more than this are
// never auto-joined (e.g. the app was closed over the meeting start).
const autoJoinStaleAfter = 2 * time.Minute

type autoJoinScheduler struct {
	mu       sync.Mutex
	enabled  bool
	lead     time.Duration
	now      func() time.Time                        // injected clock
	after    func(d time.Duration) <-chan time.Time // injected timer
	openURL  func(string)
	onJoined func(ev TrayEvent)
	joined   map[string]bool // event IDs already opened — never open twice
	events   []TrayEvent
	cancel   chan struct{} // closing aborts the pending wait; nil when idle
}

func newAutoJoinScheduler(now func() time.Time, after func(d time.Duration) <-chan time.Time) *autoJoinScheduler {
	return &autoJoinScheduler{now: now, after: after, joined: make(map[string]bool)}
}

// setSinks wires the effect callbacks (runtime.BrowserOpenURL + the
// auto-joined event emit in production; capture channels in tests).
func (s *autoJoinScheduler) setSinks(openURL func(string), onJoined func(TrayEvent)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.openURL, s.onJoined = openURL, onJoined
}

// SetEvents replaces the feed (same TrayEvent feed as the tray, pushed via
// SetUpcomingEvents) and reschedules to the next joinable start-lead.
func (s *autoJoinScheduler) SetEvents(evs []TrayEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append([]TrayEvent(nil), evs...)
	s.rescheduleLocked()
}

// SetConfig applies the user's auto-join setting. Disabled cancels all timers.
func (s *autoJoinScheduler) SetConfig(enabled bool, lead time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled, s.lead = enabled, lead
	s.rescheduleLocked()
}

// rescheduleLocked cancels any pending wait and, when enabled, schedules the
// next un-joined event that has a JoinURL and isn't stale. Callers hold s.mu.
func (s *autoJoinScheduler) rescheduleLocked() {
	if s.cancel != nil {
		close(s.cancel)
		s.cancel = nil
	}
	if !s.enabled {
		return
	}
	now := s.now()
	var next *TrayEvent
	for i := range s.events {
		ev := s.events[i]
		if ev.JoinURL == "" || s.joined[ev.ID] {
			continue
		}
		if now.Sub(ev.StartAt) > autoJoinStaleAfter {
			continue // started too long ago — never join late
		}
		if next == nil || ev.StartAt.Before(next.StartAt) {
			copyEv := ev
			next = &copyEv
		}
	}
	if next == nil {
		return
	}
	wait := next.StartAt.Add(-s.lead).Sub(now)
	if wait < 0 {
		wait = 0
	}
	cancel := make(chan struct{})
	s.cancel = cancel
	timer := s.after(wait)
	id := next.ID
	go func() {
		select {
		case <-timer:
			s.fire(id, cancel)
		case <-cancel:
		}
	}()
}

// fire opens the event's conferencing link exactly once, notifies the
// frontend, and schedules the following event. A wait that was superseded by
// a reschedule (cancel no longer current) is ignored even if its timer races
// the cancellation.
func (s *autoJoinScheduler) fire(id string, cancel chan struct{}) {
	s.mu.Lock()
	if s.cancel != cancel {
		s.mu.Unlock()
		return // superseded or cancelled
	}
	s.cancel = nil
	if !s.enabled || s.joined[id] {
		s.rescheduleLocked()
		s.mu.Unlock()
		return
	}
	var ev *TrayEvent
	for i := range s.events {
		if s.events[i].ID == id {
			ev = &s.events[i]
			break
		}
	}
	if ev == nil {
		s.rescheduleLocked()
		s.mu.Unlock()
		return
	}
	s.joined[id] = true
	joined := *ev
	openURL, onJoined := s.openURL, s.onJoined
	s.rescheduleLocked()
	s.mu.Unlock()

	if openURL != nil {
		openURL(joined.JoinURL)
	}
	if onJoined != nil {
		onJoined(joined)
	}
}

// SetAutoJoin is bound to the frontend (window.go.main.App): the Settings
// toggle pushes the user's real preference on startup and on every change.
func (a *App) SetAutoJoin(enabled bool, leadSeconds int) {
	a.autoJoin.SetConfig(enabled, time.Duration(leadSeconds)*time.Second)
}
