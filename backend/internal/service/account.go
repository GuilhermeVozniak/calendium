package service

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// oauthStateTTL bounds how long a provider-connect flow may take.
const oauthStateTTL = 10 * time.Minute

// AccountService implements port.AccountService: the backend-owned
// Google/Microsoft OAuth flows (offline access) and account lifecycle.
type AccountService struct {
	accounts         port.AccountRepo
	states           port.OAuthStateRepo
	syncState        port.SyncStateRepo
	oauth            map[domain.Provider]port.OAuthGateway
	allowedRedirects []string
	clock            port.Clock
}

var _ port.AccountService = (*AccountService)(nil)

func NewAccountService(
	accounts port.AccountRepo,
	states port.OAuthStateRepo,
	syncState port.SyncStateRepo,
	oauth map[domain.Provider]port.OAuthGateway,
	allowedRedirects []string,
	clock port.Clock,
) *AccountService {
	return &AccountService{
		accounts:         accounts,
		states:           states,
		syncState:        syncState,
		oauth:            oauth,
		allowedRedirects: allowedRedirects,
		clock:            clock,
	}
}

func (s *AccountService) List(ctx context.Context, userID string) ([]domain.ConnectedAccount, error) {
	accounts, err := s.accounts.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if accounts == nil {
		accounts = []domain.ConnectedAccount{}
	}
	return accounts, nil
}

func (s *AccountService) BeginConnect(ctx context.Context, userID string, provider domain.Provider, redirectURL string) (string, error) {
	if redirectURL == "" {
		return "", fmt.Errorf("%w: redirectUrl is required", domain.ErrValidation)
	}
	if !redirectAllowed(redirectURL, s.allowedRedirects) {
		return "", fmt.Errorf("%w: redirectUrl is not allowed", domain.ErrValidation)
	}
	gw, ok := s.oauth[provider]
	if !ok {
		return "", fmt.Errorf("%w: provider %s is not configured", domain.ErrValidation, provider)
	}
	state := randomToken(32)
	if err := s.states.Create(ctx, port.OAuthState{
		State:       state,
		UserID:      userID,
		Provider:    provider,
		RedirectURL: redirectURL,
		ExpiresAt:   s.clock.Now().Add(oauthStateTTL),
	}); err != nil {
		return "", err
	}
	return gw.AuthURL(state, redirectURL), nil
}

func (s *AccountService) CompleteConnect(ctx context.Context, provider domain.Provider, state, code string) (domain.ConnectedAccount, error) {
	var zero domain.ConnectedAccount
	if state == "" || code == "" {
		return zero, fmt.Errorf("%w: state and code are required", domain.ErrValidation)
	}
	st, err := s.states.Consume(ctx, state)
	if err != nil {
		return zero, fmt.Errorf("%w: invalid oauth state", domain.ErrUnauthorized)
	}
	if st.Provider != provider {
		return zero, fmt.Errorf("%w: oauth state provider mismatch", domain.ErrUnauthorized)
	}
	if s.clock.Now().After(st.ExpiresAt) {
		return zero, fmt.Errorf("%w: oauth state expired", domain.ErrUnauthorized)
	}
	gw, ok := s.oauth[provider]
	if !ok {
		return zero, fmt.Errorf("%w: provider %s is not configured", domain.ErrValidation, provider)
	}
	tok, err := gw.Exchange(ctx, code, st.RedirectURL)
	if err != nil {
		return zero, fmt.Errorf("%w: authorization code exchange failed: %v", domain.ErrUnauthorized, err)
	}
	if tok.Email == "" {
		return zero, fmt.Errorf("%w: provider grant did not include the account email", domain.ErrValidation)
	}

	// Reconnecting an existing account refreshes it instead of duplicating.
	existing, err := s.accounts.ListByUser(ctx, st.UserID)
	if err != nil {
		return zero, err
	}
	var account domain.ConnectedAccount
	found := false
	for _, a := range existing {
		if a.Provider == provider && strings.EqualFold(a.Email, tok.Email) {
			account, found = a, true
			break
		}
	}
	if found {
		account.Status = domain.AccountActive
		if len(tok.Scopes) > 0 {
			account.Scopes = tok.Scopes
		}
		if err := s.accounts.Update(ctx, account); err != nil {
			return zero, err
		}
	} else {
		account, err = s.accounts.Create(ctx, domain.ConnectedAccount{
			ID:        newID(),
			UserID:    st.UserID,
			Provider:  provider,
			Email:     tok.Email,
			Status:    domain.AccountSyncing, // first sync pass flips it to active
			Scopes:    tok.Scopes,
			CreatedAt: s.clock.Now(),
		})
		if err != nil {
			return zero, err
		}
	}
	if err := s.accounts.SaveTokens(ctx, account.ID, tok.TokenSet); err != nil {
		return zero, err
	}
	return account, nil
}

func (s *AccountService) Disconnect(ctx context.Context, userID, accountID string) error {
	a, err := ownedAccount(ctx, s.accounts, userID, accountID)
	if err != nil {
		return err
	}
	if err := s.syncState.DeleteByAccount(ctx, a.ID); err != nil {
		return err
	}
	return s.accounts.Delete(ctx, a.ID)
}

// redirectAllowed validates a client-supplied OAuth redirect URL against the
// server-side allowlist. For an http(s) origin the scheme and host (hostname,
// and port when the allow entry pins one) must match; for a custom app scheme
// (a host-less allow entry such as "calendium://") a scheme match suffices.
// Any path is accepted. This blocks open-redirect abuse of the connect flow.
func redirectAllowed(raw string, allowed []string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return false
	}
	for _, a := range allowed {
		au, err := url.Parse(a)
		if err != nil || au.Scheme == "" {
			continue
		}
		if !strings.EqualFold(u.Scheme, au.Scheme) {
			continue
		}
		if au.Host == "" {
			return true // scheme-only allow entry (e.g. "calendium://")
		}
		if !strings.EqualFold(u.Hostname(), au.Hostname()) {
			continue
		}
		if au.Port() == "" || au.Port() == u.Port() {
			return true
		}
	}
	return false
}
