package service

// ai_jobs_instant_replies_test.go covers AIJobService.runInstantReplies
// directly (same package, so the unexported method is callable without
// driving a full ProcessDueAiJobs/dispatch round trip): persisting a clean
// 1-3 reply response, clamping a >3 reply response, and surfacing a 0-reply
// response as a plain error that classifyJobError retries generically
// (see runInstantReplies's doc comment in ai_jobs.go).

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func TestAIJobInstantRepliesPersistsReplies(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", Subject: "Q3 planning"}); err != nil {
		t.Fatal(err)
	}
	messages := newMessageRepo()
	if _, err := messages.Upsert(ctx, domain.Message{
		ID: "m1", ThreadID: "t1", From: domain.EmailAddress{Email: "boss@acme.com"},
		BodyText: "Can you send the deck by Friday?", SentAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	ai := newAI()
	ai.jsonOut = `{"replies": ["Sounds good, will do.", "I can't make Friday, how about Monday?", "What deck do you mean?"]}`
	ai.jsonModel = "gpt-test"
	usage := newAiUsageRepo()

	svc := NewAIJobService(AIJobServiceDeps{
		Threads:    threads,
		Messages:   messages,
		Usage:      usage,
		AI:         ai,
		Clock:      newClock(now),
		DailyLimit: 10,
	})

	job := domain.AiJob{ID: "j1", UserID: "u1", Kind: domain.AiJobInstantReplies, ThreadID: strPtr("t1")}
	if err := svc.runInstantReplies(ctx, job); err != nil {
		t.Fatalf("runInstantReplies() error = %v, want nil", err)
	}

	got, err := threads.GetByID(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Sounds good, will do.", "I can't make Friday, how about Monday?", "What deck do you mean?"}
	if len(got.InstantReplies) != len(want) {
		t.Fatalf("InstantReplies = %+v, want %+v", got.InstantReplies, want)
	}
	for i, w := range want {
		if got.InstantReplies[i] != w {
			t.Fatalf("InstantReplies[%d] = %q, want %q", i, got.InstantReplies[i], w)
		}
	}
	if usage.calls["u1"] != 1 {
		t.Fatalf("usage.calls[u1] = %d, want 1 (budget charged for the generate)", usage.calls["u1"])
	}
	if ai.lastSystem != instantRepliesSystem {
		t.Fatalf("lastSystem = %q, want the instant replies system prompt", ai.lastSystem)
	}
}

func TestAIJobInstantRepliesClampsMoreThanThree(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1"}); err != nil {
		t.Fatal(err)
	}
	messages := newMessageRepo()

	ai := newAI()
	ai.jsonOut = `{"replies": ["one", "two", "three", "four", "five"]}`

	svc := NewAIJobService(AIJobServiceDeps{
		Threads:    threads,
		Messages:   messages,
		Usage:      newAiUsageRepo(),
		AI:         ai,
		Clock:      newClock(now),
		DailyLimit: 10,
	})

	job := domain.AiJob{ID: "j1", UserID: "u1", Kind: domain.AiJobInstantReplies, ThreadID: strPtr("t1")}
	if err := svc.runInstantReplies(ctx, job); err != nil {
		t.Fatalf("runInstantReplies() error = %v, want nil", err)
	}

	got, err := threads.GetByID(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.InstantReplies) != 3 {
		t.Fatalf("InstantReplies = %+v, want exactly 3 (clamped)", got.InstantReplies)
	}
	want := []string{"one", "two", "three"}
	for i, w := range want {
		if got.InstantReplies[i] != w {
			t.Fatalf("InstantReplies[%d] = %q, want %q", i, got.InstantReplies[i], w)
		}
	}
}

func TestAIJobInstantRepliesZeroRepliesIsRetryError(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1"}); err != nil {
		t.Fatal(err)
	}
	messages := newMessageRepo()

	ai := newAI()
	ai.jsonOut = `{"replies": []}`

	svc := NewAIJobService(AIJobServiceDeps{
		Threads:    threads,
		Messages:   messages,
		Usage:      newAiUsageRepo(),
		AI:         ai,
		Clock:      newClock(now),
		DailyLimit: 10,
	})

	job := domain.AiJob{ID: "j1", UserID: "u1", Kind: domain.AiJobInstantReplies, ThreadID: strPtr("t1")}
	err := svc.runInstantReplies(ctx, job)
	if err == nil {
		t.Fatal("runInstantReplies() error = nil, want non-nil (0 replies is malformed AI output)")
	}
	if !errors.Is(err, domain.ErrAIOutput) {
		t.Fatalf("err = %v, want it to wrap domain.ErrAIOutput", err)
	}
	// classifyJobError has no special case for ErrAIOutput, so it must take
	// the generic backoff/retry path, not drop or dead-letter immediately.
	retryAt, deadLetter, drop := classifyJobError(err, 1, now)
	if drop || deadLetter {
		t.Fatalf("classifyJobError: drop=%v deadLetter=%v, want generic retry", drop, deadLetter)
	}
	if retryAt == nil {
		t.Fatal("classifyJobError: retryAt = nil, want a generic backoff retry time")
	}

	got, err := threads.GetByID(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.InstantReplies) != 0 {
		t.Fatalf("InstantReplies = %+v, want unchanged (nothing persisted on error)", got.InstantReplies)
	}
}

func TestAIJobInstantRepliesBlankEntriesFiltered(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1"}); err != nil {
		t.Fatal(err)
	}

	ai := newAI()
	ai.jsonOut = `{"replies": ["  ", "actual reply", ""]}`

	svc := NewAIJobService(AIJobServiceDeps{
		Threads:    threads,
		Messages:   newMessageRepo(),
		Usage:      newAiUsageRepo(),
		AI:         ai,
		Clock:      newClock(now),
		DailyLimit: 10,
	})

	job := domain.AiJob{ID: "j1", UserID: "u1", Kind: domain.AiJobInstantReplies, ThreadID: strPtr("t1")}
	if err := svc.runInstantReplies(ctx, job); err != nil {
		t.Fatalf("runInstantReplies() error = %v, want nil", err)
	}
	got, err := threads.GetByID(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.InstantReplies) != 1 || got.InstantReplies[0] != "actual reply" {
		t.Fatalf("InstantReplies = %+v, want [\"actual reply\"] (blanks filtered)", got.InstantReplies)
	}
}

func TestAIJobInstantRepliesMissingThreadDrops(t *testing.T) {
	ctx := context.Background()

	svc := NewAIJobService(AIJobServiceDeps{
		Threads:    newThreadRepo(), // empty
		Messages:   newMessageRepo(),
		Usage:      newAiUsageRepo(),
		AI:         newAI(),
		Clock:      newClock(time.Now()),
		DailyLimit: 10,
	})

	job := domain.AiJob{ID: "j1", UserID: "u1", Kind: domain.AiJobInstantReplies, ThreadID: strPtr("does-not-exist")}
	err := svc.runInstantReplies(ctx, job)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want domain.ErrNotFound (guard-fetch drop)", err)
	}
}
