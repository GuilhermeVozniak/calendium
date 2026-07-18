package service

// ai_jobs_reminder_test.go covers runReminderDetect (Task 10): the
// reminder_detect AI job handler that judges whether the owner's sent mail
// still awaits a reply, and arms Threads.SetReminderIfUnset when it does.
//
// Guard order under test: an inbound reply already on the thread, or a
// user-set RemindAt, must both short-circuit before any AI call (asserted
// via the fakeAI's lastSystem/lastUser staying empty and the usage fake
// staying uncharged) -- only a genuine "still waiting" case may reach
// completeJSONBudgeted.

import (
	"context"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// reminderTestDeps wires a minimal AIJobService for runReminderDetect,
// returning the fakes so each test can seed/assert against them directly.
func reminderTestDeps(now time.Time) (*AIJobService, *fakeThreadRepo, *fakeMessageRepo, *fakeAccountRepo, *fakeAI, *fakeAiUsageRepo, *fakeClock) {
	threads := newThreadRepo()
	messages := newMessageRepo()
	accounts := newAccountRepo()
	ai := newAI()
	usage := newAiUsageRepo()
	clock := newClock(now)

	svc := NewAIJobService(AIJobServiceDeps{
		Threads:    threads,
		Messages:   messages,
		Accounts:   accounts,
		AI:         ai,
		Usage:      usage,
		Clock:      clock,
		DailyLimit: 100,
	})
	return svc, threads, messages, accounts, ai, usage, clock
}

func TestRunReminderDetectReplyAlreadyArrived(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	sentAt := now.Add(-24 * time.Hour)

	svc, threads, messages, accounts, ai, usage, _ := reminderTestDeps(now)

	if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", Email: "owner@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", AccountID: "a1", Subject: "Proposal", Snippet: "Let me know"}); err != nil {
		t.Fatal(err)
	}
	// The owner's own sent message at sentAt (must be ignored as "inbound").
	if _, err := messages.Upsert(ctx, domain.Message{ID: "m1", ThreadID: "t1", From: domain.EmailAddress{Email: "owner@example.com"}, SentAt: sentAt}); err != nil {
		t.Fatal(err)
	}
	// A genuine inbound reply after sentAt: the recipient answered.
	if _, err := messages.Upsert(ctx, domain.Message{ID: "m2", ThreadID: "t1", From: domain.EmailAddress{Email: "recipient@example.com"}, SentAt: sentAt.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}

	j := domain.AiJob{ID: "j1", UserID: "u1", ThreadID: strPtr("t1"), Payload: map[string]string{"sentAt": sentAt.Format(time.RFC3339)}}
	if err := svc.runReminderDetect(ctx, j); err != nil {
		t.Fatalf("runReminderDetect() error = %v, want nil", err)
	}

	if ai.lastSystem != "" || ai.lastUser != "" {
		t.Fatalf("AI was called (lastSystem=%q lastUser=%q), want it skipped: reply already arrived", ai.lastSystem, ai.lastUser)
	}
	if len(usage.calls) != 0 {
		t.Fatalf("usage.calls = %v, want empty: no budget spent when reply already arrived", usage.calls)
	}
	got, err := threads.GetByID(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if got.RemindAt != nil {
		t.Fatalf("RemindAt = %v, want nil: no reminder needed once recipient replied", got.RemindAt)
	}
}

func TestRunReminderDetectUserReminderUntouched(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	sentAt := now.Add(-24 * time.Hour)
	userRemindAt := now.Add(72 * time.Hour)

	svc, threads, _, accounts, ai, usage, _ := reminderTestDeps(now)

	if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", Email: "owner@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", AccountID: "a1", RemindAt: &userRemindAt}); err != nil {
		t.Fatal(err)
	}

	j := domain.AiJob{ID: "j1", UserID: "u1", ThreadID: strPtr("t1"), Payload: map[string]string{"sentAt": sentAt.Format(time.RFC3339)}}
	if err := svc.runReminderDetect(ctx, j); err != nil {
		t.Fatalf("runReminderDetect() error = %v, want nil", err)
	}

	if ai.lastSystem != "" || ai.lastUser != "" {
		t.Fatalf("AI was called, want it skipped: user already set a reminder")
	}
	if len(usage.calls) != 0 {
		t.Fatalf("usage.calls = %v, want empty", usage.calls)
	}
	got, err := threads.GetByID(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if got.RemindAt == nil || !got.RemindAt.Equal(userRemindAt) {
		t.Fatalf("RemindAt = %v, want untouched %v", got.RemindAt, userRemindAt)
	}
}

func TestRunReminderDetectAwaitingSetsReminderClamped(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	sentAt := now.Add(-24 * time.Hour) // typical: job runs 24h after send

	svc, threads, _, accounts, ai, usage, _ := reminderTestDeps(now)

	if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", Email: "owner@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", AccountID: "a1", Subject: "Proposal", Snippet: "Thoughts?"}); err != nil {
		t.Fatal(err)
	}
	ai.jsonOut = `{"awaitingReply":true,"remindDays":20}` // out of [1,14] range
	ai.jsonModel = "test-model"

	j := domain.AiJob{ID: "j1", UserID: "u1", ThreadID: strPtr("t1"), Payload: map[string]string{"sentAt": sentAt.Format(time.RFC3339)}}
	if err := svc.runReminderDetect(ctx, j); err != nil {
		t.Fatalf("runReminderDetect() error = %v, want nil", err)
	}

	if ai.lastSystem != reminderSystem {
		t.Fatalf("lastSystem = %q, want reminderSystem", ai.lastSystem)
	}
	if usage.calls["u1"] != 1 {
		t.Fatalf("usage.calls[u1] = %d, want 1: AI call must spend budget", usage.calls["u1"])
	}
	got, err := threads.GetByID(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	want := sentAt.AddDate(0, 0, 3) // clamped default: 20 is out of [1,14]
	if got.RemindAt == nil || !got.RemindAt.Equal(want) {
		t.Fatalf("RemindAt = %v, want %v (clamped to default 3 days)", got.RemindAt, want)
	}
}

func TestRunReminderDetectNotAwaitingSetsNothing(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	sentAt := now.Add(-24 * time.Hour)

	svc, threads, _, accounts, ai, usage, _ := reminderTestDeps(now)

	if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", Email: "owner@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", AccountID: "a1", Subject: "FYI", Snippet: "Just letting you know"}); err != nil {
		t.Fatal(err)
	}
	ai.jsonOut = `{"awaitingReply":false,"remindDays":5}`

	j := domain.AiJob{ID: "j1", UserID: "u1", ThreadID: strPtr("t1"), Payload: map[string]string{"sentAt": sentAt.Format(time.RFC3339)}}
	if err := svc.runReminderDetect(ctx, j); err != nil {
		t.Fatalf("runReminderDetect() error = %v, want nil", err)
	}

	if usage.calls["u1"] != 1 {
		t.Fatalf("usage.calls[u1] = %d, want 1: the AI call itself still happens (only its verdict is 'no')", usage.calls["u1"])
	}
	got, err := threads.GetByID(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if got.RemindAt != nil {
		t.Fatalf("RemindAt = %v, want nil: model said no reminder needed", got.RemindAt)
	}
}

func TestRunReminderDetectPastTargetUsesNowPlus24h(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	sentAt := now.Add(-30 * 24 * time.Hour) // 30 days ago: a long-delayed job run

	svc, threads, _, accounts, ai, _, _ := reminderTestDeps(now)

	if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", Email: "owner@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", AccountID: "a1", Subject: "Proposal", Snippet: "Thoughts?"}); err != nil {
		t.Fatal(err)
	}
	ai.jsonOut = `{"awaitingReply":true,"remindDays":3}` // sentAt+3d is far in the past

	j := domain.AiJob{ID: "j1", UserID: "u1", ThreadID: strPtr("t1"), Payload: map[string]string{"sentAt": sentAt.Format(time.RFC3339)}}
	if err := svc.runReminderDetect(ctx, j); err != nil {
		t.Fatalf("runReminderDetect() error = %v, want nil", err)
	}

	got, err := threads.GetByID(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	want := now.Add(24 * time.Hour)
	if got.RemindAt == nil || !got.RemindAt.Equal(want) {
		t.Fatalf("RemindAt = %v, want now+24h = %v", got.RemindAt, want)
	}
}

func TestRunReminderDetectMissingThreadDrops(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, _, _, _ := reminderTestDeps(time.Now())

	j := domain.AiJob{ID: "j1", UserID: "u1", ThreadID: strPtr("does-not-exist"), Payload: map[string]string{"sentAt": time.Now().Format(time.RFC3339)}}
	if err := svc.runReminderDetect(ctx, j); err == nil {
		t.Fatal("runReminderDetect() error = nil, want domain.ErrNotFound (vanished thread)")
	} else if err != domain.ErrNotFound {
		t.Fatalf("err = %v, want domain.ErrNotFound", err)
	}
}

func TestRunReminderDetectInvalidSentAtPayloadErrors(t *testing.T) {
	ctx := context.Background()
	svc, threads, _, accounts, _, _, _ := reminderTestDeps(time.Now())

	if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", Email: "owner@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", AccountID: "a1"}); err != nil {
		t.Fatal(err)
	}

	j := domain.AiJob{ID: "j1", UserID: "u1", ThreadID: strPtr("t1"), Payload: map[string]string{"sentAt": "not-a-timestamp"}}
	if err := svc.runReminderDetect(ctx, j); err == nil {
		t.Fatal("runReminderDetect() error = nil, want a validation error for the malformed payload")
	}
}
