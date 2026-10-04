package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	"calendium/backend/internal/domain"
)

// --- auth --------------------------------------------------------------------

type userCtxKey struct{}

// userFrom returns the authenticated user placed in the context by
// requireAuth. Handlers behind requireAuth may rely on it being present.
func userFrom(r *http.Request) domain.User {
	u, _ := r.Context().Value(userCtxKey{}).(domain.User)
	return u
}

// requireAuth verifies the Better Auth bearer JWT and upserts the user row
// (docs/architecture.md: users are keyed by the JWT sub and created on
// first authenticated request).
func (s *server) requireAuth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			s.writeError(w, r, domain.ErrUnauthorized)
			return
		}
		identity, err := s.deps.Verifier.Verify(r.Context(), token)
		if err != nil {
			if errors.Is(err, domain.ErrUpstream) {
				s.writeError(w, r, err) // 502: the JWKS endpoint is down, the token may well be fine
				return
			}
			s.deps.Logger.Debug("token rejected", "error", err)
			writeJSON(w, http.StatusUnauthorized, errorBody{Error: errorDetail{
				Code:      "unauthorized",
				Message:   "invalid or expired access token",
				RequestID: requestIDFrom(r.Context()),
			}})
			return
		}
		user, err := s.deps.Users.EnsureUser(r.Context(), identity)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		setLogUser(r.Context(), user.ID, "")
		ctx := context.WithValue(r.Context(), userCtxKey{}, user)
		next(w, r.WithContext(ctx))
	})
}

func bearerToken(r *http.Request) (string, bool) {
	const prefix = "bearer "
	h := r.Header.Get("Authorization")
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(h[len(prefix):])
	return token, token != ""
}

// --- request logging ---------------------------------------------------------

// responseWriter is the ONE wrapper every middleware uses: it records the
// status and byte count, forwards Flush for the SSE streams, and exposes
// Unwrap so http.ResponseController keeps working through the stack.
type responseWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
	wrote  bool
}

func (w *responseWriter) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.wrote = true
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	w.wrote = true
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

// Flush forwards http.Flusher so streaming handlers keep working.
func (w *responseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// logFields is the mutable per-request record requireAuth/withActAs fill
// in; the outer logger cannot see values stored in inner contexts, so the
// pointer is placed in the context before the handler runs.
type logFields struct {
	UserID  string
	ActorID string
}

type logFieldsKey struct{}

// setLogUser records the effective user (principal under act-as) and the
// real actor for the request log line. No-op without logRequests.
func setLogUser(ctx context.Context, userID, actorID string) {
	if f, ok := ctx.Value(logFieldsKey{}).(*logFields); ok {
		f.UserID = userID
		if actorID != "" {
			f.ActorID = actorID
		}
	}
}

// redactPath replaces the secret-bearing segment of the tokenized public
// routes (and the contact email) with [redacted]; used only when no route
// pattern matched (404/405), since patterns never carry the token.
func redactPath(path string) string {
	for _, prefix := range []string{"/v1/shared/threads/", "/v1/public/polls/", "/v1/mail/contacts/"} {
		rest, ok := strings.CutPrefix(path, prefix)
		if !ok {
			continue
		}
		if _, tail, hasTail := strings.Cut(rest, "/"); hasTail {
			return prefix + "[redacted]/" + tail
		}
		return prefix + "[redacted]"
	}
	return path
}

// routeOf is the loggable route: the matched ServeMux pattern without its
// method ("/v1/mail/threads/{id}"), or the redacted raw path when nothing
// matched. Query strings are never included.
func routeOf(r *http.Request) string {
	if r.Pattern != "" {
		if _, route, ok := strings.Cut(r.Pattern, " "); ok {
			return route
		}
		return r.Pattern
	}
	return redactPath(r.URL.Path)
}

func (s *server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		fields := &logFields{}
		// This pointer is the one ServeMux mutates (r.Pattern), so keep it.
		r = r.WithContext(context.WithValue(r.Context(), logFieldsKey{}, fields))
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		s.deps.Logger.Info("http",
			"request_id", requestIDFrom(r.Context()),
			"method", r.Method,
			"route", routeOf(r),
			"status", rw.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"bytes", rw.bytes,
			"client_ip", s.clientIP(r),
			"user_id", fields.UserID,
			"actor_id", fields.ActorID,
		)
	})
}

// --- panic recovery ----------------------------------------------------------

func (s *server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if rec == http.ErrAbortHandler {
				panic(rec) // let the server abort the connection as intended
			}
			// recoverPanics sits outside requestID, so the id is read back
			// from the response header requestID already set.
			id := requestIDFrom(r.Context())
			if id == "" {
				id = w.Header().Get("X-Request-Id")
			}
			s.deps.Logger.Error("panic recovered",
				"method", r.Method, "route", routeOf(r), "request_id", id,
				"panic", rec, "stack", string(debug.Stack()))
			writeJSON(w, http.StatusInternalServerError, errorBody{Error: errorDetail{
				Code:      "internal",
				Message:   "internal server error",
				RequestID: id,
			}})
		}()
		next.ServeHTTP(w, r)
	})
}

// --- CORS ---------------------------------------------------------------------

// corsMiddleware reflects an allowed Origin so browser clients can call the
// API cross-origin. Always reflected: the packaged Wails WebView origins
// (production desktop clients) and every origin in allowedOrigins
// (CORS_ALLOWED_ORIGINS + PUBLIC_WEB_URL + BETTER_AUTH_URL). Reflected only
// when allowDevOrigins (ALLOW_DEV_ORIGINS=true): http(s)://localhost,
// 127.0.0.1 and ::1 on any port — the dev servers. Credentials are allowed
// (bearer JWTs); preflights get a 204.
func corsMiddleware(next http.Handler, allowedOrigins []string, allowDevOrigins bool) http.Handler {
	allow := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
			allow[o] = struct{}{}
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && originAllowed(origin, allow, allowDevOrigins) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func originAllowed(origin string, allow map[string]struct{}, allowDevOrigins bool) bool {
	if isWailsOrigin(origin) {
		return true
	}
	if allowDevOrigins && isLocalDevOrigin(origin) {
		return true
	}
	_, ok := allow[strings.TrimRight(origin, "/")]
	return ok
}

func isLocalDevOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// isWailsOrigin matches the packaged desktop WebView origins across
// platforms: wails://wails and wails://wails.localhost (macOS/Linux) and
// http(s)://wails.localhost (Windows, any port).
func isWailsOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()
	switch u.Scheme {
	case "wails":
		return host == "wails" || host == "wails.localhost"
	case "http", "https":
		return host == "wails.localhost"
	}
	return false
}
