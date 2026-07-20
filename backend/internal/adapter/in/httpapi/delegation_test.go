package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

var _ port.DelegationService = (*fakeDelegationService)(nil)

type fakeDelegationService struct {
	createRet          domain.Delegation
	createErr          error
	gotCreatePrincipal string
	gotCreateEmail     string
	gotCreateScopes    []domain.DelegationScope

	listAsPrincipal []domain.Delegation
	listAsAssistant []domain.Delegation
	listErr         error

	acceptRet     domain.Delegation
	acceptErr     error
	gotAcceptUser string
	gotAcceptID   string

	revokeErr     error
	gotRevokeUser string
	gotRevokeID   string

	authorizeErr     error
	authorizeCalls   int
	gotAuthAssistant string
	gotAuthPrincipal string
	gotAuthScope     domain.DelegationScope

	recorded  []domain.AuditEntry
	recordErr error

	auditRet      []domain.AuditEntry
	auditErr      error
	gotAuditUser  string
	gotAuditLimit int
}

func (f *fakeDelegationService) Create(_ context.Context, principalID, assistantEmail string, scopes []domain.DelegationScope) (domain.Delegation, error) {
	f.gotCreatePrincipal, f.gotCreateEmail, f.gotCreateScopes = principalID, assistantEmail, scopes
	return f.createRet, f.createErr
}
func (f *fakeDelegationService) List(_ context.Context, userID string) ([]domain.Delegation, []domain.Delegation, error) {
	return f.listAsPrincipal, f.listAsAssistant, f.listErr
}
func (f *fakeDelegationService) Accept(_ context.Context, assistantID, delegationID string) (domain.Delegation, error) {
	f.gotAcceptUser, f.gotAcceptID = assistantID, delegationID
	return f.acceptRet, f.acceptErr
}
func (f *fakeDelegationService) Revoke(_ context.Context, userID, delegationID string) error {
	f.gotRevokeUser, f.gotRevokeID = userID, delegationID
	return f.revokeErr
}
func (f *fakeDelegationService) Authorize(_ context.Context, assistantID, principalID string, scope domain.DelegationScope) error {
	f.authorizeCalls++
	f.gotAuthAssistant, f.gotAuthPrincipal, f.gotAuthScope = assistantID, principalID, scope
	return f.authorizeErr
}
func (f *fakeDelegationService) RecordAudit(_ context.Context, e domain.AuditEntry) error {
	if f.recordErr != nil {
		return f.recordErr
	}
	f.recorded = append(f.recorded, e)
	return nil
}
func (f *fakeDelegationService) Audit(_ context.Context, principalID string, limit int) ([]domain.AuditEntry, error) {
	f.gotAuditUser, f.gotAuditLimit = principalID, limit
	return f.auditRet, f.auditErr
}

// delegHarness wires a fakeDelegationService into the standard harness. The
// verifier resolves defaultToken to defaultUserID (the assistant); the
// principal is resolved through the fake user service.
func delegHarness(t *testing.T) (*harness, *fakeDelegationService) {
	t.Helper()
	h := newHarness(t)
	fd := &fakeDelegationService{}
	h.deps.Delegations = fd
	h.users.getRet = domain.User{ID: "principal_1", Email: "principal@example.com"}
	return h, fd
}

// actAs issues a request with a valid bearer token plus the act-as header.
func actAs(h *harness, principalID, method, target string, body io.Reader) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("Authorization", "Bearer "+defaultToken)
	req.Header.Set(actAsHeader, principalID)
	rec := httptest.NewRecorder()
	h.handler().ServeHTTP(rec, req)
	return rec
}

// --- scope mapping (fail closed) ---------------------------------------------

func TestDelegationScopeForRoute(t *testing.T) {
	tests := []struct {
		method, path string
		wantScope    domain.DelegationScope
		wantResource string
		wantOK       bool
	}{
		{http.MethodGet, "/v1/mail/threads", domain.ScopeMailRead, "mail", true},
		{http.MethodGet, "/v1/mail/drafts/d1", domain.ScopeMailRead, "mail", true},
		{http.MethodGet, "/v1/search", domain.ScopeMailRead, "mail", true},
		{http.MethodPost, "/v1/mail/threads/t1/actions", domain.ScopeMailWrite, "mail", true},
		{http.MethodDelete, "/v1/mail/drafts/d1", domain.ScopeMailWrite, "mail", true},
		{http.MethodGet, "/v1/calendars", domain.ScopeCalendarRead, "calendar", true},
		{http.MethodGet, "/v1/events", domain.ScopeCalendarRead, "event", true},
		{http.MethodGet, "/v1/availability", domain.ScopeCalendarRead, "calendar", true},
		{http.MethodPatch, "/v1/calendars/c1", domain.ScopeCalendarWrite, "calendar", true},
		{http.MethodPost, "/v1/events", domain.ScopeCalendarWrite, "event", true},
		{http.MethodDelete, "/v1/events/e1", domain.ScopeCalendarWrite, "event", true},
		// Never delegable — explicit rejections per the brief.
		{http.MethodPost, "/v1/teams", "", "", false},
		{http.MethodGet, "/v1/billing/subscription", "", "", false},
		{http.MethodGet, "/v1/accounts", "", "", false},
		{http.MethodPost, "/v1/devices", "", "", false},
		{http.MethodGet, "/v1/delegations", "", "", false},
		{http.MethodDelete, "/v1/delegations/d1", "", "", false},
		// Collaboration sub-surfaces under delegable prefixes are denied
		// (I1 fix): shares, comments, team activity, snippets, calendar
		// share management.
		{http.MethodPost, "/v1/mail/threads/t1/share", "", "", false},
		{http.MethodGet, "/v1/mail/threads/t1/shares", "", "", false},
		{http.MethodDelete, "/v1/mail/threads/t1/shares/sh1", "", "", false},
		{http.MethodGet, "/v1/mail/threads/t1/comments", "", "", false},
		{http.MethodPost, "/v1/mail/threads/t1/comments", "", "", false},
		{http.MethodGet, "/v1/mail/threads/t1/team-activity", "", "", false},
		{http.MethodGet, "/v1/mail/snippets", "", "", false},
		{http.MethodPost, "/v1/mail/snippets", "", "", false},
		{http.MethodPut, "/v1/mail/snippets/sn1", "", "", false},
		{http.MethodDelete, "/v1/mail/snippets/sn1", "", "", false},
		{http.MethodGet, "/v1/calendars/c1/shares", "", "", false},
		{http.MethodPost, "/v1/calendars/c1/shares", "", "", false},
		{http.MethodPatch, "/v1/calendars/c1/shares/sh1", "", "", false},
		{http.MethodDelete, "/v1/calendars/c1/shares/sh1", "", "", false},
		{http.MethodPatch, "/v1/comments/cm1", "", "", false},
		{http.MethodDelete, "/v1/comments/cm1", "", "", false},
		// Event notes are private commentary (M2.8 Task 4): never delegable.
		{http.MethodGet, "/v1/events/e1/note", "", "", false},
		{http.MethodPut, "/v1/events/e1/note", "", "", false},
		// But sibling thread sub-routes stay delegable.
		{http.MethodPost, "/v1/mail/threads/t1/snooze", domain.ScopeMailWrite, "mail", true},
		// Fail closed on everything else.
		{http.MethodPost, "/v1/ai/compose", "", "", false},
		{http.MethodGet, "/v1/me", "", "", false},
		{http.MethodGet, "/v1/collab/stream", "", "", false},
		{http.MethodPost, "/v1/search", "", "", false},
		{http.MethodGet, "/v1/mailfoo", "", "", false},
	}
	for _, tc := range tests {
		scope, resource, ok := delegationScopeForRoute(tc.method, tc.path)
		if ok != tc.wantOK || scope != tc.wantScope || resource != tc.wantResource {
			t.Errorf("delegationScopeForRoute(%s %s) = (%q, %q, %v), want (%q, %q, %v)",
				tc.method, tc.path, scope, resource, ok, tc.wantScope, tc.wantResource, tc.wantOK)
		}
	}
}

// --- Step 2: middleware behavior ---------------------------------------------

func TestActAsValidGrantReachesHandlerAsPrincipal(t *testing.T) {
	h, fd := delegHarness(t)

	rec := actAs(h, "principal_1", http.MethodGet, "/v1/mail/threads", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if h.mail.gotListUserID != "principal_1" {
		t.Fatalf("handler saw userID %q, want the principal's id", h.mail.gotListUserID)
	}
	if fd.authorizeCalls != 1 || fd.gotAuthAssistant != defaultUserID ||
		fd.gotAuthPrincipal != "principal_1" || fd.gotAuthScope != domain.ScopeMailRead {
		t.Fatalf("authorize call = (%q, %q, %q) x%d", fd.gotAuthAssistant, fd.gotAuthPrincipal, fd.gotAuthScope, fd.authorizeCalls)
	}
	if len(fd.recorded) != 0 {
		t.Fatalf("delegated READ recorded %d audit entries, want 0", len(fd.recorded))
	}
}

func TestActAsMissingGrantIsForbidden(t *testing.T) {
	h, fd := delegHarness(t)
	fd.authorizeErr = domain.ErrForbidden

	rec := actAs(h, "principal_1", http.MethodGet, "/v1/mail/threads", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if got := decodeErr(t, rec); got.Code != "forbidden" {
		t.Fatalf("error code = %q, want forbidden", got.Code)
	}
	if h.mail.listCalls != 0 {
		t.Fatal("handler must not run without an authorized grant")
	}
}

func TestActAsNonDelegableRouteIsForbiddenOutright(t *testing.T) {
	h, fd := delegHarness(t)

	rec := actAs(h, "principal_1", http.MethodPost, "/v1/devices",
		jsonBody(t, map[string]string{"platform": "web", "token": "tok"}))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if fd.authorizeCalls != 0 {
		t.Fatal("non-delegable routes must be rejected before any grant lookup")
	}
	if len(fd.recorded) != 0 {
		t.Fatalf("rejected request recorded %d audit entries, want 0", len(fd.recorded))
	}
}

// TestActAsDeniedCollabSubpathsAreForbidden drives every denied team/collab
// sub-path through the full stack under act-as: each must 403 before any
// grant lookup, so a delegated assistant can never mint share tokens,
// self-grant calendar shares, touch team comments/activity, or read/edit
// snippets on the principal's behalf.
func TestActAsDeniedCollabSubpathsAreForbidden(t *testing.T) {
	denied := []struct{ method, path string }{
		{http.MethodPost, "/v1/mail/threads/t1/share"},
		{http.MethodGet, "/v1/mail/threads/t1/shares"},
		{http.MethodDelete, "/v1/mail/threads/t1/shares/sh1"},
		{http.MethodGet, "/v1/mail/threads/t1/comments"},
		{http.MethodPost, "/v1/mail/threads/t1/comments"},
		{http.MethodGet, "/v1/mail/threads/t1/team-activity"},
		{http.MethodGet, "/v1/mail/snippets"},
		{http.MethodPost, "/v1/mail/snippets"},
		{http.MethodPut, "/v1/mail/snippets/sn1"},
		{http.MethodDelete, "/v1/mail/snippets/sn1"},
		{http.MethodGet, "/v1/calendars/c1/shares"},
		{http.MethodPost, "/v1/calendars/c1/shares"},
		{http.MethodPatch, "/v1/calendars/c1/shares/sh1"},
		{http.MethodDelete, "/v1/calendars/c1/shares/sh1"},
		{http.MethodPatch, "/v1/comments/cm1"},
		{http.MethodDelete, "/v1/comments/cm1"},
		{http.MethodGet, "/v1/events/e1/note"},
		{http.MethodPut, "/v1/events/e1/note"},
	}
	for _, tc := range denied {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			h, fd := delegHarness(t)
			rec := actAs(h, "principal_1", tc.method, tc.path, nil)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
			}
			if got := decodeErr(t, rec).Code; got != "forbidden" {
				t.Fatalf("error code = %q, want forbidden", got)
			}
			if fd.authorizeCalls != 0 {
				t.Fatal("denied sub-path must be rejected before any grant lookup")
			}
			if len(fd.recorded) != 0 {
				t.Fatalf("denied request recorded %d audit entries, want 0", len(fd.recorded))
			}
		})
	}
}

func TestActAsDelegationsRouteIsForbidden(t *testing.T) {
	h, fd := delegHarness(t)

	rec := actAs(h, "principal_1", http.MethodGet, "/v1/delegations", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if fd.authorizeCalls != 0 {
		t.Fatal("delegation management must never be delegable")
	}
}

func TestActAsUnresolvablePrincipalIsForbidden(t *testing.T) {
	h, _ := delegHarness(t)
	h.users.getErr = domain.ErrNotFound
	h.users.getRet = domain.User{}

	rec := actAs(h, "ghost", http.MethodGet, "/v1/mail/threads", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (fail closed)", rec.Code)
	}
	if h.mail.listCalls != 0 {
		t.Fatal("handler must not run for an unresolvable principal")
	}
}

func TestDelegatedMutationRecordsOneAttributedAuditEntry(t *testing.T) {
	h, fd := delegHarness(t)
	h.mail.actRet = domain.Thread{ID: "t1"}

	rec := actAs(h, "principal_1", http.MethodPost, "/v1/mail/threads/t1/actions",
		jsonBody(t, map[string]string{"action": "archive"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if fd.gotAuthScope != domain.ScopeMailWrite {
		t.Fatalf("scope = %q, want mail_write", fd.gotAuthScope)
	}
	if len(fd.recorded) != 1 {
		t.Fatalf("audit entries = %d, want exactly 1", len(fd.recorded))
	}
	e := fd.recorded[0]
	if e.ActorID != defaultUserID {
		t.Fatalf("actor = %q, want the REAL actor (assistant %q)", e.ActorID, defaultUserID)
	}
	if e.PrincipalID != "principal_1" {
		t.Fatalf("principal = %q, want principal_1", e.PrincipalID)
	}
	if e.Action != "POST /v1/mail/threads/{id}/actions" {
		t.Fatalf("action = %q", e.Action)
	}
	if e.ResourceType != "mail" {
		t.Fatalf("resourceType = %q, want mail", e.ResourceType)
	}
	if e.ResourceID != "t1" {
		t.Fatalf("resource = %q, want t1", e.ResourceID)
	}
	if e.Metadata["route"] != "/v1/mail/threads/{id}/actions" {
		t.Fatalf("metadata route = %v", e.Metadata["route"])
	}
}

func TestDelegatedFailedMutationRecordsNothing(t *testing.T) {
	h, fd := delegHarness(t)
	h.mail.actErr = domain.ErrNotFound

	rec := actAs(h, "principal_1", http.MethodPost, "/v1/mail/threads/t1/actions",
		jsonBody(t, map[string]string{"action": "archive"}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if len(fd.recorded) != 0 {
		t.Fatalf("failed mutation recorded %d audit entries, want 0", len(fd.recorded))
	}
}

func TestNonDelegatedRequestRecordsNothing(t *testing.T) {
	h, fd := delegHarness(t)
	h.mail.actRet = domain.Thread{ID: "t1"}

	rec := h.authed(http.MethodPost, "/v1/mail/threads/t1/actions",
		jsonBody(t, map[string]string{"action": "archive"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if fd.authorizeCalls != 0 || len(fd.recorded) != 0 {
		t.Fatalf("non-delegated request touched delegation service (authorize=%d, audit=%d)",
			fd.authorizeCalls, len(fd.recorded))
	}
	if h.mail.gotActID != "t1" {
		t.Fatalf("handler did not run normally: gotActID=%q", h.mail.gotActID)
	}
}

func TestActAsRetainsActorAlongsidePrincipal(t *testing.T) {
	h, _ := delegHarness(t)
	srv := h.server()

	var gotUser, gotActor domain.User
	var actorOK bool
	handler := srv.requireAuth(srv.withActAs(func(w http.ResponseWriter, r *http.Request) {
		gotUser = userFrom(r)
		gotActor, actorOK = actorFrom(r)
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/mail/threads", nil)
	req.Header.Set("Authorization", "Bearer "+defaultToken)
	req.Header.Set(actAsHeader, "principal_1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body.String())
	}
	if gotUser.ID != "principal_1" {
		t.Fatalf("context user = %q, want the principal", gotUser.ID)
	}
	if !actorOK || gotActor.ID != defaultUserID {
		t.Fatalf("context actor = (%q, %v), want the assistant retained", gotActor.ID, actorOK)
	}

	// A non-delegated request carries no actor.
	req = httptest.NewRequest(http.MethodGet, "/v1/mail/threads", nil)
	req.Header.Set("Authorization", "Bearer "+defaultToken)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if gotUser.ID != defaultUserID {
		t.Fatalf("non-delegated context user = %q, want %q", gotUser.ID, defaultUserID)
	}
	if actorOK {
		t.Fatal("non-delegated request must carry no actor")
	}
}

// --- delegation management handlers ------------------------------------------

func TestCreateDelegationHandler(t *testing.T) {
	h, fd := delegHarness(t)
	fd.createRet = domain.Delegation{ID: "del1", Status: domain.DelegationPending}

	rec := h.authed(http.MethodPost, "/v1/delegations",
		jsonBody(t, map[string]any{
			"assistantEmail": "assistant@example.com",
			"scopes":         []string{"mail_read", "mail_write"},
		}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", rec.Code, rec.Body.String())
	}
	if fd.gotCreatePrincipal != defaultUserID || fd.gotCreateEmail != "assistant@example.com" {
		t.Fatalf("create args = (%q, %q)", fd.gotCreatePrincipal, fd.gotCreateEmail)
	}
	if len(fd.gotCreateScopes) != 2 || fd.gotCreateScopes[0] != domain.ScopeMailRead {
		t.Fatalf("scopes = %v", fd.gotCreateScopes)
	}

	rec = h.authed(http.MethodPost, "/v1/delegations",
		jsonBody(t, map[string]any{"assistantEmail": "a@b.c", "scopes": []string{"sudo"}}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid scope: status = %d, want 400", rec.Code)
	}
}

func TestListDelegationsHandler(t *testing.T) {
	h, fd := delegHarness(t)
	fd.listAsPrincipal = []domain.Delegation{{ID: "del1"}}
	fd.listAsAssistant = []domain.Delegation{}

	rec := h.authed(http.MethodGet, "/v1/delegations", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		AsPrincipal []domain.Delegation `json:"asPrincipal"`
		AsAssistant []domain.Delegation `json:"asAssistant"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.AsPrincipal) != 1 || body.AsAssistant == nil {
		t.Fatalf("body = %+v", body)
	}
}

func TestAcceptAndRevokeDelegationHandlers(t *testing.T) {
	h, fd := delegHarness(t)
	fd.acceptRet = domain.Delegation{ID: "del1", Status: domain.DelegationActive}

	rec := h.authed(http.MethodPost, "/v1/delegations/del1/accept", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("accept status = %d, want 200", rec.Code)
	}
	if fd.gotAcceptUser != defaultUserID || fd.gotAcceptID != "del1" {
		t.Fatalf("accept args = (%q, %q)", fd.gotAcceptUser, fd.gotAcceptID)
	}

	rec = h.authed(http.MethodDelete, "/v1/delegations/del1", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d, want 204", rec.Code)
	}
	if fd.gotRevokeUser != defaultUserID || fd.gotRevokeID != "del1" {
		t.Fatalf("revoke args = (%q, %q)", fd.gotRevokeUser, fd.gotRevokeID)
	}
}

func TestDelegationAuditHandler(t *testing.T) {
	h, fd := delegHarness(t)
	fd.auditRet = []domain.AuditEntry{{ID: "a1", PrincipalID: defaultUserID, ActorID: "assistant_1"}}

	rec := h.authed(http.MethodGet, "/v1/delegations/audit?limit=5", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if fd.gotAuditUser != defaultUserID || fd.gotAuditLimit != 5 {
		t.Fatalf("audit args = (%q, %d)", fd.gotAuditUser, fd.gotAuditLimit)
	}

	rec = h.authed(http.MethodGet, "/v1/delegations/audit?limit=nope", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad limit: status = %d, want 400", rec.Code)
	}
}

func TestDelegationRoutesWithoutServiceAre501(t *testing.T) {
	h := newHarness(t) // Deps.Delegations left nil
	rec := h.authed(http.MethodGet, "/v1/delegations", nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestActAsWithoutServiceIsForbidden(t *testing.T) {
	h := newHarness(t) // Deps.Delegations left nil
	rec := actAs(h, "principal_1", http.MethodGet, "/v1/mail/threads", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (fail closed)", rec.Code)
	}
}
