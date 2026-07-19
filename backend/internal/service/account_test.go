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

// --- AccountService.List ------------------------------------------------------

// TestAccountListScopedToUser pins account.go List: results are scoped to the
// requesting user (never leaking another user's accounts) and the nil slice
// from an empty ListByUser is normalized to a non-nil empty slice.
func TestAccountListScopedToUser(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	t.Run("scoped to requesting user", func(t *testing.T) {
		accounts := newAccountRepo()
		for _, a := range []domain.ConnectedAccount{
			{ID: "a1", UserID: "u1"},
			{ID: "a2", UserID: "u1"},
			{ID: "a3", UserID: "u2"},
		} {
			if _, err := accounts.Create(ctx, a); err != nil {
				t.Fatal(err)
			}
		}
		svc := NewAccountService(accounts, nil, nil, nil, nil, "", newClock(base))

		got, err := svc.List(ctx, "u1")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("len(got) = %d, want 2", len(got))
		}
		for _, a := range got {
			if a.UserID != "u1" {
				t.Fatalf("List(u1) returned foreign account %+v", a)
			}
			if a.ID == "a3" {
				t.Fatal("List(u1) leaked u2's account a3")
			}
		}
	})

	t.Run("empty result is non-nil", func(t *testing.T) {
		accounts := newAccountRepo()
		svc := NewAccountService(accounts, nil, nil, nil, nil, "", newClock(base))

		got, err := svc.List(ctx, "ghost")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if got == nil {
			t.Fatal("List returned nil slice, want non-nil empty slice")
		}
		if len(got) != 0 {
			t.Fatalf("len(got) = %d, want 0", len(got))
		}
	})
}

// --- AccountService.Disconnect ------------------------------------------------

// TestDisconnectAccount pins account.go Disconnect: an owned account is
// deleted and its sync state cleared; a foreign or missing account yields
// domain.ErrNotFound without mutating anything (ownership invariant: 404,
// never 401/403).
func TestDisconnectAccount(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	t.Run("owned account is deleted and sync state cleared", func(t *testing.T) {
		accounts := newAccountRepo()
		if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", UserID: "u1"}); err != nil {
			t.Fatal(err)
		}
		syncState := newSyncStateRepo()
		svc := NewAccountService(accounts, nil, syncState, nil, nil, "", newClock(base))

		if err := svc.Disconnect(ctx, "u1", "a1"); err != nil {
			t.Fatalf("Disconnect: %v", err)
		}
		if _, err := accounts.GetByID(ctx, "a1"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("GetByID after disconnect err = %v, want ErrNotFound", err)
		}
		found := false
		for _, id := range syncState.deletedAccounts {
			if id == "a1" {
				found = true
			}
		}
		if !found {
			t.Fatalf("syncState.deletedAccounts = %v, want it to contain a1", syncState.deletedAccounts)
		}
	})

	t.Run("foreign account is untouched, returns ErrNotFound", func(t *testing.T) {
		accounts := newAccountRepo()
		if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", UserID: "u2"}); err != nil {
			t.Fatal(err)
		}
		syncState := newSyncStateRepo()
		svc := NewAccountService(accounts, nil, syncState, nil, nil, "", newClock(base))

		if err := svc.Disconnect(ctx, "u1", "a1"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("Disconnect(u1, a1) err = %v, want ErrNotFound", err)
		}
		if _, err := accounts.GetByID(ctx, "a1"); err != nil {
			t.Fatalf("foreign account was deleted: %v", err)
		}
		if len(syncState.deletedAccounts) != 0 {
			t.Fatalf("syncState.deletedAccounts = %v, want empty (no cleanup on non-owned account)", syncState.deletedAccounts)
		}
	})

	t.Run("missing account returns ErrNotFound", func(t *testing.T) {
		accounts := newAccountRepo()
		syncState := newSyncStateRepo()
		svc := NewAccountService(accounts, nil, syncState, nil, nil, "", newClock(base))

		if err := svc.Disconnect(ctx, "u1", "ghost"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("Disconnect(u1, ghost) err = %v, want ErrNotFound", err)
		}
	})
}

// --- AccountService.SetSignature ---------------------------------------------

// TestSetSignature pins account.go SetSignature: an owned account's
// SignatureHTML is replaced verbatim (no sanitization at this layer — that
// happens at the HTTP boundary) and persisted via Update; a foreign account
// returns domain.ErrNotFound and leaves the account untouched.
func TestSetSignature(t *testing.T) {
	ctx := context.Background()
	const owner = "u1"
	const html = "<p>Best,<br>Ada</p>"

	t.Run("happy path persists the signature", func(t *testing.T) {
		accounts := newAccountRepo()
		if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", UserID: owner}); err != nil {
			t.Fatal(err)
		}
		svc := NewAccountService(accounts, nil, nil, nil, nil, "", newClock(time.Now()))

		got, err := svc.SetSignature(ctx, owner, "a1", html)
		if err != nil {
			t.Fatalf("SetSignature: %v", err)
		}
		if got.SignatureHTML != html {
			t.Fatalf("SignatureHTML = %q, want %q", got.SignatureHTML, html)
		}
		if accounts.updated == nil || accounts.updated.SignatureHTML != html {
			t.Fatalf("not persisted through Update: %+v", accounts.updated)
		}
	})

	t.Run("foreign account returns ErrNotFound, unchanged", func(t *testing.T) {
		accounts := newAccountRepo()
		if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", UserID: "u2"}); err != nil {
			t.Fatal(err)
		}
		svc := NewAccountService(accounts, nil, nil, nil, nil, "", newClock(time.Now()))

		if _, err := svc.SetSignature(ctx, owner, "a1", html); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("SetSignature(intruder) err = %v, want ErrNotFound", err)
		}
		if accounts.updated != nil {
			t.Fatalf("foreign account was updated: %+v", accounts.updated)
		}
		stored, err := accounts.GetByID(ctx, "a1")
		if err != nil {
			t.Fatal(err)
		}
		if stored.SignatureHTML != "" {
			t.Fatalf("SignatureHTML = %q, want unchanged empty", stored.SignatureHTML)
		}
	})

	t.Run("missing account returns ErrNotFound", func(t *testing.T) {
		accounts := newAccountRepo()
		svc := NewAccountService(accounts, nil, nil, nil, nil, "", newClock(time.Now()))

		if _, err := svc.SetSignature(ctx, owner, "ghost", html); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("SetSignature(ghost) err = %v, want ErrNotFound", err)
		}
	})

	t.Run("signature over 100 KB returns ErrValidation, unchanged", func(t *testing.T) {
		accounts := newAccountRepo()
		if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", UserID: owner}); err != nil {
			t.Fatal(err)
		}
		svc := NewAccountService(accounts, nil, nil, nil, nil, "", newClock(time.Now()))

		oversized := strings.Repeat("a", 100*1024+1)
		if _, err := svc.SetSignature(ctx, owner, "a1", oversized); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("SetSignature(oversized) err = %v, want ErrValidation", err)
		}
		if accounts.updated != nil {
			t.Fatalf("oversized signature was persisted: %+v", accounts.updated)
		}
	})
}

// --- AccountService.SetAutoBcc ------------------------------------------------

// TestSetAutoBcc pins account.go SetAutoBcc: an owned account's auto-BCC list
// is normalized (trimmed, lowercased, de-duplicated — same as SetVipSenders)
// and persisted via Update; a foreign account returns domain.ErrNotFound and
// leaves the account untouched.
func TestSetAutoBcc(t *testing.T) {
	ctx := context.Background()
	const owner = "u1"

	t.Run("happy path normalizes and persists", func(t *testing.T) {
		accounts := newAccountRepo()
		if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", UserID: owner}); err != nil {
			t.Fatal(err)
		}
		svc := NewAccountService(accounts, nil, nil, nil, nil, "", newClock(time.Now()))

		got, err := svc.SetAutoBcc(ctx, owner, "a1",
			[]string{" Archive@X.com ", "archive@x.com", "", "legal@y.com"})
		if err != nil {
			t.Fatalf("SetAutoBcc: %v", err)
		}
		want := []string{"archive@x.com", "legal@y.com"}
		if !reflect.DeepEqual(got.AutoBcc, want) {
			t.Fatalf("AutoBcc = %v, want %v", got.AutoBcc, want)
		}
		if accounts.updated == nil || !reflect.DeepEqual(accounts.updated.AutoBcc, want) {
			t.Fatalf("not persisted through Update: %+v", accounts.updated)
		}
	})

	t.Run("foreign account returns ErrNotFound, unchanged", func(t *testing.T) {
		accounts := newAccountRepo()
		if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", UserID: "u2"}); err != nil {
			t.Fatal(err)
		}
		svc := NewAccountService(accounts, nil, nil, nil, nil, "", newClock(time.Now()))

		if _, err := svc.SetAutoBcc(ctx, owner, "a1", []string{"x@y.com"}); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("SetAutoBcc(intruder) err = %v, want ErrNotFound", err)
		}
		if accounts.updated != nil {
			t.Fatalf("foreign account was updated: %+v", accounts.updated)
		}
		stored, err := accounts.GetByID(ctx, "a1")
		if err != nil {
			t.Fatal(err)
		}
		if len(stored.AutoBcc) != 0 {
			t.Fatalf("AutoBcc = %v, want unchanged empty", stored.AutoBcc)
		}
	})

	t.Run("missing account returns ErrNotFound", func(t *testing.T) {
		accounts := newAccountRepo()
		svc := NewAccountService(accounts, nil, nil, nil, nil, "", newClock(time.Now()))

		if _, err := svc.SetAutoBcc(ctx, owner, "ghost", []string{"x@y.com"}); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("SetAutoBcc(ghost) err = %v, want ErrNotFound", err)
		}
	})

	t.Run("display-name-wrapped address is canonicalized to the bare lowercased address", func(t *testing.T) {
		accounts := newAccountRepo()
		if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", UserID: owner}); err != nil {
			t.Fatal(err)
		}
		svc := NewAccountService(accounts, nil, nil, nil, nil, "", newClock(time.Now()))

		// "Bob <Bob@X.com>" is syntactically valid per mail.ParseAddress but
		// must never be persisted (or handed to a provider) with the display
		// name attached -- only the bare, lowercased address is stored.
		got, err := svc.SetAutoBcc(ctx, owner, "a1", []string{"Bob <Bob@X.com>"})
		if err != nil {
			t.Fatalf("SetAutoBcc: %v", err)
		}
		want := []string{"bob@x.com"}
		if !reflect.DeepEqual(got.AutoBcc, want) {
			t.Fatalf("AutoBcc = %v, want %v (display name must be discarded)", got.AutoBcc, want)
		}
	})

	t.Run("invalid address returns ErrValidation, unchanged", func(t *testing.T) {
		accounts := newAccountRepo()
		if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", UserID: owner}); err != nil {
			t.Fatal(err)
		}
		svc := NewAccountService(accounts, nil, nil, nil, nil, "", newClock(time.Now()))

		if _, err := svc.SetAutoBcc(ctx, owner, "a1", []string{"not-an-email"}); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("SetAutoBcc(invalid) err = %v, want ErrValidation", err)
		}
		if accounts.updated != nil {
			t.Fatalf("account was updated despite an invalid entry: %+v", accounts.updated)
		}
		stored, err := accounts.GetByID(ctx, "a1")
		if err != nil {
			t.Fatal(err)
		}
		if len(stored.AutoBcc) != 0 {
			t.Fatalf("AutoBcc = %v, want unchanged empty", stored.AutoBcc)
		}
	})
}

// --- tokenSource.accessToken ---------------------------------------------------

// TestTokenSourceRefresh pins service.go tokenSource.accessToken: a live token
// is returned untouched; an expired one is refreshed, re-persisted via
// SaveTokens, and a failed refresh flags the account reauth_required and
// surfaces domain.ErrUnauthorized.
func TestTokenSourceRefresh(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	acct := domain.ConnectedAccount{ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Status: domain.AccountActive}

	// build wires an account + seeded token set into a tokenSource.
	build := func(clock *fakeClock, gw *fakeOAuthGateway, saved port.TokenSet) (*fakeAccountRepo, tokenSource) {
		accounts := newAccountRepo()
		if _, err := accounts.Create(ctx, acct); err != nil {
			t.Fatal(err)
		}
		if err := accounts.SaveTokens(ctx, acct.ID, saved); err != nil {
			t.Fatal(err)
		}
		ts := tokenSource{
			accounts: accounts,
			oauth:    map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: gw},
			clock:    clock,
		}
		return accounts, ts
	}

	t.Run("valid unexpired token returned as-is", func(t *testing.T) {
		gw := newOAuthGateway()
		_, ts := build(newClock(base), gw, port.TokenSet{
			AccessToken:  "access-good",
			RefreshToken: "refresh-1",
			ExpiresAt:    base.Add(time.Hour), // beyond the 1-minute slack
		})
		got, err := ts.accessToken(ctx, acct)
		if err != nil {
			t.Fatalf("accessToken: %v", err)
		}
		if got != "access-good" {
			t.Fatalf("token = %q, want access-good", got)
		}
		if gw.refreshCalls != 0 {
			t.Fatalf("Refresh called %d times, want 0 (token still valid)", gw.refreshCalls)
		}
	})

	t.Run("expired token triggers refresh and re-persist", func(t *testing.T) {
		gw := newOAuthGateway()
		gw.refreshToken = port.OAuthToken{TokenSet: port.TokenSet{
			AccessToken:  "access-fresh",
			RefreshToken: "refresh-2",
			ExpiresAt:    base.Add(time.Hour),
		}}
		accounts, ts := build(newClock(base), gw, port.TokenSet{
			AccessToken:  "access-stale",
			RefreshToken: "refresh-1",
			ExpiresAt:    base.Add(-time.Minute), // already expired
		})
		got, err := ts.accessToken(ctx, acct)
		if err != nil {
			t.Fatalf("accessToken: %v", err)
		}
		if got != "access-fresh" {
			t.Fatalf("token = %q, want access-fresh", got)
		}
		if gw.refreshCalls != 1 {
			t.Fatalf("Refresh calls = %d, want 1", gw.refreshCalls)
		}
		if gw.lastRefreshToken != "refresh-1" {
			t.Fatalf("Refresh got refresh token %q, want refresh-1", gw.lastRefreshToken)
		}
		persisted, err := accounts.GetTokens(ctx, acct.ID)
		if err != nil {
			t.Fatal(err)
		}
		if persisted.AccessToken != "access-fresh" || persisted.RefreshToken != "refresh-2" {
			t.Fatalf("persisted tokens = %+v, want the refreshed pair", persisted)
		}
	})

	t.Run("refresh failure flags reauth_required and returns ErrUnauthorized", func(t *testing.T) {
		gw := newOAuthGateway()
		gw.refreshErr = errors.New("invalid_grant")
		accounts, ts := build(newClock(base), gw, port.TokenSet{
			AccessToken:  "access-stale",
			RefreshToken: "refresh-1",
			ExpiresAt:    base.Add(-time.Minute),
		})
		if _, err := ts.accessToken(ctx, acct); !errors.Is(err, domain.ErrUnauthorized) {
			t.Fatalf("err = %v, want domain.ErrUnauthorized", err)
		}
		reloaded, err := accounts.GetByID(ctx, acct.ID)
		if err != nil {
			t.Fatal(err)
		}
		if reloaded.Status != domain.AccountReauthRequired {
			t.Fatalf("account status = %q, want %q", reloaded.Status, domain.AccountReauthRequired)
		}
	})

	t.Run("refresh omitting refresh_token keeps the prior one", func(t *testing.T) {
		gw := newOAuthGateway()
		gw.refreshToken = port.OAuthToken{TokenSet: port.TokenSet{
			AccessToken:  "access-fresh",
			RefreshToken: "", // provider did not rotate the refresh token
			ExpiresAt:    base.Add(time.Hour),
		}}
		accounts, ts := build(newClock(base), gw, port.TokenSet{
			AccessToken:  "access-stale",
			RefreshToken: "refresh-1",
			ExpiresAt:    base.Add(-time.Minute),
		})
		got, err := ts.accessToken(ctx, acct)
		if err != nil {
			t.Fatalf("accessToken: %v", err)
		}
		if got != "access-fresh" {
			t.Fatalf("token = %q, want access-fresh", got)
		}
		persisted, err := accounts.GetTokens(ctx, acct.ID)
		if err != nil {
			t.Fatal(err)
		}
		if persisted.RefreshToken != "refresh-1" {
			t.Fatalf("persisted RefreshToken = %q, want fallback to refresh-1", persisted.RefreshToken)
		}
		if persisted.AccessToken != "access-fresh" {
			t.Fatalf("persisted AccessToken = %q, want access-fresh", persisted.AccessToken)
		}
	})

	t.Run("no gateway configured for the provider", func(t *testing.T) {
		accounts := newAccountRepo()
		if _, err := accounts.Create(ctx, acct); err != nil {
			t.Fatal(err)
		}
		seeded := port.TokenSet{
			AccessToken:  "access-stale",
			RefreshToken: "refresh-1",
			ExpiresAt:    base.Add(-time.Minute), // already expired
		}
		if err := accounts.SaveTokens(ctx, acct.ID, seeded); err != nil {
			t.Fatal(err)
		}
		ts := tokenSource{
			accounts: accounts,
			oauth:    map[domain.Provider]port.OAuthGateway{}, // no gateway for domain.ProviderGoogle
			clock:    newClock(base),
		}

		_, err := ts.accessToken(ctx, acct)
		if err == nil {
			t.Fatal("accessToken: want a non-nil error when no gateway is configured")
		}
		if !strings.Contains(err.Error(), string(domain.ProviderGoogle)) {
			t.Fatalf("err = %q, want it to name the missing provider %q", err.Error(), acct.Provider)
		}

		persisted, gerr := accounts.GetTokens(ctx, acct.ID)
		if gerr != nil {
			t.Fatal(gerr)
		}
		if persisted != seeded {
			t.Fatalf("token set mutated: got %+v, want unchanged %+v", persisted, seeded)
		}
		reloaded, gerr := accounts.GetByID(ctx, acct.ID)
		if gerr != nil {
			t.Fatal(gerr)
		}
		if reloaded.Status != acct.Status {
			t.Fatalf("account status = %q, want unchanged %q", reloaded.Status, acct.Status)
		}
	})
}
