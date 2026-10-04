package httpapi

import (
	"net/http"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// Shared-calendar management routes (M2.7 Task 12). All permission and
// ownership checks live in the service layer; these handlers only translate
// JSON. When the sharing service is not wired the surface answers 501.

func (s *server) sharing(w http.ResponseWriter, r *http.Request) (port.CalendarSharingService, bool) {
	if s.deps.CalendarShares == nil {
		s.writeError(w, r, domain.ErrNotImplemented)
		return nil, false
	}
	return s.deps.CalendarShares, true
}

func (s *server) handleListCalendarShares(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.sharing(w, r)
	if !ok {
		return
	}
	shares, err := svc.ListCalendarShares(r.Context(), userFrom(r).ID, r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, shares)
}

func (s *server) handleShareCalendar(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.sharing(w, r)
	if !ok {
		return
	}
	var in port.CalendarShareInput
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	var fc fieldCheck
	fc.title("granteeUserId", in.GranteeUserID)
	fc.title("granteeTeamId", in.GranteeTeamID)
	fc.title("permission", in.Permission)
	if err := fc.err(); err != nil {
		s.writeError(w, r, err)
		return
	}
	share, err := svc.ShareCalendar(r.Context(), userFrom(r).ID, r.PathValue("id"), in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, share)
}

func (s *server) handleUpdateCalendarShare(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.sharing(w, r)
	if !ok {
		return
	}
	var in struct {
		Permission string `json:"permission"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	var fc fieldCheck
	fc.title("permission", in.Permission)
	if err := fc.err(); err != nil {
		s.writeError(w, r, err)
		return
	}
	share, err := svc.UpdateCalendarShare(r.Context(), userFrom(r).ID,
		r.PathValue("id"), r.PathValue("shareId"), in.Permission)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, share)
}

func (s *server) handleRevokeCalendarShare(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.sharing(w, r)
	if !ok {
		return
	}
	if err := svc.RevokeCalendarShare(r.Context(), userFrom(r).ID,
		r.PathValue("id"), r.PathValue("shareId")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
