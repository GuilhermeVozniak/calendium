package httpapi

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// auth check for template/set handlers
func TestTemplateAndSetHandlersRequireAuth(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"list templates", http.MethodGet, "/v1/event-templates"},
		{"create template", http.MethodPost, "/v1/event-templates"},
		{"update template", http.MethodPut, "/v1/event-templates/t1"},
		{"delete template", http.MethodDelete, "/v1/event-templates/t1"},
		{"use template", http.MethodPost, "/v1/event-templates/t1/use"},
		{"list sets", http.MethodGet, "/v1/calendar-sets"},
		{"create set", http.MethodPost, "/v1/calendar-sets"},
		{"update set", http.MethodPut, "/v1/calendar-sets/s1"},
		{"delete set", http.MethodDelete, "/v1/calendar-sets/s1"},
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

func TestHandleListEventsParsing(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantStatus int
		wantCall   bool
		check      func(t *testing.T, h *harness)
	}{
		{
			name:       "from/to and repeated+comma calendarIds parsed",
			query:      "?from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z&calendarIds=c1,c2&calendarIds=c3",
			wantStatus: http.StatusOK,
			wantCall:   true,
			check: func(t *testing.T, h *harness) {
				wantFrom, _ := time.Parse(time.RFC3339, "2026-01-01T00:00:00Z")
				wantTo, _ := time.Parse(time.RFC3339, "2026-01-02T00:00:00Z")
				if !h.calendars.gotEventsFrom.Equal(wantFrom) {
					t.Fatalf("from = %v, want %v", h.calendars.gotEventsFrom, wantFrom)
				}
				if !h.calendars.gotEventsTo.Equal(wantTo) {
					t.Fatalf("to = %v, want %v", h.calendars.gotEventsTo, wantTo)
				}
				want := []string{"c1", "c2", "c3"}
				if !reflect.DeepEqual(h.calendars.gotEventsCalIDs, want) {
					t.Fatalf("calendarIds = %v, want %v", h.calendars.gotEventsCalIDs, want)
				}
			},
		},
		{
			name:       "invalid from rejected",
			query:      "?from=not-a-time&to=2026-01-02T00:00:00Z",
			wantStatus: http.StatusBadRequest,
			wantCall:   false,
		},
		{
			name:       "invalid/empty to rejected",
			query:      "?from=2026-01-01T00:00:00Z&to=",
			wantStatus: http.StatusBadRequest,
			wantCall:   false,
		},
		{
			name:       "no calendarIds yields nil",
			query:      "?from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z",
			wantStatus: http.StatusOK,
			wantCall:   true,
			check: func(t *testing.T, h *harness) {
				if h.calendars.gotEventsCalIDs != nil {
					t.Fatalf("calendarIds = %v, want nil", h.calendars.gotEventsCalIDs)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			rec := h.authed(http.MethodGet, "/v1/events"+tt.query, nil)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if (h.calendars.listEventsCalls > 0) != tt.wantCall {
				t.Fatalf("ListEvents called = %v, want %v", h.calendars.listEventsCalls > 0, tt.wantCall)
			}
			if tt.wantStatus == http.StatusBadRequest {
				if got := decodeErr(t, rec); got.Code != "validation_failed" {
					t.Fatalf("code = %q, want validation_failed", got.Code)
				}
			}
			if tt.check != nil {
				tt.check(t, h)
			}
		})
	}
}

func TestHandleAvailability(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantStatus int
		wantCall   bool
	}{
		{"valid duration", "?from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z&duration=30", http.StatusOK, true},
		{"missing duration", "?from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z", http.StatusBadRequest, false},
		{"zero duration", "?from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z&duration=0", http.StatusBadRequest, false},
		{"negative duration", "?from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z&duration=-5", http.StatusBadRequest, false},
		{"non-numeric duration", "?from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z&duration=abc", http.StatusBadRequest, false},
		{"invalid from/to", "?from=nope&to=2026-01-02T00:00:00Z&duration=30", http.StatusBadRequest, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			rec := h.authed(http.MethodGet, "/v1/availability"+tt.query, nil)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if (h.calendars.availCalls > 0) != tt.wantCall {
				t.Fatalf("Availability called = %v, want %v", h.calendars.availCalls > 0, tt.wantCall)
			}
			if tt.wantStatus == http.StatusBadRequest {
				if got := decodeErr(t, rec); got.Code != "validation_failed" {
					t.Fatalf("code = %q, want validation_failed", got.Code)
				}
			}
			if tt.wantCall && h.calendars.gotAvailDur != 30*time.Minute {
				t.Fatalf("gotAvailDur = %v, want 30m", h.calendars.gotAvailDur)
			}
		})
	}
}

func TestHandleListCalendars(t *testing.T) {
	h := newHarness(t)
	h.calendars.listCalsRet = []domain.Calendar{{ID: "c1"}}
	rec := h.authed(http.MethodGet, "/v1/calendars", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var got []domain.Calendar
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].ID != "c1" {
		t.Fatalf("calendars = %+v", got)
	}
}

func TestHandleUpdateCalendar(t *testing.T) {
	t.Run("valid patch forwarded", func(t *testing.T) {
		h := newHarness(t)
		isVisible := false
		color := "#fff"
		rec := h.authed(http.MethodPatch, "/v1/calendars/cal1", jsonBody(t, port.CalendarPatch{IsVisible: &isVisible, Color: &color}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.calendars.gotUpdateCal.IsVisible == nil || *h.calendars.gotUpdateCal.IsVisible != false {
			t.Fatalf("IsVisible = %v, want false", h.calendars.gotUpdateCal.IsVisible)
		}
		if h.calendars.gotUpdateCal.Color == nil || *h.calendars.gotUpdateCal.Color != "#fff" {
			t.Fatalf("Color = %v, want #fff", h.calendars.gotUpdateCal.Color)
		}
	})

	t.Run("not found", func(t *testing.T) {
		h := newHarness(t)
		h.calendars.updateCalErr = domain.ErrNotFound
		rec := h.authed(http.MethodPatch, "/v1/calendars/cal1", jsonBody(t, port.CalendarPatch{}))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

func TestHandleCreateEvent(t *testing.T) {
	t.Run("valid input", func(t *testing.T) {
		h := newHarness(t)
		start, _ := time.Parse(time.RFC3339, "2026-01-01T00:00:00Z")
		end, _ := time.Parse(time.RFC3339, "2026-01-01T01:00:00Z")
		rec := h.authed(http.MethodPost, "/v1/events", jsonBody(t, domain.EventInput{
			CalendarID: "cal1", Title: "Meeting", Start: start, End: end,
		}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("malformed JSON rejected", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/events", strings.NewReader("{"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
}

func TestHandleUpdateEvent(t *testing.T) {
	t.Run("valid patch", func(t *testing.T) {
		h := newHarness(t)
		title := "New title"
		rec := h.authed(http.MethodPatch, "/v1/events/ev1", jsonBody(t, domain.EventPatch{Title: &title}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("not found", func(t *testing.T) {
		h := newHarness(t)
		h.calendars.updateEventErr = domain.ErrNotFound
		rec := h.authed(http.MethodPatch, "/v1/events/ev1", jsonBody(t, domain.EventPatch{}))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

func TestHandleDeleteEvent(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodDelete, "/v1/events/ev1", nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.calendars.gotDeleteEvt != "ev1" {
			t.Fatalf("gotDeleteEvt = %q, want ev1", h.calendars.gotDeleteEvt)
		}
	})

	t.Run("not found", func(t *testing.T) {
		h := newHarness(t)
		h.calendars.deleteEventErr = domain.ErrNotFound
		rec := h.authed(http.MethodDelete, "/v1/events/ev1", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

func TestHandleRsvp(t *testing.T) {
	t.Run("valid response", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/events/ev1/rsvp", jsonBody(t, map[string]string{"response": "accepted"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.calendars.gotRsvp != domain.RsvpAccepted {
			t.Fatalf("gotRsvp = %q, want accepted", h.calendars.gotRsvp)
		}
		if h.calendars.gotRsvpID != "ev1" {
			t.Fatalf("gotRsvpID = %q, want ev1", h.calendars.gotRsvpID)
		}
		if h.calendars.gotRsvpComment != "" {
			t.Fatalf("gotRsvpComment = %q, want empty when omitted", h.calendars.gotRsvpComment)
		}
	})

	t.Run("comment is passed through", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/events/ev1/rsvp", jsonBody(t, map[string]string{"response": "declined", "comment": "Out of office this week."}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.calendars.gotRsvp != domain.RsvpDeclined {
			t.Fatalf("gotRsvp = %q, want declined", h.calendars.gotRsvp)
		}
		if h.calendars.gotRsvpComment != "Out of office this week." {
			t.Fatalf("gotRsvpComment = %q, want the custom message", h.calendars.gotRsvpComment)
		}
	})

	t.Run("invalid response rejected", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/events/ev1/rsvp", jsonBody(t, map[string]string{"response": "maybe"}))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "validation_failed" {
			t.Fatalf("code = %q, want validation_failed", got.Code)
		}
		if h.calendars.gotRsvpID != "" {
			t.Fatalf("RSVP called unexpectedly")
		}
	})

	t.Run("malformed JSON rejected", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/events/ev1/rsvp", strings.NewReader("{"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
}

// --- Event Template Handlers -------------------------------------------------

func TestHandleListEventTemplates(t *testing.T) {
	t.Run("empty list", func(t *testing.T) {
		h := newHarness(t)
		h.calendars.listTemplatesRet = []domain.EventTemplate{}
		rec := h.authed(http.MethodGet, "/v1/event-templates", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		var got []domain.EventTemplate
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("templates = %+v, want empty", got)
		}
	})

	t.Run("multiple templates", func(t *testing.T) {
		h := newHarness(t)
		h.calendars.listTemplatesRet = []domain.EventTemplate{
			{ID: "t1", Name: "Meeting"},
			{ID: "t2", Name: "1:1"},
		}
		rec := h.authed(http.MethodGet, "/v1/event-templates", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var got []domain.EventTemplate
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got) != 2 || got[0].ID != "t1" || got[1].ID != "t2" {
			t.Fatalf("templates = %+v", got)
		}
	})
}

func TestHandleCreateEventTemplate(t *testing.T) {
	t.Run("valid input", func(t *testing.T) {
		h := newHarness(t)
		input := domain.EventTemplateInput{
			Name:            "Meeting",
			Title:           "1:1 Meeting",
			DurationMinutes: 30,
		}
		h.calendars.createTemplateRet = domain.EventTemplate{ID: "t1", Name: "Meeting", Title: "1:1 Meeting"}
		rec := h.authed(http.MethodPost, "/v1/event-templates", jsonBody(t, input))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.calendars.gotCreateTemplate.Name != "Meeting" {
			t.Fatalf("name = %q, want Meeting", h.calendars.gotCreateTemplate.Name)
		}
		if h.calendars.gotCreateTemplate.DurationMinutes != 30 {
			t.Fatalf("duration = %d, want 30", h.calendars.gotCreateTemplate.DurationMinutes)
		}
	})

	t.Run("malformed JSON rejected", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/event-templates", strings.NewReader("{"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
}

func TestHandleUpdateEventTemplate(t *testing.T) {
	t.Run("valid patch", func(t *testing.T) {
		h := newHarness(t)
		input := domain.EventTemplateInput{
			Name:            "Updated Meeting",
			Title:           "Updated 1:1",
			DurationMinutes: 45,
		}
		h.calendars.updateTemplateRet = domain.EventTemplate{ID: "t1", Name: "Updated Meeting"}
		rec := h.authed(http.MethodPut, "/v1/event-templates/t1", jsonBody(t, input))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.calendars.gotUpdateTemplID != "t1" {
			t.Fatalf("templateID = %q, want t1", h.calendars.gotUpdateTemplID)
		}
		if h.calendars.gotUpdateTemplate.Name != "Updated Meeting" {
			t.Fatalf("name = %q, want Updated Meeting", h.calendars.gotUpdateTemplate.Name)
		}
	})

	t.Run("not found", func(t *testing.T) {
		h := newHarness(t)
		h.calendars.updateTemplateErr = domain.ErrNotFound
		input := domain.EventTemplateInput{Name: "test", DurationMinutes: 30}
		rec := h.authed(http.MethodPut, "/v1/event-templates/t1", jsonBody(t, input))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("malformed JSON rejected", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPut, "/v1/event-templates/t1", strings.NewReader("{"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
}

func TestHandleDeleteEventTemplate(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodDelete, "/v1/event-templates/t1", nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.calendars.gotDeleteTemplID != "t1" {
			t.Fatalf("templateID = %q, want t1", h.calendars.gotDeleteTemplID)
		}
	})

	t.Run("not found", func(t *testing.T) {
		h := newHarness(t)
		h.calendars.deleteTemplateErr = domain.ErrNotFound
		rec := h.authed(http.MethodDelete, "/v1/event-templates/t1", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

func TestHandleUseEventTemplate(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/event-templates/t1/use", nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.calendars.gotUseTemplID != "t1" {
			t.Fatalf("templateID = %q, want t1", h.calendars.gotUseTemplID)
		}
		if h.calendars.useTemplateCalls != 1 {
			t.Fatalf("useTemplateCalls = %d, want 1", h.calendars.useTemplateCalls)
		}
	})

	t.Run("not found", func(t *testing.T) {
		h := newHarness(t)
		h.calendars.useTemplateErr = domain.ErrNotFound
		rec := h.authed(http.MethodPost, "/v1/event-templates/t1/use", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

// --- Calendar Set Handlers ---------------------------------------------------

func TestHandleListCalendarSets(t *testing.T) {
	t.Run("empty list", func(t *testing.T) {
		h := newHarness(t)
		h.calendars.listSetsRet = []domain.CalendarSet{}
		rec := h.authed(http.MethodGet, "/v1/calendar-sets", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		var got []domain.CalendarSet
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("sets = %+v, want empty", got)
		}
	})

	t.Run("multiple sets", func(t *testing.T) {
		h := newHarness(t)
		h.calendars.listSetsRet = []domain.CalendarSet{
			{ID: "s1", Name: "Work", CalendarIDs: []string{"c1"}},
			{ID: "s2", Name: "Personal", CalendarIDs: []string{"c2"}},
		}
		rec := h.authed(http.MethodGet, "/v1/calendar-sets", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var got []domain.CalendarSet
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got) != 2 || got[0].ID != "s1" || got[1].ID != "s2" {
			t.Fatalf("sets = %+v", got)
		}
	})
}

func TestHandleCreateCalendarSet(t *testing.T) {
	t.Run("valid input", func(t *testing.T) {
		h := newHarness(t)
		input := domain.CalendarSetInput{
			Name:        "Work",
			CalendarIDs: []string{"c1", "c2"},
			Position:    0,
		}
		h.calendars.createSetRet = domain.CalendarSet{ID: "s1", Name: "Work", CalendarIDs: []string{"c1", "c2"}}
		rec := h.authed(http.MethodPost, "/v1/calendar-sets", jsonBody(t, input))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.calendars.gotCreateSet.Name != "Work" {
			t.Fatalf("name = %q, want Work", h.calendars.gotCreateSet.Name)
		}
		if len(h.calendars.gotCreateSet.CalendarIDs) != 2 {
			t.Fatalf("calendarIDs = %v, want 2 items", h.calendars.gotCreateSet.CalendarIDs)
		}
	})

	t.Run("malformed JSON rejected", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/calendar-sets", strings.NewReader("{"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
}

func TestHandleUpdateCalendarSet(t *testing.T) {
	t.Run("valid patch", func(t *testing.T) {
		h := newHarness(t)
		input := domain.CalendarSetInput{
			Name:        "Updated Work",
			CalendarIDs: []string{"c1", "c2", "c3"},
			Position:    1,
		}
		h.calendars.updateSetRet = domain.CalendarSet{ID: "s1", Name: "Updated Work"}
		rec := h.authed(http.MethodPut, "/v1/calendar-sets/s1", jsonBody(t, input))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.calendars.gotUpdateSetID != "s1" {
			t.Fatalf("setID = %q, want s1", h.calendars.gotUpdateSetID)
		}
		if h.calendars.gotUpdateSet.Name != "Updated Work" {
			t.Fatalf("name = %q, want Updated Work", h.calendars.gotUpdateSet.Name)
		}
	})

	t.Run("not found", func(t *testing.T) {
		h := newHarness(t)
		h.calendars.updateSetErr = domain.ErrNotFound
		input := domain.CalendarSetInput{Name: "test", CalendarIDs: []string{}}
		rec := h.authed(http.MethodPut, "/v1/calendar-sets/s1", jsonBody(t, input))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("malformed JSON rejected", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPut, "/v1/calendar-sets/s1", strings.NewReader("{"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
}

func TestHandleDeleteCalendarSet(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodDelete, "/v1/calendar-sets/s1", nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.calendars.gotDeleteSetID != "s1" {
			t.Fatalf("setID = %q, want s1", h.calendars.gotDeleteSetID)
		}
	})

	t.Run("not found", func(t *testing.T) {
		h := newHarness(t)
		h.calendars.deleteSetErr = domain.ErrNotFound
		rec := h.authed(http.MethodDelete, "/v1/calendar-sets/s1", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}
