# Task 16 Report — CrmProvider port + HubSpot adapter (contact context + email logging)

**Status: COMPLETE** · Branch `worktree-agent-task16-hubspot` (worktree at `.claude/worktrees/agent-task16-hubspot`, based on `main` @ 3d37489, which already contains `feat/m2-8-integrations`; `backend/internal/domain/team.go` verified present).

## What shipped

### Backend
- `backend/internal/domain/crm.go` (new): `IntegrationVendor` (+ `IntegrationVendorHubSpot`), `CrmContact`, `CrmDeal`, `CrmContext`, `CrmEmailLog` — brief-verbatim shapes.
- `backend/internal/port/driven.go` (additive, end): `CrmProvider` (brief-verbatim) plus the narrow consumer-side `CrmConnection` / `CrmConnectionStore` — clearly bannered as the seam to be wired to Task 9's integration-connection repo at merge. **No OAuth/token storage was built here.**
- `backend/internal/port/driving.go` (additive, end): `CrmService` (brief-verbatim).
- `backend/internal/adapter/out/hubspot/` (new, own package, stdlib-only): contacts search (`POST /crm/v3/objects/contacts/search`, email EQ), associated deals (`GET …/associations/deals` + `POST /crm/v3/objects/deals/batch/read`), portal id (`GET /account-info/v3/details`) for deep links, email logging (`POST /crm/v3/objects/emails` + `PUT …/associations/contacts/{id}/email_to_contact`). 401 → `domain.ErrUnauthorized`; miss → `Contact: nil` with zero extra vendor calls; `http.Client` has an explicit Timeout when not injected.
- `backend/internal/service/crm.go` (new): entitlement-gated fan-out over connected vendors; unknown-vendor connections skipped (never 501); token refresh via `port.OAuthGateway` on 401 with exactly one retry, refreshed tokens persisted through `CrmConnectionStore` (refresh token preserved when the provider omits it); per-(user,email) 5-min in-memory context cache on the injected Clock.
- `backend/internal/adapter/in/httpapi/crm.go` (new) + routes `GET /v1/crm/context` / `POST /v1/crm/log` (registered at end of `New()`); `Deps.Crm` nil ⇒ 501 `not_implemented`; wired + zero connections ⇒ 200 `[]` (pinned by test). Email logging is an explicit per-message action — no bulk export path exists anywhere.
- `InstanceFeatures.HubSpot` (`features.hubspot`) + `config.HubSpot` (HUBSPOT_CLIENT_ID/SECRET); `main.go` advertises the flag and carries a bannered `Crm: nil` note with the exact one-constructor wiring recipe for integration (needs Task 9's repo).
- **No migration** (none demanded by the brief; storage is Task 9's).

### Shared + web
- `packages/shared/src/types.ts`: CRM block mirroring domain/crm.go; `InstanceFeatures.hubspot?`. `client.ts`: `getCrmContext`, `logCrmEmail`. New `crm-client.test.ts`.
- `apps/web/lib/use-crm.ts` (new): `useCrmContext` (5-min staleTime, demo fixture ONLY behind `DEMO_MODE`), `logCrmEmail` helper.
- `apps/web/components/app/crm-card.tsx` (new): pane CRM section — vendor header, contact name/title/company, deal list with stage badges + amount/close date, "Open in HubSpot" links; collapses to nothing on loading/error/empty (hidden without a connection).
- `contact-pane.tsx`: two tight hunks (import + `<CrmCard/>`); `crm-log-menu.tsx` (new): per-message overflow menu with the explicit "Log to HubSpot" toast-confirmed action, rendered only when a HubSpot connection exists; `thread-view.tsx`: two tight hunks (import + menu in the expanded-message header, inbound/outbound inferred per message).
- Tests: contact-pane (CRM section rendered from mock / hidden without connection), crm-log-menu (explicit-click-only logging, payload, toasts, hidden states), thread-view stubs the menu.

## Gates (explicit)
- `gofmt -l` clean · `go vet ./...` clean · `go test ./...` **2114 passed / 19 pkgs, exit 0** · `golangci-lint run` (touched pkgs + cmd) **0 issues, exit 0**
- `bun run test:shared` **308 passed, exit 0** · `bun run test:web` **967 passed, exit 0 — 6/6 consecutive runs** · `bun run typecheck` (web+shared) **exit 0**
- Biome: `biome check .` cannot run inside this worktree (repo config ignores `**/.claude`, and the worktree lives under `.claude/worktrees/`); ran the identical config minus that exclusion over every touched TS/TSX file → **0 errors** (4 pre-existing warn-level findings in thread-view.tsx, untouched).

## Notes / flags for the integrator
1. **Wiring seam:** `Deps.Crm` is nil in `main.go` until Task 9's `integration_connections` repo exists; recipe in the banner. If Task 9 also declares `domain.IntegrationVendor`, keep ONE declaration.
2. **Pane gating** uses the `/v1/crm/context` response (empty ⇒ hidden) rather than the brief's `listIntegrations` (Task 9's client method, not visible in this tree) — same visible behavior, one fewer cross-task dependency; trivially switchable at integration.
3. **Fixed a real test-infra landmine my change exposed:** unmounting an open Radix menu leaks a focus-scope `setTimeout` that fires into the NEXT test file's torn-down jsdom realm (unhandled `dispatchEvent` TypeError, intermittent suite exit 1). crm-log-menu.test.tsx now unmounts + flushes timers in `afterEach`; verified 6/6 green.
4. **Observed (not mine, untouched):** the MAIN checkout at `/Users/guilherme/Dev/pessoal/calendium` currently has an unresolved merge-conflict marker in `packages/shared/src/types.ts` (~line 1049) — its web suite fails wholesale there.
