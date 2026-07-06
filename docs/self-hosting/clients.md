# Pointing the apps at your server

Calendium ships three clients — **Web**, **Desktop** (Wails), and **Mobile**
(Expo) — all built on `@calendium/shared`'s typed `ApiClient` with a configurable
base URL. Cloud users tap one button; self-hosters point the app at their own
server.

See also: [Overview](./README.md) · [Quickstart](./quickstart.md) ·
[Configuration](./configuration.md).

---

## How discovery works

Every Calendium server exposes an **unauthenticated** `GET /v1/instance` endpoint
(see [the discovery contract](./configuration.md#the-v1instance-discovery-contract)).
When you enter a server URL, the app calls it and reads back:

- the **Better Auth base URL** (`authBaseUrl`, e.g. `https://mail.example.com/api/auth`)
  for that instance — the app builds its Better Auth client against it (it differs
  per deployment and can't be hardcoded) — plus `authProviders` listing the enabled
  sign-in methods, and
- the **feature flags** (`billing`, `google`, `microsoft`, `ai`, `push`) so the
  UI adapts — e.g. on a self-host server `features.billing` is `false` and the
  billing/subscribe UI is hidden.

The shared layer exposes this as `fetchInstance(baseUrl)` and
`ApiClient.getInstance()`; the desktop and mobile connect screens use it to
validate a URL and self-configure before you sign in.

---

## Web

The web app is **served by your own deployment**. Authentication runs
**same-origin** — the web app hosts Better Auth at `/api/auth` and reads
`BETTER_AUTH_SECRET` / `BETTER_AUTH_URL` at **runtime**, so there's no auth value
to bake into the bundle. The one build-time value is:

- `NEXT_PUBLIC_API_URL` — where the browser reaches the API. Behind the bundled
  Caddy proxy the web app and API share one origin, so you can **leave it
  blank** and the client issues same-origin `/v1/…` requests. Set it explicitly
  (e.g. `https://mail.example.com`) if the API is on a different origin.

Because `NEXT_PUBLIC_API_URL` is compile-time, **changing it means rebuilding the
`web` image** (`make self-host-up` rebuilds). Users just open your domain —
there's no "enter a server" step on web; the deployment *is* the server.

---

## Desktop (Wails)

The desktop app opens on a **Connect** screen before login. Two choices:

- **Calendium Cloud** — one button; uses the built-in preset
  `https://api.calendium.app`.
- **Use a custom server** — type your server URL (e.g.
  `https://mail.example.com`). The app runs discovery
  (`fetchInstance` → `GET /v1/instance`), shows the instance name + mode, stores
  the config, and lets you sign in via that instance's Better Auth.

The chosen server is persisted (server URL + its `authBaseUrl` + mode), the shared
`ApiClient`'s `baseUrl` is a getter that always targets the active server, and the
Better Auth client is rebuilt from the discovered `authBaseUrl` — so you can
**switch servers at runtime** from **Settings**.

For local development you can pre-seed a server and skip the Connect screen by
setting `VITE_API_URL` in the desktop frontend's env; the app discovers the Better
Auth base URL from `GET /v1/instance`.

OAuth callbacks use the `calendium://` deep-link scheme regardless of which
server you connect to.

### Build the desktop app from source (internal distribution)

Build a desktop binary your team can install and point at your server:

```bash
cd apps/desktop
# optional: pre-seed the server so the Connect screen is skipped
export VITE_API_URL=https://mail.example.com
wails build          # produces a native app in build/bin
```

Users can still switch to another server from Settings after install.

---

## Mobile (Expo)

The mobile app has the same **Connect** screen: a **Calendium Cloud** button
(preset `https://api.calendium.app`) and a **custom server** field. Entering a
URL runs `discoverServer()` → `fetchInstance()` → `GET /v1/instance`, points the
shared client at it (`configureApi(serverUrl)`), and rebuilds the Better Auth
client from the discovered `authBaseUrl`. The active server URL is shown in
**Settings**.

Defaults can be seeded for development via `EXPO_PUBLIC_API_URL`; deep-linking
uses `EXPO_PUBLIC_SCHEME` (and `EXPO_PUBLIC_AUTH_REDIRECT_MODE`) — these seed a
default so the client exists before discovery finishes, but are no longer required
once you connect. The Better Auth base URL comes from `GET /v1/instance`.

> Because purchases never go through the app (Spotify model), and a self-host
> server reports `features.billing: false`, the mobile app shows **no subscribe
> screen** when pointed at a self-hosted instance — everything is already
> unlocked.

### Build the mobile app from source (internal distribution)

Self-hosters distribute their own build (you can't reuse the Cloud app's OAuth
config). Use EAS or a local prebuild, seeding your server as the default:

```bash
cd apps/mobile
# seed your server as the default connection
export EXPO_PUBLIC_API_URL=https://mail.example.com

bun install
npx expo run:ios        # or: npx expo run:android
# or a distributable build via EAS:
# eas build --platform ios --profile internal
```

Users can override the server on the Connect screen; the seeded values are just
the default.

---

## Which server URL do I enter?

| Setup | Server URL |
| --- | --- |
| Behind the bundled Caddy proxy | `https://<your DOMAIN>` (e.g. `https://mail.example.com`) |
| Local, no proxy (`PROFILE=`) | `http://localhost:8080` (or `http://<host-ip>:8080`) |
| Calendium Cloud | tap the **Cloud** button (`https://api.calendium.app`) |

Whatever you enter must be reachable from the device and must serve
`GET /v1/instance`. If discovery fails, check that the API is up
(`curl <url>/healthz`) and that your reverse proxy forwards `/v1/*` to the API
(the bundled Caddyfile already does).

---

## Moving between Cloud and self-host

Both run the same API and data model. To migrate a client, open **Connect** (or
**Settings → switch server**) and enter the other server's URL — the app
re-discovers the Better Auth base URL and feature flags and signs you in against
that instance. (Account *data* migration is a server-side concern — export/import via
your Postgres dumps; see [backups in the overview](./README.md#backups).)

---

Back to: **[Overview →](./README.md)**
