package httpapi

// Handler tests for GET /v1/mail/threads/{id}/team-activity (M2.7 Task 10).

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

type fakeTeamActivityService struct {
	rows        []domain.TeamThreadActivity
	err         error
	gotUserID   string
	gotThreadID string
}

func (f *fakeTeamActivityService) TeamThreadActivity(_ context.Context, userID, threadID string) ([]domain.TeamThreadActivity, error) {
	f.gotUserID, f.gotThreadID = userID, threadID
	return f.rows, f.err
}

var _ port.TeamActivityService = (*fakeTeamActivityService)(nil)

func TestTeamActivityRequiresAuth(t *testing.T) {
	h := newHarness(t)
	h.deps.TeamActivity = &fakeTeamActivityService{}
	rec := h.anon(http.MethodGet, "/v1/mail/threads/t1/team-activity", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestTeamActivityNotWiredAnswers501(t *testing.T) {
	h := newHarness(t) // TeamActivity left nil
	rec := h.authed(http.MethodGet, "/v1/mail/threads/t1/team-activity", nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 when the service is not wired", rec.Code)
	}
	if got := decodeErr(t, rec).Code; got != "not_implemented" {
		t.Fatalf("error code = %q, want not_implemented", got)
	}
}

func TestTeamActivityReturnsRows(t *testing.T) {
	opened := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	replied := opened.Add(3 * time.Minute)
	fake := &fakeTeamActivityService{rows: []domain.TeamThreadActivity{
		{TeamID: "team1", UserID: "mate", ConversationKey: "<k@x>", OpenedAt: &opened, RepliedAt: &replied},
	}}
	h := newHarness(t)
	h.deps.TeamActivity = fake

	rec := h.authed(http.MethodGet, "/v1/mail/threads/t42/team-activity", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if fake.gotUserID != defaultUserID || fake.gotThreadID != "t42" {
		t.Fatalf("service called with (%q, %q), want (%q, t42)", fake.gotUserID, fake.gotThreadID, defaultUserID)
	}
	var rows []domain.TeamThreadActivity
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if len(rows) != 1 || rows[0].UserID != "mate" || rows[0].TeamID != "team1" {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].OpenedAt == nil || rows[0].RepliedAt == nil {
		t.Fatalf("timestamps lost in serialization: %+v", rows[0])
	}
}

func TestTeamActivityMapsServiceErrors(t *testing.T) {
	h := newHarness(t)
	h.deps.TeamActivity = &fakeTeamActivityService{err: domain.ErrNotFound}
	rec := h.authed(http.MethodGet, "/v1/mail/threads/ghost/team-activity", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := decodeErr(t, rec).Code; got != "not_found" {
		t.Fatalf("error code = %q, want not_found", got)
	}
}
