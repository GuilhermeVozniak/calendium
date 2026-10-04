# Store and release readiness — operator runbook

Piece 5 of the production-readiness program. Everything the repository can do
is done: a `vX.Y.Z` tag builds version-stamped desktop installers and the
mobile config passes `expo-doctor` 18/18. What remains needs a human in a
console. Work top to bottom; each section names its owner. Credentials and
account setup shared with the web/API (Google, Apple sign-in, Paddle, SMTP)
are in [`go-live-external-checklist.md`](./go-live-external-checklist.md).

## 1. Hard prerequisites (block submission)

| Item | Owner | Status |
| --- | --- | --- |
| **Account deletion in-app** (Apple Guideline 5.1.1(v), Google Play User Data policy): Settings → Account → **Delete account** on iOS/Android (`apps/mobile/components/account-controls.tsx`), web and desktop, plus **Download my data**. Both stores reject a build without in-app deletion. | piece 3 | done — cite the path in App Review notes |
| Privacy policy URL: `https://<DOMAIN>/privacy` (exists; documents export and deletion) | operator (hosting, piece 6) | required |
| Support URL / email: `https://<DOMAIN>` + `NEXT_PUBLIC_SUPPORT_EMAIL` (default `support@calendium.app`) | operator | required |
| App Review sign-in: a Cloud account (email + password) for the reviewer, entered in App Store Connect → App Review Information and Play Console → App access. It must hold a **long-lived active subscription** (subscribe it to the annual plan on the web and keep it renewing), never a trial: the server grants a 14-day trial at signup (`docs/payments.md`), and a reviewer on a lapsed trial lands on the paywall, which has no purchase path in the app and gets the build rejected (2.1 / 3.1.1). Check the account is still entitled before every submission. | operator | required |
| **Sign in with Apple enabled on Cloud whenever Google sign-in is** (Guideline 4.8): the mobile sign-in screen shows "Continue with Google" whenever Cloud advertises `google`, and shows Apple only when `APPLE_CLIENT_ID` is set. Never submit while Cloud offers Google without Apple. Setup: §2 of [`go-live-external-checklist.md`](./go-live-external-checklist.md). | operator | required |
| Apple Developer Program membership, Team ID `CT22R575UG` | operator | have |
| Google Play developer account | operator | required |

The iOS/Android app shows **no price and no purchase link** (Guidelines
3.1.1/3.1.3, Spotify model): subscriptions are bought on the web only. Do not
add a price, a "Subscribe" button or a billing URL to the mobile app or its
store listing screenshots.

## 2. App Store Connect (iOS)

1. Create the app record: bundle id `app.calendium.mobile`, name **Calendium**,
   primary language, SKU `calendium-mobile`.
2. First submission is interactive from the release tag:
   `cd apps/mobile && eas submit --platform ios --profile production`.
   EAS asks for the App Store Connect app and writes `submit.production.ios.ascAppId`
   into `eas.json` — commit that change (it is an identifier, not a secret).
3. App Privacy ("nutrition labels") must match the privacy manifest in
   `app.json` (`ios.privacyManifests.NSPrivacyCollectedDataTypes`) and `/privacy`.
   Declare exactly these, each **Linked to the user: Yes**, **Used for
   tracking: No**, purpose **App Functionality**:

   | App Store Connect category | Data type | Why |
   | --- | --- | --- |
   | Contact Info | Email Address | account sign-in |
   | Contact Info | Name | account profile |
   | User Content | Emails or Text Messages | the mail the app syncs and sends |
   | User Content | Other User Content | calendar events, notes, tasks |
   | Identifiers | Device ID | push notification token |

   No data is sold, used for advertising or shared with data brokers.
   Tracking: none (`NSPrivacyTracking false`), so no ATT prompt. Data does
   reach third parties, but only as service providers processing it on our
   behalf, as `/privacy` lists them: Google and Microsoft (the mailboxes and
   calendars the user connects), Paddle (billing, on the web), OpenRouter (the
   AI features receive mail content), Todoist and HubSpot (only if connected),
   Open-Meteo (coordinates), Nominatim and OSRM (place queries and
   coordinates), Apple APNs and Google FCM (device token and notification
   preview) and our email delivery provider (account emails). None of that is
   "tracking" (Apple) or "sharing" (Play), so the answers above stand; do not
   copy "no third parties" into any questionnaire.
4. Export compliance: `ITSAppUsesNonExemptEncryption=false` is in `app.json`
   (only HTTPS); answer **No** to "does your app use encryption" beyond that.
5. Age rating: 4+. Category: Productivity.
6. App Review notes (App Review Information → Notes). Paste, filling in the
   account from §1:

   > Sign in with the demo account below (email and password). It has an
   > active subscription, so every feature is available.
   >
   > Calendium is a client for a cross-platform Calendium account that works
   > the same on the web, desktop, iOS and Android. Like a reader app, the iOS
   > app lets people use an account they already have: the subscription is
   > bought on our website, and the app offers no in-app purchase, shows no
   > prices, and contains no links, buttons or other calls to action for
   > buying elsewhere. New accounts get a 14-day free trial with full access,
   > during which the app is fully usable.
   >
   > Account deletion: Settings → Account → Delete account. Data export:
   > Settings → Account → Download my data.

## 3. Google Play

1. Create the app: package `app.calendium.mobile`, **Calendium**, Productivity.
2. Data safety form from the same table as §2.3, except that calendar data
   goes under Play's own **Calendar → Calendar events** type (notes and tasks
   stay in **Other user content**). It collects email address, name, emails,
   calendar events, other user content and device id; encrypted in transit;
   **not shared** (the providers in §2.3 are service providers acting for us);
   users can delete their account and data in-app (piece 3). Give
   `https://<DOMAIN>/privacy` as the deletion URL Google asks for.
3. Release track: EAS submits to the **internal** track as a **draft**
   (`eas.json` `submit.production.android`). Promote to production from the console.
4. Service account: Play Console → Setup → API access → create a service
   account with Release Manager role, download its JSON, upload it once with
   `eas credentials --platform android` (EAS-managed; never committed).
5. Permissions declared: only `POST_NOTIFICATIONS` (`app.json`); the storage,
   overlay and exact-alarm permissions are blocked so no declaration form is
   needed.

## 4. EAS

1. `eas init` once against the Calendium Expo account and note the project id.
   It is not committed (self-hosters build against their own project);
   `app.config.js` injects it from `EAS_PROJECT_ID`. The local `eas build` /
   `eas submit` evaluate `app.config.js` before they know the project, so they
   cannot read it from the EAS project environment: **export it in the shell
   that runs §6** (`export EAS_PROJECT_ID=<id>`). Also set it in the EAS
   project environment so the cloud build resolves the same id.
2. File secret `GOOGLE_SERVICES_JSON` (Firebase Android config, `apps/mobile/docs/push.md`).
3. `eas credentials --platform ios`: distribution certificate + provisioning
   profile + **APNs key** (EAS-managed). The server side needs the same key as
   `APNS_KEY_ID` / `APNS_TEAM_ID` / `APNS_KEY_P8`; the default `APNS_TOPIC` is
   `app.calendium.mobile` and matches `app.json`.
4. `cli.appVersionSource` is `remote`: EAS owns `buildNumber` / `versionCode`
   (`autoIncrement` on the production profile), so no commit per build.
   `expo.version` (the marketing version) comes from the tag via
   `bun run version:set` (§6).
5. White-label builds only: `EXPO_PUBLIC_CLOUD_API_URL` /
   `EXPO_PUBLIC_DEMO_SERVER_URL` (EAS environment) replace the Calendium Cloud
   preset and demo label; leave them unset for the Calendium store build.
6. The `development` profile (`developmentClient: true`) needs the dev client
   first: `cd apps/mobile && bunx expo install expo-dev-client` (it is not a
   dependency today, so a non-interactive `eas build --profile development`
   fails without it). The profiles' `channel` values do nothing until
   `expo-updates` is installed and configured; they only reserve the names for
   OTA updates.

## 5. Desktop

**macOS notarization** needs five GitHub secrets: `MACOS_CERT_P12` (base64 of
the Developer ID Application `.p12`), `MACOS_CERT_PASSWORD`, `APPLE_ID`,
`APPLE_TEAM_ID` (`CT22R575UG`), `APPLE_APP_PASSWORD` (app-specific password).
Since this piece, **a tag fails at `preflight` when `MACOS_CERT_P12` is
empty**; only a manual dispatch with `allow_unsigned=true` builds unsigned, and
it never publishes. Dry-run after setting the secrets: Actions → Release →
Run workflow (`allow_unsigned=false`, `version` empty) and confirm the DMG
staples.

**Windows** is unsigned (no Authenticode certificate yet): SmartScreen shows
"Windows protected your PC" on first run of `calendium_<v>_windows_amd64-setup.exe`;
users click *More info → Run anyway*. The installer registers `calendium://`
and installs to `Program Files\Calendium\Calendium`. The portable zip does not
register the scheme.

**Linux**: the tarball ships `Calendium`, `calendium.desktop`, `calendium.png`
and `README.txt`; the README's `install` / `xdg-mime` lines register the scheme.

**Update banner**: release builds check
`https://api.github.com/repos/GuilhermeVozniak/calendium/releases/latest` 10 s
after launch and daily; `CALENDIUM_UPDATE_URL=off` disables it. Drafts and
prereleases are ignored. `release.yml` creates the release in a single
`publish` job only after every platform's build succeeded, so `latest` never
points at a release missing a platform's download.

## 6. Release procedure (every version)

```bash
bun run version:set X.Y.Z          # stamps apps/mobile/app.json + apps/desktop/wails.json
git commit -am "release: vX.Y.Z"
git tag vX.Y.Z && git push origin main vX.Y.Z
# release.yml: version-check (seconds) + test -> preflight (signing gate)
#   -> build legs (upload artifacts) -> publish (one GitHub Release, all platforms)
export EAS_PROJECT_ID=<id>         # §4.1; the local eas CLI needs it in the shell
cd apps/mobile && eas build -p all --profile production --auto-submit   # from the tag checkout
```

The first tag after this piece is **`v1.0.0`** (mobile already shipped `1.0.0`
in its config and store versions cannot go backwards; `wails.json` is stamped
`1.0.0` too). `version-check` refuses a tag whose `X.Y.Z` differs from the
committed `app.json` / `wails.json`. Server images built with
`VERSION=X.Y.Z docker compose build` report that version from
`GET /v1/instance`.

**A rejected tag** (version drift, the signing gate or a failed build leg)
publishes nothing. Delete it locally and on the remote before fixing and
re-tagging the same version, or the new push is refused:

```bash
git push --delete origin vX.Y.Z && git tag -d vX.Y.Z
# fix (e.g. bun run version:set X.Y.Z && git commit -am "release: vX.Y.Z"), then tag and push again
```

## 7. Hygiene

- `apps/mobile/docs/apple/secret-gem.rb` reads the `.p8` next to itself
  (`File.expand_path("AuthKey_8MX6Q9WW35.p8", __dir__)`); the key file is
  gitignored (`.gitignore`: `*.p8`, `AuthKey_*.p8`) and must stay out of the
  repository. Check with `git ls-files apps/mobile/docs/apple` — it lists only
  `README.md` and `secret-gem.rb`.
- `eas.json` holds no credentials; Apple/Google credentials live in EAS
  (`eas credentials`). Rotate the Apple client secret (§2 of
  [`go-live-external-checklist.md`](./go-live-external-checklist.md)) every 170 days.
