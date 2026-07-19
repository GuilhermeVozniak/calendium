package httpapi

import (
	"fmt"
	"net/http"

	"calendium/backend/internal/domain"
)

// handleTeamThreadActivity serves GET /v1/mail/threads/{id}/team-activity:
// teammate read/replied indicators for the caller's thread (M2.7 Task 10).
// Rows only ever cover members who opted in via share_read_statuses; the
// service enforces thread ownership and team scoping.
func (s *server) handleTeamThreadActivity(w http.ResponseWriter, r *http.Request) {
	if s.deps.TeamActivity == nil {
		s.writeError(w, r, fmt.Errorf("%w: team activity is not available", domain.ErrNotImplemented))
		return
	}
	acts, err := s.deps.TeamActivity.TeamThreadActivity(r.Context(), userFrom(r).ID, r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, acts)
}
