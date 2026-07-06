package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// trialDays is granted to first-time subscribers at checkout.
const trialDays = 14

// BillingService implements port.BillingService (docs/payments.md — the
// $50/yr Spotify-model Stripe flow; webhooks drive all state).
type BillingService struct {
	users    port.UserRepo
	subs     port.SubscriptionRepo
	events   port.StripeEventRepo
	payments port.Payments
	clock    port.Clock
	tx       port.TxRunner
	// selfHosted disables Stripe entirely: GetSubscription reports an active
	// annual plan and the checkout/portal/webhook operations return
	// domain.ErrSelfHosted (open-core self-hosted mode).
	selfHosted bool
}

var _ port.BillingService = (*BillingService)(nil)

func NewBillingService(users port.UserRepo, subs port.SubscriptionRepo, events port.StripeEventRepo, payments port.Payments, clock port.Clock, tx port.TxRunner, selfHosted bool) *BillingService {
	return &BillingService{users: users, subs: subs, events: events, payments: payments, clock: clock, tx: tx, selfHosted: selfHosted}
}

func (s *BillingService) GetSubscription(ctx context.Context, userID string) (domain.Subscription, error) {
	if s.selfHosted {
		// No biller on self-hosted instances: report an active annual plan
		// (nil period) so clients treat the user as fully entitled.
		return domain.Subscription{
			UserID:   userID,
			Status:   domain.SubscriptionActive,
			Plan:     domain.PlanAnnual,
			PriceUSD: domain.PriceUSDAnnual,
		}, nil
	}
	sub, err := s.subs.GetByUserID(ctx, userID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Subscription{
			UserID:   userID,
			Status:   domain.SubscriptionNone,
			Plan:     domain.PlanAnnual,
			PriceUSD: domain.PriceUSDAnnual,
		}, nil
	}
	return sub, err
}

func (s *BillingService) CreateCheckoutSession(ctx context.Context, userID, successURL, cancelURL string) (string, error) {
	if s.selfHosted {
		return "", fmt.Errorf("%w: billing is disabled on self-hosted instances", domain.ErrSelfHosted)
	}
	if successURL == "" || cancelURL == "" {
		return "", fmt.Errorf("%w: successUrl and cancelUrl are required", domain.ErrValidation)
	}
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return "", err
	}
	sub, err := s.GetSubscription(ctx, userID)
	if err != nil {
		return "", err
	}
	if sub.StripeCustomerID == "" {
		customerID, err := s.payments.EnsureCustomer(ctx, user)
		if err != nil {
			return "", err
		}
		sub.StripeCustomerID = customerID
		if err := s.subs.Upsert(ctx, sub); err != nil {
			return "", err
		}
	}
	trial := 0
	if sub.StripeSubscriptionID == "" {
		trial = trialDays // 14-day trial for first-time subscribers only
	}
	return s.payments.CreateCheckoutSession(ctx, port.CheckoutParams{
		CustomerID: sub.StripeCustomerID,
		UserID:     userID,
		SuccessURL: successURL,
		CancelURL:  cancelURL,
		TrialDays:  trial,
	})
}

func (s *BillingService) CreatePortalSession(ctx context.Context, userID, returnURL string) (string, error) {
	if s.selfHosted {
		return "", fmt.Errorf("%w: billing is disabled on self-hosted instances", domain.ErrSelfHosted)
	}
	if returnURL == "" {
		return "", fmt.Errorf("%w: returnUrl is required", domain.ErrValidation)
	}
	sub, err := s.subs.GetByUserID(ctx, userID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && sub.StripeCustomerID == "") {
		return "", fmt.Errorf("%w: no billing profile yet; start a checkout first", domain.ErrValidation)
	}
	if err != nil {
		return "", err
	}
	return s.payments.CreatePortalSession(ctx, sub.StripeCustomerID, returnURL)
}

func (s *BillingService) HandleWebhook(ctx context.Context, payload []byte, sigHeader string) error {
	if s.selfHosted {
		return fmt.Errorf("%w: billing is disabled on self-hosted instances", domain.ErrSelfHosted)
	}
	ev, err := s.payments.ParseWebhook(payload, sigHeader)
	if err != nil {
		return fmt.Errorf("%w: webhook signature verification failed: %v", domain.ErrUnauthorized, err)
	}
	// Record the idempotency marker and apply the state change in one
	// transaction: the marker commits only if the update commits, so a transient
	// failure rolls both back and Stripe's retry re-drives the event
	// (record-after-success, not record-before-processing).
	return s.tx.RunInTx(ctx, func(ctx context.Context) error {
		first, err := s.events.Record(ctx, ev.ID, ev.Type)
		if err != nil {
			return err
		}
		if !first {
			return nil // already processed; replays are no-ops
		}
		return s.applyWebhookEvent(ctx, ev)
	})
}

func (s *BillingService) applyWebhookEvent(ctx context.Context, ev port.WebhookEvent) error {
	if ev.Status == "" && ev.SubscriptionID == "" {
		return nil // event carries no subscription state (e.g. unrelated type)
	}

	// Resolve the Calendium user: metadata.user_id first, then the customer.
	userID := ev.UserID
	var existing domain.Subscription
	var err error
	if userID == "" {
		if ev.CustomerID == "" {
			return nil
		}
		existing, err = s.subs.GetByStripeCustomerID(ctx, ev.CustomerID)
		if errors.Is(err, domain.ErrNotFound) {
			return nil // customer unknown to us; ignore
		}
		if err != nil {
			return err
		}
		userID = existing.UserID
	} else {
		existing, err = s.subs.GetByUserID(ctx, userID)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
	}

	// Stripe does not guarantee event ordering. Only customer.subscription.*
	// events carry the authoritative lifecycle status/period, so only they are
	// ordered against each other: drop one older than the newest lifecycle event
	// already applied, preventing a delayed/re-delivered event from reverting a
	// canceled/past_due subscription back to active.
	isSubEvent := strings.HasPrefix(ev.Type, "customer.subscription.")
	if isSubEvent && ev.Created != nil && existing.LastEventAt != nil && ev.Created.Before(*existing.LastEventAt) {
		return nil
	}

	// invoice.* and checkout.session.completed do not carry period/cancel/trial
	// data (webhook.go leaves them zero); preserve the mirrored values so they
	// are not clobbered. Only subscription.* events overwrite them below.
	sub := domain.Subscription{
		UserID:               userID,
		Status:               ev.Status,
		Plan:                 domain.PlanAnnual,
		PriceUSD:             domain.PriceUSDAnnual,
		CurrentPeriodEnd:     existing.CurrentPeriodEnd,
		CancelAtPeriodEnd:    existing.CancelAtPeriodEnd,
		TrialEndsAt:          existing.TrialEndsAt,
		StripeCustomerID:     firstNonEmpty(ev.CustomerID, existing.StripeCustomerID),
		StripeSubscriptionID: firstNonEmpty(ev.SubscriptionID, existing.StripeSubscriptionID),
		LastEventAt:          existing.LastEventAt,
	}
	if isSubEvent {
		sub.CurrentPeriodEnd = ev.CurrentPeriodEnd
		sub.CancelAtPeriodEnd = ev.CancelAtPeriodEnd
		sub.TrialEndsAt = ev.TrialEndsAt
		if ev.Created != nil && (sub.LastEventAt == nil || ev.Created.After(*sub.LastEventAt)) {
			sub.LastEventAt = ev.Created
		}
	}
	if sub.Status == "" {
		sub.Status = existing.Status
	}
	if sub.Status == "" {
		sub.Status = domain.SubscriptionNone
	}
	return s.subs.Upsert(ctx, sub)
}

func (s *BillingService) RequireActive(ctx context.Context, userID string) error {
	return entitlement{subs: s.subs, clock: s.clock, selfHost: s.selfHosted}.require(ctx, userID)
}
