package httpapi

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

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

func TestSchedulingSettings_InvalidTimeZoneIs422(t *testing.T) {
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
