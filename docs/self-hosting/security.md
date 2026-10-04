# Security Hardening

Calendium stores people's mail and their providers' refresh tokens. Treat a
self-hosted instance like the sensitive system it is. This page is a checklist,
worked top to bottom.

---

## Checklist at a glance

- [ ] Real secrets in `.env`, never committed to git
- [ ] `TOKEN_ENCRYPTION_KEY` generated fresh, backed up, and kept stable
- [ ] `BETTER_AUTH_SECRET` treated as secret; the JWKS endpoint is public by design
- [ ] Only ports 80/443 exposed to the internet; Postgres never published
- [ ] TLS everywhere (Caddy auto-HTTPS or your own proxy)
- [ ] Firewall (UFW / cloud security group) with a default-deny inbound policy
- [ ] Base images updated on a schedule
- [ ] Backups encrypted and stored off-box
- [ ] Managed Postgres reached with `sslmode=require` (or stricter)
- [ ] `api` and `web` behind your proxy with `TRUST_PROXY=true` (the Compose default), and the proxy's address inside `TRUSTED_PROXY_CIDRS`
- [ ] `api`/`web` published on loopback; `TRUST_PROXY` only behind your own proxy
- [ ] `POSTGRES_PASSWORD` is not a default (the API refuses `change-me-please` / `calendium` in cloud mode)
- [ ] `ALLOW_DEV_ORIGINS` blank (false) in production
- [ ] SMTP configured over TLS (verification + password reset), or users know to ask you for a reset

---

## 1. Secrets management

**Never commit `.env`.** It holds your `BETTER_AUTH_SECRET`, provider client
secrets, Paddle keys (cloud only), and the token-encryption key. The repo's
`.gitignore` excludes `.env`; keep it that way and distribute secrets out of
band (a secrets manager, `scp`, your provider's secret store).

- Generate strong values: `make gen-secret` prints a fresh
  `TOKEN_ENCRYPTION_KEY`; use `openssl rand -base64 24` for `POSTGRES_PASSWORD`.
- **Change every default.** The shipped `.env.example` uses placeholders like
  `POSTGRES_PASSWORD=change-me-please` and an empty `TOKEN_ENCRYPTION_KEY`
  precisely so a copy-paste deploy *fails to boot* rather than running on known
  secrets. Booting with the example defaults is the classic self-host mistake.
- On managed platforms, prefer the native secret store (AWS Secrets Manager,
  GCP Secret Manager, Azure Key Vault) over a plaintext `.env` on disk.

---

## 2. `TOKEN_ENCRYPTION_KEY` handling

This 32-byte (64 hex char) key encrypts every stored provider refresh token with
AES-256-GCM. Config validation **refuses to boot** if it is missing or the wrong
length, so you can't accidentally run without it.

- **Back it up separately and permanently** (see [Backups](./backups.md)). It is
  *not* in the database. Lose it and every connected account must be
  re-authorized.
- **Keep it stable across deploys.** Changing it does not re-encrypt existing
  rows — it silently invalidates them.
- **Rotation is a deliberate operation, not a restart.** There is no built-in
  re-encryption command, so rotating the key today means existing provider
  tokens can no longer be decrypted and users must reconnect their accounts.
  Only rotate if the key is believed compromised, and communicate the reconnect
  to users. Store the old key until you've confirmed the new one works.

---

## 3. Auth secrets: what's secret vs. public

Better Auth (hosted by the web app at `${BETTER_AUTH_URL}/api/auth/*`) is the
identity provider for web, desktop, and mobile. Some of its config is meant to
be public and some is a signing secret — don't mix them up:

| Value | Exposure | Notes |
| --- | --- | --- |
| `BETTER_AUTH_URL` | **Public** | Public web origin; also the JWT issuer (`iss`) the backend pins. |
| `${BETTER_AUTH_URL}/api/auth/jwks` (JWKS) | **Public** | Public Ed25519 verification keys the backend fetches. Meant to be reachable — expected to be public, fine to expose. |
| `BETTER_AUTH_SECRET` | **SECRET** | Better Auth's root secret. Anyone with it can forge sessions and mint valid tokens. Never serve it, never log it, never put it in `NEXT_PUBLIC_*`. |
| `GOOGLE_CLIENT_SECRET`, `APPLE_CLIENT_SECRET`, `MS_CLIENT_SECRET`, `PADDLE_API_KEY`, `PADDLE_WEBHOOK_SECRET`, `SMTP_PASS` | **SECRET** | Backend / server-only. |

The Go backend is a pure resource server: it fetches the public JWKS from
`AUTH_JWKS_URL` (default `${BETTER_AUTH_URL}/api/auth/jwks`), verifies the
EdDSA (Ed25519) signature, and pins the issuer to `AUTH_ISSUER` (default
`BETTER_AUTH_URL`). Keeping the issuer pinned narrows what a stolen-token
attacker can present. The backend never holds `BETTER_AUTH_SECRET` — only the
web service (which hosts Better Auth) does, so guard it there.

---

## 4. Network exposure: keep Postgres private

The reference `docker-compose.yml` is built so **only the reverse proxy faces the
internet**:

- **`db` publishes no host ports.** Postgres is reachable only on the internal
  `calendium` Docker network, by service name (`db:5432`). Never add
  `ports: ["5432:5432"]` on a public host.
- **`api` and `web` publish on loopback only** — `127.0.0.1:${API_PORT:-8080}`
  and `127.0.0.1:${WEB_PORT:-3000}` — so only a proxy running on the same host
  (the bundled Caddy profile, or your own nginx/Traefik) can reach them. For a
  LAN box without any proxy set `API_BIND=0.0.0.0` / `WEB_BIND=0.0.0.0` in
  `.env` **and** `TRUST_PROXY=false`, otherwise any LAN client could spoof
  `X-Forwarded-For` and share (or dodge) another client's rate-limit bucket.
- **`TRUST_PROXY=true` is set on `api` and `web` by the compose file**
  (`${TRUST_PROXY:-true}`). Both then take the client IP — and the API also the
  scheme and host — from `X-Forwarded-*`, but only when the socket peer is
  inside `TRUSTED_PROXY_CIDRS` (default loopback + RFC 1918 + `fc00::/7`).
  Caddy replaces client-supplied `X-Forwarded-For` and the nginx sample sets
  it to `$remote_addr` (web) or appends with `$proxy_add_x_forwarded_for`
  (API), so the right-most untrusted hop is the real client in both setups. If
  other machines on the private network can reach port 8080 or 3000 directly,
  narrow `TRUSTED_PROXY_CIDRS` to the proxy's own address. Details:
  [Client IP](#client-ip-trust_proxy-and-trusted_proxy_cidrs).
- **`/readyz` is internal.** It reports database reachability (and `draining`
  during shutdown) for the compose health check; neither the Caddyfile nor the
  nginx sample routes it. `/healthz` stays the public liveness probe.

> **Docker bypasses UFW for published ports.** Docker inserts its own iptables
> NAT rules that skip UFW's `INPUT` chain, so a `ufw deny` will *not* block a
> published container port. Because the reference stack never publishes
> Postgres, the DB is safe regardless — but if you ever publish a port on a
> public host, bind it to `127.0.0.1` or install
> [`chaifeng/ufw-docker`](https://github.com/chaifeng/ufw-docker) and use the
> `DOCKER-USER` chain.

### Firewall baseline (Linux VPS)

```bash
sudo ufw default deny incoming
sudo ufw default allow outgoing
sudo ufw allow OpenSSH        # do this FIRST, or you lock yourself out
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw allow 443/udp        # HTTP/3
sudo ufw enable
```

On cloud platforms use the security group / NSG equivalent: SSH from your IP
only, 80/443 from anywhere, database port reachable **only** from the app's
security group (never `0.0.0.0/0`).

---

## 5. TLS

Serve every client over HTTPS. The bundled Caddy profile gets you auto-renewing
Let's Encrypt certificates with no manual steps — see
[Reverse Proxy & HTTPS](./reverse-proxy-tls.md). If you terminate TLS yourself,
forward `X-Forwarded-Proto` so the backend builds correct `https://` OAuth
redirect URLs. Native mobile apps generally refuse plaintext HTTP, so TLS isn't
optional in practice.

For **managed Postgres**, always use at least `sslmode=require` in
`DATABASE_URL` (RDS / Cloud SQL / Azure reject plaintext); `verify-full` with
the provider CA bundle is better. `sslmode=disable` is correct **only** for the
in-compose DB on the private Docker network.

---

## 6. OAuth redirect allowlist

`OAUTH_ALLOWED_REDIRECT_URIS` is a security control, not just config: it's the
server-side allowlist that the client-supplied return URL is checked against to
prevent open-redirect abuse of the provider-connect flow. Set it to exactly your
web origin(s) — nothing broader. Built-in defaults already cover localhost and
`calendium://`; don't add wildcards.

---

## 7. Keep base images current

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

```bash
docker compose pull          # refresh db + caddy
make self-host-up            # rebuild api/worker/web from updated base images
```

Both application images already run as a **non-root** user (`calendium` in the
backend image, `nextjs` in the web image) — keep it that way if you fork the
Dockerfiles.

---

## 8. Backups are part of security

A breach or ransomware event is a data-integrity problem too. Encrypt dumps
(`gpg`/`age`) before shipping them off-box, and store the passphrase separately.
See [Backups & Restore](./backups.md#encrypting-backups).

---

## 9. Billing is off on self-host (by design)

With `SELF_HOSTED=true`, the billing endpoints (`/v1/billing/checkout`,
`/v1/billing/portal`, `/v1/webhooks/paddle`) return `501` with a stable
`self_hosted` error, and all features are unlocked without Paddle. Leave the
`PADDLE_*` vars blank — there's no paywall to secure and no webhook secret to
protect on a self-hosted box.

---

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

### Client IP: `TRUST_PROXY` and `TRUSTED_PROXY_CIDRS`

The web app and the Go API find the client IP with the **same rule and the
same two variables**. The web app uses it for the sign-in limits above; the
API uses it for its per-IP limits on public endpoints (booking pages, polls
and shared threads: reads 60/min with a burst of 30, writes 5/min with a burst
of 5), for the `client_ip` in its request logs, and — together with
`X-Forwarded-Proto` / `X-Forwarded-Host`, which it honours only from a trusted
peer — for the OAuth callback origin. Compose sets `TRUST_PROXY` to
`${TRUST_PROXY:-true}` on both services.

The web app stamps the client IP into a server-only header
(`x-calendium-client-ip`) and overwrites it on every request, so a value the
client sends is discarded. The client IP is the **right-most `X-Forwarded-For`
entry that is not a trusted proxy**. The right-most entry is always the
immediate peer: the Docker image starts Next.js with a small preload
(`scripts/forwarded-for-peer.cjs`) that appends the socket address the
connection came from. If you run the web app outside the image (`next start`
on a host), start it the same way, `node -r ./scripts/forwarded-for-peer.cjs`,
or a client could forge the right-most entry; production logs a warning when
the preload is missing.

The API reads `X-Forwarded-For` straight from the request: it is used only
when the socket peer itself is inside `TRUSTED_PROXY_CIDRS`; otherwise the
socket peer is the client.

- `TRUST_PROXY=true` (the default under Docker Compose): entries inside
  `TRUSTED_PROXY_CIDRS` are skipped from the right, and the first entry outside
  them is the client. Entries that are not a bare IP address (`garbage`,
  `203.0.113.5:4321`, `[2001:db8::1]`) are skipped. When every entry is inside
  them (a LAN or VPN client behind the proxy), the web app takes the left-most
  entry (a forged left entry cannot win, because every hop to its right is a
  trusted proxy) and the API takes the socket peer. Behind the
  bundled Caddy each client therefore gets its own bucket. The default
  `TRUSTED_PROXY_CIDRS` is `127.0.0.0/8,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,::1/128,fc00::/7`
  (loopback and private ranges). It covers the bundled Caddy on the Compose
  network and nginx on the host in front of the published port. A client can
  prepend whatever it likes, but it cannot get past the entry its proxy added.
  Appending proxies (`$proxy_add_x_forwarded_for`, cloud load balancers) and
  overwriting ones (Caddy, nginx with `$remote_addr`) both work, as long as
  every proxy hop is inside `TRUSTED_PROXY_CIDRS`. Add a CDN's public ranges
  if one sits in front. If untrusted clients can reach `web:3000` or
  `api:8080` from a private range, narrow the list to your proxy's own
  address. An invalid entry stops the service at boot.
- `TRUST_PROXY=false` (the default when `web` or `api` runs outside Compose):
  no proxy is trusted, so only the immediate peer is used. A client-supplied
  `X-Forwarded-For` is never trusted, and the API ignores
  `X-Forwarded-Proto`/`Host`. Use it when `web:3000` / `api:8080` are exposed
  directly to a LAN with no proxy in front (`WEB_BIND`/`API_BIND=0.0.0.0`);
  with `true` a client on a private range could choose its own bucket. Behind a reverse proxy the immediate peer
  is the proxy, so every client shares the proxy's bucket on **all** auth
  endpoints: one client's typos, or an attacker's requests, block sign-in,
  session reads and JWT minting for everyone (and on the API, every visitor
  of a public booking page shares one bucket). That is why production logs a
  warning.

`TRUST_PROXY` accepts `true`/`1`/`yes` and `false`/`0`/`no`
(case-insensitive); any other value stops `web` or `api` at boot.

The supported production shape is Caddy (or nginx) + `TRUST_PROXY=true`.

Signed-in API traffic is limited per **user**, not per IP (600/min overall,
30/min for sends, bulk actions and AI calls, 120/min for search); see
[Configuration → Platform hardening](./configuration.md#platform-hardening).

### Origins

Better Auth accepts state-changing requests only from trusted origins:
`BETTER_AUTH_URL`, `PUBLIC_WEB_URL`, `CORS_ALLOWED_ORIGINS`, the desktop app's
WebView origins (`wails://wails`, `wails://wails.localhost`,
`http(s)://wails.localhost`), the mobile scheme `calendium://`, and
`https://appleid.apple.com` (Apple's `form_post`; never reflected in CORS).
The Go API's CORS always allows the desktop WebView origins plus
`CORS_ALLOWED_ORIGINS`, `PUBLIC_WEB_URL` and `BETTER_AUTH_URL`.
`http://localhost:*`, `http://127.0.0.1:*` and `[::1]` origins are allowed by
the API **only** with `ALLOW_DEV_ORIGINS=true`, in every environment. Better
Auth trusts `localhost` / `127.0.0.1` with `ALLOW_DEV_ORIGINS=true` and also
under `next dev`. Keep `ALLOW_DEV_ORIGINS` blank in production (setting it
makes `web` log a warning). The packaged desktop app needs no configuration.

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

---

## Related pages

- [Backups & Restore](./backups.md) — encrypting and off-boxing dumps
- [Reverse Proxy & HTTPS](./reverse-proxy-tls.md) — TLS and port exposure
- [Configuration & Environment Reference](./configuration.md) — every secret in one place
- [Provider & Integration Setup](./providers.md) — where each provider secret comes from
- [Troubleshooting](./troubleshooting.md) — diagnosing auth/redirect failures
