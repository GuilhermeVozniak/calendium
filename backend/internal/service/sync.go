package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

const (
	dueDraftBatch  = 50
	dueThreadBatch = 100
)

// SyncServiceDeps wires a SyncService.
type SyncServiceDeps struct {
	Accounts          port.AccountRepo
	Labels            port.LabelRepo
	Threads           port.ThreadRepo
	Messages          port.MessageRepo
	Drafts            port.DraftRepo
	Calendars         port.CalendarRepo
	Events            port.EventRepo
	Devices           port.DeviceRepo
	SyncState         port.SyncStateRepo
	MailProviders     map[domain.Provider]port.MailProvider
	CalendarProviders map[domain.Provider]port.CalendarProvider
	OAuth             map[domain.Provider]port.OAuthGateway
	Push              port.PushSender // optional; nil disables notifications
	AiJobs            port.AiJobRepo  // optional; nil disables AI enqueueing
	// Activity records team replied_at indicators on delivered sends (M2.7
	// Task 10); nil disables recording.
	Activity port.TeamThreadActivityRepo
	// Bus fans activity.updated collab events out; nil disables publishing.
	Bus port.EventBus
	// Classifiers is optional (nil when AI is not configured); when set, it
	// gates the classify job kind at enqueue time so a user with no enabled
	// classifiers never pays a job-queue/budget cost for evaluating an empty
	// rule set (see hasClassifiers in applyMailPage).
	Classifiers port.ClassifierRepo
	Clock       port.Clock
}

// SyncService implements port.SyncService: incremental provider sync (with
// split-inbox classification at ingest) and scheduled work — delayed sends,
// snooze wake-ups, and follow-up reminders.
type SyncService struct {
	accounts    port.AccountRepo
	labels      port.LabelRepo
	threads     port.ThreadRepo
	messages    port.MessageRepo
	drafts      port.DraftRepo
	calendars   port.CalendarRepo
	events      port.EventRepo
	devices     port.DeviceRepo
	syncState   port.SyncStateRepo
	mail        map[domain.Provider]port.MailProvider
	cal         map[domain.Provider]port.CalendarProvider
	tokens      tokenSource
	push        port.PushSender
	aiJobs      port.AiJobRepo
	activity    port.TeamThreadActivityRepo
	bus         port.EventBus
	classifiers port.ClassifierRepo
	clock       port.Clock
}

var _ port.SyncService = (*SyncService)(nil)

func NewSyncService(d SyncServiceDeps) *SyncService {
	return &SyncService{
		accounts:    d.Accounts,
		labels:      d.Labels,
		threads:     d.Threads,
		messages:    d.Messages,
		drafts:      d.Drafts,
		calendars:   d.Calendars,
		events:      d.Events,
		devices:     d.Devices,
		syncState:   d.SyncState,
		mail:        d.MailProviders,
		cal:         d.CalendarProviders,
		tokens:      tokenSource{accounts: d.Accounts, oauth: d.OAuth, clock: d.Clock},
		push:        d.Push,
		aiJobs:      d.AiJobs,
		activity:    d.Activity,
		bus:         d.Bus,
		classifiers: d.Classifiers,
		clock:       d.Clock,
	}
}

// SyncAccount runs one incremental mail + calendar sync pass for an account.
func (s *SyncService) SyncAccount(ctx context.Context, accountID string) error {
	acct, err := s.accounts.GetByID(ctx, accountID)
	if err != nil {
		return err
	}
	// Enqueue voice_profile on first sync of the account.
	if s.aiJobs != nil && acct.LastSyncedAt == nil {
		_ = s.aiJobs.Enqueue(ctx, domain.AiJob{
			ID:        newID(),
			UserID:    acct.UserID,
			AccountID: acct.ID,
			Kind:      domain.AiJobVoiceProfile,
			ThreadID:  nil,
			Payload:   map[string]string{},
			RunAfter:  s.clock.Now(),
		})
	}
	token, err := s.tokens.accessToken(ctx, acct)
	if err != nil {
		return err
	}
	if err := s.syncMail(ctx, acct, token); err != nil {
		return fmt.Errorf("sync mail for account %s: %w", acct.ID, err)
	}
	if err := s.syncCalendars(ctx, acct, token); err != nil {
		return fmt.Errorf("sync calendars for account %s: %w", acct.ID, err)
	}
	now := s.clock.Now()
	acct.LastSyncedAt = &now
	acct.Status = domain.AccountActive
	return s.accounts.Update(ctx, acct)
}

func (s *SyncService) syncMail(ctx context.Context, acct domain.ConnectedAccount, token string) error {
	provider, ok := s.mail[acct.Provider]
	if !ok {
		return nil
	}
	cursor := s.cursor(ctx, acct.ID, "mail")
	for {
		page, err := provider.SyncMail(ctx, token, cursor)
		if err != nil {
			return err
		}
		if err := s.applyMailPage(ctx, acct, page); err != nil {
			return err
		}
		cursor = page.NextCursor
		if err := s.saveCursor(ctx, acct.ID, "mail", cursor); err != nil {
			return err
		}
		if !page.HasMore {
			return nil
		}
	}
}

func (s *SyncService) applyMailPage(ctx context.Context, acct domain.ConnectedAccount, page port.MailSyncPage) error {
	// 1. Labels.
	for _, l := range page.Labels {
		l.AccountID = acct.ID
		if l.ID == "" {
			l.ID = newID()
		}
		if _, err := s.labels.Upsert(ctx, l); err != nil {
			return err
		}
	}
	labelIDByProvider := map[string]string{}
	if len(page.Threads) > 0 {
		all, err := s.labels.ListByAccount(ctx, acct.ID)
		if err != nil {
			return err
		}
		for _, l := range all {
			labelIDByProvider[l.ProviderLabelID] = l.ID
		}
	}

	// hasClassifiers gates the classify job kind below: computed once per
	// page (not per thread) since it's the same answer for every thread on
	// this account. A repo error is treated as "assume none" (skip
	// enqueueing) rather than failing the whole sync page, consistent with
	// enqueueIngestAiJobs's own best-effort Enqueue calls.
	hasClassifiers := false
	if s.classifiers != nil {
		if cs, err := s.classifiers.ListEnabledByUser(ctx, acct.UserID); err == nil {
			hasClassifiers = len(cs) > 0
		}
	}

	// 2. Split-inbox classification at ingest: the newest message in each
	// provider thread decides the split. VIP senders configured on the
	// account are honored so the "vip" split is actually reachable.
	vips := vipSet(acct.VIPSenders)
	splitByThread := map[string]domain.InboxSplit{}
	unsubByThread := map[string]unsubscribeInfo{}
	unsubNewestAt := map[string]time.Time{} // Track when unsubscribe info was last updated
	newestByThread := map[string]time.Time{}
	for _, im := range page.Messages {
		providerThreadID := im.Message.ThreadID // provider id at this boundary
		if last, seen := newestByThread[providerThreadID]; !seen || im.Message.SentAt.After(last) {
			newestByThread[providerThreadID] = im.Message.SentAt
			splitByThread[providerThreadID] = ClassifySplit(im, acct.Email, vips)
		}
		// Track unsubscribe info independently of split classification running max.
		// For each message, check if it carries unsubscribe headers and if its timestamp
		// is newer than any previously seen carrier for this thread.
		info := parseListUnsubscribe(im.Headers)
		if info.Mailto != "" || info.URL != "" {
			if lastCarrierAt, seen := unsubNewestAt[providerThreadID]; !seen || im.Message.SentAt.After(lastCarrierAt) {
				unsubByThread[providerThreadID] = info
				unsubNewestAt[providerThreadID] = im.Message.SentAt
			}
		}
	}

	// 3. Threads (preserving local-only state on updates).
	localThreadByProvider := map[string]domain.Thread{}
	var notify []domain.Thread
	for _, t := range page.Threads {
		t.AccountID = acct.ID
		// since bounds "is this a genuinely new inbound message" for both the
		// resurface-on-reply and push-notify checks below.
		var since time.Time
		existing, err := s.threads.GetByProviderID(ctx, acct.ID, t.ProviderThreadID)
		switch {
		case err == nil:
			t.ID = existing.ID
			t.OpenedAt = existing.OpenedAt // read state is local-only
			t.UnsubscribeMailto = existing.UnsubscribeMailto
			t.UnsubscribeURL = existing.UnsubscribeURL
			t.UnsubscribeOneClick = existing.UnsubscribeOneClick
			since = existing.LastMessageAt
			t.SnoozedUntil = existing.SnoozedUntil
			t.RemindAt = existing.RemindAt
			// A fresh inbound reply on the thread resurfaces it: Superhuman
			// auto-unsnoozes on reply, and a follow-up reminder ("resurface if
			// nobody replies") is moot once the recipient has answered.
			if hasNewInboundReply(page.Messages, t.ProviderThreadID, acct.Email, existing.LastMessageAt) {
				t.SnoozedUntil = nil
				t.RemindAt = nil
			}
			if split, ok := splitByThread[t.ProviderThreadID]; ok {
				t.Split = split
			} else if existing.Split != "" {
				t.Split = existing.Split
			}
		case errors.Is(err, domain.ErrNotFound):
			t.ID = newID()
			if split, ok := splitByThread[t.ProviderThreadID]; ok {
				t.Split = split
			} else if t.Split == "" {
				t.Split = domain.SplitOther
			}
		default:
			return err
		}
		if info, ok := unsubByThread[t.ProviderThreadID]; ok {
			t.UnsubscribeMailto = optionalString(info.Mailto)
			t.UnsubscribeURL = optionalString(info.URL)
			t.UnsubscribeOneClick = info.OneClick
		}
		if len(t.LabelIDs) > 0 {
			mapped := make([]string, 0, len(t.LabelIDs))
			for _, providerLabelID := range t.LabelIDs {
				if id, ok := labelIDByProvider[providerLabelID]; ok {
					mapped = append(mapped, id)
				}
			}
			t.LabelIDs = mapped
		}
		saved, err := s.threads.Upsert(ctx, t)
		if err != nil {
			return err
		}
		if err := s.threads.SetLabels(ctx, saved.ID, saved.LabelIDs); err != nil {
			return err
		}
		localThreadByProvider[t.ProviderThreadID] = saved
		// Compute hasNewInboundReply once to reuse for both AI-enqueue and push-notify.
		hasReply := hasNewInboundReply(page.Messages, t.ProviderThreadID, acct.Email, since)
		// Enqueue AI jobs for new inbound replies.
		if hasReply {
			s.enqueueIngestAiJobs(ctx, acct, saved, hasClassifiers)
		}
		// Push on newly-synced important/vip mail (a genuinely new inbound
		// message the owner has not sent). Only worth collecting when push is
		// wired.
		if s.push != nil && saved.InInbox &&
			(saved.Split == domain.SplitImportant || saved.Split == domain.SplitVIP) &&
			hasReply {
			notify = append(notify, saved)
		}
	}

	// 4. Messages, remapped onto local thread ids.
	for _, im := range page.Messages {
		m := im.Message
		m.AccountID = acct.ID
		m.RFCMessageID = rfcMessageIDFromHeaders(im.Headers) // conversation key for team read statuses
		providerThreadID := m.ThreadID
		if local, ok := localThreadByProvider[providerThreadID]; ok {
			m.ThreadID = local.ID
		} else {
			existing, err := s.threads.GetByProviderID(ctx, acct.ID, providerThreadID)
			if errors.Is(err, domain.ErrNotFound) {
				continue // orphan message; a later page carries its thread
			}
			if err != nil {
				return err
			}
			m.ThreadID = existing.ID
		}
		if m.ID == "" {
			m.ID = newID()
		}
		if _, err := s.messages.Upsert(ctx, m); err != nil {
			return err
		}
	}

	// 5. Push notifications for new important/vip mail (best-effort).
	for _, t := range notify {
		title := "New email"
		if t.Split == domain.SplitVIP {
			title = "New VIP email"
		}
		s.notifyThread(ctx, t, title, firstNonEmpty(t.Subject, "You have a new message"))
	}
	return nil
}

// enqueueIngestAiJobs queues background AI work for a thread that just
// received a genuinely new inbound message. Enqueue failures are intentionally
// discarded — AI enqueueing must never fail sync.
func (s *SyncService) enqueueIngestAiJobs(ctx context.Context, acct domain.ConnectedAccount, t domain.Thread, hasClassifiers bool) {
	if s.aiJobs == nil {
		return
	}
	// thread_summary + instant_replies: important|vip|team|calendar splits only
	if t.Split == domain.SplitImportant || t.Split == domain.SplitVIP || t.Split == domain.SplitTeam || t.Split == domain.SplitCalendar {
		_ = s.aiJobs.Enqueue(ctx, domain.AiJob{
			ID:        newID(),
			UserID:    acct.UserID,
			AccountID: acct.ID,
			Kind:      domain.AiJobThreadSummary,
			ThreadID:  &t.ID,
			Payload:   map[string]string{},
			RunAfter:  s.clock.Now(),
		})
		_ = s.aiJobs.Enqueue(ctx, domain.AiJob{
			ID:        newID(),
			UserID:    acct.UserID,
			AccountID: acct.ID,
			Kind:      domain.AiJobInstantReplies,
			ThreadID:  &t.ID,
			Payload:   map[string]string{},
			RunAfter:  s.clock.Now(),
		})
	}
	// auto_draft: important|vip splits only (direct human mail)
	if t.Split == domain.SplitImportant || t.Split == domain.SplitVIP {
		_ = s.aiJobs.Enqueue(ctx, domain.AiJob{
			ID:        newID(),
			UserID:    acct.UserID,
			AccountID: acct.ID,
			Kind:      domain.AiJobAutoDraft,
			ThreadID:  &t.ID,
			Payload:   map[string]string{},
			RunAfter:  s.clock.Now(),
		})
	}
	// classify: enqueued for every new-inbound thread regardless of split,
	// but only when the user has at least one enabled classifier — otherwise
	// the job would just spend a queue slot (and, once dispatched, an AI
	// budget unit) evaluating an empty rule set for nothing.
	if hasClassifiers {
		_ = s.aiJobs.Enqueue(ctx, domain.AiJob{
			ID:        newID(),
			UserID:    acct.UserID,
			AccountID: acct.ID,
			Kind:      domain.AiJobClassify,
			ThreadID:  &t.ID,
			Payload:   map[string]string{},
			RunAfter:  s.clock.Now(),
		})
	}
}

// vipSet builds the lowercased VIP-sender lookup passed to ClassifySplit;
// it returns nil when the account has no VIP senders configured.
func vipSet(senders []string) map[string]struct{} {
	if len(senders) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(senders))
	for _, s := range senders {
		if e := strings.ToLower(strings.TrimSpace(s)); e != "" {
			set[e] = struct{}{}
		}
	}
	return set
}

// hasNewInboundReply reports whether the page carries a message on the given
// provider thread from someone other than the account owner that is newer than
// the last message already recorded — a fresh reply that should cancel a
// pending follow-up reminder.
func hasNewInboundReply(msgs []port.IncomingMessage, providerThreadID, ownerEmail string, since time.Time) bool {
	owner := strings.ToLower(strings.TrimSpace(ownerEmail))
	for _, im := range msgs {
		if im.Message.ThreadID != providerThreadID {
			continue
		}
		from := strings.ToLower(strings.TrimSpace(im.Message.From.Email))
		if from == "" || from == owner {
			continue
		}
		if im.Message.SentAt.After(since) {
			return true
		}
	}
	return false
}

func (s *SyncService) syncCalendars(ctx context.Context, acct domain.ConnectedAccount, token string) error {
	provider, ok := s.cal[acct.Provider]
	if !ok {
		return nil
	}
	cals, err := provider.SyncCalendars(ctx, token)
	if err != nil {
		return err
	}
	for _, c := range cals {
		c.AccountID = acct.ID
		if c.ID == "" {
			c.ID = newID()
		}
		saved, err := s.calendars.Upsert(ctx, c) // preserves IsVisible/Color prefs
		if err != nil {
			return err
		}
		if err := s.syncEvents(ctx, acct, token, provider, saved); err != nil {
			return err
		}
	}
	return nil
}

func (s *SyncService) syncEvents(ctx context.Context, acct domain.ConnectedAccount, token string, provider port.CalendarProvider, cal domain.Calendar) error {
	resource := "events:" + cal.ProviderCalendarID
	cursor := s.cursor(ctx, acct.ID, resource)
	for {
		page, err := provider.SyncEvents(ctx, token, cal.ProviderCalendarID, cursor)
		if err != nil {
			return err
		}
		for _, ev := range page.Events {
			ev.CalendarID = cal.ID
			if ev.ID == "" {
				ev.ID = newID()
			}
			if _, err := s.events.Upsert(ctx, ev); err != nil {
				return err
			}
		}
		for _, providerEventID := range page.DeletedIDs {
			if err := s.events.DeleteByProviderID(ctx, cal.ID, providerEventID); err != nil && !errors.Is(err, domain.ErrNotFound) {
				return err
			}
		}
		cursor = page.NextCursor
		if err := s.saveCursor(ctx, acct.ID, resource, cursor); err != nil {
			return err
		}
		if !page.HasMore {
			return nil
		}
	}
}

// ProcessDueWork delivers due scheduled drafts (Send Later / undo-send) and
// resurfaces due snoozes and follow-up reminders, notifying via push.
func (s *SyncService) ProcessDueWork(ctx context.Context) error {
	now := s.clock.Now()
	return errors.Join(
		s.deliverDueDrafts(ctx, now),
		s.wakeSnoozedThreads(ctx, now),
		s.fireReminders(ctx, now),
	)
}

func (s *SyncService) deliverDueDrafts(ctx context.Context, now time.Time) error {
	due, err := s.drafts.ListScheduledDue(ctx, now, dueDraftBatch)
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range due {
		if err := s.deliverDraft(ctx, d); err != nil {
			errs = append(errs, fmt.Errorf("deliver draft %s: %w", d.ID, err))
		}
	}
	return errors.Join(errs...)
}

func (s *SyncService) deliverDraft(ctx context.Context, d domain.Draft) error {
	acct, err := s.accounts.GetByID(ctx, d.AccountID)
	if err != nil {
		return err
	}
	provider, ok := s.mail[acct.Provider]
	if !ok {
		return fmt.Errorf("no mail provider configured for %s", acct.Provider)
	}
	token, err := s.tokens.accessToken(ctx, acct)
	if err != nil {
		return err
	}

	out := port.OutgoingMessage{
		From:     domain.EmailAddress{Email: acct.Email},
		To:       d.To,
		Cc:       d.Cc,
		Bcc:      d.Bcc,
		Subject:  d.Subject,
		BodyHTML: d.BodyHTML,
	}
	if d.ThreadID != nil {
		if t, err := s.threads.GetByID(ctx, *d.ThreadID); err == nil {
			out.ProviderThreadID = t.ProviderThreadID
		}
	}

	// Auto-BCC: merge the account's configured addresses into the outgoing
	// BCC, skipping any address already present on the message. This is a
	// delivery-only concern applied server-side (M2.4/M2.3 convention -- never
	// trust the client) -- the mirrored domain.Message below keeps the
	// draft's original Bcc, so auto-BCC never becomes visible thread content.
	if len(acct.AutoBcc) > 0 {
		seen := make(map[string]bool, len(d.To)+len(d.Cc)+len(d.Bcc))
		for _, lists := range [][]domain.EmailAddress{d.To, d.Cc, d.Bcc} {
			for _, a := range lists {
				seen[strings.ToLower(a.Email)] = true
			}
		}
		for _, addr := range acct.AutoBcc {
			if !seen[strings.ToLower(addr)] {
				out.Bcc = append(out.Bcc, domain.EmailAddress{Email: addr})
			}
		}
	}

	// Claim the draft before sending so a crash/retry between send and delete, a
	// slow post-send DB write, or a second worker instance cannot deliver it
	// twice. Clearing scheduled_at is the claim token: ListScheduledDue only
	// returns rows that still have it set, so a claimed draft is never
	// re-selected.
	claimed, err := s.drafts.ClaimScheduled(ctx, d.ID)
	if err != nil {
		return err
	}
	if !claimed {
		return nil // already claimed by a concurrent pass or worker
	}

	sent, err := provider.Send(ctx, token, out)
	if err != nil {
		return s.handleSendFailure(ctx, d, err)
	}
	sentAt := sent.SentAt
	if sentAt.IsZero() {
		sentAt = s.clock.Now()
	}

	msg := domain.Message{
		ID:                newID(),
		AccountID:         acct.ID,
		ProviderMessageID: sent.ProviderMessageID,
		From:              domain.EmailAddress{Email: acct.Email},
		To:                d.To,
		Cc:                d.Cc,
		Bcc:               d.Bcc,
		Subject:           d.Subject,
		BodyHTML:          d.BodyHTML,
		Attachments:       []domain.Attachment{},
		SentAt:            sentAt,
		IsDraft:           false,
	}
	if d.ThreadID != nil {
		msg.ThreadID = *d.ThreadID
	}
	if msg.ThreadID != "" {
		if _, err := s.messages.Upsert(ctx, msg); err != nil {
			return err
		}
		// Targeted bump: increment message_count and advance last_message_at
		// in the database so a concurrent user mutation on the thread is not
		// clobbered by a stale full-row write.
		if err := s.threads.AppendSentMessage(ctx, msg.ThreadID, sentAt); err != nil &&
			!errors.Is(err, domain.ErrNotFound) {
			return err
		}
		// Enqueue reminder_detect for threaded replies: give recipient 24h before judging "awaiting reply".
		if s.aiJobs != nil {
			_ = s.aiJobs.Enqueue(ctx, domain.AiJob{
				ID:        newID(),
				UserID:    acct.UserID,
				AccountID: acct.ID,
				Kind:      domain.AiJobReminderDetect,
				ThreadID:  d.ThreadID,
				Payload:   map[string]string{"sentAt": sentAt.Format(time.RFC3339)},
				RunAfter:  s.clock.Now().Add(24 * time.Hour),
			})
		}
		s.recordReplyActivity(ctx, acct.UserID, msg.ThreadID, sentAt) // team reply indicators: best-effort
	}
	// New standalone threads are picked up by the next provider sync.
	return s.drafts.Delete(ctx, d.ID)
}

// maxSendAttempts caps delivery retries for a scheduled draft before it is
// dead-lettered (scheduled_at left clear, last_error set) so a permanently
// rejected send stops looping.
const maxSendAttempts = 5

// handleSendFailure records a failed scheduled-send. The draft was already
// claimed (scheduled_at cleared) before the send, so this re-arms it for a
// later retry with exponential backoff, or — once the attempt cap is reached
// — dead-letters it by leaving scheduled_at clear with the error recorded.
func (s *SyncService) handleSendFailure(ctx context.Context, d domain.Draft, sendErr error) error {
	attempts := d.SendAttempts + 1
	if attempts >= maxSendAttempts {
		if err := s.drafts.RecordSendFailure(ctx, d.ID, nil, sendErr.Error()); err != nil {
			return errors.Join(sendErr, err)
		}
		return fmt.Errorf("draft %s permanently failed after %d attempts: %w", d.ID, attempts, sendErr)
	}
	next := s.clock.Now().Add(sendBackoff(attempts))
	if err := s.drafts.RecordSendFailure(ctx, d.ID, &next, sendErr.Error()); err != nil {
		return errors.Join(sendErr, err)
	}
	return fmt.Errorf("draft %s send attempt %d failed, retrying after %s: %w", d.ID, attempts, sendBackoff(attempts), sendErr)
}

// sendBackoff returns the delay before the nth scheduled-send retry
// (30s, 60s, 120s, … capped at 15m).
func sendBackoff(attempt int) time.Duration {
	const base = 30 * time.Second
	const maxDelay = 15 * time.Minute
	d := base << uint(attempt-1)
	if d <= 0 || d > maxDelay {
		return maxDelay
	}
	return d
}

func (s *SyncService) wakeSnoozedThreads(ctx context.Context, now time.Time) error {
	due, err := s.threads.ListSnoozeDue(ctx, now, dueThreadBatch)
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range due {
		if err := s.threads.ClearSnooze(ctx, t.ID); err != nil {
			errs = append(errs, fmt.Errorf("wake thread %s: %w", t.ID, err))
			continue
		}
		s.notifyThread(ctx, t, "Snoozed conversation is back", t.Subject)
	}
	return errors.Join(errs...)
}

func (s *SyncService) fireReminders(ctx context.Context, now time.Time) error {
	due, err := s.threads.ListRemindersDue(ctx, now, dueThreadBatch)
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range due {
		if err := s.threads.ClearReminder(ctx, t.ID); err != nil {
			errs = append(errs, fmt.Errorf("fire reminder for thread %s: %w", t.ID, err))
			continue
		}
		s.notifyThread(ctx, t, "Follow-up reminder", t.Subject)
	}
	return errors.Join(errs...)
}

// notifyThread pushes to every device of the thread owner; best-effort.
func (s *SyncService) notifyThread(ctx context.Context, t domain.Thread, title, body string) {
	if s.push == nil {
		return
	}
	acct, err := s.accounts.GetByID(ctx, t.AccountID)
	if err != nil {
		return
	}
	devices, err := s.devices.ListByUser(ctx, acct.UserID)
	if err != nil {
		return
	}
	for _, d := range devices {
		_ = s.push.Send(ctx, d, title, body, map[string]string{"threadId": t.ID})
	}
}

func (s *SyncService) cursor(ctx context.Context, accountID, resource string) string {
	st, err := s.syncState.Get(ctx, accountID, resource)
	if err != nil {
		return ""
	}
	return st.Cursor
}

func (s *SyncService) saveCursor(ctx context.Context, accountID, resource, cursor string) error {
	return s.syncState.Save(ctx, port.SyncState{
		AccountID: accountID,
		Resource:  resource,
		Cursor:    cursor,
		UpdatedAt: s.clock.Now(),
	})
}
