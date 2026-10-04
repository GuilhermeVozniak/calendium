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
	// UserSettings is optional: when set, every background job kind (see
	// isBackgroundAiJob) for a user whose Settings → AI → "Background AI
	// processing" switch is off is completed without calling the model (a
	// voice_profile job also without re-queueing itself). Fail-open on a
	// missing repo or a read error (same as SyncService).
	UserSettings port.UserSettingsRepo
	Calendar     port.CalendarService // availability for scheduling drafts / event proposals
	AI           port.AI              // nil disables the whole service
	Clock        port.Clock
	DailyLimit   int // AI_DAILY_LIMIT, default 300
	Logger       *slog.Logger
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
	if isBackgroundAiJob(j.Kind) && !s.backgroundAIAllowed(ctx, j.UserID) {
		// Settings → AI → "Background AI processing" is off: a job queued
		// before the switch flipped (reminder_detect runs up to 24 h later,
		// a first-sync backlog for hours) completes as skipped without
		// reaching its handler, so no thread is sent to the model and the
		// voice-profile chain does not re-queue itself.
		return s.d.Jobs.Complete(ctx, j.ID)
	}
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

// isBackgroundAiJob reports whether a queued kind runs without a direct
// user action and so obeys the background-AI switch at run time. Every
// current kind is enqueued by sync (or re-queued by itself); on-demand AI
// goes through AIService, not this queue. A future user-initiated kind
// returns false here.
func isBackgroundAiJob(kind domain.AiJobKind) bool {
	switch kind {
	case domain.AiJobThreadSummary, domain.AiJobInstantReplies, domain.AiJobAutoDraft,
		domain.AiJobClassify, domain.AiJobReminderDetect, domain.AiJobVoiceProfile:
		return true
	}
	return false
}

// backgroundAIAllowed reports the user's background-AI switch, fail-open on
// a missing repo or a read error (a transient settings read must not change
// behaviour for everyone).
func (s *AIJobService) backgroundAIAllowed(ctx context.Context, userID string) bool {
	if s.d.UserSettings == nil {
		return true
	}
	settings, err := s.d.UserSettings.Get(ctx, userID)
	if err != nil {
		return true
	}
	return settings.AIBackground
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

// runInstantReplies generates and persists the Instant Reply cache: 3 short,
// ready-to-send suggestions responding to the newest message in the thread
// (normalizeInstantReplies in ai_prompts.go validates/clamps the model's raw
// output). A malformed 0-reply response comes back wrapped in
// domain.ErrAIOutput, which classifyJobError has no special case for, so it
// takes the same generic backoff/dead-letter path as any other handler
// defect -- exactly the "retry error" this task's tests expect.
func (s *AIJobService) runInstantReplies(ctx context.Context, j domain.AiJob) error {
	t, err := s.threadFor(ctx, j)
	if err != nil {
		return err
	}
	msgs, err := s.d.Messages.ListByThread(ctx, t.ID)
	if err != nil {
		return err
	}
	var out instantRepliesOut
	if _, err := s.completeJSONBudgeted(ctx, j.UserID, instantRepliesSystem,
		instantRepliesUserPrompt(t.Subject, msgs), &out); err != nil {
		return err
	}
	replies, err := normalizeInstantReplies(out.Replies)
	if err != nil {
		return err
	}
	return s.d.Threads.SetInstantReplies(ctx, t.ID, replies, s.d.Clock.Now())
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
		subject = strings.TrimSpace(th.Subject)
		if !strings.HasPrefix(strings.ToLower(subject), "re:") {
			subject = "Re: " + subject
		}
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

// runClassify evaluates every enabled classifier the user has defined
// against the thread's newest message and, for genuine matches, routes the
// thread split and/or attaches a local per-classifier label. It costs no
// budget and makes no AI call when the user has no enabled classifiers.
func (s *AIJobService) runClassify(ctx context.Context, j domain.AiJob) error {
	t, err := s.threadFor(ctx, j)
	if err != nil {
		return err
	}
	classifiers, err := s.d.Classifiers.ListEnabledByUser(ctx, j.UserID)
	if err != nil {
		return err
	}
	if len(classifiers) == 0 {
		return nil // nothing to evaluate; complete without spending budget
	}
	msgs, err := s.d.Messages.ListByThread(ctx, t.ID)
	if err != nil {
		return err
	}
	if len(msgs) == 0 {
		return nil // thread has no messages yet (shouldn't happen at ingest, but not fatal)
	}
	newest := msgs[len(msgs)-1] // ListByThread preserves insertion order; the newest ingested message is last
	body := newest.BodyText
	if body == "" {
		body = newest.BodyHTML
	}
	user := fmt.Sprintf("Subject: %s\nFrom: %s\n\n%s", t.Subject, newest.From.Email, truncate(body, aiContextBodyMax))

	var out classifyOut
	if _, err := s.completeJSONBudgeted(ctx, j.UserID, buildClassifySystem(classifiers), user, &out); err != nil {
		return err
	}

	byID := make(map[string]domain.AiClassifier, len(classifiers))
	for _, c := range classifiers {
		byID[c.ID] = c
	}
	// Hallucinated/unknown ids the model returns are silently dropped rather
	// than erroring the job.
	var matched []domain.AiClassifier
	for _, id := range out.MatchedIDs {
		if c, ok := byID[id]; ok {
			matched = append(matched, c)
		}
	}
	if len(matched) == 0 {
		return nil
	}

	// Labels first (a dedicated, narrow write): union each matched
	// classifier's local label onto the thread. The label is scoped to the
	// classifier via a synthetic ProviderLabelID so re-matching the same
	// classifier on a later message reuses the same label row instead of
	// duplicating it.
	var newLabelIDs []string
	for _, c := range matched {
		if c.LabelName == "" {
			continue
		}
		label, err := s.d.Labels.Upsert(ctx, domain.Label{
			ID:              newID(),
			AccountID:       t.AccountID,
			ProviderLabelID: "calendium-ai:" + c.ID,
			Name:            c.LabelName,
			Kind:            domain.LabelKindUser,
		})
		if err != nil {
			return err
		}
		newLabelIDs = append(newLabelIDs, label.ID)
	}
	if len(newLabelIDs) > 0 {
		fresh, err := s.d.Threads.GetByID(ctx, t.ID)
		if err != nil {
			return err
		}
		if err := s.d.Threads.SetLabels(ctx, t.ID, unionStrings(fresh.LabelIDs, newLabelIDs)); err != nil {
			return err
		}
	}

	// Split routing: the last matching classifier with a TargetSplit wins.
	// ThreadRepo has no narrower "split-only" write, so this re-fetches the
	// thread (picking up the label write above and any other concurrent
	// change) immediately before a full Update — acceptable because
	// classification runs within seconds of ingest, when the odds of a
	// genuine concurrent user mutation racing this exact window are
	// negligible.
	var targetSplit domain.InboxSplit
	for _, c := range matched {
		if c.TargetSplit != "" {
			targetSplit = c.TargetSplit
		}
	}
	if targetSplit != "" {
		fresh, err := s.d.Threads.GetByID(ctx, t.ID)
		if err != nil {
			return err
		}
		fresh.Split = targetSplit
		if err := s.d.Threads.Update(ctx, fresh); err != nil {
			return err
		}
	}
	return nil
}

// unionStrings appends add's elements onto existing, skipping duplicates and
// preserving existing's order followed by add's first-seen order.
func unionStrings(existing, add []string) []string {
	seen := make(map[string]struct{}, len(existing)+len(add))
	out := make([]string, 0, len(existing)+len(add))
	for _, id := range existing {
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	for _, id := range add {
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
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

// runVoiceProfile learns the account owner's writing style from their own
// sent mail: it loads up to voiceSampleCount of the account's sent messages,
// and when there are at least voiceMinSamples of them, asks the model for a
// style profile via completeJSONBudgeted and upserts it as the user's single
// domain.VoiceProfile row (consumed by AIService.Compose's compose/reply
// voice injection). Too few samples is a no-op, not an error: there is
// nothing yet to learn a style from, and the job never spends budget on it.
// On success it self-re-enqueues a fresh voice_profile job 30 days out, so
// the profile keeps refreshing as more sent mail accumulates. Unlike the
// other kinds it carries no ThreadID (voice_profile jobs are
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
	if !s.backgroundAIAllowed(ctx, j.UserID) {
		// The switch is off: complete the job without the model and without
		// the 30-day re-enqueue, which ends the self-perpetuating chain. A
		// sync after the switch is turned back on queues a fresh one.
		return nil
	}
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
