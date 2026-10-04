package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// inlineReconcileEvery throttles the best-effort reconcile performed by
// GetSubscription for an active row past its period end (per user, in
// memory, per process).
const inlineReconcileEvery = 10 * time.Minute

// BillingServiceDeps wires BillingService (docs/payments.md).
type BillingServiceDeps struct {
	Users    port.UserRepo
	Subs     port.SubscriptionRepo
	Events   port.BillingEventRepo
	Payments port.Payments
	Clock    port.Clock
	Tx       port.TxRunner
	// SelfHosted disables billing entirely: GetSubscription reports an
	// active annual plan and checkout/portal/webhook return
	// domain.ErrSelfHosted (open-core self-hosted mode).
	SelfHosted bool
	Logger     *slog.Logger // optional; defaults to slog.Default()
}

// BillingService implements port.BillingService: the $50/yr Paddle flow
// where webhooks drive all state and reconciliation covers lost webhooks.
type BillingService struct {
	users      port.UserRepo
	subs       port.SubscriptionRepo
	events     port.BillingEventRepo
	payments   port.Payments
	clock      port.Clock
	tx         port.TxRunner
	selfHosted bool
	logger     *slog.Logger

	mu         sync.Mutex
	lastInline map[string]time.Time // user id -> last inline reconcile attempt
}

var _ port.BillingService = (*BillingService)(nil)

func NewBillingService(d BillingServiceDeps) *BillingService {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &BillingService{
		users: d.Users, subs: d.Subs, events: d.Events, payments: d.Payments,
		clock: d.Clock, tx: d.Tx, selfHosted: d.SelfHosted, logger: d.Logger,
		lastInline: map[string]time.Time{},
	}
}

func selfHostedSubscription(userID string) domain.Subscription {
	// No biller on self-hosted instances: report an active annual plan (nil
	// period) so clients treat the user as fully entitled.
	return domain.Subscription{
		UserID:   userID,
		Status:   domain.SubscriptionActive,
		Plan:     domain.PlanAnnual,
		PriceUSD: domain.PriceUSDAnnual,
	}
}

// GetSubscription returns the user's row, granting the signup trial when
// none exists yet. (Task 11 adds the inline reconcile.)
func (s *BillingService) GetSubscription(ctx context.Context, userID string) (domain.Subscription, error) {
	if s.selfHosted {
		return selfHostedSubscription(userID), nil
	}
	sub, err := s.subs.GetByUserID(ctx, userID)
	if errors.Is(err, domain.ErrNotFound) {
		sub, err = s.grantTrial(ctx, userID)
	}
	if err != nil {
		return domain.Subscription{}, err
	}
	return sub, nil
}

// grantTrial anchors the 14-day trial to users.created_at (signup, since
// the users row is provisioned on the first authenticated call). EnsureTrial
// is ON CONFLICT DO NOTHING, so concurrent first calls both read back the
// same row.
func (s *BillingService) grantTrial(ctx context.Context, userID string) (domain.Subscription, error) {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return domain.Subscription{}, err
	}
	if err := s.subs.EnsureTrial(ctx, userID, user.CreatedAt.Add(domain.TrialLength)); err != nil {
		return domain.Subscription{}, err
	}
	return s.subs.GetByUserID(ctx, userID)
}

// Implemented in Task 9.
func (s *BillingService) CreateCheckout(ctx context.Context, userID string) (string, error) {
	if s.selfHosted {
		return "", fmt.Errorf("%w: billing is disabled on self-hosted instances", domain.ErrSelfHosted)
	}
	return "", domain.ErrNotImplemented
}

// Implemented in Task 9.
func (s *BillingService) CreatePortalSession(ctx context.Context, userID string) (port.PortalURLs, error) {
	if s.selfHosted {
		return port.PortalURLs{}, fmt.Errorf("%w: billing is disabled on self-hosted instances", domain.ErrSelfHosted)
	}
	return port.PortalURLs{}, domain.ErrNotImplemented
}

// Implemented in Task 10.
func (s *BillingService) HandleWebhook(ctx context.Context, payload []byte, sigHeader string) error {
	if s.selfHosted {
		return fmt.Errorf("%w: billing is disabled on self-hosted instances", domain.ErrSelfHosted)
	}
	return domain.ErrNotImplemented
}

// Implemented in Task 11.
func (s *BillingService) ReconcileSubscriptions(ctx context.Context) error {
	if s.selfHosted {
		return nil
	}
	return domain.ErrNotImplemented
}

func (s *BillingService) RequireActive(ctx context.Context, userID string) error {
	return entitlement{subs: s.subs, clock: s.clock, selfHost: s.selfHosted}.require(ctx, userID)
}
