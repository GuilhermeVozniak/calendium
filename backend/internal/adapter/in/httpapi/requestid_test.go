package httpapi

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

var ulidRe = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

func TestNewRequestIDFormat(t *testing.T) {
	// ULID spec vector: ms 1469918176385 encodes to the time prefix 01ARYZ6S41.
	id := newRequestID(time.UnixMilli(1469918176385))
	if !ulidRe.MatchString(id) {
		t.Fatalf("id %q is not 26 Crockford base32 chars", id)
	}
	if !strings.HasPrefix(id, "01ARYZ6S41") {
		t.Fatalf("id %q time prefix, want 01ARYZ6S41", id)
	}
	if other := newRequestID(time.UnixMilli(1469918176385)); other == id {
		t.Fatal("two ids with the same timestamp must differ (random tail)")
	}
	if zero := newRequestID(time.UnixMilli(0)); !strings.HasPrefix(zero, "0000000000") {
		t.Fatalf("epoch id %q should start with ten zeros", zero)
	}
}

func TestEncodeULIDAllOnes(t *testing.T) {
	var b [16]byte
	for i := range b {
		b[i] = 0xff
	}
	// 128 bits of ones: the leading symbol carries 3 bits (7), the rest 31 (Z).
	if got, want := encodeULID(b), "7"+strings.Repeat("Z", 25); got != want {
		t.Fatalf("encodeULID(ones) = %q, want %q", got, want)
	}
}

func TestRequestIDGeneratedAndEchoed(t *testing.T) {
	h := newHarness(t)
	rec := h.anon(http.MethodGet, "/healthz", nil)
	got := rec.Header().Get("X-Request-Id")
	if !ulidRe.MatchString(got) {
		t.Fatalf("X-Request-Id = %q, want a generated ULID", got)
	}
}

func TestRequestIDInboundOnlyFromTrustedPeer(t *testing.T) {
	const inbound = "edge-7f3a9c2e-0001"
	cases := []struct {
		name       string
		trust      bool
		remoteAddr string
		wantEcho   bool
	}{
		{"untrusted peer is ignored", false, "10.0.0.2:1", false},
		{"trusted peer is honoured", true, "10.0.0.2:1", true},
		{"trust on but peer outside CIDRs", true, "203.0.113.5:1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.deps.TrustProxy = tc.trust
			h.deps.TrustedProxyCIDRs = prefixes(t, "10.0.0.0/8")
			req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
			req.RemoteAddr = tc.remoteAddr
			req.Header.Set("X-Request-Id", inbound)
			rec := httptest.NewRecorder()
			h.handler().ServeHTTP(rec, req)
			got := rec.Header().Get("X-Request-Id")
			if tc.wantEcho && got != inbound {
				t.Fatalf("X-Request-Id = %q, want inbound %q", got, inbound)
			}
			if !tc.wantEcho && (got == inbound || !ulidRe.MatchString(got)) {
				t.Fatalf("X-Request-Id = %q, want a fresh ULID", got)
			}
		})
	}
}

// Review Focus 4: a malformed inbound id from a trusted proxy is replaced and
// never reaches the response header or the log line.
func TestRequestIDRejectsMalformedInbound(t *testing.T) {
	for _, bad := range []string{"has spaces here", strings.Repeat("a", 200), "short", "tab\there", "x\x00y12345", "line\nbreak-injected"} {
		var buf strings.Builder
		h := newHarness(t)
		h.deps.Logger = jsonLogger(&buf)
		h.deps.TrustProxy = true
		h.deps.TrustedProxyCIDRs = prefixes(t, "10.0.0.0/8")
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.RemoteAddr = "10.0.0.2:1"
		req.Header["X-Request-Id"] = []string{bad}
		rec := httptest.NewRecorder()
		h.handler().ServeHTTP(rec, req)
		got := rec.Header().Get("X-Request-Id")
		if got == bad || !ulidRe.MatchString(got) {
			t.Fatalf("inbound %q: X-Request-Id = %q, want a fresh ULID", bad, got)
		}
		if strings.Contains(buf.String(), bad) {
			t.Fatalf("inbound %q reached the log: %s", bad, buf.String())
		}
	}
}

func TestErrorEnvelopeCarriesRequestID(t *testing.T) {
	h := newHarness(t)
	rec := h.anon(http.MethodGet, "/v1/me", nil) // 401 from requireAuth
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	e := decodeErr(t, rec)
	if e.RequestID == "" || e.RequestID != rec.Header().Get("X-Request-Id") {
		t.Fatalf("error.requestId = %q, header = %q; want equal and non-empty", e.RequestID, rec.Header().Get("X-Request-Id"))
	}
}

func TestInvalidTokenEnvelopeCarriesRequestID(t *testing.T) {
	h := newHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.Header.Set("Authorization", "Bearer not-a-valid-token")
	rec := httptest.NewRecorder()
	h.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if e := decodeErr(t, rec); e.RequestID == "" || e.RequestID != rec.Header().Get("X-Request-Id") {
		t.Fatalf("error.requestId = %q, header = %q", e.RequestID, rec.Header().Get("X-Request-Id"))
	}
}
