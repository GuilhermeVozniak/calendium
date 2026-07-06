# Provider & Integration Setup

Calendium talks to a handful of external services. This page walks through
setting up every one of them for a **self-hosted** instance, which values to put
in your `.env`, and — importantly — **what breaks if you skip each one**.

Only two things are truly required for a *usable* instance:

1. **Supabase** — the identity provider clients log in with (and whose JWTs the
   backend verifies). Without it, nobody can sign in.
2. **At least one mail/calendar provider** (Google *or* Microsoft) — otherwise
   there is no mailbox to sync.

Everything else (AI, push) is optional; the composition root simply leaves the
unconfigured adapter unwired and the app boots fine without it. The public
[`GET /v1/instance`](#how-clients-discover-what-you-configured) endpoint reports
exactly which of these you configured so the apps can hide the UI they can't use.

| Integration | Required? | Env vars | What breaks if unset |
| --- | --- | --- | --- |
| Supabase auth | **Yes** (for login) | `SUPABASE_URL`, `SUPABASE_ANON_KEY`, `SUPABASE_JWT_SECRET` **or** `SUPABASE_JWKS_URL`, `NEXT_PUBLIC_SUPABASE_URL`, `NEXT_PUBLIC_SUPABASE_ANON_KEY` | No one can authenticate; every `/v1/*` call returns 401 |
| Google OAuth app | One provider required | `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` | Can't connect Gmail / Google Calendar; `features.google=false` |
| Microsoft Entra app | One provider required | `MS_CLIENT_ID`, `MS_CLIENT_SECRET` | Can't connect Outlook / Microsoft 365; `features.microsoft=false` |
| OpenRouter (AI) | Optional | `OPENROUTER_API_KEY`, `OPENROUTER_MODEL` | `POST /v1/ai/compose` disabled; `features.ai=false` |
| APNs / FCM / Web Push | Optional | see [Push](#5-push-notifications-optional) | No push notifications; `features.push=false` |

> Provider apps are **per-deployment**. You register your *own* Google Cloud and
> Microsoft Entra OAuth apps — you cannot reuse Calendium Cloud's. This is the
> single most common self-host gotcha, so follow the redirect-URI steps exactly.

---

## 1. Supabase (authentication) — required

Calendium uses **Supabase Auth** as the identity provider on web, desktop, and
mobile. Clients hold a Supabase session and send the access token as
`Authorization: Bearer <jwt>`; the Go backend **verifies that JWT locally** with
stdlib crypto and never calls Supabase. So you need *a* Supabase project, but it
stays lightweight.

Two ways to get one:

- **Supabase Cloud free tier (recommended).** Fastest path; free for a personal
  instance. Create a project at <https://supabase.com/dashboard>.
- **Self-hosted Supabase / GoTrue (advanced).** Run your own Supabase stack next
  to Calendium. Follow Supabase's own guide at
  <https://supabase.com/docs/guides/self-hosting/docker>. The only thing
  Calendium needs from it is a JWT-issuing auth endpoint plus the signing
  secret/JWKS — everything below still applies, just swap the URLs for your
  self-hosted ones.

### 1a. Create the project & enable login providers

1. Create a project. Note its **Project URL** (e.g. `https://abcd1234.supabase.co`).
2. In **Authentication → Providers**, enable the sign-in methods you want your
   users to log in with — typically **Google** and **Apple** (and/or email).
   These are *login* providers, separate from the *mailbox* providers in §2/§3.
   - For Google/Apple login you register OAuth clients with Google/Apple and
     paste their client ID/secret into Supabase, then add Supabase's callback
     (`https://<ref>.supabase.co/auth/v1/callback`) to those OAuth apps. See
     Supabase's [social login docs](https://supabase.com/docs/guides/auth/social-login).
3. In **Authentication → URL Configuration**, add your Calendium web origin
   (e.g. `https://your-domain`) and the `calendium://` redirect to the allowed
   redirect URLs so desktop/mobile deep-links resolve.

### 1b. Collect the four values Calendium needs

From **Project Settings → API** (and **→ API → JWT Settings**):

| `.env` var | Where to find it | Secret? |
| --- | --- | --- |
| `SUPABASE_URL` | Project URL | public |
| `SUPABASE_ANON_KEY` | Project API keys → `anon` `public` | **public** (safe to serve to clients) |
| `SUPABASE_JWT_SECRET` | API → JWT Settings → JWT Secret (HS256 projects) | **SECRET** — never expose |
| `SUPABASE_JWKS_URL` | `https://<ref>.supabase.co/auth/v1/.well-known/jwks.json` (RS256/ES256 projects) | public |

Set **one** of `SUPABASE_JWT_SECRET` (legacy HS256 shared secret) **or**
`SUPABASE_JWKS_URL` (asymmetric RS256/ES256 keys) depending on your project's
signing algorithm. Newer Supabase projects use asymmetric keys → prefer the JWKS
URL.

`SUPABASE_URL` does double duty: when set, the backend **pins the expected token
issuer** to `<SUPABASE_URL>/auth/v1`. A mismatch here is the usual cause of
"valid token but still 401" — see
[Troubleshooting → JWT / issuer mismatch](./troubleshooting.md#jwt--issuer-mismatch).

### 1c. The `NEXT_PUBLIC_*` twins (web app)

The Next.js web app needs the same Supabase URL and anon key, but **baked into
the browser bundle at build time**:

```bash
NEXT_PUBLIC_SUPABASE_URL=https://abcd1234.supabase.co
NEXT_PUBLIC_SUPABASE_ANON_KEY=<same anon key>
```

Because these are compile-time (Next.js inlines them), changing them means
**rebuilding the web image** (`docker compose up -d --build web`), not just
restarting it. `docker-compose.yml` passes them as build args *and* runtime env
for you — you only set them once in `.env`.

### How clients discover what you configured

Desktop and mobile clients only know your **server URL**. They call the
unauthenticated:

```http
GET /v1/instance
```

which returns your instance's public config so the apps can self-configure their
Supabase client and hide features you didn't enable:

```json
{
  "name": "Calendium",
  "mode": "self_host",
  "version": "0.1.0",
  "supabaseUrl": "https://abcd1234.supabase.co",
  "supabaseAnonKey": "eyJ...",
  "features": { "billing": false, "google": true, "microsoft": false, "ai": true, "push": false }
}
```

This is why `SUPABASE_ANON_KEY` is set on the **backend** too (not just the web
app): it's the value handed to native clients here. See
[Pointing the Apps at Your Server](./clients.md) for the client flow.

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
5. Under **Authorized redirect URIs**, add your backend callback exactly:

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

### 2c. The redirect allowlist

After the OAuth dance the backend redirects the browser back to a client-supplied
`redirectUrl`, which is validated against a server-side allowlist to prevent
open-redirect abuse. Built-in defaults already cover `http://localhost`,
`https://localhost`, `http://127.0.0.1`, and `calendium://`. **Add your
production web origin:**

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
3. **Redirect URI:** platform **Web**, value:

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

Both halves are required for Web Push to activate.

---

## Related pages

- [Configuration & Environment Reference](./configuration.md) — every variable in one table
- [Reverse Proxy & HTTPS](./reverse-proxy-tls.md) — get `YOUR_DOMAIN` serving over TLS first
- [Pointing the Apps at Your Server](./clients.md) — how clients consume `GET /v1/instance`
- [Security Hardening](./security.md) — keeping `SUPABASE_JWT_SECRET` and provider secrets safe
- [Troubleshooting](./troubleshooting.md) — OAuth redirect and JWT issuer errors
- [Architecture](../architecture.md) · [Payments](../payments.md)
