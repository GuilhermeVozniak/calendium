package postgres

import (
	"context"
	"testing"

	"calendium/backend/internal/port"
)

func TestUserPreferencesUpsert(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)

	user := seedUser(t, store, "u1")

	// Absent prefs default to the neutral theme (never ErrNotFound).
	got, err := store.UserPreferences().Get(ctx, user.ID)
	if err != nil {
		t.Fatalf("Get(empty): %v", err)
	}
	if got.Theme != port.DefaultTheme {
		t.Fatalf("Theme = %q, want %q", got.Theme, port.DefaultTheme)
	}

	// First Put inserts.
	if err := store.UserPreferences().Put(ctx, user.ID, port.UserPreferences{Theme: "ocean"}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err = store.UserPreferences().Get(ctx, user.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Theme != "ocean" {
		t.Fatalf("Theme = %q, want ocean", got.Theme)
	}

	// Second Put overwrites (upsert semantics).
	if err := store.UserPreferences().Put(ctx, user.ID, port.UserPreferences{Theme: "forest"}); err != nil {
		t.Fatalf("Put overwrite: %v", err)
	}
	got, err = store.UserPreferences().Get(ctx, user.ID)
	if err != nil {
		t.Fatalf("Get after overwrite: %v", err)
	}
	if got.Theme != "forest" {
		t.Fatalf("Theme = %q, want forest", got.Theme)
	}
}
