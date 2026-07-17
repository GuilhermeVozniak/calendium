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

// DefaultUndoSendGrace delays "send now" so it can be undone (Superhuman
// undo send). Overridden via config UNDO_SEND_SECONDS.
const DefaultUndoSendGrace = 15 * time.Second

const (
	defaultThreadPageSize = 50
	maxThreadPageSize     = 200
)

// MailServiceDeps wires a MailService.
type MailServiceDeps struct {
	Subscriptions port.SubscriptionRepo
	Accounts      port.AccountRepo
	Threads       port.ThreadRepo
	Messages      port.MessageRepo
	Drafts        port.DraftRepo
	Snippets      port.SnippetRepo
	Labels        port.LabelRepo
	MailProviders map[domain.Provider]port.MailProvider
	OAuth         map[domain.Provider]port.OAuthGateway
	Clock         port.Clock
	// SelfHosted unlocks the paywall (open-core self-hosted mode).
	SelfHosted bool
	// UndoSendGrace <= 0 falls back to DefaultUndoSendGrace.
	UndoSendGrace time.Duration
}

// MailService implements port.MailService.
type MailService struct {
	ent           entitlement
	accounts      port.AccountRepo
	threads       port.ThreadRepo
	messages      port.MessageRepo
	drafts        port.DraftRepo
	snippets      port.SnippetRepo
	labels        port.LabelRepo
	mail          map[domain.Provider]port.MailProvider
	tokens        tokenSource
	clock         port.Clock
	undoSendGrace time.Duration
}

var _ port.MailService = (*MailService)(nil)

func NewMailService(d MailServiceDeps) *MailService {
	grace := d.UndoSendGrace
	if grace <= 0 {
		grace = DefaultUndoSendGrace
	}
	return &MailService{
		ent:           entitlement{subs: d.Subscriptions, clock: d.Clock, selfHost: d.SelfHosted},
		accounts:      d.Accounts,
		threads:       d.Threads,
		messages:      d.Messages,
		drafts:        d.Drafts,
		snippets:      d.Snippets,
		labels:        d.Labels,
		mail:          d.MailProviders,
		tokens:        tokenSource{accounts: d.Accounts, oauth: d.OAuth, clock: d.Clock},
		clock:         d.Clock,
		undoSendGrace: grace,
	}
}

// --- Threads ---

func (s *MailService) ListThreads(ctx context.Context, userID string, q port.ThreadQuery) (domain.Page[domain.Thread], error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Page[domain.Thread]{}, err
	}
	q.UserID = userID
	if q.Limit <= 0 {
		q.Limit = defaultThreadPageSize
	}
	if q.Limit > maxThreadPageSize {
		q.Limit = maxThreadPageSize
	}
	page, err := s.threads.List(ctx, q)
	if err != nil {
		return domain.Page[domain.Thread]{}, err
	}
	if page.Items == nil {
		page.Items = []domain.Thread{}
	}
	return page, nil
}

func (s *MailService) GetThread(ctx context.Context, userID, threadID string) (domain.Thread, []domain.Message, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Thread{}, nil, err
	}
	t, _, err := ownedThread(ctx, s.threads, s.accounts, userID, threadID)
	if err != nil {
		return domain.Thread{}, nil, err
	}
	msgs, err := s.messages.ListByThread(ctx, t.ID)
	if err != nil {
		return domain.Thread{}, nil, err
	}
	if msgs == nil {
		msgs = []domain.Message{}
	}
	return t, msgs, nil
}

func (s *MailService) ActOnThread(ctx context.Context, userID, threadID string, action domain.ThreadAction) (domain.Thread, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Thread{}, err
	}
	t, acct, err := ownedThread(ctx, s.threads, s.accounts, userID, threadID)
	if err != nil {
		return domain.Thread{}, err
	}
	return s.applyAction(ctx, t, acct, action)
}

// applyAction mutates one owned thread locally and writes through to the
// provider. Shared by ActOnThread, BulkActOnThreads, and ArchiveOlderThan.
func (s *MailService) applyAction(ctx context.Context, t domain.Thread, acct domain.ConnectedAccount, action domain.ThreadAction) (domain.Thread, error) {
	var add, remove []string
	switch action {
	case domain.ThreadActionArchive:
		remove = []string{port.LabelKeyInbox}
		t.InInbox = false // leaves the inbox list so it stops reappearing
	case domain.ThreadActionTrash:
		add, remove = []string{port.LabelKeyTrash}, []string{port.LabelKeyInbox}
		t.InInbox = false
	case domain.ThreadActionStar:
		add = []string{port.LabelKeyStarred}
		t.Starred = true
	case domain.ThreadActionUnstar:
		remove = []string{port.LabelKeyStarred}
		t.Starred = false
	case domain.ThreadActionRead:
		remove = []string{port.LabelKeyUnread}
		t.Unread = false
	case domain.ThreadActionUnread:
		add = []string{port.LabelKeyUnread}
		t.Unread = true
	case domain.ThreadActionSpam:
		add, remove = []string{port.LabelKeySpam}, []string{port.LabelKeyInbox}
		t.InInbox = false
	case domain.ThreadActionMoveToInbox:
		add, remove = []string{port.LabelKeyInbox}, []string{port.LabelKeyTrash, port.LabelKeySpam}
		t.InInbox = true
	default:
		return domain.Thread{}, fmt.Errorf("%w: unknown thread action %q", domain.ErrValidation, action)
	}

	// Optimistic local mutation first (Superhuman-grade latency), then
	// write-through to the provider; the sync loop reconciles drift.
	if err := s.threads.Update(ctx, t); err != nil {
		return domain.Thread{}, err
	}
	if provider, ok := s.mail[acct.Provider]; ok {
		token, err := s.tokens.accessToken(ctx, acct)
		if err != nil {
			return t, err
		}
		if err := provider.ModifyLabels(ctx, token, t.ProviderThreadID, add, remove); err != nil {
			return t, fmt.Errorf("provider write-through failed: %w", err)
		}
	}
	return t, nil
}

// maxBulkThreads caps POST /v1/mail/threads/bulk-actions payloads.
const maxBulkThreads = 200

// BulkActOnThreads applies action to each of threadIDs, skipping (and
// reporting in FailedIDs) any thread that is missing, foreign, or fails the
// provider write-through rather than failing the whole request.
func (s *MailService) BulkActOnThreads(ctx context.Context, userID string, threadIDs []string, action domain.ThreadAction) (port.BulkActionResult, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return port.BulkActionResult{}, err
	}
	if len(threadIDs) == 0 {
		return port.BulkActionResult{}, fmt.Errorf("%w: threadIds is required", domain.ErrValidation)
	}
	if len(threadIDs) > maxBulkThreads {
		return port.BulkActionResult{}, fmt.Errorf("%w: at most %d threads per bulk action", domain.ErrValidation, maxBulkThreads)
	}
	res := port.BulkActionResult{Threads: []domain.Thread{}, FailedIDs: []string{}}
	for _, id := range threadIDs {
		t, acct, err := ownedThread(ctx, s.threads, s.accounts, userID, id)
		if err != nil {
			res.FailedIDs = append(res.FailedIDs, id)
			continue
		}
		updated, err := s.applyAction(ctx, t, acct, action)
		if err != nil {
			res.FailedIDs = append(res.FailedIDs, id)
			continue
		}
		res.Threads = append(res.Threads, updated)
	}
	return res, nil
}

// MarkThreadOpened records the first open of a thread (real read state) and
// writes the read status through to the provider. Idempotent: repeated opens
// keep the original OpenedAt and re-assert read state.
func (s *MailService) MarkThreadOpened(ctx context.Context, userID, threadID string) error {
	if err := s.ent.require(ctx, userID); err != nil {
		return err
	}
	t, acct, err := ownedThread(ctx, s.threads, s.accounts, userID, threadID)
	if err != nil {
		return err
	}
	if err := s.threads.MarkOpened(ctx, t.ID); err != nil {
		return err
	}
	if provider, ok := s.mail[acct.Provider]; ok {
		token, err := s.tokens.accessToken(ctx, acct)
		if err != nil {
			return err
		}
		if err := provider.ModifyLabels(ctx, token, t.ProviderThreadID, nil, []string{port.LabelKeyUnread}); err != nil {
			return fmt.Errorf("provider write-through failed: %w", err)
		}
	}
	return nil
}

func (s *MailService) SnoozeThread(ctx context.Context, userID, threadID string, until time.Time) (domain.Thread, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Thread{}, err
	}
	if !until.After(s.clock.Now()) {
		return domain.Thread{}, fmt.Errorf("%w: snooze time must be in the future", domain.ErrValidation)
	}
	t, _, err := ownedThread(ctx, s.threads, s.accounts, userID, threadID)
	if err != nil {
		return domain.Thread{}, err
	}
	t.SnoozedUntil = &until
	if err := s.threads.Update(ctx, t); err != nil {
		return domain.Thread{}, err
	}
	return t, nil
}

func (s *MailService) UnsnoozeThread(ctx context.Context, userID, threadID string) (domain.Thread, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Thread{}, err
	}
	t, _, err := ownedThread(ctx, s.threads, s.accounts, userID, threadID)
	if err != nil {
		return domain.Thread{}, err
	}
	t.SnoozedUntil = nil
	if err := s.threads.Update(ctx, t); err != nil {
		return domain.Thread{}, err
	}
	return t, nil
}

func (s *MailService) SetReminder(ctx context.Context, userID, threadID string, remindAt *time.Time) (domain.Thread, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Thread{}, err
	}
	if remindAt != nil && !remindAt.After(s.clock.Now()) {
		return domain.Thread{}, fmt.Errorf("%w: reminder time must be in the future", domain.ErrValidation)
	}
	t, _, err := ownedThread(ctx, s.threads, s.accounts, userID, threadID)
	if err != nil {
		return domain.Thread{}, err
	}
	t.RemindAt = remindAt // nil clears the reminder
	if err := s.threads.Update(ctx, t); err != nil {
		return domain.Thread{}, err
	}
	return t, nil
}

// ArchiveOlderThan is Get Me To Zero: archive every inbox thread older than
// the cutoff, paging until none remain. Archiving clears in_inbox, so each
// page shrinks and the loop terminates. Returns how many were successfully
// archived.
//
// A per-thread failure (missing account, provider write-through error) skips
// that thread and continues with the rest, mirroring BulkActOnThreads; only a
// ListInboxBefore (listing) error aborts the whole run. A thread that fails
// to archive keeps in_inbox=true, so it would otherwise keep reappearing in
// every subsequent page under this re-query-from-start pagination and loop
// forever. To guarantee termination, failing thread IDs are tracked and
// skipped on later pages, and a page consisting entirely of already-failed
// IDs ends the run.
func (s *MailService) ArchiveOlderThan(ctx context.Context, userID string, olderThan time.Time) (int, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return 0, err
	}
	archived := 0
	failed := map[string]struct{}{}
	for {
		batch, err := s.threads.ListInboxBefore(ctx, userID, olderThan, maxBulkThreads)
		if err != nil {
			return archived, err
		}
		if len(batch) == 0 {
			return archived, nil
		}

		progress := false
		for _, t := range batch {
			if _, alreadyFailed := failed[t.ID]; !alreadyFailed {
				progress = true
				break
			}
		}
		if !progress {
			// Every thread on this page has already failed before and stays
			// in_inbox; re-querying would return the same page forever.
			return archived, nil
		}

		for _, t := range batch {
			if _, alreadyFailed := failed[t.ID]; alreadyFailed {
				continue
			}
			acct, err := s.accounts.GetByID(ctx, t.AccountID)
			if err != nil {
				failed[t.ID] = struct{}{}
				continue
			}
			if _, err := s.applyAction(ctx, t, acct, domain.ThreadActionArchive); err != nil {
				failed[t.ID] = struct{}{}
				continue
			}
			archived++
		}
	}
}

// --- Drafts ---

func (s *MailService) CreateDraft(ctx context.Context, userID string, in port.DraftInput) (domain.Draft, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Draft{}, err
	}
	if in.AccountID == "" {
		return domain.Draft{}, fmt.Errorf("%w: accountId is required", domain.ErrValidation)
	}
	if _, err := ownedAccount(ctx, s.accounts, userID, in.AccountID); err != nil {
		return domain.Draft{}, err
	}
	d := domain.Draft{
		ID:          newID(),
		AccountID:   in.AccountID,
		ThreadID:    in.ThreadID,
		To:          emptyIfNil(in.To),
		Cc:          emptyIfNil(in.Cc),
		Bcc:         emptyIfNil(in.Bcc),
		Subject:     in.Subject,
		BodyHTML:    in.BodyHTML,
		ScheduledAt: in.ScheduledAt,
		UpdatedAt:   s.clock.Now(),
	}
	return s.drafts.Create(ctx, d)
}

func (s *MailService) UpdateDraft(ctx context.Context, userID, draftID string, in port.DraftInput) (domain.Draft, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Draft{}, err
	}
	d, _, err := ownedDraft(ctx, s.drafts, s.accounts, userID, draftID)
	if err != nil {
		return domain.Draft{}, err
	}
	if in.AccountID != "" && in.AccountID != d.AccountID {
		return domain.Draft{}, fmt.Errorf("%w: a draft cannot move between accounts", domain.ErrValidation)
	}
	d.ThreadID = in.ThreadID
	d.To = emptyIfNil(in.To)
	d.Cc = emptyIfNil(in.Cc)
	d.Bcc = emptyIfNil(in.Bcc)
	d.Subject = in.Subject
	d.BodyHTML = in.BodyHTML
	d.ScheduledAt = in.ScheduledAt // clearing before the grace elapses = undo send
	d.UpdatedAt = s.clock.Now()
	if err := s.drafts.Update(ctx, d); err != nil {
		return domain.Draft{}, err
	}
	return d, nil
}

func (s *MailService) GetDraft(ctx context.Context, userID, draftID string) (domain.Draft, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Draft{}, err
	}
	d, _, err := ownedDraft(ctx, s.drafts, s.accounts, userID, draftID)
	return d, err
}

func (s *MailService) ListDrafts(ctx context.Context, userID string) ([]domain.Draft, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	ds, err := s.drafts.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if ds == nil {
		ds = []domain.Draft{}
	}
	return ds, nil
}

func (s *MailService) DeleteDraft(ctx context.Context, userID, draftID string) error {
	if err := s.ent.require(ctx, userID); err != nil {
		return err
	}
	d, _, err := ownedDraft(ctx, s.drafts, s.accounts, userID, draftID)
	if err != nil {
		return err
	}
	return s.drafts.Delete(ctx, d.ID)
}

// SendDraft queues delivery rather than sending inline: "send now" becomes
// scheduledAt = now + undo-send grace, Send Later keeps the requested time.
// The worker (SyncService.ProcessDueWork) performs the provider send once
// the time elapses; until then, updating the draft with a nil scheduledAt
// undoes the send.
func (s *MailService) SendDraft(ctx context.Context, userID, draftID string) (domain.Message, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Message{}, err
	}
	d, acct, err := ownedDraft(ctx, s.drafts, s.accounts, userID, draftID)
	if err != nil {
		return domain.Message{}, err
	}
	if len(d.To)+len(d.Cc)+len(d.Bcc) == 0 {
		return domain.Message{}, fmt.Errorf("%w: draft has no recipients", domain.ErrValidation)
	}
	now := s.clock.Now()
	sendAt := now.Add(s.undoSendGrace)
	if d.ScheduledAt != nil && d.ScheduledAt.After(now) {
		sendAt = *d.ScheduledAt // Send Later
	}
	d.ScheduledAt = &sendAt
	d.UpdatedAt = now
	if err := s.drafts.Update(ctx, d); err != nil {
		return domain.Message{}, err
	}

	// Provisional message; the worker replaces it with provider truth.
	msg := domain.Message{
		ID:          newID(),
		AccountID:   d.AccountID,
		From:        domain.EmailAddress{Email: acct.Email},
		To:          d.To,
		Cc:          d.Cc,
		Bcc:         d.Bcc,
		Subject:     d.Subject,
		BodyHTML:    d.BodyHTML,
		Attachments: []domain.Attachment{},
		SentAt:      sendAt,
		IsDraft:     true,
	}
	if d.ThreadID != nil {
		msg.ThreadID = *d.ThreadID
	}
	return msg, nil
}

// UnsendDraft undoes a queued send within the grace window: it atomically
// clears the draft's scheduled_at (the worker's claim token) so delivery
// never happens, and returns the reverted draft. If the worker already
// claimed and delivered the draft (grace elapsed, draft deleted, or a
// concurrent claim won), it returns domain.ErrConflict.
func (s *MailService) UnsendDraft(ctx context.Context, userID, draftID string) (domain.Draft, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Draft{}, err
	}
	d, _, err := ownedDraft(ctx, s.drafts, s.accounts, userID, draftID)
	if errors.Is(err, domain.ErrNotFound) {
		// Delivered and deleted by the worker — nothing left to undo.
		return domain.Draft{}, fmt.Errorf("%w: draft already delivered", domain.ErrConflict)
	}
	if err != nil {
		return domain.Draft{}, err
	}
	if d.ScheduledAt == nil {
		return domain.Draft{}, fmt.Errorf("%w: draft is not scheduled to send", domain.ErrConflict)
	}
	// Clearing scheduled_at wins the race against the worker's ClaimScheduled:
	// exactly one of unsend/deliver succeeds.
	claimed, err := s.drafts.ClaimScheduled(ctx, d.ID)
	if err != nil {
		return domain.Draft{}, err
	}
	if !claimed {
		return domain.Draft{}, fmt.Errorf("%w: draft already being delivered", domain.ErrConflict)
	}
	d.ScheduledAt = nil
	d.UpdatedAt = s.clock.Now()
	return d, nil
}

// --- Snippets ---

func (s *MailService) ListSnippets(ctx context.Context, userID string) ([]domain.Snippet, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	snips, err := s.snippets.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if snips == nil {
		snips = []domain.Snippet{}
	}
	return snips, nil
}

func (s *MailService) CreateSnippet(ctx context.Context, userID string, in port.SnippetInput) (domain.Snippet, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Snippet{}, err
	}
	if err := validateSnippetInput(in); err != nil {
		return domain.Snippet{}, err
	}
	return s.snippets.Create(ctx, domain.Snippet{
		ID:       newID(),
		UserID:   userID,
		Name:     in.Name,
		Shortcut: in.Shortcut,
		BodyHTML: in.BodyHTML,
	})
}

func (s *MailService) UpdateSnippet(ctx context.Context, userID, snippetID string, in port.SnippetInput) (domain.Snippet, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Snippet{}, err
	}
	if err := validateSnippetInput(in); err != nil {
		return domain.Snippet{}, err
	}
	snip, err := s.ownedSnippet(ctx, userID, snippetID)
	if err != nil {
		return domain.Snippet{}, err
	}
	snip.Name = in.Name
	snip.Shortcut = in.Shortcut
	snip.BodyHTML = in.BodyHTML
	if err := s.snippets.Update(ctx, snip); err != nil {
		return domain.Snippet{}, err
	}
	return snip, nil
}

func (s *MailService) DeleteSnippet(ctx context.Context, userID, snippetID string) error {
	if err := s.ent.require(ctx, userID); err != nil {
		return err
	}
	snip, err := s.ownedSnippet(ctx, userID, snippetID)
	if err != nil {
		return err
	}
	return s.snippets.Delete(ctx, snip.ID)
}

func (s *MailService) ownedSnippet(ctx context.Context, userID, snippetID string) (domain.Snippet, error) {
	snip, err := s.snippets.GetByID(ctx, snippetID)
	if err != nil {
		return domain.Snippet{}, err
	}
	if snip.UserID != userID {
		return domain.Snippet{}, domain.ErrNotFound
	}
	return snip, nil
}

func validateSnippetInput(in port.SnippetInput) error {
	if strings.TrimSpace(in.Name) == "" {
		return fmt.Errorf("%w: snippet name is required", domain.ErrValidation)
	}
	if strings.TrimSpace(in.BodyHTML) == "" {
		return fmt.Errorf("%w: snippet body is required", domain.ErrValidation)
	}
	return nil
}

// --- Labels ---

func (s *MailService) ListLabels(ctx context.Context, userID string) ([]domain.Label, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	ls, err := s.labels.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if ls == nil {
		ls = []domain.Label{}
	}
	return ls, nil
}

func (s *MailService) SetThreadLabel(ctx context.Context, userID, threadID, labelID string, add bool) (domain.Thread, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Thread{}, err
	}
	t, acct, err := ownedThread(ctx, s.threads, s.accounts, userID, threadID)
	if err != nil {
		return domain.Thread{}, err
	}
	return s.setLabelOwned(ctx, t, acct, labelID, add)
}

// setLabelOwned applies a label mutation to an already-ownership-checked
// thread; shared by SetThreadLabel and BulkSetLabel.
func (s *MailService) setLabelOwned(ctx context.Context, t domain.Thread, acct domain.ConnectedAccount, labelID string, add bool) (domain.Thread, error) {
	label, err := s.labels.GetByID(ctx, labelID)
	if err != nil {
		return domain.Thread{}, err
	}
	if label.AccountID != t.AccountID {
		return domain.Thread{}, domain.ErrNotFound // labels never cross accounts
	}
	next := make([]string, 0, len(t.LabelIDs)+1)
	for _, id := range t.LabelIDs {
		if id != labelID {
			next = append(next, id)
		}
	}
	if add {
		next = append(next, labelID)
	}
	t.LabelIDs = next
	if err := s.threads.SetLabels(ctx, t.ID, next); err != nil {
		return domain.Thread{}, err
	}
	if provider, ok := s.mail[acct.Provider]; ok {
		token, err := s.tokens.accessToken(ctx, acct)
		if err != nil {
			return t, err
		}
		var addKeys, removeKeys []string
		if add {
			addKeys = []string{label.ProviderLabelID}
		} else {
			removeKeys = []string{label.ProviderLabelID}
		}
		if err := provider.ModifyLabels(ctx, token, t.ProviderThreadID, addKeys, removeKeys); err != nil {
			return t, fmt.Errorf("provider write-through failed: %w", err)
		}
	}
	return t, nil
}

// BulkSetLabel applies the same label mutation to each of threadIDs, skipping
// (and reporting in FailedIDs) any thread that is missing, foreign, or fails
// the provider write-through rather than failing the whole request.
func (s *MailService) BulkSetLabel(ctx context.Context, userID string, threadIDs []string, labelID string, add bool) (port.BulkActionResult, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return port.BulkActionResult{}, err
	}
	if len(threadIDs) == 0 || len(threadIDs) > maxBulkThreads {
		return port.BulkActionResult{}, fmt.Errorf("%w: between 1 and %d threadIds required", domain.ErrValidation, maxBulkThreads)
	}
	res := port.BulkActionResult{Threads: []domain.Thread{}, FailedIDs: []string{}}
	for _, id := range threadIDs {
		t, acct, err := ownedThread(ctx, s.threads, s.accounts, userID, id)
		if err != nil {
			res.FailedIDs = append(res.FailedIDs, id)
			continue
		}
		updated, err := s.setLabelOwned(ctx, t, acct, labelID, add)
		if err != nil {
			res.FailedIDs = append(res.FailedIDs, id)
			continue
		}
		res.Threads = append(res.Threads, updated)
	}
	return res, nil
}

func emptyIfNil(addrs []domain.EmailAddress) []domain.EmailAddress {
	if addrs == nil {
		return []domain.EmailAddress{}
	}
	return addrs
}
