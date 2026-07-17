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
// and availability.
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

// AIService is the OpenRouter-backed compose/reply/summarize/ask endpoint.
type AIService interface {
	Compose(ctx context.Context, userID string, req domain.AiComposeRequest) (domain.AiComposeResponse, error)
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
