package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

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
		UserID:           "u1",
		Status:           domain.SubscriptionActive,
		StripeCustomerID: "cus_123",
		CurrentPeriodEnd: &periodEnd,
		LastEventAt:      &lastEvent,
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
	if got.StripeCustomerID != "cus_123" {
		t.Fatalf("StripeCustomerID = %q, want cus_123", got.StripeCustomerID)
	}
	if got.CurrentPeriodEnd == nil || !got.CurrentPeriodEnd.Equal(periodEnd) {
		t.Fatalf("CurrentPeriodEnd = %v, want %v", got.CurrentPeriodEnd, periodEnd)
	}
	if got.LastEventAt == nil || !got.LastEventAt.Equal(lastEvent) {
		t.Fatalf("LastEventAt = %v, want %v", got.LastEventAt, lastEvent)
	}

	byCustomer, err := st.Subscriptions().GetByStripeCustomerID(ctx, "cus_123")
	if err != nil {
		t.Fatalf("GetByStripeCustomerID: %v", err)
	}
	if byCustomer.UserID != "u1" {
		t.Fatalf("byCustomer.UserID = %q, want u1", byCustomer.UserID)
	}

	if _, err := st.Subscriptions().GetByStripeCustomerID(ctx, "cus_unknown"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByStripeCustomerID unknown: err = %v, want ErrNotFound", err)
	}
}

func TestSubscriptionRepoUpsertPreservesCustomerIDWhenEmpty(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	if err := st.Subscriptions().Upsert(ctx, domain.Subscription{
		UserID: "u1", Status: domain.SubscriptionActive, StripeCustomerID: "cus_original",
	}); err != nil {
		t.Fatalf("Upsert first: %v", err)
	}
	if err := st.Subscriptions().Upsert(ctx, domain.Subscription{
		UserID: "u1", Status: domain.SubscriptionActive, StripeCustomerID: "",
	}); err != nil {
		t.Fatalf("Upsert second: %v", err)
	}

	got, err := st.Subscriptions().GetByUserID(ctx, "u1")
	if err != nil {
		t.Fatalf("GetByUserID: %v", err)
	}
	if got.StripeCustomerID != "cus_original" {
		t.Fatalf("StripeCustomerID = %q, want preserved cus_original", got.StripeCustomerID)
	}
}

func TestSubscriptionRepoGetByUserIDMissing(t *testing.T) {
	st, _ := newTestStore(t)
	if _, err := st.Subscriptions().GetByUserID(context.Background(), "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
