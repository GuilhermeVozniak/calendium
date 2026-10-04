package httpapi

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- M2.7 Task 7: shared conversations ---------------------------------------

// threadShareCreated is the POST /v1/mail/threads/{id}/share response: the
// stored share plus the raw link token, which appears here exactly once
// (only its hash is persisted).
type threadShareCreated struct {
	Share domain.ThreadShare `json:"share"`
	Token string             `json:"token"`
}

func (s *server) handleShareThread(w http.ResponseWriter, r *http.Request) {
	var in port.ShareThreadInput
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	share, token, err := s.deps.Collab.ShareThread(r.Context(), userFrom(r).ID, r.PathValue("id"), in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, threadShareCreated{Share: share, Token: token})
}

func (s *server) handleListThreadShares(w http.ResponseWriter, r *http.Request) {
	shares, err := s.deps.Collab.ListThreadShares(r.Context(), userFrom(r).ID, r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, shares)
}

func (s *server) handleRevokeThreadShare(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Collab.RevokeThreadShare(r.Context(), userFrom(r).ID, r.PathValue("id"), r.PathValue("shareId")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sharedViewer resolves the OPTIONAL bearer on the public share routes: no
// Authorization header means an anonymous viewer (nil), a valid bearer
// identifies the viewer for team-audience membership checks, and an invalid
// bearer fails closed as ErrNotFound — the public surface never explains
// itself (no 401 oracle).
func (s *server) sharedViewer(r *http.Request) (*string, error) {
	token, ok := bearerToken(r)
	if !ok {
		return nil, nil
	}
	identity, err := s.deps.Verifier.Verify(r.Context(), token)
	if err != nil {
		return nil, domain.ErrNotFound
	}
	user, err := s.deps.Users.EnsureUser(r.Context(), identity)
	if err != nil {
		return nil, domain.ErrNotFound
	}
	return &user.ID, nil
}

// handleGetSharedThread serves GET /v1/shared/threads/{token}: the
// read-only share projection. Unauthenticated by design; unknown, revoked,
// and expired tokens are uniformly 404.
func (s *server) handleGetSharedThread(w http.ResponseWriter, r *http.Request) {
	viewer, err := s.sharedViewer(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	view, err := s.deps.Collab.GetSharedThread(r.Context(), r.PathValue("token"), viewer)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleSharedThreadStream serves GET /v1/shared/threads/{token}/stream: an
// SSE stream of share.updated events on topic "share:<id>", with the same
// fail-closed auth semantics as the share view. Mirrors handleCollabStream's
// framing (initial comment + keepalive ticks).
func (s *server) handleSharedThreadStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok || s.deps.Events == nil {
		s.writeError(w, r, fmt.Errorf("%w: event streaming is not available", domain.ErrNotImplemented))
		return
	}
	viewer, err := s.sharedViewer(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	shareID, err := s.deps.Collab.ResolveShare(r.Context(), r.PathValue("token"), viewer)
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	events, cancel := s.deps.Events.Subscribe([]string{"share:" + shareID})
	defer cancel()

	// The share is re-authorized on every keepalive tick below — a revoked
	// or expired share stops streaming within one tick instead of living
	// until disconnect.

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
			if _, err := s.deps.Collab.ResolveShare(r.Context(), r.PathValue("token"), viewer); err != nil {
				return // fail closed: revocation/expiry ends the stream
			}
		}
	}
}
