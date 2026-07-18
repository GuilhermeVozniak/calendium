package service

// ai_jobs_test.go covers AIJobService: graceful degradation with no AI
// configured, batch claim + dispatch, and the retry/backoff/dead-letter/drop
// policy in runJob (via classifyJobError). It also covers completeJSONBudgeted,
// the per-user daily budget gate every real LLM-calling handler must go
// through (Tasks 6-14) — direct-tested here since this task's handlers are
// still stubs that don't call it yet.
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
//
// Because the budget check now lives inside completeJSONBudgeted (called by
// no stub handler yet), errAIBudgetExhausted can't be produced by driving a
// job through ProcessDueAiJobs/dispatch today; its runJob mapping is instead
// covered by TestClassifyJobError's table below, which exercises
// classifyJobError directly as a pure function.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func strPtr(s string) *string        { return &s }
func timePtr(t time.Time) *time.Time { return &t }

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

	// j1 is a real thread_summary job (Task 6): give it a Messages repo and
	// an AI fake that returns a valid summary payload so this batch-dispatch
	// test (which only cares about claim/complete bookkeeping) doesn't trip
	// over runThreadSummary's now-real behavior.
	ai := newAI()
	ai.jsonOut = `{"summary":"ok"}`

	svc := NewAIJobService(AIJobServiceDeps{
		Jobs:     jobs,
		Usage:    newAiUsageRepo(),
		Threads:  threads,
		Messages: newMessageRepo(),
		Clock:    newClock(time.Now()),
		AI:       ai,
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

// TestCompleteJSONBudgeted covers the budget choke point every real
// LLM-calling handler (Tasks 6-14) must go through: IncrementAndCheck first,
// s.d.AI.CompleteJSON only when allowed.
func TestCompleteJSONBudgeted(t *testing.T) {
	type out struct {
		X string `json:"x"`
	}

	t.Run("allowed calls through to AI.CompleteJSON", func(t *testing.T) {
		ctx := context.Background()
		usage := newAiUsageRepo()
		ai := newAI()
		ai.jsonOut = `{"x":"hi"}`
		ai.jsonModel = "gpt-test"

		svc := NewAIJobService(AIJobServiceDeps{
			Usage:      usage,
			AI:         ai,
			Clock:      newClock(time.Now()),
			DailyLimit: 10,
		})

		var o out
		model, err := svc.completeJSONBudgeted(ctx, "u1", "sys", "usr", &o)
		if err != nil {
			t.Fatalf("completeJSONBudgeted() error = %v, want nil", err)
		}
		if model != "gpt-test" {
			t.Fatalf("model = %q, want gpt-test", model)
		}
		if o.X != "hi" {
			t.Fatalf("out.X = %q, want hi", o.X)
		}
		if usage.calls["u1"] != 1 {
			t.Fatalf("usage.calls[u1] = %d, want 1", usage.calls["u1"])
		}
	})

	t.Run("over budget returns errAIBudgetExhausted without calling AI", func(t *testing.T) {
		ctx := context.Background()
		usage := newAiUsageRepo()
		usage.calls["u1"] = 1 // already at the limit
		ai := newAI()
		ai.jsonOut = `{"x":"should not be reached"}`

		svc := NewAIJobService(AIJobServiceDeps{
			Usage:      usage,
			AI:         ai,
			Clock:      newClock(time.Now()),
			DailyLimit: 1,
		})

		var o out
		_, err := svc.completeJSONBudgeted(ctx, "u1", "sys", "usr", &o)
		if !errors.Is(err, errAIBudgetExhausted) {
			t.Fatalf("err = %v, want errAIBudgetExhausted", err)
		}
		if ai.lastSystem != "" || ai.lastUser != "" {
			t.Fatalf("AI.CompleteJSON was called (lastSystem=%q lastUser=%q), want it skipped over budget", ai.lastSystem, ai.lastUser)
		}
	})

	t.Run("usage repo error passes through, not errAIBudgetExhausted", func(t *testing.T) {
		ctx := context.Background()
		usage := newAiUsageRepo()
		usageErr := errors.New("usage repo unavailable")
		usage.err = usageErr
		ai := newAI()

		svc := NewAIJobService(AIJobServiceDeps{
			Usage:      usage,
			AI:         ai,
			Clock:      newClock(time.Now()),
			DailyLimit: 10,
		})

		var o out
		_, err := svc.completeJSONBudgeted(ctx, "u1", "sys", "usr", &o)
		if !errors.Is(err, usageErr) {
			t.Fatalf("err = %v, want usageErr", err)
		}
		if errors.Is(err, errAIBudgetExhausted) {
			t.Fatalf("err = %v, want NOT errAIBudgetExhausted (this is a repo error, not a budget verdict)", err)
		}
	})

	t.Run("AI error passes through unchanged", func(t *testing.T) {
		ctx := context.Background()
		usage := newAiUsageRepo()
		ai := newAI()
		aiErr := fmt.Errorf("%w: upstream 500", domain.ErrAIUnavailable)
		ai.jsonErr = aiErr

		svc := NewAIJobService(AIJobServiceDeps{
			Usage:      usage,
			AI:         ai,
			Clock:      newClock(time.Now()),
			DailyLimit: 10,
		})

		var o out
		_, err := svc.completeJSONBudgeted(ctx, "u1", "sys", "usr", &o)
		if !errors.Is(err, domain.ErrAIUnavailable) {
			t.Fatalf("err = %v, want it to wrap domain.ErrAIUnavailable", err)
		}
		if usage.calls["u1"] != 1 {
			t.Fatalf("usage.calls[u1] = %d, want 1 (budget is charged for the attempt even though the AI call failed)", usage.calls["u1"])
		}
	})
}

// TestClassifyJobError table-tests the pure retry/dead-letter decision
// function exhaustively, including the errAIBudgetExhausted and
// ErrRateLimited/ErrAIUnavailable branches that no stub handler in this task
// can yet reach through a full ProcessDueAiJobs run.
func TestClassifyJobError(t *testing.T) {
	now := time.Date(2026, 7, 18, 15, 30, 0, 0, time.UTC)
	wantMidnight := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name           string
		err            error
		attempts       int
		wantRetryAt    *time.Time
		wantDeadLetter bool
		wantDrop       bool
	}{
		{
			name:     "ErrNotFound drops regardless of attempts",
			err:      domain.ErrNotFound,
			attempts: 1,
			wantDrop: true,
		},
		{
			name:     "wrapped ErrNotFound still drops",
			err:      fmt.Errorf("%w: thread gone", domain.ErrNotFound),
			attempts: 3,
			wantDrop: true,
		},
		{
			name:        "budget exhausted rearms at next UTC midnight",
			err:         errAIBudgetExhausted,
			attempts:    1,
			wantRetryAt: &wantMidnight,
		},
		{
			name:        "budget exhausted never dead-letters even at the attempts cap",
			err:         errAIBudgetExhausted,
			attempts:    aiJobMaxAttempts,
			wantRetryAt: &wantMidnight,
		},
		{
			name:        "ErrRateLimited rearms flat 15m at attempt 1",
			err:         domain.ErrRateLimited,
			attempts:    1,
			wantRetryAt: timePtr(now.Add(15 * time.Minute)),
		},
		{
			name:        "ErrRateLimited rearms flat 15m at attempt 10, never dead-letters",
			err:         domain.ErrRateLimited,
			attempts:    10,
			wantRetryAt: timePtr(now.Add(15 * time.Minute)),
		},
		{
			name:        "wrapped ErrRateLimited still matches via errors.Is",
			err:         fmt.Errorf("%w: 429 from provider", domain.ErrRateLimited),
			attempts:    10,
			wantRetryAt: timePtr(now.Add(15 * time.Minute)),
		},
		{
			name:        "ErrAIUnavailable rearms flat 15m at attempt 1",
			err:         domain.ErrAIUnavailable,
			attempts:    1,
			wantRetryAt: timePtr(now.Add(15 * time.Minute)),
		},
		{
			name:        "ErrAIUnavailable rearms flat 15m at attempt 10, never dead-letters",
			err:         domain.ErrAIUnavailable,
			attempts:    10,
			wantRetryAt: timePtr(now.Add(15 * time.Minute)),
		},
		{
			name:        "wrapped ErrAIUnavailable still matches via errors.Is",
			err:         fmt.Errorf("%w: outage", domain.ErrAIUnavailable),
			attempts:    10,
			wantRetryAt: timePtr(now.Add(15 * time.Minute)),
		},
		{
			name:        "generic error at attempt 1 backs off 1m",
			err:         errors.New("boom"),
			attempts:    1,
			wantRetryAt: timePtr(now.Add(time.Minute)),
		},
		{
			name:        "generic error at attempt 2 backs off 4m",
			err:         errors.New("boom"),
			attempts:    2,
			wantRetryAt: timePtr(now.Add(4 * time.Minute)),
		},
		{
			name:        "generic error at attempt 3 backs off 16m",
			err:         errors.New("boom"),
			attempts:    3,
			wantRetryAt: timePtr(now.Add(16 * time.Minute)),
		},
		{
			name:           "generic error at attempts cap dead-letters",
			err:            errors.New("boom"),
			attempts:       aiJobMaxAttempts,
			wantDeadLetter: true,
		},
		{
			name:           "generic error beyond attempts cap still dead-letters",
			err:            errors.New("boom"),
			attempts:       aiJobMaxAttempts + 5,
			wantDeadLetter: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			retryAt, deadLetter, drop := classifyJobError(tc.err, tc.attempts, now)
			if drop != tc.wantDrop {
				t.Fatalf("drop = %v, want %v", drop, tc.wantDrop)
			}
			if deadLetter != tc.wantDeadLetter {
				t.Fatalf("deadLetter = %v, want %v", deadLetter, tc.wantDeadLetter)
			}
			if tc.wantRetryAt == nil {
				if retryAt != nil {
					t.Fatalf("retryAt = %v, want nil", retryAt)
				}
				return
			}
			if retryAt == nil || !retryAt.Equal(*tc.wantRetryAt) {
				t.Fatalf("retryAt = %v, want %v", retryAt, *tc.wantRetryAt)
			}
		})
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

	usage := newAiUsageRepo()
	svc := NewAIJobService(AIJobServiceDeps{
		Jobs:       jobs,
		Usage:      usage,
		Threads:    newThreadRepo(), // empty: GetByID returns domain.ErrNotFound
		Clock:      newClock(time.Now()),
		AI:         newAI(),
		DailyLimit: 10,
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
	// Fix 1: the guard-fetch drop (deleted thread) must never spend budget —
	// runJob no longer charges IncrementAndCheck before dispatch, so a
	// dropped job leaves the usage fake untouched.
	if len(usage.calls) != 0 {
		t.Fatalf("usage.calls = %v, want empty (drop path must consume no budget)", usage.calls)
	}
}

func TestRunJobVoiceProfileHasNoThreadGuard(t *testing.T) {
	// voice_profile jobs carry a nil ThreadID; confirm dispatch routes them
	// to runVoiceProfile without requiring Threads at all (no panic on nil
	// deref). Task 11 fills in real learning (see ai_jobs_voice_test.go for
	// full coverage); this test only pins the "no thread needed" contract,
	// using an account with no sent mail yet so the handler completes via
	// its below-minimum-samples path without touching Threads.
	ctx := context.Background()

	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", UserID: "u1", Email: "alex@example.com"}); err != nil {
		t.Fatal(err)
	}

	jobs := newAiJobRepo()
	jobs.queue = []domain.AiJob{{ID: "j1", UserID: "u1", AccountID: "a1", Kind: domain.AiJobVoiceProfile, Attempts: 1}}

	svc := NewAIJobService(AIJobServiceDeps{
		Jobs:          jobs,
		Usage:         newAiUsageRepo(),
		Accounts:      accounts,
		Messages:      newMessageRepo(),
		VoiceProfiles: newVoiceProfileRepo(),
		Clock:         newClock(time.Now()),
		AI:            newAI(),
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
