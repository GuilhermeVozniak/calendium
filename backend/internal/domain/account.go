package domain

import (
	"fmt"
	"time"
)

// Provider is a mail + calendar provider.
type Provider string

const (
	ProviderGoogle    Provider = "google"
	ProviderMicrosoft Provider = "microsoft"
)

// ParseProvider validates a provider path/query parameter.
func ParseProvider(s string) (Provider, error) {
	switch Provider(s) {
	case ProviderGoogle, ProviderMicrosoft:
		return Provider(s), nil
	}
	return "", fmt.Errorf("%w: unknown provider %q", ErrValidation, s)
}

// AccountStatus is the sync/auth health of a connected account.
type AccountStatus string

const (
	AccountActive         AccountStatus = "active"
	AccountSyncing        AccountStatus = "syncing"
	AccountReauthRequired AccountStatus = "reauth_required"
	AccountDisconnected   AccountStatus = "disconnected"
)

// ConnectedAccount is a connected Google / Microsoft account providing both
// mail and calendar. OAuth refresh tokens are stored encrypted at rest by the
// AccountRepo adapter and never appear on this entity.
type ConnectedAccount struct {
	ID       string        `json:"id"`
	UserID   string        `json:"-"`
	Provider Provider      `json:"provider"`
	Email    string        `json:"email"`
	Status   AccountStatus `json:"status"`
	Scopes   []string      `json:"scopes"`
	// VIPSenders are addresses whose mail is classified into the "vip"
	// split at ingest.
	VIPSenders   []string   `json:"vipSenders"`
	LastSyncedAt *time.Time `json:"lastSyncedAt"`
	CreatedAt    time.Time  `json:"createdAt"`
	// SignatureHTML is the account's rich signature, auto-applied to new
	// compose/reply drafts.
	SignatureHTML string `json:"signatureHtml"`
	// AutoBcc is applied to every message sent from this account.
	AutoBcc []string `json:"autoBcc"`
}
