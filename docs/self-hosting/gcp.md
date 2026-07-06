# Deploy Calendium on Google Cloud

Two paths:

- **Path A — Compute Engine + Docker Compose + Cloud SQL** (recommended start).
  One VM running the same [`docker-compose.yml`](../../docker-compose.yml) as the
  [VPS guide](./vps.md), with the bundled Postgres swapped for **Cloud SQL**.
- **Path B — Cloud Run (api + web) + Cloud SQL** (serverless, mostly managed),
  with the **worker on a small always-on instance**.

Both stay in **self-hosted mode** (`SELF_HOSTED=true`, no Stripe, all features
unlocked). See the [Overview](./README.md) and [Configuration](./configuration.md)
first if you're new.

---

## Path A — Compute Engine + Docker Compose + Cloud SQL

### A1. Cloud SQL for PostgreSQL

```bash
REGION=europe-west1
gcloud sql instances create calendium-db \
  --database-version=POSTGRES_17 --cpu=1 --memory=4GB \
  --region=$REGION --root-password="$PW"
gcloud sql databases create calendium --instance=calendium-db
gcloud sql users create calendium --instance=calendium-db --password="$PW"
```

Prefer **private IP** (put the instance on your VPC via Private Service Access) so
Postgres is never internet-exposed.

### A2. Compute Engine VM

- `e2-small` (2 GB) or larger, Ubuntu 24.04.
- Install Docker + a non-root user as in [VPS Steps 2–3](./vps.md#step-2--create-a-non-root-user-and-harden-ssh).
- Firewall: allow `80` and `443` from the internet, `22` from your IP. Point a
  single **A record** for your domain at the VM's external IP.

### A3. Connect to Cloud SQL

**Option 1 — private IP.** If the VM and Cloud SQL share a VPC with Private
Service Access, connect straight to the private IP with TLS:

```dotenv
DATABASE_URL=postgres://calendium:<pw>@<private-ip>:5432/calendium?sslmode=require
```

**Option 2 — Cloud SQL Auth Proxy sidecar.** Add the proxy as a compose service on
the same `calendium` network; the proxy encrypts the hop, so the app connects
plaintext *to the proxy*. Put this in an override file (see A4):

```yaml
  cloudsql-proxy:
    image: gcr.io/cloud-sql-connectors/cloud-sql-proxy:2
    command: ["--private-ip", "--port=5432", "<PROJECT>:<REGION>:calendium-db"]
    networks: [calendium]
```

```dotenv
DATABASE_URL=postgres://calendium:<pw>@cloudsql-proxy:5432/calendium?sslmode=disable
```

### A4. Skip the bundled Postgres and launch

As on other managed-DB targets, add an override that stops the bundled `db` and
drops the containers' dependency on it (and, for Option 2, adds the proxy):

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
  # cloudsql-proxy: ...  (paste the A3 Option-2 block here if using the proxy)
```

```bash
docker compose -f docker-compose.yml -f docker-compose.external-db.yml \
  --profile caddy up -d --build
curl https://<your-domain>/v1/instance      # mode: self_host
```

Set the rest of `.env` (`SELF_HOSTED=true`, `TOKEN_ENCRYPTION_KEY`, `DOMAIN`,
`ACME_EMAIL`, Supabase, provider OAuth, loopback `API_PORT`/`WEB_PORT`) exactly as
in the [VPS guide](./vps.md#step-6--clone-and-configure). Everything else
(updating, backups, pointing apps at your server) is identical.

---

## Path B — Cloud Run + Cloud SQL (brief)

`api` and `web` run as stateless Cloud Run services; there's **no Caddy** (Cloud
Run terminates TLS and gives each service an HTTPS URL). The **worker cannot run on
plain Cloud Run** — it's a continuous poller and Cloud Run scales CPU around
requests. Run it on a tiny Compute Engine VM, or as a Cloud Run service pinned to
`--no-cpu-throttling --min-instances=1 --max-instances=1`.

### B1. Enable APIs and create the registry / database

```bash
PROJECT=<project>; REGION=europe-west1
gcloud services enable run.googleapis.com sqladmin.googleapis.com \
  artifactregistry.googleapis.com cloudbuild.googleapis.com secretmanager.googleapis.com
gcloud artifacts repositories create calendium \
  --repository-format=docker --location=$REGION
# Cloud SQL: as in A1.
```

### B2. Build and push (repo-root context, shipped Dockerfiles)

```bash
REG=$REGION-docker.pkg.dev/$PROJECT/calendium; TAG=$(git rev-parse --short HEAD)

docker buildx build --platform linux/amd64 -f backend/Dockerfile \
  -t $REG/backend:$TAG --push .

docker buildx build --platform linux/amd64 -f apps/web/Dockerfile \
  --build-arg NEXT_PUBLIC_API_URL=https://api.<your-domain> \
  --build-arg NEXT_PUBLIC_SUPABASE_URL=https://<ref>.supabase.co \
  --build-arg NEXT_PUBLIC_SUPABASE_ANON_KEY=<anon key> \
  -t $REG/web:$TAG --push .
```

### B3. Secrets

```bash
printf '%s' "$TOKEN_KEY" | gcloud secrets create TOKEN_ENCRYPTION_KEY --data-file=-
# ...repeat for SUPABASE_JWT_SECRET, GOOGLE_CLIENT_SECRET, MS_CLIENT_SECRET, etc.
```

### B4. Deploy the API

Cloud Run connects to Cloud SQL over a **unix socket** at
`/cloudsql/<INSTANCE_CONNECTION_NAME>`, so the DSN uses `host=/cloudsql/...` (the
connector encrypts — no TLS param needed). Cloud Run injects `PORT` (8080), which
the API honors, so it just works:

```bash
INSTANCE=$PROJECT:$REGION:calendium-db
gcloud run deploy calendium-api \
  --image $REG/backend:$TAG --region $REGION --allow-unauthenticated \
  --add-cloudsql-instances $INSTANCE \
  --set-env-vars "SELF_HOSTED=true,INSTANCE_NAME=Calendium,SUPABASE_URL=https://<ref>.supabase.co,DATABASE_URL=postgres://calendium:<pw>@/calendium?host=/cloudsql/$INSTANCE&sslmode=disable" \
  --set-secrets "TOKEN_ENCRYPTION_KEY=TOKEN_ENCRYPTION_KEY:latest,SUPABASE_JWT_SECRET=SUPABASE_JWT_SECRET:latest"
```

### B5. Deploy the web app and the worker

```bash
# Web (stateless)
gcloud run deploy calendium-web --image $REG/web:$TAG \
  --region $REGION --allow-unauthenticated

# Worker — same backend image, run the worker binary, always on, single instance
gcloud run deploy calendium-worker --image $REG/backend:$TAG \
  --region $REGION --no-cpu-throttling --min-instances=1 --max-instances=1 \
  --no-allow-unauthenticated --command worker \
  --add-cloudsql-instances $INSTANCE \
  --set-env-vars "SELF_HOSTED=true,DATABASE_URL=postgres://calendium:<pw>@/calendium?host=/cloudsql/$INSTANCE&sslmode=disable" \
  --set-secrets "TOKEN_ENCRYPTION_KEY=TOKEN_ENCRYPTION_KEY:latest"
```

The shipped backend image's default command is `api`; overriding it with
`--command worker` runs `cmd/worker`.

### B6. Domains and pooling

- Map custom domains: `gcloud run domain-mappings create --service calendium-web
  --domain app.<domain>` (and `calendium-api` → `api.<domain>`), or front both with
  a Global HTTPS Load Balancer. The web build's `NEXT_PUBLIC_API_URL` must match
  the API's public hostname (set in B2).
- If you ever put a **transaction-mode pooler** (e.g. Supabase Supavisor `:6543`,
  PgBouncer) in front of Postgres, append `&default_query_exec_mode=simple_protocol`
  to `DATABASE_URL` or the pgx driver errors with `prepared statement does not
  exist`. The Cloud SQL socket/direct connection above doesn't need it. See
  [Database](./configuration.md#core--database).

---

## See also

- [Configuration & Environment Variables](./configuration.md) · [Database](./configuration.md#core--database)
- [Authentication Setup](./providers.md#1-supabase-authentication--required) · [Connecting Provider Accounts](./providers.md)
- [Backups & Restore](./backups.md) · [Upgrading](./upgrades.md) · [Troubleshooting](./troubleshooting.md)
- Other targets: [VPS](./vps.md) · [AWS](./aws.md) · [Azure](./azure.md) · [Home server](./local.md)
