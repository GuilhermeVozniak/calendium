package todoist

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestAuthURL(t *testing.T) {
	o := NewOAuth("cid", "secret", nil)
	raw := o.AuthURL("state-1", "https://api.example.com/v1/integrations/callback/todoist", "ignored-challenge")

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse auth url: %v", err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != defaultAuthEndpoint {
		t.Fatalf("endpoint = %q, want %q", got, defaultAuthEndpoint)
	}
	q := u.Query()
	if q.Get("client_id") != "cid" || q.Get("state") != "state-1" || q.Get("scope") != "data:read_write" {
		t.Fatalf("query = %v", q)
	}
	if q.Get("redirect_uri") != "https://api.example.com/v1/integrations/callback/todoist" {
		t.Fatalf("redirect_uri = %q", q.Get("redirect_uri"))
	}
	// No PKCE: the challenge must never reach the vendor.
	if q.Has("code_challenge") || q.Has("code_challenge_method") {
		t.Fatalf("todoist auth URL must not carry PKCE params: %v", q)
	}
}

func TestExchange(t *testing.T) {
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		gotForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok-123","token_type":"Bearer"}`))
	}))
	defer srv.Close()

	o := NewOAuth("cid", "csecret", srv.Client())
	o.TokenEndpoint = srv.URL

	tok, err := o.Exchange(context.Background(), "code-1", "https://api.example.com/cb", "ignored-verifier")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if tok.AccessToken != "tok-123" {
		t.Fatalf("access token = %q", tok.AccessToken)
	}
	if tok.RefreshToken != "" || !tok.ExpiresAt.IsZero() {
		t.Fatalf("todoist tokens must have no refresh/expiry: %+v", tok.TokenSet)
	}
	if gotForm.Get("client_id") != "cid" || gotForm.Get("client_secret") != "csecret" ||
		gotForm.Get("code") != "code-1" || gotForm.Get("redirect_uri") != "https://api.example.com/cb" {
		t.Fatalf("form = %v", gotForm)
	}
	if gotForm.Has("code_verifier") {
		t.Fatalf("todoist exchange must not send a PKCE verifier: %v", gotForm)
	}
}

func TestExchangeErrors(t *testing.T) {
	t.Run("non-200", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"bad_authorization_code"}`))
		}))
		defer srv.Close()
		o := NewOAuth("cid", "csecret", srv.Client())
		o.TokenEndpoint = srv.URL
		if _, err := o.Exchange(context.Background(), "bad", "cb", ""); err == nil {
			t.Fatal("expected an error for a non-200 token response")
		}
	})
	t.Run("missing access_token", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{}`))
		}))
		defer srv.Close()
		o := NewOAuth("cid", "csecret", srv.Client())
		o.TokenEndpoint = srv.URL
		if _, err := o.Exchange(context.Background(), "code", "cb", ""); err == nil {
			t.Fatal("expected an error for an empty access_token")
		}
	})
}

func TestRefreshUnsupported(t *testing.T) {
	o := NewOAuth("cid", "csecret", nil)
	if _, err := o.Refresh(context.Background(), "whatever"); err == nil {
		t.Fatal("Refresh must error: todoist has no refresh flow")
	}
}
