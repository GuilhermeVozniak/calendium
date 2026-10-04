package httpapi

import (
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

type fakeCrmService struct {
	ctxRet      []domain.CrmContext
	ctxErr      error
	gotCtxUser  string
	gotCtxEmail string

	logErr     error
	gotLogUser string
	gotLog     domain.CrmEmailLog
}

var _ port.CrmService = (*fakeCrmService)(nil)

func (f *fakeCrmService) ContactContext(_ context.Context, userID, email string) ([]domain.CrmContext, error) {
	f.gotCtxUser, f.gotCtxEmail = userID, email
	return f.ctxRet, f.ctxErr
}
func (f *fakeCrmService) LogEmail(_ context.Context, userID string, log domain.CrmEmailLog) error {
	f.gotLogUser, f.gotLog = userID, log
	return f.logErr
}

func TestCrmRoutesUnwiredAnswer501(t *testing.T) {
	h := newHarness(t) // Crm left nil
	if rec := h.authed(http.MethodGet, "/v1/crm/context?email=a%40b.c", nil); rec.Code != http.StatusNotImplemented {
		t.Fatalf("GET context status = %d, want 501", rec.Code)
	}
	rec := h.authed(http.MethodPost, "/v1/crm/log", jsonBody(t, map[string]string{"contactEmail": "a@b.c"}))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("POST log status = %d, want 501", rec.Code)
	}
	if got := decodeErr(t, rec).Code; got != "not_implemented" {
		t.Fatalf("error code = %q, want not_implemented", got)
	}
}

func TestCrmContextRequiresEmailParam(t *testing.T) {
	h := newHarness(t)
	h.deps.Crm = &fakeCrmService{}
	rec := h.authed(http.MethodGet, "/v1/crm/context", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if got := decodeErr(t, rec).Code; got != "validation_failed" {
		t.Fatalf("error code = %q, want validation_failed", got)
	}
}

func TestCrmContextReturnsVendorContexts(t *testing.T) {
	amount := 1200.5
	crm := &fakeCrmService{ctxRet: []domain.CrmContext{{
		Vendor:  domain.IntegrationHubSpot,
		Contact: &domain.CrmContact{ID: "301", Email: "ada@northwind.com", Name: "Ada Lovelace"},
		Deals:   []domain.CrmDeal{{ID: "9001", Name: "Renewal", Stage: "contractsent", Amount: &amount}},
	}}}
	h := newHarness(t)
	h.deps.Crm = crm

	rec := h.authed(http.MethodGet, "/v1/crm/context?email=ada%40northwind.com", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if crm.gotCtxUser != defaultUserID || crm.gotCtxEmail != "ada@northwind.com" {
		t.Fatalf("service got (%q, %q)", crm.gotCtxUser, crm.gotCtxEmail)
	}
	var got []domain.CrmContext
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if len(got) != 1 || got[0].Vendor != domain.IntegrationHubSpot ||
		got[0].Contact == nil || got[0].Contact.Name != "Ada Lovelace" ||
		len(got[0].Deals) != 1 || got[0].Deals[0].Amount == nil || *got[0].Deals[0].Amount != 1200.5 {
		t.Fatalf("got = %+v", got)
	}
}

// TestCrmContextNoConnectionsIs200Empty pins the brief's contract: a wired
// service with zero connected CRMs answers 200 [] — never 501 — so clients
// can distinguish "feature off" from "nothing connected".
func TestCrmContextNoConnectionsIs200Empty(t *testing.T) {
	h := newHarness(t)
	h.deps.Crm = &fakeCrmService{ctxRet: []domain.CrmContext{}}

	rec := h.authed(http.MethodGet, "/v1/crm/context?email=a%40b.c", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Fatalf("body = %q, want []", body)
	}
}

func TestCrmLogForwardsPayload(t *testing.T) {
	crm := &fakeCrmService{}
	h := newHarness(t)
	h.deps.Crm = crm

	sentAt := time.Date(2026, 7, 18, 9, 30, 0, 0, time.UTC)
	rec := h.authed(http.MethodPost, "/v1/crm/log", jsonBody(t, map[string]any{
		"contactEmail": "ada@northwind.com",
		"subject":      "Renewal",
		"bodyText":     "Attached.",
		"sentAt":       sentAt.Format(time.RFC3339),
		"direction":    "outbound",
	}))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body.String())
	}
	if crm.gotLogUser != defaultUserID {
		t.Fatalf("user = %q, want %q", crm.gotLogUser, defaultUserID)
	}
	if crm.gotLog.ContactEmail != "ada@northwind.com" || crm.gotLog.Subject != "Renewal" ||
		crm.gotLog.BodyText != "Attached." || crm.gotLog.Direction != "outbound" ||
		!crm.gotLog.SentAt.Equal(sentAt) {
		t.Fatalf("forwarded log = %+v", crm.gotLog)
	}
}

// TestCrmLogBodyLimit: logging a long email (quoted history, newsletters)
// sends the full synced bodyText, so the route takes a body up to the 1 MiB
// cap rather than the 64 KiB long-text field limit (whole-branch final
// review M5). Over the cap is still 413.
func TestCrmLogBodyLimit(t *testing.T) {
	logOfSize := func(n int) string {
		const frame = `{"contactEmail":"ada@northwind.com","subject":"Renewal","bodyText":""}`
		return `{"contactEmail":"ada@northwind.com","subject":"Renewal","bodyText":"` +
			strings.Repeat("x", n-len(frame)) + `"}`
	}
	t.Run("1 MiB - 1 accepted", func(t *testing.T) {
		crm := &fakeCrmService{}
		h := newHarness(t)
		h.deps.Crm = crm
		body := logOfSize((1 << 20) - 1)
		rec := h.authed(http.MethodPost, "/v1/crm/log", strings.NewReader(body))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body=%.300s)", rec.Code, rec.Body.String())
		}
		if got, want := len(crm.gotLog.BodyText), len(body)-len(`{"contactEmail":"ada@northwind.com","subject":"Renewal","bodyText":""}`); got != want {
			t.Fatalf("bodyText len = %d, want %d", got, want)
		}
	})
	t.Run("1 MiB + 1 is 413", func(t *testing.T) {
		h := newHarness(t)
		h.deps.Crm = &fakeCrmService{}
		rec := h.authed(http.MethodPost, "/v1/crm/log", strings.NewReader(logOfSize((1<<20)+1)))
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413", rec.Code)
		}
	})
}

// TestCrmRoutesUnauthenticated401 pins that both CRM routes sit behind
// requireAuth: no bearer token means 401 before the service is ever reached.
func TestCrmRoutesUnauthenticated401(t *testing.T) {
	crm := &fakeCrmService{ctxRet: []domain.CrmContext{{Vendor: domain.IntegrationHubSpot}}}
	h := newHarness(t)
	h.deps.Crm = crm

	rec := h.anon(http.MethodGet, "/v1/crm/context?email=a%40b.c", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET context status = %d, want 401", rec.Code)
	}
	if got := decodeErr(t, rec).Code; got != "unauthorized" {
		t.Fatalf("error code = %q, want unauthorized", got)
	}
	rec = h.anon(http.MethodPost, "/v1/crm/log", jsonBody(t, map[string]string{"contactEmail": "a@b.c"}))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST log status = %d, want 401", rec.Code)
	}
	if crm.gotCtxUser != "" || crm.gotCtxEmail != "" || crm.gotLogUser != "" {
		t.Fatalf("service reached without auth: ctx=(%q,%q) log=%q", crm.gotCtxUser, crm.gotCtxEmail, crm.gotLogUser)
	}
}

// tenantCrmService returns per-user data, so a request authenticated as one
// user can never observe another user's CRM rows through the handler.
type tenantCrmService struct {
	byUser   map[string][]domain.CrmContext
	gotUsers []string
}

var _ port.CrmService = (*tenantCrmService)(nil)

func (f *tenantCrmService) ContactContext(_ context.Context, userID, _ string) ([]domain.CrmContext, error) {
	f.gotUsers = append(f.gotUsers, userID)
	return f.byUser[userID], nil
}
func (f *tenantCrmService) LogEmail(_ context.Context, userID string, _ domain.CrmEmailLog) error {
	f.gotUsers = append(f.gotUsers, userID)
	return nil
}

// TestCrmContextCrossTenantUserScoping is the cross-tenant negative: the
// handler threads ONLY the authenticated identity's user id into the CRM
// service (never a caller-supplied one — there is no user parameter on the
// route), so user B authenticating with their own token cannot see user A's
// CRM context.
func TestCrmContextCrossTenantUserScoping(t *testing.T) {
	crm := &tenantCrmService{byUser: map[string][]domain.CrmContext{
		defaultUserID: {{
			Vendor:  domain.IntegrationHubSpot,
			Contact: &domain.CrmContact{ID: "301", Email: "ada@northwind.com", Name: "Ada Lovelace"},
		}},
	}}
	h := newHarness(t)
	h.deps.Crm = crm

	// User A (the default token) sees their own vendor context.
	rec := h.authed(http.MethodGet, "/v1/crm/context?email=ada%40northwind.com", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("user A status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var aCtx []domain.CrmContext
	if err := json.Unmarshal(rec.Body.Bytes(), &aCtx); err != nil || len(aCtx) != 1 || aCtx[0].Contact == nil || aCtx[0].Contact.ID != "301" {
		t.Fatalf("user A body = %s (err %v), want their one HubSpot context", rec.Body.String(), err)
	}

	// User B authenticates with a different token; EnsureUser resolves them
	// to user_2. The handler must pass user_2 — not user A's id — downward.
	h.verifier.tokens["token-b"] = port.Identity{Subject: "user_2", Email: "intruder@example.com", Name: "Intruder"}
	h.users.ensureRet = domain.User{ID: "user_2", Email: "intruder@example.com"}
	req := httptest.NewRequest(http.MethodGet, "/v1/crm/context?email=ada%40northwind.com", nil)
	req.Header.Set("Authorization", "Bearer token-b")
	recB := httptest.NewRecorder()
	h.handler().ServeHTTP(recB, req)

	if recB.Code != http.StatusOK {
		t.Fatalf("user B status = %d, want 200 (body=%s)", recB.Code, recB.Body.String())
	}
	if body := strings.TrimSpace(recB.Body.String()); body != "[]" {
		t.Fatalf("user B body = %q, want [] — user A's CRM context leaked cross-tenant", body)
	}
	if len(crm.gotUsers) != 2 || crm.gotUsers[0] != defaultUserID || crm.gotUsers[1] != "user_2" {
		t.Fatalf("service saw users %v, want [%s user_2] (authenticated identity only)", crm.gotUsers, defaultUserID)
	}
}

func TestCrmLogRejectsMalformedJSON(t *testing.T) {
	h := newHarness(t)
	h.deps.Crm = &fakeCrmService{}
	rec := h.authed(http.MethodPost, "/v1/crm/log", strings.NewReader("{not json"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
