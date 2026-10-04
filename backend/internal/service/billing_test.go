package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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
	logs     *bytes.Buffer
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
		logs:     &bytes.Buffer{},
	}
	h.svc = NewBillingService(BillingServiceDeps{
		Users: h.users, Subs: h.subs, Events: h.events, Payments: h.payments,
		Clock: h.clock, Tx: h.tx, SelfHosted: false,
		Logger: slog.New(slog.NewTextHandler(h.logs, nil)),
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

// --- RequireActive: lazy trial grant for brand-new users -------------------

// A brand-new user whose first call is a gated one (before any client has
// fetched GET /v1/billing/subscription) gets the signup trial from the gate
// itself, anchored to users.created_at exactly like GetSubscription.
func TestRequireActiveGrantsTrialToBrandNewUser(t *testing.T) {
	ctx := context.Background()
	signup := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	h := newBillingHarness(signup.Add(time.Minute))
	h.seedUser(t, "u1", signup)

	if err := h.svc.RequireActive(ctx, "u1"); err != nil {
		t.Fatalf("first gated call of a new user = %v, want allowed (trial)", err)
	}
	sub, err := h.subs.GetByUserID(ctx, "u1")
	if err != nil {
		t.Fatalf("trial row not created: %v", err)
	}
	if want := signup.Add(domain.TrialLength); sub.Status != domain.SubscriptionTrialing || sub.TrialEndsAt == nil || !sub.TrialEndsAt.Equal(want) {
		t.Fatalf("row = %+v, want trialing until created_at+14d = %v", sub, want)
	}
	if h.subs.ensureTrialCalls != 1 {
		t.Fatalf("ensureTrialCalls = %d, want 1", h.subs.ensureTrialCalls)
	}
}

// The lazy grant is anchored to signup, so an old account without a row is
// not handed a fresh trial by the gate.
func TestRequireActiveLazyTrialIsAnchoredToSignup(t *testing.T) {
	now := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	h.seedUser(t, "u1", now.Add(-20*24*time.Hour))

	var pr *domain.PaymentRequiredError
	if err := h.svc.RequireActive(context.Background(), "u1"); !errors.As(err, &pr) || pr.Reason != domain.DenialTrialEnded {
		t.Fatalf("err = %v, want 402 trial_ended for a 20-day-old account", err)
	}
}

// Every gated service shares entitlement; the grant works through it
// directly, and an unknown user (no users row to anchor) stays 402 none.
func TestEntitlementLazyTrialGrant(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	users, subs := newUserRepo(), newSubscriptionRepo()
	if _, err := users.Upsert(ctx, domain.User{ID: "u1", Email: "u1@example.com", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	ent := entitlement{subs: subs, users: users, clock: newClock(now)}
	if err := ent.require(ctx, "u1"); err != nil {
		t.Fatalf("new user = %v, want allowed", err)
	}
	var pr *domain.PaymentRequiredError
	if err := ent.require(ctx, "ghost"); !errors.As(err, &pr) || pr.Reason != domain.DenialNone {
		t.Fatalf("unknown user = %v, want 402 none", err)
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

// Controller ruling (Track A review): postgres Upsert writes trial_ends_at
// and last_event_at verbatim on every call, and ListForReconciliation
// exempts NULL last_event_at. The checkout upsert that persists the customer
// id must therefore carry the trial end through and stamp last_event_at.
func TestCreateCheckoutUpsertKeepsTrialAndStampsLastEventAt(t *testing.T) {
	ctx := context.Background()
	signup := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	now := signup.Add(2 * 24 * time.Hour)
	h := newBillingHarness(now)
	h.seedUser(t, "u1", signup)
	h.payments.customerID = "ctm_new"
	h.payments.checkoutURL = "https://co"

	if _, err := h.svc.CreateCheckout(ctx, "u1"); err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	row, err := h.subs.GetByUserID(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if row.TrialEndsAt == nil || !row.TrialEndsAt.Equal(signup.Add(domain.TrialLength)) {
		t.Fatalf("TrialEndsAt = %v, want the granted trial end preserved through the checkout upsert", row.TrialEndsAt)
	}
	if row.LastEventAt == nil || !row.LastEventAt.Equal(now) {
		t.Fatalf("LastEventAt = %v, want now (every service write stamps a non-zero last_event_at)", row.LastEventAt)
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

// --- HandleWebhook ----------------------------------------------------------

func subEvent(ntf, evt string, at time.Time, status domain.SubscriptionStatus) port.SubscriptionEvent {
	return port.SubscriptionEvent{
		NotificationID: ntf, EventID: evt, Type: "subscription.updated", OccurredAt: at,
		CustomerID: "ctm_1", SubscriptionID: "sub_1", UserID: "u1", Status: status,
	}
}

func TestHandleWebhookBadSignatureIsUnauthorizedBeforeAnyTx(t *testing.T) {
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive, BillingCustomerID: "ctm_1"})
	h.payments.parseWebhookErr = errors.New("paddle: no matching h1 signature")

	err := h.svc.HandleWebhook(context.Background(), []byte("{}"), "ts=1;h1=00")
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if h.tx.calls != 0 || len(h.events.seen) != 0 {
		t.Fatal("a rejected signature must not open a tx or record anything")
	}
	if !h.payments.lastParseNow.Equal(now) {
		t.Fatalf("ParseWebhook now = %v, want the service clock %v", h.payments.lastParseNow, now)
	}
}

func TestHandleWebhookMalformedBodyIsValidation(t *testing.T) {
	h := newBillingHarness(time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC))
	h.payments.parseWebhookErr = fmt.Errorf("%w: paddle: decode webhook envelope", domain.ErrValidation)
	err := h.svc.HandleWebhook(context.Background(), []byte("nope"), "ts=1;h1=00")
	if !errors.Is(err, domain.ErrValidation) || errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrValidation (not unauthorized)", err)
	}
}

func TestHandleWebhookIgnoredTypeIsAcknowledgedWithoutTx(t *testing.T) {
	h := newBillingHarness(time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC))
	h.payments.webhookEvent = port.SubscriptionEvent{NotificationID: "ntf_t", EventID: "evt_t", Type: "transaction.completed", Ignored: true}
	if err := h.svc.HandleWebhook(context.Background(), []byte("{}"), "sig"); err != nil {
		t.Fatalf("ignored event must be a 200 no-op, got %v", err)
	}
	if h.tx.calls != 0 || len(h.events.seen) != 0 || len(h.subs.byUser) != 0 {
		t.Fatal("ignored events must not touch storage")
	}
}

func TestHandleWebhookAppliesAndClearsTrial(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionTrialing, TrialEndsAt: tptr(now.Add(5 * 24 * time.Hour)), BillingCustomerID: "ctm_1"})
	periodEnd := now.Add(365 * 24 * time.Hour)
	ev := subEvent("ntf_1", "evt_1", now.Add(-time.Minute), domain.SubscriptionActive)
	ev.Type = "subscription.activated"
	ev.CurrentPeriodEnd = &periodEnd
	h.payments.webhookEvent = ev

	if err := h.svc.HandleWebhook(ctx, []byte("{}"), "sig"); err != nil {
		t.Fatalf("HandleWebhook: %v", err)
	}
	got, err := h.subs.GetByUserID(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.SubscriptionActive || got.BillingSubscriptionID != "sub_1" || got.BillingCustomerID != "ctm_1" {
		t.Fatalf("row = %+v", got)
	}
	if got.TrialEndsAt != nil {
		t.Fatalf("TrialEndsAt = %v, want nil once a Paddle subscription exists", got.TrialEndsAt)
	}
	if got.CurrentPeriodEnd == nil || !got.CurrentPeriodEnd.Equal(periodEnd) {
		t.Fatalf("CurrentPeriodEnd = %v", got.CurrentPeriodEnd)
	}
	if got.LastEventAt == nil || !got.LastEventAt.Equal(ev.OccurredAt) {
		t.Fatalf("LastEventAt = %v, want %v", got.LastEventAt, ev.OccurredAt)
	}
	if _, ok := h.events.seen["ntf_1"]; !ok || h.tx.calls != 1 {
		t.Fatalf("event must be recorded inside exactly one tx (seen=%v tx=%d)", h.events.seen, h.tx.calls)
	}
}

func TestHandleWebhookDuplicateNotificationIsNoop(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive, BillingCustomerID: "ctm_1", LastEventAt: tptr(now.Add(-time.Hour))})
	if _, err := h.events.Record(ctx, port.SubscriptionEvent{NotificationID: "ntf_dup"}); err != nil {
		t.Fatal(err)
	}
	h.payments.webhookEvent = subEvent("ntf_dup", "evt_1", now, domain.SubscriptionCanceled)

	if err := h.svc.HandleWebhook(ctx, []byte("{}"), "sig"); err != nil {
		t.Fatal(err)
	}
	got, _ := h.subs.GetByUserID(ctx, "u1")
	if got.Status != domain.SubscriptionActive || h.subs.upsertCalls != 0 {
		t.Fatalf("duplicate notification must not re-apply: %+v (upserts=%d)", got, h.subs.upsertCalls)
	}
}

// Review Focus: a replay (same event id, NEW notification id) whose
// occurred_at EQUALS last_event_at must be applied (strict < guard).
func TestHandleWebhookReplayAtSameInstantApplies(t *testing.T) {
	ctx := context.Background()
	t1 := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	h := newBillingHarness(t1.Add(time.Minute))
	h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive, BillingCustomerID: "ctm_1", BillingSubscriptionID: "sub_1", LastEventAt: tptr(t1)})
	h.payments.webhookEvent = subEvent("ntf_replay", "evt_1", t1, domain.SubscriptionCanceled)

	if err := h.svc.HandleWebhook(ctx, []byte("{}"), "sig"); err != nil {
		t.Fatal(err)
	}
	got, _ := h.subs.GetByUserID(ctx, "u1")
	if got.Status != domain.SubscriptionCanceled {
		t.Fatalf("status = %q, want canceled (same-instant replay must apply)", got.Status)
	}
}

func TestHandleWebhookOlderEventIsDroppedButRecorded(t *testing.T) {
	ctx := context.Background()
	t1 := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	h := newBillingHarness(t2)
	h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive, BillingCustomerID: "ctm_1", BillingSubscriptionID: "sub_1", LastEventAt: tptr(t2)})
	h.payments.webhookEvent = subEvent("ntf_old", "evt_old", t1, domain.SubscriptionCanceled)

	if err := h.svc.HandleWebhook(ctx, []byte("{}"), "sig"); err != nil {
		t.Fatal(err)
	}
	got, _ := h.subs.GetByUserID(ctx, "u1")
	if got.Status != domain.SubscriptionActive || !got.LastEventAt.Equal(t2) {
		t.Fatalf("older event must not revert state: %+v", got)
	}
	if _, ok := h.events.seen["ntf_old"]; !ok {
		t.Fatal("the dropped notification must still be recorded so its retry is a no-op")
	}
}

// The ordering guard lives in the repo write, not in a read-then-compare:
// a newer event committed between applyEvent's read and its upsert (two
// notifications in flight at once) must not be overwritten by the older one.
func TestHandleWebhookStaleEventLosesToConcurrentNewerWrite(t *testing.T) {
	ctx := context.Background()
	t1 := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	t2, t3 := t1.Add(time.Hour), t1.Add(2*time.Hour)
	h := newBillingHarness(t3)
	h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive, BillingCustomerID: "ctm_1", BillingSubscriptionID: "sub_1", LastEventAt: tptr(t1)})
	h.payments.webhookEvent = subEvent("ntf_t2", "evt_t2", t2, domain.SubscriptionCanceled)
	h.subs.beforeWrite = func() {
		h.subs.beforeWrite = nil
		newer := h.subs.byUser["u1"]
		newer.Status, newer.LastEventAt = domain.SubscriptionPaused, tptr(t3)
		h.subs.byUser["u1"] = newer
	}

	if err := h.svc.HandleWebhook(ctx, []byte("{}"), "sig"); err != nil {
		t.Fatal(err)
	}
	got, _ := h.subs.GetByUserID(ctx, "u1")
	if got.Status != domain.SubscriptionPaused || !got.LastEventAt.Equal(t3) {
		t.Fatalf("stale event overwrote the concurrent newer write: %+v", got)
	}
	if _, ok := h.events.seen["ntf_t2"]; !ok {
		t.Fatal("the not-applied notification must still be recorded")
	}
}

func TestHandleWebhookResolvesUserByCustomerID(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	t.Run("known customer", func(t *testing.T) {
		h := newBillingHarness(now)
		h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive, BillingCustomerID: "ctm_1"})
		ev := subEvent("ntf_c", "evt_c", now, domain.SubscriptionPastDue)
		ev.UserID = "" // custom_data missing -> resolve via customer id
		h.payments.webhookEvent = ev
		if err := h.svc.HandleWebhook(ctx, []byte("{}"), "sig"); err != nil {
			t.Fatal(err)
		}
		got, _ := h.subs.GetByUserID(ctx, "u1")
		if got.Status != domain.SubscriptionPastDue || got.BillingSubscriptionID != "sub_1" {
			t.Fatalf("row = %+v", got)
		}
	})
	t.Run("unknown customer is logged and acknowledged", func(t *testing.T) {
		h := newBillingHarness(now)
		ev := subEvent("ntf_u", "evt_u", now, domain.SubscriptionActive)
		ev.UserID = ""
		ev.CustomerID = "ctm_unknown"
		h.payments.webhookEvent = ev
		if err := h.svc.HandleWebhook(ctx, []byte("{}"), "sig"); err != nil {
			t.Fatalf("unknown customer must be a 200 no-op, got %v", err)
		}
		if len(h.subs.byUser) != 0 {
			t.Fatalf("store mutated: %v", h.subs.byUser)
		}
	})
}

// Review C1: custom_data.user_id is client-settable (Paddle.js customData),
// so it never beats the stored customer mapping. The event's customer id is
// resolved first; custom data is used only for a customer no row owns, and
// only when the target user has no customer of their own (or this one).
func TestHandleWebhookUserResolutionIsAnchoredToTheCustomer(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	victimRow := domain.Subscription{
		UserID: "victim", Status: domain.SubscriptionActive, BillingCustomerID: "ctm_victim", BillingSubscriptionID: "sub_victim",
		CurrentPeriodEnd: tptr(now.Add(300 * 24 * time.Hour)), LastEventAt: tptr(now.Add(-time.Hour)),
	}
	assertRefused := func(t *testing.T, h *billingHarness, ntf string) {
		t.Helper()
		if err := h.svc.HandleWebhook(ctx, []byte("{}"), "sig"); err != nil {
			t.Fatalf("a mismatched event must be acknowledged (200), got %v", err)
		}
		if h.subs.upsertCalls != 0 {
			t.Fatalf("a mismatched event must not write (upserts=%d)", h.subs.upsertCalls)
		}
		logs := h.logs.String()
		if !strings.Contains(logs, "level=WARN") || !strings.Contains(logs, "notification_id="+ntf) {
			t.Fatalf("want a warning naming the notification id, logs=%q", logs)
		}
		if strings.Contains(logs, "@") {
			t.Fatalf("logs must not carry emails: %q", logs)
		}
	}

	t.Run("crafted custom data for a victim with another customer leaves the victim untouched", func(t *testing.T) {
		h := newBillingHarness(now)
		h.seedUser(t, "victim", now.Add(-30*24*time.Hour))
		h.seedSub(t, victimRow)
		ev := subEvent("ntf_atk", "evt_atk", now, domain.SubscriptionCanceled)
		ev.Type = "subscription.created"
		ev.CustomerID, ev.SubscriptionID, ev.UserID = "ctm_attacker", "sub_attacker", "victim"
		h.payments.webhookEvent = ev

		assertRefused(t, h, "ntf_atk")
		got, _ := h.subs.GetByUserID(ctx, "victim")
		if got.Status != domain.SubscriptionActive || got.BillingCustomerID != "ctm_victim" || got.BillingSubscriptionID != "sub_victim" {
			t.Fatalf("victim row changed: %+v", got)
		}
		if _, err := h.subs.GetByBillingCustomerID(ctx, "ctm_attacker"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("attacker customer must not be attached to anyone, err=%v", err)
		}
	})
	t.Run("custom data naming a different user than the customer's owner is refused", func(t *testing.T) {
		h := newBillingHarness(now)
		h.seedUser(t, "victim", now.Add(-30*24*time.Hour))
		h.seedUser(t, "attacker", now.Add(-30*24*time.Hour))
		h.seedSub(t, victimRow)
		h.seedSub(t, domain.Subscription{UserID: "attacker", Status: domain.SubscriptionTrialing, BillingCustomerID: "ctm_attacker"})
		ev := subEvent("ntf_mix", "evt_mix", now, domain.SubscriptionCanceled)
		ev.CustomerID, ev.SubscriptionID, ev.UserID = "ctm_attacker", "sub_attacker", "victim"
		h.payments.webhookEvent = ev

		assertRefused(t, h, "ntf_mix")
		if got, _ := h.subs.GetByUserID(ctx, "victim"); got.Status != domain.SubscriptionActive || got.BillingCustomerID != "ctm_victim" {
			t.Fatalf("victim row changed: %+v", got)
		}
		if got, _ := h.subs.GetByUserID(ctx, "attacker"); got.Status != domain.SubscriptionTrialing || got.BillingSubscriptionID != "" {
			t.Fatalf("attacker row changed: %+v", got)
		}
	})
	t.Run("first-time checkout: unknown customer with custom data attaches to that user", func(t *testing.T) {
		h := newBillingHarness(now)
		h.seedUser(t, "u1", now.Add(-2*24*time.Hour))
		periodEnd := now.Add(365 * 24 * time.Hour)
		ev := subEvent("ntf_new", "evt_new", now, domain.SubscriptionActive)
		ev.Type = "subscription.created"
		ev.CustomerID, ev.SubscriptionID = "ctm_new", "sub_new"
		ev.CurrentPeriodEnd = &periodEnd
		h.payments.webhookEvent = ev

		if err := h.svc.HandleWebhook(ctx, []byte("{}"), "sig"); err != nil {
			t.Fatal(err)
		}
		got, err := h.subs.GetByUserID(ctx, "u1")
		if err != nil || got.Status != domain.SubscriptionActive || got.BillingCustomerID != "ctm_new" || got.BillingSubscriptionID != "sub_new" {
			t.Fatalf("row = %+v err=%v", got, err)
		}
	})
	t.Run("custom data for a user whose row has no customer yet attaches the customer", func(t *testing.T) {
		h := newBillingHarness(now)
		h.seedUser(t, "u1", now.Add(-2*24*time.Hour))
		h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionTrialing, TrialEndsAt: tptr(now.Add(12 * 24 * time.Hour))})
		ev := subEvent("ntf_att", "evt_att", now, domain.SubscriptionActive)
		ev.CustomerID, ev.SubscriptionID = "ctm_new", "sub_new"
		h.payments.webhookEvent = ev

		if err := h.svc.HandleWebhook(ctx, []byte("{}"), "sig"); err != nil {
			t.Fatal(err)
		}
		got, _ := h.subs.GetByUserID(ctx, "u1")
		if got.Status != domain.SubscriptionActive || got.BillingCustomerID != "ctm_new" || got.TrialEndsAt != nil {
			t.Fatalf("row = %+v", got)
		}
	})
	t.Run("same customer with matching custom data is applied", func(t *testing.T) {
		h := newBillingHarness(now)
		h.seedUser(t, "victim", now.Add(-30*24*time.Hour))
		h.seedSub(t, victimRow)
		ev := subEvent("ntf_ok", "evt_ok", now, domain.SubscriptionPastDue)
		ev.CustomerID, ev.SubscriptionID, ev.UserID = "ctm_victim", "sub_victim", "victim"
		h.payments.webhookEvent = ev

		if err := h.svc.HandleWebhook(ctx, []byte("{}"), "sig"); err != nil {
			t.Fatal(err)
		}
		if got, _ := h.subs.GetByUserID(ctx, "victim"); got.Status != domain.SubscriptionPastDue {
			t.Fatalf("status = %q, want past_due", got.Status)
		}
	})
}

// Review I2: custom data naming a user with no users row (local DB reset,
// deleted account) must be acknowledged, not written (the subscriptions FK
// would fail the tx -> 500 -> three days of Paddle retries).
func TestHandleWebhookUnknownCustomDataUserIsAcknowledged(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	ev := subEvent("ntf_gone", "evt_gone", now, domain.SubscriptionCanceled)
	ev.CustomerID, ev.UserID = "ctm_gone", "deleted_user"
	h.payments.webhookEvent = ev

	if err := h.svc.HandleWebhook(ctx, []byte("{}"), "sig"); err != nil {
		t.Fatalf("unknown user must be a 200 no-op, got %v", err)
	}
	if h.subs.upsertCalls != 0 || len(h.subs.byUser) != 0 {
		t.Fatalf("store mutated: upserts=%d rows=%v", h.subs.upsertCalls, h.subs.byUser)
	}
	if logs := h.logs.String(); !strings.Contains(logs, "level=WARN") || !strings.Contains(logs, "notification_id=ntf_gone") {
		t.Fatalf("want a warning naming the notification id, logs=%q", logs)
	}
	if _, ok := h.events.seen["ntf_gone"]; !ok {
		t.Fatal("the acknowledged notification must be recorded so a retry is a no-op")
	}
}

// Controller ruling (Track A review): ListForReconciliation exempts rows
// whose last_event_at is NULL, so every applied event must leave a non-zero
// LastEventAt — stamped with now when the event carries no occurred_at.
func TestHandleWebhookEventWithoutOccurredAtStampsNow(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionTrialing, BillingCustomerID: "ctm_1"})
	h.payments.webhookEvent = subEvent("ntf_z", "evt_z", time.Time{}, domain.SubscriptionActive)

	if err := h.svc.HandleWebhook(ctx, []byte("{}"), "sig"); err != nil {
		t.Fatal(err)
	}
	got, _ := h.subs.GetByUserID(ctx, "u1")
	if got.Status != domain.SubscriptionActive {
		t.Fatalf("status = %q, want active", got.Status)
	}
	if got.LastEventAt == nil || !got.LastEventAt.Equal(now) {
		t.Fatalf("LastEventAt = %v, want now (never zero/NULL after a service write)", got.LastEventAt)
	}
}

// Controller ruling (Track A review): postgres Upsert writes trial_ends_at
// from the passed value on every call. Only a provider subscription clears
// the trial; an event that brings no subscription id must pass the stored
// TrialEndsAt through instead of silently wiping it.
func TestHandleWebhookWithoutSubscriptionIDPreservesTrial(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	trialEnd := now.Add(5 * 24 * time.Hour)
	h := newBillingHarness(now)
	h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionTrialing, TrialEndsAt: &trialEnd, BillingCustomerID: "ctm_1"})
	ev := port.SubscriptionEvent{NotificationID: "ntf_n", EventID: "evt_n", Type: "subscription.updated", OccurredAt: now, CustomerID: "ctm_1", UserID: "u1"}
	h.payments.webhookEvent = ev

	if err := h.svc.HandleWebhook(ctx, []byte("{}"), "sig"); err != nil {
		t.Fatal(err)
	}
	got, _ := h.subs.GetByUserID(ctx, "u1")
	if got.BillingSubscriptionID != "" {
		t.Fatalf("BillingSubscriptionID = %q, want empty (no provider subscription yet)", got.BillingSubscriptionID)
	}
	if got.Status != domain.SubscriptionTrialing {
		t.Fatalf("status = %q, want trialing kept when the event carries none", got.Status)
	}
	if got.TrialEndsAt == nil || !got.TrialEndsAt.Equal(trialEnd) {
		t.Fatalf("TrialEndsAt = %v, want %v preserved", got.TrialEndsAt, trialEnd)
	}
	if got.LastEventAt == nil || !got.LastEventAt.Equal(now) {
		t.Fatalf("LastEventAt = %v, want %v", got.LastEventAt, now)
	}
}

// --- Reconciliation ----------------------------------------------------------

func TestReconcileSubscriptionsAppliesProviderStateAsNow(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	for _, id := range []string{"a", "b"} {
		h.seedSub(t, domain.Subscription{UserID: id, Status: domain.SubscriptionActive, BillingCustomerID: "ctm_" + id, BillingSubscriptionID: "sub_" + id,
			CurrentPeriodEnd: tptr(now.Add(-2 * time.Hour)), LastEventAt: tptr(now.Add(-time.Hour))})
	}
	// not stale: must be left alone
	h.seedSub(t, domain.Subscription{UserID: "c", Status: domain.SubscriptionActive, BillingCustomerID: "ctm_c", BillingSubscriptionID: "sub_c",
		CurrentPeriodEnd: tptr(now.Add(24 * time.Hour)), LastEventAt: tptr(now.Add(-time.Hour))})
	h.payments.getSubEvent = port.SubscriptionEvent{Type: "subscription.reconciled", Status: domain.SubscriptionCanceled, CustomerID: "ctm_x"}

	if err := h.svc.ReconcileSubscriptions(ctx); err != nil {
		t.Fatalf("ReconcileSubscriptions: %v", err)
	}
	if h.payments.getSubCalls != 2 {
		t.Fatalf("GetSubscription calls = %d, want 2 (a and b only)", h.payments.getSubCalls)
	}
	for _, id := range []string{"a", "b"} {
		got, _ := h.subs.GetByUserID(ctx, id)
		if got.Status != domain.SubscriptionCanceled {
			t.Fatalf("%s: status = %q, want canceled", id, got.Status)
		}
		if got.LastEventAt == nil || !got.LastEventAt.Equal(now) {
			t.Fatalf("%s: LastEventAt = %v, want now (synthetic event)", id, got.LastEventAt)
		}
		if got.BillingSubscriptionID != "sub_"+id || got.BillingCustomerID != "ctm_x" {
			t.Fatalf("%s: ids = %q/%q (user must be taken from the row, ids from the provider)", id, got.BillingSubscriptionID, got.BillingCustomerID)
		}
	}
	if got, _ := h.subs.GetByUserID(ctx, "c"); got.Status != domain.SubscriptionActive {
		t.Fatalf("c must be untouched: %+v", got)
	}
}

func TestReconcileSubscriptionsSkipsProviderErrors(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	h.seedSub(t, domain.Subscription{UserID: "a", Status: domain.SubscriptionActive, BillingCustomerID: "ctm_a", BillingSubscriptionID: "sub_a",
		CurrentPeriodEnd: tptr(now.Add(-2 * time.Hour)), LastEventAt: tptr(now.Add(-time.Hour))})
	h.payments.getSubErr = errors.New("paddle: http 503")
	if err := h.svc.ReconcileSubscriptions(ctx); err != nil {
		t.Fatalf("provider errors must be logged and skipped, got %v", err)
	}
	got, _ := h.subs.GetByUserID(ctx, "a")
	if got.Status != domain.SubscriptionActive || h.subs.upsertCalls != 0 {
		t.Fatalf("row must be unchanged on provider error: %+v", got)
	}
}

func TestReconcileOverridesTheOrderingGuard(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	// LastEventAt in the future (skewed provider clock) would make a plain
	// webhook at `now` drop; reconciliation must still win.
	h.seedSub(t, domain.Subscription{UserID: "a", Status: domain.SubscriptionActive, BillingCustomerID: "ctm_a", BillingSubscriptionID: "sub_a",
		CurrentPeriodEnd: tptr(now.Add(-2 * time.Hour)), LastEventAt: tptr(now.Add(time.Hour))})
	h.payments.getSubEvent = port.SubscriptionEvent{Status: domain.SubscriptionPaused}
	if err := h.svc.ReconcileSubscriptions(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.subs.GetByUserID(ctx, "a"); got.Status != domain.SubscriptionPaused {
		t.Fatalf("status = %q, want paused (reconcile always wins)", got.Status)
	}
}

// Each row is stamped with the clock at its own apply time, not the pass
// start: a webhook applied mid-pass must not be followed by a forced write
// that moves last_event_at backwards.
func TestReconcileStampsEachRowAtApplyTime(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	for _, id := range []string{"a", "b"} {
		h.seedSub(t, domain.Subscription{UserID: id, Status: domain.SubscriptionActive, BillingCustomerID: "ctm_" + id, BillingSubscriptionID: "sub_" + id,
			CurrentPeriodEnd: tptr(now.Add(-2 * time.Hour)), LastEventAt: tptr(now.Add(-time.Hour))})
	}
	stamp := map[string]time.Time{}
	h.payments.getSubEvent = port.SubscriptionEvent{Status: domain.SubscriptionCanceled}
	h.payments.onGetSub = func(_ context.Context, id string) {
		h.clock.Advance(time.Minute) // the provider read takes time
		stamp[id] = h.clock.Now()
	}
	if err := h.svc.ReconcileSubscriptions(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		got, _ := h.subs.GetByUserID(ctx, id)
		if want := stamp["sub_"+id]; got.LastEventAt == nil || !got.LastEventAt.Equal(want) {
			t.Fatalf("%s: LastEventAt = %v, want its own apply time %v (pass started %v)", id, got.LastEventAt, want, now)
		}
	}
}

func TestReconcileSubscriptionsPropagatesListError(t *testing.T) {
	h := newBillingHarness(time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC))
	h.subs.listErr = errors.New("db down")
	if err := h.svc.ReconcileSubscriptions(context.Background()); !errors.Is(err, h.subs.listErr) {
		t.Fatalf("err = %v, want the list error", err)
	}
	if h.payments.getSubCalls != 0 {
		t.Fatal("no provider call without a row list")
	}
}

// Shutdown mid-pass: the pass stops between rows and reports ctx.Err().
func TestReconcileSubscriptionsStopsWhenContextCanceledMidPass(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	for _, id := range []string{"a", "b", "c"} {
		h.seedSub(t, domain.Subscription{UserID: id, Status: domain.SubscriptionActive, BillingCustomerID: "ctm_" + id, BillingSubscriptionID: "sub_" + id,
			CurrentPeriodEnd: tptr(now.Add(-2 * time.Hour)), LastEventAt: tptr(now.Add(-time.Hour))})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.payments.getSubEvent = port.SubscriptionEvent{Status: domain.SubscriptionCanceled}
	h.payments.onGetSub = func(context.Context, string) { cancel() }
	if err := h.svc.ReconcileSubscriptions(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if h.payments.getSubCalls != 1 {
		t.Fatalf("provider calls = %d, want 1 (stop between rows)", h.payments.getSubCalls)
	}
}

// --- Inline reconcile in GetSubscription -----------------------------------

// A hung provider must not stall GET /v1/billing/subscription: the inline
// read runs under its own short deadline.
func TestGetSubscriptionInlineReconcileHasShortDeadline(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	h.seedSub(t, lapsedActive(now))
	var remaining time.Duration
	var hasDeadline bool
	h.payments.onGetSub = func(ctx context.Context, _ string) {
		var dl time.Time
		dl, hasDeadline = ctx.Deadline()
		remaining = time.Until(dl)
	}
	if _, err := h.svc.GetSubscription(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}
	if !hasDeadline || remaining <= 0 || remaining > inlineReconcileTimeout || inlineReconcileTimeout != 5*time.Second {
		t.Fatalf("inline reconcile deadline: has=%v remaining=%v, want <= 5s", hasDeadline, remaining)
	}
}

// The per-user throttle map is pruned of entries older than the window, so
// it is bounded by the users seen in the last 10 minutes.
func TestInlineReconcileThrottleMapIsPruned(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	for i := 0; i < 50; i++ {
		if !h.svc.claimInlineReconcile(fmt.Sprintf("old-%d", i), now) {
			t.Fatal("first claim must succeed")
		}
	}
	later := now.Add(inlineReconcileEvery)
	if !h.svc.claimInlineReconcile("fresh", later) {
		t.Fatal("claim must succeed")
	}
	h.svc.mu.Lock()
	size := len(h.svc.lastInline)
	h.svc.mu.Unlock()
	if size != 1 {
		t.Fatalf("throttle map size = %d, want 1 (entries older than the window pruned)", size)
	}
	// Pruning must not reopen a slot still inside the window.
	if h.svc.claimInlineReconcile("fresh", later.Add(time.Minute)) {
		t.Fatal("a slot inside the window must stay claimed")
	}
}

func lapsedActive(now time.Time) domain.Subscription {
	return domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive, BillingCustomerID: "ctm_1", BillingSubscriptionID: "sub_1",
		CurrentPeriodEnd: tptr(now.Add(-time.Minute)), LastEventAt: tptr(now.Add(-24 * time.Hour))}
}

func TestGetSubscriptionInlineReconcileWhenActivePastPeriodEnd(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	h.seedSub(t, lapsedActive(now))
	renewed := now.Add(365 * 24 * time.Hour)
	h.payments.getSubEvent = port.SubscriptionEvent{Status: domain.SubscriptionActive, CurrentPeriodEnd: &renewed, CustomerID: "ctm_1", SubscriptionID: "sub_1"}

	sub, err := h.svc.GetSubscription(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if h.payments.getSubCalls != 1 || h.payments.lastGetSubID != "sub_1" {
		t.Fatalf("GetSubscription calls = %d (%q), want 1 for sub_1", h.payments.getSubCalls, h.payments.lastGetSubID)
	}
	if sub.CurrentPeriodEnd == nil || !sub.CurrentPeriodEnd.Equal(renewed) {
		t.Fatalf("returned row must be the reconciled one: %+v", sub)
	}
	if !sub.HasAccess(now) {
		t.Fatal("reconciled renewal must grant access")
	}
}

func TestGetSubscriptionInlineReconcileThrottlesPerUser(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	h.seedSub(t, lapsedActive(now))
	// Provider still reports the lapsed state, so the row stays lapsed and
	// every call would re-trigger without the throttle.
	h.payments.getSubEvent = port.SubscriptionEvent{Status: domain.SubscriptionActive, CurrentPeriodEnd: tptr(now.Add(-time.Minute))}

	for i := 0; i < 3; i++ {
		if _, err := h.svc.GetSubscription(ctx, "u1"); err != nil {
			t.Fatal(err)
		}
	}
	if h.payments.getSubCalls != 1 {
		t.Fatalf("calls within 10m = %d, want 1", h.payments.getSubCalls)
	}
	h.clock.Advance(inlineReconcileEvery)
	if _, err := h.svc.GetSubscription(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if h.payments.getSubCalls != 2 {
		t.Fatalf("calls after 10m = %d, want 2", h.payments.getSubCalls)
	}
}

// Review Focus: the throttle slot is claimed BEFORE the provider call, so a
// failing provider cannot be hammered by a lapsed user's refreshes.
func TestGetSubscriptionInlineReconcileThrottlesFailuresToo(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	h.seedSub(t, lapsedActive(now))
	h.payments.getSubErr = errors.New("paddle: http 503")

	for i := 0; i < 3; i++ {
		sub, err := h.svc.GetSubscription(ctx, "u1")
		if err != nil {
			t.Fatalf("inline reconcile failure must not fail the read: %v", err)
		}
		if sub.Status != domain.SubscriptionActive {
			t.Fatalf("stored row must be returned on failure: %+v", sub)
		}
	}
	if h.payments.getSubCalls != 1 {
		t.Fatalf("calls = %d, want 1 (slot claimed before the call)", h.payments.getSubCalls)
	}
}

func TestGetSubscriptionNoInlineReconcileWhenNotLapsed(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	cases := map[string]domain.Subscription{
		"active inside period":         {UserID: "u1", Status: domain.SubscriptionActive, BillingSubscriptionID: "sub_1", CurrentPeriodEnd: tptr(now.Add(time.Hour))},
		"active lapsed without sub id": {UserID: "u1", Status: domain.SubscriptionActive, CurrentPeriodEnd: tptr(now.Add(-time.Hour))},
		"past_due lapsed":              {UserID: "u1", Status: domain.SubscriptionPastDue, BillingSubscriptionID: "sub_1", CurrentPeriodEnd: tptr(now.Add(-time.Hour))},
		"trialing":                     {UserID: "u1", Status: domain.SubscriptionTrialing, TrialEndsAt: tptr(now.Add(-time.Hour))},
	}
	for name, seed := range cases {
		t.Run(name, func(t *testing.T) {
			h := newBillingHarness(now)
			h.seedSub(t, seed)
			if _, err := h.svc.GetSubscription(ctx, "u1"); err != nil {
				t.Fatal(err)
			}
			if h.payments.getSubCalls != 0 {
				t.Fatalf("inline reconcile must not run: calls = %d", h.payments.getSubCalls)
			}
		})
	}
}

// Keep the port import used by later tasks' tests in this file.
var _ port.SubscriptionEvent
