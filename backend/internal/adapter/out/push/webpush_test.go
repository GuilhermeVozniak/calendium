package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/config"
)

// generateVAPIDConfig returns a config.VAPID with a fresh P-256 keypair
// encoded as base64url (matching how VAPID_PUBLIC_KEY/VAPID_PRIVATE_KEY are
// configured in production).
func generateVAPIDConfig(t *testing.T) config.VAPID {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return config.VAPID{
		PublicKey:  b64url(key.PublicKey().Bytes()),
		PrivateKey: b64url(key.Bytes()),
	}
}

// subscriptionToken builds a browser PushSubscription JSON (the device token
// format webpush.send expects) pointed at endpoint, with a fresh UA keypair.
func subscriptionToken(t *testing.T, endpoint string) string {
	t.Helper()
	uaKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	authSecret := make([]byte, 16)
	if _, err := rand.Read(authSecret); err != nil {
		t.Fatal(err)
	}
	sub := subscription{Endpoint: endpoint}
	sub.Keys.P256dh = b64url(uaKey.PublicKey().Bytes())
	sub.Keys.Auth = b64url(authSecret)
	raw, err := json.Marshal(sub)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestWebPushSend exercises the actual Web Push delivery path end to end: a
// real VAPID keypair, a subscription pointed at an httptest server, and
// assertions on the request the server actually received.
func TestWebPushSend(t *testing.T) {
	t.Run("delivers with aes128gcm encoding, TTL, and a verifiable VAPID authorization", func(t *testing.T) {
		cfg := generateVAPIDConfig(t)

		var gotCE, gotTTL, gotAuth, gotCT string
		var gotBody []byte
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotCE = r.Header.Get("Content-Encoding")
			gotTTL = r.Header.Get("TTL")
			gotAuth = r.Header.Get("Authorization")
			gotCT = r.Header.Get("Content-Type")
			gotBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated) // push services ack with 2xx
		}))
		defer srv.Close()

		sender := newWebPushSender(cfg, srv.Client())
		token := subscriptionToken(t, srv.URL)

		if err := sender.send(context.Background(), token, "New mail", "from Ada", map[string]string{"threadId": "t-1"}); err != nil {
			t.Fatalf("send: %v", err)
		}

		if gotCE != "aes128gcm" {
			t.Errorf("Content-Encoding = %q, want aes128gcm", gotCE)
		}
		if gotTTL != webPushTTL {
			t.Errorf("TTL = %q, want %q", gotTTL, webPushTTL)
		}
		if gotCT != "application/octet-stream" {
			t.Errorf("Content-Type = %q, want application/octet-stream", gotCT)
		}
		if len(gotBody) == 0 {
			t.Fatal("empty request body")
		}

		if !strings.HasPrefix(gotAuth, "vapid t=") {
			t.Fatalf("Authorization = %q, want prefix %q", gotAuth, "vapid t=")
		}
		jwtPart, kPart, ok := strings.Cut(strings.TrimPrefix(gotAuth, "vapid t="), ", k=")
		if !ok {
			t.Fatalf("Authorization = %q, want %q", gotAuth, "vapid t=<jwt>, k=<pub>")
		}
		if kPart != trimPad(cfg.PublicKey) {
			t.Errorf("k = %q, want %q", kPart, trimPad(cfg.PublicKey))
		}

		jwtParts := strings.Split(jwtPart, ".")
		if len(jwtParts) != 3 {
			t.Fatalf("t= is not a compact JWS: %q", jwtPart)
		}
		hb, err := base64.RawURLEncoding.DecodeString(jwtParts[0])
		if err != nil {
			t.Fatalf("decode header: %v", err)
		}
		var hdr struct{ Alg, Typ string }
		if err := json.Unmarshal(hb, &hdr); err != nil {
			t.Fatalf("unmarshal header: %v", err)
		}
		if hdr.Alg != "ES256" {
			t.Errorf("alg = %q, want ES256", hdr.Alg)
		}

		cb, err := base64.RawURLEncoding.DecodeString(jwtParts[1])
		if err != nil {
			t.Fatalf("decode claims: %v", err)
		}
		var claims struct {
			Aud string `json:"aud"`
			Sub string `json:"sub"`
			Exp int64  `json:"exp"`
		}
		if err := json.Unmarshal(cb, &claims); err != nil {
			t.Fatalf("unmarshal claims: %v", err)
		}
		if claims.Aud != srv.URL {
			t.Errorf("aud = %q, want %q", claims.Aud, srv.URL)
		}
		if claims.Sub != vapidSubject {
			t.Errorf("sub = %q, want %q", claims.Sub, vapidSubject)
		}
		if claims.Exp <= time.Now().Unix() {
			t.Errorf("exp = %d, want a future unix timestamp", claims.Exp)
		}

		sig, err := base64.RawURLEncoding.DecodeString(jwtParts[2])
		if err != nil {
			t.Fatalf("decode signature: %v", err)
		}
		if len(sig) != 64 {
			t.Fatalf("signature = %d bytes, want 64 (raw r||s)", len(sig))
		}
		digest := sha256.Sum256([]byte(jwtParts[0] + "." + jwtParts[1]))
		r := new(big.Int).SetBytes(sig[:32])
		s := new(big.Int).SetBytes(sig[32:])
		signKey, pubB64, err := parseVAPIDKeys(cfg)
		if err != nil {
			t.Fatalf("parseVAPIDKeys: %v", err)
		}
		if pubB64 != trimPad(cfg.PublicKey) {
			t.Fatalf("parseVAPIDKeys pub = %q, want %q", pubB64, trimPad(cfg.PublicKey))
		}
		if !ecdsa.Verify(&signKey.PublicKey, digest[:], r, s) {
			t.Fatal("VAPID JWT signature does not verify against the VAPID public key (k=)")
		}
	})

	t.Run("non-2xx response is an error", func(t *testing.T) {
		cfg := generateVAPIDConfig(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusGone)
			_, _ = w.Write([]byte("subscription expired"))
		}))
		defer srv.Close()

		sender := newWebPushSender(cfg, srv.Client())
		token := subscriptionToken(t, srv.URL)

		err := sender.send(context.Background(), token, "t", "b", nil)
		if err == nil {
			t.Fatal("want error")
		}
		if !strings.Contains(err.Error(), "web push http 410") {
			t.Fatalf("err = %v, want to contain %q", err, "web push http 410")
		}
	})

	t.Run("malformed subscription JSON is an error", func(t *testing.T) {
		sender := newWebPushSender(config.VAPID{}, http.DefaultClient)
		err := sender.send(context.Background(), "not-json", "t", "b", nil)
		if err == nil || !strings.Contains(err.Error(), "parse web push subscription") {
			t.Fatalf("err = %v, want to contain %q", err, "parse web push subscription")
		}
	})

	t.Run("subscription missing endpoint or keys is an error", func(t *testing.T) {
		sender := newWebPushSender(config.VAPID{}, http.DefaultClient)
		err := sender.send(context.Background(), `{"endpoint":"","keys":{"p256dh":"","auth":""}}`, "t", "b", nil)
		if err == nil || !strings.Contains(err.Error(), "missing endpoint or keys") {
			t.Fatalf("err = %v, want to contain %q", err, "missing endpoint or keys")
		}
	})

	t.Run("invalid p256dh key is an error", func(t *testing.T) {
		cfg := generateVAPIDConfig(t)
		sender := newWebPushSender(cfg, http.DefaultClient)
		sub := subscription{Endpoint: "https://push.example.com/ep"}
		sub.Keys.P256dh = "not-valid-base64!!"
		sub.Keys.Auth = b64url(make([]byte, 16))
		raw, err := json.Marshal(sub)
		if err != nil {
			t.Fatal(err)
		}
		err = sender.send(context.Background(), string(raw), "t", "b", nil)
		if err == nil || !strings.Contains(err.Error(), "decode p256dh") {
			t.Fatalf("err = %v, want to contain %q", err, "decode p256dh")
		}
	})

	t.Run("misconfigured VAPID key surfaces an error at send time", func(t *testing.T) {
		sender := newWebPushSender(config.VAPID{PrivateKey: "not valid base64!!"}, http.DefaultClient)
		token := subscriptionToken(t, "https://push.example.com/ep")
		err := sender.send(context.Background(), token, "t", "b", nil)
		if err == nil || !strings.Contains(err.Error(), "decode VAPID_PRIVATE_KEY") {
			t.Fatalf("err = %v, want to contain %q", err, "decode VAPID_PRIVATE_KEY")
		}
	})

	t.Run("invalid push endpoint URL is an error", func(t *testing.T) {
		cfg := generateVAPIDConfig(t)
		sender := newWebPushSender(cfg, http.DefaultClient)
		token := subscriptionToken(t, "http://example.com/%zz")
		err := sender.send(context.Background(), token, "t", "b", nil)
		if err == nil || !strings.Contains(err.Error(), "parse push endpoint") {
			t.Fatalf("err = %v, want to contain %q", err, "parse push endpoint")
		}
	})
}

// TestTrimPad checks both the no-op and padding-stripped cases.
func TestTrimPad(t *testing.T) {
	cases := []struct{ in, want string }{
		{"abc", "abc"},
		{"abc=", "abc"},
		{"abc==", "abc"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := trimPad(tc.in); got != tc.want {
			t.Errorf("trimPad(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestParseVAPIDKeysErrors covers the two ways a misconfigured
// VAPID_PRIVATE_KEY is rejected.
func TestParseVAPIDKeysErrors(t *testing.T) {
	t.Run("invalid base64", func(t *testing.T) {
		_, _, err := parseVAPIDKeys(config.VAPID{PrivateKey: "not valid base64!!"})
		if err == nil || !strings.Contains(err.Error(), "decode VAPID_PRIVATE_KEY") {
			t.Fatalf("err = %v, want to contain %q", err, "decode VAPID_PRIVATE_KEY")
		}
	})
	t.Run("wrong-length scalar", func(t *testing.T) {
		_, _, err := parseVAPIDKeys(config.VAPID{PrivateKey: b64url([]byte("short"))})
		if err == nil || !strings.Contains(err.Error(), "not a valid P-256 scalar") {
			t.Fatalf("err = %v, want to contain %q", err, "not a valid P-256 scalar")
		}
	})
}

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
