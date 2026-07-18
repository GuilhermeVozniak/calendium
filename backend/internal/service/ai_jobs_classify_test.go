package service

// ai_jobs_classify_test.go covers AIJobService.runClassify (Task 9): the
// real classify job handler that evaluates a user's custom natural-language
// classifiers against a thread's newest message and routes split/labels on a
// genuine match.

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func TestRunClassifyNoEnabledClassifiersCompletesWithoutAICall(t *testing.T) {
	ctx := context.Background()
	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", AccountID: "a1"}); err != nil {
		t.Fatal(err)
	}
	ai := newAI()
	usage := newAiUsageRepo()

	svc := NewAIJobService(AIJobServiceDeps{
		Threads:     threads,
		Classifiers: newClassifierRepo(), // empty: no enabled classifiers
		Usage:       usage,
		AI:          ai,
		Clock:       newClock(time.Now()),
		DailyLimit:  10,
	})

	job := domain.AiJob{ID: "j1", UserID: "u1", ThreadID: strPtr("t1"), Kind: domain.AiJobClassify}
	if err := svc.runClassify(ctx, job); err != nil {
		t.Fatalf("runClassify() error = %v, want nil", err)
	}
	if ai.lastSystem != "" || ai.lastUser != "" {
		t.Fatalf("AI was called (system=%q user=%q), want no call when no enabled classifiers", ai.lastSystem, ai.lastUser)
	}
	if len(usage.calls) != 0 {
		t.Fatalf("usage.calls = %v, want empty (no budget spent)", usage.calls)
	}
}

func TestRunClassifyMatchRoutesSplitAndAttachesLabel(t *testing.T) {
	ctx := context.Background()
	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", AccountID: "a1", Split: domain.SplitOther}); err != nil {
		t.Fatal(err)
	}
	messages := newMessageRepo()
	if _, err := messages.Upsert(ctx, domain.Message{
		ID: "m1", ThreadID: "t1",
		From: domain.EmailAddress{Email: "sender@example.com"}, BodyText: "Please approve invoice #42",
		SentAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	classifiers := newClassifierRepo()
	c1, err := classifiers.Create(ctx, domain.AiClassifier{
		UserID: "u1", Name: "Invoices", Prompt: "invoice emails",
		TargetSplit: domain.SplitImportant, LabelName: "Invoices", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	labels := newLabelRepo()
	ai := newAI()
	ai.jsonOut = `{"matchedClassifierIds":["` + c1.ID + `"]}`
	ai.jsonModel = "test-model"
	usage := newAiUsageRepo()

	svc := NewAIJobService(AIJobServiceDeps{
		Threads:     threads,
		Messages:    messages,
		Classifiers: classifiers,
		Labels:      labels,
		Usage:       usage,
		AI:          ai,
		Clock:       newClock(time.Now()),
		DailyLimit:  10,
	})

	job := domain.AiJob{ID: "j1", UserID: "u1", ThreadID: strPtr("t1"), Kind: domain.AiJobClassify}
	if err := svc.runClassify(ctx, job); err != nil {
		t.Fatalf("runClassify() error = %v, want nil", err)
	}

	updated, err := threads.GetByID(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Split != domain.SplitImportant {
		t.Fatalf("Split = %q, want important", updated.Split)
	}
	if len(updated.LabelIDs) != 1 {
		t.Fatalf("LabelIDs = %v, want 1 label", updated.LabelIDs)
	}
	lbl, err := labels.GetByID(ctx, updated.LabelIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if lbl.Name != "Invoices" || lbl.ProviderLabelID != "calendium-ai:"+c1.ID || lbl.AccountID != "a1" {
		t.Fatalf("label = %+v, want Invoices/calendium-ai:%s on account a1", lbl, c1.ID)
	}
	if usage.calls["u1"] != 1 {
		t.Fatalf("usage.calls[u1] = %d, want 1", usage.calls["u1"])
	}
	if ai.lastSystem == "" {
		t.Fatal("AI system prompt was empty, want the built classify prompt")
	}
}

func TestRunClassifyNoMatchLeavesThreadUntouched(t *testing.T) {
	ctx := context.Background()
	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", AccountID: "a1", Split: domain.SplitOther}); err != nil {
		t.Fatal(err)
	}
	messages := newMessageRepo()
	if _, err := messages.Upsert(ctx, domain.Message{ID: "m1", ThreadID: "t1", SentAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	classifiers := newClassifierRepo()
	if _, err := classifiers.Create(ctx, domain.AiClassifier{
		UserID: "u1", Name: "Invoices", Prompt: "invoice emails", TargetSplit: domain.SplitImportant, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	labels := newLabelRepo()
	ai := newAI()
	ai.jsonOut = `{"matchedClassifierIds":[]}`

	svc := NewAIJobService(AIJobServiceDeps{
		Threads:     threads,
		Messages:    messages,
		Classifiers: classifiers,
		Labels:      labels,
		Usage:       newAiUsageRepo(),
		AI:          ai,
		Clock:       newClock(time.Now()),
		DailyLimit:  10,
	})

	job := domain.AiJob{ID: "j1", UserID: "u1", ThreadID: strPtr("t1"), Kind: domain.AiJobClassify}
	if err := svc.runClassify(ctx, job); err != nil {
		t.Fatalf("runClassify() error = %v, want nil", err)
	}
	updated, err := threads.GetByID(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Split != domain.SplitOther {
		t.Fatalf("Split = %q, want unchanged (other)", updated.Split)
	}
	if len(updated.LabelIDs) != 0 {
		t.Fatalf("LabelIDs = %v, want none", updated.LabelIDs)
	}
}

func TestRunClassifyDropsHallucinatedIDs(t *testing.T) {
	ctx := context.Background()
	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", AccountID: "a1", Split: domain.SplitOther}); err != nil {
		t.Fatal(err)
	}
	messages := newMessageRepo()
	if _, err := messages.Upsert(ctx, domain.Message{ID: "m1", ThreadID: "t1", SentAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	classifiers := newClassifierRepo()
	if _, err := classifiers.Create(ctx, domain.AiClassifier{
		ID: "c-real", UserID: "u1", Name: "Real", Prompt: "real rule", TargetSplit: domain.SplitImportant, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	ai := newAI()
	ai.jsonOut = `{"matchedClassifierIds":["c-fake-does-not-exist"]}`

	svc := NewAIJobService(AIJobServiceDeps{
		Threads:     threads,
		Messages:    messages,
		Classifiers: classifiers,
		Labels:      newLabelRepo(),
		Usage:       newAiUsageRepo(),
		AI:          ai,
		Clock:       newClock(time.Now()),
		DailyLimit:  10,
	})

	job := domain.AiJob{ID: "j1", UserID: "u1", ThreadID: strPtr("t1"), Kind: domain.AiJobClassify}
	if err := svc.runClassify(ctx, job); err != nil {
		t.Fatalf("runClassify() error = %v, want nil", err)
	}
	updated, err := threads.GetByID(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Split != domain.SplitOther {
		t.Fatalf("Split = %q, want unchanged (hallucinated id must be dropped, not applied)", updated.Split)
	}
	if len(updated.LabelIDs) != 0 {
		t.Fatalf("LabelIDs = %v, want none", updated.LabelIDs)
	}
}

func TestRunClassifyThreadNotFoundDrops(t *testing.T) {
	ctx := context.Background()
	svc := NewAIJobService(AIJobServiceDeps{
		Threads: newThreadRepo(), // empty
		Usage:   newAiUsageRepo(),
		AI:      newAI(),
		Clock:   newClock(time.Now()),
	})
	job := domain.AiJob{ID: "j1", UserID: "u1", ThreadID: strPtr("missing"), Kind: domain.AiJobClassify}
	err := svc.runClassify(ctx, job)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRunClassifyLabelOnlyClassifierDoesNotChangeSplit(t *testing.T) {
	ctx := context.Background()
	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", AccountID: "a1", Split: domain.SplitOther}); err != nil {
		t.Fatal(err)
	}
	messages := newMessageRepo()
	if _, err := messages.Upsert(ctx, domain.Message{ID: "m1", ThreadID: "t1", SentAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	classifiers := newClassifierRepo()
	c1, err := classifiers.Create(ctx, domain.AiClassifier{
		UserID: "u1", Name: "Receipts", Prompt: "receipt emails", LabelName: "Receipts", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	labels := newLabelRepo()
	ai := newAI()
	ai.jsonOut = `{"matchedClassifierIds":["` + c1.ID + `"]}`

	svc := NewAIJobService(AIJobServiceDeps{
		Threads:     threads,
		Messages:    messages,
		Classifiers: classifiers,
		Labels:      labels,
		Usage:       newAiUsageRepo(),
		AI:          ai,
		Clock:       newClock(time.Now()),
		DailyLimit:  10,
	})

	job := domain.AiJob{ID: "j1", UserID: "u1", ThreadID: strPtr("t1"), Kind: domain.AiJobClassify}
	if err := svc.runClassify(ctx, job); err != nil {
		t.Fatalf("runClassify() error = %v, want nil", err)
	}
	updated, err := threads.GetByID(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Split != domain.SplitOther {
		t.Fatalf("Split = %q, want unchanged (classifier had no TargetSplit)", updated.Split)
	}
	if len(updated.LabelIDs) != 1 {
		t.Fatalf("LabelIDs = %v, want 1 label", updated.LabelIDs)
	}
}

func TestRunClassifyNoMessagesCompletesWithoutAICall(t *testing.T) {
	ctx := context.Background()
	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", AccountID: "a1"}); err != nil {
		t.Fatal(err)
	}
	classifiers := newClassifierRepo()
	if _, err := classifiers.Create(ctx, domain.AiClassifier{
		UserID: "u1", Name: "Invoices", Prompt: "invoice emails", TargetSplit: domain.SplitImportant, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	ai := newAI()

	svc := NewAIJobService(AIJobServiceDeps{
		Threads:     threads,
		Messages:    newMessageRepo(), // empty
		Classifiers: classifiers,
		Usage:       newAiUsageRepo(),
		AI:          ai,
		Clock:       newClock(time.Now()),
		DailyLimit:  10,
	})

	job := domain.AiJob{ID: "j1", UserID: "u1", ThreadID: strPtr("t1"), Kind: domain.AiJobClassify}
	if err := svc.runClassify(ctx, job); err != nil {
		t.Fatalf("runClassify() error = %v, want nil", err)
	}
	if ai.lastSystem != "" {
		t.Fatal("AI was called, want no call when thread has no messages")
	}
}
