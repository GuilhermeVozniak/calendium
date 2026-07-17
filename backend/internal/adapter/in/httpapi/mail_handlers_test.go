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
