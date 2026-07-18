package postgres

import (
	"context"
	"errors"
	"testing"

	"calendium/backend/internal/domain"
)

func TestClassifierRepoCRUD(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	created, err := st.Classifiers().Create(ctx, domain.AiClassifier{
		UserID: "u1", Name: "Receipts", Prompt: "purchase receipts and invoices",
		TargetSplit: domain.SplitOther, LabelName: "Receipts", Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("Create did not assign an id")
	}

	got, err := st.Classifiers().GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "Receipts" || got.Prompt != "purchase receipts and invoices" || !got.Enabled {
		t.Fatalf("GetByID = %+v", got)
	}

	got.Name = "Purchases"
	got.Enabled = false
	if err := st.Classifiers().Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	updated, err := st.Classifiers().GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if updated.Name != "Purchases" || updated.Enabled {
		t.Fatalf("updated = %+v", updated)
	}

	if err := st.Classifiers().Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := st.Classifiers().GetByID(ctx, created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByID after delete: err = %v, want ErrNotFound", err)
	}
	if err := st.Classifiers().Update(ctx, domain.AiClassifier{ID: "nope"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Update unknown: err = %v, want ErrNotFound", err)
	}
	if err := st.Classifiers().Delete(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Delete unknown: err = %v, want ErrNotFound", err)
	}
}

func TestClassifierRepoListByUserAndEnabledFilter(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")

	enabled, err := st.Classifiers().Create(ctx, domain.AiClassifier{
		UserID: "u1", Name: "Enabled one", Prompt: "p1", Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create enabled: %v", err)
	}
	if _, err := st.Classifiers().Create(ctx, domain.AiClassifier{
		UserID: "u1", Name: "Disabled one", Prompt: "p2", Enabled: false,
	}); err != nil {
		t.Fatalf("Create disabled: %v", err)
	}
	if _, err := st.Classifiers().Create(ctx, domain.AiClassifier{
		UserID: "u2", Name: "Someone else's", Prompt: "p3", Enabled: true,
	}); err != nil {
		t.Fatalf("Create other user: %v", err)
	}

	all, err := st.Classifiers().ListByUser(ctx, "u1")
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListByUser = %d classifiers, want 2", len(all))
	}

	onlyEnabled, err := st.Classifiers().ListEnabledByUser(ctx, "u1")
	if err != nil {
		t.Fatalf("ListEnabledByUser: %v", err)
	}
	if len(onlyEnabled) != 1 || onlyEnabled[0].ID != enabled.ID {
		t.Fatalf("ListEnabledByUser = %+v, want only %s", onlyEnabled, enabled.ID)
	}
}
