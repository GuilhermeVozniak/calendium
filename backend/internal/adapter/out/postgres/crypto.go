package postgres

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

var errNoTokenKey = errors.New("postgres: token encryption key not configured")

// SetTokenEncryptionKey installs the 32-byte AES-256-GCM key (from
// config.Crypto.TokenEncryptionKey) used to encrypt provider OAuth tokens
// at rest. Ciphertexts are nonce-prefixed.
func (s *Store) SetTokenEncryptionKey(key []byte) error {
	if len(key) != 32 {
		return fmt.Errorf("postgres: token encryption key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("postgres: init AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("postgres: init GCM: %w", err)
	}
	s.aead = aead
	return nil
}

// sealToken encrypts a plaintext token; empty input maps to NULL (nil).
func (s *Store) sealToken(plain string) ([]byte, error) {
	if plain == "" {
		return nil, nil
	}
	if s.aead == nil {
		return nil, errNoTokenKey
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("postgres: nonce: %w", err)
	}
	return s.aead.Seal(nonce, nonce, []byte(plain), nil), nil
}

// openToken decrypts a nonce-prefixed ciphertext; NULL/empty maps to "".
func (s *Store) openToken(blob []byte) (string, error) {
	if len(blob) == 0 {
		return "", nil
	}
	if s.aead == nil {
		return "", errNoTokenKey
	}
	ns := s.aead.NonceSize()
	if len(blob) < ns {
		return "", errors.New("postgres: token ciphertext too short")
	}
	plain, err := s.aead.Open(nil, blob[:ns], blob[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("postgres: decrypt token: %w", err)
	}
	return string(plain), nil
}
