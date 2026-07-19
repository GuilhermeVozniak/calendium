package domain

import (
	"fmt"
	"time"
)

// IntegrationVendor is an external per-user integration vendor (todo/CRM
// tools connected via their own OAuth apps — M2.8).
type IntegrationVendor string

const (
	IntegrationTodoist IntegrationVendor = "todoist"
	IntegrationHubSpot IntegrationVendor = "hubspot"
)

// ParseIntegrationVendor validates a vendor path/query parameter.
func ParseIntegrationVendor(s string) (IntegrationVendor, error) {
	switch IntegrationVendor(s) {
	case IntegrationTodoist, IntegrationHubSpot:
		return IntegrationVendor(s), nil
	}
	return "", fmt.Errorf("%w: unknown integration vendor %q", ErrValidation, s)
}

// Integration connection health statuses.
const (
	IntegrationStatusActive = "active"
	IntegrationStatusError  = "error"
)

// IntegrationConnection is a per-user vendor OAuth grant (one per vendor).
// OAuth tokens are stored encrypted at rest by the IntegrationRepo adapter
// and NEVER appear on this entity, so they cannot leak through JSON
// serialization (the TokenHash / TeamInvitation precedent).
type IntegrationConnection struct {
	ID              string            `json:"id"`
	UserID          string            `json:"-"`
	Vendor          IntegrationVendor `json:"vendor"`
	ExternalAccount string            `json:"externalAccount"` // vendor login/portal label
	Status          string            `json:"status"`          // active|error
	LastError       *string           `json:"lastError"`
	CreatedAt       time.Time         `json:"createdAt"`
}
