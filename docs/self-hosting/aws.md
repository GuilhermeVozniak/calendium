# Deploy Calendium on AWS

Two paths, from simplest to most managed:

- **Path A — EC2 + Docker Compose + RDS Postgres** (recommended start). One VM
  running the same [`docker-compose.yml`](../../docker-compose.yml) as the
  [VPS guide](./vps.md), with the bundled Postgres swapped for **Amazon RDS**.
- **Path B — ECS Fargate + RDS + ALB** (no host to patch). Higher-level; drop
  Caddy and let the ALB terminate TLS.

Both keep Calendium in **self-hosted mode** (`SELF_HOSTED=true`, no Paddle, all
features unlocked). If you haven't yet, skim the [Overview](./README.md) and
[Configuration reference](./configuration.md).

---

## Path A — EC2 + Docker Compose + RDS

### A1. RDS for PostgreSQL

Create a PostgreSQL instance (Postgres 15/16/17; `db.t4g.micro` on Graviton is a
cheap start):

- **Publicly accessible: No.** Put it in private subnets (a DB subnet group).
- Enable **automated backups** (7–35 days) and set a maintenance window.
- Note the endpoint, e.g. `calendium.abcd1234.eu-central-1.rds.amazonaws.com`.

### A2. EC2 instance

- Ubuntu 24.04, `t3.small` / `t4g.small` (2 GB+; the Next.js build is
  memory-hungry — add swap or build images elsewhere).
- Attach an **Elastic IP** so the address is stable.
- Install Docker and create a non-root user exactly as in
  [VPS Steps 2–3](./vps.md#step-2--create-a-non-root-user-and-harden-ssh).

### A3. Security groups (the part people get wrong)

- **EC2 SG** — inbound `22` from *your IP only*, `80` and `443` from `0.0.0.0/0`.
- **RDS SG** — inbound `5432` **from the EC2 security group** (source = the
  security-group ID, not a CIDR). Nothing else. This keeps Postgres off the
  internet entirely.

### A4. DNS

Point a single **A record** for your domain at the Elastic IP (Calendium's Caddy
serves one hostname and routes `/v1/*` + `/healthz` to the API internally).

### A5. Configure `.env` for the managed database

Clone the repo and `cp .env.example .env` as in the [VPS guide](./vps.md#step-6--clone-and-configure).
The only differences from the VPS `.env` are the database settings — RDS requires
TLS, so use `sslmode=require`:

```dotenv
DATABASE_URL=postgres://calendium:<password>@calendium.abcd1234.eu-central-1.rds.amazonaws.com:5432/calendium?sslmode=require
```

For full MITM protection use `verify-full` with the RDS root bundle. Download it
on the host, mount it into the `api`/`worker` containers, and reference it:

```bash
curl -o deploy/certs/rds-global-bundle.pem \
  https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem
```

```dotenv
DATABASE_URL=postgres://calendium:<pw>@<endpoint>:5432/calendium?sslmode=verify-full&sslrootcert=/certs/rds-global-bundle.pem
```

(Mount it via a small override — see the `volumes:` in the external-DB override
below.) Keep the other required vars (`SELF_HOSTED=true`, `TOKEN_ENCRYPTION_KEY`,
`INTERNAL_API_SECRET` on api, worker and web, `INTERNAL_API_URL` on web pointing
at the api's private address, never the public load balancer,
`DOMAIN`, `ACME_EMAIL`, Better Auth (`BETTER_AUTH_SECRET`/`BETTER_AUTH_URL`),
provider OAuth, and the loopback `API_BIND`/`WEB_BIND` defaults) exactly as in the
[VPS guide](./vps.md#step-6--clone-and-configure).

### A6. Skip the bundled Postgres

The reference compose ships a `db` service you don't want here. Add a small
**override file** at the repo root that keeps the bundled Postgres from starting
and removes the containers' dependency on it (uses Docker Compose merge tags,
available in Compose v2.24+/v5):

```yaml
# docker-compose.external-db.yml
services:
  db:
    profiles: ["never"]          # bundled Postgres never starts
  api:
    depends_on: !reset []
    # (optional) mount the RDS CA bundle for sslmode=verify-full:
    volumes:
      - ./deploy/certs:/certs:ro
  worker:
    depends_on: !override
      api:
        condition: service_started
    volumes:
      - ./deploy/certs:/certs:ro
```

Launch with both files (put the flags in a shell alias or the systemd unit):

```bash
docker compose -f docker-compose.yml -f docker-compose.external-db.yml \
  --profile caddy up -d --build
```

Verify:

```bash
curl https://<your-domain>/healthz          # 200
curl https://<your-domain>/v1/instance      # mode: self_host, features.billing: false
```

### A7. Secrets via AWS Secrets Manager (recommended)

Instead of a plaintext `.env` on the box, store it as a secret and pull it at
deploy time. Give the EC2 an **instance role** with `secretsmanager:GetSecretValue`
scoped to that secret's ARN — no static AWS keys on the host:

```bash
aws secretsmanager get-secret-value --secret-id calendium/prod \
  --query SecretString --output text > .env
```

Then run the launch command from A6. Rotate by updating the secret and re-pulling.

Everything else — updating (`git pull` + relaunch), backups (`make db-backup`
still works, or rely on RDS automated backups + PITR), pointing apps at your
server — is identical to the [VPS guide](./vps.md#updating).

---

## Path B — ECS Fargate + RDS + ALB (brief)

No host to patch. The **ALB + an ACM certificate terminate TLS**, so you **don't
run Caddy** here — instead the ALB routes to the `api` and `web` containers
directly. Because `NEXT_PUBLIC_*` is baked into the web bundle at build time, the
web image is built with your API hostname as a build arg.

### B1. Build and push images to ECR

Both images build from the **repo root** using the shipped Dockerfiles:

```bash
R=eu-central-1; ACC=<account-id>; TAG=$(git rev-parse --short HEAD)
aws ecr create-repository --repository-name calendium/backend
aws ecr create-repository --repository-name calendium/web
aws ecr get-login-password --region $R \
  | docker login --username AWS --password-stdin $ACC.dkr.ecr.$R.amazonaws.com

# Backend (one image; the worker task overrides the command to "worker")
docker buildx build --platform linux/amd64 -f backend/Dockerfile \
  -t $ACC.dkr.ecr.$R.amazonaws.com/calendium/backend:$TAG --push .

# Web (NEXT_PUBLIC_API_URL is the only compile-time build arg; Better Auth is runtime)
docker buildx build --platform linux/amd64 -f apps/web/Dockerfile \
  --build-arg NEXT_PUBLIC_API_URL=https://api.<your-domain> \
  -t $ACC.dkr.ecr.$R.amazonaws.com/calendium/web:$TAG --push .
```

### B2. RDS

As in A1, in the same VPC. The RDS SG allows `5432` **from the ECS task SG**.
`DATABASE_URL` uses `sslmode=require`.

### B3. Secrets

Put each secret in Secrets Manager and reference it from the task definition's
`secrets:` block (ECS injects them as env vars). Grant the **task execution role**
`secretsmanager:GetSecretValue` + `kms:Decrypt`:

```json
"secrets": [
  { "name": "DATABASE_URL",         "valueFrom": "arn:aws:secretsmanager:...:secret:calendium/DATABASE_URL" },
  { "name": "TOKEN_ENCRYPTION_KEY", "valueFrom": "arn:aws:secretsmanager:...:secret:calendium/TOKEN_ENCRYPTION_KEY" },
  { "name": "BETTER_AUTH_SECRET",   "valueFrom": "arn:aws:secretsmanager:...:secret:calendium/BETTER_AUTH_SECRET" }
]
```

Also set `SELF_HOSTED=true`, `BETTER_AUTH_URL` (public web origin), and provider
OAuth vars; the `web` task reads `BETTER_AUTH_SECRET`, `BETTER_AUTH_URL`, and
`DATABASE_URL` at runtime to run Better Auth.

### B4. Three ECS services on Fargate

- **`api`** — image `calendium/backend`, container port **8080**, registered to an
  ALB target group with health check `GET /readyz` (`503` while the database is
  unreachable or the task is draining for shutdown, so the ALB stops routing to
  it; `/healthz` is liveness only). Keep it at **1 task during a
  rollout** so migrations apply cleanly (or run a one-off `RunTask` of the api
  image before scaling). Set the container's `stopTimeout` above
  `SHUTDOWN_DRAIN_DELAY + SHUTDOWN_TIMEOUT` + 5 s (40 s with the defaults) so
  ECS does not SIGKILL a draining task.
- **`worker`** — same image, **command override `["worker"]`**, `desiredCount = 1`,
  **no load balancer**, **do not autoscale**. It's a continuous poller — exactly
  one replica, always on.
- **`web`** — image `calendium/web`, container port **3000**, its own target group
  (health check `GET /api/health`).
- **Client IP (both `api` and `web`)** — outside Compose `TRUST_PROXY` defaults
  to `false`, which would put every client in the ALB's few rate-limit buckets.
  Set `TRUST_PROXY=true` and `TRUSTED_PROXY_CIDRS=<your VPC CIDR>` (for example
  `10.0.0.0/16`) in both task definitions: ALB nodes connect from private
  addresses inside the VPC and append the client to `X-Forwarded-For`. Check
  one api request log line: its client IP must be yours, not a `10.x` address.
  See [Security → Client IP](./security.md#client-ip-trust_proxy-and-trusted_proxy_cidrs).

### B5. ALB + ACM + Route 53

An HTTPS:443 listener with an ACM certificate, host-based routing:
`api.<domain>` → api target group, `app.<domain>` → web target group. Route 53
alias records point both hostnames at the ALB. Because api and web are now on
separate hostnames, set the web build's `NEXT_PUBLIC_API_URL` to
`https://api.<domain>` (as in B1). This is a genuine cross-origin split, so also
set `CORS_ALLOWED_ORIGINS=https://app.<domain>` (the Go API and Better Auth routes
reflect CORS for it) and `PUBLIC_API_URL=https://api.<domain>` so the
mailbox-connect callback registers as `https://api.<domain>/v1/accounts/callback/{provider}`.

### B6. Connection pooling caveat

If you front RDS with **RDS Proxy** for connection pooling, note it's a
**transaction-mode pooler**, which breaks the pgx driver's prepared statements
(`prepared statement does not exist`). Fix by appending
`&default_query_exec_mode=simple_protocol` to `DATABASE_URL`, or connect to RDS
directly. This does **not** apply to the in-VPC direct connection in Path A. See
[Database](./configuration.md#core--database).

---

## See also

- [Configuration & Environment Variables](./configuration.md) · [Database](./configuration.md#core--database)
- [Authentication Setup](./providers.md#1-authentication-better-auth--built-in) · [Connecting Provider Accounts](./providers.md)
- [Backups & Restore](./backups.md) · [Upgrading](./upgrades.md) · [Troubleshooting](./troubleshooting.md)
- Other targets: [VPS](./vps.md) · [GCP](./gcp.md) · [Azure](./azure.md) · [Home server](./local.md)
