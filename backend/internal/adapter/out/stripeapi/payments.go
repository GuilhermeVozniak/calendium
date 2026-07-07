package stripeapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// EnsureCustomer finds the Stripe customer bound to the Calendium user (by
// metadata.user_id) or creates one.
func (c *Client) EnsureCustomer(ctx context.Context, user domain.User) (string, error) {
	query := fmt.Sprintf("metadata['user_id']:'%s'", strings.ReplaceAll(user.ID, "'", `\'`))
	var search struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/customers/search", url.Values{"query": {query}, "limit": {"1"}}, &search); err != nil {
		return "", err
	}
	if len(search.Data) > 0 {
		return search.Data[0].ID, nil
	}

	form := url.Values{
		"email":             {user.Email},
		"metadata[user_id]": {user.ID},
	}
	if user.Name != nil && *user.Name != "" {
		form.Set("name", *user.Name)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/customers", form, &created); err != nil {
		return "", err
	}
	return created.ID, nil
}

// CreateCheckoutSession opens a subscription Checkout for the annual plan.
// The Calendium user id rides along as client_reference_id and metadata so
// webhooks can bind the subscription back to the user (docs/payments.md).
func (c *Client) CreateCheckoutSession(ctx context.Context, p port.CheckoutParams) (string, error) {
	form := url.Values{
		"mode":                                 {"subscription"},
		"line_items[0][price]":                 {c.annualPriceID},
		"line_items[0][quantity]":              {"1"},
		"success_url":                          {p.SuccessURL},
		"cancel_url":                           {p.CancelURL},
		"client_reference_id":                  {p.UserID},
		"metadata[user_id]":                    {p.UserID},
		"subscription_data[metadata][user_id]": {p.UserID},
	}
	if p.CustomerID != "" {
		form.Set("customer", p.CustomerID)
	}
	if p.TrialDays > 0 {
		form.Set("subscription_data[trial_period_days]", strconv.Itoa(p.TrialDays))
	}
	var session struct {
		URL string `json:"url"`
	}
	if err := c.do(ctx, http.MethodPost, "/checkout/sessions", form, &session); err != nil {
		return "", err
	}
	return session.URL, nil
}

// CreatePortalSession opens the Stripe Billing Portal (manage / cancel /
// update card).
func (c *Client) CreatePortalSession(ctx context.Context, customerID, returnURL string) (string, error) {
	form := url.Values{
		"customer":   {customerID},
		"return_url": {returnURL},
	}
	var session struct {
		URL string `json:"url"`
	}
	if err := c.do(ctx, http.MethodPost, "/billing_portal/sessions", form, &session); err != nil {
		return "", err
	}
	return session.URL, nil
}

// stripeSubscription is the subset of the Stripe subscription object the
// mirror needs.
type stripeSubscription struct {
	ID                string `json:"id"`
	Customer          string `json:"customer"`
	Status            string `json:"status"`
	CurrentPeriodEnd  int64  `json:"current_period_end"`
	CancelAtPeriodEnd bool   `json:"cancel_at_period_end"`
	TrialEnd          int64  `json:"trial_end"`
	Metadata          struct {
		UserID string `json:"user_id"`
	} `json:"metadata"`
}

// GetSubscription fetches a subscription by id (used when a webhook carries
// only the subscription reference).
func (c *Client) GetSubscription(ctx context.Context, subscriptionID string) (port.WebhookEvent, error) {
	var sub stripeSubscription
	if err := c.do(ctx, http.MethodGet, "/subscriptions/"+url.PathEscape(subscriptionID), nil, &sub); err != nil {
		return port.WebhookEvent{}, err
	}
	ev := port.WebhookEvent{
		CustomerID:        sub.Customer,
		SubscriptionID:    sub.ID,
		UserID:            sub.Metadata.UserID,
		Status:            mapSubscriptionStatus(sub.Status),
		CancelAtPeriodEnd: sub.CancelAtPeriodEnd,
		CurrentPeriodEnd:  unixPtr(sub.CurrentPeriodEnd),
		TrialEndsAt:       unixPtr(sub.TrialEnd),
	}
	return ev, nil
}

// mapSubscriptionStatus normalizes Stripe subscription statuses onto the
// docs/payments.md lifecycle.
func mapSubscriptionStatus(s string) domain.SubscriptionStatus {
	switch s {
	case "trialing":
		return domain.SubscriptionTrialing
	case "active":
		return domain.SubscriptionActive
	case "past_due":
		return domain.SubscriptionPastDue
	case "canceled":
		return domain.SubscriptionCanceled
	case "unpaid", "incomplete_expired":
		return domain.SubscriptionExpired
	case "incomplete", "paused":
		return domain.SubscriptionNone
	default:
		return domain.SubscriptionNone
	}
}

func unixPtr(sec int64) *time.Time {
	if sec == 0 {
		return nil
	}
	t := time.Unix(sec, 0).UTC()
	return &t
}
