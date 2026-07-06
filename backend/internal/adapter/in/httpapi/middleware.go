package httpapi

import (
	"context"
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
			s.deps.Logger.Debug("token rejected", "error", err)
			writeJSON(w, http.StatusUnauthorized, errorBody{Error: errorDetail{
				Code:    "unauthorized",
				Message: "invalid or expired access token",
			}})
			return
		}
		user, err := s.deps.Users.EnsureUser(r.Context(), identity)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
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

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.deps.Logger.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
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
			s.deps.Logger.Error("panic recovered",
				"method", r.Method, "path", r.URL.Path,
				"panic", rec, "stack", string(debug.Stack()))
			writeJSON(w, http.StatusInternalServerError, errorBody{Error: errorDetail{
				Code:    "internal",
				Message: "internal server error",
			}})
		}()
		next.ServeHTTP(w, r)
	})
}

// --- CORS ---------------------------------------------------------------------

// corsMiddleware reflects an allowed Origin so browser clients can call the
// API cross-origin: localhost/127.0.0.1 dev servers (web/desktop/Expo, any
// port), the packaged Wails WebView origins, and any origin in the
// CORS_ALLOWED_ORIGINS env allowlist. Credentials are allowed (bearer JWTs);
// preflights get a 204.
func corsMiddleware(next http.Handler, allowedOrigins []string) http.Handler {
	allow := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
			allow[o] = struct{}{}
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && originAllowed(origin, allow) {
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

func originAllowed(origin string, allow map[string]struct{}) bool {
	if isLocalDevOrigin(origin) || isWailsOrigin(origin) {
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
