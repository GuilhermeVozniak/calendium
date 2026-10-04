package paddle

import (
	"context"
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

// Implemented in Task 6.
func (c *Client) CreatePortalSession(context.Context, string, string) (port.PortalURLs, error) {
	return port.PortalURLs{}, fmt.Errorf("paddle: not implemented")
}
func (c *Client) GetSubscription(context.Context, string) (port.SubscriptionEvent, error) {
	return port.SubscriptionEvent{}, fmt.Errorf("paddle: not implemented")
}
func (c *Client) CancelSubscription(context.Context, string, bool) error {
	return fmt.Errorf("paddle: not implemented")
}

// Implemented in Task 7.
func (c *Client) ParseWebhook([]byte, string, time.Time) (port.SubscriptionEvent, error) {
	return port.SubscriptionEvent{}, fmt.Errorf("paddle: not implemented")
}
