package port

import (
	"context"

	"calendium/backend/internal/domain"
)

// --- EA delegation (M2.7 Task 15) -------------------------------------------

// DelegationRepo persists EA delegation grants.
type DelegationRepo interface {
	Create(ctx context.Context, d domain.Delegation) (domain.Delegation, error)
	GetByID(ctx context.Context, id string) (domain.Delegation, error)
	// GetActive returns the (principal, assistant) grant only while its
	// status is active; domain.ErrNotFound otherwise. Authorization re-reads
	// this row on every delegated request — grants are never cached, so
	// revocation takes effect immediately.
	GetActive(ctx context.Context, principalID, assistantID string) (domain.Delegation, error)
	// ListByUser returns grants where userID is either the principal or the
	// assistant.
	ListByUser(ctx context.Context, userID string) ([]domain.Delegation, error)
	Update(ctx context.Context, d domain.Delegation) error
}

// AuditLogRepo persists the append-only delegation audit log. It
// deliberately exposes no update or delete surface: entries, once written,
// are immutable.
type AuditLogRepo interface {
	Append(ctx context.Context, e domain.AuditEntry) (domain.AuditEntry, error)
	// ListByPrincipal returns the principal's entries, newest first.
	ListByPrincipal(ctx context.Context, principalID string, limit int) ([]domain.AuditEntry, error)
}

// DelegationUserDirectory resolves the delegation target user by email.
type DelegationUserDirectory interface {
	GetByEmail(ctx context.Context, email string) (domain.User, error)
}

// DelegationService manages EA grants and authorizes delegated requests.
type DelegationService interface {
	Create(ctx context.Context, principalID, assistantEmail string, scopes []domain.DelegationScope) (domain.Delegation, error)
	List(ctx context.Context, userID string) (asPrincipal, asAssistant []domain.Delegation, err error)
	Accept(ctx context.Context, assistantID, delegationID string) (domain.Delegation, error)
	Revoke(ctx context.Context, userID, delegationID string) error // either party
	// Authorize validates that assistant may act as principal with the
	// given scope; returns ErrForbidden when the grant is missing/inactive
	// or lacks the scope. Called by the HTTP middleware.
	Authorize(ctx context.Context, assistantID, principalID string, scope domain.DelegationScope) error
	// RecordAudit persists an audit entry for a delegated mutation.
	RecordAudit(ctx context.Context, e domain.AuditEntry) error
	Audit(ctx context.Context, principalID string, limit int) ([]domain.AuditEntry, error)
}
