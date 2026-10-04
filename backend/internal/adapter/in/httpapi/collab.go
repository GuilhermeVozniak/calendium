package httpapi

import (
	"fmt"
	"net/http"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// Team-comment routes (M2.7 Task 9). All authorization — membership,
// thread visibility, author/admin rules — lives in the CollabService;
// these handlers only translate HTTP. Comment bodies are plain user text:
// stored verbatim, escaped render-side by the frontends (no HTML is
// accepted or stored here, so no sanitization pass applies).

// collab returns the comments service or ErrNotImplemented when the M2.7
// collab service is not wired (mirrors the Events/Teams deps pattern).
func (s *server) collab() (port.CollabService, error) {
	if s.deps.Collab == nil {
		return nil, fmt.Errorf("%w: team comments are not available", domain.ErrNotImplemented)
	}
	return s.deps.Collab, nil
}

// handleListComments serves GET /v1/mail/threads/{id}/comments?teamId=…
func (s *server) handleListComments(w http.ResponseWriter, r *http.Request) {
	svc, err := s.collab()
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	teamID := r.URL.Query().Get("teamId")
	if teamID == "" {
		s.writeError(w, r, fmt.Errorf("%w: teamId query parameter is required", domain.ErrValidation))
		return
	}
	comments, err := svc.ListComments(r.Context(), userFrom(r).ID, r.PathValue("id"), teamID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]domain.Comment{"comments": comments})
}

// handleAddComment serves POST /v1/mail/threads/{id}/comments.
func (s *server) handleAddComment(w http.ResponseWriter, r *http.Request) {
	svc, err := s.collab()
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var in port.CommentInput
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	var fc fieldCheck
	fc.title("teamId", in.TeamID)
	fc.text("body", in.Body)
	if err := fc.err(); err != nil {
		s.writeError(w, r, err)
		return
	}
	comment, err := svc.AddComment(r.Context(), userFrom(r).ID, r.PathValue("id"), in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, comment)
}

// handleUpdateComment serves PATCH /v1/comments/{id}.
func (s *server) handleUpdateComment(w http.ResponseWriter, r *http.Request) {
	svc, err := s.collab()
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	var fc fieldCheck
	fc.text("body", in.Body)
	if err := fc.err(); err != nil {
		s.writeError(w, r, err)
		return
	}
	comment, err := svc.UpdateComment(r.Context(), userFrom(r).ID, r.PathValue("id"), in.Body)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, comment)
}

// handleDeleteComment serves DELETE /v1/comments/{id}.
func (s *server) handleDeleteComment(w http.ResponseWriter, r *http.Request) {
	svc, err := s.collab()
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := svc.DeleteComment(r.Context(), userFrom(r).ID, r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
