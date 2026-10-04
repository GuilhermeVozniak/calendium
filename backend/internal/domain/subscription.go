package domain

import (
	"fmt"
	"time"
)

// SubscriptionStatus is the Paddle-mirrored lifecycle (docs/payments.md):
// none → trialing → active → (past_due → active | paused | canceled).
// Expiry is never stored: HasAccess computes it from time, so a stale mirror
// can never keep a lapsed user entitled.
type SubscriptionStatus string

const (
	SubscriptionTrialing SubscriptionStatus = "trialing"
	SubscriptionActive   SubscriptionStatus = "active"
	SubscriptionPastDue  SubscriptionStatus = "past_due"
	SubscriptionPaused   SubscriptionStatus = "paused"
	SubscriptionCanceled SubscriptionStatus = "canceled"
	SubscriptionNone     SubscriptionStatus = "none"
)

const (
	// PlanAnnual is the only plan: Calendium Annual, $50/year.
	PlanAnnual = "annual"
	// PriceUSDAnnual is the fixed yearly price in whole dollars.
	PriceUSDAnnual = 50
	// TrialLength is the card-free trial granted server-side at signup.
	TrialLength = 14 * 24 * time.Hour
	// ActiveGrace keeps an active subscription entitled past its period end
	// while the renewal webhook is late.
	ActiveGrace = 3 * 24 * time.Hour
	// PastDueGrace is the dunning window honoured for a failed renewal.
	PastDueGrace = 7 * 24 * time.Hour
)

// Subscription is the single $50/yr annual plan (Spotify model — the web
// checkout is the only purchase surface; mobile apps only reflect state).
type Subscription struct {
	UserID            string             `json:"-"`
	Status            SubscriptionStatus `json:"status"`
	Plan              string             `json:"plan"`     // always PlanAnnual
	PriceUSD          int                `json:"priceUsd"` // always PriceUSDAnnual
	CurrentPeriodEnd  *time.Time         `json:"currentPeriodEnd"`
	CancelAtPeriodEnd bool               `json:"cancelAtPeriodEnd"`
	TrialEndsAt       *time.Time         `json:"trialEndsAt"`
	// BillingCustomerID / BillingSubscriptionID are the provider's ids
	// (Paddle ctm_/sub_); internal state, never part of the API payload.
	BillingCustomerID     string `json:"-"`
	BillingSubscriptionID string `json:"-"`
	// LastEventAt is the provider `occurred_at` of the most recent
	// subscription.* event applied to this mirror; it orders lifecycle events
	// so an out-of-order or re-delivered older one is dropped.
	LastEventAt *time.Time `json:"-"`
}

// DenialReason names why a subscription does not grant access; it is the
// `details.reason` of 402 responses.
type DenialReason string

const (
	DenialTrialEnded DenialReason = "trial_ended"
	DenialPastDue    DenialReason = "past_due"
	DenialCanceled   DenialReason = "canceled"
	DenialPaused     DenialReason = "paused"
	DenialNone       DenialReason = "none"
)

// DenialReason is the single entitlement rule. It returns "" when the
// subscription grants access at `now`, otherwise the reason it does not:
//
//	trialing  -> now < TrialEndsAt
//	active    -> CurrentPeriodEnd == nil || now < CurrentPeriodEnd + ActiveGrace
//	past_due  -> CurrentPeriodEnd != nil && now < CurrentPeriodEnd + PastDueGrace
//	paused, canceled, none (and anything unknown) -> denied
func (s Subscription) DenialReason(now time.Time) DenialReason {
	switch s.Status {
	case SubscriptionTrialing:
		if s.TrialEndsAt != nil && now.Before(*s.TrialEndsAt) {
			return ""
		}
		return DenialTrialEnded
	case SubscriptionActive:
		if s.CurrentPeriodEnd == nil || now.Before(s.CurrentPeriodEnd.Add(ActiveGrace)) {
			return ""
		}
		return DenialPastDue
	case SubscriptionPastDue:
		if s.CurrentPeriodEnd != nil && now.Before(s.CurrentPeriodEnd.Add(PastDueGrace)) {
			return ""
		}
		return DenialPastDue
	case SubscriptionPaused:
		return DenialPaused
	case SubscriptionCanceled:
		return DenialCanceled
	default:
		return DenialNone
	}
}

// HasAccess reports whether the subscription currently unlocks the product.
func (s Subscription) HasAccess(now time.Time) bool {
	return s.DenialReason(now) == ""
}

// PaymentRequiredError is the typed form of ErrPaymentRequired carrying the
// 402 `details` body. errors.Is(err, ErrPaymentRequired) holds for it.
type PaymentRequiredError struct {
	Reason           DenialReason
	TrialEndsAt      *time.Time
	CurrentPeriodEnd *time.Time
}

// NewPaymentRequiredError builds the denial for s at now. Call it only when
// !s.HasAccess(now).
func NewPaymentRequiredError(s Subscription, now time.Time) *PaymentRequiredError {
	return &PaymentRequiredError{
		Reason:           s.DenialReason(now),
		TrialEndsAt:      s.TrialEndsAt,
		CurrentPeriodEnd: s.CurrentPeriodEnd,
	}
}

func (e *PaymentRequiredError) Error() string {
	return fmt.Sprintf("payment required: %s", e.Reason)
}

// Unwrap lets errors.Is(err, ErrPaymentRequired) match the typed error.
func (e *PaymentRequiredError) Unwrap() error { return ErrPaymentRequired }
