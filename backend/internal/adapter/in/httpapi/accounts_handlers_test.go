package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"calendium/backend/internal/domain"
)

func TestHandleListAccounts(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h := newHarness(t)
		h.accounts.listRet = []domain.ConnectedAccount{{ID: "a1"}}
		rec := h.authed(http.MethodGet, "/v1/accounts", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		var got []domain.ConnectedAccount
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got) != 1 || got[0].ID != "a1" {
			t.Fatalf("accounts = %+v", got)
		}
	})

	t.Run("service error forwarded", func(t *testing.T) {
		h := newHarness(t)
		h.accounts.listErr = domain.ErrPaymentRequired
		rec := h.authed(http.MethodGet, "/v1/accounts", nil)
		if rec.Code != http.StatusPaymentRequired {
			t.Fatalf("status = %d, want 402 (body=%s)", rec.Code, rec.Body.String())
		}
	})
}

func TestHandleConnectAccount(t *testing.T) {
	t.Run("valid provider builds redirect", func(t *testing.T) {
		h := newHarness(t)
		h.accounts.beginURL = "https://accounts.google.com/o/oauth2/auth?..."
		rec := h.authed(http.MethodPost, "/v1/accounts/connect/google", jsonBody(t, map[string]string{"redirectUrl": "myapp://cb"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["url"] != h.accounts.beginURL {
			t.Fatalf("url = %q, want %q", body["url"], h.accounts.beginURL)
		}
		if h.accounts.gotProvider != domain.ProviderGoogle {
			t.Fatalf("gotProvider = %q, want google", h.accounts.gotProvider)
		}
		if h.accounts.gotRedirectURL != "myapp://cb" {
			t.Fatalf("gotRedirectURL = %q, want myapp://cb", h.accounts.gotRedirectURL)
		}
		if h.accounts.gotBeginBaseURL != "http://example.com" {
			t.Fatalf("gotBeginBaseURL = %q, want http://example.com", h.accounts.gotBeginBaseURL)
		}
	})

	t.Run("unknown provider rejected", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/accounts/connect/yahoo", jsonBody(t, map[string]string{"redirectUrl": "myapp://cb"}))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "validation_failed" {
			t.Fatalf("code = %q, want validation_failed", got.Code)
		}
		if h.accounts.gotProvider != "" {
			t.Fatalf("BeginConnect called unexpectedly")
		}
	})

	t.Run("malformed JSON on a valid provider", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/accounts/connect/google", strings.NewReader("{"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("service error forwarded", func(t *testing.T) {
		h := newHarness(t)
		h.accounts.beginErr = domain.ErrNotFound
		rec := h.authed(http.MethodPost, "/v1/accounts/connect/google", jsonBody(t, map[string]string{"redirectUrl": "myapp://cb"}))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
		}
	})
}

func TestHandleSetVipSenders(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPut, "/v1/accounts/acc1/vip-senders", jsonBody(t, map[string][]string{"vipSenders": {"a@x.com", "b@y.com"}}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.accounts.gotVIPID != "acc1" {
			t.Fatalf("gotVIPID = %q, want acc1", h.accounts.gotVIPID)
		}
		want := []string{"a@x.com", "b@y.com"}
		if !reflect.DeepEqual(h.accounts.gotVIP, want) {
			t.Fatalf("gotVIP = %v, want %v", h.accounts.gotVIP, want)
		}
	})

	t.Run("other user's account not found", func(t *testing.T) {
		h := newHarness(t)
		h.accounts.vipErr = domain.ErrNotFound
		rec := h.authed(http.MethodPut, "/v1/accounts/acc1/vip-senders", jsonBody(t, map[string][]string{"vipSenders": {"a@x.com"}}))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("malformed JSON rejected", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPut, "/v1/accounts/acc1/vip-senders", strings.NewReader("{"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
}

func TestHandleDisconnectAccount(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodDelete, "/v1/accounts/acc1", nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.accounts.gotDisconnectID != "acc1" {
			t.Fatalf("gotDisconnectID = %q, want acc1", h.accounts.gotDisconnectID)
		}
	})

	t.Run("not found", func(t *testing.T) {
		h := newHarness(t)
		h.accounts.disconnectErr = domain.ErrNotFound
		rec := h.authed(http.MethodDelete, "/v1/accounts/acc1", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

func TestHandleAccountCallback(t *testing.T) {
	t.Run("success redirects to client with connected status", func(t *testing.T) {
		h := newHarness(t)
		h.accounts.completeRedirect = "myapp://done"
		rec := h.anon(http.MethodGet, "/v1/accounts/callback/google?state=st&code=cd", nil)
		if rec.Code != http.StatusFound {
			t.Fatalf("status = %d, want 302 (body=%s)", rec.Code, rec.Body.String())
		}
		loc := rec.Header().Get("Location")
		if !strings.Contains(loc, "myapp://done") || !strings.Contains(loc, "status=connected") {
			t.Fatalf("Location = %q, want to contain myapp://done and status=connected", loc)
		}
		if h.accounts.gotState != "st" || h.accounts.gotCode != "cd" {
			t.Fatalf("gotState=%q gotCode=%q, want st/cd", h.accounts.gotState, h.accounts.gotCode)
		}
	})

	t.Run("known validation error redirects with error status", func(t *testing.T) {
		h := newHarness(t)
		h.accounts.completeErr = domain.ErrValidation
		h.accounts.completeRedirect = "myapp://done"
		rec := h.anon(http.MethodGet, "/v1/accounts/callback/google?state=st&code=cd", nil)
		if rec.Code != http.StatusFound {
			t.Fatalf("status = %d, want 302 (body=%s)", rec.Code, rec.Body.String())
		}
		loc := rec.Header().Get("Location")
		if !strings.Contains(loc, "myapp://done") || !strings.Contains(loc, "status=error") {
			t.Fatalf("Location = %q, want myapp://done?status=error", loc)
		}
	})

	t.Run("invalid state with no known redirect renders HTML page", func(t *testing.T) {
		h := newHarness(t)
		h.accounts.completeErr = domain.ErrValidation
		h.accounts.completeRedirect = ""
		rec := h.anon(http.MethodGet, "/v1/accounts/callback/google?state=bad&code=cd", nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Fatalf("Content-Type = %q, want text/html; charset=utf-8", ct)
		}
		if !strings.Contains(rec.Body.String(), "Connection failed") {
			t.Fatalf("body missing 'Connection failed': %s", rec.Body.String())
		}
	})

	t.Run("unknown provider rejected before calling service", func(t *testing.T) {
		h := newHarness(t)
		rec := h.anon(http.MethodGet, "/v1/accounts/callback/yahoo?state=st&code=cd", nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "Unknown provider.") {
			t.Fatalf("body missing 'Unknown provider.': %s", rec.Body.String())
		}
		if h.accounts.gotState != "" {
			t.Fatalf("CompleteConnect called unexpectedly")
		}
	})

	t.Run("internal error renders generic message without leaking detail", func(t *testing.T) {
		h := newHarness(t)
		h.accounts.completeErr = errors.New("boom")
		h.accounts.completeRedirect = ""
		rec := h.anon(http.MethodGet, "/v1/accounts/callback/google?state=st&code=cd", nil)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500 (body=%s)", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "Something went wrong while connecting the account.") {
			t.Fatalf("body missing generic message: %s", rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "boom") {
			t.Fatalf("body leaked internal error detail: %s", rec.Body.String())
		}
	})
}
