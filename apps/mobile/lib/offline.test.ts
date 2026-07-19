// --- Mocks -------------------------------------------------------------
// offline.ts fans out to AsyncStorage (durable outbox + persister storage),
// expo-network (reachability replay trigger), RN AppState (foreground replay
// trigger), and the shared ApiClient (replay executor). Each is mocked so the
// module's own queue/replay logic is what's under test.
jest.mock('@react-native-async-storage/async-storage', () =>
  require('@react-native-async-storage/async-storage/jest/async-storage-mock')
);

type NetworkState = { isConnected: boolean; isInternetReachable: boolean | null };
const networkListeners: Array<(state: NetworkState) => void> = [];
let mockNetworkState: NetworkState = { isConnected: false, isInternetReachable: false };
jest.mock('expo-network', () => ({
  addNetworkStateListener: (fn: (state: NetworkState) => void) => {
    networkListeners.push(fn);
    return {
      remove: () => {
        const i = networkListeners.indexOf(fn);
        if (i >= 0) networkListeners.splice(i, 1);
      },
    };
  },
  getNetworkStateAsync: () => Promise.resolve(mockNetworkState),
}));

const mockActOnThread = jest.fn();
const mockSnoozeThread = jest.fn();
jest.mock('@/lib/api', () => ({
  api: {
    actOnThread: (...args: unknown[]) => mockActOnThread(...args),
    snoozeThread: (...args: unknown[]) => mockSnoozeThread(...args),
  },
}));

import AsyncStorage from '@react-native-async-storage/async-storage';
import { ApiRequestError } from '@calendium/shared';
import { QueryClient } from '@tanstack/react-query';
import { AppState } from 'react-native';
import {
  generateOutboxId,
  getOutbox,
  isNetworkError,
  outboxReady,
  queueIfOffline,
  resetOutboxForTests,
  startOutboxReplay,
} from './offline';
import { PERSISTED_PREFIXES, persister, persistOptions, queryClient } from './query-client';

const SEVEN_DAYS_MS = 7 * 24 * 60 * 60 * 1000;
const OUTBOX_KEY = 'outbox:v1';

// RN's fetch rejects with exactly this on connectivity loss.
const networkFailure = () => new TypeError('Network request failed');

// Replay hops across several awaits (storage load, api call, persist); a real
// macrotask tick is the reliable flush in this jest-expo setup (waitFor is
// fragile here — see hooks/use-push-registration.test.ts).
const flush = () => new Promise((resolve) => setTimeout(resolve, 20));

beforeEach(async () => {
  jest.clearAllMocks();
  networkListeners.length = 0;
  mockNetworkState = { isConnected: false, isInternetReachable: false };
  await AsyncStorage.clear();
  resetOutboxForTests();
});

// --- Pure/synchronous checks first; heavy async interaction tests are LAST
// in this file on purpose (jest-expo ordering fragility — M2.5 lesson).

describe('generateOutboxId', () => {
  it('produces unique non-empty ids', () => {
    const ids = new Set(Array.from({ length: 200 }, () => generateOutboxId()));
    expect(ids.size).toBe(200);
    for (const id of ids) expect(id.length).toBeGreaterThan(8);
  });

  it('still produces unique ids when crypto.randomUUID is unavailable (Hermes)', () => {
    const desc = Object.getOwnPropertyDescriptor(globalThis, 'crypto');
    Object.defineProperty(globalThis, 'crypto', { value: undefined, configurable: true });
    try {
      const ids = new Set(Array.from({ length: 100 }, () => generateOutboxId()));
      expect(ids.size).toBe(100);
    } finally {
      if (desc) Object.defineProperty(globalThis, 'crypto', desc);
    }
  });
});

describe('isNetworkError', () => {
  it('is false for API-level errors (the server answered)', () => {
    expect(isNetworkError(new ApiRequestError(500, 'internal', 'boom'))).toBe(false);
    expect(isNetworkError(new ApiRequestError(402, 'payment_required', 'paywall'))).toBe(false);
  });

  it('is true for fetch connectivity failures', () => {
    expect(isNetworkError(networkFailure())).toBe(true);
    expect(isNetworkError(new Error('Network request failed'))).toBe(true);
    expect(isNetworkError(new Error('request timeout'))).toBe(true);
  });

  it('is false for ordinary programming errors and non-errors', () => {
    expect(isNetworkError(new Error('Thread not loaded yet'))).toBe(false);
    expect(isNetworkError('nope')).toBe(false);
    expect(isNetworkError(undefined)).toBe(false);
  });
});

describe('query persistence config', () => {
  it('exports an AsyncStorage-backed persister', () => {
    expect(persister).toBeDefined();
    expect(typeof persister.persistClient).toBe('function');
  });

  it('uses the shared buster and a 7-day maxAge, with a matching gcTime default', () => {
    expect(persistOptions.buster).toBe('calendium-cache-v1');
    expect(persistOptions.maxAge).toBe(SEVEN_DAYS_MS);
    expect(queryClient.getDefaultOptions().queries?.gcTime).toBe(SEVEN_DAYS_MS);
  });

  it('persists only allowlisted prefixes — api-online and AI suggestions stay out', () => {
    expect(PERSISTED_PREFIXES).toContain('threads');
    expect(PERSISTED_PREFIXES).toContain('thread');
    expect(PERSISTED_PREFIXES).not.toContain('api-online');
    expect(PERSISTED_PREFIXES).not.toContain('instant-replies');

    const should = persistOptions.dehydrateOptions?.shouldDehydrateQuery;
    expect(should).toBeDefined();
    const fakeQuery = (key: unknown[], status: string) =>
      ({ queryKey: key, state: { status } }) as never;
    expect(should?.(fakeQuery(['threads', 'important'], 'success'))).toBe(true);
    expect(should?.(fakeQuery(['api-online'], 'success'))).toBe(false);
    expect(should?.(fakeQuery(['instant-replies', 'thr_1'], 'success'))).toBe(false);
    // Never persist an in-flight/error state as if it were data.
    expect(should?.(fakeQuery(['threads', 'important'], 'pending'))).toBe(false);
  });
});

describe('getOutbox', () => {
  it('returns a stable singleton', () => {
    expect(getOutbox()).toBe(getOutbox());
  });

  it('durably persists enqueued actions to AsyncStorage under outbox:v1', async () => {
    await outboxReady();
    await getOutbox().enqueue({ kind: 'thread_action', threadId: 'thr_1', action: 'archive' });

    const raw = await AsyncStorage.getItem(OUTBOX_KEY);
    expect(raw).toBeTruthy();
    const entries = JSON.parse(raw as string) as Array<{ status: string; action: unknown }>;
    expect(entries).toHaveLength(1);
    expect(entries[0].status).toBe('queued');
    expect(entries[0].action).toEqual({ kind: 'thread_action', threadId: 'thr_1', action: 'archive' });
  });
});

describe('queueIfOffline (enqueue-on-network-error)', () => {
  it('queues the action and reports true for a network failure', async () => {
    const queued = await queueIfOffline(networkFailure(), {
      kind: 'thread_snooze',
      threadId: 'thr_2',
      until: '2026-07-20T09:00:00.000Z',
    });
    expect(queued).toBe(true);
    expect(getOutbox().queuedCount).toBe(1);
  });

  it('does NOT queue when the server actually rejected the action', async () => {
    const queued = await queueIfOffline(new ApiRequestError(402, 'payment_required', 'paywall'), {
      kind: 'thread_action',
      threadId: 'thr_2',
      action: 'archive',
    });
    expect(queued).toBe(false);
    expect(getOutbox().queuedCount).toBe(0);
  });
});

// --- Heavy async interaction tests: keep these LAST (see note above). ------

describe('startOutboxReplay', () => {
  it('replays queued actions when reachability returns, then refreshes mail queries', async () => {
    mockActOnThread.mockResolvedValue({});
    const client = new QueryClient();
    const invalidateSpy = jest.spyOn(client, 'invalidateQueries').mockResolvedValue();

    const stop = startOutboxReplay(client);
    await flush(); // initial kick sees the offline mockNetworkState — no replay
    expect(networkListeners).toHaveLength(1);

    await queueIfOffline(networkFailure(), {
      kind: 'thread_action',
      threadId: 'thr_9',
      action: 'archive',
    });
    expect(mockActOnThread).not.toHaveBeenCalled();

    networkListeners[0]({ isConnected: true, isInternetReachable: true });
    await flush();

    expect(mockActOnThread).toHaveBeenCalledWith('thr_9', 'archive');
    expect(getOutbox().queuedCount).toBe(0);
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['threads'] });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['thread'] });
    stop();
  });

  it('does not replay while the network state is unreachable', async () => {
    mockActOnThread.mockResolvedValue({});
    const client = new QueryClient();
    const stop = startOutboxReplay(client);
    await flush();

    await queueIfOffline(networkFailure(), {
      kind: 'thread_action',
      threadId: 'thr_10',
      action: 'archive',
    });
    networkListeners[0]({ isConnected: false, isInternetReachable: false });
    await flush();

    expect(mockActOnThread).not.toHaveBeenCalled();
    expect(getOutbox().queuedCount).toBe(1); // still honestly pending
    stop();
  });

  it('keeps entries queued (at-least-once) when replay itself hits a network failure', async () => {
    mockActOnThread.mockRejectedValue(networkFailure());
    const client = new QueryClient();
    const invalidateSpy = jest.spyOn(client, 'invalidateQueries').mockResolvedValue();

    const stop = startOutboxReplay(client);
    await flush();
    await queueIfOffline(networkFailure(), {
      kind: 'thread_action',
      threadId: 'thr_11',
      action: 'archive',
    });

    networkListeners[0]({ isConnected: true, isInternetReachable: true });
    await flush();

    expect(mockActOnThread).toHaveBeenCalled();
    expect(getOutbox().queuedCount).toBe(1); // nothing fabricated as synced
    expect(invalidateSpy).not.toHaveBeenCalled(); // no refresh when nothing replayed
    stop();
  });

  it('replays when the app returns to the foreground', async () => {
    const appStateSpy = jest.spyOn(AppState, 'addEventListener');
    mockActOnThread.mockResolvedValue({});
    const client = new QueryClient();
    jest.spyOn(client, 'invalidateQueries').mockResolvedValue();

    const stop = startOutboxReplay(client);
    await flush();
    await queueIfOffline(networkFailure(), {
      kind: 'thread_action',
      threadId: 'thr_12',
      action: 'archive',
    });

    mockNetworkState = { isConnected: true, isInternetReachable: true };
    const handler = appStateSpy.mock.calls[0]?.[1] as (state: string) => void;
    expect(handler).toBeDefined();
    handler('active');
    await flush();

    expect(mockActOnThread).toHaveBeenCalledWith('thr_12', 'archive');
    stop();
    appStateSpy.mockRestore();
  });

  it('stops replaying after the returned cleanup runs', async () => {
    mockActOnThread.mockResolvedValue({});
    const client = new QueryClient();
    const stop = startOutboxReplay(client);
    await flush();

    await queueIfOffline(networkFailure(), {
      kind: 'thread_action',
      threadId: 'thr_13',
      action: 'archive',
    });
    stop();
    expect(networkListeners).toHaveLength(0); // listener actually removed

    expect(mockActOnThread).not.toHaveBeenCalled();
    expect(getOutbox().queuedCount).toBe(1);
  });
});
