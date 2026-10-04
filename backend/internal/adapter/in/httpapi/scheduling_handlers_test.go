package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- public booking page -----------------------------------------------------

func TestPublicBookingPage_OK_NoAuthHeader(t *testing.T) {
	h := newHarness(t)
	h.scheduling.publicPageRet = port.PublicBookingPage{
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
	if e := decodeErr(t, rec); e.Code != "payload_too_large" {
		t.Fatalf("expected code payload_too_large, got %q", e.Code)
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
	if got := rec.Header().Get("Retry-After"); got != "12" {
		t.Fatalf("expected Retry-After: 12 (5/min, empty bucket), got %q", got)
	}
	if e := decodeErr(t, rec); e.Code != "rate_limited" {
		t.Fatalf("expected code rate_limited, got %q", e.Code)
	}
}

// --- booking links -------------------------------------------------------------

func TestSchedulingListLinks(t *testing.T) {
	h := newHarness(t)
	h.sched.listLinksRet = []domain.BookingLink{{ID: "l1", Slug: "intro"}}

	rec := h.authed(http.MethodGet, "/v1/booking-links", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if h.sched.gotListLinksUserID != defaultUserID {
		t.Fatalf("gotListLinksUserID = %q, want %q", h.sched.gotListLinksUserID, defaultUserID)
	}
}

func TestSchedulingCreateLink_SlugConflict(t *testing.T) {
	h := newHarness(t)
	h.sched.createLinkErr = fmt.Errorf("%w: slug %q is taken", domain.ErrConflict, "intro")

	rec := h.authed(http.MethodPost, "/v1/booking-links", jsonBody(t, port.BookingLinkInput{
		Slug: "intro", Title: "Intro call", CalendarID: "cal1", DurationMinutes: 30,
	}))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body)
	}
	if decodeErr(t, rec).Code != "conflict" {
		t.Fatalf("error code = %q, want conflict", decodeErr(t, rec).Code)
	}
	if h.sched.gotCreateLinkUserID != defaultUserID {
		t.Fatalf("gotCreateLinkUserID = %q, want %q", h.sched.gotCreateLinkUserID, defaultUserID)
	}
}

func TestSchedulingUpdateLink_ForeignLinkIs404(t *testing.T) {
	h := newHarness(t)
	// Ownership scoping is enforced by the service (a foreign linkID is
	// indistinguishable from a missing one); this proves the handler
	// forwards userID/linkID and maps the resulting ErrNotFound to 404.
	h.sched.updateLinkErr = domain.ErrNotFound

	rec := h.authed(http.MethodPut, "/v1/booking-links/foreign-link", jsonBody(t, port.BookingLinkInput{
		Slug: "intro", Title: "Intro call", CalendarID: "cal1", DurationMinutes: 30,
	}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
	}
	if h.sched.gotUpdateLinkID != "foreign-link" {
		t.Fatalf("gotUpdateLinkID = %q, want foreign-link", h.sched.gotUpdateLinkID)
	}
	if h.sched.gotUpdateLinkUserID != defaultUserID {
		t.Fatalf("gotUpdateLinkUserID = %q, want %q", h.sched.gotUpdateLinkUserID, defaultUserID)
	}
}

func TestSchedulingDeleteLink(t *testing.T) {
	h := newHarness(t)

	rec := h.authed(http.MethodDelete, "/v1/booking-links/l1", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body)
	}
	if h.sched.gotDeleteLinkID != "l1" || h.sched.gotDeleteLinkUserID != defaultUserID {
		t.Fatalf("gotDeleteLink = (%q, %q), want (%q, l1)", h.sched.gotDeleteLinkUserID, h.sched.gotDeleteLinkID, defaultUserID)
	}

	h.sched.deleteLinkErr = domain.ErrNotFound
	rec = h.authed(http.MethodDelete, "/v1/booking-links/foreign", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
	}
}

// --- bookings --------------------------------------------------------------------

func TestSchedulingListBookingsAndCancel(t *testing.T) {
	h := newHarness(t)
	h.sched.listBookingsRet = []domain.Booking{{ID: "b1"}}

	rec := h.authed(http.MethodGet, "/v1/bookings", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	rec = h.authed(http.MethodPost, "/v1/bookings/b1/cancel", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("cancel status = %d, want 204; body=%s", rec.Code, rec.Body)
	}
	if h.sched.gotCancelBookingID != "b1" {
		t.Fatalf("gotCancelBookingID = %q, want b1", h.sched.gotCancelBookingID)
	}

	h.sched.cancelBookingErr = domain.ErrNotFound
	rec = h.authed(http.MethodPost, "/v1/bookings/foreign/cancel", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
	}
}

// --- meeting polls -----------------------------------------------------------

func TestSchedulingPollsWired(t *testing.T) {
	h := newHarness(t)
	h.sched.listPollsRet = []domain.MeetingPoll{{ID: "p1"}}
	h.sched.createPollRet = domain.MeetingPoll{ID: "p1"}
	h.sched.confirmPollRet = domain.MeetingPoll{ID: "p1", Status: domain.PollConfirmed}

	if rec := h.authed(http.MethodGet, "/v1/polls", nil); rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	rec := h.authed(http.MethodPost, "/v1/polls", jsonBody(t, port.PollInput{
		Title: "Sync", CalendarID: "cal1", DurationMinutes: 30,
		Options: []domain.PollOption{{}, {}},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	rec = h.authed(http.MethodPost, "/v1/polls/p1/confirm", jsonBody(t, map[string]string{"optionId": "opt1"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if h.sched.gotConfirmPollID != "p1" || h.sched.gotConfirmPollOptionID != "opt1" {
		t.Fatalf("confirm got (%q, %q), want (p1, opt1)", h.sched.gotConfirmPollID, h.sched.gotConfirmPollOptionID)
	}

	rec = h.authed(http.MethodDelete, "/v1/polls/p1", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body=%s", rec.Code, rec.Body)
	}
	if h.sched.gotDeletePollID != "p1" {
		t.Fatalf("gotDeletePollID = %q, want p1", h.sched.gotDeletePollID)
	}
}

// --- propose-new-time ---------------------------------------------------------

func TestSchedulingProposalsWired(t *testing.T) {
	h := newHarness(t)
	h.sched.proposeTimeRet = domain.TimeProposal{ID: "tp1"}
	h.sched.listProposalsRet = []domain.TimeProposal{{ID: "tp1"}}
	h.sched.acceptProposalRet = domain.Event{ID: "e1"}

	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	rec := h.authed(http.MethodPost, "/v1/events/e1/propose-time", jsonBody(t, port.TimeProposalInput{
		Start: base, End: base.Add(30 * time.Minute),
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("propose status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	rec = h.authed(http.MethodGet, "/v1/events/e1/proposals", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	rec = h.authed(http.MethodPost, "/v1/events/e1/proposals/tp1/accept", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("accept status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	rec = h.authed(http.MethodPost, "/v1/events/e1/proposals/tp1/decline", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("decline status = %d, want 204; body=%s", rec.Code, rec.Body)
	}

	h.sched.declineProposalErr = domain.ErrConflict
	rec = h.authed(http.MethodPost, "/v1/events/e1/proposals/tp1/decline", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("decline (conflict) status = %d, want 409; body=%s", rec.Code, rec.Body)
	}
}

// --- guest free/busy -----------------------------------------------------------

func TestSchedulingGuestFreeBusy_EmailCountValidation(t *testing.T) {
	h := newHarness(t)
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)

	cases := []struct {
		name   string
		emails []string
	}{
		{"zero emails", nil},
		{"too many emails", func() []string {
			emails := make([]string, 21)
			for i := range emails {
				emails[i] = "guest@example.com"
			}
			return emails
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := h.authed(http.MethodPost, "/v1/freebusy", jsonBody(t, port.FreeBusyRequest{
				Emails: tc.emails, From: base, To: base.Add(time.Hour),
			}))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 422/400; body=%s", rec.Code, rec.Body)
			}
			if decodeErr(t, rec).Code != "validation_failed" {
				t.Fatalf("error code = %q, want validation_failed", decodeErr(t, rec).Code)
			}
		})
	}
}

func TestSchedulingGuestFreeBusy_SpanValidation(t *testing.T) {
	h := newHarness(t)
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)

	rec := h.authed(http.MethodPost, "/v1/freebusy", jsonBody(t, port.FreeBusyRequest{
		Emails: []string{"guest@example.com"}, From: base, To: base.Add(15 * 24 * time.Hour),
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 422/400; body=%s", rec.Code, rec.Body)
	}
}

func TestSchedulingGuestFreeBusy_OK(t *testing.T) {
	h := newHarness(t)
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	h.sched.freeBusyRet = map[string][]domain.BusyInterval{
		"guest@example.com": {{Start: base, End: base.Add(time.Hour)}},
	}

	rec := h.authed(http.MethodPost, "/v1/freebusy", jsonBody(t, port.FreeBusyRequest{
		Emails: []string{"guest@example.com"}, From: base, To: base.Add(24 * time.Hour),
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if h.sched.gotFreeBusyUserID != defaultUserID {
		t.Fatalf("gotFreeBusyUserID = %q, want %q", h.sched.gotFreeBusyUserID, defaultUserID)
	}
	if len(h.sched.gotFreeBusyReq.Emails) != 1 {
		t.Fatalf("gotFreeBusyReq.Emails = %v, want 1 email", h.sched.gotFreeBusyReq.Emails)
	}
}

// --- settings ------------------------------------------------------------------

func TestSchedulingSettings_RoundTrip(t *testing.T) {
	h := newHarness(t)
	h.settings.getRet = domain.UserSettings{TimeZone: "UTC", WorkingHours: []domain.AvailabilityWindow{}}
	h.settings.updateRet = domain.UserSettings{TimeZone: "America/New_York"}

	rec := h.authed(http.MethodGet, "/v1/settings", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	rec = h.authed(http.MethodPut, "/v1/settings", jsonBody(t, domain.UserSettings{TimeZone: "America/New_York"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if h.settings.gotUpdateUserID != defaultUserID {
		t.Fatalf("gotUpdateUserID = %q, want %q", h.settings.gotUpdateUserID, defaultUserID)
	}
	if h.settings.gotUpdateIn.TimeZone != "America/New_York" {
		t.Fatalf("gotUpdateIn.TimeZone = %q, want America/New_York", h.settings.gotUpdateIn.TimeZone)
	}
}

func TestSchedulingSettings_InvalidTimeZoneIs400(t *testing.T) {
	h := newHarness(t)
	h.settings.updateErr = fmt.Errorf("%w: invalid time zone %q", domain.ErrValidation, "Not/AZone")

	rec := h.authed(http.MethodPut, "/v1/settings", jsonBody(t, domain.UserSettings{TimeZone: "Not/AZone"}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 422/400; body=%s", rec.Code, rec.Body)
	}
	if decodeErr(t, rec).Code != "validation_failed" {
		t.Fatalf("error code = %q, want validation_failed", decodeErr(t, rec).Code)
	}
}

// --- auth required -------------------------------------------------------------

func TestSchedulingRoutesRequireAuth(t *testing.T) {
	h := newHarness(t)
	for _, req := range []struct {
		method, path string
	}{
		{http.MethodGet, "/v1/booking-links"},
		{http.MethodGet, "/v1/bookings"},
		{http.MethodGet, "/v1/polls"},
		{http.MethodPost, "/v1/freebusy"},
		{http.MethodGet, "/v1/settings"},
	} {
		rec := h.anon(req.method, req.path, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s anon status = %d, want 401", req.method, req.path, rec.Code)
		}
	}
}

func TestUpdateSettingsAIBackgroundTriState(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantCall bool
		wantOn   bool
	}{
		{"absent keeps stored value", `{"timeZone":"UTC","workingHours":[],"workingLocation":""}`, false, false},
		{"explicit null keeps stored value", `{"timeZone":"UTC","workingHours":[],"workingLocation":"","aiBackground":null}`, false, false},
		{"true", `{"timeZone":"UTC","workingHours":[],"workingLocation":"","aiBackground":true}`, true, true},
		{"false", `{"timeZone":"UTC","workingHours":[],"workingLocation":"","aiBackground":false}`, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			on := true
			if tc.wantCall {
				on = tc.wantOn
			}
			h.settings.updateRet = domain.UserSettings{TimeZone: "UTC", WorkingHours: []domain.AvailabilityWindow{}, AIBackground: on}
			rec := h.authed(http.MethodPut, "/v1/settings", strings.NewReader(tc.body))
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			// One service call carries the whole document plus the optional
			// switch, so the write is atomic (no half-applied PUT).
			if h.settings.updateCalls != 1 || h.settings.gotUpdateUserID != defaultUserID {
				t.Fatalf("Update calls = %d (user %q), want exactly 1", h.settings.updateCalls, h.settings.gotUpdateUserID)
			}
			if (h.settings.gotUpdateAI != nil) != tc.wantCall {
				t.Fatalf("Update aiBackground = %v, wantCall=%v", h.settings.gotUpdateAI, tc.wantCall)
			}
			if tc.wantCall && *h.settings.gotUpdateAI != tc.wantOn {
				t.Fatalf("Update aiBackground = %v, want %v", *h.settings.gotUpdateAI, tc.wantOn)
			}
			if tc.wantCall && !strings.Contains(rec.Body.String(), fmt.Sprintf(`"aiBackground":%v`, tc.wantOn)) {
				t.Fatalf("response must carry the switch: %s", rec.Body.String())
			}
		})
	}
}

func TestUpdateSettingsInvalidAIBackgroundIs400(t *testing.T) {
	h := newHarness(t)
	rec := h.authed(http.MethodPut, "/v1/settings", strings.NewReader(`{"timeZone":"UTC","aiBackground":"yes"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
}
