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
			accounts := newAccountRepo()
			accounts.byID["a1"] = acct
			drafts := newDraftRepo(accounts)
			if tt.draft != nil {
				drafts.byID["d1"] = *tt.draft
			}
			drafts.claimOutcome["d1"] = tt.claim
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
	accounts := newAccountRepo()
	accounts.byID["a1"] = domain.ConnectedAccount{ID: "a1", UserID: owner}
	threads := newThreadRepo()
	threads.byID["t1"] = domain.Thread{ID: "t1", AccountID: "a1"}
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
	threads := newThreadRepo()
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
	accounts := newAccountRepo()
	accounts.byID["a1"] = domain.ConnectedAccount{ID: "a1", UserID: owner, Email: "me@x.com"}
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
	gw := newOAuthGateway()
	states := newOAuthStateRepo()
	oauth := map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: gw}
	svc := NewAccountService(newAccountRepo(), states, nil, oauth,
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
	gw := newOAuthGateway()
	states := newOAuthStateRepo()
	oauth := map[domain.Provider]port.OAuthGateway{domain.ProviderMicrosoft: gw}
	// No PUBLIC_API_URL configured — derive the callback origin from the request.
	svc := NewAccountService(newAccountRepo(), states, nil, oauth,
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
	gw := newOAuthGateway()
	gw.token = port.OAuthToken{Email: "me@x.com", Scopes: []string{"scope"}}
	accounts := newAccountRepo()
	states := newOAuthStateRepo()
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
