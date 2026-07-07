package postgres

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

func TestAccountRepoCreateGetAndEncryptedTokens(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	created, err := st.Accounts().Create(ctx, domain.ConnectedAccount{
		UserID:   "u1",
		Provider: domain.ProviderGoogle,
		Email:    "me@gmail.com",
		Status:   domain.AccountActive,
		Scopes:   []string{"https://mail.google.com/"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("Create did not assign an id")
	}

	got, err := st.Accounts().GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Email != "me@gmail.com" || got.Provider != domain.ProviderGoogle {
		t.Fatalf("GetByID = %+v", got)
	}
	// scanAccount normalizes nil slices to empty (never JSON null over the wire).
	if got.Scopes == nil || got.VIPSenders == nil {
		t.Fatalf("nil slices must be normalized to empty: %+v", got)
	}
	if len(got.VIPSenders) != 0 {
		t.Fatalf("Create must not write vip_senders: %+v", got.VIPSenders)
	}

	// --- token vault round-trip ---
	const access, refresh = "ya29.super-secret-access", "1//refresh-secret"
	exp := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := st.Accounts().SaveTokens(ctx, created.ID, port.TokenSet{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresAt:    exp,
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}
	tok, err := st.Accounts().GetTokens(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetTokens: %v", err)
	}
	if tok.AccessToken != access || tok.RefreshToken != refresh {
		t.Fatalf("token round-trip mismatch: %+v", tok)
	}
	if !tok.ExpiresAt.Equal(exp) {
		t.Fatalf("ExpiresAt = %v, want %v", tok.ExpiresAt, exp)
	}

	// --- ciphertext at rest: read the raw columns, assert no plaintext ---
	var accessEnc, refreshEnc []byte
	if err := db.QueryRowContext(ctx,
		`SELECT access_token_enc, refresh_token_enc FROM connected_accounts WHERE id = $1`,
		created.ID).Scan(&accessEnc, &refreshEnc); err != nil {
		t.Fatalf("read raw columns: %v", err)
	}
	if len(accessEnc) == 0 || bytes.Contains(accessEnc, []byte(access)) {
		t.Fatalf("access token stored in plaintext: %q", accessEnc)
	}
	if len(refreshEnc) == 0 || bytes.Contains(refreshEnc, []byte(refresh)) {
		t.Fatalf("refresh token stored in plaintext: %q", refreshEnc)
	}
	// nonce-prefixed GCM ciphertext is strictly longer than the plaintext.
	if len(accessEnc) <= len(access) {
		t.Fatalf("ciphertext unexpectedly short: %d bytes", len(accessEnc))
	}
}

func TestAccountRepoGetByIDMissing(t *testing.T) {
	st, _ := newTestStore(t)
	if _, err := st.Accounts().GetByID(context.Background(), "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestAccountRepoListByUser(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")

	a1, err := st.Accounts().Create(ctx, domain.ConnectedAccount{
		UserID: "u1", Provider: domain.ProviderGoogle, Email: "u1-a@example.com", Status: domain.AccountActive,
	})
	if err != nil {
		t.Fatalf("Create a1: %v", err)
	}
	a2, err := st.Accounts().Create(ctx, domain.ConnectedAccount{
		UserID: "u1", Provider: domain.ProviderMicrosoft, Email: "u1-b@example.com", Status: domain.AccountActive,
	})
	if err != nil {
		t.Fatalf("Create a2: %v", err)
	}
	if _, err := st.Accounts().Create(ctx, domain.ConnectedAccount{
		UserID: "u2", Provider: domain.ProviderGoogle, Email: "u2-a@example.com", Status: domain.AccountActive,
	}); err != nil {
		t.Fatalf("Create a3: %v", err)
	}

	list, err := st.Accounts().ListByUser(ctx, "u1")
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListByUser(u1) = %d accounts, want 2", len(list))
	}
	if list[0].ID != a1.ID || list[1].ID != a2.ID {
		t.Fatalf("ListByUser not ordered by created_at: %+v", list)
	}
}

func TestAccountRepoListSyncable(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	statuses := []domain.AccountStatus{
		domain.AccountActive, domain.AccountSyncing, domain.AccountReauthRequired, domain.AccountDisconnected,
	}
	ids := map[domain.AccountStatus]string{}
	for i, status := range statuses {
		a, err := st.Accounts().Create(ctx, domain.ConnectedAccount{
			UserID:   "u1",
			Provider: domain.ProviderGoogle,
			Email:    "acct" + string(rune('a'+i)) + "@example.com",
			Status:   status,
		})
		if err != nil {
			t.Fatalf("Create status=%s: %v", status, err)
		}
		ids[status] = a.ID
	}

	syncable, err := st.Accounts().ListSyncable(ctx)
	if err != nil {
		t.Fatalf("ListSyncable: %v", err)
	}
	got := map[string]bool{}
	for _, a := range syncable {
		got[a.ID] = true
	}
	if !got[ids[domain.AccountActive]] || !got[ids[domain.AccountSyncing]] {
		t.Fatalf("ListSyncable missing active/syncing accounts: %+v", syncable)
	}
	if got[ids[domain.AccountReauthRequired]] || got[ids[domain.AccountDisconnected]] {
		t.Fatalf("ListSyncable returned non-syncable accounts: %+v", syncable)
	}
}

func TestAccountRepoUpdate(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	a, err := st.Accounts().Create(ctx, domain.ConnectedAccount{
		UserID: "u1", Provider: domain.ProviderGoogle, Email: "orig@example.com", Status: domain.AccountActive,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	a.Email = "updated@example.com"
	a.Status = domain.AccountReauthRequired
	a.Scopes = []string{"scope.a", "scope.b"}
	a.VIPSenders = []string{"vip@example.com"}
	if err := st.Accounts().Update(ctx, a); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := st.Accounts().GetByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Email != "updated@example.com" {
		t.Fatalf("Email = %q, want updated@example.com", got.Email)
	}
	if got.Status != domain.AccountReauthRequired {
		t.Fatalf("Status = %q, want reauth_required", got.Status)
	}
	if len(got.Scopes) != 2 || got.Scopes[0] != "scope.a" || got.Scopes[1] != "scope.b" {
		t.Fatalf("Scopes = %+v", got.Scopes)
	}
	if len(got.VIPSenders) != 1 || got.VIPSenders[0] != "vip@example.com" {
		t.Fatalf("VIPSenders = %+v", got.VIPSenders)
	}
}

func TestAccountRepoUpdateMissing(t *testing.T) {
	st, _ := newTestStore(t)
	err := st.Accounts().Update(context.Background(), domain.ConnectedAccount{ID: "nope", Email: "x@example.com"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestAccountRepoDelete(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	a, err := st.Accounts().Create(ctx, domain.ConnectedAccount{
		UserID: "u1", Provider: domain.ProviderGoogle, Email: "del@example.com", Status: domain.AccountActive,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := st.Accounts().Delete(ctx, a.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := st.Accounts().GetByID(ctx, a.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByID after delete: err = %v, want ErrNotFound", err)
	}
	if err := st.Accounts().Delete(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Delete unknown: err = %v, want ErrNotFound", err)
	}
}

func TestAccountRepoSaveTokensMissing(t *testing.T) {
	st, _ := newTestStore(t)
	err := st.Accounts().SaveTokens(context.Background(), "nope", port.TokenSet{AccessToken: "x"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestAccountRepoSaveTokensEmpty(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	a := seedAccount(t, st, "u1")

	if err := st.Accounts().SaveTokens(ctx, a.ID, port.TokenSet{}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}
	tok, err := st.Accounts().GetTokens(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetTokens: %v", err)
	}
	if tok.AccessToken != "" || tok.RefreshToken != "" {
		t.Fatalf("token = %+v, want empty strings", tok)
	}
	if !tok.ExpiresAt.IsZero() {
		t.Fatalf("ExpiresAt = %v, want zero", tok.ExpiresAt)
	}
}
