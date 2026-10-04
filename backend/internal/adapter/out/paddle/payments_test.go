package paddle

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
		var gotMethod, gotPath string // recorded here, asserted on the test goroutine
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotMethod, gotPath = r.Method, r.URL.Path
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":"txn_1","status":"ready","checkout":{"url":"https://app.example/checkout?_ptxn=txn_1"}}}`))
		}))
		defer srv.Close()
		url, err := newTestClient(t, srv).CreateCheckout(context.Background(), port.CheckoutParams{UserID: "user-9", CustomerID: "ctm_1"})
		if gotMethod != http.MethodPost || gotPath != "/transactions" {
			t.Fatalf("request = %s %s, want POST /transactions", gotMethod, gotPath)
		}
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

func TestCreatePortalSession(t *testing.T) {
	t.Run("returns overview plus per-subscription cancel/update links", func(t *testing.T) {
		var gotPath string
		var body map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":"cpls_1","urls":{"general":{"overview":"https://portal/overview"},"subscriptions":[{"id":"sub_1","cancel_subscription":"https://portal/cancel","update_subscription_payment_method":"https://portal/update","view_subscription":"https://portal/view"}]}}}`))
		}))
		defer srv.Close()
		urls, err := newTestClient(t, srv).CreatePortalSession(context.Background(), "ctm_1", "sub_1")
		if err != nil {
			t.Fatal(err)
		}
		if gotPath != "/customers/ctm_1/portal-sessions" {
			t.Fatalf("path = %q", gotPath)
		}
		ids, _ := body["subscription_ids"].([]any)
		if len(ids) != 1 || ids[0] != "sub_1" {
			t.Fatalf("subscription_ids = %v", body["subscription_ids"])
		}
		want := port.PortalURLs{Overview: "https://portal/overview", Cancel: "https://portal/cancel", UpdatePayment: "https://portal/update"}
		if urls != want {
			t.Fatalf("urls = %+v, want %+v", urls, want)
		}
	})

	t.Run("without a subscription id sends no ids and leaves cancel/update empty", func(t *testing.T) {
		var body map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			_, _ = w.Write([]byte(`{"data":{"urls":{"general":{"overview":"https://portal/overview"},"subscriptions":[]}}}`))
		}))
		defer srv.Close()
		urls, err := newTestClient(t, srv).CreatePortalSession(context.Background(), "ctm_1", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := body["subscription_ids"]; ok {
			t.Fatalf("body must omit subscription_ids, got %v", body)
		}
		if urls.Overview != "https://portal/overview" || urls.Cancel != "" || urls.UpdatePayment != "" {
			t.Fatalf("urls = %+v", urls)
		}
	})
}

func TestGetSubscription(t *testing.T) {
	t.Run("normalizes period, customer, custom_data and scheduled cancel", func(t *testing.T) {
		var gotMethod, gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotMethod, gotPath = r.Method, r.URL.EscapedPath()
			_, _ = w.Write([]byte(`{"data":{
				"id":"sub_1","status":"active","customer_id":"ctm_1",
				"custom_data":{"user_id":"user-9"},
				"current_billing_period":{"starts_at":"2026-10-01T00:00:00Z","ends_at":"2027-10-01T00:00:00Z"},
				"next_billed_at":"2027-10-01T00:00:00Z",
				"scheduled_change":{"action":"cancel","effective_at":"2027-10-01T00:00:00Z"},
				"items":[{"price":{"id":"pri_annual"}}]}}`))
		}))
		defer srv.Close()
		ev, err := newTestClient(t, srv).GetSubscription(context.Background(), "sub/1")
		if err != nil {
			t.Fatal(err)
		}
		if gotMethod != http.MethodGet || gotPath != "/subscriptions/sub%2F1" {
			t.Fatalf("request = %s %s", gotMethod, gotPath)
		}
		if ev.SubscriptionID != "sub_1" || ev.CustomerID != "ctm_1" || ev.UserID != "user-9" {
			t.Fatalf("ids = %+v", ev)
		}
		if ev.Status != domain.SubscriptionActive || !ev.CancelAtPeriodEnd {
			t.Fatalf("state = %+v", ev)
		}
		if ev.CurrentPeriodEnd == nil || ev.CurrentPeriodEnd.Format(time.RFC3339) != "2027-10-01T00:00:00Z" {
			t.Fatalf("CurrentPeriodEnd = %v", ev.CurrentPeriodEnd)
		}
		if ev.Type != "subscription.reconciled" || !ev.OccurredAt.IsZero() || ev.Ignored {
			t.Fatalf("reconcile read must have Type subscription.reconciled, zero OccurredAt, not Ignored: %+v", ev)
		}
	})

	t.Run("propagates not found", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"type":"request_error","code":"entity_not_found","detail":"gone"}}`))
		}))
		defer srv.Close()
		_, err := newTestClient(t, srv).GetSubscription(context.Background(), "sub_missing")
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want wrap of ErrNotFound", err)
		}
	})
}

func TestCancelSubscription(t *testing.T) {
	for _, tc := range []struct {
		name        string
		immediately bool
		want        string
	}{
		{"end of period", false, "next_billing_period"},
		{"immediately", true, "immediately"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			var body map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				raw, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(raw, &body)
				_, _ = w.Write([]byte(`{"data":{"id":"sub_1","status":"active"}}`))
			}))
			defer srv.Close()
			if err := newTestClient(t, srv).CancelSubscription(context.Background(), "sub_1", tc.immediately); err != nil {
				t.Fatal(err)
			}
			if gotPath != "/subscriptions/sub_1/cancel" {
				t.Fatalf("path = %q", gotPath)
			}
			if body["effective_from"] != tc.want {
				t.Fatalf("effective_from = %v, want %q", body["effective_from"], tc.want)
			}
		})
	}
}

// TestMapStatus is the full Paddle → domain table, including the defensive
// trialing→active mapping and fail-closed default.
func TestMapStatus(t *testing.T) {
	tests := []struct {
		in   string
		want domain.SubscriptionStatus
	}{
		{"active", domain.SubscriptionActive},
		{"trialing", domain.SubscriptionActive},
		{"past_due", domain.SubscriptionPastDue},
		{"paused", domain.SubscriptionPaused},
		{"canceled", domain.SubscriptionCanceled},
		{"", domain.SubscriptionNone},
		{"some_future_status", domain.SubscriptionNone},
	}
	for _, tc := range tests {
		t.Run("status/"+tc.in, func(t *testing.T) {
			if got := mapStatus(tc.in); got != tc.want {
				t.Fatalf("mapStatus(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
