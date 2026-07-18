package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// publicBodyLimit bounds unauthenticated POST bodies well below the general
// maxBodyBytes: a booking or a poll ballot is a handful of fields, so a
// generous authenticated-sized cap would only help an abuser.
const publicBodyLimit = 16 << 10

// maxPublicSlotsSpan caps how wide a from/to window the public slots query
// may request, independent of (and in addition to) the service-level check —
// defense in depth on the unauthenticated surface.
const maxPublicSlotsSpan = 31 * 24 * time.Hour

// decodePublicJSON decodes a public (unauthenticated) POST body capped at
// publicBodyLimit. An oversized body is rejected with 413 directly, rather
// than folded into the generic 400 validation mapping, so clients get an
// accurate signal. Reports whether decoding succeeded; on failure the error
// response has already been written.
func (s *server) decodePublicJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, publicBodyLimit)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorBody{Error: errorDetail{
				Code:    "request_too_large",
				Message: "request body too large",
			}})
			return false
		}
		s.writeError(w, r, fmt.Errorf("%w: invalid JSON body: %v", domain.ErrValidation, err))
		return false
	}
	return true
}

// handlePublicBookingPage serves the public booking-link document: title,
// duration, owner display name — no owner PII, no auth required. Unknown or
// inactive slugs surface as the uniform 404 (domain.ErrNotFound).
func (s *server) handlePublicBookingPage(w http.ResponseWriter, r *http.Request) {
	page, err := s.deps.Scheduling.PublicPage(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// handlePublicSlots returns bookable start times for a booking link between
// from and to (RFC 3339 query params), capped at maxPublicSlotsSpan.
func (s *server) handlePublicSlots(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	from, to, err := timeRange(qs.Get("from"), qs.Get("to"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if to.Sub(from) > maxPublicSlotsSpan {
		s.writeError(w, r, fmt.Errorf("%w: query window must be at most 31 days", domain.ErrValidation))
		return
	}
	slots, err := s.deps.Scheduling.PublicSlots(r.Context(), r.PathValue("slug"), from, to)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, slots)
}

// handlePublicBook runs the public booking pipeline (hold, free/busy
// re-check, provider event, confirm). domain.ErrConflict (slot taken) maps
// to 409 via the existing writeError/statusFor mapping.
func (s *server) handlePublicBook(w http.ResponseWriter, r *http.Request) {
	var req port.BookingRequest
	if !s.decodePublicJSON(w, r, &req) {
		return
	}
	booking, err := s.deps.Scheduling.Book(r.Context(), r.PathValue("slug"), req)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, booking)
}

// handlePublicPoll serves the public meeting-poll document: options plus
// anonymized tallies. Unknown tokens surface as the uniform 404.
func (s *server) handlePublicPoll(w http.ResponseWriter, r *http.Request) {
	poll, err := s.deps.Scheduling.PublicPollByToken(r.Context(), r.PathValue("token"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, poll)
}

// handlePublicPollVote records (or replaces) one voter's ballot and returns
// the refreshed public poll view.
func (s *server) handlePublicPollVote(w http.ResponseWriter, r *http.Request) {
	var ballot port.PollBallot
	if !s.decodePublicJSON(w, r, &ballot) {
		return
	}
	poll, err := s.deps.Scheduling.VotePoll(r.Context(), r.PathValue("token"), ballot)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, poll)
}

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
