// Offline support for the Wails frontend (M2.6 Task 5): connectivity
// classification, the durable outbox singleton, and replay-on-reconnect.
// Uses fake-indexeddb so the real createIndexedDbKv path is exercised.
import 'fake-indexeddb/auto';
import { IDBFactory } from 'fake-indexeddb';
import { Outbox, createIndexedDbKv, createKvOutboxStorage, isLocalDraftId } from '@calendium/shared';
import { renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

// ---------------------------------------------------------------------------
// Mocks: offline.ts talks to the server via lib/api's `api` singleton, gates
// on apiConfigured()/isDemoMode(), and surfaces conflicts via lib/toast.
// ---------------------------------------------------------------------------

const mocks = vi.hoisted(() => ({
  configured: true,
  demo: false,
  actOnThread: vi.fn(),
  snoozeThread: vi.fn(),
  setThreadReminder: vi.fn(),
  markThreadOpened: vi.fn(),
  saveDraft: vi.fn(),
  updateDraft: vi.fn(),
  sendDraft: vi.fn(),
  toast: vi.fn(),
}));

vi.mock('./api', () => ({
  api: {
    actOnThread: mocks.actOnThread,
    snoozeThread: mocks.snoozeThread,
    setThreadReminder: mocks.setThreadReminder,
    markThreadOpened: mocks.markThreadOpened,
    saveDraft: mocks.saveDraft,
    updateDraft: mocks.updateDraft,
    sendDraft: mocks.sendDraft,
  },
  apiConfigured: () => mocks.configured,
}));
vi.mock('./server-config', () => ({
  isDemoMode: () => mocks.demo,
}));
vi.mock('./toast', () => ({
  toast: mocks.toast,
  errorMessage: (e: unknown) => (e instanceof Error ? e.message : 'error'),
}));

/**
 * Fresh module state (singleton outbox) per test. Also returns the SAME
 * registry's ApiRequestError class — after resetModules, offline.ts loads a
 * fresh @calendium/shared copy, so instanceof checks only hold for errors
 * constructed from that copy (not this file's top-level import).
 */
async function loadOffline() {
  vi.resetModules();
  const offline = await import('./offline');
  const { ApiRequestError: FreshApiRequestError } = await import('@calendium/shared');
  return { ...offline, FreshApiRequestError };
}

function stubQueryClient() {
  return { invalidateQueries: vi.fn().mockResolvedValue(undefined) };
}

beforeEach(() => {
  vi.clearAllMocks();
  mocks.configured = true;
  mocks.demo = false;
  // Fresh IndexedDB per test so outbox state never leaks between tests.
  globalThis.indexedDB = new IDBFactory();
});

describe('isNetworkError', () => {
  it('classifies fetch-level failures as network errors', async () => {
    const offline = await loadOffline();
    expect(offline.isNetworkError(new TypeError('Failed to fetch'))).toBe(true);
    expect(offline.isNetworkError(new Error('NetworkError when attempting to fetch resource'))).toBe(true);
  });

  it('never classifies server responses or app errors as network errors', async () => {
    const offline = await loadOffline();
    expect(offline.isNetworkError(new offline.FreshApiRequestError(500, 'internal', 'boom'))).toBe(false);
    expect(offline.isNetworkError(new offline.FreshApiRequestError(409, 'conflict', 'taken'))).toBe(false);
    expect(offline.isNetworkError(new Error('validation failed'))).toBe(false);
    expect(offline.isNetworkError('nope')).toBe(false);
  });
});

describe('newLocalDraftId', () => {
  it('mints unique ids the shared outbox recognizes as local', async () => {
    const offline = await loadOffline();
    const a = offline.newLocalDraftId();
    const b = offline.newLocalDraftId();
    expect(isLocalDraftId(a)).toBe(true);
    expect(a).not.toBe(b);
  });
});

describe('getOutbox', () => {
  it('returns one shared initialized singleton', async () => {
    const offline = await loadOffline();
    const [a, b] = await Promise.all([offline.getOutbox(), offline.getOutbox()]);
    expect(a).toBe(b);
    await a.enqueue({ kind: 'thread_action', threadId: 't1', action: 'archive' });
    expect((await offline.getOutbox()).queuedCount).toBe(1);
  });
});

describe('queueOffline + useQueuedCount', () => {
  it('durably queues and reflects the real queued count in the hook', async () => {
    const offline = await loadOffline();
    const { result } = renderHook(() => offline.useQueuedCount());
    expect(result.current).toBe(0);
    expect(await offline.queueOffline({ kind: 'thread_action', threadId: 't1', action: 'archive' })).toBe(true);
    await waitFor(() => expect(result.current).toBe(1));
  });

  it('returns false when persistence is unavailable (degrade path, no fake success)', async () => {
    const offline = await loadOffline();
    // Simulate a broken storage layer: every open attempt explodes.
    globalThis.indexedDB = {
      open() {
        throw new Error('quota exceeded');
      },
    } as unknown as IDBFactory;
    expect(await offline.queueOffline({ kind: 'thread_action', threadId: 't1', action: 'archive' })).toBe(false);
  });
});

describe('startOutboxReplay', () => {
  it('replays queued work on start and again on the online event, then invalidates queries', async () => {
    const offline = await loadOffline();
    mocks.actOnThread.mockResolvedValue({});
    const outbox = await offline.getOutbox();
    await outbox.enqueue({ kind: 'thread_action', threadId: 't1', action: 'archive' });

    const queryClient = stubQueryClient();
    const stop = offline.startOutboxReplay(queryClient as never);
    await waitFor(() => expect(mocks.actOnThread).toHaveBeenCalledWith('t1', 'archive'));
    await waitFor(() => expect(queryClient.invalidateQueries).toHaveBeenCalled());
    expect(outbox.queuedCount).toBe(0);

    // Back offline → user queues more → connectivity returns.
    await outbox.enqueue({ kind: 'thread_snooze', threadId: 't2', until: '2026-07-20T00:00:00Z' });
    mocks.snoozeThread.mockResolvedValue({});
    window.dispatchEvent(new Event('online'));
    await waitFor(() => expect(mocks.snoozeThread).toHaveBeenCalledWith('t2', '2026-07-20T00:00:00Z'));
    await waitFor(() => expect(outbox.queuedCount).toBe(0));
    stop();
  });

  it('keeps entries queued (no invalidation, no fake sync) when replay hits a network error', async () => {
    const offline = await loadOffline();
    mocks.actOnThread.mockRejectedValue(new TypeError('Failed to fetch'));
    const outbox = await offline.getOutbox();
    await outbox.enqueue({ kind: 'thread_action', threadId: 't1', action: 'archive' });

    const queryClient = stubQueryClient();
    const stop = offline.startOutboxReplay(queryClient as never);
    await waitFor(() => expect(mocks.actOnThread).toHaveBeenCalled());
    // Truthful sync state: the action is still pending, nothing pretends success.
    expect(outbox.queuedCount).toBe(1);
    expect(queryClient.invalidateQueries).not.toHaveBeenCalled();
    stop();
  });

  it('surfaces a conflict (4xx) honestly: toast + entry cleared + server truth refetched', async () => {
    const offline = await loadOffline();
    mocks.actOnThread.mockRejectedValue(new offline.FreshApiRequestError(409, 'conflict', 'thread changed'));
    const outbox = await offline.getOutbox();
    await outbox.enqueue({ kind: 'thread_action', threadId: 't1', action: 'archive' });

    const queryClient = stubQueryClient();
    const stop = offline.startOutboxReplay(queryClient as never);
    await waitFor(() => expect(mocks.toast).toHaveBeenCalledWith(expect.objectContaining({ variant: 'destructive' })));
    await waitFor(() => expect(queryClient.invalidateQueries).toHaveBeenCalled());
    // Rolled back truthfully: the conflict is surfaced and no longer pending.
    await waitFor(() => expect(outbox.pending.length).toBe(0));
    stop();
  });

  it('never replays in demo mode or without a configured server', async () => {
    const offline = await loadOffline();
    const outbox = await offline.getOutbox();
    await outbox.enqueue({ kind: 'thread_action', threadId: 't1', action: 'archive' });
    mocks.demo = true;
    const queryClient = stubQueryClient();
    const stop = offline.startOutboxReplay(queryClient as never);
    window.dispatchEvent(new Event('online'));
    await new Promise((r) => setTimeout(r, 20));
    expect(mocks.actOnThread).not.toHaveBeenCalled();
    stop();
  });
});

describe('clearOfflineState', () => {
  it('sign-out wipes the outbox + query-cache keys, active account, and the in-memory queue', async () => {
    const offline = await loadOffline();
    const kv = createIndexedDbKv();
    await kv.setItem(offline.QUERY_CACHE_STORAGE_KEY, '{"cached":"prior-user-mail"}');
    window.localStorage.setItem('calendium.activeAccountId', 'acc1');
    const outbox = await offline.getOutbox();
    await outbox.enqueue({ kind: 'thread_action', threadId: 't1', action: 'archive' });
    expect(outbox.queuedCount).toBe(1);

    await offline.clearOfflineState();

    expect(outbox.queuedCount).toBe(0);
    expect(await kv.getItem('outbox:v1')).toBeNull();
    expect(await kv.getItem(offline.QUERY_CACHE_STORAGE_KEY)).toBeNull();
    expect(window.localStorage.getItem('calendium.activeAccountId')).toBeNull();
  });

  it('a replay trigger after sign-out sends nothing', async () => {
    const offline = await loadOffline();
    const outbox = await offline.getOutbox();
    await outbox.enqueue({ kind: 'thread_action', threadId: 't1', action: 'archive' });
    await offline.clearOfflineState();

    const queryClient = stubQueryClient();
    const stop = offline.startOutboxReplay(queryClient as never);
    window.dispatchEvent(new Event('online'));
    await new Promise((r) => setTimeout(r, 30));
    expect(mocks.actOnThread).not.toHaveBeenCalled();
    expect(queryClient.invalidateQueries).not.toHaveBeenCalled();
    stop();
  });

  it('never throws when storage is unavailable (sign-out must still complete)', async () => {
    const offline = await loadOffline();
    globalThis.indexedDB = {
      open() {
        throw new Error('storage gone');
      },
    } as unknown as IDBFactory;
    await expect(offline.clearOfflineState()).resolves.toBeUndefined();
  });
});

describe('persistence across reloads', () => {
  it('queued triage survives a simulated reload (new Outbox over the same storage)', async () => {
    const offline = await loadOffline();
    const outbox = await offline.getOutbox();
    await outbox.enqueue({ kind: 'thread_action', threadId: 't-reload', action: 'archive' });

    // "Reload": a brand-new Outbox over the same IndexedDB-backed storage.
    const reloaded = new Outbox(createKvOutboxStorage(createIndexedDbKv()));
    await reloaded.init();
    expect(reloaded.queuedCount).toBe(1);
    expect(reloaded.pending[0]?.action).toEqual({
      kind: 'thread_action',
      threadId: 't-reload',
      action: 'archive',
    });
  });
});
