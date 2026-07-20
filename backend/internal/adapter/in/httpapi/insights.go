package httpapi

import (
	"fmt"
	"net/http"
	"time"

	"calendium/backend/internal/domain"
)

// handleGetTimeInsights serves GET /v1/insights/time?from&to (M2.8 Task 17):
// aggregated time analytics for [from, to), computed from the local mirror
// only. from/to are required RFC 3339 timestamps; the service caps the range
// at 92 days.
func (s *server) handleGetTimeInsights(w http.ResponseWriter, r *http.Request) {
	if s.deps.Insights == nil {
		s.writeError(w, r, fmt.Errorf("%w: insights are not available on this instance", domain.ErrNotImplemented))
		return
	}
	qs := r.URL.Query()
	from, err := time.Parse(time.RFC3339, qs.Get("from"))
	if err != nil {
		s.writeError(w, r, fmt.Errorf("%w: from must be an RFC 3339 timestamp: %v", domain.ErrValidation, err))
		return
	}
	to, err := time.Parse(time.RFC3339, qs.Get("to"))
	if err != nil {
		s.writeError(w, r, fmt.Errorf("%w: to must be an RFC 3339 timestamp: %v", domain.ErrValidation, err))
		return
	}
	insights, err := s.deps.Insights.TimeInsights(r.Context(), userFrom(r).ID, from, to)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, insights)
}
