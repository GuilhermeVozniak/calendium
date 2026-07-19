// Package eventbus is the in-process port.EventBus broker: a mutex-guarded
// subscriber set with per-subscriber buffered channels. Publish never
// blocks — a subscriber whose buffer is full simply misses the event (SSE
// clients re-sync on reconnect), so a slow consumer can never stall the
// publisher or grow memory without bound. Single-process by design; the
// multi-instance path is a Postgres LISTEN/NOTIFY adapter behind the same
// port.
package eventbus

import (
	"sync"

	"calendium/backend/internal/port"
)

// subscriberBuffer is each subscriber's channel capacity. Events published
// while the buffer is full are dropped for that subscriber only.
const subscriberBuffer = 64

// Bus implements port.EventBus. The zero value is not usable; call New.
type Bus struct {
	mu   sync.Mutex
	subs map[*subscriber]struct{}
}

type subscriber struct {
	topics map[string]struct{}
	ch     chan port.CollabEvent
}

// New returns an empty broker ready for Publish/Subscribe.
func New() *Bus {
	return &Bus{subs: make(map[*subscriber]struct{})}
}

// Publish delivers ev to every subscriber of ev.Topic without blocking:
// the non-blocking send drops the event for subscribers whose buffer is
// full. Sends happen under the mutex, so a concurrent cancel (which
// removes the subscriber under the same mutex before closing its channel)
// can never race a send onto a closed channel.
func (b *Bus) Publish(ev port.CollabEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for s := range b.subs {
		if _, ok := s.topics[ev.Topic]; !ok {
			continue
		}
		select {
		case s.ch <- ev:
		default: // slow consumer: drop, never block
		}
	}
}

// Subscribe registers a subscriber for the given topics and returns its
// event channel plus an idempotent cancel that unregisters and closes the
// channel.
func (b *Bus) Subscribe(topics []string) (<-chan port.CollabEvent, func()) {
	s := &subscriber{
		topics: make(map[string]struct{}, len(topics)),
		ch:     make(chan port.CollabEvent, subscriberBuffer),
	}
	for _, t := range topics {
		s.topics[t] = struct{}{}
	}

	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			delete(b.subs, s)
			// Safe under the mutex: no publisher can hold a reference to
			// s.ch once it is out of the map.
			close(s.ch)
		})
	}
	return s.ch, cancel
}
