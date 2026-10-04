package paddle

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// EnsureCustomer finds the Paddle customer by exact email
// (GET /customers?email=) or creates one (POST /customers).
func (c *Client) EnsureCustomer(ctx context.Context, user domain.User) (string, error) {
	var found []struct {
		ID string `json:"id"`
	}
	q := url.Values{"email": {user.Email}}
	if err := c.do(ctx, http.MethodGet, "/customers?"+q.Encode(), nil, &found); err != nil {
		return "", err
	}
	if len(found) > 0 {
		return found[0].ID, nil
	}
	body := map[string]any{"email": user.Email}
	if user.Name != nil && *user.Name != "" {
		body["name"] = *user.Name
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/customers", body, &created); err != nil {
		return "", err
	}
	return created.ID, nil
}

// CreateCheckout creates a transaction for the annual price bound to the
// user (custom_data.user_id) and returns data.checkout.url — the default
// payment link plus `_ptxn`, which Paddle.js on the web /checkout page turns
// into the overlay. No URLs are sent: Paddle owns where checkout lands.
func (c *Client) CreateCheckout(ctx context.Context, p port.CheckoutParams) (string, error) {
	body := map[string]any{
		"items":       []map[string]any{{"price_id": c.cfg.AnnualPriceID, "quantity": 1}},
		"customer_id": p.CustomerID,
		"custom_data": map[string]string{"user_id": p.UserID},
	}
	var txn struct {
		ID       string `json:"id"`
		Checkout struct {
			URL string `json:"url"`
		} `json:"checkout"`
	}
	if err := c.do(ctx, http.MethodPost, "/transactions", body, &txn); err != nil {
		return "", err
	}
	if txn.Checkout.URL == "" {
		return "", fmt.Errorf("paddle: transaction %s has no checkout url (is the default payment link configured in the Paddle dashboard?)", txn.ID)
	}
	return txn.Checkout.URL, nil
}

// CreatePortalSession opens a temporary customer-portal session. Overview
// is always present; Cancel/UpdatePayment are filled only for the given
// subscription id (empty when there is none). Links must not be cached.
func (c *Client) CreatePortalSession(ctx context.Context, customerID, subscriptionID string) (port.PortalURLs, error) {
	body := map[string]any{}
	if subscriptionID != "" {
		body["subscription_ids"] = []string{subscriptionID}
	}
	var session struct {
		URLs struct {
			General struct {
				Overview string `json:"overview"`
			} `json:"general"`
			Subscriptions []struct {
				ID                              string `json:"id"`
				CancelSubscription              string `json:"cancel_subscription"`
				UpdateSubscriptionPaymentMethod string `json:"update_subscription_payment_method"`
			} `json:"subscriptions"`
		} `json:"urls"`
	}
	if err := c.do(ctx, http.MethodPost, "/customers/"+url.PathEscape(customerID)+"/portal-sessions", body, &session); err != nil {
		return port.PortalURLs{}, err
	}
	out := port.PortalURLs{Overview: session.URLs.General.Overview}
	for _, s := range session.URLs.Subscriptions {
		if s.ID == subscriptionID {
			out.Cancel = s.CancelSubscription
			out.UpdatePayment = s.UpdateSubscriptionPaymentMethod
		}
	}
	return out, nil
}

// subscription is the subset of Paddle's subscription entity the mirror
// needs. custom_data, current_billing_period and scheduled_change are
// nullable in Paddle payloads (canceled subscriptions have no period).
type subscription struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	CustomerID string `json:"customer_id"`
	CustomData *struct {
		UserID string `json:"user_id"`
	} `json:"custom_data"`
	CurrentBillingPeriod *struct {
		StartsAt time.Time `json:"starts_at"`
		EndsAt   time.Time `json:"ends_at"`
	} `json:"current_billing_period"`
	ScheduledChange *struct {
		Action      string    `json:"action"`
		EffectiveAt time.Time `json:"effective_at"`
	} `json:"scheduled_change"`
}

// normalizeSubscription copies the entity onto ev (envelope fields already
// set by the caller) using the docs/payments.md mapping.
func normalizeSubscription(sub subscription, ev port.SubscriptionEvent) port.SubscriptionEvent {
	ev.SubscriptionID = sub.ID
	ev.CustomerID = sub.CustomerID
	if sub.CustomData != nil {
		ev.UserID = sub.CustomData.UserID
	}
	ev.Status = mapStatus(sub.Status)
	if sub.CurrentBillingPeriod != nil && !sub.CurrentBillingPeriod.EndsAt.IsZero() {
		end := sub.CurrentBillingPeriod.EndsAt.UTC()
		ev.CurrentPeriodEnd = &end
	}
	ev.CancelAtPeriodEnd = sub.ScheduledChange != nil && sub.ScheduledChange.Action == "cancel"
	return ev
}

// GetSubscription reads the live subscription for reconciliation. The
// caller stamps OccurredAt (there is no event time on a read).
func (c *Client) GetSubscription(ctx context.Context, subscriptionID string) (port.SubscriptionEvent, error) {
	var sub subscription
	if err := c.do(ctx, http.MethodGet, "/subscriptions/"+url.PathEscape(subscriptionID), nil, &sub); err != nil {
		return port.SubscriptionEvent{}, err
	}
	return normalizeSubscription(sub, port.SubscriptionEvent{Type: "subscription.reconciled"}), nil
}

// alreadyCanceledCodes are Paddle request-error codes meaning the
// subscription is already canceled, so a cancel has nothing left to do.
var alreadyCanceledCodes = map[string]bool{
	"subscription_is_canceled_action_invalid": true,
	"subscription_update_when_canceled":       true,
}

// CancelSubscription cancels at the next billing period (Paddle keeps
// status=active with scheduled_change.action=cancel) or immediately.
// Idempotent: when Paddle refuses because the subscription is already
// canceled — a known error code, or any other 4xx request error after which
// the subscription reads back as canceled — the cancel counts as done (a
// retried account purge must not stall on its own earlier cancel).
// Auth, not-found, rate-limit and 5xx failures are returned unchanged.
func (c *Client) CancelSubscription(ctx context.Context, subscriptionID string, immediately bool) error {
	effective := "next_billing_period"
	if immediately {
		effective = "immediately"
	}
	err := c.do(ctx, http.MethodPost, "/subscriptions/"+url.PathEscape(subscriptionID)+"/cancel",
		map[string]string{"effective_from": effective}, nil)
	var apiErr *APIError
	if err == nil || !errors.As(err, &apiErr) || !cancelRefusal(apiErr.Status) {
		return err
	}
	if alreadyCanceledCodes[apiErr.Code] {
		return nil
	}
	sub, gerr := c.GetSubscription(ctx, subscriptionID)
	if gerr == nil && sub.Status == domain.SubscriptionCanceled {
		return nil
	}
	return err
}

// cancelRefusal: a 4xx that is a statement about the subscription's state,
// not an auth/lookup/throttle failure.
func cancelRefusal(status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests:
		return false
	}
	return status >= 400 && status < 500
}

// mapStatus normalizes Paddle statuses onto the domain set. Paddle trials
// are never configured, so "trialing" is treated as paid-active
// defensively; anything unrecognized fails closed to none.
func mapStatus(s string) domain.SubscriptionStatus {
	switch s {
	case "active", "trialing":
		return domain.SubscriptionActive
	case "past_due":
		return domain.SubscriptionPastDue
	case "paused":
		return domain.SubscriptionPaused
	case "canceled":
		return domain.SubscriptionCanceled
	default:
		return domain.SubscriptionNone
	}
}
