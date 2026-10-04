# syntax=docker/dockerfile:1
#
# Calendium backend image. Builds BOTH cmd/api and cmd/worker as static,
# CGO-disabled binaries and ships them in one minimal image. The default
# command runs the API; the worker service overrides it with `worker`
# (see docker-compose.yml). Build context is the repo root so the root
# .dockerignore applies:  docker build -f backend/Dockerfile .

# ── build: compile static api + worker binaries ─────────────────────────────
# golang:1.26-alpine resolved 2026-10-04 (docker buildx imagetools inspect)
FROM golang:1.26-alpine@sha256:8ac98ca534ac3f51e1f420a1dd2c15e74c75cfa0f23f3ad27eb5d7236c349a0c AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux GOFLAGS=-trimpath

# Cache module downloads on go.mod/go.sum before copying the full source.
COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ ./
# Version stamped into httpapi.Version (GET /v1/instance "version");
# docker-compose passes VERSION=${VERSION:-dev}, release pipelines pass the
# tag. The worker does not link httpapi, so -X is a no-op there (kept so both
# builds share one flag set).
ARG VERSION=dev
RUN go build -ldflags="-s -w -X calendium/backend/internal/adapter/in/httpapi.Version=${VERSION}" -o /out/api ./cmd/api \
 && go build -ldflags="-s -w -X calendium/backend/internal/adapter/in/httpapi.Version=${VERSION}" -o /out/worker ./cmd/worker

# ── runtime: minimal, non-root image running either binary ──────────────────
# alpine:3.22 resolved 2026-10-04 (docker buildx imagetools inspect)
FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
RUN apk add --no-cache ca-certificates wget \
 && addgroup -S calendium && adduser -S -G calendium calendium

COPY --from=build /out/api /usr/local/bin/api
COPY --from=build /out/worker /usr/local/bin/worker

USER calendium
EXPOSE 8080

# Liveness against the unauthenticated /healthz endpoint (busybox wget).
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1

# Default entrypoint is the API. Run the worker with:  command: ["worker"].
CMD ["api"]
