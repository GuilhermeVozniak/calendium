package authjwt

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// signEdDSA builds a compact EdDSA JWS from the given header and claims maps.
func signEdDSA(t *testing.T, priv ed25519.PrivateKey, header, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	signingInput := enc(header) + "." + enc(claims)
	sig := ed25519.Sign(priv, []byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// jwksServer serves a single-key OKP/Ed25519 JWKS for the given public key.
func jwksServer(t *testing.T, kid string, pub ed25519.PublicKey) *httptest.Server {
	t.Helper()
	body := map[string]any{
		"keys": []map[string]any{{
			"kty": "OKP",
			"crv": "Ed25519",
			"kid": kid,
			"x":   base64.RawURLEncoding.EncodeToString(pub),
		}},
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(body)
	}))
}

func TestVerifyEd25519(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	const (
		kid    = "test-kid"
		issuer = "https://app.calendium.com"
	)
	srv := jwksServer(t, kid, pub)
	defer srv.Close()

	v := NewVerifier(srv.URL, issuer, srv.Client())

	header := map[string]any{"alg": "EdDSA", "typ": "JWT", "kid": kid}
	claims := map[string]any{
		"sub":   "user_123",
		"email": "ada@calendium.com",
		"name":  "Ada Lovelace",
		"image": "https://cdn/av.png",
		"iss":   issuer,
		"exp":   time.Now().Add(15 * time.Minute).Unix(),
	}
	token := signEdDSA(t, priv, header, claims)

	id, err := v.Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if id.Subject != "user_123" {
		t.Errorf("Subject = %q, want user_123", id.Subject)
	}
	if id.Email != "ada@calendium.com" {
		t.Errorf("Email = %q", id.Email)
	}
	if id.Name != "Ada Lovelace" {
		t.Errorf("Name = %q", id.Name)
	}
	if id.AvatarURL != "https://cdn/av.png" {
		t.Errorf("AvatarURL = %q", id.AvatarURL)
	}
}

func TestVerifyRejectsWrongKey(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)       // key published in JWKS
	_, otherPriv, _ := ed25519.GenerateKey(nil) // attacker signs with a different key
	const (
		kid    = "test-kid"
		issuer = "https://app.calendium.com"
	)
	srv := jwksServer(t, kid, pub)
	defer srv.Close()

	v := NewVerifier(srv.URL, issuer, srv.Client())

	header := map[string]any{"alg": "EdDSA", "kid": kid}
	claims := map[string]any{"sub": "u", "iss": issuer, "exp": time.Now().Add(time.Minute).Unix()}
	token := signEdDSA(t, otherPriv, header, claims)

	if _, err := v.Verify(context.Background(), token); err == nil {
		t.Fatal("expected wrong-key token to be rejected")
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	const (
		kid    = "test-kid"
		issuer = "https://app.calendium.com"
	)
	srv := jwksServer(t, kid, pub)
	defer srv.Close()

	v := NewVerifier(srv.URL, issuer, srv.Client())

	header := map[string]any{"alg": "EdDSA", "kid": kid}
	claims := map[string]any{
		"sub": "u",
		"iss": issuer,
		"exp": time.Now().Add(-10 * time.Minute).Unix(), // beyond clock skew
	}
	token := signEdDSA(t, priv, header, claims)

	if _, err := v.Verify(context.Background(), token); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expired error, got %v", err)
	}
}

func TestVerifyRejectsWrongIssuer(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	const kid = "test-kid"
	srv := jwksServer(t, kid, pub)
	defer srv.Close()

	v := NewVerifier(srv.URL, "https://app.calendium.com", srv.Client())

	header := map[string]any{"alg": "EdDSA", "kid": kid}
	claims := map[string]any{
		"sub": "u",
		"iss": "https://evil.example.com",
		"exp": time.Now().Add(time.Minute).Unix(),
	}
	token := signEdDSA(t, priv, header, claims)

	if _, err := v.Verify(context.Background(), token); err == nil || !strings.Contains(err.Error(), "issuer") {
		t.Fatalf("expected issuer error, got %v", err)
	}
}

// --- RS256 / ES256 via JWKS ---------------------------------------------------

// countingJWKS serves a fixed JWKS body and counts every GET, so tests can
// assert the verifier caches keys across Verify calls.
type countingJWKS struct {
	*httptest.Server
	hits int32
}

func (c *countingJWKS) count() int { return int(atomic.LoadInt32(&c.hits)) }

func newCountingJWKS(t *testing.T, keys ...map[string]any) *countingJWKS {
	t.Helper()
	body := map[string]any{"keys": keys}
	c := &countingJWKS{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&c.hits, 1)
		_ = json.NewEncoder(w).Encode(body)
	}))
	return c
}

func rsaJWK(kid string, pub *rsa.PublicKey) map[string]any {
	return map[string]any{
		"kty": "RSA", "kid": kid,
		"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

func ecJWK(kid string, pub *ecdsa.PublicKey) map[string]any {
	x, y := make([]byte, 32), make([]byte, 32)
	pub.X.FillBytes(x)
	pub.Y.FillBytes(y)
	return map[string]any{
		"kty": "EC", "crv": "P-256", "kid": kid,
		"x": base64.RawURLEncoding.EncodeToString(x),
		"y": base64.RawURLEncoding.EncodeToString(y),
	}
}

func encSeg(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func signRS256(t *testing.T, priv *rsa.PrivateKey, header, claims map[string]any) string {
	t.Helper()
	input := encSeg(t, header) + "." + encSeg(t, claims)
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("rsa sign: %v", err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func signES256(t *testing.T, priv *ecdsa.PrivateKey, header, claims map[string]any) string {
	t.Helper()
	input := encSeg(t, header) + "." + encSeg(t, claims)
	sum := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, priv, sum[:])
	if err != nil {
		t.Fatalf("ecdsa sign: %v", err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestVerifyRS256AndCaches(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const kid, issuer = "rsa-kid", "https://app.calendium.com"

	srv := newCountingJWKS(t, rsaJWK(kid, &priv.PublicKey))
	defer srv.Close()

	v := NewVerifier(srv.URL, issuer, srv.Client())

	mint := func() string {
		return signRS256(t, priv,
			map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid},
			map[string]any{"sub": "user_rsa", "email": "ada@calendium.com",
				"iss": issuer, "exp": time.Now().Add(15 * time.Minute).Unix()},
		)
	}

	id, err := v.Verify(context.Background(), mint())
	if err != nil {
		t.Fatalf("first Verify: %v", err)
	}
	if id.Subject != "user_rsa" || id.Email != "ada@calendium.com" {
		t.Fatalf("identity = %+v", id)
	}

	// Second Verify with a fresh token but same kid must hit the cache.
	if _, err := v.Verify(context.Background(), mint()); err != nil {
		t.Fatalf("second Verify: %v", err)
	}
	if got := srv.count(); got != 1 {
		t.Fatalf("JWKS fetched %d times, want exactly 1 (second Verify must not refetch)", got)
	}
}

func TestVerifyES256(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const kid, issuer = "ec-kid", "https://app.calendium.com"
	srv := newCountingJWKS(t, ecJWK(kid, &priv.PublicKey))
	defer srv.Close()

	v := NewVerifier(srv.URL, issuer, srv.Client())
	token := signES256(t, priv,
		map[string]any{"alg": "ES256", "kid": kid},
		map[string]any{"sub": "user_ec", "iss": issuer, "exp": time.Now().Add(time.Minute).Unix()})

	id, err := v.Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if id.Subject != "user_ec" {
		t.Errorf("Subject = %q, want user_ec", id.Subject)
	}
}

func TestVerifyKidSelection(t *testing.T) {
	rsaPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const issuer = "https://app.calendium.com"
	srv := newCountingJWKS(t, rsaJWK("k-rsa", &rsaPriv.PublicKey), ecJWK("k-ec", &ecPriv.PublicKey))
	defer srv.Close()

	v := NewVerifier(srv.URL, issuer, srv.Client())
	exp := time.Now().Add(time.Minute).Unix()

	rs := signRS256(t, rsaPriv, map[string]any{"alg": "RS256", "kid": "k-rsa"},
		map[string]any{"sub": "u1", "iss": issuer, "exp": exp})
	es := signES256(t, ecPriv, map[string]any{"alg": "ES256", "kid": "k-ec"},
		map[string]any{"sub": "u2", "iss": issuer, "exp": exp})

	if id, err := v.Verify(context.Background(), rs); err != nil || id.Subject != "u1" {
		t.Fatalf("k-rsa/RS256: id=%+v err=%v", id, err)
	}
	if id, err := v.Verify(context.Background(), es); err != nil || id.Subject != "u2" {
		t.Fatalf("k-ec/ES256: id=%+v err=%v", id, err)
	}
	if got := srv.count(); got != 1 {
		t.Fatalf("JWKS fetched %d times, want 1 (both kids in one key set)", got)
	}
}

// TestVerifyRejectsAlgConfusion guards the classic JWT algorithm-confusion
// bypass: Verify dispatches on the untrusted header `alg` field, so it must
// reject both an unsigned "none" token and an "HS256" token forged by
// HMAC-signing with the (published, therefore attacker-known) JWKS public
// key bytes as the shared secret. Verifier only implements EdDSA/RS256/ES256
// (verifier.go's verifySignature default case), so both must fall through
// to the unsupported-alg error rather than being accepted.
func TestVerifyRejectsAlgConfusion(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	const (
		kid    = "confusion-kid"
		issuer = "https://app.calendium.com"
	)
	srv := jwksServer(t, kid, pub)
	defer srv.Close()

	v := NewVerifier(srv.URL, issuer, srv.Client())
	exp := time.Now().Add(time.Minute).Unix()

	t.Run("alg none is rejected", func(t *testing.T) {
		header := encSeg(t, map[string]any{"alg": "none", "typ": "JWT", "kid": kid})
		claims := encSeg(t, map[string]any{"sub": "user_none", "iss": issuer, "exp": exp})
		token := header + "." + claims + "." // alg=none carries an empty signature segment

		_, err := v.Verify(context.Background(), token)
		if err == nil {
			t.Fatal("expected alg=none token to be rejected")
		}
		if !strings.Contains(err.Error(), "unsupported alg") {
			t.Fatalf(`err = %v, want to contain "unsupported alg"`, err)
		}
	})

	t.Run("alg HS256 signed with the JWKS public key bytes is rejected", func(t *testing.T) {
		header := encSeg(t, map[string]any{"alg": "HS256", "typ": "JWT", "kid": kid})
		claims := encSeg(t, map[string]any{"sub": "user_hs256", "iss": issuer, "exp": exp})
		signingInput := header + "." + claims

		// Classic alg-confusion bypass: treat the published Ed25519 public key
		// bytes as an HMAC-SHA256 shared secret.
		mac := hmac.New(sha256.New, pub)
		mac.Write([]byte(signingInput))
		sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
		token := signingInput + "." + sig

		_, err := v.Verify(context.Background(), token)
		if err == nil {
			t.Fatal("expected HS256 alg-confusion token to be rejected")
		}
		if !strings.Contains(err.Error(), "unsupported alg") {
			t.Fatalf(`err = %v, want to contain "unsupported alg"`, err)
		}
	})
}

func TestVerifyRS256Rejects(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const kid, issuer = "rsa-kid", "https://app.calendium.com"
	srv := newCountingJWKS(t, rsaJWK(kid, &priv.PublicKey))
	defer srv.Close()
	v := NewVerifier(srv.URL, issuer, srv.Client())
	now := time.Now()

	tests := []struct {
		name         string
		header       map[string]any
		claims       map[string]any
		wantContains string
	}{
		{"expired", map[string]any{"alg": "RS256", "kid": kid},
			map[string]any{"sub": "u", "iss": issuer, "exp": now.Add(-10 * time.Minute).Unix()}, "expired"},
		{"wrong issuer", map[string]any{"alg": "RS256", "kid": kid},
			map[string]any{"sub": "u", "iss": "https://evil.example", "exp": now.Add(time.Minute).Unix()}, "issuer"},
		{"unknown kid", map[string]any{"alg": "RS256", "kid": "no-such-kid"},
			map[string]any{"sub": "u", "iss": issuer, "exp": now.Add(time.Minute).Unix()}, "no JWK with kid"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			token := signRS256(t, priv, tc.header, tc.claims)
			_, err := v.Verify(context.Background(), token)
			if err == nil || !strings.Contains(err.Error(), tc.wantContains) {
				t.Fatalf("err = %v, want contains %q", err, tc.wantContains)
			}
		})
	}
}

// TestColdCacheJWKSFailureIsUpstream: no cached keys + unreachable JWKS
// must surface as domain.ErrUpstream (502), not as an invalid token (401).
func TestColdCacheJWKSFailureIsUpstream(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	const kid, issuer = "cold-kid", "https://app.calendium.com"
	srv := jwksServer(t, kid, pub)
	client := srv.Client()
	url := srv.URL
	srv.Close() // nothing is listening: every fetch fails

	v := NewVerifier(url, issuer, client)
	token := signEdDSA(t, priv,
		map[string]any{"alg": "EdDSA", "kid": kid},
		map[string]any{"sub": "u1", "iss": issuer, "exp": time.Now().Add(time.Minute).Unix()})
	_, err := v.Verify(context.Background(), token)
	if !errors.Is(err, domain.ErrUpstream) {
		t.Fatalf("err = %v, want wrap of domain.ErrUpstream", err)
	}
}

// TestWarmCacheUnknownKidStaysUnauthorized: with keys cached, an unknown
// kid during an outage is still a bad token (no 502 oracle for forgeries).
func TestWarmCacheUnknownKidStaysUnauthorized(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	_, otherPriv, _ := ed25519.GenerateKey(nil)
	const kid, issuer = "warm-kid", "https://app.calendium.com"
	srv := jwksServer(t, kid, pub)
	v := NewVerifier(srv.URL, issuer, srv.Client())
	if _, err := v.lookupKey(context.Background(), kid); err != nil {
		t.Fatalf("warm-up: %v", err)
	}
	srv.Close()
	v.mu.Lock()
	v.fetchedAt = time.Now().Add(-jwksTTL - time.Minute) // force a refetch attempt
	v.mu.Unlock()
	token := signEdDSA(t, otherPriv,
		map[string]any{"alg": "EdDSA", "kid": "forged-kid"},
		map[string]any{"sub": "u1", "iss": issuer, "exp": time.Now().Add(time.Minute).Unix()})
	_, err := v.Verify(context.Background(), token)
	if err == nil || errors.Is(err, domain.ErrUpstream) {
		t.Fatalf("err = %v, want a non-upstream rejection", err)
	}
}
