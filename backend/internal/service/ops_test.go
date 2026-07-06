package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- fakes -------------------------------------------------------------------

type stubAccountRepo struct {
	port.AccountRepo
	byID           map[string]domain.ConnectedAccount
	updated        *domain.ConnectedAccount
	created        *domain.ConnectedAccount
	savedTokensFor string
}

func (r *stubAccountRepo) GetByID(_ context.Context, id string) (domain.ConnectedAccount, error) {
	a, ok := r.byID[id]
	if !ok {
		return domain.ConnectedAccount{}, domain.ErrNotFound
	}
	return a, nil
}

func (r *stubAccountRepo) ListByUser(_ context.Context, userID string) ([]domain.ConnectedAccount, error) {
	out := []domain.ConnectedAccount{}
	for _, a := range r.byID {
		if a.UserID == userID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (r *stubAccountRepo) Create(_ context.Context, a domain.ConnectedAccount) (domain.ConnectedAccount, error) {
	if r.byID == nil {
		r.byID = map[string]domain.ConnectedAccount{}
	}
	r.byID[a.ID] = a
	r.created = &a
	return a, nil
}

func (r *stubAccountRepo) Update(_ context.Context, a domain.ConnectedAccount) error {
	if r.byID == nil {
		r.byID = map[string]domain.ConnectedAccount{}
	}
	r.byID[a.ID] = a
	r.updated = &a
	return nil
}

func (r *stubAccountRepo) SaveTokens(_ context.Context, accountID string, _ port.TokenSet) error {
	r.savedTokensFor = accountID
	return nil
}

type stubThreadRepo struct {
	port.ThreadRepo
	byID       map[string]domain.Thread
	markOpened int
	lastQuery  port.ThreadQuery
}

func (r *stubThreadRepo) GetByID(_ context.Context, id string) (domain.Thread, error) {
	t, ok := r.byID[id]
	if !ok {
		return domain.Thread{}, domain.ErrNotFound
	}
	return t, nil
}

func (r *stubThreadRepo) MarkOpened(_ context.Context, _ string) error {
	r.markOpened++
	return nil
}

func (r *stubThreadRepo) List(_ context.Context, q port.ThreadQuery) (domain.Page[domain.Thread], error) {
	r.lastQuery = q
	return domain.Page[domain.Thread]{Items: []domain.Thread{}}, nil
}

type stubDraftRepo struct {
	port.DraftRepo
	byID        map[string]domain.Draft
	claimResult bool
	claimedID   string
}

func (r *stubDraftRepo) GetByID(_ context.Context, id string) (domain.Draft, error) {
	d, ok := r.byID[id]
	if !ok {
		return domain.Draft{}, domain.ErrNotFound
	}
	return d, nil
}

func (r *stubDraftRepo) ClaimScheduled(_ context.Context, id string) (bool, error) {
	r.claimedID = id
	return r.claimResult, nil
}

type stubOAuthStateRepo struct {
	created *port.OAuthState
	byState map[string]port.OAuthState
}

func (r *stubOAuthStateRepo) Create(_ context.Context, s port.OAuthState) error {
	if r.byState == nil {
		r.byState = map[string]port.OAuthState{}
	}
	r.byState[s.State] = s
	r.created = &s
	return nil
}

func (r *stubOAuthStateRepo) Consume(_ context.Context, state string) (port.OAuthState, error) {
	s, ok := r.byState[state]
	if !ok {
		return port.OAuthState{}, domain.ErrNotFound
	}
	delete(r.byState, state)
	return s, nil
}

type stubOAuthGateway struct {
	authRedirect, authChallenge string
	exchRedirect, exchVerifier  string
	token                       port.OAuthToken
}

func (g *stubOAuthGateway) AuthURL(state, redirectURI, codeChallenge string) string {
	g.authRedirect, g.authChallenge = redirectURI, codeChallenge
	return "https://provider.example/auth?state=" + state
}

func (g *stubOAuthGateway) Exchange(_ context.Context, _, redirectURI, codeVerifier string) (port.OAuthToken, error) {
	g.exchRedirect, g.exchVerifier = redirectURI, codeVerifier
	return g.token, nil
}

func (g *stubOAuthGateway) Refresh(context.Context, string) (port.OAuthToken, error) {
	return port.OAuthToken{}, nil
}

// --- MailService.UnsendDraft -------------------------------------------------

func TestUnsendDraft(t *testing.T) {
	const owner = "u1"
	future := time.Now().Add(time.Minute)
	acct := domain.ConnectedAccount{ID: "a1", UserID: owner}

	tests := []struct {
		name  string
		draft *domain.Draft // nil = draft already delivered/deleted
		claim bool          // ClaimScheduled outcome
		want  error         // nil = success (schedule cleared)
	}{
		{"clears schedule within grace", &domain.Draft{ID: "d1", AccountID: "a1", ScheduledAt: &future}, true, nil},
		{"already delivered (draft gone)", nil, false, domain.ErrConflict},
		{"draft not scheduled to send", &domain.Draft{ID: "d1", AccountID: "a1"}, false, domain.ErrConflict},
		{"worker won the delivery race", &domain.Draft{ID: "d1", AccountID: "a1", ScheduledAt: &future}, false, domain.ErrConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			accounts := &stubAccountRepo{byID: map[string]domain.ConnectedAccount{"a1": acct}}
			drafts := &stubDraftRepo{byID: map[string]domain.Draft{}, claimResult: tt.claim}
			if tt.draft != nil {
				drafts.byID["d1"] = *tt.draft
			}
			svc := NewMailService(MailServiceDeps{Accounts: accounts, Drafts: drafts, Clock: SystemClock{}, SelfHosted: true})

			got, err := svc.UnsendDraft(context.Background(), owner, "d1")
			if tt.want != nil {
				if !errors.Is(err, tt.want) {
					t.Fatalf("err = %v, want %v", err, tt.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.ScheduledAt != nil {
				t.Fatalf("ScheduledAt = %v, want nil (cleared)", got.ScheduledAt)
			}
			if drafts.claimedID != "d1" {
				t.Fatalf("ClaimScheduled id = %q, want d1", drafts.claimedID)
			}
		})
	}
}

// --- MailService.MarkThreadOpened --------------------------------------------

func TestMarkThreadOpenedIdempotentAndOwned(t *testing.T) {
	const owner = "u1"
	accounts := &stubAccountRepo{byID: map[string]domain.ConnectedAccount{"a1": {ID: "a1", UserID: owner}}}
	threads := &stubThreadRepo{byID: map[string]domain.Thread{"t1": {ID: "t1", AccountID: "a1"}}}
	svc := NewMailService(MailServiceDeps{Accounts: accounts, Threads: threads, Clock: SystemClock{}, SelfHosted: true})

	for i := 0; i < 2; i++ {
		if err := svc.MarkThreadOpened(context.Background(), owner, "t1"); err != nil {
			t.Fatalf("open #%d: %v", i, err)
		}
	}
	if threads.markOpened != 2 {
		t.Fatalf("MarkOpened calls = %d, want 2 (idempotent, always re-asserts)", threads.markOpened)
	}
	if err := svc.MarkThreadOpened(context.Background(), "intruder", "t1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign open err = %v, want ErrNotFound", err)
	}
}

// --- MailService.ListThreads forwards the pseudo-view ------------------------

func TestListThreadsForwardsView(t *testing.T) {
	threads := &stubThreadRepo{}
	svc := NewMailService(MailServiceDeps{Threads: threads, Clock: SystemClock{}, SelfHosted: true})

	if _, err := svc.ListThreads(context.Background(), "u1", port.ThreadQuery{View: domain.ThreadViewSent}); err != nil {
		t.Fatal(err)
	}
	if threads.lastQuery.View != domain.ThreadViewSent {
		t.Fatalf("View forwarded = %q, want %q", threads.lastQuery.View, domain.ThreadViewSent)
	}
	if threads.lastQuery.UserID != "u1" {
		t.Fatalf("UserID = %q, want u1", threads.lastQuery.UserID)
	}
}

// --- AccountService.SetVipSenders --------------------------------------------

func TestSetVipSenders(t *testing.T) {
	const owner = "u1"
	accounts := &stubAccountRepo{byID: map[string]domain.ConnectedAccount{
		"a1": {ID: "a1", UserID: owner, Email: "me@x.com"},
	}}
	svc := NewAccountService(accounts, nil, nil, nil, nil, "", SystemClock{})

	got, err := svc.SetVipSenders(context.Background(), owner, "a1",
		[]string{" Boss@X.com ", "boss@x.com", "", "vip@y.com"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"boss@x.com", "vip@y.com"} // trimmed, lowercased, de-duplicated
	if !reflect.DeepEqual(got.VIPSenders, want) {
		t.Fatalf("VIPSenders = %v, want %v", got.VIPSenders, want)
	}
	if accounts.updated == nil || !reflect.DeepEqual(accounts.updated.VIPSenders, want) {
		t.Fatalf("not persisted through Update: %+v", accounts.updated)
	}
	if _, err := svc.SetVipSenders(context.Background(), "intruder", "a1", nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign account err = %v, want ErrNotFound", err)
	}
}

// --- AccountService OAuth: callback redirect_uri + PKCE ----------------------

func TestBeginConnectUsesCallbackAndPKCE(t *testing.T) {
	gw := &stubOAuthGateway{}
	states := &stubOAuthStateRepo{}
	oauth := map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: gw}
	svc := NewAccountService(&stubAccountRepo{}, states, nil, oauth,
		[]string{"https://app.example.com"}, "https://api.example.com", SystemClock{})

	authURL, err := svc.BeginConnect(context.Background(), "u1", domain.ProviderGoogle,
		"https://app.example.com/settings", "http://ignored:9999")
	if err != nil {
		t.Fatal(err)
	}
	// redirect_uri sent to the provider is the backend's own callback, never
	// the client return URL (finding 0).
	const wantCallback = "https://api.example.com/v1/accounts/callback/google"
	if gw.authRedirect != wantCallback {
		t.Fatalf("provider redirect_uri = %q, want %q", gw.authRedirect, wantCallback)
	}
	if states.created == nil || states.created.RedirectURL != "https://app.example.com/settings" {
		t.Fatalf("client redirect must be stored in state, got %+v", states.created)
	}
	// PKCE: verifier stored server-side, challenge is its S256 hash (finding 31).
	if states.created.CodeVerifier == "" {
		t.Fatal("no PKCE verifier stored in state")
	}
	if gw.authChallenge != pkceChallenge(states.created.CodeVerifier) {
		t.Fatalf("code_challenge %q != S256(verifier)", gw.authChallenge)
	}
	if !strings.Contains(authURL, states.created.State) {
		t.Fatal("authURL does not carry the state")
	}
}

func TestBeginConnectDerivesCallbackFromRequest(t *testing.T) {
	gw := &stubOAuthGateway{}
	states := &stubOAuthStateRepo{}
	oauth := map[domain.Provider]port.OAuthGateway{domain.ProviderMicrosoft: gw}
	// No PUBLIC_API_URL configured — derive the callback origin from the request.
	svc := NewAccountService(&stubAccountRepo{}, states, nil, oauth,
		[]string{"calendium://"}, "", SystemClock{})

	if _, err := svc.BeginConnect(context.Background(), "u1", domain.ProviderMicrosoft,
		"calendium://auth", "https://api.derived.test"); err != nil {
		t.Fatal(err)
	}
	const want = "https://api.derived.test/v1/accounts/callback/microsoft"
	if gw.authRedirect != want {
		t.Fatalf("provider redirect_uri = %q, want %q", gw.authRedirect, want)
	}
}

func TestCompleteConnectReplaysVerifierAndReturnsRedirect(t *testing.T) {
	gw := &stubOAuthGateway{token: port.OAuthToken{Email: "me@x.com", Scopes: []string{"scope"}}}
	accounts := &stubAccountRepo{byID: map[string]domain.ConnectedAccount{}}
	states := &stubOAuthStateRepo{}
	oauth := map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: gw}
	svc := NewAccountService(accounts, states, nil, oauth,
		[]string{"https://app.example.com"}, "https://api.example.com", SystemClock{})

	// Seed a valid state via BeginConnect.
	if _, err := svc.BeginConnect(context.Background(), "u1", domain.ProviderGoogle,
		"https://app.example.com/settings", ""); err != nil {
		t.Fatal(err)
	}
	state := states.created.State
	verifier := states.created.CodeVerifier

	acct, redirect, err := svc.CompleteConnect(context.Background(), domain.ProviderGoogle,
		state, "authcode", "https://api.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if gw.exchVerifier != verifier {
		t.Fatalf("exchange code_verifier = %q, want %q (PKCE round-trip)", gw.exchVerifier, verifier)
	}
	if gw.exchRedirect != "https://api.example.com/v1/accounts/callback/google" {
		t.Fatalf("exchange redirect_uri = %q", gw.exchRedirect)
	}
	if redirect != "https://app.example.com/settings" {
		t.Fatalf("client redirect = %q, want the stored return URL", redirect)
	}
	if acct.Email != "me@x.com" || accounts.created == nil {
		t.Fatalf("account not created from grant: %+v", acct)
	}
}
