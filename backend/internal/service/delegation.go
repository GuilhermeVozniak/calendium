package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// DelegationServiceDeps wires the delegation service.
type DelegationServiceDeps struct {
	Delegations port.DelegationRepo
	Audit       port.AuditRepo
	Users       port.DelegationUserDirectory
	Clock       port.Clock
}

// DelegationService implements port.DelegationService: explicit EA grants
// (grantor→delegate, scoped), fail-closed authorization that re-reads the
// grant on every call (revocation is immediate — nothing is cached), and
// the append-only audit log of delegated mutations.
type DelegationService struct {
	deps DelegationServiceDeps
}

var _ port.DelegationService = (*DelegationService)(nil)

// NewDelegationService builds a DelegationService.
func NewDelegationService(deps DelegationServiceDeps) *DelegationService {
	return &DelegationService{deps: deps}
}

// Create grants scopes to the existing user identified by assistantEmail.
// The grant starts pending: it authorizes nothing until accepted.
func (s *DelegationService) Create(ctx context.Context, principalID, assistantEmail string, scopes []domain.DelegationScope) (domain.Delegation, error) {
	email := strings.ToLower(strings.TrimSpace(assistantEmail))
	if email == "" {
		return domain.Delegation{}, fmt.Errorf("%w: assistant email is required", domain.ErrValidation)
	}
	if len(scopes) == 0 {
		return domain.Delegation{}, fmt.Errorf("%w: at least one scope is required", domain.ErrValidation)
	}
	deduped := make([]domain.DelegationScope, 0, len(scopes))
	seen := map[domain.DelegationScope]bool{}
	for _, sc := range scopes {
		if _, err := domain.ParseDelegationScope(string(sc)); err != nil {
			return domain.Delegation{}, err
		}
		if !seen[sc] {
			seen[sc] = true
			deduped = append(deduped, sc)
		}
	}
	assistant, err := s.deps.Users.GetByEmail(ctx, email)
	if err != nil {
		return domain.Delegation{}, err // ErrNotFound for unknown emails
	}
	if assistant.ID == principalID {
		return domain.Delegation{}, fmt.Errorf("%w: cannot delegate to yourself", domain.ErrValidation)
	}
	d, err := s.deps.Delegations.Create(ctx, domain.Delegation{
		PrincipalID: principalID,
		AssistantID: assistant.ID,
		Scopes:      deduped,
		Status:      domain.DelegationPending,
		CreatedAt:   s.deps.Clock.Now().UTC(),
	})
	if err != nil {
		return domain.Delegation{}, err
	}
	return s.enrich(ctx, d), nil
}

// List returns the caller's grants split by side.
func (s *DelegationService) List(ctx context.Context, userID string) (asPrincipal, asAssistant []domain.Delegation, err error) {
	all, err := s.deps.Delegations.ListByUser(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	asPrincipal, asAssistant = []domain.Delegation{}, []domain.Delegation{}
	for _, d := range all {
		d = s.enrich(ctx, d)
		if d.PrincipalID == userID {
			asPrincipal = append(asPrincipal, d)
		}
		if d.AssistantID == userID {
			asAssistant = append(asAssistant, d)
		}
	}
	return asPrincipal, asAssistant, nil
}

// Accept activates a pending grant. Only the named assistant may accept;
// anyone else gets ErrNotFound so grant existence is never leaked.
func (s *DelegationService) Accept(ctx context.Context, assistantID, delegationID string) (domain.Delegation, error) {
	d, err := s.deps.Delegations.GetByID(ctx, delegationID)
	if err != nil {
		return domain.Delegation{}, err
	}
	if d.AssistantID != assistantID {
		return domain.Delegation{}, fmt.Errorf("%w: delegation not found", domain.ErrNotFound)
	}
	switch d.Status {
	case domain.DelegationActive:
		return s.enrich(ctx, d), nil // idempotent
	case domain.DelegationRevoked:
		return domain.Delegation{}, fmt.Errorf("%w: delegation was revoked", domain.ErrConflict)
	}
	now := s.deps.Clock.Now().UTC()
	d.Status = domain.DelegationActive
	d.AcceptedAt = &now
	if err := s.deps.Delegations.Update(ctx, d); err != nil {
		return domain.Delegation{}, err
	}
	return s.enrich(ctx, d), nil
}

// Revoke terminates a grant. Either party may revoke; outsiders get
// ErrNotFound. Takes effect immediately: Authorize re-reads the grant on
// every call, so no in-flight cache can outlive a revocation.
func (s *DelegationService) Revoke(ctx context.Context, userID, delegationID string) error {
	d, err := s.deps.Delegations.GetByID(ctx, delegationID)
	if err != nil {
		return err
	}
	if d.PrincipalID != userID && d.AssistantID != userID {
		return fmt.Errorf("%w: delegation not found", domain.ErrNotFound)
	}
	if d.Status == domain.DelegationRevoked {
		return nil // idempotent
	}
	now := s.deps.Clock.Now().UTC()
	d.Status = domain.DelegationRevoked
	d.RevokedAt = &now
	return s.deps.Delegations.Update(ctx, d)
}

// Authorize validates that assistant may act as principal with scope. It
// fails closed: missing grant, pending or revoked grant, or a grant lacking
// the scope all yield ErrForbidden. The grant is fetched fresh on every
// call — never cached — so revocation is immediate.
func (s *DelegationService) Authorize(ctx context.Context, assistantID, principalID string, scope domain.DelegationScope) error {
	if assistantID == "" || principalID == "" || assistantID == principalID {
		return fmt.Errorf("%w: delegation required", domain.ErrForbidden)
	}
	d, err := s.deps.Delegations.GetActive(ctx, principalID, assistantID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("%w: no active delegation from this principal", domain.ErrForbidden)
		}
		return err
	}
	if d.Status != domain.DelegationActive {
		return fmt.Errorf("%w: delegation is not active", domain.ErrForbidden)
	}
	if !d.HasScope(scope) {
		return fmt.Errorf("%w: delegation does not grant scope %q", domain.ErrForbidden, scope)
	}
	return nil
}

// enrich resolves both parties' display identity (name + email) onto the
// grant for responses. Least-leak: it is only ever applied to grants the
// caller is a party to — both parties already share the delegation, so no
// new information crosses a boundary. Lookup failures leave the fields
// empty (never fabricated); clients fall back to the id.
func (s *DelegationService) enrich(ctx context.Context, d domain.Delegation) domain.Delegation {
	if p, err := s.deps.Users.GetByID(ctx, d.PrincipalID); err == nil {
		if p.Name != nil {
			d.PrincipalName = *p.Name
		}
		d.PrincipalEmail = p.Email
	}
	if a, err := s.deps.Users.GetByID(ctx, d.AssistantID); err == nil {
		if a.Name != nil {
			d.AssistantName = *a.Name
		}
		d.AssistantEmail = a.Email
	}
	return d
}

// RecordAudit persists an audit entry for a delegated mutation. Attribution
// is mandatory: entries missing the real actor, the principal, or the
// action are rejected rather than written incomplete.
func (s *DelegationService) RecordAudit(ctx context.Context, e domain.AuditEntry) error {
	if e.PrincipalID == "" || e.ActorID == "" || e.Action == "" {
		return fmt.Errorf("%w: audit entries require principal, actor, and action", domain.ErrValidation)
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = s.deps.Clock.Now().UTC()
	}
	return s.deps.Audit.Record(ctx, e)
}

// Audit lists the caller's own audit trail (principal-only by construction:
// the id is always the authenticated caller's, so an assistant can never
// read a principal's log).
func (s *DelegationService) Audit(ctx context.Context, principalID string, limit int) ([]domain.AuditEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	return s.deps.Audit.ListByPrincipal(ctx, principalID, limit)
}
