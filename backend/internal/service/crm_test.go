package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- local doubles (crm-prefixed to avoid clashing with package fixtures) ---

type crmConnStore struct {
	conns   []port.CrmConnection
	listErr error
	updated map[string]port.TokenSet // connectionID => persisted tokens
}

func (f *crmConnStore) ListByUser(_ context.Context, _ string) ([]port.CrmConnection, error) {
	return f.conns, f.listErr
}
func (f *crmConnStore) UpdateTokens(_ context.Context, connectionID string, t port.TokenSet) error {
	if f.updated == nil {
		f.updated = map[string]port.TokenSet{}
	}
	f.updated[connectionID] = t
	return nil
}

type crmProviderFake struct {
	vendor       domain.IntegrationVendor
	badTokens    map[string]bool // access tokens that 401
	ctxRet       domain.CrmContext
	contextCalls int
	gotTokens    []string
	gotEmails    []string
	gotLogs      []domain.CrmEmailLog
}

func (f *crmProviderFake) Vendor() domain.IntegrationVendor { return f.vendor }
func (f *crmProviderFake) ContactContext(_ context.Context, accessToken, email string) (domain.CrmContext, error) {
	f.contextCalls++
	f.gotTokens = append(f.gotTokens, accessToken)
	f.gotEmails = append(f.gotEmails, email)
	if f.badTokens[accessToken] {
		return domain.CrmContext{}, fmt.Errorf("vendor 401: %w", domain.ErrUnauthorized)
	}
	return f.ctxRet, nil
}
func (f *crmProviderFake) LogEmail(_ context.Context, accessToken string, log domain.CrmEmailLog) error {
	f.gotTokens = append(f.gotTokens, accessToken)
	if f.badTokens[accessToken] {
		return fmt.Errorf("vendor 401: %w", domain.ErrUnauthorized)
	}
	f.gotLogs = append(f.gotLogs, log)
	return nil
}

var _ port.CrmProvider = (*crmProviderFake)(nil)

type crmOAuthFake struct {
	refreshRet      port.OAuthToken
	refreshErr      error
	refreshCalls    int
	gotRefreshToken string
}

func (f *crmOAuthFake) AuthURL(_, _, _ string) string { return "" }
func (f *crmOAuthFake) Exchange(_ context.Context, _, _, _ string) (port.OAuthToken, error) {
	return port.OAuthToken{}, errors.New("not used")
}
func (f *crmOAuthFake) Refresh(_ context.Context, refreshToken string) (port.OAuthToken, error) {
	f.refreshCalls++
	f.gotRefreshToken = refreshToken
	return f.refreshRet, f.refreshErr
}

var _ port.OAuthGateway = (*crmOAuthFake)(nil)

// crmTestClock is a mutable clock so cache-expiry tests can advance time.
type crmTestClock struct{ now time.Time }

func (c *crmTestClock) Now() time.Time { return c.now }

func hubspotConn(id, access, refresh string) port.CrmConnection {
	return port.CrmConnection{
		ID:     id,
		UserID: "u1",
		Vendor: domain.IntegrationVendorHubSpot,
		Tokens: port.TokenSet{AccessToken: access, RefreshToken: refresh},
	}
}

func newCrmForTest(store *crmConnStore, provider *crmProviderFake, oauth *crmOAuthFake, clock *crmTestClock) *CrmService {
	providers := map[domain.IntegrationVendor]port.CrmProvider{}
	if provider != nil {
		providers[provider.vendor] = provider
	}
	oauthMap := map[domain.IntegrationVendor]port.OAuthGateway{}
	if oauth != nil {
		oauthMap[domain.IntegrationVendorHubSpot] = oauth
	}
	return NewCrmService(CrmServiceDeps{
		Subscriptions: newSubscriptionRepo(),
		Connections:   store,
		Providers:     providers,
		OAuth:         oauthMap,
		Clock:         clock,
		SelfHosted:    true, // bypass entitlement except in the dedicated test
	})
}

// --- tests -------------------------------------------------------------------

func TestCrmContactContextNoConnectionsReturnsEmpty(t *testing.T) {
	svc := newCrmForTest(&crmConnStore{}, nil, nil, &crmTestClock{now: time.Now()})

	got, err := svc.ContactContext(context.Background(), "u1", "a@b.c")
	if err != nil {
		t.Fatalf("ContactContext: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("got = %#v, want empty non-nil slice", got)
	}
}

func TestCrmContactContextFansOutAndCaches(t *testing.T) {
	clock := &crmTestClock{now: time.Now()}
	provider := &crmProviderFake{
		vendor: domain.IntegrationVendorHubSpot,
		ctxRet: domain.CrmContext{
			Vendor:  domain.IntegrationVendorHubSpot,
			Contact: &domain.CrmContact{ID: "301", Email: "ada@northwind.com"},
			Deals:   []domain.CrmDeal{},
		},
	}
	store := &crmConnStore{conns: []port.CrmConnection{hubspotConn("c1", "tok", "ref")}}
	svc := newCrmForTest(store, provider, nil, clock)

	got, err := svc.ContactContext(context.Background(), "u1", "Ada@Northwind.com")
	if err != nil {
		t.Fatalf("ContactContext: %v", err)
	}
	if len(got) != 1 || got[0].Contact == nil || got[0].Contact.ID != "301" {
		t.Fatalf("got = %+v", got)
	}
	if provider.gotEmails[0] != "ada@northwind.com" {
		t.Fatalf("provider email = %q, want lowercased/trimmed", provider.gotEmails[0])
	}

	// Second call within the TTL is served from cache.
	if _, err := svc.ContactContext(context.Background(), "u1", "ada@northwind.com"); err != nil {
		t.Fatal(err)
	}
	if provider.contextCalls != 1 {
		t.Fatalf("provider calls = %d, want 1 (cache hit)", provider.contextCalls)
	}

	// After the TTL the vendor is queried again.
	clock.now = clock.now.Add(crmContextTTL + time.Second)
	if _, err := svc.ContactContext(context.Background(), "u1", "ada@northwind.com"); err != nil {
		t.Fatal(err)
	}
	if provider.contextCalls != 2 {
		t.Fatalf("provider calls = %d, want 2 (cache expired)", provider.contextCalls)
	}
}

func TestCrmContactContextRefreshesTokenOnceOn401(t *testing.T) {
	provider := &crmProviderFake{
		vendor:    domain.IntegrationVendorHubSpot,
		badTokens: map[string]bool{"stale": true},
		ctxRet:    domain.CrmContext{Vendor: domain.IntegrationVendorHubSpot},
	}
	oauth := &crmOAuthFake{refreshRet: port.OAuthToken{TokenSet: port.TokenSet{AccessToken: "fresh"}}}
	store := &crmConnStore{conns: []port.CrmConnection{hubspotConn("c1", "stale", "ref-1")}}
	svc := newCrmForTest(store, provider, oauth, &crmTestClock{now: time.Now()})

	got, err := svc.ContactContext(context.Background(), "u1", "a@b.c")
	if err != nil {
		t.Fatalf("ContactContext: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got = %+v, want 1 context", got)
	}
	if oauth.refreshCalls != 1 || oauth.gotRefreshToken != "ref-1" {
		t.Fatalf("refresh calls = %d (token %q), want exactly one with ref-1", oauth.refreshCalls, oauth.gotRefreshToken)
	}
	if len(provider.gotTokens) != 2 || provider.gotTokens[0] != "stale" || provider.gotTokens[1] != "fresh" {
		t.Fatalf("provider tokens = %v, want [stale fresh]", provider.gotTokens)
	}
	// The refreshed set is persisted, preserving the refresh token the
	// provider omitted.
	saved, ok := store.updated["c1"]
	if !ok || saved.AccessToken != "fresh" || saved.RefreshToken != "ref-1" {
		t.Fatalf("persisted tokens = %+v, want fresh access + preserved refresh", saved)
	}
}

func TestCrmContactContext401AfterRefreshFailsWithoutLoop(t *testing.T) {
	provider := &crmProviderFake{
		vendor:    domain.IntegrationVendorHubSpot,
		badTokens: map[string]bool{"stale": true, "still-bad": true},
	}
	oauth := &crmOAuthFake{refreshRet: port.OAuthToken{TokenSet: port.TokenSet{AccessToken: "still-bad"}}}
	store := &crmConnStore{conns: []port.CrmConnection{hubspotConn("c1", "stale", "ref")}}
	svc := newCrmForTest(store, provider, oauth, &crmTestClock{now: time.Now()})

	_, err := svc.ContactContext(context.Background(), "u1", "a@b.c")
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized after one retry", err)
	}
	if oauth.refreshCalls != 1 {
		t.Fatalf("refresh calls = %d, want exactly 1 (no loop)", oauth.refreshCalls)
	}
	if provider.contextCalls != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.contextCalls)
	}
}

func TestCrmSkipsConnectionsWithoutConfiguredProvider(t *testing.T) {
	store := &crmConnStore{conns: []port.CrmConnection{{
		ID: "c9", UserID: "u1", Vendor: domain.IntegrationVendor("salesforce"),
		Tokens: port.TokenSet{AccessToken: "t"},
	}}}
	svc := newCrmForTest(store, nil, nil, &crmTestClock{now: time.Now()})

	got, err := svc.ContactContext(context.Background(), "u1", "a@b.c")
	if err != nil {
		t.Fatalf("ContactContext: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got = %+v, want empty (unconfigured vendor skipped)", got)
	}
	if err := svc.LogEmail(context.Background(), "u1", domain.CrmEmailLog{
		ContactEmail: "a@b.c", Direction: "outbound",
	}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("LogEmail err = %v, want ErrValidation (no usable CRM)", err)
	}
}

func TestCrmRequiresEntitlement(t *testing.T) {
	svc := NewCrmService(CrmServiceDeps{
		Subscriptions: newSubscriptionRepo(), // empty => ErrPaymentRequired
		Connections:   &crmConnStore{},
		Clock:         &crmTestClock{now: time.Now()},
		SelfHosted:    false,
	})
	if _, err := svc.ContactContext(context.Background(), "u1", "a@b.c"); !errors.Is(err, domain.ErrPaymentRequired) {
		t.Fatalf("ContactContext err = %v, want ErrPaymentRequired", err)
	}
	if err := svc.LogEmail(context.Background(), "u1", domain.CrmEmailLog{
		ContactEmail: "a@b.c", Direction: "outbound",
	}); !errors.Is(err, domain.ErrPaymentRequired) {
		t.Fatalf("LogEmail err = %v, want ErrPaymentRequired", err)
	}
}

func TestCrmLogEmailValidatesAndForwards(t *testing.T) {
	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	clock := &crmTestClock{now: now}
	provider := &crmProviderFake{vendor: domain.IntegrationVendorHubSpot}
	store := &crmConnStore{conns: []port.CrmConnection{hubspotConn("c1", "tok", "ref")}}
	svc := newCrmForTest(store, provider, nil, clock)
	ctx := context.Background()

	if err := svc.LogEmail(ctx, "u1", domain.CrmEmailLog{ContactEmail: "nope", Direction: "outbound"}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("bad email err = %v, want ErrValidation", err)
	}
	if err := svc.LogEmail(ctx, "u1", domain.CrmEmailLog{ContactEmail: "a@b.c", Direction: "sideways"}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("bad direction err = %v, want ErrValidation", err)
	}

	err := svc.LogEmail(ctx, "u1", domain.CrmEmailLog{
		ContactEmail: " Ada@Northwind.com ",
		Subject:      "Renewal",
		BodyText:     "body",
		Direction:    "outbound",
	})
	if err != nil {
		t.Fatalf("LogEmail: %v", err)
	}
	if len(provider.gotLogs) != 1 {
		t.Fatalf("provider logs = %d, want 1", len(provider.gotLogs))
	}
	got := provider.gotLogs[0]
	if got.ContactEmail != "ada@northwind.com" || got.Subject != "Renewal" || got.BodyText != "body" {
		t.Fatalf("forwarded log = %+v", got)
	}
	if !got.SentAt.Equal(now) {
		t.Fatalf("SentAt = %v, want defaulted to clock now %v", got.SentAt, now)
	}
}
