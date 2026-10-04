// Package httpapi is the inbound HTTP adapter: it exposes every REST
// endpoint from docs/architecture.md on a stdlib net/http ServeMux (Go
// 1.22+ method/pattern routing) and translates between JSON and the
// driving ports. It carries no business logic.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/netip"

	"calendium/backend/internal/port"
)

// Deps wires the adapter to the hexagon: every driving port and the token
// verifier for the auth middleware.
type Deps struct {
	Logger    *slog.Logger // optional; defaults to slog.Default()
	Verifier  port.TokenVerifier
	Users     port.UserService
	Billing   port.BillingService
	Accounts  port.AccountService
	Mail      port.MailService
	Calendars port.CalendarService
	// CalendarShares manages calendar sharing grants (M2.7 Task 12). When
	// nil the share routes answer 501.
	CalendarShares port.CalendarSharingService
	Search         port.SearchService
	AI             port.AIService
	Devices        port.DeviceService
	Prefs          port.PrefsService
	// Scheduling covers booking links, bookings, meeting polls,
	// propose-new-time, and guest free/busy (owner-authenticated surface;
	// the public booking/poll routes live behind their own rate limiter).
	Scheduling port.SchedulingService
	// Settings is per-user scheduling preferences (time zone, working
	// hours, working location).
	Settings port.SettingsService
	// Events fans realtime collaboration events out to SSE subscribers
	// (GET /v1/collab/stream). When nil the stream endpoint answers 501.
	Events port.EventBus
	// Teams manages teams, membership, and invitations (M2.7); it also
	// resolves the caller's memberships so the SSE stream subscribes to the
	// caller's team topics.
	Teams port.TeamService
	// Collab is the M2.7 collaboration surface (tokenized live thread
	// shares + team thread-comments). When nil those routes answer 501.
	Collab port.CollabService
	// Delegations manages EA grants and authorizes X-Calendium-Act-As
	// delegated requests (M2.7 Task 15). When nil the delegation routes
	// answer 501 and any act-as request is rejected (fail closed).
	Delegations port.DelegationService
	// TeamActivity serves teammate read/reply indicators (M2.7 Task 10);
	// when nil the team-activity route answers 501.
	TeamActivity port.TeamActivityService
	// Tasks is the first-class task surface (M2.8): local todos plus
	// mirrored external provider todos.
	Tasks port.TaskService
	// Weather serves inline day forecasts for calendar surfaces (M2.8 Task
	// 13, Open-Meteo). When nil — no vendor configured — GET /v1/weather
	// answers 501 and clients hide the weather chips.
	Weather port.WeatherService
	// Places is location autocomplete backed by a MapsProvider (M2.8
	// Task 11). When nil (maps not configured) the places route answers 501
	// and GET /v1/instance advertises features.maps=false.
	Places port.PlacesService
	// Integrations manages per-user vendor OAuth connections
	// (Todoist/HubSpot, M2.8 Task 9). When nil the integration routes
	// answer 501.
	Integrations port.IntegrationService
	// Crm serves CRM contact context and explicit per-message email logging
	// (M2.8 Task 16). When nil (vendor unconfigured, or the integration-
	// connection repo not composed) the /v1/crm routes answer 501.
	Crm port.CrmService
	// Insights serves aggregated time analytics computed from the local
	// mirror (M2.8 Task 17). When nil the insights route answers 501.
	Insights port.InsightsService
	// Instance is the public self-configuration document served verbatim at
	// GET /v1/instance; the composition root fills it from config + which
	// gateways are wired.
	Instance InstanceInfo
	// CORSAllowedOrigins is the explicit CORS allowlist (CORS_ALLOWED_ORIGINS
	// plus the web app's PUBLIC_WEB_URL and BETTER_AUTH_URL), reflected in
	// addition to the built-in Wails origins.
	CORSAllowedOrigins []string
	// AllowDevOrigins (ALLOW_DEV_ORIGINS) reflects http(s)://localhost and
	// 127.0.0.1 origins in CORS; the Wails origins and CORSAllowedOrigins
	// are reflected regardless. The run-the-binary env template turns it
	// on; production leaves it off.
	AllowDevOrigins bool
	// TrustProxy (TRUST_PROXY) makes X-Forwarded-* from a peer inside
	// TrustedProxyCIDRs authoritative for the client IP, scheme and host.
	TrustProxy bool
	// TrustedProxyCIDRs (TRUSTED_PROXY_CIDRS) is the proxy allowlist; the
	// config package supplies the default loopback/RFC1918/ULA set.
	TrustedProxyCIDRs []netip.Prefix
	// RateLimits is the per-class budget; a zero class is disabled (the zero
	// value disables API rate limiting).
	RateLimits RateLimits
	// Drain is closed by the composition root when shutdown begins: /readyz
	// turns 503 and the SSE streams return so Shutdown can finish. nil = never.
	Drain <-chan struct{}
	// Ready pings the primary dependency (DB) for /readyz; nil = always ready.
	Ready func(context.Context) error
}

type server struct {
	deps  Deps
	proxy proxyTrust
	// limits holds the per-user class limiters (nil entry = class disabled);
	// publicRead/publicWrite are the per-IP public limiters.
	limits                  map[string]*rateLimiter
	publicRead, publicWrite *rateLimiter
	// classOf records each authed pattern's limiter class (tests).
	classOf map[string]string
}

// newServer builds the handler state shared by New and the middleware unit
// tests (harness.server()).
func newServer(deps Deps) *server {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	s := &server{
		deps:    deps,
		proxy:   proxyTrust{enabled: deps.TrustProxy, nets: deps.TrustedProxyCIDRs},
		classOf: map[string]string{},
	}
	rl := deps.RateLimits
	s.limits = map[string]*rateLimiter{
		classUser:        newClassLimiter(rl.UserPerMin, rl.UserPerMin),
		classMutateHeavy: newClassLimiter(rl.MutateHeavyPerMin, rl.MutateHeavyPerMin),
		classSearch:      newClassLimiter(rl.SearchPerMin, rl.SearchPerMin),
	}
	s.publicRead = newClassLimiter(rl.PublicReadPerMin, rl.PublicReadPerMin/2)
	s.publicWrite = newClassLimiter(rl.PublicWritePerMin, rl.PublicWritePerMin)
	return s
}

// New builds the full v1 REST handler with recovery, request logging, CORS
// (Wails + CORS_ALLOWED_ORIGINS always, localhost dev only with
// ALLOW_DEV_ORIGINS), and bearer-token auth on every /v1 route except the
// Paddle webhook and the provider OAuth callback.
func New(deps Deps) http.Handler {
	_, h := build(deps)
	return h
}

// build is New returning the server state too (route-table tests).
func build(deps Deps) (*server, http.Handler) {
	s := newServer(deps)
	deps = s.deps

	mux := http.NewServeMux()

	// Unauthenticated surface.
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	mux.HandleFunc("GET /v1/instance", s.handleInstance)
	mux.HandleFunc("POST /v1/webhooks/paddle", s.handlePaddleWebhook)
	mux.HandleFunc("GET /v1/accounts/callback/{provider}", s.handleAccountCallback)
	// M2.8 Task 9: vendor OAuth redirect target (state-validated, like the
	// account callback above).
	mux.HandleFunc("GET /v1/integrations/callback/{vendor}", s.handleIntegrationCallback)

	// Public scheduling surface (unauthenticated, rate limited): booking
	// pages/slots/bookings and meeting-poll view/vote. Two buckets — reads
	// generous, writes tight — per client IP, in-process (see ratelimit.go;
	// budgets from Deps.RateLimits).
	publicRead, publicWrite := s.publicRead, s.publicWrite
	mux.HandleFunc("GET /v1/public/booking/{slug}", s.rateLimited(publicRead, s.handlePublicBookingPage))
	mux.HandleFunc("GET /v1/public/booking/{slug}/slots", s.rateLimited(publicRead, s.handlePublicSlots))
	mux.HandleFunc("POST /v1/public/booking/{slug}/bookings", s.rateLimited(publicWrite, s.handlePublicBook))
	mux.HandleFunc("GET /v1/public/polls/{token}", s.rateLimited(publicRead, s.handlePublicPoll))
	mux.HandleFunc("POST /v1/public/polls/{token}/votes", s.rateLimited(publicWrite, s.handlePublicPollVote))

	// M2.7 shared conversations: tokenized share links. Registered OUTSIDE
	// authed(...) (the Paddle-webhook precedent): external shares are fully
	// public, team shares re-check an optional bearer inside the handler.
	mux.HandleFunc("GET /v1/shared/threads/{token}", s.rateLimited(publicRead, s.handleGetSharedThread))
	mux.HandleFunc("GET /v1/shared/threads/{token}/stream", s.rateLimited(publicRead, s.handleSharedThreadStream))

	// Authenticated surface. Chain: requireAuth → userLimited(class) →
	// withActAs → deadline → handler, so the ACTOR is charged, never the
	// principal.
	authed := func(pattern string, h http.HandlerFunc, opts ...routeOption) {
		o := routeOptions{class: classUser, deadline: defaultHandlerDeadline}
		for _, opt := range opts {
			opt(&o)
		}
		s.classOf[pattern] = o.class
		inner := h
		if !o.noDeadline {
			inner = s.withDeadline(o.deadline, inner)
		}
		inner = s.withActAs(inner)
		inner = s.userLimited(s.limits[o.class], inner)
		mux.Handle(pattern, s.requireAuth(inner))
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

	// M2.8 Task 9: per-user vendor integrations (Todoist/HubSpot).
	authed("GET /v1/integrations", s.handleListIntegrations)
	authed("POST /v1/integrations/connect/{vendor}", s.handleConnectIntegration)
	authed("DELETE /v1/integrations/{id}", s.handleDisconnectIntegration)

	authed("GET /v1/mail/threads", s.handleListThreads)
	authed("GET /v1/mail/threads/{id}", s.handleGetThread)
	authed("POST /v1/mail/threads/{id}/actions", s.handleThreadAction)
	authed("POST /v1/mail/threads/bulk-actions", s.handleBulkThreadActions, limitClass(classMutateHeavy))
	authed("POST /v1/mail/threads/{id}/open", s.handleMarkThreadOpened)
	authed("POST /v1/mail/threads/{id}/snooze", s.handleSnoozeThread)
	authed("DELETE /v1/mail/threads/{id}/snooze", s.handleUnsnoozeThread)
	authed("POST /v1/mail/threads/{id}/reminder", s.handleThreadReminder)
	authed("POST /v1/mail/threads/{id}/unsubscribe", s.handleUnsubscribeThread)
	authed("GET /v1/mail/threads/{id}/instant-replies", s.handleInstantReplies)
	authed("GET /v1/mail/threads/{id}/team-activity", s.handleTeamThreadActivity)
	authed("POST /v1/mail/threads/zero", s.handleGetMeToZero, limitClass(classMutateHeavy))

	authed("GET /v1/mail/labels", s.handleListLabels)
	authed("POST /v1/mail/threads/{id}/labels", s.handleSetThreadLabel)

	authed("GET /v1/mail/drafts", s.handleListDrafts)
	authed("POST /v1/mail/drafts", s.handleCreateDraft)
	authed("GET /v1/mail/drafts/{id}", s.handleGetDraft)
	authed("PUT /v1/mail/drafts/{id}", s.handleUpdateDraft)
	authed("DELETE /v1/mail/drafts/{id}", s.handleDeleteDraft)
	authed("POST /v1/mail/drafts/{id}/send", s.handleSendDraft, limitClass(classMutateHeavy))
	authed("POST /v1/mail/drafts/{id}/unsend", s.handleUnsendDraft)

	authed("GET /v1/mail/snippets", s.handleListSnippets)
	authed("POST /v1/mail/snippets", s.handleCreateSnippet)
	authed("PUT /v1/mail/snippets/{id}", s.handleUpdateSnippet)
	authed("DELETE /v1/mail/snippets/{id}", s.handleDeleteSnippet)

	// M2.5: Recent Opens feed, Smart Send, attachment quick-access, contact
	// summary, and emoji reactions.
	authed("GET /v1/mail/opens", s.handleListOpens)
	authed("GET /v1/mail/send-suggestion", s.handleSendSuggestion)
	authed("GET /v1/mail/attachments", s.handleSearchAttachments, limitClass(classSearch))
	authed("GET /v1/mail/attachments/{id}/content", s.handleGetAttachmentContent, deadline(attachmentHandlerDeadline))
	authed("GET /v1/mail/contacts/{email}", s.handleGetContact)
	authed("POST /v1/mail/messages/{id}/reactions", s.handleReactToMessage)
	authed("DELETE /v1/mail/messages/{id}/reactions/{emoji}", s.handleRemoveReaction)

	// M2.7: shared conversations (owner-side share management).
	authed("POST /v1/mail/threads/{id}/share", s.handleShareThread, limitClass(classMutateHeavy))
	authed("GET /v1/mail/threads/{id}/shares", s.handleListThreadShares)
	authed("DELETE /v1/mail/threads/{id}/shares/{shareId}", s.handleRevokeThreadShare)

	authed("GET /v1/calendars", s.handleListCalendars)
	authed("PATCH /v1/calendars/{id}", s.handleUpdateCalendar)

	// M2.7 Task 12: shared calendars with granular permissions.
	authed("GET /v1/calendars/{id}/shares", s.handleListCalendarShares)
	authed("POST /v1/calendars/{id}/shares", s.handleShareCalendar)
	authed("PATCH /v1/calendars/{id}/shares/{shareId}", s.handleUpdateCalendarShare)
	authed("DELETE /v1/calendars/{id}/shares/{shareId}", s.handleRevokeCalendarShare)

	authed("GET /v1/events", s.handleListEvents)
	authed("POST /v1/events", s.handleCreateEvent)
	authed("PATCH /v1/events/{id}", s.handleUpdateEvent)
	authed("DELETE /v1/events/{id}", s.handleDeleteEvent)
	authed("POST /v1/events/{id}/rsvp", s.handleRsvp)
	// M2.8 Task 4: local-only docs/notes attached to events.
	authed("GET /v1/events/{id}/note", s.handleGetEventNote)
	authed("PUT /v1/events/{id}/note", s.handlePutEventNote)
	authed("GET /v1/availability", s.handleAvailability)

	// M2.8: first-class tasks (local + mirrored provider todos).
	authed("GET /v1/tasks", s.handleListTasks)
	authed("POST /v1/tasks", s.handleCreateTask)
	authed("PATCH /v1/tasks/{id}", s.handleUpdateTask)
	authed("POST /v1/tasks/{id}/complete", s.handleCompleteTask)
	authed("POST /v1/tasks/{id}/reopen", s.handleReopenTask)
	authed("DELETE /v1/tasks/{id}", s.handleDeleteTask)

	authed("GET /v1/event-templates", s.handleListEventTemplates)
	authed("POST /v1/event-templates", s.handleCreateEventTemplate)
	authed("PUT /v1/event-templates/{id}", s.handleUpdateEventTemplate)
	authed("DELETE /v1/event-templates/{id}", s.handleDeleteEventTemplate)
	authed("POST /v1/event-templates/{id}/use", s.handleUseEventTemplate)

	authed("GET /v1/calendar-sets", s.handleListCalendarSets)
	authed("POST /v1/calendar-sets", s.handleCreateCalendarSet)
	authed("PUT /v1/calendar-sets/{id}", s.handleUpdateCalendarSet)
	authed("DELETE /v1/calendar-sets/{id}", s.handleDeleteCalendarSet)

	// M2.8 Task 15: interesting-calendar ICS feed subscriptions.
	authed("GET /v1/calendar-subscriptions", s.handleListCalendarSubscriptions)
	authed("POST /v1/calendar-subscriptions", s.handleCreateCalendarSubscription, limitClass(classMutateHeavy))
	authed("PATCH /v1/calendar-subscriptions/{id}", s.handleUpdateCalendarSubscription)
	authed("DELETE /v1/calendar-subscriptions/{id}", s.handleDeleteCalendarSubscription)

	authed("GET /v1/search", s.handleSearch, limitClass(classSearch))
	authed("POST /v1/ai/compose", s.handleAiCompose, limitClass(classMutateHeavy))
	authed("POST /v1/ai/ask", s.handleAiAsk, limitClass(classMutateHeavy))
	authed("POST /v1/ai/event-proposal", s.handleAiEventProposal, limitClass(classMutateHeavy))

	authed("GET /v1/classifiers", s.handleListClassifiers)
	authed("POST /v1/classifiers", s.handleCreateClassifier)
	authed("PATCH /v1/classifiers/{id}", s.handleUpdateClassifier)
	authed("DELETE /v1/classifiers/{id}", s.handleDeleteClassifier)

	authed("POST /v1/devices", s.handleRegisterDevice)
	authed("DELETE /v1/devices/{id}", s.handleUnregisterDevice)

	authed("GET /v1/prefs", s.handleGetPrefs)
	authed("PUT /v1/prefs", s.handleUpdatePrefs)

	// M2.8 Task 5: calendar automation preferences (FocusGuard, buffers,
	// OOO, travel, weather) — the settings the Wave-2 automation engine
	// fans out over.
	authed("GET /v1/prefs/calendar", s.handleGetCalendarPrefs)
	authed("PATCH /v1/prefs/calendar", s.handleUpdateCalendarPrefs)

	// Scheduling: owner-authenticated surface (booking links, bookings,
	// meeting polls, propose-new-time, guest free/busy, settings). The
	// public booking/poll routes are registered separately, in the
	// unauthenticated section above, behind their own rate limiter.
	authed("GET /v1/booking-links", s.handleListLinks)
	authed("POST /v1/booking-links", s.handleCreateLink, limitClass(classMutateHeavy))
	authed("PUT /v1/booking-links/{id}", s.handleUpdateLink)
	authed("DELETE /v1/booking-links/{id}", s.handleDeleteLink)

	authed("GET /v1/bookings", s.handleListBookings)
	authed("POST /v1/bookings/{id}/cancel", s.handleCancelBooking)

	authed("GET /v1/polls", s.handleListPolls)
	authed("POST /v1/polls", s.handleCreatePoll, limitClass(classMutateHeavy))
	authed("POST /v1/polls/{id}/confirm", s.handleConfirmPoll)
	authed("DELETE /v1/polls/{id}", s.handleDeletePoll)

	authed("POST /v1/events/{id}/propose-time", s.handleProposeTime)
	authed("GET /v1/events/{id}/proposals", s.handleListProposals)
	authed("POST /v1/events/{id}/proposals/{pid}/accept", s.handleAcceptProposal)
	authed("POST /v1/events/{id}/proposals/{pid}/decline", s.handleDeclineProposal)

	authed("POST /v1/freebusy", s.handleGuestFreeBusy)

	authed("GET /v1/settings", s.handleGetSettings)
	authed("PUT /v1/settings", s.handleUpdateSettings)

	// M2.7: teams, membership, and email invitations.
	authed("POST /v1/teams", s.handleCreateTeam)
	authed("GET /v1/teams", s.handleListTeams)
	authed("GET /v1/teams/{id}", s.handleGetTeam)
	authed("PATCH /v1/teams/{id}", s.handleRenameTeam)
	authed("DELETE /v1/teams/{id}", s.handleDeleteTeam)
	authed("PATCH /v1/teams/{id}/members/{userId}", s.handleSetMemberRole)
	authed("PUT /v1/teams/{id}/read-status-sharing", s.handleSetShareReadStatuses)
	authed("DELETE /v1/teams/{id}/members/{userId}", s.handleRemoveMember)
	authed("POST /v1/teams/{id}/invitations", s.handleInvite, limitClass(classMutateHeavy))
	authed("GET /v1/teams/{id}/invitations", s.handleListInvitations)
	authed("DELETE /v1/teams/{id}/invitations/{invitationId}", s.handleRevokeInvitation)
	authed("POST /v1/invitations/accept", s.handleAcceptInvitation)

	// M2.7 Task 13: team availability overview (per-member opaque busy
	// blocks; membership + opt-in enforced in the calendar service).
	authed("GET /v1/teams/{id}/availability", s.handleTeamAvailability)

	// M2.7: realtime collaboration stream (SSE) and team thread-comments.
	authed("GET /v1/collab/stream", s.handleCollabStream, noDeadline())
	authed("GET /v1/mail/threads/{id}/comments", s.handleListComments)
	authed("POST /v1/mail/threads/{id}/comments", s.handleAddComment)
	authed("PATCH /v1/comments/{id}", s.handleUpdateComment)
	authed("DELETE /v1/comments/{id}", s.handleDeleteComment)

	// M2.7 Task 15: EA delegation grants + audit log.
	authed("POST /v1/delegations", s.handleCreateDelegation)
	authed("GET /v1/delegations", s.handleListDelegations)
	authed("POST /v1/delegations/{id}/accept", s.handleAcceptDelegation)
	authed("DELETE /v1/delegations/{id}", s.handleRevokeDelegation)
	authed("GET /v1/delegations/audit", s.handleDelegationAudit)

	// M2.8 Task 13: inline weather on calendar days. Best-effort decoration:
	// its own endpoint, never on a calendar request's critical path.
	authed("GET /v1/weather", s.handleGetWeather)

	// M2.8 Task 11: location autocomplete (Nominatim-backed; 501 unwired).
	authed("GET /v1/places/autocomplete", s.handlePlacesAutocomplete, limitClass(classSearch))

	// M2.8 Task 16: CRM contact context + explicit email logging.
	authed("GET /v1/crm/context", s.handleCrmContext)
	authed("POST /v1/crm/log", s.handleCrmLog)

	// M2.8 Task 17: time analytics computed from the local mirror.
	authed("GET /v1/insights/time", s.handleGetTimeInsights)

	var h http.Handler = mux
	h = corsMiddleware(h, deps.CORSAllowedOrigins, deps.AllowDevOrigins)
	h = s.logRequests(h)
	h = s.requestID(h)
	h = s.securityHeaders(h)
	h = s.recoverPanics(h)
	return s, h
}

func (s *server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
