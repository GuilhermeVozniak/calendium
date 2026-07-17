package postgres

import (
	"context"
	"reflect"
	"testing"

	"calendium/backend/internal/domain"
)

func TestPrefsRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)

	// Seed a user.
	user := seedUser(t, store, "u1")

	// Absent prefs come back as the zero value.
	got, err := store.Prefs().Get(ctx, user.ID)
	if err != nil {
		t.Fatalf("Get(empty): %v", err)
	}
	if len(got.SplitOrder) != 0 {
		t.Fatalf("SplitOrder = %v, want empty", got.SplitOrder)
	}

	// Save new prefs.
	want := domain.UserPrefs{SplitOrder: []domain.InboxSplit{domain.SplitVIP, domain.SplitImportant, domain.SplitOther}}
	if err := store.Prefs().Save(ctx, user.ID, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Get returns saved prefs.
	got, err = store.Prefs().Get(ctx, user.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got = %+v, want %+v", got, want)
	}

	// Overwrite prefs.
	want2 := domain.UserPrefs{SplitOrder: []domain.InboxSplit{domain.SplitOther}}
	if err := store.Prefs().Save(ctx, user.ID, want2); err != nil {
		t.Fatalf("Save overwrite: %v", err)
	}

	// Get returns updated prefs.
	got, err = store.Prefs().Get(ctx, user.ID)
	if err != nil {
		t.Fatalf("Get after overwrite: %v", err)
	}
	if !reflect.DeepEqual(got, want2) {
		t.Fatalf("got = %+v, want %+v", got, want2)
	}
}
