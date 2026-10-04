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
