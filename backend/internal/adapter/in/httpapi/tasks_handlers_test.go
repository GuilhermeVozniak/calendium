package httpapi

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func TestTasksRequireAuth(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"list", http.MethodGet, "/v1/tasks"},
		{"create", http.MethodPost, "/v1/tasks"},
		{"update", http.MethodPatch, "/v1/tasks/t1"},
		{"complete", http.MethodPost, "/v1/tasks/t1/complete"},
		{"reopen", http.MethodPost, "/v1/tasks/t1/reopen"},
		{"delete", http.MethodDelete, "/v1/tasks/t1"},
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

func TestTasksListParsing(t *testing.T) {
	from, _ := time.Parse(time.RFC3339, "2026-07-20T00:00:00Z")
	to, _ := time.Parse(time.RFC3339, "2026-07-27T00:00:00Z")

	tests := []struct {
		name       string
		query      string
		wantStatus int
		wantCall   bool
		check      func(t *testing.T, h *harness)
	}{
		{
			name:       "scheduled overlap window plus flags",
			query:      "?from=2026-07-20T00:00:00Z&to=2026-07-27T00:00:00Z&unscheduled=1&includeCompleted=1",
			wantStatus: http.StatusOK,
			wantCall:   true,
			check: func(t *testing.T, h *harness) {
				q := h.tasks.gotListQuery
				if !q.ScheduledFrom.Equal(from) || !q.ScheduledTo.Equal(to) {
					t.Fatalf("scheduled range = %v..%v, want %v..%v", q.ScheduledFrom, q.ScheduledTo, from, to)
				}
				if !q.UnscheduledOnly || !q.IncludeCompleted {
					t.Fatalf("flags = %v/%v, want true/true", q.UnscheduledOnly, q.IncludeCompleted)
				}
				if h.tasks.gotListUser != defaultUserID {
					t.Fatalf("user = %q, want %q", h.tasks.gotListUser, defaultUserID)
				}
			},
		},
		{
			name:       "due range",
			query:      "?dueFrom=2026-07-20T00:00:00Z&dueTo=2026-07-27T00:00:00Z",
			wantStatus: http.StatusOK,
			wantCall:   true,
			check: func(t *testing.T, h *harness) {
				q := h.tasks.gotListQuery
				if !q.DueFrom.Equal(from) || !q.DueTo.Equal(to) {
					t.Fatalf("due range = %v..%v, want %v..%v", q.DueFrom, q.DueTo, from, to)
				}
				if !q.ScheduledFrom.IsZero() || !q.ScheduledTo.IsZero() {
					t.Fatalf("scheduled range unexpectedly set: %v..%v", q.ScheduledFrom, q.ScheduledTo)
				}
			},
		},
		{
			name:       "one-sided dueFrom is allowed",
			query:      "?dueFrom=2026-07-20T00:00:00Z",
			wantStatus: http.StatusOK,
			wantCall:   true,
			check: func(t *testing.T, h *harness) {
				q := h.tasks.gotListQuery
				if !q.DueFrom.Equal(from) || !q.DueTo.IsZero() {
					t.Fatalf("due range = %v..%v, want %v..zero", q.DueFrom, q.DueTo, from)
				}
			},
		},
		{
			name:       "no params yields a zero query",
			query:      "",
			wantStatus: http.StatusOK,
			wantCall:   true,
			check: func(t *testing.T, h *harness) {
				q := h.tasks.gotListQuery
				if !q.ScheduledFrom.IsZero() || !q.DueFrom.IsZero() || q.UnscheduledOnly || q.IncludeCompleted {
					t.Fatalf("query = %+v, want zero", q)
				}
			},
		},
		{
			name:       "invalid from rejected",
			query:      "?from=not-a-time&to=2026-07-27T00:00:00Z",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "from without to rejected",
			query:      "?from=2026-07-20T00:00:00Z",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid dueTo rejected",
			query:      "?dueTo=tomorrow",
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.tasks.listRet = []domain.Task{{ID: "task_1", Title: "t"}}
			rec := h.authed(http.MethodGet, "/v1/tasks"+tt.query, nil)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if (h.tasks.listCalls > 0) != tt.wantCall {
				t.Fatalf("ListTasks called = %v, want %v", h.tasks.listCalls > 0, tt.wantCall)
			}
			if tt.wantStatus == http.StatusBadRequest {
				if got := decodeErr(t, rec); got.Code != "validation_failed" {
					t.Fatalf("code = %q, want validation_failed", got.Code)
				}
				return
			}
			if !strings.Contains(rec.Body.String(), `"task_1"`) {
				t.Fatalf("body %q does not contain task_1", rec.Body.String())
			}
			if tt.check != nil {
				tt.check(t, h)
			}
		})
	}
}

func TestTasksCreate(t *testing.T) {
	t.Run("happy path forwards the input", func(t *testing.T) {
		h := newHarness(t)
		h.tasks.createRet = domain.Task{ID: "task_1", Title: "Write brief"}
		body := jsonBody(t, map[string]any{
			"title":    "Write brief",
			"notes":    "details",
			"due":      "2026-07-21T17:00:00Z",
			"position": 512,
		})
		rec := h.authed(http.MethodPost, "/v1/tasks", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		in := h.tasks.gotCreateInput
		if in.Title != "Write brief" || in.Notes != "details" {
			t.Fatalf("input = %+v", in)
		}
		wantDue, _ := time.Parse(time.RFC3339, "2026-07-21T17:00:00Z")
		if in.Due == nil || !in.Due.Equal(wantDue) {
			t.Fatalf("Due = %v, want %v", in.Due, wantDue)
		}
		if in.Position == nil || *in.Position != 512 {
			t.Fatalf("Position = %v, want 512", in.Position)
		}
		if h.tasks.gotCreateUser != defaultUserID {
			t.Fatalf("user = %q, want %q", h.tasks.gotCreateUser, defaultUserID)
		}
		if !strings.Contains(rec.Body.String(), `"task_1"`) {
			t.Fatalf("body %q does not echo the created task", rec.Body.String())
		}
	})

	t.Run("malformed JSON is 400 without a service call", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/tasks", strings.NewReader("{not json"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		if h.tasks.createCalls != 0 {
			t.Fatalf("CreateTask called %d times, want 0", h.tasks.createCalls)
		}
	})

	t.Run("service errors pass through the codec", func(t *testing.T) {
		tests := []struct {
			name       string
			err        error
			wantStatus int
			wantCode   string
		}{
			{"validation", domain.ErrValidation, http.StatusBadRequest, "validation_failed"},
			{"payment required", domain.ErrPaymentRequired, http.StatusPaymentRequired, "payment_required"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				h := newHarness(t)
				h.tasks.createErr = tt.err
				rec := h.authed(http.MethodPost, "/v1/tasks", jsonBody(t, map[string]any{"title": "t"}))
				if rec.Code != tt.wantStatus {
					t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
				}
				if got := decodeErr(t, rec); got.Code != tt.wantCode {
					t.Fatalf("code = %q, want %q", got.Code, tt.wantCode)
				}
			})
		}
	})
}

func TestTasksUpdate(t *testing.T) {
	t.Run("JSON null clears, absent leaves unchanged, values set", func(t *testing.T) {
		h := newHarness(t)
		h.tasks.updateRet = domain.Task{ID: "t1", Title: "renamed"}
		rec := h.authed(http.MethodPatch, "/v1/tasks/t1",
			strings.NewReader(`{"title":"renamed","due":null,"notes":"keep","position":2048}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.tasks.gotUpdateID != "t1" || h.tasks.gotUpdateUser != defaultUserID {
			t.Fatalf("id/user = %q/%q", h.tasks.gotUpdateID, h.tasks.gotUpdateUser)
		}
		p := h.tasks.gotUpdatePatch
		if p.Title == nil || *p.Title != "renamed" {
			t.Fatalf("Title = %v, want renamed", p.Title)
		}
		if p.Due == nil {
			t.Fatal("Due = nil (absent), want explicit clear")
		}
		if *p.Due != nil {
			t.Fatalf("*Due = %v, want nil (clear)", *p.Due)
		}
		if p.Notes == nil || *p.Notes == nil || **p.Notes != "keep" {
			t.Fatalf("Notes = %v, want keep", p.Notes)
		}
		if p.ScheduledStart != nil || p.ScheduledEnd != nil || p.AllDayDue != nil {
			t.Fatalf("absent fields set: %+v", p)
		}
		if p.Position == nil || *p.Position != 2048 {
			t.Fatalf("Position = %v, want 2048", p.Position)
		}
	})

	t.Run("due value sets both pointer levels", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPatch, "/v1/tasks/t1",
			strings.NewReader(`{"due":"2026-07-21T17:00:00Z"}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		p := h.tasks.gotUpdatePatch
		want, _ := time.Parse(time.RFC3339, "2026-07-21T17:00:00Z")
		if p.Due == nil || *p.Due == nil || !(*p.Due).Equal(want) {
			t.Fatalf("Due = %v, want %v", p.Due, want)
		}
	})

	t.Run("invalid due value is 400 without a service call", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPatch, "/v1/tasks/t1", strings.NewReader(`{"due":"tomorrow"}`))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.tasks.updateCalls != 0 {
			t.Fatalf("UpdateTask called %d times, want 0", h.tasks.updateCalls)
		}
		if got := decodeErr(t, rec); got.Code != "validation_failed" {
			t.Fatalf("code = %q, want validation_failed", got.Code)
		}
	})

	t.Run("not found passes through as 404", func(t *testing.T) {
		h := newHarness(t)
		h.tasks.updateErr = domain.ErrNotFound
		rec := h.authed(http.MethodPatch, "/v1/tasks/nope", strings.NewReader(`{"title":"x"}`))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

func TestTasksCompleteReopen(t *testing.T) {
	t.Run("complete", func(t *testing.T) {
		h := newHarness(t)
		h.tasks.completeRet = domain.Task{ID: "t1", Title: "t"}
		rec := h.authed(http.MethodPost, "/v1/tasks/t1/complete", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.tasks.gotCompleteID != "t1" {
			t.Fatalf("id = %q, want t1", h.tasks.gotCompleteID)
		}
	})

	t.Run("reopen", func(t *testing.T) {
		h := newHarness(t)
		h.tasks.reopenRet = domain.Task{ID: "t1", Title: "t"}
		rec := h.authed(http.MethodPost, "/v1/tasks/t1/reopen", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.tasks.gotReopenID != "t1" {
			t.Fatalf("id = %q, want t1", h.tasks.gotReopenID)
		}
	})

	t.Run("complete not found is 404", func(t *testing.T) {
		h := newHarness(t)
		h.tasks.completeErr = domain.ErrNotFound
		rec := h.authed(http.MethodPost, "/v1/tasks/nope/complete", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

func TestTasksDelete(t *testing.T) {
	t.Run("deletes and answers 204", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodDelete, "/v1/tasks/t1", nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.tasks.gotDeleteID != "t1" {
			t.Fatalf("id = %q, want t1", h.tasks.gotDeleteID)
		}
	})

	t.Run("not found is 404", func(t *testing.T) {
		h := newHarness(t)
		h.tasks.deleteErr = domain.ErrNotFound
		rec := h.authed(http.MethodDelete, "/v1/tasks/nope", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}
