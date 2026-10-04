# Platform Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the API, worker, web app and the reference compose stack safe behind a public reverse proxy: trusted-proxy client IPs, per-user rate limits, security headers plus a nonce CSP, bounded requests, graceful shutdown, hard config validation, redacted JSON logs, pinned health-checked images and an SSRF guard that covers CGNAT/NAT64.

**Architecture:** Everything lands at the edges of the hexagon: new `httpapi` middleware (`proxy.go`, `headers.go`, `requestid.go`, `limits.go`, `ratelimit.go`, `codec.go`) in front of unchanged services; `config.FromEnv` grows warnings and the new knobs; `cmd/api` extracts a testable `serve()` with a drain channel and `/readyz`; a new `adapter/out/netguard` package is the one SSRF predicate. The web app adds a nonce CSP in `middleware.ts` (Report-Only first, enforced in a separate task), static headers in `next.config.ts`, `/api/csp-report`, `/api/health` and a `BETTER_AUTH_SECRET` boot check in `instrumentation.ts`. Containers get digest pins, health checks, loopback publishing and a CI image build.

**Tech Stack:** Go 1.26 stdlib (`net/http`, `net/netip`, `log/slog`, `crypto/rand`), Next.js 15.5 (`middleware.ts`, `instrumentation.ts`, route handlers), Vitest, Playwright (demo mode), Docker/Compose v2, GitHub Actions (`docker/build-push-action@v6`), Dependabot.

**Spec:** docs/superpowers/specs/2026-10-04-platform-hardening-design.md

## Global Constraints

- Hexagonal rule: `domain` imports only stdlib; `port` imports `domain`; `service` imports `domain` + `port`; adapters import `port`/`domain`; nothing in `domain`/`port`/`service` imports an adapter. Stdlib only in `backend/go.mod` (pgx stays a `database/sql` driver). No new migration (0030 reserved, unused).
- Client IP: `TRUST_PROXY` (bool, default `false`) + `TRUSTED_PROXY_CIDRS` (default when trusting: `127.0.0.0/8,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,::1/128,fc00::/7`); trusted peer → right-most `X-Forwarded-For` hop outside the trusted CIDRs, else the socket address; canonical key is `Unmap().String()`; `X-Forwarded-Host/Proto` honoured only from trusted proxies; invalid CIDR = boot error.
- Rate limits (per min / burst): `publicRead` `ip:` 60/30, `publicWrite` `ip:` 5/5, `user` `user:` 600/600, `mutate_heavy` `user:` 30/30, `search` `user:` 120/120; `0` disables a class; 429 + `Retry-After = ceil((1 − tokens) / perMin min)`, min 1 s; the actor (never the act-as principal) is charged; env `RATE_LIMIT_PUBLIC_READ_PER_MIN`, `RATE_LIMIT_PUBLIC_WRITE_PER_MIN`, `RATE_LIMIT_USER_PER_MIN`, `RATE_LIMIT_MUTATE_HEAVY_PER_MIN`, `RATE_LIMIT_SEARCH_PER_MIN`.
- `mutate_heavy` routes: `POST /v1/mail/drafts/{id}/send`, `POST /v1/mail/threads/bulk-actions`, `POST /v1/mail/threads/zero`, `POST /v1/calendar-subscriptions`, `POST /v1/teams/{id}/invitations`, `POST /v1/booking-links`, `POST /v1/polls`, `POST /v1/mail/threads/{id}/share`, `POST /v1/ai/compose`, `POST /v1/ai/ask`, `POST /v1/ai/event-proposal`. `search` routes: `GET /v1/search`, `GET /v1/mail/attachments`, `GET /v1/places/autocomplete`.
- API headers on every response incl. preflights: `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Frame-Options: DENY`; `Cache-Control: no-store` under `/v1` (SSE switches `no-cache` → `no-store`, keeps `X-Accel-Buffering: no`); HSTS `max-age=31536000; includeSubDomains` only on TLS or trusted `X-Forwarded-Proto: https`.
- Web static headers for `/(.*)`: HSTS `max-age=31536000; includeSubDomains`, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: strict-origin-when-cross-origin`, `Permissions-Policy: camera=(), microphone=(), geolocation=()`.
- CSP (middleware, per request nonce = base64 of 16 random bytes): `default-src 'self'; script-src 'self' 'nonce-<n>' https://cdn.paddle.com; frame-src https://*.paddle.com https://accounts.google.com https://appleid.apple.com; connect-src 'self' <API origin> https://*.paddle.com; img-src 'self' data: https:; style-src 'self' 'unsafe-inline'; font-src 'self' data:; frame-ancestors 'none'; base-uri 'self'; form-action 'self' https://appleid.apple.com` + `report-uri /api/csp-report; report-to csp`; dev only adds `'unsafe-eval'` to `script-src` and `ws:` to `connect-src`; matcher `/((?!api/|_next/static|_next/image|sw\.js|manifest\.webmanifest|icon|offline).*)`; `/offline` gets the same policy with `script-src 'self' 'unsafe-inline'` from `next.config.ts`; header name is `Content-Security-Policy-Report-Only` when `CSP_REPORT_ONLY=true` else `Content-Security-Policy`; `CSP_REPORT_ONLY` defaults `true` in Task 16 and `false` from Task 18 (runtime env, no rebuild). Paddle.js (`https://cdn.paddle.com/paddle/v2/paddle.js`, frames/XHR to `*.paddle.com`) and Better Auth `/api/auth/*` stay compatible.
- `/api/csp-report`: `POST`, `application/csp-report` or `application/reports+json`, ≤ 16 KiB, one `console.warn` JSON line `{msg:"csp_violation", documentUri, violatedDirective, blockedUri, sourceFile, lineNumber, userAgent}` → 204; else 400 / 413. `/api/health`: `dynamic = 'force-dynamic'`, 200 `{"status":"ok"}`.
- HTTP server: `ReadHeaderTimeout` 10 s, `ReadTimeout` 60 s, `IdleTimeout` 120 s, `MaxHeaderBytes` 64 KiB, no `WriteTimeout`; 60 s per-handler deadline on authed non-stream routes → 504 `timeout` when nothing was written; `GET /v1/mail/attachments/{id}/content` 10 min; both SSE routes have no deadline and end on drain.
- Shutdown: signal → `stop()` (second signal force-kills) → `close(drain)` → sleep `SHUTDOWN_DRAIN_DELAY` (default `0s`) → `srv.Shutdown` (`SHUTDOWN_TIMEOUT`, default `30s`), on deadline log + `srv.Close()` → wait pgbus listener ≤ 5 s → `db.Close()` → exit 0. Worker `wg.Wait()` bounded by `SHUTDOWN_TIMEOUT`; on timeout log `worker: shutdown timed out`, exit 1.
- `GET /readyz`: drain closed → 503 `{"status":"draining"}`; DB ping (2 s) fails → 503 `{"status":"db_unavailable"}`; else 200 `{"status":"ok"}`. Not routed by Caddy/nginx. `/healthz` unchanged. Compose checks `api` on `/readyz` (`interval 10s, timeout 3s, start_period 20s, retries 3`).
- Config: `FromEnv` returns `(Config, []string warnings, error)`; `DATABASE_URL` password ∈ {`change-me-please`, `calendium`} → cloud error / self-host warning `DATABASE_URL uses a default password; set POSTGRES_PASSWORD`; `PUBLIC_WEB_URL` empty → cloud error / self-host warning, or error in both modes with `RequirePublicWebURL()` (api); `LOG_FORMAT` `json|text` (default `json`), `LOG_LEVEL` `debug|info|warn|error` (default `info`); startup line logs names/booleans only, DB URL via `RedactURL` (password → `***`).
- Web refuses `BETTER_AUTH_SECRET` with `Buffer.byteLength < 32` in all modes (`instrumentation.ts` `register()` when `NEXT_RUNTIME === 'nodejs'` and `NEXT_PHASE !== 'phase-production-build'`; `lib/auth.ts` at module load) with the message `BETTER_AUTH_SECRET must be at least 32 bytes; generate one with: openssl rand -base64 32`.
- Request limits: JSON 1 MiB default; 10 MiB `POST /v1/mail/drafts` and `PUT /v1/mail/drafts/{id}`; 16 KiB public; 1 MiB webhook; oversize → 413 `payload_too_large` everywhere. Field limits: `maxTitleRunes = 500`, `maxTextBytes = 64 << 10`, `maxEmailRunes = 320`, `maxURLRunes = 2048`, `maxArrayItems = 500` → 400 `validation_failed` with `details: {"field","limit"}`; stricter domain limits (team name 120, comment body) stay authoritative.
- Logging: slog JSON; `request_id` is a 26-char Crockford-base32 ULID (48-bit ms + 80 bits `crypto/rand`), echoed as `X-Request-Id`, accepted inbound only from a trusted peer matching `^[A-Za-z0-9._-]{8,128}$`, added to the error envelope as `error.requestId`; line `msg=http request_id method route status duration_ms bytes client_ip user_id actor_id`; `route` is `r.Pattern` or `redactPath(path)`; `[redacted]` for `/v1/shared/threads/{token}[/stream]`, `/v1/public/polls/{token}[/votes]`, `/v1/mail/contacts/{email}`; query strings never logged.
- `domain.ErrUpstream` → 502 `upstream_unavailable`, message `A service Calendium depends on is unavailable. Please try again later.`; wrapped by `openrouter` (401/403), the billing adapter, and `authjwt` on a JWKS fetch failure with no cached key; `requireAuth` maps it to 502; per-user grants keep `ErrUnauthorized`; provider named only in logs.
- Containers: `golang:1.26-alpine@sha256:<digest>`, `alpine:3.22@sha256:<digest>`, `oven/bun:1.3@sha256:<digest>`, `node:22-alpine@sha256:<digest>` resolved with `docker buildx imagetools inspect <ref> --format '{{.Manifest.Digest}}'`; web `HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD wget -q -O /dev/null http://127.0.0.1:3000/api/health || exit 1`; compose `api.ports: "${API_BIND:-127.0.0.1}:${API_PORT:-8080}:8080"`, `web.ports: "${WEB_BIND:-127.0.0.1}:${WEB_PORT:-3000}:3000"`, `api.environment.TRUST_PROXY: "true"`, `deploy.resources.limits.memory: 512m` on api/worker/web, `db` unpublished; CI `docker-build` job with `push: false`; weekly Dependabot `docker` entries for `/backend`, `/apps/web`, `/`.
- SSRF: `netguard.IsPublic` = `Unmap()`, `IsGlobalUnicast() && !IsPrivate()`, and not in `100.64.0.0/10` or `64:ff9b::/96`; `icsfeed.isPublicAddr` and `unsubscribe.blockPrivateNetworks` delegate; `newFetcher(nil)` and `allowLoopbackDialsForTest` hooks stay.
- Demo mode, the `/api/auth` e2e stubs and the Playwright suite (`next build && next start` in CI) stay green; marketing pages switch from `○` to `ƒ` at build (accepted).

## Review Focus

1. An `X-Forwarded-For` hop carrying a port or brackets (`203.0.113.5:4321`, `[2001:db8::1]`) does not parse as an address: it is skipped and the walk continues to the next hop, never crashes, never trusts the proxy's own address. (Test added to Task 1.)
2. A handler that already wrote a 200 and part of its body before the deadline fires must not get a 504 appended: the deadline wrapper writes 504 only when nothing was written. (Test added to Task 5.)
3. Field limits count runes for titles/emails/URLs and bytes for text: a 500-rune title made of multi-byte characters (1000 bytes) is accepted, a 501-ASCII title is rejected, and a 64 KiB text of 2-byte runes (32 768 runes) is rejected. (Test added to Task 7.)
4. An inbound `X-Request-Id` from a trusted proxy that fails the regex (spaces, 200 chars, control characters) is replaced by a generated id and the inbound value never reaches the response header or the log line (log-injection guard). (Test added to Task 2.)
5. A readiness ping that ignores its context and hangs still yields a 503 `db_unavailable` within the 2 s budget instead of stalling the compose health check. (Test added to Task 10.)

## Execution tracks

Strict file ownership — a file appears in exactly one track; agents never edit another track's files.

- **Track A — Go httpapi middleware (Tasks 1–10, sequential).** Owns everything under `backend/internal/adapter/in/httpapi/` (all `.go` and `_test.go` files, incl. `httpapi.go`, `harness_test.go`), `backend/internal/domain/errors.go`, `backend/internal/adapter/out/openrouter/*`, `backend/internal/adapter/out/authjwt/*`, `backend/internal/adapter/out/stripeapi/*`. Starts immediately. Produces the `Deps` fields Track B's Task 12 consumes.
- **Track B — Go lifecycle + config (Tasks 11–14).** Owns `backend/internal/config/*`, `backend/cmd/api/*`, `backend/cmd/worker/*`, `backend/internal/adapter/out/netguard/*` (new), `backend/internal/adapter/out/icsfeed/*`, `backend/internal/adapter/out/unsubscribe/*`, `backend/.env.example`. Task 11 and Task 14 start immediately; Task 13 after Task 11; Task 12 after Task 11 **and** Track A Tasks 1, 3 and 10 are merged (it wires `Deps.TrustProxy/TrustedProxyCIDRs/RateLimits/Drain/Ready`).
- **Track C — web (Tasks 15–18, sequential).** Owns `apps/web/next.config.ts`, `apps/web/middleware.ts`, `apps/web/instrumentation.ts`, `apps/web/app/layout.tsx`, `apps/web/app/api/csp-report/route.ts`, `apps/web/app/api/health/route.ts`, `apps/web/lib/csp.ts`, `apps/web/lib/csp.test.ts`, `apps/web/lib/csp-report.ts`, `apps/web/lib/csp-report.test.ts`, `apps/web/lib/security-headers.ts`, `apps/web/lib/security-headers.test.ts`, `apps/web/lib/auth-secret.ts`, `apps/web/lib/auth-secret.test.ts`, `apps/web/lib/auth.ts`, `apps/web/middleware.test.ts`, `apps/web/instrumentation.test.ts`, `apps/web/app/api/csp-report/route.test.ts`, `apps/web/app/api/health/route.test.ts`, `apps/web/vitest.setup.ts`, `apps/web/e2e/csp.spec.ts`, `apps/web/.env.example`. Starts immediately. Task 18 (enforce) runs only after Task 16 and the e2e suite passes enforced.
- **Track D — containers / CI / docs (Tasks 19–20).** Owns `backend/Dockerfile`, `apps/web/Dockerfile`, `docker-compose.yml`, `.github/workflows/test.yml`, `.github/dependabot.yml`, `.env.example` (root), `docs/self-hosting/security.md`, `docs/self-hosting/reverse-proxy-tls.md`, `docs/self-hosting/upgrades.md`, `docs/self-hosting/configuration.md`. Starts immediately (compose/doc changes reference env vars that are inert until Tracks A/B merge).
- **Gate — Tasks 21–22** run after all tracks are merged.

---

### Task 1: Proxy trust, client IP and `requestBaseURL` (Track A)

**Files:**
- Create: `backend/internal/adapter/in/httpapi/proxy.go`, `backend/internal/adapter/in/httpapi/proxy_test.go`
- Modify: `backend/internal/adapter/in/httpapi/httpapi.go` (Deps L16-87, `New` L89-96, `server` struct L85-87), `backend/internal/adapter/in/httpapi/ratelimit.go` (L72-79 `clientIP`, L83-94 `rateLimited`), `backend/internal/adapter/in/httpapi/accounts.go` (L34-35, L110-111, L133-151), `backend/internal/adapter/in/httpapi/integrations.go` (L48-49, L75-76), `backend/internal/adapter/in/httpapi/harness_test.go` (L1340 `server()`), `backend/internal/adapter/in/httpapi/ratelimit_test.go` (L96-114 `TestClientIP`)

**Interfaces:**
- Consumes: nothing new.
- Produces: `Deps.TrustProxy bool`, `Deps.TrustedProxyCIDRs []netip.Prefix`; `type proxyTrust struct{ enabled bool; nets []netip.Prefix }` with `peerTrusted(r *http.Request) bool`, `clientIP(r *http.Request) string`, `forwardedProto(r *http.Request) string`, `forwardedHost(r *http.Request) string`; `func newServer(deps Deps) *server`; `func (s *server) clientIP(r *http.Request) string`; `func (s *server) requestBaseURL(r *http.Request) string`; test helper `func prefixes(t *testing.T, cidrs ...string) []netip.Prefix`.

- [ ] **Step 1: Write the failing tests** in `backend/internal/adapter/in/httpapi/proxy_test.go`:

```go
package httpapi

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

// prefixes parses CIDRs for tests (the config package owns the production default list).
func prefixes(t *testing.T, cidrs ...string) []netip.Prefix {
	t.Helper()
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		out = append(out, netip.MustParsePrefix(c))
	}
	return out
}

func trustedProxy(t *testing.T) proxyTrust {
	t.Helper()
	return proxyTrust{enabled: true, nets: prefixes(t,
		"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "::1/128", "fc00::/7")}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		trust      bool
		remoteAddr string
		xff        []string
		want       string
	}{
		{"trust off ignores XFF", false, "10.0.0.2:1234", []string{"203.0.113.9"}, "10.0.0.2"},
		{"trust off, no port", false, "203.0.113.5", nil, "203.0.113.5"},
		{"trust off, ipv6 with port", false, "[2001:db8::1]:443", nil, "2001:db8::1"},
		{"trusted peer, zero hops", true, "10.0.0.2:1234", nil, "10.0.0.2"},
		{"trusted peer, one hop", true, "10.0.0.2:1234", []string{"203.0.113.9"}, "203.0.113.9"},
		{"trusted peer, three hops", true, "10.0.0.2:1234", []string{"198.51.100.7, 203.0.113.9, 10.0.0.3"}, "203.0.113.9"},
		{"right-most hop trusted walks left", true, "10.0.0.2:1234", []string{"203.0.113.9, 192.168.1.1"}, "203.0.113.9"},
		{"all hops trusted falls back to peer", true, "10.0.0.2:1234", []string{"192.168.1.1, 10.0.0.3"}, "10.0.0.2"},
		{"unparsable hop skipped", true, "10.0.0.2:1234", []string{"203.0.113.9, garbage"}, "203.0.113.9"},
		{"ipv4-mapped ipv6 hop is unmapped", true, "10.0.0.2:1234", []string{"::ffff:203.0.113.9"}, "203.0.113.9"},
		{"multiple XFF headers joined in order", true, "10.0.0.2:1234", []string{"198.51.100.7", "203.0.113.9"}, "203.0.113.9"},
		{"untrusted peer with XFF uses peer", true, "203.0.113.50:1234", []string{"198.51.100.7"}, "203.0.113.50"},
		{"ipv6 peer in ULA trusted", true, "[fd00::5]:1234", []string{"2001:db8::9"}, "2001:db8::9"},
		// Review Focus 1: a hop with a port / brackets does not parse and is skipped.
		{"hop with port skipped", true, "10.0.0.2:1234", []string{"203.0.113.9, 198.51.100.7:4321"}, "203.0.113.9"},
		{"bracketed hop skipped", true, "10.0.0.2:1234", []string{"203.0.113.9, [2001:db8::1]"}, "203.0.113.9"},
		{"only unparsable hops fall back to peer", true, "10.0.0.2:1234", []string{"198.51.100.7:4321"}, "10.0.0.2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := proxyTrust{}
			if tt.trust {
				p = trustedProxy(t)
			}
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remoteAddr
			for _, v := range tt.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := p.clientIP(r); got != tt.want {
				t.Fatalf("clientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPeerTrustedRequiresEnabledAndMembership(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.2:1234"
	if (proxyTrust{}).peerTrusted(r) {
		t.Fatal("disabled trust must never trust a peer")
	}
	if !trustedProxy(t).peerTrusted(r) {
		t.Fatal("10.0.0.2 is inside the default trusted CIDRs")
	}
	r.RemoteAddr = "203.0.113.5:1234"
	if trustedProxy(t).peerTrusted(r) {
		t.Fatal("203.0.113.5 is not a trusted proxy")
	}
	r.RemoteAddr = "not-an-address"
	if trustedProxy(t).peerTrusted(r) {
		t.Fatal("an unparsable RemoteAddr must not be trusted")
	}
}

func TestRequestBaseURLForwardedOnlyFromTrustedProxy(t *testing.T) {
	tests := []struct {
		name       string
		trust      bool
		remoteAddr string
		tls        bool
		proto      string
		host       string
		want       string
	}{
		{"plain request", false, "203.0.113.5:1", false, "", "", "http://api.example.test"},
		{"tls request", false, "203.0.113.5:1", true, "", "", "https://api.example.test"},
		{"untrusted peer headers ignored", false, "10.0.0.2:1", false, "https", "evil.example", "http://api.example.test"},
		{"trusted peer headers honoured", true, "10.0.0.2:1", false, "https", "app.example.com", "https://app.example.com"},
		{"trusted peer first value wins", true, "10.0.0.2:1", false, "https, http", "app.example.com, internal", "https://app.example.com"},
		{"trusted but untrusted peer address", true, "203.0.113.5:1", false, "https", "evil.example", "http://api.example.test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.deps.TrustProxy = tt.trust
			h.deps.TrustedProxyCIDRs = prefixes(t, "10.0.0.0/8")
			s := h.server()
			r := httptest.NewRequest(http.MethodGet, "http://api.example.test/v1/accounts/connect/google", nil)
			r.RemoteAddr = tt.remoteAddr
			if tt.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if tt.proto != "" {
				r.Header.Set("X-Forwarded-Proto", tt.proto)
			}
			if tt.host != "" {
				r.Header.Set("X-Forwarded-Host", tt.host)
			}
			if got := s.requestBaseURL(r); got != tt.want {
				t.Fatalf("requestBaseURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Delete the old `TestClientIP`** from `backend/internal/adapter/in/httpapi/ratelimit_test.go` (L96-114, it is superseded by the matrix above) and run the new tests:

```bash
cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/adapter/in/httpapi/ -run 'TestClientIP|TestPeerTrusted|TestRequestBaseURL' 2>&1 | head -20
```
Expected: build failure `undefined: proxyTrust` / `h.deps.TrustProxy undefined`.

- [ ] **Step 3: Create `backend/internal/adapter/in/httpapi/proxy.go`:**

```go
package httpapi

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// proxyTrust decides which socket peers may speak for the real client
// through X-Forwarded-* (spec decision 1). With enabled=false every header
// is ignored and the socket address is the client. A client that reaches
// the API directly from a trusted CIDR can spoof the headers — the
// loopback publish in docker-compose.yml closes that, and
// TRUSTED_PROXY_CIDRS can be narrowed to the proxy's own address.
type proxyTrust struct {
	enabled bool
	nets    []netip.Prefix
}

// peerAddr parses the socket peer out of r.RemoteAddr (host:port or bare
// host), unmapping IPv4-in-IPv6.
func peerAddr(r *http.Request) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}

func (p proxyTrust) inNets(a netip.Addr) bool {
	for _, n := range p.nets {
		if n.Contains(a) {
			return true
		}
	}
	return false
}

// peerTrusted reports whether the socket peer is a trusted proxy.
func (p proxyTrust) peerTrusted(r *http.Request) bool {
	if !p.enabled {
		return false
	}
	a, ok := peerAddr(r)
	return ok && p.inNets(a)
}

// clientIP is the canonical rate-limit / log key: the socket peer unless it
// is a trusted proxy, in which case the right-most X-Forwarded-For hop that
// parses and is outside the trusted CIDRs. Hops that do not parse (ports,
// brackets, garbage) are skipped; no usable hop means the peer itself.
func (p proxyTrust) clientIP(r *http.Request) string {
	peer, ok := peerAddr(r)
	if !ok {
		return r.RemoteAddr
	}
	if !p.peerTrusted(r) {
		return peer.String()
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(v, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			continue
		}
		a = a.Unmap()
		if !p.inNets(a) {
			return a.String()
		}
	}
	return peer.String()
}

// forwardedProto returns the first X-Forwarded-Proto value, lower-cased,
// only when the peer is a trusted proxy; "" otherwise.
func (p proxyTrust) forwardedProto(r *http.Request) string {
	if !p.peerTrusted(r) {
		return ""
	}
	v := r.Header.Get("X-Forwarded-Proto")
	if v == "" {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(strings.Split(v, ",")[0]))
}

// forwardedHost returns the first X-Forwarded-Host value only when the peer
// is a trusted proxy; "" otherwise.
func (p proxyTrust) forwardedHost(r *http.Request) string {
	if !p.peerTrusted(r) {
		return ""
	}
	v := r.Header.Get("X-Forwarded-Host")
	if v == "" {
		return ""
	}
	return strings.TrimSpace(strings.Split(v, ",")[0])
}

// clientIP is the server-level accessor every middleware uses.
func (s *server) clientIP(r *http.Request) string { return s.proxy.clientIP(r) }

// requestBaseURL derives the API's public origin (scheme://host) from the
// incoming request: TLS/Host by default, X-Forwarded-Proto/Host only from a
// trusted proxy. Used to build the provider redirect_uri when
// PUBLIC_API_URL is unset.
func (s *server) requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := s.proxy.forwardedProto(r); p != "" {
		scheme = p
	}
	host := r.Host
	if h := s.proxy.forwardedHost(r); h != "" {
		host = h
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}
```

- [ ] **Step 4: Wire `Deps` and the server constructor** in `backend/internal/adapter/in/httpapi/httpapi.go`. Add to the imports `"net/netip"`. Append to `Deps` after `CORSAllowedOrigins`:

```go
	// TrustProxy (TRUST_PROXY) makes X-Forwarded-* from a peer inside
	// TrustedProxyCIDRs authoritative for the client IP, scheme and host.
	TrustProxy bool
	// TrustedProxyCIDRs (TRUSTED_PROXY_CIDRS) is the proxy allowlist; the
	// config package supplies the default loopback/RFC1918/ULA set.
	TrustedProxyCIDRs []netip.Prefix
```

Replace the `server` struct and the top of `New`:

```go
type server struct {
	deps  Deps
	proxy proxyTrust
}

// newServer builds the handler state shared by New and the middleware unit
// tests (harness.server()).
func newServer(deps Deps) *server {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	return &server{
		deps:  deps,
		proxy: proxyTrust{enabled: deps.TrustProxy, nets: deps.TrustedProxyCIDRs},
	}
}

// New builds the full v1 REST handler ...
func New(deps Deps) http.Handler {
	s := newServer(deps)
	deps = s.deps
```

(delete the old `if deps.Logger == nil {...}` and `s := &server{deps: deps}` lines).

- [ ] **Step 5: Switch the callers.** In `ratelimit.go` delete the package-level `clientIP` function (L70-79) and in `rateLimited` change `l.allow(clientIP(r))` to `l.allow(s.clientIP(r))`. In `accounts.go` delete the package-level `requestBaseURL` (L133-151) and change both call sites (`handleConnectAccount`, `handleAccountCallback`) from `requestBaseURL(r)` to `s.requestBaseURL(r)`; drop the now-unused `"strings"` import if `goimports`/`go vet` flags it (`withStatusParam` still uses `net/url`; `strings` is still used by `writeCallbackPage` — check with `go build`). In `integrations.go` change both `requestBaseURL(r)` call sites to `s.requestBaseURL(r)`. In `harness_test.go` change `func (h *harness) server() *server { return &server{deps: h.deps} }` to `func (h *harness) server() *server { return newServer(h.deps) }`.

- [ ] **Step 6: Run the package tests:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium/backend && go build ./... && go test ./internal/adapter/in/httpapi/ 2>&1 | tail -5
```
Expected: `ok  calendium/backend/internal/adapter/in/httpapi`.

- [ ] **Step 7: Commit:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && git add backend/internal/adapter/in/httpapi && git commit -m "feat(httpapi): trusted-proxy client IP and forwarded headers" -m "clientIP walks X-Forwarded-For right-to-left only from peers inside TRUSTED_PROXY_CIDRS; requestBaseURL honours X-Forwarded-Proto/Host only from trusted proxies." -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 2: Unified response writer, request ids and structured request logs (Track A)

**Files:**
- Create: `backend/internal/adapter/in/httpapi/requestid.go`, `backend/internal/adapter/in/httpapi/requestid_test.go`, `backend/internal/adapter/in/httpapi/logging_test.go`
- Modify: `backend/internal/adapter/in/httpapi/middleware.go` (L24-50 `requireAuth`, L63-99 `statusRecorder`/`logRequests`, L103-122 `recoverPanics`), `backend/internal/adapter/in/httpapi/delegation.go` (L150-152 `withActAs` principal swap, L164 `statusRecorder`), `backend/internal/adapter/in/httpapi/codec.go` (L15-22 `errorDetail`, L125-137 `writeError`), `backend/internal/adapter/in/httpapi/httpapi.go` (`New` tail L337-342)

**Interfaces:**
- Consumes: `s.proxy.peerTrusted`, `s.clientIP` (Task 1).
- Produces: `type responseWriter struct` (`WriteHeader`, `Write`, `Flush`, `Unwrap`; fields `status int`, `bytes int64`, `wrote bool`); `func newRequestID(now time.Time) string`; `func requestIDFrom(ctx context.Context) string`; `func (s *server) requestID(next http.Handler) http.Handler`; `type logFields struct{ UserID, ActorID string }`; `func setLogUser(ctx context.Context, userID, actorID string)`; `func redactPath(path string) string`; `func routeOf(r *http.Request) string`; `errorDetail.RequestID string \`json:"requestId,omitempty"\``; `errorDetail.Details map[string]any \`json:"details,omitempty"\``.

- [ ] **Step 1: Write the failing request-id tests** in `backend/internal/adapter/in/httpapi/requestid_test.go`:

```go
package httpapi

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

var ulidRe = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

func TestNewRequestIDFormat(t *testing.T) {
	// ULID spec vector: ms 1469918176385 encodes to the time prefix 01ARYZ6S41.
	id := newRequestID(time.UnixMilli(1469918176385))
	if !ulidRe.MatchString(id) {
		t.Fatalf("id %q is not 26 Crockford base32 chars", id)
	}
	if !strings.HasPrefix(id, "01ARYZ6S41") {
		t.Fatalf("id %q time prefix, want 01ARYZ6S41", id)
	}
	if other := newRequestID(time.UnixMilli(1469918176385)); other == id {
		t.Fatal("two ids with the same timestamp must differ (random tail)")
	}
	if zero := newRequestID(time.UnixMilli(0)); !strings.HasPrefix(zero, "0000000000") {
		t.Fatalf("epoch id %q should start with ten zeros", zero)
	}
}

func TestRequestIDGeneratedAndEchoed(t *testing.T) {
	h := newHarness(t)
	rec := h.anon(http.MethodGet, "/healthz", nil)
	got := rec.Header().Get("X-Request-Id")
	if !ulidRe.MatchString(got) {
		t.Fatalf("X-Request-Id = %q, want a generated ULID", got)
	}
}

func TestRequestIDInboundOnlyFromTrustedPeer(t *testing.T) {
	const inbound = "edge-7f3a9c2e-0001"
	cases := []struct {
		name       string
		trust      bool
		remoteAddr string
		wantEcho   bool
	}{
		{"untrusted peer is ignored", false, "10.0.0.2:1", false},
		{"trusted peer is honoured", true, "10.0.0.2:1", true},
		{"trust on but peer outside CIDRs", true, "203.0.113.5:1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.deps.TrustProxy = tc.trust
			h.deps.TrustedProxyCIDRs = prefixes(t, "10.0.0.0/8")
			req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
			req.RemoteAddr = tc.remoteAddr
			req.Header.Set("X-Request-Id", inbound)
			rec := httptest.NewRecorder()
			h.handler().ServeHTTP(rec, req)
			got := rec.Header().Get("X-Request-Id")
			if tc.wantEcho && got != inbound {
				t.Fatalf("X-Request-Id = %q, want inbound %q", got, inbound)
			}
			if !tc.wantEcho && (got == inbound || !ulidRe.MatchString(got)) {
				t.Fatalf("X-Request-Id = %q, want a fresh ULID", got)
			}
		})
	}
}

// Review Focus 4: a malformed inbound id from a trusted proxy is replaced.
func TestRequestIDRejectsMalformedInbound(t *testing.T) {
	for _, bad := range []string{"has spaces here", strings.Repeat("a", 200), "short", "tab\there", "x\x00y12345"} {
		h := newHarness(t)
		h.deps.TrustProxy = true
		h.deps.TrustedProxyCIDRs = prefixes(t, "10.0.0.0/8")
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.RemoteAddr = "10.0.0.2:1"
		req.Header.Set("X-Request-Id", bad)
		rec := httptest.NewRecorder()
		h.handler().ServeHTTP(rec, req)
		got := rec.Header().Get("X-Request-Id")
		if got == bad || !ulidRe.MatchString(got) {
			t.Fatalf("inbound %q: X-Request-Id = %q, want a fresh ULID", bad, got)
		}
	}
}

func TestErrorEnvelopeCarriesRequestID(t *testing.T) {
	h := newHarness(t)
	rec := h.anon(http.MethodGet, "/v1/me", nil) // 401 from requireAuth
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	e := decodeErr(t, rec)
	if e.RequestID == "" || e.RequestID != rec.Header().Get("X-Request-Id") {
		t.Fatalf("error.requestId = %q, header = %q; want equal and non-empty", e.RequestID, rec.Header().Get("X-Request-Id"))
	}
}
```

- [ ] **Step 2: Write the failing logging tests** in `backend/internal/adapter/in/httpapi/logging_test.go`:

```go
package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// jsonLogLines parses every JSON line the harness logger wrote.
func jsonLogLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		out = append(out, m)
	}
	return out
}

func httpLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	for _, m := range jsonLogLines(t, buf) {
		if m["msg"] == "http" {
			return m
		}
	}
	t.Fatal("no msg=http line logged")
	return nil
}

func TestLogLineFields(t *testing.T) {
	var buf bytes.Buffer
	h := newHarness(t)
	h.deps.Logger = slog.New(slog.NewJSONHandler(&buf, nil))
	req := httptest.NewRequest(http.MethodGet, "/v1/me?token=should-not-be-logged", nil)
	req.RemoteAddr = "203.0.113.9:4321"
	req.Header.Set("Authorization", "Bearer "+defaultToken)
	rec := httptest.NewRecorder()
	h.handler().ServeHTTP(rec, req)

	line := httpLine(t, &buf)
	for _, key := range []string{"request_id", "method", "route", "status", "duration_ms", "bytes", "client_ip", "user_id", "actor_id"} {
		if _, ok := line[key]; !ok {
			t.Errorf("log line missing %q: %v", key, line)
		}
	}
	if line["route"] != "/v1/me" {
		t.Errorf("route = %v, want /v1/me (pattern, not raw path)", line["route"])
	}
	if line["user_id"] != defaultUserID {
		t.Errorf("user_id = %v, want %s", line["user_id"], defaultUserID)
	}
	if line["client_ip"] != "203.0.113.9" {
		t.Errorf("client_ip = %v, want 203.0.113.9", line["client_ip"])
	}
	if line["request_id"] != rec.Header().Get("X-Request-Id") {
		t.Errorf("request_id = %v, want the echoed header %q", line["request_id"], rec.Header().Get("X-Request-Id"))
	}
	if strings.Contains(buf.String(), "should-not-be-logged") {
		t.Fatalf("query string leaked into logs: %s", buf.String())
	}
	if _, ok := line["path"]; ok {
		t.Fatalf("raw path must not be logged: %v", line)
	}
}

func TestLogActorUnderActAs(t *testing.T) {
	var buf bytes.Buffer
	h, _ := delegHarness(t)
	h.deps.Logger = slog.New(slog.NewJSONHandler(&buf, nil))
	rec := actAs(h, "principal_1", http.MethodGet, "/v1/mail/threads", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body=%s)", rec.Code, rec.Body.String())
	}
	line := httpLine(t, &buf)
	if line["user_id"] != "principal_1" || line["actor_id"] != defaultUserID {
		t.Fatalf("user_id=%v actor_id=%v, want principal_1 / %s", line["user_id"], line["actor_id"], defaultUserID)
	}
}

func TestRedactPath(t *testing.T) {
	tests := map[string]string{
		"/v1/shared/threads/tok123":           "/v1/shared/threads/[redacted]",
		"/v1/shared/threads/tok123/stream":    "/v1/shared/threads/[redacted]/stream",
		"/v1/public/polls/tok456":             "/v1/public/polls/[redacted]",
		"/v1/public/polls/tok456/votes":       "/v1/public/polls/[redacted]/votes",
		"/v1/mail/contacts/ada@example.com":   "/v1/mail/contacts/[redacted]",
		"/v1/invitations/accept":              "/v1/invitations/accept",
		"/v1/mail/threads/t1":                 "/v1/mail/threads/t1",
		"/nope":                               "/nope",
	}
	for in, want := range tests {
		if got := redactPath(in); got != want {
			t.Errorf("redactPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLogRedactsTokensOn404(t *testing.T) {
	var buf bytes.Buffer
	h := newHarness(t)
	h.deps.Logger = slog.New(slog.NewJSONHandler(&buf, nil))
	rec := h.anon(http.MethodGet, "/v1/shared/threads/secret-share-token/unknown", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if strings.Contains(buf.String(), "secret-share-token") {
		t.Fatalf("share token leaked into logs: %s", buf.String())
	}
	if line := httpLine(t, &buf); line["route"] != "/v1/shared/threads/[redacted]/unknown" {
		t.Fatalf("route = %v", line["route"])
	}
}

func TestInvitationTokenNeverLogged(t *testing.T) {
	var buf bytes.Buffer
	h := newHarness(t)
	h.deps.Logger = slog.New(slog.NewJSONHandler(&buf, nil))
	rec := h.authed(http.MethodPost, "/v1/invitations/accept", strings.NewReader(`{"token":"invite-secret-token"}`))
	if rec.Code == http.StatusInternalServerError {
		t.Fatalf("unexpected 500: %s", rec.Body.String())
	}
	if strings.Contains(buf.String(), "invite-secret-token") {
		t.Fatalf("invitation token leaked into logs: %s", buf.String())
	}
}

func TestWriteErrorLogsRouteAndRequestID(t *testing.T) {
	var buf bytes.Buffer
	h := newHarness(t)
	h.deps.Logger = slog.New(slog.NewJSONHandler(&buf, nil))
	h.mail.getThreadErr = domainNotFound()
	rec := h.authed(http.MethodGet, "/v1/mail/threads/t-123", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var found bool
	for _, m := range jsonLogLines(t, &buf) {
		if m["msg"] == "request rejected" {
			found = true
			if m["route"] != "/v1/mail/threads/{id}" || m["request_id"] != rec.Header().Get("X-Request-Id") {
				t.Fatalf("rejection line = %v", m)
			}
			if _, ok := m["path"]; ok {
				t.Fatalf("rejection line still logs the raw path: %v", m)
			}
		}
	}
	if !found {
		t.Fatal("no 'request rejected' line")
	}
}
```

Add this helper at the bottom of `logging_test.go` (keeps the import list honest):

```go
func domainNotFound() error { return fmt.Errorf("thread: %w", domain.ErrNotFound) }
```

and add `"fmt"` and `"calendium/backend/internal/domain"` to its imports.

- [ ] **Step 3: Run the tests:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/adapter/in/httpapi/ -run 'TestNewRequestID|TestRequestID|TestErrorEnvelope|TestLog|TestRedactPath|TestInvitationToken|TestWriteErrorLogs' 2>&1 | head -20
```
Expected: build failure `undefined: newRequestID` / `undefined: redactPath` / `e.RequestID undefined`.

- [ ] **Step 4: Create `backend/internal/adapter/in/httpapi/requestid.go`:**

```go
package httpapi

import (
	"context"
	"crypto/rand"
	"net/http"
	"regexp"
	"time"
)

// requestIDRe bounds an inbound X-Request-Id (only accepted from a trusted
// proxy): safe characters, 8–128 long, so it can never inject log lines.
var requestIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{8,128}$`)

type requestIDKey struct{}

// requestIDFrom returns the id placed in the context by requestID; "" when
// the middleware is not in the chain (bare handler unit tests).
func requestIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey{}).(string)
	return v
}

// crockford is the ULID alphabet (no I, L, O, U).
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// newRequestID returns a 26-char ULID-style id: 48-bit millisecond
// timestamp followed by 80 bits from crypto/rand, Crockford base32.
func newRequestID(now time.Time) string {
	var b [16]byte
	ms := uint64(now.UnixMilli())
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	if _, err := rand.Read(b[6:]); err != nil {
		panic("httpapi: crypto/rand unavailable: " + err.Error())
	}
	return encodeULID(b)
}

// encodeULID renders 128 big-endian bits as 26 base32 symbols, least
// significant symbol last (the first symbol only carries 3 bits).
func encodeULID(b [16]byte) string {
	var out [26]byte
	acc, bits, j := uint64(0), uint(0), 25
	for i := 15; i >= 0; i-- {
		acc |= uint64(b[i]) << bits
		bits += 8
		for bits >= 5 && j >= 0 {
			out[j] = crockford[acc&31]
			acc >>= 5
			bits -= 5
			j--
		}
	}
	for j >= 0 {
		out[j] = crockford[acc&31]
		acc >>= 5
		j--
	}
	return string(out[:])
}

// requestID assigns every request an id: the inbound X-Request-Id when the
// peer is a trusted proxy and the value matches requestIDRe, else a fresh
// ULID. The id is echoed on the response and stored in the context for the
// logger and the error envelope.
func (s *server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := ""
		if s.proxy.peerTrusted(r) {
			if v := r.Header.Get("X-Request-Id"); requestIDRe.MatchString(v) {
				id = v
			}
		}
		if id == "" {
			id = newRequestID(time.Now())
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}
```

- [ ] **Step 5: Rework `middleware.go`.** Replace the `statusRecorder` type and its two methods (L63-80) and `logRequests` (L82-95) with:

```go
// responseWriter is the ONE wrapper every middleware uses: it records the
// status and byte count, forwards Flush for the SSE streams, and exposes
// Unwrap so http.ResponseController keeps working through the stack.
type responseWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
	wrote  bool
}

func (w *responseWriter) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.wrote = true
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	w.wrote = true
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

// Flush forwards http.Flusher so streaming handlers keep working.
func (w *responseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// logFields is the mutable per-request record requireAuth/withActAs fill
// in; the outer logger cannot see values stored in inner contexts, so the
// pointer is placed in the context before the handler runs.
type logFields struct {
	UserID  string
	ActorID string
}

type logFieldsKey struct{}

// setLogUser records the effective user (principal under act-as) and the
// real actor for the request log line. No-op without logRequests.
func setLogUser(ctx context.Context, userID, actorID string) {
	if f, ok := ctx.Value(logFieldsKey{}).(*logFields); ok {
		f.UserID = userID
		if actorID != "" {
			f.ActorID = actorID
		}
	}
}

// redactPath replaces the secret-bearing segment of the tokenized public
// routes (and the contact email) with [redacted]; used only when no route
// pattern matched (404/405), since patterns never carry the token.
func redactPath(path string) string {
	for _, prefix := range []string{"/v1/shared/threads/", "/v1/public/polls/", "/v1/mail/contacts/"} {
		rest, ok := strings.CutPrefix(path, prefix)
		if !ok {
			continue
		}
		if _, tail, hasTail := strings.Cut(rest, "/"); hasTail {
			return prefix + "[redacted]/" + tail
		}
		return prefix + "[redacted]"
	}
	return path
}

// routeOf is the loggable route: the matched ServeMux pattern without its
// method ("/v1/mail/threads/{id}"), or the redacted raw path when nothing
// matched. Query strings are never included.
func routeOf(r *http.Request) string {
	if r.Pattern != "" {
		if _, route, ok := strings.Cut(r.Pattern, " "); ok {
			return route
		}
		return r.Pattern
	}
	return redactPath(r.URL.Path)
}

func (s *server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		fields := &logFields{}
		// This pointer is the one ServeMux mutates (r.Pattern), so keep it.
		r = r.WithContext(context.WithValue(r.Context(), logFieldsKey{}, fields))
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		s.deps.Logger.Info("http",
			"request_id", requestIDFrom(r.Context()),
			"method", r.Method,
			"route", routeOf(r),
			"status", rw.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"bytes", rw.bytes,
			"client_ip", s.clientIP(r),
			"user_id", fields.UserID,
			"actor_id", fields.ActorID,
		)
	})
}
```

In `requireAuth` (L24-50): replace the inline 401 envelope with

```go
			writeJSON(w, http.StatusUnauthorized, errorBody{Error: errorDetail{
				Code:      "unauthorized",
				Message:   "invalid or expired access token",
				RequestID: requestIDFrom(r.Context()),
			}})
```

and after `user, err := s.deps.Users.EnsureUser(...)` succeeds add `setLogUser(r.Context(), user.ID, "")` before building `ctx`.

In `recoverPanics` (L103-122) change the log call to

```go
			s.deps.Logger.Error("panic recovered",
				"method", r.Method, "route", routeOf(r), "request_id", requestIDFrom(r.Context()),
				"panic", rec, "stack", string(debug.Stack()))
			writeJSON(w, http.StatusInternalServerError, errorBody{Error: errorDetail{
				Code:      "internal",
				Message:   "internal server error",
				RequestID: requestIDFrom(r.Context()),
			}})
```

- [ ] **Step 6: Update `delegation.go`.** After `ctx = context.WithValue(ctx, actorCtxKey{}, assistant)` (L151) add `setLogUser(ctx, principal.ID, assistant.ID)`. Replace `rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}` (L164) with `rec := &responseWriter{ResponseWriter: w, status: http.StatusOK}`.

- [ ] **Step 7: Update `codec.go`.** Replace `errorDetail`:

```go
type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// RequestID is the X-Request-Id of the failed request so users can quote
	// it to support; absent when the middleware is not in the chain.
	RequestID string `json:"requestId,omitempty"`
	// Details carries machine-readable context for specific codes: the
	// field limits (Task 7) fill {"field","limit"}; billing's 402 uses the
	// same slot.
	Details map[string]any `json:"details,omitempty"`
}
```

Replace `writeError`:

```go
// writeError renders the `{ "error": { code, message, requestId } }`
// envelope with a stable per-code message. Every failure is logged
// server-side with full detail (route pattern + request id, never the raw
// path or query); the wrapped error text is never echoed to clients.
func (s *server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := statusFor(err)
	if status >= http.StatusInternalServerError {
		s.deps.Logger.Error("request failed",
			"method", r.Method, "route", routeOf(r), "request_id", requestIDFrom(r.Context()),
			"status", status, "error", err)
	} else {
		s.deps.Logger.Info("request rejected",
			"method", r.Method, "route", routeOf(r), "request_id", requestIDFrom(r.Context()),
			"status", status, "code", code, "error", err)
	}
	writeJSON(w, status, errorBody{Error: errorDetail{
		Code: code, Message: safeMessage(code), RequestID: requestIDFrom(r.Context()),
	}})
}
```

- [ ] **Step 8: Insert the middleware** in `New` (`httpapi.go` tail): the chain becomes

```go
	var h http.Handler = mux
	h = corsMiddleware(h, deps.CORSAllowedOrigins)
	h = s.logRequests(h)
	h = s.requestID(h)
	h = s.recoverPanics(h)
	return h
```

- [ ] **Step 9: Run the package tests:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium/backend && go vet ./internal/adapter/in/httpapi/ && go test ./internal/adapter/in/httpapi/ 2>&1 | tail -5
```
Expected: `ok`. (`TestRecoverPanics` and `TestRequireAuth` still pass: the envelope only gained optional fields.)

- [ ] **Step 10: Commit:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && git add backend/internal/adapter/in/httpapi && git commit -m "feat(httpapi): request ids, unified responseWriter and redacted structured request logs" -m "ULID X-Request-Id (inbound only from trusted proxies), error.requestId, route-pattern logging with [redacted] tokens, user_id/actor_id via a context-held logFields record." -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 3: Per-user rate limiting with classes and `Retry-After` (Track A)

**Files:**
- Modify: `backend/internal/adapter/in/httpapi/ratelimit.go` (whole file), `backend/internal/adapter/in/httpapi/ratelimit_test.go` (L10-54, L116-147), `backend/internal/adapter/in/httpapi/httpapi.go` (Deps, `New` L98-130 public limiters, L132-135 `authed`, route table), `backend/internal/adapter/in/httpapi/scheduling_handlers_test.go` (L305-307 `Retry-After` assertion)
- Test: `backend/internal/adapter/in/httpapi/ratelimit_user_test.go` (new)

**Interfaces:**
- Consumes: `s.clientIP`, `requestIDFrom`, `userFrom`, `delegHarness`/`actAs` (delegation_test.go).
- Produces: `type RateLimits struct{ PublicReadPerMin, PublicWritePerMin, UserPerMin, MutateHeavyPerMin, SearchPerMin int }`; `func DefaultRateLimits() RateLimits`; `Deps.RateLimits RateLimits`; `func (l *rateLimiter) allow(key string) (bool, time.Duration)`; `func newClassLimiter(perMin, burst int) *rateLimiter`; constants `classUser = "user"`, `classMutateHeavy = "mutate_heavy"`, `classSearch = "search"`; `type routeOption func(*routeOptions)`; `func limitClass(name string) routeOption`; `func (s *server) userLimited(l *rateLimiter, next http.HandlerFunc) http.HandlerFunc`; `func (s *server) writeRateLimited(w, r, retry time.Duration)`; `server.limits map[string]*rateLimiter`; `authed(pattern string, h http.HandlerFunc, opts ...routeOption)`.

- [ ] **Step 1: Rewrite the limiter unit tests** in `ratelimit_test.go`. Replace `TestRateLimiterBurstThenDeny` and `TestRateLimiterRefillAfterTime` with:

```go
func TestRateLimiterBurstThenDenyWithRetryAfter(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	l := newRateLimiter(5, 3, clock) // 5/min = one token every 12s

	for i := 0; i < 3; i++ {
		if ok, _ := l.allow("k"); !ok {
			t.Fatalf("expected call %d within burst to be allowed", i+1)
		}
	}
	ok, retry := l.allow("k")
	if ok {
		t.Fatal("expected call beyond burst to be denied")
	}
	if retry != 12*time.Second {
		t.Fatalf("retryAfter = %v, want 12s (ceil((1-0)/5 min))", retry)
	}
	now = now.Add(6 * time.Second) // half a token refilled → 6s left, still denied
	if ok, retry := l.allow("k"); ok || retry != 6*time.Second {
		t.Fatalf("after 6s: ok=%v retry=%v, want denied / 6s", ok, retry)
	}
}

func TestRateLimiterRetryAfterMinimumOneSecond(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	l := newRateLimiter(600, 1, clock) // 10 tokens/s
	l.allow("k")
	if ok, retry := l.allow("k"); ok || retry != time.Second {
		t.Fatalf("ok=%v retry=%v, want denied / 1s floor", ok, retry)
	}
}

func TestRateLimiterRefillAfterTime(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	l := newRateLimiter(60, 2, clock) // 60/min = 1/sec

	if ok, _ := l.allow("k"); !ok {
		t.Fatal("expected first burst token")
	}
	if ok, _ := l.allow("k"); !ok {
		t.Fatal("expected second burst token")
	}
	if ok, _ := l.allow("k"); ok {
		t.Fatal("expected bucket to be empty")
	}
	now = now.Add(time.Minute)
	if ok, _ := l.allow("k"); !ok {
		t.Fatal("expected token after refill")
	}
	if ok, _ := l.allow("k"); !ok {
		t.Fatal("expected second refilled token")
	}
	if ok, _ := l.allow("k"); ok {
		t.Fatal("expected bucket empty again")
	}
}
```

In `TestRateLimiterKeyIsolation` and `TestRateLimiterGCPrunesIdleBuckets` change every `l.allow(x)` boolean use to `ok, _ := l.allow(x)` form (e.g. `if ok, _ := l.allow("ip-a"); !ok {`). In `TestRateLimitedHandler` replace `s := &server{}` with `s := newHarness(t).server()` and the `Retry-After` assertion with:

```go
	if got := rec.Header().Get("Retry-After"); got != "12" {
		t.Fatalf("expected Retry-After: 12 (5/min, empty bucket), got %q", got)
	}
```

In `scheduling_handlers_test.go` `TestPublicRoutes_RateLimited429` (L305-307) replace the `"60"` assertion with the same `"12"` expectation (public write is 5/min).

- [ ] **Step 2: Write the per-user tests** in `backend/internal/adapter/in/httpapi/ratelimit_user_test.go`:

```go
package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"calendium/backend/internal/port"
)

func TestDefaultRateLimits(t *testing.T) {
	d := DefaultRateLimits()
	if d != (RateLimits{PublicReadPerMin: 60, PublicWritePerMin: 5, UserPerMin: 600, MutateHeavyPerMin: 30, SearchPerMin: 120}) {
		t.Fatalf("DefaultRateLimits() = %+v", d)
	}
}

// TestMutateHeavy31stCallIs429 drives POST /v1/mail/threads/zero (class
// mutate_heavy, 30/min burst 30) through one handler instance.
func TestMutateHeavy31stCallIs429(t *testing.T) {
	h := newHarness(t)
	handler := h.handler()
	body := `{"olderThan":"2026-01-01T00:00:00Z"}`
	for i := 0; i < 30; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/mail/threads/zero", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+defaultToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d: status = %d (body=%s)", i+1, rec.Code, rec.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/mail/threads/zero", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+defaultToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("31st call: status = %d, want 429", rec.Code)
	}
	retry, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || retry < 1 {
		t.Fatalf("Retry-After = %q, want an integer >= 1", rec.Header().Get("Retry-After"))
	}
	e := decodeErr(t, rec)
	if e.Code != "rate_limited" || e.RequestID == "" {
		t.Fatalf("envelope = %+v", e)
	}
	// The general user class is untouched by the mutate_heavy bucket.
	req = httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+defaultToken)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/me after mutate_heavy exhaustion: %d", rec.Code)
	}
}

func TestUserLimitsAreIsolatedPerUser(t *testing.T) {
	h := newHarness(t)
	h.deps.RateLimits = RateLimits{PublicReadPerMin: 60, PublicWritePerMin: 5, UserPerMin: 2, MutateHeavyPerMin: 30, SearchPerMin: 120}
	h.verifier.tokens["other-token"] = port.Identity{Subject: "user_2", Email: "other@example.com"}
	handler := h.handler()
	call := func(token string) int {
		req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}
	call(defaultToken)
	call(defaultToken)
	if got := call(defaultToken); got != http.StatusTooManyRequests {
		t.Fatalf("3rd call for user_1 = %d, want 429", got)
	}
	// user_2 shares the EnsureUser fake, so swap the returned user for isolation.
	h.users.ensureRet.ID = "user_2"
	if got := call("other-token"); got != http.StatusOK {
		t.Fatalf("user_2's first call = %d, want 200 (own bucket)", got)
	}
}

func TestSearchClass121stCallIs429(t *testing.T) {
	h := newHarness(t)
	handler := h.handler()
	for i := 0; i < 120; i++ {
		req := httptest.NewRequest(http.MethodGet, "/v1/search?q=x", nil)
		req.Header.Set("Authorization", "Bearer "+defaultToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d: %d (body=%s)", i+1, rec.Code, rec.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/search?q=x", nil)
	req.Header.Set("Authorization", "Bearer "+defaultToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("121st search = %d, want 429", rec.Code)
	}
}

// TestActorChargedUnderActAs: 30 delegated bulk-actions (mutate_heavy) by
// the assistant exhaust the ASSISTANT's bucket — its own 31st direct call
// is refused — proving the principal is never the limiter key.
func TestActorChargedUnderActAs(t *testing.T) {
	h, _ := delegHarness(t)
	handler := h.handler()
	body := `{"threadIds":["t1"],"action":"archive"}`
	for i := 0; i < 30; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/mail/threads/bulk-actions", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+defaultToken)
		req.Header.Set(actAsHeader, "principal_1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("delegated call %d: %d (body=%s)", i+1, rec.Code, rec.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/mail/threads/bulk-actions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+defaultToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("assistant's own 31st call = %d, want 429 (actor is charged)", rec.Code)
	}
}

func TestZeroDisablesClass(t *testing.T) {
	h := newHarness(t)
	h.deps.RateLimits = RateLimits{PublicReadPerMin: 60, PublicWritePerMin: 5, UserPerMin: 0, MutateHeavyPerMin: 0, SearchPerMin: 120}
	handler := h.handler()
	for i := 0; i < 700; i++ {
		req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
		req.Header.Set("Authorization", "Bearer "+defaultToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d with user class disabled: %d", i+1, rec.Code)
		}
	}
}

func TestPublicLimitsUnchanged(t *testing.T) {
	h := newHarness(t)
	handler := h.handler()
	for i := 0; i < 30; i++ {
		req := httptest.NewRequest(http.MethodGet, "/v1/public/booking/demo", nil)
		req.RemoteAddr = "198.51.100.1:1"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("public read call %d of burst 30 was limited", i+1)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/public/booking/demo", nil)
	req.RemoteAddr = "198.51.100.1:1"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("31st public read = %d, want 429", rec.Code)
	}
}
```

- [ ] **Step 3: Run the tests:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/adapter/in/httpapi/ -run 'TestRateLimiter|TestRateLimited|TestDefaultRateLimits|TestMutateHeavy|TestUserLimits|TestSearchClass|TestActorCharged|TestZeroDisables|TestPublicLimits' 2>&1 | head -20
```
Expected: build failure `assignment mismatch: ... l.allow returns 1 value` / `undefined: RateLimits`.

- [ ] **Step 4: Rewrite `backend/internal/adapter/in/httpapi/ratelimit.go`:**

```go
package httpapi

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RateLimits is the per-class budget (requests per minute); burst equals
// the per-minute value except publicRead (half). A zero value for the whole
// struct means DefaultRateLimits; a zero for ONE class disables that class
// (tests only). Limits are per process: a multi-replica deployment
// multiplies them by replica count (upgrades.md assumes one api replica).
type RateLimits struct {
	PublicReadPerMin  int // per IP: public GETs + share routes
	PublicWritePerMin int // per IP: public POSTs
	UserPerMin        int // per user: every authed route not in a class
	MutateHeavyPerMin int // per user: token-minting / fan-out mutations
	SearchPerMin      int // per user: search-shaped reads
}

// DefaultRateLimits mirrors the spec table (and the RATE_LIMIT_* defaults).
func DefaultRateLimits() RateLimits {
	return RateLimits{PublicReadPerMin: 60, PublicWritePerMin: 5, UserPerMin: 600, MutateHeavyPerMin: 30, SearchPerMin: 120}
}

// Limiter class names used by limitClass on the route table.
const (
	classUser        = "user"
	classMutateHeavy = "mutate_heavy"
	classSearch      = "search"
)

// rateLimiter is an in-memory per-key token bucket: capacity `burst`,
// refilled at `perMin` tokens/minute.
type rateLimiter struct {
	mu      sync.Mutex
	perMin  float64
	burst   float64
	now     func() time.Time
	buckets map[string]*bucket
	lastGC  time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(perMin, burst int, now func() time.Time) *rateLimiter {
	return &rateLimiter{
		perMin: float64(perMin), burst: float64(burst),
		now: now, buckets: make(map[string]*bucket),
	}
}

// newClassLimiter builds a production limiter, or nil (class disabled)
// when perMin is zero.
func newClassLimiter(perMin, burst int) *rateLimiter {
	if perMin <= 0 {
		return nil
	}
	if burst < 1 {
		burst = 1
	}
	return newRateLimiter(perMin, burst, time.Now)
}

// allow consumes one token for key. When refused it also returns how long
// until one token is available: ceil((1 − tokens) / perMin minutes), at
// least one second — the Retry-After value.
func (l *rateLimiter) allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.now()
	// Opportunistic GC: drop buckets idle > 10 minutes, at most once a minute.
	if t.Sub(l.lastGC) > time.Minute {
		for k, b := range l.buckets {
			if t.Sub(b.last) > 10*time.Minute {
				delete(l.buckets, k)
			}
		}
		l.lastGC = t
	}
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: t}
		l.buckets[key] = b
	}
	b.tokens = min(l.burst, b.tokens+t.Sub(b.last).Minutes()*l.perMin)
	b.last = t
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	seconds := math.Ceil((1 - b.tokens) / l.perMin * 60)
	if seconds < 1 {
		seconds = 1
	}
	return false, time.Duration(seconds) * time.Second
}

// writeRateLimited renders the 429 envelope with Retry-After in whole seconds.
func (s *server) writeRateLimited(w http.ResponseWriter, r *http.Request, retry time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retry.Seconds()))))
	writeJSON(w, http.StatusTooManyRequests, errorBody{Error: errorDetail{
		Code: "rate_limited", Message: safeMessage("rate_limited"), RequestID: requestIDFrom(r.Context()),
	}})
}

// rateLimited wraps a public handler with a per-client-IP bucket; a nil
// limiter (class disabled) passes through.
func (s *server) rateLimited(l *rateLimiter, next http.HandlerFunc) http.HandlerFunc {
	if l == nil {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if ok, retry := l.allow("ip:" + s.clientIP(r)); !ok {
			s.writeRateLimited(w, r, retry)
			return
		}
		next(w, r)
	}
}

// userLimited wraps an authed handler with a per-user bucket keyed on the
// AUTHENTICATED actor (it runs before withActAs swaps in the principal).
func (s *server) userLimited(l *rateLimiter, next http.HandlerFunc) http.HandlerFunc {
	if l == nil {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if ok, retry := l.allow("user:" + userFrom(r).ID); !ok {
			s.writeRateLimited(w, r, retry)
			return
		}
		next(w, r)
	}
}

// routeOptions tunes one authed route; see limitClass (and Task 5's
// deadline/noDeadline).
type routeOptions struct {
	class string
}

type routeOption func(*routeOptions)

// limitClass charges the route to the named per-user class instead of
// the general user bucket.
func limitClass(name string) routeOption {
	return func(o *routeOptions) { o.class = name }
}
```

- [ ] **Step 5: Wire `New`.** In `httpapi.go` add `RateLimits RateLimits` to `Deps` (after `TrustedProxyCIDRs`, with the comment `// RateLimits is the per-class budget; zero means DefaultRateLimits.`), add `limits map[string]*rateLimiter` to the `server` struct, and in `newServer` after constructing `s`:

```go
	rl := deps.RateLimits
	if rl == (RateLimits{}) {
		rl = DefaultRateLimits()
	}
	s.limits = map[string]*rateLimiter{
		classUser:        newClassLimiter(rl.UserPerMin, rl.UserPerMin),
		classMutateHeavy: newClassLimiter(rl.MutateHeavyPerMin, rl.MutateHeavyPerMin),
		classSearch:      newClassLimiter(rl.SearchPerMin, rl.SearchPerMin),
	}
	s.publicRead = newClassLimiter(rl.PublicReadPerMin, rl.PublicReadPerMin/2)
	s.publicWrite = newClassLimiter(rl.PublicWritePerMin, rl.PublicWritePerMin)
```

(add `publicRead, publicWrite *rateLimiter` to the `server` struct). In `New` delete the two `newRateLimiter(...)` lines and use `s.publicRead` / `s.publicWrite` in the seven `s.rateLimited(...)` registrations. Replace `authed`:

```go
	// Authenticated surface. Chain: requireAuth → userLimited(class) →
	// withActAs → handler, so the ACTOR is charged, never the principal.
	authed := func(pattern string, h http.HandlerFunc, opts ...routeOption) {
		o := routeOptions{class: classUser}
		for _, opt := range opts {
			opt(&o)
		}
		inner := s.withActAs(h)
		inner = s.userLimited(s.limits[o.class], inner)
		mux.Handle(pattern, s.requireAuth(inner))
	}
```

Then tag the classed routes:

```go
	authed("POST /v1/mail/threads/bulk-actions", s.handleBulkThreadActions, limitClass(classMutateHeavy))
	authed("POST /v1/mail/threads/zero", s.handleGetMeToZero, limitClass(classMutateHeavy))
	authed("POST /v1/mail/drafts/{id}/send", s.handleSendDraft, limitClass(classMutateHeavy))
	authed("GET /v1/mail/attachments", s.handleSearchAttachments, limitClass(classSearch))
	authed("POST /v1/mail/threads/{id}/share", s.handleShareThread, limitClass(classMutateHeavy))
	authed("POST /v1/calendar-subscriptions", s.handleCreateCalendarSubscription, limitClass(classMutateHeavy))
	authed("GET /v1/search", s.handleSearch, limitClass(classSearch))
	authed("POST /v1/ai/compose", s.handleAiCompose, limitClass(classMutateHeavy))
	authed("POST /v1/ai/ask", s.handleAiAsk, limitClass(classMutateHeavy))
	authed("POST /v1/ai/event-proposal", s.handleAiEventProposal, limitClass(classMutateHeavy))
	authed("POST /v1/booking-links", s.handleCreateLink, limitClass(classMutateHeavy))
	authed("POST /v1/polls", s.handleCreatePoll, limitClass(classMutateHeavy))
	authed("POST /v1/teams/{id}/invitations", s.handleInvite, limitClass(classMutateHeavy))
	authed("GET /v1/places/autocomplete", s.handlePlacesAutocomplete, limitClass(classSearch))
```

(each replaces the existing untagged registration of the same pattern; all other `authed(...)` calls stay as they are).

- [ ] **Step 6: Run the package tests:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium/backend && go vet ./internal/adapter/in/httpapi/ && go test ./internal/adapter/in/httpapi/ 2>&1 | tail -5
```
Expected: `ok`.

- [ ] **Step 7: Commit:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && git add backend/internal/adapter/in/httpapi && git commit -m "feat(httpapi): per-user rate limiting with mutate_heavy/search classes and computed Retry-After" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 4: API security headers (Track A)

**Files:**
- Create: `backend/internal/adapter/in/httpapi/headers.go`, `backend/internal/adapter/in/httpapi/headers_test.go`
- Modify: `backend/internal/adapter/in/httpapi/httpapi.go` (`New` tail), `backend/internal/adapter/in/httpapi/stream.go` (L73 `Cache-Control`), `backend/internal/adapter/in/httpapi/threadshare.go` (L119 `Cache-Control`)

**Interfaces:**
- Consumes: `s.proxy.forwardedProto` (Task 1).
- Produces: `func (s *server) securityHeaders(next http.Handler) http.Handler`.

- [ ] **Step 1: Write the failing tests** in `headers_test.go`:

```go
package httpapi

import (
	"bufio"
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func assertStaticHeaders(t *testing.T, h http.Header) {
	t.Helper()
	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
		"X-Frame-Options":        "DENY",
	}
	for k, v := range want {
		if got := h.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	t.Run("json route under /v1 gets no-store", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodGet, "/v1/me", nil)
		assertStaticHeaders(t, rec.Header())
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("Cache-Control = %q, want no-store", got)
		}
		if rec.Header().Get("Strict-Transport-Security") != "" {
			t.Fatal("HSTS must not be set on a plain-HTTP request")
		}
	})
	t.Run("404 outside /v1 has static headers, no cache directive", func(t *testing.T) {
		h := newHarness(t)
		rec := h.anon(http.MethodGet, "/nope", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d", rec.Code)
		}
		assertStaticHeaders(t, rec.Header())
		if rec.Header().Get("Cache-Control") != "" {
			t.Fatalf("Cache-Control = %q on /nope, want empty", rec.Header().Get("Cache-Control"))
		}
	})
	t.Run("CORS preflight carries the headers", func(t *testing.T) {
		h := newHarness(t)
		req := httptest.NewRequest(http.MethodOptions, "/v1/me", nil)
		req.Header.Set("Origin", "http://localhost:3000")
		req.Header.Set("Access-Control-Request-Method", "GET")
		rec := httptest.NewRecorder()
		h.handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
		assertStaticHeaders(t, rec.Header())
	})
	t.Run("HSTS on TLS", func(t *testing.T) {
		h := newHarness(t)
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.TLS = &tls.ConnectionState{}
		rec := httptest.NewRecorder()
		h.handler().ServeHTTP(rec, req)
		if got := rec.Header().Get("Strict-Transport-Security"); got != "max-age=31536000; includeSubDomains" {
			t.Fatalf("HSTS = %q", got)
		}
	})
	t.Run("HSTS from trusted X-Forwarded-Proto https only", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			trust bool
			peer  string
			want  bool
		}{
			{"trusted proxy", true, "10.0.0.2:1", true},
			{"untrusted peer", false, "10.0.0.2:1", false},
			{"trust on, peer outside CIDRs", true, "203.0.113.5:1", false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				h := newHarness(t)
				h.deps.TrustProxy = tc.trust
				h.deps.TrustedProxyCIDRs = prefixes(t, "10.0.0.0/8")
				req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
				req.RemoteAddr = tc.peer
				req.Header.Set("X-Forwarded-Proto", "https")
				rec := httptest.NewRecorder()
				h.handler().ServeHTTP(rec, req)
				if got := rec.Header().Get("Strict-Transport-Security") != ""; got != tc.want {
					t.Fatalf("HSTS present = %v, want %v", got, tc.want)
				}
			})
		}
	})
}

func TestSSEStreamIsNoStore(t *testing.T) {
	h := newHarness(t)
	srv := httptest.NewServer(h.handler())
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/collab/stream", nil)
	req.Header.Set("Authorization", "Bearer "+defaultToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := resp.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("X-Accel-Buffering = %q, want no", got)
	}
	assertStaticHeaders(t, resp.Header)
	line, _ := bufio.NewReader(resp.Body).ReadString('\n')
	if !strings.HasPrefix(line, ": connected") {
		t.Fatalf("first frame = %q", line)
	}
}
```

- [ ] **Step 2: Run:** `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/adapter/in/httpapi/ -run 'TestSecurityHeaders|TestSSEStreamIsNoStore' 2>&1 | head` — expected: FAIL (`X-Content-Type-Options = ""`).

- [ ] **Step 3: Create `headers.go`:**

```go
package httpapi

import (
	"net/http"
	"strings"
)

// securityHeaders sets the static API headers on EVERY response (including
// CORS preflights and 404s), Cache-Control: no-store under /v1 (tokens and
// mail must never land in a shared cache), and HSTS only when the request
// is known to be HTTPS: direct TLS or X-Forwarded-Proto from a trusted
// proxy. It sits directly inside recoverPanics so a panic response carries
// the headers too.
func (s *server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		if r.URL.Path == "/v1" || strings.HasPrefix(r.URL.Path, "/v1/") {
			h.Set("Cache-Control", "no-store")
		}
		if r.TLS != nil || s.proxy.forwardedProto(r) == "https" {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 4: Chain it** in `New` (`httpapi.go` tail):

```go
	var h http.Handler = mux
	h = corsMiddleware(h, deps.CORSAllowedOrigins)
	h = s.logRequests(h)
	h = s.requestID(h)
	h = s.securityHeaders(h)
	h = s.recoverPanics(h)
	return h
```

and in `stream.go` L73 and `threadshare.go` L119 change `h.Set("Cache-Control", "no-cache")` to `h.Set("Cache-Control", "no-store")`.

- [ ] **Step 5: Run the package tests:** `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/adapter/in/httpapi/ 2>&1 | tail -3` — expected `ok`.

- [ ] **Step 6: Commit:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && git add backend/internal/adapter/in/httpapi && git commit -m "feat(httpapi): security headers, no-store under /v1 and conditional HSTS" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 5: Per-handler deadlines (Track A)

**Files:**
- Create: `backend/internal/adapter/in/httpapi/deadline.go`, `backend/internal/adapter/in/httpapi/deadline_test.go`
- Modify: `backend/internal/adapter/in/httpapi/ratelimit.go` (`routeOptions`, add `deadline`/`noDeadline`), `backend/internal/adapter/in/httpapi/httpapi.go` (`authed` chain; attachment + stream registrations), `backend/internal/adapter/in/httpapi/codec.go` (`safeMessage`), `backend/internal/adapter/in/httpapi/harness_test.go` (`fakeUserService`, `fakeMailService`)

**Interfaces:**
- Consumes: `responseWriter`, `requestIDFrom`, `routeOptions`.
- Produces: `var defaultHandlerDeadline = 60 * time.Second`; `var attachmentHandlerDeadline = 10 * time.Minute`; `func (s *server) withDeadline(d time.Duration, next http.HandlerFunc) http.HandlerFunc`; `func deadline(d time.Duration) routeOption`; `func noDeadline() routeOption`; `safeMessage("timeout")`; test hooks `fakeUserService.prefsBlock func(context.Context)` and `fakeMailService.attachmentBlock func(context.Context)`.

- [ ] **Step 1: Add the blocking hooks to the harness.** In `harness_test.go` add `prefsBlock func(context.Context)` to `fakeUserService` and make `GetPreferences` start with `if f.prefsBlock != nil { f.prefsBlock(ctx) }`; add `attachmentBlock func(context.Context)` to `fakeMailService` and make its `GetAttachmentContent` start with `if f.attachmentBlock != nil { f.attachmentBlock(ctx) }`.

- [ ] **Step 2: Write the failing tests** in `deadline_test.go`:

```go
package httpapi

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func shrinkDeadlines(t *testing.T, handler, attachment time.Duration) {
	t.Helper()
	prevH, prevA := defaultHandlerDeadline, attachmentHandlerDeadline
	defaultHandlerDeadline, attachmentHandlerDeadline = handler, attachment
	t.Cleanup(func() { defaultHandlerDeadline, attachmentHandlerDeadline = prevH, prevA })
}

func TestDeadlineReturns504WhenHandlerExceeds(t *testing.T) {
	shrinkDeadlines(t, 50*time.Millisecond, time.Second)
	h := newHarness(t)
	h.users.prefsBlock = func(ctx context.Context) { <-ctx.Done() }
	rec := h.authed(http.MethodGet, "/v1/me/preferences", nil)
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504 (body=%s)", rec.Code, rec.Body.String())
	}
	e := decodeErr(t, rec)
	if e.Code != "timeout" || e.RequestID == "" {
		t.Fatalf("envelope = %+v", e)
	}
}

// Review Focus 2: a handler that already streamed part of a 200 is left alone.
func TestDeadlineDoesNotOverwritePartialResponse(t *testing.T) {
	h := newHarness(t)
	s := h.server()
	wrapped := s.withDeadline(30*time.Millisecond, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		<-r.Context().Done()
		_, _ = w.Write([]byte("-tail"))
	})
	rec := httptest.NewRecorder()
	wrapped(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "partial-tail" {
		t.Fatalf("status=%d body=%q, want 200 partial-tail", rec.Code, rec.Body.String())
	}
}

func TestAttachmentRouteUsesLongDeadline(t *testing.T) {
	shrinkDeadlines(t, 20*time.Millisecond, 500*time.Millisecond)
	h := newHarness(t)
	h.mail.attachmentBlock = func(ctx context.Context) {
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond): // longer than the general deadline, shorter than the attachment one
		}
	}
	h.mail.getAttachmentContentData = []byte("%PDF")
	rec := h.authed(http.MethodGet, "/v1/mail/attachments/a1/content", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (attachment deadline is the long one)", rec.Code)
	}
}

func TestStreamRouteHasNoDeadline(t *testing.T) {
	shrinkDeadlines(t, 20*time.Millisecond, 20*time.Millisecond)
	prev := keepaliveInterval
	keepaliveInterval = 40 * time.Millisecond
	t.Cleanup(func() { keepaliveInterval = prev })
	h := newHarness(t)
	srv := httptest.NewServer(h.handler())
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/collab/stream", nil)
	req.Header.Set("Authorization", "Bearer "+defaultToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	reader := bufio.NewReader(resp.Body)
	var keepalives int
	for keepalives < 3 {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("stream ended after %d keepalives (deadline applied?): %v", keepalives, err)
		}
		if strings.HasPrefix(line, ": keepalive") {
			keepalives++
		}
	}
}
```

- [ ] **Step 3: Run:** `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/adapter/in/httpapi/ -run 'TestDeadline|TestAttachmentRouteUsesLongDeadline|TestStreamRouteHasNoDeadline' 2>&1 | head` — expected: build failure `undefined: defaultHandlerDeadline`.

- [ ] **Step 4: Create `deadline.go`:**

```go
package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Per-handler context deadlines. Vars (not consts) so tests can shrink them.
var (
	// defaultHandlerDeadline bounds every authed non-stream route.
	defaultHandlerDeadline = 60 * time.Second
	// attachmentHandlerDeadline covers GET /v1/mail/attachments/{id}/content,
	// which proxies provider downloads.
	attachmentHandlerDeadline = 10 * time.Minute
)

// withDeadline puts a timeout on the request context and, when the handler
// returns having written NOTHING after the deadline passed, answers 504
// timeout. Unlike http.TimeoutHandler it never buffers, so Flusher and
// attachment streaming keep working; a partially written response is left
// exactly as the handler produced it.
func (s *server) withDeadline(d time.Duration, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), d)
		defer cancel()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next(rw, r.WithContext(ctx))
		if !rw.wrote && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			writeJSON(rw, http.StatusGatewayTimeout, errorBody{Error: errorDetail{
				Code: "timeout", Message: safeMessage("timeout"), RequestID: requestIDFrom(r.Context()),
			}})
		}
	}
}
```

- [ ] **Step 5: Extend `routeOptions`** in `ratelimit.go`:

```go
type routeOptions struct {
	class      string
	deadline   time.Duration
	noDeadline bool
}

// deadline overrides the per-handler context deadline for one route.
func deadline(d time.Duration) routeOption {
	return func(o *routeOptions) { o.deadline = d }
}

// noDeadline marks a streaming route (SSE) that must run until the client
// disconnects or the server drains.
func noDeadline() routeOption {
	return func(o *routeOptions) { o.noDeadline = true }
}
```

and update `authed` in `New`:

```go
	// Chain: requireAuth → userLimited(class) → withActAs → deadline → h.
	authed := func(pattern string, h http.HandlerFunc, opts ...routeOption) {
		o := routeOptions{class: classUser, deadline: defaultHandlerDeadline}
		for _, opt := range opts {
			opt(&o)
		}
		inner := h
		if !o.noDeadline {
			inner = s.withDeadline(o.deadline, inner)
		}
		inner = s.withActAs(inner)
		inner = s.userLimited(s.limits[o.class], inner)
		mux.Handle(pattern, s.requireAuth(inner))
	}
```

Then tag two routes:

```go
	authed("GET /v1/mail/attachments/{id}/content", s.handleGetAttachmentContent, deadline(attachmentHandlerDeadline))
	authed("GET /v1/collab/stream", s.handleCollabStream, noDeadline())
```

- [ ] **Step 6: Add the message** to `safeMessage` in `codec.go`:

```go
	case "timeout":
		return "The request took too long to complete. Please try again."
```

and the row `{"timeout", "The request took too long to complete. Please try again."},` to `TestSafeMessage` in `codec_test.go`.

- [ ] **Step 7: Run the package tests:** `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/adapter/in/httpapi/ 2>&1 | tail -3` — expected `ok`.

- [ ] **Step 8: Commit:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && git add backend/internal/adapter/in/httpapi && git commit -m "feat(httpapi): 60s per-handler deadlines with 504 timeout, 10min for attachments, none for SSE" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 6: Body limits and 413 `payload_too_large` (Track A)

**Files:**
- Modify: `backend/internal/adapter/in/httpapi/codec.go` (L12-13, L30-38, `statusFor`, `safeMessage`), `backend/internal/adapter/in/httpapi/mail.go` (L247-249 `handleCreateDraft`, L269-271 `handleUpdateDraft`), `backend/internal/adapter/in/httpapi/scheduling.go` (L24-43 `decodePublicJSON`), `backend/internal/adapter/in/httpapi/billing.go` (L57-62 webhook read), `backend/internal/adapter/in/httpapi/scheduling_handlers_test.go` (L202-204 code assertion), `backend/internal/adapter/in/httpapi/codec_test.go` (tables)
- Test: `backend/internal/adapter/in/httpapi/bodylimit_test.go` (new)

**Interfaces:**
- Produces: `const maxBodyBytes = 1 << 20`; `const maxDraftBodyBytes = 10 << 20`; `var errPayloadTooLarge = errors.New("payload too large")`; `func decodeJSONLimit(w http.ResponseWriter, r *http.Request, dst any, n int64) error`; `statusFor(errPayloadTooLarge) == (413, "payload_too_large")`; `safeMessage("payload_too_large")`.

- [ ] **Step 1: Write the failing tests** in `bodylimit_test.go`:

```go
package httpapi

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// jsonOfSize returns a syntactically valid JSON object whose encoded size is
// exactly n bytes: {"pad":"xxxx..."}.
func jsonOfSize(n int) string {
	const frame = `{"pad":""}`
	return `{"pad":"` + strings.Repeat("x", n-len(frame)) + `"}`
}

func TestBodyLimits(t *testing.T) {
	t.Run("1 MiB + 1 on a default route is 413", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPut, "/v1/prefs", strings.NewReader(jsonOfSize(maxBodyBytes+1)))
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413 (body=%s)", rec.Code, rec.Body.String())
		}
		if e := decodeErr(t, rec); e.Code != "payload_too_large" {
			t.Fatalf("code = %q", e.Code)
		}
	})
	t.Run("exactly 1 MiB on a default route is accepted by the decoder", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPut, "/v1/prefs", strings.NewReader(jsonOfSize(maxBodyBytes)))
		if rec.Code == http.StatusRequestEntityTooLarge {
			t.Fatalf("a body of exactly maxBodyBytes must not be 413 (body=%s)", rec.Body.String())
		}
	})
	t.Run("draft create accepts 2 MiB", func(t *testing.T) {
		h := newHarness(t)
		body := fmt.Sprintf(`{"accountId":"a1","subject":"hi","bodyHtml":%q}`, strings.Repeat("y", 2<<20))
		rec := h.authed(http.MethodPost, "/v1/mail/drafts", strings.NewReader(body))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
	})
	t.Run("draft update rejects 10 MiB + 1", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPut, "/v1/mail/drafts/d1", strings.NewReader(jsonOfSize(maxDraftBodyBytes+1)))
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413", rec.Code)
		}
	})
	t.Run("public booking over 16 KiB is 413 payload_too_large", func(t *testing.T) {
		h := newHarness(t)
		rec := h.anon(http.MethodPost, "/v1/public/booking/demo/bookings", strings.NewReader(jsonOfSize(publicBodyLimit+1)))
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413", rec.Code)
		}
		if e := decodeErr(t, rec); e.Code != "payload_too_large" {
			t.Fatalf("code = %q, want payload_too_large", e.Code)
		}
	})
	t.Run("webhook over 1 MiB is 413", func(t *testing.T) {
		h := newHarness(t)
		rec := h.anon(http.MethodPost, "/v1/webhooks/stripe", bytes.NewReader(bytes.Repeat([]byte("z"), (1<<20)+1)))
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.billing.webhookCalls != 0 {
			t.Fatal("oversized webhook must not reach Billing")
		}
	})
}
```

If piece 1 has already replaced `/v1/webhooks/stripe` with `/v1/webhooks/paddle`, point the last sub-test at the Paddle route and its fake counter instead.

- [ ] **Step 2: Run:** `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/adapter/in/httpapi/ -run TestBodyLimits 2>&1 | head` — expected: build failure `undefined: maxDraftBodyBytes` (then FAIL on `payload_too_large`).

- [ ] **Step 3: Update `codec.go`.** Replace the body-cap constant and `decodeJSON`:

```go
// Body caps. The default covers every JSON route; drafts (inline images)
// get decodeJSONLimit with maxDraftBodyBytes; the public scheduling POSTs
// use publicBodyLimit (scheduling.go) and the webhook its own 1 MiB.
const (
	maxBodyBytes      = 1 << 20
	maxDraftBodyBytes = 10 << 20
)

// errPayloadTooLarge marks a body over its route cap; statusFor maps it to
// 413 payload_too_large everywhere (JSON, public, webhook).
var errPayloadTooLarge = errors.New("payload too large")

// decodeJSON reads a bounded JSON body into dst; malformed input becomes a
// domain validation error (HTTP 400), an oversized body errPayloadTooLarge.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	return decodeJSONLimit(w, r, dst, maxBodyBytes)
}

// decodeJSONLimit is decodeJSON with an explicit cap.
func decodeJSONLimit(w http.ResponseWriter, r *http.Request, dst any, n int64) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, n))
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return fmt.Errorf("%w: body exceeds %d bytes", errPayloadTooLarge, n)
		}
		return fmt.Errorf("%w: invalid JSON body: %v", domain.ErrValidation, err)
	}
	return nil
}
```

In `statusFor` add as the FIRST case `case errors.Is(err, errPayloadTooLarge): return http.StatusRequestEntityTooLarge, "payload_too_large"`; in `safeMessage` add `case "payload_too_large": return "The request body is too large."`. Add the rows `{"payload too large", fmt.Errorf("x: %w", errPayloadTooLarge), http.StatusRequestEntityTooLarge, "payload_too_large"},` to `TestStatusFor` and `{"payload_too_large", "The request body is too large."},` to `TestSafeMessage`.

- [ ] **Step 4: Switch the special routes.** `mail.go`: in `handleCreateDraft` and `handleUpdateDraft` replace `decodeJSON(w, r, &in)` with `decodeJSONLimit(w, r, &in, maxDraftBodyBytes)`. `scheduling.go`: replace the body of `decodePublicJSON` with

```go
func (s *server) decodePublicJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := decodeJSONLimit(w, r, dst, publicBodyLimit); err != nil {
		s.writeError(w, r, err)
		return false
	}
	return true
}
```

(drop the now-unused `"encoding/json"` and `"errors"` imports if `go build` reports them). `billing.go` webhook read:

```go
	payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			s.writeError(w, r, fmt.Errorf("%w: webhook body exceeds 1 MiB", errPayloadTooLarge))
			return
		}
		s.writeError(w, r, domain.ErrValidation)
		return
	}
```

(add `"errors"` and `"fmt"` to `billing.go` imports). In `scheduling_handlers_test.go` L202-204 change the expected code from `request_too_large` to `payload_too_large`.

- [ ] **Step 5: Run the package tests:** `cd /Users/guilherme/Dev/pessoal/calendium/backend && go vet ./internal/adapter/in/httpapi/ && go test ./internal/adapter/in/httpapi/ 2>&1 | tail -3` — expected `ok`.

- [ ] **Step 6: Commit:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && git add backend/internal/adapter/in/httpapi && git commit -m "feat(httpapi): 1 MiB default body cap, 10 MiB for drafts, uniform 413 payload_too_large" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 7: Field limits — collector, envelope details, mail/accounts/misc handlers (Track A)

**Files:**
- Create: `backend/internal/adapter/in/httpapi/limits.go`, `backend/internal/adapter/in/httpapi/limits_test.go`
- Modify: `backend/internal/adapter/in/httpapi/codec.go` (`writeError`), `backend/internal/adapter/in/httpapi/accounts.go`, `backend/internal/adapter/in/httpapi/integrations.go`, `backend/internal/adapter/in/httpapi/billing.go`, `backend/internal/adapter/in/httpapi/mail.go`, `backend/internal/adapter/in/httpapi/misc.go`, `backend/internal/adapter/in/httpapi/collab.go`, `backend/internal/adapter/in/httpapi/crm.go`, `backend/internal/adapter/in/httpapi/classifier_handlers.go`, `backend/internal/adapter/in/httpapi/tasks.go`, `backend/internal/adapter/in/httpapi/subscriptions.go`, `backend/internal/adapter/in/httpapi/delegation.go` (`handleCreateDelegation`)

**Interfaces:**
- Produces: constants `maxTitleRunes = 500`, `maxTextBytes = 64 << 10`, `maxEmailRunes = 320`, `maxURLRunes = 2048`, `maxArrayItems = 500`; `type limitError struct{ field string; limit int }` (`Error()`, `Is(domain.ErrValidation)`); `type fieldCheck struct{ first error }` with methods `title(field, v string)`, `text(field, v string)`, `email(field, v string)`, `url(field, v string)`, `list(field string, n int)`, `emails(field string, vs []string)`, `urls(field string, vs []string)`, `optTitle(field string, v *string)`, `optText(field string, v *string)`, `err() error`; `writeError` fills `Details{"field","limit"}`.

- [ ] **Step 1: Write the failing unit tests** in `limits_test.go`:

```go
package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"calendium/backend/internal/domain"
)

func TestFieldCheckCollector(t *testing.T) {
	var fc fieldCheck
	fc.title("title", strings.Repeat("a", 500))
	fc.text("notes", strings.Repeat("b", 64<<10))
	fc.email("email", strings.Repeat("c", 320))
	fc.url("url", strings.Repeat("d", 2048))
	fc.list("items", 500)
	if err := fc.err(); err != nil {
		t.Fatalf("values at the limit must pass: %v", err)
	}
	fc.title("title", strings.Repeat("a", 501))
	fc.text("notes", strings.Repeat("b", (64<<10)+1)) // second failure is ignored: first wins
	err := fc.err()
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
	if err.Error() != "validation failed: title exceeds 500" {
		t.Fatalf("err text = %q", err.Error())
	}
	var le *limitError
	if !errors.As(err, &le) || le.field != "title" || le.limit != 500 {
		t.Fatalf("limitError = %+v", le)
	}
}

// Review Focus 3: titles/emails/urls count runes, text counts bytes.
func TestFieldCheckRunesVersusBytes(t *testing.T) {
	var fc fieldCheck
	fc.title("title", strings.Repeat("é", 500)) // 500 runes, 1000 bytes
	if err := fc.err(); err != nil {
		t.Fatalf("500 multi-byte runes must pass: %v", err)
	}
	fc.text("notes", strings.Repeat("é", 32768)) // 32768 runes, 65536 bytes = limit
	if err := fc.err(); err != nil {
		t.Fatalf("exactly 64 KiB must pass: %v", err)
	}
	fc.text("notes", strings.Repeat("é", 32769)) // 65538 bytes
	if err := fc.err(); err == nil {
		t.Fatal("64 KiB + 2 bytes of 2-byte runes must fail (bytes, not runes)")
	}
}

func TestFieldLimitsViaHandlersMailAccountsMisc(t *testing.T) {
	long := func(n int) string { return strings.Repeat("x", n) }
	emails := func(n int) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = `"a@example.com"`
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	tests := []struct {
		name      string
		method    string
		target    string
		body      string
		wantField string
		wantLimit float64
	}{
		{"draft subject 501", http.MethodPost, "/v1/mail/drafts", `{"accountId":"a1","subject":"` + long(501) + `"}`, "subject", 500},
		{"draft to 501 recipients", http.MethodPost, "/v1/mail/drafts", `{"accountId":"a1","to":` + strings.Replace(emails(501), `"a@example.com"`, `{"email":"a@example.com"}`, -1) + `}`, "to", 500},
		{"draft cc email 321", http.MethodPut, "/v1/mail/drafts/d1", `{"accountId":"a1","cc":[{"email":"` + long(321) + `"}]}`, "cc", 320},
		{"snippet name 501", http.MethodPost, "/v1/mail/snippets", `{"name":"` + long(501) + `","bodyHtml":"<p>x</p>"}`, "name", 500},
		{"snippet body 64KiB+1", http.MethodPut, "/v1/mail/snippets/s1", `{"name":"n","bodyHtml":"` + long((64<<10)+1) + `"}`, "bodyHtml", 65536},
		{"bulk threadIds 501", http.MethodPost, "/v1/mail/threads/bulk-actions", `{"threadIds":` + emails(501) + `,"action":"archive"}`, "threadIds", 500},
		{"label id 501", http.MethodPost, "/v1/mail/threads/t1/labels", `{"labelId":"` + long(501) + `","add":true}`, "labelId", 500},
		{"reaction emoji 501", http.MethodPost, "/v1/mail/messages/m1/reactions", `{"emoji":"` + long(501) + `"}`, "emoji", 500},
		{"vip senders 501", http.MethodPut, "/v1/accounts/a1/vip-senders", `{"vipSenders":` + emails(501) + `}`, "vipSenders", 500},
		{"auto bcc email 321", http.MethodPut, "/v1/accounts/a1/auto-bcc", `{"autoBcc":["` + long(321) + `"]}`, "autoBcc", 320},
		{"signature 64KiB+1", http.MethodPut, "/v1/accounts/a1/signature", `{"signatureHtml":"` + long((64<<10)+1) + `"}`, "signatureHtml", 65536},
		{"connect redirectUrl 2049", http.MethodPost, "/v1/accounts/connect/google", `{"redirectUrl":"https://" + long(2049) + `"}`, "redirectUrl", 2048},
		{"integration redirectUrl 2049", http.MethodPost, "/v1/integrations/connect/todoist", `{"redirectUrl":"` + long(2049) + `"}`, "redirectUrl", 2048},
		{"ai compose prompt 64KiB+1", http.MethodPost, "/v1/ai/compose", `{"action":"write","prompt":"` + long((64<<10)+1) + `"}`, "prompt", 65536},
		{"ai ask question 64KiB+1", http.MethodPost, "/v1/ai/ask", `{"question":"` + long((64<<10)+1) + `"}`, "question", 65536},
		{"device token 501", http.MethodPost, "/v1/devices", `{"platform":"web","token":"` + long(501) + `"}`, "token", 500},
		{"comment body 64KiB+1", http.MethodPost, "/v1/mail/threads/t1/comments", `{"teamId":"team_1","body":"` + long((64<<10)+1) + `"}`, "body", 65536},
		{"crm log contactEmail 321", http.MethodPost, "/v1/crm/log", `{"contactEmail":"` + long(321) + `","subject":"s"}`, "contactEmail", 320},
		{"classifier prompt 64KiB+1", http.MethodPost, "/v1/classifiers", `{"name":"n","prompt":"` + long((64<<10)+1) + `"}`, "prompt", 65536},
		{"task title 501", http.MethodPost, "/v1/tasks", `{"title":"` + long(501) + `"}`, "title", 500},
		{"task patch notes 64KiB+1", http.MethodPatch, "/v1/tasks/t1", `{"notes":"` + long((64<<10)+1) + `"}`, "notes", 65536},
		{"subscription url 2049", http.MethodPost, "/v1/calendar-subscriptions", `{"url":"https://" + long(2049) + `"}`, "url", 2048},
		{"delegation scopes 501", http.MethodPost, "/v1/delegations", `{"assistantEmail":"a@example.com","scopes":` + emails(501) + `}`, "scopes", 500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.deps.Integrations = &fakeIntegrationService{}
			h.deps.Collab = &fakeCollabService{}
			h.deps.Crm = &fakeCrmService{}
			h.deps.Delegations = &fakeDelegationService{}
			rec := h.authed(tt.method, tt.target, strings.NewReader(tt.body))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
			}
			e := decodeErr(t, rec)
			if e.Code != "validation_failed" || e.Details["field"] != tt.wantField || e.Details["limit"] != tt.wantLimit {
				t.Fatalf("envelope = %+v, want validation_failed field=%s limit=%v", e, tt.wantField, tt.wantLimit)
			}
		})
	}
}
```

The fakes already exist in the package: `fakeIntegrationService` (`integrations_handlers_test.go`), `fakeCollabService` (`threadshare_handlers_test.go`), `fakeCrmService` (`crm_handlers_test.go`), `fakeDelegationService` (`delegation_test.go`).

- [ ] **Step 2: Run:** `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/adapter/in/httpapi/ -run 'TestFieldCheck|TestFieldLimitsViaHandlersMailAccountsMisc' 2>&1 | head` — expected: build failure `undefined: fieldCheck`.

- [ ] **Step 3: Create `limits.go`:**

```go
package httpapi

import (
	"fmt"
	"unicode/utf8"

	"calendium/backend/internal/domain"
)

// Handler-layer field limits (spec decision 6). Titles/names/subjects/
// slugs/labels count runes; long text counts bytes; arrays count items.
// Stricter domain limits (team name 120, comment body) stay authoritative
// because they run after these in the service.
const (
	maxTitleRunes = 500
	maxTextBytes  = 64 << 10
	maxEmailRunes = 320
	maxURLRunes   = 2048
	maxArrayItems = 500
)

// limitError is the ErrValidation-shaped failure writeError turns into
// 400 validation_failed with details {"field","limit"}.
type limitError struct {
	field string
	limit int
}

func (e *limitError) Error() string {
	return fmt.Sprintf("%v: %s exceeds %d", domain.ErrValidation, e.field, e.limit)
}

// Is makes errors.Is(err, domain.ErrValidation) true.
func (e *limitError) Is(target error) bool { return target == domain.ErrValidation }

// fieldCheck collects the FIRST limit violation of a decoded payload.
type fieldCheck struct{ first error }

func (c *fieldCheck) fail(field string, limit int) {
	if c.first == nil {
		c.first = &limitError{field: field, limit: limit}
	}
}

func (c *fieldCheck) title(field, v string) {
	if utf8.RuneCountInString(v) > maxTitleRunes {
		c.fail(field, maxTitleRunes)
	}
}

func (c *fieldCheck) text(field, v string) {
	if len(v) > maxTextBytes {
		c.fail(field, maxTextBytes)
	}
}

func (c *fieldCheck) email(field, v string) {
	if utf8.RuneCountInString(v) > maxEmailRunes {
		c.fail(field, maxEmailRunes)
	}
}

func (c *fieldCheck) url(field, v string) {
	if utf8.RuneCountInString(v) > maxURLRunes {
		c.fail(field, maxURLRunes)
	}
}

func (c *fieldCheck) list(field string, n int) {
	if n > maxArrayItems {
		c.fail(field, maxArrayItems)
	}
}

// emails checks the count and every element of an address list.
func (c *fieldCheck) emails(field string, vs []string) {
	c.list(field, len(vs))
	for _, v := range vs {
		c.email(field, v)
	}
}

// urls checks the count and every element of a URL list.
func (c *fieldCheck) urls(field string, vs []string) {
	c.list(field, len(vs))
	for _, v := range vs {
		c.url(field, v)
	}
}

func (c *fieldCheck) optTitle(field string, v *string) {
	if v != nil {
		c.title(field, *v)
	}
}

func (c *fieldCheck) optText(field string, v *string) {
	if v != nil {
		c.text(field, *v)
	}
}

// err returns the first violation, or nil.
func (c *fieldCheck) err() error { return c.first }
```

- [ ] **Step 4: Fill `Details` in `writeError`** (`codec.go`): after `status, code := statusFor(err)` build the body and attach the limit:

```go
	body := errorBody{Error: errorDetail{Code: code, Message: safeMessage(code), RequestID: requestIDFrom(r.Context())}}
	var le *limitError
	if errors.As(err, &le) {
		body.Error.Details = map[string]any{"field": le.field, "limit": le.limit}
	}
	// ...existing logging...
	writeJSON(w, status, body)
```

- [ ] **Step 5: Apply the checks.** In every handler below insert the block right after the `decodeJSON` error check (before the service call). Pattern:

```go
	var fc fieldCheck
	// per-handler lines
	if err := fc.err(); err != nil {
		s.writeError(w, r, err)
		return
	}
```

Per-handler lines:

`accounts.go`
- `handleConnectAccount`: `fc.url("redirectUrl", in.RedirectURL)`
- `handleSetVipSenders`: `fc.emails("vipSenders", in.VipSenders)`
- `handleSetSignature`: `fc.text("signatureHtml", in.SignatureHTML)`
- `handleSetAutoBcc`: `fc.emails("autoBcc", in.AutoBcc)`

`integrations.go`
- `handleConnectIntegration`: `fc.url("redirectUrl", in.RedirectURL)`

`billing.go` (skip if piece 1 already removed the client-supplied URLs)
- `handleCreateCheckout`: `fc.url("successUrl", in.SuccessURL)`, `fc.url("cancelUrl", in.CancelURL)`
- `handleCreatePortal`: `fc.url("returnUrl", in.ReturnURL)`

`mail.go`
- `handleBulkThreadActions`: `fc.list("threadIds", len(in.ThreadIDs))`, `fc.title("labelId", in.LabelID)`
- `handleSetThreadLabel`: `fc.title("labelId", in.LabelID)`
- `handleCreateDraft` and `handleUpdateDraft` (add a shared helper in `mail.go`):

```go
// checkDraftInput applies the field limits to a draft payload. bodyHtml is
// deliberately exempt: it carries inline images and is bounded by the
// 10 MiB route cap instead of the 64 KiB text limit.
func checkDraftInput(in port.DraftInput) error {
	var fc fieldCheck
	fc.title("subject", in.Subject)
	for field, list := range map[string][]domain.EmailAddress{"to": in.To, "cc": in.Cc, "bcc": in.Bcc} {
		fc.list(field, len(list))
		for _, a := range list {
			fc.email(field, a.Email)
		}
	}
	return fc.err()
}
```

  and in both handlers after decoding: `if err := checkDraftInput(in); err != nil { s.writeError(w, r, err); return }`.
- `handleCreateSnippet` and `handleUpdateSnippet` (before `sanitizeSignatureHTML`): `fc.title("name", in.Name)`, `fc.optTitle("shortcut", in.Shortcut)`, `fc.text("bodyHtml", in.BodyHTML)`
- `handleReactToMessage`: `fc.title("emoji", in.Emoji)`

`misc.go`
- `handleAiCompose`: `fc.text("prompt", req.Prompt)`, `fc.title("tone", req.Tone)`
- `handleAiAsk`: `fc.text("question", req.Question)`
- `handleRegisterDevice`: `fc.title("platform", in.Platform)`, `fc.title("token", in.Token)`
- `handleUpdatePrefs` (`prefs.go`): `fc.list("splitOrder", len(in.SplitOrder))`

`collab.go`
- `handleAddComment`: `fc.title("teamId", in.TeamID)`, `fc.text("body", in.Body)`
- `handleUpdateComment`: `fc.text("body", in.Body)`

`crm.go`
- `handleCrmLog`: `fc.email("contactEmail", in.ContactEmail)`, `fc.title("subject", in.Subject)`, `fc.text("bodyText", in.BodyText)`

`classifier_handlers.go`
- `handleCreateClassifier` and `handleUpdateClassifier`: `fc.title("name", in.Name)`, `fc.text("prompt", in.Prompt)`, `fc.title("labelName", in.LabelName)`

`tasks.go`
- `handleCreateTask`: `fc.title("title", in.Title)`, `fc.text("notes", in.Notes)`
- `handleUpdateTask` (after `decodeTaskPatch`): `fc.optTitle("title", patch.Title)` and `if patch.Notes != nil { fc.optText("notes", *patch.Notes) }`

`subscriptions.go`
- `handleCreateCalendarSubscription`: `fc.url("url", in.URL)`, `fc.title("name", in.Name)`, `fc.title("color", in.Color)`
- `handleUpdateCalendarSubscription`: `fc.optTitle("name", patch.Name)`, `fc.optTitle("color", patch.Color)`

`delegation.go`
- `handleCreateDelegation`: `fc.email("assistantEmail", in.AssistantEmail)`, `fc.list("scopes", len(in.Scopes))` (before the scope parsing loop)

- [ ] **Step 6: Run the package tests:** `cd /Users/guilherme/Dev/pessoal/calendium/backend && go vet ./internal/adapter/in/httpapi/ && go test ./internal/adapter/in/httpapi/ 2>&1 | tail -3` — expected `ok`.

- [ ] **Step 7: Commit:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && git add backend/internal/adapter/in/httpapi && git commit -m "feat(httpapi): handler-layer field limits with details{field,limit} on mail, accounts and misc routes" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 8: Field limits — calendar, scheduling, teams, prefs (Track A)

**Files:**
- Modify: `backend/internal/adapter/in/httpapi/calendar.go`, `backend/internal/adapter/in/httpapi/scheduling.go`, `backend/internal/adapter/in/httpapi/teams.go`, `backend/internal/adapter/in/httpapi/prefs.go` (`handleUpdateCalendarPrefs`), `backend/internal/adapter/in/httpapi/calendar_shares.go`
- Test: `backend/internal/adapter/in/httpapi/limits_calendar_test.go` (new)

**Interfaces:**
- Consumes: `fieldCheck` (Task 7).

- [ ] **Step 1: Write the failing table test** in `limits_calendar_test.go`:

```go
package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestFieldLimitsViaHandlersCalendarScheduling(t *testing.T) {
	long := func(n int) string { return strings.Repeat("x", n) }
	strs := func(n int) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = `"a@example.com"`
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	window := `{"weekday":1,"start":"09:00","end":"17:00"}`
	windows := func(n int) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = window
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	tests := []struct {
		name      string
		method    string
		target    string
		body      string
		wantField string
		wantLimit float64
	}{
		{"event title 501", http.MethodPost, "/v1/events", `{"calendarId":"c1","title":"` + long(501) + `","start":"2026-01-01T10:00:00Z","end":"2026-01-01T11:00:00Z"}`, "title", 500},
		{"event description 64KiB+1", http.MethodPost, "/v1/events", `{"calendarId":"c1","title":"t","description":"` + long((64<<10)+1) + `","start":"2026-01-01T10:00:00Z","end":"2026-01-01T11:00:00Z"}`, "description", 65536},
		{"event attendees 501", http.MethodPost, "/v1/events", `{"calendarId":"c1","title":"t","attendeeEmails":` + strs(501) + `,"start":"2026-01-01T10:00:00Z","end":"2026-01-01T11:00:00Z"}`, "attendeeEmails", 500},
		{"event patch location 501", http.MethodPatch, "/v1/events/e1", `{"location":"` + long(501) + `"}`, "location", 500},
		{"rsvp comment 64KiB+1", http.MethodPost, "/v1/events/e1/rsvp", `{"response":"accepted","comment":"` + long((64<<10)+1) + `"}`, "comment", 65536},
		{"event note links 501", http.MethodPut, "/v1/events/e1/note", `{"bodyMd":"x","links":` + strs(501) + `}`, "links", 500},
		{"event note link 2049", http.MethodPut, "/v1/events/e1/note", `{"bodyMd":"x","links":["https://" + long(2049) + `"]}`, "links", 2048},
		{"template name 501", http.MethodPost, "/v1/event-templates", `{"name":"` + long(501) + `","title":"t","durationMinutes":30}`, "name", 500},
		{"calendar set name 501", http.MethodPost, "/v1/calendar-sets", `{"name":"` + long(501) + `","calendarIds":[]}`, "name", 500},
		{"calendar set ids 501", http.MethodPut, "/v1/calendar-sets/s1", `{"name":"n","calendarIds":` + strs(501) + `}`, "calendarIds", 500},
		{"booking link title 501", http.MethodPost, "/v1/booking-links", `{"slug":"s","title":"` + long(501) + `","calendarId":"c1","durationMinutes":30,"timeZone":"UTC","windows":[]}`, "title", 500},
		{"booking link windows 501", http.MethodPut, "/v1/booking-links/l1", `{"slug":"s","title":"t","calendarId":"c1","durationMinutes":30,"timeZone":"UTC","windows":` + windows(501) + `}`, "windows", 500},
		{"poll description 64KiB+1", http.MethodPost, "/v1/polls", `{"title":"t","description":"` + long((64<<10)+1) + `","calendarId":"c1","durationMinutes":30,"options":[]}`, "description", 65536},
		{"proposal note 64KiB+1", http.MethodPost, "/v1/events/e1/propose-time", `{"start":"2026-01-01T10:00:00Z","end":"2026-01-01T11:00:00Z","note":"` + long((64<<10)+1) + `"}`, "note", 65536},
		{"freebusy email 321", http.MethodPost, "/v1/freebusy", `{"emails":["` + long(321) + `"],"from":"2026-01-01T00:00:00Z","to":"2026-01-02T00:00:00Z"}`, "emails", 320},
		{"settings workingLocation 501", http.MethodPut, "/v1/settings", `{"timeZone":"UTC","workingHours":[],"workingLocation":"` + long(501) + `"}`, "workingLocation", 500},
		{"team name 501", http.MethodPost, "/v1/teams", `{"name":"` + long(501) + `"}`, "name", 500},
		{"invite email 321", http.MethodPost, "/v1/teams/team_1/invitations", `{"email":"` + long(321) + `","role":"member"}`, "email", 320},
		{"calendar prefs decline message 64KiB+1", http.MethodPatch, "/v1/prefs/calendar", `{"focusDeclineMessage":"` + long((64<<10)+1) + `"}`, "focusDeclineMessage", 65536},
		{"calendar share permission 501", http.MethodPost, "/v1/calendars/c1/shares", `{"granteeUserId":"u2","permission":"` + long(501) + `"}`, "permission", 500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.deps.CalendarShares = &fakeCalendarSharingService{}
			rec := h.authed(tt.method, tt.target, strings.NewReader(tt.body))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
			}
			e := decodeErr(t, rec)
			if e.Code != "validation_failed" || e.Details["field"] != tt.wantField || e.Details["limit"] != tt.wantLimit {
				t.Fatalf("envelope = %+v, want validation_failed field=%s limit=%v", e, tt.wantField, tt.wantLimit)
			}
		})
	}
}

func TestPublicFieldLimits(t *testing.T) {
	long := func(n int) string { return strings.Repeat("x", n) }
	tests := []struct {
		name      string
		target    string
		body      string
		wantField string
	}{
		{"booking inviteeEmail 321", "/v1/public/booking/demo/bookings", `{"start":"2026-01-01T10:00:00Z","inviteeName":"n","inviteeEmail":"` + long(321) + `","inviteeTimeZone":"UTC"}`, "inviteeEmail"},
		{"booking inviteeName 501", "/v1/public/booking/demo/bookings", `{"start":"2026-01-01T10:00:00Z","inviteeName":"` + long(501) + `","inviteeEmail":"a@example.com","inviteeTimeZone":"UTC"}`, "inviteeName"},
		{"ballot voterName 501", "/v1/public/polls/tok/votes", `{"voterEmail":"a@example.com","voterName":"` + long(501) + `","choices":{}}`, "voterName"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			rec := h.anon(http.MethodPost, tt.target, strings.NewReader(tt.body))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
			}
			if e := decodeErr(t, rec); e.Details["field"] != tt.wantField {
				t.Fatalf("details = %v, want field %s", e.Details, tt.wantField)
			}
		})
	}
}
```

(`fakeCalendarSharingService` lives in `calendar_shares_handlers_test.go`.)

- [ ] **Step 2: Run:** `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/adapter/in/httpapi/ -run 'TestFieldLimitsViaHandlersCalendarScheduling|TestPublicFieldLimits' 2>&1 | head` — expected FAIL (`status = 200, want 400`).

- [ ] **Step 3: Apply the checks** (same insertion pattern as Task 7, after the decode error check):

`calendar.go`
- `handleCreateEvent`: `fc.title("title", in.Title)`, `fc.text("description", in.Description)`, `fc.title("location", in.Location)`, `fc.title("recurrenceRule", in.RecurrenceRule)`, `fc.emails("attendeeEmails", in.AttendeeEmails)`, `fc.list("reminderMinutes", len(in.ReminderMinutes))`
- `handleUpdateEvent`: `fc.optTitle("title", patch.Title)`, `fc.optText("description", patch.Description)`, `fc.optTitle("location", patch.Location)`, `fc.optTitle("recurrenceRule", patch.RecurrenceRule)`, `if patch.AttendeeEmails != nil { fc.emails("attendeeEmails", *patch.AttendeeEmails) }`, `if patch.ReminderMinutes != nil { fc.list("reminderMinutes", len(*patch.ReminderMinutes)) }`
- `handleRsvp`: `fc.text("comment", in.Comment)`
- `handlePutEventNote`: `fc.text("bodyMd", in.BodyMD)`, `fc.urls("links", in.Links)`
- `handleCreateEventTemplate` and `handleUpdateEventTemplate`: `fc.title("name", in.Name)`, `fc.title("title", in.Title)`, `fc.text("description", in.Description)`, `fc.title("location", in.Location)`, `fc.emails("attendeeEmails", in.AttendeeEmails)`, `fc.list("reminderMinutes", len(in.ReminderMinutes))`, `fc.optTitle("recurrenceRule", in.RecurrenceRule)`
- `handleCreateCalendarSet` and `handleUpdateCalendarSet`: `fc.title("name", in.Name)`, `fc.list("calendarIds", len(in.CalendarIDs))`
- `handleUpdateCalendar`: `fc.optTitle("color", patch.Color)`

`scheduling.go`
- `handlePublicBook` (after `decodePublicJSON`): `fc.title("inviteeName", req.InviteeName)`, `fc.email("inviteeEmail", req.InviteeEmail)`, `fc.title("inviteeTimeZone", req.InviteeTZ)`, `fc.text("note", req.Note)`
- `handlePublicPollVote`: `fc.email("voterEmail", ballot.VoterEmail)`, `fc.title("voterName", ballot.VoterName)`, `fc.list("choices", len(ballot.Choices))`
- `handleCreateLink` and `handleUpdateLink`: `fc.title("slug", in.Slug)`, `fc.title("title", in.Title)`, `fc.text("description", in.Description)`, `fc.title("timeZone", in.TimeZone)`, `fc.list("windows", len(in.Windows))`, `fc.list("memberUserIds", len(in.MemberUserIDs))`
- `handleCreatePoll`: `fc.title("title", in.Title)`, `fc.text("description", in.Description)`, `fc.list("options", len(in.Options))`
- `handleConfirmPoll`: `fc.title("optionId", in.OptionID)`
- `handleProposeTime`: `fc.text("note", in.Note)`
- `handleGuestFreeBusy` (before the existing count check): `fc.emails("emails", req.Emails)`
- `handleUpdateSettings`: `fc.title("timeZone", in.TimeZone)`, `fc.list("workingHours", len(in.WorkingHours))`, `fc.title("workingLocation", in.WorkingLocation)`

`teams.go`
- `handleCreateTeam` and `handleRenameTeam`: `fc.title("name", in.Name)`
- `handleSetMemberRole`: `fc.title("role", in.Role)`
- `handleInvite`: `fc.email("email", in.Email)`, `fc.title("role", in.Role)`
- `handleAcceptInvitation`: `fc.title("token", in.Token)`

`prefs.go`
- `handleUpdateCalendarPrefs`: `fc.optTitle("timeZone", patch.TimeZone)`, `if patch.WorkDays != nil { fc.list("workDays", len(*patch.WorkDays)) }`, `fc.optText("focusDeclineMessage", patch.FocusDeclineMessage)`, `fc.optText("oooDeclineMessage", patch.OOODeclineMessage)`

`calendar_shares.go`
- `handleShareCalendar`: `fc.title("granteeUserId", in.GranteeUserID)`, `fc.title("granteeTeamId", in.GranteeTeamID)`, `fc.title("permission", in.Permission)`
- `handleUpdateCalendarShare`: `fc.title("permission", in.Permission)`

- [ ] **Step 4: Run the package tests:** `cd /Users/guilherme/Dev/pessoal/calendium/backend && go vet ./internal/adapter/in/httpapi/ && go test ./internal/adapter/in/httpapi/ 2>&1 | tail -3` — expected `ok` (existing handler tests send short payloads).

- [ ] **Step 5: Commit:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && git add backend/internal/adapter/in/httpapi && git commit -m "feat(httpapi): field limits on calendar, scheduling, team, prefs and sharing routes" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 9: `domain.ErrUpstream` → 502 `upstream_unavailable` (Track A)

**Files:**
- Modify: `backend/internal/domain/errors.go` (sentinel list), `backend/internal/adapter/in/httpapi/codec.go` (`statusFor`, `safeMessage`), `backend/internal/adapter/in/httpapi/codec_test.go` (tables), `backend/internal/adapter/in/httpapi/middleware.go` (`requireAuth`), `backend/internal/adapter/in/httpapi/middleware_test.go` (`TestRequireAuth` table), `backend/internal/adapter/out/openrouter/client.go` (L196-197), `backend/internal/adapter/out/openrouter/client_test.go` (L128-129), `backend/internal/adapter/out/authjwt/verifier.go` (`lookupKey` L265-278), `backend/internal/adapter/out/authjwt/verifier_test.go` (new test), `backend/internal/adapter/out/stripeapi/client.go` (L84-85) + `payments_test.go` (L316-317) **only if the package still exists** (piece 1 deletes it; its Paddle adapter keeps `502 billing_unavailable`).

**Interfaces:**
- Produces: `domain.ErrUpstream = errors.New("upstream unavailable")`; `statusFor(ErrUpstream) == (502, "upstream_unavailable")`; `safeMessage("upstream_unavailable") == "A service Calendium depends on is unavailable. Please try again later."`.

- [ ] **Step 1: Write the failing tests.** `codec_test.go`: add `{"upstream", fmt.Errorf("x: %w", domain.ErrUpstream), http.StatusBadGateway, "upstream_unavailable"},` to `TestStatusFor` and `TestStatusForViaHandler`, and `{"upstream_unavailable", "A service Calendium depends on is unavailable. Please try again later."},` to `TestSafeMessage`. `middleware_test.go` `TestRequireAuth` table, new row:

```go
		{
			name:        "verifier upstream failure is 502, never 401",
			setAuth:     true,
			authHeader:  "Bearer some-token",
			verifierErr: fmt.Errorf("%w: authjwt: fetch JWKS: dial tcp 10.0.0.5:3000: connection refused", domain.ErrUpstream),
			wantStatus:  http.StatusBadGateway,
			wantCode:    "upstream_unavailable",
			wantMessage: "A service Calendium depends on is unavailable. Please try again later.",
			wantNext:    false,
			wantVerify:  true,
		},
```

(add `"fmt"` to that file's imports) plus, inside the loop after the existing message assertion, `if strings.Contains(rec.Body.String(), "authjwt") || strings.Contains(rec.Body.String(), "10.0.0.5") { t.Fatalf("provider detail leaked: %s", rec.Body.String()) }`. `openrouter/client_test.go` L128-129: change both `domain.ErrUnauthorized` expectations to `domain.ErrUpstream`. `authjwt/verifier_test.go`, append:

```go
// TestColdCacheJWKSFailureIsUpstream: no cached keys + unreachable JWKS
// must surface as domain.ErrUpstream (502), not as an invalid token (401).
func TestColdCacheJWKSFailureIsUpstream(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	const kid, issuer = "cold-kid", "https://app.calendium.com"
	srv := jwksServer(t, kid, pub)
	client := srv.Client()
	url := srv.URL
	srv.Close() // nothing is listening: every fetch fails

	v := NewVerifier(url, issuer, client)
	token := signEdDSA(t, priv,
		map[string]any{"alg": "EdDSA", "kid": kid},
		map[string]any{"sub": "u1", "iss": issuer, "exp": time.Now().Add(time.Minute).Unix()})
	_, err := v.Verify(context.Background(), token)
	if !errors.Is(err, domain.ErrUpstream) {
		t.Fatalf("err = %v, want wrap of domain.ErrUpstream", err)
	}
}

// TestWarmCacheUnknownKidStaysUnauthorized: with keys cached, an unknown
// kid during an outage is still a bad token (no 502 oracle for forgeries).
func TestWarmCacheUnknownKidStaysUnauthorized(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	_, otherPriv, _ := ed25519.GenerateKey(nil)
	const kid, issuer = "warm-kid", "https://app.calendium.com"
	srv := jwksServer(t, kid, pub)
	v := NewVerifier(srv.URL, issuer, srv.Client())
	if _, err := v.lookupKey(context.Background(), kid); err != nil {
		t.Fatalf("warm-up: %v", err)
	}
	srv.Close()
	v.mu.Lock()
	v.fetchedAt = time.Now().Add(-jwksTTL - time.Minute) // force a refetch attempt
	v.mu.Unlock()
	token := signEdDSA(t, otherPriv,
		map[string]any{"alg": "EdDSA", "kid": "forged-kid"},
		map[string]any{"sub": "u1", "iss": issuer, "exp": time.Now().Add(time.Minute).Unix()})
	_, err := v.Verify(context.Background(), token)
	if err == nil || errors.Is(err, domain.ErrUpstream) {
		t.Fatalf("err = %v, want a non-upstream rejection", err)
	}
}
```

(add `"errors"` and `"calendium/backend/internal/domain"` to the verifier test imports).

- [ ] **Step 2: Run:** `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/adapter/in/httpapi/ ./internal/adapter/out/openrouter/ ./internal/adapter/out/authjwt/ 2>&1 | grep -E 'undefined|FAIL|ok' | head` — expected: `undefined: domain.ErrUpstream`.

- [ ] **Step 3: Add the sentinel** in `backend/internal/domain/errors.go` (inside the `var (...)` block, with the doc-comment table entry `ErrUpstream → 502`):

```go
	// ErrUpstream marks a failure of a service the PLATFORM depends on with
	// the platform's own credentials (AI gateway, billing API, the Better
	// Auth JWKS endpoint) — never a per-user provider grant, which keeps
	// ErrUnauthorized so services can drive a token refresh. The HTTP adapter
	// maps it to 502 Bad Gateway; the provider is named only in server logs.
	ErrUpstream = errors.New("upstream unavailable")
```

- [ ] **Step 4: Map it.** `codec.go` `statusFor`: add `case errors.Is(err, domain.ErrUpstream): return http.StatusBadGateway, "upstream_unavailable"` before the `ErrAIOutput` case; `safeMessage`: `case "upstream_unavailable": return "A service Calendium depends on is unavailable. Please try again later."`. `middleware.go` `requireAuth`:

```go
		identity, err := s.deps.Verifier.Verify(r.Context(), token)
		if err != nil {
			if errors.Is(err, domain.ErrUpstream) {
				s.writeError(w, r, err) // 502: the JWKS endpoint is down, the token may well be fine
				return
			}
			s.deps.Logger.Debug("token rejected", "error", err)
			// ...existing 401 envelope...
```

(add `"errors"` to the imports). `openrouter/client.go` L196-197: `return "", "", fmt.Errorf("%w: %w", domain.ErrUpstream, err)`. `authjwt/verifier.go` `lookupKey` refresh-failure branch:

```go
	if err := v.refresh(ctx); err != nil {
		// Stale fallback: keep serving a matching cached key through a transient
		// JWKS outage rather than locking everyone out.
		v.mu.Lock()
		k, have := v.keys[kid]
		cold := len(v.keys) == 0
		v.mu.Unlock()
		if have {
			return k, nil
		}
		if cold {
			return nil, fmt.Errorf("%w: %w", domain.ErrUpstream, err)
		}
		return nil, err
	}
```

(add `"calendium/backend/internal/domain"` to the verifier imports). If `backend/internal/adapter/out/stripeapi/` still exists: `client.go` L84-85 `return fmt.Errorf("%w: %w", domain.ErrUpstream, err)` and `payments_test.go` L316-317 expect `domain.ErrUpstream`; the `errors.Is(err, domain.ErrUnauthorized)` negative assertion at L346 becomes `errors.Is(err, domain.ErrUpstream)`.

- [ ] **Step 5: Run:** `cd /Users/guilherme/Dev/pessoal/calendium/backend && go build ./... && go vet ./... && go test ./internal/domain/ ./internal/adapter/in/httpapi/ ./internal/adapter/out/openrouter/ ./internal/adapter/out/authjwt/ ./internal/adapter/out/stripeapi/ ./internal/service/ 2>&1 | tail -8` — expected all `ok` (the service package has no branch on the AI gateway's ErrUnauthorized).

- [ ] **Step 6: Commit:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && git add backend/internal/domain/errors.go backend/internal/adapter/in/httpapi backend/internal/adapter/out/openrouter backend/internal/adapter/out/authjwt backend/internal/adapter/out/stripeapi && git commit -m "feat(api): platform-credential failures surface as 502 upstream_unavailable" -m "openrouter 401/403, the billing adapter and a cold-cache JWKS fetch failure wrap domain.ErrUpstream; requireAuth maps it to 502 instead of 401." -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 10: Readiness, drain channel and drain-aware SSE (Track A)

**Files:**
- Create: `backend/internal/adapter/in/httpapi/readyz.go`, `backend/internal/adapter/in/httpapi/readyz_test.go`
- Modify: `backend/internal/adapter/in/httpapi/httpapi.go` (Deps, route table), `backend/internal/adapter/in/httpapi/stream.go` (select loop), `backend/internal/adapter/in/httpapi/threadshare.go` (select loop), `backend/internal/adapter/in/httpapi/stream_test.go` (new test appended)

**Interfaces:**
- Produces: `Deps.Drain <-chan struct{}`; `Deps.Ready func(context.Context) error`; `var readyzPingTimeout = 2 * time.Second`; `GET /readyz`; `func (s *server) handleReadyz(w, r)`.

- [ ] **Step 1: Write the failing tests** in `readyz_test.go`:

```go
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

func readyzStatus(t *testing.T, h *harness) (int, string) {
	t.Helper()
	rec := h.anon(http.MethodGet, "/readyz", nil)
	var body struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return rec.Code, body.Status
}

func TestReadyz(t *testing.T) {
	t.Run("ping ok", func(t *testing.T) {
		h := newHarness(t)
		h.deps.Ready = func(context.Context) error { return nil }
		if code, status := readyzStatus(t, h); code != http.StatusOK || status != "ok" {
			t.Fatalf("%d %s, want 200 ok", code, status)
		}
	})
	t.Run("nil Ready is ok (tests, no DB)", func(t *testing.T) {
		h := newHarness(t)
		if code, _ := readyzStatus(t, h); code != http.StatusOK {
			t.Fatalf("code = %d", code)
		}
	})
	t.Run("ping error is 503 db_unavailable", func(t *testing.T) {
		h := newHarness(t)
		h.deps.Ready = func(context.Context) error { return errors.New("dial tcp: refused") }
		if code, status := readyzStatus(t, h); code != http.StatusServiceUnavailable || status != "db_unavailable" {
			t.Fatalf("%d %s, want 503 db_unavailable", code, status)
		}
	})
	t.Run("draining is 503 before the ping", func(t *testing.T) {
		h := newHarness(t)
		drain := make(chan struct{})
		close(drain)
		h.deps.Drain = drain
		pinged := false
		h.deps.Ready = func(context.Context) error { pinged = true; return nil }
		code, status := readyzStatus(t, h)
		if code != http.StatusServiceUnavailable || status != "draining" || pinged {
			t.Fatalf("%d %s pinged=%v, want 503 draining without a ping", code, status, pinged)
		}
	})
	// Review Focus 5: a ping that ignores its context still answers in time.
	t.Run("hung ping times out to 503", func(t *testing.T) {
		prev := readyzPingTimeout
		readyzPingTimeout = 50 * time.Millisecond
		t.Cleanup(func() { readyzPingTimeout = prev })
		h := newHarness(t)
		h.deps.Ready = func(context.Context) error { time.Sleep(500 * time.Millisecond); return nil }
		start := time.Now()
		code, status := readyzStatus(t, h)
		if code != http.StatusServiceUnavailable || status != "db_unavailable" {
			t.Fatalf("%d %s, want 503 db_unavailable", code, status)
		}
		if time.Since(start) > 300*time.Millisecond {
			t.Fatalf("readyz took %v; the ping timeout was not honoured", time.Since(start))
		}
	})
}
```

Append to `stream_test.go`:

```go
// TestCollabStreamEndsOnDrain: closing Deps.Drain ends an open SSE stream
// so srv.Shutdown can complete (the client reconnects on its own).
func TestCollabStreamEndsOnDrain(t *testing.T) {
	h := newHarness(t)
	drain := make(chan struct{})
	h.deps.Drain = drain
	srv := httptest.NewServer(h.handler())
	defer srv.Close()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/v1/collab/stream", nil)
	req.Header.Set("Authorization", "Bearer "+defaultToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	waitFor(t, "subscription", func() bool { return h.events.activeSubscribers() == 1 })
	close(drain)
	done := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(resp.Body)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stream ended with error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not end after drain")
	}
	waitFor(t, "unsubscribe", func() bool { return h.events.activeSubscribers() == 0 })
}
```

(add `"io"` to `stream_test.go` imports).

- [ ] **Step 2: Run:** `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/adapter/in/httpapi/ -run 'TestReadyz|TestCollabStreamEndsOnDrain' 2>&1 | head` — expected: build failure `h.deps.Ready undefined`.

- [ ] **Step 3: Add the deps and the handler.** In `httpapi.go` `Deps`:

```go
	// Drain is closed by the composition root when shutdown begins: /readyz
	// turns 503 and the SSE streams return so Shutdown can finish. nil = never.
	Drain <-chan struct{}
	// Ready pings the primary dependency (DB) for /readyz; nil = always ready.
	Ready func(context.Context) error
```

(add `"context"` and `"time"` imports as needed) and register `mux.HandleFunc("GET /readyz", s.handleReadyz)` right after `/healthz`. Create `readyz.go`:

```go
package httpapi

import (
	"context"
	"net/http"
	"time"
)

// readyzPingTimeout bounds the dependency ping; a var so tests can shrink it.
var readyzPingTimeout = 2 * time.Second

// handleReadyz is the orchestrator readiness probe (compose/k8s), distinct
// from the static /healthz liveness: 503 draining once shutdown started,
// 503 db_unavailable when the ping fails or hangs past readyzPingTimeout,
// else 200 ok. It reveals DB state, so proxies must not route it.
func (s *server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if s.deps.Drain != nil {
		select {
		case <-s.deps.Drain:
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "draining"})
			return
		default:
		}
	}
	if s.deps.Ready != nil {
		ctx, cancel := context.WithTimeout(r.Context(), readyzPingTimeout)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- s.deps.Ready(ctx) }()
		select {
		case err := <-done:
			if err != nil {
				s.deps.Logger.Warn("readyz: dependency ping failed", "error", err)
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db_unavailable"})
				return
			}
		case <-ctx.Done():
			s.deps.Logger.Warn("readyz: dependency ping timed out", "timeout", readyzPingTimeout.String())
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db_unavailable"})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
```

- [ ] **Step 4: Make both SSE loops drain-aware.** In `stream.go` `handleCollabStream` and `threadshare.go` `handleSharedThreadStream` add as the first `select` case:

```go
		case <-s.deps.Drain:
			return // server draining: EventSource reconnects against the next replica
```

(a nil channel never fires, so the harness default is unchanged).

- [ ] **Step 5: Run the package tests:** `cd /Users/guilherme/Dev/pessoal/calendium/backend && go vet ./internal/adapter/in/httpapi/ && go test -race ./internal/adapter/in/httpapi/ 2>&1 | tail -3` — expected `ok`.

- [ ] **Step 6: Commit:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && git add backend/internal/adapter/in/httpapi && git commit -m "feat(httpapi): GET /readyz with drain and bounded DB ping; SSE streams end on drain" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 11: Config — warnings, new knobs, default-password refusal, redaction, logger (Track B)

**Files:**
- Modify: `backend/internal/config/config.go` (`HTTP` struct L15-23, `Config` L193-211, `FromEnv` L219-390), `backend/internal/config/config_test.go` (`configEnvKeys` L17-29, every `FromEnv()` call), `backend/.env.example` (new section)
- Create: `backend/internal/config/logger.go`, `backend/internal/config/hardening_test.go`

**Interfaces:**
- Produces: `func FromEnv(opts ...Option) (Config, []string, error)`; `type Option func(*options)`; `func RequirePublicWebURL() Option`; `HTTP.TrustProxy bool`; `HTTP.TrustedProxyCIDRs []netip.Prefix`; `var DefaultTrustedProxyCIDRs = []string{"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "::1/128", "fc00::/7"}`; `type RateLimits struct{ PublicReadPerMin, PublicWritePerMin, UserPerMin, MutateHeavyPerMin, SearchPerMin int }`; `type Shutdown struct{ Timeout, DrainDelay time.Duration }`; `type Log struct{ Format string; Level slog.Level }`; `Config.RateLimits`, `Config.Shutdown`, `Config.Log`; `func RedactURL(raw string) string`; `func (c Config) Summary() []any`; `func NewLogger(w io.Writer, l Log) *slog.Logger`.

- [ ] **Step 1: Mechanically update the existing call sites** (the signature gains a warnings slice):

```bash
cd /Users/guilherme/Dev/pessoal/calendium/backend && sed -i '' 's/c, err := FromEnv()/c, _, err := FromEnv()/g' internal/config/config_test.go && grep -c 'c, _, err := FromEnv()' internal/config/config_test.go
```
Expected: `15` (or the number of previous call sites). Add to `configEnvKeys`: `"TRUST_PROXY", "TRUSTED_PROXY_CIDRS", "RATE_LIMIT_PUBLIC_READ_PER_MIN", "RATE_LIMIT_PUBLIC_WRITE_PER_MIN", "RATE_LIMIT_USER_PER_MIN", "RATE_LIMIT_MUTATE_HEAVY_PER_MIN", "RATE_LIMIT_SEARCH_PER_MIN", "SHUTDOWN_TIMEOUT", "SHUTDOWN_DRAIN_DELAY", "LOG_FORMAT", "LOG_LEVEL", "TODOIST_CLIENT_ID", "TODOIST_CLIENT_SECRET", "HUBSPOT_CLIENT_ID", "HUBSPOT_CLIENT_SECRET", "MAPS_NOMINATIM_URL", "MAPS_OSRM_URL"`.

- [ ] **Step 2: Write the failing tests** in `hardening_test.go`:

```go
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
	for _, pw := range []string{"change-me-please", "calendium"} {
		t.Run(pw+" cloud", func(t *testing.T) {
			setBaseEnv(t, map[string]string{
				"DATABASE_URL": "postgres://calendium:" + pw + "@db:5432/calendium?sslmode=disable",
				"SELF_HOSTED":  "false",
				"PUBLIC_WEB_URL": "https://app.example.com",
			})
			_, _, err := FromEnv()
			if err == nil || !strings.Contains(err.Error(), "DATABASE_URL uses a default password; set POSTGRES_PASSWORD") {
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
			if !containsStr(warnings, "DATABASE_URL uses a default password; set POSTGRES_PASSWORD") {
				t.Fatalf("warnings = %v", warnings)
			}
		})
	}
	t.Run("strong password is silent", func(t *testing.T) {
		setBaseEnv(t, map[string]string{"DATABASE_URL": "postgres://calendium:s3cr3t-long-value@db:5432/calendium", "SELF_HOSTED": "true"})
		_, warnings, err := FromEnv()
		if err != nil || len(warnings) != 0 {
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
	t.Run("self-host without PUBLIC_WEB_URL warns", func(t *testing.T) {
		setBaseEnv(t, map[string]string{"SELF_HOSTED": "true"})
		_, warnings, err := FromEnv()
		if err != nil || !containsStr(warnings, "PUBLIC_WEB_URL (or APP_URL) is not set; emailed links and GET /v1/instance need it") {
			t.Fatalf("err=%v warnings=%v", err, warnings)
		}
	})
	t.Run("RequirePublicWebURL errors in both modes", func(t *testing.T) {
		setBaseEnv(t, map[string]string{"SELF_HOSTED": "true"})
		if _, _, err := FromEnv(RequirePublicWebURL()); err == nil || !strings.Contains(err.Error(), "PUBLIC_WEB_URL (or APP_URL) is required") {
			t.Fatalf("err = %v", err)
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
		assertInt(t, "len(TrustedProxyCIDRs)", len(c.HTTP.TrustedProxyCIDRs), 6)
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
		setBaseEnv(t, map[string]string{"SELF_HOSTED": "true",
			"RATE_LIMIT_USER_PER_MIN": "0", "RATE_LIMIT_SEARCH_PER_MIN": "7",
			"SHUTDOWN_TIMEOUT": "45s", "SHUTDOWN_DRAIN_DELAY": "2s", "LOG_FORMAT": "text", "LOG_LEVEL": "debug"})
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
		"STRIPE_SECRET_KEY": "SECRET-stripe", "STRIPE_WEBHOOK_SECRET": "SECRET-whsec",
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
```

- [ ] **Step 3: Run:** `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/config/ 2>&1 | head` — expected: build failure `assignment mismatch` / `undefined: RequirePublicWebURL`.

- [ ] **Step 4: Extend `config.go`.** Imports gain `"io"`, `"log/slog"`, `"net/netip"`, `"net/url"`. Add to `HTTP`:

```go
	// TrustProxy (TRUST_PROXY, default false) makes X-Forwarded-* from peers
	// inside TrustedProxyCIDRs authoritative for client IP, scheme and host.
	TrustProxy bool
	// TrustedProxyCIDRs (TRUSTED_PROXY_CIDRS, comma-separated) is the proxy
	// allowlist; defaults to DefaultTrustedProxyCIDRs. A bad entry is a boot
	// error.
	TrustedProxyCIDRs []netip.Prefix
```

New types next to `HTTP`:

```go
// DefaultTrustedProxyCIDRs is the TRUSTED_PROXY_CIDRS default: loopback,
// RFC 1918 and IPv6 ULA — "the proxy is on this host or this private
// network".
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
```

Add `RateLimits RateLimits`, `Shutdown Shutdown`, `Log Log` to `Config`. Options:

```go
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
```

Change the signature to `func FromEnv(opts ...Option) (Config, []string, error)` with `var warnings []string` and `var o options; for _, opt := range opts { opt(&o) }` at the top; the error return becomes `return Config{}, nil, errors.Join(errs...)` and the success return `return cfg, warnings, nil`. Move the `SELF_HOSTED` parse block ABOVE the `DATABASE_URL` check, then replace `if cfg.DB.URL == "" { ... }` with:

```go
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
```

After the `PublicWebURL`/`APP_URL` resolution add:

```go
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
```

Before the final `if len(errs) > 0` add the new parsers:

```go
	// --- platform hardening knobs ---
	if v := os.Getenv("TRUST_PROXY"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("TRUST_PROXY must be true or false, got %q", v))
		} else {
			cfg.HTTP.TrustProxy = b
		}
	}
	cidrs := DefaultTrustedProxyCIDRs
	if v := os.Getenv("TRUSTED_PROXY_CIDRS"); v != "" {
		cidrs = strings.Split(v, ",")
	}
	for _, raw := range cidrs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("TRUSTED_PROXY_CIDRS entry %q is not a CIDR", raw))
			continue
		}
		cfg.HTTP.TrustedProxyCIDRs = append(cfg.HTTP.TrustedProxyCIDRs, p)
	}

	cfg.RateLimits = RateLimits{PublicReadPerMin: 60, PublicWritePerMin: 5, UserPerMin: 600, MutateHeavyPerMin: 30, SearchPerMin: 120}
	for name, dst := range map[string]*int{
		"RATE_LIMIT_PUBLIC_READ_PER_MIN":  &cfg.RateLimits.PublicReadPerMin,
		"RATE_LIMIT_PUBLIC_WRITE_PER_MIN": &cfg.RateLimits.PublicWritePerMin,
		"RATE_LIMIT_USER_PER_MIN":         &cfg.RateLimits.UserPerMin,
		"RATE_LIMIT_MUTATE_HEAVY_PER_MIN": &cfg.RateLimits.MutateHeavyPerMin,
		"RATE_LIMIT_SEARCH_PER_MIN":       &cfg.RateLimits.SearchPerMin,
	} {
		if v := os.Getenv(name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				errs = append(errs, fmt.Errorf("%s must be a non-negative integer (0 disables), got %q", name, v))
			} else {
				*dst = n
			}
		}
	}

	cfg.Shutdown = Shutdown{Timeout: 30 * time.Second}
	for name, dst := range map[string]*time.Duration{
		"SHUTDOWN_TIMEOUT":     &cfg.Shutdown.Timeout,
		"SHUTDOWN_DRAIN_DELAY": &cfg.Shutdown.DrainDelay,
	} {
		if v := os.Getenv(name); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil || d < 0 {
				errs = append(errs, fmt.Errorf("%s must be a non-negative Go duration (e.g. 30s), got %q", name, v))
			} else {
				*dst = d
			}
		}
	}

	cfg.Log = Log{Format: "json", Level: slog.LevelInfo}
	if v := os.Getenv("LOG_FORMAT"); v != "" {
		switch v {
		case "json", "text":
			cfg.Log.Format = v
		default:
			errs = append(errs, fmt.Errorf("LOG_FORMAT must be json or text, got %q", v))
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
			errs = append(errs, fmt.Errorf("LOG_LEVEL must be debug|info|warn|error, got %q", v))
		}
	}
```

Append to `config.go`:

```go
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
	return u.String()
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
		"billing", c.Stripe.SecretKey != "",
		"ai", c.OpenRouter.APIKey != "",
		"push_apns", c.Push.APNs.KeyP8 != "",
		"push_fcm", c.Push.FCM.ServiceAccountJSON != "",
		"push_webpush", c.Push.VAPID.PublicKey != "" && c.Push.VAPID.PrivateKey != "",
		"weather", c.Weather.BaseURL != "",
		"maps", c.Maps.NominatimBaseURL != "",
	}
}
```

(If piece 1 has already renamed `Stripe` to `Paddle`, the `"billing"` entry reads `c.Paddle.APIKey != ""`.) Create `logger.go`:

```go
package config

import (
	"io"
	"log/slog"
)

// NewLogger builds the process logger from LOG_FORMAT/LOG_LEVEL: JSON for
// production log shippers, text for a terminal.
func NewLogger(w io.Writer, l Log) *slog.Logger {
	opts := &slog.HandlerOptions{Level: l.Level}
	if l.Format == "text" {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}
```

- [ ] **Step 5: Fix the existing table.** `TestFromEnv` rows that previously booted with the default `postgres://localhost:5432/calendium` DSN and no `SELF_HOSTED` now fail on the cloud `PUBLIC_WEB_URL` rule: change `withBase` in `config_test.go` to also set `"SELF_HOSTED": "true"` (rows that assert `SelfHosted == false` already set it explicitly to `false` and must add `"PUBLIC_WEB_URL": "https://app.example.com"`); add `t.Setenv("SELF_HOSTED", "true")` to `TestFromEnvIntegrationVendors` and `TestFromEnvIntegrationVendorsDefaultEmpty`, which set their env by hand. Then run:

```bash
cd /Users/guilherme/Dev/pessoal/calendium/backend && go vet ./internal/config/ && go test ./internal/config/ 2>&1 | tail -3
```
Expected: `ok`. (`cmd/api` and `cmd/worker` do not compile until Tasks 12–13; that is expected inside this track.)

- [ ] **Step 6: Document the knobs** in `backend/.env.example` (append):

```dotenv
# --- Platform hardening (reverse proxy, limits, shutdown, logs) ---
# Trust X-Forwarded-For/Proto/Host from peers inside TRUSTED_PROXY_CIDRS.
# Leave false when the API is reached directly; docker-compose sets true.
TRUST_PROXY=false
# Comma-separated proxy allowlist (default: loopback + RFC1918 + fc00::/7).
# Narrow it to your proxy's address when clients can reach the API from a
# private network.
TRUSTED_PROXY_CIDRS=
# Per-minute budgets; 0 disables a class. Defaults: 60/5 per IP (public
# read/write), 600/30/120 per user (general/mutate_heavy/search).
RATE_LIMIT_PUBLIC_READ_PER_MIN=60
RATE_LIMIT_PUBLIC_WRITE_PER_MIN=5
RATE_LIMIT_USER_PER_MIN=600
RATE_LIMIT_MUTATE_HEAVY_PER_MIN=30
RATE_LIMIT_SEARCH_PER_MIN=120
# Graceful shutdown: in-flight bound, and how long /readyz answers 503
# before listeners close (Go durations).
SHUTDOWN_TIMEOUT=30s
SHUTDOWN_DRAIN_DELAY=0s
# json (default, for log shippers) or text (local dev); debug|info|warn|error.
LOG_FORMAT=json
LOG_LEVEL=info
```

- [ ] **Step 7: Commit:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && git add backend/internal/config backend/.env.example && git commit -m "feat(config): warnings, proxy trust, rate-limit/shutdown/log knobs, default-password refusal, redacted summary" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 12: `cmd/api` — `serve()`, server timeouts, graceful shutdown, wiring (Track B; after Tasks 1, 3, 10, 11)

**Files:**
- Create: `backend/cmd/api/serve.go`, `backend/cmd/api/serve_test.go`
- Modify: `backend/cmd/api/main.go` (L46-71 `main`/`run` head, L128 pgbus listener, L435-480 `httpapi.Deps`, L482-500 server/shutdown)

**Interfaces:**
- Consumes: `config.FromEnv(config.RequirePublicWebURL())`, `config.NewLogger`, `cfg.Summary()`, `cfg.HTTP.TrustProxy/TrustedProxyCIDRs`, `cfg.RateLimits`, `cfg.Shutdown`, `cfg.Log` (Task 11); `httpapi.Deps{TrustProxy, TrustedProxyCIDRs, RateLimits, Drain, Ready}` (Tasks 1, 3, 10).
- Produces: `type serveOptions struct{ shutdownTimeout, drainDelay time.Duration }`; `func serve(ctx context.Context, logger *slog.Logger, srv *http.Server, ln net.Listener, drain chan struct{}, opts serveOptions) error`.

- [ ] **Step 1: Write the failing tests** in `backend/cmd/api/serve_test.go` (the first test file in `cmd/api`):

```go
package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"calendium/backend/internal/adapter/in/httpapi"
)

// startServe boots serve() on a random loopback port with the real httpapi
// handler (for /readyz) plus a /slow route that blocks until release is
// closed. It returns the base URL, the cancel that simulates SIGTERM, and
// a channel that yields serve's return value.
func startServe(t *testing.T, opts serveOptions, release <-chan struct{}, entered chan<- struct{}) (string, context.CancelFunc, chan struct{}, <-chan error) {
	t.Helper()
	drain := make(chan struct{})
	api := httpapi.New(httpapi.Deps{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Drain:  drain,
		Ready:  func(context.Context) error { return nil },
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("done"))
	})
	mux.Handle("/", api)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- serve(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), srv, ln, drain, opts)
	}()
	return "http://" + ln.Addr().String(), cancel, drain, errCh
}

func TestGracefulShutdown(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	base, cancel, drain, errCh := startServe(t, serveOptions{shutdownTimeout: 5 * time.Second}, release, entered)

	type result struct {
		status int
		body   string
		err    error
	}
	resCh := make(chan result, 1)
	go func() {
		resp, err := http.Get(base + "/slow")
		if err != nil {
			resCh <- result{err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		resCh <- result{status: resp.StatusCode, body: string(b)}
	}()
	<-entered // the request is in flight

	cancel() // SIGTERM
	select {
	case <-drain:
	case <-time.After(time.Second):
		t.Fatal("drain channel was not closed after the signal")
	}
	select {
	case err := <-errCh:
		t.Fatalf("serve returned %v while a request was still in flight", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(release) // the handler finishes
	res := <-resCh
	if res.err != nil || res.status != http.StatusOK || res.body != "done" {
		t.Fatalf("in-flight request = %+v, want 200 done", res)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("serve returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serve did not return after the last request completed")
	}
}

func TestServeReadyzDrainingDuringDrainDelay(t *testing.T) {
	release := make(chan struct{})
	close(release)
	entered := make(chan struct{}, 1)
	base, cancel, drain, errCh := startServe(t, serveOptions{shutdownTimeout: 5 * time.Second, drainDelay: 500 * time.Millisecond}, release, entered)

	resp, err := http.Get(base + "/readyz")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("readyz before shutdown: %v %v", err, resp)
	}
	_ = resp.Body.Close()

	cancel()
	<-drain // listeners stay open for drainDelay after this
	resp, err = http.Get(base + "/readyz")
	if err != nil {
		t.Fatalf("readyz during drain delay: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || string(body) != "{\"status\":\"draining\"}\n" {
		t.Fatalf("readyz = %d %q, want 503 draining", resp.StatusCode, body)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("serve returned %v", err)
	}
}

func TestServeForcesCloseAfterShutdownTimeout(t *testing.T) {
	release := make(chan struct{}) // never closed: the handler hangs forever
	entered := make(chan struct{}, 1)
	base, cancel, _, errCh := startServe(t, serveOptions{shutdownTimeout: 200 * time.Millisecond}, release, entered)
	go func() {
		resp, err := http.Get(base + "/slow")
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-entered
	start := time.Now()
	cancel()
	select {
	case <-errCh:
		if time.Since(start) > 2*time.Second {
			t.Fatalf("serve took %v, want ~200ms then Close()", time.Since(start))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serve never returned: Close() after the deadline is missing")
	}
	close(release)
}
```

- [ ] **Step 2: Run:** `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./cmd/api/ 2>&1 | head` — expected: build failure `undefined: serve` (plus the `FromEnv` mismatch from Task 11).

- [ ] **Step 3: Create `backend/cmd/api/serve.go`:**

```go
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// serveOptions carries the SHUTDOWN_* knobs.
type serveOptions struct {
	shutdownTimeout time.Duration
	drainDelay      time.Duration
}

// serve runs srv on ln until ctx is cancelled (SIGTERM/SIGINT), then
// drains in the documented order: close(drain) so /readyz answers 503 and
// the SSE streams end → sleep drainDelay so a balancer stops routing →
// srv.Shutdown bounded by shutdownTimeout → on deadline log and
// srv.Close(). It returns nil on a clean stop and the listener error
// otherwise; the caller closes the DB afterwards.
func serve(ctx context.Context, logger *slog.Logger, srv *http.Server, ln net.Listener, drain chan struct{}, opts serveOptions) error {
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	logger.Info("api: shutdown requested; draining",
		"drain_delay", opts.drainDelay.String(), "timeout", opts.shutdownTimeout.String())
	close(drain)
	if opts.drainDelay > 0 {
		time.Sleep(opts.drainDelay)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), opts.shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("api: shutdown deadline exceeded; closing open connections", "error", err)
		_ = srv.Close()
	}
	<-errCh // Serve has returned (ErrServerClosed)
	return nil
}
```

- [ ] **Step 4: Rewire `main.go`.** Imports: add `"net"`, `"sync"`; drop `"errors"` only if nothing else uses it (the JWKS check still does). Replace `main` and the head of `run`:

```go
func main() {
	// Bootstrap logger until config picks the real handler.
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(logger); err != nil {
		logger.Error("api: fatal", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, warnings, err := config.FromEnv(config.RequirePublicWebURL())
	if err != nil {
		return err
	}
	logger = config.NewLogger(os.Stderr, cfg.Log)
	for _, w := range warnings {
		logger.Warn(w)
	}
	logger.Info("api: config", cfg.Summary()...)

	// Fail fast on a guaranteed-broken auth configuration instead of booting
	// healthy and 401ing every request.
	if cfg.Auth.JWKSURL == "" {
		return errors.New("BETTER_AUTH_URL (or AUTH_JWKS_URL) is required: without it every authenticated request fails with 401")
	}
	// A second signal after the first restores default handling (force-kill).
	go func() {
		<-ctx.Done()
		stop()
	}()
```

(the old `PUBLIC_WEB_URL` check is deleted: `RequirePublicWebURL()` owns it now). Replace the pgbus line `go pgbus.NewListener(cfg.DB.URL, bus, logger).Run(ctx)` with:

```go
	var listeners sync.WaitGroup
	listeners.Add(1)
	go func() {
		defer listeners.Done()
		pgbus.NewListener(cfg.DB.URL, bus, logger).Run(ctx)
	}()
	drain := make(chan struct{})
```

In the `httpapi.Deps{...}` literal add after `CORSAllowedOrigins`:

```go
		TrustProxy:        cfg.HTTP.TrustProxy,
		TrustedProxyCIDRs: cfg.HTTP.TrustedProxyCIDRs,
		RateLimits: httpapi.RateLimits{
			PublicReadPerMin:  cfg.RateLimits.PublicReadPerMin,
			PublicWritePerMin: cfg.RateLimits.PublicWritePerMin,
			UserPerMin:        cfg.RateLimits.UserPerMin,
			MutateHeavyPerMin: cfg.RateLimits.MutateHeavyPerMin,
			SearchPerMin:      cfg.RateLimits.SearchPerMin,
		},
		Drain: drain,
		Ready: db.PingContext,
```

Replace the server block through the end of `run`:

```go
	srv := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		// No WriteTimeout: SSE streams and attachment downloads outlive any
		// sane value; per-handler deadlines live in httpapi instead.
	}
	ln, err := net.Listen("tcp", cfg.HTTP.Addr)
	if err != nil {
		return err
	}
	logger.Info("api: listening", "addr", ln.Addr().String())
	if err := serve(ctx, logger, srv, ln, drain, serveOptions{
		shutdownTimeout: cfg.Shutdown.Timeout,
		drainDelay:      cfg.Shutdown.DrainDelay,
	}); err != nil {
		return err
	}

	// The LISTEN connection ends with ctx; give it a moment before the
	// deferred db.Close().
	listenersDone := make(chan struct{})
	go func() {
		listeners.Wait()
		close(listenersDone)
	}()
	select {
	case <-listenersDone:
	case <-time.After(5 * time.Second):
		logger.Warn("api: pgbus listener did not stop within 5s")
	}
	logger.Info("api: shut down cleanly")
	return nil
}
```

- [ ] **Step 5: Run:** `cd /Users/guilherme/Dev/pessoal/calendium/backend && go build ./... && go vet ./cmd/api/ && go test -race ./cmd/api/ 2>&1 | tail -3` — expected `ok`.

- [ ] **Step 6: Commit:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && git add backend/cmd/api && git commit -m "feat(api): testable serve() with drain, bounded Shutdown then Close, server timeouts, hardening deps wired" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 13: `cmd/worker` — bounded shutdown, logger, warnings (Track B; after Task 11)

**Files:**
- Create: `backend/cmd/worker/shutdown.go`, `backend/cmd/worker/shutdown_test.go`
- Modify: `backend/cmd/worker/main.go` (L65-78 `main`/`run` head, L354-356 `wg.Wait()`)

**Interfaces:**
- Consumes: `config.FromEnv`, `config.NewLogger`, `cfg.Summary()`, `cfg.Shutdown.Timeout` (Task 11).
- Produces: `func waitWithTimeout(wg *sync.WaitGroup, d time.Duration) bool`; `var errShutdownTimeout = errors.New("worker: shutdown timed out")`.

- [ ] **Step 1: Write the failing test** in `backend/cmd/worker/shutdown_test.go`:

```go
package main

import (
	"sync"
	"testing"
	"time"
)

func TestWaitWithTimeout(t *testing.T) {
	t.Run("returns true when the group finishes in time", func(t *testing.T) {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			time.Sleep(20 * time.Millisecond)
			wg.Done()
		}()
		if !waitWithTimeout(&wg, time.Second) {
			t.Fatal("want true")
		}
	})
	t.Run("returns false when the group hangs", func(t *testing.T) {
		var wg sync.WaitGroup
		wg.Add(1) // never Done
		start := time.Now()
		if waitWithTimeout(&wg, 50*time.Millisecond) {
			t.Fatal("want false")
		}
		if time.Since(start) > 500*time.Millisecond {
			t.Fatalf("took %v, want ~50ms", time.Since(start))
		}
		wg.Done()
	})
}
```

- [ ] **Step 2: Run:** `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./cmd/worker/ 2>&1 | head` — expected: build failure `undefined: waitWithTimeout`.

- [ ] **Step 3: Create `backend/cmd/worker/shutdown.go`:**

```go
package main

import (
	"errors"
	"sync"
	"time"
)

// errShutdownTimeout makes main exit 1 when a loop ignores its cancelled
// context past SHUTDOWN_TIMEOUT.
var errShutdownTimeout = errors.New("worker: shutdown timed out")

// waitWithTimeout waits for wg at most d; false means it is still running.
func waitWithTimeout(wg *sync.WaitGroup, d time.Duration) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}
```

- [ ] **Step 4: Update `main.go`.** `run` head:

```go
	cfg, warnings, err := config.FromEnv()
	if err != nil {
		return err
	}
	logger = config.NewLogger(os.Stderr, cfg.Log)
	for _, w := range warnings {
		logger.Warn(w)
	}
	logger.Info("worker: config", cfg.Summary()...)
```

Replace `wg.Wait()` + the clean-shutdown log with:

```go
	<-ctx.Done()
	logger.Info("worker: shutdown requested; waiting for loops", "timeout", cfg.Shutdown.Timeout.String())
	if !waitWithTimeout(&wg, cfg.Shutdown.Timeout) {
		logger.Error("worker: shutdown timed out", "timeout", cfg.Shutdown.Timeout.String())
		return errShutdownTimeout
	}
	logger.Info("worker: shut down cleanly")
	return nil
```

(`main` keeps `os.Exit(1)` on a non-nil error, which is the documented exit code.)

- [ ] **Step 5: Run:** `cd /Users/guilherme/Dev/pessoal/calendium/backend && go build ./... && go vet ./cmd/worker/ && go test ./cmd/worker/ 2>&1 | tail -3` — expected `ok`.

- [ ] **Step 6: Commit:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && git add backend/cmd/worker && git commit -m "feat(worker): bounded loop drain on shutdown, configured logger and startup summary" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 14: `netguard` shared SSRF predicate (Track B)

**Files:**
- Create: `backend/internal/adapter/out/netguard/netguard.go`, `backend/internal/adapter/out/netguard/netguard_test.go`
- Modify: `backend/internal/adapter/out/icsfeed/fetcher.go` (L136-141 `isPublicAddr`), `backend/internal/adapter/out/icsfeed/fetcher_test.go` (L191-222 `TestIsPublicAddr` moves), `backend/internal/adapter/out/unsubscribe/client.go` (L30-44 `blockPrivateNetworks`), `backend/internal/adapter/out/unsubscribe/client_test.go` (new case)

**Interfaces:**
- Produces: `func netguard.IsPublic(ip netip.Addr) bool`; `func netguard.Control(network, address string, c syscall.RawConn) error`.

- [ ] **Step 1: Write the failing tests** in `netguard_test.go`:

```go
package netguard

import (
	"net/netip"
	"testing"
)

// TestIsPublic pins every refused range (moved from icsfeed) plus the
// CGNAT and NAT64 additions.
func TestIsPublic(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"93.184.216.34", true},
		{"2606:2800:220:1::1", true},
		{"127.0.0.1", false},
		{"127.8.8.8", false},
		{"::1", false},
		{"10.0.0.5", false},
		{"172.16.0.1", false},
		{"192.168.1.1", false},
		{"169.254.169.254", false},
		{"fe80::1", false},
		{"fc00::1", false},
		{"fdab::12", false},
		{"0.0.0.0", false},
		{"::", false},
		{"224.0.0.1", false},
		{"::ffff:10.0.0.5", false},
		{"::ffff:127.0.0.1", false},
		{"::ffff:93.184.216.34", true},
		// CGNAT 100.64.0.0/10 (RFC 6598)
		{"100.64.0.1", false},
		{"100.127.255.255", false},
		{"::ffff:100.64.0.1", false},
		{"100.128.0.1", true},
		{"100.63.255.255", true},
		// NAT64 64:ff9b::/96 (RFC 6052): the whole prefix is blocked.
		{"64:ff9b::7f00:1", false},
		{"64:ff9b::5db8:d822", false},
		{"64:ff9b:1::1", true},
	}
	for _, tc := range cases {
		if got := IsPublic(netip.MustParseAddr(tc.addr)); got != tc.want {
			t.Errorf("IsPublic(%s) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}

func TestControl(t *testing.T) {
	if err := Control("tcp", "93.184.216.34:443", nil); err != nil {
		t.Fatalf("public literal refused: %v", err)
	}
	for _, addr := range []string{"100.64.0.1:443", "[64:ff9b::7f00:1]:443", "127.0.0.1:1", "[fe80::1%25eth0]:443", "not-an-address", "host.example:443"} {
		if err := Control("tcp", addr, nil); err == nil {
			t.Errorf("Control(%q) = nil, want refusal", addr)
		}
	}
}
```

- [ ] **Step 2: Run:** `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/adapter/out/netguard/ 2>&1 | head` — expected: `no Go files` / build failure.

- [ ] **Step 3: Create `netguard.go`:**

```go
// Package netguard is the ONE SSRF predicate shared by every outbound
// adapter that dials user-supplied hosts (icsfeed subscriptions, RFC 8058
// unsubscribe POSTs): an address may be dialled only when it is public
// global unicast and outside every private, loopback, link-local, ULA,
// unspecified, multicast, CGNAT (100.64.0.0/10) and NAT64 (64:ff9b::/96)
// range. Nothing in Calendium needs NAT64, so the whole prefix is refused
// rather than translated and re-checked.
package netguard

import (
	"fmt"
	"net"
	"net/netip"
	"syscall"
)

var blocked = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"), // CGNAT, RFC 6598
	netip.MustParsePrefix("64:ff9b::/96"),  // NAT64, RFC 6052
}

// IsPublic reports whether ip may be dialled. IPv4-mapped IPv6 is unmapped
// first so ::ffff:10.0.0.5 is refused like 10.0.0.5.
func IsPublic(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	for _, p := range blocked {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// Control is a net.Dialer.Control hook: it runs after DNS resolution and
// before the connect syscall, so a hostname resolving (or re-resolving) to
// a refused address can never be reached.
func Control(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("netguard: %w", err)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("netguard: could not parse resolved address %q", host)
	}
	if !IsPublic(ip) {
		return fmt.Errorf("netguard: refusing to dial disallowed address %s", ip)
	}
	return nil
}
```

- [ ] **Step 4: Delegate.** `icsfeed/fetcher.go`: import `"calendium/backend/internal/adapter/out/netguard"` and replace `isPublicAddr`:

```go
// isPublicAddr delegates to the shared SSRF predicate (netguard); kept as
// a package function so the dial guard reads the same as before.
func isPublicAddr(ip netip.Addr) bool { return netguard.IsPublic(ip) }
```

Delete `TestIsPublicAddr` from `fetcher_test.go` (L191-222; the table now lives in `netguard_test.go`) and remove its now-unused imports if any. `unsubscribe/client.go`: import netguard and replace `blockPrivateNetworks`:

```go
// blockPrivateNetworks delegates to the shared SSRF predicate; it stays a
// named function so dialGuard (and allowLoopbackDialsForTest) are unchanged.
func blockPrivateNetworks(network, address string, c syscall.RawConn) error {
	return netguard.Control(network, address, c)
}
```

(drop the now-unused `"net"` import if `go build` says so). Append to `unsubscribe/client_test.go`:

```go
func TestPostOneClickBlocksCGNATAndNAT64(t *testing.T) {
	for _, target := range []string{"https://100.64.0.1:1/x", "https://[64:ff9b::7f00:1]:1/x"} {
		if err := New().PostOneClick(context.Background(), target); err == nil {
			t.Fatalf("%s accepted, want rejection by the dial guard", target)
		}
	}
}
```

- [ ] **Step 5: Run:** `cd /Users/guilherme/Dev/pessoal/calendium/backend && go vet ./internal/adapter/out/... && go test ./internal/adapter/out/netguard/ ./internal/adapter/out/icsfeed/ ./internal/adapter/out/unsubscribe/ 2>&1 | tail -4` — expected three `ok` lines.

- [ ] **Step 6: Commit:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && git add backend/internal/adapter/out/netguard backend/internal/adapter/out/icsfeed backend/internal/adapter/out/unsubscribe && git commit -m "feat(netguard): shared SSRF predicate covering CGNAT and NAT64; icsfeed and unsubscribe delegate" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 15: Web — CSP policy builder, static headers, `/api/health`, `/api/csp-report` (Track C)

**Files:**
- Create: `apps/web/lib/csp.ts`, `apps/web/lib/csp.test.ts`, `apps/web/lib/security-headers.ts`, `apps/web/lib/security-headers.test.ts`, `apps/web/lib/csp-report.ts`, `apps/web/lib/csp-report.test.ts`, `apps/web/app/api/health/route.ts`, `apps/web/app/api/health/route.test.ts`, `apps/web/app/api/csp-report/route.ts`, `apps/web/app/api/csp-report/route.test.ts`
- Modify: `apps/web/next.config.ts`

**Interfaces:**
- Produces: `cspPolicy({ nonce?: string; apiUrl: string; dev: boolean }): string` (no nonce → `'unsafe-inline'`); `makeNonce(): string`; `STATIC_SECURITY_HEADERS: {key,value}[]`; `offlineCsp(): string`; `securityHeaders(): { source: string; headers: {key,value}[] }[]`; `parseCspReports(parsed: unknown): CspViolation[]`; `type CspViolation = { documentUri: string; violatedDirective: string; blockedUri: string; sourceFile: string; lineNumber: number | null }`; `MAX_CSP_REPORT_BYTES = 16 * 1024`.

- [ ] **Step 1: Write the failing tests.** `apps/web/lib/csp.test.ts`:

```ts
// @vitest-environment node
import { describe, expect, it } from 'vitest';

import { cspPolicy, makeNonce } from '@/lib/csp';

describe('cspPolicy', () => {
  it('renders the production nonce policy', () => {
    const policy = cspPolicy({ nonce: 'abc123', apiUrl: 'https://api.example.com', dev: false });
    expect(policy).toBe(
      "default-src 'self'; script-src 'self' 'nonce-abc123' https://cdn.paddle.com; frame-src https://*.paddle.com https://accounts.google.com https://appleid.apple.com; connect-src 'self' https://api.example.com https://*.paddle.com; img-src 'self' data: https:; style-src 'self' 'unsafe-inline'; font-src 'self' data:; frame-ancestors 'none'; base-uri 'self'; form-action 'self' https://appleid.apple.com",
    );
  });
  it('omits the API origin when same-origin', () => {
    const policy = cspPolicy({ nonce: 'n', apiUrl: '', dev: false });
    expect(policy).toContain("connect-src 'self' https://*.paddle.com;");
  });
  it('adds unsafe-eval and ws: in dev only', () => {
    const dev = cspPolicy({ nonce: 'n', apiUrl: '', dev: true });
    expect(dev).toContain("script-src 'self' 'nonce-n' https://cdn.paddle.com 'unsafe-eval'");
    expect(dev).toContain("connect-src 'self' https://*.paddle.com ws:");
    const prod = cspPolicy({ nonce: 'n', apiUrl: '', dev: false });
    expect(prod).not.toContain('unsafe-eval');
    expect(prod).not.toContain('ws:');
  });
  it('uses unsafe-inline for scripts when no nonce is given (the /offline page)', () => {
    const policy = cspPolicy({ apiUrl: '', dev: false });
    expect(policy).toContain("script-src 'self' 'unsafe-inline' https://cdn.paddle.com");
    expect(policy).not.toContain('nonce-');
  });
});

describe('makeNonce', () => {
  it('returns base64 of 16 random bytes and never repeats', () => {
    const a = makeNonce();
    const b = makeNonce();
    expect(a).toMatch(/^[A-Za-z0-9+/]{22}==$/);
    expect(a).not.toBe(b);
  });
});
```

`apps/web/lib/security-headers.test.ts`:

```ts
// @vitest-environment node
import { afterEach, describe, expect, it, vi } from 'vitest';

import { STATIC_SECURITY_HEADERS, offlineCsp, securityHeaders } from '@/lib/security-headers';

describe('securityHeaders', () => {
  afterEach(() => vi.unstubAllEnvs());

  it('matches the spec snapshot for every route', () => {
    expect(STATIC_SECURITY_HEADERS).toEqual([
      { key: 'Strict-Transport-Security', value: 'max-age=31536000; includeSubDomains' },
      { key: 'X-Content-Type-Options', value: 'nosniff' },
      { key: 'X-Frame-Options', value: 'DENY' },
      { key: 'Referrer-Policy', value: 'strict-origin-when-cross-origin' },
      { key: 'Permissions-Policy', value: 'camera=(), microphone=(), geolocation=()' },
    ]);
    const [all, offline] = securityHeaders();
    expect(all.source).toBe('/(.*)');
    expect(all.headers).toBe(STATIC_SECURITY_HEADERS);
    expect(offline.source).toBe('/offline');
    expect(offline.headers).toEqual([{ key: 'Content-Security-Policy', value: offlineCsp() }]);
  });

  it('offline CSP is the nonce policy with unsafe-inline scripts and the API origin', () => {
    vi.stubEnv('NEXT_PUBLIC_API_URL', 'https://api.example.com/');
    expect(offlineCsp()).toContain("script-src 'self' 'unsafe-inline' https://cdn.paddle.com");
    expect(offlineCsp()).toContain("connect-src 'self' https://api.example.com https://*.paddle.com");
  });
});
```

`apps/web/lib/csp-report.test.ts`:

```ts
// @vitest-environment node
import { describe, expect, it } from 'vitest';

import { parseCspReports } from '@/lib/csp-report';

describe('parseCspReports', () => {
  it('normalises a legacy report-uri body', () => {
    expect(
      parseCspReports({
        'csp-report': {
          'document-uri': 'https://app.example.com/mail',
          'violated-directive': 'script-src',
          'blocked-uri': 'https://evil.example/x.js',
          'source-file': 'https://app.example.com/_next/a.js',
          'line-number': 12,
        },
      }),
    ).toEqual([
      {
        documentUri: 'https://app.example.com/mail',
        violatedDirective: 'script-src',
        blockedUri: 'https://evil.example/x.js',
        sourceFile: 'https://app.example.com/_next/a.js',
        lineNumber: 12,
      },
    ]);
  });
  it('normalises a Reporting API array and ignores other report types', () => {
    expect(
      parseCspReports([
        { type: 'deprecation', body: {} },
        {
          type: 'csp-violation',
          body: { documentURL: 'https://a/b', effectiveDirective: 'img-src', blockedURL: 'http://x/y.png' },
        },
      ]),
    ).toEqual([
      { documentUri: 'https://a/b', violatedDirective: 'img-src', blockedUri: 'http://x/y.png', sourceFile: '', lineNumber: null },
    ]);
  });
  it('returns an empty list for anything else', () => {
    expect(parseCspReports('nope')).toEqual([]);
    expect(parseCspReports({ other: 1 })).toEqual([]);
    expect(parseCspReports(null)).toEqual([]);
  });
});
```

`apps/web/app/api/health/route.test.ts`:

```ts
// @vitest-environment node
import { describe, expect, it } from 'vitest';

import { GET, dynamic } from '@/app/api/health/route';

describe('GET /api/health', () => {
  it('answers 200 {status:ok} and is never statically cached', async () => {
    const res = GET();
    expect(res.status).toBe(200);
    await expect(res.json()).resolves.toEqual({ status: 'ok' });
    expect(dynamic).toBe('force-dynamic');
  });
});
```

`apps/web/app/api/csp-report/route.test.ts`:

```ts
// @vitest-environment node
import { afterEach, describe, expect, it, vi } from 'vitest';

import { POST } from '@/app/api/csp-report/route';

function post(body: string, type = 'application/csp-report'): Request {
  return new Request('http://localhost/api/csp-report', {
    method: 'POST',
    headers: { 'content-type': type, 'user-agent': 'vitest' },
    body,
  });
}

describe('POST /api/csp-report', () => {
  afterEach(() => vi.restoreAllMocks());

  it('logs one csp_violation line per report and answers 204', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const res = await POST(
      post(JSON.stringify({ 'csp-report': { 'document-uri': 'https://a/b', 'violated-directive': 'script-src', 'blocked-uri': 'inline' } })),
    );
    expect(res.status).toBe(204);
    expect(warn).toHaveBeenCalledTimes(1);
    expect(JSON.parse(warn.mock.calls[0][0] as string)).toEqual({
      msg: 'csp_violation',
      documentUri: 'https://a/b',
      violatedDirective: 'script-src',
      blockedUri: 'inline',
      sourceFile: '',
      lineNumber: null,
      userAgent: 'vitest',
    });
  });
  it('accepts application/reports+json', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    const res = await POST(post(JSON.stringify([{ type: 'csp-violation', body: { documentURL: 'https://a', effectiveDirective: 'img-src', blockedURL: 'x' } }]), 'application/reports+json'));
    expect(res.status).toBe(204);
  });
  it('rejects other content types and malformed JSON with 400', async () => {
    expect((await POST(post('{}', 'text/plain'))).status).toBe(400);
    expect((await POST(post('{not json'))).status).toBe(400);
  });
  it('rejects bodies over 16 KiB with 413', async () => {
    const res = await POST(post(JSON.stringify({ 'csp-report': { 'blocked-uri': 'x'.repeat(16 * 1024) } })));
    expect(res.status).toBe(413);
  });
});
```

- [ ] **Step 2: Run:** `cd /Users/guilherme/Dev/pessoal/calendium && bun run --cwd apps/web test -- lib/csp lib/security-headers lib/csp-report app/api 2>&1 | tail -15` — expected: `Failed to resolve import "@/lib/csp"` (and siblings).

- [ ] **Step 3: Create `apps/web/lib/csp.ts`:**

```ts
/**
 * Content Security Policy for the web app (platform-hardening spec,
 * decision 3). One builder serves both the per-request nonce policy
 * (middleware.ts) and the static /offline policy (next.config.ts headers):
 * the only difference is script-src 'nonce-<n>' versus 'unsafe-inline'.
 *
 * Allowed third parties: Paddle.js (script + overlay iframes + XHR) and the
 * Google / Apple sign-in frames. Everything else is same-origin.
 */
export type CspOptions = {
  /** Per-request nonce; omit for the static /offline policy. */
  nonce?: string;
  /** API origin for connect-src; '' means same origin (env.apiUrl). */
  apiUrl: string;
  /** Dev adds 'unsafe-eval' (React refresh) and ws: (HMR). */
  dev: boolean;
};

export function cspPolicy({ nonce, apiUrl, dev }: CspOptions): string {
  const scriptSrc = ["'self'", nonce ? `'nonce-${nonce}'` : "'unsafe-inline'", 'https://cdn.paddle.com'];
  if (dev) scriptSrc.push("'unsafe-eval'");
  const connectSrc = ["'self'"];
  if (apiUrl) connectSrc.push(apiUrl);
  connectSrc.push('https://*.paddle.com');
  if (dev) connectSrc.push('ws:');
  return [
    "default-src 'self'",
    `script-src ${scriptSrc.join(' ')}`,
    'frame-src https://*.paddle.com https://accounts.google.com https://appleid.apple.com',
    `connect-src ${connectSrc.join(' ')}`,
    "img-src 'self' data: https:",
    "style-src 'self' 'unsafe-inline'",
    "font-src 'self' data:",
    "frame-ancestors 'none'",
    "base-uri 'self'",
    "form-action 'self' https://appleid.apple.com",
  ].join('; ');
}

/** 16 random bytes, base64 — valid in both the edge and node runtimes. */
export function makeNonce(): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  let binary = '';
  for (const b of bytes) binary += String.fromCharCode(b);
  return btoa(binary);
}
```

`apps/web/lib/security-headers.ts` (relative imports on purpose: `next.config.ts` is loaded before the `@/` alias exists):

```ts
import { cspPolicy } from './csp';
import { env } from './env';

/** The five static headers next.config.ts sets on every route. */
export const STATIC_SECURITY_HEADERS = [
  { key: 'Strict-Transport-Security', value: 'max-age=31536000; includeSubDomains' },
  { key: 'X-Content-Type-Options', value: 'nosniff' },
  { key: 'X-Frame-Options', value: 'DENY' },
  { key: 'Referrer-Policy', value: 'strict-origin-when-cross-origin' },
  { key: 'Permissions-Policy', value: 'camera=(), microphone=(), geolocation=()' },
];

/**
 * /offline is force-static and precached by public/sw.js, so it cannot
 * carry a per-request nonce: it gets the same policy with 'unsafe-inline'
 * scripts. It renders no user content.
 */
export function offlineCsp(): string {
  return cspPolicy({ apiUrl: env.apiUrl, dev: process.env.NODE_ENV !== 'production' });
}

export function securityHeaders() {
  return [
    { source: '/(.*)', headers: STATIC_SECURITY_HEADERS },
    { source: '/offline', headers: [{ key: 'Content-Security-Policy', value: offlineCsp() }] },
  ];
}
```

`apps/web/lib/csp-report.ts`:

```ts
/** One normalised CSP violation, whichever wire format delivered it. */
export type CspViolation = {
  documentUri: string;
  violatedDirective: string;
  blockedUri: string;
  sourceFile: string;
  lineNumber: number | null;
};

export const MAX_CSP_REPORT_BYTES = 16 * 1024;

function str(v: unknown): string {
  return typeof v === 'string' ? v : '';
}

function num(v: unknown): number | null {
  return typeof v === 'number' && Number.isFinite(v) ? v : null;
}

/**
 * Accepts the legacy `report-uri` body ({"csp-report": {...}}) and the
 * Reporting API array ([{type:"csp-violation", body:{...}}]); anything
 * else yields no reports (the route still answers 204 — no oracle).
 */
export function parseCspReports(parsed: unknown): CspViolation[] {
  if (Array.isArray(parsed)) {
    return parsed
      .filter((r): r is { type: string; body: Record<string, unknown> } =>
        typeof r === 'object' && r !== null && (r as { type?: unknown }).type === 'csp-violation' && typeof (r as { body?: unknown }).body === 'object',
      )
      .map(({ body }) => ({
        documentUri: str(body.documentURL),
        violatedDirective: str(body.effectiveDirective),
        blockedUri: str(body.blockedURL),
        sourceFile: str(body.sourceFile),
        lineNumber: num(body.lineNumber),
      }));
  }
  if (typeof parsed === 'object' && parsed !== null && 'csp-report' in parsed) {
    const body = (parsed as { 'csp-report': unknown })['csp-report'];
    if (typeof body !== 'object' || body === null) return [];
    const r = body as Record<string, unknown>;
    return [
      {
        documentUri: str(r['document-uri']),
        violatedDirective: str(r['violated-directive']),
        blockedUri: str(r['blocked-uri']),
        sourceFile: str(r['source-file']),
        lineNumber: num(r['line-number']),
      },
    ];
  }
  return [];
}
```

`apps/web/app/api/health/route.ts`:

```ts
/** Container HEALTHCHECK target (apps/web/Dockerfile); never prerendered. */
export const dynamic = 'force-dynamic';

export function GET(): Response {
  return Response.json({ status: 'ok' });
}
```

`apps/web/app/api/csp-report/route.ts`:

```ts
import { MAX_CSP_REPORT_BYTES, parseCspReports } from '@/lib/csp-report';

export const dynamic = 'force-dynamic';

/**
 * CSP violation sink: one console.warn JSON line per report (picked up by
 * the container's log shipper), no storage. Rejects anything that is not a
 * CSP report body (400) or is over 16 KiB (413).
 */
export async function POST(req: Request): Promise<Response> {
  const type = req.headers.get('content-type') ?? '';
  if (!type.startsWith('application/csp-report') && !type.startsWith('application/reports+json')) {
    return new Response(null, { status: 400 });
  }
  const declared = Number(req.headers.get('content-length') ?? '0');
  if (declared > MAX_CSP_REPORT_BYTES) return new Response(null, { status: 413 });
  const text = await req.text();
  if (new TextEncoder().encode(text).byteLength > MAX_CSP_REPORT_BYTES) {
    return new Response(null, { status: 413 });
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    return new Response(null, { status: 400 });
  }
  const userAgent = req.headers.get('user-agent') ?? '';
  for (const report of parseCspReports(parsed)) {
    console.warn(JSON.stringify({ msg: 'csp_violation', ...report, userAgent }));
  }
  return new Response(null, { status: 204 });
}
```

`apps/web/next.config.ts`:

```ts
import type { NextConfig } from 'next';
import path from 'node:path';

import { securityHeaders } from './lib/security-headers';

const nextConfig: NextConfig = {
  // Emit a self-contained server bundle (.next/standalone) for the Docker image.
  output: 'standalone',
  // Trace from the monorepo root so the workspace dep @calendium/shared and the
  // hoisted node_modules are included in the standalone output.
  outputFileTracingRoot: path.join(__dirname, '../../'),
  transpilePackages: ['@calendium/shared'],
  // Static security headers on every route; the per-request nonce CSP is
  // added by middleware.ts, /offline gets its fixed CSP here.
  async headers() {
    return securityHeaders();
  },
};

export default nextConfig;
```

- [ ] **Step 4: Run:** `cd /Users/guilherme/Dev/pessoal/calendium && bun run --cwd apps/web test 2>&1 | tail -6 && bunx biome check apps/web && bun run --cwd apps/web typecheck` — expected: all suites pass, Biome clean, `tsc` clean.

- [ ] **Step 5: Commit:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && git add apps/web/lib/csp.ts apps/web/lib/csp.test.ts apps/web/lib/security-headers.ts apps/web/lib/security-headers.test.ts apps/web/lib/csp-report.ts apps/web/lib/csp-report.test.ts apps/web/app/api/health apps/web/app/api/csp-report apps/web/next.config.ts && git commit -m "feat(web): static security headers, CSP policy builder, /api/health and /api/csp-report" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 16: Web — nonce CSP middleware (Report-Only), layout nonce, e2e spec (Track C)

**Files:**
- Create: `apps/web/middleware.ts`, `apps/web/middleware.test.ts`, `apps/web/e2e/csp.spec.ts`
- Modify: `apps/web/app/layout.tsx`, `apps/web/.env.example`

**Interfaces:**
- Consumes: `cspPolicy`, `makeNonce` (Task 15), `env.apiUrl`.
- Produces: `middleware(req: NextRequest): NextResponse`; `cspReportOnly(): boolean` (env `CSP_REPORT_ONLY`, default `'true'` in this task); request header `x-nonce`; `<script id="theme-init" nonce=...>`.

- [ ] **Step 1: Write the failing middleware test** in `apps/web/middleware.test.ts`:

```ts
// @vitest-environment node
import { NextRequest } from 'next/server';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { config, cspReportOnly, middleware } from '@/middleware';

function run(path = '/mail') {
  return middleware(new NextRequest(`http://localhost:3000${path}`));
}

describe('middleware CSP', () => {
  afterEach(() => vi.unstubAllEnvs());

  it('defaults to Report-Only with a fresh nonce on request and response', () => {
    vi.stubEnv('CSP_REPORT_ONLY', undefined);
    vi.stubEnv('NEXT_PUBLIC_API_URL', 'https://api.example.com');
    const res = run();
    const header = res.headers.get('content-security-policy-report-only');
    expect(header).toBeTruthy();
    expect(res.headers.get('content-security-policy')).toBeNull();
    const nonce = /'nonce-([^']+)'/.exec(header ?? '')?.[1];
    expect(nonce).toMatch(/^[A-Za-z0-9+/]{22}==$/);
    expect(header).toContain("connect-src 'self' https://api.example.com https://*.paddle.com");
    expect(header).toContain('; report-uri /api/csp-report; report-to csp');
    expect(res.headers.get('reporting-endpoints')).toBe('csp="/api/csp-report"');
    // NextResponse.next({request:{headers}}) exposes the overridden request
    // headers as x-middleware-request-<name>.
    expect(res.headers.get('x-middleware-request-x-nonce')).toBe(nonce);
    expect(res.headers.get('x-middleware-request-content-security-policy-report-only')).toBe(header);
  });

  it('enforces when CSP_REPORT_ONLY=false', () => {
    vi.stubEnv('CSP_REPORT_ONLY', 'false');
    const res = run();
    expect(res.headers.get('content-security-policy')).toContain("default-src 'self'");
    expect(res.headers.get('content-security-policy-report-only')).toBeNull();
    expect(cspReportOnly()).toBe(false);
  });

  it('same-origin API when NEXT_PUBLIC_API_URL is unset', () => {
    vi.stubEnv('NEXT_PUBLIC_API_URL', undefined);
    const header = run().headers.get('content-security-policy-report-only') ?? '';
    expect(header).toContain("connect-src 'self' https://*.paddle.com");
  });

  it('adds the dev-only sources outside production', () => {
    vi.stubEnv('NODE_ENV', 'development');
    const header = run().headers.get('content-security-policy-report-only') ?? '';
    expect(header).toContain("'unsafe-eval'");
    expect(header).toContain('ws:');
    vi.stubEnv('NODE_ENV', 'production');
    const prod = run().headers.get('content-security-policy-report-only') ?? '';
    expect(prod).not.toContain("'unsafe-eval'");
  });

  it('never matches the excluded paths', () => {
    expect(config.matcher).toEqual(['/((?!api/|_next/static|_next/image|sw\\.js|manifest\\.webmanifest|icon|offline).*)']);
    const re = new RegExp(`^${config.matcher[0]}$`);
    for (const excluded of ['/api/auth/token', '/_next/static/a.js', '/_next/image', '/sw.js', '/manifest.webmanifest', '/icon.svg', '/icon-192.png', '/offline']) {
      expect(re.test(excluded), excluded).toBe(false);
    }
    for (const included of ['/', '/mail', '/checkout', '/checkout/success', '/signin']) {
      expect(re.test(included), included).toBe(true);
    }
  });
});
```

And `apps/web/e2e/csp.spec.ts`:

```ts
import { expect, test } from './fixtures';

/**
 * Platform hardening: every app page carries the nonce CSP (Report-Only
 * until Task 18 flips CSP_REPORT_ONLY) and the theme-init inline script is
 * nonced with the same value, so enforcing the policy cannot break first
 * paint. Browsers hide the nonce content attribute; read the IDL property.
 */
test.describe('Content Security Policy', () => {
  test('page response has the CSP header and the theme script carries its nonce', async ({ page }) => {
    const response = await page.goto('/mail');
    expect(response).not.toBeNull();
    const headers = response!.headers();
    const csp = headers['content-security-policy-report-only'] ?? headers['content-security-policy'];
    expect(csp, 'CSP header present').toBeTruthy();
    expect(csp).toContain("default-src 'self'");
    expect(csp).toContain('https://cdn.paddle.com');
    expect(csp).toContain("frame-ancestors 'none'");
    const nonce = /'nonce-([^']+)'/.exec(csp ?? '')?.[1];
    expect(nonce).toBeTruthy();
    const scriptNonce = await page.evaluate(
      () => (document.getElementById('theme-init') as HTMLScriptElement | null)?.nonce ?? null,
    );
    expect(scriptNonce).toBe(nonce);
    expect(headers['x-frame-options']).toBe('DENY');
    expect(headers['x-content-type-options']).toBe('nosniff');
  });

  test('/offline keeps a static CSP without a nonce', async ({ page }) => {
    const response = await page.goto('/offline');
    const csp = response!.headers()['content-security-policy'];
    expect(csp).toContain("script-src 'self' 'unsafe-inline'");
    expect(csp).not.toContain('nonce-');
  });
});
```

- [ ] **Step 2: Run:** `cd /Users/guilherme/Dev/pessoal/calendium && bun run --cwd apps/web test -- middleware 2>&1 | tail -8` — expected: `Failed to resolve import "@/middleware"`.

- [ ] **Step 3: Create `apps/web/middleware.ts`:**

```ts
import { type NextRequest, NextResponse } from 'next/server';

import { cspPolicy, makeNonce } from '@/lib/csp';
import { env } from '@/lib/env';

/**
 * Per-request nonce CSP (platform-hardening spec, decision 3). Excluded:
 * the API routes (Better Auth, csp-report, health), Next static assets, the
 * service worker and manifest, icons, and /offline (static CSP from
 * next.config.ts). Next 15.5 reads the policy from the REQUEST header to
 * nonce its own inline scripts; the same value is copied to the response.
 */
export const config = {
  matcher: ['/((?!api/|_next/static|_next/image|sw\\.js|manifest\\.webmanifest|icon|offline).*)'],
};

/**
 * CSP_REPORT_ONLY is read at runtime (not inlined) so the policy can be
 * rolled back to Report-Only without a rebuild. Default: report-only until
 * the e2e suite and a sandbox checkout pass enforced (then Task 18 flips it).
 */
export function cspReportOnly(): boolean {
  return (process.env.CSP_REPORT_ONLY ?? 'true') === 'true';
}

export function middleware(req: NextRequest): NextResponse {
  const nonce = makeNonce();
  const policy = `${cspPolicy({
    nonce,
    apiUrl: env.apiUrl,
    dev: process.env.NODE_ENV !== 'production',
  })}; report-uri /api/csp-report; report-to csp`;
  const headerName = cspReportOnly() ? 'Content-Security-Policy-Report-Only' : 'Content-Security-Policy';

  const requestHeaders = new Headers(req.headers);
  requestHeaders.set('x-nonce', nonce);
  requestHeaders.set(headerName, policy);

  const res = NextResponse.next({ request: { headers: requestHeaders } });
  res.headers.set(headerName, policy);
  res.headers.set('Reporting-Endpoints', 'csp="/api/csp-report"');
  return res;
}
```

- [ ] **Step 4: Propagate the nonce** in `apps/web/app/layout.tsx`: add `import { headers } from 'next/headers';` and change the component to

```tsx
export default async function RootLayout({ children }: { children: React.ReactNode }) {
  // Set by middleware.ts; empty on /offline (force-static, no middleware),
  // where the static CSP allows inline scripts instead.
  const nonce = (await headers()).get('x-nonce') ?? undefined;
  return (
    <html lang="en" className={inter.variable} suppressHydrationWarning>
      <head>
        {/* biome-ignore lint/security/noDangerouslySetInnerHtml: hardcoded, static
            script (themeInitScript above) — not user-controlled input. */}
        <script id="theme-init" nonce={nonce} dangerouslySetInnerHTML={{ __html: themeInitScript }} />
      </head>
      <body className="font-sans antialiased">
        <Providers>{children}</Providers>
      </body>
    </html>
  );
}
```

(Reading `headers()` in the root layout makes every matched route dynamic; marketing pages go from `○` to `ƒ` at build — accepted by the spec.) Append to `apps/web/.env.example`:

```dotenv
# ── Content Security Policy ────────────────────────────────────────────────────
# Runtime (no rebuild). true = Content-Security-Policy-Report-Only (violations
# only logged via /api/csp-report); false = enforced. Default: true.
CSP_REPORT_ONLY=true
```

- [ ] **Step 5: Run the web suites, lint, typecheck and the e2e suite:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && bun run --cwd apps/web test 2>&1 | tail -6 && bunx biome check apps/web && bun run --cwd apps/web typecheck && bun run test:e2e 2>&1 | tail -15
```
Expected: unit suites pass; e2e passes including `csp.spec.ts` (local run uses `next dev`, so the dev-only sources appear in the header; the assertions above do not depend on them).

- [ ] **Step 6: Commit:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && git add apps/web/middleware.ts apps/web/middleware.test.ts apps/web/app/layout.tsx apps/web/e2e/csp.spec.ts apps/web/.env.example && git commit -m "feat(web): per-request nonce CSP in Report-Only mode with reporting endpoint; nonce on the theme script" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 17: Web — `BETTER_AUTH_SECRET` boot check (Track C)

**Files:**
- Create: `apps/web/lib/auth-secret.ts`, `apps/web/lib/auth-secret.test.ts`, `apps/web/instrumentation.ts`, `apps/web/instrumentation.test.ts`
- Modify: `apps/web/lib/auth.ts` (top of file), `apps/web/vitest.setup.ts`

**Interfaces:**
- Produces: `MIN_BETTER_AUTH_SECRET_BYTES = 32`; `assertBetterAuthSecret(env?: NodeJS.ProcessEnv): void` (skips when `env.NEXT_PHASE === 'phase-production-build'`); `register(): Promise<void>`.

- [ ] **Step 1: Write the failing tests.** `apps/web/lib/auth-secret.test.ts`:

```ts
// @vitest-environment node
import { describe, expect, it } from 'vitest';

import { assertBetterAuthSecret } from '@/lib/auth-secret';

const MESSAGE = 'BETTER_AUTH_SECRET must be at least 32 bytes; generate one with: openssl rand -base64 32';

describe('assertBetterAuthSecret', () => {
  it('throws on a 31-byte secret', () => {
    expect(() => assertBetterAuthSecret({ BETTER_AUTH_SECRET: 'a'.repeat(31) })).toThrow(MESSAGE);
  });
  it('throws when unset or empty', () => {
    expect(() => assertBetterAuthSecret({})).toThrow(MESSAGE);
    expect(() => assertBetterAuthSecret({ BETTER_AUTH_SECRET: '' })).toThrow(MESSAGE);
  });
  it('accepts 32 bytes and counts bytes, not characters', () => {
    expect(() => assertBetterAuthSecret({ BETTER_AUTH_SECRET: 'a'.repeat(32) })).not.toThrow();
    expect(() => assertBetterAuthSecret({ BETTER_AUTH_SECRET: 'é'.repeat(16) })).not.toThrow(); // 32 bytes
    expect(() => assertBetterAuthSecret({ BETTER_AUTH_SECRET: 'é'.repeat(15) })).toThrow(MESSAGE); // 30 bytes
  });
  it("accepts Playwright's 45-char e2e secret", () => {
    expect(() => assertBetterAuthSecret({ BETTER_AUTH_SECRET: 'e2e-playwright-not-a-real-secret-0123456789ab' })).not.toThrow();
  });
  it('skips during the production build phase (no secret in the image build)', () => {
    expect(() => assertBetterAuthSecret({ NEXT_PHASE: 'phase-production-build' })).not.toThrow();
  });
});
```

`apps/web/instrumentation.test.ts`:

```ts
// @vitest-environment node
import { afterEach, describe, expect, it, vi } from 'vitest';

import { register } from '@/instrumentation';

describe('instrumentation.register', () => {
  afterEach(() => vi.unstubAllEnvs());

  it('rejects a short secret on the node runtime', async () => {
    vi.stubEnv('NEXT_RUNTIME', 'nodejs');
    vi.stubEnv('NEXT_PHASE', '');
    vi.stubEnv('BETTER_AUTH_SECRET', 'x'.repeat(31));
    await expect(register()).rejects.toThrow('BETTER_AUTH_SECRET must be at least 32 bytes');
  });
  it('passes with a long secret', async () => {
    vi.stubEnv('NEXT_RUNTIME', 'nodejs');
    vi.stubEnv('NEXT_PHASE', '');
    vi.stubEnv('BETTER_AUTH_SECRET', 'x'.repeat(32));
    await expect(register()).resolves.toBeUndefined();
  });
  it('skips the edge runtime and the build phase', async () => {
    vi.stubEnv('NEXT_RUNTIME', 'edge');
    vi.stubEnv('BETTER_AUTH_SECRET', '');
    await expect(register()).resolves.toBeUndefined();
    vi.stubEnv('NEXT_RUNTIME', 'nodejs');
    vi.stubEnv('NEXT_PHASE', 'phase-production-build');
    await expect(register()).resolves.toBeUndefined();
  });
});
```

- [ ] **Step 2: Run:** `cd /Users/guilherme/Dev/pessoal/calendium && bun run --cwd apps/web test -- auth-secret instrumentation 2>&1 | tail -8` — expected: `Failed to resolve import`.

- [ ] **Step 3: Create `apps/web/lib/auth-secret.ts`:**

```ts
export const MIN_BETTER_AUTH_SECRET_BYTES = 32;

/**
 * Refuses to run Better Auth with a weak root secret (it signs sessions and
 * encrypts the JWKS private keys). Skipped during `next build`: the Docker
 * build stage has no secret and must stay secret-free. Called from
 * instrumentation.ts (server start) and lib/auth.ts (module load).
 */
export function assertBetterAuthSecret(env: NodeJS.ProcessEnv = process.env): void {
  if (env.NEXT_PHASE === 'phase-production-build') return;
  if (Buffer.byteLength(env.BETTER_AUTH_SECRET ?? '', 'utf8') >= MIN_BETTER_AUTH_SECRET_BYTES) return;
  throw new Error('BETTER_AUTH_SECRET must be at least 32 bytes; generate one with: openssl rand -base64 32');
}
```

`apps/web/instrumentation.ts`:

```ts
/**
 * Next.js instrumentation hook: runs once when the server boots (node
 * runtime only; the edge runtime never hosts Better Auth). A short
 * BETTER_AUTH_SECRET aborts startup here with an actionable message
 * instead of silently signing sessions with a weak key.
 */
export async function register(): Promise<void> {
  if (process.env.NEXT_RUNTIME !== 'nodejs') return;
  const { assertBetterAuthSecret } = await import('./lib/auth-secret');
  assertBetterAuthSecret();
}
```

`apps/web/lib/auth.ts`: add after the `pg` import

```ts
import { assertBetterAuthSecret } from '@/lib/auth-secret';

// Same rule as instrumentation.ts, enforced again at module load so a
// route handler can never construct Better Auth around a weak secret.
assertBetterAuthSecret();
```

`apps/web/vitest.setup.ts`: add near the top

```ts
// lib/auth.ts asserts BETTER_AUTH_SECRET at module load; give the unit
// suites a syntactically valid (never used) value.
process.env.BETTER_AUTH_SECRET ??= 'vitest-better-auth-secret-0123456789abcdef';
```

- [ ] **Step 4: Run:** `cd /Users/guilherme/Dev/pessoal/calendium && bun run --cwd apps/web test 2>&1 | tail -6 && bunx biome check apps/web && bun run --cwd apps/web typecheck && NEXT_PHASE=phase-production-build bun run --cwd apps/web build 2>&1 | tail -5` — expected: suites pass (incl. `lib/auth.test.ts`), lint/typecheck clean, and a build with no secret succeeds (phase guard).

- [ ] **Step 5: Commit:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && git add apps/web/lib/auth-secret.ts apps/web/lib/auth-secret.test.ts apps/web/instrumentation.ts apps/web/instrumentation.test.ts apps/web/lib/auth.ts apps/web/vitest.setup.ts && git commit -m "feat(web): refuse to start with a BETTER_AUTH_SECRET under 32 bytes" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 18: Web — enforce the CSP (Track C; only after Task 16 and the enforced e2e run)

**Files:**
- Modify: `apps/web/middleware.ts` (`cspReportOnly` default), `apps/web/middleware.test.ts`, `apps/web/e2e/csp.spec.ts`, `apps/web/.env.example`

**Interfaces:**
- Produces: `cspReportOnly()` defaults to `false`.

- [ ] **Step 1: Gate — run the whole e2e suite enforced** (CI runs `next build && next start`; locally force the same path):

```bash
cd /Users/guilherme/Dev/pessoal/calendium && CSP_REPORT_ONLY=false CI=1 bun run test:e2e 2>&1 | tail -20
```
Expected: every spec passes (compose, calendar, offline, scheduling, tasks, csp, ...). Any failure is a real CSP break (an inline handler, a blocked `http:` image, an un-nonced inline script) — fix the page, never widen the policy beyond the spec.

- [ ] **Step 2: Gate — sandbox checkout under Report-Only.** If `apps/web/app/checkout/page.tsx` (piece 1) exists and Paddle sandbox credentials (`NEXT_PUBLIC_PADDLE_CLIENT_TOKEN`, a sandbox price) are available: run the web app with `CSP_REPORT_ONLY=true`, open `/checkout`, complete a sandbox purchase to `/checkout/success`, and confirm the server log contains zero `csp_violation` lines. If the page or the credentials are absent, record **BLOCKED (sandbox checkout gate: piece 1 not merged / no Paddle sandbox credentials)** in the task result and STOP here without committing — the policy stays Report-Only (safe) and this task is re-run when the gate can be exercised.

- [ ] **Step 3: Flip the default.** `apps/web/middleware.ts`: `return (process.env.CSP_REPORT_ONLY ?? 'false') === 'true';` and reword its comment to `Default: enforced (Task 18); set CSP_REPORT_ONLY=true to roll back to Report-Only without a rebuild.` `apps/web/.env.example`: `CSP_REPORT_ONLY=false` with the comment `Default: false (enforced). Set true to roll back to report-only.` `middleware.test.ts`: the first test becomes `'defaults to enforced with a fresh nonce on request and response'` asserting `content-security-policy` present and `content-security-policy-report-only` null (same nonce/request-header assertions, header names swapped), and add:

```ts
  it('rolls back to Report-Only when CSP_REPORT_ONLY=true', () => {
    vi.stubEnv('CSP_REPORT_ONLY', 'true');
    const res = run();
    expect(res.headers.get('content-security-policy-report-only')).toContain("default-src 'self'");
    expect(res.headers.get('content-security-policy')).toBeNull();
  });
```

`e2e/csp.spec.ts`: replace the header lookup with

```ts
    const csp = headers['content-security-policy'];
    expect(csp, 'CSP is enforced (not report-only)').toBeTruthy();
    expect(headers['content-security-policy-report-only']).toBeUndefined();
```

- [ ] **Step 4: Run:** `cd /Users/guilherme/Dev/pessoal/calendium && bun run --cwd apps/web test -- middleware 2>&1 | tail -5 && CI=1 bun run test:e2e 2>&1 | tail -10` — expected: pass.

- [ ] **Step 5: Commit:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && git add apps/web/middleware.ts apps/web/middleware.test.ts apps/web/e2e/csp.spec.ts apps/web/.env.example && git commit -m "feat(web): enforce the Content-Security-Policy by default (CSP_REPORT_ONLY=true rolls back)" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 19: Image digest pins, web HEALTHCHECK, CI `docker-build`, Dependabot (Track D)

**Files:**
- Modify: `backend/Dockerfile` (L10, L23), `apps/web/Dockerfile` (L11, L27, L46, after L60 `EXPOSE 3000`), `.github/workflows/test.yml` (append job)
- Create: `.github/dependabot.yml`

**Interfaces:**
- Consumes: `GET /api/health` (Task 15) for the web HEALTHCHECK.
- Produces: digest-pinned `FROM` lines; CI job `docker-build`.

- [ ] **Step 1: Write the failing check** (the gate for this task is a grep, not a Go test):

```bash
cd /Users/guilherme/Dev/pessoal/calendium && grep -nE '^FROM .*@sha256:' backend/Dockerfile apps/web/Dockerfile; grep -n HEALTHCHECK apps/web/Dockerfile; test -f .github/dependabot.yml && echo dependabot-present; grep -n 'docker-build' .github/workflows/test.yml
```
Expected: no output from any of the four commands (nothing is pinned, no web HEALTHCHECK, no Dependabot config, no CI job).

- [ ] **Step 2: Resolve the digests and rewrite the `FROM` lines** (needs the Docker daemon; records tag + date in a comment above each pin):

```bash
cd /Users/guilherme/Dev/pessoal/calendium
TODAY=$(date +%Y-%m-%d)
GO_DIGEST=$(docker buildx imagetools inspect golang:1.26-alpine --format '{{.Manifest.Digest}}')
ALPINE_DIGEST=$(docker buildx imagetools inspect alpine:3.22 --format '{{.Manifest.Digest}}')
BUN_DIGEST=$(docker buildx imagetools inspect oven/bun:1.3 --format '{{.Manifest.Digest}}')
NODE_DIGEST=$(docker buildx imagetools inspect node:22-alpine --format '{{.Manifest.Digest}}')
echo "go=$GO_DIGEST alpine=$ALPINE_DIGEST bun=$BUN_DIGEST node=$NODE_DIGEST"
perl -pi -e "s~^FROM golang:1.26-alpine AS build\$~# golang:1.26-alpine resolved ${TODAY} (docker buildx imagetools inspect)\nFROM golang:1.26-alpine\@${GO_DIGEST} AS build~" backend/Dockerfile
perl -pi -e "s~^FROM alpine:3.20\$~# alpine:3.22 resolved ${TODAY} (docker buildx imagetools inspect)\nFROM alpine:3.22\@${ALPINE_DIGEST}~" backend/Dockerfile
perl -pi -e "s~^FROM oven/bun:1 AS (deps|build)\$~# oven/bun:1.3 resolved ${TODAY} (docker buildx imagetools inspect)\nFROM oven/bun:1.3\@${BUN_DIGEST} AS \$1~" apps/web/Dockerfile
perl -pi -e "s~^FROM node:22-alpine AS runner\$~# node:22-alpine resolved ${TODAY} (docker buildx imagetools inspect)\nFROM node:22-alpine\@${NODE_DIGEST} AS runner~" apps/web/Dockerfile
grep -nE '^(FROM|# .*resolved)' backend/Dockerfile apps/web/Dockerfile
```
Expected: six `FROM` lines, each with `@sha256:` and a dated comment above it (two `oven/bun:1.3` stages).

- [ ] **Step 3: Add the web HEALTHCHECK** in `apps/web/Dockerfile` right after `EXPOSE 3000`:

```dockerfile
# Readiness for compose/orchestrators (busybox wget ships in node:22-alpine);
# /api/health is force-dynamic so a cached 200 can never mask a dead server.
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:3000/api/health || exit 1
```

- [ ] **Step 4: Create `.github/dependabot.yml`** (keeps the digests from rotting):

```yaml
# Weekly digest bumps for every pinned base image. Compose's own images
# (postgres, caddy) live in docker-compose.yml at the repo root.
version: 2
updates:
  - package-ecosystem: docker
    directory: /backend
    schedule:
      interval: weekly
  - package-ecosystem: docker
    directory: /apps/web
    schedule:
      interval: weekly
  - package-ecosystem: docker
    directory: /
    schedule:
      interval: weekly
```

- [ ] **Step 5: Append the CI job** to `.github/workflows/test.yml` (after the `e2e` job):

```yaml
  docker-build:
    name: Docker images build (backend, web)
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@v4

      - name: Set up Docker Buildx
        uses: docker/setup-buildx-action@v3

      # Build only (no registry, no push): proves the digest pins resolve
      # and both Dockerfiles still build from the repo root context.
      - name: Build backend image
        uses: docker/build-push-action@v6
        with:
          context: .
          file: backend/Dockerfile
          push: false
          cache-from: type=gha,scope=backend
          cache-to: type=gha,mode=max,scope=backend

      - name: Build web image
        uses: docker/build-push-action@v6
        with:
          context: .
          file: apps/web/Dockerfile
          push: false
          build-args: |
            NEXT_PUBLIC_API_URL=https://ci.invalid
          cache-from: type=gha,scope=web
          cache-to: type=gha,mode=max,scope=web
```

- [ ] **Step 6: Re-run the check and build both images locally:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && grep -cE '^FROM .*@sha256:' backend/Dockerfile apps/web/Dockerfile && grep -c HEALTHCHECK apps/web/Dockerfile && test -f .github/dependabot.yml && grep -c 'docker-build' .github/workflows/test.yml && docker build -f backend/Dockerfile -t calendium-backend:pin-check . 2>&1 | tail -3 && docker build -f apps/web/Dockerfile --build-arg NEXT_PUBLIC_API_URL=https://ci.invalid -t calendium-web:pin-check . 2>&1 | tail -3
```
Expected: `backend/Dockerfile:2`, `apps/web/Dockerfile:3`, `1`, `1`, and both builds end with a successful `writing image`/`naming to` line (the web build runs under `NEXT_PHASE=phase-production-build`, so the Task 17 secret guard is skipped).

- [ ] **Step 7: Commit:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && git add backend/Dockerfile apps/web/Dockerfile .github/dependabot.yml .github/workflows/test.yml && git commit -m "build: digest-pin base images (alpine 3.22, bun 1.3), web HEALTHCHECK, CI docker-build job, Dependabot docker updates" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 20: Compose loopback publish, limits, readiness, `.env.example` and self-hosting docs (Track D)

**Files:**
- Modify: `docker-compose.yml` (`api`, `worker`, `web`, `caddy` services), `.env.example` (root; port section + new section), `docs/self-hosting/security.md` (§4 bullet 2, §7 table), `docs/self-hosting/reverse-proxy-tls.md` (new section + nginx note), `docs/self-hosting/upgrades.md` (single-replica section), `docs/self-hosting/configuration.md` (new section)

**Interfaces:**
- Consumes: `/readyz` (Tasks 10, 12), `TRUST_PROXY` and friends (Task 11), `/api/health` (Task 15).

- [ ] **Step 1: Write the failing check:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && grep -nE 'API_BIND|WEB_BIND|TRUST_PROXY|readyz|memory: 512m|service_healthy' docker-compose.yml; grep -n 'API_BIND' .env.example; grep -n 'TRUST_PROXY' docs/self-hosting/configuration.md docs/self-hosting/reverse-proxy-tls.md docs/self-hosting/security.md
```
Expected: only the two existing `condition: service_healthy` lines under `db` dependencies; nothing else.

- [ ] **Step 2: Edit `docker-compose.yml`.** `api` service — add to `environment`, replace `ports`, add `healthcheck` and `deploy`:

```yaml
    environment:
      # Bind inside the container; the host mapping is API_PORT below.
      HTTP_ADDR: ":8080"
      # api + worker verify Better Auth JWTs by fetching its JWKS server-to-server.
      # BETTER_AUTH_URL is a *public* origin the api container usually can't reach
      # from inside this network, so default the JWKS to the in-network web
      # service. Override in .env only if web is reachable at another URL.
      AUTH_JWKS_URL: ${AUTH_JWKS_URL:-http://web:3000/api/auth/jwks}
      # The bundled Caddy (or your own proxy on this host) is the only peer
      # that can reach the API, so its X-Forwarded-* headers are trusted:
      # rate limits, logs and OAuth callback URLs see the real client.
      TRUST_PROXY: "true"
    depends_on:
      db:
        condition: service_healthy
    ports:
      # Loopback by default so only a proxy on this host reaches the API
      # (Docker published ports bypass UFW). LAN without a proxy: API_BIND=0.0.0.0.
      - "${API_BIND:-127.0.0.1}:${API_PORT:-8080}:8080"
    # Readiness instead of the image's /healthz liveness: 503 while the DB is
    # unreachable or the process is draining, so the proxy stops routing early.
    healthcheck:
      test: ["CMD", "wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8080/readyz"]
      interval: 10s
      timeout: 3s
      start_period: 20s
      retries: 3
    deploy:
      resources:
        limits:
          memory: 512m
```

`worker` service — add after `healthcheck: disable: true`:

```yaml
    deploy:
      resources:
        limits:
          memory: 512m
```

`web` service — replace `ports` and add `deploy` (the image's HEALTHCHECK from Task 19 applies as-is):

```yaml
    ports:
      - "${WEB_BIND:-127.0.0.1}:${WEB_PORT:-3000}:3000"
    deploy:
      resources:
        limits:
          memory: 512m
```

`caddy` service — replace `depends_on`:

```yaml
    depends_on:
      web:
        condition: service_healthy
      api:
        condition: service_healthy
```

Update the file header comment: `#   docker compose up -d --build                 # db + api + worker + web (api/web on 127.0.0.1 only)`.

- [ ] **Step 3: Edit the root `.env.example`.** Replace the final port section with:

```dotenv
# ─── Host port mappings ───────────────────────────────────────────────────────────
# api/web are published on loopback only: a proxy on this host (the bundled
# Caddy profile, or your own nginx/Traefik) is the only way in. For a LAN box
# WITHOUT a proxy set API_BIND=0.0.0.0 and WEB_BIND=0.0.0.0 (then also set
# TRUST_PROXY=false so clients cannot spoof X-Forwarded-For).
API_BIND=127.0.0.1
WEB_BIND=127.0.0.1
WEB_PORT=3000
API_PORT=8080

# ─── Platform hardening (api + worker) ─────────────────────────────────────────
# docker-compose.yml sets TRUST_PROXY=true on api (Caddy is the only peer).
# Narrow the proxy allowlist to your proxy's address if clients share the
# private network with the API (default: loopback + RFC1918 + fc00::/7).
TRUSTED_PROXY_CIDRS=
# Per-minute budgets; 0 disables a class. Single api replica assumed.
RATE_LIMIT_PUBLIC_READ_PER_MIN=60
RATE_LIMIT_PUBLIC_WRITE_PER_MIN=5
RATE_LIMIT_USER_PER_MIN=600
RATE_LIMIT_MUTATE_HEAVY_PER_MIN=30
RATE_LIMIT_SEARCH_PER_MIN=120
# Graceful shutdown bound and the /readyz 503 window before listeners close.
SHUTDOWN_TIMEOUT=30s
SHUTDOWN_DRAIN_DELAY=0s
# json for log shippers, text for a terminal; debug|info|warn|error.
LOG_FORMAT=json
LOG_LEVEL=info
# Web: true = Content-Security-Policy-Report-Only (violations only logged),
# false = enforced. Runtime, no rebuild.
CSP_REPORT_ONLY=false
```

(and change the `POSTGRES_PASSWORD=change-me-please` / `DATABASE_URL` lines' comment to `# MUST be changed: the API refuses to boot in cloud mode with this value and warns loudly when self-hosted.`)

- [ ] **Step 4: Edit `docs/self-hosting/security.md`.** In §4 replace the second bullet (`**\`api\` and \`web\` publish ...** ... in \`.env\`.`) with:

```markdown
- **`api` and `web` publish on loopback only** — `127.0.0.1:${API_PORT:-8080}`
  and `127.0.0.1:${WEB_PORT:-3000}` — so only a proxy running on the same host
  (the bundled Caddy profile, or your own nginx/Traefik) can reach them. For a
  LAN box without any proxy set `API_BIND=0.0.0.0` / `WEB_BIND=0.0.0.0` in
  `.env` **and** `TRUST_PROXY=false`, otherwise any LAN client could spoof
  `X-Forwarded-For` and share (or dodge) another client's rate-limit bucket.
- **`TRUST_PROXY=true` is set on `api` by the compose file.** The API then takes
  the client IP, scheme and host from `X-Forwarded-*` — but only when the
  socket peer is inside `TRUSTED_PROXY_CIDRS` (default loopback + RFC 1918 +
  `fc00::/7`). Caddy strips client-supplied `X-Forwarded-*` and the nginx
  sample appends with `$proxy_add_x_forwarded_for`, so the right-most
  untrusted hop is the real client in both setups. If other machines on the
  private network can reach port 8080 directly, narrow `TRUSTED_PROXY_CIDRS`
  to the proxy's own address.
- **`/readyz` is internal.** It reports database reachability (and `draining`
  during shutdown) for the compose health check; neither the Caddyfile nor the
  nginx sample routes it. `/healthz` stays the public liveness probe.
```

In §7 replace the image table and the sentence before it with:

```markdown
Every base image is **pinned by digest** (`image:tag@sha256:…`, with the tag and
resolution date in a comment above each `FROM`), and
[`.github/dependabot.yml`](../../.github/dependabot.yml) opens a weekly PR when a
pinned digest has a newer build. CI builds both images on every push
(`docker-build` job) so a rotten pin fails before it reaches you.

| Image | Used by |
| --- | --- |
| `postgres:16-alpine` | `db` |
| `caddy:2-alpine` | `caddy` proxy |
| `golang:1.26-alpine@sha256:…` (build), `alpine:3.22@sha256:…` (runtime) | backend image |
| `oven/bun:1.3@sha256:…` (build), `node:22-alpine@sha256:…` (runtime) | web image |
```

and add to the checklist at the top: `- [ ] \`api\`/\`web\` published on loopback; \`TRUST_PROXY\` only behind your own proxy` and `- [ ] \`POSTGRES_PASSWORD\` is not a default (the API refuses \`change-me-please\` / \`calendium\` in cloud mode)`.

- [ ] **Step 5: Edit `docs/self-hosting/reverse-proxy-tls.md`.** After the intro paragraph that ends `use **Traefik** if you run a dynamic Docker fleet.` insert:

```markdown
### Proxy trust (`TRUST_PROXY`)

Behind any proxy the API must know the *real* client address for per-IP rate
limits, request logs and the OAuth callback origin. The compose file sets
`TRUST_PROXY=true` on `api`, which makes the API honour `X-Forwarded-For`,
`X-Forwarded-Proto` and `X-Forwarded-Host` — but only from a peer inside
`TRUSTED_PROXY_CIDRS` (default: loopback, RFC 1918, `fc00::/7`). The client is
the right-most `X-Forwarded-For` hop that is *not* a trusted proxy. Caddy
replaces client-supplied `X-Forwarded-*`; nginx must use
`$proxy_add_x_forwarded_for` (the sample does). Never set `TRUST_PROXY=true`
when clients can reach port 8080 directly.

`/readyz` (database ping, `503 draining` during shutdown) is for the compose
health check only — keep it out of the proxy; route `/healthz` instead.
```

In the nginx section replace `The sample already sets the headers that matter.` with `The sample already sets the headers that matter (\`X-Forwarded-For\` via \`$proxy_add_x_forwarded_for\`, \`X-Forwarded-Proto\`, \`X-Forwarded-Host\`); nginx runs on the host, so its \`127.0.0.1\` peer address is inside the default \`TRUSTED_PROXY_CIDRS\`.` and update the paragraph `Run the stack **without** the caddy profile ...` to say the stack publishes `web` on `127.0.0.1:${WEB_PORT:-3000}` and `api` on `127.0.0.1:${API_PORT:-8080}` by default (no `.env` change needed for a same-host nginx).

- [ ] **Step 6: Edit `docs/self-hosting/upgrades.md`.** In `## Single-replica / near-zero-downtime notes` add two bullets after the first:

```markdown
- **Rate limits and shutdown assume that single `api` replica.** Limits are
  per process (`RATE_LIMIT_*`, see [Configuration](./configuration.md#platform-hardening)),
  so two replicas double every budget. On stop the API answers `503` on
  `/readyz`, waits `SHUTDOWN_DRAIN_DELAY`, then gives in-flight requests up to
  `SHUTDOWN_TIMEOUT` (default 30 s) before closing; SSE clients reconnect on
  their own.
- **Watch the drain in the logs** during `make self-host-up`:
  `api: shutdown requested; draining` … `api: shut down cleanly`. A
  `worker: shutdown timed out` line means a loop ignored its cancelled
  context past `SHUTDOWN_TIMEOUT` — the worker exits 1 and compose restarts it.
```

- [ ] **Step 7: Edit `docs/self-hosting/configuration.md`.** Add `API_BIND` / `WEB_BIND` rows to the Core table after `API_PORT`:

```markdown
| `API_BIND` / `WEB_BIND` | No | `127.0.0.1` | Host interface the `api`/`web` ports bind to. Loopback keeps them reachable only through a proxy on the same host; `0.0.0.0` exposes them on the LAN (then set `TRUST_PROXY=false`). |
```

Change the `POSTGRES_PASSWORD` row description to `Bundled \`db\` password — **change it**. The API refuses to boot in cloud mode (and warns loudly when \`SELF_HOSTED=true\`) when \`DATABASE_URL\` still carries \`change-me-please\` or \`calendium\`. Must match the password in \`DATABASE_URL\`.` and the `BETTER_AUTH_SECRET` row to `Better Auth signing/encryption secret, **at least 32 bytes** (\`openssl rand -base64 32\`). The web server refuses to start with a shorter value — in production, in \`bun run dev\`, everywhere. **Secret — never expose.**` Add the `PUBLIC_WEB_URL` row note: `Required by \`api\` in both modes; the \`worker\` errors without it in cloud mode and warns when self-hosted.` Then add a new section before `## Minimum viable configuration`:

```markdown
## Platform hardening

Reverse-proxy trust, rate limits, shutdown and logging. Every value has a safe
default; the compose file only overrides `TRUST_PROXY`.

| Variable | Where | Default | Description |
| --- | --- | --- | --- |
| `TRUST_PROXY` | api | `false` | Honour `X-Forwarded-For/Proto/Host` from peers inside `TRUSTED_PROXY_CIDRS`. `docker-compose.yml` sets `true` on `api`. Keep `false` when clients reach the API directly. |
| `TRUSTED_PROXY_CIDRS` | api | `127.0.0.0/8,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,::1/128,fc00::/7` | Comma-separated proxy allowlist; an invalid entry fails boot. Narrow it to your proxy's address when clients share the private network. |
| `RATE_LIMIT_PUBLIC_READ_PER_MIN` | api | `60` (burst 30) | Per client IP: public booking/poll/share GETs. |
| `RATE_LIMIT_PUBLIC_WRITE_PER_MIN` | api | `5` (burst 5) | Per client IP: public booking/poll POSTs. |
| `RATE_LIMIT_USER_PER_MIN` | api | `600` | Per user: every authenticated route not in a class below. `0` disables the class. |
| `RATE_LIMIT_MUTATE_HEAVY_PER_MIN` | api | `30` | Per user: sends, bulk actions, inbox zero, subscriptions, invitations, booking links, polls, thread shares, AI calls. |
| `RATE_LIMIT_SEARCH_PER_MIN` | api | `120` | Per user: search, attachment search, place autocomplete. |
| `SHUTDOWN_TIMEOUT` | api, worker | `30s` | Go duration. In-flight requests / loop passes get this long after SIGTERM; the worker exits 1 if a loop overruns it. |
| `SHUTDOWN_DRAIN_DELAY` | api | `0s` | How long `/readyz` answers `503 draining` before listeners close (set `2s`–`5s` behind a load balancer that polls readiness). |
| `LOG_FORMAT` | api, worker | `json` | `json` for log shippers, `text` for a terminal. Request lines carry `request_id` (also sent as `X-Request-Id`), method, route pattern, status, duration, bytes, client IP, user id and actor id; tokens and query strings are never logged. |
| `LOG_LEVEL` | api, worker | `info` | `debug`, `info`, `warn` or `error`. |
| `CSP_REPORT_ONLY` | web (runtime) | `false` | `true` switches the web app's Content-Security-Policy to report-only (violations are logged as `csp_violation` lines via `/api/csp-report`) without a rebuild. |

Rate limits are **per process**; the reference stack runs one `api` replica.
A limited request answers `429` with a `Retry-After` header; an oversized body
answers `413 payload_too_large` (1 MiB default, 10 MiB for drafts, 16 KiB on
public routes); a field over its limit answers `400 validation_failed` with
`details: {"field", "limit"}`.
```

(If Task 18 has not flipped the default yet, write `true` in the `CSP_REPORT_ONLY` default cell and in `.env.example`; Task 18 updates both.)

- [ ] **Step 8: Validate the compose file and re-run the check:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && docker compose config --quiet && echo compose-ok && grep -cE 'API_BIND|WEB_BIND|TRUST_PROXY|readyz|memory: 512m' docker-compose.yml && grep -c 'API_BIND' .env.example && grep -c 'TRUST_PROXY' docs/self-hosting/configuration.md docs/self-hosting/reverse-proxy-tls.md docs/self-hosting/security.md
```
Expected: `compose-ok`, `7` (API_BIND, WEB_BIND, TRUST_PROXY, readyz x2, memory x3 → counts lines: at least 7), `1`, and a non-zero count for each doc. (`docker compose config` only parses; it does not start anything.)

- [ ] **Step 9: Commit:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && git add docker-compose.yml .env.example docs/self-hosting/security.md docs/self-hosting/reverse-proxy-tls.md docs/self-hosting/upgrades.md docs/self-hosting/configuration.md && git commit -m "ops(compose): loopback publish, memory limits, /readyz health check, TRUST_PROXY; self-hosting docs for proxy trust, limits, shutdown and logging" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

---

### Task 21: FULL-SUITE GATE (after every track is merged)

**Files:** none modified (fix-forward commits only if a step fails).

- [ ] **Step 1: Go build, vet, race tests (incl. testcontainers):**

```bash
cd /Users/guilherme/Dev/pessoal/calendium/backend && go build ./... && go vet ./... && REQUIRE_DOCKER=1 go test -race ./... 2>&1 | tail -30
```
Expected: every package `ok`; `cmd/api` and `cmd/worker` now have tests.

- [ ] **Step 2: TypeScript suites:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && bun run test:shared && bun run test:web && bun run test:desktop && bun run test:mobile 2>&1 | tail -12
```
Expected: all green (web now includes the middleware, csp, security-headers, csp-report, auth-secret, instrumentation and route tests).

- [ ] **Step 3: Lint:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && bunx biome check . && (cd backend && golangci-lint run ./...) && (cd apps/desktop && golangci-lint run ./...) && bun run typecheck
```
Expected: no findings.

- [ ] **Step 4: Playwright, production build path, CSP as shipped:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && CI=1 bun run test:e2e 2>&1 | tail -15
```
Expected: all specs pass including `csp.spec.ts` (`next build` must print `ƒ` for the app routes and `○` for `/offline`).

- [ ] **Step 5: Images build with the pinned digests:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && docker build -f backend/Dockerfile -t calendium-backend:gate . 2>&1 | tail -2 && docker build -f apps/web/Dockerfile --build-arg NEXT_PUBLIC_API_URL=https://ci.invalid -t calendium-web:gate . 2>&1 | tail -2
```
Expected: both succeed.

- [ ] **Step 6: Spec grep — nothing left on the old paths:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && ! grep -rn 'alpine:3.20' backend/Dockerfile docs && ! grep -rn '"path", r.URL.Path' backend/internal/adapter/in/httpapi && ! grep -rn 'request_too_large' backend && grep -rn 'no-cache' backend/internal/adapter/in/httpapi/*.go | grep -v _test | wc -l
```
Expected: the three negated greps succeed silently and the last count is `0`.

---

### Task 22: VERIFICATION RUNBOOK — compose stack behind Caddy (manual; marks itself BLOCKED when operator inputs are absent)

**Files:** none modified. Record the outcome of each step in the task result.

- [ ] **Step 1: Preconditions (else BLOCKED, not failed).** A `.env` at the repo root with `TOKEN_ENCRYPTION_KEY` (64 hex), `BETTER_AUTH_SECRET` (≥ 32 bytes), a non-default `POSTGRES_PASSWORD` mirrored in `DATABASE_URL`, `SELF_HOSTED=true`, `DOMAIN=localhost`, `PUBLIC_WEB_URL=https://localhost`, `BETTER_AUTH_URL=https://localhost`, `NEXT_PUBLIC_API_URL=` (blank), `SHUTDOWN_DRAIN_DELAY=3s`; a Docker daemon; ports 80/443/3000/8080 free on the host (a live stack already using them → **BLOCKED: another stack is bound to the ports; do not stop it**). If `.env` is missing or any value above is absent: **BLOCKED: operator-supplied .env required** and stop.

- [ ] **Step 2: Boot and wait for health:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && docker compose --profile caddy up -d --build && for i in $(seq 1 30); do docker compose ps --format '{{.Service}} {{.Health}}' | tr '\n' ' '; echo; docker compose ps --format '{{.Health}}' api web | grep -qv healthy || break; sleep 5; done; docker compose ps
```
Expected: `api healthy`, `web healthy`, `db healthy`, `caddy running`; `docker compose logs api | grep -E 'api: config|warn'` shows the startup summary with `database=postgres://calendium:***@db:5432/...` and no `default password` warning.

- [ ] **Step 3: Headers through the proxy:**

```bash
curl -ksI https://localhost/v1/instance | grep -iE 'x-content-type-options|referrer-policy|x-frame-options|cache-control|strict-transport-security|x-request-id'
curl -ksI https://localhost/ | grep -iE 'content-security-policy|strict-transport-security|x-frame-options|permissions-policy|reporting-endpoints'
curl -ksI https://localhost/offline | grep -i 'content-security-policy'
curl -ks -o /dev/null -w '%{http_code}\n' https://localhost/readyz
curl -s http://127.0.0.1:8080/readyz
```
Expected: API line shows `nosniff`, `no-referrer`, `DENY`, `no-store`, `max-age=31536000; includeSubDomains` (Caddy sets `X-Forwarded-Proto: https`) and a 26-char `X-Request-Id`; web `/` shows `Content-Security-Policy` (or `-Report-Only` if Task 18 is still gated) with a `nonce-`, the five static headers and `Reporting-Endpoints: csp="/api/csp-report"`; `/offline` CSP contains `'unsafe-inline'`; `https://localhost/readyz` is `404` (not routed); the direct `/readyz` is `{"status":"ok"}`.

- [ ] **Step 4: Rate limit and request limits from outside:**

```bash
for i in $(seq 1 31); do curl -ks -o /dev/null -w '%{http_code} ' https://localhost/v1/public/booking/nope; done; echo
curl -ksI https://localhost/v1/public/booking/nope | grep -i retry-after
head -c 17000 /dev/zero | tr '\0' 'a' | curl -ks -o /dev/null -w '%{http_code}\n' -X POST -H 'Content-Type: application/json' --data-binary @- https://localhost/v1/public/booking/nope/bookings
```
Expected: thirty `404` then `429`; a `Retry-After` header; `413`. (`docker compose logs api | tail -3` shows `client_ip` as your host address, not Caddy's container IP.)

- [ ] **Step 5: Graceful stop with an in-flight request:**

```bash
cd /Users/guilherme/Dev/pessoal/calendium && (sleep 1; curl -s -o /dev/null -w 'in-flight during drain: %{http_code}\n' http://127.0.0.1:8080/v1/instance) & time docker compose stop api; docker compose logs --tail 20 api | grep -E 'draining|shut down cleanly|deadline exceeded'
```
Expected: `in-flight during drain: 200` (the 3 s drain delay keeps listeners open), `docker compose stop api` returns well under 30 s, and the log shows `api: shutdown requested; draining` followed by `api: shut down cleanly` with no `deadline exceeded` line. Then `docker compose start api` and confirm `api healthy` again.

- [ ] **Step 6: Worker stop:** `time docker compose stop worker; docker compose logs --tail 5 worker` — expected: returns within 30 s and ends with `worker: shut down cleanly` (never `worker: shutdown timed out`).

- [ ] **Step 7: Sandbox checkout under the CSP (gate for Task 18 if still pending).** If `apps/web/app/checkout/page.tsx` exists and Paddle sandbox credentials are in `.env`: open `https://localhost/checkout`, complete a sandbox purchase to `/checkout/success`, then `docker compose logs web | grep -c csp_violation` — expected `0`. Otherwise record **BLOCKED: Paddle sandbox checkout needs piece 1 merged and sandbox credentials**.

- [ ] **Step 8: Tear down:** `docker compose --profile caddy down` (keeps `db_data`). Report every expected/actual pair and any BLOCKED step verbatim.

---
