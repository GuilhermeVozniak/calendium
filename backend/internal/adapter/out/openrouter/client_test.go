package openrouter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"calendium/backend/internal/domain"
)

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

func TestComplete(t *testing.T) {
	t.Run("posts chat-completions JSON and parses text/model", func(t *testing.T) {
		var gotPath, gotAuth, gotCT string
		var gotBody map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotAuth = r.Header.Get("Authorization")
			gotCT = r.Header.Get("Content-Type")
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &gotBody)
			_, _ = w.Write([]byte(`{"model":"anthropic/claude-3.5","choices":[{"message":{"role":"assistant","content":"Hi there"}}]}`))
		}))
		defer srv.Close()

		c := NewClient("sk-or-key", "openrouter/auto", rewriteClient(t, srv.URL))
		text, model, err := c.Complete(context.Background(), "be brief", "hello")
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if text != "Hi there" {
			t.Errorf("text = %q", text)
		}
		if model != "anthropic/claude-3.5" { // upstream-reported model wins over configured
			t.Errorf("model = %q", model)
		}
		if gotPath != "/api/v1/chat/completions" {
			t.Errorf("path = %q", gotPath)
		}
		if gotAuth != "Bearer sk-or-key" {
			t.Errorf("auth = %q", gotAuth)
		}
		if gotCT != "application/json" {
			t.Errorf("content-type = %q", gotCT)
		}
		if gotBody["model"] != "openrouter/auto" {
			t.Errorf("body model = %v", gotBody["model"])
		}
		msgs, ok := gotBody["messages"].([]any)
		if !ok || len(msgs) != 2 {
			t.Fatalf("messages = %v", gotBody["messages"])
		}
		sys := msgs[0].(map[string]any)
		usr := msgs[1].(map[string]any)
		if sys["role"] != "system" || sys["content"] != "be brief" {
			t.Errorf("system msg = %v", sys)
		}
		if usr["role"] != "user" || usr["content"] != "hello" {
			t.Errorf("user msg = %v", usr)
		}
	})

	t.Run("omits system message when system is empty", func(t *testing.T) {
		var gotBody map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &gotBody)
			_, _ = w.Write([]byte(`{"model":"m","choices":[{"message":{"content":"ok"}}]}`))
		}))
		defer srv.Close()

		c := NewClient("k", "m", rewriteClient(t, srv.URL))
		if _, _, err := c.Complete(context.Background(), "", "just user"); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		msgs := gotBody["messages"].([]any)
		if len(msgs) != 1 || msgs[0].(map[string]any)["role"] != "user" {
			t.Fatalf("messages = %v, want single user message", msgs)
		}
	})

	t.Run("falls back to configured model when upstream omits model", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
		}))
		defer srv.Close()

		c := NewClient("k", "configured-model", rewriteClient(t, srv.URL))
		_, model, err := c.Complete(context.Background(), "", "hi")
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if model != "configured-model" {
			t.Errorf("model = %q, want fallback to configured model", model)
		}
	})

	t.Run("error mapping", func(t *testing.T) {
		tests := []struct {
			name       string
			status     int
			body       string
			wantUnauth bool
		}{
			{"server error", http.StatusInternalServerError, `{"error":{"message":"boom"}}`, false},
			{"unauthorized", http.StatusUnauthorized, `{"error":{"message":"bad key"}}`, true},
			{"forbidden", http.StatusForbidden, `{"error":{"message":"forbidden"}}`, true},
			{"no choices", http.StatusOK, `{"model":"m","choices":[]}`, false},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()
				c := NewClient("k", "", rewriteClient(t, srv.URL))
				_, _, err := c.Complete(context.Background(), "", "hi")
				if err == nil {
					t.Fatal("want error")
				}
				if tc.wantUnauth && !errors.Is(err, domain.ErrUnauthorized) {
					t.Fatalf("err = %v, want wrap of domain.ErrUnauthorized", err)
				}
			})
		}
	})
}
