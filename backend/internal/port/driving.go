// Package port defines the hexagon's edges: driving (use-case) interfaces
// consumed by inbound adapters, and driven interfaces implemented by
// outbound adapters. It depends only on domain and the standard library.
package port

import (
	"context"
	"time"

	"calendium/backend/internal/domain"
)

// UserService resolves the authenticated Calendium user.
type UserService interface {
	// EnsureUser upserts the user row from a verified identity (first
	// request creates it) and returns the current user.
	EnsureUser(ctx context.Context, id Identity) (domain.User, error)
	GetUser(ctx context.Context, userID string) (domain.User, error)
	// GetPreferences returns the user's cross-device preferences (named
	// theme), defaulting the theme to DefaultTheme when unset.
	GetPreferences(ctx context.Context, userID string) (UserPreferences, error)
	// UpdatePreferences validates (theme must be in ThemeNames →
	// domain.ErrValidation otherwise) and stores the preference document,
	// returning what was saved.
	UpdatePreferences(ctx context.Context, userID string, prefs UserPreferences) (UserPreferences, error)
}

// BillingService implements the $50/yr Stripe flow (docs/payments.md).
type BillingService interface {
	// GetSubscription returns the user's subscription, or a status "none"
	// placeholder when they have never subscribed.
	GetSubscription(ctx context.Context, userID string) (domain.Subscription, error)
	CreateCheckoutSession(ctx context.Context, userID, successURL, cancelURL string) (url string, err error)
	CreatePortalSession(ctx context.Context, userID, returnURL string) (url string, err error)
	// HandleWebhook verifies, deduplicates, and applies a Stripe webhook.
	HandleWebhook(ctx context.Context, payload []byte, sigHeader string) error
	// RequireActive returns domain.ErrPaymentRequired unless the
	// subscription currently grants access (trialing, active, or past_due
	// within the 7-day grace window).
	RequireActive(ctx context.Context, userID string) error
}

// AccountService manages connected Google / Microsoft accounts.
type AccountService interface {
	List(ctx context.Context, userID string) ([]domain.ConnectedAccount, error)
	// BeginConnect starts the provider OAuth flow and returns the URL to
	// open in a browser. redirectURL is the client's own return target
	// (stored in state for the final callback redirect, never sent to the
	// provider); requestBaseURL is the API's public origin used to build the
	// provider redirect_uri when PUBLIC_API_URL is unset.
	BeginConnect(ctx context.Context, userID string, provider domain.Provider, redirectURL, requestBaseURL string) (authURL string, err error)
	// CompleteConnect handles the OAuth callback (state-validated) and stores
	// the account with encrypted tokens. It returns the client redirect URL
	// stored in state so the callback can 302 the browser back to the client;
	// on failure after the state is consumed, the redirect URL is still
	// returned (empty only when the state itself was invalid).
	CompleteConnect(ctx context.Context, provider domain.Provider, state, code, requestBaseURL string) (account domain.ConnectedAccount, clientRedirect string, err error)
	// SetVipSenders replaces the account's VIP-sender list (addresses routed
	// to the "vip" split at ingest).
	SetVipSenders(ctx context.Context, userID, accountID string, vipSenders []string) (domain.ConnectedAccount, error)
	Disconnect(ctx context.Context, userID, accountID string) error
	// SetSignature replaces the account's rich signature (sanitized HTML).
	SetSignature(ctx context.Context, userID, accountID, signatureHTML string) (domain.ConnectedAccount, error)
	// SetAutoBcc replaces the account's auto-BCC list applied at send.
	SetAutoBcc(ctx context.Context, userID, accountID string, autoBcc []string) (domain.ConnectedAccount, error)
}

// DraftInput is the create/update draft payload (autosave-friendly).
type DraftInput struct {
	AccountID   string                `json:"accountId"`
	ThreadID    *string               `json:"threadId"`
	To          []domain.EmailAddress `json:"to"`
	Cc          []domain.EmailAddress `json:"cc"`
	Bcc         []domain.EmailAddress `json:"bcc"`
	Subject     string                `json:"subject"`
	BodyHTML    string                `json:"bodyHtml"`
	ScheduledAt *time.Time            `json:"scheduledAt"`
}

// SnippetInput is the create/update snippet payload.
type SnippetInput struct {
	Name     string  `json:"name"`
	Shortcut *string `json:"shortcut"`
	BodyHTML string  `json:"bodyHtml"`
	// TeamID scopes a NEW snippet to a team the caller belongs to (any
	// role). Create only — a snippet's scope is immutable afterwards, so
	// update ignores it. Nil creates a personal snippet.
	TeamID *string `json:"teamId"`
}

// BulkActionResult reports a bulk mutation: mutated threads plus the ids
// that failed (not found / foreign / provider error) — never a partial 500.
type BulkActionResult struct {
	Threads   []domain.Thread `json:"threads"`
	FailedIDs []string        `json:"failedIds"`
}

// MailService covers threads, drafts, and snippets.
type MailService interface {
	ListThreads(ctx context.Context, userID string, q ThreadQuery) (domain.Page[domain.Thread], error)
	GetThread(ctx context.Context, userID, threadID string) (domain.Thread, []domain.Message, error)
	// ActOnThread applies archive/trash/star/... locally and writes through
	// to the provider.
	ActOnThread(ctx context.Context, userID, threadID string, action domain.ThreadAction) (domain.Thread, error)
	// BulkActOnThreads applies the same action to up to maxBulkThreads owned
	// threads, skipping (and reporting) any that are missing or foreign
	// instead of failing the whole request.
	BulkActOnThreads(ctx context.Context, userID string, threadIDs []string, action domain.ThreadAction) (BulkActionResult, error)
	// MarkThreadOpened records that the owner opened the thread: it sets
	// OpenedAt (when null), clears unread, and writes the read state through
	// to the provider. Idempotent.
	MarkThreadOpened(ctx context.Context, userID, threadID string) error
	// SnoozeThread hides the thread until the given time; the worker
	// resurfaces it when due.
	SnoozeThread(ctx context.Context, userID, threadID string, until time.Time) (domain.Thread, error)
	// UnsnoozeThread clears a pending snooze (client-side undo of snooze)
	// without marking the thread unread.
	UnsnoozeThread(ctx context.Context, userID, threadID string) (domain.Thread, error)
	// SetReminder sets (or clears, with nil) a follow-up reminder.
	SetReminder(ctx context.Context, userID, threadID string, remindAt *time.Time) (domain.Thread, error)
	// ArchiveOlderThan is Get Me To Zero: archive every inbox thread older than
	// the cutoff, paging until none remain. Returns how many were archived.
	ArchiveOlderThan(ctx context.Context, userID string, olderThan time.Time) (archived int, err error)

	CreateDraft(ctx context.Context, userID string, in DraftInput) (domain.Draft, error)
	UpdateDraft(ctx context.Context, userID, draftID string, in DraftInput) (domain.Draft, error)
	GetDraft(ctx context.Context, userID, draftID string) (domain.Draft, error)
	ListDrafts(ctx context.Context, userID string) ([]domain.Draft, error)
	DeleteDraft(ctx context.Context, userID, draftID string) error
	// SendDraft queues the draft for delivery: at its ScheduledAt when set
	// in the future (Send Later), otherwise after the configurable
	// undo-send grace. It returns a provisional message; the worker
	// performs the actual provider send.
	SendDraft(ctx context.Context, userID, draftID string) (domain.Message, error)
	// UnsendDraft cancels a queued send within the undo-send grace window by
	// clearing the draft's ScheduledAt, returning the reverted draft. It
	// returns domain.ErrConflict when the worker has already delivered the
	// draft (the grace window elapsed).
	UnsendDraft(ctx context.Context, userID, draftID string) (domain.Draft, error)

	ListSnippets(ctx context.Context, userID string) ([]domain.Snippet, error)
	CreateSnippet(ctx context.Context, userID string, in SnippetInput) (domain.Snippet, error)
	UpdateSnippet(ctx context.Context, userID, snippetID string, in SnippetInput) (domain.Snippet, error)
	DeleteSnippet(ctx context.Context, userID, snippetID string) error

	ListLabels(ctx context.Context, userID string) ([]domain.Label, error)
	// SetThreadLabel adds (add=true) or removes a user/system label on a
	// thread, writing through to the provider with the label's provider id.
	SetThreadLabel(ctx context.Context, userID, threadID, labelID string, add bool) (domain.Thread, error)
	// BulkSetLabel applies the same label mutation to up to maxBulkThreads
	// owned threads, skipping (and reporting) any that are missing or
	// foreign instead of failing the whole request.
	BulkSetLabel(ctx context.Context, userID string, threadIDs []string, labelID string, add bool) (BulkActionResult, error)
	// UnsubscribeThread executes the thread's List-Unsubscribe: RFC 8058
	// one-click when available, else a mailto send, else it reports the URL
	// for the client to open. domain.ErrValidation when the thread has none.
	UnsubscribeThread(ctx context.Context, userID, threadID string) (UnsubscribeResult, error)

	ListOpens(ctx context.Context, userID, cursor string, limit int) (domain.Page[domain.OpenEvent], error)
	// SuggestSendTime returns domain.ErrNotFound when history is too thin
	// (fewer than 5 recorded opens for the recipient).
	SuggestSendTime(ctx context.Context, userID, recipientEmail string) (domain.SendSuggestion, error)
	SearchAttachments(ctx context.Context, userID string, q AttachmentQuery) (domain.Page[domain.AttachmentHit], error)
	// GetAttachmentContent fetches an attachment body from the provider.
	GetAttachmentContent(ctx context.Context, userID, attachmentID string) (data []byte, mimeType, filename string, err error)
	GetContact(ctx context.Context, userID, email string) (domain.ContactSummary, error)
	// ReactToMessage stores the reaction; when sendReply is true it also
	// queues a tiny threaded reply through the scheduled-send pipeline.
	ReactToMessage(ctx context.Context, userID, messageID, emoji string, sendReply bool) (ReactionResult, error)
	RemoveReaction(ctx context.Context, userID, messageID, emoji string) error
}

// ReactionResult is a stored reaction plus the tiny-reply draft id when
// the reaction was also queued for delivery (undo-send capable).
type ReactionResult struct {
	Reaction domain.Reaction `json:"reaction"`
	DraftID  *string         `json:"draftId"`
}

// UnsubscribeResult reports how an unsubscribe was (or must be) performed:
// "one_click" and "mailto" completed server-side; "link" returns the URL the
// client must open in a browser.
type UnsubscribeResult struct {
	Method string `json:"method"` // "one_click" | "mailto" | "link"
	URL    string `json:"url,omitempty"`
}

// CalendarPatch is the PATCH /v1/calendars/{id} payload; nil = unchanged.
type CalendarPatch struct {
	IsVisible *bool   `json:"isVisible"`
	Color     *string `json:"color"`
}

// CalendarService covers calendars, events (provider write-through), rsvp,
// availability, event templates, and calendar sets.
type CalendarService interface {
	ListCalendars(ctx context.Context, userID string) ([]domain.Calendar, error)
	UpdateCalendar(ctx context.Context, userID, calendarID string, patch CalendarPatch) (domain.Calendar, error)
	ListEvents(ctx context.Context, userID string, from, to time.Time, calendarIDs []string) ([]domain.Event, error)
	CreateEvent(ctx context.Context, userID string, in domain.EventInput) (domain.Event, error)
	UpdateEvent(ctx context.Context, userID, eventID string, patch domain.EventPatch) (domain.Event, error)
	DeleteEvent(ctx context.Context, userID, eventID string) error
	RSVP(ctx context.Context, userID, eventID string, response domain.RsvpStatus) (domain.Event, error)
	// Availability returns free windows of at least slotDuration between
	// from and to, computed from the user's visible calendars.
	Availability(ctx context.Context, userID string, from, to time.Time, slotDuration time.Duration) ([]domain.AvailabilitySlot, error)

	// Event template methods
	ListEventTemplates(ctx context.Context, userID string) ([]domain.EventTemplate, error)
	CreateEventTemplate(ctx context.Context, userID string, in domain.EventTemplateInput) (domain.EventTemplate, error)
	UpdateEventTemplate(ctx context.Context, userID, templateID string, in domain.EventTemplateInput) (domain.EventTemplate, error)
	DeleteEventTemplate(ctx context.Context, userID, templateID string) error
	UseEventTemplate(ctx context.Context, userID, templateID string) error

	// Calendar set methods
	ListCalendarSets(ctx context.Context, userID string) ([]domain.CalendarSet, error)
	CreateCalendarSet(ctx context.Context, userID string, in domain.CalendarSetInput) (domain.CalendarSet, error)
	UpdateCalendarSet(ctx context.Context, userID, setID string, in domain.CalendarSetInput) (domain.CalendarSet, error)
	DeleteCalendarSet(ctx context.Context, userID, setID string) error
}

// SearchResult is the unified GET /v1/search response.
type SearchResult struct {
	Threads []domain.Thread `json:"threads"`
	Events  []domain.Event  `json:"events"`
}

// SearchService is unified search over threads and events.
type SearchService interface {
	Search(ctx context.Context, userID, query string) (SearchResult, error)
}

// ClassifierInput is the create/update payload for a user-defined
// natural-language classifier (POST/PATCH /v1/classifiers). At least one of
// TargetSplit/LabelName is required; TargetSplit, when set, is validated via
// domain.ParseInboxSplit.
type ClassifierInput struct {
	Name        string            `json:"name"`
	Prompt      string            `json:"prompt"`
	TargetSplit domain.InboxSplit `json:"targetSplit,omitempty"`
	LabelName   string            `json:"labelName,omitempty"`
	Enabled     bool              `json:"enabled"`
}

// AIService is the OpenRouter-backed compose/reply/summarize/ask endpoint,
// plus CRUD for the user's custom natural-language classifiers (applied at
// ingest by AIJobService's classify job kind).
type AIService interface {
	Compose(ctx context.Context, userID string, req domain.AiComposeRequest) (domain.AiComposeResponse, error)
	// Ask answers a natural-language question over the user's mailbox (or one
	// thread when req.ThreadID is set) and returns the answer plus the thread/
	// message ids it drew on. Counts against the daily AI budget.
	Ask(ctx context.Context, userID string, req domain.AiAskRequest) (domain.AiAskResponse, error)
	// ProposeEvent reads the thread and proposes a calendar event (title,
	// attendees from participants, start/end aligned to real availability).
	// The client reviews and creates it via the existing POST /v1/events.
	ProposeEvent(ctx context.Context, userID, threadID string) (domain.AiEventProposal, error)
	// InstantReplies returns the 3 cached quick replies for the thread,
	// generating and caching them on demand when absent (on-open fallback for
	// splits the worker skips). Counts against the daily AI budget only when
	// it generates.
	InstantReplies(ctx context.Context, userID, threadID string) ([]string, error)
	ListClassifiers(ctx context.Context, userID string) ([]domain.AiClassifier, error)
	CreateClassifier(ctx context.Context, userID string, in ClassifierInput) (domain.AiClassifier, error)
	UpdateClassifier(ctx context.Context, userID, classifierID string, in ClassifierInput) (domain.AiClassifier, error)
	DeleteClassifier(ctx context.Context, userID, classifierID string) error
}

// DeviceService manages push-notification device registrations.
type DeviceService interface {
	Register(ctx context.Context, userID string, platform domain.DevicePlatform, token string) (domain.NotificationDevice, error)
	Unregister(ctx context.Context, userID, deviceID string) error
}

// PrefsService covers user preferences like split reordering.
type PrefsService interface {
	GetPrefs(ctx context.Context, userID string) (domain.UserPrefs, error)
	UpdatePrefs(ctx context.Context, userID string, p domain.UserPrefs) (domain.UserPrefs, error)
}

// SyncService is consumed by cmd/worker: provider polling plus scheduled
// work (delayed sends, snooze wake-ups, follow-up reminders).
type SyncService interface {
	// SyncAccount runs one incremental mail + calendar sync pass.
	SyncAccount(ctx context.Context, accountID string) error
	// ProcessDueWork delivers due scheduled drafts and resurfaces due
	// snoozes/reminders (with push notifications).
	ProcessDueWork(ctx context.Context) error
}

// AIJobService drains the background AI job queue (consumed by cmd/worker).
type AIJobService interface {
	// ProcessDueAiJobs claims and executes one batch of due AI jobs.
	// It is a no-op returning nil when AI is not configured.
	ProcessDueAiJobs(ctx context.Context) error
}

// BookingLinkInput is the create/update booking-link payload.
type BookingLinkInput struct {
	Slug                string                      `json:"slug"`
	Title               string                      `json:"title"`
	Description         string                      `json:"description,omitempty"`
	CalendarID          string                      `json:"calendarId"`
	DurationMinutes     int                         `json:"durationMinutes"`
	TimeZone            string                      `json:"timeZone"`
	Windows             []domain.AvailabilityWindow `json:"windows"`
	BufferBeforeMin     int                         `json:"bufferBeforeMin"`
	BufferAfterMin      int                         `json:"bufferAfterMin"`
	DailyLimit          int                         `json:"dailyLimit"`
	MinNoticeMin        int                         `json:"minNoticeMin"`
	MaxAdvanceDays      int                         `json:"maxAdvanceDays"`
	RespectWorkingHours bool                        `json:"respectWorkingHours"`
	AddConferencing     bool                        `json:"addConferencing"`
	Active              bool                        `json:"active"`
	// TeamID scopes the link to a team (M2.7 Task 14, collective
	// availability); nil/empty = personal link.
	TeamID *string `json:"teamId,omitempty"`
	// MemberUserIDs lists team members to include; requires TeamID. Each must
	// be a member of the team and have shared free/busy with it (or be the
	// creator, who is implicit).
	MemberUserIDs []string `json:"memberUserIds,omitempty"`
}

// PublicBookingPage is the public GET /v1/public/booking/{slug} document —
// no owner PII beyond display name.
type PublicBookingPage struct {
	Slug            string  `json:"slug"`
	Title           string  `json:"title"`
	Description     *string `json:"description"`
	OwnerName       string  `json:"ownerName"`
	DurationMinutes int     `json:"durationMinutes"`
	TimeZone        string  `json:"timeZone"` // owner's link TZ (for "times shown in…" hints)
}

// BookingRequest is the public booking payload.
type BookingRequest struct {
	Start        time.Time `json:"start"`
	InviteeName  string    `json:"inviteeName"`
	InviteeEmail string    `json:"inviteeEmail"`
	InviteeTZ    string    `json:"inviteeTimeZone"`
	Note         string    `json:"note,omitempty"`
}

// PollInput is the create-poll payload.
type PollInput struct {
	Title           string              `json:"title"`
	Description     string              `json:"description,omitempty"`
	CalendarID      string              `json:"calendarId"`
	DurationMinutes int                 `json:"durationMinutes"`
	Options         []domain.PollOption `json:"options"` // IDs assigned server-side
}

// PublicPoll is the public poll document incl. anonymized tallies.
type PublicPoll struct {
	Token           string               `json:"token"`
	Title           string               `json:"title"`
	Description     *string              `json:"description"`
	OrganizerName   string               `json:"organizerName"`
	DurationMinutes int                  `json:"durationMinutes"`
	Status          domain.PollStatus    `json:"status"`
	Options         []domain.PollOption  `json:"options"`
	Tallies         map[string]PollTally `json:"tallies"` // optionID → tally
	WinnerOptionID  *string              `json:"winnerOptionId"`
}

// PollTally aggregates votes for one option.
type PollTally struct {
	Yes      int `json:"yes"`
	No       int `json:"no"`
	IfNeeded int `json:"ifNeeded"`
}

// PollBallot is one public voter's submission.
type PollBallot struct {
	VoterEmail string                           `json:"voterEmail"`
	VoterName  string                           `json:"voterName"`
	Choices    map[string]domain.PollVoteChoice `json:"choices"` // optionID → choice
}

// TimeProposalInput is the propose-new-time payload.
type TimeProposalInput struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Note  string    `json:"note,omitempty"`
}

// FreeBusyRequest asks for guest busy intervals (Find-a-Time grid).
type FreeBusyRequest struct {
	Emails []string  `json:"emails"`
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
}

// SchedulingService covers booking links, public booking, meeting polls,
// propose-new-time, and guest free/busy.
type SchedulingService interface {
	// Owner surface (authenticated).
	CreateLink(ctx context.Context, userID string, in BookingLinkInput) (domain.BookingLink, error)
	UpdateLink(ctx context.Context, userID, linkID string, in BookingLinkInput) (domain.BookingLink, error)
	ListLinks(ctx context.Context, userID string) ([]domain.BookingLink, error)
	DeleteLink(ctx context.Context, userID, linkID string) error
	ListBookings(ctx context.Context, userID string) ([]domain.Booking, error)
	CancelBooking(ctx context.Context, userID, bookingID string) error

	// Public surface (unauthenticated, rate limited).
	PublicPage(ctx context.Context, slug string) (PublicBookingPage, error)
	// PublicSlots returns bookable start times between from and to.
	PublicSlots(ctx context.Context, slug string, from, to time.Time) ([]domain.AvailabilitySlot, error)
	// Book runs the hold → provider free/busy re-check → event create →
	// confirm → email pipeline. domain.ErrConflict when the slot is taken.
	Book(ctx context.Context, slug string, req BookingRequest) (domain.Booking, error)

	// Meeting polls.
	CreatePoll(ctx context.Context, userID string, in PollInput) (domain.MeetingPoll, error)
	ListPolls(ctx context.Context, userID string) ([]domain.MeetingPoll, error)
	// ConfirmPoll picks the winner, creates the event (write-through), and
	// emails every yes/if_needed voter an invitation-style notice.
	ConfirmPoll(ctx context.Context, userID, pollID, optionID string) (domain.MeetingPoll, error)
	DeletePoll(ctx context.Context, userID, pollID string) error
	PublicPollByToken(ctx context.Context, token string) (PublicPoll, error)
	VotePoll(ctx context.Context, token string, ballot PollBallot) (PublicPoll, error)

	// Propose-new-time (authenticated invitee → organizer accept).
	ProposeTime(ctx context.Context, userID, eventID string, in TimeProposalInput) (domain.TimeProposal, error)
	ListProposals(ctx context.Context, userID, eventID string) ([]domain.TimeProposal, error)
	// AcceptProposal patches the event to the proposed time via provider
	// write-through and supersedes sibling proposals.
	AcceptProposal(ctx context.Context, userID, eventID, proposalID string) (domain.Event, error)
	DeclineProposal(ctx context.Context, userID, eventID, proposalID string) error

	// GuestFreeBusy powers the Find-a-Time grid via provider APIs.
	GuestFreeBusy(ctx context.Context, userID string, req FreeBusyRequest) (map[string][]domain.BusyInterval, error)

	// ExpireHolds is called by cmd/worker; cancels overdue holds.
	ExpireHolds(ctx context.Context) error
}

// SettingsService reads/writes per-user scheduling settings.
type SettingsService interface {
	Get(ctx context.Context, userID string) (domain.UserSettings, error)
	Update(ctx context.Context, userID string, s domain.UserSettings) (domain.UserSettings, error)
}

// TeamInput is the create/rename team payload.
type TeamInput struct {
	Name string `json:"name"`
}

// TeamService manages teams, membership, and email invitations. All
// role/authorization checks live here (service layer), never in adapters:
// non-members get ErrNotFound, under-privileged members get ErrForbidden.
type TeamService interface {
	Create(ctx context.Context, userID string, in TeamInput) (domain.Team, error)
	List(ctx context.Context, userID string) ([]domain.Team, error)
	// Get returns the team and its members; callers must be members.
	Get(ctx context.Context, userID, teamID string) (domain.Team, []domain.TeamMember, error)
	Rename(ctx context.Context, userID, teamID, name string) (domain.Team, error)
	// Delete requires the owner role and removes the team and all
	// memberships, shares, and comments (DB cascades).
	Delete(ctx context.Context, userID, teamID string) error
	// SetMemberRole requires admin+; only owners may grant/revoke owner.
	// Demoting or removing the last owner returns ErrConflict.
	SetMemberRole(ctx context.Context, userID, teamID, memberUserID string, role domain.TeamRole) (domain.TeamMember, error)
	// SetShareReadStatuses toggles the CALLER's own read-status opt-in.
	SetShareReadStatuses(ctx context.Context, userID, teamID string, share bool) (domain.TeamMember, error)
	// RemoveMember: admins remove members, owners remove anyone; any member
	// may remove themselves (leave), except the last owner (ErrConflict).
	RemoveMember(ctx context.Context, userID, teamID, memberUserID string) error
	// Invite (admin+) creates a pending invitation and emails the invite
	// link via the inviter's own connected account send pipeline.
	Invite(ctx context.Context, userID, teamID, email string, role domain.TeamRole) (domain.TeamInvitation, error)
	ListInvitations(ctx context.Context, userID, teamID string) ([]domain.TeamInvitation, error)
	RevokeInvitation(ctx context.Context, userID, teamID, invitationID string) error
	// AcceptInvitation redeems a raw invite token for the AUTHENTICATED
	// user. The token is hashed and looked up; expired/revoked/used tokens
	// return ErrNotFound (no oracle).
	AcceptInvitation(ctx context.Context, userID, token string) (domain.Team, error)
}

// --- Collaboration on threads (M2.7 Tasks 7+9) -------------------------------

// ShareThreadInput creates a live share link for a thread.
type ShareThreadInput struct {
	Audience domain.ShareAudience `json:"audience"` // "team" | "external"
	TeamID   string               `json:"teamId,omitempty"`
	// ExpiresAt optionally bounds the link's life; nil = until revoked.
	ExpiresAt *time.Time `json:"expiresAt"`
}

// SharedThreadView is the read-only projection served to share viewers:
// thread metadata + messages, with recipients' Bcc stripped and no
// labels/split/snooze state (owner-private triage data never leaves).
type SharedThreadView struct {
	Subject   string               `json:"subject"`
	Audience  domain.ShareAudience `json:"audience"`
	Messages  []domain.Message     `json:"messages"`
	UpdatedAt time.Time            `json:"updatedAt"`
}

// CommentInput is the add-comment payload.
type CommentInput struct {
	TeamID string `json:"teamId"`
	Body   string `json:"body"`
}

// CollabService is the team-collaboration surface on mail threads (M2.7):
// tokenized live shares plus comments with @mentions. All authorization
// lives here: sharing requires thread ownership + entitlement, team-audience
// shares require the sharer's membership of TeamID, and viewers of
// revoked/expired/unknown tokens uniformly get ErrNotFound (no oracle).
// Commenting requires (a) team membership and (b) the thread being visible
// to that team — the caller owns it, or an unrevoked team-audience share
// exists (comments piggyback on the explicit share; they never expose a
// thread by themselves). Non-members and invisible threads are ErrNotFound
// (never an oracle); an under-privileged known member is ErrForbidden.
type CollabService interface {
	// ShareThread creates a live share link; the raw token is returned
	// exactly once — only its hash is stored.
	ShareThread(ctx context.Context, userID, threadID string, in ShareThreadInput) (share domain.ThreadShare, rawToken string, err error)
	ListThreadShares(ctx context.Context, userID, threadID string) ([]domain.ThreadShare, error)
	RevokeThreadShare(ctx context.Context, userID, threadID, shareID string) error
	// GetSharedThread serves a share view. viewerUserID is nil for
	// unauthenticated (external) viewers; team-audience shares require a
	// viewer who is a member of the share's team.
	GetSharedThread(ctx context.Context, rawToken string, viewerUserID *string) (SharedThreadView, error)
	// ResolveShare authorizes a raw share token with GetSharedThread's exact
	// fail-closed semantics and returns just the share id — the SSE stream
	// endpoint's subscription topic ("share:<id>") needs no projection.
	ResolveShare(ctx context.Context, rawToken string, viewerUserID *string) (shareID string, err error)

	ListComments(ctx context.Context, userID, threadID, teamID string) ([]domain.Comment, error)
	AddComment(ctx context.Context, userID, threadID string, in CommentInput) (domain.Comment, error)
	// UpdateComment: author only (team admins may delete, not edit).
	UpdateComment(ctx context.Context, userID, commentID, body string) (domain.Comment, error)
	// DeleteComment: the author, or a team admin+ (soft delete).
	DeleteComment(ctx context.Context, userID, commentID string) error
}
