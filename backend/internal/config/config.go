// Package config loads backend configuration from the environment
// (docs/architecture.md "Environment"; see backend/.env.example).
package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	netmail "net/mail"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// HTTP configures the API listener.
type HTTP struct {
	// Addr is the listen address (HTTP_ADDR, or ":$PORT"; default ":8080").
	Addr string
	// CORSAllowedOrigins is the extra browser-CORS allowlist
	// (CORS_ALLOWED_ORIGINS, comma-separated) reflected in addition to the
	// built-in localhost-dev and Wails WebView origins.
	CORSAllowedOrigins []string
	// AllowDevOrigins (ALLOW_DEV_ORIGINS, default false) reflects
	// http(s)://localhost, 127.0.0.1 and ::1 origins in CORS. The Wails
	// WebView origins and CORSAllowedOrigins are always reflected; this gates
	// only the local dev servers. backend/.env.example (run-the-binary
	// template) sets it true; the production root .env leaves it blank.
	AllowDevOrigins bool
	// TrustProxy (TRUST_PROXY, default false) makes X-Forwarded-* from peers
	// inside TrustedProxyCIDRs authoritative for client IP, scheme and host.
	TrustProxy bool
	// TrustedProxyCIDRs (TRUSTED_PROXY_CIDRS, comma-separated) is the proxy
	// allowlist; defaults to DefaultTrustedProxyCIDRs. A bad entry is a boot
	// error.
	TrustedProxyCIDRs []netip.Prefix
}

// DefaultTrustedProxyCIDRs is the TRUSTED_PROXY_CIDRS default: loopback,
// RFC 1918 and IPv6 ULA — "the proxy is on this host or this private
// network". Identical to the web tier's default (apps/web).
var DefaultTrustedProxyCIDRs = []string{
	"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "::1/128", "fc00::/7",
}

// RateLimits are the per-class request budgets per minute (RATE_LIMIT_*);
// 0 disables a class. Mirrors httpapi.RateLimits (the composition root
// copies field by field so httpapi never imports config).
type RateLimits struct {
	PublicReadPerMin  int // RATE_LIMIT_PUBLIC_READ_PER_MIN (default 60)
	PublicWritePerMin int // RATE_LIMIT_PUBLIC_WRITE_PER_MIN (default 5)
	UserPerMin        int // RATE_LIMIT_USER_PER_MIN (default 600)
	MutateHeavyPerMin int // RATE_LIMIT_MUTATE_HEAVY_PER_MIN (default 30)
	SearchPerMin      int // RATE_LIMIT_SEARCH_PER_MIN (default 120)
}

// Shutdown tunes graceful shutdown for api and worker.
type Shutdown struct {
	// Timeout (SHUTDOWN_TIMEOUT, default 30s) bounds in-flight work.
	Timeout time.Duration
	// DrainDelay (SHUTDOWN_DRAIN_DELAY, default 0s) keeps /readyz at 503
	// before listeners close so a load balancer can stop routing.
	DrainDelay time.Duration
}

// Log selects the slog handler (LOG_FORMAT json|text, LOG_LEVEL).
type Log struct {
	Format string
	Level  slog.Level
}

// DB configures Postgres.
type DB struct {
	URL string // DATABASE_URL
}

// Auth configures verification of Better Auth JWTs. Better Auth is hosted by
// the Next.js web app at ${BetterAuthURL}/api/auth/*; the Go backend is a pure
// resource server that fetches the JWKS and verifies EdDSA/RS256/ES256
// signatures offline. JWKSURL and Issuer are derived from BetterAuthURL when
// not set explicitly.
type Auth struct {
	// BetterAuthURL is the public web origin hosting Better Auth
	// (BETTER_AUTH_URL), e.g. https://app.calendium.com (no trailing slash).
	BetterAuthURL string
	// JWKSURL is the Better Auth JWKS endpoint (AUTH_JWKS_URL); defaults to
	// ${BetterAuthURL}/api/auth/jwks.
	JWKSURL string
	// Issuer is the expected token `iss` claim (AUTH_ISSUER); defaults to
	// ${BetterAuthURL}. "" disables issuer pinning.
	Issuer string
}

// Apple configures Sign in with Apple. The backend does not perform the login
// itself (Better Auth does), but reports it in GET /v1/instance when set.
type Apple struct {
	ClientID     string // APPLE_CLIENT_ID (Services ID)
	ClientSecret string // APPLE_CLIENT_SECRET
}

// Google configures the Google OAuth app (Gmail + Google Calendar).
type Google struct {
	ClientID     string // GOOGLE_CLIENT_ID
	ClientSecret string // GOOGLE_CLIENT_SECRET
}

// Microsoft configures the Microsoft Graph OAuth app.
type Microsoft struct {
	ClientID     string // MS_CLIENT_ID
	ClientSecret string // MS_CLIENT_SECRET
}

// Todoist configures the Todoist OAuth app for per-user task integrations
// (M2.8). Unset leaves the vendor unwired: its endpoints answer 501 and
// GET /v1/instance does not advertise it.
type Todoist struct {
	ClientID     string // TODOIST_CLIENT_ID
	ClientSecret string // TODOIST_CLIENT_SECRET
}

// HubSpot configures the HubSpot OAuth app for per-user CRM integrations
// (M2.8). Unset leaves the vendor unwired (501 + not advertised).
type HubSpot struct {
	ClientID     string // HUBSPOT_CLIENT_ID
	ClientSecret string // HUBSPOT_CLIENT_SECRET
}

// Paddle environment values (PADDLE_ENV).
const (
	PaddleEnvSandbox = "sandbox"
	PaddleEnvLive    = "live"
)

// Paddle configures billing (docs/payments.md). Cloud only: with
// SELF_HOSTED=false every field but Env is mandatory (ValidateCloudBilling).
type Paddle struct {
	Env           string // PADDLE_ENV: sandbox (default) | live
	APIKey        string // PADDLE_API_KEY
	WebhookSecret string // PADDLE_WEBHOOK_SECRET (notification destination secret)
	AnnualPriceID string // PADDLE_PRICE_ID_ANNUAL
}

// Billing holds billing tunables.
type Billing struct {
	// ReconcileInterval paces the worker's subscription reconciliation loop
	// (BILLING_RECONCILE_INTERVAL, default 6h).
	ReconcileInterval time.Duration
}

// APNs configures Apple push (HTTP/2 + ES256 JWT).
type APNs struct {
	KeyID  string // APNS_KEY_ID
	TeamID string // APNS_TEAM_ID
	KeyP8  string // APNS_KEY_P8 (PEM contents, not a path)
}

// FCM configures Firebase Cloud Messaging v1.
type FCM struct {
	ServiceAccountJSON string // FCM_SERVICE_ACCOUNT_JSON
}

// VAPID configures Web Push.
type VAPID struct {
	PublicKey  string // VAPID_PUBLIC_KEY
	PrivateKey string // VAPID_PRIVATE_KEY
}

// Push groups the three push transports.
type Push struct {
	APNs  APNs
	FCM   FCM
	VAPID VAPID
}

// OpenRouter configures the AI gateway.
type OpenRouter struct {
	APIKey string // OPENROUTER_API_KEY
	Model  string // OPENROUTER_MODEL (default "openrouter/auto")
	// DailyLimit is the per-user daily AI job budget (AI_DAILY_LIMIT,
	// default 300).
	DailyLimit int
}

// Maps configures the location-autocomplete / travel-time vendor endpoints
// (M2.8 Task 11): a Nominatim geocoding instance and an OSRM routing
// instance. Both empty (the default) leaves the maps feature off — the
// places routes answer 501 and GET /v1/instance advertises maps=false.
type Maps struct {
	NominatimBaseURL string // MAPS_NOMINATIM_URL
	OSRMBaseURL      string // MAPS_OSRM_URL
}

// Crypto holds secrets-at-rest material.
type Crypto struct {
	// TokenEncryptionKey is the 32-byte AES-256-GCM key for provider
	// refresh tokens (TOKEN_ENCRYPTION_KEY, 64 hex chars).
	TokenEncryptionKey []byte
}

// Mail holds mail behavior tunables.
type Mail struct {
	// UndoSendGrace delays "send now" so it can be undone
	// (UNDO_SEND_SECONDS; default 15s).
	UndoSendGrace time.Duration
}

// OAuth holds provider-connect flow settings.
type OAuth struct {
	// AllowedRedirectURIs is the server-side allowlist the client-supplied
	// redirectUrl is validated against (OAUTH_ALLOWED_REDIRECT_URIS,
	// comma-separated, appended to the built-in localhost + calendium://
	// defaults). Guards the connect flow against open-redirect abuse.
	AllowedRedirectURIs []string
}

// defaultRedirectAllowlist covers local development and the app's custom URL
// scheme; production web origins are added via OAUTH_ALLOWED_REDIRECT_URIS.
var defaultRedirectAllowlist = []string{
	"http://localhost",
	"https://localhost",
	"http://127.0.0.1",
	"calendium://",
}

// Weather configures the Open-Meteo forecast adapter (M2.8 Task 13).
// Open-Meteo is keyless, so weather defaults to ON; setting OPEN_METEO_URL
// to an empty value disables it (adapter unwired, GET /v1/weather → 501).
type Weather struct {
	// BaseURL is the Open-Meteo API origin (OPEN_METEO_URL, default
	// https://api.open-meteo.com). Empty disables weather.
	BaseURL string
}

// Instance describes the deployment mode surfaced to clients via the public
// GET /v1/instance discovery endpoint (open-core: self-hosted vs. cloud).
type Instance struct {
	// SelfHosted (SELF_HOSTED, default false) marks a free self-hosted
	// deployment: entitlement gating becomes a no-op (all features unlocked)
	// and the billing endpoints return domain.ErrSelfHosted instead of
	// calling the payments provider.
	SelfHosted bool
	// Name (INSTANCE_NAME, default "Calendium") is shown to clients.
	Name string
	// PublicWebURL (PUBLIC_WEB_URL, falling back to APP_URL) is the public web
	// origin advertised to clients (Better Auth base URL); optional.
	PublicWebURL string
	// PublicAPIURL (PUBLIC_API_URL) is the API's own public origin, used to
	// build the provider OAuth redirect_uri (${PublicAPIURL}/v1/accounts/
	// callback/{provider}). When empty it is derived per-request. Optional.
	PublicAPIURL string
	// AppBaseURL (APP_BASE_URL, falling back to PublicWebURL) is the web app
	// origin used to build emailed team-invite links
	// (${AppBaseURL}/invite/<token>). Optional.
	AppBaseURL string
}

// SMTP configures the instance's own transactional sender (SMTP_*): team
// invitations go through it when the inviter has no connected mailbox, and
// the web app uses the same variables for verification and password-reset
// mail. Unset on self-host disables it (invitations fall back to copyable
// links); cloud mode (SELF_HOSTED=false) requires SMTP_HOST and SMTP_FROM —
// see ValidateCloudEmail.
type SMTP struct {
	Host   string // SMTP_HOST
	Port   int    // SMTP_PORT (default 587)
	User   string // SMTP_USER (optional; must be paired with SMTP_PASS)
	Pass   string // SMTP_PASS
	From   string // SMTP_FROM: "addr" or "Name <addr>"
	Secure bool   // SMTP_SECURE: true = implicit TLS, false = STARTTLS (default)
}

// Configured reports whether an SMTP sender is set up (SMTP_HOST present).
func (s SMTP) Configured() bool { return s.Host != "" }

// partialError reports a half-set SMTP block: SMTP_HOST, SMTP_FROM,
// SMTP_USER or SMTP_PASS present without SMTP_HOST+SMTP_FROM, or a lone
// SMTP_USER/SMTP_PASS. SMTP_PORT/SMTP_SECURE alone are fine (the env
// templates ship them pre-filled).
func (s SMTP) partialError() error {
	if s.Host == "" && s.From == "" && s.User == "" && s.Pass == "" {
		return nil
	}
	var missing []string
	if s.Host == "" {
		missing = append(missing, "SMTP_HOST")
	}
	if s.From == "" {
		missing = append(missing, "SMTP_FROM")
	}
	if s.User == "" && s.Pass != "" {
		missing = append(missing, "SMTP_USER")
	}
	if s.User != "" && s.Pass == "" {
		missing = append(missing, "SMTP_PASS")
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("SMTP_* is partially configured: %s", strings.Join(missing, ", "))
}

// Config is the full backend configuration.
type Config struct {
	HTTP       HTTP
	DB         DB
	Auth       Auth
	Google     Google
	Apple      Apple
	Microsoft  Microsoft
	Todoist    Todoist
	HubSpot    HubSpot
	Paddle     Paddle
	Billing    Billing
	Push       Push
	OpenRouter OpenRouter
	Crypto     Crypto
	Mail       Mail
	OAuth      OAuth
	Instance   Instance
	Weather    Weather
	Maps       Maps
	SMTP       SMTP
	RateLimits RateLimits
	Shutdown   Shutdown
	Log        Log
}

type options struct{ requirePublicWebURL bool }

// Option tunes FromEnv per binary.
type Option func(*options)

// RequirePublicWebURL makes an empty PUBLIC_WEB_URL/APP_URL a hard error in
// both modes (cmd/api: GET /v1/instance must advertise an absolute Better
// Auth base URL). Without it the worker gets a cloud error / self-host
// warning.
func RequirePublicWebURL() Option {
	return func(o *options) { o.requirePublicWebURL = true }
}

// FromEnv builds a Config from environment variables. DATABASE_URL and a
// valid TOKEN_ENCRYPTION_KEY are required; everything else is optional so
// partial deployments (e.g. no push) still boot, with the affected adapters
// left unwired by the composition root. The second result carries
// non-fatal warnings (self-host only) the caller logs at startup.
func FromEnv(opts ...Option) (Config, []string, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	var errs []error
	var warnings []string

	cfg := Config{
		DB: DB{URL: os.Getenv("DATABASE_URL")},
		Auth: Auth{
			BetterAuthURL: strings.TrimRight(os.Getenv("BETTER_AUTH_URL"), "/"),
			JWKSURL:       os.Getenv("AUTH_JWKS_URL"),
			Issuer:        os.Getenv("AUTH_ISSUER"),
		},
		Google: Google{
			ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
			ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		},
		Apple: Apple{
			ClientID:     os.Getenv("APPLE_CLIENT_ID"),
			ClientSecret: os.Getenv("APPLE_CLIENT_SECRET"),
		},
		Microsoft: Microsoft{
			ClientID:     os.Getenv("MS_CLIENT_ID"),
			ClientSecret: os.Getenv("MS_CLIENT_SECRET"),
		},
		Todoist: Todoist{
			ClientID:     os.Getenv("TODOIST_CLIENT_ID"),
			ClientSecret: os.Getenv("TODOIST_CLIENT_SECRET"),
		},
		HubSpot: HubSpot{
			ClientID:     os.Getenv("HUBSPOT_CLIENT_ID"),
			ClientSecret: os.Getenv("HUBSPOT_CLIENT_SECRET"),
		},
		Paddle: Paddle{
			Env:           os.Getenv("PADDLE_ENV"),
			APIKey:        os.Getenv("PADDLE_API_KEY"),
			WebhookSecret: os.Getenv("PADDLE_WEBHOOK_SECRET"),
			AnnualPriceID: os.Getenv("PADDLE_PRICE_ID_ANNUAL"),
		},
		Push: Push{
			APNs: APNs{
				KeyID:  os.Getenv("APNS_KEY_ID"),
				TeamID: os.Getenv("APNS_TEAM_ID"),
				KeyP8:  os.Getenv("APNS_KEY_P8"),
			},
			FCM:   FCM{ServiceAccountJSON: os.Getenv("FCM_SERVICE_ACCOUNT_JSON")},
			VAPID: VAPID{PublicKey: os.Getenv("VAPID_PUBLIC_KEY"), PrivateKey: os.Getenv("VAPID_PRIVATE_KEY")},
		},
		OpenRouter: OpenRouter{
			APIKey: os.Getenv("OPENROUTER_API_KEY"),
			Model:  os.Getenv("OPENROUTER_MODEL"),
		},
	}

	cfg.HTTP.Addr = os.Getenv("HTTP_ADDR")
	if cfg.HTTP.Addr == "" {
		if p := os.Getenv("PORT"); p != "" {
			cfg.HTTP.Addr = ":" + p
		} else {
			cfg.HTTP.Addr = ":8080"
		}
	}

	// SELF_HOSTED is parsed first: the default-password and PUBLIC_WEB_URL
	// rules below are errors in cloud mode and warnings on self-host.
	cfg.Instance.SelfHosted = envBool("SELF_HOSTED", &errs)

	if cfg.DB.URL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	} else if u, err := url.Parse(cfg.DB.URL); err == nil && u.User != nil {
		if pw, _ := u.User.Password(); pw == "change-me-please" || pw == "calendium" {
			const msg = "DATABASE_URL uses a default password; set POSTGRES_PASSWORD"
			if cfg.Instance.SelfHosted {
				warnings = append(warnings, msg)
			} else {
				errs = append(errs, errors.New(msg))
			}
		}
	}

	// Derive the Better Auth JWKS URL and issuer from BETTER_AUTH_URL unless
	// they were set explicitly.
	if cfg.Auth.JWKSURL == "" && cfg.Auth.BetterAuthURL != "" {
		cfg.Auth.JWKSURL = cfg.Auth.BetterAuthURL + "/api/auth/jwks"
	}
	if cfg.Auth.Issuer == "" {
		cfg.Auth.Issuer = cfg.Auth.BetterAuthURL
	}

	switch key := os.Getenv("TOKEN_ENCRYPTION_KEY"); key {
	case "":
		errs = append(errs, errors.New("TOKEN_ENCRYPTION_KEY is required (64 hex chars / 32 bytes for AES-256-GCM)"))
	default:
		raw, err := hex.DecodeString(key)
		if err != nil || len(raw) != 32 {
			errs = append(errs, errors.New("TOKEN_ENCRYPTION_KEY must be exactly 64 hex chars (32 bytes)"))
		} else {
			cfg.Crypto.TokenEncryptionKey = raw
		}
	}

	if cfg.OpenRouter.Model == "" {
		cfg.OpenRouter.Model = "openrouter/auto"
	}

	cfg.OpenRouter.DailyLimit = 300
	if v := os.Getenv("AI_DAILY_LIMIT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			errs = append(errs, fmt.Errorf("AI_DAILY_LIMIT must be a positive integer, got %q", v))
		} else {
			cfg.OpenRouter.DailyLimit = n
		}
	}

	cfg.Mail.UndoSendGrace = 15 * time.Second
	if v := os.Getenv("UNDO_SEND_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			errs = append(errs, fmt.Errorf("UNDO_SEND_SECONDS must be a non-negative integer, got %q", v))
		} else {
			cfg.Mail.UndoSendGrace = time.Duration(n) * time.Second
		}
	}

	switch cfg.Paddle.Env {
	case "":
		cfg.Paddle.Env = PaddleEnvSandbox
	case PaddleEnvSandbox, PaddleEnvLive:
	default:
		errs = append(errs, fmt.Errorf("PADDLE_ENV must be %q or %q, got %q", PaddleEnvSandbox, PaddleEnvLive, cfg.Paddle.Env))
	}

	cfg.Billing.ReconcileInterval = 6 * time.Hour
	if v := os.Getenv("BILLING_RECONCILE_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("BILLING_RECONCILE_INTERVAL must be a positive Go duration (e.g. 6h), got %q", v))
		} else {
			cfg.Billing.ReconcileInterval = d
		}
	}

	cfg.Instance.Name = os.Getenv("INSTANCE_NAME")
	if cfg.Instance.Name == "" {
		cfg.Instance.Name = "Calendium"
	}
	// PUBLIC_WEB_URL wins over APP_URL when both are set (the public origin
	// advertised to clients); APP_URL is the fallback for older deployments.
	cfg.Instance.PublicWebURL = os.Getenv("PUBLIC_WEB_URL")
	if cfg.Instance.PublicWebURL == "" {
		cfg.Instance.PublicWebURL = os.Getenv("APP_URL")
	}
	cfg.Instance.PublicAPIURL = os.Getenv("PUBLIC_API_URL")
	// APP_BASE_URL wins when set; the public web origin is the natural
	// fallback since the web app hosts the /invite/<token> accept page.
	cfg.Instance.AppBaseURL = os.Getenv("APP_BASE_URL")
	if cfg.Instance.AppBaseURL == "" {
		cfg.Instance.AppBaseURL = cfg.Instance.PublicWebURL
	}
	if cfg.Instance.PublicWebURL == "" {
		switch {
		case o.requirePublicWebURL:
			errs = append(errs, errors.New("PUBLIC_WEB_URL (or APP_URL) is required: GET /v1/instance must advertise an absolute Better Auth base URL"))
		case cfg.Instance.SelfHosted:
			warnings = append(warnings, "PUBLIC_WEB_URL (or APP_URL) is not set; emailed links and GET /v1/instance need it")
		default:
			errs = append(errs, errors.New("PUBLIC_WEB_URL (or APP_URL) is required in cloud mode"))
		}
	}

	// Transactional email (piece 2). Port/secure/from are validated even when
	// SMTP_HOST is blank so a typo surfaces at boot; the cloud-mode
	// requirement lives in ValidateCloudEmail so FromEnv stays mode-agnostic.
	cfg.SMTP = SMTP{
		Host: strings.TrimSpace(os.Getenv("SMTP_HOST")),
		Port: 587,
		User: os.Getenv("SMTP_USER"),
		Pass: os.Getenv("SMTP_PASS"),
		From: strings.TrimSpace(os.Getenv("SMTP_FROM")),
	}
	if v := os.Getenv("SMTP_PORT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			errs = append(errs, fmt.Errorf("SMTP_PORT must be an integer between 1 and 65535, got %q", v))
		} else {
			cfg.SMTP.Port = n
		}
	}
	cfg.SMTP.Secure = envBool("SMTP_SECURE", &errs)
	if cfg.SMTP.From != "" {
		if _, err := netmail.ParseAddress(cfg.SMTP.From); err != nil {
			errs = append(errs, fmt.Errorf("SMTP_FROM must be an email address or \"Name <addr>\", got %q", cfg.SMTP.From))
		}
	}
	if err := cfg.SMTP.partialError(); err != nil {
		errs = append(errs, err)
	}
	cfg.HTTP.AllowDevOrigins = envBool("ALLOW_DEV_ORIGINS", &errs)

	// Weather (M2.8 Task 13): keyless vendor, so the default is on. LookupEnv
	// (not Getenv) so an explicitly empty OPEN_METEO_URL means "disable",
	// while an unset one means "use the public API".
	if v, ok := os.LookupEnv("OPEN_METEO_URL"); ok {
		cfg.Weather.BaseURL = strings.TrimRight(strings.TrimSpace(v), "/")
	} else {
		cfg.Weather.BaseURL = "https://api.open-meteo.com"
	}

	cfg.OAuth.AllowedRedirectURIs = append([]string(nil), defaultRedirectAllowlist...)
	if v := os.Getenv("OAUTH_ALLOWED_REDIRECT_URIS"); v != "" {
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				cfg.OAuth.AllowedRedirectURIs = append(cfg.OAuth.AllowedRedirectURIs, p)
			}
		}
	}

	cfg.Maps = Maps{
		NominatimBaseURL: strings.TrimRight(os.Getenv("MAPS_NOMINATIM_URL"), "/"),
		OSRMBaseURL:      strings.TrimRight(os.Getenv("MAPS_OSRM_URL"), "/"),
	}

	if v := os.Getenv("CORS_ALLOWED_ORIGINS"); v != "" {
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				cfg.HTTP.CORSAllowedOrigins = append(cfg.HTTP.CORSAllowedOrigins, p)
			}
		}
	}

	parseHardening(&cfg, &errs)

	if len(errs) > 0 {
		return Config{}, nil, errors.Join(errs...)
	}
	return cfg, warnings, nil
}

// parseHardening reads the platform-hardening knobs: proxy trust, rate
// limits, shutdown timing and logging.
func parseHardening(cfg *Config, errs *[]error) {
	cfg.HTTP.TrustProxy = envBool("TRUST_PROXY", errs)
	cidrs := DefaultTrustedProxyCIDRs
	if v := os.Getenv("TRUSTED_PROXY_CIDRS"); strings.TrimSpace(v) != "" {
		cidrs = strings.Split(v, ",")
	}
	for _, raw := range cidrs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			*errs = append(*errs, fmt.Errorf("TRUSTED_PROXY_CIDRS entry %q is not a CIDR", raw))
			continue
		}
		cfg.HTTP.TrustedProxyCIDRs = append(cfg.HTTP.TrustedProxyCIDRs, p)
	}

	cfg.RateLimits = RateLimits{PublicReadPerMin: 60, PublicWritePerMin: 5, UserPerMin: 600, MutateHeavyPerMin: 30, SearchPerMin: 120}
	for _, rl := range []struct {
		name string
		dst  *int
	}{
		{"RATE_LIMIT_PUBLIC_READ_PER_MIN", &cfg.RateLimits.PublicReadPerMin},
		{"RATE_LIMIT_PUBLIC_WRITE_PER_MIN", &cfg.RateLimits.PublicWritePerMin},
		{"RATE_LIMIT_USER_PER_MIN", &cfg.RateLimits.UserPerMin},
		{"RATE_LIMIT_MUTATE_HEAVY_PER_MIN", &cfg.RateLimits.MutateHeavyPerMin},
		{"RATE_LIMIT_SEARCH_PER_MIN", &cfg.RateLimits.SearchPerMin},
	} {
		if v := os.Getenv(rl.name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				*errs = append(*errs, fmt.Errorf("%s must be a non-negative integer (0 disables), got %q", rl.name, v))
			} else {
				*rl.dst = n
			}
		}
	}

	cfg.Shutdown = Shutdown{Timeout: 30 * time.Second}
	for _, d := range []struct {
		name string
		dst  *time.Duration
	}{
		{"SHUTDOWN_TIMEOUT", &cfg.Shutdown.Timeout},
		{"SHUTDOWN_DRAIN_DELAY", &cfg.Shutdown.DrainDelay},
	} {
		if v := os.Getenv(d.name); v != "" {
			dur, err := time.ParseDuration(v)
			if err != nil || dur < 0 {
				*errs = append(*errs, fmt.Errorf("%s must be a non-negative Go duration (e.g. 30s), got %q", d.name, v))
			} else {
				*d.dst = dur
			}
		}
	}

	cfg.Log = Log{Format: "json", Level: slog.LevelInfo}
	if v := os.Getenv("LOG_FORMAT"); v != "" {
		switch v {
		case "json", "text":
			cfg.Log.Format = v
		default:
			*errs = append(*errs, fmt.Errorf("LOG_FORMAT must be json or text, got %q", v))
		}
	}
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		switch strings.ToLower(v) {
		case "debug":
			cfg.Log.Level = slog.LevelDebug
		case "info":
			cfg.Log.Level = slog.LevelInfo
		case "warn":
			cfg.Log.Level = slog.LevelWarn
		case "error":
			cfg.Log.Level = slog.LevelError
		default:
			*errs = append(*errs, fmt.Errorf("LOG_LEVEL must be debug|info|warn|error, got %q", v))
		}
	}
}

// RedactURL renders a DSN/URL with its password replaced by *** so it can
// be logged. Anything that does not parse is replaced wholesale — an
// unparseable DSN may still contain a secret.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "[unparseable url]"
	}
	if u.User != nil {
		if _, has := u.User.Password(); has {
			u.User = url.UserPassword(u.User.Username(), "***")
		}
	}
	// url.URL.String percent-encodes '*' in userinfo; un-escape the marker so
	// the log line reads user:***@host.
	return strings.Replace(u.String(), ":%2A%2A%2A@", ":***@", 1)
}

// Summary is the one startup log line: names, booleans and redacted URLs
// only — never a secret value. Passed straight to slog as key/value pairs.
func (c Config) Summary() []any {
	return []any{
		"self_hosted", c.Instance.SelfHosted,
		"instance_name", c.Instance.Name,
		"http_addr", c.HTTP.Addr,
		"database", RedactURL(c.DB.URL),
		"public_web_url", c.Instance.PublicWebURL,
		"public_api_url", c.Instance.PublicAPIURL,
		"jwks_url", c.Auth.JWKSURL,
		"trust_proxy", c.HTTP.TrustProxy,
		"trusted_proxy_cidrs", len(c.HTTP.TrustedProxyCIDRs),
		"allow_dev_origins", c.HTTP.AllowDevOrigins,
		"rate_limit_public_read_per_min", c.RateLimits.PublicReadPerMin,
		"rate_limit_public_write_per_min", c.RateLimits.PublicWritePerMin,
		"rate_limit_user_per_min", c.RateLimits.UserPerMin,
		"rate_limit_mutate_heavy_per_min", c.RateLimits.MutateHeavyPerMin,
		"rate_limit_search_per_min", c.RateLimits.SearchPerMin,
		"shutdown_timeout", c.Shutdown.Timeout.String(),
		"shutdown_drain_delay", c.Shutdown.DrainDelay.String(),
		"log_format", c.Log.Format,
		"google", c.Google.ClientID != "",
		"apple", c.Apple.ClientID != "",
		"microsoft", c.Microsoft.ClientID != "",
		"todoist", c.Todoist.ClientID != "",
		"hubspot", c.HubSpot.ClientID != "",
		"billing", c.Paddle.APIKey != "",
		"paddle_env", c.Paddle.Env,
		"email", c.SMTP.Configured(),
		"ai", c.OpenRouter.APIKey != "",
		"push_apns", c.Push.APNs.KeyP8 != "",
		"push_fcm", c.Push.FCM.ServiceAccountJSON != "",
		"push_webpush", c.Push.VAPID.PublicKey != "" && c.Push.VAPID.PrivateKey != "",
		"weather", c.Weather.BaseURL != "",
		"maps", c.Maps.NominatimBaseURL != "",
	}
}

// parseBool is the boolean grammar every tier shares (the web mirrors it in
// apps/web/lib/auth-env.ts envBool): true/1/yes are true and false/0/no or
// blank are false, case-insensitively and ignoring surrounding spaces.
// Anything else is an error, so SELF_HOSTED=1 means the same thing to the
// api, the worker and the web app.
func parseBool(name, raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes":
		return true, nil
	case "false", "0", "no", "":
		return false, nil
	}
	return false, fmt.Errorf("%s must be true or false (also 1/0, yes/no), got %q", name, raw)
}

// envBool reads a boolean variable with parseBool, appending a parse error
// to errs (FromEnv reports every bad variable at once) and returning false.
func envBool(name string, errs *[]error) bool {
	b, err := parseBool(name, os.Getenv(name))
	if err != nil {
		*errs = append(*errs, err)
	}
	return b
}

// ValidateCloudEmail enforces the cloud startup rule for transactional
// email: with SELF_HOSTED=false an SMTP sender is mandatory because email
// verification, password reset and team invitations all depend on it. Both
// cmd/api and cmd/worker call it right after FromEnv.
func (c Config) ValidateCloudEmail() error {
	if c.Instance.SelfHosted || c.SMTP.Configured() {
		return nil
	}
	return errors.New("SMTP_HOST and SMTP_FROM are required when SELF_HOSTED=false (cloud mode); set them or run with SELF_HOSTED=true")
}

// ValidateCloudBilling enforces the cloud startup rule (docs/payments.md):
// with SELF_HOSTED=false the Paddle credentials are mandatory, so a cloud
// deployment can never boot with a forgeable webhook or no biller. Both
// cmd/api and cmd/worker call it right after FromEnv.
func (c Config) ValidateCloudBilling() error {
	if c.Instance.SelfHosted {
		return nil
	}
	var missing []string
	if c.Paddle.APIKey == "" {
		missing = append(missing, "PADDLE_API_KEY")
	}
	if c.Paddle.WebhookSecret == "" {
		missing = append(missing, "PADDLE_WEBHOOK_SECRET")
	}
	if c.Paddle.AnnualPriceID == "" {
		missing = append(missing, "PADDLE_PRICE_ID_ANNUAL")
	}
	if len(missing) > 0 {
		return fmt.Errorf("cloud mode (SELF_HOSTED=false) requires %s; set them, or run with SELF_HOSTED=true", strings.Join(missing, ", "))
	}
	return nil
}
