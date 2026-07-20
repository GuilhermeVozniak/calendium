// Command api is the Calendium HTTP API entrypoint: the composition root
// that wires config → Postgres → outbound gateways → services → the
// httpapi handler, then serves with graceful shutdown.
package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"calendium/backend/internal/adapter/in/httpapi"
	"calendium/backend/internal/adapter/out/authjwt"
	"calendium/backend/internal/adapter/out/eventbus"
	"calendium/backend/internal/adapter/out/googleapi"
	"calendium/backend/internal/adapter/out/hubspot"
	"calendium/backend/internal/adapter/out/icsfeed"
	"calendium/backend/internal/adapter/out/msgraph"
	"calendium/backend/internal/adapter/out/nominatim"
	"calendium/backend/internal/adapter/out/openmeteo"
	"calendium/backend/internal/adapter/out/openrouter"
	"calendium/backend/internal/adapter/out/postgres"
	"calendium/backend/internal/adapter/out/push"
	"calendium/backend/internal/adapter/out/stripeapi"
	"calendium/backend/internal/adapter/out/todoist"
	"calendium/backend/internal/adapter/out/unsubscribe"
	"calendium/backend/internal/config"
	"calendium/backend/internal/domain"
	"calendium/backend/internal/migrate"
	"calendium/backend/internal/port"
	"calendium/backend/internal/service"
	"calendium/backend/migrations"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(logger); err != nil {
		logger.Error("api: fatal", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}

	// Fail fast on a guaranteed-broken auth configuration instead of booting
	// healthy and 401ing every request / advertising a relative authBaseUrl.
	if cfg.Auth.JWKSURL == "" {
		return errors.New("BETTER_AUTH_URL (or AUTH_JWKS_URL) is required: without it every authenticated request fails with 401")
	}
	if cfg.Instance.PublicWebURL == "" {
		return errors.New("PUBLIC_WEB_URL (or APP_URL) is required: GET /v1/instance must advertise an absolute Better Auth base URL")
	}

	// --- Postgres + migrations ---
	db, err := sql.Open("pgx", cfg.DB.URL)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = db.PingContext(pingCtx)
	cancel()
	if err != nil {
		return err
	}
	if err := migrate.Apply(ctx, db, migrations.FS); err != nil {
		return err
	}
	logger.Info("api: migrations applied")

	store := postgres.NewStore(db)
	if err := store.SetTokenEncryptionKey(cfg.Crypto.TokenEncryptionKey); err != nil {
		return err
	}

	// --- outbound gateways ---
	hc := &http.Client{Timeout: 30 * time.Second}

	verifier := authjwt.NewVerifier(cfg.Auth.JWKSURL, cfg.Auth.Issuer, hc)
	stripe := stripeapi.NewClient(cfg.Stripe.SecretKey, cfg.Stripe.WebhookSecret, cfg.Stripe.AnnualPriceID, hc)
	ai := openrouter.NewClient(cfg.OpenRouter.APIKey, cfg.OpenRouter.Model, hc)

	oauth := map[domain.Provider]port.OAuthGateway{}
	mailProviders := map[domain.Provider]port.MailProvider{}
	calendarProviders := map[domain.Provider]port.CalendarProvider{}
	if cfg.Google.ClientID != "" {
		g := googleapi.NewClient(cfg.Google.ClientID, cfg.Google.ClientSecret, hc)
		oauth[domain.ProviderGoogle] = g
		mailProviders[domain.ProviderGoogle] = g
		calendarProviders[domain.ProviderGoogle] = g
	}
	if cfg.Microsoft.ClientID != "" {
		m := msgraph.NewClient(cfg.Microsoft.ClientID, cfg.Microsoft.ClientSecret, hc)
		oauth[domain.ProviderMicrosoft] = m
		mailProviders[domain.ProviderMicrosoft] = m
		calendarProviders[domain.ProviderMicrosoft] = m
	}

	// M2.8 Task 9: per-user vendor OAuth (Todoist/HubSpot). Only vendors with
	// config present are wired; the rest answer 501 and are not advertised.
	integrationOAuth := map[domain.IntegrationVendor]port.OAuthGateway{}
	if cfg.Todoist.ClientID != "" {
		integrationOAuth[domain.IntegrationTodoist] = todoist.NewOAuth(cfg.Todoist.ClientID, cfg.Todoist.ClientSecret, hc)
	}
	if cfg.HubSpot.ClientID != "" {
		integrationOAuth[domain.IntegrationHubSpot] = hubspot.NewOAuth(cfg.HubSpot.ClientID, cfg.HubSpot.ClientSecret, hc)
	}

	// M2.8 Task 10: todo-tool adapters keyed by task source. Completing or
	// reopening a mirrored task writes through to the vendor before the local
	// mark; the worker owns the periodic mirror sync.
	todoProviders := map[domain.TaskSource]port.TodoProvider{}
	if cfg.Todoist.ClientID != "" {
		todoProviders[domain.TaskSourceTodoist] = todoist.NewClient(hc)
	}

	// --- services ---
	clock := service.SystemClock{}
	bus := eventbus.New()
	activityRepo := postgres.NewTeamThreadActivityRepo(store)

	users := service.NewUserService(store.Users(), store.UserPreferences(), clock)
	billing := service.NewBillingService(store.Users(), store.Subscriptions(), store.StripeEvents(), stripe, clock, store, cfg.Instance.SelfHosted)
	accounts := service.NewAccountService(store.Accounts(), store.OAuthStates(), store.SyncStates(), oauth, cfg.OAuth.AllowedRedirectURIs, cfg.Instance.PublicAPIURL, clock)
	integrations := service.NewIntegrationService(
		postgres.NewIntegrationRepo(store), store.OAuthStates(), integrationOAuth,
		cfg.OAuth.AllowedRedirectURIs, cfg.Instance.PublicAPIURL,
		// Todoist task purge on disconnect: mirrored Todoist rows are removed
		// so the task rail never shows tasks from a revoked grant.
		func(ctx context.Context, userID string) error {
			return store.Tasks().DeleteBySource(ctx, userID, domain.TaskSourceTodoist)
		},
		clock)
	mail := service.NewMailService(service.MailServiceDeps{
		Subscriptions: store.Subscriptions(),
		Accounts:      store.Accounts(),
		Threads:       store.Threads(),
		Messages:      store.Messages(),
		Drafts:        store.Drafts(),
		Snippets:      store.Snippets(),
		// Team-scoped snippets (M2.7 Task 11).
		Teams:         postgres.NewTeamRepo(store),
		Labels:        store.Labels(),
		Reactions:     store.Reactions(),
		Activity:      activityRepo,
		Bus:           bus,
		MailProviders: mailProviders,
		OAuth:         oauth,
		Unsubscriber:  unsubscribe.New(),
		Clock:         clock,
		SelfHosted:    cfg.Instance.SelfHosted,
		UndoSendGrace: cfg.Mail.UndoSendGrace,
		Logger:        logger,
	})
	calendars := service.NewCalendarService(service.CalendarServiceDeps{
		Subscriptions:     store.Subscriptions(),
		Accounts:          store.Accounts(),
		Calendars:         store.Calendars(),
		Events:            store.Events(),
		Templates:         store.EventTemplates(),
		Sets:              store.CalendarSets(),
		CalendarProviders: calendarProviders,
		OAuth:             oauth,
		Clock:             clock,
		Settings:          store.UserSettings(),
		SelfHosted:        cfg.Instance.SelfHosted,
		// Shared calendars (M2.7 Task 12): grants, audit trail, and the
		// team/user lookups behind grantee resolution.
		Shares: postgres.NewCalendarShareRepo(store),
		Audit:  postgres.NewAuditRepo(store),
		Teams:  postgres.NewTeamRepo(store),
		Users:  store.Users(),
		// Local-only event notes (M2.8 Task 4).
		Notes: store.EventNotes(),
		// Interesting-calendar ICS subscriptions (M2.8 Task 15). No vendor
		// config needed — the fetcher is plain HTTPS, so the routes always
		// work.
		CalendarSubs: store.CalendarSubscriptions(),
		IcsFetcher:   icsfeed.New(nil),
	})
	search := service.NewSearchService(store.Subscriptions(), store.Threads(), store.Events(), clock, cfg.Instance.SelfHosted)
	aiSvc := service.NewAIService(service.AIServiceDeps{
		Subscriptions: store.Subscriptions(),
		Accounts:      store.Accounts(),
		Threads:       store.Threads(),
		Messages:      store.Messages(),
		Drafts:        store.Drafts(),
		Usage:         store.AiUsage(),
		VoiceProfiles: store.VoiceProfiles(),
		Calendar:      calendars,
		Classifiers:   store.Classifiers(),
		AI:            ai,
		Clock:         clock,
		DailyLimit:    cfg.OpenRouter.DailyLimit,
		SelfHosted:    cfg.Instance.SelfHosted,
	})
	devices := service.NewDeviceService(store.Devices(), clock)
	prefs := service.NewPrefsService(store.Prefs(), store.CalendarPrefs(), store.Subscriptions(), clock, cfg.Instance.SelfHosted)
	scheduling := service.NewSchedulingService(service.SchedulingServiceDeps{
		Subscriptions:     store.Subscriptions(),
		Users:             store.Users(),
		Accounts:          store.Accounts(),
		Calendars:         store.Calendars(),
		Events:            store.Events(),
		Links:             store.BookingLinks(),
		Bookings:          store.Bookings(),
		Polls:             store.Polls(),
		Proposals:         store.TimeProposals(),
		Settings:          store.UserSettings(),
		Teams:             postgres.NewTeamRepo(store),
		Shares:            postgres.NewCalendarShareRepo(store),
		Tx:                store,
		CalendarProviders: calendarProviders,
		MailProviders:     mailProviders,
		OAuth:             oauth,
		Clock:             clock,
		SelfHosted:        cfg.Instance.SelfHosted,
		PublicWebURL:      cfg.Instance.PublicWebURL,
		Logger:            logger,
	})
	settingsSvc := service.NewSettingsService(store.UserSettings())
	// The Store has no accessors for the team repos; they are standalone
	// constructors over the same *Store (shared tx plumbing).
	teams := service.NewTeamService(service.TeamServiceDeps{
		Teams:       postgres.NewTeamRepo(store),
		Invitations: postgres.NewTeamInvitationRepo(store),
		Users:       store.Users(),
		Accounts:    store.Accounts(),
		Mail:        mailProviders,
		OAuth:       oauth,
		Subs:        store.Subscriptions(),
		Tx:          store,
		Clock:       clock,
		SelfHost:    cfg.Instance.SelfHosted,
		AppBaseURL:  cfg.Instance.AppBaseURL,
	})

	// Mention pushes reuse the worker's dispatcher; unconfigured push
	// degrades to nil (mention pushes silently disabled).
	var pushSender port.PushSender
	if cfg.Push != (config.Push{}) {
		pushSender = push.NewDispatcher(cfg.Push, hc)
	}

	// M2.7 collaboration: shared conversations (Task 7) + team comments
	// with @mentions (Task 9), one service behind port.CollabService.
	collab := service.NewCollabService(service.CollabServiceDeps{
		Shares:   postgres.NewThreadShareRepo(store),
		Comments: postgres.NewCommentRepo(store),
		Teams:    postgres.NewTeamRepo(store),
		Threads:  store.Threads(),
		Messages: store.Messages(),
		Accounts: store.Accounts(),
		Users:    store.Users(),
		Devices:  store.Devices(),
		Push:     pushSender,
		Bus:      bus,
		Subs:     store.Subscriptions(),
		Clock:    clock,
		SelfHost: cfg.Instance.SelfHosted,
	})

	// EA delegation mode (M2.7 Task 15): explicit grants + fail-closed
	// act-as authorization. Delegated mutations land in the unified
	// audit_entries trail (Task 12's AuditRepo).
	delegations := service.NewDelegationService(service.DelegationServiceDeps{
		Delegations: postgres.NewDelegationRepo(store),
		Audit:       postgres.NewAuditRepo(store),
		Users:       postgres.NewUserDirectory(store),
		Clock:       clock,
	})

	// M2.8: first-class tasks (local todos + mirrored provider todos).
	tasksSvc := service.NewTaskService(service.TaskServiceDeps{
		Subscriptions: store.Subscriptions(),
		Tasks:         store.Tasks(),
		Clock:         clock,
		TodoProviders: todoProviders,
		Integrations:  postgres.NewIntegrationRepo(store),
		SelfHosted:    cfg.Instance.SelfHosted,
	})

	// M2.7 Task 10: teammate read/reply indicators.
	teamActivitySvc := service.NewTeamActivityService(service.TeamActivityServiceDeps{
		Subscriptions: store.Subscriptions(),
		Accounts:      store.Accounts(),
		Threads:       store.Threads(),
		Teams:         postgres.NewTeamRepo(store),
		Activity:      activityRepo,
		Clock:         clock,
		SelfHosted:    cfg.Instance.SelfHosted,
	})

	// M2.8 Task 13: inline weather (Open-Meteo, keyless — on unless
	// OPEN_METEO_URL is explicitly emptied). Left nil when disabled:
	// GET /v1/weather answers 501 and capabilities.weather reads false.
	var weatherSvc port.WeatherService
	if cfg.Weather.BaseURL != "" {
		weatherSvc = service.NewWeatherService(service.WeatherServiceDeps{
			Provider:      openmeteo.New(cfg.Weather.BaseURL),
			Subscriptions: store.Subscriptions(),
			Clock:         clock,
			SelfHosted:    cfg.Instance.SelfHosted,
		})
	}

	// M2.8 Task 11: location autocomplete (Nominatim) + travel times (OSRM).
	// Unconfigured base URLs leave the provider unwired: the places route
	// answers 501 and features.maps stays false, so clients hide the
	// affordance (graceful degradation).
	var placesSvc port.PlacesService
	mapsConfigured := cfg.Maps.NominatimBaseURL != ""
	if mapsConfigured {
		placesSvc = service.NewPlacesService(service.PlacesServiceDeps{
			Subscriptions: store.Subscriptions(),
			Maps:          nominatim.New(cfg.Maps.NominatimBaseURL, cfg.Maps.OSRMBaseURL, hc),
			Clock:         clock,
			SelfHosted:    cfg.Instance.SelfHosted,
		})
	}

	// M2.8 Task 16: CRM contact context + explicit per-message email logging
	// (HubSpot). Consumes Task 9's integration-connection store (AES-GCM
	// token vault) through the narrow crmConnectionStore adapter below. Left
	// nil when the HubSpot OAuth app is unconfigured: /v1/crm answers 501.
	var crmSvc port.CrmService
	if cfg.HubSpot.ClientID != "" {
		crmSvc = service.NewCrmService(service.CrmServiceDeps{
			Subscriptions: store.Subscriptions(),
			Connections:   crmConnectionStore{repo: postgres.NewIntegrationRepo(store)},
			Providers: map[domain.IntegrationVendor]port.CrmProvider{
				domain.IntegrationHubSpot: hubspot.NewClient(hc),
			},
			OAuth: map[domain.IntegrationVendor]port.OAuthGateway{
				domain.IntegrationHubSpot: integrationOAuth[domain.IntegrationHubSpot],
			},
			Clock:      clock,
			SelfHosted: cfg.Instance.SelfHosted,
		})
	}

	// --- instance discovery document (GET /v1/instance) ---
	mode := httpapi.ModeCloud
	if cfg.Instance.SelfHosted {
		mode = httpapi.ModeSelfHost
	}
	webPushConfigured := cfg.Push.VAPID.PublicKey != "" && cfg.Push.VAPID.PrivateKey != ""
	pushConfigured := cfg.Push.APNs.KeyP8 != "" ||
		cfg.Push.FCM.ServiceAccountJSON != "" ||
		webPushConfigured

	// vapidPublicKey is advertised only when web push is configured so web
	// clients can subscribe the service worker.
	vapidPublicKey := ""
	if webPushConfigured {
		vapidPublicKey = cfg.Push.VAPID.PublicKey
	}

	// authBaseUrl is where clients reach Better Auth (hosted by the web app).
	authBaseURL := strings.TrimRight(cfg.Instance.PublicWebURL, "/") + "/api/auth"
	authProviders := []string{"email"}
	if cfg.Google.ClientID != "" {
		authProviders = append(authProviders, "google")
	}
	if cfg.Apple.ClientID != "" {
		authProviders = append(authProviders, "apple")
	}
	instance := httpapi.InstanceInfo{
		Name:            cfg.Instance.Name,
		Mode:            mode,
		Version:         httpapi.Version,
		AuthBaseURL:     authBaseURL,
		AuthProviders:   authProviders,
		UndoSendSeconds: int(cfg.Mail.UndoSendGrace / time.Second),
		VapidPublicKey:  vapidPublicKey,
		Features: httpapi.InstanceFeatures{
			Billing:   !cfg.Instance.SelfHosted,
			Google:    cfg.Google.ClientID != "",
			Microsoft: cfg.Microsoft.ClientID != "",
			AI:        cfg.OpenRouter.APIKey != "",
			Push:      pushConfigured,
			Maps:      mapsConfigured,
		},
		// Only vendors whose config is present are advertised (M2.8).
		Capabilities: httpapi.InstanceCapabilities{
			Todoist: cfg.Todoist.ClientID != "",
			HubSpot: cfg.HubSpot.ClientID != "",
			Maps:    mapsConfigured,
			Weather: cfg.Weather.BaseURL != "",
		},
	}

	// --- HTTP server ---
	handler := httpapi.New(httpapi.Deps{
		Logger:    logger,
		Verifier:  verifier,
		Users:     users,
		Billing:   billing,
		Accounts:  accounts,
		Mail:      mail,
		Calendars: calendars,
		// The calendar service also implements the sharing management port.
		CalendarShares: calendars,
		Search:         search,
		AI:             aiSvc,
		Devices:        devices,
		Prefs:          prefs,
		Scheduling:     scheduling,
		Settings:       settingsSvc,
		Collab:         collab,
		Payments:       stripe,
		// M2.7 collaboration: team service + in-process SSE fan-out. The
		// stream scopes each subscriber to user:<id> plus the caller's real
		// team:<id> memberships resolved through Teams.
		Teams:  teams,
		Events: bus,
		// M2.7 Task 15: EA delegation grants + act-as + audit surface.
		Delegations: delegations,
		// M2.7 Task 10: teammate read/reply indicators.
		TeamActivity: teamActivitySvc,
		// M2.8: first-class tasks.
		Tasks: tasksSvc,
		// M2.8 Task 13: inline weather (nil when disabled → 501).
		Weather: weatherSvc,
		// M2.8 Task 11: location autocomplete (nil when maps unconfigured).
		Places: placesSvc,
		// M2.8 Task 9: per-user vendor integrations.
		Integrations: integrations,
		// M2.8 Task 16: CRM contact context + explicit email logging (nil
		// when the HubSpot OAuth app is unconfigured → /v1/crm answers 501).
		Crm:                crmSvc,
		Instance:           instance,
		CORSAllowedOrigins: cfg.HTTP.CORSAllowedOrigins,
	})

	srv := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	logger.Info("api: listening", "addr", cfg.HTTP.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	logger.Info("api: shut down cleanly")
	return nil
}

// crmConnectionStore adapts Task 9's IntegrationRepo (connection rows + the
// AES-GCM token vault) to port.CrmConnectionStore — the narrow decrypted-
// token view the CRM service consumes. The CRM side never grows its own
// OAuth/token storage.
type crmConnectionStore struct{ repo *postgres.IntegrationRepo }

var _ port.CrmConnectionStore = crmConnectionStore{}

func (s crmConnectionStore) ListByUser(ctx context.Context, userID string) ([]port.CrmConnection, error) {
	conns, err := s.repo.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]port.CrmConnection, 0, len(conns))
	for _, c := range conns {
		t, err := s.repo.GetTokens(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, port.CrmConnection{ID: c.ID, UserID: c.UserID, Vendor: c.Vendor, Tokens: t})
	}
	return out, nil
}

func (s crmConnectionStore) UpdateTokens(ctx context.Context, connectionID string, t port.TokenSet) error {
	return s.repo.SaveTokens(ctx, connectionID, t)
}
