package service

import (
	"context"
	"errors"
	"testing"

	"calendium/backend/internal/domain"
)

func boolPtr(b bool) *bool { return &b }

func TestSettingsUpdatePreservesAIBackgroundWhenAbsent(t *testing.T) {
	ctx := context.Background()
	repo := newUserSettingsRepo()
	svc := NewSettingsService(repo)

	fresh, err := svc.Update(ctx, "u1", domain.UserSettings{TimeZone: "UTC"}, nil)
	if err != nil || !fresh.AIBackground {
		t.Fatalf("first Update = (%+v, %v), want AIBackground default true", fresh, err)
	}
	if _, err := svc.Update(ctx, "u1", domain.UserSettings{TimeZone: "UTC"}, boolPtr(false)); err != nil {
		t.Fatal(err)
	}
	// An older client PUTs the whole document without the field (decodes as false).
	after, err := svc.Update(ctx, "u1", domain.UserSettings{TimeZone: "Europe/Lisbon", AIBackground: false}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if after.AIBackground {
		t.Fatal("Update must not flip AIBackground back on")
	}
	if after.TimeZone != "Europe/Lisbon" {
		t.Fatalf("TimeZone = %q", after.TimeZone)
	}
	got, _ := svc.Get(ctx, "u1")
	if got.AIBackground {
		t.Fatal("stored AIBackground must stay false")
	}
}

func TestSettingsUpdateWithAIBackgroundReturnsDocument(t *testing.T) {
	ctx := context.Background()
	svc := NewSettingsService(newUserSettingsRepo())
	got, err := svc.Update(ctx, "u1", domain.UserSettings{TimeZone: "UTC"}, boolPtr(false))
	if err != nil {
		t.Fatal(err)
	}
	if got.AIBackground || got.TimeZone != "UTC" || got.WorkingHours == nil {
		t.Fatalf("Update(aiBackground=false) on a fresh user = %+v, want aiBackground=false with defaults and a non-nil WorkingHours", got)
	}
	got, _ = svc.Update(ctx, "u1", domain.UserSettings{TimeZone: "UTC"}, boolPtr(true))
	if !got.AIBackground {
		t.Fatal("Update(aiBackground=true) not reflected")
	}
}

// The document and the switch are one repo write: a failing write leaves
// neither the time zone nor the switch changed (no half-applied PUT).
func TestSettingsUpdateIsOneAtomicWrite(t *testing.T) {
	ctx := context.Background()
	repo := newUserSettingsRepo()
	svc := NewSettingsService(repo)
	if _, err := svc.Update(ctx, "u1", domain.UserSettings{TimeZone: "UTC"}, nil); err != nil {
		t.Fatal(err)
	}
	before := repo.saveCalls
	if _, err := svc.Update(ctx, "u1", domain.UserSettings{TimeZone: "Europe/Lisbon"}, boolPtr(false)); err != nil {
		t.Fatal(err)
	}
	if repo.saveCalls-before != 1 {
		t.Fatalf("repo writes = %d, want exactly 1 (time zone and switch together)", repo.saveCalls-before)
	}
	repo.saveErr = errors.New("db down")
	if _, err := svc.Update(ctx, "u1", domain.UserSettings{TimeZone: "Asia/Tokyo"}, boolPtr(true)); err == nil {
		t.Fatal("want the write error")
	}
	got, _ := svc.Get(ctx, "u1")
	if got.TimeZone != "Europe/Lisbon" || got.AIBackground {
		t.Fatalf("after a failed write = %+v, want the previous document untouched", got)
	}
}
