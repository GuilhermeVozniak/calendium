-- 0027_paddle_billing.sql — Stripe → Paddle Billing (docs/payments.md).
-- No data migration: there are no live subscribers. Provider columns get
-- provider-neutral names, the Stripe event ledger becomes a notification-id
-- ledger, and `expired` leaves the status set (expiry is computed from time
-- by domain.Subscription.HasAccess, never stored).

ALTER TABLE subscriptions RENAME COLUMN stripe_customer_id TO billing_customer_id;
ALTER TABLE subscriptions RENAME COLUMN stripe_subscription_id TO billing_subscription_id;
ALTER INDEX subscriptions_stripe_customer_idx RENAME TO subscriptions_billing_customer_idx;
ALTER INDEX subscriptions_stripe_subscription_idx RENAME TO subscriptions_billing_subscription_idx;

-- Defensive: nothing should hold 'expired', but the check below must apply.
UPDATE subscriptions SET status = 'canceled' WHERE status = 'expired';
ALTER TABLE subscriptions
    ADD CONSTRAINT subscriptions_status_check
    CHECK (status IN ('trialing', 'active', 'past_due', 'paused', 'canceled', 'none'));

-- Reconciliation scan: rows with a provider subscription whose billing
-- period lapsed or whose last event is older than a week.
CREATE INDEX subscriptions_reconcile_idx
    ON subscriptions (current_period_end, last_event_at)
    WHERE billing_subscription_id IS NOT NULL;

DROP TABLE stripe_events;

-- Webhook idempotency ledger keyed by Paddle notification id. A replay reuses
-- event_id with a fresh notification_id and is deliberately NOT deduplicated
-- here: the service's occurred_at ordering guard decides whether it applies.
CREATE TABLE billing_events (
    notification_id text PRIMARY KEY,
    event_id        text NOT NULL,
    event_type      text NOT NULL,
    occurred_at     timestamptz NOT NULL,
    received_at     timestamptz NOT NULL DEFAULT now()
);
