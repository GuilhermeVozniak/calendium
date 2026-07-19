# Task 16 report — M2.7 final gates + docs (branch feat/m2-7-collab, HEAD 3ca4afe)

STATUS: COMPLETE — all gates PASS. (This file previously held the M2.5 task-16 report; superseded by this phase's Task 16.)

## Gate evidence (every command with explicit exit code)

### 1. Backend (cd backend)
- `go build ./...` — **exit 0**
- `REQUIRE_DOCKER=1 go test ./...` — **exit 0**. All 13 test packages ok, incl. `adapter/out/postgres` 8.8s (testcontainers, real Docker Postgres). 951 top-level test functions passed (counted via cached `go test ./... -json`, top-level `"Action":"pass"` test events; subtests excluded).
- `gofmt -l .` — printed nothing, **exit 0**
- `golangci-lint run ./...` — "0 issues.", **exit 0**

### 2. Migrations sanity
- Files present: **0001–0018 complete** (+ embed.go), including `0014_team_thread_activity.sql`.
- **CORRECTION (final-review fix wave, b9e3753):** this report originally claimed "0014 is intentionally absent — T10 needed no new table." That rationale was **false**. Task 10 (team read statuses / reply indicators, migration 0014 with `messages.rfc_message_id` + `team_thread_activity`) had been implemented and reviewed but was never merged to the branch — the gap in the numbering was lost work, not a design decision, and this report rationalized it instead of flagging it (final review C1). T10 was merged in `b9e3753`; the chain is now complete with no gap.
- Runner note (still true, now moot): `backend/internal/migrate/migrate.go` globs `*.sql` from the embed FS, `slices.Sort` (lexicographic), applies each file not yet in `schema_migrations` — no sequential-numbering requirement.
- Fresh-DB application from zero: the `adapter/out/postgres` testcontainers suite boots Docker Postgres and applies the full embedded chain before its 951-suite subset ran green (exit 0 above).

### 3. Monorepo JS
- `bun install --frozen-lockfile` — **exit 0** ("Checked 1301 installs across 1279 packages (no changes)")
- `bun run --cwd packages/shared test` — **exit 0**, 7 files / **301 tests** passed
- `bun run --cwd apps/web test` — **exit 0**, 81 files / **954 tests** passed
- `bun run --cwd apps/desktop/frontend test` — **exit 0**, 16 files / **201 tests** passed
- `bun run --cwd apps/mobile test` — **exit 0**, **211 tests** passed (jest; known force-exit teardown warning, pre-existing)
- `bun run typecheck` (web + shared tsc) — **exit 0**
- `bun run lint:js` (biome, main checkout) — **exit 0**, 391 files checked, 0 errors (43 pre-existing warnings)

### 4. Desktop Go host (apps/desktop) + mobile
- `go build ./...` — **exit 0** (benign macOS `-lobjc` dup-library ld warning)
- `go test ./...` — **exit 0**, ok calendium/desktop
- `golangci-lint run ./...` — "0 issues.", **exit 0**
- Mobile test script covered above (211 passed).

### 5. E2e demo-mode Playwright (apps/web)
- First run: **exit 1** — `node_modules/.bin/next: No such file or directory`. Mechanical env breakage: `next` is hoisted to root `node_modules/.bin` under the current bun workspace layout; `playwright.config.ts` (unchanged since M2-era commit 0b0cc76) hardcoded the apps/web-relative path.
- Fix: webServer command now uses `bun x next …` (resolves the workspace-local binary in either hoisting layout). Config-only change.
- Re-run `bunx playwright test` — **exit 0**, **41 passed** (27.9s), incl. the serial perf project: j/k p95 and archive p95 both under the 100ms budget.

## Brief-specific items (docs)
- `docs/collaboration.md` (new): the four encoded decisions (service-layer authz + ErrForbidden/GetMember-first; SSE over EventBus with LISTEN/NOTIFY scale path; explicit-share-only privacy incl. token-hash, busy-only redaction, 35d cap, opt-out read statuses, grants-don't-outlive-membership; RFC Message-ID conversation correlation), provider-sharing out-of-scope rationale, team-billing flag (per-user $50/yr unchanged; seat-based plan = future Stripe work), deferred items (round-robin, multi-instance SSE, mobile/desktop collab screens).
- `docs/feature-map.md`: flipped all 11 Collaboration rows (Shared Conversations, Team Comments, Team read statuses, Team Snippets, Team scheduling, Shared calendars w/ permissions, Find a Time grid, Find Time inline, Team availability overview, Team scheduling links, EA delegation) from `planned` → `scaffolded` with shipped-implementation notes per row.
- `docs/state-and-gaps.md`: added "M2.7 — Collaboration (COMPLETE)" section (migrations 0011–0018 map, 0014-gap note), flipped the Collaboration gap line, summary updated (M2.3–M2.6 noted as complete per archived ledgers).

## Commits made by this task
1. `fix(web): resolve next binary via bun x in playwright webServer` — mechanical e2e unblock.
2. `docs: collaboration architecture decisions and feature-map status` — docs + this report.

## Verdict
ALL GATES PASS (as of HEAD 3ca4afe; see the correction in section 2 — the migration chain is 0001–0018 complete after the T10 merge, and the "0013→0015 gap by design" claim was wrong). Backend 951 top-level tests, shared 301, web 954, desktop-frontend 201, mobile 211, e2e 41; gofmt/golangci/biome/typecheck clean; migrations apply from zero on Docker Postgres.
