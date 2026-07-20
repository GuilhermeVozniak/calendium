package hubspot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAuthURL(t *testing.T) {
	o := NewOAuth("cid", "secret", nil)
	raw := o.AuthURL("state-1", "https://api.example.com/v1/integrations/callback/hubspot", "ignored")

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse auth url: %v", err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != defaultAuthEndpoint {
		t.Fatalf("endpoint = %q, want %q", got, defaultAuthEndpoint)
	}
	q := u.Query()
	if q.Get("client_id") != "cid" || q.Get("state") != "state-1" {
		t.Fatalf("query = %v", q)
	}
	for _, want := range []string{"crm.objects.contacts.read", "crm.objects.deals.read", "crm.objects.emails.write"} {
		if !strings.Contains(q.Get("scope"), want) {
			t.Fatalf("scope = %q, missing %q", q.Get("scope"), want)
		}
	}
	if strings.Contains(q.Get("scope"), "crm.objects.contacts.write") {
		t.Fatalf("scope = %q must not request contact write access", q.Get("scope"))
	}
	if q.Has("code_challenge") {
		t.Fatalf("hubspot auth URL must not carry PKCE params: %v", q)
	}
}

// fakeHubSpotOAuth serves both the token endpoint (POST /token) and the
// token-info endpoint (GET /access-tokens/{token}).
func fakeHubSpotOAuth(t *testing.T, tokenStatus int, tokenBody string, infoBody string) (*httptest.Server, *url.Values) {
	t.Helper()
	var gotForm url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		gotForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(tokenStatus)
		_, _ = w.Write([]byte(tokenBody))
	})
	mux.HandleFunc("GET /access-tokens/{token}", func(w http.ResponseWriter, r *http.Request) {
		if infoBody == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(infoBody))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &gotForm
}

func newTestOAuth(srv *httptest.Server) *OAuth {
	o := NewOAuth("cid", "csecret", srv.Client())
	o.TokenEndpoint = srv.URL + "/token"
	o.TokenInfoEndpoint = srv.URL + "/access-tokens"
	return o
}

func TestExchange(t *testing.T) {
	srv, gotForm := fakeHubSpotOAuth(t, http.StatusOK,
		`{"access_token":"at-1","refresh_token":"rt-1","expires_in":1800}`,
		`{"user":"owner@example.com","hub_domain":"acme.hubspot.com"}`)
	o := newTestOAuth(srv)

	tok, err := o.Exchange(context.Background(), "code-1", "https://api.example.com/cb", "ignored")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if tok.AccessToken != "at-1" || tok.RefreshToken != "rt-1" {
		t.Fatalf("tokens = %+v", tok.TokenSet)
	}
	if tok.Email != "owner@example.com" {
		t.Fatalf("email label = %q", tok.Email)
	}
	if until := time.Until(tok.ExpiresAt); until < 25*time.Minute || until > 31*time.Minute {
		t.Fatalf("expiry not derived from expires_in: %v", tok.ExpiresAt)
	}
	f := *gotForm
	if f.Get("grant_type") != "authorization_code" || f.Get("client_id") != "cid" ||
		f.Get("client_secret") != "csecret" || f.Get("code") != "code-1" ||
		f.Get("redirect_uri") != "https://api.example.com/cb" {
		t.Fatalf("form = %v", f)
	}
}

func TestExchangeLabelFailureIsBestEffort(t *testing.T) {
	srv, _ := fakeHubSpotOAuth(t, http.StatusOK,
		`{"access_token":"at-1","refresh_token":"rt-1","expires_in":1800}`,
		"") // token-info 401s
	o := newTestOAuth(srv)
	tok, err := o.Exchange(context.Background(), "code-1", "cb", "")
	if err != nil {
		t.Fatalf("Exchange must not fail when the label lookup fails: %v", err)
	}
	if tok.Email != "" {
		t.Fatalf("email = %q, want empty", tok.Email)
	}
}

func TestRefresh(t *testing.T) {
	t.Run("rotates when the vendor returns a new refresh token", func(t *testing.T) {
		srv, gotForm := fakeHubSpotOAuth(t, http.StatusOK,
			`{"access_token":"at-2","refresh_token":"rt-2","expires_in":1800}`, "")
		o := newTestOAuth(srv)
		tok, err := o.Refresh(context.Background(), "rt-1")
		if err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		if tok.AccessToken != "at-2" || tok.RefreshToken != "rt-2" {
			t.Fatalf("tokens = %+v", tok.TokenSet)
		}
		f := *gotForm
		if f.Get("grant_type") != "refresh_token" || f.Get("refresh_token") != "rt-1" {
			t.Fatalf("form = %v", f)
		}
	})
	t.Run("carries the old refresh token through when not rotated", func(t *testing.T) {
		srv, _ := fakeHubSpotOAuth(t, http.StatusOK,
			`{"access_token":"at-2","expires_in":1800}`, "")
		o := newTestOAuth(srv)
		tok, err := o.Refresh(context.Background(), "rt-1")
		if err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		if tok.RefreshToken != "rt-1" {
			t.Fatalf("refresh token = %q, want carried-through rt-1", tok.RefreshToken)
		}
	})
}

func TestTokenEndpointError(t *testing.T) {
	srv, _ := fakeHubSpotOAuth(t, http.StatusBadRequest,
		`{"status":"BAD_AUTH_CODE","message":"expired"}`, "")
	o := newTestOAuth(srv)
	if _, err := o.Exchange(context.Background(), "bad", "cb", ""); err == nil {
		t.Fatal("expected an error for a non-200 token response")
	}
}
