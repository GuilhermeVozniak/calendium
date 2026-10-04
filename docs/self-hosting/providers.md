# Provider & Integration Setup

Calendium talks to a handful of external services. This page walks through
setting up every one of them for a **self-hosted** instance, which values to put
in your `.env`, and — importantly — **what breaks if you skip each one**.

Only one thing is truly required for a *usable* instance:

1. **At least one mail/calendar provider** (Google *or* Microsoft) — otherwise
   there is no mailbox to sync.

Authentication is **built in**: [Better Auth](https://better-auth.com) runs inside
the web app on your own Postgres, so email + password sign-in works out of the box
with no external service. Social **Google / Apple** login is an optional add-on
(§1).

Everything else (AI, push) is optional; the composition root simply leaves the
unconfigured adapter unwired and the app boots fine without it. The public
[`GET /v1/instance`](#how-clients-discover-what-you-configured) endpoint reports
exactly which of these you configured so the apps can hide the UI they can't use.

| Integration | Required? | Env vars | What breaks if unset |
| --- | --- | --- | --- |
| Authentication (Better Auth) | Built in | `BETTER_AUTH_SECRET`, `BETTER_AUTH_URL` (+ `GOOGLE_*`/`APPLE_*` for social login) | Web app won't start without `BETTER_AUTH_SECRET`; without social creds only email + password is available |
| Google OAuth app | One provider required | `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` | Can't connect Gmail / Google Calendar; `features.google=false` |
| Microsoft Entra app | One provider required | `MS_CLIENT_ID`, `MS_CLIENT_SECRET` | Can't connect Outlook / Microsoft 365; `features.microsoft=false` |
| OpenRouter (AI) | Optional | `OPENROUTER_API_KEY`, `OPENROUTER_MODEL` | `POST /v1/ai/compose` disabled; `features.ai=false` |
| APNs / FCM / Web Push | Optional | see [Push](#5-push-notifications--optional) | No push notifications; `features.push=false` |

> Provider apps are **per-deployment**. You register your *own* Google Cloud and
> Microsoft Entra OAuth apps — you cannot reuse Calendium Cloud's. This is the
> single most common self-host gotcha, so follow the redirect-URI steps exactly.

---

## 1. Authentication (Better Auth — built in)

Calendium's identity provider is **[Better Auth](https://better-auth.com)**, and
it runs **inside the Next.js web app** at `${BETTER_AUTH_URL}/api/auth/*`, backed
by the **same Postgres** as the Go API (its own tables: `user`, `session`,
`account`, `verification`, `jwks`). There is **no external auth service to run** —
the big self-hosting win over the old Supabase setup.

Clients sign in against Better Auth, which mints a short-lived (15m) EdDSA
(Ed25519) JWT; they send it to the Go API as `Authorization: Bearer <jwt>`. The
backend is a pure **resource server**: it fetches Better Auth's public keys from
`${BETTER_AUTH_URL}/api/auth/jwks` and verifies the signature locally — it never
holds a shared secret and never calls out to validate a token.

**Email + password works out of the box** with zero extra configuration. Social
**Google** and **Apple** sign-in are optional add-ons (§1c).

### 1a. Required: the Better Auth secret & URL

Set these two in `.env` — the web app reads them at **runtime** (they are *not*
`NEXT_PUBLIC_*` and are never shipped to the browser):

| `.env` var | What | Secret? |
| --- | --- | --- |
| `BETTER_AUTH_SECRET` | Signing/encryption secret for Better Auth. Generate with `openssl rand -base64 32`. | **SECRET** — never expose |
| `BETTER_AUTH_URL` | Public web origin where Better Auth is reachable (your domain as an `https://` URL, **no trailing slash**). Dev: `http://localhost:3000`. | public |

```bash
BETTER_AUTH_SECRET=$(openssl rand -base64 32)
BETTER_AUTH_URL=https://your-domain
```

The Go backend derives its verification settings from `BETTER_AUTH_URL`:
`AUTH_JWKS_URL` defaults to `${BETTER_AUTH_URL}/api/auth/jwks` and `AUTH_ISSUER`
(the pinned token `iss`) to `${BETTER_AUTH_URL}`. Set them explicitly only if the
JWKS is served from a different origin than the issuer Better Auth stamps.

### 1b. The database schema (no JS migration step)

Better Auth's tables live in the **same Postgres** as everything else and are
created by the **Go migrate runner at boot** from
[`backend/migrations/0002_better_auth.sql`](../../backend/migrations/0002_better_auth.sql)
(`user`, `session`, `account`, `verification`, `jwks`). You do **not** run any
Node/JS migration at deploy — bringing up the `api` container owns the whole
schema.

If you upgrade Better Auth and its schema changes, regenerate that file from the
web app's auth config and fold the diff back in:

```bash
bunx @better-auth/cli generate --config apps/web/lib/auth.ts
# review the output, then update backend/migrations/0002_better_auth.sql
```

### 1c. Optional: Google & Apple social sign-in

Email + password needs nothing more. To offer **"Continue with Google/Apple"**,
register OAuth apps and set their credentials — Better Auth advertises each
provider in `GET /v1/instance` (`authProviders`) only when both its id and secret
are present.

| `.env` var | For |
| --- | --- |
| `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET` | Google sign-in (**shared** with Gmail/Calendar mailbox-connect — see §2) |
| `APPLE_CLIENT_ID` / `APPLE_CLIENT_SECRET` | Sign in with Apple (Services ID + secret) |

Register these **login** redirect URIs on the respective OAuth apps:

```
${BETTER_AUTH_URL}/api/auth/callback/google
${BETTER_AUTH_URL}/api/auth/callback/apple
```

> **One Google app for both jobs.** The Google OAuth client used for *login* here
> is the same one used to *connect a Gmail/Calendar mailbox* (§2). Register
> **both** redirect URIs on it: the Better Auth login callback above **and** the
> backend mailbox-connect callback `https://YOUR_DOMAIN/v1/accounts/callback/google`.

> **Apple posts back to Better Auth.** Sign in with Apple returns with a
> cross-site `form_post` from `https://appleid.apple.com`. That origin is always
> in Better Auth's trusted origins (it is never reflected in CORS), so there is
> nothing to configure — but the callback only works on your real HTTPS domain;
> Apple rejects `localhost`.

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

### How clients discover what you configured

Desktop and mobile clients only know your **server URL**. They call the
unauthenticated:

```http
GET /v1/instance
```

which returns your instance's public config so the apps can build their Better
Auth client and hide features you didn't enable:

```json
{
  "name": "Calendium",
  "mode": "self_host",
  "version": "0.1.0",
  "authBaseUrl": "https://your-domain/api/auth",
  "authProviders": ["email", "google", "apple"],
  "undoSendSeconds": 15,
  "features": { "billing": false, "google": true, "microsoft": false, "ai": true, "push": false, "email": true }
}
```

`authBaseUrl` is `${PUBLIC_WEB_URL||APP_URL}/api/auth` — the base a client builds
its Better Auth client against (e.g. `${authBaseUrl}/jwks`, the sign-in
endpoints). `authProviders` always includes `"email"`, plus `"google"`/`"apple"`
when you configured their credentials. See
[Pointing the Apps at Your Server](./clients.md) for the client flow.
`features.email` is `true` when an SMTP sender is configured; the web forgot-password page falls back to the administrator instructions only when it is `false`.

---

## 2. Google Cloud OAuth app (Gmail + Google Calendar)

Connecting a Gmail mailbox is a **separate OAuth flow** from login — the backend
runs its own Google OAuth to get *offline* access (a refresh token) to mail and
calendar, which it stores AES-256-GCM-encrypted in Postgres.

### 2a. Create the app

1. Open <https://console.cloud.google.com/> and create (or pick) a project.
2. **APIs & Services → Library**: enable **Gmail API** and **Google Calendar API**.
3. **APIs & Services → OAuth consent screen**: choose **External**, fill in the
   app name/support email, and add the scopes Calendium requests (below).
4. **APIs & Services → Credentials → Create credentials → OAuth client ID →
   Web application**.
5. Under **Authorized redirect URIs**, add this API's own callback exactly —
   `${PUBLIC_API_URL}/v1/accounts/callback/google`. Behind the bundled Caddy
   proxy the API shares your domain under `/v1`, so that's:

   ```
   https://YOUR_DOMAIN/v1/accounts/callback/google
   ```

   For local testing also add `http://localhost:8080/v1/accounts/callback/google`.
6. Copy the **Client ID** and **Client secret** into `.env`:

   ```bash
   GOOGLE_CLIENT_ID=xxxx.apps.googleusercontent.com
   GOOGLE_CLIENT_SECRET=xxxx
   ```

### 2b. Scopes the backend requests

Exactly these (from `backend/internal/adapter/out/googleapi/oauth.go`):

| Scope | Why |
| --- | --- |
| `https://www.googleapis.com/auth/gmail.modify` | Read, label, archive, trash, and send mail |
| `https://www.googleapis.com/auth/calendar` | Read/write calendars & events |
| `openid`, `email` | Identify the connected account |

The consent URL is requested with `access_type=offline` and `prompt=consent`, so
Google always returns a refresh token.

> **Verification note:** `gmail.modify` and `calendar` are *restricted/sensitive*
> scopes. While your consent screen is in **Testing** mode you can add your own
> Google accounts as **Test users** and connect immediately. To let arbitrary
> users connect (Production), Google requires app verification. For a personal or
> small self-host, staying in Testing with a handful of test users is usually all
> you need.

### 2c. Two redirect URLs — don't mix them up

The connect flow uses **two different** URLs:

- **Provider redirect URI** (registered at Google/Microsoft, step 5 above):
  `${PUBLIC_API_URL}/v1/accounts/callback/{provider}` — the backend's **own**
  callback, where the provider sends the authorization code. `PUBLIC_API_URL` is
  the public origin of the API; leave it blank to have the backend derive
  `scheme://host` from the connect request (honoring `X-Forwarded-Proto`/`Host`),
  which is correct behind the bundled proxy. Set it explicitly if the API is on a
  different origin than the request the client sends.
- **Client return URL** — where the backend redirects the browser **after** it
  has exchanged the code and stored the tokens (e.g. your web app's
  `/settings?status=connected`). It's validated against a server-side allowlist
  to prevent open-redirect abuse.

Built-in allowlist defaults already cover `http://localhost`, `https://localhost`,
`http://127.0.0.1`, and `calendium://` (the desktop/mobile return scheme). **Add
your production web origin:**

```bash
OAUTH_ALLOWED_REDIRECT_URIS=https://YOUR_DOMAIN
```

This is comma-separated and *appended* to the defaults. If you omit it, the
provider-connect flow will reject your web app's return URL. See
[Troubleshooting → OAuth redirect rejected](./troubleshooting.md#oauth-redirect-rejected).

**What breaks without Google:** users can't connect a Gmail/Google Calendar
account; `features.google` reports `false` and the clients hide the Google
connect button.

---

## 3. Microsoft Entra app (Outlook / Microsoft 365)

The Microsoft mailbox flow mirrors Google, against Microsoft Graph.

### 3a. Create the app

1. Open <https://entra.microsoft.com/> → **Identity → Applications → App
   registrations → New registration**.
2. **Supported account types:** choose *Accounts in any organizational directory
   and personal Microsoft accounts* — the backend authorizes against the
   `/common` endpoint, so multi-tenant + personal is the matching choice.
3. **Redirect URI:** platform **Web**, value
   `${PUBLIC_API_URL}/v1/accounts/callback/microsoft` (behind the bundled proxy,
   your domain under `/v1`):

   ```
   https://YOUR_DOMAIN/v1/accounts/callback/microsoft
   ```
4. **Certificates & secrets → New client secret** → copy the *Value* (not the ID).
5. **API permissions → Add a permission → Microsoft Graph → Delegated** and add:
   `Mail.ReadWrite`, `Mail.Send`, `Calendars.ReadWrite`, `offline_access`,
   `openid`, `email`.
6. Fill `.env`:

   ```bash
   MS_CLIENT_ID=<Application (client) ID>
   MS_CLIENT_SECRET=<the secret Value>
   ```

### 3b. Scopes the backend requests

Exactly these (from `backend/internal/adapter/out/msgraph/oauth.go`):

`offline_access`, `openid`, `email`,
`https://graph.microsoft.com/Mail.ReadWrite`,
`https://graph.microsoft.com/Mail.Send`,
`https://graph.microsoft.com/Calendars.ReadWrite`.

Also add your web origin to `OAUTH_ALLOWED_REDIRECT_URIS` exactly as in §2c (the
same variable serves both providers).

**What breaks without Microsoft:** users can't connect an Outlook/Microsoft 365
account; `features.microsoft` reports `false`.

---

## 4. OpenRouter (AI compose) — optional

The AI compose/reply/summarize endpoint (`POST /v1/ai/compose`) is backed by
[OpenRouter](https://openrouter.ai/).

```bash
OPENROUTER_API_KEY=sk-or-...
OPENROUTER_MODEL=openrouter/auto   # default; pick any model slug OpenRouter offers
```

`OPENROUTER_MODEL` defaults to `openrouter/auto` when blank. **What breaks
without it:** the AI endpoint is disabled and `features.ai` reports `false`; the
apps hide AI actions. Everything else works normally.

---

## 5. Push notifications — optional

Calendium fans push out to three transports; configure any subset. If **none**
are configured, `features.push=false` and clients simply don't receive
background notifications (the app still works — you just won't get new-mail /
reminder alerts when it's closed).

The backend treats push as "enabled" if **any** of these is present:
`APNS_KEY_P8`, `FCM_SERVICE_ACCOUNT_JSON`, or **both** `VAPID_PUBLIC_KEY` and
`VAPID_PRIVATE_KEY`.

### 5a. Apple Push (APNs) — iOS / macOS

HTTP/2 + ES256 JWT auth.

1. In the [Apple Developer](https://developer.apple.com/account/resources/authkeys/list)
   portal → **Keys → New key**, enable **Apple Push Notifications service (APNs)**,
   create it, and download the `AuthKey_XXXXXXXXXX.p8` (you can only download it
   once). Note the **Key ID** and your **Team ID**.
2. `.env`:

   ```bash
   APNS_KEY_ID=XXXXXXXXXX
   APNS_TEAM_ID=YYYYYYYYYY
   # PEM *contents* of the .p8 with newlines escaped as \n — NOT a file path:
   APNS_KEY_P8=-----BEGIN PRIVATE KEY-----\nMIGT...\n-----END PRIVATE KEY-----\n
   ```

   Produce the single-line escaped value:

   ```bash
   awk 'BEGIN{ORS="\\n"}1' AuthKey_XXXXXXXXXX.p8
   ```

### 5b. Firebase Cloud Messaging (FCM v1) — Android

1. In the [Firebase console](https://console.firebase.google.com/) → **Project
   settings → Service accounts → Generate new private key** → download the JSON.
2. Put it on a **single line** in `.env`:

   ```bash
   FCM_SERVICE_ACCOUNT_JSON={"type":"service_account","project_id":"...", ...}
   ```

   Minify it with: `jq -c . service-account.json`.

### 5c. Web Push (VAPID) — browser

Generate a VAPID keypair:

```bash
npx web-push generate-vapid-keys
```

Then:

```bash
VAPID_PUBLIC_KEY=BB...
VAPID_PRIVATE_KEY=...
```

Both halves are required for Web Push to activate. When they are set,
`GET /v1/instance` advertises the public half as `vapidPublicKey`; the web app
registers `public/sw.js`, subscribes with that key, and registers the browser as
a push device — after which the worker delivers a push on newly-synced
important/VIP messages.

---

## Related pages

- [Configuration & Environment Reference](./configuration.md) — every variable in one table
- [Reverse Proxy & HTTPS](./reverse-proxy-tls.md) — get `YOUR_DOMAIN` serving over TLS first
- [Pointing the Apps at Your Server](./clients.md) — how clients consume `GET /v1/instance`
- [Security Hardening](./security.md) — keeping `BETTER_AUTH_SECRET` and provider secrets safe
- [Troubleshooting](./troubleshooting.md) — OAuth redirect and JWT issuer errors
- [Architecture](../architecture.md) · [Payments](../payments.md)
