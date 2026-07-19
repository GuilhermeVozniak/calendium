package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- fakeCollabService (Task 7: kept out of harness_test.go so parallel
// --- feature branches don't collide on the shared fixture file) --------------

type fakeCollabService struct {
	shareRet       domain.ThreadShare
	shareToken     string
	shareErr       error
	gotShareUser   string
	gotShareThread string
	gotShareIn     port.ShareThreadInput

	listRet       []domain.ThreadShare
	listErr       error
	gotListThread string

	revokeErr       error
	gotRevokeThread string
	gotRevokeShare  string

	viewRet       port.SharedThreadView
	viewErr       error
	viewCalls     int
	gotViewToken  string
	gotViewViewer *string

	resolveID        string
	resolveErr       error
	gotResolveToken  string
	gotResolveViewer *string
}

var _ port.CollabService = (*fakeCollabService)(nil)

func (f *fakeCollabService) ShareThread(_ context.Context, userID, threadID string, in port.ShareThreadInput) (domain.ThreadShare, string, error) {
	f.gotShareUser, f.gotShareThread, f.gotShareIn = userID, threadID, in
	return f.shareRet, f.shareToken, f.shareErr
}
func (f *fakeCollabService) ListThreadShares(_ context.Context, _, threadID string) ([]domain.ThreadShare, error) {
	f.gotListThread = threadID
	return f.listRet, f.listErr
}
func (f *fakeCollabService) RevokeThreadShare(_ context.Context, _, threadID, shareID string) error {
	f.gotRevokeThread, f.gotRevokeShare = threadID, shareID
	return f.revokeErr
}
func (f *fakeCollabService) GetSharedThread(_ context.Context, rawToken string, viewer *string) (port.SharedThreadView, error) {
	f.viewCalls++
	f.gotViewToken, f.gotViewViewer = rawToken, viewer
	return f.viewRet, f.viewErr
}
func (f *fakeCollabService) ResolveShare(_ context.Context, rawToken string, viewer *string) (string, error) {
	f.gotResolveToken, f.gotResolveViewer = rawToken, viewer
	return f.resolveID, f.resolveErr
}

func collabHarness(t *testing.T) (*harness, *fakeCollabService) {
	t.Helper()
	h := newHarness(t)
	f := &fakeCollabService{}
	h.deps.Collab = f
	return h, f
}

// --- owner-side share management ---------------------------------------------

func TestShareThreadHandler(t *testing.T) {
	h, f := collabHarness(t)
	exp := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	teamID := "team1"
	f.shareRet = domain.ThreadShare{ID: "sh1", ThreadID: "t1", CreatedBy: defaultUserID,
		Audience: domain.ShareAudienceTeam, TeamID: &teamID, TokenHash: "super-secret-hash"}
	f.shareToken = "raw-token-once"

	rec := h.authed("POST", "/v1/mail/threads/t1/share",
		strings.NewReader(`{"audience":"team","teamId":"team1","expiresAt":"2026-08-01T00:00:00Z"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", rec.Code, rec.Body.String())
	}
	if f.gotShareUser != defaultUserID || f.gotShareThread != "t1" {
		t.Fatalf("service got user=%q thread=%q", f.gotShareUser, f.gotShareThread)
	}
	if f.gotShareIn.Audience != domain.ShareAudienceTeam || f.gotShareIn.TeamID != "team1" ||
		f.gotShareIn.ExpiresAt == nil || !f.gotShareIn.ExpiresAt.Equal(exp) {
		t.Fatalf("service got input %+v", f.gotShareIn)
	}

	var body struct {
		Share domain.ThreadShare `json:"share"`
		Token string             `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if body.Token != "raw-token-once" || body.Share.ID != "sh1" {
		t.Fatalf("body = %+v", body)
	}
	// The stored hash must never serialize.
	if strings.Contains(rec.Body.String(), "super-secret-hash") || strings.Contains(strings.ToLower(rec.Body.String()), "tokenhash") {
		t.Fatalf("token hash leaked: %s", rec.Body.String())
	}
}

func TestShareThreadHandlerRejectsBadJSONAndAnon(t *testing.T) {
	h, _ := collabHarness(t)
	if rec := h.authed("POST", "/v1/mail/threads/t1/share", strings.NewReader("{nope")); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad JSON status = %d, want 400", rec.Code)
	}
	if rec := h.anon("POST", "/v1/mail/threads/t1/share", strings.NewReader(`{"audience":"external"}`)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon status = %d, want 401", rec.Code)
	}
}

func TestListThreadSharesHandler(t *testing.T) {
	h, f := collabHarness(t)
	f.listRet = []domain.ThreadShare{{ID: "sh1", ThreadID: "t1", Audience: domain.ShareAudienceExternal}}

	rec := h.authed("GET", "/v1/mail/threads/t1/shares", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if f.gotListThread != "t1" {
		t.Fatalf("service got thread %q", f.gotListThread)
	}
	var shares []domain.ThreadShare
	if err := json.Unmarshal(rec.Body.Bytes(), &shares); err != nil || len(shares) != 1 || shares[0].ID != "sh1" {
		t.Fatalf("body = %s (err=%v)", rec.Body.String(), err)
	}
}

func TestRevokeThreadShareHandler(t *testing.T) {
	h, f := collabHarness(t)
	rec := h.authed("DELETE", "/v1/mail/threads/t1/shares/sh1", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if f.gotRevokeThread != "t1" || f.gotRevokeShare != "sh1" {
		t.Fatalf("service got thread=%q share=%q", f.gotRevokeThread, f.gotRevokeShare)
	}

	f.revokeErr = domain.ErrNotFound
	rec = h.authed("DELETE", "/v1/mail/threads/t1/shares/ghost", nil)
	if rec.Code != http.StatusNotFound || decodeErr(t, rec).Code != "not_found" {
		t.Fatalf("status = %d body = %s, want 404 not_found", rec.Code, rec.Body.String())
	}
}

// --- public share view --------------------------------------------------------

func TestGetSharedThreadHandlerAnonymous(t *testing.T) {
	h, f := collabHarness(t)
	f.viewRet = port.SharedThreadView{Subject: "Launch plan", Audience: domain.ShareAudienceExternal, Messages: []domain.Message{}}

	// No Authorization header: the route must work unauthenticated.
	rec := h.anon("GET", "/v1/shared/threads/tok123", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if f.gotViewToken != "tok123" || f.gotViewViewer != nil {
		t.Fatalf("service got token=%q viewer=%v, want tok123/nil", f.gotViewToken, f.gotViewViewer)
	}
	var view port.SharedThreadView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil || view.Subject != "Launch plan" {
		t.Fatalf("body = %s (err=%v)", rec.Body.String(), err)
	}
}

func TestGetSharedThreadHandlerForwardsBearerViewer(t *testing.T) {
	h, f := collabHarness(t)
	rec := h.authed("GET", "/v1/shared/threads/tok123", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if f.gotViewViewer == nil || *f.gotViewViewer != defaultUserID {
		t.Fatalf("viewer = %v, want %q", f.gotViewViewer, defaultUserID)
	}
}

func TestGetSharedThreadHandlerInvalidBearerFailsClosed(t *testing.T) {
	h, f := collabHarness(t)
	req := httptest.NewRequest("GET", "/v1/shared/threads/tok123", nil)
	req.Header.Set("Authorization", "Bearer bogus-token")
	rec := httptest.NewRecorder()
	h.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound || decodeErr(t, rec).Code != "not_found" {
		t.Fatalf("status = %d body = %s, want uniform 404", rec.Code, rec.Body.String())
	}
	if f.viewCalls != 0 {
		t.Fatal("service reached despite invalid bearer")
	}
}

func TestGetSharedThreadHandlerUnknownToken404(t *testing.T) {
	h, f := collabHarness(t)
	f.viewErr = domain.ErrNotFound
	rec := h.anon("GET", "/v1/shared/threads/nope", nil)
	if rec.Code != http.StatusNotFound || decodeErr(t, rec).Code != "not_found" {
		t.Fatalf("status = %d body = %s, want 404 not_found", rec.Code, rec.Body.String())
	}
}

// --- public share stream ------------------------------------------------------

func TestSharedThreadStreamDeliversShareEvents(t *testing.T) {
	h, f := collabHarness(t)
	f.resolveID = "sh1"
	srv := httptest.NewServer(h.handler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/shared/threads/tok123/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	if f.gotResolveToken != "tok123" || f.gotResolveViewer != nil {
		t.Fatalf("resolve got token=%q viewer=%v", f.gotResolveToken, f.gotResolveViewer)
	}

	waitFor(t, "subscription", func() bool { return h.events.activeSubscribers() == 1 })
	want := [][]string{{"share:sh1"}}
	if topics := h.events.subscribedTopics(); len(topics) != 1 || topics[0][0] != "share:sh1" {
		t.Fatalf("subscribed topics = %v, want %v", topics, want)
	}

	h.events.Publish(port.CollabEvent{Topic: "share:sh1", Type: "share.updated"})

	reader := bufio.NewReader(resp.Body)
	var sawEvent bool
	for !sawEvent {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("stream ended early: %v", err)
		}
		if strings.HasPrefix(line, "event: share.updated") {
			sawEvent = true
		}
	}
}

func TestSharedThreadStreamFailsClosedOnBadToken(t *testing.T) {
	h, f := collabHarness(t)
	f.resolveErr = domain.ErrNotFound

	rec := h.anon("GET", "/v1/shared/threads/revoked/stream", nil)
	if rec.Code != http.StatusNotFound || decodeErr(t, rec).Code != "not_found" {
		t.Fatalf("status = %d body = %s, want uniform 404", rec.Code, rec.Body.String())
	}
	if h.events.activeSubscribers() != 0 {
		t.Fatal("subscribed before authorization")
	}
}
