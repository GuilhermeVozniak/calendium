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

---

## 1. Secrets management

**Never commit `.env`.** It holds your `BETTER_AUTH_SECRET`, provider client
secrets, Stripe keys (cloud only), and the token-encryption key. The repo's
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
| `GOOGLE_CLIENT_SECRET`, `APPLE_CLIENT_SECRET`, `MS_CLIENT_SECRET`, `STRIPE_*` | **SECRET** | Backend / server-only. |

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
- **`api` and `web` publish `${API_PORT:-8080}` and `${WEB_PORT:-3000}`** to the
  host so a bring-your-own-proxy (nginx/Traefik on the host) can reach them.
  When you run the bundled **Caddy** profile you don't need those host ports
  open to the world — **firewall them** (allow only 80/443 inbound) or bind them
  to loopback by setting, e.g., `API_PORT=127.0.0.1:8080` and
  `WEB_PORT=127.0.0.1:3000` in `.env`.

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

The stack pins these images — rebuild/pull them on a schedule to pick up security
fixes:

| Image | Used by |
| --- | --- |
| `postgres:16-alpine` | `db` |
| `caddy:2-alpine` | `caddy` proxy |
| `golang:1.26-alpine` (build), `alpine:3.20` (runtime) | backend image |
| `oven/bun:1` (build), `node:22-alpine` (runtime) | web image |

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
`/v1/billing/portal`, `/v1/webhooks/stripe`) return `501` with a stable
`self_hosted` error, and all features are unlocked without Stripe. Leave the
`STRIPE_*` vars blank — there's no paywall to secure and no webhook secret to
protect on a self-hosted box.

---

## Related pages

- [Backups & Restore](./backups.md) — encrypting and off-boxing dumps
- [Reverse Proxy & HTTPS](./reverse-proxy-tls.md) — TLS and port exposure
- [Configuration & Environment Reference](./configuration.md) — every secret in one place
- [Provider & Integration Setup](./providers.md) — where each provider secret comes from
- [Troubleshooting](./troubleshooting.md) — diagnosing auth/redirect failures
