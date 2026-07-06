// Package service implements the driving ports (internal/port/driving.go)
// using only the domain model and the driven ports. It contains all business
// rules: split-inbox classification, snooze/reminder semantics, undo-send,
// subscription gating, and availability computation.
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// newID returns a random 32-hex-char identifier for locally created rows.
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("service: crypto/rand unavailable: %v", err))
	}
	return hex.EncodeToString(b[:])
}

// randomToken returns a URL-safe random token of n bytes, hex-encoded.
func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("service: crypto/rand unavailable: %v", err))
	}
	return hex.EncodeToString(b)
}

func ptr[T any](v T) *T { return &v }

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// SystemClock is the production port.Clock.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

// entitlement enforces the subscription paywall shared by all gated
// use-cases (docs/payments.md): access requires trialing, active, or
// past_due within the 7-day grace window; anything else is 402.
type entitlement struct {
	subs  port.SubscriptionRepo
	clock port.Clock
	// selfHost unlocks every gated use-case for open-core self-hosted
	// deployments (SELF_HOSTED=true): require always succeeds and never
	// touches the subscription repo.
	selfHost bool
}

func (e entitlement) require(ctx context.Context, userID string) error {
	if e.selfHost {
		return nil
	}
	sub, err := e.subs.GetByUserID(ctx, userID)
	if errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("%w: no subscription", domain.ErrPaymentRequired)
	}
	if err != nil {
		return err
	}
	if !sub.HasAccess(e.clock.Now()) {
		return fmt.Errorf("%w: subscription status %s", domain.ErrPaymentRequired, sub.Status)
	}
	return nil
}

// tokenExpirySlack refreshes tokens slightly before they expire.
const tokenExpirySlack = time.Minute

// tokenSource returns fresh provider access tokens, transparently refreshing
// and re-persisting them (encrypted by the AccountRepo) when expired. A
// failed refresh flags the account reauth_required.
type tokenSource struct {
	accounts port.AccountRepo
	oauth    map[domain.Provider]port.OAuthGateway
	clock    port.Clock
}

func (ts tokenSource) accessToken(ctx context.Context, account domain.ConnectedAccount) (string, error) {
	t, err := ts.accounts.GetTokens(ctx, account.ID)
	if err != nil {
		return "", err
	}
	if t.AccessToken != "" && ts.clock.Now().Add(tokenExpirySlack).Before(t.ExpiresAt) {
		return t.AccessToken, nil
	}
	gw, ok := ts.oauth[account.Provider]
	if !ok {
		return "", fmt.Errorf("service: no oauth gateway configured for provider %s", account.Provider)
	}
	fresh, err := gw.Refresh(ctx, t.RefreshToken)
	if err != nil {
		account.Status = domain.AccountReauthRequired
		_ = ts.accounts.Update(ctx, account)
		return "", fmt.Errorf("%w: token refresh failed for account %s: %v", domain.ErrUnauthorized, account.ID, err)
	}
	if fresh.RefreshToken == "" {
		fresh.RefreshToken = t.RefreshToken
	}
	if err := ts.accounts.SaveTokens(ctx, account.ID, fresh.TokenSet); err != nil {
		return "", err
	}
	return fresh.AccessToken, nil
}

// ownedAccount loads an account and enforces ownership; foreign rows are
// indistinguishable from missing ones (404, never 403).
func ownedAccount(ctx context.Context, accounts port.AccountRepo, userID, accountID string) (domain.ConnectedAccount, error) {
	a, err := accounts.GetByID(ctx, accountID)
	if err != nil {
		return domain.ConnectedAccount{}, err
	}
	if a.UserID != userID {
		return domain.ConnectedAccount{}, domain.ErrNotFound
	}
	return a, nil
}

func ownedThread(ctx context.Context, threads port.ThreadRepo, accounts port.AccountRepo, userID, threadID string) (domain.Thread, domain.ConnectedAccount, error) {
	t, err := threads.GetByID(ctx, threadID)
	if err != nil {
		return domain.Thread{}, domain.ConnectedAccount{}, err
	}
	a, err := accounts.GetByID(ctx, t.AccountID)
	if err != nil {
		return domain.Thread{}, domain.ConnectedAccount{}, err
	}
	if a.UserID != userID {
		return domain.Thread{}, domain.ConnectedAccount{}, domain.ErrNotFound
	}
	return t, a, nil
}

func ownedDraft(ctx context.Context, drafts port.DraftRepo, accounts port.AccountRepo, userID, draftID string) (domain.Draft, domain.ConnectedAccount, error) {
	d, err := drafts.GetByID(ctx, draftID)
	if err != nil {
		return domain.Draft{}, domain.ConnectedAccount{}, err
	}
	a, err := accounts.GetByID(ctx, d.AccountID)
	if err != nil {
		return domain.Draft{}, domain.ConnectedAccount{}, err
	}
	if a.UserID != userID {
		return domain.Draft{}, domain.ConnectedAccount{}, domain.ErrNotFound
	}
	return d, a, nil
}
