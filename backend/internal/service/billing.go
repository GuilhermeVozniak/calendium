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

// inlineReconcileTimeout bounds the provider read done inline by
// GetSubscription, so a hung provider cannot stall the request.
const inlineReconcileTimeout = 5 * time.Second

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
// none exists yet, and best-effort reconciling an active row that is past
// its period end (throttled per user).
func (s *BillingService) GetSubscription(ctx context.Context, userID string) (domain.Subscription, error) {
	if s.selfHosted {
		return selfHostedSubscription(userID), nil
	}
	sub, err := s.subs.GetByUserID(ctx, userID)
	if errors.Is(err, domain.ErrNotFound) {
		sub, err = grantTrial(ctx, s.users, s.subs, userID)
	}
	if err != nil {
		return domain.Subscription{}, err
	}
	// Best-effort inline reconcile: an active row past its period end with a
	// provider subscription is re-read at most once per 10 minutes per user.
	now := s.clock.Now()
	if sub.Status == domain.SubscriptionActive && sub.BillingSubscriptionID != "" &&
		sub.CurrentPeriodEnd != nil && !now.Before(*sub.CurrentPeriodEnd) &&
		s.claimInlineReconcile(userID, now) {
		ictx, cancel := context.WithTimeout(ctx, inlineReconcileTimeout)
		fresh, err := s.reconcileOne(ictx, sub)
		cancel()
		if err != nil {
			s.logger.Warn("billing: inline reconcile failed", "user_id", userID, "error", err)
		} else {
			sub = fresh
		}
	}
	return sub, nil
}

// grantTrial anchors the 14-day trial to users.created_at (signup, since
// the users row is provisioned on the first authenticated call). EnsureTrial
// is ON CONFLICT DO NOTHING, so concurrent first calls both read back the
// same row. Shared by GetSubscription and the entitlement gate; an unknown
// user propagates ErrNotFound.
func grantTrial(ctx context.Context, users port.UserRepo, subs port.SubscriptionRepo, userID string) (domain.Subscription, error) {
	user, err := users.GetByID(ctx, userID)
	if err != nil {
		return domain.Subscription{}, err
	}
	if err := subs.EnsureTrial(ctx, userID, user.CreatedAt.Add(domain.TrialLength)); err != nil {
		return domain.Subscription{}, err
	}
	return subs.GetByUserID(ctx, userID)
}

// CreateCheckout starts the hosted checkout: grants the trial row if
// missing, refuses a second live subscription, ensures the provider
// customer (persisting its id), then creates the transaction. No URLs are
// accepted from clients; Paddle returns the checkout url.
func (s *BillingService) CreateCheckout(ctx context.Context, userID string) (string, error) {
	if s.selfHosted {
		return "", fmt.Errorf("%w: billing is disabled on self-hosted instances", domain.ErrSelfHosted)
	}
	sub, err := s.GetSubscription(ctx, userID)
	if err != nil {
		return "", err
	}
	if sub.BillingSubscriptionID != "" {
		switch sub.Status {
		case domain.SubscriptionActive, domain.SubscriptionPastDue, domain.SubscriptionPaused:
			return "", fmt.Errorf("%w: subscription %s is %s", domain.ErrAlreadySubscribed, sub.BillingSubscriptionID, sub.Status)
		}
	}
	if sub.BillingCustomerID == "" {
		user, err := s.users.GetByID(ctx, userID)
		if err != nil {
			return "", err
		}
		customerID, err := s.payments.EnsureCustomer(ctx, user)
		if err != nil {
			return "", billingUnavailable("ensure customer", err)
		}
		sub.BillingCustomerID = customerID
		if sub.LastEventAt == nil {
			// Every service write leaves a non-zero last_event_at: the trial
			// row EnsureTrial created has none, and ListForReconciliation
			// exempts NULL (controller ruling, Track A review).
			now := s.clock.Now()
			sub.LastEventAt = &now
		}
		// Guarded write: if a webhook landed since the read, its newer row
		// (which carries the customer id) wins and this write is skipped.
		if _, err := s.subs.UpsertIfNewer(ctx, sub); err != nil {
			return "", err
		}
	}
	url, err := s.payments.CreateCheckout(ctx, port.CheckoutParams{UserID: userID, CustomerID: sub.BillingCustomerID})
	if err != nil {
		return "", billingUnavailable("create checkout", err)
	}
	return url, nil
}

// CreatePortalSession returns temporary portal links. Cancellation and
// payment-method updates happen in Paddle's portal and flow back by webhook.
func (s *BillingService) CreatePortalSession(ctx context.Context, userID string) (port.PortalURLs, error) {
	if s.selfHosted {
		return port.PortalURLs{}, fmt.Errorf("%w: billing is disabled on self-hosted instances", domain.ErrSelfHosted)
	}
	sub, err := s.subs.GetByUserID(ctx, userID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && sub.BillingCustomerID == "") {
		return port.PortalURLs{}, fmt.Errorf("%w: no billing profile yet; start a checkout first", domain.ErrNoBillingProfile)
	}
	if err != nil {
		return port.PortalURLs{}, err
	}
	urls, err := s.payments.CreatePortalSession(ctx, sub.BillingCustomerID, sub.BillingSubscriptionID)
	if err != nil {
		return port.PortalURLs{}, billingUnavailable("create portal session", err)
	}
	if sub.BillingSubscriptionID == "" {
		urls.Cancel, urls.UpdatePayment = "", ""
	}
	return urls, nil
}

// billingUnavailable wraps a provider failure for the HTTP layer (502).
func billingUnavailable(op string, err error) error {
	return fmt.Errorf("%w: %s: %v", domain.ErrBillingUnavailable, op, err)
}

// HandleWebhook runs the pipeline: verify (401) → ignore non-subscription
// types (200) → one tx { record notification id (replay → 200) → resolve
// user → ordering guard → upsert }. The marker commits only if the update
// commits, so a transient failure rolls both back and Paddle's retry
// re-drives the event.
func (s *BillingService) HandleWebhook(ctx context.Context, payload []byte, sigHeader string) error {
	if s.selfHosted {
		return fmt.Errorf("%w: billing is disabled on self-hosted instances", domain.ErrSelfHosted)
	}
	ev, err := s.payments.ParseWebhook(payload, sigHeader, s.clock.Now())
	if err != nil {
		if errors.Is(err, domain.ErrValidation) {
			return err
		}
		return fmt.Errorf("%w: webhook signature verification failed: %v", domain.ErrUnauthorized, err)
	}
	if ev.Ignored {
		return nil
	}
	return s.tx.RunInTx(ctx, func(ctx context.Context) error {
		first, err := s.events.Record(ctx, ev)
		if err != nil {
			return err
		}
		if !first {
			return nil // already processed; duplicate deliveries are no-ops
		}
		return s.applyEvent(ctx, ev, false)
	})
}

// applyEvent resolves the user (custom_data.user_id, else the stored
// customer id) and upserts the mirror. Unless force (reconciliation), the
// write goes through UpsertIfNewer, whose SQL guard drops an event older
// than the stored last_event_at atomically; the read here only resolves
// the user and passes the stored trial / ids through. A provider subscription
// existing clears trial_ends_at; otherwise the stored trial end is passed
// through, because the Postgres Upsert writes trial_ends_at verbatim. An
// event without occurred_at is stamped with now so last_event_at is never
// NULL (ListForReconciliation exempts NULL) — both controller rulings from
// the Track A review.
func (s *BillingService) applyEvent(ctx context.Context, ev port.SubscriptionEvent, force bool) error {
	userID := ev.UserID
	var existing domain.Subscription
	var err error
	if userID == "" {
		if ev.CustomerID == "" {
			s.logger.Warn("billing: event carries neither user id nor customer id", "event_id", ev.EventID, "type", ev.Type)
			return nil
		}
		existing, err = s.subs.GetByBillingCustomerID(ctx, ev.CustomerID)
		if errors.Is(err, domain.ErrNotFound) {
			s.logger.Warn("billing: event for unknown customer", "event_id", ev.EventID, "customer_id", ev.CustomerID, "type", ev.Type)
			return nil
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

	// An event with no occurred_at (not produced by the Paddle parser, but
	// never written as NULL) is treated as happening now.
	occurred := ev.OccurredAt
	if occurred.IsZero() {
		occurred = s.clock.Now()
	}

	sub := domain.Subscription{
		UserID:                userID,
		Status:                ev.Status,
		Plan:                  domain.PlanAnnual,
		PriceUSD:              domain.PriceUSDAnnual,
		CurrentPeriodEnd:      ev.CurrentPeriodEnd,
		CancelAtPeriodEnd:     ev.CancelAtPeriodEnd,
		TrialEndsAt:           existing.TrialEndsAt,
		BillingCustomerID:     firstNonEmpty(ev.CustomerID, existing.BillingCustomerID),
		BillingSubscriptionID: firstNonEmpty(ev.SubscriptionID, existing.BillingSubscriptionID),
		LastEventAt:           &occurred,
	}
	if sub.BillingSubscriptionID != "" {
		sub.TrialEndsAt = nil // a provider subscription supersedes the signup trial
	}
	if sub.Status == "" {
		sub.Status = existing.Status
	}
	if sub.Status == "" {
		sub.Status = domain.SubscriptionNone
	}
	if force {
		return s.subs.Upsert(ctx, sub)
	}
	// Paddle does not guarantee delivery order: an event older than the
	// newest stored one is skipped by the repo (same-instant replays apply).
	applied, err := s.subs.UpsertIfNewer(ctx, sub)
	if err != nil {
		return err
	}
	if !applied {
		s.logger.Info("billing: stale event not applied", "event_id", ev.EventID, "type", ev.Type, "user_id", userID)
	}
	return nil
}

// ReconcileSubscriptions re-reads every stale row from Paddle and applies
// it as a synthetic event stamped at its own apply time (so it always
// wins). Provider errors are logged and skipped; only a list failure or
// ctx ending (shutdown) is returned.
func (s *BillingService) ReconcileSubscriptions(ctx context.Context) error {
	if s.selfHosted {
		return nil
	}
	rows, err := s.subs.ListForReconciliation(ctx, s.clock.Now())
	if err != nil {
		return err
	}
	for _, sub := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, err := s.reconcileOne(ctx, sub); err != nil {
			s.logger.Warn("billing: reconcile failed", "user_id", sub.UserID, "subscription_id", sub.BillingSubscriptionID, "error", err)
		}
	}
	return nil
}

// reconcileOne fetches the live subscription and applies it through the
// webhook upsert path with OccurredAt = the clock at apply time (after the
// provider read, not the pass start) and force = true (the adapter's
// GetSubscription returns a zero OccurredAt by design, and the read must
// never be dropped as stale). Known narrow race, accepted: a webhook
// committed between the provider read and the forced write is overwritten
// by this slightly older snapshot; the next webhook or pass corrects it.
func (s *BillingService) reconcileOne(ctx context.Context, sub domain.Subscription) (domain.Subscription, error) {
	ev, err := s.payments.GetSubscription(ctx, sub.BillingSubscriptionID)
	if err != nil {
		return sub, err
	}
	ev.UserID = sub.UserID
	ev.OccurredAt = s.clock.Now()
	if ev.SubscriptionID == "" {
		ev.SubscriptionID = sub.BillingSubscriptionID
	}
	if err := s.applyEvent(ctx, ev, true); err != nil {
		return sub, err
	}
	return s.subs.GetByUserID(ctx, sub.UserID)
}

// claimInlineReconcile reserves the per-user inline slot. It is claimed
// before the provider call so a failing provider is not hammered. Entries
// older than the window are pruned on every claim, which bounds the map by
// the users that claimed within the last inlineReconcileEvery.
func (s *BillingService) claimInlineReconcile(userID string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, last := range s.lastInline {
		if now.Sub(last) >= inlineReconcileEvery {
			delete(s.lastInline, id)
		}
	}
	if _, ok := s.lastInline[userID]; ok {
		return false
	}
	s.lastInline[userID] = now
	return true
}

func (s *BillingService) RequireActive(ctx context.Context, userID string) error {
	return entitlement{subs: s.subs, users: s.users, clock: s.clock, selfHost: s.selfHosted}.require(ctx, userID)
}
