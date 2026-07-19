# Task 6 Report — Realtime SSE (EventBus port, in-memory broker, stream endpoint)

STATUS: COMPLETE

## What was built

- `backend/internal/port/driven.go` — appended `CollabEvent` + `EventBus` verbatim from the brief (new "Realtime event bus (M2.7)" section at end of file; added `encoding/json` import).
- `backend/internal/adapter/out/eventbus/bus.go` (+ `bus_test.go`) — in-process broker: mutex-guarded subscriber set, per-subscriber buffered channels (cap 64, `subscriberBuffer`). Publish is non-blocking (full buffer ⇒ drop for that subscriber only); cancel is idempotent (`sync.Once`), removes the subscriber and closes its channel under the same mutex publishes hold, so send-on-closed-channel is impossible by construction.
- `backend/internal/adapter/in/httpapi/stream.go` (+ `stream_test.go`) — `GET /v1/collab/stream` (authed): subscribes to `user:<uid>` plus `team:<id>` via `Teams.List`, writes `event:`/`data:` frames (data = full CollabEvent JSON), `: keepalive` comment every 25s, `http.Flusher` flushes, exits + unsubscribes on `r.Context().Done()`. Team-list errors map through the codec (500 envelope, no leaked subscription).
- `backend/internal/adapter/in/httpapi/httpapi.go` — Deps gains `Events port.EventBus` and `Teams TeamLister`; route registered in the authed block.
- `backend/internal/adapter/in/httpapi/middleware.go` — `statusRecorder.Flush()` added: the logging wrapper previously swallowed `http.Flusher`, which would have broken SSE through the full stack (caught by TDD through `New(deps)`).
- `backend/cmd/api/main.go` — wires `Events: eventbus.New()`.
- `apps/web/lib/collab-stream.ts` (+ test) — `openCollabStream(onEvent, { signal, … })`: fetch + ReadableStream SSE line parser (Authorization header, no token in URL), exponential-backoff reconnect 1s→30s cap reset on healthy connect, abortable sleep, idempotent closer, malformed frames skipped.
- Harness (`harness_test.go`, additive): `fakeEventBus` (topic-filtering, capture of subscribed topics + live subscriber count) and `fakeTeamLister`, wired into Deps.

## Design decisions

- **TeamLister is consumer-side in httpapi** (`List(ctx, userID) ([]domain.Team, error)`): no `TeamService` exists in `port/driving.go` yet (parallel task) and the brief doesn't spec one. The future full service satisfies it structurally. `Deps.Teams == nil` is tolerated (stream carries only `user:<id>`), so main.go compiles today — Task 3's postgres `store.Teams()` doesn't exist yet either.
- `keepaliveInterval` is a package var (25s) so a test can shrink it and assert the tick.

## Tests / validation (explicit exit codes)

- `go test -race ./...` — **exit 0**, 1755 tests / 18 packages (incl. 12 eventbus tests: round-trip, topic isolation, full-buffer drop w/o deadlock, cancel-closes-channel + idempotency, publish-after-cancel, concurrent publish/subscribe/cancel; 5 stream tests: 401 unauth, scoped delivery over a real HTTP conn with cross-tenant no-leak assertion + context-teardown/no-goroutine-leak, codec error mapping, nil-Teams user-topic-only, keepalive tick).
- `gofmt -l .` — **exit 0**, no files.
- `cd backend && golangci-lint run ./...` — **exit 0**, no issues.
- `bun run lint:go` — **exit 1**, but the sole issue is pre-existing/environmental: `apps/desktop/main.go` `//go:embed all:frontend/dist` fails because the gitignored desktop `frontend/dist` build artifact doesn't exist in a fresh worktree. Backend (this task's surface) lints clean.
- `bun run --cwd apps/web test collab-stream` — **exit 0**, 6 tests.
- `bun run --cwd apps/web typecheck` — **exit 0**; Biome check on the two new TS files — **exit 0**.

## Concerns

- `lint:go`'s desktop leg needs `apps/desktop/frontend/dist` built; unrelated to this task.
- When the M2.7 team service lands, swap `Deps.Teams TeamLister` for the full port type (drop-in) and wire it in main.go.
