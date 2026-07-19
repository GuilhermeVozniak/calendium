import type { ReactNode } from 'react';
import { ApiRequestError, type Thread } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

// ---------------------------------------------------------------------------
// Mocks (mirrors use-mail-triage.test.tsx, plus the offline pair)
// ---------------------------------------------------------------------------

const demoState = vi.hoisted(() => ({ value: false }));
vi.mock('@/lib/demo', () => ({
  get DEMO_MODE() {
    return demoState.value;
  },
}));

const onlineState = vi.hoisted(() => ({ value: true }));
vi.mock('@/lib/offline/connectivity', () => ({
  isOnline: () => onlineState.value,
  subscribeOnline: () => () => {},
  reportApiReachable: vi.fn(),
  useOnline: () => onlineState.value,
}));

const queueActionMock = vi.hoisted(() => vi.fn());
vi.mock('@/lib/offline/queue', () => ({
  isNetworkError: (err: unknown) =>
    err instanceof TypeError || (err instanceof Error && err.name === 'AbortError'),
  queueAction: (...args: unknown[]) => queueActionMock(...args),
  getOutbox: vi.fn(),
  newLocalDraftId: () => 'local-test-id',
  startOutboxReplay: () => () => {},
}));

const actOnThreadMock = vi.fn();
const snoozeThreadMock = vi.fn();
const markThreadOpenedMock = vi.fn();
vi.mock('@/lib/api', () => ({
  getApiClient: () => ({
    actOnThread: (...args: unknown[]) => actOnThreadMock(...args),
    snoozeThread: (...args: unknown[]) => snoozeThreadMock(...args),
    markThreadOpened: (...args: unknown[]) => markThreadOpenedMock(...args),
  }),
}));

const applyMockActionMock = vi.fn();
vi.mock('@/lib/mail-mock', () => ({
  applyMockAction: (...args: unknown[]) => applyMockActionMock(...args),
  applyMockLabel: vi.fn(),
  getMockLabels: vi.fn(() => []),
  getMockThread: vi.fn(() => null),
  getMockThreads: vi.fn(() => ({ items: [], nextCursor: null })),
  mockAiCompose: vi.fn(() => ({ text: '' })),
  mockArchiveOlderThan: vi.fn(() => 0),
  mockBulkAction: vi.fn(),
  mockRemindThread: vi.fn(),
  mockSnoozeThread: vi.fn(),
  mockUnsnoozeThread: vi.fn(),
  mockUnsubscribe: vi.fn(() => ({ method: 'link' })),
}));

const toastErrorMock = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    error: (...args: unknown[]) => toastErrorMock(...args),
    success: vi.fn(),
    info: vi.fn(),
  },
}));

// Imported after the mocks above so use-mail.ts picks them up.
import { mailUndo, useMailActions, type ThreadListResult } from '@/lib/use-mail';

// ---------------------------------------------------------------------------
// Fixtures / helpers
// ---------------------------------------------------------------------------

const THREADS_KEY = ['threads', 'important', null, ''] as const;

function makeThread(id: string, overrides: Partial<Thread> = {}): Thread {
  return {
    id,
    accountId: 'acc1',
    subject: `Subject ${id}`,
    snippet: 'snippet',
    participants: [],
    labelIds: [],
    split: 'important',
    messageCount: 1,
    unread: false,
    starred: false,
    lastMessageAt: new Date().toISOString(),
    openedAt: null,
    snoozedUntil: null,
    remindAt: null,
    unsubscribeMailto: null,
    unsubscribeUrl: null,
    unsubscribeOneClick: false,
    ...overrides,
  };
}

function seedThreads(queryClient: QueryClient, threads: Thread[]) {
  queryClient.setQueryData<ThreadListResult>(THREADS_KEY, {
    page: { items: threads, nextCursor: null },
    source: 'api',
  });
}

function readThreadIds(queryClient: QueryClient): string[] {
  const cached = queryClient.getQueryData<ThreadListResult>(THREADS_KEY);
  return cached ? cached.page.items.map((t) => t.id) : [];
}

function createWrapper(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
  };
}

function renderMailActions(queryClient: QueryClient) {
  return renderHook(() => useMailActions(), { wrapper: createWrapper(queryClient) });
}

beforeEach(() => {
  vi.clearAllMocks();
  demoState.value = false;
  onlineState.value = true;
  mailUndo.clear();
});

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('useMailActions offline queueing', () => {
  it('a network-failed archive keeps the optimistic removal and enqueues, without an error toast', async () => {
    const queryClient = new QueryClient();
    seedThreads(queryClient, [makeThread('t1')]);
    actOnThreadMock.mockRejectedValueOnce(new TypeError('fetch failed'));

    const { result } = renderMailActions(queryClient);
    const ok = await result.current.act('t1', 'archive');

    expect(ok).toBe(true);
    expect(readThreadIds(queryClient)).toEqual([]); // optimistic removal kept — no revert
    expect(queueActionMock).toHaveBeenCalledWith({
      kind: 'thread_action',
      threadId: 't1',
      action: 'archive',
    });
    expect(toastErrorMock).not.toHaveBeenCalled();
  });

  it('a server 5xx still reverts and toasts — the existing behavior is preserved', async () => {
    const queryClient = new QueryClient();
    seedThreads(queryClient, [makeThread('t1')]);
    actOnThreadMock.mockRejectedValueOnce(new ApiRequestError(500, 'internal', 'boom'));

    const { result } = renderMailActions(queryClient);
    const ok = await result.current.act('t1', 'archive');

    expect(ok).toBe(false);
    expect(readThreadIds(queryClient)).toEqual(['t1']); // reverted
    expect(queueActionMock).not.toHaveBeenCalled();
    expect(toastErrorMock).toHaveBeenCalled();
    expect(mailUndo.size).toBe(0);
  });

  it('known-offline pre-check queues without even attempting the API call', async () => {
    onlineState.value = false;
    const queryClient = new QueryClient();
    seedThreads(queryClient, [makeThread('t1')]);

    const { result } = renderMailActions(queryClient);
    const ok = await result.current.act('t1', 'archive');

    expect(ok).toBe(true);
    expect(actOnThreadMock).not.toHaveBeenCalled();
    expect(readThreadIds(queryClient)).toEqual([]);
    expect(queueActionMock).toHaveBeenCalledWith({
      kind: 'thread_action',
      threadId: 't1',
      action: 'archive',
    });
  });

  it('snooze offline enqueues a thread_snooze with the exact until timestamp', async () => {
    onlineState.value = false;
    const queryClient = new QueryClient();
    seedThreads(queryClient, [makeThread('t1')]);
    const until = '2026-07-20T09:00:00.000Z';

    const { result } = renderMailActions(queryClient);
    const ok = await result.current.snooze('t1', until);

    expect(ok).toBe(true);
    expect(snoozeThreadMock).not.toHaveBeenCalled();
    expect(queueActionMock).toHaveBeenCalledWith({ kind: 'thread_snooze', threadId: 't1', until });
  });

  it('markOpened enqueues a silent thread_open on a network error', async () => {
    const queryClient = new QueryClient();
    seedThreads(queryClient, [makeThread('t1', { unread: true })]);
    markThreadOpenedMock.mockRejectedValueOnce(new TypeError('fetch failed'));

    const { result } = renderMailActions(queryClient);
    await result.current.markOpened('t1');

    expect(queueActionMock).toHaveBeenCalledWith(
      { kind: 'thread_open', threadId: 't1' },
      { silent: true }
    );
  });

  it('markOpened does not queue on a real server rejection', async () => {
    const queryClient = new QueryClient();
    seedThreads(queryClient, [makeThread('t1', { unread: true })]);
    markThreadOpenedMock.mockRejectedValueOnce(new ApiRequestError(410, 'gone', 'nope'));

    const { result } = renderMailActions(queryClient);
    await result.current.markOpened('t1');

    expect(queueActionMock).not.toHaveBeenCalled();
  });
});
