// Package authjwt verifies Better Auth access tokens locally with the Go
// standard library only: EdDSA (Ed25519, Better Auth's default),
// RS256, and ES256, all against a cached JWKS fetch. It implements
// port.TokenVerifier and is a pure resource-server verifier — it never mints
// tokens (Better Auth, hosted by the Next.js web app, does that).
package authjwt

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"calendium/backend/internal/port"
)

const (
	// clockSkew is the leeway applied to exp/nbf checks.
	clockSkew = 60 * time.Second
	// jwksTTL is how long a fetched key set is trusted before refresh.
	jwksTTL = 15 * time.Minute
	// jwksRefetchMin throttles refetches triggered by unknown kids.
	jwksRefetchMin = 30 * time.Second
	// jwksFetchTimeout bounds a single JWKS HTTP fetch independently of the
	// shared http.Client timeout, so a hung endpoint fails fast.
	jwksFetchTimeout = 10 * time.Second
)

// Verifier verifies Better Auth JWTs against the JWKS published at
// ${BETTER_AUTH_URL}/api/auth/jwks. It supports the asymmetric algorithms
// Better Auth can issue: EdDSA/Ed25519 (default), RS256, and ES256.
type Verifier struct {
	jwksURL string
	issuer  string
	hc      *http.Client

	mu        sync.Mutex
	keys      map[string]crypto.PublicKey
	fetchedAt time.Time

	// fetchMu serializes JWKS refetches and is held across the network fetch,
	// so the verify hot-path mutex mu never is.
	fetchMu sync.Mutex
}

var _ port.TokenVerifier = (*Verifier)(nil)

// NewVerifier builds a Verifier; hc defaults to http.DefaultClient. jwksURL is
// the Better Auth JWKS endpoint (AUTH_JWKS_URL). issuer is the expected token
// `iss` claim (AUTH_ISSUER = BETTER_AUTH_URL); "" disables issuer pinning.
func NewVerifier(jwksURL, issuer string, hc *http.Client) *Verifier {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Verifier{
		jwksURL: jwksURL,
		issuer:  issuer,
		hc:      hc,
		keys:    map[string]crypto.PublicKey{},
	}
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

// jwtClaims mirrors the Better Auth JWT payload: standard sub/iss/exp plus the
// user fields the default payload carries (email/name/image). Apple omits the
// email claim after the first authorization, so it may be empty.
type jwtClaims struct {
	Sub     string `json:"sub"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	Image   string `json:"image"`
	Picture string `json:"picture"`
	Exp     int64  `json:"exp"`
	Nbf     int64  `json:"nbf"`
	Iss     string `json:"iss"`
}

// Verify implements port.TokenVerifier.
func (v *Verifier) Verify(ctx context.Context, token string) (port.Identity, error) {
	var zero port.Identity

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return zero, errors.New("authjwt: token is not a compact JWS")
	}
	headerRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return zero, fmt.Errorf("authjwt: decode header: %w", err)
	}
	claimsRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return zero, fmt.Errorf("authjwt: decode claims: %w", err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return zero, fmt.Errorf("authjwt: decode signature: %w", err)
	}

	var hdr jwtHeader
	if err := json.Unmarshal(headerRaw, &hdr); err != nil {
		return zero, fmt.Errorf("authjwt: parse header: %w", err)
	}

	signingInput := token[:len(parts[0])+1+len(parts[1])]
	if err := v.verifySignature(ctx, hdr, signingInput, sig); err != nil {
		return zero, err
	}

	var claims jwtClaims
	if err := json.Unmarshal(claimsRaw, &claims); err != nil {
		return zero, fmt.Errorf("authjwt: parse claims: %w", err)
	}
	if err := validateClaims(claims, v.issuer, time.Now()); err != nil {
		return zero, err
	}

	return identityFrom(claims), nil
}

func (v *Verifier) verifySignature(ctx context.Context, hdr jwtHeader, signingInput string, sig []byte) error {
	switch hdr.Alg {
	case "EdDSA":
		key, err := v.lookupKey(ctx, hdr.Kid)
		if err != nil {
			return err
		}
		pub, ok := key.(ed25519.PublicKey)
		if !ok {
			return fmt.Errorf("authjwt: JWK %q is not an Ed25519 key", hdr.Kid)
		}
		// Ed25519 signs the message directly (no pre-hash).
		if !ed25519.Verify(pub, []byte(signingInput), sig) {
			return errors.New("authjwt: invalid EdDSA signature")
		}
		return nil

	case "RS256":
		key, err := v.lookupKey(ctx, hdr.Kid)
		if err != nil {
			return err
		}
		pub, ok := key.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("authjwt: JWK %q is not an RSA key", hdr.Kid)
		}
		digest := sha256.Sum256([]byte(signingInput))
		if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
			return errors.New("authjwt: invalid RS256 signature")
		}
		return nil

	case "ES256":
		key, err := v.lookupKey(ctx, hdr.Kid)
		if err != nil {
			return err
		}
		pub, ok := key.(*ecdsa.PublicKey)
		if !ok {
			return fmt.Errorf("authjwt: JWK %q is not an EC key", hdr.Kid)
		}
		if len(sig) != 64 {
			return errors.New("authjwt: malformed ES256 signature")
		}
		digest := sha256.Sum256([]byte(signingInput))
		r := new(big.Int).SetBytes(sig[:32])
		s := new(big.Int).SetBytes(sig[32:])
		if !ecdsa.Verify(pub, digest[:], r, s) {
			return errors.New("authjwt: invalid ES256 signature")
		}
		return nil

	default:
		return fmt.Errorf("authjwt: unsupported alg %q", hdr.Alg)
	}
}

// validateClaims checks exp/nbf strictly (with skew) and pins the issuer to
// expectedIssuer when configured. Audience is treated leniently and not
// enforced (Better Auth may or may not set aud depending on jwt() config).
func validateClaims(c jwtClaims, expectedIssuer string, now time.Time) error {
	if c.Exp == 0 {
		return errors.New("authjwt: token has no exp claim")
	}
	if now.After(time.Unix(c.Exp, 0).Add(clockSkew)) {
		return errors.New("authjwt: token expired")
	}
	if c.Nbf != 0 && now.Add(clockSkew).Before(time.Unix(c.Nbf, 0)) {
		return errors.New("authjwt: token not yet valid")
	}
	if expectedIssuer != "" && c.Iss != expectedIssuer {
		return fmt.Errorf("authjwt: unexpected issuer %q", c.Iss)
	}
	if c.Sub == "" {
		return errors.New("authjwt: token has no sub claim")
	}
	return nil
}

func identityFrom(c jwtClaims) port.Identity {
	avatar := c.Image
	if avatar == "" {
		avatar = c.Picture
	}
	return port.Identity{
		Subject:   c.Sub,
		Email:     c.Email,
		Name:      c.Name,
		AvatarURL: avatar,
	}
}

// --- JWKS --------------------------------------------------------------------

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Crv string `json:"crv"`
	N   string `json:"n"`
	E   string `json:"e"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type jwks struct {
	Keys []jwk `json:"keys"`
}

// lookupKey returns the cached public key for kid, refetching the JWKS when
// the cache is stale or the kid is unknown (throttled). The network fetch runs
// outside v.mu so a slow/hung JWKS endpoint cannot stall verification of tokens
// whose keys are already cached.
func (v *Verifier) lookupKey(ctx context.Context, kid string) (crypto.PublicKey, error) {
	if v.jwksURL == "" {
		return nil, errors.New("authjwt: asymmetric token but no JWKS URL configured")
	}

	v.mu.Lock()
	k, have := v.keys[kid]
	fresh := time.Since(v.fetchedAt) < jwksTTL
	shouldFetch := len(v.keys) == 0 || time.Since(v.fetchedAt) >= jwksRefetchMin
	v.mu.Unlock()

	if have && fresh {
		return k, nil
	}
	if !shouldFetch {
		if have {
			return k, nil // cached; refetch throttled, serve the existing key
		}
		return nil, fmt.Errorf("authjwt: no JWK with kid %q", kid)
	}

	if err := v.refresh(ctx); err != nil {
		// Stale fallback: keep serving a matching cached key through a transient
		// JWKS outage rather than locking everyone out.
		v.mu.Lock()
		k, have := v.keys[kid]
		v.mu.Unlock()
		if have {
			return k, nil
		}
		return nil, err
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	if k, ok := v.keys[kid]; ok {
		return k, nil
	}
	if kid == "" && len(v.keys) == 1 {
		for _, k := range v.keys {
			return k, nil
		}
	}
	return nil, fmt.Errorf("authjwt: no JWK with kid %q", kid)
}

// refresh fetches the JWKS and swaps in the new key set. Callers serialize on
// fetchMu and double-check freshness, so a burst of cache misses triggers a
// single network fetch; v.mu is only held for the brief snapshot and store,
// never across the HTTP call.
func (v *Verifier) refresh(ctx context.Context) error {
	v.fetchMu.Lock()
	defer v.fetchMu.Unlock()

	v.mu.Lock()
	recent := len(v.keys) > 0 && time.Since(v.fetchedAt) < jwksRefetchMin
	v.mu.Unlock()
	if recent {
		return nil // another goroutine refreshed while we waited on fetchMu
	}

	fetchCtx, cancel := context.WithTimeout(ctx, jwksFetchTimeout)
	defer cancel()
	keys, err := v.fetch(fetchCtx)
	if err != nil {
		return err
	}

	v.mu.Lock()
	v.keys = keys
	v.fetchedAt = time.Now()
	v.mu.Unlock()
	return nil
}

// fetch performs the JWKS HTTP GET and parses it into a key set. It holds no
// verifier mutex.
func (v *Verifier) fetch(ctx context.Context) (map[string]crypto.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return nil, fmt.Errorf("authjwt: build JWKS request: %w", err)
	}
	res, err := v.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("authjwt: fetch JWKS: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("authjwt: JWKS endpoint returned %s", res.Status)
	}
	var set jwks
	if err := json.NewDecoder(res.Body).Decode(&set); err != nil {
		return nil, fmt.Errorf("authjwt: parse JWKS: %w", err)
	}

	keys := make(map[string]crypto.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		pub, err := k.publicKey()
		if err != nil {
			continue // skip unusable entries; others may still verify
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		return nil, errors.New("authjwt: JWKS contained no usable keys")
	}
	return keys, nil
}

func (k jwk) publicKey() (crypto.PublicKey, error) {
	switch k.Kty {
	case "OKP":
		// Better Auth's default: Ed25519 signing keys (kty=OKP, crv=Ed25519).
		if k.Crv != "Ed25519" {
			return nil, fmt.Errorf("authjwt: unsupported OKP curve %q", k.Crv)
		}
		x, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return nil, fmt.Errorf("authjwt: decode OKP x: %w", err)
		}
		if len(x) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("authjwt: Ed25519 key has wrong size %d", len(x))
		}
		return ed25519.PublicKey(x), nil
	case "RSA":
		n, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			return nil, fmt.Errorf("authjwt: decode RSA modulus: %w", err)
		}
		e, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			return nil, fmt.Errorf("authjwt: decode RSA exponent: %w", err)
		}
		return &rsa.PublicKey{
			N: new(big.Int).SetBytes(n),
			E: int(new(big.Int).SetBytes(e).Int64()),
		}, nil
	case "EC":
		if k.Crv != "P-256" {
			return nil, fmt.Errorf("authjwt: unsupported EC curve %q", k.Crv)
		}
		x, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return nil, fmt.Errorf("authjwt: decode EC x: %w", err)
		}
		y, err := base64.RawURLEncoding.DecodeString(k.Y)
		if err != nil {
			return nil, fmt.Errorf("authjwt: decode EC y: %w", err)
		}
		return &ecdsa.PublicKey{
			Curve: elliptic.P256(),
			X:     new(big.Int).SetBytes(x),
			Y:     new(big.Int).SetBytes(y),
		}, nil
	default:
		return nil, fmt.Errorf("authjwt: unsupported kty %q", k.Kty)
	}
}
