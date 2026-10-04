package push

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"calendium/backend/internal/config"
	"calendium/backend/internal/domain"
)

// rewriteRoundTripper redirects the hardcoded APNs/FCM hosts to the test
// server. The *http.Client passed to NewDispatcher is the injection seam;
// production source is unchanged.
type rewriteRoundTripper struct {
	target *url.URL
	base   http.RoundTripper
}

func (rt rewriteRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = rt.target.Scheme
	req.URL.Host = rt.target.Host
	req.Host = rt.target.Host
	return rt.base.RoundTrip(req)
}

func rewriteClient(t *testing.T, serverURL string) *http.Client {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	return &http.Client{Transport: rewriteRoundTripper{target: u, base: http.DefaultTransport}}
}

// ecKeyP8PEM generates a P-256 key and its PKCS#8 PEM (an Apple .p8 shape).
func ecKeyP8PEM(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func TestAPNsSend(t *testing.T) {
	t.Run("routes request, headers, payload, and ES256 provider token", func(t *testing.T) {
		key, keyPEM := ecKeyP8PEM(t)

		var gotPath, gotAuth, gotTopic, gotType, gotPriority, gotCT string
		var gotPayload map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotAuth = r.Header.Get("Authorization")
			gotTopic = r.Header.Get("apns-topic")
			gotType = r.Header.Get("apns-push-type")
			gotPriority = r.Header.Get("apns-priority")
			gotCT = r.Header.Get("Content-Type")
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &gotPayload)
			w.WriteHeader(http.StatusOK) // APNs signals success with 200 + empty body
		}))
		defer srv.Close()

		cfg := config.Push{APNs: config.APNs{KeyID: "KID123", TeamID: "TEAM99", KeyP8: keyPEM}}
		d := NewDispatcher(cfg, rewriteClient(t, srv.URL))

		dev := domain.NotificationDevice{Platform: domain.PlatformIOS, Token: "abc123devtoken"}
		if err := d.Send(context.Background(), dev, "New mail", "from Ada", map[string]string{"threadId": "t-1"}); err != nil {
			t.Fatalf("Send: %v", err)
		}

		if gotPath != "/3/device/abc123devtoken" {
			t.Errorf("path = %q", gotPath)
		}
		if gotTopic != defaultAPNsTopic {
			t.Errorf("apns-topic = %q, want %q", gotTopic, defaultAPNsTopic)
		}
		if gotType != "alert" || gotPriority != "10" {
			t.Errorf("apns-push-type=%q apns-priority=%q", gotType, gotPriority)
		}
		if gotCT != "application/json" {
			t.Errorf("content-type = %q", gotCT)
		}

		aps, _ := gotPayload["aps"].(map[string]any)
		alert, _ := aps["alert"].(map[string]any)
		if alert["title"] != "New mail" || alert["body"] != "from Ada" {
			t.Errorf("aps.alert = %v", alert)
		}
		if gotPayload["threadId"] != "t-1" { // custom data keys hoisted to top level
			t.Errorf("custom data key = %v", gotPayload["threadId"])
		}

		if !strings.HasPrefix(gotAuth, "Bearer ") {
			t.Fatalf("Authorization = %q", gotAuth)
		}
		verifyES256JWT(t, strings.TrimPrefix(gotAuth, "Bearer "), &key.PublicKey, "KID123", "TEAM99")
	})

	t.Run("also routes macOS devices to APNs", func(t *testing.T) {
		_, keyPEM := ecKeyP8PEM(t)
		var gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		cfg := config.Push{APNs: config.APNs{KeyID: "KID123", TeamID: "TEAM99", KeyP8: keyPEM}}
		d := NewDispatcher(cfg, rewriteClient(t, srv.URL))

		dev := domain.NotificationDevice{Platform: domain.PlatformMacOS, Token: "mac-device-token"}
		if err := d.Send(context.Background(), dev, "t", "b", nil); err != nil {
			t.Fatalf("Send: %v", err)
		}
		if gotPath != "/3/device/mac-device-token" {
			t.Errorf("path = %q", gotPath)
		}
	})

	t.Run("non-200 APNs response surfaces the reason", func(t *testing.T) {
		_, keyPEM := ecKeyP8PEM(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusGone)
			_, _ = w.Write([]byte(`{"reason":"BadDeviceToken"}`))
		}))
		defer srv.Close()

		cfg := config.Push{APNs: config.APNs{KeyID: "KID123", TeamID: "TEAM99", KeyP8: keyPEM}}
		d := NewDispatcher(cfg, rewriteClient(t, srv.URL))

		dev := domain.NotificationDevice{Platform: domain.PlatformIOS, Token: "stale-token"}
		err := d.Send(context.Background(), dev, "t", "b", nil)
		if err == nil {
			t.Fatal("want error")
		}
		if !strings.Contains(err.Error(), "apns http 410: BadDeviceToken") {
			t.Fatalf("err = %v, want to contain %q", err, "apns http 410: BadDeviceToken")
		}
	})
}

// verifyES256JWT decodes a compact ES256 JWS, checks header/claims, and
// verifies the raw r||s (64-byte) signature with pub.
func verifyES256JWT(t *testing.T, token string, pub *ecdsa.PublicKey, wantKid, wantIss string) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt has %d parts", len(parts))
	}
	var hdr struct{ Alg, Kid string }
	hb, _ := base64.RawURLEncoding.DecodeString(parts[0])
	if err := json.Unmarshal(hb, &hdr); err != nil {
		t.Fatalf("decode header: %v", err)
	}
	if hdr.Alg != "ES256" || hdr.Kid != wantKid {
		t.Errorf("header = %+v, want alg ES256 kid %q", hdr, wantKid)
	}
	var claims struct {
		Iss string `json:"iss"`
		Iat int64  `json:"iat"`
	}
	cb, _ := base64.RawURLEncoding.DecodeString(parts[1])
	if err := json.Unmarshal(cb, &claims); err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	if claims.Iss != wantIss || claims.Iat == 0 {
		t.Errorf("claims = %+v, want iss %q + nonzero iat", claims, wantIss)
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	if len(sig) != 64 {
		t.Fatalf("signature = %d bytes, want 64", len(sig))
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(pub, digest[:], r, s) {
		t.Fatal("ES256 provider-token signature invalid")
	}
}

// The default topic must equal the shipped iOS bundle id (apps/mobile/app.json
// ios.bundleIdentifier); APNs rejects pushes whose topic does not match.
func TestDefaultAPNsTopic_IsTheMobileBundleID(t *testing.T) {
	if defaultAPNsTopic != "app.calendium.mobile" {
		t.Fatalf("defaultAPNsTopic = %q, want app.calendium.mobile", defaultAPNsTopic)
	}
	t.Setenv("APNS_TOPIC", "")
	if s := newAPNsSender(config.APNs{}, nil); s.topic != "app.calendium.mobile" {
		t.Fatalf("topic = %q, want the default", s.topic)
	}
}

func TestAPNsTopic_EnvOverrideWins(t *testing.T) {
	t.Setenv("APNS_TOPIC", "com.example.custom")
	if s := newAPNsSender(config.APNs{}, nil); s.topic != "com.example.custom" {
		t.Fatalf("topic = %q, want com.example.custom", s.topic)
	}
}
