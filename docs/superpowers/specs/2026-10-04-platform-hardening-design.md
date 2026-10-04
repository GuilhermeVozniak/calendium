# Platform hardening — design

Date: 2026-10-04. Status: draft for review.
Program context: piece 4 of 6 in the production-readiness program (billing,
email/auth, account lifecycle, **platform hardening**, store readiness,
external checklist). Independent of pieces 1–3 except for the Paddle overlay
CSP and the `billing_unavailable` code noted below.

## Goal

Make the API, worker, web app and the reference compose stack safe behind a
public reverse proxy: correct client-IP handling, per-user rate limits,
security headers and a CSP, bounded requests, graceful shutdown, hard config
validation, structured redacted logs, pinned health-checked images, and an
SSRF guard that also covers CGNAT/NAT64.

## Findings being fixed (verified in code)

1. Rate limiter keys on the socket peer: `clientIP` reads `r.RemoteAddr`
   (`backend/internal/adapter/in/httpapi/ratelimit.go:74-79`), so behind
   Caddy/nginx all callers share one bucket. Only the public/share routes are
   limited (`httpapi.go:118-130`); `authed(...)` wraps `requireAuth` +
   `withActAs` only (`httpapi.go:133-135`).
2. No security headers anywhere: `middleware.go:84-95`, `137-165`;
   `apps/web/next.config.ts` has no `headers()`; no `apps/web/middleware.ts`.
3. Shutdown not graceful: `srv.Shutdown` runs detached with a 10 s context
   (`backend/cmd/api/main.go:502-507`); `run()` returns when `ListenAndServe`
   returns (`main.go:510-513`), so the deferred `db.Close()` races in-flight
   handlers. Worker `wg.Wait()` is unbounded (`cmd/worker/main.go:354`).
4. Only `ReadHeaderTimeout` is set (`cmd/api/main.go:497-501`).
5. 10 MiB body cap on every JSON route (`codec.go:12-13`, `:33`); 16 KiB on
   public scheduling (`scheduling.go:16`), 1 MiB on the webhook
   (`billing.go:59`). Field sizes unbounded except team name
   (`domain/team.go:56`) and comment body (`domain/collab.go:44`).
6. Default DB passwords accepted: `config.go:279-281` checks only non-empty;
   `.env.example` ships `change-me-please`, compose defaults `calendium`.
7. `BETTER_AUTH_SECRET` passed through unchecked (`apps/web/lib/auth.ts:123`);
   no `instrumentation.ts`.
8. Logs record raw `r.URL.Path` (`middleware.go:91`, `:111`; `codec.go:131`,
   `:134`) — share/poll tokens and `/v1/mail/contacts/{email}` leak; no
   request id; text handler (`cmd/api/main.go:45`, `cmd/worker/main.go:64`).
9. `docker-compose.yml:51`, `:104` publish `${API_PORT}:8080` /
   `${WEB_PORT}:3000` on all interfaces.
10. `backend/Dockerfile:23` is `alpine:3.20` (EOL); `:10`,
    `apps/web/Dockerfile:11`, `:27`, `:46` float; web image has no
    `HEALTHCHECK`; `.github/workflows/test.yml` (and `release.yml`) never
    build an image.
11. `requestBaseURL` trusts `X-Forwarded-Proto/Host` from any peer
    (`httpapi/accounts.go:137-151`), used for the OAuth `redirect_uri` when
    `PUBLIC_API_URL` is unset (`accounts.go:35`, `:111`).
12. SSRF guards allow 100.64.0.0/10 and 64:ff9b::/96: `isPublicAddr` =
    `IsGlobalUnicast && !IsPrivate` (`icsfeed/fetcher.go:138-141`);
    `blockPrivateNetworks` checks loopback/private/link-local/unspecified
    (`unsubscribe/client.go:32-44`). The two packages do **not** share a
    helper (the brief assumed one) — this piece extracts it.
13. Platform-credential 401/403 → `domain.ErrUnauthorized`
    (`stripeapi/client.go:84-85`, `openrouter/client.go:200`) → 401
    `unauthorized` (`codec.go:63-64`). A cold JWKS cache plus unreachable web
    app also ends as 401 "invalid or expired access token"
    (`authjwt/verifier.go:269-278`, `middleware.go:33-43`).

## Decisions (fixed; designed around, not reopened)

1. **Client IP.** `TRUST_PROXY` (bool, default `false`) + `TRUSTED_PROXY_CIDRS`
   (default when trusting: `127.0.0.0/8,10.0.0.0/8,172.16.0.0/12,
   192.168.0.0/16,::1/128,fc00::/7`). Trusted peer → right-most
   `X-Forwarded-For` hop not in a trusted CIDR; else socket address. One
   `clientIP(r)` for rate limiting, logging, `requestBaseURL`;
   `X-Forwarded-Host/Proto` honoured only from trusted proxies. Compose sets
   `TRUST_PROXY=true` on `api`; loopback publish documented.
2. **Rate limiting.** In-memory token buckets (single replica, documented).
   Public: per-IP, existing limits. Authenticated: per-user 600/min;
   classes `mutate_heavy` 30/min, `search` 120/min. 429 + `Retry-After`.
   `RATE_LIMIT_*` env, sane defaults.
3. **Security headers.** API: `X-Content-Type-Options: nosniff`,
   `Cache-Control: no-store` on `/v1`, `Referrer-Policy: no-referrer`,
   `X-Frame-Options: DENY`, HSTS `max-age=31536000; includeSubDomains` only
   on TLS or `X-Forwarded-Proto: https` from a trusted proxy. Web:
   `headers()` in `next.config.ts` (HSTS, nosniff, `X-Frame-Options: DENY`,
   `Referrer-Policy: strict-origin-when-cross-origin`, `Permissions-Policy:
   camera=(), microphone=(), geolocation=()`) and a nonce CSP from
   `apps/web/middleware.ts`: `default-src 'self'; script-src 'self'
   'nonce-<n>' https://cdn.paddle.com; frame-src https://*.paddle.com
   https://accounts.google.com https://appleid.apple.com; connect-src 'self'
   <API origin> https://*.paddle.com; img-src 'self' data: https:; style-src
   'self' 'unsafe-inline'; font-src 'self' data:; frame-ancestors 'none';
   base-uri 'self'; form-action 'self' https://appleid.apple.com`.
   Report-Only + report endpoint first; enforce in the next task after e2e
   passes enforced. `/checkout*` (piece 1) stays compatible.
4. **HTTP server.** `ReadHeaderTimeout` 10 s, `ReadTimeout` 60 s,
   `IdleTimeout` 120 s, `MaxHeaderBytes` 64 KiB, no `WriteTimeout`; 60 s
   per-handler context deadline on non-stream routes. SIGTERM/SIGINT →
   `Shutdown` (30 s), wait, close DB; worker loops cancel and wait ≤ 30 s.
   `/healthz` static liveness; `/readyz` pings DB (2 s), 503 while draining;
   compose checks `api` on `/readyz`.
5. **Config validation.** Cloud refuses DB passwords `change-me-please` /
   `calendium` and empty `PUBLIC_WEB_URL`; self-host warns loudly. Web refuses
   `BETTER_AUTH_SECRET` < 32 bytes in all modes. Secrets never logged.
6. **Request limits.** JSON 1 MiB default; 10 MiB draft create/update; 16 KiB
   public; 1 MiB webhook. Handler-layer field limits: titles/names/subjects
   500 chars, notes/descriptions/bodies 64 KiB, emails 320, URLs 2048, arrays
   ≤ 500 → 400 `validation_failed` naming the field.
7. **Logging.** slog JSON; `request_id` (ULID-style, crypto/rand, echoed as
   `X-Request-Id`, accepted from trusted proxies), `user_id`, method, route
   pattern, status, duration; paths redacted for `/v1/shared/threads/{token}`,
   `/v1/public/polls/{token}`, `/v1/mail/contacts/{email}`,
   `/v1/invitations/accept`. Upstream provider auth failures → 502
   `upstream_unavailable`, provider named only in logs.
8. **Containers/CI.** `alpine:3.22` + `golang:1.26-alpine` and `oven/bun` +
   `node:22-alpine` pinned by digest; web `HEALTHCHECK` on `/api/health`;
   compose publishes `127.0.0.1:8080` / `127.0.0.1:3000` with a LAN override;
   memory limits on api/worker/web; CI `docker-build` job (no push). Postgres
   stays unpublished.
9. **SSRF.** Add 100.64.0.0/10 and 64:ff9b::/96 to the shared dial guard.

## Non-goals

Multi-replica (Redis) rate limiting; metrics/tracing; WAF or bot management;
changing how per-user provider-grant failures (Google/Microsoft/HubSpot/
Todoist 401 after refresh) reach clients; TLS inside the compose network;
image signing/SBOMs.

## Architecture

### Client IP and proxy trust (`httpapi/proxy.go`)

`Deps` gains `TrustProxy bool`, `TrustedProxyCIDRs []netip.Prefix` (config
parses; invalid CIDR = boot error). `proxyTrust` provides:

- `peerTrusted(r)`: `enabled && ParseAddr(host(RemoteAddr)).Unmap() ∈ nets`.
- `clientIP(r)`: untrusted peer → peer. Else join all `X-Forwarded-For`
  headers in order, split on commas, walk from the right, return the first
  hop that parses and is outside `nets`; none → peer. Canonical
  `Unmap().String()` (IPv6 unbracketed) is the bucket key.
- `forwardedProto(r)` / `forwardedHost(r)`: first value, only when trusted.

`requestBaseURL` becomes a `*server` method over these helpers, `r.TLS`/
`r.Host` as fallback. Caddy strips client-supplied `X-Forwarded-*` and the
nginx sample appends with `$proxy_add_x_forwarded_for`, so right-most-untrusted
yields the real client in both setups. Documented caveat: a client reaching
the API *directly* from a trusted CIDR can spoof; the loopback publish closes
it, and `TRUSTED_PROXY_CIDRS` can be narrowed to the proxy address.

### Rate limiting (`httpapi/ratelimit.go`)

`allow(key)` returns `(ok, retryAfter)`; `retryAfter = ceil((1 − tokens) /
perMin min)`, min 1 s → `Retry-After`. Limiters built in `New` from
`Deps.RateLimits`:

| Limiter | Key | Default (per min / burst) | Routes |
| --- | --- | --- | --- |
| `publicRead` | `ip:` | 60 / 30 | existing public GETs + share routes |
| `publicWrite` | `ip:` | 5 / 5 | existing public POSTs |
| `user` | `user:` | 600 / 600 | every `authed` route not in a class |
| `mutate_heavy` | `user:` | 30 / 30 | `POST /v1/mail/drafts/{id}/send`, `/v1/mail/threads/bulk-actions`, `/v1/mail/threads/zero`, `/v1/calendar-subscriptions`, `/v1/teams/{id}/invitations`, `/v1/booking-links`, `/v1/polls`, `/v1/mail/threads/{id}/share`, `/v1/ai/compose`, `/v1/ai/ask`, `/v1/ai/event-proposal` |
| `search` | `user:` | 120 / 120 | `GET /v1/search`, `/v1/mail/attachments`, `/v1/places/autocomplete` |

`authed(pattern, h, opts...)` takes `limitClass(name)` / `noDeadline()`;
chain: `requireAuth → userLimited(class) → withActAs → deadline → h`, so the
*actor* is charged, never the act-as principal. Polls and thread shares join
`mutate_heavy` because they mint public tokens; the AI daily budget still
applies. `0` disables a class (tests only). Limits multiply by replica count;
`upgrades.md` already assumes one `api` replica.

### Security headers and CSP

API (`httpapi/headers.go`, directly inside `recoverPanics`): four static
headers on every response including preflights; `Cache-Control: no-store`
under `/v1` (SSE handlers switch their `no-cache` to `no-store`, keep
`X-Accel-Buffering: no`); HSTS under decision 3's condition.

Web:

- `next.config.ts` `headers()`: the five static headers for `/(.*)`; for
  `/offline` only, a fixed CSP equal to the nonce policy but `script-src
  'self' 'unsafe-inline'` — that page is `force-static`
  (`app/offline/page.tsx:10`) and precached by `public/sw.js`, so it cannot
  carry a nonce; it renders no user content.
- `apps/web/middleware.ts` (Next 15.5; `proxy.ts` on Next 16), matcher
  `/((?!api/|_next/static|_next/image|sw\.js|manifest\.webmanifest|icon|offline).*)`.
  Per request: `nonce = base64(crypto.getRandomValues(16 bytes))`; policy per
  decision 3 with `connect-src 'self' ${env.apiUrl} https://*.paddle.com`
  (`lib/env.ts` normalises `NEXT_PUBLIC_API_URL`; empty = same origin). Dev
  only (`NODE_ENV !== 'production'`): `script-src` + `'unsafe-eval'`,
  `connect-src` + `ws:`. Append `report-uri /api/csp-report; report-to csp`
  and set `Reporting-Endpoints: csp="/api/csp-report"`. Header is
  `Content-Security-Policy-Report-Only` when `CSP_REPORT_ONLY=true`, else
  `Content-Security-Policy`; set on the request (so Next nonces its own
  inline scripts) and copied to the response; `x-nonce` request header.
- Nonce propagation: `app/layout.tsx` becomes `async`, reads
  `(await headers()).get('x-nonce')`, sets `nonce` on the theme-init script
  (`layout.tsx:49`). Consequence: every matched route renders dynamically
  (marketing pages lose prerendering; accepted). `/checkout` loads Paddle.js
  via `next/script` from `https://cdn.paddle.com/paddle/v2/paddle.js` —
  host-allowed, so no nonce needed on that tag; a thin server wrapper still
  passes `nonce` to the client component for any inline bootstrap.
- `app/api/csp-report/route.ts`: `POST`, `application/csp-report` or
  `application/reports+json`, ≤ 16 KiB, one `console.warn` JSON line
  `{msg:"csp_violation", documentUri, violatedDirective, blockedUri,
  sourceFile, lineNumber, userAgent}` → 204; else 400/413. No storage.
- Rollout: Task A ships with `CSP_REPORT_ONLY` defaulting `true`; Task B
  flips it to `false` once e2e and a sandbox checkout pass enforced. The env
  stays as a runtime rollback (no rebuild).

### Server lifecycle

- `http.Server{ReadHeaderTimeout: 10s, ReadTimeout: 60s, IdleTimeout: 120s,
  MaxHeaderBytes: 64 << 10}`; no `WriteTimeout`.
- `withDeadline(d)`: `context.WithTimeout` on the request; if the handler
  returns with nothing written and `ctx.Err() == DeadlineExceeded` → 504
  `timeout`. It does **not** buffer (unlike `http.TimeoutHandler`, which
  breaks `Flusher` and attachment streaming). 60 s default;
  `GET /v1/mail/attachments/{id}/content` 10 min; both SSE routes `noDeadline`.
- `Deps.Drain <-chan struct{}` (closed by `main` on signal) and
  `Deps.Ready func(context.Context) error` (DB ping). SSE handlers add
  `case <-s.deps.Drain` and return (EventSource reconnects). `GET /readyz`:
  drain closed → 503 `{"status":"draining"}`; ping (2 s) fails → 503
  `{"status":"db_unavailable"}`; else 200 `{"status":"ok"}`. Not routed by
  Caddy/nginx (internal; it reveals DB state). `/healthz` unchanged.
- API sequence: signal → `stop()` (second signal force-kills) →
  `close(drain)` → sleep `SHUTDOWN_DRAIN_DELAY` → `srv.Shutdown(ctx
  SHUTDOWN_TIMEOUT)`, on deadline log + `srv.Close()` → wait pgbus listener
  (now in a `WaitGroup`, ≤ 5 s) → deferred `db.Close()` → exit 0. `run()`
  extracts `serve(ctx, srv, drain)` so it is testable.
- Worker: `wg.Wait()` behind a `select` with a `SHUTDOWN_TIMEOUT` timer; on
  timeout log `worker: shutdown timed out`, exit 1. Loop bodies already get
  the cancelled `ctx`.
- Compose: `api.healthcheck` overrides the image default with `/readyz`
  (`interval 10s, timeout 3s, start_period 20s, retries 3`); `caddy`
  `depends_on` api/web `condition: service_healthy`.

### Config validation

- `FromEnv` returns `(Config, []string warnings, error)`; `main` logs
  warnings at `WARN` first. New checks, both binaries: `DATABASE_URL` parsed
  with `net/url`, password ∈ {`change-me-please`, `calendium`} → cloud error /
  self-host warning `DATABASE_URL uses a default password; set
  POSTGRES_PASSWORD`. `PUBLIC_WEB_URL` empty → cloud error, self-host warning
  for the worker; the API keeps today's hard requirement in both modes
  (`cmd/api/main.go:66-68`, `/v1/instance` needs it), moved into `FromEnv`
  behind a `RequirePublicWebURL` option. `TRUST_PROXY`,
  `TRUSTED_PROXY_CIDRS`, `RATE_LIMIT_*`, `SHUTDOWN_*`, `LOG_*` use the
  existing `errs` pattern.
- One startup line logs names and booleans only; DB URLs go through
  `redactURL` (password → `***`); a test asserts no secret value appears.
- Web `apps/web/instrumentation.ts` `register()`: when `NEXT_RUNTIME ===
  'nodejs'` and `NEXT_PHASE !== 'phase-production-build'`, require
  `Buffer.byteLength(BETTER_AUTH_SECRET ?? '') >= 32` else throw
  `BETTER_AUTH_SECRET must be at least 32 bytes; generate one with: openssl
  rand -base64 32`. The build stage has no secret, hence the phase guard.
  `lib/auth.ts` asserts the same at module load. Playwright's e2e secret
  (45 chars) passes; `bun run dev` now needs a real value (docs updated).

### Request limits (`httpapi/codec.go`, `httpapi/limits.go`)

- `decodeJSON` → 1 MiB; `decodeJSONLimit(w, r, dst, n)` for `POST
  /v1/mail/drafts` and `PUT /v1/mail/drafts/{id}` (10 MiB); webhook 1 MiB
  and public 16 KiB unchanged. `*http.MaxBytesError` → 413
  `payload_too_large` everywhere.
- `limits.go`: `maxTitleRunes = 500`, `maxTextBytes = 64 << 10`,
  `maxEmailRunes = 320`, `maxURLRunes = 2048`, `maxArrayItems = 500`; a
  `fieldCheck` collector (`title`, `text`, `email`, `url`, `list`) whose
  `err()` is `fmt.Errorf("%w: %s exceeds %d", domain.ErrValidation, field,
  limit)`. `errorDetail` gains `Details map[string]any` (`omitempty`);
  `writeError` fills `{"field","limit"}` (same slot piece 1 uses for 402).
- Applied in every handler decoding titles/names/subjects/slugs/labels; long
  text (`notes`, `description`, `body*`, `signatureHtml`, `prompt`); emails
  (`to/cc/bcc[]`, `email`, `vipSenders[]`, `autoBcc[]`); URLs (`url`,
  `redirectUrl`); every array (count, plus per-element email). Stricter
  domain limits (team name 120, comment body) stay authoritative.

### Logging

- `LOG_FORMAT` (`json` | `text`) and `LOG_LEVEL` pick the slog handler in
  both binaries.
- `requestID` middleware (inside `recoverPanics`, outside `logRequests`):
  accept inbound `X-Request-Id` only when `peerTrusted(r)` and it matches
  `^[A-Za-z0-9._-]{8,128}$`; else generate a 26-char Crockford-base32 ULID
  (48-bit ms + 80 bits `crypto/rand`). Context-stored, echoed on every
  response, and added to the error envelope as `error.requestId`.
- `logRequests` stores a mutable `*logFields{UserID, ActorID}` in the
  context that `requireAuth` / `withActAs` fill (inner context values are
  invisible to the outer logger). Line: `msg=http request_id method route
  status duration_ms bytes client_ip user_id actor_id`; `route` is
  `r.Pattern` (set by `ServeMux` on the pointer the middleware holds), or
  `redactPath(path)` when empty (404/405). Query strings are never logged.
- `redactPath`: `[redacted]` for the variable segment of
  `/v1/shared/threads/{token}[/stream]`, `/v1/public/polls/{token}[/votes]`,
  `/v1/mail/contacts/{email}`. `/v1/invitations/accept` carries its token in
  the body, never logged; the test pins that. `writeError` and
  `recoverPanics` log `route` + `request_id` instead of `path`.
- `domain.ErrUpstream` → 502 `upstream_unavailable`, message "A service
  Calendium depends on is unavailable. Please try again later." Wrapped by
  adapters whose credentials are the *platform's*: `openrouter`
  (`client.go:200`), the billing adapter (`stripeapi/client.go:84-85`; the
  piece-1 Paddle adapter may keep its more specific 502 `billing_unavailable`),
  and `authjwt` on a JWKS fetch failure with no cached key (`requireAuth`
  maps `ErrUpstream` to 502, not the 401 branch). Per-user grants (googleapi,
  msgraph, hubspot, todoist) keep `ErrUnauthorized`: services use it to drive
  the single refresh (`service/crm.go:155-157`, `service/account.go` token
  source). Provider name goes in the log line only.

### Containers and CI

- `backend/Dockerfile`: `golang:1.26-alpine@sha256:<digest>` and
  `alpine:3.22@sha256:<digest>`; `apps/web/Dockerfile`: `oven/bun:1.3@sha256:
  <digest>` (minor-pinned) and `node:22-alpine@sha256:<digest>`. Digests are
  resolved at implementation with `docker buildx imagetools inspect <ref>
  --format '{{.Manifest.Digest}}'` and recorded in a comment with tag and
  date. Web adds `HEALTHCHECK --interval=30s --timeout=5s --start-period=20s
  --retries=3 CMD wget -q -O /dev/null http://127.0.0.1:3000/api/health ||
  exit 1` (busybox `wget` ships in the alpine base). `app/api/health/route.ts`:
  `dynamic = 'force-dynamic'`, `GET` → 200 `{"status":"ok"}`.
- `docker-compose.yml`: `api.ports: "${API_BIND:-127.0.0.1}:${API_PORT:-8080}:8080"`,
  `web.ports: "${WEB_BIND:-127.0.0.1}:${WEB_PORT:-3000}:3000"`;
  `api.environment.TRUST_PROXY: "true"`; `deploy.resources.limits.memory:
  512m` on `api`, `worker`, `web` (honoured by compose v2 without swarm);
  `db` unchanged. `.env.example` documents `API_BIND=0.0.0.0` for LAN use
  without a proxy.
- `test.yml` gains `docker-build`: `docker/setup-buildx-action@v3`, two
  `docker/build-push-action@v6` steps (`push: false`, `context: .`,
  `file: backend/Dockerfile` / `apps/web/Dockerfile`, `build-args:
  NEXT_PUBLIC_API_URL=https://ci.invalid`, `cache-from/to: type=gha`). A new
  `.github/dependabot.yml` with a weekly `docker` ecosystem entry for
  `/backend`, `/apps/web`, `/` keeps digests from rotting.
- Docs: `security.md` §4/§7, `reverse-proxy-tls.md`, `upgrades.md`,
  `configuration.md` (table below).

### SSRF guard (`backend/internal/adapter/out/netguard`)

New package: `IsPublic(netip.Addr) bool` = `Unmap()`, `IsGlobalUnicast() &&
!IsPrivate()`, and not in `100.64.0.0/10` or `64:ff9b::/96` (whole NAT64
prefix blocked; nothing here needs NAT64); `Control(network, address string,
syscall.RawConn) error` for dialer use. `icsfeed.isPublicAddr` and
`unsubscribe.blockPrivateNetworks` delegate to it; existing tests move to
`netguard`, the package test hooks (`newFetcher(nil)`,
`allowLoopbackDialsForTest`) stay.

## Configuration

| Variable | Where | Default | Mode | Notes |
| --- | --- | --- | --- | --- |
| `TRUST_PROXY` | api | `false` | both | compose sets `true` on `api` |
| `TRUSTED_PROXY_CIDRS` | api | six CIDRs (decision 1) | both | comma-separated; bad entry = boot error |
| `RATE_LIMIT_PUBLIC_READ_PER_MIN` | api | `60` (burst 30) | both | per IP |
| `RATE_LIMIT_PUBLIC_WRITE_PER_MIN` | api | `5` (burst 5) | both | per IP |
| `RATE_LIMIT_USER_PER_MIN` | api | `600` | both | per user; `0` disables class |
| `RATE_LIMIT_MUTATE_HEAVY_PER_MIN` | api | `30` | both | per user |
| `RATE_LIMIT_SEARCH_PER_MIN` | api | `120` | both | per user |
| `SHUTDOWN_TIMEOUT` | api, worker | `30s` | both | Go duration |
| `SHUTDOWN_DRAIN_DELAY` | api | `0s` | both | `/readyz` 503 window before listeners close |
| `LOG_FORMAT` | api, worker | `json` | both | `text` for local dev |
| `LOG_LEVEL` | api, worker | `info` | both | `debug|info|warn|error` |
| `DATABASE_URL` | api, worker | required | both | default password: cloud error / self-host warn |
| `PUBLIC_WEB_URL` | api, worker | — | both | api required; worker cloud error / self-host warn |
| `BETTER_AUTH_SECRET` | web | — | both | < 32 bytes refuses to start |
| `CSP_REPORT_ONLY` | web (runtime) | `true` Task A, `false` from Task B | both | report-only vs enforce |
| `NEXT_PUBLIC_API_URL` | web (build) | same origin | both | feeds `connect-src` |
| `API_BIND` / `WEB_BIND` | compose | `127.0.0.1` | both | `0.0.0.0` for LAN without proxy |
| `API_PORT` / `WEB_PORT` | compose | `8080` / `3000` | both | unchanged |

## Compatibility notes

- **SSE**: no `WriteTimeout`, no deadline, drain-aware; every new wrapper uses
  one `responseWriter` forwarding `Flush` and implementing `Unwrap`
  (`http.ResponseController` keeps working). Clients reconnect on drain.
- **Paddle overlay** (`/checkout`, `/checkout/success`): script, iframe, XHR
  hosts and inline styles are covered by decision 3; a sandbox checkout under
  Report-Only with zero `csp_violation` lines gates Task B.
- **Better Auth** `/api/auth/*`: excluded from the CSP matcher; static
  headers apply. Apple `form_post` and Google redirects are top-level
  navigations, unaffected by `frame-ancestors`/`form-action`. Wails/Expo use
  bearer tokens and ignore `TRUST_PROXY`.
- **Email HTML**: remote `http:` images are blocked by `img-src https:`
  (accepted). The PDF preview iframe is a same-origin blob with `sandbox=""`
  (`attachments-pane.tsx:424-431`), allowed via `frame-src` → `default-src
  'self'` fallback; confirmed in the Report-Only phase.
- **Clients**: 429 is already handled by the outbox
  (`packages/shared/src/offline/outbox.ts:239`) and 502/503 by
  `aiErrorMessage` (`apps/web/lib/use-mail.ts:1009`); the initial query
  fan-out is far below the 600 burst.
- **e2e**: CI runs `next build && next start`, so the nonce path is
  exercised; demo mode and the `/api/auth` stubs are untouched; `/offline`
  keeps its static CSP. Marketing pages switch from `○` to `ƒ` at build.

## Testing and verification

Go (`httpapi` unless noted):

- `TestClientIP` matrix: trust off; trusted peer with 0/1/3 hops; right-most
  hop trusted → walks left; all hops trusted → peer; unparsable hop skipped;
  IPv4-mapped IPv6; multiple XFF headers; untrusted peer with XFF → peer.
  `TestRequestBaseURLForwardedOnlyFromTrustedProxy`.
- Limiter: per-user isolation; 31st `mutate_heavy` call → 429 with
  `Retry-After ≥ 1`; actor charged under act-as; `0` disables; public
  limits unchanged. `TestSecurityHeaders`: JSON, 404 and preflight; HSTS only
  with TLS or trusted `X-Forwarded-Proto: https`; `no-store` on `/v1` + SSE.
- `TestGracefulShutdown` (first `cmd/api` test, via `serve`): a handler
  blocked on a channel finishes with 200 after SIGTERM; `/readyz` → 503 while
  draining; the SSE stream ends. `TestReadyz`: ping ok → 200, error → 503.
  Deadline: 50 ms test deadline → 504; stream route has none; attachment
  route uses the long one.
- Limits: 1 MiB + 1 → 413; draft route accepts 2 MiB; table over every
  limited field (501-rune title, 64 KiB + 1 note, 321-char email, 2049-char
  URL, 501-item array) → 400 with `details.field`.
- Logging: line carries `request_id`, `route`, `user_id`; `X-Request-Id`
  echoed; inbound id accepted only from a trusted peer; redaction for the
  listed routes; no query string. 502 mapping: `openrouter` 401 →
  `ErrUpstream` → 502 `upstream_unavailable`; `requireAuth` with a verifier
  returning `ErrUpstream` → 502; cold-cache JWKS fetch failure → `ErrUpstream`.
- `netguard` table: `100.64.0.1`, `100.127.255.255`, `64:ff9b::7f00:1`,
  `64:ff9b::5db8:d822`, `100.128.0.1` (public) plus the existing cases.
  `config`: default passwords (cloud error / self-host warning), bad CIDR,
  `RATE_LIMIT_*` parsing, `PUBLIC_WEB_URL` rules, no secret in startup log.

Web (vitest): `middleware.ts` policy for prod/dev and report-only/enforce,
`connect-src` with and without `NEXT_PUBLIC_API_URL`, `x-nonce` set;
`headers()` snapshot; `/api/csp-report` 204 / 400 / 413; `/api/health` 200;
`instrumentation.ts` throws on a 31-byte secret and skips in the build phase.
Playwright: existing suite green with the CSP enforced (Task B gate) plus one
spec asserting the CSP response header and the nonce on the theme script.

CI: `docker-build` green for both images; `go vet`, `golangci-lint`, Biome
unchanged. Manual: compose up with Caddy, `curl -I` shows the headers;
`docker compose stop api` returns within 30 s while a request started just
before still gets 200.
