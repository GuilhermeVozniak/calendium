package postgres

import (
	"context"
	"database/sql"
	"time"

	"calendium/backend/internal/domain"
)

// --- port.AiJobRepo -----------------------------------------------------------

// Enqueue inserts the job; on (kind, thread_id) conflict — the partial
// unique index ai_jobs_dedup_idx — it resets run_after/locked_at so a
// message burst collapses into one fresh job. Jobs with a nil ThreadID
// (e.g. voice_profile) fall outside that index and always insert fresh.
//
// The DO UPDATE carries a `WHERE ai_jobs.locked_at IS NULL` guard so a job
// currently claimed by a worker (locked_at set) is left untouched: without
// it, a burst re-enqueue mid-run would unlock the claimed row underneath the
// worker, opening a window for a second worker to double-claim it (double
// budget charge, duplicate AI drafts) and for the original worker's Complete
// to then delete the re-armed row (losing the refresh). Against a locked
// row the insert becomes a no-op, same as ON CONFLICT DO NOTHING.
func (r aiJobRepo) Enqueue(ctx context.Context, j domain.AiJob) error {
	if j.ID == "" {
		j.ID = newID()
	}
	if j.RunAfter.IsZero() {
		j.RunAfter = time.Now().UTC()
	}
	payload, err := jsonObject(j.Payload)
	if err != nil {
		return err
	}
	_, err = r.q(ctx).ExecContext(ctx, `
		INSERT INTO ai_jobs (id, user_id, account_id, kind, thread_id, payload, run_after)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (kind, thread_id) WHERE thread_id IS NOT NULL
		DO UPDATE SET run_after = EXCLUDED.run_after, locked_at = NULL, payload = EXCLUDED.payload
		WHERE ai_jobs.locked_at IS NULL`,
		j.ID, j.UserID, j.AccountID, string(j.Kind), nullStrPtr(j.ThreadID), payload, j.RunAfter)
	return err
}

// ClaimDue atomically claims up to limit due jobs (run_after <= now,
// unlocked or lock expired >10m) with FOR UPDATE SKIP LOCKED, bumping
// attempts and locked_at. Safe under concurrent workers.
func (r aiJobRepo) ClaimDue(ctx context.Context, now time.Time, limit int) ([]domain.AiJob, error) {
	rows, err := r.q(ctx).QueryContext(ctx, `
		UPDATE ai_jobs SET locked_at = $1, attempts = attempts + 1
		WHERE id IN (
		    SELECT id FROM ai_jobs
		    WHERE run_after <= $1
		      AND (locked_at IS NULL OR locked_at < $1 - interval '10 minutes')
		    ORDER BY run_after
		    LIMIT $2
		    FOR UPDATE SKIP LOCKED
		)
		RETURNING id, user_id, account_id, kind, thread_id, payload, attempts, run_after`,
		now, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	jobs := []domain.AiJob{}
	for rows.Next() {
		j, err := scanAiJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// Complete deletes a finished job.
func (r aiJobRepo) Complete(ctx context.Context, id string) error {
	return mustAffect(r.q(ctx).ExecContext(ctx, `DELETE FROM ai_jobs WHERE id = $1`, id))
}

// Fail records errMsg and re-arms the job at retryAt, or dead-letters it
// (row deleted) when retryAt is nil.
func (r aiJobRepo) Fail(ctx context.Context, id string, retryAt *time.Time, errMsg string) error {
	if retryAt == nil {
		return mustAffect(r.q(ctx).ExecContext(ctx, `DELETE FROM ai_jobs WHERE id = $1`, id))
	}
	return mustAffect(r.q(ctx).ExecContext(ctx, `
		UPDATE ai_jobs SET run_after = $2, locked_at = NULL, last_error = $3
		WHERE id = $1`, id, *retryAt, errMsg))
}

func scanAiJob(r rowScanner) (domain.AiJob, error) {
	var j domain.AiJob
	var kind string
	var threadID sql.NullString
	var payload []byte
	if err := r.Scan(&j.ID, &j.UserID, &j.AccountID, &kind, &threadID, &payload, &j.Attempts, &j.RunAfter); err != nil {
		return domain.AiJob{}, notFound(err)
	}
	j.Kind = domain.AiJobKind(kind)
	j.ThreadID = strPtr(threadID)
	if err := unmarshalInto(payload, &j.Payload); err != nil {
		return domain.AiJob{}, err
	}
	if j.Payload == nil {
		j.Payload = map[string]string{}
	}
	return j, nil
}
