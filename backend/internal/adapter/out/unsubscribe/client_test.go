package unsubscribe

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"
)

// allowLoopbackDialsForTest swaps the package-level dial guard for a no-op so
// tests can exercise HTTP-level behavior (redirects, status codes, body
// framing) against httptest servers, which always listen on loopback
// addresses the production guard must reject. Restored via t.Cleanup so
// production behavior is never left altered.
func allowLoopbackDialsForTest(t *testing.T) {
	t.Helper()
	prev := dialGuard
	dialGuard = func(_, _ string, _ syscall.RawConn) error { return nil }
	t.Cleanup(func() { dialGuard = prev })
}

func TestPostOneClickSendsRFC8058Body(t *testing.T) {
	allowLoopbackDialsForTest(t)
	var gotBody, gotContentType, gotMethod string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotContentType, gotMethod = string(b), r.Header.Get("Content-Type"), r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New()
	c.HTTP.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test-only: self-signed httptest cert
	if err := c.PostOneClick(context.Background(), srv.URL+"/u?id=1"); err != nil {
		t.Fatalf("PostOneClick: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotContentType != "application/x-www-form-urlencoded" {
		t.Fatalf("content-type = %q", gotContentType)
	}
	if gotBody != "List-Unsubscribe=One-Click" {
		t.Fatalf("body = %q", gotBody)
	}
}

func TestPostOneClickRejectsBadTargets(t *testing.T) {
	if err := New().PostOneClick(context.Background(), "mailto:x@y.z"); err == nil {
		t.Fatal("non-http url accepted")
	}
	allowLoopbackDialsForTest(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	c := New()
	c.HTTP.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test-only: self-signed httptest cert
	if err := c.PostOneClick(context.Background(), srv.URL); err == nil {
		t.Fatal("non-2xx status accepted")
	}
}

func TestPostOneClickRejectsNonHTTPSScheme(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer srv.Close()

	// srv.URL is http://127.0.0.1:PORT — plain http, which RFC 8058 forbids
	// for one-click POST targets. Must be rejected before any request fires.
	if err := New().PostOneClick(context.Background(), srv.URL); err == nil {
		t.Fatal("http scheme accepted, want rejection")
	}
	if hit {
		t.Fatal("server was hit despite scheme rejection")
	}
}

func TestPostOneClickDoesNotFollowRedirects(t *testing.T) {
	allowLoopbackDialsForTest(t)
	var finalHit bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final" {
			finalHit = true
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/final", http.StatusFound)
	}))
	defer srv.Close()

	c := New()
	c.HTTP.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test-only: self-signed httptest cert
	if err := c.PostOneClick(context.Background(), srv.URL+"/start"); err == nil {
		t.Fatal("3xx response accepted, want error")
	}
	if finalHit {
		t.Fatal("redirect target was hit; redirects must not be followed")
	}
}

func TestPostOneClickBlocksPrivateAndLoopbackAddresses(t *testing.T) {
	// No guard swap here: exercises the real production dial guard against a
	// loopback target. Nothing needs to actually listen on the port — the
	// guard must reject before any connection attempt.
	if err := New().PostOneClick(context.Background(), "https://127.0.0.1:1/x"); err == nil {
		t.Fatal("loopback address accepted, want rejection")
	}
}

func TestPostOneClickBlocksCGNATAndNAT64(t *testing.T) {
	for _, target := range []string{"https://100.64.0.1:1/x", "https://[64:ff9b::7f00:1]:1/x"} {
		if err := New().PostOneClick(context.Background(), target); err == nil {
			t.Fatalf("%s accepted, want rejection by the dial guard", target)
		}
	}
}

func TestBlockPrivateNetworksDelegatesToNetguard(t *testing.T) {
	for _, addr := range []string{"100.64.0.1:443", "[64:ff9b::7f00:1]:443", "224.0.0.1:443"} {
		if err := blockPrivateNetworks("tcp", addr, nil); err == nil {
			t.Errorf("blockPrivateNetworks(%q) = nil, want refusal", addr)
		}
	}
	if err := blockPrivateNetworks("tcp", "93.184.216.34:443", nil); err != nil {
		t.Errorf("public address refused: %v", err)
	}
}
