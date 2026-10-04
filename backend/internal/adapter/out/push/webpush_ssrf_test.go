package push

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"calendium/backend/internal/adapter/out/netguard"
	"calendium/backend/internal/config"
	"calendium/backend/internal/domain"
)

// countingServer is an httptest server on 127.0.0.1 that counts requests.
func countingServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if h != nil {
			h(w, r)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// TestNewDispatcherWebPushRefusesPrivateEndpoints pins the production wiring:
// whatever client the caller passes, Web Push delivery dials through
// netguard, so a subscription pointed at loopback, private or link-local
// space fails before any request leaves the host (whole-branch review I3).
func TestNewDispatcherWebPushRefusesPrivateEndpoints(t *testing.T) {
	srv, hits := countingServer(t, nil)
	// srv.Client() would happily reach 127.0.0.1; the dispatcher must not use
	// it for Web Push.
	d := NewDispatcher(config.Push{VAPID: generateVAPIDConfig(t)}, srv.Client())

	endpoints := []string{
		srv.URL + "/push",                          // loopback httptest server
		"https://127.0.0.1:1/push",                 // loopback literal
		"https://localhost:1/push",                 // loopback by name
		"https://10.0.0.1/push",                    // RFC 1918
		"https://169.254.169.254/latest/meta-data", // link-local metadata
		"https://[::1]:1/push",                     // IPv6 loopback
		"https://[fd00::1]/push",                   // IPv6 ULA
	}
	for _, ep := range endpoints {
		t.Run(ep, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			dev := domain.NotificationDevice{Platform: domain.PlatformWeb, Token: subscriptionToken(t, ep)}
			err := d.Send(ctx, dev, "t", "b", nil)
			if err == nil || !strings.Contains(err.Error(), "netguard") {
				t.Fatalf("Send(%s) err = %v, want a netguard refusal", ep, err)
			}
		})
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("loopback push endpoint received %d requests, want 0", n)
	}
}

// TestWebPushClientRechecksRedirects proves every redirect hop is vetted: a
// first hop the test explicitly allows may not bounce delivery to a
// private address, whether the Location is an IP literal (refused by the
// redirect policy) or a hostname resolving to loopback (refused at dial).
func TestWebPushClientRechecksRedirects(t *testing.T) {
	cfg := generateVAPIDConfig(t)
	target, targetHits := countingServer(t, nil)
	port := target.URL[strings.LastIndex(target.URL, ":")+1:]

	for _, tc := range []struct{ name, location string }{
		{"ip literal", "http://127.0.0.1:" + port + "/internal"},
		{"hostname", "http://localhost:" + port + "/internal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first, firstHits := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, tc.location, http.StatusTemporaryRedirect)
			})
			allowed := first.Listener.Addr().String()
			hc := newWebPushClient(func(network, address string, c syscall.RawConn) error {
				if address == allowed {
					return nil // stand-in for a public push service
				}
				return netguard.Control(network, address, c)
			})
			sender := newWebPushSender(cfg, hc)

			err := sender.send(context.Background(), subscriptionToken(t, first.URL+"/push"), "t", "b", nil)
			if err == nil {
				t.Fatal("send followed a redirect into loopback, want an error")
			}
			if firstHits.Load() != 1 {
				t.Fatalf("first hop hits = %d, want 1", firstHits.Load())
			}
		})
	}
	if n := targetHits.Load(); n != 0 {
		t.Fatalf("redirect target received %d requests, want 0", n)
	}
}

// TestWebPushRedirectPolicy: an https push service may not redirect
// delivery to cleartext http, to a non-http scheme, or to a private IP
// literal, and hops are bounded.
func TestWebPushRedirectPolicy(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "http://push.example/x", nil)
	via, _ := http.NewRequest(http.MethodPost, "https://push.example/x", nil)
	if err := webPushRedirectPolicy(req, []*http.Request{via}); err == nil {
		t.Fatal("https → http redirect allowed, want refusal")
	}
	ok, _ := http.NewRequest(http.MethodPost, "https://push2.example/x", nil)
	if err := webPushRedirectPolicy(ok, []*http.Request{via}); err != nil {
		t.Fatalf("https → https redirect refused: %v", err)
	}
	for _, bad := range []string{"https://10.1.2.3/x", "https://169.254.169.254/x", "https://[::1]/x", "https://[::ffff:127.0.0.1]/x", "ftp://push.example/x"} {
		r, _ := http.NewRequest(http.MethodPost, bad, nil)
		if err := webPushRedirectPolicy(r, []*http.Request{via}); err == nil {
			t.Errorf("redirect to %s allowed, want refusal", bad)
		}
	}
	many := make([]*http.Request, maxWebPushRedirects)
	for i := range many {
		many[i] = via
	}
	if err := webPushRedirectPolicy(ok, many); err == nil {
		t.Fatalf("redirect #%d allowed, want the hop limit", maxWebPushRedirects+1)
	}
}

// TestWebPushClientHasNoProxy: an environment proxy would carry delivery
// past the dial guard.
func TestWebPushClientHasNoProxy(t *testing.T) {
	tr, ok := newWebPushClient(netguard.Control).Transport.(*http.Transport)
	if !ok {
		t.Fatal("web push client transport is not *http.Transport")
	}
	if tr.Proxy != nil {
		t.Fatal("web push transport has a Proxy func, want none")
	}
}
