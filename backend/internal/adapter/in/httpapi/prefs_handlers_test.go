package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"calendium/backend/internal/domain"
)

// --- GET /v1/prefs/calendar --------------------------------------------------

// TestGetCalendarPrefs covers the read path: the service's document is
// returned verbatim with camelCase keys and no userId leak.
func TestGetCalendarPrefs(t *testing.T) {
	h := newHarness(t)
	prefs := domain.DefaultCalendarPrefs(defaultUserID)
	prefs.FocusGoalMinutesPerWeek = 600
	h.prefs.getCalendarRet = prefs

	rec := h.authed(http.MethodGet, "/v1/prefs/calendar", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if h.prefs.gotCalendarUserID != defaultUserID {
		t.Fatalf("service called with user %q, want %q", h.prefs.gotCalendarUserID, defaultUserID)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["timeZone"] != "UTC" || got["focusGoalMinutesPerWeek"] != float64(600) {
		t.Fatalf("body = %v", got)
	}
	if _, leaked := got["UserID"]; leaked {
		t.Fatalf("UserID must not be serialized: %v", got)
	}
	if got["homeLat"] != nil {
		t.Fatalf("unset homeLat must serialize as null, got %v", got["homeLat"])
	}
}

// TestGetCalendarPrefsPaymentRequired maps the entitlement error to 402.
func TestGetCalendarPrefsPaymentRequired(t *testing.T) {
	h := newHarness(t)
	h.prefs.getCalendarErr = domain.ErrPaymentRequired

	rec := h.authed(http.MethodGet, "/v1/prefs/calendar", nil)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", rec.Code)
	}
	if code := decodeErr(t, rec).Code; code != "payment_required" {
		t.Fatalf("code = %q, want payment_required", code)
	}
}

// TestGetCalendarPrefsRequiresAuth: no bearer token => 401.
func TestGetCalendarPrefsRequiresAuth(t *testing.T) {
	h := newHarness(t)
	rec := h.anon(http.MethodGet, "/v1/prefs/calendar", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// --- PATCH /v1/prefs/calendar ------------------------------------------------

// TestUpdateCalendarPrefs covers the happy path: only the JSON-present
// fields reach the service as non-nil patch pointers, and the merged
// document comes back.
func TestUpdateCalendarPrefs(t *testing.T) {
	h := newHarness(t)
	updated := domain.DefaultCalendarPrefs(defaultUserID)
	updated.AutoBufferMinutes = 10
	updated.TravelMode = domain.TravelTransit
	h.prefs.updateCalendarRet = updated

	rec := h.authed(http.MethodPatch, "/v1/prefs/calendar",
		strings.NewReader(`{"autoBufferMinutes":10,"travelMode":"transit"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	patch := h.prefs.gotCalendarPatch
	if patch.AutoBufferMinutes == nil || *patch.AutoBufferMinutes != 10 {
		t.Fatalf("AutoBufferMinutes = %v, want 10", patch.AutoBufferMinutes)
	}
	if patch.TravelMode == nil || *patch.TravelMode != domain.TravelTransit {
		t.Fatalf("TravelMode = %v, want transit", patch.TravelMode)
	}
	// Absent fields must stay nil (nil-means-unchanged).
	if patch.TimeZone != nil || patch.FocusGoalMinutesPerWeek != nil || patch.HomeLat != nil {
		t.Fatalf("absent fields decoded non-nil: %+v", patch)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["autoBufferMinutes"] != float64(10) || got["travelMode"] != "transit" {
		t.Fatalf("body = %v", got)
	}
}

// TestUpdateCalendarPrefsValidation maps domain.ErrValidation to 400.
func TestUpdateCalendarPrefsValidation(t *testing.T) {
	h := newHarness(t)
	h.prefs.updateCalendarErr = domain.ErrValidation

	rec := h.authed(http.MethodPatch, "/v1/prefs/calendar", strings.NewReader(`{"autoBufferMinutes":3}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if code := decodeErr(t, rec).Code; code != "validation_failed" {
		t.Fatalf("code = %q, want validation_failed", code)
	}
}

// TestUpdateCalendarPrefsMalformedBody: invalid JSON is a 400 before the
// service is reached.
func TestUpdateCalendarPrefsMalformedBody(t *testing.T) {
	h := newHarness(t)
	rec := h.authed(http.MethodPatch, "/v1/prefs/calendar", strings.NewReader(`{"timeZone":`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if h.prefs.gotCalendarUserID != "" {
		t.Fatalf("service must not be called on malformed JSON")
	}
}

// TestUpdateCalendarPrefsPaymentRequired maps the entitlement error to 402.
func TestUpdateCalendarPrefsPaymentRequired(t *testing.T) {
	h := newHarness(t)
	h.prefs.updateCalendarErr = domain.ErrPaymentRequired

	rec := h.authed(http.MethodPatch, "/v1/prefs/calendar", strings.NewReader(`{}`))
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", rec.Code)
	}
}
