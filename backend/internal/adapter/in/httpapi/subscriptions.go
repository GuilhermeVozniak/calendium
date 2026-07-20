package httpapi

import (
	"net/http"

	"calendium/backend/internal/port"
)

// Interesting-calendar ICS feed subscriptions (M2.8 Task 15). Thin JSON ↔
// port translation only; validation (https-only), the synchronous first
// fetch, and ownership checks all live in the calendar service.

func (s *server) handleListCalendarSubscriptions(w http.ResponseWriter, r *http.Request) {
	subs, err := s.deps.Calendars.ListCalendarSubscriptions(r.Context(), userFrom(r).ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, subs)
}

func (s *server) handleCreateCalendarSubscription(w http.ResponseWriter, r *http.Request) {
	var in port.CalendarSubscriptionInput
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	sub, err := s.deps.Calendars.CreateCalendarSubscription(r.Context(), userFrom(r).ID, in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

func (s *server) handleUpdateCalendarSubscription(w http.ResponseWriter, r *http.Request) {
	var patch port.CalendarSubscriptionPatch
	if err := decodeJSON(w, r, &patch); err != nil {
		s.writeError(w, r, err)
		return
	}
	sub, err := s.deps.Calendars.UpdateCalendarSubscription(r.Context(), userFrom(r).ID, r.PathValue("id"), patch)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

func (s *server) handleDeleteCalendarSubscription(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Calendars.DeleteCalendarSubscription(r.Context(), userFrom(r).ID, r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
