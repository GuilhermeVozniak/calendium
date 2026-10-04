package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// crmContextTTL is how long per-(user,email) CRM context is served from
// memory before re-querying the vendor.
const crmContextTTL = 5 * time.Minute

// CrmServiceDeps wires the CRM use-cases (M2.8 Task 16).
type CrmServiceDeps struct {
	Subscriptions port.SubscriptionRepo
	Users         port.UserRepo // anchors the entitlement gate's lazy trial grant
	// Connections is the narrow consumer-side view of the per-user
	// integration-connection storage built by the parallel integration-OAuth
	// task (Task 9). This service never stores or refreshes tokens itself
	// beyond persisting a gateway-refreshed set back through this store.
	Connections port.CrmConnectionStore
	Providers   map[domain.IntegrationVendor]port.CrmProvider
	// OAuth refreshes vendor tokens when a provider call returns 401
	// (exactly one retry), keyed like Providers.
	OAuth      map[domain.IntegrationVendor]port.OAuthGateway
	Clock      port.Clock
	SelfHosted bool
}

// CrmService implements port.CrmService: fan-out over the user's connected
// CRM vendors with entitlement gating, token refresh on 401, and a 5-minute
// in-memory context cache.
type CrmService struct {
	ent       entitlement
	conns     port.CrmConnectionStore
	providers map[domain.IntegrationVendor]port.CrmProvider
	oauth     map[domain.IntegrationVendor]port.OAuthGateway
	clock     port.Clock

	mu    sync.Mutex
	cache map[string]crmCacheEntry
}

type crmCacheEntry struct {
	contexts []domain.CrmContext
	at       time.Time
}

var _ port.CrmService = (*CrmService)(nil)

func NewCrmService(d CrmServiceDeps) *CrmService {
	return &CrmService{
		ent:       entitlement{subs: d.Subscriptions, users: d.Users, clock: d.Clock, selfHost: d.SelfHosted},
		conns:     d.Connections,
		providers: d.Providers,
		oauth:     d.OAuth,
		clock:     d.Clock,
		cache:     map[string]crmCacheEntry{},
	}
}

// ContactContext returns one CrmContext per connected-and-configured CRM
// vendor (empty slice when none — the pane hides its section). Results are
// cached per (user, email) for crmContextTTL.
func (s *CrmService) ContactContext(ctx context.Context, userID, email string) ([]domain.CrmContext, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || !strings.Contains(email, "@") {
		return nil, fmt.Errorf("%w: a valid email is required", domain.ErrValidation)
	}

	key := userID + "\x00" + email
	now := s.clock.Now()
	s.mu.Lock()
	if e, ok := s.cache[key]; ok && now.Sub(e.at) < crmContextTTL {
		s.mu.Unlock()
		return e.contexts, nil
	}
	s.mu.Unlock()

	conns, err := s.conns.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := []domain.CrmContext{}
	for _, conn := range conns {
		provider, ok := s.providers[conn.Vendor]
		if !ok {
			// Vendor not configured on this instance — skip; a connection
			// without an adapter must degrade to "nothing to show", never 501.
			continue
		}
		var cctx domain.CrmContext
		err := s.withFreshToken(ctx, conn, func(accessToken string) error {
			var callErr error
			cctx, callErr = provider.ContactContext(ctx, accessToken, email)
			return callErr
		})
		if err != nil {
			return nil, err
		}
		out = append(out, cctx)
	}

	s.mu.Lock()
	s.cache[key] = crmCacheEntry{contexts: out, at: now}
	s.mu.Unlock()
	return out, nil
}

// LogEmail records one email on the CRM timeline of the contact. This is an
// explicit per-message user action; nothing in this service (or its callers)
// exports mail to a third party in bulk or implicitly.
func (s *CrmService) LogEmail(ctx context.Context, userID string, log domain.CrmEmailLog) error {
	if err := s.ent.require(ctx, userID); err != nil {
		return err
	}
	log.ContactEmail = strings.ToLower(strings.TrimSpace(log.ContactEmail))
	if log.ContactEmail == "" || !strings.Contains(log.ContactEmail, "@") {
		return fmt.Errorf("%w: a valid contactEmail is required", domain.ErrValidation)
	}
	if log.Direction != "inbound" && log.Direction != "outbound" {
		return fmt.Errorf("%w: direction must be inbound or outbound", domain.ErrValidation)
	}
	if log.SentAt.IsZero() {
		log.SentAt = s.clock.Now()
	}

	conns, err := s.conns.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	for _, conn := range conns {
		provider, ok := s.providers[conn.Vendor]
		if !ok {
			continue
		}
		return s.withFreshToken(ctx, conn, func(accessToken string) error {
			return provider.LogEmail(ctx, accessToken, log)
		})
	}
	return fmt.Errorf("%w: no CRM is connected", domain.ErrValidation)
}

// withFreshToken calls fn with the connection's access token; on 401 it
// refreshes through the vendor's OAuth gateway exactly once, persists the new
// tokens through the connection store (which encrypts at rest), and retries.
func (s *CrmService) withFreshToken(ctx context.Context, conn port.CrmConnection, fn func(accessToken string) error) error {
	err := fn(conn.Tokens.AccessToken)
	if err == nil || !errors.Is(err, domain.ErrUnauthorized) {
		return err
	}
	gw, ok := s.oauth[conn.Vendor]
	if !ok {
		return err
	}
	tok, rerr := gw.Refresh(ctx, conn.Tokens.RefreshToken)
	if rerr != nil {
		return fmt.Errorf("refresh %s token: %w", conn.Vendor, rerr)
	}
	ts := tok.TokenSet
	if ts.RefreshToken == "" {
		// Providers commonly omit the refresh token on refresh — keep ours.
		ts.RefreshToken = conn.Tokens.RefreshToken
	}
	if uerr := s.conns.UpdateTokens(ctx, conn.ID, ts); uerr != nil {
		return uerr
	}
	return fn(ts.AccessToken)
}
