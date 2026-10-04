# Deploy Calendium on a Linux VPS

This is the **blessed self-hosting path**: one small Linux server running the whole
Calendium stack (Postgres, API, worker, web, and an automatic-HTTPS reverse proxy)
with Docker Compose. It works the same on **Hetzner Cloud**, **DigitalOcean**,
**Linode/Akamai**, **Vultr**, **Scaleway**, or any other plain Ubuntu box.

Time to a working, HTTPS-secured instance: **~15 minutes** once DNS has propagated.

> New here? Read the [Self-Hosting Overview](./README.md) first, then come back.
> Every other deployment target ([AWS](./aws.md), [GCP](./gcp.md),
> [Azure](./azure.md), [home server](./local.md)) reuses the same `docker-compose.yml`
> you'll use below.

---

## What you'll be running

The repo's root [`docker-compose.yml`](../../docker-compose.yml) starts five services
on a single private bridge network named `calendium`:

| Service   | Image                       | Host port                  | Role |
| --------- | --------------------------- | -------------------------- | ---- |
| `db`      | `postgres:16-alpine`        | none (internal only)       | Postgres 16; data in the `db_data` named volume |
| `api`     | `calendium-backend:latest`  | `${API_PORT:-8080}` → 8080 | Go HTTP API; **applies embedded SQL migrations at boot** |
| `worker`  | `calendium-backend:latest`  | none                       | Same image, `command: ["worker"]`; sync / scheduled-send / push loops |
| `web`     | `calendium-web:latest`      | `${WEB_PORT:-3000}` → 3000 | Next.js 15 app (`node apps/web/server.js`) |
| `caddy`   | `caddy:2-alpine`            | 80, 443, 443/udp           | Reverse proxy + automatic HTTPS (compose profile `caddy`) |

Caddy serves a **single domain**: it routes `/v1/*` and `/healthz` to the API
(`api:8080`) and everything else to the web app (`web:3000`). So you only need
**one DNS record**, and the browser talks to the API on the same origin as the site.

> **Self-hosted = every feature unlocked, no Paddle.** With `SELF_HOSTED=true`
> the billing endpoints return `501 self_hosted`, `GET /v1/billing/subscription`
> reports a permanent active annual plan, and clients treat every user as fully
> entitled. See [Configuration](./configuration.md).

---

## Requirements

- A server with **2 vCPU / 4 GB RAM / 20 GB disk** minimum. 2 GB works to *run*
  the stack but the Next.js image build is memory-hungry and can OOM — on a 2 GB
  box either add swap (below) or build the images on a bigger machine and pull them.
  RAM scales with the number of connected mail/calendar accounts (the worker holds
  a Gmail `historyId` / Graph delta cursor per account).
- **Ubuntu 24.04 LTS** (these commands assume it; Debian 12 is nearly identical).
- **Docker Engine 29+** and the **Compose v2/v5 plugin** (installed below).
- A **domain name** you control, and the ability to add a DNS record.
- Inbound **ports 80 and 443** reachable from the internet (for Let's Encrypt).

---

## Step 1 — Create the server

Pick any provider; a 2 vCPU / 4 GB instance is comfortable:

- **Hetzner Cloud** — `CPX21` (or `CX22`), image *Ubuntu 24.04*.
- **DigitalOcean** — a *Basic* Droplet, 2 GB+ / 2 vCPU, Ubuntu 24.04.
- **Linode / Vultr / Scaleway** — the equivalent 2 GB+ shared instance.

Add your **SSH public key** during creation (do not use password login). If the
provider offers a **floating / reserved IP**, attach one so the address survives a
rebuild.

## Step 2 — Create a non-root user and harden SSH

SSH in as `root` the first time, then:

```bash
adduser deploy
usermod -aG sudo deploy
rsync --archive --chown=deploy:deploy ~/.ssh /home/deploy   # copy authorized_keys

# Disable root login and password auth
sudo sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin no/'              /etc/ssh/sshd_config
sudo sed -i 's/^#\?PasswordAuthentication.*/PasswordAuthentication no/' /etc/ssh/sshd_config
sudo systemctl restart ssh
```

Log back in as `deploy` from now on: `ssh deploy@<server-ip>`.

## Step 3 — Install Docker Engine + Compose plugin

Use Docker's official apt repository (preferred over `get.docker.com` for production):

```bash
sudo apt-get update
sudo apt-get install -y ca-certificates curl
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] \
  https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo $VERSION_CODENAME) stable" \
  | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null
sudo apt-get update
sudo apt-get install -y docker-ce docker-ce-cli containerd.io \
  docker-buildx-plugin docker-compose-plugin

sudo usermod -aG docker deploy    # run docker without sudo (log out/in to apply)
docker compose version            # verify — note "docker compose", not "docker-compose"
```

**(Optional) add swap** on a small box so the web build doesn't OOM:

```bash
sudo fallocate -l 2G /swapfile && sudo chmod 600 /swapfile
sudo mkswap /swapfile && sudo swapon /swapfile
echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab
```

## Step 4 — Firewall (UFW)

```bash
sudo apt-get install -y ufw
sudo ufw default deny incoming
sudo ufw default allow outgoing
sudo ufw allow OpenSSH          # do this FIRST or you lock yourself out
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw allow 443/udp          # HTTP/3
sudo ufw enable
sudo ufw status verbose
```

> ⚠️ **UFW does not filter Docker-published ports.** Docker inserts its own
> iptables NAT rules that skip UFW's `INPUT` chain, so a `ports:` mapping is
> reachable from the internet even with `ufw deny`. The bundled Postgres is safe
> because the compose file **never publishes it**. But the `api` (`8080`) and `web`
> (`3000`) services *are* published to the host by default. When you run behind
> the Caddy profile (recommended), bind those to loopback so only Caddy — which
> reaches them by service name on the internal network — can see them. Set this in
> your `.env` (Step 6):
>
> ```dotenv
> API_PORT=127.0.0.1:8080
> WEB_PORT=127.0.0.1:3000
> ```
>
> These interpolate into the compose `ports:` as `127.0.0.1:8080:8080` /
> `127.0.0.1:3000:3000`, so Docker binds them to localhost only and the internet
> can't reach them directly — Caddy on `:80/:443` is the sole public entrypoint.

## Step 5 — Point DNS at the server

Create a single **A record** (and an **AAAA** record if your VPS has IPv6):

```
Type   Name               Value
A      calendium.example.com   <your server IPv4>
AAAA   calendium.example.com   <your server IPv6, if any>
```

Wait for it to resolve (`dig +short calendium.example.com`) **before** you start the
stack — Caddy's ACME challenge needs the name pointing at the box and ports 80/443
reachable, or certificate issuance fails.

## Step 6 — Clone and configure

```bash
git clone https://github.com/<your-org>/calendium.git
cd calendium
cp .env.example .env
```

Generate the required encryption key and put it in `.env`:

```bash
make gen-secret        # prints a 32-byte hex key (or: openssl rand -hex 32)
```

Edit `.env`. At minimum, set these (full reference in
[Configuration & Environment Variables](./configuration.md)):

```dotenv
# ── Mode ─────────────────────────────────────────────
SELF_HOSTED=true
INSTANCE_NAME=Calendium

# ── Reverse proxy / public origin ────────────────────
DOMAIN=calendium.example.com
ACME_EMAIL=you@example.com
APP_URL=https://calendium.example.com
PUBLIC_WEB_URL=https://calendium.example.com

# ── Postgres (bundled db service) ────────────────────
POSTGRES_USER=calendium
POSTGRES_PASSWORD=<openssl rand -base64 24>
POSTGRES_DB=calendium
DATABASE_URL=postgres://calendium:<same-password>@db:5432/calendium?sslmode=disable

# ── Secrets at rest (REQUIRED) ───────────────────────
TOKEN_ENCRYPTION_KEY=<paste the make gen-secret output — exactly 64 hex chars>

# ── Authentication (Better Auth — built into the web app; see ./providers.md) ─
BETTER_AUTH_SECRET=<openssl rand -base64 32>   # signing secret — keep it stable
BETTER_AUTH_URL=https://calendium.example.com  # public web origin, no trailing slash
# Optional social login (the Google app also connects Gmail/Calendar below):
# GOOGLE_CLIENT_ID=...   GOOGLE_CLIENT_SECRET=...
# APPLE_CLIENT_ID=...    APPLE_CLIENT_SECRET=...

# ── Where the browser reaches the API (same origin as the site) ──
NEXT_PUBLIC_API_URL=https://calendium.example.com

# ── Provider OAuth apps (see ./providers.md) ─────────
OAUTH_ALLOWED_REDIRECT_URIS=https://calendium.example.com
GOOGLE_CLIENT_ID=...
GOOGLE_CLIENT_SECRET=...
MS_CLIENT_ID=...
MS_CLIENT_SECRET=...

# ── Keep the API/web off the public internet (see Step 4 note) ──
API_PORT=127.0.0.1:8080
WEB_PORT=127.0.0.1:3000
```

Notes that trip people up:

- `sslmode=disable` is **correct here** — the API talks to Postgres over the
  private Docker network, never the internet. (Only change it for a *managed*
  database; see [Database](./configuration.md#core--database).)
- `TOKEN_ENCRYPTION_KEY` must be **exactly 64 hex characters** (32 bytes) or the
  API refuses to boot. **Back it up separately** — if you lose it, every stored
  provider refresh token becomes undecryptable and all users must reconnect.
- `NEXT_PUBLIC_API_URL` is **baked into the browser bundle at build time** (passed
  as a Docker build arg by compose); change it later and you must rebuild the web
  image (`docker compose build web`). Better Auth's own secrets are read at
  **runtime**, not baked.
- Self-hosters register their **own** Google Cloud / Microsoft Entra OAuth apps —
  you can't reuse Calendium Cloud's. See [Connecting Provider Accounts](./providers.md).

## Step 7 — Launch

```bash
make self-host-up
```

That wraps `docker compose --profile caddy up -d --build` — it builds the backend
and web images from the repo root and starts `db`, `api`, `worker`, `web`, and
`caddy`. (Prefer raw compose? Run `docker compose --profile caddy up -d --build`.)

Watch the boot — the API applies migrations, then Caddy provisions the certificate:

```bash
make self-host-logs                       # tail everything
docker compose logs -f caddy api          # or just cert issuance + migrations
```

## Step 8 — Verify

```bash
curl https://calendium.example.com/healthz          # -> 200 OK
curl https://calendium.example.com/v1/instance      # -> instance discovery JSON
```

`GET /v1/instance` is unauthenticated and should return something like:

```json
{
  "name": "Calendium",
  "mode": "self_host",
  "version": "0.1.0",
  "authBaseUrl": "https://calendium.example.com/api/auth",
  "authProviders": ["email", "google"],
  "undoSendSeconds": 15,
  "features": { "billing": false, "google": true, "microsoft": false, "ai": false, "push": false }
}
```

`"mode": "self_host"` and `"features.billing": false` confirm self-host mode is
active. Open `https://calendium.example.com` in a browser — you should see the
Calendium web app with a valid certificate.

---

## Point your apps at your server

Your instance is now the source of truth. In the desktop and mobile apps, choose
**"Use a custom server"** and enter `https://calendium.example.com`; the client
hits `GET /v1/instance` to self-configure (Better Auth base URL, which features to
show). The web app is already wired via `NEXT_PUBLIC_API_URL`. Full walkthrough:
[Pointing the Apps at Your Server](./clients.md).

## Updating

```bash
cd calendium
git pull
make self-host-up          # rebuilds images and restarts; api re-applies migrations
docker compose logs -f api # confirm migrations applied and /healthz is green
```

Migrations are **forward-only** and run at API boot. Snapshot the database before
upgrading, and see [Upgrading](./upgrades.md) for pinning versions and rollback.

## Backups

```bash
make db-backup                                   # -> backups/calendium-<timestamp>.sql.gz
make db-restore FILE=backups/calendium-YYYYMMDD-HHMMSS.sql.gz
```

Ship those dumps off-box (a backup on the same disk isn't a backup) and store
`TOKEN_ENCRYPTION_KEY` somewhere permanent. See [Backups & Restore](./backups.md).

## Run it as a system service (optional)

To start/stop the whole stack cleanly on boot, install the shipped unit
[`deploy/systemd/calendium-compose.service`](../../deploy/systemd/calendium-compose.service):

```bash
sudo cp deploy/systemd/calendium-compose.service /etc/systemd/system/
# place the repo (with a filled-in .env) at /opt/calendium, or edit WorkingDirectory
sudo systemctl daemon-reload
sudo systemctl enable --now calendium-compose.service
```

---

## Troubleshooting

| Symptom | Cause & fix |
| --- | --- |
| No certificate / Caddy retries forever | DNS not pointing at the box yet, or ports 80/443 blocked. Confirm `dig +short DOMAIN` returns your IP and both ports are open, then `docker compose restart caddy`. |
| `curl` to `:8080`/`:3000` from another host works | You didn't set `API_PORT`/`WEB_PORT` to `127.0.0.1:*`. Docker bypasses UFW — bind to loopback (Step 4) and `docker compose up -d`. |
| API won't start, complains about the key | `TOKEN_ENCRYPTION_KEY` isn't exactly 64 hex chars. Regenerate with `make gen-secret`. |
| Web shows the wrong API URL | `NEXT_PUBLIC_API_URL` is baked at build time — fix `.env` and `docker compose build web && docker compose up -d web`. |
| Provider connect fails / "redirect not allowed" | Add your web origin to `OAUTH_ALLOWED_REDIRECT_URIS` **and** register the same redirect URIs in Google/Microsoft consoles. See [Providers](./providers.md). |

More in [Troubleshooting](./troubleshooting.md).

---

## Where to next

- [Configuration & Environment Variable Reference](./configuration.md)
- [Authentication Setup for Your Instance](./providers.md#1-authentication-better-auth--built-in)
- [Connecting Provider Accounts](./providers.md)
- [Reverse Proxy & HTTPS](./reverse-proxy-tls.md) (nginx / Traefik alternates)
- [Backups & Restore](./backups.md) · [Upgrading](./upgrades.md) · [Security Hardening](./security.md)
- Cloud platforms: [AWS](./aws.md) · [GCP](./gcp.md) · [Azure](./azure.md) · [Home server / bare metal](./local.md)
