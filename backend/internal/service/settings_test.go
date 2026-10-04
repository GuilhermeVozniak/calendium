package service

import (
	"context"
	"testing"

	"calendium/backend/internal/domain"
)

func TestSettingsUpdatePreservesAIBackgroundWhenAbsent(t *testing.T) {
	ctx := context.Background()
	repo := newUserSettingsRepo()
	svc := NewSettingsService(repo)

	fresh, err := svc.Update(ctx, "u1", domain.UserSettings{TimeZone: "UTC"})
	if err != nil || !fresh.AIBackground {
		t.Fatalf("first Update = (%+v, %v), want AIBackground default true", fresh, err)
	}
	if _, err := svc.SetAIBackground(ctx, "u1", false); err != nil {
		t.Fatal(err)
	}
	// An older client PUTs the whole document without the field (decodes as false).
	after, err := svc.Update(ctx, "u1", domain.UserSettings{TimeZone: "Europe/Lisbon", AIBackground: false})
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

func TestSettingsSetAIBackgroundReturnsDocument(t *testing.T) {
	ctx := context.Background()
	svc := NewSettingsService(newUserSettingsRepo())
	got, err := svc.SetAIBackground(ctx, "u1", false)
	if err != nil {
		t.Fatal(err)
	}
	if got.AIBackground || got.TimeZone != "UTC" || got.WorkingHours == nil {
		t.Fatalf("SetAIBackground on a fresh user = %+v, want aiBackground=false with defaults and a non-nil WorkingHours", got)
	}
	got, _ = svc.SetAIBackground(ctx, "u1", true)
	if !got.AIBackground {
		t.Fatal("SetAIBackground(true) not reflected")
	}
}
