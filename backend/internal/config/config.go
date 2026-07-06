// Package config loads backend configuration from the environment
// (docs/architecture.md "Environment"; see backend/.env.example).
package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// HTTP configures the API listener.
type HTTP struct {
	// Addr is the listen address (HTTP_ADDR, or ":$PORT"; default ":8080").
	Addr string
}

// DB configures Postgres.
type DB struct {
	URL string // DATABASE_URL
}

// Supabase configures local JWT verification. At least one of JWTSecret
// (HS256) or JWKSURL (RS256/ES256) must be set for authenticated routes.
type Supabase struct {
	JWTSecret string // SUPABASE_JWT_SECRET
	JWKSURL   string // SUPABASE_JWKS_URL
	// URL is the Supabase project URL (SUPABASE_URL); the expected token
	// issuer is derived from it as <URL>/auth/v1.
	URL string
	// AnonKey is the PUBLIC Supabase anon key (SUPABASE_ANON_KEY). Unlike
	// JWTSecret it is safe to serve to clients (GET /v1/instance) so they can
	// self-configure; it is distinct from the secret JWTSecret.
	AnonKey string
}

// Issuer returns the expected JWT `iss` claim (<URL>/auth/v1), or "" when no
// project URL is configured (issuer pinning is then disabled).
func (s Supabase) Issuer() string {
	if s.URL == "" {
		return ""
	}
	return strings.TrimRight(s.URL, "/") + "/auth/v1"
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

// Stripe configures billing (docs/payments.md).
type Stripe struct {
	SecretKey     string // STRIPE_SECRET_KEY
	WebhookSecret string // STRIPE_WEBHOOK_SECRET
	AnnualPriceID string // STRIPE_PRICE_ID_ANNUAL
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

// Instance describes the deployment mode surfaced to clients via the public
// GET /v1/instance discovery endpoint (open-core: self-hosted vs. cloud).
type Instance struct {
	// SelfHosted (SELF_HOSTED, default false) marks a free self-hosted
	// deployment: entitlement gating becomes a no-op (all features unlocked)
	// and the billing endpoints return domain.ErrSelfHosted instead of
	// calling Stripe.
	SelfHosted bool
	// Name (INSTANCE_NAME, default "Calendium") is shown to clients.
	Name string
	// PublicWebURL (APP_URL, falling back to PUBLIC_WEB_URL) is the public web
	// origin used to build absolute links; optional.
	PublicWebURL string
}

// Config is the full backend configuration.
type Config struct {
	HTTP       HTTP
	DB         DB
	Supabase   Supabase
	Google     Google
	Microsoft  Microsoft
	Stripe     Stripe
	Push       Push
	OpenRouter OpenRouter
	Crypto     Crypto
	Mail       Mail
	OAuth      OAuth
	Instance   Instance
}

// FromEnv builds a Config from environment variables. DATABASE_URL and a
// valid TOKEN_ENCRYPTION_KEY are required; everything else is optional so
// partial deployments (e.g. no push) still boot, with the affected adapters
// left unwired by the composition root.
func FromEnv() (Config, error) {
	var errs []error

	cfg := Config{
		DB: DB{URL: os.Getenv("DATABASE_URL")},
		Supabase: Supabase{
			JWTSecret: os.Getenv("SUPABASE_JWT_SECRET"),
			JWKSURL:   os.Getenv("SUPABASE_JWKS_URL"),
			URL:       os.Getenv("SUPABASE_URL"),
			AnonKey:   os.Getenv("SUPABASE_ANON_KEY"),
		},
		Google: Google{
			ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
			ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		},
		Microsoft: Microsoft{
			ClientID:     os.Getenv("MS_CLIENT_ID"),
			ClientSecret: os.Getenv("MS_CLIENT_SECRET"),
		},
		Stripe: Stripe{
			SecretKey:     os.Getenv("STRIPE_SECRET_KEY"),
			WebhookSecret: os.Getenv("STRIPE_WEBHOOK_SECRET"),
			AnnualPriceID: os.Getenv("STRIPE_PRICE_ID_ANNUAL"),
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

	if cfg.DB.URL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
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

	cfg.Mail.UndoSendGrace = 15 * time.Second
	if v := os.Getenv("UNDO_SEND_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			errs = append(errs, fmt.Errorf("UNDO_SEND_SECONDS must be a non-negative integer, got %q", v))
		} else {
			cfg.Mail.UndoSendGrace = time.Duration(n) * time.Second
		}
	}

	cfg.Instance.Name = os.Getenv("INSTANCE_NAME")
	if cfg.Instance.Name == "" {
		cfg.Instance.Name = "Calendium"
	}
	cfg.Instance.PublicWebURL = os.Getenv("APP_URL")
	if cfg.Instance.PublicWebURL == "" {
		cfg.Instance.PublicWebURL = os.Getenv("PUBLIC_WEB_URL")
	}
	if v := os.Getenv("SELF_HOSTED"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("SELF_HOSTED must be true or false, got %q", v))
		} else {
			cfg.Instance.SelfHosted = b
		}
	}

	cfg.OAuth.AllowedRedirectURIs = append([]string(nil), defaultRedirectAllowlist...)
	if v := os.Getenv("OAUTH_ALLOWED_REDIRECT_URIS"); v != "" {
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				cfg.OAuth.AllowedRedirectURIs = append(cfg.OAuth.AllowedRedirectURIs, p)
			}
		}
	}

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	return cfg, nil
}
