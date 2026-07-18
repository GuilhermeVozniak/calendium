package httpapi

import (
	"fmt"
	"net/http"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// maxFreeBusyEmails and maxFreeBusySpan bound POST /v1/freebusy: enough for a
// small-group Find-a-Time grid without turning one request into an unbounded
// provider-API fan-out. Enforced here (before the service call) so a bad
// request never reaches an outbound provider gateway.
const (
	maxFreeBusyEmails = 20
	maxFreeBusySpan   = 14 * 24 * time.Hour
)

// --- booking links -------------------------------------------------------------

func (s *server) handleListLinks(w http.ResponseWriter, r *http.Request) {
	links, err := s.deps.Scheduling.ListLinks(r.Context(), userFrom(r).ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, links)
}

func (s *server) handleCreateLink(w http.ResponseWriter, r *http.Request) {
	var in port.BookingLinkInput
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	link, err := s.deps.Scheduling.CreateLink(r.Context(), userFrom(r).ID, in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, link)
}

func (s *server) handleUpdateLink(w http.ResponseWriter, r *http.Request) {
	var in port.BookingLinkInput
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	link, err := s.deps.Scheduling.UpdateLink(r.Context(), userFrom(r).ID, r.PathValue("id"), in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, link)
}

func (s *server) handleDeleteLink(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Scheduling.DeleteLink(r.Context(), userFrom(r).ID, r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- bookings --------------------------------------------------------------------

func (s *server) handleListBookings(w http.ResponseWriter, r *http.Request) {
	bookings, err := s.deps.Scheduling.ListBookings(r.Context(), userFrom(r).ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, bookings)
}

func (s *server) handleCancelBooking(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Scheduling.CancelBooking(r.Context(), userFrom(r).ID, r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- meeting polls -----------------------------------------------------------

func (s *server) handleListPolls(w http.ResponseWriter, r *http.Request) {
	polls, err := s.deps.Scheduling.ListPolls(r.Context(), userFrom(r).ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, polls)
}

func (s *server) handleCreatePoll(w http.ResponseWriter, r *http.Request) {
	var in port.PollInput
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	poll, err := s.deps.Scheduling.CreatePoll(r.Context(), userFrom(r).ID, in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, poll)
}

func (s *server) handleConfirmPoll(w http.ResponseWriter, r *http.Request) {
	var in struct {
		OptionID string `json:"optionId"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	poll, err := s.deps.Scheduling.ConfirmPoll(r.Context(), userFrom(r).ID, r.PathValue("id"), in.OptionID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, poll)
}

func (s *server) handleDeletePoll(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Scheduling.DeletePoll(r.Context(), userFrom(r).ID, r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- propose-new-time ---------------------------------------------------------

func (s *server) handleProposeTime(w http.ResponseWriter, r *http.Request) {
	var in port.TimeProposalInput
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	proposal, err := s.deps.Scheduling.ProposeTime(r.Context(), userFrom(r).ID, r.PathValue("id"), in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, proposal)
}

func (s *server) handleListProposals(w http.ResponseWriter, r *http.Request) {
	proposals, err := s.deps.Scheduling.ListProposals(r.Context(), userFrom(r).ID, r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, proposals)
}

func (s *server) handleAcceptProposal(w http.ResponseWriter, r *http.Request) {
	event, err := s.deps.Scheduling.AcceptProposal(r.Context(), userFrom(r).ID, r.PathValue("id"), r.PathValue("pid"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, event)
}

func (s *server) handleDeclineProposal(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Scheduling.DeclineProposal(r.Context(), userFrom(r).ID, r.PathValue("id"), r.PathValue("pid")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- guest free/busy -----------------------------------------------------------

func (s *server) handleGuestFreeBusy(w http.ResponseWriter, r *http.Request) {
	var req port.FreeBusyRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	if len(req.Emails) == 0 || len(req.Emails) > maxFreeBusyEmails {
		s.writeError(w, r, fmt.Errorf("%w: emails must include between 1 and %d addresses", domain.ErrValidation, maxFreeBusyEmails))
		return
	}
	if !req.To.After(req.From) {
		s.writeError(w, r, fmt.Errorf("%w: `to` must be after `from`", domain.ErrValidation))
		return
	}
	if req.To.Sub(req.From) > maxFreeBusySpan {
		s.writeError(w, r, fmt.Errorf("%w: span must not exceed %d days", domain.ErrValidation, int(maxFreeBusySpan/(24*time.Hour))))
		return
	}
	busy, err := s.deps.Scheduling.GuestFreeBusy(r.Context(), userFrom(r).ID, req)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, busy)
}

// --- settings ------------------------------------------------------------------

func (s *server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.deps.Settings.Get(r.Context(), userFrom(r).ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var in domain.UserSettings
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	settings, err := s.deps.Settings.Update(r.Context(), userFrom(r).ID, in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}
