package domain

import "time"

// SubscriptionStatus follows the Stripe-driven lifecycle documented in
// docs/payments.md: none → trialing → active → (past_due → active | canceled) → expired.
type SubscriptionStatus string

const (
	SubscriptionTrialing SubscriptionStatus = "trialing"
	SubscriptionActive   SubscriptionStatus = "active"
	SubscriptionPastDue  SubscriptionStatus = "past_due"
	SubscriptionCanceled SubscriptionStatus = "canceled"
	SubscriptionExpired  SubscriptionStatus = "expired"
	SubscriptionNone     SubscriptionStatus = "none"
)

const (
	// PlanAnnual is the only plan: Calendium Annual, $50/year.
	PlanAnnual = "annual"
	// PriceUSDAnnual is the fixed yearly price in whole dollars.
	PriceUSDAnnual = 50
	// PastDueGrace keeps access alive while Stripe retries a failed renewal.
	PastDueGrace = 7 * 24 * time.Hour
)

// Subscription is the single $50/yr annual plan (Spotify model — Stripe is
// the only biller; mobile apps never sell, they only reflect this state).
type Subscription struct {
	UserID               string             `json:"-"`
	Status               SubscriptionStatus `json:"status"`
	Plan                 string             `json:"plan"`     // always PlanAnnual
	PriceUSD             int                `json:"priceUsd"` // always PriceUSDAnnual
	CurrentPeriodEnd     *time.Time         `json:"currentPeriodEnd"`
	CancelAtPeriodEnd    bool               `json:"cancelAtPeriodEnd"`
	TrialEndsAt          *time.Time         `json:"trialEndsAt"`
	StripeCustomerID     string             `json:"-"`
	StripeSubscriptionID string             `json:"-"`
	// LastEventAt is the Stripe `created` time of the most recent
	// customer.subscription.* event applied to this mirror. It orders
	// lifecycle events so out-of-order/re-delivered older ones are dropped;
	// it is internal state, never part of the API payload.
	LastEventAt *time.Time `json:"-"`
}

// HasAccess reports whether the subscription currently unlocks the product.
// trialing and active always do; past_due keeps access for PastDueGrace past
// the period end while Stripe retries; everything else is paywalled.
func (s Subscription) HasAccess(now time.Time) bool {
	switch s.Status {
	case SubscriptionTrialing, SubscriptionActive:
		return true
	case SubscriptionPastDue:
		if s.CurrentPeriodEnd == nil {
			return false
		}
		return now.Before(s.CurrentPeriodEnd.Add(PastDueGrace))
	default:
		return false
	}
}
