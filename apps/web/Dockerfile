# syntax=docker/dockerfile:1
#
# Calendium web image (Next.js standalone output). The build context MUST be
# the repo root so bun workspaces (@calendium/shared) resolve:
#   docker build -f apps/web/Dockerfile .
# NEXT_PUBLIC_* values are inlined into the browser bundle at BUILD time and
# are passed as build args by docker-compose.yml.

# ── deps: install the whole bun workspace (cached on the manifests) ─────────
FROM oven/bun:1 AS deps
WORKDIR /app
# Copy every workspace manifest so `bun install --frozen-lockfile` resolves
# the full graph, then install once (hoisted to /app/node_modules).
COPY package.json bun.lock bunfig.toml ./
COPY apps/web/package.json apps/web/package.json
COPY apps/mobile/package.json apps/mobile/package.json
COPY apps/desktop/frontend/package.json apps/desktop/frontend/package.json
COPY packages/shared/package.json packages/shared/package.json
RUN bun install --frozen-lockfile

# ── build: compile the Next.js standalone bundle ────────────────────────────
FROM oven/bun:1 AS build
WORKDIR /app
ENV NEXT_TELEMETRY_DISABLED=1
# bun keeps each workspace's deps in that workspace's node_modules (it does not
# fully hoist to the root), so copy the entire installed /app tree — root AND
# per-workspace node_modules — then overlay the source (the root .dockerignore
# keeps node_modules out of `COPY . .`, so the installed deps survive).
COPY --from=deps /app ./
COPY . .

ARG NEXT_PUBLIC_SUPABASE_URL
ARG NEXT_PUBLIC_SUPABASE_ANON_KEY
ARG NEXT_PUBLIC_API_URL
ENV NEXT_PUBLIC_SUPABASE_URL=$NEXT_PUBLIC_SUPABASE_URL \
    NEXT_PUBLIC_SUPABASE_ANON_KEY=$NEXT_PUBLIC_SUPABASE_ANON_KEY \
    NEXT_PUBLIC_API_URL=$NEXT_PUBLIC_API_URL

RUN bun run --cwd apps/web build

# ── runtime: minimal Node server for the standalone output ──────────────────
FROM node:22-alpine AS runner
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

USER nextjs
EXPOSE 3000
CMD ["node", "apps/web/server.js"]
