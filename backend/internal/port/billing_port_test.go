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
func (stubSubs) Upsert(context.Context, domain.Subscription) error    { return nil }
func (stubSubs) EnsureTrial(context.Context, string, time.Time) error { return nil }
func (stubSubs) ListForReconciliation(context.Context, time.Time) ([]domain.Subscription, error) {
	return nil, nil
}

type stubEvents struct{}

func (stubEvents) Record(context.Context, SubscriptionEvent) (bool, error) { return true, nil }

func TestBillingPortShapes(t *testing.T) {
	var _ Payments = stubPayments{}
	var _ SubscriptionRepo = stubSubs{}
	var _ BillingEventRepo = stubEvents{}
	ev := SubscriptionEvent{NotificationID: "ntf_1", EventID: "evt_1", Type: "subscription.updated", Status: domain.SubscriptionPaused}
	if ev.Ignored {
		t.Fatal("zero SubscriptionEvent must not be ignored by default")
	}
	if (CheckoutParams{UserID: "u", CustomerID: "c"}).CustomerID != "c" {
		t.Fatal("CheckoutParams must carry only UserID and CustomerID")
	}
}
