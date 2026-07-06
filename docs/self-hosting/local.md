# Run Calendium on a home server or bare metal

This covers running Calendium **without a cloud VPS**: a NAS, a mini-PC, a spare
desktop, or a Raspberry Pi on your LAN — with or without a public domain. It uses
the same [`docker-compose.yml`](../../docker-compose.yml) as every other target;
only *how you expose it* changes.

Pick the scenario that fits:

1. [LAN only — no public domain](#1-lan-only-no-public-domain)
2. [Behind an existing reverse proxy (nginx / Traefik / NPM / pfSense)](#2-behind-an-existing-reverse-proxy)
3. [Remote access without opening ports (Cloudflare Tunnel / Tailscale)](#3-remote-access-without-opening-ports)
4. [Raspberry Pi / arm64 notes](#4-raspberry-pi--arm64-notes)

First do the common setup: install Docker (see [VPS Step 3](./vps.md#step-3--install-docker-engine--compose-plugin)),
`git clone` the repo, `cp .env.example .env`, and fill in the required values
(`SELF_HOSTED=true`, a `make gen-secret` `TOKEN_ENCRYPTION_KEY`, Supabase, provider
OAuth). Reference: [Configuration](./configuration.md). You still need **a Supabase
project** for identity — a free Supabase Cloud project is the simplest path. See
[Authentication Setup](./providers.md#1-supabase-authentication--required).

---

## 1. LAN only, no public domain

The published host ports let you reach Calendium directly over your LAN, no proxy
or certificate required. Start the stack **without** the Caddy profile:

```bash
make self-host-up PROFILE=            # PROFILE= disables the caddy proxy
# equivalently: docker compose up -d --build
```

This publishes `web` on port `3000` and `api` on port `8080` on the host. Say the
machine's LAN IP is `192.168.1.50` — set these in `.env` **before** starting (the
`NEXT_PUBLIC_*` values are baked into the web bundle at build time):

```dotenv
NEXT_PUBLIC_API_URL=http://192.168.1.50:8080
APP_URL=http://192.168.1.50:3000
PUBLIC_WEB_URL=http://192.168.1.50:3000
OAUTH_ALLOWED_REDIRECT_URIS=http://192.168.1.50:3000
```

Then open `http://192.168.1.50:3000`. Verify the API with
`curl http://192.168.1.50:8080/healthz`. (Change `WEB_PORT`/`API_PORT` in `.env` if
`3000`/`8080` clash with something else.)

**Want HTTPS on the LAN?** Keep the Caddy profile and let Caddy mint a
locally-trusted certificate. Either set `DOMAIN=localhost` (works on the box
itself), or, for a LAN hostname, edit
[`deploy/caddy/Caddyfile`](../../deploy/caddy/Caddyfile) to force an internal CA:

```caddyfile
{$DOMAIN} {
	tls internal
	encode zstd gzip
	@api path /v1/* /healthz
	handle @api { reverse_proxy api:8080 }
	handle { reverse_proxy web:3000 }
}
```

Set `DOMAIN=calendium.lan`, add `calendium.lan` to each client's `/etc/hosts` (or
your router's DNS), start with `make self-host-up`, and install Caddy's root CA
(`docker compose exec caddy cat /data/caddy/pki/authorities/local/root.crt`) on the
client machines to avoid browser warnings.

---

## 2. Behind an existing reverse proxy

If you already run **nginx**, **Traefik**, **Nginx Proxy Manager**, or **pfSense**,
don't run Calendium's Caddy. Bind the app ports to **loopback** so only your proxy
(on the same host) can reach them, then point the proxy at them. In `.env`:

```dotenv
API_PORT=127.0.0.1:8080
WEB_PORT=127.0.0.1:3000
```

```bash
make self-host-up PROFILE=            # start without Caddy
```

A ready-to-use **nginx + certbot** vhost ships at
[`deploy/nginx/calendium.conf`](../../deploy/nginx/calendium.conf) — it proxies
`/v1/` and `/healthz` to `127.0.0.1:8080` and everything else to `127.0.0.1:3000`,
with the Let's Encrypt (`certbot --webroot`) flow documented in its header:

```bash
sudo cp deploy/nginx/calendium.conf /etc/nginx/sites-available/calendium.conf
sudo ln -s /etc/nginx/sites-available/calendium.conf /etc/nginx/sites-enabled/
# edit server_name, then:
sudo mkdir -p /var/www/certbot && sudo nginx -t && sudo systemctl reload nginx
sudo certbot certonly --webroot -w /var/www/certbot -d calendium.example.com
# enable the HTTPS server block + the :80→:443 redirect in the file, reload nginx
```

> ⚠️ **Set `X-Forwarded-Proto` (and `X-Real-IP`, `X-Forwarded-For`) on your proxy.**
> If you don't, you'll get infinite HTTPS redirect loops and OAuth callbacks that
> build `http://` URLs — the single most common self-host support ticket. The
> shipped nginx sample sets them for you; if you write your own config or use
> Traefik, replicate that.

Set `NEXT_PUBLIC_API_URL=https://calendium.example.com` (your proxy routes `/v1/*`
to the API on the same origin) and rebuild the web image if you change it:
`docker compose build web && docker compose up -d web`.

---

## 3. Remote access without opening ports

Reach your home instance from anywhere without port-forwarding on your router.

### Cloudflare Tunnel

Cloudflare terminates TLS at its edge, so **don't run Caddy**. Add `cloudflared` as
a compose service on the `calendium` network (create a
`docker-compose.override.yml`) so it reaches `web` and `api` by service name:

```yaml
# docker-compose.override.yml
services:
  cloudflared:
    image: cloudflare/cloudflared:latest
    command: tunnel --no-autoupdate run
    environment:
      TUNNEL_TOKEN: ${TUNNEL_TOKEN}
    restart: unless-stopped
    networks: [calendium]
```

Create the tunnel and a public hostname in the Cloudflare dashboard (or CLI). Route
a single hostname, splitting `/v1` + `/healthz` to the API and the rest to the web
app — matching Calendium's single-domain model:

```yaml
# tunnel ingress (Cloudflare dashboard → Public Hostname, or config.yml)
ingress:
  - hostname: calendium.example.com
    path: ^/(v1|healthz)
    service: http://api:8080
  - hostname: calendium.example.com
    service: http://web:3000
  - service: http_status:404        # required catch-all
```

Set `NEXT_PUBLIC_API_URL=https://calendium.example.com` and
`OAUTH_ALLOWED_REDIRECT_URIS=https://calendium.example.com`, start with
`make self-host-up PROFILE=` (no Caddy), and no inbound ports are opened at all.

### Tailscale

Best for private (tailnet-only) access. Bind the app ports to loopback as in §2
(`API_PORT=127.0.0.1:8080`, `WEB_PORT=127.0.0.1:3000`), then expose them from the
host's Tailscale node with path routing:

```bash
tailscale serve --bg --set-path /        http://127.0.0.1:3000   # web
tailscale serve --bg --set-path /v1      http://127.0.0.1:8080   # api
tailscale serve --bg --set-path /healthz http://127.0.0.1:8080
```

Your instance is now at `https://<node>.<tailnet>.ts.net` (MagicDNS + automatic
cert) for any device on your tailnet. Swap `serve` for `funnel` to expose it on the
public internet. Set `NEXT_PUBLIC_API_URL` to that hostname and rebuild `web`.

---

## 4. Raspberry Pi / arm64 notes

- **All images are multi-arch.** `postgres:16-alpine`, `golang:1.26-alpine`,
  `node:22-alpine`, and `caddy:2-alpine` all have official arm64 variants, so the
  stack builds and runs natively on a Pi 4/5, Ampere, or Graviton.
- **Use a 64-bit OS** (Raspberry Pi OS Lite arm64 / Ubuntu Server arm64).
- **Watch RAM during the web build.** The Next.js build is memory-hungry and can
  OOM on a 2–4 GB Pi. Either add swap/zram, or build the images on a beefier
  machine and move them over:

  ```bash
  # on a bigger arm64 (or cross-building) machine:
  docker buildx build --platform linux/arm64 -f backend/Dockerfile -t calendium-backend:latest .
  docker buildx build --platform linux/arm64 -f apps/web/Dockerfile \
    --build-arg NEXT_PUBLIC_API_URL=https://calendium.example.com \
    --build-arg NEXT_PUBLIC_SUPABASE_URL=https://<ref>.supabase.co \
    --build-arg NEXT_PUBLIC_SUPABASE_ANON_KEY=<anon key> \
    -t calendium-web:latest .
  # then `docker save | ssh pi docker load`, or push to a registry and pull on the Pi
  ```

Everything else (UFW, the Docker apt repo, Caddy) is identical to the
[VPS guide](./vps.md).

---

## Run it on boot

Install the shipped systemd unit
[`deploy/systemd/calendium-compose.service`](../../deploy/systemd/calendium-compose.service)
so the stack comes up after a reboot/power-cut. Drop `--profile caddy` from its
`ExecStart`/`ExecStop`/`ExecReload` lines if you terminate TLS with your own proxy
(scenario §2/§3):

```bash
sudo cp deploy/systemd/calendium-compose.service /etc/systemd/system/
# place the repo at /opt/calendium (or edit WorkingDirectory)
sudo systemctl daemon-reload
sudo systemctl enable --now calendium-compose.service
```

---

## See also

- [Configuration & Environment Variables](./configuration.md) · [Authentication Setup](./providers.md#1-supabase-authentication--required)
- [Reverse Proxy & HTTPS](./reverse-proxy-tls.md) · [Connecting Provider Accounts](./providers.md)
- [Backups & Restore](./backups.md) · [Pointing the Apps at Your Server](./clients.md) · [Troubleshooting](./troubleshooting.md)
- Other targets: [VPS](./vps.md) · [AWS](./aws.md) · [GCP](./gcp.md) · [Azure](./azure.md)
