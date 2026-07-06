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
	"calendium/backend/internal/adapter/out/googleapi"
	"calendium/backend/internal/adapter/out/msgraph"
	"calendium/backend/internal/adapter/out/openrouter"
	"calendium/backend/internal/adapter/out/postgres"
	"calendium/backend/internal/adapter/out/stripeapi"
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

	// --- Postgres + migrations ---
	db, err := sql.Open("pgx", cfg.DB.URL)
	if err != nil {
		return err
	}
	defer db.Close()

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

	// --- services ---
	clock := service.SystemClock{}

	users := service.NewUserService(store.Users(), clock)
	billing := service.NewBillingService(store.Users(), store.Subscriptions(), store.StripeEvents(), stripe, clock, store, cfg.Instance.SelfHosted)
	accounts := service.NewAccountService(store.Accounts(), store.OAuthStates(), store.SyncStates(), oauth, cfg.OAuth.AllowedRedirectURIs, clock)
	mail := service.NewMailService(service.MailServiceDeps{
		Subscriptions: store.Subscriptions(),
		Accounts:      store.Accounts(),
		Threads:       store.Threads(),
		Messages:      store.Messages(),
		Drafts:        store.Drafts(),
		Snippets:      store.Snippets(),
		MailProviders: mailProviders,
		OAuth:         oauth,
		Clock:         clock,
		SelfHosted:    cfg.Instance.SelfHosted,
		UndoSendGrace: cfg.Mail.UndoSendGrace,
	})
	calendars := service.NewCalendarService(service.CalendarServiceDeps{
		Subscriptions:     store.Subscriptions(),
		Accounts:          store.Accounts(),
		Calendars:         store.Calendars(),
		Events:            store.Events(),
		CalendarProviders: calendarProviders,
		OAuth:             oauth,
		Clock:             clock,
		SelfHosted:        cfg.Instance.SelfHosted,
	})
	search := service.NewSearchService(store.Subscriptions(), store.Threads(), store.Events(), clock, cfg.Instance.SelfHosted)
	aiSvc := service.NewAIService(service.AIServiceDeps{
		Subscriptions: store.Subscriptions(),
		Accounts:      store.Accounts(),
		Threads:       store.Threads(),
		Messages:      store.Messages(),
		Drafts:        store.Drafts(),
		AI:            ai,
		Clock:         clock,
		SelfHosted:    cfg.Instance.SelfHosted,
	})
	devices := service.NewDeviceService(store.Devices(), clock)

	// --- instance discovery document (GET /v1/instance) ---
	mode := httpapi.ModeCloud
	if cfg.Instance.SelfHosted {
		mode = httpapi.ModeSelfHost
	}
	pushConfigured := cfg.Push.APNs.KeyP8 != "" ||
		cfg.Push.FCM.ServiceAccountJSON != "" ||
		(cfg.Push.VAPID.PublicKey != "" && cfg.Push.VAPID.PrivateKey != "")

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
		Name:          cfg.Instance.Name,
		Mode:          mode,
		Version:       httpapi.Version,
		AuthBaseURL:   authBaseURL,
		AuthProviders: authProviders,
		Features: httpapi.InstanceFeatures{
			Billing:   !cfg.Instance.SelfHosted,
			Google:    cfg.Google.ClientID != "",
			Microsoft: cfg.Microsoft.ClientID != "",
			AI:        cfg.OpenRouter.APIKey != "",
			Push:      pushConfigured,
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
		Search:    search,
		AI:        aiSvc,
		Devices:   devices,
		Payments:  stripe,
		Instance:  instance,
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
