package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// integrationCallbackPathPrefix is the backend's own vendor-OAuth redirect
// target: redirect_uri = ${apiBase}${integrationCallbackPathPrefix}{vendor}.
const integrationCallbackPathPrefix = "/v1/integrations/callback/"

// integrationStateProvider namespaces oauth_states rows minted by the vendor
// flow (stored Provider = "integration:<vendor>"), so an account-connect
// state can never complete an integration callback — and vice versa. Any
// mismatch fails closed.
func integrationStateProvider(vendor domain.IntegrationVendor) domain.Provider {
	return domain.Provider("integration:" + string(vendor))
}

// IntegrationService implements port.IntegrationService: per-user vendor
// OAuth (Todoist/HubSpot) mirroring AccountService's connect choreography.
type IntegrationService struct {
	integrations     port.IntegrationRepo
	states           port.OAuthStateRepo
	oauth            map[domain.IntegrationVendor]port.OAuthGateway
	allowedRedirects []string
	callbackBaseURL  string
	// purgeTodoistTasks removes the user's mirrored Todoist tasks on
	// disconnect. Wired by the composition root to TaskRepo.DeleteBySource
	// once the tasks surface lands (M2.8 Task 1/10); nil-safe until then.
	purgeTodoistTasks func(ctx context.Context, userID string) error
	clock             port.Clock
}

var _ port.IntegrationService = (*IntegrationService)(nil)

func NewIntegrationService(
	integrations port.IntegrationRepo,
	states port.OAuthStateRepo,
	oauth map[domain.IntegrationVendor]port.OAuthGateway,
	allowedRedirects []string,
	callbackBaseURL string,
	purgeTodoistTasks func(ctx context.Context, userID string) error,
	clock port.Clock,
) *IntegrationService {
	return &IntegrationService{
		integrations:      integrations,
		states:            states,
		oauth:             oauth,
		allowedRedirects:  allowedRedirects,
		callbackBaseURL:   strings.TrimRight(callbackBaseURL, "/"),
		purgeTodoistTasks: purgeTodoistTasks,
		clock:             clock,
	}
}

func (s *IntegrationService) List(ctx context.Context, userID string) ([]domain.IntegrationConnection, error) {
	conns, err := s.integrations.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if conns == nil {
		conns = []domain.IntegrationConnection{}
	}
	return conns, nil
}

func (s *IntegrationService) BeginConnect(ctx context.Context, userID string, vendor domain.IntegrationVendor, redirectURL, requestBaseURL string) (string, error) {
	if redirectURL == "" {
		return "", fmt.Errorf("%w: redirectUrl is required", domain.ErrValidation)
	}
	if !redirectAllowed(redirectURL, s.allowedRedirects) {
		return "", fmt.Errorf("%w: redirectUrl is not allowed", domain.ErrValidation)
	}
	gw, ok := s.oauth[vendor]
	if !ok {
		return "", fmt.Errorf("%w: integration vendor %s is not configured", domain.ErrNotImplemented, vendor)
	}
	callback, err := s.callbackURI(vendor, requestBaseURL)
	if err != nil {
		return "", err
	}
	// One-time CSRF state, stored server-side. Vendor OAuth uses no PKCE
	// (Todoist/HubSpot are confidential-client flows): the challenge is
	// passed empty and no verifier is kept.
	state := randomToken(32)
	if err := s.states.Create(ctx, port.OAuthState{
		State:       state,
		UserID:      userID,
		Provider:    integrationStateProvider(vendor),
		RedirectURL: redirectURL, // client return target, replayed on callback
		ExpiresAt:   s.clock.Now().Add(oauthStateTTL),
	}); err != nil {
		return "", err
	}
	return gw.AuthURL(state, callback, ""), nil
}

func (s *IntegrationService) CompleteConnect(ctx context.Context, vendor domain.IntegrationVendor, state, code, requestBaseURL string) (domain.IntegrationConnection, string, error) {
	var zero domain.IntegrationConnection
	if state == "" {
		return zero, "", fmt.Errorf("%w: state is required", domain.ErrValidation)
	}
	st, err := s.states.Consume(ctx, state)
	if err != nil {
		// Replayed or forged state: fail closed with no client redirect.
		return zero, "", fmt.Errorf("%w: invalid oauth state", domain.ErrNotFound)
	}
	// The state is consumed; st.RedirectURL is the client return target the
	// callback 302s to on both success and failure.
	if st.Provider != integrationStateProvider(vendor) {
		// A state minted for another vendor — or for the account-connect flow
		// entirely — must never complete this callback.
		return zero, st.RedirectURL, fmt.Errorf("%w: oauth state vendor mismatch", domain.ErrUnauthorized)
	}
	if s.clock.Now().After(st.ExpiresAt) {
		return zero, st.RedirectURL, fmt.Errorf("%w: oauth state expired", domain.ErrUnauthorized)
	}
	if code == "" {
		return zero, st.RedirectURL, fmt.Errorf("%w: authorization code is required", domain.ErrValidation)
	}
	gw, ok := s.oauth[vendor]
	if !ok {
		return zero, st.RedirectURL, fmt.Errorf("%w: integration vendor %s is not configured", domain.ErrNotImplemented, vendor)
	}
	callback, err := s.callbackURI(vendor, requestBaseURL)
	if err != nil {
		return zero, st.RedirectURL, err
	}
	tok, err := gw.Exchange(ctx, code, callback, "")
	if err != nil {
		return zero, st.RedirectURL, fmt.Errorf("%w: authorization code exchange failed: %v", domain.ErrUnauthorized, err)
	}

	conn, err := s.upsertConnection(ctx, st.UserID, vendor, tok)
	if err != nil {
		return zero, st.RedirectURL, err
	}
	if err := s.integrations.SaveTokens(ctx, conn.ID, tok.TokenSet); err != nil {
		return zero, st.RedirectURL, err
	}
	return conn, st.RedirectURL, nil
}

// upsertConnection refreshes the user's existing (user, vendor) connection on
// reconnect instead of duplicating it (the table enforces uniqueness).
func (s *IntegrationService) upsertConnection(ctx context.Context, userID string, vendor domain.IntegrationVendor, tok port.OAuthToken) (domain.IntegrationConnection, error) {
	existing, err := s.integrations.GetByVendor(ctx, userID, vendor)
	switch {
	case err == nil:
		existing.Status = domain.IntegrationStatusActive
		existing.LastError = nil
		if tok.Email != "" {
			existing.ExternalAccount = tok.Email
		}
		if err := s.integrations.Update(ctx, existing); err != nil {
			return domain.IntegrationConnection{}, err
		}
		return existing, nil
	case errors.Is(err, domain.ErrNotFound):
		return s.integrations.Create(ctx, domain.IntegrationConnection{
			ID:              newID(),
			UserID:          userID,
			Vendor:          vendor,
			ExternalAccount: tok.Email, // vendor label; may be empty (e.g. Todoist)
			Status:          domain.IntegrationStatusActive,
			CreatedAt:       s.clock.Now(),
		})
	default:
		return domain.IntegrationConnection{}, err
	}
}

func (s *IntegrationService) Disconnect(ctx context.Context, userID, connectionID string) error {
	conn, err := s.integrations.GetByID(ctx, connectionID)
	if err != nil {
		return err
	}
	if conn.UserID != userID {
		// Foreign rows are indistinguishable from missing ones.
		return domain.ErrNotFound
	}
	// Disconnecting Todoist also purges the user's mirrored tasks so the
	// task rail never shows rows from a revoked grant.
	if conn.Vendor == domain.IntegrationTodoist && s.purgeTodoistTasks != nil {
		if err := s.purgeTodoistTasks(ctx, userID); err != nil {
			return err
		}
	}
	return s.integrations.Delete(ctx, conn.ID)
}

// callbackURI builds the vendor redirect_uri pointing at the backend's own
// integration callback. PUBLIC_API_URL wins; otherwise the origin is derived
// from the incoming request (see requestBaseURL in the HTTP adapter).
func (s *IntegrationService) callbackURI(vendor domain.IntegrationVendor, requestBaseURL string) (string, error) {
	base := s.callbackBaseURL
	if base == "" {
		base = strings.TrimRight(requestBaseURL, "/")
	}
	if base == "" {
		return "", fmt.Errorf("%w: cannot determine the API callback base URL (set PUBLIC_API_URL)", domain.ErrValidation)
	}
	return base + integrationCallbackPathPrefix + string(vendor), nil
}
