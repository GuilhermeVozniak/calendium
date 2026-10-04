# Billing on Paddle — design

Date: 2026-10-04. Status: approved in conversation, awaiting written review.
Program context: piece 1 of 6 in the production-readiness program (billing,
email/auth, account lifecycle, platform hardening, store readiness, external
checklist). Pieces 2, 4 and 5 are independent of this one; piece 3 (account
deletion) depends on the cancel operation defined here.

## Goal

Replace Stripe with Paddle Billing as the only biller for Calendium Cloud and,
in the same seam, fix the billing defects found in the 2026-10-02 audit:

1. New cloud users saw a broken inbox: no trial, every core call 402, no paywall.
2. The webhook was forgeable when the secret was empty, and the API started in
   cloud mode with no billing keys at all.
3. Access never expired on its own: entitlement trusted the stored status.
4. Trialing users could buy a second subscription.
5. Client-supplied success/cancel URLs were an open redirect.

Why Paddle: it is a merchant of record, so sales tax/VAT, invoices, receipts
and refunds are handled by Paddle. That closes the tax and receipts gaps
without per-country registrations.

## Decisions already made with the user

- One plan, Calendium Annual, USD 50/year. No seats, no monthly plan.
- The 14-day trial starts at signup, server-side, with no card. Paddle only
  appears at the paywall.
- Lapsed users (trial ended, canceled, paused, past-due beyond grace) get a
  hard paywall; mailbox sync continues for them.
- Spotify model stays: web and desktop pay through a browser checkout; mobile
  only reflects state and links to the web.
- Stripe is removed entirely (code, columns, docs). No data migration: there
  are no live subscribers.
- Overlay checkout, not inline.
- The Paddle account is approved; sandbox and live are available.

## Non-goals

- Seat/team billing, promo codes, multiple plans or currencies.
- Localized prices on the marketing page (the overlay shows the localized,
  tax-correct total; the marketing page keeps a static "$50/year").
- An in-app cancel endpoint (Paddle's portal cancels; piece 3 uses the port
  operation server-side).
- Refund/dispute handling beyond what subscription status webhooks convey.
- Email notifications (trial ending, payment failed): Paddle sends its own
  dunning emails; app-sent email arrives with piece 2.

## Paddle facts this design relies on (verified against developer.paddle.com)

- Base URLs: `https://sandbox-api.paddle.com`, `https://api.paddle.com`.
  Auth: `Authorization: Bearer <api key>`. Responses are `{data, meta}`;
  errors are `{error: {type, code, detail}}`.
- `POST /customers {email, name?}` creates a customer (`ctm_...`);
  `GET /customers?email=<e>` filters by exact email.
- `POST /transactions {items:[{price_id, quantity}], customer_id, custom_data}`
  returns `data.checkout.url` = `<default payment link>?_ptxn=<txn id>`.
  The default payment link is a page on an approved website that includes
  Paddle.js; Paddle.js auto-opens the checkout when `_ptxn` is present.
  Localhost is allowed in sandbox only.
- Paddle.js: `https://cdn.paddle.com/paddle/v2/paddle.js`,
  `Paddle.Environment.set("sandbox")`, `Paddle.Initialize({token, eventCallback})`,
  `settings.successUrl`, `checkout.completed` event.
- `POST /customers/{id}/portal-sessions {subscription_ids}` returns
  `urls.general.overview` and per-subscription `cancel_subscription`,
  `update_subscription_payment_method`, `view_subscription`. Links are
  temporary and must not be cached.
- `POST /subscriptions/{id}/cancel {effective_from: next_billing_period|immediately}`.
  End-of-period cancel keeps `status=active` and sets
  `scheduled_change {action: cancel, effective_at}`; immediate sets
  `status=canceled`, `canceled_at`.
- Subscription statuses: `active`, `trialing`, `past_due`, `paused`, `canceled`.
  `current_billing_period.{starts_at, ends_at}`, `next_billed_at`,
  `customer_id`, `custom_data`, `items[].price.id`.
- Dunning: by default failed renewals retry 7 times over 30 days, then the
  subscription is canceled (or paused, per dashboard setting).
- Webhooks: header `Paddle-Signature: ts=<unix>;h1=<hex>[;h1=<hex>]`,
  signature = HMAC-SHA256(secret, `<ts>:<raw body>`), hex, constant-time
  compare. Envelope `{event_id (evt_), event_type, occurred_at, notification_id (ntf_), data}`.
  Delivery order is NOT guaranteed; order by `occurred_at`. Replays reuse
  `event_id` with a new `notification_id`. Must respond 200 within 5 s.
  Live retries 60 times over 3 days.
- Events used: `subscription.created|activated|updated|canceled|past_due|paused|resumed|trialing`.
  `transaction.*` events are acknowledged and ignored.

## Section 1 — data model and port

### Migration (next free number; assign at execution)

- `subscriptions`: rename `stripe_customer_id` → `billing_customer_id`,
  `stripe_subscription_id` → `billing_subscription_id` (indexes renamed
  accordingly). Keep `status`, `plan`, `price_usd`, `current_period_end`,
  `cancel_at_period_end`, `trial_ends_at`, `updated_at`, `last_event_at`.
- Drop `stripe_events`; create `billing_events(notification_id text primary key,
  event_id text not null, event_type text not null, occurred_at timestamptz not null,
  received_at timestamptz not null default now())`.
- `status` check constraint (if any) updated to
  `trialing|active|past_due|paused|canceled|none`. `expired` is removed from
  the domain; expiry is computed from time.

### Domain

```go
type SubscriptionStatus string // trialing|active|past_due|paused|canceled|none

const (
    TrialLength    = 14 * 24 * time.Hour
    ActiveGrace    = 3 * 24 * time.Hour  // late renewal webhook
    PastDueGrace   = 7 * 24 * time.Hour  // dunning window we honour
)

type Subscription struct {
    UserID, Status, Plan, PriceUSD, CurrentPeriodEnd, CancelAtPeriodEnd,
    TrialEndsAt, BillingCustomerID, BillingSubscriptionID, LastEventAt // as today, renamed
}

// HasAccess is the single entitlement rule.
//   trialing  -> now < TrialEndsAt
//   active    -> CurrentPeriodEnd == nil || now < CurrentPeriodEnd + ActiveGrace
//   past_due  -> CurrentPeriodEnd != nil && now < CurrentPeriodEnd + PastDueGrace
//   paused, canceled, none -> false
// DenialReason(now) returns trial_ended|past_due|canceled|paused|none for 402 bodies.
```

### Trial at signup

`BillingService.GetSubscription` (used by the entitlement gate and the
subscription endpoint) upserts, for a user with no row, a `trialing` row with
`trial_ends_at = users.created_at + TrialLength`. The Go `users` row is
provisioned on the first authenticated call, which the web app makes right
after signup, so the clock is anchored to signup. The upsert is idempotent
(`ON CONFLICT DO NOTHING`), so concurrent first calls are safe. Self-host mode
never writes rows and returns the synthetic active subscription as today.

### Port (provider-neutral)

```go
type Payments interface {
    EnsureCustomer(ctx, user domain.User) (customerID string, err error)   // GET by email, else POST
    CreateCheckout(ctx, CheckoutParams{UserID, CustomerID string}) (url string, err error)
    CreatePortalSession(ctx, customerID, subscriptionID string) (PortalURLs, error)
    ParseWebhook(payload []byte, sigHeader string, now time.Time) (SubscriptionEvent, error)
    GetSubscription(ctx, subscriptionID string) (SubscriptionEvent, error)   // reconciliation
    CancelSubscription(ctx, subscriptionID string, immediately bool) error   // piece 3
}

type PortalURLs struct{ Overview, Cancel, UpdatePayment string }

type SubscriptionEvent struct {
    NotificationID, EventID, Type string
    OccurredAt                    time.Time
    CustomerID, SubscriptionID    string
    UserID                        string // custom_data.user_id when present
    Status                        domain.SubscriptionStatus
    CurrentPeriodEnd              *time.Time
    CancelAtPeriodEnd             bool   // scheduled_change.action == cancel
    Ignored                       bool   // transaction.* and unknown types
}

type BillingEventRepo interface { Record(ctx, ev SubscriptionEvent) (first bool, err error) }
type SubscriptionRepo interface {
    GetByUserID, GetByBillingCustomerID, Upsert, EnsureTrial(ctx, userID string, trialEndsAt time.Time) error,
    ListForReconciliation(ctx, now time.Time) ([]domain.Subscription, error)
}
```

Status mapping Paddle → domain: `active→active`, `trialing→active` (we never
configure Paddle trials; defensive), `past_due→past_due`, `paused→paused`,
`canceled→canceled`.

### Configuration

API/worker: `PADDLE_ENV` (`sandbox`|`live`, default `sandbox`),
`PADDLE_API_KEY`, `PADDLE_WEBHOOK_SECRET`, `PADDLE_PRICE_ID_ANNUAL`,
`BILLING_RECONCILE_INTERVAL` (default `6h`). Web (public, build-time):
`NEXT_PUBLIC_PADDLE_CLIENT_TOKEN`, `NEXT_PUBLIC_PADDLE_ENV`.

Startup rule: when `SELF_HOSTED=false`, `cmd/api` and `cmd/worker` exit with a
clear error if any of `PADDLE_API_KEY`, `PADDLE_WEBHOOK_SECRET`,
`PADDLE_PRICE_ID_ANNUAL` is empty. `features.billing` in `GET /v1/instance`
is therefore `true` exactly when cloud mode is on. All `STRIPE_*` names are
removed from config, compose and docs.

## Section 2 — flows

### Checkout

`POST /v1/billing/checkout` (authed, cloud only, no request body) → `{url}`.

1. Load the subscription (granting the trial row if missing).
2. If `BillingSubscriptionID != ""` and status ∈ {active, past_due, paused}
   → 409 `already_subscribed`. Clients open the portal instead.
3. `EnsureCustomer` (store `billing_customer_id` on success).
4. Create the transaction with the annual price, `customer_id`,
   `custom_data.user_id`. Return `data.checkout.url`.

No client-supplied URLs anywhere. Paddle API failures map to
502 `billing_unavailable`.

### Webhook

`POST /v1/webhooks/paddle` (unauthenticated, 1 MB body cap, replaces
`/v1/webhooks/stripe`).

1. Verify `Paddle-Signature` (any `h1` may match; tolerance 5 minutes — wider
   than Paddle's 5 s SDK default to absorb clock skew; retries carry fresh
   timestamps). Failure → 401, body not parsed. Empty configured secret is
   impossible by the startup rule, and `verify` additionally refuses an empty
   secret defensively.
2. Parse the envelope. Non-`subscription.*` types → `Ignored=true` → 200.
3. In one DB transaction: `Record(notification_id)`; if already seen → 200.
   Resolve user: `custom_data.user_id`, else `GetByBillingCustomerID`;
   unknown → log at warn, 200.
4. Ordering guard: if `OccurredAt < existing.LastEventAt` → 200, no change.
5. Upsert: status (mapped), `current_period_end = current_billing_period.ends_at`,
   `cancel_at_period_end = scheduled_change.action == "cancel"`,
   `billing_subscription_id`, `billing_customer_id`, `last_event_at = OccurredAt`,
   `trial_ends_at = nil` once a Paddle subscription exists.
6. Respond 200 `{received: true}`. Processing is a single short transaction,
   well inside Paddle's 5 s limit.

### Reconciliation

- Worker loop every `BILLING_RECONCILE_INTERVAL`: `ListForReconciliation`
  returns rows with a `billing_subscription_id` where
  (status ∈ {active, past_due, paused} and `current_period_end < now - 1h`)
  or `last_event_at < now - 7d`. For each, `GetSubscription` and apply it
  through the same upsert path as a synthetic event with `OccurredAt = now`
  (so it always wins). Paddle errors are logged and skipped.
- Inline: `GET /v1/billing/subscription` reconciles the caller's row when it
  is `active` past `current_period_end`, at most once per 10 minutes per user
  (in-memory throttle keyed by user id; best-effort).

### Portal

`POST /v1/billing/portal` (authed, cloud only, no body) →
`{overviewUrl, cancelUrl, updatePaymentUrl}`. Requires `billing_customer_id`;
otherwise 400 `no_billing_profile`. `cancelUrl`/`updatePaymentUrl` are empty
strings when there is no subscription id. Cancellation and payment updates
happen in Paddle's portal and flow back by webhook.

### Entitlement gate and 402 body

`entitlement.require` uses `HasAccess(now)`. Denials return
`402 {error:{code:"payment_required", message, details:{reason, trialEndsAt?, currentPeriodEnd?}}}`.
Ungated endpoints stay as today (`/v1/me`, `/v1/billing/*`, account connect,
devices, preferences). Mail/calendar sync in the worker is not gated.
Background automation keeps its existing skip-if-not-entitled behaviour.

### Instance payload

`GET /v1/instance` gains `webUrl` (= `PUBLIC_WEB_URL`), used by mobile and
desktop to build the pricing/billing links instead of hardcoded domains.

## Section 3 — clients, docs, verification

### Web (`apps/web`)

- `/checkout` — public page (no auth gate; desktop opens it in the system
  browser). Loads Paddle.js, calls `Paddle.Environment.set` when
  `NEXT_PUBLIC_PADDLE_ENV=sandbox`, `Paddle.Initialize({token, eventCallback})`
  with `settings.successUrl = <origin>/checkout/success`. Paddle.js opens the
  overlay from `_ptxn`. If the parameter is missing the page shows a short
  "nothing to pay" message. This URL is the Paddle default payment link.
- `/checkout/success` — public page: "Payment received. Activating…". When a
  session exists it polls `GET /v1/billing/subscription` every 2 s for up to
  60 s and then links to `/mail`; otherwise it tells the user to return to
  the app.
- `PaywallScreen` rendered by the `(app)` layout in place of the children when
  `features.billing` and the subscription denies access. Copy by reason:
  `trial_ended`/`none`/`canceled` → "Subscribe · $50/year" (checkout);
  `past_due`/`paused` → "Update payment method" (portal `updatePaymentUrl`)
  plus a secondary "Manage billing". Sign-out stays reachable.
- Trial banner inside the app during the last 3 trial days, dismissible per
  day, with a Subscribe action.
- Settings → Billing: status line (trial ends / renews / cancels on date),
  Subscribe (when no Paddle subscription) or Manage billing + Cancel (portal
  links). `?checkout=success` is no longer used; the success page handles it.
- Sign-in footer trial/price line and the pricing CTA render only when
  `features.billing` is true.
- Marketing pricing, terms, privacy and self-hosting docs pages: Stripe →
  Paddle (merchant of record; invoices, receipts and tax via Paddle).
- Demo mode mocks updated for the new client methods.

### Shared (`packages/shared`)

- `SubscriptionStatus` adds `paused`, drops `expired`.
- `ApiRequestError` exposes `details` (typed `PaymentRequiredDetails` for 402).
- `createCheckoutSession()` takes no arguments; `createBillingPortalSession()`
  returns `{overviewUrl, cancelUrl, updatePaymentUrl}`.
- `InstanceInfo.webUrl`.

### Desktop and mobile

- Both render a paywall screen from the 402 reason with no purchase UI.
  Desktop: "Open billing in your browser" → `<webUrl>/settings`. Mobile:
  "Manage on the web" link → `<webUrl>/pricing`. Hardcoded `calendium.app`
  billing links are removed.

### Backend cleanup

- Delete `internal/adapter/out/stripeapi`; add `internal/adapter/out/paddle`
  (stdlib `net/http`, injectable `http.Client`, base URL by `PADDLE_ENV`).
- Remove Stripe wiring in `cmd/api`/`cmd/worker`, config, compose, `.env.example`.
- Docs: rewrite `docs/payments.md`, update `docs/pricing-model.md`,
  `docs/architecture.md` (REST table), `docs/self-hosting/configuration.md`,
  `README.md`; add `docs/paddle-go-live.md` (domain approval, live keys and
  price id, notification destination with the event list above, default
  payment link, tax/currency settings, dunning end action).

### Verification

- Backend: adapter tests against an `httptest` fake Paddle (signature
  accept/reject incl. multiple `h1`, tolerance, empty secret; envelope
  parsing; status mapping; customer lookup/create; transaction → checkout url;
  portal; get subscription; cancel). Service tests: trial grant anchored to
  `created_at`, entitlement time matrix for every status, out-of-order drop,
  replay idempotency by notification id, already-subscribed guard,
  reconciliation selection and apply, inline reconcile throttle. Handler
  tests: webhook 401/200, checkout 409/502, 402 body shape. Config tests:
  cloud mode refuses to start without keys. Postgres tests cover the
  migration and new repo methods.
- Web: unit tests for `PaywallScreen` per reason, Settings billing states,
  `/checkout` initialisation (Paddle.js mocked), success-page polling.
  Playwright (demo mode) covers paywall rendering.
- Sandbox run (last task of the plan): sandbox keys in `.env`, sandbox default
  payment link = `http://localhost:3000/checkout`, a temporary tunnel so the
  sandbox notification destination can reach `localhost:8080`, one checkout
  with Paddle's test card, the subscription observed flipping
  `trialing → active`, then a portal cancel observed as
  `cancel_at_period_end=true`. Secrets are entered by the user, never pasted
  into chat.

## Open items for the operator (not code)

- Set the sandbox default payment link and, before go-live, the live one on
  the approved production domain.
- Create the live product/price and the live notification destination.
- Choose tax-inclusive vs exclusive pricing and the dunning end action in the
  Paddle dashboard.
