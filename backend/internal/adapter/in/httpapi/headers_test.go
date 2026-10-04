package httpapi

import (
	"bufio"
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func assertStaticHeaders(t *testing.T, h http.Header) {
	t.Helper()
	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
		"X-Frame-Options":        "DENY",
	}
	for k, v := range want {
		if got := h.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	t.Run("json route under /v1 gets no-store", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodGet, "/v1/me", nil)
		assertStaticHeaders(t, rec.Header())
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("Cache-Control = %q, want no-store", got)
		}
		if rec.Header().Get("Strict-Transport-Security") != "" {
			t.Fatal("HSTS must not be set on a plain-HTTP request")
		}
	})
	t.Run("404 outside /v1 has static headers, no cache directive", func(t *testing.T) {
		h := newHarness(t)
		rec := h.anon(http.MethodGet, "/nope", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d", rec.Code)
		}
		assertStaticHeaders(t, rec.Header())
		if rec.Header().Get("Cache-Control") != "" {
			t.Fatalf("Cache-Control = %q on /nope, want empty", rec.Header().Get("Cache-Control"))
		}
	})
	t.Run("CORS preflight carries the headers", func(t *testing.T) {
		h := newHarness(t)
		req := httptest.NewRequest(http.MethodOptions, "/v1/me", nil)
		req.Header.Set("Origin", "http://localhost:3000")
		req.Header.Set("Access-Control-Request-Method", "GET")
		rec := httptest.NewRecorder()
		h.handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
		assertStaticHeaders(t, rec.Header())
	})
	t.Run("HSTS on TLS", func(t *testing.T) {
		h := newHarness(t)
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.TLS = &tls.ConnectionState{}
		rec := httptest.NewRecorder()
		h.handler().ServeHTTP(rec, req)
		if got := rec.Header().Get("Strict-Transport-Security"); got != "max-age=31536000; includeSubDomains" {
			t.Fatalf("HSTS = %q", got)
		}
	})
	t.Run("HSTS from trusted X-Forwarded-Proto https only", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			trust bool
			peer  string
			want  bool
		}{
			{"trusted proxy", true, "10.0.0.2:1", true},
			{"untrusted peer", false, "10.0.0.2:1", false},
			{"trust on, peer outside CIDRs", true, "203.0.113.5:1", false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				h := newHarness(t)
				h.deps.TrustProxy = tc.trust
				h.deps.TrustedProxyCIDRs = prefixes(t, "10.0.0.0/8")
				req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
				req.RemoteAddr = tc.peer
				req.Header.Set("X-Forwarded-Proto", "https")
				rec := httptest.NewRecorder()
				h.handler().ServeHTTP(rec, req)
				if got := rec.Header().Get("Strict-Transport-Security") != ""; got != tc.want {
					t.Fatalf("HSTS present = %v, want %v", got, tc.want)
				}
			})
		}
	})
}

func TestSSEStreamIsNoStore(t *testing.T) {
	h := newHarness(t)
	srv := httptest.NewServer(h.handler())
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/collab/stream", nil)
	req.Header.Set("Authorization", "Bearer "+defaultToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := resp.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("X-Accel-Buffering = %q, want no", got)
	}
	assertStaticHeaders(t, resp.Header)
	line, _ := bufio.NewReader(resp.Body).ReadString('\n')
	if !strings.HasPrefix(line, ": connected") {
		t.Fatalf("first frame = %q", line)
	}
}
