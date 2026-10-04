# syntax=docker/dockerfile:1
#
# Calendium web image (Next.js standalone output). The build context MUST be
# the repo root so bun workspaces (@calendium/shared) resolve:
#   docker build -f apps/web/Dockerfile .
# NEXT_PUBLIC_* values are inlined into the browser bundle at BUILD time and
# are passed as build args by docker-compose.yml. Auth is Better Auth, hosted
# by this app at /api/auth/*; its secrets are runtime env, not build args.

# ── deps: install the whole bun workspace (cached on the manifests) ─────────
# oven/bun:1.3 resolved 2026-10-04 (docker buildx imagetools inspect)
FROM oven/bun:1.3@sha256:e10577f0db68676a7024391c6e5cb4b879ebd17188ab750cf10024a6d700e5c4 AS deps
WORKDIR /app
# Copy every workspace manifest so `bun install --frozen-lockfile` resolves
# the full graph, then install once (hoisted to /app/node_modules).
COPY package.json bun.lock bunfig.toml ./
COPY apps/web/package.json apps/web/package.json
COPY apps/mobile/package.json apps/mobile/package.json
COPY apps/desktop/frontend/package.json apps/desktop/frontend/package.json
COPY packages/shared/package.json packages/shared/package.json
# --ignore-scripts: the root package's `prepare` runs `lefthook install`, a
# developer git-hook step that needs git and a .git dir — neither exists in
# this image (.git is dockerignored), so without the flag the install fails
# with "exec: git: executable file not found".
RUN bun install --frozen-lockfile --ignore-scripts

# ── build: compile the Next.js standalone bundle ────────────────────────────
# oven/bun:1.3 resolved 2026-10-04 (docker buildx imagetools inspect)
FROM oven/bun:1.3@sha256:e10577f0db68676a7024391c6e5cb4b879ebd17188ab750cf10024a6d700e5c4 AS build
WORKDIR /app
ENV NEXT_TELEMETRY_DISABLED=1
# bun keeps each workspace's deps in that workspace's node_modules (it does not
# fully hoist to the root), so copy the entire installed /app tree — root AND
# per-workspace node_modules — then overlay the source (the root .dockerignore
# keeps node_modules out of `COPY . .`, so the installed deps survive).
COPY --from=deps /app ./
COPY . .

# Only NEXT_PUBLIC_* are inlined at build time. Better Auth's secrets
# (BETTER_AUTH_SECRET/BETTER_AUTH_URL/DATABASE_URL/GOOGLE_*/APPLE_*) are read at
# RUNTIME and supplied by docker-compose to the runner stage — never baked here.
ARG NEXT_PUBLIC_API_URL
ENV NEXT_PUBLIC_API_URL=$NEXT_PUBLIC_API_URL
# Support address on the marketing/legal pages (blank = support@calendium.app).
ARG NEXT_PUBLIC_SUPPORT_EMAIL
ENV NEXT_PUBLIC_SUPPORT_EMAIL=$NEXT_PUBLIC_SUPPORT_EMAIL
# Paddle.js overlay checkout (docs/payments.md): public client token + env.
ARG NEXT_PUBLIC_PADDLE_CLIENT_TOKEN
ARG NEXT_PUBLIC_PADDLE_ENV=sandbox
ENV NEXT_PUBLIC_PADDLE_CLIENT_TOKEN=$NEXT_PUBLIC_PADDLE_CLIENT_TOKEN \
    NEXT_PUBLIC_PADDLE_ENV=$NEXT_PUBLIC_PADDLE_ENV

RUN bun run --cwd apps/web build

# ── runtime: minimal Node server for the standalone output ──────────────────
# node:22-alpine resolved 2026-10-04 (docker buildx imagetools inspect)
FROM node:22-alpine@sha256:0a7108bf6c7bf5de370ffb1a3ed6be93d405b43ff159f681a8d18c0e2bc2e402 AS runner
WORKDIR /app
ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1 \
    PORT=3000 \
    HOSTNAME=0.0.0.0
RUN addgroup -S nodejs && adduser -S -G nodejs nextjs

# Standalone server + traced node_modules (monorepo layout preserved), the
# hashed static assets, and public/. outputFileTracingRoot=<repo root> puts
# the server entrypoint at apps/web/server.js inside the standalone dir.
COPY --from=build --chown=nextjs:nodejs /app/apps/web/.next/standalone ./
COPY --from=build --chown=nextjs:nodejs /app/apps/web/.next/static ./apps/web/.next/static
COPY --from=build --chown=nextjs:nodejs /app/apps/web/public ./apps/web/public
# Operator CLI (self-host password reset without SMTP). It imports only
# node:* builtins, its sibling password-hash.mjs and `pg` — `pg` (with
# pg-pool/pg-protocol/pg-types/pgpass…) is in the standalone trace at
# /app/node_modules because lib/auth.ts uses it; better-auth is NOT, which is
# why the CLI hashes with node:crypto (scripts/password-hash.test.ts).
COPY --from=build --chown=nextjs:nodejs /app/apps/web/scripts ./apps/web/scripts

USER nextjs
EXPOSE 3000
# Readiness for compose/orchestrators (busybox wget ships in node:22-alpine);
# /api/health is force-dynamic so a cached 200 can never mask a dead server.
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:3000/api/health || exit 1

# The preload appends the socket peer to X-Forwarded-For (Next only fills it
# when absent), so auth rate limits key on the real immediate peer.
CMD ["node", "--require", "./apps/web/scripts/forwarded-for-peer.cjs", "apps/web/server.js"]
