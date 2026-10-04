package httpapi

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"time"

	"calendium/backend/internal/domain"
)

const (
	internalSecretHeader = "X-Internal-Secret"
	// internalPathPrefix is the server-to-server surface: no CORS, no
	// public/user rate limits, secret-authenticated.
	internalPathPrefix = "/v1/internal/"
	// internalBodyLimit caps internal request bodies (the purge has none).
	internalBodyLimit = 1 << 10
	exportTimeout     = 10 * time.Minute
)

// internalSecretMatches compares the hex header to the configured secret in
// constant time over SHA-256 digests, so neither the length nor a prefix of
// the secret leaks through timing. Malformed or wrong-length hex never
// matches; case and surrounding whitespace are tolerated.
func internalSecretMatches(header string, secret []byte) bool {
	if len(secret) == 0 {
		return false
	}
	raw, err := hex.DecodeString(strings.TrimSpace(header))
	if err != nil {
		return false
	}
	got, want := sha256.Sum256(raw), sha256.Sum256(secret)
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1 && len(raw) == len(secret)
}

// handleInternalPurgeUser is DELETE /v1/internal/users/{id}: authenticated
// by the shared secret (never a JWT) and registered outside authed(...).
// Without a configured secret, a wired service, or a matching header it
// answers exactly like an unknown route, so probing cannot tell the two
// apart. The pattern is registered without a method so a wrong method is a
// 404 too (a 405 + Allow would reveal the route). Logs carry the route
// pattern, never the id, email or team names.
func (s *server) handleInternalPurgeUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete || s.deps.Lifecycle == nil ||
		!internalSecretMatches(r.Header.Get(internalSecretHeader), s.deps.InternalSecret) {
		http.NotFound(w, r)
		return
	}
	if r.ContentLength > internalBodyLimit {
		s.writeError(w, r, errPayloadTooLarge)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, internalBodyLimit)
	if _, err := s.deps.Lifecycle.Purge(r.Context(), r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// exportSink adapts the HTTP response into a port.ExportSink: the headers
// and the 200 status go out on the first file, the zip is flushed to the
// client after every file, and a failure before the first file still gets a
// JSON error envelope because nothing has been written yet.
type exportSink struct {
	w        http.ResponseWriter
	rc       *http.ResponseController
	zw       *zip.Writer
	filename string
	started  bool
}

func (s *exportSink) Create(name string) (io.Writer, error) {
	if s.started {
		if err := s.zw.Flush(); err != nil {
			return nil, err
		}
		_ = s.rc.Flush()
	} else {
		h := s.w.Header()
		h.Set("Content-Type", "application/zip")
		h.Set("Content-Disposition", `attachment; filename="`+s.filename+`"`)
		h.Set("Cache-Control", "no-store")
		s.w.WriteHeader(http.StatusOK)
		s.started = true
	}
	return s.zw.Create(name)
}

// handleExport is GET /v1/me/export: streams calendium-export-<date>.zip
// under a 10-minute budget. Act-as is rejected before this runs (the route
// is absent from delegationScopeForRoute → 403). Not entitlement-gated.
func (s *server) handleExport(w http.ResponseWriter, r *http.Request) {
	if s.deps.Export == nil {
		s.writeError(w, r, domain.ErrNotImplemented)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), exportTimeout)
	defer cancel()
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(exportTimeout))
	userID := userFrom(r).ID
	sink := &exportSink{
		w: w, rc: rc, zw: zip.NewWriter(w),
		filename: "calendium-export-" + time.Now().UTC().Format("2006-01-02") + ".zip",
	}
	if err := s.deps.Export.Export(ctx, userID, sink); err != nil {
		if !sink.started {
			s.writeError(w, r, err)
			return
		}
		// Bytes are already on the wire: abort without the central
		// directory so the client sees a corrupt (incomplete) zip rather
		// than a plausible partial export. The throttle slot stays claimed.
		s.deps.Logger.Error("export aborted mid-stream", "userId", userID, "error", err)
		panic(http.ErrAbortHandler)
	}
	if err := sink.zw.Close(); err != nil {
		s.deps.Logger.Error("export close failed", "userId", userID, "error", err)
		return
	}
	_ = rc.Flush()
}
