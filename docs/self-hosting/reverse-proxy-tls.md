# Reverse Proxy & HTTPS

Every Calendium client needs to reach your server over **HTTPS**. A reverse
proxy sits in front of the two internal services and terminates TLS:

```
                     ╭─ https://your-domain ─╮
   browser / apps ────▶│      reverse proxy      │
                     ╰────────┬──────────╰
                    /v1/*, /healthz │ everything else
                        ▼          ▼
                    api:8080     web:3000
```

**Routing is the same everywhere:** `/v1/*` and `/healthz` go to the Go **API**
(`api:8080`); **everything else** goes to the Next.js **web** app (`web:3000`).
There is a single public domain — the API lives under the `/v1` path of that same
origin, which is why `NEXT_PUBLIC_API_URL` for a proxied deployment is just
`https://your-domain` (no port, no separate host).

We recommend **Caddy** — it's bundled, it's three lines, and it gets you valid
auto-renewing HTTPS with zero manual certificate work. Use **nginx** if you
already run it or need fine-grained tuning; use **Traefik** if you run a dynamic
Docker fleet.

### Proxy trust (`TRUST_PROXY`)

Behind any proxy the API and the web app must know the *real* client address:
the API for its per-IP rate limits, request logs and the OAuth callback origin,
the web app for its sign-in rate limits. The compose file sets
`TRUST_PROXY=${TRUST_PROXY:-true}` on both `api` and `web`, which makes them
honour `X-Forwarded-For` (and, on the API, `X-Forwarded-Proto` and
`X-Forwarded-Host`) — but only from a peer inside `TRUSTED_PROXY_CIDRS`
(default: loopback, RFC 1918, `fc00::/7`). The client is the right-most
`X-Forwarded-For` hop that is *not* a trusted proxy. Caddy replaces
client-supplied `X-Forwarded-*`; nginx must either append with
`$proxy_add_x_forwarded_for` or overwrite with `$remote_addr` (the sample does
the former for the API, the latter for the web app). Appending lets a client
whose own address is inside `TRUSTED_PROXY_CIDRS` (a LAN/VPN client of the
proxy host) choose the API's rate-limit key by sending its own
`X-Forwarded-For`; overwrite on the API block too if such clients exist. Never set
`TRUST_PROXY=true` when clients can reach port 8080 or 3000 directly — that is
why the compose file publishes both on `127.0.0.1` only
(`API_BIND`/`WEB_BIND`). Full rule:
[Security → Client IP](./security.md#client-ip-trust_proxy-and-trusted_proxy_cidrs).

`/readyz` (database ping, `503 draining` during shutdown) is for the compose
health check only — keep it out of the proxy; route `/healthz` instead.

---

## Caddy (recommended, bundled)

The repo ships a ready [`deploy/caddy/Caddyfile`](../../deploy/caddy/Caddyfile)
behind a Compose **profile** named `caddy`, so it only starts when you ask for
it:

```bash
# from the repo root, with DOMAIN + ACME_EMAIL set in .env
docker compose --profile caddy up -d
# or, equivalently:
make self-host-up            # PROFILE defaults to `caddy`
```

Run the plain `docker compose up -d` (or `make self-host-up PROFILE=`) if you
bring your own proxy instead — then the `caddy` service never starts.

### The Caddyfile, explained

```caddyfile
{
	# ACME account email. docker-compose.yml turns ACME_EMAIL into the whole
	# `email <address>` line, or an empty line when ACME_EMAIL is blank (an
	# empty `email` directive is a Caddyfile syntax error).
	{$ACME_EMAIL_LINE}
}

{$DOMAIN} {
	encode zstd gzip

	# API + health checks → Go backend.
	@api path /v1/* /healthz
	handle @api {
		# The api honours X-Request-Id from this (trusted) proxy, so never
		# relay a client-chosen one; the api generates a fresh id.
		reverse_proxy api:8080 {
			header_up -X-Request-Id
		}
	}

	# Everything else → Next.js web app.
	handle {
		reverse_proxy web:3000
	}
}
```

- `{$DOMAIN}` and `{$ACME_EMAIL_LINE}` are read from the environment. The
  `caddy` service in `docker-compose.yml` passes `DOMAIN` through from your
  `.env` and builds `ACME_EMAIL_LINE` from `ACME_EMAIL` (`email <address>`, or
  nothing when `ACME_EMAIL` is blank). If you run Caddy outside Compose, set
  `ACME_EMAIL_LINE="email you@example.com"` yourself or leave it unset.
- With a **real domain**, Caddy provisions a Let's Encrypt certificate
  automatically over HTTP/TLS-ALPN and renews it forever.
- With `DOMAIN=localhost`, Caddy serves a **locally-trusted internal cert** —
  handy for kicking the tyres before you point a real domain at the box.
- `api:8080` and `web:3000` are Docker **service names** on the internal
  `calendium` network; Caddy reaches them by name, no host ports involved.
- Caddy sets `X-Forwarded-For` to the connecting client's address and ignores
  whatever the client sent. Compose sets `TRUST_PROXY=true` on `api` and `web`
  by default, and Caddy's Compose-network address is inside the default
  `TRUSTED_PROXY_CIDRS`, so the web app's auth rate limits and the API's
  public-endpoint limits and logs see the real client and each client gets its
  own bucket, LAN clients included
  ([why](./security.md#client-ip-trust_proxy-and-trusted_proxy_cidrs)). Do not
  set `TRUST_PROXY=false` with this profile.
- Caddy drops any client-sent `X-Request-Id` on the API route, so the API
  always generates the id (it honours an inbound one only from a trusted
  proxy). The nginx sample sends nginx's own `$request_id` instead.

### Before you `up`: DNS + ports

For certificate issuance to succeed:

1. Point a DNS **A** record (and **AAAA** if you have IPv6) for `your-domain`
   at the server's public IP.
2. Make ports **80 and 443** (TCP, plus 443/UDP for HTTP/3) reachable from the
   internet — the `caddy` service publishes `80:80`, `443:443`, and
   `443:443/udp`.
3. Set `DOMAIN` and `ACME_EMAIL` in `.env`, then bring the stack up.

If you `up` before DNS resolves or with 80/443 blocked, Caddy's ACME challenge
fails and you get no certificate — see
[Troubleshooting → Certificate issuance fails](./troubleshooting.md#certificate-issuance-fails).

### Cert persistence (already handled)

Caddy's issued certificates live in the **`caddy_data`** named volume
(`/data` in the container). This is mounted for you in `docker-compose.yml`.
**Do not remove it** — if you wipe it, every restart re-requests certificates
and you'll quickly hit Let's Encrypt rate limits.

---

## nginx + certbot (alternate)

Use this when you already run nginx or need caching / rewrites / large-body
tuning. The repo ships a sample at
[`deploy/nginx/calendium.conf`](../../deploy/nginx/calendium.conf).

Run the stack **without** the caddy profile so nginx (on the host) can reach the
published ports — `docker-compose.yml` publishes `web` on
`${WEB_BIND:-127.0.0.1}:${WEB_PORT:-3000}` and `api` on
`${API_BIND:-127.0.0.1}:${API_PORT:-8080}` by default, which the sample proxies as `127.0.0.1:3000` / `127.0.0.1:8080`. No
`.env` change is needed for a same-host nginx.

```bash
sudo cp deploy/nginx/calendium.conf /etc/nginx/sites-available/calendium.conf
sudo ln -s /etc/nginx/sites-available/calendium.conf /etc/nginx/sites-enabled/
sudo nano /etc/nginx/sites-available/calendium.conf   # set server_name
sudo mkdir -p /var/www/certbot && sudo nginx -t && sudo systemctl reload nginx
sudo certbot certonly --webroot -w /var/www/certbot -d your-domain
# then uncomment the HTTPS server block + the :80 -> :443 redirect, reload nginx
```

The sample already sets the headers that matter (`X-Forwarded-For`,
`X-Forwarded-Proto`, `X-Forwarded-Host`). nginx runs on the host and reaches
the containers through the published loopback ports, so the address `api` and
`web` see as their peer (the Docker bridge gateway, or `127.0.0.1`) is inside
the default `TRUSTED_PROXY_CIDRS`. The same path routing applies:

```nginx
location /v1/      { proxy_pass http://calendium_api; ... }
location = /healthz { proxy_pass http://calendium_api; ... }
location /         { proxy_pass http://calendium_web; ... }
```

> **You MUST forward `X-Forwarded-Proto` (plus `X-Real-IP` /
> `X-Forwarded-For`)** — the sample does. Omit it and OAuth callbacks build
> `http://` URLs behind your `https://` proxy and you get infinite redirect
> loops. This is the #1 self-host support ticket across the industry. See
> [Troubleshooting → Redirect loop](./troubleshooting.md#redirect-loop-behind-a-proxy).

> **Keep `TRUST_PROXY=true` for the web app and the API** (the Compose default;
> set it yourself if you run either outside Compose). Their rate limits key on
> the right-most `X-Forwarded-For` entry that is not inside `TRUSTED_PROXY_CIDRS`.
> Overwriting the header (`proxy_set_header X-Forwarded-For $remote_addr;`, which
> the sample does for `location /`) and appending it (`$proxy_add_x_forwarded_for`,
> AWS ALB, Google Cloud Load Balancing) both work for internet clients; with
> appending, a client that is itself inside `TRUSTED_PROXY_CIDRS` can pick its
> own key, so overwrite when LAN/VPN clients use the proxy. Every proxy hop must be inside
> `TRUSTED_PROXY_CIDRS`: the default covers loopback and private ranges, so add
> any public CDN ranges yourself. With `TRUST_PROXY=false` every client shares
> the proxy's bucket on all auth endpoints, including JWT minting, so one
> client can lock everyone out.

The sample sets `client_max_body_size 25m` for attachment uploads — raise it if
your users send larger attachments. certbot's own systemd timer handles renewal.

---

## Traefik (alternate, Docker-native)

If you already run Traefik as your fleet's edge router, drop the bundled Caddy
and the published host ports, attach the `api`/`web` services to Traefik's
network, and route by labels. The routing rule is the same — `/v1` and
`/healthz` to the API, everything else to web. Sketch:

```yaml
api:
  labels:
    - "traefik.enable=true"
    - "traefik.http.routers.calendium-api.rule=Host(`your-domain`) && (PathPrefix(`/v1`) || Path(`/healthz`))"
    - "traefik.http.routers.calendium-api.tls.certresolver=le"
    - "traefik.http.services.calendium-api.loadbalancer.server.port=8080"
web:
  labels:
    - "traefik.enable=true"
    - "traefik.http.routers.calendium-web.rule=Host(`your-domain`)"
    - "traefik.http.routers.calendium-web.tls.certresolver=le"
    - "traefik.http.services.calendium-web.loadbalancer.server.port=3000"
```

Give the API router a **higher priority** than web (or rely on Traefik's
longest-match) so `/v1` wins. As with nginx, ensure Traefik forwards
`X-Forwarded-Proto` (it does by default). Route the API path prefix with higher
specificity than the catch-all web rule.

---

## Cloud load balancers (no Caddy)

On AWS ALB / GCP HTTPS LB / Azure Container Apps ingress, the platform
terminates TLS with a managed cert — don't run Caddy. Create two routes on your
domain: one matching `/v1/*` and `/healthz` → the API target (port 8080, health
check `GET /readyz`, which also fails while the database is unreachable or the
API is draining for shutdown; keep `/readyz` itself out of the public routes),
and a default route → the web target (port 3000, health check `/api/health`).
The load balancer's own addresses must be inside `TRUSTED_PROXY_CIDRS` (they
usually are private VPC addresses) for `TRUST_PROXY=true` to see real clients. See the cloud sections of the deployment guide for per-provider
steps.

---

## LAN-only / no domain

For a home server with no public domain, set `DOMAIN=your-host.lan` and let
Caddy mint an internal cert (`tls internal`-style, which the bundled
`DOMAIN=localhost` path does automatically), then trust Caddy's root CA on your
clients — or skip the proxy entirely and reach `web` on `:3000` and `api` on
`:8080` directly over the LAN (no TLS). Without a proxy, set
`API_BIND=0.0.0.0`, `WEB_BIND=0.0.0.0` **and** `TRUST_PROXY=false` in `.env`:
the compose file publishes both on loopback only by default, and with
`TRUST_PROXY=true` a LAN client could spoof `X-Forwarded-For`. (The default
`TRUSTED_PROXY_CIDRS` includes Docker's `172.16.0.0/12` bridge gateway, which
Docker can NAT direct LAN clients to; if a proxy must stay trusted, narrow
`TRUSTED_PROXY_CIDRS` to its address instead.) Note that native mobile apps generally
require HTTPS, so an internal cert (trusted on-device) is the better LAN path.

---

## Related pages

- [Configuration & Environment Reference](./configuration.md) — `DOMAIN`, `ACME_EMAIL`, `NEXT_PUBLIC_API_URL`
- [Provider & Integration Setup](./providers.md) — OAuth redirect URIs use your proxied `https://your-domain`
- [Security Hardening](./security.md) — which ports to publish and which to keep internal
- [Troubleshooting](./troubleshooting.md) — redirect loops, cert issuance, clients can't reach `/v1/instance`
