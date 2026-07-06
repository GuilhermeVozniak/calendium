package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

func (s *server) handleListCalendars(w http.ResponseWriter, r *http.Request) {
	cals, err := s.deps.Calendars.ListCalendars(r.Context(), userFrom(r).ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, cals)
}

func (s *server) handleUpdateCalendar(w http.ResponseWriter, r *http.Request) {
	var patch port.CalendarPatch
	if err := decodeJSON(w, r, &patch); err != nil {
		s.writeError(w, r, err)
		return
	}
	cal, err := s.deps.Calendars.UpdateCalendar(r.Context(), userFrom(r).ID, r.PathValue("id"), patch)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, cal)
}

func (s *server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	from, to, err := timeRange(qs.Get("from"), qs.Get("to"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var calendarIDs []string
	for _, raw := range qs["calendarIds"] {
		for _, id := range strings.Split(raw, ",") {
			if id = strings.TrimSpace(id); id != "" {
				calendarIDs = append(calendarIDs, id)
			}
		}
	}
	events, err := s.deps.Calendars.ListEvents(r.Context(), userFrom(r).ID, from, to, calendarIDs)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *server) handleCreateEvent(w http.ResponseWriter, r *http.Request) {
	var in domain.EventInput
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	event, err := s.deps.Calendars.CreateEvent(r.Context(), userFrom(r).ID, in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, event)
}

func (s *server) handleUpdateEvent(w http.ResponseWriter, r *http.Request) {
	var patch domain.EventPatch
	if err := decodeJSON(w, r, &patch); err != nil {
		s.writeError(w, r, err)
		return
	}
	event, err := s.deps.Calendars.UpdateEvent(r.Context(), userFrom(r).ID, r.PathValue("id"), patch)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, event)
}

func (s *server) handleDeleteEvent(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Calendars.DeleteEvent(r.Context(), userFrom(r).ID, r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleRsvp(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Response string `json:"response"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	response, err := domain.ParseRsvpStatus(in.Response)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	event, err := s.deps.Calendars.RSVP(r.Context(), userFrom(r).ID, r.PathValue("id"), response)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, event)
}

func (s *server) handleAvailability(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	from, to, err := timeRange(qs.Get("from"), qs.Get("to"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	minutes, err := strconv.Atoi(qs.Get("duration"))
	if err != nil || minutes <= 0 {
		s.writeError(w, r, fmt.Errorf("%w: duration must be a positive number of minutes", domain.ErrValidation))
		return
	}
	slots, err := s.deps.Calendars.Availability(r.Context(), userFrom(r).ID, from, to,
		time.Duration(minutes)*time.Minute)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, slots)
}

// timeRange parses the from/to RFC 3339 query parameters.
func timeRange(fromRaw, toRaw string) (from, to time.Time, err error) {
	if from, err = time.Parse(time.RFC3339, fromRaw); err != nil {
		return from, to, fmt.Errorf("%w: `from` must be an RFC 3339 timestamp", domain.ErrValidation)
	}
	if to, err = time.Parse(time.RFC3339, toRaw); err != nil {
		return from, to, fmt.Errorf("%w: `to` must be an RFC 3339 timestamp", domain.ErrValidation)
	}
	return from, to, nil
}
