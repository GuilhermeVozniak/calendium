package postgres

import (
	"context"
	"errors"
	"testing"

	"calendium/backend/internal/domain"
)

// --- SnippetRepo ---------------------------------------------------------

func TestSnippetRepoCreateAndGetByID(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	created, err := st.Snippets().Create(ctx, domain.Snippet{
		UserID: "u1", Name: "Greeting", BodyHTML: "<p>hi</p>",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := st.Snippets().GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "Greeting" {
		t.Fatalf("Name = %q", got.Name)
	}
	if got.Shortcut != nil {
		t.Fatalf("Shortcut = %v, want nil when unset", got.Shortcut)
	}
}

func TestSnippetRepoListByUser(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	if _, err := st.Snippets().Create(ctx, domain.Snippet{UserID: "u1", Name: "Zeta"}); err != nil {
		t.Fatalf("Create Zeta: %v", err)
	}
	if _, err := st.Snippets().Create(ctx, domain.Snippet{UserID: "u1", Name: "Alpha"}); err != nil {
		t.Fatalf("Create Alpha: %v", err)
	}

	list, err := st.Snippets().ListByUser(ctx, "u1")
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(list) != 2 || list[0].Name != "Alpha" || list[1].Name != "Zeta" {
		t.Fatalf("ListByUser = %+v, want [Alpha, Zeta]", list)
	}
}

func TestSnippetRepoUpdate(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	created, err := st.Snippets().Create(ctx, domain.Snippet{UserID: "u1", Name: "orig"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	shortcut := ";sig"
	created.Name = "updated"
	created.Shortcut = &shortcut
	created.BodyHTML = "<p>new body</p>"
	created.UsageCount = 5
	if err := st.Snippets().Update(ctx, created); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := st.Snippets().GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "updated" || got.Shortcut == nil || *got.Shortcut != ";sig" ||
		got.BodyHTML != "<p>new body</p>" || got.UsageCount != 5 {
		t.Fatalf("got = %+v", got)
	}

	if err := st.Snippets().Update(ctx, domain.Snippet{ID: "nope", UserID: "u1"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Update unknown: err = %v, want ErrNotFound", err)
	}
}

func TestSnippetRepoDelete(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	created, err := st.Snippets().Create(ctx, domain.Snippet{UserID: "u1", Name: "to delete"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := st.Snippets().Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := st.Snippets().GetByID(ctx, created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByID after delete: err = %v, want ErrNotFound", err)
	}
	if err := st.Snippets().Delete(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Delete unknown: err = %v, want ErrNotFound", err)
	}
}

// --- LabelRepo -------------------------------------------------------------

func TestLabelRepoUpsertNew(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")

	l, err := st.Labels().Upsert(ctx, domain.Label{
		AccountID: acct.ID, ProviderLabelID: newID(), Name: "Inbox",
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if l.ID == "" {
		t.Fatal("Upsert did not assign an id")
	}
	if l.Kind != domain.LabelKindUser {
		t.Fatalf("Kind = %q, want default user", l.Kind)
	}

	list, err := st.Labels().ListByAccount(ctx, acct.ID)
	if err != nil {
		t.Fatalf("ListByAccount: %v", err)
	}
	if len(list) != 1 || list[0].ID != l.ID {
		t.Fatalf("ListByAccount = %+v", list)
	}
}

func TestLabelRepoUpsertConflictUpdatesFields(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")

	pid := newID()
	first, err := st.Labels().Upsert(ctx, domain.Label{
		AccountID: acct.ID, ProviderLabelID: pid, Name: "Old Name",
	})
	if err != nil {
		t.Fatalf("Upsert first: %v", err)
	}

	color := "#ff0000"
	second, err := st.Labels().Upsert(ctx, domain.Label{
		AccountID: acct.ID, ProviderLabelID: pid, Name: "New Name",
		Color: &color, Kind: domain.LabelKindSystem,
	})
	if err != nil {
		t.Fatalf("Upsert second: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("Upsert on conflict changed id: %s -> %s", first.ID, second.ID)
	}
	if second.Name != "New Name" || second.Kind != domain.LabelKindSystem ||
		second.Color == nil || *second.Color != "#ff0000" {
		t.Fatalf("second = %+v", second)
	}

	list, err := st.Labels().ListByAccount(ctx, acct.ID)
	if err != nil {
		t.Fatalf("ListByAccount: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListByAccount = %+v, want exactly one row after conflict update", list)
	}
}

func TestLabelRepoListByAccountOrdered(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")

	if _, err := st.Labels().Upsert(ctx, domain.Label{AccountID: acct.ID, ProviderLabelID: newID(), Name: "Zeta"}); err != nil {
		t.Fatalf("Upsert Zeta: %v", err)
	}
	if _, err := st.Labels().Upsert(ctx, domain.Label{AccountID: acct.ID, ProviderLabelID: newID(), Name: "Alpha"}); err != nil {
		t.Fatalf("Upsert Alpha: %v", err)
	}

	list, err := st.Labels().ListByAccount(ctx, acct.ID)
	if err != nil {
		t.Fatalf("ListByAccount: %v", err)
	}
	if len(list) != 2 || list[0].Name != "Alpha" || list[1].Name != "Zeta" {
		t.Fatalf("ListByAccount = %+v, want [Alpha, Zeta]", list)
	}
}

func TestLabelListByUserAndGetByID(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	acct1 := seedAccount(t, st, "u1")
	acct2 := seedAccount(t, st, "u2")

	l1, err := st.Labels().Upsert(ctx, domain.Label{AccountID: acct1.ID, ProviderLabelID: newID(), Name: "Zeta"})
	if err != nil {
		t.Fatalf("Upsert l1: %v", err)
	}
	if _, err := st.Labels().Upsert(ctx, domain.Label{AccountID: acct1.ID, ProviderLabelID: newID(), Name: "Alpha"}); err != nil {
		t.Fatalf("Upsert l2: %v", err)
	}
	other, err := st.Labels().Upsert(ctx, domain.Label{AccountID: acct2.ID, ProviderLabelID: newID(), Name: "Other"})
	if err != nil {
		t.Fatalf("Upsert other: %v", err)
	}

	list, err := st.Labels().ListByUser(ctx, "u1")
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(list) != 2 || list[0].Name != "Alpha" || list[1].Name != "Zeta" {
		t.Fatalf("ListByUser(u1) = %+v, want [Alpha, Zeta], scoped away from u2", list)
	}

	got, err := st.Labels().GetByID(ctx, l1.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != l1.ID || got.Name != "Zeta" {
		t.Fatalf("GetByID = %+v", got)
	}

	if _, err := st.Labels().GetByID(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByID unknown: err = %v, want ErrNotFound", err)
	}

	// sanity: other's label exists but is outside u1's ListByUser scope
	if other.AccountID != acct2.ID {
		t.Fatalf("other.AccountID = %q, want %q", other.AccountID, acct2.ID)
	}
}
