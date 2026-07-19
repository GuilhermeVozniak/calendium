package main

import (
	"sync"
	"testing"
	"time"
)

// schedHarness drives autoJoinScheduler with a fake clock and fake timers so
// tests control exactly when a scheduled auto-join "fires".
type schedHarness struct {
	now time.Time

	mu     sync.Mutex
	timers []chan time.Time

	afterCalls chan time.Duration
	opened     chan string
	joined     chan TrayEvent
}

func newSchedHarness() *schedHarness {
	return &schedHarness{
		now:        time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC),
		afterCalls: make(chan time.Duration, 16),
		opened:     make(chan string, 16),
		joined:     make(chan TrayEvent, 16),
	}
}

func (h *schedHarness) scheduler() *autoJoinScheduler {
	s := newAutoJoinScheduler(
		func() time.Time { return h.now },
		func(d time.Duration) <-chan time.Time {
			ch := make(chan time.Time, 1)
			h.mu.Lock()
			h.timers = append(h.timers, ch)
			h.mu.Unlock()
			h.afterCalls <- d
			return ch
		},
	)
	s.setSinks(
		func(url string) { h.opened <- url },
		func(ev TrayEvent) { h.joined <- ev },
	)
	return s
}

// fireLatest triggers the most recently created fake timer.
func (h *schedHarness) fireLatest(t *testing.T) {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.timers) == 0 {
		t.Fatal("no timer scheduled")
	}
	h.timers[len(h.timers)-1] <- h.now
}

func waitFor[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		panic("unreachable")
	}
}

func expectQuiet[T any](t *testing.T, ch <-chan T, what string) {
	t.Helper()
	select {
	case v := <-ch:
		t.Fatalf("unexpected %s: %v", what, v)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestAutoJoin_FiresAtStartMinusLead(t *testing.T) {
	h := newSchedHarness()
	s := h.scheduler()
	s.SetConfig(true, 30*time.Second)

	ev := TrayEvent{ID: "e1", Title: "Standup", StartAt: h.now.Add(10 * time.Minute), JoinURL: "https://zoom.us/j/1"}
	s.SetEvents([]TrayEvent{ev})

	if d := waitFor(t, h.afterCalls, "schedule"); d != 10*time.Minute-30*time.Second {
		t.Fatalf("scheduled after %v, want start-lead = 9m30s", d)
	}
	h.fireLatest(t)
	if url := waitFor(t, h.opened, "openURL"); url != ev.JoinURL {
		t.Fatalf("opened %q, want %q", url, ev.JoinURL)
	}
	if got := waitFor(t, h.joined, "onJoined"); got.ID != "e1" {
		t.Fatalf("onJoined event = %+v, want e1", got)
	}
}

func TestAutoJoin_SkipsEventsWithoutJoinURL(t *testing.T) {
	h := newSchedHarness()
	s := h.scheduler()
	s.SetConfig(true, 0)

	s.SetEvents([]TrayEvent{
		{ID: "nourl", Title: "Focus", StartAt: h.now.Add(5 * time.Minute)},
		{ID: "zoom", Title: "Zoom", StartAt: h.now.Add(10 * time.Minute), JoinURL: "https://zoom.us/j/2"},
	})

	if d := waitFor(t, h.afterCalls, "schedule"); d != 10*time.Minute {
		t.Fatalf("scheduled after %v, want 10m (the event WITH a join URL)", d)
	}
	h.fireLatest(t)
	if url := waitFor(t, h.opened, "openURL"); url != "https://zoom.us/j/2" {
		t.Fatalf("opened %q, want the zoom URL", url)
	}
}

func TestAutoJoin_ExactlyOncePerEventAcrossRepushes(t *testing.T) {
	h := newSchedHarness()
	s := h.scheduler()
	s.SetConfig(true, 0)

	evs := []TrayEvent{{ID: "e1", Title: "Standup", StartAt: h.now.Add(time.Minute), JoinURL: "https://zoom.us/j/1"}}
	s.SetEvents(evs)
	waitFor(t, h.afterCalls, "schedule")
	h.fireLatest(t)
	waitFor(t, h.opened, "openURL")
	waitFor(t, h.joined, "onJoined")

	// The frontend re-pushes the same feed every 60s — the already-joined
	// event must never be opened again.
	s.SetEvents(evs)
	expectQuiet(t, h.afterCalls, "reschedule of an already-joined event")
	expectQuiet(t, h.opened, "second openURL")
}

func TestAutoJoin_ReschedulesWhenNearerEventArrives(t *testing.T) {
	h := newSchedHarness()
	s := h.scheduler()
	s.SetConfig(true, 0)

	s.SetEvents([]TrayEvent{{ID: "far", Title: "Far", StartAt: h.now.Add(30 * time.Minute), JoinURL: "https://zoom.us/j/far"}})
	waitFor(t, h.afterCalls, "first schedule")
	h.mu.Lock()
	staleTimer := h.timers[len(h.timers)-1]
	h.mu.Unlock()

	s.SetEvents([]TrayEvent{
		{ID: "near", Title: "Near", StartAt: h.now.Add(2 * time.Minute), JoinURL: "https://zoom.us/j/near"},
		{ID: "far", Title: "Far", StartAt: h.now.Add(30 * time.Minute), JoinURL: "https://zoom.us/j/far"},
	})
	if d := waitFor(t, h.afterCalls, "reschedule"); d != 2*time.Minute {
		t.Fatalf("rescheduled after %v, want 2m for the nearer event", d)
	}

	// The superseded timer firing late must be ignored (cancelled wait).
	staleTimer <- h.now
	expectQuiet(t, h.opened, "open from the superseded timer")

	h.fireLatest(t)
	if url := waitFor(t, h.opened, "openURL"); url != "https://zoom.us/j/near" {
		t.Fatalf("opened %q, want the nearer event's URL", url)
	}
}

func TestAutoJoin_DisableCancelsPendingTimer(t *testing.T) {
	h := newSchedHarness()
	s := h.scheduler()
	s.SetConfig(true, 0)

	s.SetEvents([]TrayEvent{{ID: "e1", Title: "Standup", StartAt: h.now.Add(time.Minute), JoinURL: "https://zoom.us/j/1"}})
	waitFor(t, h.afterCalls, "schedule")
	h.mu.Lock()
	timer := h.timers[len(h.timers)-1]
	h.mu.Unlock()

	s.SetConfig(false, 0)
	timer <- h.now
	expectQuiet(t, h.opened, "open after disable")
	expectQuiet(t, h.joined, "join after disable")
}

func TestAutoJoin_DisabledByDefaultSchedulesNothing(t *testing.T) {
	h := newSchedHarness()
	s := h.scheduler()

	s.SetEvents([]TrayEvent{{ID: "e1", Title: "Standup", StartAt: h.now.Add(time.Minute), JoinURL: "https://zoom.us/j/1"}})
	expectQuiet(t, h.afterCalls, "schedule while disabled")
}

func TestAutoJoin_StaleEventsNeverJoined(t *testing.T) {
	h := newSchedHarness()
	s := h.scheduler()
	s.SetConfig(true, 0)

	// Started 5 minutes ago (> 2min stale cutoff) — never joined.
	s.SetEvents([]TrayEvent{{ID: "stale", Title: "Old", StartAt: h.now.Add(-5 * time.Minute), JoinURL: "https://zoom.us/j/old"}})
	expectQuiet(t, h.afterCalls, "schedule of a stale event")

	// But an event that started 1 minute ago is still fair game (fires now).
	s.SetEvents([]TrayEvent{{ID: "justnow", Title: "Just started", StartAt: h.now.Add(-time.Minute), JoinURL: "https://zoom.us/j/now"}})
	if d := waitFor(t, h.afterCalls, "immediate schedule"); d != 0 {
		t.Fatalf("scheduled after %v, want 0 (fire immediately)", d)
	}
	h.fireLatest(t)
	if url := waitFor(t, h.opened, "openURL"); url != "https://zoom.us/j/now" {
		t.Fatalf("opened %q, want the just-started event", url)
	}
}
