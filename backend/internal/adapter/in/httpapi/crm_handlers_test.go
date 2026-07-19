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
		Vendor:  domain.IntegrationVendorHubSpot,
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
	if len(got) != 1 || got[0].Vendor != domain.IntegrationVendorHubSpot ||
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

func TestCrmLogRejectsMalformedJSON(t *testing.T) {
	h := newHarness(t)
	h.deps.Crm = &fakeCrmService{}
	rec := h.authed(http.MethodPost, "/v1/crm/log", strings.NewReader("{not json"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
