package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- CollabService double: comment-side methods (the struct itself lives
// --- in threadshare_handlers_test.go, holding both tasks' fields) -----------

func (f *fakeCollabService) ListComments(_ context.Context, userID, threadID, teamID string) ([]domain.Comment, error) {
	f.gotList.userID, f.gotList.threadID, f.gotList.teamID = userID, threadID, teamID
	return f.commentListRet, f.commentListErr
}

func (f *fakeCollabService) AddComment(_ context.Context, userID, threadID string, in port.CommentInput) (domain.Comment, error) {
	f.gotAddUser, f.gotThreadID, f.gotAddInput = userID, threadID, in
	return f.addRet, f.addErr
}

func (f *fakeCollabService) UpdateComment(_ context.Context, userID, commentID, body string) (domain.Comment, error) {
	f.gotUpdateID, f.gotUpdateStr = commentID, body
	return f.updateRet, f.updateErr
}

func (f *fakeCollabService) DeleteComment(_ context.Context, userID, commentID string) error {
	f.gotDelete.userID, f.gotDelete.commentID = userID, commentID
	return f.deleteErr
}

func newCollabHarness(t *testing.T) (*harness, *fakeCollabService) {
	t.Helper()
	h := newHarness(t)
	f := &fakeCollabService{}
	h.deps.Collab = f
	return h, f
}

// --- tests -------------------------------------------------------------------

func TestCommentRoutesRequireAuth(t *testing.T) {
	h, _ := newCollabHarness(t)
	for _, rt := range []struct{ method, target string }{
		{http.MethodGet, "/v1/mail/threads/t1/comments?teamId=team1"},
		{http.MethodPost, "/v1/mail/threads/t1/comments"},
		{http.MethodPatch, "/v1/comments/c1"},
		{http.MethodDelete, "/v1/comments/c1"},
	} {
		if rec := h.anon(rt.method, rt.target, nil); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s anon = %d, want 401", rt.method, rt.target, rec.Code)
		}
	}
}

func TestCommentRoutes501WhenCollabUnwired(t *testing.T) {
	h := newHarness(t) // Collab left nil
	if rec := h.authed(http.MethodGet, "/v1/mail/threads/t1/comments?teamId=team1", nil); rec.Code != http.StatusNotImplemented {
		t.Fatalf("list with nil Collab = %d, want 501", rec.Code)
	}
}

func TestListCommentsHandler(t *testing.T) {
	h, f := newCollabHarness(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	f.commentListRet = []domain.Comment{{
		ID: "c1", ThreadID: "t1", TeamID: "team1", AuthorID: "user_1",
		Body: "hello", Mentions: []string{}, CreatedAt: now, UpdatedAt: now,
	}}

	rec := h.authed(http.MethodGet, "/v1/mail/threads/t1/comments?teamId=team1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body %s", rec.Code, rec.Body)
	}
	if f.gotList.userID != defaultUserID || f.gotList.threadID != "t1" || f.gotList.teamID != "team1" {
		t.Fatalf("service got %+v", f.gotList)
	}
	var body struct {
		Comments []domain.Comment `json:"comments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Comments) != 1 || body.Comments[0].ID != "c1" || body.Comments[0].Body != "hello" {
		t.Fatalf("body = %+v", body)
	}
}

func TestListCommentsRequiresTeamID(t *testing.T) {
	h, _ := newCollabHarness(t)
	if rec := h.authed(http.MethodGet, "/v1/mail/threads/t1/comments", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing teamId = %d, want 400", rec.Code)
	}
}

func TestAddCommentHandler(t *testing.T) {
	h, f := newCollabHarness(t)
	f.addRet = domain.Comment{ID: "c9", ThreadID: "t1", TeamID: "team1", AuthorID: defaultUserID, Body: "hi @ada@example.com"}

	rec := h.authed(http.MethodPost, "/v1/mail/threads/t1/comments",
		strings.NewReader(`{"teamId":"team1","body":"hi @ada@example.com"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body %s", rec.Code, rec.Body)
	}
	if f.gotAddUser != defaultUserID || f.gotThreadID != "t1" {
		t.Fatalf("service got user %q thread %q", f.gotAddUser, f.gotThreadID)
	}
	if f.gotAddInput.TeamID != "team1" || f.gotAddInput.Body != "hi @ada@example.com" {
		t.Fatalf("input = %+v", f.gotAddInput)
	}
	var c domain.Comment
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil || c.ID != "c9" {
		t.Fatalf("response = %s (%v)", rec.Body, err)
	}
}

func TestAddCommentMalformedJSON(t *testing.T) {
	h, _ := newCollabHarness(t)
	if rec := h.authed(http.MethodPost, "/v1/mail/threads/t1/comments", strings.NewReader(`{`)); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed body = %d, want 400", rec.Code)
	}
}

func TestCommentErrorMapping(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"non-member 404", domain.ErrNotFound, http.StatusNotFound},
		{"forbidden 403", domain.ErrForbidden, http.StatusForbidden},
		{"validation 400", domain.ErrValidation, http.StatusBadRequest},
		{"payment 402", domain.ErrPaymentRequired, http.StatusPaymentRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, f := newCollabHarness(t)
			f.addErr, f.commentListErr, f.updateErr, f.deleteErr = tt.err, tt.err, tt.err, tt.err
			if rec := h.authed(http.MethodPost, "/v1/mail/threads/t1/comments", strings.NewReader(`{"teamId":"x","body":"b"}`)); rec.Code != tt.want {
				t.Fatalf("add = %d, want %d", rec.Code, tt.want)
			}
			if rec := h.authed(http.MethodGet, "/v1/mail/threads/t1/comments?teamId=x", nil); rec.Code != tt.want {
				t.Fatalf("list = %d, want %d", rec.Code, tt.want)
			}
			if rec := h.authed(http.MethodPatch, "/v1/comments/c1", strings.NewReader(`{"body":"b"}`)); rec.Code != tt.want {
				t.Fatalf("update = %d, want %d", rec.Code, tt.want)
			}
			if rec := h.authed(http.MethodDelete, "/v1/comments/c1", nil); rec.Code != tt.want {
				t.Fatalf("delete = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

func TestUpdateCommentHandler(t *testing.T) {
	h, f := newCollabHarness(t)
	f.updateRet = domain.Comment{ID: "c1", Body: "edited"}

	rec := h.authed(http.MethodPatch, "/v1/comments/c1", strings.NewReader(`{"body":"edited"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body %s", rec.Code, rec.Body)
	}
	if f.gotUpdateID != "c1" || f.gotUpdateStr != "edited" {
		t.Fatalf("service got id %q body %q", f.gotUpdateID, f.gotUpdateStr)
	}
}

func TestDeleteCommentHandler(t *testing.T) {
	h, f := newCollabHarness(t)
	rec := h.authed(http.MethodDelete, "/v1/comments/c1", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d body %s", rec.Code, rec.Body)
	}
	if f.gotDelete.userID != defaultUserID || f.gotDelete.commentID != "c1" {
		t.Fatalf("service got %+v", f.gotDelete)
	}
}
