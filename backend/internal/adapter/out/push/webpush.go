package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"calendium/backend/internal/config"
)

const (
	webPushTTL         = "2419200" // 4 weeks, in seconds
	webPushRecordSize  = 4096
	vapidTokenLifetime = 12 * time.Hour
	vapidSubject       = "mailto:push@calendium.app"
)

// webPushSender delivers Web Push notifications: VAPID ES256 authorization
// (RFC 8292) + aes128gcm payload encryption (RFC 8291 / RFC 8188), stdlib
// crypto only. The device token is the browser's PushSubscription JSON.
type webPushSender struct {
	cfg config.VAPID
	hc  *http.Client

	keyOnce sync.Once
	signKey *ecdsa.PrivateKey
	pubB64  string
	keyErr  error
}

// subscription is the browser PushSubscription serialization stored as the
// device token.
type subscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"` // UA public key, base64url uncompressed point
		Auth   string `json:"auth"`   // 16-byte auth secret, base64url
	} `json:"keys"`
}

func newWebPushSender(cfg config.VAPID, hc *http.Client) *webPushSender {
	return &webPushSender{cfg: cfg, hc: hc}
}

func (s *webPushSender) send(ctx context.Context, token, title, body string, data map[string]string) error {
	var sub subscription
	if err := json.Unmarshal([]byte(token), &sub); err != nil {
		return fmt.Errorf("push: parse web push subscription: %w", err)
	}
	if sub.Endpoint == "" || sub.Keys.P256dh == "" || sub.Keys.Auth == "" {
		return fmt.Errorf("push: web push subscription is missing endpoint or keys")
	}

	payload, err := json.Marshal(map[string]any{"title": title, "body": body, "data": data})
	if err != nil {
		return fmt.Errorf("push: encode web push payload: %w", err)
	}
	ciphertext, err := encryptAES128GCM(payload, sub.Keys.P256dh, sub.Keys.Auth)
	if err != nil {
		return err
	}

	auth, err := s.vapidAuthorization(sub.Endpoint)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(ciphertext))
	if err != nil {
		return fmt.Errorf("push: build web push request: %w", err)
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", webPushTTL)
	req.Header.Set("Urgency", "high")

	res, err := s.hc.Do(req)
	if err != nil {
		return fmt.Errorf("push: web push request: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= 200 && res.StatusCode <= 299 {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	return fmt.Errorf("push: web push http %d: %s", res.StatusCode, truncate(string(raw), 200))
}

// vapidAuthorization builds the "vapid t=<jwt>, k=<pub>" header (RFC 8292);
// aud is the push service origin.
func (s *webPushSender) vapidAuthorization(endpoint string) (string, error) {
	s.keyOnce.Do(func() { s.signKey, s.pubB64, s.keyErr = parseVAPIDKeys(s.cfg) })
	if s.keyErr != nil {
		return "", s.keyErr
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("push: parse push endpoint: %w", err)
	}
	header := map[string]string{"typ": "JWT", "alg": "ES256"}
	claims := map[string]any{
		"aud": u.Scheme + "://" + u.Host,
		"exp": time.Now().Add(vapidTokenLifetime).Unix(),
		"sub": vapidSubject,
	}
	jwt, err := signES256(header, claims, s.signKey)
	if err != nil {
		return "", err
	}
	return "vapid t=" + jwt + ", k=" + s.pubB64, nil
}

// parseVAPIDKeys reconstructs the ES256 signing key from the base64url
// 32-byte private scalar and returns it with the base64url uncompressed
// public point for the k= parameter.
func parseVAPIDKeys(cfg config.VAPID) (*ecdsa.PrivateKey, string, error) {
	d, err := base64.RawURLEncoding.DecodeString(trimPad(cfg.PrivateKey))
	if err != nil {
		return nil, "", fmt.Errorf("push: decode VAPID_PRIVATE_KEY: %w", err)
	}
	key, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), d)
	if err != nil {
		return nil, "", fmt.Errorf("push: VAPID_PRIVATE_KEY is not a valid P-256 scalar: %w", err)
	}
	pub, err := key.PublicKey.Bytes() // 0x04 || X || Y
	if err != nil {
		return nil, "", fmt.Errorf("push: encode VAPID public key: %w", err)
	}
	pubB64 := cfg.PublicKey
	if pubB64 == "" {
		pubB64 = base64.RawURLEncoding.EncodeToString(pub)
	}
	return key, trimPad(pubB64), nil
}

// encryptAES128GCM implements the RFC 8291 message encryption: ECDH over
// P-256 with the subscription keys, HKDF-SHA256 key derivation, and a
// single aes128gcm record (RFC 8188) carrying the whole payload.
func encryptAES128GCM(plaintext []byte, p256dhB64, authB64 string) ([]byte, error) {
	uaPubBytes, err := base64.RawURLEncoding.DecodeString(trimPad(p256dhB64))
	if err != nil {
		return nil, fmt.Errorf("push: decode p256dh: %w", err)
	}
	authSecret, err := base64.RawURLEncoding.DecodeString(trimPad(authB64))
	if err != nil {
		return nil, fmt.Errorf("push: decode auth secret: %w", err)
	}
	uaPub, err := ecdh.P256().NewPublicKey(uaPubBytes)
	if err != nil {
		return nil, fmt.Errorf("push: p256dh is not a valid P-256 point: %w", err)
	}

	asKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("push: generate ephemeral key: %w", err)
	}
	asPub := asKey.PublicKey().Bytes()
	ecdhSecret, err := asKey.ECDH(uaPub)
	if err != nil {
		return nil, fmt.Errorf("push: ecdh: %w", err)
	}

	// IKM = HKDF(auth_secret, ecdh_secret, "WebPush: info" || 0x00 || ua_public || as_public)
	info := "WebPush: info\x00" + string(uaPubBytes) + string(asPub)
	ikm, err := hkdf.Key(sha256.New, ecdhSecret, authSecret, info, 32)
	if err != nil {
		return nil, fmt.Errorf("push: hkdf ikm: %w", err)
	}

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("push: generate salt: %w", err)
	}
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, fmt.Errorf("push: hkdf cek: %w", err)
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, fmt.Errorf("push: hkdf nonce: %w", err)
	}

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, fmt.Errorf("push: aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("push: gcm: %w", err)
	}

	// One final record: plaintext || 0x02 delimiter, sealed with the nonce.
	record := append(append([]byte{}, plaintext...), 0x02)
	if len(record)+gcm.Overhead() > webPushRecordSize {
		return nil, fmt.Errorf("push: payload exceeds the %d-byte web push record", webPushRecordSize)
	}
	sealed := gcm.Seal(nil, nonce, record, nil)

	// aes128gcm header: salt(16) || rs(4) || idlen(1) || keyid(as_public, 65).
	var out bytes.Buffer
	out.Write(salt)
	_ = binary.Write(&out, binary.BigEndian, uint32(webPushRecordSize))
	out.WriteByte(byte(len(asPub)))
	out.Write(asPub)
	out.Write(sealed)
	return out.Bytes(), nil
}

// trimPad strips base64 padding so both padded and raw inputs decode.
func trimPad(s string) string {
	for len(s) > 0 && s[len(s)-1] == '=' {
		s = s[:len(s)-1]
	}
	return s
}
