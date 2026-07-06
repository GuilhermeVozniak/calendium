# Push notifications — mobile client setup

The Calendium backend delivers push **directly**: APNs for iOS, FCM v1 for
Android (`backend/internal/adapter/out/push`). There is no Expo push service in
the path. The mobile app therefore registers the **native device token** from
`Notifications.getDevicePushTokenAsync()` at `POST /v1/devices`
(`hooks/use-push-registration.ts`) — an Expo push token would be undeliverable.

Push is gated on `features.push` from `GET /v1/instance`; the server sets that
`true` once APNs, FCM, or Web Push is configured (see
`docs/self-hosting/providers.md` §5). If the flag is `false`, the app never even
asks for permission.

> Remote push only works in a real build (dev client or standalone) on a
> physical device — never in Expo Go or a simulator. The hook no-ops in those
> environments.

## Android (FCM) — requires operator Firebase config

An FCM registration token can only be minted when the app is built with your
Firebase Android config. That file is specific to **your** Firebase project, so
it is intentionally **not committed** (it is git-ignored) and must be supplied
per deployment:

1. In the [Firebase console](https://console.firebase.google.com/), create (or
   reuse) a project and **add an Android app** with package name
   **`app.calendium.mobile`** (must match `android.package` in `app.json`).
2. Download the generated **`google-services.json`**.
3. Save it as **`apps/mobile/google-services.json`** — `app.config.js` detects it
   and wires `android.googleServicesFile` automatically. (Alternatively, point
   the `GOOGLE_SERVICES_JSON` env var at another path, e.g. an EAS secret file.)
4. Rebuild the app (`eas build` or a local `expo prebuild` + `run:android`).
   `google-services.json.example` shows the expected shape.
5. On the **server**, set `FCM_SERVICE_ACCOUNT_JSON` (providers.md §5b) so the
   backend can send to FCM. The Firebase *project* must be the same on both ends.

Without `google-services.json`, the Android build still succeeds and push
degrades honestly: `getDevicePushTokenAsync()` throws → `notifyPushUnavailable()`
warns (and shows an alert in dev) → **nothing is registered** (no fake success).

## iOS (APNs)

`getDevicePushTokenAsync()` returns the native APNs token once the build has the
Push Notifications capability. With EAS, `eas credentials` provisions the APNs
key and entitlement for you; no `google-services.json` is needed on iOS. On the
server, set `APNS_KEY_ID` / `APNS_TEAM_ID` / `APNS_KEY_P8` and `APNS_TOPIC =
app.calendium.mobile` (providers.md §5a).

## EAS project id

`eas build` links a build to an Expo project. Self-hosters build against their
own project, so the id is not committed either — set `EAS_PROJECT_ID` in the
build environment and `app.config.js` injects it as `extra.eas.projectId`.

## Why push can't "just work" from the committed config

Both `google-services.json` (a Firebase project) and the EAS/Apple credentials
are per-operator resources that cannot live in an open-source repository. The
committed config is therefore wired for **graceful degradation**: it builds and
runs everywhere, advertises push only when the server reports it, and becomes a
fully working FCM/APNs client the moment an operator drops in their own config
and rebuilds — no code changes required.
