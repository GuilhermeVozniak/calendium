package service

// ai_jobs_test.go covers AIJobService: graceful degradation with no AI
// configured, batch claim + dispatch, the per-user daily budget gate, and
// the retry/backoff/dead-letter/drop policy in runJob's error switch.
//
// The run* handlers are stubs in this task (Tasks 6-11 fill in real
// generation); the only real behavior they have today is the shared
// threadFor existence guard. That guard is what makes the ErrNotFound-drops
// path exercisable here: a job whose ThreadID doesn't resolve in
// fakeThreadRepo surfaces domain.ErrNotFound exactly like a thread deleted
// out from under a queued job would in production. The "handler error" and
// "unknown kind" scenarios both use an unrecognized Kind, since it's the
// only source of a non-nil, non-ErrNotFound error while the six known kinds
// are stubbed to succeed once their thread guard passes.

import (
	"context"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func strPtr(s string) *string { return &s }

func TestProcessDueAiJobsNoAIConfigured(t *testing.T) {
	jobs := newAiJobRepo()
	jobs.queue = []domain.AiJob{{ID: "j1", UserID: "u1", Kind: domain.AiJobThreadSummary}}

	svc := NewAIJobService(AIJobServiceDeps{
		Jobs:  jobs,
		Usage: newAiUsageRepo(),
		Clock: newClock(time.Now()),
		AI:    nil, // not configured
	})

	if err := svc.ProcessDueAiJobs(context.Background()); err != nil {
		t.Fatalf("ProcessDueAiJobs() error = %v, want nil", err)
	}
	if jobs.claimCalls != 0 {
		t.Fatalf("ClaimDue called %d times, want 0 (no-op when AI is nil)", jobs.claimCalls)
	}
}

func TestProcessDueAiJobsDispatchesClaimedBatch(t *testing.T) {
	ctx := context.Background()
	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t2"}); err != nil {
		t.Fatal(err)
	}

	jobs := newAiJobRepo()
	jobs.queue = []domain.AiJob{
		{ID: "j1", UserID: "u1", Kind: domain.AiJobThreadSummary, ThreadID: strPtr("t1"), Attempts: 1},
		{ID: "j2", UserID: "u1", Kind: domain.AiJobInstantReplies, ThreadID: strPtr("t2"), Attempts: 1},
	}

	svc := NewAIJobService(AIJobServiceDeps{
		Jobs:    jobs,
		Usage:   newAiUsageRepo(),
		Threads: threads,
		Clock:   newClock(time.Now()),
		AI:      newAI(),
	})

	if err := svc.ProcessDueAiJobs(ctx); err != nil {
		t.Fatalf("ProcessDueAiJobs() error = %v, want nil", err)
	}
	if jobs.claimCalls != 1 {
		t.Fatalf("ClaimDue called %d times, want 1", jobs.claimCalls)
	}
	if got := jobs.completed; len(got) != 2 || got[0] != "j1" || got[1] != "j2" {
		t.Fatalf("completed = %v, want [j1 j2]", got)
	}
	if len(jobs.failed) != 0 {
		t.Fatalf("failed = %v, want none", jobs.failed)
	}
}

func TestRunJobBudgetExhaustedParksUntilNextMidnight(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 15, 30, 0, 0, time.UTC)
	clock := newClock(now)

	usage := newAiUsageRepo()
	usage.calls["u1"] = 1 // one call already made against a limit of 1

	jobs := newAiJobRepo()
	jobs.queue = []domain.AiJob{
		{ID: "j1", UserID: "u1", Kind: domain.AiJobThreadSummary, ThreadID: strPtr("missing"), Attempts: 1},
	}

	svc := NewAIJobService(AIJobServiceDeps{
		Jobs:       jobs,
		Usage:      usage,
		Threads:    newThreadRepo(),
		Clock:      clock,
		AI:         newAI(),
		DailyLimit: 1,
	})

	if err := svc.ProcessDueAiJobs(ctx); err != nil {
		t.Fatalf("ProcessDueAiJobs() error = %v, want nil (budget exhaustion is not a job error)", err)
	}
	if len(jobs.completed) != 0 {
		t.Fatalf("completed = %v, want none (budget check happens before dispatch)", jobs.completed)
	}
	if len(jobs.failed) != 1 {
		t.Fatalf("failed count = %d, want 1", len(jobs.failed))
	}
	got := jobs.failed[0]
	if got.ID != "j1" {
		t.Fatalf("failed job id = %q, want j1", got.ID)
	}
	if got.ErrMsg != "daily ai budget exhausted" {
		t.Fatalf("errMsg = %q, want %q", got.ErrMsg, "daily ai budget exhausted")
	}
	wantRetry := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	if got.RetryAt == nil || !got.RetryAt.Equal(wantRetry) {
		t.Fatalf("retryAt = %v, want %v", got.RetryAt, wantRetry)
	}
}

func TestRunJobHandlerErrorBackoffRetry(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	clock := newClock(now)

	jobs := newAiJobRepo()
	jobs.queue = []domain.AiJob{
		{ID: "j1", UserID: "u1", Kind: "bogus_kind", Attempts: 2}, // below aiJobMaxAttempts (4)
	}

	svc := NewAIJobService(AIJobServiceDeps{
		Jobs:  jobs,
		Usage: newAiUsageRepo(),
		Clock: clock,
		AI:    newAI(),
	})

	if err := svc.ProcessDueAiJobs(ctx); err == nil {
		t.Fatal("ProcessDueAiJobs() error = nil, want non-nil (handler error propagates)")
	}
	if len(jobs.completed) != 0 {
		t.Fatalf("completed = %v, want none", jobs.completed)
	}
	if len(jobs.failed) != 1 {
		t.Fatalf("failed count = %d, want 1", len(jobs.failed))
	}
	got := jobs.failed[0]
	if !strings.Contains(got.ErrMsg, "unknown ai job kind") {
		t.Fatalf("errMsg = %q, want it to mention the unknown kind", got.ErrMsg)
	}
	wantRetry := now.Add(aiJobBackoff(2)) // 4 minutes
	if got.RetryAt == nil || !got.RetryAt.Equal(wantRetry) {
		t.Fatalf("retryAt = %v, want %v", got.RetryAt, wantRetry)
	}
}

func TestRunJobAttemptsCapDeadLetters(t *testing.T) {
	ctx := context.Background()

	jobs := newAiJobRepo()
	jobs.queue = []domain.AiJob{
		{ID: "j1", UserID: "u1", Kind: "bogus_kind", Attempts: aiJobMaxAttempts}, // at the cap
	}

	svc := NewAIJobService(AIJobServiceDeps{
		Jobs:  jobs,
		Usage: newAiUsageRepo(),
		Clock: newClock(time.Now()),
		AI:    newAI(),
	})

	err := svc.ProcessDueAiJobs(ctx)
	if err == nil {
		t.Fatal("ProcessDueAiJobs() error = nil, want non-nil (dead-lettered job still reports)")
	}
	if !strings.Contains(err.Error(), "dead-lettered after 4 attempts") {
		t.Fatalf("error = %q, want it to mention dead-lettering", err.Error())
	}
	if len(jobs.completed) != 0 {
		t.Fatalf("completed = %v, want none", jobs.completed)
	}
	if len(jobs.failed) != 1 {
		t.Fatalf("failed count = %d, want 1", len(jobs.failed))
	}
	if got := jobs.failed[0]; got.RetryAt != nil {
		t.Fatalf("retryAt = %v, want nil (dead-letter, no re-arm)", got.RetryAt)
	}
}

func TestRunJobUnknownKindSurfacesDispatchError(t *testing.T) {
	// Distinct from the attempts-cap test above: this asserts dispatch()'s
	// default branch itself (not just the runJob switch) produces the
	// expected error for a kind none of the six known handlers own.
	ctx := context.Background()

	jobs := newAiJobRepo()
	jobs.queue = []domain.AiJob{{ID: "j1", UserID: "u1", Kind: "not_a_real_kind", Attempts: 1}}

	svc := NewAIJobService(AIJobServiceDeps{
		Jobs:  jobs,
		Usage: newAiUsageRepo(),
		Clock: newClock(time.Now()),
		AI:    newAI(),
	})

	err := svc.ProcessDueAiJobs(ctx)
	if err == nil {
		t.Fatal("ProcessDueAiJobs() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), `unknown ai job kind "not_a_real_kind"`) {
		t.Fatalf("error = %q, want it to name the unrecognized kind", err.Error())
	}
}

func TestRunJobErrNotFoundDropsJob(t *testing.T) {
	ctx := context.Background()

	jobs := newAiJobRepo()
	jobs.queue = []domain.AiJob{
		{ID: "j1", UserID: "u1", Kind: domain.AiJobThreadSummary, ThreadID: strPtr("does-not-exist"), Attempts: 1},
	}

	svc := NewAIJobService(AIJobServiceDeps{
		Jobs:    jobs,
		Usage:   newAiUsageRepo(),
		Threads: newThreadRepo(), // empty: GetByID returns domain.ErrNotFound
		Clock:   newClock(time.Now()),
		AI:      newAI(),
	})

	if err := svc.ProcessDueAiJobs(ctx); err != nil {
		t.Fatalf("ProcessDueAiJobs() error = %v, want nil (vanished thread is dropped, not an error)", err)
	}
	if len(jobs.failed) != 0 {
		t.Fatalf("failed = %v, want none", jobs.failed)
	}
	if got := jobs.completed; len(got) != 1 || got[0] != "j1" {
		t.Fatalf("completed = %v, want [j1]", got)
	}
}

func TestRunJobVoiceProfileHasNoThreadGuard(t *testing.T) {
	// voice_profile jobs carry a nil ThreadID; confirm dispatch routes them
	// to the stub without requiring Threads at all (no panic on nil deref).
	ctx := context.Background()

	jobs := newAiJobRepo()
	jobs.queue = []domain.AiJob{{ID: "j1", UserID: "u1", Kind: domain.AiJobVoiceProfile, Attempts: 1}}

	svc := NewAIJobService(AIJobServiceDeps{
		Jobs:  jobs,
		Usage: newAiUsageRepo(),
		Clock: newClock(time.Now()),
		AI:    newAI(),
	})

	if err := svc.ProcessDueAiJobs(ctx); err != nil {
		t.Fatalf("ProcessDueAiJobs() error = %v, want nil", err)
	}
	if got := jobs.completed; len(got) != 1 || got[0] != "j1" {
		t.Fatalf("completed = %v, want [j1]", got)
	}
}

func TestNewAIJobServiceDefaultsDailyLimit(t *testing.T) {
	svc := NewAIJobService(AIJobServiceDeps{DailyLimit: 0})
	if svc.d.DailyLimit != 300 {
		t.Fatalf("DailyLimit = %d, want 300 (default)", svc.d.DailyLimit)
	}
	svc = NewAIJobService(AIJobServiceDeps{DailyLimit: -5})
	if svc.d.DailyLimit != 300 {
		t.Fatalf("DailyLimit = %d, want 300 (default for negative input)", svc.d.DailyLimit)
	}
	svc = NewAIJobService(AIJobServiceDeps{DailyLimit: 42})
	if svc.d.DailyLimit != 42 {
		t.Fatalf("DailyLimit = %d, want 42 (explicit value preserved)", svc.d.DailyLimit)
	}
}

// TestNewAIJobServiceFullDepsWiring exercises every AIJobServiceDeps field
// wired at once (as cmd/worker/main.go does), confirming the whole deps
// struct compiles/wires cleanly even though most fields go untouched by
// this task's stub handlers.
func TestNewAIJobServiceFullDepsWiring(t *testing.T) {
	svc := NewAIJobService(AIJobServiceDeps{
		Jobs:          newAiJobRepo(),
		Usage:         newAiUsageRepo(),
		Accounts:      newAccountRepo(),
		Threads:       newThreadRepo(),
		Messages:      newMessageRepo(),
		Drafts:        newDraftRepo(newAccountRepo()),
		Labels:        newLabelRepo(),
		Classifiers:   newClassifierRepo(),
		VoiceProfiles: newVoiceProfileRepo(),
		Calendar:      nil, // unused by this task's stub handlers
		AI:            newAI(),
		Clock:         newClock(time.Now()),
	})

	if err := svc.ProcessDueAiJobs(context.Background()); err != nil {
		t.Fatalf("ProcessDueAiJobs() error = %v, want nil (empty queue)", err)
	}
}

func TestAiJobBackoff(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, time.Minute},
		{2, 4 * time.Minute},
		{3, 16 * time.Minute},
		{4, 30 * time.Minute}, // 64m uncapped, capped to 30m
		{5, 30 * time.Minute},
	}
	for _, tc := range cases {
		if got := aiJobBackoff(tc.attempt); got != tc.want {
			t.Errorf("aiJobBackoff(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}
