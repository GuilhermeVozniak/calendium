package config

import (
	"bytes"
	"encoding/hex"
	"testing"
	"time"
)

// validKeyHex is 64 hex chars => 32 bytes, a valid AES-256-GCM key.
const validKeyHex = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

// configEnvKeys is every environment variable FromEnv reads. clearEnv blanks
// them all so a test starts from a known-empty environment regardless of the
// host/CI env. For FromEnv's os.Getenv("")==unset checks, "" == unset.
var configEnvKeys = []string{
	"DATABASE_URL", "BETTER_AUTH_URL", "AUTH_JWKS_URL", "AUTH_ISSUER",
	"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET",
	"APPLE_CLIENT_ID", "APPLE_CLIENT_SECRET",
	"MS_CLIENT_ID", "MS_CLIENT_SECRET",
	"STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET", "STRIPE_PRICE_ID_ANNUAL",
	"APNS_KEY_ID", "APNS_TEAM_ID", "APNS_KEY_P8",
	"FCM_SERVICE_ACCOUNT_JSON", "VAPID_PUBLIC_KEY", "VAPID_PRIVATE_KEY",
	"OPENROUTER_API_KEY", "OPENROUTER_MODEL", "AI_DAILY_LIMIT",
	"HTTP_ADDR", "PORT", "TOKEN_ENCRYPTION_KEY", "UNDO_SEND_SECONDS",
	"INSTANCE_NAME", "PUBLIC_WEB_URL", "APP_URL", "PUBLIC_API_URL", "APP_BASE_URL",
	"SELF_HOSTED", "OAUTH_ALLOWED_REDIRECT_URIS", "CORS_ALLOWED_ORIGINS",
}

// clearEnv blanks every config env var for the duration of the test; t.Setenv
// restores prior values at test end. Must not be combined with t.Parallel.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range configEnvKeys {
		t.Setenv(k, "")
	}
}

// withBase returns a copy of the two required-valid vars merged with extra, so
// a success-path case can override a single knob and still boot.
func withBase(extra map[string]string) map[string]string {
	m := map[string]string{
		"DATABASE_URL":         "postgres://localhost:5432/calendium",
		"TOKEN_ENCRYPTION_KEY": validKeyHex,
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func containsStr(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}

func TestFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
		check   func(t *testing.T, c Config)
	}{
		// ---- required-var errors ----
		{
			name:    "missing DATABASE_URL errors",
			env:     map[string]string{"TOKEN_ENCRYPTION_KEY": validKeyHex},
			wantErr: true,
		},
		{
			name:    "missing TOKEN_ENCRYPTION_KEY errors",
			env:     map[string]string{"DATABASE_URL": "postgres://localhost/db"},
			wantErr: true,
		},
		{
			name:    "TOKEN_ENCRYPTION_KEY not hex errors",
			env:     withBase(map[string]string{"TOKEN_ENCRYPTION_KEY": "zzzz-not-hex-zzzz"}),
			wantErr: true,
		},
		{
			name: "TOKEN_ENCRYPTION_KEY valid hex but wrong length errors",
			// 32 hex chars = 16 bytes, not 32.
			env:     withBase(map[string]string{"TOKEN_ENCRYPTION_KEY": "00112233445566778899aabbccddeeff"}),
			wantErr: true,
		},
		// ---- crypto happy path ----
		{
			name: "valid key decodes to 32 bytes",
			env:  withBase(nil),
			check: func(t *testing.T, c Config) {
				want, _ := hex.DecodeString(validKeyHex)
				if len(c.Crypto.TokenEncryptionKey) != 32 {
					t.Fatalf("key len = %d, want 32", len(c.Crypto.TokenEncryptionKey))
				}
				if !bytes.Equal(c.Crypto.TokenEncryptionKey, want) {
					t.Fatalf("key bytes = %x, want %x", c.Crypto.TokenEncryptionKey, want)
				}
			},
		},
		// ---- HTTP.Addr ----
		{
			name:  "HTTP_ADDR default :8080",
			env:   withBase(nil),
			check: func(t *testing.T, c Config) { assertEq(t, "HTTP.Addr", c.HTTP.Addr, ":8080") },
		},
		{
			name:  "PORT fallback when HTTP_ADDR unset",
			env:   withBase(map[string]string{"PORT": "9090"}),
			check: func(t *testing.T, c Config) { assertEq(t, "HTTP.Addr", c.HTTP.Addr, ":9090") },
		},
		{
			name:  "HTTP_ADDR wins over PORT",
			env:   withBase(map[string]string{"HTTP_ADDR": "127.0.0.1:1234", "PORT": "9090"}),
			check: func(t *testing.T, c Config) { assertEq(t, "HTTP.Addr", c.HTTP.Addr, "127.0.0.1:1234") },
		},
		// ---- UNDO_SEND_SECONDS ----
		{
			name:  "UNDO_SEND_SECONDS default 15s",
			env:   withBase(nil),
			check: func(t *testing.T, c Config) { assertDur(t, "UndoSendGrace", c.Mail.UndoSendGrace, 15*time.Second) },
		},
		{
			name:  "UNDO_SEND_SECONDS custom",
			env:   withBase(map[string]string{"UNDO_SEND_SECONDS": "30"}),
			check: func(t *testing.T, c Config) { assertDur(t, "UndoSendGrace", c.Mail.UndoSendGrace, 30*time.Second) },
		},
		{
			name:  "UNDO_SEND_SECONDS zero allowed",
			env:   withBase(map[string]string{"UNDO_SEND_SECONDS": "0"}),
			check: func(t *testing.T, c Config) { assertDur(t, "UndoSendGrace", c.Mail.UndoSendGrace, 0) },
		},
		{
			name:    "UNDO_SEND_SECONDS negative errors",
			env:     withBase(map[string]string{"UNDO_SEND_SECONDS": "-1"}),
			wantErr: true,
		},
		{
			name:    "UNDO_SEND_SECONDS non-int errors",
			env:     withBase(map[string]string{"UNDO_SEND_SECONDS": "abc"}),
			wantErr: true,
		},
		// ---- AI_DAILY_LIMIT ----
		{
			name:  "AI_DAILY_LIMIT default 300",
			env:   withBase(nil),
			check: func(t *testing.T, c Config) { assertInt(t, "OpenRouter.DailyLimit", c.OpenRouter.DailyLimit, 300) },
		},
		{
			name:  "AI_DAILY_LIMIT custom",
			env:   withBase(map[string]string{"AI_DAILY_LIMIT": "500"}),
			check: func(t *testing.T, c Config) { assertInt(t, "OpenRouter.DailyLimit", c.OpenRouter.DailyLimit, 500) },
		},
		{
			name:    "AI_DAILY_LIMIT zero errors",
			env:     withBase(map[string]string{"AI_DAILY_LIMIT": "0"}),
			wantErr: true,
		},
		{
			name:    "AI_DAILY_LIMIT negative errors",
			env:     withBase(map[string]string{"AI_DAILY_LIMIT": "-5"}),
			wantErr: true,
		},
		{
			name:    "AI_DAILY_LIMIT non-int errors",
			env:     withBase(map[string]string{"AI_DAILY_LIMIT": "abc"}),
			wantErr: true,
		},
		// ---- SELF_HOSTED ----
		{
			name:  "SELF_HOSTED default false",
			env:   withBase(nil),
			check: func(t *testing.T, c Config) { assertBool(t, "SelfHosted", c.Instance.SelfHosted, false) },
		},
		{
			name:  "SELF_HOSTED true",
			env:   withBase(map[string]string{"SELF_HOSTED": "true"}),
			check: func(t *testing.T, c Config) { assertBool(t, "SelfHosted", c.Instance.SelfHosted, true) },
		},
		{
			name:  "SELF_HOSTED false",
			env:   withBase(map[string]string{"SELF_HOSTED": "false"}),
			check: func(t *testing.T, c Config) { assertBool(t, "SelfHosted", c.Instance.SelfHosted, false) },
		},
		{
			name:    "SELF_HOSTED invalid errors",
			env:     withBase(map[string]string{"SELF_HOSTED": "yes"}),
			wantErr: true,
		},
		// ---- OpenRouter model ----
		{
			name:  "OpenRouter model default",
			env:   withBase(nil),
			check: func(t *testing.T, c Config) { assertEq(t, "OpenRouter.Model", c.OpenRouter.Model, "openrouter/auto") },
		},
		{
			name: "OpenRouter model override",
			env:  withBase(map[string]string{"OPENROUTER_MODEL": "anthropic/claude-sonnet"}),
			check: func(t *testing.T, c Config) {
				assertEq(t, "OpenRouter.Model", c.OpenRouter.Model, "anthropic/claude-sonnet")
			},
		},
		// ---- Auth derivation ----
		{
			name: "JWKS + Issuer derived from BETTER_AUTH_URL",
			env:  withBase(map[string]string{"BETTER_AUTH_URL": "https://app.calendium.com"}),
			check: func(t *testing.T, c Config) {
				assertEq(t, "JWKSURL", c.Auth.JWKSURL, "https://app.calendium.com/api/auth/jwks")
				assertEq(t, "Issuer", c.Auth.Issuer, "https://app.calendium.com")
			},
		},
		{
			name: "BETTER_AUTH_URL trailing slash trimmed before derivation",
			env:  withBase(map[string]string{"BETTER_AUTH_URL": "https://app.calendium.com/"}),
			check: func(t *testing.T, c Config) {
				assertEq(t, "JWKSURL", c.Auth.JWKSURL, "https://app.calendium.com/api/auth/jwks")
				assertEq(t, "Issuer", c.Auth.Issuer, "https://app.calendium.com")
			},
		},
		{
			name: "explicit JWKS + Issuer win over derivation",
			env: withBase(map[string]string{
				"BETTER_AUTH_URL": "https://app.calendium.com",
				"AUTH_JWKS_URL":   "https://auth.example.com/jwks",
				"AUTH_ISSUER":     "https://issuer.example.com",
			}),
			check: func(t *testing.T, c Config) {
				assertEq(t, "JWKSURL", c.Auth.JWKSURL, "https://auth.example.com/jwks")
				assertEq(t, "Issuer", c.Auth.Issuer, "https://issuer.example.com")
			},
		},
		{
			name: "empty BETTER_AUTH_URL leaves Issuer empty and JWKS underived",
			env:  withBase(nil),
			check: func(t *testing.T, c Config) {
				assertEq(t, "JWKSURL", c.Auth.JWKSURL, "")
				assertEq(t, "Issuer", c.Auth.Issuer, "")
			},
		},
		// ---- OAuth redirect allowlist ----
		{
			name: "OAUTH_ALLOWED_REDIRECT_URIS appended to the 4 defaults",
			env:  withBase(map[string]string{"OAUTH_ALLOWED_REDIRECT_URIS": "https://app.example.com, https://web.example.com"}),
			check: func(t *testing.T, c Config) {
				got := c.OAuth.AllowedRedirectURIs
				if len(got) != 6 {
					t.Fatalf("AllowedRedirectURIs len = %d, want 6 (%v)", len(got), got)
				}
				for _, want := range []string{
					"http://localhost", "https://localhost", "http://127.0.0.1", "calendium://",
					"https://app.example.com", "https://web.example.com",
				} {
					if !containsStr(got, want) {
						t.Errorf("AllowedRedirectURIs missing %q (%v)", want, got)
					}
				}
			},
		},
		{
			name: "OAUTH_ALLOWED_REDIRECT_URIS empty leaves only defaults",
			env:  withBase(nil),
			check: func(t *testing.T, c Config) {
				if len(c.OAuth.AllowedRedirectURIs) != 4 {
					t.Fatalf("AllowedRedirectURIs len = %d, want 4 (%v)", len(c.OAuth.AllowedRedirectURIs), c.OAuth.AllowedRedirectURIs)
				}
			},
		},
		// ---- CORS ----
		{
			name: "CORS_ALLOWED_ORIGINS comma-split and trimmed",
			env:  withBase(map[string]string{"CORS_ALLOWED_ORIGINS": "https://a.com, https://b.com ,, https://c.com"}),
			check: func(t *testing.T, c Config) {
				got := c.HTTP.CORSAllowedOrigins
				if len(got) != 3 {
					t.Fatalf("CORSAllowedOrigins = %v, want 3 non-empty entries", got)
				}
				for i, want := range []string{"https://a.com", "https://b.com", "https://c.com"} {
					assertEq(t, "CORSAllowedOrigins["+want+"]", got[i], want)
				}
			},
		},
		{
			name: "CORS_ALLOWED_ORIGINS unset yields nil",
			env:  withBase(nil),
			check: func(t *testing.T, c Config) {
				if c.HTTP.CORSAllowedOrigins != nil {
					t.Fatalf("CORSAllowedOrigins = %v, want nil", c.HTTP.CORSAllowedOrigins)
				}
			},
		},
		// ---- Instance name + public web url ----
		{
			name:  "INSTANCE_NAME default Calendium",
			env:   withBase(nil),
			check: func(t *testing.T, c Config) { assertEq(t, "Instance.Name", c.Instance.Name, "Calendium") },
		},
		{
			name:  "INSTANCE_NAME override",
			env:   withBase(map[string]string{"INSTANCE_NAME": "Acme Mail"}),
			check: func(t *testing.T, c Config) { assertEq(t, "Instance.Name", c.Instance.Name, "Acme Mail") },
		},
		{
			name: "PUBLIC_WEB_URL wins over APP_URL",
			env: withBase(map[string]string{
				"PUBLIC_WEB_URL": "https://web.example.com",
				"APP_URL":        "https://app.example.com",
			}),
			check: func(t *testing.T, c Config) {
				assertEq(t, "PublicWebURL", c.Instance.PublicWebURL, "https://web.example.com")
			},
		},
		{
			name: "APP_URL is the fallback when PUBLIC_WEB_URL unset",
			env:  withBase(map[string]string{"APP_URL": "https://app.example.com"}),
			check: func(t *testing.T, c Config) {
				assertEq(t, "PublicWebURL", c.Instance.PublicWebURL, "https://app.example.com")
			},
		},
		// ---- Invite link base URL ----
		{
			name: "APP_BASE_URL wins over PUBLIC_WEB_URL",
			env: withBase(map[string]string{
				"APP_BASE_URL":   "https://invite.example.com",
				"PUBLIC_WEB_URL": "https://web.example.com",
			}),
			check: func(t *testing.T, c Config) {
				assertEq(t, "AppBaseURL", c.Instance.AppBaseURL, "https://invite.example.com")
			},
		},
		{
			name: "APP_BASE_URL falls back to PUBLIC_WEB_URL",
			env:  withBase(map[string]string{"PUBLIC_WEB_URL": "https://web.example.com"}),
			check: func(t *testing.T, c Config) {
				assertEq(t, "AppBaseURL", c.Instance.AppBaseURL, "https://web.example.com")
			},
		},
		{
			name:  "AppBaseURL empty when neither set",
			env:   withBase(nil),
			check: func(t *testing.T, c Config) { assertEq(t, "AppBaseURL", c.Instance.AppBaseURL, "") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			c, err := FromEnv()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("FromEnv() error = nil, want non-nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("FromEnv() unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, c)
			}
		})
	}
}

// TestFromEnvJoinsMultipleErrors asserts that when several required vars are
// bad at once FromEnv still returns a single non-nil error (errors.Join) and a
// zero-value Config (no partial config leaks out on the error path). We assert
// presence, not the joined string.
func TestFromEnvJoinsMultipleErrors(t *testing.T) {
	clearEnv(t)
	// DATABASE_URL missing AND TOKEN_ENCRYPTION_KEY invalid AND
	// UNDO_SEND_SECONDS non-int => three collected errors.
	t.Setenv("TOKEN_ENCRYPTION_KEY", "nothex")
	t.Setenv("UNDO_SEND_SECONDS", "abc")

	c, err := FromEnv()
	if err == nil {
		t.Fatalf("FromEnv() error = nil, want non-nil (joined)")
	}
	if !bytes.Equal(c.Crypto.TokenEncryptionKey, nil) || c.HTTP.Addr != "" || c.Instance.Name != "" {
		t.Fatalf("FromEnv() returned a non-zero Config on error path: %+v", c)
	}
}

// TestFromEnvIntegrationVendors covers the M2.8 per-user integration OAuth
// apps (Todoist/HubSpot): parsed when set, empty (vendor unwired) otherwise.
func TestFromEnvIntegrationVendors(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("TOKEN_ENCRYPTION_KEY", validKeyHex)
	t.Setenv("TODOIST_CLIENT_ID", "td-id")
	t.Setenv("TODOIST_CLIENT_SECRET", "td-secret")
	t.Setenv("HUBSPOT_CLIENT_ID", "hs-id")
	t.Setenv("HUBSPOT_CLIENT_SECRET", "hs-secret")

	c, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	assertEq(t, "Todoist.ClientID", c.Todoist.ClientID, "td-id")
	assertEq(t, "Todoist.ClientSecret", c.Todoist.ClientSecret, "td-secret")
	assertEq(t, "HubSpot.ClientID", c.HubSpot.ClientID, "hs-id")
	assertEq(t, "HubSpot.ClientSecret", c.HubSpot.ClientSecret, "hs-secret")
}

func TestFromEnvIntegrationVendorsDefaultEmpty(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("TOKEN_ENCRYPTION_KEY", validKeyHex)

	c, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if c.Todoist != (Todoist{}) || c.HubSpot != (HubSpot{}) {
		t.Fatalf("vendors must default to unwired: todoist=%+v hubspot=%+v", c.Todoist, c.HubSpot)
	}
}

func assertEq(t *testing.T, field, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", field, got, want)
	}
}

func assertDur(t *testing.T, field string, got, want time.Duration) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", field, got, want)
	}
}

func assertBool(t *testing.T, field string, got, want bool) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", field, got, want)
	}
}

func assertInt(t *testing.T, field string, got, want int) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %d, want %d", field, got, want)
	}
}
