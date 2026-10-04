# Billing on Paddle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Stripe with Paddle Billing as Calendium Cloud's only biller and, in the same seam, fix the five 2026-10-02 audit defects: a server-side 14-day trial granted at signup (no card), a webhook that refuses to verify with an empty secret and an API/worker that refuse to boot in cloud mode without billing keys, access that expires from time rather than from stored status, a 409 guard against double subscriptions, and no client-supplied redirect URLs anywhere.

**Architecture:** Hexagonal Go backend (`domain ← port ← service`, adapters at the edges). `domain.Subscription` owns the single entitlement rule (`HasAccess`/`DenialReason`). A provider-neutral `port.Payments` is implemented by a new stdlib `adapter/out/paddle` package (REST + `Paddle-Signature` HMAC); the Stripe adapter, columns, config and docs are deleted. `service.BillingService` grants the trial on first read, runs checkout/portal/webhook/reconciliation, and maps gateway failures to `502 billing_unavailable`. `httpapi` exposes `POST /v1/billing/checkout`, `POST /v1/billing/portal`, `GET /v1/billing/subscription`, `POST /v1/webhooks/paddle`, a typed `402` body, and `instance.webUrl`. The shared TS contract gains `paused`, typed 402 details, no-arg checkout, portal URL triple and `webUrl`; the web app adds public `/checkout` (Paddle.js overlay) and `/checkout/success` pages, a layout-level `BillingGate`/`PaywallScreen`, a last-3-days trial banner and a rewritten Settings billing section; mobile and desktop render read-only paywall screens linking to `instance.webUrl`.

**Tech Stack:** Go 1.26 stdlib (`net/http`, `crypto/hmac`, `database/sql` + pgx driver, `log/slog`), SQL migrations in `backend/migrations`, testcontainers Postgres tests, Next.js 15 + React 19 + TanStack Query + Paddle.js v2, Expo/React Native (Jest), Wails/Vite React (Vitest), `packages/shared` TS contract (Vitest), Playwright demo-mode e2e, Biome, golangci-lint.

**Spec:** docs/superpowers/specs/2026-10-04-paddle-billing-design.md

## Global Constraints

- One plan: `Calendium Annual`, `plan = "annual"`, `price_usd = 50`; no seats, no monthly plan, no promo codes.
- `domain.TrialLength = 14 * 24 * time.Hour`; the trial row is `trialing` with `trial_ends_at = users.created_at + TrialLength`, granted by `BillingService.GetSubscription` via idempotent `SubscriptionRepo.EnsureTrial` (`ON CONFLICT (user_id) DO NOTHING`); self-host mode never writes rows.
- `domain.ActiveGrace = 3 * 24 * time.Hour`; `domain.PastDueGrace = 7 * 24 * time.Hour`.
- `HasAccess(now)`: `trialing → now < TrialEndsAt`; `active → CurrentPeriodEnd == nil || now < CurrentPeriodEnd + ActiveGrace`; `past_due → CurrentPeriodEnd != nil && now < CurrentPeriodEnd + PastDueGrace`; `paused`, `canceled`, `none` → false.
- Status set (Go, SQL check constraint, TS): `trialing | active | past_due | paused | canceled | none`; `expired` is removed everywhere.
- Paddle → domain status mapping: `active→active`, `trialing→active`, `past_due→past_due`, `paused→paused`, `canceled→canceled`, anything else → `none`.
- Webhook signature: header `Paddle-Signature: ts=<unix>;h1=<hex>[;h1=<hex>]`, `HMAC-SHA256(secret, "<ts>:<raw body>")` hex, `hmac.Equal`, any `h1` may match, tolerance `5 * time.Minute` in both directions, empty configured secret → verification refused.
- Webhook idempotency key is `notification_id` (`billing_events.notification_id` primary key); ordering guard drops an event whose `occurred_at` is strictly before the row's `last_event_at`; a Paddle subscription existing clears `trial_ends_at`.
- Reconciliation: worker loop every `BILLING_RECONCILE_INTERVAL` (default `6h`); `ListForReconciliation` = rows with `billing_subscription_id` where (`status ∈ {active, past_due, paused}` and `current_period_end < now - 1h`) or `last_event_at < now - 7d`; inline reconcile in `GET /v1/billing/subscription` when `active` past `current_period_end`, throttled to once per `10 * time.Minute` per user (in-memory).
- Backend env: `PADDLE_ENV` (`sandbox|live`, default `sandbox`), `PADDLE_API_KEY`, `PADDLE_WEBHOOK_SECRET`, `PADDLE_PRICE_ID_ANNUAL`, `BILLING_RECONCILE_INTERVAL` (default `6h`). Web (build-time, public): `NEXT_PUBLIC_PADDLE_CLIENT_TOKEN`, `NEXT_PUBLIC_PADDLE_ENV`.
- Startup rule: with `SELF_HOSTED=false`, both `cmd/api` and `cmd/worker` exit with a clear error if any of `PADDLE_API_KEY`, `PADDLE_WEBHOOK_SECRET`, `PADDLE_PRICE_ID_ANNUAL` is empty; `features.billing == !SELF_HOSTED`.
- Base URLs: `https://sandbox-api.paddle.com` / `https://api.paddle.com`; auth `Authorization: Bearer <PADDLE_API_KEY>`; success bodies `{data, meta}`, error bodies `{error:{type,code,detail}}`.
- Endpoints: `POST /v1/billing/checkout` (authed, no request body) → `{url}`; `POST /v1/billing/portal` (authed, no body) → `{overviewUrl, cancelUrl, updatePaymentUrl}`; `GET /v1/billing/subscription`; `POST /v1/webhooks/paddle` (unauthenticated, 1 MB body cap, `200 {received:true}`); `/v1/webhooks/stripe` is removed.
- Error codes: `409 already_subscribed` (checkout while `billing_subscription_id != ""` and status ∈ {active, past_due, paused}); `400 no_billing_profile` (portal without `billing_customer_id`); `502 billing_unavailable` (any Paddle API failure); `401 unauthorized` (bad webhook signature); `501 self_hosted` (billing endpoints in self-host mode).
- 402 body: `{"error":{"code":"payment_required","message":"An active subscription is required.","details":{"reason":"trial_ended|past_due|canceled|paused|none","trialEndsAt"?:RFC3339,"currentPeriodEnd"?:RFC3339}}}`.
- No client-supplied URLs anywhere: checkout URL comes from Paddle (`data.checkout.url`), portal URLs come from Paddle, `successUrl` is fixed to `<origin>/checkout/success` on the web `/checkout` page.
- `GET /v1/instance` gains `webUrl` (= `PUBLIC_WEB_URL`, trailing slash trimmed); mobile builds `<webUrl>/pricing`, desktop builds `<webUrl>/settings`; no hardcoded `calendium.app` billing links remain.
- Hexagonal rule: `domain` imports only stdlib; `port` imports `domain`; `service` imports `domain` + `port`; adapters import `port`; nothing in `domain`/`port`/`service` imports an adapter. No frameworks; `pgx` only as the `database/sql` driver.
- When this plan is done, `grep -rniE 'stripe' --exclude-dir=node_modules --exclude-dir=.git --exclude-dir=.next --exclude=bun.lock . | grep -v '^./docs/superpowers/'` prints nothing: no Stripe code, column, config, compose, sample-data or doc reference remains (only the spec and this plan under `docs/superpowers/` may mention it).

## Review Focus

- A Paddle replay that reuses `event_id` with a fresh `notification_id` and the SAME `occurred_at` as the row's `last_event_at` must be re-applied (idempotent), not dropped: the ordering guard is strict `<`. (Test added to Task 10.)
- `subscription.canceled` payloads carry `custom_data: null` and `current_billing_period: null`; the parser must not panic and must yield `CurrentPeriodEnd == nil` so a canceled user is paywalled with reason `canceled`, not kept alive by a stale period. (Test added to Task 7.)
- Clock skew: a webhook whose `ts` is 4 minutes in the future must verify, 6 minutes in either direction must be rejected; only the first `h1` of a rotated header may be stale. (Test added to Task 7.)
- During a Paddle outage the inline reconcile must record the throttle slot BEFORE calling Paddle, so a lapsed `active` user refreshing the inbox cannot hit Paddle more than once per 10 minutes even while every call fails. (Test added to Task 11.)
- The web `BillingGate` must fail open when the subscription request itself fails (network/5xx): an unreachable billing API must never lock a paying user out of their mail; only a successfully fetched subscription that denies access shows the paywall. (Test added to Task 15.)

## Execution tracks

Strict file ownership: a file appears in exactly one track; agents never edit another track's files. The repo will not compile as a whole between Task 2 and Task 14 (the port boundary changes shape); each track verifies its own packages with scoped `go test ./internal/<pkg>/...` commands until Task 14 restores `go build ./...`.

- **Track A — backend foundation (Tasks 1–4, sequential).** Owns `backend/internal/domain/{subscription.go,errors.go,domain_test.go}`, `backend/internal/port/{driven.go,driving.go,billing_port_test.go}`, `backend/internal/adapter/out/stripeapi/*` (deletion only), `backend/migrations/0027_paddle_billing.sql`, `backend/internal/adapter/out/postgres/{user.go,store.go,postgres_test.go,user_billing_test.go,misc_repo_test.go}`, `backend/internal/config/{config.go,config_test.go}`. Starts immediately.
- **Track B — Paddle adapter (Tasks 5–7, sequential).** Owns `backend/internal/adapter/out/paddle/*` (new). Starts after Task 2 is committed (it implements `port.Payments`).
- **Track C — service, HTTP, composition (Tasks 8–13, sequential).** Owns `backend/internal/service/{billing.go,billing_test.go,fakes_test.go,selfhost_test.go,service.go}`, `backend/internal/adapter/in/httpapi/{billing.go,billing_handlers_test.go,codec.go,codec_billing_test.go,httpapi.go,instance.go,instance_test.go,harness_test.go,middleware_test.go}`, `backend/cmd/api/main.go`, `backend/cmd/worker/main.go`. Tasks 8–12 start after Task 4; Task 13 also needs Track B complete.
- **Track D — shared contract + web (Tasks 14–18, sequential).** Owns `packages/shared/src/{types.ts,client.ts,billing.ts,billing.test.ts,index.ts,client.test.ts}`, every file under `apps/web/` listed in those tasks, `apps/web/Dockerfile`. Starts immediately (the contract is fixed by this plan, not by the Go code).
- **Track E — mobile, desktop, docs, env (Tasks 19–21).** Owns `apps/mobile/*`, `apps/desktop/frontend/*`, `docs/*` (except `docs/superpowers/`), `README.md`, `.env.example`, `docker-compose.yml`. Tasks 19 and 20 start after Task 14 is committed (they consume the shared types); Task 21 can start immediately but its final Stripe sweep runs last. Tasks 19, 20, 21 touch disjoint files and may be split across agents.
- **Final (Tasks 22–23, sequential, after every track has merged).** Sandbox verification runbook, then the full-suite gate.

Dependency order: A → (B ∥ C-service/handlers ∥ D) → C-composition (Task 13 needs B) → E (19, 20 need Task 14) → 21's Stripe sweep → 22 → 23.

---

### Task 1: Domain — statuses, grace windows, `HasAccess`/`DenialReason`, typed 402 error

**Files:**
- Modify: `backend/internal/domain/subscription.go` (whole file)
- Modify: `backend/internal/domain/errors.go` (add three sentinels; retarget `ErrSelfHosted` comment)
- Modify: `backend/internal/domain/domain_test.go` (replace the `HasAccess` table test that contains the case `expired has no access`; add two tests)

**Interfaces:**
- Consumes: `domain.ErrPaymentRequired` (existing).
- Produces: `domain.SubscriptionStatus` consts `SubscriptionTrialing|SubscriptionActive|SubscriptionPastDue|SubscriptionPaused|SubscriptionCanceled|SubscriptionNone`; consts `PlanAnnual`, `PriceUSDAnnual`, `TrialLength`, `ActiveGrace`, `PastDueGrace`; `domain.Subscription{UserID, Status, Plan, PriceUSD, CurrentPeriodEnd *time.Time, CancelAtPeriodEnd bool, TrialEndsAt *time.Time, BillingCustomerID, BillingSubscriptionID string, LastEventAt *time.Time}`; `func (s Subscription) HasAccess(now time.Time) bool`; `type DenialReason string` with `DenialTrialEnded|DenialPastDue|DenialCanceled|DenialPaused|DenialNone`; `func (s Subscription) DenialReason(now time.Time) DenialReason` (`""` when access is granted); `type PaymentRequiredError struct{Reason DenialReason; TrialEndsAt, CurrentPeriodEnd *time.Time}` with `Error()`, `Unwrap() error` (= `ErrPaymentRequired`); `func NewPaymentRequiredError(s Subscription, now time.Time) *PaymentRequiredError`; sentinels `ErrAlreadySubscribed`, `ErrNoBillingProfile`, `ErrBillingUnavailable`.

- [ ] **Step 1: Write the failing domain tests.** Delete the existing `HasAccess` table test function in `backend/internal/domain/domain_test.go` (the one whose table contains `"expired has no access"`), and add:

```go
func TestSubscriptionAccessMatrix(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	ptr := func(v time.Time) *time.Time { return &v }

	tests := []struct {
		name       string
		sub        Subscription
		wantAccess bool
		wantReason DenialReason
	}{
		{"trialing before trial end", Subscription{Status: SubscriptionTrialing, TrialEndsAt: ptr(future)}, true, ""},
		{"trialing exactly at trial end", Subscription{Status: SubscriptionTrialing, TrialEndsAt: ptr(now)}, false, DenialTrialEnded},
		{"trialing without a trial end", Subscription{Status: SubscriptionTrialing}, false, DenialTrialEnded},
		{"active with nil period end", Subscription{Status: SubscriptionActive}, true, ""},
		{"active inside period", Subscription{Status: SubscriptionActive, CurrentPeriodEnd: ptr(future)}, true, ""},
		{"active inside 3d grace", Subscription{Status: SubscriptionActive, CurrentPeriodEnd: ptr(now.Add(-ActiveGrace + time.Minute))}, true, ""},
		{"active exactly at grace end", Subscription{Status: SubscriptionActive, CurrentPeriodEnd: ptr(now.Add(-ActiveGrace))}, false, DenialPastDue},
		{"past_due inside 7d grace", Subscription{Status: SubscriptionPastDue, CurrentPeriodEnd: ptr(now.Add(-PastDueGrace + time.Minute))}, true, ""},
		{"past_due exactly at grace end", Subscription{Status: SubscriptionPastDue, CurrentPeriodEnd: ptr(now.Add(-PastDueGrace))}, false, DenialPastDue},
		{"past_due without period end", Subscription{Status: SubscriptionPastDue}, false, DenialPastDue},
		{"paused with future period end", Subscription{Status: SubscriptionPaused, CurrentPeriodEnd: ptr(future)}, false, DenialPaused},
		{"canceled with future period end", Subscription{Status: SubscriptionCanceled, CurrentPeriodEnd: ptr(future)}, false, DenialCanceled},
		{"none", Subscription{Status: SubscriptionNone}, false, DenialNone},
		{"unknown status fails closed", Subscription{Status: "weird"}, false, DenialNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.sub.HasAccess(now); got != tt.wantAccess {
				t.Fatalf("HasAccess = %v, want %v", got, tt.wantAccess)
			}
			if got := tt.sub.DenialReason(now); got != tt.wantReason {
				t.Fatalf("DenialReason = %q, want %q", got, tt.wantReason)
			}
		})
	}
}

func TestSubscriptionConstants(t *testing.T) {
	if TrialLength != 14*24*time.Hour {
		t.Fatalf("TrialLength = %v, want 14d", TrialLength)
	}
	if ActiveGrace != 3*24*time.Hour {
		t.Fatalf("ActiveGrace = %v, want 3d", ActiveGrace)
	}
	if PastDueGrace != 7*24*time.Hour {
		t.Fatalf("PastDueGrace = %v, want 7d", PastDueGrace)
	}
	if PlanAnnual != "annual" || PriceUSDAnnual != 50 {
		t.Fatalf("plan/price = %q/%d, want annual/50", PlanAnnual, PriceUSDAnnual)
	}
}

func TestPaymentRequiredError(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	trialEnd := now.Add(-time.Hour)
	sub := Subscription{Status: SubscriptionTrialing, TrialEndsAt: &trialEnd}

	err := NewPaymentRequiredError(sub, now)
	if !errors.Is(err, ErrPaymentRequired) {
		t.Fatalf("errors.Is(ErrPaymentRequired) = false for %v", err)
	}
	var pr *PaymentRequiredError
	if !errors.As(err, &pr) {
		t.Fatalf("errors.As(*PaymentRequiredError) = false")
	}
	if pr.Reason != DenialTrialEnded {
		t.Fatalf("Reason = %q, want trial_ended", pr.Reason)
	}
	if pr.TrialEndsAt == nil || !pr.TrialEndsAt.Equal(trialEnd) {
		t.Fatalf("TrialEndsAt = %v, want %v", pr.TrialEndsAt, trialEnd)
	}
	if pr.Error() != "payment required: trial_ended" {
		t.Fatalf("Error() = %q", pr.Error())
	}

	wrapped := fmt.Errorf("gate: %w", err)
	if !errors.As(wrapped, &pr) {
		t.Fatalf("wrapped error lost the typed PaymentRequiredError")
	}
}
```

Add `"errors"` and `"fmt"` to the test file's imports if absent.

- [ ] **Step 2: Run the tests and confirm they fail to compile.** `cd backend && go test ./internal/domain/ -run 'TestSubscriptionAccessMatrix|TestSubscriptionConstants|TestPaymentRequiredError'` — expected: `undefined: DenialReason`, `undefined: SubscriptionPaused`, `undefined: TrialLength`, `undefined: NewPaymentRequiredError`.

- [ ] **Step 3: Replace `backend/internal/domain/subscription.go` with:**

```go
package domain

import (
	"fmt"
	"time"
)

// SubscriptionStatus is the Paddle-mirrored lifecycle (docs/payments.md):
// none → trialing → active → (past_due → active | paused | canceled).
// Expiry is never stored: HasAccess computes it from time, so a stale mirror
// can never keep a lapsed user entitled.
type SubscriptionStatus string

const (
	SubscriptionTrialing SubscriptionStatus = "trialing"
	SubscriptionActive   SubscriptionStatus = "active"
	SubscriptionPastDue  SubscriptionStatus = "past_due"
	SubscriptionPaused   SubscriptionStatus = "paused"
	SubscriptionCanceled SubscriptionStatus = "canceled"
	SubscriptionNone     SubscriptionStatus = "none"
)

const (
	// PlanAnnual is the only plan: Calendium Annual, $50/year.
	PlanAnnual = "annual"
	// PriceUSDAnnual is the fixed yearly price in whole dollars.
	PriceUSDAnnual = 50
	// TrialLength is the card-free trial granted server-side at signup.
	TrialLength = 14 * 24 * time.Hour
	// ActiveGrace keeps an active subscription entitled past its period end
	// while the renewal webhook is late.
	ActiveGrace = 3 * 24 * time.Hour
	// PastDueGrace is the dunning window honoured for a failed renewal.
	PastDueGrace = 7 * 24 * time.Hour
)

// Subscription is the single $50/yr annual plan (Spotify model — the web
// checkout is the only purchase surface; mobile apps only reflect state).
type Subscription struct {
	UserID            string             `json:"-"`
	Status            SubscriptionStatus `json:"status"`
	Plan              string             `json:"plan"`     // always PlanAnnual
	PriceUSD          int                `json:"priceUsd"` // always PriceUSDAnnual
	CurrentPeriodEnd  *time.Time         `json:"currentPeriodEnd"`
	CancelAtPeriodEnd bool               `json:"cancelAtPeriodEnd"`
	TrialEndsAt       *time.Time         `json:"trialEndsAt"`
	// BillingCustomerID / BillingSubscriptionID are the provider's ids
	// (Paddle ctm_/sub_); internal state, never part of the API payload.
	BillingCustomerID     string `json:"-"`
	BillingSubscriptionID string `json:"-"`
	// LastEventAt is the provider `occurred_at` of the most recent
	// subscription.* event applied to this mirror; it orders lifecycle events
	// so an out-of-order or re-delivered older one is dropped.
	LastEventAt *time.Time `json:"-"`
}

// DenialReason names why a subscription does not grant access; it is the
// `details.reason` of 402 responses.
type DenialReason string

const (
	DenialTrialEnded DenialReason = "trial_ended"
	DenialPastDue    DenialReason = "past_due"
	DenialCanceled   DenialReason = "canceled"
	DenialPaused     DenialReason = "paused"
	DenialNone       DenialReason = "none"
)

// DenialReason is the single entitlement rule. It returns "" when the
// subscription grants access at `now`, otherwise the reason it does not:
//
//	trialing  -> now < TrialEndsAt
//	active    -> CurrentPeriodEnd == nil || now < CurrentPeriodEnd + ActiveGrace
//	past_due  -> CurrentPeriodEnd != nil && now < CurrentPeriodEnd + PastDueGrace
//	paused, canceled, none (and anything unknown) -> denied
func (s Subscription) DenialReason(now time.Time) DenialReason {
	switch s.Status {
	case SubscriptionTrialing:
		if s.TrialEndsAt != nil && now.Before(*s.TrialEndsAt) {
			return ""
		}
		return DenialTrialEnded
	case SubscriptionActive:
		if s.CurrentPeriodEnd == nil || now.Before(s.CurrentPeriodEnd.Add(ActiveGrace)) {
			return ""
		}
		return DenialPastDue
	case SubscriptionPastDue:
		if s.CurrentPeriodEnd != nil && now.Before(s.CurrentPeriodEnd.Add(PastDueGrace)) {
			return ""
		}
		return DenialPastDue
	case SubscriptionPaused:
		return DenialPaused
	case SubscriptionCanceled:
		return DenialCanceled
	default:
		return DenialNone
	}
}

// HasAccess reports whether the subscription currently unlocks the product.
func (s Subscription) HasAccess(now time.Time) bool {
	return s.DenialReason(now) == ""
}

// PaymentRequiredError is the typed form of ErrPaymentRequired carrying the
// 402 `details` body. errors.Is(err, ErrPaymentRequired) holds for it.
type PaymentRequiredError struct {
	Reason           DenialReason
	TrialEndsAt      *time.Time
	CurrentPeriodEnd *time.Time
}

// NewPaymentRequiredError builds the denial for s at now. Call it only when
// !s.HasAccess(now).
func NewPaymentRequiredError(s Subscription, now time.Time) *PaymentRequiredError {
	return &PaymentRequiredError{
		Reason:           s.DenialReason(now),
		TrialEndsAt:      s.TrialEndsAt,
		CurrentPeriodEnd: s.CurrentPeriodEnd,
	}
}

func (e *PaymentRequiredError) Error() string {
	return fmt.Sprintf("payment required: %s", e.Reason)
}

// Unwrap lets errors.Is(err, ErrPaymentRequired) match the typed error.
func (e *PaymentRequiredError) Unwrap() error { return ErrPaymentRequired }
```

- [ ] **Step 4: Add the sentinels in `backend/internal/domain/errors.go`.** Inside the `var (...)` block, after `ErrRateLimited`, add:

```go
	// ErrAlreadySubscribed marks a checkout attempt by a user who already has
	// a live provider subscription (active, past_due or paused). The HTTP
	// adapter maps it to 409 "already_subscribed"; clients open the portal.
	ErrAlreadySubscribed = errors.New("already subscribed")
	// ErrNoBillingProfile marks a portal request for a user with no provider
	// customer yet. Mapped to 400 "no_billing_profile".
	ErrNoBillingProfile = errors.New("no billing profile")
	// ErrBillingUnavailable wraps any failure talking to the payments
	// provider. Mapped to 502 "billing_unavailable".
	ErrBillingUnavailable = errors.New("billing unavailable")
```

Also change the `ErrSelfHosted` comment from `(the Stripe billing endpoints)` to `(the billing endpoints)`, and extend the mapping comment block at the top of the file with three lines: `ErrAlreadySubscribed → 409`, `ErrNoBillingProfile → 400`, `ErrBillingUnavailable → 502`.

- [ ] **Step 5: Run the domain suite.** `cd backend && go test ./internal/domain/...` — expected: `ok`.

- [ ] **Step 6: Commit.**

```bash
git add backend/internal/domain
git commit -m "feat(domain): time-based subscription entitlement with paused status and typed 402 denial" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 2: Ports — provider-neutral `Payments`, `SubscriptionEvent`, `BillingEventRepo`, extended `SubscriptionRepo`, new `BillingService`; delete `stripeapi`

**Files:**
- Create: `backend/internal/port/billing_port_test.go`
- Modify: `backend/internal/port/driven.go` (the `SubscriptionRepo` block ~L53–58, the `StripeEventRepo` block ~L389–394, the `CheckoutParams`/`WebhookEvent`/`Payments` block ~L701–739, the gateway header comment ~L591)
- Modify: `backend/internal/port/driving.go` (the `BillingService` block ~L28–41)
- Delete: `backend/internal/adapter/out/stripeapi/client.go`, `payments.go`, `payments_test.go`, `webhook.go`, `webhook_test.go`

**Interfaces:**
- Consumes: Task 1 domain types.
- Produces: `port.SubscriptionRepo{GetByUserID; GetByBillingCustomerID(ctx, customerID string); Upsert; EnsureTrial(ctx, userID string, trialEndsAt time.Time) error; ListForReconciliation(ctx, now time.Time) ([]domain.Subscription, error)}`; `port.BillingEventRepo{Record(ctx, ev SubscriptionEvent) (first bool, err error)}`; `port.CheckoutParams{UserID, CustomerID string}`; `port.PortalURLs{Overview, Cancel, UpdatePayment string}`; `port.SubscriptionEvent{NotificationID, EventID, Type string; OccurredAt time.Time; CustomerID, SubscriptionID, UserID string; Status domain.SubscriptionStatus; CurrentPeriodEnd *time.Time; CancelAtPeriodEnd, Ignored bool}`; `port.Payments{EnsureCustomer(ctx, user domain.User) (string, error); CreateCheckout(ctx, p CheckoutParams) (string, error); CreatePortalSession(ctx, customerID, subscriptionID string) (PortalURLs, error); ParseWebhook(payload []byte, sigHeader string, now time.Time) (SubscriptionEvent, error); GetSubscription(ctx, subscriptionID string) (SubscriptionEvent, error); CancelSubscription(ctx, subscriptionID string, immediately bool) error}`; `port.BillingService{GetSubscription(ctx, userID) (domain.Subscription, error); CreateCheckout(ctx, userID string) (url string, err error); CreatePortalSession(ctx, userID string) (PortalURLs, error); HandleWebhook(ctx, payload []byte, sigHeader string) error; ReconcileSubscriptions(ctx) error; RequireActive(ctx, userID string) error}`.

- [ ] **Step 1: Write the compile-time contract test** at `backend/internal/port/billing_port_test.go`:

```go
package port

import (
	"context"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// The port package has no behaviour; this test pins the exact method sets
// the service and adapters are written against so a drift fails here first.
type stubPayments struct{}

func (stubPayments) EnsureCustomer(context.Context, domain.User) (string, error) { return "", nil }
func (stubPayments) CreateCheckout(context.Context, CheckoutParams) (string, error) {
	return "", nil
}
func (stubPayments) CreatePortalSession(context.Context, string, string) (PortalURLs, error) {
	return PortalURLs{}, nil
}
func (stubPayments) ParseWebhook([]byte, string, time.Time) (SubscriptionEvent, error) {
	return SubscriptionEvent{}, nil
}
func (stubPayments) GetSubscription(context.Context, string) (SubscriptionEvent, error) {
	return SubscriptionEvent{}, nil
}
func (stubPayments) CancelSubscription(context.Context, string, bool) error { return nil }

type stubSubs struct{}

func (stubSubs) GetByUserID(context.Context, string) (domain.Subscription, error) {
	return domain.Subscription{}, nil
}
func (stubSubs) GetByBillingCustomerID(context.Context, string) (domain.Subscription, error) {
	return domain.Subscription{}, nil
}
func (stubSubs) Upsert(context.Context, domain.Subscription) error            { return nil }
func (stubSubs) EnsureTrial(context.Context, string, time.Time) error          { return nil }
func (stubSubs) ListForReconciliation(context.Context, time.Time) ([]domain.Subscription, error) {
	return nil, nil
}

type stubEvents struct{}

func (stubEvents) Record(context.Context, SubscriptionEvent) (bool, error) { return true, nil }

func TestBillingPortShapes(t *testing.T) {
	var _ Payments = stubPayments{}
	var _ SubscriptionRepo = stubSubs{}
	var _ BillingEventRepo = stubEvents{}
	ev := SubscriptionEvent{NotificationID: "ntf_1", EventID: "evt_1", Type: "subscription.updated", Status: domain.SubscriptionPaused}
	if ev.Ignored {
		t.Fatal("zero SubscriptionEvent must not be ignored by default")
	}
	if (CheckoutParams{UserID: "u", CustomerID: "c"}).CustomerID != "c" {
		t.Fatal("CheckoutParams must carry only UserID and CustomerID")
	}
}
```

- [ ] **Step 2: Run it and confirm the failure.** `cd backend && go test ./internal/port/ -run TestBillingPortShapes` — expected: `cannot use stubPayments{} (...) as Payments value ... missing method CancelSubscription` (and `SubscriptionRepo` missing `EnsureTrial`).

- [ ] **Step 3: Replace the `SubscriptionRepo` block in `backend/internal/port/driven.go` with:**

```go
// SubscriptionRepo persists the one-row-per-user billing mirror
// (docs/payments.md). Provider ids are opaque strings (Paddle ctm_/sub_).
type SubscriptionRepo interface {
	GetByUserID(ctx context.Context, userID string) (domain.Subscription, error)
	GetByBillingCustomerID(ctx context.Context, customerID string) (domain.Subscription, error)
	// Upsert replaces the row; empty billing ids never clobber stored ones.
	Upsert(ctx context.Context, s domain.Subscription) error
	// EnsureTrial inserts a trialing row ending at trialEndsAt when the user
	// has no row yet; it is a no-op otherwise (ON CONFLICT DO NOTHING), so
	// concurrent first calls are safe.
	EnsureTrial(ctx context.Context, userID string, trialEndsAt time.Time) error
	// ListForReconciliation returns rows with a billing_subscription_id
	// where (status ∈ {active, past_due, paused} and current_period_end <
	// now - 1h) or last_event_at < now - 7d, ordered by user_id.
	ListForReconciliation(ctx context.Context, now time.Time) ([]domain.Subscription, error)
}
```

- [ ] **Step 4: Replace the `StripeEventRepo` block with:**

```go
// BillingEventRepo is the webhook idempotency ledger keyed by the provider
// notification id (a replay reuses event_id with a NEW notification id and
// is deliberately re-applied through the occurred_at ordering guard).
type BillingEventRepo interface {
	// Record inserts the notification id; first is false when it was already
	// recorded (the webhook must then be skipped).
	Record(ctx context.Context, ev SubscriptionEvent) (first bool, err error)
}
```

- [ ] **Step 5: Replace the `CheckoutParams` + `WebhookEvent` + `Payments` block with:**

```go
// CheckoutParams parameterizes a hosted checkout for the single annual plan
// (the price id is baked into the adapter). No URLs: the provider decides
// where checkout lands and the web /checkout page owns the success URL.
type CheckoutParams struct {
	UserID     string // bound via custom_data.user_id
	CustomerID string
}

// PortalURLs are temporary customer-portal links; never cache them.
// Cancel/UpdatePayment are empty when there is no subscription id.
type PortalURLs struct {
	Overview      string
	Cancel        string
	UpdatePayment string
}

// SubscriptionEvent is a verified, normalized provider subscription event —
// from a webhook or from a reconciliation read (then OccurredAt is "now").
type SubscriptionEvent struct {
	NotificationID string
	EventID        string
	Type           string
	OccurredAt     time.Time
	CustomerID     string
	SubscriptionID string
	UserID         string // custom_data.user_id when present
	Status         domain.SubscriptionStatus
	CurrentPeriodEnd  *time.Time
	CancelAtPeriodEnd bool // scheduled_change.action == "cancel"
	// Ignored marks non-subscription event types (transaction.*, unknown):
	// acknowledged with 200 and never applied.
	Ignored bool
}

// Payments is the provider-neutral billing surface (docs/payments.md),
// implemented by internal/adapter/out/paddle.
type Payments interface {
	// EnsureCustomer returns the provider customer id for the user, looking
	// it up by exact email first and creating it otherwise.
	EnsureCustomer(ctx context.Context, user domain.User) (customerID string, err error)
	// CreateCheckout returns the hosted checkout URL for the annual plan.
	CreateCheckout(ctx context.Context, p CheckoutParams) (url string, err error)
	CreatePortalSession(ctx context.Context, customerID, subscriptionID string) (PortalURLs, error)
	// ParseWebhook verifies the provider signature header against now
	// (HMAC, constant-time, 5-minute tolerance, empty secret refused) and
	// normalizes the envelope. Signature failures must not parse the body.
	ParseWebhook(payload []byte, sigHeader string, now time.Time) (SubscriptionEvent, error)
	// GetSubscription reads the live subscription (reconciliation).
	GetSubscription(ctx context.Context, subscriptionID string) (SubscriptionEvent, error)
	// CancelSubscription cancels at period end, or immediately (account
	// deletion, program piece 3).
	CancelSubscription(ctx context.Context, subscriptionID string, immediately bool) error
}
```

Update the gateway header comment to `// Gateways (implemented by internal/adapter/out/{googleapi,msgraph,paddle,openrouter,push,authjwt})`.

- [ ] **Step 6: Replace the `BillingService` block in `backend/internal/port/driving.go` with:**

```go
// BillingService implements the $50/yr Paddle flow (docs/payments.md).
type BillingService interface {
	// GetSubscription returns the user's subscription, granting the 14-day
	// trial row (anchored to users.created_at) when none exists yet, and
	// best-effort reconciling an active row that is past its period end.
	GetSubscription(ctx context.Context, userID string) (domain.Subscription, error)
	// CreateCheckout returns the hosted checkout URL; domain.ErrAlreadySubscribed
	// when a live provider subscription exists.
	CreateCheckout(ctx context.Context, userID string) (url string, err error)
	// CreatePortalSession returns temporary portal links; domain.ErrNoBillingProfile
	// when the user has no provider customer yet.
	CreatePortalSession(ctx context.Context, userID string) (PortalURLs, error)
	// HandleWebhook verifies, deduplicates (notification id), orders
	// (occurred_at) and applies a provider webhook.
	HandleWebhook(ctx context.Context, payload []byte, sigHeader string) error
	// ReconcileSubscriptions re-reads every stale row from the provider
	// (worker loop). Provider errors are logged and skipped, never returned.
	ReconcileSubscriptions(ctx context.Context) error
	// RequireActive returns a *domain.PaymentRequiredError (wrapping
	// domain.ErrPaymentRequired) unless HasAccess(now).
	RequireActive(ctx context.Context, userID string) error
}
```

- [ ] **Step 7: Delete the Stripe adapter.** `git rm -r backend/internal/adapter/out/stripeapi`.

- [ ] **Step 8: Verify the port package.** `cd backend && go test ./internal/port/... ./internal/domain/...` — expected: `ok` for both. (`go build ./...` now fails in `service`, `httpapi`, `postgres`, `cmd/*` — expected until Tasks 3, 8–13 land.)

- [ ] **Step 9: Commit.**

```bash
git add -A backend/internal/port backend/internal/adapter/out/stripeapi
git commit -m "feat(port): provider-neutral Payments/SubscriptionEvent/BillingEventRepo ports; remove stripeapi" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 3: Migration 0027 + Postgres repos (rename columns, `EnsureTrial`, `ListForReconciliation`, `billing_events`)

**Files:**
- Create: `backend/migrations/0027_paddle_billing.sql`
- Modify: `backend/internal/adapter/out/postgres/user.go` (the `port.SubscriptionRepo` and `port.StripeEventRepo` sections, L43–118)
- Modify: `backend/internal/adapter/out/postgres/store.go` (accessor L31, assertion L65, type L97)
- Modify: `backend/internal/adapter/out/postgres/postgres_test.go` (`truncateAll` table list)
- Modify: `backend/internal/adapter/out/postgres/misc_repo_test.go` (replace `TestStripeEventRepoRecordDedup`)
- Modify: `backend/internal/adapter/out/postgres/user_billing_test.go` (rename fields; add tests)

**Interfaces:**
- Consumes: Task 2 ports.
- Produces: `(*Store) BillingEvents() port.BillingEventRepo`; `subscriptionRepo` implementing the five-method `port.SubscriptionRepo`; tables `subscriptions(billing_customer_id, billing_subscription_id, …)` with `subscriptions_status_check`, `billing_events(notification_id PK, event_id, event_type, occurred_at, received_at)`.

- [ ] **Step 1: Write the failing Postgres tests.** In `backend/internal/adapter/out/postgres/user_billing_test.go` rename every `StripeCustomerID` → `BillingCustomerID`, `GetByStripeCustomerID` → `GetByBillingCustomerID`, and append:

```go
func TestSubscriptionRepoEnsureTrialIsIdempotent(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	first := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	second := first.Add(48 * time.Hour)

	if err := st.Subscriptions().EnsureTrial(ctx, "u1", first); err != nil {
		t.Fatalf("EnsureTrial 1: %v", err)
	}
	if err := st.Subscriptions().EnsureTrial(ctx, "u1", second); err != nil {
		t.Fatalf("EnsureTrial 2: %v", err)
	}
	got, err := st.Subscriptions().GetByUserID(ctx, "u1")
	if err != nil {
		t.Fatalf("GetByUserID: %v", err)
	}
	if got.Status != domain.SubscriptionTrialing {
		t.Fatalf("Status = %q, want trialing", got.Status)
	}
	if got.TrialEndsAt == nil || !got.TrialEndsAt.Equal(first) {
		t.Fatalf("TrialEndsAt = %v, want the FIRST grant %v", got.TrialEndsAt, first)
	}
	if got.Plan != domain.PlanAnnual || got.PriceUSD != domain.PriceUSDAnnual {
		t.Fatalf("plan/price = %q/%d", got.Plan, got.PriceUSD)
	}
}

func TestSubscriptionRepoEnsureTrialDoesNotTouchExistingRow(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	if err := st.Subscriptions().Upsert(ctx, domain.Subscription{UserID: "u1", Status: domain.SubscriptionCanceled}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := st.Subscriptions().EnsureTrial(ctx, "u1", time.Now().Add(14*24*time.Hour)); err != nil {
		t.Fatalf("EnsureTrial: %v", err)
	}
	got, err := st.Subscriptions().GetByUserID(ctx, "u1")
	if err != nil {
		t.Fatalf("GetByUserID: %v", err)
	}
	if got.Status != domain.SubscriptionCanceled || got.TrialEndsAt != nil {
		t.Fatalf("EnsureTrial must not re-grant: got %+v", got)
	}
}

func TestSubscriptionRepoStatusCheckConstraint(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	if err := st.Subscriptions().Upsert(ctx, domain.Subscription{UserID: "u1", Status: domain.SubscriptionPaused}); err != nil {
		t.Fatalf("paused must be accepted: %v", err)
	}
	_, err := db.ExecContext(ctx, `UPDATE subscriptions SET status = 'expired' WHERE user_id = $1`, "u1")
	if err == nil {
		t.Fatal("status 'expired' must be rejected by subscriptions_status_check")
	}
}

func TestSubscriptionRepoListForReconciliation(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	ptr := func(v time.Time) *time.Time { return &v }
	for _, id := range []string{"lapsed", "fresh", "stale", "nosub", "paused-lapsed", "canceled-lapsed"} {
		seedUser(t, st, id)
	}
	seed := []domain.Subscription{
		// active, period ended 2h ago -> selected
		{UserID: "lapsed", Status: domain.SubscriptionActive, BillingSubscriptionID: "sub_lapsed", BillingCustomerID: "ctm_lapsed", CurrentPeriodEnd: ptr(now.Add(-2 * time.Hour)), LastEventAt: ptr(now.Add(-time.Hour))},
		// active, period ended 30m ago (inside the 1h slack), recent event -> not selected
		{UserID: "fresh", Status: domain.SubscriptionActive, BillingSubscriptionID: "sub_fresh", BillingCustomerID: "ctm_fresh", CurrentPeriodEnd: ptr(now.Add(-30 * time.Minute)), LastEventAt: ptr(now.Add(-time.Hour))},
		// active, period far in the future, but no event for 8 days -> selected
		{UserID: "stale", Status: domain.SubscriptionActive, BillingSubscriptionID: "sub_stale", BillingCustomerID: "ctm_stale", CurrentPeriodEnd: ptr(now.Add(300 * 24 * time.Hour)), LastEventAt: ptr(now.Add(-8 * 24 * time.Hour))},
		// trialing, no provider subscription -> never selected
		{UserID: "nosub", Status: domain.SubscriptionTrialing, TrialEndsAt: ptr(now.Add(-24 * time.Hour)), LastEventAt: ptr(now.Add(-30 * 24 * time.Hour))},
		// paused with lapsed period -> selected
		{UserID: "paused-lapsed", Status: domain.SubscriptionPaused, BillingSubscriptionID: "sub_paused", BillingCustomerID: "ctm_paused", CurrentPeriodEnd: ptr(now.Add(-2 * time.Hour)), LastEventAt: ptr(now.Add(-time.Hour))},
		// canceled with lapsed period but recent event -> not selected (status not in the set)
		{UserID: "canceled-lapsed", Status: domain.SubscriptionCanceled, BillingSubscriptionID: "sub_canceled", BillingCustomerID: "ctm_canceled", CurrentPeriodEnd: ptr(now.Add(-2 * time.Hour)), LastEventAt: ptr(now.Add(-time.Hour))},
	}
	for _, s := range seed {
		if err := st.Subscriptions().Upsert(ctx, s); err != nil {
			t.Fatalf("seed %s: %v", s.UserID, err)
		}
	}
	got, err := st.Subscriptions().ListForReconciliation(ctx, now)
	if err != nil {
		t.Fatalf("ListForReconciliation: %v", err)
	}
	var ids []string
	for _, s := range got {
		ids = append(ids, s.UserID)
	}
	want := []string{"lapsed", "paused-lapsed", "stale"}
	if len(ids) != len(want) {
		t.Fatalf("selected = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("selected = %v, want %v (ordered by user_id)", ids, want)
		}
	}
}
```

Replace `TestStripeEventRepoRecordDedup` in `misc_repo_test.go` with:

```go
// --- BillingEventRepo ------------------------------------------------------

func TestBillingEventRepoRecordDedupByNotificationID(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

	first, err := st.BillingEvents().Record(ctx, port.SubscriptionEvent{NotificationID: "ntf_1", EventID: "evt_1", Type: "subscription.updated", OccurredAt: at})
	if err != nil {
		t.Fatalf("Record 1: %v", err)
	}
	if !first {
		t.Fatal("first Record must report first = true")
	}
	again, err := st.BillingEvents().Record(ctx, port.SubscriptionEvent{NotificationID: "ntf_1", EventID: "evt_1", Type: "subscription.updated", OccurredAt: at})
	if err != nil {
		t.Fatalf("Record 2: %v", err)
	}
	if again {
		t.Fatal("same notification id must report first = false")
	}
	// A Paddle replay: same event id, NEW notification id -> recorded as first
	// (the service's occurred_at guard decides whether it changes anything).
	replay, err := st.BillingEvents().Record(ctx, port.SubscriptionEvent{NotificationID: "ntf_2", EventID: "evt_1", Type: "subscription.updated", OccurredAt: at})
	if err != nil {
		t.Fatalf("Record 3: %v", err)
	}
	if !replay {
		t.Fatal("a new notification id for a replayed event id must be first = true")
	}
}
```

(`misc_repo_test.go` already imports `port`; add `"time"` to its imports if absent. `user_billing_test.go` already imports `time`.)

- [ ] **Step 2: Run and confirm compile failure.** `cd backend && REQUIRE_DOCKER=1 go test ./internal/adapter/out/postgres/ -run 'TestSubscriptionRepo|TestBillingEventRepo'` — expected: `undefined: domain.SubscriptionPaused`, `st.Subscriptions().EnsureTrial undefined`, `st.BillingEvents undefined`, plus the store's `var _ port.SubscriptionRepo = subscriptionRepo{}` assertion failing.

- [ ] **Step 3: Create `backend/migrations/0027_paddle_billing.sql`:**

```sql
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
```

- [ ] **Step 4: Replace the subscription + event sections of `backend/internal/adapter/out/postgres/user.go` (everything from `// --- port.SubscriptionRepo` to the end of file) with:**

```go
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
```

Add `"time"` and `"calendium/backend/internal/port"` to `user.go`'s imports.

- [ ] **Step 5: Update `backend/internal/adapter/out/postgres/store.go`.** Replace `func (s *Store) StripeEvents() port.StripeEventRepo     { return stripeEventRepo{s} }` with `func (s *Store) BillingEvents() port.BillingEventRepo   { return billingEventRepo{s} }`; replace `_ port.StripeEventRepo     = stripeEventRepo{}` with `_ port.BillingEventRepo    = billingEventRepo{}`; replace the type `stripeEventRepo     struct{ *Store }` with `billingEventRepo    struct{ *Store }`. Then run `gofmt -w backend/internal/adapter/out/postgres/store.go`.

- [ ] **Step 6: Update `truncateAll` in `postgres_test.go`:** replace `stripe_events` with `billing_events` in the `TRUNCATE` list.

- [ ] **Step 7: Run the Postgres suite.** `cd backend && REQUIRE_DOCKER=1 go test ./internal/adapter/out/postgres/...` — expected: `ok` (all repos; the migration applies cleanly on a fresh container).

- [ ] **Step 8: Commit.**

```bash
git add backend/migrations/0027_paddle_billing.sql backend/internal/adapter/out/postgres
git commit -m "feat(postgres): migration 0027 renames billing columns, adds billing_events, EnsureTrial and ListForReconciliation" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 4: Config — `PADDLE_*`, `BILLING_RECONCILE_INTERVAL`, cloud startup rule

**Files:**
- Modify: `backend/internal/config/config.go` (replace the `Stripe` struct L81–86, the `Config.Stripe` field L205, the `Stripe:` literal L250–254; add parsing + `ValidateCloudBilling`)
- Modify: `backend/internal/config/config_test.go` (`configEnvKeys`; add tests)

**Interfaces:**
- Produces: `config.Paddle{Env, APIKey, WebhookSecret, AnnualPriceID string}`; `config.Billing{ReconcileInterval time.Duration}`; `Config.Paddle`, `Config.Billing`; consts `config.PaddleEnvSandbox = "sandbox"`, `config.PaddleEnvLive = "live"`; `func (c Config) ValidateCloudBilling() error`.

- [ ] **Step 1: Write the failing config tests.** In `config_test.go`, replace `"STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET", "STRIPE_PRICE_ID_ANNUAL",` in `configEnvKeys` with `"PADDLE_ENV", "PADDLE_API_KEY", "PADDLE_WEBHOOK_SECRET", "PADDLE_PRICE_ID_ANNUAL", "BILLING_RECONCILE_INTERVAL",` and append:

```go
// TestPaddleFromEnv covers the Paddle knobs: env default/validation, key
// passthrough, and the reconcile interval default/override/validation.
func TestPaddleFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
		check   func(t *testing.T, c Config)
	}{
		{
			name: "PADDLE_ENV defaults to sandbox and interval to 6h",
			env:  withBase(nil),
			check: func(t *testing.T, c Config) {
				assertEq(t, "Paddle.Env", c.Paddle.Env, PaddleEnvSandbox)
				assertDur(t, "Billing.ReconcileInterval", c.Billing.ReconcileInterval, 6*time.Hour)
			},
		},
		{
			name: "keys and live env pass through",
			env: withBase(map[string]string{
				"PADDLE_ENV": "live", "PADDLE_API_KEY": "pdl_live_k", "PADDLE_WEBHOOK_SECRET": "pdl_ntfset_s", "PADDLE_PRICE_ID_ANNUAL": "pri_1",
			}),
			check: func(t *testing.T, c Config) {
				assertEq(t, "Paddle.Env", c.Paddle.Env, PaddleEnvLive)
				assertEq(t, "Paddle.APIKey", c.Paddle.APIKey, "pdl_live_k")
				assertEq(t, "Paddle.WebhookSecret", c.Paddle.WebhookSecret, "pdl_ntfset_s")
				assertEq(t, "Paddle.AnnualPriceID", c.Paddle.AnnualPriceID, "pri_1")
			},
		},
		{name: "PADDLE_ENV invalid errors", env: withBase(map[string]string{"PADDLE_ENV": "prod"}), wantErr: true},
		{
			name:  "BILLING_RECONCILE_INTERVAL override",
			env:   withBase(map[string]string{"BILLING_RECONCILE_INTERVAL": "30m"}),
			check: func(t *testing.T, c Config) { assertDur(t, "Billing.ReconcileInterval", c.Billing.ReconcileInterval, 30*time.Minute) },
		},
		{name: "BILLING_RECONCILE_INTERVAL zero errors", env: withBase(map[string]string{"BILLING_RECONCILE_INTERVAL": "0s"}), wantErr: true},
		{name: "BILLING_RECONCILE_INTERVAL garbage errors", env: withBase(map[string]string{"BILLING_RECONCILE_INTERVAL": "soon"}), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			c, err := FromEnv()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("FromEnv() error = nil, want non-nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("FromEnv() unexpected error: %v", err)
			}
			tt.check(t, c)
		})
	}
}

// TestValidateCloudBilling pins the startup rule: cloud mode refuses to run
// without every Paddle credential; self-host never needs them.
func TestValidateCloudBilling(t *testing.T) {
	full := Paddle{Env: PaddleEnvSandbox, APIKey: "k", WebhookSecret: "s", AnnualPriceID: "p"}
	tests := []struct {
		name       string
		selfHosted bool
		paddle     Paddle
		wantErr    string // substring; "" = nil error
	}{
		{"self-host with nothing set is fine", true, Paddle{}, ""},
		{"cloud with all keys is fine", false, full, ""},
		{"cloud missing api key", false, Paddle{WebhookSecret: "s", AnnualPriceID: "p"}, "PADDLE_API_KEY"},
		{"cloud missing webhook secret", false, Paddle{APIKey: "k", AnnualPriceID: "p"}, "PADDLE_WEBHOOK_SECRET"},
		{"cloud missing price id", false, Paddle{APIKey: "k", WebhookSecret: "s"}, "PADDLE_PRICE_ID_ANNUAL"},
		{"cloud missing everything names all three", false, Paddle{}, "PADDLE_API_KEY, PADDLE_WEBHOOK_SECRET, PADDLE_PRICE_ID_ANNUAL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Config{Instance: Instance{SelfHosted: tt.selfHosted}, Paddle: tt.paddle}
			err := c.ValidateCloudBilling()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateCloudBilling() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateCloudBilling() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}
```

Add `"strings"` to the test imports.

- [ ] **Step 2: Run and confirm failure.** `cd backend && go test ./internal/config/ -run 'TestPaddleFromEnv|TestValidateCloudBilling'` — expected: `undefined: PaddleEnvSandbox`, `c.Paddle undefined`, `c.ValidateCloudBilling undefined`.

- [ ] **Step 3: Implement in `config.go`.** Replace the `Stripe` struct with:

```go
// Paddle environment values (PADDLE_ENV).
const (
	PaddleEnvSandbox = "sandbox"
	PaddleEnvLive    = "live"
)

// Paddle configures billing (docs/payments.md). Cloud only: with
// SELF_HOSTED=false every field but Env is mandatory (ValidateCloudBilling).
type Paddle struct {
	Env           string // PADDLE_ENV: sandbox (default) | live
	APIKey        string // PADDLE_API_KEY
	WebhookSecret string // PADDLE_WEBHOOK_SECRET (notification destination secret)
	AnnualPriceID string // PADDLE_PRICE_ID_ANNUAL
}

// Billing holds billing tunables.
type Billing struct {
	// ReconcileInterval paces the worker's subscription reconciliation loop
	// (BILLING_RECONCILE_INTERVAL, default 6h).
	ReconcileInterval time.Duration
}
```

In `Config`, replace `Stripe     Stripe` with `Paddle     Paddle` and add `Billing    Billing`. In `FromEnv`, replace the `Stripe: Stripe{...}` literal with:

```go
		Paddle: Paddle{
			Env:           os.Getenv("PADDLE_ENV"),
			APIKey:        os.Getenv("PADDLE_API_KEY"),
			WebhookSecret: os.Getenv("PADDLE_WEBHOOK_SECRET"),
			AnnualPriceID: os.Getenv("PADDLE_PRICE_ID_ANNUAL"),
		},
```

After the `UNDO_SEND_SECONDS` block add:

```go
	switch cfg.Paddle.Env {
	case "":
		cfg.Paddle.Env = PaddleEnvSandbox
	case PaddleEnvSandbox, PaddleEnvLive:
	default:
		errs = append(errs, fmt.Errorf("PADDLE_ENV must be %q or %q, got %q", PaddleEnvSandbox, PaddleEnvLive, cfg.Paddle.Env))
	}

	cfg.Billing.ReconcileInterval = 6 * time.Hour
	if v := os.Getenv("BILLING_RECONCILE_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("BILLING_RECONCILE_INTERVAL must be a positive Go duration (e.g. 6h), got %q", v))
		} else {
			cfg.Billing.ReconcileInterval = d
		}
	}
```

Change the `Instance.SelfHosted` comment `calling Stripe.` to `calling the payments provider.` Append at the end of the file:

```go
// ValidateCloudBilling enforces the cloud startup rule (docs/payments.md):
// with SELF_HOSTED=false the Paddle credentials are mandatory, so a cloud
// deployment can never boot with a forgeable webhook or no biller. Both
// cmd/api and cmd/worker call it right after FromEnv.
func (c Config) ValidateCloudBilling() error {
	if c.Instance.SelfHosted {
		return nil
	}
	var missing []string
	if c.Paddle.APIKey == "" {
		missing = append(missing, "PADDLE_API_KEY")
	}
	if c.Paddle.WebhookSecret == "" {
		missing = append(missing, "PADDLE_WEBHOOK_SECRET")
	}
	if c.Paddle.AnnualPriceID == "" {
		missing = append(missing, "PADDLE_PRICE_ID_ANNUAL")
	}
	if len(missing) > 0 {
		return fmt.Errorf("cloud mode (SELF_HOSTED=false) requires %s; set them, or run with SELF_HOSTED=true", strings.Join(missing, ", "))
	}
	return nil
}
```

- [ ] **Step 4: Run the config suite.** `cd backend && go test ./internal/config/...` — expected: `ok`.

- [ ] **Step 5: Commit.**

```bash
git add backend/internal/config
git commit -m "feat(config): PADDLE_* and BILLING_RECONCILE_INTERVAL with a cloud-mode startup check; drop STRIPE_*" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 5: Paddle client — base URL by env, bearer auth, `{data,meta}`/`{error}` envelopes, `EnsureCustomer`, `CreateCheckout`

**Files:**
- Create: `backend/internal/adapter/out/paddle/client.go`
- Create: `backend/internal/adapter/out/paddle/payments.go` (this task adds `EnsureCustomer` + `CreateCheckout`; Task 6 appends the rest)
- Create: `backend/internal/adapter/out/paddle/client_test.go`
- Create: `backend/internal/adapter/out/paddle/payments_test.go`

**Interfaces:**
- Consumes: `port.Payments`, `port.CheckoutParams`, `domain.User`, `domain.ErrUnauthorized`, `domain.ErrNotFound`.
- Produces: `paddle.Config{Env, APIKey, WebhookSecret, AnnualPriceID, BaseURL string}`; `func paddle.NewClient(cfg Config, hc *http.Client) *Client`; `func paddle.BaseURLFor(env string) string`; consts `paddle.EnvSandbox`, `paddle.EnvLive`; `type paddle.APIError struct{Status int; Type, Code, Detail string}`; `(*Client) EnsureCustomer`, `(*Client) CreateCheckout`; unexported `(*Client) do(ctx, method, path string, in, out any) error` decoding `data`.

- [ ] **Step 1: Write the failing client tests** at `backend/internal/adapter/out/paddle/client_test.go`:

```go
package paddle

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"calendium/backend/internal/domain"
)

// newTestClient points the gateway at an httptest server through the
// Config.BaseURL override (the only injection seam besides *http.Client).
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	return NewClient(Config{Env: EnvSandbox, APIKey: "pdl_test_key", WebhookSecret: "ntf_secret", AnnualPriceID: "pri_annual", BaseURL: srv.URL}, srv.Client())
}

func TestBaseURLFor(t *testing.T) {
	if got := BaseURLFor(EnvSandbox); got != "https://sandbox-api.paddle.com" {
		t.Fatalf("sandbox = %q", got)
	}
	if got := BaseURLFor(EnvLive); got != "https://api.paddle.com" {
		t.Fatalf("live = %q", got)
	}
	if got := BaseURLFor(""); got != "https://sandbox-api.paddle.com" {
		t.Fatalf("empty env must default to sandbox, got %q", got)
	}
	if c := NewClient(Config{Env: EnvLive}, nil); c.baseURL != "https://api.paddle.com" {
		t.Fatalf("NewClient live baseURL = %q", c.baseURL)
	}
}

func TestDoSendsBearerAndDecodesData(t *testing.T) {
	var gotAuth, gotAccept, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotCT = r.Header.Get("Content-Type")
		_, _ = w.Write([]byte(`{"data":{"id":"ctm_1"},"meta":{"request_id":"r"}}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	var out struct {
		ID string `json:"id"`
	}
	if err := c.do(context.Background(), http.MethodPost, "/customers", map[string]string{"email": "a@b.c"}, &out); err != nil {
		t.Fatalf("do: %v", err)
	}
	if out.ID != "ctm_1" {
		t.Fatalf("decoded id = %q, want ctm_1 (must unwrap the data envelope)", out.ID)
	}
	if gotAuth != "Bearer pdl_test_key" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotAccept != "application/json" || gotCT != "application/json" {
		t.Fatalf("Accept/Content-Type = %q/%q", gotAccept, gotCT)
	}
}

func TestDoMapsErrors(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		wantIs   error // nil = only *APIError
		wantCode string
	}{
		{"unauthorized", http.StatusUnauthorized, domain.ErrUnauthorized, "authentication_malformed"},
		{"forbidden", http.StatusForbidden, domain.ErrUnauthorized, "forbidden"},
		{"not found", http.StatusNotFound, domain.ErrNotFound, "entity_not_found"},
		{"server error", http.StatusInternalServerError, nil, "internal_error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":{"type":"request_error","code":"` + tc.wantCode + `","detail":"nope"}}`))
			}))
			defer srv.Close()
			c := newTestClient(t, srv)
			err := c.do(context.Background(), http.MethodGet, "/subscriptions/sub_1", nil, &struct{}{})
			if err == nil {
				t.Fatal("want error")
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Fatalf("err = %v, want wrap of %v", err, tc.wantIs)
			}
			var api *APIError
			if !errors.As(err, &api) {
				t.Fatalf("err = %v, want *APIError in chain", err)
			}
			if api.Status != tc.status || api.Code != tc.wantCode || api.Detail != "nope" {
				t.Fatalf("APIError = %+v", api)
			}
			if !strings.Contains(err.Error(), "paddle: http") {
				t.Fatalf("err text = %q", err.Error())
			}
		})
	}
}
```

And `backend/internal/adapter/out/paddle/payments_test.go`:

```go
package paddle

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

func strptr(s string) *string { return &s }

func TestEnsureCustomer(t *testing.T) {
	t.Run("creates when the email lookup is empty", func(t *testing.T) {
		var gotEmailQuery string
		var created map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/customers":
				gotEmailQuery = r.URL.Query().Get("email")
				_, _ = w.Write([]byte(`{"data":[],"meta":{"pagination":{"has_more":false}}}`))
			case r.Method == http.MethodPost && r.URL.Path == "/customers":
				raw, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(raw, &created)
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"data":{"id":"ctm_new","email":"ada@x.com"}}`))
			default:
				t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			}
		}))
		defer srv.Close()
		id, err := newTestClient(t, srv).EnsureCustomer(context.Background(), domain.User{ID: "user-9", Email: "ada@x.com", Name: strptr("Ada")})
		if err != nil {
			t.Fatalf("EnsureCustomer: %v", err)
		}
		if id != "ctm_new" {
			t.Fatalf("id = %q", id)
		}
		if gotEmailQuery != "ada@x.com" {
			t.Fatalf("lookup email = %q", gotEmailQuery)
		}
		if created["email"] != "ada@x.com" || created["name"] != "Ada" {
			t.Fatalf("create body = %v", created)
		}
	})

	t.Run("returns the existing customer without creating", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("unexpected create call: %s %s", r.Method, r.URL.Path)
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"ctm_existing","email":"a@b.com"}]}`))
		}))
		defer srv.Close()
		id, err := newTestClient(t, srv).EnsureCustomer(context.Background(), domain.User{ID: "u", Email: "a@b.com"})
		if err != nil {
			t.Fatal(err)
		}
		if id != "ctm_existing" {
			t.Fatalf("id = %q", id)
		}
	})

	t.Run("omits name when the user has none", func(t *testing.T) {
		var created map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`{"data":[]}`))
				return
			}
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &created)
			_, _ = w.Write([]byte(`{"data":{"id":"ctm_noname"}}`))
		}))
		defer srv.Close()
		if _, err := newTestClient(t, srv).EnsureCustomer(context.Background(), domain.User{ID: "u", Email: "n@x.com"}); err != nil {
			t.Fatal(err)
		}
		if _, ok := created["name"]; ok {
			t.Fatalf("create body had name = %v, want none", created["name"])
		}
	})
}

func TestCreateCheckout(t *testing.T) {
	t.Run("posts the annual item, customer and custom_data and returns checkout.url", func(t *testing.T) {
		var body map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/transactions" {
				t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
			}
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":"txn_1","status":"ready","checkout":{"url":"https://app.example/checkout?_ptxn=txn_1"}}}`))
		}))
		defer srv.Close()
		url, err := newTestClient(t, srv).CreateCheckout(context.Background(), port.CheckoutParams{UserID: "user-9", CustomerID: "ctm_1"})
		if err != nil {
			t.Fatal(err)
		}
		if url != "https://app.example/checkout?_ptxn=txn_1" {
			t.Fatalf("url = %q", url)
		}
		items, _ := body["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("items = %v", body["items"])
		}
		item, _ := items[0].(map[string]any)
		if item["price_id"] != "pri_annual" || item["quantity"] != float64(1) {
			t.Fatalf("item = %v", item)
		}
		if body["customer_id"] != "ctm_1" {
			t.Fatalf("customer_id = %v", body["customer_id"])
		}
		custom, _ := body["custom_data"].(map[string]any)
		if custom["user_id"] != "user-9" {
			t.Fatalf("custom_data = %v", body["custom_data"])
		}
		for _, forbidden := range []string{"success_url", "cancel_url", "return_url"} {
			if _, ok := body[forbidden]; ok {
				t.Fatalf("body must not carry %s (no client URLs)", forbidden)
			}
		}
	})

	t.Run("fails loudly when the transaction has no checkout url", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"data":{"id":"txn_2","checkout":{"url":null}}}`))
		}))
		defer srv.Close()
		_, err := newTestClient(t, srv).CreateCheckout(context.Background(), port.CheckoutParams{UserID: "u", CustomerID: "ctm_1"})
		if err == nil {
			t.Fatal("want error when the default payment link is unset")
		}
	})
}
```

- [ ] **Step 2: Run and confirm failure.** `cd backend && go test ./internal/adapter/out/paddle/` — expected: `no Go files` / `undefined: NewClient`.

- [ ] **Step 3: Create `backend/internal/adapter/out/paddle/client.go`:**

```go
// Package paddle implements port.Payments against the Paddle Billing REST
// API (docs/payments.md): customers, transactions (overlay checkout),
// customer-portal sessions, subscription reads/cancels, and Paddle-Signature
// webhook verification. stdlib net/http only; the http.Client is injected.
package paddle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// Environment names (PADDLE_ENV).
const (
	EnvSandbox = "sandbox"
	EnvLive    = "live"

	sandboxBaseURL = "https://sandbox-api.paddle.com"
	liveBaseURL    = "https://api.paddle.com"
)

// Config configures the gateway. BaseURL overrides the environment-derived
// origin (tests point it at an httptest server); leave it empty in production.
type Config struct {
	Env           string // EnvSandbox | EnvLive
	APIKey        string
	WebhookSecret string
	AnnualPriceID string
	BaseURL       string
}

// Client is the Paddle billing gateway.
type Client struct {
	cfg     Config
	baseURL string
	hc      *http.Client
}

var _ port.Payments = (*Client)(nil)

// BaseURLFor returns the API origin for a PADDLE_ENV value; anything but
// "live" is sandbox so a typo can never hit production billing.
func BaseURLFor(env string) string {
	if env == EnvLive {
		return liveBaseURL
	}
	return sandboxBaseURL
}

// NewClient builds the gateway. hc may be nil (http.DefaultClient).
func NewClient(cfg Config, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	base := cfg.BaseURL
	if base == "" {
		base = BaseURLFor(cfg.Env)
	}
	return &Client{cfg: cfg, baseURL: strings.TrimRight(base, "/"), hc: hc}
}

// APIError is a non-2xx Paddle response ({error: {type, code, detail}}).
type APIError struct {
	Status int
	Type   string
	Code   string
	Detail string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("paddle: http %d (%s/%s): %s", e.Status, e.Type, e.Code, e.Detail)
}

// do performs one JSON call. Paddle wraps every success body in {data, meta};
// out (when non-nil) receives the decoded `data` member. 401/403 wrap
// domain.ErrUnauthorized and 404 wraps domain.ErrNotFound; every non-2xx
// carries an *APIError in its chain.
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("paddle: encode request: %w", err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("paddle: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("paddle: %s %s: %w", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("paddle: read response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		var env struct {
			Error struct {
				Type   string `json:"type"`
				Code   string `json:"code"`
				Detail string `json:"detail"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &env)
		apiErr := &APIError{Status: res.StatusCode, Type: env.Error.Type, Code: env.Error.Code, Detail: env.Error.Detail}
		switch res.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Errorf("%w: %w", domain.ErrUnauthorized, apiErr)
		case http.StatusNotFound:
			return fmt.Errorf("%w: %w", domain.ErrNotFound, apiErr)
		}
		return apiErr
	}
	if out == nil {
		return nil
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("paddle: decode envelope: %w", err)
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("paddle: decode data: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Create `backend/internal/adapter/out/paddle/payments.go` (first half; Task 6 appends):**

```go
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
```

- [ ] **Step 5: Temporarily satisfy the `port.Payments` assertion** so the package compiles for this task's tests: append to `payments.go` the four stubs Task 6 will replace —

```go
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
```

- [ ] **Step 6: Run the adapter tests.** `cd backend && go test ./internal/adapter/out/paddle/ -run 'TestBaseURLFor|TestDo|TestEnsureCustomer|TestCreateCheckout' -v` — expected: all `PASS`.

- [ ] **Step 7: Commit.**

```bash
git add backend/internal/adapter/out/paddle
git commit -m "feat(paddle): stdlib Paddle Billing client with customer lookup/create and overlay checkout transactions" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 6: Paddle — portal sessions, `GetSubscription`, `CancelSubscription`, status mapping

**Files:**
- Modify: `backend/internal/adapter/out/paddle/payments.go` (replace the three Task 5 stubs; add `subscription`, `normalizeSubscription`, `mapStatus`)
- Modify: `backend/internal/adapter/out/paddle/payments_test.go` (append)

**Interfaces:**
- Produces: `(*Client) CreatePortalSession(ctx, customerID, subscriptionID string) (port.PortalURLs, error)`; `(*Client) GetSubscription(ctx, subscriptionID string) (port.SubscriptionEvent, error)` (Type `"subscription.reconciled"`, `OccurredAt` zero — the service stamps it); `(*Client) CancelSubscription(ctx, subscriptionID string, immediately bool) error`; unexported `type subscription struct`, `func normalizeSubscription(sub subscription, ev port.SubscriptionEvent) port.SubscriptionEvent`, `func mapStatus(s string) domain.SubscriptionStatus`.

- [ ] **Step 1: Append the failing tests to `payments_test.go`:**

```go
func TestCreatePortalSession(t *testing.T) {
	t.Run("returns overview plus per-subscription cancel/update links", func(t *testing.T) {
		var gotPath string
		var body map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":"cpls_1","urls":{"general":{"overview":"https://portal/overview"},"subscriptions":[{"id":"sub_1","cancel_subscription":"https://portal/cancel","update_subscription_payment_method":"https://portal/update","view_subscription":"https://portal/view"}]}}}`))
		}))
		defer srv.Close()
		urls, err := newTestClient(t, srv).CreatePortalSession(context.Background(), "ctm_1", "sub_1")
		if err != nil {
			t.Fatal(err)
		}
		if gotPath != "/customers/ctm_1/portal-sessions" {
			t.Fatalf("path = %q", gotPath)
		}
		ids, _ := body["subscription_ids"].([]any)
		if len(ids) != 1 || ids[0] != "sub_1" {
			t.Fatalf("subscription_ids = %v", body["subscription_ids"])
		}
		want := port.PortalURLs{Overview: "https://portal/overview", Cancel: "https://portal/cancel", UpdatePayment: "https://portal/update"}
		if urls != want {
			t.Fatalf("urls = %+v, want %+v", urls, want)
		}
	})

	t.Run("without a subscription id sends no ids and leaves cancel/update empty", func(t *testing.T) {
		var body map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			_, _ = w.Write([]byte(`{"data":{"urls":{"general":{"overview":"https://portal/overview"},"subscriptions":[]}}}`))
		}))
		defer srv.Close()
		urls, err := newTestClient(t, srv).CreatePortalSession(context.Background(), "ctm_1", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := body["subscription_ids"]; ok {
			t.Fatalf("body must omit subscription_ids, got %v", body)
		}
		if urls.Overview != "https://portal/overview" || urls.Cancel != "" || urls.UpdatePayment != "" {
			t.Fatalf("urls = %+v", urls)
		}
	})
}

func TestGetSubscription(t *testing.T) {
	t.Run("normalizes period, customer, custom_data and scheduled cancel", func(t *testing.T) {
		var gotMethod, gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotMethod, gotPath = r.Method, r.URL.EscapedPath()
			_, _ = w.Write([]byte(`{"data":{
				"id":"sub_1","status":"active","customer_id":"ctm_1",
				"custom_data":{"user_id":"user-9"},
				"current_billing_period":{"starts_at":"2026-10-01T00:00:00Z","ends_at":"2027-10-01T00:00:00Z"},
				"next_billed_at":"2027-10-01T00:00:00Z",
				"scheduled_change":{"action":"cancel","effective_at":"2027-10-01T00:00:00Z"},
				"items":[{"price":{"id":"pri_annual"}}]}}`))
		}))
		defer srv.Close()
		ev, err := newTestClient(t, srv).GetSubscription(context.Background(), "sub/1")
		if err != nil {
			t.Fatal(err)
		}
		if gotMethod != http.MethodGet || gotPath != "/subscriptions/sub%2F1" {
			t.Fatalf("request = %s %s", gotMethod, gotPath)
		}
		if ev.SubscriptionID != "sub_1" || ev.CustomerID != "ctm_1" || ev.UserID != "user-9" {
			t.Fatalf("ids = %+v", ev)
		}
		if ev.Status != domain.SubscriptionActive || !ev.CancelAtPeriodEnd {
			t.Fatalf("state = %+v", ev)
		}
		if ev.CurrentPeriodEnd == nil || ev.CurrentPeriodEnd.Format(time.RFC3339) != "2027-10-01T00:00:00Z" {
			t.Fatalf("CurrentPeriodEnd = %v", ev.CurrentPeriodEnd)
		}
		if ev.Type != "subscription.reconciled" || !ev.OccurredAt.IsZero() || ev.Ignored {
			t.Fatalf("reconcile read must have Type subscription.reconciled, zero OccurredAt, not Ignored: %+v", ev)
		}
	})

	t.Run("propagates not found", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"type":"request_error","code":"entity_not_found","detail":"gone"}}`))
		}))
		defer srv.Close()
		_, err := newTestClient(t, srv).GetSubscription(context.Background(), "sub_missing")
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want wrap of ErrNotFound", err)
		}
	})
}

func TestCancelSubscription(t *testing.T) {
	for _, tc := range []struct {
		name        string
		immediately bool
		want        string
	}{
		{"end of period", false, "next_billing_period"},
		{"immediately", true, "immediately"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			var body map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				raw, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(raw, &body)
				_, _ = w.Write([]byte(`{"data":{"id":"sub_1","status":"active"}}`))
			}))
			defer srv.Close()
			if err := newTestClient(t, srv).CancelSubscription(context.Background(), "sub_1", tc.immediately); err != nil {
				t.Fatal(err)
			}
			if gotPath != "/subscriptions/sub_1/cancel" {
				t.Fatalf("path = %q", gotPath)
			}
			if body["effective_from"] != tc.want {
				t.Fatalf("effective_from = %v, want %q", body["effective_from"], tc.want)
			}
		})
	}
}

// TestMapStatus is the full Paddle → domain table, including the defensive
// trialing→active mapping and fail-closed default.
func TestMapStatus(t *testing.T) {
	tests := []struct {
		in   string
		want domain.SubscriptionStatus
	}{
		{"active", domain.SubscriptionActive},
		{"trialing", domain.SubscriptionActive},
		{"past_due", domain.SubscriptionPastDue},
		{"paused", domain.SubscriptionPaused},
		{"canceled", domain.SubscriptionCanceled},
		{"", domain.SubscriptionNone},
		{"some_future_status", domain.SubscriptionNone},
	}
	for _, tc := range tests {
		t.Run("status/"+tc.in, func(t *testing.T) {
			if got := mapStatus(tc.in); got != tc.want {
				t.Fatalf("mapStatus(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
```

Add `"errors"` and `"time"` to `payments_test.go`'s imports.

- [ ] **Step 2: Run and confirm failure.** `cd backend && go test ./internal/adapter/out/paddle/ -run 'TestCreatePortalSession|TestGetSubscription|TestCancelSubscription|TestMapStatus'` — expected: `undefined: mapStatus` and the `not implemented` stub errors.

- [ ] **Step 3: Replace the Task 5 stubs for `CreatePortalSession`, `GetSubscription`, `CancelSubscription` (keep the `ParseWebhook` stub), appending to `payments.go`:**

```go
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

// CancelSubscription cancels at the next billing period (Paddle keeps
// status=active with scheduled_change.action=cancel) or immediately.
func (c *Client) CancelSubscription(ctx context.Context, subscriptionID string, immediately bool) error {
	effective := "next_billing_period"
	if immediately {
		effective = "immediately"
	}
	return c.do(ctx, http.MethodPost, "/subscriptions/"+url.PathEscape(subscriptionID)+"/cancel",
		map[string]string{"effective_from": effective}, nil)
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
```

- [ ] **Step 4: Run the adapter tests.** `cd backend && go test ./internal/adapter/out/paddle/ -v` — expected: everything except the still-stubbed webhook `PASS`.

- [ ] **Step 5: Commit.**

```bash
git add backend/internal/adapter/out/paddle
git commit -m "feat(paddle): portal sessions, subscription read/cancel and status mapping" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 7: Paddle — `Paddle-Signature` verification and webhook envelope parsing

**Files:**
- Create: `backend/internal/adapter/out/paddle/webhook.go`
- Create: `backend/internal/adapter/out/paddle/webhook_test.go`
- Modify: `backend/internal/adapter/out/paddle/payments.go` (delete the `ParseWebhook` stub)

**Interfaces:**
- Produces: `(*Client) ParseWebhook(payload []byte, sigHeader string, now time.Time) (port.SubscriptionEvent, error)` — signature failures return a plain error (service maps to `ErrUnauthorized`), malformed JSON after a valid signature returns an error wrapping `domain.ErrValidation`; unexported `func verifySignature(payload []byte, header, secret string, now time.Time) error`; const `webhookTolerance = 5 * time.Minute`.

- [ ] **Step 1: Write the failing tests** at `backend/internal/adapter/out/paddle/webhook_test.go`:

```go
package paddle

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// sign computes HMAC-SHA256(secret, "<ts>:<payload>") hex — the Paddle scheme.
func sign(secret string, ts int64, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d:%s", ts, payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func header(ts int64, h1 ...string) string {
	parts := []string{fmt.Sprintf("ts=%d", ts)}
	for _, h := range h1 {
		parts = append(parts, "h1="+h)
	}
	return strings.Join(parts, ";")
}

func TestVerifySignature(t *testing.T) {
	secret := "pdl_ntfset_secret"
	payload := []byte(`{"event_id":"evt_1"}`)
	now := time.Unix(1_700_000_000, 0)
	ts := now.Unix()

	t.Run("valid", func(t *testing.T) {
		if err := verifySignature(payload, header(ts, sign(secret, ts, payload)), secret, now); err != nil {
			t.Fatalf("valid signature rejected: %v", err)
		}
	})
	t.Run("wrong secret", func(t *testing.T) {
		if err := verifySignature(payload, header(ts, sign(secret, ts, payload)), "other", now); err == nil {
			t.Fatal("wrong secret accepted")
		}
	})
	t.Run("empty configured secret is refused even with a matching mac", func(t *testing.T) {
		if err := verifySignature(payload, header(ts, sign("", ts, payload)), "", now); err == nil {
			t.Fatal("empty secret must be refused")
		}
	})
	t.Run("tampered body", func(t *testing.T) {
		if err := verifySignature([]byte(`{"event_id":"evt_2"}`), header(ts, sign(secret, ts, payload)), secret, now); err == nil {
			t.Fatal("tampered body accepted")
		}
	})
	t.Run("multiple h1: any match passes, including when the first is stale", func(t *testing.T) {
		h := header(ts, sign("old-secret", ts, payload), sign(secret, ts, payload))
		if err := verifySignature(payload, h, secret, now); err != nil {
			t.Fatalf("rotated header rejected: %v", err)
		}
	})
	t.Run("tolerance window", func(t *testing.T) {
		h := header(ts, sign(secret, ts, payload))
		for _, tc := range []struct {
			name  string
			clock time.Time
			ok    bool
		}{
			{"4m late", now.Add(4 * time.Minute), true},
			{"4m early (skewed sender clock)", now.Add(-4 * time.Minute), true},
			{"6m late", now.Add(6 * time.Minute), false},
			{"6m early", now.Add(-6 * time.Minute), false},
		} {
			err := verifySignature(payload, h, secret, tc.clock)
			if tc.ok && err != nil {
				t.Fatalf("%s: rejected: %v", tc.name, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("%s: accepted outside the 5m tolerance", tc.name)
			}
		}
	})
	t.Run("malformed headers", func(t *testing.T) {
		for _, h := range []string{"", "ts=abc;h1=00", "h1=00", fmt.Sprintf("ts=%d", ts), fmt.Sprintf("ts=%d;h1=zz", ts)} {
			if err := verifySignature(payload, h, secret, now); err == nil {
				t.Fatalf("header %q accepted", h)
			}
		}
	})
}

func TestParseWebhook(t *testing.T) {
	c := NewClient(Config{Env: EnvSandbox, WebhookSecret: "pdl_ntfset_secret"}, nil)
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	signed := func(payload string) ([]byte, string) {
		b := []byte(payload)
		return b, header(now.Unix(), sign("pdl_ntfset_secret", now.Unix(), b))
	}

	t.Run("subscription.updated is normalized", func(t *testing.T) {
		payload, h := signed(`{
			"event_id":"evt_1","event_type":"subscription.updated","occurred_at":"2026-10-04T11:59:00Z","notification_id":"ntf_1",
			"data":{"id":"sub_1","status":"past_due","customer_id":"ctm_1","custom_data":{"user_id":"user-9"},
				"current_billing_period":{"starts_at":"2025-10-01T00:00:00Z","ends_at":"2026-10-01T00:00:00Z"},
				"scheduled_change":null}}`)
		ev, err := c.ParseWebhook(payload, h, now)
		if err != nil {
			t.Fatalf("ParseWebhook: %v", err)
		}
		if ev.NotificationID != "ntf_1" || ev.EventID != "evt_1" || ev.Type != "subscription.updated" {
			t.Fatalf("envelope = %+v", ev)
		}
		if !ev.OccurredAt.Equal(time.Date(2026, 10, 4, 11, 59, 0, 0, time.UTC)) {
			t.Fatalf("OccurredAt = %v", ev.OccurredAt)
		}
		if ev.SubscriptionID != "sub_1" || ev.CustomerID != "ctm_1" || ev.UserID != "user-9" {
			t.Fatalf("ids = %+v", ev)
		}
		if ev.Status != domain.SubscriptionPastDue || ev.CancelAtPeriodEnd || ev.Ignored {
			t.Fatalf("state = %+v", ev)
		}
		if ev.CurrentPeriodEnd == nil || !ev.CurrentPeriodEnd.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
			t.Fatalf("CurrentPeriodEnd = %v", ev.CurrentPeriodEnd)
		}
	})

	t.Run("scheduled cancel sets CancelAtPeriodEnd with status active", func(t *testing.T) {
		payload, h := signed(`{"event_id":"evt_2","event_type":"subscription.updated","occurred_at":"2026-10-04T11:59:00Z","notification_id":"ntf_2",
			"data":{"id":"sub_1","status":"active","customer_id":"ctm_1","custom_data":{"user_id":"user-9"},
				"current_billing_period":{"starts_at":"2026-10-01T00:00:00Z","ends_at":"2027-10-01T00:00:00Z"},
				"scheduled_change":{"action":"cancel","effective_at":"2027-10-01T00:00:00Z"}}}`)
		ev, err := c.ParseWebhook(payload, h, now)
		if err != nil {
			t.Fatal(err)
		}
		if ev.Status != domain.SubscriptionActive || !ev.CancelAtPeriodEnd {
			t.Fatalf("state = %+v", ev)
		}
	})

	// Review Focus: canceled payloads carry null custom_data and null period.
	t.Run("subscription.canceled with null custom_data and null period", func(t *testing.T) {
		payload, h := signed(`{"event_id":"evt_3","event_type":"subscription.canceled","occurred_at":"2026-10-04T11:59:00Z","notification_id":"ntf_3",
			"data":{"id":"sub_1","status":"canceled","customer_id":"ctm_1","custom_data":null,"current_billing_period":null,"scheduled_change":null}}`)
		ev, err := c.ParseWebhook(payload, h, now)
		if err != nil {
			t.Fatal(err)
		}
		if ev.Status != domain.SubscriptionCanceled || ev.UserID != "" || ev.CurrentPeriodEnd != nil || ev.CancelAtPeriodEnd {
			t.Fatalf("state = %+v", ev)
		}
	})

	t.Run("trialing maps to active", func(t *testing.T) {
		payload, h := signed(`{"event_id":"evt_4","event_type":"subscription.trialing","occurred_at":"2026-10-04T11:59:00Z","notification_id":"ntf_4",
			"data":{"id":"sub_1","status":"trialing","customer_id":"ctm_1"}}`)
		ev, err := c.ParseWebhook(payload, h, now)
		if err != nil {
			t.Fatal(err)
		}
		if ev.Status != domain.SubscriptionActive {
			t.Fatalf("Status = %q, want active", ev.Status)
		}
	})

	t.Run("transaction.* and unknown types are Ignored but keep ids", func(t *testing.T) {
		for _, typ := range []string{"transaction.completed", "customer.updated", "something.new"} {
			payload, h := signed(`{"event_id":"evt_5","event_type":"` + typ + `","occurred_at":"2026-10-04T11:59:00Z","notification_id":"ntf_5","data":{"id":"txn_1"}}`)
			ev, err := c.ParseWebhook(payload, h, now)
			if err != nil {
				t.Fatalf("%s: %v", typ, err)
			}
			if !ev.Ignored || ev.NotificationID != "ntf_5" || ev.Status != "" {
				t.Fatalf("%s: ev = %+v, want Ignored with envelope ids only", typ, ev)
			}
		}
	})

	t.Run("bad signature never parses the body", func(t *testing.T) {
		payload := []byte(`not json at all`)
		_, err := c.ParseWebhook(payload, header(now.Unix(), "00"), now)
		if err == nil || errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want a signature error (not a validation/parse error)", err)
		}
	})

	t.Run("malformed envelope after a valid signature is ErrValidation", func(t *testing.T) {
		payload, h := signed(`{"event_type":"subscription.updated"}`)
		_, err := c.ParseWebhook(payload, h, now)
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})
}
```

- [ ] **Step 2: Run and confirm failure.** `cd backend && go test ./internal/adapter/out/paddle/ -run 'TestVerifySignature|TestParseWebhook'` — expected: `undefined: verifySignature` and the stub's `not implemented`.

- [ ] **Step 3: Delete the `ParseWebhook` stub from `payments.go` and create `backend/internal/adapter/out/paddle/webhook.go`:**

```go
package paddle

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// webhookTolerance bounds |now - ts|. 5 minutes is wider than Paddle's 5 s
// SDK default to absorb clock skew; retries carry fresh timestamps so replay
// exposure stays bounded.
const webhookTolerance = 5 * time.Minute

// ParseWebhook verifies the Paddle-Signature header and normalizes the
// envelope {event_id, event_type, occurred_at, notification_id, data}.
// Signature failures return before the body is parsed. Non-subscription
// event types come back Ignored=true with the envelope ids only.
func (c *Client) ParseWebhook(payload []byte, sigHeader string, now time.Time) (port.SubscriptionEvent, error) {
	if err := verifySignature(payload, sigHeader, c.cfg.WebhookSecret, now); err != nil {
		return port.SubscriptionEvent{}, err
	}
	var env struct {
		EventID        string          `json:"event_id"`
		EventType      string          `json:"event_type"`
		OccurredAt     time.Time       `json:"occurred_at"`
		NotificationID string          `json:"notification_id"`
		Data           json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return port.SubscriptionEvent{}, fmt.Errorf("%w: paddle: decode webhook envelope: %v", domain.ErrValidation, err)
	}
	if env.EventID == "" || env.NotificationID == "" || env.EventType == "" {
		return port.SubscriptionEvent{}, fmt.Errorf("%w: paddle: webhook envelope missing event_id, notification_id or event_type", domain.ErrValidation)
	}
	out := port.SubscriptionEvent{
		NotificationID: env.NotificationID,
		EventID:        env.EventID,
		Type:           env.EventType,
		OccurredAt:     env.OccurredAt.UTC(),
	}
	if !strings.HasPrefix(env.EventType, "subscription.") {
		out.Ignored = true
		return out, nil
	}
	var sub subscription
	if err := json.Unmarshal(env.Data, &sub); err != nil {
		return port.SubscriptionEvent{}, fmt.Errorf("%w: paddle: decode subscription data: %v", domain.ErrValidation, err)
	}
	return normalizeSubscription(sub, out), nil
}

// verifySignature implements Paddle-Signature: `ts=<unix>;h1=<hex>[;h1=<hex>]`,
// HMAC-SHA256(secret, "<ts>:<raw body>") hex, constant-time compare, any h1
// may match (key rotation). An empty secret is refused outright so a
// misconfigured deployment can never accept forged events.
func verifySignature(payload []byte, header, secret string, now time.Time) error {
	if secret == "" {
		return errors.New("paddle: webhook secret is empty; refusing to verify")
	}
	if header == "" {
		return errors.New("paddle: missing Paddle-Signature header")
	}
	var ts int64 = -1
	var candidates [][]byte
	for _, part := range strings.Split(header, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "ts":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return fmt.Errorf("paddle: bad signature timestamp %q", v)
			}
			ts = n
		case "h1":
			sig, err := hex.DecodeString(v)
			if err == nil && len(sig) > 0 {
				candidates = append(candidates, sig)
			}
		}
	}
	if ts < 0 || len(candidates) == 0 {
		return errors.New("paddle: malformed Paddle-Signature header")
	}
	if drift := now.Sub(time.Unix(ts, 0)); drift > webhookTolerance || drift < -webhookTolerance {
		return fmt.Errorf("paddle: webhook timestamp outside %s tolerance", webhookTolerance)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10)))
	mac.Write([]byte(":"))
	mac.Write(payload)
	expected := mac.Sum(nil)
	for _, sig := range candidates {
		if hmac.Equal(expected, sig) {
			return nil
		}
	}
	return errors.New("paddle: no matching h1 signature")
}
```

- [ ] **Step 4: Run the whole adapter package and lint it.** `cd backend && go test ./internal/adapter/out/paddle/... && go vet ./internal/adapter/out/paddle/... && golangci-lint run ./internal/adapter/out/paddle/...` — expected: `ok`, no vet or lint findings; `grep -rn '"github.com' internal/adapter/out/paddle` prints nothing (stdlib only).

- [ ] **Step 5: Commit.**

```bash
git add backend/internal/adapter/out/paddle
git commit -m "feat(paddle): Paddle-Signature verification (ts/h1, 5m tolerance, empty-secret refusal) and envelope parsing" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 8: Service — billing fakes, `BillingService` skeleton, trial grant in `GetSubscription`, time-based entitlement with typed 402

**Files:**
- Modify: `backend/internal/service/fakes_test.go` (replace the `fakeSubscriptionRepo` section ~L68–101, the `fakeStripeEventRepo` section ~L1062–1081, the `fakePayments` section ~L1437–1487)
- Modify: `backend/internal/service/billing.go` (whole file)
- Modify: `backend/internal/service/billing_test.go` (whole file — every Stripe test is deleted)
- Modify: `backend/internal/service/selfhost_test.go` (`TestBillingSelfHost`)
- Modify: `backend/internal/service/service.go` (`entitlement.require`, the `entitlement` doc comment)

**Interfaces:**
- Consumes: Task 1 domain, Task 2 ports.
- Produces: `service.BillingServiceDeps{Users port.UserRepo; Subs port.SubscriptionRepo; Events port.BillingEventRepo; Payments port.Payments; Clock port.Clock; Tx port.TxRunner; SelfHosted bool; Logger *slog.Logger}`; `func service.NewBillingService(d BillingServiceDeps) *BillingService`; `(*BillingService) GetSubscription`, `RequireActive`; stubs for `CreateCheckout`, `CreatePortalSession`, `HandleWebhook`, `ReconcileSubscriptions` (replaced in Tasks 9–11); test fakes `newSubscriptionRepo() *fakeSubscriptionRepo` (fields `byUser`, `byCustomer`, `ensureTrialCalls`, `upsertCalls`), `newBillingEventRepo() *fakeBillingEventRepo` (field `seen map[string]port.SubscriptionEvent`), `newPayments() *fakePayments` (programmable `customerID, ensureErr, checkoutURL, checkoutErr, portalURLs, portalErr, webhookEvent, parseWebhookErr, getSubEvent, getSubErr, cancelErr`; recorded `ensureCustomerCalls, lastEnsureUser, lastCheckoutParams, lastPortalCustomerID, lastPortalSubscriptionID, lastParseNow, getSubCalls, lastGetSubID, cancelCalls, lastCancelID, lastCancelImmediately`).

- [ ] **Step 1: Rewrite the three fake sections in `fakes_test.go`.** Replace the subscription-repo section with:

```go
// --- subscription repo -------------------------------------------------------

// fakeSubscriptionRepo indexes every upserted row by BOTH UserID and
// BillingCustomerID so GetByUserID / GetByBillingCustomerID stay consistent.
// Upsert mirrors the Postgres COALESCE: empty billing ids never clobber
// stored ones. EnsureTrial is idempotent and counts calls.
type fakeSubscriptionRepo struct {
	byUser           map[string]domain.Subscription
	byCustomer       map[string]domain.Subscription
	ensureTrialCalls int
	upsertCalls      int
}

func newSubscriptionRepo() *fakeSubscriptionRepo {
	return &fakeSubscriptionRepo{
		byUser:     map[string]domain.Subscription{},
		byCustomer: map[string]domain.Subscription{},
	}
}

func (r *fakeSubscriptionRepo) GetByUserID(_ context.Context, userID string) (domain.Subscription, error) {
	s, ok := r.byUser[userID]
	if !ok {
		return domain.Subscription{}, domain.ErrNotFound
	}
	return s, nil
}

func (r *fakeSubscriptionRepo) GetByBillingCustomerID(_ context.Context, customerID string) (domain.Subscription, error) {
	s, ok := r.byCustomer[customerID]
	if !ok {
		return domain.Subscription{}, domain.ErrNotFound
	}
	return s, nil
}

func (r *fakeSubscriptionRepo) Upsert(_ context.Context, s domain.Subscription) error {
	r.upsertCalls++
	if prev, ok := r.byUser[s.UserID]; ok {
		if s.BillingCustomerID == "" {
			s.BillingCustomerID = prev.BillingCustomerID
		}
		if s.BillingSubscriptionID == "" {
			s.BillingSubscriptionID = prev.BillingSubscriptionID
		}
	}
	r.byUser[s.UserID] = s
	if s.BillingCustomerID != "" {
		r.byCustomer[s.BillingCustomerID] = s
	}
	return nil
}

func (r *fakeSubscriptionRepo) EnsureTrial(_ context.Context, userID string, trialEndsAt time.Time) error {
	r.ensureTrialCalls++
	if _, ok := r.byUser[userID]; ok {
		return nil
	}
	end := trialEndsAt
	r.byUser[userID] = domain.Subscription{
		UserID:      userID,
		Status:      domain.SubscriptionTrialing,
		Plan:        domain.PlanAnnual,
		PriceUSD:    domain.PriceUSDAnnual,
		TrialEndsAt: &end,
	}
	return nil
}

func (r *fakeSubscriptionRepo) ListForReconciliation(_ context.Context, now time.Time) ([]domain.Subscription, error) {
	var out []domain.Subscription
	for _, s := range r.byUser {
		if s.BillingSubscriptionID == "" {
			continue
		}
		stale := s.LastEventAt != nil && s.LastEventAt.Before(now.Add(-7*24*time.Hour))
		lapsed := false
		switch s.Status {
		case domain.SubscriptionActive, domain.SubscriptionPastDue, domain.SubscriptionPaused:
			lapsed = s.CurrentPeriodEnd != nil && s.CurrentPeriodEnd.Before(now.Add(-time.Hour))
		}
		if stale || lapsed {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UserID < out[j].UserID })
	return out, nil
}

var _ port.SubscriptionRepo = (*fakeSubscriptionRepo)(nil)
```

Replace the `fakeStripeEventRepo` section with:

```go
// --- billing-event repo ------------------------------------------------------

// fakeBillingEventRepo is set-backed by notification id: Record returns
// first=true the first time an id is seen, false on every repeat.
type fakeBillingEventRepo struct {
	seen map[string]port.SubscriptionEvent
}

func newBillingEventRepo() *fakeBillingEventRepo {
	return &fakeBillingEventRepo{seen: map[string]port.SubscriptionEvent{}}
}

func (r *fakeBillingEventRepo) Record(_ context.Context, ev port.SubscriptionEvent) (bool, error) {
	if _, ok := r.seen[ev.NotificationID]; ok {
		return false, nil
	}
	r.seen[ev.NotificationID] = ev
	return true, nil
}

var _ port.BillingEventRepo = (*fakeBillingEventRepo)(nil)
```

Replace the `fakePayments` section with:

```go
// --- payments ----------------------------------------------------------------

// fakePayments serves programmable results for every port.Payments
// operation and records the arguments billing.go passes.
type fakePayments struct {
	// programmable
	customerID      string
	ensureErr       error
	checkoutURL     string
	checkoutErr     error
	portalURLs      port.PortalURLs
	portalErr       error
	webhookEvent    port.SubscriptionEvent
	parseWebhookErr error
	getSubEvent     port.SubscriptionEvent
	getSubErr       error
	cancelErr       error

	// recording
	ensureCustomerCalls      int
	lastEnsureUser           domain.User
	lastCheckoutParams       port.CheckoutParams
	lastPortalCustomerID     string
	lastPortalSubscriptionID string
	lastParseNow             time.Time
	getSubCalls              int
	lastGetSubID             string
	cancelCalls              int
	lastCancelID             string
	lastCancelImmediately    bool
}

func newPayments() *fakePayments { return &fakePayments{} }

func (p *fakePayments) EnsureCustomer(_ context.Context, user domain.User) (string, error) {
	p.ensureCustomerCalls++
	p.lastEnsureUser = user
	return p.customerID, p.ensureErr
}

func (p *fakePayments) CreateCheckout(_ context.Context, params port.CheckoutParams) (string, error) {
	p.lastCheckoutParams = params
	return p.checkoutURL, p.checkoutErr
}

func (p *fakePayments) CreatePortalSession(_ context.Context, customerID, subscriptionID string) (port.PortalURLs, error) {
	p.lastPortalCustomerID = customerID
	p.lastPortalSubscriptionID = subscriptionID
	return p.portalURLs, p.portalErr
}

func (p *fakePayments) ParseWebhook(_ []byte, _ string, now time.Time) (port.SubscriptionEvent, error) {
	p.lastParseNow = now
	return p.webhookEvent, p.parseWebhookErr
}

func (p *fakePayments) GetSubscription(_ context.Context, subscriptionID string) (port.SubscriptionEvent, error) {
	p.getSubCalls++
	p.lastGetSubID = subscriptionID
	return p.getSubEvent, p.getSubErr
}

func (p *fakePayments) CancelSubscription(_ context.Context, subscriptionID string, immediately bool) error {
	p.cancelCalls++
	p.lastCancelID = subscriptionID
	p.lastCancelImmediately = immediately
	return p.cancelErr
}

var _ port.Payments = (*fakePayments)(nil)
```

- [ ] **Step 2: Replace `backend/internal/service/billing_test.go` entirely with the Task 8 tests:**

```go
package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// This file covers the paid (SelfHosted=false) Paddle flow in billing.go.
// Self-host bypass is covered by selfhost_test.go.

type billingHarness struct {
	users    *fakeUserRepo
	subs     *fakeSubscriptionRepo
	events   *fakeBillingEventRepo
	payments *fakePayments
	clock    *fakeClock
	tx       *fakeTxRunner
	svc      *BillingService
}

func newBillingHarness(now time.Time) *billingHarness {
	h := &billingHarness{
		users:    newUserRepo(),
		subs:     newSubscriptionRepo(),
		events:   newBillingEventRepo(),
		payments: newPayments(),
		clock:    newClock(now),
		tx:       newTxRunner(),
	}
	h.svc = NewBillingService(BillingServiceDeps{
		Users: h.users, Subs: h.subs, Events: h.events, Payments: h.payments,
		Clock: h.clock, Tx: h.tx, SelfHosted: false,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return h
}

func (h *billingHarness) seedUser(t *testing.T, id string, createdAt time.Time) {
	t.Helper()
	if _, err := h.users.Upsert(context.Background(), domain.User{ID: id, Email: id + "@example.com", CreatedAt: createdAt}); err != nil {
		t.Fatal(err)
	}
}

func (h *billingHarness) seedSub(t *testing.T, s domain.Subscription) {
	t.Helper()
	if err := h.subs.Upsert(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	h.subs.upsertCalls = 0
}

func tptr(v time.Time) *time.Time { return &v }

// --- GetSubscription: trial grant -------------------------------------------

func TestGetSubscriptionGrantsTrialAnchoredToSignup(t *testing.T) {
	ctx := context.Background()
	signup := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	h := newBillingHarness(signup.Add(3 * time.Hour)) // first call hours after signup
	h.seedUser(t, "u1", signup)

	sub, err := h.svc.GetSubscription(ctx, "u1")
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}
	if sub.Status != domain.SubscriptionTrialing {
		t.Fatalf("status = %q, want trialing", sub.Status)
	}
	want := signup.Add(domain.TrialLength)
	if sub.TrialEndsAt == nil || !sub.TrialEndsAt.Equal(want) {
		t.Fatalf("TrialEndsAt = %v, want created_at+14d = %v (anchored to signup, not to the call)", sub.TrialEndsAt, want)
	}
	if sub.UserID != "u1" || sub.Plan != domain.PlanAnnual || sub.PriceUSD != domain.PriceUSDAnnual {
		t.Fatalf("row = %+v", sub)
	}
	if h.subs.ensureTrialCalls != 1 {
		t.Fatalf("ensureTrialCalls = %d, want 1", h.subs.ensureTrialCalls)
	}
	if _, err := h.svc.GetSubscription(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if h.subs.ensureTrialCalls != 1 {
		t.Fatalf("second read must not call EnsureTrial again, calls = %d", h.subs.ensureTrialCalls)
	}
}

func TestGetSubscriptionDoesNotRegrantAnExistingRow(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	h.seedUser(t, "u1", now.Add(-30*24*time.Hour))
	h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionCanceled, BillingCustomerID: "ctm_1"})

	sub, err := h.svc.GetSubscription(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if sub.Status != domain.SubscriptionCanceled || sub.TrialEndsAt != nil {
		t.Fatalf("existing row must be returned unmodified: %+v", sub)
	}
	if h.subs.ensureTrialCalls != 0 {
		t.Fatalf("EnsureTrial must not run for an existing row")
	}
}

func TestGetSubscriptionUnknownUserPropagatesNotFound(t *testing.T) {
	h := newBillingHarness(time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC))
	if _, err := h.svc.GetSubscription(context.Background(), "ghost"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound (no users row to anchor the trial)", err)
	}
	if h.subs.ensureTrialCalls != 0 {
		t.Fatal("EnsureTrial must not run without a users row")
	}
}

// --- RequireActive: entitlement matrix with typed 402 -----------------------

func TestRequireActiveMatrix(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	tests := []struct {
		name   string
		seed   *domain.Subscription // nil = no row
		want   domain.DenialReason  // "" = access granted
		wantTE *time.Time
		wantPE *time.Time
	}{
		{"no row", nil, domain.DenialNone, nil, nil},
		{"trialing in trial", &domain.Subscription{Status: domain.SubscriptionTrialing, TrialEndsAt: tptr(future)}, "", nil, nil},
		{"trialing ended", &domain.Subscription{Status: domain.SubscriptionTrialing, TrialEndsAt: tptr(now.Add(-time.Minute))}, domain.DenialTrialEnded, tptr(now.Add(-time.Minute)), nil},
		{"active no period", &domain.Subscription{Status: domain.SubscriptionActive}, "", nil, nil},
		{"active past grace", &domain.Subscription{Status: domain.SubscriptionActive, CurrentPeriodEnd: tptr(now.Add(-domain.ActiveGrace))}, domain.DenialPastDue, nil, tptr(now.Add(-domain.ActiveGrace))},
		{"past_due in grace", &domain.Subscription{Status: domain.SubscriptionPastDue, CurrentPeriodEnd: tptr(now)}, "", nil, nil},
		{"past_due beyond grace", &domain.Subscription{Status: domain.SubscriptionPastDue, CurrentPeriodEnd: tptr(now.Add(-domain.PastDueGrace))}, domain.DenialPastDue, nil, tptr(now.Add(-domain.PastDueGrace))},
		{"paused", &domain.Subscription{Status: domain.SubscriptionPaused, CurrentPeriodEnd: tptr(future)}, domain.DenialPaused, nil, tptr(future)},
		{"canceled", &domain.Subscription{Status: domain.SubscriptionCanceled}, domain.DenialCanceled, nil, nil},
		{"none", &domain.Subscription{Status: domain.SubscriptionNone}, domain.DenialNone, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newBillingHarness(now)
			if tt.seed != nil {
				s := *tt.seed
				s.UserID = "u1"
				h.seedSub(t, s)
			}
			err := h.svc.RequireActive(context.Background(), "u1")
			if tt.want == "" {
				if err != nil {
					t.Fatalf("RequireActive = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, domain.ErrPaymentRequired) {
				t.Fatalf("err = %v, want ErrPaymentRequired", err)
			}
			var pr *domain.PaymentRequiredError
			if !errors.As(err, &pr) {
				t.Fatalf("err = %v, want *domain.PaymentRequiredError", err)
			}
			if pr.Reason != tt.want {
				t.Fatalf("Reason = %q, want %q", pr.Reason, tt.want)
			}
			if (pr.TrialEndsAt == nil) != (tt.wantTE == nil) || (pr.TrialEndsAt != nil && !pr.TrialEndsAt.Equal(*tt.wantTE)) {
				t.Fatalf("TrialEndsAt = %v, want %v", pr.TrialEndsAt, tt.wantTE)
			}
			if (pr.CurrentPeriodEnd == nil) != (tt.wantPE == nil) || (pr.CurrentPeriodEnd != nil && !pr.CurrentPeriodEnd.Equal(*tt.wantPE)) {
				t.Fatalf("CurrentPeriodEnd = %v, want %v", pr.CurrentPeriodEnd, tt.wantPE)
			}
		})
	}
}

// Keep the port import used by later tasks' tests in this file.
var _ port.SubscriptionEvent
```

- [ ] **Step 3: Update `selfhost_test.go`'s `TestBillingSelfHost`** to the new API:

```go
func TestBillingSelfHost(t *testing.T) {
	ctx := context.Background()
	b := NewBillingService(BillingServiceDeps{Clock: SystemClock{}, SelfHosted: true})

	sub, err := b.GetSubscription(ctx, "u1")
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}
	if sub.Status != domain.SubscriptionActive {
		t.Fatalf("status = %q, want %q", sub.Status, domain.SubscriptionActive)
	}
	if sub.Plan != domain.PlanAnnual {
		t.Fatalf("plan = %q, want %q", sub.Plan, domain.PlanAnnual)
	}
	if !sub.HasAccess(time.Now()) {
		t.Fatalf("self-host subscription should grant access")
	}

	if _, err := b.CreateCheckout(ctx, "u1"); !errors.Is(err, domain.ErrSelfHosted) {
		t.Fatalf("checkout: expected ErrSelfHosted, got %v", err)
	}
	if _, err := b.CreatePortalSession(ctx, "u1"); !errors.Is(err, domain.ErrSelfHosted) {
		t.Fatalf("portal: expected ErrSelfHosted, got %v", err)
	}
	if err := b.HandleWebhook(ctx, nil, ""); !errors.Is(err, domain.ErrSelfHosted) {
		t.Fatalf("webhook: expected ErrSelfHosted, got %v", err)
	}
	if err := b.ReconcileSubscriptions(ctx); err != nil {
		t.Fatalf("reconcile must be a silent no-op on self-host, got %v", err)
	}
	if err := b.RequireActive(ctx, "u1"); err != nil {
		t.Fatalf("RequireActive self-host: %v", err)
	}
}
```

Update its doc comment to `every billing operation returns domain.ErrSelfHosted` (drop "Stripe").

- [ ] **Step 4: Run and confirm failure.** `cd backend && go test ./internal/service/ -run 'TestGetSubscription|TestRequireActive|TestBillingSelfHost'` — expected: `undefined: BillingServiceDeps`, `undefined: newBillingEventRepo`.

- [ ] **Step 5: Replace `backend/internal/service/billing.go` with:**

```go
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// inlineReconcileEvery throttles the best-effort reconcile performed by
// GetSubscription for an active row past its period end (per user, in
// memory, per process).
const inlineReconcileEvery = 10 * time.Minute

// BillingServiceDeps wires BillingService (docs/payments.md).
type BillingServiceDeps struct {
	Users    port.UserRepo
	Subs     port.SubscriptionRepo
	Events   port.BillingEventRepo
	Payments port.Payments
	Clock    port.Clock
	Tx       port.TxRunner
	// SelfHosted disables billing entirely: GetSubscription reports an
	// active annual plan and checkout/portal/webhook return
	// domain.ErrSelfHosted (open-core self-hosted mode).
	SelfHosted bool
	Logger     *slog.Logger // optional; defaults to slog.Default()
}

// BillingService implements port.BillingService: the $50/yr Paddle flow
// where webhooks drive all state and reconciliation covers lost webhooks.
type BillingService struct {
	users      port.UserRepo
	subs       port.SubscriptionRepo
	events     port.BillingEventRepo
	payments   port.Payments
	clock      port.Clock
	tx         port.TxRunner
	selfHosted bool
	logger     *slog.Logger

	mu         sync.Mutex
	lastInline map[string]time.Time // user id -> last inline reconcile attempt
}

var _ port.BillingService = (*BillingService)(nil)

func NewBillingService(d BillingServiceDeps) *BillingService {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &BillingService{
		users: d.Users, subs: d.Subs, events: d.Events, payments: d.Payments,
		clock: d.Clock, tx: d.Tx, selfHosted: d.SelfHosted, logger: d.Logger,
		lastInline: map[string]time.Time{},
	}
}

func selfHostedSubscription(userID string) domain.Subscription {
	// No biller on self-hosted instances: report an active annual plan (nil
	// period) so clients treat the user as fully entitled.
	return domain.Subscription{
		UserID:   userID,
		Status:   domain.SubscriptionActive,
		Plan:     domain.PlanAnnual,
		PriceUSD: domain.PriceUSDAnnual,
	}
}

// GetSubscription returns the user's row, granting the signup trial when
// none exists yet. (Task 11 adds the inline reconcile.)
func (s *BillingService) GetSubscription(ctx context.Context, userID string) (domain.Subscription, error) {
	if s.selfHosted {
		return selfHostedSubscription(userID), nil
	}
	sub, err := s.subs.GetByUserID(ctx, userID)
	if errors.Is(err, domain.ErrNotFound) {
		sub, err = s.grantTrial(ctx, userID)
	}
	if err != nil {
		return domain.Subscription{}, err
	}
	return sub, nil
}

// grantTrial anchors the 14-day trial to users.created_at (signup, since
// the users row is provisioned on the first authenticated call). EnsureTrial
// is ON CONFLICT DO NOTHING, so concurrent first calls both read back the
// same row.
func (s *BillingService) grantTrial(ctx context.Context, userID string) (domain.Subscription, error) {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return domain.Subscription{}, err
	}
	if err := s.subs.EnsureTrial(ctx, userID, user.CreatedAt.Add(domain.TrialLength)); err != nil {
		return domain.Subscription{}, err
	}
	return s.subs.GetByUserID(ctx, userID)
}

// Implemented in Task 9.
func (s *BillingService) CreateCheckout(ctx context.Context, userID string) (string, error) {
	if s.selfHosted {
		return "", fmt.Errorf("%w: billing is disabled on self-hosted instances", domain.ErrSelfHosted)
	}
	return "", domain.ErrNotImplemented
}

// Implemented in Task 9.
func (s *BillingService) CreatePortalSession(ctx context.Context, userID string) (port.PortalURLs, error) {
	if s.selfHosted {
		return port.PortalURLs{}, fmt.Errorf("%w: billing is disabled on self-hosted instances", domain.ErrSelfHosted)
	}
	return port.PortalURLs{}, domain.ErrNotImplemented
}

// Implemented in Task 10.
func (s *BillingService) HandleWebhook(ctx context.Context, payload []byte, sigHeader string) error {
	if s.selfHosted {
		return fmt.Errorf("%w: billing is disabled on self-hosted instances", domain.ErrSelfHosted)
	}
	return domain.ErrNotImplemented
}

// Implemented in Task 11.
func (s *BillingService) ReconcileSubscriptions(ctx context.Context) error {
	if s.selfHosted {
		return nil
	}
	return domain.ErrNotImplemented
}

func (s *BillingService) RequireActive(ctx context.Context, userID string) error {
	return entitlement{subs: s.subs, clock: s.clock, selfHost: s.selfHosted}.require(ctx, userID)
}
```

- [ ] **Step 6: Update `entitlement.require` in `service.go`:**

```go
// entitlement enforces the subscription paywall shared by all gated
// use-cases (docs/payments.md): access is decided by
// domain.Subscription.HasAccess at the current time; a denial is a
// *domain.PaymentRequiredError carrying the 402 details.
type entitlement struct {
	subs  port.SubscriptionRepo
	clock port.Clock
	// selfHost unlocks every gated use-case for open-core self-hosted
	// deployments (SELF_HOSTED=true): require always succeeds and never
	// touches the subscription repo.
	selfHost bool
}

func (e entitlement) require(ctx context.Context, userID string) error {
	if e.selfHost {
		return nil
	}
	sub, err := e.subs.GetByUserID(ctx, userID)
	if errors.Is(err, domain.ErrNotFound) {
		return &domain.PaymentRequiredError{Reason: domain.DenialNone}
	}
	if err != nil {
		return err
	}
	if now := e.clock.Now(); !sub.HasAccess(now) {
		return domain.NewPaymentRequiredError(sub, now)
	}
	return nil
}
```

- [ ] **Step 7: Run the service suite.** `cd backend && go test ./internal/service/...` — expected: `ok` (every other service's entitlement tests still pass via `errors.Is(err, domain.ErrPaymentRequired)`).

- [ ] **Step 8: Commit.**

```bash
git add backend/internal/service
git commit -m "feat(service): Paddle billing fakes, signup-anchored trial grant and time-based entitlement with typed 402" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 9: Service — checkout (409 `already_subscribed`) and portal (400 `no_billing_profile`), 502 mapping

**Files:**
- Modify: `backend/internal/service/billing.go` (replace the two Task 8 stubs; add `billingUnavailable`)
- Modify: `backend/internal/service/billing_test.go` (append)

**Interfaces:**
- Produces: `(*BillingService) CreateCheckout(ctx, userID) (string, error)`; `(*BillingService) CreatePortalSession(ctx, userID) (port.PortalURLs, error)`; unexported `func billingUnavailable(op string, err error) error` (wraps `domain.ErrBillingUnavailable`).

- [ ] **Step 1: Append the failing tests:**

```go
// --- CreateCheckout -----------------------------------------------------------

func TestCreateCheckoutFirstTimeCreatesCustomerAndTrialRow(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	h.seedUser(t, "u1", now.Add(-time.Hour))
	h.payments.customerID = "ctm_new"
	h.payments.checkoutURL = "https://app/checkout?_ptxn=txn_1"

	url, err := h.svc.CreateCheckout(ctx, "u1")
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	if url != "https://app/checkout?_ptxn=txn_1" {
		t.Fatalf("url = %q", url)
	}
	if h.payments.ensureCustomerCalls != 1 || h.payments.lastEnsureUser.ID != "u1" {
		t.Fatalf("EnsureCustomer calls = %d (user %q), want 1 for u1", h.payments.ensureCustomerCalls, h.payments.lastEnsureUser.ID)
	}
	if h.payments.lastCheckoutParams != (port.CheckoutParams{UserID: "u1", CustomerID: "ctm_new"}) {
		t.Fatalf("CheckoutParams = %+v", h.payments.lastCheckoutParams)
	}
	row, err := h.subs.GetByUserID(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != domain.SubscriptionTrialing || row.BillingCustomerID != "ctm_new" {
		t.Fatalf("row = %+v, want trialing with the customer id persisted", row)
	}
}

func TestCreateCheckoutReusesStoredCustomer(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	h.seedUser(t, "u1", now)
	h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionTrialing, TrialEndsAt: tptr(now.Add(24 * time.Hour)), BillingCustomerID: "ctm_old"})
	h.payments.checkoutURL = "https://co"

	if _, err := h.svc.CreateCheckout(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if h.payments.ensureCustomerCalls != 0 {
		t.Fatalf("EnsureCustomer must not run when a customer id is stored")
	}
	if h.payments.lastCheckoutParams.CustomerID != "ctm_old" {
		t.Fatalf("CustomerID = %q, want ctm_old", h.payments.lastCheckoutParams.CustomerID)
	}
}

func TestCreateCheckoutRefusesLiveSubscriptions(t *testing.T) {
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	for _, status := range []domain.SubscriptionStatus{domain.SubscriptionActive, domain.SubscriptionPastDue, domain.SubscriptionPaused} {
		t.Run(string(status), func(t *testing.T) {
			h := newBillingHarness(now)
			h.seedUser(t, "u1", now)
			h.seedSub(t, domain.Subscription{UserID: "u1", Status: status, BillingCustomerID: "ctm_1", BillingSubscriptionID: "sub_1"})
			_, err := h.svc.CreateCheckout(context.Background(), "u1")
			if !errors.Is(err, domain.ErrAlreadySubscribed) {
				t.Fatalf("err = %v, want ErrAlreadySubscribed", err)
			}
			if h.payments.ensureCustomerCalls != 0 || h.payments.lastCheckoutParams != (port.CheckoutParams{}) {
				t.Fatal("no provider call may happen for an already-subscribed user")
			}
		})
	}
	t.Run("canceled with an old subscription id may resubscribe", func(t *testing.T) {
		h := newBillingHarness(now)
		h.seedUser(t, "u1", now)
		h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionCanceled, BillingCustomerID: "ctm_1", BillingSubscriptionID: "sub_old"})
		h.payments.checkoutURL = "https://co"
		if _, err := h.svc.CreateCheckout(context.Background(), "u1"); err != nil {
			t.Fatalf("canceled user must be able to resubscribe: %v", err)
		}
	})
}

func TestCreateCheckoutMapsProviderFailuresTo502(t *testing.T) {
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	t.Run("EnsureCustomer fails", func(t *testing.T) {
		h := newBillingHarness(now)
		h.seedUser(t, "u1", now)
		h.payments.ensureErr = errors.New("paddle: http 500")
		_, err := h.svc.CreateCheckout(context.Background(), "u1")
		if !errors.Is(err, domain.ErrBillingUnavailable) {
			t.Fatalf("err = %v, want ErrBillingUnavailable", err)
		}
	})
	t.Run("CreateCheckout fails", func(t *testing.T) {
		h := newBillingHarness(now)
		h.seedUser(t, "u1", now)
		h.payments.customerID = "ctm_1"
		h.payments.checkoutErr = errors.New("paddle: http 503")
		_, err := h.svc.CreateCheckout(context.Background(), "u1")
		if !errors.Is(err, domain.ErrBillingUnavailable) {
			t.Fatalf("err = %v, want ErrBillingUnavailable", err)
		}
	})
}

// --- CreatePortalSession ----------------------------------------------------

func TestCreatePortalSession(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	full := port.PortalURLs{Overview: "https://p/o", Cancel: "https://p/c", UpdatePayment: "https://p/u"}

	t.Run("no row is no_billing_profile", func(t *testing.T) {
		h := newBillingHarness(now)
		_, err := h.svc.CreatePortalSession(ctx, "u1")
		if !errors.Is(err, domain.ErrNoBillingProfile) {
			t.Fatalf("err = %v, want ErrNoBillingProfile", err)
		}
	})
	t.Run("row without customer is no_billing_profile", func(t *testing.T) {
		h := newBillingHarness(now)
		h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionTrialing})
		_, err := h.svc.CreatePortalSession(ctx, "u1")
		if !errors.Is(err, domain.ErrNoBillingProfile) {
			t.Fatalf("err = %v, want ErrNoBillingProfile", err)
		}
		if h.payments.lastPortalCustomerID != "" {
			t.Fatal("provider must not be called")
		}
	})
	t.Run("customer without subscription returns overview only", func(t *testing.T) {
		h := newBillingHarness(now)
		h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionTrialing, BillingCustomerID: "ctm_1"})
		h.payments.portalURLs = full // provider might echo links; service blanks them
		urls, err := h.svc.CreatePortalSession(ctx, "u1")
		if err != nil {
			t.Fatal(err)
		}
		if h.payments.lastPortalCustomerID != "ctm_1" || h.payments.lastPortalSubscriptionID != "" {
			t.Fatalf("provider args = %q/%q", h.payments.lastPortalCustomerID, h.payments.lastPortalSubscriptionID)
		}
		if urls != (port.PortalURLs{Overview: "https://p/o"}) {
			t.Fatalf("urls = %+v, want cancel/update blanked without a subscription id", urls)
		}
	})
	t.Run("customer with subscription passes every link through", func(t *testing.T) {
		h := newBillingHarness(now)
		h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive, BillingCustomerID: "ctm_1", BillingSubscriptionID: "sub_1"})
		h.payments.portalURLs = full
		urls, err := h.svc.CreatePortalSession(ctx, "u1")
		if err != nil {
			t.Fatal(err)
		}
		if h.payments.lastPortalSubscriptionID != "sub_1" || urls != full {
			t.Fatalf("urls = %+v (sub id %q)", urls, h.payments.lastPortalSubscriptionID)
		}
	})
	t.Run("provider failure is 502", func(t *testing.T) {
		h := newBillingHarness(now)
		h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive, BillingCustomerID: "ctm_1"})
		h.payments.portalErr = errors.New("paddle: http 500")
		_, err := h.svc.CreatePortalSession(ctx, "u1")
		if !errors.Is(err, domain.ErrBillingUnavailable) {
			t.Fatalf("err = %v, want ErrBillingUnavailable", err)
		}
	})
}
```

- [ ] **Step 2: Run and confirm failure.** `cd backend && go test ./internal/service/ -run 'TestCreateCheckout|TestCreatePortalSession'` — expected: failures with `not implemented`.

- [ ] **Step 3: Replace the two stubs in `billing.go` with:**

```go
// CreateCheckout starts the hosted checkout: grants the trial row if
// missing, refuses a second live subscription, ensures the provider
// customer (persisting its id), then creates the transaction. No URLs are
// accepted from clients; Paddle returns the checkout url.
func (s *BillingService) CreateCheckout(ctx context.Context, userID string) (string, error) {
	if s.selfHosted {
		return "", fmt.Errorf("%w: billing is disabled on self-hosted instances", domain.ErrSelfHosted)
	}
	sub, err := s.GetSubscription(ctx, userID)
	if err != nil {
		return "", err
	}
	if sub.BillingSubscriptionID != "" {
		switch sub.Status {
		case domain.SubscriptionActive, domain.SubscriptionPastDue, domain.SubscriptionPaused:
			return "", fmt.Errorf("%w: subscription %s is %s", domain.ErrAlreadySubscribed, sub.BillingSubscriptionID, sub.Status)
		}
	}
	if sub.BillingCustomerID == "" {
		user, err := s.users.GetByID(ctx, userID)
		if err != nil {
			return "", err
		}
		customerID, err := s.payments.EnsureCustomer(ctx, user)
		if err != nil {
			return "", billingUnavailable("ensure customer", err)
		}
		sub.BillingCustomerID = customerID
		if err := s.subs.Upsert(ctx, sub); err != nil {
			return "", err
		}
	}
	url, err := s.payments.CreateCheckout(ctx, port.CheckoutParams{UserID: userID, CustomerID: sub.BillingCustomerID})
	if err != nil {
		return "", billingUnavailable("create checkout", err)
	}
	return url, nil
}

// CreatePortalSession returns temporary portal links. Cancellation and
// payment-method updates happen in Paddle's portal and flow back by webhook.
func (s *BillingService) CreatePortalSession(ctx context.Context, userID string) (port.PortalURLs, error) {
	if s.selfHosted {
		return port.PortalURLs{}, fmt.Errorf("%w: billing is disabled on self-hosted instances", domain.ErrSelfHosted)
	}
	sub, err := s.subs.GetByUserID(ctx, userID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && sub.BillingCustomerID == "") {
		return port.PortalURLs{}, fmt.Errorf("%w: no billing profile yet; start a checkout first", domain.ErrNoBillingProfile)
	}
	if err != nil {
		return port.PortalURLs{}, err
	}
	urls, err := s.payments.CreatePortalSession(ctx, sub.BillingCustomerID, sub.BillingSubscriptionID)
	if err != nil {
		return port.PortalURLs{}, billingUnavailable("create portal session", err)
	}
	if sub.BillingSubscriptionID == "" {
		urls.Cancel, urls.UpdatePayment = "", ""
	}
	return urls, nil
}

// billingUnavailable wraps a provider failure for the HTTP layer (502).
func billingUnavailable(op string, err error) error {
	return fmt.Errorf("%w: %s: %v", domain.ErrBillingUnavailable, op, err)
}
```

- [ ] **Step 4: Run.** `cd backend && go test ./internal/service/...` — expected: `ok`.

- [ ] **Step 5: Commit.**

```bash
git add backend/internal/service
git commit -m "feat(service): Paddle checkout with already-subscribed guard and portal links with no-billing-profile guard" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 10: Service — webhook pipeline (signature → ignore → record → resolve → order → upsert)

**Files:**
- Modify: `backend/internal/service/billing.go` (replace the `HandleWebhook` stub; add `applyEvent`)
- Modify: `backend/internal/service/billing_test.go` (append)

**Interfaces:**
- Produces: `(*BillingService) HandleWebhook(ctx, payload []byte, sigHeader string) error`; unexported `(*BillingService) applyEvent(ctx, ev port.SubscriptionEvent, force bool) error` (reused by Task 11's reconciliation with `force=true`).

- [ ] **Step 1: Append the failing tests:**

```go
// --- HandleWebhook ----------------------------------------------------------

func subEvent(ntf, evt string, at time.Time, status domain.SubscriptionStatus) port.SubscriptionEvent {
	return port.SubscriptionEvent{
		NotificationID: ntf, EventID: evt, Type: "subscription.updated", OccurredAt: at,
		CustomerID: "ctm_1", SubscriptionID: "sub_1", UserID: "u1", Status: status,
	}
}

func TestHandleWebhookBadSignatureIsUnauthorizedBeforeAnyTx(t *testing.T) {
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive, BillingCustomerID: "ctm_1"})
	h.payments.parseWebhookErr = errors.New("paddle: no matching h1 signature")

	err := h.svc.HandleWebhook(context.Background(), []byte("{}"), "ts=1;h1=00")
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if h.tx.calls != 0 || len(h.events.seen) != 0 {
		t.Fatal("a rejected signature must not open a tx or record anything")
	}
	if !h.payments.lastParseNow.Equal(now) {
		t.Fatalf("ParseWebhook now = %v, want the service clock %v", h.payments.lastParseNow, now)
	}
}

func TestHandleWebhookMalformedBodyIsValidation(t *testing.T) {
	h := newBillingHarness(time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC))
	h.payments.parseWebhookErr = fmt.Errorf("%w: paddle: decode webhook envelope", domain.ErrValidation)
	err := h.svc.HandleWebhook(context.Background(), []byte("nope"), "ts=1;h1=00")
	if !errors.Is(err, domain.ErrValidation) || errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrValidation (not unauthorized)", err)
	}
}

func TestHandleWebhookIgnoredTypeIsAcknowledgedWithoutTx(t *testing.T) {
	h := newBillingHarness(time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC))
	h.payments.webhookEvent = port.SubscriptionEvent{NotificationID: "ntf_t", EventID: "evt_t", Type: "transaction.completed", Ignored: true}
	if err := h.svc.HandleWebhook(context.Background(), []byte("{}"), "sig"); err != nil {
		t.Fatalf("ignored event must be a 200 no-op, got %v", err)
	}
	if h.tx.calls != 0 || len(h.events.seen) != 0 || len(h.subs.byUser) != 0 {
		t.Fatal("ignored events must not touch storage")
	}
}

func TestHandleWebhookAppliesAndClearsTrial(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionTrialing, TrialEndsAt: tptr(now.Add(5 * 24 * time.Hour)), BillingCustomerID: "ctm_1"})
	periodEnd := now.Add(365 * 24 * time.Hour)
	ev := subEvent("ntf_1", "evt_1", now.Add(-time.Minute), domain.SubscriptionActive)
	ev.Type = "subscription.activated"
	ev.CurrentPeriodEnd = &periodEnd
	h.payments.webhookEvent = ev

	if err := h.svc.HandleWebhook(ctx, []byte("{}"), "sig"); err != nil {
		t.Fatalf("HandleWebhook: %v", err)
	}
	got, err := h.subs.GetByUserID(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.SubscriptionActive || got.BillingSubscriptionID != "sub_1" || got.BillingCustomerID != "ctm_1" {
		t.Fatalf("row = %+v", got)
	}
	if got.TrialEndsAt != nil {
		t.Fatalf("TrialEndsAt = %v, want nil once a Paddle subscription exists", got.TrialEndsAt)
	}
	if got.CurrentPeriodEnd == nil || !got.CurrentPeriodEnd.Equal(periodEnd) {
		t.Fatalf("CurrentPeriodEnd = %v", got.CurrentPeriodEnd)
	}
	if got.LastEventAt == nil || !got.LastEventAt.Equal(ev.OccurredAt) {
		t.Fatalf("LastEventAt = %v, want %v", got.LastEventAt, ev.OccurredAt)
	}
	if _, ok := h.events.seen["ntf_1"]; !ok || h.tx.calls != 1 {
		t.Fatalf("event must be recorded inside exactly one tx (seen=%v tx=%d)", h.events.seen, h.tx.calls)
	}
}

func TestHandleWebhookDuplicateNotificationIsNoop(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive, BillingCustomerID: "ctm_1", LastEventAt: tptr(now.Add(-time.Hour))})
	if _, err := h.events.Record(ctx, port.SubscriptionEvent{NotificationID: "ntf_dup"}); err != nil {
		t.Fatal(err)
	}
	h.payments.webhookEvent = subEvent("ntf_dup", "evt_1", now, domain.SubscriptionCanceled)

	if err := h.svc.HandleWebhook(ctx, []byte("{}"), "sig"); err != nil {
		t.Fatal(err)
	}
	got, _ := h.subs.GetByUserID(ctx, "u1")
	if got.Status != domain.SubscriptionActive || h.subs.upsertCalls != 0 {
		t.Fatalf("duplicate notification must not re-apply: %+v (upserts=%d)", got, h.subs.upsertCalls)
	}
}

// Review Focus: a replay (same event id, NEW notification id) whose
// occurred_at EQUALS last_event_at must be applied (strict < guard).
func TestHandleWebhookReplayAtSameInstantApplies(t *testing.T) {
	ctx := context.Background()
	t1 := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	h := newBillingHarness(t1.Add(time.Minute))
	h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive, BillingCustomerID: "ctm_1", BillingSubscriptionID: "sub_1", LastEventAt: tptr(t1)})
	h.payments.webhookEvent = subEvent("ntf_replay", "evt_1", t1, domain.SubscriptionCanceled)

	if err := h.svc.HandleWebhook(ctx, []byte("{}"), "sig"); err != nil {
		t.Fatal(err)
	}
	got, _ := h.subs.GetByUserID(ctx, "u1")
	if got.Status != domain.SubscriptionCanceled {
		t.Fatalf("status = %q, want canceled (same-instant replay must apply)", got.Status)
	}
}

func TestHandleWebhookOlderEventIsDroppedButRecorded(t *testing.T) {
	ctx := context.Background()
	t1 := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	h := newBillingHarness(t2)
	h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive, BillingCustomerID: "ctm_1", BillingSubscriptionID: "sub_1", LastEventAt: tptr(t2)})
	h.payments.webhookEvent = subEvent("ntf_old", "evt_old", t1, domain.SubscriptionCanceled)

	if err := h.svc.HandleWebhook(ctx, []byte("{}"), "sig"); err != nil {
		t.Fatal(err)
	}
	got, _ := h.subs.GetByUserID(ctx, "u1")
	if got.Status != domain.SubscriptionActive || !got.LastEventAt.Equal(t2) {
		t.Fatalf("older event must not revert state: %+v", got)
	}
	if _, ok := h.events.seen["ntf_old"]; !ok {
		t.Fatal("the dropped notification must still be recorded so its retry is a no-op")
	}
}

func TestHandleWebhookResolvesUserByCustomerID(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	t.Run("known customer", func(t *testing.T) {
		h := newBillingHarness(now)
		h.seedSub(t, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive, BillingCustomerID: "ctm_1"})
		ev := subEvent("ntf_c", "evt_c", now, domain.SubscriptionPastDue)
		ev.UserID = "" // custom_data missing -> resolve via customer id
		h.payments.webhookEvent = ev
		if err := h.svc.HandleWebhook(ctx, []byte("{}"), "sig"); err != nil {
			t.Fatal(err)
		}
		got, _ := h.subs.GetByUserID(ctx, "u1")
		if got.Status != domain.SubscriptionPastDue || got.BillingSubscriptionID != "sub_1" {
			t.Fatalf("row = %+v", got)
		}
	})
	t.Run("unknown customer is logged and acknowledged", func(t *testing.T) {
		h := newBillingHarness(now)
		ev := subEvent("ntf_u", "evt_u", now, domain.SubscriptionActive)
		ev.UserID = ""
		ev.CustomerID = "ctm_unknown"
		h.payments.webhookEvent = ev
		if err := h.svc.HandleWebhook(ctx, []byte("{}"), "sig"); err != nil {
			t.Fatalf("unknown customer must be a 200 no-op, got %v", err)
		}
		if len(h.subs.byUser) != 0 {
			t.Fatalf("store mutated: %v", h.subs.byUser)
		}
	})
}
```

Add `"fmt"` to the test file imports.

- [ ] **Step 2: Run and confirm failure.** `cd backend && go test ./internal/service/ -run TestHandleWebhook` — expected: `not implemented` failures.

- [ ] **Step 3: Replace the `HandleWebhook` stub with:**

```go
// HandleWebhook runs the pipeline: verify (401) → ignore non-subscription
// types (200) → one tx { record notification id (replay → 200) → resolve
// user → ordering guard → upsert }. The marker commits only if the update
// commits, so a transient failure rolls both back and Paddle's retry
// re-drives the event.
func (s *BillingService) HandleWebhook(ctx context.Context, payload []byte, sigHeader string) error {
	if s.selfHosted {
		return fmt.Errorf("%w: billing is disabled on self-hosted instances", domain.ErrSelfHosted)
	}
	ev, err := s.payments.ParseWebhook(payload, sigHeader, s.clock.Now())
	if err != nil {
		if errors.Is(err, domain.ErrValidation) {
			return err
		}
		return fmt.Errorf("%w: webhook signature verification failed: %v", domain.ErrUnauthorized, err)
	}
	if ev.Ignored {
		return nil
	}
	return s.tx.RunInTx(ctx, func(ctx context.Context) error {
		first, err := s.events.Record(ctx, ev)
		if err != nil {
			return err
		}
		if !first {
			return nil // already processed; duplicate deliveries are no-ops
		}
		return s.applyEvent(ctx, ev, false)
	})
}

// applyEvent resolves the user (custom_data.user_id, else the stored
// customer id), drops events older than the mirrored LastEventAt unless
// force (reconciliation), and upserts the mirror. A provider subscription
// existing clears trial_ends_at.
func (s *BillingService) applyEvent(ctx context.Context, ev port.SubscriptionEvent, force bool) error {
	userID := ev.UserID
	var existing domain.Subscription
	var err error
	if userID == "" {
		if ev.CustomerID == "" {
			s.logger.Warn("billing: event carries neither user id nor customer id", "event_id", ev.EventID, "type", ev.Type)
			return nil
		}
		existing, err = s.subs.GetByBillingCustomerID(ctx, ev.CustomerID)
		if errors.Is(err, domain.ErrNotFound) {
			s.logger.Warn("billing: event for unknown customer", "event_id", ev.EventID, "customer_id", ev.CustomerID, "type", ev.Type)
			return nil
		}
		if err != nil {
			return err
		}
		userID = existing.UserID
	} else {
		existing, err = s.subs.GetByUserID(ctx, userID)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
	}

	// Paddle does not guarantee delivery order: an event older than the
	// newest applied one is dropped (strict <, so same-instant replays apply).
	if !force && existing.LastEventAt != nil && ev.OccurredAt.Before(*existing.LastEventAt) {
		return nil
	}

	occurred := ev.OccurredAt
	sub := domain.Subscription{
		UserID:                userID,
		Status:                ev.Status,
		Plan:                  domain.PlanAnnual,
		PriceUSD:              domain.PriceUSDAnnual,
		CurrentPeriodEnd:      ev.CurrentPeriodEnd,
		CancelAtPeriodEnd:     ev.CancelAtPeriodEnd,
		TrialEndsAt:           nil, // a provider subscription supersedes the signup trial
		BillingCustomerID:     firstNonEmpty(ev.CustomerID, existing.BillingCustomerID),
		BillingSubscriptionID: firstNonEmpty(ev.SubscriptionID, existing.BillingSubscriptionID),
		LastEventAt:           &occurred,
	}
	if sub.Status == "" {
		sub.Status = existing.Status
	}
	if sub.Status == "" {
		sub.Status = domain.SubscriptionNone
	}
	return s.subs.Upsert(ctx, sub)
}
```

- [ ] **Step 4: Run.** `cd backend && go test ./internal/service/...` — expected: `ok`.

- [ ] **Step 5: Commit.**

```bash
git add backend/internal/service
git commit -m "feat(service): Paddle webhook pipeline with notification-id idempotency and occurred_at ordering" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 11: Service — reconciliation loop and throttled inline reconcile

**Files:**
- Modify: `backend/internal/service/billing.go` (replace the `ReconcileSubscriptions` stub; add `reconcileOne`, `claimInlineReconcile`; extend `GetSubscription`)
- Modify: `backend/internal/service/billing_test.go` (append)

**Interfaces:**
- Produces: `(*BillingService) ReconcileSubscriptions(ctx) error`; unexported `(*BillingService) reconcileOne(ctx, sub domain.Subscription, now time.Time) (domain.Subscription, error)`, `(*BillingService) claimInlineReconcile(userID string, now time.Time) bool`.

- [ ] **Step 1: Append the failing tests:**

```go
// --- Reconciliation ----------------------------------------------------------

func TestReconcileSubscriptionsAppliesProviderStateAsNow(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	for _, id := range []string{"a", "b"} {
		h.seedSub(t, domain.Subscription{UserID: id, Status: domain.SubscriptionActive, BillingCustomerID: "ctm_" + id, BillingSubscriptionID: "sub_" + id,
			CurrentPeriodEnd: tptr(now.Add(-2 * time.Hour)), LastEventAt: tptr(now.Add(-time.Hour))})
	}
	// not stale: must be left alone
	h.seedSub(t, domain.Subscription{UserID: "c", Status: domain.SubscriptionActive, BillingCustomerID: "ctm_c", BillingSubscriptionID: "sub_c",
		CurrentPeriodEnd: tptr(now.Add(24 * time.Hour)), LastEventAt: tptr(now.Add(-time.Hour))})
	h.payments.getSubEvent = port.SubscriptionEvent{Type: "subscription.reconciled", Status: domain.SubscriptionCanceled, CustomerID: "ctm_x"}

	if err := h.svc.ReconcileSubscriptions(ctx); err != nil {
		t.Fatalf("ReconcileSubscriptions: %v", err)
	}
	if h.payments.getSubCalls != 2 {
		t.Fatalf("GetSubscription calls = %d, want 2 (a and b only)", h.payments.getSubCalls)
	}
	for _, id := range []string{"a", "b"} {
		got, _ := h.subs.GetByUserID(ctx, id)
		if got.Status != domain.SubscriptionCanceled {
			t.Fatalf("%s: status = %q, want canceled", id, got.Status)
		}
		if got.LastEventAt == nil || !got.LastEventAt.Equal(now) {
			t.Fatalf("%s: LastEventAt = %v, want now (synthetic event)", id, got.LastEventAt)
		}
		if got.BillingSubscriptionID != "sub_"+id || got.BillingCustomerID != "ctm_x" {
			t.Fatalf("%s: ids = %q/%q (user must be taken from the row, ids from the provider)", id, got.BillingSubscriptionID, got.BillingCustomerID)
		}
	}
	if got, _ := h.subs.GetByUserID(ctx, "c"); got.Status != domain.SubscriptionActive {
		t.Fatalf("c must be untouched: %+v", got)
	}
}

func TestReconcileSubscriptionsSkipsProviderErrors(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	h.seedSub(t, domain.Subscription{UserID: "a", Status: domain.SubscriptionActive, BillingCustomerID: "ctm_a", BillingSubscriptionID: "sub_a",
		CurrentPeriodEnd: tptr(now.Add(-2 * time.Hour)), LastEventAt: tptr(now.Add(-time.Hour))})
	h.payments.getSubErr = errors.New("paddle: http 503")
	if err := h.svc.ReconcileSubscriptions(ctx); err != nil {
		t.Fatalf("provider errors must be logged and skipped, got %v", err)
	}
	got, _ := h.subs.GetByUserID(ctx, "a")
	if got.Status != domain.SubscriptionActive || h.subs.upsertCalls != 0 {
		t.Fatalf("row must be unchanged on provider error: %+v", got)
	}
}

func TestReconcileOverridesTheOrderingGuard(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	// LastEventAt in the future (skewed provider clock) would make a plain
	// webhook at `now` drop; reconciliation must still win.
	h.seedSub(t, domain.Subscription{UserID: "a", Status: domain.SubscriptionActive, BillingCustomerID: "ctm_a", BillingSubscriptionID: "sub_a",
		CurrentPeriodEnd: tptr(now.Add(-2 * time.Hour)), LastEventAt: tptr(now.Add(time.Hour))})
	h.payments.getSubEvent = port.SubscriptionEvent{Status: domain.SubscriptionPaused}
	if err := h.svc.ReconcileSubscriptions(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.subs.GetByUserID(ctx, "a"); got.Status != domain.SubscriptionPaused {
		t.Fatalf("status = %q, want paused (reconcile always wins)", got.Status)
	}
}

// --- Inline reconcile in GetSubscription -----------------------------------

func lapsedActive(now time.Time) domain.Subscription {
	return domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive, BillingCustomerID: "ctm_1", BillingSubscriptionID: "sub_1",
		CurrentPeriodEnd: tptr(now.Add(-time.Minute)), LastEventAt: tptr(now.Add(-24 * time.Hour))}
}

func TestGetSubscriptionInlineReconcileWhenActivePastPeriodEnd(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	h.seedSub(t, lapsedActive(now))
	renewed := now.Add(365 * 24 * time.Hour)
	h.payments.getSubEvent = port.SubscriptionEvent{Status: domain.SubscriptionActive, CurrentPeriodEnd: &renewed, CustomerID: "ctm_1", SubscriptionID: "sub_1"}

	sub, err := h.svc.GetSubscription(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if h.payments.getSubCalls != 1 || h.payments.lastGetSubID != "sub_1" {
		t.Fatalf("GetSubscription calls = %d (%q), want 1 for sub_1", h.payments.getSubCalls, h.payments.lastGetSubID)
	}
	if sub.CurrentPeriodEnd == nil || !sub.CurrentPeriodEnd.Equal(renewed) {
		t.Fatalf("returned row must be the reconciled one: %+v", sub)
	}
	if !sub.HasAccess(now) {
		t.Fatal("reconciled renewal must grant access")
	}
}

func TestGetSubscriptionInlineReconcileThrottlesPerUser(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	h.seedSub(t, lapsedActive(now))
	// Provider still reports the lapsed state, so the row stays lapsed and
	// every call would re-trigger without the throttle.
	h.payments.getSubEvent = port.SubscriptionEvent{Status: domain.SubscriptionActive, CurrentPeriodEnd: tptr(now.Add(-time.Minute))}

	for i := 0; i < 3; i++ {
		if _, err := h.svc.GetSubscription(ctx, "u1"); err != nil {
			t.Fatal(err)
		}
	}
	if h.payments.getSubCalls != 1 {
		t.Fatalf("calls within 10m = %d, want 1", h.payments.getSubCalls)
	}
	h.clock.Advance(inlineReconcileEvery)
	if _, err := h.svc.GetSubscription(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if h.payments.getSubCalls != 2 {
		t.Fatalf("calls after 10m = %d, want 2", h.payments.getSubCalls)
	}
}

// Review Focus: the throttle slot is claimed BEFORE the provider call, so a
// failing provider cannot be hammered by a lapsed user's refreshes.
func TestGetSubscriptionInlineReconcileThrottlesFailuresToo(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	h := newBillingHarness(now)
	h.seedSub(t, lapsedActive(now))
	h.payments.getSubErr = errors.New("paddle: http 503")

	for i := 0; i < 3; i++ {
		sub, err := h.svc.GetSubscription(ctx, "u1")
		if err != nil {
			t.Fatalf("inline reconcile failure must not fail the read: %v", err)
		}
		if sub.Status != domain.SubscriptionActive {
			t.Fatalf("stored row must be returned on failure: %+v", sub)
		}
	}
	if h.payments.getSubCalls != 1 {
		t.Fatalf("calls = %d, want 1 (slot claimed before the call)", h.payments.getSubCalls)
	}
}

func TestGetSubscriptionNoInlineReconcileWhenNotLapsed(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	cases := map[string]domain.Subscription{
		"active inside period":         {UserID: "u1", Status: domain.SubscriptionActive, BillingSubscriptionID: "sub_1", CurrentPeriodEnd: tptr(now.Add(time.Hour))},
		"active lapsed without sub id": {UserID: "u1", Status: domain.SubscriptionActive, CurrentPeriodEnd: tptr(now.Add(-time.Hour))},
		"past_due lapsed":              {UserID: "u1", Status: domain.SubscriptionPastDue, BillingSubscriptionID: "sub_1", CurrentPeriodEnd: tptr(now.Add(-time.Hour))},
		"trialing":                     {UserID: "u1", Status: domain.SubscriptionTrialing, TrialEndsAt: tptr(now.Add(-time.Hour))},
	}
	for name, seed := range cases {
		t.Run(name, func(t *testing.T) {
			h := newBillingHarness(now)
			h.seedSub(t, seed)
			if _, err := h.svc.GetSubscription(ctx, "u1"); err != nil {
				t.Fatal(err)
			}
			if h.payments.getSubCalls != 0 {
				t.Fatalf("inline reconcile must not run: calls = %d", h.payments.getSubCalls)
			}
		})
	}
}
```

- [ ] **Step 2: Run and confirm failure.** `cd backend && go test ./internal/service/ -run 'TestReconcile|TestGetSubscriptionInline|TestGetSubscriptionNoInline'` — expected: `not implemented` and `calls = 0, want 1`.

- [ ] **Step 3: Implement.** Replace the `ReconcileSubscriptions` stub with:

```go
// ReconcileSubscriptions re-reads every stale row from Paddle and applies
// it as a synthetic event at now (so it always wins). Provider errors are
// logged and skipped; the loop never fails as a whole.
func (s *BillingService) ReconcileSubscriptions(ctx context.Context) error {
	if s.selfHosted {
		return nil
	}
	now := s.clock.Now()
	rows, err := s.subs.ListForReconciliation(ctx, now)
	if err != nil {
		return err
	}
	for _, sub := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, err := s.reconcileOne(ctx, sub, now); err != nil {
			s.logger.Warn("billing: reconcile failed", "user_id", sub.UserID, "subscription_id", sub.BillingSubscriptionID, "error", err)
		}
	}
	return nil
}

// reconcileOne fetches the live subscription and applies it through the
// webhook upsert path with OccurredAt = now and force = true.
func (s *BillingService) reconcileOne(ctx context.Context, sub domain.Subscription, now time.Time) (domain.Subscription, error) {
	ev, err := s.payments.GetSubscription(ctx, sub.BillingSubscriptionID)
	if err != nil {
		return sub, err
	}
	ev.UserID = sub.UserID
	ev.OccurredAt = now
	if ev.SubscriptionID == "" {
		ev.SubscriptionID = sub.BillingSubscriptionID
	}
	if err := s.applyEvent(ctx, ev, true); err != nil {
		return sub, err
	}
	return s.subs.GetByUserID(ctx, sub.UserID)
}

// claimInlineReconcile reserves the per-user inline slot. It is claimed
// before the provider call so a failing provider is not hammered.
func (s *BillingService) claimInlineReconcile(userID string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if last, ok := s.lastInline[userID]; ok && now.Sub(last) < inlineReconcileEvery {
		return false
	}
	s.lastInline[userID] = now
	return true
}
```

Then replace the body of `GetSubscription` (after the trial grant) so the function reads:

```go
func (s *BillingService) GetSubscription(ctx context.Context, userID string) (domain.Subscription, error) {
	if s.selfHosted {
		return selfHostedSubscription(userID), nil
	}
	sub, err := s.subs.GetByUserID(ctx, userID)
	if errors.Is(err, domain.ErrNotFound) {
		sub, err = s.grantTrial(ctx, userID)
	}
	if err != nil {
		return domain.Subscription{}, err
	}
	// Best-effort inline reconcile: an active row past its period end with a
	// provider subscription is re-read at most once per 10 minutes per user.
	now := s.clock.Now()
	if sub.Status == domain.SubscriptionActive && sub.BillingSubscriptionID != "" &&
		sub.CurrentPeriodEnd != nil && !now.Before(*sub.CurrentPeriodEnd) &&
		s.claimInlineReconcile(userID, now) {
		fresh, err := s.reconcileOne(ctx, sub, now)
		if err != nil {
			s.logger.Warn("billing: inline reconcile failed", "user_id", userID, "error", err)
		} else {
			sub = fresh
		}
	}
	return sub, nil
}
```

Update the `GetSubscription` doc comment to drop "(Task 11 adds the inline reconcile.)".

- [ ] **Step 4: Run with the race detector.** `cd backend && go test -race ./internal/service/...` — expected: `ok`.

- [ ] **Step 5: Commit.**

```bash
git add backend/internal/service
git commit -m "feat(service): subscription reconciliation loop and throttled inline reconcile" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 12: HTTP — billing handlers, `POST /v1/webhooks/paddle`, 402 details, 409/400/502 codes, `instance.webUrl`

**Files:**
- Modify: `backend/internal/adapter/in/httpapi/billing.go` (whole file)
- Modify: `backend/internal/adapter/in/httpapi/codec.go` (`errorDetail`, `statusFor`, `safeMessage`, `writeError`)
- Create: `backend/internal/adapter/in/httpapi/codec_billing_test.go`
- Modify: `backend/internal/adapter/in/httpapi/httpapi.go` (route L109; delete the `Payments` field L39–42; comments L97, L127)
- Modify: `backend/internal/adapter/in/httpapi/instance.go` (`InstanceInfo.WebURL`)
- Modify: `backend/internal/adapter/in/httpapi/instance_test.go` (add `WebURL`)
- Modify: `backend/internal/adapter/in/httpapi/harness_test.go` (`fakeBillingService` L104–144)
- Modify: `backend/internal/adapter/in/httpapi/billing_handlers_test.go` (whole file)
- Modify: `backend/internal/adapter/in/httpapi/middleware_test.go` (the `stripe webhook does not require a bearer token` subtest ~L429–435)

**Interfaces:**
- Consumes: `port.BillingService` (Task 2), domain sentinels (Task 1).
- Produces: handlers `handleCreateCheckout`, `handleCreatePortal`, `handlePaddleWebhook`, `handleGetSubscription`; `errorDetail.Details any \`json:"details,omitempty"\``; `type paymentRequiredDetails struct{Reason string; TrialEndsAt, CurrentPeriodEnd *time.Time}`; `InstanceInfo.WebURL string \`json:"webUrl"\``; `Deps` without `Payments`.

- [ ] **Step 1: Update the harness fake.** Replace `fakeBillingService` and its methods in `harness_test.go` with:

```go
type fakeBillingService struct {
	subRet domain.Subscription
	subErr error

	checkoutURL       string
	checkoutErr       error
	checkoutCalls     int
	gotCheckoutUserID string

	portalURLs      port.PortalURLs
	portalErr       error
	gotPortalUserID string

	webhookErr        error
	webhookCalls      int
	gotWebhookPayload []byte
	gotWebhookSig     string

	reconcileCalls   int
	requireActiveErr error
}

func (f *fakeBillingService) GetSubscription(ctx context.Context, userID string) (domain.Subscription, error) {
	return f.subRet, f.subErr
}
func (f *fakeBillingService) CreateCheckout(ctx context.Context, userID string) (string, error) {
	f.checkoutCalls++
	f.gotCheckoutUserID = userID
	return f.checkoutURL, f.checkoutErr
}
func (f *fakeBillingService) CreatePortalSession(ctx context.Context, userID string) (port.PortalURLs, error) {
	f.gotPortalUserID = userID
	return f.portalURLs, f.portalErr
}
func (f *fakeBillingService) HandleWebhook(ctx context.Context, payload []byte, sigHeader string) error {
	f.webhookCalls++
	f.gotWebhookPayload = payload
	f.gotWebhookSig = sigHeader
	return f.webhookErr
}
func (f *fakeBillingService) ReconcileSubscriptions(ctx context.Context) error {
	f.reconcileCalls++
	return nil
}
func (f *fakeBillingService) RequireActive(ctx context.Context, userID string) error {
	return f.requireActiveErr
}
```

- [ ] **Step 2: Replace `billing_handlers_test.go` entirely:**

```go
package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

func TestHandleMe(t *testing.T) {
	h := newHarness(t)
	rec := h.authed(http.MethodGet, "/v1/me", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var got domain.User
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != defaultUserID {
		t.Fatalf("id = %q, want %q", got.ID, defaultUserID)
	}
	if h.users.ensureCalls == 0 {
		t.Fatalf("EnsureUser was not called")
	}
}

func TestHandleGetSubscription(t *testing.T) {
	h := newHarness(t)
	end := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	h.billing.subRet = domain.Subscription{Status: domain.SubscriptionPaused, Plan: domain.PlanAnnual, PriceUSD: 50, CurrentPeriodEnd: &end, BillingCustomerID: "ctm_secret", BillingSubscriptionID: "sub_secret"}
	rec := h.authed(http.MethodGet, "/v1/billing/subscription", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(raw["status"]) != `"paused"` {
		t.Fatalf("status = %s, want paused", raw["status"])
	}
	for _, k := range []string{"billingCustomerId", "billingSubscriptionId", "BillingCustomerID", "userId"} {
		if _, ok := raw[k]; ok {
			t.Fatalf("provider ids must never be serialized: found %q", k)
		}
	}
}

func TestHandleCreateCheckout(t *testing.T) {
	t.Run("no request body, returns url", func(t *testing.T) {
		h := newHarness(t)
		h.billing.checkoutURL = "https://app/checkout?_ptxn=txn_1"
		rec := h.authed(http.MethodPost, "/v1/billing/checkout", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["url"] != h.billing.checkoutURL {
			t.Fatalf("url = %q", body["url"])
		}
		if h.billing.gotCheckoutUserID != defaultUserID {
			t.Fatalf("userID = %q, want %q", h.billing.gotCheckoutUserID, defaultUserID)
		}
	})
	t.Run("client-supplied URLs are ignored, not an error", func(t *testing.T) {
		h := newHarness(t)
		h.billing.checkoutURL = "https://app/checkout"
		rec := h.authed(http.MethodPost, "/v1/billing/checkout", jsonBody(t, map[string]string{"successUrl": "https://evil"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})
	t.Run("already subscribed is 409", func(t *testing.T) {
		h := newHarness(t)
		h.billing.checkoutErr = domain.ErrAlreadySubscribed
		rec := h.authed(http.MethodPost, "/v1/billing/checkout", nil)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "already_subscribed" {
			t.Fatalf("code = %q, want already_subscribed", got.Code)
		}
	})
	t.Run("provider failure is 502", func(t *testing.T) {
		h := newHarness(t)
		h.billing.checkoutErr = domain.ErrBillingUnavailable
		rec := h.authed(http.MethodPost, "/v1/billing/checkout", nil)
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "billing_unavailable" {
			t.Fatalf("code = %q, want billing_unavailable", got.Code)
		}
	})
	t.Run("self-hosted is 501", func(t *testing.T) {
		h := newHarness(t)
		h.billing.checkoutErr = domain.ErrSelfHosted
		rec := h.authed(http.MethodPost, "/v1/billing/checkout", nil)
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want 501", rec.Code)
		}
	})
}

func TestHandleCreatePortal(t *testing.T) {
	t.Run("returns the three urls", func(t *testing.T) {
		h := newHarness(t)
		h.billing.portalURLs = port.PortalURLs{Overview: "https://p/o", Cancel: "https://p/c", UpdatePayment: "https://p/u"}
		rec := h.authed(http.MethodPost, "/v1/billing/portal", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["overviewUrl"] != "https://p/o" || body["cancelUrl"] != "https://p/c" || body["updatePaymentUrl"] != "https://p/u" {
			t.Fatalf("body = %v", body)
		}
		if h.billing.gotPortalUserID != defaultUserID {
			t.Fatalf("userID = %q", h.billing.gotPortalUserID)
		}
	})
	t.Run("empty cancel/update are serialized as empty strings", func(t *testing.T) {
		h := newHarness(t)
		h.billing.portalURLs = port.PortalURLs{Overview: "https://p/o"}
		rec := h.authed(http.MethodPost, "/v1/billing/portal", nil)
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if v, ok := body["cancelUrl"]; !ok || v != "" {
			t.Fatalf("cancelUrl = %q (present=%v), want present and empty", v, ok)
		}
	})
	t.Run("no billing profile is 400", func(t *testing.T) {
		h := newHarness(t)
		h.billing.portalErr = domain.ErrNoBillingProfile
		rec := h.authed(http.MethodPost, "/v1/billing/portal", nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "no_billing_profile" {
			t.Fatalf("code = %q, want no_billing_profile", got.Code)
		}
	})
}

func TestHandlePaddleWebhook(t *testing.T) {
	post := func(h *harness, body []byte, sig string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/paddle", bytes.NewReader(body))
		if sig != "" {
			req.Header.Set("Paddle-Signature", sig)
		}
		rec := httptest.NewRecorder()
		h.handler().ServeHTTP(rec, req)
		return rec
	}

	t.Run("forwards raw body and Paddle-Signature, answers received", func(t *testing.T) {
		h := newHarness(t)
		body := []byte(`{"event_type":"subscription.updated"}`)
		rec := post(h, body, "ts=1;h1=ab")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		var got map[string]bool
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || !got["received"] {
			t.Fatalf("body = %s", rec.Body.String())
		}
		if h.billing.gotWebhookSig != "ts=1;h1=ab" || !bytes.Equal(h.billing.gotWebhookPayload, body) {
			t.Fatalf("forwarded sig=%q payload=%s", h.billing.gotWebhookSig, h.billing.gotWebhookPayload)
		}
	})
	t.Run("bad signature is 401", func(t *testing.T) {
		h := newHarness(t)
		h.billing.webhookErr = domain.ErrUnauthorized
		rec := post(h, []byte(`{}`), "bad")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})
	t.Run("malformed envelope is 400", func(t *testing.T) {
		h := newHarness(t)
		h.billing.webhookErr = domain.ErrValidation
		rec := post(h, []byte(`{}`), "ts=1;h1=ab")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
	t.Run("oversized body rejected before HandleWebhook runs", func(t *testing.T) {
		h := newHarness(t)
		big := bytes.Repeat([]byte("a"), (1<<20)+1024)
		rec := post(h, big, "ts=1;h1=ab")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		if h.billing.webhookCalls != 0 {
			t.Fatalf("HandleWebhook called = %d, want 0", h.billing.webhookCalls)
		}
	})
	t.Run("old stripe route is gone", func(t *testing.T) {
		h := newHarness(t)
		req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/stripe", bytes.NewReader([]byte(`{}`)))
		rec := httptest.NewRecorder()
		h.handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 for the removed Stripe route", rec.Code)
		}
	})
}
```

- [ ] **Step 3: Create `codec_billing_test.go`** (white-box test of the 402 envelope):

```go
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func TestWriteErrorPaymentRequiredDetails(t *testing.T) {
	s := &server{deps: Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	trialEnd := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)

	t.Run("typed error renders details", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/mail/threads", nil)
		err := fmt.Errorf("mail: %w", &domain.PaymentRequiredError{Reason: domain.DenialTrialEnded, TrialEndsAt: &trialEnd})
		s.writeError(rec, req, err)
		if rec.Code != http.StatusPaymentRequired {
			t.Fatalf("status = %d, want 402", rec.Code)
		}
		var body struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
				Details struct {
					Reason           string  `json:"reason"`
					TrialEndsAt      *string `json:"trialEndsAt"`
					CurrentPeriodEnd *string `json:"currentPeriodEnd"`
				} `json:"details"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v (%s)", err, rec.Body.String())
		}
		if body.Error.Code != "payment_required" || body.Error.Message != "An active subscription is required." {
			t.Fatalf("envelope = %+v", body.Error)
		}
		if body.Error.Details.Reason != "trial_ended" {
			t.Fatalf("reason = %q", body.Error.Details.Reason)
		}
		if body.Error.Details.TrialEndsAt == nil || *body.Error.Details.TrialEndsAt != "2026-10-18T09:00:00Z" {
			t.Fatalf("trialEndsAt = %v", body.Error.Details.TrialEndsAt)
		}
		var raw map[string]map[string]json.RawMessage
		_ = json.Unmarshal(rec.Body.Bytes(), &raw)
		var details map[string]json.RawMessage
		_ = json.Unmarshal(raw["error"]["details"], &details)
		if _, ok := details["currentPeriodEnd"]; ok {
			t.Fatal("nil currentPeriodEnd must be omitted")
		}
	})

	t.Run("bare sentinel has no details key", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/mail/threads", nil)
		s.writeError(rec, req, domain.ErrPaymentRequired)
		var raw map[string]map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		if _, ok := raw["error"]["details"]; ok {
			t.Fatalf("details must be omitted without a typed error: %s", rec.Body.String())
		}
	})

	t.Run("new sentinels map to their codes", func(t *testing.T) {
		for _, tc := range []struct {
			err    error
			status int
			code   string
		}{
			{domain.ErrAlreadySubscribed, http.StatusConflict, "already_subscribed"},
			{domain.ErrNoBillingProfile, http.StatusBadRequest, "no_billing_profile"},
			{domain.ErrBillingUnavailable, http.StatusBadGateway, "billing_unavailable"},
		} {
			status, code := statusFor(fmt.Errorf("wrap: %w", tc.err))
			if status != tc.status || code != tc.code {
				t.Fatalf("statusFor(%v) = %d/%q, want %d/%q", tc.err, status, code, tc.status, tc.code)
			}
			if safeMessage(code) == "Internal server error." {
				t.Fatalf("safeMessage(%q) must have a dedicated message", code)
			}
		}
		if !errors.Is(fmt.Errorf("x: %w", domain.ErrAlreadySubscribed), domain.ErrAlreadySubscribed) {
			t.Fatal("sanity")
		}
	})
}
```

- [ ] **Step 4: Update `instance_test.go`:** add `WebURL: "https://app.calendium.com",` to the `InstanceInfo` literal and `"webUrl"` to the top-level keys loop.

- [ ] **Step 5: Update `middleware_test.go`:** rename the subtest to `"paddle webhook does not require a bearer token"` and change its path to `/v1/webhooks/paddle`.

- [ ] **Step 6: Run and confirm failure.** `cd backend && go test ./internal/adapter/in/httpapi/ -run 'TestHandle|TestWriteError|TestHandleInstance|TestPublicRoutes'` — expected: compile errors (`fakeBillingService` does not implement `port.BillingService`, `undefined: ... WebURL`).

- [ ] **Step 7: Replace `billing.go` with:**

```go
package httpapi

import (
	"io"
	"net/http"

	"calendium/backend/internal/domain"
)

func (s *server) handleMe(w http.ResponseWriter, r *http.Request) {
	// requireAuth already upserted the user for this request.
	writeJSON(w, http.StatusOK, userFrom(r))
}

func (s *server) handleGetSubscription(w http.ResponseWriter, r *http.Request) {
	sub, err := s.deps.Billing.GetSubscription(r.Context(), userFrom(r).ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

// handleCreateCheckout takes no request body: success/cancel destinations
// are never client-supplied (the web /checkout page owns successUrl).
func (s *server) handleCreateCheckout(w http.ResponseWriter, r *http.Request) {
	url, err := s.deps.Billing.CreateCheckout(r.Context(), userFrom(r).ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

// handleCreatePortal takes no request body and returns temporary portal
// links; cancelUrl/updatePaymentUrl are empty strings without a subscription.
func (s *server) handleCreatePortal(w http.ResponseWriter, r *http.Request) {
	urls, err := s.deps.Billing.CreatePortalSession(r.Context(), userFrom(r).ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"overviewUrl":      urls.Overview,
		"cancelUrl":        urls.Cancel,
		"updatePaymentUrl": urls.UpdatePayment,
	})
}

// handlePaddleWebhook is unauthenticated; the Paddle-Signature header is
// verified (HMAC-SHA256) and events are deduplicated/ordered inside Billing.
// Bodies are capped at 1 MB.
func (s *server) handlePaddleWebhook(w http.ResponseWriter, r *http.Request) {
	payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		s.writeError(w, r, domain.ErrValidation)
		return
	}
	if err := s.deps.Billing.HandleWebhook(r.Context(), payload, r.Header.Get("Paddle-Signature")); err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"received": true})
}
```

- [ ] **Step 8: Update `codec.go`.** Add `"time"` to imports. Change `errorDetail` to:

```go
type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Details carries structured, client-safe context for specific codes
	// (402 payment_required); omitted otherwise.
	Details any `json:"details,omitempty"`
}

// paymentRequiredDetails is the 402 `details` body (docs/payments.md).
type paymentRequiredDetails struct {
	Reason           string     `json:"reason"`
	TrialEndsAt      *time.Time `json:"trialEndsAt,omitempty"`
	CurrentPeriodEnd *time.Time `json:"currentPeriodEnd,omitempty"`
}
```

In `statusFor`, add before the `ErrConflict` case:

```go
	case errors.Is(err, domain.ErrAlreadySubscribed):
		return http.StatusConflict, "already_subscribed"
	case errors.Is(err, domain.ErrNoBillingProfile):
		return http.StatusBadRequest, "no_billing_profile"
	case errors.Is(err, domain.ErrBillingUnavailable):
		return http.StatusBadGateway, "billing_unavailable"
```

In `safeMessage`, add:

```go
	case "already_subscribed":
		return "You already have an active subscription. Manage it from billing."
	case "no_billing_profile":
		return "No billing profile yet. Start a checkout first."
	case "billing_unavailable":
		return "The billing service is temporarily unavailable. Please try again."
```

Replace `writeError`'s last line with:

```go
	detail := errorDetail{Code: code, Message: safeMessage(code)}
	var pr *domain.PaymentRequiredError
	if errors.As(err, &pr) {
		detail.Details = paymentRequiredDetails{Reason: string(pr.Reason), TrialEndsAt: pr.TrialEndsAt, CurrentPeriodEnd: pr.CurrentPeriodEnd}
	}
	writeJSON(w, status, errorBody{Error: detail})
```

- [ ] **Step 9: Update `httpapi.go`.** Replace `mux.HandleFunc("POST /v1/webhooks/stripe", s.handleStripeWebhook)` with `mux.HandleFunc("POST /v1/webhooks/paddle", s.handlePaddleWebhook)`; delete the `Payments port.Payments` field and its comment from `Deps`; change the `New` doc comment `except the Stripe webhook` to `except the Paddle webhook`, and the shared-threads comment `(the Stripe-webhook precedent)` to `(the Paddle-webhook precedent)`.

- [ ] **Step 10: Update `instance.go`.** After `AuthProviders`, add:

```go
	// WebURL is the public web app origin (PUBLIC_WEB_URL, no trailing
	// slash). Mobile and desktop build billing links from it
	// (<webUrl>/pricing, <webUrl>/settings) instead of hardcoded domains.
	WebURL string `json:"webUrl"`
```

and change the `InstanceFeatures.Billing` line comment to `// Billing is true exactly when the deployment runs in cloud mode (Paddle wired).`

- [ ] **Step 11: Run the httpapi suite.** `cd backend && go test ./internal/adapter/in/httpapi/...` — expected: `ok`.

- [ ] **Step 12: Commit.**

```bash
git add backend/internal/adapter/in/httpapi
git commit -m "feat(httpapi): Paddle webhook route, body-less checkout/portal, typed 402 details, billing error codes, instance.webUrl" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 13: Composition — `cmd/api` and `cmd/worker` wiring, startup refusal, reconcile loop

**Files:**
- Modify: `backend/cmd/api/main.go` (imports L33; L52–61 startup checks; L97 gateway; L149 billing service; L470 `Payments:`; instance literal ~L404–428)
- Modify: `backend/cmd/worker/main.go` (imports; after `config.FromEnv`; billing service + loop)

**Interfaces:**
- Consumes: `paddle.NewClient`, `paddle.Config` (Task 5), `service.NewBillingService`/`BillingServiceDeps` (Task 8), `cfg.ValidateCloudBilling()`/`cfg.Paddle`/`cfg.Billing` (Task 4), `store.BillingEvents()` (Task 3), `httpapi.InstanceInfo.WebURL` (Task 12).

- [ ] **Step 1: Prove the current tree does not build.** `cd backend && go build ./... 2>&1 | head` — expected: errors in `cmd/api` and `cmd/worker` (`undefined: stripeapi`, `cfg.Stripe undefined`, `store.StripeEvents undefined`).

- [ ] **Step 2: Edit `cmd/api/main.go`.** Replace the import `"calendium/backend/internal/adapter/out/stripeapi"` with `"calendium/backend/internal/adapter/out/paddle"`. After the `PUBLIC_WEB_URL` check (inside `run`, before Postgres), add:

```go
	// Cloud mode must never boot without a biller or with a forgeable webhook.
	if err := cfg.ValidateCloudBilling(); err != nil {
		return err
	}
```

Replace the `stripe := stripeapi.NewClient(...)` line with:

```go
	payments := paddle.NewClient(paddle.Config{
		Env:           cfg.Paddle.Env,
		APIKey:        cfg.Paddle.APIKey,
		WebhookSecret: cfg.Paddle.WebhookSecret,
		AnnualPriceID: cfg.Paddle.AnnualPriceID,
	}, hc)
```

Replace the `billing := service.NewBillingService(...)` line with:

```go
	billing := service.NewBillingService(service.BillingServiceDeps{
		Users:      store.Users(),
		Subs:       store.Subscriptions(),
		Events:     store.BillingEvents(),
		Payments:   payments,
		Clock:      clock,
		Tx:         store,
		SelfHosted: cfg.Instance.SelfHosted,
		Logger:     logger,
	})
```

Delete the `Payments:       stripe,` line from the `httpapi.Deps` literal. In the `httpapi.InstanceInfo` literal add `WebURL:          strings.TrimRight(cfg.Instance.PublicWebURL, "/"),` after `AuthProviders`. Add `"billing_env", cfg.Paddle.Env,` to the `api: optional deps` log line.

- [ ] **Step 3: Edit `cmd/worker/main.go`.** Add the import `"calendium/backend/internal/adapter/out/paddle"`. After `cfg, err := config.FromEnv()` error handling add:

```go
	if err := cfg.ValidateCloudBilling(); err != nil {
		return err
	}
```

After the `mapsProvider` block add:

```go
	// Billing reconciliation (docs/payments.md): re-reads stale subscription
	// mirrors from Paddle so a lost webhook never leaves a user entitled or
	// locked out for long. Cloud only; self-host has no biller.
	var billingSvc *service.BillingService
	if !cfg.Instance.SelfHosted {
		billingSvc = service.NewBillingService(service.BillingServiceDeps{
			Users:  store.Users(),
			Subs:   store.Subscriptions(),
			Events: store.BillingEvents(),
			Payments: paddle.NewClient(paddle.Config{
				Env:           cfg.Paddle.Env,
				APIKey:        cfg.Paddle.APIKey,
				WebhookSecret: cfg.Paddle.WebhookSecret,
				AnnualPriceID: cfg.Paddle.AnnualPriceID,
			}, hc),
			Clock:      service.SystemClock{},
			Tx:         store,
			SelfHosted: false,
			Logger:     logger,
		})
	}
```

After the `todoSyncSvc` loop block add:

```go
	if billingSvc != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runLoop(ctx, cfg.Billing.ReconcileInterval, func(ctx context.Context) {
				if err := billingSvc.ReconcileSubscriptions(ctx); err != nil {
					logger.Error("worker: reconcile subscriptions", "error", err)
				}
			})
		}()
	}
```

Add `"billing_reconcile_enabled", billingSvc != nil, "billing_reconcile_interval", cfg.Billing.ReconcileInterval.String(),` to the `worker: loops started` log line.

- [ ] **Step 4: Build, vet, lint and run the whole backend.** `cd backend && go build ./... && go vet ./... && golangci-lint run ./... && REQUIRE_DOCKER=1 go test ./...` — expected: clean build, no findings, all packages `ok`.

- [ ] **Step 5: Prove the startup refusal end to end** (no live stack touched — the binary exits before opening Postgres): `cd backend && SELF_HOSTED=false DATABASE_URL=postgres://x TOKEN_ENCRYPTION_KEY=00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff BETTER_AUTH_URL=http://localhost:3000 PUBLIC_WEB_URL=http://localhost:3000 go run ./cmd/api; echo "exit=$?"` — expected: log line `api: fatal error="cloud mode (SELF_HOSTED=false) requires PADDLE_API_KEY, PADDLE_WEBHOOK_SECRET, PADDLE_PRICE_ID_ANNUAL; ..."` and `exit=1`. Repeat with `./cmd/worker` — expected: `worker: fatal ...` and `exit=1`.

- [ ] **Step 6: Grep for leftovers in the backend.** `grep -rni stripe backend/` — expected: no output.

- [ ] **Step 7: Commit.**

```bash
git add backend/cmd
git commit -m "feat(cmd): wire the Paddle gateway and billing service into api and worker; refuse cloud boot without keys; reconcile loop" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 14: Shared contract — `paused`, typed 402 details, no-arg checkout, portal URL triple, `webUrl`, billing helpers

**Files:**
- Modify: `packages/shared/src/types.ts` (`SubscriptionStatus` L13–19; `InstanceFeatures.billing` comment ~L852; `InstanceInfo` ~L865; add billing types after `Subscription`)
- Modify: `packages/shared/src/client.ts` (`ApiRequestError` ~L120–128; `request` error branch ~L155–158; the two billing methods ~L176–185; `fetchInstance` error branch ~L109–112)
- Create: `packages/shared/src/billing.ts`
- Create: `packages/shared/src/billing.test.ts`
- Modify: `packages/shared/src/index.ts` (add `export * from './billing';`)
- Modify: `packages/shared/src/client.test.ts` (append)

**Interfaces:**
- Produces (types.ts): `SubscriptionStatus = 'trialing' | 'active' | 'past_due' | 'paused' | 'canceled' | 'none'`; `PaymentRequiredReason = 'trial_ended' | 'past_due' | 'canceled' | 'paused' | 'none'`; `interface PaymentRequiredDetails { reason: PaymentRequiredReason; trialEndsAt?: string; currentPeriodEnd?: string }`; `interface BillingPortalUrls { overviewUrl: string; cancelUrl: string; updatePaymentUrl: string }`; `InstanceInfo.webUrl: string`.
- Produces (client.ts): `new ApiRequestError(status, code, message, details?: unknown)` with `readonly details: unknown` and getter `paymentRequired: PaymentRequiredDetails | null`; `ApiClient.createCheckoutSession(): Promise<{ url: string }>`; `ApiClient.createBillingPortalSession(): Promise<BillingPortalUrls>`.
- Produces (billing.ts): `DAY_MS`, `TRIAL_LENGTH_DAYS = 14`, `ACTIVE_GRACE_MS`, `PAST_DUE_GRACE_MS`, `TRIAL_BANNER_DAYS = 3`; `subscriptionDenialReason(sub: Subscription, nowMs?: number): PaymentRequiredReason | null`; `subscriptionHasAccess(sub, nowMs?): boolean`; `trialDaysLeft(sub, nowMs?): number | null`; `hasBillingSubscription(sub): boolean`.

- [ ] **Step 1: Write the failing helper tests** at `packages/shared/src/billing.test.ts`:

```ts
import { describe, expect, it } from 'vitest';
import {
  ACTIVE_GRACE_MS,
  hasBillingSubscription,
  PAST_DUE_GRACE_MS,
  subscriptionDenialReason,
  subscriptionHasAccess,
  TRIAL_BANNER_DAYS,
  trialDaysLeft,
} from './billing';
import type { Subscription, SubscriptionStatus } from './types';

const NOW = Date.parse('2026-10-04T12:00:00Z');
const HOUR = 60 * 60 * 1000;

function sub(status: SubscriptionStatus, extra: Partial<Subscription> = {}): Subscription {
  return {
    status,
    plan: 'annual',
    priceUsd: 50,
    currentPeriodEnd: null,
    cancelAtPeriodEnd: false,
    trialEndsAt: null,
    ...extra,
  };
}
const iso = (ms: number) => new Date(ms).toISOString();

describe('subscriptionDenialReason (mirror of domain.Subscription.DenialReason)', () => {
  it.each<[string, Subscription, ReturnType<typeof subscriptionDenialReason>]>([
    ['trialing before end', sub('trialing', { trialEndsAt: iso(NOW + HOUR) }), null],
    ['trialing at end', sub('trialing', { trialEndsAt: iso(NOW) }), 'trial_ended'],
    ['trialing without end', sub('trialing'), 'trial_ended'],
    ['active no period', sub('active'), null],
    ['active inside grace', sub('active', { currentPeriodEnd: iso(NOW - ACTIVE_GRACE_MS + HOUR) }), null],
    ['active past grace', sub('active', { currentPeriodEnd: iso(NOW - ACTIVE_GRACE_MS) }), 'past_due'],
    ['past_due inside grace', sub('past_due', { currentPeriodEnd: iso(NOW - PAST_DUE_GRACE_MS + HOUR) }), null],
    ['past_due past grace', sub('past_due', { currentPeriodEnd: iso(NOW - PAST_DUE_GRACE_MS) }), 'past_due'],
    ['past_due without period', sub('past_due'), 'past_due'],
    ['paused', sub('paused', { currentPeriodEnd: iso(NOW + HOUR) }), 'paused'],
    ['canceled', sub('canceled', { currentPeriodEnd: iso(NOW + HOUR) }), 'canceled'],
    ['none', sub('none'), 'none'],
  ])('%s', (_name, s, want) => {
    expect(subscriptionDenialReason(s, NOW)).toBe(want);
    expect(subscriptionHasAccess(s, NOW)).toBe(want === null);
  });
});

describe('trialDaysLeft', () => {
  it('counts whole days up, never below zero, only while trialing', () => {
    expect(trialDaysLeft(sub('trialing', { trialEndsAt: iso(NOW + 2 * 24 * HOUR + HOUR) }), NOW)).toBe(3);
    expect(trialDaysLeft(sub('trialing', { trialEndsAt: iso(NOW + 24 * HOUR) }), NOW)).toBe(1);
    expect(trialDaysLeft(sub('trialing', { trialEndsAt: iso(NOW - HOUR) }), NOW)).toBe(0);
    expect(trialDaysLeft(sub('trialing'), NOW)).toBeNull();
    expect(trialDaysLeft(sub('active', { trialEndsAt: iso(NOW + HOUR) }), NOW)).toBeNull();
  });
  it('exposes the 3-day banner window', () => {
    expect(TRIAL_BANNER_DAYS).toBe(3);
  });
});

describe('hasBillingSubscription', () => {
  it('is true only for live provider statuses', () => {
    expect(hasBillingSubscription(sub('active'))).toBe(true);
    expect(hasBillingSubscription(sub('past_due'))).toBe(true);
    expect(hasBillingSubscription(sub('paused'))).toBe(true);
    expect(hasBillingSubscription(sub('trialing'))).toBe(false);
    expect(hasBillingSubscription(sub('canceled'))).toBe(false);
    expect(hasBillingSubscription(sub('none'))).toBe(false);
  });
});
```

Append to `packages/shared/src/client.test.ts`:

```ts
describe('billing (Paddle)', () => {
  const mk = (fetchFn: typeof fetch) =>
    new ApiClient({ baseUrl: 'https://api.test', getAccessToken: async () => 'tok', fetch: fetchFn });

  it('createCheckoutSession posts with no body and returns the url', async () => {
    const { fetchFn, calls } = createFakeFetch([{ status: 200, body: { url: 'https://app/checkout?_ptxn=txn_1' } }]);
    const res = await mk(fetchFn).createCheckoutSession();
    expect(res.url).toBe('https://app/checkout?_ptxn=txn_1');
    expect(calls[0]?.url).toBe('https://api.test/v1/billing/checkout');
    expect(calls[0]?.method).toBe('POST');
    expect(calls[0]?.body).toBeUndefined();
  });

  it('createBillingPortalSession posts with no body and returns the url triple', async () => {
    const { fetchFn, calls } = createFakeFetch([
      { status: 200, body: { overviewUrl: 'https://p/o', cancelUrl: '', updatePaymentUrl: 'https://p/u' } },
    ]);
    const res = await mk(fetchFn).createBillingPortalSession();
    expect(res).toEqual({ overviewUrl: 'https://p/o', cancelUrl: '', updatePaymentUrl: 'https://p/u' });
    expect(calls[0]?.url).toBe('https://api.test/v1/billing/portal');
    expect(calls[0]?.body).toBeUndefined();
  });

  it('a 402 exposes typed payment-required details', async () => {
    const { fetchFn } = createFakeFetch([
      {
        status: 402,
        body: {
          error: {
            code: 'payment_required',
            message: 'An active subscription is required.',
            details: { reason: 'trial_ended', trialEndsAt: '2026-10-18T09:00:00Z' },
          },
        },
      },
    ]);
    let caught: unknown;
    try {
      await mk(fetchFn).getSubscription();
    } catch (err) {
      caught = err;
    }
    expect(caught).toBeInstanceOf(ApiRequestError);
    const err = caught as ApiRequestError;
    expect(err.status).toBe(402);
    expect(err.code).toBe('payment_required');
    expect(err.paymentRequired).toEqual({ reason: 'trial_ended', trialEndsAt: '2026-10-18T09:00:00Z' });
  });

  it('non-402 errors and malformed details have null paymentRequired', async () => {
    const { fetchFn } = createFakeFetch([{ status: 409, body: { error: { code: 'already_subscribed', message: 'x', details: { reason: 'nope' } } } }]);
    let caught: unknown;
    try {
      await mk(fetchFn).createCheckoutSession();
    } catch (err) {
      caught = err;
    }
    expect((caught as ApiRequestError).code).toBe('already_subscribed');
    expect((caught as ApiRequestError).paymentRequired).toBeNull();
    const bare = new ApiRequestError(402, 'payment_required', 'x', { nope: true });
    expect(bare.paymentRequired).toBeNull();
  });

  it('fetchInstance carries webUrl', async () => {
    const { fetchFn } = createFakeFetch([
      {
        status: 200,
        body: {
          name: 'Calendium', mode: 'cloud', version: '0.1.0', authBaseUrl: 'https://web/api/auth',
          authProviders: ['email'], undoSendSeconds: 15, webUrl: 'https://web',
          features: { billing: true, google: false, microsoft: false, ai: false, push: false },
        },
      },
    ]);
    const info = await fetchInstance('https://api.test/', fetchFn);
    expect(info.webUrl).toBe('https://web');
  });
});
```

- [ ] **Step 2: Run and confirm failure.** `bun run --cwd packages/shared test` — expected: `billing.test.ts` fails to resolve `./billing`; `client.test.ts` fails on `createCheckoutSession()` arity / `paymentRequired` undefined.

- [ ] **Step 3: Edit `types.ts`.** Replace the `SubscriptionStatus` union with:

```ts
/** Mirrors backend/internal/domain/subscription.go. Expiry is never a status: it is computed from time. */
export type SubscriptionStatus = 'trialing' | 'active' | 'past_due' | 'paused' | 'canceled' | 'none';
```

After the `Subscription` interface add:

```ts
/** `details.reason` of a 402 payment_required response (domain.DenialReason). */
export type PaymentRequiredReason = 'trial_ended' | 'past_due' | 'canceled' | 'paused' | 'none';

/** Structured `error.details` of a 402 response. */
export interface PaymentRequiredDetails {
  reason: PaymentRequiredReason;
  trialEndsAt?: string;
  currentPeriodEnd?: string;
}

/**
 * Temporary Paddle customer-portal links (POST /v1/billing/portal). Never
 * cache them. cancelUrl/updatePaymentUrl are empty strings when the user has
 * no provider subscription yet.
 */
export interface BillingPortalUrls {
  overviewUrl: string;
  cancelUrl: string;
  updatePaymentUrl: string;
}
```

Change the `InstanceFeatures.billing` doc comment to `/** Paddle billing is wired (true exactly in cloud mode; false on self-hosted instances). */`. In `InstanceInfo`, after `authProviders`, add:

```ts
  /**
   * Public web app origin (PUBLIC_WEB_URL, no trailing slash). Mobile and
   * desktop build billing links from it: `${webUrl}/pricing`, `${webUrl}/settings`.
   */
  webUrl: string;
```

- [ ] **Step 4: Edit `client.ts`.** Replace the `ApiRequestError` class with:

```ts
export class ApiRequestError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    message: string,
    /** Raw `error.details` from the response envelope, when present. */
    public readonly details?: unknown
  ) {
    super(message);
    this.name = 'ApiRequestError';
  }

  /** Typed 402 payload, or null for any other error or a malformed body. */
  get paymentRequired(): PaymentRequiredDetails | null {
    if (this.status !== 402 || !this.details || typeof this.details !== 'object') return null;
    const d = this.details as Partial<PaymentRequiredDetails>;
    return typeof d.reason === 'string' ? (d as PaymentRequiredDetails) : null;
  }
}
```

In `request`, change `throw new ApiRequestError(res.status, code, message);` to `throw new ApiRequestError(res.status, code, message, json?.error?.details);` (both in `request` and in `fetchInstance`'s error branch). Replace the two billing methods with:

```ts
  /**
   * Starts a Paddle overlay checkout; navigate the browser to the returned
   * URL (the web /checkout page). No arguments: redirect targets are never
   * client-supplied. Throws ApiRequestError(409, 'already_subscribed') when a
   * live subscription exists — open the portal instead.
   */
  createCheckoutSession() {
    return this.request<{ url: string }>('POST', '/v1/billing/checkout');
  }
  /**
   * Temporary Paddle customer-portal links. Throws
   * ApiRequestError(400, 'no_billing_profile') before any checkout happened.
   */
  createBillingPortalSession() {
    return this.request<BillingPortalUrls>('POST', '/v1/billing/portal');
  }
```

Add `BillingPortalUrls` and `PaymentRequiredDetails` to the `./types` import list.

- [ ] **Step 5: Create `packages/shared/src/billing.ts`:**

```ts
import type { PaymentRequiredReason, Subscription } from './types';

/**
 * Client-side mirror of backend/internal/domain/subscription.go. The server
 * is authoritative (every gated call answers 402); these helpers let the
 * shells decide what to render from the subscription document they already
 * fetched, without a round-trip per screen.
 */

export const DAY_MS = 24 * 60 * 60 * 1000;
/** Card-free trial granted server-side at signup. */
export const TRIAL_LENGTH_DAYS = 14;
/** An active row stays entitled this long past currentPeriodEnd (late renewal webhook). */
export const ACTIVE_GRACE_MS = 3 * DAY_MS;
/** A past_due row stays entitled this long past currentPeriodEnd (dunning window). */
export const PAST_DUE_GRACE_MS = 7 * DAY_MS;
/** Show the trial banner during the last N days of the trial. */
export const TRIAL_BANNER_DAYS = 3;

/** Why the subscription denies access at `nowMs`, or null when it grants it. */
export function subscriptionDenialReason(
  sub: Subscription,
  nowMs: number = Date.now()
): PaymentRequiredReason | null {
  switch (sub.status) {
    case 'trialing':
      return sub.trialEndsAt && nowMs < Date.parse(sub.trialEndsAt) ? null : 'trial_ended';
    case 'active':
      return !sub.currentPeriodEnd || nowMs < Date.parse(sub.currentPeriodEnd) + ACTIVE_GRACE_MS
        ? null
        : 'past_due';
    case 'past_due':
      return sub.currentPeriodEnd && nowMs < Date.parse(sub.currentPeriodEnd) + PAST_DUE_GRACE_MS
        ? null
        : 'past_due';
    case 'paused':
      return 'paused';
    case 'canceled':
      return 'canceled';
    default:
      return 'none';
  }
}

export function subscriptionHasAccess(sub: Subscription, nowMs: number = Date.now()): boolean {
  return subscriptionDenialReason(sub, nowMs) === null;
}

/** Whole days left in the trial (ceil, floored at 0); null when not trialing. */
export function trialDaysLeft(sub: Subscription, nowMs: number = Date.now()): number | null {
  if (sub.status !== 'trialing' || !sub.trialEndsAt) return null;
  return Math.max(0, Math.ceil((Date.parse(sub.trialEndsAt) - nowMs) / DAY_MS));
}

/** True when a live provider subscription exists (manage it in the portal rather than starting a checkout). */
export function hasBillingSubscription(sub: Subscription): boolean {
  return sub.status === 'active' || sub.status === 'past_due' || sub.status === 'paused';
}
```

Add `export * from './billing';` to `packages/shared/src/index.ts`.

- [ ] **Step 6: Run the shared suite, typecheck and lint.** `bun run --cwd packages/shared test && bunx tsc -p packages/shared --noEmit && bunx biome check packages/shared` — expected: all green.

- [ ] **Step 7: Commit.**

```bash
git add packages/shared
git commit -m "feat(shared): paused status, typed 402 details, no-arg checkout, portal url triple, instance.webUrl, billing helpers" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 15: Web — paywall module (`PaywallScreen`, `TrialBanner`, `BillingGate`) mounted in the `(app)` layout; demo mocks

**Files:**
- Modify: `apps/web/components/app/paywall.tsx` (whole file)
- Create: `apps/web/components/app/paywall.test.tsx`
- Modify: `apps/web/lib/settings-data.ts` (append `startCheckout`, `openBillingPortal`)
- Modify: `apps/web/lib/settings-mock.ts` (subscription seed reads a demo status; add `createCheckout`, `portalUrls`)
- Modify: `apps/web/lib/use-instance.ts` (`DEMO_INSTANCE`: `webUrl`, `billing: true`)
- Modify: `apps/web/app/(app)/layout.tsx` (wrap `{children}` in `BillingGate`)
- Modify: `apps/web/app/(app)/settings/settings-page.tsx` (remove the `PaywallBanner` import and its `<PaywallBanner .../>` usage only; Task 17 rewrites the section)

**Interfaces:**
- Consumes: Task 14 shared exports.
- Produces: `useCheckoutMutation()`, `useBillingPortalMutation(target?: 'overview' | 'cancel' | 'updatePayment')`, `PaywallScreen({ reason, className? })`, `TrialBanner({ subscription })`, `BillingGate({ children })`; `startCheckout(): Promise<{ url: string }>`, `openBillingPortal(): Promise<BillingPortalUrls>` in `settings-data.ts`; `settingsMock.createCheckout()`, `settingsMock.portalUrls()`; demo localStorage key `calendium.demo.subscriptionStatus`.

- [ ] **Step 1: Write the failing tests** at `apps/web/components/app/paywall.test.tsx`:

```tsx
import type { BillingPortalUrls, InstanceInfo, Subscription } from '@calendium/shared';
import { ApiRequestError } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const fetchSubscriptionMock = vi.fn();
const startCheckoutMock = vi.fn();
const openBillingPortalMock = vi.fn();
vi.mock('@/lib/settings-data', () => ({
  fetchSubscription: (...args: unknown[]) => fetchSubscriptionMock(...args),
  startCheckout: (...args: unknown[]) => startCheckoutMock(...args),
  openBillingPortal: (...args: unknown[]) => openBillingPortalMock(...args),
}));

const instanceState: { data: InstanceInfo | undefined; isPending: boolean } = { data: undefined, isPending: false };
vi.mock('@/lib/use-instance', () => ({ useInstance: () => instanceState }));

const replaceMock = vi.fn();
vi.mock('next/navigation', () => ({ useRouter: () => ({ replace: replaceMock, push: vi.fn() }) }));

const signOutMock = vi.fn(async () => {});
vi.mock('@/lib/sign-out', () => ({ performSignOut: () => signOutMock() }));

const toastError = vi.fn();
vi.mock('sonner', () => ({ toast: { error: (...a: unknown[]) => toastError(...a), success: vi.fn(), info: vi.fn() } }));

import { BillingGate, PaywallScreen, TrialBanner } from './paywall';

const assignMock = vi.fn();

function instance(billing: boolean): InstanceInfo {
  return {
    name: 'Calendium', mode: 'cloud', version: 't', authBaseUrl: 'http://localhost/api/auth', authProviders: ['email'],
    undoSendSeconds: 15, webUrl: 'http://localhost:3000',
    features: { billing, google: false, microsoft: false, ai: false, push: false },
  };
}

function sub(status: Subscription['status'], extra: Partial<Subscription> = {}): Subscription {
  return { status, plan: 'annual', priceUsd: 50, currentPeriodEnd: null, cancelAtPeriodEnd: false, trialEndsAt: null, ...extra };
}

const PORTAL: BillingPortalUrls = { overviewUrl: 'https://p/o', cancelUrl: 'https://p/c', updatePaymentUrl: 'https://p/u' };

function renderWithQuery(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

beforeEach(() => {
  vi.clearAllMocks();
  window.localStorage.clear();
  instanceState.data = instance(true);
  instanceState.isPending = false;
  Object.defineProperty(window, 'location', {
    configurable: true,
    value: { ...window.location, assign: assignMock, origin: 'http://localhost:3000' },
  });
});

describe('PaywallScreen', () => {
  it.each([
    ['trial_ended', 'Your free trial has ended', 'Subscribe · $50/year'],
    ['none', 'Subscribe to keep using Calendium', 'Subscribe · $50/year'],
    ['canceled', 'Your subscription has ended', 'Subscribe · $50/year'],
    ['past_due', 'Payment failed', 'Update payment method'],
    ['paused', 'Your subscription is paused', 'Update payment method'],
  ] as const)('renders %s copy and primary action', (reason, heading, action) => {
    renderWithQuery(<PaywallScreen reason={reason} />);
    expect(screen.getByRole('heading', { name: heading })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: action })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Sign out/ })).toBeInTheDocument();
  });

  it('past_due shows a secondary Manage billing action', () => {
    renderWithQuery(<PaywallScreen reason="past_due" />);
    expect(screen.getByRole('button', { name: 'Manage billing' })).toBeInTheDocument();
  });

  it('Subscribe starts a checkout and navigates to the returned url', async () => {
    startCheckoutMock.mockResolvedValue({ url: 'http://localhost:3000/checkout?_ptxn=txn_1' });
    renderWithQuery(<PaywallScreen reason="trial_ended" />);
    await userEvent.click(screen.getByRole('button', { name: 'Subscribe · $50/year' }));
    await waitFor(() => expect(assignMock).toHaveBeenCalledWith('http://localhost:3000/checkout?_ptxn=txn_1'));
  });

  it('a 409 already_subscribed falls through to the portal overview', async () => {
    startCheckoutMock.mockRejectedValue(new ApiRequestError(409, 'already_subscribed', 'x'));
    openBillingPortalMock.mockResolvedValue(PORTAL);
    renderWithQuery(<PaywallScreen reason="none" />);
    await userEvent.click(screen.getByRole('button', { name: 'Subscribe · $50/year' }));
    await waitFor(() => expect(assignMock).toHaveBeenCalledWith('https://p/o'));
    expect(toastError).not.toHaveBeenCalled();
  });

  it('Update payment method opens the portal update link', async () => {
    openBillingPortalMock.mockResolvedValue(PORTAL);
    renderWithQuery(<PaywallScreen reason="past_due" />);
    await userEvent.click(screen.getByRole('button', { name: 'Update payment method' }));
    await waitFor(() => expect(assignMock).toHaveBeenCalledWith('https://p/u'));
  });

  it('falls back to the overview when the update link is empty', async () => {
    openBillingPortalMock.mockResolvedValue({ ...PORTAL, updatePaymentUrl: '' });
    renderWithQuery(<PaywallScreen reason="paused" />);
    await userEvent.click(screen.getByRole('button', { name: 'Update payment method' }));
    await waitFor(() => expect(assignMock).toHaveBeenCalledWith('https://p/o'));
  });

  it('Sign out ends the session and goes to /signin', async () => {
    renderWithQuery(<PaywallScreen reason="canceled" />);
    await userEvent.click(screen.getByRole('button', { name: /Sign out/ }));
    await waitFor(() => expect(signOutMock).toHaveBeenCalled());
    expect(replaceMock).toHaveBeenCalledWith('/signin');
  });

  it('surfaces a toast when the billing API fails', async () => {
    startCheckoutMock.mockRejectedValue(new ApiRequestError(502, 'billing_unavailable', 'x'));
    renderWithQuery(<PaywallScreen reason="none" />);
    await userEvent.click(screen.getByRole('button', { name: 'Subscribe · $50/year' }));
    await waitFor(() => expect(toastError).toHaveBeenCalled());
    expect(assignMock).not.toHaveBeenCalled();
  });
});

describe('TrialBanner', () => {
  const twoDays = new Date(Date.now() + 2 * 24 * 3600_000).toISOString();
  const tenDays = new Date(Date.now() + 10 * 24 * 3600_000).toISOString();

  it('shows only during the last 3 trial days', () => {
    const { rerender } = renderWithQuery(<TrialBanner subscription={sub('trialing', { trialEndsAt: tenDays })} />);
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    rerender(
      <QueryClientProvider client={new QueryClient()}>
        <TrialBanner subscription={sub('trialing', { trialEndsAt: twoDays })} />
      </QueryClientProvider>
    );
    expect(screen.getByRole('status')).toHaveTextContent(/Your free trial ends in 2 days/);
    expect(screen.getByRole('button', { name: 'Subscribe · $50/year' })).toBeInTheDocument();
  });

  it('dismiss hides it for the rest of the day and persists', async () => {
    renderWithQuery(<TrialBanner subscription={sub('trialing', { trialEndsAt: twoDays })} />);
    await userEvent.click(screen.getByRole('button', { name: 'Dismiss for today' }));
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    expect(window.localStorage.getItem('calendium.trial-banner.dismissed')).toBe(new Date().toISOString().slice(0, 10));
    renderWithQuery(<TrialBanner subscription={sub('trialing', { trialEndsAt: twoDays })} />);
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });

  it('renders nothing for non-trial subscriptions', () => {
    renderWithQuery(<TrialBanner subscription={sub('active')} />);
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });
});

describe('BillingGate', () => {
  const child = <div data-testid="app">inbox</div>;

  it('renders children immediately when billing is off', () => {
    instanceState.data = instance(false);
    renderWithQuery(<BillingGate>{child}</BillingGate>);
    expect(screen.getByTestId('app')).toBeInTheDocument();
    expect(fetchSubscriptionMock).not.toHaveBeenCalled();
  });

  it('waits for instance discovery before deciding', () => {
    instanceState.data = undefined;
    instanceState.isPending = true;
    renderWithQuery(<BillingGate>{child}</BillingGate>);
    expect(screen.queryByTestId('app')).not.toBeInTheDocument();
    expect(screen.getByRole('status', { name: 'Loading' })).toBeInTheDocument();
  });

  it('renders children for an entitled subscription', async () => {
    fetchSubscriptionMock.mockResolvedValue(sub('trialing', { trialEndsAt: new Date(Date.now() + 10 * 24 * 3600_000).toISOString() }));
    renderWithQuery(<BillingGate>{child}</BillingGate>);
    expect(await screen.findByTestId('app')).toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'Subscription required' })).not.toBeInTheDocument();
  });

  it('replaces children with the paywall for a denied subscription', async () => {
    fetchSubscriptionMock.mockResolvedValue(sub('canceled'));
    renderWithQuery(<BillingGate>{child}</BillingGate>);
    expect(await screen.findByRole('heading', { name: 'Your subscription has ended' })).toBeInTheDocument();
    expect(screen.queryByTestId('app')).not.toBeInTheDocument();
  });

  // Review Focus: fail open when the subscription cannot be fetched.
  it('fails open when the subscription request errors', async () => {
    fetchSubscriptionMock.mockRejectedValue(new Error('network down'));
    renderWithQuery(<BillingGate>{child}</BillingGate>);
    expect(await screen.findByTestId('app')).toBeInTheDocument();
  });

  it('shows the trial banner above children in the last 3 days', async () => {
    fetchSubscriptionMock.mockResolvedValue(sub('trialing', { trialEndsAt: new Date(Date.now() + 2 * 24 * 3600_000).toISOString() }));
    renderWithQuery(<BillingGate>{child}</BillingGate>);
    expect(await screen.findByTestId('app')).toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent(/trial ends/);
  });
});
```

- [ ] **Step 2: Run and confirm failure.** `bun run --cwd apps/web test components/app/paywall.test.tsx` — expected: `BillingGate`/`PaywallScreen`/`TrialBanner` are not exported.

- [ ] **Step 3: Extend `apps/web/lib/settings-mock.ts`.** Add `SubscriptionStatus` and `BillingPortalUrls` to the type import. Replace the `subscription: { status: 'trialing', ... }` seed object with `subscription: demoSubscription(now),` and add above `seed()`:

```ts
/** Demo/e2e hook: `localStorage.calendium.demo.subscriptionStatus` picks the seeded state. */
export const DEMO_SUBSCRIPTION_STATUS_KEY = 'calendium.demo.subscriptionStatus';

function demoSubscription(now: Date): Subscription {
  let status: SubscriptionStatus = 'trialing';
  try {
    const stored = window.localStorage.getItem(DEMO_SUBSCRIPTION_STATUS_KEY);
    if (stored) status = stored as SubscriptionStatus;
  } catch {
    // Private mode / SSR: keep the default.
  }
  const base = { plan: 'annual' as const, priceUsd: 50 as const, cancelAtPeriodEnd: false, trialEndsAt: null };
  switch (status) {
    case 'active':
      return { ...base, status, currentPeriodEnd: addDays(now, 300).toISOString() };
    case 'past_due':
      return { ...base, status, currentPeriodEnd: subDays(now, 10).toISOString() };
    case 'paused':
      return { ...base, status, currentPeriodEnd: subDays(now, 1).toISOString() };
    case 'canceled':
      return { ...base, status, currentPeriodEnd: subDays(now, 2).toISOString() };
    case 'none':
      return { ...base, status, currentPeriodEnd: null };
    default:
      return { ...base, status: 'trialing', currentPeriodEnd: null, trialEndsAt: addDays(now, 9).toISOString() };
  }
}
```

Add to the `settingsMock` object after `getSubscription`:

```ts
  /** Demo checkout: the overlay page shows "nothing to pay" (no _ptxn) and the mock stays as-is. */
  createCheckout(): { url: string } {
    return { url: `${window.location.origin}/checkout` };
  },

  portalUrls(): BillingPortalUrls {
    const origin = window.location.origin;
    return { overviewUrl: `${origin}/settings?tab=billing`, cancelUrl: '', updatePaymentUrl: '' };
  },
```

- [ ] **Step 4: Append to `apps/web/lib/settings-data.ts`** (add `BillingPortalUrls` to the type import):

```ts
/** POST /v1/billing/checkout — no arguments; the browser navigates to the returned URL. */
export async function startCheckout(): Promise<{ url: string }> {
  try {
    return await getApiClient().createCheckoutSession();
  } catch (err) {
    if (DEMO_MODE && !(err instanceof ApiRequestError)) return settingsMock.createCheckout();
    throw err;
  }
}

/** POST /v1/billing/portal — temporary Paddle portal links. */
export async function openBillingPortal(): Promise<BillingPortalUrls> {
  try {
    return await getApiClient().createBillingPortalSession();
  } catch (err) {
    if (DEMO_MODE && !(err instanceof ApiRequestError)) return settingsMock.portalUrls();
    throw err;
  }
}
```

and add `import { ApiRequestError } from '@calendium/shared';` (API-level errors such as 409/400 must propagate even in demo mode; only unreachable-API failures fall back).

- [ ] **Step 5: Update `apps/web/lib/use-instance.ts`.** In `DEMO_INSTANCE` set `features: { billing: true, google: true, microsoft: true, ai: true, push: false }` and add `webUrl: typeof window === 'undefined' ? 'http://localhost:3000' : window.location.origin,`. Update the comment to say billing is on so the Playwright demo covers the paywall.

- [ ] **Step 6: Replace `apps/web/components/app/paywall.tsx` with:**

```tsx
'use client';

import * as React from 'react';
import { useRouter } from 'next/navigation';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Lock, LogOut, X } from 'lucide-react';
import { toast } from 'sonner';

import type { PaymentRequiredReason, Subscription } from '@calendium/shared';
import { ApiRequestError, subscriptionDenialReason, TRIAL_BANNER_DAYS, trialDaysLeft } from '@calendium/shared';

import { Button } from '@/components/ui/button';
import { fetchSubscription, openBillingPortal, startCheckout } from '@/lib/settings-data';
import { performSignOut } from '@/lib/sign-out';
import { useInstance } from '@/lib/use-instance';
import { cn } from '@/lib/utils';

/**
 * Billing UI (docs/payments.md — Spotify model on Paddle). The web app is
 * the only purchase surface: checkout is a Paddle overlay on /checkout and
 * cancel/update-card happen in Paddle's portal; this module owns every
 * redirect into those.
 */

export type PortalTarget = 'overview' | 'cancel' | 'updatePayment';

/** Starts a checkout; a 409 already_subscribed falls through to the portal. */
export function useCheckoutMutation() {
  return useMutation({
    mutationFn: async () => {
      try {
        const { url } = await startCheckout();
        return url;
      } catch (err) {
        if (err instanceof ApiRequestError && err.code === 'already_subscribed') {
          const urls = await openBillingPortal();
          return urls.overviewUrl;
        }
        throw err;
      }
    },
    onSuccess: (url) => window.location.assign(url),
    onError: () => toast.error('Could not start checkout — the billing service is unavailable.'),
  });
}

/** Opens one of the portal links, falling back to the overview when that link is empty. */
export function useBillingPortalMutation(target: PortalTarget = 'overview') {
  return useMutation({
    mutationFn: async () => {
      const urls = await openBillingPortal();
      const picked =
        target === 'cancel' ? urls.cancelUrl : target === 'updatePayment' ? urls.updatePaymentUrl : urls.overviewUrl;
      return picked || urls.overviewUrl;
    },
    onSuccess: (url) => window.location.assign(url),
    onError: () => toast.error('Could not open billing — the billing service is unavailable.'),
  });
}

const COPY: Record<PaymentRequiredReason, { title: string; body: string }> = {
  trial_ended: {
    title: 'Your free trial has ended',
    body: 'Subscribe to keep using Calendium on web, desktop, and mobile. Your mail keeps syncing in the meantime.',
  },
  none: {
    title: 'Subscribe to keep using Calendium',
    body: 'Calendium is $50/year — one plan for email + calendar on every platform.',
  },
  canceled: {
    title: 'Your subscription has ended',
    body: 'Resubscribe any time to pick up right where you left off.',
  },
  past_due: {
    title: 'Payment failed',
    body: 'We could not charge your card and the grace period has passed. Update your payment method to restore access.',
  },
  paused: {
    title: 'Your subscription is paused',
    body: 'Update your payment method or resume the plan from billing to restore access.',
  },
};

/** Full-pane paywall rendered by the (app) layout in place of the page. */
export function PaywallScreen({ reason, className }: { reason: PaymentRequiredReason; className?: string }) {
  const router = useRouter();
  const checkout = useCheckoutMutation();
  const updatePayment = useBillingPortalMutation('updatePayment');
  const manage = useBillingPortalMutation('overview');
  const needsPayment = reason === 'past_due' || reason === 'paused';
  const copy = COPY[reason];

  return (
    <section aria-label="Subscription required" className={cn('flex h-full items-center justify-center p-6', className)}>
      <div className="flex w-full max-w-md flex-col items-center gap-4 rounded-lg border p-8 text-center">
        <div className="bg-background flex size-10 items-center justify-center rounded-md border">
          <Lock className="size-5" />
        </div>
        <h1 className="text-lg font-semibold">{copy.title}</h1>
        <p className="text-muted-foreground text-sm">{copy.body}</p>
        {needsPayment ? (
          <div className="flex flex-col gap-2 sm:flex-row">
            <Button onClick={() => updatePayment.mutate()} disabled={updatePayment.isPending}>
              Update payment method
            </Button>
            <Button variant="outline" onClick={() => manage.mutate()} disabled={manage.isPending}>
              Manage billing
            </Button>
          </div>
        ) : (
          <Button onClick={() => checkout.mutate()} disabled={checkout.isPending}>
            Subscribe · $50/year
          </Button>
        )}
        <Button
          variant="ghost"
          size="sm"
          onClick={async () => {
            await performSignOut();
            router.replace('/signin');
          }}
        >
          <LogOut /> Sign out
        </Button>
      </div>
    </section>
  );
}

const TRIAL_BANNER_KEY = 'calendium.trial-banner.dismissed';
const todayKey = () => new Date().toISOString().slice(0, 10);

/** Last-3-days trial reminder, dismissible once per calendar day. */
export function TrialBanner({ subscription }: { subscription: Subscription | null | undefined }) {
  const checkout = useCheckoutMutation();
  const [dismissedDay, setDismissedDay] = React.useState<string | null>(() => {
    try {
      return window.localStorage.getItem(TRIAL_BANNER_KEY);
    } catch {
      return null;
    }
  });
  if (!subscription) return null;
  const days = trialDaysLeft(subscription);
  if (days === null || days > TRIAL_BANNER_DAYS || dismissedDay === todayKey()) return null;
  const when = days <= 0 ? 'today' : days === 1 ? 'in 1 day' : `in ${days} days`;

  return (
    <div role="status" className="bg-muted text-muted-foreground flex shrink-0 items-center gap-3 border-b px-4 py-1.5 text-xs">
      <span className="flex-1">
        <span className="text-foreground font-medium">Your free trial ends {when}.</span> Subscribe to keep Calendium on
        every device.
      </span>
      <Button size="sm" variant="outline" onClick={() => checkout.mutate()} disabled={checkout.isPending}>
        Subscribe · $50/year
      </Button>
      <button
        type="button"
        aria-label="Dismiss for today"
        className="hover:text-foreground"
        onClick={() => {
          const key = todayKey();
          try {
            window.localStorage.setItem(TRIAL_BANNER_KEY, key);
          } catch {
            // Best effort.
          }
          setDismissedDay(key);
        }}
      >
        <X className="size-3.5" />
      </button>
    </div>
  );
}

/**
 * Gate for the authenticated shell: when the server bills, fetch the
 * subscription (which also grants the signup trial server-side) before
 * rendering the page; deny → PaywallScreen; grant → TrialBanner + page.
 * Fails OPEN when discovery or the subscription request errors — an
 * unreachable billing API must never lock a paying user out.
 */
export function BillingGate({ children }: { children: React.ReactNode }) {
  const instance = useInstance();
  const billing = !!instance.data?.features.billing;
  const subscription = useQuery({
    queryKey: ['subscription'],
    queryFn: fetchSubscription,
    enabled: billing,
    retry: 1,
    staleTime: 60_000,
    refetchInterval: 5 * 60_000,
  });

  // The (app) layout's <main> is a flex column so the banner can sit above
  // the page; the page keeps its full-height box through this wrapper.
  const page = <div className="min-h-0 flex-1">{children}</div>;

  if (instance.isPending || (billing && subscription.isPending)) {
    return (
      <div role="status" aria-label="Loading" className="flex h-full items-center justify-center">
        <div className="bg-primary size-8 animate-pulse rounded-lg" />
      </div>
    );
  }
  if (!billing || subscription.isError || !subscription.data) return page;
  const reason = subscriptionDenialReason(subscription.data);
  if (reason) return <PaywallScreen reason={reason} />;
  return (
    <>
      <TrialBanner subscription={subscription.data} />
      {page}
    </>
  );
}
```

- [ ] **Step 7: Mount the gate.** In `apps/web/app/(app)/layout.tsx` add `import { BillingGate } from '@/components/app/paywall';` and change `<main className="min-h-0 flex-1">{children}</main>` to:

```tsx
                  <main className="flex min-h-0 flex-1 flex-col">
                    <BillingGate>{children}</BillingGate>
                  </main>
```

In `settings-page.tsx` remove `PaywallBanner,` from the `@/components/app/paywall` import and delete the `{billingEnabled && (<PaywallBanner subscription={subscriptionQuery.data} className="mt-4" />)}` block.

- [ ] **Step 8: Run the web suite, typecheck and lint.** `bun run --cwd apps/web test && bunx tsc -p apps/web --noEmit && bunx biome check apps/web` — expected: green (the settings subscriptions test still passes; no `PaywallBanner` references remain: `grep -rn PaywallBanner apps/web` prints nothing).

- [ ] **Step 9: Commit.**

```bash
git add apps/web
git commit -m "feat(web): layout-level BillingGate with reason-keyed PaywallScreen, last-3-days trial banner and demo billing mocks" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 16: Web — public `/checkout` (Paddle.js overlay) and `/checkout/success` pages

**Files:**
- Create: `apps/web/lib/paddle.ts`
- Create: `apps/web/app/checkout/page.tsx`
- Create: `apps/web/app/checkout/checkout-client.tsx`
- Create: `apps/web/app/checkout/checkout-client.test.tsx`
- Create: `apps/web/app/checkout/success/page.tsx`
- Create: `apps/web/app/checkout/success/success-client.tsx`
- Create: `apps/web/app/checkout/success/success-client.test.tsx`
- Modify: `apps/web/Dockerfile` (build args for the two public Paddle vars)

**Interfaces:**
- Produces: `PADDLE_JS_SRC = 'https://cdn.paddle.com/paddle/v2/paddle.js'`; `interface PaddleJs { Environment: { set(env: 'sandbox' | 'production'): void }; Initialize(opts: PaddleInitializeOptions): void }`; `loadPaddleJs(doc?: Document): Promise<PaddleJs>`; `initPaddle(paddle: PaddleJs, opts: { token: string; env: string | undefined; successUrl: string; eventCallback?: (event: { name: string }) => void }): void`; `CheckoutClient()`; `CheckoutSuccessClient({ pollMs?: number; maxAttempts?: number })`; global `window.Paddle?: PaddleJs`.

- [ ] **Step 1: Write the failing checkout test** at `apps/web/app/checkout/checkout-client.test.tsx`:

```tsx
import { render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { CheckoutClient } from './checkout-client';

const envSet = vi.fn();
const initialize = vi.fn();

function stubPaddle() {
  (window as unknown as { Paddle?: unknown }).Paddle = { Environment: { set: envSet }, Initialize: initialize };
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubEnv('NEXT_PUBLIC_PADDLE_CLIENT_TOKEN', 'test_tok');
  vi.stubEnv('NEXT_PUBLIC_PADDLE_ENV', 'sandbox');
  stubPaddle();
});

afterEach(() => {
  vi.unstubAllEnvs();
  delete (window as unknown as { Paddle?: unknown }).Paddle;
  window.history.replaceState(null, '', '/checkout');
});

describe('/checkout', () => {
  it('initialises Paddle.js in sandbox with the fixed success url when _ptxn is present', async () => {
    window.history.replaceState(null, '', '/checkout?_ptxn=txn_123');
    render(<CheckoutClient />);
    await waitFor(() => expect(initialize).toHaveBeenCalledTimes(1));
    expect(envSet).toHaveBeenCalledWith('sandbox');
    expect(initialize).toHaveBeenCalledWith(
      expect.objectContaining({
        token: 'test_tok',
        checkout: { settings: expect.objectContaining({ successUrl: `${window.location.origin}/checkout/success`, displayMode: 'overlay' }) },
      })
    );
    expect(screen.getByText(/Opening secure checkout/)).toBeInTheDocument();
  });

  it('does not call Environment.set outside sandbox', async () => {
    vi.stubEnv('NEXT_PUBLIC_PADDLE_ENV', 'production');
    window.history.replaceState(null, '', '/checkout?_ptxn=txn_123');
    render(<CheckoutClient />);
    await waitFor(() => expect(initialize).toHaveBeenCalled());
    expect(envSet).not.toHaveBeenCalled();
  });

  it('shows "nothing to pay" without _ptxn and never initialises', async () => {
    render(<CheckoutClient />);
    expect(await screen.findByRole('heading', { name: 'Nothing to pay' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /Back to Calendium/ })).toHaveAttribute('href', '/mail');
    expect(initialize).not.toHaveBeenCalled();
  });

  // Review Focus: a missing client token must be loud, not a blank page.
  it('reports a configuration error when the client token is unset', async () => {
    vi.stubEnv('NEXT_PUBLIC_PADDLE_CLIENT_TOKEN', '');
    window.history.replaceState(null, '', '/checkout?_ptxn=txn_123');
    render(<CheckoutClient />);
    expect(await screen.findByRole('alert')).toHaveTextContent(/NEXT_PUBLIC_PADDLE_CLIENT_TOKEN/);
    expect(initialize).not.toHaveBeenCalled();
  });
});
```

And `apps/web/app/checkout/success/success-client.test.tsx`:

```tsx
import type { Subscription } from '@calendium/shared';
import { act, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const session: { data: { user: { id: string } } | null; isPending: boolean } = { data: { user: { id: 'u1' } }, isPending: false };
vi.mock('@/lib/auth-client', () => ({ authClient: { useSession: () => session } }));

const getSubscriptionMock = vi.fn();
vi.mock('@/lib/api', () => ({ getApiClient: () => ({ getSubscription: (...a: unknown[]) => getSubscriptionMock(...a) }) }));

import { CheckoutSuccessClient } from './success-client';

function sub(status: Subscription['status']): Subscription {
  return { status, plan: 'annual', priceUsd: 50, currentPeriodEnd: null, cancelAtPeriodEnd: false, trialEndsAt: null };
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.clearAllMocks();
  session.data = { user: { id: 'u1' } };
  session.isPending = false;
});
afterEach(() => vi.useRealTimers());

describe('/checkout/success', () => {
  it('polls every 2s until the subscription is active, then links to the app', async () => {
    getSubscriptionMock.mockResolvedValueOnce(sub('trialing')).mockResolvedValueOnce(sub('active'));
    render(<CheckoutSuccessClient />);
    await act(() => vi.advanceTimersByTimeAsync(0));
    expect(screen.getByText(/Activating/)).toBeInTheDocument();
    expect(getSubscriptionMock).toHaveBeenCalledTimes(1);
    await act(() => vi.advanceTimersByTimeAsync(2000));
    expect(getSubscriptionMock).toHaveBeenCalledTimes(2);
    expect(screen.getByRole('heading', { name: "You're all set" })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /Open Calendium/ })).toHaveAttribute('href', '/mail');
  });

  it('gives up after maxAttempts and still links to the app', async () => {
    getSubscriptionMock.mockResolvedValue(sub('trialing'));
    render(<CheckoutSuccessClient pollMs={2000} maxAttempts={3} />);
    await act(() => vi.advanceTimersByTimeAsync(0));
    await act(() => vi.advanceTimersByTimeAsync(2000));
    await act(() => vi.advanceTimersByTimeAsync(2000));
    expect(getSubscriptionMock).toHaveBeenCalledTimes(3);
    expect(screen.getByText(/taking longer than usual/)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /Open Calendium/ })).toHaveAttribute('href', '/mail');
  });

  it('tolerates transient polling errors', async () => {
    getSubscriptionMock.mockRejectedValueOnce(new Error('net')).mockResolvedValueOnce(sub('active'));
    render(<CheckoutSuccessClient />);
    await act(() => vi.advanceTimersByTimeAsync(0));
    await act(() => vi.advanceTimersByTimeAsync(2000));
    expect(screen.getByRole('heading', { name: "You're all set" })).toBeInTheDocument();
  });

  it('without a session tells the user to return to the app and never polls', async () => {
    session.data = null;
    render(<CheckoutSuccessClient />);
    await act(() => vi.advanceTimersByTimeAsync(5000));
    expect(screen.getByText(/Return to the app/)).toBeInTheDocument();
    expect(getSubscriptionMock).not.toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run and confirm failure.** `bun run --cwd apps/web test app/checkout` — expected: modules `./checkout-client` / `./success-client` not found.

- [ ] **Step 3: Create `apps/web/lib/paddle.ts`:**

```ts
/**
 * Paddle.js v2 loader (overlay checkout, docs/payments.md). Loaded only on
 * /checkout; the script tag is injected once and resolved from window.Paddle.
 */

export const PADDLE_JS_SRC = 'https://cdn.paddle.com/paddle/v2/paddle.js';

export interface PaddleCheckoutSettings {
  successUrl?: string;
  displayMode?: 'overlay' | 'inline';
}

export interface PaddleInitializeOptions {
  token: string;
  eventCallback?: (event: { name: string }) => void;
  checkout?: { settings: PaddleCheckoutSettings };
}

export interface PaddleJs {
  Environment: { set(env: 'sandbox' | 'production'): void };
  Initialize(options: PaddleInitializeOptions): void;
}

declare global {
  interface Window {
    Paddle?: PaddleJs;
  }
}

const SCRIPT_ID = 'paddle-js';

/** Resolves window.Paddle, injecting the CDN script once if needed. */
export function loadPaddleJs(doc: Document = document): Promise<PaddleJs> {
  if (window.Paddle) return Promise.resolve(window.Paddle);
  return new Promise((resolve, reject) => {
    const existing = doc.getElementById(SCRIPT_ID) as HTMLScriptElement | null;
    const script = existing ?? doc.createElement('script');
    const done = () => (window.Paddle ? resolve(window.Paddle) : reject(new Error('Paddle.js loaded without window.Paddle')));
    script.addEventListener('load', done, { once: true });
    script.addEventListener('error', () => reject(new Error('Could not load Paddle.js')), { once: true });
    if (!existing) {
      script.id = SCRIPT_ID;
      script.src = PADDLE_JS_SRC;
      script.async = true;
      doc.head.appendChild(script);
    }
  });
}

/** Environment.set('sandbox') when NEXT_PUBLIC_PADDLE_ENV=sandbox, then Initialize with the fixed success URL. */
export function initPaddle(
  paddle: PaddleJs,
  opts: { token: string; env: string | undefined; successUrl: string; eventCallback?: (event: { name: string }) => void }
): void {
  if (opts.env === 'sandbox') paddle.Environment.set('sandbox');
  paddle.Initialize({
    token: opts.token,
    eventCallback: opts.eventCallback,
    checkout: { settings: { successUrl: opts.successUrl, displayMode: 'overlay' } },
  });
}
```

- [ ] **Step 4: Create `apps/web/app/checkout/checkout-client.tsx`:**

```tsx
'use client';

import * as React from 'react';
import Link from 'next/link';
import { CalendarRange, Loader2 } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { initPaddle, loadPaddleJs } from '@/lib/paddle';

type State =
  | { kind: 'loading' }
  | { kind: 'ready' }
  | { kind: 'no-transaction' }
  | { kind: 'error'; message: string };

/**
 * Paddle's default payment link points here. Paddle.js opens the overlay
 * for the `_ptxn` transaction created by POST /v1/billing/checkout; the
 * success URL is fixed to /checkout/success (never client-supplied).
 */
export function CheckoutClient() {
  const [state, setState] = React.useState<State>({ kind: 'loading' });

  React.useEffect(() => {
    const txn = new URLSearchParams(window.location.search).get('_ptxn');
    if (!txn) {
      setState({ kind: 'no-transaction' });
      return;
    }
    const token = process.env.NEXT_PUBLIC_PADDLE_CLIENT_TOKEN;
    if (!token) {
      setState({ kind: 'error', message: 'Checkout is not configured on this deployment: NEXT_PUBLIC_PADDLE_CLIENT_TOKEN is unset.' });
      return;
    }
    let cancelled = false;
    loadPaddleJs()
      .then((paddle) => {
        if (cancelled) return;
        initPaddle(paddle, {
          token,
          env: process.env.NEXT_PUBLIC_PADDLE_ENV,
          successUrl: `${window.location.origin}/checkout/success`,
        });
        setState({ kind: 'ready' });
      })
      .catch(() => {
        if (!cancelled) setState({ kind: 'error', message: 'Could not load the payment form. Check your connection and reload the page.' });
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <main className="flex min-h-svh flex-col items-center justify-center px-6 text-center">
      <div className="bg-primary text-primary-foreground flex size-12 items-center justify-center rounded-xl">
        <CalendarRange className="size-6" />
      </div>
      {state.kind === 'no-transaction' && (
        <>
          <h1 className="mt-6 text-2xl font-semibold tracking-tight">Nothing to pay</h1>
          <p className="text-muted-foreground mt-2 max-w-sm text-sm">
            This page opens a checkout started from the app. Start one from Settings → Billing.
          </p>
          <Button asChild className="mt-6">
            <Link href="/mail">Back to Calendium</Link>
          </Button>
        </>
      )}
      {(state.kind === 'loading' || state.kind === 'ready') && (
        <p className="text-muted-foreground mt-6 flex items-center gap-2 text-sm">
          <Loader2 className="size-4 animate-spin" /> Opening secure checkout…
        </p>
      )}
      {state.kind === 'error' && (
        <p role="alert" className="text-destructive mt-6 max-w-sm text-sm">
          {state.message}
        </p>
      )}
    </main>
  );
}
```

Create `apps/web/app/checkout/page.tsx`:

```tsx
import type { Metadata } from 'next';

import { CheckoutClient } from './checkout-client';

export const metadata: Metadata = { title: 'Checkout', robots: { index: false } };

/** Public (no auth gate): Paddle's default payment link lands here. */
export default function CheckoutPage() {
  return <CheckoutClient />;
}
```

- [ ] **Step 5: Create `apps/web/app/checkout/success/success-client.tsx`:**

```tsx
'use client';

import * as React from 'react';
import Link from 'next/link';
import { CalendarRange, CheckCircle2, Loader2 } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { getApiClient } from '@/lib/api';
import { authClient } from '@/lib/auth-client';

type Status = 'polling' | 'active' | 'timeout';

/**
 * Paddle redirects here after a completed checkout. The webhook activates
 * the subscription asynchronously, so with a session we poll
 * GET /v1/billing/subscription every 2 s for up to 60 s.
 */
export function CheckoutSuccessClient({ pollMs = 2000, maxAttempts = 30 }: { pollMs?: number; maxAttempts?: number }) {
  const { data: session, isPending } = authClient.useSession();
  const [status, setStatus] = React.useState<Status>('polling');

  React.useEffect(() => {
    if (isPending || !session) return;
    let attempts = 0;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    const tick = async () => {
      attempts += 1;
      try {
        const sub = await getApiClient().getSubscription();
        if (sub.status === 'active') {
          if (!stopped) setStatus('active');
          return;
        }
      } catch {
        // Transient; keep polling.
      }
      if (stopped) return;
      if (attempts >= maxAttempts) {
        setStatus('timeout');
        return;
      }
      timer = setTimeout(tick, pollMs);
    };
    timer = setTimeout(tick, 0);
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [isPending, session, pollMs, maxAttempts]);

  const signedOut = !isPending && !session;

  return (
    <main className="flex min-h-svh flex-col items-center justify-center px-6 text-center">
      <div className="bg-primary text-primary-foreground flex size-12 items-center justify-center rounded-xl">
        <CalendarRange className="size-6" />
      </div>
      <h1 className="mt-6 text-2xl font-semibold tracking-tight">
        {status === 'active' ? "You're all set" : 'Payment received'}
      </h1>
      {signedOut ? (
        <p className="text-muted-foreground mt-2 max-w-sm text-sm">Return to the app to continue — your subscription activates within a minute.</p>
      ) : status === 'active' ? (
        <p className="text-muted-foreground mt-2 flex items-center gap-2 text-sm">
          <CheckCircle2 className="size-4 text-emerald-500" /> Your Calendium Annual plan is active.
        </p>
      ) : status === 'timeout' ? (
        <p className="text-muted-foreground mt-2 max-w-sm text-sm">Activation is taking longer than usual. It will finish in the background — open the app and refresh in a minute.</p>
      ) : (
        <p className="text-muted-foreground mt-2 flex items-center gap-2 text-sm">
          <Loader2 className="size-4 animate-spin" /> Activating your subscription…
        </p>
      )}
      {(signedOut || status !== 'polling') && (
        <Button asChild className="mt-6">
          <Link href="/mail">Open Calendium</Link>
        </Button>
      )}
    </main>
  );
}
```

Create `apps/web/app/checkout/success/page.tsx`:

```tsx
import type { Metadata } from 'next';

import { CheckoutSuccessClient } from './success-client';

export const metadata: Metadata = { title: 'Payment received', robots: { index: false } };

/** Public: Paddle's success URL. */
export default function CheckoutSuccessPage() {
  return <CheckoutSuccessClient />;
}
```

- [ ] **Step 6: Dockerfile build args.** In `apps/web/Dockerfile`, after `ENV NEXT_PUBLIC_API_URL=$NEXT_PUBLIC_API_URL` add:

```dockerfile
# Paddle.js overlay checkout (docs/payments.md): public client token + env.
ARG NEXT_PUBLIC_PADDLE_CLIENT_TOKEN
ARG NEXT_PUBLIC_PADDLE_ENV=sandbox
ENV NEXT_PUBLIC_PADDLE_CLIENT_TOKEN=$NEXT_PUBLIC_PADDLE_CLIENT_TOKEN \
    NEXT_PUBLIC_PADDLE_ENV=$NEXT_PUBLIC_PADDLE_ENV
```

- [ ] **Step 7: Run.** `bun run --cwd apps/web test app/checkout && bunx tsc -p apps/web --noEmit && bunx biome check apps/web` — expected: green.

- [ ] **Step 8: Commit.**

```bash
git add apps/web
git commit -m "feat(web): public /checkout Paddle.js overlay page and /checkout/success activation polling" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 17: Web — Settings billing section, cloud-only sign-in footer, Playwright paywall spec

**Files:**
- Modify: `apps/web/app/(app)/settings/settings-page.tsx` (the `?checkout=` effect ~L185–200; the `BillingSection` ~L1420–1545)
- Create: `apps/web/app/(app)/settings/settings-billing.test.tsx`
- Modify: `apps/web/app/signin/page.tsx` (footer paragraph)
- Create: `apps/web/e2e/paywall.spec.ts`

**Interfaces:**
- Consumes: Task 15 hooks, Task 14 helpers.
- Produces: `export function BillingSection({ subscription, loading })` (named export for tests).

- [ ] **Step 1: Write the failing settings test** at `apps/web/app/(app)/settings/settings-billing.test.tsx`:

```tsx
import type { Subscription } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const startCheckoutMock = vi.fn();
const openBillingPortalMock = vi.fn();
vi.mock('@/lib/settings-data', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/settings-data')>()),
  startCheckout: (...a: unknown[]) => startCheckoutMock(...a),
  openBillingPortal: (...a: unknown[]) => openBillingPortalMock(...a),
}));
vi.mock('@/lib/api', () => ({ getApiClient: () => ({}) }));
vi.mock('sonner', () => ({ toast: { error: vi.fn(), success: vi.fn(), info: vi.fn() } }));

import { BillingSection } from './settings-page';

const assignMock = vi.fn();
function sub(status: Subscription['status'], extra: Partial<Subscription> = {}): Subscription {
  return { status, plan: 'annual', priceUsd: 50, currentPeriodEnd: null, cancelAtPeriodEnd: false, trialEndsAt: null, ...extra };
}
function renderSection(s: Subscription | undefined) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <BillingSection subscription={s} loading={false} />
    </QueryClientProvider>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  Object.defineProperty(window, 'location', { configurable: true, value: { ...window.location, assign: assignMock } });
});

describe('Settings → Billing', () => {
  it('trialing: status line with the end date, Subscribe only', () => {
    renderSection(sub('trialing', { trialEndsAt: '2026-10-18T09:00:00Z' }));
    expect(screen.getByText(/Free trial — ends October 18, 2026/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Subscribe · $50/year' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Manage billing' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Cancel subscription' })).not.toBeInTheDocument();
  });

  it('active: renews on date, Manage billing + Cancel subscription, no Subscribe', async () => {
    openBillingPortalMock.mockResolvedValue({ overviewUrl: 'https://p/o', cancelUrl: 'https://p/c', updatePaymentUrl: 'https://p/u' });
    renderSection(sub('active', { currentPeriodEnd: '2027-10-01T12:00:00Z' }));
    expect(screen.getByText(/Renews on October 1, 2027/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Subscribe · $50/year' })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Cancel subscription' }));
    await waitFor(() => expect(assignMock).toHaveBeenCalledWith('https://p/c'));
    await userEvent.click(screen.getByRole('button', { name: 'Manage billing' }));
    await waitFor(() => expect(assignMock).toHaveBeenCalledWith('https://p/o'));
  });

  it('active with scheduled cancel: cancels on date', () => {
    renderSection(sub('active', { currentPeriodEnd: '2027-10-01T12:00:00Z', cancelAtPeriodEnd: true }));
    expect(screen.getByText(/Cancels on October 1, 2027/)).toBeInTheDocument();
  });

  it('past_due: Update payment method is the primary action', async () => {
    openBillingPortalMock.mockResolvedValue({ overviewUrl: 'https://p/o', cancelUrl: 'https://p/c', updatePaymentUrl: 'https://p/u' });
    renderSection(sub('past_due', { currentPeriodEnd: '2026-10-01T12:00:00Z' }));
    await userEvent.click(screen.getByRole('button', { name: 'Update payment method' }));
    await waitFor(() => expect(assignMock).toHaveBeenCalledWith('https://p/u'));
  });

  it('paused: shows the paused badge and payment action', () => {
    renderSection(sub('paused'));
    expect(screen.getByText('Paused')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Update payment method' })).toBeInTheDocument();
  });

  it('canceled: Subscribe again, no portal buttons', () => {
    renderSection(sub('canceled'));
    expect(screen.getByRole('button', { name: 'Subscribe · $50/year' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Cancel subscription' })).not.toBeInTheDocument();
  });

  it('mentions Paddle as merchant of record and never Stripe', () => {
    renderSection(sub('none'));
    expect(screen.getByText(/Paddle/)).toBeInTheDocument();
    expect(screen.queryByText(/Stripe/)).not.toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run and confirm failure.** `bun run --cwd apps/web test settings-billing` — expected: `BillingSection` is not exported / old copy assertions fail.

- [ ] **Step 3: Rewrite the settings billing section.** In `settings-page.tsx`: (a) in the deep-link `useEffect`, delete the `const checkout = params.get('checkout'); ... if (checkout) window.history.replaceState(...)` lines so only the `?tab=`/`#hash` handling remains and change the comment to `// Deep-link section via ?tab= (primary) or #hash.`; (b) replace the whole `BillingSection` (from its `// Billing` banner comment to the closing brace) with:

```tsx
// ---------------------------------------------------------------------------
// Billing (docs/payments.md — $50/yr on Paddle, Spotify model)
// ---------------------------------------------------------------------------

export function BillingSection({
  subscription,
  loading,
}: {
  subscription: Subscription | undefined;
  loading: boolean;
}) {
  const checkout = useCheckoutMutation();
  const manage = useBillingPortalMutation('overview');
  const cancel = useBillingPortalMutation('cancel');
  const updatePayment = useBillingPortalMutation('updatePayment');

  if (loading) return <Skeleton className="h-56 w-full" />;

  const sub = subscription;
  const fmt = (iso: string) => format(new Date(iso), 'MMMM d, yyyy');
  const live = !!sub && hasBillingSubscription(sub);
  const daysLeft = sub ? trialDaysLeft(sub) : null;

  const badge = (() => {
    switch (sub?.status) {
      case 'trialing':
        return <Badge variant="secondary">Free trial</Badge>;
      case 'active':
        return (
          <Badge variant="outline" className="gap-1.5">
            <span className="size-1.5 rounded-full bg-emerald-500" />
            Active
          </Badge>
        );
      case 'past_due':
        return <Badge variant="destructive">Payment failed</Badge>;
      case 'paused':
        return <Badge variant="outline">Paused</Badge>;
      case 'canceled':
        return <Badge variant="outline">Canceled</Badge>;
      default:
        return <Badge variant="outline">No subscription</Badge>;
    }
  })();

  const statusLine = (() => {
    if (!sub) return 'No subscription yet.';
    switch (sub.status) {
      case 'trialing':
        return sub.trialEndsAt
          ? `Free trial — ends ${fmt(sub.trialEndsAt)}${daysLeft !== null ? ` (${daysLeft} day${daysLeft === 1 ? '' : 's'} left)` : ''}. Subscribe any time; billing starts only when you do.`
          : 'Free trial.';
      case 'active':
        if (!sub.currentPeriodEnd) return 'Active.';
        return sub.cancelAtPeriodEnd
          ? `Cancels on ${fmt(sub.currentPeriodEnd)} — access continues until then.`
          : `Renews on ${fmt(sub.currentPeriodEnd)} for $50.`;
      case 'past_due':
        return 'We could not charge your card. Update your payment method to keep access.';
      case 'paused':
        return 'Your plan is paused. Update your payment method or resume it from billing.';
      case 'canceled':
        return 'Your subscription has ended. Resubscribe any time.';
      default:
        return 'You do not have an active subscription.';
    }
  })();

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <CreditCard className="size-4" />
            Calendium Annual
          </CardTitle>
          <CardDescription>
            One plan - $50/year after a 14-day free trial. Unlocks web, desktop, and mobile.
          </CardDescription>
          <CardAction>{badge}</CardAction>
        </CardHeader>
        <CardContent className="text-sm">
          <p>{statusLine}</p>
        </CardContent>
        <CardFooter className="gap-2 border-t pt-6">
          {live ? (
            <>
              {(sub?.status === 'past_due' || sub?.status === 'paused') && (
                <Button onClick={() => updatePayment.mutate()} disabled={updatePayment.isPending}>
                  Update payment method
                </Button>
              )}
              <Button variant="outline" onClick={() => manage.mutate()} disabled={manage.isPending}>
                Manage billing
                <ExternalLink />
              </Button>
              <Button variant="ghost" onClick={() => cancel.mutate()} disabled={cancel.isPending}>
                Cancel subscription
              </Button>
            </>
          ) : (
            <Button onClick={() => checkout.mutate()} disabled={checkout.isPending}>
              Subscribe · $50/year
            </Button>
          )}
        </CardFooter>
      </Card>
      <p className="text-xs text-muted-foreground">
        Billing runs through Paddle, our merchant of record: invoices, receipts, and sales tax or VAT
        are handled by Paddle. Checkout always happens on the web - the mobile and desktop apps never
        charge you directly.
      </p>
    </div>
  );
}
```

Add `hasBillingSubscription` and `trialDaysLeft` to the `@calendium/shared` value import. Remove the now-unused `formatDistanceToNow` import if nothing else uses it (check with `bunx tsc`).

- [ ] **Step 4: Cloud-only sign-in footer.** In `apps/web/app/signin/page.tsx` replace the footer `<p>` with:

```tsx
        <p className="text-muted-foreground mt-8 text-center text-xs text-balance">
          {instance?.features.billing ? '14-day free trial, then $50/year. ' : ''}By continuing you agree to the{' '}
          <Link href="/terms" className="hover:text-foreground underline underline-offset-2">
            Terms
          </Link>{' '}
          and{' '}
          <Link href="/privacy" className="hover:text-foreground underline underline-offset-2">
            Privacy Policy
          </Link>
          .
        </p>
```

- [ ] **Step 5: Playwright paywall spec** at `apps/web/e2e/paywall.spec.ts`:

```ts
import { expect, test } from './fixtures';

/**
 * Demo-mode paywall: DEMO_INSTANCE advertises billing and lib/settings-mock
 * seeds the subscription from localStorage.calendium.demo.subscriptionStatus.
 */
test.describe('Paywall (demo mode)', () => {
  test('a canceled subscription replaces the inbox with the paywall', async ({ page }) => {
    await page.addInitScript(() => {
      window.localStorage.setItem('calendium.demo.subscriptionStatus', 'canceled');
    });
    await page.goto('/mail');
    await expect(page.getByRole('heading', { name: 'Your subscription has ended' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Subscribe · $50/year' })).toBeVisible();
    await expect(page.getByRole('button', { name: /Postmortem: checkout latency spike/ })).toHaveCount(0);
  });

  test('a past-due subscription asks for a payment method', async ({ page }) => {
    await page.addInitScript(() => {
      window.localStorage.setItem('calendium.demo.subscriptionStatus', 'past_due');
    });
    await page.goto('/mail');
    await expect(page.getByRole('heading', { name: 'Payment failed' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Update payment method' })).toBeVisible();
  });

  test('the default trial keeps the inbox and shows no banner (9 days left)', async ({ page }) => {
    await page.goto('/mail');
    await expect(page.getByRole('button', { name: /Postmortem: checkout latency spike/ })).toBeVisible();
    await expect(page.getByText(/Your free trial ends/)).toHaveCount(0);
  });

  test('/checkout without a transaction says there is nothing to pay', async ({ page }) => {
    await page.goto('/checkout');
    await expect(page.getByRole('heading', { name: 'Nothing to pay' })).toBeVisible();
  });
});
```

- [ ] **Step 6: Run.** `bun run --cwd apps/web test && bunx tsc -p apps/web --noEmit && bunx biome check apps/web && bun run --cwd apps/web e2e e2e/paywall.spec.ts` — expected: unit + e2e green.

- [ ] **Step 7: Commit.**

```bash
git add apps/web
git commit -m "feat(web): Paddle billing settings section, cloud-only sign-in footer and demo-mode paywall e2e" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 18: Web — marketing/legal copy (Stripe → Paddle), billing-gated pricing CTA, sample-data sweep

**Files:**
- Create: `apps/web/components/marketing/start-trial-cta.tsx`
- Create: `apps/web/components/marketing/start-trial-cta.test.tsx`
- Modify: `apps/web/app/(marketing)/pricing/page.tsx` (FAQ answers L66–87; CTA block ~L178–186; billing section lede ~L246)
- Modify: `apps/web/app/(marketing)/terms/page.tsx` (~L51–53)
- Modify: `apps/web/app/(marketing)/privacy/page.tsx` (~L46–47, ~L83)
- Modify: `apps/web/app/(marketing)/docs/self-hosting/page.tsx` (~L51)
- Modify: `apps/web/components/marketing/final-cta.tsx` (~L35)
- Modify: `apps/web/components/marketing/product-mock.tsx` (~L41 sample sender)
- Modify: `apps/web/lib/mail-mock.ts` (~L311 and ~L317 sample text)

**Interfaces:**
- Produces: `StartTrialCta({ className? })` — renders the "Start free trial" link only when `features.billing` is true, otherwise a "Self-host for free" link.

- [ ] **Step 1: Write the failing CTA test** at `apps/web/components/marketing/start-trial-cta.test.tsx`:

```tsx
import type { InstanceInfo } from '@calendium/shared';
import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const state: { data: InstanceInfo | undefined; isPending: boolean } = { data: undefined, isPending: false };
vi.mock('@/lib/use-instance', () => ({ useInstance: () => state }));

import { START_TRIAL_HREF, SELF_HOSTING_DOCS_HREF } from './links';
import { StartTrialCta } from './start-trial-cta';

function instance(billing: boolean): InstanceInfo {
  return {
    name: 'C', mode: billing ? 'cloud' : 'self_host', version: 't', authBaseUrl: 'x', authProviders: ['email'], undoSendSeconds: 15, webUrl: 'http://localhost:3000',
    features: { billing, google: false, microsoft: false, ai: false, push: false },
  };
}

beforeEach(() => {
  state.data = undefined;
  state.isPending = false;
});

describe('StartTrialCta', () => {
  it('links to the trial funnel when the server bills', () => {
    state.data = instance(true);
    render(<StartTrialCta />);
    expect(screen.getByRole('link', { name: /Start free trial/ })).toHaveAttribute('href', START_TRIAL_HREF);
  });
  it('offers self-hosting when billing is off', () => {
    state.data = instance(false);
    render(<StartTrialCta />);
    expect(screen.queryByRole('link', { name: /Start free trial/ })).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: /Self-host for free/ })).toHaveAttribute('href', SELF_HOSTING_DOCS_HREF);
  });
  it('is disabled while discovery is pending', () => {
    state.isPending = true;
    render(<StartTrialCta />);
    expect(screen.getByRole('button', { name: /Start free trial/ })).toBeDisabled();
  });
});
```

- [ ] **Step 2: Run and confirm failure.** `bun run --cwd apps/web test start-trial-cta` — expected: module not found.

- [ ] **Step 3: Create `apps/web/components/marketing/start-trial-cta.tsx`:**

```tsx
'use client';

import Link from 'next/link';
import { ArrowRight } from 'lucide-react';

import { SELF_HOSTING_DOCS_HREF, START_TRIAL_HREF } from '@/components/marketing/links';
import { Button } from '@/components/ui/button';
import { useInstance } from '@/lib/use-instance';

/** Pricing CTA: the trial funnel exists only when the server bills (cloud). */
export function StartTrialCta({ className }: { className?: string }) {
  const { data: instance, isPending } = useInstance();
  if (isPending) {
    return (
      <Button size="lg" className={className} disabled>
        Start free trial
      </Button>
    );
  }
  if (!instance?.features.billing) {
    return (
      <Button asChild size="lg" variant="outline" className={className}>
        <Link href={SELF_HOSTING_DOCS_HREF}>Self-host for free</Link>
      </Button>
    );
  }
  return (
    <Button asChild size="lg" className={className}>
      <Link href={START_TRIAL_HREF}>
        Start free trial
        <ArrowRight />
      </Link>
    </Button>
  );
}
```

- [ ] **Step 4: Pricing page.** In `pricing/page.tsx` replace the `<Button asChild size="lg" className="w-full"><Link href={START_TRIAL_HREF}>Start free trial<ArrowRight /></Link></Button>` block with `<StartTrialCta className="w-full" />` (import it; drop `START_TRIAL_HREF`/`ArrowRight` imports if now unused). Replace copy: `Checkout and billing by Stripe. No in-app purchases, ever.` → `Checkout and billing by Paddle, our merchant of record. No in-app purchases, ever.`; the "How does billing work?" answer → `'On Cloud, checkout runs on Paddle, our merchant of record. You start with a 14-day free trial; when it ends, subscribe for $50 a year and the plan renews yearly. Paddle handles invoices, receipts, and sales tax or VAT for your country. Self-hosting has no billing at all.'`; the cancel answer → `'Anytime, in about three clicks: Settings → Billing → Cancel subscription opens the Paddle customer portal, where you can cancel, update your card, or download invoices. Your access continues until the end of the period you paid for.'`; the payment-methods answer → `'Everything Paddle Checkout supports: major credit and debit cards, plus Apple Pay, Google Pay, and PayPal where available.'`; the billing section lede → `"Paddle handles every Cloud charge as merchant of record; you stay in control from the customer portal. Self-hosting has no billing at all."`; the taxes footnote → `Cloud prices in USD before tax. Paddle shows the exact total, including any sales tax or VAT, at checkout.`

- [ ] **Step 5: Legal and docs pages.** `terms/page.tsx`: `Billing runs through Stripe; when your trial ends, your payment method is charged and the plan renews annually until you cancel.` → `Billing runs through Paddle, our merchant of record; once you subscribe, the plan renews annually until you cancel.` `privacy/page.tsx`: `Subscriptions run through Stripe. We store your subscription status and customer ID; your card details are held by Stripe, not us.` → `Subscriptions run through Paddle, our merchant of record. We store your subscription status and Paddle customer ID; your card details are held by Paddle, not us.` and `<strong>Stripe</strong> — payment processing and billing for Cloud subscriptions.` → `<strong>Paddle</strong> — merchant of record: payment processing, invoicing and tax for Cloud subscriptions.` `docs/self-hosting/page.tsx`: `turns Stripe off` → `turns Paddle billing off`. `final-cta.tsx`: `checkout by Stripe` → `checkout by Paddle`.

- [ ] **Step 6: Sample-data sweep.** `product-mock.tsx`: `sender: 'Stripe'` → `sender: 'Mercury'`. `mail-mock.ts`: `from your Stripe days` → `from your payments days` (both occurrences, the message text and the quoted reply).

- [ ] **Step 7: Verify no Stripe remains in the web app and everything is green.** `grep -rni stripe apps/web --exclude-dir=node_modules --exclude-dir=.next` — expected: no output. `bun run --cwd apps/web test && bunx tsc -p apps/web --noEmit && bunx biome check apps/web` — expected: green.

- [ ] **Step 8: Commit.**

```bash
git add apps/web
git commit -m "feat(web): marketing and legal copy on Paddle as merchant of record; billing-gated pricing CTA" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 19: Mobile — read-only paywall screen, tabs gate, `webUrl` links

**Files:**
- Create: `apps/mobile/components/paywall-screen.tsx`
- Create: `apps/mobile/components/paywall-screen.test.tsx`
- Modify: `apps/mobile/app/(tabs)/_layout.tsx` (subscription gate)
- Create: `apps/mobile/app/(tabs)/_layout.paywall.test.tsx`
- Modify: `apps/mobile/app/(tabs)/settings.tsx` (`SubscriptionCard` ~L483–548)
- Modify: `apps/mobile/app/(tabs)/settings.test.tsx` (only if it asserts the old hardcoded pricing URL)
- Modify: `apps/mobile/lib/server-config.ts` (`ServerConfig.webUrl`, `DEMO_CONFIG`, `discoverServer`)

**Interfaces:**
- Consumes: `PaymentRequiredReason`, `Subscription`, `subscriptionDenialReason`, `hasBillingSubscription` from `@calendium/shared` (Task 14); `useAuth().signOut`; `useServerConfig().config`.
- Produces: `PaywallScreen({ reason, webUrl }: { reason: PaymentRequiredReason; webUrl: string | null })`; `ServerConfig.webUrl: string`.

- [ ] **Step 1: Write the failing screen test** at `apps/mobile/components/paywall-screen.test.tsx`:

```tsx
const mockSignOut = jest.fn();
jest.mock('@/context/auth', () => ({
  __esModule: true,
  default: () => ({ signOut: mockSignOut }),
}));

const mockOpenBrowser = jest.fn();
jest.mock('expo-web-browser', () => ({
  openBrowserAsync: (...args: unknown[]) => mockOpenBrowser(...args),
}));

jest.mock('react-native-safe-area-context', () => ({
  useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }),
}));

import { fireEvent, render, screen } from '@testing-library/react-native';
import { PaywallScreen } from './paywall-screen';

beforeEach(() => jest.clearAllMocks());

describe('PaywallScreen (mobile, read-only)', () => {
  it.each([
    ['trial_ended', 'Your free trial has ended'],
    ['none', 'Subscribe to keep using Calendium'],
    ['canceled', 'Your subscription has ended'],
    ['past_due', 'Payment failed'],
    ['paused', 'Your subscription is paused'],
  ] as const)('renders %s copy with no purchase button', (reason, title) => {
    render(<PaywallScreen reason={reason} webUrl="https://web.example" />);
    expect(screen.getByText(title)).toBeTruthy();
    expect(screen.queryByText(/Subscribe/)).toBeNull();
    expect(screen.getByText('Manage on the web')).toBeTruthy();
  });

  it('opens <webUrl>/pricing in the browser', () => {
    render(<PaywallScreen reason="trial_ended" webUrl="https://web.example" />);
    fireEvent.press(screen.getByText('Manage on the web'));
    expect(mockOpenBrowser).toHaveBeenCalledWith('https://web.example/pricing');
  });

  it('hides the web link when the server advertises no webUrl', () => {
    render(<PaywallScreen reason="none" webUrl={null} />);
    expect(screen.queryByText('Manage on the web')).toBeNull();
  });

  it('signs out', () => {
    render(<PaywallScreen reason="canceled" webUrl={null} />);
    fireEvent.press(screen.getByText('Sign out'));
    expect(mockSignOut).toHaveBeenCalled();
  });
});
```

And the gate test at `apps/mobile/app/(tabs)/_layout.paywall.test.tsx` (same mock shape as `_layout.test.tsx`):

```tsx
const mockUseAuth = jest.fn();
jest.mock('@/context/auth', () => ({
  __esModule: true,
  default: (...args: unknown[]) => mockUseAuth(...args),
}));
jest.mock('@/hooks/use-push-registration', () => ({ usePushRegistration: jest.fn() }));

const mockUseServerConfig = jest.fn();
jest.mock('@/lib/server-config', () => ({
  useServerConfig: (...args: unknown[]) => mockUseServerConfig(...args),
}));

const mockGetSubscription = jest.fn();
jest.mock('@/lib/api', () => ({ api: { getSubscription: (...a: unknown[]) => mockGetSubscription(...a) } }));
jest.mock('@/lib/mock', () => ({
  withMockFallback: (real: () => unknown) => real(),
  mockSubscription: { status: 'trialing' },
}));
jest.mock('expo-web-browser', () => ({ openBrowserAsync: jest.fn() }));
jest.mock('nativewind', () => ({ useColorScheme: () => ({ colorScheme: 'light' }) }));
jest.mock('react-native-safe-area-context', () => ({
  useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }),
}));
jest.mock('@react-native-async-storage/async-storage', () =>
  require('@react-native-async-storage/async-storage/jest/async-storage-mock')
);
jest.mock('expo-router', () => {
  const React = require('react');
  const { Text, View } = require('react-native');
  function Tabs({ children }: { children: React.ReactNode }) {
    const items = React.Children.toArray(children).filter(React.isValidElement) as React.ReactElement<{ name: string }>[];
    return (
      <View>
        {items.map((c) => (
          <Text key={c.props.name}>{c.props.name}</Text>
        ))}
      </View>
    );
  }
  Tabs.Screen = () => null;
  return { Tabs, Redirect: () => null, useRouter: () => ({ push: jest.fn() }) };
});

import { act, render, screen } from '@testing-library/react-native';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import TabsLayout from './_layout';

const CLOUD = { mode: 'cloud', webUrl: 'https://web.example', features: { billing: true, google: false, microsoft: false, ai: false, push: false } };
const SELF_HOST = { mode: 'self_host', webUrl: '', features: { billing: false, google: false, microsoft: false, ai: false, push: false } };

async function flush() {
  await act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });
}

function renderLayout() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <TabsLayout />
    </QueryClientProvider>
  );
}

beforeEach(() => {
  jest.clearAllMocks();
  mockUseAuth.mockReturnValue({ user: { id: 'u1' }, loading: false, signOut: jest.fn() });
});

describe('TabsLayout billing gate', () => {
  it('renders tabs without fetching when billing is off', async () => {
    mockUseServerConfig.mockReturnValue({ config: SELF_HOST });
    renderLayout();
    await flush();
    expect(screen.getByText('inbox')).toBeTruthy();
    expect(mockGetSubscription).not.toHaveBeenCalled();
  });

  it('renders tabs for an entitled subscription', async () => {
    mockUseServerConfig.mockReturnValue({ config: CLOUD });
    mockGetSubscription.mockResolvedValue({ status: 'trialing', plan: 'annual', priceUsd: 50, currentPeriodEnd: null, cancelAtPeriodEnd: false, trialEndsAt: new Date(Date.now() + 86_400_000).toISOString() });
    renderLayout();
    await flush();
    expect(screen.getByText('inbox')).toBeTruthy();
  });

  it('replaces the tabs with the paywall for a denied subscription', async () => {
    mockUseServerConfig.mockReturnValue({ config: CLOUD });
    mockGetSubscription.mockResolvedValue({ status: 'canceled', plan: 'annual', priceUsd: 50, currentPeriodEnd: null, cancelAtPeriodEnd: false, trialEndsAt: null });
    renderLayout();
    await flush();
    expect(screen.getByText('Your subscription has ended')).toBeTruthy();
    expect(screen.queryByText('inbox')).toBeNull();
  });

  it('fails open when the subscription cannot be fetched', async () => {
    mockUseServerConfig.mockReturnValue({ config: CLOUD });
    mockGetSubscription.mockRejectedValue(new Error('offline'));
    renderLayout();
    await flush();
    await flush();
    expect(screen.getByText('inbox')).toBeTruthy();
  });
});
```

- [ ] **Step 2: Run and confirm failure.** `bun run --cwd apps/mobile test -- paywall` — expected: `Cannot find module './paywall-screen'`; the layout test renders tabs regardless of a canceled subscription.

- [ ] **Step 3: Add `webUrl` to the mobile server config.** In `apps/mobile/lib/server-config.ts` add to `ServerConfig` (after `features`):

```ts
  /** Public web app origin from /v1/instance; billing lives at `${webUrl}/pricing`. Empty on pre-webUrl servers. */
  webUrl: string;
```

In `DEMO_CONFIG` add `webUrl: 'https://demo.calendium.app',`. In `discoverServer` add `webUrl: info.webUrl ?? '',`. In `getStoredServerConfig`, backfill: replace `return raw ? (JSON.parse(raw) as ServerConfig) : null;` with:

```ts
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<ServerConfig>;
    return { webUrl: '', ...parsed } as ServerConfig;
```

- [ ] **Step 4: Create `apps/mobile/components/paywall-screen.tsx`:**

```tsx
import { Button } from '@/components/ui/button';
import { Icon } from '@/components/ui/icon';
import { Text } from '@/components/ui/text';
import useAuth from '@/context/auth';
import type { PaymentRequiredReason } from '@calendium/shared';
import * as WebBrowser from 'expo-web-browser';
import { ExternalLinkIcon, LockIcon, LogOutIcon } from 'lucide-react-native';
import { View } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';

const COPY: Record<PaymentRequiredReason, { title: string; body: string }> = {
  trial_ended: { title: 'Your free trial has ended', body: 'Subscribe on the web to keep using Calendium on every device.' },
  none: { title: 'Subscribe to keep using Calendium', body: 'Calendium is $50/year — one plan for email + calendar everywhere.' },
  canceled: { title: 'Your subscription has ended', body: 'Resubscribe on the web any time to pick up where you left off.' },
  past_due: { title: 'Payment failed', body: 'Update your payment method on the web to restore access.' },
  paused: { title: 'Your subscription is paused', body: 'Resume the plan or update your payment method on the web.' },
};

/**
 * Read-only paywall (docs/payments.md, Spotify model): the app never sells.
 * It states why access is denied and links to the web app's pricing page
 * built from the server-advertised webUrl — never a hardcoded domain.
 */
export function PaywallScreen({ reason, webUrl }: { reason: PaymentRequiredReason; webUrl: string | null }) {
  const { signOut } = useAuth();
  const insets = useSafeAreaInsets();
  const copy = COPY[reason];
  return (
    <View
      className="flex-1 items-center justify-center gap-4 bg-background px-6"
      style={{ paddingTop: insets.top, paddingBottom: insets.bottom }}>
      <Icon as={LockIcon} className="size-8 text-muted-foreground" />
      <Text className="text-center text-lg font-semibold">{copy.title}</Text>
      <Text className="text-center text-sm text-muted-foreground">{copy.body}</Text>
      <Text className="text-center text-xs text-muted-foreground">
        Calendium is managed on the web — there are no purchases in this app.
      </Text>
      {webUrl ? (
        <Button
          variant="outline"
          size="sm"
          className="flex-row gap-2"
          onPress={() => WebBrowser.openBrowserAsync(`${webUrl}/pricing`)}>
          <Icon as={ExternalLinkIcon} className="size-4" />
          <Text>Manage on the web</Text>
        </Button>
      ) : null}
      <Button variant="ghost" size="sm" className="flex-row gap-2" onPress={() => void signOut()}>
        <Icon as={LogOutIcon} className="size-4" />
        <Text>Sign out</Text>
      </Button>
    </View>
  );
}
```

- [ ] **Step 5: Gate the tabs.** In `apps/mobile/app/(tabs)/_layout.tsx` add imports:

```tsx
import { PaywallScreen } from '@/components/paywall-screen';
import { api } from '@/lib/api';
import { mockSubscription, withMockFallback } from '@/lib/mock';
import { subscriptionDenialReason } from '@calendium/shared';
import { useQuery } from '@tanstack/react-query';
```

After `const aiEnabled = ...` add:

```tsx
  // Billing gate (docs/payments.md): fetching the subscription first also
  // grants the signup trial server-side, so no gated call can race it.
  const billingEnabled = config?.features?.billing ?? false;
  const subscriptionQuery = useQuery({
    queryKey: ['subscription'],
    queryFn: () => withMockFallback(() => api.getSubscription(), () => mockSubscription),
    enabled: !!user && billingEnabled,
    retry: 1,
    staleTime: 60_000,
  });
```

Replace `if (loading) {` with `if (loading || (billingEnabled && !!user && subscriptionQuery.isPending)) {` (same spinner). After the `if (!user) return <Redirect href="/" />;` line add:

```tsx
  // Fail open on fetch errors: an unreachable billing API never locks a user out.
  const paywallReason =
    billingEnabled && subscriptionQuery.data ? subscriptionDenialReason(subscriptionQuery.data) : null;
  if (paywallReason) {
    return <PaywallScreen reason={paywallReason} webUrl={config?.webUrl || null} />;
  }
```

- [ ] **Step 6: Settings subscription card.** In `settings.tsx`'s `SubscriptionCard`, replace `const unsubscribed = status === 'none' || status === 'expired';` with `const live = hasBillingSubscription(subscription);` (import `hasBillingSubscription` from `@calendium/shared`, and add `useServerConfig` is already imported — read `const { config } = useServerConfig();` inside the card). Add a `case 'paused': return 'Paused — resume or update your payment method on the web';` to the status switch and change `'Calendium Pro'` wording to `'Calendium Annual'`. Replace the `{unsubscribed ? (...) : (...)}` block with:

```tsx
      <Text className="text-sm text-muted-foreground">
        {live ? 'Manage your plan on the web — there are no purchases in this app.' : 'Calendium is managed on the web — there are no purchases in this app.'}
      </Text>
      {config?.webUrl ? (
        <Button
          variant="outline"
          size="sm"
          className="flex-row gap-2 self-start"
          onPress={() => WebBrowser.openBrowserAsync(`${config.webUrl}/${live ? 'settings?tab=billing' : 'pricing'}`)}>
          <Icon as={ExternalLinkIcon} className="size-4" />
          <Text>Manage on the web</Text>
        </Button>
      ) : null}
```

In `apps/mobile/app/(tabs)/settings.test.tsx` add `webUrl: 'https://web.example'` to `AI_ENABLED_CONFIG` and `AI_DISABLED_CONFIG`; if any expectation asserts `'https://calendium.app/pricing'`, change it to `'https://web.example/pricing'`. Then run `bun run --cwd apps/mobile test -- settings.test` — expected: green.

- [ ] **Step 6b: Keep the existing tabs-layout test green.** `_layout.tsx` now calls `useQuery`, so in `apps/mobile/app/(tabs)/_layout.test.tsx`: add `jest.mock('@/lib/api', () => ({ api: { getSubscription: jest.fn() } }));`, `jest.mock('@/lib/mock', () => ({ withMockFallback: (real: () => unknown) => real(), mockSubscription: { status: 'trialing' } }));`, `jest.mock('expo-web-browser', () => ({ openBrowserAsync: jest.fn() }));`, import `{ QueryClient, QueryClientProvider } from '@tanstack/react-query'`, and wrap every `render(<TabsLayout />)` as `render(<QueryClientProvider client={new QueryClient()}><TabsLayout /></QueryClientProvider>)`. Its configs already carry `billing: false`, so no fetch happens and its assertions are unchanged.

- [ ] **Step 7: Run the mobile suite and lint.** `bun run --cwd apps/mobile test && bunx biome check apps/mobile && grep -rn 'calendium.app/pricing\|expired' apps/mobile/app apps/mobile/components apps/mobile/lib --include='*.ts' --include='*.tsx' | grep -v test` — expected: tests green, no findings, grep prints nothing.

- [ ] **Step 8: Commit.**

```bash
git add apps/mobile
git commit -m "feat(mobile): read-only paywall screen gated in the tabs layout; billing links from instance.webUrl" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 20: Desktop — `PaywallView`, App gate, Settings on `webUrl`, demo subscription defaults to trial

**Files:**
- Create: `apps/desktop/frontend/src/views/PaywallView.tsx`
- Create: `apps/desktop/frontend/src/views/PaywallView.test.tsx`
- Modify: `apps/desktop/frontend/src/App.tsx` (gate around the view switch)
- Modify: `apps/desktop/frontend/src/views/SettingsView.tsx` (`STATUS_BADGE` ~L463–470; `openBilling` ~L588–600; copy ~L737–738; `subscribed` ~L619)
- Modify: `apps/desktop/frontend/src/lib/server-config.ts` (`ServerConfig.webUrl`, `DEMO_CONFIG`, `readStored` backfill, `discoverServer`)
- Modify: `apps/desktop/frontend/src/lib/mock.ts` (`mockSubscription` default)
- Modify: `apps/desktop/frontend/src/lib/wails.ts` (comment L10)

**Interfaces:**
- Produces: `PaywallView({ reason }: { reason: PaymentRequiredReason })` — "Open billing in your browser" → `desktop.OpenExternal(`${webUrl}/settings?tab=billing`)`; `ServerConfig.webUrl: string`; `billingWebOrigin(config: ServerConfig | null): string | null` (prefers `webUrl`, falls back to `webOrigin`).

- [ ] **Step 1: Write the failing view test** at `apps/desktop/frontend/src/views/PaywallView.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const openExternal = vi.hoisted(() => vi.fn(async () => {}));
vi.mock('@/lib/wails', () => ({ desktop: { OpenExternal: openExternal }, isDesktop: true }));

const serverState = vi.hoisted(() => ({
  config: { webUrl: 'https://web.example', authBaseUrl: 'https://web.example/api/auth', features: { billing: true, google: false, microsoft: false, ai: false, push: false } } as unknown,
  demoMode: false,
}));
vi.mock('@/lib/server-config', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/server-config')>()),
  useServerConfig: () => ({ config: serverState.config, demoMode: serverState.demoMode, clear: vi.fn(), exitDemo: vi.fn() }),
}));

const signOut = vi.hoisted(() => vi.fn(async () => {}));
vi.mock('@/lib/auth', () => ({ signOut, clearStoredToken: vi.fn() }));
vi.mock('@/lib/offline', () => ({ clearOfflineState: vi.fn(async () => {}) }));

import { PaywallView } from './PaywallView';

beforeEach(() => vi.clearAllMocks());

describe('PaywallView', () => {
  it.each([
    ['trial_ended', 'Your free trial has ended'],
    ['none', 'Subscribe to keep using Calendium'],
    ['canceled', 'Your subscription has ended'],
    ['past_due', 'Payment failed'],
    ['paused', 'Your subscription is paused'],
  ] as const)('renders %s copy with no purchase UI', (reason, title) => {
    render(<PaywallView reason={reason} />);
    expect(screen.getByRole('heading', { name: title })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Subscribe/ })).not.toBeInTheDocument();
  });

  it('opens <webUrl>/settings in the system browser', async () => {
    render(<PaywallView reason="trial_ended" />);
    await userEvent.click(screen.getByRole('button', { name: /Open billing in your browser/ }));
    expect(openExternal).toHaveBeenCalledWith('https://web.example/settings?tab=billing');
  });

  it('falls back to the auth origin when webUrl is empty', async () => {
    serverState.config = { webUrl: '', authBaseUrl: 'https://auth.example/api/auth', features: { billing: true } };
    render(<PaywallView reason="none" />);
    await userEvent.click(screen.getByRole('button', { name: /Open billing in your browser/ }));
    expect(openExternal).toHaveBeenCalledWith('https://auth.example/settings?tab=billing');
  });

  it('signs out', async () => {
    render(<PaywallView reason="canceled" />);
    await userEvent.click(screen.getByRole('button', { name: /Sign out/ }));
    expect(signOut).toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run and confirm failure.** `bun run --cwd apps/desktop/frontend test src/views/PaywallView.test.tsx` — expected: module not found.

- [ ] **Step 3: Server config.** In `apps/desktop/frontend/src/lib/server-config.ts` add to `ServerConfig` (after `undoSendSeconds`):

```ts
  /** Public web app origin from /v1/instance; billing lives at `${webUrl}/settings?tab=billing`. Empty on pre-webUrl servers. */
  webUrl: string;
```

Add `webUrl: '',` to `DEMO_CONFIG`; add `webUrl: '',` to the backfill object in `readStored` (before `...parsed`); add `webUrl: info.webUrl ?? '',` in `discoverServer`. After `webOrigin` add:

```ts
/** Where billing lives: the server-advertised web origin, else the auth origin. */
export function billingWebOrigin(config: ServerConfig | null): string | null {
  if (config?.webUrl) return config.webUrl.replace(/\/+$/, '');
  return webOrigin(config);
}
```

- [ ] **Step 4: Create `apps/desktop/frontend/src/views/PaywallView.tsx`:**

```tsx
import type { PaymentRequiredReason } from '@calendium/shared';
import { ExternalLink, Lock, LogOut } from 'lucide-react';

import { signOut } from '@/lib/auth';
import { clearOfflineState } from '@/lib/offline';
import { billingWebOrigin, useServerConfig } from '@/lib/server-config';
import { desktop } from '@/lib/wails';
import { Button } from '@/ui/button';

const COPY: Record<PaymentRequiredReason, { title: string; body: string }> = {
  trial_ended: { title: 'Your free trial has ended', body: 'Subscribe in your browser to keep using Calendium on every device.' },
  none: { title: 'Subscribe to keep using Calendium', body: 'Calendium is $50/year — one plan for email + calendar everywhere.' },
  canceled: { title: 'Your subscription has ended', body: 'Resubscribe in your browser any time to pick up where you left off.' },
  past_due: { title: 'Payment failed', body: 'Update your payment method in your browser to restore access.' },
  paused: { title: 'Your subscription is paused', body: 'Resume the plan or update your payment method in your browser.' },
};

/** Read-only paywall (docs/payments.md): billing always happens on the web. */
export function PaywallView({ reason }: { reason: PaymentRequiredReason }) {
  const { config, demoMode, exitDemo } = useServerConfig();
  const copy = COPY[reason];
  const origin = billingWebOrigin(config);

  return (
    <section aria-label="Subscription required" className="flex h-full items-center justify-center p-6">
      <div className="flex w-full max-w-md flex-col items-center gap-4 rounded-lg border p-8 text-center">
        <Lock className="size-6 text-muted-foreground" />
        <h1 className="text-lg font-semibold">{copy.title}</h1>
        <p className="text-sm text-muted-foreground">{copy.body}</p>
        <Button onClick={() => origin && desktop.OpenExternal(`${origin}/settings?tab=billing`)} disabled={!origin}>
          <ExternalLink /> Open billing in your browser
        </Button>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => {
            if (demoMode) exitDemo();
            else void signOut().finally(() => void clearOfflineState());
          }}
        >
          <LogOut /> Sign out
        </Button>
      </div>
    </section>
  );
}
```

- [ ] **Step 5: Gate the app.** In `App.tsx` add imports `import { subscriptionDenialReason } from '@calendium/shared';`, `import { mockSubscription } from '@/lib/mock';` (extend the existing `@/lib/mock` import), `import { useServerConfig } from '@/lib/server-config';`, `import { PaywallView } from '@/views/PaywallView';`. Inside the `App` component, next to the existing search `useQuery`, add:

```tsx
  const { config } = useServerConfig();
  const billingEnabled = config?.features?.billing ?? false;
  const subscriptionQuery = useQuery({
    queryKey: ['subscription'],
    queryFn: () =>
      orMock(
        () => api.getSubscription(),
        () => mockSubscription()
      ),
    enabled: billingEnabled,
    staleTime: 60_000,
    refetchInterval: 5 * 60_000,
  });
  // Fail open on fetch errors; only a fetched subscription that denies access paywalls.
  const paywallReason =
    billingEnabled && subscriptionQuery.data ? subscriptionDenialReason(subscriptionQuery.data) : null;
```

Replace the `<main className="min-w-0 flex-1">` block with:

```tsx
        <main className="min-w-0 flex-1">
          {paywallReason ? (
            <PaywallView reason={paywallReason} />
          ) : (
            <>
              {view === 'inbox' && <InboxView split={split} />}
              {view === 'calendar' && <CalendarView />}
              {view === 'settings' && <SettingsView />}
            </>
          )}
        </main>
```

- [ ] **Step 6: Settings view.** In `SettingsView.tsx`: replace `STATUS_BADGE` with

```tsx
const STATUS_BADGE: Record<SubscriptionStatus, { label: string; variant: 'default' | 'secondary' | 'destructive' | 'outline' }> = {
  active: { label: 'Active', variant: 'default' },
  trialing: { label: 'Trial', variant: 'secondary' },
  past_due: { label: 'Past due', variant: 'destructive' },
  paused: { label: 'Paused', variant: 'outline' },
  canceled: { label: 'Canceled', variant: 'outline' },
  none: { label: 'No subscription', variant: 'outline' },
};
```

change `import { useServerConfig, webOrigin } from '@/lib/server-config';` to import `billingWebOrigin` instead of `webOrigin` and use `const origin = billingWebOrigin(config);` in `openBilling`; change the copy `Billing always happens on the web via Stripe — there are no in-app purchases.` to `Billing always happens on the web through Paddle, our merchant of record — there are no in-app purchases.`; change `const subscribed = subscription?.status === 'active' || subscription?.status === 'trialing';` to `const subscribed = !!subscription && hasBillingSubscription(subscription);` (import `hasBillingSubscription` from `@calendium/shared`), so trialing users see "Subscribe on the web". In `SettingsView.test.tsx`'s `vi.mock('@/lib/server-config', ...)` replace `webOrigin: () => null,` with `billingWebOrigin: () => null,`.

- [ ] **Step 7: Demo defaults + comment.** In `lib/mock.ts` change `mockSubscription()`'s non-paid branch to `{ status: 'trialing', plan: 'annual', priceUsd: 50, currentPeriodEnd: null, cancelAtPeriodEnd: false, trialEndsAt: iso(addDays(now, 9)) }` and update its banner comment to `visibly flips trialing → active`. In `lib/wails.ts` change `(Stripe checkout et al.)` to `(web billing et al.)`.

- [ ] **Step 8: Run the desktop suite and lint.** `bun run --cwd apps/desktop/frontend test && bunx tsc -p apps/desktop/frontend --noEmit && bunx biome check apps/desktop/frontend && grep -rni stripe apps/desktop/frontend/src` — expected: green; grep prints nothing.

- [ ] **Step 9: Commit.**

```bash
git add apps/desktop
git commit -m "feat(desktop): read-only PaywallView gating the app; billing links from instance.webUrl; demo trial default" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 21: Docs, `.env.example`, `docker-compose.yml`, go-live runbook, repo-wide Stripe sweep

**Files:**
- Modify: `docs/payments.md` (whole file)
- Create: `docs/paddle-go-live.md`
- Modify: `docs/pricing-model.md` (L11, L19, L47–57, L67)
- Modify: `docs/architecture.md` (L15, L46, L64–67, L132, L141–146, L154–164)
- Modify: `docs/self-hosting/configuration.md` (L125–135, L170–171, L211–215 table)
- Modify: `README.md` (L24)
- Modify: `.env.example` (L124–127)
- Modify: `docker-compose.yml` (web `args` + `environment`)
- Modify (one-line Stripe mentions): `docs/collaboration.md` L36, `docs/state-and-gaps.md` L47, `docs/feature-map.md` L166, `docs/tech-stack.md` L20, `docs/self-hosting/{azure.md L12, aws.md L11, gcp.md L11, security.md L26/L71/L183–185, quickstart.md L62, troubleshooting.md L284, README.md L9/L12/L35/L244/L253, vps.md L34}`

**Interfaces:** none (documentation and deployment config).

- [ ] **Step 1: Write the "test": a repo-wide sweep that must end empty.** Run it now to list every target: `grep -rniE 'stripe' --exclude-dir=node_modules --exclude-dir=.git --exclude-dir=.next --exclude=bun.lock . | grep -v '^./docs/superpowers/'` — expected (before this task): the doc/env/compose lines listed in Files above. After Step 8 it must print nothing.

- [ ] **Step 2: Replace `docs/payments.md` with:**

```markdown
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
3. One DB transaction: record `notification_id` in `billing_events` (duplicate → `200`, no change); resolve the user by `custom_data.user_id`, else `billing_customer_id`; unknown → warn + `200`; drop the event if `occurred_at < last_event_at` (delivery order is not guaranteed; replays carry a new `notification_id` and the same `occurred_at`, so they re-apply idempotently); upsert status (mapped), `current_period_end = current_billing_period.ends_at`, `cancel_at_period_end`, provider ids, `last_event_at = occurred_at`, `trial_ends_at = null`.
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
```

- [ ] **Step 3: Create `docs/paddle-go-live.md`:**

```markdown
# Paddle go-live checklist

Operator steps outside the codebase (docs/payments.md has the flows). Do each block in **sandbox** first, then repeat for **live**.

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
```

- [ ] **Step 4: Update the other docs.** `docs/pricing-model.md`: L11 `**$50 / year** via Stripe (14-day trial)` → `**$50 / year** via Paddle (14-day trial, Paddle is merchant of record)`; L19 `Stripe / billing code path` → `Paddle / billing code path`; L47–48 `Users go through the Stripe flow` → `Users go through the Paddle flow`; L55–57 `the Stripe webhook all return 501 self_hosted — there is no Stripe integration to reach, and no Stripe keys are required to boot.` → `the Paddle webhook all return **501 self_hosted** — there is no Paddle integration to reach, and no Paddle keys are required to boot.`; L67 `(Stripe, $50/year, states, webhooks)` → `(Paddle, $50/year, states, webhooks)`.
`docs/architecture.md`: L15 `Stripe (billing)` → `Paddle (billing)`; L46 `out/stripeapi/        # Stripe REST client + webhook HMAC verification (stdlib)` → `out/paddle/           # Paddle Billing REST client + Paddle-Signature HMAC verification (stdlib)`; replace the four billing rows of the REST table with:

```markdown
| `GET /v1/billing/subscription` | Subscription status ($50/yr annual plan); grants the 14-day signup trial on first call; `402 {details:{reason}}` elsewhere when lapsed |
| `POST /v1/billing/checkout` | Create a Paddle overlay checkout (no body) `→ {url}` (409 `already_subscribed`, 502 `billing_unavailable`, 501 `self_hosted` when `SELF_HOSTED`) |
| `POST /v1/billing/portal` | Paddle customer-portal links (no body) `→ {overviewUrl, cancelUrl, updatePaymentUrl}` (400 `no_billing_profile`, 501 `self_hosted`) |
| `POST /v1/webhooks/paddle` | Paddle webhook (Paddle-Signature verified, unauthenticated, 1 MB cap) |
```

L132: replace `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`, `STRIPE_PRICE_ID_ANNUAL` with `PADDLE_ENV`, `PADDLE_API_KEY`, `PADDLE_WEBHOOK_SECRET`, `PADDLE_PRICE_ID_ANNUAL`, `BILLING_RECONCILE_INTERVAL` and `disables Stripe billing` → `disables Paddle billing`; L141 `Stripe billing is live` → `Paddle billing is live (the api and worker refuse to boot without the Paddle keys)`; L144 `Stripe is switched off` → `Paddle is switched off`; L154 add `webUrl,` after `authProviders,` in the instance shape and append `, and \`webUrl\` is \`PUBLIC_WEB_URL\` (mobile/desktop build billing links from it)` to the sentence ending `features.billing = !SELF_HOSTED`.
`docs/self-hosting/configuration.md`: replace the Billing table rows with

```markdown
| `PADDLE_ENV` | No | `sandbox` | `sandbox` or `live` — selects the Paddle API origin. |
| `PADDLE_API_KEY` | **Yes (cloud)** | — | Paddle API key. Cloud mode refuses to start without it. |
| `PADDLE_WEBHOOK_SECRET` | **Yes (cloud)** | — | Notification-destination secret for `Paddle-Signature` verification. Cloud mode refuses to start without it. |
| `PADDLE_PRICE_ID_ANNUAL` | **Yes (cloud)** | — | Paddle price id (`pri_…`) for the $50/yr plan (see [`../payments.md`](../payments.md)). |
| `BILLING_RECONCILE_INTERVAL` | No | `6h` | Worker loop that re-reads stale subscriptions from Paddle. |
| `NEXT_PUBLIC_PADDLE_CLIENT_TOKEN` | **Yes (cloud, web build)** | — | Paddle.js client token, inlined at web build time. |
| `NEXT_PUBLIC_PADDLE_ENV` | No | `sandbox` | `sandbox` or `production`, inlined at web build time; must match `PADDLE_ENV`. |
```

change `POST /v1/webhooks/stripe` → `POST /v1/webhooks/paddle` (L171), and add a table row `| \`webUrl\` | \`PUBLIC_WEB_URL\` — mobile and desktop build billing links from it. |` after the `authProviders` row.
`README.md` L24: `for **$50/year** via Stripe` → `for **$50/year** via Paddle (merchant of record)`.
One-liners: `docs/collaboration.md` `via Stripe` → `via Paddle`; `docs/state-and-gaps.md` L47 → `` `PADDLE_API_KEY`, `PADDLE_WEBHOOK_SECRET`, `PADDLE_PRICE_ID_ANNUAL` (+ web `NEXT_PUBLIC_PADDLE_CLIENT_TOKEN`) ``; `docs/feature-map.md` `Stripe $50/yr checkout/portal/webhooks` → `Paddle $50/yr checkout/portal/webhooks`; `docs/tech-stack.md` `Stripe REST via net/http` → `Paddle Billing REST via net/http`; in every `docs/self-hosting/*.md` line listed in Files replace `no Stripe` → `no Paddle`, `Stripe keys` → `Paddle keys`, `STRIPE_*` → `PADDLE_*`, `/v1/webhooks/stripe` → `/v1/webhooks/paddle`, `Stripe ($50/yr)` → `Paddle ($50/yr)`, `bill through Stripe` → `bill through Paddle`, `(Stripe wiring, provisioning)` → `(Paddle wiring, provisioning)`, `disables Stripe` → `disables Paddle billing`, `and the Stripe webhook return` → `and the Paddle webhook return`.

- [ ] **Step 5: `.env.example`.** Replace the Stripe block with:

```dotenv
# ─── Paddle billing (cloud only — leave blank when self-hosting) ─────────────
# With SELF_HOSTED=false the api AND worker refuse to start unless the three
# keys are set. See docs/paddle-go-live.md.
PADDLE_ENV=sandbox
PADDLE_API_KEY=
PADDLE_WEBHOOK_SECRET=
PADDLE_PRICE_ID_ANNUAL=
# Worker reconciliation cadence (Go duration).
BILLING_RECONCILE_INTERVAL=6h
# Web app (build-time, public): Paddle.js client token and environment.
NEXT_PUBLIC_PADDLE_CLIENT_TOKEN=
NEXT_PUBLIC_PADDLE_ENV=sandbox
```

- [ ] **Step 6: `docker-compose.yml`.** Under the `web` service add to `build.args`:

```yaml
        NEXT_PUBLIC_PADDLE_CLIENT_TOKEN: ${NEXT_PUBLIC_PADDLE_CLIENT_TOKEN:-}
        NEXT_PUBLIC_PADDLE_ENV: ${NEXT_PUBLIC_PADDLE_ENV:-sandbox}
```

and to `environment`:

```yaml
      NEXT_PUBLIC_PADDLE_CLIENT_TOKEN: ${NEXT_PUBLIC_PADDLE_CLIENT_TOKEN:-}
      NEXT_PUBLIC_PADDLE_ENV: ${NEXT_PUBLIC_PADDLE_ENV:-sandbox}
```

(`api` and `worker` read `PADDLE_*`/`BILLING_RECONCILE_INTERVAL` through `env_file: .env` already.) Validate without starting anything: `docker compose config --quiet` — expected: no output.

- [ ] **Step 7: Markdown sanity.** `grep -n 'paddle-go-live' docs/payments.md README.md` prints the cross-links; `ls docs/paddle-go-live.md` exists.

- [ ] **Step 8: Repo-wide sweep (the gate for this task; run after Tracks A–D have merged).** `grep -rniE 'stripe' --exclude-dir=node_modules --exclude-dir=.git --exclude-dir=.next --exclude=bun.lock . | grep -v '^./docs/superpowers/'` — expected: **no output**.

- [ ] **Step 9: Commit.**

```bash
git add docs README.md .env.example docker-compose.yml
git commit -m "docs: Paddle billing flows, go-live checklist, env/compose variables; remove every Stripe reference" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 22: Sandbox verification runbook (blocked, not failed, when sandbox credentials are absent)

**Files:**
- Read-only: `.env` (never printed, never edited by the agent), `docs/paddle-go-live.md`
- Create (scratch only, not committed): the agent's scratchpad notes

**Interfaces:** none. This task is a runbook executed against the real Paddle sandbox after every track has merged. Secrets are entered by the user into `.env`; the agent never pastes them into chat, logs or files.

- [ ] **Step 1: Check the sandbox prerequisites without exposing values.** Run `grep -E '^(SELF_HOSTED|PADDLE_ENV|PADDLE_API_KEY|PADDLE_WEBHOOK_SECRET|PADDLE_PRICE_ID_ANNUAL|NEXT_PUBLIC_PADDLE_CLIENT_TOKEN|NEXT_PUBLIC_PADDLE_ENV|PUBLIC_WEB_URL|PUBLIC_API_URL)=' .env 2>/dev/null | sed -E 's/=(.+)$/=<set>/; s/=$/=<EMPTY>/'` — expected for a runnable sandbox: `SELF_HOSTED=false`, `PADDLE_ENV=sandbox`, `NEXT_PUBLIC_PADDLE_ENV=sandbox`, and `<set>` for the three `PADDLE_*` keys and the client token. **If any `PADDLE_*` or the client token prints `<EMPTY>` or the file is missing: mark this task BLOCKED (not failed), paste the following into the final report, and stop here:**

  > Sandbox verification is blocked until you set these in `.env` (values from the Paddle sandbox dashboard, see docs/paddle-go-live.md §2–4): `SELF_HOSTED=false`, `PADDLE_ENV=sandbox`, `PADDLE_API_KEY`, `PADDLE_WEBHOOK_SECRET`, `PADDLE_PRICE_ID_ANNUAL`, `NEXT_PUBLIC_PADDLE_CLIENT_TOKEN`, `NEXT_PUBLIC_PADDLE_ENV=sandbox`, `PUBLIC_WEB_URL=http://localhost:3000`. In the Paddle sandbox dashboard set the default payment link to `http://localhost:3000/checkout` and create a notification destination pointing at `<tunnel>/v1/webhooks/paddle` with the eight `subscription.*` events; the tunnel is started in Step 3.

- [ ] **Step 2: Start the cloud-mode stack — only with the user's go-ahead, because a stack is already in use.** Ask the user whether the running stack is already cloud mode with the new build (`curl -s http://localhost:8080/v1/instance | tee /dev/stderr | grep -q '"billing":true'`). If not, the USER runs: `docker compose build api worker web && docker compose up -d` (the web image must be rebuilt because `NEXT_PUBLIC_PADDLE_*` are build-time). Expected: `docker compose logs api --tail 5` shows `api: listening`; a stack booted with an empty key instead shows `api: fatal error="cloud mode (SELF_HOSTED=false) requires PADDLE_..."` — that is the startup rule working, fix `.env` and retry.

- [ ] **Step 3: Expose the webhook.** The USER starts a tunnel in a separate terminal: `cloudflared tunnel --url http://localhost:8080` (or `ngrok http 8080`) and pastes `<tunnel>/v1/webhooks/paddle` into the sandbox notification destination (docs/paddle-go-live.md §4), copying its secret into `PADDLE_WEBHOOK_SECRET` if not done yet (then `docker compose restart api worker`). Expected: Paddle's "Send test event" to the destination shows `200` in Paddle's notification log and `docker compose logs api | grep webhooks/paddle` shows a `200`.

- [ ] **Step 4: Trial at signup.** In a fresh browser profile open `http://localhost:3000/signin`, create a new account. Expected: the inbox renders (no 402), Settings → Billing shows `Free trial — ends <signup + 14 days>`; `psql "$DATABASE_URL" -c "select status, trial_ends_at, billing_customer_id from subscriptions order by updated_at desc limit 1"` shows `trialing`, a `trial_ends_at` 14 days after `users.created_at`, and a null customer id.

- [ ] **Step 5: Checkout.** Click **Subscribe · $50/year**. Expected: the browser lands on `http://localhost:3000/checkout?_ptxn=txn_…`, the Paddle overlay opens (sandbox badge visible). Pay with Paddle's test card `4242 4242 4242 4242`, any future expiry, CVC `100`, any address. Expected: redirect to `/checkout/success` showing "Activating…" then "You're all set" within ~10 s; `docker compose logs api | grep -c 'POST /v1/webhooks/paddle'` ≥ 2 (`subscription.created`, `subscription.activated`); the `subscriptions` row shows `status=active`, `billing_subscription_id=sub_…`, `billing_customer_id=ctm_…`, `trial_ends_at=NULL`, `current_period_end` one year out; `billing_events` has one row per notification id.

- [ ] **Step 6: Double-subscription guard.** From the terminal, with a bearer token from the browser's `/api/auth/token` response: `curl -s -o /dev/null -w '%{http_code}\n' -X POST -H "Authorization: Bearer $TOKEN" http://localhost:8080/v1/billing/checkout` — expected: `409`, and Settings → Billing now shows **Manage billing** / **Cancel subscription** instead of Subscribe.

- [ ] **Step 7: Portal cancel.** Settings → Billing → **Cancel subscription** → confirm in Paddle's portal (end of period). Expected: after the `subscription.updated` webhook, the row shows `cancel_at_period_end=true`, status still `active`; Settings shows `Cancels on <period end>`; the inbox still works (access until period end).

- [ ] **Step 8: Mobile/desktop read-only check (optional if the apps are not built locally).** Point the desktop app (or Expo) at `http://localhost:8080`; expected: `GET /v1/instance` carries `webUrl: http://localhost:3000`, the Settings billing section links to the web app, and no purchase button exists.

- [ ] **Step 9: Report.** Record in the final report: whether each of Steps 4–7 passed, the sandbox transaction id and subscription id (not secrets), and any Paddle dashboard setting that had to change. Nothing from this task is committed; `git status` must be clean apart from untracked scratch files outside the repo.

---

### Task 23: Full-suite gate

**Files:** none modified. Every command runs from the repo root on the merged branch.

- [ ] **Step 1: Backend.** `cd backend && go build ./... && go vet ./... && golangci-lint run ./... && REQUIRE_DOCKER=1 go test -race ./...` — expected: no findings; every package `ok` (Postgres tests run against the testcontainer, not the live stack).

- [ ] **Step 2: Desktop Go module lint.** `cd apps/desktop && golangci-lint run ./...` — expected: no findings.

- [ ] **Step 3: TypeScript suites.** `bun run test:shared && bun run test:web && bun run test:mobile && bun run test:desktop` — expected: all green (`billing.test.ts`, `paywall.test.tsx`, `checkout-client.test.tsx`, `success-client.test.tsx`, `settings-billing.test.tsx`, `start-trial-cta.test.tsx`, `paywall-screen.test.tsx`, `_layout.paywall.test.tsx`, `PaywallView.test.tsx` all listed as passed).

- [ ] **Step 4: Typecheck.** `bunx tsc -p packages/shared --noEmit && bunx tsc -p apps/web --noEmit && bunx tsc -p apps/desktop/frontend --noEmit && bunx tsc -p apps/mobile --noEmit` — expected: no errors.

- [ ] **Step 5: Lint.** `bun run lint` (Biome + both golangci-lint modules) — expected: clean.

- [ ] **Step 6: Playwright demo e2e.** `bun run test:e2e` — expected: the whole suite green including `e2e/paywall.spec.ts` (demo mode, no backend, DEMO_INSTANCE billing on).

- [ ] **Step 7: Stripe is gone.** `grep -rniE 'stripe' --exclude-dir=node_modules --exclude-dir=.git --exclude-dir=.next --exclude=bun.lock . | grep -v '^./docs/superpowers/'` — expected: no output. `ls backend/internal/adapter/out/stripeapi 2>&1` — expected: `No such file or directory`.

- [ ] **Step 8: Spec cross-check (read-only).** Confirm each line of **Global Constraints** maps to a passing test: domain matrix (Task 1), migration/EnsureTrial/ListForReconciliation (Task 3), cloud startup rule (Task 4, Task 13 Step 5), signature/tolerance/empty secret/multi-h1 (Task 7), status mapping (Task 6), trial grant anchored to `created_at` (Task 8), 409/400/502 (Tasks 9, 12), notification-id idempotency + ordering + trial cleared (Task 10), reconciliation + 10-minute throttle (Task 11), 402 body shape (Task 12), `webUrl` (Tasks 12, 14, 19, 20), no client URLs (Tasks 5, 12, 14), Paddle.js init (Task 16), paywall per reason + banner + settings (Tasks 15, 17), mobile/desktop paywalls (Tasks 19, 20), docs/env/compose (Task 21).

- [ ] **Step 9: Integrate.** Follow superpowers:finishing-a-development-branch: merge the track branches into the feature branch, re-run Steps 1–7 on the merged tree, then open the PR with the Task 22 report attached.

