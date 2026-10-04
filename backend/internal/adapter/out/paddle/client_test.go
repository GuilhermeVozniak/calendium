package paddle

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"calendium/backend/internal/domain"
)

// newTestClient points the gateway at an httptest server through the
// Config.BaseURL override (the only injection seam besides *http.Client).
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	return NewClient(Config{Env: EnvSandbox, APIKey: "pdl_test_key", WebhookSecret: "ntf_secret", AnnualPriceID: "pri_annual", BaseURL: srv.URL}, srv.Client())
}

func TestBaseURLFor(t *testing.T) {
	if got := BaseURLFor(EnvSandbox); got != "https://sandbox-api.paddle.com" {
		t.Fatalf("sandbox = %q", got)
	}
	if got := BaseURLFor(EnvLive); got != "https://api.paddle.com" {
		t.Fatalf("live = %q", got)
	}
	if got := BaseURLFor(""); got != "https://sandbox-api.paddle.com" {
		t.Fatalf("empty env must default to sandbox, got %q", got)
	}
	if c := NewClient(Config{Env: EnvLive}, nil); c.baseURL != "https://api.paddle.com" {
		t.Fatalf("NewClient live baseURL = %q", c.baseURL)
	}
}

func TestDoSendsBearerAndDecodesData(t *testing.T) {
	var gotAuth, gotAccept, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotCT = r.Header.Get("Content-Type")
		_, _ = w.Write([]byte(`{"data":{"id":"ctm_1"},"meta":{"request_id":"r"}}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	var out struct {
		ID string `json:"id"`
	}
	if err := c.do(context.Background(), http.MethodPost, "/customers", map[string]string{"email": "a@b.c"}, &out); err != nil {
		t.Fatalf("do: %v", err)
	}
	if out.ID != "ctm_1" {
		t.Fatalf("decoded id = %q, want ctm_1 (must unwrap the data envelope)", out.ID)
	}
	if gotAuth != "Bearer pdl_test_key" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotAccept != "application/json" || gotCT != "application/json" {
		t.Fatalf("Accept/Content-Type = %q/%q", gotAccept, gotCT)
	}
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial tcp: connection refused")
}

// The EnsureCustomer lookup carries the customer email in its query; a
// transport error must not leak it into logs via the error text.
func TestTransportErrorTextOmitsCustomerEmail(t *testing.T) {
	c := NewClient(Config{Env: EnvSandbox, APIKey: "k", BaseURL: "https://paddle.invalid"}, &http.Client{Transport: failingTransport{}})
	_, err := c.EnsureCustomer(context.Background(), domain.User{ID: "u1", Email: "secret.person@example.com"})
	if err == nil {
		t.Fatal("want a transport error")
	}
	for _, leak := range []string{"secret.person", "example.com", "email="} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("error text leaks %q: %q", leak, err.Error())
		}
	}
	if !strings.Contains(err.Error(), "/customers") || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("error text lost its diagnostics: %q", err.Error())
	}
}

// A non-2xx body that is not Paddle's {error} envelope (proxy HTML, empty
// 502/504) keeps the status and a truncated raw-body snippet.
func TestDoNonJSONErrorBodyKeepsStatusAndTruncatedBody(t *testing.T) {
	html := "<html><body>Bad Gateway " + strings.Repeat("x", 2000) + "</body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(html))
	}))
	defer srv.Close()
	err := newTestClient(t, srv).do(context.Background(), http.MethodGet, "/subscriptions/sub_1", nil, &struct{}{})
	var api *APIError
	if !errors.As(err, &api) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if api.Status != http.StatusBadGateway || !strings.Contains(err.Error(), "502") {
		t.Fatalf("status lost: %+v / %q", api, err.Error())
	}
	if !strings.HasPrefix(api.Detail, "<html><body>Bad Gateway") || len(api.Detail) > 512 {
		t.Fatalf("Detail = %d bytes %q, want a <=512-byte prefix of the raw body", len(api.Detail), api.Detail)
	}
}

func TestDoMapsErrors(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		wantIs   error // nil = only *APIError
		wantCode string
	}{
		{"unauthorized", http.StatusUnauthorized, domain.ErrUnauthorized, "authentication_malformed"},
		{"forbidden", http.StatusForbidden, domain.ErrUnauthorized, "forbidden"},
		{"not found", http.StatusNotFound, domain.ErrNotFound, "entity_not_found"},
		{"server error", http.StatusInternalServerError, nil, "internal_error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":{"type":"request_error","code":"` + tc.wantCode + `","detail":"nope"}}`))
			}))
			defer srv.Close()
			c := newTestClient(t, srv)
			err := c.do(context.Background(), http.MethodGet, "/subscriptions/sub_1", nil, &struct{}{})
			if err == nil {
				t.Fatal("want error")
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Fatalf("err = %v, want wrap of %v", err, tc.wantIs)
			}
			var api *APIError
			if !errors.As(err, &api) {
				t.Fatalf("err = %v, want *APIError in chain", err)
			}
			if api.Status != tc.status || api.Code != tc.wantCode || api.Detail != "nope" {
				t.Fatalf("APIError = %+v", api)
			}
			if !strings.Contains(err.Error(), "paddle: http") {
				t.Fatalf("err text = %q", err.Error())
			}
		})
	}
}
