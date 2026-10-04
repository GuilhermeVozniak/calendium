package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// notFoundSubs is a SubscriptionRepo whose GetByUserID always reports no
// subscription; the other methods panic (they must never be reached here).
type notFoundSubs struct{ port.SubscriptionRepo }

func (notFoundSubs) GetByUserID(context.Context, string) (domain.Subscription, error) {
	return domain.Subscription{}, domain.ErrNotFound
}

// TestEntitlementSelfHostBypass proves SELF_HOSTED unlocks every gated
// use-case: require succeeds without ever consulting the subscription repo,
// whereas the default (paid) path paywalls a user with no subscription.
func TestEntitlementSelfHostBypass(t *testing.T) {
	ctx := context.Background()

	gated := entitlement{subs: notFoundSubs{}, clock: SystemClock{}}
	if err := gated.require(ctx, "u1"); !errors.Is(err, domain.ErrPaymentRequired) {
		t.Fatalf("paid mode: expected ErrPaymentRequired, got %v", err)
	}

	// selfHost short-circuits before touching subs (nil here would panic).
	open := entitlement{selfHost: true}
	if err := open.require(ctx, "u1"); err != nil {
		t.Fatalf("self-host should bypass entitlement, got %v", err)
	}
}

// TestBillingSelfHost verifies the billing surface under SELF_HOSTED: an
// active annual subscription is reported and every billing operation returns
// domain.ErrSelfHosted. nil repos/gateways prove none are reached.
func TestBillingSelfHost(t *testing.T) {
	ctx := context.Background()
	b := NewBillingService(BillingServiceDeps{Clock: SystemClock{}, SelfHosted: true})

	sub, err := b.GetSubscription(ctx, "u1")
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}
	if sub.Status != domain.SubscriptionActive {
		t.Fatalf("status = %q, want %q", sub.Status, domain.SubscriptionActive)
	}
	if sub.Plan != domain.PlanAnnual {
		t.Fatalf("plan = %q, want %q", sub.Plan, domain.PlanAnnual)
	}
	if !sub.HasAccess(time.Now()) {
		t.Fatalf("self-host subscription should grant access")
	}

	if _, err := b.CreateCheckout(ctx, "u1"); !errors.Is(err, domain.ErrSelfHosted) {
		t.Fatalf("checkout: expected ErrSelfHosted, got %v", err)
	}
	if _, err := b.CreatePortalSession(ctx, "u1"); !errors.Is(err, domain.ErrSelfHosted) {
		t.Fatalf("portal: expected ErrSelfHosted, got %v", err)
	}
	if err := b.HandleWebhook(ctx, nil, ""); !errors.Is(err, domain.ErrSelfHosted) {
		t.Fatalf("webhook: expected ErrSelfHosted, got %v", err)
	}
	if err := b.ReconcileSubscriptions(ctx); err != nil {
		t.Fatalf("reconcile must be a silent no-op on self-host, got %v", err)
	}
	if err := b.RequireActive(ctx, "u1"); err != nil {
		t.Fatalf("RequireActive self-host: %v", err)
	}
}
