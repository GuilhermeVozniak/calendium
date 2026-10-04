# Calendium — self-hosting helpers.
#
#   make self-host-up      # build & start the full stack
#   make gen-secrets       # print fresh TOKEN_ENCRYPTION_KEY, INTERNAL_API_SECRET
#                          # and BETTER_AUTH_SECRET lines to paste into .env
#   make gen-secret        # print one fresh 32-byte hex secret
#   make db-backup         # dump the database to backups/
#
# Override the reverse proxy: `make self-host-up PROFILE=` to skip Caddy.

COMPOSE ?= docker compose
PROFILE ?= caddy
profile_flag = $(if $(strip $(PROFILE)),--profile $(PROFILE),)

.DEFAULT_GOAL := help
.PHONY: help self-host-up self-host-down self-host-logs gen-secret gen-secrets db-backup db-restore test-api test-api-cover

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

self-host-up: ## Build & start the stack (db, api, worker, web, +caddy)
	$(COMPOSE) $(profile_flag) up -d --build

self-host-down: ## Stop the stack (keeps the db volume)
	$(COMPOSE) $(profile_flag) down

self-host-logs: ## Tail logs from all services
	$(COMPOSE) $(profile_flag) logs -f --tail=100

gen-secret: ## Print one fresh 32-byte hex secret (TOKEN_ENCRYPTION_KEY or INTERNAL_API_SECRET)
	@openssl rand -hex 32

gen-secrets: ## Print every required secret as .env lines (a distinct value each)
	@echo "TOKEN_ENCRYPTION_KEY=$$(openssl rand -hex 32)"
	@echo "INTERNAL_API_SECRET=$$(openssl rand -hex 32)"
	@echo "BETTER_AUTH_SECRET=$$(openssl rand -base64 32)"

db-backup: ## Dump the database to backups/calendium-<timestamp>.sql.gz
	@mkdir -p backups
	@$(COMPOSE) exec -T db sh -c 'pg_dump -U "$$POSTGRES_USER" "$$POSTGRES_DB"' \
		| gzip > backups/calendium-$$(date +%Y%m%d-%H%M%S).sql.gz
	@echo "backup written to backups/"

db-restore: ## Restore a dump: make db-restore FILE=backups/xxx.sql.gz
	@test -n "$(FILE)" || { echo "usage: make db-restore FILE=backups/calendium-YYYYMMDD-HHMMSS.sql.gz"; exit 1; }
	@gunzip -c "$(FILE)" | $(COMPOSE) exec -T db sh -c 'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB"'

test-api: ## Run the Go backend test suite (needs Docker for testcontainers)
	cd backend && go test ./...

test-api-cover: ## Run the backend suite printing per-package coverage
	cd backend && go test -cover ./...
