// Package httpapi is the inbound HTTP adapter: it exposes every REST
// endpoint from docs/architecture.md on a stdlib net/http ServeMux (Go
// 1.22+ method/pattern routing) and translates between JSON and the
// driving ports. It carries no business logic.
package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"calendium/backend/internal/port"
)

// Deps wires the adapter to the hexagon: every driving port, the token
// verifier for the auth middleware, and the raw Payments gateway.
type Deps struct {
	Logger    *slog.Logger // optional; defaults to slog.Default()
	Verifier  port.TokenVerifier
	Users     port.UserService
	Billing   port.BillingService
	Accounts  port.AccountService
	Mail      port.MailService
	Calendars port.CalendarService
	Search    port.SearchService
	AI        port.AIService
	Devices   port.DeviceService
	Prefs     port.PrefsService
	// Scheduling covers booking links, bookings, meeting polls,
	// propose-new-time, and guest free/busy (owner-authenticated surface;
	// the public booking/poll routes live behind their own rate limiter).
	Scheduling port.SchedulingService
	// Settings is per-user scheduling preferences (time zone, working
	// hours, working location).
	Settings port.SettingsService
	// Payments is the raw Stripe gateway. The webhook route verifies and
	// applies events through Billing; the port is part of Deps so the
	// composition surface matches the adapter contract.
	Payments port.Payments
	// Events fans realtime collaboration events out to SSE subscribers
	// (GET /v1/collab/stream). When nil the stream endpoint answers 501.
	Events port.EventBus
	// Teams resolves the caller's memberships so the stream subscribes only
	// to the caller's team topics. Optional until the M2.7 team service is
	// wired: when nil the stream carries only user:<id> events.
	Teams TeamLister
	// Delegations manages EA grants and authorizes X-Calendium-Act-As
	// delegated requests (M2.7 Task 15). When nil the delegation routes
	// answer 501 and any act-as request is rejected (fail closed).
	Delegations port.DelegationService
	// Instance is the public self-configuration document served verbatim at
	// GET /v1/instance; the composition root fills it from config + which
	// gateways are wired.
	Instance InstanceInfo
	// CORSAllowedOrigins is the extra CORS allowlist (CORS_ALLOWED_ORIGINS)
	// reflected in addition to the built-in localhost-dev and Wails origins.
	CORSAllowedOrigins []string
}

type server struct {
	deps Deps
}

// New builds the full v1 REST handler with recovery, request logging, CORS
// (localhost dev + Wails + CORS_ALLOWED_ORIGINS), and bearer-token auth on
// every /v1 route except the Stripe webhook and the provider OAuth callback.
func New(deps Deps) http.Handler {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	s := &server{deps: deps}

	mux := http.NewServeMux()

	// Unauthenticated surface.
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /v1/instance", s.handleInstance)
	mux.HandleFunc("POST /v1/webhooks/stripe", s.handleStripeWebhook)
	mux.HandleFunc("GET /v1/accounts/callback/{provider}", s.handleAccountCallback)

	// Public scheduling surface (unauthenticated, rate limited): booking
	// pages/slots/bookings and meeting-poll view/vote. Two buckets — reads
	// generous, writes tight — per-IP, in-process (see ratelimit.go).
	publicRead := newRateLimiter(60, 30, time.Now) // GETs: 60/min, burst 30
	publicWrite := newRateLimiter(5, 5, time.Now)  // POSTs: 5/min, burst 5
	mux.HandleFunc("GET /v1/public/booking/{slug}", s.rateLimited(publicRead, s.handlePublicBookingPage))
	mux.HandleFunc("GET /v1/public/booking/{slug}/slots", s.rateLimited(publicRead, s.handlePublicSlots))
	mux.HandleFunc("POST /v1/public/booking/{slug}/bookings", s.rateLimited(publicWrite, s.handlePublicBook))
	mux.HandleFunc("GET /v1/public/polls/{token}", s.rateLimited(publicRead, s.handlePublicPoll))
	mux.HandleFunc("POST /v1/public/polls/{token}/votes", s.rateLimited(publicWrite, s.handlePublicPollVote))

	// Authenticated surface.
	authed := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, s.requireAuth(s.withActAs(h)))
	}

	authed("GET /v1/me", s.handleMe)
	authed("GET /v1/me/preferences", s.handleGetPreferences)
	authed("PUT /v1/me/preferences", s.handleUpdatePreferences)

	authed("GET /v1/billing/subscription", s.handleGetSubscription)
	authed("POST /v1/billing/checkout", s.handleCreateCheckout)
	authed("POST /v1/billing/portal", s.handleCreatePortal)

	authed("GET /v1/accounts", s.handleListAccounts)
	authed("POST /v1/accounts/connect/{provider}", s.handleConnectAccount)
	authed("PUT /v1/accounts/{id}/vip-senders", s.handleSetVipSenders)
	authed("PUT /v1/accounts/{id}/signature", s.handleSetSignature)
	authed("PUT /v1/accounts/{id}/auto-bcc", s.handleSetAutoBcc)
	authed("DELETE /v1/accounts/{id}", s.handleDisconnectAccount)

	authed("GET /v1/mail/threads", s.handleListThreads)
	authed("GET /v1/mail/threads/{id}", s.handleGetThread)
	authed("POST /v1/mail/threads/{id}/actions", s.handleThreadAction)
	authed("POST /v1/mail/threads/bulk-actions", s.handleBulkThreadActions)
	authed("POST /v1/mail/threads/{id}/open", s.handleMarkThreadOpened)
	authed("POST /v1/mail/threads/{id}/snooze", s.handleSnoozeThread)
	authed("DELETE /v1/mail/threads/{id}/snooze", s.handleUnsnoozeThread)
	authed("POST /v1/mail/threads/{id}/reminder", s.handleThreadReminder)
	authed("POST /v1/mail/threads/{id}/unsubscribe", s.handleUnsubscribeThread)
	authed("GET /v1/mail/threads/{id}/instant-replies", s.handleInstantReplies)
	authed("POST /v1/mail/threads/zero", s.handleGetMeToZero)

	authed("GET /v1/mail/labels", s.handleListLabels)
	authed("POST /v1/mail/threads/{id}/labels", s.handleSetThreadLabel)

	authed("GET /v1/mail/drafts", s.handleListDrafts)
	authed("POST /v1/mail/drafts", s.handleCreateDraft)
	authed("GET /v1/mail/drafts/{id}", s.handleGetDraft)
	authed("PUT /v1/mail/drafts/{id}", s.handleUpdateDraft)
	authed("DELETE /v1/mail/drafts/{id}", s.handleDeleteDraft)
	authed("POST /v1/mail/drafts/{id}/send", s.handleSendDraft)
	authed("POST /v1/mail/drafts/{id}/unsend", s.handleUnsendDraft)

	authed("GET /v1/mail/snippets", s.handleListSnippets)
	authed("POST /v1/mail/snippets", s.handleCreateSnippet)
	authed("PUT /v1/mail/snippets/{id}", s.handleUpdateSnippet)
	authed("DELETE /v1/mail/snippets/{id}", s.handleDeleteSnippet)

	// M2.5: Recent Opens feed, Smart Send, attachment quick-access, contact
	// summary, and emoji reactions.
	authed("GET /v1/mail/opens", s.handleListOpens)
	authed("GET /v1/mail/send-suggestion", s.handleSendSuggestion)
	authed("GET /v1/mail/attachments", s.handleSearchAttachments)
	authed("GET /v1/mail/attachments/{id}/content", s.handleGetAttachmentContent)
	authed("GET /v1/mail/contacts/{email}", s.handleGetContact)
	authed("POST /v1/mail/messages/{id}/reactions", s.handleReactToMessage)
	authed("DELETE /v1/mail/messages/{id}/reactions/{emoji}", s.handleRemoveReaction)

	authed("GET /v1/calendars", s.handleListCalendars)
	authed("PATCH /v1/calendars/{id}", s.handleUpdateCalendar)

	authed("GET /v1/events", s.handleListEvents)
	authed("POST /v1/events", s.handleCreateEvent)
	authed("PATCH /v1/events/{id}", s.handleUpdateEvent)
	authed("DELETE /v1/events/{id}", s.handleDeleteEvent)
	authed("POST /v1/events/{id}/rsvp", s.handleRsvp)
	authed("GET /v1/availability", s.handleAvailability)

	authed("GET /v1/event-templates", s.handleListEventTemplates)
	authed("POST /v1/event-templates", s.handleCreateEventTemplate)
	authed("PUT /v1/event-templates/{id}", s.handleUpdateEventTemplate)
	authed("DELETE /v1/event-templates/{id}", s.handleDeleteEventTemplate)
	authed("POST /v1/event-templates/{id}/use", s.handleUseEventTemplate)

	authed("GET /v1/calendar-sets", s.handleListCalendarSets)
	authed("POST /v1/calendar-sets", s.handleCreateCalendarSet)
	authed("PUT /v1/calendar-sets/{id}", s.handleUpdateCalendarSet)
	authed("DELETE /v1/calendar-sets/{id}", s.handleDeleteCalendarSet)

	authed("GET /v1/search", s.handleSearch)
	authed("POST /v1/ai/compose", s.handleAiCompose)
	authed("POST /v1/ai/ask", s.handleAiAsk)
	authed("POST /v1/ai/event-proposal", s.handleAiEventProposal)

	authed("GET /v1/classifiers", s.handleListClassifiers)
	authed("POST /v1/classifiers", s.handleCreateClassifier)
	authed("PATCH /v1/classifiers/{id}", s.handleUpdateClassifier)
	authed("DELETE /v1/classifiers/{id}", s.handleDeleteClassifier)

	authed("POST /v1/devices", s.handleRegisterDevice)
	authed("DELETE /v1/devices/{id}", s.handleUnregisterDevice)

	authed("GET /v1/prefs", s.handleGetPrefs)
	authed("PUT /v1/prefs", s.handleUpdatePrefs)

	// Scheduling: owner-authenticated surface (booking links, bookings,
	// meeting polls, propose-new-time, guest free/busy, settings). The
	// public booking/poll routes are registered separately, in the
	// unauthenticated section above, behind their own rate limiter.
	authed("GET /v1/booking-links", s.handleListLinks)
	authed("POST /v1/booking-links", s.handleCreateLink)
	authed("PUT /v1/booking-links/{id}", s.handleUpdateLink)
	authed("DELETE /v1/booking-links/{id}", s.handleDeleteLink)

	authed("GET /v1/bookings", s.handleListBookings)
	authed("POST /v1/bookings/{id}/cancel", s.handleCancelBooking)

	authed("GET /v1/polls", s.handleListPolls)
	authed("POST /v1/polls", s.handleCreatePoll)
	authed("POST /v1/polls/{id}/confirm", s.handleConfirmPoll)
	authed("DELETE /v1/polls/{id}", s.handleDeletePoll)

	authed("POST /v1/events/{id}/propose-time", s.handleProposeTime)
	authed("GET /v1/events/{id}/proposals", s.handleListProposals)
	authed("POST /v1/events/{id}/proposals/{pid}/accept", s.handleAcceptProposal)
	authed("POST /v1/events/{id}/proposals/{pid}/decline", s.handleDeclineProposal)

	authed("POST /v1/freebusy", s.handleGuestFreeBusy)

	authed("GET /v1/settings", s.handleGetSettings)
	authed("PUT /v1/settings", s.handleUpdateSettings)

	// M2.7: realtime collaboration stream (SSE).
	authed("GET /v1/collab/stream", s.handleCollabStream)

	// M2.7 Task 15: EA delegation grants + audit log.
	authed("POST /v1/delegations", s.handleCreateDelegation)
	authed("GET /v1/delegations", s.handleListDelegations)
	authed("POST /v1/delegations/{id}/accept", s.handleAcceptDelegation)
	authed("DELETE /v1/delegations/{id}", s.handleRevokeDelegation)
	authed("GET /v1/delegations/audit", s.handleDelegationAudit)

	var h http.Handler = mux
	h = corsMiddleware(h, deps.CORSAllowedOrigins)
	h = s.logRequests(h)
	h = s.recoverPanics(h)
	return h
}

func (s *server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
