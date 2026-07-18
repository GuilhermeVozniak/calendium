package port

import (
	"context"
	"time"

	"calendium/backend/internal/domain"
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

// SubscriptionRepo persists the one-row-per-user Stripe subscription mirror.
type SubscriptionRepo interface {
	GetByUserID(ctx context.Context, userID string) (domain.Subscription, error)
	GetByStripeCustomerID(ctx context.Context, customerID string) (domain.Subscription, error)
	Upsert(ctx context.Context, s domain.Subscription) error
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
}

// MessageRepo persists mirrored mail messages.
type MessageRepo interface {
	Upsert(ctx context.Context, m domain.Message) (domain.Message, error)
	GetByID(ctx context.Context, id string) (domain.Message, error)
	GetByProviderID(ctx context.Context, accountID, providerMessageID string) (domain.Message, error)
	ListByThread(ctx context.Context, threadID string) ([]domain.Message, error)
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
}

// SnippetRepo persists per-user canned responses.
type SnippetRepo interface {
	Create(ctx context.Context, s domain.Snippet) (domain.Snippet, error)
	GetByID(ctx context.Context, id string) (domain.Snippet, error)
	ListByUser(ctx context.Context, userID string) ([]domain.Snippet, error)
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

// StripeEventRepo records processed Stripe webhook event ids for idempotency.
type StripeEventRepo interface {
	// Record inserts the event id; firstTime is false when it was already
	// recorded (the webhook must then be skipped).
	Record(ctx context.Context, eventID, eventType string) (firstTime bool, err error)
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

// ---------------------------------------------------------------------------
// Gateways (implemented by internal/adapter/out/{googleapi,msgraph,stripeapi,openrouter,push,authjwt})
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
	RSVP(ctx context.Context, accessToken, providerCalendarID, providerEventID string, response domain.RsvpStatus) error
}

// CheckoutParams parameterizes a Stripe Checkout session for the single
// annual plan (the price id is baked into the adapter).
type CheckoutParams struct {
	CustomerID string
	UserID     string // bound via client_reference_id + metadata.user_id
	SuccessURL string
	CancelURL  string
	// TrialDays is 14 for first-time subscribers, 0 otherwise.
	TrialDays int
}

// WebhookEvent is a verified, normalized Stripe webhook event. Fields other
// than ID/Type are populated only for subscription-bearing events.
type WebhookEvent struct {
	ID   string
	Type string
	// Created is the Stripe event `created` time; used to drop out-of-order
	// or re-delivered older customer.subscription.* events.
	Created           *time.Time
	CustomerID        string
	SubscriptionID    string
	UserID            string // from metadata.user_id / client_reference_id, when present
	Status            domain.SubscriptionStatus
	CurrentPeriodEnd  *time.Time
	CancelAtPeriodEnd bool
	TrialEndsAt       *time.Time
}

// Payments is the Stripe billing surface (docs/payments.md).
type Payments interface {
	// EnsureCustomer returns the Stripe customer id for the user, creating
	// the customer if needed.
	EnsureCustomer(ctx context.Context, user domain.User) (customerID string, err error)
	CreateCheckoutSession(ctx context.Context, p CheckoutParams) (url string, err error)
	CreatePortalSession(ctx context.Context, customerID, returnURL string) (url string, err error)
	// ParseWebhook verifies the Stripe-Signature header (HMAC-SHA256,
	// constant-time compare, 5-minute tolerance) and normalizes the event.
	ParseWebhook(payload []byte, sigHeader string) (WebhookEvent, error)
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
