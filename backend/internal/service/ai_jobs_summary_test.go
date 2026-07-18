package service

// ai_jobs_summary_test.go covers Task 6's runThreadSummary: the live
// auto-summarize job handler. It exercises the happy path (summary
// persisted via ThreadRepo.SetSummary), clamping an overlong model summary
// to 200 chars, an empty model summary surfacing as a retryable error (not
// a drop), the shared threadFor guard dropping jobs for vanished threads,
// and threadContext including only the newest aiContextMessages messages.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func newSummaryJobService(threads *fakeThreadRepo, msgs *fakeMessageRepo, ai *fakeAI, usage *fakeAiUsageRepo, clock *fakeClock) *AIJobService {
	return NewAIJobService(AIJobServiceDeps{
		Threads:    threads,
		Messages:   msgs,
		Usage:      usage,
		AI:         ai,
		Clock:      clock,
		DailyLimit: 300,
	})
}

func TestAIJobSummaryHappyPathPersistsSummary(t *testing.T) {
	baseTime := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", Subject: "Q3 planning"}); err != nil {
		t.Fatal(err)
	}
	msgs := newMessageRepo()
	if _, err := msgs.Upsert(ctx, domain.Message{
		ID: "m1", ThreadID: "t1", SentAt: baseTime,
		From: domain.EmailAddress{Email: "a@example.com"}, BodyText: "hi",
	}); err != nil {
		t.Fatal(err)
	}
	ai := newAI()
	ai.jsonOut = `{"summary":"Discussing Q3 roadmap, waiting on budget sign-off"}`
	ai.jsonModel = "gpt-test"
	usage := newAiUsageRepo()
	svc := newSummaryJobService(threads, msgs, ai, usage, newClock(baseTime))

	j := domain.AiJob{ID: "j1", UserID: "u1", Kind: domain.AiJobThreadSummary, ThreadID: strPtr("t1")}
	if err := svc.runThreadSummary(ctx, j); err != nil {
		t.Fatalf("runThreadSummary() error = %v, want nil", err)
	}

	got := threads.byID["t1"]
	if got.Summary != "Discussing Q3 roadmap, waiting on budget sign-off" {
		t.Fatalf("Summary = %q, want the model's summary", got.Summary)
	}
	if usage.calls["u1"] != 1 {
		t.Fatalf("usage.calls[u1] = %d, want 1 (budget charged for the LLM call)", usage.calls["u1"])
	}
	if ai.lastSystem != summarySystem {
		t.Fatalf("system prompt = %q, want summarySystem", ai.lastSystem)
	}
}

func TestAIJobSummaryClampsOverlongSummaryTo200Chars(t *testing.T) {
	ctx := context.Background()
	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1"}); err != nil {
		t.Fatal(err)
	}
	msgs := newMessageRepo()
	ai := newAI()
	long := strings.Repeat("x", 250)
	ai.jsonOut = `{"summary":"` + long + `"}`
	svc := newSummaryJobService(threads, msgs, ai, newAiUsageRepo(), newClock(time.Now()))

	j := domain.AiJob{ID: "j1", UserID: "u1", Kind: domain.AiJobThreadSummary, ThreadID: strPtr("t1")}
	if err := svc.runThreadSummary(ctx, j); err != nil {
		t.Fatalf("runThreadSummary() error = %v, want nil", err)
	}

	got := threads.byID["t1"].Summary
	if len([]rune(got)) > 201 {
		t.Fatalf("Summary rune length = %d, want <= 201 (200 + ellipsis), got %q", len([]rune(got)), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("Summary = %q, want truncated with an ellipsis suffix", got)
	}
}

func TestAIJobSummaryEmptyModelSummaryIsRetryableError(t *testing.T) {
	ctx := context.Background()
	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1"}); err != nil {
		t.Fatal(err)
	}
	msgs := newMessageRepo()
	ai := newAI()
	ai.jsonOut = `{"summary":"   "}`
	svc := newSummaryJobService(threads, msgs, ai, newAiUsageRepo(), newClock(time.Now()))

	j := domain.AiJob{ID: "j1", UserID: "u1", Kind: domain.AiJobThreadSummary, ThreadID: strPtr("t1")}
	err := svc.runThreadSummary(ctx, j)
	if err == nil {
		t.Fatal("runThreadSummary() error = nil, want non-nil for an empty model summary")
	}
	if errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want NOT ErrNotFound (must be retryable, not a drop)", err)
	}
	if got := threads.byID["t1"].Summary; got != "" {
		t.Fatalf("Summary = %q, want unset when the handler fails", got)
	}
}

func TestAIJobSummaryThreadGoneDropsJob(t *testing.T) {
	ctx := context.Background()
	threads := newThreadRepo() // empty: GetByID returns domain.ErrNotFound
	msgs := newMessageRepo()
	ai := newAI()
	svc := newSummaryJobService(threads, msgs, ai, newAiUsageRepo(), newClock(time.Now()))

	j := domain.AiJob{ID: "j1", UserID: "u1", Kind: domain.AiJobThreadSummary, ThreadID: strPtr("does-not-exist")}
	err := svc.runThreadSummary(ctx, j)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if ai.lastUser != "" {
		t.Fatalf("AI was called (lastUser=%q), want it skipped when the thread is gone", ai.lastUser)
	}
}

func TestAIJobSummaryContextIncludesOnlyNewestMessages(t *testing.T) {
	baseTime := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", Subject: "Long thread"}); err != nil {
		t.Fatal(err)
	}
	msgs := newMessageRepo()
	total := aiContextMessages + 5
	for i := range total {
		id := fmt.Sprintf("m%02d", i)
		if _, err := msgs.Upsert(ctx, domain.Message{
			ID:       id,
			ThreadID: "t1",
			SentAt:   baseTime.Add(time.Duration(i) * time.Minute),
			From:     domain.EmailAddress{Email: "a@example.com"},
			BodyText: fmt.Sprintf("marker-%02d", i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	ai := newAI()
	ai.jsonOut = `{"summary":"ok"}`
	svc := newSummaryJobService(threads, msgs, ai, newAiUsageRepo(), newClock(baseTime))

	j := domain.AiJob{ID: "j1", UserID: "u1", Kind: domain.AiJobThreadSummary, ThreadID: strPtr("t1")}
	if err := svc.runThreadSummary(ctx, j); err != nil {
		t.Fatalf("runThreadSummary() error = %v, want nil", err)
	}

	if strings.Contains(ai.lastUser, "marker-00") {
		t.Fatalf("prompt included the oldest message; want only the newest %d messages. prompt=%q", aiContextMessages, ai.lastUser)
	}
	wantMarker := fmt.Sprintf("marker-%02d", total-1)
	if !strings.Contains(ai.lastUser, wantMarker) {
		t.Fatalf("prompt missing newest marker %q; prompt=%q", wantMarker, ai.lastUser)
	}
}
