package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// This file covers the paid (SelfHosted=false) Paddle flow in billing.go.
// Self-host bypass is covered by selfhost_test.go.

type billingHarness struct {
	users    *fakeUserRepo
	subs     *fakeSubscriptionRepo
	events   *fakeBillingEventRepo
	payments *fakePayments
	clock    *fakeClock
	tx       *fakeTxRunner
	svc      *BillingService
}

func newBillingHarness(now time.Time) *billingHarness {
	h := &billingHarness{
		users:    newUserRepo(),
		subs:     newSubscriptionRepo(),
		events:   newBillingEventRepo(),
		payments: newPayments(),
		clock:    newClock(now),
		tx:       newTxRunner(),
	}
	h.svc = NewBillingService(BillingServiceDeps{
		Users: h.users, Subs: h.subs, Events: h.events, Payments: h.payments,
		Clock: h.clock, Tx: h.tx, SelfHosted: false,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return h
}

func (h *billingHarness) seedUser(t *testing.T, id string, createdAt time.Time) {
	t.Helper()
	if _, err := h.users.Upsert(context.Background(), domain.User{ID: id, Email: id + "@example.com", CreatedAt: createdAt}); err != nil {
		t.Fatal(err)
	}
}

func (h *billingHarness) seedSub(t *testing.T, s domain.Subscription) {
	t.Helper()
	if err := h.subs.Upsert(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	h.subs.upsertCalls = 0
}

func tptr(v time.Time) *time.Time { return &v }

// --- GetSubscription: trial grant -------------------------------------------

func TestGetSubscriptionGrantsTrialAnchoredToSignup(t *testing.T) {
	ctx := context.Background()
	signup := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	h := newBillingHarness(signup.Add(3 * time.Hour)) // first call hours after signup
	h.seedUser(t, "u1", signup)

	sub, err := h.svc.GetSubscription(ctx, "u1")
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}
	if sub.Status != domain.SubscriptionTrialing {
		t.Fatalf("status = %q, want trialing", sub.Status)
	}
	want := signup.Add(domain.TrialLength)
	if sub.TrialEndsAt == nil || !sub.TrialEndsAt.Equal(want) {
		t.Fatalf("TrialEndsAt = %v, want created_at+14d = %v (anchored to signup, not to the call)", sub.TrialEndsAt, want)
	}
	if sub.UserID != "u1" || sub.Plan != domain.PlanAnnual || sub.PriceUSD != domain.PriceUSDAnnual {
		t.Fatalf("row = %+v", sub)
	}
	if h.subs.ensureTrialCalls != 1 {
		t.Fatalf("ensureTrialCalls = %d, want 1", h.subs.ensureTrialCalls)
	}
	if _, err := h.svc.GetSubscription(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if h.subs.ensureTrialCalls != 1 {
		t.Fatalf("second read must not call EnsureTrial again, calls = %d", h.subs.ensureTrialCalls)
	}
}

func TestGetSubscriptionDoesNotRegrantAnExistingRow(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	h.seedUser(t, "u1", now.Add(-30*24*time.Hour))
	h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionCanceled, BillingCustomerID: "ctm_1"})

	sub, err := h.svc.GetSubscription(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if sub.Status != domain.SubscriptionCanceled || sub.TrialEndsAt != nil {
		t.Fatalf("existing row must be returned unmodified: %+v", sub)
	}
	if h.subs.ensureTrialCalls != 0 {
		t.Fatalf("EnsureTrial must not run for an existing row")
	}
}

func TestGetSubscriptionUnknownUserPropagatesNotFound(t *testing.T) {
	h := newBillingHarness(time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC))
	if _, err := h.svc.GetSubscription(context.Background(), "ghost"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound (no users row to anchor the trial)", err)
	}
	if h.subs.ensureTrialCalls != 0 {
		t.Fatal("EnsureTrial must not run without a users row")
	}
}

// --- RequireActive: entitlement matrix with typed 402 -----------------------

func TestRequireActiveMatrix(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	tests := []struct {
		name   string
		seed   *domain.Subscription // nil = no row
		want   domain.DenialReason  // "" = access granted
		wantTE *time.Time
		wantPE *time.Time
	}{
		{"no row", nil, domain.DenialNone, nil, nil},
		{"trialing in trial", &domain.Subscription{Status: domain.SubscriptionTrialing, TrialEndsAt: tptr(future)}, "", nil, nil},
		{"trialing ended", &domain.Subscription{Status: domain.SubscriptionTrialing, TrialEndsAt: tptr(now.Add(-time.Minute))}, domain.DenialTrialEnded, tptr(now.Add(-time.Minute)), nil},
		{"active no period", &domain.Subscription{Status: domain.SubscriptionActive}, "", nil, nil},
		{"active past grace", &domain.Subscription{Status: domain.SubscriptionActive, CurrentPeriodEnd: tptr(now.Add(-domain.ActiveGrace))}, domain.DenialPastDue, nil, tptr(now.Add(-domain.ActiveGrace))},
		{"past_due in grace", &domain.Subscription{Status: domain.SubscriptionPastDue, CurrentPeriodEnd: tptr(now)}, "", nil, nil},
		{"past_due beyond grace", &domain.Subscription{Status: domain.SubscriptionPastDue, CurrentPeriodEnd: tptr(now.Add(-domain.PastDueGrace))}, domain.DenialPastDue, nil, tptr(now.Add(-domain.PastDueGrace))},
		{"paused", &domain.Subscription{Status: domain.SubscriptionPaused, CurrentPeriodEnd: tptr(future)}, domain.DenialPaused, nil, tptr(future)},
		{"canceled", &domain.Subscription{Status: domain.SubscriptionCanceled}, domain.DenialCanceled, nil, nil},
		{"none", &domain.Subscription{Status: domain.SubscriptionNone}, domain.DenialNone, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newBillingHarness(now)
			if tt.seed != nil {
				s := *tt.seed
				s.UserID = "u1"
				h.seedSub(t, s)
			}
			err := h.svc.RequireActive(context.Background(), "u1")
			if tt.want == "" {
				if err != nil {
					t.Fatalf("RequireActive = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, domain.ErrPaymentRequired) {
				t.Fatalf("err = %v, want ErrPaymentRequired", err)
			}
			var pr *domain.PaymentRequiredError
			if !errors.As(err, &pr) {
				t.Fatalf("err = %v, want *domain.PaymentRequiredError", err)
			}
			if pr.Reason != tt.want {
				t.Fatalf("Reason = %q, want %q", pr.Reason, tt.want)
			}
			if (pr.TrialEndsAt == nil) != (tt.wantTE == nil) || (pr.TrialEndsAt != nil && !pr.TrialEndsAt.Equal(*tt.wantTE)) {
				t.Fatalf("TrialEndsAt = %v, want %v", pr.TrialEndsAt, tt.wantTE)
			}
			if (pr.CurrentPeriodEnd == nil) != (tt.wantPE == nil) || (pr.CurrentPeriodEnd != nil && !pr.CurrentPeriodEnd.Equal(*tt.wantPE)) {
				t.Fatalf("CurrentPeriodEnd = %v, want %v", pr.CurrentPeriodEnd, tt.wantPE)
			}
		})
	}
}

// --- CreateCheckout -----------------------------------------------------------

func TestCreateCheckoutFirstTimeCreatesCustomerAndTrialRow(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	h.seedUser(t, "u1", now.Add(-time.Hour))
	h.payments.customerID = "ctm_new"
	h.payments.checkoutURL = "https://app/checkout?_ptxn=txn_1"

	url, err := h.svc.CreateCheckout(ctx, "u1")
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	if url != "https://app/checkout?_ptxn=txn_1" {
		t.Fatalf("url = %q", url)
	}
	if h.payments.ensureCustomerCalls != 1 || h.payments.lastEnsureUser.ID != "u1" {
		t.Fatalf("EnsureCustomer calls = %d (user %q), want 1 for u1", h.payments.ensureCustomerCalls, h.payments.lastEnsureUser.ID)
	}
	if h.payments.lastCheckoutParams != (port.CheckoutParams{UserID: "u1", CustomerID: "ctm_new"}) {
		t.Fatalf("CheckoutParams = %+v", h.payments.lastCheckoutParams)
	}
	row, err := h.subs.GetByUserID(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != domain.SubscriptionTrialing || row.BillingCustomerID != "ctm_new" {
		t.Fatalf("row = %+v, want trialing with the customer id persisted", row)
	}
}

func TestCreateCheckoutReusesStoredCustomer(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	h.seedUser(t, "u1", now)
	h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionTrialing, TrialEndsAt: tptr(now.Add(24 * time.Hour)), BillingCustomerID: "ctm_old"})
	h.payments.checkoutURL = "https://co"

	if _, err := h.svc.CreateCheckout(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if h.payments.ensureCustomerCalls != 0 {
		t.Fatalf("EnsureCustomer must not run when a customer id is stored")
	}
	if h.payments.lastCheckoutParams.CustomerID != "ctm_old" {
		t.Fatalf("CustomerID = %q, want ctm_old", h.payments.lastCheckoutParams.CustomerID)
	}
}

func TestCreateCheckoutRefusesLiveSubscriptions(t *testing.T) {
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	for _, status := range []domain.SubscriptionStatus{domain.SubscriptionActive, domain.SubscriptionPastDue, domain.SubscriptionPaused} {
		t.Run(string(status), func(t *testing.T) {
			h := newBillingHarness(now)
			h.seedUser(t, "u1", now)
			h.seedSub(t, domain.Subscription{UserID: "u1", Status: status, BillingCustomerID: "ctm_1", BillingSubscriptionID: "sub_1"})
			_, err := h.svc.CreateCheckout(context.Background(), "u1")
			if !errors.Is(err, domain.ErrAlreadySubscribed) {
				t.Fatalf("err = %v, want ErrAlreadySubscribed", err)
			}
			if h.payments.ensureCustomerCalls != 0 || h.payments.lastCheckoutParams != (port.CheckoutParams{}) {
				t.Fatal("no provider call may happen for an already-subscribed user")
			}
		})
	}
	t.Run("canceled with an old subscription id may resubscribe", func(t *testing.T) {
		h := newBillingHarness(now)
		h.seedUser(t, "u1", now)
		h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionCanceled, BillingCustomerID: "ctm_1", BillingSubscriptionID: "sub_old"})
		h.payments.checkoutURL = "https://co"
		if _, err := h.svc.CreateCheckout(context.Background(), "u1"); err != nil {
			t.Fatalf("canceled user must be able to resubscribe: %v", err)
		}
	})
}

func TestCreateCheckoutMapsProviderFailuresTo502(t *testing.T) {
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	t.Run("EnsureCustomer fails", func(t *testing.T) {
		h := newBillingHarness(now)
		h.seedUser(t, "u1", now)
		h.payments.ensureErr = errors.New("paddle: http 500")
		_, err := h.svc.CreateCheckout(context.Background(), "u1")
		if !errors.Is(err, domain.ErrBillingUnavailable) {
			t.Fatalf("err = %v, want ErrBillingUnavailable", err)
		}
	})
	t.Run("CreateCheckout fails", func(t *testing.T) {
		h := newBillingHarness(now)
		h.seedUser(t, "u1", now)
		h.payments.customerID = "ctm_1"
		h.payments.checkoutErr = errors.New("paddle: http 503")
		_, err := h.svc.CreateCheckout(context.Background(), "u1")
		if !errors.Is(err, domain.ErrBillingUnavailable) {
			t.Fatalf("err = %v, want ErrBillingUnavailable", err)
		}
	})
}

// --- CreatePortalSession ----------------------------------------------------

func TestCreatePortalSession(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	full := port.PortalURLs{Overview: "https://p/o", Cancel: "https://p/c", UpdatePayment: "https://p/u"}

	t.Run("no row is no_billing_profile", func(t *testing.T) {
		h := newBillingHarness(now)
		_, err := h.svc.CreatePortalSession(ctx, "u1")
		if !errors.Is(err, domain.ErrNoBillingProfile) {
			t.Fatalf("err = %v, want ErrNoBillingProfile", err)
		}
	})
	t.Run("row without customer is no_billing_profile", func(t *testing.T) {
		h := newBillingHarness(now)
		h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionTrialing})
		_, err := h.svc.CreatePortalSession(ctx, "u1")
		if !errors.Is(err, domain.ErrNoBillingProfile) {
			t.Fatalf("err = %v, want ErrNoBillingProfile", err)
		}
		if h.payments.lastPortalCustomerID != "" {
			t.Fatal("provider must not be called")
		}
	})
	t.Run("customer without subscription returns overview only", func(t *testing.T) {
		h := newBillingHarness(now)
		h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionTrialing, BillingCustomerID: "ctm_1"})
		h.payments.portalURLs = full // provider might echo links; service blanks them
		urls, err := h.svc.CreatePortalSession(ctx, "u1")
		if err != nil {
			t.Fatal(err)
		}
		if h.payments.lastPortalCustomerID != "ctm_1" || h.payments.lastPortalSubscriptionID != "" {
			t.Fatalf("provider args = %q/%q", h.payments.lastPortalCustomerID, h.payments.lastPortalSubscriptionID)
		}
		if urls != (port.PortalURLs{Overview: "https://p/o"}) {
			t.Fatalf("urls = %+v, want cancel/update blanked without a subscription id", urls)
		}
	})
	t.Run("customer with subscription passes every link through", func(t *testing.T) {
		h := newBillingHarness(now)
		h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive, BillingCustomerID: "ctm_1", BillingSubscriptionID: "sub_1"})
		h.payments.portalURLs = full
		urls, err := h.svc.CreatePortalSession(ctx, "u1")
		if err != nil {
			t.Fatal(err)
		}
		if h.payments.lastPortalSubscriptionID != "sub_1" || urls != full {
			t.Fatalf("urls = %+v (sub id %q)", urls, h.payments.lastPortalSubscriptionID)
		}
	})
	t.Run("provider failure is 502", func(t *testing.T) {
		h := newBillingHarness(now)
		h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive, BillingCustomerID: "ctm_1"})
		h.payments.portalErr = errors.New("paddle: http 500")
		_, err := h.svc.CreatePortalSession(ctx, "u1")
		if !errors.Is(err, domain.ErrBillingUnavailable) {
			t.Fatalf("err = %v, want ErrBillingUnavailable", err)
		}
	})
}

// Keep the port import used by later tasks' tests in this file.
var _ port.SubscriptionEvent
