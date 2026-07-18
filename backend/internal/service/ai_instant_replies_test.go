package service

// ai_instant_replies_test.go covers AIService.InstantReplies, the on-open
// fallback a client hits when a thread's InstantReplies cache is empty or
// stale (see the doc comment on InstantReplies in ai.go): ownership via
// ownedThread, a fresh cache returned with no AI call, a stale cache
// regenerated, ErrAIUnavailable with no AI configured, and ErrRateLimited
// once the daily budget is exhausted.

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func TestAIServiceInstantRepliesFreshCacheSkipsAI(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	lastMessage := now.Add(-2 * time.Hour)
	generatedAt := now.Add(-time.Hour) // newer than lastMessage: fresh

	accounts := newAccountRepo()
	accounts.byID["a1"] = domain.ConnectedAccount{ID: "a1", UserID: "u1"}
	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{
		ID: "t1", AccountID: "a1", LastMessageAt: lastMessage,
		InstantReplies: []string{"cached one", "cached two"}, InstantRepliesUpdatedAt: timePtr(generatedAt),
	}); err != nil {
		t.Fatal(err)
	}

	ai := newAI()
	ai.jsonOut = `{"replies": ["should not be reached"]}`

	svc := NewAIService(AIServiceDeps{
		Accounts:   accounts,
		Threads:    threads,
		Messages:   newMessageRepo(),
		Usage:      newAiUsageRepo(),
		AI:         ai,
		Clock:      newClock(now),
		DailyLimit: 10,
	})

	got, err := svc.InstantReplies(ctx, "u1", "t1")
	if err != nil {
		t.Fatalf("InstantReplies() error = %v, want nil", err)
	}
	if len(got) != 2 || got[0] != "cached one" || got[1] != "cached two" {
		t.Fatalf("InstantReplies() = %+v, want the cached replies unchanged", got)
	}
	if ai.lastSystem != "" {
		t.Fatalf("AI was called (lastSystem=%q), want fresh cache to skip generation entirely", ai.lastSystem)
	}
}

func TestAIServiceInstantRepliesStaleCacheRegenerates(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	generatedAt := now.Add(-2 * time.Hour)
	newMessageAt := now.Add(-time.Hour) // newer than generatedAt: stale

	accounts := newAccountRepo()
	accounts.byID["a1"] = domain.ConnectedAccount{ID: "a1", UserID: "u1"}
	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{
		ID: "t1", AccountID: "a1", Subject: "Re: budget", LastMessageAt: newMessageAt,
		InstantReplies: []string{"stale reply"}, InstantRepliesUpdatedAt: timePtr(generatedAt),
	}); err != nil {
		t.Fatal(err)
	}
	messages := newMessageRepo()
	if _, err := messages.Upsert(ctx, domain.Message{
		ID: "m1", ThreadID: "t1", From: domain.EmailAddress{Email: "boss@acme.com"},
		BodyText: "New question just now", SentAt: newMessageAt,
	}); err != nil {
		t.Fatal(err)
	}

	ai := newAI()
	ai.jsonOut = `{"replies": ["fresh reply one", "fresh reply two"]}`
	usage := newAiUsageRepo()

	svc := NewAIService(AIServiceDeps{
		Accounts:   accounts,
		Threads:    threads,
		Messages:   messages,
		Usage:      usage,
		AI:         ai,
		Clock:      newClock(now),
		DailyLimit: 10,
	})

	got, err := svc.InstantReplies(ctx, "u1", "t1")
	if err != nil {
		t.Fatalf("InstantReplies() error = %v, want nil", err)
	}
	if len(got) != 2 || got[0] != "fresh reply one" || got[1] != "fresh reply two" {
		t.Fatalf("InstantReplies() = %+v, want the freshly generated replies", got)
	}
	if usage.calls["u1"] != 1 {
		t.Fatalf("usage.calls[u1] = %d, want 1 (regenerate charges budget)", usage.calls["u1"])
	}
	persisted, err := threads.GetByID(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.InstantReplies) != 2 || persisted.InstantReplies[0] != "fresh reply one" {
		t.Fatalf("persisted InstantReplies = %+v, want the fresh replies saved via SetInstantReplies", persisted.InstantReplies)
	}
}

func TestAIServiceInstantRepliesNoCacheGeneratesAndPersists(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

	accounts := newAccountRepo()
	accounts.byID["a1"] = domain.ConnectedAccount{ID: "a1", UserID: "u1"}
	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", AccountID: "a1", LastMessageAt: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}

	ai := newAI()
	ai.jsonOut = `{"replies": ["a", "b", "c"]}`

	svc := NewAIService(AIServiceDeps{
		Accounts:   accounts,
		Threads:    threads,
		Messages:   newMessageRepo(),
		Usage:      newAiUsageRepo(),
		AI:         ai,
		Clock:      newClock(now),
		DailyLimit: 10,
	})

	got, err := svc.InstantReplies(ctx, "u1", "t1")
	if err != nil {
		t.Fatalf("InstantReplies() error = %v, want nil", err)
	}
	if len(got) != 3 {
		t.Fatalf("InstantReplies() = %+v, want 3 replies", got)
	}
}

func TestAIServiceInstantRepliesNoAIConfigured(t *testing.T) {
	ctx := context.Background()

	accounts := newAccountRepo()
	accounts.byID["a1"] = domain.ConnectedAccount{ID: "a1", UserID: "u1"}
	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", AccountID: "a1"}); err != nil {
		t.Fatal(err)
	}

	svc := NewAIService(AIServiceDeps{
		Accounts: accounts,
		Threads:  threads,
		Messages: newMessageRepo(),
		Usage:    newAiUsageRepo(),
		AI:       nil, // not configured
		Clock:    newClock(time.Now()),
	})

	_, err := svc.InstantReplies(ctx, "u1", "t1")
	if !errors.Is(err, domain.ErrAIUnavailable) {
		t.Fatalf("err = %v, want domain.ErrAIUnavailable", err)
	}
}

func TestAIServiceInstantRepliesBudgetExhausted(t *testing.T) {
	ctx := context.Background()

	accounts := newAccountRepo()
	accounts.byID["a1"] = domain.ConnectedAccount{ID: "a1", UserID: "u1"}
	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", AccountID: "a1"}); err != nil {
		t.Fatal(err)
	}

	usage := newAiUsageRepo()
	usage.calls["u1"] = 1 // already at the limit

	ai := newAI()
	ai.jsonOut = `{"replies": ["should not be reached"]}`

	svc := NewAIService(AIServiceDeps{
		Accounts:   accounts,
		Threads:    threads,
		Messages:   newMessageRepo(),
		Usage:      usage,
		AI:         ai,
		Clock:      newClock(time.Now()),
		DailyLimit: 1,
	})

	_, err := svc.InstantReplies(ctx, "u1", "t1")
	if !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("err = %v, want domain.ErrRateLimited", err)
	}
	if ai.lastSystem != "" {
		t.Fatalf("AI was called (lastSystem=%q), want budget exhaustion to skip generation", ai.lastSystem)
	}
}

func TestAIServiceInstantRepliesForeignThreadNotFound(t *testing.T) {
	ctx := context.Background()

	accounts := newAccountRepo()
	accounts.byID["a1"] = domain.ConnectedAccount{ID: "a1", UserID: "someone-else"}
	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", AccountID: "a1"}); err != nil {
		t.Fatal(err)
	}

	svc := NewAIService(AIServiceDeps{
		Accounts: accounts,
		Threads:  threads,
		Messages: newMessageRepo(),
		Usage:    newAiUsageRepo(),
		AI:       newAI(),
		Clock:    newClock(time.Now()),
	})

	_, err := svc.InstantReplies(ctx, "u1", "t1")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want domain.ErrNotFound (foreign thread)", err)
	}
}
