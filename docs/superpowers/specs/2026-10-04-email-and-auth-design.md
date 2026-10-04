# Email and auth hardening — design

Program context: piece 2 of 6 in the production-readiness program (billing,
**email + auth**, account lifecycle, platform hardening, store readiness,
go-live). Depends on piece 1 for `InstanceInfo.webUrl`; piece 4 reuses the
`TRUST_PROXY` variable introduced here for the Go api.

## Goal

Make the identity layer launch-ready: transactional email over SMTP, verified
addresses, self-service password reset and change, enumeration-safe sign-up,
durable per-IP auth rate limits, JWT reuse on API calls, and a trusted-origin
list that excludes dev origins in production — while self-host without SMTP
keeps working and demo mode / the e2e suite stay green.

## Context & findings being fixed

| # | Finding | Where |
| --- | --- | --- |
| 1 | No email sending anywhere: `emailAndPassword: { enabled: true }` is the whole config (no `emailVerification`, no `sendResetPassword`); the backend has no mail port. | `apps/web/lib/auth.ts:123`, `backend/internal/service/team.go:40-41` |
| 2 | No password reset and no change-password UI; no "Forgot password?" link; Settings tabs stop at Billing. | `apps/web/app/signin/page.tsx:229-277`, `apps/web/app/(app)/settings/settings-page.tsx:227-245` |
| 3 | No email verification: anyone can register someone else's address, and Better Auth then refuses to link Google/Apple to that unverified local account. | `apps/web/lib/auth.ts:123` |
| 4 | Sign-up reveals existence: Better Auth 1.6.23 returns its generic duplicate response only when `requireEmailVerification` is on (`sign-up.mjs:160-206`), so the toast shows "User already exists". | `apps/web/app/signin/page.tsx:128-129` |
| 5 | Team invitations fail unless the inviter has a connected mailbox. | `backend/internal/service/team.go:313-316`, `:483-497` |
| 6 | Rate limiting is Better Auth defaults: production-only, in-memory (per replica, lost on restart), and `x-forwarded-for` trusted only when single-valued (`@better-auth/core/dist/utils/ip.mjs:167-171`). | `apps/web/lib/auth.ts:115-146` |
| 7 | localhost/127.0.0.1 origins trusted/reflected unconditionally, in production too (the Wails WebView origins are legitimate production client origins and stay); `https://appleid.apple.com` missing, so Apple's `form_post` callback fails the origin check. | `apps/web/lib/auth.ts:73-92`, `:102-113`, `backend/internal/adapter/in/httpapi/middleware.go:143-160` |
| 8 | Every API request mints a JWT at `/api/auth/token`, colliding with per-IP auth limits. | `packages/shared/src/client.ts:111-112`, `apps/web/lib/auth-client.ts:29-42`, `apps/desktop/frontend/src/lib/auth.ts:215-238`, `apps/mobile/lib/auth-client.ts:62-82` |

Better Auth 1.6.23 facts relied on (verified in `node_modules/better-auth/dist`):
`storage: "database"` needs a `rateLimit` table (`key` unique, `count`,
`lastRequest` bigint) and uses atomic `incrementOne` (replica-safe);
`customRules` match the exact path after `/api/auth` and apply to GET (so
`/token` is limitable); `/send-verification-email` and
`/request-password-reset` are generic and constant-time for unknown addresses;
`/change-password` already has a 3/10 s built-in rule. Next.js fills
`x-forwarded-for` from the socket when absent (`next/dist/server/base-server.js:568`).

## Decisions (fixed)

1. **SMTP, provider-agnostic.** Web sends Better Auth mail with `nodemailer`
   (new dependency) from `SMTP_HOST`, `SMTP_PORT`, `SMTP_USER`, `SMTP_PASS`,
   `SMTP_FROM`, `SMTP_SECURE` (`true` = implicit TLS, `false` = STARTTLS). Go gets
   a `Mailer` port + `internal/adapter/out/smtp` adapter (stdlib only: `net/smtp`,
   `crypto/tls`, `mime/*`, `net/mail`) on the same variables, used for team
   invitations when the inviter has no connected mailbox (mailbox preferred).
   Cloud (`SELF_HOSTED=false`) requires SMTP at startup: api, worker and web fail
   fast with a clear error. Self-host: SMTP optional; unset ⇒ verification off,
   forgot-password explains the admin resets passwords (CLI documented),
   invitations fall back to a copyable link shown to the inviter.
2. **Email verification required** for email/password sign-up when SMTP is
   configured (`emailVerification` + `requireEmailVerification: true`, auto
   sign-in after verification, resend with cooldown). Social sign-ups are
   provider-verified. Sign-up responses never reveal whether an email exists.
3. **Password reset** at `/forgot-password` and `/reset-password?token=` in web;
   mobile and desktop link to `<webUrl>/forgot-password`. Token lifetime 1 hour.
4. **Change password** in Settings → Account (Better Auth `changePassword`,
   current password required, other sessions revoked). Policy: 10–128 chars,
   reject passwords containing the email local part. No 2FA.
5. **Auth rate limiting**: Better Auth `rateLimit` on in all environments,
   `storage: "database"`; `sign-in/email` 5/60 s, `sign-up/email` 3/60 s,
   forget-password 3/600 s, `send-verification-email` 3/600 s, token endpoint
   60/60 s, per IP; IP = first `X-Forwarded-For` hop only when `TRUST_PROXY=true`,
   else the socket address.
6. **JWT caching** in the shared `ApiClient`: reuse until 60 s before `exp`
   (decoded, not verified), refresh once on 401; same in web `getAccessToken`.
7. **Origins**: the four Wails WebView origins (`wails://wails`,
   `wails://wails.localhost`, `http://wails.localhost`, `https://wails.localhost`)
   are production origins and are always trusted — in Better Auth
   `trustedOrigins`, the Better Auth CORS reflection list and the Go CORS
   middleware — exactly like `calendium://` and `https://appleid.apple.com`.
   Only `http://localhost:*` and `http://127.0.0.1:*` are gated on
   `NODE_ENV !== 'production' || ALLOW_DEV_ORIGINS === 'true'`; production
   additionally trusts `BETTER_AUTH_URL`, `PUBLIC_WEB_URL` and
   `CORS_ALLOWED_ORIGINS`. Mirrored in the Go CORS middleware.
8. **Templates**: branded HTML + text in `apps/web/lib/email/` (`verify-email`,
   `reset-password`, `team-invitation`, `delete-account-confirm` reserved for
   piece 3); Go keeps a minimal invitation template. Links come from
   `PUBLIC_WEB_URL`/`BETTER_AUTH_URL`, never request headers.
9. Demo mode and the e2e suite (`apps/web/e2e/fixtures.ts` stubs
   `/api/auth/get-session` and `/api/auth/token`) keep working.

## Non-goals

2FA/passkeys, email change, magic links, admin UI, a notice email on duplicate
sign-up, rate limiting of Go API routes (piece 4), grandfathering existing
unverified users (they verify on next sign-in).

## Architecture

### Components

- `apps/web/lib/email/config.ts` — `readMailConfig(env)` →
  `{ configured: false } | { configured: true, host, port, secure, user?, pass?,
  from, requireTLS }` with `requireTLS = !secure && !isLoopback(host)`;
  `assertMailConfigForMode(env)` throws in cloud mode without SMTP.
- `apps/web/lib/email/transport.ts` — lazy nodemailer transport (`host, port,
  secure, requireTLS, auth`), `sendMail({ to, subject, html, text, replyTo? })`,
  `isMailConfigured()`. Never constructed at build time.
- `apps/web/lib/email/render.ts` + `templates/*.ts` — `renderEmail({ heading,
  intro, cta, footer })` → `{ subject, html, text }`; every interpolation goes
  through `escapeHtml`. `team-invitation.ts` is the reference wording the Go
  template mirrors; unit-tested, not wired at runtime in piece 2.
- `apps/web/instrumentation.ts` — `register()` (Node runtime) runs
  `assertMailConfigForMode` + env parsing and logs the production warnings; a
  throw aborts `next start`/`next dev`; `next build` is unaffected.
- `apps/web/lib/auth-env.ts` — pure, DB-free builders: `buildTrustedOrigins(env)`,
  `isAllowedOrigin(origin, env)`, `rateLimitRules()`, `clientIpFor(headers,
  trustProxy)`, `passwordPolicyError(password, email)`.
- `apps/web/lib/auth.ts` wires them into `betterAuth`; `app/api/auth/[...all]/route.ts`
  sets the client-IP header before delegating.
- Web pages: `app/forgot-password/page.tsx`, `app/reset-password/page.tsx`,
  `app/verify-email/page.tsx`; sign-in gains a `check-email` state and the
  "Forgot password?" link; `settings-account.tsx` + `account` tab;
  `apps/web/scripts/reset-password.mjs` operator CLI. Go and shared pieces are
  listed under API / contract changes.

### Sign-up + verify (SMTP configured)

1. Client calls `signUp.email({ name, email, password, callbackURL: '/verify-email' })`
   (web; desktop `signUpEmail`; mobile `signUpWithEmail`).
2. Better Auth checks length 10–128; the `before` hook rejects a password
   containing the email local part (`PASSWORD_CONTAINS_EMAIL`, 400).
3. New address: user created `emailVerified=false`, token issued (`expiresIn`
   86 400 s), `sendVerificationEmail` renders `verify-email` and awaits
   `sendMail`; response `{ token: null, user }`. Existing address: Better Auth
   hashes the password for timing and returns the same shape with a synthetic
   user; nothing is sent. The two are indistinguishable.
4. Web shows "Check your inbox" + Resend (60 s client cooldown,
   `sendVerificationEmail({ email, callbackURL: '/verify-email' })`). Desktop and
   mobile detect `token === null`, switch to sign-in mode, show the same notice.
5. Link `${BETTER_AUTH_URL}/api/auth/verify-email?token=…&callbackURL=/verify-email`
   (built from `baseURL`, i.e. env). Better Auth verifies, sets the session
   cookie (`autoSignInAfterVerification`), 302 → `/verify-email`, which shows
   "Verified" + Continue (`/mail`) with a session, "Verified, sign in to
   continue" without one (other browser / native sign-up), or "Link expired" +
   resend form on `?error=`.
7. Sign-in while unverified → 403 `EMAIL_NOT_VERIFIED`; `sendOnSignIn: true`
   re-sends; clients show "Verify your email first — we sent a new link". Social
   linking now succeeds because local accounts are verified.

Without SMTP: `requireEmailVerification=false`; sign-up auto-signs-in as today
and Better Auth's duplicate error is surfaced (see Security).

### Forgot / reset

`/forgot-password` → `requestPasswordReset({ email, redirectTo: '/reset-password' })`
→ always "If an account exists for that address, we sent a link" →
`sendResetPassword` renders `reset-password` with Better Auth's `url`
(`${BETTER_AUTH_URL}/api/auth/reset-password/<token>?callbackURL=/reset-password`,
`resetPasswordTokenExpiresIn: 3600`) → click → Better Auth validates, 302 →
`/reset-password?token=<token>` or `?error=INVALID_TOKEN` → page calls
`resetPassword({ newPassword, token })` (policy hook resolves the email from the
`reset-password:<token>` verification row) → all sessions revoked
(`revokeSessionsOnPasswordReset: true`) → toast → `/signin`. When
`instance.features.email === false` the page renders the admin instructions
instead of the form. Native "Forgot password?" opens `${webUrl}/forgot-password`
in the system browser (fallback: origin of `authBaseUrl`).

### Change password

Settings → Account: `listAccounts()` decides; a `credential` account shows
current/new/confirm → `changePassword({ currentPassword, newPassword,
revokeOtherSessions: true })` (policy hook uses the session email); social-only
users see "You sign in with Google/Apple". Success: "Password updated. Other
devices were signed out."

### Team invitation

`TeamService.Invite` picks, in order: (a) inviter's first active connected
account with a mail provider — unchanged, From = inviter; (b) the `Mailer` when
wired — From = `SMTP_FROM`, `Reply-To` = inviter, Go `text/template` +
`html/template` with the web template's wording; (c) neither — the invitation
is created and the response carries `delivery: "link"` and
`inviteUrl: <AppBaseURL>/invite/<token>`; the web invite form shows a copyable
link ("Email isn't configured on this server — share this link; it expires in
14 days"). Send failures in (a)/(b) revoke the row and return the error, as
today. Cloud always has (b), so (c) never occurs there.

### Rate limiting

`route.ts` clones the `Request` with `x-calendium-client-ip` always overwritten:
`TRUST_PROXY=true` → first `x-forwarded-for` hop; else the header only when
single-valued (Next.js's socket fill), else empty → Better Auth's shared
`no-trusted-ip` bucket. Better Auth reads only that header
(`advanced.ipAddress.ipAddressHeaders`). Keys are `ip + path`, rows live in
`rateLimit` and are pruned by Better Auth. Over limit → 429 + `X-Retry-After`;
pages show "Too many attempts, try again in N s".

### JWT caching

`createAccessTokenCache(mint)` holds `{ token, expMs }`; `get()` returns it
while `now < expMs − 60 000`, else mints (one in-flight promise shared by
concurrent callers). `null` and tokens without a decodable `exp` are never
cached. `ApiClient.request`: 401 with a token attached and no prior retry →
`invalidate()`, re-mint, retry once. The cache lives in `opts`, so `actAs()`
(`client.ts:790-811`, which spreads opts) shares it; sign-out calls
`invalidate()`. The e2e `/token` stub returns 401 → `null` → not cached →
behaviour unchanged.

### Origins

`devOriginsAllowed = NODE_ENV !== 'production' || ALLOW_DEV_ORIGINS === 'true'`;
`trustedOrigins = ['calendium://', 'https://appleid.apple.com', ...WAILS_ORIGINS,
BETTER_AUTH_URL, PUBLIC_WEB_URL?, ...CORS_ALLOWED_ORIGINS,
...(devOriginsAllowed ? ['http://localhost:*', 'http://127.0.0.1:*'] : [])]`.
`isAllowedOrigin` (CORS reflection) always matches the Wails origins and the
explicit list and gates only its localhost/127.0.0.1/::1 matcher; appleid is
trusted but never reflected (a form POST is a navigation). Go:
`corsMiddleware(next, allowed, allowDevOrigins)` always reflects `isWailsOrigin`
and the explicit origins (`CORS_ALLOWED_ORIGINS` + `PUBLIC_WEB_URL` +
`BETTER_AUTH_URL`), and `isLocalDevOrigin` only when `ALLOW_DEV_ORIGINS=true` (Go
has no build mode; `backend/.env.example`, the run-the-binary template, sets it
true). The packaged desktop app therefore needs no operator configuration.

## Configuration

| Variable | Read by | Default | Validation |
| --- | --- | --- | --- |
| `SMTP_HOST` | web, api, worker | — | Required with `SMTP_FROM` when any `SMTP_*` is set, and always in cloud mode. |
| `SMTP_PORT` | all | `587` | Integer 1–65535. |
| `SMTP_USER` / `SMTP_PASS` | all | — | Optional pair; one without the other errors. |
| `SMTP_FROM` | all | — | `addr` or `Name <addr>`; parsed by `net/mail` / nodemailer. |
| `SMTP_SECURE` | all | `false` | `true`/`false`; `false` ⇒ STARTTLS required unless host is loopback. |
| `SELF_HOSTED` | web (new), api, worker | `false` | Compose already passes `.env` to `web`. |
| `TRUST_PROXY` | web (api in piece 4) | `false` | Production + `false` logs a startup warning. |
| `ALLOW_DEV_ORIGINS` | web, api | `false` | Gates only localhost/127.0.0.1 origins (Wails origins are always allowed); production + `true` logs a warning. |
| `PUBLIC_WEB_URL` | web (new) | — | Added to trusted origins when set. |

Startup: `config.FromEnv` (api, worker) and `instrumentation.ts`/`lib/auth.ts`
(web) fail with `SMTP_HOST and SMTP_FROM are required when SELF_HOSTED=false
(cloud mode); set them or run with SELF_HOSTED=true`, or `SMTP_* is partially
configured: <missing>`. Self-host without SMTP boots and logs `email: disabled
(no SMTP_HOST); verification off, invitations fall back to links`.
`playwright.config.ts` adds `SELF_HOSTED: 'true'` to the webServer env.
`GET /v1/instance` gains `features.email` (`cfg.SMTP.Configured()`); `webUrl`
(piece 1) feeds the native "Forgot password?" links.

## API / contract changes

Better Auth options (`apps/web/lib/auth.ts`), with `mail = readMailConfig(env)`:

```ts
emailAndPassword: {
  enabled: true, minPasswordLength: 10, maxPasswordLength: 128,
  requireEmailVerification: mail.configured,
  resetPasswordTokenExpiresIn: 3600, revokeSessionsOnPasswordReset: true,
  sendResetPassword: mail.configured ? ({ user, url }) => sendMail(resetPasswordEmail({ to: user.email, url })) : undefined,
},
emailVerification: mail.configured ? {
  sendOnSignUp: true, sendOnSignIn: true, autoSignInAfterVerification: true, expiresIn: 86_400,
  sendVerificationEmail: ({ user, url }) => sendMail(verifyEmail({ to: user.email, url })),
} : undefined,
rateLimit: { enabled: true, storage: 'database', window: 60, max: 100,
  customRules: { '/sign-in/email': { window: 60, max: 5 }, '/sign-up/email': { window: 60, max: 3 },
    '/request-password-reset': { window: 600, max: 3 }, '/forget-password': { window: 600, max: 3 },
    '/send-verification-email': { window: 600, max: 3 }, '/token': { window: 60, max: 60 } } },
advanced: { ipAddress: { ipAddressHeaders: ['x-calendium-client-ip'] } },
hooks: { before: passwordPolicyHook }, // /sign-up/email, /change-password, /reset-password
trustedOrigins: buildTrustedOrigins(process.env),
```

`/forget-password` is the deprecated alias of `/request-password-reset`; both
get the rule. `/token` is the jwt plugin's GET. New web routes:
`/forgot-password`, `/reset-password` (`?token=`|`?error=`), `/verify-email`
(`?error=`), Settings `?tab=account`. New deps: `nodemailer`, `@types/nodemailer`.

```sql
-- backend/migrations/0027_better_auth_rate_limit.sql
CREATE TABLE IF NOT EXISTS "rateLimit" (
    "id"          text    NOT NULL PRIMARY KEY,
    "key"         text    NOT NULL UNIQUE,
    "count"       integer NOT NULL,
    "lastRequest" bigint  NOT NULL
);
CREATE INDEX IF NOT EXISTS "rateLimit_lastRequest_idx" ON "rateLimit" ("lastRequest");
```

Go port (`backend/internal/port/driven.go`):

```go
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

`config.SMTP{Host string; Port int; User, Pass, From string; Secure bool}` +
`Configured() bool`. `smtp.New(cfg config.SMTP) *Client` with seams
`DialContext func(ctx, network, addr string) (net.Conn, error)` and
`TLSConfig *tls.Config` (ServerName = host). `Send`: 15 s deadline; `Secure` →
`tls.Client` + `smtp.NewClient`, else `smtp.NewClient` + `StartTLS` when
advertised, failing with `smtp: server does not offer STARTTLS; refusing to send
credentials in clear` for non-loopback hosts; AUTH PLAIN when offered, else a
stdlib-compatible `LOGIN` `smtp.Auth`, none when `User` is empty; RFC 5322
headers (`From`, `To`, `Reply-To`, `Subject` via `mime.QEncoding`, `Date`,
`Message-ID <rand@fromdomain>`, `MIME-Version`), `text/plain` or
`multipart/alternative` with quoted-printable parts; header values with CR/LF
are rejected. `TeamServiceDeps.Mailer port.Mailer` (nil when unset).
`domain.TeamInvitation` gains response-only `Delivery InvitationDelivery
`json:"delivery,omitempty"`` (`mailbox|smtp|link`) and `InviteURL string
`json:"inviteUrl,omitempty"``, mirrored in the TS `TeamInvitation`.
`httpapi.Deps.AllowDevOrigins bool`; `InstanceFeatures.Email bool `json:"email"``;
composition roots build `smtp.New(cfg.SMTP)` when configured.

Shared: `createAccessTokenCache(mint, now?)`, `decodeJwtExp(token)`,
`ApiClientOptions.accessTokens?` (internal), `ApiClient.invalidateAccessToken()`;
web exports `getAccessToken`/`invalidateAccessToken`; desktop and mobile keep
their minting functions and get caching via `ApiClient`. `InstanceFeatures.email`
is added to shared types and both native `DEFAULT_FEATURES`.

## Error handling

- SMTP failure in sign-up/reset: the callback awaits `sendMail`, logs
  `email.send_failed` (recipient domain + provider error, never the body) and
  rethrows → 500; pages show "We couldn't send the email. Try again in a
  minute." The user row stays unverified; a later sign-in re-sends.
- Go invitation send failure → row revoked, `sending invitation email: …` 500,
  logged at error; client toast asks to retry.
- 429 → `X-Retry-After` in page copy; `/token` 429 → `getAccessToken` returns
  `null` → unauthenticated request → 401 → one retry → `ApiRequestError(401)`.
- Invalid/expired tokens → Better Auth redirects with `?error=`; pages offer
  the recovery path. Reused reset token → `INVALID_TOKEN` toast.
- Bad startup config → non-zero exit naming the exact variable.

## Security considerations

- Enumeration: with SMTP, sign-up, resend and reset are generic and
  timing-equalised by Better Auth; sign-in reveals `EMAIL_NOT_VERIFIED` only
  after a correct password. Without SMTP (self-host) the duplicate sign-up
  error is visible — documented trade-off.
- Tokens are single-use, expiring (24 h / 1 h), in links built from
  `BETTER_AUTH_URL`; relative `callbackURL`s are origin-checked by Better Auth.
- Reset revokes all sessions; change-password revokes others; JWTs stay
  ≤15 min so a cached token never outlives its session by more.
- `x-calendium-client-ip` is server-set only. With `TRUST_PROXY=true` the proxy
  must *overwrite* `X-Forwarded-For` (Caddy does; nginx needs
  `proxy_set_header X-Forwarded-For $remote_addr`) or first-hop can be forged.
  With `TRUST_PROXY=false` the web tier cannot see the socket: a direct client
  that sets a single-valued `X-Forwarded-For` keys its own bucket, hence the
  production warning; the bundled Caddy profile + `TRUST_PROXY=true` is the
  supported production shape.
- SMTP credentials only over TLS (implicit or STARTTLS) except to loopback;
  `TLSConfig` verifies the server certificate; nodemailer uses `requireTLS`.
- User-controlled strings in mail are HTML-escaped (web `escapeHtml`, Go
  `html/template`); `Reply-To` validated with `net/mail`; no CR/LF in headers.
- Password policy enforced server-side (hook), mirrored client-side.

## Testing & verification

- **Go unit** — `adapter/out/smtp/smtp_test.go`: fake SMTP server on
  `net.Listen("tcp", "127.0.0.1:0")` scripting the dialogue: implicit TLS
  (`tls.Listen` with an in-test ECDSA cert, client `TLSConfig.RootCAs`), STARTTLS
  upgrade, plaintext AUTH to loopback, non-loopback without STARTTLS refused
  (`DialContext` seam + `Host: "mail.example.test"`), AUTH LOGIN fallback,
  multipart rendering (headers, QP bodies, encoded subject, Reply-To), context
  timeout. `service/team_test.go`: SMTP when no mailbox, mailbox preferred,
  link fallback populates `Delivery/InviteURL` (replaces
  `TestTeamInviteRequiresConnectedAccount`), SMTP failure revokes.
  `config_test.go`: cloud requires SMTP, partial SMTP, port/bool parsing,
  `ALLOW_DEV_ORIGINS`. `httpapi/middleware_test.go`: localhost gate, Wails
  origins always reflected, explicit origins. `instance_test.go`: `features.email`.
- **Better Auth config** (Vitest node, `lib/auth-env.test.ts`): trusted origins
  per env, `isAllowedOrigin` gating, rules table, `clientIpFor` both modes and
  multi-hop, password policy incl. local part, mail config per mode; template
  snapshots + escaping.
- **Vitest pages**: sign-in `check-email` + cooldown + 403 copy; forgot (form
  vs admin text); reset (token/error/mismatch); verify-email (three states);
  settings account (credential vs social). Shared: `access-token-cache.test.ts`
  (window, in-flight dedup, null not cached), `client.test.ts` 401 retry-once.
  Desktop/mobile: `token === null` sign-up path, forgot link URL.
- **Playwright (demo mode)**: `auth-recovery.spec.ts`, `settings-account.spec.ts`
  with route stubs for `request-password-reset`, `reset-password`,
  `list-accounts`, `change-password`; existing fixtures unchanged.
- **Manual runbook** — `reframe-mailpit-test` already occupies Mailpit's default
  ports, so: `docker run -d --name calendium-mailpit -p 18025:8025 -p 11025:1025
  axllent/mailpit`; `.env`: `SMTP_HOST=127.0.0.1` (compose:
  `host.docker.internal`), `SMTP_PORT=11025`, `SMTP_SECURE=false`, no user/pass.
  (1) sign up → mail at http://localhost:18025 → click → verified in `/mail`;
  (2) same address again → identical response, no mail; (3) forgot → reset →
  old session gone, reused link errors; (4) 6th sign-in in a minute → 429;
  (5) change password → other browser signed out; (6) invite with no mailbox →
  SMTP mail with Reply-To; then stop Mailpit, unset SMTP, `SELF_HOSTED=true` →
  copyable invite link, forgot shows admin text; (7) `SELF_HOSTED=false` without
  SMTP → api/worker/web refuse to start; (8) `NODE_ENV=production` without
  `ALLOW_DEV_ORIGINS` → a `localhost:3000` dev web gets 403 "Invalid origin"
  while the packaged desktop app still signs in.

## Operator setup

- `.env.example` / `backend/.env.example`: "Transactional email (SMTP)" block
  with the six variables and the cloud/self-host rule; `TRUST_PROXY`;
  `ALLOW_DEV_ORIGINS` (root blank, backend template `true`).
- `docs/self-hosting/configuration.md`: SMTP table, `TRUST_PROXY`,
  `ALLOW_DEV_ORIGINS`, web reads `SELF_HOSTED`/`PUBLIC_WEB_URL`, `features.email`,
  cloud startup requirements. `providers.md` §1d "Transactional email" (any
  SMTP provider, SPF/DKIM/DMARC, Mailpit); §1c Apple `form_post` note.
- `docs/self-hosting/security.md`: `TRUST_PROXY=true` behind the proxy, dev
  origins off, rate limits in Postgres, and the self-host reset procedure:
  `SELECT id FROM "user" WHERE email = $1`, then `docker compose exec web node
  apps/web/scripts/reset-password.mjs <email>` — hashes with `better-auth/crypto`
  `hashPassword`, upserts the `credential` row in `account`, deletes the user's
  `session` rows, prints a temporary password.
- `docs/self-hosting/reverse-proxy-tls.md`: proxies must overwrite
  `X-Forwarded-For`.
- `docs/self-hosting/upgrades.md`: adding SMTP turns verification on; existing
  users verify once on next sign-in. `troubleshooting.md`: SMTP errors, 429s,
  "Invalid origin". Go-live checklist §5: add "proxy overwrites XFF,
  `TRUST_PROXY=true`".
