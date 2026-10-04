package port

import (
	"context"
	"encoding/json"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/ics"
)

// ---------------------------------------------------------------------------
// Cross-cutting
// ---------------------------------------------------------------------------

// Identity is the authenticated principal extracted from a verified
// Better Auth JWT.
type Identity struct {
	Subject   string // stable user id (JWT `sub` claim)
	Email     string
	Name      string
	AvatarURL string
}

// TokenVerifier verifies a Better Auth access token locally (EdDSA/RS256/ES256
// via JWKS) and returns the caller's identity.
type TokenVerifier interface {
	Verify(ctx context.Context, jwt string) (Identity, error)
}

// Clock abstracts time for testable scheduling logic.
type Clock interface {
	Now() time.Time
}

// TxRunner runs fn inside a database transaction. Repositories resolve the
// transaction from ctx; fn returning an error rolls back.
type TxRunner interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// ---------------------------------------------------------------------------
// Repositories (implemented by internal/adapter/out/postgres)
// All lookups scoped by id return domain.ErrNotFound when absent.
// ---------------------------------------------------------------------------

// UserRepo persists users keyed by the Better Auth subject id.
type UserRepo interface {
	// Upsert inserts the user or refreshes email/name/avatar on conflict.
	Upsert(ctx context.Context, u domain.User) (domain.User, error)
	GetByID(ctx context.Context, id string) (domain.User, error)
}

// SubscriptionRepo persists the one-row-per-user billing mirror
// (docs/payments.md). Provider ids are opaque strings (Paddle ctm_/sub_).
type SubscriptionRepo interface {
	GetByUserID(ctx context.Context, userID string) (domain.Subscription, error)
	GetByBillingCustomerID(ctx context.Context, customerID string) (domain.Subscription, error)
	// Upsert replaces the row unconditionally (the forced write used by
	// reconciliation); empty billing ids never clobber stored ones.
	Upsert(ctx context.Context, s domain.Subscription) error
	// UpsertIfNewer is Upsert guarded by event order, checked atomically in
	// the write: an existing row is replaced only when its last_event_at is
	// NULL or <= s.LastEventAt. applied is false when a newer event is
	// already stored (the write was skipped).
	UpsertIfNewer(ctx context.Context, s domain.Subscription) (applied bool, err error)
	// EnsureTrial inserts a trialing row ending at trialEndsAt when the user
	// has no row yet; it is a no-op otherwise (ON CONFLICT DO NOTHING), so
	// concurrent first calls are safe.
	EnsureTrial(ctx context.Context, userID string, trialEndsAt time.Time) error
	// ListForReconciliation returns rows with a billing_subscription_id
	// where (status ∈ {active, past_due, paused} and current_period_end <
	// now - 1h) or last_event_at < now - 7d, ordered by user_id.
	ListForReconciliation(ctx context.Context, now time.Time) ([]domain.Subscription, error)
}

// TokenSet is a provider OAuth token bundle.
type TokenSet struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

// AccountRepo persists connected provider accounts. Implementations MUST
// store access/refresh tokens encrypted at rest (AES-256-GCM with
// config.Crypto.TokenEncryptionKey); TokenSet crosses this boundary in
// plaintext only.
type AccountRepo interface {
	Create(ctx context.Context, a domain.ConnectedAccount) (domain.ConnectedAccount, error)
	GetByID(ctx context.Context, id string) (domain.ConnectedAccount, error)
	ListByUser(ctx context.Context, userID string) ([]domain.ConnectedAccount, error)
	// ListSyncable returns every account the worker should poll (status
	// active or syncing).
	ListSyncable(ctx context.Context) ([]domain.ConnectedAccount, error)
	Update(ctx context.Context, a domain.ConnectedAccount) error
	Delete(ctx context.Context, id string) error
	SaveTokens(ctx context.Context, accountID string, t TokenSet) error
	GetTokens(ctx context.Context, accountID string) (TokenSet, error)
}

// ThreadQuery filters thread lists. UserID is mandatory; zero values on the
// other fields mean "no filter". Snoozed threads are excluded unless
// IncludeSnoozed is set. A non-empty View selects a cross-split pseudo-view
// (starred/snoozed/sent) and takes precedence over the default inbox-only
// listing; clients use View for these, never LabelID.
type ThreadQuery struct {
	UserID         string
	AccountID      string
	Split          domain.InboxSplit
	View           domain.ThreadView
	LabelID        string
	Query          string
	Cursor         string
	Limit          int
	IncludeSnoozed bool
}

// ThreadRepo persists mirrored mail threads.
type ThreadRepo interface {
	Upsert(ctx context.Context, t domain.Thread) (domain.Thread, error)
	GetByID(ctx context.Context, id string) (domain.Thread, error)
	GetByProviderID(ctx context.Context, accountID, providerThreadID string) (domain.Thread, error)
	List(ctx context.Context, q ThreadQuery) (domain.Page[domain.Thread], error)
	Update(ctx context.Context, t domain.Thread) error
	// MarkOpened records the first open of a thread with a targeted write:
	// it sets opened_at (when null) and clears unread, idempotently, without
	// clobbering columns a concurrent mutation may have changed.
	MarkOpened(ctx context.Context, id string) error
	SetLabels(ctx context.Context, threadID string, labelIDs []string) error
	Search(ctx context.Context, userID, query string, limit int) ([]domain.Thread, error)
	// ListSnoozeDue returns threads whose snooze elapsed at or before now.
	ListSnoozeDue(ctx context.Context, now time.Time, limit int) ([]domain.Thread, error)
	// ListRemindersDue returns threads whose follow-up reminder is due.
	ListRemindersDue(ctx context.Context, now time.Time, limit int) ([]domain.Thread, error)
	// ClearSnooze wakes a snoozed thread (clears snoozed_until, marks unread)
	// with a targeted write that preserves concurrent user mutations.
	ClearSnooze(ctx context.Context, id string) error
	// ClearReminder fires a follow-up reminder (clears remind_at, marks unread).
	ClearReminder(ctx context.Context, id string) error
	// AppendSentMessage bumps message_count and advances last_message_at for a
	// newly delivered message, atomically in the database.
	AppendSentMessage(ctx context.Context, id string, sentAt time.Time) error
	// ListInboxBefore returns inbox threads (in_inbox, not snoozed) with
	// last_message_at strictly before the cutoff, oldest first.
	ListInboxBefore(ctx context.Context, userID string, before time.Time, limit int) ([]domain.Thread, error)
	// SetSummary/SetInstantReplies are targeted writes that never clobber
	// concurrent user mutations (same pattern as MarkOpened/ClearSnooze).
	SetSummary(ctx context.Context, threadID, summary string, at time.Time) error
	SetInstantReplies(ctx context.Context, threadID string, replies []string, at time.Time) error
	// SetReminderIfUnset arms remind_at only when currently null, so an AI
	// auto-reminder never overwrites a user-chosen reminder.
	SetReminderIfUnset(ctx context.Context, threadID string, remindAt time.Time) error
}

// OpensQuery pages the Recent Opens feed (keyset on opened_at DESC, id DESC).
type OpensQuery struct {
	UserID string
	Cursor string
	Limit  int
}

// AttachmentQuery filters the attachment quick-access search.
type AttachmentQuery struct {
	UserID   string
	Query    string // filename substring (ILIKE, trigram-backed)
	Contact  string // restrict to messages involving this email
	ThreadID string
	Cursor   string
	Limit    int
}

// MessageRepo persists mirrored mail messages.
type MessageRepo interface {
	Upsert(ctx context.Context, m domain.Message) (domain.Message, error)
	GetByID(ctx context.Context, id string) (domain.Message, error)
	GetByProviderID(ctx context.Context, accountID, providerMessageID string) (domain.Message, error)
	ListByThread(ctx context.Context, threadID string) ([]domain.Message, error)
	// ListSentByAccount returns the newest messages sent from the account's
	// own address (from_addr email match), newest first.
	ListSentByAccount(ctx context.Context, accountID, accountEmail string, limit int) ([]domain.Message, error)
	// ListOpens returns opened sent messages, newest open first.
	ListOpens(ctx context.Context, q OpensQuery) (domain.Page[domain.OpenEvent], error)
	// OpenHourHistogram buckets opens of mail the user sent to
	// recipientEmail by UTC hour of opened_at.
	OpenHourHistogram(ctx context.Context, userID, recipientEmail string) ([24]int, error)
	// SearchAttachments searches mirrored attachment metadata.
	SearchAttachments(ctx context.Context, q AttachmentQuery) (domain.Page[domain.AttachmentHit], error)
	// GetAttachment returns one attachment plus its owning message id.
	GetAttachment(ctx context.Context, attachmentID string) (domain.Attachment, string, error)
	// ContactSummary aggregates sender info from the local mirror.
	ContactSummary(ctx context.Context, userID, email string) (domain.ContactSummary, error)
}

// ReactionRepo persists emoji reactions.
type ReactionRepo interface {
	// Create is idempotent on (message_id, user_id, emoji): re-reacting
	// returns the existing row.
	Create(ctx context.Context, r domain.Reaction) (domain.Reaction, error)
	ListByMessages(ctx context.Context, messageIDs []string) (map[string][]domain.Reaction, error)
	// DeleteByEmoji removes the user's reaction; ErrNotFound when absent.
	DeleteByEmoji(ctx context.Context, messageID, userID, emoji string) error
}

// DraftRepo persists drafts, including scheduled (Send Later / undo-send
// grace) drafts awaiting delivery by the worker.
type DraftRepo interface {
	Create(ctx context.Context, d domain.Draft) (domain.Draft, error)
	GetByID(ctx context.Context, id string) (domain.Draft, error)
	ListByUser(ctx context.Context, userID string) ([]domain.Draft, error)
	Update(ctx context.Context, d domain.Draft) error
	Delete(ctx context.Context, id string) error
	// ListScheduledDue returns drafts with scheduledAt <= now.
	ListScheduledDue(ctx context.Context, now time.Time, limit int) ([]domain.Draft, error)
	// ClaimScheduled atomically claims a due scheduled draft for delivery by
	// clearing its scheduledAt; claimed is false when it was already claimed
	// (a concurrent worker or an earlier pass), guarding against sending the
	// same draft to recipients more than once.
	ClaimScheduled(ctx context.Context, id string) (claimed bool, err error)
	// RecordSendFailure records a failed delivery attempt: it increments the
	// attempt counter and stores errMsg, setting scheduled_at to nextAttemptAt
	// to retry, or leaving it clear (nil) to dead-letter the draft.
	RecordSendFailure(ctx context.Context, id string, nextAttemptAt *time.Time, errMsg string) error
	// GetAiGeneratedByThread returns the provisional AI draft for the thread
	// (ErrNotFound when none).
	GetAiGeneratedByThread(ctx context.Context, threadID string) (domain.Draft, error)
}

// SnippetRepo persists canned responses, personal (team_id NULL) or
// team-scoped (M2.7 team snippets).
type SnippetRepo interface {
	Create(ctx context.Context, s domain.Snippet) (domain.Snippet, error)
	GetByID(ctx context.Context, id string) (domain.Snippet, error)
	// ListByUser returns the user's PERSONAL (non-team) snippets; team
	// snippets are reached through ListByTeams.
	ListByUser(ctx context.Context, userID string) ([]domain.Snippet, error)
	// ListByTeams returns every snippet scoped to any of teamIDs.
	ListByTeams(ctx context.Context, teamIDs []string) ([]domain.Snippet, error)
	Update(ctx context.Context, s domain.Snippet) error
	Delete(ctx context.Context, id string) error
}

// LabelRepo persists mirrored mail labels.
type LabelRepo interface {
	// Upsert matches on (accountID, providerLabelID).
	Upsert(ctx context.Context, l domain.Label) (domain.Label, error)
	ListByAccount(ctx context.Context, accountID string) ([]domain.Label, error)
	GetByID(ctx context.Context, id string) (domain.Label, error)
	ListByUser(ctx context.Context, userID string) ([]domain.Label, error)
}

// CalendarRepo persists mirrored calendars.
type CalendarRepo interface {
	// Upsert matches on (accountID, providerCalendarID) and preserves the
	// local preferences IsVisible and Color on conflict.
	Upsert(ctx context.Context, c domain.Calendar) (domain.Calendar, error)
	GetByID(ctx context.Context, id string) (domain.Calendar, error)
	ListByUser(ctx context.Context, userID string) ([]domain.Calendar, error)
	ListByAccount(ctx context.Context, accountID string) ([]domain.Calendar, error)
	Update(ctx context.Context, c domain.Calendar) error
}

// EventRepo persists mirrored events.
type EventRepo interface {
	Upsert(ctx context.Context, e domain.Event) (domain.Event, error)
	GetByID(ctx context.Context, id string) (domain.Event, error)
	GetByProviderID(ctx context.Context, calendarID, providerEventID string) (domain.Event, error)
	// ListInRange returns events overlapping [from, to) on the user's
	// calendars, optionally restricted to calendarIDs.
	ListInRange(ctx context.Context, userID string, from, to time.Time, calendarIDs []string) ([]domain.Event, error)
	Delete(ctx context.Context, id string) error
	DeleteByProviderID(ctx context.Context, calendarID, providerEventID string) error
	Search(ctx context.Context, userID, query string, limit int) ([]domain.Event, error)
	// ClearGeo nulls location_lat/location_lon (M2.8 Task 12). Upsert
	// COALESCE-preserves coordinates on NULL input so coordinate-less
	// provider syncs cannot wipe them — a deliberate clear (location edited
	// without a fresh autocomplete pick) therefore needs this targeted write.
	ClearGeo(ctx context.Context, id string) error
}

// --- Tasks (M2.8) ---

// TaskQuery filters task lists. UserID is mandatory; zero values mean "no
// filter". Completed tasks are excluded unless IncludeCompleted is set.
// ScheduledFrom/ScheduledTo select tasks whose scheduled block overlaps
// [ScheduledFrom, ScheduledTo) — the calendar-grid query. DueFrom/DueTo
// select by due date — the rail's "due today" grouping.
type TaskQuery struct {
	UserID           string
	Source           domain.TaskSource
	IncludeCompleted bool
	ScheduledFrom    time.Time
	ScheduledTo      time.Time
	DueFrom          time.Time
	DueTo            time.Time
	UnscheduledOnly  bool
	Limit            int
}

// TaskRepo persists first-class tasks (local and mirrored external todos).
type TaskRepo interface {
	Create(ctx context.Context, t domain.Task) (domain.Task, error)
	GetByID(ctx context.Context, id string) (domain.Task, error)
	// GetByExternalID resolves a mirrored provider todo; domain.ErrNotFound
	// when the task was never synced.
	GetByExternalID(ctx context.Context, userID string, source domain.TaskSource, externalID string) (domain.Task, error)
	List(ctx context.Context, q TaskQuery) ([]domain.Task, error)
	Update(ctx context.Context, t domain.Task) error
	Delete(ctx context.Context, id string) error
	// DeleteBySource removes every mirrored task of one source for a user
	// (integration disconnect).
	DeleteBySource(ctx context.Context, userID string, source domain.TaskSource) error
}

// EventNoteRepo persists local-only notes attached to events (M2.8 Task 4).
type EventNoteRepo interface {
	// Upsert replaces the note for (eventID); empty BodyMD+Links deletes it.
	Upsert(ctx context.Context, n domain.EventNote) (domain.EventNote, error)
	GetByEventID(ctx context.Context, eventID string) (domain.EventNote, error)
}

// EventTemplateRepo persists per-user saved event defaults.
type EventTemplateRepo interface {
	Create(ctx context.Context, userID string, t domain.EventTemplate) (domain.EventTemplate, error)
	GetByID(ctx context.Context, id string) (domain.EventTemplate, string, error) // returns ownerUserID
	ListByUser(ctx context.Context, userID string) ([]domain.EventTemplate, error)
	Update(ctx context.Context, t domain.EventTemplate) error
	IncrementUsage(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
}

// CalendarSetRepo persists per-user named calendar groups.
type CalendarSetRepo interface {
	Create(ctx context.Context, userID string, s domain.CalendarSet) (domain.CalendarSet, error)
	GetByID(ctx context.Context, id string) (domain.CalendarSet, string, error) // returns ownerUserID
	ListByUser(ctx context.Context, userID string) ([]domain.CalendarSet, error)
	Update(ctx context.Context, s domain.CalendarSet) error
	Delete(ctx context.Context, id string) error
}

// DeviceRepo persists push-notification device registrations.
type DeviceRepo interface {
	// Upsert matches on (userID, token) so re-registrations are idempotent.
	Upsert(ctx context.Context, d domain.NotificationDevice) (domain.NotificationDevice, error)
	GetByID(ctx context.Context, id string) (domain.NotificationDevice, error)
	ListByUser(ctx context.Context, userID string) ([]domain.NotificationDevice, error)
	Delete(ctx context.Context, id string) error
}

// SyncState is an opaque incremental-sync cursor per account resource
// (e.g. "mail", "calendars", "events:<providerCalendarID>").
type SyncState struct {
	AccountID string
	Resource  string
	Cursor    string
	UpdatedAt time.Time
}

// SyncStateRepo persists incremental-sync cursors.
type SyncStateRepo interface {
	Get(ctx context.Context, accountID, resource string) (SyncState, error)
	Save(ctx context.Context, s SyncState) error
	// DeleteByAccount wipes all cursors when an account is disconnected.
	DeleteByAccount(ctx context.Context, accountID string) error
}

// PrefsRepo persists user preferences.
type PrefsRepo interface {
	// Get returns the user's preferences, or the zero value when absent (never ErrNotFound).
	Get(ctx context.Context, userID string) (domain.UserPrefs, error)
	Save(ctx context.Context, userID string, p domain.UserPrefs) error
}

// UserPreferences is the per-user, cross-device preference document (named
// theme, M2.6 Task 13). Distinct from domain.UserPrefs (inbox split layout):
// this document is served at GET/PUT /v1/me/preferences.
type UserPreferences struct {
	Theme string `json:"theme"`
}

// DefaultTheme is the named theme applied when a user has no stored preference.
const DefaultTheme = "neutral"

// ThemeNames is the allowed named-theme set (curated token palettes).
var ThemeNames = []string{"neutral", "ocean", "forest", "sunset"}

// ValidTheme reports whether name is one of ThemeNames.
func ValidTheme(name string) bool {
	for _, t := range ThemeNames {
		if t == name {
			return true
		}
	}
	return false
}

// UserPreferencesRepo persists the cross-device preference document.
type UserPreferencesRepo interface {
	// Get returns the user's preferences, with Theme defaulted to
	// DefaultTheme when absent (never ErrNotFound).
	Get(ctx context.Context, userID string) (UserPreferences, error)
	// Put upserts the preference document.
	Put(ctx context.Context, userID string, prefs UserPreferences) error
}

// BillingEventRepo is the webhook idempotency ledger keyed by the provider
// notification id (a replay reuses event_id with a NEW notification id and
// is deliberately re-applied through the occurred_at ordering guard).
type BillingEventRepo interface {
	// Record inserts the notification id; first is false when it was already
	// recorded (the webhook must then be skipped).
	Record(ctx context.Context, ev SubscriptionEvent) (first bool, err error)
}

// OAuthState is a pending provider-connect flow (CSRF state + PKCE verifier).
type OAuthState struct {
	State        string
	UserID       string
	Provider     domain.Provider
	RedirectURL  string
	CodeVerifier string
	ExpiresAt    time.Time
}

// OAuthStateRepo persists one-time OAuth states.
type OAuthStateRepo interface {
	Create(ctx context.Context, s OAuthState) error
	// Consume atomically fetches and deletes the state; domain.ErrNotFound
	// when missing (replayed or forged).
	Consume(ctx context.Context, state string) (OAuthState, error)
}

// AiJobRepo persists the background AI job queue.
type AiJobRepo interface {
	// Enqueue inserts the job; on (kind, thread_id) conflict it resets
	// run_after/locked_at so a message burst collapses into one fresh job.
	Enqueue(ctx context.Context, j domain.AiJob) error
	// ClaimDue atomically claims up to limit due jobs (run_after <= now,
	// unlocked or lock expired >10m) with FOR UPDATE SKIP LOCKED, bumping
	// attempts and locked_at. Safe under concurrent workers.
	ClaimDue(ctx context.Context, now time.Time, limit int) ([]domain.AiJob, error)
	// Complete deletes a finished job.
	Complete(ctx context.Context, id string) error
	// Fail records errMsg and re-arms the job at retryAt, or dead-letters it
	// (row deleted, error logged by caller) when retryAt is nil.
	Fail(ctx context.Context, id string, retryAt *time.Time, errMsg string) error
}

// ClassifierRepo persists user-defined natural-language classifiers.
type ClassifierRepo interface {
	Create(ctx context.Context, c domain.AiClassifier) (domain.AiClassifier, error)
	GetByID(ctx context.Context, id string) (domain.AiClassifier, error)
	ListByUser(ctx context.Context, userID string) ([]domain.AiClassifier, error)
	ListEnabledByUser(ctx context.Context, userID string) ([]domain.AiClassifier, error)
	Update(ctx context.Context, c domain.AiClassifier) error
	Delete(ctx context.Context, id string) error
}

// VoiceProfileRepo persists per-user writing-style profiles.
type VoiceProfileRepo interface {
	Get(ctx context.Context, userID string) (domain.VoiceProfile, error) // ErrNotFound when absent
	Upsert(ctx context.Context, p domain.VoiceProfile) error
}

// AiUsageRepo enforces the per-user daily AI budget.
type AiUsageRepo interface {
	// IncrementAndCheck atomically bumps the user's counter for day and
	// reports whether this call was within limit (counter <= limit after
	// the bump refuses: allowed=false leaves the counter unchanged).
	IncrementAndCheck(ctx context.Context, userID string, day time.Time, limit int) (allowed bool, err error)
}

// BookingLinkRepo persists booking links. Create returns domain.ErrConflict
// on a (case-insensitive) slug collision.
type BookingLinkRepo interface {
	Create(ctx context.Context, l domain.BookingLink) (domain.BookingLink, error)
	GetByID(ctx context.Context, id string) (domain.BookingLink, error)
	// GetBySlug matches case-insensitively; domain.ErrNotFound when absent.
	GetBySlug(ctx context.Context, slug string) (domain.BookingLink, error)
	ListByUser(ctx context.Context, userID string) ([]domain.BookingLink, error)
	Update(ctx context.Context, l domain.BookingLink) error
	Delete(ctx context.Context, id string) error
}

// BookingRepo persists bookings. CreateHold inserts a status="hold" row and
// returns domain.ErrConflict when the DB exclusion constraint rejects an
// overlapping active booking (SQLSTATE 23P01).
type BookingRepo interface {
	CreateHold(ctx context.Context, b domain.Booking) (domain.Booking, error)
	GetByID(ctx context.Context, id string) (domain.Booking, error)
	// ListActiveInRange returns hold+confirmed bookings overlapping [from,to).
	ListActiveInRange(ctx context.Context, linkID string, from, to time.Time) ([]domain.Booking, error)
	ListByUser(ctx context.Context, userID string, limit int) ([]domain.Booking, error)
	// Confirm promotes a hold: status="confirmed", event_id set, hold_expires_at cleared.
	Confirm(ctx context.Context, id, eventID string) error
	Cancel(ctx context.Context, id string) error
	// ExpireHolds cancels holds whose hold_expires_at <= now; returns count.
	ExpireHolds(ctx context.Context, now time.Time) (int64, error)
}

// PollRepo persists meeting polls and votes.
type PollRepo interface {
	Create(ctx context.Context, p domain.MeetingPoll) (domain.MeetingPoll, error)
	GetByID(ctx context.Context, id string) (domain.MeetingPoll, error)
	GetByToken(ctx context.Context, token string) (domain.MeetingPoll, error)
	ListByUser(ctx context.Context, userID string) ([]domain.MeetingPoll, error)
	Update(ctx context.Context, p domain.MeetingPoll) error
	Delete(ctx context.Context, id string) error
	// UpsertVotes replaces the voter's ballot (keyed poll_id+option_id+lower(email)).
	UpsertVotes(ctx context.Context, votes []domain.PollVote) error
	ListVotes(ctx context.Context, pollID string) ([]domain.PollVote, error)
}

// TimeProposalRepo persists propose-new-time counter-proposals.
type TimeProposalRepo interface {
	Create(ctx context.Context, p domain.TimeProposal) (domain.TimeProposal, error)
	GetByID(ctx context.Context, id string) (domain.TimeProposal, error)
	ListByEvent(ctx context.Context, eventID string) ([]domain.TimeProposal, error)
	Update(ctx context.Context, p domain.TimeProposal) error
}

// UserSettingsRepo persists per-user scheduling settings; Get returns a
// zero-value UserSettings (TimeZone "UTC") when no row exists.
type UserSettingsRepo interface {
	Get(ctx context.Context, userID string) (domain.UserSettings, error)
	Upsert(ctx context.Context, s domain.UserSettings) error
}

// --- Collaboration (M2.7) ---

// TeamRepo persists teams and memberships.
type TeamRepo interface {
	// Create inserts the team and its owner membership atomically.
	Create(ctx context.Context, t domain.Team, owner domain.TeamMember) (domain.Team, error)
	GetByID(ctx context.Context, id string) (domain.Team, error)
	ListByUser(ctx context.Context, userID string) ([]domain.Team, error)
	Update(ctx context.Context, t domain.Team) error
	Delete(ctx context.Context, id string) error
	// GetMember returns domain.ErrNotFound for non-members — the authz
	// primitive behind service-layer membership checks.
	GetMember(ctx context.Context, teamID, userID string) (domain.TeamMember, error)
	ListMembers(ctx context.Context, teamID string) ([]domain.TeamMember, error)
	// UpsertMember inserts or updates role/share_read_statuses.
	UpsertMember(ctx context.Context, m domain.TeamMember) error
	RemoveMember(ctx context.Context, teamID, userID string) error
	// CountByRole supports the last-owner invariant.
	CountByRole(ctx context.Context, teamID string, role domain.TeamRole) (int, error)
}

// TeamInvitationRepo persists email invitations (token stored hashed).
type TeamInvitationRepo interface {
	Create(ctx context.Context, inv domain.TeamInvitation) (domain.TeamInvitation, error)
	GetByID(ctx context.Context, id string) (domain.TeamInvitation, error)
	GetByTokenHash(ctx context.Context, tokenHash string) (domain.TeamInvitation, error)
	ListByTeam(ctx context.Context, teamID string) ([]domain.TeamInvitation, error)
	Update(ctx context.Context, inv domain.TeamInvitation) error
}

// --- Shared conversations (M2.7 Task 7) --------------------------------------

// ThreadShareRepo persists tokenized live thread shares (table
// thread_shares; token stored hashed — raw tokens never cross this
// boundary).
type ThreadShareRepo interface {
	Create(ctx context.Context, s domain.ThreadShare) (domain.ThreadShare, error)
	GetByID(ctx context.Context, id string) (domain.ThreadShare, error)
	// GetByTokenHash is the share-link lookup; domain.ErrNotFound when absent.
	GetByTokenHash(ctx context.Context, tokenHash string) (domain.ThreadShare, error)
	ListByThread(ctx context.Context, threadID string) ([]domain.ThreadShare, error)
	// Revoke stamps revoked_at; domain.ErrNotFound when the share is missing.
	Revoke(ctx context.Context, id string, at time.Time) error
}

// --- Team comments (M2.7 Task 9) ---

// CommentRepo persists team comments on mail threads. Soft-deleted rows
// (deleted_at set) are invisible to every read: a soft-deleted id is
// indistinguishable from a missing one (ErrNotFound).
type CommentRepo interface {
	Create(ctx context.Context, c domain.Comment) (domain.Comment, error)
	GetByID(ctx context.Context, id string) (domain.Comment, error)
	// ListByThreadTeam returns the live comments one team sees on one
	// thread, oldest first.
	ListByThreadTeam(ctx context.Context, threadID, teamID string) ([]domain.Comment, error)
	Update(ctx context.Context, c domain.Comment) error
	SoftDelete(ctx context.Context, id string, at time.Time) error
}

// --- Integrations (M2.8 Task 9) ----------------------------------------------

// IntegrationRepo persists per-user vendor OAuth connections
// (Todoist/HubSpot). Implementations MUST store access/refresh tokens
// encrypted at rest exactly like AccountRepo (AES-256-GCM with
// config.Crypto.TokenEncryptionKey); TokenSet crosses this boundary in
// plaintext only. Create returns domain.ErrConflict on a (user, vendor)
// collision; lookups return domain.ErrNotFound when absent.
type IntegrationRepo interface {
	Create(ctx context.Context, c domain.IntegrationConnection) (domain.IntegrationConnection, error)
	GetByID(ctx context.Context, id string) (domain.IntegrationConnection, error)
	GetByVendor(ctx context.Context, userID string, vendor domain.IntegrationVendor) (domain.IntegrationConnection, error)
	ListByUser(ctx context.Context, userID string) ([]domain.IntegrationConnection, error)
	ListByVendor(ctx context.Context, vendor domain.IntegrationVendor) ([]domain.IntegrationConnection, error)
	Update(ctx context.Context, c domain.IntegrationConnection) error
	Delete(ctx context.Context, id string) error
	SaveTokens(ctx context.Context, connectionID string, t TokenSet) error
	GetTokens(ctx context.Context, connectionID string) (TokenSet, error)
}

// ---------------------------------------------------------------------------
// Gateways (implemented by internal/adapter/out/{googleapi,msgraph,paddle,openrouter,push,authjwt})
// ---------------------------------------------------------------------------

// OAuthToken is the result of an authorization-code exchange or refresh.
type OAuthToken struct {
	TokenSet
	Scopes []string
	// Email is the provider account email when the grant reveals it
	// (id_token / userinfo); may be empty on refresh.
	Email string
}

// OAuthGateway runs the provider OAuth flow for offline mail/calendar access.
// Scopes are baked into the adapter for its provider. The flow uses PKCE
// (S256): codeChallenge is the base64url SHA-256 of the verifier sent on
// AuthURL, and the matching codeVerifier is replayed on Exchange.
type OAuthGateway interface {
	AuthURL(state, redirectURI, codeChallenge string) string
	Exchange(ctx context.Context, code, redirectURI, codeVerifier string) (OAuthToken, error)
	Refresh(ctx context.Context, refreshToken string) (OAuthToken, error)
}

// Canonical label keys used with MailProvider.ModifyLabels. Adapters map
// them to provider-native labels/folders/flags.
const (
	LabelKeyInbox   = "INBOX"
	LabelKeyStarred = "STARRED"
	LabelKeyUnread  = "UNREAD"
	LabelKeyTrash   = "TRASH"
	LabelKeySpam    = "SPAM"
)

// IncomingMessage is a synced message plus the raw headers the split-inbox
// classifier needs (List-Unsubscribe, Precedence, Content-Type, ...). Header
// keys are canonical MIME form (e.g. "List-Unsubscribe").
type IncomingMessage struct {
	Message domain.Message // ThreadID carries the PROVIDER thread id; ids are remapped by the sync service
	Headers map[string]string
}

// MailSyncPage is one page of incremental mail sync (Gmail historyId /
// Graph delta). NextCursor is the opaque cursor to persist; HasMore signals
// an immediate follow-up call.
type MailSyncPage struct {
	Labels     []domain.Label  // ProviderLabelID set; ID/AccountID left empty
	Threads    []domain.Thread // ProviderThreadID set; ID/AccountID left empty
	Messages   []IncomingMessage
	NextCursor string
	HasMore    bool
}

// OutgoingMessage is a message to deliver via the provider.
type OutgoingMessage struct {
	From     domain.EmailAddress
	To       []domain.EmailAddress
	Cc       []domain.EmailAddress
	Bcc      []domain.EmailAddress
	Subject  string
	BodyHTML string
	BodyText string
	// ProviderThreadID threads replies at the provider, when known.
	ProviderThreadID string
	// InReplyTo is the RFC 822 Message-ID being answered, when replying.
	InReplyTo string
}

// SentMessage identifies a delivered message at the provider.
type SentMessage struct {
	ProviderMessageID string
	ProviderThreadID  string
	SentAt            time.Time
}

// MailProvider is the Gmail / Microsoft Graph mail surface.
type MailProvider interface {
	// SyncMail performs incremental sync from cursor ("" = initial sync).
	SyncMail(ctx context.Context, accessToken, cursor string) (MailSyncPage, error)
	Send(ctx context.Context, accessToken string, msg OutgoingMessage) (SentMessage, error)
	// ModifyLabels adds/removes canonical label keys (LabelKey*) or
	// provider label ids on a thread.
	ModifyLabels(ctx context.Context, accessToken, providerThreadID string, add, remove []string) error
	// FetchAttachment downloads one attachment body on demand.
	FetchAttachment(ctx context.Context, accessToken, providerMessageID, providerAttachmentID string) (data []byte, mimeType string, err error)
}

// CalendarSyncPage is one page of incremental event sync for one calendar.
type CalendarSyncPage struct {
	Events     []domain.Event // ProviderEventID set; ID/CalendarID left empty
	DeletedIDs []string       // provider event ids removed upstream
	NextCursor string
	HasMore    bool
}

// CalendarProvider is the Google Calendar / Microsoft Graph calendar surface.
type CalendarProvider interface {
	SyncCalendars(ctx context.Context, accessToken string) ([]domain.Calendar, error)
	SyncEvents(ctx context.Context, accessToken, providerCalendarID, cursor string) (CalendarSyncPage, error)
	CreateEvent(ctx context.Context, accessToken, providerCalendarID string, in domain.EventInput) (domain.Event, error)
	UpdateEvent(ctx context.Context, accessToken, providerCalendarID, providerEventID string, patch domain.EventPatch) (domain.Event, error)
	DeleteEvent(ctx context.Context, accessToken, providerCalendarID, providerEventID string) error
	// RSVP writes the caller's response through to the provider. comment, when
	// non-empty, is carried to the organizer as the RSVP note (Google:
	// attendees[].comment; Graph: the respond action's comment field).
	RSVP(ctx context.Context, accessToken, providerCalendarID, providerEventID string, response domain.RsvpStatus, comment string) error
	// FreeBusy returns busy intervals per requested attendee email between
	// from and to (Google POST /freeBusy; Graph POST /me/calendar/getSchedule).
	// Emails absent from the result were not resolvable by the provider.
	FreeBusy(ctx context.Context, accessToken string, emails []string, from, to time.Time) (map[string][]domain.BusyInterval, error)
}

// CheckoutParams parameterizes a hosted checkout for the single annual plan
// (the price id is baked into the adapter). No URLs: the provider decides
// where checkout lands and the web /checkout page owns the success URL.
type CheckoutParams struct {
	UserID     string // bound via custom_data.user_id
	CustomerID string
}

// PortalURLs are temporary customer-portal links; never cache them.
// Cancel/UpdatePayment are empty when there is no subscription id.
type PortalURLs struct {
	Overview      string
	Cancel        string
	UpdatePayment string
}

// SubscriptionEvent is a verified, normalized provider subscription event —
// from a webhook or from a reconciliation read (then OccurredAt is "now").
type SubscriptionEvent struct {
	NotificationID    string
	EventID           string
	Type              string
	OccurredAt        time.Time
	CustomerID        string
	SubscriptionID    string
	UserID            string // custom_data.user_id when present (client-settable: never beats CustomerID)
	Status            domain.SubscriptionStatus
	CurrentPeriodEnd  *time.Time
	CancelAtPeriodEnd bool // scheduled_change.action == "cancel"
	// Ignored marks non-subscription event types (transaction.*, unknown):
	// acknowledged with 200 and never applied.
	Ignored bool
}

// Payments is the provider-neutral billing surface (docs/payments.md),
// implemented by internal/adapter/out/paddle.
type Payments interface {
	// EnsureCustomer returns the provider customer id for the user, looking
	// it up by exact email first and creating it otherwise.
	EnsureCustomer(ctx context.Context, user domain.User) (customerID string, err error)
	// CreateCheckout returns the hosted checkout URL for the annual plan.
	CreateCheckout(ctx context.Context, p CheckoutParams) (url string, err error)
	CreatePortalSession(ctx context.Context, customerID, subscriptionID string) (PortalURLs, error)
	// ParseWebhook verifies the provider signature header against now
	// (HMAC, constant-time, 5-minute tolerance, empty secret refused) and
	// normalizes the envelope. Signature failures must not parse the body.
	ParseWebhook(payload []byte, sigHeader string, now time.Time) (SubscriptionEvent, error)
	// GetSubscription reads the live subscription (reconciliation).
	GetSubscription(ctx context.Context, subscriptionID string) (SubscriptionEvent, error)
	// CancelSubscription cancels at period end, or immediately (account
	// deletion, program piece 3).
	CancelSubscription(ctx context.Context, subscriptionID string, immediately bool) error
}

// AI is the OpenRouter chat-completions surface.
type AI interface {
	// Complete sends one system+user exchange and returns freeform text.
	Complete(ctx context.Context, system, user string) (text, model string, err error)
	// CompleteJSON sends one system+user exchange requesting a JSON-object
	// response (OpenRouter response_format json_object) and decodes it into
	// out. It returns domain.ErrAIOutput-wrapped errors when the model's
	// reply is not valid JSON for out.
	CompleteJSON(ctx context.Context, system, user string, out any) (model string, err error)
}

// PushSender fans a notification out to one device via APNs / FCM / Web Push.
type PushSender interface {
	Send(ctx context.Context, device domain.NotificationDevice, title, body string, data map[string]string) error
}

// UnsubscribeGateway performs an RFC 8058 one-click list-unsubscribe POST
// against a sender-provided URL.
type UnsubscribeGateway interface {
	PostOneClick(ctx context.Context, url string) error
}

// ---------------------------------------------------------------------------
// Realtime event bus (M2.7, implemented by internal/adapter/out/eventbus)
// ---------------------------------------------------------------------------

// CollabEvent is a realtime collaboration notification fanned out over SSE.
type CollabEvent struct {
	// Topic scopes delivery: "team:<teamID>" | "share:<shareID>" | "user:<userID>".
	Topic   string          `json:"topic"`
	Type    string          `json:"type"` // "comment.created" | "comment.deleted" | "activity.updated" | "share.updated" | "mention"
	Payload json.RawMessage `json:"payload"`
}

// --- Calendar automation preferences (M2.8 Task 5) ---------------------------

// CalendarPrefsRepo persists the per-user calendar automation preference
// document (table calendar_prefs, one JSONB row per user).
type CalendarPrefsRepo interface {
	// Get returns DefaultCalendarPrefs(userID) when no row exists.
	Get(ctx context.Context, userID string) (domain.CalendarPrefs, error)
	Upsert(ctx context.Context, p domain.CalendarPrefs) error
	// ListAutomated returns prefs rows with any automation enabled — the
	// worker's fan-out set (no full-user table scan of defaults).
	ListAutomated(ctx context.Context) ([]domain.CalendarPrefs, error)
}

// --- Managed events (M2.8 Task 6) -------------------------------------------

// ManagedEventRepo persists the automation engine's ownership ledger
// (table managed_events): which mirrored events the engine created and may
// therefore move, shrink, or delete on later passes. Rows cascade away when
// the mirrored event row is deleted.
type ManagedEventRepo interface {
	Create(ctx context.Context, m domain.ManagedEvent) error
	// GetByEventID returns domain.ErrNotFound when the event is not managed.
	GetByEventID(ctx context.Context, eventID string) (domain.ManagedEvent, error)
	ListByUser(ctx context.Context, userID string, kind domain.ManagedKind) ([]domain.ManagedEvent, error)
	ListBySourceEvent(ctx context.Context, sourceEventID string) ([]domain.ManagedEvent, error)
	Delete(ctx context.Context, eventID string) error
}

// EventBus fans CollabEvents out to in-process subscribers. Publish never
// blocks (slow subscribers drop events — SSE clients re-sync on reconnect).
// Two implementations: adapter/out/eventbus (in-memory, the API's broker)
// and adapter/out/pgbus (Postgres NOTIFY publisher + LISTEN forwarder that
// bridges worker-published events into the API's in-memory bus, envelope
// {topic,type} only — payloads never cross processes).
type EventBus interface {
	Publish(ev CollabEvent)
	// Subscribe returns a channel of events for the given topics and a
	// cancel func. The channel closes on cancel.
	Subscribe(topics []string) (<-chan CollabEvent, func())
}

// ---------------------------------------------------------------------------
// Weather (M2.8 Task 13, implemented by internal/adapter/out/openmeteo)
// ---------------------------------------------------------------------------

// WeatherProvider fetches a multi-day daily forecast for one location.
// Weather is best-effort decoration on calendar surfaces: implementations
// must use a short timeout, and callers must degrade gracefully (hide the
// chip) on any error — a vendor failure never fails a calendar request.
type WeatherProvider interface {
	DailyForecast(ctx context.Context, lat, lon float64, timeZone string, days int) ([]domain.DayForecast, error)
}

// ---------------------------------------------------------------------------
// Maps (M2.8 Task 11, implemented by internal/adapter/out/nominatim)
// ---------------------------------------------------------------------------

// MapsProvider is the geocoding + routing vendor surface (Nominatim/OSRM).
// Left unwired when MAPS_NOMINATIM_URL is not configured — consumers must
// degrade gracefully (501 routes, hidden UI affordances).
type MapsProvider interface {
	// Autocomplete returns up to limit place suggestions for a partial query.
	Autocomplete(ctx context.Context, query string, limit int) ([]domain.Place, error)
	// TravelTime estimates door-to-door duration between two points.
	TravelTime(ctx context.Context, fromLat, fromLon, toLat, toLon float64, mode domain.TravelMode) (time.Duration, error)
}

// ---------------------------------------------------------------------------
// Todo tools (M2.8 Task 10, implemented by internal/adapter/out/todoist)
// ---------------------------------------------------------------------------

// TodoSyncPage is one page of incremental todo sync.
type TodoSyncPage struct {
	Tasks      []domain.Task // ExternalID+Source set; ID/UserID left empty
	DeletedIDs []string      // provider task ids removed or completed upstream
	NextCursor string
	HasMore    bool
}

// TodoProvider is the external todo-tool surface (Todoist first; Things,
// Notion, Linear later). SyncTasks performs incremental sync from cursor
// ("" = full sync; Todoist uses the Sync v9 sync_token). loc resolves
// floating (zone-less) vendor due datetimes — the connection owner's
// CalendarPrefs timezone; nil falls back to UTC.
type TodoProvider interface {
	Source() domain.TaskSource
	SyncTasks(ctx context.Context, accessToken, cursor string, loc *time.Location) (TodoSyncPage, error)
	CompleteTask(ctx context.Context, accessToken, externalID string) error
	ReopenTask(ctx context.Context, accessToken, externalID string) error
}

// --- CRM integrations (M2.8 Task 16) ----------------------------------------

// CrmProvider is a vendor CRM adapter (HubSpot first).
type CrmProvider interface {
	Vendor() domain.IntegrationVendor
	// ContactContext resolves a contact by email with associated open deals;
	// a missing contact returns CrmContext{Contact: nil}, not an error.
	ContactContext(ctx context.Context, accessToken, email string) (domain.CrmContext, error)
	// LogEmail records an email engagement on the contact's timeline.
	LogEmail(ctx context.Context, accessToken string, log domain.CrmEmailLog) error
}

// CrmConnection is the narrow, consumer-side view of a per-user integration
// connection the CRM service needs: vendor plus current (decrypted) tokens.
//
// NOTE(M2.8 integration): the parallel integration-OAuth task (Task 9) owns
// the real storage — integration_connections, the connect/callback flow, and
// AES-GCM token encryption at rest. This interface is deliberately minimal so
// Task 9's repository can implement (or be thinly adapted to) it at merge;
// the CRM side must never grow its own OAuth/token storage.
type CrmConnection struct {
	ID     string
	UserID string
	Vendor domain.IntegrationVendor
	Tokens TokenSet
}

// CrmConnectionStore reads and refreshes per-user integration connections.
type CrmConnectionStore interface {
	// ListByUser returns the user's integration connections with decrypted
	// tokens; an empty slice (never an error) when none exist.
	ListByUser(ctx context.Context, userID string) ([]CrmConnection, error)
	// UpdateTokens persists refreshed tokens for a connection
	// (implementations encrypt at rest).
	UpdateTokens(ctx context.Context, connectionID string, t TokenSet) error
}

// ---------------------------------------------------------------------------
// Interesting-calendar ICS subscriptions (M2.8 Task 15)
// ---------------------------------------------------------------------------

// CalendarSubscriptionRepo persists user-added ICS feed subscriptions and
// their expanded, read-only event mirrors (subscription_events).
type CalendarSubscriptionRepo interface {
	Create(ctx context.Context, s domain.CalendarSubscription) (domain.CalendarSubscription, error)
	GetByID(ctx context.Context, id string) (domain.CalendarSubscription, error)
	ListByUser(ctx context.Context, userID string) ([]domain.CalendarSubscription, error)
	// ListDue returns subscriptions not fetched since `since` (hourly cadence).
	ListDue(ctx context.Context, since time.Time) ([]domain.CalendarSubscription, error)
	Update(ctx context.Context, s domain.CalendarSubscription) error
	Delete(ctx context.Context, id string) error
	// ReplaceEvents atomically swaps the expanded occurrence set for a
	// subscription (12-month horizon), preserving nothing — feeds own truth.
	// Event UIDs travel in domain.Event.ProviderEventID.
	ReplaceEvents(ctx context.Context, subscriptionID string, events []domain.Event) error
	// ListEventsInRange mirrors EventRepo.ListInRange for subscription
	// events: occurrences overlapping [from, to) across the user's VISIBLE
	// subscriptions, with SubscriptionID set and Status confirmed.
	ListEventsInRange(ctx context.Context, userID string, from, to time.Time) ([]domain.Event, error)
}

// IcsFetcher retrieves and parses a feed. notModified is true when the
// server honored the cached validator (etag) and events must be kept.
type IcsFetcher interface {
	Fetch(ctx context.Context, url, etag string) (cal ics.Calendar, newEtag string, notModified bool, err error)
}

// ---------------------------------------------------------------------------
// Travel alerts (M2.8 Task 12, implemented by internal/adapter/out/postgres)
// ---------------------------------------------------------------------------

// TravelAlertRepo persists leave-now alerts, one per event (PK event_id,
// ON DELETE CASCADE from events).
type TravelAlertRepo interface {
	// Upsert creates or refreshes the alert. A changed leave_at clears
	// sent_at (a moved event re-arms its alert); an unchanged leave_at
	// preserves it, so idempotent passes never cause a re-send.
	Upsert(ctx context.Context, eventID, userID string, leaveAt time.Time) error
	// ListDue returns unsent alerts with leave_at <= now, oldest first.
	ListDue(ctx context.Context, now time.Time, limit int) ([]domain.TravelAlert, error)
	// MarkSent stamps sent_at — called only AFTER an observed successful
	// push (honesty policy); domain.ErrNotFound when the alert is missing.
	MarkSent(ctx context.Context, eventID string, at time.Time) error
	// Delete removes the alert; idempotent (absence is not an error).
	Delete(ctx context.Context, eventID string) error
}

// --- Time insights (M2.8 Task 17) --------------------------------------------

// InsightsManagedEventRepo is the read-only, all-kinds view of the
// managed-events ledger the insights aggregation consumes: every managed
// event for one user regardless of kind, so events of automation kinds that
// don't exist yet are still categorized (never mistaken for meetings)
// without any insights change.
type InsightsManagedEventRepo interface {
	ListAllByUser(ctx context.Context, userID string) ([]domain.ManagedEvent, error)
}

// ---------------------------------------------------------------------------
// Transactional email (piece 2)
// ---------------------------------------------------------------------------

// Email is a transactional message sent from the instance's own address
// (SMTP_FROM), independent of any user's connected mailbox.
type Email struct {
	To      []string
	ReplyTo string // optional
	Subject string
	Text    string // required
	HTML    string // optional; multipart/alternative when set
}

// Mailer delivers transactional email. Implementations are safe for
// concurrent use and honour ctx deadlines.
type Mailer interface {
	Send(ctx context.Context, msg Email) error
}
