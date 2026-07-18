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

// autoDraftAvailabilityWindow / autoDraftSlotDuration / autoDraftMaxSlots
// bound the scheduling-draft availability lookup: the next 5 days, in
// 30-minute slots, offering at most the first 6 free windows to the model.
const (
	autoDraftAvailabilityWindow = 5 * 24 * time.Hour
	autoDraftSlotDuration       = 30 * time.Minute
	autoDraftMaxSlots           = 6
)

// runAutoDraft drafts (or refreshes) a provisional reply to a thread awaiting
// a response from the owner. It never sends: the draft it writes always has
// a nil ScheduledAt, and a user who has already touched the draft (MailService
// .UpdateDraft clears AiGenerated on any manual save) is never overwritten —
// GetAiGeneratedByThread only surfaces drafts still owned by the AI.
func (s *AIJobService) runAutoDraft(ctx context.Context, j domain.AiJob) error {
	th, err := s.threadFor(ctx, j)
	if err != nil {
		return err
	}
	acct, err := s.d.Accounts.GetByID(ctx, j.AccountID)
	if err != nil {
		return err
	}
	msgs, err := s.d.Messages.ListByThread(ctx, th.ID)
	if err != nil {
		return err
	}
	if len(msgs) == 0 {
		return nil // nothing to reply to
	}
	newest := msgs[0]
	for _, m := range msgs[1:] {
		if m.SentAt.After(newest.SentAt) {
			newest = m
		}
	}
	if strings.EqualFold(newest.From.Email, acct.Email) {
		return nil // the owner sent the newest message; nothing awaits a reply
	}
	if last, ok := j.Payload["lastMessageId"]; ok && last == newest.ID {
		return nil // this exact message was already drafted against
	}

	var out autoDraftOut
	if _, err := s.completeJSONBudgeted(ctx, j.UserID, autoDraftSystem, autoDraftUserPrompt(th, msgs, ""), &out); err != nil {
		return err
	}
	if !out.ShouldDraft {
		return nil // no reply expected from the owner
	}
	if out.IsMeetingRequest {
		avail := s.renderAvailability(ctx, j.UserID)
		if _, err := s.completeJSONBudgeted(ctx, j.UserID, autoDraftSystem, autoDraftUserPrompt(th, msgs, avail), &out); err != nil {
			return err
		}
	}

	subject := out.Subject
	if th.Subject != "" {
		subject = "Re: " + th.Subject
	}

	existing, err := s.d.Drafts.GetAiGeneratedByThread(ctx, th.ID)
	switch {
	case err == nil:
		// Refresh the still-AI-owned draft for the new message; a
		// user-touched draft never reaches here (GetAiGeneratedByThread
		// only returns drafts where ai_generated is still true).
		existing.To = []domain.EmailAddress{newest.From}
		existing.Subject = subject
		existing.BodyHTML = out.BodyHTML
		existing.AiGenerated = true
		existing.UpdatedAt = s.d.Clock.Now()
		return s.d.Drafts.Update(ctx, existing)
	case errors.Is(err, domain.ErrNotFound):
		_, cerr := s.d.Drafts.Create(ctx, domain.Draft{
			ID:          newID(),
			AccountID:   j.AccountID,
			ThreadID:    &th.ID,
			To:          []domain.EmailAddress{newest.From},
			Subject:     subject,
			BodyHTML:    out.BodyHTML,
			AiGenerated: true,
			UpdatedAt:   s.d.Clock.Now(),
		})
		return cerr
	default:
		return err
	}
}

// autoDraftUserPrompt renders the conversation (subject + last
// aiContextMessages messages, same context budget as AIService.Compose) plus
// an optional trailing AVAILABILITY block for the CompleteJSON user turn.
func autoDraftUserPrompt(t domain.Thread, msgs []domain.Message, availability string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Conversation subject: %s\n\n", t.Subject)
	start := 0
	if len(msgs) > aiContextMessages {
		start = len(msgs) - aiContextMessages
	}
	for _, m := range msgs[start:] {
		body := m.BodyText
		if body == "" {
			body = m.BodyHTML
		}
		fmt.Fprintf(&b, "From %s at %s:\n%s\n\n", m.From.Email, m.SentAt.Format(time.RFC3339), truncate(body, aiContextBodyMax))
	}
	if availability != "" {
		b.WriteString(availability)
	}
	return strings.TrimSpace(b.String())
}

// renderAvailability computes the owner's next-5-days availability and
// formats up to the first 6 slots, in the account's primary-calendar time
// zone, as an "AVAILABILITY:" block. Any failure (no Calendar wired, repo
// error, unknown zone) degrades to an empty string rather than failing the
// job — a scheduling draft without offered times still gets drafted.
func (s *AIJobService) renderAvailability(ctx context.Context, userID string) string {
	if s.d.Calendar == nil {
		return ""
	}
	now := s.d.Clock.Now()
	slots, err := s.d.Calendar.Availability(ctx, userID, now, now.Add(autoDraftAvailabilityWindow), autoDraftSlotDuration)
	if err != nil || len(slots) == 0 {
		return ""
	}
	loc := s.accountZone(ctx, userID)
	n := len(slots)
	if n > autoDraftMaxSlots {
		n = autoDraftMaxSlots
	}
	var b strings.Builder
	b.WriteString("AVAILABILITY:\n")
	for _, sl := range slots[:n] {
		fmt.Fprintf(&b, "- %s to %s\n", sl.Start.In(loc).Format(time.RFC1123), sl.End.In(loc).Format(time.RFC1123))
	}
	return b.String()
}

// accountZone resolves the user's primary-calendar time zone (falling back
// to the first calendar, then UTC) for rendering availability slots in local
// time. Never errors: any failure degrades to time.UTC.
func (s *AIJobService) accountZone(ctx context.Context, userID string) *time.Location {
	cals, err := s.d.Calendar.ListCalendars(ctx, userID)
	if err != nil || len(cals) == 0 {
		return time.UTC
	}
	zone := cals[0].TimeZone
	for _, c := range cals {
		if c.IsPrimary {
			zone = c.TimeZone
			break
		}
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.UTC
	}
	return loc
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
