package httpapi

import (
	"net/http"
	"strings"
)

// securityHeaders sets the static API headers on EVERY response (including
// CORS preflights and 404s), Cache-Control: no-store under /v1 (tokens and
// mail must never land in a shared cache), and HSTS only when the request
// is known to be HTTPS: direct TLS or X-Forwarded-Proto from a trusted
// proxy. It sits directly inside recoverPanics so a panic response carries
// the headers too.
func (s *server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		if r.URL.Path == "/v1" || strings.HasPrefix(r.URL.Path, "/v1/") {
			h.Set("Cache-Control", "no-store")
		}
		if r.TLS != nil || s.proxy.forwardedProto(r) == "https" {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}
