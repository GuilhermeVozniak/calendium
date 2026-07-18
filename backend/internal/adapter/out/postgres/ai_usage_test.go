package postgres

import (
	"context"
	"testing"
	"time"
)

func TestAiUsageRepoIncrementAndCheck(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	day := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	// Under the limit: allowed and the counter advances.
	allowed, err := st.AiUsage().IncrementAndCheck(ctx, "u1", day, 2)
	if err != nil {
		t.Fatalf("IncrementAndCheck 1: %v", err)
	}
	if !allowed {
		t.Fatal("call 1 (calls=1, limit=2) = false, want true")
	}
	allowed, err = st.AiUsage().IncrementAndCheck(ctx, "u1", day, 2)
	if err != nil {
		t.Fatalf("IncrementAndCheck 2: %v", err)
	}
	if !allowed {
		t.Fatal("call 2 (calls=2, limit=2) = false, want true")
	}

	// At the limit: refused, counter left unchanged.
	allowed, err = st.AiUsage().IncrementAndCheck(ctx, "u1", day, 2)
	if err != nil {
		t.Fatalf("IncrementAndCheck 3: %v", err)
	}
	if allowed {
		t.Fatal("call 3 (calls already at limit 2) = true, want false")
	}
	stillOverLimit, err := st.AiUsage().IncrementAndCheck(ctx, "u1", day, 2)
	if err != nil {
		t.Fatalf("IncrementAndCheck 4: %v", err)
	}
	if stillOverLimit {
		t.Fatal("call 4 (still at limit) = true, want false — counter must stay unchanged, not creep past limit")
	}

	// A new day resets the counter.
	nextDay := day.Add(24 * time.Hour)
	allowedNextDay, err := st.AiUsage().IncrementAndCheck(ctx, "u1", nextDay, 2)
	if err != nil {
		t.Fatalf("IncrementAndCheck next day: %v", err)
	}
	if !allowedNextDay {
		t.Fatal("first call on a new day = false, want true (counter should have reset)")
	}
}

func TestAiUsageRepoPerUserIsolation(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	day := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	if _, err := st.AiUsage().IncrementAndCheck(ctx, "u1", day, 1); err != nil {
		t.Fatalf("u1 call: %v", err)
	}
	allowed, err := st.AiUsage().IncrementAndCheck(ctx, "u2", day, 1)
	if err != nil {
		t.Fatalf("u2 call: %v", err)
	}
	if !allowed {
		t.Fatal("u2's first call must not be affected by u1 reaching its own limit")
	}
}
