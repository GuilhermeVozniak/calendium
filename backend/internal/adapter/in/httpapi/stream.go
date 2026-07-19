package httpapi

import (
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

// handleCollabStream serves GET /v1/collab/stream: a text/event-stream of
// CollabEvents scoped to the authed caller — user:<uid> plus team:<id> for
// every team the caller belongs to. Frames are `event:`/`data:` pairs (the
// data payload is the full CollabEvent JSON); a `: keepalive` comment is
// written every keepaliveInterval. The handler exits — and unsubscribes —
// when the request context ends (client disconnect or server shutdown).
func (s *server) handleCollabStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok || s.deps.Events == nil {
		s.writeError(w, r, fmt.Errorf("%w: event streaming is not available", domain.ErrNotImplemented))
		return
	}

	user := userFrom(r)
	topics := []string{"user:" + user.ID}
	if s.deps.Teams != nil {
		teams, err := s.deps.Teams.List(r.Context(), user.ID)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		for _, t := range teams {
			topics = append(topics, "team:"+t.ID)
		}
	}

	events, cancel := s.deps.Events.Subscribe(topics)
	defer cancel()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no") // defeat reverse-proxy buffering
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, ": connected\n\n")
	flusher.Flush()

	ticker := time.NewTicker(keepaliveInterval)
	defer ticker.Stop()

	for {
		select {
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
