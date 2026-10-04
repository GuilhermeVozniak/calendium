# Troubleshooting

Symptom → cause → fix for the failures self-hosters actually hit. Start by
checking the two liveness endpoints and the logs:

```bash
curl -i https://your-domain/healthz          # API liveness (should be 200)
curl -s https://your-domain/v1/instance | jq  # discovery payload clients rely on
docker compose logs --tail=100 api worker web caddy
```

---

## Quick index

| Symptom | Jump to |
| --- | --- |
| Container won't start, `TOKEN_ENCRYPTION_KEY` error | [Boot fails: config validation](#boot-fails-config-validation) |
| `DATABASE_URL is required` / connection refused / SSL | [Database connection](#database-connection) |
| `migrate: apply ...` on startup | [Migration failure](#migration-failure) |
| Web sign-in works but API returns 401 | [JWT / issuer mismatch](#jwt--issuer-mismatch) |
| Client can't reach `/v1/instance` | [Clients can't discover the server](#clients-cant-discover-the-server) |
| Web app calls the wrong API URL / CORS | [Wrong API URL baked into web](#wrong-api-url-baked-into-web) |
| `redirect_uri_mismatch` connecting Gmail/Outlook | [OAuth redirect rejected](#oauth-redirect-rejected) |
| Infinite HTTPS redirects behind a proxy | [Redirect loop behind a proxy](#redirect-loop-behind-a-proxy) |
| No TLS certificate issued | [Certificate issuance fails](#certificate-issuance-fails) |
| Dead "Subscribe" screen on self-host | [Billing UI on self-host](#billing-ui-on-self-host) |
| `SMTP_*` startup error, "We couldn't send the email" | [Email not sending / SMTP errors](#email-not-sending--smtp-errors) |
| "Too many attempts, try again in N s" / `429` on sign-in | [Sign-in rate limits](#too-many-attempts-try-again-in-n-s-http-429) |
| `403 Invalid origin` on sign-in, CORS errors from a new origin | [Invalid origin](#invalid-origin-403-on-sign-in) |

---

## Boot fails: config validation

**Symptom:** the `api` (and `worker`) container exits immediately; logs show
`TOKEN_ENCRYPTION_KEY is required` or
`TOKEN_ENCRYPTION_KEY must be exactly 64 hex chars (32 bytes)`.

**Cause:** the key is empty or the wrong length. `FromEnv` validates it at boot
and refuses to start otherwise (also for a missing `DATABASE_URL`, a
non-boolean `SELF_HOSTED`, or a bad `UNDO_SEND_SECONDS`).

**Fix:** generate exactly 32 bytes of hex and set it in `.env`:

```bash
make gen-secret            # or: openssl rand -hex 32
# -> paste into TOKEN_ENCRYPTION_KEY=...
docker compose up -d
```

Don't quote it or add spaces — it must decode to exactly 32 bytes.

---

## Database connection

**Symptoms & fixes:**

- `DATABASE_URL is required` — the var is empty. In the bundled stack it should
  be `postgres://calendium:<password>@db:5432/calendium?sslmode=disable`, with
  the host `db` (the compose service name) and the password matching
  `POSTGRES_PASSWORD`.
- **`connection refused` / `dial tcp` to `db`** — the API started before
  Postgres was ready. The compose file already gates `api`/`worker` on
  `db: condition: service_healthy`, so this usually means the DB itself failed
  its healthcheck. Check `docker compose logs db` (bad `POSTGRES_PASSWORD`,
  corrupt `db_data` volume, disk full).
- **`password authentication failed`** — `DATABASE_URL`'s credentials drifted
  from `POSTGRES_USER`/`POSTGRES_PASSWORD`. Keep them in sync. If you changed
  the password *after* the volume was first initialized, Postgres kept the old
  one — either set it back or reset the volume (destroys data).
- **`SSL is not enabled on the server` or TLS errors against managed Postgres**
  — `sslmode=disable` is only correct for the in-compose DB. For RDS / Cloud SQL
  / Azure use `sslmode=require` (or `verify-full` with the provider CA bundle).
- **`prepared statement "..." does not exist`** — you're behind a
  transaction-mode pooler (PgBouncer, Supabase Supavisor `:6543`, RDS Proxy).
  Append `default_query_exec_mode=simple_protocol` to `DATABASE_URL`, or connect
  to the direct `:5432` port. Not needed for the bundled DB.

---

## Migration failure

**Symptom:** `api` logs `migrate: apply 000X_...sql: ...` and the container
exits.

**Cause:** a migration couldn't apply — almost always because the schema was
edited by hand, or a partial/failed prior run left conflicting objects.
Migrations are embedded, applied at boot, tracked in `schema_migrations`, and
run under an advisory lock, so concurrent api+worker startup is *not* the cause.

**Fix:** read the specific SQL error. If it's from a manual change, reconcile it
or restore your pre-upgrade snapshot (`make db-restore FILE=...`) and retry. Each
migration runs in its own transaction, so a failure leaves the database at the
last good version — you won't get a half-applied migration. See
[Upgrading → How migrations run](./upgrades.md#how-migrations-run).

---

## JWT / issuer mismatch

**Symptom:** users sign in fine in the web app (the Better Auth session works)
but every `Authorization: Bearer` call to `/v1/*` returns `401`.

Auth is **Better Auth**, hosted by the *web* service at
`${BETTER_AUTH_URL}/api/auth/*`. The Go API is a pure resource server: it fetches
the public JWKS and verifies the EdDSA (Ed25519) token locally. A 401 *after* a
good login almost always means the API can't verify the token, not that login
failed.

**Causes & fixes:**

- **The API can't reach the JWKS endpoint.** The backend fetches public keys
  from `AUTH_JWKS_URL` **server-to-server**, so it must be reachable from the
  `api`/`worker` containers to the `web` service — not just from a browser. The
  bundled `docker-compose.yml` defaults it to the in-network
  `http://web:3000/api/auth/jwks`, which works out of the box — **leave
  `AUTH_JWKS_URL` blank in `.env`**. A 401 here usually means you *overrode* it
  wrongly: pointing it at the public `BETTER_AUTH_URL` (the `api` container can't
  loop back through your proxy) or at a host the container can't resolve. Either
  clear it to fall back to the compose default, or set a URL the `api` container
  can actually reach. Test from inside the container:
  `docker compose exec api wget -qO- "$AUTH_JWKS_URL"` — you should get a JSON
  JWKS containing `"kty":"OKP","crv":"Ed25519"`.
- **Issuer pin mismatch.** The backend requires the token `iss` to equal
  `AUTH_ISSUER` (default `BETTER_AUTH_URL`). If `AUTH_ISSUER` doesn't match the
  `BETTER_AUTH_URL` the web app is configured with, every token is rejected.
  Keep `AUTH_ISSUER` and `BETTER_AUTH_URL` the same public origin, with **no
  trailing slash**.
- **"Auth works in the web app but the API returns 401."** This is the classic
  shape of the two problems above: the browser holds a valid Better Auth session
  (so same-origin `/api/auth/*` and login work), but the API rejects the bearer
  token because it can't fetch JWKS or the issuer doesn't match. Check
  `AUTH_JWKS_URL` reachability first, then `AUTH_ISSUER`.
- **Clock skew** on the server can invalidate the short-lived (default 15m)
  token's `exp`/`iat`. Ensure NTP is running.

---

## Clients can't discover the server

**Symptom:** a desktop/mobile client (or your browser) can't load
`GET /v1/instance`, so the "connect to your server" step fails.

**Causes & fixes:**

- **Proxy routing.** `/v1/*` must route to `api:8080`. With the bundled Caddy
  this is automatic; with nginx/Traefik confirm the `/v1/` location points at
  the API upstream, not web. Test directly:
  `curl -i https://your-domain/v1/instance`.
- **It's unauthenticated — a 401 here means misrouting.** `/v1/instance` is
  registered *outside* the auth middleware. If you get 401, the request is
  hitting an authenticated route instead (wrong path or a proxy rewrite
  mangling `/v1/instance`).
- **TLS / mixed content.** Native apps require HTTPS. A self-signed or
  untrusted cert makes the client refuse the connection — use a real domain +
  Caddy, or trust your internal CA on the device.

---

## Wrong API URL baked into web

**Symptom:** the web app calls `http://localhost:8080` (or the old URL) in
production, or you see CORS/blocked-request errors in the browser console.

**Cause:** `NEXT_PUBLIC_API_URL` is inlined into the browser bundle **at build
time**. Setting it only as runtime env does nothing — the old value is already
compiled in. (Better Auth needs no `NEXT_PUBLIC_*` var: the web app talks to it
same-origin at `/api/auth`, and `BETTER_AUTH_SECRET`/`BETTER_AUTH_URL` are read
at runtime.)

**Fix:** set it in `.env` and **rebuild** the web image:

```bash
# behind the bundled proxy, this is just your domain (API lives under /v1):
NEXT_PUBLIC_API_URL=https://your-domain
docker compose up -d --build web     # or: make self-host-up
```

Note there's usually **no CORS to configure**: behind the proxy the web app and
API share one origin (`https://your-domain`, API under `/v1`). CORS errors are a
symptom that the web app is pointed at a *different* origin than it's served
from — fix the URL rather than loosening CORS. When you *deliberately* serve the
web app and API from different origins (or a third-party browser client hits the
API), add those origins to `CORS_ALLOWED_ORIGINS` — the desktop app's
`wails://wails.localhost` origin and localhost dev origins are already allowed.

---

## OAuth redirect rejected

**Symptom A — at the provider:** Google shows `redirect_uri_mismatch`, or
Microsoft shows `AADSTS50011`, when connecting a mailbox.

**Cause/fix:** the backend callback URL isn't registered in the provider
console. This is the **API's own** callback — `${PUBLIC_API_URL}/v1/accounts/callback/{provider}`
(behind the bundled proxy, your domain under `/v1`). Register it **exactly**:

```
https://your-domain/v1/accounts/callback/google
https://your-domain/v1/accounts/callback/microsoft
```

in Google Cloud (Authorized redirect URIs) and Microsoft Entra (Web redirect
URI). Scheme, host, and path must match character-for-character. See
[Providers](./providers.md).

**Symptom B — after the provider:** the flow completes at Google/Microsoft but
Calendium then refuses to redirect the browser back to your web app.

**Cause/fix:** the client's return URL isn't in the server allowlist. Add your
web origin to `OAUTH_ALLOWED_REDIRECT_URIS` (comma-separated, appended to the
built-in `localhost` + `calendium://` defaults) and restart the API:

```bash
OAUTH_ALLOWED_REDIRECT_URIS=https://your-domain
```

**Symptom C — social sign-in (Google / Apple *login*):** the Google or Apple
consent screen shows `redirect_uri_mismatch` when a user tries to **sign in**
(not connect a mailbox).

**Cause/fix:** Better Auth's social callback isn't registered on the OAuth
client. Better Auth handles sign-in at the web app, so the callback lives under
`/api/auth`, not `/v1`. Register these **exactly**:

```
https://your-domain/api/auth/callback/google
https://your-domain/api/auth/callback/apple
```

Because `GOOGLE_CLIENT_ID`/`GOOGLE_CLIENT_SECRET` are **shared** between login
and mailbox-connect, the *same* Google OAuth client must list **both**
`/api/auth/callback/google` (Better Auth sign-in) and
`/v1/accounts/callback/google` (mailbox connect) as authorized redirect URIs.

---

## Redirect loop behind a proxy

**Symptom:** the browser bounces between `http` and `https` forever, or OAuth
URLs come back as `http://` behind your `https://` proxy.

**Cause:** your proxy isn't telling the backend the original scheme.

**Fix:** forward `X-Forwarded-Proto` (and `X-Real-IP` / `X-Forwarded-For`). The
bundled Caddy and the sample `deploy/nginx/calendium.conf` already do. If you
wrote your own nginx/Traefik config, add:

```nginx
proxy_set_header X-Forwarded-Proto $scheme;
proxy_set_header X-Real-IP $remote_addr;
proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
```

This is the single most common self-host support ticket.

---

## Certificate issuance fails

**Symptom:** Caddy logs ACME errors; the site has no valid certificate.

**Causes & fixes:**

- **DNS not pointing at the box yet.** The `A`/`AAAA` record for `your-domain`
  must resolve to the server's public IP *before* you `up`. Verify with
  `dig +short your-domain`.
- **Ports 80/443 not reachable.** Let's Encrypt validates over 80/443 — open
  them in your firewall/security group and make sure nothing else is bound to
  them on the host.
- **`DOMAIN` unset or `localhost`.** With `DOMAIN=localhost` Caddy issues a
  local internal cert (no public ACME). Set a real `DOMAIN` in `.env`.
- **Rate-limited.** If you wiped the `caddy_data` volume and restarted many
  times, you may be temporarily blocked by Let's Encrypt. Wait it out, and keep
  the `caddy_data` volume so certs persist across restarts.

---

## Billing UI on self-host

**Symptom:** a self-hosted user lands on a dead "Subscribe" screen, or
`POST /v1/billing/checkout` returns `501`.

**This is expected.** With `SELF_HOSTED=true`, billing is disabled: checkout,
portal, and the Paddle webhook return
`501 {"error":{"code":"self_hosted","message":"Billing is disabled on self-hosted instances."}}`,
and `GET /v1/billing/subscription` reports an active annual plan so clients treat
the user as fully entitled. `GET /v1/instance` advertises `features.billing:
false` so up-to-date clients **hide** the subscribe UI entirely.

**If you still see a subscribe screen:** confirm `SELF_HOSTED=true` is actually
in effect (`curl -s https://your-domain/v1/instance | jq '.mode, .features.billing'`
should show `"self_host"` and `false`), and that your web client was **rebuilt**
after setting it — a stale bundle may still show cloud-only UI.

---

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
If *everyone* hits them at once (including `/api/auth/get-session` and
`/api/auth/token`, so pages bounce to sign-in and API calls fail with 401), the
web app cannot tell clients apart and they share one bucket. Compose defaults
`TRUST_PROXY` to `true`, so behind the bundled Caddy each client normally gets
its own bucket. With `TRUST_PROXY=false` (set explicitly in `.env`, or `web`
run outside Compose) it uses only the immediate peer, which behind a proxy is
the proxy itself, and the `web` log shows `TRUST_PROXY is not true in
production: …` at startup. With `TRUST_PROXY=true` the same happens when a
proxy hop is outside `TRUSTED_PROXY_CIDRS`, for example a CDN with public
addresses.

The Go API answers its own `429`s with a `Retry-After` header: per client IP
on public booking/poll/share pages, per user on signed-in routes
([Configuration → Platform hardening](./configuration.md#platform-hardening)).
It finds the client IP with the same `TRUST_PROXY` / `TRUSTED_PROXY_CIDRS`
rule, so if every visitor of a public page is limited at once the cause is the
same.

**Fix:** behind a proxy, remove `TRUST_PROXY=false` from `.env` (or set it to
`true`), add any public proxy ranges to `TRUSTED_PROXY_CIDRS`, and restart `web`
and `api`. To clear the web counters (for example after a
load test): `docker compose exec db psql -U calendium -d calendium -c 'DELETE FROM "rateLimit";'`.
The API's counters live in memory and reset when `api` restarts.

---

## "Invalid origin" (403) on sign-in

**Symptom:** sign-in from a browser, desktop or mobile client fails with `403`
`Invalid origin` (the `web` log names the rejected origin), or the browser blocks
API calls with a CORS error.

**Cause:** the request's `Origin` is not trusted. Production trusts
`BETTER_AUTH_URL`, `PUBLIC_WEB_URL`, `CORS_ALLOWED_ORIGINS`, the desktop WebView
origins, `calendium://` and `https://appleid.apple.com`; `localhost` /
`127.0.0.1` origins only with `ALLOW_DEV_ORIGINS=true`. The API's CORS applies
that localhost rule in every environment, including development.

**Fix:** make `BETTER_AUTH_URL` and `PUBLIC_WEB_URL` exactly your public origin
(scheme, host and port; no trailing slash), add any other web origin to
`CORS_ALLOWED_ORIGINS`, and restart `web` and `api`. For a local dev web app
pointed at a production server, `ALLOW_DEV_ORIGINS=true` works (and logs a
warning).

---

## Still stuck?

- Re-read the [Configuration & Environment Reference](./configuration.md) — most
  issues are one wrong env var.
- Confirm the whole stack is healthy: `docker compose ps` (every service `Up`,
  `db` healthy).
- Check the [Architecture](../architecture.md) doc for how a request flows
  through auth → service → adapter.

---

## Related pages

- [Configuration & Environment Reference](./configuration.md)
- [Provider & Integration Setup](./providers.md)
- [Reverse Proxy & HTTPS](./reverse-proxy-tls.md)
- [Upgrading](./upgrades.md) · [Backups & Restore](./backups.md) · [Security](./security.md)
