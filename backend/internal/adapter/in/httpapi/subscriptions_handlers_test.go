package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func TestSubscriptionHandlersRequireAuth(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"list", http.MethodGet, "/v1/calendar-subscriptions"},
		{"create", http.MethodPost, "/v1/calendar-subscriptions"},
		{"update", http.MethodPatch, "/v1/calendar-subscriptions/s1"},
		{"delete", http.MethodDelete, "/v1/calendar-subscriptions/s1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			rec := h.anon(tt.method, tt.path, nil)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
		})
	}
}

func TestHandleListCalendarSubscriptions(t *testing.T) {
	h := newHarness(t)
	fetched := time.Date(2026, 7, 19, 11, 0, 0, 0, time.UTC)
	h.calendars.listSubsRet = []domain.CalendarSubscription{{
		ID: "s1", UserID: "secret-user", URL: "https://example.com/h.ics",
		Name: "Holidays", Color: "#8b5cf6", IsVisible: true,
		Etag: `"secret-etag"`, LastFetchedAt: &fetched,
	}}

	rec := h.authed(http.MethodGet, "/v1/calendar-subscriptions", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0]["id"] != "s1" || got[0]["name"] != "Holidays" {
		t.Fatalf("body = %v", got)
	}
	// Internal fields must never serialize.
	if _, ok := got[0]["UserID"]; ok {
		t.Fatal("UserID leaked into JSON")
	}
	for k := range got[0] {
		if k == "Etag" || k == "etag" {
			t.Fatal("etag leaked into JSON")
		}
	}
}

func TestHandleCreateCalendarSubscription(t *testing.T) {
	t.Run("happy path passes input through", func(t *testing.T) {
		h := newHarness(t)
		h.calendars.createSubRet = domain.CalendarSubscription{ID: "s1", URL: "https://example.com/h.ics", Name: "US Holidays"}

		rec := h.authed(http.MethodPost, "/v1/calendar-subscriptions",
			jsonBody(t, map[string]string{"url": "https://example.com/h.ics", "name": "", "color": "#123456"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.calendars.gotCreateSub.URL != "https://example.com/h.ics" || h.calendars.gotCreateSub.Color != "#123456" {
			t.Fatalf("input = %+v", h.calendars.gotCreateSub)
		}
		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got["name"] != "US Holidays" {
			t.Fatalf("body = %v, want the service-resolved name", got)
		}
	})

	t.Run("invalid URL surfaces as 400", func(t *testing.T) {
		h := newHarness(t)
		h.calendars.createSubErr = fmt.Errorf("%w: url must be an absolute https URL", domain.ErrValidation)
		rec := h.authed(http.MethodPost, "/v1/calendar-subscriptions",
			jsonBody(t, map[string]string{"url": "http://example.com/h.ics"}))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		if decodeErr(t, rec).Code != "validation_failed" {
			t.Fatalf("code = %q", decodeErr(t, rec).Code)
		}
	})

	t.Run("unfetchable feed surfaces as 422 with a stable message", func(t *testing.T) {
		h := newHarness(t)
		h.calendars.createSubErr = fmt.Errorf("%w: feed answered status 500", domain.ErrUnprocessable)
		rec := h.authed(http.MethodPost, "/v1/calendar-subscriptions",
			jsonBody(t, map[string]string{"url": "https://example.com/h.ics"}))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422 (body=%s)", rec.Code, rec.Body.String())
		}
		detail := decodeErr(t, rec)
		if detail.Code != "unprocessable" || detail.Message == "" {
			t.Fatalf("error = %+v, want code unprocessable with a message", detail)
		}
	})

	t.Run("malformed JSON body is 400", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/calendar-subscriptions", jsonBody(t, "not-an-object"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
}

func TestHandleUpdateCalendarSubscription(t *testing.T) {
	t.Run("patches visibility and color", func(t *testing.T) {
		h := newHarness(t)
		h.calendars.updateSubRet = domain.CalendarSubscription{ID: "s1", Color: "#00ff00", IsVisible: false}

		rec := h.authed(http.MethodPatch, "/v1/calendar-subscriptions/s1",
			jsonBody(t, map[string]any{"isVisible": false, "color": "#00ff00"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d (body=%s)", rec.Code, rec.Body.String())
		}
		if h.calendars.gotUpdateSubID != "s1" {
			t.Fatalf("id = %q, want s1", h.calendars.gotUpdateSubID)
		}
		p := h.calendars.gotUpdateSub
		if p.IsVisible == nil || *p.IsVisible || p.Color == nil || *p.Color != "#00ff00" || p.Name != nil {
			t.Fatalf("patch = %+v", p)
		}
	})

	t.Run("someone else's subscription is 404", func(t *testing.T) {
		h := newHarness(t)
		h.calendars.updateSubErr = domain.ErrNotFound
		rec := h.authed(http.MethodPatch, "/v1/calendar-subscriptions/other",
			jsonBody(t, map[string]any{"isVisible": true}))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

func TestHandleDeleteCalendarSubscription(t *testing.T) {
	h := newHarness(t)
	rec := h.authed(http.MethodDelete, "/v1/calendar-subscriptions/s1", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if h.calendars.gotDeleteSubID != "s1" {
		t.Fatalf("id = %q, want s1", h.calendars.gotDeleteSubID)
	}
}
