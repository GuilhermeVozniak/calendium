# Paddle go-live checklist

Operator steps outside the codebase (docs/payments.md has the flows). Do each block in **sandbox** first, then repeat for **live**. The cross-vendor launch checklist (Google, Apple, Microsoft, email, hosting) lives in [release/go-live-external-checklist.md](./release/go-live-external-checklist.md); its section 3 is the short form of this page.

## 1. Website and default payment link
- Paddle → Checkout → Website approval: submit the public web origin (`PUBLIC_WEB_URL`, e.g. `https://app.calendium.app`). Localhost is allowed in sandbox only.
- Paddle → Checkout → Checkout settings → **Default payment link** = `<PUBLIC_WEB_URL>/checkout` (sandbox: `http://localhost:3000/checkout`). The page includes Paddle.js and opens the overlay from `_ptxn`.

## 2. Catalog
- Product **Calendium Annual**; price **USD 50.00 / year**, quantity 1. Copy the price id into `PADDLE_PRICE_ID_ANNUAL` (`pri_…`).
- Tax: choose tax-inclusive or tax-exclusive pricing (Checkout → Tax). The marketing page shows a static "$50/year"; the overlay shows the localized, tax-correct total.

## 3. API key and client token
- Developer tools → Authentication → API key with customers, transactions, subscriptions and portal-session scopes → `PADDLE_API_KEY`.
- Client-side token → `NEXT_PUBLIC_PADDLE_CLIENT_TOKEN` (web build arg). Set `NEXT_PUBLIC_PADDLE_ENV=sandbox|production` to match `PADDLE_ENV`.

## 4. Notification destination (webhook)
- Developer tools → Notifications → New destination: URL `<PUBLIC_API_URL>/v1/webhooks/paddle`, type webhook, version latest.
- Events: `subscription.created`, `subscription.activated`, `subscription.updated`, `subscription.canceled`, `subscription.past_due`, `subscription.paused`, `subscription.resumed`, `subscription.trialing`.
- Copy the destination's secret key into `PADDLE_WEBHOOK_SECRET` (`pdl_ntfset_…`). The API refuses to verify with an empty secret and refuses to boot in cloud mode without it.

## 5. Dunning and retention
- Subscriptions → Payment retries: keep the default (7 retries over 30 days) and choose the end action (`cancel` or `pause`). Both are handled: the app paywalls on `canceled`/`paused` with matching copy.

## 6. Environment
```dotenv
SELF_HOSTED=false
PADDLE_ENV=live                 # sandbox while testing
PADDLE_API_KEY=
PADDLE_WEBHOOK_SECRET=
PADDLE_PRICE_ID_ANNUAL=
BILLING_RECONCILE_INTERVAL=6h
NEXT_PUBLIC_PADDLE_CLIENT_TOKEN=
NEXT_PUBLIC_PADDLE_ENV=production   # sandbox while testing
```
Rebuild the web image after changing `NEXT_PUBLIC_*` (build-time inlined).

## 7. Verify
- `GET /v1/instance` shows `mode: cloud`, `features.billing: true`, `webUrl` = your web origin.
- A fresh account sees a 14-day trial in Settings → Billing.
- Subscribe with Paddle's sandbox test card (`4242 4242 4242 4242`, any future expiry, CVC `100`); `/checkout/success` flips to "You're all set" once the `subscription.activated` webhook lands; `subscriptions.status` is `active`.
- Portal → Cancel → the row shows `cancel_at_period_end = true` after `subscription.updated`.
- Paddle → Notifications → Logs shows `200` deliveries; `billing_events` has one row per notification.
