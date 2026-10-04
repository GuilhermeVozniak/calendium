package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// keepaliveInterval is how often an SSE comment tick is written so clients
// and intermediaries can tell a quiet stream from a dead one. A var (not a
// const) only so tests can shrink it.
var keepaliveInterval = 25 * time.Second

// collabTopics resolves the caller's CURRENT topic set: user:<uid> plus
// team:<id> for every team the caller belongs to right now.
func (s *server) collabTopics(ctx context.Context, userID string) ([]string, error) {
	topics := []string{"user:" + userID}
	if s.deps.Teams != nil {
		teams, err := s.deps.Teams.List(ctx, userID)
		if err != nil {
			return nil, err
		}
		for _, t := range teams {
			topics = append(topics, "team:"+t.ID)
		}
	}
	return topics, nil
}

// sameTopicSet reports whether a and b contain the same topics (order-free).
func sameTopicSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]struct{}, len(a))
	for _, t := range a {
		set[t] = struct{}{}
	}
	for _, t := range b {
		if _, ok := set[t]; !ok {
			return false
		}
	}
	return true
}

// handleCollabStream serves GET /v1/collab/stream: a text/event-stream of
// CollabEvents scoped to the authed caller — user:<uid> plus team:<id> for
// every team the caller belongs to. Frames are `event:`/`data:` pairs (the
// data payload is the full CollabEvent JSON); a `: keepalive` comment is
// written every keepaliveInterval. The handler exits — and unsubscribes —
// when the request context ends (client disconnect or server shutdown).
//
// Authorization is NOT frozen at connect time: memberships are re-resolved
// on every keepalive tick and the subscription is replaced whenever the
// topic set changed, so removal from (or joining) a team is reflected
// within one tick instead of persisting until disconnect. A failed
// re-resolution terminates the stream (fail closed — the client
// reconnects and re-authorizes).
func (s *server) handleCollabStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok || s.deps.Events == nil {
		s.writeError(w, r, fmt.Errorf("%w: event streaming is not available", domain.ErrNotImplemented))
		return
	}

	user := userFrom(r)
	topics, err := s.collabTopics(r.Context(), user.ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	events, cancel := s.deps.Events.Subscribe(topics)
	defer func() { cancel() }()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no") // defeat reverse-proxy buffering
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, ": connected\n\n")
	flusher.Flush()

	ticker := time.NewTicker(keepaliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.deps.Drain:
			return // server draining: EventSource reconnects against the next replica
		case <-r.Context().Done():
			return
		case ev, open := <-events:
			if !open {
				return
			}
			if err := writeSSEEvent(w, ev); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
			// Re-authorize: drop team topics the caller no longer belongs
			// to (and pick up new ones) by re-subscribing with the current
			// set. Stale, already-buffered events from a dropped topic are
			// discarded with the old subscription.
			current, err := s.collabTopics(r.Context(), user.ID)
			if err != nil {
				return // fail closed on a failed membership re-check
			}
			if !sameTopicSet(topics, current) {
				newEvents, newCancel := s.deps.Events.Subscribe(current)
				cancel()
				events, cancel, topics = newEvents, newCancel, current
			}
		}
	}
}

// writeSSEEvent renders one CollabEvent as an SSE frame. An event that
// cannot be marshalled (invalid RawMessage payload) is skipped — nil error
// — rather than corrupting the stream; a Type containing newlines (never
// produced internally) is omitted from the `event:` field for the same
// reason.
func writeSSEEvent(w io.Writer, ev port.CollabEvent) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return nil
	}
	if ev.Type != "" && !strings.ContainsAny(ev.Type, "\r\n") {
		if _, err := fmt.Fprintf(w, "event: %s\n", ev.Type); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}
