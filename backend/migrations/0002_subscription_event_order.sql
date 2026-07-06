-- Stripe does not guarantee webhook event ordering. Record the `created`
-- timestamp of the most recent customer.subscription.* event applied to each
-- mirror row so a delayed or re-delivered older lifecycle event can be
-- ignored instead of reverting status/period back to a stale value.
ALTER TABLE subscriptions ADD COLUMN last_event_at timestamptz;
