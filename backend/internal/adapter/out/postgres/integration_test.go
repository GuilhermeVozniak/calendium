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

func TestIntegrationRepoRoundTrip(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	repo := NewIntegrationRepo(st)
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")

	msg := "rate limited"
	created, err := repo.Create(ctx, domain.IntegrationConnection{
		UserID:          "u1",
		Vendor:          domain.IntegrationTodoist,
		ExternalAccount: "person@example.com",
		Status:          domain.IntegrationStatusActive,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" || created.CreatedAt.IsZero() {
		t.Fatalf("Create did not default id/createdAt: %+v", created)
	}

	got, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.UserID != "u1" || got.Vendor != domain.IntegrationTodoist || got.ExternalAccount != "person@example.com" || got.Status != "active" || got.LastError != nil {
		t.Fatalf("GetByID = %+v", got)
	}

	byVendor, err := repo.GetByVendor(ctx, "u1", domain.IntegrationTodoist)
	if err != nil || byVendor.ID != created.ID {
		t.Fatalf("GetByVendor = %+v, %v", byVendor, err)
	}
	if _, err := repo.GetByVendor(ctx, "u1", domain.IntegrationHubSpot); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByVendor miss = %v, want ErrNotFound", err)
	}

	// Update health fields.
	got.Status = domain.IntegrationStatusError
	got.LastError = &msg
	got.ExternalAccount = "renamed@example.com"
	if err := repo.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	updated, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "error" || updated.LastError == nil || *updated.LastError != msg || updated.ExternalAccount != "renamed@example.com" {
		t.Fatalf("Update round-trip = %+v", updated)
	}

	// Listings.
	if _, err := repo.Create(ctx, domain.IntegrationConnection{UserID: "u2", Vendor: domain.IntegrationTodoist, Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(ctx, domain.IntegrationConnection{UserID: "u1", Vendor: domain.IntegrationHubSpot, Status: "active"}); err != nil {
		t.Fatal(err)
	}
	byUser, err := repo.ListByUser(ctx, "u1")
	if err != nil || len(byUser) != 2 {
		t.Fatalf("ListByUser = %d conns, %v; want 2", len(byUser), err)
	}
	byVendorList, err := repo.ListByVendor(ctx, domain.IntegrationTodoist)
	if err != nil || len(byVendorList) != 2 {
		t.Fatalf("ListByVendor = %d conns, %v; want 2", len(byVendorList), err)
	}

	// Delete.
	if err := repo.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.GetByID(ctx, created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByID after delete = %v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("double Delete = %v, want ErrNotFound", err)
	}
}

func TestIntegrationRepoUniquePerUserVendor(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	repo := NewIntegrationRepo(st)
	seedUser(t, st, "u1")

	if _, err := repo.Create(ctx, domain.IntegrationConnection{UserID: "u1", Vendor: domain.IntegrationTodoist, Status: "active"}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := repo.Create(ctx, domain.IntegrationConnection{UserID: "u1", Vendor: domain.IntegrationTodoist, Status: "active"})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate (user, vendor) Create = %v, want ErrConflict", err)
	}
}

func TestIntegrationRepoTokensEncryptedAtRest(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	repo := NewIntegrationRepo(st)
	seedUser(t, st, "u1")

	conn, err := repo.Create(ctx, domain.IntegrationConnection{UserID: "u1", Vendor: domain.IntegrationHubSpot, Status: "active"})
	if err != nil {
		t.Fatal(err)
	}

	expires := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	plainAccess, plainRefresh := "super-secret-access-token", "super-secret-refresh-token"
	if err := repo.SaveTokens(ctx, conn.ID, port.TokenSet{
		AccessToken: plainAccess, RefreshToken: plainRefresh, ExpiresAt: expires,
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	// Decrypt round-trip through the repo.
	got, err := repo.GetTokens(ctx, conn.ID)
	if err != nil {
		t.Fatalf("GetTokens: %v", err)
	}
	if got.AccessToken != plainAccess || got.RefreshToken != plainRefresh || !got.ExpiresAt.Equal(expires) {
		t.Fatalf("GetTokens = %+v", got)
	}

	// The raw row must hold ciphertext, never the plaintext.
	var rawAccess, rawRefresh []byte
	if err := db.QueryRowContext(ctx,
		`SELECT access_token_enc, refresh_token_enc FROM integration_connections WHERE id = $1`,
		conn.ID).Scan(&rawAccess, &rawRefresh); err != nil {
		t.Fatalf("raw row: %v", err)
	}
	if bytes.Contains(rawAccess, []byte(plainAccess)) || bytes.Equal(rawAccess, []byte(plainAccess)) {
		t.Fatal("access token stored in plaintext")
	}
	if bytes.Contains(rawRefresh, []byte(plainRefresh)) || bytes.Equal(rawRefresh, []byte(plainRefresh)) {
		t.Fatal("refresh token stored in plaintext")
	}
	if len(rawAccess) == 0 || len(rawRefresh) == 0 {
		t.Fatal("expected non-empty ciphertexts")
	}

	// SaveTokens on an unknown connection is ErrNotFound, and empty tokens
	// round-trip as empty (NULL at rest).
	if err := repo.SaveTokens(ctx, "missing", port.TokenSet{AccessToken: "x"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("SaveTokens(missing) = %v, want ErrNotFound", err)
	}
	if err := repo.SaveTokens(ctx, conn.ID, port.TokenSet{AccessToken: "only-access"}); err != nil {
		t.Fatal(err)
	}
	got, err = repo.GetTokens(ctx, conn.ID)
	if err != nil || got.RefreshToken != "" || !got.ExpiresAt.IsZero() {
		t.Fatalf("empty refresh/expiry round-trip = %+v, %v", got, err)
	}
}

func TestIntegrationRepoDeleteCascadesFromUser(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	repo := NewIntegrationRepo(st)
	seedUser(t, st, "u1")

	conn, err := repo.Create(ctx, domain.IntegrationConnection{UserID: "u1", Vendor: domain.IntegrationTodoist, Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = 'u1'`); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if _, err := repo.GetByID(ctx, conn.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("connection survived the user cascade: %v", err)
	}
}
