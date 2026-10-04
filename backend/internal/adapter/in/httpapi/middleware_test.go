package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

func TestRequireAuth(t *testing.T) {
	const (
		msgReject  = "invalid or expired access token"           // verifier-error path
		msgMissing = "Authentication is required or has failed." // writeError(ErrUnauthorized) path
	)
	tests := []struct {
		name        string
		setAuth     bool
		authHeader  string
		verifierErr error // forces Verify to fail
		unknownTok  bool  // send a token the verifier map does not know
		ensureErr   error // EnsureUser failure
		wantStatus  int
		wantCode    string
		wantMessage string
		wantNext    bool // did the wrapped handler run?
		wantVerify  bool // was the verifier consulted?
	}{
		{
			name:        "missing Authorization header",
			setAuth:     false,
			wantStatus:  http.StatusUnauthorized,
			wantCode:    "unauthorized",
			wantMessage: msgMissing,
			wantNext:    false,
			wantVerify:  false,
		},
		{
			name:        "wrong scheme is malformed",
			setAuth:     true,
			authHeader:  "Token abc123",
			wantStatus:  http.StatusUnauthorized,
			wantCode:    "unauthorized",
			wantMessage: msgMissing,
			wantNext:    false,
			wantVerify:  false,
		},
		{
			name:        "bearer with empty token is malformed",
			setAuth:     true,
			authHeader:  "Bearer    ", // only whitespace after the scheme
			wantStatus:  http.StatusUnauthorized,
			wantCode:    "unauthorized",
			wantMessage: msgMissing,
			wantNext:    false,
			wantVerify:  false,
		},
		{
			name:        "verifier rejects the token",
			setAuth:     true,
			authHeader:  "Bearer some-token",
			verifierErr: errors.New("bad signature"),
			wantStatus:  http.StatusUnauthorized,
			wantCode:    "unauthorized",
			wantMessage: msgReject,
			wantNext:    false,
			wantVerify:  true,
		},
		{
			name:        "unknown token rejected",
			setAuth:     true,
			authHeader:  "Bearer nope",
			unknownTok:  true,
			wantStatus:  http.StatusUnauthorized,
			wantCode:    "unauthorized",
			wantMessage: msgReject,
			wantNext:    false,
			wantVerify:  true,
		},
		{
			name:        "EnsureUser failure surfaces as 500",
			setAuth:     true,
			authHeader:  "Bearer " + defaultToken,
			ensureErr:   errors.New("db unavailable"),
			wantStatus:  http.StatusInternalServerError,
			wantCode:    "internal",
			wantMessage: "Internal server error.",
			wantNext:    false,
			wantVerify:  true,
		},
		{
			name:       "success runs next with injected user",
			setAuth:    true,
			authHeader: "Bearer " + defaultToken,
			wantStatus: http.StatusOK,
			wantNext:   true,
			wantVerify: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.verifier.err = tt.verifierErr
			h.users.ensureErr = tt.ensureErr
			if tt.unknownTok {
				h.verifier.tokens = map[string]port.Identity{} // known map, token absent
			}

			var ran bool
			var gotUser domain.User
			next := func(w http.ResponseWriter, r *http.Request) {
				ran = true
				gotUser = userFrom(r)
				w.WriteHeader(http.StatusOK)
			}
			handler := h.server().requireAuth(next)

			req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
			if tt.setAuth {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if ran != tt.wantNext {
				t.Fatalf("next ran = %v, want %v", ran, tt.wantNext)
			}
			if (h.verifier.calls > 0) != tt.wantVerify {
				t.Fatalf("verifier consulted = %v, want %v", h.verifier.calls > 0, tt.wantVerify)
			}
			if tt.wantStatus != http.StatusOK {
				got := decodeErr(t, rec)
				if got.Code != tt.wantCode {
					t.Fatalf("code = %q, want %q", got.Code, tt.wantCode)
				}
				if got.Message != tt.wantMessage {
					t.Fatalf("message = %q, want %q", got.Message, tt.wantMessage)
				}
			}
			if tt.wantNext {
				if gotUser.ID != defaultUserID {
					t.Fatalf("injected user id = %q, want %q", gotUser.ID, defaultUserID)
				}
				if h.users.gotIdentity.Subject != defaultUserID {
					t.Fatalf("EnsureUser identity subject = %q, want %q", h.users.gotIdentity.Subject, defaultUserID)
				}
			}
		})
	}
}

// --- CORS ----------------------------------------------------------------

func TestCORS(t *testing.T) {
	tests := []struct {
		name          string
		origin        string
		allowDev      bool
		wantOriginHdr string // expected Access-Control-Allow-Origin; "" = not set
	}{
		{name: "localhost reflected when ALLOW_DEV_ORIGINS", origin: "http://localhost:5173", allowDev: true, wantOriginHdr: "http://localhost:5173"},
		{name: "127.0.0.1 reflected when ALLOW_DEV_ORIGINS", origin: "http://127.0.0.1:3000", allowDev: true, wantOriginHdr: "http://127.0.0.1:3000"},
		{name: "localhost NOT reflected without ALLOW_DEV_ORIGINS", origin: "http://localhost:3000", allowDev: false, wantOriginHdr: ""},
		{name: "127.0.0.1 NOT reflected without ALLOW_DEV_ORIGINS", origin: "http://127.0.0.1:3000", allowDev: false, wantOriginHdr: ""},
		{name: "::1 NOT reflected without ALLOW_DEV_ORIGINS", origin: "http://[::1]:3000", allowDev: false, wantOriginHdr: ""},
		{name: "wails://wails always reflected", origin: "wails://wails", allowDev: false, wantOriginHdr: "wails://wails"},
		{name: "wails://wails.localhost always reflected", origin: "wails://wails.localhost", allowDev: false, wantOriginHdr: "wails://wails.localhost"},
		{name: "http://wails.localhost always reflected", origin: "http://wails.localhost", allowDev: false, wantOriginHdr: "http://wails.localhost"},
		{name: "https://wails.localhost:4567 always reflected", origin: "https://wails.localhost:4567", allowDev: false, wantOriginHdr: "https://wails.localhost:4567"},
		{name: "explicit allowlist match without dev origins", origin: "https://app.example.com", allowDev: false, wantOriginHdr: "https://app.example.com"},
		{name: "explicit allowlist trailing slash trimmed for matching", origin: "https://app.example.com/", allowDev: true, wantOriginHdr: "https://app.example.com/"},
		{name: "disallowed origin not reflected", origin: "https://evil.example.com", allowDev: true, wantOriginHdr: ""},
		{name: "localhost lookalike not reflected", origin: "http://localhost.evil.example", allowDev: true, wantOriginHdr: ""},
		{name: "no origin header", origin: "", allowDev: true, wantOriginHdr: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var probeRan bool
			probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				probeRan = true
				w.WriteHeader(http.StatusOK)
			})
			handler := corsMiddleware(probe, []string{"https://app.example.com"}, tt.allowDev)
			req := httptest.NewRequest(http.MethodGet, "/v1/whatever", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if !probeRan {
				t.Fatalf("probe did not run, want the request served")
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != tt.wantOriginHdr {
				t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, tt.wantOriginHdr)
			}
			if tt.wantOriginHdr != "" {
				if rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
					t.Fatalf("missing Access-Control-Allow-Credentials")
				}
				if !strings.Contains(rec.Header().Get("Vary"), "Origin") {
					t.Fatalf("Vary = %q, want to contain Origin", rec.Header().Get("Vary"))
				}
			} else if rec.Header().Get("Access-Control-Allow-Credentials") != "" {
				t.Fatalf("unexpected Access-Control-Allow-Credentials for disallowed/absent origin")
			}
		})
	}

	t.Run("preflight with request method returns 204 without running next", func(t *testing.T) {
		var probeRan bool
		probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			probeRan = true
			w.WriteHeader(http.StatusOK)
		})
		handler := corsMiddleware(probe, []string{"https://app.example.com"}, true)
		req := httptest.NewRequest(http.MethodOptions, "/v1/whatever", nil)
		req.Header.Set("Origin", "http://localhost:5173")
		req.Header.Set("Access-Control-Request-Method", "POST")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
			t.Fatalf("Access-Control-Allow-Origin = %q, want reflected origin", got)
		}
		if rec.Header().Get("Access-Control-Allow-Methods") == "" {
			t.Fatalf("missing Access-Control-Allow-Methods header on preflight")
		}
		if probeRan {
			t.Fatalf("probe ran on preflight, want short-circuited")
		}
	})

	t.Run("OPTIONS without request-method header falls through to next", func(t *testing.T) {
		var probeRan bool
		probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			probeRan = true
			w.WriteHeader(http.StatusOK)
		})
		handler := corsMiddleware(probe, []string{"https://app.example.com"}, true)
		req := httptest.NewRequest(http.MethodOptions, "/v1/whatever", nil)
		req.Header.Set("Origin", "https://app.example.com")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code == http.StatusNoContent {
			t.Fatalf("status = 204, want fallthrough to next (no preflight request method)")
		}
		if !probeRan {
			t.Fatalf("probe did not run, want fallthrough")
		}
	})
}

// TestCORSDepsAllowDevOrigins proves New() threads Deps.AllowDevOrigins into
// the CORS layer: the same localhost origin flips between reflected and
// ignored on the public /healthz route.
func TestCORSDepsAllowDevOrigins(t *testing.T) {
	for _, allowDev := range []bool{false, true} {
		h := newHarness(t)
		h.deps.AllowDevOrigins = allowDev
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.Header.Set("Origin", "http://localhost:3000")
		rec := httptest.NewRecorder()
		h.handler().ServeHTTP(rec, req)
		got := rec.Header().Get("Access-Control-Allow-Origin")
		if allowDev && got != "http://localhost:3000" {
			t.Fatalf("AllowDevOrigins=true: ACAO = %q, want the origin reflected", got)
		}
		if !allowDev && got != "" {
			t.Fatalf("AllowDevOrigins=false: ACAO = %q, want unset", got)
		}
	}
}

// --- panic recovery --------------------------------------------------------

// panicMailService overrides ListThreads to panic, proving recoverPanics
// catches a panic raised deep inside a real driving service, not just in a
// synthetic probe handler.
type panicMailService struct {
	fakeMailService
}

func (p *panicMailService) ListThreads(ctx context.Context, userID string, q port.ThreadQuery) (domain.Page[domain.Thread], error) {
	panic("boom from service")
}

func TestRecoverPanics(t *testing.T) {
	t.Run("handler panic recovered as 500 JSON envelope", func(t *testing.T) {
		h := newHarness(t)
		panicky := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic("boom")
		})
		handler := h.server().recoverPanics(panicky)
		req := httptest.NewRequest(http.MethodGet, "/v1/whatever", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500 (body=%s)", rec.Code, rec.Body.String())
		}
		got := decodeErr(t, rec)
		if got.Code != "internal" {
			t.Fatalf("code = %q, want internal", got.Code)
		}
		if got.Message != "internal server error" {
			t.Fatalf("message = %q, want %q", got.Message, "internal server error")
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("expected a non-empty response body (connection not aborted)")
		}
	})

	t.Run("no panic passes through unchanged", func(t *testing.T) {
		h := newHarness(t)
		ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("fine"))
		})
		handler := h.server().recoverPanics(ok)
		req := httptest.NewRequest(http.MethodGet, "/v1/whatever", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if rec.Body.String() != "fine" {
			t.Fatalf("body = %q, want %q", rec.Body.String(), "fine")
		}
	})

	t.Run("http.ErrAbortHandler panic propagates unconverted", func(t *testing.T) {
		h := newHarness(t)
		abort := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic(http.ErrAbortHandler)
		})
		handler := h.server().recoverPanics(abort)
		req := httptest.NewRequest(http.MethodGet, "/v1/whatever", nil)
		rec := httptest.NewRecorder()

		defer func() {
			r := recover()
			if r != http.ErrAbortHandler {
				t.Fatalf("recovered = %v, want http.ErrAbortHandler", r)
			}
		}()
		handler.ServeHTTP(rec, req)
		t.Fatalf("expected panic to propagate, but ServeHTTP returned normally")
	})

	t.Run("panic in wired service recovered through full middleware stack", func(t *testing.T) {
		h := newHarness(t)
		h.deps.Mail = &panicMailService{}
		rec := h.authed(http.MethodGet, "/v1/mail/threads", nil)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500 (body=%s)", rec.Code, rec.Body.String())
		}
		got := decodeErr(t, rec)
		if got.Code != "internal" {
			t.Fatalf("code = %q, want internal", got.Code)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("expected a non-empty response body (connection not aborted)")
		}
	})
}

// --- decodeJSON limits -----------------------------------------------------

func TestDecodeJSONLimits(t *testing.T) {
	t.Run("malformed JSON body", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/mail/drafts", strings.NewReader("{"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "validation_failed" {
			t.Fatalf("code = %q, want validation_failed", got.Code)
		}
		if h.mail.gotCreateDraft.AccountID != "" {
			t.Fatalf("CreateDraft called unexpectedly: %+v", h.mail.gotCreateDraft)
		}
	})

	t.Run("empty body", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/mail/drafts", strings.NewReader(""))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "validation_failed" {
			t.Fatalf("code = %q, want validation_failed", got.Code)
		}
	})

	t.Run("oversized body exceeds 10MB limit", func(t *testing.T) {
		h := newHarness(t)
		body := `{"subject":"` + strings.Repeat("a", 11<<20) + `"}`
		rec := h.authed(http.MethodPost, "/v1/mail/drafts", strings.NewReader(body))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "validation_failed" {
			t.Fatalf("code = %q, want validation_failed", got.Code)
		}
		if h.mail.gotCreateDraft.AccountID != "" {
			t.Fatalf("CreateDraft called unexpectedly: %+v", h.mail.gotCreateDraft)
		}
	})

	t.Run("valid body decodes and forwards", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/mail/drafts", jsonBody(t, port.DraftInput{AccountID: "acc1", Subject: "hi"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.mail.gotCreateDraft.AccountID != "acc1" {
			t.Fatalf("gotCreateDraft.AccountID = %q, want acc1", h.mail.gotCreateDraft.AccountID)
		}
		if h.mail.gotCreateDraft.Subject != "hi" {
			t.Fatalf("gotCreateDraft.Subject = %q, want hi", h.mail.gotCreateDraft.Subject)
		}
	})
}

// --- cross-cutting sanity ---------------------------------------------------

func TestPublicRoutesReachableWithoutToken(t *testing.T) {
	t.Run("healthz", func(t *testing.T) {
		h := newHarness(t)
		rec := h.anon(http.MethodGet, "/healthz", nil)
		if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
			t.Fatalf("status=%d body=%q, want 200 \"ok\"", rec.Code, rec.Body.String())
		}
	})
	t.Run("instance", func(t *testing.T) {
		h := newHarness(t)
		rec := h.anon(http.MethodGet, "/v1/instance", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})
	t.Run("paddle webhook does not require a bearer token", func(t *testing.T) {
		h := newHarness(t)
		rec := h.anon(http.MethodPost, "/v1/webhooks/paddle", strings.NewReader("{}"))
		if rec.Code == http.StatusUnauthorized {
			t.Fatalf("status = 401, want the webhook route reachable without auth")
		}
	})
	t.Run("account callback does not require a bearer token", func(t *testing.T) {
		h := newHarness(t)
		rec := h.anon(http.MethodGet, "/v1/accounts/callback/google?state=st&code=cd", nil)
		if rec.Code == http.StatusUnauthorized {
			t.Fatalf("status = 401, want the callback route reachable without auth")
		}
	})
}

func TestAuthedRoutesRequireToken(t *testing.T) {
	tests := []struct {
		name   string
		method string
		target string
	}{
		{"me", http.MethodGet, "/v1/me"},
		{"mail threads", http.MethodGet, "/v1/mail/threads"},
		{"mail opens", http.MethodGet, "/v1/mail/opens"},
		{"mail send-suggestion", http.MethodGet, "/v1/mail/send-suggestion?email=a@b.com"},
		{"mail attachments search", http.MethodGet, "/v1/mail/attachments"},
		{"mail attachment content", http.MethodGet, "/v1/mail/attachments/att1/content"},
		{"mail contact", http.MethodGet, "/v1/mail/contacts/a@b.com"},
		{"mail react to message", http.MethodPost, "/v1/mail/messages/m1/reactions"},
		{"mail remove reaction", http.MethodDelete, "/v1/mail/messages/m1/reactions/%F0%9F%91%8D"},
		{"account signature", http.MethodPut, "/v1/accounts/acc1/signature"},
		{"account auto-bcc", http.MethodPut, "/v1/accounts/acc1/auto-bcc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			rec := h.anon(tt.method, tt.target, nil)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 (body=%s)", rec.Code, rec.Body.String())
			}
			if got := decodeErr(t, rec); got.Code != "unauthorized" {
				t.Fatalf("code = %q, want unauthorized", got.Code)
			}
			if h.mail.listCalls != 0 {
				t.Fatalf("ListThreads called = %d, want 0 (requireAuth should short-circuit)", h.mail.listCalls)
			}
		})
	}
}
