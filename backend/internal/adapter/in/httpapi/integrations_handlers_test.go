package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- fake IntegrationService -------------------------------------------------

type fakeIntegrationService struct {
	listRet []domain.IntegrationConnection
	listErr error

	beginURL       string
	beginErr       error
	gotVendor      domain.IntegrationVendor
	gotRedirectURL string
	gotBaseURL     string

	completeConn     domain.IntegrationConnection
	completeRedirect string
	completeErr      error
	gotState         string
	gotCode          string

	disconnectErr   error
	gotDisconnectID string
}

func (f *fakeIntegrationService) List(_ context.Context, _ string) ([]domain.IntegrationConnection, error) {
	return f.listRet, f.listErr
}
func (f *fakeIntegrationService) BeginConnect(_ context.Context, _ string, vendor domain.IntegrationVendor, redirectURL, requestBaseURL string) (string, error) {
	f.gotVendor, f.gotRedirectURL, f.gotBaseURL = vendor, redirectURL, requestBaseURL
	return f.beginURL, f.beginErr
}
func (f *fakeIntegrationService) CompleteConnect(_ context.Context, vendor domain.IntegrationVendor, state, code, _ string) (domain.IntegrationConnection, string, error) {
	f.gotVendor, f.gotState, f.gotCode = vendor, state, code
	return f.completeConn, f.completeRedirect, f.completeErr
}
func (f *fakeIntegrationService) Disconnect(_ context.Context, _ string, connectionID string) error {
	f.gotDisconnectID = connectionID
	return f.disconnectErr
}

var _ port.IntegrationService = (*fakeIntegrationService)(nil)

func integrationHarnessHTTP(t *testing.T) (*harness, *fakeIntegrationService) {
	t.Helper()
	h := newHarness(t)
	fake := &fakeIntegrationService{}
	h.deps.Integrations = fake
	return h, fake
}

// --- tests -------------------------------------------------------------------

func TestListIntegrations(t *testing.T) {
	t.Run("returns the caller's connections without leaking the owner id", func(t *testing.T) {
		h, fake := integrationHarnessHTTP(t)
		lastErr := "sync broke"
		fake.listRet = []domain.IntegrationConnection{{
			ID:              "conn1",
			UserID:          "secret-owner-id",
			Vendor:          domain.IntegrationTodoist,
			ExternalAccount: "person@example.com",
			Status:          domain.IntegrationStatusError,
			LastError:       &lastErr,
			CreatedAt:       time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC),
		}}
		rec := h.authed(http.MethodGet, "/v1/integrations", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if strings.Contains(body, "secret-owner-id") || strings.Contains(body, "userId") {
			t.Fatalf("response leaks the owner id: %s", body)
		}
		if strings.Contains(strings.ToLower(body), "token") {
			t.Fatalf("response carries a token-ish field: %s", body)
		}
		var conns []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &conns); err != nil {
			t.Fatalf("decode: %v (body=%s)", err, body)
		}
		if len(conns) != 1 || conns[0]["vendor"] != "todoist" || conns[0]["lastError"] != "sync broke" {
			t.Fatalf("conns = %+v", conns)
		}
	})

	t.Run("501 when the service is not wired", func(t *testing.T) {
		h := newHarness(t) // Integrations left nil
		rec := h.authed(http.MethodGet, "/v1/integrations", nil)
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want 501", rec.Code)
		}
		if got := decodeErr(t, rec).Code; got != "not_implemented" {
			t.Fatalf("code = %q, want not_implemented", got)
		}
	})

	t.Run("401 without a bearer token", func(t *testing.T) {
		h, _ := integrationHarnessHTTP(t)
		rec := h.anon(http.MethodGet, "/v1/integrations", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})
}

func TestConnectIntegration(t *testing.T) {
	t.Run("returns the vendor auth URL", func(t *testing.T) {
		h, fake := integrationHarnessHTTP(t)
		fake.beginURL = "https://todoist.com/oauth/authorize?state=abc"
		rec := h.authed(http.MethodPost, "/v1/integrations/connect/todoist",
			jsonBody(t, map[string]string{"redirectUrl": "http://localhost:3000/settings"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["url"] != fake.beginURL {
			t.Fatalf("url = %q", body["url"])
		}
		if fake.gotVendor != domain.IntegrationTodoist || fake.gotRedirectURL != "http://localhost:3000/settings" {
			t.Fatalf("service called with (%q, %q)", fake.gotVendor, fake.gotRedirectURL)
		}
		if fake.gotBaseURL == "" {
			t.Fatal("requestBaseURL not forwarded")
		}
	})

	t.Run("unknown vendor is 400", func(t *testing.T) {
		h, _ := integrationHarnessHTTP(t)
		rec := h.authed(http.MethodPost, "/v1/integrations/connect/linear",
			jsonBody(t, map[string]string{"redirectUrl": "http://localhost:3000"}))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec).Code; got != "validation_failed" {
			t.Fatalf("code = %q, want validation_failed", got)
		}
	})

	t.Run("unconfigured vendor is 501", func(t *testing.T) {
		h, fake := integrationHarnessHTTP(t)
		fake.beginErr = fmt.Errorf("%w: integration vendor hubspot is not configured", domain.ErrNotImplemented)
		rec := h.authed(http.MethodPost, "/v1/integrations/connect/hubspot",
			jsonBody(t, map[string]string{"redirectUrl": "http://localhost:3000"}))
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want 501 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec).Code; got != "not_implemented" {
			t.Fatalf("code = %q, want not_implemented", got)
		}
	})
}

func TestIntegrationCallback(t *testing.T) {
	t.Run("302s to the client redirect with status=connected", func(t *testing.T) {
		h, fake := integrationHarnessHTTP(t)
		fake.completeRedirect = "http://localhost:3000/settings"
		rec := h.anon(http.MethodGet, "/v1/integrations/callback/todoist?state=st-1&code=co-1", nil)
		if rec.Code != http.StatusFound {
			t.Fatalf("status = %d, want 302 (body=%s)", rec.Code, rec.Body.String())
		}
		if loc := rec.Header().Get("Location"); loc != "http://localhost:3000/settings?status=connected" {
			t.Fatalf("Location = %q", loc)
		}
		if fake.gotState != "st-1" || fake.gotCode != "co-1" || fake.gotVendor != domain.IntegrationTodoist {
			t.Fatalf("service called with state=%q code=%q vendor=%q", fake.gotState, fake.gotCode, fake.gotVendor)
		}
	})

	t.Run("failure after state consumption 302s with status=error", func(t *testing.T) {
		h, fake := integrationHarnessHTTP(t)
		fake.completeRedirect = "http://localhost:3000/settings"
		fake.completeErr = fmt.Errorf("%w: authorization code exchange failed", domain.ErrUnauthorized)
		rec := h.anon(http.MethodGet, "/v1/integrations/callback/todoist?state=st-1&code=bad", nil)
		if rec.Code != http.StatusFound {
			t.Fatalf("status = %d, want 302", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "http://localhost:3000/settings?status=error" {
			t.Fatalf("Location = %q", loc)
		}
	})

	t.Run("state mismatch fails closed: no redirect, static error page", func(t *testing.T) {
		h, fake := integrationHarnessHTTP(t)
		fake.completeRedirect = "" // forged state — no client redirect is known
		fake.completeErr = fmt.Errorf("%w: invalid oauth state", domain.ErrNotFound)
		rec := h.anon(http.MethodGet, "/v1/integrations/callback/todoist?state=forged&code=co", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (fail closed, no open redirect)", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "" {
			t.Fatalf("a forged state must never redirect, got Location %q", loc)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
			t.Fatalf("Content-Type = %q, want the static html page", ct)
		}
	})

	t.Run("unknown vendor renders a 400 page", func(t *testing.T) {
		h, fake := integrationHarnessHTTP(t)
		rec := h.anon(http.MethodGet, "/v1/integrations/callback/linear?state=s&code=c", nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		if fake.gotState != "" {
			t.Fatal("service must not be called for an unknown vendor")
		}
	})

	t.Run("501 page when the service is not wired", func(t *testing.T) {
		h := newHarness(t)
		rec := h.anon(http.MethodGet, "/v1/integrations/callback/todoist?state=s&code=c", nil)
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want 501", rec.Code)
		}
	})
}

func TestDisconnectIntegration(t *testing.T) {
	t.Run("204 on success", func(t *testing.T) {
		h, fake := integrationHarnessHTTP(t)
		rec := h.authed(http.MethodDelete, "/v1/integrations/conn42", nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body.String())
		}
		if fake.gotDisconnectID != "conn42" {
			t.Fatalf("disconnect id = %q", fake.gotDisconnectID)
		}
	})

	t.Run("missing/foreign connection is 404", func(t *testing.T) {
		h, fake := integrationHarnessHTTP(t)
		fake.disconnectErr = domain.ErrNotFound
		rec := h.authed(http.MethodDelete, "/v1/integrations/ghost", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

// TestInstanceCapabilities proves GET /v1/instance serves the capability
// flags verbatim so clients can gate their integration UI.
func TestInstanceCapabilities(t *testing.T) {
	h := newHarness(t)
	h.deps.Instance.Capabilities = InstanceCapabilities{Todoist: true}
	rec := h.anon(http.MethodGet, "/v1/instance", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if body.Capabilities == nil {
		t.Fatalf("instance document has no capabilities object: %s", rec.Body.String())
	}
	if !body.Capabilities["todoist"] || body.Capabilities["hubspot"] || body.Capabilities["maps"] || body.Capabilities["weather"] {
		t.Fatalf("capabilities = %v, want only todoist advertised", body.Capabilities)
	}
}
