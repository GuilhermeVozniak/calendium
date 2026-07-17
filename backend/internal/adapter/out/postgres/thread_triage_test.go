package postgres

import (
	"context"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func TestThreadPersistsUnsubscribeFields(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	if _, err := store.Users().Upsert(ctx, domain.User{ID: "u1", Email: "u1@example.com"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := store.Accounts().Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "u1@example.com",
	}); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	mailto := "mailto:unsub@news.example"
	link := "https://news.example/unsub?u=1"
	saved, err := store.Threads().Upsert(ctx, domain.Thread{
		ID: "t1", AccountID: "a1", ProviderThreadID: "pt1",
		LastMessageAt:       time.Now().UTC(),
		UnsubscribeMailto:   &mailto,
		UnsubscribeURL:      &link,
		UnsubscribeOneClick: true,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := store.Threads().GetByID(ctx, saved.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.UnsubscribeMailto == nil || *got.UnsubscribeMailto != mailto {
		t.Fatalf("UnsubscribeMailto = %v, want %q", got.UnsubscribeMailto, mailto)
	}
	if got.UnsubscribeURL == nil || *got.UnsubscribeURL != link {
		t.Fatalf("UnsubscribeURL = %v, want %q", got.UnsubscribeURL, link)
	}
	if !got.UnsubscribeOneClick {
		t.Fatal("UnsubscribeOneClick = false, want true")
	}

	// Update clears them (sender stopped offering unsubscribe).
	got.UnsubscribeMailto, got.UnsubscribeURL, got.UnsubscribeOneClick = nil, nil, false
	if err := store.Threads().Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got2, err := store.Threads().GetByID(ctx, saved.ID)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if got2.UnsubscribeMailto != nil || got2.UnsubscribeURL != nil || got2.UnsubscribeOneClick {
		t.Fatalf("unsubscribe fields not cleared: %+v", got2)
	}
}

func TestListInboxBefore(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	if _, err := store.Users().Upsert(ctx, domain.User{ID: "u1", Email: "u1@example.com"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := store.Accounts().Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "u1@example.com",
	}); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	cutoff := time.Now().UTC().Add(-7 * 24 * time.Hour)

	// Old, unsnoozed thread (should be included)
	old := domain.Thread{
		ID:             "t-old",
		AccountID:      "a1",
		ProviderThreadID: "pt-old",
		InInbox:        true,
		LastMessageAt: cutoff.Add(-time.Hour),
	}
	if _, err := store.Threads().Upsert(ctx, old); err != nil {
		t.Fatalf("seed old thread: %v", err)
	}

	// Old, snoozed thread (should be excluded)
	futureTime := cutoff.Add(time.Hour)
	snoozed := domain.Thread{
		ID:             "t-snoozed",
		AccountID:      "a1",
		ProviderThreadID: "pt-snoozed",
		InInbox:        true,
		LastMessageAt: cutoff.Add(-2 * time.Hour),
		SnoozedUntil:   &futureTime,
	}
	if _, err := store.Threads().Upsert(ctx, snoozed); err != nil {
		t.Fatalf("seed snoozed thread: %v", err)
	}

	// New thread (should be excluded)
	new := domain.Thread{
		ID:             "t-new",
		AccountID:      "a1",
		ProviderThreadID: "pt-new",
		InInbox:        true,
		LastMessageAt: cutoff.Add(time.Hour),
	}
	if _, err := store.Threads().Upsert(ctx, new); err != nil {
		t.Fatalf("seed new thread: %v", err)
	}

	// Query
	got, err := store.Threads().ListInboxBefore(ctx, "u1", cutoff, 10)
	if err != nil {
		t.Fatalf("ListInboxBefore: %v", err)
	}

	// Expect only the old, unsnoozed thread
	if len(got) != 1 {
		t.Fatalf("got %d threads, want 1", len(got))
	}
	if got[0].ID != "t-old" {
		t.Fatalf("got thread %q, want t-old", got[0].ID)
	}
}
