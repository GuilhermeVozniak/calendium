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
	// The same rule for every DSN shape pgx accepts: a ?password= query
	// parameter and libpq keyword/value strings (plain and quoted).
	for _, dsn := range []string{
		"postgres://calendium@db:5432/calendium?sslmode=disable&password=calendium",
		"postgres://db/calendium?PASSWORD=change-me-please",
		"host=db user=calendium password=calendium dbname=calendium",
		"host=db user=calendium password='change-me-please' dbname=calendium",
	} {
		t.Run(dsn+" cloud", func(t *testing.T) {
			setBaseEnv(t, map[string]string{"DATABASE_URL": dsn, "SELF_HOSTED": "false", "PUBLIC_WEB_URL": "https://app.example.com"})
			if _, _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), msg) {
				t.Fatalf("err = %v, want the default-password error", err)
			}
		})
		t.Run(dsn+" self-host", func(t *testing.T) {
			setBaseEnv(t, map[string]string{"DATABASE_URL": dsn, "SELF_HOSTED": "true", "PUBLIC_WEB_URL": "https://app.example.com"})
			_, warnings, err := FromEnv()
			if err != nil || !containsStr(warnings, msg) {
				t.Fatalf("err=%v warnings=%v", err, warnings)
			}
		})
	}
	t.Run("strong keyword-DSN password boots in cloud", func(t *testing.T) {
		setBaseEnv(t, map[string]string{
			"DATABASE_URL":   "host=db user=calendium password='s3cr3t long' dbname=calendium",
			"SELF_HOSTED":    "false",
			"PUBLIC_WEB_URL": "https://app.example.com",
		})
		if _, warnings, err := FromEnv(); err != nil || len(warnings) != 0 {
			t.Fatalf("err=%v warnings=%v", err, warnings)
		}
	})
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
	t.Run("blank entries only means defaults", func(t *testing.T) {
		for _, v := range []string{",", " , ,", "  "} {
			setBaseEnv(t, map[string]string{"SELF_HOSTED": "true", "TRUSTED_PROXY_CIDRS": v})
			c, _, err := FromEnv()
			if err != nil {
				t.Fatal(err)
			}
			if len(c.HTTP.TrustedProxyCIDRs) != len(DefaultTrustedProxyCIDRs) {
				t.Fatalf("TRUSTED_PROXY_CIDRS=%q → %v, want the defaults", v, c.HTTP.TrustedProxyCIDRs)
			}
		}
	})
	t.Run("bad TRUST_PROXY is a boot error", func(t *testing.T) {
		setBaseEnv(t, map[string]string{"SELF_HOSTED": "true", "TRUST_PROXY": "yes-please"})
		if _, _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), "TRUST_PROXY") {
			t.Fatalf("err = %v", err)
		}
	})
}

// TestParseTrustedProxyCIDRs mirrors apps/web/lib/client-ip.ts
// proxyTrustFromEnv entry for entry: a bare IP is a single host (/32 or
// /128), blank entries are ignored, whitespace is trimmed, IPv4-mapped
// addresses are unmapped, a zone is dropped, and anything else refuses to
// boot.
func TestParseTrustedProxyCIDRs(t *testing.T) {
	valid := []struct{ in, want string }{
		{"", strings.Join(DefaultTrustedProxyCIDRs, ",")},
		{" , ,", strings.Join(DefaultTrustedProxyCIDRs, ",")},
		{" 203.0.113.0/24 , ::1/128 ", "203.0.113.0/24,::1/128"},
		{"172.18.0.5", "172.18.0.5/32"},
		{"2001:db8::7", "2001:db8::7/128"},
		{"10.0.0.0/8,,172.18.0.5", "10.0.0.0/8,172.18.0.5/32"},
		{"10.1.2.3/8", "10.0.0.0/8"},
		{"::ffff:10.0.0.1", "10.0.0.1/32"},
		{"::FFFF:10.0.0.0/8", "10.0.0.0/8"},
		{"FE80::1%eth0", "fe80::1/128"},
		{"10.0.0.0/08", "10.0.0.0/8"},
		{"0.0.0.0/0", "0.0.0.0/0"},
	}
	for _, tc := range valid {
		ps, err := ParseTrustedProxyCIDRs(tc.in)
		if err != nil {
			t.Errorf("ParseTrustedProxyCIDRs(%q) error: %v", tc.in, err)
			continue
		}
		got := make([]string, 0, len(ps))
		for _, p := range ps {
			got = append(got, p.String())
		}
		if strings.Join(got, ",") != tc.want {
			t.Errorf("ParseTrustedProxyCIDRs(%q) = %v, want %s", tc.in, got, tc.want)
		}
	}
	for _, in := range []string{
		"not-a-cidr", "10.0.0.0/8,not-a-cidr", "10.0.0.0/33", "::1/129", "::ffff:10.0.0.0/104",
		"10.0.0.0/", "10.0.0.0/-1", "10.0.0.0/+8", "10.0.0.0/8/8", "/8", "10.0.0.0/ 8", "host.example",
	} {
		if _, err := ParseTrustedProxyCIDRs(in); err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXY_CIDRS") {
			t.Errorf("ParseTrustedProxyCIDRs(%q) err = %v, want a TRUSTED_PROXY_CIDRS error", in, err)
		}
	}
}

func TestRateLimitsZeroAndUnset(t *testing.T) {
	all := map[string]string{
		"RATE_LIMIT_PUBLIC_READ_PER_MIN": "0", "RATE_LIMIT_PUBLIC_WRITE_PER_MIN": "0", "RATE_LIMIT_USER_PER_MIN": "0",
		"RATE_LIMIT_MUTATE_HEAVY_PER_MIN": "0", "RATE_LIMIT_SEARCH_PER_MIN": "0", "SELF_HOSTED": "true",
	}
	t.Run("all five 0 disables API rate limiting", func(t *testing.T) {
		setBaseEnv(t, all)
		c, _, err := FromEnv()
		if err != nil {
			t.Fatal(err)
		}
		if c.RateLimits != (RateLimits{}) || !c.RateLimits.Disabled() {
			t.Fatalf("RateLimits = %+v, want every class disabled", c.RateLimits)
		}
	})
	t.Run("one class 0 disables only that class", func(t *testing.T) {
		setBaseEnv(t, map[string]string{"SELF_HOSTED": "true", "RATE_LIMIT_PUBLIC_WRITE_PER_MIN": "0"})
		c, _, err := FromEnv()
		if err != nil {
			t.Fatal(err)
		}
		if c.RateLimits != (RateLimits{PublicReadPerMin: 60, UserPerMin: 600, MutateHeavyPerMin: 30, SearchPerMin: 120}) || c.RateLimits.Disabled() {
			t.Fatalf("RateLimits = %+v", c.RateLimits)
		}
	})
	t.Run("unset or blank means default", func(t *testing.T) {
		setBaseEnv(t, map[string]string{"SELF_HOSTED": "true", "RATE_LIMIT_USER_PER_MIN": ""})
		c, _, err := FromEnv()
		if err != nil {
			t.Fatal(err)
		}
		if c.RateLimits != DefaultRateLimits() {
			t.Fatalf("RateLimits = %+v, want the defaults", c.RateLimits)
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
	cases := []struct{ in, want string }{
		{"postgres://calendium:hunter2@db:5432/calendium?sslmode=disable", "postgres://calendium:***@db:5432/calendium?sslmode=disable"},
		{"postgres://calendium@db:5432/calendium", "postgres://calendium@db:5432/calendium"},
		{"postgres://db:5432/calendium", "postgres://db:5432/calendium"},
		{"://not a url", "[unparseable url]"},
		{"", ""},
		// password= query parameter (any case, any position; order kept).
		{"postgres://db:5432/calendium?password=hunter2", "postgres://db:5432/calendium?password=***"},
		{"postgresql://calendium@db/calendium?sslmode=disable&PassWord=hunter2&application_name=api", "postgresql://calendium@db/calendium?sslmode=disable&PassWord=***&application_name=api"},
		{"postgres://u:hunter2@db/c?password=hunter2", "postgres://u:***@db/c?password=***"},
		{"postgres://db/c?sslpassword=hunter2", "postgres://db/c?sslpassword=***"},
		{"postgres://db/c?pass%77ord=hunter2", "postgres://db/c?pass%77ord=***"},
		// libpq keyword/value DSNs, plain and quoted values.
		{"host=db user=calendium password=hunter2 dbname=calendium", "host=db user=calendium password=*** dbname=calendium"},
		{"host=db password = hunter2 sslmode=disable", "host=db password = *** sslmode=disable"},
		{"host=db password='hunter2 with spaces' dbname=calendium", "host=db password=*** dbname=calendium"},
		{`host=db password='it\'s hunter2' dbname=calendium`, "host=db password=*** dbname=calendium"},
		{`host=db PASSWORD=hunter2\ x dbname=c`, "host=db PASSWORD=*** dbname=c"},
		{"password=hunter2", "password=***"},
		{"host=db user=calendium dbname=calendium", "host=db user=calendium dbname=calendium"},
		// Anything that does not tokenise may hide a secret: placeholder.
		{"host=db password='hunter2", "[unparseable dsn]"},
		{"hunter2", "[unparseable dsn]"},
		{"host=db hunter2", "[unparseable dsn]"},
	}
	for _, tc := range cases {
		got := RedactURL(tc.in)
		if got != tc.want {
			t.Errorf("RedactURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if strings.Contains(got, "hunter2") {
			t.Errorf("RedactURL(%q) leaks the secret: %q", tc.in, got)
		}
	}
}

func TestLoggableURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"https://app.example.com", "https://app.example.com"},
		{"https://admin:hunter2@app.example.com/base", "https://app.example.com/base"},
		{"https://auth.example.com/api/auth/jwks?token=hunter2#frag", "https://auth.example.com/api/auth/jwks"},
		{"https://hunter2@api.example.com?", "https://api.example.com"},
		{"://hunter2 not a url", "[unparseable url]"},
	}
	for _, tc := range cases {
		got := loggableURL(tc.in)
		if got != tc.want {
			t.Errorf("loggableURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if strings.Contains(got, "hunter2") {
			t.Errorf("loggableURL(%q) leaks the secret: %q", tc.in, got)
		}
	}
}

// TestSummaryRedactsEveryDSNAndURLForm proves no password reaches the boot
// summary whatever shape DATABASE_URL takes, and that the public URLs lose
// their userinfo and query.
func TestSummaryRedactsEveryDSNAndURLForm(t *testing.T) {
	for _, dsn := range []string{
		"postgres://calendium:hunter2@db:5432/calendium",
		"postgres://db:5432/calendium?password=hunter2",
		"host=db user=calendium password=hunter2 dbname=calendium",
		"host=db password='hunter2 x' dbname=calendium",
	} {
		c := Config{}
		c.DB.URL = dsn
		c.Instance.PublicWebURL = "https://u:hunter2@app.example.com"
		c.Instance.PublicAPIURL = "https://api.example.com/?key=hunter2"
		c.Auth.JWKSURL = "https://auth.example.com/jwks?access_token=hunter2"
		var buf bytes.Buffer
		NewLogger(&buf, Log{Format: "json", Level: slog.LevelInfo}).Info("api: config", c.Summary()...)
		if strings.Contains(buf.String(), "hunter2") {
			t.Errorf("summary leaks the secret for %q: %s", dsn, buf.String())
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
