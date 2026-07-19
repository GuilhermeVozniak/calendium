package domain

import "time"

// CRM integrations (M2.8 Task 16): contact context for the contact pane and
// explicit per-message email logging. Mirrors the CRM block in
// packages/shared/src/types.ts — keep the two in sync.

// IntegrationVendor identifies a third-party integration provider connected
// per user (M2.8), e.g. "hubspot".
//
// NOTE(M2.8 integration): the parallel integration-OAuth task (Task 9) owns
// the canonical integrations domain (integration_connections, connect and
// callback flow, AES-GCM token storage). If it declares the same type, unify
// on ONE declaration at merge — this one carries no extra behavior.
type IntegrationVendor string

// IntegrationVendorHubSpot is the HubSpot CRM.
const IntegrationVendorHubSpot IntegrationVendor = "hubspot"

// CrmContact is a CRM-side person record resolved by email address.
type CrmContact struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	Company   string `json:"company"`
	Title     string `json:"title"`
	Phone     string `json:"phone"`
	Owner     string `json:"owner"`
	VendorURL string `json:"vendorUrl"` // deep link into the CRM record
}

// CrmDeal is a deal/opportunity associated with a contact.
type CrmDeal struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Stage     string     `json:"stage"`
	Amount    *float64   `json:"amount"`
	CloseDate *time.Time `json:"closeDate"`
	VendorURL string     `json:"vendorUrl"`
}

// CrmContext is everything the contact pane shows for one email address.
type CrmContext struct {
	Vendor  IntegrationVendor `json:"vendor"`
	Contact *CrmContact       `json:"contact"` // nil = not in CRM
	Deals   []CrmDeal         `json:"deals"`
}

// CrmEmailLog is one email engagement recorded on a CRM contact's timeline.
// Logging is always an explicit per-message user action — mail is never
// exported to a CRM implicitly or in bulk.
type CrmEmailLog struct {
	ContactEmail string    `json:"contactEmail"`
	Subject      string    `json:"subject"`
	BodyText     string    `json:"bodyText"`
	SentAt       time.Time `json:"sentAt"`
	Direction    string    `json:"direction"` // inbound|outbound
}
