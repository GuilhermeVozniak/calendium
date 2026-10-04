package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
	"calendium/backend/internal/service"
)

// Handler-level coverage for review I2: the real BillingService behind the
// real webhook route, with storage doubles whose subscription write enforces
// the subscriptions.user_id -> users FK like Postgres does. A notification
// whose custom_data user has no users row must be acknowledged with 200, not
// fail the tx with 500 (which Paddle retries for three days).

type whUsers struct{ byID map[string]domain.User }

func (r *whUsers) Upsert(_ context.Context, u domain.User) (domain.User, error) {
	r.byID[u.ID] = u
	return u, nil
}

func (r *whUsers) GetByID(_ context.Context, id string) (domain.User, error) {
	u, ok := r.byID[id]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return u, nil
}

type whSubs struct {
	users  *whUsers
	byUser map[string]domain.Subscription
	writes int
}

var errFKViolation = errors.New("insert or update on table \"subscriptions\" violates foreign key constraint")

func (r *whSubs) write(s domain.Subscription) error {
	if _, ok := r.users.byID[s.UserID]; !ok {
		return errFKViolation
	}
	r.writes++
	r.byUser[s.UserID] = s
	return nil
}

func (r *whSubs) GetByUserID(_ context.Context, userID string) (domain.Subscription, error) {
	s, ok := r.byUser[userID]
	if !ok {
		return domain.Subscription{}, domain.ErrNotFound
	}
	return s, nil
}

func (r *whSubs) GetByBillingCustomerID(_ context.Context, customerID string) (domain.Subscription, error) {
	for _, s := range r.byUser {
		if s.BillingCustomerID == customerID {
			return s, nil
		}
	}
	return domain.Subscription{}, domain.ErrNotFound
}

func (r *whSubs) Upsert(_ context.Context, s domain.Subscription) error { return r.write(s) }

func (r *whSubs) UpsertIfNewer(_ context.Context, s domain.Subscription) (bool, error) {
	return true, r.write(s)
}

func (r *whSubs) EnsureTrial(context.Context, string, time.Time) error { return nil }

func (r *whSubs) ListForReconciliation(context.Context, time.Time) ([]domain.Subscription, error) {
	return nil, nil
}

type whEvents struct{ seen map[string]bool }

func (r *whEvents) Record(_ context.Context, ev port.SubscriptionEvent) (bool, error) {
	if r.seen[ev.NotificationID] {
		return false, nil
	}
	r.seen[ev.NotificationID] = true
	return true, nil
}

type whPayments struct{ ev port.SubscriptionEvent }

func (p *whPayments) EnsureCustomer(context.Context, domain.User) (string, error) { return "", nil }
func (p *whPayments) CreateCheckout(context.Context, port.CheckoutParams) (string, error) {
	return "", nil
}
func (p *whPayments) CreatePortalSession(context.Context, string, string) (port.PortalURLs, error) {
	return port.PortalURLs{}, nil
}
func (p *whPayments) ParseWebhook([]byte, string, time.Time) (port.SubscriptionEvent, error) {
	return p.ev, nil
}
func (p *whPayments) GetSubscription(context.Context, string) (port.SubscriptionEvent, error) {
	return port.SubscriptionEvent{}, nil
}
func (p *whPayments) CancelSubscription(context.Context, string, bool) error { return nil }

type whClock struct{ now time.Time }

func (c whClock) Now() time.Time { return c.now }

// whTx mirrors a database transaction: the ledger marker is rolled back
// when the callback fails.
type whTx struct{ events *whEvents }

func (t whTx) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	before := map[string]bool{}
	for k, v := range t.events.seen {
		before[k] = v
	}
	if err := fn(ctx); err != nil {
		t.events.seen = before
		return err
	}
	return nil
}

func TestPaddleWebhookForUserWithoutUsersRowIsAcknowledged(t *testing.T) {
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	users := &whUsers{byID: map[string]domain.User{}}
	subs := &whSubs{users: users, byUser: map[string]domain.Subscription{}}
	events := &whEvents{seen: map[string]bool{}}
	payments := &whPayments{ev: port.SubscriptionEvent{
		NotificationID: "ntf_gone", EventID: "evt_gone", Type: "subscription.canceled", OccurredAt: now,
		CustomerID: "ctm_gone", SubscriptionID: "sub_gone", UserID: "deleted_user", Status: domain.SubscriptionCanceled,
	}}
	h := newHarness(t)
	h.deps.Billing = service.NewBillingService(service.BillingServiceDeps{
		Users: users, Subs: subs, Events: events, Payments: payments,
		Clock: whClock{now: now}, Tx: whTx{events: events},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/paddle", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Paddle-Signature", "ts=1;h1=ab")
	rec := httptest.NewRecorder()
	h.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 so Paddle stops retrying (body=%s)", rec.Code, rec.Body.String())
	}
	if subs.writes != 0 {
		t.Fatalf("subscription writes = %d, want 0", subs.writes)
	}
	if !events.seen["ntf_gone"] {
		t.Fatal("the acknowledged notification must stay recorded so a retry is a no-op")
	}
}
