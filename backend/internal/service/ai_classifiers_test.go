package service

// ai_classifiers_test.go covers AIService's classifier CRUD (Task 9):
// ownership, validation, and the 20-per-user cap.

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

func newClassifierAIService(classifiers port.ClassifierRepo) *AIService {
	subs := newSubscriptionRepo()
	for _, userID := range []string{"u1", "u2"} {
		_ = subs.Upsert(context.Background(), domain.Subscription{UserID: userID, Status: domain.SubscriptionActive})
	}
	return NewAIService(AIServiceDeps{
		Subscriptions: subs,
		Classifiers:   classifiers,
		Clock:         newClock(time.Now()),
	})
}

func TestCreateClassifierValidation(t *testing.T) {
	tests := []struct {
		name string
		in   port.ClassifierInput
	}{
		{"empty name", port.ClassifierInput{Name: "  ", Prompt: "p", LabelName: "L"}},
		{"empty prompt", port.ClassifierInput{Name: "N", Prompt: "  ", LabelName: "L"}},
		{"neither split nor label", port.ClassifierInput{Name: "N", Prompt: "p"}},
		{"invalid target split", port.ClassifierInput{Name: "N", Prompt: "p", TargetSplit: "bogus"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newClassifierAIService(newClassifierRepo())
			_, err := svc.CreateClassifier(context.Background(), "u1", tt.in)
			if !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("err = %v, want ErrValidation", err)
			}
		})
	}
}

func TestCreateClassifierSucceedsWithSplitOnly(t *testing.T) {
	svc := newClassifierAIService(newClassifierRepo())
	c, err := svc.CreateClassifier(context.Background(), "u1", port.ClassifierInput{
		Name: "Invoices", Prompt: "invoice emails", TargetSplit: domain.SplitImportant, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateClassifier() error = %v, want nil", err)
	}
	if c.ID == "" {
		t.Fatal("ID is empty, want generated")
	}
	if c.Name != "Invoices" || c.Prompt != "invoice emails" || c.TargetSplit != domain.SplitImportant {
		t.Fatalf("classifier = %+v, unexpected", c)
	}
}

func TestCreateClassifierSucceedsWithLabelOnly(t *testing.T) {
	svc := newClassifierAIService(newClassifierRepo())
	c, err := svc.CreateClassifier(context.Background(), "u1", port.ClassifierInput{
		Name: "Receipts", Prompt: "receipt emails", LabelName: "Receipts", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateClassifier() error = %v, want nil", err)
	}
	if c.LabelName != "Receipts" {
		t.Fatalf("LabelName = %q, want Receipts", c.LabelName)
	}
}

func TestCreateClassifierEnforces20Cap(t *testing.T) {
	repo := newClassifierRepo()
	svc := newClassifierAIService(repo)
	ctx := context.Background()
	for i := 0; i < maxClassifiersPerUser; i++ {
		if _, err := svc.CreateClassifier(ctx, "u1", port.ClassifierInput{
			Name: "rule", Prompt: "p", LabelName: "L",
		}); err != nil {
			t.Fatalf("CreateClassifier() #%d error = %v, want nil", i, err)
		}
	}
	_, err := svc.CreateClassifier(ctx, "u1", port.ClassifierInput{Name: "one too many", Prompt: "p", LabelName: "L"})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation (cap reached)", err)
	}
	// A different user is unaffected by u1's cap.
	if _, err := svc.CreateClassifier(ctx, "u2", port.ClassifierInput{Name: "n", Prompt: "p", LabelName: "L"}); err != nil {
		t.Fatalf("other user's CreateClassifier() error = %v, want nil", err)
	}
}

func TestListClassifiersScopedByUser(t *testing.T) {
	repo := newClassifierRepo()
	ctx := context.Background()
	if _, err := repo.Create(ctx, domain.AiClassifier{UserID: "u1", Name: "a", Prompt: "p", LabelName: "L"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(ctx, domain.AiClassifier{UserID: "u2", Name: "b", Prompt: "p", LabelName: "L"}); err != nil {
		t.Fatal(err)
	}
	svc := newClassifierAIService(repo)
	cs, err := svc.ListClassifiers(ctx, "u1")
	if err != nil {
		t.Fatalf("ListClassifiers() error = %v, want nil", err)
	}
	if len(cs) != 1 || cs[0].Name != "a" {
		t.Fatalf("classifiers = %+v, want [a]", cs)
	}
}

func TestUpdateClassifierOwnershipEnforced(t *testing.T) {
	repo := newClassifierRepo()
	ctx := context.Background()
	c, err := repo.Create(ctx, domain.AiClassifier{UserID: "u1", Name: "a", Prompt: "p", LabelName: "L"})
	if err != nil {
		t.Fatal(err)
	}
	svc := newClassifierAIService(repo)
	_, err = svc.UpdateClassifier(ctx, "u2" /* foreign */, c.ID, port.ClassifierInput{Name: "b", Prompt: "p2", LabelName: "L2"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound (foreign classifier hidden as 404)", err)
	}
}

func TestUpdateClassifierUnknownID(t *testing.T) {
	svc := newClassifierAIService(newClassifierRepo())
	_, err := svc.UpdateClassifier(context.Background(), "u1", "does-not-exist", port.ClassifierInput{Name: "b", Prompt: "p", LabelName: "L"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestUpdateClassifierAppliesChanges(t *testing.T) {
	repo := newClassifierRepo()
	ctx := context.Background()
	c, err := repo.Create(ctx, domain.AiClassifier{UserID: "u1", Name: "a", Prompt: "p", LabelName: "L", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	svc := newClassifierAIService(repo)
	updated, err := svc.UpdateClassifier(ctx, "u1", c.ID, port.ClassifierInput{
		Name: "renamed", Prompt: "new prompt", TargetSplit: domain.SplitVIP, Enabled: true,
	})
	if err != nil {
		t.Fatalf("UpdateClassifier() error = %v, want nil", err)
	}
	if updated.ID != c.ID {
		t.Fatalf("ID changed: got %q, want %q", updated.ID, c.ID)
	}
	if updated.Name != "renamed" || updated.Prompt != "new prompt" || updated.TargetSplit != domain.SplitVIP || !updated.Enabled {
		t.Fatalf("updated = %+v, unexpected", updated)
	}
	if updated.LabelName != "" {
		t.Fatalf("LabelName = %q, want cleared (full-replace update didn't set it)", updated.LabelName)
	}
}

func TestUpdateClassifierValidatesInput(t *testing.T) {
	repo := newClassifierRepo()
	ctx := context.Background()
	c, err := repo.Create(ctx, domain.AiClassifier{UserID: "u1", Name: "a", Prompt: "p", LabelName: "L"})
	if err != nil {
		t.Fatal(err)
	}
	svc := newClassifierAIService(repo)
	_, err = svc.UpdateClassifier(ctx, "u1", c.ID, port.ClassifierInput{Name: "", Prompt: "p", LabelName: "L"})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}

func TestDeleteClassifierOwnershipEnforced(t *testing.T) {
	repo := newClassifierRepo()
	ctx := context.Background()
	c, err := repo.Create(ctx, domain.AiClassifier{UserID: "u1", Name: "a", Prompt: "p", LabelName: "L"})
	if err != nil {
		t.Fatal(err)
	}
	svc := newClassifierAIService(repo)
	if err := svc.DeleteClassifier(ctx, "u2", c.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound (foreign classifier hidden as 404)", err)
	}
	// Owner can still delete it afterwards.
	if err := svc.DeleteClassifier(ctx, "u1", c.ID); err != nil {
		t.Fatalf("DeleteClassifier() error = %v, want nil", err)
	}
	if _, err := repo.GetByID(ctx, c.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("classifier still exists after delete")
	}
}

func TestDeleteClassifierUnknownID(t *testing.T) {
	svc := newClassifierAIService(newClassifierRepo())
	err := svc.DeleteClassifier(context.Background(), "u1", "does-not-exist")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestClassifierCRUDRequiresEntitlement(t *testing.T) {
	// No subscription seeded for u1 => entitlement.require fails => 402.
	svc := NewAIService(AIServiceDeps{
		Subscriptions: newSubscriptionRepo(),
		Classifiers:   newClassifierRepo(),
		Clock:         newClock(time.Now()),
	})
	ctx := context.Background()
	if _, err := svc.ListClassifiers(ctx, "u1"); !errors.Is(err, domain.ErrPaymentRequired) {
		t.Fatalf("ListClassifiers() err = %v, want ErrPaymentRequired", err)
	}
	if _, err := svc.CreateClassifier(ctx, "u1", port.ClassifierInput{Name: "n", Prompt: "p", LabelName: "L"}); !errors.Is(err, domain.ErrPaymentRequired) {
		t.Fatalf("CreateClassifier() err = %v, want ErrPaymentRequired", err)
	}
}
