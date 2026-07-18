// Package service (this file): AIJobService drains the background AI job
// queue (port.AiJobRepo), consumed by cmd/worker's third loop. It enforces
// the per-user daily AI budget, dispatches to per-kind handlers, and retries
// failures with capped exponential backoff before dead-lettering.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

const (
	aiJobBatch       = 20
	aiJobMaxAttempts = 4
)

// AIJobServiceDeps wires AIJobService. AI nil disables the whole service
// (graceful degradation when OPENROUTER_API_KEY is unset): ProcessDueAiJobs
// becomes a no-op so cmd/worker simply doesn't start the AI loop.
type AIJobServiceDeps struct {
	Jobs          port.AiJobRepo
	Usage         port.AiUsageRepo
	Accounts      port.AccountRepo
	Threads       port.ThreadRepo
	Messages      port.MessageRepo
	Drafts        port.DraftRepo
	Labels        port.LabelRepo
	Classifiers   port.ClassifierRepo
	VoiceProfiles port.VoiceProfileRepo
	Calendar      port.CalendarService // availability for scheduling drafts / event proposals
	AI            port.AI              // nil disables the whole service
	Clock         port.Clock
	DailyLimit    int // AI_DAILY_LIMIT, default 300
	Logger        *slog.Logger
}

// AIJobService is the worker-side consumer of the ai_jobs queue.
type AIJobService struct {
	d AIJobServiceDeps
}

var _ port.AIJobService = (*AIJobService)(nil)

// NewAIJobService builds an AIJobService, defaulting DailyLimit to 300 when
// unset or non-positive (defense in depth alongside config.FromEnv's own
// AI_DAILY_LIMIT default/validation).
func NewAIJobService(d AIJobServiceDeps) *AIJobService {
	if d.DailyLimit <= 0 {
		d.DailyLimit = 300
	}
	return &AIJobService{d: d}
}

// ProcessDueAiJobs claims and executes one batch of due AI jobs. It is a
// no-op returning nil when AI is not configured.
func (s *AIJobService) ProcessDueAiJobs(ctx context.Context) error {
	if s.d.AI == nil {
		return nil // graceful degradation: no key, no work
	}
	now := s.d.Clock.Now()
	jobs, err := s.d.Jobs.ClaimDue(ctx, now, aiJobBatch)
	if err != nil {
		return err
	}
	var errs []error
	for _, j := range jobs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.runJob(ctx, j); err != nil {
			errs = append(errs, fmt.Errorf("ai job %s (%s): %w", j.ID, j.Kind, err))
		}
	}
	return errors.Join(errs...)
}

// runJob executes one claimed job: budget check, dispatch, retry/backoff.
func (s *AIJobService) runJob(ctx context.Context, j domain.AiJob) error {
	allowed, err := s.d.Usage.IncrementAndCheck(ctx, j.UserID, s.d.Clock.Now(), s.d.DailyLimit)
	if err != nil {
		return err
	}
	if !allowed {
		// Budget exhausted: park until next UTC midnight, don't count attempt.
		next := s.d.Clock.Now().UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
		return s.d.Jobs.Fail(ctx, j.ID, &next, "daily ai budget exhausted")
	}
	err = s.dispatch(ctx, j)
	switch {
	case err == nil:
		return s.d.Jobs.Complete(ctx, j.ID)
	case errors.Is(err, domain.ErrNotFound):
		// Thread/draft vanished under the job: drop it, not an error.
		return s.d.Jobs.Complete(ctx, j.ID)
	case j.Attempts >= aiJobMaxAttempts:
		if ferr := s.d.Jobs.Fail(ctx, j.ID, nil, err.Error()); ferr != nil {
			return errors.Join(err, ferr)
		}
		return fmt.Errorf("dead-lettered after %d attempts: %w", j.Attempts, err)
	default:
		retry := s.d.Clock.Now().Add(aiJobBackoff(j.Attempts))
		if ferr := s.d.Jobs.Fail(ctx, j.ID, &retry, err.Error()); ferr != nil {
			return errors.Join(err, ferr)
		}
		return err
	}
}

func (s *AIJobService) dispatch(ctx context.Context, j domain.AiJob) error {
	switch j.Kind {
	case domain.AiJobThreadSummary:
		return s.runThreadSummary(ctx, j)
	case domain.AiJobInstantReplies:
		return s.runInstantReplies(ctx, j)
	case domain.AiJobAutoDraft:
		return s.runAutoDraft(ctx, j)
	case domain.AiJobClassify:
		return s.runClassify(ctx, j)
	case domain.AiJobReminderDetect:
		return s.runReminderDetect(ctx, j)
	case domain.AiJobVoiceProfile:
		return s.runVoiceProfile(ctx, j)
	default:
		return fmt.Errorf("unknown ai job kind %q", j.Kind)
	}
}

// aiJobBackoff: 1m, 4m, 16m, capped at 30m.
func aiJobBackoff(attempt int) time.Duration {
	d := time.Minute << uint(2*(attempt-1))
	if d <= 0 || d > 30*time.Minute {
		return 30 * time.Minute
	}
	return d
}

// threadFor resolves the job's thread, the shared guard for every
// thread-scoped job kind: it lets a job for an archived/deleted thread be
// dropped via the domain.ErrNotFound branch in runJob instead of retried
// forever. j.ThreadID is nil only for AiJobVoiceProfile, which does not call
// this helper.
func (s *AIJobService) threadFor(ctx context.Context, j domain.AiJob) (domain.Thread, error) {
	if j.ThreadID == nil {
		return domain.Thread{}, fmt.Errorf("%w: ai job %s missing thread_id", domain.ErrValidation, j.Kind)
	}
	return s.d.Threads.GetByID(ctx, *j.ThreadID)
}

// runThreadSummary is a stub in this task (Task 6 fills in real summary
// generation + Threads.SetSummary); it only performs the existence guard so
// ProcessDueAiJobs already drops jobs for since-deleted threads.
func (s *AIJobService) runThreadSummary(ctx context.Context, j domain.AiJob) error {
	_, err := s.threadFor(ctx, j)
	return err
}

// runInstantReplies is a stub in this task; Task 7 fills in real generation.
func (s *AIJobService) runInstantReplies(ctx context.Context, j domain.AiJob) error {
	_, err := s.threadFor(ctx, j)
	return err
}

// runAutoDraft is a stub in this task; Task 8 fills in real generation.
func (s *AIJobService) runAutoDraft(ctx context.Context, j domain.AiJob) error {
	_, err := s.threadFor(ctx, j)
	return err
}

// runClassify is a stub in this task; Task 9 fills in real classification.
func (s *AIJobService) runClassify(ctx context.Context, j domain.AiJob) error {
	_, err := s.threadFor(ctx, j)
	return err
}

// runReminderDetect is a stub in this task; Task 10 fills in real detection.
func (s *AIJobService) runReminderDetect(ctx context.Context, j domain.AiJob) error {
	_, err := s.threadFor(ctx, j)
	return err
}

// runVoiceProfile is a stub in this task; Task 11 fills in real learning.
// Unlike the other kinds it carries no ThreadID (voice_profile jobs are
// account/user-scoped), so there is no existence guard to run here.
//
// KNOWN GAP (carried from Task 3): the ai_jobs dedup unique index only
// applies when thread_id IS NOT NULL, so repeated Enqueue calls for
// voice_profile (nil ThreadID) can accumulate duplicate rows instead of
// collapsing like thread-scoped kinds do. This task does not add an
// existence-check guard here because the real handler (Task 11) is expected
// to be an idempotent upsert (VoiceProfileRepo.Upsert overwrites the single
// per-user row), so processing a duplicate job twice is wasted work, not a
// correctness bug. If duplicate-job volume becomes a problem, the fix
// belongs on the enqueue side (a cheap "does a voice_profile job already
// exist for this user" check before Enqueue), not here.
func (s *AIJobService) runVoiceProfile(_ context.Context, _ domain.AiJob) error {
	return nil
}
