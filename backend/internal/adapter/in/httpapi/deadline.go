package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Per-handler context deadlines. Vars (not consts) so tests can shrink them.
var (
	// defaultHandlerDeadline bounds every authed non-stream route.
	defaultHandlerDeadline = 60 * time.Second
	// attachmentHandlerDeadline covers GET /v1/mail/attachments/{id}/content,
	// which proxies provider downloads.
	attachmentHandlerDeadline = 10 * time.Minute
)

// withDeadline puts a timeout on the request context and, when the handler
// returns having written NOTHING after the deadline passed, answers 504
// timeout. Unlike http.TimeoutHandler it never buffers, so Flusher and
// attachment streaming keep working; a partially written response is left
// exactly as the handler produced it.
func (s *server) withDeadline(d time.Duration, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), d)
		defer cancel()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next(rw, r.WithContext(ctx))
		if !rw.wrote && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			writeJSON(rw, http.StatusGatewayTimeout, errorBody{Error: errorDetail{
				Code: "timeout", Message: safeMessage("timeout"), RequestID: requestIDFrom(r.Context()),
			}})
		}
	}
}
