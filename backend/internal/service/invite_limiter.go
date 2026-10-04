package service

import (
	"sync"
	"time"
)

// rollingLimiter is an in-memory per-key budget of at most limit events per
// rolling window. In-memory is deliberate (single API process; a restart
// merely resets budgets), matching the program's other rate limits.
type rollingLimiter struct {
	limit  int
	window time.Duration

	mu     sync.Mutex
	events map[string][]time.Time // ascending
}

func newRollingLimiter(limit int, window time.Duration) *rollingLimiter {
	return &rollingLimiter{limit: limit, window: window, events: map[string][]time.Time{}}
}

// reserve records one event for key at now and reports true, or reports
// false (recording nothing) when key already used its budget in the window
// ending at now. Check and record are atomic, so concurrent callers cannot
// overshoot the limit.
func (l *rollingLimiter) reserve(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-l.window)
	evs := l.events[key]
	i := 0
	for i < len(evs) && !evs[i].After(cutoff) {
		i++
	}
	evs = evs[i:]
	if len(evs) >= l.limit {
		l.events[key] = evs
		return false
	}
	l.events[key] = append(evs, now)
	return true
}

// release gives back the event reserved at at (an attempt that never
// reached a send), dropping the key once it is empty.
func (l *rollingLimiter) release(key string, at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	evs := l.events[key]
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Equal(at) {
			evs = append(evs[:i], evs[i+1:]...)
			break
		}
	}
	if len(evs) == 0 {
		delete(l.events, key)
		return
	}
	l.events[key] = evs
}
