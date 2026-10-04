# Deploy Calendium on Azure

Two paths:

- **Path A — VM + Docker Compose + Azure Database for PostgreSQL Flexible Server**
  (recommended start). One VM running the same
  [`docker-compose.yml`](../../docker-compose.yml) as the [VPS guide](./vps.md),
  with the bundled Postgres swapped for a **Flexible Server**.
- **Path B — Azure Container Apps + Flexible Server + Key Vault** (managed
  containers), with the worker as a single always-on replica.

Both stay in **self-hosted mode** (`SELF_HOSTED=true`, no Paddle, all features
unlocked). Skim the [Overview](./README.md) and [Configuration](./configuration.md)
if you're new.

---

## Path A — VM + Docker Compose + Flexible Server

### A1. Azure Database for PostgreSQL Flexible Server

```bash
az group create -n calendium-rg -l eastus
az postgres flexible-server create -g calendium-rg -n calendium-db \
  --tier Burstable --sku-name Standard_B1ms --version 17 \
  --admin-user calendium --admin-password "$PW" --database-name calendium
```

Prefer **private access** (a delegated VNet subnet) so Postgres isn't exposed; or
use public access with firewall rules limited to the VM's IP.

### A2. VM

- `Standard_B2s` (Ubuntu 24.04) or larger.
- Install Docker + a non-root user as in [VPS Steps 2–3](./vps.md#step-2--create-a-non-root-user-and-harden-ssh).
- NSG: allow `22` (your IP), `80`, `443`. Point a single **A record** for your
  domain at the VM's public IP.

### A3. Connection string (TLS is required)

Azure enforces TLS, so use `sslmode=require`:

```dotenv
DATABASE_URL=postgres://calendium:<pw>@calendium-db.postgres.database.azure.com:5432/calendium?sslmode=require
```

For `verify-full`, download the Azure PostgreSQL root CA, mount it into the
`api`/`worker` containers, and add `&sslrootcert=/certs/<azure-ca>.pem`.

### A4. Skip the bundled Postgres and launch

Same override pattern as the other managed-DB targets — stop the bundled `db` and
drop the dependency on it:

```yaml
# docker-compose.external-db.yml
services:
  db:
    profiles: ["never"]
  api:
    depends_on: !reset []
  worker:
    depends_on: !override
      api:
        condition: service_started
```

```bash
docker compose -f docker-compose.yml -f docker-compose.external-db.yml \
  --profile caddy up -d --build
curl https://<your-domain>/v1/instance      # mode: self_host
```

Set the remaining `.env` values (`SELF_HOSTED=true`, `TOKEN_ENCRYPTION_KEY`,
`DOMAIN`, `ACME_EMAIL`, Better Auth (`BETTER_AUTH_SECRET`/`BETTER_AUTH_URL`),
provider OAuth, loopback `API_BIND`/`WEB_BIND` defaults) exactly as in the
[VPS guide](./vps.md#step-6--clone-and-configure). Updating,
backups, and pointing apps at your server are identical.

---

## Path B — Azure Container Apps + Flexible Server + Key Vault (brief)

Container Apps gives `api` and `web` managed HTTPS FQDNs, so there's **no Caddy**.
The **worker runs with ingress disabled and a fixed single replica** (no
scale-to-zero — it's a continuous poller). Because `NEXT_PUBLIC_*` is baked into
the web bundle at build time, the web image is built with your API's public FQDN as
a build arg.

### B1. Registry, environment, database

```bash
az acr create -g calendium-rg -n calendiumacr --sku Basic
az containerapp env create -g calendium-rg -n calendium-env -l eastus
# Flexible Server: as in A1.
```

### B2. Build and push (repo-root context, shipped Dockerfiles)

```bash
ACR=calendiumacr.azurecr.io; TAG=$(git rev-parse --short HEAD)
az acr login -n calendiumacr

docker buildx build --platform linux/amd64 -f backend/Dockerfile \
  -t $ACR/calendium/backend:$TAG --push .

docker buildx build --platform linux/amd64 -f apps/web/Dockerfile \
  --build-arg NEXT_PUBLIC_API_URL=https://api.<your-domain> \
  -t $ACR/calendium/web:$TAG --push .
```

### B3. Secrets via Key Vault + managed identity

Create a **user-assigned managed identity**, grant it Key Vault `get` on secrets,
and reference them by URI (no secrets in the deploy command):

```bash
az containerapp create -g calendium-rg -n calendium-api --environment calendium-env \
  --image $ACR/calendium/backend:$TAG \
  --user-assigned $IDENTITY_ID --ingress external --target-port 8080 \
  --secrets "token-key=keyvaultref:https://<vault>.vault.azure.net/secrets/token-key,identityref:$IDENTITY_ID" \
            "db-url=keyvaultref:https://<vault>.vault.azure.net/secrets/db-url,identityref:$IDENTITY_ID" \
  --env-vars "SELF_HOSTED=true" "TOKEN_ENCRYPTION_KEY=secretref:token-key" \
             "DATABASE_URL=secretref:db-url" "BETTER_AUTH_URL=https://app.<your-domain>"
# The api only needs BETTER_AUTH_URL (it derives AUTH_JWKS_URL/AUTH_ISSUER and
# verifies tokens against the public JWKS — no auth secret on the backend).
```

### B4. Web and worker apps

```bash
# Web — hosts Better Auth, so it needs Postgres + the auth secret at runtime
az containerapp create -g calendium-rg -n calendium-web --environment calendium-env \
  --image $ACR/calendium/web:$TAG --ingress external --target-port 3000 \
  --user-assigned $IDENTITY_ID \
  --secrets "auth-secret=keyvaultref:https://<vault>.vault.azure.net/secrets/auth-secret,identityref:$IDENTITY_ID" \
            "db-url=keyvaultref:https://<vault>.vault.azure.net/secrets/db-url,identityref:$IDENTITY_ID" \
  --env-vars "BETTER_AUTH_URL=https://app.<your-domain>" "BETTER_AUTH_SECRET=secretref:auth-secret" \
             "DATABASE_URL=secretref:db-url"

# Worker — no ingress, exactly one always-on replica, runs the worker binary
az containerapp create -g calendium-rg -n calendium-worker --environment calendium-env \
  --image $ACR/calendium/backend:$TAG --ingress disabled \
  --min-replicas 1 --max-replicas 1 --command worker \
  --user-assigned $IDENTITY_ID \
  --env-vars "SELF_HOSTED=true" "DATABASE_URL=secretref:db-url" \
             "TOKEN_ENCRYPTION_KEY=secretref:token-key"
```

The shipped backend image defaults to the `api` binary; `--command worker` runs
`cmd/worker`.

### B5. Domains and networking

- Map custom domains `api.<domain>` → `calendium-api` and `app.<domain>` →
  `calendium-web`. The web build's `NEXT_PUBLIC_API_URL` must match the API's
  public FQDN (set in B2).
- Because web (`app.<domain>`) and API (`api.<domain>`) are separate origins, set
  `CORS_ALLOWED_ORIGINS=https://app.<domain>` and `PUBLIC_API_URL=https://api.<domain>`
  (the OAuth callback base — register `https://api.<domain>/v1/accounts/callback/{provider}`).
- Use Flexible Server VNet/firewall rules so the Container Apps environment can
  reach `5432`.
- Passwordless option: Flexible Server supports **Microsoft Entra ID** auth via the
  same managed identity (no DB password in Key Vault) — the app fetches an access
  token to use as the DB password.

---

## See also

- [Configuration & Environment Variables](./configuration.md) · [Database](./configuration.md#core--database)
- [Authentication Setup](./providers.md#1-authentication-better-auth--built-in) · [Connecting Provider Accounts](./providers.md)
- [Backups & Restore](./backups.md) · [Upgrading](./upgrades.md) · [Troubleshooting](./troubleshooting.md)
- Other targets: [VPS](./vps.md) · [AWS](./aws.md) · [GCP](./gcp.md) · [Home server](./local.md)
