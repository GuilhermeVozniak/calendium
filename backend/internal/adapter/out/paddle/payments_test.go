package paddle

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

func strptr(s string) *string { return &s }

func TestEnsureCustomer(t *testing.T) {
	t.Run("creates when the email lookup is empty", func(t *testing.T) {
		var gotEmailQuery string
		var created map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/customers":
				gotEmailQuery = r.URL.Query().Get("email")
				_, _ = w.Write([]byte(`{"data":[],"meta":{"pagination":{"has_more":false}}}`))
			case r.Method == http.MethodPost && r.URL.Path == "/customers":
				raw, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(raw, &created)
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"data":{"id":"ctm_new","email":"ada@x.com"}}`))
			default:
				t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			}
		}))
		defer srv.Close()
		id, err := newTestClient(t, srv).EnsureCustomer(context.Background(), domain.User{ID: "user-9", Email: "ada@x.com", Name: strptr("Ada")})
		if err != nil {
			t.Fatalf("EnsureCustomer: %v", err)
		}
		if id != "ctm_new" {
			t.Fatalf("id = %q", id)
		}
		if gotEmailQuery != "ada@x.com" {
			t.Fatalf("lookup email = %q", gotEmailQuery)
		}
		if created["email"] != "ada@x.com" || created["name"] != "Ada" {
			t.Fatalf("create body = %v", created)
		}
	})

	t.Run("returns the existing customer without creating", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("unexpected create call: %s %s", r.Method, r.URL.Path)
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"ctm_existing","email":"a@b.com"}]}`))
		}))
		defer srv.Close()
		id, err := newTestClient(t, srv).EnsureCustomer(context.Background(), domain.User{ID: "u", Email: "a@b.com"})
		if err != nil {
			t.Fatal(err)
		}
		if id != "ctm_existing" {
			t.Fatalf("id = %q", id)
		}
	})

	t.Run("omits name when the user has none", func(t *testing.T) {
		var created map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`{"data":[]}`))
				return
			}
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &created)
			_, _ = w.Write([]byte(`{"data":{"id":"ctm_noname"}}`))
		}))
		defer srv.Close()
		if _, err := newTestClient(t, srv).EnsureCustomer(context.Background(), domain.User{ID: "u", Email: "n@x.com"}); err != nil {
			t.Fatal(err)
		}
		if _, ok := created["name"]; ok {
			t.Fatalf("create body had name = %v, want none", created["name"])
		}
	})
}

func TestCreateCheckout(t *testing.T) {
	t.Run("posts the annual item, customer and custom_data and returns checkout.url", func(t *testing.T) {
		var body map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/transactions" {
				t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
			}
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":"txn_1","status":"ready","checkout":{"url":"https://app.example/checkout?_ptxn=txn_1"}}}`))
		}))
		defer srv.Close()
		url, err := newTestClient(t, srv).CreateCheckout(context.Background(), port.CheckoutParams{UserID: "user-9", CustomerID: "ctm_1"})
		if err != nil {
			t.Fatal(err)
		}
		if url != "https://app.example/checkout?_ptxn=txn_1" {
			t.Fatalf("url = %q", url)
		}
		items, _ := body["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("items = %v", body["items"])
		}
		item, _ := items[0].(map[string]any)
		if item["price_id"] != "pri_annual" || item["quantity"] != float64(1) {
			t.Fatalf("item = %v", item)
		}
		if body["customer_id"] != "ctm_1" {
			t.Fatalf("customer_id = %v", body["customer_id"])
		}
		custom, _ := body["custom_data"].(map[string]any)
		if custom["user_id"] != "user-9" {
			t.Fatalf("custom_data = %v", body["custom_data"])
		}
		for _, forbidden := range []string{"success_url", "cancel_url", "return_url"} {
			if _, ok := body[forbidden]; ok {
				t.Fatalf("body must not carry %s (no client URLs)", forbidden)
			}
		}
	})

	t.Run("fails loudly when the transaction has no checkout url", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"data":{"id":"txn_2","checkout":{"url":null}}}`))
		}))
		defer srv.Close()
		_, err := newTestClient(t, srv).CreateCheckout(context.Background(), port.CheckoutParams{UserID: "u", CustomerID: "ctm_1"})
		if err == nil {
			t.Fatal("want error when the default payment link is unset")
		}
	})
}
