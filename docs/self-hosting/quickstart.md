# Quickstart — deploy Calendium in ~15 minutes

This is the blessed path: the repo-root [`docker-compose.yml`](../../docker-compose.yml)
brings up `db`, `api`, `worker`, `web`, and (optionally) a `caddy` reverse proxy
with automatic HTTPS. If you just want to poke at it on your laptop, jump to
[Run it locally without TLS](#run-it-locally-without-tls) at the bottom.

See also: [Configuration reference](./configuration.md) ·
[Pointing the apps at your server](./clients.md) ·
[Overview](./README.md).

---

## Before you start

You need:

- A Linux host with **Docker 29 + Compose v5**, `make`, and `openssl`
  (see [requirements](./README.md#requirements)).
- A domain with a **DNS A/AAAA record** pointing at the host, and ports
  **80 + 443** open (only needed for real HTTPS).
- No external auth service — authentication (**Better Auth**) is built into the
  web app; you just set a secret in step 4.

---

## 1. Clone and create your `.env`

```bash
git clone https://github.com/<you>/calendium.git
cd calendium
cp .env.example .env
```

Everything the stack needs comes from this single `.env` file. It is git-ignored
— keep your secrets there.

## 2. Generate the required secret

`TOKEN_ENCRYPTION_KEY` encrypts provider refresh tokens at rest (AES-256-GCM). It
must be **exactly 64 hex characters** (32 bytes) or the API refuses to boot.

```bash
make gen-secret        # prints a fresh key — copy it into .env
# equivalently: openssl rand -hex 32
```

Also set a real database password. In `.env`:

```dotenv
POSTGRES_PASSWORD=<a strong password>
TOKEN_ENCRYPTION_KEY=<paste the 64-hex-char key from make gen-secret>
```

> ⚠️ **Back up `TOKEN_ENCRYPTION_KEY` somewhere safe and keep it stable.** It is
> not stored in the database — lose it and every connected account must
> reconnect.

## 3. Set your instance identity and mode

```dotenv
SELF_HOSTED=true                       # unlocks all features, disables Stripe
INSTANCE_NAME=Acme Mail                 # shown to clients on the connect screen
DOMAIN=mail.example.com                 # the domain Caddy will serve
ACME_EMAIL=you@example.com              # Let's Encrypt expiry notices
APP_URL=https://mail.example.com
PUBLIC_WEB_URL=https://mail.example.com
```

Keep `DATABASE_URL` pointing at the bundled `db` service — the host is the
compose service name, and the password must match `POSTGRES_PASSWORD`:

```dotenv
DATABASE_URL=postgres://calendium:<same password>@db:5432/calendium?sslmode=disable
```

(`sslmode=disable` is correct here — traffic never leaves the private Docker
network. If you point at a *managed* Postgres instead, use `sslmode=require`.)

## 4. Set up authentication (Better Auth)

Authentication is **built into the web app** — [Better Auth](https://better-auth.com)
runs at `${BETTER_AUTH_URL}/api/auth/*` on the same Postgres you already
configured. There's **no external auth service** to create. Email + password
sign-in works out of the box; you just need a secret and your public URL:

```dotenv
BETTER_AUTH_SECRET=<openssl rand -base64 32>   # signing secret — keep it stable
BETTER_AUTH_URL=https://mail.example.com       # your public web origin, no trailing slash

# The browser reaches the API same-origin behind Caddy; set explicitly otherwise:
NEXT_PUBLIC_API_URL=https://mail.example.com   # or leave blank to use same-origin
```

The Go API verifies Better Auth's JWTs by fetching its JWKS **server-to-server**.
The bundled `docker-compose.yml` already points `AUTH_JWKS_URL` at the in-network
`http://web:3000/api/auth/jwks` (reachable from the `api`/`worker` containers,
unlike the public `BETTER_AUTH_URL`), and the backend derives `AUTH_ISSUER` from
`BETTER_AUTH_URL` — so you leave both blank unless your setup is unusual.

> **Want Google / Apple sign-in too?** Add `GOOGLE_CLIENT_ID/SECRET` and/or
> `APPLE_CLIENT_ID/SECRET`, and register the login redirect URIs
> `${BETTER_AUTH_URL}/api/auth/callback/google` and `.../callback/apple`. The
> Google app is the **same one** you use to connect Gmail/Calendar (step 5).
> Details: [Providers → Authentication](./providers.md#1-authentication-better-auth--built-in).

## 5. (Optional) connect Gmail / Outlook and other providers

To let users connect mail/calendar, register your **own** OAuth apps and add the
credentials. These are per-deployment — you cannot reuse Calendium Cloud's.

```dotenv
# Add your web origin (and any custom redirect targets) to the allowlist:
OAUTH_ALLOWED_REDIRECT_URIS=https://mail.example.com
GOOGLE_CLIENT_ID=...
GOOGLE_CLIENT_SECRET=...
MS_CLIENT_ID=...
MS_CLIENT_SECRET=...

# Optional AI (compose/reply/summarize):
OPENROUTER_API_KEY=...
```

When you register the Google/Microsoft apps, set their **authorized redirect
URI** to this API's own callback — `${PUBLIC_API_URL}/v1/accounts/callback/google`
(and `.../callback/microsoft`). Behind the bundled Caddy proxy the API shares your
domain under `/v1`, so that's `https://mail.example.com/v1/accounts/callback/google`;
`PUBLIC_API_URL` is derived from the request automatically there, but set it
explicitly if the API is on a different origin. Separately, make sure your web
origin is in `OAUTH_ALLOWED_REDIRECT_URIS` — that's where the browser is sent
*after* the callback completes. The full list of provider, push, and AI variables
is in the [configuration reference](./configuration.md). You can add these later
and `make self-host-up` again — unset providers are simply disabled.

## 6. Point DNS at your host

Create a DNS **A** record (and **AAAA** if you have IPv6) for `DOMAIN` → your
server's IP. Wait for it to propagate and make sure ports **80** and **443** are
open. Caddy needs this reachable before it can issue a certificate.

## 7. Start the stack

```bash
make self-host-up          # builds & starts db, api, worker, web + Caddy
# equivalent to: docker compose --profile caddy up -d --build
```

The `api` container applies the embedded SQL migrations on boot, then the
`worker` starts once `db` is healthy and `api` has started. Watch it come up:

```bash
make self-host-logs        # tail all services (Ctrl-C to stop tailing)
```

## 8. Verify

```bash
# Liveness (unauthenticated):
curl https://mail.example.com/healthz
# → 200 OK

# Instance discovery (unauthenticated) — confirms self-host mode + features:
curl https://mail.example.com/v1/instance
```

You should see something like:

```json
{
  "name": "Acme Mail",
  "mode": "self_host",
  "version": "0.1.0",
  "authBaseUrl": "https://mail.example.com/api/auth",
  "authProviders": ["email", "google"],
  "undoSendSeconds": 15,
  "features": { "billing": false, "google": true, "microsoft": true, "ai": true, "push": false }
}
```

`mode: self_host` and `features.billing: false` confirm the paywall is off and
everything is unlocked. The other feature flags reflect which credentials you
set. (Full contract: [configuration → discovery](./configuration.md#the-v1instance-discovery-contract).)

## 9. Connect a client

Open `https://mail.example.com` in a browser to use the web app, or point the
desktop/mobile apps at your server:

- **Web:** just visit your domain — the web app hosts Better Auth at `/api/auth`
  and already targets your API.
- **Desktop / Mobile:** on the **Connect** screen, choose *"Use a custom
  server"* and enter `https://mail.example.com`. The app calls `GET /v1/instance`
  to self-configure (Better Auth base URL, feature flags), then you sign in.

See [Pointing the apps at your server](./clients.md) for the full client flow and
for building the desktop/mobile apps from source.

---

## Run it locally without TLS

To kick the tires on your laptop without a domain or certificates, skip the Caddy
profile — `api` and `web` are published directly on the host (`8080` and `3000`).

In `.env` keep the localhost-friendly defaults:

```dotenv
SELF_HOSTED=true
DOMAIN=localhost
APP_URL=http://localhost:3000
PUBLIC_WEB_URL=http://localhost:3000
NEXT_PUBLIC_API_URL=http://localhost:8080
TOKEN_ENCRYPTION_KEY=<make gen-secret>
POSTGRES_PASSWORD=<anything>
DATABASE_URL=postgres://calendium:<same>@db:5432/calendium?sslmode=disable
# plus BETTER_AUTH_SECRET + BETTER_AUTH_URL from step 4
```

Start **without** the proxy:

```bash
make self-host-up PROFILE=        # docker compose up -d --build (no caddy)
curl http://localhost:8080/healthz
curl http://localhost:8080/v1/instance
```

Then open `http://localhost:3000`, or point desktop/mobile at
`http://localhost:8080`. (You can also run Caddy with `DOMAIN=localhost` for a
locally-trusted self-signed cert.)

Want to tear it down? `make self-host-down` stops the stack but keeps your data
(`db_data` volume).

---

Next: **[Configuration reference →](./configuration.md)** ·
**[Pointing the apps at your server →](./clients.md)**
