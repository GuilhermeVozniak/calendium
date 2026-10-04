package httpapi

import (
	"context"
	"net/http"
	"time"
)

// readyzPingTimeout bounds the dependency ping; a var so tests can shrink it.
var readyzPingTimeout = 2 * time.Second

// handleReadyz is the orchestrator readiness probe (compose/k8s), distinct
// from the static /healthz liveness: 503 draining once shutdown started,
// 503 db_unavailable when the ping fails or hangs past readyzPingTimeout,
// else 200 ok. It reveals DB state, so proxies must not route it.
func (s *server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if s.deps.Drain != nil {
		select {
		case <-s.deps.Drain:
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "draining"})
			return
		default:
		}
	}
	if s.deps.Ready != nil {
		ctx, cancel := context.WithTimeout(r.Context(), readyzPingTimeout)
		defer cancel()
		done := make(chan error, 1) // buffered: a hung ping's goroutine never blocks on send
		go func() { done <- s.deps.Ready(ctx) }()
		select {
		case err := <-done:
			if err != nil {
				s.deps.Logger.Warn("readyz: dependency ping failed", "error", err)
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db_unavailable"})
				return
			}
		case <-ctx.Done():
			s.deps.Logger.Warn("readyz: dependency ping timed out", "timeout", readyzPingTimeout.String())
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db_unavailable"})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
