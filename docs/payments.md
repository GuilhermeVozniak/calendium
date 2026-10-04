# Payments — $50/year via Paddle Billing (Spotify model)

One plan: **Calendium Annual — $50/year**, 14-day free trial granted server-side at signup (no card), all platforms unlocked by the one subscription. **No in-app purchases anywhere.** Paddle is the only biller and our **merchant of record**: sales tax/VAT, invoices, receipts and refunds are handled by Paddle.

> **Cloud tier only.** Everything here applies to the managed **Calendium Cloud** tier (`SELF_HOSTED=false`). On self-hosted instances (`SELF_HOSTED=true`) billing is disabled: the paywall is off, `GET /v1/billing/subscription` reports a synthetic active plan, and `checkout`/`portal`/webhook return `501 self_hosted`. See [pricing-model.md](./pricing-model.md) and [self-hosting/README.md](./self-hosting/README.md).

## Why the Spotify model

Purchases never go through Apple/Google IAP: web and desktop run the Paddle overlay checkout in a browser, and the mobile apps never show a purchase UI — they only reflect subscription state fetched from the backend (`GET /v1/billing/subscription`) and link to `<webUrl>/pricing`.

## Trial and entitlement

- `GET /v1/billing/subscription` grants, for a user with no row, a `trialing` row with `trial_ends_at = users.created_at + 14d` (idempotent `ON CONFLICT DO NOTHING`). Every client shell fetches it before rendering, so the trial always exists before any gated call.
- Access is computed from time, never from a stored `expired` status (`domain.Subscription.HasAccess`):
  - `trialing` → `now < trial_ends_at`
  - `active` → `current_period_end` is null **or** `now < current_period_end + 3d` (late-renewal grace)
  - `past_due` → `now < current_period_end + 7d` (dunning window)
  - `paused`, `canceled`, `none` → denied
- Denials answer `402 {error:{code:"payment_required", message, details:{reason, trialEndsAt?, currentPeriodEnd?}}}` with `reason ∈ trial_ended|past_due|canceled|paused|none`. Ungated: `/v1/me`, `/v1/billing/*`, account connect, devices, preferences. Mail/calendar sync keeps running for lapsed users.

## Flows

### Checkout (web; desktop opens the same pages in the system browser)
1. `POST /v1/billing/checkout` (no body) → `409 already_subscribed` when a live Paddle subscription exists (status active/past_due/paused); otherwise the backend ensures the Paddle customer (`GET /customers?email=`, else `POST /customers`), stores `billing_customer_id`, creates a transaction (`POST /transactions` with the annual price, `customer_id`, `custom_data.user_id`) and returns `data.checkout.url`.
2. That URL is Paddle's **default payment link** = `<PUBLIC_WEB_URL>/checkout?_ptxn=<txn>`. The public `/checkout` page loads Paddle.js (`https://cdn.paddle.com/paddle/v2/paddle.js`), calls `Paddle.Environment.set("sandbox")` when `NEXT_PUBLIC_PADDLE_ENV=sandbox`, then `Paddle.Initialize({token: NEXT_PUBLIC_PADDLE_CLIENT_TOKEN, checkout:{settings:{successUrl:"<origin>/checkout/success", displayMode:"overlay"}}})`; Paddle.js opens the overlay for `_ptxn`.
3. After payment Paddle redirects to `/checkout/success`, which polls `GET /v1/billing/subscription` every 2 s for up to 60 s until the webhook has flipped the row to `active`.
4. No client-supplied URLs anywhere: Paddle decides the checkout page; the success URL is fixed by the web app.

### Portal
`POST /v1/billing/portal` (no body) → `{overviewUrl, cancelUrl, updatePaymentUrl}` from `POST /customers/{id}/portal-sessions`. Requires `billing_customer_id` (`400 no_billing_profile` otherwise); `cancelUrl`/`updatePaymentUrl` are empty without a subscription id. Links are temporary and never cached. Cancel and card updates happen in Paddle's portal and flow back by webhook (end-of-period cancel keeps `status=active` with `scheduled_change.action=cancel` → `cancel_at_period_end=true`).

### Webhook
`POST /v1/webhooks/paddle` (unauthenticated, 1 MB cap):
1. Verify `Paddle-Signature: ts=<unix>;h1=<hex>[;h1=<hex>]` — `HMAC-SHA256(PADDLE_WEBHOOK_SECRET, "<ts>:<raw body>")`, constant-time, any `h1` may match, 5-minute tolerance, empty secret refused. Failure → `401`, body never parsed.
2. Parse the envelope `{event_id, event_type, occurred_at, notification_id, data}`. Non-`subscription.*` types (`transaction.*`, …) are acknowledged with `200` and ignored.
3. One DB transaction: record `notification_id` in `billing_events` (duplicate → `200`, no change); resolve the user by the event's `customer_id` (the row owning that `billing_customer_id`); `custom_data.user_id` is used only when no row owns the customer and that user has no customer of their own, because Paddle.js lets a client set `customData`; a mismatch between the two, an unknown customer, or a user with no `users` row → warn (notification id, no emails) + `200`, nothing written; drop the event if `occurred_at < last_event_at` (delivery order is not guaranteed; replays carry a new `notification_id` and the same `occurred_at`, so they re-apply idempotently); upsert status (mapped), `current_period_end = current_billing_period.ends_at`, `cancel_at_period_end`, provider ids, `last_event_at = occurred_at`, `trial_ends_at = null`.
4. `200 {received:true}` well inside Paddle's 5 s limit.

Events subscribed: `subscription.created|activated|updated|canceled|past_due|paused|resumed|trialing`. Status mapping: `active→active`, `trialing→active` (we never configure Paddle trials), `past_due→past_due`, `paused→paused`, `canceled→canceled`, unknown → `none` (fail closed).

### Reconciliation
- Worker loop every `BILLING_RECONCILE_INTERVAL` (default 6h): rows with a `billing_subscription_id` where (status ∈ {active, past_due, paused} and `current_period_end < now - 1h`) or `last_event_at < now - 7d` are re-read with `GET /subscriptions/{id}` and applied as a synthetic event at `now` (always wins). Paddle errors are logged and skipped.
- Inline: `GET /v1/billing/subscription` reconciles the caller's row when it is `active` past `current_period_end`, at most once per 10 minutes per user (in-memory throttle, claimed before the Paddle call).

## Subscription states

`none → trialing → active → (past_due → active | paused | canceled)`

Expiry is computed (see above). `cancelAtPeriodEnd` keeps access until `currentPeriodEnd`; Paddle's dunning (by default 7 retries over 30 days) ends in `canceled` or `paused` per the dashboard setting.

## Backend implementation notes

- `internal/adapter/out/paddle` is a stdlib `net/http` JSON client (`https://sandbox-api.paddle.com` / `https://api.paddle.com` by `PADDLE_ENV`, `Authorization: Bearer PADDLE_API_KEY`, `{data, meta}` / `{error:{type,code,detail}}` envelopes): customers, transactions, portal sessions, subscription read and cancel (`POST /subscriptions/{id}/cancel`, used by account deletion), webhook verification.
- `internal/service/billing.go` owns the trial grant, the 409/400/502 rules, the webhook pipeline and reconciliation; `domain.Subscription` owns the entitlement rule.
- Schema: `subscriptions(billing_customer_id, billing_subscription_id, status check constraint, …)`, `billing_events(notification_id pk, event_id, event_type, occurred_at, received_at)` — migration `0027_paddle_billing.sql`.
- Config: `PADDLE_ENV`, `PADDLE_API_KEY`, `PADDLE_WEBHOOK_SECRET`, `PADDLE_PRICE_ID_ANNUAL`, `BILLING_RECONCILE_INTERVAL`; web `NEXT_PUBLIC_PADDLE_CLIENT_TOKEN`, `NEXT_PUBLIC_PADDLE_ENV`. With `SELF_HOSTED=false` the api and worker refuse to start if any Paddle key is empty.

Operator steps for sandbox and live are in [paddle-go-live.md](./paddle-go-live.md).
