package httpapi

import (
	"net/http"

	"calendium/backend/internal/port"
)

// handleListClassifiers is GET /v1/classifiers.
func (s *server) handleListClassifiers(w http.ResponseWriter, r *http.Request) {
	cs, err := s.deps.AI.ListClassifiers(r.Context(), userFrom(r).ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, cs)
}

// handleCreateClassifier is POST /v1/classifiers.
func (s *server) handleCreateClassifier(w http.ResponseWriter, r *http.Request) {
	var in port.ClassifierInput
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	c, err := s.deps.AI.CreateClassifier(r.Context(), userFrom(r).ID, in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// handleUpdateClassifier is PATCH /v1/classifiers/{id} (full-replace payload,
// matching the event-template/calendar-set PUT-style handlers).
func (s *server) handleUpdateClassifier(w http.ResponseWriter, r *http.Request) {
	var in port.ClassifierInput
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	c, err := s.deps.AI.UpdateClassifier(r.Context(), userFrom(r).ID, r.PathValue("id"), in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// handleDeleteClassifier is DELETE /v1/classifiers/{id}.
func (s *server) handleDeleteClassifier(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.AI.DeleteClassifier(r.Context(), userFrom(r).ID, r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
