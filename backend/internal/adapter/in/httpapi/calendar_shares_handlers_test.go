package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- CalendarSharingService double -------------------------------------------

type fakeCalendarSharingService struct {
	shareRet     domain.CalendarShare
	shareErr     error
	gotShareUser string
	gotShareCal  string
	gotShareIn   port.CalendarShareInput

	listRet    []domain.CalendarShare
	listErr    error
	gotListCal string

	updateRet      domain.CalendarShare
	updateErr      error
	gotUpdateCal   string
	gotUpdateShare string
	gotUpdatePerm  string

	revokeErr      error
	gotRevokeCal   string
	gotRevokeShare string
}

func (f *fakeCalendarSharingService) ShareCalendar(_ context.Context, userID, calendarID string, in port.CalendarShareInput) (domain.CalendarShare, error) {
	f.gotShareUser, f.gotShareCal, f.gotShareIn = userID, calendarID, in
	return f.shareRet, f.shareErr
}

func (f *fakeCalendarSharingService) ListCalendarShares(_ context.Context, _, calendarID string) ([]domain.CalendarShare, error) {
	f.gotListCal = calendarID
	return f.listRet, f.listErr
}

func (f *fakeCalendarSharingService) UpdateCalendarShare(_ context.Context, _, calendarID, shareID, permission string) (domain.CalendarShare, error) {
	f.gotUpdateCal, f.gotUpdateShare, f.gotUpdatePerm = calendarID, shareID, permission
	return f.updateRet, f.updateErr
}

func (f *fakeCalendarSharingService) RevokeCalendarShare(_ context.Context, _, calendarID, shareID string) error {
	f.gotRevokeCal, f.gotRevokeShare = calendarID, shareID
	return f.revokeErr
}

var _ port.CalendarSharingService = (*fakeCalendarSharingService)(nil)

func newSharesHarness(t *testing.T) (*harness, *fakeCalendarSharingService) {
	t.Helper()
	h := newHarness(t)
	f := &fakeCalendarSharingService{}
	h.deps.CalendarShares = f
	return h, f
}

// --- tests -------------------------------------------------------------------

func TestShareCalendarRoute(t *testing.T) {
	h, f := newSharesHarness(t)
	grantee := "u2"
	f.shareRet = domain.CalendarShare{
		ID: "sh1", CalendarID: "cal1", GranteeUserID: &grantee,
		Permission: domain.PermissionReader, CreatedBy: defaultUserID,
		CreatedAt: time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC),
	}

	rec := h.authed("POST", "/v1/calendars/cal1/shares",
		jsonBody(t, map[string]string{"granteeUserId": "u2", "permission": "reader"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if f.gotShareUser != defaultUserID || f.gotShareCal != "cal1" {
		t.Fatalf("service got user=%q cal=%q", f.gotShareUser, f.gotShareCal)
	}
	if f.gotShareIn.GranteeUserID != "u2" || f.gotShareIn.Permission != "reader" {
		t.Fatalf("service got input %+v", f.gotShareIn)
	}
	var got domain.CalendarShare
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != "sh1" || got.Permission != domain.PermissionReader {
		t.Fatalf("response = %+v", got)
	}
}

func TestShareCalendarRouteErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{domain.ErrNotFound, http.StatusNotFound},
		{domain.ErrForbidden, http.StatusForbidden},
		{domain.ErrConflict, http.StatusConflict},
		{domain.ErrValidation, http.StatusBadRequest},
		{domain.ErrPaymentRequired, http.StatusPaymentRequired},
	}
	for _, tc := range cases {
		h, f := newSharesHarness(t)
		f.shareErr = tc.err
		rec := h.authed("POST", "/v1/calendars/cal1/shares",
			jsonBody(t, map[string]string{"granteeUserId": "u2"}))
		if rec.Code != tc.want {
			t.Fatalf("err %v: status = %d, want %d", tc.err, rec.Code, tc.want)
		}
	}
}

func TestListCalendarSharesRoute(t *testing.T) {
	h, f := newSharesHarness(t)
	team := "t1"
	f.listRet = []domain.CalendarShare{{ID: "sh1", CalendarID: "cal1", GranteeTeamID: &team, Permission: domain.PermissionFreeBusy}}

	rec := h.authed("GET", "/v1/calendars/cal1/shares", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body=%s)", rec.Code, rec.Body.String())
	}
	if f.gotListCal != "cal1" {
		t.Fatalf("service got cal %q", f.gotListCal)
	}
	var got []domain.CalendarShare
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got) != 1 || got[0].ID != "sh1" {
		t.Fatalf("response = %v, %v", got, err)
	}
}

func TestUpdateCalendarShareRoute(t *testing.T) {
	h, f := newSharesHarness(t)
	f.updateRet = domain.CalendarShare{ID: "sh1", CalendarID: "cal1", Permission: domain.PermissionEditor}

	rec := h.authed("PATCH", "/v1/calendars/cal1/shares/sh1",
		jsonBody(t, map[string]string{"permission": "editor"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body=%s)", rec.Code, rec.Body.String())
	}
	if f.gotUpdateCal != "cal1" || f.gotUpdateShare != "sh1" || f.gotUpdatePerm != "editor" {
		t.Fatalf("service got cal=%q share=%q perm=%q", f.gotUpdateCal, f.gotUpdateShare, f.gotUpdatePerm)
	}
}

func TestRevokeCalendarShareRoute(t *testing.T) {
	h, f := newSharesHarness(t)
	rec := h.authed("DELETE", "/v1/calendars/cal1/shares/sh1", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if f.gotRevokeCal != "cal1" || f.gotRevokeShare != "sh1" {
		t.Fatalf("service got cal=%q share=%q", f.gotRevokeCal, f.gotRevokeShare)
	}

	h2, f2 := newSharesHarness(t)
	f2.revokeErr = domain.ErrNotFound
	if rec := h2.authed("DELETE", "/v1/calendars/cal1/shares/nope", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestCalendarShareRoutesRequireAuth(t *testing.T) {
	h, _ := newSharesHarness(t)
	if rec := h.anon("POST", "/v1/calendars/cal1/shares", jsonBody(t, map[string]string{"granteeUserId": "u2"})); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon status = %d, want 401", rec.Code)
	}
}

func TestCalendarShareRoutesUnwiredAnswer501(t *testing.T) {
	h := newHarness(t) // CalendarShares left nil
	if rec := h.authed("GET", "/v1/calendars/cal1/shares", nil); rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

// --- team availability route (M2.7 Task 13) ----------------------------------

func TestTeamAvailabilityRoute(t *testing.T) {
	h := newHarness(t)
	h.calendars.teamAvailRet = []port.MemberAvailability{
		{UserID: "u1", Name: "Ada One", Email: "u1@x.com", Shared: true, Busy: []domain.AvailabilitySlot{{
			Start: time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 7, 7, 11, 0, 0, 0, time.UTC),
		}}},
		{UserID: "u2", Shared: false, Busy: []domain.AvailabilitySlot{}},
	}

	rec := h.authed("GET", "/v1/teams/t1/availability?from=2026-07-07T00:00:00Z&to=2026-07-08T00:00:00Z", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body=%s)", rec.Code, rec.Body.String())
	}
	if h.calendars.gotTeamAvailID != "t1" {
		t.Fatalf("service got team %q", h.calendars.gotTeamAvailID)
	}
	if !h.calendars.gotTeamAvailFrom.Equal(time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)) ||
		!h.calendars.gotTeamAvailTo.Equal(time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("range = %v..%v", h.calendars.gotTeamAvailFrom, h.calendars.gotTeamAvailTo)
	}

	// Wire-shape contract: camelCase keys, busy blocks carry ONLY start/end.
	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got) != 2 {
		t.Fatalf("body = %s (err %v)", rec.Body.String(), err)
	}
	if got[0]["userId"] != "u1" || got[0]["shared"] != true {
		t.Fatalf("row0 = %+v", got[0])
	}
	// F2: display identity rides along; unenriched rows serialize empty.
	if got[0]["name"] != "Ada One" || got[0]["email"] != "u1@x.com" {
		t.Fatalf("row0 identity = %v/%v, want Ada One/u1@x.com", got[0]["name"], got[0]["email"])
	}
	if got[1]["name"] != "" || got[1]["email"] != "" {
		t.Fatalf("row1 identity = %v/%v, want empty", got[1]["name"], got[1]["email"])
	}
	busy, ok := got[0]["busy"].([]any)
	if !ok || len(busy) != 1 {
		t.Fatalf("row0 busy = %+v", got[0]["busy"])
	}
	block, _ := busy[0].(map[string]any)
	if len(block) != 2 || block["start"] == nil || block["end"] == nil {
		t.Fatalf("busy block must carry only start/end: %+v", block)
	}
	if got[1]["userId"] != "u2" || got[1]["shared"] != false {
		t.Fatalf("row1 = %+v", got[1])
	}
}

func TestTeamAvailabilityRouteRangeValidation(t *testing.T) {
	h := newHarness(t)
	for _, path := range []string{
		"/v1/teams/t1/availability",
		"/v1/teams/t1/availability?from=2026-07-07T00:00:00Z",
		"/v1/teams/t1/availability?from=bogus&to=2026-07-08T00:00:00Z",
	} {
		if rec := h.authed("GET", path, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", path, rec.Code)
		}
	}
	if h.calendars.gotTeamAvailID != "" {
		t.Fatalf("service reached despite an invalid range")
	}
}

func TestTeamAvailabilityRouteErrorsAndAuth(t *testing.T) {
	h := newHarness(t)
	h.calendars.teamAvailErr = domain.ErrNotFound
	if rec := h.authed("GET", "/v1/teams/nope/availability?from=2026-07-07T00:00:00Z&to=2026-07-08T00:00:00Z", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}

	h2 := newHarness(t)
	if rec := h2.anon("GET", "/v1/teams/t1/availability?from=2026-07-07T00:00:00Z&to=2026-07-08T00:00:00Z", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon status = %d, want 401", rec.Code)
	}
}
