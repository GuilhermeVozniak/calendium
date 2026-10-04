package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"calendium/backend/internal/domain"
)

// --- UserRepo ----------------------------------------------------------------

func TestUserRepoUpsertInsertAndGetByID(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()

	created, err := st.Users().Upsert(ctx, domain.User{ID: "u1", Email: "u1@example.com"})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if created.ID != "u1" || created.Email != "u1@example.com" {
		t.Fatalf("created = %+v", created)
	}

	got, err := st.Users().GetByID(ctx, "u1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != "u1" || got.Email != "u1@example.com" {
		t.Fatalf("got = %+v", got)
	}
}

func TestUserRepoUpsertPreservesNameWhenNil(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()

	name := "Ada"
	avatar := "https://example.com/ada.png"
	if _, err := st.Users().Upsert(ctx, domain.User{
		ID: "u1", Email: "old@example.com", Name: &name, AvatarURL: &avatar,
	}); err != nil {
		t.Fatalf("Upsert first: %v", err)
	}

	updated, err := st.Users().Upsert(ctx, domain.User{ID: "u1", Email: "new@example.com"})
	if err != nil {
		t.Fatalf("Upsert second: %v", err)
	}
	if updated.Email != "new@example.com" {
		t.Fatalf("Email = %q, want new@example.com", updated.Email)
	}
	if updated.Name == nil || *updated.Name != "Ada" {
		t.Fatalf("Name = %v, want preserved Ada", updated.Name)
	}
	if updated.AvatarURL == nil || *updated.AvatarURL != avatar {
		t.Fatalf("AvatarURL = %v, want preserved", updated.AvatarURL)
	}
}

func TestUserRepoUpsertOverwritesNameWhenSet(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()

	name := "Ada"
	if _, err := st.Users().Upsert(ctx, domain.User{ID: "u1", Email: "u1@example.com", Name: &name}); err != nil {
		t.Fatalf("Upsert first: %v", err)
	}

	newName := "Grace"
	updated, err := st.Users().Upsert(ctx, domain.User{ID: "u1", Email: "u1@example.com", Name: &newName})
	if err != nil {
		t.Fatalf("Upsert second: %v", err)
	}
	if updated.Name == nil || *updated.Name != "Grace" {
		t.Fatalf("Name = %v, want Grace", updated.Name)
	}
}

func TestUserRepoGetByIDMissing(t *testing.T) {
	st, _ := newTestStore(t)
	if _, err := st.Users().GetByID(context.Background(), "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// --- SubscriptionRepo ----------------------------------------------------------

func TestSubscriptionRepoUpsertDefaults(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	if err := st.Subscriptions().Upsert(ctx, domain.Subscription{UserID: "u1"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := st.Subscriptions().GetByUserID(ctx, "u1")
	if err != nil {
		t.Fatalf("GetByUserID: %v", err)
	}
	if got.Status != domain.SubscriptionNone {
		t.Fatalf("Status = %q, want none", got.Status)
	}
	if got.Plan != domain.PlanAnnual {
		t.Fatalf("Plan = %q, want annual", got.Plan)
	}
	if got.PriceUSD != domain.PriceUSDAnnual {
		t.Fatalf("PriceUSD = %d, want %d", got.PriceUSD, domain.PriceUSDAnnual)
	}
}

func TestSubscriptionRepoUpsertActiveFields(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	periodEnd := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	lastEvent := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := st.Subscriptions().Upsert(ctx, domain.Subscription{
		UserID:            "u1",
		Status:            domain.SubscriptionActive,
		BillingCustomerID: "cus_123",
		CurrentPeriodEnd:  &periodEnd,
		LastEventAt:       &lastEvent,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := st.Subscriptions().GetByUserID(ctx, "u1")
	if err != nil {
		t.Fatalf("GetByUserID: %v", err)
	}
	if got.Status != domain.SubscriptionActive {
		t.Fatalf("Status = %q, want active", got.Status)
	}
	if got.BillingCustomerID != "cus_123" {
		t.Fatalf("BillingCustomerID = %q, want cus_123", got.BillingCustomerID)
	}
	if got.CurrentPeriodEnd == nil || !got.CurrentPeriodEnd.Equal(periodEnd) {
		t.Fatalf("CurrentPeriodEnd = %v, want %v", got.CurrentPeriodEnd, periodEnd)
	}
	if got.LastEventAt == nil || !got.LastEventAt.Equal(lastEvent) {
		t.Fatalf("LastEventAt = %v, want %v", got.LastEventAt, lastEvent)
	}

	byCustomer, err := st.Subscriptions().GetByBillingCustomerID(ctx, "cus_123")
	if err != nil {
		t.Fatalf("GetByBillingCustomerID: %v", err)
	}
	if byCustomer.UserID != "u1" {
		t.Fatalf("byCustomer.UserID = %q, want u1", byCustomer.UserID)
	}

	if _, err := st.Subscriptions().GetByBillingCustomerID(ctx, "cus_unknown"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByBillingCustomerID unknown: err = %v, want ErrNotFound", err)
	}
}

func TestSubscriptionRepoUpsertPreservesCustomerIDWhenEmpty(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	if err := st.Subscriptions().Upsert(ctx, domain.Subscription{
		UserID: "u1", Status: domain.SubscriptionActive, BillingCustomerID: "cus_original",
	}); err != nil {
		t.Fatalf("Upsert first: %v", err)
	}
	if err := st.Subscriptions().Upsert(ctx, domain.Subscription{
		UserID: "u1", Status: domain.SubscriptionActive, BillingCustomerID: "",
	}); err != nil {
		t.Fatalf("Upsert second: %v", err)
	}

	got, err := st.Subscriptions().GetByUserID(ctx, "u1")
	if err != nil {
		t.Fatalf("GetByUserID: %v", err)
	}
	if got.BillingCustomerID != "cus_original" {
		t.Fatalf("BillingCustomerID = %q, want preserved cus_original", got.BillingCustomerID)
	}
}

func TestSubscriptionRepoGetByUserIDMissing(t *testing.T) {
	st, _ := newTestStore(t)
	if _, err := st.Subscriptions().GetByUserID(context.Background(), "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

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
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "subscriptions_status_check" {
		t.Fatalf("err = %v, want check_violation (23514) on subscriptions_status_check", err)
	}
}

func TestSubscriptionRepoListForReconciliation(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	ptr := func(v time.Time) *time.Time { return &v }
	for _, id := range []string{"lapsed", "fresh", "stale", "nosub", "paused-lapsed", "canceled-lapsed", "pastdue-lapsed", "null-lapsed", "null-fresh"} {
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
		// past_due with lapsed period -> selected (third status of the set)
		{UserID: "pastdue-lapsed", Status: domain.SubscriptionPastDue, BillingSubscriptionID: "sub_pastdue", BillingCustomerID: "ctm_pastdue", CurrentPeriodEnd: ptr(now.Add(-2 * time.Hour)), LastEventAt: ptr(now.Add(-time.Hour))},
		// NULL last_event_at with a lapsed period -> selected by the period clause
		{UserID: "null-lapsed", Status: domain.SubscriptionActive, BillingSubscriptionID: "sub_null_lapsed", BillingCustomerID: "ctm_null_lapsed", CurrentPeriodEnd: ptr(now.Add(-2 * time.Hour))},
		// NULL last_event_at, period in the future -> not selected (NULL never trips the 7-day clause)
		{UserID: "null-fresh", Status: domain.SubscriptionActive, BillingSubscriptionID: "sub_null_fresh", BillingCustomerID: "ctm_null_fresh", CurrentPeriodEnd: ptr(now.Add(300 * 24 * time.Hour))},
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
	want := []string{"lapsed", "null-lapsed", "pastdue-lapsed", "paused-lapsed", "stale"}
	if len(ids) != len(want) {
		t.Fatalf("selected = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("selected = %v, want %v (ordered by user_id)", ids, want)
		}
	}
}

// TestSubscriptionRepoUpsertIfNewerOrderingGuard pins the SQL-level ordering
// guard: the DO UPDATE only applies when the stored last_event_at is NULL or
// <= the incoming one, so two concurrent webhooks cannot regress the row by
// committing in the wrong order. Upsert is the forced variant.
func TestSubscriptionRepoUpsertIfNewerOrderingGuard(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	t1 := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	t3 := t2.Add(time.Hour)
	row := func(status domain.SubscriptionStatus, at time.Time) domain.Subscription {
		return domain.Subscription{UserID: "u1", Status: status, BillingCustomerID: "ctm_1", BillingSubscriptionID: "sub_1", LastEventAt: &at}
	}
	get := func() domain.Subscription {
		t.Helper()
		got, err := st.Subscriptions().GetByUserID(ctx, "u1")
		if err != nil {
			t.Fatalf("GetByUserID: %v", err)
		}
		return got
	}

	// No row yet: the insert always applies.
	applied, err := st.Subscriptions().UpsertIfNewer(ctx, row(domain.SubscriptionActive, t2))
	if err != nil || !applied {
		t.Fatalf("first write: applied=%v err=%v, want applied", applied, err)
	}

	// An older event must not overwrite the newer stored one.
	applied, err = st.Subscriptions().UpsertIfNewer(ctx, row(domain.SubscriptionCanceled, t1))
	if err != nil {
		t.Fatalf("older write: %v", err)
	}
	if applied {
		t.Fatal("older event reported applied")
	}
	if got := get(); got.Status != domain.SubscriptionActive || !got.LastEventAt.Equal(t2) {
		t.Fatalf("older event overwrote the row: %+v", got)
	}

	// Same instant applies (replays are idempotent), newer applies.
	if applied, err = st.Subscriptions().UpsertIfNewer(ctx, row(domain.SubscriptionPastDue, t2)); err != nil || !applied {
		t.Fatalf("same-instant write: applied=%v err=%v, want applied", applied, err)
	}
	if applied, err = st.Subscriptions().UpsertIfNewer(ctx, row(domain.SubscriptionPaused, t3)); err != nil || !applied {
		t.Fatalf("newer write: applied=%v err=%v, want applied", applied, err)
	}
	if got := get(); got.Status != domain.SubscriptionPaused || !got.LastEventAt.Equal(t3) {
		t.Fatalf("newer event not stored: %+v", got)
	}

	// The forced variant (reconciliation) overwrites regardless of order.
	if err := st.Subscriptions().Upsert(ctx, row(domain.SubscriptionCanceled, t1)); err != nil {
		t.Fatalf("forced write: %v", err)
	}
	if got := get(); got.Status != domain.SubscriptionCanceled || !got.LastEventAt.Equal(t1) {
		t.Fatalf("forced write did not apply: %+v", got)
	}
}

func TestSubscriptionRepoUpsertIfNewerAppliesOverNullLastEventAt(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	if err := st.Subscriptions().EnsureTrial(ctx, "u1", time.Now().Add(domain.TrialLength)); err != nil {
		t.Fatalf("EnsureTrial: %v", err)
	}
	at := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	applied, err := st.Subscriptions().UpsertIfNewer(ctx, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive, BillingSubscriptionID: "sub_1", LastEventAt: &at})
	if err != nil || !applied {
		t.Fatalf("applied=%v err=%v, want applied over a NULL last_event_at", applied, err)
	}
	got, err := st.Subscriptions().GetByUserID(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.SubscriptionActive || got.LastEventAt == nil || !got.LastEventAt.Equal(at) {
		t.Fatalf("row = %+v", got)
	}
}
