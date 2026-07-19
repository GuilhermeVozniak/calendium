package domain

import "time"

// AuditEntry records a mutation one user performed on another user's
// resources (e.g. an editor-grantee writing to a shared calendar through
// the owner's provider tokens). ActorID is who acted; PrincipalID is whose
// resource was touched.
type AuditEntry struct {
	ID           string         `json:"id"`
	ActorID      string         `json:"actorId"`
	PrincipalID  string         `json:"principalId"`
	Action       string         `json:"action"` // 'event.create' | 'event.update' | 'event.delete' | ...
	ResourceType string         `json:"resourceType"`
	ResourceID   string         `json:"resourceId"`
	Metadata     map[string]any `json:"metadata"`
	CreatedAt    time.Time      `json:"createdAt"`
}
