package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func seedDelegation(t *testing.T, st *Store, principalID, assistantID string, status domain.DelegationStatus, scopes ...domain.DelegationScope) domain.Delegation {
	t.Helper()
	d, err := NewDelegationRepo(st).Create(context.Background(), domain.Delegation{
		PrincipalID: principalID,
		AssistantID: assistantID,
		Scopes:      scopes,
		Status:      status,
	})
	if err != nil {
		t.Fatalf("seed delegation: %v", err)
	}
	return d
}

func TestDelegationRepoCRUD(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	repo := NewDelegationRepo(st)
	seedUser(t, st, "principal_1")
	seedUser(t, st, "assistant_1")

	d := seedDelegation(t, st, "principal_1", "assistant_1", domain.DelegationPending,
		domain.ScopeMailRead, domain.ScopeCalendarWrite)

	got, err := repo.GetByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.PrincipalID != "principal_1" || got.AssistantID != "assistant_1" {
		t.Fatalf("parties = %q→%q", got.PrincipalID, got.AssistantID)
	}
	if got.Status != domain.DelegationPending || got.AcceptedAt != nil || got.RevokedAt != nil {
		t.Fatalf("lifecycle = %+v, want pending with nil timestamps", got)
	}
	if len(got.Scopes) != 2 || !got.HasScope(domain.ScopeMailRead) || !got.HasScope(domain.ScopeCalendarWrite) {
		t.Fatalf("scopes = %v", got.Scopes)
	}

	if _, err := repo.GetByID(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing id: want ErrNotFound, got %v", err)
	}

	// One live grant per (principal, assistant): a duplicate conflicts.
	_, err = repo.Create(ctx, domain.Delegation{
		PrincipalID: "principal_1", AssistantID: "assistant_1",
		Scopes: []domain.DelegationScope{domain.ScopeMailRead},
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate grant: want ErrConflict, got %v", err)
	}

	// Update to active persists timestamps.
	now := time.Now().UTC().Truncate(time.Microsecond)
	d.Status = domain.DelegationActive
	d.AcceptedAt = &now
	if err := repo.Update(ctx, d); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err = repo.GetByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Status != domain.DelegationActive || got.AcceptedAt == nil || !got.AcceptedAt.Equal(now) {
		t.Fatalf("after update = %+v", got)
	}

	if err := repo.Update(ctx, domain.Delegation{ID: "missing", Status: domain.DelegationActive}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update missing: want ErrNotFound, got %v", err)
	}
}

func TestDelegationRepoGetActiveTracksStatus(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	repo := NewDelegationRepo(st)
	seedUser(t, st, "principal_1")
	seedUser(t, st, "assistant_1")

	d := seedDelegation(t, st, "principal_1", "assistant_1", domain.DelegationPending, domain.ScopeMailRead)

	// Pending grants never authorize.
	if _, err := repo.GetActive(ctx, "principal_1", "assistant_1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("pending: want ErrNotFound, got %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	d.Status = domain.DelegationActive
	d.AcceptedAt = &now
	if err := repo.Update(ctx, d); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if _, err := repo.GetActive(ctx, "principal_1", "assistant_1"); err != nil {
		t.Fatalf("active: %v", err)
	}
	// Direction matters: the assistant is not the principal.
	if _, err := repo.GetActive(ctx, "assistant_1", "principal_1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("reversed direction: want ErrNotFound, got %v", err)
	}

	// Revocation is visible to the very next read — nothing is cached.
	d.Status = domain.DelegationRevoked
	d.RevokedAt = &now
	if err := repo.Update(ctx, d); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := repo.GetActive(ctx, "principal_1", "assistant_1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revoked: want ErrNotFound, got %v", err)
	}
}

// TestDelegationRepoRegrantAfterRevoke proves revocation is not permanent:
// the unique index is partial (live rows only), so once a grant is revoked
// the same (principal, assistant) pair can be granted again.
func TestDelegationRepoRegrantAfterRevoke(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	repo := NewDelegationRepo(st)
	seedUser(t, st, "principal_1")
	seedUser(t, st, "assistant_1")

	d := seedDelegation(t, st, "principal_1", "assistant_1", domain.DelegationPending, domain.ScopeMailRead)
	now := time.Now().UTC().Truncate(time.Microsecond)
	d.Status = domain.DelegationRevoked
	d.RevokedAt = &now
	if err := repo.Update(ctx, d); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	// Re-grant succeeds and the revoked row stays behind as history.
	redo := seedDelegation(t, st, "principal_1", "assistant_1", domain.DelegationPending, domain.ScopeMailRead)
	if redo.ID == d.ID {
		t.Fatal("re-grant reused the revoked row's id")
	}
	redo.Status = domain.DelegationActive
	redo.AcceptedAt = &now
	if err := repo.Update(ctx, redo); err != nil {
		t.Fatalf("activate re-grant: %v", err)
	}
	got, err := repo.GetActive(ctx, "principal_1", "assistant_1")
	if err != nil {
		t.Fatalf("active re-grant not visible: %v", err)
	}
	if got.ID != redo.ID {
		t.Fatalf("GetActive = %q, want the re-granted row %q", got.ID, redo.ID)
	}

	// A second live grant still conflicts.
	if _, err := repo.Create(ctx, domain.Delegation{
		PrincipalID: "principal_1", AssistantID: "assistant_1",
		Scopes: []domain.DelegationScope{domain.ScopeMailRead},
	}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second live grant: want ErrConflict, got %v", err)
	}
}

func TestDelegationRepoListByUser(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	repo := NewDelegationRepo(st)
	seedUser(t, st, "principal_1")
	seedUser(t, st, "assistant_1")
	seedUser(t, st, "bystander")

	seedDelegation(t, st, "principal_1", "assistant_1", domain.DelegationPending, domain.ScopeMailRead)
	seedDelegation(t, st, "assistant_1", "principal_1", domain.DelegationPending, domain.ScopeCalendarRead)

	for _, userID := range []string{"principal_1", "assistant_1"} {
		got, err := repo.ListByUser(ctx, userID)
		if err != nil {
			t.Fatalf("list %s: %v", userID, err)
		}
		if len(got) != 2 {
			t.Fatalf("list %s = %d grants, want 2", userID, len(got))
		}
	}
	got, err := repo.ListByUser(ctx, "bystander")
	if err != nil {
		t.Fatalf("list bystander: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("bystander sees %d grants, want 0", len(got))
	}
}

func TestAuditRepoDelegationAppendAndList(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	repo := NewAuditRepo(st)
	seedUser(t, st, "principal_1")
	seedUser(t, st, "assistant_1")

	base := time.Now().UTC().Truncate(time.Microsecond)
	for i := 0; i < 3; i++ {
		err := repo.Record(ctx, domain.AuditEntry{
			PrincipalID:  "principal_1",
			ActorID:      "assistant_1",
			Action:       "POST /v1/mail/threads/{id}/actions",
			ResourceType: "mail",
			ResourceID:   "t1",
			Metadata:     map[string]any{"route": "/v1/mail/threads/{id}/actions"},
			CreatedAt:    base.Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	// A different principal's entry stays out of the listing.
	if err := repo.Record(ctx, domain.AuditEntry{
		PrincipalID: "assistant_1", ActorID: "principal_1", Action: "POST /v1/mail/drafts",
		ResourceType: "mail",
	}); err != nil {
		t.Fatalf("append foreign: %v", err)
	}

	got, err := repo.ListByPrincipal(ctx, "principal_1", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("entries = %d, want 3", len(got))
	}
	// Newest first.
	if !got[0].CreatedAt.After(got[2].CreatedAt) {
		t.Fatalf("order: got[0]=%v got[2]=%v, want newest first", got[0].CreatedAt, got[2].CreatedAt)
	}
	if got[0].ActorID != "assistant_1" || got[0].PrincipalID != "principal_1" ||
		got[0].ResourceType != "mail" || got[0].ResourceID != "t1" {
		t.Fatalf("attribution = %+v", got[0])
	}

	limited, err := repo.ListByPrincipal(ctx, "principal_1", 2)
	if err != nil {
		t.Fatalf("list limited: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("limited = %d, want 2", len(limited))
	}
}

func TestUserDirectoryGetByEmail(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	dir := NewUserDirectory(st)
	seedUser(t, st, "assistant_1") // email assistant_1@example.com

	u, err := dir.GetByEmail(ctx, "ASSISTANT_1@Example.COM")
	if err != nil {
		t.Fatalf("get by email: %v", err)
	}
	if u.ID != "assistant_1" {
		t.Fatalf("user = %q, want assistant_1", u.ID)
	}
	if _, err := dir.GetByEmail(ctx, "nobody@example.com"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown email: want ErrNotFound, got %v", err)
	}
}
