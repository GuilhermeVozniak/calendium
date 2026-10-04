package postgres

import (
	"context"
	"database/sql"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- port.UserRepo -----------------------------------------------------------

const userCols = `id, email, name, avatar_url, created_at`

func scanUser(r rowScanner) (domain.User, error) {
	var u domain.User
	var name, avatar sql.NullString
	if err := r.Scan(&u.ID, &u.Email, &name, &avatar, &u.CreatedAt); err != nil {
		return domain.User{}, notFound(err)
	}
	u.Name = strPtr(name)
	u.AvatarURL = strPtr(avatar)
	return u, nil
}

func (r userRepo) Upsert(ctx context.Context, u domain.User) (domain.User, error) {
	row := r.q(ctx).QueryRowContext(ctx, `
		INSERT INTO users (id, email, name, avatar_url)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET
			email      = EXCLUDED.email,
			name       = COALESCE(EXCLUDED.name, users.name),
			avatar_url = COALESCE(EXCLUDED.avatar_url, users.avatar_url)
		RETURNING `+userCols,
		u.ID, u.Email, nullStrPtr(u.Name), nullStrPtr(u.AvatarURL))
	return scanUser(row)
}

func (r userRepo) GetByID(ctx context.Context, id string) (domain.User, error) {
	row := r.q(ctx).QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id)
	return scanUser(row)
}

// --- port.SubscriptionRepo ---------------------------------------------------

const subscriptionCols = `user_id, status, plan, price_usd, billing_customer_id,
	billing_subscription_id, current_period_end, cancel_at_period_end, trial_ends_at, last_event_at`

func scanSubscription(r rowScanner) (domain.Subscription, error) {
	var s domain.Subscription
	var customerID, subscriptionID sql.NullString
	var periodEnd, trialEnds, lastEvent sql.NullTime
	if err := r.Scan(&s.UserID, &s.Status, &s.Plan, &s.PriceUSD, &customerID,
		&subscriptionID, &periodEnd, &s.CancelAtPeriodEnd, &trialEnds, &lastEvent); err != nil {
		return domain.Subscription{}, notFound(err)
	}
	s.BillingCustomerID = customerID.String
	s.BillingSubscriptionID = subscriptionID.String
	s.CurrentPeriodEnd = timePtr(periodEnd)
	s.TrialEndsAt = timePtr(trialEnds)
	s.LastEventAt = timePtr(lastEvent)
	return s, nil
}

func (r subscriptionRepo) GetByUserID(ctx context.Context, userID string) (domain.Subscription, error) {
	row := r.q(ctx).QueryRowContext(ctx,
		`SELECT `+subscriptionCols+` FROM subscriptions WHERE user_id = $1`, userID)
	return scanSubscription(row)
}

func (r subscriptionRepo) GetByBillingCustomerID(ctx context.Context, customerID string) (domain.Subscription, error) {
	row := r.q(ctx).QueryRowContext(ctx,
		`SELECT `+subscriptionCols+` FROM subscriptions WHERE billing_customer_id = $1`, customerID)
	return scanSubscription(row)
}

func (r subscriptionRepo) Upsert(ctx context.Context, s domain.Subscription) error {
	if s.Status == "" {
		s.Status = domain.SubscriptionNone
	}
	if s.Plan == "" {
		s.Plan = domain.PlanAnnual
	}
	if s.PriceUSD == 0 {
		s.PriceUSD = domain.PriceUSDAnnual
	}
	_, err := r.q(ctx).ExecContext(ctx, `
		INSERT INTO subscriptions (user_id, status, plan, price_usd, billing_customer_id,
			billing_subscription_id, current_period_end, cancel_at_period_end, trial_ends_at, last_event_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now())
		ON CONFLICT (user_id) DO UPDATE SET
			status                  = EXCLUDED.status,
			plan                    = EXCLUDED.plan,
			price_usd               = EXCLUDED.price_usd,
			billing_customer_id     = COALESCE(EXCLUDED.billing_customer_id, subscriptions.billing_customer_id),
			billing_subscription_id = COALESCE(EXCLUDED.billing_subscription_id, subscriptions.billing_subscription_id),
			current_period_end      = EXCLUDED.current_period_end,
			cancel_at_period_end    = EXCLUDED.cancel_at_period_end,
			trial_ends_at           = EXCLUDED.trial_ends_at,
			last_event_at           = EXCLUDED.last_event_at,
			updated_at              = now()`,
		s.UserID, string(s.Status), s.Plan, s.PriceUSD, nullStr(s.BillingCustomerID),
		nullStr(s.BillingSubscriptionID), nullTimePtr(s.CurrentPeriodEnd), s.CancelAtPeriodEnd,
		nullTimePtr(s.TrialEndsAt), nullTimePtr(s.LastEventAt))
	return err
}

// EnsureTrial grants the signup trial exactly once per user: ON CONFLICT DO
// NOTHING makes concurrent first calls safe and never touches an existing row.
func (r subscriptionRepo) EnsureTrial(ctx context.Context, userID string, trialEndsAt time.Time) error {
	_, err := r.q(ctx).ExecContext(ctx, `
		INSERT INTO subscriptions (user_id, status, plan, price_usd, trial_ends_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (user_id) DO NOTHING`,
		userID, string(domain.SubscriptionTrialing), domain.PlanAnnual, domain.PriceUSDAnnual, trialEndsAt)
	return err
}

func (r subscriptionRepo) ListForReconciliation(ctx context.Context, now time.Time) ([]domain.Subscription, error) {
	rows, err := r.q(ctx).QueryContext(ctx, `
		SELECT `+subscriptionCols+` FROM subscriptions
		WHERE billing_subscription_id IS NOT NULL
		  AND (
		    (status IN ($2, $3, $4) AND current_period_end < $1::timestamptz - interval '1 hour')
		    OR last_event_at < $1::timestamptz - interval '7 days'
		  )
		ORDER BY user_id`,
		now, string(domain.SubscriptionActive), string(domain.SubscriptionPastDue), string(domain.SubscriptionPaused))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []domain.Subscription
	for rows.Next() {
		s, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// --- port.BillingEventRepo ---------------------------------------------------

func (r billingEventRepo) Record(ctx context.Context, ev port.SubscriptionEvent) (bool, error) {
	res, err := r.q(ctx).ExecContext(ctx,
		`INSERT INTO billing_events (notification_id, event_id, event_type, occurred_at)
		 VALUES ($1, $2, $3, $4) ON CONFLICT (notification_id) DO NOTHING`,
		ev.NotificationID, ev.EventID, ev.Type, ev.OccurredAt)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}
