import 'fake-indexeddb/auto';

import type { QueryClient } from '@tanstack/react-query';
import { IDBFactory } from 'fake-indexeddb';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  actOnThread: vi.fn(),
  markThreadOpened: vi.fn(),
  snoozeThread: vi.fn(),
  setThreadReminder: vi.fn(),
  saveDraft: vi.fn(),
  updateDraft: vi.fn(),
  sendDraft: vi.fn(),
}));
vi.mock('@/lib/api', () => ({ getApiClient: () => apiMock }));

// Acting identity in effect (lib/act-as). Mutable per test: null = self.
const actAs = vi.hoisted(() => ({ current: null as string | null }));
vi.mock('@/lib/act-as', () => ({ getActingAs: () => actAs.current }));

const toastMock = vi.hoisted(() =>
  Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn(), info: vi.fn() })
);
vi.mock('sonner', () => ({ toast: toastMock }));

type QueueModule = typeof import('@/lib/offline/queue');
type SharedModule = typeof import('@calendium/shared');

let queue: QueueModule;
// Imported AFTER vi.resetModules() so `instanceof ApiRequestError` inside the
// freshly-loaded queue/outbox modules matches the class these tests construct.
let ApiRequestError: SharedModule['ApiRequestError'];
let cleanups: Array<() => void>;

function stubQueryClient() {
  const invalidateQueries = vi.fn();
  return {
    client: { invalidateQueries } as unknown as QueryClient,
    invalidateQueries,
  };
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

beforeEach(async () => {
  vi.clearAllMocks();
  actAs.current = null;
  cleanups = [];
  // Fresh IndexedDB universe + fresh module state (outbox singleton AND the
  // connectivity tracker queue.ts subscribes to) per test.
  globalThis.indexedDB = new IDBFactory();
  vi.resetModules();
  queue = await import('@/lib/offline/queue');
  ({ ApiRequestError } = await import('@calendium/shared'));
});

afterEach(() => {
  for (const cleanup of cleanups) cleanup();
  vi.useRealTimers();
});

describe('isNetworkError', () => {
  it('classifies transport failures (fetch TypeError, AbortError) as network errors', () => {
    expect(queue.isNetworkError(new TypeError('fetch failed'))).toBe(true);
    expect(
      queue.isNetworkError(new DOMException('The operation was aborted.', 'AbortError'))
    ).toBe(true);
  });

  it('does not classify real server responses or other values as network errors', () => {
    expect(queue.isNetworkError(new ApiRequestError(500, 'internal', 'boom'))).toBe(false);
    expect(queue.isNetworkError(new ApiRequestError(409, 'conflict', 'nope'))).toBe(false);
    expect(queue.isNetworkError(new Error('something else'))).toBe(false);
    expect(queue.isNetworkError('not an error')).toBe(false);
  });
});

describe('queueAction', () => {
  it('enqueues into the durable outbox and toasts', async () => {
    await queue.queueAction({ kind: 'thread_action', threadId: 't1', action: 'archive' });
    expect(queue.getOutbox().queuedCount).toBe(1);
    expect(toastMock).toHaveBeenCalledWith('Offline — action queued');
  });

  it('silent mode enqueues without toasting (read receipts, compose internals)', async () => {
    await queue.queueAction({ kind: 'thread_open', threadId: 't1' }, { silent: true });
    expect(queue.getOutbox().queuedCount).toBe(1);
    expect(toastMock).not.toHaveBeenCalled();
  });

  it('skips the "queued" toast when the action coalesced away (nothing was queued)', async () => {
    await queue.queueAction({ kind: 'thread_action', threadId: 't1', action: 'star' });
    toastMock.mockClear();
    // The opposite action cancels BOTH entries — enqueue returns null.
    await queue.queueAction({ kind: 'thread_action', threadId: 't1', action: 'unstar' });
    expect(queue.getOutbox().queuedCount).toBe(0);
    expect(toastMock).not.toHaveBeenCalled();
  });
});

describe('newLocalDraftId', () => {
  it('uses the shared local-draft prefix and is unique per call', () => {
    const a = queue.newLocalDraftId();
    const b = queue.newLocalDraftId();
    expect(a).toMatch(/^local-/);
    expect(b).toMatch(/^local-/);
    expect(a).not.toBe(b);
  });
});

describe('startOutboxReplay', () => {
  it('replays on the offline→online transition and invalidates thread/draft queries', async () => {
    window.dispatchEvent(new Event('offline'));
    await queue.queueAction({ kind: 'thread_action', threadId: 't1', action: 'archive' });
    apiMock.actOnThread.mockResolvedValue({ id: 't1' });

    const { client, invalidateQueries } = stubQueryClient();
    cleanups.push(queue.startOutboxReplay(client));
    await sleep(20);
    expect(apiMock.actOnThread).not.toHaveBeenCalled(); // still offline — no replay yet

    window.dispatchEvent(new Event('online'));
    await vi.waitFor(() => {
      expect(apiMock.actOnThread).toHaveBeenCalledWith('t1', 'archive');
      expect(queue.getOutbox().queuedCount).toBe(0);
    });
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ['threads'] });
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ['thread'] });
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ['drafts'] });
  });

  it('toasts each conflict — except thread_open, which is dismissed silently', async () => {
    window.dispatchEvent(new Event('offline'));
    await queue.queueAction({ kind: 'thread_action', threadId: 't1', action: 'archive' });
    await queue.queueAction({ kind: 'thread_open', threadId: 't2' }, { silent: true });
    apiMock.actOnThread.mockRejectedValue(new ApiRequestError(409, 'conflict', 'thread changed'));
    apiMock.markThreadOpened.mockRejectedValue(new ApiRequestError(410, 'gone', 'thread deleted'));

    const { client } = stubQueryClient();
    cleanups.push(queue.startOutboxReplay(client));
    window.dispatchEvent(new Event('online'));

    await vi.waitFor(() => {
      expect(toastMock.error).toHaveBeenCalledTimes(1);
      // Only the thread_action conflict remains surfaced; the thread_open
      // conflict was auto-dismissed without a toast.
      const pending = [...queue.getOutbox().pending];
      expect(pending).toHaveLength(1);
      expect(pending[0]!.status).toBe('conflict');
      expect(pending[0]!.action.kind).toBe('thread_action');
    });
    expect(toastMock.error).toHaveBeenCalledWith(expect.stringContaining('archive'));
  });

  it('a network-interrupted replay stays queued (honest) and retries on the 30s interval', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
    apiMock.actOnThread
      .mockRejectedValueOnce(new TypeError('fetch failed'))
      .mockResolvedValue({ id: 't1' });
    await queue.queueAction({ kind: 'thread_action', threadId: 't1', action: 'archive' });

    const { client } = stubQueryClient();
    cleanups.push(queue.startOutboxReplay(client)); // already online — initial run hits the network error
    await sleep(20);
    expect(queue.getOutbox().queuedCount).toBe(1); // never claimed synced

    await vi.advanceTimersByTimeAsync(30_000);
    await sleep(20);
    expect(queue.getOutbox().queuedCount).toBe(0);
  });

  it('invalidates queries when an entry exhausts retries and lands as failed', async () => {
    // Seed storage with an entry one attempt away from MAX_ATTEMPTS so a
    // single 5xx replay marks it failed — its optimistic patch is now stale
    // and must be invalidated back to server truth.
    const { createIndexedDbKv, OUTBOX_STORAGE_KEY } = await import('@calendium/shared');
    const kv = createIndexedDbKv();
    await kv.setItem(
      OUTBOX_STORAGE_KEY,
      JSON.stringify([
        {
          id: 'e1',
          seq: 1,
          createdAt: new Date().toISOString(),
          attempts: 4,
          status: 'queued',
          lastError: null,
          action: { kind: 'thread_action', threadId: 't1', action: 'archive' },
        },
      ])
    );
    apiMock.actOnThread.mockRejectedValue(new ApiRequestError(500, 'internal', 'boom'));

    const { client, invalidateQueries } = stubQueryClient();
    cleanups.push(queue.startOutboxReplay(client));
    await vi.waitFor(() => {
      const pending = [...queue.getOutbox().pending];
      expect(pending[0]?.status).toBe('failed');
    });
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ['threads'] });
  });

  it('cleanup stops the online/focus triggers', async () => {
    window.dispatchEvent(new Event('offline'));
    await queue.queueAction({ kind: 'thread_action', threadId: 't1', action: 'archive' });
    apiMock.actOnThread.mockResolvedValue({ id: 't1' });

    const { client } = stubQueryClient();
    const stop = queue.startOutboxReplay(client);
    stop();

    window.dispatchEvent(new Event('online'));
    window.dispatchEvent(new Event('focus'));
    await sleep(30);
    expect(apiMock.actOnThread).not.toHaveBeenCalled();
  });
});

describe('acting identity isolation', () => {
  it('an entry queued as SELF stays parked while acting, then replays after acting stops', async () => {
    await queue.queueAction(
      { kind: 'thread_action', threadId: 't1', action: 'archive' },
      { silent: true }
    );
    apiMock.actOnThread.mockResolvedValue({ id: 't1' });
    actAs.current = 'principal-1'; // start acting BEFORE anything replays

    const { client } = stubQueryClient();
    cleanups.push(queue.startOutboxReplay(client));
    window.dispatchEvent(new Event('focus'));
    await sleep(30);
    // Never sent through the acting client — that would mutate the PRINCIPAL.
    expect(apiMock.actOnThread).not.toHaveBeenCalled();
    expect(queue.getOutbox().queuedCount).toBe(1);

    actAs.current = null; // back to self — the matching identity replays it
    window.dispatchEvent(new Event('focus'));
    await vi.waitFor(() => {
      expect(apiMock.actOnThread).toHaveBeenCalledWith('t1', 'archive');
      expect(queue.getOutbox().queuedCount).toBe(0);
    });
  });

  it('an entry queued WHILE ACTING never replays as self, only when acting again', async () => {
    actAs.current = 'principal-1';
    await queue.queueAction(
      { kind: 'thread_action', threadId: 't1', action: 'archive' },
      { silent: true }
    );
    apiMock.actOnThread.mockResolvedValue({ id: 't1' });
    actAs.current = null; // stop acting before the replay triggers fire

    const { client } = stubQueryClient();
    cleanups.push(queue.startOutboxReplay(client));
    window.dispatchEvent(new Event('focus'));
    await sleep(30);
    // Never sent as self — that would mutate the ASSISTANT's own account.
    expect(apiMock.actOnThread).not.toHaveBeenCalled();
    expect(queue.getOutbox().queuedCount).toBe(1);

    actAs.current = 'principal-1'; // acting again — matching identity replays
    window.dispatchEvent(new Event('focus'));
    await vi.waitFor(() => {
      expect(apiMock.actOnThread).toHaveBeenCalledWith('t1', 'archive');
      expect(queue.getOutbox().queuedCount).toBe(0);
    });
  });

  it('a legacy untagged entry (pre-actingAs persistence) is treated as self', async () => {
    const { createIndexedDbKv, OUTBOX_STORAGE_KEY } = await import('@calendium/shared');
    const kv = createIndexedDbKv();
    await kv.setItem(
      OUTBOX_STORAGE_KEY,
      JSON.stringify([
        {
          id: 'legacy-1',
          seq: 1,
          createdAt: new Date().toISOString(),
          attempts: 0,
          status: 'queued',
          lastError: null,
          // No actingAs field at all — written before the tag existed.
          action: { kind: 'thread_action', threadId: 't1', action: 'archive' },
        },
      ])
    );
    apiMock.actOnThread.mockResolvedValue({ id: 't1' });
    actAs.current = 'principal-1';

    const { client } = stubQueryClient();
    cleanups.push(queue.startOutboxReplay(client));
    window.dispatchEvent(new Event('focus'));
    await sleep(30);
    expect(apiMock.actOnThread).not.toHaveBeenCalled(); // parked while acting

    actAs.current = null;
    window.dispatchEvent(new Event('focus'));
    await vi.waitFor(() => {
      expect(apiMock.actOnThread).toHaveBeenCalledWith('t1', 'archive');
      expect(queue.getOutbox().queuedCount).toBe(0);
    });
  });
});

describe('clearOfflineState', () => {
  it('sign-out wipes the outbox + query-cache keys, active account, and the in-memory queue', async () => {
    const { createIndexedDbKv, OUTBOX_STORAGE_KEY } = await import('@calendium/shared');
    const kv = createIndexedDbKv();
    await kv.setItem(queue.QUERY_CACHE_STORAGE_KEY, '{"cached":"prior-user-mail"}');
    window.localStorage.setItem('calendium.activeAccountId', 'acc1');
    await queue.queueAction(
      { kind: 'thread_action', threadId: 't1', action: 'archive' },
      { silent: true }
    );
    expect(queue.getOutbox().queuedCount).toBe(1);

    await queue.clearOfflineState();

    expect(queue.getOutbox().queuedCount).toBe(0);
    expect(await kv.getItem(OUTBOX_STORAGE_KEY)).toBeNull();
    expect(await kv.getItem(queue.QUERY_CACHE_STORAGE_KEY)).toBeNull();
    expect(window.localStorage.getItem('calendium.activeAccountId')).toBeNull();
  });

  it('a replay trigger after sign-out sends nothing', async () => {
    await queue.queueAction(
      { kind: 'thread_action', threadId: 't1', action: 'archive' },
      { silent: true }
    );
    await queue.clearOfflineState();

    const { client } = stubQueryClient();
    cleanups.push(queue.startOutboxReplay(client));
    window.dispatchEvent(new Event('online'));
    window.dispatchEvent(new Event('focus'));
    await sleep(30);
    expect(apiMock.actOnThread).not.toHaveBeenCalled();
  });
});
