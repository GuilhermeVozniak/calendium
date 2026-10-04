package push

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"calendium/backend/internal/config"
	"calendium/backend/internal/domain"
)

// TestDispatcherRouting pins Send's platform → transport routing table
// (ios/macos → apns, android → fcm, web → webpush) and the "transport not
// configured" guard on each branch. APNs/FCM send internals are already
// covered by apns_test.go/fcm_test.go; this only exercises the routing
// decision itself.
func TestDispatcherRouting(t *testing.T) {
	t.Run("ios and macos route to apns", func(t *testing.T) {
		_, keyPEM := ecKeyP8PEM(t)
		var hits int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits++
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		cfg := config.Push{APNs: config.APNs{KeyID: "KID123", TeamID: "TEAM99", KeyP8: keyPEM}}
		d := NewDispatcher(cfg, rewriteClient(t, srv.URL))

		for _, plat := range []domain.DevicePlatform{domain.PlatformIOS, domain.PlatformMacOS} {
			dev := domain.NotificationDevice{Platform: plat, Token: "tok"}
			if err := d.Send(context.Background(), dev, "t", "b", nil); err != nil {
				t.Fatalf("%s: Send: %v", plat, err)
			}
		}
		if hits != 2 {
			t.Fatalf("apns endpoint hit %d times, want 2", hits)
		}
	})

	t.Run("android routes to fcm", func(t *testing.T) {
		key := generateRSAKey(t)
		var hitSend bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/token":
				_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
			case strings.HasSuffix(r.URL.Path, "messages:send"):
				hitSend = true
				_, _ = w.Write([]byte(`{"name":"projects/p/messages/0:1"}`))
			default:
				t.Errorf("unexpected request path %q", r.URL.Path)
			}
		}))
		defer srv.Close()

		cfg := config.Push{FCM: config.FCM{ServiceAccountJSON: serviceAccountJSON(t, key)}}
		d := NewDispatcher(cfg, rewriteClient(t, srv.URL))

		dev := domain.NotificationDevice{Platform: domain.PlatformAndroid, Token: "fcm-tok"}
		if err := d.Send(context.Background(), dev, "t", "b", nil); err != nil {
			t.Fatalf("Send: %v", err)
		}
		if !hitSend {
			t.Fatal("messages:send was not called")
		}
	})

	t.Run("web routes to webpush", func(t *testing.T) {
		var hit bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hit = true
			w.WriteHeader(http.StatusCreated)
		}))
		defer srv.Close()

		cfg := config.Push{VAPID: generateVAPIDConfig(t)}
		// newDispatcher: the production Web Push client refuses loopback
		// (webpush_ssrf_test.go), so inject the httptest client here.
		d := newDispatcher(cfg, srv.Client(), srv.Client())

		dev := domain.NotificationDevice{Platform: domain.PlatformWeb, Token: subscriptionToken(t, srv.URL)}
		if err := d.Send(context.Background(), dev, "t", "b", nil); err != nil {
			t.Fatalf("Send: %v", err)
		}
		if !hit {
			t.Fatal("webpush endpoint was not called")
		}
	})

	t.Run("each transport reports the documented error when unconfigured", func(t *testing.T) {
		d := NewDispatcher(config.Push{}, http.DefaultClient)
		tests := []struct {
			platform domain.DevicePlatform
			want     string
		}{
			{domain.PlatformIOS, "apns transport not configured"},
			{domain.PlatformMacOS, "apns transport not configured"},
			{domain.PlatformAndroid, "fcm transport not configured"},
			{domain.PlatformWeb, "web push transport not configured"},
		}
		for _, tc := range tests {
			t.Run(string(tc.platform), func(t *testing.T) {
				dev := domain.NotificationDevice{Platform: tc.platform, Token: "tok"}
				err := d.Send(context.Background(), dev, "t", "b", nil)
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("err = %v, want to contain %q", err, tc.want)
				}
			})
		}
	})

	t.Run("unknown platform is a validation error", func(t *testing.T) {
		d := NewDispatcher(config.Push{}, http.DefaultClient)
		dev := domain.NotificationDevice{Platform: domain.DevicePlatform("smoke-signal"), Token: "tok"}
		err := d.Send(context.Background(), dev, "t", "b", nil)
		if err == nil || !strings.Contains(err.Error(), "unknown device platform") {
			t.Fatalf("err = %v, want to contain %q", err, "unknown device platform")
		}
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want wrap of domain.ErrValidation", err)
		}
	})
}
