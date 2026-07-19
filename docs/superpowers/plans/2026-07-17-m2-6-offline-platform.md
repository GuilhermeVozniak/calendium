# M2.6 — Offline & Platform Polish Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
> **Migration numbering:** the eight M2 phase plans were authored in parallel, so migration filenames here are provisional — at execution time use the next free number in `backend/migrations/` and update references in the affected task.

**Spec:** `docs/superpowers/specs/2026-07-17-m2-roadmap-design.md` (phase M2.6). Feature-map rows covered: Offline mode, Preloading architecture, Global desktop shortcuts, Menu-bar mini calendar, Menu-bar next event + instant join, Auto-join meetings, Multiple account switching, Themes and dark mode (named themes).

## Goal

Make Calendium feel instant and dependable off the network, and make the desktop app earn its place on the OS:

1. **Offline mode** — reads/search served from a persisted TanStack Query cache (IndexedDB on web/desktop, AsyncStorage on mobile); every outbound action (triage, snooze, reminder, draft save/send) captured in a durable outbox and replayed on reconnect with explicit conflict handling. No fabricated success: queued state is visibly queued.
2. **Preloading** — thread bodies prefetched on hover and around the selection, next page prefetched before the user reaches the end, so navigation is cache-hits.
3. **Desktop host features** — system-wide compose/search hotkeys, a menu-bar next-event countdown with instant join (text-based tray; the graphical mini-calendar is explicitly deferred — see Task 10 feasibility notes), and auto-join at meeting start.
4. **Multi-account switching** — `Ctrl/Cmd+1..9` scopes the inbox to one connected account (backend `ThreadQuery.AccountID` already filters; the HTTP param, client param, and UI plumbing are the gap).
5. **Named themes** — 4 curated token sets (Neutral, Ocean, Forest, Sunset) switchable from the command palette, persisted locally *and* per user on the backend.

## Architecture

```
packages/shared/src/offline/
  outbox.ts          Outbox core: durable action queue, coalescing, replay w/ conflict handling
  kv.ts              AsyncKeyValueStore interface + IndexedDB + in-memory implementations
                     (mobile passes AsyncStorage directly — same shape)

apps/web             PersistQueryClientProvider (IDB persister) in components/providers.tsx
                     lib/offline/ (connectivity + app-level outbox singleton + replay wiring)
                     lib/use-mail.ts mutations fall through to the outbox on network failure
                     public/sw.js grows static-asset caching + manifest (installable PWA)

apps/desktop/frontend  same persist + outbox wiring as web (Vite/React, shares @calendium/shared)

apps/desktop (Go host, module calendium/desktop — separate from backend)
  hotkeys.go / hotkeys_darwin.go   global shortcuts via golang.design/x/hotkey
  tray.go                          menu-bar countdown + join via github.com/energye/systray
  scheduler.go                     auto-join timer (pure, clock-injected, unit-tested)
  Frontend pushes upcoming-event data DOWN into the host via a bound method
  (`SetUpcomingEvents`); the host never talks to the API itself. Host pushes
  actions UP as runtime events ("global-shortcut", "tray-action", "auto-join").

apps/mobile          AsyncStorage persister + outbox with AsyncStorage-backed storage;
                     expo-network reachability triggers replay

backend (stdlib-only)
  httpapi/mail.go        parse `accountId` into port.ThreadQuery.AccountID (repo already filters)
  user preferences       new user_preferences table + GET/PUT /v1/me/preferences (theme)
```

**Offline data flow (all clients):** UI mutation → optimistic cache patch (already exists) → API call → on *network* failure (or known-offline), enqueue into the Outbox instead of reverting → replay on reconnect in FIFO order → conflicts (4xx) surface as dismissible toasts, never silent drops. Query *reads* survive restarts via `persistQueryClient`; the outbox survives restarts via its own KV record, so a queued send outlives an app quit.

**Conflict policy (explicit):**
- Triage actions (`archive/star/read/...`, snooze, reminder, open) are idempotent last-write-wins; on replay a `404` (thread gone) or other 4xx marks the entry `conflict` and continues — the next refetch reconciles the cache.
- Draft saves are full-replace PUTs (the API's existing contract), so the policy is **client last-write-wins**: the queued body overwrites the server draft. A `404` (draft deleted elsewhere) → `conflict`, surfaced with "Draft was deleted on another device — kept a local copy" (body re-materialized into a fresh draft via `saveDraft`).
- Draft sends: `409` from `/send` (e.g. already sent elsewhere) → `conflict`, surfaced; never auto-retried.
- `401/402/429` and network failures stop the replay and leave everything queued (auth/billing/backoff are not conflicts).
- `5xx` retries with an attempt counter; after 5 attempts the entry is marked `failed` and surfaced.

## Tech Stack

- **TanStack Query v5** (already everywhere) + new deps `@tanstack/react-query-persist-client` and `@tanstack/query-async-storage-persister` in `apps/web`, `apps/desktop/frontend`, `apps/mobile`.
- **IndexedDB** raw (hand-rolled ~60-line KV in `packages/shared` — no `idb-keyval` dep) for web/desktop persistence; **`@react-native-async-storage/async-storage`** (already a mobile dep) for RN.
- **Go desktop host deps (desktop module only):** `golang.design/x/hotkey` (global shortcuts), `github.com/energye/systray` (tray; the getlantern fork that runs alongside Wails v2 — see Task 10). Wails stays v2.12.
- **Backend:** Go stdlib only, as always. One migration + one small handler pair.
- Existing gates: Biome, golangci-lint, vitest/jest/go test, Playwright e2e (demo mode), lefthook pre-push.

## Global Constraints

- **Honesty policy:** mock/sample data only behind explicit demo flags (`DEMO_MODE` on web, equivalents on desktop/mobile). Offline UI states show *real* queued/conflict/failed state — never fake success. New offline behavior must not silently swallow real errors outside demo mode.
- **BACKEND stays stdlib-only** (`backend/go.mod` gains zero dependencies). The **desktop Go module** (`apps/desktop/go.mod`, module `calendium/desktop`) is separate and may take the two vetted deps named above — nothing else.
- **TDD:** every task writes the failing test first, then the implementation. Run the named test command and see it fail before implementing.
- **Gates before every commit:** `bunx biome check .` at repo root; `cd backend && golangci-lint run && go test ./...` when backend touched; `cd apps/desktop && go vet ./... && go test ./...` when desktop host touched; workspace `bun run test` + `bun run typecheck` for each touched frontend.
- **Conventional commits** (`feat(web): …`, `feat(desktop): …`, `feat(shared): …`, `feat(backend): …`), one commit per task.
- Design system unchanged: shadcn new-york, neutral tokens, radius 0.625rem; new themes are token-set variants, not component restyles.

---

### Task 1: Outbox core in `@calendium/shared`

The heart of offline mode: a durable, coalescing, replayable action queue. Platform-agnostic — storage is injected.

**Files:**
- `packages/shared/src/offline/outbox.ts` (new)
- `packages/shared/src/offline/outbox.test.ts` (new)
- `packages/shared/src/index.ts` (export the new module)

**Interfaces:** (this is the real implementation core — use it verbatim as the starting point)

```ts
// packages/shared/src/offline/outbox.ts
import { ApiClient, ApiRequestError } from '../client';
import type { DraftInput, ThreadAction } from '../types';

/** One durable, replayable user action captured while offline. */
export type OutboxAction =
  | { kind: 'thread_action'; threadId: string; action: ThreadAction }
  | { kind: 'thread_snooze'; threadId: string; until: string }
  | { kind: 'thread_reminder'; threadId: string; remindAt: string | null }
  | { kind: 'thread_open'; threadId: string }
  /** draftId may be a local id (`local-…`) for drafts created offline. */
  | { kind: 'draft_save'; draftId: string; accountId: string; input: DraftInput }
  | { kind: 'draft_send'; draftId: string };

export type OutboxEntryStatus = 'queued' | 'conflict' | 'failed';

export interface OutboxEntry {
  id: string;
  seq: number; // replay order
  createdAt: string;
  attempts: number;
  status: OutboxEntryStatus;
  lastError: string | null;
  action: OutboxAction;
}

export interface OutboxStorage {
  load(): Promise<OutboxEntry[]>;
  save(entries: OutboxEntry[]): Promise<void>;
}

export interface ReplayReport {
  replayed: OutboxEntry[];
  conflicts: OutboxEntry[];
  failed: OutboxEntry[];
  /** True when replay stopped early (network/auth/5xx backoff) with entries still queued. */
  interrupted: boolean;
}

export const LOCAL_DRAFT_PREFIX = 'local-';
export const isLocalDraftId = (id: string): boolean => id.startsWith(LOCAL_DRAFT_PREFIX);

/** Pairs of actions that cancel each other out when both are still queued. */
const CANCELLING: Partial<Record<ThreadAction, ThreadAction>> = {
  star: 'unstar',
  unstar: 'star',
  read: 'unread',
  unread: 'read',
  archive: 'move_to_inbox',
  move_to_inbox: 'archive',
};

const MAX_ATTEMPTS = 5;
const MAX_ENTRIES = 500;

export class Outbox {
  private entries: OutboxEntry[] = [];
  private seq = 0;
  private loaded = false;
  private replaying = false;
  private readonly listeners = new Set<(entries: readonly OutboxEntry[]) => void>();

  constructor(
    private readonly storage: OutboxStorage,
    private readonly newId: () => string = () => crypto.randomUUID()
  ) {}

  async init(): Promise<void> {
    this.entries = await this.storage.load();
    this.seq = this.entries.reduce((m, e) => Math.max(m, e.seq), 0) + 1;
    this.loaded = true;
    this.notify();
  }

  get pending(): readonly OutboxEntry[] {
    return this.entries;
  }

  get queuedCount(): number {
    return this.entries.filter((e) => e.status === 'queued').length;
  }

  subscribe(fn: (entries: readonly OutboxEntry[]) => void): () => void {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  }

  /**
   * Queue an action, coalescing against what's already queued:
   * - a thread_action cancelled by an opposite queued one removes BOTH
   *   (star→unstar while offline = nothing to replay);
   * - duplicate thread_actions are dropped;
   * - snooze / reminder / open / draft_save replace the queued entry for the
   *   same target (keeping the original seq so a later queued send still
   *   replays after the save it depends on).
   * Returns the stored entry, or null when the action coalesced away.
   */
  async enqueue(action: OutboxAction): Promise<OutboxEntry | null> {
    const queued = this.entries.filter((e) => e.status === 'queued');

    if (action.kind === 'thread_action') {
      const cancel = CANCELLING[action.action];
      const opposite = queued.find(
        (e) =>
          e.action.kind === 'thread_action' &&
          e.action.threadId === action.threadId &&
          e.action.action === cancel
      );
      if (opposite) {
        this.entries = this.entries.filter((e) => e.id !== opposite.id);
        await this.persist();
        return null;
      }
      const dup = queued.find(
        (e) =>
          e.action.kind === 'thread_action' &&
          e.action.threadId === action.threadId &&
          e.action.action === action.action
      );
      if (dup) return dup;
    }

    const replaceable = queued.find((e) => this.sameTarget(e.action, action));
    if (replaceable) {
      replaceable.action = action;
      replaceable.createdAt = new Date().toISOString();
      await this.persist();
      return replaceable;
    }

    const entry: OutboxEntry = {
      id: this.newId(),
      seq: this.seq++,
      createdAt: new Date().toISOString(),
      attempts: 0,
      status: 'queued',
      lastError: null,
      action,
    };
    this.entries.push(entry);
    if (this.entries.length > MAX_ENTRIES) {
      // Shed non-queued (conflict/failed) history first, then the oldest.
      const shed =
        this.entries.find((e) => e.status !== 'queued') ?? this.entries[0];
      this.entries = this.entries.filter((e) => e.id !== shed.id);
    }
    await this.persist();
    return entry;
  }

  /** Drop queued save/send entries for a draft (undo of a queued send). */
  async removeForDraft(draftId: string): Promise<void> {
    this.entries = this.entries.filter(
      (e) =>
        !(
          (e.action.kind === 'draft_save' || e.action.kind === 'draft_send') &&
          e.action.draftId === draftId &&
          e.status === 'queued'
        )
    );
    await this.persist();
  }

  /** Clear a surfaced conflict/failed entry once the user acknowledged it. */
  async dismiss(entryId: string): Promise<void> {
    this.entries = this.entries.filter((e) => e.id !== entryId);
    await this.persist();
  }

  /**
   * Replay queued entries in seq order. Stops (leaving entries queued) on
   * network errors, 401/402/429, and 5xx (after bumping attempts); marks
   * unrecoverable 4xx entries `conflict` and continues. Concurrent-safe:
   * a second call while replaying returns an empty report.
   */
  async replay(client: ApiClient): Promise<ReplayReport> {
    const report: ReplayReport = { replayed: [], conflicts: [], failed: [], interrupted: false };
    if (this.replaying || !this.loaded) return report;
    this.replaying = true;
    try {
      for (const entry of [...this.entries].sort((a, b) => a.seq - b.seq)) {
        if (entry.status !== 'queued') continue;
        try {
          await this.execute(client, entry.action);
          this.entries = this.entries.filter((e) => e.id !== entry.id);
          report.replayed.push(entry);
        } catch (err) {
          if (err instanceof ApiRequestError) {
            if (err.status === 401 || err.status === 402 || err.status === 429) {
              report.interrupted = true;
              break;
            }
            if (err.status >= 500) {
              entry.attempts += 1;
              entry.lastError = err.message;
              if (entry.attempts >= MAX_ATTEMPTS) {
                entry.status = 'failed';
                report.failed.push(entry);
                continue;
              }
              report.interrupted = true;
              break;
            }
            // Remaining 4xx: server owns the truth — conflict, keep going.
            entry.status = 'conflict';
            entry.lastError = err.message;
            report.conflicts.push(entry);
            continue;
          }
          report.interrupted = true; // network failure
          break;
        }
      }
    } finally {
      this.replaying = false;
      await this.persist();
    }
    return report;
  }

  private async execute(client: ApiClient, action: OutboxAction): Promise<void> {
    switch (action.kind) {
      case 'thread_action':
        await client.actOnThread(action.threadId, action.action);
        return;
      case 'thread_snooze':
        await client.snoozeThread(action.threadId, action.until);
        return;
      case 'thread_reminder':
        await client.setThreadReminder(action.threadId, action.remindAt);
        return;
      case 'thread_open':
        await client.markThreadOpened(action.threadId);
        return;
      case 'draft_save': {
        if (isLocalDraftId(action.draftId)) {
          const created = await client.saveDraft({ ...action.input, accountId: action.accountId });
          this.rewriteDraftId(action.draftId, created.id);
        } else {
          await client.updateDraft(action.draftId, action.input);
        }
        return;
      }
      case 'draft_send':
        await client.sendDraft(action.draftId);
        return;
    }
  }

  /** After an offline-created draft gets a server id, repoint queued entries. */
  private rewriteDraftId(localId: string, serverId: string): void {
    for (const e of this.entries) {
      if (
        (e.action.kind === 'draft_save' || e.action.kind === 'draft_send') &&
        e.action.draftId === localId
      ) {
        e.action = { ...e.action, draftId: serverId };
      }
    }
  }

  private sameTarget(a: OutboxAction, b: OutboxAction): boolean {
    if (a.kind !== b.kind) return false;
    switch (a.kind) {
      case 'thread_snooze':
      case 'thread_reminder':
      case 'thread_open':
        return a.threadId === (b as typeof a).threadId;
      case 'draft_save':
        return a.draftId === (b as typeof a).draftId;
      default:
        return false; // thread_action handled above; draft_send never replaces
    }
  }

  private async persist(): Promise<void> {
    await this.storage.save(this.entries);
    this.notify();
  }

  private notify(): void {
    for (const fn of this.listeners) fn(this.entries);
  }
}
```

**Test strategy:** pure unit tests with an in-memory `OutboxStorage` and a fake `ApiClient` (object with vi.fn methods; only the six methods `execute` touches are needed — cast via `as unknown as ApiClient`). Simulate `ApiRequestError` statuses and a `TypeError('fetch failed')` for network.

**Steps:**
- [ ] Write `packages/shared/src/offline/outbox.test.ts` covering: enqueue+persist round-trip through `init()`; star→unstar coalesces to empty; duplicate `thread_action` dedupes; second snooze for the same thread replaces the first (same seq); `draft_save` replace keeps seq ordering ahead of a queued `draft_send`; replay success removes entries in seq order; local draft id rewrite (`draft_save` local → `saveDraft` called, queued `draft_send` now carries the server id); 404 on `actOnThread` → entry `conflict`, replay continues; 500 ×5 → `failed`; network `TypeError` → `interrupted: true`, entry still `queued`; 401 stops replay without touching entries; re-entrant `replay()` no-ops; `removeForDraft` clears queued save+send; `MAX_ENTRIES` shedding drops non-queued first.
- [ ] `cd packages/shared && bun run test` — confirm the new suite fails (module missing).
- [ ] Implement `outbox.ts` as above; export from `packages/shared/src/index.ts` (`export * from './offline/outbox';`).
- [ ] `cd packages/shared && bun run test && bun run typecheck` — green.
- [ ] `bunx biome check .` — clean. Commit: `feat(shared): offline outbox core with coalescing and conflict-aware replay`.

---

### Task 2: KV storage — IndexedDB + memory + outbox adapter

**Files:**
- `packages/shared/src/offline/kv.ts` (new)
- `packages/shared/src/offline/kv.test.ts` (new)
- `packages/shared/src/index.ts` (export)

**Interfaces:**

```ts
/** Matches @react-native-async-storage/async-storage AND
 *  @tanstack/query-async-storage-persister's expected storage shape. */
export interface AsyncKeyValueStore {
  getItem(key: string): Promise<string | null>;
  setItem(key: string, value: string): Promise<void>;
  removeItem(key: string): Promise<void>;
}

/** Browser IndexedDB-backed KV (web + desktop webview). ~60 lines, raw IDB,
 *  single object store, promisified requests. Lazily opens on first call. */
export function createIndexedDbKv(dbName = 'calendium-offline', storeName = 'kv'): AsyncKeyValueStore;

/** In-memory KV for tests and non-browser environments. */
export function createMemoryKv(): AsyncKeyValueStore;

/** OutboxStorage over any KV: JSON array under key 'outbox:v1'.
 *  Corrupt/unparseable payloads load as [] (never crash the app over cache). */
export function createKvOutboxStorage(kv: AsyncKeyValueStore): OutboxStorage;
```

**Test strategy:** `createMemoryKv` + `createKvOutboxStorage` fully unit-tested; `createIndexedDbKv` tested in `apps/web`'s jsdom vitest env — jsdom has no IndexedDB, so add dev-dep `fake-indexeddb` to `packages/shared` and use `import 'fake-indexeddb/auto'` in the test file (test-only dep; the honesty policy governs runtime mocks, not test fakes).

**Steps:**
- [ ] Write `kv.test.ts`: memory KV get/set/remove semantics; outbox storage round-trip; corrupt JSON → `[]`; IDB KV round-trip + persistence across two `createIndexedDbKv` calls with the same dbName (fake-indexeddb).
- [ ] `cd packages/shared && bun add -d fake-indexeddb && bun run test` — fail.
- [ ] Implement `kv.ts`; export from index.
- [ ] `cd packages/shared && bun run test && bun run typecheck && bunx biome check .` — green. Commit: `feat(shared): async KV stores (IndexedDB, memory) and outbox storage adapter`.

---

### Task 3: Web — persisted query cache + connectivity tracker

**Files:**
- `apps/web/package.json` (add `@tanstack/react-query-persist-client`, `@tanstack/query-async-storage-persister`)
- `apps/web/components/providers.tsx` (modify — swap `QueryClientProvider` for `PersistQueryClientProvider`)
- `apps/web/lib/offline/connectivity.ts` (new)
- `apps/web/lib/offline/connectivity.test.ts` (new)
- `apps/web/components/providers.test.tsx` (new)

**Interfaces:**

```ts
// apps/web/lib/offline/connectivity.ts
/** navigator.onLine seeded, 'online'/'offline' event-driven, cross-checked by
 *  the existing useApiOnline probe (use-mail.ts). Browser online + API down
 *  still counts as offline for queueing purposes. */
export function isOnline(): boolean;
export function subscribeOnline(fn: (online: boolean) => void): () => void;
/** Called by the API-probe layer to fold server reachability in. */
export function reportApiReachable(ok: boolean): void;
export function useOnline(): boolean; // React hook over the above

// providers.tsx additions
const CACHE_BUSTER = 'calendium-cache-v1'; // bump on breaking query-shape changes
// persister: createAsyncStoragePersister({ storage: createIndexedDbKv(), key: 'rq:v1', throttleTime: 1000 })
// PersistQueryClientProvider persistOptions: { persister, maxAge: 7*24*60*60*1000, buster: CACHE_BUSTER,
//   dehydrateOptions: { shouldDehydrateQuery: (q) => PERSISTED_PREFIXES.has(String(q.queryKey[0])) } }
const PERSISTED_PREFIXES = new Set(['threads', 'thread', 'drafts', 'events', 'calendars', 'snippets', 'accounts', 'search']);
// queryClient defaults gain gcTime: 7 days (gcTime must exceed persist maxAge or restored queries GC instantly)
```

Note: `['api-online']` is deliberately NOT persisted. `DEMO_MODE` data flows through the same cache — acceptable, it is labeled by `source` already.

**Steps:**
- [ ] `cd apps/web && bun add @tanstack/react-query-persist-client @tanstack/query-async-storage-persister`.
- [ ] Write `connectivity.test.ts` (jsdom): starts from `navigator.onLine`; flips on window `offline`/`online` events; `reportApiReachable(false)` forces offline even when `navigator.onLine` is true; subscribers fire once per change; `useOnline` re-renders (renderHook).
- [ ] Write `providers.test.tsx`: renders children; a query marked with a persisted prefix is written to the (fake-indexeddb) store after `throttleTime`; `['api-online']` is not persisted. Add `fake-indexeddb` dev-dep to `apps/web`.
- [ ] `cd apps/web && bun run test` — fail.
- [ ] Implement `connectivity.ts`; rewire `providers.tsx` to `PersistQueryClientProvider`; call `reportApiReachable` from `useApiOnline`'s queryFn in `lib/use-mail.ts` (true/false on probe result).
- [ ] `cd apps/web && bun run test && bun run typecheck && bunx biome check .` — green. Commit: `feat(web): persist query cache to IndexedDB and track real connectivity`.

---

### Task 4: Web — outbox wiring (queue triage + compose offline, replay on reconnect)

**Files:**
- `apps/web/lib/offline/queue.ts` (new — app singleton + replay orchestration)
- `apps/web/lib/offline/queue.test.ts` (new)
- `apps/web/lib/use-mail.ts` (modify — mutations fall through to outbox)
- `apps/web/components/app/compose.tsx` (modify — offline save/send path)
- `apps/web/components/app/outbox-indicator.tsx` (new — queued-count pill + conflict toasts host)
- `apps/web/app/(app)/layout.tsx` (modify — mount indicator, start replay listeners)
- `apps/web/e2e/offline.spec.ts` (new — Playwright, demo mode + `context.setOffline`)

**Interfaces:**

```ts
// apps/web/lib/offline/queue.ts
export function getOutbox(): Outbox; // lazy singleton: new Outbox(createKvOutboxStorage(createIndexedDbKv())) + init()
/** True when the error is a transport failure (TypeError from fetch / AbortError),
 *  as opposed to an ApiRequestError the server actually returned. */
export function isNetworkError(err: unknown): boolean;
/** enqueue + toast('Offline — action queued') helper used by mutation paths. */
export function queueAction(action: OutboxAction): Promise<void>;
/** Wire replay triggers: subscribeOnline(true→replay), window focus, 30s interval.
 *  On report: invalidate ['threads'], ['thread'], ['drafts']; toast each conflict
 *  (auto-dismiss thread_open conflicts silently). Returns cleanup. */
export function startOutboxReplay(queryClient: QueryClient): () => void;
export function newLocalDraftId(): string; // LOCAL_DRAFT_PREFIX + crypto.randomUUID()
```

`use-mail.ts` change (surgical): in `runOptimistic`'s catch — currently `if (DEMO_MODE) return; …revert…` — becomes:

```ts
} catch (err) {
  if (DEMO_MODE) return;
  if (isNetworkError(err)) {
    await queueAction(offlineAction); // keep the optimistic patch; no revert
    return;
  }
  /* existing revert + toast path */
}
```

with `runOptimistic` gaining an `offlineAction: OutboxAction` parameter supplied by `act`/`snooze`/`remind`. `markOpened` enqueues `thread_open` on network error. Compose: when `!isOnline()` (or on network error), `saveDraft` uses `newLocalDraftId()` + `queueAction({kind:'draft_save',…})`, send queues `draft_send`; the undo toast for a *queued* send calls `getOutbox().removeForDraft(id)` instead of `unsendDraft`.

**Test strategy:** unit tests mock `getApiClient` (as existing use-mail tests do) and make it throw `new TypeError('fetch failed')` vs `new ApiRequestError(500,…)` to assert queue-vs-revert branching; `startOutboxReplay` tested with fake timers + a stub QueryClient. E2E: demo-mode Playwright drives `context.setOffline(true)`, archives a thread, asserts the queued pill shows "1 queued", goes online, asserts the pill clears.

**Steps:**
- [ ] Write `queue.test.ts`: `isNetworkError` classification (TypeError→true, ApiRequestError→false); `queueAction` enqueues + toasts; `startOutboxReplay` replays on the online transition and invalidates thread queries; conflict report → one toast per conflict except `thread_open`.
- [ ] Extend `apps/web/lib/use-mail.ts` tests (or add `use-mail.offline.test.tsx`): network-failed archive keeps the optimistic removal and enqueues; server-4xx archive still reverts + toasts (existing behavior preserved).
- [ ] `cd apps/web && bun run test` — fail.
- [ ] Implement `queue.ts`, the `use-mail.ts` catch-branch change, compose offline path, `outbox-indicator.tsx` (subscribes to `getOutbox()`, renders queued count + conflict list with dismiss), mount in `(app)/layout.tsx` next to the existing offline banner, call `startOutboxReplay` there.
- [ ] Write + run `e2e/offline.spec.ts`: `cd apps/web && bun run e2e -- offline.spec.ts`.
- [ ] `cd apps/web && bun run test && bun run typecheck && bunx biome check .` — green. Commit: `feat(web): offline outbox for triage and compose with replay on reconnect`.

---

### Task 5: Desktop frontend — persist + outbox (parity with web)

**Files:**
- `apps/desktop/frontend/package.json` (add the two persist deps + `fake-indexeddb` dev)
- `apps/desktop/frontend/src/main.tsx` (modify — `PersistQueryClientProvider`, same persister/buster config as web)
- `apps/desktop/frontend/src/lib/offline.ts` (new — connectivity + `getOutbox` + `startOutboxReplay`; same interfaces as web's `connectivity.ts` + `queue.ts` collapsed into one module, using the desktop `getApiClient` from `src/lib/api.ts`)
- `apps/desktop/frontend/src/lib/offline.test.ts` (new)
- `apps/desktop/frontend/src/views/InboxView.tsx` + `src/views/ComposeView.tsx` (modify — mutation catch-branches queue on network error, mirroring Task 4's pattern; queued-count pill in the InboxView header)

**Interfaces:** identical to Task 3/4 exports; import `Outbox`, `createIndexedDbKv`, `createKvOutboxStorage`, `isLocalDraftId`, `newLocalDraftId` equivalents from `@calendium/shared`. The Wails webview (WKWebView/WebView2) fully supports IndexedDB, so the web persister works unchanged.

**Steps:**
- [ ] Write `offline.test.ts` (vitest+jsdom, fake-indexeddb): singleton init, isNetworkError, replay-on-online invalidation (stub QueryClient), queued triage survives a simulated reload (new Outbox over same storage).
- [ ] `cd apps/desktop/frontend && bun add @tanstack/react-query-persist-client @tanstack/query-async-storage-persister && bun add -d fake-indexeddb && bun run test` — fail.
- [ ] Implement `offline.ts`; rewire `main.tsx`; patch InboxView/ComposeView mutation error paths + pill.
- [ ] `cd apps/desktop/frontend && bun run test && bun run typecheck && bunx biome check .` — green. Commit: `feat(desktop): persisted query cache and offline outbox in the Wails frontend`.

---

### Task 6: Mobile — AsyncStorage persister + outbox + reachability replay

**Files:**
- `apps/mobile/package.json` (add the two persist deps)
- `apps/mobile/lib/query-client.ts` (modify — export the persister alongside the client)
- `apps/mobile/app/_layout.tsx` (modify — `PersistQueryClientProvider`)
- `apps/mobile/lib/offline.ts` (new)
- `apps/mobile/lib/offline.test.ts` (new)
- `apps/mobile/app/(tabs)/inbox.tsx` + `apps/mobile/app/thread/[id].tsx` (modify — triage mutations queue on network error; queued badge in the inbox header)

**Interfaces:**

```ts
// apps/mobile/lib/offline.ts
import AsyncStorage from '@react-native-async-storage/async-storage';
// AsyncStorage satisfies AsyncKeyValueStore structurally — pass it straight in:
export function getOutbox(): Outbox; // new Outbox(createKvOutboxStorage(AsyncStorage))
export function isNetworkError(err: unknown): boolean;
/** Replay triggers: expo-network Network.addNetworkStateListener (already a dep)
 *  isConnected && isInternetReachable !== false → replay; plus AppState 'active'. */
export function startOutboxReplay(queryClient: QueryClient): () => void;
// persister: createAsyncStoragePersister({ storage: AsyncStorage, key: 'rq:v1' })
// same buster 'calendium-cache-v1', maxAge 7d, gcTime 7d, PERSISTED_PREFIXES allowlist
```

**Test strategy:** jest (`jest-expo`) with the official AsyncStorage jest mock (`@react-native-async-storage/async-storage/jest/async-storage-mock` — a test fake, allowed) and a mocked `expo-network` listener; assert enqueue-on-network-error, replay-on-reconnect, and persister config (allowlist excludes `api-online`).

**Steps:**
- [ ] Write `lib/offline.test.ts` per the strategy above.
- [ ] `cd apps/mobile && bun add @tanstack/react-query-persist-client @tanstack/query-async-storage-persister && bun run test` — fail.
- [ ] Implement `offline.ts`; rewire `_layout.tsx` + `query-client.ts`; patch inbox/thread triage paths + badge.
- [ ] `cd apps/mobile && bun run test && bunx biome check .` — green. Commit: `feat(mobile): AsyncStorage query persistence and offline outbox with reachability replay`.

---

### Task 7: Preloading — hover/adjacent/next-page prefetch (web + desktop)

**Files:**
- `apps/web/lib/use-prefetch.ts` (new)
- `apps/web/lib/use-prefetch.test.tsx` (new)
- `apps/web/lib/use-mail.ts` (modify — `useThreadList` → `useInfiniteQuery` with cursor pages; expose `fetchNextPage`/`hasNextPage`; keep `ThreadListResult` consumer shape via a flattened `items` selector so `mail/page.tsx` churn stays small)
- `apps/web/app/(app)/mail/page.tsx` (modify — wire hover + selection + scroll-sentinel prefetch)
- `apps/desktop/frontend/src/views/InboxView.tsx` (modify — same hover/adjacent prefetch; desktop list is non-paginated today, so next-page prefetch is web-only until it paginates)

**Interfaces:**

```ts
// apps/web/lib/use-prefetch.ts
/** Hover-intent prefetch of a thread body: fires queryClient.prefetchQuery(
 *  ['thread', id]) after `delayMs` (default 80) of sustained hover; returns
 *  onMouseEnter/onMouseLeave handlers. Shares the exact queryFn/key of
 *  useThreadDetail so the cache is a hit, never a duplicate. */
export function useThreadHoverPrefetch(delayMs?: number): {
  onHoverStart: (threadId: string) => void;
  onHoverEnd: () => void;
};
/** On selection change, prefetch neighbors idx±1 and idx±2 (j/k targets). */
export function usePrefetchNeighbors(items: Thread[], selectedId: string | null): void;
/** Calls fetchNextPage() when the sentinel row (10th-from-last) mounts and
 *  hasNextPage && !isFetchingNextPage. */
export function useNextPagePrefetch(opts: { nearEnd: boolean; hasNextPage: boolean; isFetching: boolean; fetchNextPage: () => void }): void;
```

**Image / proxy caching considerations (recorded decision, no code this task):** message bodies currently load remote images directly; there is no backend image proxy yet. When the proxy lands (planned with the tracking-protection work), its responses must carry `Cache-Control: private, max-age=604800, immutable` and the service worker (Task 8) gets one cache-first route for `GET <api>/v1/proxy/image`. Until then, prefetching a thread body warms HTML but not remote images — stated limitation, not a gap in this plan.

**Steps:**
- [ ] Write `use-prefetch.test.tsx`: hover under 80ms does not prefetch; sustained hover prefetches exactly once per id; neighbor prefetch fires for ±1/±2 only, skips already-cached fresh entries (`staleTime` respected); sentinel triggers `fetchNextPage` once. Extend use-mail tests for the infinite-query migration (page merge keeps order; `nextCursor` drives `hasNextPage`; demo fallback still single-page).
- [ ] `cd apps/web && bun run test` — fail.
- [ ] Implement; wire into `mail/page.tsx` rows and selection effect; port hover/adjacent prefetch to desktop `InboxView.tsx` (+ its test file).
- [ ] `cd apps/web && bun run test && bun run typecheck && cd ../../apps/desktop/frontend && bun run test && bunx biome check .` (root) — green. Commit: `feat(web,desktop): hover, neighbor, and next-page thread prefetching`.

---

### Task 8: Web PWA — service-worker asset caching + manifest

Scope discipline: the SW caches **static assets and an offline shell only**. API data stays in the TanStack persisted cache (Task 3) — duplicating API responses in the SW would create a second, conflicting source of truth.

**Files:**
- `apps/web/public/sw.js` (modify — currently push-only; add versioned `install`/`activate`/`fetch` handlers)
- `apps/web/public/manifest.webmanifest` (new — name, icons from existing favicon assets, `display: standalone`, theme colors matching light/dark background tokens)
- `apps/web/app/layout.tsx` (modify — `<link rel="manifest">` + `themeColor` metadata)
- `apps/web/lib/sw-caching.test.ts` (new — pure-function tests; see below)

**Interfaces:** keep `sw.js` logic in small pure functions exported for tests via `self.__test` guard or a parallel `lib/sw-caching.ts` imported nowhere else:

```ts
// apps/web/lib/sw-caching.ts  (mirrored into sw.js — sw.js stays dependency-free)
export const SW_CACHE = 'calendium-static-v1';
/** cache-first: /_next/static/**, /fonts/**, .png/.svg/.ico under /;
 *  network-only: anything with Authorization, POST+, /v1/**, /api/auth/**;
 *  navigation requests: network-first with cached '/offline' fallback. */
export function classifyRequest(url: URL, method: string, hasAuth: boolean): 'cache-first' | 'network-only' | 'navigation';
```

Push handling already in `sw.js` is untouched; registration continues through `lib/web-push.ts` (`registration()` already registers `/sw.js` — no double registration; add an unconditional `navigator.serviceWorker.register('/sw.js')` on app mount in `(app)/layout.tsx` so caching works before push opt-in).

**Steps:**
- [ ] Write `sw-caching.test.ts`: classification table (static → cache-first; `/v1/...` and auth'd requests → network-only; document navigations → navigation) and cache-name versioning constant.
- [ ] `cd apps/web && bun run test` — fail.
- [ ] Implement `lib/sw-caching.ts`, extend `public/sw.js` (inline the same logic; `activate` deletes stale `calendium-static-*` caches), add manifest + layout metadata + eager registration.
- [ ] Verify: `cd apps/web && bun run build` (SW and manifest emitted untouched from `public/`), `bun run test && bun run typecheck`, `bunx biome check .`. Commit: `feat(web): installable PWA shell with static-asset service worker caching`.

---

### Task 9: Desktop host — global shortcuts (system-wide compose/search)

**Feasibility (honest):** Wails **v2 has no global-shortcut API** (that ships in v3). `golang.design/x/hotkey` is the right dependency for the v2 host and is allowed here (desktop module only; backend untouched). Platform reality:
- **Windows/Linux:** fully supported — register from a dedicated goroutine that calls `runtime.LockOSThread()` and pumps `hk.Keydown()`.
- **macOS:** `x/hotkey` uses Carbon `RegisterEventHotKey`, which must be driven from the main run loop; `x/hotkey/mainthread` cannot be used because **Wails owns the main thread**. The chosen workaround is a small cgo shim (`hotkeys_darwin.go`) that `dispatch_async`es the register/unregister calls onto the main GCD queue, which the already-running `NSApplication` loop services — a pattern proven in community Wails-v2 + x/hotkey integrations. No Accessibility permission is required for `RegisterEventHotKey` (unlike event taps). **Contingency, stated now:** if the shim misbehaves on a target macOS version, the Settings toggle below ships default-off on macOS and the feature is deferred to the Wails v3 migration — the toggle and event contract are identical either way, so no rework.

**Files:**
- `apps/desktop/hotkeys.go` (new — registration table, goroutine lifecycle, emits Wails event)
- `apps/desktop/hotkeys_darwin.go` / `hotkeys_default.go` (new — build-tagged dispatch shim vs direct registration)
- `apps/desktop/hotkeys_test.go` (new — table/normalization logic only; OS registration is not unit-testable)
- `apps/desktop/app.go` (modify — `SetGlobalShortcutsEnabled(bool)` bound method; start/stop from `startup`)
- `apps/desktop/go.mod` (add `golang.design/x/hotkey`)
- `apps/desktop/frontend/src/lib/wails.ts` (modify — typed `onGlobalShortcut` event subscription)
- `apps/desktop/frontend/src/App.tsx` (modify — on `global-shortcut` event: show/focus window via `runtime.WindowShow`, then open Compose or the command palette)
- `apps/desktop/frontend/src/views/SettingsView.tsx` (modify — "Global shortcuts" toggle, persisted in localStorage, calls the bound method)

**Interfaces:**

```go
// hotkeys.go
const globalShortcutEvent = "global-shortcut" // payload: {"action":"compose"|"search"}

type shortcutSpec struct {
    Action string        // "compose" | "search"
    Mods   []hotkey.Modifier
    Key    hotkey.Key
}

// Defaults: Cmd+Shift+C / Cmd+Shift+K on macOS, Ctrl+Shift+C / Ctrl+Shift+K elsewhere.
func defaultShortcuts() []shortcutSpec

type hotkeyManager struct { /* specs, stop chan, registered []*hotkey.Hotkey */ }
func newHotkeyManager(emit func(action string)) *hotkeyManager
func (m *hotkeyManager) Start() error // registers via platform shim, spawns listen goroutines
func (m *hotkeyManager) Stop()
```

**Steps:**
- [ ] Write `hotkeys_test.go`: `defaultShortcuts` per-GOOS table; manager `Start`/`Stop` idempotence with an injected fake register function (the platform shim is behind a function var `registerFn` so tests never touch the OS).
- [ ] `cd apps/desktop && go test ./...` — fail.
- [ ] `cd apps/desktop && go get golang.design/x/hotkey@latest`; implement `hotkeys.go` + build-tagged shims; wire into `app.go` (`startup` starts it when the persisted toggle payload says enabled; bound method flips it live).
- [ ] Frontend: extend `wails.ts` typing + `App.tsx` handler + Settings toggle; test the event→UI dispatch in `apps/desktop/frontend/src/lib/wails.test.ts` (emit fake event → compose opened).
- [ ] `cd apps/desktop && go vet ./... && go test ./... && cd frontend && bun run test && bun run typecheck` — green. Manual smoke on macOS: `wails dev`, press Cmd+Shift+C with another app focused, Calendium comes forward with compose open. Commit: `feat(desktop): system-wide compose and search hotkeys via x/hotkey`.

---

### Task 10: Desktop host — menu-bar countdown, event menu, instant join

**Feasibility (honest):** Wails **v2 does not ship systray support** (removed before v2 stable; first-class tray arrives in Wails v3). The workable v2 path is `github.com/energye/systray` — the maintained getlantern fork that runs its own event loop from a goroutine and is used alongside Wails v2 on macOS/Windows/Linux. Its hard limits shape the design:
- Tray menus are **native text menu items** (title, checkmark, icon). A *graphical* mini month calendar (Fantastical-style NSPopover) is **not possible** in Wails v2 — v2 is single-window and systray menus can't host custom views. **Decision:** ship a text tray — countdown in the menu-bar title (`SetTitle`, macOS renders text beside the icon), today's remaining events as menu rows, a Join row, and "Open Calendar" which focuses the main window on the calendar view. The graphical menu-bar calendar is **deferred to the Wails v3 migration** and tracked as such in `docs/feature-map.md` (this satisfies "menu-bar next event + instant join" fully and "menu-bar mini calendar" in text form).
- `systray.Run` conflicts with Wails owning main on some platforms; `energye/systray` exposes `systray.RunWithExternalLoop` / goroutine-safe registration — use that, started from Wails `OnStartup`, stopped in `OnShutdown`.

**Files:**
- `apps/desktop/tray.go` (new — tray lifecycle, menu render, title updater ticker)
- `apps/desktop/traystate.go` (new — pure state: upcoming events, countdown formatting)
- `apps/desktop/traystate_test.go` (new)
- `apps/desktop/app.go` (modify — `SetUpcomingEvents(json string)` bound method; `OnShutdown` hook in `main.go`)
- `apps/desktop/go.mod` (add `github.com/energye/systray`)
- `apps/desktop/frontend/src/lib/tray.ts` (new — selects the next 5 upcoming events with conferencing URLs from the events query and pushes them via the bound method on every refetch + a 60s interval)
- `apps/desktop/frontend/src/lib/tray.test.ts` (new)
- `apps/desktop/frontend/src/App.tsx` (modify — mount the tray-feed hook; handle `tray-action` events: `open-calendar`, `compose`)

**Interfaces:**

```go
// traystate.go — pure, fully unit-tested
type TrayEvent struct {
    ID      string    `json:"id"`
    Title   string    `json:"title"`
    StartAt time.Time `json:"startAt"`
    JoinURL string    `json:"joinUrl"` // empty when no conferencing
}

// "Standup in 12m" / "Standup now" / "" (nothing in next 12h). Title capped at 24 runes.
func trayTitle(events []TrayEvent, now time.Time) string
func nextEvent(events []TrayEvent, now time.Time) (TrayEvent, bool)
func menuRows(events []TrayEvent, now time.Time, max int) []string // "10:30  Standup"

// tray.go
type trayManager struct{ mu sync.Mutex; events []TrayEvent; openURL func(string); emit func(action string) }
func (t *trayManager) SetEvents(evs []TrayEvent)     // re-renders menu + title
func (t *trayManager) tick(now time.Time)            // 30s ticker → SetTitle
// Menu: [countdown row disabled] [next 5 events] [Join <title>] [Compose] [Open Calendar] [Quit]
// Join → openURL(ev.JoinURL) (runtime.BrowserOpenURL); rows → emit("tray-action:…")
```

```ts
// frontend/src/lib/tray.ts
export function selectTrayEvents(events: Event[], now: Date): TrayEventPayload[]; // next 12h, sorted, ≤5, conferencing URL extracted from event.conferencing ?? location/description URL match
export function useTrayFeed(): void; // effect: on events-query data change + 60s interval → window.go.main.App.SetUpcomingEvents(JSON.stringify(...))
```

**Steps:**
- [ ] Write `traystate_test.go`: countdown strings (future/now/past/empty/cap), next-event selection skips ended events, menu rows formatting, JSON decode of the bound payload (bad JSON → error, state unchanged).
- [ ] Write `frontend/src/lib/tray.test.ts`: `selectTrayEvents` filtering/sorting/conferencing-URL extraction (uses shared `Event` fixtures).
- [ ] `cd apps/desktop && go test ./... ; cd frontend && bun run test` — fail.
- [ ] `cd apps/desktop && go get github.com/energye/systray@latest`; implement `traystate.go`, `tray.go` (start in `OnStartup` goroutine, `systray.Quit()` in a new `OnShutdown`), bound `SetUpcomingEvents`, frontend feed + `tray-action` handling. Tray icon: embed a template PNG (`build/appicon-tray.png`, monochrome, `SetTemplateIcon` on macOS for dark-menu-bar correctness).
- [ ] `cd apps/desktop && go vet ./... && go test ./... && cd frontend && bun run test && bun run typecheck` — green. Manual smoke: `wails dev`, seed an event 10 minutes out, tray shows countdown; Join opens the URL in the browser.
- [ ] Update the two menu-bar rows in `docs/feature-map.md` (status + the graphical-calendar deferral note). Commit: `feat(desktop): menu-bar next-event countdown and instant join via energye/systray`.

---

### Task 11: Desktop host — auto-join meetings

**Files:**
- `apps/desktop/scheduler.go` (new — clock-injected auto-join scheduler)
- `apps/desktop/scheduler_test.go` (new)
- `apps/desktop/app.go` (modify — `SetAutoJoin(enabled bool, leadSeconds int)` bound method; scheduler consumes the same `TrayEvent` feed from Task 10's `SetUpcomingEvents`)
- `apps/desktop/frontend/src/views/SettingsView.tsx` (modify — "Auto-join meetings" toggle + lead-time select 0/30/60s, persisted in localStorage, pushed via the bound method on startup and change)
- `apps/desktop/frontend/src/App.tsx` (modify — toast on `auto-joined` event: "Joined Standup")

**Interfaces:**

```go
// scheduler.go — pure core, testable with a fake clock
type autoJoinScheduler struct {
    mu       sync.Mutex
    enabled  bool
    lead     time.Duration
    now      func() time.Time            // injected clock
    after    func(d time.Duration) <-chan time.Time // injected timer
    openURL  func(string)
    onJoined func(ev TrayEvent)
    joined   map[string]bool // event IDs already opened — never open twice
}
func (s *autoJoinScheduler) SetEvents(evs []TrayEvent) // reschedules to the next joinable start-lead
func (s *autoJoinScheduler) SetConfig(enabled bool, lead time.Duration)
```

Behavior: at `StartAt - lead` for the next event *with a JoinURL*, call `openURL` (→ `runtime.BrowserOpenURL`) exactly once per event id, emit `auto-joined` with the event JSON, then schedule the following event. Events whose start already passed by >2min are never joined. Disabled → all timers cancelled.

**Steps:**
- [ ] Write `scheduler_test.go` (fake clock/timer channels): fires at start-lead; skips events without JoinURL; exactly-once per id across `SetEvents` re-pushes; reschedules when a nearer event arrives; disable cancels; stale events skipped.
- [ ] `cd apps/desktop && go test ./...` — fail.
- [ ] Implement `scheduler.go`; wire `SetUpcomingEvents` to feed both tray and scheduler; add `SetAutoJoin` binding; Settings toggle + toast.
- [ ] `cd apps/desktop && go vet ./... && go test ./... && cd frontend && bun run test` — green. Commit: `feat(desktop): auto-join opens the conferencing link at meeting start`.

---

### Task 12: Multi-account switching (Ctrl/Cmd+1..9)

`port.ThreadQuery.AccountID` exists and `backend/internal/adapter/out/postgres/mail.go List` already filters `t.account_id` on it — the gaps are: the HTTP handler never parses `accountId`, the shared client never sends it, and no client has switcher UI/shortcuts.

**Files:**
- `backend/internal/adapter/in/httpapi/mail.go` (modify — `q.AccountID = qs.Get("accountId")` in `handleListThreads`)
- `backend/internal/adapter/in/httpapi/mail_handlers_test.go` (modify — extend the filter-parsing table)
- `packages/shared/src/client.ts` (modify — `listThreads` params gain `accountId?: string`)
- `packages/shared/src/client.test.ts` (modify)
- `apps/web/lib/use-accounts.ts` (new — `useAccounts()` query over `listAccounts` + `useActiveAccount()` context hook)
- `apps/web/components/app/account-switcher.tsx` (new — sidebar dropdown: "All accounts" + one row per account with its index badge ⌘1..9)
- `apps/web/app/(app)/layout.tsx` (modify — `ActiveAccountProvider` (localStorage-persisted `activeAccountId: string | null`), mount switcher in the sidebar)
- `apps/web/lib/shortcuts.ts` + `shortcuts.test.ts` (modify — `mod+1..9` → switch to account n-1, `mod+0` → all accounts; ignore when a text input is focused, matching existing guard)
- `apps/web/lib/use-mail.ts` (modify — `MailListParams.accountId`; include in queryKey + `listThreads` call)
- `apps/web/components/app/command-palette.tsx` (modify — "Switch to <account email>" commands showing the shortcut, per the shortcut-teaching pattern)
- `apps/desktop/frontend/src/views/InboxView.tsx` (modify — same context + header dropdown + mod+1..9 keydown handling)
- `apps/mobile/app/(tabs)/inbox.tsx` (modify — account filter chips row when >1 account; no shortcuts on mobile)

**Interfaces:**

```ts
// packages/shared/src/client.ts — listThreads params
{ split?; view?; labelId?; q?; cursor?; limit?; accountId?: string }  // → qs.set('accountId', …)

// apps/web — context
interface ActiveAccountContextValue {
  accounts: ConnectedAccount[];
  activeAccountId: string | null;          // null = all accounts
  setActiveAccountId: (id: string | null) => void;
}
```

**Steps:**
- [ ] Backend TDD: add table rows to `mail_handlers_test.go` (`?accountId=acc1` → `q.AccountID == "acc1"`; absent → zero value). `cd backend && go test ./internal/adapter/in/httpapi/` — fail → implement one line in `mail.go` → green. `golangci-lint run`.
- [ ] Shared TDD: `client.test.ts` asserts `accountId` lands in the query string and is omitted when unset. `cd packages/shared && bun run test` — fail → implement → green.
- [ ] Web TDD: shortcuts tests (`mod+2` with 3 accounts selects the 2nd; `mod+5` with 3 accounts no-ops; `mod+0` clears; suppressed while typing); switcher component test (renders accounts, click switches, badge shows shortcut); `use-mail` test asserts `accountId` in queryKey and request. `cd apps/web && bun run test` — fail → implement provider/switcher/palette/hook wiring → green.
- [ ] Desktop + mobile: port (InboxView test for keydown switching; mobile chips render test). `cd apps/desktop/frontend && bun run test && cd ../../../apps/mobile && bun run test` — green.
- [ ] `bun run typecheck` in each touched workspace; `bunx biome check .`. Commit: `feat: multi-account inbox scoping with mod+1..9 switching across platforms`.

---

### Task 13: Named themes — curated token sets, palette switching, per-user persistence

Four themes: **Neutral** (existing tokens, default, no attribute), **Ocean**, **Forest**, **Sunset**. Themes are `data-theme` attributes on `<html>` layered *with* the existing light/dark class — every theme has a light and dark variant. Only tokens that differ from the neutral base are overridden; the cascade supplies the rest.

**Files:**
- `apps/web/app/globals.css` (modify — three `[data-theme=…]` override blocks, light + `.dark` each)
- `apps/web/components/theme-provider.tsx` + new `theme-provider.test.tsx` additions (modify — named-theme state)
- `apps/web/app/layout.tsx` (modify — pre-paint init script also reads/sets `data-theme` to prevent flash)
- `apps/web/components/app/command-palette.tsx` (modify — "Theme: Ocean" etc. commands)
- `packages/shared/src/types.ts` (modify — `UserPreferences { theme: ThemeName }`, `type ThemeName = 'neutral'|'ocean'|'forest'|'sunset'`)
- `packages/shared/src/client.ts` + `client.test.ts` (modify — `getPreferences()` / `updatePreferences(prefs)` → `GET/PUT /v1/me/preferences`)
- `backend/migrations/0005_user_preferences.sql` (new — `CREATE TABLE user_preferences (user_id text PRIMARY KEY, theme text NOT NULL DEFAULT 'neutral', updated_at timestamptz NOT NULL DEFAULT now());`)
- `backend/internal/port/driven.go` (modify — `UserPreferencesRepo { Get(ctx, userID) / Put(ctx, userID, prefs) }` + `UserPreferences` struct)
- `backend/internal/port/driving.go` (modify — methods on the user/me service)
- `backend/internal/adapter/out/postgres/preferences.go` + `preferences_test.go` (new — upsert semantics)
- `backend/internal/adapter/in/httpapi/misc.go` + `misc_handlers_test.go` (modify — `GET/PUT /v1/me/preferences`; PUT validates theme against the allowed set → 400 otherwise)
- `backend/internal/service/` (wire repo through the existing me/user service + fakes in `fakes_test.go`)
- `apps/desktop/frontend/src/styles.css` + `SettingsView.tsx` (modify — same token blocks + theme picker calling the shared client)
- `apps/mobile/lib/theme.ts` + `apps/mobile/app/(tabs)/settings.tsx` (modify — named palette maps + picker; applies stored server preference on sign-in)

**Interfaces & tokens:** (Ocean given in full as the template; Forest/Sunset override the same nine variables with the hues listed)

```css
/* globals.css — Ocean, light */
:root[data-theme='ocean'] {
  --background: 210 40% 99%;
  --primary: 217 72% 46%;
  --primary-foreground: 210 40% 98%;
  --secondary: 214 45% 94%;
  --accent: 214 45% 93%;
  --accent-foreground: 217 60% 25%;
  --muted: 214 40% 94%;
  --border: 214 32% 88%;
  --ring: 217 72% 55%;
}
/* Ocean, dark */
:root[data-theme='ocean'].dark {
  --background: 218 35% 8%;
  --primary: 213 80% 66%;
  --primary-foreground: 218 35% 10%;
  --secondary: 216 28% 16%;
  --accent: 216 28% 18%;
  --accent-foreground: 213 70% 85%;
  --muted: 216 28% 15%;
  --border: 216 26% 18%;
  --ring: 213 80% 60%;
}
/* Forest: identical structure, hue family 150–160, light primary 158 55% 34%,
   dark primary 152 45% 60%. Sunset: hue family 20–30, light primary 24 82% 48%,
   dark primary 27 90% 62%. Same nine variables, same saturation/lightness
   relationships as the Ocean block. */
```

```ts
// theme-provider.tsx additions
export type ThemeName = 'neutral' | 'ocean' | 'forest' | 'sunset';
const NAMED_THEME_STORAGE_KEY = 'calendium-named-theme';
interface ThemeContextValue {
  theme: Theme; resolvedTheme: ResolvedTheme; setTheme(t: Theme): void;
  namedTheme: ThemeName;
  /** Applies data-theme, persists to localStorage immediately, then
   *  fire-and-forget PUT /v1/me/preferences (errors toast outside demo mode). */
  setNamedTheme(name: ThemeName): void;
}
// apply(): neutral → delete html.dataset.theme; else set it.
// On sign-in/app mount: getPreferences() once; server value wins over localStorage
// when they differ (localStorage is the offline fallback, synced back on change).
```

**Steps:**
- [ ] Backend TDD: `preferences_test.go` (Get missing → defaults `neutral`; Put upserts; second Put overwrites), `misc_handlers_test.go` (GET returns prefs; PUT valid theme 200 + persisted; PUT `theme=bogus` → 400; unauthenticated → 401). `cd backend && go test ./...` — fail → migration + repo + service + handlers → green; `golangci-lint run`.
- [ ] Shared TDD: `client.test.ts` for the two new methods. `cd packages/shared && bun run test` — green after implementing.
- [ ] Web TDD: `theme-provider.test.tsx` — `setNamedTheme('ocean')` sets `data-theme` and localStorage; `neutral` removes the attribute; server preference fetched on mount wins; PUT failure outside demo toasts and keeps the local theme (no fake success). Palette test: four "Theme:" commands, executing one switches. `cd apps/web && bun run test` — fail → implement CSS blocks + provider + pre-paint script + palette commands → green.
- [ ] Desktop: copy the three token blocks into `styles.css` (same selectors), picker in SettingsView + test. Mobile: palette maps in `lib/theme.ts` + settings picker + jest test. Run both suites.
- [ ] Update `docs/feature-map.md` Themes row status. `bunx biome check .`; typechecks. Commit: `feat: four named themes with palette switching and per-user persistence`.

---

## Final phase gate

- [ ] All suites green from the repo root: `bun run test` in `packages/shared`, `apps/web`, `apps/desktop/frontend`, `apps/mobile`; `cd backend && go test ./... && golangci-lint run`; `cd apps/desktop && go vet ./... && go test ./...`; `cd apps/web && bun run e2e`.
- [ ] `bunx biome check .` clean; lefthook pre-push passes.
- [ ] Manual desktop smoke on macOS (`cd apps/desktop && wails dev`): global hotkey from another app, tray countdown + Join, auto-join fires, mod+2 account switch, Ocean theme applies.
- [ ] Web offline smoke: DevTools → Network offline → archive, snooze, compose+send → reload the tab → queue intact → go online → actions land, conflicts (if any) toast.
- [ ] `docs/feature-map.md` statuses updated for every M2.6 row (including the explicit Wails-v3 deferral note on the graphical menu-bar calendar).
