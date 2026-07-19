package service

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- local fakes (deleg-prefixed to avoid clashing with fakes_test.go) ------

type delegClock struct{ t time.Time }

func (c delegClock) Now() time.Time { return c.t }

type delegRepoFake struct {
	byID    map[string]domain.Delegation
	nextID  int
	created int
}

func newDelegRepoFake() *delegRepoFake {
	return &delegRepoFake{byID: map[string]domain.Delegation{}}
}

func (f *delegRepoFake) Create(_ context.Context, d domain.Delegation) (domain.Delegation, error) {
	for _, existing := range f.byID {
		if existing.PrincipalID == d.PrincipalID && existing.AssistantID == d.AssistantID {
			return domain.Delegation{}, domain.ErrConflict
		}
	}
	f.nextID++
	f.created++
	if d.ID == "" {
		d.ID = "del_" + strconv.Itoa(f.nextID)
	}
	f.byID[d.ID] = d
	return d, nil
}

func (f *delegRepoFake) GetByID(_ context.Context, id string) (domain.Delegation, error) {
	d, ok := f.byID[id]
	if !ok {
		return domain.Delegation{}, domain.ErrNotFound
	}
	return d, nil
}

func (f *delegRepoFake) GetActive(_ context.Context, principalID, assistantID string) (domain.Delegation, error) {
	for _, d := range f.byID {
		if d.PrincipalID == principalID && d.AssistantID == assistantID && d.Status == domain.DelegationActive {
			return d, nil
		}
	}
	return domain.Delegation{}, domain.ErrNotFound
}

func (f *delegRepoFake) ListByUser(_ context.Context, userID string) ([]domain.Delegation, error) {
	out := []domain.Delegation{}
	for _, d := range f.byID {
		if d.PrincipalID == userID || d.AssistantID == userID {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *delegRepoFake) Update(_ context.Context, d domain.Delegation) error {
	if _, ok := f.byID[d.ID]; !ok {
		return domain.ErrNotFound
	}
	f.byID[d.ID] = d
	return nil
}

type delegAuditFake struct {
	entries   []domain.AuditEntry
	appendErr error
}

func (f *delegAuditFake) Append(_ context.Context, e domain.AuditEntry) (domain.AuditEntry, error) {
	if f.appendErr != nil {
		return domain.AuditEntry{}, f.appendErr
	}
	if e.ID == "" {
		e.ID = "audit_" + strconv.Itoa(len(f.entries)+1)
	}
	f.entries = append(f.entries, e)
	return e, nil
}

func (f *delegAuditFake) ListByPrincipal(_ context.Context, principalID string, limit int) ([]domain.AuditEntry, error) {
	out := []domain.AuditEntry{}
	for i := len(f.entries) - 1; i >= 0 && len(out) < limit; i-- {
		if f.entries[i].PrincipalID == principalID {
			out = append(out, f.entries[i])
		}
	}
	return out, nil
}

type delegUserDirFake struct {
	byEmail map[string]domain.User
}

func (f *delegUserDirFake) GetByEmail(_ context.Context, email string) (domain.User, error) {
	u, ok := f.byEmail[email]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return u, nil
}

var (
	_ port.DelegationRepo          = (*delegRepoFake)(nil)
	_ port.AuditLogRepo            = (*delegAuditFake)(nil)
	_ port.DelegationUserDirectory = (*delegUserDirFake)(nil)
)

func newDelegService() (*DelegationService, *delegRepoFake, *delegAuditFake, *delegUserDirFake) {
	repo := newDelegRepoFake()
	audit := &delegAuditFake{}
	users := &delegUserDirFake{byEmail: map[string]domain.User{
		"assistant@example.com": {ID: "assistant_1", Email: "assistant@example.com"},
		"principal@example.com": {ID: "principal_1", Email: "principal@example.com"},
	}}
	svc := NewDelegationService(DelegationServiceDeps{
		Delegations: repo,
		Audit:       audit,
		Users:       users,
		Clock:       delegClock{t: time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)},
	})
	return svc, repo, audit, users
}

// grantActive creates and accepts a grant principal_1 → assistant_1.
func grantActive(t *testing.T, svc *DelegationService, scopes ...domain.DelegationScope) domain.Delegation {
	t.Helper()
	ctx := context.Background()
	d, err := svc.Create(ctx, "principal_1", "assistant@example.com", scopes)
	if err != nil {
		t.Fatalf("create grant: %v", err)
	}
	d, err = svc.Accept(ctx, "assistant_1", d.ID)
	if err != nil {
		t.Fatalf("accept grant: %v", err)
	}
	return d
}

// --- Step 1: service behavior ------------------------------------------------

func TestDelegationCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("unknown email is not found", func(t *testing.T) {
		svc, _, _, _ := newDelegService()
		_, err := svc.Create(ctx, "principal_1", "nobody@example.com", []domain.DelegationScope{domain.ScopeMailRead})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	})

	t.Run("self-delegation is invalid", func(t *testing.T) {
		svc, _, _, _ := newDelegService()
		_, err := svc.Create(ctx, "principal_1", "principal@example.com", []domain.DelegationScope{domain.ScopeMailRead})
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("want ErrValidation, got %v", err)
		}
	})

	t.Run("empty and invalid scopes are rejected", func(t *testing.T) {
		svc, _, _, _ := newDelegService()
		if _, err := svc.Create(ctx, "principal_1", "assistant@example.com", nil); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("empty scopes: want ErrValidation, got %v", err)
		}
		if _, err := svc.Create(ctx, "principal_1", "assistant@example.com", []domain.DelegationScope{"root"}); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("invalid scope: want ErrValidation, got %v", err)
		}
	})

	t.Run("grant starts pending with normalized email lookup", func(t *testing.T) {
		svc, _, _, _ := newDelegService()
		d, err := svc.Create(ctx, "principal_1", "  Assistant@Example.com ", []domain.DelegationScope{domain.ScopeMailRead, domain.ScopeMailRead, domain.ScopeMailWrite})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if d.Status != domain.DelegationPending {
			t.Fatalf("status = %q, want pending", d.Status)
		}
		if d.AssistantID != "assistant_1" || d.PrincipalID != "principal_1" {
			t.Fatalf("parties = %q→%q", d.PrincipalID, d.AssistantID)
		}
		if len(d.Scopes) != 2 {
			t.Fatalf("scopes = %v, want deduped [mail_read mail_write]", d.Scopes)
		}
	})

	t.Run("duplicate grant conflicts", func(t *testing.T) {
		svc, _, _, _ := newDelegService()
		scopes := []domain.DelegationScope{domain.ScopeMailRead}
		if _, err := svc.Create(ctx, "principal_1", "assistant@example.com", scopes); err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := svc.Create(ctx, "principal_1", "assistant@example.com", scopes); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("want ErrConflict, got %v", err)
		}
	})
}

func TestDelegationAccept(t *testing.T) {
	ctx := context.Background()

	t.Run("wrong user gets not found", func(t *testing.T) {
		svc, _, _, _ := newDelegService()
		d, err := svc.Create(ctx, "principal_1", "assistant@example.com", []domain.DelegationScope{domain.ScopeMailRead})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := svc.Accept(ctx, "someone_else", d.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
		if _, err := svc.Accept(ctx, "principal_1", d.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("principal accepting own grant: want ErrNotFound, got %v", err)
		}
	})

	t.Run("assistant accepts pending grant", func(t *testing.T) {
		svc, _, _, _ := newDelegService()
		d, err := svc.Create(ctx, "principal_1", "assistant@example.com", []domain.DelegationScope{domain.ScopeMailRead})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		got, err := svc.Accept(ctx, "assistant_1", d.ID)
		if err != nil {
			t.Fatalf("accept: %v", err)
		}
		if got.Status != domain.DelegationActive || got.AcceptedAt == nil {
			t.Fatalf("accepted grant = %+v, want active with AcceptedAt", got)
		}
	})

	t.Run("revoked grant cannot be accepted", func(t *testing.T) {
		svc, _, _, _ := newDelegService()
		d, _ := svc.Create(ctx, "principal_1", "assistant@example.com", []domain.DelegationScope{domain.ScopeMailRead})
		if err := svc.Revoke(ctx, "principal_1", d.ID); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		if _, err := svc.Accept(ctx, "assistant_1", d.ID); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("want ErrConflict, got %v", err)
		}
	})
}

func TestDelegationAuthorize(t *testing.T) {
	ctx := context.Background()

	t.Run("no grant fails closed", func(t *testing.T) {
		svc, _, _, _ := newDelegService()
		if err := svc.Authorize(ctx, "assistant_1", "principal_1", domain.ScopeMailRead); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("want ErrForbidden, got %v", err)
		}
	})

	t.Run("pending grant is forbidden", func(t *testing.T) {
		svc, _, _, _ := newDelegService()
		if _, err := svc.Create(ctx, "principal_1", "assistant@example.com", []domain.DelegationScope{domain.ScopeMailRead}); err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := svc.Authorize(ctx, "assistant_1", "principal_1", domain.ScopeMailRead); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("want ErrForbidden, got %v", err)
		}
	})

	t.Run("scope mismatch is forbidden", func(t *testing.T) {
		svc, _, _, _ := newDelegService()
		grantActive(t, svc, domain.ScopeMailRead)
		if err := svc.Authorize(ctx, "assistant_1", "principal_1", domain.ScopeMailWrite); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("want ErrForbidden, got %v", err)
		}
	})

	t.Run("active grant with scope authorizes", func(t *testing.T) {
		svc, _, _, _ := newDelegService()
		grantActive(t, svc, domain.ScopeMailRead, domain.ScopeCalendarWrite)
		if err := svc.Authorize(ctx, "assistant_1", "principal_1", domain.ScopeCalendarWrite); err != nil {
			t.Fatalf("authorize: %v", err)
		}
	})

	t.Run("self act-as is forbidden", func(t *testing.T) {
		svc, _, _, _ := newDelegService()
		if err := svc.Authorize(ctx, "principal_1", "principal_1", domain.ScopeMailRead); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("want ErrForbidden, got %v", err)
		}
	})
}

func TestDelegationRevocationIsImmediate(t *testing.T) {
	ctx := context.Background()

	t.Run("revocation by principal cuts access on the next call", func(t *testing.T) {
		svc, _, _, _ := newDelegService()
		d := grantActive(t, svc, domain.ScopeMailWrite)
		if err := svc.Authorize(ctx, "assistant_1", "principal_1", domain.ScopeMailWrite); err != nil {
			t.Fatalf("authorize before revoke: %v", err)
		}
		if err := svc.Revoke(ctx, "principal_1", d.ID); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		// Immediately after revocation — no cache window, the very next
		// authorization re-reads the grant and fails closed.
		if err := svc.Authorize(ctx, "assistant_1", "principal_1", domain.ScopeMailWrite); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("want ErrForbidden after revoke, got %v", err)
		}
	})

	t.Run("assistant can revoke their own side", func(t *testing.T) {
		svc, _, _, _ := newDelegService()
		d := grantActive(t, svc, domain.ScopeMailRead)
		if err := svc.Revoke(ctx, "assistant_1", d.ID); err != nil {
			t.Fatalf("revoke by assistant: %v", err)
		}
		if err := svc.Authorize(ctx, "assistant_1", "principal_1", domain.ScopeMailRead); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("want ErrForbidden, got %v", err)
		}
	})

	t.Run("outsider cannot revoke", func(t *testing.T) {
		svc, _, _, _ := newDelegService()
		d := grantActive(t, svc, domain.ScopeMailRead)
		if err := svc.Revoke(ctx, "someone_else", d.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	})
}

func TestDelegationList(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := newDelegService()
	grantActive(t, svc, domain.ScopeMailRead)

	asP, asA, err := svc.List(ctx, "principal_1")
	if err != nil {
		t.Fatalf("list principal: %v", err)
	}
	if len(asP) != 1 || len(asA) != 0 {
		t.Fatalf("principal sides = %d/%d, want 1/0", len(asP), len(asA))
	}
	asP, asA, err = svc.List(ctx, "assistant_1")
	if err != nil {
		t.Fatalf("list assistant: %v", err)
	}
	if len(asP) != 0 || len(asA) != 1 {
		t.Fatalf("assistant sides = %d/%d, want 0/1", len(asP), len(asA))
	}
}

func TestDelegationAudit(t *testing.T) {
	ctx := context.Background()

	t.Run("records attribute the real actor and the principal", func(t *testing.T) {
		svc, _, audit, _ := newDelegService()
		err := svc.RecordAudit(ctx, domain.AuditEntry{
			PrincipalID: "principal_1",
			ActorID:     "assistant_1",
			Action:      "POST /v1/mail/threads/{id}/actions",
			ResourceID:  "t1",
		})
		if err != nil {
			t.Fatalf("record: %v", err)
		}
		if len(audit.entries) != 1 {
			t.Fatalf("entries = %d, want 1", len(audit.entries))
		}
		e := audit.entries[0]
		if e.ActorID != "assistant_1" || e.PrincipalID != "principal_1" || e.CreatedAt.IsZero() {
			t.Fatalf("entry = %+v, want actor+principal+timestamp", e)
		}
	})

	t.Run("incomplete attribution is rejected", func(t *testing.T) {
		svc, _, audit, _ := newDelegService()
		for _, e := range []domain.AuditEntry{
			{ActorID: "a", Action: "x"},
			{PrincipalID: "p", Action: "x"},
			{PrincipalID: "p", ActorID: "a"},
		} {
			if err := svc.RecordAudit(ctx, e); !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("entry %+v: want ErrValidation, got %v", e, err)
			}
		}
		if len(audit.entries) != 0 {
			t.Fatalf("no incomplete entry may be written, got %d", len(audit.entries))
		}
	})

	t.Run("audit list is scoped to the caller as principal", func(t *testing.T) {
		svc, _, _, _ := newDelegService()
		if err := svc.RecordAudit(ctx, domain.AuditEntry{
			PrincipalID: "principal_1", ActorID: "assistant_1", Action: "POST /v1/mail/drafts",
		}); err != nil {
			t.Fatalf("record: %v", err)
		}
		got, err := svc.Audit(ctx, "principal_1", 0)
		if err != nil {
			t.Fatalf("audit principal: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("principal audit = %d entries, want 1", len(got))
		}
		// The assistant asking with their own id sees nothing of the
		// principal's log — the surface never accepts a foreign principal id.
		got, err = svc.Audit(ctx, "assistant_1", 0)
		if err != nil {
			t.Fatalf("audit assistant: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("assistant audit = %d entries, want 0", len(got))
		}
	})
}
