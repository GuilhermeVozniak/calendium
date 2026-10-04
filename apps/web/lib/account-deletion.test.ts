import { QueryClient, QueryObserver } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const order: string[] = [];
const invalidateMock = vi.fn(() => order.push('invalidateAccessToken'));
const suspendMock = vi.fn(() => order.push('suspendApi'));
const resumeMock = vi.fn(() => order.push('resumeApi'));
vi.mock('@/lib/auth-client', () => ({
  invalidateAccessToken: () => invalidateMock(),
  suspendApi: () => suspendMock(),
  resumeApi: () => resumeMock(),
}));
const clearActingAsMock = vi.fn(() => order.push('clearActingAs'));
vi.mock('@/lib/act-as', () => ({ clearActingAs: () => clearActingAsMock() }));
const resetTourSessionMock = vi.fn(() => order.push('resetTourSession'));
vi.mock('@/lib/tour-state', () => ({ resetTourSession: () => resetTourSessionMock() }));
const clearOfflineStateMock = vi.fn(async () => {
  order.push('clearOfflineState');
});
vi.mock('@/lib/offline/queue', () => ({ clearOfflineState: () => clearOfflineStateMock() }));

import {
  beginAccountDeletion,
  endAccountDeletion,
  isAccountDeletionInProgress,
  leaveAfterAccountDeletion,
  scrubLocalStateAfterDeletion,
} from './account-deletion';

function stubServiceWorker() {
  const unsubscribe = vi.fn(async () => true);
  const unregister = vi.fn(async () => true);
  const registration = { pushManager: { getSubscription: vi.fn(async () => ({ unsubscribe })) }, unregister };
  Object.defineProperty(navigator, 'serviceWorker', {
    configurable: true,
    value: { getRegistrations: vi.fn(async () => [registration]) },
  });
  return { unsubscribe, unregister };
}

beforeEach(() => {
  endAccountDeletion();
  order.length = 0;
  vi.clearAllMocks();
  window.localStorage.clear();
});

afterEach(() => {
  Reflect.deleteProperty(navigator, 'serviceWorker');
  vi.restoreAllMocks();
});

describe('deletion-in-progress flag', () => {
  it('is set by beginAccountDeletion and cleared by endAccountDeletion', async () => {
    expect(isAccountDeletionInProgress()).toBe(false);
    await beginAccountDeletion();
    expect(isAccountDeletionInProgress()).toBe(true);
    endAccountDeletion();
    expect(isAccountDeletionInProgress()).toBe(false);
  });
});

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

// C-1 (web): the deleting tab must not reach the API while the purge runs or
// after it — a request carrying the about-to-be-deleted user's JWT is exactly
// what could race the purge. beginAccountDeletion pauses everything first.
describe('beginAccountDeletion pauses all API activity', () => {
  it('suspends the API, cancels in-flight queries and stops refetch intervals until endAccountDeletion', async () => {
    const client = new QueryClient();
    client.mount(); // as QueryClientProvider does: online/focus changes reach the cache
    let aborted = 0;
    const queryFn = vi.fn(
      ({ signal }: { signal: AbortSignal }) =>
        new Promise<number>((resolve, reject) => {
          const timer = setTimeout(() => resolve(1), 5);
          signal.addEventListener('abort', () => {
            clearTimeout(timer);
            aborted += 1;
            reject(new Error('aborted'));
          });
        })
    );
    const observer = new QueryObserver(client, { queryKey: ['poll'], queryFn, refetchInterval: 15 });
    const unsubscribe = observer.subscribe(() => {});
    await vi.waitFor(() => expect(queryFn.mock.calls.length).toBeGreaterThan(1));
    // Park the next fetch in flight so the pause has something to cancel.
    queryFn.mockImplementation(
      ({ signal }: { signal: AbortSignal }) =>
        new Promise<number>((_resolve, reject) => {
          signal.addEventListener('abort', () => {
            aborted += 1;
            reject(new Error('aborted'));
          });
        })
    );
    const before = queryFn.mock.calls.length;
    await vi.waitFor(() => expect(queryFn.mock.calls.length).toBeGreaterThan(before));

    await beginAccountDeletion(client);
    expect(suspendMock).toHaveBeenCalledTimes(1);
    expect(isAccountDeletionInProgress()).toBe(true);
    expect(aborted).toBeGreaterThan(0);

    const paused = queryFn.mock.calls.length;
    await sleep(100); // several interval periods
    void client.invalidateQueries(); // an explicit refetch is held too
    void client.refetchQueries();
    await sleep(30);
    expect(queryFn.mock.calls.length).toBe(paused);

    queryFn.mockResolvedValue(2);
    endAccountDeletion();
    expect(resumeMock).toHaveBeenCalled();
    await vi.waitFor(() => expect(queryFn.mock.calls.length).toBeGreaterThan(paused));
    unsubscribe();
    client.unmount();
    client.clear();
  });

  it('suspends the API before anything else and works without a query client', async () => {
    await beginAccountDeletion();
    expect(order[0]).toBe('suspendApi');
    expect(isAccountDeletionInProgress()).toBe(true);
  });
});

describe('scrubLocalStateAfterDeletion', () => {
  it('drops the JWT first, then clears the query cache, acting-as, tour, offline state, push and owned storage', async () => {
    const qc = new QueryClient();
    qc.setQueryData(['threads'], [{ id: 't1' }]);
    const { unsubscribe, unregister } = stubServiceWorker();
    window.localStorage.setItem('calendium.activeAccountId', 'a1');
    window.localStorage.setItem('calendium.web-push.device-id', 'd1');
    window.localStorage.setItem('calendium.tour.v1.u1', '{}');
    window.localStorage.setItem('calendium-theme', 'dark');
    window.localStorage.setItem('someone-else', 'keep');

    await scrubLocalStateAfterDeletion(qc);

    expect(order[0]).toBe('invalidateAccessToken');
    expect(clearActingAsMock).toHaveBeenCalledTimes(1);
    expect(resetTourSessionMock).toHaveBeenCalledTimes(1);
    expect(clearOfflineStateMock).toHaveBeenCalledTimes(1);
    expect(qc.getQueryData(['threads'])).toBeUndefined();
    expect(unsubscribe).toHaveBeenCalledTimes(1);
    expect(unregister).toHaveBeenCalledTimes(1);
    expect(window.localStorage.getItem('calendium.activeAccountId')).toBeNull();
    expect(window.localStorage.getItem('calendium.web-push.device-id')).toBeNull();
    expect(window.localStorage.getItem('calendium.tour.v1.u1')).toBeNull();
    expect(window.localStorage.getItem('calendium-theme')).toBeNull();
    expect(window.localStorage.getItem('someone-else')).toBe('keep');
  });

  it('never calls the API (no device unregister, no sign-out round trip)', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(null, { status: 204 }));
    stubServiceWorker();
    await scrubLocalStateAfterDeletion(new QueryClient());
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it('is best effort: a failing step never blocks the others', async () => {
    clearOfflineStateMock.mockRejectedValueOnce(new Error('idb down'));
    Object.defineProperty(navigator, 'serviceWorker', {
      configurable: true,
      value: { getRegistrations: vi.fn(async () => Promise.reject(new Error('no sw'))) },
    });
    window.localStorage.setItem('calendium.actAs.principalId', 'p1');
    await expect(scrubLocalStateAfterDeletion(new QueryClient())).resolves.toBeUndefined();
    expect(window.localStorage.getItem('calendium.actAs.principalId')).toBeNull();
  });

  it('works without service-worker support', async () => {
    await expect(scrubLocalStateAfterDeletion(new QueryClient())).resolves.toBeUndefined();
  });
});

describe('leaveAfterAccountDeletion', () => {
  it('hard-navigates to /goodbye with location.replace', () => {
    const replace = vi.fn();
    leaveAfterAccountDeletion(replace);
    expect(replace).toHaveBeenCalledWith('/goodbye');
  });
});
