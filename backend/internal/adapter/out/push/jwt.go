package push

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
)

// Minimal JWS helpers shared by the three transports (APNs ES256 provider
// tokens, FCM RS256 service-account assertions, Web Push VAPID ES256).

func b64url(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// signingInput renders header/claims as base64url(json).base64url(json).
func signingInput(header, claims any) (string, error) {
	h, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("push: encode jwt header: %w", err)
	}
	c, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("push: encode jwt claims: %w", err)
	}
	return b64url(h) + "." + b64url(c), nil
}

// signES256 produces a JWS with a raw r||s (64-byte) ECDSA P-256 signature.
func signES256(header, claims any, key *ecdsa.PrivateKey) (string, error) {
	input, err := signingInput(header, claims)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return "", fmt.Errorf("push: es256 sign: %w", err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return input + "." + b64url(sig), nil
}

// signRS256 produces a JWS with an RSASSA-PKCS1-v1_5 SHA-256 signature.
func signRS256(header, claims any, key *rsa.PrivateKey) (string, error) {
	input, err := signingInput(header, claims)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("push: rs256 sign: %w", err)
	}
	return input + "." + b64url(sig), nil
}

// parseECPrivateKeyPEM reads a PEM-encoded EC private key (PKCS#8 as in
// Apple .p8 files, or SEC 1).
func parseECPrivateKeyPEM(pemData string) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, fmt.Errorf("push: no PEM block in EC private key")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		ec, ok := key.(*ecdsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("push: PKCS#8 key is %T, want *ecdsa.PrivateKey", key)
		}
		return ec, nil
	}
	return x509.ParseECPrivateKey(block.Bytes)
}

// parseRSAPrivateKeyPEM reads a PEM-encoded RSA private key (PKCS#8 as in
// Google service-account JSON, or PKCS#1).
func parseRSAPrivateKeyPEM(pemData string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, fmt.Errorf("push: no PEM block in RSA private key")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("push: PKCS#8 key is %T, want *rsa.PrivateKey", key)
		}
		return rsaKey, nil
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}
