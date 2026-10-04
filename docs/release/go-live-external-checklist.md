# Go-live: things only the operator can do

This is piece 6 of the production-readiness program. Everything here happens
in a third-party console, needs a human identity, or has a lead time measured
in weeks. Start the long ones today. Code changes they depend on are noted
with the piece that ships them.

## 1. Google (longest lead time — start first)

Calendium requests `https://www.googleapis.com/auth/gmail.modify` (restricted)
and `https://www.googleapis.com/auth/calendar` (sensitive) for mailbox
connect, plus `openid email profile` for login. One OAuth client serves both.

- [ ] Google Cloud project; enable **Gmail API** and **Google Calendar API**.
- [ ] Verify the production domain in Search Console with the same account.
- [ ] OAuth consent screen: **External**, app name, support email, homepage
      `https://<DOMAIN>`, privacy `https://<DOMAIN>/privacy`, terms
      `https://<DOMAIN>/terms`, authorized domain `<DOMAIN>`. Scopes as above.
- [ ] OAuth client, type **Web application**, redirect URIs exactly:
      - `https://<DOMAIN>/api/auth/callback/google` (login)
      - `https://<DOMAIN>/v1/accounts/callback/google` (mailbox connect)
      Add `http://localhost:3000/api/auth/callback/google` and
      `http://localhost:8080/v1/accounts/callback/google` only on a separate
      dev client.
- [ ] Set `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `PUBLIC_API_URL`,
      `OAUTH_ALLOWED_REDIRECT_URIS` in the production `.env`.
- [ ] Publish the consent screen to **Production** and submit for
      **verification**: scope justifications, a demo video of the consent
      flow, and a privacy policy with the Google API Services User Data
      Policy **Limited Use** disclosure (piece 3 updates the policy).
- [ ] After verification, complete the **CASA security assessment**
      (annual) required for the restricted Gmail scope.
- Until verified: 100-user cap, "unverified app" warning, and in *Testing*
  mode refresh tokens expire after 7 days (accounts flip to
  `reauth_required`).

## 2. Sign in with Apple

Existing material in `apps/mobile/docs/apple/`: Team ID `CT22R575UG`,
Services ID `com.calendium.app.service`, Key ID `8MX6Q9WW35`, the `.p8`
(gitignored), and `secret-gem.rb` which mints the client secret. Its
`key_file` path is stale; point it at `apps/mobile/docs/apple/AuthKey_8MX6Q9WW35.p8`.

- [ ] On the Services ID, Sign in with Apple → Configure: domain `<DOMAIN>`
      (no scheme), return URL `https://<DOMAIN>/api/auth/callback/apple`.
      Apple rejects localhost; Apple login can only be exercised on the real
      domain.
- [ ] Run `ruby apps/mobile/docs/apple/secret-gem.rb` to mint a client
      secret (max 180 days). Set `APPLE_CLIENT_ID=com.calendium.app.service`
      and `APPLE_CLIENT_SECRET=<jwt>` on web and api.
- [ ] Calendar a rotation reminder 170 days out; the secret expires.
- Code dependency: piece 2 adds `https://appleid.apple.com` to the trusted
  origins so Apple's form-post callback is accepted.

## 3. Paddle (billing — piece 1)

- [ ] **Website approval**: Paddle > My account > Settings > Website approval
      for `<DOMAIN>`. Live checkout refuses unapproved domains.
- [ ] **Default payment link** (Paddle > Checkout > Checkout configuration):
      sandbox `http://localhost:3000/checkout`, live `https://<DOMAIN>/checkout`.
- [ ] Catalog in **live**: product "Calendium Annual", recurring yearly price
      USD 50. Copy the live `pri_...` into `PADDLE_PRICE_ID_ANNUAL`.
- [ ] **Notification destination** in sandbox and live:
      `https://<API ORIGIN>/v1/webhooks/paddle`, events
      `subscription.created, subscription.activated, subscription.updated,
      subscription.canceled, subscription.past_due, subscription.paused,
      subscription.resumed, subscription.trialing`. Copy the endpoint secret
      into `PADDLE_WEBHOOK_SECRET`.
- [ ] API key (server) → `PADDLE_API_KEY`; client-side token →
      `NEXT_PUBLIC_PADDLE_CLIENT_TOKEN`; `PADDLE_ENV=live`.
- [ ] Dashboard choices: tax-inclusive or exclusive pricing; payment
      recovery end action (cancel vs pause) — the app treats both as
      no-access.
- [ ] Sandbox verification before go-live: the plan's last task, needs the
      sandbox values in the local `.env`.

## 4. Microsoft (Outlook / Microsoft 365)

- [ ] Entra app registration, accounts in any org directory + personal.
- [ ] Web redirect URI `https://<DOMAIN>/v1/accounts/callback/microsoft`.
- [ ] Delegated Graph permissions: `Mail.ReadWrite`, `Mail.Send`,
      `Calendars.ReadWrite`, `offline_access`, `openid`, `email`.
- [ ] Client secret (max 24 months; calendar the rotation) →
      `MS_CLIENT_ID`, `MS_CLIENT_SECRET`.
- [ ] **Publisher verification** (Partner Center ID + verified domain);
      without it, users in other tenants cannot self-consent to mail scopes.

## 5. Transactional email (piece 2)

- [ ] An SMTP sender (any provider): set `SMTP_HOST`, `SMTP_PORT`,
      `SMTP_USER`, `SMTP_PASS`, `SMTP_FROM`, `SMTP_SECURE`.
- [ ] SPF, DKIM and DMARC records for the sending domain.
- [ ] From the production domain, sign up with a real inbox: the verification
      mail arrives (not in spam) and its link opens `https://<DOMAIN>/verify-email`.
- [ ] `TRUST_PROXY` is `true` for `web` (the Compose default; nothing in
      `.env` overrides it to `false`), and every proxy hop in front of it is
      inside `TRUSTED_PROXY_CIDRS` (the default covers loopback and private
      ranges, so the bundled Caddy is covered). Behind the bundled Caddy each
      client then gets its own bucket. With `TRUST_PROXY=false` only the
      immediate peer, the proxy, is used, so every client shares one bucket on
      all auth endpoints, including JWT minting.
- [ ] `ALLOW_DEV_ORIGINS` blank in the production `.env`.
- Code dependency: piece 2 makes `api`, `worker` and `web` refuse to start
  with `SELF_HOSTED=false` and no `SMTP_HOST`/`SMTP_FROM`.

## 6. Apple notarization for the desktop app (release workflow)

- [ ] GitHub secrets `MACOS_CERT_P12`, `MACOS_CERT_PASSWORD`, `APPLE_ID`,
      `APPLE_TEAM_ID`, `APPLE_APP_PASSWORD`. The last Release run got a 401
      from notarytool; `APPLE_ID` was re-set afterwards but never exercised.
      Re-run the Release workflow (dispatch) and confirm the DMG staples.

## 7. Push (optional at launch)

- [ ] APNs key (`APNS_KEY_ID`, `APNS_TEAM_ID`, `APNS_KEY_P8`,
      `APNS_TOPIC=app.calendium.mobile`), FCM service account
      (`FCM_SERVICE_ACCOUNT_JSON`), VAPID keys for web push.

## 8. Hosting

- [ ] DNS for `<DOMAIN>` to the host; Caddy profile for automatic TLS, or
      your own proxy with `X-Forwarded-Proto` set (see
      `docs/self-hosting/reverse-proxy-tls.md`).
- [ ] Production `.env` with fresh `TOKEN_ENCRYPTION_KEY`,
      `BETTER_AUTH_SECRET`, `INTERNAL_API_SECRET` (piece 3), a real
      `POSTGRES_PASSWORD`, `SELF_HOSTED=false`, `TRUST_PROXY=true` (piece 4).
- [ ] Backups scheduled per `docs/self-hosting/backups.md`.
