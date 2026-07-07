package push

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"testing"

	"calendium/backend/internal/config"
)

// TestEncryptAES128GCMRoundTrip plays the user agent side of RFC 8291:
// decrypt the record with the subscription's private key and check the
// plaintext and padding delimiter.
func TestEncryptAES128GCMRoundTrip(t *testing.T) {
	uaKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	authSecret := make([]byte, 16)
	if _, err := rand.Read(authSecret); err != nil {
		t.Fatal(err)
	}
	p256dh := base64.RawURLEncoding.EncodeToString(uaKey.PublicKey().Bytes())
	auth := base64.RawURLEncoding.EncodeToString(authSecret)

	plaintext := []byte(`{"title":"New mail","body":"hello"}`)
	out, err := encryptAES128GCM(plaintext, p256dh, auth)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Header: salt(16) || rs(4) || idlen(1) || keyid.
	salt := out[:16]
	rs := binary.BigEndian.Uint32(out[16:20])
	idlen := int(out[20])
	if rs != webPushRecordSize || idlen != 65 {
		t.Fatalf("bad header: rs=%d idlen=%d", rs, idlen)
	}
	asPubBytes := out[21 : 21+idlen]
	ciphertext := out[21+idlen:]

	asPub, err := ecdh.P256().NewPublicKey(asPubBytes)
	if err != nil {
		t.Fatalf("as_public invalid: %v", err)
	}
	secret, err := uaKey.ECDH(asPub)
	if err != nil {
		t.Fatal(err)
	}
	info := "WebPush: info\x00" + string(uaKey.PublicKey().Bytes()) + string(asPubBytes)
	ikm, err := hkdf.Key(sha256.New, secret, authSecret, info, 32)
	if err != nil {
		t.Fatal(err)
	}
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	record, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if record[len(record)-1] != 0x02 {
		t.Fatalf("missing 0x02 last-record delimiter, got %#x", record[len(record)-1])
	}
	if !bytes.Equal(record[:len(record)-1], plaintext) {
		t.Fatalf("plaintext mismatch: %q", record)
	}
}

// TestParseVAPIDKeys checks the ES256 key reconstruction from a raw scalar.
func TestParseVAPIDKeys(t *testing.T) {
	gen, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.VAPID{
		PublicKey:  base64.RawURLEncoding.EncodeToString(gen.PublicKey().Bytes()),
		PrivateKey: base64.RawURLEncoding.EncodeToString(gen.Bytes()),
	}
	key, pub, err := parseVAPIDKeys(cfg)
	if err != nil {
		t.Fatalf("parseVAPIDKeys: %v", err)
	}
	if pub != cfg.PublicKey {
		t.Fatalf("public key mismatch")
	}
	jwt, err := signES256(map[string]string{"alg": "ES256"}, map[string]any{"aud": "https://example.com"}, key)
	if err != nil || jwt == "" {
		t.Fatalf("signES256 with reconstructed key: %v", err)
	}
}
