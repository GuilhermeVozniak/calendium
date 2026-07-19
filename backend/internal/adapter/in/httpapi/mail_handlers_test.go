package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

func TestListThreadsParsing(t *testing.T) {
	tests := []struct {
		name       string
		query      string // raw query string appended to /v1/mail/threads
		wantStatus int
		wantCall   bool // did Mail.ListThreads get invoked?
		check      func(t *testing.T, q port.ThreadQuery)
	}{
		{
			name:       "all filters parse",
			query:      "?labelId=lbl1&q=hello&cursor=cur1&split=important&view=starred&limit=25",
			wantStatus: http.StatusOK,
			wantCall:   true,
			check: func(t *testing.T, q port.ThreadQuery) {
				if q.LabelID != "lbl1" || q.Query != "hello" || q.Cursor != "cur1" {
					t.Fatalf("string filters = %+v", q)
				}
				if q.Split != domain.SplitImportant {
					t.Fatalf("split = %q, want %q", q.Split, domain.SplitImportant)
				}
				if q.View != domain.ThreadViewStarred {
					t.Fatalf("view = %q, want %q", q.View, domain.ThreadViewStarred)
				}
				if q.Limit != 25 {
					t.Fatalf("limit = %d, want 25", q.Limit)
				}
			},
		},
		{
			name:       "no optional params leaves zero values",
			query:      "",
			wantStatus: http.StatusOK,
			wantCall:   true,
			check: func(t *testing.T, q port.ThreadQuery) {
				if q.Split != "" || q.View != "" || q.Limit != 0 || q.LabelID != "" {
					t.Fatalf("expected zero-valued query, got %+v", q)
				}
			},
		},
		{name: "invalid split rejected", query: "?split=bogus", wantStatus: http.StatusBadRequest, wantCall: false},
		{name: "invalid view rejected", query: "?view=drafts", wantStatus: http.StatusBadRequest, wantCall: false},
		{name: "non-numeric limit rejected", query: "?limit=abc", wantStatus: http.StatusBadRequest, wantCall: false},
		{name: "zero limit rejected", query: "?limit=0", wantStatus: http.StatusBadRequest, wantCall: false},
		{name: "negative limit rejected", query: "?limit=-5", wantStatus: http.StatusBadRequest, wantCall: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			rec := h.authed(http.MethodGet, "/v1/mail/threads"+tt.query, nil)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if (h.mail.listCalls > 0) != tt.wantCall {
				t.Fatalf("ListThreads called = %v, want %v", h.mail.listCalls > 0, tt.wantCall)
			}
			if tt.wantStatus == http.StatusBadRequest {
				if got := decodeErr(t, rec); got.Code != "validation_failed" {
					t.Fatalf("code = %q, want validation_failed", got.Code)
				}
			}
			if tt.wantCall {
				if h.mail.gotListUserID != defaultUserID {
					t.Fatalf("userID = %q, want %q", h.mail.gotListUserID, defaultUserID)
				}
				if tt.check != nil {
					tt.check(t, h.mail.gotListQuery)
				}
			}
		})
	}
}

func TestHandleListThreadsIncludesAiSummary(t *testing.T) {
	// Task 6 (live auto-summarize): Thread.Summary carries json:"summary" so
	// once the worker job persists a value via ThreadRepo.SetSummary, it
	// must flow through the existing list-threads response unchanged — no
	// handler code paths needed to be added for this, but it's worth a test
	// asserting the field isn't dropped/renamed anywhere in the response
	// pipeline.
	h := newHarness(t)
	h.mail.listPage = domain.Page[domain.Thread]{
		Items: []domain.Thread{{ID: "th1", Subject: "Q3 planning", Summary: "Discussing Q3 roadmap, waiting on budget sign-off"}},
	}
	rec := h.authed(http.MethodGet, "/v1/mail/threads", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var got domain.Page[domain.Thread]
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].Summary != "Discussing Q3 roadmap, waiting on budget sign-off" {
		t.Fatalf("items = %+v, want summary to flow through from the repo", got.Items)
	}
	if !strings.Contains(rec.Body.String(), `"summary":"Discussing Q3 roadmap`) {
		t.Fatalf("body missing raw \"summary\" JSON field: %s", rec.Body.String())
	}
}

func TestHandleListThreadsPaywall(t *testing.T) {
	h := newHarness(t)
	h.mail.listErr = domain.ErrPaymentRequired
	rec := h.authed(http.MethodGet, "/v1/mail/threads", nil)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402 (body=%s)", rec.Code, rec.Body.String())
	}
	if got := decodeErr(t, rec); got.Code != "payment_required" {
		t.Fatalf("code = %q, want payment_required", got.Code)
	}
}

func TestHandleGetThread(t *testing.T) {
	t.Run("success returns thread and messages", func(t *testing.T) {
		h := newHarness(t)
		h.mail.getThreadThread = domain.Thread{ID: "th1"}
		h.mail.getThreadMsgs = []domain.Message{{ID: "m1"}}
		rec := h.authed(http.MethodGet, "/v1/mail/threads/th1", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.mail.gotGetThreadID != "th1" {
			t.Fatalf("gotGetThreadID = %q, want th1", h.mail.gotGetThreadID)
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if _, ok := body["thread"]; !ok {
			t.Fatalf("missing top-level 'thread' key in %s", rec.Body.String())
		}
		if _, ok := body["messages"]; !ok {
			t.Fatalf("missing top-level 'messages' key in %s", rec.Body.String())
		}
	})

	t.Run("other user's thread is not found", func(t *testing.T) {
		h := newHarness(t)
		h.mail.getThreadErr = domain.ErrNotFound
		rec := h.authed(http.MethodGet, "/v1/mail/threads/th1", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "not_found" {
			t.Fatalf("code = %q, want not_found", got.Code)
		}
	})
}

func TestHandleThreadAction(t *testing.T) {
	t.Run("valid action forwarded", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/mail/threads/th1/actions", jsonBody(t, map[string]string{"action": "archive"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.mail.gotAction != domain.ThreadActionArchive {
			t.Fatalf("gotAction = %q, want archive", h.mail.gotAction)
		}
		if h.mail.gotActID != "th1" {
			t.Fatalf("gotActID = %q, want th1", h.mail.gotActID)
		}
	})

	t.Run("invalid action rejected", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/mail/threads/th1/actions", jsonBody(t, map[string]string{"action": "frobnicate"}))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "validation_failed" {
			t.Fatalf("code = %q, want validation_failed", got.Code)
		}
		if h.mail.gotActID != "" {
			t.Fatalf("ActOnThread called unexpectedly")
		}
	})

	t.Run("malformed JSON body rejected", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/mail/threads/th1/actions", strings.NewReader("{"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		if got := decodeErr(t, rec); got.Code != "validation_failed" {
			t.Fatalf("code = %q, want validation_failed", got.Code)
		}
	})
}

func TestHandleMarkThreadOpened(t *testing.T) {
	h := newHarness(t)
	rec := h.authed(http.MethodPost, "/v1/mail/threads/th1/open", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", rec.Body.String())
	}
	if h.mail.gotMarkID != "th1" {
		t.Fatalf("gotMarkID = %q, want th1", h.mail.gotMarkID)
	}
}

func TestHandleSnoozeThread(t *testing.T) {
	t.Run("valid until parses", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/mail/threads/th1/snooze", jsonBody(t, map[string]string{"until": "2030-01-01T00:00:00Z"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.mail.gotUntil.IsZero() {
			t.Fatalf("gotUntil is zero, want a parsed timestamp")
		}
	})

	t.Run("missing until rejected", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/mail/threads/th1/snooze", jsonBody(t, map[string]string{}))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "validation_failed" {
			t.Fatalf("code = %q, want validation_failed", got.Code)
		}
		if !h.mail.gotUntil.IsZero() {
			t.Fatalf("SnoozeThread called unexpectedly")
		}
	})
}

func TestHandleThreadReminder(t *testing.T) {
	t.Run("set reminder", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/mail/threads/th1/reminder", jsonBody(t, map[string]string{"remindAt": "2030-01-01T00:00:00Z"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.mail.gotRemindAt == nil {
			t.Fatalf("gotRemindAt = nil, want set")
		}
	})

	t.Run("null remindAt clears reminder", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/mail/threads/th1/reminder", strings.NewReader(`{"remindAt":null}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.mail.gotRemindAt != nil {
			t.Fatalf("gotRemindAt = %v, want nil", h.mail.gotRemindAt)
		}
	})
}

func TestHandleListDrafts(t *testing.T) {
	h := newHarness(t)
	h.mail.listDraftsRet = []domain.Draft{{ID: "d1"}, {ID: "d2"}}
	rec := h.authed(http.MethodGet, "/v1/mail/drafts", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var got []domain.Draft
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 || got[0].ID != "d1" || got[1].ID != "d2" {
		t.Fatalf("drafts = %+v", got)
	}
}

func TestHandleUpdateDraft(t *testing.T) {
	t.Run("valid update", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPut, "/v1/mail/drafts/d1", jsonBody(t, port.DraftInput{AccountID: "acc1", Subject: "updated"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.mail.gotUpdateDraft.Subject != "updated" {
			t.Fatalf("gotUpdateDraft.Subject = %q, want updated", h.mail.gotUpdateDraft.Subject)
		}
	})

	t.Run("not found", func(t *testing.T) {
		h := newHarness(t)
		h.mail.updateDraftErr = domain.ErrNotFound
		rec := h.authed(http.MethodPut, "/v1/mail/drafts/d1", jsonBody(t, port.DraftInput{AccountID: "acc1"}))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
		}
	})
}

func TestHandleDeleteDraft(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodDelete, "/v1/mail/drafts/d1", nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.mail.gotDeleteDraft != "d1" {
			t.Fatalf("gotDeleteDraft = %q, want d1", h.mail.gotDeleteDraft)
		}
	})

	t.Run("not found", func(t *testing.T) {
		h := newHarness(t)
		h.mail.deleteDraftErr = domain.ErrNotFound
		rec := h.authed(http.MethodDelete, "/v1/mail/drafts/d1", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

func TestHandleSendDraft(t *testing.T) {
	h := newHarness(t)
	h.mail.sendDraftRet = domain.Message{ID: "m1"}
	rec := h.authed(http.MethodPost, "/v1/mail/drafts/d1/send", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var got domain.Message
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != "m1" {
		t.Fatalf("message id = %q, want m1", got.ID)
	}
}

func TestHandleUnsendDraft(t *testing.T) {
	h := newHarness(t)
	h.mail.unsendDraftErr = domain.ErrConflict
	rec := h.authed(http.MethodPost, "/v1/mail/drafts/d1/unsend", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", rec.Code, rec.Body.String())
	}
	if got := decodeErr(t, rec); got.Code != "conflict" {
		t.Fatalf("code = %q, want conflict", got.Code)
	}
}

func TestHandleSnippets(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		h := newHarness(t)
		h.mail.listSnippetsRet = []domain.Snippet{{ID: "s1"}}
		rec := h.authed(http.MethodGet, "/v1/mail/snippets", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("create success", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/mail/snippets", jsonBody(t, port.SnippetInput{Name: "n"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("create malformed JSON", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/mail/snippets", strings.NewReader("{"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("update success", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPut, "/v1/mail/snippets/s1", jsonBody(t, port.SnippetInput{Name: "n2"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("update malformed JSON", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPut, "/v1/mail/snippets/s1", strings.NewReader("{"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("delete success", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodDelete, "/v1/mail/snippets/s1", nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("delete not found", func(t *testing.T) {
		h := newHarness(t)
		h.mail.deleteSnipErr = domain.ErrNotFound
		rec := h.authed(http.MethodDelete, "/v1/mail/snippets/s1", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

func TestHandleListLabels(t *testing.T) {
	h := newHarness(t)
	h.mail.listLabelsRet = []domain.Label{{ID: "l1", Name: "Follow up"}}
	rec := h.authed(http.MethodGet, "/v1/mail/labels", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var got []domain.Label
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "l1" {
		t.Fatalf("got = %+v", got)
	}
}

func TestHandleSetThreadLabel(t *testing.T) {
	t.Run("add success", func(t *testing.T) {
		h := newHarness(t)
		h.mail.setLabelRet = domain.Thread{ID: "th1", LabelIDs: []string{"l1"}}
		rec := h.authed(http.MethodPost, "/v1/mail/threads/th1/labels", jsonBody(t, map[string]any{"labelId": "l1", "add": true}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.mail.gotSetLabelID != "th1" || h.mail.gotSetLabelLabelID != "l1" || !h.mail.gotSetLabelAdd {
			t.Fatalf("got threadID=%q labelID=%q add=%v", h.mail.gotSetLabelID, h.mail.gotSetLabelLabelID, h.mail.gotSetLabelAdd)
		}
	})

	t.Run("missing labelId rejected", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/mail/threads/th1/labels", jsonBody(t, map[string]any{"add": true}))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "validation_failed" {
			t.Fatalf("code = %q, want validation_failed", got.Code)
		}
	})

	t.Run("service not found propagates", func(t *testing.T) {
		h := newHarness(t)
		h.mail.setLabelErr = domain.ErrNotFound
		rec := h.authed(http.MethodPost, "/v1/mail/threads/th1/labels", jsonBody(t, map[string]any{"labelId": "l1", "add": false}))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
		}
	})
}

func TestHandleBulkThreadActionsLabelDispatch(t *testing.T) {
	t.Run("label", func(t *testing.T) {
		h := newHarness(t)
		h.mail.bulkSetLabelRet = port.BulkActionResult{Threads: []domain.Thread{{ID: "t1"}}, FailedIDs: []string{}}
		rec := h.authed(http.MethodPost, "/v1/mail/threads/bulk-actions",
			jsonBody(t, map[string]any{"threadIds": []string{"t1"}, "action": "label", "labelId": "l1"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.mail.gotBulkSetLabelLabelID != "l1" || !h.mail.gotBulkSetLabelAdd {
			t.Fatalf("labelId=%q add=%v", h.mail.gotBulkSetLabelLabelID, h.mail.gotBulkSetLabelAdd)
		}
		if len(h.mail.gotBulkSetLabelIDs) != 1 || h.mail.gotBulkSetLabelIDs[0] != "t1" {
			t.Fatalf("gotBulkSetLabelIDs = %v", h.mail.gotBulkSetLabelIDs)
		}
	})

	t.Run("unlabel", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/mail/threads/bulk-actions",
			jsonBody(t, map[string]any{"threadIds": []string{"t1"}, "action": "unlabel", "labelId": "l1"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.mail.gotBulkSetLabelAdd {
			t.Fatalf("gotBulkSetLabelAdd = true, want false for unlabel")
		}
	})

	t.Run("missing labelId rejected", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/mail/threads/bulk-actions",
			jsonBody(t, map[string]any{"threadIds": []string{"t1"}, "action": "label"}))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "validation_failed" {
			t.Fatalf("code = %q, want validation_failed", got.Code)
		}
	})
}

// --- M2.5: opens, smart send, attachments, contacts, reactions -----------

func TestHandleListOpens(t *testing.T) {
	t.Run("success with cursor and limit", func(t *testing.T) {
		h := newHarness(t)
		next := "cur2"
		h.mail.listOpensRet = domain.Page[domain.OpenEvent]{
			Items:      []domain.OpenEvent{{MessageID: "m1", ThreadID: "th1"}},
			NextCursor: &next,
		}
		rec := h.authed(http.MethodGet, "/v1/mail/opens?cursor=cur1&limit=10", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.mail.gotListOpensUser != defaultUserID || h.mail.gotListOpensCur != "cur1" || h.mail.gotListOpensLim != 10 {
			t.Fatalf("got user=%q cursor=%q limit=%d", h.mail.gotListOpensUser, h.mail.gotListOpensCur, h.mail.gotListOpensLim)
		}
		var got domain.Page[domain.OpenEvent]
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got.Items) != 1 || got.Items[0].MessageID != "m1" {
			t.Fatalf("items = %+v", got.Items)
		}
	})

	t.Run("no params leaves zero values", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodGet, "/v1/mail/opens", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.mail.gotListOpensCur != "" || h.mail.gotListOpensLim != 0 {
			t.Fatalf("cursor=%q limit=%d, want zero values", h.mail.gotListOpensCur, h.mail.gotListOpensLim)
		}
	})

	t.Run("non-numeric limit rejected", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodGet, "/v1/mail/opens?limit=abc", nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "validation_failed" {
			t.Fatalf("code = %q, want validation_failed", got.Code)
		}
	})

	t.Run("service error forwarded", func(t *testing.T) {
		h := newHarness(t)
		h.mail.listOpensErr = domain.ErrPaymentRequired
		rec := h.authed(http.MethodGet, "/v1/mail/opens", nil)
		if rec.Code != http.StatusPaymentRequired {
			t.Fatalf("status = %d, want 402 (body=%s)", rec.Code, rec.Body.String())
		}
	})
}

func TestHandleSendSuggestion(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h := newHarness(t)
		h.mail.suggestSendRet = domain.SendSuggestion{Email: "a@b.com", Confidence: 0.8, SampleSize: 7}
		rec := h.authed(http.MethodGet, "/v1/mail/send-suggestion?email=a@b.com", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.mail.gotSuggestSendUser != defaultUserID || h.mail.gotSuggestSendMail != "a@b.com" {
			t.Fatalf("got user=%q email=%q", h.mail.gotSuggestSendUser, h.mail.gotSuggestSendMail)
		}
		var got domain.SendSuggestion
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Email != "a@b.com" || got.SampleSize != 7 {
			t.Fatalf("got = %+v", got)
		}
	})

	t.Run("missing email rejected", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodGet, "/v1/mail/send-suggestion", nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "validation_failed" {
			t.Fatalf("code = %q, want validation_failed", got.Code)
		}
		if h.mail.gotSuggestSendMail != "" {
			t.Fatalf("SuggestSendTime called unexpectedly")
		}
	})

	t.Run("thin history maps to 404, not an error toast", func(t *testing.T) {
		h := newHarness(t)
		h.mail.suggestSendErr = domain.ErrNotFound
		rec := h.authed(http.MethodGet, "/v1/mail/send-suggestion?email=a@b.com", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "not_found" {
			t.Fatalf("code = %q, want not_found", got.Code)
		}
	})
}

func TestHandleSearchAttachments(t *testing.T) {
	t.Run("all filters parse", func(t *testing.T) {
		h := newHarness(t)
		h.mail.searchAttachmentsRet = domain.Page[domain.AttachmentHit]{Items: []domain.AttachmentHit{{MessageID: "m1"}}}
		rec := h.authed(http.MethodGet, "/v1/mail/attachments?q=invoice&contact=a@b.com&threadId=th1&cursor=cur1&limit=5", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		q := h.mail.gotSearchAttachmentsQ
		if q.Query != "invoice" || q.Contact != "a@b.com" || q.ThreadID != "th1" || q.Cursor != "cur1" || q.Limit != 5 {
			t.Fatalf("q = %+v", q)
		}
		if h.mail.gotSearchAttachmentsUser != defaultUserID {
			t.Fatalf("gotSearchAttachmentsUser = %q, want %q", h.mail.gotSearchAttachmentsUser, defaultUserID)
		}
	})

	t.Run("no params leaves zero-valued query", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodGet, "/v1/mail/attachments", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if q := h.mail.gotSearchAttachmentsQ; q.Query != "" || q.Contact != "" || q.ThreadID != "" || q.Cursor != "" || q.Limit != 0 {
			t.Fatalf("q = %+v, want zero-valued", q)
		}
	})

	t.Run("non-numeric limit rejected", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodGet, "/v1/mail/attachments?limit=abc", nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "validation_failed" {
			t.Fatalf("code = %q, want validation_failed", got.Code)
		}
	})
}

func TestHandleGetAttachmentContent(t *testing.T) {
	t.Run("streams raw bytes with content headers", func(t *testing.T) {
		h := newHarness(t)
		h.mail.getAttachmentContentData = []byte("%PDF-1.4 fake pdf bytes")
		h.mail.getAttachmentContentMimeType = "application/pdf"
		h.mail.getAttachmentContentFilename = "invoice.pdf"
		rec := h.authed(http.MethodGet, "/v1/mail/attachments/att1/content", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/pdf" {
			t.Fatalf("Content-Type = %q, want application/pdf", ct)
		}
		if cd := rec.Header().Get("Content-Disposition"); cd != `inline; filename="invoice.pdf"` {
			t.Fatalf("Content-Disposition = %q, want inline; filename=\"invoice.pdf\"", cd)
		}
		// M2.5 review fix (MINOR e): this endpoint serves arbitrary
		// user-supplied attachment content — nosniff stops the browser from
		// MIME-sniffing it into something more dangerous than the reported
		// Content-Type.
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
		}
		if rec.Body.String() != "%PDF-1.4 fake pdf bytes" {
			t.Fatalf("body = %q, want raw bytes (no JSON envelope)", rec.Body.String())
		}
		if h.mail.gotGetAttachmentContentUser != defaultUserID || h.mail.gotGetAttachmentContentID != "att1" {
			t.Fatalf("got user=%q id=%q", h.mail.gotGetAttachmentContentUser, h.mail.gotGetAttachmentContentID)
		}
	})

	t.Run("not found", func(t *testing.T) {
		h := newHarness(t)
		h.mail.getAttachmentContentErr = domain.ErrNotFound
		rec := h.authed(http.MethodGet, "/v1/mail/attachments/att1/content", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
		}
	})
}

func TestHandleGetContact(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h := newHarness(t)
		h.mail.getContactRet = domain.ContactSummary{Email: "a@b.com", ThreadCount: 3}
		rec := h.authed(http.MethodGet, "/v1/mail/contacts/a@b.com", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.mail.gotGetContactUser != defaultUserID || h.mail.gotGetContactMail != "a@b.com" {
			t.Fatalf("got user=%q email=%q", h.mail.gotGetContactUser, h.mail.gotGetContactMail)
		}
		var got domain.ContactSummary
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Email != "a@b.com" || got.ThreadCount != 3 {
			t.Fatalf("got = %+v", got)
		}
	})

	t.Run("not found", func(t *testing.T) {
		h := newHarness(t)
		h.mail.getContactErr = domain.ErrNotFound
		rec := h.authed(http.MethodGet, "/v1/mail/contacts/a@b.com", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
		}
	})
}

func TestHandleReactToMessage(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h := newHarness(t)
		draftID := "d1"
		h.mail.reactRet = port.ReactionResult{Reaction: domain.Reaction{ID: "r1", Emoji: "👍"}, DraftID: &draftID}
		rec := h.authed(http.MethodPost, "/v1/mail/messages/m1/reactions", jsonBody(t, map[string]any{"emoji": "👍", "sendReply": true}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.mail.gotReactUser != defaultUserID || h.mail.gotReactMessageID != "m1" || h.mail.gotReactEmoji != "👍" || !h.mail.gotReactSendReply {
			t.Fatalf("got user=%q messageID=%q emoji=%q sendReply=%v",
				h.mail.gotReactUser, h.mail.gotReactMessageID, h.mail.gotReactEmoji, h.mail.gotReactSendReply)
		}
		var got port.ReactionResult
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Reaction.ID != "r1" || got.DraftID == nil || *got.DraftID != "d1" {
			t.Fatalf("got = %+v", got)
		}
	})

	t.Run("missing emoji rejected", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/mail/messages/m1/reactions", jsonBody(t, map[string]any{"sendReply": false}))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "validation_failed" {
			t.Fatalf("code = %q, want validation_failed", got.Code)
		}
		if h.mail.gotReactMessageID != "" {
			t.Fatalf("ReactToMessage called unexpectedly")
		}
	})

	t.Run("malformed JSON rejected", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/mail/messages/m1/reactions", strings.NewReader("{"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("service error forwarded", func(t *testing.T) {
		h := newHarness(t)
		h.mail.reactErr = domain.ErrNotFound
		rec := h.authed(http.MethodPost, "/v1/mail/messages/m1/reactions", jsonBody(t, map[string]any{"emoji": "👍"}))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
		}
	})
}

func TestHandleRemoveReaction(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodDelete, "/v1/mail/messages/m1/reactions/%F0%9F%91%8D", nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.mail.gotRemoveReactionUser != defaultUserID || h.mail.gotRemoveReactionMessageID != "m1" || h.mail.gotRemoveReactionEmoji != "👍" {
			t.Fatalf("got user=%q messageID=%q emoji=%q (want URL-decoded emoji)",
				h.mail.gotRemoveReactionUser, h.mail.gotRemoveReactionMessageID, h.mail.gotRemoveReactionEmoji)
		}
	})

	t.Run("not found", func(t *testing.T) {
		h := newHarness(t)
		h.mail.removeReactionErr = domain.ErrNotFound
		rec := h.authed(http.MethodDelete, "/v1/mail/messages/m1/reactions/%F0%9F%91%8D", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
		}
	})
}
