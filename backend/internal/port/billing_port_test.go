package port

import (
	"context"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// The port package has no behaviour; this test pins the exact method sets
// the service and adapters are written against so a drift fails here first.
type stubPayments struct{}

func (stubPayments) EnsureCustomer(context.Context, domain.User) (string, error) { return "", nil }
func (stubPayments) CreateCheckout(context.Context, CheckoutParams) (string, error) {
	return "", nil
}
func (stubPayments) CreatePortalSession(context.Context, string, string) (PortalURLs, error) {
	return PortalURLs{}, nil
}
func (stubPayments) ParseWebhook([]byte, string, time.Time) (SubscriptionEvent, error) {
	return SubscriptionEvent{}, nil
}
func (stubPayments) GetSubscription(context.Context, string) (SubscriptionEvent, error) {
	return SubscriptionEvent{}, nil
}
func (stubPayments) CancelSubscription(context.Context, string, bool) error { return nil }

type stubSubs struct{}

func (stubSubs) GetByUserID(context.Context, string) (domain.Subscription, error) {
	return domain.Subscription{}, nil
}
func (stubSubs) GetByBillingCustomerID(context.Context, string) (domain.Subscription, error) {
	return domain.Subscription{}, nil
}
func (stubSubs) Upsert(context.Context, domain.Subscription) error { return nil }
func (stubSubs) UpsertIfNewer(context.Context, domain.Subscription) (bool, error) {
	return true, nil
}
func (stubSubs) EnsureTrial(context.Context, string, time.Time) error { return nil }
func (stubSubs) ListForReconciliation(context.Context, time.Time) ([]domain.Subscription, error) {
	return nil, nil
}

type stubEvents struct{}

func (stubEvents) Record(context.Context, SubscriptionEvent) (bool, error) { return true, nil }

// The value of this file is compile-time: the stubs above pin the method
// sets, and the positional literal below pins CheckoutParams to exactly
// {UserID, CustomerID} (no client URLs) — any drift fails to build.
var (
	_ Payments         = stubPayments{}
	_ SubscriptionRepo = stubSubs{}
	_ BillingEventRepo = stubEvents{}
	_                  = CheckoutParams{"user-id", "customer-id"}
)

// TestBillingPortShapes exists so `go test` compiles (and so checks) the
// assertions above; it has no runtime behaviour to verify.
func TestBillingPortShapes(*testing.T) {}
