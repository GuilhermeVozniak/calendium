# Payments — $50/year via Stripe (Spotify model)

One plan: **Calendium Annual — $50/year**, 14-day free trial, all platforms unlocked by the one subscription. **No in-app purchases anywhere.** Stripe is the only biller.

## Why the Spotify model

Like Spotify, purchases never go through Apple/Google IAP: web and desktop run the regular Stripe Checkout flow, and the mobile apps never show a purchase UI — they only reflect subscription state fetched from the backend (`GET /v1/billing/subscription`).

## Flows

### Web (and desktop — desktop opens these pages in the default browser)
1. Signed-in user hits a paywall (`trialing` ended or `none`) → pricing screen.
2. `POST /v1/billing/checkout` → backend creates a Stripe Checkout Session (`mode=subscription`, `price=STRIPE_PRICE_ID_ANNUAL`, customer bound to the Calendium user id via `client_reference_id` + `metadata.user_id`, `subscription_data.trial_period_days=14` for first-time subscribers).
3. Browser redirects to `session.url` (checkout.stripe.com) → pays → redirected to `successUrl`.
4. Webhooks drive state (never trust the redirect): `checkout.session.completed`, `customer.subscription.created/updated/deleted`, `invoice.paid`, `invoice.payment_failed` → upsert `subscriptions` row.
5. Manage / cancel / update card: `POST /v1/billing/portal` → Stripe Billing Portal.

### Mobile (iOS / Android)
- Reads subscription state; when unsubscribed shows: “Calendium Pro is managed on the web — calendium.app/pricing” (plain link, **no** purchase button, App Store 3.1.3(a)-style reader treatment).
- After subscribing on the web, the app unlocks on next state fetch / push.

### Desktop (Wails)
- Identical to web, but `POST /v1/billing/checkout` is called from the app and the returned URL is opened with the system browser (`wails runtime.BrowserOpenURL`). The app polls `GET /v1/billing/subscription` until the webhook lands, then unlocks — the “Spotify desktop” experience.

## Subscription states

`none → trialing → active → (past_due → active | canceled) → expired`

Grace: `past_due` keeps access for 7 days while Stripe retries. `cancelAtPeriodEnd` keeps access until `currentPeriodEnd`.

## Backend implementation notes

- `internal/adapter/out/stripeapi` is a raw stdlib REST client (form-encoded POSTs to `api.stripe.com/v1/*`): create customer, create checkout session, create portal session, fetch subscription.
- Webhook verification: `Stripe-Signature` header → HMAC-SHA256 over `t.payload` with `STRIPE_WEBHOOK_SECRET`, constant-time compare, 5-minute tolerance.
- Idempotency: webhook events recorded in `stripe_events` (unique event id) before processing.
