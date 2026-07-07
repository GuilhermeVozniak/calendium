package postgres

import (
	"bytes"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
)

// newSealedStore builds a bare Store with only the AEAD installed — seal/open
// touch nothing else on Store, so a nil db is fine.
func newSealedStore(t *testing.T, key []byte) *Store {
	t.Helper()
	s := &Store{}
	if err := s.SetTokenEncryptionKey(key); err != nil {
		t.Fatalf("SetTokenEncryptionKey: %v", err)
	}
	return s
}

func randKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return k
}

func TestSetTokenEncryptionKeyLength(t *testing.T) {
	s := &Store{}
	for _, n := range []int{0, 16, 31, 33, 64} {
		if err := s.SetTokenEncryptionKey(make([]byte, n)); err == nil {
			t.Errorf("key length %d accepted, want error", n)
		}
	}
	if err := s.SetTokenEncryptionKey(make([]byte, 32)); err != nil {
		t.Errorf("32-byte key rejected: %v", err)
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	s := newSealedStore(t, randKey(t))
	for _, plain := range []string{"", "ya29.short-token", strings.Repeat("secret-", 1000)} {
		t.Run(plain[:min(len(plain), 12)], func(t *testing.T) {
			blob, err := s.sealToken(plain)
			if err != nil {
				t.Fatalf("sealToken: %v", err)
			}
			if plain == "" {
				if blob != nil {
					t.Fatalf("empty plaintext should seal to nil, got %v", blob)
				}
			} else if bytes.Equal(blob, []byte(plain)) {
				t.Fatal("ciphertext equals plaintext")
			}
			got, err := s.openToken(blob)
			if err != nil {
				t.Fatalf("openToken: %v", err)
			}
			if got != plain {
				t.Fatalf("round trip = %q, want %q", got, plain)
			}
		})
	}
}

// TestSealTokenNonceIsRandom: two seals of the same plaintext must differ
// (random 12-byte GCM nonce prefix).
func TestSealTokenNonceIsRandom(t *testing.T) {
	s := newSealedStore(t, randKey(t))
	a, err := s.sealToken("same-token")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.sealToken("same-token")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions of same plaintext identical; nonce not random")
	}
}

// TestOpenTokenWrongKeyFails: a ciphertext sealed under k1 must not decrypt
// under a different key (GCM auth tag mismatch).
func TestOpenTokenWrongKeyFails(t *testing.T) {
	blob, err := newSealedStore(t, randKey(t)).sealToken("provider-refresh-token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newSealedStore(t, randKey(t)).openToken(blob); err == nil {
		t.Fatal("decrypt under wrong key succeeded, want auth failure")
	}
}

func TestSealOpenNoKey(t *testing.T) {
	s := &Store{} // no AEAD installed
	if _, err := s.sealToken("x"); !errors.Is(err, errNoTokenKey) {
		t.Fatalf("sealToken err = %v, want errNoTokenKey", err)
	}
	if _, err := s.openToken([]byte("nonempty")); !errors.Is(err, errNoTokenKey) {
		t.Fatalf("openToken err = %v, want errNoTokenKey", err)
	}
	// Empty inputs are the NULL mapping and never touch the AEAD.
	if b, err := s.sealToken(""); err != nil || b != nil {
		t.Fatalf("sealToken(\"\") = %v, %v", b, err)
	}
	if v, err := s.openToken(nil); err != nil || v != "" {
		t.Fatalf("openToken(nil) = %q, %v", v, err)
	}
}

func TestOpenTokenTooShort(t *testing.T) {
	s := newSealedStore(t, randKey(t))
	if _, err := s.openToken([]byte{1, 2, 3}); err == nil {
		t.Fatal("ciphertext shorter than nonce accepted")
	}
}
