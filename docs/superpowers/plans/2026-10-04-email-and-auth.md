# Email and Auth Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the identity layer launch-ready: SMTP transactional email on the web tier (nodemailer) and the Go tier (stdlib `net/smtp`), email verification required whenever SMTP is configured, self-service password reset and change, enumeration-safe sign-up, durable per-IP Better Auth rate limits in Postgres, JWT reuse with one 401 retry in the shared API client, and a trusted-origin list that gates localhost origins in production — while self-host without SMTP, demo mode and the Playwright suite keep working.

**Architecture:** The web app owns identity (Better Auth 1.6.23 at `/api/auth/*`): pure helpers in `apps/web/lib/auth-env.ts` and `apps/web/lib/email/*` feed `lib/auth.ts` (verification, reset, rate limiting over a new `rateLimit` table, password-policy `hooks.before`, trusted origins) and the catch-all route stamps a server-set client-IP header. The Go hexagon gains a `port.Mailer` implemented by a stdlib `adapter/out/smtp` package; `service.TeamService` falls back mailbox → SMTP → copyable link; `config` parses `SMTP_*`/`ALLOW_DEV_ORIGINS`, the CORS middleware gates localhost on `ALLOW_DEV_ORIGINS`, and `GET /v1/instance` advertises `features.email`. `packages/shared` adds a JWT cache (`createAccessTokenCache`) wired into `ApiClient` (401 → re-mint once); web pages add forgot/reset/verify-email and Settings → Account; desktop and mobile detect verification-required sign-ups and link to `<webUrl>/forgot-password`.

**Tech Stack:** Go 1.26 stdlib (`net/smtp`, `crypto/tls`, `mime/*`, `net/mail`, `html/template`, `text/template`), SQL migrations in `backend/migrations`, Next.js 15 + React 19 + Better Auth 1.6.23 + `nodemailer`, `packages/shared` TS contract (Vitest node), Expo SDK 54 (jest-expo + @testing-library/react-native), Wails/Vite React (Vitest), Playwright demo-mode e2e, Biome, golangci-lint.

**Spec:** docs/superpowers/specs/2026-10-04-email-and-auth-design.md

> **Migration number:** the spec names `0027_better_auth_rate_limit.sql`; piece 1 (Paddle billing) owns `0027`, so this plan uses **`backend/migrations/0028_better_auth_rate_limit.sql`** (provisional — renumber only if another piece lands between).
>
> **Dependencies on other pieces:** piece 1 adds `webUrl` to `GET /v1/instance`, `InstanceInfo.webUrl: string` in `packages/shared/src/types.ts`, and plumbs `webUrl` into the desktop and mobile `ServerConfig` (its Tasks 19–20). This plan CONSUMES those (never defines them); Tasks 17 and 18 start with a grep that confirms the field exists. Piece 1 also introduced `Config.ValidateCloudBilling()` + `withBase` in `config_test.go`; Task 1 adds the sibling `ValidateCloudEmail()` the same way. Piece 4 defines `TRUST_PROXY` for the Go tier; this plan reads the same env name on the web tier only.
>
> **Known gap vs. the spec (do not silently resolve):** no client (web, desktop or mobile) currently has a team-invitation UI — `ApiClient.invite()` has no caller. The spec's "web invite form shows a copyable link" therefore has no target file; this plan ships the contract (`delivery`/`inviteUrl` in Go and TS) and stops there. Task 21 verifies the contract through the API and records the gap in its report.

## Global Constraints

- Env (web, api, worker): `SMTP_HOST`, `SMTP_PORT` (default `587`, integer 1–65535), `SMTP_USER`/`SMTP_PASS` (optional pair; one without the other errors), `SMTP_FROM` (`addr` or `Name <addr>`), `SMTP_SECURE` (`true` = implicit TLS, `false` = STARTTLS, default `false`); `SMTP_HOST` and `SMTP_FROM` required together whenever `SMTP_HOST`/`SMTP_FROM`/`SMTP_USER`/`SMTP_PASS` is set, and always when `SELF_HOSTED=false`.
- Startup errors, verbatim: `SMTP_HOST and SMTP_FROM are required when SELF_HOSTED=false (cloud mode); set them or run with SELF_HOSTED=true` and `SMTP_* is partially configured: <missing>`; self-host without SMTP logs `email: disabled (no SMTP_HOST); verification off, invitations fall back to links`.
- `SELF_HOSTED` is read by web (new), api and worker; `TRUST_PROXY` (default `false`) by web; `ALLOW_DEV_ORIGINS` (default `false`) by web and api; `PUBLIC_WEB_URL` by web (new); production + `TRUST_PROXY=false` and production + `ALLOW_DEV_ORIGINS=true` each log one startup warning.
- Better Auth options (exact): `emailAndPassword: { enabled: true, minPasswordLength: 10, maxPasswordLength: 128, requireEmailVerification: mail.configured, resetPasswordTokenExpiresIn: 3600, revokeSessionsOnPasswordReset: true, sendResetPassword }`; `emailVerification: { sendOnSignUp: true, sendOnSignIn: true, autoSignInAfterVerification: true, expiresIn: 86_400, sendVerificationEmail }` only when SMTP is configured; `rateLimit: { enabled: true, storage: 'database', window: 60, max: 100 }` with `customRules` `/sign-in/email` 5/60 s, `/sign-up/email` 3/60 s, `/request-password-reset` 3/600 s, `/forget-password` 3/600 s, `/send-verification-email` 3/600 s, `/token` 60/60 s; `advanced.ipAddress.ipAddressHeaders: ['x-calendium-client-ip']`; `hooks.before` = password policy; `trustedOrigins: buildTrustedOrigins(process.env)`.
- Client IP: route.ts overwrites `x-calendium-client-ip` on every request — `TRUST_PROXY=true` → first `X-Forwarded-For` hop; else the header only when single-valued; else `''` (Better Auth's shared `no-trusted-ip` bucket). 429 responses carry `X-Retry-After`; pages show `Too many attempts, try again in N s`.
- Password policy (server hook, mirrored client-side): 10–128 chars; reject a password containing the email local part (case-insensitive, local parts ≥ 3 chars) with code `PASSWORD_CONTAINS_EMAIL` (400); length violations `PASSWORD_TOO_SHORT`/`PASSWORD_TOO_LONG` (400).
- Trusted origins (fixed, always): `calendium://`, `https://appleid.apple.com`, `wails://wails`, `wails://wails.localhost`, `http://wails.localhost`, `https://wails.localhost`; from env: `BETTER_AUTH_URL`, `PUBLIC_WEB_URL`, `CORS_ALLOWED_ORIGINS`; `http://localhost:*` and `http://127.0.0.1:*` only when `NODE_ENV !== 'production' || ALLOW_DEV_ORIGINS === 'true'`. CORS reflection never reflects `appleid.apple.com`. Go `corsMiddleware(next, allowed, allowDevOrigins)`: Wails + explicit origins always, localhost/127.0.0.1/::1 only when `ALLOW_DEV_ORIGINS=true`; `backend/.env.example` sets it `true`, root `.env.example` leaves it blank.
- Routes: `/forgot-password`, `/reset-password` (`?token=` | `?error=`), `/verify-email` (`?error=`), Settings `?tab=account`; verify links `${BETTER_AUTH_URL}/api/auth/verify-email?token=…&callbackURL=/verify-email` (24 h), reset links `${BETTER_AUTH_URL}/api/auth/reset-password/<token>?callbackURL=/reset-password` (1 h); sign-in while unverified → 403 `EMAIL_NOT_VERIFIED`; reused/invalid reset token → `INVALID_TOKEN`.
- JWT cache: reuse while `now < exp − 60 000 ms` (decoded, not verified), one shared in-flight mint, `null` and tokens without a decodable `exp` are never cached; `ApiClient.request` retries exactly once on 401 when a token was attached; `actAs()` shares the cache; sign-out invalidates. The e2e `/token` stub (401 → `null`) stays uncached.
- Go port: `port.Email{To []string; ReplyTo, Subject, Text, HTML string}`, `port.Mailer{ Send(ctx, Email) error }`; `config.SMTP{Host string; Port int; User, Pass, From string; Secure bool}` + `Configured() bool`; `smtp.New(cfg config.SMTP) *Client` with seams `DialContext` and `TLSConfig`; `Send` 15 s deadline, STARTTLS mandatory off loopback (`smtp: server does not offer STARTTLS; refusing to send credentials in clear`), AUTH PLAIN then LOGIN, none when `User` is empty; RFC 5322 headers + quoted-printable `text/plain` or `multipart/alternative`; CR/LF in header values rejected.
- Team invitations: mailbox (From = inviter) → `Mailer` (From = `SMTP_FROM`, `Reply-To` = inviter) → link (`delivery: "link"`, `inviteUrl: <AppBaseURL>/invite/<token>`); send failure in the first two revokes the row and returns `sending invitation email: …` (500). `domain.TeamInvitation` gains response-only `Delivery InvitationDelivery \`json:"delivery,omitempty"\`` (`mailbox|smtp|link`) and `InviteURL string \`json:"inviteUrl,omitempty"\``; `InstanceFeatures.Email bool \`json:"email"\`` = `cfg.SMTP.Configured()`; `httpapi.Deps.AllowDevOrigins bool`; `TeamServiceDeps.Mailer port.Mailer` (nil when unset).
- Shared TS: `InstanceFeatures.email?: boolean` (optional, like `maps`, so pre-piece-2 servers and the many `features:` fixtures stay valid; the Go side always sends it), `TeamInvitation.delivery?: 'mailbox'|'smtp'|'link'`, `TeamInvitation.inviteUrl?: string`, `createAccessTokenCache(mint, now?)`, `decodeJwtExp(token)`, `ApiClientOptions.accessTokens?`, `ApiClient.invalidateAccessToken()`.
- Hexagonal rule: `domain` imports only stdlib; `port` imports `domain`; `service` imports `domain` + `port`; adapters import `port`/`config`; nothing in `domain`/`port`/`service` imports an adapter. Stdlib only in Go (`pgx` solely as the `database/sql` driver). New web deps: `nodemailer`, `@types/nodemailer` only.
- Demo mode + e2e: `playwright.config.ts` adds `SELF_HOSTED: 'true'` to the webServer env; `apps/web/e2e/fixtures.ts` is unchanged; `next build` never constructs a mail transport and never throws on missing SMTP (only `instrumentation.ts` `register()` aborts `next start`/`next dev`).

## Review Focus

- `X-Forwarded-For: " 203.0.113.9 , 10.0.0.1,"` (whitespace, trailing comma) with `TRUST_PROXY=true` must yield `203.0.113.9`, and with `TRUST_PROXY=false` must yield `''` (multi-hop is never trusted) — and a request that arrives with its own forged `x-calendium-client-ip` header must have it overwritten. (Tests added to Task 6.)
- A password containing the email local part in a different case (`Ada.Lovelace@example.test` + `ADA.LOVELACE-2026!`) must be rejected with `PASSWORD_CONTAINS_EMAIL`; a 2-char local part (`ab@example.test`) must not reject `abcdefghijk1`. (Tests added to Task 6.)
- SMTP wire format: a text line beginning with `.` must arrive dot-stuffed and must not terminate the message; a non-ASCII subject must go out RFC 2047 Q-encoded and decode back identically; a recipient display name containing a comma must be quoted so it is one `To` address. (Tests added to Tasks 2 and 3.)
- A freshly minted JWT whose `exp` is already inside the 60 s margin must be returned to the caller but never cached, so a slow clock cannot pin a stale token for every subsequent request. (Test added to Task 11.)
- The forgot-password page must render the email form when `/v1/instance` is unavailable (query error / undefined) or when `features.email` is absent — only an explicit `features.email === false` switches to the admin instructions. (Test added to Task 13.)

## Execution tracks

Strict file ownership: every file below belongs to exactly one track; agents never edit another track's files. Within a track tasks run in order; across tracks only the stated dependencies apply.

- **Track A — Go backend (Tasks 1–5, sequential).** Owns `backend/internal/config/{config.go,config_test.go}`, `backend/.env.example`, `backend/internal/port/driven.go`, `backend/internal/adapter/out/smtp/*` (new), `backend/internal/domain/team.go`, `backend/internal/service/{team.go,team_test.go,fakes_test.go}`, `backend/internal/adapter/in/httpapi/{httpapi.go,middleware.go,middleware_test.go,instance.go,instance_test.go}`, `backend/cmd/api/main.go`, `backend/cmd/worker/main.go`. Starts immediately; no dependency on other tracks.
- **Track B — web auth config and email library (Tasks 6–10, sequential).** Owns `apps/web/lib/auth-env.ts`, `apps/web/lib/auth-env.test.ts`, `apps/web/lib/email/**` (new), `apps/web/instrumentation.ts`, `apps/web/instrumentation.test.ts`, `apps/web/package.json`, `apps/web/playwright.config.ts`, `apps/web/lib/auth.ts`, `apps/web/lib/auth.test.ts`, `apps/web/app/api/auth/[...all]/route.ts`, `backend/migrations/0028_better_auth_rate_limit.sql`, `apps/web/lib/auth-client.ts`, `apps/web/lib/auth-client.test.ts`, `apps/web/lib/api.ts`, `apps/web/lib/sign-out.ts`, `apps/web/lib/sign-out.test.ts`, `apps/web/lib/use-mail-attachments.test.ts`, `apps/web/components/app/command-palette.test.tsx` (one mock line), `apps/web/scripts/reset-password.mjs`, `apps/web/Dockerfile`. Tasks 6–9 start immediately; Task 10 starts after Track C's Task 11 is committed (it imports `createAccessTokenCache`).
- **Track C — shared contract and web pages (Tasks 11–16).** Owns `packages/shared/src/{access-token-cache.ts,access-token-cache.test.ts,client.ts,client.test.ts,types.ts,index.ts}`, `apps/web/lib/auth-copy.ts`, `apps/web/lib/auth-copy.test.ts`, `apps/web/components/auth/auth-shell.tsx`, `apps/web/app/signin/page.tsx`, `apps/web/app/signin/signin-page.test.tsx`, `apps/web/app/forgot-password/**`, `apps/web/app/reset-password/**`, `apps/web/app/verify-email/**`, `apps/web/app/(app)/settings/settings-page.tsx`, `apps/web/app/(app)/settings/settings-account.tsx`, `apps/web/app/(app)/settings/settings-account.test.tsx`, `apps/web/e2e/auth-recovery.spec.ts`, `apps/web/e2e/settings-account.spec.ts`. Task 11 starts immediately; Tasks 12–15 start after Track B's Task 6 is committed (they import `passwordPolicyError` from `lib/auth-env.ts`); Task 16 after Tasks 12–15.
- **Track D — desktop, mobile, docs and env (Tasks 17–19).** Owns `apps/desktop/frontend/src/lib/{auth.ts,auth.test.ts,api.ts,server-config.ts,server-config.test.ts}`, `apps/desktop/frontend/src/views/{SignInView.tsx,SignInView.test.tsx}`, `apps/mobile/lib/{server-config.ts,server-config.test.ts,api.ts,api.test.ts,auth-client.ts}`, `apps/mobile/context/{auth.tsx,auth.test.tsx}`, `apps/mobile/app/{index.tsx,index.test.tsx}`, `.env.example`, `apps/web/.env.example`, `deploy/nginx/calendium.conf`, `docs/self-hosting/{configuration.md,providers.md,security.md,reverse-proxy-tls.md,upgrades.md,troubleshooting.md}`, `docs/release/go-live-external-checklist.md`. Tasks 17 and 18 start after Track C's Task 11 is committed (they consume `createAccessTokenCache` and `InstanceFeatures.email`); Task 19 starts immediately. Tasks 17, 18 and 19 touch disjoint files and may run on three agents.
- **Final (Tasks 20–21, after every track has merged).** Full-suite gate, then the manual Mailpit runbook.

Dependency order: A ∥ B(6–9) ∥ C(11) → B(10) and C(12–16) and D(17–18) → D(19) any time → 20 → 21.

---

### Task 1: Config — `SMTP_*`, `ALLOW_DEV_ORIGINS`, `ValidateCloudEmail`, backend env template

**Files:**
- Modify: `backend/internal/config/config.go` (add `SMTP` type + `Config.SMTP`, `HTTP.AllowDevOrigins`, parsing block after the `SELF_HOSTED` block, `ValidateCloudEmail`)
- Modify: `backend/internal/config/config_test.go` (`configEnvKeys`; two new test functions)
- Modify: `backend/.env.example` (new SMTP block; `ALLOW_DEV_ORIGINS=true`; CORS comment)

**Interfaces:**
- Consumes: piece 1's `withBase`/`clearEnv` helpers in `config_test.go` (unchanged).
- Produces: `config.SMTP{Host string; Port int; User, Pass, From string; Secure bool}`; `func (s SMTP) Configured() bool`; `Config.SMTP SMTP`; `HTTP.AllowDevOrigins bool`; `func (c Config) ValidateCloudEmail() error` (message exactly `SMTP_HOST and SMTP_FROM are required when SELF_HOSTED=false (cloud mode); set them or run with SELF_HOSTED=true`); `FromEnv` errors `SMTP_* is partially configured: <missing>`, `SMTP_PORT must be an integer between 1 and 65535, got %q`, `SMTP_SECURE must be true or false, got %q`, `SMTP_FROM must be an email address or "Name <addr>", got %q`, `ALLOW_DEV_ORIGINS must be true or false, got %q`.

- [ ] **Step 1: Write the failing config tests.** In `backend/internal/config/config_test.go` add `"strings"` to the imports, extend `configEnvKeys` with `"SMTP_HOST", "SMTP_PORT", "SMTP_USER", "SMTP_PASS", "SMTP_FROM", "SMTP_SECURE", "ALLOW_DEV_ORIGINS",` (after the `"OPEN_METEO_URL",` entry), and append:

```go
// TestSMTPFromEnv covers the transactional-email knobs (piece 2): unset is
// "disabled", a full block parses with defaults, half-set blocks and bad
// port/bool/address values error, and ALLOW_DEV_ORIGINS parses as a bool.
// SMTP_PORT/SMTP_SECURE alone are NOT "partial" (the env templates ship
// them pre-filled next to a blank SMTP_HOST).
func TestSMTPFromEnv(t *testing.T) {
	full := map[string]string{"SMTP_HOST": "smtp.example.test", "SMTP_FROM": "Calendium <noreply@example.test>"}
	withFull := func(extra map[string]string) map[string]string {
		m := withBase(full)
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string // substring of the joined error; "" = success
		check   func(t *testing.T, c Config)
	}{
		{
			name:  "unset leaves SMTP unconfigured",
			env:   withBase(nil),
			check: func(t *testing.T, c Config) { assertBool(t, "SMTP.Configured", c.SMTP.Configured(), false) },
		},
		{
			name:  "port and secure alone are not a partial block",
			env:   withBase(map[string]string{"SMTP_PORT": "587", "SMTP_SECURE": "false"}),
			check: func(t *testing.T, c Config) { assertBool(t, "SMTP.Configured", c.SMTP.Configured(), false) },
		},
		{
			name: "host+from parse with port 587 and STARTTLS defaults",
			env:  withBase(map[string]string{"SMTP_HOST": " smtp.example.test ", "SMTP_FROM": "Calendium <noreply@example.test>"}),
			check: func(t *testing.T, c Config) {
				assertBool(t, "SMTP.Configured", c.SMTP.Configured(), true)
				assertEq(t, "SMTP.Host", c.SMTP.Host, "smtp.example.test")
				assertInt(t, "SMTP.Port", c.SMTP.Port, 587)
				assertBool(t, "SMTP.Secure", c.SMTP.Secure, false)
				assertEq(t, "SMTP.From", c.SMTP.From, "Calendium <noreply@example.test>")
				assertEq(t, "SMTP.User", c.SMTP.User, "")
			},
		},
		{
			name: "user+pass+port+secure",
			env:  withFull(map[string]string{"SMTP_USER": "apikey", "SMTP_PASS": "s3cret", "SMTP_PORT": "465", "SMTP_SECURE": "true"}),
			check: func(t *testing.T, c Config) {
				assertEq(t, "SMTP.User", c.SMTP.User, "apikey")
				assertEq(t, "SMTP.Pass", c.SMTP.Pass, "s3cret")
				assertInt(t, "SMTP.Port", c.SMTP.Port, 465)
				assertBool(t, "SMTP.Secure", c.SMTP.Secure, true)
			},
		},
		{name: "SMTP_PORT zero errors", env: withFull(map[string]string{"SMTP_PORT": "0"}), wantErr: `SMTP_PORT must be an integer between 1 and 65535, got "0"`},
		{name: "SMTP_PORT too large errors", env: withFull(map[string]string{"SMTP_PORT": "65536"}), wantErr: "SMTP_PORT must be an integer"},
		{name: "SMTP_PORT non-int errors", env: withFull(map[string]string{"SMTP_PORT": "abc"}), wantErr: `SMTP_PORT must be an integer between 1 and 65535, got "abc"`},
		{name: "SMTP_SECURE invalid errors", env: withFull(map[string]string{"SMTP_SECURE": "yes"}), wantErr: `SMTP_SECURE must be true or false, got "yes"`},
		{name: "SMTP_FROM malformed errors", env: withFull(map[string]string{"SMTP_FROM": "not-an-address"}), wantErr: `SMTP_FROM must be an email address or "Name <addr>", got "not-an-address"`},
		{name: "host without from is partial", env: withBase(map[string]string{"SMTP_HOST": "h"}), wantErr: "SMTP_* is partially configured: SMTP_FROM"},
		{name: "from without host is partial", env: withBase(map[string]string{"SMTP_FROM": "a@b.test"}), wantErr: "SMTP_* is partially configured: SMTP_HOST"},
		{name: "user without pass is partial", env: withFull(map[string]string{"SMTP_USER": "u"}), wantErr: "SMTP_* is partially configured: SMTP_PASS"},
		{name: "pass without user is partial", env: withFull(map[string]string{"SMTP_PASS": "p"}), wantErr: "SMTP_* is partially configured: SMTP_USER"},
		{name: "only user set names host, from and pass", env: withBase(map[string]string{"SMTP_USER": "u"}), wantErr: "SMTP_* is partially configured: SMTP_HOST, SMTP_FROM, SMTP_PASS"},
		{name: "ALLOW_DEV_ORIGINS default false", env: withBase(nil), check: func(t *testing.T, c Config) { assertBool(t, "AllowDevOrigins", c.HTTP.AllowDevOrigins, false) }},
		{name: "ALLOW_DEV_ORIGINS true", env: withBase(map[string]string{"ALLOW_DEV_ORIGINS": "true"}), check: func(t *testing.T, c Config) { assertBool(t, "AllowDevOrigins", c.HTTP.AllowDevOrigins, true) }},
		{name: "ALLOW_DEV_ORIGINS invalid errors", env: withBase(map[string]string{"ALLOW_DEV_ORIGINS": "yes"}), wantErr: `ALLOW_DEV_ORIGINS must be true or false, got "yes"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			c, err := FromEnv()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("FromEnv() error = %v, want containing %q", err, tt.wantErr)
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

// TestValidateCloudEmail pins the startup rule: cloud mode refuses to run
// without an SMTP sender (verification, reset and invitations depend on it);
// self-host boots with or without one. Same shape as ValidateCloudBilling.
func TestValidateCloudEmail(t *testing.T) {
	const want = "SMTP_HOST and SMTP_FROM are required when SELF_HOSTED=false (cloud mode); set them or run with SELF_HOSTED=true"
	tests := []struct {
		name       string
		selfHosted bool
		smtp       SMTP
		wantErr    bool
	}{
		{"self-host without SMTP boots", true, SMTP{}, false},
		{"self-host with SMTP boots", true, SMTP{Host: "h", From: "a@b.test", Port: 587}, false},
		{"cloud with SMTP boots", false, SMTP{Host: "h", From: "a@b.test", Port: 587}, false},
		{"cloud without SMTP refuses", false, SMTP{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Config{Instance: Instance{SelfHosted: tt.selfHosted}, SMTP: tt.smtp}
			err := c.ValidateCloudEmail()
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("ValidateCloudEmail() = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != want {
				t.Fatalf("ValidateCloudEmail() = %v, want %q", err, want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail to compile.**

```bash
cd backend && go test ./internal/config/ -run 'TestSMTPFromEnv|TestValidateCloudEmail'
```

Expected: `undefined: SMTP`, `c.SMTP undefined`, `c.HTTP.AllowDevOrigins undefined`, `c.ValidateCloudEmail undefined`.

- [ ] **Step 3: Implement the config.** In `backend/internal/config/config.go`:

(a) add `netmail "net/mail"` to the import block;

(b) in `type HTTP struct`, after `CORSAllowedOrigins []string`, add:

```go
	// AllowDevOrigins (ALLOW_DEV_ORIGINS, default false) reflects
	// http(s)://localhost, 127.0.0.1 and ::1 origins in CORS. The Wails
	// WebView origins and CORSAllowedOrigins are always reflected; this gates
	// only the local dev servers. backend/.env.example (run-the-binary
	// template) sets it true; the production root .env leaves it blank.
	AllowDevOrigins bool
```

(c) after `type Instance struct { … }` add:

```go
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
```

(d) in `type Config struct` add `SMTP       SMTP` after `Maps       Maps`;

(e) in `FromEnv`, directly after the `SELF_HOSTED` `if v := os.Getenv("SELF_HOSTED"); v != "" { … }` block, add:

```go
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
	if v := os.Getenv("SMTP_SECURE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("SMTP_SECURE must be true or false, got %q", v))
		} else {
			cfg.SMTP.Secure = b
		}
	}
	if cfg.SMTP.From != "" {
		if _, err := netmail.ParseAddress(cfg.SMTP.From); err != nil {
			errs = append(errs, fmt.Errorf("SMTP_FROM must be an email address or \"Name <addr>\", got %q", cfg.SMTP.From))
		}
	}
	if err := cfg.SMTP.partialError(); err != nil {
		errs = append(errs, err)
	}
	if v := os.Getenv("ALLOW_DEV_ORIGINS"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("ALLOW_DEV_ORIGINS must be true or false, got %q", v))
		} else {
			cfg.HTTP.AllowDevOrigins = b
		}
	}
```

(f) after `FromEnv` (next to piece 1's `ValidateCloudBilling`) add:

```go
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
```

- [ ] **Step 4: Run the config tests.**

```bash
cd backend && go test ./internal/config/
```

Expected: `ok  	calendium/backend/internal/config`.

- [ ] **Step 5: Update `backend/.env.example`.** Replace the two comment lines above `CORS_ALLOWED_ORIGINS=` (`# Extra browser origins allowed for CORS on the API + Better Auth routes` … `# default.`) with:

```
# Extra browser origins allowed for CORS on the API + Better Auth routes
# (comma-separated). The desktop app's wails:// origins are always allowed;
# localhost dev origins only when ALLOW_DEV_ORIGINS=true (below).
```

and append this block after the `# --- Mail behavior ---` block:

```
# --- Transactional email (SMTP) ---
# The instance's own sender: team invitations when the inviter has no connected
# mailbox (the web app uses the same variables for verification + password
# reset mail). Any SMTP provider works.
#   SELF_HOSTED=true  : optional — leave SMTP_HOST blank to disable (email
#                       verification off, invitations fall back to copyable links).
#   SELF_HOSTED=false : SMTP_HOST and SMTP_FROM are REQUIRED to boot.
SMTP_HOST=
SMTP_PORT=587
# Optional login; set both or neither.
SMTP_USER=
SMTP_PASS=
# "addr" or "Name <addr>".
SMTP_FROM=
# true = implicit TLS (usually port 465); false = STARTTLS, required unless
# SMTP_HOST is loopback (Mailpit / a local relay).
SMTP_SECURE=false

# --- Browser origins ---
# Reflect http(s)://localhost / 127.0.0.1 origins in CORS. This template is for
# running the binary directly in development, so it is on; the production root
# .env.example leaves it blank (false). Wails desktop origins and
# CORS_ALLOWED_ORIGINS are reflected regardless.
ALLOW_DEV_ORIGINS=true
```

- [ ] **Step 6: Vet and commit.**

```bash
cd backend && go vet ./internal/config/ && go test ./internal/config/
git add backend/internal/config/config.go backend/internal/config/config_test.go backend/.env.example
git commit -m "feat(config): SMTP_* sender settings, ALLOW_DEV_ORIGINS and the cloud-mode email startup rule" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 2: Port `Mailer` + SMTP adapter message builder (RFC 5322 rendering)

**Files:**
- Modify: `backend/internal/port/driven.go` (append `Email` + `Mailer` at the end of the file)
- Create: `backend/internal/adapter/out/smtp/message.go`
- Test: `backend/internal/adapter/out/smtp/message_test.go`

**Interfaces:**
- Produces: `port.Email{To []string; ReplyTo string; Subject string; Text string; HTML string}`; `port.Mailer interface { Send(ctx context.Context, msg Email) error }`; package-private `type message struct{ from string; rcpts []string; raw []byte }`; `func buildMessage(msg port.Email, from *netmail.Address, now time.Time, msgID string) (message, error)`; `func newMessageID(fromAddr string) (string, error)`; `var errHeaderNewline`.

- [ ] **Step 1: Add the port.** Append to `backend/internal/port/driven.go`:

```go
// ---------------------------------------------------------------------------
// Transactional email (piece 2)
// ---------------------------------------------------------------------------

// Email is a transactional message sent from the instance's own address
// (SMTP_FROM), independent of any user's connected mailbox.
type Email struct {
	To      []string
	ReplyTo string // optional
	Subject string
	Text    string // required
	HTML    string // optional; multipart/alternative when set
}

// Mailer delivers transactional email. Implementations are safe for
// concurrent use and honour ctx deadlines.
type Mailer interface {
	Send(ctx context.Context, msg Email) error
}
```

- [ ] **Step 2: Write the failing message tests.** Create `backend/internal/adapter/out/smtp/message_test.go`:

```go
package smtp

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	netmail "net/mail"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/port"
)

// testNow is a Sunday so the RFC 1123Z Date header is deterministic.
var testNow = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

func parseFrom(t *testing.T, s string) *netmail.Address {
	t.Helper()
	a, err := netmail.ParseAddress(s)
	if err != nil {
		t.Fatalf("parse from %q: %v", s, err)
	}
	return a
}

func TestBuildMessageTextOnly(t *testing.T) {
	m, err := buildMessage(port.Email{
		To:      []string{"Ada Lovelace <ada@example.test>"},
		Subject: "Welcome",
		Text:    "Hello Ada,\nline two = with equals\n",
	}, parseFrom(t, "Calendium <noreply@calendium.test>"), testNow, "<abc@calendium.test>")
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	if m.from != "noreply@calendium.test" {
		t.Fatalf("envelope from = %q, want the bare SMTP_FROM address", m.from)
	}
	if len(m.rcpts) != 1 || m.rcpts[0] != "ada@example.test" {
		t.Fatalf("envelope rcpts = %v, want [ada@example.test]", m.rcpts)
	}
	msg, err := netmail.ReadMessage(bytes.NewReader(m.raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v\n%s", err, m.raw)
	}
	for k, want := range map[string]string{
		"From":                      `"Calendium" <noreply@calendium.test>`,
		"To":                        `"Ada Lovelace" <ada@example.test>`,
		"Subject":                   "Welcome",
		"Date":                      "Sun, 04 Oct 2026 12:00:00 +0000",
		"Message-Id":                "<abc@calendium.test>",
		"Mime-Version":              "1.0",
		"Content-Type":              `text/plain; charset="utf-8"`,
		"Content-Transfer-Encoding": "quoted-printable",
	} {
		if got := msg.Header.Get(k); got != want {
			t.Errorf("header %s = %q, want %q", k, got, want)
		}
	}
	if got := msg.Header.Get("Reply-To"); got != "" {
		t.Errorf("Reply-To = %q, want absent", got)
	}
	if !bytes.Contains(m.raw, []byte("=3D")) {
		t.Fatalf("body is not quoted-printable encoded:\n%s", m.raw)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if string(body) != "Hello Ada,\r\nline two = with equals\r\n" {
		t.Fatalf("decoded body = %q", body)
	}
}

func TestBuildMessageMultipartWithReplyToAndEncodedSubject(t *testing.T) {
	const subject = "Convite: equipe “Vendas” — café"
	m, err := buildMessage(port.Email{
		To:      []string{"a@example.test", "Bob, Jr. <bob@example.test>"},
		ReplyTo: "Olive Owner <owner@acme.test>",
		Subject: subject,
		Text:    "Plain body\n",
		HTML:    "<p>HTML body &amp; more</p>\n",
	}, parseFrom(t, "noreply@calendium.test"), testNow, "<id@calendium.test>")
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	if len(m.rcpts) != 2 || m.rcpts[0] != "a@example.test" || m.rcpts[1] != "bob@example.test" {
		t.Fatalf("envelope rcpts = %v", m.rcpts)
	}
	msg, err := netmail.ReadMessage(bytes.NewReader(m.raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v\n%s", err, m.raw)
	}
	// Subject is RFC 2047 Q-encoded on the wire and decodes back verbatim.
	rawSubject := msg.Header.Get("Subject")
	if !strings.HasPrefix(rawSubject, "=?utf-8?q?") {
		t.Fatalf("Subject = %q, want an RFC 2047 encoded word", rawSubject)
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(rawSubject)
	if err != nil || decoded != subject {
		t.Fatalf("decoded subject = %q (err %v), want %q", decoded, err, subject)
	}
	if got := msg.Header.Get("Reply-To"); got != `"Olive Owner" <owner@acme.test>` {
		t.Fatalf("Reply-To = %q", got)
	}
	// A display name with a comma is quoted so the header still parses as two addresses.
	if got := msg.Header.Get("To"); got != `<a@example.test>, "Bob, Jr." <bob@example.test>` {
		t.Fatalf("To = %q", got)
	}
	if tos, err := msg.Header.AddressList("To"); err != nil || len(tos) != 2 || tos[1].Name != "Bob, Jr." {
		t.Fatalf("To does not parse back to two addresses: %v (err %v)", tos, err)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/alternative" || params["boundary"] == "" {
		t.Fatalf("Content-Type = %q (err %v), want multipart/alternative with a boundary", msg.Header.Get("Content-Type"), err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	var types, bodies []string
	for {
		p, err := mr.NextRawPart() // raw: keep the CTE header and decode ourselves
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextRawPart: %v", err)
		}
		if cte := p.Header.Get("Content-Transfer-Encoding"); cte != "quoted-printable" {
			t.Fatalf("part CTE = %q, want quoted-printable", cte)
		}
		types = append(types, p.Header.Get("Content-Type"))
		b, err := io.ReadAll(quotedprintable.NewReader(p))
		if err != nil {
			t.Fatalf("decode part: %v", err)
		}
		bodies = append(bodies, string(b))
	}
	if len(types) != 2 || types[0] != `text/plain; charset="utf-8"` || types[1] != `text/html; charset="utf-8"` {
		t.Fatalf("part types = %v, want text/plain then text/html", types)
	}
	if bodies[0] != "Plain body\r\n" || bodies[1] != "<p>HTML body &amp; more</p>\r\n" {
		t.Fatalf("part bodies = %q", bodies)
	}
}

func TestBuildMessageRejectsBadInput(t *testing.T) {
	from := parseFrom(t, "noreply@calendium.test")
	tests := []struct {
		name string
		msg  port.Email
		want string
	}{
		{"no recipients", port.Email{Subject: "s", Text: "t"}, "at least one recipient"},
		{"blank text", port.Email{To: []string{"a@example.test"}, Subject: "s", Text: " \n"}, "text body is required"},
		{"CRLF in subject", port.Email{To: []string{"a@example.test"}, Subject: "x\r\nBcc: evil@example.test", Text: "t"}, errHeaderNewline.Error()},
		{"LF in reply-to", port.Email{To: []string{"a@example.test"}, ReplyTo: "a@b.test\nX-Injected: 1", Subject: "s", Text: "t"}, errHeaderNewline.Error()},
		{"CR in recipient", port.Email{To: []string{"a@example.test\rBcc: x@y.test"}, Subject: "s", Text: "t"}, errHeaderNewline.Error()},
		{"invalid recipient", port.Email{To: []string{"not an address"}, Subject: "s", Text: "t"}, "invalid recipient"},
		{"invalid reply-to", port.Email{To: []string{"a@example.test"}, ReplyTo: "nope", Subject: "s", Text: "t"}, "invalid Reply-To"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildMessage(tt.msg, from, testNow, "<id@x.test>")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestNewMessageIDUsesFromDomain(t *testing.T) {
	id, err := newMessageID("noreply@calendium.test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id, "<") || !strings.HasSuffix(id, "@calendium.test>") || len(id) != len("<>@calendium.test")+32 {
		t.Fatalf("id = %q, want <32 hex chars@calendium.test>", id)
	}
	other, _ := newMessageID("noreply@calendium.test")
	if other == id {
		t.Fatal("message ids must be unique")
	}
	if fallback, _ := newMessageID("no-at-sign"); !strings.HasSuffix(fallback, "@calendium>") {
		t.Fatalf("fallback id = %q, want the calendium domain", fallback)
	}
}
```

- [ ] **Step 3: Run the tests and watch them fail.**

```bash
cd backend && go test ./internal/adapter/out/smtp/
```

Expected: `undefined: buildMessage`, `undefined: newMessageID`, `undefined: errHeaderNewline` (package has no non-test files yet — `go test` reports `no non-test Go files` or build errors; either is the expected failure).

- [ ] **Step 4: Implement the builder.** Create `backend/internal/adapter/out/smtp/message.go`:

```go
// Package smtp is the outbound transactional-email adapter: a port.Mailer
// over the Go standard library only (net/smtp, crypto/tls, mime/*,
// net/mail). It sends from the instance's own SMTP_FROM address and never
// touches a user's connected mailbox.
package smtp

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	netmail "net/mail"
	"net/textproto"
	"strings"
	"time"

	"calendium/backend/internal/port"
)

// message is a fully rendered RFC 5322 message plus its SMTP envelope.
type message struct {
	from  string   // envelope MAIL FROM (bare addr-spec)
	rcpts []string // envelope RCPT TO (bare addr-specs)
	raw   []byte   // headers + body, CRLF line endings; dot-stuffing is the transport's job
}

// errHeaderNewline rejects CR/LF in any header value so a caller-controlled
// string can never inject extra headers.
var errHeaderNewline = errors.New("smtp: header values must not contain CR or LF")

// buildMessage renders msg into wire form. from is the parsed SMTP_FROM;
// now stamps Date; msgID is the full "<id@domain>" Message-ID.
func buildMessage(msg port.Email, from *netmail.Address, now time.Time, msgID string) (message, error) {
	if len(msg.To) == 0 {
		return message{}, errors.New("smtp: at least one recipient is required")
	}
	if strings.TrimSpace(msg.Text) == "" {
		return message{}, errors.New("smtp: a text body is required")
	}
	for _, v := range append([]string{msg.Subject, msg.ReplyTo}, msg.To...) {
		if strings.ContainsAny(v, "\r\n") {
			return message{}, errHeaderNewline
		}
	}
	out := message{from: from.Address}
	display := make([]string, 0, len(msg.To))
	for _, t := range msg.To {
		a, err := netmail.ParseAddress(t)
		if err != nil {
			return message{}, fmt.Errorf("smtp: invalid recipient %q: %w", t, err)
		}
		out.rcpts = append(out.rcpts, a.Address)
		display = append(display, a.String())
	}

	var b strings.Builder
	header := func(k, v string) {
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(v)
		b.WriteString("\r\n")
	}
	header("From", from.String())
	header("To", strings.Join(display, ", "))
	if msg.ReplyTo != "" {
		a, err := netmail.ParseAddress(msg.ReplyTo)
		if err != nil {
			return message{}, fmt.Errorf("smtp: invalid Reply-To %q: %w", msg.ReplyTo, err)
		}
		header("Reply-To", a.String())
	}
	header("Subject", mime.QEncoding.Encode("utf-8", msg.Subject))
	header("Date", now.UTC().Format(time.RFC1123Z))
	header("Message-ID", msgID)
	header("MIME-Version", "1.0")

	if msg.HTML == "" {
		header("Content-Type", `text/plain; charset="utf-8"`)
		header("Content-Transfer-Encoding", "quoted-printable")
		b.WriteString("\r\n")
		if err := writeQuotedPrintable(&b, msg.Text); err != nil {
			return message{}, err
		}
		out.raw = []byte(b.String())
		return out, nil
	}

	mw := multipart.NewWriter(&b)
	header("Content-Type", `multipart/alternative; boundary="`+mw.Boundary()+`"`)
	b.WriteString("\r\n")
	parts := []struct{ ctype, body string }{{"text/plain", msg.Text}, {"text/html", msg.HTML}}
	for _, p := range parts {
		pw, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {p.ctype + `; charset="utf-8"`},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return message{}, err
		}
		if err := writeQuotedPrintable(pw, p.body); err != nil {
			return message{}, err
		}
	}
	if err := mw.Close(); err != nil {
		return message{}, err
	}
	out.raw = []byte(b.String())
	return out, nil
}

func writeQuotedPrintable(w io.Writer, s string) error {
	qw := quotedprintable.NewWriter(w)
	if _, err := io.WriteString(qw, s); err != nil {
		return err
	}
	return qw.Close()
}

// newMessageID returns "<32 hex chars@domain>" where domain is the part of
// fromAddr after '@' (falling back to "calendium").
func newMessageID(fromAddr string) (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("smtp: message id: %w", err)
	}
	domain := "calendium"
	if i := strings.LastIndex(fromAddr, "@"); i >= 0 && i+1 < len(fromAddr) {
		domain = fromAddr[i+1:]
	}
	return "<" + hex.EncodeToString(buf[:]) + "@" + domain + ">", nil
}
```

- [ ] **Step 5: Run the tests.**

```bash
cd backend && go test ./internal/adapter/out/smtp/ && go vet ./internal/port/ ./internal/adapter/out/smtp/
```

Expected: `ok  	calendium/backend/internal/adapter/out/smtp`.

- [ ] **Step 6: Commit.**

```bash
git add backend/internal/port/driven.go backend/internal/adapter/out/smtp/message.go backend/internal/adapter/out/smtp/message_test.go
git commit -m "feat(smtp): port.Mailer and the RFC 5322/MIME message builder for the stdlib SMTP adapter" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 3: SMTP adapter transport — dial, implicit TLS / STARTTLS, AUTH PLAIN/LOGIN, deadline

**Files:**
- Create: `backend/internal/adapter/out/smtp/smtp.go`
- Test: `backend/internal/adapter/out/smtp/smtp_test.go`

**Interfaces:**
- Consumes: `config.SMTP` (Task 1), `port.Email`/`port.Mailer` (Task 2), `buildMessage`/`newMessageID` (Task 2).
- Produces: `smtp.New(cfg config.SMTP) *Client`; `type Client struct { DialContext func(ctx context.Context, network, addr string) (net.Conn, error); TLSConfig *tls.Config; … }`; `func (c *Client) Send(ctx context.Context, msg port.Email) error`; `var ErrNoStartTLS = errors.New("smtp: server does not offer STARTTLS; refusing to send credentials in clear")`; `const sendTimeout = 15 * time.Second`.

- [ ] **Step 1: Write the failing transport tests with a scripted fake server.** Create `backend/internal/adapter/out/smtp/smtp_test.go`:

```go
package smtp

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"calendium/backend/internal/config"
	"calendium/backend/internal/port"
)

// fakeServer is a scripted SMTP server on 127.0.0.1:0 that records every
// command and the DATA payload it receives. It serves exactly one session.
type fakeServer struct {
	t         *testing.T
	ln        net.Listener
	tlsConfig *tls.Config // nil: no TLS at all; set: offer STARTTLS (plain listener) or serve implicit TLS
	implicit  bool        // listen with tls.Listen instead of offering STARTTLS
	authMechs string      // "" = no AUTH extension; e.g. "PLAIN LOGIN"
	silent    bool        // never send the 220 greeting (deadline tests)

	mu       sync.Mutex
	commands []string
	data     string
	authUser string
	authPass string
	upgraded bool // STARTTLS completed
}

func (s *fakeServer) addr() string { return s.ln.Addr().String() }

func (s *fakeServer) port() int {
	_, p, _ := net.SplitHostPort(s.addr())
	n, _ := strconv.Atoi(p)
	return n
}

func startFakeServer(t *testing.T, s *fakeServer) *fakeServer {
	t.Helper()
	s.t = t
	var err error
	if s.implicit {
		s.ln, err = tls.Listen("tcp", "127.0.0.1:0", s.tlsConfig)
	} else {
		s.ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = s.ln.Close() })
	go func() {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		s.serve(conn)
	}()
	return s
}

func (s *fakeServer) record(cmd string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands = append(s.commands, cmd)
}

func (s *fakeServer) serve(conn net.Conn) {
	if s.silent {
		time.Sleep(2 * time.Second)
		return
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	reply := func(lines ...string) {
		for _, l := range lines {
			_, _ = w.WriteString(l + "\r\n")
		}
		_ = w.Flush()
	}
	reply("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		s.record(line)
		verb := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
		switch verb {
		case "EHLO", "HELO":
			ext := []string{"250-fake"}
			s.mu.Lock()
			upgraded := s.upgraded
			s.mu.Unlock()
			if s.tlsConfig != nil && !s.implicit && !upgraded {
				ext = append(ext, "250-STARTTLS")
			}
			if s.authMechs != "" {
				ext = append(ext, "250-AUTH "+s.authMechs)
			}
			ext = append(ext, "250 8BITMIME")
			reply(ext...)
		case "STARTTLS":
			reply("220 go ahead")
			tc := tls.Server(conn, s.tlsConfig)
			if err := tc.Handshake(); err != nil {
				return
			}
			s.mu.Lock()
			s.upgraded = true
			s.mu.Unlock()
			conn = tc
			r = bufio.NewReader(conn)
			w = bufio.NewWriter(conn)
		case "AUTH":
			s.handleAuth(line, r, reply)
		case "MAIL", "RCPT":
			reply("250 OK")
		case "DATA":
			reply("354 go")
			var body strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				body.WriteString(l)
			}
			s.mu.Lock()
			s.data = body.String()
			s.mu.Unlock()
			reply("250 queued")
		case "QUIT":
			reply("221 bye")
			return
		default:
			reply("500 unknown")
		}
	}
}

func (s *fakeServer) handleAuth(line string, r *bufio.Reader, reply func(...string)) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		reply("501 bad auth")
		return
	}
	switch strings.ToUpper(fields[1]) {
	case "PLAIN":
		var payload string
		if len(fields) >= 3 {
			payload = fields[2]
		} else {
			reply("334 ")
			l, _ := r.ReadString('\n')
			payload = strings.TrimSpace(l)
		}
		raw, _ := base64.StdEncoding.DecodeString(payload)
		parts := strings.Split(string(raw), "\x00")
		if len(parts) == 3 {
			s.mu.Lock()
			s.authUser, s.authPass = parts[1], parts[2]
			s.mu.Unlock()
		}
		reply("235 ok")
	case "LOGIN":
		reply("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
		u, _ := r.ReadString('\n')
		reply("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
		p, _ := r.ReadString('\n')
		ub, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(u))
		pb, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(p))
		s.mu.Lock()
		s.authUser, s.authPass = string(ub), string(pb)
		s.mu.Unlock()
		reply("235 ok")
	default:
		reply("504 unsupported")
	}
}

// localCert mints a self-signed ECDSA certificate for 127.0.0.1 and returns
// the server config that serves it and a client config that trusts it.
func localCert(t *testing.T) (server, client *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	server = &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}},
		MinVersion:   tls.VersionTLS12,
	}
	client = &tls.Config{RootCAs: pool, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12}
	return server, client
}

// newTestClient builds a Client that always dials the fake server, whatever
// cfg.Host says (so a non-loopback hostname can be exercised).
func newTestClient(t *testing.T, srv *fakeServer, cfg config.SMTP, tlsCfg *tls.Config) *Client {
	t.Helper()
	cfg.Port = srv.port()
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.From == "" {
		cfg.From = "Calendium <noreply@calendium.test>"
	}
	c := New(cfg)
	c.now = func() time.Time { return testNow }
	target := srv.addr()
	c.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, target)
	}
	if tlsCfg != nil {
		c.TLSConfig = tlsCfg
	}
	return c
}

var sampleMail = port.Email{
	To:      []string{"ada@example.test"},
	Subject: "Hi",
	Text:    "Hello\n.leading dot line\nbye\n",
	HTML:    "<p>Hello</p>",
}

func indexPrefix(cmds []string, prefix string) int {
	for i, c := range cmds {
		if strings.HasPrefix(strings.ToUpper(c), strings.ToUpper(prefix)) {
			return i
		}
	}
	return -1
}

func TestSendPlaintextToLoopbackWithAuthPlain(t *testing.T) {
	srv := startFakeServer(t, &fakeServer{authMechs: "PLAIN LOGIN"})
	c := newTestClient(t, srv, config.SMTP{User: "apikey", Pass: "s3cret"}, nil)

	if err := c.Send(context.Background(), sampleMail); err != nil {
		t.Fatalf("Send: %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.authUser != "apikey" || srv.authPass != "s3cret" {
		t.Fatalf("auth = %q/%q, want apikey/s3cret", srv.authUser, srv.authPass)
	}
	if indexPrefix(srv.commands, "AUTH PLAIN") < 0 {
		t.Fatalf("PLAIN must be preferred over LOGIN; commands = %v", srv.commands)
	}
	if indexPrefix(srv.commands, "MAIL FROM:<noreply@calendium.test>") < 0 {
		t.Fatalf("missing MAIL FROM; commands = %v", srv.commands)
	}
	if indexPrefix(srv.commands, "RCPT TO:<ada@example.test>") < 0 {
		t.Fatalf("missing RCPT TO; commands = %v", srv.commands)
	}
	if indexPrefix(srv.commands, "QUIT") < 0 {
		t.Fatalf("session must end with QUIT; commands = %v", srv.commands)
	}
	if !strings.Contains(srv.data, "Subject: Hi\r\n") || !strings.Contains(srv.data, "Content-Type: multipart/alternative") {
		t.Fatalf("DATA payload missing headers:\n%s", srv.data)
	}
	// The body line that starts with "." reached the server dot-stuffed and
	// did not terminate the message early ("bye" is still inside the body).
	if !strings.Contains(srv.data, "\r\n..leading dot line\r\n") || !strings.Contains(srv.data, "\r\nbye\r\n") {
		t.Fatalf("DATA payload is not dot-stuffed as expected:\n%s", srv.data)
	}
	if srv.upgraded {
		t.Fatal("no STARTTLS was offered, so no upgrade should have happened")
	}
}

func TestSendStartTLSUpgradeThenAuth(t *testing.T) {
	serverTLS, clientTLS := localCert(t)
	srv := startFakeServer(t, &fakeServer{tlsConfig: serverTLS, authMechs: "PLAIN"})
	c := newTestClient(t, srv, config.SMTP{User: "u", Pass: "p"}, clientTLS)

	if err := c.Send(context.Background(), sampleMail); err != nil {
		t.Fatalf("Send: %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if !srv.upgraded {
		t.Fatal("STARTTLS was advertised but the client did not upgrade")
	}
	tlsIdx, authIdx := indexPrefix(srv.commands, "STARTTLS"), indexPrefix(srv.commands, "AUTH PLAIN")
	if tlsIdx < 0 || authIdx < 0 || authIdx < tlsIdx {
		t.Fatalf("AUTH must follow STARTTLS; commands = %v", srv.commands)
	}
	if srv.authUser != "u" || srv.authPass != "p" {
		t.Fatalf("auth = %q/%q", srv.authUser, srv.authPass)
	}
	if srv.data == "" {
		t.Fatal("no DATA received over the upgraded connection")
	}
}

func TestSendImplicitTLS(t *testing.T) {
	serverTLS, clientTLS := localCert(t)
	srv := startFakeServer(t, &fakeServer{implicit: true, tlsConfig: serverTLS})
	c := newTestClient(t, srv, config.SMTP{Secure: true}, clientTLS)

	if err := c.Send(context.Background(), sampleMail); err != nil {
		t.Fatalf("Send: %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if indexPrefix(srv.commands, "STARTTLS") >= 0 {
		t.Fatalf("implicit TLS must not negotiate STARTTLS; commands = %v", srv.commands)
	}
	if indexPrefix(srv.commands, "AUTH") >= 0 {
		t.Fatalf("no SMTP_USER, so no AUTH; commands = %v", srv.commands)
	}
	if !strings.Contains(srv.data, "From: \"Calendium\" <noreply@calendium.test>\r\n") {
		t.Fatalf("DATA payload missing From:\n%s", srv.data)
	}
}

func TestSendImplicitTLSRejectsUntrustedCertificate(t *testing.T) {
	serverTLS, _ := localCert(t)
	srv := startFakeServer(t, &fakeServer{implicit: true, tlsConfig: serverTLS})
	// Default TLSConfig: system roots only, so the in-test cert must fail verification.
	c := newTestClient(t, srv, config.SMTP{Secure: true}, nil)
	err := c.Send(context.Background(), sampleMail)
	if err == nil {
		t.Fatal("Send succeeded against an untrusted certificate")
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.data != "" {
		t.Fatal("message must not be delivered over an unverified connection")
	}
}

func TestSendRefusesPlaintextOffLoopback(t *testing.T) {
	srv := startFakeServer(t, &fakeServer{authMechs: "PLAIN"})
	c := newTestClient(t, srv, config.SMTP{Host: "mail.example.test", User: "u", Pass: "p"}, nil)

	err := c.Send(context.Background(), sampleMail)
	if !errors.Is(err, ErrNoStartTLS) {
		t.Fatalf("err = %v, want ErrNoStartTLS", err)
	}
	if err.Error() != "smtp: server does not offer STARTTLS; refusing to send credentials in clear" {
		t.Fatalf("err text = %q", err.Error())
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if indexPrefix(srv.commands, "AUTH") >= 0 || indexPrefix(srv.commands, "MAIL") >= 0 {
		t.Fatalf("nothing may be sent after the refusal; commands = %v", srv.commands)
	}
}

func TestSendAuthLoginFallback(t *testing.T) {
	srv := startFakeServer(t, &fakeServer{authMechs: "LOGIN"})
	c := newTestClient(t, srv, config.SMTP{User: "login-user", Pass: "login-pass"}, nil)

	if err := c.Send(context.Background(), sampleMail); err != nil {
		t.Fatalf("Send: %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if indexPrefix(srv.commands, "AUTH LOGIN") < 0 {
		t.Fatalf("expected AUTH LOGIN; commands = %v", srv.commands)
	}
	if srv.authUser != "login-user" || srv.authPass != "login-pass" {
		t.Fatalf("auth = %q/%q", srv.authUser, srv.authPass)
	}
}

func TestSendFailsOnUnsupportedAuthMechanism(t *testing.T) {
	srv := startFakeServer(t, &fakeServer{authMechs: "CRAM-MD5"})
	c := newTestClient(t, srv, config.SMTP{User: "u", Pass: "p"}, nil)
	err := c.Send(context.Background(), sampleMail)
	if err == nil || !strings.Contains(err.Error(), "no supported AUTH mechanism") {
		t.Fatalf("err = %v, want an unsupported-mechanism error", err)
	}
}

func TestSendHonoursContextDeadline(t *testing.T) {
	srv := startFakeServer(t, &fakeServer{silent: true})
	c := newTestClient(t, srv, config.SMTP{}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := c.Send(ctx, sampleMail)
	if err == nil {
		t.Fatal("Send succeeded against a server that never greeted")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Send ignored the ctx deadline: took %v", elapsed)
	}
}

func TestSendRejectsInvalidFromBeforeDialing(t *testing.T) {
	c := New(config.SMTP{Host: "127.0.0.1", Port: 1, From: "nope"})
	dialed := false
	c.DialContext = func(context.Context, string, string) (net.Conn, error) {
		dialed = true
		return nil, errors.New("unexpected dial")
	}
	err := c.Send(context.Background(), sampleMail)
	if err == nil || !strings.Contains(err.Error(), "invalid SMTP_FROM") {
		t.Fatalf("err = %v, want an invalid SMTP_FROM error", err)
	}
	if dialed {
		t.Fatal("a bad SMTP_FROM must fail before any network activity")
	}
}

func TestIsLoopback(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "LOCALHOST": true, "127.0.0.1": true, "127.1.2.3": true, "::1": true, "[::1]": true,
		"mail.example.test": false, "10.0.0.5": false, "localhost.example": false, "": false,
	} {
		if got := isLoopback(host); got != want {
			t.Errorf("isLoopback(%q) = %v, want %v", host, got, want)
		}
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail.**

```bash
cd backend && go test ./internal/adapter/out/smtp/
```

Expected: `undefined: New`, `undefined: Client`, `undefined: ErrNoStartTLS`, `undefined: isLoopback`.

- [ ] **Step 3: Implement the transport.** Create `backend/internal/adapter/out/smtp/smtp.go`:

```go
package smtp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	netmail "net/mail"
	gosmtp "net/smtp"
	"strconv"
	"strings"
	"time"

	"calendium/backend/internal/config"
	"calendium/backend/internal/port"
)

// sendTimeout bounds one Send end to end (dial, TLS, AUTH, DATA, QUIT).
const sendTimeout = 15 * time.Second

// ErrNoStartTLS is returned when SMTP_SECURE=false and a non-loopback server
// does not offer STARTTLS: credentials and mail are never sent in clear.
var ErrNoStartTLS = errors.New("smtp: server does not offer STARTTLS; refusing to send credentials in clear")

// Client is a port.Mailer over net/smtp. Every Send dials a fresh session;
// the struct holds no connection state, so it is safe for concurrent use.
type Client struct {
	cfg config.SMTP
	// DialContext is the TCP dial seam (tests point it at a fake server).
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
	// TLSConfig serves implicit TLS and STARTTLS; ServerName defaults to
	// cfg.Host so the server certificate is verified against system roots.
	TLSConfig *tls.Config
	now       func() time.Time
}

var _ port.Mailer = (*Client)(nil)

// New builds a Client for cfg. Call only when cfg.Configured().
func New(cfg config.SMTP) *Client {
	d := &net.Dialer{Timeout: sendTimeout}
	return &Client{
		cfg:         cfg,
		DialContext: d.DialContext,
		TLSConfig:   &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12},
		now:         time.Now,
	}
}

// Send renders msg and delivers it in one SMTP session: implicit TLS when
// Secure, else STARTTLS when advertised (mandatory off loopback); AUTH PLAIN
// (preferred) or LOGIN when the server offers one and SMTP_USER is set; then
// MAIL/RCPT/DATA/QUIT. Honours ctx and caps the whole exchange at 15 s.
func (c *Client) Send(ctx context.Context, msg port.Email) error {
	from, err := netmail.ParseAddress(c.cfg.From)
	if err != nil {
		return fmt.Errorf("smtp: invalid SMTP_FROM %q: %w", c.cfg.From, err)
	}
	msgID, err := newMessageID(from.Address)
	if err != nil {
		return err
	}
	m, err := buildMessage(msg, from, c.now(), msgID)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	addr := net.JoinHostPort(c.cfg.Host, strconv.Itoa(c.cfg.Port))
	conn, err := c.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp: dial %s: %w", addr, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	// Cancellation mid-dialogue closes the socket, which unblocks net/smtp.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	if c.cfg.Secure {
		conn = tls.Client(conn, c.TLSConfig)
	}
	cl, err := gosmtp.NewClient(conn, c.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp: greeting from %s: %w", addr, err)
	}
	defer func() { _ = cl.Close() }()

	if !c.cfg.Secure {
		if ok, _ := cl.Extension("STARTTLS"); ok {
			if err := cl.StartTLS(c.TLSConfig); err != nil {
				return fmt.Errorf("smtp: starttls: %w", err)
			}
		} else if !isLoopback(c.cfg.Host) {
			return ErrNoStartTLS
		}
	}
	if c.cfg.User != "" {
		if ok, mechs := cl.Extension("AUTH"); ok {
			auth, err := pickAuth(mechs, c.cfg)
			if err != nil {
				return err
			}
			if err := cl.Auth(auth); err != nil {
				return fmt.Errorf("smtp: auth: %w", err)
			}
		}
	}
	if err := cl.Mail(m.from); err != nil {
		return fmt.Errorf("smtp: MAIL FROM: %w", err)
	}
	for _, r := range m.rcpts {
		if err := cl.Rcpt(r); err != nil {
			return fmt.Errorf("smtp: RCPT TO %s: %w", r, err)
		}
	}
	w, err := cl.Data()
	if err != nil {
		return fmt.Errorf("smtp: DATA: %w", err)
	}
	if _, err := w.Write(m.raw); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp: write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: end of data: %w", err)
	}
	if err := cl.Quit(); err != nil {
		return fmt.Errorf("smtp: QUIT: %w", err)
	}
	return nil
}

// pickAuth prefers PLAIN, falls back to LOGIN, and refuses anything else.
// net/smtp's PlainAuth itself refuses an unencrypted non-localhost session.
func pickAuth(mechs string, cfg config.SMTP) (gosmtp.Auth, error) {
	switch {
	case strings.Contains(mechs, "PLAIN"):
		return gosmtp.PlainAuth("", cfg.User, cfg.Pass, cfg.Host), nil
	case strings.Contains(mechs, "LOGIN"):
		return loginAuth{user: cfg.User, pass: cfg.Pass}, nil
	}
	return nil, fmt.Errorf("smtp: server offers no supported AUTH mechanism (%q)", mechs)
}

// loginAuth is the AUTH LOGIN mechanism (username/password answered to two
// base64 challenges), still the only mechanism some providers offer. Like
// PlainAuth it refuses to run on an unencrypted connection to a non-loopback
// host.
type loginAuth struct{ user, pass string }

func (a loginAuth) Start(server *gosmtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && !isLoopback(server.Name) {
		return "", nil, errors.New("smtp: LOGIN auth refused on an unencrypted connection")
	}
	return "LOGIN", nil, nil
}

func (a loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(fromServer))) {
	case "username:":
		return []byte(a.user), nil
	case "password:":
		return []byte(a.pass), nil
	}
	return nil, fmt.Errorf("smtp: unexpected LOGIN challenge %q", fromServer)
}

// isLoopback reports whether host names the local machine (Mailpit, a dev
// relay): the one case where plaintext SMTP is acceptable.
func isLoopback(host string) bool {
	h := strings.Trim(strings.TrimSpace(host), "[]")
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
```

- [ ] **Step 4: Run the adapter tests, vet and lint.**

```bash
cd backend && go test ./internal/adapter/out/smtp/ -count=1 && go vet ./internal/adapter/out/smtp/ && golangci-lint run ./internal/adapter/out/smtp/...
```

Expected: `ok  	calendium/backend/internal/adapter/out/smtp` and no lint findings.

- [ ] **Step 5: Commit.**

```bash
git add backend/internal/adapter/out/smtp/smtp.go backend/internal/adapter/out/smtp/smtp_test.go
git commit -m "feat(smtp): stdlib SMTP transport with implicit TLS, STARTTLS, AUTH PLAIN/LOGIN and a 15s deadline" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 4: Team invitations — mailbox → SMTP → copyable link

**Files:**
- Modify: `backend/internal/domain/team.go` (add `InvitationDelivery`; two fields on `TeamInvitation`)
- Modify: `backend/internal/service/team.go` (`TeamServiceDeps.Mailer`, templates, `Invite`, `senderAccount`, `inviterIdentity`, `renderInviteEmail`; drop `buildInviteEmail` and the `html` import)
- Modify: `backend/internal/service/fakes_test.go` (add `fakeMailer`)
- Test: `backend/internal/service/team_test.go` (fixture gains a mailer; replace `TestTeamInviteRequiresConnectedAccount`; four new tests)

**Interfaces:**
- Consumes: `port.Mailer`, `port.Email` (Task 2).
- Produces: `domain.InvitationDelivery` with consts `domain.DeliveryMailbox = "mailbox"`, `domain.DeliverySMTP = "smtp"`, `domain.DeliveryLink = "link"`; `domain.TeamInvitation.Delivery InvitationDelivery \`json:"delivery,omitempty"\``, `domain.TeamInvitation.InviteURL string \`json:"inviteUrl,omitempty"\``; `service.TeamServiceDeps.Mailer port.Mailer`; test fake `fakeMailer{sent []port.Email; sendErr error}` + `newMailer()`; fixture `newTeamFixtureWithMailer(t, selfHost, mailer *fakeMailer)`.

- [ ] **Step 1: Add the domain fields.** In `backend/internal/domain/team.go`, before `type TeamInvitation struct` add:

```go
// InvitationDelivery reports how an invitation was delivered: through the
// inviter's connected mailbox, the instance's SMTP sender, or — when neither
// exists — a link the inviter shares by hand. Response-only; never stored.
type InvitationDelivery string

const (
	DeliveryMailbox InvitationDelivery = "mailbox"
	DeliverySMTP    InvitationDelivery = "smtp"
	DeliveryLink    InvitationDelivery = "link"
)
```

and inside `TeamInvitation`, after `CreatedAt time.Time \`json:"createdAt"\``, add:

```go
	// Delivery and InviteURL are response-only (set by TeamService.Invite,
	// never persisted). InviteURL is populated only for DeliveryLink, where
	// the inviter must share the accept link themselves.
	Delivery  InvitationDelivery `json:"delivery,omitempty"`
	InviteURL string             `json:"inviteUrl,omitempty"`
```

- [ ] **Step 2: Add the fake mailer.** In `backend/internal/service/fakes_test.go`, after the `var _ port.MailProvider = (*fakeMailProvider)(nil)` line (end of the mail-provider section), add:

```go
// --- instance mailer (piece 2) -----------------------------------------------

// fakeMailer records every port.Mailer.Send and returns a programmable error.
type fakeMailer struct {
	sent    []port.Email
	sendErr error
}

func newMailer() *fakeMailer { return &fakeMailer{} }

func (m *fakeMailer) Send(_ context.Context, msg port.Email) error {
	m.sent = append(m.sent, msg)
	return m.sendErr
}

var _ port.Mailer = (*fakeMailer)(nil)
```

- [ ] **Step 3: Write the failing service tests.** In `backend/internal/service/team_test.go`:

(a) add `mailer *fakeMailer` to `teamFixture` (after `provider *fakeMailProvider`), and replace `func newTeamFixture(t *testing.T, selfHost bool) *teamFixture { … }` with:

```go
func newTeamFixture(t *testing.T, selfHost bool) *teamFixture {
	return newTeamFixtureWithMailer(t, selfHost, nil)
}

// newTeamFixtureWithMailer wires an optional instance Mailer (nil = SMTP
// unset, the default self-host shape). A nil *fakeMailer must become a nil
// port.Mailer, never a typed nil, so the service's nil check holds.
func newTeamFixtureWithMailer(t *testing.T, selfHost bool, mailer *fakeMailer) *teamFixture {
	t.Helper()
	f := &teamFixture{
		teams:    newTeamRepo(),
		invites:  newTeamInvitationRepo(),
		users:    newUserRepo(),
		accounts: newAccountRepo(),
		subs:     newSubscriptionRepo(),
		provider: newMailProvider(),
		mailer:   mailer,
		tx:       newTxRunner(),
		clock:    newClock(time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)),
	}
	var m port.Mailer
	if mailer != nil {
		m = mailer
	}
	f.svc = NewTeamService(TeamServiceDeps{
		Teams:       f.teams,
		Invitations: f.invites,
		Users:       f.users,
		Accounts:    f.accounts,
		Mail:        map[domain.Provider]port.MailProvider{domain.ProviderGoogle: f.provider},
		OAuth:       map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Mailer:      m,
		Subs:        f.subs,
		Tx:          f.tx,
		Clock:       f.clock,
		SelfHost:    selfHost,
		AppBaseURL:  "https://app.calendium.test",
	})
	return f
}
```

(b) delete `TestTeamInviteRequiresConnectedAccount` entirely and add in its place:

```go
// TestTeamInviteFallsBackToLinkWithoutSender replaces the old
// TestTeamInviteRequiresConnectedAccount: with neither a connected mailbox
// nor an instance Mailer the invitation is still created and the response
// carries the accept link for the inviter to share by hand.
func TestTeamInviteFallsBackToLinkWithoutSender(t *testing.T) {
	f := newTeamFixture(t, true)
	f.seedTeam(t, "t1", "owner")
	ctx := context.Background()

	inv, err := f.svc.Invite(ctx, "owner", "t1", "x@example.com", domain.TeamRoleMember)
	if err != nil {
		t.Fatalf("Invite without any sender: %v", err)
	}
	if inv.Delivery != domain.DeliveryLink {
		t.Fatalf("Delivery = %q, want link", inv.Delivery)
	}
	const prefix = "https://app.calendium.test/invite/"
	if !strings.HasPrefix(inv.InviteURL, prefix) {
		t.Fatalf("InviteURL = %q, want prefix %q", inv.InviteURL, prefix)
	}
	raw := strings.TrimPrefix(inv.InviteURL, prefix)
	if len(raw) != 64 {
		t.Fatalf("token in link has length %d, want 64 hex chars", len(raw))
	}
	if inv.TokenHash != hashInviteToken(raw) {
		t.Fatal("InviteURL token does not hash to the stored TokenHash")
	}
	if len(f.provider.sent) != 0 {
		t.Fatalf("link fallback sent %d mailbox emails, want 0", len(f.provider.sent))
	}
	stored, _ := f.invites.ListByTeam(ctx, "t1")
	if len(stored) != 1 || stored[0].Status != domain.InvitePending {
		t.Fatalf("stored = %+v, want one pending invitation", stored)
	}
	// The link is response-only: the raw token is never persisted.
	if _, err := f.invites.GetByTokenHash(ctx, raw); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("raw token must not be stored")
	}
}

func TestTeamInviteUsesSMTPWhenNoMailbox(t *testing.T) {
	mailer := newMailer()
	f := newTeamFixtureWithMailer(t, true, mailer)
	f.seedTeam(t, "t1", "owner")
	name := "Olive Owner"
	if _, err := f.users.Upsert(context.Background(), domain.User{ID: "owner", Email: "olive@acme.com", Name: &name}); err != nil {
		t.Fatal(err)
	}

	inv, err := f.svc.Invite(context.Background(), "owner", "t1", "New@Example.com", domain.TeamRoleMember)
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if inv.Delivery != domain.DeliverySMTP || inv.InviteURL != "" {
		t.Fatalf("inv = %+v, want smtp delivery without an inviteUrl", inv)
	}
	if len(mailer.sent) != 1 {
		t.Fatalf("mailer sends = %d, want 1", len(mailer.sent))
	}
	if len(f.provider.sent) != 0 {
		t.Fatal("the mailbox provider must not be used without a connected account")
	}
	m := mailer.sent[0]
	if len(m.To) != 1 || m.To[0] != "new@example.com" {
		t.Fatalf("To = %v, want the canonical invitee", m.To)
	}
	if m.ReplyTo != "olive@acme.com" {
		t.Fatalf("ReplyTo = %q, want the inviter's email", m.ReplyTo)
	}
	if m.Subject != "Olive Owner invited you to Team t1 on Calendium" {
		t.Fatalf("Subject = %q", m.Subject)
	}
	if !strings.Contains(m.Text, `Olive Owner has invited you to join the team "Team t1" on Calendium.`) {
		t.Fatalf("Text = %q, want the shared wording", m.Text)
	}
	if !strings.Contains(m.Text, "https://app.calendium.test/invite/") || !strings.Contains(m.HTML, "https://app.calendium.test/invite/") {
		t.Fatal("both bodies must carry the invite link")
	}
	if !strings.Contains(m.Text, "expires in 14 days") {
		t.Fatalf("Text = %q, want the expiry footer", m.Text)
	}
}

func TestTeamInvitePrefersMailboxOverSMTP(t *testing.T) {
	mailer := newMailer()
	f := newTeamFixtureWithMailer(t, true, mailer)
	f.seedTeam(t, "t1", "owner")
	acct := f.seedSendAccount(t, "owner")

	inv, raw := f.invite(t, "owner", "t1", "x@example.com", domain.TeamRoleMember)
	if inv.Delivery != domain.DeliveryMailbox || inv.InviteURL != "" {
		t.Fatalf("inv = %+v, want mailbox delivery", inv)
	}
	if len(mailer.sent) != 0 {
		t.Fatalf("SMTP used although a mailbox exists: %d sends", len(mailer.sent))
	}
	if f.provider.sent[0].From.Email != acct.Email {
		t.Fatalf("From = %q, want the inviter's mailbox %q", f.provider.sent[0].From.Email, acct.Email)
	}
	if !strings.Contains(f.provider.sent[0].BodyText, "/invite/"+raw) {
		t.Fatal("mailbox email must carry the invite link")
	}
}

func TestTeamInviteSMTPFailureRevokesInvitation(t *testing.T) {
	mailer := newMailer()
	mailer.sendErr = errors.New("smtp exploded")
	f := newTeamFixtureWithMailer(t, true, mailer)
	f.seedTeam(t, "t1", "owner")
	ctx := context.Background()

	_, err := f.svc.Invite(ctx, "owner", "t1", "x@example.com", domain.TeamRoleMember)
	if !errors.Is(err, mailer.sendErr) {
		t.Fatalf("err = %v, want wrapped %v", err, mailer.sendErr)
	}
	if !strings.HasPrefix(err.Error(), "sending invitation email: ") {
		t.Fatalf("err text = %q", err.Error())
	}
	invs, _ := f.invites.ListByTeam(ctx, "t1")
	if len(invs) != 1 || invs[0].Status != domain.InviteRevoked {
		t.Fatalf("stored = %+v, want one revoked invitation (rollback frees the pending-unique index)", invs)
	}
}

func TestTeamInviteHTMLEscapesTeamName(t *testing.T) {
	mailer := newMailer()
	f := newTeamFixtureWithMailer(t, true, mailer)
	team := f.seedTeam(t, "t1", "owner")
	team.Name = `Ops <script>alert(1)</script> & "Co"`
	if err := f.teams.Update(context.Background(), team); err != nil {
		t.Fatal(err)
	}

	if _, err := f.svc.Invite(context.Background(), "owner", "t1", "x@example.com", domain.TeamRoleMember); err != nil {
		t.Fatal(err)
	}
	got := mailer.sent[0]
	if strings.Contains(got.HTML, "<script>") {
		t.Fatalf("HTML body carries raw markup: %q", got.HTML)
	}
	if !strings.Contains(got.HTML, "&lt;script&gt;") {
		t.Fatalf("HTML body = %q, want escaped markup", got.HTML)
	}
	if !strings.Contains(got.Text, `Ops <script>alert(1)</script> & "Co"`) {
		t.Fatal("text body must not be HTML-escaped")
	}
}
```

- [ ] **Step 4: Run the tests and watch them fail.**

```bash
cd backend && go test ./internal/service/ -run 'TestTeamInvite'
```

Expected: `unknown field Mailer in struct literal of type TeamServiceDeps`, `f.mailer undefined`.

- [ ] **Step 5: Implement the fallback chain.** In `backend/internal/service/team.go`:

(a) replace the import block with:

```go
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	htmltemplate "html/template"
	netmail "net/mail"
	"strings"
	texttemplate "text/template"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)
```

(b) in `TeamServiceDeps`, after `OAuth       map[domain.Provider]port.OAuthGateway`, add:

```go
	// Mailer is the instance's own SMTP sender, used for invitations when the
	// inviter has no connected mailbox. nil when SMTP_HOST is unset: the
	// invitation is then returned with a copyable link instead.
	Mailer port.Mailer
```

(c) in `type TeamService struct` add `mailer      port.Mailer` after `mail        map[domain.Provider]port.MailProvider`, and in `NewTeamService` add `mailer:      d.Mailer,` after `mail:        d.Mail,`. Update the TeamService doc comment's last two sentences to: `Invitation emails go out through the inviter's own connected account when one exists, else through the instance's SMTP sender, else the response carries a link the inviter shares by hand.`

(d) after `var _ port.TeamService = (*TeamService)(nil)` add:

```go
// Invitation wording mirrors apps/web/lib/email/templates/team-invitation.ts
// so an invite reads the same whichever sender delivered it. html/template
// escapes the inviter name and team name contextually; the text template
// keeps them raw.
var (
	inviteTextTmpl = texttemplate.Must(texttemplate.New("invite-text").Parse(
		"{{.Inviter}} has invited you to join the team \"{{.Team}}\" on Calendium.\n\n" +
			"Accept the invitation: {{.Link}}\n\n" +
			"The link expires in 14 days. If you weren't expecting this, you can safely ignore this email.\n"))
	inviteHTMLTmpl = htmltemplate.Must(htmltemplate.New("invite-html").Parse(
		`<p>{{.Inviter}} has invited you to join the team <strong>{{.Team}}</strong> on Calendium.</p>` +
			`<p><a href="{{.Link}}">Accept the invitation</a></p>` +
			`<p>The link expires in 14 days. If you weren&rsquo;t expecting this, you can safely ignore this email.</p>`))
)

type inviteEmailData struct{ Inviter, Team, Link string }
```

(e) replace the body of `Invite` from the line `acct, provider, err := s.senderAccount(ctx, userID)` through the final `return inv, nil` with:

```go
	// Pick the sender BEFORE persisting anything so a broken mailbox never
	// leaves a dangling pending invitation behind: (a) the inviter's own
	// connected mailbox, (b) the instance SMTP sender, (c) a link the inviter
	// shares by hand. Cloud always has (b), so (c) only happens on self-host.
	acct, provider, hasMailbox, err := s.senderAccount(ctx, userID)
	if err != nil {
		return domain.TeamInvitation{}, err
	}
	delivery := domain.DeliveryLink
	var accessToken string
	switch {
	case hasMailbox:
		delivery = domain.DeliveryMailbox
		accessToken, err = s.tokens.accessToken(ctx, acct)
		if err != nil {
			return domain.TeamInvitation{}, err
		}
	case s.mailer != nil:
		delivery = domain.DeliverySMTP
	}
	now := s.clock.Now()
	raw := randomToken(32)
	inv, err := s.invitations.Create(ctx, domain.TeamInvitation{
		ID:        newID(),
		TeamID:    teamID,
		Email:     canonical,
		Role:      role,
		InvitedBy: userID,
		Status:    domain.InvitePending,
		TokenHash: hashInviteToken(raw),
		ExpiresAt: now.Add(inviteTTL),
		CreatedAt: now,
	})
	if err != nil {
		return domain.TeamInvitation{}, err
	}
	link := s.appBaseURL + "/invite/" + raw
	inviter, replyTo := s.inviterIdentity(ctx, userID, acct)
	subject, htmlBody, textBody, err := renderInviteEmail(inviter, team.Name, link)
	if err != nil {
		return domain.TeamInvitation{}, err
	}
	switch delivery {
	case domain.DeliveryMailbox:
		_, err = provider.Send(ctx, accessToken, port.OutgoingMessage{
			From:     domain.EmailAddress{Email: acct.Email},
			To:       []domain.EmailAddress{{Email: canonical}},
			Subject:  subject,
			BodyHTML: htmlBody,
			BodyText: textBody,
		})
	case domain.DeliverySMTP:
		err = s.mailer.Send(ctx, port.Email{
			To:      []string{canonical},
			ReplyTo: replyTo,
			Subject: subject,
			Text:    textBody,
			HTML:    htmlBody,
		})
	case domain.DeliveryLink:
		inv.InviteURL = link
	}
	if err != nil {
		// Best-effort rollback so the pending-unique index does not block
		// a retry after a transient send failure.
		inv.Status = domain.InviteRevoked
		_ = s.invitations.Update(ctx, inv)
		return domain.TeamInvitation{}, fmt.Errorf("sending invitation email: %w", err)
	}
	inv.Delivery = delivery
	return inv, nil
```

(f) replace `senderAccount` and `buildInviteEmail` with:

```go
// senderAccount picks the inviter's first active connected account that has a
// configured mail provider. found=false (with a nil error) means the inviter
// has no usable mailbox and the caller falls back to SMTP or a link.
func (s *TeamService) senderAccount(ctx context.Context, userID string) (acct domain.ConnectedAccount, provider port.MailProvider, found bool, err error) {
	accounts, err := s.accounts.ListByUser(ctx, userID)
	if err != nil {
		return domain.ConnectedAccount{}, nil, false, err
	}
	for _, a := range accounts {
		if a.Status != domain.AccountActive {
			continue
		}
		if p, ok := s.mail[a.Provider]; ok {
			return a, p, true, nil
		}
	}
	return domain.ConnectedAccount{}, nil, false, nil
}

// inviterIdentity resolves the display name used in the invitation and the
// Reply-To for SMTP delivery: the user row's name (else its email), with the
// connected mailbox address as the fallback when the user row is missing.
func (s *TeamService) inviterIdentity(ctx context.Context, inviterID string, acct domain.ConnectedAccount) (name, replyTo string) {
	name, replyTo = acct.Email, acct.Email
	if u, err := s.users.GetByID(ctx, inviterID); err == nil {
		if u.Email != "" {
			name, replyTo = u.Email, u.Email
		}
		if u.Name != nil && strings.TrimSpace(*u.Name) != "" {
			name = strings.TrimSpace(*u.Name)
		}
	}
	if name == "" {
		name = "A teammate"
	}
	return name, replyTo
}

// renderInviteEmail renders the subject and both bodies from the shared
// templates.
func renderInviteEmail(inviter, team, link string) (subject, htmlBody, textBody string, err error) {
	data := inviteEmailData{Inviter: inviter, Team: team, Link: link}
	var textOut, htmlOut strings.Builder
	if err := inviteTextTmpl.Execute(&textOut, data); err != nil {
		return "", "", "", fmt.Errorf("rendering invitation text: %w", err)
	}
	if err := inviteHTMLTmpl.Execute(&htmlOut, data); err != nil {
		return "", "", "", fmt.Errorf("rendering invitation html: %w", err)
	}
	return fmt.Sprintf("%s invited you to %s on Calendium", inviter, team), htmlOut.String(), textOut.String(), nil
}
```

- [ ] **Step 6: Run the service tests (the pre-existing invite tests must still pass: happy path, duplicate conflict, mailbox send failure, accept).**

```bash
cd backend && go test ./internal/service/ -run 'TestTeam' -count=1 && go vet ./internal/domain/ ./internal/service/
```

Expected: `ok  	calendium/backend/internal/service`.

- [ ] **Step 7: Commit.**

```bash
git add backend/internal/domain/team.go backend/internal/service/team.go backend/internal/service/team_test.go backend/internal/service/fakes_test.go
git commit -m "feat(teams): invitations fall back from the inviter's mailbox to the instance SMTP sender to a copyable link" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 5: HTTP — CORS dev-origin gate, `features.email`, composition roots

**Files:**
- Modify: `backend/internal/adapter/in/httpapi/middleware.go` (`corsMiddleware`, `originAllowed`)
- Modify: `backend/internal/adapter/in/httpapi/httpapi.go` (`Deps.AllowDevOrigins`; the `corsMiddleware` call in `New`)
- Modify: `backend/internal/adapter/in/httpapi/instance.go` (`InstanceFeatures.Email`)
- Modify: `backend/cmd/api/main.go`, `backend/cmd/worker/main.go`
- Test: `backend/internal/adapter/in/httpapi/middleware_test.go` (`TestCORS` table + call sites; new `TestCORSDepsAllowDevOrigins`), `backend/internal/adapter/in/httpapi/instance_test.go`

**Interfaces:**
- Consumes: `config.SMTP.Configured()`, `Config.ValidateCloudEmail()`, `cfg.HTTP.AllowDevOrigins` (Task 1); `smtp.New` (Task 3); `TeamServiceDeps.Mailer` (Task 4).
- Produces: `httpapi.Deps.AllowDevOrigins bool`; `func corsMiddleware(next http.Handler, allowedOrigins []string, allowDevOrigins bool) http.Handler`; `httpapi.InstanceFeatures.Email bool \`json:"email"\``.

- [ ] **Step 1: Write the failing middleware and instance tests.** In `backend/internal/adapter/in/httpapi/middleware_test.go`, inside `TestCORS` replace the table definition (from `tests := []struct {` through the closing `}` of the slice literal) with:

```go
	tests := []struct {
		name          string
		origin        string
		allowDev      bool
		wantOriginHdr string // expected Access-Control-Allow-Origin; "" = not set
	}{
		{name: "localhost reflected when ALLOW_DEV_ORIGINS", origin: "http://localhost:5173", allowDev: true, wantOriginHdr: "http://localhost:5173"},
		{name: "127.0.0.1 reflected when ALLOW_DEV_ORIGINS", origin: "http://127.0.0.1:3000", allowDev: true, wantOriginHdr: "http://127.0.0.1:3000"},
		{name: "localhost NOT reflected without ALLOW_DEV_ORIGINS", origin: "http://localhost:3000", allowDev: false, wantOriginHdr: ""},
		{name: "127.0.0.1 NOT reflected without ALLOW_DEV_ORIGINS", origin: "http://127.0.0.1:3000", allowDev: false, wantOriginHdr: ""},
		{name: "::1 NOT reflected without ALLOW_DEV_ORIGINS", origin: "http://[::1]:3000", allowDev: false, wantOriginHdr: ""},
		{name: "wails://wails always reflected", origin: "wails://wails", allowDev: false, wantOriginHdr: "wails://wails"},
		{name: "wails://wails.localhost always reflected", origin: "wails://wails.localhost", allowDev: false, wantOriginHdr: "wails://wails.localhost"},
		{name: "http://wails.localhost always reflected", origin: "http://wails.localhost", allowDev: false, wantOriginHdr: "http://wails.localhost"},
		{name: "https://wails.localhost:4567 always reflected", origin: "https://wails.localhost:4567", allowDev: false, wantOriginHdr: "https://wails.localhost:4567"},
		{name: "explicit allowlist match without dev origins", origin: "https://app.example.com", allowDev: false, wantOriginHdr: "https://app.example.com"},
		{name: "explicit allowlist trailing slash trimmed for matching", origin: "https://app.example.com/", allowDev: true, wantOriginHdr: "https://app.example.com/"},
		{name: "disallowed origin not reflected", origin: "https://evil.example.com", allowDev: true, wantOriginHdr: ""},
		{name: "localhost lookalike not reflected", origin: "http://localhost.evil.example", allowDev: true, wantOriginHdr: ""},
		{name: "no origin header", origin: "", allowDev: true, wantOriginHdr: ""},
	}
```

change the table loop's `handler := corsMiddleware(probe, []string{"https://app.example.com"})` to `handler := corsMiddleware(probe, []string{"https://app.example.com"}, tt.allowDev)`, and in the two preflight subtests (`preflight with request method returns 204 without running next`, `OPTIONS without request-method header falls through to next`) change `corsMiddleware(probe, []string{"https://app.example.com"})` to `corsMiddleware(probe, []string{"https://app.example.com"}, true)`. Then append after `TestCORS`:

```go
// TestCORSDepsAllowDevOrigins proves New() threads Deps.AllowDevOrigins into
// the CORS layer: the same localhost origin flips between reflected and
// ignored on the public /healthz route.
func TestCORSDepsAllowDevOrigins(t *testing.T) {
	for _, allowDev := range []bool{false, true} {
		h := newHarness(t)
		h.deps.AllowDevOrigins = allowDev
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.Header.Set("Origin", "http://localhost:3000")
		rec := httptest.NewRecorder()
		h.handler().ServeHTTP(rec, req)
		got := rec.Header().Get("Access-Control-Allow-Origin")
		if allowDev && got != "http://localhost:3000" {
			t.Fatalf("AllowDevOrigins=true: ACAO = %q, want the origin reflected", got)
		}
		if !allowDev && got != "" {
			t.Fatalf("AllowDevOrigins=false: ACAO = %q, want unset", got)
		}
	}
}
```

In `backend/internal/adapter/in/httpapi/instance_test.go`, inside `TestHandleInstance` add `Email:     true,` after `Push:      false,` in the `Features:` literal, and change the features key loop to `for _, k := range []string{"billing", "google", "microsoft", "ai", "push", "email"} {`. Then append:

```go
// TestHandleInstanceFeaturesEmailFalseIsExplicit: features.email is a
// boolean that is always present (clients gate the forgot-password form on
// `=== false`), so an unconfigured SMTP must serialize as false, not vanish.
func TestHandleInstanceFeaturesEmailFalseIsExplicit(t *testing.T) {
	h := New(Deps{Instance: InstanceInfo{Features: InstanceFeatures{Email: false}}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	req := httptest.NewRequest(http.MethodGet, "/v1/instance", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var features map[string]json.RawMessage
	if err := json.Unmarshal(raw["features"], &features); err != nil {
		t.Fatalf("decode features: %v", err)
	}
	if string(features["email"]) != "false" {
		t.Fatalf("features.email = %s, want false", features["email"])
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail.**

```bash
cd backend && go test ./internal/adapter/in/httpapi/ -run 'TestCORS|TestHandleInstance'
```

Expected: `too many arguments in call to corsMiddleware`, `h.deps.AllowDevOrigins undefined`, `unknown field Email in struct literal`.

- [ ] **Step 3: Implement the HTTP changes.**

(a) `backend/internal/adapter/in/httpapi/middleware.go` — replace `corsMiddleware` and `originAllowed` with:

```go
// corsMiddleware reflects an allowed Origin so browser clients can call the
// API cross-origin. Always reflected: the packaged Wails WebView origins
// (production desktop clients) and every origin in allowedOrigins
// (CORS_ALLOWED_ORIGINS + PUBLIC_WEB_URL + BETTER_AUTH_URL). Reflected only
// when allowDevOrigins (ALLOW_DEV_ORIGINS=true): http(s)://localhost,
// 127.0.0.1 and ::1 on any port — the dev servers. Credentials are allowed
// (bearer JWTs); preflights get a 204.
func corsMiddleware(next http.Handler, allowedOrigins []string, allowDevOrigins bool) http.Handler {
	allow := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
			allow[o] = struct{}{}
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && originAllowed(origin, allow, allowDevOrigins) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func originAllowed(origin string, allow map[string]struct{}, allowDevOrigins bool) bool {
	if isWailsOrigin(origin) {
		return true
	}
	if allowDevOrigins && isLocalDevOrigin(origin) {
		return true
	}
	_, ok := allow[strings.TrimRight(origin, "/")]
	return ok
}
```

(b) `backend/internal/adapter/in/httpapi/httpapi.go` — in `Deps`, after `CORSAllowedOrigins []string`, add:

```go
	// AllowDevOrigins (ALLOW_DEV_ORIGINS) reflects http(s)://localhost and
	// 127.0.0.1 origins in CORS; the Wails origins and CORSAllowedOrigins
	// are reflected regardless. The run-the-binary env template turns it
	// on; production leaves it off.
	AllowDevOrigins bool
```

and change `h = corsMiddleware(h, deps.CORSAllowedOrigins)` to `h = corsMiddleware(h, deps.CORSAllowedOrigins, deps.AllowDevOrigins)`. Update the `New` doc comment's parenthetical to `(Wails + CORS_ALLOWED_ORIGINS always, localhost dev only with ALLOW_DEV_ORIGINS)`.

(c) `backend/internal/adapter/in/httpapi/instance.go` — in `InstanceFeatures`, after `Maps bool \`json:"maps"\``, add:

```go
	// Email reports a configured SMTP sender (SMTP_HOST). false means the
	// web forgot-password page shows the administrator reset instructions
	// and team invitations fall back to copyable links.
	Email bool `json:"email"`
```

- [ ] **Step 4: Run the httpapi tests.**

```bash
cd backend && go test ./internal/adapter/in/httpapi/ -count=1
```

Expected: `ok  	calendium/backend/internal/adapter/in/httpapi`.

- [ ] **Step 5: Wire the composition roots.** In `backend/cmd/api/main.go`:

(a) add `"calendium/backend/internal/adapter/out/smtp"` to the imports (alphabetically after `pgbus`/`postgres`/`push`, before `stripeapi`/`paddle`);

(b) directly after `cfg, err := config.FromEnv(); if err != nil { return err }` (and after piece 1's `ValidateCloudBilling` call if present) add:

```go
	if err := cfg.ValidateCloudEmail(); err != nil {
		return err
	}
```

(c) after `hc := &http.Client{Timeout: 30 * time.Second}` add:

```go
	// Transactional email (piece 2): the instance's own SMTP sender. nil
	// when SMTP_HOST is unset — team invitations then fall back to copyable
	// links and GET /v1/instance advertises features.email=false.
	var mailer port.Mailer
	if cfg.SMTP.Configured() {
		mailer = smtp.New(cfg.SMTP)
		logger.Info("api: email enabled", "host", cfg.SMTP.Host, "port", cfg.SMTP.Port, "secure", cfg.SMTP.Secure)
	} else {
		logger.Warn("email: disabled (no SMTP_HOST); verification off, invitations fall back to links")
	}
```

(d) in the `service.NewTeamService(service.TeamServiceDeps{ … })` literal add `Mailer:      mailer,` after `OAuth:       oauth,`;

(e) in the `Features: httpapi.InstanceFeatures{ … }` literal add `Email:     cfg.SMTP.Configured(),` after `Maps:      mapsConfigured,`;

(f) in the `logger.Info("api: optional deps", …)` call add `"mailer", mailer != nil,` after `"push", pushSender != nil,`;

(g) in the `httpapi.New(httpapi.Deps{ … })` literal add `AllowDevOrigins:    cfg.HTTP.AllowDevOrigins,` after `CORSAllowedOrigins: cfg.HTTP.CORSAllowedOrigins,`.

In `backend/cmd/worker/main.go`, directly after `cfg, err := config.FromEnv(); if err != nil { return err }` (and after piece 1's `ValidateCloudBilling` call if present) add:

```go
	if err := cfg.ValidateCloudEmail(); err != nil {
		return err
	}
	if !cfg.SMTP.Configured() {
		logger.Warn("email: disabled (no SMTP_HOST); verification off, invitations fall back to links")
	}
```

(The worker sends no transactional mail itself; it only shares the fail-fast rule and the diagnostic line.)

- [ ] **Step 6: Build, vet, run the backend suite, lint.**

```bash
cd backend && go build ./... && go vet ./... && go test ./... && golangci-lint run ./...
```

Expected: every package `ok` (Postgres packages skip without Docker), no lint findings.

- [ ] **Step 7: Commit.**

```bash
git add backend/internal/adapter/in/httpapi backend/cmd/api/main.go backend/cmd/worker/main.go
git commit -m "feat(api): gate localhost CORS on ALLOW_DEV_ORIGINS, advertise features.email, wire the SMTP mailer and the cloud email rule" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 6: `lib/auth-env.ts` — trusted origins, CORS reflection, rate-limit rules, client IP, password policy, startup warnings

**Files:**
- Create: `apps/web/lib/auth-env.ts`
- Test: `apps/web/lib/auth-env.test.ts`

**Interfaces:**
- Produces: `type EnvLike = Record<string, string | undefined>`; consts `WAILS_ORIGINS`, `APPLE_FORM_POST_ORIGIN = 'https://appleid.apple.com'`, `NATIVE_SCHEME_ORIGIN = 'calendium://'`, `CLIENT_IP_HEADER = 'x-calendium-client-ip'`, `PASSWORD_MIN_LENGTH = 10`, `PASSWORD_MAX_LENGTH = 128`; `devOriginsAllowed(env): boolean`; `envAllowedOrigins(env): string[]`; `explicitOrigins(env): string[]`; `buildTrustedOrigins(env): string[]`; `isAllowedOrigin(origin, env): boolean`; `rateLimitRules(): Record<string, { window: number; max: number }>`; `clientIpFor(headers: Headers, trustProxy: boolean): string`; `withClientIp(req: Request, trustProxy: boolean): Request`; `passwordPolicyError(password, email): { code: 'PASSWORD_TOO_SHORT' | 'PASSWORD_TOO_LONG' | 'PASSWORD_CONTAINS_EMAIL'; message: string } | null`; `startupWarnings(env): string[]`.

- [ ] **Step 1: Write the failing tests.** Create `apps/web/lib/auth-env.test.ts`:

```ts
import { describe, expect, it } from 'vitest';

import {
  APPLE_FORM_POST_ORIGIN,
  CLIENT_IP_HEADER,
  buildTrustedOrigins,
  clientIpFor,
  devOriginsAllowed,
  isAllowedOrigin,
  passwordPolicyError,
  rateLimitRules,
  startupWarnings,
  withClientIp,
} from '@/lib/auth-env';

const WAILS = ['wails://wails', 'wails://wails.localhost', 'http://wails.localhost', 'https://wails.localhost'];
const DEV = { NODE_ENV: 'development' };
const PROD = { NODE_ENV: 'production' };

describe('devOriginsAllowed', () => {
  it('is on outside production, off in production, and re-enabled by ALLOW_DEV_ORIGINS=true', () => {
    expect(devOriginsAllowed({})).toBe(true);
    expect(devOriginsAllowed(DEV)).toBe(true);
    expect(devOriginsAllowed({ NODE_ENV: 'test' })).toBe(true);
    expect(devOriginsAllowed(PROD)).toBe(false);
    expect(devOriginsAllowed({ ...PROD, ALLOW_DEV_ORIGINS: 'true' })).toBe(true);
    expect(devOriginsAllowed({ ...PROD, ALLOW_DEV_ORIGINS: '1' })).toBe(false);
  });
});

describe('buildTrustedOrigins', () => {
  it('always trusts the native scheme, Apple form_post and the four Wails origins', () => {
    const trusted = buildTrustedOrigins(PROD);
    expect(trusted).toContain('calendium://');
    expect(trusted).toContain(APPLE_FORM_POST_ORIGIN);
    for (const w of WAILS) expect(trusted).toContain(w);
  });

  it('adds localhost wildcards only when dev origins are allowed', () => {
    expect(buildTrustedOrigins(DEV)).toEqual(expect.arrayContaining(['http://localhost:*', 'http://127.0.0.1:*']));
    expect(buildTrustedOrigins(PROD)).not.toContain('http://localhost:*');
    expect(buildTrustedOrigins(PROD)).not.toContain('http://127.0.0.1:*');
    expect(buildTrustedOrigins({ ...PROD, ALLOW_DEV_ORIGINS: 'true' })).toContain('http://localhost:*');
  });

  it('adds BETTER_AUTH_URL, PUBLIC_WEB_URL and CORS_ALLOWED_ORIGINS (trimmed, de-duplicated, no trailing slash)', () => {
    const trusted = buildTrustedOrigins({
      ...PROD,
      BETTER_AUTH_URL: 'https://mail.example.com/',
      PUBLIC_WEB_URL: 'https://mail.example.com',
      CORS_ALLOWED_ORIGINS: ' https://ops.example.com , https://partner.example.com/ ,,',
    });
    expect(trusted.filter((o) => o === 'https://mail.example.com')).toHaveLength(1);
    expect(trusted).toContain('https://ops.example.com');
    expect(trusted).toContain('https://partner.example.com');
    expect(trusted).not.toContain('');
  });
});

describe('isAllowedOrigin (CORS reflection)', () => {
  it('rejects a missing or unparseable origin', () => {
    expect(isAllowedOrigin(null, DEV)).toBe(false);
    expect(isAllowedOrigin(undefined, DEV)).toBe(false);
    expect(isAllowedOrigin('', DEV)).toBe(false);
    expect(isAllowedOrigin('not a url', DEV)).toBe(false);
  });

  it('always reflects the Wails origins, including any port on wails.localhost, even in production', () => {
    for (const w of WAILS) expect(isAllowedOrigin(w, PROD)).toBe(true);
    expect(isAllowedOrigin('http://wails.localhost:1420', PROD)).toBe(true);
    expect(isAllowedOrigin('https://wails.localhost:9999', PROD)).toBe(true);
  });

  it('reflects localhost / 127.0.0.1 / ::1 only when dev origins are allowed', () => {
    for (const o of ['http://localhost:3000', 'https://localhost', 'http://127.0.0.1:8080', 'http://[::1]:3000']) {
      expect(isAllowedOrigin(o, DEV)).toBe(true);
      expect(isAllowedOrigin(o, PROD)).toBe(false);
      expect(isAllowedOrigin(o, { ...PROD, ALLOW_DEV_ORIGINS: 'true' })).toBe(true);
    }
    expect(isAllowedOrigin('ftp://localhost', DEV)).toBe(false);
    expect(isAllowedOrigin('http://localhost.evil.example', DEV)).toBe(false);
  });

  it('reflects the explicit origins in production', () => {
    const env = { ...PROD, BETTER_AUTH_URL: 'https://mail.example.com', PUBLIC_WEB_URL: 'https://app.example.com/', CORS_ALLOWED_ORIGINS: ' https://ops.example.com ' };
    expect(isAllowedOrigin('https://mail.example.com', env)).toBe(true);
    expect(isAllowedOrigin('https://app.example.com', env)).toBe(true);
    expect(isAllowedOrigin('https://ops.example.com', env)).toBe(true);
    expect(isAllowedOrigin('https://unlisted.example.com', env)).toBe(false);
  });

  it('never reflects appleid.apple.com (trusted for CSRF, not for CORS)', () => {
    expect(isAllowedOrigin(APPLE_FORM_POST_ORIGIN, DEV)).toBe(false);
    expect(isAllowedOrigin(APPLE_FORM_POST_ORIGIN, PROD)).toBe(false);
  });
});

describe('rateLimitRules', () => {
  it('matches the spec table exactly', () => {
    expect(rateLimitRules()).toEqual({
      '/sign-in/email': { window: 60, max: 5 },
      '/sign-up/email': { window: 60, max: 3 },
      '/request-password-reset': { window: 600, max: 3 },
      '/forget-password': { window: 600, max: 3 },
      '/send-verification-email': { window: 600, max: 3 },
      '/token': { window: 60, max: 60 },
    });
  });
});

describe('clientIpFor', () => {
  const headers = (xff?: string) => new Headers(xff === undefined ? {} : { 'x-forwarded-for': xff });

  it('returns empty when the header is absent or blank', () => {
    expect(clientIpFor(headers(), true)).toBe('');
    expect(clientIpFor(headers(''), false)).toBe('');
    expect(clientIpFor(headers(' , '), true)).toBe('');
  });

  it('TRUST_PROXY=false: trusts only a single-valued header (the socket fill)', () => {
    expect(clientIpFor(headers('203.0.113.9'), false)).toBe('203.0.113.9');
    expect(clientIpFor(headers('203.0.113.9, 10.0.0.1'), false)).toBe('');
  });

  it('TRUST_PROXY=true: takes the first hop and tolerates whitespace and a trailing comma', () => {
    expect(clientIpFor(headers(' 203.0.113.9 , 10.0.0.1,'), true)).toBe('203.0.113.9');
    expect(clientIpFor(headers(' 203.0.113.9 , 10.0.0.1,'), false)).toBe('');
  });
});

describe('withClientIp', () => {
  it('overwrites a forged x-calendium-client-ip and preserves method, URL and body', async () => {
    const original = new Request('https://mail.example.com/api/auth/sign-in/email', {
      method: 'POST',
      headers: { 'content-type': 'application/json', [CLIENT_IP_HEADER]: '1.2.3.4', 'x-forwarded-for': '203.0.113.9, 10.0.0.1' },
      body: JSON.stringify({ email: 'a@b.test' }),
    });
    const stamped = withClientIp(original, false);
    expect(stamped.headers.get(CLIENT_IP_HEADER)).toBe('');
    expect(stamped.method).toBe('POST');
    expect(stamped.url).toBe(original.url);
    expect(stamped.headers.get('content-type')).toBe('application/json');
    expect(await stamped.text()).toBe(JSON.stringify({ email: 'a@b.test' }));
  });

  it('stamps the first hop when the proxy is trusted', () => {
    const req = new Request('https://mail.example.com/api/auth/token', { headers: { 'x-forwarded-for': '203.0.113.9, 10.0.0.1' } });
    expect(withClientIp(req, true).headers.get(CLIENT_IP_HEADER)).toBe('203.0.113.9');
  });
});

describe('passwordPolicyError', () => {
  it('enforces 10–128 characters', () => {
    expect(passwordPolicyError('short-one', 'a@b.test')).toEqual({ code: 'PASSWORD_TOO_SHORT', message: 'Password must be at least 10 characters.' });
    expect(passwordPolicyError('x'.repeat(129), 'a@b.test')).toEqual({ code: 'PASSWORD_TOO_LONG', message: 'Password must be at most 128 characters.' });
    expect(passwordPolicyError('x'.repeat(10), 'a@b.test')).toBeNull();
    expect(passwordPolicyError('x'.repeat(128), 'a@b.test')).toBeNull();
  });

  it('rejects the email local part case-insensitively', () => {
    expect(passwordPolicyError('ADA.LOVELACE-2026!', 'Ada.Lovelace@example.test')).toEqual({
      code: 'PASSWORD_CONTAINS_EMAIL',
      message: 'Password must not contain your email address.',
    });
    expect(passwordPolicyError('correct-horse-battery', 'ada.lovelace@example.test')).toBeNull();
  });

  it('ignores local parts shorter than 3 characters and a missing email', () => {
    expect(passwordPolicyError('abcdefghijk1', 'ab@example.test')).toBeNull();
    expect(passwordPolicyError('abcdefghijk1', null)).toBeNull();
    expect(passwordPolicyError('abcdefghijk1', undefined)).toBeNull();
    expect(passwordPolicyError('abcdefghijk1', 'abc@example.test')).toEqual(expect.objectContaining({ code: 'PASSWORD_CONTAINS_EMAIL' }));
  });
});

describe('startupWarnings', () => {
  it('is silent outside production', () => {
    expect(startupWarnings(DEV)).toEqual([]);
    expect(startupWarnings({ ...DEV, ALLOW_DEV_ORIGINS: 'true' })).toEqual([]);
  });

  it('warns about TRUST_PROXY and ALLOW_DEV_ORIGINS in production', () => {
    const both = startupWarnings({ ...PROD, ALLOW_DEV_ORIGINS: 'true' });
    expect(both).toHaveLength(2);
    expect(both[0]).toMatch(/^TRUST_PROXY is not true in production/);
    expect(both[1]).toMatch(/^ALLOW_DEV_ORIGINS=true in production/);
    expect(startupWarnings({ ...PROD, TRUST_PROXY: 'true' })).toEqual([]);
  });
});
```

- [ ] **Step 2: Run the test and watch it fail.**

```bash
cd apps/web && bunx vitest run lib/auth-env.test.ts
```

Expected: `Failed to resolve import "@/lib/auth-env"`.

- [ ] **Step 3: Implement the helpers.** Create `apps/web/lib/auth-env.ts`:

```ts
/**
 * Pure, DB-free helpers behind lib/auth.ts and app/api/auth/[...all]/route.ts:
 * trusted origins, CORS reflection, rate-limit rules, client-IP resolution,
 * the password policy and startup warnings. Everything takes `env`
 * explicitly so it is unit-testable without process.env or Better Auth.
 * Mirrors the Go side (config.FromEnv + httpapi.corsMiddleware).
 */

export type EnvLike = Record<string, string | undefined>;

/**
 * Wails desktop WebView page origins. macOS/Linux serve the app from
 * `wails://wails`; Windows uses `http://wails.localhost`. They are
 * production client origins and are ALWAYS trusted — never gated on NODE_ENV.
 */
export const WAILS_ORIGINS = [
  'wails://wails',
  'wails://wails.localhost',
  'http://wails.localhost',
  'https://wails.localhost',
] as const;

/**
 * Sign in with Apple posts its callback (`response_mode=form_post`) from this
 * origin. Trusted for the CSRF/origin check, never reflected in CORS (a form
 * POST is a navigation, not a fetch).
 */
export const APPLE_FORM_POST_ORIGIN = 'https://appleid.apple.com';

/** Native deep-link scheme (mobile OAuth callbacks). */
export const NATIVE_SCHEME_ORIGIN = 'calendium://';

/**
 * The server-set header Better Auth reads the client IP from. route.ts
 * overwrites it on every request, so a caller can never choose its bucket.
 */
export const CLIENT_IP_HEADER = 'x-calendium-client-ip';

const stripSlash = (s: string) => s.trim().replace(/\/+$/, '');

/** localhost / 127.0.0.1 origins are trusted outside production, or when explicitly re-enabled. */
export function devOriginsAllowed(env: EnvLike): boolean {
  return env.NODE_ENV !== 'production' || env.ALLOW_DEV_ORIGINS === 'true';
}

/** CORS_ALLOWED_ORIGINS: comma-separated exact origins (trimmed, trailing slashes dropped). */
export function envAllowedOrigins(env: EnvLike): string[] {
  return (env.CORS_ALLOWED_ORIGINS ?? '').split(',').map(stripSlash).filter(Boolean);
}

/** Operator-configured exact origins: BETTER_AUTH_URL, PUBLIC_WEB_URL and CORS_ALLOWED_ORIGINS. */
export function explicitOrigins(env: EnvLike): string[] {
  const out: string[] = [];
  for (const raw of [env.BETTER_AUTH_URL, env.PUBLIC_WEB_URL]) {
    const origin = raw ? stripSlash(raw) : '';
    if (origin) out.push(origin);
  }
  out.push(...envAllowedOrigins(env));
  return Array.from(new Set(out));
}

/**
 * Origins Better Auth trusts for its CSRF/callback checks. Fixed: the native
 * scheme, Apple's form_post origin and the Wails origins. From env: the
 * explicit origins. Dev only: localhost / 127.0.0.1 wildcards.
 */
export function buildTrustedOrigins(env: EnvLike): string[] {
  const origins: string[] = [NATIVE_SCHEME_ORIGIN, APPLE_FORM_POST_ORIGIN, ...WAILS_ORIGINS, ...explicitOrigins(env)];
  if (devOriginsAllowed(env)) origins.push('http://localhost:*', 'http://127.0.0.1:*');
  return Array.from(new Set(origins));
}

/** http(s)://localhost | 127.0.0.1 | ::1 on any port — the dev servers. */
function isLocalhostDevOrigin(url: URL): boolean {
  if (url.protocol !== 'http:' && url.protocol !== 'https:') return false;
  return (
    url.hostname === 'localhost' ||
    url.hostname === '127.0.0.1' ||
    url.hostname === '::1' ||
    url.hostname === '[::1]'
  );
}

/**
 * Whether an `Origin` header is reflected into Access-Control-Allow-Origin
 * (credentialed CORS forbids `*`). Always: the Wails origins (any port on
 * wails.localhost) and the explicit origins. Gated on devOriginsAllowed:
 * localhost / 127.0.0.1 / ::1. Never: appleid.apple.com or anything else.
 * Mirrors the Go API's corsMiddleware so the whole stack admits the same
 * clients.
 */
export function isAllowedOrigin(origin: string | null | undefined, env: EnvLike): boolean {
  if (!origin) return false;
  if ((WAILS_ORIGINS as readonly string[]).includes(origin)) return true;
  if (explicitOrigins(env).includes(stripSlash(origin))) return true;
  try {
    const url = new URL(origin);
    if (url.hostname === 'wails.localhost' && (url.protocol === 'http:' || url.protocol === 'https:')) {
      return true;
    }
    return devOriginsAllowed(env) && isLocalhostDevOrigin(url);
  } catch {
    return false;
  }
}

export interface RateLimitRule {
  window: number;
  max: number;
}

/**
 * Per-IP Better Auth `customRules`, keyed by the exact path after /api/auth.
 * Better Auth applies them to GET too, so the jwt plugin's /token is limited;
 * /forget-password is the deprecated alias of /request-password-reset.
 */
export function rateLimitRules(): Record<string, RateLimitRule> {
  return {
    '/sign-in/email': { window: 60, max: 5 },
    '/sign-up/email': { window: 60, max: 3 },
    '/request-password-reset': { window: 600, max: 3 },
    '/forget-password': { window: 600, max: 3 },
    '/send-verification-email': { window: 600, max: 3 },
    '/token': { window: 60, max: 60 },
  };
}

/**
 * The client IP for rate limiting, from X-Forwarded-For. trustProxy=true:
 * the first hop (the proxy MUST overwrite the header — Caddy does). Else: the
 * header only when single-valued (Next.js fills it from the socket when
 * absent); a multi-hop value is untrusted and yields '' → Better Auth's
 * shared `no-trusted-ip` bucket.
 */
export function clientIpFor(headers: Headers, trustProxy: boolean): string {
  const hops = (headers.get('x-forwarded-for') ?? '')
    .split(',')
    .map((h) => h.trim())
    .filter(Boolean);
  if (hops.length === 0) return '';
  if (trustProxy) return hops[0] ?? '';
  return hops.length === 1 ? (hops[0] ?? '') : '';
}

/** A copy of `req` whose CLIENT_IP_HEADER is always the server-resolved value (forged values are overwritten). */
export function withClientIp(req: Request, trustProxy: boolean): Request {
  const headers = new Headers(req.headers);
  headers.set(CLIENT_IP_HEADER, clientIpFor(req.headers, trustProxy));
  // `duplex` is required by undici when re-wrapping a request that carries a
  // body stream; it is absent from lib.dom's RequestInit, hence the cast.
  return new Request(req, { headers, duplex: 'half' } as RequestInit);
}

export const PASSWORD_MIN_LENGTH = 10;
export const PASSWORD_MAX_LENGTH = 128;

export interface PasswordPolicyError {
  code: 'PASSWORD_TOO_SHORT' | 'PASSWORD_TOO_LONG' | 'PASSWORD_CONTAINS_EMAIL';
  message: string;
}

/**
 * Server-side password policy (mirrored client-side for instant feedback):
 * 10–128 characters and no email local part (case-insensitive). Local parts
 * shorter than 3 characters are ignored so `ab@x.test` cannot reject every
 * password containing "ab".
 */
export function passwordPolicyError(password: string, email: string | null | undefined): PasswordPolicyError | null {
  if (password.length < PASSWORD_MIN_LENGTH) {
    return { code: 'PASSWORD_TOO_SHORT', message: `Password must be at least ${PASSWORD_MIN_LENGTH} characters.` };
  }
  if (password.length > PASSWORD_MAX_LENGTH) {
    return { code: 'PASSWORD_TOO_LONG', message: `Password must be at most ${PASSWORD_MAX_LENGTH} characters.` };
  }
  const local = (email ?? '').split('@')[0]?.trim().toLowerCase() ?? '';
  if (local.length >= 3 && password.toLowerCase().includes(local)) {
    return { code: 'PASSWORD_CONTAINS_EMAIL', message: 'Password must not contain your email address.' };
  }
  return null;
}

/** Production misconfiguration warnings, logged once at boot by instrumentation.ts. */
export function startupWarnings(env: EnvLike): string[] {
  const out: string[] = [];
  if (env.NODE_ENV !== 'production') return out;
  if (env.TRUST_PROXY !== 'true') {
    out.push(
      'TRUST_PROXY is not true in production: auth rate limits are keyed on a client-controlled X-Forwarded-For. Run behind the bundled Caddy profile (which overwrites the header) with TRUST_PROXY=true — see docs/self-hosting/security.md.'
    );
  }
  if (env.ALLOW_DEV_ORIGINS === 'true') {
    out.push(
      'ALLOW_DEV_ORIGINS=true in production: http://localhost and http://127.0.0.1 origins are trusted for auth and reflected in CORS. Unset it unless you are debugging.'
    );
  }
  return out;
}
```

- [ ] **Step 4: Run the test.**

```bash
cd apps/web && bunx vitest run lib/auth-env.test.ts
```

Expected: all `auth-env.test.ts` cases pass.

- [ ] **Step 5: Commit.**

```bash
git add apps/web/lib/auth-env.ts apps/web/lib/auth-env.test.ts
git commit -m "feat(web): pure auth-env helpers for trusted origins, CORS reflection, rate-limit rules, client IP and the password policy" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 7: `lib/email/config.ts`, `instrumentation.ts`, nodemailer dependency, e2e `SELF_HOSTED`

**Files:**
- Create: `apps/web/lib/email/config.ts`, `apps/web/instrumentation.ts`
- Modify: `apps/web/package.json` (dependencies), `apps/web/playwright.config.ts` (webServer env)
- Test: `apps/web/lib/email/config.test.ts`, `apps/web/instrumentation.test.ts`

**Interfaces:**
- Consumes: `startupWarnings` (Task 6).
- Produces: `type MailConfig = { configured: false } | { configured: true; host: string; port: number; secure: boolean; user?: string; pass?: string; from: string; requireTLS: boolean }`; `readMailConfig(env: EnvLike): MailConfig` (throws on partial/invalid); `assertMailConfigForMode(env: EnvLike): MailConfig` (also throws the cloud error); `isLoopback(host: string): boolean`; `register(): Promise<void>` in `instrumentation.ts`.

- [ ] **Step 1: Add the dependency.**

```bash
cd apps/web && bun add nodemailer@^7 && bun add -d @types/nodemailer@^7
```

Expected `apps/web/package.json` diff: `"nodemailer": "^7.0.0"` (or the resolved 7.x) under `dependencies`, `"@types/nodemailer": "^7.0.0"` under `devDependencies`, and `bun.lock` updated. (If the registry has no `@types/nodemailer` 7.x yet, use `bun add -d @types/nodemailer@^6` — the `createTransport`/`sendMail` typings are the same.)

- [ ] **Step 2: Write the failing config tests.** Create `apps/web/lib/email/config.test.ts`:

```ts
import { describe, expect, it } from 'vitest';

import { assertMailConfigForMode, isLoopback, readMailConfig } from '@/lib/email/config';

const FULL = { SMTP_HOST: 'smtp.example.test', SMTP_FROM: 'Calendium <noreply@example.test>' };
const CLOUD_ERROR =
  'SMTP_HOST and SMTP_FROM are required when SELF_HOSTED=false (cloud mode); set them or run with SELF_HOSTED=true';

describe('readMailConfig', () => {
  it('is unconfigured when no SMTP_* identity variable is set', () => {
    expect(readMailConfig({})).toEqual({ configured: false });
    expect(readMailConfig({ SMTP_HOST: '', SMTP_FROM: '' })).toEqual({ configured: false });
    // The env templates ship port/secure pre-filled next to a blank host.
    expect(readMailConfig({ SMTP_PORT: '587', SMTP_SECURE: 'false' })).toEqual({ configured: false });
  });

  it('parses host+from with port 587, STARTTLS required off loopback, no auth', () => {
    expect(readMailConfig(FULL)).toEqual({
      configured: true,
      host: 'smtp.example.test',
      port: 587,
      secure: false,
      from: 'Calendium <noreply@example.test>',
      requireTLS: true,
    });
  });

  it('trims whitespace around host and from', () => {
    expect(readMailConfig({ SMTP_HOST: ' smtp.example.test ', SMTP_FROM: ' a@b.test ' })).toMatchObject({
      host: 'smtp.example.test',
      from: 'a@b.test',
    });
  });

  it('does not require TLS for a loopback host', () => {
    expect(readMailConfig({ SMTP_HOST: '127.0.0.1', SMTP_FROM: 'a@b.test' })).toMatchObject({ requireTLS: false, secure: false });
    expect(readMailConfig({ SMTP_HOST: 'localhost', SMTP_FROM: 'a@b.test' })).toMatchObject({ requireTLS: false });
  });

  it('SMTP_SECURE=true means implicit TLS and no STARTTLS requirement', () => {
    expect(readMailConfig({ ...FULL, SMTP_SECURE: 'true', SMTP_PORT: '465' })).toMatchObject({ secure: true, requireTLS: false, port: 465 });
  });

  it('carries the auth pair when both are set', () => {
    expect(readMailConfig({ ...FULL, SMTP_USER: 'apikey', SMTP_PASS: 's3cret' })).toMatchObject({ user: 'apikey', pass: 's3cret' });
    expect(readMailConfig(FULL)).not.toHaveProperty('user');
  });

  it.each([
    [{ SMTP_HOST: 'h' }, 'SMTP_* is partially configured: SMTP_FROM'],
    [{ SMTP_FROM: 'a@b.test' }, 'SMTP_* is partially configured: SMTP_HOST'],
    [{ SMTP_USER: 'u' }, 'SMTP_* is partially configured: SMTP_HOST, SMTP_FROM, SMTP_PASS'],
    [{ ...FULL, SMTP_USER: 'u' }, 'SMTP_* is partially configured: SMTP_PASS'],
    [{ ...FULL, SMTP_PASS: 'p' }, 'SMTP_* is partially configured: SMTP_USER'],
    [{ ...FULL, SMTP_PORT: '0' }, 'SMTP_PORT must be an integer between 1 and 65535, got "0"'],
    [{ ...FULL, SMTP_PORT: '65536' }, 'SMTP_PORT must be an integer between 1 and 65535, got "65536"'],
    [{ ...FULL, SMTP_PORT: 'abc' }, 'SMTP_PORT must be an integer between 1 and 65535, got "abc"'],
    [{ ...FULL, SMTP_SECURE: 'yes' }, 'SMTP_SECURE must be true or false, got "yes"'],
    [{ ...FULL, SMTP_FROM: 'not-an-address' }, 'SMTP_FROM must be an email address or "Name <addr>", got "not-an-address"'],
  ])('rejects %j with %s', (env, message) => {
    expect(() => readMailConfig(env)).toThrow(message);
  });
});

describe('assertMailConfigForMode', () => {
  it('throws in cloud mode (SELF_HOSTED unset or false) without SMTP', () => {
    expect(() => assertMailConfigForMode({})).toThrow(CLOUD_ERROR);
    expect(() => assertMailConfigForMode({ SELF_HOSTED: 'false' })).toThrow(CLOUD_ERROR);
  });

  it('returns the unconfigured shape on self-host without SMTP', () => {
    expect(assertMailConfigForMode({ SELF_HOSTED: 'true' })).toEqual({ configured: false });
  });

  it('returns the parsed config in cloud mode with SMTP', () => {
    expect(assertMailConfigForMode(FULL)).toMatchObject({ configured: true, host: 'smtp.example.test' });
  });

  it('still reports a partial block before the mode check', () => {
    expect(() => assertMailConfigForMode({ SELF_HOSTED: 'true', SMTP_HOST: 'h' })).toThrow('SMTP_* is partially configured: SMTP_FROM');
  });
});

describe('isLoopback', () => {
  it.each(['localhost', 'LOCALHOST', '127.0.0.1', '::1', '[::1]'])('%s is loopback', (h) => {
    expect(isLoopback(h)).toBe(true);
  });
  it.each(['smtp.example.test', '127.0.0.1.evil', 'localhost.example', ''])('%s is not loopback', (h) => {
    expect(isLoopback(h)).toBe(false);
  });
});
```

and `apps/web/instrumentation.test.ts`:

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { register } from './instrumentation';

const SMTP_KEYS = ['SMTP_HOST', 'SMTP_PORT', 'SMTP_USER', 'SMTP_PASS', 'SMTP_FROM', 'SMTP_SECURE'];

describe('instrumentation.register', () => {
  let warn: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    for (const k of [...SMTP_KEYS, 'SELF_HOSTED', 'TRUST_PROXY', 'ALLOW_DEV_ORIGINS']) vi.stubEnv(k, '');
    vi.stubEnv('NEXT_RUNTIME', 'nodejs');
    warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
  });

  afterEach(() => {
    vi.unstubAllEnvs();
    vi.restoreAllMocks();
  });

  it('does nothing on the edge runtime', async () => {
    vi.stubEnv('NEXT_RUNTIME', 'edge');
    await expect(register()).resolves.toBeUndefined();
    expect(warn).not.toHaveBeenCalled();
  });

  it('aborts cloud mode without SMTP, naming the variables', async () => {
    await expect(register()).rejects.toThrow('SMTP_HOST and SMTP_FROM are required when SELF_HOSTED=false (cloud mode); set them or run with SELF_HOSTED=true');
  });

  it('boots self-host without SMTP and logs the disabled line', async () => {
    vi.stubEnv('SELF_HOSTED', 'true');
    await expect(register()).resolves.toBeUndefined();
    expect(warn).toHaveBeenCalledWith('email: disabled (no SMTP_HOST); verification off, invitations fall back to links');
  });

  it('logs the production warnings', async () => {
    vi.stubEnv('SELF_HOSTED', 'true');
    vi.stubEnv('NODE_ENV', 'production');
    vi.stubEnv('ALLOW_DEV_ORIGINS', 'true');
    await register();
    const messages = warn.mock.calls.map((c) => String(c[0]));
    expect(messages.some((m) => m.startsWith('TRUST_PROXY is not true in production'))).toBe(true);
    expect(messages.some((m) => m.startsWith('ALLOW_DEV_ORIGINS=true in production'))).toBe(true);
  });
});
```

- [ ] **Step 3: Run the tests and watch them fail.**

```bash
cd apps/web && bunx vitest run lib/email/config.test.ts instrumentation.test.ts
```

Expected: `Failed to resolve import "@/lib/email/config"` and `./instrumentation`.

- [ ] **Step 4: Implement the config reader.** Create `apps/web/lib/email/config.ts`:

```ts
import type { EnvLike } from '@/lib/auth-env';

/**
 * SMTP settings for the web tier, read from the same SMTP_* variables as the
 * Go api/worker (config.FromEnv). `{ configured: false }` means no sender:
 * email verification is off, forgot-password shows the admin instructions.
 */
export type MailConfig =
  | { configured: false }
  | {
      configured: true;
      host: string;
      port: number;
      secure: boolean;
      user?: string;
      pass?: string;
      from: string;
      /** STARTTLS is mandatory: not implicit TLS and not a loopback host. */
      requireTLS: boolean;
    };

const FROM_RE = /^(?:[^<>]*<)?[^\s@<>]+@[^\s@<>]+>?$/;

/** localhost / 127.x / ::1 — the one case plaintext SMTP is acceptable (Mailpit, a local relay). */
export function isLoopback(host: string): boolean {
  const h = host.trim().toLowerCase().replace(/^\[|\]$/g, '');
  return h === 'localhost' || h === '::1' || /^127\.\d{1,3}\.\d{1,3}\.\d{1,3}$/.test(h);
}

/**
 * Parses SMTP_*. Throws on a half-set block or an invalid value so a typo
 * fails at boot with the variable named; returns `{configured:false}` when
 * none of SMTP_HOST/SMTP_FROM/SMTP_USER/SMTP_PASS is set (SMTP_PORT and
 * SMTP_SECURE alone do not count — the env templates pre-fill them).
 */
export function readMailConfig(env: EnvLike): MailConfig {
  const host = env.SMTP_HOST?.trim() ?? '';
  const from = env.SMTP_FROM?.trim() ?? '';
  const user = env.SMTP_USER ?? '';
  const pass = env.SMTP_PASS ?? '';
  if (!host && !from && !user && !pass) return { configured: false };

  const missing: string[] = [];
  if (!host) missing.push('SMTP_HOST');
  if (!from) missing.push('SMTP_FROM');
  if (!user && pass) missing.push('SMTP_USER');
  if (user && !pass) missing.push('SMTP_PASS');
  if (missing.length > 0) throw new Error(`SMTP_* is partially configured: ${missing.join(', ')}`);

  const portRaw = env.SMTP_PORT ?? '587';
  const port = /^\d+$/.test(portRaw) ? Number(portRaw) : Number.NaN;
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw new Error(`SMTP_PORT must be an integer between 1 and 65535, got "${portRaw}"`);
  }
  const secureRaw = env.SMTP_SECURE ?? 'false';
  if (secureRaw !== 'true' && secureRaw !== 'false') {
    throw new Error(`SMTP_SECURE must be true or false, got "${secureRaw}"`);
  }
  if (!FROM_RE.test(from)) {
    throw new Error(`SMTP_FROM must be an email address or "Name <addr>", got "${from}"`);
  }
  const secure = secureRaw === 'true';
  return {
    configured: true,
    host,
    port,
    secure,
    from,
    requireTLS: !secure && !isLoopback(host),
    ...(user ? { user, pass } : {}),
  };
}

/**
 * readMailConfig plus the cloud rule: with SELF_HOSTED!=true an SMTP sender
 * is mandatory (verification, reset and invitations depend on it). Called
 * from instrumentation.ts at server boot — never from `next build`.
 */
export function assertMailConfigForMode(env: EnvLike): MailConfig {
  const mail = readMailConfig(env);
  if (!mail.configured && env.SELF_HOSTED !== 'true') {
    throw new Error(
      'SMTP_HOST and SMTP_FROM are required when SELF_HOSTED=false (cloud mode); set them or run with SELF_HOSTED=true'
    );
  }
  return mail;
}
```

- [ ] **Step 5: Implement the instrumentation hook.** Create `apps/web/instrumentation.ts`:

```ts
/**
 * Next.js instrumentation hook (Node runtime only). Runs once when
 * `next start` / `next dev` boot the server — never during `next build` — so
 * a broken email/auth configuration aborts the process with the exact
 * variable named instead of failing on the first sign-up. Mirrors
 * config.FromEnv + ValidateCloudEmail on the Go side.
 */
export async function register(): Promise<void> {
  if (process.env.NEXT_RUNTIME !== 'nodejs') return;
  const { assertMailConfigForMode } = await import('@/lib/email/config');
  const { startupWarnings } = await import('@/lib/auth-env');
  const mail = assertMailConfigForMode(process.env);
  if (!mail.configured) {
    console.warn('email: disabled (no SMTP_HOST); verification off, invitations fall back to links');
  }
  for (const warning of startupWarnings(process.env)) console.warn(warning);
}
```

- [ ] **Step 6: Keep the e2e server bootable.** In `apps/web/playwright.config.ts`, inside `webServer.env`, after the `NEXT_PUBLIC_API_URL: 'http://127.0.0.1:58080',` line add:

```ts
      // instrumentation.ts (piece 2) refuses to boot cloud mode without SMTP;
      // the e2e suite runs as a self-host without email (Better Auth is
      // stubbed at the network layer anyway, see fixtures.ts).
      SELF_HOSTED: 'true',
```

- [ ] **Step 7: Run the tests.**

```bash
cd apps/web && bunx vitest run lib/email/config.test.ts instrumentation.test.ts && bunx tsc --noEmit
```

Expected: both files pass; typecheck clean.

- [ ] **Step 8: Commit.**

```bash
git add apps/web/package.json bun.lock apps/web/lib/email/config.ts apps/web/lib/email/config.test.ts apps/web/instrumentation.ts apps/web/instrumentation.test.ts apps/web/playwright.config.ts
git commit -m "feat(web): SMTP config reader, boot-time email/auth checks in instrumentation.ts, nodemailer dependency" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 8: Email rendering, templates and the nodemailer transport

**Files:**
- Create: `apps/web/lib/email/render.ts`, `apps/web/lib/email/transport.ts`, `apps/web/lib/email/templates/verify-email.ts`, `apps/web/lib/email/templates/reset-password.ts`, `apps/web/lib/email/templates/team-invitation.ts`
- Test: `apps/web/lib/email/templates.test.ts`, `apps/web/lib/email/transport.test.ts`

**Interfaces:**
- Consumes: `readMailConfig` (Task 7).
- Produces: `escapeHtml(s: string): string`; `interface EmailContent { subject; heading; intro; cta?: { label; url }; footer }`; `interface RenderedEmail { subject; html; text }`; `interface OutgoingMail { to: string; subject: string; html: string; text: string; replyTo?: string }`; `renderEmail(c: EmailContent): RenderedEmail`; `verifyEmail({ to, url }): OutgoingMail`; `resetPasswordEmail({ to, url }): OutgoingMail`; `teamInvitationEmail({ to, inviter, team, url }): OutgoingMail` (reference wording; not wired at runtime); `sendMail(mail: OutgoingMail): Promise<void>`; `isMailConfigured(): boolean`; `_resetTransportForTests(): void`. The `delete-account-confirm` template is reserved for piece 3 and is NOT created here.

- [ ] **Step 1: Write the failing template and transport tests.** Create `apps/web/lib/email/templates.test.ts`:

```ts
import { describe, expect, it } from 'vitest';

import { escapeHtml, renderEmail } from '@/lib/email/render';
import { resetPasswordEmail } from '@/lib/email/templates/reset-password';
import { teamInvitationEmail } from '@/lib/email/templates/team-invitation';
import { verifyEmail } from '@/lib/email/templates/verify-email';

describe('escapeHtml', () => {
  it('escapes the five HTML metacharacters and nothing else', () => {
    expect(escapeHtml(`<a href="x">Tom & Jerry's</a>`)).toBe('&lt;a href=&quot;x&quot;&gt;Tom &amp; Jerry&#39;s&lt;/a&gt;');
    expect(escapeHtml('café — ok')).toBe('café — ok');
  });
});

describe('renderEmail', () => {
  it('escapes every interpolation in html and keeps text raw', () => {
    const out = renderEmail({
      subject: 'S',
      heading: 'Hello <b>',
      intro: 'Intro & more',
      cta: { label: 'Go "now"', url: 'https://x.test/a?b=1&c=2' },
      footer: "Footer 'q'",
    });
    expect(out.subject).toBe('S');
    expect(out.html).toContain('Hello &lt;b&gt;');
    expect(out.html).toContain('Intro &amp; more');
    expect(out.html).toContain('href="https://x.test/a?b=1&amp;c=2"');
    expect(out.html).toContain('Go &quot;now&quot;');
    expect(out.html).toContain('Footer &#39;q&#39;');
    expect(out.html).not.toContain('<b>');
    expect(out.text).toBe("Hello <b>\n\nIntro & more\n\nGo \"now\": https://x.test/a?b=1&c=2\n\nFooter 'q'\n");
  });

  it('omits the CTA block when absent', () => {
    const out = renderEmail({ subject: 'S', heading: 'H', intro: 'I', footer: 'F' });
    expect(out.html).not.toContain('<a ');
    expect(out.text).toBe('H\n\nI\n\nF\n');
  });
});

const URL = 'https://mail.example.com/api/auth/verify-email?token=abc&callbackURL=%2Fverify-email';

describe('templates', () => {
  it('verifyEmail', () => {
    const m = verifyEmail({ to: 'ada@example.test', url: URL });
    expect(m.to).toBe('ada@example.test');
    expect(m.subject).toBe('Verify your email for Calendium');
    expect(m.html).toContain(`href="${URL.replace('&', '&amp;')}"`);
    expect(m.text).toContain(`Verify email: ${URL}`);
    expect(m.text).toContain('24 hours');
  });

  it('resetPasswordEmail', () => {
    const m = resetPasswordEmail({ to: 'ada@example.test', url: 'https://mail.example.com/api/auth/reset-password/tok?callbackURL=%2Freset-password' });
    expect(m.subject).toBe('Reset your Calendium password');
    expect(m.text).toContain('Reset password: https://mail.example.com/api/auth/reset-password/tok?callbackURL=%2Freset-password');
    expect(m.text).toContain('1 hour');
    expect(m.text).toContain('your password stays the same');
  });

  it('teamInvitationEmail mirrors the Go wording and escapes the inviter and team', () => {
    const m = teamInvitationEmail({ to: 'new@example.test', inviter: 'Olive <script>', team: 'Ops & "Co"', url: 'https://app.example.com/invite/raw' });
    expect(m.subject).toBe('Olive <script> invited you to Ops & "Co" on Calendium');
    expect(m.html).toContain('Olive &lt;script&gt; has invited you to join the team &quot;Ops &amp; &quot;Co&quot;&quot; on Calendium.');
    expect(m.html).not.toContain('<script>');
    expect(m.text).toContain('Olive <script> has invited you to join the team "Ops & "Co"" on Calendium.');
    expect(m.text).toContain('Accept the invitation: https://app.example.com/invite/raw');
    expect(m.text).toContain("The link expires in 14 days. If you weren't expecting this, you can safely ignore this email.");
  });
});
```

and `apps/web/lib/email/transport.test.ts`:

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const createTransportMock = vi.hoisted(() => vi.fn());
vi.mock('nodemailer', () => ({ default: { createTransport: createTransportMock } }));

import { _resetTransportForTests, isMailConfigured, sendMail } from '@/lib/email/transport';

const SMTP_KEYS = ['SMTP_HOST', 'SMTP_PORT', 'SMTP_USER', 'SMTP_PASS', 'SMTP_FROM', 'SMTP_SECURE'];
const MAIL = { to: 'ada@example.test', subject: 'Hi', html: '<p>Hi</p>', text: 'Hi' };

beforeEach(() => {
  for (const k of SMTP_KEYS) vi.stubEnv(k, '');
  _resetTransportForTests();
  createTransportMock.mockReset();
});

afterEach(() => {
  vi.unstubAllEnvs();
  vi.restoreAllMocks();
});

describe('transport', () => {
  it('is unconfigured without SMTP_HOST: sendMail throws and no transport is ever built', async () => {
    expect(isMailConfigured()).toBe(false);
    await expect(sendMail(MAIL)).rejects.toThrow('email is not configured (SMTP_HOST unset)');
    expect(createTransportMock).not.toHaveBeenCalled();
  });

  it('builds the transport lazily from SMTP_* and sends from SMTP_FROM', async () => {
    vi.stubEnv('SMTP_HOST', 'smtp.example.test');
    vi.stubEnv('SMTP_FROM', 'Calendium <noreply@example.test>');
    vi.stubEnv('SMTP_USER', 'u');
    vi.stubEnv('SMTP_PASS', 'p');
    const sendMailMock = vi.fn().mockResolvedValue({ messageId: 'x' });
    createTransportMock.mockReturnValue({ sendMail: sendMailMock });

    expect(isMailConfigured()).toBe(true);
    expect(createTransportMock).not.toHaveBeenCalled();

    await sendMail({ ...MAIL, replyTo: 'owner@acme.test' });
    expect(createTransportMock).toHaveBeenCalledWith({
      host: 'smtp.example.test',
      port: 587,
      secure: false,
      requireTLS: true,
      auth: { user: 'u', pass: 'p' },
    });
    expect(sendMailMock).toHaveBeenCalledWith({
      from: 'Calendium <noreply@example.test>',
      to: 'ada@example.test',
      subject: 'Hi',
      html: '<p>Hi</p>',
      text: 'Hi',
      replyTo: 'owner@acme.test',
    });

    await sendMail(MAIL);
    expect(createTransportMock).toHaveBeenCalledTimes(1);
    expect(sendMailMock.mock.calls[1]?.[0]).not.toHaveProperty('replyTo');
  });

  it('omits auth and requireTLS for an unauthenticated loopback relay', async () => {
    vi.stubEnv('SMTP_HOST', '127.0.0.1');
    vi.stubEnv('SMTP_PORT', '11025');
    vi.stubEnv('SMTP_FROM', 'noreply@example.test');
    createTransportMock.mockReturnValue({ sendMail: vi.fn().mockResolvedValue({}) });
    await sendMail(MAIL);
    expect(createTransportMock).toHaveBeenCalledWith({ host: '127.0.0.1', port: 11025, secure: false, requireTLS: false });
  });

  it('logs email.send_failed with the recipient domain only and rethrows', async () => {
    vi.stubEnv('SMTP_HOST', 'smtp.example.test');
    vi.stubEnv('SMTP_FROM', 'noreply@example.test');
    const boom = new Error('535 bad credentials');
    createTransportMock.mockReturnValue({ sendMail: vi.fn().mockRejectedValue(boom) });
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    await expect(sendMail({ ...MAIL, subject: 'Secret subject', text: 'secret body' })).rejects.toBe(boom);
    expect(errorSpy).toHaveBeenCalledWith('email.send_failed', { domain: 'example.test', error: '535 bad credentials' });
    const logged = JSON.stringify(errorSpy.mock.calls);
    expect(logged).not.toContain('secret');
    expect(logged).not.toContain('ada@');
  });
});
```

- [ ] **Step 2: Run the tests and watch them fail.**

```bash
cd apps/web && bunx vitest run lib/email/templates.test.ts lib/email/transport.test.ts
```

Expected: unresolved imports for `@/lib/email/render`, `@/lib/email/templates/*`, `@/lib/email/transport`.

- [ ] **Step 3: Implement render + templates.** Create `apps/web/lib/email/render.ts`:

```ts
/**
 * Minimal branded email rendering: one heading, one paragraph, an optional
 * call-to-action button and a footer, as inline-styled HTML plus a plain-text
 * twin. EVERY interpolation goes through escapeHtml — user-controlled strings
 * (names, team names, addresses) must never reach the HTML raw.
 */

export interface EmailContent {
  subject: string;
  heading: string;
  intro: string;
  cta?: { label: string; url: string };
  footer: string;
}

export interface RenderedEmail {
  subject: string;
  html: string;
  text: string;
}

/** A rendered message addressed to one recipient (the transport's input). */
export interface OutgoingMail extends RenderedEmail {
  to: string;
  replyTo?: string;
}

const ESCAPES: Record<string, string> = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' };

export function escapeHtml(value: string): string {
  return value.replace(/[&<>"']/g, (c) => ESCAPES[c] ?? c);
}

export function renderEmail(c: EmailContent): RenderedEmail {
  const cta = c.cta
    ? `<p style="margin:24px 0"><a href="${escapeHtml(c.cta.url)}" style="display:inline-block;padding:12px 20px;border-radius:8px;background:#111827;color:#ffffff;text-decoration:none;font-weight:600">${escapeHtml(c.cta.label)}</a></p>` +
      `<p style="font-size:13px;color:#6b7280;word-break:break-all">If the button doesn't work, open this link: ${escapeHtml(c.cta.url)}</p>`
    : '';
  const html =
    `<!doctype html><html><body style="margin:0;padding:32px 16px;background:#f9fafb;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;color:#111827">` +
    `<table role="presentation" width="100%" cellspacing="0" cellpadding="0"><tr><td align="center">` +
    `<table role="presentation" width="100%" style="max-width:520px;background:#ffffff;border-radius:12px;padding:32px" cellspacing="0" cellpadding="0"><tr><td>` +
    `<p style="margin:0 0 16px;font-size:13px;font-weight:600;letter-spacing:.04em;color:#6b7280">CALENDIUM</p>` +
    `<h1 style="margin:0 0 16px;font-size:20px;font-weight:600">${escapeHtml(c.heading)}</h1>` +
    `<p style="margin:0;font-size:15px;line-height:1.6">${escapeHtml(c.intro)}</p>` +
    cta +
    `<p style="margin:24px 0 0;font-size:13px;line-height:1.6;color:#6b7280">${escapeHtml(c.footer)}</p>` +
    `</td></tr></table></td></tr></table></body></html>`;
  const text = [c.heading, '', c.intro, ...(c.cta ? ['', `${c.cta.label}: ${c.cta.url}`] : []), '', c.footer, ''].join('\n');
  return { subject: c.subject, html, text };
}
```

Create `apps/web/lib/email/templates/verify-email.ts`:

```ts
import { type OutgoingMail, renderEmail } from '@/lib/email/render';

/** Sign-up / sign-in verification (Better Auth `sendVerificationEmail`; link valid 24 h). */
export function verifyEmail({ to, url }: { to: string; url: string }): OutgoingMail {
  return {
    to,
    ...renderEmail({
      subject: 'Verify your email for Calendium',
      heading: 'Verify your email',
      intro: 'Confirm this address to finish setting up your Calendium account.',
      cta: { label: 'Verify email', url },
      footer: "This link expires in 24 hours. If you didn't create a Calendium account, you can safely ignore this email.",
    }),
  };
}
```

Create `apps/web/lib/email/templates/reset-password.ts`:

```ts
import { type OutgoingMail, renderEmail } from '@/lib/email/render';

/** Password reset (Better Auth `sendResetPassword`; link valid 1 h, single use). */
export function resetPasswordEmail({ to, url }: { to: string; url: string }): OutgoingMail {
  return {
    to,
    ...renderEmail({
      subject: 'Reset your Calendium password',
      heading: 'Reset your password',
      intro: 'We received a request to reset the password for this address.',
      cta: { label: 'Reset password', url },
      footer: "This link expires in 1 hour and can be used once. If you didn't request a reset, you can safely ignore this email — your password stays the same.",
    }),
  };
}
```

Create `apps/web/lib/email/templates/team-invitation.ts`:

```ts
import { type OutgoingMail, renderEmail } from '@/lib/email/render';

/**
 * Team invitation — the REFERENCE wording that backend/internal/service/
 * team.go's text/html templates mirror. Not wired at runtime in piece 2 (the
 * Go service sends invitations); kept here and unit-tested so the two stay
 * in step.
 */
export function teamInvitationEmail({ to, inviter, team, url }: { to: string; inviter: string; team: string; url: string }): OutgoingMail {
  return {
    to,
    ...renderEmail({
      subject: `${inviter} invited you to ${team} on Calendium`,
      heading: `Join ${team}`,
      intro: `${inviter} has invited you to join the team "${team}" on Calendium.`,
      cta: { label: 'Accept the invitation', url },
      footer: "The link expires in 14 days. If you weren't expecting this, you can safely ignore this email.",
    }),
  };
}
```

- [ ] **Step 4: Implement the transport.** Create `apps/web/lib/email/transport.ts`:

```ts
import nodemailer, { type Transporter } from 'nodemailer';

import { type MailConfig, readMailConfig } from '@/lib/email/config';
import type { OutgoingMail } from '@/lib/email/render';

/**
 * Lazy nodemailer transport over SMTP_*. Nothing here runs at import or at
 * `next build`: the config is read on first use and the transport is built
 * on the first send, so a self-host without SMTP never touches nodemailer.
 */
let transport: Transporter | undefined;
let config: MailConfig | undefined;

function mailConfig(): MailConfig {
  config ??= readMailConfig(process.env);
  return config;
}

export function isMailConfigured(): boolean {
  return mailConfig().configured;
}

function transporter(): Transporter {
  const cfg = mailConfig();
  if (!cfg.configured) throw new Error('email is not configured (SMTP_HOST unset)');
  transport ??= nodemailer.createTransport({
    host: cfg.host,
    port: cfg.port,
    secure: cfg.secure,
    requireTLS: cfg.requireTLS,
    ...(cfg.user ? { auth: { user: cfg.user, pass: cfg.pass } } : {}),
  });
  return transport;
}

/**
 * Sends one message from SMTP_FROM. On failure logs `email.send_failed` with
 * the recipient DOMAIN and the provider error (never the address, subject or
 * body) and rethrows so Better Auth answers 500 and the page can say
 * "We couldn't send the email. Try again in a minute."
 */
export async function sendMail(mail: OutgoingMail): Promise<void> {
  const cfg = mailConfig();
  if (!cfg.configured) throw new Error('email is not configured (SMTP_HOST unset)');
  try {
    await transporter().sendMail({
      from: cfg.from,
      to: mail.to,
      subject: mail.subject,
      html: mail.html,
      text: mail.text,
      ...(mail.replyTo ? { replyTo: mail.replyTo } : {}),
    });
  } catch (err) {
    console.error('email.send_failed', {
      domain: mail.to.split('@')[1] ?? '',
      error: err instanceof Error ? err.message : String(err),
    });
    throw err;
  }
}

/** Test seam: forget the cached config and transport. */
export function _resetTransportForTests(): void {
  transport = undefined;
  config = undefined;
}
```

- [ ] **Step 5: Run the tests and typecheck.**

```bash
cd apps/web && bunx vitest run lib/email && bunx tsc --noEmit
```

Expected: `templates.test.ts` and `transport.test.ts` pass; typecheck clean.

- [ ] **Step 6: Commit.**

```bash
git add apps/web/lib/email
git commit -m "feat(web): branded email rendering, verify/reset/invitation templates and a lazy nodemailer transport" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 9: Wire Better Auth — verification, reset, rate limiting, password hook, client-IP route, migration 0028

**Files:**
- Modify: `apps/web/lib/auth.ts` (whole file), `apps/web/app/api/auth/[...all]/route.ts` (whole file)
- Create: `backend/migrations/0028_better_auth_rate_limit.sql`
- Test: `apps/web/lib/auth.test.ts` (replace whole file)

**Interfaces:**
- Consumes: `buildTrustedOrigins`, `isAllowedOrigin`, `rateLimitRules`, `passwordPolicyError`, `withClientIp`, `CLIENT_IP_HEADER` (Task 6); `readMailConfig` (Task 7); `sendMail`, `verifyEmail`, `resetPasswordEmail` (Task 8).
- Produces: `auth` (Better Auth instance) with the Global Constraints options; `GET`/`POST`/`OPTIONS` route handlers; the `rateLimit` table.

- [ ] **Step 1: Replace `apps/web/lib/auth.test.ts` with the new option assertions (the origin tests moved to `auth-env.test.ts` in Task 6).**

```ts
import { describe, expect, it } from 'vitest';

// lib/auth.ts constructs the Better Auth server at import. `db()` builds a
// pg.Pool lazily (no connection) and readMailConfig sees no SMTP_* in the
// Vitest environment, so the module loads under jsdom without Postgres or
// SMTP. The origin/IP/policy helpers live in lib/auth-env.ts and are covered
// by auth-env.test.ts; this file pins what lib/auth.ts hands to betterAuth().
import { auth } from '@/lib/auth';

interface Options {
  trustedOrigins?: string[];
  rateLimit?: { enabled?: boolean; storage?: string; window?: number; max?: number; customRules?: Record<string, { window: number; max: number }> };
  emailAndPassword?: {
    requireEmailVerification?: boolean;
    minPasswordLength?: number;
    maxPasswordLength?: number;
    resetPasswordTokenExpiresIn?: number;
    revokeSessionsOnPasswordReset?: boolean;
    sendResetPassword?: unknown;
  };
  emailVerification?: unknown;
  advanced?: { ipAddress?: { ipAddressHeaders?: string[] } };
  hooks?: { before?: unknown };
}

const options = auth.options as unknown as Options;

describe('auth options', () => {
  it('trusts the fixed origins (native scheme, Apple form_post, four Wails origins)', () => {
    for (const o of ['calendium://', 'https://appleid.apple.com', 'wails://wails', 'wails://wails.localhost', 'http://wails.localhost', 'https://wails.localhost']) {
      expect(options.trustedOrigins).toContain(o);
    }
  });

  it('enables database-backed rate limiting with the per-route rules', () => {
    expect(options.rateLimit).toMatchObject({ enabled: true, storage: 'database', window: 60, max: 100 });
    expect(options.rateLimit?.customRules).toEqual({
      '/sign-in/email': { window: 60, max: 5 },
      '/sign-up/email': { window: 60, max: 3 },
      '/request-password-reset': { window: 600, max: 3 },
      '/forget-password': { window: 600, max: 3 },
      '/send-verification-email': { window: 600, max: 3 },
      '/token': { window: 60, max: 60 },
    });
  });

  it('reads the client IP only from the server-set header', () => {
    expect(options.advanced?.ipAddress?.ipAddressHeaders).toEqual(['x-calendium-client-ip']);
  });

  it('applies the password bounds and reset semantics; verification follows SMTP (unset in this run)', () => {
    expect(options.emailAndPassword).toMatchObject({
      minPasswordLength: 10,
      maxPasswordLength: 128,
      resetPasswordTokenExpiresIn: 3600,
      revokeSessionsOnPasswordReset: true,
      requireEmailVerification: false,
    });
    expect(options.emailAndPassword?.sendResetPassword).toBeUndefined();
    expect(options.emailVerification).toBeUndefined();
  });

  it('installs a before hook (the password policy)', () => {
    expect(typeof options.hooks?.before).toBe('function');
  });
});
```

- [ ] **Step 2: Run it and watch it fail.**

```bash
cd apps/web && bunx vitest run lib/auth.test.ts
```

Expected: the rate-limit, IP-header, password-bounds and hook assertions fail (current `auth.ts` sets none of them).

- [ ] **Step 3: Rewrite `apps/web/lib/auth.ts`.**

```ts
import { betterAuth } from 'better-auth';
import { APIError, createAuthMiddleware, getSessionFromCtx } from 'better-auth/api';
import { bearer, jwt, oneTimeToken } from 'better-auth/plugins';
import { nextCookies } from 'better-auth/next-js';
import { expo } from '@better-auth/expo';
import { Pool } from 'pg';

import { CLIENT_IP_HEADER, buildTrustedOrigins, passwordPolicyError, rateLimitRules } from '@/lib/auth-env';
import { readMailConfig } from '@/lib/email/config';
import { resetPasswordEmail } from '@/lib/email/templates/reset-password';
import { verifyEmail } from '@/lib/email/templates/verify-email';
import { sendMail } from '@/lib/email/transport';

/**
 * Better Auth server — the identity provider for web, desktop, and mobile.
 * Hosted by the Next.js web app at `${BETTER_AUTH_URL}/api/auth/*` and backed
 * by the SAME Postgres as the Go API (its own tables: user, session, account,
 * verification, jwks, rateLimit — see backend/migrations/0002 and 0028).
 *
 * The Go backend is a pure resource server: it verifies the EdDSA JWTs minted
 * here (GET /api/auth/token) by fetching JWKS from /api/auth/jwks.
 */

/**
 * Lazily-created pg Pool. Constructing a Pool does NOT open a connection, so
 * this is safe at module load and keeps `next build` fully offline.
 */
let pool: Pool | undefined;
function db(): Pool {
  pool ??= new Pool({ connectionString: process.env.DATABASE_URL });
  return pool;
}

/** Only register a social provider when BOTH its id and secret are configured. */
function socialProviders() {
  const providers: Record<string, { clientId: string; clientSecret: string }> = {};
  if (process.env.GOOGLE_CLIENT_ID && process.env.GOOGLE_CLIENT_SECRET) {
    providers.google = {
      clientId: process.env.GOOGLE_CLIENT_ID,
      clientSecret: process.env.GOOGLE_CLIENT_SECRET,
    };
  }
  if (process.env.APPLE_CLIENT_ID && process.env.APPLE_CLIENT_SECRET) {
    providers.apple = {
      clientId: process.env.APPLE_CLIENT_ID,
      clientSecret: process.env.APPLE_CLIENT_SECRET,
    };
  }
  return providers;
}

/**
 * SMTP is read once at module load. readMailConfig returns {configured:false}
 * when no SMTP_* is set (self-host without email, `next build`) and throws on
 * a half-set block so a typo surfaces at boot. The cloud-mode requirement is
 * enforced by instrumentation.ts before any request is served.
 */
const mail = readMailConfig(process.env);

/**
 * Password policy, enforced server-side on every password-setting route (the
 * pages mirror it for instant feedback). The email the policy checks against
 * comes from the request body (sign-up), the session (change-password) or the
 * reset token's verification row (reset-password).
 */
const passwordPolicyHook = createAuthMiddleware(async (ctx) => {
  if (ctx.path !== '/sign-up/email' && ctx.path !== '/change-password' && ctx.path !== '/reset-password') return;
  const body = (ctx.body ?? {}) as { password?: unknown; newPassword?: unknown; email?: unknown; token?: unknown };
  const password =
    typeof body.password === 'string' ? body.password : typeof body.newPassword === 'string' ? body.newPassword : '';
  let email: string | null = null;
  if (ctx.path === '/sign-up/email') {
    email = typeof body.email === 'string' ? body.email : null;
  } else if (ctx.path === '/change-password') {
    email = (await getSessionFromCtx(ctx))?.user.email ?? null;
  } else if (typeof body.token === 'string') {
    const verification = await ctx.context.internalAdapter.findVerificationValue(`reset-password:${body.token}`);
    if (verification) email = (await ctx.context.internalAdapter.findUserById(verification.value))?.email ?? null;
  }
  const violation = passwordPolicyError(password, email);
  if (violation) throw new APIError('BAD_REQUEST', { message: violation.message, code: violation.code });
});

export const auth = betterAuth({
  database: db(),
  secret: process.env.BETTER_AUTH_SECRET,
  baseURL: process.env.BETTER_AUTH_URL,
  trustedOrigins: buildTrustedOrigins(process.env),
  emailAndPassword: {
    enabled: true,
    minPasswordLength: 10,
    maxPasswordLength: 128,
    // With SMTP, new accounts must verify before signing in (and sign-up
    // answers generically for duplicates); without it, self-host keeps the
    // auto-sign-in behaviour and surfaces the duplicate error.
    requireEmailVerification: mail.configured,
    resetPasswordTokenExpiresIn: 3600,
    revokeSessionsOnPasswordReset: true,
    sendResetPassword: mail.configured
      ? async ({ user, url }) => {
          await sendMail(resetPasswordEmail({ to: user.email, url }));
        }
      : undefined,
  },
  emailVerification: mail.configured
    ? {
        sendOnSignUp: true,
        sendOnSignIn: true,
        autoSignInAfterVerification: true,
        expiresIn: 86_400,
        sendVerificationEmail: async ({ user, url }) => {
          await sendMail(verifyEmail({ to: user.email, url }));
        },
      }
    : undefined,
  // Per-IP limits in Postgres (replica-safe, restart-safe); keys are ip+path.
  rateLimit: { enabled: true, storage: 'database', window: 60, max: 100, customRules: rateLimitRules() },
  // The ONLY header Better Auth reads the client IP from; route.ts overwrites
  // it on every request from X-Forwarded-For per TRUST_PROXY.
  advanced: { ipAddress: { ipAddressHeaders: [CLIENT_IP_HEADER] } },
  hooks: { before: passwordPolicyHook },
  socialProviders: socialProviders(),
  plugins: [
    // Asymmetric EdDSA (Ed25519) JWTs + JWKS at /api/auth/jwks. The token
    // carries sub=userId and iss=BETTER_AUTH_URL by default; add email/name so
    // the Go resource server can mirror the user without a DB round-trip.
    jwt({
      jwt: {
        definePayload: ({ user }) => ({
          email: user.email,
          name: user.name,
        }),
      },
    }),
    // Accept `Authorization: Bearer <session-token>` on Better Auth's own
    // endpoints for native (desktop/mobile) clients.
    bearer(),
    // Mobile (Expo) deep-link + secure-store session support.
    expo(),
    // Short-lived one-time tokens for the desktop browser → app handoff (see
    // apps/web/app/desktop-callback/page.tsx).
    oneTimeToken(),
    // MUST be last: makes Set-Cookie from server actions/route handlers work.
    nextCookies(),
  ],
});
```

- [ ] **Step 4: Rewrite `apps/web/app/api/auth/[...all]/route.ts`.**

```ts
import { toNextJsHandler } from 'better-auth/next-js';

import { auth } from '@/lib/auth';
import { isAllowedOrigin, withClientIp } from '@/lib/auth-env';

/** Better Auth catch-all: hosts sign-in, OAuth callbacks, JWKS, token mint. */
const handlers = toNextJsHandler(auth);

const trustProxy = () => process.env.TRUST_PROXY === 'true';

/**
 * Cross-origin auth support for native clients. The Wails desktop WebView
 * (and, with ALLOW_DEV_ORIGINS, the localhost dev servers) call these
 * endpoints from a DIFFERENT origin, so the browser preflights and needs
 * CORS headers back — Better Auth itself emits none. We reflect only
 * allowlisted origins (credentialed CORS forbids `*`) and expose
 * `set-auth-token` so the bearer clients can read their session token.
 */
function corsHeaders(req: Request): Headers {
  const headers = new Headers();
  headers.set('Vary', 'Origin');
  const origin = req.headers.get('origin');
  if (isAllowedOrigin(origin, process.env)) {
    headers.set('Access-Control-Allow-Origin', origin as string);
    headers.set('Access-Control-Allow-Credentials', 'true');
    headers.set('Access-Control-Allow-Methods', 'GET, POST, OPTIONS');
    headers.set('Access-Control-Allow-Headers', 'Content-Type, Authorization');
    headers.set('Access-Control-Expose-Headers', 'set-auth-token, x-retry-after');
  }
  return headers;
}

function withCors(res: Response, req: Request): Response {
  corsHeaders(req).forEach((value, key) => {
    res.headers.set(key, value);
  });
  return res;
}

// Every request is re-wrapped with the server-resolved client IP before Better
// Auth sees it (rate-limit buckets are keyed on it); see lib/auth-env.ts.
export async function GET(req: Request): Promise<Response> {
  return withCors(await handlers.GET(withClientIp(req, trustProxy())), req);
}

export async function POST(req: Request): Promise<Response> {
  return withCors(await handlers.POST(withClientIp(req, trustProxy())), req);
}

export function OPTIONS(req: Request): Response {
  return new Response(null, { status: 204, headers: corsHeaders(req) });
}
```

- [ ] **Step 5: Add the migration.** Create `backend/migrations/0028_better_auth_rate_limit.sql`:

```sql
-- 0028_better_auth_rate_limit.sql — Better Auth rate limiting (piece 2).
--
-- apps/web/lib/auth.ts sets `rateLimit: { storage: "database" }`, which keeps
-- per-IP auth counters in Postgres so limits survive restarts and are shared
-- across web replicas. Column names are Better Auth's own (camel-cased,
-- quoted) and its atomic `incrementOne` relies on the unique `key`. Better
-- Auth prunes rows older than the largest window in the background; the
-- index keeps that delete and the per-key lookup cheap.
CREATE TABLE IF NOT EXISTS "rateLimit" (
    "id"          text    NOT NULL PRIMARY KEY,
    "key"         text    NOT NULL UNIQUE,
    "count"       integer NOT NULL,
    "lastRequest" bigint  NOT NULL
);
CREATE INDEX IF NOT EXISTS "rateLimit_lastRequest_idx" ON "rateLimit" ("lastRequest");
```

- [ ] **Step 6: Run the web tests, typecheck, and the Postgres migration suite.**

```bash
cd apps/web && bunx vitest run lib/auth.test.ts lib/auth-env.test.ts && bunx tsc --noEmit
cd ../../backend && go test ./internal/adapter/out/postgres/... -count=1
```

Expected: both Vitest files pass; typecheck clean; the Postgres suite (which applies every file in `backend/migrations` through `migrate.Apply` against testcontainers) passes, or is skipped with the usual Docker notice when Docker is absent (`REQUIRE_DOCKER=1` makes it mandatory).

- [ ] **Step 7: Commit.**

```bash
git add apps/web/lib/auth.ts apps/web/lib/auth.test.ts "apps/web/app/api/auth/[...all]/route.ts" backend/migrations/0028_better_auth_rate_limit.sql
git commit -m "feat(auth): email verification, password reset, Postgres rate limits, password-policy hook, server-set client IP, migration 0028" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 10: Web JWT cache, sign-out invalidation, operator reset CLI, Docker image

**Files:**
- Modify: `apps/web/lib/auth-client.ts`, `apps/web/lib/api.ts`, `apps/web/lib/sign-out.ts`, `apps/web/lib/sign-out.test.ts` (mock), `apps/web/lib/use-mail-attachments.test.ts` (mock), `apps/web/components/app/command-palette.test.tsx` (mock), `apps/web/Dockerfile`
- Create: `apps/web/scripts/reset-password.mjs`
- Test: `apps/web/lib/auth-client.test.ts`

**Interfaces:**
- Consumes: `createAccessTokenCache`, `AccessTokenCache`, `ApiClientOptions.accessTokens` (Track C Task 11 — must be committed first).
- Produces: `accessTokens: AccessTokenCache`, `getAccessToken(): Promise<string | null>`, `invalidateAccessToken(): void` from `@/lib/auth-client`; `performSignOut` now invalidates the cache; `node apps/web/scripts/reset-password.mjs <email>`.

- [ ] **Step 1: Write the failing auth-client test.** Create `apps/web/lib/auth-client.test.ts`:

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { accessTokens, getAccessToken, invalidateAccessToken } from '@/lib/auth-client';

function jwt(expSeconds: number): string {
  const payload = btoa(JSON.stringify({ sub: 'u1', exp: expSeconds })).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  return `eyJhbGciOiJFZERTQSJ9.${payload}.sig`;
}

const fetchMock = vi.fn<typeof fetch>();

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
  accessTokens.invalidate();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('getAccessToken (web, cached)', () => {
  it('mints from /api/auth/token with the session cookie and caches a fresh JWT', async () => {
    const token = jwt(Math.floor(Date.now() / 1000) + 900);
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ token }), { status: 200 }));

    expect(await getAccessToken()).toBe(token);
    expect(await getAccessToken()).toBe(token);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledWith('/api/auth/token', expect.objectContaining({ method: 'GET', credentials: 'include' }));
  });

  it('returns null (uncached) on 401/429 and network errors, then mints again next time', async () => {
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ error: 'nope' }), { status: 401 }));
    expect(await getAccessToken()).toBeNull();
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 429, headers: { 'X-Retry-After': '30' } }));
    expect(await getAccessToken()).toBeNull();
    fetchMock.mockRejectedValueOnce(new TypeError('offline'));
    expect(await getAccessToken()).toBeNull();
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it('invalidateAccessToken forces a re-mint', async () => {
    const first = jwt(Math.floor(Date.now() / 1000) + 900);
    const second = jwt(Math.floor(Date.now() / 1000) + 950);
    fetchMock
      .mockResolvedValueOnce(new Response(JSON.stringify({ token: first }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ token: second }), { status: 200 }));
    expect(await getAccessToken()).toBe(first);
    invalidateAccessToken();
    expect(await getAccessToken()).toBe(second);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});
```

- [ ] **Step 2: Run it and watch it fail.**

```bash
cd apps/web && bunx vitest run lib/auth-client.test.ts
```

Expected: `No "accessTokens" export is defined` / `invalidateAccessToken is not a function`.

- [ ] **Step 3: Implement the cache in `apps/web/lib/auth-client.ts`.** Replace everything from the `getAccessToken` doc comment to the end of the file with:

```ts
/**
 * Mints a short-lived (default 15m) EdDSA JWT for the current session via the
 * jwt() plugin's GET /api/auth/token (same-origin, session cookie). Returns
 * null when signed out, rate limited (429) or unreachable — null is never
 * cached, so the next call tries again.
 */
async function mintAccessToken(): Promise<string | null> {
  try {
    const res = await fetch('/api/auth/token', {
      method: 'GET',
      credentials: 'include',
      headers: { accept: 'application/json' },
    });
    if (!res.ok) return null;
    const data = (await res.json()) as { token?: string };
    return data.token ?? null;
  } catch {
    return null;
  }
}

/**
 * Process-wide JWT cache (piece 2): reuse the token until 60 s before `exp`,
 * share one in-flight mint between concurrent callers. lib/api.ts hands this
 * SAME cache to ApiClient so a 401 retry and sign-out invalidate the one copy;
 * every other caller (attachments, collab stream, web push) goes through
 * getAccessToken and benefits too.
 */
export const accessTokens = createAccessTokenCache(mintAccessToken);

/** The Bearer credential for the Go API (cached JWT, minted on demand). */
export function getAccessToken(): Promise<string | null> {
  return accessTokens.get();
}

/** Drops the cached JWT (sign-out, server switch). The next call re-mints. */
export function invalidateAccessToken(): void {
  accessTokens.invalidate();
}
```

and add `import { createAccessTokenCache } from '@calendium/shared';` after the `better-auth/client/plugins` import.

- [ ] **Step 4: Share the cache with the API client and invalidate on sign-out.**

`apps/web/lib/api.ts` — change the import to `import { accessTokens, getAccessToken } from '@/lib/auth-client';` and the construction to:

```ts
  client ??= new ApiClient({
    baseUrl: env.apiUrl,
    getAccessToken,
    // One JWT cache for the whole tab: ApiClient's 401 retry and sign-out
    // both invalidate the same copy (lib/auth-client.ts).
    accessTokens,
  });
```

and update its doc comment sentence `Mints a fresh Better Auth JWT (GET /api/auth/token) per request` to `Reuses the cached Better Auth JWT (lib/auth-client.ts; GET /api/auth/token until 60 s before expiry)`.

`apps/web/lib/sign-out.ts` — change the import to `import { invalidateAccessToken, signOut } from '@/lib/auth-client';`, add the sentence `The cached API JWT is dropped too, so a second account on the same tab can never ride the first one's token.` to the doc comment, and make the body:

```ts
export async function performSignOut(): Promise<void> {
  await signOut();
  invalidateAccessToken();
  clearActingAs();
  resetTourSession();
  await clearOfflineState();
}
```

Update the three `@/lib/auth-client` mocks (Vitest throws on access to an export the factory did not define):
- `apps/web/lib/sign-out.test.ts`: add `const invalidateMock = vi.fn();` next to `signOutMock`, make the factory `({ signOut: (...args: unknown[]) => signOutMock(...args), invalidateAccessToken: () => invalidateMock() })`, and add to the existing success test: `expect(invalidateMock).toHaveBeenCalledTimes(1);`.
- `apps/web/components/app/command-palette.test.tsx`: make the factory `({ signOut: (...args: unknown[]) => signOutMock(...args), invalidateAccessToken: () => {} })`.
- `apps/web/lib/use-mail-attachments.test.ts`: make the factory `({ getAccessToken: async () => null, accessTokens: undefined })`.

- [ ] **Step 5: Add the operator CLI.** Create `apps/web/scripts/reset-password.mjs`:

```js
#!/usr/bin/env node
/**
 * Self-host password reset without email (docs/self-hosting/security.md).
 *
 *   docker compose exec web node apps/web/scripts/reset-password.mjs <email>
 *
 * Hashes a fresh temporary password with Better Auth's own hashPassword,
 * upserts the user's `credential` row in "account", deletes every "session"
 * row (all devices are signed out) and prints the temporary password once.
 * Reads DATABASE_URL like the web server does.
 */
import { randomBytes } from 'node:crypto';

import { hashPassword } from 'better-auth/crypto';
import pg from 'pg';

const email = process.argv[2]?.trim().toLowerCase();
if (!email) {
  console.error('usage: node apps/web/scripts/reset-password.mjs <email>');
  process.exit(2);
}
if (!process.env.DATABASE_URL) {
  console.error('DATABASE_URL is required');
  process.exit(2);
}

const pool = new pg.Pool({ connectionString: process.env.DATABASE_URL });
const client = await pool.connect();
try {
  const { rows } = await client.query('SELECT "id" FROM "user" WHERE lower("email") = $1', [email]);
  if (rows.length === 0) {
    console.error(`no user with email ${email}`);
    process.exit(1);
  }
  const userId = rows[0].id;
  const password = randomBytes(12).toString('base64url');
  const hash = await hashPassword(password);

  await client.query('BEGIN');
  const updated = await client.query(
    'UPDATE "account" SET "password" = $1, "updatedAt" = now() WHERE "userId" = $2 AND "providerId" = $3',
    [hash, userId, 'credential']
  );
  if (updated.rowCount === 0) {
    await client.query(
      'INSERT INTO "account" ("id", "accountId", "providerId", "userId", "password", "createdAt", "updatedAt") VALUES ($1, $2, $3, $2, $4, now(), now())',
      [randomBytes(16).toString('hex'), userId, 'credential', hash]
    );
  }
  await client.query('DELETE FROM "session" WHERE "userId" = $1', [userId]);
  await client.query('COMMIT');

  console.log(`Temporary password for ${email}: ${password}`);
  console.log('All sessions were signed out. Ask the user to sign in and change it in Settings → Account.');
} catch (err) {
  await client.query('ROLLBACK').catch(() => {});
  console.error(err instanceof Error ? err.message : err);
  process.exit(1);
} finally {
  client.release();
  await pool.end();
}
```

In `apps/web/Dockerfile`, after the `COPY --from=build --chown=nextjs:nodejs /app/apps/web/public ./apps/web/public` line, add:

```dockerfile
# Operator CLI (self-host password reset without SMTP); runs against the
# traced node_modules next to the standalone server.
COPY --from=build --chown=nextjs:nodejs /app/apps/web/scripts ./apps/web/scripts
```

- [ ] **Step 6: Run the affected web tests, Biome and typecheck.**

```bash
cd apps/web && bunx vitest run lib/auth-client.test.ts lib/sign-out.test.ts lib/use-mail-attachments.test.ts components/app/command-palette.test.tsx && bunx tsc --noEmit && cd ../.. && bunx biome check apps/web/lib apps/web/scripts
```

Expected: all four files pass; typecheck and Biome clean.

- [ ] **Step 7: Commit.**

```bash
git add apps/web/lib/auth-client.ts apps/web/lib/auth-client.test.ts apps/web/lib/api.ts apps/web/lib/sign-out.ts apps/web/lib/sign-out.test.ts apps/web/lib/use-mail-attachments.test.ts apps/web/components/app/command-palette.test.tsx apps/web/scripts/reset-password.mjs apps/web/Dockerfile
git commit -m "feat(web): cache the API JWT until 60s before expiry, invalidate on sign-out, add the self-host password reset CLI" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 11: Shared contract — `createAccessTokenCache`, `ApiClient` 401 retry, `features.email`, invitation delivery

**Files:**
- Create: `packages/shared/src/access-token-cache.ts`
- Modify: `packages/shared/src/client.ts` (`ApiClientOptions`, constructor, `request`, new `invalidateAccessToken`), `packages/shared/src/types.ts` (`InstanceFeatures`, `TeamInvitation`), `packages/shared/src/index.ts`
- Test: `packages/shared/src/access-token-cache.test.ts`, `packages/shared/src/client.test.ts` (append one describe block)

**Interfaces:**
- Produces: `ACCESS_TOKEN_REFRESH_MARGIN_MS = 60_000`; `interface AccessTokenCache { get(): Promise<string | null>; invalidate(): void }`; `createAccessTokenCache(mint: () => Promise<string | null>, now?: () => number): AccessTokenCache`; `decodeJwtExp(token: string): number | null` (ms since epoch); `ApiClientOptions.accessTokens?: AccessTokenCache`; `ApiClient.invalidateAccessToken(): void`; `InstanceFeatures.email?: boolean`; `type TeamInvitationDelivery = 'mailbox' | 'smtp' | 'link'`; `TeamInvitation.delivery?: TeamInvitationDelivery`; `TeamInvitation.inviteUrl?: string`.

- [ ] **Step 1: Write the failing cache tests.** Create `packages/shared/src/access-token-cache.test.ts`:

```ts
import { describe, expect, it, vi } from 'vitest';

import { ACCESS_TOKEN_REFRESH_MARGIN_MS, createAccessTokenCache, decodeJwtExp } from './access-token-cache';

/** A syntactically valid, unsigned JWT with the given exp (seconds). */
function jwt(expSeconds: number, extra: Record<string, unknown> = {}): string {
  const payload = Buffer.from(JSON.stringify({ sub: 'u1', exp: expSeconds, ...extra })).toString('base64url');
  return `eyJhbGciOiJFZERTQSJ9.${payload}.sig`;
}

const T0 = 1_800_000_000_000; // fixed "now" in ms

describe('decodeJwtExp', () => {
  it('returns exp in milliseconds for a well-formed token', () => {
    expect(decodeJwtExp(jwt(1_800_000_900))).toBe(1_800_000_900_000);
  });

  it('decodes payloads with non-ASCII claims', () => {
    expect(decodeJwtExp(jwt(1_800_000_900, { name: 'José — café' }))).toBe(1_800_000_900_000);
  });

  it('returns null for opaque strings, missing or non-numeric exp, and bad base64', () => {
    expect(decodeJwtExp('test-access-token')).toBeNull();
    expect(decodeJwtExp('a.b')).toBeNull();
    expect(decodeJwtExp(`h.${Buffer.from('{"sub":"u1"}').toString('base64url')}.s`)).toBeNull();
    expect(decodeJwtExp(`h.${Buffer.from('{"exp":"soon"}').toString('base64url')}.s`)).toBeNull();
    expect(decodeJwtExp('h.%%%.s')).toBeNull();
    expect(decodeJwtExp(`h.${Buffer.from('not json').toString('base64url')}.s`)).toBeNull();
  });
});

describe('createAccessTokenCache', () => {
  it('reuses a token until 60 s before exp, then mints again', async () => {
    let now = T0;
    const first = jwt((T0 + 900_000) / 1000);
    const second = jwt((T0 + 1_800_000) / 1000);
    const mint = vi.fn<() => Promise<string | null>>().mockResolvedValueOnce(first).mockResolvedValueOnce(second);
    const cache = createAccessTokenCache(mint, () => now);

    expect(await cache.get()).toBe(first);
    now = T0 + 900_000 - ACCESS_TOKEN_REFRESH_MARGIN_MS - 1;
    expect(await cache.get()).toBe(first);
    expect(mint).toHaveBeenCalledTimes(1);

    now = T0 + 900_000 - ACCESS_TOKEN_REFRESH_MARGIN_MS;
    expect(await cache.get()).toBe(second);
    expect(mint).toHaveBeenCalledTimes(2);
  });

  it('shares one in-flight mint between concurrent callers', async () => {
    let resolve!: (t: string | null) => void;
    const mint = vi.fn(() => new Promise<string | null>((r) => { resolve = r; }));
    const cache = createAccessTokenCache(mint, () => T0);
    const a = cache.get();
    const b = cache.get();
    expect(mint).toHaveBeenCalledTimes(1);
    resolve(jwt((T0 + 900_000) / 1000));
    expect(await a).toBe(await b);
    expect(await cache.get()).toBe(await a);
    expect(mint).toHaveBeenCalledTimes(1);
  });

  it('never caches null, opaque tokens, or tokens without a decodable exp', async () => {
    const mint = vi.fn<() => Promise<string | null>>()
      .mockResolvedValueOnce(null)
      .mockResolvedValueOnce('test-access-token')
      .mockResolvedValueOnce(`h.${Buffer.from('{"sub":"u1"}').toString('base64url')}.s`)
      .mockResolvedValueOnce(jwt((T0 + 900_000) / 1000));
    const cache = createAccessTokenCache(mint, () => T0);
    expect(await cache.get()).toBeNull();
    expect(await cache.get()).toBe('test-access-token');
    expect(await cache.get()).toMatch(/^h\./);
    expect(mint).toHaveBeenCalledTimes(3);
    await cache.get();
    await cache.get();
    expect(mint).toHaveBeenCalledTimes(4);
  });

  it('returns but does not cache a token that is already inside the refresh margin', async () => {
    const stale = jwt((T0 + ACCESS_TOKEN_REFRESH_MARGIN_MS - 1) / 1000);
    const mint = vi.fn<() => Promise<string | null>>().mockResolvedValue(stale);
    const cache = createAccessTokenCache(mint, () => T0);
    expect(await cache.get()).toBe(stale);
    expect(await cache.get()).toBe(stale);
    expect(mint).toHaveBeenCalledTimes(2);
  });

  it('invalidate() forces the next get() to mint', async () => {
    const mint = vi.fn<() => Promise<string | null>>().mockResolvedValue(jwt((T0 + 900_000) / 1000));
    const cache = createAccessTokenCache(mint, () => T0);
    await cache.get();
    cache.invalidate();
    await cache.get();
    expect(mint).toHaveBeenCalledTimes(2);
  });

  it('propagates a mint rejection to every waiter and recovers afterwards', async () => {
    const mint = vi.fn<() => Promise<string | null>>().mockRejectedValueOnce(new Error('boom')).mockResolvedValueOnce(null);
    const cache = createAccessTokenCache(mint, () => T0);
    const a = cache.get();
    const b = cache.get();
    await expect(a).rejects.toThrow('boom');
    await expect(b).rejects.toThrow('boom');
    expect(await cache.get()).toBeNull();
  });
});
```

Append to `packages/shared/src/client.test.ts` (uses the existing `makeClient`/`createFakeFetch`/`BASE_URL` helpers):

```ts
// ---------------------------------------------------------------------------
// Access-token cache + single 401 retry (piece 2)
// ---------------------------------------------------------------------------

function fakeJwt(expSeconds: number): string {
  const payload = Buffer.from(JSON.stringify({ sub: 'u1', exp: expSeconds })).toString('base64url');
  return `eyJhbGciOiJFZERTQSJ9.${payload}.sig`;
}

describe('access-token cache and 401 retry', () => {
  const fresh = () => fakeJwt(Math.floor(Date.now() / 1000) + 900);

  it('reuses a cached JWT across requests', async () => {
    const { client, getAccessToken } = makeClient({ token: fresh(), responses: [{ status: 200, body: {} }] });
    await client.getMe();
    await client.listCalendars();
    expect(getAccessToken).toHaveBeenCalledTimes(1);
  });

  it('keeps minting per request for an opaque token (pre-existing fixtures unchanged)', async () => {
    const { client, getAccessToken } = makeClient({ responses: [{ status: 200, body: {} }] });
    await client.getMe();
    await client.getMe();
    expect(getAccessToken).toHaveBeenCalledTimes(2);
  });

  it('retries exactly once with a re-minted token on 401 and returns the retry result', async () => {
    const first = fresh();
    const second = fakeJwt(Math.floor(Date.now() / 1000) + 950);
    const { client, calls, getAccessToken } = makeClient({
      token: first,
      responses: [
        { status: 401, body: { error: { code: 'unauthorized', message: 'invalid or expired access token' } } },
        { status: 200, body: { id: 'me1' } },
      ],
    });
    getAccessToken.mockResolvedValueOnce(first).mockResolvedValueOnce(second);
    const result = await client.getMe();
    expect(result).toEqual({ id: 'me1' });
    expect(calls).toHaveLength(2);
    expect(calls[0].headers.Authorization).toBe(`Bearer ${first}`);
    expect(calls[1].headers.Authorization).toBe(`Bearer ${second}`);
    expect(getAccessToken).toHaveBeenCalledTimes(2);
  });

  it('throws ApiRequestError(401) after the single retry', async () => {
    const { client, calls } = makeClient({
      token: fresh(),
      responses: [{ status: 401, body: { error: { code: 'unauthorized', message: 'nope' } } }],
    });
    await expect(client.getMe()).rejects.toMatchObject({ status: 401, code: 'unauthorized' });
    expect(calls).toHaveLength(2);
  });

  it('does not retry a 401 when no token was attached (the e2e /token stub yields null)', async () => {
    const { client, calls, getAccessToken } = makeClient({
      token: null,
      responses: [{ status: 401, body: { error: { code: 'unauthorized', message: 'nope' } } }],
    });
    await expect(client.getMe()).rejects.toMatchObject({ status: 401 });
    expect(calls).toHaveLength(1);
    expect(getAccessToken).toHaveBeenCalledTimes(1);
  });

  it('invalidateAccessToken() forces a re-mint on the next request', async () => {
    const { client, getAccessToken } = makeClient({ token: fresh(), responses: [{ status: 200, body: {} }] });
    await client.getMe();
    client.invalidateAccessToken();
    await client.getMe();
    expect(getAccessToken).toHaveBeenCalledTimes(2);
  });

  it('actAs() shares the parent cache', async () => {
    const { client, getAccessToken } = makeClient({ token: fresh(), responses: [{ status: 200, body: {} }] });
    await client.getMe();
    await client.actAs('user_principal').getThread('t1');
    expect(getAccessToken).toHaveBeenCalledTimes(1);
  });

  it('an explicit accessTokens option is used instead of a private cache', async () => {
    const { fetchFn } = createFakeFetch([{ status: 200, body: {} }]);
    const shared = { get: vi.fn(async () => fresh()), invalidate: vi.fn() };
    const getAccessToken = vi.fn(async () => 'never-called');
    const client = new ApiClient({ baseUrl: BASE_URL, getAccessToken, fetch: fetchFn as unknown as typeof fetch, accessTokens: shared });
    await client.getMe();
    client.invalidateAccessToken();
    expect(shared.get).toHaveBeenCalledTimes(1);
    expect(shared.invalidate).toHaveBeenCalledTimes(1);
    expect(getAccessToken).not.toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run the tests and watch them fail.**

```bash
bun run test:shared
```

Expected: `access-token-cache.test.ts` fails to resolve `./access-token-cache`; in `client.test.ts` the new block fails (`client.invalidateAccessToken is not a function`, retry assertions on `calls` length).

- [ ] **Step 3: Implement the cache.** Create `packages/shared/src/access-token-cache.ts`:

```ts
/**
 * JWT reuse for API calls (piece 2). Every ApiClient request used to mint a
 * fresh token at /api/auth/token, colliding with the per-IP /token rate limit
 * (60/60 s). The cache returns the last minted token while
 * `now < exp − 60 s`, shares one in-flight mint between concurrent callers,
 * and never caches `null` or a token without a decodable `exp`. `exp` is
 * decoded, NOT verified — the Go API verifies signatures; this only decides
 * when to ask for a new one.
 */

export const ACCESS_TOKEN_REFRESH_MARGIN_MS = 60_000;

export interface AccessTokenCache {
  /** The cached token while fresh, else the result of one shared mint. */
  get(): Promise<string | null>;
  /** Drops the cached token (401 retry, sign-out, server switch). */
  invalidate(): void;
}

const B64URL = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_';

/**
 * base64url → UTF-8 without atob/TextDecoder, so the same code runs in
 * browsers, Node and React Native. Returns null on any non-alphabet char.
 */
function decodeBase64Url(input: string): string | null {
  const bytes: number[] = [];
  let buffer = 0;
  let bits = 0;
  for (const ch of input.replace(/=+$/, '')) {
    const v = B64URL.indexOf(ch);
    if (v < 0) return null;
    buffer = (buffer << 6) | v;
    bits += 6;
    if (bits >= 8) {
      bits -= 8;
      bytes.push((buffer >> bits) & 0xff);
    }
  }
  // Decode UTF-8 by hand (the payload is small; claims may carry names).
  let out = '';
  for (let i = 0; i < bytes.length; ) {
    const b0 = bytes[i] ?? 0;
    if (b0 < 0x80) {
      out += String.fromCharCode(b0);
      i += 1;
    } else if (b0 < 0xe0) {
      out += String.fromCharCode(((b0 & 0x1f) << 6) | ((bytes[i + 1] ?? 0) & 0x3f));
      i += 2;
    } else if (b0 < 0xf0) {
      out += String.fromCharCode(((b0 & 0x0f) << 12) | (((bytes[i + 1] ?? 0) & 0x3f) << 6) | ((bytes[i + 2] ?? 0) & 0x3f));
      i += 3;
    } else {
      const cp = ((b0 & 0x07) << 18) | (((bytes[i + 1] ?? 0) & 0x3f) << 12) | (((bytes[i + 2] ?? 0) & 0x3f) << 6) | ((bytes[i + 3] ?? 0) & 0x3f);
      out += String.fromCodePoint(cp);
      i += 4;
    }
  }
  return out;
}

/** `exp` of an (unverified) JWT in milliseconds since the epoch, or null when absent or undecodable. */
export function decodeJwtExp(token: string): number | null {
  const parts = token.split('.');
  if (parts.length !== 3 || !parts[1]) return null;
  const json = decodeBase64Url(parts[1]);
  if (json === null) return null;
  try {
    const payload = JSON.parse(json) as { exp?: unknown };
    return typeof payload.exp === 'number' && Number.isFinite(payload.exp) ? payload.exp * 1000 : null;
  } catch {
    return null;
  }
}

export function createAccessTokenCache(
  mint: () => Promise<string | null>,
  now: () => number = () => Date.now()
): AccessTokenCache {
  let cached: { token: string; expMs: number } | null = null;
  let inflight: Promise<string | null> | null = null;

  return {
    get() {
      if (cached && now() < cached.expMs - ACCESS_TOKEN_REFRESH_MARGIN_MS) {
        return Promise.resolve(cached.token);
      }
      cached = null;
      if (inflight) return inflight;
      inflight = (async () => {
        try {
          const token = await mint();
          if (token) {
            const expMs = decodeJwtExp(token);
            if (expMs !== null && now() < expMs - ACCESS_TOKEN_REFRESH_MARGIN_MS) {
              cached = { token, expMs };
            }
          }
          return token;
        } finally {
          inflight = null;
        }
      })();
      return inflight;
    },
    invalidate() {
      cached = null;
    },
  };
}
```

- [ ] **Step 4: Wire the cache into `ApiClient`.** In `packages/shared/src/client.ts`:

(a) add `import { type AccessTokenCache, createAccessTokenCache } from './access-token-cache';` before the `from './types'` import block;

(b) replace `export interface ApiClientOptions { … }` with:

```ts
export interface ApiClientOptions {
  baseUrl: string;
  /** Returns the current Better Auth access token (JWT) or null when signed out. */
  getAccessToken: () => Promise<string | null>;
  fetch?: typeof fetch;
  /**
   * Shared JWT cache (internal). ApiClient creates one over getAccessToken
   * when absent; the web app passes its own so sign-out and the 401 retry
   * invalidate the single copy every caller uses.
   */
  accessTokens?: AccessTokenCache;
}
```

(c) replace the constructor and `request` with:

```ts
  constructor(private readonly opts: ApiClientOptions) {
    // Attach the cache to the SAME options object — never copy opts: desktop
    // passes a live `baseUrl` getter and mobile mutates `baseUrl` in place.
    opts.accessTokens ??= createAccessTokenCache(() => opts.getAccessToken());
  }

  /** Drops the cached JWT so the next request mints a fresh one (sign-out, server switch). */
  invalidateAccessToken(): void {
    this.opts.accessTokens?.invalidate();
  }

  private async request<T>(method: string, path: string, body?: unknown, retried = false): Promise<T> {
    const tokens = this.opts.accessTokens;
    const token = tokens ? await tokens.get() : await this.opts.getAccessToken();
    const doFetch = this.opts.fetch ?? fetch;
    const res = await doFetch(`${this.opts.baseUrl}${path}`, {
      method,
      headers: {
        'Content-Type': 'application/json',
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (res.status === 401 && token && tokens && !retried) {
      // A cached JWT may have been revoked (password reset) or just expired:
      // drop it, re-mint once, and retry once. A second 401 surfaces as-is.
      tokens.invalidate();
      return this.request<T>(method, path, body, true);
    }
    if (res.status === 204) return undefined as T;
    const json = await res.json().catch(() => null);
    if (!res.ok) {
      const code = json?.error?.code ?? 'unknown';
      const message = json?.error?.message ?? `Request failed with status ${res.status}`;
      throw new ApiRequestError(res.status, code, message);
    }
    return json as T;
  }
```

(`actAs` is unchanged: its `{ ...this.opts, fetch: wrapped }` spread copies the `accessTokens` reference, so the derived client shares the cache.)

- [ ] **Step 5: Extend the types and the barrel.** In `packages/shared/src/types.ts`:

(a) in the FIRST `export interface InstanceFeatures { … }` (the one with `billing/google/microsoft/ai/push`), after `push: boolean;` add:

```ts
  /**
   * An SMTP sender is configured (piece 2). Current servers always send it;
   * typed optional (like `maps`) so pre-piece-2 servers and fixtures stay
   * valid. Only an explicit `false` switches the web forgot-password page
   * to the administrator instructions.
   */
  email?: boolean;
```

(b) before `export interface TeamInvitation {` add:

```ts
/** How a team invitation went out (response-only, mirrors domain.InvitationDelivery). */
export type TeamInvitationDelivery = 'mailbox' | 'smtp' | 'link';
```

and inside `TeamInvitation`, after `createdAt: string;`, add:

```ts
  /**
   * Response-only: the inviter's connected mailbox, the instance SMTP sender,
   * or a link to share by hand (self-host without either).
   */
  delivery?: TeamInvitationDelivery;
  /** Present only when delivery === 'link': the accept URL the inviter must share. */
  inviteUrl?: string;
```

In `packages/shared/src/index.ts` add `export * from './access-token-cache';` after `export * from './client';`. In `packages/shared/src/client.ts` change the `invite` doc comment to `/** Admin+; emails the invite from the inviter's connected mailbox, else the instance SMTP sender, else returns delivery "link" with inviteUrl to share by hand. */`.

- [ ] **Step 6: Run the shared suite, typecheck, and Biome.**

```bash
bun run test:shared && bun run --cwd packages/shared typecheck && bunx biome check packages/shared
```

Expected: all shared tests pass (the pre-existing `methodCases` table still sees one call per case because its opaque `test-access-token` is never cached); typecheck and Biome clean.

- [ ] **Step 7: Commit.**

```bash
git add packages/shared/src/access-token-cache.ts packages/shared/src/access-token-cache.test.ts packages/shared/src/client.ts packages/shared/src/client.test.ts packages/shared/src/types.ts packages/shared/src/index.ts
git commit -m "feat(shared): JWT access-token cache with one 401 retry in ApiClient; features.email and invitation delivery in the contract" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 12: Sign-in page — `check-email` state, resend cooldown, verification/429 copy, "Forgot password?"

**Files:**
- Create: `apps/web/lib/auth-copy.ts`, `apps/web/components/auth/auth-shell.tsx`
- Modify: `apps/web/app/signin/page.tsx` (replace `SignInPage` and the imports; the icon components and `nextDestination` stay)
- Test: `apps/web/lib/auth-copy.test.ts`, `apps/web/app/signin/signin-page.test.tsx`

**Interfaces:**
- Consumes: `passwordPolicyError`, `PASSWORD_MIN_LENGTH` (Task 6).
- Produces: `AuthClientError { status?: number; code?: string; message?: string }`; consts `VERIFY_FIRST_MESSAGE = 'Verify your email first — we sent a new link.'`, `EMAIL_SEND_FAILED_MESSAGE = "We couldn't send the email. Try again in a minute."`, `RESEND_COOLDOWN_SECONDS = 60`; `rateLimitMessage(retryAfter): string` (`Too many attempts, try again in N s`, N = parsed header or 60); `describeAuthError(error, { fallback, retryAfter?, sendsMail? }): string`; `captureRetryAfter()` → `{ fetchOptions: { onError }, get value(): string | null }`; `AuthShell({ title, description?, children })` component.

- [ ] **Step 1: Write the failing copy tests.** Create `apps/web/lib/auth-copy.test.ts`:

```ts
import { describe, expect, it } from 'vitest';

import {
  EMAIL_SEND_FAILED_MESSAGE,
  VERIFY_FIRST_MESSAGE,
  captureRetryAfter,
  describeAuthError,
  rateLimitMessage,
} from '@/lib/auth-copy';

describe('rateLimitMessage', () => {
  it('uses the X-Retry-After value when present and 60 s otherwise', () => {
    expect(rateLimitMessage('42')).toBe('Too many attempts, try again in 42 s');
    expect(rateLimitMessage('0.4')).toBe('Too many attempts, try again in 1 s');
    expect(rateLimitMessage(null)).toBe('Too many attempts, try again in 60 s');
    expect(rateLimitMessage('soon')).toBe('Too many attempts, try again in 60 s');
  });
});

describe('describeAuthError', () => {
  it('maps 429, EMAIL_NOT_VERIFIED and mail-route 500s, else uses the server message or the fallback', () => {
    expect(describeAuthError({ status: 429 }, { fallback: 'f', retryAfter: '7' })).toBe('Too many attempts, try again in 7 s');
    expect(describeAuthError({ status: 403, code: 'EMAIL_NOT_VERIFIED', message: 'Email not verified' }, { fallback: 'f' })).toBe(VERIFY_FIRST_MESSAGE);
    expect(describeAuthError({ status: 500, message: 'Internal' }, { fallback: 'f', sendsMail: true })).toBe(EMAIL_SEND_FAILED_MESSAGE);
    expect(describeAuthError({ status: 500, message: 'Internal' }, { fallback: 'f' })).toBe('Internal');
    expect(describeAuthError({ status: 400, message: 'Password must not contain your email address.' }, { fallback: 'f' })).toBe('Password must not contain your email address.');
    expect(describeAuthError({ status: 400 }, { fallback: 'f' })).toBe('f');
    expect(describeAuthError(null, { fallback: 'f' })).toBe('f');
  });
});

describe('captureRetryAfter', () => {
  it('records the header from a Better Auth onError context', () => {
    const capture = captureRetryAfter();
    expect(capture.value).toBeNull();
    capture.fetchOptions.onError({ response: new Response(null, { status: 429, headers: { 'X-Retry-After': '12' } }) });
    expect(capture.value).toBe('12');
  });
});
```

- [ ] **Step 2: Implement the copy helpers.** Create `apps/web/lib/auth-copy.ts`:

```ts
/**
 * User-facing wording for Better Auth client errors, shared by the sign-in,
 * forgot/reset/verify pages and Settings → Account so every surface says the
 * same thing for the same failure.
 */

export interface AuthClientError {
  status?: number;
  code?: string;
  message?: string;
}

export const VERIFY_FIRST_MESSAGE = 'Verify your email first — we sent a new link.';
export const EMAIL_SEND_FAILED_MESSAGE = "We couldn't send the email. Try again in a minute.";
export const RESEND_COOLDOWN_SECONDS = 60;

/** 429 copy: N comes from Better Auth's X-Retry-After header, else the 60 s window. */
export function rateLimitMessage(retryAfter: string | null | undefined): string {
  const n = Number(retryAfter);
  const seconds = Number.isFinite(n) && n > 0 ? Math.ceil(n) : 60;
  return `Too many attempts, try again in ${seconds} s`;
}

export function describeAuthError(
  error: AuthClientError | null | undefined,
  opts: { fallback: string; retryAfter?: string | null; sendsMail?: boolean }
): string {
  if (!error) return opts.fallback;
  if (error.status === 429) return rateLimitMessage(opts.retryAfter);
  if (error.code === 'EMAIL_NOT_VERIFIED') return VERIFY_FIRST_MESSAGE;
  if (error.status === 500 && opts.sendsMail) return EMAIL_SEND_FAILED_MESSAGE;
  return error.message || opts.fallback;
}

/**
 * Better Auth's client only hands back `{status, code, message}`; the
 * X-Retry-After header is reachable through a per-call `onError` hook. Pass
 * `capture.fetchOptions` as the second argument of any auth call and read
 * `capture.value` afterwards.
 */
export function captureRetryAfter() {
  let value: string | null = null;
  return {
    fetchOptions: {
      onError: (ctx: { response: Response }) => {
        value = ctx.response.headers.get('x-retry-after');
      },
    },
    get value(): string | null {
      return value;
    },
  };
}
```

- [ ] **Step 3: Add the shared auth page shell.** Create `apps/web/components/auth/auth-shell.tsx`:

```tsx
import { CalendarRange } from 'lucide-react';
import type * as React from 'react';

/** Centered card used by the public auth pages (forgot/reset/verify). */
export function AuthShell({
  title,
  description,
  children,
}: {
  title: string;
  description?: string;
  children: React.ReactNode;
}) {
  return (
    <main className="relative flex min-h-svh flex-col items-center justify-center overflow-hidden px-6">
      <div
        aria-hidden
        className="absolute inset-0 -z-10 [background-image:radial-gradient(hsl(var(--border))_1px,transparent_1px)] [mask-image:radial-gradient(ellipse_60%_50%_at_50%_45%,black,transparent)] [background-size:24px_24px]"
      />
      <div className="flex w-full max-w-sm flex-col items-center">
        <div className="bg-primary text-primary-foreground flex size-12 items-center justify-center rounded-xl shadow-sm">
          <CalendarRange className="size-6" />
        </div>
        <h1 className="mt-6 text-2xl font-semibold tracking-tight">{title}</h1>
        {description && (
          <p className="text-muted-foreground mt-2 text-center text-sm text-balance">{description}</p>
        )}
        <div className="mt-8 flex w-full flex-col gap-3">{children}</div>
      </div>
    </main>
  );
}
```

- [ ] **Step 4: Write the failing sign-in page test.** Create `apps/web/app/signin/signin-page.test.tsx`:

```tsx
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const replaceMock = vi.fn();
vi.mock('next/navigation', () => ({ useRouter: () => ({ replace: replaceMock, push: vi.fn() }) }));
vi.mock('next/link', () => ({
  default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

const signInEmail = vi.fn();
const signUpEmail = vi.fn();
const sendVerificationEmail = vi.fn();
const useSessionMock = vi.fn(() => ({ data: null, isPending: false }));
vi.mock('@/lib/auth-client', () => ({
  authClient: {
    useSession: () => useSessionMock(),
    sendVerificationEmail: (...args: unknown[]) => sendVerificationEmail(...args),
  },
  signIn: { email: (...args: unknown[]) => signInEmail(...args), social: vi.fn() },
  signUp: { email: (...args: unknown[]) => signUpEmail(...args) },
}));

vi.mock('@/lib/use-instance', () => ({
  useInstance: () => ({ data: { authProviders: ['email'], features: { email: true } } }),
}));

const toastError = vi.fn();
const toastSuccess = vi.fn();
vi.mock('sonner', () => ({ toast: { error: (...a: unknown[]) => toastError(...a), success: (...a: unknown[]) => toastSuccess(...a) } }));

import SignInPage from './page';

async function fillAndSubmit(user: ReturnType<typeof userEvent.setup>, opts: { signup?: boolean } = {}) {
  if (opts.signup) await user.click(screen.getByRole('button', { name: "Don't have an account? Sign up" }));
  await user.type(screen.getByLabelText('Email'), 'ada@example.test');
  await user.type(screen.getByLabelText('Password'), 'correct-horse-battery');
  await user.click(screen.getByRole('button', { name: opts.signup ? 'Create account' : 'Sign in' }));
}

beforeEach(() => {
  vi.clearAllMocks();
  window.history.replaceState(null, '', '/signin');
});

afterEach(() => {
  vi.useRealTimers();
});

describe('SignInPage', () => {
  it('links to /forgot-password and requires a 10-character password', () => {
    render(<SignInPage />);
    expect(screen.getByRole('link', { name: 'Forgot password?' })).toHaveAttribute('href', '/forgot-password');
    expect(screen.getByLabelText('Password')).toHaveAttribute('minlength', '10');
  });

  it('sign-up with token === null shows "Check your inbox" with a 60 s resend cooldown, then resends', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    signUpEmail.mockResolvedValue({ data: { token: null, user: { email: 'ada@example.test' } }, error: null });
    sendVerificationEmail.mockResolvedValue({ data: { status: true }, error: null });
    render(<SignInPage />);

    await fillAndSubmit(user, { signup: true });

    expect(signUpEmail).toHaveBeenCalledWith(
      expect.objectContaining({ email: 'ada@example.test', password: 'correct-horse-battery', callbackURL: '/verify-email' }),
      expect.anything()
    );
    expect(await screen.findByText('Check your inbox')).toBeInTheDocument();
    expect(screen.getByText(/ada@example\.test/)).toBeInTheDocument();
    const resend = screen.getByRole('button', { name: /Resend in 60 s/ });
    expect(resend).toBeDisabled();
    expect(replaceMock).not.toHaveBeenCalled();

    await act(async () => {
      vi.advanceTimersByTime(60_000);
    });
    const ready = await screen.findByRole('button', { name: 'Resend email' });
    expect(ready).toBeEnabled();
    await user.click(ready);
    expect(sendVerificationEmail).toHaveBeenCalledWith({ email: 'ada@example.test', callbackURL: '/verify-email' }, expect.anything());
    expect(await screen.findByRole('button', { name: /Resend in 60 s/ })).toBeDisabled();
  });

  it('sign-up that returns a session goes straight to the inbox (SMTP off)', async () => {
    const user = userEvent.setup();
    signUpEmail.mockResolvedValue({ data: { token: 'sess', user: {} }, error: null });
    render(<SignInPage />);
    await fillAndSubmit(user, { signup: true });
    await waitFor(() => expect(replaceMock).toHaveBeenCalledWith('/mail'));
  });

  it('sign-in 403 EMAIL_NOT_VERIFIED switches to the check-email notice with the verify-first copy', async () => {
    const user = userEvent.setup();
    signInEmail.mockResolvedValue({ data: null, error: { status: 403, code: 'EMAIL_NOT_VERIFIED', message: 'Email not verified' } });
    render(<SignInPage />);
    await fillAndSubmit(user);
    expect(await screen.findByText('Verify your email first — we sent a new link.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Resend in 60 s/ })).toBeDisabled();
    expect(replaceMock).not.toHaveBeenCalled();
  });

  it('sign-in 429 shows the retry-after copy from the header', async () => {
    const user = userEvent.setup();
    signInEmail.mockImplementation(async (_body: unknown, opts: { onError: (ctx: { response: Response }) => void }) => {
      opts.onError({ response: new Response(null, { status: 429, headers: { 'X-Retry-After': '42' } }) });
      return { data: null, error: { status: 429, message: 'Too many requests. Please try again later.' } };
    });
    render(<SignInPage />);
    await fillAndSubmit(user);
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('Too many attempts, try again in 42 s'));
  });

  it('sign-in success passes the destination as callbackURL and navigates', async () => {
    const user = userEvent.setup();
    window.history.replaceState(null, '', '/signin?next=/calendar');
    signInEmail.mockResolvedValue({ data: { token: 'sess' }, error: null });
    render(<SignInPage />);
    await fillAndSubmit(user);
    expect(signInEmail).toHaveBeenCalledWith(expect.objectContaining({ email: 'ada@example.test', callbackURL: '/calendar' }), expect.anything());
    await waitFor(() => expect(replaceMock).toHaveBeenCalledWith('/calendar'));
  });
});
```

- [ ] **Step 5: Run the tests and watch them fail.**

```bash
cd apps/web && bunx vitest run lib/auth-copy.test.ts app/signin/signin-page.test.tsx
```

Expected: `auth-copy.test.ts` cannot resolve `@/lib/auth-copy`; the page test fails on the missing link, `minlength`, and the `Check your inbox` state.

- [ ] **Step 6: Rewrite the page.** In `apps/web/app/signin/page.tsx` replace the import block with:

```tsx
'use client';

import * as React from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { CalendarRange, Loader2, MailCheck, Zap } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Kbd } from '@/components/ui/kbd';
import { Label } from '@/components/ui/label';
import { Separator } from '@/components/ui/separator';
import { authClient, signIn, signUp } from '@/lib/auth-client';
import { RESEND_COOLDOWN_SECONDS, captureRetryAfter, describeAuthError } from '@/lib/auth-copy';
import { PASSWORD_MIN_LENGTH, passwordPolicyError } from '@/lib/auth-env';
import { useInstance } from '@/lib/use-instance';
import { cn } from '@/lib/utils';
```

keep `nextDestination`, `PROVIDER_LABELS`, `GoogleIcon`, `AppleIcon`, `SocialProvider` and `Pending` exactly as they are, and replace `export default function SignInPage() { … }` (to the end of the file) with:

```tsx
type Mode = 'signin' | 'signup' | 'check-email';

/** Second-based countdown; returns the remaining seconds and a restart function. */
function useCooldown(seconds: number): [number, () => void] {
  const [remaining, setRemaining] = React.useState(0);
  React.useEffect(() => {
    if (remaining <= 0) return;
    const id = window.setInterval(() => setRemaining((r) => (r > 0 ? r - 1 : 0)), 1000);
    return () => window.clearInterval(id);
  }, [remaining]);
  const restart = React.useCallback(() => setRemaining(seconds), [seconds]);
  return [remaining, restart];
}

export default function SignInPage() {
  const router = useRouter();
  const [mode, setMode] = React.useState<Mode>('signin');
  const [pending, setPending] = React.useState<Pending>(null);
  const [name, setName] = React.useState('');
  const [email, setEmail] = React.useState('');
  const [password, setPassword] = React.useState('');
  // check-email state: which address we told the user to look at, and why.
  const [checkEmail, setCheckEmail] = React.useState('');
  const [notice, setNotice] = React.useState<string | null>(null);
  const [cooldown, restartCooldown] = useCooldown(RESEND_COOLDOWN_SECONDS);

  // Only offer the social providers the server actually advertises (a
  // self-host without Google/Apple creds omits them from /v1/instance).
  const { data: instance } = useInstance();
  const providers = instance?.authProviders ?? [];
  const showGoogle = providers.includes('google');
  const showApple = providers.includes('apple');
  const showSocial = showGoogle || showApple;

  // Already signed in → honor ?next=, else straight to the inbox.
  const { data: session } = authClient.useSession();
  React.useEffect(() => {
    if (session) router.replace(nextDestination());
  }, [session, router]);

  function showCheckEmail(address: string, message: string | null) {
    setCheckEmail(address);
    setNotice(message);
    setMode('check-email');
    restartCooldown();
  }

  async function social(provider: SocialProvider) {
    if (!providers.includes(provider)) {
      toast.error(`${PROVIDER_LABELS[provider]} sign-in isn't enabled on this server.`);
      return;
    }
    setPending(provider);
    try {
      const { error } = await signIn.social({ provider, callbackURL: nextDestination() });
      if (error) throw new Error(error.message ?? 'Sign-in failed');
      // Success → the browser is being redirected to the provider.
    } catch (err) {
      setPending(null);
      toast.error(err instanceof Error ? err.message : 'Could not start sign-in. Try again.');
    }
  }

  async function submitEmail(e: React.FormEvent) {
    e.preventDefault();
    if (mode === 'signup') {
      const violation = passwordPolicyError(password, email);
      if (violation) {
        toast.error(violation.message);
        return;
      }
    }
    setPending('email');
    const retry = captureRetryAfter();
    try {
      if (mode === 'signup') {
        const { data, error } = await signUp.email(
          { name: name || email, email, password, callbackURL: '/verify-email' },
          retry.fetchOptions
        );
        if (error) {
          toast.error(describeAuthError(error, { fallback: 'Could not create account', retryAfter: retry.value, sendsMail: true }));
          return;
        }
        // With SMTP configured the server never signs a new account in:
        // `token === null` for a new address AND for an existing one (no
        // enumeration), so both land on the same "check your inbox" screen.
        if (data && data.token === null) {
          showCheckEmail(email, null);
          return;
        }
      } else {
        const { error } = await signIn.email({ email, password, callbackURL: nextDestination() }, retry.fetchOptions);
        if (error) {
          if (error.code === 'EMAIL_NOT_VERIFIED') {
            // The server re-sent the verification link (sendOnSignIn).
            showCheckEmail(email, describeAuthError(error, { fallback: 'Invalid email or password' }));
            return;
          }
          toast.error(describeAuthError(error, { fallback: 'Invalid email or password', retryAfter: retry.value }));
          return;
        }
      }
      router.replace(nextDestination());
    } finally {
      setPending(null);
    }
  }

  async function resend() {
    setPending('email');
    const retry = captureRetryAfter();
    try {
      const { error } = await authClient.sendVerificationEmail({ email: checkEmail, callbackURL: '/verify-email' }, retry.fetchOptions);
      if (error) {
        toast.error(describeAuthError(error, { fallback: 'Could not resend the email', retryAfter: retry.value, sendsMail: true }));
        return;
      }
      restartCooldown();
    } finally {
      setPending(null);
    }
  }

  const busy = pending !== null;

  return (
    <main className="relative flex min-h-svh flex-col items-center justify-center overflow-hidden px-6">
      {/* Faint dot grid backdrop */}
      <div
        aria-hidden
        className="absolute inset-0 -z-10 [background-image:radial-gradient(hsl(var(--border))_1px,transparent_1px)] [mask-image:radial-gradient(ellipse_60%_50%_at_50%_45%,black,transparent)] [background-size:24px_24px]"
      />

      <div className="flex w-full max-w-sm flex-col items-center">
        <div className="bg-primary text-primary-foreground flex size-12 items-center justify-center rounded-xl shadow-sm">
          <CalendarRange className="size-6" />
        </div>

        {mode === 'check-email' ? (
          <>
            <h1 className="mt-6 text-2xl font-semibold tracking-tight">Check your inbox</h1>
            <p className="text-muted-foreground mt-2 text-center text-sm text-balance" role="status">
              {notice ?? 'If that address is new to Calendium, we sent a verification link to'}{' '}
              <span className="text-foreground font-medium">{checkEmail}</span>. Open it to continue.
            </p>
            <MailCheck className="text-muted-foreground mt-8 size-8" aria-hidden />
            <Button
              variant="outline"
              size="lg"
              className="mt-6 w-full"
              disabled={busy || cooldown > 0}
              onClick={() => void resend()}>
              {pending === 'email' ? <Loader2 className="animate-spin" /> : null}
              {cooldown > 0 ? `Resend in ${cooldown} s` : 'Resend email'}
            </Button>
            <button
              type="button"
              className="text-muted-foreground hover:text-foreground mt-4 text-xs transition-colors"
              onClick={() => setMode('signin')}
              disabled={busy}>
              Back to sign in
            </button>
          </>
        ) : (
          <>
            <h1 className="mt-6 text-2xl font-semibold tracking-tight">Calendium</h1>
            <p className="text-muted-foreground mt-2 text-center text-sm text-balance">
              The fastest email and calendar experience. One inbox, one calendar, zero friction.
            </p>

            {showSocial && (
              <>
                <div className="mt-8 flex w-full flex-col gap-2.5">
                  {showGoogle && (
                    <Button variant="outline" size="lg" className="w-full" disabled={busy} onClick={() => social('google')}>
                      {pending === 'google' ? <Loader2 className="animate-spin" /> : <GoogleIcon className="size-4" />}
                      Continue with Google
                    </Button>
                  )}
                  {showApple && (
                    <Button variant="outline" size="lg" className="w-full" disabled={busy} onClick={() => social('apple')}>
                      {pending === 'apple' ? <Loader2 className="animate-spin" /> : <AppleIcon className="size-4" />}
                      Continue with Apple
                    </Button>
                  )}
                </div>

                <div className="mt-6 flex w-full items-center gap-3">
                  <Separator className="flex-1" />
                  <span className="text-muted-foreground text-xs">or with email</span>
                  <Separator className="flex-1" />
                </div>
              </>
            )}

            <form onSubmit={submitEmail} className={cn('flex w-full flex-col gap-3', showSocial ? 'mt-6' : 'mt-8')}>
              {mode === 'signup' && (
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="name">Name</Label>
                  <Input id="name" type="text" autoComplete="name" placeholder="Ada Lovelace" value={name} onChange={(e) => setName(e.target.value)} disabled={busy} />
                </div>
              )}
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="email">Email</Label>
                <Input id="email" type="email" autoComplete="email" required placeholder="you@calendium.app" value={email} onChange={(e) => setEmail(e.target.value)} disabled={busy} />
              </div>
              <div className="flex flex-col gap-1.5">
                <div className="flex items-center justify-between">
                  <Label htmlFor="password">Password</Label>
                  {mode === 'signin' && (
                    <Link href="/forgot-password" className="text-muted-foreground hover:text-foreground text-xs underline-offset-2 hover:underline">
                      Forgot password?
                    </Link>
                  )}
                </div>
                <Input
                  id="password"
                  type="password"
                  autoComplete={mode === 'signup' ? 'new-password' : 'current-password'}
                  required
                  minLength={PASSWORD_MIN_LENGTH}
                  placeholder="••••••••••"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  disabled={busy}
                />
                {mode === 'signup' && (
                  <p className="text-muted-foreground text-xs">At least {PASSWORD_MIN_LENGTH} characters, and not your email address.</p>
                )}
              </div>
              <Button type="submit" size="lg" className="w-full" disabled={busy}>
                {pending === 'email' ? <Loader2 className="animate-spin" /> : null}
                {mode === 'signup' ? 'Create account' : 'Sign in'}
              </Button>
            </form>

            <button
              type="button"
              className="text-muted-foreground hover:text-foreground mt-4 text-xs transition-colors"
              onClick={() => setMode((m) => (m === 'signin' ? 'signup' : 'signin'))}
              disabled={busy}>
              {mode === 'signin' ? "Don't have an account? Sign up" : 'Already have an account? Sign in'}
            </button>

            <div className="text-muted-foreground mt-6 flex items-center gap-2 text-xs">
              <Zap className="size-3.5" />
              <span>
                Every action is a keystroke away — hit <Kbd size="sm">⌘</Kbd> <Kbd size="sm">K</Kbd> once you're in.
              </span>
            </div>

            <p className="text-muted-foreground mt-8 text-center text-xs text-balance">
              14-day free trial, then $50/year. By continuing you agree to the{' '}
              <Link href="/terms" className="hover:text-foreground underline underline-offset-2">Terms</Link> and{' '}
              <Link href="/privacy" className="hover:text-foreground underline underline-offset-2">Privacy Policy</Link>.
            </p>
          </>
        )}
      </div>
    </main>
  );
}
```

- [ ] **Step 7: Run the tests, typecheck and Biome.**

```bash
cd apps/web && bunx vitest run lib/auth-copy.test.ts app/signin && bunx tsc --noEmit && cd ../.. && bunx biome check apps/web/app/signin apps/web/lib/auth-copy.ts apps/web/components/auth
```

Expected: all pass; typecheck and Biome clean.

- [ ] **Step 8: Commit.**

```bash
git add apps/web/lib/auth-copy.ts apps/web/lib/auth-copy.test.ts apps/web/components/auth/auth-shell.tsx apps/web/app/signin/page.tsx apps/web/app/signin/signin-page.test.tsx
git commit -m "feat(web): sign-in page gains the check-your-inbox state with resend cooldown, verification and rate-limit copy, and a forgot-password link" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 13: `/forgot-password` and `/reset-password` pages

**Files:**
- Create: `apps/web/app/forgot-password/page.tsx`, `apps/web/app/reset-password/page.tsx`
- Test: `apps/web/app/forgot-password/forgot-password.test.tsx`, `apps/web/app/reset-password/reset-password.test.tsx`

**Interfaces:**
- Consumes: `AuthShell` (Task 12), `captureRetryAfter`, `describeAuthError` (Task 12), `passwordPolicyError`, `PASSWORD_MIN_LENGTH` (Task 6), `useInstance`, `authClient`.
- Produces: the two routes. Page copy (exact): generic confirmation `If an account exists for that address, we sent a link.`; admin notice heading `Password reset by email isn't available on this server`; reset success toast `Password updated. Sign in with your new password.`; invalid link heading `This link is invalid or has expired`.

- [ ] **Step 1: Write the failing page tests.** Create `apps/web/app/forgot-password/forgot-password.test.tsx`:

```tsx
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('next/link', () => ({
  default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

const requestPasswordReset = vi.fn();
vi.mock('@/lib/auth-client', () => ({
  authClient: { requestPasswordReset: (...args: unknown[]) => requestPasswordReset(...args) },
}));

const instanceState = vi.hoisted(() => ({ value: { data: undefined as unknown, isError: false } }));
vi.mock('@/lib/use-instance', () => ({ useInstance: () => instanceState.value }));

const toastError = vi.fn();
vi.mock('sonner', () => ({ toast: { error: (...a: unknown[]) => toastError(...a), success: vi.fn() } }));

import ForgotPasswordPage from './page';

beforeEach(() => {
  vi.clearAllMocks();
  instanceState.value = { data: { features: { email: true } }, isError: false };
});

describe('ForgotPasswordPage', () => {
  it('submits the email with redirectTo=/reset-password and shows the generic confirmation', async () => {
    const user = userEvent.setup();
    requestPasswordReset.mockResolvedValue({ data: { status: true }, error: null });
    render(<ForgotPasswordPage />);
    await user.type(screen.getByLabelText('Email'), 'ada@example.test');
    await user.click(screen.getByRole('button', { name: 'Send reset link' }));
    expect(requestPasswordReset).toHaveBeenCalledWith({ email: 'ada@example.test', redirectTo: '/reset-password' }, expect.anything());
    expect(await screen.findByText('If an account exists for that address, we sent a link.')).toBeInTheDocument();
    expect(screen.queryByLabelText('Email')).not.toBeInTheDocument();
  });

  it('shows the same confirmation whatever the server answered for an unknown address (no enumeration)', async () => {
    const user = userEvent.setup();
    requestPasswordReset.mockResolvedValue({ data: { status: true, message: 'If this email exists in our system, check your email for the reset link' }, error: null });
    render(<ForgotPasswordPage />);
    await user.type(screen.getByLabelText('Email'), 'nobody@example.test');
    await user.click(screen.getByRole('button', { name: 'Send reset link' }));
    expect(await screen.findByText('If an account exists for that address, we sent a link.')).toBeInTheDocument();
  });

  it('renders the administrator instructions when features.email === false', () => {
    instanceState.value = { data: { features: { email: false } }, isError: false };
    render(<ForgotPasswordPage />);
    expect(screen.getByText("Password reset by email isn't available on this server")).toBeInTheDocument();
    expect(screen.getByText(/reset-password\.mjs/)).toBeInTheDocument();
    expect(screen.queryByLabelText('Email')).not.toBeInTheDocument();
  });

  it('renders the form when the instance is unavailable or does not report features.email', () => {
    instanceState.value = { data: undefined, isError: true };
    const { unmount } = render(<ForgotPasswordPage />);
    expect(screen.getByLabelText('Email')).toBeInTheDocument();
    unmount();
    instanceState.value = { data: { features: {} }, isError: false };
    render(<ForgotPasswordPage />);
    expect(screen.getByLabelText('Email')).toBeInTheDocument();
  });

  it('maps 429 and 500 to the shared copy', async () => {
    const user = userEvent.setup();
    requestPasswordReset.mockImplementationOnce(async (_b: unknown, o: { onError: (c: { response: Response }) => void }) => {
      o.onError({ response: new Response(null, { status: 429, headers: { 'X-Retry-After': '9' } }) });
      return { data: null, error: { status: 429 } };
    });
    render(<ForgotPasswordPage />);
    await user.type(screen.getByLabelText('Email'), 'ada@example.test');
    await user.click(screen.getByRole('button', { name: 'Send reset link' }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('Too many attempts, try again in 9 s'));

    requestPasswordReset.mockResolvedValueOnce({ data: null, error: { status: 500, message: 'Internal Server Error' } });
    await user.click(screen.getByRole('button', { name: 'Send reset link' }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith("We couldn't send the email. Try again in a minute."));
  });
});
```

Create `apps/web/app/reset-password/reset-password.test.tsx`:

```tsx
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const replaceMock = vi.fn();
vi.mock('next/navigation', () => ({ useRouter: () => ({ replace: replaceMock, push: vi.fn() }) }));
vi.mock('next/link', () => ({
  default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

const resetPassword = vi.fn();
vi.mock('@/lib/auth-client', () => ({ authClient: { resetPassword: (...args: unknown[]) => resetPassword(...args) } }));

const toastError = vi.fn();
const toastSuccess = vi.fn();
vi.mock('sonner', () => ({ toast: { error: (...a: unknown[]) => toastError(...a), success: (...a: unknown[]) => toastSuccess(...a) } }));

import ResetPasswordPage from './page';

beforeEach(() => {
  vi.clearAllMocks();
});

async function fill(user: ReturnType<typeof userEvent.setup>, next: string, confirm = next) {
  await user.type(await screen.findByLabelText('New password'), next);
  await user.type(screen.getByLabelText('Confirm new password'), confirm);
  await user.click(screen.getByRole('button', { name: 'Update password' }));
}

describe('ResetPasswordPage', () => {
  it('with ?token= resets, toasts and goes to /signin', async () => {
    window.history.replaceState(null, '', '/reset-password?token=tok-123');
    resetPassword.mockResolvedValue({ data: { status: true }, error: null });
    const user = userEvent.setup();
    render(<ResetPasswordPage />);
    await fill(user, 'correct-horse-battery');
    expect(resetPassword).toHaveBeenCalledWith({ newPassword: 'correct-horse-battery', token: 'tok-123' }, expect.anything());
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith('Password updated. Sign in with your new password.'));
    expect(replaceMock).toHaveBeenCalledWith('/signin');
  });

  it('rejects a mismatch and a policy violation client-side without calling the server', async () => {
    window.history.replaceState(null, '', '/reset-password?token=tok-123');
    const user = userEvent.setup();
    render(<ResetPasswordPage />);
    await fill(user, 'correct-horse-battery', 'correct-horse-batterx');
    expect(await screen.findByText("Passwords don't match.")).toBeInTheDocument();
    await user.clear(screen.getByLabelText('New password'));
    await user.clear(screen.getByLabelText('Confirm new password'));
    await fill(user, 'short');
    expect(await screen.findByText('Password must be at least 10 characters.')).toBeInTheDocument();
    expect(resetPassword).not.toHaveBeenCalled();
  });

  it('with ?error=INVALID_TOKEN shows the recovery path instead of the form', async () => {
    window.history.replaceState(null, '', '/reset-password?error=INVALID_TOKEN');
    render(<ResetPasswordPage />);
    expect(await screen.findByText('This link is invalid or has expired')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Request a new link' })).toHaveAttribute('href', '/forgot-password');
    expect(screen.queryByLabelText('New password')).not.toBeInTheDocument();
  });

  it('without a token behaves like an invalid link', async () => {
    window.history.replaceState(null, '', '/reset-password');
    render(<ResetPasswordPage />);
    expect(await screen.findByText('This link is invalid or has expired')).toBeInTheDocument();
  });

  it('a reused token (server INVALID_TOKEN) toasts and shows the recovery path', async () => {
    window.history.replaceState(null, '', '/reset-password?token=used');
    resetPassword.mockResolvedValue({ data: null, error: { status: 400, code: 'INVALID_TOKEN', message: 'Invalid token' } });
    const user = userEvent.setup();
    render(<ResetPasswordPage />);
    await fill(user, 'correct-horse-battery');
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('This link is invalid or has expired. Request a new one.'));
    expect(await screen.findByRole('link', { name: 'Request a new link' })).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run them and watch them fail.**

```bash
cd apps/web && bunx vitest run app/forgot-password app/reset-password
```

Expected: `Failed to resolve import "./page"` in both.

- [ ] **Step 3: Implement the forgot-password page.** Create `apps/web/app/forgot-password/page.tsx`:

```tsx
'use client';

import * as React from 'react';
import Link from 'next/link';
import { Loader2 } from 'lucide-react';
import { toast } from 'sonner';

import { AuthShell } from '@/components/auth/auth-shell';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { authClient } from '@/lib/auth-client';
import { captureRetryAfter, describeAuthError } from '@/lib/auth-copy';
import { useInstance } from '@/lib/use-instance';

const BACK_TO_SIGN_IN = (
  <Link href="/signin" className="text-muted-foreground hover:text-foreground mt-2 text-center text-xs underline-offset-2 hover:underline">
    Back to sign in
  </Link>
);

/**
 * Self-service password reset entry. The confirmation is deliberately the
 * same whether or not the address exists (Better Auth answers generically
 * and in constant time). When the server reports `features.email === false`
 * (self-host without SMTP) the page explains the administrator path instead
 * — any other instance state (unknown, unreachable, pre-piece-2) shows the
 * form.
 */
export default function ForgotPasswordPage() {
  const { data: instance } = useInstance();
  const emailDisabled = instance?.features?.email === false;
  const [email, setEmail] = React.useState('');
  const [sent, setSent] = React.useState(false);
  const [pending, setPending] = React.useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setPending(true);
    const retry = captureRetryAfter();
    try {
      const { error } = await authClient.requestPasswordReset({ email, redirectTo: '/reset-password' }, retry.fetchOptions);
      if (error) {
        toast.error(describeAuthError(error, { fallback: 'Something went wrong. Try again.', retryAfter: retry.value, sendsMail: true }));
        return;
      }
      setSent(true);
    } finally {
      setPending(false);
    }
  }

  if (emailDisabled) {
    return (
      <AuthShell title="Password reset by email isn't available on this server">
        <p className="text-muted-foreground text-sm">
          This Calendium instance has no outgoing email configured. Ask your administrator to reset your password; on the server they run:
        </p>
        <pre className="bg-muted overflow-x-auto rounded-md p-3 text-xs">
          docker compose exec web node apps/web/scripts/reset-password.mjs &lt;your email&gt;
        </pre>
        <p className="text-muted-foreground text-sm">
          It prints a temporary password and signs out every device. Sign in with it, then change it in Settings → Account.
        </p>
        {BACK_TO_SIGN_IN}
      </AuthShell>
    );
  }

  if (sent) {
    return (
      <AuthShell title="Check your inbox">
        <p className="text-muted-foreground text-center text-sm" role="status">
          If an account exists for that address, we sent a link.
        </p>
        <p className="text-muted-foreground text-center text-xs">The link expires in 1 hour.</p>
        {BACK_TO_SIGN_IN}
      </AuthShell>
    );
  }

  return (
    <AuthShell title="Forgot your password?" description="Enter your email and we'll send you a link to choose a new one.">
      <form onSubmit={submit} className="flex flex-col gap-3">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="email">Email</Label>
          <Input id="email" type="email" autoComplete="email" required placeholder="you@calendium.app" value={email} onChange={(e) => setEmail(e.target.value)} disabled={pending} />
        </div>
        <Button type="submit" size="lg" className="w-full" disabled={pending}>
          {pending ? <Loader2 className="animate-spin" /> : null}
          Send reset link
        </Button>
      </form>
      {BACK_TO_SIGN_IN}
    </AuthShell>
  );
}
```

- [ ] **Step 4: Implement the reset-password page.** Create `apps/web/app/reset-password/page.tsx`:

```tsx
'use client';

import * as React from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { Loader2 } from 'lucide-react';
import { toast } from 'sonner';

import { AuthShell } from '@/components/auth/auth-shell';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { authClient } from '@/lib/auth-client';
import { captureRetryAfter, describeAuthError } from '@/lib/auth-copy';
import { PASSWORD_MIN_LENGTH, passwordPolicyError } from '@/lib/auth-env';

const INVALID_LINK_TOAST = 'This link is invalid or has expired. Request a new one.';

/**
 * Landing page of the emailed reset link. Better Auth validates the token at
 * /api/auth/reset-password/<token> and redirects here with ?token= (valid)
 * or ?error=INVALID_TOKEN. The query is read after mount (this is a client
 * page rendered on the server too) so there is no hydration mismatch.
 */
export default function ResetPasswordPage() {
  const router = useRouter();
  const [params, setParams] = React.useState<{ token: string | null; error: string | null } | null>(null);
  const [next, setNext] = React.useState('');
  const [confirm, setConfirm] = React.useState('');
  const [formError, setFormError] = React.useState<string | null>(null);
  const [pending, setPending] = React.useState(false);
  const [invalid, setInvalid] = React.useState(false);

  React.useEffect(() => {
    const sp = new URLSearchParams(window.location.search);
    setParams({ token: sp.get('token'), error: sp.get('error') });
  }, []);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);
    // The email is unknown on this page; the server hook also checks the
    // local-part rule against the token's user.
    const violation = passwordPolicyError(next, null);
    if (violation) {
      setFormError(violation.message);
      return;
    }
    if (next !== confirm) {
      setFormError("Passwords don't match.");
      return;
    }
    setPending(true);
    const retry = captureRetryAfter();
    try {
      const { error } = await authClient.resetPassword({ newPassword: next, token: params?.token ?? '' }, retry.fetchOptions);
      if (error) {
        if (error.code === 'INVALID_TOKEN') {
          toast.error(INVALID_LINK_TOAST);
          setInvalid(true);
          return;
        }
        setFormError(describeAuthError(error, { fallback: 'Could not update your password. Try again.', retryAfter: retry.value }));
        return;
      }
      toast.success('Password updated. Sign in with your new password.');
      router.replace('/signin');
    } finally {
      setPending(false);
    }
  }

  if (!params) {
    return (
      <AuthShell title="Reset your password">
        <Loader2 className="text-muted-foreground mx-auto size-5 animate-spin" aria-label="Loading" />
      </AuthShell>
    );
  }

  if (invalid || params.error || !params.token) {
    return (
      <AuthShell title="This link is invalid or has expired" description="Reset links work once and expire after 1 hour.">
        <Button asChild size="lg" className="w-full">
          <Link href="/forgot-password">Request a new link</Link>
        </Button>
        <Link href="/signin" className="text-muted-foreground hover:text-foreground mt-2 text-center text-xs underline-offset-2 hover:underline">
          Back to sign in
        </Link>
      </AuthShell>
    );
  }

  return (
    <AuthShell title="Choose a new password" description="All your other sessions will be signed out.">
      <form onSubmit={submit} className="flex flex-col gap-3">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="new-password">New password</Label>
          <Input id="new-password" type="password" autoComplete="new-password" required minLength={PASSWORD_MIN_LENGTH} value={next} onChange={(e) => setNext(e.target.value)} disabled={pending} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="confirm-password">Confirm new password</Label>
          <Input id="confirm-password" type="password" autoComplete="new-password" required minLength={PASSWORD_MIN_LENGTH} value={confirm} onChange={(e) => setConfirm(e.target.value)} disabled={pending} />
        </div>
        {formError && (
          <p className="text-destructive text-sm" role="alert">
            {formError}
          </p>
        )}
        <Button type="submit" size="lg" className="w-full" disabled={pending}>
          {pending ? <Loader2 className="animate-spin" /> : null}
          Update password
        </Button>
      </form>
    </AuthShell>
  );
}
```

(`Button` already supports `asChild` via `@radix-ui/react-slot` — see `apps/web/components/ui/button.tsx`.)

- [ ] **Step 5: Run the tests, typecheck and Biome.**

```bash
cd apps/web && bunx vitest run app/forgot-password app/reset-password && bunx tsc --noEmit && cd ../.. && bunx biome check apps/web/app/forgot-password apps/web/app/reset-password
```

Expected: both files pass; typecheck and Biome clean.

- [ ] **Step 6: Commit.**

```bash
git add apps/web/app/forgot-password apps/web/app/reset-password
git commit -m "feat(web): forgot-password and reset-password pages with the self-host admin fallback" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 14: `/verify-email` page

**Files:**
- Create: `apps/web/app/verify-email/page.tsx`
- Test: `apps/web/app/verify-email/verify-email.test.tsx`

**Interfaces:**
- Consumes: `AuthShell`, `captureRetryAfter`, `describeAuthError` (Task 12), `authClient.useSession`, `authClient.sendVerificationEmail`.
- Produces: the route with three states — `?error=` → `This link has expired or was already used` + resend form; session → `Email verified` + `Continue` (→ `/mail`); no session → `Email verified` + `Sign in to continue` (→ `/signin`).

- [ ] **Step 1: Write the failing test.** Create `apps/web/app/verify-email/verify-email.test.tsx`:

```tsx
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const replaceMock = vi.fn();
vi.mock('next/navigation', () => ({ useRouter: () => ({ replace: replaceMock, push: vi.fn() }) }));
vi.mock('next/link', () => ({
  default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

const sendVerificationEmail = vi.fn();
const sessionState = vi.hoisted(() => ({ value: { data: null as unknown, isPending: false } }));
vi.mock('@/lib/auth-client', () => ({
  authClient: {
    useSession: () => sessionState.value,
    sendVerificationEmail: (...args: unknown[]) => sendVerificationEmail(...args),
  },
}));

const toastError = vi.fn();
vi.mock('sonner', () => ({ toast: { error: (...a: unknown[]) => toastError(...a), success: vi.fn() } }));

import VerifyEmailPage from './page';

beforeEach(() => {
  vi.clearAllMocks();
  sessionState.value = { data: null, isPending: false };
});

describe('VerifyEmailPage', () => {
  it('with a session: Verified + Continue to /mail', async () => {
    window.history.replaceState(null, '', '/verify-email');
    sessionState.value = { data: { user: { id: 'u1' } }, isPending: false };
    const user = userEvent.setup();
    render(<VerifyEmailPage />);
    expect(await screen.findByText('Email verified')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Continue' }));
    expect(replaceMock).toHaveBeenCalledWith('/mail');
  });

  it('without a session: Verified + sign in link', async () => {
    window.history.replaceState(null, '', '/verify-email');
    render(<VerifyEmailPage />);
    expect(await screen.findByText('Email verified')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Sign in to continue' })).toHaveAttribute('href', '/signin');
  });

  it.each(['TOKEN_EXPIRED', 'INVALID_TOKEN'])('with ?error=%s: expired copy and a resend form that calls sendVerificationEmail', async (code) => {
    window.history.replaceState(null, '', `/verify-email?error=${code}`);
    sendVerificationEmail.mockResolvedValue({ data: { status: true }, error: null });
    const user = userEvent.setup();
    render(<VerifyEmailPage />);
    expect(await screen.findByText('This link has expired or was already used')).toBeInTheDocument();
    await user.type(screen.getByLabelText('Email'), 'ada@example.test');
    await user.click(screen.getByRole('button', { name: 'Send a new link' }));
    expect(sendVerificationEmail).toHaveBeenCalledWith({ email: 'ada@example.test', callbackURL: '/verify-email' }, expect.anything());
    expect(await screen.findByText('If an account exists for that address, we sent a new link.')).toBeInTheDocument();
  });

  it('resend failures use the shared copy', async () => {
    window.history.replaceState(null, '', '/verify-email?error=INVALID_TOKEN');
    sendVerificationEmail.mockResolvedValue({ data: null, error: { status: 500, message: 'Internal' } });
    const user = userEvent.setup();
    render(<VerifyEmailPage />);
    await user.type(await screen.findByLabelText('Email'), 'ada@example.test');
    await user.click(screen.getByRole('button', { name: 'Send a new link' }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith("We couldn't send the email. Try again in a minute."));
  });

  it('shows a spinner while the session is pending', () => {
    window.history.replaceState(null, '', '/verify-email');
    sessionState.value = { data: null, isPending: true };
    render(<VerifyEmailPage />);
    expect(screen.getByLabelText('Loading')).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run it and watch it fail.**

```bash
cd apps/web && bunx vitest run app/verify-email
```

Expected: `Failed to resolve import "./page"`.

- [ ] **Step 3: Implement the page.** Create `apps/web/app/verify-email/page.tsx`:

```tsx
'use client';

import * as React from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { Loader2, MailCheck } from 'lucide-react';
import { toast } from 'sonner';

import { AuthShell } from '@/components/auth/auth-shell';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { authClient } from '@/lib/auth-client';
import { captureRetryAfter, describeAuthError } from '@/lib/auth-copy';

/**
 * Landing page of the emailed verification link. Better Auth verifies the
 * token at /api/auth/verify-email, sets the session cookie
 * (autoSignInAfterVerification) and redirects here; on a bad or expired
 * token it redirects with ?error=TOKEN_EXPIRED|INVALID_TOKEN instead.
 * A native sign-up (desktop/mobile) lands here in a browser without the
 * app's session, hence the "sign in to continue" variant.
 */
export default function VerifyEmailPage() {
  const router = useRouter();
  const { data: session, isPending } = authClient.useSession();
  const [error, setError] = React.useState<string | null | undefined>(undefined);
  const [email, setEmail] = React.useState('');
  const [resent, setResent] = React.useState(false);
  const [pending, setPending] = React.useState(false);

  React.useEffect(() => {
    setError(new URLSearchParams(window.location.search).get('error'));
  }, []);

  async function resend(e: React.FormEvent) {
    e.preventDefault();
    setPending(true);
    const retry = captureRetryAfter();
    try {
      const { error: sendError } = await authClient.sendVerificationEmail({ email, callbackURL: '/verify-email' }, retry.fetchOptions);
      if (sendError) {
        toast.error(describeAuthError(sendError, { fallback: 'Could not send the email. Try again.', retryAfter: retry.value, sendsMail: true }));
        return;
      }
      setResent(true);
    } finally {
      setPending(false);
    }
  }

  if (error === undefined || isPending) {
    return (
      <AuthShell title="Verifying…">
        <Loader2 className="text-muted-foreground mx-auto size-5 animate-spin" aria-label="Loading" />
      </AuthShell>
    );
  }

  if (error) {
    return (
      <AuthShell title="This link has expired or was already used" description="Verification links work once and expire after 24 hours. Enter your email to get a new one.">
        {resent ? (
          <p className="text-muted-foreground text-center text-sm" role="status">
            If an account exists for that address, we sent a new link.
          </p>
        ) : (
          <form onSubmit={resend} className="flex flex-col gap-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="email">Email</Label>
              <Input id="email" type="email" autoComplete="email" required placeholder="you@calendium.app" value={email} onChange={(e) => setEmail(e.target.value)} disabled={pending} />
            </div>
            <Button type="submit" size="lg" className="w-full" disabled={pending}>
              {pending ? <Loader2 className="animate-spin" /> : null}
              Send a new link
            </Button>
          </form>
        )}
        <Link href="/signin" className="text-muted-foreground hover:text-foreground mt-2 text-center text-xs underline-offset-2 hover:underline">
          Back to sign in
        </Link>
      </AuthShell>
    );
  }

  return (
    <AuthShell title="Email verified" description={session ? 'Your address is confirmed and you are signed in.' : 'Your address is confirmed. Sign in to start using Calendium.'}>
      <MailCheck className="text-muted-foreground mx-auto size-8" aria-hidden />
      {session ? (
        <Button size="lg" className="w-full" onClick={() => router.replace('/mail')}>
          Continue
        </Button>
      ) : (
        <Button asChild size="lg" className="w-full">
          <Link href="/signin">Sign in to continue</Link>
        </Button>
      )}
    </AuthShell>
  );
}
```

- [ ] **Step 4: Run the test, typecheck and Biome.**

```bash
cd apps/web && bunx vitest run app/verify-email && bunx tsc --noEmit && cd ../.. && bunx biome check apps/web/app/verify-email
```

Expected: pass; clean.

- [ ] **Step 5: Commit.**

```bash
git add apps/web/app/verify-email
git commit -m "feat(web): verify-email landing page with verified/sign-in/expired states and resend" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 15: Settings → Account — change password

**Files:**
- Create: `apps/web/app/(app)/settings/settings-account.tsx`
- Modify: `apps/web/app/(app)/settings/settings-page.tsx` (`SettingsTab`, `KNOWN_TABS`, `availableTabs`, tab trigger + content, import)
- Test: `apps/web/app/(app)/settings/settings-account.test.tsx`

**Interfaces:**
- Consumes: `passwordPolicyError` (Task 6), `describeAuthError`, `captureRetryAfter` (Task 12), `authClient.listAccounts`, `authClient.changePassword`, `useSession`.
- Produces: `AccountSection()` component; Settings tab `account` (label `Account`, deep link `?tab=account`); success toast `Password updated. Other devices were signed out.`; form errors `Passwords don't match.`, `Current password is incorrect.`.

- [ ] **Step 1: Write the failing test.** Create `apps/web/app/(app)/settings/settings-account.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const listAccounts = vi.fn();
const changePassword = vi.fn();
vi.mock('@/lib/auth-client', () => ({
  authClient: {
    listAccounts: (...args: unknown[]) => listAccounts(...args),
    changePassword: (...args: unknown[]) => changePassword(...args),
  },
  useSession: () => ({ data: { user: { id: 'u1', email: 'ada@example.test' } }, isPending: false }),
}));

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({ toast: { success: (...a: unknown[]) => toastSuccess(...a), error: (...a: unknown[]) => toastError(...a) } }));

import { AccountSection } from './settings-account';

const CREDENTIAL = { id: 'acc-1', providerId: 'credential', accountId: 'u1', scopes: [], createdAt: new Date(), updatedAt: new Date() };
const GOOGLE = { id: 'acc-2', providerId: 'google', accountId: '123', scopes: [], createdAt: new Date(), updatedAt: new Date() };

function renderSection() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <AccountSection />
    </QueryClientProvider>
  );
}

async function fill(user: ReturnType<typeof userEvent.setup>, current: string, next: string, confirm = next) {
  await user.type(await screen.findByLabelText('Current password'), current);
  await user.type(screen.getByLabelText('New password'), next);
  await user.type(screen.getByLabelText('Confirm new password'), confirm);
  await user.click(screen.getByRole('button', { name: 'Update password' }));
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe('AccountSection', () => {
  it('shows the email and the change-password form for a credential account, and submits with revokeOtherSessions', async () => {
    listAccounts.mockResolvedValue({ data: [CREDENTIAL, GOOGLE], error: null });
    changePassword.mockResolvedValue({ data: { token: null, user: {} }, error: null });
    const user = userEvent.setup();
    renderSection();
    expect(await screen.findByText('ada@example.test')).toBeInTheDocument();
    await fill(user, 'old-password-1', 'correct-horse-battery');
    expect(changePassword).toHaveBeenCalledWith(
      { currentPassword: 'old-password-1', newPassword: 'correct-horse-battery', revokeOtherSessions: true },
      expect.anything()
    );
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith('Password updated. Other devices were signed out.'));
    expect(screen.getByLabelText('New password')).toHaveValue('');
  });

  it('rejects a mismatch and a policy violation client-side', async () => {
    listAccounts.mockResolvedValue({ data: [CREDENTIAL], error: null });
    const user = userEvent.setup();
    renderSection();
    await fill(user, 'old-password-1', 'correct-horse-battery', 'correct-horse-batterx');
    expect(await screen.findByText("Passwords don't match.")).toBeInTheDocument();
    for (const label of ['Current password', 'New password', 'Confirm new password']) await user.clear(screen.getByLabelText(label));
    await fill(user, 'old-password-1', 'ada@example-2026');
    expect(await screen.findByText('Password must not contain your email address.')).toBeInTheDocument();
    expect(changePassword).not.toHaveBeenCalled();
  });

  it('maps INVALID_PASSWORD to "Current password is incorrect."', async () => {
    listAccounts.mockResolvedValue({ data: [CREDENTIAL], error: null });
    changePassword.mockResolvedValue({ data: null, error: { status: 400, code: 'INVALID_PASSWORD', message: 'Invalid password' } });
    const user = userEvent.setup();
    renderSection();
    await fill(user, 'wrong-password', 'correct-horse-battery');
    expect(await screen.findByText('Current password is incorrect.')).toBeInTheDocument();
  });

  it('explains the sign-in method for a social-only account', async () => {
    listAccounts.mockResolvedValue({ data: [GOOGLE], error: null });
    renderSection();
    expect(await screen.findByText(/You sign in with Google\./)).toBeInTheDocument();
    expect(screen.queryByLabelText('Current password')).not.toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run it and watch it fail.**

```bash
cd apps/web && bunx vitest run "app/(app)/settings/settings-account.test.tsx"
```

Expected: `Failed to resolve import "./settings-account"`.

- [ ] **Step 3: Implement the section.** Create `apps/web/app/(app)/settings/settings-account.tsx`:

```tsx
'use client';

import * as React from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Loader2 } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { authClient, useSession } from '@/lib/auth-client';
import { captureRetryAfter, describeAuthError } from '@/lib/auth-copy';
import { PASSWORD_MIN_LENGTH, passwordPolicyError } from '@/lib/auth-env';

const PROVIDER_LABELS: Record<string, string> = { google: 'Google', apple: 'Apple' };

/** Better Auth's list-accounts row (the fields this section reads). */
interface LinkedAccount {
  id: string;
  providerId: string;
}

/**
 * Settings → Account (piece 2): change the password of an email/password
 * account. `listAccounts` decides: a `credential` row shows the form, a
 * social-only user is told how they sign in. The policy is checked here for
 * instant feedback and enforced again by the server hook.
 */
export function AccountSection() {
  const { data: session } = useSession();
  const email = session?.user.email ?? '';
  const accountsQuery = useQuery({
    queryKey: ['auth-accounts'],
    queryFn: async (): Promise<LinkedAccount[]> => {
      const { data, error } = await authClient.listAccounts();
      if (error) throw new Error(error.message ?? 'Could not load sign-in methods');
      return (data ?? []) as LinkedAccount[];
    },
  });
  const accounts = accountsQuery.data ?? [];
  const hasPassword = accounts.some((a) => a.providerId === 'credential');
  const socialProviders = accounts.filter((a) => a.providerId !== 'credential').map((a) => PROVIDER_LABELS[a.providerId] ?? a.providerId);

  const [current, setCurrent] = React.useState('');
  const [next, setNext] = React.useState('');
  const [confirm, setConfirm] = React.useState('');
  const [formError, setFormError] = React.useState<string | null>(null);

  const change = useMutation({
    mutationFn: async () => {
      const violation = passwordPolicyError(next, email);
      if (violation) throw new Error(violation.message);
      if (next !== confirm) throw new Error("Passwords don't match.");
      const retry = captureRetryAfter();
      const { error } = await authClient.changePassword(
        { currentPassword: current, newPassword: next, revokeOtherSessions: true },
        retry.fetchOptions
      );
      if (error) {
        if (error.code === 'INVALID_PASSWORD') throw new Error('Current password is incorrect.');
        throw new Error(describeAuthError(error, { fallback: 'Could not update your password. Try again.', retryAfter: retry.value }));
      }
    },
    onSuccess: () => {
      toast.success('Password updated. Other devices were signed out.');
      setCurrent('');
      setNext('');
      setConfirm('');
      setFormError(null);
    },
    onError: (err) => setFormError(err instanceof Error ? err.message : 'Could not update your password. Try again.'),
  });

  return (
    <Card>
      <CardHeader>
        <CardTitle>Account</CardTitle>
        <CardDescription>
          Signed in as <span className="text-foreground font-medium">{email}</span>.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {accountsQuery.isLoading ? (
          <Skeleton className="h-24 w-full" />
        ) : accountsQuery.isError ? (
          <p className="text-destructive text-sm">Could not load your sign-in methods. Reload to try again.</p>
        ) : hasPassword ? (
          <form
            className="flex max-w-sm flex-col gap-3"
            onSubmit={(e) => {
              e.preventDefault();
              setFormError(null);
              change.mutate();
            }}>
            <div className="grid gap-1.5">
              <Label htmlFor="account-current-password">Current password</Label>
              <Input id="account-current-password" type="password" autoComplete="current-password" required value={current} onChange={(e) => setCurrent(e.target.value)} disabled={change.isPending} />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="account-new-password">New password</Label>
              <Input id="account-new-password" type="password" autoComplete="new-password" required minLength={PASSWORD_MIN_LENGTH} value={next} onChange={(e) => setNext(e.target.value)} disabled={change.isPending} />
              <p className="text-muted-foreground text-xs">At least {PASSWORD_MIN_LENGTH} characters, and not your email address.</p>
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="account-confirm-password">Confirm new password</Label>
              <Input id="account-confirm-password" type="password" autoComplete="new-password" required minLength={PASSWORD_MIN_LENGTH} value={confirm} onChange={(e) => setConfirm(e.target.value)} disabled={change.isPending} />
            </div>
            {formError && (
              <p className="text-destructive text-sm" role="alert">
                {formError}
              </p>
            )}
            <div>
              <Button type="submit" disabled={change.isPending}>
                {change.isPending && <Loader2 className="animate-spin" />}
                Update password
              </Button>
            </div>
          </form>
        ) : (
          <p className="text-muted-foreground text-sm">
            You sign in with {socialProviders.length > 0 ? socialProviders.join(' and ') : 'a linked provider'}. Password sign-in isn't set up for this account.
          </p>
        )}
      </CardContent>
      {hasPassword && (
        <CardFooter className="border-t pt-6">
          <p className="text-muted-foreground text-xs">Changing your password signs out every other device.</p>
        </CardFooter>
      )}
    </Card>
  );
}
```

- [ ] **Step 4: Add the tab.** In `apps/web/app/(app)/settings/settings-page.tsx`:

(a) add `import { AccountSection } from './settings-account';` after `import { CalendarAutomationSection } from './calendar-automation';`;

(b) in `type SettingsTab =` add `| 'account'` directly after `| 'accounts'`; in `KNOWN_TABS` add `'account',` after `'accounts',`; in the `availableTabs` array literal add `'account',` after `'accounts',`;

(c) in `<TabsList>` add `<TabsTrigger value="account">Account</TabsTrigger>` directly after `<TabsTrigger value="accounts">Accounts</TabsTrigger>`;

(d) after the `<TabsContent value="accounts" className="mt-4"> … </TabsContent>` block add:

```tsx
          <TabsContent value="account" className="mt-4">
            <AccountSection />
          </TabsContent>
```

(e) in the header paragraph change `Accounts, snippets, templates,` to `Accounts, account security, snippets, templates,`.

- [ ] **Step 5: Run the settings tests, typecheck and Biome.**

```bash
cd apps/web && bunx vitest run "app/(app)/settings" && bunx tsc --noEmit && cd ../.. && bunx biome check "apps/web/app/(app)/settings"
```

Expected: `settings-account.test.tsx` and the pre-existing settings tests pass; typecheck and Biome clean.

- [ ] **Step 6: Commit.**

```bash
git add "apps/web/app/(app)/settings/settings-account.tsx" "apps/web/app/(app)/settings/settings-account.test.tsx" "apps/web/app/(app)/settings/settings-page.tsx"
git commit -m "feat(web): Settings → Account tab with change password (other sessions revoked)" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 16: Playwright — `auth-recovery.spec.ts` and `settings-account.spec.ts` (demo mode)

**Files:**
- Create: `apps/web/e2e/auth-recovery.spec.ts`, `apps/web/e2e/settings-account.spec.ts`

**Interfaces:**
- Consumes: `apps/web/e2e/fixtures.ts` unchanged (stubs `get-session` with a fake session and `/token` with 401); pages from Tasks 12–15.

- [ ] **Step 1: Write the specs.** Create `apps/web/e2e/auth-recovery.spec.ts`:

```ts
import { expect, test } from './fixtures';

/**
 * Password recovery and verification pages in demo mode. Better Auth's
 * server route is never hit: the endpoints these pages call are stubbed at
 * the network layer exactly like fixtures.ts stubs get-session and /token.
 */
test.describe('Auth recovery', () => {
  test('forgot password shows the generic confirmation', async ({ page }) => {
    await page.route('**/api/auth/request-password-reset*', (route) => route.fulfill({ json: { status: true } }));
    await page.goto('/forgot-password');
    await page.getByLabel('Email').fill('someone@example.com');
    await page.getByRole('button', { name: 'Send reset link' }).click();
    await expect(page.getByText('If an account exists for that address, we sent a link.')).toBeVisible();
  });

  test('reset password with a token updates and returns to sign-in', async ({ page }) => {
    await page.route('**/api/auth/reset-password', (route) => route.fulfill({ json: { status: true } }));
    await page.goto('/reset-password?token=e2e-token');
    await page.getByLabel('New password').fill('correct-horse-battery');
    await page.getByLabel('Confirm new password').fill('correct-horse-battery');
    await page.getByRole('button', { name: 'Update password' }).click();
    await expect(page.getByText('Password updated. Sign in with your new password.')).toBeVisible();
    // The stubbed session makes /signin bounce straight to the inbox.
    await expect(page).toHaveURL(/\/(signin|mail)/);
  });

  test('reset password with an error shows the recovery path', async ({ page }) => {
    await page.goto('/reset-password?error=INVALID_TOKEN');
    await expect(page.getByText('This link is invalid or has expired')).toBeVisible();
    await page.getByRole('link', { name: 'Request a new link' }).click();
    await expect(page).toHaveURL(/\/forgot-password$/);
  });

  test('verify-email with the stubbed session shows Verified and continues to the inbox', async ({ page }) => {
    await page.goto('/verify-email');
    await expect(page.getByText('Email verified')).toBeVisible();
    await page.getByRole('button', { name: 'Continue' }).click();
    await expect(page).toHaveURL(/\/mail/);
  });

  test('verify-email with an expired link offers a resend', async ({ page }) => {
    await page.route('**/api/auth/send-verification-email*', (route) => route.fulfill({ json: { status: true } }));
    await page.goto('/verify-email?error=TOKEN_EXPIRED');
    await expect(page.getByText('This link has expired or was already used')).toBeVisible();
    await page.getByLabel('Email').fill('someone@example.com');
    await page.getByRole('button', { name: 'Send a new link' }).click();
    await expect(page.getByText('If an account exists for that address, we sent a new link.')).toBeVisible();
  });

  test('sign-in page links to forgot password', async ({ page }) => {
    // The stubbed session redirects /signin; drop it for this one test.
    await page.route('**/api/auth/get-session*', (route) => route.fulfill({ json: null }));
    await page.goto('/signin');
    await expect(page.getByRole('link', { name: 'Forgot password?' })).toHaveAttribute('href', '/forgot-password');
  });
});
```

Create `apps/web/e2e/settings-account.spec.ts`:

```ts
import { expect, test } from './fixtures';

const NOW = new Date().toISOString();

test.describe('Settings → Account', () => {
  test('changes the password through the stubbed Better Auth endpoints', async ({ page }) => {
    await page.route('**/api/auth/list-accounts*', (route) =>
      route.fulfill({ json: [{ id: 'acc-1', providerId: 'credential', accountId: 'e2e-user-1', scopes: [], createdAt: NOW, updatedAt: NOW }] })
    );
    await page.route('**/api/auth/change-password', (route) => route.fulfill({ json: { token: null, user: { id: 'e2e-user-1' } } }));

    await page.goto('/settings?tab=account');
    // exact: 'Account' is also a substring of the 'Accounts' tab.
    await expect(page.getByRole('tab', { name: 'Account', exact: true })).toHaveAttribute('data-state', 'active');
    await expect(page.getByText('e2e@calendium.app')).toBeVisible();

    await page.getByLabel('Current password').fill('old-password-1');
    await page.getByLabel('New password').fill('correct-horse-battery');
    await page.getByLabel('Confirm new password').fill('correct-horse-battery');
    await page.getByRole('button', { name: 'Update password' }).click();

    await expect(page.getByText('Password updated. Other devices were signed out.')).toBeVisible();
  });

  test('a social-only account sees its sign-in method instead of the form', async ({ page }) => {
    await page.route('**/api/auth/list-accounts*', (route) =>
      route.fulfill({ json: [{ id: 'acc-2', providerId: 'google', accountId: '123', scopes: [], createdAt: NOW, updatedAt: NOW }] })
    );
    await page.goto('/settings?tab=account');
    await expect(page.getByText(/You sign in with Google\./)).toBeVisible();
    await expect(page.getByLabel('Current password')).toHaveCount(0);
  });
});
```

- [ ] **Step 2: Run the two specs (the webServer boots in demo mode with `SELF_HOSTED=true`, Task 7).**

```bash
cd apps/web && bunx playwright test e2e/auth-recovery.spec.ts e2e/settings-account.spec.ts --project=chromium
```

Expected: 8 passed. (If `settings-account` fails only on the tab assertion, the `Account` tab is missing from `KNOWN_TABS` in Task 15 — fix there, not here.)

- [ ] **Step 3: Run the whole e2e suite to prove the existing specs are untouched by the auth changes.**

```bash
bun run test:e2e
```

Expected: all specs pass.

- [ ] **Step 4: Commit.**

```bash
git add apps/web/e2e/auth-recovery.spec.ts apps/web/e2e/settings-account.spec.ts
git commit -m "test(e2e): auth recovery pages and Settings → Account in demo mode" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 17: Desktop — verification-required sign-up, cached JWT, "Forgot password?" link

**Files:**
- Modify: `apps/desktop/frontend/src/lib/server-config.ts` (`DEFAULT_FEATURES`, `DEMO_CONFIG`, new `forgotPasswordUrl`), `apps/desktop/frontend/src/lib/auth.ts` (`AuthResult`, `signInEmail`, `signUpEmail`, `signOut`, access-token cache), `apps/desktop/frontend/src/lib/api.ts` (pass `accessTokens`), `apps/desktop/frontend/src/views/SignInView.tsx` (notice + link)
- Test: `apps/desktop/frontend/src/lib/server-config.test.ts` (append), `apps/desktop/frontend/src/lib/auth.test.ts` (append), `apps/desktop/frontend/src/views/SignInView.test.tsx` (new)

**Interfaces:**
- Consumes: `createAccessTokenCache`, `InstanceFeatures.email` (Task 11); `ServerConfig.webUrl` (piece 1 Task 20 — verified in Step 1).
- Produces: `forgotPasswordUrl(config: ServerConfig | null): string | null`; `AuthResult.verificationRequired?: boolean`; `accessTokens: AccessTokenCache` exported from `lib/auth.ts`; `getAccessToken()` now cached; desktop copy `Check your inbox — we sent a verification link to <email>.`, `Verify your email first — we sent a new link.`, `Too many attempts, try again in N s`.

- [ ] **Step 1: Confirm the piece 1 dependency.**

```bash
grep -n "webUrl" apps/desktop/frontend/src/lib/server-config.ts packages/shared/src/types.ts
```

Expected: at least one match in each file (`webUrl: string` on `InstanceInfo`, and `webUrl` on the desktop `ServerConfig` + `discoverServer`). If `server-config.ts` has NO match, piece 1 has not landed: STOP this task and merge piece 1 first (this plan consumes `webUrl`, it never defines it).

- [ ] **Step 2: Write the failing tests.** Append to `apps/desktop/frontend/src/lib/server-config.test.ts`:

```ts
describe('forgotPasswordUrl', () => {
  it('prefers the advertised webUrl, falls back to the Better Auth origin, and is null without either', async () => {
    const { forgotPasswordUrl, DEMO_CONFIG } = await import('./server-config');
    const base = { ...DEMO_CONFIG, authBaseUrl: 'https://mail.example.com/api/auth' };
    expect(forgotPasswordUrl({ ...base, webUrl: 'https://app.example.com/' })).toBe('https://app.example.com/forgot-password');
    expect(forgotPasswordUrl({ ...base, webUrl: '' })).toBe('https://mail.example.com/forgot-password');
    expect(forgotPasswordUrl({ ...base, webUrl: '', authBaseUrl: '' })).toBeNull();
    expect(forgotPasswordUrl(null)).toBeNull();
  });

  it('DEFAULT_FEATURES and DEMO_CONFIG carry features.email=false', async () => {
    const { DEMO_CONFIG } = await import('./server-config');
    expect(DEMO_CONFIG.features.email).toBe(false);
  });
});
```

Append to `apps/desktop/frontend/src/lib/auth.test.ts` (reuses `configureServer`, `installFetch` and the per-test `vi.resetModules()` from that file):

```ts
function fakeJwt(expSeconds: number): string {
  const payload = btoa(JSON.stringify({ sub: 'u1', exp: expSeconds })).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  return `eyJhbGciOiJFZERTQSJ9.${payload}.sig`;
}

describe('signUpEmail — verification required (piece 2)', () => {
  it('reports verificationRequired when the server returns token === null and does not fetch a session', async () => {
    configureServer();
    const { calls } = installFetch({
      '/sign-up/email': { status: 200, body: { token: null, user: { id: 'u2', email: 'ada@test.dev' } } },
    });
    const { signUpEmail, getStoredToken } = await import('./auth');
    const result = await signUpEmail('Ada Lovelace', 'ada@test.dev', 'correct-horse-battery');
    expect(result).toEqual({ ok: true, verificationRequired: true });
    expect(getStoredToken()).toBeNull();
    expect(calls.some((c) => c.url.includes('/get-session'))).toBe(false);
    const signUpCall = calls.find((c) => c.url.includes('/sign-up/email'));
    expect(signUpCall?.body).toMatchObject({ callbackURL: '/verify-email' });
  });
});

describe('signInEmail — verification and rate-limit copy (piece 2)', () => {
  it('maps 403 EMAIL_NOT_VERIFIED to the verify-first message', async () => {
    configureServer();
    installFetch({ '/sign-in/email': { status: 403, body: { code: 'EMAIL_NOT_VERIFIED', message: 'Email not verified' } } });
    const { signInEmail } = await import('./auth');
    expect(await signInEmail('ada@test.dev', 'correct-horse-battery')).toEqual({
      ok: false,
      error: 'Verify your email first — we sent a new link.',
    });
  });

  it('maps 429 to the retry-after copy', async () => {
    configureServer();
    installFetch({ '/sign-in/email': { status: 429, body: { message: 'Too many requests. Please try again later.' }, headers: { 'X-Retry-After': '42' } } });
    const { signInEmail } = await import('./auth');
    expect(await signInEmail('ada@test.dev', 'correct-horse-battery')).toEqual({ ok: false, error: 'Too many attempts, try again in 42 s' });
  });
});

describe('getAccessToken cache (piece 2)', () => {
  it('reuses a fresh JWT across calls and re-mints after signOut', async () => {
    const config = configureServer();
    localStorage.setItem('calendium.bearerToken', 'stored-token');
    const token = fakeJwt(Math.floor(Date.now() / 1000) + 900);
    const { calls } = installFetch({
      '/token': { status: 200, body: { token } },
      '/sign-out': { status: 200, body: {} },
    });
    const { getAccessToken, signOut } = await import('./auth');
    expect(await getAccessToken()).toBe(token);
    expect(await getAccessToken()).toBe(token);
    expect(calls.filter((c) => c.url === `${config.authBaseUrl}/token`)).toHaveLength(1);

    await signOut();
    // Signed out: no stored token, so the cache is empty AND minting short-circuits.
    expect(await getAccessToken()).toBeNull();
    expect(calls.filter((c) => c.url === `${config.authBaseUrl}/token`)).toHaveLength(1);
  });
});
```

Create `apps/desktop/frontend/src/views/SignInView.test.tsx`:

```tsx
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const signInEmail = vi.fn();
const signUpEmail = vi.fn();
vi.mock('@/lib/auth', () => ({
  signInEmail: (...a: unknown[]) => signInEmail(...a),
  signUpEmail: (...a: unknown[]) => signUpEmail(...a),
  verifyOtt: vi.fn(),
}));

const openExternal = vi.fn();
vi.mock('@/lib/wails', () => ({
  desktop: { OpenExternal: (...a: unknown[]) => openExternal(...a) },
  onDeepLink: () => () => {},
}));

vi.mock('@/lib/server-config', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/server-config')>();
  return {
    ...actual,
    useServerConfig: () => ({
      config: {
        serverUrl: 'https://api.test',
        authBaseUrl: 'https://mail.example.com/api/auth',
        authProviders: ['email'],
        mode: 'self_host',
        name: 'Test',
        features: { billing: false, google: false, microsoft: false, ai: false, push: false, email: true },
        undoSendSeconds: 15,
        webUrl: 'https://app.example.com',
      },
    }),
  };
});

import { SignInView } from './SignInView';

beforeEach(() => {
  vi.clearAllMocks();
});

describe('SignInView (piece 2)', () => {
  it('"Forgot password?" opens <webUrl>/forgot-password in the system browser', async () => {
    const user = userEvent.setup();
    render(<SignInView />);
    await user.click(screen.getByRole('button', { name: 'Forgot password?' }));
    expect(openExternal).toHaveBeenCalledWith('https://app.example.com/forgot-password');
  });

  it('a verification-required sign-up switches to sign-in mode with the check-inbox notice', async () => {
    signUpEmail.mockResolvedValue({ ok: true, verificationRequired: true });
    const user = userEvent.setup();
    render(<SignInView />);
    await user.click(screen.getByRole('button', { name: 'Create one' }));
    await user.type(screen.getByLabelText('Name'), 'Ada');
    await user.type(screen.getByLabelText('Email'), 'ada@example.test');
    await user.type(screen.getByLabelText('Password'), 'correct-horse-battery');
    await user.click(screen.getByRole('button', { name: 'Create account' }));
    await waitFor(() => expect(screen.getByText('Check your inbox — we sent a verification link to ada@example.test.')).toBeTruthy());
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeTruthy();
  });

  it('surfaces the verify-first error from sign-in', async () => {
    signInEmail.mockResolvedValue({ ok: false, error: 'Verify your email first — we sent a new link.' });
    const user = userEvent.setup();
    render(<SignInView />);
    await user.type(screen.getByLabelText('Email'), 'ada@example.test');
    await user.type(screen.getByLabelText('Password'), 'correct-horse-battery');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));
    await waitFor(() => expect(screen.getByText('Verify your email first — we sent a new link.')).toBeTruthy());
  });
});
```

- [ ] **Step 3: Run them and watch them fail.**

```bash
bun run test:desktop
```

Expected: `forgotPasswordUrl` is not exported; `verificationRequired` is undefined (sign-up refreshes the session and fails on the missing `/get-session` route); the 403/429 copy mismatches; the cache test records two `/token` calls; `SignInView.test.tsx` finds no `Forgot password?` button.

- [ ] **Step 4: Implement `server-config.ts`.** Add `email: false,` to `DEFAULT_FEATURES` (after `push: false,`) and to `DEMO_CONFIG.features` (`features: { billing: true, google: true, microsoft: true, ai: true, push: false, email: false }`), and after `webOrigin` add:

```ts
/**
 * Where "Forgot password?" sends the user: the web app's forgot-password page
 * on the server's advertised web URL (InstanceInfo.webUrl), falling back to
 * the Better Auth origin. Opened in the system browser; the desktop never
 * handles reset itself.
 */
export function forgotPasswordUrl(config: ServerConfig | null): string | null {
  const base = config?.webUrl?.replace(/\/+$/, '') || webOrigin(config);
  return base ? `${base}/forgot-password` : null;
}
```

- [ ] **Step 5: Implement `auth.ts`.** Add `import { createAccessTokenCache } from '@calendium/shared';` after the `better-auth/react` import; change `AuthResult` to:

```ts
export interface AuthResult {
  ok: boolean;
  error?: string;
  /** Sign-up succeeded but the server requires email verification first (no session yet). */
  verificationRequired?: boolean;
}
```

add after `AuthResult`:

```ts
/** Copy shared with the web app for Better Auth client errors. */
function describeAuthError(error: { status?: number; code?: string; message?: string }, fallback: string, retryAfter: string | null): string {
  if (error.status === 429) {
    const n = Number(retryAfter);
    return `Too many attempts, try again in ${Number.isFinite(n) && n > 0 ? Math.ceil(n) : 60} s`;
  }
  if (error.code === 'EMAIL_NOT_VERIFIED') return 'Verify your email first — we sent a new link.';
  return error.message ?? fallback;
}

/** Captures X-Retry-After from a Better Auth client call's onError hook. */
function retryAfterCapture() {
  let value: string | null = null;
  return {
    fetchOptions: { onError: (ctx: { response: Response }) => { value = ctx.response.headers.get('x-retry-after'); } },
    get value() { return value; },
  };
}
```

replace `signInEmail` and `signUpEmail` with:

```ts
export async function signInEmail(email: string, password: string): Promise<AuthResult> {
  const c = getAuthClient();
  if (!c) return { ok: false, error: 'Connect to a server first.' };
  const retry = retryAfterCapture();
  const { error } = await c.signIn.email({ email, password }, retry.fetchOptions);
  if (error) return { ok: false, error: describeAuthError(error, 'Could not sign in.', retry.value) };
  await refreshSession();
  return { ok: true };
}

export async function signUpEmail(name: string, email: string, password: string): Promise<AuthResult> {
  const c = getAuthClient();
  if (!c) return { ok: false, error: 'Connect to a server first.' };
  const retry = retryAfterCapture();
  // callbackURL is relative: Better Auth resolves it against BETTER_AUTH_URL,
  // so the emailed link lands on the web /verify-email page.
  const { data, error } = await c.signUp.email({ name, email, password, callbackURL: '/verify-email' }, retry.fetchOptions);
  if (error) return { ok: false, error: describeAuthError(error, 'Could not create your account.', retry.value) };
  // With SMTP configured the server never signs a new account in (and answers
  // the same for an existing address): token === null means "check your inbox".
  if (data && data.token === null) return { ok: true, verificationRequired: true };
  await refreshSession();
  return { ok: true };
}
```

in `signOut`, add `accessTokens.invalidate();` directly after `clearStoredToken();`; and replace the `getAccessToken` doc comment + function with:

```ts
/**
 * Mints a short-lived (default 15m) EdDSA JWT via `${authBaseUrl}/token` with
 * the stored session token; null when signed out, unconfigured, rate limited
 * or unreachable (null is never cached).
 */
async function mintAccessToken(): Promise<string | null> {
  const cfg = getActiveServerConfig();
  const token = getStoredToken();
  if (!cfg?.authBaseUrl || !token) return null;
  try {
    const res = await fetch(`${cfg.authBaseUrl}/token`, {
      method: 'GET',
      headers: { accept: 'application/json', authorization: `Bearer ${token}` },
    });
    if (!res.ok) return null;
    const data = (await res.json()) as { token?: string };
    return data.token ?? null;
  } catch {
    return null;
  }
}

/**
 * JWT cache shared with lib/api.ts (piece 2): reused until 60 s before `exp`,
 * invalidated on sign-out so a second account on this machine can never ride
 * the first one's token.
 */
export const accessTokens = createAccessTokenCache(mintAccessToken);

/** The Bearer credential for the Go API (cached JWT, minted on demand). */
export function getAccessToken(): Promise<string | null> {
  return accessTokens.get();
}
```

In `apps/desktop/frontend/src/lib/api.ts` change the import to `import { accessTokens, getAccessToken } from './auth';` and add `accessTokens,` after `getAccessToken,` in the `new ApiClient({ … })` literal (keep the `baseUrl` getter untouched).

- [ ] **Step 6: Implement the view.** In `apps/desktop/frontend/src/views/SignInView.tsx`:

(a) change the server-config import to `import { forgotPasswordUrl, useServerConfig, webOrigin } from '@/lib/server-config';`;

(b) add `const [notice, setNotice] = useState<string | null>(null);` after the `error` state;

(c) replace `submitEmail` with:

```tsx
  async function submitEmail(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setNotice(null);
    setBusy('email');
    const res =
      mode === 'signin'
        ? await signInEmail(email, password)
        : await signUpEmail(name, email, password);
    if (!res.ok) {
      setError(res.error ?? 'Something went wrong.');
      setBusy(null);
      return;
    }
    if (res.verificationRequired) {
      // No session yet: the server emailed a verification link. Flip to
      // sign-in so the user can continue once the link is clicked.
      setNotice(`Check your inbox — we sent a verification link to ${email}.`);
      setMode('signin');
      setPassword('');
      setBusy(null);
    }
  }

  function openForgotPassword() {
    const url = forgotPasswordUrl(config);
    if (!url) {
      setError('Connect to a server first.');
      return;
    }
    desktop.OpenExternal(url);
  }
```

(d) in the form, directly after the `{error && <p className="text-sm text-destructive">{error}</p>}` line add:

```tsx
              {notice && (
                <p className="text-sm text-muted-foreground" role="status">
                  {notice}
                </p>
              )}
              {!isSignup && (
                <button
                  type="button"
                  className="self-end text-xs text-muted-foreground underline-offset-4 hover:underline"
                  onClick={openForgotPassword}
                  disabled={busy !== null}
                >
                  Forgot password?
                </button>
              )}
```

- [ ] **Step 7: Run the desktop suite, typecheck and Biome.**

```bash
bun run test:desktop && cd apps/desktop/frontend && bunx tsc --noEmit && cd ../../.. && bunx biome check apps/desktop/frontend/src
```

Expected: all desktop tests pass (the pre-existing `getAccessToken` cases still pass: `jwt-xyz` is opaque and never cached, and each test resets modules); typecheck and Biome clean.

- [ ] **Step 8: Commit.**

```bash
git add apps/desktop/frontend/src/lib/server-config.ts apps/desktop/frontend/src/lib/server-config.test.ts apps/desktop/frontend/src/lib/auth.ts apps/desktop/frontend/src/lib/auth.test.ts apps/desktop/frontend/src/lib/api.ts apps/desktop/frontend/src/views/SignInView.tsx apps/desktop/frontend/src/views/SignInView.test.tsx
git commit -m "feat(desktop): verification-required sign-up, cached API JWT invalidated on sign-out, forgot-password link to the web app" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 18: Mobile — verification-required sign-up, JWT invalidation, "Forgot password?" link

**Files:**
- Modify: `apps/mobile/lib/server-config.ts` (`DEMO_CONFIG.features.email`, new `forgotPasswordUrl`, new `verifyEmailCallbackUrl`), `apps/mobile/lib/api.ts` (`configureApi` invalidates; doc comment), `apps/mobile/lib/auth-client.ts` (`getBetterAuthToken` doc comment only), `apps/mobile/context/auth.tsx` (`signUpWithEmail` result, error copy, sign-out invalidation), `apps/mobile/app/index.tsx` (notice + link)
- Test: `apps/mobile/lib/server-config.test.ts` (append — the file already exists), `apps/mobile/lib/api.test.ts` (append), `apps/mobile/context/auth.test.tsx` (new), `apps/mobile/app/index.test.tsx` (new)

**Interfaces:**
- Consumes: `ApiClient.invalidateAccessToken`, `InstanceFeatures.email` (Task 11 — `ApiClient` builds a private `createAccessTokenCache` from `getAccessToken` when no `accessTokens` option is passed, so mobile gets caching without new options); `ServerConfig.webUrl` (piece 1 Task 19 — verified in Step 1); `expo-linking` (already a dependency).
- Produces: `forgotPasswordUrl(config: ServerConfig | null): string | null`; `verifyEmailCallbackUrl(config: ServerConfig): string` (absolute `<webUrl>/verify-email` — `@better-auth/expo`'s client rewrites any RELATIVE `callbackURL` into a `calendium://` deep link via `Linking.createURL`, and the app has no verify-email screen); `AuthContextType.signUpWithEmail: (name, email, password) => Promise<{ verificationRequired: boolean }>`; mobile copy `Check your inbox — we sent a verification link to <email>.`, `Verify your email first — we sent a new link.` (alert title `Verify your email`), `Too many attempts, try again in N s`.

- [ ] **Step 1: Confirm the piece 1 dependency.**

```bash
grep -n "webUrl" apps/mobile/lib/server-config.ts packages/shared/src/types.ts
```

Expected: at least one match in each file (`webUrl: string` on `ServerConfig` and in `discoverServer`; `webUrl` on `InstanceInfo`). If `apps/mobile/lib/server-config.ts` has NO match, piece 1 has not landed: STOP this task and merge piece 1 first.

- [ ] **Step 2: Write the failing tests.**

(a) `apps/mobile/lib/server-config.test.ts` already exists (it mocks `@/lib/api`, `@/lib/auth-client`, `@/lib/mock`, `@calendium/shared` and AsyncStorage). Add `DEMO_CONFIG,`, `forgotPasswordUrl,` and `verifyEmailCallbackUrl,` to its existing `import { … } from './server-config';` list, and append:

```ts
describe('forgotPasswordUrl (piece 2)', () => {
  const base: ServerConfig = { ...DEMO_CONFIG, authBaseUrl: 'https://mail.example.com/api/auth', demoMode: false };

  it('prefers webUrl, falls back to the Better Auth origin, and is null without either', () => {
    expect(forgotPasswordUrl({ ...base, webUrl: 'https://app.example.com/' })).toBe('https://app.example.com/forgot-password');
    expect(forgotPasswordUrl({ ...base, webUrl: '' })).toBe('https://mail.example.com/forgot-password');
    expect(forgotPasswordUrl({ ...base, webUrl: '', authBaseUrl: '' })).toBeNull();
    expect(forgotPasswordUrl(null)).toBeNull();
  });

  it('verifyEmailCallbackUrl is absolute so the Expo client does not rewrite it into a deep link', () => {
    expect(verifyEmailCallbackUrl({ ...base, webUrl: 'https://app.example.com' })).toBe('https://app.example.com/verify-email');
    expect(verifyEmailCallbackUrl({ ...base, webUrl: '' })).toBe('https://mail.example.com/verify-email');
  });

  it('DEMO_CONFIG advertises features.email=false', () => {
    expect(DEMO_CONFIG.features.email).toBe(false);
  });
});
```

(b) Append to `apps/mobile/lib/api.test.ts` (reuses its `mockGetBetterAuthToken`, `mockFetch` and the `afterEach(() => configureApi(''))`):

```ts
describe('access-token cache (piece 2)', () => {
  function fakeJwt(expSeconds: number): string {
    const payload = btoa(JSON.stringify({ sub: 'u1', exp: expSeconds })).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
    return `eyJhbGciOiJFZERTQSJ9.${payload}.sig`;
  }

  it('reuses a fresh JWT across requests and re-mints after configureApi switches servers', async () => {
    mockGetBetterAuthToken.mockResolvedValue(fakeJwt(Math.floor(Date.now() / 1000) + 900));
    configureApi('https://one.example.com');
    await api.getMe();
    await api.getMe();
    expect(mockGetBetterAuthToken).toHaveBeenCalledTimes(1);

    // A JWT minted for one server must never be sent to the next one.
    configureApi('https://two.example.com');
    await api.getMe();
    expect(mockGetBetterAuthToken).toHaveBeenCalledTimes(2);
  });
});
```

(c) Create `apps/mobile/context/auth.test.tsx`:

```tsx
import type { ReactNode } from 'react';

const mockUseServerConfig = jest.fn();
jest.mock('@/lib/server-config', () => ({
  useServerConfig: (...args: unknown[]) => mockUseServerConfig(...args),
  verifyEmailCallbackUrl: () => 'https://app.example.com/verify-email',
}));

const mockInvalidate = jest.fn();
jest.mock('@/lib/api', () => ({ api: { invalidateAccessToken: () => mockInvalidate() } }));
jest.mock('@/hooks/use-push-registration', () => ({ unregisterPushDevice: jest.fn(async () => {}) }));
jest.mock('@/lib/offline', () => ({ clearOfflineState: jest.fn(async () => {}) }));
jest.mock('@/lib/query-client', () => ({ queryClient: { clear: jest.fn() } }));

import { act, renderHook } from '@testing-library/react-native';
import { Alert } from 'react-native';
import useAuth, { AuthProvider } from './auth';

const signUp = { email: jest.fn() };
const signIn = { email: jest.fn(), social: jest.fn() };
const getSession = jest.fn();
const signOutMock = jest.fn();
const authClient = { signUp, signIn, getSession, signOut: signOutMock };

function wrapper({ children }: { children: ReactNode }) {
  return <AuthProvider>{children}</AuthProvider>;
}

/**
 * Mounts the provider and lets its initial getSession() settle. RNTL 14's
 * renderHook is async, and `waitFor` is unreliable in this jest-expo +
 * React 19 setup (see hooks/use-push-registration.test.ts), so a real tick
 * inside act() flushes the effect instead.
 */
async function mountAuth() {
  const hook = await renderHook(() => useAuth(), { wrapper });
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  return hook;
}

/** The slice of a Better Auth onError context the retry-after capture reads. */
function retryAfterContext(seconds: string) {
  return {
    response: { headers: { get: (name: string) => (name.toLowerCase() === 'x-retry-after' ? seconds : null) } },
  };
}

beforeEach(() => {
  jest.clearAllMocks();
  jest.spyOn(Alert, 'alert').mockImplementation(() => {});
  getSession.mockResolvedValue({ data: null });
  mockUseServerConfig.mockReturnValue({
    authClient,
    config: { authBaseUrl: 'https://mail.example.com/api/auth', webUrl: 'https://app.example.com', demoMode: false },
    clear: jest.fn(),
  });
});

afterEach(() => {
  jest.restoreAllMocks();
});

describe('AuthProvider (piece 2)', () => {
  it('signUpWithEmail reports verificationRequired for token === null, sends the absolute callbackURL, and does not refresh', async () => {
    signUp.email.mockResolvedValue({ data: { token: null, user: { id: 'u1' } }, error: null });
    const { result } = await mountAuth();
    getSession.mockClear();
    let outcome: { verificationRequired: boolean } | undefined;
    await act(async () => {
      outcome = await result.current.signUpWithEmail('Ada', 'ada@example.test', 'correct-horse-battery');
    });
    expect(outcome).toEqual({ verificationRequired: true });
    expect(signUp.email).toHaveBeenCalledWith(
      {
        name: 'Ada',
        email: 'ada@example.test',
        password: 'correct-horse-battery',
        callbackURL: 'https://app.example.com/verify-email',
      },
      expect.objectContaining({ onError: expect.any(Function) })
    );
    expect(getSession).not.toHaveBeenCalled();
    expect(result.current.user).toBeNull();
  });

  it('signUpWithEmail with a session refreshes and reports verificationRequired=false', async () => {
    signUp.email.mockResolvedValue({ data: { token: 'sess', user: { id: 'u1' } }, error: null });
    const { result } = await mountAuth();
    getSession.mockResolvedValue({ data: { user: { id: 'u1', email: 'ada@example.test', name: 'Ada', image: null } } });
    let outcome: { verificationRequired: boolean } | undefined;
    await act(async () => {
      outcome = await result.current.signUpWithEmail('Ada', 'ada@example.test', 'correct-horse-battery');
    });
    expect(outcome).toEqual({ verificationRequired: false });
    expect(result.current.user?.email).toBe('ada@example.test');
  });

  it('signInWithEmail maps EMAIL_NOT_VERIFIED to the verify-first alert', async () => {
    signIn.email.mockResolvedValue({
      data: null,
      error: { status: 403, code: 'EMAIL_NOT_VERIFIED', message: 'Email not verified' },
    });
    const { result } = await mountAuth();
    await expect(result.current.signInWithEmail('ada@example.test', 'correct-horse-battery')).rejects.toThrow(
      'Verify your email first — we sent a new link.'
    );
    expect(Alert.alert).toHaveBeenCalledWith('Verify your email', 'Verify your email first — we sent a new link.');
  });

  it('signInWithEmail maps 429 to the X-Retry-After copy', async () => {
    signIn.email.mockImplementation(async (_body: unknown, fetchOptions: { onError: (ctx: unknown) => void }) => {
      fetchOptions.onError(retryAfterContext('42'));
      return { data: null, error: { status: 429, message: 'Too many requests. Please try again later.' } };
    });
    const { result } = await mountAuth();
    await expect(result.current.signInWithEmail('ada@example.test', 'correct-horse-battery')).rejects.toThrow(
      'Too many attempts, try again in 42 s'
    );
    expect(Alert.alert).toHaveBeenCalledWith('Error', 'Too many attempts, try again in 42 s');
  });

  it('signOut invalidates the cached API JWT', async () => {
    signOutMock.mockResolvedValue({});
    const { result } = await mountAuth();
    await act(async () => {
      await result.current.signOut();
    });
    expect(mockInvalidate).toHaveBeenCalledTimes(1);
  });
});
```

(d) Create `apps/mobile/app/index.test.tsx`:

```tsx
jest.mock('@/assets/icons/apple.svg', () => ({ __esModule: true, default: () => null }));
jest.mock('@/assets/icons/google.svg', () => ({ __esModule: true, default: () => null }));

const mockUseAuth = jest.fn();
jest.mock('@/context/auth', () => ({
  __esModule: true,
  default: (...args: unknown[]) => mockUseAuth(...args),
}));

const mockUseServerConfig = jest.fn();
jest.mock('@/lib/server-config', () => ({
  useServerConfig: (...args: unknown[]) => mockUseServerConfig(...args),
  forgotPasswordUrl: (config: { webUrl?: string } | null) => (config?.webUrl ? `${config.webUrl}/forgot-password` : null),
}));

const mockOpenURL = jest.fn();
jest.mock('expo-linking', () => ({ openURL: (...args: unknown[]) => mockOpenURL(...args) }));

jest.mock('expo-router', () => ({ Redirect: () => null, Stack: { Screen: () => null } }));

import { act, fireEvent, render, screen } from '@testing-library/react-native';
import SignInScreen from './index';

// Same tick-in-act() flush as compose.test.tsx / settings.test.tsx.
async function flush() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 50));
  });
}

function authValue(overrides: Record<string, unknown> = {}) {
  return {
    user: null,
    loading: false,
    signInWithOAuth: jest.fn(),
    signInWithEmail: jest.fn(async () => {}),
    signUpWithEmail: jest.fn(async () => ({ verificationRequired: false })),
    ...overrides,
  };
}

beforeEach(() => {
  jest.clearAllMocks();
  mockOpenURL.mockResolvedValue(true);
  mockUseServerConfig.mockReturnValue({
    isConfigured: true,
    isLoading: false,
    config: { authProviders: ['email'], webUrl: 'https://app.example.com' },
  });
  mockUseAuth.mockReturnValue(authValue());
});

describe('SignInScreen (piece 2)', () => {
  it('"Forgot password?" opens <webUrl>/forgot-password in the system browser', async () => {
    await render(<SignInScreen />);
    await fireEvent.press(screen.getByText('Forgot password?'));
    expect(mockOpenURL).toHaveBeenCalledWith('https://app.example.com/forgot-password');
  });

  it('a verification-required sign-up flips to sign-in mode with the check-inbox notice', async () => {
    const signUpWithEmail = jest.fn(async () => ({ verificationRequired: true }));
    mockUseAuth.mockReturnValue(authValue({ signUpWithEmail }));
    await render(<SignInScreen />);
    await fireEvent.press(screen.getByText("Don't have an account? Create one"));
    expect(screen.queryByText('Forgot password?')).toBeNull();
    await fireEvent.changeText(screen.getByPlaceholderText('Name'), 'Ada');
    await fireEvent.changeText(screen.getByPlaceholderText('Email'), 'ada@example.test');
    await fireEvent.changeText(screen.getByPlaceholderText('Password'), 'correct-horse-battery');
    await fireEvent.press(screen.getByText('Create account'));
    await flush();
    expect(signUpWithEmail).toHaveBeenCalledWith('Ada', 'ada@example.test', 'correct-horse-battery');
    expect(screen.getByText('Check your inbox — we sent a verification link to ada@example.test.')).toBeTruthy();
    expect(screen.getByText('Sign in')).toBeTruthy();
    expect(screen.getByText('Forgot password?')).toBeTruthy();
  });
});
```

- [ ] **Step 3: Run them and watch them fail.**

```bash
cd apps/mobile && bunx jest lib/server-config.test.ts lib/api.test.ts context/auth.test.tsx app/index.test.tsx
```

Expected: `forgotPasswordUrl is not a function` / `verifyEmailCallbackUrl is not a function` and `DEMO_CONFIG.features.email` is `undefined`; the api cache test sees 1 mint call after the server switch (expected 2); `signUpWithEmail` resolves `undefined` and `signUp.email` was called without `callbackURL`; no `Verify your email` alert and the 429 test gets `Too many requests. Please try again later.`; `mockInvalidate` was never called; `index.test.tsx` finds no `Forgot password?` text.

- [ ] **Step 4: Implement `server-config.ts`, `api.ts` and the `auth-client.ts` comment.** In `apps/mobile/lib/server-config.ts` change `DEMO_CONFIG.features` to `{ billing: true, google: true, microsoft: true, ai: true, push: false, email: false }` and add after `normalizeServerUrl`:

```ts
/** The server's web origin: the advertised webUrl, else authBaseUrl without /api/auth. */
function webBase(config: ServerConfig | null): string | null {
  const fromWebUrl = config?.webUrl?.replace(/\/+$/, '');
  if (fromWebUrl) return fromWebUrl;
  const fromAuth = config?.authBaseUrl?.replace(/\/+$/, '').replace(/\/api\/auth$/, '');
  return fromAuth || null;
}

/** "Forgot password?" opens the web app's reset page in the system browser; null until a server is configured. */
export function forgotPasswordUrl(config: ServerConfig | null): string | null {
  const base = webBase(config);
  return base ? `${base}/forgot-password` : null;
}

/**
 * Absolute callbackURL for email sign-up. The @better-auth/expo client
 * rewrites any RELATIVE callbackURL into a calendium:// deep link, which the
 * app has no verify-email screen for; an absolute web URL lands the emailed
 * link on the web /verify-email page instead (trusted via PUBLIC_WEB_URL /
 * BETTER_AUTH_URL).
 */
export function verifyEmailCallbackUrl(config: ServerConfig): string {
  return `${webBase(config) ?? ''}/verify-email`;
}
```

In `apps/mobile/lib/api.ts` make `configureApi`:

```ts
/** Point the shared API client at a server base URL discovered at runtime. */
export function configureApi(baseUrl: string): void {
  options.baseUrl = baseUrl.replace(/\/+$/, '') || DEFAULT_BASE;
  // A JWT minted for the previous server must never be sent to the next one.
  api.invalidateAccessToken();
}
```

and in its file doc comment replace `short-lived Better Auth JWT (minted per request)` with `short-lived Better Auth JWT (cached by ApiClient until 60 s before expiry)`.

In `apps/mobile/lib/auth-client.ts`, in the `getBetterAuthToken` doc comment replace `Minted per request` / `(default 15m expiry), never cached. Returns null when signed out/unreachable.` with `Minted on demand (default 15m expiry); ApiClient caches the result until 60 s before` / `expiry and drops it on sign-out or server switch. Returns null when signed out/unreachable.` (comment only — the function body is unchanged).

- [ ] **Step 5: Implement the context.** In `apps/mobile/context/auth.tsx`:

(a) add `import { api } from '@/lib/api';` after the `use-push-registration` import and change the server-config import to `import { useServerConfig, verifyEmailCallbackUrl } from '@/lib/server-config';`;

(b) change the interface line to `signUpWithEmail: (name: string, email: string, password: string) => Promise<{ verificationRequired: boolean }>;`;

(c) add after `DEMO_USER`:

```ts
const VERIFY_FIRST_MESSAGE = 'Verify your email first — we sent a new link.';

/** Shared wording for Better Auth client errors (mirrors the web app and desktop). */
function describeAuthError(
  error: { status?: number; code?: string; message?: string },
  fallback: string,
  retryAfter: string | null
): string {
  if (error.status === 429) {
    const n = Number(retryAfter);
    return `Too many attempts, try again in ${Number.isFinite(n) && n > 0 ? Math.ceil(n) : 60} s`;
  }
  if (error.code === 'EMAIL_NOT_VERIFIED') return VERIFY_FIRST_MESSAGE;
  return error.message ?? fallback;
}

/** Captures X-Retry-After from a Better Auth client call's onError hook. */
function retryAfterCapture() {
  let value: string | null = null;
  return {
    fetchOptions: {
      onError: (ctx: { response: Response }) => {
        value = ctx.response.headers.get('x-retry-after');
      },
    },
    get value() {
      return value;
    },
  };
}
```

(d) replace `signInWithEmail` and `signUpWithEmail` with:

```ts
  const signInWithEmail = async (email: string, password: string) => {
    if (!authClient) {
      Alert.alert('Connect a server', 'Choose a Calendium server before signing in.');
      return;
    }
    const retry = retryAfterCapture();
    const { error } = await authClient.signIn.email({ email, password }, retry.fetchOptions);
    if (error) {
      const message = describeAuthError(error, 'Sign in failed.', retry.value);
      Alert.alert(error.code === 'EMAIL_NOT_VERIFIED' ? 'Verify your email' : 'Error', message);
      throw new Error(message);
    }
    await refresh();
  };

  const signUpWithEmail = async (name: string, email: string, password: string) => {
    if (!authClient || !config) {
      Alert.alert('Connect a server', 'Choose a Calendium server before signing in.');
      return { verificationRequired: false };
    }
    const retry = retryAfterCapture();
    const { data, error } = await authClient.signUp.email(
      { name, email, password, callbackURL: verifyEmailCallbackUrl(config) },
      retry.fetchOptions
    );
    if (error) {
      const message = describeAuthError(error, 'Sign up failed.', retry.value);
      Alert.alert('Error', message);
      throw new Error(message);
    }
    // With SMTP configured the server never signs a new account in (and
    // answers the same for an existing address): token === null means the
    // user must open the emailed link before signing in.
    if (data && data.token === null) return { verificationRequired: true };
    await refresh();
    return { verificationRequired: false };
  };
```

(e) in `signOut`'s `finally` block add `api.invalidateAccessToken();` directly before `queryClient.clear();` (with the comment `// …and the cached API JWT, so the next account never rides this one's token.`).

- [ ] **Step 6: Implement the screen.** In `apps/mobile/app/index.tsx`:

(a) add `import * as Linking from 'expo-linking';` after the `expo-router` import and change the server-config import to `import { forgotPasswordUrl, useServerConfig } from '@/lib/server-config';`;

(b) add `const [notice, setNotice] = React.useState<string | null>(null);` after the `password` state;

(c) replace `handleEmailAuth` with:

```tsx
  const handleEmailAuth = async () => {
    try {
      setLoading(true);
      setNotice(null);
      if (mode === 'sign-up') {
        const { verificationRequired } = await signUpWithEmail(name.trim(), email.trim(), password);
        if (verificationRequired) {
          // No session yet: the server emailed a verification link.
          setNotice(`Check your inbox — we sent a verification link to ${email.trim()}.`);
          setMode('sign-in');
          setPassword('');
        }
      } else {
        await signInWithEmail(email.trim(), password);
      }
    } catch {
      // Error already surfaced by the auth context.
    } finally {
      setLoading(false);
    }
  };

  const openForgotPassword = () => {
    const url = forgotPasswordUrl(config);
    if (url) void Linking.openURL(url);
  };
```

(d) inside the email/password `<View className="w-full max-w-xs gap-3">`, directly after the submit `<Button onPress={handleEmailAuth} disabled={loading}>…</Button>`, add:

```tsx
            {notice && (
              <Text className="text-center text-sm text-muted-foreground" accessibilityRole="text">
                {notice}
              </Text>
            )}
            {mode === 'sign-in' && (
              <Button variant="ghost" size="sm" disabled={loading} onPress={openForgotPassword}>
                <Text className="text-sm text-muted-foreground">Forgot password?</Text>
              </Button>
            )}
```

- [ ] **Step 7: Run the mobile suite, typecheck and Biome.**

```bash
bun run test:mobile && cd apps/mobile && bunx tsc --noEmit && cd ../.. && bunx biome check apps/mobile
```

Expected: all mobile suites pass — the new four files plus the existing screen tests, which mock `@/context/auth` and `@/lib/api` and are unaffected; the existing `api.test.ts` cases still pass (`the-jwt` is opaque, so it is never cached, and `afterEach(() => configureApi(''))` now also clears the cache between tests); typecheck and Biome clean.

- [ ] **Step 8: Commit.**

```bash
git add apps/mobile/lib/server-config.ts apps/mobile/lib/server-config.test.ts apps/mobile/lib/api.ts apps/mobile/lib/api.test.ts apps/mobile/lib/auth-client.ts apps/mobile/context/auth.tsx apps/mobile/context/auth.test.tsx apps/mobile/app/index.tsx apps/mobile/app/index.test.tsx
git commit -m "feat(mobile): verification-required sign-up with an absolute verify callback, JWT invalidation on sign-out/server switch, forgot-password link" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 19: Operator docs, env templates, nginx sample, go-live checklist

**Files:**
- Modify: `.env.example` (root — CORS block, new SMTP and client-IP blocks), `apps/web/.env.example` (mode, public origin, SMTP, proxy trust, dev origins), `deploy/nginx/calendium.conf` (web `location /` overwrites `X-Forwarded-For`)
- Modify: `docs/self-hosting/configuration.md`, `docs/self-hosting/providers.md`, `docs/self-hosting/security.md`, `docs/self-hosting/reverse-proxy-tls.md`, `docs/self-hosting/upgrades.md`, `docs/self-hosting/troubleshooting.md`, `docs/release/go-live-external-checklist.md`

**Interfaces:**
- Consumes (documents, never redefines): the env names, defaults and verbatim startup messages in Global Constraints; `node apps/web/scripts/reset-password.mjs <email>` and its `Temporary password for <email>: <password>` output (Task 10); `features.email` (Task 5); migration `0028_better_auth_rate_limit.sql` (Task 9); the `TRUST_PROXY is not true in production: …` warning (Task 6); page copy from Tasks 12–15.
- Produces: doc anchors `configuration.md#transactional-email-smtp`, `providers.md#1d-transactional-email-smtp`, `security.md#10-sign-in-protection`, `security.md#resetting-a-password-without-email-self-host`, `troubleshooting.md#email-not-sending--smtp-errors`, `troubleshooting.md#too-many-attempts-try-again-in-n-s-http-429`, `troubleshooting.md#invalid-origin-403-on-sign-in` (linked from each other below).

This task touches no code and has no dependency on other tracks: it documents the behaviour fixed in Global Constraints. Paths and anchors only — never paste secrets.

- [ ] **Step 1: Write the failing check.**

```bash
grep -c '^SMTP_HOST=' .env.example apps/web/.env.example; grep -c 'ALLOW_DEV_ORIGINS' .env.example docs/self-hosting/configuration.md docs/self-hosting/security.md; grep -c 'reset-password.mjs' docs/self-hosting/security.md; grep -c 'X-Forwarded-For \$remote_addr' deploy/nginx/calendium.conf; grep -c 'Transactional email' docs/self-hosting/providers.md docs/self-hosting/configuration.md
```

Expected: every count is `0`.

- [ ] **Step 2: Root `.env.example`.** Replace the whole `# ─── Cross-origin requests (CORS) ───…` block (the header line, its four comment lines and `CORS_ALLOWED_ORIGINS=`) with:

```dotenv
# ─── Cross-origin requests (CORS) & trusted origins ──────────────────────────
# The Go API and the Better Auth routes always allow the desktop app's WebView
# origins (wails://wails, wails://wails.localhost, http(s)://wails.localhost)
# plus BETTER_AUTH_URL and PUBLIC_WEB_URL. Add extra browser origins here
# (comma-separated) — e.g. a web app served from a different origin than the API.
CORS_ALLOWED_ORIGINS=
# Also trust http://localhost:* and http://127.0.0.1:* origins. Leave blank
# (false) in production — setting it logs a startup warning there. `bun run dev`
# trusts them regardless.
ALLOW_DEV_ORIGINS=
```

and insert after the `# ─── Mail behavior ───…` block (after `UNDO_SEND_SECONDS=15`, before the Paddle block):

```dotenv
# ─── Transactional email (SMTP) ──────────────────────────────────────────────
# One sender for the whole instance: the web app sends email-verification and
# password-reset mail, the api sends team invitations when the inviter has no
# connected mailbox. Any SMTP provider works (SES, Postmark, Resend, Mailgun,
# your own relay). See docs/self-hosting/providers.md §1d.
#   SELF_HOSTED=true  : optional. Blank SMTP_HOST = email off: sign-up needs no
#                       verification, "Forgot password?" tells users to ask the
#                       admin (docs/self-hosting/security.md), and team
#                       invitations return a link to share by hand.
#   SELF_HOSTED=false : SMTP_HOST and SMTP_FROM are REQUIRED; api, worker and
#                       web refuse to start without them.
# Setting SMTP_HOST turns email verification ON for email+password sign-up;
# existing accounts verify once, on their next sign-in.
SMTP_HOST=
SMTP_PORT=587
# Optional login; set both or neither.
SMTP_USER=
SMTP_PASS=
# "addr" or "Name <addr>", e.g. Calendium <no-reply@mail.example.com>
SMTP_FROM=
# true = implicit TLS (usually port 465); false = STARTTLS, which is mandatory
# unless SMTP_HOST is loopback (127.0.0.1 / localhost — Mailpit, a local relay).
SMTP_SECURE=false

# ─── Client IP for sign-in rate limits (web) ─────────────────────────────────
# Sign-in, sign-up and password reset are rate limited per client IP, with the
# counters in Postgres. true = the client is the FIRST X-Forwarded-For hop:
# only correct behind a proxy that OVERWRITES that header (the bundled Caddy
# profile does; nginx: proxy_set_header X-Forwarded-For $remote_addr).
# Blank/false = X-Forwarded-For is used only when single-valued; production
# logs a startup warning. Keep it false when browsers reach web:3000 directly.
TRUST_PROXY=
```

- [ ] **Step 3: `apps/web/.env.example`** (used for `bun run dev:web`). Append:

```dotenv

# ── Instance mode & public origin ─────────────────────────────────────────────
# true = self-host: SMTP optional. Unset/false = cloud: the web server refuses
# to start (instrumentation.ts) unless SMTP_HOST and SMTP_FROM are set.
SELF_HOSTED=true
# Public web origin advertised to clients; also added to Better Auth's trusted origins.
PUBLIC_WEB_URL=http://localhost:3000

# ── Transactional email (SMTP) — verification + password reset ────────────────
# Blank SMTP_HOST disables email (no verification; /forgot-password shows the
# administrator instructions). Local testing with Mailpit:
#   docker run -d --name calendium-mailpit -p 18025:8025 -p 11025:1025 axllent/mailpit
#   SMTP_HOST=127.0.0.1  SMTP_PORT=11025  SMTP_FROM=Calendium <no-reply@calendium.test>
# and read the mail at http://localhost:18025.
SMTP_HOST=
SMTP_PORT=587
SMTP_USER=
SMTP_PASS=
SMTP_FROM=
SMTP_SECURE=false

# ── Proxy trust & dev origins ─────────────────────────────────────────────────
# true only behind a proxy that overwrites X-Forwarded-For (auth rate limits key on it).
TRUST_PROXY=false
# Trust http://localhost:* / http://127.0.0.1:* under `next start`
# (NODE_ENV=production). `next dev` trusts them regardless.
ALLOW_DEV_ORIGINS=false
```

- [ ] **Step 4: `deploy/nginx/calendium.conf`.** In the active `location / {` block (the one proxying to `calendium_web`) replace

```nginx
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
```

with

```nginx
        # Overwrite, never append: with TRUST_PROXY=true the web app keys its
        # sign-in rate limits on the FIRST X-Forwarded-For hop.
        proxy_set_header X-Forwarded-For $remote_addr;
```

and in the commented HTTPS block's `#     location / {` replace `#         proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;` with `#         proxy_set_header X-Forwarded-For $remote_addr;`. Leave both `location /v1/` blocks unchanged (the Go API resolves the client from the right-most untrusted hop, which works with appending).

- [ ] **Step 5: `docs/self-hosting/configuration.md`.**

(a) `SELF_HOSTED` row — replace its closing `The root \`.env.example\` defaults it to \`true\`. |` with:

```markdown
The root `.env.example` defaults it to `true`. Read by `api`, `worker` **and `web`**: with `false` (cloud mode) all three refuse to start unless `SMTP_HOST` and `SMTP_FROM` are set ([Transactional email](#transactional-email-smtp)). |
```

(b) `PUBLIC_WEB_URL` row — append ` The web app also adds it to Better Auth's trusted origins.` before the closing ` |`.

(c) In the **Web app (Next.js)** table replace the `CORS_ALLOWED_ORIGINS` row with these three rows:

```markdown
| `CORS_ALLOWED_ORIGINS` | No | — | Comma-separated extra browser origins trusted by **both** the Go API (CORS) and Better Auth (trusted origins). Always trusted without listing them: `BETTER_AUTH_URL`, `PUBLIC_WEB_URL`, the desktop app's WebView origins (`wails://wails`, `wails://wails.localhost`, `http(s)://wails.localhost`), `calendium://` and `https://appleid.apple.com` (Apple's sign-in `form_post`; trusted, never reflected in CORS). |
| `ALLOW_DEV_ORIGINS` | No | `false` | Also trust `http://localhost:*` / `http://127.0.0.1:*` (and `::1` on the API). `bun run dev` / `next dev` trusts them anyway; in production keep it blank — `true` logs a startup warning. Read by `web` and `api`. |
| `TRUST_PROXY` | No | `false` | How the web app finds the client IP for its sign-in rate limits. `true`: the first `X-Forwarded-For` hop — only behind a proxy that **overwrites** the header (bundled Caddy; nginx with `X-Forwarded-For $remote_addr`). `false`: the header only when single-valued; production logs a warning. See [Security → Sign-in protection](./security.md#10-sign-in-protection). |
```

(d) Insert a new section directly before `## Provider OAuth apps (mail + calendar) & social login`:

```markdown
## Transactional email (SMTP)

One instance-wide sender. The **web** app sends email-verification and
password-reset mail; the **api** sends team invitations when the inviter has
no connected mailbox. All three containers read the same variables.
Provider setup and DNS: [Providers → Transactional email](./providers.md#1d-transactional-email-smtp).

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `SMTP_HOST` | Cloud: **yes**. Self-host: no | — | SMTP server hostname. Required together with `SMTP_FROM` whenever any `SMTP_*` value is set. Blank on self-host = email off. |
| `SMTP_PORT` | No | `587` | Integer 1–65535 (`465` for implicit TLS). |
| `SMTP_USER` / `SMTP_PASS` | No | — | Login; set both or neither. **`SMTP_PASS` is a secret.** |
| `SMTP_FROM` | With `SMTP_HOST` | — | `addr` or `Name <addr>`, e.g. `Calendium <no-reply@mail.example.com>`. |
| `SMTP_SECURE` | No | `false` | `true` = implicit TLS; `false` = STARTTLS, **required** unless `SMTP_HOST` is loopback (`127.0.0.1`, `localhost`, `::1`). The server certificate is always verified. |

| | SMTP configured | No SMTP (self-host only) |
| --- | --- | --- |
| Email+password sign-up | Must verify the address (link valid 24 h); same response whether or not the address exists | Signed in immediately; a duplicate address is reported |
| Forgot password | Reset link by email (valid 1 h, signs out every device) | Page explains the administrator resets passwords ([procedure](./security.md#resetting-a-password-without-email-self-host)) |
| Team invitation, inviter has no connected mailbox | Sent from `SMTP_FROM`, `Reply-To` = inviter | The inviter gets a link to share (`delivery: "link"`, expires in 14 days) |
| `GET /v1/instance` `features.email` | `true` | `false` |

Startup: with `SELF_HOSTED=false` and no SMTP, `api`, `worker` and `web` exit
with `SMTP_HOST and SMTP_FROM are required when SELF_HOSTED=false (cloud mode); set them or run with SELF_HOSTED=true`;
a half-configured block exits with `SMTP_* is partially configured: <missing>`.
Self-host without SMTP boots and logs
`email: disabled (no SMTP_HOST); verification off, invitations fall back to links`.
```

(e) In the `/v1/instance` JSON sample replace

```json
    "push": false
  }
```

with

```json
    "push": false,
    "email": true
  }
```

and add after the `features.push` table row:

```markdown
| `features.email` | `true` when `SMTP_HOST` is set (an SMTP sender is configured). The web forgot-password page shows the administrator instructions only when this is explicitly `false`. |
```

(f) Replace the paragraph under **Minimum viable configuration** that starts `Add \`GOOGLE_*\` / \`APPLE_*\` for social sign-in` with:

```markdown
Add `SMTP_*` to turn on email verification and self-service password reset
(required in cloud mode), `GOOGLE_*` / `APPLE_*` for social sign-in (the Google
creds also connect Gmail/Calendar), `MS_*` for Outlook, `OPENROUTER_API_KEY` for
AI, and push keys as needed — each unlocks its adapter without touching the rest.
```

- [ ] **Step 6: `docs/self-hosting/providers.md`.**

(a) In §1c, directly after the `> **One Google app for both jobs.** …` blockquote (ending `` `https://YOUR_DOMAIN/v1/accounts/callback/google`. ``), add:

```markdown
> **Apple posts back to Better Auth.** Sign in with Apple returns with a
> cross-site `form_post` from `https://appleid.apple.com`. That origin is always
> in Better Auth's trusted origins (it is never reflected in CORS), so there is
> nothing to configure — but the callback only works on your real HTTPS domain;
> Apple rejects `localhost`.
```

(b) Insert before `### How clients discover what you configured`:

````markdown
### 1d. Transactional email (SMTP)

Calendium sends three kinds of mail from one instance-wide sender:

| Mail | Sent by | When |
| --- | --- | --- |
| Verify your email (link valid 24 h) | web (Better Auth) | Email+password sign-up, and again on sign-in while still unverified |
| Reset your password (link valid 1 h, single use) | web (Better Auth) | `/forgot-password` |
| Team invitation | api | The inviter has **no** connected mailbox — a connected Gmail/Outlook mailbox is preferred and sends as the inviter |

Any SMTP provider works — Amazon SES, Postmark, Resend, Mailgun, SendGrid,
Fastmail, or your own relay. Set the `SMTP_*` variables
([reference](./configuration.md#transactional-email-smtp)) in `.env`; `web`,
`api` and `worker` all read them.

```dotenv
SMTP_HOST=smtp.your-provider.example
SMTP_PORT=587
SMTP_USER=<username or API token>
SMTP_PASS=<password or API token>
SMTP_FROM=Calendium <no-reply@mail.example.com>
SMTP_SECURE=false          # STARTTLS on 587; use SMTP_PORT=465 + SMTP_SECURE=true for implicit TLS
```

**Deliverability — publish these for the `SMTP_FROM` domain** or verification
and reset links land in spam (and a user who never receives the link cannot
sign in):

- **SPF** — a TXT record authorising your provider (`v=spf1 include:<provider's SPF host> ~all`; the provider documents the include).
- **DKIM** — the CNAME/TXT keys your provider generates, so mail is signed with the `SMTP_FROM` domain.
- **DMARC** — `_dmarc.<domain>` TXT, starting at `v=DMARC1; p=none; rua=mailto:dmarc@<domain>`; tighten to `quarantine`/`reject` once the reports are clean.

**Self-host without SMTP** is supported: leave `SMTP_HOST` blank. Sign-up then
signs users in without verification, `/forgot-password` tells them to ask you
([reset procedure](./security.md#resetting-a-password-without-email-self-host)),
and team invitations return a link for the inviter to share. Cloud mode
(`SELF_HOSTED=false`) refuses to start without SMTP.

**Adding SMTP later turns verification on** — existing email+password users
verify once, on their next sign-in ([Upgrading](./upgrades.md#turning-on-email-smtp)).

**Try it locally with Mailpit** (a fake inbox; ports 18025/11025 so they don't
collide with another Mailpit on the defaults):

```bash
docker run -d --name calendium-mailpit -p 18025:8025 -p 11025:1025 axllent/mailpit
# .env:  SMTP_HOST=127.0.0.1  SMTP_PORT=11025  SMTP_SECURE=false  SMTP_FROM=Calendium <no-reply@calendium.test>
# inbox: http://localhost:18025
```

Plaintext SMTP is allowed only to a loopback host, so point a web app and API
running **on the host** (`bun run dev:web`, `bun run dev:api`) at Mailpit.
Inside the compose stack `127.0.0.1` is the container itself, and a
non-loopback host such as `host.docker.internal` requires STARTTLS, which a
default Mailpit does not offer.
````

(c) In the `How clients discover what you configured` JSON sample replace `"push": false }` with `"push": false, "email": true }`, and after the paragraph that ends `See [Pointing the Apps at Your Server](./clients.md) for the client flow.` add: `` `features.email` is `true` when an SMTP sender is configured; the web forgot-password page falls back to the administrator instructions only when it is `false`. ``

- [ ] **Step 7: `docs/self-hosting/security.md`.**

(a) In **Checklist at a glance**, after `- [ ] Managed Postgres reached with \`sslmode=require\` (or stricter)` add:

```markdown
- [ ] `web` behind your proxy with `TRUST_PROXY=true`, and the proxy overwrites `X-Forwarded-For`
- [ ] `ALLOW_DEV_ORIGINS` blank (false) in production
- [ ] SMTP configured over TLS (verification + password reset), or users know to ask you for a reset
```

(b) In §3's table change the secrets row's first cell to `` `GOOGLE_CLIENT_SECRET`, `APPLE_CLIENT_SECRET`, `MS_CLIENT_SECRET`, `PADDLE_API_KEY`, `PADDLE_WEBHOOK_SECRET`, `SMTP_PASS` `` (append `` , `SMTP_PASS` `` to whatever list piece 1 left there).

(c) Insert before `## Related pages`:

````markdown
## 10. Sign-in protection

### Rate limits (Postgres-backed)

Better Auth rate-limits its endpoints per client IP in every environment. The
counters live in the `rateLimit` table (migration
`0028_better_auth_rate_limit.sql`), so they survive restarts and are shared by
every `web` replica.

| Endpoint (under `/api/auth`) | Limit |
| --- | --- |
| `/sign-in/email` | 5 per 60 s |
| `/sign-up/email` | 3 per 60 s |
| `/request-password-reset` (and the legacy `/forget-password`) | 3 per 10 min |
| `/send-verification-email` | 3 per 10 min |
| `/token` (the API JWT; clients cache it until 60 s before expiry) | 60 per 60 s |
| `/change-password` | 3 per 10 s (Better Auth built-in) |
| everything else | 100 per 60 s |

Over the limit → `429` with `X-Retry-After: <seconds>`; the apps say
"Too many attempts, try again in N s".

### Client IP: `TRUST_PROXY`

The web server cannot see the TCP peer, so it derives the client IP from
headers and stamps it into a server-only header (`x-calendium-client-ip`,
overwritten on every request — a client-supplied value is discarded):

- `TRUST_PROXY=true` — the first `X-Forwarded-For` hop. Correct **only** behind
  a proxy that overwrites the header: the bundled Caddy profile does; nginx needs
  `proxy_set_header X-Forwarded-For $remote_addr;` in the web `location /` (the
  [sample](../../deploy/nginx/calendium.conf) does). Behind an appending proxy a
  client can forge the first hop and choose its own bucket.
- `TRUST_PROXY=false` (default) — `X-Forwarded-For` only when it holds exactly
  one address; anything else lands in one shared bucket. A client that reaches
  `web:3000` directly can send its own single-valued header and get its own
  bucket, which is why production logs a warning. The supported production shape
  is Caddy (or nginx as above) + `TRUST_PROXY=true`.

### Origins

Better Auth accepts state-changing requests only from trusted origins:
`BETTER_AUTH_URL`, `PUBLIC_WEB_URL`, `CORS_ALLOWED_ORIGINS`, the desktop app's
WebView origins (`wails://wails`, `wails://wails.localhost`,
`http(s)://wails.localhost`), the mobile scheme `calendium://`, and
`https://appleid.apple.com` (Apple's `form_post`; never reflected in CORS).
`http://localhost:*` / `http://127.0.0.1:*` are trusted only in development or
with `ALLOW_DEV_ORIGINS=true`, and the Go API's CORS applies the same rule.
Keep `ALLOW_DEV_ORIGINS` blank in production (setting it logs a warning). The
packaged desktop app needs no configuration.

### Email verification and passwords

With SMTP configured, email+password accounts must verify their address
(link valid 24 h); sign-up, resend and reset answer identically whether or not
the address exists, and sign-in reveals "verify your email first" only after a
correct password. Passwords are 10–128 characters and must not contain the
address's local part. A reset (link valid 1 h, single use) signs out every
device; a change in **Settings → Account** signs out every other device. API
JWTs live at most 15 minutes, so a cached token never outlives its session by
more. Without SMTP the duplicate-address error on sign-up is visible — the
documented trade-off of running without email.

### Resetting a password without email (self-host)

Without SMTP users cannot reset their own password; `/forgot-password` tells
them to ask you. As the operator:

```bash
# 1. Confirm the account exists
docker compose exec db psql -U calendium -d calendium -c "SELECT id FROM \"user\" WHERE email = 'ada@example.com';"
# 2. Set a temporary password and sign out every device
docker compose exec web node apps/web/scripts/reset-password.mjs ada@example.com
```

The script hashes a fresh temporary password with Better Auth's own
`hashPassword`, upserts the user's `credential` row in `account`, deletes all of
their `session` rows and prints `Temporary password for <email>: <password>`
once. Hand it over on a channel you trust; the user signs in and changes it in
**Settings → Account**.
````

- [ ] **Step 8: `docs/self-hosting/reverse-proxy-tls.md`.**

(a) In **The Caddyfile, explained**, after the bullet that starts `` - `api:8080` and `web:3000` are Docker **service names** `` add:

```markdown
- Caddy sets `X-Forwarded-For` to the connecting client's address and ignores
  whatever the client sent — exactly what the web app's sign-in rate limits
  need. With this profile set `TRUST_PROXY=true` in `.env`
  ([why](./security.md#10-sign-in-protection)).
```

(b) In the nginx section, directly after the `> **You MUST forward \`X-Forwarded-Proto\`` blockquote, add:

```markdown
> **Overwrite `X-Forwarded-For` for the web app.** With `TRUST_PROXY=true` the
> web app's sign-in rate limits key on the *first* `X-Forwarded-For` hop, so its
> `location /` must replace the header — `proxy_set_header X-Forwarded-For $remote_addr;`
> — not append with `$proxy_add_x_forwarded_for`, or a client can forge that hop.
> The sample does this for `location /`. Load balancers that only append (AWS
> ALB, Google Cloud Load Balancing) make the first hop client-controlled: there,
> keep `TRUST_PROXY=false`.
```

- [ ] **Step 9: `docs/self-hosting/upgrades.md`.** Insert after the `## Better Auth schema` section (before `## Keep migrations backward-compatible`):

```markdown
## Turning on email (SMTP)

- **Adding `SMTP_HOST`/`SMTP_FROM` turns email verification on** for
  email+password accounts. Existing users are not grandfathered: their next
  sign-in answers "Verify your email first — we sent a new link." and mails the
  link; after one click they sign in as before. Google/Apple sign-ins are
  unaffected. Tell your users before you flip it. Removing SMTP turns
  verification off again; nothing in the database changes.
- This release adds migration `0028_better_auth_rate_limit.sql` (the `rateLimit`
  table behind the sign-in rate limits); the API applies it at boot like any other.
- **Cloud (`SELF_HOSTED=false`)**: `api`, `worker` and `web` now refuse to start
  without `SMTP_HOST` and `SMTP_FROM` — set them before upgrading.
- **`web` now reads `SELF_HOSTED`.** The compose stack already passes `.env` to
  `web`. If you run the web app another way (for example `bun run dev:web` with
  `apps/web/.env`), add `SELF_HOSTED=true` for a self-hosted instance, or it
  boots in cloud mode and stops with the SMTP error.
- **localhost origins are no longer trusted in production.** If a browser client
  served from `http://localhost:<port>` talks to a production server, add that
  origin to `CORS_ALLOWED_ORIGINS` (or set `ALLOW_DEV_ORIGINS=true`, which logs a
  warning). The desktop app is unaffected.
- **Behind a proxy, set `TRUST_PROXY=true`** (and make nginx overwrite
  `X-Forwarded-For`, see [Reverse proxy](./reverse-proxy-tls.md)); otherwise every
  client shares one sign-in rate-limit bucket.
```

- [ ] **Step 10: `docs/self-hosting/troubleshooting.md`.** Add three rows at the end of the **Quick index** table:

```markdown
| `SMTP_*` startup error, "We couldn't send the email" | [Email not sending / SMTP errors](#email-not-sending--smtp-errors) |
| "Too many attempts, try again in N s" / `429` on sign-in | [Sign-in rate limits](#too-many-attempts-try-again-in-n-s-http-429) |
| `403 Invalid origin` on sign-in, CORS errors from a new origin | [Invalid origin](#invalid-origin-403-on-sign-in) |
```

and insert before `## Still stuck?`:

```markdown
## Email not sending / SMTP errors

**Symptom:** `api`, `worker` or `web` exits with
`SMTP_HOST and SMTP_FROM are required when SELF_HOSTED=false (cloud mode); set them or run with SELF_HOSTED=true`
or `SMTP_* is partially configured: <missing>`; or sign-up / forgot-password
shows "We couldn't send the email. Try again in a minute."; or inviting a
teammate fails with `sending invitation email: …` (500).

**Fix, by cause:**

- **Startup error** — set `SMTP_HOST` and `SMTP_FROM` (and `SMTP_USER` +
  `SMTP_PASS` together, or neither), or run self-hosted with `SELF_HOSTED=true`.
- **`smtp: server does not offer STARTTLS; refusing to send credentials in clear`**
  (api log) or a STARTTLS/`requireTLS` error in the `web` log — the server on
  `SMTP_PORT` offers no STARTTLS. Use the provider's STARTTLS port (`587`) with
  `SMTP_SECURE=false`, or its implicit-TLS port (`465`) with `SMTP_SECURE=true`.
  Plaintext is allowed only to loopback (`127.0.0.1`, `localhost`).
- **`535` / authentication failed** — wrong `SMTP_USER`/`SMTP_PASS` (many
  providers use an API token for both).
- **Certificate errors** — the server certificate is always verified; set
  `SMTP_HOST` to the provider's hostname, not an IP address.
- The `web` log line `email.send_failed` carries the recipient's domain and the
  provider's error (never the message) — start there.

**Mail is sent but never arrives:** check spam, then SPF/DKIM/DMARC
([Providers → Transactional email](./providers.md#1d-transactional-email-smtp)).
**`/forgot-password` says reset by email isn't available:** no SMTP is
configured (`curl -s https://your-domain/v1/instance | jq .features.email` is
`false`) — configure it, or reset the password yourself
([procedure](./security.md#resetting-a-password-without-email-self-host)).

---

## "Too many attempts, try again in N s" (HTTP 429)

**Symptom:** sign-in, sign-up or forgot-password answers `429` with an
`X-Retry-After` header.

**Cause:** the per-IP sign-in limits ([Security → Rate limits](./security.md#rate-limits-postgres-backed)).
If *everyone* hits them at once, the web app cannot tell clients apart and they
share one bucket: the `web` log shows
`TRUST_PROXY is not true in production: …` at startup.

**Fix:** behind Caddy, or nginx overwriting `X-Forwarded-For`, set
`TRUST_PROXY=true` and restart `web`. To clear the counters (for example after a
load test): `docker compose exec db psql -U calendium -d calendium -c 'DELETE FROM "rateLimit";'`.

---

## "Invalid origin" (403) on sign-in

**Symptom:** sign-in from a browser, desktop or mobile client fails with `403`
`Invalid origin` (the `web` log names the rejected origin), or the browser blocks
API calls with a CORS error.

**Cause:** the request's `Origin` is not trusted. Production trusts
`BETTER_AUTH_URL`, `PUBLIC_WEB_URL`, `CORS_ALLOWED_ORIGINS`, the desktop WebView
origins, `calendium://` and `https://appleid.apple.com`; `localhost` /
`127.0.0.1` origins only with `ALLOW_DEV_ORIGINS=true`.

**Fix:** make `BETTER_AUTH_URL` and `PUBLIC_WEB_URL` exactly your public origin
(scheme, host and port; no trailing slash), add any other web origin to
`CORS_ALLOWED_ORIGINS`, and restart `web` and `api`. For a local dev web app
pointed at a production server, `ALLOW_DEV_ORIGINS=true` works (and logs a
warning).

---
```

- [ ] **Step 11: `docs/release/go-live-external-checklist.md`.** Append to §5 (after the SPF/DKIM/DMARC item):

```markdown
- [ ] From the production domain, sign up with a real inbox: the verification
      mail arrives (not in spam) and its link opens `https://<DOMAIN>/verify-email`.
- [ ] The proxy overwrites `X-Forwarded-For` (bundled Caddy does; nginx:
      `proxy_set_header X-Forwarded-For $remote_addr;` in the web `location /`)
      and `TRUST_PROXY=true` is set for `web` — otherwise every client shares
      one sign-in rate-limit bucket.
- [ ] `ALLOW_DEV_ORIGINS` blank in the production `.env`.
- Code dependency: piece 2 makes `api`, `worker` and `web` refuse to start
  with `SELF_HOSTED=false` and no `SMTP_HOST`/`SMTP_FROM`.
```

- [ ] **Step 12: Re-run the check and the link targets.**

```bash
grep -c '^SMTP_HOST=' .env.example apps/web/.env.example; grep -c 'ALLOW_DEV_ORIGINS' .env.example docs/self-hosting/configuration.md docs/self-hosting/security.md; grep -c 'reset-password.mjs' docs/self-hosting/security.md; grep -c 'X-Forwarded-For \$remote_addr' deploy/nginx/calendium.conf; grep -c 'Transactional email' docs/self-hosting/providers.md docs/self-hosting/configuration.md
grep -nxF -e '## Transactional email (SMTP)' -e '### 1d. Transactional email (SMTP)' -e '## 10. Sign-in protection' -e '### Rate limits (Postgres-backed)' -e '### Resetting a password without email (self-host)' -e '## Turning on email (SMTP)' -e '## Email not sending / SMTP errors' -e '## "Too many attempts, try again in N s" (HTTP 429)' -e '## "Invalid origin" (403) on sign-in' docs/self-hosting/*.md
```

Expected: `.env.example:1`, `apps/web/.env.example:1`; every `ALLOW_DEV_ORIGINS` count ≥ 1; `reset-password.mjs` ≥ 1; nginx `2`; `Transactional email` ≥ 1 in both docs; the second command prints exactly nine headings (one per anchor linked above).

- [ ] **Step 13: Commit.**

```bash
git add .env.example apps/web/.env.example deploy/nginx/calendium.conf docs/self-hosting/configuration.md docs/self-hosting/providers.md docs/self-hosting/security.md docs/self-hosting/reverse-proxy-tls.md docs/self-hosting/upgrades.md docs/self-hosting/troubleshooting.md docs/release/go-live-external-checklist.md
git commit -m "docs(self-host): SMTP setup and deliverability, sign-in rate limits and TRUST_PROXY, trusted origins, no-email password reset; env templates and nginx XFF overwrite" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 20: FULL-SUITE GATE (after every track is merged)

**Files:** none modified (fix-forward commits only if a step fails; each fix goes to the track that owns the file and re-runs that track's task tests first).

- [ ] **Step 1: Go build, vet, race tests (incl. testcontainers, which apply migrations through `0028`):**

```bash
cd /Users/guilherme/Dev/pessoal/calendium/backend && go build ./... && go vet ./... && REQUIRE_DOCKER=1 go test -race ./... 2>&1 | tail -40
```

Expected: every package `ok`, including `internal/config`, `internal/adapter/out/smtp`, `internal/service`, `internal/adapter/in/httpapi` and `internal/adapter/out/postgres` (its migration test applies `0028_better_auth_rate_limit.sql`); no `FAIL`, no `DATA RACE`.

- [ ] **Step 2: TypeScript suites, one at a time so a failure names its workspace:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && bun run test:shared && bun run test:web && bun run test:desktop && bun run test:mobile
```

Expected: all green — shared includes `access-token-cache.test.ts` and the 401-retry block in `client.test.ts`; web includes `auth-env`, `email/*`, `instrumentation`, `auth`, `auth-client`, `auth-copy`, the sign-in/forgot/reset/verify-email page tests and `settings-account`; desktop includes `SignInView.test.tsx`; mobile includes `context/auth.test.tsx` and `app/index.test.tsx`.

- [ ] **Step 3: Lint and typecheck every workspace:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && bun run lint && bun run typecheck && (cd apps/desktop/frontend && bunx tsc --noEmit) && (cd apps/mobile && bunx tsc --noEmit)
```

Expected: Biome and golangci-lint (backend + desktop modules) report nothing; all four `tsc --noEmit` runs are clean.

- [ ] **Step 4: Playwright, production build path (demo mode, `SELF_HOSTED=true` from `playwright.config.ts`):**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && CI=1 bun run test:e2e 2>&1 | tail -20
```

Expected: every spec passes, including `auth-recovery.spec.ts` and `settings-account.spec.ts`; `apps/web/e2e/fixtures.ts` is unchanged by this piece (`git diff --quiet "$(git merge-base HEAD main)" -- apps/web/e2e/fixtures.ts && echo fixtures-unchanged` prints `fixtures-unchanged`).

- [ ] **Step 5: `next build` never needs SMTP or `SELF_HOSTED`:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium/apps/web && env -u SMTP_HOST -u SMTP_FROM -u SMTP_USER -u SMTP_PASS -u SELF_HOSTED NEXT_PUBLIC_API_URL=https://ci.invalid bunx next build 2>&1 | tail -5
```

Expected: the build completes (route table printed, exit 0) — `instrumentation.ts` `register()` runs only at server start, and no module constructs a mail transport at import time.

- [ ] **Step 6: Both images build; the web image ships the operator CLI:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && docker build -f backend/Dockerfile -t calendium-backend:gate . 2>&1 | tail -2 && docker build -f apps/web/Dockerfile --build-arg NEXT_PUBLIC_API_URL=https://ci.invalid -t calendium-web:gate . 2>&1 | tail -2 && docker run --rm --entrypoint ls calendium-web:gate apps/web/scripts/reset-password.mjs
```

Expected: both builds succeed and the last command prints `apps/web/scripts/reset-password.mjs`. (`docker build`/`docker run --rm` only; nothing is started with compose.)

- [ ] **Step 7: Spec greps — nothing left on the old paths:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && ls backend/migrations | grep rate_limit && ! grep -rn 'TestTeamInviteRequiresConnectedAccount' backend && ! grep -rniE 'do not cache it|do NOT cache it|minted per request|never cached\. Returns' apps/web/lib apps/desktop/frontend/src/lib apps/mobile/lib && grep -c '^SMTP_HOST=' .env.example backend/.env.example apps/web/.env.example && grep -rln 'Forgot password?' apps/web/app/signin apps/desktop/frontend/src/views apps/mobile/app | grep -v test && ! grep -n "'http://localhost:\*'" apps/web/lib/auth.ts && grep -c 'X-Forwarded-For \$remote_addr' deploy/nginx/calendium.conf
```

Expected: exactly `0028_better_auth_rate_limit.sql` from the `ls`; the three negated greps succeed silently; `.env.example:1`, `backend/.env.example:1`, `apps/web/.env.example:1`; three files with the "Forgot password?" link (`apps/web/app/signin/page.tsx`, `apps/desktop/frontend/src/views/SignInView.tsx`, `apps/mobile/app/index.tsx`); `2` nginx lines.

---

### Task 21: VERIFICATION RUNBOOK — Mailpit, host-run api + production web (manual; marks itself BLOCKED when operator inputs are absent)

**Files:** none modified. Everything runs from a scratch directory (`/tmp/calendium-email-runbook`) against throwaway containers on non-default ports, so a live compose stack on 3000/8080 is never touched. Record the outcome of every step (expected vs. actual) in the task result.

Why host-run processes and not compose: plaintext SMTP is allowed only to a loopback host (Global Constraints), and Mailpit offers no STARTTLS by default; inside compose, Mailpit would be `host.docker.internal` (non-loopback), which the Go adapter and nodemailer correctly refuse. So the api runs as a binary and the web as `next build` + `next start` (`NODE_ENV=production`, which also exercises the production-only origin gate and warnings) on the host, with Postgres and Mailpit in standalone containers.

- [ ] **Step 1: Preconditions (else BLOCKED, not failed).** Tasks 1–20 merged and green. `docker info` succeeds; `jq`, `curl`, `go`, `bun`, `node`, `openssl` and `perl` are on PATH. Ports `13000`, `13001`, `15432`, `18025`, `11025`, `18080`, `18081` are free (`for p in 13000 13001 15432 18025 11025 18080 18081; do lsof -nP -iTCP:$p -sTCP:LISTEN >/dev/null && echo "busy $p"; done` prints nothing) and no container is named `calendium-rb-pg` or `calendium-mailpit` (`docker ps -a --format '{{.Names}}' | grep -xE 'calendium-rb-pg|calendium-mailpit'` prints nothing). A busy port or an existing container → **BLOCKED: <port|container> in use; do not stop or remove it** and stop. Use only curl and the Mailpit API; never read `.env` or `apps/web/.env*`. If `apps/web/.env`, `.env.local` or `.env.production` exists, the env files written below set every variable this runbook depends on explicitly (empty where it must be off), so Next.js cannot fill them from those files.

- [ ] **Step 2: Scratch env and throwaway services:**

```bash
mkdir -p /tmp/calendium-email-runbook && cd /tmp/calendium-email-runbook
cat > vars.sh <<'EOF'
J=/tmp/calendium-email-runbook
W=http://localhost:13000
A=http://localhost:18080
M=http://localhost:18025
H='Content-Type: application/json'
O='Origin: http://localhost:13000'
mail_count() { curl -s "$M/api/v1/search?query=to:$1" | jq '.messages_count'; }
last_mail() { curl -s "$M/api/v1/message/$(curl -s "$M/api/v1/search?query=to:$1" | jq -r '.messages[0].ID')"; }
EOF
cat > rb.env <<EOF
DATABASE_URL=postgres://calendium:rb-pg-pass@127.0.0.1:15432/calendium?sslmode=disable
TOKEN_ENCRYPTION_KEY=$(openssl rand -hex 32)
BETTER_AUTH_SECRET=$(openssl rand -base64 32)
BETTER_AUTH_URL=http://localhost:13000
PUBLIC_WEB_URL=http://localhost:13000
APP_URL=http://localhost:13000
NEXT_PUBLIC_API_URL=http://localhost:18080
HTTP_ADDR=127.0.0.1:18080
SELF_HOSTED=true
TRUST_PROXY=
ALLOW_DEV_ORIGINS=
CORS_ALLOWED_ORIGINS=
SMTP_HOST=127.0.0.1
SMTP_PORT=11025
SMTP_USER=
SMTP_PASS=
SMTP_SECURE=false
SMTP_FROM="Calendium <no-reply@calendium.test>"
EOF
docker run -d --name calendium-rb-pg -e POSTGRES_USER=calendium -e POSTGRES_PASSWORD=rb-pg-pass -e POSTGRES_DB=calendium -p 127.0.0.1:15432:5432 postgres:16-alpine
docker run -d --name calendium-mailpit -p 127.0.0.1:18025:8025 -p 127.0.0.1:11025:1025 axllent/mailpit
for i in $(seq 1 30); do docker exec calendium-rb-pg pg_isready -U calendium -d calendium >/dev/null 2>&1 && echo pg-ready && break; sleep 1; done
curl -s http://localhost:18025/api/v1/messages | jq '.messages_count'
```

Expected: two container ids, `pg-ready`, and `0` from Mailpit.

- [ ] **Step 3: Build and start the api and the production web app:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && . /tmp/calendium-email-runbook/vars.sh && set -a && . $J/rb.env && set +a
(cd backend && go build -o $J/api ./cmd/api && go build -o $J/worker ./cmd/worker) && echo go-built
bun run --cwd apps/web build > $J/web-build.log 2>&1 && echo web-built
(cd apps/web && nohup bunx next start -p 13000 > $J/web.log 2>&1 &)
for i in $(seq 1 60); do curl -sf -o /dev/null $W/signin && echo web-up && break; sleep 2; done
nohup $J/api > $J/api.log 2>&1 & echo $! > $J/api.pid
for i in $(seq 1 60); do curl -sf -o /dev/null $A/healthz && echo api-up && break; sleep 1; done
curl -s $A/v1/instance | jq -c '{mode, email: .features.email, webUrl}'
grep -c 'email enabled' $J/api.log; grep -c 'email: disabled' $J/api.log $J/web.log; grep -c 'TRUST_PROXY is not true in production' $J/web.log
docker exec calendium-rb-pg psql -U calendium -d calendium -tAc 'SELECT count(*) FROM "rateLimit"'
```

Expected: `go-built`, `web-built`, `web-up`, `api-up`; `{"mode":"self_host","email":true,"webUrl":"http://localhost:13000"}`; `1` (`api: email enabled host=127.0.0.1 port=11025 secure=false`); `0` and `0`; `1` (production + `TRUST_PROXY` unset warns once); `0` (migration `0028` created the table, nothing counted yet).

- [ ] **Step 4: Sign up, receive the mail, verify (spec runbook 1):**

```bash
. /tmp/calendium-email-runbook/vars.sh
curl -s -X POST $W/api/auth/sign-up/email -H "$H" -H "$O" -d '{"name":"Ada","email":"ada@example.test","password":"correct-horse-battery","callbackURL":"/verify-email"}' | tee $J/signup1.json | jq -c '{token, keys: (.user | keys)}'
sleep 2; mail_count ada@example.test
last_mail ada@example.test | jq -c '{from: .From.Address, subject: .Subject}'
LINK=$(last_mail ada@example.test | jq -r .Text | grep -oE 'http://localhost:13000/api/auth/verify-email\?[^[:space:]]+' | head -1); echo "$LINK"
curl -s -o /dev/null -c $J/ada.jar -w '%{http_code} %{redirect_url}\n' "$LINK"
curl -s -b $J/ada.jar $W/api/auth/get-session | jq -c '{email: .user.email, verified: .user.emailVerified}'
```

Expected: `{"token":null,"keys":[…]}`; `1`; `{"from":"no-reply@calendium.test","subject":"Verify your email for Calendium"}`; a link built from `BETTER_AUTH_URL` with `callbackURL` `/verify-email`; `302 http://localhost:13000/verify-email`; `{"email":"ada@example.test","verified":true}` (`autoSignInAfterVerification` set the session cookie on the redirect).

- [ ] **Step 5: Same address again — identical response, no mail (spec runbook 2):**

```bash
. /tmp/calendium-email-runbook/vars.sh
curl -s -X POST $W/api/auth/sign-up/email -H "$H" -H "$O" -d '{"name":"Ada","email":"ada@example.test","password":"another-horse-battery","callbackURL":"/verify-email"}' > $J/signup2.json
diff <(jq -c '{token, keys: (.user | keys)}' $J/signup1.json) <(jq -c '{token, keys: (.user | keys)}' $J/signup2.json) && echo same-shape
sleep 2; mail_count ada@example.test
```

Expected: `same-shape` and still `1`.

- [ ] **Step 6: Unverified sign-in → 403 and a fresh link:**

```bash
. /tmp/calendium-email-runbook/vars.sh
curl -s -o /dev/null -X POST $W/api/auth/sign-up/email -H "$H" -H "$O" -d '{"name":"Bob","email":"bob@example.test","password":"correct-horse-battery","callbackURL":"/verify-email"}'
curl -s -w '\n%{http_code}\n' -X POST $W/api/auth/sign-in/email -H "$H" -H "$O" -d '{"email":"bob@example.test","password":"correct-horse-battery"}'
sleep 2; mail_count bob@example.test
```

Expected: a body with `"code":"EMAIL_NOT_VERIFIED"` and `403`; `2` (`sendOnSignIn` re-sent the link).

- [ ] **Step 7: Forgot → reset → sessions gone → reused link rejected (spec runbook 3):**

```bash
. /tmp/calendium-email-runbook/vars.sh
curl -s -c $J/ada2.jar -X POST $W/api/auth/sign-in/email -H "$H" -H "$O" -d '{"email":"ada@example.test","password":"correct-horse-battery"}' | jq -r '.user.email'
curl -s -w ' %{http_code}\n' -X POST $W/api/auth/request-password-reset -H "$H" -H "$O" -d '{"email":"ada@example.test","redirectTo":"/reset-password"}'
curl -s -w ' %{http_code}\n' -X POST $W/api/auth/request-password-reset -H "$H" -H "$O" -d '{"email":"nobody@example.test","redirectTo":"/reset-password"}'
sleep 2; mail_count nobody@example.test; last_mail ada@example.test | jq -r .Subject
RLINK=$(last_mail ada@example.test | jq -r .Text | grep -oE 'http://localhost:13000/api/auth/reset-password/[^[:space:]]+' | head -1); echo "$RLINK"
curl -s -o /dev/null -w '%{http_code} %{redirect_url}\n' "$RLINK"
TOKEN=$(echo "$RLINK" | sed -E 's#.*/reset-password/([^?]+).*#\1#')
curl -s -w ' %{http_code}\n' -X POST $W/api/auth/reset-password -H "$H" -H "$O" -d "{\"newPassword\":\"ADA-was-here-2026\",\"token\":\"$TOKEN\"}"
curl -s -w ' %{http_code}\n' -X POST $W/api/auth/reset-password -H "$H" -H "$O" -d "{\"newPassword\":\"new-horse-battery-2\",\"token\":\"$TOKEN\"}"
curl -s -b $J/ada.jar $W/api/auth/get-session; echo; curl -s -b $J/ada2.jar $W/api/auth/get-session; echo
curl -s -o /dev/null -w '%{http_code} %{redirect_url}\n' "$RLINK"
curl -s -w ' %{http_code}\n' -X POST $W/api/auth/reset-password -H "$H" -H "$O" -d "{\"newPassword\":\"newer-horse-battery-3\",\"token\":\"$TOKEN\"}"
```

Expected: `ada@example.test`; two lines with byte-identical JSON bodies and `200` (generic for known and unknown addresses); `0` and `Reset your Calendium password`; the reset link; `302 http://localhost:13000/reset-password?token=<TOKEN>`; `PASSWORD_CONTAINS_EMAIL … 400` (the policy hook resolved the email from the reset token, case-insensitively; the token is not consumed); a success body and `200`; `null` twice (`revokeSessionsOnPasswordReset`); `302 http://localhost:13000/reset-password?error=INVALID_TOKEN`; `INVALID_TOKEN … 400`.

- [ ] **Step 8: Change password → the other session is signed out (spec runbook 5):**

```bash
. /tmp/calendium-email-runbook/vars.sh
curl -s -c $J/a.jar -o /dev/null -X POST $W/api/auth/sign-in/email -H "$H" -H "$O" -d '{"email":"ada@example.test","password":"new-horse-battery-2"}'
curl -s -c $J/b.jar -o /dev/null -X POST $W/api/auth/sign-in/email -H "$H" -H "$O" -d '{"email":"ada@example.test","password":"new-horse-battery-2"}'
curl -s -b $J/a.jar -c $J/a.jar -o /dev/null -w '%{http_code}\n' -X POST $W/api/auth/change-password -H "$H" -H "$O" -d '{"currentPassword":"new-horse-battery-2","newPassword":"third-horse-battery-3","revokeOtherSessions":true}'
curl -s -b $J/a.jar $W/api/auth/get-session | jq -r '.user.email // "null"'
curl -s -b $J/b.jar $W/api/auth/get-session | jq -r '.user.email // "null"'
```

Expected: `200`; `ada@example.test`; `null`.

- [ ] **Step 9: Invitation with no connected mailbox → SMTP with Reply-To (spec runbook 6, first half):**

```bash
. /tmp/calendium-email-runbook/vars.sh
JWT=$(curl -s -b $J/a.jar $W/api/auth/token | jq -r .token)
curl -s -X POST $A/v1/teams -H "Authorization: Bearer $JWT" -H "$H" -d '{"name":"Runbook"}' | jq -r .id > $J/team.id
curl -s -X POST $A/v1/teams/$(cat $J/team.id)/invitations -H "Authorization: Bearer $JWT" -H "$H" -d '{"email":"grace@example.test","role":"member"}' | jq -c '{delivery, inviteUrl}'
sleep 2; last_mail grace@example.test | jq -c '{from: .From.Address, replyTo: [.ReplyTo[].Address], subject: .Subject}'
last_mail grace@example.test | jq -r .Text | grep -oE 'http://localhost:13000/invite/[^[:space:]]+'
```

Expected: `{"delivery":"smtp","inviteUrl":null}`; `{"from":"no-reply@calendium.test","replyTo":["ada@example.test"],"subject":"Ada invited you to Runbook on Calendium"}`; one `http://localhost:13000/invite/<token>` link.

- [ ] **Step 10: Sign-in rate limit, Postgres-backed, forged headers ignored (spec runbook 4 + Review Focus):**

```bash
. /tmp/calendium-email-runbook/vars.sh
sleep 61   # let the sign-in window from Steps 6–8 expire
for i in 1 2 3 4 5 6; do curl -s -o /dev/null -w '%{http_code} ' -X POST $W/api/auth/sign-in/email -H "$H" -H "$O" -d '{"email":"ada@example.test","password":"wrong-password-0"}'; done; echo
curl -s -o /dev/null -D - -X POST $W/api/auth/sign-in/email -H "$H" -H "$O" -d '{"email":"ada@example.test","password":"wrong-password-0"}' | grep -iE '^HTTP|^x-retry-after'
curl -s -o /dev/null -w 'forged client-ip header: %{http_code}\n' -X POST $W/api/auth/sign-in/email -H "$H" -H "$O" -H 'x-calendium-client-ip: 198.51.100.7' -d '{"email":"ada@example.test","password":"wrong-password-0"}'
curl -s -o /dev/null -w 'multi-hop XFF: %{http_code}\n' -X POST $W/api/auth/sign-in/email -H "$H" -H "$O" -H 'X-Forwarded-For:  203.0.113.9 , 10.0.0.1,' -d '{"email":"ada@example.test","password":"wrong-password-0"}'
docker exec calendium-rb-pg psql -U calendium -d calendium -tAc "SELECT key, count FROM \"rateLimit\" WHERE key LIKE '%/sign-in/email' ORDER BY \"lastRequest\" DESC"
```

Expected: `401 401 401 401 401 429`; `HTTP/1.1 429` with `X-Retry-After: <1–60>`; `forged client-ip header: 429` (route.ts overwrote the header, same bucket); `multi-hop XFF: 401` (with `TRUST_PROXY` unset a multi-hop header is never trusted, so the request lands in the separate empty-IP bucket — not the exhausted one); a row keyed on the loopback address (`127.0.0.1` or `::1`) plus `/sign-in/email` with a count ≥ 6 — the counters live in Postgres, so they would survive a `web` restart.

- [ ] **Step 11: SMTP outage — sign-up fails cleanly, invitation is revoked (spec Error handling):**

```bash
. /tmp/calendium-email-runbook/vars.sh
docker stop calendium-mailpit
curl -s -o /dev/null -w 'sign-up with SMTP down: %{http_code}\n' -X POST $W/api/auth/sign-up/email -H "$H" -H "$O" -d '{"name":"Carol","email":"carol@example.test","password":"correct-horse-battery","callbackURL":"/verify-email"}'
grep 'email.send_failed' $J/web.log | tail -1
docker exec calendium-rb-pg psql -U calendium -d calendium -tAc "SELECT \"emailVerified\" FROM \"user\" WHERE email = 'carol@example.test'"
JWT=$(curl -s -b $J/a.jar $W/api/auth/token | jq -r .token)
curl -s -w ' %{http_code}\n' -X POST $A/v1/teams/$(cat $J/team.id)/invitations -H "Authorization: Bearer $JWT" -H "$H" -d '{"email":"dan@example.test","role":"member"}'
curl -s $A/v1/teams/$(cat $J/team.id)/invitations -H "Authorization: Bearer $JWT" | jq -c '[.[] | select(.email == "dan@example.test") | .status]'
```

Expected: `sign-up with SMTP down: 500`; a log line containing `email.send_failed` and `example.test` but neither the subject nor the link; `f` (the row stays unverified; a later sign-in re-sends); a body containing `sending invitation email:` and `500`; `[]` or `["revoked"]` — never `pending`.

- [ ] **Step 12: Self-host without SMTP — link fallback, auto sign-in, operator reset (spec runbook 6, second half):**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && . /tmp/calendium-email-runbook/vars.sh
kill $(cat $J/api.pid); pkill -f 'next start -p 13000'; sleep 3
grep -v '^SMTP_' $J/rb.env > $J/rb-nosmtp.env && printf 'SMTP_HOST=\nSMTP_PORT=\nSMTP_USER=\nSMTP_PASS=\nSMTP_FROM=\nSMTP_SECURE=\n' >> $J/rb-nosmtp.env
set -a && . $J/rb-nosmtp.env && set +a
(cd apps/web && nohup bunx next start -p 13000 > $J/web.log 2>&1 &)
for i in $(seq 1 60); do curl -sf -o /dev/null $W/signin && echo web-up && break; sleep 2; done
nohup $J/api > $J/api.log 2>&1 & echo $! > $J/api.pid
for i in $(seq 1 60); do curl -sf -o /dev/null $A/healthz && echo api-up && break; sleep 1; done
curl -s $A/v1/instance | jq '.features.email'
grep -h 'email: disabled' $J/api.log $J/web.log
curl -s -X POST $W/api/auth/sign-up/email -H "$H" -H "$O" -d '{"name":"Erin","email":"erin@example.test","password":"correct-horse-battery"}' | jq -r '.token | type'
curl -s -w ' %{http_code}\n' -X POST $W/api/auth/sign-up/email -H "$H" -H "$O" -d '{"name":"Erin","email":"erin@example.test","password":"correct-horse-battery"}'
JWT=$(curl -s -b $J/a.jar $W/api/auth/token | jq -r .token)
curl -s -X POST $A/v1/teams/$(cat $J/team.id)/invitations -H "Authorization: Bearer $JWT" -H "$H" -d '{"email":"frank@example.test","role":"member"}' | jq -c '{delivery, inviteUrl}'
sleep 61   # fresh sign-in window for the temporary password below
node apps/web/scripts/reset-password.mjs ada@example.test | tee $J/reset.out
sed -n 's/^Temporary password for ada@example.test: //p' $J/reset.out > $J/ada.pw
curl -s -b $J/a.jar $W/api/auth/get-session; echo
curl -s -o /dev/null -w 'sign-in with temporary password: %{http_code}\n' -X POST $W/api/auth/sign-in/email -H "$H" -H "$O" -d "{\"email\":\"ada@example.test\",\"password\":\"$(cat $J/ada.pw)\"}"
```

Expected: `web-up`, `api-up`; `false`; two lines `email: disabled (no SMTP_HOST); verification off, invitations fall back to links` (api and web); `string` (signed in immediately, no verification); a body with `"code":"USER_ALREADY_EXISTS_USE_ANOTHER_EMAIL"` and `422` (the documented no-SMTP trade-off); `{"delivery":"link","inviteUrl":"http://localhost:13000/invite/<token>"}`; `Temporary password for ada@example.test: <16 chars>` plus the sessions-signed-out line; `null` (every session deleted); `sign-in with temporary password: 200`.

- [ ] **Step 13: Origins in production (spec runbook 8, API half):**

```bash
. /tmp/calendium-email-runbook/vars.sh
curl -s -w ' %{http_code}\n' -X POST $W/api/auth/sign-in/email -H "$H" -H 'Origin: http://localhost:5173' -d '{"email":"ada@example.test","password":"wrong-password-0"}'
curl -s -w ' %{http_code}\n' -X POST $W/api/auth/sign-in/email -H "$H" -H 'Origin: wails://wails.localhost' -d '{"email":"ada@example.test","password":"wrong-password-0"}'
curl -s -o /dev/null -D - -H 'Origin: http://localhost:5173' $A/v1/instance | grep -i '^access-control-allow-origin' || echo 'no ACAO for localhost'
curl -s -o /dev/null -D - -H 'Origin: wails://wails.localhost' $A/v1/instance | grep -i '^access-control-allow-origin'
```

Expected: a body with `"code":"INVALID_ORIGIN"` / `Invalid origin` and `403` (a dev origin other than `BETTER_AUTH_URL` under `NODE_ENV=production` without `ALLOW_DEV_ORIGINS`); a body with `INVALID_EMAIL_OR_PASSWORD` and `401` (the Wails origin passes the origin check); `no ACAO for localhost`; `Access-Control-Allow-Origin: wails://wails.localhost`.

- [ ] **Step 14: Cloud mode without SMTP refuses to start; partial SMTP is named (spec runbook 7):**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && . /tmp/calendium-email-runbook/vars.sh
for bin in api worker; do
  ( set -a; . $J/rb-nosmtp.env; set +a
    export SELF_HOSTED=false PADDLE_API_KEY=runbook PADDLE_WEBHOOK_SECRET=runbook PADDLE_PRICE_ID_ANNUAL=pri_runbook HTTP_ADDR=127.0.0.1:18081
    perl -e 'alarm shift; exec @ARGV' 20 $J/$bin > $J/cloud-$bin.log 2>&1; echo "$bin exit=$?" )
done
( set -a; . $J/rb-nosmtp.env; set +a; export SELF_HOSTED=false
  cd apps/web && perl -e 'alarm shift; exec @ARGV' 90 bunx next start -p 13001 > $J/cloud-web.log 2>&1; echo "web exit=$?" )
grep -c 'SMTP_HOST and SMTP_FROM are required when SELF_HOSTED=false (cloud mode); set them or run with SELF_HOSTED=true' $J/cloud-api.log $J/cloud-worker.log $J/cloud-web.log
( set -a; . $J/rb-nosmtp.env; set +a; export SMTP_HOST=127.0.0.1 HTTP_ADDR=127.0.0.1:18081
  perl -e 'alarm shift; exec @ARGV' 20 $J/api > $J/partial-api.log 2>&1; echo "partial exit=$?" ); grep -o 'SMTP_\* is partially configured: SMTP_FROM' $J/partial-api.log
```

Expected: `api exit=1`, `worker exit=1`, `web exit=<non-zero, not 142>` (142 means the alarm fired because the process kept running — FAIL); each log counts `1`; `partial exit=1` and `SMTP_* is partially configured: SMTP_FROM`. The dummy `PADDLE_*` values only get past piece 1's earlier cloud check; if a log instead names another cloud-only variable from a piece merged after this plan, export a dummy value for it and re-run — the step passes when the SMTP message is what stops the process.

- [ ] **Step 15: Operator-input checks (each independently BLOCKED, not failed, when its input is absent).**

(a) **Real SMTP provider.** Needs `RUNBOOK_SMTP_HOST`, `RUNBOOK_SMTP_PORT`, `RUNBOOK_SMTP_USER`, `RUNBOOK_SMTP_PASS`, `RUNBOOK_SMTP_FROM`, `RUNBOOK_SMTP_SECURE` and `RUNBOOK_INBOX` (a mailbox a human can open) exported in the shell by the operator — never read from `.env`. Any missing → **BLOCKED: operator SMTP credentials and a real inbox required**. Otherwise restart the api and web exactly as in Step 12 but with `SMTP_HOST=$RUNBOOK_SMTP_HOST` … `SMTP_SECURE=$RUNBOOK_SMTP_SECURE` exported after sourcing `rb-nosmtp.env`; confirm `api: email enabled` in `api.log`; sign up `$RUNBOOK_INBOX` as in Step 4. Expected: `{"token":null,…}`; the human sees the mail in the inbox (not spam) with `Authentication-Results` showing `spf=pass` and `dkim=pass` for the `SMTP_FROM` domain, and the link lands on `http://localhost:13000/verify-email` showing **Email verified**. A non-loopback host without STARTTLS must instead log `email.send_failed` with the STARTTLS refusal — record that as a provider-configuration finding, not a code failure.

(b) **Packaged desktop app signs in with dev origins off.** Needs a packaged build (`apps/desktop/build/bin/`) and a human at the machine; absent → **BLOCKED: packaged desktop build and an operator required**. Otherwise connect the app to `http://localhost:18080` and sign in as `ada@example.test` with the password in `/tmp/calendium-email-runbook/ada.pw`. Expected: signed in (the Wails origin is trusted in production), and **Forgot password?** opens `http://localhost:13000/forgot-password` in the system browser.

(c) **Mailbox-preferred invitation.** Needs Google or Microsoft OAuth credentials and a connected mailbox for the inviter; absent → **BLOCKED: a connected Gmail/Outlook mailbox is required** (the preference order is covered by Task 4's `service/team_test.go`). Otherwise invite a new address and expect `{"delivery":"mailbox"}` with the mail sent from the inviter's own mailbox.

(d) **Browser spot-checks** (a human or browser automation, in a **private window** — cookies on `localhost` are shared across ports, so a normal window would clobber the live stack's session on `:3000`); absent → **BLOCKED: no browser session available**. Expected: `http://localhost:13000/forgot-password` shows **Password reset by email isn't available on this server** (the stack now runs without SMTP); `http://localhost:13000/verify-email?error=INVALID_TOKEN` shows **This link has expired or was already used**; signed in as Ada, `http://localhost:13000/settings?tab=account` shows the change-password form.

- [ ] **Step 16: Report, then tear down.** Report every expected/actual pair from Steps 2–15, each BLOCKED reason verbatim, and the known gap from the plan header: no client has a team-invitation UI, so the `delivery`/`inviteUrl` contract was verified through the API only (Steps 9, 11 and 12). Then:

```bash
. /tmp/calendium-email-runbook/vars.sh
kill $(cat $J/api.pid) 2>/dev/null; pkill -f 'next start -p 13000'; pkill -f 'next start -p 13001'
docker rm -f calendium-rb-pg calendium-mailpit
rm -rf /tmp/calendium-email-runbook
```

Expected: both containers removed; nothing listens on 13000/18080 any more; the live stack on 3000/8080 (if any) was never touched.
