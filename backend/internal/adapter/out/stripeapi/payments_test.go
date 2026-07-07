package stripeapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
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

func strptr(s string) *string { return &s }

func TestEnsureCustomer(t *testing.T) {
	t.Run("creates a customer when the search finds none", func(t *testing.T) {
		var searchQuery, custBody, custCT string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/v1/customers/search":
				searchQuery = r.URL.Query().Get("query")
				_, _ = w.Write([]byte(`{"data":[]}`))
			case r.Method == http.MethodPost && r.URL.Path == "/v1/customers":
				raw, _ := io.ReadAll(r.Body)
				custBody = string(raw)
				custCT = r.Header.Get("Content-Type")
				_, _ = w.Write([]byte(`{"id":"cus_new"}`))
			default:
				t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			}
		}))
		defer srv.Close()

		c := NewClient("sk_test", "wh", "price_x", rewriteClient(t, srv.URL))
		id, err := c.EnsureCustomer(context.Background(),
			domain.User{ID: "user-9", Email: "ada@x.com", Name: strptr("Ada")})
		if err != nil {
			t.Fatalf("EnsureCustomer: %v", err)
		}
		if id != "cus_new" {
			t.Errorf("id = %q", id)
		}
		if !strings.Contains(searchQuery, "metadata['user_id']:'user-9'") {
			t.Errorf("search query = %q", searchQuery)
		}
		if custCT != "application/x-www-form-urlencoded" {
			t.Errorf("create content-type = %q", custCT)
		}
		form, _ := url.ParseQuery(custBody)
		if form.Get("email") != "ada@x.com" || form.Get("metadata[user_id]") != "user-9" || form.Get("name") != "Ada" {
			t.Errorf("create form = %q", custBody)
		}
	})

	t.Run("returns the existing customer without creating one", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/customers/search" {
				t.Errorf("unexpected create call: %s %s", r.Method, r.URL.Path)
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"cus_existing"}]}`))
		}))
		defer srv.Close()

		c := NewClient("sk_test", "wh", "price_x", rewriteClient(t, srv.URL))
		id, err := c.EnsureCustomer(context.Background(), domain.User{ID: "user-9", Email: "a@b.com"})
		if err != nil {
			t.Fatal(err)
		}
		if id != "cus_existing" {
			t.Errorf("id = %q", id)
		}
	})

	t.Run("omits the name field when the user has none", func(t *testing.T) {
		var custBody string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/v1/customers/search":
				_, _ = w.Write([]byte(`{"data":[]}`))
			case r.URL.Path == "/v1/customers":
				raw, _ := io.ReadAll(r.Body)
				custBody = string(raw)
				_, _ = w.Write([]byte(`{"id":"cus_noname"}`))
			}
		}))
		defer srv.Close()

		c := NewClient("sk_test", "wh", "price_x", rewriteClient(t, srv.URL))
		if _, err := c.EnsureCustomer(context.Background(), domain.User{ID: "user-1", Email: "no-name@x.com"}); err != nil {
			t.Fatal(err)
		}
		form, _ := url.ParseQuery(custBody)
		if _, ok := form["name"]; ok {
			t.Errorf("form had name = %q, want no name field", form.Get("name"))
		}
	})
}

func TestCreateCheckoutSession(t *testing.T) {
	t.Run("posts full form params and returns the checkout url", func(t *testing.T) {
		var form url.Values
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/v1/checkout/sessions" {
				t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
			}
			raw, _ := io.ReadAll(r.Body)
			form, _ = url.ParseQuery(string(raw))
			_, _ = w.Write([]byte(`{"id":"cs_1","url":"https://checkout.stripe.com/c/pay/cs_1"}`))
		}))
		defer srv.Close()

		c := NewClient("sk_test", "wh", "price_annual", rewriteClient(t, srv.URL))
		got, err := c.CreateCheckoutSession(context.Background(), port.CheckoutParams{
			UserID: "user-9", CustomerID: "cus_1",
			SuccessURL: "https://a/ok", CancelURL: "https://a/no", TrialDays: 14,
		})
		if err != nil {
			t.Fatal(err)
		}
		if got != "https://checkout.stripe.com/c/pay/cs_1" {
			t.Errorf("url = %q", got)
		}
		want := map[string]string{
			"mode":                                 "subscription",
			"line_items[0][price]":                 "price_annual",
			"line_items[0][quantity]":              "1",
			"success_url":                          "https://a/ok",
			"cancel_url":                           "https://a/no",
			"client_reference_id":                  "user-9",
			"metadata[user_id]":                    "user-9",
			"subscription_data[metadata][user_id]": "user-9",
			"customer":                             "cus_1",
			"subscription_data[trial_period_days]": "14",
		}
		for k, v := range want {
			if form.Get(k) != v {
				t.Errorf("form[%q] = %q, want %q", k, form.Get(k), v)
			}
		}
	})

	t.Run("omits customer and trial_period_days when not set", func(t *testing.T) {
		var form url.Values
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			form, _ = url.ParseQuery(string(raw))
			_, _ = w.Write([]byte(`{"id":"cs_2","url":"https://checkout.stripe.com/c/pay/cs_2"}`))
		}))
		defer srv.Close()

		c := NewClient("sk_test", "wh", "price_annual", rewriteClient(t, srv.URL))
		if _, err := c.CreateCheckoutSession(context.Background(), port.CheckoutParams{
			UserID: "user-9", SuccessURL: "https://a/ok", CancelURL: "https://a/no",
		}); err != nil {
			t.Fatal(err)
		}
		if _, ok := form["customer"]; ok {
			t.Errorf("form had customer = %q, want no customer field", form.Get("customer"))
		}
		if _, ok := form["subscription_data[trial_period_days]"]; ok {
			t.Errorf("form had trial_period_days = %q, want no trial field", form.Get("subscription_data[trial_period_days]"))
		}
	})
}

func TestCreatePortalSession(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/billing_portal/sessions" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		form, _ = url.ParseQuery(string(raw))
		_, _ = w.Write([]byte(`{"id":"bps_1","url":"https://billing.stripe.com/p/session/bps_1"}`))
	}))
	defer srv.Close()

	c := NewClient("sk_test", "wh", "price_x", rewriteClient(t, srv.URL))
	got, err := c.CreatePortalSession(context.Background(), "cus_1", "https://a/return")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://billing.stripe.com/p/session/bps_1" {
		t.Errorf("url = %q", got)
	}
	if form.Get("customer") != "cus_1" || form.Get("return_url") != "https://a/return" {
		t.Errorf("form = %v", form)
	}
}

// TestDoMapsErrorStatuses checks the shared do() sentinel mapping.
func TestDoMapsErrorStatuses(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   error
	}{
		{"unauthorized", http.StatusUnauthorized, domain.ErrUnauthorized},
		{"forbidden", http.StatusForbidden, domain.ErrUnauthorized},
		{"not found", http.StatusNotFound, domain.ErrNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"nope"}}`))
			}))
			defer srv.Close()
			c := NewClient("sk_test", "wh", "price_x", rewriteClient(t, srv.URL))
			_, err := c.CreatePortalSession(context.Background(), "cus_1", "https://a/r")
			if !errors.Is(err, tc.want) {
				t.Fatalf("status %d: err = %v, want wrap of %v", tc.status, err, tc.want)
			}
		})
	}

	t.Run("unmapped status still returns a descriptive error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"type":"api_error","message":"boom"}}`))
		}))
		defer srv.Close()
		c := NewClient("sk_test", "wh", "price_x", rewriteClient(t, srv.URL))
		_, err := c.CreatePortalSession(context.Background(), "cus_1", "https://a/r")
		if err == nil {
			t.Fatal("want error")
		}
		if errors.Is(err, domain.ErrUnauthorized) || errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want neither sentinel", err)
		}
		if !strings.Contains(err.Error(), "stripeapi: http 500") {
			t.Fatalf("err = %v, want to contain %q", err, "stripeapi: http 500")
		}
	})
}
