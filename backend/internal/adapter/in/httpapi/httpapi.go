// Package httpapi is the inbound HTTP adapter: it exposes every REST
// endpoint from docs/architecture.md on a stdlib net/http ServeMux (Go
// 1.22+ method/pattern routing) and translates between JSON and the
// driving ports. It carries no business logic.
package httpapi

import (
	"log/slog"
	"net/http"

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
	// Payments is the raw Stripe gateway. The webhook route verifies and
	// applies events through Billing; the port is part of Deps so the
	// composition surface matches the adapter contract.
	Payments port.Payments
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

	// Authenticated surface.
	authed := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, s.requireAuth(h))
	}

	authed("GET /v1/me", s.handleMe)

	authed("GET /v1/billing/subscription", s.handleGetSubscription)
	authed("POST /v1/billing/checkout", s.handleCreateCheckout)
	authed("POST /v1/billing/portal", s.handleCreatePortal)

	authed("GET /v1/accounts", s.handleListAccounts)
	authed("POST /v1/accounts/connect/{provider}", s.handleConnectAccount)
	authed("PUT /v1/accounts/{id}/vip-senders", s.handleSetVipSenders)
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

	authed("GET /v1/calendars", s.handleListCalendars)
	authed("PATCH /v1/calendars/{id}", s.handleUpdateCalendar)

	authed("GET /v1/events", s.handleListEvents)
	authed("POST /v1/events", s.handleCreateEvent)
	authed("PATCH /v1/events/{id}", s.handleUpdateEvent)
	authed("DELETE /v1/events/{id}", s.handleDeleteEvent)
	authed("POST /v1/events/{id}/rsvp", s.handleRsvp)
	authed("GET /v1/availability", s.handleAvailability)

	authed("GET /v1/search", s.handleSearch)
	authed("POST /v1/ai/compose", s.handleAiCompose)

	authed("POST /v1/devices", s.handleRegisterDevice)
	authed("DELETE /v1/devices/{id}", s.handleUnregisterDevice)

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
