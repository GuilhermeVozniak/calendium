# syntax=docker/dockerfile:1
#
# Calendium backend image. Builds BOTH cmd/api and cmd/worker as static,
# CGO-disabled binaries and ships them in one minimal image. The default
# command runs the API; the worker service overrides it with `worker`
# (see docker-compose.yml). Build context is the repo root so the root
# .dockerignore applies:  docker build -f backend/Dockerfile .

# ── build: compile static api + worker binaries ─────────────────────────────
FROM golang:1.26-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux GOFLAGS=-trimpath

# Cache module downloads on go.mod/go.sum before copying the full source.
COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ ./
RUN go build -ldflags="-s -w" -o /out/api ./cmd/api \
 && go build -ldflags="-s -w" -o /out/worker ./cmd/worker

# ── runtime: minimal, non-root image running either binary ──────────────────
FROM alpine:3.20
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
