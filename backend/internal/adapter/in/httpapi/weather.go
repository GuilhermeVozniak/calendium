package httpapi

import (
	"fmt"
	"net/http"
	"strconv"

	"calendium/backend/internal/domain"
)

// handleGetWeather serves GET /v1/weather?lat&lon&days&tz (M2.8 Task 13):
// up to 14 days of forecast for one location. Weather is best-effort
// decoration — when no vendor is configured (Deps.Weather nil) it answers
// 501 and clients hide the chips.
func (s *server) handleGetWeather(w http.ResponseWriter, r *http.Request) {
	if s.deps.Weather == nil {
		s.writeError(w, r, fmt.Errorf("%w: weather is not configured on this instance", domain.ErrNotImplemented))
		return
	}
	qs := r.URL.Query()
	lat, err := strconv.ParseFloat(qs.Get("lat"), 64)
	if err != nil {
		s.writeError(w, r, fmt.Errorf("%w: lat must be a number: %v", domain.ErrValidation, err))
		return
	}
	lon, err := strconv.ParseFloat(qs.Get("lon"), 64)
	if err != nil {
		s.writeError(w, r, fmt.Errorf("%w: lon must be a number: %v", domain.ErrValidation, err))
		return
	}
	tz := qs.Get("tz")
	if tz == "" {
		s.writeError(w, r, fmt.Errorf("%w: tz is required (IANA time zone)", domain.ErrValidation))
		return
	}
	days := 7
	if v := qs.Get("days"); v != "" {
		days, err = strconv.Atoi(v)
		if err != nil {
			s.writeError(w, r, fmt.Errorf("%w: days must be an integer: %v", domain.ErrValidation, err))
			return
		}
	}
	forecast, err := s.deps.Weather.Forecast(r.Context(), userFrom(r).ID, lat, lon, tz, days)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, forecast)
}
