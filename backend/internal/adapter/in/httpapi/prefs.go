package httpapi

import (
	"net/http"

	"calendium/backend/internal/domain"
)

func (s *server) handleGetPrefs(w http.ResponseWriter, r *http.Request) {
	prefs, err := s.deps.Prefs.GetPrefs(r.Context(), userFrom(r).ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}

func (s *server) handleUpdatePrefs(w http.ResponseWriter, r *http.Request) {
	var in domain.UserPrefs
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	prefs, err := s.deps.Prefs.UpdatePrefs(r.Context(), userFrom(r).ID, in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}
