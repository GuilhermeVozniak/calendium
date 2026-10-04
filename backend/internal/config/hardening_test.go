package config

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func setBaseEnv(t *testing.T, extra map[string]string) {
	t.Helper()
	clearEnv(t)
	for k, v := range withBase(extra) {
		t.Setenv(k, v)
	}
}

func TestDefaultPasswordRefusedInCloudWarnedOnSelfHost(t *testing.T) {
	const msg = "DATABASE_URL uses a default password; set POSTGRES_PASSWORD"
	for _, pw := range []string{"change-me-please", "calendium"} {
		t.Run(pw+" cloud", func(t *testing.T) {
			setBaseEnv(t, map[string]string{
				"DATABASE_URL":   "postgres://calendium:" + pw + "@db:5432/calendium?sslmode=disable",
				"SELF_HOSTED":    "false",
				"PUBLIC_WEB_URL": "https://app.example.com",
			})
			_, _, err := FromEnv()
			if err == nil || !strings.Contains(err.Error(), msg) {
				t.Fatalf("err = %v, want the default-password error", err)
			}
		})
		t.Run(pw+" self-host", func(t *testing.T) {
			setBaseEnv(t, map[string]string{
				"DATABASE_URL": "postgres://calendium:" + pw + "@db:5432/calendium?sslmode=disable",
				"SELF_HOSTED":  "true",
			})
			_, warnings, err := FromEnv()
			if err != nil {
				t.Fatalf("self-host must boot: %v", err)
			}
			if !containsStr(warnings, msg) {
				t.Fatalf("warnings = %v", warnings)
			}
		})
	}
	t.Run("strong password is silent", func(t *testing.T) {
		setBaseEnv(t, map[string]string{
			"DATABASE_URL":   "postgres://calendium:s3cr3t-long-value@db:5432/calendium",
			"SELF_HOSTED":    "true",
			"PUBLIC_WEB_URL": "https://app.example.com",
		})
		_, warnings, err := FromEnv()
		if err != nil || len(warnings) != 0 {
			t.Fatalf("err=%v warnings=%v", err, warnings)
		}
	})
	t.Run("strong password boots in cloud", func(t *testing.T) {
		setBaseEnv(t, map[string]string{
			"DATABASE_URL":   "postgres://calendium:s3cr3t-long-value@db:5432/calendium",
			"SELF_HOSTED":    "false",
			"PUBLIC_WEB_URL": "https://app.example.com",
		})
		if _, warnings, err := FromEnv(); err != nil || len(warnings) != 0 {
			t.Fatalf("err=%v warnings=%v", err, warnings)
		}
	})
}

func TestPublicWebURLRules(t *testing.T) {
	t.Run("cloud without PUBLIC_WEB_URL errors", func(t *testing.T) {
		setBaseEnv(t, map[string]string{"SELF_HOSTED": "false"})
		if _, _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), "PUBLIC_WEB_URL") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("APP_URL satisfies the rule", func(t *testing.T) {
		setBaseEnv(t, map[string]string{"SELF_HOSTED": "false", "APP_URL": "https://app.example.com"})
		if _, _, err := FromEnv(RequirePublicWebURL()); err != nil {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("self-host without PUBLIC_WEB_URL warns", func(t *testing.T) {
		setBaseEnv(t, map[string]string{"SELF_HOSTED": "true"})
		_, warnings, err := FromEnv()
		if err != nil || !containsStr(warnings, "PUBLIC_WEB_URL (or APP_URL) is not set; emailed links and GET /v1/instance need it") {
			t.Fatalf("err=%v warnings=%v", err, warnings)
		}
	})
	t.Run("RequirePublicWebURL errors in both modes", func(t *testing.T) {
		for _, sh := range []string{"true", "false"} {
			setBaseEnv(t, map[string]string{"SELF_HOSTED": sh})
			if _, _, err := FromEnv(RequirePublicWebURL()); err == nil || !strings.Contains(err.Error(), "PUBLIC_WEB_URL (or APP_URL) is required") {
				t.Fatalf("SELF_HOSTED=%s err = %v", sh, err)
			}
		}
	})
}

func TestProxyTrustParsing(t *testing.T) {
	t.Run("defaults off with the default CIDR list", func(t *testing.T) {
		setBaseEnv(t, map[string]string{"SELF_HOSTED": "true"})
		c, _, err := FromEnv()
		if err != nil {
			t.Fatal(err)
		}
		assertBool(t, "TrustProxy", c.HTTP.TrustProxy, false)
		got := make([]string, 0, len(c.HTTP.TrustedProxyCIDRs))
		for _, p := range c.HTTP.TrustedProxyCIDRs {
			got = append(got, p.String())
		}
		// Must match the web tier's default exactly (apps/web TRUSTED_PROXY_CIDRS).
		assertEq(t, "TrustedProxyCIDRs", strings.Join(got, ","),
			"127.0.0.0/8,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,::1/128,fc00::/7")
	})
	t.Run("explicit CIDRs", func(t *testing.T) {
		setBaseEnv(t, map[string]string{"SELF_HOSTED": "true", "TRUST_PROXY": "true", "TRUSTED_PROXY_CIDRS": " 172.18.0.0/16 , ::1/128 "})
		c, _, err := FromEnv()
		if err != nil {
			t.Fatal(err)
		}
		assertBool(t, "TrustProxy", c.HTTP.TrustProxy, true)
		if len(c.HTTP.TrustedProxyCIDRs) != 2 || c.HTTP.TrustedProxyCIDRs[0].String() != "172.18.0.0/16" {
			t.Fatalf("CIDRs = %v", c.HTTP.TrustedProxyCIDRs)
		}
	})
	t.Run("TRUST_PROXY shares the boolean grammar", func(t *testing.T) {
		for v, want := range map[string]bool{"1": true, "YES": true, "no": false, "0": false} {
			setBaseEnv(t, map[string]string{"SELF_HOSTED": "true", "TRUST_PROXY": v})
			c, _, err := FromEnv()
			if err != nil {
				t.Fatal(err)
			}
			assertBool(t, "TrustProxy="+v, c.HTTP.TrustProxy, want)
		}
	})
	t.Run("bad CIDR is a boot error", func(t *testing.T) {
		setBaseEnv(t, map[string]string{"SELF_HOSTED": "true", "TRUSTED_PROXY_CIDRS": "10.0.0.0/8,not-a-cidr"})
		if _, _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXY_CIDRS") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("bad TRUST_PROXY is a boot error", func(t *testing.T) {
		setBaseEnv(t, map[string]string{"SELF_HOSTED": "true", "TRUST_PROXY": "yes-please"})
		if _, _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), "TRUST_PROXY") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestRateLimitShutdownLogParsing(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		setBaseEnv(t, map[string]string{"SELF_HOSTED": "true"})
		c, _, err := FromEnv()
		if err != nil {
			t.Fatal(err)
		}
		if c.RateLimits != (RateLimits{PublicReadPerMin: 60, PublicWritePerMin: 5, UserPerMin: 600, MutateHeavyPerMin: 30, SearchPerMin: 120}) {
			t.Fatalf("RateLimits = %+v", c.RateLimits)
		}
		assertDur(t, "Shutdown.Timeout", c.Shutdown.Timeout, 30*time.Second)
		assertDur(t, "Shutdown.DrainDelay", c.Shutdown.DrainDelay, 0)
		assertEq(t, "Log.Format", c.Log.Format, "json")
		if c.Log.Level != slog.LevelInfo {
			t.Fatalf("Log.Level = %v", c.Log.Level)
		}
	})
	t.Run("overrides", func(t *testing.T) {
		setBaseEnv(t, map[string]string{
			"SELF_HOSTED":             "true",
			"RATE_LIMIT_USER_PER_MIN": "0", "RATE_LIMIT_SEARCH_PER_MIN": "7",
			"SHUTDOWN_TIMEOUT": "45s", "SHUTDOWN_DRAIN_DELAY": "2s", "LOG_FORMAT": "text", "LOG_LEVEL": "debug",
		})
		c, _, err := FromEnv()
		if err != nil {
			t.Fatal(err)
		}
		assertInt(t, "UserPerMin", c.RateLimits.UserPerMin, 0)
		assertInt(t, "SearchPerMin", c.RateLimits.SearchPerMin, 7)
		assertDur(t, "Shutdown.Timeout", c.Shutdown.Timeout, 45*time.Second)
		assertDur(t, "Shutdown.DrainDelay", c.Shutdown.DrainDelay, 2*time.Second)
		assertEq(t, "Log.Format", c.Log.Format, "text")
		if c.Log.Level != slog.LevelDebug {
			t.Fatalf("Log.Level = %v", c.Log.Level)
		}
	})
	for name, env := range map[string]map[string]string{
		"negative rate limit":  {"RATE_LIMIT_PUBLIC_READ_PER_MIN": "-1"},
		"non-integer rate":     {"RATE_LIMIT_MUTATE_HEAVY_PER_MIN": "lots"},
		"bad shutdown timeout": {"SHUTDOWN_TIMEOUT": "soon"},
		"negative drain delay": {"SHUTDOWN_DRAIN_DELAY": "-1s"},
		"bad log format":       {"LOG_FORMAT": "xml"},
		"bad log level":        {"LOG_LEVEL": "loud"},
	} {
		t.Run(name+" errors", func(t *testing.T) {
			env["SELF_HOSTED"] = "true"
			setBaseEnv(t, env)
			if _, _, err := FromEnv(); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestRedactURL(t *testing.T) {
	cases := map[string]string{
		"postgres://calendium:hunter2@db:5432/calendium?sslmode=disable": "postgres://calendium:***@db:5432/calendium?sslmode=disable",
		"postgres://calendium@db:5432/calendium":                         "postgres://calendium@db:5432/calendium",
		"postgres://db:5432/calendium":                                   "postgres://db:5432/calendium",
		"://not a url":                                                   "[unparseable url]",
	}
	for in, want := range cases {
		if got := RedactURL(in); got != want {
			t.Errorf("RedactURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSummaryNeverContainsSecrets sets every secret to a unique marker and
// proves the startup summary carries none of them.
func TestSummaryNeverContainsSecrets(t *testing.T) {
	secrets := map[string]string{
		"DATABASE_URL": "postgres://calendium:SECRET-dbpw@db:5432/calendium", "TOKEN_ENCRYPTION_KEY": validKeyHex,
		"GOOGLE_CLIENT_SECRET": "SECRET-google", "APPLE_CLIENT_SECRET": "SECRET-apple", "MS_CLIENT_SECRET": "SECRET-ms",
		"TODOIST_CLIENT_SECRET": "SECRET-todoist", "HUBSPOT_CLIENT_SECRET": "SECRET-hubspot",
		"PADDLE_API_KEY": "SECRET-paddle", "PADDLE_WEBHOOK_SECRET": "SECRET-pdlwh", "PADDLE_PRICE_ID_ANNUAL": "pri_1",
		"SMTP_HOST": "smtp.example.test", "SMTP_FROM": "noreply@example.test", "SMTP_USER": "u", "SMTP_PASS": "SECRET-smtp",
		"APNS_KEY_P8": "SECRET-apns", "FCM_SERVICE_ACCOUNT_JSON": "SECRET-fcm", "VAPID_PRIVATE_KEY": "SECRET-vapid",
		"OPENROUTER_API_KEY": "SECRET-openrouter", "SELF_HOSTED": "true",
		"GOOGLE_CLIENT_ID": "gid", "APPLE_CLIENT_ID": "aid", "MS_CLIENT_ID": "mid", "TODOIST_CLIENT_ID": "tid", "HUBSPOT_CLIENT_ID": "hid",
	}
	setBaseEnv(t, secrets)
	c, _, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	NewLogger(&buf, c.Log).Info("api: config", c.Summary()...)
	line := buf.String()
	for k, v := range secrets {
		if strings.Contains(v, "SECRET-") && strings.Contains(line, v) {
			t.Errorf("summary leaks %s: %s", k, line)
		}
	}
	if strings.Contains(line, validKeyHex) {
		t.Errorf("summary leaks TOKEN_ENCRYPTION_KEY: %s", line)
	}
	if !strings.Contains(line, "calendium:***@db") {
		t.Errorf("summary should carry the redacted DSN: %s", line)
	}
}

func TestNewLogger(t *testing.T) {
	var buf bytes.Buffer
	NewLogger(&buf, Log{Format: "json", Level: slog.LevelWarn}).Info("hidden")
	NewLogger(&buf, Log{Format: "json", Level: slog.LevelWarn}).Warn("shown", "k", "v")
	if strings.Contains(buf.String(), "hidden") || !strings.HasPrefix(buf.String(), `{"time"`) {
		t.Fatalf("json logger output = %q", buf.String())
	}
	buf.Reset()
	NewLogger(&buf, Log{Format: "text", Level: slog.LevelInfo}).Info("shown")
	if !strings.HasPrefix(buf.String(), "time=") {
		t.Fatalf("text logger output = %q", buf.String())
	}
}
