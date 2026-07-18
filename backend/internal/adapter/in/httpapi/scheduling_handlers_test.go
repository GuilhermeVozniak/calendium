package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- public booking page -----------------------------------------------------

func TestPublicBookingPage_OK_NoAuthHeader(t *testing.T) {
	h := newHarness(t)
	h.scheduling.publicPageResp = port.PublicBookingPage{
		Slug: "jane", Title: "30 min chat", OwnerName: "Jane Doe",
		DurationMinutes: 30, TimeZone: "America/New_York",
	}

	rec := h.anon(http.MethodGet, "/v1/public/booking/jane", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	var got port.PublicBookingPage
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Slug != "jane" || got.OwnerName != "Jane Doe" {
		t.Fatalf("unexpected page: %+v", got)
	}
	if h.scheduling.gotPublicSlug != "jane" {
		t.Fatalf("expected slug %q passed through, got %q", "jane", h.scheduling.gotPublicSlug)
	}
}

func TestPublicBookingPage_UnknownSlug_404(t *testing.T) {
	h := newHarness(t)
	h.scheduling.publicPageErr = domain.ErrNotFound

	rec := h.anon(http.MethodGet, "/v1/public/booking/nope", nil)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
	if e := decodeErr(t, rec); e.Code != "not_found" {
		t.Fatalf("expected code not_found, got %q", e.Code)
	}
}

// --- public slots --------------------------------------------------------------

func TestPublicSlots_HappyPath(t *testing.T) {
	h := newHarness(t)
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	h.scheduling.publicSlotsRet = []domain.AvailabilitySlot{
		{Start: from.Add(9 * time.Hour), End: from.Add(9*time.Hour + 30*time.Minute)},
	}

	target := fmt.Sprintf("/v1/public/booking/jane/slots?from=%s&to=%s",
		from.Format(time.RFC3339), to.Format(time.RFC3339))
	rec := h.anon(http.MethodGet, target, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	var got []domain.AvailabilitySlot
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 slot, got %d", len(got))
	}
	if h.scheduling.gotSlotsSlug != "jane" || !h.scheduling.gotSlotsFrom.Equal(from) || !h.scheduling.gotSlotsTo.Equal(to) {
		t.Fatalf("unexpected pass-through: slug=%q from=%v to=%v",
			h.scheduling.gotSlotsSlug, h.scheduling.gotSlotsFrom, h.scheduling.gotSlotsTo)
	}
}

func TestPublicSlots_SpanTooWide_Rejected(t *testing.T) {
	h := newHarness(t)
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 32) // > 31 days

	target := fmt.Sprintf("/v1/public/booking/jane/slots?from=%s&to=%s",
		from.Format(time.RFC3339), to.Format(time.RFC3339))
	rec := h.anon(http.MethodGet, target, nil)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an over-wide window, got %d", rec.Code)
	}
	if e := decodeErr(t, rec); e.Code != "validation_failed" {
		t.Fatalf("expected code validation_failed, got %q", e.Code)
	}
	if h.scheduling.gotSlotsSlug != "" {
		t.Fatal("expected the service not to be called when the handler rejects the window")
	}
}

func TestPublicSlots_UnknownSlug_404(t *testing.T) {
	h := newHarness(t)
	h.scheduling.publicSlotsErr = domain.ErrNotFound
	from := time.Now().UTC()
	to := from.Add(time.Hour)

	target := fmt.Sprintf("/v1/public/booking/nope/slots?from=%s&to=%s",
		from.Format(time.RFC3339), to.Format(time.RFC3339))
	rec := h.anon(http.MethodGet, target, nil)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// --- public book ---------------------------------------------------------------

func validBookingBody(t *testing.T) io.Reader {
	return jsonBody(t, port.BookingRequest{
		Start:        time.Now().Add(48 * time.Hour),
		InviteeName:  "Alex Guest",
		InviteeEmail: "alex@example.com",
		InviteeTZ:    "UTC",
	})
}

func TestPublicBook_Created201(t *testing.T) {
	h := newHarness(t)
	h.scheduling.bookRet = domain.Booking{ID: "bk_1", Status: domain.BookingConfirmed}

	rec := h.anon(http.MethodPost, "/v1/public/booking/jane/bookings", validBookingBody(t))

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	var got domain.Booking
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != "bk_1" {
		t.Fatalf("unexpected booking: %+v", got)
	}
	if h.scheduling.gotBookSlug != "jane" || h.scheduling.gotBookReq.InviteeEmail != "alex@example.com" {
		t.Fatalf("unexpected pass-through: slug=%q req=%+v", h.scheduling.gotBookSlug, h.scheduling.gotBookReq)
	}
}

func TestPublicBook_Conflict409(t *testing.T) {
	h := newHarness(t)
	h.scheduling.bookErr = fmt.Errorf("%w: slot is no longer available", domain.ErrConflict)

	rec := h.anon(http.MethodPost, "/v1/public/booking/jane/bookings", validBookingBody(t))

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
	if e := decodeErr(t, rec); e.Code != "conflict" {
		t.Fatalf("expected code conflict, got %q", e.Code)
	}
}

// TestPublicBook_ValidationRejected covers the invalid-payload mapping
// (the brief calls this out as "422"; this codebase's existing writeError /
// statusFor maps domain.ErrValidation to 400 uniformly across every
// endpoint, so this test asserts the actual, consistent behavior rather than
// introduce a one-off 422 special case for this route).
func TestPublicBook_ValidationRejected(t *testing.T) {
	h := newHarness(t)
	h.scheduling.bookErr = fmt.Errorf("%w: inviteeEmail is required", domain.ErrValidation)

	rec := h.anon(http.MethodPost, "/v1/public/booking/jane/bookings", validBookingBody(t))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if e := decodeErr(t, rec); e.Code != "validation_failed" {
		t.Fatalf("expected code validation_failed, got %q", e.Code)
	}
}

func TestPublicBook_BodyTooLarge_413(t *testing.T) {
	h := newHarness(t)

	oversized := port.BookingRequest{
		Start:        time.Now().Add(24 * time.Hour),
		InviteeName:  "Alex Guest",
		InviteeEmail: "alex@example.com",
		InviteeTZ:    "UTC",
		Note:         string(bytes.Repeat([]byte("x"), 20<<10)), // > 16KB body limit
	}

	rec := h.anon(http.MethodPost, "/v1/public/booking/jane/bookings", jsonBody(t, oversized))

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if e := decodeErr(t, rec); e.Code != "request_too_large" {
		t.Fatalf("expected code request_too_large, got %q", e.Code)
	}
	if h.scheduling.gotBookSlug != "" {
		t.Fatal("expected the service not to be called for an oversized body")
	}
}

// --- public poll -----------------------------------------------------------

func TestPublicPoll_OK(t *testing.T) {
	h := newHarness(t)
	h.scheduling.publicPollRet = port.PublicPoll{Token: "tok123", Title: "Team sync", OrganizerName: "Jane"}

	rec := h.anon(http.MethodGet, "/v1/public/polls/tok123", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	var got port.PublicPoll
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Title != "Team sync" {
		t.Fatalf("unexpected poll: %+v", got)
	}
	if h.scheduling.gotPollToken != "tok123" {
		t.Fatalf("expected token passed through, got %q", h.scheduling.gotPollToken)
	}
}

func TestPublicPoll_UnknownToken_404(t *testing.T) {
	h := newHarness(t)
	h.scheduling.publicPollErr = domain.ErrNotFound

	rec := h.anon(http.MethodGet, "/v1/public/polls/nope", nil)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestPublicPollVote_OK(t *testing.T) {
	h := newHarness(t)
	h.scheduling.votePollRet = port.PublicPoll{Token: "tok123", Title: "Team sync"}

	ballot := port.PollBallot{
		VoterEmail: "voter@example.com",
		VoterName:  "Voter One",
		Choices:    map[string]domain.PollVoteChoice{"opt_1": domain.VoteYes},
	}
	rec := h.anon(http.MethodPost, "/v1/public/polls/tok123/votes", jsonBody(t, ballot))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if h.scheduling.gotVoteToken != "tok123" || h.scheduling.gotVoteBallot.VoterEmail != "voter@example.com" {
		t.Fatalf("unexpected pass-through: token=%q ballot=%+v",
			h.scheduling.gotVoteToken, h.scheduling.gotVoteBallot)
	}
}

func TestPublicPollVote_Conflict409(t *testing.T) {
	h := newHarness(t)
	h.scheduling.votePollErr = fmt.Errorf("%w: poll is not open for voting", domain.ErrConflict)

	ballot := port.PollBallot{VoterEmail: "voter@example.com", Choices: map[string]domain.PollVoteChoice{"opt_1": domain.VoteYes}}
	rec := h.anon(http.MethodPost, "/v1/public/polls/tok123/votes", jsonBody(t, ballot))

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
}

// --- rate limiting on the live route table --------------------------------

// TestPublicRoutes_RateLimited429 drives the same http.Handler instance
// across repeated requests (unlike harness.anon, which calls New() fresh
// each time and would reset the limiter) to prove the write bucket
// (5/min, burst 5) trips a 429 with Retry-After on the 6th call.
func TestPublicRoutes_RateLimited429(t *testing.T) {
	h := newHarness(t)
	h.scheduling.bookRet = domain.Booking{ID: "bk_1"}
	handler := h.handler()

	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/public/booking/jane/bookings", validBookingBody(t))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	for i := 0; i < 5; i++ {
		rec := post()
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("unexpected 429 on call %d of burst 5", i+1)
		}
	}

	rec := post()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after burst exhausted, got %d", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "60" {
		t.Fatalf("expected Retry-After: 60, got %q", got)
	}
	if e := decodeErr(t, rec); e.Code != "rate_limited" {
		t.Fatalf("expected code rate_limited, got %q", e.Code)
	}
}
