import type { ReactNode } from 'react';
import type { Thread } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

// ---------------------------------------------------------------------------
// Mocks (mirrors use-mail-triage.test.tsx)
// ---------------------------------------------------------------------------

const demoState = vi.hoisted(() => ({ value: false }));
vi.mock('@/lib/demo', () => ({
  get DEMO_MODE() {
    return demoState.value;
  },
}));

const listThreadsMock = vi.fn();
vi.mock('@/lib/api', () => ({
  getApiClient: () => ({
    listThreads: (...args: unknown[]) => listThreadsMock(...args),
  }),
}));

const getMockThreadsMock = vi.fn();
vi.mock('@/lib/mail-mock', () => ({
  applyMockAction: vi.fn(),
  applyMockLabel: vi.fn(),
  getMockLabels: vi.fn(() => []),
  getMockThread: vi.fn(() => null),
  getMockThreads: (...args: unknown[]) => getMockThreadsMock(...args),
  mockAiCompose: vi.fn(() => ({ text: '' })),
  mockArchiveOlderThan: vi.fn(() => 0),
  mockBulkAction: vi.fn(),
  mockRemindThread: vi.fn(),
  mockSnoozeThread: vi.fn(),
  mockUnsnoozeThread: vi.fn(),
  mockUnsubscribe: vi.fn(() => ({ method: 'link' })),
}));

vi.mock('sonner', () => ({
  toast: { error: vi.fn(), success: vi.fn(), info: vi.fn() },
}));

// Imported after the mocks above so use-mail.ts picks them up.
import { useThreadList } from '@/lib/use-mail';

// ---------------------------------------------------------------------------
// Fixtures / helpers
// ---------------------------------------------------------------------------

function makeThread(id: string): Thread {
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
  };
}

function createWrapper() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  demoState.value = false;
});

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('useThreadList infinite pagination', () => {
  it('merges pages in order; nextCursor drives hasNextPage and the follow-up cursor', async () => {
    listThreadsMock
      .mockResolvedValueOnce({ items: [makeThread('t1'), makeThread('t2')], nextCursor: 'c2' })
      .mockResolvedValueOnce({ items: [makeThread('t3')], nextCursor: null });

    const { result } = renderHook(() => useThreadList({ split: 'important' }), {
      wrapper: createWrapper(),
    });

    await waitFor(() => expect(result.current.data).toBeDefined());
    expect(result.current.data!.items.map((t) => t.id)).toEqual(['t1', 't2']);
    expect(result.current.data!.source).toBe('api');
    expect(result.current.hasNextPage).toBe(true);
    expect(listThreadsMock).toHaveBeenNthCalledWith(
      1,
      expect.objectContaining({ split: 'important', cursor: undefined })
    );

    await result.current.fetchNextPage();

    await waitFor(() =>
      expect(result.current.data!.items.map((t) => t.id)).toEqual(['t1', 't2', 't3'])
    );
    expect(result.current.hasNextPage).toBe(false);
    expect(listThreadsMock).toHaveBeenNthCalledWith(2, expect.objectContaining({ cursor: 'c2' }));
  });

  it('a null nextCursor on the first page means no next page', async () => {
    listThreadsMock.mockResolvedValueOnce({ items: [makeThread('t1')], nextCursor: null });

    const { result } = renderHook(() => useThreadList({ split: 'important' }), {
      wrapper: createWrapper(),
    });

    await waitFor(() => expect(result.current.data).toBeDefined());
    expect(result.current.hasNextPage).toBe(false);
  });

  it('demo fallback stays single-page', async () => {
    demoState.value = true;
    listThreadsMock.mockRejectedValue(new Error('unreachable'));
    getMockThreadsMock.mockReturnValue({ items: [makeThread('d1')], nextCursor: null });

    const { result } = renderHook(() => useThreadList({ split: 'important' }), {
      wrapper: createWrapper(),
    });

    await waitFor(() => expect(result.current.data).toBeDefined());
    expect(result.current.data!.items.map((t) => t.id)).toEqual(['d1']);
    expect(result.current.data!.source).toBe('demo');
    expect(result.current.hasNextPage).toBe(false);
  });
});
