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
- A **Supabase project** (free Supabase Cloud is fine) — set up in step 4.

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

## 4. Set up Supabase authentication

Calendium uses Supabase for login on every client, and the backend verifies
Supabase JWTs locally. Create a project (the free tier is enough):

1. Create a project at [supabase.com](https://supabase.com) (or point at your
   own self-hosted Supabase/GoTrue).
2. In **Project Settings → API**, copy the **Project URL** and the **anon**
   (public) key.
3. In **Project Settings → API → JWT Settings**, copy the **JWT secret**.
4. Enable the sign-in methods you want (Google/Apple OAuth, email) under
   **Authentication → Providers**.

Fill these into `.env`:

```dotenv
# Backend verifies JWTs with the secret (HS256). Set this OR SUPABASE_JWKS_URL.
SUPABASE_URL=https://<ref>.supabase.co
SUPABASE_JWT_SECRET=<the JWT secret>
SUPABASE_ANON_KEY=<the anon/public key>       # served to clients via /v1/instance

# The web app bakes these into the browser bundle at build time:
NEXT_PUBLIC_SUPABASE_URL=https://<ref>.supabase.co
NEXT_PUBLIC_SUPABASE_ANON_KEY=<the anon/public key>
NEXT_PUBLIC_API_URL=https://mail.example.com  # or leave blank to use same-origin (behind Caddy)
```

`SUPABASE_ANON_KEY` is public and safe to serve — the desktop and mobile apps
fetch it from **your** server via `GET /v1/instance` so they can configure
Supabase per-instance (see [clients](./clients.md)).

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
URI** to your API callback, e.g. `https://mail.example.com/v1/accounts/callback/google`
(and `.../callback/microsoft`), and make sure the same origins are in
`OAUTH_ALLOWED_REDIRECT_URIS`. The full list of provider, push, and AI variables
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
  "supabaseUrl": "https://<ref>.supabase.co",
  "supabaseAnonKey": "<anon key>",
  "features": { "billing": false, "google": true, "microsoft": true, "ai": true, "push": false }
}
```

`mode: self_host` and `features.billing: false` confirm the paywall is off and
everything is unlocked. The other feature flags reflect which credentials you
set. (Full contract: [configuration → discovery](./configuration.md#the-v1instance-discovery-contract).)

## 9. Connect a client

Open `https://mail.example.com` in a browser to use the web app, or point the
desktop/mobile apps at your server:

- **Web:** just visit your domain — the image you built already targets your API
  and Supabase.
- **Desktop / Mobile:** on the **Connect** screen, choose *"Use a custom
  server"* and enter `https://mail.example.com`. The app calls `GET /v1/instance`
  to self-configure (Supabase creds, feature flags), then you sign in.

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
# plus your Supabase URL / anon key / JWT secret from step 4
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
