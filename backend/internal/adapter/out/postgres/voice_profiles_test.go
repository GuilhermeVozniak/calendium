package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func TestVoiceProfileRepoGetNotFound(t *testing.T) {
	st, _ := newTestStore(t)
	seedUser(t, st, "u1")

	if _, err := st.VoiceProfiles().Get(context.Background(), "u1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get before upsert: err = %v, want ErrNotFound", err)
	}
}

func TestVoiceProfileRepoUpsertRoundTrip(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := st.VoiceProfiles().Upsert(ctx, domain.VoiceProfile{
		UserID: "u1", Profile: "concise, friendly, no emoji", SampleCount: 12,
		Model: "openrouter/model-a", UpdatedAt: t0,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := st.VoiceProfiles().Get(ctx, "u1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Profile != "concise, friendly, no emoji" || got.SampleCount != 12 || got.Model != "openrouter/model-a" {
		t.Fatalf("Get = %+v", got)
	}
	if !got.UpdatedAt.Equal(t0) {
		t.Fatalf("UpdatedAt = %v, want %v", got.UpdatedAt, t0)
	}

	t1 := t0.Add(24 * time.Hour)
	if err := st.VoiceProfiles().Upsert(ctx, domain.VoiceProfile{
		UserID: "u1", Profile: "updated profile", SampleCount: 20,
		Model: "openrouter/model-b", UpdatedAt: t1,
	}); err != nil {
		t.Fatalf("Upsert overwrite: %v", err)
	}
	got2, err := st.VoiceProfiles().Get(ctx, "u1")
	if err != nil {
		t.Fatalf("Get after overwrite: %v", err)
	}
	if got2.Profile != "updated profile" || got2.SampleCount != 20 || got2.Model != "openrouter/model-b" {
		t.Fatalf("Get after overwrite = %+v", got2)
	}
	if !got2.UpdatedAt.Equal(t1) {
		t.Fatalf("UpdatedAt after overwrite = %v, want %v", got2.UpdatedAt, t1)
	}
}
