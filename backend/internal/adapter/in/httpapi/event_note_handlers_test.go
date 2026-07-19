package httpapi

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func TestEventNoteHandlersRequireAuth(t *testing.T) {
	for _, tt := range []struct{ name, method string }{
		{"get note", http.MethodGet},
		{"put note", http.MethodPut},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			rec := h.anon(tt.method, "/v1/events/ev1/note", nil)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
		})
	}
}

func TestHandleGetEventNote(t *testing.T) {
	t.Run("empty note comes back 200 with empty fields (never 404)", func(t *testing.T) {
		h := newHarness(t)
		h.calendars.getNoteRet = domain.EventNote{EventID: "ev1", Links: []string{}}

		rec := h.authed(http.MethodGet, "/v1/events/ev1/note", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.calendars.gotGetNoteID != "ev1" {
			t.Fatalf("eventID = %q, want ev1", h.calendars.gotGetNoteID)
		}
		var got domain.EventNote
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.EventID != "ev1" || got.BodyMD != "" || len(got.Links) != 0 {
			t.Fatalf("got = %+v, want empty note for ev1", got)
		}
		// UserID is json:"-": it must never appear in the response body.
		var raw map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
			t.Fatalf("decode raw: %v", err)
		}
		for _, k := range []string{"userId", "UserID"} {
			if _, ok := raw[k]; ok {
				t.Fatalf("response leaked %s", k)
			}
		}
	})

	t.Run("cross-user event maps to 404", func(t *testing.T) {
		h := newHarness(t)
		h.calendars.getNoteErr = domain.ErrNotFound

		rec := h.authed(http.MethodGet, "/v1/events/other-ev/note", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
		if got := decodeErr(t, rec); got.Code != "not_found" {
			t.Fatalf("code = %q, want not_found", got.Code)
		}
	})
}

func TestHandlePutEventNote(t *testing.T) {
	t.Run("put forwards body and links, then re-GET serves the stored note", func(t *testing.T) {
		h := newHarness(t)
		stored := domain.EventNote{
			EventID:   "ev1",
			BodyMD:    "# Prep\n- read the doc",
			Links:     []string{"https://notion.so/doc"},
			UpdatedAt: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
		}
		h.calendars.putNoteRet = stored

		rec := h.authed(http.MethodPut, "/v1/events/ev1/note", jsonBody(t, map[string]any{
			"bodyMd": "# Prep\n- read the doc",
			"links":  []string{"https://notion.so/doc"},
		}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.calendars.gotPutNoteID != "ev1" {
			t.Fatalf("eventID = %q, want ev1", h.calendars.gotPutNoteID)
		}
		if h.calendars.gotPutNoteBody != "# Prep\n- read the doc" {
			t.Fatalf("bodyMd = %q", h.calendars.gotPutNoteBody)
		}
		if want := []string{"https://notion.so/doc"}; !reflect.DeepEqual(h.calendars.gotPutNoteLinks, want) {
			t.Fatalf("links = %v, want %v", h.calendars.gotPutNoteLinks, want)
		}

		h.calendars.getNoteRet = stored
		rec = h.authed(http.MethodGet, "/v1/events/ev1/note", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("re-GET status = %d, want 200", rec.Code)
		}
		var got domain.EventNote
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.BodyMD != stored.BodyMD || !reflect.DeepEqual(got.Links, stored.Links) {
			t.Fatalf("got = %+v, want %+v", got, stored)
		}
	})

	t.Run("cross-user event maps to 404", func(t *testing.T) {
		h := newHarness(t)
		h.calendars.putNoteErr = domain.ErrNotFound

		rec := h.authed(http.MethodPut, "/v1/events/other-ev/note", jsonBody(t, map[string]any{"bodyMd": "x"}))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("malformed JSON body is rejected", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPut, "/v1/events/ev1/note", strings.NewReader("{not json"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		if h.calendars.gotPutNoteID != "" {
			t.Fatal("service reached despite malformed body")
		}
	})
}
