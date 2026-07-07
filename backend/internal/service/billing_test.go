package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// This file covers the paid (selfHosted=false) Stripe flow in billing.go.
// Self-host bypass / ErrSelfHosted are covered by selfhost_test.go
// (TestBillingSelfHost, TestEntitlementSelfHostBypass) and are not
// duplicated here.

// TestGetSubscriptionPaidPath pins GetSubscription's two paid-path branches:
// a repo miss synthesizes a SubscriptionNone placeholder (never an error),
// and an existing row is returned unmodified.
func TestGetSubscriptionPaidPath(t *testing.T) {
	ctx := context.Background()
	const userID = "u1"
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("no subscription returns placeholder", func(t *testing.T) {
		subs := newSubscriptionRepo()
		b := NewBillingService(newUserRepo(), subs, newStripeEventRepo(), newPayments(), newClock(now), newTxRunner(), false)

		sub, err := b.GetSubscription(ctx, userID)
		if err != nil {
			t.Fatalf("GetSubscription: %v", err)
		}
		if sub.Status != domain.SubscriptionNone {
			t.Fatalf("status = %q, want %q", sub.Status, domain.SubscriptionNone)
		}
		if sub.Plan != domain.PlanAnnual {
			t.Fatalf("plan = %q, want %q", sub.Plan, domain.PlanAnnual)
		}
		if sub.PriceUSD != domain.PriceUSDAnnual {
			t.Fatalf("priceUSD = %d, want %d", sub.PriceUSD, domain.PriceUSDAnnual)
		}
		if sub.UserID != userID {
			t.Fatalf("userID = %q, want %q", sub.UserID, userID)
		}
	})

	t.Run("active subscription returned as-is", func(t *testing.T) {
		subs := newSubscriptionRepo()
		seed := domain.Subscription{
			UserID:           userID,
			Status:           domain.SubscriptionActive,
			StripeCustomerID: "cus_1",
		}
		if err := subs.Upsert(ctx, seed); err != nil {
			t.Fatal(err)
		}
		b := NewBillingService(newUserRepo(), subs, newStripeEventRepo(), newPayments(), newClock(now), newTxRunner(), false)

		sub, err := b.GetSubscription(ctx, userID)
		if err != nil {
			t.Fatalf("GetSubscription: %v", err)
		}
		if sub != seed {
			t.Fatalf("GetSubscription = %+v, want %+v", sub, seed)
		}
	})
}

// TestCreateCheckoutSessionTrialLogic pins the paid-path trial rule
// (billing.go): a first-time subscriber (no StripeSubscriptionID) gets a
// 14-day trial and a freshly-ensured Stripe customer persisted via Upsert; a
// returning subscriber gets TrialDays=0 and reuses the stored customer.
func TestCreateCheckoutSessionTrialLogic(t *testing.T) {
	const userID = "u1"
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	tests := []struct {
		name           string
		seed           *domain.Subscription // existing row; nil => none yet
		wantTrialDays  int
		wantEnsure     bool // EnsureCustomer must be invoked (no customer yet)
		wantCustomerID string
	}{
		{
			name:           "first-time subscriber gets 14-day trial and customer persisted",
			seed:           nil,
			wantTrialDays:  trialDays, // 14
			wantEnsure:     true,
			wantCustomerID: "cus_new",
		},
		{
			name: "returning subscriber gets no trial and reuses customer",
			seed: &domain.Subscription{
				UserID:               userID,
				Status:               domain.SubscriptionActive,
				StripeCustomerID:     "cus_old",
				StripeSubscriptionID: "sub_old",
			},
			wantTrialDays:  0,
			wantEnsure:     false,
			wantCustomerID: "cus_old",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			clock := newClock(now)
			users := newUserRepo()
			if _, err := users.Upsert(ctx, domain.User{ID: userID, Email: "me@x.com"}); err != nil {
				t.Fatal(err)
			}
			subs := newSubscriptionRepo()
			if tt.seed != nil {
				if err := subs.Upsert(ctx, *tt.seed); err != nil {
					t.Fatal(err)
				}
			}
			payments := newPayments()
			payments.customerID = "cus_new"            // EnsureCustomer return
			payments.checkoutURL = "https://co.test/1" // CreateCheckoutSession return

			b := NewBillingService(users, subs, newStripeEventRepo(), payments, clock, newTxRunner(), false)

			url, err := b.CreateCheckoutSession(ctx, userID, "https://app/success", "https://app/cancel")
			if err != nil {
				t.Fatalf("CreateCheckoutSession: %v", err)
			}
			if url != "https://co.test/1" {
				t.Fatalf("url = %q, want the payments checkout url", url)
			}
			p := payments.lastCheckoutParams
			if p.TrialDays != tt.wantTrialDays {
				t.Fatalf("TrialDays = %d, want %d", p.TrialDays, tt.wantTrialDays)
			}
			if p.CustomerID != tt.wantCustomerID {
				t.Fatalf("CheckoutParams.CustomerID = %q, want %q", p.CustomerID, tt.wantCustomerID)
			}
			if p.UserID != userID {
				t.Fatalf("CheckoutParams.UserID = %q, want %q", p.UserID, userID)
			}
			if p.SuccessURL != "https://app/success" || p.CancelURL != "https://app/cancel" {
				t.Fatalf("checkout URLs not forwarded: %+v", p)
			}
			if got := payments.ensureCustomerCalls > 0; got != tt.wantEnsure {
				t.Fatalf("EnsureCustomer called = %v, want %v", got, tt.wantEnsure)
			}
			// The (existing or freshly-ensured) customer id is mirrored on the row.
			persisted, err := subs.GetByUserID(ctx, userID)
			if err != nil {
				t.Fatalf("subscription not persisted: %v", err)
			}
			if persisted.StripeCustomerID != tt.wantCustomerID {
				t.Fatalf("persisted StripeCustomerID = %q, want %q", persisted.StripeCustomerID, tt.wantCustomerID)
			}
		})
	}
}

// TestCreateCheckoutSessionValidation proves successURL/cancelURL are
// required and checked before any Stripe call (short-circuit).
func TestCreateCheckoutSessionValidation(t *testing.T) {
	ctx := context.Background()
	const userID = "u1"
	now := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		successURL string
		cancelURL  string
	}{
		{name: "empty successURL", successURL: "", cancelURL: "https://app/cancel"},
		{name: "empty cancelURL", successURL: "https://app/success", cancelURL: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payments := newPayments()
			b := NewBillingService(newUserRepo(), newSubscriptionRepo(), newStripeEventRepo(), payments, newClock(now), newTxRunner(), false)

			_, err := b.CreateCheckoutSession(ctx, userID, tt.successURL, tt.cancelURL)
			if !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("err = %v, want ErrValidation", err)
			}
			if payments.ensureCustomerCalls != 0 {
				t.Fatalf("ensureCustomerCalls = %d, want 0 (short-circuit before Stripe)", payments.ensureCustomerCalls)
			}
			if payments.lastCheckoutParams != (port.CheckoutParams{}) {
				t.Fatalf("lastCheckoutParams = %+v, want zero value", payments.lastCheckoutParams)
			}
		})
	}
}

// TestCreatePortalSession covers the guard clauses (missing profile, missing
// customer id, missing returnURL) and the happy path.
func TestCreatePortalSession(t *testing.T) {
	ctx := context.Background()
	const userID = "u1"
	now := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)

	t.Run("no billing profile", func(t *testing.T) {
		payments := newPayments()
		b := NewBillingService(newUserRepo(), newSubscriptionRepo(), newStripeEventRepo(), payments, newClock(now), newTxRunner(), false)

		_, err := b.CreatePortalSession(ctx, userID, "https://app/return")
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
		if payments.lastPortalCustomerID != "" || payments.lastPortalReturnURL != "" {
			t.Fatalf("portal fields should be untouched, got customerID=%q returnURL=%q", payments.lastPortalCustomerID, payments.lastPortalReturnURL)
		}
	})

	t.Run("profile without customer", func(t *testing.T) {
		subs := newSubscriptionRepo()
		if err := subs.Upsert(ctx, domain.Subscription{UserID: userID, StripeCustomerID: ""}); err != nil {
			t.Fatal(err)
		}
		payments := newPayments()
		b := NewBillingService(newUserRepo(), subs, newStripeEventRepo(), payments, newClock(now), newTxRunner(), false)

		_, err := b.CreatePortalSession(ctx, userID, "https://app/return")
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("empty returnURL", func(t *testing.T) {
		b := NewBillingService(newUserRepo(), newSubscriptionRepo(), newStripeEventRepo(), newPayments(), newClock(now), newTxRunner(), false)

		_, err := b.CreatePortalSession(ctx, userID, "")
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("happy path", func(t *testing.T) {
		subs := newSubscriptionRepo()
		if err := subs.Upsert(ctx, domain.Subscription{UserID: userID, StripeCustomerID: "cus_1"}); err != nil {
			t.Fatal(err)
		}
		payments := newPayments()
		payments.portalURL = "https://portal.test/x"
		b := NewBillingService(newUserRepo(), subs, newStripeEventRepo(), payments, newClock(now), newTxRunner(), false)

		const returnURL = "https://app/return"
		url, err := b.CreatePortalSession(ctx, userID, returnURL)
		if err != nil {
			t.Fatalf("CreatePortalSession: %v", err)
		}
		if url != "https://portal.test/x" {
			t.Fatalf("url = %q, want portal url", url)
		}
		if payments.lastPortalCustomerID != "cus_1" {
			t.Fatalf("lastPortalCustomerID = %q, want cus_1", payments.lastPortalCustomerID)
		}
		if payments.lastPortalReturnURL != returnURL {
			t.Fatalf("lastPortalReturnURL = %q, want %q", payments.lastPortalReturnURL, returnURL)
		}
	})
}

// TestHandleWebhookIdempotentReplay proves the replay guard: an event id
// already recorded is a no-op — applyWebhookEvent is skipped, the existing
// active status is NOT downgraded — and handling still runs inside a tx.
func TestHandleWebhookIdempotentReplay(t *testing.T) {
	ctx := context.Background()
	const userID = "u1"
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	subs := newSubscriptionRepo()
	if err := subs.Upsert(ctx, domain.Subscription{
		UserID:           userID,
		Status:           domain.SubscriptionActive,
		StripeCustomerID: "cus_1",
	}); err != nil {
		t.Fatal(err)
	}

	events := newStripeEventRepo()
	// Pre-record so the fake reports firstTime=false for this id on replay.
	if _, err := events.Record(ctx, "evt_1", "customer.subscription.updated"); err != nil {
		t.Fatal(err)
	}

	created := now
	payments := newPayments()
	payments.webhookEvent = port.WebhookEvent{
		ID:         "evt_1",
		Type:       "customer.subscription.updated",
		UserID:     userID,
		CustomerID: "cus_1",
		Status:     domain.SubscriptionCanceled, // would downgrade IF applied
		Created:    &created,
	}
	tx := newTxRunner()

	b := NewBillingService(newUserRepo(), subs, events, payments, newClock(now), tx, false)

	if err := b.HandleWebhook(ctx, []byte("{}"), "sig"); err != nil {
		t.Fatalf("HandleWebhook: %v", err)
	}
	got, err := subs.GetByUserID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.SubscriptionActive {
		t.Fatalf("status = %q, want %q (replay must not re-apply)", got.Status, domain.SubscriptionActive)
	}
	if tx.calls == 0 {
		t.Fatal("expected RunInTx to wrap webhook handling")
	}
}

// TestHandleWebhookParseError proves a bad signature is rejected before the
// tx even opens, and leaves stored state untouched.
func TestHandleWebhookParseError(t *testing.T) {
	ctx := context.Background()
	const userID = "u1"
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	subs := newSubscriptionRepo()
	seed := domain.Subscription{UserID: userID, Status: domain.SubscriptionActive, StripeCustomerID: "cus_1"}
	if err := subs.Upsert(ctx, seed); err != nil {
		t.Fatal(err)
	}

	payments := newPayments()
	payments.parseWebhookErr = errors.New("bad sig")
	tx := newTxRunner()

	b := NewBillingService(newUserRepo(), subs, newStripeEventRepo(), payments, newClock(now), tx, false)

	err := b.HandleWebhook(ctx, []byte("{}"), "sig")
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("HandleWebhook error = %v, want ErrUnauthorized", err)
	}
	if tx.calls != 0 {
		t.Fatalf("tx.calls = %d, want 0 (parse fails before the tx)", tx.calls)
	}
	got, err := subs.GetByUserID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if got != seed {
		t.Fatalf("subscription changed: got %+v, want unchanged %+v", got, seed)
	}
}

// TestHandleWebhookApplies proves a first-delivery customer.subscription.*
// event is recorded, applied inside a tx, and advances LastEventAt.
func TestHandleWebhookApplies(t *testing.T) {
	ctx := context.Background()
	const userID = "u1"
	now := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	future := now.Add(365 * 24 * time.Hour)

	subs := newSubscriptionRepo()
	events := newStripeEventRepo()
	payments := newPayments()
	payments.webhookEvent = port.WebhookEvent{
		ID:               "evt_new",
		Type:             "customer.subscription.created",
		UserID:           userID,
		Status:           domain.SubscriptionActive,
		SubscriptionID:   "sub_1",
		CustomerID:       "cus_1",
		CurrentPeriodEnd: &future,
		Created:          &now,
	}
	tx := newTxRunner()

	b := NewBillingService(newUserRepo(), subs, events, payments, newClock(now), tx, false)

	if err := b.HandleWebhook(ctx, []byte("{}"), "sig"); err != nil {
		t.Fatalf("HandleWebhook: %v", err)
	}
	got, err := subs.GetByUserID(ctx, userID)
	if err != nil {
		t.Fatalf("subscription not persisted: %v", err)
	}
	if got.Status != domain.SubscriptionActive {
		t.Fatalf("status = %q, want %q", got.Status, domain.SubscriptionActive)
	}
	if got.StripeSubscriptionID != "sub_1" {
		t.Fatalf("StripeSubscriptionID = %q, want sub_1", got.StripeSubscriptionID)
	}
	if got.CurrentPeriodEnd == nil || !got.CurrentPeriodEnd.Equal(future) {
		t.Fatalf("CurrentPeriodEnd = %v, want %v", got.CurrentPeriodEnd, future)
	}
	if got.LastEventAt == nil || !got.LastEventAt.Equal(now) {
		t.Fatalf("LastEventAt = %v, want %v", got.LastEventAt, now)
	}
	if tx.calls == 0 {
		t.Fatal("expected RunInTx to wrap webhook handling")
	}
}

// TestHandleWebhookOutOfOrder proves an older customer.subscription.* event
// (Created before the mirrored LastEventAt) is dropped rather than
// reverting an already-applied newer lifecycle state.
func TestHandleWebhookOutOfOrder(t *testing.T) {
	ctx := context.Background()
	const userID = "u1"
	t1 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	t2 := t1.Add(24 * time.Hour)

	subs := newSubscriptionRepo()
	if err := subs.Upsert(ctx, domain.Subscription{
		UserID:           userID,
		Status:           domain.SubscriptionActive,
		StripeCustomerID: "cus_1",
		LastEventAt:      &t2,
	}); err != nil {
		t.Fatal(err)
	}

	events := newStripeEventRepo()
	payments := newPayments()
	// Id must NOT be pre-recorded: proves the drop is ordering, not idempotency.
	payments.webhookEvent = port.WebhookEvent{
		ID:         "evt_old",
		Type:       "customer.subscription.updated",
		UserID:     userID,
		CustomerID: "cus_1",
		Status:     domain.SubscriptionCanceled,
		Created:    &t1,
	}
	tx := newTxRunner()

	b := NewBillingService(newUserRepo(), subs, events, payments, newClock(t2), tx, false)

	if err := b.HandleWebhook(ctx, []byte("{}"), "sig"); err != nil {
		t.Fatalf("HandleWebhook: %v", err)
	}
	got, err := subs.GetByUserID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.SubscriptionActive {
		t.Fatalf("status = %q, want %q (older event must not revert)", got.Status, domain.SubscriptionActive)
	}
	if got.LastEventAt == nil || !got.LastEventAt.Equal(t2) {
		t.Fatalf("LastEventAt = %v, want %v (unchanged)", got.LastEventAt, t2)
	}
}

// TestHandleWebhookPreservesInvoiceFields proves a non-customer.subscription.*
// event (invoice.paid) never clobbers the mirrored period/cancel/trial
// fields or LastEventAt, matching webhook.go's normalization which leaves
// those fields zero for invoice.* events.
func TestHandleWebhookPreservesInvoiceFields(t *testing.T) {
	ctx := context.Background()
	const userID = "u1"
	t1 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	t2 := t1.Add(24 * time.Hour)
	pe := t1.Add(30 * 24 * time.Hour)
	te := t1.Add(-5 * 24 * time.Hour)

	subs := newSubscriptionRepo()
	if err := subs.Upsert(ctx, domain.Subscription{
		UserID:               userID,
		Status:               domain.SubscriptionActive,
		StripeCustomerID:     "cus_1",
		StripeSubscriptionID: "sub_1",
		CurrentPeriodEnd:     &pe,
		CancelAtPeriodEnd:    true,
		TrialEndsAt:          &te,
		LastEventAt:          &t1,
	}); err != nil {
		t.Fatal(err)
	}

	events := newStripeEventRepo()
	payments := newPayments()
	payments.webhookEvent = port.WebhookEvent{
		ID:             "evt_invoice",
		Type:           "invoice.paid",
		UserID:         userID,
		Status:         domain.SubscriptionActive,
		SubscriptionID: "sub_1",
		CustomerID:     "cus_1",
		Created:        &t2,
		// Period/cancel/trial intentionally left zero: webhook.go never
		// populates them for invoice.* events.
	}
	tx := newTxRunner()

	b := NewBillingService(newUserRepo(), subs, events, payments, newClock(t2), tx, false)

	if err := b.HandleWebhook(ctx, []byte("{}"), "sig"); err != nil {
		t.Fatalf("HandleWebhook: %v", err)
	}
	got, err := subs.GetByUserID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentPeriodEnd == nil || !got.CurrentPeriodEnd.Equal(pe) {
		t.Fatalf("CurrentPeriodEnd = %v, want %v", got.CurrentPeriodEnd, pe)
	}
	if !got.CancelAtPeriodEnd {
		t.Fatal("CancelAtPeriodEnd should be preserved as true")
	}
	if got.TrialEndsAt == nil || !got.TrialEndsAt.Equal(te) {
		t.Fatalf("TrialEndsAt = %v, want %v", got.TrialEndsAt, te)
	}
	if got.StripeSubscriptionID != "sub_1" {
		t.Fatalf("StripeSubscriptionID = %q, want sub_1", got.StripeSubscriptionID)
	}
	if got.LastEventAt == nil || !got.LastEventAt.Equal(t1) {
		t.Fatalf("LastEventAt = %v, want %v (non-subscription events never advance it)", got.LastEventAt, t1)
	}
	if got.Status != domain.SubscriptionActive {
		t.Fatalf("status = %q, want %q", got.Status, domain.SubscriptionActive)
	}
}

// TestRequireActivePaidPath covers the paid-path entitlement gate: no
// subscription, past_due within/beyond the grace window, and expired.
func TestRequireActivePaidPath(t *testing.T) {
	ctx := context.Background()
	const userID = "u1"
	now := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)

	t.Run("no subscription", func(t *testing.T) {
		b := NewBillingService(newUserRepo(), newSubscriptionRepo(), newStripeEventRepo(), newPayments(), newClock(now), newTxRunner(), false)
		if err := b.RequireActive(ctx, userID); !errors.Is(err, domain.ErrPaymentRequired) {
			t.Fatalf("err = %v, want ErrPaymentRequired", err)
		}
	})

	t.Run("past_due within grace", func(t *testing.T) {
		subs := newSubscriptionRepo()
		if err := subs.Upsert(ctx, domain.Subscription{
			UserID:           userID,
			Status:           domain.SubscriptionPastDue,
			CurrentPeriodEnd: &now,
		}); err != nil {
			t.Fatal(err)
		}
		b := NewBillingService(newUserRepo(), subs, newStripeEventRepo(), newPayments(), newClock(now), newTxRunner(), false)
		if err := b.RequireActive(ctx, userID); err != nil {
			t.Fatalf("RequireActive = %v, want nil", err)
		}
	})

	t.Run("past_due beyond grace", func(t *testing.T) {
		periodEnd := now.Add(-8 * 24 * time.Hour)
		subs := newSubscriptionRepo()
		if err := subs.Upsert(ctx, domain.Subscription{
			UserID:           userID,
			Status:           domain.SubscriptionPastDue,
			CurrentPeriodEnd: &periodEnd,
		}); err != nil {
			t.Fatal(err)
		}
		b := NewBillingService(newUserRepo(), subs, newStripeEventRepo(), newPayments(), newClock(now), newTxRunner(), false)
		if err := b.RequireActive(ctx, userID); !errors.Is(err, domain.ErrPaymentRequired) {
			t.Fatalf("err = %v, want ErrPaymentRequired", err)
		}
	})

	t.Run("expired", func(t *testing.T) {
		subs := newSubscriptionRepo()
		if err := subs.Upsert(ctx, domain.Subscription{UserID: userID, Status: domain.SubscriptionExpired}); err != nil {
			t.Fatal(err)
		}
		b := NewBillingService(newUserRepo(), subs, newStripeEventRepo(), newPayments(), newClock(now), newTxRunner(), false)
		if err := b.RequireActive(ctx, userID); !errors.Is(err, domain.ErrPaymentRequired) {
			t.Fatalf("err = %v, want ErrPaymentRequired", err)
		}
	})
}
