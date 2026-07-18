package postgres

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func TestAiJobRepoEnqueueDedupCollapse(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	th := seedThread(t, st, acct.ID, time.Now())
	threadID := th.ID
	now := time.Now().UTC().Truncate(time.Microsecond)

	if err := st.AiJobs().Enqueue(ctx, domain.AiJob{
		ID: "job1", UserID: "u1", AccountID: acct.ID, Kind: domain.AiJobThreadSummary,
		ThreadID: &threadID, RunAfter: now.Add(1 * time.Hour),
	}); err != nil {
		t.Fatalf("Enqueue 1: %v", err)
	}
	// A message burst re-enqueues the same (kind, thread): it must collapse
	// into the existing row, not create a second one, and refresh run_after.
	if err := st.AiJobs().Enqueue(ctx, domain.AiJob{
		ID: "job2", UserID: "u1", AccountID: acct.ID, Kind: domain.AiJobThreadSummary,
		ThreadID: &threadID, RunAfter: now.Add(-1 * time.Minute), Payload: map[string]string{"reason": "burst"},
	}); err != nil {
		t.Fatalf("Enqueue 2 (collapse): %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM ai_jobs WHERE thread_id = $1`, threadID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("ai_jobs rows for thread = %d, want 1 (dedup collapse)", count)
	}

	claimed, err := st.AiJobs().ClaimDue(ctx, now, 10)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("ClaimDue = %d jobs, want 1", len(claimed))
	}
	if claimed[0].ID != "job1" {
		t.Fatalf("claimed id = %s, want job1 (original row id preserved across collapse)", claimed[0].ID)
	}
	if claimed[0].Payload["reason"] != "burst" {
		t.Fatalf("claimed payload = %+v, want the refreshed payload from the collapse", claimed[0].Payload)
	}
}

func TestAiJobRepoClaimSkipsFreshLockReclaimsStale(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	now := time.Now().UTC().Truncate(time.Microsecond)

	if err := st.AiJobs().Enqueue(ctx, domain.AiJob{
		ID: "jobA", UserID: "u1", AccountID: acct.ID, Kind: domain.AiJobVoiceProfile, RunAfter: now.Add(-1 * time.Minute),
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Simulate another worker holding a fresh lock (< 10m old): must not be claimable.
	if _, err := db.ExecContext(ctx, `UPDATE ai_jobs SET locked_at = $1 WHERE id = 'jobA'`, now.Add(-1*time.Minute)); err != nil {
		t.Fatalf("seed fresh lock: %v", err)
	}
	claimed, err := st.AiJobs().ClaimDue(ctx, now, 10)
	if err != nil {
		t.Fatalf("ClaimDue (fresh lock): %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("ClaimDue with a fresh lock = %+v, want none claimed", claimed)
	}

	// Simulate a stale lock (> 10m old, e.g. a crashed worker): reclaimable.
	if _, err := db.ExecContext(ctx, `UPDATE ai_jobs SET locked_at = $1 WHERE id = 'jobA'`, now.Add(-15*time.Minute)); err != nil {
		t.Fatalf("seed stale lock: %v", err)
	}
	claimed, err = st.AiJobs().ClaimDue(ctx, now, 10)
	if err != nil {
		t.Fatalf("ClaimDue (stale lock): %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != "jobA" {
		t.Fatalf("ClaimDue with a stale lock = %+v, want [jobA] reclaimed", claimed)
	}
	if claimed[0].Attempts != 1 {
		t.Fatalf("Attempts after reclaim = %d, want 1 (bumped)", claimed[0].Attempts)
	}
}

func TestAiJobRepoNotYetDueIsNotClaimed(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	now := time.Now().UTC().Truncate(time.Microsecond)

	if err := st.AiJobs().Enqueue(ctx, domain.AiJob{
		ID: "future", UserID: "u1", AccountID: acct.ID, Kind: domain.AiJobVoiceProfile, RunAfter: now.Add(1 * time.Hour),
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	claimed, err := st.AiJobs().ClaimDue(ctx, now, 10)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("ClaimDue = %+v, want none (not yet due)", claimed)
	}
}

func TestAiJobRepoComplete(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	now := time.Now().UTC().Truncate(time.Microsecond)

	if err := st.AiJobs().Enqueue(ctx, domain.AiJob{
		ID: "jobC", UserID: "u1", AccountID: acct.ID, Kind: domain.AiJobVoiceProfile, RunAfter: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := st.AiJobs().Complete(ctx, "jobC"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	claimed, err := st.AiJobs().ClaimDue(ctx, now, 10)
	if err != nil {
		t.Fatalf("ClaimDue after complete: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("ClaimDue after Complete = %+v, want none", claimed)
	}
	if err := st.AiJobs().Complete(ctx, "jobC"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Complete again: err = %v, want ErrNotFound", err)
	}
}

func TestAiJobRepoFailRearmAndDeadLetter(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	now := time.Now().UTC().Truncate(time.Microsecond)

	if err := st.AiJobs().Enqueue(ctx, domain.AiJob{
		ID: "jobF", UserID: "u1", AccountID: acct.ID, Kind: domain.AiJobVoiceProfile, RunAfter: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := st.AiJobs().ClaimDue(ctx, now, 10); err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}

	retryAt := now.Add(30 * time.Minute)
	if err := st.AiJobs().Fail(ctx, "jobF", &retryAt, "rate limited"); err != nil {
		t.Fatalf("Fail (rearm): %v", err)
	}
	var runAfter time.Time
	var lockedAt sql.NullTime
	var lastErr string
	if err := db.QueryRowContext(ctx, `SELECT run_after, locked_at, last_error FROM ai_jobs WHERE id = 'jobF'`).
		Scan(&runAfter, &lockedAt, &lastErr); err != nil {
		t.Fatalf("select after Fail: %v", err)
	}
	if !runAfter.Equal(retryAt) {
		t.Fatalf("run_after = %v, want %v", runAfter, retryAt)
	}
	if lockedAt.Valid {
		t.Fatalf("locked_at = %v, want nil (re-armed)", lockedAt.Time)
	}
	if lastErr != "rate limited" {
		t.Fatalf("last_error = %q, want %q", lastErr, "rate limited")
	}

	// retryAt = nil dead-letters: the row is deleted.
	if err := st.AiJobs().Fail(ctx, "jobF", nil, "giving up"); err != nil {
		t.Fatalf("Fail (dead-letter): %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM ai_jobs WHERE id = 'jobF'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("ai_jobs rows for jobF after dead-letter = %d, want 0", count)
	}

	if err := st.AiJobs().Fail(ctx, "nope", nil, "x"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Fail unknown: err = %v, want ErrNotFound", err)
	}
}

// TestAiJobRepoClaimDueConcurrent genuinely exercises FOR UPDATE SKIP LOCKED
// under concurrency: many goroutines race to claim from the same due queue,
// each issuing its own single-statement ClaimDue against the shared
// connection pool. The claimed sets must be disjoint (no job claimed twice)
// and, since the queue holds exactly workers*perWorker jobs, every job must
// end up claimed exactly once.
func TestAiJobRepoClaimDueConcurrent(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	now := time.Now().UTC().Truncate(time.Microsecond)

	const workers = 8
	const perWorker = 5
	const total = workers * perWorker

	seeded := make(map[string]bool, total)
	for i := 0; i < total; i++ {
		id := newID()
		seeded[id] = true
		if err := st.AiJobs().Enqueue(ctx, domain.AiJob{
			ID: id, UserID: "u1", AccountID: acct.ID, Kind: domain.AiJobVoiceProfile,
			RunAfter: now.Add(-time.Minute),
		}); err != nil {
			t.Fatalf("seed job %d: %v", i, err)
		}
	}

	var wg sync.WaitGroup
	results := make([][]domain.AiJob, workers)
	errs := make([]error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			results[w], errs[w] = st.AiJobs().ClaimDue(ctx, now, perWorker)
		}(w)
	}
	wg.Wait()

	claimedIDs := map[string]int{}
	for w, err := range errs {
		if err != nil {
			t.Fatalf("worker %d ClaimDue: %v", w, err)
		}
		for _, j := range results[w] {
			claimedIDs[j.ID]++
		}
	}

	if len(claimedIDs) != total {
		missing := []string{}
		for id := range seeded {
			if claimedIDs[id] == 0 {
				missing = append(missing, id)
			}
		}
		sort.Strings(missing)
		t.Fatalf("claimed %d distinct jobs, want %d; missing=%v", len(claimedIDs), total, missing)
	}
	for id, n := range claimedIDs {
		if n != 1 {
			t.Fatalf("job %s claimed %d times, want exactly 1 (disjoint claim violated)", id, n)
		}
	}
}
