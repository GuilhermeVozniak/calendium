package httpapi

import (
	"fmt"
	"net/http"

	"calendium/backend/internal/domain"
)

// handlePlacesAutocomplete serves GET /v1/places/autocomplete?q=… (M2.8
// Task 11): up to 5 place suggestions for a partial location query. When no
// maps provider is configured the Places dep is nil and the route answers
// 501, matching the features.maps=false advertised by GET /v1/instance so
// clients hide the affordance. Query validation (min 3 chars) lives in the
// service.
func (s *server) handlePlacesAutocomplete(w http.ResponseWriter, r *http.Request) {
	if s.deps.Places == nil {
		s.writeError(w, r, fmt.Errorf("%w: maps are not available on this instance", domain.ErrNotImplemented))
		return
	}
	places, err := s.deps.Places.Autocomplete(r.Context(), userFrom(r).ID, r.URL.Query().Get("q"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, places)
}
