// Command worker runs Calendium's background loops (port.SyncService):
// incremental provider sync per active account, due scheduled sends (Send
// Later / undo-send), and snooze/reminder wake-ups with push notifications.
package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"calendium/backend/internal/adapter/out/googleapi"
	"calendium/backend/internal/adapter/out/icsfeed"
	"calendium/backend/internal/adapter/out/msgraph"
	"calendium/backend/internal/adapter/out/nominatim"
	"calendium/backend/internal/adapter/out/openrouter"
	"calendium/backend/internal/adapter/out/paddle"
	"calendium/backend/internal/adapter/out/pgbus"
	"calendium/backend/internal/adapter/out/postgres"
	"calendium/backend/internal/adapter/out/push"
	"calendium/backend/internal/adapter/out/todoist"
	"calendium/backend/internal/config"
	"calendium/backend/internal/domain"
	"calendium/backend/internal/migrate"
	"calendium/backend/internal/port"
	"calendium/backend/internal/service"
	"calendium/backend/migrations"
)

const (
	// syncInterval paces incremental provider sync across all accounts.
	syncInterval = time.Minute
	// dueWorkInterval paces scheduled sends and snooze/reminder wake-ups;
	// it bounds how late an undo-send delivery can fire, so keep it short.
	dueWorkInterval = 5 * time.Second
	// aiJobInterval paces the background AI job queue drain.
	aiJobInterval = 15 * time.Second
	// todoSyncInterval paces the external todo-mirror sync (M2.8 Task 10).
	todoSyncInterval = time.Minute
	// expireHoldsInterval paces the booking-hold expiry sweep (unconfirmed
	// holds past their hold_expires_at are cancelled, freeing the slot).
	expireHoldsInterval = time.Minute
	// automationInterval paces the calendar automation engine (FocusGuard,
	// auto-decline, buffers, and the travel pass). RunAutomation is
	// idempotent; 5m keeps leave-now alert arming as responsive as the old
	// standalone travel loop while the other passes no-op when nothing
	// changed. Delivery of due alerts rides the 5s due-work loop.
	automationInterval = 5 * time.Minute
	// subscriptionRefreshPass paces the ICS feed refresh sweep; each feed is
	// only refetched when it is > 1h stale (service.SubscriptionRefresher),
	// so the pass itself can run more often than hourly without hammering.
	subscriptionRefreshPass = 15 * time.Minute
	// perAccountTimeout bounds one account's sync pass.
	perAccountTimeout = 5 * time.Minute
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(logger); err != nil {
		logger.Error("worker: fatal", "error", err)
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
	if err := cfg.ValidateCloudBilling(); err != nil {
		return err
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
	logger.Info("worker: migrations applied")

	store := postgres.NewStore(db)
	if err := store.SetTokenEncryptionKey(cfg.Crypto.TokenEncryptionKey); err != nil {
		return err
	}

	// Cross-process realtime bridge (M2 follow-up F1): the worker's bus is
	// the pg_notify publisher — every event it publishes crosses Postgres as
	// a compact {topic,type} envelope and is republished into cmd/api's
	// in-memory bus for SSE subscribers. The worker keeps NO local
	// subscribers; only the worker publishes to Postgres, so API-local
	// events are never delivered twice.
	bus := pgbus.NewPublisher(db, logger)

	// --- outbound gateways ---
	hc := &http.Client{Timeout: 30 * time.Second}

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

	var pushSender port.PushSender
	if cfg.Push != (config.Push{}) {
		pushSender = push.NewDispatcher(cfg.Push, hc)
	}

	// Maps (M2.8 Task 12): unconfigured leaves the provider nil and every
	// travel feature degrades silently (no vendor calls, no buffers, no
	// alerts).
	var mapsProvider port.MapsProvider
	if cfg.Maps.NominatimBaseURL != "" {
		mapsProvider = nominatim.New(cfg.Maps.NominatimBaseURL, cfg.Maps.OSRMBaseURL, hc)
	}

	// Billing reconciliation (docs/payments.md): re-reads stale subscription
	// mirrors from Paddle so a lost webhook never leaves a user entitled or
	// locked out for long. Cloud only; self-host has no biller.
	var billingSvc *service.BillingService
	if !cfg.Instance.SelfHosted {
		billingSvc = service.NewBillingService(service.BillingServiceDeps{
			Users:  store.Users(),
			Subs:   store.Subscriptions(),
			Events: store.BillingEvents(),
			Payments: paddle.NewClient(paddle.Config{
				Env:           cfg.Paddle.Env,
				APIKey:        cfg.Paddle.APIKey,
				WebhookSecret: cfg.Paddle.WebhookSecret,
				AnnualPriceID: cfg.Paddle.AnnualPriceID,
			}, hc),
			Clock:      service.SystemClock{},
			Tx:         store,
			SelfHosted: false,
			Logger:     logger,
		})
	}

	// --- AI job queue: gated on OPENROUTER_API_KEY, degrades to a
	// no-op loop-that-never-starts when unset. Pass AiJobs repo only when enabled. ---
	var aiGateway port.AI
	var aiJobsRepo port.AiJobRepo
	if cfg.OpenRouter.APIKey != "" {
		aiGateway = openrouter.NewClient(cfg.OpenRouter.APIKey, cfg.OpenRouter.Model, hc)
		aiJobsRepo = store.AiJobs()
	}

	syncSvc := service.NewSyncService(service.SyncServiceDeps{
		Accounts:          store.Accounts(),
		Labels:            store.Labels(),
		Threads:           store.Threads(),
		Messages:          store.Messages(),
		Drafts:            store.Drafts(),
		Calendars:         store.Calendars(),
		Events:            store.Events(),
		Devices:           store.Devices(),
		SyncState:         store.SyncStates(),
		MailProviders:     mailProviders,
		CalendarProviders: calendarProviders,
		OAuth:             oauth,
		Push:              pushSender,
		AiJobs:            aiJobsRepo,
		// Activity records team replied_at indicators on delivered sends;
		// Shares + Bus fan share.updated / activity.updated out through the
		// Postgres NOTIFY bridge so API SSE clients hear about worker-side
		// deliveries in realtime instead of waiting for a refetch.
		Activity:    postgres.NewTeamThreadActivityRepo(store),
		Shares:      postgres.NewThreadShareRepo(store),
		Bus:         bus,
		Classifiers: store.Classifiers(),
		Clock:       service.SystemClock{},
		// Leave-now travel alerts (M2.8 Task 12) ride the 5s due-work loop.
		TravelAlerts:  store.TravelAlerts(),
		CalendarPrefs: store.CalendarPrefs(),
	})
	calendarSvc := service.NewCalendarService(service.CalendarServiceDeps{
		Subscriptions:     store.Subscriptions(),
		Users:             store.Users(),
		Accounts:          store.Accounts(),
		Calendars:         store.Calendars(),
		Events:            store.Events(),
		Templates:         store.EventTemplates(),
		Sets:              store.CalendarSets(),
		CalendarProviders: calendarProviders,
		OAuth:             oauth,
		Clock:             service.SystemClock{},
		Settings:          store.UserSettings(),
		SelfHosted:        cfg.Instance.SelfHosted,
	})
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
		Clock:             service.SystemClock{},
		SelfHosted:        cfg.Instance.SelfHosted,
		PublicWebURL:      cfg.Instance.PublicWebURL,
		Logger:            logger,
	})
	// M2.8 Task 15: hourly ICS subscription refresh. Deliberately its own
	// loop: refresh is feed-cadenced (per-feed hourly), not per-user, and
	// writes only subscription_events — disjoint from RunAutomation's
	// managed-events surface.
	subscriptionRefresher := service.NewSubscriptionRefresher(service.SubscriptionRefresherDeps{
		Subs:    store.CalendarSubscriptions(),
		Fetcher: icsfeed.New(hc),
		Clock:   service.SystemClock{},
		Logger:  logger,
	})
	aiJobSvc := service.NewAIJobService(service.AIJobServiceDeps{
		Jobs:          store.AiJobs(),
		Usage:         store.AiUsage(),
		Accounts:      store.Accounts(),
		Threads:       store.Threads(),
		Messages:      store.Messages(),
		Drafts:        store.Drafts(),
		Labels:        store.Labels(),
		Classifiers:   store.Classifiers(),
		VoiceProfiles: store.VoiceProfiles(),
		Calendar:      calendarSvc,
		AI:            aiGateway,
		Clock:         service.SystemClock{},
		DailyLimit:    cfg.OpenRouter.DailyLimit,
		Logger:        logger,
	})
	autoSvc := service.NewAutomationService(service.AutomationServiceDeps{
		Prefs:       store.CalendarPrefs(),
		Accounts:    store.Accounts(),
		Calendars:   store.Calendars(),
		Events:      store.Events(),
		Managed:     store.ManagedEvents(),
		CalendarSvc: calendarSvc,
		// Entitlement gate: lapsed cloud users are skipped silently.
		Subscriptions: store.Subscriptions(),
		Users:         store.Users(),
		SelfHosted:    cfg.Instance.SelfHosted,
		// Travel pass (M2.8 Task 12): managed "Travel to …" blocks + leave
		// alerts, folded into the automation loop so a single writer owns
		// every managed-events surface. mapsProvider nil disables it.
		Maps:   mapsProvider,
		Alerts: store.TravelAlerts(),
		Clock:  service.SystemClock{},
		Logger: logger,
	})

	// --- Todo mirror sync (M2.8 Task 10): gated on the Todoist OAuth app
	// being configured; without it no vendor is wired and the loop never
	// starts. ---
	var todoSyncSvc *service.TodoSyncService
	if cfg.Todoist.ClientID != "" {
		todoSyncSvc = service.NewTodoSyncService(service.TodoSyncDeps{
			Integrations: postgres.NewIntegrationRepo(store),
			Tasks:        store.Tasks(),
			SyncState:    store.SyncStates(),
			// Floating vendor due datetimes resolve on the owner's wall clock.
			Prefs: store.CalendarPrefs(),
			Providers: map[domain.TaskSource]port.TodoProvider{
				domain.TaskSourceTodoist: todoist.NewClient(hc),
			},
			OAuth: map[domain.IntegrationVendor]port.OAuthGateway{
				domain.IntegrationTodoist: todoist.NewOAuth(cfg.Todoist.ClientID, cfg.Todoist.ClientSecret, hc),
			},
			Clock:  service.SystemClock{},
			Logger: logger,
		})
	}

	// --- loops ---
	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		runLoop(ctx, syncInterval, func(ctx context.Context) {
			syncAllAccounts(ctx, logger, store.Accounts(), syncSvc)
		})
	}()
	go func() {
		defer wg.Done()
		runLoop(ctx, dueWorkInterval, func(ctx context.Context) {
			if err := syncSvc.ProcessDueWork(ctx); err != nil {
				logger.Error("worker: process due work", "error", err)
			}
		})
	}()
	go func() {
		defer wg.Done()
		runLoop(ctx, expireHoldsInterval, func(ctx context.Context) {
			if err := scheduling.ExpireHolds(ctx); err != nil {
				logger.Error("worker: expire holds", "error", err)
			}
		})
	}()
	go func() {
		defer wg.Done()
		runLoop(ctx, automationInterval, func(ctx context.Context) {
			if err := autoSvc.RunAutomation(ctx); err != nil {
				logger.Error("worker: run automation", "error", err)
			}
		})
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		runLoop(ctx, subscriptionRefreshPass, func(ctx context.Context) {
			if err := subscriptionRefresher.RefreshDue(ctx); err != nil {
				logger.Error("worker: refresh calendar subscriptions", "error", err)
			}
		})
	}()
	if todoSyncSvc != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runLoop(ctx, todoSyncInterval, func(ctx context.Context) {
				if err := todoSyncSvc.SyncTodos(ctx); err != nil {
					logger.Error("worker: todo sync", "error", err)
				}
			})
		}()
	}
	if billingSvc != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runLoop(ctx, cfg.Billing.ReconcileInterval, func(ctx context.Context) {
				reconcilePass(ctx, logger, billingSvc)
			})
		}()
	}
	if aiGateway != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runLoop(ctx, aiJobInterval, func(ctx context.Context) {
				if err := aiJobSvc.ProcessDueAiJobs(ctx); err != nil {
					logger.Error("worker: process ai jobs", "error", err)
				}
			})
		}()
	}
	logger.Info("worker: loops started",
		"sync_interval", syncInterval.String(),
		"due_work_interval", dueWorkInterval.String(),
		"automation_interval", automationInterval.String(),
		"ai_jobs_enabled", aiGateway != nil,
		"travel_enabled", mapsProvider != nil,
		"todo_sync_enabled", todoSyncSvc != nil,
		"billing_reconcile_enabled", billingSvc != nil,
		"billing_reconcile_interval", cfg.Billing.ReconcileInterval.String(),
		"providers", len(mailProviders))
	wg.Wait()
	logger.Info("worker: shut down cleanly")
	return nil
}

// reconcilePass runs one billing reconciliation pass. A pass cut short by
// shutdown (context.Canceled) is expected and not logged as an error.
func reconcilePass(ctx context.Context, logger *slog.Logger, billing port.BillingService) {
	if err := billing.ReconcileSubscriptions(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("worker: reconcile subscriptions", "error", err)
	}
}

// runLoop invokes fn immediately and then on every tick until ctx ends.
func runLoop(ctx context.Context, interval time.Duration, fn func(context.Context)) {
	fn(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fn(ctx)
		}
	}
}

// syncAllAccounts runs one incremental mail + calendar pass per syncable
// account. Accounts fail independently; one broken grant must not stall
// the fleet.
func syncAllAccounts(ctx context.Context, logger *slog.Logger, accounts port.AccountRepo, syncSvc port.SyncService) {
	list, err := accounts.ListSyncable(ctx)
	if err != nil {
		logger.Error("worker: list syncable accounts", "error", err)
		return
	}
	for _, account := range list {
		if ctx.Err() != nil {
			return
		}
		accountCtx, cancel := context.WithTimeout(ctx, perAccountTimeout)
		err := syncSvc.SyncAccount(accountCtx, account.ID)
		cancel()
		if err != nil {
			logger.Error("worker: sync account",
				"account_id", account.ID, "provider", account.Provider, "error", err)
		}
	}
}
