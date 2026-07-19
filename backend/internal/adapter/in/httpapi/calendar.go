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

// --- Event notes (M2.8 Task 4) ----------------------------------------------
//
// Note bodies are markdown treated as plain text end-to-end (the web client
// renders them in a textarea / as text, never as HTML), so no HTML
// sanitization applies here; link URLs are scheme-validated in the service.

func (s *server) handleGetEventNote(w http.ResponseWriter, r *http.Request) {
	note, err := s.deps.Calendars.GetEventNote(r.Context(), userFrom(r).ID, r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, note)
}

func (s *server) handlePutEventNote(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BodyMD string   `json:"bodyMd"`
		Links  []string `json:"links"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	note, err := s.deps.Calendars.PutEventNote(r.Context(), userFrom(r).ID, r.PathValue("id"), in.BodyMD, in.Links)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, note)
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

// handleTeamAvailability serves GET /v1/teams/{id}/availability?from&to —
// each team member's opaque busy blocks (M2.7 Task 13). Membership, the
// per-member sharing opt-in, and the 35-day range cap are all enforced in
// the service; this handler only parses the range.
func (s *server) handleTeamAvailability(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	from, to, err := timeRange(qs.Get("from"), qs.Get("to"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	rows, err := s.deps.Calendars.TeamAvailability(r.Context(), userFrom(r).ID, r.PathValue("id"), from, to)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
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

// Event Template Handlers

func (s *server) handleListEventTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := s.deps.Calendars.ListEventTemplates(r.Context(), userFrom(r).ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, templates)
}

func (s *server) handleCreateEventTemplate(w http.ResponseWriter, r *http.Request) {
	var in domain.EventTemplateInput
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	template, err := s.deps.Calendars.CreateEventTemplate(r.Context(), userFrom(r).ID, in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, template)
}

func (s *server) handleUpdateEventTemplate(w http.ResponseWriter, r *http.Request) {
	var in domain.EventTemplateInput
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	template, err := s.deps.Calendars.UpdateEventTemplate(r.Context(), userFrom(r).ID, r.PathValue("id"), in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, template)
}

func (s *server) handleDeleteEventTemplate(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Calendars.DeleteEventTemplate(r.Context(), userFrom(r).ID, r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleUseEventTemplate(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Calendars.UseEventTemplate(r.Context(), userFrom(r).ID, r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Calendar Set Handlers

func (s *server) handleListCalendarSets(w http.ResponseWriter, r *http.Request) {
	sets, err := s.deps.Calendars.ListCalendarSets(r.Context(), userFrom(r).ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sets)
}

func (s *server) handleCreateCalendarSet(w http.ResponseWriter, r *http.Request) {
	var in domain.CalendarSetInput
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	set, err := s.deps.Calendars.CreateCalendarSet(r.Context(), userFrom(r).ID, in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, set)
}

func (s *server) handleUpdateCalendarSet(w http.ResponseWriter, r *http.Request) {
	var in domain.CalendarSetInput
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	set, err := s.deps.Calendars.UpdateCalendarSet(r.Context(), userFrom(r).ID, r.PathValue("id"), in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, set)
}

func (s *server) handleDeleteCalendarSet(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Calendars.DeleteCalendarSet(r.Context(), userFrom(r).ID, r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
