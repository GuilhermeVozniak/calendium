package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// oauthStateTTL bounds how long a provider-connect flow may take.
const oauthStateTTL = 10 * time.Minute

// callbackPathPrefix is the backend's own OAuth redirect target. Providers
// redirect here (redirect_uri = ${apiBase}${callbackPathPrefix}{provider});
// the handler then 302s the browser to the client's stored redirectUrl.
const callbackPathPrefix = "/v1/accounts/callback/"

// AccountService implements port.AccountService: the backend-owned
// Google/Microsoft OAuth flows (offline access) and account lifecycle.
type AccountService struct {
	accounts         port.AccountRepo
	states           port.OAuthStateRepo
	syncState        port.SyncStateRepo
	oauth            map[domain.Provider]port.OAuthGateway
	allowedRedirects []string
	// callbackBaseURL (PUBLIC_API_URL) is the API's public origin used to
	// build the provider redirect_uri. When empty it is derived per-request
	// from the incoming BeginConnect/callback request.
	callbackBaseURL string
	clock           port.Clock
}

var _ port.AccountService = (*AccountService)(nil)

func NewAccountService(
	accounts port.AccountRepo,
	states port.OAuthStateRepo,
	syncState port.SyncStateRepo,
	oauth map[domain.Provider]port.OAuthGateway,
	allowedRedirects []string,
	callbackBaseURL string,
	clock port.Clock,
) *AccountService {
	return &AccountService{
		accounts:         accounts,
		states:           states,
		syncState:        syncState,
		oauth:            oauth,
		allowedRedirects: allowedRedirects,
		callbackBaseURL:  strings.TrimRight(callbackBaseURL, "/"),
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

func (s *AccountService) BeginConnect(ctx context.Context, userID string, provider domain.Provider, redirectURL, requestBaseURL string) (string, error) {
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
	callback, err := s.callbackURI(provider, requestBaseURL)
	if err != nil {
		return "", err
	}
	// PKCE (S256): keep the verifier server-side in state, send only the
	// challenge to the provider.
	verifier := randomToken(32)
	state := randomToken(32)
	if err := s.states.Create(ctx, port.OAuthState{
		State:        state,
		UserID:       userID,
		Provider:     provider,
		RedirectURL:  redirectURL, // client return target, replayed on callback
		CodeVerifier: verifier,
		ExpiresAt:    s.clock.Now().Add(oauthStateTTL),
	}); err != nil {
		return "", err
	}
	return gw.AuthURL(state, callback, pkceChallenge(verifier)), nil
}

func (s *AccountService) CompleteConnect(ctx context.Context, provider domain.Provider, state, code, requestBaseURL string) (domain.ConnectedAccount, string, error) {
	var zero domain.ConnectedAccount
	if state == "" {
		return zero, "", fmt.Errorf("%w: state is required", domain.ErrValidation)
	}
	st, err := s.states.Consume(ctx, state)
	if err != nil {
		return zero, "", fmt.Errorf("%w: invalid oauth state", domain.ErrUnauthorized)
	}
	// The state is consumed; st.RedirectURL is the client return target the
	// callback 302s to on both success and failure.
	if st.Provider != provider {
		return zero, st.RedirectURL, fmt.Errorf("%w: oauth state provider mismatch", domain.ErrUnauthorized)
	}
	if s.clock.Now().After(st.ExpiresAt) {
		return zero, st.RedirectURL, fmt.Errorf("%w: oauth state expired", domain.ErrUnauthorized)
	}
	if code == "" {
		return zero, st.RedirectURL, fmt.Errorf("%w: authorization code is required", domain.ErrValidation)
	}
	gw, ok := s.oauth[provider]
	if !ok {
		return zero, st.RedirectURL, fmt.Errorf("%w: provider %s is not configured", domain.ErrValidation, provider)
	}
	callback, err := s.callbackURI(provider, requestBaseURL)
	if err != nil {
		return zero, st.RedirectURL, err
	}
	tok, err := gw.Exchange(ctx, code, callback, st.CodeVerifier)
	if err != nil {
		return zero, st.RedirectURL, fmt.Errorf("%w: authorization code exchange failed: %v", domain.ErrUnauthorized, err)
	}
	if tok.Email == "" {
		return zero, st.RedirectURL, fmt.Errorf("%w: provider grant did not include the account email", domain.ErrValidation)
	}

	// Reconnecting an existing account refreshes it instead of duplicating.
	existing, err := s.accounts.ListByUser(ctx, st.UserID)
	if err != nil {
		return zero, st.RedirectURL, err
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
			return zero, st.RedirectURL, err
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
			return zero, st.RedirectURL, err
		}
	}
	if err := s.accounts.SaveTokens(ctx, account.ID, tok.TokenSet); err != nil {
		return zero, st.RedirectURL, err
	}
	return account, st.RedirectURL, nil
}

// SetVipSenders replaces the account's VIP-sender list. Addresses are
// normalized (trimmed, lowercased, de-duplicated) so ingest classification
// matches reliably.
func (s *AccountService) SetVipSenders(ctx context.Context, userID, accountID string, vipSenders []string) (domain.ConnectedAccount, error) {
	a, err := ownedAccount(ctx, s.accounts, userID, accountID)
	if err != nil {
		return domain.ConnectedAccount{}, err
	}
	a.VIPSenders = normalizeVipSenders(vipSenders)
	if err := s.accounts.Update(ctx, a); err != nil {
		return domain.ConnectedAccount{}, err
	}
	return a, nil
}

func normalizeVipSenders(in []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, s := range in {
		e := strings.ToLower(strings.TrimSpace(s))
		if e == "" {
			continue
		}
		if _, dup := seen[e]; dup {
			continue
		}
		seen[e] = struct{}{}
		out = append(out, e)
	}
	return out
}

// callbackURI builds the provider redirect_uri pointing at the backend's own
// OAuth callback. PUBLIC_API_URL wins; otherwise the origin is derived from
// the incoming request (see requestBaseURL in the HTTP adapter).
func (s *AccountService) callbackURI(provider domain.Provider, requestBaseURL string) (string, error) {
	base := s.callbackBaseURL
	if base == "" {
		base = strings.TrimRight(requestBaseURL, "/")
	}
	if base == "" {
		return "", fmt.Errorf("%w: cannot determine the API callback base URL (set PUBLIC_API_URL)", domain.ErrValidation)
	}
	return base + callbackPathPrefix + string(provider), nil
}

// pkceChallenge is the base64url-encoded SHA-256 of the verifier (PKCE S256).
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
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
