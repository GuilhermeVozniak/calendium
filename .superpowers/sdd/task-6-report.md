# Task 6 Report — Mobile: AsyncStorage persister + outbox + reachability replay

**Status:** COMPLETE
**Branch:** worktree-agent-ad737d28a2b8a0cb1 (based on main, `feat/m2-6-platform` merged in first — clean fast-forward)
**Commit:** 542be87 `feat(mobile): AsyncStorage query persistence and offline outbox with reachability replay`

## What was built

- `apps/mobile/lib/offline.ts` (new): `getOutbox()` singleton over `new Outbox(createKvOutboxStorage(AsyncStorage), generateOutboxId)`. BINDING FLAG honored: `generateOutboxId` injected as the `newId` constructor param — uses `crypto.randomUUID` when present, otherwise a time+random token (Hermes-safe; no new dep — expo-crypto is not in the app and ids are device-local only). `isNetworkError` distinguishes fetch/connectivity failures from `ApiRequestError` (server answered → never queued). `queueIfOffline(error, action)` is the screens' single decision point. `startOutboxReplay(queryClient)` replays on `Network.addNetworkStateListener` (isConnected && isInternetReachable !== false), `AppState 'active'` (reachability-gated via `getNetworkStateAsync`), plus one gated startup kick for entries queued in a previous session; invalidates `['threads']`/`['thread']` only when something actually replayed; returns cleanup. `useQueuedCount()` (useSyncExternalStore over `outbox.subscribe`) drives the badge.
- `apps/mobile/lib/query-client.ts`: exports `persister` (`createAsyncStoragePersister({ storage: AsyncStorage, key: 'rq:v1' })`) and `persistOptions` (buster `calendium-cache-v1`, maxAge 7d, `PERSISTED_PREFIXES` allowlist + success-only dehydrate); queryClient default `gcTime` 7d. Allowlist excludes `api-online`, `instant-replies`, `send-suggestion`.
- `apps/mobile/context/providers.tsx`: `QueryClientProvider` → `PersistQueryClientProvider`; starts `startOutboxReplay` for the app lifetime. (Brief listed `_layout.tsx`, but the provider tree actually lives in context/providers.tsx, which _layout renders — modified there.)
- `apps/mobile/app/(tabs)/inbox.tsx`: act/snooze mutations — on network error, queue durably and KEEP the optimistic state as an honest pending change; real amber "N queued" badge (testID `outbox-badge`) in the header, live via notify(). Server rejections still roll back + alert (truthful rollback preserved).
- `apps/mobile/app/thread/[id].tsx`: archive + snooze catch paths queue-on-network-error the same way.
- Deps added: `@tanstack/react-query-persist-client`, `@tanstack/query-async-storage-persister` (5.101.2).

## Tests (TDD)

`apps/mobile/lib/offline.test.ts` (new, 14 tests, written red-first): id fallback without `crypto.randomUUID`, isNetworkError honesty, persister config (buster/maxAge/gcTime, allowlist excludes api-online, pending never dehydrated), durable enqueue to `outbox:v1`, enqueue-on-network-error vs server-rejection, replay-on-reconnect + query invalidation, no replay while unreachable, at-least-once honesty (entry stays queued when replay itself hits a network failure, no invalidation), AppState-active trigger, cleanup. Heavy async interaction tests placed LAST per the jest-expo ordering lesson; real-macrotask flush instead of waitFor. Added AsyncStorage/expo-network mocks to the two screen suites that now import offline.ts.

Validation (explicit exit codes): `bunx tsc --noEmit` → 0; `bunx jest --ci --forceExit` → 0 (21 suites, 195 tests, 0 fail); scoped `bunx biome check` → 0 (one pre-existing warning in the untouched markOpened effect).

## Concerns

- `bun.lock` (repo root) is in the commit — unavoidable side effect of the dep add; everything else is apps/mobile only.
- Replay currently invalidates `['threads']`/`['thread']`; if later tasks queue draft/calendar actions on mobile, widen the invalidation set.
- `AppState.addEventListener`'s subscription is optional-chained on cleanup: jest-expo's mock returned undefined in one ordering; harmless in the real app.

## Review fixes (post-review, commit 542be87 minors)

1. **Honest queue failure (inbox)**: `apps/mobile/app/(tabs)/inbox.tsx` act/snooze `onError` no longer fire-and-forgets `queueIfOffline` — they now `await` it (with `.catch(() => false)`) and, when the AsyncStorage write fails, fall through to the existing revert + alert instead of leaving optimistic UI standing with nothing queued (and no unhandled rejection). Mirrors the thread screen's awaited pattern. Action-bar archive button gained `testID="triage-archive"` for the test.
2. **Replay-conflict surfacing**: `apps/mobile/lib/offline.ts` `replayNow` now invalidates `['threads']`/`['thread']` when `report.conflicts.length > 0` even with `replayed=0` (stale optimistic state refetches to server truth) and raises a one-line `Alert` naming how many queued changes the server rejected. Conflict entries stay in the outbox (status `'conflict'`) until dismissed — no auto-dismiss.

Tests: inbox suite gained an offline-queue-persistence-failure test (storage save rejection → optimistic archive reverted, alert fired, no unhandled rejection); offline suite gained a conflict-replay test (invalidation of both keys + notice + entry kept as `'conflict'`). The inbox Opens pagination test moved to its own LAST describe — its fetch+fetchNextPage tail corrupts any later render in that file (verified: the new test's query never even mounted when placed after it), preserving the heavy-async-last discipline.

Validation: `bunx tsc --noEmit` → 0; `bunx jest --ci --forceExit` → 0 (21 suites, 197 tests); scoped `bunx biome check` on the 4 touched files → 0.
