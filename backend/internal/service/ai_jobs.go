// Package service (this file): AIJobService drains the background AI job
// queue (port.AiJobRepo), consumed by cmd/worker's third loop. It enforces
// the per-user daily AI budget at the point of the actual LLM call (see
// completeJSONBudgeted), dispatches to per-kind handlers, and retries
// failures with capped exponential backoff before dead-lettering (see
// classifyJobError).
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

const (
	aiJobBatch       = 20
	aiJobMaxAttempts = 4
)

// errAIBudgetExhausted is returned by completeJSONBudgeted when the caller's
// daily AI usage budget is exhausted. classifyJobError maps it to a rearm at
// the next UTC midnight instead of the generic backoff/dead-letter path:
// exhausting the daily cap is an expected, account-wide condition (nothing
// wrong with this particular job), not a per-job defect worth escalating
// toward dead-lettering.
var errAIBudgetExhausted = errors.New("daily ai budget exhausted")

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

// runJob executes one claimed job: dispatch, then retry/backoff/dead-letter
// per classifyJobError. There is no budget check here anymore — the guard
// fetches inside dispatch's handlers (threadFor et al.) run first and can
// drop a job (ErrNotFound) before any quota is spent; the budget is only
// charged once a handler actually reaches completeJSONBudgeted for a real
// LLM attempt.
func (s *AIJobService) runJob(ctx context.Context, j domain.AiJob) error {
	err := s.dispatch(ctx, j)
	if err == nil {
		return s.d.Jobs.Complete(ctx, j.ID)
	}
	retryAt, deadLetter, drop := classifyJobError(err, j.Attempts, s.d.Clock.Now())
	switch {
	case drop:
		// Thread/draft vanished under the job: drop it, not an error.
		return s.d.Jobs.Complete(ctx, j.ID)
	case deadLetter:
		if ferr := s.d.Jobs.Fail(ctx, j.ID, nil, err.Error()); ferr != nil {
			return errors.Join(err, ferr)
		}
		return fmt.Errorf("dead-lettered after %d attempts: %w", j.Attempts, err)
	default:
		if ferr := s.d.Jobs.Fail(ctx, j.ID, retryAt, err.Error()); ferr != nil {
			return errors.Join(err, ferr)
		}
		return err
	}
}

// classifyJobError maps a dispatch error to runJob's retry/dead-letter
// decision. It is a pure function of (err, attempts, now), kept separate
// from runJob so every branch — including ones the current stub handlers
// can't yet produce, like errAIBudgetExhausted — can be table-tested without
// driving a job through dispatch.
//
//   - domain.ErrNotFound (thread/draft vanished under the job): drop=true,
//     the job is completed rather than retried or failed.
//   - errAIBudgetExhausted: retryAt is the next UTC midnight, checked before
//     the attempts cap so it NEVER dead-letters no matter how many attempts
//     the job has racked up — an exhausted daily cap is an account-wide
//     condition, not a defect in this job.
//   - domain.ErrRateLimited / domain.ErrAIUnavailable (and anything wrapping
//     them, via errors.Is): flat 15m rearm regardless of attempts, also
//     checked before the attempts cap so provider outages never dead-letter
//     either. Trade-off: retries for these two sentinels are unbounded — a
//     job stuck behind a *permanent* misconfiguration that happens to
//     surface as one of them (rather than a generic error) would retry
//     forever every 15m instead of dead-lettering. In practice, auth/config
//     failures (e.g. a bad or revoked API key, 401-class responses) are
//     expected to come back as a generic error from the AI adapter, not
//     wrapped in ErrRateLimited/ErrAIUnavailable, so they fall through to
//     the branch below and DO dead-letter normally.
//   - everything else: capped exponential backoff (aiJobBackoff), dead-
//     lettering once attempts reaches aiJobMaxAttempts.
func classifyJobError(err error, attempts int, now time.Time) (retryAt *time.Time, deadLetter bool, drop bool) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return nil, false, true
	case errors.Is(err, errAIBudgetExhausted):
		next := now.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
		return &next, false, false
	case errors.Is(err, domain.ErrRateLimited), errors.Is(err, domain.ErrAIUnavailable):
		retry := now.Add(15 * time.Minute)
		return &retry, false, false
	case attempts >= aiJobMaxAttempts:
		return nil, true, false
	default:
		retry := now.Add(aiJobBackoff(attempts))
		return &retry, false, false
	}
}

// completeJSONBudgeted is THE choke point every job handler that calls the
// LLM must go through (Tasks 6-14 wire their real generation through this,
// not s.d.AI.CompleteJSON directly): it charges the caller's daily AI usage
// budget via Usage.IncrementAndCheck and only proceeds to s.d.AI.CompleteJSON
// once the budget allows it. Handlers must run their own existence/guard
// fetches (e.g. threadFor) BEFORE calling this, so a job whose target
// vanished (domain.ErrNotFound) is dropped without ever reaching here and
// without spending any quota. On an exhausted budget this returns
// errAIBudgetExhausted, which classifyJobError recognizes and rearms at the
// next UTC midnight instead of the generic backoff/dead-letter path.
func (s *AIJobService) completeJSONBudgeted(ctx context.Context, userID, system, user string, out any) (string, error) {
	allowed, err := s.d.Usage.IncrementAndCheck(ctx, userID, s.d.Clock.Now(), s.d.DailyLimit)
	if err != nil {
		return "", err
	}
	if !allowed {
		return "", errAIBudgetExhausted
	}
	return s.d.AI.CompleteJSON(ctx, system, user, out)
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

// summaryMaxLen caps the persisted thread summary length. The prompt asks
// for <=120 chars, but the model isn't a reliable enforcer of its own
// instructions, so this is a hard backstop before persistence.
const summaryMaxLen = 200

// runThreadSummary generates (or refreshes) a thread's live summary: guard-
// fetch the thread (dropping the job via ErrNotFound if it's gone), load its
// messages, ask the model for a single-line summary via
// completeJSONBudgeted, validate it's non-empty (an empty summary is a
// model/prompt defect worth retrying, not a reason to persist blank text),
// clamp it to summaryMaxLen, and persist via Threads.SetSummary. Because
// ingest re-enqueues this job kind on every new inbound message and the
// ai_jobs dedup index collapses bursts, the summary stays live as messages
// arrive.
func (s *AIJobService) runThreadSummary(ctx context.Context, j domain.AiJob) error {
	t, err := s.threadFor(ctx, j)
	if err != nil {
		return err
	}
	msgs, err := s.d.Messages.ListByThread(ctx, t.ID)
	if err != nil {
		return err
	}

	var out summaryOut
	if _, err := s.completeJSONBudgeted(ctx, j.UserID, summarySystem, threadContext(t, msgs), &out); err != nil {
		return err
	}

	summary := strings.TrimSpace(out.Summary)
	if summary == "" {
		return fmt.Errorf("%w: thread_summary: model returned an empty summary", domain.ErrAIOutput)
	}
	summary = truncate(summary, summaryMaxLen)

	return s.d.Threads.SetSummary(ctx, t.ID, summary, s.d.Clock.Now())
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

// runReminderDetect judges whether the owner's sent mail (Task 5 enqueues
// this job 24h after send, via payload["sentAt"] in RFC3339) still awaits a
// reply from the recipient, and arms a follow-up reminder when it does.
//
// Guard order matters: both the inbound-reply check and the user-reminder
// check return before completeJSONBudgeted, so neither spends any AI
// budget — only a genuine "still no reply, no user reminder set" case
// reaches the LLM.
func (s *AIJobService) runReminderDetect(ctx context.Context, j domain.AiJob) error {
	t, err := s.threadFor(ctx, j)
	if err != nil {
		return err
	}
	// A user-set reminder is never overwritten by the AI heuristic.
	if t.RemindAt != nil {
		return nil
	}

	sentAt, err := reminderSentAt(j.Payload)
	if err != nil {
		return err
	}

	acct, err := s.d.Accounts.GetByID(ctx, t.AccountID)
	if err != nil {
		return err
	}

	msgs, err := s.d.Messages.ListByThread(ctx, t.ID)
	if err != nil {
		return err
	}
	if reminderHasInboundReplyAfter(msgs, acct.Email, sentAt) {
		return nil // recipient already answered; nothing to remind about
	}

	var out reminderOut
	if _, err := s.completeJSONBudgeted(ctx, j.UserID, reminderSystem, reminderUserPrompt(t.Subject, t.Snippet), &out); err != nil {
		return err
	}
	if !out.AwaitingReply {
		return nil
	}

	days := out.RemindDays
	if days < 1 || days > 14 {
		days = 3
	}
	remindAt := sentAt.AddDate(0, 0, days)
	if now := s.d.Clock.Now(); !remindAt.After(now) {
		// Target already elapsed (e.g. a long-delayed job run, or the model
		// picking a short horizon on an old sentAt): still give the thread a
		// fresh, forward-looking reminder rather than one that fires
		// immediately (or never, if a scheduler skips past-due arms).
		remindAt = now.Add(24 * time.Hour)
	}
	return s.d.Threads.SetReminderIfUnset(ctx, t.ID, remindAt)
}

// reminderSentAt parses the RFC3339 payload["sentAt"] set by sync.go's send
// path when it enqueues a reminder_detect job.
func reminderSentAt(payload map[string]string) (time.Time, error) {
	raw := payload["sentAt"]
	sentAt, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: reminder_detect job payload[sentAt] %q: %v", domain.ErrValidation, raw, err)
	}
	return sentAt, nil
}

// reminderHasInboundReplyAfter reports whether any message in msgs is from
// someone other than the account owner and was sent after sentAt — the
// recipient answering, which makes an AI-judged reminder moot. This mirrors
// hasNewInboundReply's owner-vs-recipient direction check (sync.go) but over
// the mirrored domain.Message list a job handler has on hand, rather than
// the raw sync-page IncomingMessage batch.
func reminderHasInboundReplyAfter(msgs []domain.Message, ownerEmail string, sentAt time.Time) bool {
	owner := strings.ToLower(strings.TrimSpace(ownerEmail))
	for _, m := range msgs {
		from := strings.ToLower(strings.TrimSpace(m.From.Email))
		if from == "" || from == owner {
			continue
		}
		if m.SentAt.After(sentAt) {
			return true
		}
	}
	return false
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
//
// Idempotency: VoiceProfileRepo.Upsert overwrites the single per-user row,
// so re-running this handler for an account that already has a profile (a
// re-enqueued or duplicated job) just refreshes it cleanly — no error, no
// duplicate rows.
func (s *AIJobService) runVoiceProfile(ctx context.Context, j domain.AiJob) error {
	acct, err := s.d.Accounts.GetByID(ctx, j.AccountID)
	if err != nil {
		return err // domain.ErrNotFound (account deleted) drops the job
	}
	msgs, err := s.d.Messages.ListSentByAccount(ctx, j.AccountID, acct.Email, voiceSampleCount)
	if err != nil {
		return err
	}
	if len(msgs) < voiceMinSamples {
		// Not enough sent mail yet to learn a style from. The self
		// re-enqueue only happens after a successful profile generation, so
		// this relies on the 30-day re-enqueue scheduled at first sync (or
		// the next duplicate job) to retry later.
		return nil
	}

	var b strings.Builder
	for _, m := range msgs {
		fmt.Fprintf(&b, "---\n%s\n", firstNonEmpty(m.BodyText, m.BodyHTML))
	}

	var out voiceProfileOut
	model, err := s.completeJSONBudgeted(ctx, j.UserID, voiceProfileSystem, b.String(), &out)
	if err != nil {
		return err
	}

	if err := s.d.VoiceProfiles.Upsert(ctx, domain.VoiceProfile{
		UserID:      j.UserID,
		Profile:     out.Profile,
		SampleCount: len(msgs),
		Model:       model,
		UpdatedAt:   s.d.Clock.Now(),
	}); err != nil {
		return err
	}

	return s.d.Jobs.Enqueue(ctx, domain.AiJob{
		ID:        newID(),
		UserID:    j.UserID,
		AccountID: j.AccountID,
		Kind:      domain.AiJobVoiceProfile,
		ThreadID:  nil,
		Payload:   map[string]string{},
		RunAfter:  s.d.Clock.Now().Add(30 * 24 * time.Hour),
	})
}
