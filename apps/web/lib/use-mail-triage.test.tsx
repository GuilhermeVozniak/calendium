import type { ReactNode } from 'react';
import type { Thread } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

// Toggleable DEMO_MODE backing this file's demo-mode test (d). Set per-test
// via `demoState.value`; the getter keeps use-mail.ts's live `DEMO_MODE`
// import in sync without needing vi.resetModules()/dynamic re-import (which
// would also mint a fresh `mailUndo` instance and defeat the shared-singleton
// clear() pattern the rest of these tests rely on).
const demoState = vi.hoisted(() => ({ value: false }));
vi.mock('@/lib/demo', () => ({
  get DEMO_MODE() {
    return demoState.value;
  },
}));

const actOnThreadMock = vi.fn();
const bulkThreadActionMock = vi.fn();
const setThreadLabelMock = vi.fn();
vi.mock('@/lib/api', () => ({
  getApiClient: () => ({
    actOnThread: (...args: unknown[]) => actOnThreadMock(...args),
    bulkThreadAction: (...args: unknown[]) => bulkThreadActionMock(...args),
    setThreadLabel: (...args: unknown[]) => setThreadLabelMock(...args),
  }),
}));

const applyMockActionMock = vi.fn();
const applyMockLabelMock = vi.fn();
const mockBulkActionMock = vi.fn();
vi.mock('@/lib/mail-mock', () => ({
  applyMockAction: (...args: unknown[]) => applyMockActionMock(...args),
  applyMockLabel: (...args: unknown[]) => applyMockLabelMock(...args),
  getMockLabels: vi.fn(() => []),
  getMockThread: vi.fn(() => null),
  getMockThreads: vi.fn(() => ({ items: [], nextCursor: null })),
  mockAiCompose: vi.fn(() => ({ text: '' })),
  mockArchiveOlderThan: vi.fn(() => 0),
  mockBulkAction: (...args: unknown[]) => mockBulkActionMock(...args),
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
import { mailUndo, useMailActions, type ThreadListData } from '@/lib/use-mail';

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
  queryClient.setQueryData<ThreadListData>(THREADS_KEY, {
    pages: [{ page: { items: threads, nextCursor: null }, source: 'api' }],
    pageParams: [undefined],
  });
}

function readThreadIds(queryClient: QueryClient): string[] {
  const cached = queryClient.getQueryData<ThreadListData>(THREADS_KEY);
  return cached ? cached.pages.flatMap((p) => p.page.items.map((t) => t.id)) : [];
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
  mailUndo.clear();
});

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('useMailActions undo/bulk honesty', () => {
  it('act(): a real API rejection reverts the cache and pushes no undo entry', async () => {
    const queryClient = new QueryClient();
    seedThreads(queryClient, [makeThread('t1')]);
    actOnThreadMock.mockRejectedValueOnce(new Error('network down'));

    const { result } = renderMailActions(queryClient);

    const ok = await result.current.act('t1', 'archive');

    expect(ok).toBe(false);
    expect(mailUndo.size).toBe(0);
    // 'archive' removes from the list optimistically; a failed real call
    // must revert that removal.
    expect(readThreadIds(queryClient)).toEqual(['t1']);
    expect(toastErrorMock).toHaveBeenCalled();
  });

  it('act(): success pushes an undo entry; undoLast() invokes the inverse API and resolves true', async () => {
    const queryClient = new QueryClient();
    seedThreads(queryClient, [makeThread('t1')]);
    actOnThreadMock.mockResolvedValue(undefined);

    const { result } = renderMailActions(queryClient);

    const ok = await result.current.act('t1', 'archive');
    expect(ok).toBe(true);
    expect(mailUndo.size).toBe(1);
    expect(readThreadIds(queryClient)).toEqual([]);

    const undone = await result.current.undoLast();

    expect(undone).toBe(true);
    expect(actOnThreadMock).toHaveBeenNthCalledWith(1, 't1', 'archive');
    expect(actOnThreadMock).toHaveBeenNthCalledWith(2, 't1', 'move_to_inbox');
  });

  it('bulkAct(): real mode with partial failedIds restores the failed ids, targets undo at the succeeded ids only, and toasts', async () => {
    const queryClient = new QueryClient();
    seedThreads(queryClient, [makeThread('id1'), makeThread('id2'), makeThread('id3')]);
    bulkThreadActionMock.mockResolvedValueOnce({ threads: [], failedIds: ['id2'] });
    bulkThreadActionMock.mockResolvedValue({ threads: [], failedIds: [] });

    const { result } = renderMailActions(queryClient);

    await result.current.bulkAct(['id1', 'id2'], 'archive');

    // id1 succeeded (archived => removed), id2 failed => restored, id3 was
    // never touched.
    expect(readThreadIds(queryClient).sort()).toEqual(['id2', 'id3']);
    expect(toastErrorMock).toHaveBeenCalledWith(expect.stringContaining('1 conversation'));
    expect(mailUndo.size).toBe(1);

    await result.current.undoLast();

    expect(bulkThreadActionMock).toHaveBeenNthCalledWith(2, {
      threadIds: ['id1'],
      action: 'move_to_inbox',
    });
  });

  it('bulkAct(): demo mode applies the mock fallback and still pushes an undo entry', async () => {
    demoState.value = true;
    const queryClient = new QueryClient();
    seedThreads(queryClient, [makeThread('id1'), makeThread('id2')]);
    bulkThreadActionMock.mockRejectedValueOnce(new Error('offline'));

    const { result } = renderMailActions(queryClient);

    await result.current.bulkAct(['id1', 'id2'], 'archive');

    expect(mockBulkActionMock).toHaveBeenCalledWith(['id1', 'id2'], 'archive');
    expect(mailUndo.size).toBe(1);
    // Demo mode never reverts the optimistic state.
    expect(readThreadIds(queryClient)).toEqual([]);
  });

  it('undoLast(): a failing inverse resolves false without rejecting, and toasts', async () => {
    const queryClient = new QueryClient();
    mailUndo.push({ label: 'Test entry', undo: () => Promise.reject(new Error('nope')) });

    const { result } = renderMailActions(queryClient);

    await expect(result.current.undoLast()).resolves.toBe(false);
    expect(toastErrorMock).toHaveBeenCalled();
  });

  it('setLabel(): undoing a label change does not push a new undo entry (no Z ping-pong)', async () => {
    const queryClient = new QueryClient();
    seedThreads(queryClient, [makeThread('t1')]);
    setThreadLabelMock.mockResolvedValue(undefined);

    const { result } = renderMailActions(queryClient);

    const ok = await result.current.setLabel('t1', 'lbl1', true);
    expect(ok).toBe(true);
    expect(mailUndo.size).toBe(1);

    const undone = await result.current.undoLast();

    expect(undone).toBe(true);
    expect(setThreadLabelMock).toHaveBeenNthCalledWith(2, 't1', 'lbl1', false);
    // The undo closure's own setLabel call must be non-undoable — otherwise
    // every Z press here would push a fresh undo entry and Z would toggle
    // the label forever instead of draining the stack.
    expect(mailUndo.size).toBe(0);
  });

  it('act(): Z pressed while the request is still in flight still undoes the action', async () => {
    const queryClient = new QueryClient();
    seedThreads(queryClient, [makeThread('t1')]);
    let resolveArchive!: () => void;
    actOnThreadMock.mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          resolveArchive = resolve;
        })
    );
    actOnThreadMock.mockResolvedValue(undefined); // the inverse call

    const { result } = renderMailActions(queryClient);

    const pending = result.current.act('t1', 'archive');
    // The row is already gone optimistically, so Z must have something to
    // undo right now — not only once the network round trip settles.
    expect(readThreadIds(queryClient)).toEqual([]);
    expect(mailUndo.size).toBe(1);

    const undoing = result.current.undoLast();
    expect(mailUndo.size).toBe(0);
    resolveArchive();

    await expect(pending).resolves.toBe(true);
    await expect(undoing).resolves.toBe(true);
    expect(actOnThreadMock).toHaveBeenNthCalledWith(1, 't1', 'archive');
    expect(actOnThreadMock).toHaveBeenNthCalledWith(2, 't1', 'move_to_inbox');
  });

  it('act(): a request that fails after Z was pressed mid-flight sends no inverse and leaves no entry', async () => {
    const queryClient = new QueryClient();
    seedThreads(queryClient, [makeThread('t1')]);
    let rejectArchive!: (err: Error) => void;
    actOnThreadMock.mockImplementationOnce(
      () =>
        new Promise<void>((_resolve, reject) => {
          rejectArchive = reject;
        })
    );

    const { result } = renderMailActions(queryClient);

    const pending = result.current.act('t1', 'archive');
    expect(mailUndo.size).toBe(1);
    const undoing = result.current.undoLast();
    rejectArchive(new Error('network down'));

    await expect(pending).resolves.toBe(false);
    // The rollback already restored the row, so the undo has nothing left
    // to do: it must not fire the inverse against a state that never changed.
    await expect(undoing).resolves.toBe(true);
    expect(actOnThreadMock).toHaveBeenCalledTimes(1);
    expect(readThreadIds(queryClient)).toEqual(['t1']);
    expect(mailUndo.size).toBe(0);
  });
});
