package domain

import (
	"fmt"
	"time"
)

// DelegationScope is one unit of access an assistant may exercise on behalf
// of a principal. Scopes are explicit and enumerated — there is no wildcard
// and no implied scope: a grant authorizes exactly what it lists.
type DelegationScope string

const (
	ScopeMailRead      DelegationScope = "mail_read"
	ScopeMailWrite     DelegationScope = "mail_write"
	ScopeCalendarRead  DelegationScope = "calendar_read"
	ScopeCalendarWrite DelegationScope = "calendar_write"
)

// ParseDelegationScope validates a scope body parameter.
func ParseDelegationScope(s string) (DelegationScope, error) {
	switch DelegationScope(s) {
	case ScopeMailRead, ScopeMailWrite, ScopeCalendarRead, ScopeCalendarWrite:
		return DelegationScope(s), nil
	}
	return "", fmt.Errorf("%w: unknown delegation scope %q", ErrValidation, s)
}

// DelegationStatus is the grant lifecycle: a grant authorizes nothing until
// the assistant accepts it (pending → active) and nothing again the moment
// either party revokes it (→ revoked, terminal).
type DelegationStatus string

const (
	DelegationPending DelegationStatus = "pending"
	DelegationActive  DelegationStatus = "active"
	DelegationRevoked DelegationStatus = "revoked"
)

// Delegation is an explicit grantor→delegate grant: the principal allows
// the assistant to act as them over the normal API, limited to Scopes.
type Delegation struct {
	ID          string            `json:"id"`
	PrincipalID string            `json:"principalId"`
	AssistantID string            `json:"assistantId"`
	Scopes      []DelegationScope `json:"scopes"`
	Status      DelegationStatus  `json:"status"`
	CreatedAt   time.Time         `json:"createdAt"`
	AcceptedAt  *time.Time        `json:"acceptedAt"`
	RevokedAt   *time.Time        `json:"revokedAt"`
}

// HasScope reports whether the grant lists s.
func (d Delegation) HasScope(s DelegationScope) bool {
	for _, have := range d.Scopes {
		if have == s {
			return true
		}
	}
	return false
}

// Delegated mutations are recorded as AuditEntry (audit.go): ActorID is the
// REAL actor (the assistant who made the request) and PrincipalID the
// account acted upon — every delegated action stays attributable to the
// human who performed it. The audit log is append-only: entries are never
// updated or deleted.
