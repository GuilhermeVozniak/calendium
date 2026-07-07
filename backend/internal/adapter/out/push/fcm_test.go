package push

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"calendium/backend/internal/config"
	"calendium/backend/internal/domain"
)

// serviceAccountJSON builds a Google service-account JSON with a fresh RSA
// key. token_uri stays the real Google host — the rewrite transport redirects
// it (path "/token") to the test server; messages:send is redirected the same
// way.
func serviceAccountJSON(t *testing.T, key *rsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	b, err := json.Marshal(map[string]string{
		"project_id":   "proj-42",
		"client_email": "sa@proj-42.iam.gserviceaccount.com",
		"private_key":  keyPEM,
		"token_uri":    "https://oauth2.googleapis.com/token",
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func generateRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestFCMSend(t *testing.T) {
	t.Run("token exchange then messages:send with the FCM v1 body", func(t *testing.T) {
		key := generateRSAKey(t)
		var tokenForm url.Values
		var sendPath, sendAuth, sendCT string
		var sendBody map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/token":
				raw, _ := io.ReadAll(r.Body)
				tokenForm, _ = url.ParseQuery(string(raw))
				_, _ = w.Write([]byte(`{"access_token":"ya29.test","expires_in":3600}`))
			case strings.HasSuffix(r.URL.Path, "messages:send"):
				sendPath = r.URL.Path
				sendAuth = r.Header.Get("Authorization")
				sendCT = r.Header.Get("Content-Type")
				raw, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(raw, &sendBody)
				_, _ = w.Write([]byte(`{"name":"projects/proj-42/messages/0:1"}`))
			default:
				t.Errorf("unexpected request path %q", r.URL.Path)
			}
		}))
		defer srv.Close()

		cfg := config.Push{FCM: config.FCM{ServiceAccountJSON: serviceAccountJSON(t, key)}}
		d := NewDispatcher(cfg, rewriteClient(t, srv.URL))

		dev := domain.NotificationDevice{Platform: domain.PlatformAndroid, Token: "fcm-reg-token"}
		if err := d.Send(context.Background(), dev, "New mail", "from Ada", map[string]string{"threadId": "t-1"}); err != nil {
			t.Fatalf("Send: %v", err)
		}

		// Token exchange: OAuth2 JWT-bearer grant with an RS256 assertion.
		if gt := tokenForm.Get("grant_type"); gt != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
			t.Errorf("grant_type = %q", gt)
		}
		assertion := tokenForm.Get("assertion")
		parts := strings.Split(assertion, ".")
		if len(parts) != 3 {
			t.Fatalf("assertion is not a compact JWS: %q", assertion)
		}
		// Fully verify the RS256 signature with the service-account public key.
		digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		sig, err := base64.RawURLEncoding.DecodeString(parts[2])
		if err != nil {
			t.Fatalf("decode signature: %v", err)
		}
		if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], sig); err != nil {
			t.Fatalf("RS256 assertion signature invalid: %v", err)
		}

		// messages:send: bearer the minted access token + FCM v1 message shape.
		if sendPath != "/v1/projects/proj-42/messages:send" {
			t.Errorf("send path = %q", sendPath)
		}
		if sendAuth != "Bearer ya29.test" {
			t.Errorf("send Authorization = %q", sendAuth)
		}
		if sendCT != "application/json" {
			t.Errorf("send content-type = %q", sendCT)
		}
		msg, _ := sendBody["message"].(map[string]any)
		if msg["token"] != "fcm-reg-token" {
			t.Errorf("message.token = %v", msg["token"])
		}
		notif, _ := msg["notification"].(map[string]any)
		if notif["title"] != "New mail" || notif["body"] != "from Ada" {
			t.Errorf("message.notification = %v", notif)
		}
		data, _ := msg["data"].(map[string]any)
		if data["threadId"] != "t-1" {
			t.Errorf("message.data = %v", data)
		}
	})

	t.Run("token exchange failure surfaces the http status", func(t *testing.T) {
		key := generateRSAKey(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/token":
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":"unavailable"}`))
			default:
				t.Errorf("unexpected request to messages:send after failed token exchange: %s", r.URL.Path)
			}
		}))
		defer srv.Close()

		cfg := config.Push{FCM: config.FCM{ServiceAccountJSON: serviceAccountJSON(t, key)}}
		d := NewDispatcher(cfg, rewriteClient(t, srv.URL))

		dev := domain.NotificationDevice{Platform: domain.PlatformAndroid, Token: "fcm-reg-token"}
		err := d.Send(context.Background(), dev, "t", "b", nil)
		if err == nil {
			t.Fatal("want error")
		}
		if !strings.Contains(err.Error(), "fcm token exchange http 503") {
			t.Fatalf("err = %v, want to contain %q", err, "fcm token exchange http 503")
		}
	})

	t.Run("unconfigured FCM transport errors without any network call", func(t *testing.T) {
		d := NewDispatcher(config.Push{}, rewriteClient(t, "http://127.0.0.1:0"))
		dev := domain.NotificationDevice{Platform: domain.PlatformAndroid, Token: "fcm-reg-token"}
		err := d.Send(context.Background(), dev, "t", "b", nil)
		if err == nil {
			t.Fatal("want error")
		}
		if !strings.Contains(err.Error(), "fcm transport not configured") {
			t.Fatalf("err = %v, want to contain %q", err, "fcm transport not configured")
		}
	})
}
